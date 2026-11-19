package sml

import (
	"bytes"
	"strings"

	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// stringTable is a workbook's shared strings: text that cells refer to by
// number. All of it is held as one string, and a cell's text is a part of
// that string, so a million cells that show the same text share its
// memory.
type stringTable struct {
	text string
	ends []uint32 // where each string ends; it starts where the one before ends
}

func (t *stringTable) at(i int) (string, bool) {
	if t == nil || i < 0 || i >= len(t.ends) {
		return "", false
	}
	start := uint32(0)
	if i > 0 {
		start = t.ends[i-1]
	}
	return t.text[start:t.ends[i]], true
}

// maxStringBytes bounds the shared strings as a whole: positions in them
// are kept in 32 bits.
const maxStringBytes = 1<<32 - 1

// readStrings reads a shared-strings part. removed counts the control
// characters taken out. full reports that the table was cut at its size
// limit.
func readStrings(s *xmlscan.Scanner) (t *stringTable, removed int, full bool) {
	t = &stringTable{}
	var all strings.Builder
	var raw, cleaned []byte
	for s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start || s.Depth() != 2 {
			continue
		}
		if !s.Is(ns.Sheet, "si") {
			s.Skip()
			continue
		}
		raw = readRichText(s, raw[:0])
		var n int
		cleaned, n = cellText(cleaned[:0], raw)
		removed += n
		if int64(all.Len())+int64(len(cleaned)) > maxStringBytes {
			t.text = all.String()
			return t, removed, true
		}
		all.Write(cleaned)
		t.ends = append(t.ends, uint32(all.Len()))
	}
	t.text = all.String()
	return t, removed, false
}

// readRichText appends the text of a string item (the element whose Start
// token is current): its t elements, alone or in formatted runs. The
// reading aid for East Asian text (rPh) is another rendering of the same
// characters and is left out.
func readRichText(s *xmlscan.Scanner, dst []byte) []byte {
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Is(ns.Sheet, "t"):
			dst = s.AppendText(dst)
		case s.Is(ns.Sheet, "r"):
			// its t is read on the next turn
		default:
			s.Skip()
		}
	}
	return dst
}

// cellText appends text from a cell or a shared string to dst in the
// shared model's form: escapes decoded, valid UTF-8, no control
// characters, ligatures expanded, no space at the ends.
func cellText(dst, raw []byte) ([]byte, int) {
	if clean.Plain(raw) && bytes.IndexByte(raw, '_') < 0 {
		return append(dst, bytes.TrimSpace(raw)...), 0
	}
	start := len(dst)
	var removed int
	if bytes.Contains(raw, []byte("_x")) {
		var tmp [256]byte
		dst, removed = clean.Append(dst, unescape(tmp[:0], raw), clean.Ligatures)
	} else {
		dst, removed = clean.Append(dst, raw, clean.Ligatures)
	}
	trimmed := bytes.TrimSpace(dst[start:])
	n := copy(dst[start:], trimmed)
	return dst[:start+n], removed
}

// unescape decodes the way a spreadsheet writes characters XML cannot
// hold: _x000D_ is a carriage return, and _x005F_ an underscore that would
// otherwise start such an escape.
func unescape(dst, src []byte) []byte {
	for i := 0; i < len(src); i++ {
		if src[i] == '_' && i+6 < len(src) && src[i+1] == 'x' && src[i+6] == '_' {
			v, ok := 0, true
			for _, c := range src[i+2 : i+6] {
				switch {
				case c >= '0' && c <= '9':
					v = v<<4 | int(c-'0')
				case c >= 'a' && c <= 'f':
					v = v<<4 | int(c-'a'+10)
				case c >= 'A' && c <= 'F':
					v = v<<4 | int(c-'A'+10)
				default:
					ok = false
				}
			}
			if ok {
				dst = appendRune(dst, rune(v))
				i += 6
				continue
			}
		}
		dst = append(dst, src[i])
	}
	return dst
}

func appendRune(dst []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r)&0x3F)
	case r >= 0xD800 && r <= 0xDFFF:
		return append(dst, 0xEF, 0xBF, 0xBD) // half of a surrogate pair: not a character
	}
	return append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
}
