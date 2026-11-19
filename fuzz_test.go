package gofastooxml_test

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// FuzzOpen feeds arbitrary bytes to Extract. Whatever the input, the call
// must return a valid document or an error that wraps one of the shared
// errors; it must not panic, hang or exhaust memory.
//
// Seeds are the builder's presets and every committed fixture.
func FuzzOpen(f *testing.F) {
	f.Add([]byte{})
	// Encrypted files, which are compound files: the fuzzer changes the
	// container, the encryption information and the package.
	f.Add(ooxmltest.Encrypt(ooxmltest.Docx(ooxmltest.Para("x")).Bytes(), "VelvetSweatshop", ooxmltest.Encryption{SpinCount: 2}))
	f.Add(ooxmltest.Compound(map[string][]byte{"WordDocument": make([]byte, 600)}))
	f.Add([]byte("PK\x03\x04"))
	// Files cut short: without their directory, and inside a member.
	// They are read by the members' own headers.
	whole := ooxmltest.Docx(ooxmltest.Para("cut short")).Bytes()
	f.Add(whole[:directoryAt(f, whole)])
	f.Add(whole[:directoryAt(f, whole)*2/3])
	f.Add(ooxmltest.Docx(ooxmltest.Para("seed")).WithCore(`<dc:title>t</dc:title>`).Bytes())
	f.Add(ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: `<row r="1">` + ooxmltest.InlineCell("A1", "seed") + `</row>`}).Bytes())
	f.Add(ooxmltest.Pptx(ooxmltest.Slide{Shapes: ooxmltest.TextShape(2, 0, 0, 100, 20, "seed")}).Bytes())
	for _, file := range ooxmltest.LoadManifest(f, "fixtures.json") {
		f.Add(file.Read(f, false))
	}
	// A small budget keeps a fuzzed decompression bomb cheap, and a low
	// bound on the rounds of hashing keeps a fuzzed password check cheap.
	opts := &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: 4 << 20}}
	e := &gofastooxml.Extractor{MaxSpinCount: 2000}
	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := e.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), opts)
		checkOutcome(t, "fuzz input", doc, err)
	})
}

