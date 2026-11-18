// Package wml reads WordprocessingML, the format of Word documents, into
// the blocks of the shared document model.
package wml

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/dml"
	"github.com/AOShei/go-fast-ooxml/internal/media"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// Warning codes raised by this package beyond the shared ones in
// gofastcore. Each states a fact.
const (
	// WarnUnreadContent: the document holds content of a kind this version
	// does not read: a chart, a diagram.
	WarnUnreadContent = warn.UnreadContent
	// WarnControlCharsRemoved: see the package gofastooxml.
	WarnControlCharsRemoved = warn.ControlCharsRemoved
	// WarnPartDamaged: a supporting part (styles, numbering, notes, a
	// header) could not be read to its end; what was read is used.
	WarnPartDamaged = warn.PartDamaged
	// WarnTableFlattened: a table whose declared grid is far larger than
	// the cells it holds was written as lines of text, cells separated by
	// tabs, instead of as a table.
	WarnTableFlattened = warn.TableFlattened
)

// HiddenChecked is the set of hidden-text reasons this reader checks
// completely. Clipping (a text box or table row too small for its text)
// and transparency are flagged where they are found, but only some forms
// of them are looked for, so they are not listed. Text hidden by its place
// on the page (off the page, under a shape) is not looked for at all: this
// reader does no layout.
const HiddenChecked = gofastcore.HiddenInvisible | gofastcore.HiddenTiny | gofastcore.HiddenLowContrast | gofastcore.HiddenChars

var scanOptions = &xmlscan.Options{Spaces: ns.Spaces}

// note is a footnote, endnote or comment read ahead of the body.
type note struct {
	text   string
	spans  []gofastcore.Span
	hidden gofastcore.Hidden
	author string
	date   string
}

// furnitureRef is a header or footer named by a section.
type furnitureRef struct {
	name   string
	header bool
	// shown says some section displays it: it is the section's ordinary
	// header, or its first-page header and the section has a different
	// first page. even marks an even-page header, which is shown only if
	// the document has different odd and even pages.
	shown, even bool
}

// reader holds what is shared while one Word document is read.
type reader struct {
	ctx  context.Context
	pkg  *opc.Package
	opts *gofastcore.Options
	doc  *gofastcore.Document
	part *gofastcore.Part

	styles    *styles
	numbering *numbering
	num       numState
	footnotes map[int]note
	endnotes  map[int]note
	comments  map[int]note

	footnoteN, endnoteN int
	usedNotes           map[string]bool // notes already emitted: each appears once
	furniture           []furnitureRef  // headers and footers named by sections, in order
	firstPage           [2]int          // the latest first-page footer and header named: index in furniture, plus one
	evenAndOdd          bool            // the document has different odd and even pages
	showBackground      bool            // the page colour is displayed
	theme               map[string]color
	drawn               dml.Palette // resolves the colours of drawings
	page                color       // the page background
	seenFurniture       map[string]bool
	images              map[string]*gofastcore.Image // by part name, to share one read
	delivered           map[string]bool              // image data already given once
	children            map[string]bool              // embedded parts already handed on

	inserted, deleted int  // tracked changes seen
	removed           int  // control characters removed
	unmapped          int  // symbol-font characters with no known Unicode equivalent
	flattened         int  // tables written as text
	clamped           int  // headings deeper than six levels
	sawImage          bool // the body has a picture, whether or not pictures are extracted
	unread            map[string]int
	external          int
	budget            bool // the decompression budget ran out
}

