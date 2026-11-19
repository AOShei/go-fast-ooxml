package diagram

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-ooxml/internal/dml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/theme"
)

const (
	dgmNS = ` xmlns:dgm="http://schemas.openxmlformats.org/drawingml/2006/diagram" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:dsp="http://schemas.microsoft.com/office/drawing/2008/diagram"`
)

func pt(id, typ string, paras ...string) string {
	t := ""
	if typ != "" {
		t = ` type="` + typ + `"`
	}
	var b strings.Builder
	for _, p := range paras {
		lvl := ""
		if strings.HasPrefix(p, ">") {
			lvl, p = `<a:pPr lvl="1"/>`, p[1:]
		}
		b.WriteString(`<a:p>` + lvl + `<a:r><a:t>` + p + `</a:t></a:r></a:p>`)
	}
	return `<dgm:pt modelId="` + id + `"` + t + `><dgm:prSet/><dgm:spPr/><dgm:t><a:bodyPr/>` + b.String() + `</dgm:t></dgm:pt>`
}

func cx(typ, src, dest string, ord int) string {
	t := ""
	if typ != "" {
		t = ` type="` + typ + `"`
	}
	return fmt.Sprintf(`<dgm:cxn modelId="c%s%s"%s srcId="%s" destId="%s" srcOrd="%d" destOrd="0"/>`, src, dest, t, src, dest, ord)
}

func shape(id string, x, y, w, h int, fill, ink, text string) string {
	body := ""
	if text != "" {
		body = `<dsp:txBody><a:bodyPr/><a:p><a:r><a:rPr sz="1800"><a:solidFill><a:srgbClr val="` + ink + `"/></a:solidFill></a:rPr><a:t>` + text + `</a:t></a:r></a:p></dsp:txBody>`
	}
	return fmt.Sprintf(`<dsp:sp modelId="%s"><dsp:nvSpPr><dsp:cNvPr id="0" name=""/><dsp:cNvSpPr/></dsp:nvSpPr><dsp:spPr><a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"/><a:solidFill><a:srgbClr val="%s"/></a:solidFill></dsp:spPr>%s</dsp:sp>`, id, x, y, w, h, fill, body)
}

