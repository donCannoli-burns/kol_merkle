package compiler

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/donCannoli-burns/kol_merkle/internal/store"
)

func TestCompileIdempotentAndVerify(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "fixture.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	data, err := os.ReadFile("../../testdata/mini-wiki.xml")
	if err != nil {
		t.Fatal(err)
	}
	first, count, err := Compile(ctx, s, "fixture://kol", strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	second, count2, err := Compile(ctx, s, "fixture://kol", strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	if first != second || count != 2 || count2 != 2 {
		t.Fatalf("non-deterministic snapshot: %s %s %d %d", first, second, count, count2)
	}
	obj, snap, err := s.Verify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if obj != 3 || snap != 1 {
		t.Fatalf("unexpected archive counts objects=%d snapshots=%d", obj, snap)
	}
	hits, err := s.Search(ctx, "undead", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Title != "Zmobie" {
		t.Fatalf("unexpected search hits %+v", hits)
	}
	_, _, err = Compile(ctx, s, "fixture://kol", strings.NewReader(`<mediawiki><page><title>Zmobie</title><id>31</id><revision><id>102</id><timestamp>2020-01-02T00:00:00Z</timestamp><text>malicious replacement</text></revision></page></mediawiki>`))
	if err == nil {
		t.Fatal("conflicting revision not rejected")
	}
	obj2, snap2, err := s.Verify(ctx)
	if err != nil || obj2 != obj || snap2 != snap {
		t.Fatalf("rollback failed after collision: %v %d %d", err, obj2, snap2)
	}
}
