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
	CTVisioStencil   = "application/vnd.ms-visio.stencil.main+xml"
	CTVisioSets      = "application/vnd.ms-visio.recordsets+xml"
	CTVisioSet       = "application/vnd.ms-visio.recordset+xml"
	RelVisioDocument = "http://schemas.microsoft.com/visio/2010/relationships/document"
	RelVisioPages    = "http://schemas.microsoft.com/visio/2010/relationships/pages"
	RelVisioPage     = "http://schemas.microsoft.com/visio/2010/relationships/page"
	RelVisioMasters  = "http://schemas.microsoft.com/visio/2010/relationships/masters"
	RelVisioMaster   = "http://schemas.microsoft.com/visio/2010/relationships/master"
	RelVisioComments = "http://schemas.microsoft.com/visio/2010/relationships/comments"
	RelVisioSets     = "http://schemas.microsoft.com/visio/2010/relationships/recordsets"
	RelVisioSet      = "http://schemas.microsoft.com/visio/2010/relationships/recordset"
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
	Hidden       bool
	// Width and Height are the size of its page in inches; none if zero.
	Width, Height float64
	Shapes        string
}

// VisioRecordset is a data recordset of a drawing: its name, and the
// content of the part that holds its rows (see ADORowset).
type VisioRecordset struct {
	Name, Data string
}

// Visio describes a drawing to build. Page IDs are their indexes;
// page parts are visio/pages/pageN.xml, N from 1.
type Visio struct {
	ContentType string // of the main part; a drawing's if empty
	Pages       []VisioPage
	Masters     []VisioMaster
	Styles      string // the content of the StyleSheets element
	Comments    string // the content of the comments part's root, if any
	Recordsets  []VisioRecordset
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
			hidden, sheet := "", ""
			if m.Hidden {
				hidden = ` Hidden="1"`
			}
			if m.Width > 0 {
				sheet = fmt.Sprintf(`<PageSheet><Cell N="PageWidth" V="%g"/><Cell N="PageHeight" V="%g"/></PageSheet>`, m.Width, m.Height)
			}
			fmt.Fprintf(&masters, `<Master ID="%d" Name="%s" Prompt="%s"%s>%s<Rel r:id="%s"/></Master>`, i+1, EscapeXML(m.Name), EscapeXML(m.Prompt), hidden, sheet, id)
			p.Add(fmt.Sprintf("visio/masters/master%d.xml", i+1), CTVisioMaster,
				`<MasterContents xmlns="`+NSVisio+`" xmlns:r="`+NSRel+`"><Shapes>`+m.Shapes+`</Shapes></MasterContents>`)
		}
		masters.WriteString(`</Masters>`)
		p.Add("visio/masters/masters.xml", CTVisioMasters, masters.String())
	}
	if len(v.Recordsets) > 0 {
		p.Rel("visio/document.xml", RelVisioSets, "data/recordsets.xml")
		var sets strings.Builder
		sets.WriteString(`<DataRecordSets xmlns="` + NSVisio + `" xmlns:r="` + NSRel + `">`)
		for i, rs := range v.Recordsets {
			id := p.Rel("visio/data/recordsets.xml", RelVisioSet, fmt.Sprintf("recordset%d.xml", i+1))
			fmt.Fprintf(&sets, `<DataRecordSet ID="%d" ConnectionID="0" Name="%s"><Rel r:id="%s"/><DataColumns/></DataRecordSet>`, i, EscapeXML(rs.Name), id)
			p.Add(fmt.Sprintf("visio/data/recordset%d.xml", i+1), CTVisioSet, rs.Data)
		}
		sets.WriteString(`</DataRecordSets>`)
		p.Add("visio/data/recordsets.xml", CTVisioSets, sets.String())
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

// ADORowset returns an ADO rowset saved as XML, as Visio keeps a data
// recordset: columns names the columns, each "attribute=name", or just a
// name when it is the attribute; rows holds each row's attributes, as
// written in the z:row tag.
func ADORowset(columns []string, rows ...string) string {
	var b strings.Builder
	b.WriteString(`<xml xmlns:s="uuid:BDC6E3F0-6DA3-11d1-A2A3-00AA00C14882" xmlns:dt="uuid:C2F41010-65B3-11d1-A29F-00AA00C14882" xmlns:rs="urn:schemas-microsoft-com:rowset" xmlns:z="#RowsetSchema">`)
	b.WriteString(`<s:Schema id="RowsetSchema"><s:ElementType name="row" content="eltOnly">`)
	for i, c := range columns {
		attr, name, ok := strings.Cut(c, "=")
		if ok {
			fmt.Fprintf(&b, `<s:AttributeType name="%s" rs:name="%s" rs:number="%d"><s:datatype dt:type="string"/></s:AttributeType>`, attr, EscapeXML(name), i+1)
		} else {
			fmt.Fprintf(&b, `<s:AttributeType name="%s" rs:number="%d"><s:datatype dt:type="string"/></s:AttributeType>`, attr, i+1)
		}
	}
	b.WriteString(`<s:extends type="rs:rowbase"/></s:ElementType></s:Schema><rs:data>`)
	for _, r := range rows {
		b.WriteString(`<z:row ` + r + `/>`)
	}
	b.WriteString(`</rs:data></xml>`)
	return b.String()
}
