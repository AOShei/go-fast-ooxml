package pml

import (
	"context"
	"errors"
	"sort"
	"strconv"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-core/clean"
	"github.com/AOShei/go-fast-ooxml/internal/dml"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/theme"
	"github.com/AOShei/go-fast-ooxml/internal/unit"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// index puts the relationships of the part being read where its shapes
// look them up.
func (pg *page) index(rels []opc.Rel) {
	if pg.rels == nil {
		pg.rels = make(map[string]opc.Rel, len(rels))
	} else {
		clear(pg.rels)
	}
	for _, rel := range rels {
		pg.rels[rel.ID] = rel
	}
}

// slide reads one slide into a part.
func (pg *page) slide(ctx context.Context, index int, ref slideRef) *unit.Result {
	p := pg.p
	res := &unit.Result{}
	part := &res.Part
	part.Kind, part.Number, part.Width, part.Height = gofastcore.PartSlide, index+1, p.width, p.height
	pg.exceeded, pg.damaged, pg.cells, pg.full = false, nil, 0, false
	// What the document may still hold of text is taken paragraph by
	// paragraph as the slide, its notes and its comments are read.
	room := gofastcore.TextBudgetFrom(ctx)
	pg.text.Budget, pg.text.Taken = room, 0

	removed, unmapped, sawImage := 0, 0, false
	cut := ""
	switch {
	case ref.target == "" || !p.pkg.Has(ref.target):
		part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, "the presentation lists the slide but does not contain it")
	case p.exceeded.Load():
		part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, "nothing was read before the limit")
		res.Limited = true
	case ctx.Err() != nil:
		// Cancelled: the caller gets the context's error, not this part.
	default:
		rels, err := p.pkg.RelsWith(ref.target, &pg.s)
		pg.fail(opc.RelsName(ref.target), err, false)
		res.Rels = rels
		var notes string
		var comments []string
		pg.parent, pg.master, pg.styles = nil, nil, nil
		pg.pal = dml.Palette{Map: dml.DefaultMap}
		for _, rel := range rels {
			switch {
			case rel.External:
				if !ns.IsRel(rel.Type, ns.RelHyperlink) {
					res.External++
				}
			case ns.IsRel(rel.Type, ns.RelSlideLayout) && pg.parent == nil:
				pg.parent = p.sheet(rel.Target, false)
			case ns.IsRel(rel.Type, ns.RelNotesSlide) && notes == "":
				notes = rel.Target
			case ns.IsRel(rel.Type, ns.RelComments):
				comments = append(comments, rel.Target)
			}
		}
		colors := dml.DefaultMap
		if l := pg.parent; l != nil {
			pg.pal.Theme, colors = l.theme, l.cmap
			if pg.master = l.master; pg.master != nil {
				pg.styles = &pg.master.styles
			}
		}

		// The slide itself. A slide may replace the colour map, and says
		// so after its shapes; one that does is read again under its own.
		pg.role, pg.name = roleSlide, ref.target
		pg.number = strconv.Itoa(index + p.first)
		pg.index(rels)
		pg.lost.Take() // what an earlier slide left, should it have stopped short
		pg.read(colors)
		if pg.found != nil && *pg.found != colors && pg.failure == nil && !pg.exceeded && !pg.full {
			// Read again with the colours the slide turned out to have:
			// its text is taken a second time, so the first is given
			// back, and what the bounds left out is counted again.
			room.Return(pg.text.Taken)
			pg.text.Taken = 0
			pg.lost.Take()
			colors = *pg.found
			pg.read(colors)
		}
		blocks := pg.arrange()
		if pg.hidden {
			part.Display = gofastcore.DisplayHidden
		}
		part.Name = pg.title
		res.Unread, res.Programs = pg.unread, pg.programs
		// The workbooks the slide's charts embed are files the slide
		// embeds.
		res.Rels = append(rels[:len(rels):len(rels)], pg.charts...)
		removed, sawImage, cut = pg.text.Removed, pg.sawImage, pg.cut
		pg.text.Removed, pg.cells = 0, 0 // counted; the cells stay taken
		unmapped, pg.text.Unmapped = pg.text.Unmapped, 0
		if err := pg.failure; err != nil {
			switch {
			case errors.Is(err, xmlscan.ErrDepth), errors.Is(err, xmlscan.ErrTokenTooLarge):
				part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the slide is cut off: "+clean.Label(err.Error()))
			default:
				part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, "the slide is cut off: "+clean.Label(err.Error()))
			}
		}

		// What the layout and the master put on every slide.
		// A slide may hide all of it, and a layout its master's.
		if l := pg.parent; l != nil && pg.showMaster {
			var above []gofastcore.Block
			if l.master != nil && l.show {
				above = l.master.static
			}
			// What every slide repeats is stored once and written with
			// every slide: it counts against the text the document may
			// hold each time.
			bytes := l.staticBytes
			if above != nil {
				bytes += l.master.staticBytes
			}
			switch n := int64(len(above) + len(l.static)); {
			case n == 0:
			case p.repeated.Add(-n) < 0:
				p.repeated.Add(n)
				part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the text of the slide's layout and master is left out: the slides of the presentation repeat more of it than is kept")
				res.Limited = true
			case !room.Take(bytes):
				pg.full = true
			default:
				if above != nil {
					pg.lost.Merge(l.master.lost)
				}
				pg.lost.Merge(l.lost)
				blocks = appendRepeated(blocks, above)
				// A layout often repeats what its master has, in the same
				// place; a reader sees it once.
				for i := range l.static {
					if above == nil || !l.repeats[i] {
						blocks = appendRepeated(blocks, l.static[i:i+1])
					}
				}
			}
		}

		// The speaker's notes, then the comments.
		if notes != "" && p.pkg.Has(notes) && !pg.exceeded {
			pg.role, pg.name = roleNotes, notes
			pg.parent, pg.master, pg.styles = nil, nil, nil
			pg.number = ""
			pg.index(nil)
			pg.read(dml.DefaultMap)
			if pg.failure != nil {
				pg.damaged = append(pg.damaged, notes)
			}
			blocks = append(blocks, pg.blocks...)
			removed += pg.text.Removed
			pg.text.Removed = 0
			unmapped += pg.text.Unmapped
			pg.text.Unmapped = 0
			res.Programs += pg.programs
			// What the notes master puts on every notes page, but the
			// header or footer the page has its own of.
			blocks = pg.addPaper(blocks, 0, pg.ownHF, room, part, res)
		}
		for _, name := range comments {
			if !pg.exceeded {
				blocks = pg.comments(name, blocks)
			}
		}
		removed += pg.text.Removed
		pg.text.Removed = 0
		unmapped += pg.text.Unmapped
		pg.text.Unmapped = 0
		// What the handout master puts on every handout: once, with the
		// first slide.
		if index == 0 {
			blocks = pg.addPaper(blocks, 1, [2]bool{}, room, part, res)
		}
		part.Blocks = blocks
		pg.blocks = pg.blocks[:0]
	}

	res.Damaged = pg.damaged
	if pg.full {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, warn.TextCut("slide"))
		res.Limited = true
	}
	if pg.exceeded {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the decompressed size limit was reached; the slide is cut off there")
		res.Limited = true
	}
	if cut != "" {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, cut+"; the tables of the slide are cut off there")
		res.Limited = true
	}
	if pg.lost.Say(part) {
		res.Limited = true
	}
	hasText, hasImage := false, sawImage
	for i := range part.Blocks {
		if part.Blocks[i].Kind == gofastcore.KindImage {
			hasImage = true
		} else {
			hasText = true
		}
	}
	if !hasText && !part.Has(gofastcore.WarnPartFailed) {
		if hasImage {
			part.Warn(gofastcore.SeverityWarning, gofastcore.WarnPartImageOnly, "")
		} else {
			part.Warn(gofastcore.SeverityInfo, gofastcore.WarnPartNoText, "")
		}
	}
	if removed > 0 {
		part.Warn(gofastcore.SeverityInfo, gofastcore.WarnTextControlCharactersRemoved, strconv.Itoa(removed))
	}
	if unmapped > 0 {
		part.Warn(gofastcore.SeverityWarning, gofastcore.WarnTextUnmappedGlyphs, warn.Unmapped(unmapped))
	}
	part.Finish()
	return res
}

