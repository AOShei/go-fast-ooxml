package wml

import (
	"bytes"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-core/charset"
	"github.com/AOShei/go-fast-core/clean"
	"github.com/AOShei/go-fast-core/hidden"
	"github.com/AOShei/go-fast-core/symbol"
	"github.com/AOShei/go-fast-core/xmlscan"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/omml"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
)

// parser reads the block-level content of one part: the document body, a
// footnotes part, a header. Text boxes and table cells nest, so its
// methods call each other; the depth is bounded by the tokenizer's limit
// on element nesting.
type parser struct {
	r    *reader
	s    xmlscan.Scanner
	name string             // the part being read
	rels map[string]opc.Rel // its relationships, by id

	text    []byte             // text of the paragraphs being built, innermost last
	spans   []gofastcore.Span  // their spans, offsets counted from each paragraph's start
	scratch []byte             // for cleaning a piece of text
	symbols []byte             // for translating text in a symbol font
	para    paraState          // the paragraph being read
	after   []gofastcore.Block // blocks that follow the paragraph or table in progress

	// code holds the text of the block of code being joined by emitBody,
	// which is codeIn's block codeAt: each paragraph is appended, not the
	// whole block copied again.
	code   strings.Builder
	codeIn *[]gofastcore.Block
	codeAt int

	href      string     // target of the hyperlink being read, when links are asked for
	hrefDepth int        // depth of its element
	fields    []field    // the fields being read, innermost last
	deep      int        // fields begun past maxFieldDepth and not yet ended
	fieldHref string     // target of the HYPERLINK field whose result is being read
	cell      *cellProps // properties of the table cell being read, if any
	row       *rowProps

	tables   int  // tables being read; notes wait until the outermost is out
	boxes    int  // text boxes being read
	body     bool // the parser reads the main body, where a horizontal line is a thematic break
	produced int  // text and images emitted so far, for alternate content
	alts     []alternate
	polls    int
}

// field is a field being read: its code (the instruction, such as
// MERGEFIELD Name), then its result, which is what the document shows.
type field struct {
	code   []byte
	inCode bool   // its separator, or its end, has not come yet
	nested bool   // it began in the code of the field around it
	href   string // where its result leads, for a HYPERLINK field
	cut    bool   // its code was longer than maxFieldCode
}

// Bounds on the fields kept track of.
const (
	maxFieldDepth = warn.MaxFieldDepth
	maxFieldCode  = warn.MaxFieldCode
)

// inCode reports whether the parser is in the code of a field, where
// nothing is shown: the result of a field nested in that code is not.
func (p *parser) inCode() bool {
	for i := range p.fields {
		if p.fields[i].inCode {
			return true
		}
	}
	return false
}

// fieldChar reads w:fldChar: a field begins, its code ends, or it ends.
// The code of a field is never shown; it is kept as a note after the
// paragraph its field ends in (fieldNote). A field in the code of another
// is part of that one's code, written in braces as Word shows it.
func (p *parser) fieldChar() {
	typ, _ := p.s.Attr(ns.Word, "fldCharType")
	switch string(typ) {
	case "begin":
		// A field nested deeper than the bound is not kept apart: its
		// code and its result are read as the field around it reads
		// them, and its end ends it, not that one.
		if p.deep > 0 || len(p.fields) >= maxFieldDepth {
			p.deep++
			p.r.lost.Add(warn.CapFieldDepth, 1)
			return
		}
		nested := p.inCode()
		p.fields = append(p.fields, field{inCode: true, nested: nested})
	case "separate":
		if p.deep > 0 {
			return
		}
		if n := len(p.fields); n > 0 && p.fields[n-1].inCode {
			f := &p.fields[n-1]
			f.inCode = false
			if p.r.opts.Spans {
				f.href = p.fieldLink(f.code)
			}
		}
	case "end":
		if p.deep > 0 {
			p.deep--
			return
		}
		n := len(p.fields)
		if n == 0 {
			return
		}
		f := p.fields[n-1]
		p.fields = p.fields[:n-1]
		code := bytes.TrimSpace(f.code)
		switch {
		case len(code) == 0:
			if f.cut {
				p.r.lost.Add(warn.CapFieldCode, 1)
			}
		case f.nested && n > 1 && p.fields[n-2].inCode:
			// The code of a nested field is part of the code of the one
			// around it, and is cut with it.
			outer := &p.fields[n-2]
			outer.code = append(append(append(outer.code, "{ "...), code...), " }"...)
			outer.cut = outer.cut || f.cut
			cutCode(outer)
		default:
			if f.cut {
				p.r.lost.Add(warn.CapFieldCode, 1)
			}
			p.fieldNote(code)
		}
	}
	p.fieldHref = ""
	for i := len(p.fields) - 1; i >= 0; i-- {
		if f := &p.fields[i]; !f.inCode && f.href != "" {
			p.fieldHref = f.href
			break
		}
	}
}

// cutCode cuts the code of f to maxFieldCode bytes, at the start of a
// character, if it is longer, and marks it cut.
func cutCode(f *field) {
	if len(f.code) <= maxFieldCode {
		return
	}
	f.code, f.cut = charset.CutUTF8(f.code, maxFieldCode), true
}

// fieldNote keeps the code of a field as a note after the paragraph: every
// code, whatever field it is, from a page number's to an index entry's or
// the branches of IF. A code is stored data, never shown, and is marked
// so (StoredFieldCode), in its place: the renderers leave it out unless
// asked for stored data. The file declares nothing about its display, and
// no reason is set.
func (p *parser) fieldNote(code []byte) {
	text := p.clean(code, clean.Prose)
	if text == "" {
		return
	}
	p.after = append(p.after, gofastcore.Block{Kind: gofastcore.KindNote, Text: text,
		Attrs: map[string]string{gofastcore.AttrNoteType: gofastcore.NoteField, gofastcore.AttrStored: gofastcore.StoredFieldCode}})
}

// alternate tracks one mc:AlternateContent element: a file may hold the
// same content twice, once for new readers and once for old, and only one
// may be read.
type alternate struct {
	depth    int // depth of the AlternateContent element
	start    int // value of produced when it began
	produced bool
}

type paraProps struct {
	style   *style
	styleID string
	numID   int  // -1 not set, 0 none
	ilvl    int8 // -1 not set
	outline int8 // -1 not set, 9 body text
}

// paraState is what the runs of the paragraph being read need to know.
type paraState struct {
	start     int                // where its text begins in parser.text
	spanStart int                // where its spans begin in parser.spans
	base      *runProps          // the formatting its style and the defaults give its text
	own       uint8              // the on/off properties its style switches on, apart from the defaults
	declared  gofastcore.Display // what that formatting declares about the display of its text
	flags     clean.Flags        // how its text is cleaned
	verbatim  bool               // it is code: its text is kept as written
	display   []byte             // the text of its display equations
	lead      int                // a thematic break before its text: its index in parser.after, plus one
}

