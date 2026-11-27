// Package wordml translates a WordprocessingML 2003 document (Word 2003's
// "XML Document", root w:wordDocument) into the parts of the .docx it
// corresponds to, so that the Word reader reads it as it reads any other.
//
// WordprocessingML 2003 is the direct ancestor of the WordprocessingML of
// .docx, and most of it is the same elements in another namespace. What
// differs is translated:
//
//   - Names that 2003 hyphenates are camel-cased (sz-cs is szCs,
//     line-rule lineRule, h-ansi hAnsi), and fareast is eastAsia.
//   - The document is one file: styles, lists (w:lists) and settings
//     (w:docPr) become their parts; footnotes, endnotes and comments,
//     written where they are cited, go into their parts, with a reference
//     where they were; headers and footers, written in their section's
//     w:sectPr, become parts that the section names.
//   - Lists: w:listPr (w:ilvl, w:ilfo) is w:numPr; w:listDef and w:list
//     are w:abstractNum and w:num, a level's w:nfc (a number) is w:numFmt
//     (a name).
//   - Revisions and comments are aml:annotation elements: an insertion
//     is w:ins, a deletion w:del, a comment's range and body are
//     w:commentRangeStart, w:commentRangeEnd and a comment, a bookmark is
//     w:bookmarkStart and w:bookmarkEnd.
//   - Links are w:hlink with the address in w:dest, not a relationship.
//   - Pictures are VML whose v:imagedata names a w:binData, the picture
//     in base64, by a wordml:// name; the picture becomes a media part.
//   - wx:sect and wx:sub-section, which only group, are left out around
//     what they hold; so are smart tags and other elements of namespaces
//     the format does not define. Auxiliary hints (wx:) and Word's
//     revision session ids (wsp:) are dropped.
package wordml

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/AOShei/go-fast-core/xmlscan"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/xmlindex"
)

// Namespaces as URIs.
const (
	wordNS   = "http://schemas.microsoft.com/office/word/2003/wordml"
	officeNS = "urn:schemas-microsoft-com:office:office"
)

// Parts of the translation.
const (
	Main = "word/document.xml"
	// Props and Custom are the document's properties, read where they
	// are in the file.
	Props  = "documentproperties"
	Custom = "customdocumentproperties"
)

// ErrBudget: the pictures decoded, and the parts written, came to more
// than the budget allows.
var ErrBudget = errors.New("wordml: the translation is larger than the decompressed size limit")

// Result is a translation: the parts to make a package of, and how the
// reading of the file went.
type Result struct {
	Sections []opc.Section
	// Err is why the body could not be read to its end, if it could
	// not; what was read before is in the translation.
	Err error
	// CharRefs counts the character references to no character XML
	// allows (&#0;, a surrogate, a number past U+10FFFF) in what was
	// translated, each read as U+FFFD (xmlscan's Stats.CharRefsReplaced).
	CharRefs int
}

// scanOptions maps the namespaces the translation tells apart.
var scanOptions = &xmlscan.Options{Spaces: ns.Spaces}

