package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

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
		// The part is written straight into the output a block at a time,
		// and a table a row at a time, not encoded whole first: a Word
		// document is one part, as large as the document, and a sheet can
		// be tens of megabytes.
		if err := direct.part(&part, 2); err != nil {
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

// partWriter writes a part into the output as it would stand in the
// document: the bytes json.Encoder writes for it, without holding the
// whole of it encoded. A part is written a block at a time, and the rows
// of a table a row at a time.
type partWriter struct {
	w      *bufio.Writer
	indent bool
	buf    bytes.Buffer
	enc    *json.Encoder // into buf
}

func newPartWriter(w *bufio.Writer, indent bool) *partWriter {
	p := &partWriter{w: w, indent: indent}
	p.enc = json.NewEncoder(&p.buf)
	p.enc.SetEscapeHTML(false)
	return p
}

// encoded returns v as it would stand in the document at the given
// depth: the first line not indented, the rest indented to it, and no
// line end after it. The bytes are good until the next call.
func (p *partWriter) encoded(v any, depth int) ([]byte, error) {
	p.buf.Reset()
	if p.indent {
		p.enc.SetIndent(spaces(depth), "  ")
	}
	if err := p.enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(p.buf.Bytes(), []byte("\n")), nil
}

// spaces is the indentation of a level.
func spaces(depth int) string {
	const blank = "                                "
	if 2*depth <= len(blank) {
		return blank[:2*depth]
	}
	return strings.Repeat("  ", depth)
}

// line ends the line and indents the next to a level, where the output
// is indented.
func (p *partWriter) line(depth int) {
	if p.indent {
		p.w.WriteByte('\n')
		p.w.WriteString(spaces(depth))
	}
}

// split encodes frame, a value in which a list has been emptied, and
// returns what stands before the list and after it: the list is the
// field of the given name, at the given depth, written "[]". ok is false
// if the field is not found where it is looked for.
func (p *partWriter) split(frame any, depth int, name string, at int) (before, after []byte, ok bool, err error) {
	b, err := p.encoded(frame, depth)
	if err != nil {
		return nil, nil, false, err
	}
	// A field's name cannot stand so in a string, where its quotes
	// would be escaped; the fields before it hold no list of this name.
	mark := `"` + name + `":[]`
	if p.indent {
		mark = "\n" + spaces(at) + `"` + name + `": []`
	}
	i := bytes.Index(b, []byte(mark))
	if i < 0 {
		return nil, nil, false, nil
	}
	return b[:i+len(mark)-1], b[i+len(mark):], true, nil
}

// part writes a part at the given depth.
func (p *partWriter) part(part *gofastcore.Part, depth int) error {
	if len(part.Blocks) > 0 {
		frame := *part
		frame.Blocks = []gofastcore.Block{}
		before, after, ok, err := p.split(&frame, depth, "blocks", depth+1)
		if err != nil {
			return err
		}
		if ok {
			// What follows the blocks is kept: the buffer is used again.
			p.w.Write(before)
			rest := append([]byte(nil), after...)
			for i := range part.Blocks {
				if i > 0 {
					p.w.WriteByte(',')
				}
				p.line(depth + 2)
				if err := p.block(&part.Blocks[i], depth+2); err != nil {
					return err
				}
			}
			p.line(depth + 1)
			p.w.WriteByte(']')
			_, err = p.w.Write(rest)
			return err
		}
	}
	b, err := p.encoded(part, depth)
	if err != nil {
		return err
	}
	_, err = p.w.Write(b)
	return err
}

// block writes a block at the given depth; of a table, its rows one
// after another.
func (p *partWriter) block(block *gofastcore.Block, depth int) error {
	if block.Table != nil && len(block.Table.Rows) > 0 {
		frame := *block
		table := *block.Table
		table.Rows = [][]string{}
		frame.Table = &table
		before, after, ok, err := p.split(&frame, depth, "rows", depth+2)
		if err != nil {
			return err
		}
		if ok {
			p.w.Write(before)
			rest := append([]byte(nil), after...)
			for i, row := range block.Table.Rows {
				if i > 0 {
					p.w.WriteByte(',')
				}
				p.line(depth + 3)
				if err := p.row(row, depth+3); err != nil {
					return err
				}
			}
			p.line(depth + 2)
			p.w.WriteByte(']')
			_, err = p.w.Write(rest)
			return err
		}
	}
	b, err := p.encoded(block, depth)
	if err != nil {
		return err
	}
	_, err = p.w.Write(b)
	return err
}

// row writes the cells of a row of a table.
func (p *partWriter) row(row []string, depth int) error {
	switch {
	case row == nil:
		_, err := p.w.WriteString("null")
		return err
	case len(row) == 0:
		_, err := p.w.WriteString("[]")
		return err
	}
	p.w.WriteByte('[')
	for i, cell := range row {
		if i > 0 {
			p.w.WriteByte(',')
		}
		p.line(depth + 1)
		if plain(cell) {
			p.w.WriteByte('"')
			p.w.WriteString(cell)
			p.w.WriteByte('"')
			continue
		}
		b, err := p.encoded(cell, 0)
		if err != nil {
			return err
		}
		p.w.Write(b)
	}
	p.line(depth)
	return p.w.WriteByte(']')
}

// plain reports whether a string stands in JSON as it is, between
// quotes: it has nothing the encoder escapes or replaces.
func plain(s string) bool {
	ascii := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == '"' || c == '\\' {
			return false
		}
		ascii = ascii && c < 0x80
	}
	if ascii {
		return true
	}
	// The encoder replaces what is not UTF-8, and escapes the two line
	// separators of Unicode, U+2028 and U+2029.
	return utf8.ValidString(s) && !strings.Contains(s, "\xe2\x80\xa8") && !strings.Contains(s, "\xe2\x80\xa9")
}
