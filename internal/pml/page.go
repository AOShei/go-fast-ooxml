package pml

import (
	"context"
	"errors"
	"slices"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/dml"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/theme"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// role is what kind of part a page is.
type role uint8

const (
	roleSlide role = iota
	roleLayout
	roleMaster
	roleNotes
	roleNotesMaster // what every notes page has: its header and footer
	roleHandout     // the handout master: what every printed handout has
)

// paper reports whether a page of this role is printed, not shown: a
// notes page or a handout. It is white, and has a size of its own.
func (r role) paper() bool { return r == roleNotes || r == roleNotesMaster || r == roleHandout }

// phType is the type of a placeholder: a shape that stands for a kind of
// content and takes its place and formatting from the shape of the same
// kind on the layout, which takes them from the master.
type phType uint8

const (
	phNone phType = iota
	phTitle
	phCtrTitle
	phSubTitle
	phBody
	phObject // obj, and what holds one kind of object: chart, tbl, pic, dgm, media, clipArt
	phDate
	phFooter
	phNumber
	phHeader
	phImage // sldImg: the picture of the slide on a notes page
)

func readPh(s *xmlscan.Scanner) (ref phRef) {
	ref.has, ref.typ = true, phObject
	if v, ok := s.Attr(xmlscan.NoSpace, "type"); ok {
		switch string(v) {
		case "title":
			ref.typ = phTitle
		case "ctrTitle":
			ref.typ = phCtrTitle
		case "subTitle":
			ref.typ = phSubTitle
		case "body":
			ref.typ = phBody
		case "dt":
			ref.typ = phDate
		case "ftr":
			ref.typ = phFooter
		case "sldNum":
			ref.typ = phNumber
		case "hdr":
			ref.typ = phHeader
		case "sldImg":
			ref.typ = phImage
		}
	}
	if n, ok := intAttr(s, "idx"); ok && n >= 0 && n < 1<<31 {
		ref.idx = int32(n)
	}
	return ref
}

// Families of placeholder: one of a family on a slide may take after
// another of the same family on its layout.
const (
	familyTitle = iota
	familyBody
	familyDate
	familyFooter
	familyNumber
	familyHeader
	familyImage
)

func (t phType) family() int {
	switch t {
	case phTitle, phCtrTitle:
		return familyTitle
	case phDate:
		return familyDate
	case phFooter:
		return familyFooter
	case phNumber:
		return familyNumber
	case phHeader:
		return familyHeader
	case phImage:
		return familyImage
	}
	return familyBody
}

// The master's three text styles, by the family that uses each.
const (
	styleTitle = iota
	styleBody
	styleOther
)

func (t phType) style() int {
	switch t.family() {
	case familyTitle:
		return styleTitle
	case familyBody:
		return styleBody
	}
	return styleOther
}

type phRef struct {
	has bool
	typ phType
	idx int32
}

// placeholder is a placeholder of a layout or a master, as far as the
// shapes that take after it need it.
type placeholder struct {
	ref   phRef
	box   dml.Rect
	boxed bool
	fill  dml.Fill
	list  *dml.ListStyle
	up    *placeholder // on a layout: the master's placeholder it takes after
}

// match finds the placeholder that one with the given reference takes
// after: the one with the same number if it is of the same family, or
// else the first of the same type, or of the same family.
func match(list []placeholder, ref phRef, byNumber bool) *placeholder {
	var same, kin *placeholder
	for i := range list {
		ph := &list[i]
		if ph.ref.typ.family() != ref.typ.family() {
			continue
		}
		if byNumber && ph.ref.idx == ref.idx {
			return ph
		}
		if same == nil && ph.ref.typ == ref.typ {
			same = ph
		}
		if kin == nil {
			kin = ph
		}
	}
	if same != nil {
		return same
	}
	return kin
}

// painted is an area a shape paints, which text drawn later may be seen
// against.
type painted struct {
	box   dml.Rect
	color theme.Color
	lit   bool // one known colour over all of the box
}

// layout is a slide layout or a slide master as the slides that use it
// need it.
type layout struct {
	name   string
	master *layout       // of a layout; nil for a master
	theme  *theme.Scheme // of the master
	cmap   dml.ColorMap  // in force for it
	show   bool          // a layout: the master's shapes show on its slides
	bg     dml.Fill      // its own background, if it has one
	phs    []placeholder
	paint  []painted          // what its shapes paint, in drawing order
	static []gofastcore.Block // its own text, which shows on every slide that uses it
	// staticBytes is the size of that text: what a slide that repeats it
	// adds to the text of the document.
	staticBytes int
	repeats     []bool            // of a layout: which of static the master has too
	styles      [3]*dml.ListStyle // a master's text styles
	loose       bool              // it paints something whose place is not known
}

// sheet returns the layout or master in the named part, reading it the
// first time it is asked for.
func (p *presentation) sheet(name string, master bool) *layout {
	key := name
	if master {
		key = "master:" + name // a part may be named as both by a damaged file
	}
	p.mu.Lock()
	l := p.sheets[key]
	p.mu.Unlock()
	if l != nil {
		return l
	}
	// Read outside the lock: two slides may read the same layout at
	// once, and get the same result; the first to finish is kept.
	l = &layout{name: name, cmap: dml.DefaultMap, show: true}
	pg := &page{p: p}
	var s xmlscan.Scanner
	rels, err := p.pkg.RelsWith(name, &s)
	p.note(opc.RelsName(name), err)
	for _, rel := range rels {
		switch {
		case rel.External:
		case master && ns.IsRel(rel.Type, ns.RelTheme) && l.theme == nil:
			l.theme = p.theme(rel.Target)
		case !master && ns.IsRel(rel.Type, ns.RelSlideMaster) && l.master == nil:
			l.master = p.sheet(rel.Target, true)
		}
	}
	if l.master != nil {
		l.theme, l.cmap = l.master.theme, l.master.cmap
	}
	pg.sheet(l, rels, master)
	p.mu.Lock()
	if kept := p.sheets[key]; kept != nil {
		l = kept
	} else {
		p.sheets[key] = l
	}
	p.mu.Unlock()
	return l
}

// group is the group of shapes being read: how its children's
// coordinates become the page's, and what holds for all of them.
type group struct {
	m    dml.Matrix
	hide bool     // the group is marked hidden
	top  bool     // the page's own shape tree
	ids  [3]int64 // the identifiers of the groups it is in, innermost first
}

// props is what is known of the shape being read.
type props struct {
	id      int64 // the number animations refer to it by
	ph      phRef
	alt     string
	hide    bool
	media   bool // it plays sound or video
	xfrm    dml.Xfrm
	rect    bool     // its outline is a plain rectangle
	fill    dml.Fill // its own
	styled  dml.Fill // from its style
	font    dml.Color
	blip    string // a picture: the relationship of its image
	linked  bool   // the image is outside the file
	faded   bool   // the picture is made partly transparent
	hasBlip bool

	// Worked out by place.
	placed  bool
	in      *placeholder
	box     dml.Rect
	boxed   bool
	upright bool
	reasons gofastcore.Hidden
	paint   dml.Fill // what fills it, its own or inherited
	behind  theme.Color
	lit     bool
}

// shape is a shape that has been read, for ordering the page and for
// finding what covers what.
type shape struct {
	box        dml.Rect
	boxed      bool
	first, end int      // its blocks
	unit       int      // the top-level shape or group it is part of
	title      bool     // the slide's title
	text       bool     // it has text that could be covered
	cover      bool     // it hides what is under it
	picture    string   // a picture that hides what is under it if it has no transparency
	ids        [4]int64 // its identifier and those of the groups it is in
}

// page reads one part that holds a tree of shapes: a slide, a layout, a
// master or a notes page. A worker keeps one and reads slide after slide
// with it, so its buffers are reused.
type page struct {
	p    *presentation
	role role
	name string
	s    xmlscan.Scanner
	text dml.Text
	full bool // the document has taken all the text it may hold
	pal  dml.Palette

	rels   map[string]opc.Rel
	parent *layout                // a slide's layout, a layout's master
	master *layout                // the master in force, if any
	styles *[3]*dml.ListStyle     // the master's text styles
	number string                 // what a slide-number field shows
	fonts  dml.ListStyle          // scratch: the colour a shape's style gives its text
	chain  [5]*dml.ListStyle      // scratch
	link   func(id []byte) string // set once

	// What the part says of itself.
	hidden     bool // show="0"
	showMaster bool
	bg         dml.Fill
	found      *dml.ColorMap // a colour map the part states

	// The background, resolved.
	backdrop theme.Color
	lit      bool

	shapes  []shape
	blocks  []gofastcore.Block
	paint   []painted
	phs     []placeholder
	loose   bool // something is painted whose place is not known
	unit    int
	effort  int // comparisons the hidden-text checks may still make
	skipped int // shapes left unchecked when it ran out

	animated    []int64 // the shapes and groups that animations act on
	allAnimated bool    // more of them than are kept

	title    string
	sawImage bool
	unread   map[string]int
	damaged  []string
	external int
	programs int
	exceeded bool   // the decompression budget ran out
	cut      string // why the part was cut short, if it was
	cells    int64  // table cells taken from the shared allowance
	failure  error  // why the part itself could not be read
	// Of a notes master or handout: whether its header [0] and footer [1]
	// are shown, and the blocks of each, as pairs of from and to.
	hf      [2]bool
	hfBlock [2][]int
	// Of a notes page: whether it has a header [0] and a footer [1] of its
	// own, in place of the notes master's.
	ownHF [2]bool
}

// reset makes the page ready to read a part, or to read it again.
func (pg *page) reset() {
	pg.hidden, pg.showMaster, pg.bg, pg.found = false, true, dml.Fill{}, nil
	pg.shapes, pg.blocks, pg.paint, pg.phs = pg.shapes[:0], pg.blocks[:0], pg.paint[:0], nil
	pg.loose, pg.unit, pg.skipped = false, 0, 0
	pg.hf, pg.hfBlock, pg.ownHF = [2]bool{true, true}, [2][]int{}, [2]bool{}
	pg.animated, pg.allAnimated = pg.animated[:0], false
	pg.title, pg.sawImage, pg.unread = "", false, nil
	pg.external, pg.programs, pg.cut, pg.failure = 0, 0, "", nil
	pg.text.Removed, pg.text.Unmapped, pg.text.Programs = 0, 0, 0
	if pg.cells > 0 {
		pg.p.cells.Add(pg.cells) // given back: the part is read again
		pg.cells = 0
	}
	if pg.link == nil {
		pg.link = func(id []byte) string {
			if rel, ok := pg.rels[string(id)]; ok && rel.External {
				return rel.Target // cleaned when the relationships were read
			}
			return ""
		}
	}
}

// fail records why reading a part stopped, if it did. own says the part
// is the page's own and not one that supports it.
func (pg *page) fail(name string, err error, own bool) {
	switch {
	case err == nil:
	case errors.Is(err, opc.ErrBudget):
		pg.exceeded = true
		pg.p.exceeded.Store(true)
	case errors.Is(err, warn.ErrText):
		pg.full = true
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
	case own:
		pg.failure = err
	default:
		pg.damaged = append(pg.damaged, name)
	}
}

// count notes content that is present and not read.
func (pg *page) count(kind string) {
	if pg.unread == nil {
		pg.unread = map[string]int{}
	}
	pg.unread[kind]++
}

// read reads the page's part with the colour map m in force, and reports
// the map the part itself states, if it states one.
func (pg *page) read(m dml.ColorMap) {
	pg.reset()
	pg.pal.Map = m
	c, err := pg.p.pkg.Scan(pg.name, &pg.s, scanOptions)
	if err != nil {
		pg.fail(pg.name, err, true)
		return
	}
	pg.part()
	c.Close()
	pg.fail(pg.name, pg.s.Err(), true)
}

// part reads the part the scanner is at the start of.
func (pg *page) part() {
	s := &pg.s
	for s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch s.Depth() {
		case 1:
			if v, ok := s.Attr(xmlscan.NoSpace, "show"); ok {
				pg.hidden = !isTrue(v)
			}
			if v, ok := s.Attr(xmlscan.NoSpace, "showMasterSp"); ok {
				pg.showMaster = isTrue(v)
			}
		case 2:
			switch {
			case s.Is(ns.Pres, "cSld"), s.Is(ns.Pres, "clrMapOvr"), s.Is(ns.Pres, "txStyles"):
				// Read as their content comes.
			case s.Is(ns.Pres, "clrMap"):
				m := dml.ReadColorMap(s)
				pg.found = &m
				s.Skip()
			case s.Is(ns.Pres, "timing") && pg.role == roleSlide:
				pg.timing()
				return // nothing that is read comes after it
			case s.Is(ns.Pres, "extLst") && pg.role != roleMaster:
				return
			case s.Is(ns.Pres, "hf"):
				// Which of the header, footer, date and number a notes
				// page or handout shows. Shown unless it says not.
				for i, name := range [2]string{"hdr", "ftr"} {
					if v, ok := s.Attr(xmlscan.NoSpace, name); ok && (string(v) == "0" || string(v) == "false") {
						pg.hf[i] = false
					}
				}
				s.Skip()
			default:
				s.Skip()
			}
		case 3:
			switch {
			case s.Is(ns.Pres, "bg"):
				pg.background()
			case s.Is(ns.Pres, "spTree"):
				pg.settle()
				g := group{m: dml.Identity, top: true}
				pg.items(&g)
			case s.Is(ns.Draw, "overrideClrMapping"):
				m := dml.ReadColorMap(s)
				pg.found = &m
				s.Skip()
			case pg.role == roleMaster && s.Is(ns.Pres, "titleStyle"):
				pg.styles[styleTitle] = dml.ReadListStyle(s)
			case pg.role == roleMaster && s.Is(ns.Pres, "bodyStyle"):
				pg.styles[styleBody] = dml.ReadListStyle(s)
			case pg.role == roleMaster && s.Is(ns.Pres, "otherStyle"):
				pg.styles[styleOther] = dml.ReadListStyle(s)
			default:
				s.Skip()
			}
		default:
			s.Skip()
		}
	}
}

