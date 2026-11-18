package wml

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
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

	text  []byte             // text of the paragraphs being built, innermost last
	after []gofastcore.Block // blocks that follow the paragraph or table in progress
	cell  *cellProps         // properties of the table cell being read, if any
	row   *rowProps

	tables   int // tables being read; notes wait until the outermost is out
	produced int // text and images emitted so far, for alternate content
	alts     []alternate
	polls    int
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
	header  bool // carries section properties (their references were read)
}

type cellProps struct {
	span     int
	vMerge   int8 // 0 none, 1 restart, 2 continue
	text     []byte
	hasBlock bool
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
			s.Skip() // the page background and the like
		}
	}
}

// emitBody appends a block to the body. Consecutive code paragraphs are
// one block of code.
func (p *parser) emitBody(out *[]gofastcore.Block, b gofastcore.Block) {
	if n := len(*out); n > 0 && b.Kind == gofastcore.KindCode && (*out)[n-1].Kind == gofastcore.KindCode {
		(*out)[n-1].Text += "\n" + b.Text
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
func (p *parser) section() {
	s := &p.s
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
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
		rel, ok := p.rels[string(id)]
		if !ok || rel.External {
			continue
		}
		if header {
			p.r.headers = append(p.r.headers, rel.Target)
		} else {
			p.r.footers = append(p.r.footers, rel.Target)
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
	if v, ok := s.Attr(ns.Word14, "paraId"); ok && len(v) <= 32 {
		paraID = clean.Label(string(v))
	}
	pp := paraProps{numID: -1, ilvl: -1, outline: -1}
	if p.r.styles != nil {
		pp.style = p.r.styles.def
	}
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
			case "del":
				p.deletion()
			case "moveFrom":
				// The text was moved, not removed: it is read where it went.
				s.Skip()
			default:
				s.Skip()
			}
		case ns.Math:
			// An equation: its characters are kept in order, its layout
			// (fractions, exponents) is not reconstructed.
			p.text = s.AppendText(p.text)
		default:
			s.Skip()
		}
	}
	raw := p.text[start:]
	p.text = p.text[:start]
	b, ok := p.block(&pp, raw)
	if !ok {
		return
	}
	// Word's own id for the paragraph: what a citation can point at in a
	// format with no pages.
	b.Loc.Anchor = paraID
	p.produced++
	emit(b)
}

// block turns a paragraph's properties and text into a block.
func (p *parser) block(pp *paraProps, raw []byte) (gofastcore.Block, bool) {
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
	flags := clean.Prose
	if kind == roleCode {
		flags = clean.Verbatim
	}
	text := p.clean(raw, flags)
	if kind == roleCode {
		text = strings.TrimRight(text, " \t\n")
		if strings.TrimSpace(text) == "" {
			text = ""
		}
	}
	if text == "" {
		// An empty paragraph is spacing. A numbered one still counts in
		// its list, as it does in Word.
		if numID > 0 {
			p.label(pp, numID, ilvl)
		}
		return gofastcore.Block{}, false
	}

	b := gofastcore.Block{Kind: gofastcore.KindParagraph, Text: text}
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
	case kind == roleCode:
		b.Kind = gofastcore.KindCode
	}
	return b, true
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
		case "t":
			if deleted {
				s.Skip()
				continue
			}
			p.runText()
		case "delText":
			if deleted {
				p.runText()
			} else {
				s.Skip()
			}
		case "tab", "ptab":
			p.text = append(p.text, '\t')
		case "br", "cr":
			p.text = append(p.text, '\n')
		case "noBreakHyphen":
			p.text = append(p.text, "‑"...)
		case "softHyphen":
			p.text = append(p.text, "\u00AD"...)
		case "sym":
			p.symbol()
		case "ruby":
			p.ruby()
		case "footnoteReference":
			p.noteRef(p.r.footnotes, &p.r.footnoteN, "decimal")
		case "endnoteReference":
			p.noteRef(p.r.endnotes, &p.r.endnoteN, "lowerRoman")
		case "commentReference":
			p.commentRef()
		case "drawing", "pict", "object":
			p.drawing()
		default:
			// Run properties, field codes (w:instrText), and marks with
			// no text of their own.
			s.Skip()
		}
	}
}

