// Package diagram reads a SmartArt diagram: the text of its data model, in
// the order and nesting the model declares, and, where the file carries
// the drawing Office made of it, where each piece of text is drawn and
// what the drawing declares about its display.
//
// The data model is what the diagram says: points (the nodes that hold
// text, and the document that holds them) joined by connections that say
// which node is a child of which, and in what order. The layout turns the
// model into shapes when the diagram is shown; Office keeps the result in
// a drawing part beside the model, and other programs may not. A layout
// can leave a node out: such a node's text is in the file and on no
// slide. Nothing is said of it: the file declares nothing about its
// display. A shape of the drawing marked hidden, and text drawn with no
// ink, are declared, and kept.
package diagram

import (
	"bytes"
	"sort"
	"strconv"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-core/clean"
	"github.com/AOShei/go-fast-core/xmlscan"
	"github.com/AOShei/go-fast-ooxml/internal/dml"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
)

// Limits on what a diagram may make the reader hold.
const (
	maxPoints = warn.MaxDiagramNodes
	maxCxns   = warn.MaxDiagramLinks
	maxDepth  = warn.MaxDiagramDepth
)

// Item is a paragraph of a node's text.
type Item struct {
	Text  string
	Level int // the node's depth in the model, from 1, plus the paragraph's own level
	// Display is what the file declares about the display of the text.
	Display gofastcore.Display
	// Box is the drawn shape that shows the node, in the diagram's own
	// space (points, from the corner of the frame that holds it), if the
	// drawing has one.
	Box dml.Xfrm
}

// Diagram is what a diagram's parts say.
type Diagram struct {
	Items []Item
	// Drawn says the file has the drawing of the diagram, which places
	// the nodes and says what is declared about their display.
	Drawn   bool
	Removed int // control characters taken out
	// Lost counts what the reader's fixed bounds left out.
	Lost warn.Lost
}

type point struct {
	id, typ string
	paras   []para
}

type para struct {
	text  string
	level int
}

type cxn struct {
	typ, src, dest string
	ord            int
	// The transitions of a parent-child connection: the connector from
	// the parent, and the one to the next sibling, which can hold text.
	parTrans, sibTrans string
}

var scanOptions = &xmlscan.Options{Spaces: ns.Spaces}

// Read reads the diagram whose data model is the part data. rels are the
// relationships of the part that shows the diagram, by id: the drawing is
// named there. pal resolves the drawing's colours, for text drawn with no
// ink; nil leaves them unresolved.
func Read(pkg *opc.Package, data string, rels map[string]opc.Rel, pal *dml.Palette) (*Diagram, error) {
	d := &Diagram{}
	var s xmlscan.Scanner
	c, err := pkg.Scan(data, &s, scanOptions)
	if err != nil {
		return nil, err
	}
	points, cxns, drawingID := d.model(&s)
	c.Close()
	if err := s.Err(); err != nil && len(points) == 0 {
		return d, err
	}
	readErr := s.Err()

	// The drawing: the one the model names, if the part has it.
	var drawing string
	if rel, ok := rels[drawingID]; ok && !rel.External && ns.IsRel(rel.Type, ns.RelDiagramDrawing) {
		drawing = rel.Target
	}
	var shapes map[string]drawn
	if drawing != "" && pkg.Has(drawing) {
		shapes, err = d.drawing(pkg, drawing, pal)
		if err != nil && readErr == nil {
			readErr = err
		}
		// A drawing is used only if it is of this model: one that is
		// empty, or draws none of the model's presentation points, is a
		// drawing that was not kept up to date, and says nothing.
		matched := false
		for _, x := range cxns {
			if x.typ == "presOf" {
				if _, ok := shapes[x.dest]; ok {
					matched = true
					break
				}
			}
		}
		if err == nil && matched {
			d.Drawn = true
		} else {
			shapes = nil
		}
	}
	d.arrange(points, cxns, shapes)
	return d, readErr
}

