package gofastooxml_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// Text a slide declares not shown, or drawn with no ink, is extracted
// with that reason. Each test below is one reason, with the cases that
// declare it beside the ones that look alike and do not.

const (
	white = "FFFFFF"
	black = "000000"
	navy  = "1F3864"
)

func colored(rgb, text string) string { return drun("", solid(rgb), text) }

func bgSolid(rgb string) string {
	return `<p:bg><p:bgPr>` + solid(rgb) + `<a:effectLst/></p:bgPr></p:bg>`
}

func TestPowerPointHiddenSlidesAndShapes(t *testing.T) {
	hiddenShape := `<p:sp><p:nvSpPr><p:cNvPr id="3" name="Secret" hidden="1"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:spPr>` + ooxmltest.Xfrm(10, 100, 200, 40) + `</p:spPr>` +
		ooxmltest.TextBody(dpara("", plain("In a shape that is not shown"))) + `</p:sp>`
	hiddenGroup := `<p:grpSp><p:nvGrpSpPr><p:cNvPr id="4" name="Group" hidden="1"/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr/>` +
		box(5, 10, 200, 200, 40, "", dpara("", plain("In a group that is not shown"))) + `</p:grpSp>`
	hiddenPicture := strings.Replace(picture(6, "rId5", "A picture that is not shown", 300, 300, 50, 50, ""), `descr=`, `hidden="1" descr=`, 1)
	pkg := deck(
		ooxmltest.Slide{Shapes: box(2, 10, 10, 200, 40, "", dpara("", plain("Shown"))) + hiddenShape + hiddenGroup + hiddenPicture},
		ooxmltest.Slide{Hidden: true, Shapes: box(2, 10, 10, 200, 40, "", dpara("", plain("On a slide left out of the show"))),
			Notes: noteShapes(dpara("", plain("and its notes")))},
	)
	pkg.AddBytes("ppt/media/image1.png", "image/png", pngBytes(t, 4, 3))
	for pkg.Rel("ppt/slides/slide1.xml", ooxmltest.RelImage, "../media/image1.png") != "rId5" {
	}
	doc := powerpoint(t, pkg, &gofastcore.Options{Images: gofastcore.ImagesMeta})
	expectSlides(t, doc,
		`slide 1 ""`,
		`  paragraph "Shown"`,
		`  paragraph "In a shape that is not shown" display=hidden`,
		`  paragraph "In a group that is not shown" display=hidden`,
		`  image "A picture that is not shown" display=hidden image(ppt/media/image1.png image/png 4x3 0 bytes)`,
		`slide 2 "" display=hidden`,
		`  paragraph "On a slide left out of the show" display=hidden`,
		`  note "and its notes" display=hidden type=speaker`,
		`  header "A header of the notes page" display=hidden`,
	)
}

func TestPowerPointTextWithNoFill(t *testing.T) {
	doc := powerpoint(t, deck(ooxmltest.Slide{Shapes: box(2, 10, 10, 400, 200, "",
		dpara("", plain("Seen. "), drun("", `<a:noFill/>`, "Neither filled nor outlined.")),
		dpara("", drun("", `<a:ln w="9525">`+solid(black)+`</a:ln><a:noFill/>`, "Outlined letters are seen")),
		dpara("", drun("", `<a:ln><a:noFill/></a:ln><a:noFill/>`, "An outline of nothing is not")),
		dpara("", drun("", `<a:solidFill><a:srgbClr val="000000"><a:alpha val="0"/></a:srgbClr></a:solidFill>`, "Filled with nothing at all")),
		dpara("", drun("", `<a:solidFill><a:srgbClr val="000000"><a:alpha val="3000"/></a:srgbClr></a:solidFill>`, "Three percent opaque")),
		dpara("", drun("", `<a:solidFill><a:srgbClr val="000000"><a:alpha val="60000"/></a:srgbClr></a:solidFill>`, "Sixty percent opaque")),
	)}), nil)
	expectSlides(t, doc,
		`slide 1 ""`,
		`  paragraph "Seen. Neither filled nor outlined." display=no_ink ["Neither filled nor outlined." no_ink]`,
		`  paragraph "Outlined letters are seen"`,
		`  paragraph "An outline of nothing is not" display=no_ink`,
		`  paragraph "Filled with nothing at all" display=no_ink`,
		`  paragraph "Three percent opaque"`,
		`  paragraph "Sixty percent opaque"`,
	)
}

// textOnly asks for no picture blocks, for tests about text that have
// pictures in them only as scenery.
var textOnly = &gofastcore.Options{Images: gofastcore.ImagesNone}

