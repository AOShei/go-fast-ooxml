package sml

import (
	"strconv"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// A pivot table is drawn from a cache: the records of its source, kept in
// the workbook so that the table can be laid out again without the
// source. When the source is a range of the workbook, the records are a
// copy of cells that are read anyway. When the source is another
// workbook, a database, or a sheet or name the workbook no longer has,
// the records are data the file holds and shows nowhere: they are read
// into the stored part, as a table. Like a sheet's cells, they are data,
// and not flagged for their length.

// calcField is a calculated field of a cache: a formula over the other
// fields, which no cell shows.
type calcField struct {
	name, formula string
}

// maxCalculated bounds the calculated fields kept from a workbook.
const maxCalculated = 1 << 12

// pivotCache is a cache the workbook lists.
type pivotCache struct {
	target string // its definition part
	gone   string // why its source is not in the workbook; empty if it is
}

const (
	relPivotCache   = "pivotCacheDefinition"
	relPivotRecords = "pivotCacheRecords"
	maxCaches       = 1 << 10
	maxCacheFields  = 1 << 14
	maxSharedItems  = 1 << 20
)

// readCaches reads pivotCaches in the workbook part: the parts of the
// caches, by the workbook's relationships.
func (b *Book) readCaches(s *xmlscan.Scanner, byID map[string]opc.Rel) {
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start || !s.Is(ns.Sheet, "pivotCache") || len(b.caches) >= maxCaches {
			continue
		}
		id, _ := s.Attr(ns.Rel, "id")
		if rel, ok := byID[string(id)]; ok && !rel.External && ns.IsRel(rel.Type, relPivotCache) {
			b.caches = append(b.caches, pivotCache{target: rel.Target})
		}
	}
}

// judgeCaches decides, for each cache, whether its source is in the
// workbook. A source named by a table or a defined name is in it if the
// workbook has a table or name of that name. It also reads the formulas
// of the caches' calculated fields.
func (b *Book) judgeCaches() {
	sheets := map[string]bool{}
	for _, r := range b.refs {
		sheets[strings.ToLower(r.name)] = true
	}
	names := map[string]bool{}
	for _, n := range b.names {
		names[strings.ToLower(n.name)] = true
	}
	var tables map[string]bool // read when first needed
	for i := range b.caches {
		c := &b.caches[i]
		if !b.wb.pkg.Has(c.target) {
			continue
		}
		var s xmlscan.Scanner
		closer, err := b.wb.pkg.Scan(c.target, &s, scanOptions)
		if err != nil {
			continue
		}
		typ, sheet, name, external := "", "", "", false
		consolidation := []string{}
		for s.Next() != xmlscan.EOF {
			if s.Kind() != xmlscan.Start {
				continue
			}
			switch {
			case s.Is(ns.Sheet, "cacheSource"):
				if v, ok := s.Attr(xmlscan.NoSpace, "type"); ok {
					typ = string(v)
				}
			case s.Is(ns.Sheet, "worksheetSource"):
				if v, ok := s.Attr(xmlscan.NoSpace, "sheet"); ok {
					sheet = string(v)
				}
				if v, ok := s.Attr(xmlscan.NoSpace, "name"); ok {
					name = string(v)
				}
				_, external = s.Attr(ns.Rel, "id")
			case s.Is(ns.Sheet, "rangeSet"):
				if _, ok := s.Attr(ns.Rel, "id"); ok {
					external = true
				}
				if v, ok := s.Attr(xmlscan.NoSpace, "sheet"); ok && len(consolidation) < 256 {
					consolidation = append(consolidation, string(v))
				}
			case s.Is(ns.Sheet, "cacheField"):
				// A calculated field has a formula; what it holds of
				// values is not wanted here.
				if f, ok := s.Attr(xmlscan.NoSpace, "formula"); ok && len(b.calculated) < maxCalculated {
					formula, _ := clean.String(f, clean.Prose)
					n, _ := s.Attr(xmlscan.NoSpace, "name")
					if formula != "" {
						b.calculated = append(b.calculated, calcField{name: clean.Name(string(n)), formula: strings.TrimPrefix(formula, "=")})
					}
				}
				s.Skip()
			case s.Is(ns.Sheet, "extLst"), s.Is(ns.Sheet, "cacheHierarchies"), s.Is(ns.Sheet, "kpis"), s.Is(ns.Sheet, "dimensions"):
				s.Skip()
			}
		}
		closer.Close()
		switch {
		case typ == "external":
			c.gone = "a connection to a source outside the workbook"
		case typ == "scenario":
			c.gone = "the scenarios of the workbook"
		case external:
			c.gone = "another workbook"
		case typ == "consolidation":
			for _, sh := range consolidation {
				if !sheets[strings.ToLower(sh)] {
					c.gone = "a sheet the workbook does not have"
				}
			}
		case sheet != "" && !sheets[strings.ToLower(sheet)]:
			c.gone = "a sheet the workbook does not have"
		case sheet == "" && name != "":
			// A named range or a table.
			if names[strings.ToLower(name)] {
				continue
			}
			if tables == nil {
				tables = b.tableNames()
			}
			if !tables[strings.ToLower(name)] {
				c.gone = "a name the workbook does not have"
			}
		}
	}
}

