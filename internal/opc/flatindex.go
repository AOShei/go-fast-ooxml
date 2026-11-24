package opc

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/AOShei/go-fast-ooxml/internal/xmlroot"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// The index pass reads the package's own elements (package, part, and
// the part's data) and skips over each part's content without reading
// it: a part whose XML is malformed, or nested deeper than the scanner
// allows, then fails as that part, as it would in an archive, and does
// not stop the package from being read. Skipping needs to know only
// where markup begins and ends: comments, CDATA sections, processing
// instructions, and tags, whose quoted values may hold ">".

// Bounds of the index pass.
const (
	maxFlatTag   = xmlscan.DefaultMaxTokenBytes // one tag of the package's own elements
	maxFlatDepth = 64                           // the package's own elements, nested
	// maxFlatBindings bounds the namespaces declared around a part that
	// it is read with: the innermost are kept. Office declares one, the
	// package's own, which a part does not use. Without a bound, a file
	// declaring many around many parts would cost their product.
	maxFlatBindings = 64
)

var errFlatEnd = errors.New("the file ends inside the package")

type flatIndex struct {
	r   *bufio.Reader
	off int64 // offset of the next byte
	tag []byte
	// lastSlash: the tag markup read last ended with "/>".
	lastSlash bool
}

func (x *flatIndex) readByte() (byte, error) {
	c, err := x.r.ReadByte()
	if err == nil {
		x.off++
	} else if err == io.EOF {
		err = errFlatEnd
	}
	return c, err
}

// skipPast consumes input up to and including end, of at most 3 bytes.
// The bytes read last are compared with it, so that "]]]>" ends a CDATA
// section as "]]>" does.
func (x *flatIndex) skipPast(end string) error {
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
func (x *flatIndex) text(count func([]byte)) error {
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
			return errFlatEnd
		default:
			return err
		}
	}
}

// markup consumes what follows a "<" up to the end of that markup, and
// says what it was. For a tag, x.tag holds what is between "<" and ">"
// when keep is set, which is for the package's own elements: there, and
// only there, a document type declaration is refused.
func (x *flatIndex) markup(keep bool) (kind byte, err error) {
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
			if len(x.tag) >= maxFlatTag {
				return 0, fmt.Errorf("a tag of the package longer than %d bytes", maxFlatTag)
			}
			x.tag = append(x.tag, c)
		}
	}
}

