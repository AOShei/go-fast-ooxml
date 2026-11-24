package opc

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"math"

	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
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
	src     io.ReaderAt
	off, n  int64
	base64  bool  // binaryData: base64, decoded as it is read
	decoded int64 // binaryData: the bytes it decodes to
	// xmlData: namespaces declared around the part, on the package
	// element (shared by every part) and on the part and data elements.
	outer, own []xmlscan.Binding
}

// size is the part's size as read: for base64, what it decodes to.
func (m *flatPart) size() int64 {
	if m.base64 {
		return m.decoded
	}
	return m.n
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
func (p *Package) Flat() bool { return p.flat }

// FlatDamage returns where a Flat OPC package breaks off; nil for one read
// to its end, and for a zip archive.
func (p *Package) FlatDamage() *FlatDamage { return p.flatDamage }

// OpenFlat reads a Flat OPC package: one pass over the file finds its
// parts, which are then read as those of an archive are.
//
// A file that is not UTF-8 (UTF-16, or a single-byte encoding its
// declaration names) is converted whole, into memory, first: a part read
// where it is must be in the encoding the scanner is given. Office writes
// UTF-8.
func OpenFlat(r io.ReaderAt, size int64, lim Limits) (*Package, error) {
	src := r
	if text, converted, err := xmlscan.UTF8(io.NewSectionReader(r, 0, size)); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDamaged, err)
	} else if converted {
		// The budget for decompressed bytes is what bounds this copy; the
		// file itself is bounded by the input limit already.
		data, err := io.ReadAll(io.LimitReader(text, min(lim.MaxDecodedBytes, math.MaxInt64-1)+1))
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrDamaged, err)
		}
		if int64(len(data)) > lim.MaxDecodedBytes {
			return nil, ErrBudget
		}
		// The declaration names the encoding the file was in; it is
		// dropped, so that the copy is read as the UTF-8 it now is.
		if bytes.HasPrefix(data, []byte("<?xml")) {
			if i := bytes.Index(data, []byte("?>")); i >= 0 {
				data = data[i+2:]
			}
		}
		src, size = bytes.NewReader(data), int64(len(data))
	}
	p := &Package{
		flatParts: map[string]*flatPart{},
		defaults:  map[string]string{},
		overrides: map[string]string{},
		flat:      true,
	}
	p.budget.Store(lim.MaxDecodedBytes)
	if err := p.indexFlat(src, size, lim); err != nil {
		return nil, err
	}
	return p, nil
}