var noFormatting runProps

type cellProps struct {
	span     int
	vMerge   int8 // 0 none, 1 restart, 2 continue
	text     []byte
	hasBlock bool
	hidden   gofastcore.Display // what the file declares about the display of any of its text
}

type rowProps struct {
	header     bool
	deleted    bool
	gridBefore int
}

func (r *reader) newParser(name string, rels map[string]opc.Rel) *parser {
	return &parser{r: r, name: name, rels: rels}
}

// clean applies the text conventions and counts what had to be removed.
func (p *parser) clean(b []byte, flags clean.Flags) string {
	s, removed := clean.String(b, flags)
	p.r.removed += removed
	return s
}

// cancelled reports whether the caller has given up. It looks every few
// hundred paragraphs.
func (p *parser) cancelled() bool {
	p.polls++
	return p.polls%256 == 0 && p.r.ctx.Err() != nil
}

// document reads w:document: it finds the body and reads it.
func (p *parser) document(out *[]gofastcore.Block) {
	s := &p.s
	for s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start {
			continue
		}
		if s.Depth() == 2 && s.Is(ns.Word, "body") {
			p.container(func(b gofastcore.Block) { p.emitBody(out, b) })
			p.flush(func(b gofastcore.Block) { p.emitBody(out, b) })
			continue
		}
		if s.Depth() >= 2 {
			s.Skip()
		}
	}
}

// emitBody appends a block to the body. Consecutive code paragraphs are
// one block of code, with or without spans: the blocks are the same
// whether spans are asked for or not. The block's text is built in
// p.code, so a run of n paragraphs costs time in proportion to its
// length, not to n times it.
func (p *parser) emitBody(out *[]gofastcore.Block, b gofastcore.Block) {
	if n := len(*out); n > 0 && b.Kind == gofastcore.KindCode && (*out)[n-1].Kind == gofastcore.KindCode &&
		b.Display == 0 && (*out)[n-1].Display == 0 {
		last := &(*out)[n-1]
		at := len(last.Text) + 1
		if p.codeIn != out || p.codeAt != n-1 || p.code.Len() != len(last.Text) {
			// A new run: what the builder holds is another block's.
			p.code = strings.Builder{}
			p.code.WriteString(last.Text)
			p.codeIn, p.codeAt = out, n-1
		}
		p.code.WriteByte('\n')
		p.code.WriteString(b.Text)
		last.Text = p.code.String()
		for _, span := range b.Spans {
			span.Start += at
			span.End += at
			last.Spans = append(last.Spans, span)
		}
		return
	}
	*out = append(*out, b)
}

// flush emits the blocks that were waiting for the current paragraph or
// table to be finished: its footnotes, comments, deletions, images and
// text boxes.
func (p *parser) flush(emit func(gofastcore.Block)) {
	if p.tables > 0 || len(p.after) == 0 {
		return
	}
	waiting := p.after
	p.after = nil
	for _, b := range waiting {
		emit(b)
	}
}

// wrapper reports whether the current start tag is an element whose
// children are read as if they stood in its place: content controls,
// insertions, alternate content, and so on. For alternate content it also
// decides whether this branch is the one to read.
func (p *parser) wrapper() (transparent, skip bool) {
	s := &p.s
	switch s.Space() {
	case ns.Word:
		switch string(s.Local()) {
		case "sdt", "sdtContent", "customXml", "smartTag", "moveTo", "fldSimple", "hyperlink", "dir", "bdo":
			return true, false
		case "ins":
			p.r.inserted++
			return true, false
		}
	case ns.MC:
		depth := s.Depth()
		switch string(s.Local()) {
		case "AlternateContent":
			for n := len(p.alts); n > 0 && p.alts[n-1].depth >= depth; n = len(p.alts) {
				p.alts = p.alts[:n-1]
			}
			p.alts = append(p.alts, alternate{depth: depth, start: p.produced})
			return true, false
		case "Choice", "Fallback":
			for n := len(p.alts); n > 0 && p.alts[n-1].depth >= depth; n = len(p.alts) {
				p.alts = p.alts[:n-1]
			}
			if n := len(p.alts); n > 0 {
				a := &p.alts[n-1]
				if p.produced > a.start {
					a.produced = true
				}
				if a.produced {
					return false, true // an earlier branch gave the content
				}
			}
			return true, false
		}
	}
	return false, false
}

// container reads block-level content up to the end of the element whose
// start tag is current: a body, a table cell, a text box, a note.
func (p *parser) container(emit func(gofastcore.Block)) {
	s := &p.s
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || (k == xmlscan.End && s.Depth() < depth) {
			return
		}
		if k != xmlscan.Start {
			continue
		}
		if transparent, skip := p.wrapper(); transparent {
			continue
		} else if skip {
			s.Skip()
			continue
		}
		if s.Space() != ns.Word {
			s.Skip()
			continue
		}
		switch string(s.Local()) {
		case "p":
			if p.cancelled() {
				return
			}
			p.paragraph(emit)
			p.flush(emit)
		case "tbl":
			p.table(emit)
			p.flush(emit)
		case "tcPr":
			p.cellProps()
		case "sectPr":
			p.section()
		case "altChunk":
			s.Skip() // an embedded document; handed on with the other embedded files
		case "del", "moveFrom":
			// Whole paragraphs or rows that were deleted: with all changes
			// accepted they are gone.
			p.r.deleted++
			s.Skip()
		default:
			s.Skip()
		}
	}
}

// section reads section properties for the headers and footers they name.
// A first-page header is shown only if the section has a different first
// page; an even-page one only if the document has different odd and even
// pages, which is a setting of the document.
func (p *parser) section() {
	s := &p.s
	titlePage := false
	var notes [2]notePr
	defer func() { p.r.endSection(notes) }()
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		if s.Depth() == depth+1 && s.Is(ns.Word, "titlePg") {
			titlePage = onOff(s)
			continue
		}
		if s.Depth() == depth+1 && s.Is(ns.Word, "footnotePr") {
			notes[0] = readNotePr(s)
			continue
		}
		if s.Depth() == depth+1 && s.Is(ns.Word, "endnotePr") {
			notes[1] = readNotePr(s)
			continue
		}
		header := s.Is(ns.Word, "headerReference")
		if !header && !s.Is(ns.Word, "footerReference") {
			if s.Depth() == depth+1 {
				s.Skip()
			}
			continue
		}
		id, _ := s.Attr(ns.Rel, "id")
		typ, _ := s.Attr(ns.Word, "type")
		rel, ok := p.rels[string(id)]
		if !ok || rel.External {
			continue
		}
		ref := furnitureRef{name: rel.Target, header: header, named: true, shown: true}
		switch string(typ) {
		case "even":
			ref.even = true
		case "first":
			ref.shown = false
			which := 0
			if header {
				which = 1
			}
			p.r.firstPage[which] = len(p.r.furniture) + 1
		}
		p.r.furniture = append(p.r.furniture, ref)
	}
	// A section with a different first page shows the first-page header
	// and footer it names or, naming none, the ones it inherits from the
	// sections before it.
	if titlePage {
		for _, i := range p.r.firstPage {
			if i > 0 {
				p.r.furniture[i-1].shown = true
			}
		}
	}
}

