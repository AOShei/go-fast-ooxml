package gofastooxml_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// pngOf returns a PNG of the given size in one colour; with split, its
// right half is in the other.
func pngOf(t testing.TB, c, other color.RGBA, split bool) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 40; x++ {
			if split && x >= 20 {
				img.Set(x, y, other)
			} else {
				img.Set(x, y, c)
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

var (
	pale = color.RGBA{0xF8, 0xF8, 0xF8, 0xFF}
	dark = color.RGBA{0x10, 0x10, 0x10, 0xFF}
)

// inkPara is a paragraph of text in the given colour.
func inkPara(rgb, text string) string {
	return `<a:p><a:r><a:rPr lang="en-US"><a:solidFill><a:srgbClr val="` + rgb + `"/></a:solidFill></a:rPr><a:t>` + text + `</a:t></a:r></a:p>`
}

func inkBox(id int, x, y, w, h float64, fill, para string) string {
	return ooxmltest.Shape(id, "", ooxmltest.Xfrm(x, y, w, h)+`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom>`+fill, ooxmltest.TextBody(para))
}

// With Options.DeepHidden, text on a slide is judged against what lies
// under it where that is not one colour: a gradient's stops, a picture's
// colours under the text. Text lost against nearly all of it is
// low_contrast; without the option, none of it is judged.
func TestPowerPointDeepHidden(t *testing.T) {
	const img = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/image"
	gradient := `<a:gradFill><a:gsLst><a:gs pos="0"><a:srgbClr val="000000"/></a:gs><a:gs pos="100000"><a:srgbClr val="141414"/></a:gs></a:gsLst></a:gradFill>`
	shapes := inkBox(2, 20, 20, 300, 40, "", inkPara("FFFFFF", "White on a pale picture background")) +
		inkBox(3, 20, 80, 300, 40, gradient, inkPara("000000", "Black on a dark gradient")) +
		inkBox(4, 20, 140, 300, 40, gradient, inkPara("FFFFFF", "White on a dark gradient")) +
		picture(5, "rIdDark", "dark", 350, 20, 300, 200, "") +
		inkBox(6, 360, 30, 100, 40, "", inkPara("000000", "Black on a dark picture")) +
		inkBox(7, 360, 90, 100, 40, "", inkPara("FFFFFF", "White on a dark picture")) +
		inkBox(8, 20, 200, 300, 40, `<a:blipFill><a:blip r:embed="rIdDark"/><a:stretch><a:fillRect/></a:stretch></a:blipFill>`, inkPara("000000", "Black on its own dark picture")) +
		picture(9, "rIdSplit", "split", 350, 300, 300, 100, "") +
		inkBox(10, 360, 310, 280, 40, "", inkPara("000000", "Black on a picture half dark"))
	bg := `<p:bg><p:bgPr><a:blipFill><a:blip r:embed="rIdPale"/><a:stretch><a:fillRect/></a:stretch></a:blipFill><a:effectLst/></p:bgPr></p:bg>`
	pkg := ooxmltest.Pptx(ooxmltest.Slide{Shapes: shapes, Background: bg})
	for id, data := range map[string][]byte{"rIdPale": pngOf(t, pale, pale, false), "rIdDark": pngOf(t, dark, dark, false), "rIdSplit": pngOf(t, dark, pale, true)} {
		name := "media/" + id + ".png"
		pkg.AddBytes("ppt/"+name, "image/png", data)
		pkg.RelID("ppt/slides/slide1.xml", id, img, "../"+name)
	}
	deep := powerpoint(t, pkg, &gofastcore.Options{DeepHidden: true})
	flagged := map[string]bool{}
	for _, b := range deep.Parts[0].Blocks {
		if b.Kind != gofastcore.KindImage {
			flagged[b.Text] = b.Hidden&gofastcore.HiddenLowContrast != 0
		}
	}
	want := map[string]bool{
		"White on a pale picture background": true,
		"Black on a dark gradient":           true,
		"White on a dark gradient":           false,
		"Black on a dark picture":            true,
		"White on a dark picture":            false,
		"Black on its own dark picture":      true,
		"Black on a picture half dark":       false,
	}
	for text, w := range want {
		if got, ok := flagged[text]; !ok || got != w {
			t.Errorf("%q: low_contrast %v, want %v (found %v)", text, got, w, ok)
		}
	}
	for _, b := range powerpoint(t, pkg, nil).Parts[0].Blocks {
		if b.Hidden&gofastcore.HiddenLowContrast != 0 {
			t.Errorf("without DeepHidden, %q is flagged", b.Text)
		}
	}
}

// A cell with a gradient fill is judged against its stops with
// DeepHidden.
func TestExcelDeepHidden(t *testing.T) {
	styles := xlStyles(nil, []string{`<sz val="11"/><color rgb="FFFFFFFF"/>`},
		[]string{`<gradientFill degree="90"><stop position="0"><color rgb="FFFFFFFF"/></stop><stop position="1"><color rgb="FFF4F4F4"/></stop></gradientFill>`},
		`numFmtId="0" fontId="0" fillId="0"`, `numFmtId="0" fontId="1" fillId="2"`)
	pkg := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "G", Data: xlRow(1, xlStyledStr("A1", 1, "white on a pale gradient"), xlStr("B1", "plain"))}).WithSheetStyles(styles)
	expectSheets(t, excel(t, pkg, &gofastcore.Options{DeepHidden: true}),
		`sheet 1 "G"`, `  table A1:B1 inferred hidden=low_contrast [["white on a pale gradient" "plain"]] hidden(0,0 1x1 low_contrast)`)
	expectSheets(t, excel(t, pkg, nil),
		`sheet 1 "G"`, `  table A1:B1 inferred [["white on a pale gradient" "plain"]]`)
}

