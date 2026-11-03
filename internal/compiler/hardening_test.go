package compiler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/donCannoli-burns/kol_merkle/internal/store"
)

const testOrigin = "fixture://kol"

func fixtureArchive(t *testing.T) (*store.Store, []byte, string) {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "archive.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	content, err := os.ReadFile("../../testdata/mini-wiki.xml")
	if err != nil {
		t.Fatal(err)
	}
	root, _, err := Compile(ctx, s, testOrigin, strings.NewReader(string(content)))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Verify(ctx); err != nil {
		t.Fatalf("clean archive verification: %v", err)
	}
	return s, content, root
}

func TestAdversarialVerification(t *testing.T) {
	attacks := []struct{ name, sql string }{
		{"delete-older-revision", `DELETE FROM revisions WHERE source_revision_id=101`},
		{"rewrite-revision-timestamp", `UPDATE revisions SET captured_timestamp='1999-01-01T00:00:00Z' WHERE source_revision_id=101`},
		{"forge-fts-body", `UPDATE revision_fts SET body='never existed in any blob' WHERE source_revision_id=101`},
		{"duplicate-fts-row", `INSERT INTO revision_fts(page_key,source_revision_id,title,body) SELECT page_key,source_revision_id,title,body FROM revision_fts LIMIT 1`},
		{"fts-revision-id-text-decimal", `UPDATE revision_fts SET source_revision_id='101.0' WHERE source_revision_id=101`},
		{"fts-revision-id-text-integer", `UPDATE revision_fts SET source_revision_id='101' WHERE source_revision_id=101`},
		{"fts-revision-id-real", `UPDATE revision_fts SET source_revision_id=101.0 WHERE source_revision_id=101`},
		{"fts-body-storage-class", `UPDATE revision_fts SET body=CAST(body AS BLOB) WHERE source_revision_id=101`},
		{"delete-history-membership", `DELETE FROM snapshot_history_entries WHERE source_revision_id=101`},
		{"forge-snapshot-title", `UPDATE snapshot_entries SET title='untrue snapshot title' WHERE source_revision_id=102`},
		{"forge-history-timestamp", `UPDATE snapshot_history_entries SET captured_timestamp='1999-01-01T00:00:00Z' WHERE source_revision_id=101`},
		{"forge-export-byte-count", `UPDATE snapshot_attestations SET export_byte_length=export_byte_length+1`},
	}
	for _, a := range attacks {
		t.Run(a.name, func(t *testing.T) {
			ctx := context.Background()
			s, _, _ := fixtureArchive(t)
			if _, err := s.DB.ExecContext(ctx, a.sql); err != nil {
				t.Fatalf("attack setup failed: %v", err)
			}
			if _, _, err := s.Verify(ctx); err == nil {
				t.Fatalf("verification accepted %s", a.name)
			}
		})
	}
}

func TestExistingRevisionMetadataConflictRollback(t *testing.T) {
	ctx := context.Background()
	s, _, original := fixtureArchive(t)
	originalBytes, originalSnapshots, err := s.Verify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, changed := range []string{
		`<mediawiki><page><title>Zmobie</title><id>31</id><revision><id>102</id><timestamp>2035-01-02T00:00:00Z</timestamp><text>An undead monster. [[Item:1]]</text></revision></page></mediawiki>`,
		`<mediawiki><page><title>Forged title</title><id>31</id><revision><id>102</id><timestamp>2020-01-02T00:00:00Z</timestamp><text>An undead monster. [[Item:1]]</text></revision></page></mediawiki>`,
	} {
		if _, _, err := Compile(ctx, s, testOrigin, strings.NewReader(changed)); err == nil {
			t.Fatal("metadata conflict allowed")
		}
	}
	bytesAfter, snapshotsAfter, err := s.Verify(ctx)
	if err != nil || originalBytes != bytesAfter || originalSnapshots != snapshotsAfter {
		t.Fatalf("failed rollback: %v", err)
	}
	var retained string
	if err := s.DB.QueryRowContext(ctx, `SELECT root FROM snapshots`).Scan(&retained); err != nil || retained != original {
		t.Fatalf("snapshot changed after rollback: %v", err)
	}
}

