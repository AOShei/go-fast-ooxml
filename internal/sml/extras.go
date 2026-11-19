package sml

import (
	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/dml"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/theme"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// What hangs on a sheet besides its cells: the tables it declares, the
// comments on its cells, and the shapes and pictures that float over it.

// declaredTable is a table the workbook declares: where it is and how
// many of its rows are headers.
type declaredTable struct {
	at         rect
	headerRows int
	totalRows  int
	name       string // the name Excel shows for it
	// Its style, and which of the style's elements it shows.
	style                                              string
	firstColumn, lastColumn, rowStripes, columnStripes bool
}

// declared reads the sheet's table parts.
func (sh *sheet) declared() []declaredTable {
	var out []declaredTable
	for _, target := range sh.tables {
		sh.load(target, func(s *xmlscan.Scanner) {
			for s.Next() != xmlscan.EOF {
				if s.Kind() != xmlscan.Start {
					continue
				}
				// Everything needed is on the root element.
				if !s.Is(ns.Sheet, "table") {
					return
				}
				ref, ok := s.Attr(xmlscan.NoSpace, "ref")
				if !ok {
					return
				}
				at, ok := parseRange(ref)
				if !ok {
					return
				}
				t := declaredTable{at: at, headerRows: 1}
				// The name the workbook shows; name is the one formulas
				// use, and is the same unless it was changed by hand.
				if v, ok := s.Attr(xmlscan.NoSpace, "displayName"); ok && len(v) > 0 {
					t.name = clean.Name(string(v))
				} else if v, ok := s.Attr(xmlscan.NoSpace, "name"); ok {
					t.name = clean.Name(string(v))
				}
				if n, ok := intAttr(s, "headerRowCount"); ok {
					t.headerRows = min(n, 1)
				}
				if n, ok := intAttr(s, "totalsRowCount"); ok && n > 0 {
					t.totalRows = min(n, 1)
				}
				// Its style is in a child element.
				depth := s.Depth()
				for s.Next() != xmlscan.EOF && s.Depth() >= depth {
					if s.Kind() != xmlscan.Start || s.Depth() != depth+1 || !s.Is(ns.Sheet, "tableStyleInfo") {
						continue
					}
					v, _ := s.Attr(xmlscan.NoSpace, "name")
					t.style = string(v)
					on := func(name string) bool { v, ok := s.Attr(xmlscan.NoSpace, name); return ok && isTrue(v) }
					t.firstColumn, t.lastColumn = on("showFirstColumn"), on("showLastColumn")
					t.rowStripes, t.columnStripes = on("showRowStripes"), on("showColumnStripes")
				}
				out = append(out, t)
				return
			}
		})
	}
	return out
}

// readPersons reads the people who wrote threaded comments, by id.
func readPersons(s *xmlscan.Scanner) map[string]string {
	persons := map[string]string{}
	for s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start || !s.Is(ns.Threaded, "person") || len(persons) >= maxAuthors {
			continue
		}
		id, ok1 := s.Attr(xmlscan.NoSpace, "id")
		name, ok2 := s.Attr(xmlscan.NoSpace, "displayName")
		if ok1 && ok2 && len(id) <= 64 {
			persons[string(id)] = clean.Name(string(name))
		}
	}
	return persons
}

const (
	maxAuthors  = 1 << 12
	maxComments = 1 << 18
)