// readParaProps reads w:pPr. p is nil when the properties are a style's.
func readParaProps(s *xmlscan.Scanner, pp *paraProps, p *parser) {
	pp.numID, pp.ilvl, pp.outline = -1, -1, -1
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start || s.Depth() != depth+1 {
			continue
		}
		if s.Space() != ns.Word {
			s.Skip()
			continue
		}
		switch string(s.Local()) {
		case "pStyle":
			if v, ok := s.Attr(ns.Word, "val"); ok && p != nil {
				pp.styleID = string(v)
				pp.style = p.r.styles.get(v)
			}
		case "outlineLvl":
			if v, ok := intAttr(s, "val"); ok && v <= 9 {
				pp.outline = int8(v)
			}
		case "numPr":
			inner := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= inner {
				if s.Kind() != xmlscan.Start || s.Depth() != inner+1 {
					continue
				}
				switch {
				case s.Is(ns.Word, "numId"):
					if v, ok := intAttr(s, "val"); ok {
						pp.numID = v
					}
				case s.Is(ns.Word, "ilvl"):
					if v, ok := intAttr(s, "val"); ok && v < maxLevels {
						pp.ilvl = int8(v)
					}
				default:
					s.Skip() // w:ins and the like: a record of a change
				}
			}
		case "sectPr":
			if p != nil {
				p.section()
			} else {
				s.Skip()
			}
		default:
			// Everything else, including pPrChange, which holds the
			// properties the paragraph had before a tracked change.
			s.Skip()
		}
	}
}

// paragraph reads w:p and emits its block, if it has any text.
func (p *parser) paragraph(emit func(gofastcore.Block)) {
	s := &p.s
	var paraID string
	if v, ok := s.Attr(ns.Word14, "paraId"); ok {
		if len(v) <= 32 {
			paraID = clean.Label(string(v))
		} else {
			p.r.lost.Add(warn.CapParaID, 1)
		}
	}
	pp := paraProps{numID: -1, ilvl: -1, outline: -1}
	if p.r.styles != nil {
		pp.style = p.r.styles.def
	}
	// A paragraph can sit inside another's run, in a text box; the outer
	// one's state is put back when this one is done.
	outer, outerHref, outerDepth := p.para, p.href, p.hrefDepth
	p.para = paraState{start: len(p.text), spanStart: len(p.spans)}
	p.href, p.hrefDepth = "", 0
	p.format(&pp)
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || (k == xmlscan.End && s.Depth() < depth) {
			break
		}
		if k == xmlscan.End && p.hrefDepth > 0 && s.Depth() < p.hrefDepth {
			p.href, p.hrefDepth = "", 0 // the hyperlink has ended
		}
		if k != xmlscan.Start {
			continue
		}
		if p.r.opts.Spans && s.Is(ns.Word, "hyperlink") {
			p.href, p.hrefDepth = p.linkTarget(), s.Depth()
		}
		if s.Is(ns.Word, "fldSimple") {
			// A field written in one piece: its code is an attribute.
			if code, ok := s.Attr(ns.Word, "instr"); ok {
				if target := p.fieldLink(code); target != "" && p.r.opts.Spans {
					p.href, p.hrefDepth = target, s.Depth()
				}
				p.fieldNote(bytes.TrimSpace(code))
			}
		}
		if transparent, skip := p.wrapper(); transparent {
			continue
		} else if skip {
			s.Skip()
			continue
		}
		switch s.Space() {
		case ns.Word:
			switch string(s.Local()) {
			case "r":
				p.run(false)
			case "pPr":
				readParaProps(s, &pp, p)
				p.format(&pp)
			case "del":
				p.deletion()
			case "moveFrom":
				// The text was moved, not removed: it is read where it went.
				s.Skip()
			default:
				s.Skip()
			}
		case ns.Math:
			// An equation, in UnicodeMath, the linear form Word shows.
			display := s.Is(ns.Math, "oMathPara")
			start := len(p.text)
			p.text = omml.Linear(s, p.text)
			p.tidy(start)
			if display {
				if len(p.para.display) > 0 {
					p.para.display = append(p.para.display, '\n')
				}
				p.para.display = append(p.para.display, p.text[start:]...)
			}
		default:
			s.Skip()
		}
	}
	b, ok := p.block(&pp)
	// A paragraph that is a display equation and nothing else is one.
	if ok && b.Kind == gofastcore.KindParagraph && len(p.para.display) > 0 && b.Marker == "" &&
		b.Text == string(bytes.TrimSpace(p.para.display)) {
		b.Kind = gofastcore.KindFormula
		if b.Attrs == nil {
			b.Attrs = map[string]string{}
		}
		b.Attrs[gofastcore.AttrNotation] = gofastcore.NotationUnicodeMath
	}
	lead := p.para.lead
	p.text = p.text[:p.para.start]
	p.spans = p.spans[:p.para.spanStart]
	p.para, p.href, p.hrefDepth = outer, outerHref, outerDepth
	if !ok {
		return
	}
	// A horizontal line that came before the paragraph's text stands
	// before it; one that came after, or in a paragraph with no text,
	// follows it with the rest of what waits.
	if i := lead - 1; i >= 0 && i < len(p.after) && p.after[i].Kind == gofastcore.KindThematicBreak {
		brk := p.after[i]
		p.after = append(p.after[:i], p.after[i+1:]...)
		emit(brk)
	}
	// Word's own id for the paragraph: what a citation can point at in a
	// format with no pages.
	b.Loc.Anchor = paraID
	p.produced++
	emit(b)
}

// format works out what the paragraph's properties mean for its runs: the
// formatting they inherit, and how their text is cleaned. It is called again when the properties are read, since they
// are the paragraph's first child.
func (p *parser) format(pp *paraProps) {
	st := &p.para
	st.base, st.flags, st.verbatim = &noFormatting, clean.Ligatures, false
	if p.r.styles != nil {
		st.base = &p.r.styles.defaults
	}
	st.own = 0
	if pp.style != nil {
		st.base, st.own = &pp.style.run, pp.style.own
		// A paragraph in a code style is code, kept as written, unless it
		// is also a heading or a numbered item: those are what they are
		// declared to be, and their text is text like any other.
		outline, numID := pp.outline, pp.numID
		if outline < 0 {
			outline = pp.style.outline
		}
		if numID < 0 {
			numID = pp.style.numID
		}
		if pp.style.role == roleCode && !(outline >= 0 && outline < 9) && numID <= 0 {
			st.flags, st.verbatim = clean.Verbatim, true
		}
	}
	st.declared = st.base.display()
}

