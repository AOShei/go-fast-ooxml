package dml

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core/xmlscan"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/theme"
)

const nsDraw = "http://schemas.openxmlformats.org/drawingml/2006/main"

const themeXML = `<a:theme xmlns:a="` + nsDraw + `"><a:themeElements><a:clrScheme name="t">` +
	`<a:dk1><a:sysClr val="windowText" lastClr="000000"/></a:dk1><a:lt1><a:sysClr val="window" lastClr="FFFFFF"/></a:lt1>` +
	`<a:dk2><a:srgbClr val="44546A"/></a:dk2><a:lt2><a:srgbClr val="E7E6E6"/></a:lt2><a:accent1><a:srgbClr val="4472C4"/></a:accent1>` +
	`<a:accent2><a:srgbClr val="ED7D31"/></a:accent2><a:hlink><a:srgbClr val="0563C1"/></a:hlink></a:clrScheme>` +
	`<a:fmtScheme><a:fillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill>` +
	`<a:gradFill><a:gsLst><a:gs pos="0"><a:schemeClr val="phClr"/></a:gs></a:gsLst></a:gradFill>` +
	`<a:solidFill><a:schemeClr val="phClr"><a:tint val="50000"/></a:schemeClr></a:solidFill></a:fillStyleLst>` +
	`<a:lnStyleLst><a:ln><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln></a:lnStyleLst>` +
	`<a:bgFillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:blipFill/></a:bgFillStyleLst></a:fmtScheme></a:themeElements></a:theme>`

func scan(xml string) *xmlscan.Scanner {
	s := xmlscan.NewBytes([]byte(xml), &xmlscan.Options{Spaces: ns.Spaces})
	for s.Next() != xmlscan.Start {
	}
	return s
}

func palette() *Palette {
	return &Palette{Theme: theme.Read(xmlscan.NewBytes([]byte(themeXML), &xmlscan.Options{Spaces: ns.Spaces})), Map: DefaultMap}
}

func near(a, b theme.Color) bool {
	d := func(x, y uint8) bool { return math.Abs(float64(x)-float64(y)) <= 1 }
	return d(a.R, b.R) && d(a.G, b.G) && d(a.B, b.B)
}

// Colours as files write them, against what Office shows for them.
func TestColours(t *testing.T) {
	pal := palette()
	hex := func(s string) theme.Color {
		c, ok := theme.Hex([]byte(s))
		if !ok {
			t.Fatalf("bad colour %q", s)
		}
		return c
	}
	for _, c := range []struct {
		xml   string
		want  string // "" if the colour is not worked out
		alpha float64
	}{
		{`<a:srgbClr val="1F3864"/>`, "1F3864", 1},
		{`<a:sysClr val="window" lastClr="FFFFFF"/>`, "FFFFFF", 1},
		{`<a:schemeClr val="accent1"/>`, "4472C4", 1},
		{`<a:schemeClr val="tx1"/>`, "000000", 1},
		{`<a:schemeClr val="bg1"/>`, "FFFFFF", 1},
		{`<a:schemeClr val="dk2"/>`, "44546A", 1},
		{`<a:prstClr val="red"/>`, "FF0000", 1},
		{`<a:scrgbClr r="100000" g="0" b="0"/>`, "FF0000", 1},
		// "Black, Text 1, Lighter 35%" and "White, Background 1, Darker 15%".
		{`<a:schemeClr val="tx1"><a:lumMod val="65000"/><a:lumOff val="35000"/></a:schemeClr>`, "595959", 1},
		{`<a:schemeClr val="bg1"><a:lumMod val="85000"/></a:schemeClr>`, "D9D9D9", 1},
		// "Blue, Accent 1, Lighter 40%".
		{`<a:schemeClr val="accent1"><a:lumMod val="60000"/><a:lumOff val="40000"/></a:schemeClr>`, "8FAADC", 1},
		// The grey of footers in the classic themes: text at a 75% tint.
		{`<a:schemeClr val="tx1"><a:tint val="75000"/></a:schemeClr>`, "898989", 1},
		{`<a:srgbClr val="FFFFFF"><a:shade val="50000"/></a:srgbClr>`, "BCBCBC", 1},
		{`<a:srgbClr val="000000"><a:alpha val="40000"/></a:srgbClr>`, "000000", 0.4},
		{`<a:srgbClr val="000000"><a:alpha val="50%"/><a:alphaMod val="50%"/></a:srgbClr>`, "000000", 0.25},
		{`<a:srgbClr val="000000"><a:alpha val="0"/></a:srgbClr>`, "000000", 0},
		// Not worked out: a colour in a form or with an adjustment this
		// package does not compute, or one the theme does not have.
		{`<a:hslClr hue="0" sat="100000" lum="50000"/>`, "", 0},
		{`<a:srgbClr val="FF0000"><a:hueOff val="100000"/></a:srgbClr>`, "", 0},
		{`<a:schemeClr val="accent6"/>`, "", 0},
		{`<a:schemeClr val="phClr"/>`, "", 0},
		{`<a:srgbClr val="XYZ"/>`, "", 0},
		{`<a:prstClr val="papayaWhip"/>`, "", 0},
	} {
		col := ReadColor(scan(`<a:solidFill xmlns:a="` + nsDraw + `">` + c.xml + `</a:solidFill>`))
		got, alpha, ok := pal.Resolve(col, nil)
		switch {
		case c.want == "":
			if ok {
				t.Errorf("%s: worked out as %v", c.xml, got)
			}
		case !ok || !near(got, hex(c.want)) || math.Abs(alpha-c.alpha) > 1e-9:
			t.Errorf("%s: %02X%02X%02X alpha %v (%v), want %s alpha %v", c.xml, got.R, got.G, got.B, alpha, ok, c.want, c.alpha)
		}
	}
	if ReadColor(scan(`<a:solidFill xmlns:a="` + nsDraw + `"/>`)).IsSet() {
		t.Error("an element with no colour gave one")
	}
	// A placeholder colour is the colour of whatever uses the style.
	red := theme.Color{R: 255}
	if got, _, ok := pal.Resolve(ReadColor(scan(`<a:solidFill xmlns:a="`+nsDraw+`"><a:schemeClr val="phClr"/></a:solidFill>`)), &red); !ok || got != red {
		t.Errorf("placeholder colour: %v %v", got, ok)
	}
}

