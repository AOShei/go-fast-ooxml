// Package xmlindex finds where the elements of an XML file are, to a
// depth the caller chooses, without reading what is in the elements it
// does not go into: those are skipped over, by where their markup begins
// and ends. Each element found can then be read as a part of its own,
// with xmlscan, from its place in the file and with the namespaces
// declared around it.
//
// It is how the Office formats saved as one XML file are read: Flat OPC,
// whose parts are elements, and SpreadsheetML 2003, whose sheets are. An
// element whose content is malformed, or nested deeper than the scanner
// reads, fails when it is read, alone, as a part of an archive would.
//
// Skipping needs to know only where markup begins and ends: comments,
// CDATA sections, processing instructions, and tags, whose quoted values
// may hold ">". A document type declaration before the root, or among
// the elements gone into, is refused: it could declare entities.
package xmlindex

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// Bounds.
const (
	maxTag   = xmlscan.DefaultMaxTokenBytes // one tag of an element gone into or found
	maxDepth = 64                           // elements gone into, nested
	// MaxBindings bounds the namespaces declared around an element that
	// it is read with: the innermost are kept. Without a bound, a file
	// declaring many around many elements would cost their product.
	MaxBindings = 64
)

// Errors.
var (
	// ErrTooLarge: a file that is not UTF-8 converts to more than
	// Options.MaxCopy bytes.
	ErrTooLarge = errors.New("xmlindex: the file converted to UTF-8 is larger than allowed")
	// ErrTooMany: more elements than Options.MaxElements.
	ErrTooMany = errors.New("xmlindex: more elements than allowed")
	// ErrNoRoot: no root element.
	ErrNoRoot = errors.New("xmlindex: XML with no root element")
)

// Attr is an attribute of an element found. Namespace declarations are
// not among them.
type Attr struct {
	Space, Local, Value string
}

// Element is an element found.
type Element struct {
	Name         string // qualified, as written
	Space, Local string // its namespace's URI and its local name
	Attrs        []Attr
	// Start is the offset of its "<"; End that of the byte after it, 0
	// if the file breaks off inside it. ContentStart and ContentEnd
	// bound what is between its tags (equal for an empty element).
	Start, End               int64
	ContentStart, ContentEnd int64
	// Children are the elements in it, if it was gone into.
	Children []*Element
	// Outer are the namespaces declared on its ancestors, to read the
	// element with; Own those declared on it, to read its content with
	// (after Outer). Elements share Outer with their siblings.
	Outer, Own []xmlscan.Binding
}

// Attr returns the value of the attribute with the given namespace URI
// and local name.
func (e *Element) Attr(space, local string) (string, bool) {
	for _, a := range e.Attrs {
		if a.Local == local && a.Space == space {
			return a.Value, true
		}
	}
	return "", false
}

// Is reports whether the element has the given namespace and local name.
func (e *Element) Is(space, local string) bool { return e.Space == space && e.Local == local }

// Bindings returns the namespaces to read the element's content with,
// such as when the content is read as a part: Outer and Own, the
// innermost MaxBindings of them.
func (e *Element) Bindings() []xmlscan.Binding {
	b := slices.Concat(e.Outer, e.Own)
	if len(b) > MaxBindings {
		b = b[len(b)-MaxBindings:]
	}
	return b
}

// Options says how far to go.
type Options struct {
	// Descend says whether to go into an element and find its children,
	// given the element (its tag read) and its depth, 1 for the root.
	// The root is always gone into.
	Descend func(e *Element, depth int) bool
	// Text, if set, is given the character data directly inside an
	// element that is not gone into, in pieces (not inside its child
	// elements). It is for counting what a base64 element holds.
	Text func(e *Element, text []byte)
	// MaxElements bounds the elements found; 0 means no bound.
	MaxElements int
	// MaxCopy bounds the copy of a file that is not UTF-8, converted.
	MaxCopy int64
}

// Index is what Read found.
type Index struct {
	Root *Element
	// Src is the file as the elements' offsets count it: the file, or
	// its conversion to UTF-8, of Size bytes.
	Src  io.ReaderAt
	Size int64
	// Damage says why the file could not be read to the end of its root
	// element, and At where; elements found before are complete.
	Damage error
	At     int64
}

// Section returns a reader of the element, from its "<" to its end.
func (x *Index) Section(e *Element) *io.SectionReader {
	return io.NewSectionReader(x.Src, e.Start, e.End-e.Start)
}

