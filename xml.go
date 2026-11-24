package gofastooxml

import (
	"fmt"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/xmlroot"
)

// The Office formats saved as one XML file are told by their root
// element:
//
//   - Flat OPC (pkg:package): a Word, Excel or PowerPoint package written
//     as XML, read as the archive it stands for and giving the same
//     document, with Metadata.Extra["packaging"] "flat_opc".
//
// Any other XML is refused with ErrUnsupported, so that XML sent here by
// mistake is seen to be.
const xmlFormats = "Flat OPC, WordprocessingML 2003, SpreadsheetML 2003"

// detectXML says what the file is if it is XML, and refuses XML that is
// none of the formats read.
func (f *File) detectXML() error {
	kind, root := xmlroot.Detect(f.body)
	f.xml = kind
	switch kind {
	case xmlroot.NotXML, xmlroot.FlatOPC:
		return nil
	case xmlroot.WordML:
		return fmt.Errorf("gofastooxml: a WordprocessingML 2003 document (Word 2003 XML), not read yet: %w", gofastcore.ErrUnsupported)
	case xmlroot.SpreadsheetML:
		return fmt.Errorf("gofastooxml: a SpreadsheetML 2003 workbook (Excel 2003 XML), not read yet: %w", gofastcore.ErrUnsupported)
	}
	what := "XML"
	switch root {
	case "":
	case xmlroot.DTD:
		what = "XML with a document type declaration"
	default:
		what = "XML whose root is " + clean.Label(root)
	}
	return fmt.Errorf("gofastooxml: %s, none of the Office formats saved as XML (%s): %w", what, xmlFormats, gofastcore.ErrUnsupported)
}
