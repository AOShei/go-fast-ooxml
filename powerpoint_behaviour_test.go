package gofastooxml_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// Helpers for writing the shapes of a slide.

const rectGeom = `<a:prstGeom prst="rect"><a:avLst/></a:prstGeom>`

// ph is the content of p:nvPr for a placeholder of the given type ("" for
// the default, an object) and number (negative for none).
func ph(typ string, idx int) string {
	s := `<p:ph`
	if typ != "" {
		s += ` type="` + typ + `"`
	}
	if idx >= 0 {
		s += fmt.Sprintf(` idx="%d"`, idx)
	}
	return s + `/>`
}

// dpara is an a:p element: props is the content of a:pPr's start tag and
// element ("lvl=\"1\"" or with children after a ">"), and runs follow.
func dpara(pPr string, runs ...string) string {
	return `<a:p>` + pPr + strings.Join(runs, "") + `</a:p>`
}

// drun is a run of text with the given a:rPr attributes and content.
func drun(attrs, props, text string) string {
	return `<a:r><a:rPr lang="en-US"` + attrs + `>` + props + `</a:rPr><a:t>` + ooxmltest.EscapeXML(text) + `</a:t></a:r>`
}

func plain(text string) string { return drun("", "", text) }

func solid(rgb string) string {
	return `<a:solidFill><a:srgbClr val="` + rgb + `"/></a:solidFill>`
}

func scheme(name string) string {
	return `<a:solidFill><a:schemeClr val="` + name + `"/></a:solidFill>`
}

// box is a text box at the given place with the given fill (an element,
// or "") and paragraphs.
func box(id int, x, y, w, h float64, fill string, paragraphs ...string) string {
	return ooxmltest.Shape(id, "", ooxmltest.Xfrm(x, y, w, h)+rectGeom+fill, ooxmltest.TextBody(paragraphs...))
}

// filled is a rectangle with no text.
func filled(id int, x, y, w, h float64, geom, fill string) string {
	return ooxmltest.Shape(id, "", ooxmltest.Xfrm(x, y, w, h)+`<a:prstGeom prst="`+geom+`"><a:avLst/></a:prstGeom>`+fill, "")
}

// picture is a p:pic showing the image with the given relationship.
func picture(id int, rel, alt string, x, y, w, h float64, extra string) string {
	return fmt.Sprintf(`<p:pic><p:nvPicPr><p:cNvPr id="%d" name="Picture %d" descr="%s"/><p:cNvPicPr/><p:nvPr/></p:nvPicPr>`+
		`<p:blipFill><a:blip r:embed="%s">%s</a:blip><a:stretch><a:fillRect/></a:stretch></p:blipFill><p:spPr>%s%s</p:spPr></p:pic>`,
		id, id, alt, rel, extra, ooxmltest.Xfrm(x, y, w, h), rectGeom)
}

// renderSlides writes a presentation one line per slide and block, for
// comparing with what a test expects.
func renderSlides(doc *gofastcore.Document, boxes bool) []string {
	var out []string
	for p := range doc.Parts {
		part := &doc.Parts[p]
		line := fmt.Sprintf("%s %d %q", part.Kind, part.Number, part.Name)
		if part.Display != 0 {
			line += " display=" + part.Display.String()
		}
		out = append(out, line)
		for i := range part.Blocks {
			b := &part.Blocks[i]
			var sb strings.Builder
			sb.WriteString("  " + b.Kind.String())
			if b.Level > 0 {
				fmt.Fprintf(&sb, "[%d]", b.Level)
			}
			if b.Marker != "" {
				fmt.Fprintf(&sb, " <%s>", b.Marker)
			}
			if b.Text != "" {
				fmt.Fprintf(&sb, " %q", b.Text)
			}
			if b.Display != 0 {
				fmt.Fprintf(&sb, " display=%s", b.Display)
			}
			for _, sp := range b.Spans {
				fmt.Fprintf(&sb, " [%q", b.Text[sp.Start:sp.End])
				if sp.Style != 0 {
					sb.WriteString(" " + sp.Style.String())
				}
				if sp.Href != "" {
					sb.WriteString(" ->" + sp.Href)
				}
				if sp.Display != 0 {
					sb.WriteString(" " + sp.Display.String())
				}
				sb.WriteString("]")
			}
			keys := make([]string, 0, len(b.Attrs))
			for k := range b.Attrs {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(&sb, " %s=%s", k, b.Attrs[k])
			}
			if t := b.Table; t != nil {
				if t.HeaderRows > 0 {
					fmt.Fprintf(&sb, " header=%d", t.HeaderRows)
				}
				fmt.Fprintf(&sb, " %q", t.Rows)
				if len(t.Merges) > 0 {
					fmt.Fprintf(&sb, " merges=%v", t.Merges)
				}
				for _, h := range t.Display {
					fmt.Fprintf(&sb, " display(%d,%d %s)", h.Row, h.Col, h.Display)
				}
			}
			if img := b.Image; img != nil {
				fmt.Fprintf(&sb, " image(%s %s %dx%d %d bytes)", img.ID, img.MIME, img.Width, img.Height, len(img.Data))
			}
			if r := b.Loc.BBox; boxes && r != (gofastcore.Rect{}) {
				fmt.Fprintf(&sb, " @%v,%v,%v,%v", r.X0, r.Y0, r.X1, r.Y1)
			}
			out = append(out, sb.String())
		}
	}
	return out
}

func powerpoint(t *testing.T, pkg *ooxmltest.Package, opts *gofastcore.Options) *gofastcore.Document {
	t.Helper()
	doc, err := extract(t, pkg.Bytes(), opts)
	if err != nil {
		t.Fatal(err)
	}
	ooxmltest.CheckDocument(t, doc)
	return doc
}

