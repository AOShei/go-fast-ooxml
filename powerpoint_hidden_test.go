package gofastooxml_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// Text a reader of a slide cannot see is extracted and flagged with the
// reason. Each test below is one reason, with the cases that must be
// flagged beside the ones that look alike and must not be.

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
		`  paragraph "In a shape that is not shown" hidden=invisible`,
		`  paragraph "In a group that is not shown" hidden=invisible`,
		`  image "A picture that is not shown" hidden=invisible image(ppt/media/image1.png image/png 4x3 0 bytes)`,
		`slide 2 "" hidden=invisible`,
		`  paragraph "On a slide left out of the show" hidden=invisible`,
		`  note "and its notes" hidden=invisible type=speaker`,
		`  header "A header of the notes page" hidden=invisible`,
	)
	if !slideWarned(doc, 0, gofastcore.WarnTextHidden) || !slideWarned(doc, 1, gofastcore.WarnTextHidden) {
		t.Errorf("text.hidden is missing: %v %v", doc.Parts[0].Warnings, doc.Parts[1].Warnings)
	}
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
		`  paragraph "Seen. Neither filled nor outlined." hidden=invisible ["Neither filled nor outlined." invisible]`,
		`  paragraph "Outlined letters are seen"`,
		`  paragraph "An outline of nothing is not" hidden=invisible`,
		`  paragraph "Filled with nothing at all" hidden=transparent`,
		`  paragraph "Three percent opaque" hidden=low_contrast`,
		`  paragraph "Sixty percent opaque"`,
	)
}

func TestPowerPointTinyText(t *testing.T) {
	layout := testLayout() + ooxmltest.Shape(9, ph("body", 20), ooxmltest.Xfrm(10, 400, 300, 40),
		`<p:txBody><a:bodyPr/><a:lstStyle><a:lvl1pPr><a:buNone/><a:defRPr sz="100"/></a:lvl1pPr></a:lstStyle>`+dpara("", plain("Small print"))+`</p:txBody>`)
	autofit := func(scale, size, text string) string {
		return ooxmltest.Shape(7, "", ooxmltest.Xfrm(10, 300, 300, 20),
			`<p:txBody><a:bodyPr><a:normAutofit fontScale="`+scale+`"/></a:bodyPr>`+dpara("", drun(size, "", text))+`</p:txBody>`)
	}
	pkg := ooxmltest.Pptx(ooxmltest.Slide{Shapes: box(2, 10, 10, 400, 200, "",
		dpara("", plain("Eighteen points by default. "), drun(` sz="100"`, "", "One point."), drun(` sz="199"`, "", " Just under two."), drun(` sz="200"`, "", " Two points.")),
	) + autofit("10000", ` sz="1800"`, "Shrunk to a tenth of eighteen points") + autofit("25000", ` sz="1800"`, "Shrunk to a quarter") +
		ooxmltest.Shape(8, ph("body", 20), "", ooxmltest.TextBody(dpara("", plain("One point, from the layout")), dpara("", drun(` sz="1200"`, "", "Twelve, said here"))))},
	).Master("", testMaster(), "", testTxStyles).Layout("", layout, "")
	doc := powerpoint(t, pkg, nil)
	expectSlides(t, doc,
		`slide 1 ""`,
		`  paragraph "Eighteen points by default. One point. Just under two. Two points." hidden=tiny ["One point. Just under two." tiny]`,
		`  paragraph "Shrunk to a tenth of eighteen points" hidden=tiny`,
		`  paragraph "Shrunk to a quarter"`,
		`  paragraph "One point, from the layout" hidden=tiny`,
		`  paragraph "Twelve, said here"`,
	)
}