// A colour map decides what the names for text and background stand for.
func TestColourMap(t *testing.T) {
	pal := palette()
	dark := ReadColorMap(scan(`<a:overrideClrMapping xmlns:a="` + nsDraw + `" bg1="dk1" tx1="lt1" bg2="dk2" tx2="lt2" accent1="accent2" accent2="accent2" hlink="hlink" folHlink="folHlink" bogus="x" bg1x="lt1"/>`))
	text, _ := Scheme("tx1")
	accent, _ := Scheme("accent1")
	direct, _ := Scheme("dk1")
	black, white := theme.Color{}, theme.Color{R: 255, G: 255, B: 255}
	if c, _, _ := pal.Resolve(text, nil); c != black {
		t.Errorf("tx1 under the usual map: %v", c)
	}
	pal.Map = dark
	if c, _, _ := pal.Resolve(text, nil); c != white {
		t.Errorf("tx1 under a dark map: %v", c)
	}
	if c, _, _ := pal.Resolve(accent, nil); c != (theme.Color{R: 0xED, G: 0x7D, B: 0x31}) {
		t.Errorf("accent1 mapped to accent2: %v", c)
	}
	if c, _, _ := pal.Resolve(direct, nil); c != black {
		t.Errorf("dk1 does not go through the map: %v", c)
	}
	// A map cannot point a name at another mapped name; such an entry is
	// ignored.
	odd := ReadColorMap(scan(`<a:overrideClrMapping xmlns:a="` + nsDraw + `" bg1="tx1" tx1="nonsense"/>`))
	if odd != DefaultMap {
		t.Errorf("a map of nonsense: %v", odd)
	}
	if _, ok := Scheme("nonsense"); ok {
		t.Error("an unknown name was accepted")
	}
}

func TestFills(t *testing.T) {
	read := func(xml string) (Fill, bool) {
		return ReadFill(scan(strings.Replace(xml, ">", ` xmlns:a="`+nsDraw+`">`, 1)))
	}
	for xml, want := range map[string]Paint{
		`<a:solidFill><a:srgbClr val="FF0000"/></a:solidFill>`: PaintSolid,
		`<a:solidFill></a:solidFill>`:                          PaintOther,
		`<a:noFill></a:noFill>`:                                PaintNone,
		`<a:gradFill><a:gsLst/></a:gradFill>`:                  PaintOther,
		`<a:blipFill><a:blip/></a:blipFill>`:                   PaintOther,
		`<a:pattFill></a:pattFill>`:                            PaintOther,
		`<a:grpFill></a:grpFill>`:                              PaintOther,
	} {
		if f, ok := read(xml); !ok || f.Paint != want {
			t.Errorf("%s: %v %v, want %v", xml, f.Paint, ok, want)
		}
	}
	if _, ok := read(`<a:ln><a:solidFill/></a:ln>`); ok {
		t.Error("an outline was read as a fill")
	}
}

