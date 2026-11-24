package gofastooxml

import (
	"errors"
	"fmt"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/props"
	"github.com/AOShei/go-fast-ooxml/internal/sml"
	"github.com/AOShei/go-fast-ooxml/internal/xmlindex"
	"github.com/AOShei/go-fast-ooxml/internal/xmlroot"
)

// The Office formats saved as one XML file are told by their root
// element:
//
//   - Flat OPC (pkg:package): a Word, Excel or PowerPoint package written
//     as XML, read as the archive it stands for and giving the same
//     document, with Metadata.Extra["packaging"] "flat_opc".
//   - SpreadsheetML 2003 (Workbook): Excel 2002 and 2003's XML
//     spreadsheet, format "spreadsheetml", read into sheets as .xlsx is
//     (internal/sml/ss2003.go).
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
	case xmlroot.NotXML, xmlroot.FlatOPC, xmlroot.SpreadsheetML:
		return nil
	case xmlroot.WordML:
		return fmt.Errorf("gofastooxml: a WordprocessingML 2003 document (Word 2003 XML), not read yet: %w", gofastcore.ErrUnsupported)
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

// Formats and media types of the Office 2003 XML formats.
const (
	formatSpreadsheetML = "spreadsheetml"
	mimeSpreadsheetML   = "application/vnd.ms-spreadsheetml"
)

// maxXMLElements bounds the elements an Office 2003 XML file's index
// holds: its root's children, and a worksheet's. Excel writes a few for
// each sheet.
const maxXMLElements = 1 << 16

// openSpreadsheetML opens a SpreadsheetML 2003 workbook: its index, the
// package of its elements, its properties and its sheets.
func (f *File) openSpreadsheetML(first bool, lim opc.Limits) (*session, error) {
	x, err := xmlindex.Read(f.body, f.bodySize, xmlindex.Options{Descend: sml.SSDescend, MaxElements: maxXMLElements, MaxCopy: lim.MaxDecodedBytes})
	if err != nil {
		return nil, xmlError("a SpreadsheetML 2003 workbook", err)
	}
	layout := sml.SSIndex(x)
	pkg := opc.NewSections(x.Src, lim, layout.Sections)
	meta, err := props.Read2003(pkg, sml.SSProps, sml.SSCustom)
	if first {
		f.source = gofastcore.Source{Format: formatSpreadsheetML, MIME: mimeSpreadsheetML, Name: f.opts.Name, Size: f.size}
		f.meta = meta.Metadata
		f.note2003(x, meta, err)
	}
	sess := &session{pkg: pkg}
	sess.src = sml.OpenSS(pkg, layout, &f.opts, sml.Config{
		MaxCells: gofastcore.ResolveLimit(f.e.MaxCells, DefaultMaxCells),
	})
	return sess, nil
}

// note2003 keeps the warnings of opening an Office 2003 XML file: where
// it breaks off, and what reading its properties met.
func (f *File) note2003(x *xmlindex.Index, meta props.Result, err error) {
	warn := func(severity gofastcore.Severity, code, message string) {
		f.opened = append(f.opened, gofastcore.Warning{Code: code, Severity: severity, Message: message})
	}
	if x.Damage != nil {
		warn(gofastcore.SeverityWarning, gofastcore.WarnInputRepaired,
			clean.Label(fmt.Sprintf("the XML file cannot be read past byte %d (%v); what is complete before it is read", x.At, x.Damage)))
	}
	if len(meta.Damaged) > 0 {
		warn(gofastcore.SeverityWarning, gofastcore.WarnInputStreamDamaged,
			"the document properties could not be read to their end; the properties read before the damage are kept")
	}
	f.opened = append(f.opened, meta.Warnings()...)
	if meta.Removed > 0 {
		warn(gofastcore.SeverityInfo, gofastcore.WarnTextControlCharactersRemoved, fmt.Sprintf("%d in document properties", meta.Removed))
	}
	if errors.Is(err, opc.ErrBudget) {
		warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "decompressed size limit reached while reading document properties")
	}
}

// xmlError is the error for an Office 2003 XML file whose index could not
// be read.
func xmlError(what string, err error) error {
	shared := gofastcore.ErrMalformed
	switch {
	case errors.Is(err, xmlindex.ErrTooLarge):
		shared = gofastcore.ErrLimitExceeded
		err = errors.New("converted to UTF-8, it is larger than the decompressed size limit")
	case errors.Is(err, xmlindex.ErrTooMany):
		shared = gofastcore.ErrLimitExceeded
	}
	return fmt.Errorf("gofastooxml: %s: %s: %w", what, clean.Label(err.Error()), shared)
}
