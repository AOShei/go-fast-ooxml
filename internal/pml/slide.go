package pml

import (
	"context"
	"errors"
	"sort"
	"strconv"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/dml"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
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

	removed, unmapped, skipped, sawImage := 0, 0, 0, false
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
		pg.read(colors)
		if pg.found != nil && *pg.found != colors && pg.failure == nil && !pg.exceeded && !pg.full {
			// Read again with the colours the slide turned out to have:
			// its text is taken a second time, so the first is given
			// back.
			room.Return(pg.text.Taken)
			pg.text.Taken = 0
			colors = *pg.found
			pg.read(colors)
		}
		blocks := pg.arrange()
		if pg.hidden {
			part.Hidden = gofastcore.HiddenInvisible
		}
		part.Name = pg.title
		res.Unread, res.Programs = pg.unread, pg.programs
		removed, skipped, sawImage, cut = pg.text.Removed, pg.skipped, pg.sawImage, pg.cut
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
				blocks = append(blocks, above...)
				// A layout often repeats what its master has, in the same
				// place; a reader sees it once.
				for i := range l.static {
					if above == nil || !l.repeats[i] {
						blocks = append(blocks, l.static[i])
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
	if skipped > 0 {
		part.Warn(gofastcore.SeverityWarning, warn.HiddenUnchecked,
			strconv.Itoa(skipped)+" shapes were not compared with what lies under or over them: the slide has more overlapping shapes than are checked")
	}
	if removed > 0 {
		part.Warn(gofastcore.SeverityInfo, warn.ControlCharsRemoved, strconv.Itoa(removed))
	}
	if unmapped > 0 {
		part.Warn(gofastcore.SeverityWarning, gofastcore.WarnTextUnmappedGlyphs, warn.Unmapped(unmapped))
	}
	part.MarkHidden()
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
		pg.read(colors)
	}
	if pg.failure != nil {
		p.note(l.name, pg.failure)
	}
	for _, name := range pg.damaged {
		p.note(name, errors.New("damaged"))
	}
	l.cmap, l.show, l.bg, l.phs, l.loose = colors, pg.showMaster, pg.bg, pg.phs, pg.loose
	l.paint = append([]painted(nil), pg.paint...)
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
		text string
		box  gofastcore.Rect
	}
	seen := map[said]bool{}
	kept := l.static[:0]
	for _, b := range l.static {
		if key := (said{b.Text, b.Loc.BBox}); !seen[key] {
			seen[key] = true
			kept = append(kept, b)
		}
	}
	l.static = kept
	// These blocks are copied to every slide that shows them, and the
	// copies share their spans. Invisible characters are marked here,
	// once, so that nothing is added to a block several slides hold.
	l.staticBytes = 0
	for i := range l.static {
		b := &l.static[i]
		l.staticBytes += len(b.Text)
		for _, sp := range b.Spans {
			l.staticBytes += len(sp.Href) // written with every slide, as the text is
		}
		if found := gofastcore.InvisibleChars(b.Text); found != nil {
			b.Spans = append(b.Spans[:len(b.Spans):len(b.Spans)], found...)
			b.Hidden |= gofastcore.HiddenChars
		}
	}
	if l.master != nil && len(l.master.static) > 0 && len(l.static) > 0 {
		above := map[said]bool{}
		for _, b := range l.master.static {
			above[said{b.Text, b.Loc.BBox}] = true
		}
		l.repeats = make([]bool, len(l.static))
		for i, b := range l.static {
			l.repeats[i] = above[said{b.Text, b.Loc.BBox}]
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
	t.Lit, t.Later, t.Styles, t.Link, t.SlideNumber = false, false, false, nil, ""

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
		case (s.Is(ns.Pres, "cm") || modern && (string(s.Local()) == "cm" || string(s.Local()) == "reply")) && len(stack) < 64:
			o := open{depth: s.Depth(), modern: modern}
			if v, ok := s.Attr(xmlscan.NoSpace, "authorId"); ok && len(v) <= 64 {
				o.author = append([]byte(nil), v...)
			}
			date, ok := s.Attr(xmlscan.NoSpace, "dt")
			if !ok {
				date, ok = s.Attr(xmlscan.NoSpace, "created")
			}
			if ok && len(date) <= 64 {
				o.date = append([]byte(nil), date...)
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
const maxAuthors = 1 << 16

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
					if ok1 && ok2 && len(id) <= 64 && len(authors) < maxAuthors {
						authors[string(id)], _ = clean.String(who, clean.Prose)
					}
				}
			})
		}
		p.authors = authors
	})
	return p.authors
}