// Where a shape lies once the groups it is in have moved, scaled, turned
// and mirrored it.
func TestGeometry(t *testing.T) {
	xfrm := func(attrs string, x, y, w, h float64, child string) Xfrm {
		return ReadXfrm(scan(`<a:xfrm xmlns:a="` + nsDraw + `"` + attrs + `><a:off x="` + emu(x) + `" y="` + emu(y) + `"/><a:ext cx="` + emu(w) + `" cy="` + emu(h) + `"/>` + child + `</a:xfrm>`))
	}
	bounds := func(m Matrix, w, h float64) (Rect, bool) {
		r, upright, ok := m.Bounds(w, h)
		if !ok {
			t.Fatal("no bounds")
		}
		round := func(v float64) float64 { return math.Round(v*100) / 100 }
		return Rect{round(r.X0), round(r.Y0), round(r.X1), round(r.Y1)}, upright
	}
	plain := xfrm("", 10, 20, 100, 50, "")
	if r, up := bounds(plain.Local(), 100, 50); r != (Rect{10, 20, 110, 70}) || !up {
		t.Errorf("a plain shape: %v %v", r, up)
	}
	// A quarter turn about the centre swaps width and height.
	if r, up := bounds(xfrm(` rot="5400000"`, 10, 20, 100, 50, "").Local(), 100, 50); r != (Rect{35, -5, 85, 95}) || !up {
		t.Errorf("a quarter turn: %v %v", r, up)
	}
	if r, up := bounds(xfrm(` rot="10800000" flipH="1" flipV="true"`, 10, 20, 100, 50, "").Local(), 100, 50); r != (Rect{10, 20, 110, 70}) || !up {
		t.Errorf("a half turn, mirrored: %v %v", r, up)
	}
	// 45 degrees: a square's bounding box grows by the square root of two,
	// and the shape no longer has its sides along the axes.
	if r, up := bounds(xfrm(` rot="2700000"`, 0, 0, 100, 100, "").Local(), 100, 100); r != (Rect{-20.71, -20.71, 120.71, 120.71}) || up {
		t.Errorf("an eighth turn: %v %v", r, up)
	}
	// A group shown at half the size of its children's space.
	group := xfrm("", 200, 100, 50, 50, `<a:chOff x="`+emu(1000)+`" y="`+emu(1000)+`"/><a:chExt cx="`+emu(100)+`" cy="`+emu(100)+`"/>`)
	child := xfrm("", 1020, 1040, 60, 20, "")
	if r, _ := bounds(child.Local().Then(group.Children()), 60, 20); r != (Rect{210, 120, 240, 130}) {
		t.Errorf("a shape in a scaled group: %v", r)
	}
	// A group that says nothing of its children's space leaves them where
	// they say.
	if r, _ := bounds(child.Local().Then(xfrm("", 0, 0, 0, 0, "").Children()), 60, 20); r != (Rect{1020, 1040, 1080, 1060}) {
		t.Errorf("a shape in a group with no space of its own: %v", r)
	}
	// Numbers too large to be a place give none, and do not overflow.
	huge := xfrm("", 1e9, 1e9, 1e9, 1e9, `<a:chOff x="0" y="0"/><a:chExt cx="1" cy="1"/>`)
	m := child.Local()
	for i := 0; i < 400; i++ {
		m = m.Then(huge.Children())
	}
	if _, _, ok := m.Bounds(60, 20); ok {
		t.Error("a shape scaled beyond any place was given one")
	}
	if x := ReadXfrm(scan(`<a:xfrm xmlns:a="` + nsDraw + `"><a:off x="0" y="0"/></a:xfrm>`)); x.Set {
		t.Error("a place with no size was taken to be set")
	}
}

func emu(points float64) string { return strconv.FormatInt(int64(math.Round(points*EMU)), 10) }

func TestNumbers(t *testing.T) {
	for _, c := range []struct {
		n      int
		scheme string
		want   string
	}{
		{3, "arabicPeriod", "3."}, {3, "arabicParenR", "3)"}, {3, "arabicParenBoth", "(3)"}, {3, "arabicPlain", "3"},
		{3, "alphaLcPeriod", "c."}, {27, "alphaUcParenR", "AA)"}, {4, "romanLcParenBoth", "(iv)"}, {1999, "romanUcPeriod", "MCMXCIX."},
		{5, "circleNumDbPlain", "⑤"}, {21, "circleNumDbPlain", "21"},
		{7, "hebrew2Minus", "7."}, {7, "", "7."}, {4000, "romanUcPeriod", "4000."}, {0, "alphaLcPeriod", "0."},
	} {
		if got := Number(c.n, c.scheme); got != c.want {
			t.Errorf("Number(%d, %q) = %q, want %q", c.n, c.scheme, got, c.want)
		}
	}
}