// maxAnimated bounds the animated shapes that are kept.
const maxAnimated = 1 << 14

// timing reads p:timing, the slide's animations, for one thing: which
// shapes they act on. A shape that appears, moves or leaves during the
// show does not hide what is under it for the whole of the show.
func (pg *page) timing() {
	s := &pg.s
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start || !s.Is(ns.Pres, "spTgt") {
			continue
		}
		if id, ok := intAttr(s, "spid"); ok {
			if len(pg.animated) < maxAnimated {
				pg.animated = append(pg.animated, id)
			} else {
				pg.allAnimated = true
			}
		}
	}
	slices.Sort(pg.animated)
}

// background reads p:bg, the page's own background.
func (pg *page) background() {
	s := &pg.s
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Is(ns.Pres, "bgPr"):
			inner := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= inner {
				if s.Kind() != xmlscan.Start {
					continue
				}
				if f, ok := dml.ReadFill(s); ok {
					if pg.bg.Paint == dml.PaintUnset {
						pg.bg = f
					}
					continue
				}
				s.Skip()
			}
		case s.Is(ns.Pres, "bgRef"):
			// One of the theme's background styles, in a colour.
			idx, _ := intAttr(s, "idx")
			pg.bg = pg.pal.StyleFill(int(idx), dml.ReadColor(s))
		default:
			s.Skip()
		}
	}
}