func TestPowerPointLowContrast(t *testing.T) {
	doc := powerpoint(t, deck(
		ooxmltest.Slide{Shapes: box(2, 10, 10, 300, 30, "", dpara("", plain("Black on the white slide. "), colored(white, "White on it."), colored("FEFEFE", " Nearly white."))) +
			box(3, 10, 50, 300, 30, solid(navy), dpara("", colored(white, "White on a dark box. "), colored(navy, "The box's own colour."), plain(" Black on navy is hard to read but is not hidden."))) +
			box(4, 10, 90, 300, 30, `<a:noFill/>`, dpara("", colored(white, "White in a box with no fill"))) +
			box(5, 10, 130, 300, 30, `<a:gradFill><a:gsLst><a:gs pos="0"><a:srgbClr val="FFFFFF"/></a:gs><a:gs pos="100000"><a:srgbClr val="000000"/></a:gs></a:gsLst></a:gradFill>`,
				dpara("", colored(white, "White on a gradient is not judged"))) +
			box(6, 10, 170, 300, 30, "", dpara("", drun("", `<a:highlight><a:srgbClr val="FFFFFF"/></a:highlight>`+solid(white), "White on a white highlight"),
				drun("", `<a:highlight><a:srgbClr val="000000"/></a:highlight>`+solid(white), " and on a black one"))) +
			box(7, 10, 210, 300, 30, `<a:solidFill><a:srgbClr val="FFFFFF"><a:alpha val="50000"/></a:srgbClr></a:solidFill>`, dpara("", colored(white, "White on a half-transparent box is not judged"))) +
			// Theme colours, and a colour that is adjusted.
			box(8, 10, 250, 300, 30, scheme("accent1"), dpara("", drun("", scheme("accent1"), "Accent on accent"), drun("", scheme("bg1"), " and background on accent"),
				drun("", `<a:solidFill><a:schemeClr val="accent1"><a:lumMod val="98000"/></a:schemeClr></a:solidFill>`, " and accent a little darker")))},
		// A dark slide: white is seen and black is not.
		ooxmltest.Slide{Background: bgSolid(black), Shapes: box(2, 10, 10, 300, 30, "", dpara("", colored(white, "White on the dark slide. "), colored("050505", "Nearly black on it."))) +
			box(3, 10, 50, 300, 30, "", dpara("", plain("The text colour nothing changes")))},
	), nil)
	expectSlides(t, doc,
		`slide 1 ""`,
		`  paragraph "Black on the white slide. White on it. Nearly white." hidden=low_contrast ["White on it. Nearly white." low_contrast]`,
		`  paragraph "White on a dark box. The box's own colour. Black on navy is hard to read but is not hidden." hidden=low_contrast ["The box's own colour." low_contrast]`,
		`  paragraph "White in a box with no fill" hidden=low_contrast`,
		`  paragraph "White on a gradient is not judged"`,
		`  paragraph "White on a white highlight and on a black one" hidden=low_contrast ["White on a white highlight" low_contrast]`,
		`  paragraph "White on a half-transparent box is not judged"`,
		`  paragraph "Accent on accent and background on accent and accent a little darker" hidden=low_contrast ["Accent on accent" low_contrast] [" and accent a little darker" low_contrast]`,
		`slide 2 ""`,
		`  paragraph "White on the dark slide. Nearly black on it." hidden=low_contrast ["Nearly black on it." low_contrast]`,
		`  paragraph "The text colour nothing changes" hidden=low_contrast`,
	)
}

// textOnly asks for no picture blocks, for tests about text that have
// pictures in them only as what lies behind or over it.
var textOnly = &gofastcore.Options{Images: gofastcore.ImagesNone}

