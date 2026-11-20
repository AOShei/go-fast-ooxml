package sml

import (
	"bytes"
	"sort"
	"strings"
	"unicode"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/formula"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// What a cell holds besides what it shows (gofastcore.CellDetail): with
// Options.Cells its formula, and the type and stored value of what it
// holds; with Options.Spans where it links to.

// pos is the place of a cell on its sheet.
type pos struct{ row, col int32 }

// detail is what a cell holds besides its text, before its table is made.
// The details of a sheet are kept in the order of its cells, and found by
// their place.
type detail struct {
	at             pos
	formula, value string
	typ            gofastcore.CellType
}

// sharedFormula is the first cell of a shared formula: its formula, and
// where it is, from which the formulas of the other cells are moved.
type sharedFormula struct {
	text     string
	row, col int32
}

// Bounds on what a sheet keeps of formulas.
const (
	maxFormula = 32 << 10 // Excel's own limit is 8,192 characters
	maxShared  = 1 << 20
	maxLinks   = 1 << 18
	// A link over a range of more cells than this is kept as a range
	// and looked up, not spread over its cells.
	linkSpread = 1 << 12
)

// formulaOf reads the formula of the cell at row, col (the f element
// whose Start token is current) as Excel shows it, without "=".
func (sh *sheet) formulaOf(s *xmlscan.Scanner, row, col int32) string {
	typ, _ := s.Attr(xmlscan.NoSpace, "t")
	si, shared := intAttr(s, "si")
	_, master := s.Attr(xmlscan.NoSpace, "ref")
	var r1, r2 string
	var twoD, rowInput bool
	if string(typ) == "dataTable" {
		if v, ok := s.Attr(xmlscan.NoSpace, "r1"); ok {
			r1 = clean.Label(string(v))
		}
		if v, ok := s.Attr(xmlscan.NoSpace, "r2"); ok {
			r2 = clean.Label(string(v))
		}
		v, _ := s.Attr(xmlscan.NoSpace, "dt2D")
		twoD = isTrue(v)
		v, _ = s.Attr(xmlscan.NoSpace, "dtr")
		rowInput = isTrue(v)
	}
	sh.fbuf = s.AppendText(sh.fbuf[:0])
	text := verbatim(sh.fbuf)
	switch string(typ) {
	case "shared":
		if !shared {
			return text
		}
		if master && text != "" {
			if sh.shared == nil {
				sh.shared = map[int]sharedFormula{}
			}
			if len(sh.shared) < maxShared {
				sh.shared[si] = sharedFormula{text, row, col}
			}
			return text
		}
		if m, ok := sh.shared[si]; ok {
			return formula.Shift(m.text, int(row-m.row), int(col-m.col))
		}
		return text
	case "dataTable":
		// A what-if table: Excel shows it as {=TABLE(row input, column
		// input)}.
		switch {
		case twoD:
			return "TABLE(" + r1 + "," + r2 + ")"
		case rowInput:
			return "TABLE(" + r1 + ",)"
		default:
			return "TABLE(," + r1 + ")"
		}
	}
	return text
}

// verbatim makes formula text fit for the output: valid UTF-8, no control
// characters but line breaks and tabs, of bounded length; nothing else is
// changed.
func verbatim(b []byte) string {
	if len(b) > maxFormula {
		b = b[:maxFormula]
	}
	s := strings.ToValidUTF8(string(b), "�")
	if strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) < 0 {
		return s
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, s)
}

// keepDetail records what a cell just added holds besides its text, if
// there is anything: its formula, and the type of what it holds with the
// value where the text does not show it in full.
func (sh *sheet) keepDetail(c *cell, typ byte, raw []byte, f string, date bool) {
	d := detail{at: pos{c.row, c.col}, formula: f}
	switch typ {
	case 'n':
		d.typ = gofastcore.CellNumber
		if date {
			d.typ = gofastcore.CellDate
		}
		if v := bytes.TrimSpace(raw); string(v) != c.text {
			d.value = clean.Label(string(v))
		}
	case 'd':
		d.typ = gofastcore.CellDate
		if v := bytes.TrimSpace(raw); string(v) != c.text {
			d.value = clean.Label(string(v))
		}
	case 'b':
		d.typ = gofastcore.CellBool
	case 'e':
		d.typ = gofastcore.CellError
	default:
		if f == "" {
			return // text, as shown
		}
		d.typ = gofastcore.CellText
	}
	if n := len(sh.details); n > 0 && !before(sh.details[n-1].at, d.at) {
		sh.detailsUnsorted = true
	}
	sh.details = append(sh.details, d)
}

