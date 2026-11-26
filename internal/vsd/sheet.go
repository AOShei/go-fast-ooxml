package vsd

import (
	"strconv"

	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// A Visio drawing describes every shape by a sheet of cells, as a
// spreadsheet would: single cells (PinX, Width, HideText), and sections of
// rows of cells (the Character section's rows hold the formats of the
// text's runs, the Property section's rows the shape's data). A shape that
// is an instance of a master says only what differs from the master's
// shape; every cell, row and text it does not set, it takes from there.
// The value of each cell is stored as Visio last worked it out (V), so
// nothing has to be calculated.

// cellID names the single cells that are read.
type cellID uint8

const (
	cPinX cellID = iota
	cPinY
	cWidth
	cHeight
	cLocPinX
	cLocPinY
	cAngle
	cFlipX
	cFlipY
	cBeginX
	cBeginY
	cEndX
	cEndY
	cHideText
	cLayerMember
	cPageWidth
	cPageHeight
	numCells
)

var cellIDs = map[string]cellID{
	"PinX": cPinX, "PinY": cPinY, "Width": cWidth, "Height": cHeight,
	"LocPinX": cLocPinX, "LocPinY": cLocPinY, "Angle": cAngle,
	"FlipX": cFlipX, "FlipY": cFlipY,
	"BeginX": cBeginX, "BeginY": cBeginY, "EndX": cEndX, "EndY": cEndY,
	"HideText": cHideText, "LayerMember": cLayerMember,
	"PageWidth": cPageWidth, "PageHeight": cPageHeight,
}

// cells holds the single cells a sheet sets.
type cells struct {
	v   [numCells]string
	set uint32
}

func (c *cells) put(id cellID, v string) {
	c.v[id] = v
	c.set |= 1 << id
}

func (c *cells) get(id cellID) (string, bool) {
	return c.v[id], c.set&(1<<id) != 0
}

// secID names the sections that are read.
type secID uint8

const (
	sChar      secID = iota // Character: the formats of the text's runs, by IX
	sPara                   // Paragraph: the formats of its paragraphs, by IX
	sField                  // Field: the values of its fields, by IX
	sProperty               // Property: the shape's data, by name
	sHyperlink              // Hyperlink: its links, by name
	sLayer                  // Layer, of a page: its layers, by IX
	numSections
)

var secIDs = map[string]secID{
	"Character": sChar, "Paragraph": sPara, "Field": sField,
	"Property": sProperty, "Hyperlink": sHyperlink, "Layer": sLayer,
}

// kept lists the cells of each section that are read; the others are
// passed over.
var kept = [numSections]map[string]bool{
	sChar:      {"Style": true, "Pos": true, "Strikethru": true, "DoubleUnderline": true, "ColorTrans": true},
	sPara:      {"Bullet": true, "BulletStr": true},
	sField:     {"Value": true},
	sProperty:  {"Label": true, "Value": true, "Invisible": true},
	sHyperlink: {"Address": true, "SubAddress": true, "Default": true},
	sLayer:     {"Visible": true, "Name": true},
}

// noFormula is the formula Visio saves for a cell that holds nothing.
const noFormula = "No Formula"

// row is a row of a section. A row of a section indexed by number has
// ix; one of a section indexed by name has name.
type row struct {
	name  string
	ix    int32 // -1 if the row has no IX
	del   bool  // Del: the row is taken away from what the master gives
	cells map[string]string
}

func (r *row) cell(name string) (string, bool) {
	v, ok := r.cells[name]
	return v, ok
}

// section is a section of a sheet.
type section struct {
	del  bool // Del: the whole section is taken away
	rows []row
}

// byIX returns the row with the given index, or nil.
func (s *section) byIX(ix int32) *row {
	if s == nil {
		return nil
	}
	for i := range s.rows {
		if s.rows[i].ix == ix {
			return &s.rows[i]
		}
	}
	return nil
}

// byName returns the row with the given name, or nil.
func (s *section) byName(name string) *row {
	if s == nil {
		return nil
	}
	for i := range s.rows {
		if s.rows[i].name == name {
			return &s.rows[i]
		}
	}
	return nil
}

// tokKind is a kind of piece of a shape's text.
type tokKind uint8

const (
	tText  tokKind = iota // characters
	tChar                 // cp: the runs from here take the format of a Character row
	tPara                 // pp: the paragraphs from here take the format of a Paragraph row
	tField                // fld: a field, whose value is that of a Field row
)

// tok is a piece of a shape's text.
type tok struct {
	kind tokKind
	ix   int32
	text string // the characters; for a field, what the text holds of it
}

// shape is a shape sheet: a shape on a page or a master, or a style.
type shape struct {
	id          int64
	master      int64 // Master: the master the shape is an instance of; -1 if none
	masterShape int64 // MasterShape: the shape of that master it takes after; -1 if none
	textStyle   int64 // TextStyle: the style its text takes after; -1 if not said
	typ         string
	name        string
	del         bool // Del: a shape of the master the instance does not have

	cells    cells
	sections [numSections]*section
	text     []tok
	hasText  bool // the sheet has a Text element, so does not take the master's

	foreign, foreignType string // ForeignData: its relationship and kind (a picture, an object)

	children []*shape
}

func newShape() *shape { return &shape{id: -1, master: -1, masterShape: -1, textStyle: -1} }

// Bounds of what a sheet may hold. They bound the work and memory a
// hostile file can ask for; drawings of any ordinary size are far below.
const (
	maxShapes    = warn.MaxShapes     // shapes of a page or master, groups and all
	maxDepth     = warn.MaxShapeDepth // groups in groups
	maxRows      = warn.MaxShapeRows  // rows of a section of one sheet
	maxTokens    = warn.MaxShapeText  // pieces of one shape's text
	maxBackPages = warn.MaxBackPages  // background pages behind a page, in turn
	maxStyleHops = 16                 // styles a style may take after in turn
)

// reader reads shape sheets from a scanner.
type reader struct {
	s     *xmlscan.Scanner
	count int // shapes read so far from the part
	lost  *warn.Lost
	over  bool // a bound left shapes or rows out
}

// shapes reads a Shapes element, whose Start token is current, and
// returns its shapes.
func (r *reader) shapes(depth int) []*shape {
	s := r.s
	var out []*shape
	d := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= d {
		if s.Kind() != xmlscan.Start {
			continue
		}
		if !s.Is(ns.Visio, "Shape") {
			s.Skip()
			continue
		}
		if r.count >= maxShapes || depth >= maxDepth {
			r.over = true
			if depth >= maxDepth {
				r.lost.Add(warn.CapShapeDepth, 1)
			} else {
				r.lost.Add(warn.CapShapes, 1)
			}
			s.Skip()
			continue
		}
		r.count++
		out = append(out, r.shape(depth))
	}
	return out
}

// shape reads a Shape element, whose Start token is current.
func (r *reader) shape(depth int) *shape {
	s := r.s
	sh := newShape()
	sh.id = intAttr(s, "ID", -1)
	sh.master = intAttr(s, "Master", -1)
	sh.masterShape = intAttr(s, "MasterShape", -1)
	sh.textStyle = intAttr(s, "TextStyle", -1)
	if v, ok := s.Attr(xmlscan.NoSpace, "Type"); ok {
		sh.typ = string(v)
	}
	if v, ok := s.Attr(xmlscan.NoSpace, "Name"); ok && len(v) > 0 {
		sh.name = string(v)
	} else if v, ok := s.Attr(xmlscan.NoSpace, "NameU"); ok {
		sh.name = string(v)
	}
	if v, ok := s.Attr(xmlscan.NoSpace, "Del"); ok {
		sh.del = isTrue(v)
	}
	r.body(sh, depth)
	return sh
}

// body reads the content of a sheet element (Shape, StyleSheet,
// PageSheet) whose Start token is current: its cells, sections, text,
// foreign data and the shapes of a group.
func (r *reader) body(sh *shape, depth int) {
	s := r.s
	d := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= d {
		if s.Kind() != xmlscan.Start || s.Depth() != d+1 || s.Space() != ns.Visio {
			if s.Kind() == xmlscan.Start {
				s.Skip()
			}
			continue
		}
		switch string(s.Local()) {
		case "Cell":
			n, _ := s.Attr(xmlscan.NoSpace, "N")
			if id, ok := cellIDs[string(n)]; ok {
				v, _ := s.Attr(xmlscan.NoSpace, "V")
				sh.cells.put(id, string(v))
			}
			s.Skip()
		case "Section":
			n, _ := s.Attr(xmlscan.NoSpace, "N")
			id, ok := secIDs[string(n)]
			if !ok {
				s.Skip()
				continue
			}
			sec := sh.sections[id]
			if sec == nil {
				sec = &section{}
				sh.sections[id] = sec
			}
			if v, ok := s.Attr(xmlscan.NoSpace, "Del"); ok && isTrue(v) {
				sec.del = true
			}
			r.section(sec, id)
		case "Text":
			sh.hasText = true
			r.text(sh)
		case "ForeignData":
			if v, ok := s.Attr(xmlscan.NoSpace, "ForeignType"); ok {
				sh.foreignType = string(v)
			}
			fd := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= fd {
				if s.Kind() == xmlscan.Start && s.Is(ns.Visio, "Rel") {
					if id, ok := s.Attr(ns.Rel, "id"); ok && sh.foreign == "" {
						sh.foreign = string(id)
					}
				}
			}
		case "Shapes":
			sh.children = append(sh.children, r.shapes(depth+1)...)
		default:
			s.Skip()
		}
	}
}

// section reads the rows of a Section element whose Start token is
// current.
func (r *reader) section(sec *section, id secID) {
	s := r.s
	d := s.Depth()
	want := kept[id]
	for s.Next() != xmlscan.EOF && s.Depth() >= d {
		if s.Kind() != xmlscan.Start {
			continue
		}
		if !s.Is(ns.Visio, "Row") || s.Depth() != d+1 {
			s.Skip()
			continue
		}
		if len(sec.rows) >= maxRows {
			r.over = true
			r.lost.Add(warn.CapShapeRows, 1)
			s.Skip()
			continue
		}
		rw := row{ix: int32(intAttr(s, "IX", -1))}
		if v, ok := s.Attr(xmlscan.NoSpace, "N"); ok {
			rw.name = string(v)
		}
		if v, ok := s.Attr(xmlscan.NoSpace, "Del"); ok {
			rw.del = isTrue(v)
		}
		rd := s.Depth()
		for s.Next() != xmlscan.EOF && s.Depth() >= rd {
			if s.Kind() != xmlscan.Start {
				continue
			}
			if s.Is(ns.Visio, "Cell") {
				n, _ := s.Attr(xmlscan.NoSpace, "N")
				if want[string(n)] {
					v, _ := s.Attr(xmlscan.NoSpace, "V")
					if id == sProperty && string(n) == "Value" {
						// A shape data item with no value: Visio saves
						// "No Formula" as its formula, and a zero as the
						// value.
						if f, _ := s.Attr(xmlscan.NoSpace, "F"); string(f) == noFormula {
							v = nil
						}
					}
					if rw.cells == nil {
						rw.cells = map[string]string{}
					}
					rw.cells[string(n)] = string(v)
				}
			}
			s.Skip()
		}
		sec.rows = append(sec.rows, rw)
	}
}

// text reads a Text element whose Start token is current.
func (r *reader) text(sh *shape) {
	s := r.s
	d := s.Depth()
	add := func(t tok) {
		if len(sh.text) < maxTokens {
			sh.text = append(sh.text, t)
		} else {
			r.over = true
			r.lost.Add(warn.CapShapeText, 1)
		}
	}
	for s.Next() != xmlscan.EOF && s.Depth() >= d {
		switch s.Kind() {
		case xmlscan.Text:
			if s.Depth() == d {
				add(tok{kind: tText, text: string(s.Text())})
			}
		case xmlscan.Start:
			if s.Space() != ns.Visio || s.Depth() != d+1 {
				s.Skip()
				continue
			}
			ix := int32(intAttr(s, "IX", 0))
			switch string(s.Local()) {
			case "cp":
				add(tok{kind: tChar, ix: ix})
				s.Skip()
			case "pp":
				add(tok{kind: tPara, ix: ix})
				s.Skip()
			case "fld":
				var held []byte
				fd := s.Depth()
				for s.Next() != xmlscan.EOF && s.Depth() >= fd {
					if s.Kind() == xmlscan.Text {
						held = append(held, s.Text()...)
					}
				}
				add(tok{kind: tField, ix: ix, text: string(held)})
			default:
				s.Skip()
			}
		}
	}
}

// intAttr returns the value of an attribute that holds a whole number,
// or def if it does not.
func intAttr(s *xmlscan.Scanner, name string, def int64) int64 {
	v, ok := s.Attr(xmlscan.NoSpace, name)
	if !ok || len(v) == 0 || len(v) > 15 {
		return def
	}
	n, err := strconv.ParseInt(string(v), 10, 64)
	if err != nil {
		return def
	}
	return n
}

func isTrue(v []byte) bool { return string(v) == "1" || string(v) == "true" }