// What text is seen against is the last thing drawn under it, on the
// slide, the layout or the master.
func TestPowerPointWhatIsBehindText(t *testing.T) {
	pkg := deck(
		ooxmltest.Slide{Shapes: filled(2, 0, 0, 720, 100, "rect", solid(navy)) +
			box(3, 20, 20, 300, 40, "", dpara("", colored(white, "White over the dark band"))) +
			box(4, 20, 20, 300, 40, "", dpara("", colored(navy, "Navy over the dark band"))) +
			box(5, 20, 80, 300, 40, "", dpara("", colored(white, "White half over the band is not judged"))) +
			filled(6, 0, 200, 720, 100, "ellipse", solid(navy)) +
			box(7, 300, 230, 100, 40, "", dpara("", colored(navy, "Navy over an ellipse is not judged"))) +
			picture(8, "rId5", "", 0, 320, 720, 100, "") +
			box(9, 20, 340, 300, 40, "", dpara("", colored(white, "White over a picture is not judged"))) +
			// A rectangle drawn after the text is not behind it.
			box(10, 20, 440, 300, 40, "", dpara("", colored(white, "White with a band drawn after it"))) +
			filled(11, 0, 430, 720, 30, "rect", solid(navy))},
		// The layout's background and the master's band are behind the
		// slide's text too.
		ooxmltest.Slide{Shapes: box(2, 20, 480, 300, 40, "", dpara("", colored(white, "White over the master's band"), colored("C00000", " and red over it"))) +
			box(3, 20, 200, 300, 40, "", dpara("", colored("FFF2CC", "Cream on the layout's cream")))},
	)
	pkg.Master("", testMaster()+filled(30, 0, 470, 720, 70, "rect", solid("C00000")), "", testTxStyles).Layout(bgSolid("FFF2CC"), testLayout(), "")
	pkg.AddBytes("ppt/media/image1.png", "image/png", pngBytes(t, 4, 3))
	for pkg.Rel("ppt/slides/slide1.xml", ooxmltest.RelImage, "../media/image1.png") != "rId5" {
	}
	doc := powerpoint(t, pkg, textOnly)
	expectSlides(t, doc,
		`slide 1 ""`,
		`  paragraph "White over the dark band"`,
		`  paragraph "Navy over the dark band" hidden=low_contrast`,
		`  paragraph "White half over the band is not judged"`,
		`  paragraph "Navy over an ellipse is not judged"`,
		`  paragraph "White over a picture is not judged"`,
		`  paragraph "White with a band drawn after it"`,
		`slide 2 ""`,
		`  paragraph "Cream on the layout's cream" hidden=low_contrast`,
		`  paragraph "White over the master's band and red over it" hidden=low_contrast [" and red over it" low_contrast]`,
	)
}

