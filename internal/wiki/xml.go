// Package wiki streams MediaWiki XML exports without loading the entire dump.
package wiki

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
)

// Revision preserves source IDs, timestamps, and original wikitext.
type Revision struct {
	ID        int64  `xml:"id"`
	Timestamp string `xml:"timestamp"`
	Text      string `xml:"text"`
}

type Page struct {
	Title     string     `xml:"title"`
	Namespace int        `xml:"ns"`
	ID        int64      `xml:"id"`
	Revisions []Revision `xml:"revision"`
}

// EachPage reads one <page> at a time. Callers must provide a bounded source.
func EachPage(r io.Reader, visit func(Page) error) error {
	d := xml.NewDecoder(r)
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("wiki XML token: %w", err)
		}
		el, ok := tok.(xml.StartElement)
		if !ok || el.Name.Local != "page" {
			continue
		}
		var p Page
		if err := d.DecodeElement(&p, &el); err != nil {
			return fmt.Errorf("wiki XML page: %w", err)
		}
		if p.ID <= 0 || p.Title == "" || len(p.Revisions) == 0 {
			return fmt.Errorf("invalid page id=%d title=%q revisions=%d", p.ID, p.Title, len(p.Revisions))
		}
		for _, rev := range p.Revisions {
			if rev.ID <= 0 || rev.Timestamp == "" {
				return fmt.Errorf("invalid revision on page %d", p.ID)
			}
		}
		if err := visit(p); err != nil {
			return err
		}
	}
}
