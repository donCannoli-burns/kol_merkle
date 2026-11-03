package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"

	"github.com/donCannoli-burns/kol_merkle/internal/merkle"
)

// Verify validates all persistent objects, revisions, full-text records,
// historical snapshot memberships, and v2 snapshot commitments. V1 snapshots
// deliberately fail closed; their previous roots did not bind full history.
func (s *Store) Verify(ctx context.Context) (int, int, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	objects, err := verifyObjects(ctx, tx)
	if err != nil {
		return 0, 0, err
	}
	if err := verifyRevisionRelations(ctx, tx); err != nil {
		return objects, 0, err
	}
	if err := verifyFTS(ctx, tx); err != nil {
		return objects, 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO revision_fts(revision_fts) VALUES('integrity-check')`); err != nil {
		return objects, 0, fmt.Errorf("FTS5 internal index integrity failure: %w", err)
	}
	snapshots, err := verifySnapshots(ctx, tx)
	if err != nil {
		return objects, 0, err
	}
	if snapshots == 0 {
		return objects, 0, fmt.Errorf("no snapshots present to verify")
	}
	var uncommitted int64
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM revisions r WHERE NOT EXISTS (
      SELECT 1 FROM snapshot_history_entries h WHERE h.page_key=r.page_key AND h.source_revision_id=r.source_revision_id
    )`).Scan(&uncommitted)
	if err != nil {
		return objects, 0, err
	}
	if uncommitted != 0 {
		return objects, 0, fmt.Errorf("%d revisions have no historical snapshot commitment", uncommitted)
	}
	return objects, snapshots, nil
}

func verifyObjects(ctx context.Context, tx *sql.Tx) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT sha256,raw,byte_length FROM objects ORDER BY sha256`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var digest string
		var data []byte
		var size int64
		if err := rows.Scan(&digest, &data, &size); err != nil {
			return 0, err
		}
		hashed := sha256.Sum256(data)
		if int64(len(data)) != size || hex.EncodeToString(hashed[:]) != digest {
			return 0, fmt.Errorf("object integrity failure: %s", digest)
		}
		count++
	}
	return count, rows.Err()
}

func verifyRevisionRelations(ctx context.Context, tx *sql.Tx) error {
	var missing int64
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM revisions r
      LEFT JOIN pages p ON p.page_key=r.page_key
      LEFT JOIN objects o ON o.sha256=r.content_sha256
      WHERE p.page_key IS NULL OR o.sha256 IS NULL`).Scan(&missing)
	if err != nil {
		return err
	}
	if missing != 0 {
		return fmt.Errorf("%d revisions have missing page or content object", missing)
	}
	rows, err := tx.QueryContext(ctx, `SELECT p.page_key,p.origin,p.source_page_id,r.source_revision_id,r.title,r.captured_timestamp,r.content_sha256 FROM revisions r JOIN pages p ON p.page_key=r.page_key ORDER BY p.page_key,r.source_revision_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var pk, origin, title, timestamp, digest string
		var pageID, revID int64
		if err := rows.Scan(&pk, &origin, &pageID, &revID, &title, &timestamp, &digest); err != nil {
			return err
		}
		if PageKey(origin, pageID) != pk || revID <= 0 || title == "" || timestamp == "" || len(digest) != 64 {
			return fmt.Errorf("revision identity/metadata mismatch for page %d revision %d", pageID, revID)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	var orphans int64
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pages p WHERE NOT EXISTS (SELECT 1 FROM revisions r WHERE r.page_key=p.page_key)`).Scan(&orphans)
	if err != nil {
		return err
	}
	if orphans != 0 {
		return fmt.Errorf("%d pages have no revisions", orphans)
	}
	return nil
}

