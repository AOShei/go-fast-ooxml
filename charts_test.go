package gofastooxml_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-core/numfmt"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

const (
	ctChart   = "application/vnd.openxmlformats-officedocument.drawingml.chart+xml"
	relChart  = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/chart"
	chartURI  = "http://schemas.openxmlformats.org/drawingml/2006/chart"
	chartNS   = ` xmlns:c="http://schemas.openxmlformats.org/drawingml/2006/chart" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
	ctXlsx    = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	relEmbeds = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/package"
)

// barChart is a chart part: a column chart with a title, one series per
// entry of values (a name, then a value per category).
func barChart(title string, cats []string, values ...[]string) string {
	var b strings.Builder
	b.WriteString(`<c:chartSpace` + chartNS + `><c:chart>`)
	if title != "" {
		b.WriteString(`<c:title><c:tx><c:rich><a:bodyPr/><a:p><a:r><a:t>` + title + `</a:t></a:r></a:p></c:rich></c:tx></c:title>`)
	}
	b.WriteString(`<c:autoTitleDeleted val="1"/><c:plotArea><c:barChart><c:barDir val="col"/>`)
	for i, v := range values {
		fmt.Fprintf(&b, `<c:ser><c:idx val="%d"/><c:order val="%d"/><c:tx><c:strRef><c:f>Sheet1!$%c$1</c:f><c:strCache><c:ptCount val="1"/><c:pt idx="0"><c:v>%s</c:v></c:pt></c:strCache></c:strRef></c:tx>`, i, i, 'B'+i, v[0])
		fmt.Fprintf(&b, `<c:cat><c:strRef><c:f>Sheet1!$A$2:$A$%d</c:f><c:strCache><c:ptCount val="%d"/>`, len(cats)+1, len(cats))
		for k, c := range cats {
			fmt.Fprintf(&b, `<c:pt idx="%d"><c:v>%s</c:v></c:pt>`, k, c)
		}
		fmt.Fprintf(&b, `</c:strCache></c:strRef></c:cat><c:val><c:numRef><c:f>Sheet1!$B$2:$B$9</c:f><c:numCache><c:formatCode>General</c:formatCode><c:ptCount val="%d"/>`, len(v)-1)
		for k, x := range v[1:] {
			fmt.Fprintf(&b, `<c:pt idx="%d"><c:v>%s</c:v></c:pt>`, k, x)
		}
		b.WriteString(`</c:numCache></c:numRef></c:val></c:ser>`)
	}
	b.WriteString(`<c:axId val="1"/><c:axId val="2"/></c:barChart>` +
		`<c:catAx><c:axId val="1"/><c:axPos val="b"/><c:title><c:tx><c:rich><a:p><a:r><a:t>Quarter</a:t></a:r></a:p></c:rich></c:tx></c:title></c:catAx>` +
		`<c:valAx><c:axId val="2"/><c:axPos val="l"/></c:valAx></c:plotArea></c:chart><c:externalData r:id="rId1"/></c:chartSpace>`)
	return b.String()
}

// withChart adds a chart part to pkg, named from source by a
// relationship, with the workbook it embeds, and returns the
// relationship's id.
func withChart(pkg *ooxmltest.Package, source, dir, xml string, workbook []byte) string {
	id := pkg.Rel(source, relChart, "../charts/chart1.xml")
	pkg.Add(dir+"/charts/chart1.xml", ctChart, xml)
	if workbook != nil {
		pkg.Rel(dir+"/charts/chart1.xml", relEmbeds, "../embeddings/Microsoft_Excel_Worksheet.xlsx")
		pkg.AddBytes(dir+"/embeddings/Microsoft_Excel_Worksheet.xlsx", ctXlsx, workbook)
	}
	return id
}

var quarterly = barChart("Sales", []string{"Q1", "Q2"}, []string{"North", "10", "12.5"}, []string{"South", "7", "9"})

// The workbook a chart embeds holds the chart's data and may hold more.
var chartWorkbook = ooxmltest.Xlsx(ooxmltest.Sheet{Name: "Sheet1", Data: xlRow(1, xlStr("A1", "only in the workbook"))}).Bytes()

func TestPowerPointChart(t *testing.T) {
	pkg := deck(ooxmltest.Slide{Shapes: box(2, 10, 10, 200, 40, "", dpara("", plain("Results"))) +
		frame(3, 50, 60, 300, 200, chartURI, `<c:chart`+chartNS+` r:id="rId2"/>`)})
	if id := withChart(pkg, "ppt/slides/slide1.xml", "ppt", quarterly, chartWorkbook); id != "rId2" {
		t.Fatalf("relationship %s", id)
	}
	doc := powerpoint(t, pkg, &gofastcore.Options{Child: childExtractor})
	got := doc.Parts[0].Blocks
	if len(got) != 3 || got[1].Kind != gofastcore.KindCaption || got[1].Text != "Sales" || got[2].Table == nil {
		t.Fatalf("blocks: %+v", got)
	}
	tb := got[2]
	if fmt.Sprint(tb.Table.Rows) != "[[ North South] [Q1 10 7] [Q2 12.5 9]]" || tb.Table.HeaderRows != 1 {
		t.Errorf("rows %q", tb.Table.Rows)
	}
	want := map[string]string{"role": "chart", "chart": "bar", "name": "Frame 3", "x_axis": "Quarter"}
	if fmt.Sprint(tb.Attrs) != fmt.Sprint(want) {
		t.Errorf("attrs %v, want %v", tb.Attrs, want)
	}
	if tb.Loc.BBox.X0 != 50 || tb.Loc.BBox.Y0 != 60 || got[1].Loc.BBox != tb.Loc.BBox {
		t.Errorf("places %v %v", got[1].Loc.BBox, tb.Loc.BBox)
	}
	if len(doc.Children) != 1 || !strings.Contains(doc.Children[0].Text(), "only in the workbook") {
		t.Errorf("children: %+v", doc.Children)
	}
	if md := doc.Markdown(); !strings.Contains(md, "Sales") || !strings.Contains(md, "| Q2 | 12.5 | 9 |") {
		t.Errorf("Markdown:\n%s", md)
	}
}

// A chart in a Word document follows the paragraph it is drawn in, with
// the name the drawing gives it; the workbook it embeds is a child.
func TestWordChart(t *testing.T) {
	drawing := `<w:drawing><wp:inline><wp:extent cx="5486400" cy="3200400"/><wp:docPr id="1" name="Chart 7"/>` +
		`<a:graphic><a:graphicData uri="` + chartURI + `"><c:chart` + chartNS + ` r:id="rId1"/></a:graphicData></a:graphic></wp:inline></w:drawing>`
	pkg := ooxmltest.Docx(para(run(text("Before the chart."))+run(drawing)) + para(run(text("After it."))))
	pkg.Rel("word/document.xml", relChart, "charts/chart1.xml")
	pkg.Add("word/charts/chart1.xml", ctChart, quarterly)
	pkg.Rel("word/charts/chart1.xml", relEmbeds, "../embeddings/Microsoft_Excel_Worksheet.xlsx")
	pkg.AddBytes("word/embeddings/Microsoft_Excel_Worksheet.xlsx", ctXlsx, chartWorkbook)
	doc := word(t, pkg, &gofastcore.Options{Child: childExtractor})
	expectBlocks(t, doc,
		`paragraph "Before the chart."`,
		`caption "Sales"`,
		`table chart=bar name=Chart 7 role=chart x_axis=Quarter header=1 [["" "North" "South"] ["Q1" "10" "7"] ["Q2" "12.5" "9"]]`,
		`paragraph "After it."`,
	)
	if len(doc.Children) != 1 || !strings.Contains(doc.Children[0].Text(), "only in the workbook") {
		t.Errorf("children: %+v", doc.Children)
	}
}

// oracleSeries is a series of a chart as an oracle reads it.
type oracleSeries struct {
	Name   string     `json:"name"`
	Cats   []string   `json:"cats"`
	Vals   []*float64 `json:"vals"`
	Format string     `json:"format"`
	// Formats are the number formats points have of their own.
	Formats []*string `json:"formats"`
}

// ourSeries is a column of values of a chart table: its header, the
// values, and the columns that may be its categories. A series with no
// name has an empty header, as a column of categories has, so every
// column is taken as a possible series and every leading column with an
// empty header as possible categories.
type ourSeries struct {
	name string
	vals []string
	cats [][]string
	used bool
}

// chartSeries lists the series of the chart tables of a part.
func chartSeries(part *gofastcore.Part) []*ourSeries {
	var out []*ourSeries
	for _, b := range part.Blocks {
		if b.Table == nil || b.Attrs[gofastcore.AttrRole] != gofastcore.RoleChart || len(b.Table.Rows) == 0 {
			continue
		}
		head := b.Table.Rows[0]
		column := func(c int) []string {
			var col []string
			for _, row := range b.Table.Rows[1:] {
				col = append(col, row[c])
			}
			return col
		}
		var cats [][]string
		for c := 0; c < len(head) && head[c] == ""; c++ {
			cats = append(cats, column(c))
		}
		for c := range head {
			out = append(out, &ourSeries{name: head[c], vals: column(c), cats: cats})
		}
	}
	return out
}

// sameValue reports whether a value of ours is the oracle's number
// written through the series' number format.
func sameValue(ours string, v *float64, code string) bool {
	if v == nil {
		return ours == ""
	}
	f := numfmt.General()
	if code != "" && !strings.EqualFold(code, "General") {
		f = numfmt.Parse(code)
	}
	want, _ := f.Number(nil, *v, false)
	return ours == strings.TrimSpace(string(want))
}

// sameCategory compares a category of ours with the oracle's. The oracle
// does not format numbers: a number of ours is compared as the number it
// starts with, and a date of ours, in ISO 8601, as the serial number of
// the day and time.
func sameCategory(ours, theirs string) bool {
	if strings.TrimSpace(ours) == strings.TrimSpace(theirs) || ours == "" && theirs == "None" {
		return true
	}
	b, err := strconv.ParseFloat(theirs, 64)
	if err != nil {
		return false
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02"} {
		if d, err := time.Parse(layout, ours); err == nil {
			// Counted from 1900, or from 1904 where the chart says so.
			for _, epoch := range []time.Time{time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC), time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)} {
				if serial := d.Sub(epoch).Hours() / 24; math.Abs(serial-b) < 1.0/86400 {
					return true
				}
			}
			return false
		}
	}
	num := strings.ReplaceAll(ours, ",", "")
	end := 0
	for end < len(num) && (num[end] >= '0' && num[end] <= '9' || num[end] == '.' || num[end] == '-' || num[end] == 'E' || num[end] == 'e' || num[end] == '+') {
		end++
	}
	a, err := strconv.ParseFloat(num[:end], 64)
	// Formatted to fewer decimals than the number has.
	return err == nil && math.Abs(a-b) <= 0.005*math.Max(1, math.Abs(b))
}

// sameCategories reports whether one of the possible columns of
// categories is the oracle's.
func sameCategories(options [][]string, theirs []string) bool {
	if len(theirs) == 0 {
		return true
	}
	for _, cats := range options {
		ok := true
		for i := range theirs {
			ok = ok && (i < len(cats) && sameCategory(cats[i], theirs[i]) || i >= len(cats) && (theirs[i] == "" || theirs[i] == "None"))
		}
		if ok {
			return true
		}
	}
	return false
}

// compareCharts matches the oracle's series of one part with ours and
// counts those that agree.
func compareCharts(t *testing.T, where string, ours []*ourSeries, theirs []oracleSeries, agree, differ *int, notes *[]string) {
	for _, s := range theirs {
		var best *ourSeries
		bestOK := false
		for _, o := range ours {
			// A table is as long as the longest series in it: a shorter
			// one ends in empty cells.
			if o.used || o.name != strings.TrimSpace(strings.TrimPrefix(s.Name, "None")) || len(o.vals) < len(s.Vals) {
				continue
			}
			ok := true
			for i := range o.vals {
				switch {
				case i >= len(s.Vals):
					ok = ok && o.vals[i] == ""
				case i < len(s.Formats) && s.Formats[i] != nil:
					ok = ok && sameValue(o.vals[i], s.Vals[i], *s.Formats[i])
				default:
					ok = ok && sameValue(o.vals[i], s.Vals[i], s.Format)
				}
			}
			ok = ok && sameCategories(o.cats, s.Cats)
			if best == nil || ok && !bestOK {
				best, bestOK = o, ok
			}
			if ok {
				break
			}
		}
		switch {
		case best == nil:
			*differ++
			*notes = append(*notes, fmt.Sprintf("%s: series %q of %d values not found", where, s.Name, len(s.Vals)))
		case !bestOK:
			best.used = true
			*differ++
			*notes = append(*notes, fmt.Sprintf("%s: series %q: ours %q %q, oracle %v %q (%s)", where, s.Name, best.vals, best.cats, deref(s.Vals), s.Cats, s.Format))
		default:
			best.used = true
			*agree++
		}
	}
}

func deref(v []*float64) []any {
	out := make([]any, len(v))
	for i, x := range v {
		if x != nil {
			out[i] = *x
		}
	}
	return out
}

// The data of the charts in the corpus's presentations and workbooks
// agrees with what python-pptx and openpyxl read from them: each series'
// name, its categories, and its values written through its number format.
func TestChartsAsOracles(t *testing.T) {
	if testing.Short() {
		t.Skip("reads the corpus")
	}
	if _, ok := ooxmltest.PythonBin(t); !ok {
		t.Skip("the Python comparison environment is not set up")
	}
	for _, c := range []struct{ format, tool string }{{"pptx", "python-pptx-charts"}, {"xlsx", "openpyxl-charts"}} {
		var contents [][]byte
		var names []string
		for _, f := range ooxmltest.LoadManifest(t, "corpus.json") {
			if f.Format != c.format || f.Note != "" {
				continue
			}
			data, err := os.ReadFile(f.AbsPath(t))
			if err != nil || !bytes.Contains(data, []byte("charts/chart")) {
				continue
			}
			contents, names = append(contents, data), append(names, f.Name)
		}
		if os.Getenv("GOFAST_CHARTS_SOAK") != "" {
			// The soak set too (testdata/soak.py): many more charts, and
			// slower.
			root := filepath.Join(ooxmltest.Root(t), "testdata", "corpus", "soak")
			filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() || strings.ToLower(filepath.Ext(path)) != "."+c.format {
					return nil
				}
				if data, err := os.ReadFile(path); err == nil && bytes.Contains(data, []byte("charts/chart")) && len(data) < 32<<20 {
					rel, _ := filepath.Rel(root, path)
					contents, names = append(contents, data), append(names, "soak/"+rel)
				}
				return nil
			})
		}
		if len(contents) == 0 {
			continue
		}
		ooxmltest.WarmPython(t, c.tool, c.format, contents)
		agree, differ := 0, 0
		var notes []string
		for i, data := range contents {
			doc, err := extract(t, data, nil)
			if err != nil {
				continue
			}
			res := ooxmltest.Python(t, c.tool, data, c.format)
			if res.Error != "" {
				continue
			}
			if c.format == "pptx" {
				var slides [][]struct {
					Series []oracleSeries `json:"series"`
				}
				if json.Unmarshal([]byte(res.Text), &slides) != nil || len(slides) != pageCount(doc) {
					continue
				}
				for k, charts := range slides {
					ours := chartSeries(&doc.Parts[k])
					for _, ch := range charts {
						compareCharts(t, fmt.Sprintf("%s slide %d", names[i], k+1), ours, ch.Series, &agree, &differ, &notes)
					}
				}
				continue
			}
			var sheets []struct {
				Sheet  string `json:"sheet"`
				Charts []struct {
					Series []oracleSeries `json:"series"`
				} `json:"charts"`
			}
			if json.Unmarshal([]byte(res.Text), &sheets) != nil {
				continue
			}
			for _, sh := range sheets {
				var part *gofastcore.Part
				for k := range doc.Parts {
					if doc.Parts[k].Name == sh.Sheet {
						part = &doc.Parts[k]
					}
				}
				if part == nil {
					continue
				}
				ours := chartSeries(part)
				for _, ch := range sh.Charts {
					compareCharts(t, names[i]+" "+sh.Sheet, ours, ch.Series, &agree, &differ, &notes)
				}
			}
		}
		t.Logf("%s: %d series agree with %s, %d differ", c.format, agree, c.tool, differ)
		for k, n := range notes {
			if k == 30 {
				t.Logf("... and %d more", len(notes)-k)
				break
			}
			t.Log(n)
		}
		if differ > 0 {
			t.Errorf("%s: %d series differ from %s", c.format, differ, c.tool)
		}
	}
}