// Read finds the elements of the XML file r, of size bytes.
//
// A file that is not UTF-8 (UTF-16, or a single-byte encoding its
// declaration names) is converted whole, into memory, first: an element
// read where it is must be in the encoding the scanner is given.
func Read(r io.ReaderAt, size int64, opts Options) (*Index, error) {
	x := &Index{Src: r, Size: size}
	text, converted, err := xmlscan.UTF8(io.NewSectionReader(r, 0, size))
	if err != nil {
		return nil, err
	}
	if converted {
		limit := opts.MaxCopy
		if limit <= 0 {
			limit = math.MaxInt64 - 1
		}
		data, err := io.ReadAll(io.LimitReader(text, min(limit, math.MaxInt64-1)+1))
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > limit {
			return nil, ErrTooLarge
		}
		// The declaration names the encoding the file was in; it is
		// dropped, so that the copy is read as the UTF-8 it now is.
		if bytes.HasPrefix(data, []byte("<?xml")) {
			if i := bytes.Index(data, []byte("?>")); i >= 0 {
				data = data[i+2:]
			}
		}
		x.Src, x.Size = bytes.NewReader(data), int64(len(data))
	}
	return x, x.read(opts)
}

var errEnd = errors.New("the file ends inside an element")

type lexer struct {
	r   *bufio.Reader
	off int64 // offset of the next byte
	tag []byte
	// lastSlash: the tag markup read last ended with "/>".
	lastSlash bool
}

func (x *lexer) readByte() (byte, error) {
	c, err := x.r.ReadByte()
	if err == nil {
		x.off++
	} else if err == io.EOF {
		err = errEnd
	}
	return c, err
}

// skipPast consumes input up to and including end, of at most 3 bytes.
// The bytes read last are compared with it, so that "]]]>" ends a CDATA
// section as "]]>" does.
func (x *lexer) skipPast(end string) error {
	var last [3]byte
	for n := 1; ; n++ {
		c, err := x.readByte()
		if err != nil {
			return err
		}
		last[0], last[1], last[2] = last[1], last[2], c
		if n >= len(end) && string(last[3-len(end):]) == end {
			return nil
		}
	}
}

// text consumes character data up to the next "<", which it consumes too,
// and calls count with each stretch of it.
func (x *lexer) text(count func([]byte)) error {
	for {
		b, err := x.r.ReadSlice('<')
		x.off += int64(len(b))
		if count != nil {
			if err == nil {
				count(b[:len(b)-1])
			} else {
				count(b)
			}
		}
		switch err {
		case nil:
			return nil
		case bufio.ErrBufferFull:
			continue
		case io.EOF:
			return errEnd
		default:
			return err
		}
	}
}

// markup consumes what follows a "<" up to the end of that markup, and
// says what it was. For a tag, x.tag holds what is between "<" and ">"
// when keep is set, which is for the package's own elements: there, and
// only there, a document type declaration is refused.
func (x *lexer) markup(keep bool) (kind byte, err error) {
	c, err := x.readByte()
	if err != nil {
		return 0, err
	}
	switch c {
	case '?':
		return '?', x.skipPast("?>")
	case '!':
		peek, _ := x.r.Peek(7)
		switch {
		case bytes.HasPrefix(peek, []byte("--")):
			return '!', x.skipPast("-->")
		case bytes.HasPrefix(peek, []byte("[CDATA[")):
			return '!', x.skipPast("]]>")
		case keep && bytes.HasPrefix(peek, []byte("DOCTYPE")):
			return 0, xmlscan.ErrDTD
		}
		return '!', x.skipPast(">")
	}
	kind = '<'
	if c == '/' {
		kind = '/'
	}
	x.tag = x.tag[:0]
	if keep && kind == '<' {
		x.tag = append(x.tag, c)
	}
	var quote byte
	prev := c
	for {
		c, err := x.readByte()
		if err != nil {
			return 0, err
		}
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '>':
			x.lastSlash = prev == '/'
			return kind, nil
		}
		prev = c
		if keep {
			if len(x.tag) >= maxTag {
				return 0, fmt.Errorf("a tag of the package longer than %d bytes", maxTag)
			}
			x.tag = append(x.tag, c)
		}
	}
}

