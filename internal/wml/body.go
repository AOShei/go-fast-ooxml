package wml

import (
	"bytes"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/dml"
	"github.com/AOShei/go-fast-ooxml/internal/hidden"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/omml"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/stored"
	"github.com/AOShei/go-fast-ooxml/internal/symbol"
	"github.com/AOShei/go-fast-ooxml/internal/theme"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
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

	box color       // the fill of the text box being read; unset outside one
	tbl *tableState // the table being read

	href      string     // target of the hyperlink being read, when links are asked for
	hrefDepth int        // depth of its element
	fields    []field    // the fields being read, innermost last
	fieldHref string     // target of the HYPERLINK field whose result is being read
	cell      *cellProps // properties of the table cell being read, if any
	row       *rowProps

	tables   int // tables being read; notes wait until the outermost is out
	produced int // text and images emitted so far, for alternate content
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
}

// Bounds on the fields kept track of.
const (
	maxFieldDepth = 64
	maxFieldCode  = 1 << 16
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
// paragraph its field ends in, flagged as hidden. A field in the code of
// another is part of that one's code, written in braces as Word shows it.
func (p *parser) fieldChar() {
	typ, _ := p.s.Attr(ns.Word, "fldCharType")
	switch string(typ) {
	case "begin":
		nested := p.inCode()
		if len(p.fields) < maxFieldDepth {
			p.fields = append(p.fields, field{inCode: true, nested: nested})
		}
	case "separate":
		if n := len(p.fields); n > 0 && p.fields[n-1].inCode {
			f := &p.fields[n-1]
			f.inCode = false
			if p.r.opts.Spans {
				f.href = fieldLink(f.code)
			}
		}
	case "end":
		n := len(p.fields)
		if n == 0 {
			return
		}
		f := p.fields[n-1]
		p.fields = p.fields[:n-1]
		code := bytes.TrimSpace(f.code)
		switch {
		case len(code) == 0:
		case f.nested && n > 1 && p.fields[n-2].inCode:
			outer := &p.fields[n-2]
			if len(outer.code)+len(code)+4 <= maxFieldCode {
				outer.code = append(append(append(outer.code, "{ "...), code...), " }"...)
			}
		default:
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

// fieldNote keeps the code of a field as a note after the paragraph, if
// the code holds text that is shown nowhere. Most fields are structure:
// a page number, a cross-reference, a table of contents, a merge field.
// Their codes say what to show, and what they show is the text; they are
// not kept. Other codes are kept: an index entry, the branches of IF, the
// text of SET, ASK or FILLIN, a citation tool's data. Like the other data
// a file keeps and shows nowhere, they are not flagged for that, which is
// routine. Flagged as hidden are a structural field whose code holds more
// than such a field needs, and a code whose text is out of proportion for
// a field (stored.MaxWords words in its quoted strings), apart from a
// citation tool's, whose data is long by nature.
func (p *parser) fieldNote(code []byte) {
	text := p.clean(code, clean.Prose)
	if text == "" {
		return
	}
	keyword, _, _ := strings.Cut(text, " ")
	keyword = strings.ToUpper(keyword)
	structural := structuralFields[keyword] || strings.HasPrefix(keyword, "=") // a calculation is structure too
	if structural && plainCode(text) {
		return
	}
	var hidden gofastcore.Hidden
	switch n := stored.QuotedWords(text); {
	case structural:
		hidden = gofastcore.HiddenInvisible // more than it needs
		p.r.longs.Add("the code of a "+keyword+" field", "", stored.QuotedWords(text))
	case n > stored.MaxWords && !citation(text):
		hidden = gofastcore.HiddenInvisible
		p.r.longs.Add("the code of a field", keyword, n)
	}
	p.r.fields++
	p.after = append(p.after, gofastcore.Block{Kind: gofastcore.KindNote, Text: text, Hidden: hidden,
		Attrs: map[string]string{gofastcore.AttrNoteType: gofastcore.NoteField}})
}

// citation reports whether a field's code is a citation tool's data
// (Zotero, Mendeley, EndNote, Citavi), which is long by nature.
func citation(code string) bool {
	fields := strings.Fields(code)
	if len(fields) < 2 || !strings.EqualFold(fields[0], "ADDIN") {
		return false
	}
	tool := strings.ToUpper(fields[1])
	for _, prefix := range []string{"ZOTERO_", "CSL_", "MENDELEY", "EN.", "CITAVI"} {
		if strings.HasPrefix(tool, prefix) {
			return true
		}
	}
	return false
}

// structuralFields are the fields whose codes name what to show (a page
// number, a bookmark, a style, a property, a column of merged data) and
// carry no text of their own.
var structuralFields = map[string]bool{
	"PAGE": true, "NUMPAGES": true, "SECTIONPAGES": true, "SECTION": true, "PAGEREF": true,
	"REF": true, "NOTEREF": true, "SEQ": true, "TOC": true, "TOA": true, "INDEX": true,
	"STYLEREF": true, "DATE": true, "TIME": true, "CREATEDATE": true, "SAVEDATE": true,
	"PRINTDATE": true, "EDITTIME": true, "FILENAME": true, "FILESIZE": true, "NUMWORDS": true,
	"NUMCHARS": true, "REVNUM": true, "TEMPLATE": true, "LISTNUM": true, "AUTONUM": true,
	"AUTONUMLGL": true, "AUTONUMOUT": true, "HYPERLINK": true, "FORMTEXT": true,
	"FORMCHECKBOX": true, "FORMDROPDOWN": true, "MERGEFIELD": true, "MERGEREC": true,
	"MERGESEQ": true, "NEXT": true, "BIBLIOGRAPHY": true, "CITATION": true,
	"DOCPROPERTY": true, "DOCVARIABLE": true, "SYMBOL": true, "INCLUDEPICTURE": true,
	"INCLUDETEXT": true, "LINK": true, "EMBED": true, "SHAPE": true, "BARCODE": true,
	"TITLE": true, "SUBJECT": true, "AUTHOR": true, "KEYWORDS": true, "COMMENTS": true,
	"LASTSAVEDBY": true, "USERNAME": true, "USERINITIALS": true, "USERADDRESS": true,
	"QUOTE": true, "INFO": true, "DOCPROPERTIES": true, "GREETINGLINE": true, "ADDRESSBLOCK": true,
}

// maxPlainCode bounds the code of a structural field that is not kept.
const maxPlainCode = 4 << 10

// plainCode reports whether a structural field's code is no more than such
// a field needs: names, switches, a format, a list of styles, but no more
// than stored.MaxWords words of text in its quoted arguments.
func plainCode(code string) bool {
	return len(code) <= maxPlainCode && stored.QuotedWords(code) <= stored.MaxWords
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
	numID   int   // -1 not set, 0 none
	ilvl    int8  // -1 not set
	outline int8  // -1 not set, 9 body text
	shade   color // shading behind the paragraph
}

// paraState is what the runs of the paragraph being read need to know.
type paraState struct {
	start     int               // where its text begins in parser.text
	spanStart int               // where its spans begin in parser.spans
	base      *runProps         // the formatting its style and the defaults give its text
	own       uint8             // the on/off properties its style switches on, apart from the defaults
	hidden    gofastcore.Hidden // why text with only that formatting cannot be seen, if so
	behind    color             // what lies behind its text
	flags     clean.Flags       // how its text is cleaned
	verbatim  bool              // it is code: its text is kept as written
	display   []byte            // the text of its display equations
	seq       int               // its number, in the order paragraphs begin
	section   int               // the section it is in: properties it carries end it, after it
}

var noFormatting runProps

type cellProps struct {
	span     int
	vMerge   int8 // 0 none, 1 restart, 2 continue
	text     []byte
	hasBlock bool
	shade    color             // shading behind the cell
	cnf      int               // conditions of the table's style the cell says apply to it
	hidden   gofastcore.Hidden // reasons any of its text cannot be seen
}

type rowProps struct {
	header     bool
	deleted    bool
	gridBefore int
	clipped    bool // an exact height too small to show a line of text
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
		if s.Depth() == 2 && s.Is(ns.Word, "background") {
			// The page colour. Word shows it only when the document's
			// settings say so. A child element makes it a gradient or a
			// picture, which is not one colour; with DeepHidden, text is
			// judged against the gradient's two colours or the picture's.
			c := readColor(s, "color", "themeColor", p.r.theme)
			first := c
			for s.Next() != xmlscan.EOF && s.Depth() >= 2 {
				if s.Kind() != xmlscan.Start {
					continue
				}
				c = unknown
				if p.r.opts.DeepHidden && s.Is(ns.VML, "fill") {
					c.under = p.pageFill(s, first)
				}
			}
			if p.r.showBackground && c.set && !c.auto {
				p.r.page = c
			}
			continue
		}
		if s.Depth() >= 2 {
			s.Skip()
		}
	}
}

// emitBody appends a block to the body. Consecutive code paragraphs are
// one block of code, with or without spans: the blocks are the same
// whether spans are asked for or not.
func (p *parser) emitBody(out *[]gofastcore.Block, b gofastcore.Block) {
	if n := len(*out); n > 0 && b.Kind == gofastcore.KindCode && (*out)[n-1].Kind == gofastcore.KindCode &&
		b.Hidden == 0 && (*out)[n-1].Hidden == 0 {
		last := &(*out)[n-1]
		at := len(last.Text) + 1
		last.Text += "\n" + b.Text
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
	g := letter
	defer func() {
		p.r.endSection(notes)
		if len(p.r.pages) < 1<<16 {
			p.r.pages = append(p.r.pages, g)
		}
	}()
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		if s.Depth() == depth+1 && (s.Is(ns.Word, "pgSz") || s.Is(ns.Word, "pgMar")) {
			// Lengths in twentieths of a point; a margin given as
			// negative is one that text does not push.
			twips := func(name string, into *float64) {
				if v, ok := s.Attr(ns.Word, name); ok {
					if n, err := strconv.ParseInt(string(v), 10, 32); err == nil {
						*into = abs(float64(n)) / 20
					}
				}
			}
			if s.Is(ns.Word, "pgSz") {
				twips("w", &g.w)
				twips("h", &g.h)
			} else {
				twips("top", &g.top)
				twips("bottom", &g.bottom)
				twips("left", &g.left)
				twips("right", &g.right)
			}
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
		ref := furnitureRef{name: rel.Target, header: header, shown: true}
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
func readParaProps(s *xmlscan.Scanner, pp *paraProps, p *parser, theme map[string]color) {
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
		case "shd":
			pp.shade = readShade(s, theme)
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
	if v, ok := s.Attr(ns.Word14, "paraId"); ok && len(v) <= 32 {
		paraID = clean.Label(string(v))
	}
	pp := paraProps{numID: -1, ilvl: -1, outline: -1}
	if p.r.styles != nil {
		pp.style = p.r.styles.def
	}
	// A paragraph can sit inside another's run, in a text box; the outer
	// one's state is put back when this one is done.
	outer, outerHref, outerDepth := p.para, p.href, p.hrefDepth
	p.para = paraState{start: len(p.text), spanStart: len(p.spans), seq: p.r.paragraphs, section: len(p.r.sections)}
	p.r.paragraphs++
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
				if target := fieldLink(code); target != "" && p.r.opts.Spans {
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
				readParaProps(s, &pp, p, p.r.theme)
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
	p.text = p.text[:p.para.start]
	p.spans = p.spans[:p.para.spanStart]
	p.para, p.href, p.hrefDepth = outer, outerHref, outerDepth
	if !ok {
		return
	}
	// Word's own id for the paragraph: what a citation can point at in a
	// format with no pages.
	b.Loc.Anchor = paraID
	p.produced++
	emit(b)
}

// format works out what the paragraph's properties mean for its runs: the
// formatting they inherit, what lies behind them, and how their text is
// cleaned. It is called again when the properties are read, since they
// are the paragraph's first child.
func (p *parser) format(pp *paraProps) {
	st := &p.para
	st.base, st.flags, st.verbatim = &noFormatting, clean.Ligatures, false
	if p.r.styles != nil {
		st.base = &p.r.styles.defaults
	}
	st.behind = pp.shade
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
		if !st.behind.set || st.behind.auto {
			st.behind = pp.style.shade
		}
	}
	if !st.behind.set || st.behind.auto {
		st.behind = p.backdrop()
	}
	st.hidden = st.base.hiddenReasons(st.behind)
}

// backdrop returns what lies behind a paragraph that has no shading of
// its own. Nearest first: its table cell's shading, set on the cell or by
// the table's style; the fill of the text box it is in; the page.
func (p *parser) backdrop() color {
	if p.cell != nil {
		if p.cell.shade.set && !p.cell.shade.auto {
			return p.cell.shade
		}
		if p.tbl != nil {
			if c, ok := p.tbl.styleShade(p.cell.cnf); ok {
				return c
			}
			if p.tbl.shade.set && !p.tbl.shade.auto {
				return p.tbl.shade
			}
		}
	}
	if p.box.set {
		return p.box
	}
	return p.r.page
}

// hideWhole records that all of a block is hidden for the given reasons.
// The reasons then belong to the block, not to parts of it.
func hideWhole(b *gofastcore.Block, reasons gofastcore.Hidden) {
	b.Hidden |= reasons
	if b.Table != nil {
		kept := b.Table.Hidden[:0]
		for _, h := range b.Table.Hidden {
			if h.Hidden &^= reasons; h.Hidden != 0 {
				kept = append(kept, h)
			}
		}
		if b.Table.Hidden = kept; len(kept) == 0 {
			b.Table.Hidden = nil
		}
	}
	kept := b.Spans[:0]
	for _, sp := range b.Spans {
		if sp.Hidden &^= reasons; sp.Hidden != 0 || sp.Style != 0 || sp.Href != "" {
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
		return "#" + clean.Label(string(anchor))
	}
	return ""
}

// span records the formatting of the text of the current paragraph from
// start to the end of what has been read, joining it to the span before
// if that one ends where this begins and says the same.
func (p *parser) span(start int, style gofastcore.Style, href string, hidden gofastcore.Hidden) {
	from, to := start-p.para.start, len(p.text)-p.para.start
	if to <= from {
		return
	}
	if n := len(p.spans); n > p.para.spanStart {
		if last := &p.spans[n-1]; last.End == from && last.Style == style && last.Href == href && last.Hidden == hidden {
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
	p.spans = append(p.spans, gofastcore.Span{Start: from, End: to, Style: style, Href: href, Hidden: hidden})
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
	b.Spans, b.Hidden = settle(p.spans[p.para.spanStart:], from, to, raw)
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
// spans and hidden reasons of its block.
func settle(collected []gofastcore.Span, from, to int, raw []byte) ([]gofastcore.Span, gofastcore.Hidden) {
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
			readRunProps(s, &direct, p.r.theme)
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
			p.drawing()
		case "fldChar":
			// A field: its code, then what it displays. The code of a
			// HYPERLINK field says where the displayed text leads.
			p.fieldChar()
		case "instrText", "delInstrText":
			n := len(p.fields)
			if n > 0 && p.fields[n-1].inCode && (s.Is(ns.Word, "delInstrText") == deleted) && len(p.fields[n-1].code) < maxFieldCode {
				p.fields[n-1].code = s.AppendText(p.fields[n-1].code)
			} else {
				s.Skip()
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
	hidden := p.para.hidden
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
		hidden = direct.hiddenReasons(p.para.behind)
	}
	// What hangs on a hidden run is hidden with it: the footnote whose
	// reference it holds, the text box or picture anchored in it. A
	// comment is not; it is shown in the margin whatever it is about.
	if hidden&gofastcore.HiddenInvisible != 0 {
		for i := hung; i < len(p.after); i++ {
			if b := &p.after[i]; b.Kind != gofastcore.KindNote {
				hideWhole(b, gofastcore.HiddenInvisible)
			}
		}
	}
	if hidden != 0 || style != 0 || href != "" {
		p.span(start, style, href, hidden)
	}
}

// fieldLink returns the target of a HYPERLINK field from its code, such as
// HYPERLINK "https://example.org" or HYPERLINK \l "bookmark", or "".
func fieldLink(code []byte) string {
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
			target := clean.Address(strings.Trim(f, `"`))
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
		p.after = append(p.after, gofastcore.Block{Kind: gofastcore.KindFootnote, Marker: marker, Text: n.text, Spans: n.spans, Hidden: n.hidden})
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
	p.after = append(p.after, gofastcore.Block{Kind: gofastcore.KindNote, Text: n.text, Spans: n.spans, Hidden: n.hidden, Attrs: attrs})
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
// the paragraph they are anchored in.
func (p *parser) drawing() {
	s := &p.s
	var alt, target, name string
	var external bool
	// A text box of a fixed size shows no more than fits in it. One
	// smaller than a line of text in either direction shows nothing.
	width, height := -1.0, -1.0 // in points; -1 unknown
	autofit := false
	boxes := len(p.after)
	hasBox := false
	// The fill of the shape being read, which is what the text of its
	// text box is seen against. Unset is no fill.
	var fill color
	shapes := 0      // shapes seen: sizes are only understood for one alone
	shapeProps := 0  // depth of the shape's properties while they are read
	ownFill := false // the shape sets its fill itself, not through its style
	// Where it floats, if it is anchored at a place of its own, and the
	// fill of its one shape, which may hide what is under it.
	anchor := float{paragraph: p.para.seq, section: p.para.section}
	anchored := false
	var shapeFill color
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
		if shapeProps > 0 && s.Depth() <= shapeProps {
			shapeProps = 0
		}
		switch {
		case s.Is(ns.WordDraw, "anchor"):
			anchored = true
			readAnchor(s, &anchor)
		case s.Is(ns.WordDraw, "simplePos"):
			readSimplePos(s, &anchor)
		case s.Is(ns.WordDraw, "positionH"):
			readPosition(s, &anchor.h)
		case s.Is(ns.WordDraw, "positionV"):
			readPosition(s, &anchor.v)
		case s.Is(ns.WordShape, "wsp"):
			fill, ownFill, shapeProps = color{}, false, 0
			shapes++
		case s.Is(ns.WordShape, "spPr"):
			shapeProps = s.Depth()
		case shapeProps > 0 && s.Depth() == shapeProps+1 && s.Space() == ns.Draw:
			if f, ok := dml.ReadFill(s); ok {
				fill, ownFill = p.r.drawingFill(p.picture(f)), true
			} else if s.Is(ns.Draw, "ln") {
				s.Skip() // the outline has a fill of its own
			}
		case s.Is(ns.Draw, "fillRef"):
			// The fill the shape's style gives it, unless it has its own:
			// one of the theme's fill styles, in a colour.
			if idx, ok := s.Attr(xmlscan.NoSpace, "idx"); ok && !ownFill {
				n, _ := atoi(idx)
				fill = p.r.drawingFill(p.r.drawn.StyleFill(n, dml.ReadColor(s)))
			}
		case s.Is(ns.VML, "fill"):
			fill = vmlFillElement(s, fill)
		case s.Is(ns.WordDraw, "docPr"):
			if v, ok := s.Attr(xmlscan.NoSpace, "name"); ok {
				name = clean.Name(string(v))
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
			if v, ok := s.Attr(xmlscan.NoSpace, "style"); ok {
				width, height = vmlSize(v)
				autofit = autofit || bytes.Contains(v, []byte("mso-fit-shape-to-text:t"))
			}
			fill = vmlFill(s)
			shapes++
		case s.Is(ns.WordDraw, "extent"):
			// Sizes are in EMU: 12,700 to the point.
			if cx, ok := emuAttr(s, "cx"); ok {
				width = cx
			}
			if cy, ok := emuAttr(s, "cy"); ok {
				height = cy
			}
		case s.Is(ns.Draw, "spAutoFit"):
			autofit = true // the box grows to fit its text
		case s.Is(ns.Word, "txbxContent"):
			// A text box: its paragraphs follow the one it hangs on.
			hasBox = true
			shapeFill = fill
			if !fill.set {
				fill = p.backdrop() // nothing of its own: what the box sits on shows through
			}
			outerBox, outerCell, outerTable := p.box, p.cell, p.tbl
			p.box, p.cell, p.tbl = fill, nil, nil
			p.container(func(b gofastcore.Block) { p.after = append(p.after, b) })
			p.box, p.cell, p.tbl = outerBox, outerCell, outerTable
			fill = color{}
		case s.Is(ns.Chart, "chart"), s.Is(ns.ChartEx, "chart"):
			p.chart(name)
		case s.Is(ns.Diagram, "relIds"):
			p.diagram()
		}
	}
	if hasBox && shapes <= 1 && !autofit && (width >= 0 && width < gofastcore.HiddenTinySize || height >= 0 && height < gofastcore.HiddenTinySize) {
		for i := boxes; i < len(p.after); i++ {
			if b := &p.after[i]; b.Kind != gofastcore.KindImage {
				hideWhole(b, gofastcore.HiddenClipped)
			}
		}
	}
	if !shapeFill.set {
		shapeFill = fill
	}
	if anchored && shapes <= 1 && len(p.r.floats) < maxFloats {
		p.float(anchor, boxes, hasBox, shapeFill, target, external, width, height)
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
		p.after = append(p.after, gofastcore.Block{
			Kind: gofastcore.KindImage, Text: alt, Image: &gofastcore.Image{},
			Attrs: map[string]string{"href": target},
		})
		return
	}
	p.after = append(p.after, p.r.imageBlock(target, alt))
}

// emuAttr reads a length attribute given in EMU and returns it in points.
func emuAttr(s *xmlscan.Scanner, name string) (float64, bool) {
	v, ok := s.Attr(xmlscan.NoSpace, name)
	if !ok {
		return 0, false
	}
	n, ok := atoi(v)
	return float64(n) / 12700, ok
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
		case s.Is(ns.Word, "shd"):
			p.cell.shade = readShade(s, p.r.theme)
		case s.Is(ns.Word, "cnfStyle"):
			p.cell.cnf = readCnf(s)
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
	hidden gofastcore.Hidden
}

// table reads w:tbl into a table block.
func (p *parser) table(emit func(gofastcore.Block)) {
	s := &p.s
	p.tables++
	savedCell, savedRow, savedTable := p.cell, p.row, p.tbl
	state := tableState{}
	p.tbl, p.cell = &state, nil
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
		if s.Is(ns.Word, "tblPr") {
			// What decides the shading behind the table's cells.
			inner := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= inner {
				if s.Kind() != xmlscan.Start || s.Depth() != inner+1 {
					continue
				}
				switch {
				case s.Is(ns.Word, "tblStyle"):
					if v, ok := s.Attr(ns.Word, "val"); ok {
						if p.r.styles == nil {
							// No styles part: the built-in styles are still known.
							p.r.styles = &styles{byID: map[string]*style{}, chars: map[string]*charStyle{}, tables: map[string]*tableStyle{}}
						}
						state.style = p.r.styles.table(string(v), p.r.theme)
					}
				case s.Is(ns.Word, "tblLook"):
					state.look = readLook(s)
				case s.Is(ns.Word, "shd"):
					state.shade = readShade(s, p.r.theme)
				default:
					s.Skip()
				}
			}
			continue
		}
		if !s.Is(ns.Word, "tr") {
			s.Skip()
			continue
		}
		state.row = len(rows)
		row, props := p.tableRow()
		if props.deleted {
			p.r.deleted++
			continue
		}
		if props.clipped {
			for i := range row {
				if row[i].text != "" {
					row[i].hidden |= gofastcore.HiddenClipped
				}
			}
		}
		if counting && props.header {
			headerRows++
		} else {
			counting = false
		}
		rows = append(rows, row)
		before = append(before, props.gridBefore)
	}
	p.cell, p.row, p.tbl = savedCell, savedRow, savedTable
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
					spans = append(spans, gofastcore.Span{Start: at, End: len(line), Hidden: c.hidden})
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
				b.Spans, b.Hidden = settle(spans, from, to, line)
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
	var hidden gofastcore.Hidden
	open := make([]int, cols)   // per column: index in t.Merges of the vertical merge running through it, plus one
	marked := make([]int, cols) // per column: index in t.Hidden of the entry of that merge's first cell, plus one
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
					top := &t.Rows[m.Row][m.Col]
					if *top != "" {
						*top += "\n"
					}
					*top += c.text
					if c.hidden != 0 {
						// The text has joined the merge's first cell, and
						// so does what is said of it.
						if marked[col] == 0 {
							t.Hidden = append(t.Hidden, gofastcore.HiddenCells{Row: m.Row, Col: m.Col, RowSpan: 1, ColSpan: 1})
							marked[col] = len(t.Hidden)
						}
						t.Hidden[marked[col]-1].Hidden |= c.hidden
						hidden |= c.hidden
					}
				}
			default:
				t.Rows[r][col] = c.text
				for k := col; k < col+c.span && k < cols; k++ {
					open[k], marked[k] = 0, 0
				}
				if c.hidden != 0 && c.text != "" {
					t.Hidden = append(t.Hidden, gofastcore.HiddenCells{Row: r, Col: col, RowSpan: 1, ColSpan: 1, Hidden: c.hidden})
					marked[col] = len(t.Hidden)
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
	emit(gofastcore.Block{Kind: gofastcore.KindTable, Table: t, Hidden: hidden})
}

// tableRow reads w:tr.
func (p *parser) tableRow() ([]tableCell, rowProps) {
	s := &p.s
	var row []tableCell
	var props rowProps
	if p.tbl != nil {
		p.tbl.col, p.tbl.rowCnf = 0, 0
	}
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
						if p.tbl != nil {
							p.tbl.col = props.gridBefore
						}
					}
				case s.Is(ns.Word, "cnfStyle"):
					if p.tbl != nil {
						p.tbl.rowCnf = readCnf(s)
					}
				case s.Is(ns.Word, "trHeight"):
					// A row of an exact height shows no more than fits in
					// it. Heights are in twentieths of a point.
					rule, _ := s.Attr(ns.Word, "hRule")
					if v, ok := intAttr(s, "val"); ok && string(rule) == "exact" && float64(v)/20 < gofastcore.HiddenTinySize {
						props.clipped = true
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
				// A cell is text without parts, so it is flagged as a
				// whole when any of its text is hidden.
				cell.hidden |= b.Hidden
			})
			p.cell = nil
			if p.tbl != nil {
				p.tbl.col += cell.span
			}
			row = append(row, tableCell{text: string(cell.text), span: cell.span, vMerge: cell.vMerge, hidden: cell.hidden})
		default:
			s.Skip()
		}
	}
}

// picture turns the relationship of a fill's picture into its part's
// name, as this part sees it.
func (p *parser) picture(f dml.Fill) dml.Fill {
	if f.Blip == "" {
		return f
	}
	if rel, ok := p.rels[f.Blip]; ok && !rel.External {
		f.Blip = rel.Target
	} else {
		f.Blip = ""
	}
	return f
}

// pageFill reads the v:fill of the page's background: a gradient from the
// page colour to color2, or a picture, stretched or tiled, whose colours
// are those of the page either way.
func (p *parser) pageFill(s *xmlscan.Scanner, first color) []theme.Color {
	typ, _ := s.Attr(xmlscan.NoSpace, "type")
	if id, ok := s.Attr(ns.Rel, "id"); ok && string(typ) != "gradient" && string(typ) != "gradientRadial" && string(typ) != "pattern" {
		return p.r.deepColors(p.picture(dml.Fill{Paint: dml.PaintOther, Blip: string(id)}))
	}
	if string(typ) != "gradient" && string(typ) != "gradientRadial" {
		return nil
	}
	second := white
	if v, ok := s.Attr(xmlscan.NoSpace, "color2"); ok {
		second = vmlColor(v)
	}
	if !first.set || first.auto || first.unknown || !second.set || second.unknown {
		return nil
	}
	return []theme.Color{{R: first.r, G: first.g, B: first.b}, {R: second.r, G: second.g, B: second.b}}
}
