// Package store owns durable, non-destructive SQLite storage and integrity checks.
package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"

	_ "modernc.org/sqlite"

	"github.com/donCannoli-burns/kol_merkle/internal/merkle"
	"github.com/donCannoli-burns/kol_merkle/internal/wiki"
)

const schema = `
PRAGMA foreign_keys=ON;
CREATE TABLE IF NOT EXISTS pages (
  page_key TEXT PRIMARY KEY, origin TEXT NOT NULL, source_page_id INTEGER NOT NULL,
  namespace INTEGER NOT NULL, initial_title TEXT NOT NULL,
  UNIQUE(origin,source_page_id)
);
CREATE TABLE IF NOT EXISTS objects (
  sha256 TEXT PRIMARY KEY, raw BLOB NOT NULL, byte_length INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS revisions (
  page_key TEXT NOT NULL REFERENCES pages(page_key),
  source_revision_id INTEGER NOT NULL,
  title TEXT NOT NULL, captured_timestamp TEXT NOT NULL,
  content_sha256 TEXT NOT NULL REFERENCES objects(sha256),
  PRIMARY KEY(page_key,source_revision_id)
);
CREATE VIRTUAL TABLE IF NOT EXISTS revision_fts USING fts5(page_key UNINDEXED, source_revision_id UNINDEXED, title, body);
CREATE TABLE IF NOT EXISTS snapshots (
  root TEXT PRIMARY KEY, origin TEXT NOT NULL, page_count INTEGER NOT NULL, algorithm TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS snapshot_entries (
  root TEXT NOT NULL REFERENCES snapshots(root),
  page_key TEXT NOT NULL REFERENCES pages(page_key),
  source_revision_id INTEGER NOT NULL,
  source_page_id INTEGER NOT NULL, namespace INTEGER NOT NULL,
  title TEXT NOT NULL, content_sha256 TEXT NOT NULL REFERENCES objects(sha256),
  PRIMARY KEY(root,page_key)
);

CREATE TABLE IF NOT EXISTS snapshot_attestations (
  root TEXT PRIMARY KEY REFERENCES snapshots(root),
  page_root TEXT NOT NULL, history_root TEXT NOT NULL,
  export_sha256 TEXT NOT NULL, export_byte_length INTEGER NOT NULL,
  revision_count INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS snapshot_history_entries (
  root TEXT NOT NULL REFERENCES snapshots(root),
  page_key TEXT NOT NULL REFERENCES pages(page_key),
  source_page_id INTEGER NOT NULL, namespace INTEGER NOT NULL,
  title TEXT NOT NULL, source_revision_id INTEGER NOT NULL,
  captured_timestamp TEXT NOT NULL,
  content_sha256 TEXT NOT NULL REFERENCES objects(sha256),
  PRIMARY KEY(root,page_key,source_revision_id)
);
`

type Store struct{ DB *sql.DB }

func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init SQLite: %w", err)
	}
	return &Store{DB: db}, nil
}
func (s *Store) Close() error { return s.DB.Close() }

func PageKey(origin string, pageID int64) string {
	sum := sha256.Sum256([]byte("kol_merkle/page/v1\x00" + origin + "\x00" + strconv.FormatInt(pageID, 10)))
	return hex.EncodeToString(sum[:])
}

// ImportPage adds immutable objects and revisions inside the caller's transaction.
// Imported revisions with conflicting content are rejected, never replaced.
func ImportPage(ctx context.Context, tx *sql.Tx, origin string, p wiki.Page) (merkle.Entry, error) {
	pk := PageKey(origin, p.ID)
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO pages(page_key,origin,source_page_id,namespace,initial_title) VALUES(?,?,?,?,?)`, pk, origin, p.ID, p.Namespace, p.Title); err != nil {
		return merkle.Entry{}, err
	}
	var ns int
	if err := tx.QueryRowContext(ctx, `SELECT namespace FROM pages WHERE page_key=?`, pk).Scan(&ns); err != nil {
		return merkle.Entry{}, err
	}
	if ns != p.Namespace {
		return merkle.Entry{}, fmt.Errorf("namespace conflict for page %d", p.ID)
	}
	var chosen merkle.Entry
	var newest string
	for _, r := range p.Revisions {
		data := []byte(r.Text)
		sum := sha256.Sum256(data)
		hash := hex.EncodeToString(sum[:])
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO objects(sha256,raw,byte_length) VALUES(?,?,?)`, hash, data, len(data)); err != nil {
			return merkle.Entry{}, err
		}
		var existingBytes []byte
		if err := tx.QueryRowContext(ctx, `SELECT raw FROM objects WHERE sha256=?`, hash).Scan(&existingBytes); err != nil {
			return merkle.Entry{}, err
		}
		if !bytes.Equal(data, existingBytes) {
			return merkle.Entry{}, fmt.Errorf("content hash collision or corrupted object: %s", hash)
		}
		result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO revisions(page_key,source_revision_id,title,captured_timestamp,content_sha256) VALUES(?,?,?,?,?)`, pk, r.ID, p.Title, r.Timestamp, hash)
		if err != nil {
			return merkle.Entry{}, err
		}
		var previousHash, previousTitle, previousTimestamp string
		if err := tx.QueryRowContext(ctx, `SELECT content_sha256,title,captured_timestamp FROM revisions WHERE page_key=? AND source_revision_id=?`, pk, r.ID).Scan(&previousHash, &previousTitle, &previousTimestamp); err != nil {
			return merkle.Entry{}, err
		}
		if previousHash != hash || previousTitle != p.Title || previousTimestamp != r.Timestamp {
			return merkle.Entry{}, fmt.Errorf("revision %d metadata/content conflict on %s", r.ID, p.Title)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO temp.import_history(page_key,source_page_id,namespace,title,source_revision_id,captured_timestamp,content_sha256) VALUES(?,?,?,?,?,?,?)`, pk, p.ID, p.Namespace, p.Title, r.ID, r.Timestamp, hash); err != nil {
			return merkle.Entry{}, fmt.Errorf("staging revision: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return merkle.Entry{}, err
		}
		if affected > 0 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO revision_fts(page_key,source_revision_id,title,body) VALUES(?,?,?,?)`, pk, r.ID, p.Title, r.Text); err != nil {
				return merkle.Entry{}, err
			}
		}
		if chosen.PageID == 0 || r.Timestamp > newest || (r.Timestamp == newest && r.ID > chosen.RevisionID) {
			chosen = merkle.Entry{Origin: origin, PageID: p.ID, Namespace: p.Namespace, Title: p.Title, RevisionID: r.ID, ContentHash: hash}
			newest = r.Timestamp
		}
	}
	return chosen, nil
}

// BeginImportHistory stages the exact membership of this export transaction.
func BeginImportHistory(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TEMP TABLE import_history (
      page_key TEXT NOT NULL, source_page_id INTEGER NOT NULL, namespace INTEGER NOT NULL,
      title TEXT NOT NULL, source_revision_id INTEGER NOT NULL,
      captured_timestamp TEXT NOT NULL, content_sha256 TEXT NOT NULL,
      PRIMARY KEY(page_key,source_revision_id)
    )`)
	return err
}

