package sml

import (
	"bytes"
	"sort"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// choiceList is a drop-down list a sheet declares for some of its cells: a
// data validation of type list.
type choiceList struct {
	ranges []rect
	source string // the list as written: "\"a,b,c\"", or a reference or formula
	arrow  bool   // the drop-down arrow is shown
}

const (
	maxChoiceLists = 1 << 14
	// The ranges of all the lists of a sheet: a list can be for thousands
	// of ranges, as a sheet copied down many times has.
	maxChoiceRanges = 1 << 18
)

// readValidation reads a dataValidations element, or one of the newer
// kind in extLst, whose list and ranges are elements rather than
// attributes. Only lists are kept: the other kinds of validation offer
// nothing to choose from.
func (sh *sheet) readValidation(s *xmlscan.Scanner) {
	typ, _ := s.Attr(xmlscan.NoSpace, "type")
	if string(typ) != "list" || len(sh.lists) >= maxChoiceLists {
		s.Skip()
		return
	}
	// showDropDown="1" means, against its name, that the arrow is hidden.
	v := choiceList{arrow: true}
	if b, ok := s.Attr(xmlscan.NoSpace, "showDropDown"); ok && isTrue(b) {
		v.arrow = false
	}
	if b, ok := s.Attr(xmlscan.NoSpace, "sqref"); ok {
		v.ranges = sh.appendRanges(v.ranges, b)
	}
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch string(s.Local()) {
		case "formula1":
			// The older kind holds the text here; the newer an xm:f.
			sh.raw = s.AppendText(sh.raw[:0])
			if f := bytes.TrimSpace(sh.raw); len(f) > 0 {
				v.source = string(f)
			}
		case "f":
			sh.raw = s.AppendText(sh.raw[:0])
			v.source = string(bytes.TrimSpace(sh.raw))
		case "sqref":
			sh.raw = s.AppendText(sh.raw[:0])
			v.ranges = sh.appendRanges(v.ranges, sh.raw)
		}
	}
	if v.source != "" && len(v.ranges) > 0 {
		sh.lists = append(sh.lists, v)
	}
}

// appendRanges adds the ranges of a list such as "A1:B9 D4".
func (sh *sheet) appendRanges(dst []rect, sqref []byte) []rect {
	for _, ref := range bytes.Fields(sqref) {
		if sh.listRanges >= maxChoiceRanges {
			break
		}
		if r, ok := parseRange(ref); ok {
			dst = append(dst, r)
			sh.listRanges++
		}
	}
	return dst
}

// choices returns the sheet's drop-down lists as note blocks, each at the
// first cell it is for. A list written out in the file ("a,b,c") gives its
// values, one per line; one taken from cells or worked out by a formula
// gives the formula, since those values are in the text where the cells
// are. A list whose arrow is hidden, or whose cells are all hidden, is
// never seen and is flagged so.
func (sh *sheet) choices() []placed {
	var out []placed
	var cols []span
	if len(sh.lists) > 0 {
		cols = mergedSpans(sh.hiddenCols)
	}
	for i := range sh.lists {
		v := &sh.lists[i]
		text, removed := listText(sh.out[:0], v.source)
		sh.out = text
		sh.removed += removed
		if len(text) == 0 {
			continue
		}
		var where strings.Builder
		for k, r := range v.ranges {
			if k > 0 {
				where.WriteByte(' ')
			}
			if r.r0 == r.r1 && r.c0 == r.c1 {
				where.WriteString(cellName(r.r0, r.c0))
			} else {
				where.WriteString(rangeName(r))
			}
		}
		if !sh.room.Take(gofastcore.BlockCost + len(text) + where.Len()) {
			if sh.cut == "" {
				sh.cut = "the limit on the amount of text was reached"
			}
			break
		}
		b := gofastcore.Block{Kind: gofastcore.KindNote, Text: string(text), Attrs: map[string]string{gofastcore.AttrNoteType: gofastcore.NoteChoices}}
		b.Loc.Range = where.String()
		if !v.arrow || sh.allHidden(v.ranges, cols) {
			b.Hidden = gofastcore.HiddenInvisible
		}
		first := v.ranges[0]
		for _, r := range v.ranges[1:] {
			if r.r0 < first.r0 || r.r0 == first.r0 && r.c0 < first.c0 {
				first = r
			}
		}
		out = append(out, placed{row: first.r0, col: first.c0, block: b})
	}
	sh.lists = nil
	return out
}

// listText writes the values of a list. The file writes a list of values
// as a string between quotes, the values separated by commas, a quote
// inside doubled; the commas cannot be escaped. Anything else is a
// reference or a formula, written with its "=".
func listText(dst []byte, source string) ([]byte, int) {
	removed := 0
	if len(source) < 2 || source[0] != '"' || source[len(source)-1] != '"' || strings.Contains(strings.ReplaceAll(source[1:len(source)-1], `""`, ""), `"`) {
		dst = append(dst, '=')
		start := len(dst)
		dst, removed = cellText(dst, []byte(source))
		if len(dst) == start {
			return dst[:0], removed
		}
		return dst, removed
	}
	inner := strings.ReplaceAll(source[1:len(source)-1], `""`, `"`)
	start := len(dst)
	for _, value := range strings.Split(inner, ",") {
		at := len(dst)
		if at > start {
			dst = append(dst, '\n')
		}
		mark := len(dst)
		var n int
		dst, n = cellText(dst, []byte(value))
		removed += n
		// A value is one line: the file cannot hold a line break in a
		// value of a list, and one that did would read as two values.
		for j := mark; j < len(dst); j++ {
			if dst[j] == '\n' || dst[j] == '\t' {
				dst[j] = ' '
			}
		}
		if len(dst) == mark {
			dst = dst[:at] // an empty value offers nothing
		}
	}
	return dst, removed
}

// allHidden reports whether every cell of the ranges is in a hidden row or
// a hidden column. cols is the hidden columns, merged and in order.
func (sh *sheet) allHidden(ranges []rect, cols []span) bool {
	for _, r := range ranges {
		if !rowsHidden(sh.hiddenRows, r.r0, r.r1) && !colsHidden(cols, r.c0, r.c1) {
			return false
		}
	}
	return true
}

// rowsHidden reports whether rows lo to hi are all in rows, which is in
// order with no row twice.
func rowsHidden(rows []int32, lo, hi int32) bool {
	i := sort.Search(len(rows), func(i int) bool { return rows[i] >= lo })
	j := i + int(hi-lo)
	return j < len(rows) && rows[i] == lo && rows[j] == hi
}

// colsHidden reports whether columns lo to hi are all in one of spans,
// which are in order and do not touch.
func colsHidden(spans []span, lo, hi int32) bool {
	i := sort.Search(len(spans), func(i int) bool { return spans[i].hi >= lo })
	return i < len(spans) && spans[i].lo <= lo && spans[i].hi >= hi
}

// mergedSpans returns the spans in order, those that overlap or touch
// made one.
func mergedSpans(spans []span) []span {
	out := append([]span(nil), spans...)
	sort.Slice(out, func(i, j int) bool { return out[i].lo < out[j].lo })
	n := 0
	for _, s := range out {
		if n > 0 && s.lo <= out[n-1].hi+1 {
			out[n-1].hi = max(out[n-1].hi, s.hi)
			continue
		}
		out[n] = s
		n++
	}
	return out[:n]
}