// settle works out what the page's background is: its own, or its
// layout's, or its master's, and white if none says.
func (pg *page) settle() {
	fill := pg.bg
	for l := pg.parent; fill.Paint == dml.PaintUnset && l != nil; l = l.master {
		fill = l.bg
	}
	switch fill.Paint {
	case dml.PaintUnset, dml.PaintNone:
		pg.backdrop, pg.lit = theme.White, true
	default:
		pg.backdrop, pg.lit = pg.pal.Opaque(fill, nil)
	}
	// Each shape may be compared with a few others; a page that asks for
	// more than this is not checked to the end, and says so.
	pg.effort = 1 << 18
}

// items reads the shapes in the element whose Start token is current: a
// shape tree, a group, or one of two alternative forms of a shape.
func (pg *page) items(g *group) {
	s := &pg.s
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		if g.top {
			pg.unit++
		}
		switch {
		case s.Is(ns.Pres, "sp"):
			pg.sp(g)
		case s.Is(ns.Pres, "pic"):
			pg.pic(g)
		case s.Is(ns.Pres, "graphicFrame"):
			pg.frame(g)
		case s.Is(ns.Pres, "grpSp"):
			inner := group{m: g.m, hide: g.hide, ids: [3]int64{0, g.ids[0], g.ids[1]}}
			pg.items(&inner)
		case s.Is(ns.Pres, "nvGrpSpPr"):
			var sh props
			pg.nonVisual(&sh, false)
			g.hide = g.hide || sh.hide
			if !g.top {
				g.ids[0] = sh.id
			}
		case s.Is(ns.Pres, "grpSpPr"):
			outer := g.m
			inside := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= inside {
				if s.Kind() != xmlscan.Start {
					continue
				}
				if s.Is(ns.Draw, "xfrm") {
					if x := dml.ReadXfrm(s); x.Set {
						g.m = x.Children().Then(outer)
					}
					continue
				}
				s.Skip()
			}
		case s.Is(ns.MC, "AlternateContent"):
			pg.alternate(g)
		default:
			s.Skip() // connectors, ink
		}
	}
}