// StagedHistoryRoot computes a canonical digest without collecting all revisions in memory.
func StagedHistoryRoot(ctx context.Context, tx *sql.Tx, origin string) (string, int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT source_page_id,namespace,title,source_revision_id,captured_timestamp,content_sha256 FROM temp.import_history ORDER BY source_page_id,source_revision_id`)
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()
	a := merkle.NewHistoryAccumulator()
	for rows.Next() {
		r := merkle.HistoricalRevision{Origin: origin}
		if err := rows.Scan(&r.PageID, &r.Namespace, &r.Title, &r.RevisionID, &r.Timestamp, &r.ContentHash); err != nil {
			return "", 0, err
		}
		if err := a.Add(r); err != nil {
			return "", 0, err
		}
	}
	if err := rows.Err(); err != nil {
		return "", 0, err
	}
	return a.Sum()
}

// SaveSnapshot persists an immutable v2 snapshot and its complete source history.
func SaveSnapshot(ctx context.Context, tx *sql.Tx, origin, root string, entries []merkle.Entry, pageRoot, historyRoot, sourceHash string, sourceBytes, revisionCount int64) error {
	if sourceBytes <= 0 || revisionCount <= 0 {
		return fmt.Errorf("empty source manifest")
	}
	if expected, err := merkle.SnapshotV2Root(origin, pageRoot, historyRoot, sourceHash, sourceBytes); err != nil || expected != root {
		return fmt.Errorf("invalid v2 snapshot commitment: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO snapshots(root,origin,page_count,algorithm) VALUES(?,?,?,?)`, root, origin, len(entries), merkle.SnapshotAlgorithm); err != nil {
		return err
	}
	var oldOrigin, algo string
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT origin,page_count,algorithm FROM snapshots WHERE root=?`, root).Scan(&oldOrigin, &count, &algo); err != nil {
		return err
	}
	if oldOrigin != origin || count != len(entries) || algo != merkle.SnapshotAlgorithm {
		return fmt.Errorf("snapshot metadata collision")
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO snapshot_attestations(root,page_root,history_root,export_sha256,export_byte_length,revision_count) VALUES(?,?,?,?,?,?)`, root, pageRoot, historyRoot, sourceHash, sourceBytes, revisionCount); err != nil {
		return err
	}
	var p, h, src string
	var n, c int64
	if err := tx.QueryRowContext(ctx, `SELECT page_root,history_root,export_sha256,export_byte_length,revision_count FROM snapshot_attestations WHERE root=?`, root).Scan(&p, &h, &src, &n, &c); err != nil {
		return err
	}
	if p != pageRoot || h != historyRoot || src != sourceHash || n != sourceBytes || c != revisionCount {
		return fmt.Errorf("snapshot attestation collision")
	}
	for _, e := range entries {
		pk := PageKey(origin, e.PageID)
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO snapshot_entries(root,page_key,source_revision_id,source_page_id,namespace,title,content_sha256) VALUES(?,?,?,?,?,?,?)`, root, pk, e.RevisionID, e.PageID, e.Namespace, e.Title, e.ContentHash); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO snapshot_history_entries(root,page_key,source_page_id,namespace,title,source_revision_id,captured_timestamp,content_sha256) SELECT ?,page_key,source_page_id,namespace,title,source_revision_id,captured_timestamp,content_sha256 FROM temp.import_history`, root); err != nil {
		return err
	}
	var pages, history int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM snapshot_entries WHERE root=?`, root).Scan(&pages); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM snapshot_history_entries WHERE root=?`, root).Scan(&history); err != nil {
		return err
	}
	if pages != int64(len(entries)) || history != revisionCount {
		return fmt.Errorf("snapshot entry count mismatch")
	}
	return nil
}

type SearchHit struct {
	Title      string `json:"title"`
	PageKey    string `json:"page_key"`
	RevisionID int64  `json:"revision_id"`
	Excerpt    string `json:"excerpt"`
}

func (s *Store) Search(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("limit must be 1..100")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT title,page_key,source_revision_id,snippet(revision_fts,3,'[',']',' … ',10) FROM revision_fts WHERE revision_fts MATCH ? ORDER BY rank LIMIT ?`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hits := []SearchHit{}
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(&h.Title, &h.PageKey, &h.RevisionID, &h.Excerpt); err != nil {
			return nil, err
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}