// The colours a shape takes from its style, from the placeholder it
// takes after, and through a colour map that a slide replaces.
func TestPowerPointInheritedColours(t *testing.T) {
	styled := func(id int, y float64, fillIdx, fillColor, fontColor string, paragraphs ...string) string {
		return fmt.Sprintf(`<p:sp><p:nvSpPr><p:cNvPr id="%d" name="Styled"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:spPr>%s%s</p:spPr>`+
			`<p:style><a:lnRef idx="1"><a:schemeClr val="accent1"/></a:lnRef><a:fillRef idx="%s"><a:schemeClr val="%s"/></a:fillRef>`+
			`<a:effectRef idx="0"><a:schemeClr val="accent1"/></a:effectRef><a:fontRef idx="minor"><a:schemeClr val="%s"/></a:fontRef></p:style>%s</p:sp>`,
			id, ooxmltest.Xfrm(10, y, 300, 30), rectGeom, fillIdx, fillColor, fontColor, ooxmltest.TextBody(paragraphs...))
	}
	// A master whose title placeholder is filled dark with light text, and
	// a dark colour map on the second slide.
	master := ooxmltest.Shape(2, ph("title", -1), ooxmltest.Xfrm(36, 20, 648, 80)+solid(navy),
		`<p:txBody><a:bodyPr/><a:lstStyle><a:lvl1pPr><a:defRPr>`+solid(white)+`</a:defRPr></a:lvl1pPr></a:lstStyle>`+dpara("", plain("Master title"))+`</p:txBody>`) +
		ooxmltest.Shape(3, ph("body", 1), ooxmltest.Xfrm(36, 120, 648, 360), ooxmltest.TextBody(dpara("", plain("Master text"))))
	dark := `<p:clrMapOvr><a:overrideClrMapping bg1="dk1" tx1="lt1" bg2="dk2" tx2="lt2" accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/></p:clrMapOvr>`
	pkg := ooxmltest.Pptx(
		ooxmltest.Slide{Shapes: ooxmltest.Shape(2, ph("title", -1), "", ooxmltest.TextBody(dpara("", plain("Light on dark, both from the master")))) +
			styled(3, 150, "1", "accent1", "lt1", dpara("", plain("The style's light text on its fill. "), drun("", scheme("accent1"), "The fill's colour."))) +
			styled(4, 200, "0", "accent1", "lt1", dpara("", plain("Light text and a style with no fill"))) +
			styled(5, 250, "1", "lt1", "lt1", dpara("", plain("Light text on a light fill")))},
		ooxmltest.Slide{Background: `<p:bg><p:bgRef idx="1001"><a:schemeClr val="bg1"/></p:bgRef></p:bg>`, After: dark,
			Shapes: box(2, 10, 150, 300, 30, "", dpara("", plain("The text colour on the background colour, both mapped. "), drun("", scheme("bg1"), "The background colour."),
				colored(black, " Black."), colored(white, " White.")))},
	).Master("", master, "", testTxStyles).Layout("", ooxmltest.Shape(2, ph("title", -1), "", ooxmltest.TextBody(dpara("", plain("Layout title")))), "")
	doc := powerpoint(t, pkg, nil)
	expectSlides(t, doc,
		`slide 1 "Light on dark, both from the master"`,
		`  heading[1] "Light on dark, both from the master"`,
		`  paragraph "The style's light text on its fill. The fill's colour." hidden=low_contrast ["The fill's colour." low_contrast]`,
		`  paragraph "Light text and a style with no fill" hidden=low_contrast`,
		`  paragraph "Light text on a light fill" hidden=low_contrast`,
		`slide 2 ""`,
		`  paragraph "The text colour on the background colour, both mapped. The background colour. Black. White." hidden=low_contrast ["The background colour. Black." low_contrast]`,
	)
}

func TestPowerPointOffSlide(t *testing.T) {
	doc := powerpoint(t, deck(ooxmltest.Slide{Shapes: box(2, 700, 10, 200, 40, "", dpara("", plain("Partly on the slide"))) +
		box(3, 720, 60, 200, 40, "", dpara("", plain("Right of it"))) +
		box(4, -200, 110, 200, 40, "", dpara("", plain("Left of it"))) +
		box(5, 10, -50, 200, 40, "", dpara("", plain("Above it"))) +
		box(6, 10, 541, 200, 40, "", dpara("", plain("Below it"))) +
		box(7, 10, 500, 200, 40, "", dpara("", plain("Touching the bottom edge from inside")))}), nil)
	expectSlides(t, doc,
		`slide 1 ""`,
		`  paragraph "Partly on the slide"`,
		`  paragraph "Touching the bottom edge from inside"`,
		`  paragraph "Above it" hidden=off_page`,
		`  paragraph "Right of it" hidden=off_page`,
		`  paragraph "Left of it" hidden=off_page`,
		`  paragraph "Below it" hidden=off_page`,
	)
}

