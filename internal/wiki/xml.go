// Package wiki parses locally supplied MediaWiki XML exports with per-page limits.
package wiki

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
)

const (
	MaxRevisionTextBytes = 16 << 20
	MaxPageTextBytes     = 128 << 20
	MaxPageRevisions     = 100000
	MaxTitleBytes        = 8192
	MaxRawXMLRunBytes    = 32 << 20
)

// Revision preserves the decoded UTF-8 wikitext, not the original XML escape syntax.
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

// EachPage visits one validated page at a time. Revision and page limits are
// checked during incremental page assembly, before any whole-page decode.
func EachPage(r io.Reader, visit func(Page) error) error {
	dec := xml.NewDecoder(&boundedRawRun{reader: r})
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("wiki XML token: %w", err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != "page" {
			continue
		}
		if err := readPage(dec, visit); err != nil {
			return err
		}
	}
}

func readPage(dec *xml.Decoder, visit func(Page) error) error {
	var p Page
	var totalBytes int
	for {
		tok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("wiki XML page: %w", err)
		}
		switch el := tok.(type) {
		case xml.EndElement:
			if el.Name.Local != "page" {
				return fmt.Errorf("unexpected XML closing element %q", el.Name.Local)
			}
			if p.ID <= 0 || p.Title == "" || len(p.Revisions) == 0 {
				return fmt.Errorf("invalid page id=%d title=%q revisions=%d", p.ID, p.Title, len(p.Revisions))
			}
			return visit(p)
		case xml.StartElement:
			switch el.Name.Local {
			case "title":
				if err := dec.DecodeElement(&p.Title, &el); err != nil {
					return fmt.Errorf("page title: %w", err)
				}
				if len(p.Title) > MaxTitleBytes {
					return fmt.Errorf("page title exceeds limit")
				}
			case "ns":
				if err := dec.DecodeElement(&p.Namespace, &el); err != nil {
					return fmt.Errorf("page namespace: %w", err)
				}
			case "id":
				if err := dec.DecodeElement(&p.ID, &el); err != nil {
					return fmt.Errorf("page id: %w", err)
				}
			case "revision":
				if len(p.Revisions) >= MaxPageRevisions {
					return fmt.Errorf("page exceeds revision count limit")
				}
				var rev Revision
				if err := dec.DecodeElement(&rev, &el); err != nil {
					return fmt.Errorf("page revision: %w", err)
				}
				if rev.ID <= 0 || rev.Timestamp == "" {
					return fmt.Errorf("invalid revision on page %d", p.ID)
				}
				if len(rev.Text) > MaxRevisionTextBytes {
					return fmt.Errorf("page %d revision %d exceeds text limit", p.ID, rev.ID)
				}
				totalBytes += len(rev.Text)
				if totalBytes > MaxPageTextBytes {
					return fmt.Errorf("page %d exceeds cumulative text limit", p.ID)
				}
				p.Revisions = append(p.Revisions, rev)
			default:
				if err := dec.Skip(); err != nil {
					return fmt.Errorf("skip XML field: %w", err)
				}
			}
		}
	}
}

// boundedRawRun limits an uninterrupted raw XML span before the decoder can
// allocate arbitrarily large text tokens. Other XML allocations remain bounded
// by the total input budget supplied to the compiler; this is not a complete
// defense against deliberately fragmented untrusted XML.
type boundedRawRun struct {
	reader io.Reader
	run    int
	cdata  bool
	window []byte
}

func (b *boundedRawRun) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	for _, v := range p[:n] {
		b.window = append(b.window, v)
		if len(b.window) > 9 {
			copy(b.window, b.window[1:])
			b.window = b.window[:9]
		}
		if b.cdata {
			if len(b.window) >= 3 && bytes.Equal(b.window[len(b.window)-3:], []byte("]]>")) {
				b.cdata = false
			}
		} else {
			if v == '<' {
				b.run = 0
			}
			if bytes.Equal(b.window, []byte("<![CDATA[")) {
				b.cdata = true
			}
		}
		b.run++
		if b.run > MaxRawXMLRunBytes {
			return 0, fmt.Errorf("raw XML character run exceeds %d bytes", MaxRawXMLRunBytes)
		}
	}
	return n, err
}