// sheet reads a layout or a master into l.
func (pg *page) sheet(l *layout, rels []opc.Rel, master bool) {
	p := pg.p
	// A layout is read once and kept for every slide and every reading
	// of the presentation: its text is counted where a slide repeats it,
	// not here, where a budget that ran out would cut it for all.
	room, full := pg.text.Budget, pg.full
	pg.text.Budget = nil
	defer func() { pg.text.Budget, pg.full = room, full }()
	// What the bounds leave out of it is its own, said on the slides
	// that show it.
	before := pg.lost.Take()
	defer func() { l.lost = pg.lost.Take(); pg.lost.Merge(before) }()
	pg.name, pg.number = l.name, ""
	pg.index(rels)
	pg.pal = dml.Palette{Theme: l.theme, Map: l.cmap}
	if master {
		pg.role, pg.parent, pg.master, pg.styles = roleMaster, nil, nil, &l.styles
	} else {
		pg.role, pg.parent, pg.master, pg.styles = roleLayout, l.master, l.master, nil
		if l.master != nil {
			pg.styles = &l.master.styles
		}
	}
	if !p.pkg.Has(l.name) {
		return
	}
	// A master states its colour map and its text styles after its
	// shapes, and a layout may replace the map likewise. Where what was
	// assumed for the shapes turns out wrong, they are read again.
	colors := l.cmap
	pg.read(colors)
	again := pg.found != nil && *pg.found != colors
	if pg.found != nil {
		colors = *pg.found
	}
	if master && len(pg.blocks) > 0 {
		again = true
	}
	if again && pg.failure == nil && !pg.exceeded {
		pg.lost.Take() // counted again
		pg.read(colors)
	}
	if pg.failure != nil {
		p.note(l.name, pg.failure)
	}
	for _, name := range pg.damaged {
		p.note(name, errors.New("damaged"))
	}
	l.cmap, l.show, l.phs = colors, pg.showMaster, pg.phs
	// Its own text, from the top down.
	l.static = append([]gofastcore.Block(nil), pg.blocks...)
	sort.SliceStable(l.static, func(i, j int) bool {
		a, b := l.static[i].Loc.BBox, l.static[j].Loc.BBox
		if a.Y0 != b.Y0 {
			return a.Y0 < b.Y0
		}
		return a.X0 < b.X0
	})
	// Said once: the same words in the same place again add nothing,
	// here or on the master.
	type said struct {
		text  string
		box   gofastcore.Rect
		image string // a picture is the same only if it is the same picture
	}
	seen := map[said]bool{}
	kept := l.static[:0]
	for _, b := range l.static {
		if key := (said{b.Text, b.Loc.BBox, imageID(&b)}); !seen[key] {
			seen[key] = true
			kept = append(kept, b)
		}
	}
	l.static = kept
	// These blocks are copied to every slide that shows them.
	l.staticBytes = 0
	for i := range l.static {
		l.staticBytes += blockBytes(&l.static[i])
	}
	if l.master != nil && len(l.master.static) > 0 && len(l.static) > 0 {
		above := map[said]bool{}
		for _, b := range l.master.static {
			above[said{b.Text, b.Loc.BBox, imageID(&b)}] = true
		}
		l.repeats = make([]bool, len(l.static))
		for i, b := range l.static {
			l.repeats[i] = above[said{b.Text, b.Loc.BBox, imageID(&b)}]
		}
	} else {
		l.repeats = make([]bool, len(l.static))
	}
}