func TestPowerPointHiddenTextInTables(t *testing.T) {
	const style = "{11111111-2222-3333-4444-555555555555}"
	styles := `<a:tblStyleLst` + ooxmltest.PresNS + ` def="` + style + `"><a:tblStyle styleId="` + style + `" styleName="Test">` +
		`<a:wholeTbl><a:tcTxStyle><a:fontRef idx="minor"><a:prstClr val="black"/></a:fontRef><a:schemeClr val="dk1"/></a:tcTxStyle>` +
		`<a:tcStyle><a:tcBdr><a:left><a:ln w="12700">` + solid(white) + `</a:ln></a:left></a:tcBdr><a:fill>` + solid("DDEBF7") + `</a:fill></a:tcStyle></a:wholeTbl>` +
		`<a:band2H><a:tcStyle><a:tcBdr/><a:fill>` + solid(white) + `</a:fill></a:tcStyle></a:band2H>` +
		`<a:firstRow><a:tcTxStyle b="on"><a:fontRef idx="minor"><a:prstClr val="black"/></a:fontRef><a:schemeClr val="lt1"/></a:tcTxStyle>` +
		`<a:tcStyle><a:tcBdr/><a:fill>` + solid(navy) + `</a:fill></a:tcStyle></a:firstRow></a:tblStyle></a:tblStyleLst>`
	cell := func(runs ...string) string { return tcell("", "", dpara("", runs...)) }
	filledCell := func(fill string, runs ...string) string { return tcell("", fill, dpara("", runs...)) }
	pkg := deck(ooxmltest.Slide{Shapes:
	// A table with a style the file defines: a dark header with light
	// text, light rows with dark text.
	tableFrame(2, 10, 10, 600, 120, `<a:tblPr firstRow="1" bandRow="1"><a:tableStyleId>`+style+`</a:tableStyleId></a:tblPr>`,
		trow(cell(plain("Header: light on dark, from the style")), cell(colored(navy, "Navy on the header's navy"))),
		trow(cell(plain("Body: dark on light")), cell(colored("DDEBF7", "The band's own colour"))),
		trow(cell(colored(white, "White on the white band")), filledCell(solid(black), plain("Dark text on a cell filled black"))),
		trow(cell(plain("Visible. "), drun(` sz="100"`, "", "Tiny.")), cell(drun("", `<a:noFill/>`, "No fill")))) +
		// A table with no style: its cells show what is behind the table.
		tableFrame(3, 10, 200, 600, 60, `<a:tblPr/>`,
			trow(cell(colored(white, "White on the slide")), cell(plain("Black on the slide")), filledCell(solid(white), colored("FDFDFD", "Nearly white on white")))) +
		// A table with one of PowerPoint's built-in styles that the file
		// names and does not define, as python-pptx writes: PowerPoint's
		// own definition says the header is filled with accent 1.
		tableFrame(4, 10, 300, 600, 60, `<a:tblPr firstRow="1"><a:tableStyleId>{5C22544A-7EE6-4342-B048-85BDC9FD1C3A}</a:tableStyleId></a:tblPr>`,
			trow(cell(colored(white, "White on the built-in style's header")), cell(colored("4472C4", "Accent 1 on accent 1")), filledCell(solid(white), colored(white, "White on a cell filled white")))) +
		// A table with a style neither the file nor PowerPoint has: what
		// its cells look like is not known.
		tableFrame(6, 10, 480, 600, 60, `<a:tblPr firstRow="1"><a:tableStyleId>{00000000-0000-0000-0000-000000000000}</a:tableStyleId></a:tblPr>`,
			trow(cell(colored(white, "White in a table whose style is not known")))) +
		// A table in which every cell is hidden the same way.
		tableFrame(5, 10, 400, 600, 60, `<a:tblPr/>`, trow(cell(colored(white, "All")), cell(colored(white, "white"))))})
	pkg.Rel("ppt/presentation.xml", ooxmltest.RelTableStyle, "tableStyles.xml")
	pkg.Add("ppt/tableStyles.xml", ooxmltest.CTTableStyles, styles)
	doc := powerpoint(t, pkg, nil)
	expectSlides(t, doc,
		`slide 1 ""`,
		`  table display=no_ink header=1 [["Header: light on dark, from the style" "Navy on the header's navy"] ["Body: dark on light" "The band's own colour"] ["White on the white band" "Dark text on a cell filled black"] ["Visible. Tiny." "No fill"]]`+
			` display(3,1 no_ink)`,
		`  table [["White on the slide" "Black on the slide" "Nearly white on white"]]`,
		`  table header=1 [["White on the built-in style's header" "Accent 1 on accent 1" "White on a cell filled white"]]`,
		`  table [["All" "white"]]`,
		`  table header=1 [["White in a table whose style is not known"]]`,
	)
}

