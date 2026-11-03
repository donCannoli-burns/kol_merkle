package merkle

import "testing"

func TestRootOrderAndContentSensitivity(t *testing.T) {
	a := Entry{Origin: "wiki.example", PageID: 1, Title: "One", RevisionID: 4, ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	b := Entry{Origin: "wiki.example", PageID: 2, Title: "Two", RevisionID: 7, ContentHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	r1, err := Root([]Entry{a, b})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := Root([]Entry{b, a})
	if err != nil || r1 != r2 {
		t.Fatalf("order-dependent: %q %q %v", r1, r2, err)
	}
	b.ContentHash = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	r3, err := Root([]Entry{a, b})
	if err != nil || r1 == r3 {
		t.Fatal("hash did not change with content")
	}
}
func TestDuplicate(t *testing.T) {
	a := Entry{Origin: "wiki.example", PageID: 1, Title: "One", RevisionID: 4, ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if _, err := Root([]Entry{a, a}); err == nil {
		t.Fatal("duplicate not detected")
	}
}
