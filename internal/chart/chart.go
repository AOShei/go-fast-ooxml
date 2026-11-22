// Package chart reads a chart part: the classic charts of DrawingML (c:)
// and the kinds Office added in 2016 (cx:), as Word, Excel and PowerPoint
// all store them.
//
// What a chart shows is its data: the names of its series, its categories
// and its values, as the chart's cache holds them. They are given as a
// table, with the chart's title as a caption before it and its type and
// the titles of its axes as attributes (gofastcore.RoleChart). The values
// are written through their number formats, as the chart labels them.
//
// The cache is what the chart was drawn from when it was saved; the
// workbook it was taken from may say something else now, and is not
// consulted. A series the chart's filter leaves out is declared not
// drawn, and its table is marked hidden.
package chart

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/numfmt"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// Limits on what a chart part may make the reader hold. The text the
// chart becomes is bounded by the document's text budget; these bound the
// work done before it is charged.
const (
	maxSeries = warn.MaxChartSeries
	maxPoints = warn.MaxChartPoints // points of one series or one level of categories
	maxLevels = warn.MaxChartLevels // levels of categories
	maxKinds  = warn.MaxChartAxes
	maxAxes   = warn.MaxChartAxes
	maxValues = warn.MaxChartValues // sets of values of one series
	maxTitle  = warn.MaxChartTitle  // bytes of a title, of a series' name
	// The points of all the series of a chart, and the cells of all its
	// tables: what is held before any of it is charged to the budget.
	maxTotal = warn.MaxChartTotal
)

// Chart is what a chart part says.
type Chart struct {
	// Kinds are the types of the chart's plots, lowercase as the source
	// names them ("bar", "line", "pie", "waterfall"), in order, each once.
	Kinds []string
	// Title is the chart's title as shown; AxisX and AxisY are the titles
	// of its horizontal and vertical axes.
	Title, AxisX, AxisY string
	// Tables are the chart's data: one for each set of series that share
	// their categories.
	Tables []Table
	// Rels are the chart part's relationships, among them the workbook a
	// chart in a document or presentation embeds.
	Rels []opc.Rel
	// Removed counts control characters taken out of its text.
	Removed int
	// Lost counts what the reader's fixed bounds left out.
	Lost warn.Lost
}

// Table is a table of a chart's data: a header row with the names of the
// series, a column for each level of categories, outermost first, and a
// column of values for each series.
type Table struct {
	Rows [][]string
	// Filtered: the file declares the series filtered out of the chart.
	Filtered bool
}

// dim is the data of one dimension of a series: its categories (several
// levels, innermost first) or its values (one level).
type dim struct {
	levels [][]string
}

func (d *dim) len() int {
	n := 0
	for _, l := range d.levels {
		n = max(n, len(l))
	}
	return n
}

func (d *dim) equal(o *dim) bool {
	if len(d.levels) != len(o.levels) {
		return false
	}
	for i := range d.levels {
		a, b := d.levels[i], o.levels[i]
		if len(a) != len(b) {
			return false
		}
		for j := range a {
			if a[j] != b[j] {
				return false
			}
		}
	}
	return true
}

type series struct {
	name   string
	cats   dim
	vals   []dim // one for most kinds; a bubble chart's sizes, and the dimensions of a chart of the newer kind, are more
	hidden bool
}

// reader holds what is shared while one part is read.
type reader struct {
	s        xmlscan.Scanner
	date1904 bool
	removed  int
	buf      []byte
	formats  map[string]*numfmt.Format
	total    int  // points held so far
	cut      bool // points were left out
	lost     *warn.Lost
}

var scanOptions = &xmlscan.Options{Spaces: ns.Spaces}

// Read reads the chart part name of pkg, of either kind.
func Read(pkg *opc.Package, name string) (*Chart, error) {
	c := &Chart{}
	rels, err := pkg.Rels(name)
	if err == nil {
		c.Rels = rels
	}
	r := &reader{formats: map[string]*numfmt.Format{}, lost: &c.Lost}
	closer, err := pkg.Scan(name, &r.s, scanOptions)
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	var list []series
	for r.s.Next() != xmlscan.EOF {
		if r.s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case r.s.Is(ns.Chart, "chartSpace"):
			list = r.classic(c)
		case r.s.Is(ns.ChartEx, "chartSpace"):
			list = r.newer(c)
		default:
			r.s.Skip()
		}
	}
	c.Removed = r.removed
	c.Tables = tables(list, &r.cut)
	if r.cut {
		c.Lost.Add(warn.CapChartTotal, 1)
	}
	if err := r.s.Err(); err != nil {
		return c, err
	}
	return c, nil
}