// FuzzDocx feeds arbitrary XML to the Word reader as the three parts that
// decide a document's structure: the body, the styles and the numbering.
// The package around them is sound, so the fuzzer spends its effort on the
// reader and not on the zip format.
func FuzzDocx(f *testing.F) {
	wrap := func(body string) []byte {
		return []byte(`<w:document` + ooxmltest.WordNS + `><w:body>` + body + `</w:body></w:document>`)
	}
	styles := []byte(`<w:styles` + ooxmltest.WordNS + `>` + wordStyles + `</w:styles>`)
	numbering := []byte(`<w:numbering` + ooxmltest.WordNS + `>` + wordNumbering + `</w:numbering>`)
	for _, body := range []string{
		ooxmltest.Para("plain") + ooxmltest.StyledPara("Heading1", "heading") + ooxmltest.ListPara(1, 1, "item"),
		`<w:tbl><w:tr><w:trPr><w:tblHeader/></w:trPr><w:tc><w:tcPr><w:gridSpan w:val="2"/><w:vMerge w:val="restart"/></w:tcPr>` + ooxmltest.Para("cell") + `</w:tc></w:tr></w:tbl>`,
		`<w:p><w:r><w:t>a</w:t><w:tab/><w:br/><w:sym w:font="Symbol" w:char="F061"/><w:footnoteReference w:id="2"/><w:commentReference w:id="0"/></w:r>` +
			`<w:del w:author="x"><w:r><w:delText>gone</w:delText></w:r></w:del><w:ins><w:r><w:t>new</w:t></w:r></w:ins></w:p>`,
		`<w:p><w:r><mc:AlternateContent><mc:Choice Requires="wps"><w:drawing><wp:inline><wp:docPr descr="alt"/><a:graphic><a:graphicData><wps:wsp><wps:txbx><w:txbxContent>` +
			ooxmltest.Para("box") + `</w:txbxContent></wps:txbx></wps:wsp></a:graphicData></a:graphic></wp:inline></w:drawing></mc:Choice><mc:Fallback><w:pict/></mc:Fallback></mc:AlternateContent></w:r></w:p>`,
		`<w:p><w:pPr><w:sectPr><w:headerReference w:type="default" r:id="rId1"/></w:sectPr></w:pPr></w:p><w:sdt><w:sdtContent>` + ooxmltest.Para("x") + `</w:sdtContent></w:sdt>`,
	} {
		f.Add(wrap(body), styles, numbering, []byte(ooxmltest.Para("note")))
	}
	// Character formatting, what lies behind text, links: the paths that
	// decide what is flagged as hidden.
	formatted := []byte(`<w:styles` + ooxmltest.WordNS + `>` + hiddenStyles +
		`<w:style w:type="table" w:styleId="Grid"><w:basedOn w:val="Grid"/><w:tblPr><w:tblStyleRowBandSize w:val="2"/></w:tblPr><w:tcPr><w:shd w:val="pct25" w:color="000000" w:fill="FFFFFF"/></w:tcPr>` +
		`<w:tblStylePr w:type="firstRow"><w:tcPr><w:shd w:val="clear" w:fill="1F3864"/></w:tcPr></w:tblStylePr></w:style></w:styles>`)
	for _, body := range []string{
		`<w:p><w:pPr><w:pStyle w:val="Secret"/><w:shd w:val="solid" w:color="auto" w:fill="000000"/></w:pPr><w:r><w:rPr><w:rStyle w:val="GhostChild"/><w:vanish w:val="0"/><w:b/><w:i/><w:u w:val="single"/><w:strike/>` +
			`<w:color w:val="FFFFFF" w:themeColor="background1"/><w:w w:val="1"/><w:fitText w:val="20"/><w:sz w:val="2"/><w:highlight w:val="yellow"/><w:shd w:val="clear" w:fill="auto"/><w:vertAlign w:val="superscript"/>` +
			`<w14:textFill><w14:solidFill><w14:schemeClr w14:val="bg1"><w14:alpha w14:val="100000"/></w14:schemeClr></w14:solidFill></w14:textFill></w:rPr><w:t xml:space="preserve"> a </w:t></w:r>` +
			`<w:hyperlink r:id="rId1" w:anchor="x"><w:r><w:t>b</w:t></w:r></w:hyperlink><w:fldSimple w:instr=' HYPERLINK \l "y" '><w:r><w:t>c</w:t></w:r></w:fldSimple>` +
			`<w:r><w:fldChar w:fldCharType="begin"/></w:r><w:r><w:instrText> HYPERLINK "z" </w:instrText></w:r><w:r><w:fldChar w:fldCharType="separate"/></w:r><w:r><w:rPr><w:vanish/></w:rPr><w:t>d</w:t><w:footnoteReference w:id="2"/></w:r><w:r><w:fldChar w:fldCharType="end"/></w:r></w:p>`,
		`<w:tbl><w:tblPr><w:tblStyle w:val="Grid"/><w:tblLook w:val="04A0" w:firstRow="1" w:noHBand="0"/><w:shd w:val="clear" w:fill="EEEEEE"/></w:tblPr>` +
			`<w:tr><w:trPr><w:cnfStyle w:val="100000000000"/><w:trHeight w:val="20" w:hRule="exact"/><w:gridBefore w:val="1"/></w:trPr><w:tc><w:tcPr><w:cnfStyle w:firstRow="1"/><w:shd w:val="diagStripe" w:fill="FFFFFF"/><w:vMerge w:val="restart"/></w:tcPr>` +
			`<w:p><w:r><w:rPr><w:color w:val="FFFFFF"/></w:rPr><w:t>a</w:t></w:r></w:p></w:tc></w:tr><w:tr><w:tc><w:tcPr><w:vMerge/></w:tcPr><w:p><w:r><w:rPr><w:vanish/></w:rPr><w:t>b</w:t></w:r><w:r><w:t> c</w:t></w:r></w:p></w:tc></w:tr></w:tbl>`,
		`<w:p><w:r><w:rPr><w:vanish/></w:rPr><w:drawing><wp:anchor><wp:extent cx="12700" cy="12700"/><a:graphic><a:graphicData><wps:wsp><wps:spPr><a:solidFill><a:schemeClr val="accent1"><a:lumMod val="50000"/></a:schemeClr></a:solidFill><a:ln><a:noFill/></a:ln></wps:spPr>` +
			`<wps:style><a:fillRef idx="1"><a:srgbClr val="000000"/></a:fillRef></wps:style><wps:txbx><w:txbxContent><w:tbl><w:tr><w:tc><w:p><w:r><w:rPr><w:color w:val="FFFFFF"/></w:rPr><w:t>in</w:t></w:r><w:r><w:t> box</w:t></w:r></w:p></w:tc></w:tr></w:tbl></w:txbxContent></wps:txbx>` +
			`<wps:bodyPr><a:spAutoFit/></wps:bodyPr></wps:wsp></a:graphicData></a:graphic></wp:anchor></w:drawing></w:r>` +
			`<w:r><w:pict><v:rect style="width:1pt;height:1pt" fillcolor="#abc [1]" filled="t"><v:fill type="gradient" opacity=".5" color="red"/><v:textbox><w:txbxContent><w:p><w:r><w:t>v</w:t></w:r></w:p></w:txbxContent></v:textbox></v:rect></w:pict></w:r></w:p>`,
	} {
		f.Add(wrap(body), formatted, numbering, []byte(ooxmltest.Para("note")))
	}
	// The content of a footnote, a comment and a header, which are joined
	// into one block each with their hidden ranges carried along.
	for _, note := range []string{
		`<w:p><w:r><w:t xml:space="preserve">seen </w:t></w:r><w:r><w:rPr><w:vanish/></w:rPr><w:t>unseen</w:t></w:r></w:p>` + ooxmltest.ListPara(1, 0, "item") +
			`<w:p><w:r><w:rPr><w:color w:val="FFFFFF"/></w:rPr><w:t>white</w:t></w:r></w:p>`,
		`<w:tbl><w:tr><w:tc><w:p><w:r><w:t> a</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:rPr><w:sz w:val="2"/></w:rPr><w:t>b</w:t></w:r></w:p></w:tc></w:tr>` +
			`<w:tr><w:trPr><w:trHeight w:val="1" w:hRule="exact"/></w:trPr><w:tc><w:tcPr><w:gridSpan w:val="2"/></w:tcPr><w:p><w:r><w:t>c</w:t></w:r></w:p></w:tc></w:tr></w:tbl>` + ooxmltest.Para("after"),
	} {
		f.Add(wrap(ooxmltest.Para("x")+`<w:p><w:r><w:footnoteReference w:id="2"/><w:commentReference w:id="0"/></w:r></w:p>`), formatted, numbering, []byte(note))
	}
	f.Add([]byte(`<w:document`+ooxmltest.WordNS+`><w:background w:color="000000"><v:background><v:fill type="gradient"/></v:background></w:background><w:body>`+
		ooxmltest.Para("x")+`<w:sectPr><w:headerReference w:type="first" r:id="rId5"/><w:titlePg/></w:sectPr></w:body></w:document>`), formatted, numbering, []byte(ooxmltest.Para("note")))
	opts := &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: 4 << 20}, Images: gofastcore.ImagesMeta, Spans: true}
	f.Fuzz(func(t *testing.T, body, styles, numbering, note []byte) {
		pkg := ooxmltest.Docx("")
		pkg.AddBytes("word/document.xml", ooxmltest.CTDocument, body)
		pkg.Rel("word/document.xml", ooxmltest.RelStyles, "styles.xml")
		pkg.AddBytes("word/styles.xml", ooxmltest.CTStyles, styles)
		pkg.Rel("word/document.xml", ooxmltest.RelNumbering, "numbering.xml")
		pkg.AddBytes("word/numbering.xml", ooxmltest.CTNumbering, numbering)
		pkg.WordPart("footnotes.xml", ooxmltest.CTFootnotes, ooxmltest.RelFootnotes, "footnotes", `<w:footnote w:id="2">`+string(note)+`</w:footnote>`)
		pkg.WordPart("comments.xml", ooxmltest.CTComments, ooxmltest.RelComments, "comments", `<w:comment w:id="0" w:author="a">`+string(note)+`</w:comment>`)
		pkg.WordPart("header1.xml", ooxmltest.CTHeader, ooxmltest.RelHeader, "hdr", string(note))
		data := pkg.Bytes()
		doc, err := gofastooxml.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), opts)
		checkOutcome(t, "fuzz input", doc, err)
	})
}