// skipContent consumes the content of a part's data element and its end
// tag, and returns where the content ends. count is given the character
// data at its top level.
func (x *flatIndex) skipContent(count func([]byte)) (end int64, err error) {
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

type flatScope struct {
	prefix, uri string
	prev        int // the declaration of the prefix this one hides, or -1
}

// bindings returns the declarations of scope to read a part with: the
// innermost maxFlatBindings, the package's own namespace left out.
func bindings(scope []flatScope) []xmlscan.Binding {
	var b []xmlscan.Binding
	for i := len(scope) - 1; i >= 0 && len(b) < maxFlatBindings; i-- {
		if s := scope[i]; s.uri != xmlroot.PackageNS {
			b = append(b, xmlscan.Binding{Prefix: s.prefix, URI: s.uri})
		}
	}
	slices.Reverse(b)
	return b
}

// indexFlat finds the parts of a Flat OPC package.
func (p *Package) indexFlat(src io.ReaderAt, size int64, lim Limits) error {
	x := &flatIndex{r: bufio.NewReaderSize(io.NewSectionReader(src, 0, size), 64<<10)}
	var (
		scope    []flatScope       // namespaces declared by the open elements
		outer    []xmlscan.Binding // declared on the package element, which every part shares
		marks    []int             // len(scope) before each open element's declarations
		names    []string          // the open elements' names
		partName string
		partType string
		data     *flatPart // the open part's data, once read
		parts    int
	)
	// innermost holds each prefix's declaration in scope, by its index in
	// scope: a prefix is found at once however many are declared.
	innermost := map[string]int{}
	declare := func(prefix, uri string) {
		prev, ok := innermost[prefix]
		if !ok {
			prev = -1
		}
		innermost[prefix] = len(scope)
		scope = append(scope, flatScope{prefix, uri, prev})
	}
	undeclare := func(mark int) {
		for i := len(scope) - 1; i >= mark; i-- {
			if d := scope[i]; d.prev < 0 {
				delete(innermost, d.prefix)
			} else {
				innermost[d.prefix] = d.prev
			}
		}
		scope = scope[:mark]
	}
	lookup := func(prefix string) string {
		if i, ok := innermost[prefix]; ok {
			return scope[i].uri
		}
		return ""
	}
	// split resolves a qualified name.
	split := func(qname string, element bool) (uri, local string) {
		if i := strings.IndexByte(qname, ':'); i >= 0 {
			return lookup(qname[:i]), qname[i+1:]
		}
		if element {
			return lookup(""), qname
		}
		return "", qname
	}
	damaged := func(err error) error {
		if parts == 0 {
			return fmt.Errorf("%w: %v", ErrDamaged, err)
		}
		// What was read before the damage is the package.
		p.flatDamage = &FlatDamage{At: x.off, Parts: parts, Err: err}
		return nil
	}
	for {
		if err := x.text(nil); err != nil {
			if err == errFlatEnd && len(names) == 0 && parts > 0 {
				return nil
			}
			if err == errFlatEnd && len(names) == 0 {
				return fmt.Errorf("%w: XML with no package in it", ErrDamaged)
			}
			return damaged(err)
		}
		kind, err := x.markup(true)
		if err != nil {
			if errors.Is(err, xmlscan.ErrDTD) {
				return fmt.Errorf("%w: %v", ErrDamaged, err)
			}
			return damaged(err)
		}
		switch kind {
		case '/':
			if len(names) == 0 {
				return damaged(errors.New("an end tag with no element open"))
			}
			if len(names) == 2 {
				// The end of a part: it is one if it has a name and data.
				if data != nil && partName != "" {
					canon := Canonical(partName)
					if _, dup := p.flatParts[canon]; dup {
						p.ambiguous++
					} else {
						parts++
						if lim.MaxMembers > 0 && parts > lim.MaxMembers {
							return fmt.Errorf("%w: more than %d", ErrTooManyParts, lim.MaxMembers)
						}
						p.flatParts[canon] = data
						p.names = append(p.names, canon)
						if shown := strings.TrimLeft(strings.ReplaceAll(partName, "\\", "/"), "/"); shown != canon {
							if p.written == nil {
								p.written = map[string]string{}
							}
							p.written[canon] = shown
						}
						if partType != "" {
							p.overrides[canon] = partType
						}
					}
				}
				data = nil
			}
			names = names[:len(names)-1]
			undeclare(marks[len(marks)-1])
			marks = marks[:len(marks)-1]
			if len(names) == 0 {
				return nil // the end of the package; what follows is not read
			}
			continue
		case '<':
		default:
			continue
		}
		qname, attrs, selfClosing, err := splitTag(x.tag)
		if err != nil {
			return damaged(err)
		}
		mark := len(scope)
		for _, a := range attrs {
			switch {
			case a[0] == "xmlns":
				declare("", a[1])
			case strings.HasPrefix(a[0], "xmlns:"):
				declare(a[0][6:], a[1])
			}
		}
		uri, local := split(qname, true)
		depth := len(names) + 1
		attr := func(want string) string {
			for _, a := range attrs {
				if u, l := split(a[0], false); l == want && (u == xmlroot.PackageNS || u == "") {
					return a[1]
				}
			}
			return ""
		}
		switch {
		case depth == 1 && (uri != xmlroot.PackageNS || local != "package"):
			return fmt.Errorf("%w: XML whose root is %s", ErrNotPackage, qname)
		case depth == 2 && uri == xmlroot.PackageNS && local == "part":
			partName, partType, data = attr("name"), attr("contentType"), nil
		case depth == 3 && names[1] != "" && data == nil && uri == xmlroot.PackageNS && (local == "xmlData" || local == "binaryData"):
			d := &flatPart{src: src, off: x.off, base64: local == "binaryData"}
			if !d.base64 {
				// The package's declarations are one copy that every
				// part shares; a part has a copy of its own only of
				// what it, or its data element, declares.
				d.outer, d.own = outer, bindings(scope[marks[1]:])
			}
			undeclare(mark)
			if !selfClosing {
				var chars, pad int64
				count := func(b []byte) {
					if !d.base64 {
						return
					}
					for _, c := range b {
						switch {
						case isXMLSpace(c):
						case c == '=':
							chars++
							pad++
						default:
							chars++
						}
					}
				}
				end, err := x.skipContent(count)
				if err != nil {
					return damaged(err)
				}
				d.n = end - d.off
				d.decoded = max(chars/4*3-pad, 0)
			}
			data = d
			continue
		}
		if depth == 1 {
			outer = bindings(scope)
		}
		if selfClosing {
			undeclare(mark)
			continue
		}
		if depth > maxFlatDepth {
			return damaged(fmt.Errorf("the package's elements nested more than %d deep", maxFlatDepth))
		}
		names = append(names, qname)
		marks = append(marks, mark)
		if depth == 2 && !(uri == xmlroot.PackageNS && local == "part") {
			names[1] = "" // not a part: what is in it is not read
		}
	}
}