// hideWhole records that all of a block is hidden for the given reasons.
// The reasons then belong to the block, not to parts of it.
func hideWhole(b *gofastcore.Block, reasons gofastcore.Display) {
	b.Display |= reasons
	if b.Table != nil {
		kept := b.Table.Display[:0]
		for _, h := range b.Table.Display {
			if h.Display &^= reasons; h.Display != 0 {
				kept = append(kept, h)
			}
		}
		if b.Table.Display = kept; len(kept) == 0 {
			b.Table.Display = nil
		}
	}
	kept := b.Spans[:0]
	for _, sp := range b.Spans {
		if sp.Display &^= reasons; sp.Display != 0 || sp.Style != 0 || sp.Href != "" {
			kept = append(kept, sp)
		}
	}
	if b.Spans = kept; len(kept) == 0 {
		b.Spans = nil
	}
}

// tidy cleans the text appended since start, in place: valid UTF-8, no
// control characters, ligatures expanded in prose. Cleaning each piece as
// it arrives keeps the offsets of the pieces true.
func (p *parser) tidy(start int) {
	piece := p.text[start:]
	if clean.Plain(piece) {
		return
	}
	var removed int
	p.scratch, removed = clean.Append(p.scratch[:0], piece, p.para.flags)
	p.r.removed += removed
	p.text = append(p.text[:start], p.scratch...)
}

// linkTarget returns where the w:hyperlink element being read points: an
// address outside the document, or a place inside it as "#name".
func (p *parser) linkTarget() string {
	s := &p.s
	if id, ok := s.Attr(ns.Rel, "id"); ok {
		if rel, found := p.rels[string(id)]; found && rel.External {
			return rel.Target // cleaned when the relationships were read
		}
	}
	if anchor, ok := s.Attr(ns.Word, "anchor"); ok && len(anchor) > 0 {
		return "#" + p.r.lost.Address(string(anchor))
	}
	return ""
}

// span records the formatting of the text of the current paragraph from
// start to the end of what has been read, joining it to the span before
// if that one ends where this begins and says the same.
func (p *parser) span(start int, style gofastcore.Style, href string, hidden gofastcore.Display) {
	from, to := start-p.para.start, len(p.text)-p.para.start
	if to <= from {
		return
	}
	if n := len(p.spans); n > p.para.spanStart {
		if last := &p.spans[n-1]; last.End == from && last.Style == style && last.Href == href && last.Display == hidden {
			last.End = to
			return
		}
	}
	// One address may be linked to any number of times: it counts against
	// the text the document may hold each time it is written.
	if href != "" && !p.r.text.Take(len(href)) {
		p.s.Abort(warn.ErrText)
		return
	}
	p.spans = append(p.spans, gofastcore.Span{Start: from, End: to, Style: style, Href: href, Display: hidden})
}

// block turns the paragraph that has just been read into a block.
func (p *parser) block(pp *paraProps) (gofastcore.Block, bool) {
	st := pp.style
	outline, numID, ilvl, kind := pp.outline, pp.numID, pp.ilvl, roleNone
	if st != nil {
		if outline < 0 {
			outline = st.outline
		}
		if numID < 0 {
			numID = st.numID
			if ilvl < 0 {
				ilvl = st.ilvl
			}
		}
		kind = st.role
	}
	// The text was cleaned as it was read; what is left is to trim it.
	// Code keeps its indentation.
	raw := p.text[p.para.start:]
	from, to := 0, len(raw)
	for to > 0 {
		r, size := utf8.DecodeLastRune(raw[:to])
		if !unicode.IsSpace(r) {
			break
		}
		to -= size
	}
	for !p.para.verbatim && from < to {
		r, size := utf8.DecodeRune(raw[from:to])
		if !unicode.IsSpace(r) {
			break
		}
		from += size
	}
	if from == to {
		// An empty paragraph is spacing. A numbered one still counts in
		// its list, as it does in Word.
		if numID > 0 {
			p.label(pp, numID, ilvl)
		}
		return gofastcore.Block{}, false
	}
	// Every paragraph is counted here, wherever it stands: in the body,
	// a table cell, a note, a header, a text box. A paragraph the
	// document has no room for ends the reading; what was read is kept.
	cost := to - from
	if p.cell == nil {
		cost += gofastcore.BlockCost // a block; a cell's paragraphs are charged with its table
	}
	if !p.r.text.Take(cost) {
		p.s.Abort(warn.ErrText)
		return gofastcore.Block{}, false
	}

	b := gofastcore.Block{Kind: gofastcore.KindParagraph, Text: string(raw[from:to])}
	b.Spans, b.Display = settle(p.spans[p.para.spanStart:], from, to, raw)
	var marker string
	var ordered, listed bool
	if numID > 0 {
		marker, ordered, listed = p.label(pp, numID, ilvl)
	}
	switch {
	case kind == roleTitle:
		b.Kind, b.Level = gofastcore.KindHeading, 1
		b.Attrs = map[string]string{"style": "title"}
	case kind == roleSubtitle:
		b.Kind, b.Level = gofastcore.KindHeading, 2
		b.Attrs = map[string]string{"style": "subtitle"}
	case outline >= 0 && outline < 9:
		b.Kind, b.Level = gofastcore.KindHeading, uint8(outline)+1
		if b.Level > 6 {
			// The model has six heading levels; Word has nine.
			b.Attrs = map[string]string{gofastcore.AttrLevel: strconv.Itoa(int(b.Level))}
			p.r.clamped++
			b.Level = 6
		}
		b.Marker = marker // a numbered heading keeps its number
	case listed:
		b.Kind, b.Ordered, b.Marker = gofastcore.KindListItem, ordered, marker
		b.Level = 1
		switch {
		case ilvl > 0:
			b.Level = uint8(ilvl) + 1
		case st != nil && st.nested > 0:
			b.Level = st.nested
		}
	case kind == roleQuote:
		b.Kind, b.Level = gofastcore.KindQuote, 1
	case p.para.verbatim:
		b.Kind = gofastcore.KindCode
	}
	return b, true
}

// settle turns the spans collected while a paragraph was read into the
// spans and display reasons of its block.
func settle(collected []gofastcore.Span, from, to int, raw []byte) ([]gofastcore.Span, gofastcore.Display) {
	return hidden.Settle(collected, from, to, raw)
}

// label advances the paragraph's list and returns its label.
func (p *parser) label(pp *paraProps, numID int, ilvl int8) (marker string, ordered, ok bool) {
	level := int(ilvl)
	if level < 0 {
		// The level is not written on the paragraph: the list says which
		// of its levels belongs to the paragraph's style.
		if level = p.r.numbering.levelOfStyle(numID, pp.styleID); level < 0 {
			level = 0
		}
	}
	return p.r.num.next(p.r.numbering, numID, level)
}