// FuzzXlsx gives the Excel reader arbitrary worksheet, styles,
// shared-strings and drawing parts inside a sound package. Whatever they
// hold, the result is a valid document or one of the shared errors.
func FuzzXlsx(f *testing.F) {
	sheet := func(data, extra string) []byte {
		return []byte(`<worksheet xmlns="` + ooxmltest.NSSheet + `" xmlns:r="` + ooxmltest.NSRel + `"><sheetData>` + data + `</sheetData>` + extra + `</worksheet>`)
	}
	styles := []byte(`<styleSheet xmlns="` + ooxmltest.NSSheet + `">` + xlStyles(map[int]string{164: "#,##0.00;(#,##0.00)", 165: ";;;", 166: `[h]:mm`, 167: "# ?/?"},
		[]string{`<sz val="1"/><color rgb="FFFFFFFF"/>`, `<color theme="1" tint="0.5"/>`, `<color indexed="9"/>`},
		[]string{solidFill("1F3864"), `<gradientFill/>`},
		`numFmtId="0"`, `numFmtId="164" fontId="1" fillId="2"`, `numFmtId="165"`, `numFmtId="14" fontId="2"`, `numFmtId="166" fontId="3" fillId="3"`, `numFmtId="167"`, `numFmtId="49"`) +
		`<colors><indexedColors><rgbColor rgb="00112233"/></indexedColors></colors></styleSheet>`)
	strs := []byte(`<sst xmlns="` + ooxmltest.NSSheet + `"><si><t>one</t></si><si><r><t>two</t></r><rPh><t>x</t></rPh></si><si><t>a_x000D_b</t></si></sst>`)
	drawing := []byte(drawingHeader + `<xdr:twoCellAnchor><xdr:from><xdr:col>1</xdr:col><xdr:row>2</xdr:row></xdr:from><xdr:sp><xdr:nvSpPr><xdr:cNvPr id="1" name="s" descr="d" hidden="1"/></xdr:nvSpPr>` +
		`<xdr:spPr><a:solidFill><a:srgbClr val="000000"/></a:solidFill></xdr:spPr><xdr:txBody><a:p><a:r><a:rPr sz="100"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill></a:rPr><a:t>t</a:t></a:r><a:br/></a:p></xdr:txBody></xdr:sp>` +
		`<xdr:pic><xdr:blipFill><a:blip r:embed="rId1"/></xdr:blipFill></xdr:pic></xdr:twoCellAnchor></xdr:wsDr>`)
	for _, s := range [][]byte{
		sheet(xlRow(1, `<c r="A1" t="s"><v>0</v></c>`, xlStyled("B1", 1, "-1234.5"), xlStyled("C1", 3, "46085.5"), xlStr("D1", "x"), `<c r="E1" t="b"><v>1</v></c>`, `<c r="F1" t="e"><v>#N/A</v></c>`, `<c r="G1"><f>1+1</f></c>`)+
			`<row hidden="1"><c s="2"><v>5</v></c><c s="4"><v>1.5</v></c><c s="5"><v>0.75</v></c><c s="6" t="str"><v>7</v></c></row>`,
			`<cols><col min="2" max="3" hidden="1"/></cols><mergeCells><mergeCell ref="A1:B2"/></mergeCells><tableParts><tablePart r:id="rId1"/></tableParts><drawing r:id="rId3"/>`),
		sheet(xlRow(5, xlNum("XFD5", "1"))+xlRow(1048576, xlNum("A1048576", "2"))+xlRow(3, xlNum("B3", "3"), xlNum("A3", "4"), xlNum("A3", "5")), ``),
		sheet(xlRow(1, xlNum("A1", "1"), xlNum("C1", "2"), xlNum("E1", "3"))+xlRow(3, xlNum("B3", "4"))+xlRow(4, xlNum("D4", "1e308"), xlNum("E4", "-0"), xlNum("F4", "abc")), ``),
	} {
		f.Add(s, styles, strs, drawing)
	}
	opts := &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: 4 << 20}, Images: gofastcore.ImagesMeta, Workers: 1}
	e := &gofastooxml.Extractor{MaxCells: 100_000}
	f.Fuzz(func(t *testing.T, sheet, styles, strs, drawing []byte) {
		pkg := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S"}, ooxmltest.Sheet{Name: "T", State: "hidden", Data: xlRow(1, `<c r="A1" t="s"><v>1</v></c>`)})
		pkg.AddBytes(firstSheet, ooxmltest.CTWorksheet, sheet)
		pkg.Rel("xl/workbook.xml", ooxmltest.RelStyles, "styles.xml")
		pkg.AddBytes("xl/styles.xml", ooxmltest.CTSheetStyles, styles)
		pkg.Rel("xl/workbook.xml", ooxmltest.RelSharedStrings, "sharedStrings.xml")
		pkg.AddBytes("xl/sharedStrings.xml", ooxmltest.CTSharedStrings, strs)
		pkg.Rel(firstSheet, relBase+"table", "../tables/table1.xml")
		pkg.Add("xl/tables/table1.xml", ctTable, `<table xmlns="`+ooxmltest.NSSheet+`" ref="A1:C3"/>`)
		pkg.Rel(firstSheet, relBase+"comments", "../comments1.xml")
		pkg.AddBytes("xl/comments1.xml", ctComments, drawing)
		pkg.Rel(firstSheet, relBase+"drawing", "../drawings/drawing1.xml")
		pkg.AddBytes("xl/drawings/drawing1.xml", ctDrawing, drawing)
		data := pkg.Bytes()
		doc, err := e.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), opts)
		checkOutcome(t, "fuzz input", doc, err)
		// The same sheet a band of rows at a time: every band is a part
		// that stands by itself, and the cells are those of the sheet
		// read whole, unless a limit cut either short. They are compared
		// by place: a sheet that gives one cell twice has it once when
		// read whole, and once in each band that it falls in.
		f, err := e.OpenBytes(data, opts)
		if err != nil {
			return
		}
		var bands []gofastcore.Part
		limited := doc != nil && (doc.Parts[0].Has(gofastcore.WarnLimitExceeded) || doc.Parts[0].Has(gofastcore.WarnPartFailed))
		for part, err := range f.Rows(context.Background(), 1, 2) {
			if err != nil {
				t.Fatalf("rows: %v", err)
			}
			band := gofastcore.New("xlsx")
			band.HiddenChecked = f.HiddenChecked()
			band.Parts = []gofastcore.Part{part}
			if verr := band.Validate(); verr != nil {
				t.Errorf("a band of rows is not a valid part: %v", verr)
			}
			limited = limited || part.Has(gofastcore.WarnLimitExceeded) || part.Has(gofastcore.WarnPartFailed)
			bands = append(bands, part)
		}
		if doc != nil && !limited {
			if got, want := len(fuzzCells(bands...)), len(fuzzCells(doc.Parts[0])); got != want {
				t.Errorf("%d cells a band at a time, %d in the sheet read whole", got, want)
			}
		}
	})
}