// model reads the data model, whose part s scans.
func (d *Diagram) model(s *xmlscan.Scanner) (points []point, cxns []cxn, drawingID string) {
	var buf []byte
	for s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Is(ns.Diagram, "pt"):
			if len(points) >= maxPoints {
				d.Lost.Add(warn.CapDiagramNodes, 1)
				s.Skip()
				continue
			}
			p := point{typ: "node"}
			if v, ok := s.Attr(xmlscan.NoSpace, "modelId"); ok {
				p.id = string(v)
			}
			if v, ok := s.Attr(xmlscan.NoSpace, "type"); ok {
				p.typ = string(v)
			}
			depth := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= depth {
				if s.Kind() != xmlscan.Start {
					continue
				}
				if !s.Is(ns.Diagram, "t") {
					if s.Is(ns.Diagram, "prSet") || s.Is(ns.Diagram, "spPr") || s.Is(ns.Diagram, "extLst") {
						s.Skip()
					}
					continue
				}
				// The text body: its paragraphs.
				td := s.Depth()
				for s.Next() != xmlscan.EOF && s.Depth() >= td {
					if s.Kind() != xmlscan.Start || !s.Is(ns.Draw, "p") {
						continue
					}
					var pa para
					buf = buf[:0]
					pd := s.Depth()
					for s.Next() != xmlscan.EOF && s.Depth() >= pd {
						if s.Kind() != xmlscan.Start {
							continue
						}
						switch {
						case s.Is(ns.Draw, "pPr"):
							if v, ok := s.Attr(xmlscan.NoSpace, "lvl"); ok {
								if n, err := strconv.Atoi(string(v)); err == nil && n > 0 && n < 9 {
									pa.level = n
								}
							}
							s.Skip()
						case s.Is(ns.Draw, "t"):
							buf = s.AppendText(buf)
						case s.Is(ns.Draw, "br"):
							buf = append(buf, '\n')
						case s.Is(ns.Draw, "rPr"), s.Is(ns.Draw, "endParaRPr"):
							s.Skip()
						}
					}
					t, n := clean.String(buf, clean.Prose)
					d.Removed += n
					if t != "" {
						pa.text = t
						p.paras = append(p.paras, pa)
					}
				}
			}
			points = append(points, p)
		case s.Is(ns.Diagram, "cxn"):
			if len(cxns) >= maxCxns {
				d.Lost.Add(warn.CapDiagramLinks, 1)
				continue
			}
			x := cxn{typ: "parOf"}
			if v, ok := s.Attr(xmlscan.NoSpace, "type"); ok {
				x.typ = string(v)
			}
			if v, ok := s.Attr(xmlscan.NoSpace, "srcId"); ok {
				x.src = string(v)
			}
			if v, ok := s.Attr(xmlscan.NoSpace, "destId"); ok {
				x.dest = string(v)
			}
			if v, ok := s.Attr(xmlscan.NoSpace, "srcOrd"); ok {
				x.ord, _ = strconv.Atoi(string(bytes.TrimSpace(v)))
			}
			if v, ok := s.Attr(xmlscan.NoSpace, "parTransId"); ok {
				x.parTrans = string(v)
			}
			if v, ok := s.Attr(xmlscan.NoSpace, "sibTransId"); ok {
				x.sibTrans = string(v)
			}
			cxns = append(cxns, x)
		case s.Space() == ns.DiagramDraw && string(s.Local()) == "dataModelExt":
			if v, ok := s.Attr(xmlscan.NoSpace, "relId"); ok {
				drawingID = string(v)
			}
		case s.Is(ns.Diagram, "bg"), s.Is(ns.Diagram, "whole"):
			s.Skip()
		}
	}
	return points, cxns, drawingID
}

// drawn is a shape of the drawing: where it is, and what the file
// declares about the display of the text it shows, paragraph by paragraph.
type drawn struct {
	box    dml.Xfrm
	hidden []gofastcore.Display
	text   bool // it shows text
}

// drawing reads the drawing of the diagram: its shapes by the model id of
// the presentation point each draws.
func (d *Diagram) drawing(pkg *opc.Package, name string, pal *dml.Palette) (map[string]drawn, error) {
	var s xmlscan.Scanner
	c, err := pkg.Scan(name, &s, scanOptions)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	out := map[string]drawn{}
	var t dml.Text
	for s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start || !(s.Space() == ns.DiagramDraw && string(s.Local()) == "sp") {
			continue
		}
		// Copied now: the scanner's buffer is reused as the shape is read.
		v, _ := s.Attr(xmlscan.NoSpace, "modelId")
		id := string(v)
		var sh drawn
		var ink dml.Color
		unseen := false
		props := 0
		depth := s.Depth()
		for s.Next() != xmlscan.EOF && s.Depth() >= depth {
			if s.Kind() != xmlscan.Start {
				continue
			}
			if props > 0 && s.Depth() <= props {
				props = 0
			}
			switch {
			case s.Space() == ns.DiagramDraw && string(s.Local()) == "cNvPr":
				if v, ok := s.Attr(xmlscan.NoSpace, "hidden"); ok && (string(v) == "1" || string(v) == "true") {
					unseen = true
				}
			case s.Space() == ns.DiagramDraw && string(s.Local()) == "spPr":
				props = s.Depth()
			case props > 0 && s.Is(ns.Draw, "xfrm") && s.Depth() == props+1:
				sh.box = dml.ReadXfrm(&s)
			case s.Is(ns.Draw, "fontRef"):
				ink = dml.ReadColor(&s)
			case s.Space() == ns.DiagramDraw && string(s.Local()) == "txBody":
				sh.text = true
				if pal == nil {
					s.Skip()
					continue
				}
				t.Palette, t.Chain, t.Base = pal, nil, dml.RunProps{}
				if ink.IsSet() {
					t.Base.Ink = dml.Fill{Paint: dml.PaintSolid, Color: ink}
				}
				t.Styles, t.Link, t.SlideNumber = false, nil, ""
				t.Read(&s)
				t.Removed, t.Unmapped = 0, 0
				for _, p := range t.Paras {
					sh.hidden = append(sh.hidden, p.Display)
				}
			}
		}
		if unseen {
			for i := range sh.hidden {
				sh.hidden[i] |= gofastcore.DisplayHidden
			}
			if len(sh.hidden) == 0 {
				sh.hidden = []gofastcore.Display{gofastcore.DisplayHidden}
			}
		}
		if id != "" && len(out) < maxPoints {
			out[id] = sh
		}
	}
	return out, s.Err()
}