// verifyFTS enforces both logical row equality and the storage classes expected
// by Search's Go Scan destinations. Numeric SQLite equality alone is unsafe:
// unindexed FTS5 fields may hold TEXT "101.0" that compares equal to INTEGER 101.
func verifyFTS(ctx context.Context, tx *sql.Tx) error {
	var revisions, fts, matched, duplicated int64
	for _, q := range []struct {
		query string
		dst   *int64
	}{
		{`SELECT COUNT(*) FROM revisions`, &revisions},
		{`SELECT COUNT(*) FROM revision_fts`, &fts},
		{`SELECT COUNT(*) FROM revision_fts f
          JOIN revisions r ON f.page_key=r.page_key AND f.source_revision_id=r.source_revision_id
          JOIN objects o ON o.sha256=r.content_sha256
          WHERE typeof(f.page_key)='text' AND typeof(f.source_revision_id)='integer'
            AND typeof(f.title)='text' AND typeof(f.body)='text'
            AND f.title=r.title COLLATE BINARY AND CAST(f.body AS BLOB)=o.raw`, &matched},
		{`SELECT COUNT(*) FROM (SELECT page_key,source_revision_id FROM revision_fts GROUP BY page_key,source_revision_id HAVING COUNT(*)<>1)`, &duplicated},
	} {
		if err := tx.QueryRowContext(ctx, q.query).Scan(q.dst); err != nil {
			return err
		}
	}
	if revisions != fts || revisions != matched || duplicated != 0 {
		return fmt.Errorf("FTS5 content/revision mismatch revisions=%d indexed=%d matched=%d duplicates=%d", revisions, fts, matched, duplicated)
	}
	return nil
}

type storedSnapshot struct {
	root, origin, algorithm string
	pages                   int
}