// alternate reads mc:AlternateContent: the same content in a form for
// programs that know some extension and in a form for those that do not.
// Exactly one is read: the first that yields anything.
func (pg *page) alternate(g *group) {
	s := &pg.s
	depth := s.Depth()
	done := false
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		if done || !(s.Is(ns.MC, "Choice") || s.Is(ns.MC, "Fallback")) {
			s.Skip()
			continue
		}
		before := pg.yield()
		inner := group{m: g.m, hide: g.hide, ids: g.ids}
		pg.items(&inner)
		done = pg.yield() != before
	}
}

// yield is a number that grows whenever reading yields anything.
func (pg *page) yield() int {
	n := len(pg.shapes) + len(pg.blocks) + len(pg.phs)
	for _, k := range pg.unread {
		n += k
	}
	return n
}

// nonVisual reads the properties of a shape that are not about how it
// looks (p:nvSpPr and its like): its description, whether it is hidden,
// what placeholder it is.
func (pg *page) nonVisual(sh *props, alt bool) {
	s := &pg.s
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Is(ns.Pres, "cNvPr"):
			if id, ok := intAttr(s, "id"); ok {
				sh.id = id
			}
			if v, ok := s.Attr(xmlscan.NoSpace, "hidden"); ok {
				sh.hide = isTrue(v)
			}
			if v, ok := s.Attr(xmlscan.NoSpace, "descr"); ok && alt && len(v) > 0 {
				var removed int
				sh.alt, removed = clean.String(v, clean.Prose)
				pg.text.Removed += removed
			}
		case s.Is(ns.Draw, "hlinkClick"), s.Is(ns.Draw, "hlinkHover"):
			if action, ok := s.Attr(xmlscan.NoSpace, "action"); ok && dml.StartsProgram(action) {
				pg.programs++
			}
			s.Skip()
		case s.Is(ns.Pres, "ph"):
			sh.ph = readPh(s)
			s.Skip()
		case s.Space() == ns.Draw:
			switch string(s.Local()) {
			case "videoFile", "audioFile", "wavAudioFile", "quickTimeFile", "audioCd":
				sh.media = true
			}
			s.Skip()
		}
	}
}