// skipContent consumes the content of a part's data element and its end
// tag, and returns where the content ends. count is given the character
// data at its top level.
func (x *lexer) skipContent(count func([]byte)) (end int64, err error) {
	depth := 0
	for {
		if depth == 0 {
			err = x.text(count)
		} else {
			err = x.text(nil)
		}
		if err != nil {
			return 0, err
		}
		at := x.off - 1
		kind, err := x.markup(false)
		if err != nil {
			return 0, err
		}
		switch kind {
		case '/':
			if depth == 0 {
				return at, nil
			}
			depth--
		case '<':
			if !x.lastSlash {
				depth++
			}
		}
	}
}

// splitTag splits a tag, what is between "<" and ">", into its name and
// attributes, with values decoded.
func splitTag(tag []byte) (name string, attrs [][2]string, selfClosing bool, err error) {
	if n := len(tag); n > 0 && tag[n-1] == '/' {
		selfClosing, tag = true, tag[:n-1]
	}
	i := 0
	for i < len(tag) && !isXMLSpace(tag[i]) {
		i++
	}
	name = string(tag[:i])
	for {
		for i < len(tag) && isXMLSpace(tag[i]) {
			i++
		}
		if i == len(tag) {
			return name, attrs, selfClosing, nil
		}
		start := i
		for i < len(tag) && tag[i] != '=' && !isXMLSpace(tag[i]) {
			i++
		}
		key := string(tag[start:i])
		for i < len(tag) && isXMLSpace(tag[i]) {
			i++
		}
		if i == len(tag) || tag[i] != '=' {
			return "", nil, false, fmt.Errorf("attribute %q of <%s> has no value", key, name)
		}
		i++
		for i < len(tag) && isXMLSpace(tag[i]) {
			i++
		}
		if i == len(tag) || (tag[i] != '"' && tag[i] != '\'') {
			return "", nil, false, fmt.Errorf("attribute %q of <%s> is not quoted", key, name)
		}
		q := tag[i]
		i++
		end := bytes.IndexByte(tag[i:], q)
		if end < 0 {
			return "", nil, false, fmt.Errorf("attribute %q of <%s> is not closed", key, name)
		}
		attrs = append(attrs, [2]string{key, unescape(tag[i : i+end])})
		i += end + 1
	}
}

func isXMLSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

// unescape decodes the references of an attribute value. One it does not
// know is kept as written.
func unescape(v []byte) string {
	if bytes.IndexByte(v, '&') < 0 && bytes.IndexAny(v, "\t\r\n") < 0 {
		return string(v)
	}
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c == '\t' || c == '\r' || c == '\n':
			b.WriteByte(' ')
			continue
		case c != '&':
			b.WriteByte(c)
			continue
		}
		semi := bytes.IndexByte(v[i:], ';')
		if semi < 0 {
			b.WriteByte(c)
			continue
		}
		ref := string(v[i+1 : i+semi])
		var r rune = -1
		switch ref {
		case "lt":
			r = '<'
		case "gt":
			r = '>'
		case "amp":
			r = '&'
		case "quot":
			r = '"'
		case "apos":
			r = '\''
		default:
			if strings.HasPrefix(ref, "#x") || strings.HasPrefix(ref, "#X") {
				if n, err := strconv.ParseUint(ref[2:], 16, 32); err == nil {
					r = rune(n)
				}
			} else if strings.HasPrefix(ref, "#") {
				if n, err := strconv.ParseUint(ref[1:], 10, 32); err == nil {
					r = rune(n)
				}
			}
		}
		if r < 0 || !utf8.ValidRune(r) {
			b.WriteByte(c)
			continue
		}
		b.WriteRune(r)
		i += semi
	}
	return b.String()
}

// scope holds the namespace declarations of the elements gone into.
type scope struct {
	decls     []decl
	innermost map[string]int // prefix -> its declaration in scope, by index in decls
}

type decl struct {
	prefix, uri string
	prev        int // the declaration of the prefix this one hides, or -1
}

func (s *scope) declare(prefix, uri string) {
	prev, ok := s.innermost[prefix]
	if !ok {
		prev = -1
	}
	s.innermost[prefix] = len(s.decls)
	s.decls = append(s.decls, decl{prefix, uri, prev})
}

func (s *scope) undeclare(mark int) {
	for i := len(s.decls) - 1; i >= mark; i-- {
		if d := s.decls[i]; d.prev < 0 {
			delete(s.innermost, d.prefix)
		} else {
			s.innermost[d.prefix] = d.prev
		}
	}
	s.decls = s.decls[:mark]
}

func (s *scope) lookup(prefix string) string {
	if i, ok := s.innermost[prefix]; ok {
		return s.decls[i].uri
	}
	return ""
}