// Translate translates the document x indexes, within budget bytes of
// pictures and parts.
func Translate(x *xmlindex.Index, budget int64) (*Result, error) {
	t := &translator{x: x, budget: budget, rels: map[string]*part{}}
	res := &Result{}
	var body, styles, lists, docPr *xmlindex.Element
	for _, e := range x.Root.Children {
		switch {
		case e.Is(wordNS, "body"):
			body = e
		case e.End == 0:
		case e.Is(wordNS, "styles"):
			styles = e
		case e.Is(wordNS, "lists"):
			lists = e
		case e.Is(wordNS, "docPr"):
			docPr = e
		case e.Is(officeNS, "DocumentProperties"):
			res.Sections = append(res.Sections, opc.Section{Name: Props, Off: e.Start, N: e.End - e.Start, Bindings: e.Outer})
		case e.Is(officeNS, "CustomDocumentProperties"):
			res.Sections = append(res.Sections, opc.Section{Name: Custom, Off: e.Start, N: e.End - e.Start, Bindings: e.Outer})
		}
	}
	doc := t.newPart(Main, "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml", "")
	if styles != nil {
		p := t.newPart("word/styles.xml", "application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml", "styles")
		doc.rel("styles", "styles.xml", false)
		t.section(styles, func(s *xmlscan.Scanner) { t.element(s, p) })
	}
	if lists != nil {
		p := t.newPart("word/numbering.xml", "application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml", "numbering")
		doc.rel("numbering", "numbering.xml", false)
		t.section(lists, func(s *xmlscan.Scanner) { t.lists(s, p) })
	}
	if docPr != nil {
		p := t.newPart("word/settings.xml", "application/vnd.openxmlformats-officedocument.wordprocessingml.settings+xml", "settings")
		doc.rel("settings", "settings.xml", false)
		t.section(docPr, func(s *xmlscan.Scanner) { t.settings(s, p) })
	}
	doc.open("w:document")
	doc.open("w:body")
	if body != nil {
		// A body the file breaks off in is read up to where it does.
		end := body.End
		if end == 0 {
			end = x.Size
		}
		var s xmlscan.Scanner
		s.Reset(io.NewSectionReader(x.Src, body.Start, end-body.Start), &xmlscan.Options{Spaces: ns.Spaces, Bindings: body.Outer})
		if s.Next() == xmlscan.Start {
			t.children(&s, doc)
		}
		if err := s.Err(); err != nil {
			res.Err = err
		}
		t.refs += s.Stats.CharRefsReplaced
		s.Release()
	}
	doc.closeAll()
	for _, n := range []struct {
		p    *part
		kind string
	}{{t.footnotes, "footnotes"}, {t.endnotes, "endnotes"}, {t.comments, "comments"}} {
		if n.p != nil {
			n.p.closeAll()
			doc.rel(n.kind, path.Base(n.p.name), false)
		}
	}
	if t.over {
		return nil, ErrBudget
	}
	res.CharRefs = t.refs
	for _, p := range t.parts {
		p.closeAll()
		res.Sections = append(res.Sections, opc.Section{Name: p.name, ContentType: p.contentType, Data: p.buf.Bytes()})
		if len(p.rels) > 0 {
			res.Sections = append(res.Sections, opc.Section{Name: opc.RelsName(p.name), ContentType: "application/vnd.openxmlformats-package.relationships+xml", Data: p.relsXML(t.byName)})
		}
	}
	for _, m := range t.media {
		res.Sections = append(res.Sections, opc.Section{Name: m.name, ContentType: m.contentType, Data: m.data})
	}
	return res, nil
}

type translator struct {
	x      *xmlindex.Index
	budget int64
	over   bool
	parts  []*part
	rels   map[string]*part

	footnotes, endnotes, comments *part
	notes                         int // note ids given
	headers, footers              int

	media  []media
	byName map[string]string // a binData's wordml name -> its part, from the part's directory
	refs   int               // character references to no character, as Result.CharRefs
}

type media struct {
	name, contentType string
	data              []byte
}

// section reads an element of the file with read, the scanner at its
// start tag.
func (t *translator) section(e *xmlindex.Element, read func(*xmlscan.Scanner)) {
	var s xmlscan.Scanner
	s.Reset(t.x.Section(e), &xmlscan.Options{Spaces: ns.Spaces, Bindings: e.Outer})
	defer s.Release()
	if s.Next() == xmlscan.Start {
		read(&s)
	}
	t.refs += s.Stats.CharRefsReplaced
}

// part is a part being written.
type part struct {
	name, contentType string
	buf               bytes.Buffer
	stack             []string // the elements open, to close them all if reading stops
	rels              []rel
	declared          bool // the root's namespaces are written
}

type rel struct {
	id, typ, target string
	external        bool
}

const relBase = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/"

func (t *translator) newPart(name, contentType, _ string) *part {
	p := &part{name: name, contentType: contentType}
	t.parts = append(t.parts, p)
	return p
}

// rel adds a relationship of the part and returns its id.
func (p *part) rel(kind, target string, external bool) string {
	id := "rId" + strconv.Itoa(len(p.rels)+1)
	p.rels = append(p.rels, rel{id, relBase + kind, target, external})
	return id
}

// pendingPrefix marks the target of a picture's relationship that names
// its binData, settled by relsXML.
const pendingPrefix = "\x00"

