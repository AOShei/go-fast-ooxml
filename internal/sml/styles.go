package sml

import (
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/numfmt"
	"github.com/AOShei/go-fast-ooxml/internal/symbol"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// A cell names a style by number. The style says how the cell's value is
// displayed (its number format) and which font it is in, which matters
// for a symbol font, whose text is codes.

// cellStyle is what a style means for the text of a cell.
type cellStyle struct {
	format  *numfmt.Format
	general bool        // the format is General
	symbol  symbol.Font // the cell's font, if it is a symbol font: its text is codes
}

type styleSheet struct {
	styles []cellStyle
}

var plainStyle = cellStyle{format: numfmt.General(), general: true}

// style returns the style with the given number. One the file does not
// define is the plain style.
func (st *styleSheet) style(i int) *cellStyle {
	if st == nil || i < 0 || i >= len(st.styles) {
		return &plainStyle
	}
	return &st.styles[i]
}

// Limits on a styles part. Excel itself stops at 64,000 styles.
const (
	maxStyles  = 1 << 17
	maxFormats = 1 << 13
)

// readStyles reads a styles part: its number formats, the fonts as far as
// a symbol font goes, and the cell formats that combine them.
func readStyles(s *xmlscan.Scanner) *styleSheet {
	codes := map[int]string{}
	var fonts []symbol.Font
	var xfs []xf
	for s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start || s.Depth() != 2 {
			continue
		}
		if s.Space() != ns.Sheet {
			s.Skip()
			continue
		}
		depth := s.Depth()
		switch string(s.Local()) {
		case "numFmts":
			for s.Next() != xmlscan.EOF && s.Depth() >= depth {
				if s.Kind() != xmlscan.Start || !s.Is(ns.Sheet, "numFmt") || len(codes) >= maxFormats {
					continue
				}
				id, ok := intAttr(s, "numFmtId")
				code, has := s.Attr(xmlscan.NoSpace, "formatCode")
				if ok && has {
					// Text a format writes into a cell is text like any other.
					codes[id], _ = clean.String(code, clean.Ligatures)
				}
			}
		case "fonts":
			for s.Next() != xmlscan.EOF && s.Depth() >= depth {
				if s.Kind() != xmlscan.Start || s.Depth() != depth+1 {
					continue
				}
				font := symbol.None
				inner := s.Depth()
				for s.Next() != xmlscan.EOF && s.Depth() >= inner {
					if s.Kind() == xmlscan.Start && s.Depth() == inner+1 && s.Is(ns.Sheet, "name") {
						v, _ := s.Attr(xmlscan.NoSpace, "val")
						font = symbol.ByName(string(v))
					}
				}
				if len(fonts) < maxStyles {
					fonts = append(fonts, font)
				}
			}
		case "cellXfs":
			for s.Next() != xmlscan.EOF && s.Depth() >= depth {
				if s.Kind() != xmlscan.Start || s.Depth() != depth+1 || len(xfs) >= maxStyles {
					continue
				}
				var x xf
				x.format, _ = intAttr(s, "numFmtId")
				x.font, _ = intAttr(s, "fontId")
				xfs = append(xfs, x)
			}
		default:
			s.Skip()
		}
	}

	return buildStyles(codes, fonts, xfs)
}

// xf is a cell format: the numbers of its number format and font.
type xf struct{ format, font int }

// buildStyles makes the styles of a workbook from its number formats by
// number, its fonts, and its cell formats.
func buildStyles(codes map[int]string, fonts []symbol.Font, xfs []xf) *styleSheet {
	formats := map[int]*numfmt.Format{}
	st := &styleSheet{styles: make([]cellStyle, len(xfs))}
	for i, x := range xfs {
		f, ok := formats[x.format]
		if !ok {
			code, custom := codes[x.format]
			if !custom {
				code = numfmt.Builtin(x.format)
			}
			f = numfmt.Parse(code)
			formats[x.format] = f
		}
		cs := cellStyle{format: f, general: f == numfmt.General()}
		if x.font >= 0 && x.font < len(fonts) {
			cs.symbol = fonts[x.font]
		}
		st.styles[i] = cs
	}
	return st
}

func intAttr(s *xmlscan.Scanner, name string) (int, bool) {
	v, ok := s.Attr(xmlscan.NoSpace, name)
	if !ok {
		return 0, false
	}
	return atoi(v)
}

// atoi reads a non-negative decimal number of at most nine digits.
func atoi(v []byte) (int, bool) {
	if len(v) == 0 || len(v) > 9 {
		return 0, false
	}
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

func isTrue(v []byte) bool {
	return len(v) == 1 && v[0] == '1' || string(v) == "true"
}