func before(a, b pos) bool { return a.row < b.row || a.row == b.row && a.col < b.col }

// sortDetails puts the details in the order of the cells, as sort does the
// cells, keeping the first of a cell given twice.
func (sh *sheet) sortDetails() {
	if !sh.detailsUnsorted {
		return
	}
	sort.SliceStable(sh.details, func(i, j int) bool { return before(sh.details[i].at, sh.details[j].at) })
	kept := sh.details[:0]
	for i := range sh.details {
		if n := len(kept); n > 0 && kept[n-1].at == sh.details[i].at {
			continue
		}
		kept = append(kept, sh.details[i])
	}
	sh.details, sh.detailsUnsorted = kept, false
}

// detailOf returns the detail of the cell at p, or nil.
func (sh *sheet) detailOf(p pos) *detail {
	i := sort.Search(len(sh.details), func(i int) bool { return !before(sh.details[i].at, p) })
	if i < len(sh.details) && sh.details[i].at == p {
		return &sh.details[i]
	}
	return nil
}

// link is a hyperlink of a range of cells.
type link struct {
	at   rect
	href string
}

// readLinks reads hyperlinks: where each range of cells links to, an
// address outside the file or a place in the workbook.
func (sh *sheet) readLinks(s *xmlscan.Scanner) {
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start || !s.Is(ns.Sheet, "hyperlink") || len(sh.links)+len(sh.linkAt) >= maxLinks {
			continue
		}
		ref, ok := s.Attr(xmlscan.NoSpace, "ref")
		if !ok {
			continue
		}
		at, ok := parseRange(ref)
		if !ok {
			continue
		}
		href := ""
		if id, ok := s.Attr(ns.Rel, "id"); ok {
			if rel, found := sh.rels[string(id)]; found && rel.External {
				href = rel.Target // cleaned when the relationships were read
			}
		}
		if v, ok := s.Attr(xmlscan.NoSpace, "location"); ok && href == "" && len(v) > 0 {
			href = "#" + clean.Address(string(v))
		}
		sh.addLink(at, href)
	}
}

// addLink records that the cells of at link to href.
func (sh *sheet) addLink(at rect, href string) {
	if href == "" {
		return
	}
	if n := int64(at.r1-at.r0+1) * int64(at.c1-at.c0+1); n > linkSpread {
		sh.links = append(sh.links, link{at, href})
		return
	}
	if sh.linkAt == nil {
		sh.linkAt = map[pos]string{}
	}
	// A cell given two links has the later: the one a file adds after
	// another is the one it means.
	for r := at.r0; r <= at.r1; r++ {
		for c := at.c0; c <= at.c1; c++ {
			sh.linkAt[pos{r, c}] = href
		}
	}
}

// hrefOf returns where the cell at row, col links to, or "".
func (sh *sheet) hrefOf(row, col int32) string {
	if h, ok := sh.linkAt[pos{row, col}]; ok {
		return h
	}
	for i := len(sh.links) - 1; i >= 0; i-- {
		if l := &sh.links[i]; row >= l.at.r0 && row <= l.at.r1 && col >= l.at.c0 && col <= l.at.c1 {
			return l.href
		}
	}
	return ""
}

// cellDetail returns the entry of Table.Cells for the cell c, placed at
// r, c of its table, and whether it has one.
func (sh *sheet) cellDetail(c *cell, r, col int) (gofastcore.CellDetail, bool) {
	out := gofastcore.CellDetail{Row: r, Col: col}
	if sh.wb.opts.Spans && (sh.linkAt != nil || sh.links != nil) {
		out.Href = sh.hrefOf(c.row, c.col)
	}
	if d := sh.detailOf(pos{c.row, c.col}); d != nil {
		out.Formula, out.Type, out.Value = d.formula, d.typ, d.value
	}
	return out, out.Href != "" || out.Formula != "" || out.Type != gofastcore.CellUnset
}

// detailBytes is what the entry of a cell takes from the text budget, and
// whether it has one.
func (sh *sheet) detailBytes(c *cell) (int64, bool) {
	if len(sh.details) == 0 && sh.linkAt == nil && sh.links == nil {
		return 0, false
	}
	d, ok := sh.cellDetail(c, 0, 0)
	if !ok {
		return 0, false
	}
	return int64(len(d.Href) + len(d.Formula) + len(d.Value)), true
}
