package gofastooxml_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// Presentations made to be expensive or to break the arithmetic. Each
// is read quickly into a valid document that says what was left out.

func TestPowerPointDeeplyNestedGroups(t *testing.T) {
	const depth = 2000
	shapes := strings.Repeat(`<p:grpSp><p:nvGrpSpPr><p:cNvPr id="2" name="g"/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr/>`, depth) +
		box(3, 10, 10, 100, 40, "", dpara("", plain("at the bottom"))) + strings.Repeat(`</p:grpSp>`, depth)
	data := deck(ooxmltest.Slide{Shapes: box(2, 10, 100, 100, 40, "", dpara("", plain("before the groups"))) + shapes},
		ooxmltest.Slide{Shapes: box(2, 10, 10, 100, 40, "", dpara("", plain("the next slide")))}).Bytes()
	within(t, 5*time.Second, "groups nested 2,000 deep", func() {
		doc, err := extract(t, data, nil)
		if err != nil {
			t.Error(err)
			return
		}
		ooxmltest.CheckDocument(t, doc)
		expectSlides(t, doc, `slide 1 ""`, `  paragraph "before the groups"`, `slide 2 ""`, `  paragraph "the next slide"`)
		if !slideWarned(doc, 0, gofastcore.WarnLimitExceeded) || slideWarned(doc, 1, gofastcore.WarnLimitExceeded) {
			t.Errorf("warnings: %v, %v", doc.Parts[0].Warnings, doc.Parts[1].Warnings)
		}
	})
}

// The text of a master is on every slide that uses it. A long master and
// many slides ask for far more than the file holds; what is repeated is
// bounded, the slides past the bound say so, and which they are does not
// depend on the number of workers.
func TestPowerPointRepeatedMasterText(t *testing.T) {
	var master strings.Builder
	const texts, slides = 3000, 400
	for i := 0; i < texts; i++ {
		master.WriteString(box(100+i, float64(i%700), float64(i%500), 20, 10, "", dpara("", plain(fmt.Sprintf("m%d", i)))))
	}
	list := make([]ooxmltest.Slide, slides)
	for i := range list {
		list[i].Shapes = box(2, 10, 10, 100, 40, "", dpara("", plain(fmt.Sprintf("slide %d", i+1))))
	}
	data := ooxmltest.Pptx(list...).Master("", master.String(), "", "").Bytes()
	var want string
	for _, workers := range []int{1, 8} {
		within(t, 20*time.Second, "a long master on many slides", func() {
			doc, err := extract(t, data, &gofastcore.Options{Workers: workers})
			if err != nil {
				t.Error(err)
				return
			}
			var sb strings.Builder
			full, cut := 0, 0
			for _, p := range doc.Parts {
				switch {
				case len(p.Blocks) == texts+1 && !p.Has(gofastcore.WarnLimitExceeded):
					full++
				case len(p.Blocks) == 1 && p.Has(gofastcore.WarnLimitExceeded):
					cut++
				default:
					t.Errorf("slide %d: %d blocks, warnings %v", p.Number, len(p.Blocks), p.Warnings)
				}
				fmt.Fprintf(&sb, "%d:%d ", p.Number, len(p.Blocks))
			}
			if full != (1<<20)/texts || cut != slides-full {
				t.Errorf("%d workers: %d slides with the master's text, %d without", workers, full, cut)
			}
			if workers == 1 {
				want = sb.String()
			} else if sb.String() != want {
				t.Errorf("with %d workers the slides that are cut differ", workers)
			}
		})
	}
}

func TestPowerPointNumbersThatDoNotFit(t *testing.T) {
	huge := func(id int, x, y, w, h, text string) string {
		return ooxmltest.Shape(id, "", `<a:xfrm rot="99999999999999"><a:off x="`+x+`" y="`+y+`"/><a:ext cx="`+w+`" cy="`+h+`"/></a:xfrm>`, ooxmltest.TextBody(dpara("", plain(text))))
	}
	group := func(inside string) string {
		return `<p:grpSp><p:nvGrpSpPr><p:cNvPr id="9" name="g"/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="999999999999999" cy="999999999999999"/>` +
			`<a:chOff x="0" y="0"/><a:chExt cx="1" cy="1"/></a:xfrm></p:grpSpPr>` + inside + `</p:grpSp>`
	}
	nested := box(3, 10, 10, 100, 40, "", dpara("", plain("scaled out of sight")))
	for i := 0; i < 30; i++ {
		nested = group(nested)
	}
	doc := powerpoint(t, deck(ooxmltest.Slide{Shapes: huge(2, "999999999999999", "-999999999999999", "999999999999999", "999999999999999", "far away") +
		huge(4, "99999999999999999999", "1", "1", "1", "a number too long to be one") +
		huge(5, "12700", "12700", "-5", "12700", "a negative width") + nested +
		// A table whose cells claim to span more than there is.
		tableFrame(6, 10, 10, 100, 100, `<a:tblPr/>`, trow(tcell(` gridSpan="999999999" rowSpan="999999999"`, "", dpara("", plain("wide"))), tc("b")), trow(tc("c"), tc("d"))) +
		`<p:sp><p:nvSpPr><p:cNvPr id="7" name="x"/><p:cNvSpPr/><p:nvPr><p:ph type="body" idx="99999999999"/></p:nvPr></p:nvSpPr><p:spPr/>` +
		`<p:txBody><a:bodyPr tIns="-99999999999999" bIns="999999999999999"><a:normAutofit fontScale="99999999999"/></a:bodyPr>` +
		dpara(`<a:pPr lvl="99999"><a:buAutoNum type="arabicPeriod" startAt="99999999999"/></a:pPr>`, drun(` sz="99999999999" baseline="99999999999999"`, "", "sizes that are not sizes")) + `</p:txBody></p:sp>`}), nil)
	expectLines(t, renderSlides(doc, true),
		`slide 1 ""`,
		`  table [["wide" "b"] ["c" "d"]] merges=[{0 0 2 2}] @10,10,110,110`,
		`  list_item[9] <1.> "sizes that are not sizes" @36,120,684,480`,
		`  paragraph "far away"`,
		`  paragraph "a number too long to be one"`,
		`  paragraph "a negative width"`,
		`  paragraph "scaled out of sight"`,
	)
}

