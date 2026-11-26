package gofastooxml_test

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
	gofastooxml "github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// visio reads a drawing built from v, which must give a valid document.
func visio(t *testing.T, v ooxmltest.Visio, opts *gofastcore.Options) *gofastcore.Document {
	t.Helper()
	doc, err := extract(t, v.Package().Bytes(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(); err != nil {
		t.Fatal(err)
	}
	return doc
}

// line is a block as the tests compare it: its kind, then its text.
func line(b gofastcore.Block) string {
	s := b.Kind.String() + " " + b.Text
	if b.Marker != "" {
		s = b.Kind.String() + " " + b.Marker + " " + b.Text
	}
	if b.Display != 0 {
		s += " (" + b.Display.String() + ")"
	}
	return s
}

func lines(p gofastcore.Part) []string {
	var out []string
	for _, b := range p.Blocks {
		out = append(out, line(b))
	}
	return out
}

func sameLines(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s:\n got %q\nwant %q", what, got, want)
	}
}

// Each of the six kinds of Visio package is read as vsdx, with the media
// type of its kind.
func TestVisioKinds(t *testing.T) {
	for ct, mime := range map[string]string{
		"application/vnd.ms-visio.drawing.main+xml":               "application/vnd.ms-visio.drawing",
		"application/vnd.ms-visio.drawing.macroEnabled.main+xml":  "application/vnd.ms-visio.drawing.macroEnabled.12",
		"application/vnd.ms-visio.template.main+xml":              "application/vnd.ms-visio.template",
		"application/vnd.ms-visio.template.macroEnabled.main+xml": "application/vnd.ms-visio.template.macroEnabled.12",
		"application/vnd.ms-visio.stencil.main+xml":               "application/vnd.ms-visio.stencil",
		"application/vnd.ms-visio.stencil.macroEnabled.main+xml":  "application/vnd.ms-visio.stencil.macroEnabled.12",
	} {
		doc := visio(t, ooxmltest.Visio{ContentType: ct, Pages: []ooxmltest.VisioPage{{Name: "P", Shapes: ooxmltest.VisioShape(1, 1, 1, 2, 1, ooxmltest.VisioText("hello"))}}}, nil)
		if doc.Source.Format != "vsdx" || doc.Source.MIME != mime {
			t.Errorf("%s: read as %q %q", ct, doc.Source.Format, doc.Source.MIME)
		}
		if len(doc.Parts) != 1 || doc.Parts[0].Kind != gofastcore.PartPage || doc.Parts[0].Name != "P" {
			t.Errorf("%s: parts %+v", ct, doc.Parts)
		}
	}
}

// A Visio package with no table of content types is read by its
// relationships, and says so; any other zip archive without one is still
// refused.
func TestVisioWithoutContentTypes(t *testing.T) {
	strip := func(data []byte) []byte {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for _, f := range zr.File {
			if f.Name == "[Content_Types].xml" {
				continue
			}
			w, _ := zw.Create(f.Name)
			rc, _ := f.Open()
			io.Copy(w, rc)
			rc.Close()
		}
		zw.Close()
		return buf.Bytes()
	}
	v := ooxmltest.Visio{Pages: []ooxmltest.VisioPage{{Name: "P", Shapes: ooxmltest.VisioShape(1, 1, 1, 2, 1, ooxmltest.VisioText("hello"))}}}
	doc, err := extract(t, strip(v.Package().Bytes()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Source.Format != "vsdx" || len(doc.Parts) != 1 || len(doc.Parts[0].Blocks) != 1 || doc.Parts[0].Blocks[0].Text != "hello" {
		t.Fatalf("got %+v", doc)
	}
	found := false
	for _, w := range doc.Warnings {
		found = found || (w.Code == gofastcore.WarnInputRepaired && w.Severity == gofastcore.SeverityInfo && strings.Contains(w.Message, "[Content_Types].xml"))
	}
	if !found {
		t.Errorf("no input.repaired warning: %+v", doc.Warnings)
	}
	if _, err := extract(t, strip(ooxmltest.Docx(ooxmltest.Para("x")).Bytes()), nil); err == nil {
		t.Error("a Word package with no table of content types was read")
	}
}

// A shape's text: paragraphs end at line feeds, a paragraph whose format
// has a bullet is a list item, a field gives the value the shape stores
// for it, and the formats of runs are spans.
func TestVisioText(t *testing.T) {
	text := `<Section N="Paragraph"><Row IX="0"><Cell N="Bullet" V="0"/></Row><Row IX="1"><Cell N="Bullet" V="1"/><Cell N="BulletStr" V="-"/></Row><Row IX="2"><Cell N="Bullet" V="2"/></Row></Section>` +
		`<Section N="Character"><Row IX="0"/><Row IX="1"><Cell N="Style" V="3"/></Row><Row IX="2"><Cell N="ColorTrans" V="1"/></Row></Section>` +
		`<Section N="Field"><Row IX="0"><Cell N="Value" V="42 kg" U="STR"/></Row></Section>` +
		`<Text><pp IX="0"/><cp IX="0"/>Title <cp IX="1"/>bold<cp IX="0"/>` + "\n" + `<pp IX="1"/>first item` + "\n" + `<pp IX="2"/>second item` + "\n" +
		`<pp IX="0"/>weight: <fld IX="0">stale</fld> <cp IX="2"/>unseen` + "\n" + `</Text>`
	v := ooxmltest.Visio{Pages: []ooxmltest.VisioPage{{Name: "P", Shapes: ooxmltest.VisioShape(1, 1, 1, 4, 2, text)}}}
	doc := visio(t, v, &gofastcore.Options{Spans: true})
	p := doc.Parts[0]
	sameLines(t, "blocks", lines(p), []string{
		"paragraph Title bold",
		"list_item - first item",
		"list_item • second item",
		"paragraph weight: 42 kg unseen (no_ink)",
	})
	if s := p.Blocks[0].Spans; len(s) != 1 || p.Blocks[0].Text[s[0].Start:s[0].End] != "bold" || s[0].Style != gofastcore.StyleBold|gofastcore.StyleItalic {
		t.Errorf("spans of the first paragraph: %+v", s)
	}
	last := p.Blocks[3]
	if s := last.Spans; len(s) != 1 || last.Text[s[0].Start:s[0].End] != "unseen" || s[0].Display != gofastcore.DisplayNoInk || s[0].Style != 0 {
		t.Errorf("spans of the last paragraph: %+v", s)
	}
	if b := p.Blocks[0].Loc.BBox; b != (gofastcore.Rect{X0: 72, Y0: 576, X1: 360, Y1: 720}) {
		t.Errorf("box %+v", b)
	}
	if p.Width != 612 || p.Height != 792 {
		t.Errorf("page size %v by %v", p.Width, p.Height)
	}

	// Without Options.Spans only the span that says the text is not
	// inked is kept.
	doc = visio(t, v, nil)
	if s := doc.Parts[0].Blocks[0].Spans; len(s) != 0 {
		t.Errorf("spans without Options.Spans: %+v", s)
	}
	if s := doc.Parts[0].Blocks[3].Spans; len(s) != 1 || s[0].Display != gofastcore.DisplayNoInk {
		t.Errorf("spans without Options.Spans: %+v", s)
	}
}

// A shape that is an instance of a master takes its text, cells and rows
// from the master's shape: the place, the fields' formats, and a group's
// shapes, each from the master's shape it names.
func TestVisioMasters(t *testing.T) {
	master := `<Shape ID="5" Type="Group"><Cell N="PinX" V="1"/><Cell N="PinY" V="1"/><Cell N="Width" V="2"/><Cell N="Height" V="2"/><Cell N="LocPinX" V="1"/><Cell N="LocPinY" V="1"/>` +
		`<Section N="Field"><Row IX="0"><Cell N="Value" V="from the master"/></Row></Section>` +
		`<Text>Label: <fld IX="0">x</fld></Text>` +
		`<Shapes>` + ooxmltest.VisioShape(6, 0, 1, 2, 1, ooxmltest.VisioText("top half")) + ooxmltest.VisioShape(7, 0, 0, 2, 1, `<Cell N="HideText" V="1"/>`+ooxmltest.VisioText("bottom half")) + `</Shapes></Shape>`
	page := `<Shape ID="1" Type="Group" Master="1"><Cell N="PinX" V="2"/><Cell N="PinY" V="9"/>` +
		`<Section N="Field"><Row IX="0"><Cell N="Value" V="own value"/></Row></Section>` +
		`<Shapes><Shape ID="2" MasterShape="6"/><Shape ID="3" MasterShape="7"><Text>own bottom</Text></Shape></Shapes></Shape>` +
		`<Shape ID="4" Type="Group" Master="1"><Cell N="PinX" V="6"/><Cell N="PinY" V="9"/><Shapes><Shape ID="8" MasterShape="6"/><Shape ID="9" MasterShape="7"/></Shapes></Shape>`
	doc := visio(t, ooxmltest.Visio{
		Masters: []ooxmltest.VisioMaster{{Name: "Box", Shapes: master}},
		Pages:   []ooxmltest.VisioPage{{Name: "P", Shapes: page}},
	}, nil)
	p := doc.Parts[0]
	sameLines(t, "blocks", lines(p), []string{
		"paragraph Label: own value",
		"paragraph top half",
		"paragraph own bottom (hidden)",
		"paragraph Label: from the master",
		"paragraph top half",
		"paragraph bottom half (hidden)",
	})
	// The first group's box: 2 by 2 inches centred on (2, 9), so from 72 to
	// 216 points across, and from 8 to 10 inches up the 11-inch page, 72 to
	// 216 points down; its top half is the upper half of that.
	if b := p.Blocks[0].Loc.BBox; b != (gofastcore.Rect{X0: 72, Y0: 72, X1: 216, Y1: 216}) {
		t.Errorf("group box %+v", b)
	}
	if b := p.Blocks[1].Loc.BBox; b != (gofastcore.Rect{X0: 72, Y0: 72, X1: 216, Y1: 144}) {
		t.Errorf("box of the group's upper shape %+v", b)
	}
}

// Shapes are read from the top of the page down, and from left to right
// among those that start beside each other; a line or connector stands at
// its start; a group is read as one thing, at its place.
func TestVisioReadingOrder(t *testing.T) {
	shape := ooxmltest.VisioShape
	text := ooxmltest.VisioText
	page := shape(1, 5, 1, 2, 1, text("bottom right")) +
		shape(2, 1, 1, 2, 1, text("bottom left")) +
		shape(3, 5, 8, 2, 1, text("top right")) +
		shape(4, 1, 8, 2, 1, text("top left")) +
		// A connector drawn from the bottom left up to the top: it stands
		// at its start, so it comes after the two at the top.
		`<Shape ID="5" Type="Shape"><Cell N="PinX" V="3"/><Cell N="PinY" V="5"/><Cell N="Width" V="6"/><Cell N="Height" V="0"/><Cell N="BeginX" V="1"/><Cell N="BeginY" V="4"/><Cell N="EndX" V="5"/><Cell N="EndY" V="6"/>` + text("connector") + `</Shape>` +
		// A group high on the page whose second shape is lower than the
		// connector's start: read together, at the group's place.
		`<Shape ID="6" Type="Group"><Cell N="PinX" V="4"/><Cell N="PinY" V="6"/><Cell N="Width" V="2"/><Cell N="Height" V="4"/><Cell N="LocPinX" V="1"/><Cell N="LocPinY" V="2"/><Shapes>` +
		shape(7, 0, 3, 2, 1, text("group top")) + shape(8, 0, 0, 2, 1, text("group bottom")) + `</Shapes></Shape>` +
		shape(9, 20, 20, 1, 1, text("off the page"))
	doc := visio(t, ooxmltest.Visio{Pages: []ooxmltest.VisioPage{{Name: "P", Shapes: page}}}, nil)
	sameLines(t, "order", lines(doc.Parts[0]), []string{
		"paragraph top left",
		"paragraph top right",
		"paragraph group top",
		"paragraph group bottom",
		"paragraph connector",
		"paragraph bottom left",
		"paragraph bottom right",
		"paragraph off the page",
	})
}

// What the file declares about display: hidden text, and a shape on
// layers that are all switched off. A shape also on a layer that is shown
// is shown.
func TestVisioHidden(t *testing.T) {
	shape := ooxmltest.VisioShape
	text := ooxmltest.VisioText
	layers := `<Row IX="0"><Cell N="Name" V="Shown"/><Cell N="Visible" V="1"/></Row><Row IX="1"><Cell N="Name" V="Off"/><Cell N="Visible" V="0"/></Row><Row IX="2"><Cell N="Name" V="Not printed"/><Cell N="Visible" V="1"/><Cell N="Print" V="0"/></Row>`
	page := shape(1, 1, 9, 2, 1, `<Cell N="HideText" V="1"/>`+text("hidden text")) +
		shape(2, 1, 8, 2, 1, `<Cell N="LayerMember" V="1"/>`+text("on a layer switched off")) +
		shape(3, 1, 7, 2, 1, `<Cell N="LayerMember" V="1;0"/>`+text("also on a layer shown")) +
		shape(4, 1, 6, 2, 1, `<Cell N="LayerMember" V="2"/>`+text("on a layer not printed")) +
		shape(5, 1, 5, 2, 1, text("on no layer"))
	doc := visio(t, ooxmltest.Visio{Pages: []ooxmltest.VisioPage{{Name: "P", Layers: layers, Shapes: page}}}, nil)
	sameLines(t, "display", lines(doc.Parts[0]), []string{
		"paragraph hidden text (hidden)",
		"paragraph on a layer switched off (hidden)",
		"paragraph also on a layer shown",
		"paragraph on a layer not printed",
		"paragraph on no layer",
	})
}

// A shape's link is a span over its text, with Options.Spans.
func TestVisioLinks(t *testing.T) {
	links := `<Section N="Hyperlink"><Row N="Row_1"><Cell N="Address" V="https://example.com/a"/></Row><Row N="Row_2"><Cell N="Address" V="https://example.com/b"/><Cell N="SubAddress" V="part"/><Cell N="Default" V="1"/></Row></Section>`
	doc := visio(t, ooxmltest.Visio{Pages: []ooxmltest.VisioPage{{Name: "P", Shapes: ooxmltest.VisioShape(1, 1, 1, 2, 1, links+ooxmltest.VisioText("go there"))}}}, &gofastcore.Options{Spans: true})
	b := doc.Parts[0].Blocks[0]
	if len(b.Spans) != 1 || b.Spans[0].Href != "https://example.com/b#part" || b.Spans[0].Start != 0 || b.Spans[0].End != len(b.Text) {
		t.Errorf("spans %+v", b.Spans)
	}
}

// Background pages: the one behind a page, and the one behind that, give
// their text on the page as headers and footers, by the half of the page
// they stand in. A background page that no page has behind it is a part
// of its own, after the others; one that is behind a page is not.
func TestVisioBackgroundPages(t *testing.T) {
	shape := ooxmltest.VisioShape
	text := ooxmltest.VisioText
	doc := visio(t, ooxmltest.Visio{Pages: []ooxmltest.VisioPage{
		{Name: "Front", Back: "1", Shapes: shape(1, 1, 5, 2, 1, text("body"))},
		{Name: "Back", Background: true, Back: "2", Shapes: shape(1, 1, 10, 2, 0.5, text("title")) + shape(2, 1, 0.2, 2, 0.5, text("page foot"))},
		{Name: "Further back", Background: true, Shapes: shape(1, 6, 10, 2, 0.5, text("logo"))},
		{Name: "Unused", Background: true, Back: "4", Shapes: shape(1, 1, 5, 2, 1, text("never behind a page"))},
		{Name: "Loop", Background: true, Back: "3", Shapes: shape(1, 1, 0.2, 2, 1, text("loop"))},
		{Name: "Second", Shapes: shape(1, 1, 5, 2, 1, text("second body"))},
	}}, nil)
	var names []string
	for _, p := range doc.Parts {
		names = append(names, p.Name)
	}
	if strings.Join(names, ",") != "Front,Second,Unused" {
		t.Fatalf("parts %q", names)
	}
	sameLines(t, "front", lines(doc.Parts[0]), []string{"paragraph body", "header title", "footer page foot", "header logo"})
	sameLines(t, "second", lines(doc.Parts[1]), []string{"paragraph second body"})
	// The unused page has the page behind it, which has it behind in turn:
	// the loop is read once.
	sameLines(t, "unused", lines(doc.Parts[2]), []string{"paragraph never behind a page", "footer loop"})
	if !doc.Parts[2].Has(gofastcore.WarnLimitExceeded) {
		t.Errorf("the loop of background pages is not said: %+v", doc.Parts[2].Warnings)
	}
}

// Comments are notes after the page's own text, with their author and
// date, on the page they are on.
func TestVisioComments(t *testing.T) {
	comments := `<AuthorList><AuthorEntry ID="0" Name="Ann" Initials="A"/></AuthorList><CommentList>` +
		`<CommentEntry AuthorID="0" PageID="1" ShapeID="1" Date="2026-11-26T10:00:00">Check this.</CommentEntry>` +
		`<CommentEntry AuthorID="9" PageID="0" Date="2026-11-25T09:00:00">On the first page.</CommentEntry></CommentList>`
	doc := visio(t, ooxmltest.Visio{Comments: comments, Pages: []ooxmltest.VisioPage{
		{Name: "One", Shapes: ooxmltest.VisioShape(1, 1, 1, 2, 1, ooxmltest.VisioText("one"))},
		{Name: "Two", Shapes: ooxmltest.VisioShape(1, 1, 1, 2, 1, ooxmltest.VisioText("two"))},
	}}, nil)
	sameLines(t, "one", lines(doc.Parts[0]), []string{"paragraph one", "note On the first page."})
	sameLines(t, "two", lines(doc.Parts[1]), []string{"paragraph two", "note Check this."})
	n := doc.Parts[1].Blocks[1]
	if n.Attrs[gofastcore.AttrNoteType] != gofastcore.NoteComment || n.Attrs[gofastcore.AttrAuthor] != "Ann" || n.Attrs[gofastcore.AttrDate] != "2026-11-26T10:00:00" {
		t.Errorf("attributes %v", n.Attrs)
	}
}

// A picture shape is an image block where the shape is.
func TestVisioPicture(t *testing.T) {
	v := ooxmltest.Visio{Pages: []ooxmltest.VisioPage{{Name: "P", Shapes: `<Shape ID="1" Type="Foreign"><Cell N="PinX" V="2"/><Cell N="PinY" V="2"/><Cell N="Width" V="2"/><Cell N="Height" V="2"/><ForeignData ForeignType="Bitmap"><Rel r:id="pic1"/></ForeignData></Shape>`}}}
	pkg := v.Package()
	pkg.RelID("visio/pages/page1.xml", "pic1", "http://schemas.openxmlformats.org/officeDocument/2006/relationships/image", "../media/image1.png")
	pkg.AddBytes("visio/media/image1.png", "image/png", pngBytes(t, 3, 2))
	doc, err := extract(t, pkg.Bytes(), nil)
	if err != nil {
		t.Fatal(err)
	}
	p := doc.Parts[0]
	if len(p.Blocks) != 1 || p.Blocks[0].Kind != gofastcore.KindImage || p.Blocks[0].Image.MIME != "image/png" || p.Blocks[0].Image.Width != 3 {
		t.Fatalf("blocks %+v", p.Blocks)
	}
	if b := p.Blocks[0].Loc.BBox; b != (gofastcore.Rect{X0: 72, Y0: 576, X1: 216, Y1: 720}) {
		t.Errorf("box %+v", b)
	}
	if !p.Has(gofastcore.WarnPartImageOnly) {
		t.Errorf("warnings %+v", p.Warnings)
	}
}

// A drawing with no pages, as a stencil may be, has one part that says
// so.
func TestVisioNoPages(t *testing.T) {
	doc := visio(t, ooxmltest.Visio{ContentType: "application/vnd.ms-visio.stencil.main+xml"}, nil)
	if len(doc.Parts) != 1 || !doc.Parts[0].Has(gofastcore.WarnPartNoText) {
		t.Fatalf("parts %+v", doc.Parts)
	}
}

// Groups nested past the bound are left out and said; the shapes above
// them are read.
func TestVisioDeepGroups(t *testing.T) {
	inner := ooxmltest.VisioShape(1000, 0, 0, 1, 1, ooxmltest.VisioText("deepest"))
	for i := 0; i < 70; i++ {
		inner = fmt.Sprintf(`<Shape ID="%d" Type="Group"><Cell N="PinX" V="1"/><Cell N="PinY" V="1"/><Cell N="Width" V="1"/><Cell N="Height" V="1"/><Shapes>%s</Shapes></Shape>`, i+1, inner)
	}
	doc := visio(t, ooxmltest.Visio{Pages: []ooxmltest.VisioPage{{Name: "P", Shapes: inner + ooxmltest.VisioShape(2000, 1, 9, 1, 1, ooxmltest.VisioText("shallow"))}}}, nil)
	p := doc.Parts[0]
	sameLines(t, "blocks", lines(p), []string{"paragraph shallow"})
	if !p.Has(gofastcore.WarnLimitExceeded) {
		t.Errorf("warnings %+v", p.Warnings)
	}
}

// FuzzVsdx varies the streams of a drawing: a page, a master, the list of
// pages and the comments. Whatever they hold, the drawing is read into a
// valid document or refused with a shared error, and its pages read one
// at a time are the same pages.
func FuzzVsdx(f *testing.F) {
	shape := ooxmltest.VisioShape
	text := ooxmltest.VisioText
	vs := `xmlns="` + ooxmltest.NSVisio + `" xmlns:r="` + ooxmltest.NSRel + `"`
	page := `<PageContents ` + vs + `><Shapes>` +
		shape(1, 1, 1, 4, 2, `<Cell N="HideText" V="0"/><Cell N="LayerMember" V="0;1"/><Section N="Paragraph"><Row IX="1"><Cell N="Bullet" V="1"/><Cell N="BulletStr" V="*"/></Row></Section>`+
			`<Section N="Character"><Row IX="1"><Cell N="Style" V="7"/><Cell N="ColorTrans" V="1"/></Row></Section><Section N="Field"><Row IX="0"><Cell N="Value" V="v"/></Row></Section>`+
			`<Section N="Hyperlink"><Row N="Row_1"><Cell N="Address" V="https://example.com/"/></Row></Section>`+
			`<Text><pp IX="1"/><cp IX="1"/>item `+"\n"+`<fld IX="0">f</fld></Text>`) +
		`<Shape ID="2" Type="Group" Master="1"><Cell N="PinX" V="4"/><Cell N="PinY" V="6"/><Shapes><Shape ID="3" MasterShape="6"/><Shape ID="4" MasterShape="7" Del="1"/></Shapes></Shape>` +
		`<Shape ID="5" Type="Shape"><Cell N="PinX" V="3"/><Cell N="PinY" V="5"/><Cell N="Width" V="6"/><Cell N="Height" V="0"/><Cell N="BeginX" V="1"/><Cell N="BeginY" V="4"/><Cell N="EndX" V="5"/><Cell N="EndY" V="6"/>` + text("line") + `</Shape>` +
		`<Shape ID="8" Type="Foreign"><Cell N="PinX" V="2"/><Cell N="PinY" V="2"/><Cell N="Width" V="2"/><Cell N="Height" V="2"/><ForeignData ForeignType="Bitmap"><Rel r:id="pic1"/></ForeignData></Shape>` +
		`</Shapes></PageContents>`
	master := `<MasterContents ` + vs + `><Shapes><Shape ID="5" Type="Group" TextStyle="1"><Cell N="PinX" V="1"/><Cell N="PinY" V="1"/><Cell N="Width" V="2"/><Cell N="Height" V="2"/><Cell N="Angle" V="0.5"/><Cell N="FlipX" V="1"/>` +
		`<Section N="Field"><Row IX="0"><Cell N="Value" V="m"/></Row></Section><Section N="Property"><Row N="Cost"><Cell N="Label" V="Cost"/><Cell N="Value" V="3"/></Row></Section><Text>Label: <fld IX="0">x</fld></Text>` +
		`<Shapes>` + shape(6, 0, 1, 2, 1, text("top")) + shape(7, 0, 0, 2, 1, `<Cell N="HideText" V="1"/>`+text("bottom")) + `</Shapes></Shape></Shapes></MasterContents>`
	pages := `<Pages ` + vs + `><Page ID="0" Name="Front" BackPage="1"><PageSheet><Cell N="PageWidth" V="8.5"/><Cell N="PageHeight" V="11"/><Section N="Layer"><Row IX="0"><Cell N="Visible" V="0"/></Row><Row IX="1"><Cell N="Visible" V="0"/></Row></Section></PageSheet><Rel r:id="rId1"/></Page>` +
		`<Page ID="1" Name="Back" Background="1" BackPage="0"><PageSheet><Cell N="PageWidth" V="8.5"/><Cell N="PageHeight" V="11"/></PageSheet><Rel r:id="rId2"/></Page>` +
		`<Page ID="2" Name="Unused" Background="1"><Rel r:id="rId2"/></Page></Pages>`
	comments := `<Comments ` + vs + `><AuthorList><AuthorEntry ID="0" Name="Ann"/></AuthorList><CommentList><CommentEntry AuthorID="0" PageID="0" Date="2026">Check.</CommentEntry></CommentList></Comments>`
	f.Add([]byte(page), []byte(master), []byte(pages), []byte(comments))
	f.Add([]byte(page), []byte(master), []byte(pages), []byte(`<VisioDocument `+vs+`><StyleSheets><StyleSheet ID="1" TextStyle="1"><Section N="Paragraph"><Row IX="0"><Cell N="Bullet" V="2"/></Row></Section></StyleSheet></StyleSheets></VisioDocument>`))
	opts := &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: 4 << 20}, Images: gofastcore.ImagesMeta, Workers: 1, Spans: true}
	e := &gofastooxml.Extractor{MaxCells: 100_000}
	picture := pngBytes(f, 2, 2)
	f.Fuzz(func(t *testing.T, page, master, pages, extra []byte) {
		v := ooxmltest.Visio{
			Masters: []ooxmltest.VisioMaster{{Name: "M"}},
			Pages:   []ooxmltest.VisioPage{{Name: "A"}, {Name: "B", Background: true}},
		}
		pkg := v.Package()
		pkg.AddBytes("visio/pages/page1.xml", ooxmltest.CTVisioPage, page)
		pkg.AddBytes("visio/masters/master1.xml", ooxmltest.CTVisioMaster, master)
		pkg.AddBytes("visio/pages/pages.xml", ooxmltest.CTVisioPages, pages)
		pkg.RelID("visio/pages/page1.xml", "pic1", "http://schemas.openxmlformats.org/officeDocument/2006/relationships/image", "../media/image1.png")
		pkg.AddBytes("visio/media/image1.png", "image/png", picture)
		pkg.Rel("visio/document.xml", ooxmltest.RelVisioComments, "comments.xml")
		pkg.AddBytes("visio/comments.xml", ooxmltest.CTVisioComments, extra)
		if bytes.Contains(extra, []byte("<VisioDocument")) {
			pkg.AddBytes("visio/document.xml", ooxmltest.CTVisioDrawing, extra)
		}
		data := pkg.Bytes()
		doc, err := e.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), opts)
		checkOutcome(t, "fuzz input", doc, err)
		file, err := e.OpenBytes(data, opts)
		if err != nil || doc == nil {
			return
		}
		n := 0
		for part, err := range file.Parts(context.Background()) {
			if err != nil {
				t.Fatalf("parts: %v", err)
			}
			if n >= len(doc.Parts) || part.Number != doc.Parts[n].Number || len(part.Blocks) != len(doc.Parts[n].Blocks) {
				t.Fatalf("part %d read by itself differs from the document's", n+1)
			}
			n++
		}
	})
}