// runText appends the content of w:t.
//
// White space at the ends of a text element is dropped unless the element
// asks for it to be kept with xml:space="preserve", which is what Word and
// LibreOffice do. Word writes that attribute whenever a run begins or ends
// with a space, so in its own files nothing is lost by the rule; in a file
// made by another program it reproduces what a reader sees in Word.
func (p *parser) runText() {
	s := &p.s
	space, _ := s.Attr(xmlscan.SpaceXML, "space")
	preserve := string(space) == "preserve"
	start := len(p.text)
	p.text = s.AppendText(p.text)
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
	p.text = utf8.AppendRune(p.text, r)
}

// noteRef handles a reference to a footnote or endnote: the note becomes a
// block after the paragraph, labelled with the number a reader sees.
func (p *parser) noteRef(notes map[int]note, counter *int, format string) {
	s := &p.s
	id, ok := intAttr(s, "id")
	custom, has := s.Attr(ns.Word, "customMarkFollows")
	marker := ""
	if !has || !isOn(custom) {
		*counter++
		marker = formatNumber(*counter, format)
	}
	// A note is given once, at its first reference. A file that refers to
	// one note many times does not get it copied each time.
	key := format + strconv.Itoa(id)
	if n, found := notes[id]; ok && found && n.text != "" && !p.r.usedNotes[key] {
		p.r.usedNotes[key] = true
		p.after = append(p.after, gofastcore.Block{Kind: gofastcore.KindFootnote, Marker: marker, Text: n.text})
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
	p.after = append(p.after, gofastcore.Block{Kind: gofastcore.KindNote, Text: n.text, Attrs: attrs})
}

// deletion reads w:del inside a paragraph: text removed by a tracked
// change. It is left out of the paragraph and kept as a note after it.
func (p *parser) deletion() {
	s := &p.s
	author, _ := s.Attr(ns.Word, "author")
	date, _ := s.Attr(ns.Word, "date")
	attrs := map[string]string{gofastcore.AttrNoteType: gofastcore.NoteDeletion}
	if a := p.clean(author, clean.Trim); a != "" {
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
	var alt, target string
	var external bool
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
		case s.Is(ns.VML, "shape"):
			if v, ok := s.Attr(xmlscan.NoSpace, "alt"); ok && alt == "" {
				alt = p.clean(v, clean.Prose)
			}
		case s.Is(ns.Word, "txbxContent"):
			// A text box: its paragraphs follow the one it hangs on.
			p.container(func(b gofastcore.Block) { p.after = append(p.after, b) })
		case s.Is(ns.Chart, "chart"):
			p.r.unread["chart"]++
		case s.Is(ns.Diagram, "relIds"):
			p.r.unread["diagram"]++
		}
	}
	if target == "" || p.r.opts.Images == gofastcore.ImagesNone {
		if target != "" {
			p.r.sawImage = true
		}
		return
	}
	p.produced++
	if external {
		// A picture that is not in the file: named, not fetched.
		p.after = append(p.after, gofastcore.Block{
			Kind: gofastcore.KindImage, Text: alt, Image: &gofastcore.Image{},
			Attrs: map[string]string{"href": clean.Label(target)},
		})
		return
	}
	p.after = append(p.after, p.r.imageBlock(target, alt))
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
}

// table reads w:tbl into a table block.
func (p *parser) table(emit func(gofastcore.Block)) {
	s := &p.s
	p.tables++
	savedCell, savedRow := p.cell, p.row
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
			var sb strings.Builder
			for i, c := range row {
				if i > 0 {
					sb.WriteByte('\t')
				}
				sb.WriteString(strings.ReplaceAll(c.text, "\n", " "))
			}
			if text := strings.TrimSpace(sb.String()); text != "" {
				p.produced++
				emit(gofastcore.Block{Kind: gofastcore.KindParagraph, Text: text})
			}
		}
		return
	}
	t := &gofastcore.Table{HeaderRows: min(headerRows, len(rows)), Rows: make([][]string, len(rows))}
	open := make([]int, cols) // per column: index in t.Merges of the vertical merge running through it, plus one
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
				}
			default:
				t.Rows[r][col] = c.text
				for k := col; k < col+c.span && k < cols; k++ {
					open[k] = 0
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
			open[k] = 0
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
	emit(gofastcore.Block{Kind: gofastcore.KindTable, Table: t})
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
			})
			p.cell = nil
			row = append(row, tableCell{text: string(cell.text), span: cell.span, vMerge: cell.vMerge})
		default:
			s.Skip()
		}
	}
}