// comments reads the comments on the sheet's cells as note blocks. A
// workbook has two kinds: notes, and threaded comments with replies. A
// threaded comment is also stored as a note that tells older programs
// they cannot show it; that stand-in is left out.
func (sh *sheet) comments() []placed {
	var out []placed
	threaded := map[int64]bool{}
	add := func(row, col int32, raw []byte, author, date string) {
		if len(out) >= maxComments {
			return
		}
		text, removed := cellText(sh.out[:0], raw)
		sh.out = text
		sh.removed += removed
		if len(text) == 0 {
			return
		}
		if !sh.room.Take(gofastcore.BlockCost + len(text)) {
			if sh.cut == "" {
				sh.cut = "the limit on the amount of text was reached"
			}
			return
		}
		b := gofastcore.Block{Kind: gofastcore.KindNote, Text: string(text), Attrs: map[string]string{gofastcore.AttrNoteType: gofastcore.NoteComment}}
		if author != "" {
			b.Attrs[gofastcore.AttrAuthor] = author
		}
		if date != "" {
			b.Attrs[gofastcore.AttrDate] = date
		}
		b.Loc.Range = cellName(row, col)
		out = append(out, placed{row: row, col: col, block: b})
	}
	for _, rel := range sh.list {
		if rel.External || !ns.IsRel(rel.Type, ns.RelThreadedComment) {
			continue
		}
		sh.load(rel.Target, func(s *xmlscan.Scanner) {
			for s.Next() != xmlscan.EOF {
				if s.Kind() != xmlscan.Start || !s.Is(ns.Threaded, "threadedComment") {
					continue
				}
				ref, _ := s.Attr(xmlscan.NoSpace, "ref")
				col, row, ok := parseRef(ref)
				author, date := "", ""
				if v, has := s.Attr(xmlscan.NoSpace, "personId"); has {
					author = sh.wb.persons[string(v)]
				}
				if v, has := s.Attr(xmlscan.NoSpace, "dT"); has {
					date = clean.Label(string(v))
				}
				raw := sh.raw[:0]
				depth := s.Depth()
				for s.Next() != xmlscan.EOF && s.Depth() >= depth {
					if s.Kind() == xmlscan.Start && s.Is(ns.Threaded, "text") {
						raw = s.AppendText(raw)
					}
				}
				sh.raw = raw
				if ok {
					threaded[int64(row)<<32|int64(col)] = true
					add(row, col, raw, author, date)
				}
			}
		})
	}
	for _, rel := range sh.list {
		if rel.External || !ns.IsRel(rel.Type, ns.RelComments) {
			continue
		}
		sh.load(rel.Target, func(s *xmlscan.Scanner) {
			var authors []string
			for s.Next() != xmlscan.EOF {
				if s.Kind() != xmlscan.Start || s.Depth() != 3 {
					continue
				}
				switch {
				case s.Is(ns.Sheet, "author"):
					sh.raw = s.AppendText(sh.raw[:0])
					if len(authors) < maxAuthors {
						authors = append(authors, clean.Name(string(sh.raw)))
					}
				case s.Is(ns.Sheet, "comment"):
					ref, _ := s.Attr(xmlscan.NoSpace, "ref")
					col, row, ok := parseRef(ref)
					author := ""
					if i, has := intAttr(s, "authorId"); has && i < len(authors) {
						author = authors[i]
					}
					raw := sh.raw[:0]
					depth := s.Depth()
					for s.Next() != xmlscan.EOF && s.Depth() >= depth {
						if s.Kind() == xmlscan.Start && s.Is(ns.Sheet, "text") {
							raw = readRichText(s, raw)
						}
					}
					sh.raw = raw
					if ok && !threaded[int64(row)<<32|int64(col)] {
						add(row, col, raw, author, "")
					}
				default:
					s.Skip()
				}
			}
		})
	}
	return out
}

