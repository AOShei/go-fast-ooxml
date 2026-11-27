package vsd

import (
	"context"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-core/clean"
	"github.com/AOShei/go-fast-core/xmlscan"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/stored"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
)

// A drawing linked to outside data (a workbook, a database, a list)
// keeps a copy of what it last fetched: a data recordset. The drawing's
// recordsets part lists them, each with a relationship to a part that
// holds its rows as an ADO rowset saved as XML: the columns in the
// rowset's schema (s:AttributeType, named by rs:name where the column's
// name is not an XML name), then one z:row element per row, with an
// attribute per column that has a value. Each recordset is a table of
// the stored part (linked_data), its column names the header row.

// recordset is a data recordset as the drawing lists it.
type recordset struct {
	name   string
	target string // the part that holds its rows
}

// Bounds of what is read of recordsets.
const (
	maxRecordsets    = warn.MaxRecordsets
	maxRecordColumns = warn.MaxRecordColumns
)

// readRecordsets reads the list of data recordsets.
func (d *drawing) readRecordsets(name string) {
	rels, err := d.pkg.Rels(name)
	d.note(opc.RelsName(name), err)
	byRel := make(map[string]string, len(rels))
	for _, rel := range rels {
		if !rel.External && ns.IsRel(rel.Type, relRecordset) {
			byRel[rel.ID] = rel.Target
		}
	}
	d.load(name, func(s *xmlscan.Scanner) {
		for s.Next() != xmlscan.EOF {
			if s.Kind() != xmlscan.Start {
				continue
			}
			switch {
			case s.Depth() == 1:
			case s.Is(ns.Visio, "DataRecordSet") && s.Depth() == 2:
				var set recordset
				if v, ok := s.Attr(xmlscan.NoSpace, "Name"); ok {
					set.name = string(v)
				}
				d2 := s.Depth()
				for s.Next() != xmlscan.EOF && s.Depth() >= d2 {
					if s.Kind() != xmlscan.Start {
						continue
					}
					if s.Depth() == d2+1 && s.Is(ns.Visio, "Rel") && set.target == "" {
						if rid, ok := s.Attr(ns.Rel, "id"); ok {
							set.target = byRel[string(rid)]
						}
					}
					s.Skip()
				}
				if set.target == "" || !d.pkg.Has(set.target) {
					continue
				}
				if len(d.sets) >= maxRecordsets {
					d.lost.Add(warn.CapRecordsets, 1)
					continue
				}
				d.sets = append(d.sets, set)
			default:
				s.Skip()
			}
		}
	})
}

// recordsets adds a table to the stored part for each data recordset.
func (d *drawing) recordsets(ctx context.Context, p *stored.Part) {
	for _, set := range d.sets {
		if p.Full() || ctx.Err() != nil {
			return
		}
		d.recordset(set, p)
	}
}

// recordset adds the table of one data recordset to the stored part.
func (d *drawing) recordset(set recordset, p *stored.Part) {
	var s xmlscan.Scanner
	c, err := d.pkg.Scan(set.target, &s, scanOptions)
	if err != nil {
		p.Note(set.target, err)
		return
	}
	defer c.Close()
	room := p.Room()
	var head []string
	keys := map[string]int{} // a column's attribute name to its place
	var rows [][]string
	started, stop := false, false
	// start takes the header row from the budgets, before the first row.
	start := func() bool {
		if started {
			return true
		}
		started = true
		n := gofastcore.BlockCost
		for _, h := range head {
			n += gofastcore.CellCost + len(h)
		}
		if !room.Take(n) {
			p.Cut()
			return false
		}
		if k := int64(len(head)); d.cells.Add(-k) < 0 {
			d.cells.Add(k)
			p.Limit("the limit on table cells was reached; data recordsets are cut off there")
			return false
		}
		rows = append(rows, head)
		return true
	}
	for !stop && s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Is(ns.ADOSchema, "AttributeType") && !started:
			key, _ := s.Attr(xmlscan.NoSpace, "name")
			label, ok := s.Attr(ns.ADORowset, "name")
			if !ok || len(label) == 0 {
				label = key
			}
			if _, dup := keys[string(key)]; dup || len(key) == 0 {
				s.Skip()
				continue
			}
			if len(head) >= maxRecordColumns {
				p.Lost.Add(warn.CapRecordColumns, 1)
				s.Skip()
				continue
			}
			keys[string(key)] = len(head)
			h, _ := clean.String(label, clean.Prose)
			head = append(head, p.Lost.Label(h))
			s.Skip()
		case s.Is(ns.ADORow, "row"):
			if len(head) == 0 {
				s.Skip()
				continue
			}
			if !start() {
				stop = true
				continue
			}
			row := make([]string, len(head))
			n := 0
			for i := 0; i < s.NumAttr(); i++ {
				space, local, value := s.AttrAt(i)
				if space != xmlscan.NoSpace {
					continue
				}
				if j, ok := keys[string(local)]; ok {
					row[j], _ = clean.String(value, clean.Prose)
				}
			}
			for _, x := range row {
				n += gofastcore.CellCost + len(x)
			}
			s.Skip()
			if !room.Take(n) {
				p.Cut()
				stop = true
				continue
			}
			if k := int64(len(head)); d.cells.Add(-k) < 0 {
				d.cells.Add(k)
				p.Limit("the limit on table cells was reached; data recordsets are cut off there")
				stop = true
				continue
			}
			rows = append(rows, row)
		}
	}
	if !stop {
		p.Note(set.target, s.Err())
	}
	if len(head) > 0 && !started && !start() {
		return
	}
	if len(rows) == 0 {
		return
	}
	b := gofastcore.Block{Kind: gofastcore.KindTable, Table: &gofastcore.Table{HeaderRows: 1, Rows: rows},
		Attrs: map[string]string{gofastcore.AttrStored: gofastcore.StoredLinkedData}}
	if name := p.Lost.Name(set.name); name != "" {
		b.Attrs[gofastcore.AttrName] = name
	}
	p.Blocks = append(p.Blocks, b)
}
