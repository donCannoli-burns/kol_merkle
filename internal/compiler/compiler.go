// Package compiler commits one locally supplied MediaWiki export as a snapshot.
package compiler

import (
	"context"
	"database/sql"
	"fmt"
	"io"

	"github.com/donCannoli-burns/kol_merkle/internal/merkle"
	"github.com/donCannoli-burns/kol_merkle/internal/store"
	"github.com/donCannoli-burns/kol_merkle/internal/wiki"
)

const MaxPageRevisions = 100000
const MaxTextBytes = 16 << 20

// Compile accepts a bounded stream and preserves all revisions in one transaction.
// Snapshot roots describe only the pages supplied in this import, not the whole wiki.
func Compile(ctx context.Context, s *store.Store, origin string, r io.Reader) (string, int, error) {
	if origin == "" {
		return "", 0, fmt.Errorf("origin required")
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()
	byPage := map[int64]merkle.Entry{}
	err = wiki.EachPage(r, func(p wiki.Page) error {
		if len(p.Revisions) > MaxPageRevisions {
			return fmt.Errorf("too many revisions in page %d", p.ID)
		}
		for _, rev := range p.Revisions {
			if len(rev.Text) > MaxTextBytes {
				return fmt.Errorf("page %d revision %d exceeds text limit", p.ID, rev.ID)
			}
		}
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
	root, err := merkle.Root(entries)
	if err != nil {
		return "", 0, err
	}
	if err = store.SaveSnapshot(ctx, tx, origin, root, entries); err != nil {
		return "", 0, err
	}
	if err = tx.Commit(); err != nil {
		return "", 0, err
	}
	return root, len(entries), nil
}
