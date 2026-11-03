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
		{name: "valid-meta", body: `<parentid>9</parentid><contributor><username>A</username></contributor><minor/><comment>ok</comment><origin>9</origin><model>wikitext</model><format>text/x-wiki</format><text bytes="5">hello</text><sha1>dummy</sha1>`, wantText: "hello"},
		{name: "entity-byte-count", body: `<text bytes="5">A &amp; B</text>`, wantText: "A & B"},
		{name: "utf8-byte-count", body: `<text bytes="2">é</text>`, wantText: "é"},
		{name: "zero-byte-count", body: `<text bytes="0"/>`, wantText: ""},
		{name: "duplicate-model", body: `<model>json</model><model>wikitext</model><format>text/x-wiki</format><text>body</text>`, wantErr: true},
		{name: "duplicate-model-valid", body: `<model>wikitext</model><model>wikitext</model><text>body</text>`, wantErr: true},
		{name: "duplicate-format", body: `<format>application/json</format><format>text/x-wiki</format><model>wikitext</model><text>body</text>`, wantErr: true},
		{name: "duplicate-format-valid", body: `<format>text/x-wiki</format><format>text/x-wiki</format><text>body</text>`, wantErr: true},
		{name: "duplicate-id", body: `<id>2</id><text>body</text>`, wantErr: true},
		{name: "duplicate-timestamp", body: `<timestamp>2021-01-01T00:00:00Z</timestamp><text>body</text>`, wantErr: true},
		{name: "duplicate-sha1", body: `<text>body</text><sha1>a</sha1><sha1>b</sha1>`, wantErr: true},
		{name: "missing-model-value", body: `<model/><text>body</text>`, wantErr: true},
		{name: "missing-format-value", body: `<format/><text>body</text>`, wantErr: true},
		{name: "byte-count-too-small", body: `<text bytes="4">hello</text>`, wantErr: true},
		{name: "byte-count-too-large", body: `<text bytes="6">hello</text>`, wantErr: true},
		{name: "byte-count-utf8-char-count", body: `<text bytes="1">é</text>`, wantErr: true},
		{name: "byte-count-escaped-raw-size", body: `<text bytes="9">A &amp; B</text>`, wantErr: true},
		{name: "byte-count-zero-with-body", body: `<text bytes="0">A</text>`, wantErr: true},
		{name: "unknown-child", body: `<text>ok</text><contentmodel>json</contentmodel>`, wantErr: true},
		{name: "unknown-slot-role", body: `<role>auxiliary</role><text>ok</text>`, wantErr: true},
		{name: "unexpected-text-attribute", body: `<text special="ignored">ok</text>`, wantErr: true},
		{name: "wrong-namespace-model", body: `<alt:model xmlns:alt="urn:unsupported">json</alt:model><text>ok</text>`, wantErr: true},
		{name: "direct-revision-text", body: `garbage<text>ok</text>`, wantErr: true},
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