// shapeProps reads p:spPr: where the shape is, its outline, its fill.
func (pg *page) shapeProps(sh *props) {
	s := &pg.s
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		if f, ok := dml.ReadFill(s); ok {
			if sh.fill.Paint == dml.PaintUnset {
				sh.fill = f
			}
			continue
		}
		switch {
		case s.Is(ns.Draw, "xfrm"):
			sh.xfrm = dml.ReadXfrm(s)
			continue
		case s.Is(ns.Draw, "prstGeom"):
			v, _ := s.Attr(xmlscan.NoSpace, "prst")
			sh.rect = string(v) == "rect"
		}
		s.Skip()
	}
}

// shapeStyle reads p:style: the fill and the text colour the shape takes
// from the theme unless it has its own.
func (pg *page) shapeStyle(sh *props) {
	s := &pg.s
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Is(ns.Draw, "fillRef"):
			idx, _ := intAttr(s, "idx")
			sh.styled = pg.pal.StyleFill(int(idx), dml.ReadColor(s))
		case s.Is(ns.Draw, "fontRef"):
			sh.font = dml.ReadColor(s)
		default:
			s.Skip()
		}
	}
}

// place works out where the shape is, whether it can be seen at all, and
// what its text is seen against.
func (pg *page) place(sh *props, g *group) {
	if sh.placed {
		return
	}
	sh.placed = true
	if sh.ph.has && pg.role != roleMaster {
		if pg.parent != nil {
			sh.in = match(pg.parent.phs, sh.ph, pg.role == roleSlide)
			if sh.in == nil && pg.parent.master != nil {
				sh.in = match(pg.parent.master.phs, sh.ph, false)
			}
		}
	}
	if sh.xfrm.Set {
		sh.box, sh.upright, sh.boxed = sh.xfrm.Local().Then(g.m).Bounds(sh.xfrm.W, sh.xfrm.H)
	} else {
		for in := sh.in; in != nil; in = in.up {
			if in.boxed {
				sh.box, sh.boxed, sh.upright = in.box, true, true
				break
			}
		}
	}
	if sh.hide || g.hide {
		sh.reasons |= gofastcore.HiddenInvisible
	}
	if w, h := pg.p.width, pg.p.height; sh.boxed && w > 0 && h > 0 && !pg.role.paper() &&
		(sh.box.X1 <= 0 || sh.box.Y1 <= 0 || sh.box.X0 >= w || sh.box.Y0 >= h) {
		sh.reasons |= gofastcore.HiddenOffPage
	}

	// Its fill: its own, or the one its own style gives it, or the one
	// the placeholder it takes after has.
	sh.paint = sh.fill
	if sh.paint.Paint == dml.PaintUnset && sh.styled.Paint != dml.PaintNone {
		sh.paint = sh.styled
	}
	for in := sh.in; sh.paint.Paint == dml.PaintUnset && in != nil; in = in.up {
		sh.paint = in.fill
	}
	switch sh.paint.Paint {
	case dml.PaintSolid:
		sh.behind, sh.lit = pg.pal.Opaque(sh.paint, nil)
	case dml.PaintOther:
		sh.lit = false
	default:
		sh.behind, sh.lit = pg.behind(sh.box, sh.boxed)
	}
}

