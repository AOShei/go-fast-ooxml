package opc

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/AOShei/go-fast-core/opc"
	"github.com/AOShei/go-fast-core/xmlscan"
	"github.com/AOShei/go-fast-ooxml/internal/xmlindex"
	"github.com/AOShei/go-fast-ooxml/internal/xmlroot"
)

// A Flat OPC package is a package written as one XML document instead of
// a zip archive, as Word's "Word XML Document" and PowerPoint's
// "PowerPoint XML Presentation" save it:
//
//	<pkg:package xmlns:pkg="http://schemas.microsoft.com/office/2006/xmlPackage">
//	  <pkg:part pkg:name="/word/document.xml" pkg:contentType="...">
//	    <pkg:xmlData><w:document ...>...</w:document></pkg:xmlData>
//	  </pkg:part>
//	  <pkg:part pkg:name="/word/media/image1.png" pkg:contentType="image/png" pkg:compression="store">
//	    <pkg:binaryData>iVBORw0KGgo...</pkg:binaryData>
//	  </pkg:part>
//	</pkg:package>
//
// Each part states its content type, so there is no table of them, and
// relationship parts are parts like any other. A part's XML is read where
// it is in the file, with the namespaces declared around it; a binary
// part is base64, decoded as it is read. pkg:compression says how the
// part is to be stored when the package is written as a zip archive, and
// nothing about the data here.

// flatPart is a part of a Flat OPC package: its bytes in the file.
type flatPart struct {
	src    io.ReaderAt
	off, n int64
	base64 bool // binaryData: base64, decoded as it is read
}

func (m *flatPart) open() io.ReadCloser {
	var r io.Reader = io.NewSectionReader(m.src, m.off, m.n)
	if m.base64 {
		r = base64.NewDecoder(base64.StdEncoding, &unspaced{r: r})
	}
	return io.NopCloser(r)
}

// unspaced drops the white space that base64 text is broken by: line ends,
// and the spaces and tabs some writers indent it with.
type unspaced struct{ r io.Reader }

func (u *unspaced) Read(p []byte) (int, error) {
	for {
		n, err := u.r.Read(p)
		k := 0
		for _, c := range p[:n] {
			if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
				p[k] = c
				k++
			}
		}
		if k > 0 || err != nil {
			return k, err
		}
	}
}

// FlatDamage says where a Flat OPC package that cannot be read to its end
// breaks off, and how many parts were read before.
type FlatDamage struct {
	At    int64 // byte offset, in the file as it is read (converted to UTF-8)
	Parts int
	Err   error
}

// Message describes the damage for a warning.
func (d *FlatDamage) Message() string {
	return fmt.Sprintf("the XML package cannot be read past byte %d (%v); the %d parts complete before it are read", d.At, d.Err, d.Parts)
}

// Flat reports whether the package is a Flat OPC package.
func (p *Package) Flat() bool { return p.flat && !p.sections }

// Section is a stretch of an XML file read as a part: an element of a
// file in one of the formats saved as XML that have no package (Office
// 2003's), with the namespaces declared around it.
type Section struct {
	Name, ContentType string
	Off, N            int64
	Bindings          []xmlscan.Binding
	// Data, if set, is the part itself, made in memory (a part of the
	// package an Office 2003 file is translated into), and Off and N are
	// not used.
	Data []byte
}

// NewSections returns a package whose parts are the given stretches of
// src, read as an archive's are, within the budget of lim. It has no
// relationships unless a section is named as a relationships part.
func NewSections(src io.ReaderAt, lim Limits, sections []Section) *Package {
	parts := make([]opc.Part, 0, len(sections))
	seen := make(map[string]bool, len(sections))
	for _, s := range sections {
		// Sections are named by the translation, not by the file: one
		// named twice is not a file's ambiguity, and the first is kept.
		if name := opc.Canonical(s.Name); seen[name] {
			continue
		} else {
			seen[name] = true
		}
		m := &flatPart{src: src, off: s.Off, n: s.N}
		bindings := s.Bindings
		if s.Data != nil {
			m.src, m.off, m.n, bindings = bytes.NewReader(s.Data), 0, int64(len(s.Data)), nil
		}
		parts = append(parts, opc.Part{Name: s.Name, ContentType: s.ContentType, Size: m.n, Open: m.open, Bindings: bindings})
	}
	return &Package{Package: opc.FromParts(parts, lim), flat: true, sections: true}
}

// FlatDamage returns where a Flat OPC package breaks off; nil for one read
// to its end, and for a zip archive.
func (p *Package) FlatDamage() *FlatDamage { return p.flatDamage }