// drawing reads a drawing part: the text of its shapes as paragraphs, and
// its pictures. Each block sits at the cell its shape is anchored to. It
// reports whether there are pictures that were not asked for.
func (sh *sheet) drawing(target string, out []placed) ([]placed, bool) {
	rels, err := sh.wb.pkg.Rels(target)
	sh.noteErr(opc.RelsName(target), err)
	byID := make(map[string]opc.Rel, len(rels))
	for _, rel := range rels {
		byID[rel.ID] = rel
	}
	sawImage := false
	pal := dml.Palette{Theme: sh.wb.theme, Map: dml.DefaultMap}
	// Where a link on the text of a shape leads: an address outside the
	// file, cleaned when the relationships were read.
	link := func(id []byte) string {
		if rel, ok := byID[string(id)]; ok && rel.External {
			return rel.Target
		}
		return ""
	}
	sh.load(target, func(s *xmlscan.Scanner) {
		var row, col int32 // the cell the current anchor starts at
		var alt, name string
		var unseen bool          // the current shape is marked hidden
		var own, styled dml.Fill // the current shape's fill, and the one its style gives it
		var ink dml.Color        // the colour its style gives its text
		props := 0               // depth of the shape properties being read
		choice := -1             // blocks there were when the current choice of alternatives began
		for s.Next() != xmlscan.EOF {
			if s.Kind() != xmlscan.Start {
				continue
			}
			if props > 0 && s.Depth() <= props {
				props = 0
			}
			switch {
			case s.Is(ns.MC, "Choice"):
				choice = len(out)
			case s.Is(ns.MC, "Fallback"):
				// The other form of what the choice before it holds: read
				// only if nothing was read there.
				if choice >= 0 && len(out) > choice {
					s.Skip()
				}
				choice = -1
			case s.Is(ns.SheetDraw, "from"):
				depth := s.Depth()
				for s.Next() != xmlscan.EOF && s.Depth() >= depth {
					if s.Kind() != xmlscan.Start {
						continue
					}
					switch {
					case s.Is(ns.SheetDraw, "col"):
						sh.raw = s.AppendText(sh.raw[:0])
						if n, ok := atoi(sh.raw); ok {
							col = int32(min(n, maxCols-1))
						}
					case s.Is(ns.SheetDraw, "row"):
						sh.raw = s.AppendText(sh.raw[:0])
						if n, ok := atoi(sh.raw); ok {
							row = int32(min(n, maxRows-1))
						}
					}
				}
			case s.Is(ns.SheetDraw, "sp"), s.Is(ns.SheetDraw, "pic"), s.Is(ns.SheetDraw, "graphicFrame"):
				alt, name, unseen, own, styled, ink, props = "", "", false, dml.Fill{}, dml.Fill{}, dml.Color{}, 0
			case s.Is(ns.SheetDraw, "cNvPr"):
				if v, ok := s.Attr(xmlscan.NoSpace, "descr"); ok && len(v) > 0 {
					alt, _ = clean.String(v, clean.Prose)
				}
				if v, ok := s.Attr(xmlscan.NoSpace, "hidden"); ok {
					unseen = isTrue(v)
				}
				if v, ok := s.Attr(xmlscan.NoSpace, "name"); ok {
					name = clean.Name(string(v))
				}
			case s.Is(ns.SheetDraw, "spPr"):
				props = s.Depth()
			case props > 0 && s.Depth() == props+1 && s.Space() == ns.Draw:
				if f, ok := dml.ReadFill(s); ok {
					own = f
				} else if s.Is(ns.Draw, "ln") {
					s.Skip() // the outline has a fill of its own
				}
			case s.Is(ns.Draw, "fillRef"):
				// The fill the shape's style gives it, unless it has its own.
				idx, _ := intAttr(s, "idx")
				styled = pal.StyleFill(idx, dml.ReadColor(s))
			case s.Is(ns.Draw, "fontRef"):
				ink = dml.ReadColor(s)
			case s.Is(ns.SheetDraw, "txBody"):
				fill := own
				if fill.Paint == dml.PaintUnset {
					fill = styled
				}
				out = sh.textBody(s, out, &pal, row, col, fill, ink, unseen, link)
			case s.Is(ns.Draw, "blip"):
				id, ok := s.Attr(ns.Rel, "embed")
				if !ok {
					id, ok = s.Attr(ns.Rel, "link")
				}
				rel, found := byID[string(id)]
				if !ok || !found {
					continue
				}
				if sh.wb.opts.Images == gofastcore.ImagesNone {
					sawImage = true
					continue
				}
				// What a picture says counts as text does: its
				// alternative text and, if the picture is not in the
				// file, its address, which like a link's may be given
				// any number of times.
				said := gofastcore.BlockCost + len(alt)
				if rel.External {
					said += len(rel.Target)
				}
				if !sh.room.Take(said) {
					s.Abort(warn.ErrText)
					continue
				}
				var b gofastcore.Block
				if rel.External {
					// A picture that is not in the file: named, not fetched.
					b = gofastcore.Block{Kind: gofastcore.KindImage, Text: alt, Image: &gofastcore.Image{}, Attrs: map[string]string{"href": rel.Target}}
				} else {
					var err error
					b, err = sh.wb.image(rel.Target, alt)
					sh.noteErr(rel.Target, err)
				}
				if unseen {
					b.Hidden = gofastcore.HiddenInvisible
				}
				b.Loc.Range = cellName(row, col)
				out = append(out, placed{row: row, col: col, block: b})
			case s.Is(ns.Chart, "chart"), s.Is(ns.ChartEx, "chart"):
				id, _ := s.Attr(ns.Rel, "id")
				rel, found := byID[string(id)]
				if !found || rel.External || !ns.IsRel(rel.Type, ns.RelChart, ns.RelChartEx) || !sh.wb.pkg.Has(rel.Target) {
					sh.damaged = append(sh.damaged, "the chart "+clean.Label(string(id))+" of "+target)
					continue
				}
				var cut bool
				out, cut = sh.chart(rel.Target, out, row, col, name, unseen)
				if cut {
					s.Abort(warn.ErrText)
				}
			case s.Is(ns.Diagram, "relIds"):
				id, _ := s.Attr(ns.Rel, "dm")
				rel, found := byID[string(id)]
				if !found || rel.External || !ns.IsRel(rel.Type, ns.RelDiagram) || !sh.wb.pkg.Has(rel.Target) {
					sh.damaged = append(sh.damaged, "the diagram "+clean.Label(string(id))+" of "+target)
					continue
				}
				var cut bool
				out, cut = sh.diagram(rel.Target, byID, &pal, out, row, col, unseen)
				if cut {
					s.Abort(warn.ErrText)
				}
			}
		}
	})
	return out, sawImage
}

