package xlsb

import (
	"bytes"
	"testing"
	"unicode/utf8"
)

// FuzzRecords reads arbitrary bytes as the records of a part: it ends,
// with or without an error, and never holds more than a record.
func FuzzRecords(f *testing.F) {
	f.Add([]byte{0x81, 0x01, 0x00, 0x91, 0x01, 0x00, 0x00, 0x13, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	f.Add([]byte{0x06, 0xFF, 0xFF, 0xFF, 0x7F})
	f.Add([]byte{0x86, 0x86, 0x01})
	f.Fuzz(func(t *testing.T, data []byte) {
		r := NewReader(bytes.NewReader(data))
		n := 0
		for r.Next() {
			if len(r.Data()) > MaxRecord {
				t.Fatalf("a record of %d bytes", len(r.Data()))
			}
			f := NewFields(r.Data())
			f.U32()
			if s := f.String(nil); !utf8.Valid(s) {
				t.Fatalf("a string that is not UTF-8: %q", s)
			}
			n++
		}
		if n > len(data) {
			t.Fatalf("%d records from %d bytes", n, len(data))
		}
	})
}

// FuzzFormula reads arbitrary bytes as a formula: it gives text of bounded
// length, valid UTF-8, or nothing.
func FuzzFormula(f *testing.F) {
	book := &Book{Names: []string{"Rate", "_xlnm.Print_Area"}, Sheets: []string{"A", "My sheet"},
		Links: []Link{{Kind: LinkSelf}, {Kind: LinkOther}, {Kind: LinkSame},
			{Kind: LinkBook, Book: 1, Sheets: []string{"B", "C d"}, Names: []ExternName{{"N", 0}, {"M", 2}, {"L", 5}}},
			{Kind: LinkAddin, Names: []ExternName{{"F", 0}}}},
		XTI:    []XTI{{0, 0, 1}, {1, 0, 0}, {2, 0, 0}, {0, -1, 7}, {3, 0, 1}, {3, -2, -2}, {4, -2, -2}, {3, 1, 0}},
		Tables: func() map[uint32]Table { return map[uint32]Table{1: {"T", []string{"a", "b[", ""}}} },
		Fields: []string{"x", "y z"}}
	for _, seed := range [][2][]byte{
		{{0x1E, 1, 0, 0x1E, 2, 0, 0x03}, nil},
		{{0x24, 0, 0, 0, 0, 0, 0xC0, 0x42, 1, 4, 0}, nil},
		{{0x17, 2, 0, 'a', 0, '"', 0, 0x19, 0x40, 0, 3, 0x15}, nil},
		{{0x3B, 0, 0, 0, 0, 0, 0, 0xFF, 0xFF, 0x0F, 0, 0, 0, 0, 0, 0x23, 1, 0, 0, 0, 0x10}, nil},
		{{0x60, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, {1, 0, 0, 0, 2, 0, 0, 0, 1, 1, 0, 'x', 0, 2, 1}},
		{{0x2C, 0xFF, 0xFF, 0x0F, 0, 0xFF, 0xFF, 0x26, 0, 0, 0, 0, 3, 0}, {1, 0, 0, 0}},
		{{0x18, 0x19, 0, 0, 9, 0, 1, 0, 0, 0, 1, 0, 1, 0}, nil},
		{{0x18, 0x19, 4, 0, 1, 0x20, 0, 0, 0, 0, 0, 0, 0, 0}, {1, 0, 0, 1, 0, 'T', 0, 0, 0, 1, 0, 2, 0, 1, 0, 'a', 0}},
		{{0x3A, 4, 0, 0, 0, 0, 0, 0, 0xC0, 0x39, 5, 0, 2, 0, 0, 0, 0x10}, nil},
		{{0x39, 6, 0, 1, 0, 0, 0, 0x42, 1, 0xFF, 0, 0x18, 0x1D, 1, 0, 0, 0, 0x03}, nil},
	} {
		f.Add(seed[0], seed[1], int32(3), int32(2))
	}
	f.Fuzz(func(t *testing.T, rgce, extra []byte, row, col int32) {
		text, ok := Formula(rgce, extra, book, Place{Row: row, Col: col})
		if !ok && text != "" {
			t.Fatalf("text %q of a formula not read", text)
		}
		if len(text) > maxFormula || !utf8.ValidString(text) {
			t.Fatalf("text of %d bytes, valid %v", len(text), utf8.ValidString(text))
		}
	})
}