// text returns the text of the current element, cleaned.
func (r *reader) text() string {
	r.buf = r.s.AppendText(r.buf[:0])
	t, n := clean.String(r.buf, clean.Prose)
	r.removed += n
	return t
}

// kind adds a type of plot to the chart, once.
func (c *Chart) kind(k string) {
	k = strings.ToLower(c.Lost.Label(k))
	if k == "" {
		return
	}
	for _, have := range c.Kinds {
		if have == k {
			return
		}
	}
	if len(c.Kinds) >= maxKinds {
		c.Lost.Add(warn.CapChartAxes, 1)
		return
	}
	c.Kinds = append(c.Kinds, k)
}

// value writes a value of a point through its number format.
func (r *reader) value(raw, code string) string {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		t, n := clean.String([]byte(raw), clean.Prose)
		r.removed += n
		return t
	}
	f := numfmt.General()
	if code != "" && !strings.EqualFold(code, "General") {
		var ok bool
		if f, ok = r.formats[code]; !ok {
			if len(r.formats) < 256 {
				f = numfmt.Parse(code)
				r.formats[code] = f
			} else {
				f = numfmt.Parse(code)
			}
		}
	}
	r.buf, _ = f.Number(r.buf[:0], v, r.date1904)
	t, _ := clean.String(r.buf, clean.Prose)
	return t
}

// points reads the points of a cache or a literal (c:strCache, c:numCache,
// c:lvl, c:strLit, c:numLit; cx:lvl), whose Start token is current, into
// one level. numeric says the values are numbers, to be formatted.
func (r *reader) points(space xmlscan.Space, numeric bool, code string) []string {
	s := &r.s
	count := -1
	if v, ok := s.Attr(xmlscan.NoSpace, "ptCount"); ok {
		count = atoi(v)
	}
	if v, ok := s.Attr(xmlscan.NoSpace, "formatCode"); ok {
		code = string(v)
	}
	var idx []int
	var vals []string
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Is(space, "ptCount"):
			if v, ok := s.Attr(xmlscan.NoSpace, "val"); ok {
				count = atoi(v)
			}
		case s.Is(space, "formatCode"):
			r.buf = s.AppendText(r.buf[:0])
			code = string(bytes.TrimSpace(r.buf))
		case s.Is(space, "pt"):
			i := -1
			if v, ok := s.Attr(xmlscan.NoSpace, "idx"); ok {
				i = atoi(v)
			}
			own := code
			if v, ok := s.Attr(xmlscan.NoSpace, "formatCode"); ok {
				own = string(v)
			}
			var raw string
			if space == ns.ChartEx {
				raw = r.textRaw()
			} else {
				pd := s.Depth()
				for s.Next() != xmlscan.EOF && s.Depth() >= pd {
					if s.Kind() == xmlscan.Start && s.Is(space, "v") {
						raw = r.textRaw()
					}
				}
			}
			if i >= maxPoints {
				r.lost.Add(warn.CapChartPoints, 1)
				continue
			}
			if i < 0 {
				continue
			}
			if numeric {
				raw = r.value(raw, own)
			} else {
				t, n := clean.String([]byte(raw), clean.Prose)
				r.removed += n
				raw = t
			}
			idx, vals = append(idx, i), append(vals, raw)
		default:
			s.Skip()
		}
	}
	// Points the cache counts and does not hold are empty: gaps in the
	// data. Every point held, empty or not, counts towards the chart's
	// total.
	n := max(count, 0) // -1 when the cache gives no count
	for _, i := range idx {
		n = max(n, i+1)
	}
	n = min(n, maxPoints)
	if r.total+n > maxTotal {
		n, r.cut = max(maxTotal-r.total, 0), true
	}
	r.total += n
	out := make([]string, n)
	for k, i := range idx {
		if i < n {
			out[i] = vals[k]
		}
	}
	return out
}

// textRaw returns the text of the current element as it is.
func (r *reader) textRaw() string {
	r.buf = r.s.AppendText(r.buf[:0])
	return string(r.buf)
}

func atoi(b []byte) int {
	n, err := strconv.Atoi(string(bytes.TrimSpace(b)))
	if err != nil || n < 0 {
		return -1
	}
	return n
}

