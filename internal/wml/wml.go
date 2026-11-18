// Package wml reads WordprocessingML, the format of Word documents, into
// the blocks of the shared document model.
package wml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // image sizes are read from the file header,
	_ "image/jpeg" // which needs the format registered
	_ "image/png"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// Warning codes raised by this package beyond the shared ones in
// gofastcore. Each states a fact.
const (
	// WarnUnreadContent: the document holds content of a kind this version
	// does not read: a chart, a diagram.
	WarnUnreadContent = "ooxml.unread_content"
	// WarnControlCharsRemoved: see the package gofastooxml.
	WarnControlCharsRemoved = "ooxml.control_characters_removed"
	// WarnPartDamaged: a supporting part (styles, numbering, notes, a
	// header) could not be read to its end; what was read is used.
	WarnPartDamaged = "ooxml.part_damaged"
	// WarnTableFlattened: a table whose declared grid is far larger than
	// the cells it holds was written as lines of text, cells separated by
	// tabs, instead of as a table.
	WarnTableFlattened = "ooxml.table_flattened"
)

var scanOptions = &xmlscan.Options{Spaces: ns.Spaces}

// note is a footnote, endnote or comment read ahead of the body.
type note struct {
	text   string
	author string
	date   string
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
	headers, footers    []string        // parts named by section properties, in order
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
	r := &reader{
		ctx: ctx, pkg: pkg, opts: opts, doc: doc,
		part:          &gofastcore.Part{Kind: gofastcore.PartBody, Number: 1},
		seenFurniture: map[string]bool{},
		usedNotes:     map[string]bool{},
		images:        map[string]*gofastcore.Image{},
		delivered:     map[string]bool{},
		children:      map[string]bool{},
		unread:        map[string]int{},
	}
	rels, err := pkg.Rels(main)
	r.noteErr(opc.RelsName(main), err)
	byID := make(map[string]opc.Rel, len(rels))
	for _, rel := range rels {
		byID[rel.ID] = rel
		switch {
		case rel.External:
			if !ns.IsRel(rel.Type, ns.RelHyperlink) {
				r.external++
			}
		case ns.IsRel(rel.Type, ns.RelStyles):
			r.load(rel.Target, func(s *xmlscan.Scanner) { r.styles = readStyles(s) })
		case ns.IsRel(rel.Type, ns.RelNumbering):
			r.load(rel.Target, func(s *xmlscan.Scanner) { r.numbering = readNumbering(s) })
		}
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

	// Running headers go first and footers last, each distinct one once.
	for _, name := range r.headers {
		r.part.Blocks = append(r.part.Blocks, r.furniture(name, gofastcore.KindHeader)...)
	}
	r.part.Blocks = append(r.part.Blocks, body...)
	for _, name := range r.footers {
		r.part.Blocks = append(r.part.Blocks, r.furniture(name, gofastcore.KindFooter)...)
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
				n.author, n.date = p.clean(author, clean.Trim), p.clean(date, clean.Trim)
			}
			var blocks []gofastcore.Block
			p.container(func(b gofastcore.Block) { blocks = append(blocks, b) })
			n.text = flatten(blocks)
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

// furniture reads a header or footer part into blocks of the given kind:
// one per paragraph, with a table flattened to text.
func (r *reader) furniture(name string, kind gofastcore.Kind) []gofastcore.Block {
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
	r.loadWith(name, &p.s, func() {
		// Up to the root element; a line end after the XML declaration
		// comes first as text.
		for k := p.s.Next(); k != xmlscan.Start; k = p.s.Next() {
			if k == xmlscan.EOF {
				return
			}
		}
		p.container(func(b gofastcore.Block) {
			switch b.Kind {
			case gofastcore.KindImage:
				out = append(out, b)
			default:
				if text := flatten([]gofastcore.Block{b}); text != "" {
					out = append(out, gofastcore.Block{Kind: kind, Text: text})
				}
			}
		})
		for _, b := range p.after {
			if b.Kind == gofastcore.KindImage {
				out = append(out, b)
			} else if text := flatten([]gofastcore.Block{b}); text != "" {
				out = append(out, gofastcore.Block{Kind: kind, Text: text})
			}
		}
	})
	return out
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
			var tb strings.Builder
			for r, row := range b.Table.Rows {
				if r > 0 {
					tb.WriteByte('\n')
				}
				for c, cell := range row {
					if c > 0 {
						tb.WriteByte('\t')
					}
					tb.WriteString(strings.ReplaceAll(cell, "\n", " "))
				}
			}
			text = strings.TrimSpace(tb.String())
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
	img := &gofastcore.Image{ID: clean.Label(r.pkg.Written(name)), MIME: clean.Label(r.pkg.ContentType(name))}
	r.images[name] = img
	if r.budget || !r.pkg.Has(name) {
		return img
	}
	rc, err := r.pkg.Open(name)
	if err != nil {
		return img
	}
	defer rc.Close()
	if r.opts.Images == gofastcore.ImagesData {
		data, err := io.ReadAll(rc)
		if err != nil {
			r.noteErr(name, err)
			return img
		}
		img.Data = data
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
			img.Width, img.Height = cfg.Width, cfg.Height
		}
		return img
	}
	// Only the header is needed for the size.
	if cfg, _, err := image.DecodeConfig(io.LimitReader(rc, 1<<20)); err == nil {
		img.Width, img.Height = cfg.Width, cfg.Height
	} else if errors.Is(err, opc.ErrBudget) {
		r.budget = true
	}
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
	for _, rel := range rels {
		if rel.External || !ns.IsRel(rel.Type, ns.RelOLEObject, ns.RelPackage, ns.RelAltChunk) {
			continue
		}
		if r.children[rel.Target] || !r.pkg.Has(rel.Target) {
			continue
		}
		r.children[rel.Target] = true
		name, mime, size := clean.Label(path.Base(r.pkg.Written(rel.Target))), clean.Label(r.pkg.ContentType(rel.Target)), r.pkg.Size(rel.Target)
		if r.opts.Child == nil || r.budget {
			// Listed, not read: there is nobody to read it.
			r.doc.Children = append(r.doc.Children, r.opts.ExtractChild(r.ctx, name, mime, nil, size))
			continue
		}
		rc, err := r.pkg.Open(rel.Target)
		if err != nil {
			r.noteErr(rel.Target, err)
			continue
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			r.noteErr(rel.Target, err)
			if errors.Is(err, opc.ErrBudget) {
				continue
			}
		}
		r.doc.Children = append(r.doc.Children, r.opts.ExtractChild(r.ctx, name, mime, bytes.NewReader(data), int64(len(data))))
	}
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
}