// Extract reads the Word document whose main part is named main into doc,
// which already carries its source and properties. It appends one body
// part.
//
// It does not return an error for a damaged or over-budget document: what
// could be read is in the part, and warnings say what could not.
func Extract(ctx context.Context, pkg *opc.Package, main string, opts *gofastcore.Options, doc *gofastcore.Document) {
	r := &reader{drawn: dml.Palette{Map: dml.DefaultMap},
		ctx: ctx, pkg: pkg, opts: opts, doc: doc,
		part:          &gofastcore.Part{Kind: gofastcore.PartBody, Number: 1},
		seenFurniture: map[string]bool{},
		usedNotes:     map[string]bool{},
		images:        map[string]*gofastcore.Image{},
		delivered:     map[string]bool{},
		children:      map[string]bool{},
		unread:        map[string]int{},
		page:          white,
	}
	rels, err := pkg.Rels(main)
	r.noteErr(opc.RelsName(main), err)
	byID := make(map[string]opc.Rel, len(rels))
	stylesPart := ""
	// The theme first: styles name its colours.
	for _, rel := range rels {
		if !rel.External && ns.IsRel(rel.Type, ns.RelTheme) {
			r.load(rel.Target, func(s *xmlscan.Scanner) { r.theme, r.drawn.Theme = readTheme(s) })
		}
	}
	for _, rel := range rels {
		byID[rel.ID] = rel
		switch {
		case rel.External:
			if !ns.IsRel(rel.Type, ns.RelHyperlink) {
				r.external++
			}
		case ns.IsRel(rel.Type, ns.RelStyles):
			stylesPart = rel.Target
		case ns.IsRel(rel.Type, ns.RelNumbering):
			r.load(rel.Target, func(s *xmlscan.Scanner) { r.numbering = readNumbering(s) })
		}
	}
	if stylesPart != "" {
		r.load(stylesPart, func(s *xmlscan.Scanner) { r.styles = readStyles(s, r.theme) })
	}
	// Notes are read after styles and numbering, which their paragraphs use.
	for _, rel := range rels {
		switch {
		case rel.External:
		case ns.IsRel(rel.Type, ns.RelFootnotes):
			r.footnotes = r.readNotes(rel.Target, "footnote")
		case ns.IsRel(rel.Type, ns.RelEndnotes):
			r.endnotes = r.readNotes(rel.Target, "endnote")
		case ns.IsRel(rel.Type, ns.RelComments):
			r.comments = r.readNotes(rel.Target, "comment")
		case ns.IsRel(rel.Type, ns.RelSettings):
			r.load(rel.Target, func(s *xmlscan.Scanner) {
				for s.Next() != xmlscan.EOF {
					if s.Kind() == xmlscan.Start && s.Depth() == 2 {
						switch {
						case s.Is(ns.Word, "evenAndOddHeaders"):
							r.evenAndOdd = onOff(s)
						case s.Is(ns.Word, "displayBackgroundShape"):
							r.showBackground = onOff(s)
						}
						s.Skip()
					}
				}
			})
			// A template attached by address is the usual remote reference.
			settings, err := pkg.Rels(rel.Target)
			r.noteErr(opc.RelsName(rel.Target), err)
			for _, s := range settings {
				if s.External {
					r.external++
				}
			}
		}
	}

	p := r.newParser(main, byID)
	var body []gofastcore.Block
	if c, err := pkg.Scan(main, &p.s, scanOptions); err != nil {
		r.noteErr(main, err)
		r.part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, "the main part could not be opened")
	} else {
		p.document(&body)
		c.Close()
		if err := p.s.Err(); err != nil {
			r.bodyErr(err)
		}
	}

	// A header or footer part that no section names is in the file but
	// never on a page. It is read all the same, and flagged.
	for _, rel := range rels {
		header := ns.IsRel(rel.Type, ns.RelHeader)
		if rel.External || !header && !ns.IsRel(rel.Type, ns.RelFooter) {
			continue
		}
		r.furniture = append(r.furniture, furnitureRef{name: rel.Target, header: header})
	}
	// A part is shown if any section shows it.
	shown := map[string]bool{}
	for _, f := range r.furniture {
		if f.shown && (!f.even || r.evenAndOdd) {
			shown[f.name] = true
		}
	}
	// Running headers go first and footers last, each distinct one once.
	for _, f := range r.furniture {
		if f.header {
			r.part.Blocks = append(r.part.Blocks, r.readFurniture(f.name, gofastcore.KindHeader, shown[f.name])...)
		}
	}
	r.part.Blocks = append(r.part.Blocks, body...)
	for _, f := range r.furniture {
		if !f.header {
			r.part.Blocks = append(r.part.Blocks, r.readFurniture(f.name, gofastcore.KindFooter, shown[f.name])...)
		}
	}
	r.embedded(rels)
	r.finish()
}

// load reads a supporting part. Damage to it is reported and what was read
// before the damage is used.
func (r *reader) load(name string, read func(*xmlscan.Scanner)) {
	if r.budget || !r.pkg.Has(name) {
		return
	}
	var s xmlscan.Scanner
	c, err := r.pkg.Scan(name, &s, scanOptions)
	if err != nil {
		r.noteErr(name, err)
		return
	}
	read(&s)
	c.Close()
	r.noteErr(name, s.Err())
}