func TestSourceManifestAndTimestampSensitivity(t *testing.T) {
	ctx := context.Background()
	s, xml, root := fixtureArchive(t)
	sum := sha256.Sum256(xml)
	if err := s.VerifySource(ctx, root, hex.EncodeToString(sum[:]), int64(len(xml))); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifySource(ctx, root, hex.EncodeToString(sum[:]), int64(len(xml)+1)); err == nil {
		t.Fatal("altered export length accepted")
	}
	if err := s.VerifySource(ctx, root, strings.Repeat("0", 64), int64(len(xml))); err == nil {
		t.Fatal("altered export hash accepted")
	}
	original, err := os.ReadFile("../../testdata/mini-wiki.xml")
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.ReplaceAll(string(original), "2020-01-01T00:00:00Z", "2019-01-01T00:00:00Z")
	if mutated == string(original) {
		t.Fatal("test fixture timestamp not found")
	}
	s2, err := store.Open(ctx, filepath.Join(t.TempDir(), "changed.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	changedRoot, _, err := Compile(ctx, s2, testOrigin, strings.NewReader(mutated))
	if err != nil {
		t.Fatal(err)
	}
	if changedRoot == root {
		t.Fatal("timestamp/source change did not change v2 root")
	}
}

func TestLegacySnapshotFailsClosed(t *testing.T) {
	ctx := context.Background()
	s, _, _ := fixtureArchive(t)
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO snapshots(root,origin,page_count,algorithm) VALUES('legacy','fixture://kol',0,'binary-merkle-sha256-v1')`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Verify(ctx); err == nil {
		t.Fatal("v1 archive was incorrectly treated as fully verified")
	}
}

// TestFTSRevisionIDTypeConfusion verifies the exact failure path reported by
// the independent audit with the real SQLite driver: coercible FTS TEXT joins
// an INTEGER revision, but Search cannot scan the string "101.0" into int64.
// The verifier must reject the archive before it can be reported as valid.
func TestFTSRevisionIDTypeConfusion(t *testing.T) {
	ctx := context.Background()
	s, _, _ := fixtureArchive(t)
	if _, err := s.DB.ExecContext(ctx, `UPDATE revision_fts SET source_revision_id='101.0' WHERE source_revision_id=101`); err != nil {
		t.Fatal(err)
	}
	var storageClass string
	var numericJoin int
	if err := s.DB.QueryRowContext(ctx, `SELECT typeof(source_revision_id) FROM revision_fts WHERE body='An early Zmobie.'`).Scan(&storageClass); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM revision_fts f JOIN revisions r ON f.page_key=r.page_key AND f.source_revision_id=r.source_revision_id WHERE r.source_revision_id=101`).Scan(&numericJoin); err != nil {
		t.Fatal(err)
	}
	if storageClass != "text" || numericJoin != 1 {
		t.Fatalf("failed to reproduce SQLite type confusion: storage=%q numeric_join=%d", storageClass, numericJoin)
	}
	if _, err := s.Search(ctx, "early", 10); err == nil {
		t.Fatal("expected Go int64 search scan error for textual revision ID 101.0")
	}
	if _, _, err := s.Verify(ctx); err == nil {
		t.Fatal("verify accepted coercible FTS revision ID TEXT despite broken Search")
	}
}

 
// A bad second page must not publish an earlier valid page or its content.
func TestUnavailableContentFailsClosedAndRollsBack(t *testing.T) {
    cases := []struct{name, text string}{
        {"deleted", `<text deleted="deleted"/>`},
        {"external", `<text location="tt:42" bytes="12"/>`},
        {"additional-slot", `<text>Main</text><content><role>auxiliary</role><text>Other</text></content>`},
        {"missing-text", ``},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            ctx := context.Background()
            s, _, root := fixtureArchive(t)
            initialObjects, initialSnapshots, err := s.Verify(ctx)
            if err != nil { t.Fatal(err) }
            src := `<mediawiki><page><title>Transient</title><ns>0</ns><id>900</id><revision><id>900</id><timestamp>2020-01-01T00:00:00Z</timestamp><text>must roll back</text></revision></page><page><title>Unavailable</title><ns>0</ns><id>901</id><revision><id>901</id><timestamp>2020-01-02T00:00:00Z</timestamp>` + tc.text + `</revision></page></mediawiki>`
            if _, _, err := Compile(ctx, s, testOrigin, strings.NewReader(src)); err == nil {
                t.Fatal("unsupported content was published")
            }
            objects, snapshots, err := s.Verify(ctx)
            if err != nil || objects != initialObjects || snapshots != initialSnapshots {
                t.Fatalf("archive changed after failure: objects=%d snapshots=%d error=%v", objects, snapshots, err)
            }
            var transients int
            if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM pages WHERE source_page_id IN (900,901)`).Scan(&transients); err != nil {
                t.Fatal(err)
            }
            if transients != 0 { t.Fatalf("partial import persisted: %d pages", transients) }
            var retained string
            if err := s.DB.QueryRowContext(ctx, `SELECT root FROM snapshots`).Scan(&retained); err != nil || retained != root {
                t.Fatalf("original root changed: %s (%v)", retained, err)
            }
        })
    }
}