// In Word, the text of a text box with a gradient or picture fill, and
// text on a page whose background is a picture, with DeepHidden.
func TestWordDeepHidden(t *testing.T) {
	const img = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/image"
	white := func(s string) string {
		return `<w:p><w:r><w:rPr><w:color w:val="FFFFFF"/></w:rPr><w:t>` + s + `</w:t></w:r></w:p>`
	}
	textBox := func(fill, text string) string {
		return `<w:p><w:r><w:drawing><wp:inline><wp:extent cx="2540000" cy="508000"/><wp:docPr id="1" name="Box"/><a:graphic xmlns:a="` + ooxmltest.NSDraw + `"><a:graphicData uri="x">` +
			`<wps:wsp xmlns:wps="http://schemas.microsoft.com/office/word/2010/wordprocessingShape"><wps:spPr>` + fill + `</wps:spPr><wps:txbx><w:txbxContent>` + white(text) +
			`</w:txbxContent></wps:txbx><wps:bodyPr/></wps:wsp></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>`
	}
	gradient := `<a:gradFill xmlns:a="` + ooxmltest.NSDraw + `"><a:gsLst><a:gs pos="0"><a:srgbClr val="FFFFFF"/></a:gs><a:gs pos="100000"><a:srgbClr val="F2F2F2"/></a:gs></a:gsLst></a:gradFill>`
	body := white("White on a pale picture page") + textBox(gradient, "White in a pale gradient box")
	pkg := ooxmltest.Docx(body)
	pkg.Add("word/document.xml", ooxmltest.CTDocument, `<w:document`+ooxmltest.WordNS+` xmlns:v="urn:schemas-microsoft-com:vml"><w:background w:color="FFFFFF"><v:background id="b"><v:fill r:id="rIdPale" type="frame"/></v:background></w:background><w:body>`+
		body+`<w:sectPr><w:pgSz w:w="12240" w:h="15840"/></w:sectPr></w:body></w:document>`)
	pkg.AddBytes("word/media/pale.png", "image/png", pngOf(t, pale, pale, false))
	pkg.RelID("word/document.xml", "rIdPale", img, "media/pale.png")
	pkg.WordPart("settings.xml", ooxmltest.CTSettings, ooxmltest.RelSettings, "settings", `<w:displayBackgroundShape/>`)
	expectBlocks(t, word(t, pkg, &gofastcore.Options{DeepHidden: true}),
		`paragraph "White on a pale picture page" hidden=low_contrast`,
		`paragraph "White in a pale gradient box" hidden=low_contrast`,
	)
	expectBlocks(t, word(t, pkg, nil),
		`paragraph "White on a pale picture page"`,
		`paragraph "White in a pale gradient box"`,
	)
}