// noteErr records a failure to read a supporting part.
func (r *reader) noteErr(name string, err error) {
	switch {
	case err == nil:
	case errors.Is(err, opc.ErrBudget):
		r.budget = true
	default:
		r.doc.Warn(gofastcore.SeverityWarning, WarnPartDamaged, clean.Label(name))
	}
}

// bodyErr records why the body ended early. The blocks read before that
// point are kept.
func (r *reader) bodyErr(err error) {
	switch {
	case errors.Is(err, opc.ErrBudget):
		r.budget = true
	case errors.Is(err, xmlscan.ErrDepth), errors.Is(err, xmlscan.ErrTokenTooLarge):
		r.part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the document body is cut off: "+clean.Label(err.Error()))
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		// The caller is told by Extract's return value.
	default:
		r.part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, "the document body is cut off: "+clean.Label(err.Error()))
	}
}

// readNotes reads a footnotes, endnotes or comments part into a table by
// id. Each note's paragraphs are joined with line feeds.
func (r *reader) readNotes(name, element string) map[int]note {
	notes := map[int]note{}
	rels, err := r.pkg.Rels(name)
	r.noteErr(opc.RelsName(name), err)
	byID := make(map[string]opc.Rel, len(rels))
	for _, rel := range rels {
		byID[rel.ID] = rel
	}
	p := r.newParser(name, byID)
	r.loadWith(name, &p.s, func() {
		s := &p.s
		for s.Next() != xmlscan.EOF {
			if s.Kind() != xmlscan.Start || s.Depth() != 2 {
				continue
			}
			if !s.Is(ns.Word, element) {
				s.Skip()
				continue
			}
			id, ok := intAttr(s, "id")
			typ, _ := s.Attr(ns.Word, "type")
			if !ok || len(typ) > 0 && string(typ) != "normal" {
				s.Skip() // separators are layout, not notes
				continue
			}
			var n note
			if element == "comment" {
				author, _ := s.Attr(ns.Word, "author")
				date, _ := s.Attr(ns.Word, "date")
				n.author, n.date = p.clean(author, clean.Prose), p.clean(date, clean.Trim)
			}
			var blocks []gofastcore.Block
			p.container(func(b gofastcore.Block) { blocks = append(blocks, b) })
			n.text, n.spans, n.hidden = join(blocks)
			p.after = p.after[:0] // pictures and notes inside a note are not carried over
			notes[id] = n
		}
	})
	return notes
}

func (r *reader) loadWith(name string, s *xmlscan.Scanner, read func()) {
	if r.budget || !r.pkg.Has(name) {
		return
	}
	c, err := r.pkg.Scan(name, s, scanOptions)
	if err != nil {
		r.noteErr(name, err)
		return
	}
	read()
	c.Close()
	r.noteErr(name, s.Err())
}

// readFurniture reads a header or footer part into blocks of the given
// kind: one per paragraph, with a table flattened to text. One that no
// page shows is flagged as invisible.
func (r *reader) readFurniture(name string, kind gofastcore.Kind, shown bool) []gofastcore.Block {
	if r.seenFurniture[name] {
		return nil
	}
	r.seenFurniture[name] = true
	rels, err := r.pkg.Rels(name)
	r.noteErr(opc.RelsName(name), err)
	byID := make(map[string]opc.Rel, len(rels))
	for _, rel := range rels {
		byID[rel.ID] = rel
	}
	p := r.newParser(name, byID)
	var out []gofastcore.Block
	add := func(b gofastcore.Block) {
		if b.Kind == gofastcore.KindImage {
			out = append(out, b)
			return
		}
		text, spans, hidden := join([]gofastcore.Block{b})
		if text == "" {
			return
		}
		block := gofastcore.Block{Kind: kind, Text: text, Spans: spans, Hidden: hidden}
		if !shown {
			// The whole of it is unseen, whatever else is true of parts.
			hideWhole(&block, gofastcore.HiddenInvisible)
		}
		out = append(out, block)
	}
	r.loadWith(name, &p.s, func() {
		// Up to the root element; a line end after the XML declaration
		// comes first as text.
		for k := p.s.Next(); k != xmlscan.Start; k = p.s.Next() {
			if k == xmlscan.EOF {
				return
			}
		}
		p.container(add)
		for _, b := range p.after {
			add(b)
		}
	})
	return out
}