// run reads w:r into the current paragraph's text. deleted says the run is
// inside a tracked deletion, where the text is in w:delText.
func (p *parser) run(deleted bool) {
	s := &p.s
	start, hung := len(p.text), len(p.after)
	var direct runProps
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || (k == xmlscan.End && s.Depth() < depth) {
			break
		}
		if k != xmlscan.Start {
			continue
		}
		if transparent, skip := p.wrapper(); transparent {
			continue
		} else if skip {
			s.Skip()
			continue
		}
		if s.Space() != ns.Word {
			s.Skip()
			continue
		}
		switch string(s.Local()) {
		case "rPr":
			readRunProps(s, &direct)
		case "t":
			if deleted || p.inCode() {
				s.Skip()
				continue
			}
			p.runText(p.runFont(&direct))
		case "delText":
			if deleted && !p.inCode() {
				p.runText(p.runFont(&direct))
			} else {
				s.Skip()
			}
		case "tab", "ptab":
			p.text = append(p.text, '\t')
		case "br", "cr":
			p.text = append(p.text, '\n')
		case "noBreakHyphen":
			p.text = append(p.text, "\u2011"...)
		case "softHyphen":
			p.text = append(p.text, "\u00AD"...)
		case "sym":
			p.symbol()
		case "ruby":
			p.ruby()
		case "footnoteReference":
			p.noteRef(p.r.footnotes, 0)
		case "endnoteReference":
			p.noteRef(p.r.endnotes, 1)
		case "commentReference":
			p.commentRef()
		case "drawing", "pict", "object":
			p.drawing(deleted)
		case "fldChar":
			// A field: its code, then what it displays. The code of a
			// HYPERLINK field says where the displayed text leads.
			p.fieldChar()
		case "instrText", "delInstrText":
			n := len(p.fields)
			switch {
			case n == 0 || !p.fields[n-1].inCode || s.Is(ns.Word, "delInstrText") != deleted:
				s.Skip()
			case p.fields[n-1].cut:
				s.Skip()
			default:
				f := &p.fields[n-1]
				f.code = s.AppendText(f.code)
				cutCode(f)
			}
		default:
			// Marks with no text of their own.
			s.Skip()
		}
	}
	if deleted || len(p.text) == start && len(p.after) == hung {
		return
	}
	// Everything the run added is cleaned here, in one place, before its
	// extent is recorded.
	p.tidy(start)
	// What the run's formatting means for its text. A run that sets
	// nothing of its own has its paragraph's answer.
	hidden := p.para.declared
	var style gofastcore.Style
	href := ""
	if p.r.opts.Spans {
		if href = p.href; href == "" {
			href = p.fieldHref
		}
	}
	if !direct.empty() {
		if c := p.r.styles.char(direct.style); c != nil {
			// Hidden by the character style and by the paragraph's style
			// both is shown: the two cancel. The run's own setting, and
			// the document defaults, are not part of that.
			cancels := direct.has&propVanish == 0 && c.is(propVanish) && p.para.own&propVanish != 0 &&
				!p.r.styles.defaults.is(propVanish)
			direct.under(c)
			if cancels {
				direct.setFlag(propVanish, false)
			}
		}
		if p.r.opts.Spans {
			style = direct.inline()
		}
		direct.under(p.para.base)
		hidden = direct.display()
	}
	// What hangs on a hidden run is hidden with it: the footnote whose
	// reference it holds, the text box or picture anchored in it. A
	// comment is not; it is shown in the margin whatever it is about.
	if hidden&gofastcore.DisplayHidden != 0 {
		for i := hung; i < len(p.after); i++ {
			if b := &p.after[i]; b.Kind != gofastcore.KindNote {
				hideWhole(b, gofastcore.DisplayHidden)
			}
		}
	}
	if hidden != 0 || style != 0 || href != "" {
		p.span(start, style, href, hidden)
	}
}

// fieldLink returns the target of a HYPERLINK field from its code, such as
// HYPERLINK "https://example.org" or HYPERLINK \l "bookmark", or "".
func (p *parser) fieldLink(code []byte) string {
	fields := strings.Fields(string(code))
	if len(fields) < 2 || !strings.EqualFold(fields[0], "HYPERLINK") {
		return ""
	}
	local := false
	for _, f := range fields[1:] {
		switch {
		case f == `\l`:
			local = true
		case strings.HasPrefix(f, `\`):
			// another switch, with or without a value
		case strings.HasPrefix(f, `"`):
			target := p.r.lost.Address(strings.Trim(f, `"`))
			if target == "" {
				continue
			}
			if local {
				return "#" + target
			}
			return target
		}
	}
	return ""
}

// runFont returns the symbol font a run's text is in, from its own
// properties, its character style, its paragraph's style or the
// document's defaults, nearest first; symbol.None for an ordinary font.
func (p *parser) runFont(direct *runProps) symbol.Font {
	if direct.fontSet {
		return direct.font
	}
	if c := p.r.styles.char(direct.style); c != nil && c.fontSet {
		return c.font
	}
	return p.para.base.font
}

// runText appends the content of w:t, in font.
//
// White space at the ends of a text element is dropped unless the element
// asks for it to be kept with xml:space="preserve", which is what Word and
// LibreOffice do. Word writes that attribute whenever a run begins or ends
// with a space, so in its own files nothing is lost by the rule; in a file
// made by another program it reproduces what a reader sees in Word.
func (p *parser) runText(font symbol.Font) {
	s := &p.s
	space, _ := s.Attr(xmlscan.SpaceXML, "space")
	preserve := string(space) == "preserve"
	start := len(p.text)
	p.text = s.AppendText(p.text)
	if font != symbol.None {
		// Text typed in a symbol font: each character is a code of the
		// font ("J" in Wingdings is a smiling face).
		var missing int
		p.symbols, missing = font.Translate(p.symbols[:0], p.text[start:])
		p.text = append(p.text[:start], p.symbols...)
		p.r.unmapped += missing
	}
	// A paragraph is counted when it is complete; one that has by itself
	// outgrown what is left is not read to its end first.
	if n := len(p.text) - p.para.start; n > 1<<16 && int64(n) > p.r.text.Left() {
		p.s.Abort(warn.ErrText)
	}
	if preserve {
		return
	}
	t := p.text[start:]
	i, j := 0, len(t)
	for i < j && (t[i] == ' ' || t[i] == '\n' || t[i] == '\t') {
		i++
	}
	for j > i && (t[j-1] == ' ' || t[j-1] == '\n' || t[j-1] == '\t') {
		j--
	}
	if i > 0 || j < len(t) {
		n := copy(t, t[i:j])
		p.text = p.text[:start+n]
	}
}

// ruby reads w:ruby: text with a pronunciation guide set above it, as in
// Japanese. The base text is followed by the guide in full-width
// parentheses, the usual way to write the pair on one line, so that
// neither is lost.
func (p *parser) ruby() {
	s := &p.s
	var guide []byte
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || (k == xmlscan.End && s.Depth() < depth) {
			break
		}
		if k != xmlscan.Start || s.Depth() != depth+1 {
			continue
		}
		start := len(p.text)
		switch {
		case s.Is(ns.Word, "rt"):
			p.rubyRuns()
			guide = append(guide, p.text[start:]...)
			p.text = p.text[:start]
		case s.Is(ns.Word, "rubyBase"):
			p.rubyRuns()
		default:
			s.Skip()
		}
	}
	if len(guide) > 0 {
		p.text = append(p.text, "\uFF08"...)
		p.text = append(p.text, guide...)
		p.text = append(p.text, "\uFF09"...)
	}
}

