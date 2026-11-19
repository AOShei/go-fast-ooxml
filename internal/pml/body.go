package pml

import (
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/dml"
	"github.com/AOShei/go-fast-ooxml/internal/hidden"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// textInk is the colour of text that nothing gives a colour: the first
// text colour of the colour map.
var textInk, _ = dml.Scheme("tx1")

// bbox returns a shape's box as the model states it.
func bbox(sh *props) gofastcore.Rect {
	if !sh.boxed {
		return gofastcore.Rect{}
	}
	return gofastcore.Rect{X0: round(sh.box.X0), Y0: round(sh.box.Y0), X1: round(sh.box.X1), Y1: round(sh.box.Y1)}
}

// body reads the text of a shape (the txBody whose Start token is
// current) into blocks. It reports whether the text is the slide's
// title.
func (pg *page) body(sh *props) (title bool) {
	s, t := &pg.s, &pg.text
	if pg.role == roleNotes && !(sh.ph.has && (sh.ph.typ == phBody || sh.ph.typ == phHeader || sh.ph.typ == phFooter)) {
		// On a notes page the notes are read, and the page's own header
		// and footer: the rest is the picture of the slide, the date and
		// the number.
		s.Skip()
		return false
	}
	if (pg.role == roleNotesMaster || pg.role == roleHandout) && sh.ph.has && sh.ph.typ != phHeader && sh.ph.typ != phFooter {
		// Of a notes master or handout, the header and footer and its own
		// text; not the frames the pages fill in, nor the date and number.
		s.Skip()
		return false
	}

	// What the text inherits, nearest first: the colour its shape's
	// style gives text, the placeholders it takes after, the master's
	// style for its kind of text, the presentation's defaults.
	n := 0
	if sh.font.IsSet() {
		for i := range pg.fonts {
			pg.fonts[i].Run.Ink = dml.Fill{Paint: dml.PaintSolid, Color: sh.font}
		}
		pg.chain[n] = &pg.fonts
		n++
	}
	for in := sh.in; in != nil && n < 3; in = in.up {
		if in.list != nil {
			pg.chain[n] = in.list
			n++
		}
	}
	if pg.styles != nil {
		style := styleOther
		if sh.ph.has {
			style = sh.ph.typ.style()
		}
		if ls := pg.styles[style]; ls != nil {
			pg.chain[n] = ls
			n++
		}
	}
	if pg.p.defaults != nil {
		pg.chain[n] = pg.p.defaults
		n++
	}
	t.Palette, t.Chain = &pg.pal, pg.chain[:n]
	t.Base = dml.RunProps{}
	if !pg.role.paper() {
		t.Base.Ink = dml.Fill{Paint: dml.PaintSolid, Color: textInk}
	}
	t.Behind, t.Lit, t.Later = sh.behind, sh.lit, false
	t.Styles, t.Link, t.SlideNumber = pg.p.opts.Spans, pg.link, pg.number
	t.Cost = gofastcore.BlockCost
	t.Read(s)
	pg.programs += t.Programs
	t.Programs = 0

	if len(t.Paras) == 0 || (sh.ph.has && (pg.role == roleLayout || pg.role == roleMaster)) {
		return false
	}
	reasons := sh.reasons
	if t.Clip && sh.boxed && sh.upright && sh.box.Y1-sh.box.Y0-t.Inset < t.Paras[0].Size {
		// What does not fit is cut off, and not one line fits.
		reasons |= gofastcore.HiddenClipped
	}
	kind, level, joined := gofastcore.KindParagraph, uint8(0), false
	if sh.ph.has {
		kind, level, joined = kindOf(sh.ph.typ)
	}
	switch pg.role {
	case roleNotes:
		kind, level, joined = gofastcore.KindNote, 0, false
		switch {
		case sh.ph.typ == phHeader:
			kind, pg.ownHF[0] = gofastcore.KindHeader, true
		case sh.ph.typ == phFooter:
			kind, pg.ownHF[1] = gofastcore.KindFooter, true
		}
	case roleNotesMaster, roleHandout:
		// The header, the footer, and text of the page's own: at the top
		// of the printed page or at the bottom.
		kind, level, joined = gofastcore.KindFooter, 0, false
		switch {
		case sh.ph.has && sh.ph.typ == phHeader:
			kind = gofastcore.KindHeader
		case sh.ph.has:
		case sh.boxed && pg.p.paperHeight > 0 && (sh.box.Y0+sh.box.Y1)/2 < pg.p.paperHeight/2:
			kind = gofastcore.KindHeader
		}
	case roleLayout, roleMaster:
		// Text of the layout or master itself: it is on every slide that
		// uses it, at the top or at the bottom.
		kind, level, joined = gofastcore.KindFooter, 0, false
		if h := pg.p.height; sh.boxed && h > 0 && (sh.box.Y0+sh.box.Y1)/2 < h/2 {
			kind = gofastcore.KindHeader
		}
	}
	loc := gofastcore.Location{}
	if !pg.role.paper() {
		loc.BBox = bbox(sh) // a printed page's places are not the slide's
	}

	if joined {
		b := gofastcore.Block{Kind: kind, Level: level, Loc: loc}
		if len(t.Paras) == 1 {
			p := &t.Paras[0]
			b.Text, b.Spans, b.Hidden = string(t.Buf[p.Start:p.End]), p.Spans, p.Hidden
		} else {
			// A title of several paragraphs is one heading.
			var text []byte
			var spans []gofastcore.Span
			for i := range t.Paras {
				p := &t.Paras[i]
				if i > 0 {
					text = append(text, '\n')
				}
				at := len(text)
				text = append(text, t.Buf[p.Start:p.End]...)
				whole := p.Hidden
				for _, sp := range p.Spans {
					whole &^= sp.Hidden
					sp.Start, sp.End = sp.Start+at, sp.End+at
					spans = append(spans, sp)
				}
				if whole != 0 {
					spans = append(spans, gofastcore.Span{Start: at, End: len(text), Hidden: whole})
				}
			}
			b.Text = string(text)
			b.Spans, b.Hidden = hidden.Settle(spans, 0, len(text), text)
		}
		b.Hidden |= reasons
		pg.blocks = append(pg.blocks, b)
		if level == 1 && pg.role == roleSlide && pg.title == "" {
			pg.title = strings.Join(strings.Fields(b.Text), " ")
			return true
		}
		return false
	}
	for i := range t.Paras {
		p := &t.Paras[i]
		b := gofastcore.Block{Kind: kind, Text: string(t.Buf[p.Start:p.End]), Spans: p.Spans, Hidden: p.Hidden | reasons, Loc: loc}
		switch {
		case kind == gofastcore.KindNote:
			b.Attrs = map[string]string{gofastcore.AttrNoteType: gofastcore.NoteSpeaker}
		case kind == gofastcore.KindParagraph && p.Listed:
			b.Kind, b.Level, b.Ordered, b.Marker = gofastcore.KindListItem, uint8(p.Level+1), p.Ordered, p.Marker
		}
		pg.blocks = append(pg.blocks, b)
	}
	return false
}

// pic reads a picture (p:pic).
// said counts what a picture says, its alternative text or its address,
// against the text the document may hold, and reports whether there was
// room; if not, the reading of the page ends.
func (pg *page) said(n int) bool {
	if !pg.text.Budget.Take(n) {
		pg.s.Abort(warn.ErrText)
		return false
	}
	pg.text.Taken += int64(n)
	return true
}

func (pg *page) pic(g *group) {
	s := &pg.s
	var sh props
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Is(ns.Pres, "nvPicPr"):
			pg.nonVisual(&sh, true)
		case s.Is(ns.Pres, "spPr"):
			pg.shapeProps(&sh)
		case s.Is(ns.Pres, "blipFill"):
			// Read as its content comes.
		case s.Is(ns.Draw, "blip"):
			id, ok := s.Attr(ns.Rel, "embed")
			if !ok {
				id, ok = s.Attr(ns.Rel, "link")
			}
			if ok && !sh.hasBlip {
				sh.blip, sh.hasBlip = string(id), true
			}
		case s.Is(ns.Draw, "alphaModFix"):
			if n, ok := intAttr(s, "amt"); ok && n < 100000 {
				sh.faded = true
			}
			s.Skip()
		case s.Is(ns.Draw, "alphaMod"), s.Is(ns.Draw, "alphaBiLevel"), s.Is(ns.Draw, "clrChange"):
			sh.faded = true // other ways of making part of a picture transparent
			s.Skip()
		default:
			s.Skip()
		}
	}
	sh.fill = dml.Fill{Paint: dml.PaintOther} // it is what fills its box
	pg.place(&sh, g)
	if pg.role.paper() {
		return
	}
	if sh.media {
		pg.count("audio or video clip")
	}
	first := len(pg.blocks)
	picture := ""
	// A picture on a slide, or one a layout or master puts on every slide
	// that shows it; not the empty frame of a placeholder there.
	shown := pg.role == roleSlide || !sh.ph.has
	if rel, found := pg.rels[sh.blip]; sh.hasBlip && found && shown {
		switch {
		case pg.p.opts.Images == gofastcore.ImagesNone:
			pg.sawImage = true
		case !pg.said(gofastcore.BlockCost + len(sh.alt)):
			return
		case rel.External:
			// A picture that is not in the file: named, not fetched. Its
			// address counts as a link's does.
			if !pg.said(len(rel.Target)) {
				return
			}
			pg.blocks = append(pg.blocks, gofastcore.Block{Kind: gofastcore.KindImage, Text: sh.alt, Image: &gofastcore.Image{},
				Attrs: map[string]string{"href": rel.Target}})
		default:
			found, err := pg.p.image(rel.Target)
			pg.fail(rel.Target, err, false)
			img := *found
			pg.blocks = append(pg.blocks, gofastcore.Block{Kind: gofastcore.KindImage, Text: sh.alt, Image: &img})
		}
		if len(pg.blocks) > first {
			b := &pg.blocks[first]
			b.Hidden, b.Loc.BBox = sh.reasons, bbox(&sh)
		}
		if !rel.External && sh.rect && sh.upright && !sh.faded && sh.reasons&gofastcore.HiddenInvisible == 0 {
			picture = rel.Target
		}
	}
	pg.paints(&sh, false)
	if pg.role == roleSlide {
		pg.add(&sh, g, first, false, false, picture)
	}
}

