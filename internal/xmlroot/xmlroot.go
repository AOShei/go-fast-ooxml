// Package xmlroot says which of the Office formats saved as one XML file
// an input is, by its root element: Flat OPC (a Word, Excel or PowerPoint
// package written as XML), WordprocessingML 2003 or SpreadsheetML 2003.
//
// The root element's namespace decides. The processing instruction Office
// writes before it (mso-application) is optional, and names Word for two
// of the formats.
package xmlroot

import (
	"bytes"
	"errors"
	"io"

	"github.com/AOShei/go-fast-core/xmlscan"
)

// Kind is what an input is.
type Kind uint8

const (
	// NotXML: the input does not begin as an XML document does.
	NotXML Kind = iota
	// OtherXML: an XML document of some other kind, or one whose root
	// cannot be read.
	OtherXML
	// FlatOPC: pkg:package, an Office Open XML package written as XML.
	FlatOPC
	// WordML: w:wordDocument, Word 2003's XML document.
	WordML
	// SpreadsheetML: Workbook, Excel 2002 and 2003's XML spreadsheet.
	SpreadsheetML
)

// Namespaces of the three roots.
const (
	PackageNS       = "http://schemas.microsoft.com/office/2006/xmlPackage"
	WordMLNS        = "http://schemas.microsoft.com/office/word/2003/wordml"
	SpreadsheetMLNS = "urn:schemas-microsoft-com:office:spreadsheet"
)

const (
	spacePackage xmlscan.Space = xmlscan.FirstSpace + iota
	spaceWordML
	spaceSpreadsheetML
)

var options = &xmlscan.Options{
	Spaces: map[string]xmlscan.Space{
		PackageNS:       spacePackage,
		WordMLNS:        spaceWordML,
		SpreadsheetMLNS: spaceSpreadsheetML,
	},
	BufferSize: 4096,
	// What comes before the root is a declaration, processing
	// instructions and comments: a root further in than this is not
	// looked for.
	MaxTokenBytes: maxHead,
}

// DTD is the root Detect gives for XML with a document type declaration,
// which is not read: it can declare entities, and none of the formats
// has one.
const DTD = "<!DOCTYPE>"

// maxHead bounds how far into the input the root is looked for.
const maxHead = 64 << 10

// Detect reads the start of the input, up to the root element, and says
// what it is. root is the root element's name as written, for a message;
// empty unless kind is OtherXML and a root was read.
func Detect(r io.ReaderAt) (kind Kind, root string) {
	head := io.NewSectionReader(r, 0, maxHead)
	var first [512]byte
	n, _ := head.ReadAt(first[:], 0)
	if !looksLikeXML(first[:n]) {
		return NotXML, ""
	}
	s := xmlscan.New(head, options)
	defer s.Release()
	for {
		switch s.Next() {
		case xmlscan.Start:
			switch {
			case s.Is(spacePackage, "package"):
				return FlatOPC, ""
			case s.Is(spaceWordML, "wordDocument"):
				return WordML, ""
			case s.Is(spaceSpreadsheetML, "Workbook"):
				return SpreadsheetML, ""
			}
			return OtherXML, string(s.Name())
		case xmlscan.EOF:
			if errors.Is(s.Err(), xmlscan.ErrDTD) {
				return OtherXML, DTD
			}
			return OtherXML, ""
		}
	}
}

// looksLikeXML reports whether the input begins as XML does: with a byte
// order mark, or "<" in UTF-8 or UTF-16, after white space.
func looksLikeXML(b []byte) bool {
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}), bytes.HasPrefix(b, []byte{0xFF, 0xFE}), bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		return true
	case len(b) >= 2 && (b[0] == '<' && b[1] == 0 || b[0] == 0 && b[1] == '<'):
		return true
	}
	b = bytes.TrimLeft(b, " \t\r\n")
	return len(b) > 0 && b[0] == '<'
}
