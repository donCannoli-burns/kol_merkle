package wiki

import (
	"strings"
	"testing"
)

func TestEachPage(t *testing.T) {
	xml := `<mediawiki xmlns="http://www.mediawiki.org/xml/export-0.11/"><siteinfo/><page><title>Zmobie</title><ns>0</ns><id>31</id><revision><id>102</id><timestamp>2020-01-02T00:00:00Z</timestamp><text xml:space="preserve">An undead monster.</text></revision></page></mediawiki>`
	n := 0
	err := EachPage(strings.NewReader(xml), func(p Page) error {
		n++
		if p.Title != "Zmobie" || p.ID != 31 || p.Revisions[0].Text != "An undead monster." {
			t.Fatalf("bad parse: %+v", p)
		}
		return nil
	})
	if err != nil || n != 1 {
		t.Fatalf("err=%v count=%d", err, n)
	}
}
func TestRejectInvalidPage(t *testing.T) {
	err := EachPage(strings.NewReader(`<mediawiki><page><title>Broken</title><id>0</id></page></mediawiki>`), func(Page) error { return nil })
	if err == nil {
		t.Fatal("expected invalid page rejection")
	}
}

func TestDecodedWikitextIsNotRawXMLText(t *testing.T) {
	src := `<mediawiki><page><title>XML entity</title><ns>0</ns><id>4</id><revision><id>1</id><timestamp>2020-01-01T00:00:00Z</timestamp><text>A &amp; B &lt;tag&gt; <![CDATA[<raw>]]></text></revision></page></mediawiki>`
	err := EachPage(strings.NewReader(src), func(p Page) error {
		if got := p.Revisions[0].Text; got != "A & B <tag> <raw>" {
			t.Errorf("decoded content: %q", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestIncrementalRevisionLengthRejection(t *testing.T) {
	src := `<mediawiki><page><title>Oversize</title><id>4</id><revision><id>1</id><timestamp>2020-01-01T00:00:00Z</timestamp><text>` + strings.Repeat("x", MaxRevisionTextBytes+1) + `</text></revision></page></mediawiki>`
	if err := EachPage(strings.NewReader(src), func(Page) error { t.Fatal("oversized page visited"); return nil }); err == nil {
		t.Fatal("oversize revision permitted")
	}
}

func TestRawXMLRunLimit(t *testing.T) {
	src := `<mediawiki><page><title>` + strings.Repeat("y", MaxRawXMLRunBytes+1) + `</title></page></mediawiki>`
	if err := EachPage(strings.NewReader(src), func(Page) error { return nil }); err == nil {
		t.Fatal("oversize raw token permitted")
	}
}