// fuzzCells returns the text of the cells of the tables in the given
// parts, by where the tables say they are.
func fuzzCells(parts ...gofastcore.Part) map[string]string {
	out := map[string]string{}
	for p := range parts {
		for i := range parts[p].Blocks {
			b := &parts[p].Blocks[i]
			if b.Table == nil {
				continue
			}
			for r, row := range b.Table.Rows {
				for c, text := range row {
					if text != "" {
						out[fuzzPlace(b.Loc.Range, r, c)] = text
					}
				}
			}
		}
	}
	return out
}

// fuzzPlace returns the place on the sheet of the cell in row r and
// column c of a table whose range is ref.
func fuzzPlace(ref string, r, c int) string {
	first, _, _ := strings.Cut(ref, ":")
	col, i := 0, 0
	for ; i < len(first) && first[i] >= 'A' && first[i] <= 'Z'; i++ {
		col = col*26 + int(first[i]-'A') + 1
	}
	row, _ := strconv.Atoi(first[i:])
	return strconv.Itoa(row+r) + "," + strconv.Itoa(col+c)
}

// FuzzPptx gives the PowerPoint reader arbitrary slide, layout, master
// and supporting parts inside a sound package. Whatever they hold, the
// result is a valid document or one of the shared errors.
func FuzzPptx(f *testing.F) {
	part := func(root, attrs, bg, shapes, after string) []byte {
		return []byte(`<p:` + root + ooxmltest.PresNS + attrs + `><p:cSld>` + bg + `<p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr/>` +
			shapes + `</p:spTree></p:cSld>` + after + `</p:` + root + `>`)
	}
	const style = "{11111111-2222-3333-4444-555555555555}"
	group := `<p:grpSp><p:nvGrpSpPr><p:cNvPr id="40" name="g" hidden="1"/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm rot="2700000" flipH="1"><a:off x="100" y="100"/><a:ext cx="900000" cy="400000"/>` +
		`<a:chOff x="0" y="0"/><a:chExt cx="100" cy="0"/></a:xfrm></p:grpSpPr>` + box(41, 1, 1, 5, 5, solid("FFFFFF"), dpara("", colored("FFFFFF", "in a group"))) + `</p:grpSp>`
	slide := part("sld", ` show="0" showMasterSp="0"`, `<p:bg><p:bgRef idx="1001"><a:schemeClr val="bg1"/></p:bgRef></p:bg>`,
		ooxmltest.Shape(2, ph("title", -1), "", ooxmltest.TextBody(dpara("", plain("Title"), `<a:br/>`, drun(` sz="100" b="1"`, `<a:noFill/>`, "tiny"))))+
			ooxmltest.Shape(3, ph("", 1), "", `<p:txBody><a:bodyPr vertOverflow="clip" tIns="0"><a:normAutofit fontScale="25000"/></a:bodyPr><a:lstStyle><a:lvl2pPr><a:buAutoNum type="romanLcParenR" startAt="3"/></a:lvl2pPr></a:lstStyle>`+
				dpara(`<a:pPr lvl="1"/>`, plain("numbered"))+dpara(`<a:pPr><a:buChar char="§"/><a:buFont typeface="Wingdings"/></a:pPr>`, drun("", `<a:hlinkClick r:id="rId7" action="ppaction://program"/>`, "link"),
				`<a:fld type="slidenum"><a:t>1</a:t></a:fld>`, `<mc:AlternateContent><mc:Choice><a14:m><m:oMath><m:r><m:t>x</m:t></m:r></m:oMath></a14:m></mc:Choice><mc:Fallback>`+plain("y")+`</mc:Fallback></mc:AlternateContent>`)+`</p:txBody>`)+
			group+picture(5, "rId5", "alt", 0, 0, 720, 540, `<a:alphaModFix amt="50000"/>`)+filled(6, 0, 0, 720, 540, "rect", scheme("accent1"))+
			tableFrame(7, 10, 10, 300, 100, `<a:tblPr firstRow="1" bandRow="1" lastCol="1"><a:tableStyleId>`+style+`</a:tableStyleId></a:tblPr>`,
				trow(tcell(` gridSpan="2" rowSpan="2"`, solid("000000"), dpara("", colored("000000", "a"))), tcell(` hMerge="1"`, "", dpara("", plain("b")))),
				trow(tcell(` vMerge="1"`, "", dpara("", plain("c"))), tc("d"), tc("e")))+
			frame(8, 0, 0, 10, 10, "chart", `<c:chart r:id="rId9"/>`)+
			`<mc:AlternateContent><mc:Choice Requires="p14">`+box(9, -500, 0, 10, 10, "", dpara("", plain("off")))+`</mc:Choice><mc:Fallback>`+box(10, 0, 0, 10, 10, "", dpara("", plain("fallback")))+`</mc:Fallback></mc:AlternateContent>`,
		`<p:clrMapOvr><a:overrideClrMapping bg1="dk1" tx1="lt1" bg2="dk2" tx2="lt2" accent1="accent2" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/></p:clrMapOvr>`+
			`<p:timing><p:tnLst><p:par><p:cTn id="1"><p:childTnLst><p:set><p:cBhvr><p:tgtEl><p:spTgt spid="6"/></p:tgtEl></p:cBhvr></p:set></p:childTnLst></p:cTn></p:par></p:tnLst></p:timing>`)
	layout := part("sldLayout", ` showMasterSp="1"`, bgSolid("FFF2CC"), testLayout()+box(20, 36, 4, 300, 14, solid("000000"), dpara("", plain("Layout text"))),
		`<p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr>`)
	master := part("sldMaster", "", bgSolid("FFFFFF"), testMaster()+box(21, 36, 520, 300, 14, "", dpara("", plain("Master text")))+picture(22, "rId5", "", 0, 0, 10, 10, ""),
		`<p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/>`+testTxStyles)
	extras := [][]byte{
		part("notes", "", "", noteShapes(dpara("", plain("Notes"), colored("FFFFFF", " in white"))), ""),
		[]byte(`<p:cmLst` + ooxmltest.PresNS + `><p:cm authorId="0" dt="2026-03-04T09:30:00.000" idx="1"><p:pos x="10" y="10"/><p:text>A comment</p:text></p:cm></p:cmLst>`),
		[]byte(`<p188:cmLst` + ooxmltest.PresNS + `><p188:cm id="{A}" authorId="0" created="2026"><p188:replyLst><p188:reply id="{B}" authorId="0"><p188:txBody><a:p><a:r><a:t>reply</a:t></a:r></a:p></p188:txBody></p188:reply></p188:replyLst>` +
			`<p188:txBody><a:p><a:r><a:t>comment</a:t></a:r></a:p></p188:txBody></p188:cm></p188:cmLst>`),
		[]byte(`<a:tblStyleLst` + ooxmltest.PresNS + `><a:tblStyle styleId="` + style + `"><a:tblBg><a:fill>` + solid("FFFFFF") + `</a:fill></a:tblBg><a:wholeTbl><a:tcTxStyle><a:fontRef idx="minor"/><a:schemeClr val="dk1"/></a:tcTxStyle>` +
			`<a:tcStyle><a:tcBdr><a:left><a:ln>` + solid("FFFFFF") + `</a:ln></a:left></a:tcBdr><a:fill>` + solid("DDEBF7") + `</a:fill></a:tcStyle></a:wholeTbl>` +
			`<a:lastCol><a:tcStyle><a:fillRef idx="1"><a:schemeClr val="accent1"/></a:fillRef></a:tcStyle></a:lastCol></a:tblStyle></a:tblStyleLst>`),
		[]byte(`<p:cmAuthorLst` + ooxmltest.PresNS + `><p:cmAuthor id="0" name="Ada"/></p:cmAuthorLst>`),
	}
	for _, extra := range extras {
		f.Add(slide, layout, master, extra)
	}
	opts := &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: 4 << 20}, Images: gofastcore.ImagesMeta, Workers: 1, Spans: true}
	e := &gofastooxml.Extractor{MaxCells: 100_000}
	picture := jpegBytes(f, 2, 2)
	f.Fuzz(func(t *testing.T, slide, layout, master, extra []byte) {
		pkg := ooxmltest.Pptx(ooxmltest.Slide{}, ooxmltest.Slide{Shapes: box(2, 10, 10, 100, 40, "", dpara("", plain("A sound slide")))})
		const first = "ppt/slides/slide1.xml"
		pkg.AddBytes(first, ooxmltest.CTSlide, slide)
		pkg.AddBytes("ppt/slideLayouts/slideLayout1.xml", ooxmltest.CTSlideLayout, layout)
		pkg.AddBytes("ppt/slideMasters/slideMaster1.xml", ooxmltest.CTSlideMaster, master)
		pkg.AddBytes("ppt/media/image1.jpeg", "image/jpeg", picture)
		for _, source := range []string{first, "ppt/slideMasters/slideMaster1.xml"} {
			for pkg.Rel(source, ooxmltest.RelImage, "../media/image1.jpeg") != "rId5" {
			}
		}
		pkg.Rel(first, ooxmltest.RelNotesSlide, "../notesSlides/notesSlide1.xml")
		pkg.ExternalRel(first, ooxmltest.RelHyperlink, "https://example.com/")
		pkg.Rel(first, relBase+"comments", "../comments/comment1.xml")
		pkg.Rel("ppt/presentation.xml", ooxmltest.RelTableStyle, "tableStyles.xml")
		pkg.Rel("ppt/presentation.xml", relBase+"commentAuthors", "commentAuthors.xml")
		for _, name := range []string{"ppt/notesSlides/notesSlide1.xml", "ppt/comments/comment1.xml", "ppt/tableStyles.xml", "ppt/commentAuthors.xml"} {
			pkg.AddBytes(name, "application/xml", extra)
		}
		data := pkg.Bytes()
		doc, err := e.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), opts)
		checkOutcome(t, "fuzz input", doc, err)
		// The same slides one at a time are the same slides.
		f, err := e.OpenBytes(data, opts)
		if err != nil || doc == nil {
			return
		}
		n := 0
		for part, err := range f.Parts(context.Background()) {
			if err != nil {
				t.Fatalf("parts: %v", err)
			}
			if n >= len(doc.Parts) || part.Number != doc.Parts[n].Number || len(part.Blocks) != len(doc.Parts[n].Blocks) || part.Blocks == nil {
				t.Fatalf("part %d read by itself differs from the document's", n+1)
			}
			n++
		}
		if n != len(doc.Parts) {
			t.Errorf("%d parts one at a time, %d in the document", n, len(doc.Parts))
		}
	})
}