// TestVisioCorpus reads the Visio files of the shared corpus
// (GOFAST_CORPUS, else ../.testdata/corpus), dev and held-out, each within
// 10 s, and requires a valid document or a shared error. It logs a tally
// per set; held-out files are counted, never shown.
func TestVisioCorpus(t *testing.T) {
	if testing.Short() {
		t.Skip("reads the shared corpus")
	}
	root := os.Getenv("GOFAST_CORPUS")
	if root == "" {
		root = filepath.Join(ooxmltest.Root(t), "..", ".testdata", "corpus")
	}
	read := 0
	for _, set := range []string{"dev", "heldout"} {
		dir := filepath.Join(root, set, "vsdx")
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		tally := map[string]int{}
		for _, e := range entries {
			if e.IsDir() || !strings.HasPrefix(strings.ToLower(filepath.Ext(e.Name())), ".vs") {
				continue
			}
			name := e.Name()
			if set == "heldout" {
				name = fmt.Sprintf("held-out file %d", read)
			}
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			read++
			var doc *gofastcore.Document
			within(t, 10*time.Second, name, func() { doc, err = extract(t, data, nil) })
			checkOutcome(t, name, doc, err)
			switch {
			case err != nil:
				tally["refused"]++
			default:
				tally["read"]++
				blocks := 0
				for _, p := range doc.Parts {
					blocks += len(p.Blocks)
					for _, w := range p.Warnings {
						tally[w.Code]++
					}
				}
				tally["blocks"] += blocks
			}
		}
		t.Logf("%s: %v", set, tally)
	}
	if read == 0 {
		t.Skip("no Visio files in the shared corpus")
	}
}