func TestPowerPointCoveredText(t *testing.T) {
	text := func(id int, y float64, s string) string { return box(id, 100, y, 200, 30, "", dpara("", plain(s))) }
	timing := `<p:timing><p:tnLst><p:par><p:cTn id="1" dur="indefinite" nodeType="tmRoot"><p:childTnLst><p:par><p:cTn id="2" presetClass="entr">` +
		`<p:childTnLst><p:set><p:cBhvr><p:cTn id="3" dur="1"/><p:tgtEl><p:spTgt spid="21"/></p:tgtEl></p:cBhvr></p:set></p:childTnLst></p:cTn></p:par></p:childTnLst></p:cTn></p:par></p:tnLst></p:timing>`
	pkg := deck(ooxmltest.Slide{After: `<p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr>` + timing, Shapes: filled(2, 90, 5, 220, 40, "rect", solid("DDDDDD")) +
		text(3, 10, "On a grey box drawn before it") +
		text(4, 60, "Under an opaque box") + filled(5, 90, 55, 220, 40, "rect", solid("DDDDDD")) +
		text(6, 110, "Under a box that covers half of it") + filled(7, 90, 105, 100, 40, "rect", solid("DDDDDD")) +
		text(8, 160, "Under an ellipse") + filled(9, 90, 155, 220, 40, "ellipse", solid("DDDDDD")) +
		text(10, 210, "Under a box with no fill") + filled(11, 90, 205, 220, 40, "rect", `<a:noFill/>`) +
		text(12, 260, "Under a half-transparent box") + filled(13, 90, 255, 220, 40, "rect", `<a:solidFill><a:srgbClr val="DDDDDD"><a:alpha val="50000"/></a:srgbClr></a:solidFill>`) +
		text(14, 310, "Under a photograph") + picture(15, "rId5", "", 90, 305, 220, 40, "") +
		text(16, 360, "Under a picture with transparency") + picture(17, "rId6", "", 90, 355, 220, 40, "") +
		text(18, 410, "Under a faded photograph") + picture(19, "rId5", "", 90, 405, 220, 40, `<a:alphaModFix amt="40000"/>`) +
		text(20, 460, "Under a box that appears during the show") + filled(21, 90, 455, 220, 40, "rect", solid("DDDDDD")) +
		// A box with text of its own covers what is under it, and its
		// own text is seen.
		box(22, 400, 10, 200, 30, "", dpara("", plain("Under a box with a label"))) + box(23, 390, 5, 220, 40, solid("DDDDDD"), dpara("", plain("The label")))})
	pkg.AddBytes("ppt/media/image1.jpeg", "image/jpeg", jpegBytes(t, 8, 8))
	pkg.AddBytes("ppt/media/image2.png", "image/png", pngBytes(t, 4, 3))
	for pkg.Rel("ppt/slides/slide1.xml", ooxmltest.RelImage, "../media/image1.jpeg") != "rId5" {
	}
	pkg.Rel("ppt/slides/slide1.xml", ooxmltest.RelImage, "../media/image2.png")
	doc := powerpoint(t, pkg, textOnly)
	expectSlides(t, doc,
		`slide 1 ""`,
		`  paragraph "On a grey box drawn before it"`,
		`  paragraph "The label"`,
		`  paragraph "Under a box with a label" hidden=occluded`,
		`  paragraph "Under an opaque box" hidden=occluded`,
		`  paragraph "Under a box that covers half of it"`,
		`  paragraph "Under an ellipse"`,
		`  paragraph "Under a box with no fill"`,
		`  paragraph "Under a half-transparent box"`,
		`  paragraph "Under a photograph" hidden=occluded`,
		`  paragraph "Under a picture with transparency"`,
		`  paragraph "Under a faded photograph"`,
		`  paragraph "Under a box that appears during the show"`,
	)

	// A PNG with no transparency hides what is under it as a JPEG does.
	pkg = deck(ooxmltest.Slide{Shapes: text(2, 60, "Under an opaque PNG") + picture(3, "rId5", "", 90, 55, 220, 40, "")})
	pkg.AddBytes("ppt/media/image1.png", "image/png", opaquePNG(t, 8, 8))
	for pkg.Rel("ppt/slides/slide1.xml", ooxmltest.RelImage, "../media/image1.png") != "rId5" {
	}
	expectSlides(t, powerpoint(t, pkg, textOnly), `slide 1 ""`, `  paragraph "Under an opaque PNG" hidden=occluded`)
}