// comments reads a part of comments on a slide and appends each as a
// note. There are two forms: the older, with the text in an element, and
// the threaded form of newer versions, where a comment has replies.
func (pg *page) comments(name string, out []gofastcore.Block) []gofastcore.Block {
	p := pg.p
	s := &pg.s
	c, err := p.pkg.Scan(name, s, scanOptions)
	if err != nil {
		pg.fail(name, err, false)
		return out
	}
	authors := p.authorNames()
	t := &pg.text
	t.Palette, t.Chain, t.Base = &pg.pal, nil, dml.RunProps{}
	t.Styles, t.Link, t.SlideNumber = false, nil, ""

	block := func(text, author, date []byte) gofastcore.Block {
		b := gofastcore.Block{Kind: gofastcore.KindNote, Attrs: map[string]string{gofastcore.AttrNoteType: gofastcore.NoteComment}}
		var removed int
		b.Text, removed = clean.String(text, clean.Prose)
		t.Removed += removed
		if who := authors[string(author)]; who != "" {
			b.Attrs[gofastcore.AttrAuthor] = who
		}
		if len(date) > 0 {
			b.Attrs[gofastcore.AttrDate] = clean.Label(string(date))
		}
		return b
	}
	// A threaded comment's replies are stored before its own text; they
	// are held back until the comment has been written.
	type open struct {
		depth        int
		author, date []byte
		modern       bool
	}
	var stack []open
	var held [][]gofastcore.Block
	var raw []byte
	for s.Next() != xmlscan.EOF {
		switch s.Kind() {
		case xmlscan.End:
			if n := len(stack); n > 0 && s.Depth() < stack[n-1].depth {
				stack = stack[:n-1]
				if len(stack) == 0 {
					for _, replies := range held {
						out = append(out, replies...)
					}
					held = held[:0]
				}
			}
			continue
		case xmlscan.Start:
		default:
			continue
		}
		modern := s.Space() == ns.Pres2018
		switch {
		case (s.Is(ns.Pres, "cm") || modern && (string(s.Local()) == "cm" || string(s.Local()) == "reply")) && len(stack) >= warn.MaxCommentDepth:
			pg.lost.Add(warn.CapCommentDepth, 1)
		case s.Is(ns.Pres, "cm") || modern && (string(s.Local()) == "cm" || string(s.Local()) == "reply"):
			o := open{depth: s.Depth(), modern: modern}
			if v, ok := s.Attr(xmlscan.NoSpace, "authorId"); ok {
				if len(v) <= 64 {
					o.author = append([]byte(nil), v...)
				} else {
					pg.lost.Add(warn.CapCommentAuthorID, 1)
				}
			}
			date, ok := s.Attr(xmlscan.NoSpace, "dt")
			if !ok {
				date, ok = s.Attr(xmlscan.NoSpace, "created")
			}
			if ok {
				if len(date) <= 64 {
					o.date = append([]byte(nil), date...)
				} else {
					pg.lost.Add(warn.CapCommentDate, 1)
				}
			}
			stack = append(stack, o)
			for len(held) < len(stack) {
				held = append(held, nil)
			}
		case len(stack) == 0:
		case s.Is(ns.Pres, "text"):
			// The older form: the text as it is.
			raw = s.AppendText(raw[:0])
			o := &stack[len(stack)-1]
			if b := block(raw, o.author, o.date); b.Text != "" {
				out = append(out, b)
			}
		case modern && string(s.Local()) == "txBody":
			t.Read(s)
			raw = raw[:0]
			for i := range t.Paras {
				if i > 0 {
					raw = append(raw, '\n')
				}
				raw = append(raw, t.Buf[t.Paras[i].Start:t.Paras[i].End]...)
			}
			o := &stack[len(stack)-1]
			b := block(raw, o.author, o.date)
			if b.Text == "" {
				continue
			}
			if level := len(stack); level == 1 {
				// The comment itself: before the replies read so far.
				out = append(out, b)
			} else {
				held[level-1] = append(held[level-1], b)
			}
		}
	}
	for _, replies := range held {
		out = append(out, replies...)
	}
	c.Close()
	pg.fail(name, s.Err(), false)
	return out
}