// tableNames returns the names of the tables the workbook's sheets
// declare, in lower case.
func (b *Book) tableNames() map[string]bool {
	out := map[string]bool{}
	for _, ref := range b.refs {
		if ref.target == "" || !b.wb.pkg.Has(ref.target) {
			continue
		}
		rels, _ := b.wb.pkg.Rels(ref.target)
		for _, rel := range rels {
			if rel.External || !ns.IsRel(rel.Type, ns.RelTable) || !b.wb.pkg.Has(rel.Target) {
				continue
			}
			var s xmlscan.Scanner
			closer, err := b.wb.pkg.Scan(rel.Target, &s, scanOptions)
			if err != nil {
				continue
			}
			for s.Next() != xmlscan.EOF {
				if s.Kind() != xmlscan.Start {
					continue
				}
				for _, attr := range []string{"name", "displayName"} {
					if v, ok := s.Attr(xmlscan.NoSpace, attr); ok {
						out[strings.ToLower(string(v))] = true
					}
				}
				break
			}
			closer.Close()
		}
	}
	return out
}

// goneCaches counts the caches whose source is not in the workbook.
func (b *Book) goneCaches() int {
	n := 0
	for _, c := range b.caches {
		if c.gone != "" {
			n++
		}
	}
	return n
}

// cacheField is a field of a cache: its name, and the values records
// refer to by number.
type cacheField struct {
	name  string
	items []string
}

// cacheTable reads a cache whose source is gone: a table with the names
// of its fields in the header row and a row for each record, taking from
// room as it goes. A cache that keeps no records gives the values each
// field takes, a column each. cut reports that the budget ran out.
func (b *Book) cacheTable(c pivotCache, room *gofastcore.TextBudget) (block gofastcore.Block, ok, cut, damaged bool) {
	fields, records, err := b.cacheFields(c.target)
	if err != nil {
		damaged = true
	}
	if len(fields) == 0 {
		return block, false, false, damaged
	}
	cols := len(fields)
	head := make([]string, cols)
	text := 0
	for i, f := range fields {
		head[i] = f.name
		text += len(f.name)
	}
	if !room.Take(gofastcore.BlockCost + gofastcore.CellCost*cols + text) {
		return block, false, true, damaged
	}
	rows := [][]string{head}
	// A row is taken from the budget when it is read: its cells and its
	// text.
	add := func(row []string) bool {
		n := gofastcore.CellCost * cols
		for _, v := range row {
			n += len(v)
		}
		if !room.Take(n) {
			return false
		}
		if b.wb.cells.Add(-int64(cols)) < 0 {
			return false
		}
		rows = append(rows, row)
		return true
	}
	if records != "" && b.wb.pkg.Has(records) {
		cut, err = b.cacheRecords(records, fields, add)
		if err != nil {
			damaged = true
		}
	} else {
		// No records: the values each field takes.
		longest := 0
		for _, f := range fields {
			longest = max(longest, len(f.items))
		}
		for i := 0; i < longest && !cut; i++ {
			row := make([]string, cols)
			for j, f := range fields {
				if i < len(f.items) {
					row[j] = f.items[i]
				}
			}
			cut = !add(row)
		}
	}
	block = gofastcore.Block{Kind: gofastcore.KindTable, Table: &gofastcore.Table{Rows: rows, HeaderRows: 1},
		Attrs: map[string]string{gofastcore.AttrStored: gofastcore.StoredPivotCache}}
	return block, true, cut, damaged
}