func expectLines(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("slides:\n got:\n  %s\n want:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func expectSlides(t *testing.T, doc *gofastcore.Document, want ...string) {
	t.Helper()
	expectLines(t, renderSlides(doc, false), want...)
}

func slideWarned(doc *gofastcore.Document, part int, code string) bool {
	return doc.Parts[part].Has(code)
}

// A master with the usual placeholders and text styles, and a layout
// that takes after it.
const (
	testTxStyles = `<p:txStyles>` +
		`<p:titleStyle><a:lvl1pPr><a:buNone/><a:defRPr sz="4400"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill></a:defRPr></a:lvl1pPr></p:titleStyle>` +
		`<p:bodyStyle><a:lvl1pPr><a:buFont typeface="Arial"/><a:buChar char="•"/><a:defRPr sz="3200"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill></a:defRPr></a:lvl1pPr>` +
		`<a:lvl2pPr><a:buFont typeface="Arial"/><a:buChar char="–"/><a:defRPr sz="2800"/></a:lvl2pPr>` +
		`<a:lvl3pPr><a:buFont typeface="Wingdings"/><a:buChar char="§"/><a:defRPr sz="2400"/></a:lvl3pPr></p:bodyStyle>` +
		`<p:otherStyle><a:lvl1pPr><a:defRPr sz="1800"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill></a:defRPr></a:lvl1pPr></p:otherStyle></p:txStyles>`
)

func testMaster() string {
	return ooxmltest.Shape(2, ph("title", -1), ooxmltest.Xfrm(36, 20, 648, 80), ooxmltest.TextBody(dpara("", plain("Click to edit Master title style")))) +
		ooxmltest.Shape(3, ph("body", 1), ooxmltest.Xfrm(36, 120, 648, 360), ooxmltest.TextBody(dpara("", plain("Click to edit Master text styles")))) +
		ooxmltest.Shape(4, ph("dt", 2), ooxmltest.Xfrm(36, 500, 160, 30), ooxmltest.TextBody(dpara("", plain("1/1/2026")))) +
		ooxmltest.Shape(5, ph("ftr", 3), ooxmltest.Xfrm(240, 500, 240, 30), ooxmltest.TextBody(dpara(""))) +
		ooxmltest.Shape(6, ph("sldNum", 4), ooxmltest.Xfrm(524, 500, 160, 30), ooxmltest.TextBody(dpara("", `<a:fld id="{1}" type="slidenum"><a:t>‹#›</a:t></a:fld>`)))
}

func testLayout() string {
	return ooxmltest.Shape(2, ph("title", -1), "", ooxmltest.TextBody(dpara("", plain("Click to edit Master title style")))) +
		ooxmltest.Shape(3, ph("", 1), "", ooxmltest.TextBody(dpara("", plain("Click to edit Master text styles")))) +
		ooxmltest.Shape(4, ph("subTitle", 5), ooxmltest.Xfrm(100, 300, 520, 60),
			`<p:txBody><a:bodyPr/><a:lstStyle><a:lvl1pPr><a:buNone/></a:lvl1pPr></a:lstStyle>`+dpara("", plain("Click to edit Master subtitle style"))+`</p:txBody>`) +
		ooxmltest.Shape(5, ph("ftr", 11), "", ooxmltest.TextBody(dpara(""))) +
		ooxmltest.Shape(6, ph("sldNum", 12), "", ooxmltest.TextBody(dpara("", `<a:fld id="{1}" type="slidenum"><a:t>‹#›</a:t></a:fld>`)))
}

// deck is a presentation on the test master and layout.
func deck(slides ...ooxmltest.Slide) *ooxmltest.Package {
	return ooxmltest.Pptx(slides...).Master("", testMaster(), "", testTxStyles).Layout("", testLayout(), "")
}

func TestPowerPointPlaceholders(t *testing.T) {
	doc := powerpoint(t, deck(
		ooxmltest.Slide{Shapes: ooxmltest.Shape(2, ph("title", -1), "", ooxmltest.TextBody(dpara("", plain("Quarterly "), plain("results")))) +
			ooxmltest.Shape(3, ph("subTitle", 5), "", ooxmltest.TextBody(dpara("", plain("Third quarter")))) +
			ooxmltest.Shape(4, ph("", 1), "", ooxmltest.TextBody(
				dpara("", plain("Revenue")),
				dpara(`<a:pPr lvl="1"/>`, plain("Up four percent")),
				dpara(`<a:pPr lvl="2"/>`, plain("In every region")),
				dpara(`<a:pPr><a:buNone/></a:pPr>`, plain("A remark without a bullet")),
				dpara(""),
				dpara("", plain("Costs"))))},
		// A title of two paragraphs and a line break is one heading; a
		// centred title is a title too.
		ooxmltest.Slide{Shapes: ooxmltest.Shape(2, ph("ctrTitle", -1), "", ooxmltest.TextBody(dpara("", plain("Part"), `<a:br/>`, plain("two")), dpara("", plain("continued"))))},
		// A placeholder the layout does not have takes after the master's.
		ooxmltest.Slide{Shapes: ooxmltest.Shape(2, ph("body", 7), "", ooxmltest.TextBody(dpara("", plain("Stray body"))))},
	), nil)
	expectLines(t, renderSlides(doc, true),
		`slide 1 "Quarterly results"`,
		`  heading[1] "Quarterly results" @36,20,684,100`,
		`  list_item[1] <•> "Revenue" @36,120,684,480`,
		`  list_item[2] <–> "Up four percent" @36,120,684,480`,
		`  list_item[3] <▪> "In every region" @36,120,684,480`,
		`  paragraph "A remark without a bullet" @36,120,684,480`,
		`  list_item[1] <•> "Costs" @36,120,684,480`,
		`  heading[2] "Third quarter" @100,300,620,360`,
		`slide 2 "Part two continued"`,
		`  heading[1] "Part\ntwo\ncontinued" @36,20,684,100`,
		`slide 3 ""`,
		`  list_item[1] <•> "Stray body" @36,120,684,480`,
	)
	if doc.Parts[0].Width != 720 || doc.Parts[0].Height != 540 {
		t.Errorf("slide size %v x %v", doc.Parts[0].Width, doc.Parts[0].Height)
	}
}

func TestPowerPointBulletsAndNumbers(t *testing.T) {
	num := func(typ string, start int, text string) string {
		at := ""
		if start > 0 {
			at = fmt.Sprintf(` startAt="%d"`, start)
		}
		return dpara(`<a:pPr><a:buAutoNum type="`+typ+`"`+at+`/></a:pPr>`, plain(text))
	}
	sub := func(typ, text string) string {
		return dpara(`<a:pPr lvl="1"><a:buAutoNum type="`+typ+`"/></a:pPr>`, plain(text))
	}
	doc := powerpoint(t, deck(ooxmltest.Slide{Shapes: box(2, 10, 10, 300, 400, "",
		dpara("", plain("A text box has no bullets")),
		num("arabicPeriod", 0, "one"), num("arabicPeriod", 0, "two"),
		sub("alphaLcParenR", "first"), sub("alphaLcParenR", "second"),
		num("arabicPeriod", 0, "three"),
		sub("alphaLcParenR", "starts again"),
		dpara("", plain("A break in the list")),
		num("arabicPeriod", 0, "starts again"),
		num("romanUcPeriod", 4, "four"), num("romanUcPeriod", 4, "five"),
		num("alphaUcParenBoth", 27, "twenty-seven"),
		num("arabicPlain", 0, "plain"),
		num("circleNumDbPlain", 0, "circled"),
		num("thaiAlphaPeriod", 2, "a scheme not known"),
		dpara(`<a:pPr><a:buFont typeface="Wingdings"/><a:buChar char="&#xF0FC;"/></a:pPr>`, plain("a tick")),
		dpara(`<a:pPr><a:buFont typeface="Arial"/><a:buChar char="→"/></a:pPr>`, plain("an arrow")),
		dpara(`<a:pPr><a:buBlip><a:blip r:embed="rId9"/></a:buBlip></a:pPr>`, plain("a picture for a bullet")),
	)}), nil)
	expectSlides(t, doc,
		`slide 1 ""`,
		`  paragraph "A text box has no bullets"`,
		`  list_item[1] <1.> "one"`,
		`  list_item[1] <2.> "two"`,
		`  list_item[2] <a)> "first"`,
		`  list_item[2] <b)> "second"`,
		`  list_item[1] <3.> "three"`,
		`  list_item[2] <a)> "starts again"`,
		`  paragraph "A break in the list"`,
		`  list_item[1] <1.> "starts again"`,
		`  list_item[1] <IV.> "four"`,
		`  list_item[1] <V.> "five"`,
		`  list_item[1] <(AA)> "twenty-seven"`,
		`  list_item[1] <1> "plain"`,
		`  list_item[1] <①> "circled"`,
		`  list_item[1] <2.> "a scheme not known"`,
		`  list_item[1] <✓> "a tick"`,
		`  list_item[1] <→> "an arrow"`,
		`  list_item[1] <•> "a picture for a bullet"`,
	)
	for _, b := range doc.Parts[0].Blocks {
		if (b.Kind == gofastcore.KindListItem) && b.Ordered != (b.Marker != "✓" && b.Marker != "→" && b.Marker != "•") {
			t.Errorf("%q: ordered = %v", b.Text, b.Ordered)
		}
	}
}

func TestPowerPointTextAndFields(t *testing.T) {
	pkg := deck(
		ooxmltest.Slide{Shapes: box(2, 10, 10, 300, 100, "",
			dpara("", plain("Name:\tvalue"), `<a:br/>`, plain("second line  ")),
			dpara("", plain("   ")),
			dpara("", plain("o\ufb03ce \u2028 after")),
			dpara("", plain("Slide "), `<a:fld id="{2}" type="slidenum"><a:rPr lang="en-US"/><a:t>99</a:t></a:fld>`,
				plain(" of the deck, on "), `<a:fld id="{3}" type="datetime1"><a:rPr lang="en-US"/><a:t>4 March 2026</a:t></a:fld>`),
			// An equation: its text for programs that read equations, a
			// picture of it for those that do not. One of the two is read.
			dpara("", plain("Where "), `<mc:AlternateContent><mc:Choice Requires="a14"><a14:m><m:oMath><m:r><m:t>E=m</m:t></m:r><m:sSup><m:e><m:r><m:t>c</m:t></m:r></m:e><m:sup><m:r><m:t>2</m:t></m:r></m:sup></m:sSup></m:oMath></a14:m></mc:Choice>`+
				`<mc:Fallback>`+plain("[equation]")+`</mc:Fallback></mc:AlternateContent>`, plain(" holds")),
		)},
	)
	// The first slide is numbered 5.
	pkg.Add("ppt/presentation.xml", ooxmltest.CTPresentation, `<p:presentation`+ooxmltest.PresNS+` firstSlideNum="5">`+
		`<p:sldIdLst><p:sldId id="256" r:id="rId2"/></p:sldIdLst><p:sldSz cx="9144000" cy="6858000"/></p:presentation>`)
	doc := powerpoint(t, pkg, nil)
	expectSlides(t, doc,
		`slide 1 ""`,
		`  paragraph "Name:\tvalue\nsecond line"`,
		`  paragraph "office \n after"`,
		`  paragraph "Slide 5 of the deck, on 4 March 2026"`,
		`  paragraph "Where E=mc^2 holds"`,
	)
}

func tableFrame(id int, x, y, w, h float64, tblPr string, rows ...string) string {
	return fmt.Sprintf(`<p:graphicFrame><p:nvGraphicFramePr><p:cNvPr id="%d" name="Table %d"/><p:cNvGraphicFramePr/><p:nvPr/></p:nvGraphicFramePr>`+
		`<p:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></p:xfrm>`+
		`<a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/table"><a:tbl>%s<a:tblGrid/>%s</a:tbl></a:graphicData></a:graphic></p:graphicFrame>`,
		id, id, int64(x*12700), int64(y*12700), int64(w*12700), int64(h*12700), tblPr, strings.Join(rows, ""))
}

func trow(cells ...string) string { return `<a:tr h="100">` + strings.Join(cells, "") + `</a:tr>` }

// tcell is a table cell: attrs are the attributes of a:tc, props the
// content of a:tcPr, and the paragraphs its text.
func tcell(attrs, props string, paragraphs ...string) string {
	return `<a:tc` + attrs + `><a:txBody><a:bodyPr/>` + strings.Join(paragraphs, "") + `</a:txBody><a:tcPr>` + props + `</a:tcPr></a:tc>`
}

func tc(text string) string {
	if text == "" {
		return tcell("", "", dpara(""))
	}
	return tcell("", "", dpara("", plain(text)))
}

func TestPowerPointTables(t *testing.T) {
	doc := powerpoint(t, deck(ooxmltest.Slide{Shapes: tableFrame(2, 50, 100, 600, 200, `<a:tblPr firstRow="1" bandRow="1"/>`,
		trow(tc("Region"), tcell(` gridSpan="2"`, "", dpara("", plain("Sales"))), tcell(` hMerge="1"`, "", dpara(""))),
		trow(tcell(` rowSpan="2"`, "", dpara("", plain("North")), dpara("", plain("(new)"))), tc("10"), tc("12")),
		trow(tcell(` vMerge="1"`, "", dpara("")), tc("7"), tc("")),
		// A row with a cell too few, and a cell that a merge covers
		// though it says something: it is not shown.
		trow(tcell(` gridSpan="2"`, "", dpara("", plain("Total"))), tcell(` hMerge="1"`, "", dpara("", plain("covered")))),
	) + tableFrame(3, 50, 400, 600, 50, `<a:tblPr/>`) +
		tableFrame(4, 50, 320, 600, 50, `<a:tblPr/>`, trow(tc("One cell")))}), nil)
	expectLines(t, renderSlides(doc, true),
		`slide 1 ""`,
		`  table display=hidden header=1 [["Region" "Sales" ""] ["North\n(new)" "10" "12"] ["" "7" ""] ["Total\ncovered" "" ""]] merges=[{0 1 1 2} {1 0 2 1} {3 0 1 2}] display(3,0 hidden) @50,100,650,300`,
		`  table [["One cell"]] @50,320,650,370`,
	)
}

func opaquePNG(t testing.TB, w, h int) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func jpegBytes(t testing.TB, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPowerPointPictures(t *testing.T) {
	build := func() *ooxmltest.Package {
		pkg := deck(
			ooxmltest.Slide{Shapes: picture(2, "rId5", "A chart of sales &amp; costs", 100, 100, 200, 150, "") + picture(3, "rId6", "", 400, 100, 100, 100, "")},
			ooxmltest.Slide{Shapes: picture(2, "rId5", "The same picture again", 0, 0, 720, 540, "")},
		)
		pkg.AddBytes("ppt/media/image1.png", "image/png", pngBytes(t, 4, 3))
		for _, slide := range []string{"ppt/slides/slide1.xml", "ppt/slides/slide2.xml"} {
			for pkg.Rel(slide, ooxmltest.RelImage, "../media/image1.png") != "rId5" {
			}
		}
		pkg.ExternalRel("ppt/slides/slide1.xml", ooxmltest.RelImage, "https://example.com/logo.png")
		return pkg
	}
	doc := powerpoint(t, build(), &gofastcore.Options{Images: gofastcore.ImagesMeta})
	expectLines(t, renderSlides(doc, true),
		`slide 1 ""`,
		`  image "A chart of sales & costs" image(ppt/media/image1.png image/png 4x3 0 bytes) @100,100,300,250`,
		`  image href=https://example.com/logo.png image(  0x0 0 bytes) @400,100,500,200`,
		`slide 2 ""`,
		`  image "The same picture again" image(ppt/media/image1.png image/png 4x3 0 bytes) @0,0,720,540`,
	)
	if !slideWarned(doc, 0, gofastcore.WarnPartImageOnly) || !warned(doc, gofastcore.WarnInputExternalReference) {
		t.Errorf("warnings: %v, %v", doc.Parts[0].Warnings, doc.Warnings)
	}

	// With the bytes asked for, they come with the first block that shows
	// the picture, however many workers there are.
	for _, workers := range []int{1, 4} {
		doc = powerpoint(t, build(), &gofastcore.Options{Images: gofastcore.ImagesData, Workers: workers})
		first, second := doc.Parts[0].Blocks[0].Image, doc.Parts[1].Blocks[0].Image
		if len(first.Data) == 0 || second.Data != nil || second.ID != first.ID {
			t.Errorf("%d workers: %d and %d bytes delivered", workers, len(first.Data), len(second.Data))
		}
	}

	// Pictures are blocks unless the caller asks for none. Asked for
	// none, the slide still says it has only pictures.
	if doc = powerpoint(t, build(), nil); len(doc.Parts[0].Blocks) != 2 || doc.Parts[0].Blocks[0].Image == nil || doc.Parts[0].Blocks[0].Text != "A chart of sales & costs" {
		t.Errorf("by default: blocks %v", doc.Parts[0].Blocks)
	}
	doc = powerpoint(t, build(), &gofastcore.Options{Images: gofastcore.ImagesNone})
	if len(doc.Parts[0].Blocks) != 0 || !slideWarned(doc, 0, gofastcore.WarnPartImageOnly) {
		t.Errorf("blocks %v, warnings %v", doc.Parts[0].Blocks, doc.Parts[0].Warnings)
	}
}

func TestPowerPointGroupsAndRotation(t *testing.T) {
	// A group shown at twice the size of its children's space and moved;
	// a group in it; a shape turned a quarter; one turned by 30 degrees;
	// one mirrored.
	group := func(id int, xfrm, inside string) string {
		return fmt.Sprintf(`<p:grpSp><p:nvGrpSpPr><p:cNvPr id="%d" name="Group"/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr>%s</p:grpSpPr>%s</p:grpSp>`, id, xfrm, inside)
	}
	emu := func(v float64) int64 { return int64(v * 12700) }
	groupXfrm := func(x, y, w, h, cx, cy, cw, ch float64, attrs string) string {
		return fmt.Sprintf(`<a:xfrm%s><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/><a:chOff x="%d" y="%d"/><a:chExt cx="%d" cy="%d"/></a:xfrm>`,
			attrs, emu(x), emu(y), emu(w), emu(h), emu(cx), emu(cy), emu(cw), emu(ch))
	}
	turned := func(id int, attrs string, x, y, w, h float64, text string) string {
		xf := strings.Replace(ooxmltest.Xfrm(x, y, w, h), "<a:xfrm>", "<a:xfrm"+attrs+">", 1)
		return ooxmltest.Shape(id, "", xf+rectGeom, ooxmltest.TextBody(dpara("", plain(text))))
	}
	doc := powerpoint(t, deck(ooxmltest.Slide{Shapes: group(2, groupXfrm(100, 50, 400, 200, 1000, 1000, 200, 100, ""),
		box(3, 1000, 1000, 50, 20, "", dpara("", plain("top left of the group")))+
			box(4, 1150, 1080, 50, 20, "", dpara("", plain("bottom right of the group")))+
			group(5, groupXfrm(1050, 1040, 100, 20, 0, 0, 10, 10, ""), box(6, 5, 5, 5, 5, "", dpara("", plain("inside the inner group"))))) +
		turned(7, ` rot="5400000"`, 100, 300, 200, 40, "a quarter turn") +
		turned(8, ` rot="1800000"`, 400, 300, 200, 100, "thirty degrees") +
		turned(9, ` flipH="1" flipV="1"`, 300, 420, 100, 50, "mirrored") +
		// A group turned upside down: its children change places.
		group(10, groupXfrm(500, 450, 200, 60, 0, 0, 200, 60, ` rot="10800000"`),
			box(11, 0, 0, 50, 20, "", dpara("", plain("drawn at the top left"))))}), nil)
	expectLines(t, renderSlides(doc, true),
		`slide 1 ""`,
		`  paragraph "top left of the group" @100,50,200,90`,
		`  paragraph "inside the inner group" @300,150,400,170`,
		`  paragraph "bottom right of the group" @400,210,500,250`,
		`  paragraph "a quarter turn" @180,220,220,420`,
		`  paragraph "thirty degrees" @388.4,256.7,611.6,443.3`,
		`  paragraph "mirrored" @300,420,400,470`,
		`  paragraph "drawn at the top left" @650,490,700,510`,
	)
}

func TestPowerPointReadingOrder(t *testing.T) {
	doc := powerpoint(t, deck(
		// Stored from the bottom up and from right to left; the title last.
		ooxmltest.Slide{Shapes: box(2, 400, 400, 200, 40, "", dpara("", plain("bottom right"))) +
			box(3, 50, 405, 200, 40, "", dpara("", plain("bottom left"))) +
			box(4, 400, 150, 200, 200, "", dpara("", plain("right column")), dpara("", plain("its second paragraph"))) +
			box(5, 50, 160, 200, 40, "", dpara("", plain("left column, top"))) +
			box(6, 50, 260, 200, 40, "", dpara("", plain("left column, lower"))) +
			box(7, -500, 100, 200, 40, "", dpara("", plain("beside the slide"))) +
			ooxmltest.Shape(8, "", "", ooxmltest.TextBody(dpara("", plain("nowhere in particular")))) +
			ooxmltest.Shape(9, ph("title", -1), ooxmltest.Xfrm(50, 450, 600, 60), ooxmltest.TextBody(dpara("", plain("A title at the bottom"))))},
	), nil)
	expectSlides(t, doc,
		`slide 1 "A title at the bottom"`,
		`  heading[1] "A title at the bottom"`,
		`  paragraph "left column, top"`,
		`  paragraph "right column"`,
		`  paragraph "its second paragraph"`,
		`  paragraph "left column, lower"`,
		`  paragraph "bottom left"`,
		`  paragraph "bottom right"`,
		`  paragraph "beside the slide"`,
		`  paragraph "nowhere in particular"`,
	)
}

func noteShapes(paragraphs ...string) string {
	return ooxmltest.Shape(2, ph("sldImg", 2), "", "") +
		ooxmltest.Shape(3, ph("body", 3), "", ooxmltest.TextBody(paragraphs...)) +
		ooxmltest.Shape(4, ph("sldNum", 5), "", ooxmltest.TextBody(dpara("", plain("7")))) +
		ooxmltest.Shape(5, ph("hdr", 6), "", ooxmltest.TextBody(dpara("", plain("A header of the notes page"))))
}

func TestPowerPointNotesAndComments(t *testing.T) {
	const relComments = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/comments"
	pkg := deck(
		ooxmltest.Slide{Shapes: box(2, 10, 10, 100, 40, "", dpara("", plain("Slide text"))),
			Notes: noteShapes(dpara("", plain("Remember the budget.")), dpara(""), dpara("", plain("Thank the team.")))},
		ooxmltest.Slide{Shapes: box(2, 10, 10, 100, 40, "", dpara("", plain("Second slide")))},
	)
	// Comments in the older form, on the first slide.
	pkg.Rel("ppt/slides/slide1.xml", relComments, "../comments/comment1.xml")
	pkg.Add("ppt/comments/comment1.xml", ooxmltest.CTSlideComments, `<p:cmLst`+ooxmltest.PresNS+`>`+
		`<p:cm authorId="0" dt="2026-03-04T09:30:00.000" idx="1"><p:pos x="10" y="10"/><p:text>Is this figure final?</p:text></p:cm>`+
		`<p:cm authorId="7" dt="2026-03-05T10:00:00.000" idx="2"><p:pos x="10" y="10"/><p:text>  Yes.  </p:text></p:cm></p:cmLst>`)
	pkg.Rel("ppt/presentation.xml", "http://schemas.openxmlformats.org/officeDocument/2006/relationships/commentAuthors", "commentAuthors.xml")
	pkg.Add("ppt/commentAuthors.xml", "application/vnd.openxmlformats-officedocument.presentationml.commentAuthors+xml",
		`<p:cmAuthorLst`+ooxmltest.PresNS+`><p:cmAuthor id="0" name="Ada Lovelace" initials="AL" lastIdx="1" clrIdx="0"/></p:cmAuthorLst>`)
	// Threaded comments, on the second: a comment's replies are stored
	// before its own text.
	pkg.Rel("ppt/slides/slide2.xml", "http://schemas.microsoft.com/office/2018/10/relationships/comments", "../comments/modernComment_1.xml")
	body := func(text string) string {
		return `<p188:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:t>` + text + `</a:t></a:r></a:p></p188:txBody>`
	}
	pkg.Add("ppt/comments/modernComment_1.xml", "application/vnd.ms-powerpoint.comments+xml", `<p188:cmLst`+ooxmltest.PresNS+`>`+
		`<p188:cm id="{A}" authorId="{X}" created="2026-03-06T08:00:00.000"><p188:replyLst>`+
		`<p188:reply id="{B}" authorId="{Y}" created="2026-03-06T09:00:00.000">`+body("Agreed.")+`</p188:reply>`+
		`<p188:reply id="{C}" authorId="{X}" created="2026-03-06T10:00:00.000">`+body("Done.")+`</p188:reply></p188:replyLst>`+
		body("Shorten this.")+`</p188:cm>`+
		`<p188:cm id="{D}" authorId="{Y}" created="2026-03-07T08:00:00.000">`+body("A second thread.")+`</p188:cm></p188:cmLst>`)
	pkg.Rel("ppt/presentation.xml", "http://schemas.microsoft.com/office/2018/10/relationships/authors", "authors.xml")
	pkg.Add("ppt/authors.xml", "application/vnd.ms-powerpoint.authors+xml",
		`<p188:authorLst`+ooxmltest.PresNS+`><p188:author id="{X}" name="Charles Babbage" initials="CB" userId="1" providerId="AD"/>`+
			`<p188:author id="{Y}" name="Mary Somerville" initials="MS" userId="2" providerId="AD"/></p188:authorLst>`)
	doc := powerpoint(t, pkg, nil)
	expectSlides(t, doc,
		`slide 1 ""`,
		`  paragraph "Slide text"`,
		`  note "Remember the budget." type=speaker`,
		`  note "Thank the team." type=speaker`,
		// The notes page's own header, as furniture.
		`  header "A header of the notes page"`,
		`  note "Is this figure final?" author=Ada Lovelace date=2026-03-04T09:30:00.000 type=comment`,
		`  note "Yes." date=2026-03-05T10:00:00.000 type=comment`,
		`slide 2 ""`,
		`  paragraph "Second slide"`,
		`  note "Shorten this." author=Charles Babbage date=2026-03-06T08:00:00.000 type=comment`,
		`  note "Agreed." author=Mary Somerville date=2026-03-06T09:00:00.000 type=comment`,
		`  note "Done." author=Charles Babbage date=2026-03-06T10:00:00.000 type=comment`,
		`  note "A second thread." author=Mary Somerville date=2026-03-07T08:00:00.000 type=comment`,
	)
}

func TestPowerPointFurniture(t *testing.T) {
	// The master has a line of its own at the top and one at the bottom;
	// the layout repeats the first and adds one.
	master := testMaster() + box(20, 36, 4, 300, 14, "", dpara("", plain("Analytical Ltd"))) +
		box(21, 36, 520, 300, 14, "", dpara("", plain("Confidential")))
	layout := testLayout() + box(20, 36, 4, 300, 14, "", dpara("", plain("Analytical Ltd"))) +
		box(22, 400, 4, 280, 14, "", dpara("", plain("Draft")))
	slide := ooxmltest.Shape(2, ph("title", -1), "", ooxmltest.TextBody(dpara("", plain("Plan")))) +
		ooxmltest.Shape(3, ph("ftr", 11), "", ooxmltest.TextBody(dpara("", plain("Board meeting")))) +
		ooxmltest.Shape(4, ph("sldNum", 12), "", ooxmltest.TextBody(dpara("", `<a:fld id="{1}" type="slidenum"><a:t>1</a:t></a:fld>`))) +
		ooxmltest.Shape(5, ph("dt", 10), "", ooxmltest.TextBody(dpara("", `<a:fld id="{2}" type="datetime1"><a:t>4 March 2026</a:t></a:fld>`)))
	pkg := ooxmltest.Pptx(
		ooxmltest.Slide{Shapes: slide},
		ooxmltest.Slide{Shapes: box(2, 100, 100, 200, 40, "", dpara("", plain("Second")))},
	).Master("", master, "", testTxStyles).Layout("", layout, "")
	// The second slide shows nothing of its layout and master.
	second := `<p:sld` + ooxmltest.PresNS + ` showMasterSp="0"><p:cSld><p:spTree>` + box(2, 100, 100, 200, 40, "", dpara("", plain("Second"))) + `</p:spTree></p:cSld></p:sld>`
	pkg.Add("ppt/slides/slide2.xml", ooxmltest.CTSlide, second)
	doc := powerpoint(t, pkg, nil)
	// The placeholders of the master and the layout say "Click to edit";
	// none of that is on a slide.
	expectLines(t, renderSlides(doc, true),
		`slide 1 "Plan"`,
		`  heading[1] "Plan" @36,20,684,100`,
		`  footer "4 March 2026" @36,500,196,530`,
		`  footer "Board meeting" @240,500,480,530`,
		`  footer "1" @524,500,684,530`,
		`  header "Analytical Ltd" @36,4,336,18`,
		`  footer "Confidential" @36,520,336,534`,
		`  header "Draft" @400,4,680,18`,
		`slide 2 ""`,
		`  paragraph "Second" @100,100,300,140`,
	)

	// A layout that hides its master's shapes: its own text is shown.
	pkg = ooxmltest.Pptx(ooxmltest.Slide{Shapes: box(2, 100, 100, 200, 40, "", dpara("", plain("On a layout that hides its master")))}).
		Master("", master, "", testTxStyles)
	pkg.Add("ppt/slideLayouts/slideLayout1.xml", ooxmltest.CTSlideLayout, `<p:sldLayout`+ooxmltest.PresNS+` showMasterSp="0"><p:cSld><p:spTree>`+layout+`</p:spTree></p:cSld></p:sldLayout>`)
	expectSlides(t, powerpoint(t, pkg, nil),
		`slide 1 ""`,
		`  paragraph "On a layout that hides its master"`,
		`  header "Analytical Ltd"`,
		`  header "Draft"`,
	)
}

func TestPowerPointSpansAndLinks(t *testing.T) {
	pkg := deck(ooxmltest.Slide{Shapes: box(2, 10, 10, 400, 100, "",
		dpara("", plain("Plain, "), drun(` b="1"`, "", "bold"), drun(` b="1" i="1"`, "", " and italic"), plain(", "),
			drun(` u="sng" strike="sngStrike"`, "", "struck"), drun(` baseline="30000"`, "", "2"), drun(` baseline="-25000"`, "", "n"), drun(` b="0" u="none"`, "", " off")),
		dpara("", plain("See "), drun("", `<a:hlinkClick r:id="rId7"/>`, "the report"), plain(" or "),
			drun("", `<a:hlinkClick r:id="" action="ppaction://hlinkshowjump?jump=nextslide"/>`, "the next slide")),
	) + ooxmltest.Shape(3, ph("", 1), "", `<p:txBody><a:bodyPr/><a:lstStyle><a:lvl1pPr><a:defRPr b="1"/></a:lvl1pPr></a:lstStyle>`+
		dpara("", plain("Bold from the list style"))+`</p:txBody>`)})
	for pkg.ExternalRel("ppt/slides/slide1.xml", ooxmltest.RelHyperlink, "https://example.com/report") != "rId7" {
	}
	doc := powerpoint(t, pkg, &gofastcore.Options{Spans: true})
	expectSlides(t, doc,
		`slide 1 ""`,
		`  paragraph "Plain, bold and italic, struck2n off" ["bold" bold] [" and italic" bold+italic] ["struck" underline+strike] ["2" super] ["n" sub]`,
		`  paragraph "See the report or the next slide" ["the report" ->https://example.com/report]`,
		`  list_item[1] <•> "Bold from the list style" ["Bold from the list style" bold]`,
	)
	// Without the option there are no spans, and an ordinary link is not
	// an external reference.
	doc = powerpoint(t, pkg, nil)
	for _, b := range doc.Parts[0].Blocks {
		if b.Spans != nil {
			t.Errorf("%q has spans %v", b.Text, b.Spans)
		}
	}
	if len(doc.Warnings) != 0 {
		t.Errorf("warnings: %v", doc.Warnings)
	}
}

func frame(id int, x, y, w, h float64, uri, data string) string {
	return fmt.Sprintf(`<p:graphicFrame><p:nvGraphicFramePr><p:cNvPr id="%d" name="Frame %d"/><p:cNvGraphicFramePr/><p:nvPr/></p:nvGraphicFramePr>%s`+
		`<a:graphic><a:graphicData uri="%s">%s</a:graphicData></a:graphic></p:graphicFrame>`,
		id, id, strings.Replace(strings.Replace(ooxmltest.Xfrm(x, y, w, h), "<a:xfrm>", "<p:xfrm>", 1), "</a:xfrm>", "</p:xfrm>", 1), uri, data)
}

func TestPowerPointContentThatIsNotRead(t *testing.T) {
	video := `<p:pic><p:nvPicPr><p:cNvPr id="9" name="Clip"><a:hlinkClick r:id="" action="ppaction://media"/></p:cNvPr><p:cNvPicPr/><p:nvPr><a:videoFile r:link="rId8"/></p:nvPr></p:nvPicPr>` +
		`<p:blipFill><a:blip r:embed="rId5"/></p:blipFill><p:spPr>` + ooxmltest.Xfrm(300, 300, 100, 80) + rectGeom + `</p:spPr></p:pic>`
	// An embedded workbook: what it looks like on the slide is a picture,
	// given in two forms of which one is read.
	ole := func(inner string) string {
		return `<p:oleObj name="Worksheet" r:id="rId6" progId="Excel.Sheet.12"><p:embed/>` + inner + `</p:oleObj>`
	}
	preview := picture(11, "rId5", "", 50, 300, 200, 100, "")
	pkg := deck(ooxmltest.Slide{Shapes: box(2, 10, 10, 200, 40, "", dpara("", plain("Figures"))) +
		video +
		frame(10, 50, 300, 200, 100, "http://schemas.openxmlformats.org/presentationml/2006/ole",
			`<mc:AlternateContent><mc:Choice Requires="v">`+ole(preview)+`</mc:Choice><mc:Fallback>`+ole(preview)+`</mc:Fallback></mc:AlternateContent>`) +
		// A shape in two forms.
		`<mc:AlternateContent><mc:Choice Requires="p14">` + box(12, 400, 400, 200, 40, "", dpara("", plain("The newer form"))) + `</mc:Choice>` +
		`<mc:Fallback>` + box(13, 400, 400, 200, 40, "", dpara("", plain("The older form"))) + `</mc:Fallback></mc:AlternateContent>` +
		// An action that starts a program.
		`<p:sp><p:nvSpPr><p:cNvPr id="14" name="Button"><a:hlinkClick r:id="" action="ppaction://program"/></p:cNvPr><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:spPr>` +
		ooxmltest.Xfrm(600, 480, 60, 30) + `</p:spPr>` + ooxmltest.TextBody(dpara("", plain("Run"))) + `</p:sp>`})
	pkg.AddBytes("ppt/media/image1.png", "image/png", pngBytes(t, 4, 3))
	for pkg.Rel("ppt/slides/slide1.xml", ooxmltest.RelImage, "../media/image1.png") != "rId5" {
	}
	pkg.Rel("ppt/slides/slide1.xml", ooxmltest.RelPackage, "../embeddings/Microsoft_Excel_Sheet1.xlsx")
	pkg.AddBytes("ppt/embeddings/Microsoft_Excel_Sheet1.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		ooxmltest.Xlsx(ooxmltest.Sheet{Name: "Inner", Data: xlRow(1, xlStr("A1", "inside the workbook"))}).Bytes())
	pkg.ExternalRel("ppt/slides/slide1.xml", "http://schemas.openxmlformats.org/officeDocument/2006/relationships/video", "file:///C:/clips/demo.mp4")

	doc := powerpoint(t, pkg, &gofastcore.Options{Images: gofastcore.ImagesMeta, Child: func(ctx context.Context, name, mime string, r io.ReaderAt, size int64, o *gofastcore.Options) (*gofastcore.Document, error) {
		return gofastooxml.Extract(ctx, r, size, o)
	}})
	expectSlides(t, doc,
		`slide 1 ""`,
		`  paragraph "Figures"`,
		`  image image(ppt/media/image1.png image/png 4x3 0 bytes)`,
		`  image image(ppt/media/image1.png image/png 4x3 0 bytes)`,
		`  paragraph "The newer form"`,
		`  paragraph "Run"`,
	)
	var codes []string
	for _, w := range doc.Warnings {
		codes = append(codes, w.Code+": "+w.Message)
	}
	want := []string{
		"input.active_content: 1 actions that start a program or run a macro; none was run",
		"input.external_reference: 1 links to pictures, media or files outside the presentation; nothing was fetched",
		"ooxml.unread_content: 1 audio or video clip",
	}
	if strings.Join(codes, "\n") != strings.Join(want, "\n") {
		t.Errorf("warnings:\n %s\nwant:\n %s", strings.Join(codes, "\n "), strings.Join(want, "\n "))
	}
	if len(doc.Children) != 1 || doc.Children[0].Source.Name != "Microsoft_Excel_Sheet1.xlsx" || !strings.Contains(doc.Children[0].Text(), "inside the workbook") {
		t.Errorf("children: %+v", doc.Children)
	}
}

func TestPowerPointSlideSelectionAndLimits(t *testing.T) {
	var slides []ooxmltest.Slide
	for i := 1; i <= 6; i++ {
		slides = append(slides, ooxmltest.Slide{Shapes: box(2, 10, 10, 300, 40, "", dpara("", plain(fmt.Sprintf("Slide %d", i)))), Hidden: i == 3})
	}
	data := deck(slides...).Bytes()

	doc, err := extract(t, data, &gofastcore.Options{Parts: gofastcore.PartRange{First: 2, Last: 3}})
	if err != nil {
		t.Fatal(err)
	}
	expectSlides(t, doc,
		`slide 2 ""`, `  paragraph "Slide 2"`,
		`slide 3 "" display=hidden`, `  paragraph "Slide 3" display=hidden`,
	)

	if _, err := extract(t, data, &gofastcore.Options{Parts: gofastcore.PartRange{First: 9, Last: 12}}); !errors.Is(err, gofastcore.ErrNoPartsSelected) {
		t.Errorf("selecting slides that are not there: %v", err)
	}

	doc, err = extract(t, data, &gofastcore.Options{Limits: gofastcore.Limits{MaxParts: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Parts) != 2 || !warned(doc, gofastcore.WarnLimitExceeded) {
		t.Errorf("%d parts, warnings %v", len(doc.Parts), doc.Warnings)
	}
}

// What a limit leaves out does not depend on how many workers read the
// slides.
func TestPowerPointLimitsAreTheSameForAnyWorkers(t *testing.T) {
	var slides []ooxmltest.Slide
	for i := 1; i <= 40; i++ {
		var rows []string
		for r := 0; r < 5; r++ {
			rows = append(rows, trow(tc(fmt.Sprintf("slide %d row %d", i, r)), tc(strings.Repeat("x", 40*i))))
		}
		slides = append(slides, ooxmltest.Slide{Shapes: box(2, 10, 10, 300, 40, "", dpara("", plain(strings.Repeat("word ", 30*i)))) +
			tableFrame(3, 10, 100, 600, 200, `<a:tblPr/>`, rows...) + picture(4, "rId5", "", 10, 320, 50, 50, "")})
	}
	// Every slide shows the same picture, whose bytes are asked for.
	pkg := deck(slides...)
	pkg.AddBytes("ppt/media/image1.png", "image/png", pngBytes(t, 40, 30))
	for i := range slides {
		for pkg.Rel(fmt.Sprintf("ppt/slides/slide%d.xml", i+1), ooxmltest.RelImage, "../media/image1.png") != "rId5" {
		}
	}
	data := pkg.Bytes()
	for name, e := range map[string]struct {
		extractor gofastooxml.Extractor
		limits    gofastcore.Limits
	}{
		"decompressed size": {limits: gofastcore.Limits{MaxDecodedBytes: 150_000}},
		"table cells":       {extractor: gofastooxml.Extractor{MaxCells: 133}},
	} {
		var want string
		for _, workers := range []int{1, 2, 7, 64} {
			doc, err := e.extractor.Extract(t.Context(), bytes.NewReader(data), int64(len(data)), &gofastcore.Options{Workers: workers, Limits: e.limits, Images: gofastcore.ImagesData})
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			ooxmltest.CheckDocument(t, doc)
			var sb strings.Builder
			limited := 0
			for _, p := range doc.Parts {
				fmt.Fprintf(&sb, "%d %v\n", p.Number, p.Warnings)
				if p.Has(gofastcore.WarnLimitExceeded) || p.Has(gofastcore.WarnPartFailed) {
					limited++
				}
				for _, b := range p.Blocks {
					if b.Image != nil && len(b.Image.Data) > 0 {
						fmt.Fprintf(&sb, "picture bytes on slide %d\n", p.Number)
					}
				}
			}
			got := sb.String() + strings.Join(renderSlides(doc, false), "\n")
			if limited == 0 || limited == len(doc.Parts) {
				t.Errorf("%s: %d of %d slides cut short; the test wants some", name, limited, len(doc.Parts))
			}
			if workers == 1 {
				want = got
			} else if got != want {
				t.Errorf("%s: with %d workers the result differs from that of one", name, workers)
			}
		}
	}
}

func TestPowerPointWorkersGiveTheSameResult(t *testing.T) {
	var slides []ooxmltest.Slide
	for i := 1; i <= 30; i++ {
		slides = append(slides, ooxmltest.Slide{
			Shapes: ooxmltest.Shape(2, ph("title", -1), "", ooxmltest.TextBody(dpara("", plain(fmt.Sprintf("Slide %d", i))))) +
				ooxmltest.Shape(3, ph("", 1), "", ooxmltest.TextBody(dpara("", plain("first point")), dpara(`<a:pPr lvl="1"/>`, plain(fmt.Sprintf("detail %d", i))))),
			Notes:  noteShapes(dpara("", plain(fmt.Sprintf("notes for %d", i)))),
			Hidden: i%7 == 0,
		})
	}
	data := deck(slides...).Bytes()
	one, err := extract(t, data, &gofastcore.Options{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join(renderSlides(one, true), "\n")
	for _, workers := range []int{0, 2, 5, 64} {
		doc, err := extract(t, data, &gofastcore.Options{Workers: workers})
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(renderSlides(doc, true), "\n"); got != want {
			t.Errorf("with %d workers the result differs", workers)
		}
	}
}

func TestPowerPointDamagedAndMissingParts(t *testing.T) {
	pkg := deck(
		ooxmltest.Slide{Shapes: box(2, 10, 10, 300, 40, "", dpara("", plain("Sound")))},
		ooxmltest.Slide{},
		ooxmltest.Slide{Shapes: box(2, 10, 10, 300, 40, "", dpara("", plain("Sound too")))},
		ooxmltest.Slide{Shapes: box(2, 10, 10, 300, 40, "", dpara("", plain("With notes that are cut off"))), Notes: noteShapes(dpara("", plain("x")))},
	)
	// The second slide breaks off after its first shape.
	pkg.Add("ppt/slides/slide2.xml", ooxmltest.CTSlide, `<p:sld`+ooxmltest.PresNS+`><p:cSld><p:spTree>`+
		box(2, 10, 10, 300, 40, "", dpara("", plain("Read before the damage")))+`<p:sp><p:txBody><a:p><a:r><a:t>cut of`)
	pkg.Add("ppt/notesSlides/notesSlide4.xml", ooxmltest.CTNotesSlide, `<p:notes`+ooxmltest.PresNS+`><p:cSld><p:spTree>`+
		ooxmltest.Shape(3, ph("body", 3), "", ooxmltest.TextBody(dpara("", plain("The notes begin"))))+`<p:sp>`)
	// The presentation lists a slide whose part is missing, and one it
	// gives no part for.
	gone := pkg.Rel("ppt/presentation.xml", ooxmltest.RelSlide, "slides/gone.xml")
	pkg.Add("ppt/presentation.xml", ooxmltest.CTPresentation, `<p:presentation`+ooxmltest.PresNS+`><p:sldIdLst>`+
		`<p:sldId id="256" r:id="rId2"/><p:sldId id="257" r:id="rId3"/><p:sldId id="258" r:id="rId4"/><p:sldId id="259" r:id="rId5"/>`+
		`<p:sldId id="260" r:id="`+gone+`"/><p:sldId id="261" r:id="rId99"/></p:sldIdLst><p:sldSz cx="9144000" cy="6858000"/></p:presentation>`)
	doc := powerpoint(t, pkg, nil)
	expectSlides(t, doc,
		`slide 1 ""`, `  paragraph "Sound"`,
		`slide 2 ""`, `  paragraph "Read before the damage"`, `  paragraph "cut of"`,
		`slide 3 ""`, `  paragraph "Sound too"`,
		`slide 4 ""`, `  paragraph "With notes that are cut off"`, `  note "The notes begin" type=speaker`,
		`slide 5 ""`,
		`slide 6 ""`,
	)
	for part, want := range map[int]bool{0: false, 1: true, 2: false, 3: false, 4: true, 5: true} {
		if got := slideWarned(doc, part, gofastcore.WarnPartFailed); got != want {
			t.Errorf("slide %d: failed = %v, want %v (%v)", part+1, got, want, doc.Parts[part].Warnings)
		}
	}
	damaged := ""
	for _, w := range doc.Warnings {
		if w.Code == gofastooxml.WarnPartDamaged {
			damaged += w.Message + ";"
		}
	}
	if damaged != "ppt/notesSlides/notesSlide4.xml;" {
		t.Errorf("damaged parts: %q", damaged)
	}

	// A presentation with no slides, as a template may be, is one empty
	// part, and that is not a failure.
	empty := powerpoint(t, deck(), nil)
	if len(empty.Parts) != 1 || !slideWarned(empty, 0, gofastcore.WarnPartNoText) || slideWarned(empty, 0, gofastcore.WarnPartFailed) || len(empty.Warnings) != 0 {
		t.Errorf("no slides: %+v, %v", empty.Parts, empty.Warnings)
	}
}

// A picture on a master or a layout, such as a logo, is on every slide
// that shows it, as their text is; the empty frame of a picture
// placeholder is not. Its bytes are given once.
func TestPowerPointPicturesOnLayoutsAndMasters(t *testing.T) {
	slides := make([]ooxmltest.Slide, 3)
	for i := range slides {
		slides[i].Shapes = ooxmltest.Shape(2, ph("title", -1), "", ooxmltest.TextBody(dpara("", plain(fmt.Sprintf("Slide %d", i+1)))))
	}
	pkg := deck(slides...)
	pkg.Master("", testMaster()+picture(40, "rId9", "Company logo", 600, 10, 100, 40, ""), "", testTxStyles)
	for pkg.Rel("ppt/slideMasters/slideMaster1.xml", ooxmltest.RelImage, "../media/logo.png") != "rId9" {
	}
	pkg.Layout("", picture(41, "rId9", "Banner", 10, 500, 700, 30, "")+
		// A placeholder for a picture, with nothing in it.
		`<p:pic><p:nvPicPr><p:cNvPr id="42" name="Picture Placeholder"/><p:cNvPicPr/><p:nvPr><p:ph type="pic" idx="1"/></p:nvPr></p:nvPicPr><p:blipFill><a:blip r:embed="rId9"/></p:blipFill><p:spPr/></p:pic>`, "")
	for pkg.Rel("ppt/slideLayouts/slideLayout1.xml", ooxmltest.RelImage, "../media/banner.png") != "rId9" {
	}
	pkg.AddBytes("ppt/media/logo.png", "image/png", pngBytes(t, 3, 2))
	pkg.AddBytes("ppt/media/banner.png", "image/png", pngBytes(t, 4, 1))
	doc := powerpoint(t, pkg, &gofastcore.Options{Images: gofastcore.ImagesData})
	withData := map[string]int{}
	for _, part := range doc.Parts {
		var alts []string
		for _, b := range part.Blocks {
			if b.Kind == gofastcore.KindImage {
				alts = append(alts, b.Text)
				if b.Image.Data != nil {
					withData[b.Text]++
				}
			}
		}
		if strings.Join(alts, "|") != "Company logo|Banner" {
			t.Errorf("slide %d: pictures %q", part.Number, alts)
		}
	}
	if withData["Company logo"] != 1 || withData["Banner"] != 1 {
		t.Errorf("bytes given %v times; want once each", withData)
	}
	ooxmltest.CheckDocument(t, doc)
}

// The notes master's header and footer, and its own text, are on every
// slide that has notes, as furniture; the handout master's, once, with
// the first slide. The frames the pages fill in, the date and the page
// number are not read, and a header or footer the master switches off
// is declared hidden.
func TestPowerPointNotesAndHandoutMasters(t *testing.T) {
	build := func(footer string) *ooxmltest.Package {
		slides := []ooxmltest.Slide{
			{Shapes: ooxmltest.Shape(2, ph("title", -1), "", ooxmltest.TextBody(dpara("", plain("One")))), Notes: noteShapes(dpara("", plain("Say one.")))},
			{Shapes: ooxmltest.Shape(2, ph("title", -1), "", ooxmltest.TextBody(dpara("", plain("Two"))))},
			// Notes with no header of their own: the master's is shown.
			{Shapes: ooxmltest.Shape(2, ph("title", -1), "", ooxmltest.TextBody(dpara("", plain("Three")))), Notes: ooxmltest.Shape(3, ph("body", 3), "", ooxmltest.TextBody(dpara("", plain("Say three."))))},
		}
		pkg := deck(slides...)
		const presNS = ` xmlns:a="` + ooxmltest.NSDraw + `" xmlns:r="` + ooxmltest.NSRel + `" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"`
		tree := func(shapes string) string {
			return `<p:cSld><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr/>` + shapes + `</p:spTree></p:cSld>`
		}
		at := func(y float64) string { return ooxmltest.Xfrm(10, y, 300, 20) }
		pkg.Add("ppt/notesMasters/notesMaster1.xml", "application/vnd.openxmlformats-officedocument.presentationml.notesMaster+xml",
			`<p:notesMaster`+presNS+`>`+tree(
				ooxmltest.Shape(2, ph("hdr", 1), at(0), ooxmltest.TextBody(dpara("", plain("Board pack"))))+
					ooxmltest.Shape(3, ph("dt", 2), at(0), ooxmltest.TextBody(dpara("", plain("19/11/2026"))))+
					ooxmltest.Shape(4, ph("body", 3), at(300), ooxmltest.TextBody(dpara("", plain("Click to edit Master text styles"))))+
					ooxmltest.Shape(5, ph("ftr", 4), at(700), ooxmltest.TextBody(dpara("", plain(footer))))+
					ooxmltest.Shape(6, ph("sldNum", 5), at(700), ooxmltest.TextBody(dpara("", plain("‹#›"))))+
					// Text of the master's own, near the top of the page.
					ooxmltest.Shape(7, "", at(20), ooxmltest.TextBody(dpara("", plain("Draft")))),
			)+`<p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/><p:hf ftr="0" dt="0"/></p:notesMaster>`)
		pkg.Rel("ppt/presentation.xml", "http://schemas.openxmlformats.org/officeDocument/2006/relationships/notesMaster", "notesMasters/notesMaster1.xml")
		pkg.Add("ppt/handoutMasters/handoutMaster1.xml", "application/vnd.openxmlformats-officedocument.presentationml.handoutMaster+xml",
			`<p:handoutMaster`+presNS+`>`+tree(ooxmltest.Shape(2, ph("ftr", 1), at(700), ooxmltest.TextBody(dpara("", plain("Handed out at the meeting")))))+
				`<p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/></p:handoutMaster>`)
		pkg.Rel("ppt/presentation.xml", "http://schemas.openxmlformats.org/officeDocument/2006/relationships/handoutMaster", "handoutMasters/handoutMaster1.xml")
		return pkg
	}
	doc := powerpoint(t, build("Confidential"), nil)
	for _, part := range doc.Parts {
		var got []string
		for _, b := range part.Blocks {
			line := b.Kind.String() + " " + b.Text
			if b.Display != 0 {
				line += " display=" + b.Display.String()
			}
			got = append(got, line)
		}
		var want []string
		switch part.Number {
		case 1:
			// The notes page has a header of its own (noteShapes), which
			// takes the place of the master's. The master switches its
			// footer off (hf ftr="0"): it is declared hidden.
			want = []string{"heading One", "note Say one.", "header A header of the notes page", "footer Confidential display=hidden", "header Draft", "footer Handed out at the meeting"}
		case 2:
			want = []string{"heading Two"}
		case 3:
			want = []string{"heading Three", "note Say three.", "header Board pack", "footer Confidential display=hidden", "header Draft"}
		}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("slide %d:\n got %q\nwant %q", part.Number, got, want)
		}
	}
	ooxmltest.CheckDocument(t, doc)
}