func (p *part) relsXML(byName map[string]string) []byte {
	var b bytes.Buffer
	b.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for _, r := range p.rels {
		if name, ok := strings.CutPrefix(r.target, pendingPrefix); ok {
			target := byName[name]
			if target == "" {
				continue // no picture of that name: the reference leads nowhere
			}
			r.target = "media/" + target
		}
		b.WriteString(`<Relationship Id="` + r.id + `" Type="` + r.typ + `" Target="`)
		escape(&b, r.target, true)
		b.WriteString(`"`)
		if r.external {
			b.WriteString(` TargetMode="External"`)
		}
		b.WriteString(`/>`)
	}
	b.WriteString(`</Relationships>`)
	return b.Bytes()
}

// The namespaces every part declares on its root.
const declarations = ` xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"` +
	` xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"` +
	` xmlns:v="urn:schemas-microsoft-com:vml" xmlns:o="urn:schemas-microsoft-com:office:office"` +
	` xmlns:w10="urn:schemas-microsoft-com:office:word"`

// start writes a start tag; attrs are name, value pairs.
func (p *part) start(name string, attrs ...string) {
	p.buf.WriteByte('<')
	p.buf.WriteString(name)
	if !p.declared {
		p.buf.WriteString(declarations)
		p.declared = true
	}
	for i := 0; i+1 < len(attrs); i += 2 {
		p.attr(attrs[i], attrs[i+1])
	}
}

func (p *part) attr(name, value string) {
	p.buf.WriteByte(' ')
	p.buf.WriteString(name)
	p.buf.WriteString(`="`)
	escape(&p.buf, value, true)
	p.buf.WriteByte('"')
}

// open writes a start tag, closed.
func (p *part) open(name string, attrs ...string) {
	p.start(name, attrs...)
	p.buf.WriteByte('>')
	p.stack = append(p.stack, name)
}

// empty writes an empty element.
func (p *part) empty(name string, attrs ...string) {
	p.start(name, attrs...)
	p.buf.WriteString("/>")
}

func (p *part) close() {
	name := p.stack[len(p.stack)-1]
	p.stack = p.stack[:len(p.stack)-1]
	p.buf.WriteString("</" + name + ">")
}

func (p *part) closeAll() {
	for len(p.stack) > 0 {
		p.close()
	}
}

func (p *part) text(b []byte) { escape(&p.buf, string(b), false) }

func escape(b *bytes.Buffer, s string, attr bool) {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '<':
			b.WriteString("&lt;")
		case c == '>':
			b.WriteString("&gt;")
		case c == '&':
			b.WriteString("&amp;")
		case c == '"' && attr:
			b.WriteString("&quot;")
		case (c == '\n' || c == '\r' || c == '\t') && attr:
			b.WriteString("&#" + strconv.Itoa(int(c)) + ";")
		default:
			b.WriteByte(c)
		}
	}
}

// modern returns the name WordprocessingML of .docx gives what 2003
// names name: hyphens become capitals.
func modern(name []byte) string {
	switch string(name) {
	case "fareast":
		return "eastAsia"
	case "vmerge":
		return "vMerge"
	case "hmerge":
		return "hMerge"
	}
	if bytes.IndexByte(name, '-') < 0 {
		return string(name)
	}
	var b strings.Builder
	upper := false
	for _, c := range name {
		switch {
		case c == '-':
			upper = true
		case upper && c >= 'a' && c <= 'z':
			b.WriteByte(c - 'a' + 'A')
			upper = false
		default:
			b.WriteByte(c)
			upper = false
		}
	}
	return b.String()
}

// prefix returns the prefix an element or attribute of the namespace is
// written with, or "" for one that is not written.
func prefix(space xmlscan.Space) string {
	switch space {
	case ns.Word2003:
		return "w"
	case ns.VML:
		return "v"
	case ns.Office:
		return "o"
	case ns.Word10:
		return "w10"
	}
	return ""
}

// copyAttrs writes the attributes of the current start tag that the
// translation keeps: those of the namespaces written, and those of no
// namespace, renamed; skip names ones left out.
func (t *translator) copyAttrs(s *xmlscan.Scanner, p *part, skip ...string) {
	for i := range s.NumAttr() {
		qname := s.AttrNameAt(i)
		if bytes.HasPrefix(qname, []byte("xmlns")) || bytes.HasPrefix(qname, []byte("xml:")) {
			continue
		}
		space, local, value := s.AttrAt(i)
		name := string(local)
		if space != xmlscan.NoSpace {
			pre := prefix(space)
			if pre == "" {
				continue
			}
			name = pre + ":" + modern(local)
		}
		left := false
		for _, n := range skip {
			left = left || n == name
		}
		if !left {
			p.attr(name, string(value))
		}
	}
}