// join gives the text of blocks as it is written into a note or a header,
// as flatten does, and carries their formatting and hidden reasons along:
// spans are moved to their place in the joined text, and a reason that
// held for one whole block becomes a span over that block's text. A reason
// that then covers all of the joined text is a reason of the whole.
func join(blocks []gofastcore.Block) (string, []gofastcore.Span, gofastcore.Hidden) {
	if len(blocks) == 1 && blocks[0].Table == nil && blocks[0].Marker == "" && blocks[0].Kind != gofastcore.KindImage {
		return blocks[0].Text, blocks[0].Spans, blocks[0].Hidden
	}
	var sb strings.Builder
	var spans []gofastcore.Span
	for i := range blocks {
		b := &blocks[i]
		if b.Kind == gofastcore.KindImage {
			continue
		}
		text := flatten(blocks[i : i+1])
		if text == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		at := sb.Len()
		sb.WriteString(text)
		if b.Table != nil {
			// What is said of the table's cells is said of their text.
			_, cells := tableText(b.Table)
			if whole := b.HiddenWhole(); whole != 0 {
				spans = append(spans, gofastcore.Span{Start: at, End: at + len(text), Hidden: whole})
			}
			for _, sp := range cells {
				sp.Start, sp.End = sp.Start+at, sp.End+at
				spans = append(spans, sp)
			}
			continue
		}
		// A label written in front of the text shifts its spans.
		shift := at + len(text) - len(b.Text)
		if whole := b.HiddenWhole(); whole != 0 {
			spans = append(spans, gofastcore.Span{Start: shift, End: at + len(text), Hidden: whole})
		}
		for _, sp := range b.Spans {
			sp.Start, sp.End = sp.Start+shift, sp.End+shift
			spans = append(spans, sp)
		}
	}
	text := sb.String()
	settled, hidden := settle(spans, 0, len(text), []byte(text))
	return text, settled, hidden
}

// tableText gives a table as text, rows on lines and cells separated by
// tabs, and the parts of that text that are in cells marked as hidden.
func tableText(t *gofastcore.Table) (string, []gofastcore.Span) {
	var hidden map[[2]int]gofastcore.Hidden
	for _, h := range t.Hidden {
		if hidden == nil {
			hidden = map[[2]int]gofastcore.Hidden{}
		}
		for r := h.Row; r < h.Row+h.RowSpan && r < len(t.Rows); r++ {
			for c := h.Col; c < h.Col+h.ColSpan && c < len(t.Rows[r]); c++ {
				hidden[[2]int{r, c}] |= h.Hidden
			}
		}
	}
	var tb strings.Builder
	var spans []gofastcore.Span
	for r, row := range t.Rows {
		if r > 0 {
			tb.WriteByte('\n')
		}
		for c, cell := range row {
			if c > 0 {
				tb.WriteByte('\t')
			}
			at := tb.Len()
			tb.WriteString(strings.ReplaceAll(cell, "\n", " "))
			if reasons := hidden[[2]int{r, c}]; reasons != 0 && tb.Len() > at {
				spans = append(spans, gofastcore.Span{Start: at, End: tb.Len(), Hidden: reasons})
			}
		}
	}
	// The text is trimmed, and the spans moved to match.
	full := tb.String()
	lead := len(full) - len(strings.TrimLeftFunc(full, unicode.IsSpace))
	text := strings.TrimSpace(full)
	kept := spans[:0]
	for _, sp := range spans {
		sp.Start, sp.End = max(sp.Start-lead, 0), min(sp.End-lead, len(text))
		if sp.Start < sp.End {
			kept = append(kept, sp)
		}
	}
	return text, kept
}

// flatten gives the text of blocks as it is written into a table cell, a
// note or a header: paragraphs on lines of their own, list labels in
// front, a table's cells separated by tabs.
func flatten(blocks []gofastcore.Block) string {
	if len(blocks) == 1 && blocks[0].Table == nil && blocks[0].Marker == "" {
		return blocks[0].Text
	}
	var sb strings.Builder
	for i := range blocks {
		b := &blocks[i]
		var text string
		switch {
		case b.Table != nil:
			text, _ = tableText(b.Table)
		case b.Kind == gofastcore.KindImage:
			continue
		case b.Marker != "" && b.Kind != gofastcore.KindFootnote:
			text = b.Marker + " " + b.Text
		default:
			text = b.Text
		}
		if text == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(text)
	}
	return sb.String()
}

