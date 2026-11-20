package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
)

// streamJSON writes the document f holds as JSON a part at a time, as
// each is read, so that what is held is about one part and not the whole
// document. The bytes are those json.Encoder writes for the document read
// whole (File.Document), with HTML left unescaped and, if indent is set,
// two spaces to a level; the fields of a document put its parts before
// its children and warnings, which are known only once the parts have
// been read.
//
// It returns the document's warnings, its own and its parts', for the
// remarks on standard error. written says whether any of the document has
// been written: an error after that leaves the output cut short.
func streamJSON(ctx context.Context, f *gofastooxml.File, w *bufio.Writer, indent bool) (warnings []gofastcore.Warning, written bool, err error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	// encode gives v as it would stand in the document at the given
	// depth: the first line not indented, the rest indented to it.
	encode := func(v any, depth int) ([]byte, error) {
		buf.Reset()
		if indent {
			prefix := ""
			for range depth {
				prefix += "  "
			}
			enc.SetIndent(prefix, "  ")
		}
		if err := enc.Encode(v); err != nil {
			return nil, err
		}
		return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
	}
	// nl gives the line break and indentation before an item at a depth.
	nl := func(depth int) string {
		if !indent {
			return ""
		}
		s := "\n"
		for range depth {
			s += "  "
		}
		return s
	}
	key := func(name string) string {
		if indent {
			return `"` + name + `": `
		}
		return `"` + name + `":`
	}

	// The fields before the parts, from the document with its parts
	// empty: everything up to the [] of its parts.
	head := gofastcore.Document{SchemaVersion: gofastcore.SchemaVersion, Source: f.Source(), Metadata: f.Metadata(), Parts: []gofastcore.Part{}}
	b, err := encode(&head, 0)
	if err != nil {
		return nil, false, err
	}
	tail := "[]}"
	if indent {
		tail = "[]\n}"
	}
	if !bytes.HasSuffix(b, []byte(tail)) {
		return nil, false, errors.New("the fields of a document are not in the order this command writes them")
	}
	head.Parts = nil
	prefix := append([]byte(nil), b[:len(b)-len(tail)]...)
	direct := newPartWriter(w, indent)

	n := 0
	for part, err := range f.Parts(ctx) {
		if err != nil {
			return warnings, written, err
		}
		warnings = append(warnings, part.Warnings...)
		if n == 0 {
			w.Write(prefix)
			w.WriteByte('[')
			written = true
		} else {
			w.WriteByte(',')
		}
		w.WriteString(nl(2))
		// The part is encoded straight into the output, not into a buffer
		// first: a Word document is one part, as large as the document.
		if err := direct.encode(&part, 2); err != nil {
			return warnings, written, err
		}
		n++
		// Each part goes out as it is done.
		if err := w.Flush(); err != nil {
			return warnings, written, err
		}
	}
	if n == 0 {
		// No part: the document as it would be read whole.
		w.Write(prefix)
		w.WriteString("[]")
	} else {
		w.WriteString(nl(1))
		w.WriteByte(']')
	}
	written = true

	children, err := f.Children(ctx)
	if err != nil {
		return warnings, written, err
	}
	own := f.Warnings()
	warnings = append(warnings, own...)
	if len(children) > 0 {
		b, err := encode(children, 1)
		if err != nil {
			return warnings, written, err
		}
		w.WriteByte(',')
		w.WriteString(nl(1) + key("children"))
		w.Write(b)
	}
	if len(own) > 0 {
		b, err := encode(own, 1)
		if err != nil {
			return warnings, written, err
		}
		w.WriteByte(',')
		w.WriteString(nl(1) + key("warnings"))
		w.Write(b)
	}
	w.WriteString(nl(0))
	w.WriteString("}\n")
	return warnings, written, nil
}

// partWriter encodes a value into the output as it would stand in the
// document, without the line end json.Encoder puts after it.
type partWriter struct {
	w       *bufio.Writer
	enc     *json.Encoder
	indent  bool
	pending bool // a line end held back, written only if more follows
}

func newPartWriter(w *bufio.Writer, indent bool) *partWriter {
	p := &partWriter{w: w, indent: indent}
	p.enc = json.NewEncoder(p)
	p.enc.SetEscapeHTML(false)
	return p
}

func (p *partWriter) Write(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	if p.pending {
		p.w.WriteByte('\n')
		p.pending = false
	}
	n := len(b)
	if b[n-1] == '\n' {
		b, p.pending = b[:n-1], true
	}
	_, err := p.w.Write(b)
	return n, err
}

func (p *partWriter) encode(v any, depth int) error {
	if p.indent {
		prefix := ""
		for range depth {
			prefix += "  "
		}
		p.enc.SetIndent(prefix, "  ")
	}
	err := p.enc.Encode(v)
	p.pending = false
	return err
}