// Text takes its size, colour and bullet from the nearest layer that
// says: the run, the paragraph, the body's own list style, then the
// chain it was given.
func TestTextInheritance(t *testing.T) {
	pal := palette()
	style := func(xml string) *ListStyle {
		return ReadListStyle(scan(`<a:lstStyle xmlns:a="` + nsDraw + `">` + xml + `</a:lstStyle>`))
	}
	if style("") != nil || style(`<a:defPPr/><a:extLst/>`) != nil {
		t.Error("a list style that says nothing is not nil")
	}
	far := style(`<a:lvl1pPr><a:buChar char="•"/><a:defRPr sz="100" b="1"><a:noFill/></a:defRPr></a:lvl1pPr>` +
		`<a:lvl2pPr><a:buAutoNum type="alphaLcParenR"/><a:defRPr sz="2000"/></a:lvl2pPr>`)
	near := style(`<a:lvl1pPr><a:defRPr sz="2400" b="0"/></a:lvl1pPr>`)
	body := `<p:txBody xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="` + nsDraw + `"><a:bodyPr/>` +
		`<a:lstStyle><a:lvl3pPr><a:buNone/><a:defRPr sz="150"/></a:lvl3pPr></a:lstStyle>` +
		`<a:p><a:r><a:t>size from the nearer style, no ink and bullet from the farther</a:t></a:r></a:p>` +
		`<a:p><a:pPr lvl="1"/><a:r><a:rPr sz="100"/><a:t>a size of its own</a:t></a:r><a:r><a:t>, numbered by the farther style</a:t></a:r></a:p>` +
		`<a:p><a:pPr lvl="2"/><a:r><a:t>sized by the body's own style, which also takes the bullet away</a:t></a:r></a:p>` +
		`<a:p><a:pPr lvl="8"><a:buChar char="*"/></a:pPr><a:r><a:rPr><a:solidFill><a:srgbClr val="000000"><a:alpha val="0"/></a:srgbClr></a:solidFill></a:rPr><a:t>a level nothing speaks of, with a fill of no opacity</a:t></a:r></a:p></p:txBody>`
	var txt Text
	txt.Palette, txt.Chain, txt.Styles = pal, []*ListStyle{nil, near, far}, true
	txt.Read(scan(body))
	type para struct {
		text, marker, hidden string
		level                int
		spans                int
		size                 float64
	}
	var got []para
	for _, p := range txt.Paras {
		got = append(got, para{string(txt.Buf[p.Start:p.End]), p.Marker, p.Display.String(), p.Level, len(p.Spans), p.Size})
	}
	want := []para{
		{"size from the nearer style, no ink and bullet from the farther", "•", "no_ink", 0, 0, 24},
		{"a size of its own, numbered by the farther style", "a)", "", 1, 0, 1},
		{"sized by the body's own style, which also takes the bullet away", "", "", 2, 0, 1.5},
		{"a level nothing speaks of, with a fill of no opacity", "*", "no_ink", 8, 0, 18},
	}
	if len(got) != len(want) {
		t.Fatalf("%d paragraphs: %+v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("paragraph %d:\n got  %+v\n want %+v", i, got[i], want[i])
		}
	}
}

// Reading text does not allocate for each run or paragraph beyond the
// spans that mark something.
func TestTextAllocations(t *testing.T) {
	pal := palette()
	var body strings.Builder
	body.WriteString(`<p:txBody xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="` + nsDraw + `"><a:bodyPr/><a:lstStyle/>`)
	for i := 0; i < 200; i++ {
		body.WriteString(`<a:p><a:pPr lvl="1"/><a:r><a:rPr lang="en-US" dirty="0"/><a:t>A paragraph of ordinary text, </a:t></a:r><a:r><a:rPr lang="en-US" b="1"/><a:t>in two runs.</a:t></a:r></a:p>`)
	}
	body.WriteString(`</p:txBody>`)
	data := []byte(body.String())
	var txt Text
	txt.Palette = pal
	var s xmlscan.Scanner
	read := func() {
		s.ResetBytes(data, &xmlscan.Options{Spaces: ns.Spaces})
		for s.Next() != xmlscan.Start {
		}
		txt.Read(&s)
	}
	read()
	if n := testing.AllocsPerRun(20, read); n > 2 {
		t.Errorf("reading 200 paragraphs allocates %v times", n)
	}
	if len(txt.Paras) != 200 {
		t.Errorf("%d paragraphs", len(txt.Paras))
	}
}

// short cuts a long value where a character starts, so that a character
// split at the limit is not made U+FFFD.
func TestShortDoesNotSplitACharacter(t *testing.T) {
	v := []byte(strings.Repeat("x", 31) + "\u00e9" + "tail")
	if got, want := short(v, ""), strings.Repeat("x", 31); got != want {
		t.Errorf("short = %q, want %q", got, want)
	}
	v = []byte(strings.Repeat("x", 30) + "\u00e9" + "tail")
	if got, want := short(v, ""), strings.Repeat("x", 30)+"\u00e9"; got != want {
		t.Errorf("short = %q, want %q", got, want)
	}
}
