package pml

import (
	"context"
	"errors"

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
// notes page or a handout, which has a size of its own.
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

// layout is a slide layout or a slide master as the slides that use it
// need it.
type layout struct {
	name   string
	master *layout       // of a layout; nil for a master
	theme  *theme.Scheme // of the master
	cmap   dml.ColorMap  // in force for it
	show   bool          // a layout: the master's shapes show on its slides
	phs    []placeholder
	static []gofastcore.Block // its own text, which shows on every slide that uses it
	// staticBytes is the size of that text: what a slide that repeats it
	// adds to the text of the document.
	staticBytes int
	repeats     []bool            // of a layout: which of static the master has too
	styles      [3]*dml.ListStyle // a master's text styles
	lost        *warn.Lost        // what the fixed bounds left out of it, said on every slide that shows it
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
	id   int64 // the number animations refer to it by
	ph   phRef
	alt  string
	name string // the name the file gives it, which a chart takes
	// nameOver is how many bytes the name was longer than it is kept.
	nameOver int64
	hide     bool
	media    bool // it plays sound or video
	xfrm     dml.Xfrm
	font     dml.Color
	blip     string // a picture: the relationship of its image
	linked   bool   // the image is outside the file
	hasBlip  bool

	// Worked out by place.
	placed  bool
	in      *placeholder
	box     dml.Rect
	boxed   bool
	upright bool
	reasons gofastcore.Display // what the file declares about its display
}

// shape is a shape that has been read, for ordering the page.
type shape struct {
	box        dml.Rect
	boxed      bool
	first, end int  // its blocks
	unit       int  // the top-level shape or group it is part of
	title      bool // the slide's title
}

// page reads one part that holds a tree of shapes: a slide, a layout, a
// master, a notes page, the notes master or the handout master. A worker keeps one and reads slide after slide
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
	found      *dml.ColorMap // a colour map the part states

	shapes []shape
	blocks []gofastcore.Block
	phs    []placeholder
	unit   int

	title    string
	sawImage bool
	unread   map[string]int
	charts   []opc.Rel // what the charts read embed: their workbooks
	damaged  []string
	external int
	programs int
	exceeded bool      // the decompression budget ran out
	cut      string    // why the part was cut short, if it was
	cells    int64     // table cells taken from the shared allowance
	lost     warn.Lost // what the fixed bounds left out of the part
	failure  error     // why the part itself could not be read
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
	pg.hidden, pg.showMaster, pg.found = false, true, nil
	pg.shapes, pg.blocks, pg.phs = pg.shapes[:0], pg.blocks[:0], nil
	pg.unit = 0
	pg.hf, pg.hfBlock, pg.ownHF = [2]bool{true, true}, [2][]int{}, [2]bool{}
	pg.title, pg.sawImage, pg.unread, pg.charts = "", false, nil, pg.charts[:0]
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
			case s.Is(ns.Pres, "timing"):
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
			case s.Is(ns.Pres, "spTree"):
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
			if v, ok := s.Attr(xmlscan.NoSpace, "name"); ok {
				sh.name, sh.nameOver = clean.Name(string(v)), warn.Over(string(v))
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

// shapeProps reads p:spPr: where the shape is.
func (pg *page) shapeProps(sh *props) {
	s := &pg.s
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		if s.Is(ns.Draw, "xfrm") {
			sh.xfrm = dml.ReadXfrm(s)
			continue
		}
		s.Skip()
	}
}

// shapeStyle reads p:style: the text colour the shape takes from the
// theme unless it has its own.
func (pg *page) shapeStyle(sh *props) {
	s := &pg.s
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		if s.Is(ns.Draw, "fontRef") {
			sh.font = dml.ReadColor(s)
			continue
		}
		s.Skip()
	}
}

// place works out where the shape is, the placeholder it takes after,
// and whether the file declares it hidden.
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
		sh.reasons |= gofastcore.DisplayHidden
	}
}

// add records a shape that has been read.
func (pg *page) add(sh *props, g *group, first int, title bool) {
	pg.shapes = append(pg.shapes, shape{box: sh.box, boxed: sh.boxed, first: first, end: len(pg.blocks), unit: pg.unit, title: title})
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
		ph := placeholder{ref: sh.ph, box: sh.box, boxed: sh.boxed, list: list}
		if sh.in != nil {
			ph.up = sh.in
		}
		if len(pg.phs) < maxPlaceholders {
			pg.phs = append(pg.phs, ph)
		} else {
			pg.lost.Add(warn.CapPlaceholders, 1)
		}
		return
	}
	if len(pg.blocks) > first {
		pg.add(&sh, g, first, title)
	}
}

// maxPlaceholders bounds the placeholders kept from a layout or master.
// Each placeholder of a slide is looked up among them.
const maxPlaceholders = warn.MaxPlaceholders

// kindOf says what a placeholder's text is: its kind of block, its
// heading level, and whether its paragraphs are joined into one block.
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