// cacheFields reads the fields of a cache's definition, and names its
// records part.
func (b *Book) cacheFields(target string) (fields []cacheField, records string, err error) {
	rels, _ := b.wb.pkg.Rels(target)
	byID := map[string]opc.Rel{}
	for _, rel := range rels {
		byID[rel.ID] = rel
	}
	var s xmlscan.Scanner
	closer, err := b.wb.pkg.Scan(target, &s, scanOptions)
	if err != nil {
		return nil, "", err
	}
	defer closer.Close()
	items := 0
	for s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Is(ns.Sheet, "pivotCacheDefinition"):
			if id, ok := s.Attr(ns.Rel, "id"); ok {
				if rel, ok := byID[string(id)]; ok && !rel.External && ns.IsRel(rel.Type, relPivotRecords) {
					records = rel.Target
				}
			}
		case s.Is(ns.Sheet, "cacheField"):
			if len(fields) >= maxCacheFields {
				s.Skip()
				continue
			}
			var f cacheField
			if v, ok := s.Attr(xmlscan.NoSpace, "name"); ok {
				f.name, _ = clean.String(v, clean.Prose)
			}
			depth := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= depth {
				if s.Kind() != xmlscan.Start {
					continue
				}
				if s.Depth() == depth+1 && !s.Is(ns.Sheet, "sharedItems") {
					s.Skip() // groups of values, formulas: not values records name
					continue
				}
				if s.Depth() != depth+2 || items >= maxSharedItems {
					continue
				}
				items++
				f.items = append(f.items, b.cacheValue(&s))
			}
			fields = append(fields, f)
		case s.Is(ns.Sheet, "extLst"):
			s.Skip()
		}
	}
	return fields, records, s.Err()
}

// cacheValue reads a value of a cache (s, n, b, d, e, m), whose Start
// token is current, as text.
func (b *Book) cacheValue(s *xmlscan.Scanner) string {
	v, _ := s.Attr(xmlscan.NoSpace, "v")
	switch string(s.Local()) {
	case "m":
		return ""
	case "b":
		if isTrue(v) {
			return "TRUE"
		}
		return "FALSE"
	case "n":
		if f, err := strconv.ParseFloat(string(v), 64); err == nil {
			out := strconv.AppendFloat(b.raw[:0], f, 'g', 15, 64)
			return string(out)
		}
	case "d":
		// ISO 8601 already; a date at midnight is a date.
		return strings.TrimSuffix(clean.Label(string(v)), "T00:00:00")
	}
	t, _ := clean.String(v, clean.Prose)
	return t
}

// cacheRecords reads a records part, handing each record to add as a
// row, until add reports false. cut reports that it did.
func (b *Book) cacheRecords(target string, fields []cacheField, add func([]string) bool) (cut bool, err error) {
	var s xmlscan.Scanner
	closer, err := b.wb.pkg.Scan(target, &s, scanOptions)
	if err != nil {
		return false, err
	}
	defer closer.Close()
	for s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start || !s.Is(ns.Sheet, "r") {
			continue
		}
		row := make([]string, len(fields))
		i := 0
		depth := s.Depth()
		for s.Next() != xmlscan.EOF && s.Depth() >= depth {
			if s.Kind() != xmlscan.Start || s.Depth() != depth+1 {
				continue
			}
			if i < len(row) {
				if string(s.Local()) == "x" {
					// A value of the field's shared items, by number.
					if v, ok := intAttr(&s, "v"); ok && v >= 0 && v < len(fields[i].items) {
						row[i] = fields[i].items[v]
					}
				} else {
					row[i] = b.cacheValue(&s)
				}
			}
			i++
		}
		if !add(row) {
			return true, s.Err()
		}
	}
	return false, s.Err()
}
