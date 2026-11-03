package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestCLIEndToEndAndFailClosed uses the real SQLite driver and production run()
// entrypoint, not a mocked store. Failed admissions cannot alter a known archive.
func TestCLIEndToEndAndFailClosed(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db := filepath.Join(dir, "cli.sqlite")
	export := filepath.Join("..", "..", "testdata", "mini-wiki.xml")

	var output bytes.Buffer
	if err := run(ctx, []string{"compile", "-xml", export, "-origin", "fixture://kol", "-db", db}, &output); err != nil {
		t.Fatal(err)
	}
	var compiled struct {
		Root  string `json:"snapshot_root"`
		Pages int    `json:"pages"`
	}
	if err := json.Unmarshal(output.Bytes(), &compiled); err != nil {
		t.Fatal(err)
	}
	if compiled.Pages != 2 || len(compiled.Root) != 64 {
		t.Fatalf("unexpected compile output: %+v", compiled)
	}
	output.Reset()
	if err := run(ctx, []string{"verify", "-db", db, "-root", compiled.Root, "-xml", export}, &output); err != nil {
		t.Fatal(err)
	}
	var verification struct {
		Verified       bool `json:"verified"`
		SourceVerified bool `json:"source_verified"`
		Objects        int  `json:"objects"`
		Snapshots      int  `json:"snapshots"`
	}
	if err := json.Unmarshal(output.Bytes(), &verification); err != nil {
		t.Fatal(err)
	}
	if !verification.Verified || !verification.SourceVerified || verification.Objects != 3 || verification.Snapshots != 1 {
		t.Fatalf("unexpected verification output: %+v", verification)
	}
	output.Reset()
	if err := run(ctx, []string{"search", "-db", db, "-q", "undead"}, &output); err != nil {
		t.Fatal(err)
	}
	var hits []struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(output.Bytes(), &hits); err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Title != "Zmobie" {
		t.Fatalf("unexpected search hits: %+v", hits)
	}

	badXML := filepath.Join(dir, "unavailable.xml")
	const rejected = `<mediawiki><page><title>Unavailable</title><ns>0</ns><id>600</id><revision><id>601</id><timestamp>2020-01-01T00:00:00Z</timestamp><text deleted="deleted"/></revision></page></mediawiki>`
	if err := os.WriteFile(badXML, []byte(rejected), 0600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := run(ctx, []string{"compile", "-xml", badXML, "-origin", "fixture://kol", "-db", db}, &output); err == nil {
		t.Fatal("CLI admitted deleted revision content")
	}
	output.Reset()
	if err := run(ctx, []string{"verify", "-db", db}, &output); err != nil {
		t.Fatal(err)
	}
	var after struct {
		Objects   int `json:"objects"`
		Snapshots int `json:"snapshots"`
	}
	if err := json.Unmarshal(output.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if after.Objects != verification.Objects || after.Snapshots != verification.Snapshots {
		t.Fatalf("failed CLI admission mutated archive: %+v", after)
	}
}