// split resolves a qualified name.
func (s *scope) split(qname string, element bool) (uri, local string) {
	if i := strings.IndexByte(qname, ':'); i >= 0 {
		return s.lookup(qname[:i]), qname[i+1:]
	}
	if element {
		return s.lookup(""), qname
	}
	return "", qname
}

// bindings returns decls[from:] as bindings, the innermost MaxBindings.
func (s *scope) bindings(from int) []xmlscan.Binding {
	n := min(len(s.decls)-from, MaxBindings)
	if n <= 0 {
		return nil
	}
	b := make([]xmlscan.Binding, 0, n)
	for _, d := range s.decls[len(s.decls)-n:] {
		b = append(b, xmlscan.Binding{Prefix: d.prefix, URI: d.uri})
	}
	return b
}

func (x *Index) read(opts Options) error {
	lx := &lexer{r: bufio.NewReaderSize(io.NewSectionReader(x.Src, 0, x.Size), 64<<10)}
	sc := &scope{innermost: map[string]int{}}
	var (
		open  []*Element // the elements gone into, still open
		marks []int      // len(sc.decls) before each one's declarations
		inner [][]xmlscan.Binding
		found int
	)
	damaged := func(err error) error {
		if x.Root == nil {
			if err == errEnd {
				return ErrNoRoot
			}
			return err
		}
		x.Damage, x.At = err, lx.off
		return nil
	}
	for {
		if err := lx.text(nil); err != nil {
			if err == errEnd && len(open) == 0 && x.Root != nil {
				return nil
			}
			return damaged(err)
		}
		at := lx.off - 1
		kind, err := lx.markup(true)
		if err != nil {
			if errors.Is(err, xmlscan.ErrDTD) {
				return err
			}
			return damaged(err)
		}
		switch kind {
		case '/':
			if len(open) == 0 {
				return damaged(errors.New("an end tag with no element open"))
			}
			e := open[len(open)-1]
			e.ContentEnd, e.End = at, lx.off
			open, inner = open[:len(open)-1], inner[:len(inner)-1]
			sc.undeclare(marks[len(marks)-1])
			marks = marks[:len(marks)-1]
			if len(open) == 0 {
				return nil // the end of the root; what follows is not read
			}
			continue
		case '<':
		default:
			continue
		}
		if x.Root != nil && len(open) == 0 {
			return damaged(errors.New("a second root element"))
		}
		qname, attrs, selfClosing, err := splitTag(lx.tag)
		if err != nil {
			return damaged(err)
		}
		if found++; opts.MaxElements > 0 && found > opts.MaxElements {
			return fmt.Errorf("%w: more than %d", ErrTooMany, opts.MaxElements)
		}
		mark := len(sc.decls)
		for _, a := range attrs {
			switch {
			case a[0] == "xmlns":
				sc.declare("", a[1])
			case strings.HasPrefix(a[0], "xmlns:"):
				sc.declare(a[0][6:], a[1])
			}
		}
		e := &Element{Name: qname, Start: at, ContentStart: lx.off}
		e.Space, e.Local = sc.split(qname, true)
		for _, a := range attrs {
			if a[0] == "xmlns" || strings.HasPrefix(a[0], "xmlns:") {
				continue
			}
			space, local := sc.split(a[0], false)
			e.Attrs = append(e.Attrs, Attr{space, local, a[1]})
		}
		if len(open) > 0 {
			e.Outer = inner[len(inner)-1]
		}
		e.Own = sc.bindings(mark)
		depth := len(open) + 1
		if parent := len(open); parent > 0 {
			open[parent-1].Children = append(open[parent-1].Children, e)
		} else {
			x.Root = e
		}
		switch {
		case selfClosing:
			e.ContentEnd, e.End = lx.off, lx.off
			sc.undeclare(mark)
		case depth == 1 || opts.Descend != nil && opts.Descend(e, depth):
			if depth > maxDepth {
				return damaged(fmt.Errorf("elements gone into nested more than %d deep", maxDepth))
			}
			open, marks = append(open, e), append(marks, mark)
			// What its children are read with: shared by all of them.
			in := e.Outer
			if len(e.Own) > 0 {
				in = e.Bindings()
			}
			inner = append(inner, in)
		default:
			var count func([]byte)
			if opts.Text != nil {
				count = func(b []byte) { opts.Text(e, b) }
			}
			end, err := lx.skipContent(count)
			sc.undeclare(mark)
			if err != nil {
				return damaged(err)
			}
			e.ContentEnd, e.End = end, lx.off
		}
	}
}
