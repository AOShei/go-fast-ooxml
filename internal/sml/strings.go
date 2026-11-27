package sml

import (
	"bytes"
	"strings"

	"github.com/AOShei/go-fast-core/clean"
	"github.com/AOShei/go-fast-core/symbol"
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
	// rich has, for the strings whose runs name fonts where a symbol font
	// matters, where each run begins and its font. Such a string is kept
	// untrimmed, so that the places hold, and is trimmed when a cell takes
	// it (fontsOf).
	rich map[uint32][]fontRun
}

// runs returns the runs of string i, if its runs' fonts matter.
func (t *stringTable) runs(i int) []fontRun {
	if t == nil || t.rich == nil || i < 0 {
		return nil
	}
	return t.rich[uint32(i)]
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
// limit. symbolic is whether the workbook has a symbol font: the fonts its
// strings' runs name matter then, as they do wherever one names a symbol
// font.
func readStrings(s *xmlscan.Scanner, symbolic bool) (t *stringTable, removed int, full bool) {
	t = &stringTable{}
	var all strings.Builder
	var raw, cleaned []byte
	var runs []fontRun
	for s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start || s.Depth() != 2 {
			continue
		}
		if !s.Is(ns.Sheet, "si") {
			s.Skip()
			continue
		}
		raw, runs = readRuns(s, raw[:0], runs[:0])
		var n int
		if fontsMatter(runs, symbolic) {
			cleaned, n = cleanRuns(cleaned[:0], raw, runs, true)
			if t.rich == nil {
				t.rich = map[uint32][]fontRun{}
			}
			t.rich[uint32(len(t.ends))] = append([]fontRun(nil), runs...)
		} else {
			cleaned, n = cellText(cleaned[:0], raw)
		}
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

// fontRun is a run of a string: where it begins in the string's text, and
// its font, if it names one; a run that names none is in the cell's.
type fontRun struct {
	at    uint32
	font  symbol.Font // the font it names, if that is a symbol font
	named bool        // it names a font
}

// fontsMatter reports whether the fonts of a string's runs change what it
// says: one names a symbol font, or one names a font and the workbook has
// a symbol font that the rest, or the cell, may be in.
func fontsMatter(runs []fontRun, symbolic bool) bool {
	named := false
	for _, r := range runs {
		if r.font != symbol.None {
			return true
		}
		named = named || r.named
	}
	return named && symbolic
}

// readRuns appends the text of a string item (si or is, the element whose
// Start token is current), as readRichText does, and its runs: one for
// each run, with the font its properties name, and one for text outside a
// run.
func readRuns(s *xmlscan.Scanner, dst []byte, runs []fontRun) ([]byte, []fontRun) {
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Is(ns.Sheet, "t"):
			if s.Depth() == depth+1 { // text outside a run
				runs = append(runs, fontRun{at: uint32(len(dst))})
			}
			dst = s.AppendText(dst)
		case s.Is(ns.Sheet, "r"):
			runs = append(runs, fontRun{at: uint32(len(dst))})
		case s.Is(ns.Sheet, "rPr"):
			inner := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() > inner {
				if s.Kind() == xmlscan.Start && s.Is(ns.Sheet, "rFont") && len(runs) > 0 {
					v, _ := s.Attr(xmlscan.NoSpace, "val")
					r := &runs[len(runs)-1]
					r.named, r.font = true, symbol.ByName(string(v))
				}
			}
		default:
			s.Skip()
		}
	}
	return dst, runs
}

// cleanRuns appends raw, the text of runs, cleaned as cellText (escapes,
// for a string of an XML workbook) or plainText cleans a string, a run at
// a time and not trimmed, and moves the runs' starts to where they are in
// what it appends.
func cleanRuns(dst, raw []byte, runs []fontRun, escapes bool) ([]byte, int) {
	start := len(dst)
	removed := 0
	for i := range runs {
		from, to := int(runs[i].at), len(raw)
		if i+1 < len(runs) {
			to = int(runs[i+1].at)
		}
		runs[i].at = uint32(len(dst) - start)
		piece := raw[from:to]
		var n int
		switch {
		case escapes && clean.Plain(piece) && bytes.IndexByte(piece, '_') < 0:
			dst = append(dst, piece...)
		case escapes && bytes.Contains(piece, []byte("_x")):
			var tmp [256]byte
			dst, n = clean.Append(dst, unescape(tmp[:0], piece), clean.Ligatures)
		default:
			dst, n = clean.Append(dst, piece, clean.Ligatures)
		}
		removed += n
	}
	return dst, removed
}

// fontsOf appends text, a string that cleanRuns made, with each run in its
// font: one that names no font in cell, the cell's. The codes of a symbol
// font are translated. What it appends is trimmed at the ends. missing
// counts codes that have no character.
func fontsOf(dst []byte, text string, runs []fontRun, cell symbol.Font) ([]byte, int) {
	start := len(dst)
	missing := 0
	for i, r := range runs {
		from, to := int(r.at), len(text)
		if i+1 < len(runs) {
			to = int(runs[i+1].at)
		}
		if from > to || to > len(text) {
			continue
		}
		f := r.font
		if !r.named {
			f = cell
		}
		if f == symbol.None {
			dst = append(dst, text[from:to]...)
			continue
		}
		var n int
		dst, n = f.Translate(dst, []byte(text[from:to]))
		missing += n
	}
	trimmed := bytes.TrimSpace(dst[start:])
	n := copy(dst[start:], trimmed)
	return dst[:start+n], missing
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