// image returns the description of an image part, reading its size, and
// its bytes if asked for, the first time the part is seen.
func (r *reader) image(name string) *gofastcore.Image {
	if img, ok := r.images[name]; ok {
		return img
	}
	var img *gofastcore.Image
	if r.budget {
		img = &gofastcore.Image{ID: clean.Label(r.pkg.Written(name)), MIME: clean.Label(r.pkg.ContentType(name))}
	} else {
		var err error
		img, err = media.Image(r.pkg, name, r.opts.Images)
		r.noteErr(name, err)
	}
	r.images[name] = img
	return img
}

// imageBlock makes the block for one use of an image. The bytes go with
// the first use only.
func (r *reader) imageBlock(name, alt string) gofastcore.Block {
	img := *r.image(name)
	if r.delivered[name] {
		img.Data = nil
	}
	r.delivered[name] = true
	return gofastcore.Block{Kind: gofastcore.KindImage, Text: alt, Image: &img}
}

// embedded hands the files embedded in the document to the caller's child
// extractor: other Office files, and objects inserted from other programs.
func (r *reader) embedded(rels []opc.Rel) {
	children, damaged, err := media.Embedded(r.ctx, r.pkg, r.opts, rels, r.children, !r.budget)
	r.doc.Children = append(r.doc.Children, children...)
	for _, name := range damaged {
		r.doc.Warn(gofastcore.SeverityWarning, WarnPartDamaged, clean.Label(name))
	}
	r.noteErr("", err)
}

// finish adds the warnings that describe the document as a whole.
func (r *reader) finish() {
	part := r.part
	hasText, hasImage := false, false
	for i := range part.Blocks {
		switch b := &part.Blocks[i]; {
		case b.Kind == gofastcore.KindImage:
			hasImage = true
		case b.Text != "" || b.Table != nil:
			hasText = true
		}
	}
	if r.budget {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the decompressed size limit was reached; the document is cut off there")
	}
	if !hasText && !part.Has(gofastcore.WarnPartFailed) {
		switch {
		case hasImage || r.sawImage:
			part.Warn(gofastcore.SeverityWarning, gofastcore.WarnPartImageOnly, "")
		case r.budget:
			part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, "nothing was read before the limit")
		default:
			part.Warn(gofastcore.SeverityInfo, gofastcore.WarnPartNoText, "")
		}
	}
	if r.inserted+r.deleted > 0 {
		part.Warn(gofastcore.SeverityInfo, gofastcore.WarnTextTrackedChanges,
			strconv.Itoa(r.inserted)+" insertions, "+strconv.Itoa(r.deleted)+" deletions; text is given with all changes accepted")
	}
	if r.removed > 0 {
		part.Warn(gofastcore.SeverityInfo, WarnControlCharsRemoved, strconv.Itoa(r.removed))
	}
	if r.unmapped > 0 {
		part.Warn(gofastcore.SeverityWarning, gofastcore.WarnTextUnmappedGlyphs,
			strconv.Itoa(r.unmapped)+" characters of a symbol font this version cannot translate appear as U+FFFD")
	}
	if r.clamped > 0 {
		part.Warn(gofastcore.SeverityInfo, gofastcore.WarnHeadingLevelClamped, strconv.Itoa(r.clamped)+" headings deeper than level 6")
	}
	if r.flattened > 0 {
		part.Warn(gofastcore.SeverityWarning, WarnTableFlattened, strconv.Itoa(r.flattened))
	}
	if r.external > 0 {
		r.doc.Warn(gofastcore.SeverityWarning, gofastcore.WarnInputExternalReference, fmt.Sprintf("%d, not counting hyperlinks; nothing was fetched", r.external))
	}
	if len(r.unread) > 0 {
		var kinds []string
		for _, k := range []string{"chart", "diagram"} {
			if n := r.unread[k]; n > 0 {
				kinds = append(kinds, strconv.Itoa(n)+" "+k)
			}
		}
		r.doc.Warn(gofastcore.SeverityWarning, WarnUnreadContent, strings.Join(kinds, ", "))
	}
	part.MarkHidden()
	r.doc.Parts = append(r.doc.Parts, *part)
	r.doc.HiddenChecked = HiddenChecked
}
