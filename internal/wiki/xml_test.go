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

func TestRevisionInlineContentAdmission(t *testing.T) {
	cases := []struct {
		name, body, wantText string
		wantErr bool
	}{
		{name: "inline", body: `<text xml:space="preserve">A &amp; B</text>`, wantText: "A & B"},
		{name: "cdata", body: `<text><![CDATA[<raw>]]></text>`, wantText: "<raw>"},
		{name: "empty-inline", body: `<text/>`, wantText: ""},
		{name: "empty-bytes-zero", body: `<text bytes="0"></text>`, wantText: ""},
		{name: "explicit-wikitext", body: `<model>wikitext</model><format>text/x-wiki</format><text>valid</text>`, wantText: "valid"},
		{name: "missing-text", body: ``, wantErr: true},
		{name: "deleted-stub", body: `<text deleted="deleted"/>`, wantErr: true},
		{name: "deleted-with-body", body: `<text deleted="deleted">untrusted</text>`, wantErr: true},
		{name: "external-location", body: `<text location="tt:123" bytes="9"/>`, wantErr: true},
		{name: "legacy-id-stub", body: `<text id="123"/>`, wantErr: true},
		{name: "nonzero-byte-stub", body: `<text bytes="4"/>`, wantErr: true},
		{name: "additional-content", body: `<text>Main</text><content><role>auxiliary</role><text>Other</text></content>`, wantErr: true},
		{name: "wrapped-slots", body: `<slots><slot role="main"><text>wrapped</text></slot></slots>`, wantErr: true},
		{name: "duplicate-main-text", body: `<text>first</text><text>second</text>`, wantErr: true},
		{name: "nested-markup", body: `<text><b>not inline</b></text>`, wantErr: true},
		{name: "unsupported-model", body: `<model>json</model><text>{}</text>`, wantErr: true},
		{name: "unsupported-format", body: `<format>application/json</format><text>{}</text>`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := `<mediawiki><page><title>Sample</title><ns>0</ns><id>99</id><revision><id>1</id><timestamp>2020-01-01T00:00:00Z</timestamp>` + tc.body + `</revision></page></mediawiki>`
			visited := 0
			err := EachPage(strings.NewReader(src), func(p Page) error {
				visited++
				if got := p.Revisions[0].Text; got != tc.wantText {
					t.Errorf("text = %q; want %q", got, tc.wantText)
				}
				return nil
			})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v; wantErr = %v", err, tc.wantErr)
			}
			if tc.wantErr && visited != 0 { t.Fatalf("invalid revision was admitted") }
			if !tc.wantErr && visited != 1 { t.Fatalf("valid revision was not admitted") }
		})
	}
}
