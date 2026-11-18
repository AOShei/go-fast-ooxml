// Package ooxmltest is the test harness of go-fast-ooxml: an in-memory
// builder for Office Open XML packages, scorers, oracle wrappers, the corpus
// manifest and the checks every extracted document must pass.
package ooxmltest

import (
	"archive/zip"
	"bytes"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

// Content types of the parts the builder knows.
const (
	CTDocument      = "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"
	CTStyles        = "application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"
	CTNumbering     = "application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml"
	CTWorkbook      = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"
	CTWorksheet     = "application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"
	CTSharedStrings = "application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml"
	CTSheetStyles   = "application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"
	CTPresentation  = "application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"
	CTSlide         = "application/vnd.openxmlformats-officedocument.presentationml.slide+xml"
	CTSlideLayout   = "application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"
	CTSlideMaster   = "application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"
	CTTheme         = "application/vnd.openxmlformats-officedocument.theme+xml"
	CTCoreProps     = "application/vnd.openxmlformats-package.core-properties+xml"
	CTExtProps      = "application/vnd.openxmlformats-officedocument.extended-properties+xml"
	CTCustomProps   = "application/vnd.openxmlformats-officedocument.custom-properties+xml"
	CTVBA           = "application/vnd.ms-office.vbaProject"
)

// Relationship types.
const (
	relBase          = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/"
	RelOfficeDoc     = relBase + "officeDocument"
	RelStyles        = relBase + "styles"
	RelNumbering     = relBase + "numbering"
	RelWorksheet     = relBase + "worksheet"
	RelSharedStrings = relBase + "sharedStrings"
	RelSlide         = relBase + "slide"
	RelSlideLayout   = relBase + "slideLayout"
	RelSlideMaster   = relBase + "slideMaster"
	RelTheme         = relBase + "theme"
	RelHyperlink     = relBase + "hyperlink"
	RelCoreProps     = "http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties"
	RelExtProps      = relBase + "extended-properties"
	RelCustomProps   = relBase + "custom-properties"
	RelFootnotes     = relBase + "footnotes"
	RelEndnotes      = relBase + "endnotes"
	RelComments      = relBase + "comments"
	RelHeader        = relBase + "header"
	RelFooter        = relBase + "footer"
	RelSettings      = relBase + "settings"
	RelImage         = relBase + "image"
	RelOLEObject     = relBase + "oleObject"
	RelPackage       = relBase + "package"
	RelTemplate      = relBase + "attachedTemplate"
)

// Content types of the supporting parts of a Word document.
const (
	CTFootnotes = "application/vnd.openxmlformats-officedocument.wordprocessingml.footnotes+xml"
	CTEndnotes  = "application/vnd.openxmlformats-officedocument.wordprocessingml.endnotes+xml"
	CTComments  = "application/vnd.openxmlformats-officedocument.wordprocessingml.comments+xml"
	CTHeader    = "application/vnd.openxmlformats-officedocument.wordprocessingml.header+xml"
	CTFooter    = "application/vnd.openxmlformats-officedocument.wordprocessingml.footer+xml"
	CTSettings  = "application/vnd.openxmlformats-officedocument.wordprocessingml.settings+xml"
)

// WordNS declares the namespace prefixes used in Word parts: w, r, w14,
// mc, wp, wps, a, pic, v, o, m and c. It is for the root element of a
// part a test writes itself.
const WordNS = ` xmlns:w="` + NSWord + `" xmlns:r="` + NSRel + `"` +
	` xmlns:w14="http://schemas.microsoft.com/office/word/2010/wordml"` +
	` xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006"` +
	` xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"` +
	` xmlns:wps="http://schemas.microsoft.com/office/word/2010/wordprocessingShape"` +
	` xmlns:a="` + NSDraw + `" xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"` +
	` xmlns:v="urn:schemas-microsoft-com:vml" xmlns:o="urn:schemas-microsoft-com:office:office"` +
	` xmlns:m="http://schemas.openxmlformats.org/officeDocument/2006/math"` +
	` xmlns:c="http://schemas.openxmlformats.org/drawingml/2006/chart"`

// WordPart adds a supporting part of a Word document (footnotes, a
// header, comments) and its relationship from the main part, and returns
// the relationship id. root is the element name without prefix
// ("footnotes", "hdr"); xml is its content.
func (p *Package) WordPart(name, contentType, relType, root, xml string) string {
	id := p.Rel("word/document.xml", relType, name)
	p.Add("word/"+name, contentType, `<w:`+root+WordNS+`>`+xml+`</w:`+root+`>`)
	return id
}

// XML namespaces, for use in the snippets tests pass to the builder.
const (
	NSWord  = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	NSSheet = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
	NSPres  = "http://schemas.openxmlformats.org/presentationml/2006/main"
	NSDraw  = "http://schemas.openxmlformats.org/drawingml/2006/main"
	NSRel   = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
)

const xmlHeader = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

// Package assembles an Office Open XML package in memory. The zero value is
// not usable; start with NewPackage or one of the presets (Docx, Xlsx, Pptx).
//
// Output is deterministic: parts are written in the order added, with a
// fixed timestamp, so the same calls always give the same bytes.
type Package struct {
	parts []builtPart
	rels  map[string][]rel // keyed by source part name, "" for the package
}

type builtPart struct {
	name, contentType string
	data              []byte
}

type rel struct {
	id, typ, target string
	external        bool
}

// NewPackage returns an empty package.
func NewPackage() *Package {
	return &Package{rels: map[string][]rel{}}
}

// Add adds or replaces a part. name is the path inside the package without
// a leading slash, e.g. "word/document.xml". An XML declaration is put in
// front of data unless it already has one or the part is not XML.
func (p *Package) Add(name, contentType, data string) *Package {
	if strings.HasSuffix(contentType, "xml") && !strings.HasPrefix(data, "<?xml") {
		data = xmlHeader + data
	}
	return p.AddBytes(name, contentType, []byte(data))
}

// AddBytes adds or replaces a part with exactly the given bytes.
func (p *Package) AddBytes(name, contentType string, data []byte) *Package {
	for i := range p.parts {
		if p.parts[i].name == name {
			p.parts[i] = builtPart{name, contentType, data}
			return p
		}
	}
	p.parts = append(p.parts, builtPart{name, contentType, data})
	return p
}

// Rel adds a relationship from source to target. source is a part name, or
// "" for the package itself. target is written as given, so it is relative
// to the directory of source. It returns the relationship id.
func (p *Package) Rel(source, typ, target string) string {
	return p.addRel(source, typ, target, false)
}

// ExternalRel adds a relationship to something outside the package, such
// as a hyperlink URL.
func (p *Package) ExternalRel(source, typ, target string) string {
	return p.addRel(source, typ, target, true)
}

func (p *Package) addRel(source, typ, target string, external bool) string {
	id := fmt.Sprintf("rId%d", len(p.rels[source])+1)
	p.rels[source] = append(p.rels[source], rel{id, typ, target, external})
	return id
}

// Bytes returns the package as a zip archive.
func (p *Package) Bytes() []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	write := func(name string, data []byte) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: stamp})
		if err != nil {
			panic(err) // cannot happen on a bytes.Buffer with valid names
		}
		w.Write(data)
	}

	var ct strings.Builder
	ct.WriteString(xmlHeader)
	ct.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	ct.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	ct.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)
	ct.WriteString(`<Default Extension="png" ContentType="image/png"/>`)
	ct.WriteString(`<Default Extension="jpeg" ContentType="image/jpeg"/>`)
	for _, part := range p.parts {
		if part.contentType == "" || part.contentType == "image/png" || part.contentType == "image/jpeg" || part.name == "[Content_Types].xml" {
			continue
		}
		fmt.Fprintf(&ct, `<Override PartName="/%s" ContentType="%s"/>`, part.name, part.contentType)
	}
	ct.WriteString(`</Types>`)
	// A test that needs a particular content-types table adds one itself.
	custom := false
	for _, part := range p.parts {
		custom = custom || part.name == "[Content_Types].xml"
	}
	if !custom {
		write("[Content_Types].xml", []byte(ct.String()))
	}

	sources := make([]string, 0, len(p.rels))
	for s := range p.rels {
		sources = append(sources, s)
	}
	sort.Strings(sources)
	for _, s := range sources {
		var rb strings.Builder
		rb.WriteString(xmlHeader)
		rb.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
		for _, r := range p.rels[s] {
			mode := ""
			if r.external {
				mode = ` TargetMode="External"`
			}
			fmt.Fprintf(&rb, `<Relationship Id="%s" Type="%s" Target="%s"%s/>`, r.id, r.typ, EscapeXML(r.target), mode)
		}
		rb.WriteString(`</Relationships>`)
		name := "_rels/.rels" // the package's own relationships
		if s != "" {
			name = path.Join(path.Dir(s), "_rels", path.Base(s)+".rels")
		}
		write(name, []byte(rb.String()))
	}
	for _, part := range p.parts {
		write(part.name, part.data)
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// EscapeXML escapes text for use as XML character data or an attribute
// value.
func EscapeXML(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

// Docx returns a minimal Word package whose body holds the given XML,
// which is the content of w:body: paragraphs, tables and so on, with the
// "w" prefix bound to the WordprocessingML namespace and "r" to the
// relationships namespace.
func Docx(body string) *Package {
	p := NewPackage()
	p.Rel("", RelOfficeDoc, "word/document.xml")
	p.Add("word/document.xml", CTDocument,
		`<w:document`+WordNS+`><w:body>`+body+
			`<w:sectPr><w:pgSz w:w="12240" w:h="15840"/></w:sectPr></w:body></w:document>`)
	return p
}

// DocxSection is Docx with section properties of the caller's choosing in
// place of the default ones: xml is the content of the body's w:sectPr.
func DocxSection(body, sectPr string) *Package {
	p := NewPackage()
	p.Rel("", RelOfficeDoc, "word/document.xml")
	p.Add("word/document.xml", CTDocument,
		`<w:document`+WordNS+`><w:body>`+body+`<w:sectPr>`+sectPr+`</w:sectPr></w:body></w:document>`)
	return p
}

// StyledPara returns a Word paragraph in the given paragraph style.
func StyledPara(style, text string) string {
	return `<w:p><w:pPr><w:pStyle w:val="` + style + `"/></w:pPr><w:r><w:t xml:space="preserve">` + EscapeXML(text) + `</w:t></w:r></w:p>`
}

// ListPara returns a Word paragraph numbered by a list instance at a level.
func ListPara(numID, level int, text string) string {
	return fmt.Sprintf(`<w:p><w:pPr><w:numPr><w:ilvl w:val="%d"/><w:numId w:val="%d"/></w:numPr></w:pPr><w:r><w:t xml:space="preserve">%s</w:t></w:r></w:p>`,
		level, numID, EscapeXML(text))
}

// WithStyles adds a styles part to a Word package. xml is the content of
// w:styles.
func (p *Package) WithStyles(xml string) *Package {
	p.Rel("word/document.xml", RelStyles, "styles.xml")
	return p.Add("word/styles.xml", CTStyles, `<w:styles xmlns:w="`+NSWord+`">`+xml+`</w:styles>`)
}

// WithNumbering adds a numbering part to a Word package. xml is the
// content of w:numbering.
func (p *Package) WithNumbering(xml string) *Package {
	p.Rel("word/document.xml", RelNumbering, "numbering.xml")
	return p.Add("word/numbering.xml", CTNumbering, `<w:numbering xmlns:w="`+NSWord+`">`+xml+`</w:numbering>`)
}

// WithCore adds core properties (title, author, dates) to a package. xml is
// the content of cp:coreProperties, with the prefixes cp, dc and dcterms
// bound.
func (p *Package) WithCore(xml string) *Package {
	p.Rel("", RelCoreProps, "docProps/core.xml")
	return p.Add("docProps/core.xml", CTCoreProps,
		`<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties"`+
			` xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/"`+
			` xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">`+xml+`</cp:coreProperties>`)
}

// WithApp adds extended properties (application, company) to a package.
// xml is the content of the Properties element.
func (p *Package) WithApp(xml string) *Package {
	p.Rel("", RelExtProps, "docProps/app.xml")
	return p.Add("docProps/app.xml", CTExtProps,
		`<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties"`+
			` xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes">`+xml+`</Properties>`)
}

// WithCustom adds custom properties to a package. xml is the content of
// the Properties element: property elements, with the prefix vt bound.
func (p *Package) WithCustom(xml string) *Package {
	p.Rel("", RelCustomProps, "docProps/custom.xml")
	return p.Add("docProps/custom.xml", CTCustomProps,
		`<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/custom-properties"`+
			` xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes">`+xml+`</Properties>`)
}

// Para returns a Word paragraph with one run of plain text.
func Para(text string) string {
	return `<w:p><w:r><w:t xml:space="preserve">` + EscapeXML(text) + `</w:t></w:r></w:p>`
}

// Sheet describes one worksheet for Xlsx.
type Sheet struct {
	Name string
	// Data is the content of sheetData: row elements.
	Data string
	// State is "", "hidden" or "veryHidden".
	State string
	// Extra is XML placed after sheetData inside the worksheet element,
	// e.g. mergeCells.
	Extra string
	// Before is XML placed before sheetData, e.g. cols.
	Before string
}

// Xlsx returns a minimal Excel package with the given sheets.
func Xlsx(sheets ...Sheet) *Package {
	p := NewPackage()
	p.Rel("", RelOfficeDoc, "xl/workbook.xml")
	var wb strings.Builder
	wb.WriteString(`<workbook xmlns="` + NSSheet + `" xmlns:r="` + NSRel + `"><sheets>`)
	files := make([]string, len(sheets))
	for i, s := range sheets {
		files[i] = fmt.Sprintf("worksheets/sheet%d.xml", i+1)
		id := p.Rel("xl/workbook.xml", RelWorksheet, files[i])
		state := ""
		if s.State != "" {
			state = ` state="` + s.State + `"`
		}
		fmt.Fprintf(&wb, `<sheet name="%s" sheetId="%d"%s r:id="%s"/>`, EscapeXML(s.Name), i+1, state, id)
	}
	wb.WriteString(`</sheets></workbook>`)
	p.Add("xl/workbook.xml", CTWorkbook, wb.String())
	for i, s := range sheets {
		p.Add("xl/"+files[i], CTWorksheet,
			`<worksheet xmlns="`+NSSheet+`" xmlns:r="`+NSRel+`">`+s.Before+`<sheetData>`+s.Data+`</sheetData>`+s.Extra+`</worksheet>`)
	}
	return p
}

// WithSharedStrings adds a shared-strings part holding the given strings,
// in order, so that a cell <c t="s"><v>0</v></c> refers to the first.
func (p *Package) WithSharedStrings(strs ...string) *Package {
	var sb strings.Builder
	fmt.Fprintf(&sb, `<sst xmlns="%s" count="%d" uniqueCount="%d">`, NSSheet, len(strs), len(strs))
	for _, s := range strs {
		sb.WriteString(`<si><t xml:space="preserve">` + EscapeXML(s) + `</t></si>`)
	}
	sb.WriteString(`</sst>`)
	p.Rel("xl/workbook.xml", RelSharedStrings, "sharedStrings.xml")
	return p.Add("xl/sharedStrings.xml", CTSharedStrings, sb.String())
}

// WithSheetStyles adds a styles part to an Excel package. xml is the
// content of styleSheet.
func (p *Package) WithSheetStyles(xml string) *Package {
	p.Rel("xl/workbook.xml", RelStyles, "styles.xml")
	return p.Add("xl/styles.xml", CTSheetStyles, `<styleSheet xmlns="`+NSSheet+`">`+xml+`</styleSheet>`)
}

// InlineCell returns a cell holding an inline string.
func InlineCell(ref, text string) string {
	return `<c r="` + ref + `" t="inlineStr"><is><t xml:space="preserve">` + EscapeXML(text) + `</t></is></c>`
}

// PresNS declares the namespace prefixes used in PowerPoint parts: p, a,
// r, mc, c, dgm, m, a14 and p188. It is for the root element of a part a
// test writes itself.
const PresNS = ` xmlns:p="` + NSPres + `" xmlns:a="` + NSDraw + `" xmlns:r="` + NSRel + `"` +
	` xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006"` +
	` xmlns:c="http://schemas.openxmlformats.org/drawingml/2006/chart"` +
	` xmlns:dgm="http://schemas.openxmlformats.org/drawingml/2006/diagram"` +
	` xmlns:m="http://schemas.openxmlformats.org/officeDocument/2006/math"` +
	` xmlns:a14="http://schemas.microsoft.com/office/drawing/2010/main"` +
	` xmlns:p188="http://schemas.microsoft.com/office/powerpoint/2018/8/main"`

// Content and relationship types of the supporting parts of a
// presentation.
const (
	CTNotesSlide    = "application/vnd.openxmlformats-officedocument.presentationml.notesSlide+xml"
	CTSlideComments = "application/vnd.openxmlformats-officedocument.presentationml.comments+xml"
	CTTableStyles   = "application/vnd.openxmlformats-officedocument.presentationml.tableStyles+xml"
	RelNotesSlide   = relBase + "notesSlide"
	RelTableStyle   = relBase + "tableStyles"
)

// Slide describes one slide for Pptx.
type Slide struct {
	// Shapes is the content of the slide's shape tree after the mandatory
	// group properties: p:sp, p:pic, p:graphicFrame elements, with the
	// prefixes of PresNS bound.
	Shapes string
	// Hidden marks the slide as hidden (show="0").
	Hidden bool
	// Background is the slide's p:bg element, if it has one.
	Background string
	// After is what follows the slide's content: p:clrMapOvr, p:timing.
	After string
	// Notes, if not empty, is the shape tree of the slide's notes page.
	Notes string
}

const (
	spTreeHead    = `<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr/>`
	defaultClrMap = `<p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/>`
)

// Pptx returns a minimal PowerPoint package: one theme, one master, one
// layout and the given slides. The slide size is 720 by 540 points. The
// master and the layout are empty; Master and Layout replace them.
func Pptx(slides ...Slide) *Package {
	p := NewPackage()
	p.Rel("", RelOfficeDoc, "ppt/presentation.xml")

	masterID := p.Rel("ppt/presentation.xml", RelSlideMaster, "slideMasters/slideMaster1.xml")
	var ids strings.Builder
	for i, s := range slides {
		file := fmt.Sprintf("slides/slide%d.xml", i+1)
		id := p.Rel("ppt/presentation.xml", RelSlide, file)
		fmt.Fprintf(&ids, `<p:sldId id="%d" r:id="%s"/>`, 256+i, id)
		show := ""
		if s.Hidden {
			show = ` show="0"`
		}
		p.Rel("ppt/"+file, RelSlideLayout, "../slideLayouts/slideLayout1.xml")
		p.Add("ppt/"+file, CTSlide,
			`<p:sld`+PresNS+show+`><p:cSld>`+s.Background+`<p:spTree>`+spTreeHead+s.Shapes+`</p:spTree></p:cSld>`+s.After+`</p:sld>`)
		if s.Notes != "" {
			notes := fmt.Sprintf("notesSlides/notesSlide%d.xml", i+1)
			p.Rel("ppt/"+file, RelNotesSlide, "../"+notes)
			p.Add("ppt/"+notes, CTNotesSlide, `<p:notes`+PresNS+`><p:cSld><p:spTree>`+spTreeHead+s.Notes+`</p:spTree></p:cSld></p:notes>`)
		}
	}
	p.Add("ppt/presentation.xml", CTPresentation,
		`<p:presentation`+PresNS+`><p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="`+masterID+`"/></p:sldMasterIdLst>`+
			`<p:sldIdLst>`+ids.String()+`</p:sldIdLst><p:sldSz cx="9144000" cy="6858000"/><p:notesSz cx="6858000" cy="9144000"/></p:presentation>`)

	p.Rel("ppt/slideMasters/slideMaster1.xml", RelSlideLayout, "../slideLayouts/slideLayout1.xml")
	p.Rel("ppt/slideMasters/slideMaster1.xml", RelTheme, "../theme/theme1.xml")
	p.Rel("ppt/slideLayouts/slideLayout1.xml", RelSlideMaster, "../slideMasters/slideMaster1.xml")
	p.Add("ppt/theme/theme1.xml", CTTheme, minimalTheme)
	return p.Master("", "", "", "").Layout("", "", "")
}

// Master sets the presentation's slide master: its background (a p:bg
// element), its shapes, its colour map (a p:clrMap element; the usual
// one if empty) and its text styles (a p:txStyles element).
func (p *Package) Master(background, shapes, clrMap, txStyles string) *Package {
	if clrMap == "" {
		clrMap = defaultClrMap
	}
	return p.Add("ppt/slideMasters/slideMaster1.xml", CTSlideMaster,
		`<p:sldMaster`+PresNS+`><p:cSld>`+background+`<p:spTree>`+spTreeHead+shapes+`</p:spTree></p:cSld>`+clrMap+
			`<p:sldLayoutIdLst><p:sldLayoutId id="2147483649" r:id="rId1"/></p:sldLayoutIdLst>`+txStyles+`</p:sldMaster>`)
}

// Layout sets the one slide layout: its background, its shapes, and
// what follows its content (p:clrMapOvr).
func (p *Package) Layout(background, shapes, after string) *Package {
	return p.Add("ppt/slideLayouts/slideLayout1.xml", CTSlideLayout,
		`<p:sldLayout`+PresNS+`><p:cSld name="Layout">`+background+`<p:spTree>`+spTreeHead+shapes+`</p:spTree></p:cSld>`+after+`</p:sldLayout>`)
}

// Xfrm returns the a:xfrm element of a shape at the given position and
// size in points.
func Xfrm(x, y, w, h float64) string {
	const emu = 12700
	return fmt.Sprintf(`<a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm>`, int64(x*emu), int64(y*emu), int64(w*emu), int64(h*emu))
}

// Shape returns a PowerPoint shape. nv is the content of p:nvPr (a p:ph
// element for a placeholder), props the content of p:spPr, and body the
// p:txBody element, or "" for a shape with no text.
func Shape(id int, nv, props, body string) string {
	return fmt.Sprintf(`<p:sp><p:nvSpPr><p:cNvPr id="%d" name="Shape %d"/><p:cNvSpPr/><p:nvPr>%s</p:nvPr></p:nvSpPr><p:spPr>%s</p:spPr>%s</p:sp>`, id, id, nv, props, body)
}

// TextBody returns a p:txBody element holding the given a:p elements.
func TextBody(paragraphs ...string) string {
	return `<p:txBody><a:bodyPr/>` + strings.Join(paragraphs, "") + `</p:txBody>`
}

// DrawPara returns an a:p element with one run of the given text.
func DrawPara(text string) string {
	return `<a:p><a:r><a:rPr lang="en-US"/><a:t>` + EscapeXML(text) + `</a:t></a:r></a:p>`
}

// TextShape returns a PowerPoint text box at the given position and size
// in points, holding one paragraph per text.
func TextShape(id int, x, y, w, h float64, paragraphs ...string) string {
	ps := make([]string, len(paragraphs))
	for i, t := range paragraphs {
		ps[i] = DrawPara(t)
	}
	return Shape(id, "", Xfrm(x, y, w, h)+`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom>`, TextBody(ps...))
}

const minimalTheme = `<a:theme xmlns:a="` + NSDraw + `" name="Office"><a:themeElements>` +
	`<a:clrScheme name="Office"><a:dk1><a:sysClr val="windowText" lastClr="000000"/></a:dk1><a:lt1><a:sysClr val="window" lastClr="FFFFFF"/></a:lt1>` +
	`<a:dk2><a:srgbClr val="44546A"/></a:dk2><a:lt2><a:srgbClr val="E7E6E6"/></a:lt2>` +
	`<a:accent1><a:srgbClr val="4472C4"/></a:accent1><a:accent2><a:srgbClr val="ED7D31"/></a:accent2><a:accent3><a:srgbClr val="A5A5A5"/></a:accent3>` +
	`<a:accent4><a:srgbClr val="FFC000"/></a:accent4><a:accent5><a:srgbClr val="5B9BD5"/></a:accent5><a:accent6><a:srgbClr val="70AD47"/></a:accent6>` +
	`<a:hlink><a:srgbClr val="0563C1"/></a:hlink><a:folHlink><a:srgbClr val="954F72"/></a:folHlink></a:clrScheme>` +
	`<a:fontScheme name="Office"><a:majorFont><a:latin typeface="Calibri Light"/><a:ea typeface=""/><a:cs typeface=""/></a:majorFont>` +
	`<a:minorFont><a:latin typeface="Calibri"/><a:ea typeface=""/><a:cs typeface=""/></a:minorFont></a:fontScheme>` +
	`<a:fmtScheme name="Office"><a:fillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:fillStyleLst>` +
	`<a:lnStyleLst><a:ln w="6350"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln><a:ln w="12700"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln><a:ln w="19050"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln></a:lnStyleLst>` +
	`<a:effectStyleLst><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle></a:effectStyleLst>` +
	`<a:bgFillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:bgFillStyleLst>` +
	`</a:fmtScheme></a:themeElements></a:theme>`