func attr(s *xmlscan.Scanner, name string) []byte {
	v, _ := s.Attr(xmlscan.NoSpace, name)
	return v
}

// arrange walks the model from the document down, children in their
// order, and gives each node's paragraphs as items, with any text on the
// connectors between them.
func (d *Diagram) arrange(points []point, cxns []cxn, shapes map[string]drawn) {
	byID := make(map[string]*point, len(points))
	var root *point
	for i := range points {
		p := &points[i]
		if p.id != "" {
			byID[p.id] = p
		}
		if p.typ == "doc" && root == nil {
			root = p
		}
	}
	children := map[string][]cxn{}
	shows := map[string][]string{} // a node's presentation points
	hasParent := map[string]bool{}
	for _, x := range cxns {
		switch x.typ {
		case "parOf":
			children[x.src] = append(children[x.src], x)
			hasParent[x.dest] = true
		case "presOf":
			shows[x.src] = append(shows[x.src], x.dest)
		}
	}
	for k := range children {
		list := children[k]
		sort.SliceStable(list, func(i, j int) bool { return list[i].ord < list[j].ord })
	}
	seen := map[string]bool{}
	var walk func(id string, level int)
	walk = func(id string, level int) {
		if seen[id] {
			return
		}
		if level > maxDepth {
			d.Lost.Add(warn.CapDiagramDepth, 1)
			return
		}
		seen[id] = true
		p := byID[id]
		if p != nil && level > 0 && (p.typ == "node" || p.typ == "asst") {
			d.node(p, level, shapes, shows[id])
		}
		for _, x := range children[id] {
			// Text on the connectors: the one from the parent before the
			// child, the one to the next sibling after the child and what
			// is under it.
			if t := byID[x.parTrans]; t != nil && t.typ == "parTrans" && !seen[t.id] {
				seen[t.id] = true
				d.node(t, level+1, shapes, shows[t.id])
			}
			walk(x.dest, level+1)
			if t := byID[x.sibTrans]; t != nil && t.typ == "sibTrans" && !seen[t.id] {
				seen[t.id] = true
				d.node(t, level+1, shapes, shows[t.id])
			}
		}
	}
	if root != nil {
		walk(root.id, 0)
	}
	// Nodes the walk did not reach, whose parent is missing, in the
	// order of the file.
	for i := range points {
		p := &points[i]
		if !seen[p.id] && !hasParent[p.id] && (p.typ == "node" || p.typ == "asst") {
			walk(p.id, 1)
		}
	}
}

// node gives a node's paragraphs as items, placed by the shapes that
// draw it.
func (d *Diagram) node(p *point, level int, shapes map[string]drawn, pres []string) {
	if len(p.paras) == 0 {
		return
	}
	var shown *drawn
	if shapes != nil {
		for _, id := range pres {
			if sh, ok := shapes[id]; ok && sh.text {
				shown = &sh
				break
			}
		}
	}
	for i, pa := range p.paras {
		it := Item{Text: pa.text, Level: level + pa.level}
		switch {
		case shapes == nil, shown == nil:
			// No drawing, or none of its shapes shows the node: nothing
			// is declared about it.
		default:
			it.Box = shown.box
			switch {
			case len(shown.hidden) == len(p.paras):
				it.Display = shown.hidden[i]
			case len(shown.hidden) > 0:
				// The drawing breaks the text otherwise: what holds of
				// all of it.
				all := shown.hidden[0]
				for _, h := range shown.hidden[1:] {
					all &= h
				}
				it.Display = all
			}
		}
		d.Items = append(d.Items, it)
	}
}

// Blocks returns the items as list items, taking each from budget. cut
// reports that the budget ran out.
func (d *Diagram) Blocks(budget *gofastcore.TextBudget) (blocks []gofastcore.Block, taken int64, cut bool) {
	for _, it := range d.Items {
		n := gofastcore.BlockCost + len(it.Text)
		if !budget.Take(n) {
			return blocks, taken, true
		}
		taken += int64(n)
		blocks = append(blocks, gofastcore.Block{Kind: gofastcore.KindListItem, Level: uint8(it.Level), Text: it.Text, Display: it.Display})
	}
	return blocks, taken, false
}
