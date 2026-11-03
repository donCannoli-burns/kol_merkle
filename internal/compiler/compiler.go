// Package compiler commits one locally supplied MediaWiki export as a snapshot.
package compiler

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/donCannoli-burns/kol_merkle/internal/merkle"
	"github.com/donCannoli-burns/kol_merkle/internal/store"
	"github.com/donCannoli-burns/kol_merkle/internal/wiki"
)

// Compile commits both current-page and full-history commitments, with the
// byte-exact SHA-256 of the supplied XML export bound into the snapshot root.
func Compile(ctx context.Context, s *store.Store, origin string, r io.Reader) (string, int, error) {
	if origin == "" {
		return "", 0, fmt.Errorf("origin required")
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()
	if err := store.BeginImportHistory(ctx, tx); err != nil {
		return "", 0, err
	}
	byPage := map[int64]merkle.Entry{}
	digest := sha256.New()
	counter := &byteCounter{reader: io.TeeReader(r, digest)}
	err = wiki.EachPage(counter, func(p wiki.Page) error {
		if _, exists := byPage[p.ID]; exists {
			return fmt.Errorf("duplicate source page ID %d", p.ID)
		}
		entry, err := store.ImportPage(ctx, tx, origin, p)
		if err != nil {
			return fmt.Errorf("import page %d: %w", p.ID, err)
		}
		byPage[p.ID] = entry
		return nil
	})
	if err != nil {
		return "", 0, err
	}
	if len(byPage) == 0 {
		return "", 0, fmt.Errorf("empty XML export")
	}
	entries := make([]merkle.Entry, 0, len(byPage))
	for _, e := range byPage {
		entries = append(entries, e)
	}
	pageRoot, err := merkle.Root(entries)
	if err != nil {
		return "", 0, err
	}
	historyRoot, nRevisions, err := store.StagedHistoryRoot(ctx, tx, origin)
	if err != nil {
		return "", 0, err
	}
	exportHash := hex.EncodeToString(digest.Sum(nil))
	root, err := merkle.SnapshotV2Root(origin, pageRoot, historyRoot, exportHash, counter.n)
	if err != nil {
		return "", 0, err
	}
	if err := store.SaveSnapshot(ctx, tx, origin, root, entries, pageRoot, historyRoot, exportHash, counter.n, nRevisions); err != nil {
		return "", 0, err
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE temp.import_history`); err != nil {
		return "", 0, err
	}
	if err := tx.Commit(); err != nil {
		return "", 0, err
	}
	return root, len(entries), nil
}

type byteCounter struct {
	reader io.Reader
	n      int64
}

func (c *byteCounter) Read(p []byte) (int, error) {
	n, err := c.reader.Read(p)
	c.n += int64(n)
	return n, err
}
