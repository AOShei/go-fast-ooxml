package gofastooxml_test

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"

	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

const (
	relDiagramData    = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/diagramData"
	relDiagramDrawing = "http://schemas.microsoft.com/office/2007/relationships/diagramDrawing"
	ctDiagramData     = "application/vnd.openxmlformats-officedocument.drawingml.diagramData+xml"
	ctDiagramDrawing  = "application/vnd.ms-office.drawingml.diagramDrawing+xml"
	diagramURI        = "http://schemas.openxmlformats.org/drawingml/2006/diagram"
	dgmNS             = ` xmlns:dgm="http://schemas.openxmlformats.org/drawingml/2006/diagram" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:dsp="http://schemas.microsoft.com/office/drawing/2008/diagram" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
)

// smartArt is a diagram's data model: a process of two steps, the first
// with a detail under it, and a third step the layout does not draw; and
// its drawing, which names the model's presentation points.
func smartArt(drawingID string) (model, drawing string) {
	pt := func(id, typ, text string) string {
		t := ""
		if typ != "" {
			t = ` type="` + typ + `"`
		}
		body := ""
		if text != "" {
			body = `<a:p><a:r><a:t>` + text + `</a:t></a:r></a:p>`
		}
		return `<dgm:pt modelId="` + id + `"` + t + `><dgm:prSet/><dgm:spPr/><dgm:t><a:bodyPr/>` + body + `</dgm:t></dgm:pt>`
	}
	cx := func(typ, src, dest string, ord int) string {
		t := ""
		if typ != "" {
			t = ` type="` + typ + `"`
		}
		return fmt.Sprintf(`<dgm:cxn modelId="x%s%s"%s srcId="%s" destId="%s" srcOrd="%d" destOrd="0"/>`, src, dest, t, src, dest, ord)
	}
	ext := ""
	if drawingID != "" {
		ext = `<dgm:extLst><a:ext uri="http://schemas.microsoft.com/office/drawing/2008/diagram"><dsp:dataModelExt relId="` + drawingID + `" minVer="http://schemas.openxmlformats.org/drawingml/2006/diagram"/></a:ext></dgm:extLst>`
	}
	model = `<dgm:dataModel` + dgmNS + `><dgm:ptLst>` + pt("d", "doc", "") + pt("s2", "", "Ship") + pt("s1", "", "Plan") + pt("s1a", "", "Agree scope") + pt("s3", "", "Kept out") +
		pt("p1", "pres", "") + pt("p2", "pres", "") + pt("p1a", "pres", "") +
		`</dgm:ptLst><dgm:cxnLst>` + cx("", "d", "s1", 0) + cx("", "d", "s2", 1) + cx("", "s1", "s1a", 0) + cx("", "d", "s3", 2) +
		cx("presOf", "s1", "p1", 0) + cx("presOf", "s2", "p2", 0) + cx("presOf", "s1a", "p1a", 0) + `</dgm:cxnLst>` + ext + `</dgm:dataModel>`
	shape := func(id string, x, y int, text string) string {
		return fmt.Sprintf(`<dsp:sp modelId="%s"><dsp:nvSpPr><dsp:cNvPr id="0" name=""/><dsp:cNvSpPr/></dsp:nvSpPr><dsp:spPr><a:xfrm><a:off x="%d" y="%d"/><a:ext cx="1270000" cy="635000"/></a:xfrm><a:prstGeom prst="rect"/><a:solidFill><a:srgbClr val="4472C4"/></a:solidFill></dsp:spPr>`+
			`<dsp:txBody><a:bodyPr/><a:p><a:r><a:rPr sz="1800"><a:solidFill><a:srgbClr val="FFFFFF"/></a:solidFill></a:rPr><a:t>%s</a:t></a:r></a:p></dsp:txBody></dsp:sp>`, id, x, y, text)
	}
	drawing = `<dsp:drawing` + dgmNS + `><dsp:spTree><dsp:nvGrpSpPr><dsp:cNvPr id="0" name=""/><dsp:cNvGrpSpPr/></dsp:nvGrpSpPr><dsp:grpSpPr/>` +
		shape("p1", 0, 0, "Plan") + shape("p1a", 0, 762000, "Agree scope") + shape("p2", 1524000, 0, "Ship") + `</dsp:spTree></dsp:drawing>`
	return model, drawing
}

func TestPowerPointSmartArt(t *testing.T) {
	pkg := deck(ooxmltest.Slide{Shapes: frame(3, 100, 50, 300, 200, diagramURI, `<dgm:relIds`+dgmNS+` r:dm="rId2" r:lo="rId9" r:qs="rId9" r:cs="rId9"/>`)})
	model, drawing := smartArt("rId3")
	pkg.Rel("ppt/slides/slide1.xml", relDiagramData, "../diagrams/data1.xml")
	pkg.Rel("ppt/slides/slide1.xml", relDiagramDrawing, "../diagrams/drawing1.xml")
	pkg.Add("ppt/diagrams/data1.xml", ctDiagramData, model)
	pkg.Add("ppt/diagrams/drawing1.xml", ctDiagramDrawing, drawing)
	doc := powerpoint(t, pkg, nil)
	var got []string
	for _, b := range doc.Parts[0].Blocks {
		got = append(got, fmt.Sprintf("%s %d %q %s %v", b.Kind, b.Level, b.Text, b.Display, b.Loc.BBox))
	}
	want := []string{
		`list_item 1 "Plan"  {100 50 200 100}`,
		`list_item 2 "Agree scope"  {100 110 200 160}`,
		`list_item 1 "Ship"  {220 50 320 100}`,
		`list_item 1 "Kept out"  {100 50 400 250}`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("blocks:\n %s\nwant:\n %s", strings.Join(got, "\n "), strings.Join(want, "\n "))
	}
	if warned(doc, "input.unread_content") {
		t.Errorf("warnings: %+v", doc.Warnings)
	}
}

// In Word and Excel the same: the nodes in the model's order, after the
// paragraph or at the cell of the drawing; without a drawing nothing is
// judged.
func TestWordAndExcelSmartArt(t *testing.T) {
	model, _ := smartArt("")
	drawing := `<w:drawing><wp:inline><wp:extent cx="5486400" cy="3200400"/><wp:docPr id="1" name="Diagram 1"/>` +
		`<a:graphic><a:graphicData uri="` + diagramURI + `"><dgm:relIds` + dgmNS + ` r:dm="rId1" r:lo="rId9" r:qs="rId9" r:cs="rId9"/></a:graphicData></a:graphic></wp:inline></w:drawing>`
	pkg := ooxmltest.Docx(para(run(text("Steps:"))+run(drawing)) + para(run(text("After."))))
	pkg.Rel("word/document.xml", relDiagramData, "diagrams/data1.xml")
	pkg.Add("word/diagrams/data1.xml", ctDiagramData, model)
	expectBlocks(t, word(t, pkg, nil),
		`paragraph "Steps:"`,
		`list_item[1] "Plan"`,
		`list_item[2] "Agree scope"`,
		`list_item[1] "Ship"`,
		`list_item[1] "Kept out"`,
		`paragraph "After."`,
	)

	sheet := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: xlRow(1, xlStr("A1", "cell")), Extra: `<drawing r:id="rId1"/>`})
	sheet.Rel(firstSheet, relBase+"drawing", "../drawings/drawing1.xml")
	sheet.Add("xl/drawings/drawing1.xml", ctDrawing, drawingHeader+fmt.Sprintf(`<xdr:twoCellAnchor><xdr:from><xdr:col>%d</xdr:col><xdr:colOff>0</xdr:colOff><xdr:row>%d</xdr:row><xdr:rowOff>0</xdr:rowOff></xdr:from>`+
		`<xdr:to><xdr:col>9</xdr:col><xdr:colOff>0</xdr:colOff><xdr:row>9</xdr:row><xdr:rowOff>0</xdr:rowOff></xdr:to>%s<xdr:clientData/></xdr:twoCellAnchor>`, 4, 2,
		`<xdr:graphicFrame><xdr:nvGraphicFramePr><xdr:cNvPr id="2" name="Diagram 1"/></xdr:nvGraphicFramePr><a:graphic><a:graphicData uri="`+diagramURI+`"><dgm:relIds`+dgmNS+` r:dm="rId1" r:lo="rId9" r:qs="rId9" r:cs="rId9"/></a:graphicData></a:graphic></xdr:graphicFrame>`)+`</xdr:wsDr>`)
	sheet.Rel("xl/drawings/drawing1.xml", relDiagramData, "../diagrams/data1.xml")
	sheet.Add("xl/diagrams/data1.xml", ctDiagramData, model)
	expectSheets(t, excel(t, sheet, nil),
		`sheet 1 "S"`,
		`  table A1:A1 inferred [["cell"]]`,
		`  list_item E3 "Plan"`,
		`  list_item E3 "Agree scope"`,
		`  list_item E3 "Ship"`,
		`  list_item E3 "Kept out"`,
	)
}

// The SmartArt of the corpus's presentations against LibreOffice, which
// imports a diagram from the drawing Office made of it, or lays the model
// out itself where there is none. On each slide: the items this library
// gives with no reason are text LibreOffice shows there; those in a shape
// the drawing declares hidden are not; and the diagram text LibreOffice
// shows is all among this library's items.
func TestSmartArtAsLibreOffice(t *testing.T) {
	if testing.Short() {
		t.Skip("reads the corpus")
	}
	if _, err := exec.LookPath("soffice"); err != nil {
		t.Skip("LibreOffice (soffice) is not installed")
	}
	var contents [][]byte
	var names []string
	for _, f := range ooxmltest.LoadManifest(t, "corpus.json") {
		if f.Format != "pptx" || f.Note != "" {
			continue
		}
		if data, err := os.ReadFile(f.AbsPath(t)); err == nil && bytes.Contains(data, []byte("diagrams/data")) {
			contents, names = append(contents, data), append(names, f.Name)
		}
	}
	if os.Getenv("GOFAST_SMARTART_SOAK") != "" {
		root := filepath.Join(ooxmltest.Root(t), "testdata", "corpus", "soak")
		filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || strings.ToLower(filepath.Ext(path)) != ".pptx" {
				return nil
			}
			if data, err := os.ReadFile(path); err == nil && bytes.Contains(data, []byte("diagrams/data")) && len(data) < 32<<20 {
				rel, _ := filepath.Rel(root, path)
				contents, names = append(contents, data), append(names, "soak/"+rel)
			}
			return nil
		})
	}
	if len(contents) == 0 {
		t.Skip("no presentations with SmartArt")
	}
	ooxmltest.WarmLibreOfficeSlides(t, contents)
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	var shown, shownSeen, unseen, unseenAbsent, theirs, theirsFound int
	var notes []string
	for i, data := range contents {
		doc, err := extract(t, data, nil)
		if err != nil {
			continue
		}
		lo, ok := ooxmltest.LibreOfficeSlidesOK(t, data)
		if !ok || len(lo) != pageCount(doc) {
			continue
		}
		dgm := map[string]bool{}
		for k := range diagramTexts(data) {
			dgm[norm(k)] = true
		}
		for k := range pageCount(doc) {
			if doc.Parts[k].Display != 0 {
				continue // a hidden slide: everything on it is declared hidden
			}
			loText := map[string]bool{}
			for _, p := range lo[k].All {
				loText[norm(p)] = true
			}
			ours, seenHere := map[string]bool{}, map[string]bool{}
			for _, b := range doc.Parts[k].Blocks {
				// Any text of the slide: a diagram's text may be in a
				// text box too.
				texts := []string{b.Text}
				if b.Table != nil {
					for _, row := range b.Table.Rows {
						texts = append(texts, row...)
					}
				}
				for _, x := range texts {
					ours[norm(x)] = true
					ours[strings.Join(strings.Fields(x), "")] = true
				}
				if b.Kind == gofastcore.KindListItem && b.DisplayWhole()&gofastcore.DisplayHidden == 0 {
					seenHere[norm(b.Text)] = true
				}
			}
			for _, b := range doc.Parts[k].Blocks {
				text := norm(b.Text)
				if b.Kind != gofastcore.KindListItem || !dgm[text] {
					continue
				}
				ours[text] = true
				if b.DisplayWhole()&gofastcore.DisplayHidden != 0 {
					if seenHere[text] {
						continue // the same text is drawn from another node
					}
					unseen++
					if !loText[text] {
						unseenAbsent++
					} else {
						notes = append(notes, fmt.Sprintf("%s slide %d: declared hidden, LibreOffice shows %q", names[i], k+1, text))
					}
					continue
				}
				shown++
				if loText[text] {
					shownSeen++
				} else {
					notes = append(notes, fmt.Sprintf("%s slide %d: LibreOffice does not show %q", names[i], k+1, text))
				}
			}
			for _, p := range lo[k].All {
				text := norm(p)
				if !dgm[text] {
					continue
				}
				theirs++
				// LibreOffice joins the lines of a paragraph broken by a
				// line break without a space.
				if ours[text] || ours[strings.Join(strings.Fields(p), "")] {
					theirsFound++
				} else {
					notes = append(notes, fmt.Sprintf("%s slide %d: LibreOffice shows %q, not among the items", names[i], k+1, text))
				}
			}
		}
	}
	t.Logf("%d files: of %d items with no reason, LibreOffice shows %d; of %d declared hidden, it shows %d; of %d diagram paragraphs it shows, %d are items",
		len(contents), shown, shownSeen, unseen, unseen-unseenAbsent, theirs, theirsFound)
	sort.Strings(notes)
	// LibreOffice lays out some diagrams that have no drawing of their own
	// differently, or not at all: that their items are not all shown is
	// not counted. What is: text declared hidden that it shows,
	// and diagram text it shows that is not an item.
	if unseen-unseenAbsent > 0 {
		t.Errorf("%d items declared hidden are shown by LibreOffice", unseen-unseenAbsent)
	}
	if theirs > 0 && float64(theirsFound) < 0.97*float64(theirs) {
		t.Errorf("of %d diagram paragraphs LibreOffice shows, only %d are items", theirs, theirsFound)
	}
	for k, n := range notes {
		if k == 40 {
			t.Logf("... and %d more", len(notes)-k)
			break
		}
		t.Log(n)
	}
}