// frame reads a graphic frame (p:graphicFrame): a table, a chart, a
// diagram or an embedded object.
func (pg *page) frame(g *group) {
	s := &pg.s
	var sh props
	choice := -1
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Is(ns.Pres, "nvGraphicFramePr"):
			pg.nonVisual(&sh, false)
		case s.Is(ns.Pres, "xfrm"):
			sh.xfrm = dml.ReadXfrm(s)
		case s.Is(ns.Draw, "tbl"):
			pg.place(&sh, g)
			if pg.role != roleSlide {
				s.Skip()
				continue
			}
			at := len(pg.blocks)
			pg.table(&sh)
			if len(pg.blocks) > at {
				pg.add(&sh, g, at, false, false, "")
			}
		case s.Is(ns.Chart, "chart"), s.Is(ns.ChartEx, "chart"):
			pg.chart(&sh, g)
		case s.Is(ns.Diagram, "relIds"):
			if pg.role == roleSlide {
				pg.count("diagram")
			}
			s.Skip()
		case s.Is(ns.Pres, "pic"):
			pg.pic(g) // what an embedded object looks like on the slide
		case s.Is(ns.MC, "Choice"):
			choice = pg.yield()
		case s.Is(ns.MC, "Fallback"):
			if choice >= 0 && pg.yield() > choice {
				s.Skip()
			}
			choice = -1
		}
		// Anything else holds one of these.
	}
	pg.place(&sh, g)
	if pg.role.paper() {
		return
	}
	pg.paints(&sh, false)
}
