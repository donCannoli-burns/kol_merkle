// Package wiki parses locally supplied MediaWiki XML exports with per-page limits.
package wiki

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
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


// revisionInlineText decodes only inline text. An absent, deleted, or external
// payload must never become the SHA-256 of a known-empty revision.
type revisionInlineText struct {
	value string
}

func (t *revisionInlineText) UnmarshalXML(dec *xml.Decoder, start xml.StartElement) error {
	hasID := false
	hasBytes := false
	var declaredBytes int64
	for _, a := range start.Attr {
		switch a.Name.Local {
		case "deleted", "location":
			return fmt.Errorf("revision text is unavailable: %s attribute", a.Name.Local)
		case "id":
			hasID = true
		case "bytes":
			n, err := strconv.ParseInt(a.Value, 10, 64)
			if err != nil || n < 0 {
				return fmt.Errorf("invalid revision text byte count %q", a.Value)
			}
			hasBytes, declaredBytes = true, n
		}
	}
	var body bytes.Buffer
	for {
		tok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("revision inline text: %w", err)
		}
		switch v := tok.(type) {
		case xml.CharData:
			_, _ = body.Write(v)
		case xml.EndElement:
			if v.Name != start.Name {
				return fmt.Errorf("unexpected inline text closing element")
			}
			t.value = body.String()
			if len(t.value) == 0 && (hasID || (hasBytes && declaredBytes > 0)) {
				return fmt.Errorf("revision text has a content reference or nonzero declared size but no inline bytes")
			}
			return nil
		default:
			return fmt.Errorf("unsupported markup inside revision text")
		}
	}
}

// UnmarshalXML enforces the M0 representation: exactly one inline main text,
// with no content slots or external/redacted payloads. Older exports without
// model/format metadata remain accepted.
func (r *Revision) UnmarshalXML(dec *xml.Decoder, start xml.StartElement) error {
	var wire struct {
		ID        int64                `xml:"id"`
		Timestamp string               `xml:"timestamp"`
		Text      []revisionInlineText `xml:"text"`
		Content   []struct{}           `xml:"content"`
		Slots     []struct{}           `xml:"slots"`
		Model     string               `xml:"model"`
		Format    string               `xml:"format"`
	}
	if err := dec.DecodeElement(&wire, &start); err != nil {
		return err
	}
	if len(wire.Content) != 0 || len(wire.Slots) != 0 {
		return fmt.Errorf("additional or wrapped revision content slots are unsupported by M0")
	}
	if wire.Model != "" && wire.Model != "wikitext" {
		return fmt.Errorf("unsupported revision content model %q", wire.Model)
	}
	if wire.Format != "" && wire.Format != "text/x-wiki" {
		return fmt.Errorf("unsupported revision content format %q", wire.Format)
	}
	if len(wire.Text) != 1 {
		return fmt.Errorf("revision requires exactly one inline text element; got %d", len(wire.Text))
	}
	*r = Revision{ID: wire.ID, Timestamp: wire.Timestamp, Text: wire.Text[0].value}
	return nil
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