// children translates the content of the element whose start tag is
// current, up to its end tag.
func (t *translator) children(s *xmlscan.Scanner, p *part) {
	depth := s.Depth()
	for {
		switch s.Next() {
		case xmlscan.EOF:
			return
		case xmlscan.End:
			if s.Depth() < depth {
				return
			}
		case xmlscan.Start:
			t.element(s, p)
		case xmlscan.Text:
			p.text(s.Text())
		}
	}
}

// element translates the element whose start tag is current, through its
// end tag.
func (t *translator) element(s *xmlscan.Scanner, p *part) {
	space, local := s.Space(), string(s.Local())
	switch space {
	case ns.Word2003:
		switch local {
		case "listPr":
			t.listPr(s, p)
			return
		case "hlink":
			t.hlink(s, p)
			return
		case "footnote", "endnote":
			t.note(s, p, local)
			return
		case "hdr", "ftr":
			t.furniture(s, p, local)
			return
		case "binData":
			t.binData(s)
			return
		case "fonts", "lsdException":
			s.Skip()
			return
		}
	case ns.VML:
		if local == "imagedata" {
			t.imagedata(s, p)
			return
		}
	case ns.Office, ns.Word10:
	case ns.AML:
		t.annotation(s, p)
		return
	case ns.WordAux:
		switch local {
		case "sect", "sub-section", "pBdrGroup":
			t.children(s, p) // only groups what it holds
		default:
			s.Skip() // a hint
		}
		return
	default:
		// A smart tag, or an element of a namespace the format does not
		// define: what it holds is read, as if it were not there.
		t.children(s, p)
		return
	}
	name := prefix(space) + ":" + modern(s.Local())
	if space == ns.Word2003 && local == "nfc" {
		p.empty("w:numFmt", "w:val", numFormat(attrW(s, "val")))
		s.Skip()
		return
	}
	p.start(name)
	t.copyAttrs(s, p)
	if space == ns.Word2003 {
		switch local {
		case "t", "delText", "instrText":
			// Word writes xml:space="preserve" once, on the root: its
			// text keeps its spaces.
			p.attr("xml:space", "preserve")
		}
	}
	p.buf.WriteByte('>')
	p.stack = append(p.stack, name)
	t.children(s, p)
	p.close()
}

// attrW returns the value of the attribute w:name of the current start
// tag.
func attrW(s *xmlscan.Scanner, name string) string {
	v, _ := s.Attr(ns.Word2003, name)
	return string(v)
}

// listPr translates a paragraph's list properties: its level, and its
// list, by number; the label Word wrote as a hint is not needed.
func (t *translator) listPr(s *xmlscan.Scanner, p *part) {
	var ilvl, ilfo string
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start || s.Space() != ns.Word2003 {
			continue
		}
		switch string(s.Local()) {
		case "ilvl":
			ilvl = attrW(s, "val")
		case "ilfo":
			ilfo = attrW(s, "val")
		}
	}
	p.open("w:numPr")
	if ilvl != "" {
		p.empty("w:ilvl", "w:val", ilvl)
	}
	if ilfo != "" {
		p.empty("w:numId", "w:val", ilfo)
	}
	p.close()
}

// numFormat names a number format given as a number (w:nfc), as Word's
// binary format numbers them.
func numFormat(nfc string) string {
	switch nfc {
	case "0":
		return "decimal"
	case "1":
		return "upperRoman"
	case "2":
		return "lowerRoman"
	case "3":
		return "upperLetter"
	case "4":
		return "lowerLetter"
	case "5":
		return "ordinal"
	case "6":
		return "cardinalText"
	case "7":
		return "ordinalText"
	case "22":
		return "decimalZero"
	case "23":
		return "bullet"
	case "255":
		return "none"
	}
	return "decimal"
}

// lists translates w:lists into a numbering part.
func (t *translator) lists(s *xmlscan.Scanner, p *part) {
	p.open("w:numbering")
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start || s.Depth() != depth+1 || s.Space() != ns.Word2003 {
			continue
		}
		switch string(s.Local()) {
		case "listDef":
			p.open("w:abstractNum", "w:abstractNumId", attrW(s, "listDefId"))
			t.children(s, p)
			p.close()
		case "list":
			p.open("w:num", "w:numId", attrW(s, "ilfo"))
			inner := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= inner {
				if s.Kind() != xmlscan.Start || s.Depth() != inner+1 {
					continue
				}
				if s.Is(ns.Word2003, "ilst") {
					p.empty("w:abstractNumId", "w:val", attrW(s, "val"))
					s.Skip()
					continue
				}
				t.element(s, p)
			}
			p.close()
		default:
			s.Skip()
		}
	}
	p.close()
}