// OpenFlat reads a Flat OPC package: one pass over the file finds its
// parts (xmlindex), which are then read as those of an archive are. A
// part whose XML is malformed fails when it is read, alone, as in an
// archive. A file cut short is read up to its last complete part.
//
// A file that is not UTF-8 (UTF-16, or a single-byte encoding its
// declaration names) is converted whole, into memory, first: a part read
// where it is must be in the encoding the scanner is given. Office writes
// UTF-8.
func OpenFlat(r io.ReaderAt, size int64, lim Limits) (*Package, error) {
	// What a binaryData element holds: base64 characters and padding.
	type count struct{ chars, pad int64 }
	counts := map[*xmlindex.Element]*count{}
	opts := xmlindex.Options{
		Descend: func(e *xmlindex.Element, depth int) bool {
			return depth == 2 && e.Is(xmlroot.PackageNS, "part")
		},
		Text: func(e *xmlindex.Element, text []byte) {
			if !e.Is(xmlroot.PackageNS, "binaryData") {
				return
			}
			c := counts[e]
			if c == nil {
				c = &count{}
				counts[e] = c
			}
			for _, b := range text {
				switch b {
				case ' ', '\t', '\r', '\n':
				case '=':
					c.chars++
					c.pad++
				default:
					c.chars++
				}
			}
		},
		MaxCopy: lim.MaxDecodedBytes,
	}
	if lim.MaxMembers > 0 {
		// A part is two elements; a few more are allowed around them.
		opts.MaxElements = 2*lim.MaxMembers + 1024
	}
	x, err := xmlindex.Read(r, size, opts)
	switch {
	case errors.Is(err, xmlindex.ErrTooLarge):
		return nil, ErrBudget
	case errors.Is(err, xmlindex.ErrTooMany):
		return nil, fmt.Errorf("%w: more than %d", ErrTooManyParts, lim.MaxMembers)
	case errors.Is(err, xmlindex.ErrNoRoot):
		return nil, fmt.Errorf("%w: XML with no package in it", ErrDamaged)
	case err != nil:
		return nil, fmt.Errorf("%w: %v", ErrDamaged, err)
	}
	if !x.Root.Is(xmlroot.PackageNS, "package") {
		return nil, fmt.Errorf("%w: XML whose root is %s", ErrNotPackage, x.Root.Name)
	}
	attr := func(e *xmlindex.Element, local string) string {
		if v, ok := e.Attr(xmlroot.PackageNS, local); ok {
			return v
		}
		v, _ := e.Attr("", local)
		return v
	}
	var parts []opc.Part
	seen := map[string]bool{}
	for _, part := range x.Root.Children {
		if part.End == 0 || !part.Is(xmlroot.PackageNS, "part") {
			continue // a part the file breaks off in is no part
		}
		name := attr(part, "name")
		var data *xmlindex.Element
		for _, d := range part.Children {
			if d.End > 0 && (d.Is(xmlroot.PackageNS, "xmlData") || d.Is(xmlroot.PackageNS, "binaryData")) {
				data = d
				break
			}
		}
		if data == nil || name == "" {
			continue // a part with no data, or no name, is no part
		}
		m := &flatPart{src: x.Src, off: data.ContentStart, n: data.ContentEnd - data.ContentStart, base64: data.Local == "binaryData"}
		size, bindings := m.n, []xmlscan.Binding(nil)
		if m.base64 {
			size = 0
			if c := counts[data]; c != nil {
				size = max(c.chars/4*3-c.pad, 0)
			}
		} else if len(data.Outer)+len(data.Own) > 0 {
			bindings = slices.Concat(data.Outer, data.Own)
		}
		// A second part of one name is counted by FromParts (Ambiguous);
		// only the first is a part, and counts against MaxMembers.
		canon := opc.Canonical(name)
		if !seen[canon] {
			seen[canon] = true
			if lim.MaxMembers > 0 && len(seen) > lim.MaxMembers {
				return nil, fmt.Errorf("%w: more than %d", ErrTooManyParts, lim.MaxMembers)
			}
		}
		parts = append(parts, opc.Part{Name: name, ContentType: attr(part, "contentType"), Size: size, Open: m.open, Bindings: bindings})
	}
	p := &Package{Package: opc.FromParts(parts, lim), flat: true}
	n := len(seen)
	if x.Damage != nil {
		if n == 0 {
			return nil, fmt.Errorf("%w: %v", ErrDamaged, x.Damage)
		}
		p.flatDamage = &FlatDamage{At: x.At, Parts: n, Err: x.Damage}
	}
	return p, nil
}