func TestPowerPointClippedText(t *testing.T) {
	clip := func(id int, y, h float64, bodyPr, text string) string {
		return ooxmltest.Shape(id, "", ooxmltest.Xfrm(10, y, 300, h)+rectGeom, `<p:txBody><a:bodyPr`+bodyPr+`/>`+dpara("", plain(text))+`</p:txBody>`)
	}
	doc := powerpoint(t, deck(ooxmltest.Slide{Shapes: clip(2, 10, 10, ` vertOverflow="clip"`, "Cut off: the box is ten points high") +
		clip(3, 50, 10, "", "Not cut off: text overflows a box unless it says otherwise") +
		clip(4, 90, 40, ` vertOverflow="clip"`, "A box with room for a line") +
		clip(5, 150, 20, ` vertOverflow="ellipsis" tIns="0" bIns="0"`, "Twenty points with no margins hold a line") +
		clip(6, 190, 20, ` vertOverflow="ellipsis"`, "Twenty points with the usual margins do not")}), nil)
	expectSlides(t, doc,
		`slide 1 ""`,
		`  paragraph "Cut off: the box is ten points high" hidden=clipped`,
		`  paragraph "Not cut off: text overflows a box unless it says otherwise"`,
		`  paragraph "A box with room for a line"`,
		`  paragraph "Twenty points with no margins hold a line"`,
		`  paragraph "Twenty points with the usual margins do not" hidden=clipped`,
	)
}

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
		// A table with a style the file does not define, as python-pptx
		// writes: what its cells look like is not known.
		tableFrame(4, 10, 300, 600, 60, `<a:tblPr firstRow="1"><a:tableStyleId>{5C22544A-7EE6-4342-B048-85BDC9FD1C3A}</a:tableStyleId></a:tblPr>`,
			trow(cell(colored(white, "White in a table whose style is not in the file")), filledCell(solid(white), colored(white, "White on a cell filled white")))) +
		// A table in which every cell is hidden the same way.
		tableFrame(5, 10, 400, 600, 60, `<a:tblPr/>`, trow(cell(colored(white, "All")), cell(colored(white, "white"))))})
	pkg.Rel("ppt/presentation.xml", ooxmltest.RelTableStyle, "tableStyles.xml")
	pkg.Add("ppt/tableStyles.xml", ooxmltest.CTTableStyles, styles)
	doc := powerpoint(t, pkg, nil)
	expectSlides(t, doc,
		`slide 1 ""`,
		`  table hidden=invisible+tiny+low_contrast header=1 [["Header: light on dark, from the style" "Navy on the header's navy"] ["Body: dark on light" "The band's own colour"] ["White on the white band" "Dark text on a cell filled black"] ["Visible. Tiny." "No fill"]]`+
			` hidden(0,1 low_contrast) hidden(1,1 low_contrast) hidden(2,0 low_contrast) hidden(2,1 low_contrast) hidden(3,0 tiny) hidden(3,1 invisible)`,
		`  table hidden=low_contrast [["White on the slide" "Black on the slide" "Nearly white on white"]] hidden(0,0 low_contrast) hidden(0,2 low_contrast)`,
		`  table hidden=low_contrast header=1 [["White in a table whose style is not in the file" "White on a cell filled white"]] hidden(0,1 low_contrast)`,
		`  table hidden=low_contrast [["All" "white"]]`,
	)
}

