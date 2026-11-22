package chart

import (
	"bytes"
	"context"
	"fmt"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
)

const (
	cNS  = ` xmlns:c="http://schemas.openxmlformats.org/drawingml/2006/chart" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:c15="http://schemas.microsoft.com/office/drawing/2012/chart"`
	cxNS = ` xmlns:cx="http://schemas.microsoft.com/office/drawing/2014/chartex" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"`
)

func read(t *testing.T, xml string) *Chart {
	t.Helper()
	data := ooxmltest.NewPackage().Add("xl/charts/chart1.xml", "application/vnd.openxmlformats-officedocument.drawingml.chart+xml", xml).Bytes()
	pkg, err := opc.Open(bytes.NewReader(data), int64(len(data)), opc.Limits{MaxDecodedBytes: 1 << 30, MaxMembers: 100})
	if err != nil {
		t.Fatal(err)
	}
	c, err := Read(pkg, "xl/charts/chart1.xml")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func render(blocks []gofastcore.Block) string {
	var b strings.Builder
	for _, bl := range blocks {
		fmt.Fprintf(&b, "%s", bl.Kind)
		if bl.Text != "" {
			fmt.Fprintf(&b, " %q", bl.Text)
		}
		for _, k := range []string{gofastcore.AttrName, gofastcore.AttrRole, gofastcore.AttrChart, gofastcore.AttrAxisX, gofastcore.AttrAxisY} {
			if v, ok := bl.Attrs[k]; ok {
				fmt.Fprintf(&b, " %s=%s", k, v)
			}
		}
		if bl.Display != 0 {
			fmt.Fprintf(&b, " display=%s", bl.Display)
		}
		if bl.Table != nil {
			fmt.Fprintf(&b, " %q", bl.Table.Rows)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func strCache(f string, values ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<c:strRef><c:f>%s</c:f><c:strCache><c:ptCount val="%d"/>`, f, len(values))
	for i, v := range values {
		if v != "-" {
			fmt.Fprintf(&b, `<c:pt idx="%d"><c:v>%s</c:v></c:pt>`, i, v)
		}
	}
	b.WriteString(`</c:strCache></c:strRef>`)
	return b.String()
}

func numCache(code string, values ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<c:numRef><c:f>Sheet1!$B$2:$B$9</c:f><c:numCache><c:formatCode>%s</c:formatCode><c:ptCount val="%d"/>`, code, len(values))
	for i, v := range values {
		if v != "-" {
			fmt.Fprintf(&b, `<c:pt idx="%d"><c:v>%s</c:v></c:pt>`, i, v)
		}
	}
	b.WriteString(`</c:numCache></c:numRef>`)
	return b.String()
}

func TestClassicChart(t *testing.T) {
	xml := `<c:chartSpace` + cNS + `><c:date1904 val="0"/><c:chart>` +
		`<c:title><c:tx><c:rich><a:bodyPr/><a:p><a:r><a:t>Sales by </a:t></a:r><a:r><a:t>region</a:t></a:r></a:p></c:rich></c:tx></c:title>` +
		`<c:autoTitleDeleted val="0"/><c:plotArea>` +
		`<c:barChart><c:barDir val="col"/>` +
		`<c:ser><c:idx val="0"/><c:order val="0"/><c:tx>` + strCache("Sheet1!$B$1", "2025") + `</c:tx>` +
		`<c:dLbls><c:showVal val="1"/></c:dLbls>` +
		`<c:cat>` + strCache("Sheet1!$A$2:$A$4", "North", "South", "East") + `</c:cat>` +
		`<c:val>` + numCache(`#,##0.0`, "1234.5", "-", "0.25") + `</c:val></c:ser>` +
		`<c:axId val="1"/><c:axId val="2"/></c:barChart>` +
		`<c:lineChart><c:ser><c:idx val="1"/><c:order val="1"/><c:tx><c:v>Target</c:v></c:tx>` +
		`<c:cat>` + strCache("Sheet1!$A$2:$A$4", "North", "South", "East") + `</c:cat>` +
		`<c:val>` + numCache(`0%`, "0.5", "0.75", "1") + `</c:val></c:ser></c:lineChart>` +
		`<c:catAx><c:axId val="1"/><c:delete val="0"/><c:axPos val="b"/><c:title><c:tx><c:rich><a:p><a:r><a:t>Region</a:t></a:r></a:p></c:rich></c:tx></c:title></c:catAx>` +
		`<c:valAx><c:axId val="2"/><c:delete val="0"/><c:axPos val="l"/><c:title><c:tx><c:rich><a:p><a:r><a:t>Units</a:t></a:r></a:p></c:rich></c:tx></c:title><c:numFmt formatCode="General"/></c:valAx>` +
		`<c:extLst><c:ext uri="{02D57815-91ED-43cb-92C2-25804820EDAC}"><c15:filteredLineSeries><c15:ser><c:idx val="2"/><c:order val="2"/><c:tx>` + strCache("Sheet1!$D$1", "Secret") + `</c:tx>` +
		`<c:cat>` + strCache("Sheet1!$A$2:$A$4", "North", "South", "East") + `</c:cat>` +
		`<c:val>` + numCache(`General`, "7", "8", "9") + `</c:val></c15:ser></c15:filteredLineSeries></c:ext></c:extLst>` +
		`</c:plotArea></c:chart><c:externalData r:id="rId1"><c:autoUpdate val="0"/></c:externalData></c:chartSpace>`
	c := read(t, xml)
	blocks, _, cut := c.Blocks(nil, "Chart 1")
	got := render(blocks)
	want := `caption "Sales by region"
table name=Chart 1 role=chart chart=bar line x_axis=Region y_axis=Units [["" "2025" "Target"] ["North" "1,234.5" "50%"] ["South" "" "75%"] ["East" "0.3" "100%"]]
table name=Chart 1 role=chart chart=bar line x_axis=Region y_axis=Units display=hidden [["" "Secret"] ["North" "7"] ["South" "8"] ["East" "9"]]
`
	if got != want || cut {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A title with no text of its own shows the one series' name; a chart
// with no title element shows none. Categories of several levels are
// columns, the outermost first; a scatter chart's x values are its
// categories.
func TestClassicTitlesLevelsScatter(t *testing.T) {
	multi := `<c:multiLvlStrRef><c:f>Sheet1!$A$2:$B$5</c:f><c:multiLvlStrCache><c:ptCount val="4"/>` +
		`<c:lvl><c:pt idx="0"><c:v>Q1</c:v></c:pt><c:pt idx="1"><c:v>Q2</c:v></c:pt><c:pt idx="2"><c:v>Q1</c:v></c:pt><c:pt idx="3"><c:v>Q2</c:v></c:pt></c:lvl>` +
		`<c:lvl><c:pt idx="0"><c:v>2025</c:v></c:pt><c:pt idx="2"><c:v>2026</c:v></c:pt></c:lvl></c:multiLvlStrCache></c:multiLvlStrRef>`
	xml := `<c:chartSpace` + cNS + `><c:chart><c:title><c:overlay val="0"/></c:title><c:plotArea><c:areaChart>` +
		`<c:ser><c:tx>` + strCache("Sheet1!$C$1", "Revenue") + `</c:tx><c:cat>` + multi + `</c:cat><c:val>` + numCache("General", "1", "2", "3", "4") + `</c:val></c:ser>` +
		`</c:areaChart></c:plotArea></c:chart></c:chartSpace>`
	blocks, _, _ := read(t, xml).Blocks(nil, "")
	want := `caption "Revenue"
table role=chart chart=area [["" "" "Revenue"] ["2025" "Q1" "1"] ["" "Q2" "2"] ["2026" "Q1" "3"] ["" "Q2" "4"]]
`
	if got := render(blocks); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}

	scatter := `<c:chartSpace` + cNS + `><c:chart><c:autoTitleDeleted val="1"/><c:plotArea><c:scatterChart>` +
		`<c:ser><c:tx><c:v>A</c:v></c:tx><c:xVal>` + numCache("General", "1", "2") + `</c:xVal><c:yVal>` + numCache("General", "10", "20") + `</c:yVal></c:ser>` +
		`<c:ser><c:tx><c:v>B</c:v></c:tx><c:xVal>` + numCache("General", "5") + `</c:xVal><c:yVal>` + numCache("General", "50") + `</c:yVal></c:ser>` +
		`</c:scatterChart><c:valAx><c:axPos val="b"/><c:delete val="1"/><c:title><c:tx><c:rich><a:p><a:r><a:t>hidden axis</a:t></a:r></a:p></c:rich></c:tx></c:title></c:valAx></c:plotArea></c:chart></c:chartSpace>`
	blocks, _, _ = read(t, scatter).Blocks(nil, "")
	want = `table role=chart chart=scatter [["" "A"] ["1" "10"] ["2" "20"]]
table role=chart chart=scatter [["" "B"] ["5" "50"]]
`
	if got := render(blocks); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestNewerChart(t *testing.T) {
	xml := `<cx:chartSpace` + cxNS + `><cx:chartData><cx:data id="0">` +
		`<cx:strDim type="cat"><cx:f>Sheet1!$A$2:$B$4</cx:f><cx:lvl ptCount="3"><cx:pt idx="0">Leaf 1</cx:pt><cx:pt idx="1">Leaf 2</cx:pt><cx:pt idx="2">Leaf 3</cx:pt></cx:lvl>` +
		`<cx:lvl ptCount="3"><cx:pt idx="0">Stem</cx:pt><cx:pt idx="1">Stem</cx:pt><cx:pt idx="2">Other</cx:pt></cx:lvl></cx:strDim>` +
		`<cx:numDim type="size"><cx:f>Sheet1!$C$2:$C$4</cx:f><cx:lvl ptCount="3" formatCode="0.0"><cx:pt idx="0">1</cx:pt><cx:pt idx="1">2.25</cx:pt><cx:pt idx="2">3</cx:pt></cx:lvl></cx:numDim>` +
		`</cx:data></cx:chartData><cx:chart><cx:title><cx:tx><cx:txData><cx:v>Tree &amp; leaves</cx:v></cx:txData></cx:tx></cx:title>` +
		`<cx:plotArea><cx:plotAreaRegion><cx:series layoutId="sunburst" uniqueId="{1}"><cx:tx><cx:txData><cx:f>Sheet1!$C$1</cx:f><cx:v>Size</cx:v></cx:txData></cx:tx><cx:dataId val="0"/></cx:series>` +
		`<cx:series layoutId="paretoLine" ownerIdx="0"><cx:axisId val="2"/></cx:series>` +
		`</cx:plotAreaRegion><cx:axis id="0"><cx:catScaling/><cx:title><cx:tx><cx:txData><cx:v>Parts</cx:v></cx:txData></cx:tx></cx:title></cx:axis></cx:plotArea></cx:chart></cx:chartSpace>`
	blocks, _, _ := read(t, xml).Blocks(nil, "")
	want := `caption "Tree & leaves"
table role=chart chart=sunburst paretoline x_axis=Parts [["" "" "Size"] ["Stem" "Leaf 1" "1.0"] ["Stem" "Leaf 2" "2.3"] ["Other" "Leaf 3" "3.0"]]
`
	if got := render(blocks); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}

	// Excel's own: the data is in the sheet, under names, with no cache.
	bare := `<cx:chartSpace` + cxNS + `><cx:chartData><cx:data id="0"><cx:numDim type="val"><cx:f>_xlchart.v1.1</cx:f></cx:numDim></cx:data></cx:chartData>` +
		`<cx:chart><cx:plotArea><cx:plotAreaRegion><cx:series layoutId="boxWhisker"><cx:tx><cx:txData><cx:f>_xlchart.v1.0</cx:f><cx:v>Group A</cx:v></cx:txData></cx:tx><cx:dataId val="0"/></cx:series></cx:plotAreaRegion></cx:plotArea></cx:chart></cx:chartSpace>`
	blocks, _, _ = read(t, bare).Blocks(nil, "")
	if got, want := render(blocks), "table role=chart chart=boxwhisker [[\"Group A\"]]\n"; got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// The chart takes from the budget like any block and cell; a table there
// is no room for keeps the rows that fit.
func TestChartBudget(t *testing.T) {
	var vals []string
	for i := range 100 {
		vals = append(vals, fmt.Sprint(i))
	}
	xml := `<c:chartSpace` + cNS + `><c:chart><c:plotArea><c:lineChart><c:ser><c:tx><c:v>S</c:v></c:tx><c:val>` + numCache("General", vals...) + `</c:val></c:ser></c:lineChart></c:plotArea></c:chart></c:chartSpace>`
	c := read(t, xml)
	_, budget := gofastcore.WithTextBudget(context.Background(), gofastcore.Limits{MaxTextBytes: int64(gofastcore.BlockCost + 10*gofastcore.CellCost + 20)})
	blocks, taken, cut := c.Blocks(budget, "")
	if !cut || len(blocks) != 1 || len(blocks[0].Table.Rows) == 0 || len(blocks[0].Table.Rows) >= 101 {
		t.Fatalf("cut=%v blocks=%s", cut, render(blocks))
	}
	if taken <= 0 {
		t.Errorf("taken = %d", taken)
	}
}

// A count of points far beyond the cache is bounded; a cache with no
// count and no points is empty.
func TestChartPointBounds(t *testing.T) {
	empty := read(t, `<c:chartSpace`+cNS+`><c:chart><c:plotArea><c:lineChart><c:ser><c:val><c:numRef><c:numCache/></c:numRef></c:val></c:ser></c:lineChart></c:plotArea></c:chart></c:chartSpace>`)
	if blocks, _, _ := empty.Blocks(nil, ""); len(blocks) != 1 || len(blocks[0].Table.Rows) != 1 {
		t.Errorf("empty cache: %s", render(blocks))
	}
	xml := `<c:chartSpace` + cNS + `><c:chart><c:plotArea><c:lineChart>` +
		strings.Repeat(`<c:ser><c:val><c:numRef><c:numCache><c:ptCount val="999999999"/><c:pt idx="999999998"><c:v>1</c:v></c:pt></c:numCache></c:numRef></c:val></c:ser>`, 20) +
		`</c:lineChart></c:plotArea></c:chart></c:chartSpace>`
	c := read(t, xml)
	if c.Lost.Count(warn.CapChartTotal) != 1 || c.Lost.Count(warn.CapChartPoints) != 20 {
		t.Errorf("not reported as cut: %v", c.Lost.Warnings())
	}
	cells := 0
	for _, tb := range c.Tables {
		for _, r := range tb.Rows {
			cells += len(r)
		}
	}
	if cells > maxTotal+1000 {
		t.Errorf("%d cells held", cells)
	}
}

// Any chart part gives tables that are rectangular and within the
// bounds, and the blocks the budget allows, without failing.
func FuzzChart(f *testing.F) {
	f.Add(`<c:chartSpace` + cNS + `><c:chart><c:title><c:tx><c:rich><a:p><a:r><a:t>T</a:t></a:r></a:p></c:rich></c:tx></c:title><c:plotArea><c:barChart><c:ser><c:tx><c:v>S</c:v></c:tx><c:cat>` +
		strCache("x", "a", "b") + `</c:cat><c:val>` + numCache("0.0", "1", "2") + `</c:val></c:ser></c:barChart></c:plotArea></c:chart></c:chartSpace>`)
	f.Add(`<cx:chartSpace` + cxNS + `><cx:chartData><cx:data id="0"><cx:strDim type="cat"><cx:lvl ptCount="2"><cx:pt idx="0">a</cx:pt><cx:pt idx="1">b</cx:pt></cx:lvl></cx:strDim>` +
		`<cx:numDim type="val"><cx:lvl ptCount="2" formatCode="0%"><cx:pt idx="0">0.5</cx:pt><cx:pt idx="5">1</cx:pt></cx:lvl></cx:numDim></cx:data></cx:chartData>` +
		`<cx:chart><cx:plotArea><cx:plotAreaRegion><cx:series layoutId="funnel"><cx:dataId val="0"/></cx:series></cx:plotAreaRegion></cx:plotArea></cx:chart></cx:chartSpace>`)
	f.Fuzz(func(t *testing.T, xml string) {
		data := ooxmltest.NewPackage().Add("c.xml", "application/xml", xml).Bytes()
		pkg, err := opc.Open(bytes.NewReader(data), int64(len(data)), opc.Limits{MaxDecodedBytes: 1 << 24, MaxMembers: 10})
		if err != nil {
			return
		}
		c, _ := Read(pkg, "c.xml")
		if c == nil {
			return
		}
		cells := 0
		for _, tb := range c.Tables {
			for _, r := range tb.Rows {
				if len(r) != len(tb.Rows[0]) {
					t.Fatalf("ragged table %q", tb.Rows)
				}
				cells += len(r)
			}
		}
		if cells > 2*maxTotal {
			t.Fatalf("%d cells", cells)
		}
		_, budget := gofastcore.WithTextBudget(context.Background(), gofastcore.Limits{MaxTextBytes: 4096})
		c.Blocks(budget, "n")
	})
}