// rubyRuns reads the runs inside w:rt or w:rubyBase.
func (p *parser) rubyRuns() {
	s := &p.s
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || (k == xmlscan.End && s.Depth() < depth) {
			return
		}
		if k == xmlscan.Start {
			if s.Is(ns.Word, "r") {
				p.run(false)
			} else if transparent, _ := p.wrapper(); !transparent {
				s.Skip()
			}
		}
	}
}

// symbol reads w:sym, a character from a symbol font given by number.
func (p *parser) symbol() {
	s := &p.s
	font, _ := s.Attr(ns.Word, "font")
	char, _ := s.Attr(ns.Word, "char")
	code := 0
	for _, c := range char {
		switch {
		case c >= '0' && c <= '9':
			code = code<<4 | int(c-'0')
		case c >= 'a' && c <= 'f':
			code = code<<4 | int(c-'a'+10)
		case c >= 'A' && c <= 'F':
			code = code<<4 | int(c-'A'+10)
		}
		if code > 0x10FFFF {
			code = 0xFFFD
		}
	}
	r := symbolRune(string(font), rune(code))
	if r == utf8.RuneError {
		p.r.unmapped++
	}
	// Through the same door as all other text, so that what holds for
	// text holds for a character given by number.
	var buf [utf8.UTFMax]byte
	var removed int
	p.text, removed = clean.Append(p.text, buf[:utf8.EncodeRune(buf[:], r)], p.para.flags)
	p.r.removed += removed
}

// noteRef handles a reference to a footnote or endnote: the note becomes a
// block after the paragraph, labelled with the number a reader sees.
func (p *parser) noteRef(notes map[int]note, kind int) {
	s := &p.s
	id, ok := intAttr(s, "id")
	custom, has := s.Attr(ns.Word, "customMarkFollows")
	marker := ""
	if !has || !isOn(custom) {
		marker = p.r.noteMark(kind)
	}
	// A note is given once, at its first reference. A file that refers to
	// one note many times does not get it copied each time.
	key := [2]string{"foot", "end"}[kind] + strconv.Itoa(id)
	if n, found := notes[id]; ok && found && n.text != "" && !p.r.usedNotes[key] {
		p.r.usedNotes[key] = true
		p.after = append(p.after, gofastcore.Block{Kind: gofastcore.KindFootnote, Marker: marker, Text: n.text, Spans: n.spans, Display: n.hidden})
		p.produced++
	}
}

func (p *parser) commentRef() {
	id, ok := intAttr(&p.s, "id")
	n, found := p.r.comments[id]
	key := "comment" + strconv.Itoa(id)
	if !ok || !found || n.text == "" || p.r.usedNotes[key] {
		return
	}
	p.r.usedNotes[key] = true
	attrs := map[string]string{gofastcore.AttrNoteType: gofastcore.NoteComment}
	if n.author != "" {
		attrs[gofastcore.AttrAuthor] = n.author
	}
	if n.date != "" {
		attrs[gofastcore.AttrDate] = n.date
	}
	p.after = append(p.after, gofastcore.Block{Kind: gofastcore.KindNote, Text: n.text, Spans: n.spans, Display: n.hidden, Attrs: attrs})
}

// deletion reads w:del inside a paragraph: text removed by a tracked
// change. It is left out of the paragraph and kept as a note after it.
func (p *parser) deletion() {
	s := &p.s
	author, _ := s.Attr(ns.Word, "author")
	date, _ := s.Attr(ns.Word, "date")
	attrs := map[string]string{gofastcore.AttrNoteType: gofastcore.NoteDeletion}
	if a := p.clean(author, clean.Prose); a != "" {
		attrs[gofastcore.AttrAuthor] = a
	}
	if d := p.clean(date, clean.Trim); d != "" {
		attrs[gofastcore.AttrDate] = d
	}
	p.r.deleted++
	start := len(p.text)
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || (k == xmlscan.End && s.Depth() < depth) {
			break
		}
		if k != xmlscan.Start {
			continue
		}
		if s.Is(ns.Word, "r") {
			p.run(true)
		} else if transparent, _ := p.wrapper(); !transparent {
			s.Skip()
		}
	}
	text := p.clean(p.text[start:], clean.Prose)
	p.text = p.text[:start]
	if text != "" {
		p.after = append(p.after, gofastcore.Block{Kind: gofastcore.KindNote, Text: text, Attrs: attrs})
	}
}

