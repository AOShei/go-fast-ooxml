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
)

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
		if part.contentType == "" || part.contentType == "image/png" || part.contentType == "image/jpeg" {
			continue
		}
		fmt.Fprintf(&ct, `<Override PartName="/%s" ContentType="%s"/>`, part.name, part.contentType)
	}
	ct.WriteString(`</Types>`)
	write("[Content_Types].xml", []byte(ct.String()))

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
		`<w:document xmlns:w="`+NSWord+`" xmlns:r="`+NSRel+`"><w:body>`+body+
			`<w:sectPr><w:pgSz w:w="12240" w:h="15840"/></w:sectPr></w:body></w:document>`)
	return p
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
			`<worksheet xmlns="`+NSSheet+`" xmlns:r="`+NSRel+`"><sheetData>`+s.Data+`</sheetData>`+s.Extra+`</worksheet>`)
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

// Slide describes one slide for Pptx.
type Slide struct {
	// Shapes is the content of the slide's shape tree after the mandatory
	// group properties: p:sp, p:pic, p:graphicFrame elements, with the
	// prefixes "p", "a" and "r" bound.
	Shapes string
	// Hidden marks the slide as hidden (show="0").
	Hidden bool
}

// Pptx returns a minimal PowerPoint package: one theme, one master, one
// layout and the given slides. The slide size is 720 by 540 points.
func Pptx(slides ...Slide) *Package {
	const (
		ns     = ` xmlns:p="` + NSPres + `" xmlns:a="` + NSDraw + `" xmlns:r="` + NSRel + `"`
		spTree = `<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr/>`
		clrMap = `<p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/>`
	)
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
			`<p:sld`+ns+show+`><p:cSld><p:spTree>`+spTree+s.Shapes+`</p:spTree></p:cSld></p:sld>`)
	}
	p.Add("ppt/presentation.xml", CTPresentation,
		`<p:presentation`+ns+`><p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="`+masterID+`"/></p:sldMasterIdLst>`+
			`<p:sldIdLst>`+ids.String()+`</p:sldIdLst><p:sldSz cx="9144000" cy="6858000"/><p:notesSz cx="6858000" cy="9144000"/></p:presentation>`)

	layoutID := p.Rel("ppt/slideMasters/slideMaster1.xml", RelSlideLayout, "../slideLayouts/slideLayout1.xml")
	p.Rel("ppt/slideMasters/slideMaster1.xml", RelTheme, "../theme/theme1.xml")
	p.Add("ppt/slideMasters/slideMaster1.xml", CTSlideMaster,
		`<p:sldMaster`+ns+`><p:cSld><p:spTree>`+spTree+`</p:spTree></p:cSld>`+clrMap+
			`<p:sldLayoutIdLst><p:sldLayoutId id="2147483649" r:id="`+layoutID+`"/></p:sldLayoutIdLst></p:sldMaster>`)
	p.Rel("ppt/slideLayouts/slideLayout1.xml", RelSlideMaster, "../slideMasters/slideMaster1.xml")
	p.Add("ppt/slideLayouts/slideLayout1.xml", CTSlideLayout,
		`<p:sldLayout`+ns+` type="blank"><p:cSld name="Blank"><p:spTree>`+spTree+`</p:spTree></p:cSld></p:sldLayout>`)
	p.Add("ppt/theme/theme1.xml", CTTheme, minimalTheme)
	return p
}

// TextShape returns a PowerPoint text box at the given position and size
// in points, holding one paragraph per text.
func TextShape(id int, x, y, w, h float64, paragraphs ...string) string {
	const emu = 12700
	var sb strings.Builder
	fmt.Fprintf(&sb, `<p:sp><p:nvSpPr><p:cNvPr id="%d" name="TextBox %d"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr>`, id, id)
	fmt.Fprintf(&sb, `<p:spPr><a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr>`,
		int64(x*emu), int64(y*emu), int64(w*emu), int64(h*emu))
	sb.WriteString(`<p:txBody><a:bodyPr/>`)
	for _, t := range paragraphs {
		sb.WriteString(`<a:p><a:r><a:rPr lang="en-US"/><a:t>` + EscapeXML(t) + `</a:t></a:r></a:p>`)
	}
	sb.WriteString(`</p:txBody></p:sp>`)
	return sb.String()
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
