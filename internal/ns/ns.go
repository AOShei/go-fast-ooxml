// Package ns numbers the XML namespaces and names the relationship types of
// Office Open XML, in both forms a file may use: Transitional, which Office
// writes by default, and ISO Strict.
package ns

import (
	"strings"

	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// Namespaces. A Transitional URI and its Strict counterpart share a number,
// so the rest of the library does not care which the file uses.
const (
	ContentTypes xmlscan.Space = xmlscan.FirstSpace + iota // [Content_Types].xml
	PackageRels                                            // .rels parts
	Rel                                                    // r:id and friends
	CoreProps                                              // cp:
	DC                                                     // dc:
	DCTerms                                                // dcterms:
	ExtProps                                               // docProps/app.xml
	CustomProps                                            // docProps/custom.xml
	VT                                                     // vt: value types in properties
	MC                                                     // markup compatibility
	Word                                                   // w:
	Sheet                                                  // SpreadsheetML main
	Pres                                                   // p:
	Draw                                                   // a:
	Word14                                                 // w14: Word 2010 extensions
	WordDraw                                               // wp: drawings anchored in Word text
	WordShape                                              // wps: Word 2010 shapes and text boxes
	Picture                                                // pic:
	VML                                                    // v: legacy drawings
	Office                                                 // o: legacy Office extensions
	Math                                                   // m: Office Math
	Chart                                                  // c:
	Diagram                                                // dgm: SmartArt
	SheetDraw                                              // xdr: drawings on a worksheet
	Threaded                                               // threaded comments and their authors
	Pres2018                                               // p188: PowerPoint's threaded comments
)

// Spaces is the table handed to every scanner.
var Spaces = map[string]xmlscan.Space{
	"http://schemas.openxmlformats.org/package/2006/content-types":              ContentTypes,
	"http://schemas.openxmlformats.org/package/2006/relationships":              PackageRels,
	"http://schemas.openxmlformats.org/officeDocument/2006/relationships":       Rel,
	"http://purl.oclc.org/ooxml/officeDocument/relationships":                   Rel,
	"http://schemas.openxmlformats.org/package/2006/metadata/core-properties":   CoreProps,
	"http://purl.org/dc/elements/1.1/":                                          DC,
	"http://purl.org/dc/terms/":                                                 DCTerms,
	"http://schemas.openxmlformats.org/officeDocument/2006/extended-properties": ExtProps,
	"http://purl.oclc.org/ooxml/officeDocument/extendedProperties":              ExtProps,
	"http://schemas.openxmlformats.org/officeDocument/2006/custom-properties":   CustomProps,
	"http://purl.oclc.org/ooxml/officeDocument/customProperties":                CustomProps,
	"http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes":      VT,
	"http://purl.oclc.org/ooxml/officeDocument/docPropsVTypes":                  VT,
	"http://schemas.openxmlformats.org/markup-compatibility/2006":               MC,
	"http://schemas.openxmlformats.org/wordprocessingml/2006/main":              Word,
	"http://purl.oclc.org/ooxml/wordprocessingml/main":                          Word,
	"http://schemas.openxmlformats.org/spreadsheetml/2006/main":                 Sheet,
	"http://purl.oclc.org/ooxml/spreadsheetml/main":                             Sheet,
	"http://schemas.openxmlformats.org/presentationml/2006/main":                Pres,
	"http://purl.oclc.org/ooxml/presentationml/main":                            Pres,
	"http://schemas.openxmlformats.org/drawingml/2006/main":                     Draw,
	"http://purl.oclc.org/ooxml/drawingml/main":                                 Draw,
	"http://schemas.microsoft.com/office/word/2010/wordml":                      Word14,
	"http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing":    WordDraw,
	"http://purl.oclc.org/ooxml/drawingml/wordprocessingDrawing":                WordDraw,
	"http://schemas.microsoft.com/office/word/2010/wordprocessingShape":         WordShape,
	"http://schemas.openxmlformats.org/drawingml/2006/picture":                  Picture,
	"http://purl.oclc.org/ooxml/drawingml/picture":                              Picture,
	"urn:schemas-microsoft-com:vml":                                             VML,
	"urn:schemas-microsoft-com:office:office":                                   Office,
	"http://schemas.openxmlformats.org/officeDocument/2006/math":                Math,
	"http://purl.oclc.org/ooxml/officeDocument/math":                            Math,
	"http://schemas.openxmlformats.org/drawingml/2006/chart":                    Chart,
	"http://purl.oclc.org/ooxml/drawingml/chart":                                Chart,
	"http://schemas.openxmlformats.org/drawingml/2006/diagram":                  Diagram,
	"http://purl.oclc.org/ooxml/drawingml/diagram":                              Diagram,
	"http://schemas.openxmlformats.org/drawingml/2006/spreadsheetDrawing":       SheetDraw,
	"http://purl.oclc.org/ooxml/drawingml/spreadsheetDrawing":                   SheetDraw,
	"http://schemas.microsoft.com/office/spreadsheetml/2018/threadedcomments":   Threaded,
	"http://schemas.microsoft.com/office/powerpoint/2018/8/main":                Pres2018,
}

// Relationship kinds: the last segment of a relationship type URI. The part
// before it differs between Transitional, Strict and the package-level
// types, and a few producers write a different one, so relationships are
// recognized by their ending. See IsRel.
const (
	RelOfficeDocument = "officeDocument"
	RelCoreProps      = "metadata/core-properties"
	RelExtProps       = "extended-properties"
	RelExtPropsStrict = "extendedProperties"
	RelCustomProps    = "custom-properties"
	RelCustomStrict   = "customProperties"

	RelStyles    = "styles"
	RelNumbering = "numbering"
	RelFootnotes = "footnotes"
	RelEndnotes  = "endnotes"
	RelComments  = "comments"
	RelHeader    = "header"
	RelFooter    = "footer"
	RelSettings  = "settings"
	RelImage     = "image"
	RelHyperlink = "hyperlink"
	RelOLEObject = "oleObject"
	RelPackage   = "package"
	RelAltChunk  = "aFChunk"
	RelChart     = "chart"
	RelDiagram   = "diagramData"
	RelTheme     = "theme"

	RelWorksheet       = "worksheet"
	RelChartsheet      = "chartsheet"
	RelDialogsheet     = "dialogsheet"
	RelMacrosheet      = "xlMacrosheet"
	RelIntlMacrosheet  = "xlIntlMacrosheet"
	RelSharedStrings   = "sharedStrings"
	RelTable           = "table"
	RelDrawing         = "drawing"
	RelThreadedComment = "threadedComment"
	RelPerson          = "person"
	RelExternalLink    = "externalLink"
	RelPivotTable      = "pivotTable"

	RelSlide          = "slide"
	RelSlideLayout    = "slideLayout"
	RelSlideMaster    = "slideMaster"
	RelNotesSlide     = "notesSlide"
	RelTableStyles    = "tableStyles"
	RelCommentAuthors = "commentAuthors"
	RelAuthors        = "authors"
)

// IsRel reports whether a relationship type URI is of one of the given
// kinds.
func IsRel(typ string, kinds ...string) bool {
	for _, k := range kinds {
		if len(typ) > len(k) && typ[len(typ)-len(k)-1] == '/' && strings.HasSuffix(typ, k) {
			return true
		}
	}
	return false
}