// behind works out what something in the given box, with no fill of its
// own, is seen against: the last thing painted under it, if that is one
// colour over the whole box, or the page's background if nothing is.
func (pg *page) behind(box dml.Rect, boxed bool) (theme.Color, bool) {
	if pg.role.paper() {
		return theme.White, true // a notes page is paper
	}
	// What the layout and the master draw is under the slide's shapes,
	// unless the slide hides it; a layout may hide its master's.
	lists := [3][]painted{pg.paint}
	loose := pg.loose
	if l := pg.parent; l != nil && pg.showMaster {
		if pg.role == roleSlide {
			lists[1], loose = l.paint, loose || l.loose
			if !l.show {
				l = nil
			} else {
				l = l.master
			}
		}
		if l != nil {
			lists[2], loose = l.paint, loose || l.loose
		}
	}
	if !boxed || loose {
		// Where it is, or where something painted is, is not known: it is
		// seen against the background only if nothing is painted at all.
		if len(lists[0])+len(lists[1])+len(lists[2]) == 0 && !loose {
			return pg.backdrop, pg.lit
		}
		return theme.Color{}, false
	}
	for _, list := range lists {
		for i := len(list) - 1; i >= 0; i-- {
			if pg.effort--; pg.effort < 0 {
				pg.skipped++
				return theme.Color{}, false
			}
			pt := &list[i]
			if !pt.box.Overlaps(box) {
				continue
			}
			if pt.lit && pt.box.Contains(box) {
				return pt.color, true
			}
			return theme.Color{}, false
		}
	}
	return pg.backdrop, pg.lit
}