func verifySnapshots(ctx context.Context, tx *sql.Tx) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT root,origin,page_count,algorithm FROM snapshots ORDER BY root`)
	if err != nil {
		return 0, err
	}
	all := []storedSnapshot{}
	for rows.Next() {
		var s storedSnapshot
		if err := rows.Scan(&s.root, &s.origin, &s.pages, &s.algorithm); err != nil {
			rows.Close()
			return 0, err
		}
		all = append(all, s)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, s := range all {
		if s.algorithm != merkle.SnapshotAlgorithm {
			return 0, fmt.Errorf("unsupported/legacy snapshot %s (%s): full-history verification unavailable; reimport original export", s.root, s.algorithm)
		}
		if err := verifySnapshot(ctx, tx, s); err != nil {
			return 0, fmt.Errorf("snapshot %s: %w", s.root, err)
		}
	}
	return len(all), nil
}

func verifySnapshot(ctx context.Context, tx *sql.Tx, ss storedSnapshot) error {
	var pageRoot, historyRoot, exportHash string
	var bytesCount, revisionCount int64
	err := tx.QueryRowContext(ctx, `SELECT page_root,history_root,export_sha256,export_byte_length,revision_count FROM snapshot_attestations WHERE root=?`, ss.root).Scan(&pageRoot, &historyRoot, &exportHash, &bytesCount, &revisionCount)
	if err != nil {
		return fmt.Errorf("missing v2 attestation: %w", err)
	}
	if bytesCount <= 0 || revisionCount <= 0 {
		return fmt.Errorf("invalid source manifest counts")
	}
	var broken int64
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM snapshot_entries se
        LEFT JOIN pages p ON se.page_key=p.page_key
        LEFT JOIN revisions r ON se.page_key=r.page_key AND se.source_revision_id=r.source_revision_id
        WHERE se.root=? AND (p.page_key IS NULL OR r.page_key IS NULL OR p.origin<>? OR
          p.source_page_id<>se.source_page_id OR p.namespace<>se.namespace OR
          r.title<>se.title OR r.content_sha256<>se.content_sha256)`, ss.root, ss.origin).Scan(&broken)
	if err != nil {
		return err
	}
	if broken != 0 {
		return fmt.Errorf("%d current snapshot entries disagree with source records", broken)
	}
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM snapshot_history_entries h
        LEFT JOIN pages p ON h.page_key=p.page_key
        LEFT JOIN revisions r ON h.page_key=r.page_key AND h.source_revision_id=r.source_revision_id
        WHERE h.root=? AND (p.page_key IS NULL OR r.page_key IS NULL OR p.origin<>? OR
          p.source_page_id<>h.source_page_id OR p.namespace<>h.namespace OR
          r.title<>h.title OR r.captured_timestamp<>h.captured_timestamp OR
          r.content_sha256<>h.content_sha256)`, ss.root, ss.origin).Scan(&broken)
	if err != nil {
		return err
	}
	if broken != 0 {
		return fmt.Errorf("%d history records disagree with retained revisions", broken)
	}

	erows, err := tx.QueryContext(ctx, `SELECT page_key,source_page_id,namespace,title,source_revision_id,content_sha256 FROM snapshot_entries WHERE root=?`, ss.root)
	if err != nil {
		return err
	}
	entries := []merkle.Entry{}
	current := map[int64]merkle.Entry{}
	for erows.Next() {
		var pk string
		e := merkle.Entry{Origin: ss.origin}
		if err := erows.Scan(&pk, &e.PageID, &e.Namespace, &e.Title, &e.RevisionID, &e.ContentHash); err != nil {
			erows.Close()
			return err
		}
		if pk != PageKey(ss.origin, e.PageID) {
			erows.Close()
			return fmt.Errorf("snapshot page-key mismatch")
		}
		if _, ok := current[e.PageID]; ok {
			erows.Close()
			return fmt.Errorf("duplicate page ID in current snapshot")
		}
		entries = append(entries, e)
		current[e.PageID] = e
	}
	err = erows.Err()
	erows.Close()
	if err != nil {
		return err
	}
	computedPageRoot, err := merkle.Root(entries)
	if err != nil {
		return err
	}
	if len(entries) != ss.pages || computedPageRoot != pageRoot {
		return fmt.Errorf("current-page Merkle commitment mismatch")
	}

	hrows, err := tx.QueryContext(ctx, `SELECT page_key,source_page_id,namespace,title,source_revision_id,captured_timestamp,content_sha256 FROM snapshot_history_entries WHERE root=? ORDER BY source_page_id,source_revision_id`, ss.root)
	if err != nil {
		return err
	}
	accumulator := merkle.NewHistoryAccumulator()
	newest := map[int64]struct {
		entry     merkle.Entry
		timestamp string
	}{}
	for hrows.Next() {
		var pk string
		r := merkle.HistoricalRevision{Origin: ss.origin}
		if err := hrows.Scan(&pk, &r.PageID, &r.Namespace, &r.Title, &r.RevisionID, &r.Timestamp, &r.ContentHash); err != nil {
			hrows.Close()
			return err
		}
		if pk != PageKey(ss.origin, r.PageID) {
			hrows.Close()
			return fmt.Errorf("history page-key mismatch")
		}
		if err := accumulator.Add(r); err != nil {
			hrows.Close()
			return err
		}
		old, ok := newest[r.PageID]
		if !ok || r.Timestamp > old.timestamp || (r.Timestamp == old.timestamp && r.RevisionID > old.entry.RevisionID) {
			newest[r.PageID] = struct {
				entry     merkle.Entry
				timestamp string
			}{
				entry: merkle.Entry{Origin: ss.origin, PageID: r.PageID, Namespace: r.Namespace, Title: r.Title, RevisionID: r.RevisionID, ContentHash: r.ContentHash}, timestamp: r.Timestamp,
			}
		}
	}
	err = hrows.Err()
	hrows.Close()
	if err != nil {
		return err
	}
	calculatedHistory, count, err := accumulator.Sum()
	if err != nil {
		return err
	}
	if calculatedHistory != historyRoot || count != revisionCount {
		return fmt.Errorf("historical revision commitment mismatch")
	}
	if len(current) != len(newest) {
		return fmt.Errorf("current/history page membership mismatch")
	}
	for id, latest := range newest {
		cur, ok := current[id]
		if !ok || cur != latest.entry {
			return fmt.Errorf("current snapshot diverges from latest revision page %d", id)
		}
	}
	expectedRoot, err := merkle.SnapshotV2Root(ss.origin, pageRoot, historyRoot, exportHash, bytesCount)
	if err != nil {
		return err
	}
	if expectedRoot != ss.root {
		return fmt.Errorf("v2 snapshot root mismatch")
	}
	return nil
}

// VerifySource independently checks the byte-exact source export against one
// v2 snapshot attestation. Retain the export externally for strong provenance.
func (s *Store) VerifySource(ctx context.Context, root string, exportHash string, sourceBytes int64) error {
	var storedHash string
	var storedBytes int64
	err := s.DB.QueryRowContext(ctx, `SELECT export_sha256,export_byte_length FROM snapshot_attestations WHERE root=?`, root).Scan(&storedHash, &storedBytes)
	if err != nil {
		return err
	}
	if exportHash != storedHash || sourceBytes != storedBytes {
		return fmt.Errorf("source export differs from recorded snapshot")
	}
	return nil
}