// tables groups the series by their categories: series that share them
// share a table. Series filtered out of the chart go to tables of their
// own, after the others.
func tables(list []series, cut *bool) []Table {
	var out []Table
	for _, hidden := range []bool{false, true} {
		done := make([]bool, len(list))
		for i := range list {
			if done[i] || list[i].hidden != hidden {
				continue
			}
			group := []*series{&list[i]}
			for j := i + 1; j < len(list); j++ {
				if !done[j] && list[j].hidden == hidden && list[j].cats.equal(&list[i].cats) {
					group = append(group, &list[j])
					done[j] = true
				}
			}
			out = append(out, table(group, hidden, cut))
		}
	}
	return out
}

func table(group []*series, hidden bool, cut *bool) Table {
	cats := &group[0].cats
	levels := len(cats.levels)
	n := cats.len()
	cols := levels
	for _, s := range group {
		for k := range s.vals {
			n = max(n, s.vals[k].len())
			cols++
		}
	}
	if cols > 0 && (n+1)*cols > maxTotal {
		n, *cut = maxTotal/cols-1, true
	}
	rows := make([][]string, 0, n+1)
	head := make([]string, cols)
	c := levels
	for _, s := range group {
		for range s.vals {
			head[c] = s.name
			c++
		}
	}
	rows = append(rows, head)
	for i := 0; i < n; i++ {
		row := make([]string, cols)
		for l := 0; l < levels; l++ {
			level := cats.levels[levels-1-l] // outermost first
			if i < len(level) {
				row[l] = level[i]
			}
		}
		c := levels
		for _, s := range group {
			for k := range s.vals {
				if v := s.vals[k].levels; len(v) > 0 && i < len(v[0]) {
					row[c] = v[0][i]
				}
				c++
			}
		}
		rows = append(rows, row)
	}
	return Table{Rows: rows, Filtered: hidden}
}

// Blocks returns the chart as blocks: a caption with its title, if it has
// one, and a table block for each of its tables, with name (the name the
// source gives the chart, if any), the chart's type and its axes' titles.
// What they hold is taken from budget: the caption and each table are
// blocks, each cell takes gofastcore.CellCost, and the text its bytes. A
// table there is no room for in full keeps the rows that fit; cut reports
// that the budget ran out. taken is what was taken.
func (c *Chart) Blocks(budget *gofastcore.TextBudget, name string) (blocks []gofastcore.Block, taken int64, cut bool) {
	if c.Title != "" {
		n := gofastcore.BlockCost + len(c.Title)
		if !budget.Take(n) {
			return nil, taken, true
		}
		taken += int64(n)
		blocks = append(blocks, gofastcore.Block{Kind: gofastcore.KindCaption, Text: c.Title})
	}
	attrs := map[string]string{gofastcore.AttrRole: gofastcore.RoleChart}
	if len(c.Kinds) > 0 {
		attrs[gofastcore.AttrChart] = strings.Join(c.Kinds, " ")
	}
	if c.AxisX != "" {
		attrs[gofastcore.AttrAxisX] = c.AxisX
	}
	if c.AxisY != "" {
		attrs[gofastcore.AttrAxisY] = c.AxisY
	}
	if name != "" {
		attrs[gofastcore.AttrName] = name
	}
	// The attributes are text the file decides, given with every table.
	said := 0
	for _, v := range attrs {
		said += len(v)
	}
	for k := range c.Tables {
		t := &c.Tables[k]
		cols := 0
		if len(t.Rows) > 0 {
			cols = len(t.Rows[0])
		}
		if cols == 0 {
			continue
		}
		fit := warn.FitRows(budget, len(t.Rows), cols)
		if fit == 0 {
			return blocks, taken, true
		}
		taken += int64(gofastcore.BlockCost + gofastcore.CellCost*fit*cols)
		rows := t.Rows[:fit]
		cut = fit < len(t.Rows)
		for i, row := range rows {
			n := 0
			for _, cell := range row {
				n += len(cell)
			}
			if !budget.Take(n) {
				rows, cut = rows[:i], true
				break
			}
			taken += int64(n)
		}
		if len(rows) == 0 {
			return blocks, taken, true
		}
		if budget.Take(said) {
			taken += int64(said)
		} else {
			cut = true
		}
		b := gofastcore.Block{Kind: gofastcore.KindTable, Table: &gofastcore.Table{Rows: rows, HeaderRows: 1}, Attrs: make(map[string]string, len(attrs))}
		for key, v := range attrs {
			b.Attrs[key] = v
		}
		if t.Filtered {
			b.Display = gofastcore.DisplayHidden
		}
		blocks = append(blocks, b)
		if cut {
			return blocks, taken, true
		}
	}
	return blocks, taken, false
}
