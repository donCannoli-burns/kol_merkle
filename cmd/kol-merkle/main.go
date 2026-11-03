package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/donCannoli-burns/kol_merkle/internal/compiler"
	"github.com/donCannoli-burns/kol_merkle/internal/store"
)

func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) < 1 {
		return errors.New("usage: kol-merkle compile|search|verify [flags]")
	}
	switch args[0] {
	case "compile":
		f := flag.NewFlagSet("compile", flag.ContinueOnError)
		file := f.String("xml", "", "path to local MediaWiki XML export")
		dbfile := f.String("db", "kol-merkle.sqlite", "SQLite database path")
		origin := f.String("origin", "", "stable, explicitly specified wiki source identity")
		maxMiB := f.Int64("max-mib", 2048, "maximum XML input size in MiB")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if *file == "" || *origin == "" || *maxMiB < 1 || *maxMiB > 16384 {
			return errors.New("compile requires -xml, -origin and max-mib 1..16384")
		}
		src, err := os.Open(*file)
		if err != nil {
			return err
		}
		defer src.Close()
		st, err := src.Stat()
		if err != nil {
			return err
		}
		bound := *maxMiB << 20
		if st.Size() > bound {
			return fmt.Errorf("XML file exceeds maximum allowed bytes")
		}
		s, err := store.Open(ctx, *dbfile)
		if err != nil {
			return err
		}
		defer s.Close()
		root, n, err := compiler.Compile(ctx, s, *origin, io.LimitReader(src, bound+1))
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(map[string]any{"snapshot_root": root, "pages": n, "origin": *origin})
	case "verify":
		f := flag.NewFlagSet("verify", flag.ContinueOnError)
		dbfile := f.String("db", "kol-merkle.sqlite", "SQLite database path")
		source := f.String("xml", "", "optional original XML export for independent byte-exact provenance verification")
		root := f.String("root", "", "snapshot root required with -xml")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if (*source == "") != (*root == "") {
			return errors.New("verify requires both -xml and -root when checking original export")
		}
		if _, err := os.Stat(*dbfile); err != nil {
			return fmt.Errorf("open existing database: %w", err)
		}
		s, err := store.Open(ctx, *dbfile)
		if err != nil {
			return err
		}
		defer s.Close()
		objects, snapshots, err := s.Verify(ctx)
		if err != nil {
			return err
		}
		sourceVerified := false
		if *source != "" {
			file, err := os.Open(*source)
			if err != nil {
				return err
			}
			defer file.Close()
			h := sha256.New()
			n, err := io.Copy(h, file)
			if err != nil {
				return err
			}
			if err := s.VerifySource(ctx, *root, hex.EncodeToString(h.Sum(nil)), n); err != nil {
				return err
			}
			sourceVerified = true
		}
		return json.NewEncoder(out).Encode(map[string]any{"verified": true, "objects": objects, "snapshots": snapshots, "source_verified": sourceVerified})
	case "search":
		f := flag.NewFlagSet("search", flag.ContinueOnError)
		dbfile := f.String("db", "kol-merkle.sqlite", "SQLite database path")
		query := f.String("q", "", "FTS5 search expression")
		limit := f.Int("limit", 10, "results, 1..100")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if *query == "" {
			return errors.New("search requires -q")
		}
		if _, err := os.Stat(*dbfile); err != nil {
			return fmt.Errorf("open existing database: %w", err)
		}
		s, err := store.Open(ctx, *dbfile)
		if err != nil {
			return err
		}
		defer s.Close()
		hits, err := s.Search(ctx, *query, *limit)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(hits)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "kol-merkle:", err)
		os.Exit(1)
	}
}