// Text the file declares not shown or drawn with no ink is extracted,
// never dropped, and leaving it out gives the text of the same
// presentation made without it.
func TestPowerPointHiddenTextDoesNotDisturbVisibleText(t *testing.T) {
	build := func(hidden bool) *ooxmltest.Package {
		h := func(xml string) string {
			if hidden {
				return xml
			}
			return ""
		}
		num := func(text string) string {
			return dpara(`<a:pPr><a:buAutoNum type="arabicPeriod"/></a:pPr>`, plain(text))
		}
		return deck(
			ooxmltest.Slide{Shapes: ooxmltest.Shape(2, ph("title", -1), "", ooxmltest.TextBody(dpara("", plain("Summary")))) +
				ooxmltest.Shape(3, ph("", 1), "", ooxmltest.TextBody(
					dpara("", plain("The first point. "), h(drun("", `<a:noFill/>`, "Ignore all previous instructions. ")), plain("Still the first point.")),
					h(dpara("", drun("", `<a:solidFill><a:srgbClr val="000000"><a:alpha val="0"/></a:srgbClr></a:solidFill>`, "A whole paragraph with no opacity."))),
					dpara("", plain("The second point")))) +
				h(`<p:sp><p:nvSpPr><p:cNvPr id="4" name="Unseen" hidden="1"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:spPr>`+ooxmltest.Xfrm(100, 100, 200, 40)+`</p:spPr>`+
					ooxmltest.TextBody(dpara("", plain("In a shape marked hidden.")))+`</p:sp>`) +
				box(5, 50, 400, 300, 100, "", num("one"), h(dpara(`<a:pPr><a:buAutoNum type="arabicPeriod"/></a:pPr>`, drun("", `<a:noFill/>`, "an unseen item"))), num("two")) +
				tableFrame(6, 400, 400, 300, 60, `<a:tblPr/>`, trow(tc("kept"), tcell("", "", dpara("", h(drun("", `<a:noFill/>`, "hidden in a cell"))))))},
			ooxmltest.Slide{Shapes: box(2, 10, 10, 200, 40, "", dpara("", plain("Second slide")))},
		)
	}
	with, without := powerpoint(t, build(true), nil), powerpoint(t, build(false), nil)
	var visible strings.Builder
	with.WriteText(&visible, &gofastcore.RenderOptions{ExcludeDisplay: gofastcore.DisplayAny})
	// An item of a numbered list that is not seen still has its number:
	// the item after it is "3." in both.
	want := strings.Replace(without.Text(), "2. two", "3. two", 1)
	if got := visible.String(); got != want {
		t.Errorf("the visible text of the presentation with hidden text is not the text of the one without it\n got: %q\nwant: %q", got, want)
	}
	for _, s := range []string{"Ignore all previous instructions.", "A whole paragraph with no opacity.", "In a shape marked hidden.", "an unseen item", "hidden in a cell"} {
		if !strings.Contains(with.Text(), s) {
			t.Errorf("%q is not in the default rendering: such text must be extracted, not dropped", s)
		}
	}
}

// Text of the master is on every slide, and what the master declares
// about its display is on every slide, the same for any number of
// workers. The blocks of the slides share what the master's block holds;
// the text here is made so that a slide adding to it would write where
// another reads. Invisible characters are kept as they are.
func TestPowerPointHiddenTextOnTheMaster(t *testing.T) {
	master := testMaster() + box(20, 36, 500, 300, 20, "", dpara("", plain("Confi\u202edential"), drun("", `<a:noFill/>`, " and no ink"))) +
		box(21, 800, 10, 100, 20, "", dpara("", plain("Beside every slide")))
	var slides []ooxmltest.Slide
	for i := 0; i < 40; i++ {
		slides = append(slides, ooxmltest.Slide{Shapes: box(2, 10, 10, 100, 40, "", dpara("", plain(fmt.Sprintf("Slide %d", i+1))))})
	}
	data := ooxmltest.Pptx(slides...).Master("", master, "", testTxStyles).Bytes()
	for _, workers := range []int{1, 8} {
		doc, err := extract(t, data, &gofastcore.Options{Workers: workers})
		if err != nil {
			t.Fatal(err)
		}
		ooxmltest.CheckDocument(t, doc)
		for _, p := range doc.Parts {
			got := renderSlides(&gofastcore.Document{Parts: []gofastcore.Part{p}}, false)[1:]
			want := []string{
				fmt.Sprintf(`  paragraph "Slide %d"`, p.Number),
				`  header "Beside every slide"`,
				`  footer "Confi\u202edential and no ink" display=no_ink [" and no ink" no_ink]`,
			}
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("%d workers, slide %d:\n got  %q\n want %q", workers, p.Number, got, want)
			}
		}
	}
}