// maxAuthors bounds the authors of comments that are kept.
const maxAuthors = warn.MaxCommentAuthors

// authorNames returns the names of the authors of comments, by the
// identifier comments refer to them with.
func (p *presentation) authorNames() map[string]string {
	p.authorOnce.Do(func() {
		authors := map[string]string{}
		for _, name := range p.authorParts {
			p.load(name, func(s *xmlscan.Scanner) {
				for s.Next() != xmlscan.EOF {
					if s.Kind() != xmlscan.Start || !(s.Is(ns.Pres, "cmAuthor") || s.Is(ns.Pres2018, "author")) {
						continue
					}
					id, ok1 := s.Attr(xmlscan.NoSpace, "id")
					who, ok2 := s.Attr(xmlscan.NoSpace, "name")
					switch {
					case !ok1 || !ok2 || len(id) > 64:
					case len(authors) < maxAuthors:
						authors[string(id)], _ = clean.String(who, clean.Prose)
					default:
						p.lost.Add(warn.CapCommentAuthors, 1)
					}
				}
			})
		}
		p.authors = authors
	})
	return p.authors
}

// appendRepeated appends blocks a layout or master puts on a slide. A
// picture gets an Image of its own on each slide, so that its bytes can
// be left with the first block that shows it and taken from the others.
func appendRepeated(blocks, from []gofastcore.Block) []gofastcore.Block {
	for _, b := range from {
		if b.Image != nil {
			img := *b.Image
			b.Image = &img
		}
		blocks = append(blocks, b)
	}
	return blocks
}