// textBody reads the text of a shape (the element whose Start token is
// current): one block for each of its paragraphs that has text. Text too
// small to read, or in the colour of what is behind it, is marked; so is
// all of it if the shape is hidden.
func (sh *sheet) textBody(s *xmlscan.Scanner, out []placed, pal *dml.Palette, row, col int32, fill dml.Fill, ink dml.Color, unseen bool, link func([]byte) string) []placed {
	t := &sh.shapes
	t.Palette, t.Chain, t.Base = pal, nil, dml.RunProps{}
	if ink.IsSet() {
		t.Base.Ink = dml.Fill{Paint: dml.PaintSolid, Color: ink}
	}
	// What the text is seen against: the shape's fill if it is one plain
	// colour, the sheet if it has none.
	switch fill.Paint {
	case dml.PaintSolid:
		t.Behind, t.Lit = pal.Opaque(fill, nil)
	case dml.PaintOther:
		t.Lit = false
	default:
		t.Behind, t.Lit = theme.White, true
	}
	t.Later, t.Styles, t.Link, t.SlideNumber = false, sh.wb.opts.Spans, link, ""
	t.Read(s)
	sh.removed += t.Removed
	sh.unmapped += t.Unmapped
	t.Removed, t.Unmapped = 0, 0
	for i := range t.Paras {
		p := &t.Paras[i]
		b := gofastcore.Block{Kind: gofastcore.KindParagraph, Text: string(t.Buf[p.Start:p.End]), Spans: p.Spans, Hidden: p.Hidden}
		if p.Listed {
			b.Kind, b.Level, b.Ordered, b.Marker = gofastcore.KindListItem, uint8(p.Level+1), p.Ordered, p.Marker
		}
		if unseen {
			// All of it, whatever is true of parts.
			b.Hidden |= gofastcore.HiddenInvisible
		}
		b.Loc.Range = cellName(row, col)
		out = append(out, placed{row: row, col: col, block: b})
	}
	return out
}