func read(t *testing.T, model, drawing string) *Diagram {
	t.Helper()
	pkg := ooxmltest.NewPackage().Add("ppt/diagrams/data1.xml", "application/vnd.openxmlformats-officedocument.drawingml.diagramData+xml", model)
	if drawing != "" {
		pkg.Add("ppt/diagrams/drawing1.xml", "application/vnd.ms-office.drawingml.diagramDrawing+xml", drawing)
	}
	data := pkg.Bytes()
	p, err := opc.Open(bytes.NewReader(data), int64(len(data)), opc.Limits{MaxDecodedBytes: 1 << 30, MaxMembers: 100})
	if err != nil {
		t.Fatal(err)
	}
	rels := map[string]opc.Rel{"rId9": {ID: "rId9", Type: "http://schemas.microsoft.com/office/2007/relationships/diagramDrawing", Target: "ppt/diagrams/drawing1.xml"}}
	pal := &dml.Palette{Theme: &theme.Scheme{}, Map: dml.DefaultMap}
	d, err := Read(p, "ppt/diagrams/data1.xml", rels, pal)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func render(d *Diagram) string {
	var b strings.Builder
	for _, it := range d.Items {
		fmt.Fprintf(&b, "%d %q", it.Level, it.Text)
		if it.Hidden != 0 {
			fmt.Fprintf(&b, " hidden=%s", it.Hidden)
		}
		if it.Box.Set {
			fmt.Fprintf(&b, " at %g,%g", it.Box.X, it.Box.Y)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// The model's nodes in the order and nesting its connections give, not
// the order of the file; a node's second-level paragraph one deeper.
func TestModelOrder(t *testing.T) {
	model := `<dgm:dataModel` + dgmNS + `><dgm:ptLst>` +
		pt("d", "doc") + pt("b", "", "Second") + pt("a", "", "First", ">detail") + pt("a1", "", "Under first") +
		pt("t", "parTrans") + pt("e", "", "") + pt("p", "pres") +
		`</dgm:ptLst><dgm:cxnLst>` +
		cx("", "d", "b", 1) + cx("", "d", "a", 0) + cx("", "a", "a1", 0) + cx("", "d", "e", 2) + cx("presOf", "a", "p", 0) +
		`</dgm:cxnLst></dgm:dataModel>`
	want := `1 "First"
2 "detail"
2 "Under first"
1 "Second"
`
	if got := render(read(t, model, "")); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// With the drawing: each node has the box of the shape that shows it; a
// node the layout draws nowhere is flagged invisible; text in the colour
// of its shape is flagged.
func TestDrawing(t *testing.T) {
	model := `<dgm:dataModel` + dgmNS + `><dgm:ptLst>` +
		pt("d", "doc") + pt("a", "", "Shown") + pt("b", "", "Not drawn") + pt("c", "", "White on white") +
		pt("pa", "pres") + pt("pb", "pres") + pt("pc", "pres") +
		`</dgm:ptLst><dgm:cxnLst>` +
		cx("", "d", "a", 0) + cx("", "d", "b", 1) + cx("", "d", "c", 2) +
		cx("presOf", "a", "pa", 0) + cx("presOf", "b", "pb", 0) + cx("presOf", "c", "pc", 0) +
		`</dgm:cxnLst><dgm:extLst><a:ext uri="http://schemas.microsoft.com/office/drawing/2008/diagram"><dsp:dataModelExt relId="rId9" minVer="http://schemas.openxmlformats.org/drawingml/2006/diagram"/></a:ext></dgm:extLst></dgm:dataModel>`
	drawing := `<dsp:drawing` + dgmNS + `><dsp:spTree><dsp:nvGrpSpPr><dsp:cNvPr id="0" name=""/><dsp:cNvGrpSpPr/></dsp:nvGrpSpPr><dsp:grpSpPr/>` +
		shape("pa", 12700, 25400, 127000, 63500, "4472C4", "FFFFFF", "Shown") +
		shape("pb", 0, 0, 10, 10, "4472C4", "FFFFFF", "") + // a shape with no text
		shape("pc", 254000, 0, 127000, 63500, "FFFFFF", "FFFFFF", "White on white") +
		`</dsp:spTree></dsp:drawing>`
	d := read(t, model, drawing)
	want := `1 "Shown" at 1,2
1 "Not drawn" hidden=invisible
1 "White on white" hidden=low_contrast at 20,0
`
	if got := render(d); got != want || !d.Drawn {
		t.Errorf("drawn %v; got:\n%s\nwant:\n%s", d.Drawn, got, want)
	}
}

// A model whose connections go round in a circle ends.
func TestCycle(t *testing.T) {
	model := `<dgm:dataModel` + dgmNS + `><dgm:ptLst>` + pt("d", "doc") + pt("a", "", "A") + pt("b", "", "B") +
		`</dgm:ptLst><dgm:cxnLst>` + cx("", "d", "a", 0) + cx("", "a", "b", 0) + cx("", "b", "a", 0) + `</dgm:cxnLst></dgm:dataModel>`
	if got := render(read(t, model, "")); got != "1 \"A\"\n2 \"B\"\n" {
		t.Errorf("got:\n%s", got)
	}
}

// An empty drawing, as some files have, says nothing of what is seen.
func TestEmptyDrawing(t *testing.T) {
	model := `<dgm:dataModel` + dgmNS + `><dgm:ptLst>` + pt("d", "doc") + pt("a", "", "A") + pt("pa", "pres") +
		`</dgm:ptLst><dgm:cxnLst>` + cx("", "d", "a", 0) + cx("presOf", "a", "pa", 0) + `</dgm:cxnLst>` +
		`<dgm:extLst><a:ext uri="x"><dsp:dataModelExt relId="rId9"/></a:ext></dgm:extLst></dgm:dataModel>`
	d := read(t, model, `<dsp:drawing`+dgmNS+`><dsp:spTree><dsp:nvGrpSpPr><dsp:cNvPr id="0" name=""/><dsp:cNvGrpSpPr/></dsp:nvGrpSpPr><dsp:grpSpPr/></dsp:spTree></dsp:drawing>`)
	if got := render(d); got != "1 \"A\"\n" || d.Drawn {
		t.Errorf("drawn %v, got:\n%s", d.Drawn, got)
	}
}

// Text on a connector between two steps comes between them. A shape long
// enough for the scanner's window to move on while it is read is still
// found by its id.
func TestConnectorTextAndLongShapes(t *testing.T) {
	model := `<dgm:dataModel` + dgmNS + `><dgm:ptLst>` + pt("d", "doc") + pt("a", "", "A") + pt("b", "", "B") +
		pt("sa", "sibTrans", "then") + pt("pa", "pres") + pt("pb", "pres") +
		`</dgm:ptLst><dgm:cxnLst>` +
		`<dgm:cxn modelId="x1" srcId="d" destId="a" srcOrd="0" destOrd="0" sibTransId="sa"/>` + cx("", "d", "b", 1) +
		cx("presOf", "a", "pa", 0) + cx("presOf", "b", "pb", 0) +
		`</dgm:cxnLst><dgm:extLst><a:ext uri="x"><dsp:dataModelExt relId="rId9"/></a:ext></dgm:extLst></dgm:dataModel>`
	long := strings.Repeat("A long text. ", 40000)
	drawing := `<dsp:drawing` + dgmNS + `><dsp:spTree>` + shape("pa", 0, 0, 127000, 63500, "4472C4", "FFFFFF", long) +
		shape("pb", 254000, 0, 127000, 63500, "4472C4", "FFFFFF", "B") + `</dsp:spTree></dsp:drawing>`
	d := read(t, model, drawing)
	if len(d.Items) != 3 || d.Items[0].Hidden != 0 || !d.Items[0].Box.Set || d.Items[1].Text != "then" || d.Items[2].Text != "B" || d.Items[2].Hidden != 0 {
		t.Errorf("items: %s", render(d))
	}
}

// Any data model and drawing give items with a level of at least one,
// without failing.
func FuzzDiagram(f *testing.F) {
	f.Add(`<dgm:dataModel`+dgmNS+`><dgm:ptLst>`+pt("d", "doc")+pt("a", "", "A", ">b")+pt("pa", "pres")+`</dgm:ptLst><dgm:cxnLst>`+cx("", "d", "a", 0)+cx("presOf", "a", "pa", 0)+
		`</dgm:cxnLst><dgm:extLst><a:ext uri="x"><dsp:dataModelExt relId="rId9"/></a:ext></dgm:extLst></dgm:dataModel>`,
		`<dsp:drawing`+dgmNS+`><dsp:spTree>`+shape("pa", 0, 0, 10, 10, "FFFFFF", "000000", "A")+`</dsp:spTree></dsp:drawing>`)
	f.Fuzz(func(t *testing.T, model, drawing string) {
		pkg := ooxmltest.NewPackage().Add("ppt/diagrams/data1.xml", "application/xml", model).Add("ppt/diagrams/drawing1.xml", "application/xml", drawing)
		data := pkg.Bytes()
		p, err := opc.Open(bytes.NewReader(data), int64(len(data)), opc.Limits{MaxDecodedBytes: 1 << 24, MaxMembers: 10})
		if err != nil {
			return
		}
		rels := map[string]opc.Rel{"rId9": {ID: "rId9", Type: "http://schemas.microsoft.com/office/2007/relationships/diagramDrawing", Target: "ppt/diagrams/drawing1.xml"}}
		d, _ := Read(p, "ppt/diagrams/data1.xml", rels, &dml.Palette{Map: dml.DefaultMap})
		if d == nil {
			return
		}
		for _, it := range d.Items {
			if it.Level < 1 || it.Text == "" {
				t.Fatalf("item %+v", it)
			}
		}
	})
}