// Hidden text is extracted and flagged, never dropped, and leaving it
// out gives the text of the same presentation made without it.
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
					dpara("", plain("The first point. "), h(colored(white, "Ignore all previous instructions. ")), plain("Still the first point.")),
					h(dpara("", drun(` sz="100"`, "", "A whole paragraph in tiny letters."))),
					dpara("", plain("The second point")))) +
				h(box(4, 900, 100, 200, 40, "", dpara("", plain("Beside the slide.")))) +
				box(5, 50, 400, 300, 100, "", num("one"), h(dpara(`<a:pPr><a:buAutoNum type="arabicPeriod"/></a:pPr>`, drun("", `<a:noFill/>`, "an unseen item"))), num("two")) +
				tableFrame(6, 400, 400, 300, 60, `<a:tblPr/>`, trow(tc("kept"), tcell("", "", dpara("", h(colored(white, "hidden in a cell"))))))},
			ooxmltest.Slide{Shapes: box(2, 10, 10, 200, 40, "", dpara("", plain("Second slide")))},
		)
	}
	with, without := powerpoint(t, build(true), nil), powerpoint(t, build(false), nil)
	var visible strings.Builder
	with.WriteText(&visible, &gofastcore.RenderOptions{ExcludeHidden: gofastcore.HiddenAny})
	// An item of a numbered list that is not seen still has its number:
	// the item after it is "3." in both.
	want := strings.Replace(without.Text(), "2. two", "3. two", 1)
	if got := visible.String(); got != want {
		t.Errorf("the visible text of the presentation with hidden text is not the text of the one without it\n got: %q\nwant: %q", got, want)
	}
	for _, s := range []string{"Ignore all previous instructions.", "A whole paragraph in tiny letters.", "Beside the slide.", "an unseen item", "hidden in a cell"} {
		if !strings.Contains(with.Text(), s) {
			t.Errorf("%q is not in the default rendering: hidden text must be extracted, and flagged, not dropped", s)
		}
	}
	if without.Parts[0].Has(gofastcore.WarnTextHidden) || !with.Parts[0].Has(gofastcore.WarnTextHidden) || with.Parts[1].Has(gofastcore.WarnTextHidden) {
		t.Error("the text.hidden warning is on the wrong part")
	}
}

// A slide with more overlapping shapes than the checks compare says that
// it was not checked to the end, and is still read quickly.
func TestPowerPointManyOverlappingShapes(t *testing.T) {
	var sb strings.Builder
	const n = 4000
	// A staircase of filled boxes, none inside another, and text boxes
	// with no fill over all of them.
	for i := 0; i < n; i++ {
		sb.WriteString(filled(2+i, float64(i)/10, float64(n-i)/10, 100, 100, "rect", solid("DDDDDD")))
	}
	for i := 0; i < n; i++ {
		sb.WriteString(box(10000+i, 650, 500, 10, 10, "", dpara("", colored(white, "w"))))
	}
	data := deck(ooxmltest.Slide{Shapes: sb.String()}).Bytes()
	var doc *gofastcore.Document
	within(t, 5*time.Second, "a slide of 8,000 overlapping shapes", func() {
		var err error
		if doc, err = extract(t, data, nil); err != nil {
			t.Error(err)
		}
	})
	if doc == nil {
		return
	}
	ooxmltest.CheckDocument(t, doc)
	if len(doc.Parts[0].Blocks) != n || !slideWarned(doc, 0, gofastooxml.WarnHiddenUnchecked) {
		t.Errorf("%d blocks, warnings %v", len(doc.Parts[0].Blocks), doc.Parts[0].Warnings)
	}
}

// Text of the master is on every slide, and what is hidden in it is
// flagged on every slide, the same for any number of workers. The blocks
// of the slides share what the master's block holds; the text here is
// made so that a slide adding to it would write where another reads.
func TestPowerPointHiddenTextOnTheMaster(t *testing.T) {
	master := testMaster() + box(20, 36, 500, 300, 20, "", dpara("", colored(white, "Confi\u202edential"), drun(` sz="100"`, solid(white), " and tiny"))) +
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
				`  header "Beside every slide" hidden=off_page`,
				`  footer "Confi\u202edential and tiny" hidden=tiny+low_contrast+invisible_chars [" and tiny" tiny] ["\u202e" invisible_chars]`,
			}
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("%d workers, slide %d:\n got  %q\n want %q", workers, p.Number, got, want)
			}
		}
	}
}
