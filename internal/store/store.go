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
		var previousHash string
		if err := tx.QueryRowContext(ctx, `SELECT content_sha256 FROM revisions WHERE page_key=? AND source_revision_id=?`, pk, r.ID).Scan(&previousHash); err != nil {
			return merkle.Entry{}, err
		}
		if previousHash != hash {
			return merkle.Entry{}, fmt.Errorf("revision %d hash conflict on %s", r.ID, p.Title)
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

// SaveSnapshot creates an immutable reference set. Identical input is idempotent.
func SaveSnapshot(ctx context.Context, tx *sql.Tx, origin, root string, entries []merkle.Entry) error {
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO snapshots(root,origin,page_count,algorithm) VALUES(?,?,?,?)`, root, origin, len(entries), "binary-merkle-sha256-v1"); err != nil {
		return err
	}
	var oldOrigin, algo string
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT origin,page_count,algorithm FROM snapshots WHERE root=?`, root).Scan(&oldOrigin, &count, &algo); err != nil {
		return err
	}
	if oldOrigin != origin || count != len(entries) || algo != "binary-merkle-sha256-v1" {
		return fmt.Errorf("snapshot metadata collision")
	}
	for _, e := range entries {
		pk := PageKey(origin, e.PageID)
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO snapshot_entries(root,page_key,source_revision_id,source_page_id,namespace,title,content_sha256) VALUES(?,?,?,?,?,?,?)`, root, pk, e.RevisionID, e.PageID, e.Namespace, e.Title, e.ContentHash); err != nil {
			return err
		}
	}
	var actual int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM snapshot_entries WHERE root=?`, root).Scan(&actual); err != nil {
		return err
	}
	if actual != len(entries) {
		return fmt.Errorf("snapshot entry count mismatch: %d versus %d", actual, len(entries))
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

// Verify recomputes raw blob SHA-256 hashes and every stored Merkle root.
func (s *Store) Verify(ctx context.Context) (int, int, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT sha256,raw,byte_length FROM objects ORDER BY sha256`)
	if err != nil {
		return 0, 0, err
	}
	nObjects := 0
	for rows.Next() {
		var hash string
		var data []byte
		var n int
		if err := rows.Scan(&hash, &data, &n); err != nil {
			rows.Close()
			return 0, 0, err
		}
		sum := sha256.Sum256(data)
		if len(data) != n || hex.EncodeToString(sum[:]) != hash {
			rows.Close()
			return 0, 0, fmt.Errorf("object integrity failure for %s", hash)
		}
		nObjects++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, 0, err
	}
	snapshots, err := s.DB.QueryContext(ctx, `SELECT root,origin,page_count FROM snapshots ORDER BY root`)
	if err != nil {
		return nObjects, 0, err
	}
	type snapshot struct {
		root, origin string
		count        int
	}
	all := []snapshot{}
	for snapshots.Next() {
		var x snapshot
		if err := snapshots.Scan(&x.root, &x.origin, &x.count); err != nil {
			snapshots.Close()
			return nObjects, 0, err
		}
		all = append(all, x)
	}
	err = snapshots.Err()
	snapshots.Close()
	if err != nil {
		return nObjects, 0, err
	}
	if len(all) == 0 {
		return nObjects, 0, errors.New("no snapshots present to verify")
	}
	for _, ss := range all {
		erows, err := s.DB.QueryContext(ctx, `SELECT source_page_id,namespace,title,source_revision_id,content_sha256 FROM snapshot_entries WHERE root=?`, ss.root)
		if err != nil {
			return nObjects, 0, err
		}
		entries := []merkle.Entry{}
		for erows.Next() {
			e := merkle.Entry{Origin: ss.origin}
			if err := erows.Scan(&e.PageID, &e.Namespace, &e.Title, &e.RevisionID, &e.ContentHash); err != nil {
				erows.Close()
				return nObjects, 0, err
			}
			entries = append(entries, e)
		}
		err = erows.Err()
		erows.Close()
		if err != nil {
			return nObjects, 0, err
		}
		computed, err := merkle.Root(entries)
		if err != nil {
			return nObjects, 0, err
		}
		if ss.count != len(entries) || computed != ss.root {
			return nObjects, 0, fmt.Errorf("snapshot integrity failure %s", ss.root)
		}
	}
	return nObjects, len(all), nil
}