// drawing reads w:drawing, w:pict or w:object: a picture, a text box, an
// embedded object. Pictures and the text of text boxes become blocks after
// the paragraph they are anchored in. deleted says the run is in a tracked
// deletion.
//
// Word's Horizontal Line is a legacy shape the file marks as one
// (o:hr="t"). In the main body, outside tables and text boxes, it is a
// thematic break and not a picture. A paragraph that only has a border
// is not one: the file does not say it is a break.
func (p *parser) drawing(deleted bool) {
	s := &p.s
	var alt, target, name string
	var nameOver int64
	var external, line bool
	boxes := len(p.after)
	// The drawing is declared hidden: wp:docPr hidden, or a VML shape
	// whose style says visibility:hidden.
	unseen := false
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || (k == xmlscan.End && s.Depth() < depth) {
			break
		}
		if k != xmlscan.Start {
			continue
		}
		if transparent, skip := p.wrapper(); transparent {
			continue
		} else if skip {
			s.Skip()
			continue
		}
		switch {
		case s.Is(ns.WordDraw, "docPr"):
			if v, ok := s.Attr(xmlscan.NoSpace, "hidden"); ok && isOn(v) {
				unseen = true
			}
			if v, ok := s.Attr(xmlscan.NoSpace, "name"); ok {
				name, nameOver = clean.Name(string(v)), warn.Over(string(v))
			}
			if v, ok := s.Attr(xmlscan.NoSpace, "descr"); ok && len(v) > 0 {
				alt = p.clean(v, clean.Prose)
			} else if v, ok := s.Attr(xmlscan.NoSpace, "title"); ok && alt == "" {
				alt = p.clean(v, clean.Prose)
			}
		case s.Is(ns.Draw, "blip"):
			id, ok := s.Attr(ns.Rel, "embed")
			if !ok {
				id, ok = s.Attr(ns.Rel, "link")
			}
			if rel, found := p.rels[string(id)]; ok && found {
				target, external = rel.Target, rel.External
			}
		case s.Is(ns.VML, "imagedata"):
			if id, ok := s.Attr(ns.Rel, "id"); ok {
				if rel, found := p.rels[string(id)]; found {
					target, external = rel.Target, rel.External
				}
			}
			if v, ok := s.Attr(ns.Office, "title"); ok && alt == "" {
				alt = p.clean(v, clean.Prose)
			}
		case s.Is(ns.VML, "shape"), s.Is(ns.VML, "rect"), s.Is(ns.VML, "roundrect"), s.Is(ns.VML, "oval"):
			if v, ok := s.Attr(xmlscan.NoSpace, "alt"); ok && alt == "" {
				alt = p.clean(v, clean.Prose)
			}
			if v, ok := s.Attr(xmlscan.NoSpace, "style"); ok && vmlHidden(v) {
				unseen = true
			}
			if v, ok := s.Attr(ns.Office, "hr"); ok && vmlOn(v) {
				line = true
			}
		case s.Is(ns.Word, "txbxContent"):
			// A text box: its paragraphs follow the one it hangs on.
			outerCell := p.cell
			p.cell = nil
			p.boxes++
			p.container(func(b gofastcore.Block) { p.after = append(p.after, b) })
			p.boxes--
			p.cell = outerCell
		case s.Is(ns.Chart, "chart"), s.Is(ns.ChartEx, "chart"):
			p.chart(name, nameOver)
		case s.Is(ns.Diagram, "relIds"):
			p.diagram()
		}
	}
	if unseen {
		for i := boxes; i < len(p.after); i++ {
			hideWhole(&p.after[i], gofastcore.DisplayHidden)
		}
	}
	if line && p.body && p.tables == 0 && p.cell == nil && p.boxes == 0 {
		p.horizontalLine(deleted, unseen)
		return
	}
	if target == "" || p.r.opts.Images == gofastcore.ImagesNone {
		if target != "" {
			p.r.sawImage = true
		}
		return
	}
	p.produced++
	// What a picture says counts as text does: its alternative text and,
	// if the picture is not in the file, its address, which like a
	// link's may be given any number of times.
	said := gofastcore.BlockCost + len(alt)
	if external {
		said += len(target)
	}
	if !p.r.text.Take(said) {
		p.s.Abort(warn.ErrText)
		return
	}
	if external {
		// A picture that is not in the file: named, not fetched.
		b := gofastcore.Block{
			Kind: gofastcore.KindImage, Text: alt, Image: &gofastcore.Image{},
			Attrs: map[string]string{"href": target},
		}
		if unseen {
			b.Display = gofastcore.DisplayHidden
		}
		p.after = append(p.after, b)
		return
	}
	b := p.r.imageBlock(target, alt)
	if unseen {
		b.Display |= gofastcore.DisplayHidden
	}
	p.after = append(p.after, b)
}

// horizontalLine records Word's Horizontal Line as a thematic break: after
// the paragraph it is in or, if no text has come before it there, before
// the paragraph. One in a tracked deletion is not recorded, since a break
// has no text for a deletion note to hold.
func (p *parser) horizontalLine(deleted, unseen bool) {
	if deleted {
		return
	}
	if !p.r.text.Take(gofastcore.BlockCost) {
		p.s.Abort(warn.ErrText)
		return
	}
	p.produced++
	b := gofastcore.Block{Kind: gofastcore.KindThematicBreak, Origin: gofastcore.OriginNative}
	if unseen {
		b.Display = gofastcore.DisplayHidden
	}
	if len(bytes.TrimSpace(p.text[p.para.start:])) == 0 && p.para.lead == 0 {
		p.para.lead = len(p.after) + 1
	}
	p.after = append(p.after, b)
}

// vmlOn reads a VML true or false value: t, true, f, false.
func vmlOn(v []byte) bool {
	switch string(v) {
	case "t", "true", "1", "on":
		return true
	}
	return false
}

// vmlHidden reports whether the style attribute of a legacy drawing shape
// says it is not shown (visibility:hidden).
func vmlHidden(style []byte) bool {
	for _, decl := range strings.Split(string(style), ";") {
		name, value, ok := strings.Cut(decl, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), "visibility") && strings.EqualFold(strings.TrimSpace(value), "hidden") {
			return true
		}
	}
	return false
}

// cellProps reads w:tcPr.
func (p *parser) cellProps() {
	s := &p.s
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start || s.Depth() != depth+1 || p.cell == nil {
			continue
		}
		switch {
		case s.Is(ns.Word, "gridSpan"):
			if v, ok := intAttr(s, "val"); ok && v > 1 {
				p.cell.span = min(v, 1024)
			}
		case s.Is(ns.Word, "vMerge"):
			p.cell.vMerge = 2
			if v, ok := s.Attr(ns.Word, "val"); ok && string(v) == "restart" {
				p.cell.vMerge = 1
			}
		default:
			s.Skip()
		}
	}
}

// maxColumns is the widest table kept as a table. Word itself stops at 63
// columns.
const maxColumns = 1024

type tableCell struct {
	text   string
	span   int
	vMerge int8
	hidden gofastcore.Display
}