// settings translates the document's settings (w:docPr) that the reader
// uses: odd and even headers, the notes' numbering, the template.
func (t *translator) settings(s *xmlscan.Scanner, p *part) {
	p.open("w:settings")
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start || s.Depth() != depth+1 || s.Space() != ns.Word2003 {
			continue
		}
		switch string(s.Local()) {
		case "evenAndOddHeaders":
			p.empty("w:evenAndOddHeaders", "w:val", onOff(attrW(s, "val")))
			s.Skip()
		case "footnotePr", "endnotePr":
			p.open("w:" + string(s.Local()))
			inner := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= inner {
				if s.Kind() != xmlscan.Start || s.Depth() != inner+1 || s.Space() != ns.Word2003 {
					continue
				}
				switch local := string(s.Local()); local {
				case "numFmt":
					p.empty("w:numFmt", "w:val", modern([]byte(attrW(s, "val"))))
				case "numStart", "numRestart", "pos":
					p.empty("w:"+local, "w:val", modern([]byte(attrW(s, "val"))))
				}
				s.Skip()
			}
			p.close()
		case "attachedTemplate":
			if v := attrW(s, "val"); v != "" {
				id := p.rel("attachedTemplate", v, true)
				p.empty("w:attachedTemplate", "r:id", id)
			}
			s.Skip()
		default:
			s.Skip()
		}
	}
	p.close()
}

func onOff(v string) string {
	if v == "" {
		return "on"
	}
	return v
}

// hlink translates a link: an address, a place in the document, or both.
func (t *translator) hlink(s *xmlscan.Scanner, p *part) {
	var attrs []string
	if dest := attrW(s, "dest"); dest != "" {
		attrs = append(attrs, "r:id", p.rel("hyperlink", dest, true))
	}
	if anchor := attrW(s, "bookmark"); anchor != "" {
		attrs = append(attrs, "w:anchor", anchor)
	}
	p.open("w:hyperlink", attrs...)
	t.children(s, p)
	p.close()
}

// note translates a footnote or endnote written where it is cited: a
// reference there, the note in its part.
func (t *translator) note(s *xmlscan.Scanner, p *part, kind string) {
	notes := &t.footnotes
	partName, contentType := "word/footnotes.xml", "application/vnd.openxmlformats-officedocument.wordprocessingml.footnotes+xml"
	if kind == "endnote" {
		notes = &t.endnotes
		partName, contentType = "word/endnotes.xml", "application/vnd.openxmlformats-officedocument.wordprocessingml.endnotes+xml"
	}
	if *notes == nil {
		*notes = t.newPart(partName, contentType, "")
		(*notes).open("w:" + kind + "s")
	}
	t.notes++
	id := strconv.Itoa(t.notes)
	p.empty("w:"+kind+"Reference", "w:id", id)
	(*notes).open("w:"+kind, "w:id", id)
	t.children(s, *notes)
	(*notes).close()
}

// furniture translates a header or footer, written in its section's
// properties: a part, which the section names.
func (t *translator) furniture(s *xmlscan.Scanner, p *part, kind string) {
	typ := attrW(s, "type")
	switch typ {
	case "odd", "":
		typ = "default"
	}
	n, element, relKind := 0, "w:hdr", "header"
	if kind == "hdr" {
		t.headers++
		n = t.headers
	} else {
		t.footers++
		n, element, relKind = t.footers, "w:ftr", "footer"
	}
	name := "word/" + relKind + strconv.Itoa(n) + ".xml"
	f := t.newPart(name, "application/vnd.openxmlformats-officedocument.wordprocessingml."+relKind+"+xml", "")
	id := p.rel(relKind, path.Base(name), false)
	p.empty("w:"+relKind+"Reference", "w:type", typ, "r:id", id)
	f.open(element)
	t.children(s, f)
	f.close()
}

