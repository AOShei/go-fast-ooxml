package ooxmltest

import (
	"fmt"
	"strings"
)

// Visio packages: content types, relationship types and the namespace.
const (
	NSVisio          = "http://schemas.microsoft.com/office/visio/2012/main"
	CTVisioDrawing   = "application/vnd.ms-visio.drawing.main+xml"
	CTVisioPages     = "application/vnd.ms-visio.pages+xml"
	CTVisioPage      = "application/vnd.ms-visio.page+xml"
	CTVisioMasters   = "application/vnd.ms-visio.masters+xml"
	CTVisioMaster    = "application/vnd.ms-visio.master+xml"
	CTVisioComments  = "application/vnd.ms-visio.comments+xml"
	RelVisioDocument = "http://schemas.microsoft.com/visio/2010/relationships/document"
	RelVisioPages    = "http://schemas.microsoft.com/visio/2010/relationships/pages"
	RelVisioPage     = "http://schemas.microsoft.com/visio/2010/relationships/page"
	RelVisioMasters  = "http://schemas.microsoft.com/visio/2010/relationships/masters"
	RelVisioMaster   = "http://schemas.microsoft.com/visio/2010/relationships/master"
	RelVisioComments = "http://schemas.microsoft.com/visio/2010/relationships/comments"
)

// VisioPage is a page of a drawing that Visio builds.
type VisioPage struct {
	Name       string
	Background bool
	// BackPage is the ID of the page behind this one, which is its index
	// in Visio.Pages; -1, or an empty Back, for none.
	Back string
	// Width and Height are the page's size in inches; 8.5 by 11 if zero.
	Width, Height float64
	// Layers is the rows of the page's Layer section.
	Layers string
	// Shapes is the content of the page's Shapes element.
	Shapes string
}

// VisioMaster is a master of a drawing: its ID is its index in
// Visio.Masters plus one.
type VisioMaster struct {
	Name, Prompt string
	Shapes       string
}

// Visio describes a drawing to build. Page IDs are their indexes;
// page parts are visio/pages/pageN.xml, N from 1.
type Visio struct {
	ContentType string // of the main part; a drawing's if empty
	Pages       []VisioPage
	Masters     []VisioMaster
	Styles      string // the content of the StyleSheets element
	Comments    string // the content of the comments part's root, if any
}

// Package builds the drawing.
func (v Visio) Package() *Package {
	p := NewPackage()
	ct := v.ContentType
	if ct == "" {
		ct = CTVisioDrawing
	}
	p.Rel("", RelVisioDocument, "visio/document.xml")
	doc := `<VisioDocument xmlns="` + NSVisio + `">`
	if v.Styles != "" {
		doc += `<StyleSheets>` + v.Styles + `</StyleSheets>`
	}
	p.Add("visio/document.xml", ct, doc+`</VisioDocument>`)

	var pages strings.Builder
	pages.WriteString(`<Pages xmlns="` + NSVisio + `" xmlns:r="` + NSRel + `">`)
	p.Rel("visio/document.xml", RelVisioPages, "pages/pages.xml")
	for i, pg := range v.Pages {
		w, h := pg.Width, pg.Height
		if w == 0 {
			w, h = 8.5, 11
		}
		id := p.Rel("visio/pages/pages.xml", RelVisioPage, fmt.Sprintf("page%d.xml", i+1))
		attrs := fmt.Sprintf(` ID="%d" Name="%s"`, i, EscapeXML(pg.Name))
		if pg.Background {
			attrs += ` Background="1"`
		}
		if pg.Back != "" {
			attrs += ` BackPage="` + pg.Back + `"`
		}
		fmt.Fprintf(&pages, `<Page%s><PageSheet><Cell N="PageWidth" V="%g"/><Cell N="PageHeight" V="%g"/>`, attrs, w, h)
		if pg.Layers != "" {
			pages.WriteString(`<Section N="Layer">` + pg.Layers + `</Section>`)
		}
		fmt.Fprintf(&pages, `</PageSheet><Rel r:id="%s"/></Page>`, id)
		p.Add(fmt.Sprintf("visio/pages/page%d.xml", i+1), CTVisioPage,
			`<PageContents xmlns="`+NSVisio+`" xmlns:r="`+NSRel+`"><Shapes>`+pg.Shapes+`</Shapes></PageContents>`)
	}
	pages.WriteString(`</Pages>`)
	p.Add("visio/pages/pages.xml", CTVisioPages, pages.String())

	if len(v.Masters) > 0 {
		p.Rel("visio/document.xml", RelVisioMasters, "masters/masters.xml")
		var masters strings.Builder
		masters.WriteString(`<Masters xmlns="` + NSVisio + `" xmlns:r="` + NSRel + `">`)
		for i, m := range v.Masters {
			id := p.Rel("visio/masters/masters.xml", RelVisioMaster, fmt.Sprintf("master%d.xml", i+1))
			fmt.Fprintf(&masters, `<Master ID="%d" Name="%s" Prompt="%s"><Rel r:id="%s"/></Master>`, i+1, EscapeXML(m.Name), EscapeXML(m.Prompt), id)
			p.Add(fmt.Sprintf("visio/masters/master%d.xml", i+1), CTVisioMaster,
				`<MasterContents xmlns="`+NSVisio+`" xmlns:r="`+NSRel+`"><Shapes>`+m.Shapes+`</Shapes></MasterContents>`)
		}
		masters.WriteString(`</Masters>`)
		p.Add("visio/masters/masters.xml", CTVisioMasters, masters.String())
	}
	if v.Comments != "" {
		p.Rel("visio/document.xml", RelVisioComments, "comments.xml")
		p.Add("visio/comments.xml", CTVisioComments, `<Comments xmlns="`+NSVisio+`">`+v.Comments+`</Comments>`)
	}
	return p
}

// VisioShape returns a shape with the given ID whose box is w by h inches
// with its lower left corner at (x, y), from the page's lower left, and
// whatever else the caller gives (cells, sections, text, shapes).
func VisioShape(id int, x, y, w, h float64, inside string) string {
	return fmt.Sprintf(`<Shape ID="%d" Type="Shape"><Cell N="PinX" V="%g"/><Cell N="PinY" V="%g"/><Cell N="Width" V="%g"/><Cell N="Height" V="%g"/><Cell N="LocPinX" V="%g"/><Cell N="LocPinY" V="%g"/>%s</Shape>`,
		id, x+w/2, y+h/2, w, h, w/2, h/2, inside)
}

// VisioText returns a Text element holding text, escaped; a line feed in
// it ends a paragraph.
func VisioText(text string) string { return `<Text>` + EscapeXML(text) + `</Text>` }