// imageID returns the ID of a picture block's image, or "".
func imageID(b *gofastcore.Block) string {
	if b.Image == nil {
		return ""
	}
	if b.Image.ID == "" {
		return "\x00" + b.Attrs["href"] // a picture outside the file
	}
	return b.Image.ID
}

// addPaper appends what the notes master (which 0) or the handout master
// (1) puts on every page it makes, as headers and footers. Like the text
// of a layout, it is stored once and counts each time it is written.
func (pg *page) addPaper(blocks []gofastcore.Block, which int, own [2]bool, room *gofastcore.TextBudget, part *gofastcore.Part, res *unit.Result) []gofastcore.Block {
	p := pg.p
	paper, from, bytes := p.readPaper(which)
	if own[0] || own[1] {
		// The page has a header or a footer of its own in place of the
		// master's.
		var kept []gofastcore.Block
		bytes = 0
		for i, b := range paper {
			if f := from[i]; f >= 0 && own[f] {
				continue
			}
			kept = append(kept, b)
			bytes += blockBytes(&b)
		}
		paper = kept
	}
	switch n := int64(len(paper)); {
	case n == 0:
	case p.repeated.Add(-n) < 0:
		p.repeated.Add(n)
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the header and footer of the notes pages are left out: the slides of the presentation repeat more of them than is kept")
		res.Limited = true
	case !room.Take(bytes):
		pg.full = true
	default:
		blocks = appendRepeated(blocks, paper)
	}
	return blocks
}

// readPaper reads the notes master (which 0) or the handout master (1)
// once: its header and footer, and text of its own. The frames its pages
// fill in, the date and the page number are not read. A header or footer
// the master switches off (p:hf) is declared hidden.
func (p *presentation) readPaper(which int) (blocks []gofastcore.Block, from []int8, bytes int) {
	p.paperOnce[which].Do(func() {
		name := p.masters[which]
		if name == "" || !p.pkg.Has(name) {
			return
		}
		pg := &page{p: p}
		var s xmlscan.Scanner
		rels, err := p.pkg.RelsWith(name, &s)
		p.note(opc.RelsName(name), err)
		var th *theme.Scheme
		for _, rel := range rels {
			if !rel.External && ns.IsRel(rel.Type, ns.RelTheme) && th == nil {
				th = p.theme(rel.Target)
			}
		}
		pg.name, pg.number = name, ""
		pg.role = [2]role{roleNotesMaster, roleHandout}[which]
		pg.index(rels)
		pg.pal = dml.Palette{Theme: th, Map: dml.DefaultMap}
		pg.read(dml.DefaultMap)
		if pg.found != nil && *pg.found != dml.DefaultMap && pg.failure == nil && !pg.exceeded {
			pg.read(*pg.found)
		}
		if pg.failure != nil {
			p.note(name, pg.failure)
		}
		blocks := append([]gofastcore.Block(nil), pg.blocks...)
		// Which placeholder each block is from: header 0, footer 1, or
		// none (-1), the master's own text.
		from := make([]int8, len(blocks))
		for i := range from {
			from[i] = -1
		}
		for i := range 2 {
			for j := 0; j+1 < len(pg.hfBlock[i]); j += 2 {
				for k := pg.hfBlock[i][j]; k < pg.hfBlock[i][j+1] && k < len(blocks); k++ {
					from[k] = int8(i)
					if !pg.hf[i] {
						blocks[k].Display |= gofastcore.DisplayHidden
					}
				}
			}
		}
		bytes := 0
		for i := range blocks {
			bytes += blockBytes(&blocks[i])
		}
		p.paper[which], p.paperFrom[which], p.paperBytes[which] = blocks, from, bytes
	})
	return p.paper[which], p.paperFrom[which], p.paperBytes[which]
}

// blockBytes is what a block a slide repeats from its layout, master or
// notes master takes from the text budget each time: BlockCost, its text,
// its links, and the cells of a table.
func blockBytes(b *gofastcore.Block) int {
	n := gofastcore.BlockCost + len(b.Text) + len(b.Attrs["href"])
	for _, sp := range b.Spans {
		n += len(sp.Href)
	}
	if b.Table != nil && len(b.Table.Rows) > 0 {
		for _, row := range b.Table.Rows {
			for _, c := range row {
				n += gofastcore.CellCost + len(c)
			}
		}
	}
	return n
}