// binData keeps a picture written in base64, by its name.
func (t *translator) binData(s *xmlscan.Scanner) {
	name := attrW(s, "name")
	raw := s.AppendText(nil)
	if name == "" || t.byName[name] != "" {
		return
	}
	data, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, bytes.NewReader(bytes.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, raw))))
	if err != nil && len(data) == 0 {
		return
	}
	if t.budget -= int64(len(data)); t.budget < 0 {
		t.over = true
		return
	}
	base := path.Base(strings.TrimPrefix(name, "wordml://"))
	if base == "." || base == "/" || base == "" {
		base = "image"
	}
	part := "word/media/" + strconv.Itoa(len(t.media)+1) + "-" + base
	if t.byName == nil {
		t.byName = map[string]string{}
	}
	t.byName[name] = path.Base(part)
	t.media = append(t.media, media{name: part, contentType: imageType(base), data: data})
}

// imagedata translates a VML picture's reference to its data: a
// relationship to the media part of the binData it names.
func (t *translator) imagedata(s *xmlscan.Scanner, p *part) {
	p.start("v:imagedata")
	t.copyAttrs(s, p, "src")
	src := ""
	if v, ok := s.Attr(xmlscan.NoSpace, "src"); ok {
		src = string(v)
	}
	if strings.HasPrefix(src, "wordml://") {
		// The picture's data may come later in the file: the target is
		// settled when the relationships are written.
		p.attr("r:id", p.rel("image", pendingPrefix+src, false))
	}
	p.buf.WriteString("/>")
	s.Skip()
}

// imageType returns the media type of a picture by its name's extension.
func imageType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".bmp":
		return "image/bmp"
	case ".tif", ".tiff":
		return "image/tiff"
	case ".wmf":
		return "image/x-wmf"
	case ".emf":
		return "image/x-emf"
	}
	return "application/octet-stream"
}

// annotation translates an aml:annotation: a revision, a comment, a
// bookmark. A formatting change, and any other, is left out.
func (t *translator) annotation(s *xmlscan.Scanner, p *part) {
	if !s.Is(ns.AML, "annotation") {
		if s.Is(ns.AML, "content") {
			t.children(s, p)
			return
		}
		s.Skip()
		return
	}
	typ := attrW(s, "type")
	id, _ := s.Attr(ns.AML, "id")
	author, _ := s.Attr(ns.AML, "author")
	date, _ := s.Attr(ns.AML, "createdate")
	revision := func(name string) {
		attrs := []string{"w:id", string(id)}
		if len(author) > 0 {
			attrs = append(attrs, "w:author", string(author))
		}
		if len(date) > 0 {
			attrs = append(attrs, "w:date", string(date))
		}
		p.open(name, attrs...)
		t.children(s, p) // aml:content, with the runs
		p.close()
	}
	switch typ {
	case "Word.Insertion":
		revision("w:ins")
	case "Word.Deletion":
		revision("w:del")
	case "Word.Bookmark.Start":
		p.empty("w:bookmarkStart", "w:id", string(id), "w:name", attrW(s, "name"))
		s.Skip()
	case "Word.Bookmark.End":
		p.empty("w:bookmarkEnd", "w:id", string(id))
		s.Skip()
	case "Word.Comment.Start":
		p.empty("w:commentRangeStart", "w:id", string(id))
		s.Skip()
	case "Word.Comment.End":
		p.empty("w:commentRangeEnd", "w:id", string(id))
		s.Skip()
	case "Word.Comment":
		if t.comments == nil {
			t.comments = t.newPart("word/comments.xml", "application/vnd.openxmlformats-officedocument.wordprocessingml.comments+xml", "")
			t.comments.open("w:comments")
		}
		p.empty("w:commentReference", "w:id", string(id))
		attrs := []string{"w:id", string(id)}
		if len(author) > 0 {
			attrs = append(attrs, "w:author", string(author))
		}
		if len(date) > 0 {
			attrs = append(attrs, "w:date", string(date))
		}
		if v := attrW(s, "initials"); v != "" {
			attrs = append(attrs, "w:initials", v)
		}
		t.comments.open("w:comment", attrs...)
		t.children(s, t.comments)
		t.comments.close()
	default:
		s.Skip()
	}
}

// Check reports whether the file's body is cut off, as an error to warn
// of.
func (r *Result) Check() error {
	if r.Err != nil {
		return fmt.Errorf("the document body is cut off: %w", r.Err)
	}
	return nil
}
