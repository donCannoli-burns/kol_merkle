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


// revisionInlineText decodes only inline text. Unknown attributes, external
// payloads and inconsistent decoded byte lengths fail closed.
type revisionInlineText struct {
	value string
}

func (t *revisionInlineText) UnmarshalXML(dec *xml.Decoder, start xml.StartElement) error {
	// encoding/xml does not reject duplicate attributes on StartElement.
	// Inspect expanded names before interpreting any attribute values.
	seenAttrs := make(map[xml.Name]bool, len(start.Attr))
	for _, a := range start.Attr {
		if seenAttrs[a.Name] {
			return fmt.Errorf("duplicate revision text attribute %q", a.Name.Local)
		}
		seenAttrs[a.Name] = true
	}

	hasID := false
	hasBytes := false
	var declaredBytes int64
	for _, a := range start.Attr {
		switch {
		case a.Name.Space == "" && (a.Name.Local == "deleted" || a.Name.Local == "location"):
			return fmt.Errorf("revision text is unavailable: %s attribute", a.Name.Local)
		case a.Name.Space == "" && a.Name.Local == "id":
			hasID = true
		case a.Name.Space == "" && a.Name.Local == "bytes":
			n, err := strconv.ParseInt(a.Value, 10, 64)
			if err != nil || n < 0 {
				return fmt.Errorf("invalid revision text byte count %q", a.Value)
			}
			hasBytes, declaredBytes = true, n
		case a.Name.Space == "" && a.Name.Local == "sha1":
			// A source hint only; SHA-256 content commitments use decoded bytes.
		case a.Name.Space == "http://www.w3.org/XML/1998/namespace" && a.Name.Local == "space":
			if a.Value != "preserve" {
				return fmt.Errorf("unsupported revision xml:space %q", a.Value)
			}
		default:
			return fmt.Errorf("unsupported revision text attribute %q", a.Name.Local)
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
			if hasBytes && int64(len(t.value)) != declaredBytes {
				return fmt.Errorf("revision text byte count mismatch: declared=%d decoded=%d", declaredBytes, len(t.value))
			}
			if hasID && len(t.value) == 0 {
				return fmt.Errorf("revision text has a content reference but no inline bytes")
			}
			return nil
		default:
			return fmt.Errorf("unsupported markup inside revision text")
		}
	}
}

// UnmarshalXML admits only the supported MediaWiki revision children. Unknown
// or repeated metadata must not override content-model interpretation.
func (r *Revision) UnmarshalXML(dec *xml.Decoder, start xml.StartElement) error {
	if len(start.Attr) != 0 {
		return fmt.Errorf("unsupported revision attributes")
	}
	var parsed Revision
	seen := make(map[string]bool)
	for {
		tok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("revision XML: %w", err)
		}
		switch el := tok.(type) {
		case xml.CharData:
			if len(bytes.TrimSpace(el)) != 0 {
				return fmt.Errorf("unexpected direct revision character data")
			}
		case xml.StartElement:
			if el.Name.Space != start.Name.Space {
				return fmt.Errorf("unsupported revision element namespace %q", el.Name.Space)
			}
			name := el.Name.Local
			switch name {
			case "id", "parentid", "timestamp", "contributor", "minor", "comment", "origin", "model", "format", "text", "sha1":
				// RevisionType members allowed by the MediaWiki export-0.11 schema.
			default:
				return fmt.Errorf("unsupported revision element %q", name)
			}
			if seen[name] {
				return fmt.Errorf("duplicate revision element %q", name)
			}
			seen[name] = true
			switch name {
			case "id":
				if err := dec.DecodeElement(&parsed.ID, &el); err != nil {
					return fmt.Errorf("revision id: %w", err)
				}
			case "timestamp":
				if err := dec.DecodeElement(&parsed.Timestamp, &el); err != nil {
					return fmt.Errorf("revision timestamp: %w", err)
				}
			case "model", "format":
				var value string
				if err := dec.DecodeElement(&value, &el); err != nil {
					return fmt.Errorf("revision %s: %w", name, err)
				}
				if name == "model" && value != "wikitext" {
					return fmt.Errorf("unsupported revision content model %q", value)
				}
				if name == "format" && value != "text/x-wiki" {
					return fmt.Errorf("unsupported revision content format %q", value)
				}
			case "text":
				var value revisionInlineText
				if err := dec.DecodeElement(&value, &el); err != nil {
					return fmt.Errorf("revision text: %w", err)
				}
				parsed.Text = value.value
			default:
				// Metadata ignored for the M0 wikitext commitment. It remains
				// bound as raw XML in the original export-byte commitment.
				if err := dec.Skip(); err != nil {
					return fmt.Errorf("revision %s: %w", name, err)
				}
			}
		case xml.EndElement:
			if el.Name != start.Name {
				return fmt.Errorf("unexpected revision closing element %q", el.Name.Local)
			}
			if !seen["id"] || !seen["timestamp"] || !seen["text"] {
				return fmt.Errorf("revision requires exactly one id, timestamp and inline text")
			}
			*r = parsed
			return nil
		}
	}
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