// table reads w:tbl into a table block.
func (p *parser) table(emit func(gofastcore.Block)) {
	s := &p.s
	p.tables++
	savedCell, savedRow := p.cell, p.row
	p.cell = nil
	var rows [][]tableCell
	var before []int
	headerRows, counting := 0, true
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || (k == xmlscan.End && s.Depth() < depth) {
			break
		}
		if k != xmlscan.Start {
			continue
		}
		if transparent, skip := p.wrapper(); transparent {
			continue
		} else if skip {
			s.Skip()
			continue
		}
		if !s.Is(ns.Word, "tr") {
			s.Skip()
			continue
		}
		row, props := p.tableRow()
		if props.deleted {
			p.r.deleted++
			continue
		}
		if counting && props.header {
			headerRows++
		} else {
			counting = false
		}
		rows = append(rows, row)
		before = append(before, props.gridBefore)
	}
	p.cell, p.row = savedCell, savedRow
	p.tables--

	// Lay the cells out on a grid.
	cols := 0
	for i, row := range rows {
		n := before[i]
		for _, c := range row {
			n += c.span
		}
		cols = max(cols, n)
	}
	if len(rows) == 0 || cols == 0 {
		return
	}
	// The table model is a full grid. A file can declare a grid far larger
	// than the cells it holds (one cell said to span a thousand columns,
	// on every row); filling that grid would cost memory out of all
	// proportion to the file. Such a table is written as text instead:
	// every cell is kept, one row to a line.
	cells := 0
	for _, row := range rows {
		cells += len(row)
	}
	if cols > maxColumns || len(rows)*cols > 16*cells+4096 {
		p.r.flattened++
		for _, row := range rows {
			var line []byte
			var spans []gofastcore.Span
			for i, c := range row {
				if i > 0 {
					line = append(line, '\t')
				}
				at := len(line)
				line = append(line, strings.ReplaceAll(c.text, "\n", " ")...)
				if c.hidden != 0 {
					spans = append(spans, gofastcore.Span{Start: at, End: len(line), Display: c.hidden})
				}
			}
			from, to := 0, len(line)
			for from < to && (line[from] == ' ' || line[from] == '\t') {
				from++
			}
			for to > from && (line[to-1] == ' ' || line[to-1] == '\t') {
				to--
			}
			if from < to {
				p.produced++
				b := gofastcore.Block{Kind: gofastcore.KindParagraph, Text: string(line[from:to])}
				b.Spans, b.Display = settle(spans, from, to, line)
				emit(b)
			}
		}
		return
	}
	// The table is a block, and each of its cells takes room, filled or
	// not; their text was taken paragraph by paragraph. A table the
	// document has no room for in full keeps the rows that fit, and the
	// reading ends there.
	if fit := warn.FitRows(p.r.text, len(rows), cols); fit < len(rows) {
		p.s.Abort(warn.ErrText)
		if fit == 0 {
			return
		}
		rows, before = rows[:fit], before[:fit]
		headerRows = min(headerRows, fit)
	}
	t := &gofastcore.Table{HeaderRows: min(headerRows, len(rows)), Rows: make([][]string, len(rows))}
	var hidden gofastcore.Display
	open := make([]int, cols)          // per column: index in t.Merges of the vertical merge running through it, plus one
	marked := make([]int, cols)        // per column: index in t.Display of the entry of that merge's first cell, plus one
	var joins map[int]*strings.Builder // per merge (index in t.Merges): the text of its first cell and those it covers
	for r, row := range rows {
		t.Rows[r] = make([]string, cols)
		col := before[r]
		for _, c := range row {
			if col >= cols {
				break
			}
			switch {
			case c.vMerge == 2 && open[col] > 0:
				// A continuation of the cell above.
				m := &t.Merges[open[col]-1]
				m.RowSpan = r - m.Row + 1
				if c.text != "" {
					// The text is appended in a builder of the merge's, so
					// a merge down many rows is not copied again for each.
					top := &t.Rows[m.Row][m.Col]
					j := joins[open[col]-1]
					if j == nil {
						if joins == nil {
							joins = map[int]*strings.Builder{}
						}
						j = &strings.Builder{}
						j.WriteString(*top)
						joins[open[col]-1] = j
					}
					if j.Len() > 0 {
						j.WriteByte('\n')
					}
					j.WriteString(c.text)
					*top = j.String()
					if c.hidden != 0 {
						// The text has joined the merge's first cell, and
						// so does what is said of it.
						if marked[col] == 0 {
							t.Display = append(t.Display, gofastcore.DisplayCells{Row: m.Row, Col: m.Col, RowSpan: 1, ColSpan: 1})
							marked[col] = len(t.Display)
						}
						t.Display[marked[col]-1].Display |= c.hidden
						hidden |= c.hidden
					}
				}
			default:
				t.Rows[r][col] = c.text
				for k := col; k < col+c.span && k < cols; k++ {
					open[k], marked[k] = 0, 0
				}
				if c.hidden != 0 && c.text != "" {
					t.Display = append(t.Display, gofastcore.DisplayCells{Row: r, Col: col, RowSpan: 1, ColSpan: 1, Display: c.hidden})
					marked[col] = len(t.Display)
					hidden |= c.hidden
				}
				if c.vMerge == 1 || c.span > 1 {
					t.Merges = append(t.Merges, gofastcore.Merge{Row: r, Col: col, RowSpan: 1, ColSpan: min(c.span, cols-col)})
					if c.vMerge == 1 {
						for k := col; k < col+c.span && k < cols; k++ {
							open[k] = len(t.Merges)
						}
					}
				}
			}
			col += c.span
		}
		// A column this row did not reach ends any merge running through it.
		for k := col; k < cols; k++ {
			open[k], marked[k] = 0, 0
		}
	}
	// A merge of one cell is not a merge.
	kept := t.Merges[:0]
	for _, m := range t.Merges {
		if m.RowSpan > 1 || m.ColSpan > 1 {
			kept = append(kept, m)
		}
	}
	t.Merges = kept
	if len(t.Merges) == 0 {
		t.Merges = nil
	}
	p.produced++
	emit(gofastcore.Block{Kind: gofastcore.KindTable, Table: t, Display: hidden})
}

// tableRow reads w:tr.
func (p *parser) tableRow() ([]tableCell, rowProps) {
	s := &p.s
	var row []tableCell
	var props rowProps
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || (k == xmlscan.End && s.Depth() < depth) {
			return row, props
		}
		if k != xmlscan.Start {
			continue
		}
		if transparent, skip := p.wrapper(); transparent {
			continue
		} else if skip {
			s.Skip()
			continue
		}
		switch {
		case s.Is(ns.Word, "trPr"):
			inner := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= inner {
				if s.Kind() != xmlscan.Start || s.Depth() != inner+1 {
					continue
				}
				switch {
				case s.Is(ns.Word, "tblHeader"):
					v, has := s.Attr(ns.Word, "val")
					props.header = !has || isOn(v)
				case s.Is(ns.Word, "del"):
					props.deleted = true
				case s.Is(ns.Word, "ins"):
					p.r.inserted++
				case s.Is(ns.Word, "gridBefore"):
					if v, ok := intAttr(s, "val"); ok {
						props.gridBefore = min(v, 1024)
					}
				default:
					s.Skip()
				}
			}
		case s.Is(ns.Word, "tc"):
			cell := cellProps{span: 1}
			p.cell = &cell
			p.container(func(b gofastcore.Block) {
				text := flatten([]gofastcore.Block{b})
				if text == "" {
					return
				}
				if cell.hasBlock {
					cell.text = append(cell.text, '\n')
				}
				cell.text = append(cell.text, text...)
				cell.hasBlock = true
				// A cell is text without parts, so it carries a reason as
				// a whole when any of its text does.
				cell.hidden |= b.Display
			})
			p.cell = nil
			row = append(row, tableCell{text: string(cell.text), span: cell.span, vMerge: cell.vMerge, hidden: cell.hidden})
		default:
			s.Skip()
		}
	}
}
