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
