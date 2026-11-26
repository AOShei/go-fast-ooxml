package vsd

import (
	"strings"
	"sync"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/unit"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// backEntry is what a background page puts on every page that has it
// behind it, read once.
type backEntry struct {
	once     sync.Once
	blocks   []gofastcore.Block
	bytes    int
	lost     warn.Lost
	damaged  []string
	unread   map[string]int
	sawImage bool
}

// back returns what the background page with the given index puts on the
// pages in front of it: its shapes in reading order, their text a header
// if it starts in the upper half of the background page, a footer if in
// the lower. Its own background pages are not in it.
func (d *drawing) back(index int) *backEntry {
	ref := &d.pages[index]
	d.mu.Lock()
	e := d.backs[ref.id]
	if e == nil {
		e = &backEntry{}
		d.backs[ref.id] = e
	}
	d.mu.Unlock()
	e.once.Do(func() {
		if ref.target == "" || !d.pkg.Has(ref.target) {
			return
		}
		bp := &page{d: d}
		bp.reset(ref, nil)
		bp.content(ref)
		e.blocks = bp.arrange()
		for i := range e.blocks {
			b := &e.blocks[i]
			if b.Kind == gofastcore.KindImage {
				continue
			}
			b.Kind, b.Marker = gofastcore.KindFooter, ""
			if half := bp.height / 2; bp.height <= 0 || b.Loc.BBox.Y0 < half {
				b.Kind = gofastcore.KindHeader
			}
			e.bytes += len(b.Text)
		}
		e.lost.Merge(&bp.lost)
		e.damaged, e.unread, e.sawImage = bp.damaged, bp.unread, bp.sawImage
	})
	return e
}

// background appends what the page's background pages put on it: the
// one behind it, then the one behind that, in turn.
func (pg *page) background(blocks []gofastcore.Block, ref *pageRef, part *gofastcore.Part, res *unit.Result) []gofastcore.Block {
	d := pg.d
	seen := map[int64]bool{ref.id: true}
	for b, hops := ref.backPage, 0; b >= 0; hops++ {
		index, ok := d.byID[b]
		if !ok {
			break
		}
		if seen[b] || hops >= maxBackPages {
			pg.lost.Add(warn.CapBackPages, 1)
			break
		}
		seen[b] = true
		e := d.back(index)
		pg.damaged = append(pg.damaged, e.damaged...)
		pg.lost.Merge(&e.lost)
		pg.sawImage = pg.sawImage || e.sawImage
		switch n := int64(len(e.blocks)); {
		case n == 0:
		case d.repeated.Add(-n) < 0:
			d.repeated.Add(n)
			pg.repeat = true
		case !pg.room.Take(e.bytes):
			pg.full = true
		default:
			for _, blk := range e.blocks {
				if blk.Image != nil {
					img := *blk.Image
					blk.Image = &img
				}
				blocks = append(blocks, blk)
			}
		}
		b = d.pages[index].backPage
	}
	return blocks
}

// comment is a comment on a page.
type comment struct {
	author, date, text string
}

// maxCommentAuthors bounds the authors of comments that are kept.
const maxCommentAuthors = warn.MaxCommentAuthors

// readComments reads the drawing's comments, by the page they are on.
func (d *drawing) readComments() {
	d.comments = map[int64][]comment{}
	authors := map[string]string{}
	n := 0
	d.load(d.comment, func(s *xmlscan.Scanner) {
		for s.Next() != xmlscan.EOF {
			if s.Kind() != xmlscan.Start {
				continue
			}
			switch {
			case s.Depth() <= 2 && s.Space() == ns.Visio && (s.Depth() == 1 || string(s.Local()) == "AuthorList" || string(s.Local()) == "CommentList"):
			case s.Is(ns.Visio, "AuthorEntry"):
				id, _ := s.Attr(xmlscan.NoSpace, "ID")
				name, _ := s.Attr(xmlscan.NoSpace, "Name")
				if len(authors) < maxCommentAuthors {
					authors[string(id)] = clean.Name(string(name))
				} else {
					d.lost.Add(warn.CapCommentAuthors, 1)
				}
				s.Skip()
			case s.Is(ns.Visio, "CommentEntry"):
				page := intAttr(s, "PageID", -1)
				author, _ := s.Attr(xmlscan.NoSpace, "AuthorID")
				date, _ := s.Attr(xmlscan.NoSpace, "Date")
				c := comment{author: authors[string(author)], date: clean.Label(string(date))}
				var text []byte
				depth := s.Depth()
				for s.Next() != xmlscan.EOF && s.Depth() >= depth {
					if s.Kind() == xmlscan.Text {
						text = append(text, s.Text()...)
					}
				}
				var removed int
				c.text, removed = clean.String(text, clean.Prose)
				_ = removed
				if c.text != "" && n < warn.MaxComments {
					d.comments[page] = append(d.comments[page], c)
					n++
				}
			default:
				s.Skip()
			}
		}
	})
}

// comments appends the comments on the page with the given ID, as notes.
func (pg *page) comments(blocks []gofastcore.Block, id int64) []gofastcore.Block {
	d := pg.d
	if d.comment == "" {
		return blocks
	}
	d.commentOnce.Do(d.readComments)
	for _, c := range d.comments[id] {
		b := gofastcore.Block{Kind: gofastcore.KindNote, Text: c.text, Attrs: map[string]string{gofastcore.AttrNoteType: gofastcore.NoteComment}}
		if c.author != "" {
			b.Attrs[gofastcore.AttrAuthor] = c.author
		}
		if c.date != "" && !strings.ContainsAny(c.date, "\n") {
			b.Attrs[gofastcore.AttrDate] = c.date
		}
		if !pg.room.Take(len(b.Text)) {
			pg.full = true
			break
		}
		blocks = append(blocks, b)
	}
	return blocks
}