// paints records that the shape paints its box, for text drawn after it.
func (pg *page) paints(sh *props, lit bool) {
	if sh.reasons&gofastcore.HiddenInvisible != 0 {
		return
	}
	if !sh.boxed {
		pg.loose = true
		return
	}
	pt := painted{box: sh.box}
	if lit && sh.upright {
		pt.color, pt.lit = sh.behind, sh.lit
	}
	pg.paint = append(pg.paint, pt)
}

// add records a shape that has been read.
func (pg *page) add(sh *props, g *group, first int, title, cover bool, picture string) {
	text := false
	for i := first; i < len(pg.blocks) && !text; i++ {
		text = pg.blocks[i].Kind != gofastcore.KindImage
	}
	pg.shapes = append(pg.shapes, shape{
		box: sh.box, boxed: sh.boxed, first: first, end: len(pg.blocks), unit: pg.unit, title: title,
		text:  text && sh.boxed && sh.box.X1 > sh.box.X0 && sh.box.Y1 > sh.box.Y0,
		cover: cover, picture: picture, ids: [4]int64{sh.id, g.ids[0], g.ids[1], g.ids[2]},
	})
}

// sp reads a shape (p:sp).
func (pg *page) sp(g *group) {
	s := &pg.s
	var sh props
	first := len(pg.blocks)
	title := false
	var list *dml.ListStyle // of its text
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Is(ns.Pres, "nvSpPr"):
			pg.nonVisual(&sh, false)
		case s.Is(ns.Pres, "spPr"):
			pg.shapeProps(&sh)
		case s.Is(ns.Pres, "style"):
			pg.shapeStyle(&sh)
		case s.Is(ns.Pres, "txBody"):
			pg.place(&sh, g)
			title = pg.body(&sh) || title
			list = pg.text.Own()
		default:
			s.Skip()
		}
	}
	pg.place(&sh, g)
	if (pg.role == roleNotesMaster || pg.role == roleHandout) && sh.ph.has && len(pg.blocks) > first {
		// The blocks of the header or footer, which the page may not show.
		which := 1
		if sh.ph.typ == phHeader {
			which = 0
		}
		pg.hfBlock[which] = append(pg.hfBlock[which], first, len(pg.blocks))
	}
	if pg.role.paper() {
		return
	}
	if sh.ph.has && pg.role != roleSlide {
		// A placeholder of a layout or master is not shown on a slide; it
		// is what the slide's own placeholder takes after.
		ph := placeholder{ref: sh.ph, box: sh.box, boxed: sh.boxed, fill: sh.fill, list: list}
		if ph.fill.Paint == dml.PaintUnset {
			ph.fill = sh.styled
		}
		if sh.in != nil {
			ph.up = sh.in
		}
		if len(pg.phs) < maxPlaceholders {
			pg.phs = append(pg.phs, ph)
		}
		return
	}
	painted := sh.paint.Paint == dml.PaintSolid || sh.paint.Paint == dml.PaintOther
	if painted {
		pg.paints(&sh, sh.rect)
	}
	cover := painted && sh.rect && sh.upright && sh.lit && sh.boxed && sh.reasons&gofastcore.HiddenInvisible == 0
	if len(pg.blocks) > first || painted {
		pg.add(&sh, g, first, title, cover, "")
	}
}

// maxPlaceholders bounds the placeholders kept from a layout or master.
// Each placeholder of a slide is looked up among them.
const maxPlaceholders = 256

// isSlideTitle and the like say what a placeholder's text is.
func kindOf(t phType) (kind gofastcore.Kind, level uint8, joined bool) {
	switch t {
	case phTitle, phCtrTitle:
		return gofastcore.KindHeading, 1, true
	case phSubTitle:
		return gofastcore.KindHeading, 2, true
	case phDate, phFooter, phNumber:
		return gofastcore.KindFooter, 0, false
	case phHeader:
		return gofastcore.KindHeader, 0, false
	}
	return gofastcore.KindParagraph, 0, false
}