// Parts that name each other in a circle, or name the wrong kind of part.
func TestPowerPointPartsThatReferToEachOther(t *testing.T) {
	pkg := deck(ooxmltest.Slide{Shapes: ooxmltest.Shape(2, ph("title", -1), "", ooxmltest.TextBody(dpara("", plain("Title"))))},
		ooxmltest.Slide{Shapes: box(2, 10, 10, 100, 40, "", dpara("", plain("Second")))})
	const layout, master = "ppt/slideLayouts/slideLayout1.xml", "ppt/slideMasters/slideMaster1.xml"
	// The layout names itself and a slide as its master, the master names
	// itself as a layout and its master, and a slide names itself as its
	// layout and its notes.
	pkg.Rel(layout, ooxmltest.RelSlideMaster, "slideLayout1.xml")
	pkg.Rel(layout, ooxmltest.RelSlideLayout, "slideLayout1.xml")
	pkg.Rel(master, ooxmltest.RelSlideMaster, "slideMaster1.xml")
	pkg.Rel(master, ooxmltest.RelSlideLayout, "slideMaster1.xml")
	pkg.Rel("ppt/slides/slide2.xml", ooxmltest.RelNotesSlide, "slide2.xml")
	pkg.Rel("ppt/slides/slide2.xml", relBase+"comments", "slide2.xml")
	within(t, 5*time.Second, "parts that refer to each other", func() {
		doc := powerpoint(t, pkg, nil)
		expectSlides(t, doc, `slide 1 "Title"`, `  heading[1] "Title"`, `slide 2 ""`, `  paragraph "Second"`)
	})

	// A slide whose layout is the slide itself.
	pkg = ooxmltest.NewPackage()
	pkg.Rel("", ooxmltest.RelOfficeDoc, "ppt/presentation.xml")
	id := pkg.Rel("ppt/presentation.xml", ooxmltest.RelSlide, "slides/slide1.xml")
	pkg.Add("ppt/presentation.xml", ooxmltest.CTPresentation, `<p:presentation`+ooxmltest.PresNS+`><p:sldIdLst><p:sldId id="256" r:id="`+id+`"/></p:sldIdLst></p:presentation>`)
	pkg.Rel("ppt/slides/slide1.xml", ooxmltest.RelSlideLayout, "slide1.xml")
	pkg.Rel("ppt/slides/slide1.xml", ooxmltest.RelSlideMaster, "slide1.xml")
	pkg.Add("ppt/slides/slide1.xml", ooxmltest.CTSlide, `<p:sld`+ooxmltest.PresNS+`><p:cSld><p:spTree>`+
		ooxmltest.Shape(2, ph("title", -1), ooxmltest.Xfrm(10, 10, 100, 40), ooxmltest.TextBody(dpara("", plain("Its own layout"))))+
		box(3, 10, 100, 100, 40, "", dpara("", plain("and its own master")))+`</p:spTree></p:cSld></p:sld>`)
	within(t, 5*time.Second, "a slide that is its own layout", func() {
		doc := powerpoint(t, pkg, nil)
		// With no slide size the place of a block is still given, and
		// nothing is beside the slide. What the slide says as its own
		// layout is said once more, as the layout's text.
		expectSlides(t, doc, `slide 1 "Its own layout"`, `  heading[1] "Its own layout"`, `  paragraph "and its own master"`, `  footer "and its own master"`)
		if doc.Parts[0].Width != 0 {
			t.Errorf("width %v", doc.Parts[0].Width)
		}
	})
}

func TestPowerPointManyCommentsDeep(t *testing.T) {
	var sb strings.Builder
	const depth = 5000
	sb.WriteString(`<p188:cmLst` + ooxmltest.PresNS + `>`)
	for i := 0; i < depth; i++ {
		sb.WriteString(`<p188:cm id="{A}" authorId="x"><p188:replyLst><p188:reply id="{B}" authorId="x">`)
	}
	sb.WriteString(`<p188:txBody><a:p><a:r><a:t>the innermost</a:t></a:r></a:p></p188:txBody>`)
	pkg := deck(ooxmltest.Slide{Shapes: box(2, 10, 10, 100, 40, "", dpara("", plain("Text")))})
	pkg.Rel("ppt/slides/slide1.xml", "http://schemas.microsoft.com/office/2018/10/relationships/comments", "../comments/modernComment_1.xml")
	pkg.Add("ppt/comments/modernComment_1.xml", "application/vnd.ms-powerpoint.comments+xml", sb.String())
	within(t, 5*time.Second, "comments nested 10,000 deep", func() {
		doc := powerpoint(t, pkg, nil)
		if len(doc.Parts[0].Blocks) == 0 || doc.Parts[0].Blocks[0].Text != "Text" {
			t.Errorf("blocks: %v", doc.Parts[0].Blocks)
		}
	})
}
