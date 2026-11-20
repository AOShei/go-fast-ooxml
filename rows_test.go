package gofastooxml_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// cellsOf returns the cells of the tables in the given parts, by where
// they are on the sheet: each cell's text and the reasons it is hidden.
func cellsOf(t testing.TB, parts ...gofastcore.Part) map[cellAt]string {
	t.Helper()
	out := map[cellAt]string{}
	for p := range parts {
		for i := range parts[p].Blocks {
			b := &parts[p].Blocks[i]
			if b.Table == nil || b.Attrs[gofastcore.AttrRole] == gofastcore.RoleChart {
				continue // a chart's data is not cells of the sheet
			}
			origin := parseA1(t, b.Loc.Range)
			hidden := map[cellAt]gofastcore.Display{}
			for _, h := range b.Table.Display {
				for r := h.Row; r < h.Row+h.RowSpan; r++ {
					for c := h.Col; c < h.Col+h.ColSpan; c++ {
						hidden[cellAt{r, c}] |= h.Display
					}
				}
			}
			for r, row := range b.Table.Rows {
				for c, text := range row {
					if text == "" {
						continue
					}
					at := cellAt{origin.row + r, origin.col + c}
					if _, dup := out[at]; dup {
						t.Errorf("cell %v is in two tables", at)
					}
					reasons := b.DisplayWhole() | hidden[cellAt{r, c}]
					out[at] = text + " [" + reasons.String() + "]"
				}
			}
		}
	}
	return out
}

func sameCells(t testing.TB, what string, got, want map[cellAt]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: %d cells, want %d", what, len(got), len(want))
	}
	shown := 0
	for at, text := range want {
		if got[at] != text && shown < 5 {
			t.Errorf("%s: cell %v is %q, want %q", what, at, got[at], text)
			shown++
		}
	}
}

// A sheet read a band of rows at a time gives the cells that the sheet
// read whole gives, with the same hidden flags, whatever the size of the
// band; and each band is a part that stands by itself.
func TestRowsGiveTheCellsOfTheSheet(t *testing.T) {
	// A sheet with hidden rows and columns, cells that hide their text,
	// merged cells that run over the ends of bands, gaps, and rows out of
	// order.
	var rows strings.Builder
	for r := 1; r <= 120; r++ {
		if r%17 == 0 {
			continue // a gap: tables end and begin here
		}
		hidden := ""
		if r%13 == 0 {
			hidden = ` hidden="1"`
		}
		n := strconv.Itoa(r)
		fmt.Fprintf(&rows, `<row r="%d"%s>%s%s%s%s</row>`, r, hidden,
			xlNum("A"+n, n), xlStr("B"+n, "row "+n), xlStyled("D"+n, 1, strconv.Itoa(r*1000)), xlStyledStr("F"+n, 2+r%2, "styled "+n))
	}
	rows.WriteString(xlRow(60, xlStr("H60", "out of order")))
	styles := xlStyles(map[int]string{164: "#,##0.00"}, []string{`<sz val="1"/>`, `<color rgb="FFFFFFFF"/>`}, nil,
		`numFmtId="0"`, `numFmtId="164"`, `numFmtId="0" fontId="1"`, `numFmtId="0" fontId="2"`)
	pkg := ooxmltest.Xlsx(
		ooxmltest.Sheet{Name: "Data", Data: rows.String(), Extra: `<mergeCells count="2"><mergeCell ref="A9:B12"/><mergeCell ref="D40:D41"/></mergeCells>`,
			Before: `<cols><col min="2" max="2" width="9" hidden="1"/></cols>`},
		ooxmltest.Sheet{Name: "Empty"},
		ooxmltest.Sheet{Name: "Hidden", State: "hidden", Data: xlRow(1, xlStr("A1", "on a hidden sheet"))},
	).WithSheetStyles(styles)
	data := pkg.Bytes()
	ctx := context.Background()
	f, err := gofastooxml.OpenBytes(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	for number := 1; number <= f.NumParts(); number++ {
		whole, err := f.Part(ctx, number)
		if err != nil {
			t.Fatal(err)
		}
		want := cellsOf(t, whole)
		for _, band := range []int{1, 7, 50, 100000, 0} {
			var bands []gofastcore.Part
			for part, err := range f.Rows(ctx, number, band) {
				if err != nil {
					t.Fatal(err)
				}
				if part.Kind != gofastcore.PartSheet || part.Number != number || part.Name != whole.Name || part.Display != whole.Display || part.Blocks == nil {
					t.Errorf("sheet %d: a band is %s %d %q hidden=%s", number, part.Kind, part.Number, part.Name, part.Display)
				}
				// A band is a part like any other: a document of it is
				// valid.
				doc := gofastcore.New("xlsx")
				doc.Parts = []gofastcore.Part{part}
				if err := doc.Validate(); err != nil {
					t.Errorf("sheet %d, bands of %d: %v", number, band, err)
				}
				seen := map[int]bool{}
				for _, b := range part.Blocks {
					if b.Table == nil || b.Origin != gofastcore.OriginInferred {
						t.Errorf("sheet %d: a band has a %s block of origin %s", number, b.Kind, b.Origin)
						continue
					}
					origin := parseA1(t, b.Loc.Range)
					for r := range b.Table.Rows {
						seen[origin.row+r] = true
					}
				}
				if limit := max(band, 1); len(seen) > limit+limit/2+2 {
					t.Errorf("sheet %d, bands of %d: a band spans %d rows", number, band, len(seen))
				}
				bands = append(bands, part)
			}
			sameCells(t, fmt.Sprintf("sheet %d in bands of %d", number, band), cellsOf(t, bands...), want)
			if len(want) == 0 && (len(bands) != 1 || !bands[0].Has(gofastcore.WarnPartNoText)) {
				t.Errorf("sheet %d, with no cells: %d bands, warnings %v", number, len(bands), bands)
			}
			if band == 7 && number == 1 && len(bands) < 10 {
				t.Errorf("bands of 7 rows: %d bands", len(bands))
			}
		}
	}
	// The display reasons are there to compare: this sheet has them.
	flags := 0
	for _, text := range cellsOf(t, func() gofastcore.Part { p, _ := f.Part(ctx, 1); return p }()) {
		if !strings.HasSuffix(text, "[]") {
			flags++
		}
	}
	if flags < 100 {
		t.Errorf("only %d cells of the sheet have a reason; the test wants many", flags)
	}
}

// The fixtures and real workbooks, a band at a time.
func TestRowsOnFixturesAndCorpus(t *testing.T) {
	var files []ooxmltest.File
	for _, f := range ooxmltest.LoadManifest(t, "fixtures.json") {
		if f.Format == "xlsx" {
			files = append(files, f)
		}
	}
	if !testing.Short() {
		n := 0
		for _, f := range ooxmltest.LoadManifest(t, "corpus.json") {
			if f.Format == "xlsx" && f.Note == "" && f.Size < 400_000 {
				if n++; n%6 == 0 { // a sixth of them is enough
					files = append(files, f)
				}
			}
		}
	}
	ctx := context.Background()
	sheets, cells := 0, 0
	for _, file := range files {
		data, err := os.ReadFile(file.AbsPath(t))
		if err != nil {
			continue
		}
		f, err := gofastooxml.OpenBytes(data, nil)
		if err != nil {
			continue // not a workbook this library opens; another test's business
		}
		for number := 1; number <= len(f.Sheets()); number++ {
			whole, err := f.Part(ctx, number)
			if err != nil {
				t.Fatal(err)
			}
			if whole.Has(gofastcore.WarnLimitExceeded) || whole.Has(gofastcore.WarnPartFailed) {
				continue // cut short, at a place that depends on how it is read
			}
			var bands []gofastcore.Part
			for part, err := range f.Rows(ctx, number, 25) {
				if err != nil {
					t.Fatalf("%s sheet %d: %v", file.Name, number, err)
				}
				bands = append(bands, part)
			}
			want := cellsOf(t, whole)
			sameCells(t, fmt.Sprintf("%s sheet %d", file.Name, number), cellsOf(t, bands...), want)
			sheets++
			cells += len(want)
		}
	}
	t.Logf("%d workbooks, %d sheets, %d cells: the same a band at a time as whole", len(files), sheets, cells)
}

func TestRowsOfSheetsThatCannotBeRead(t *testing.T) {
	pkg := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "Sound", Data: xlRow(1, xlStr("A1", "x"))}, ooxmltest.Sheet{Name: "Damaged"})
	var rows strings.Builder
	for r := 1; r <= 30; r++ {
		rows.WriteString(xlRow(r, xlStr("A"+strconv.Itoa(r), "row "+strconv.Itoa(r))))
	}
	pkg.Add("xl/worksheets/sheet2.xml", ooxmltest.CTWorksheet, `<worksheet xmlns="`+ooxmltest.NSSheet+`"><sheetData>`+rows.String()+`<row r="31"><c r="A31" t="inlineStr"><is><t>cut o`)
	chart := pkg.Rel("xl/workbook.xml", relBase+"chartsheet", "chartsheets/sheet1.xml")
	pkg.Add("xl/chartsheets/sheet1.xml", "application/vnd.openxmlformats-officedocument.spreadsheetml.chartsheet+xml", `<chartsheet xmlns="`+ooxmltest.NSSheet+`"/>`)
	gone := pkg.Rel("xl/workbook.xml", ooxmltest.RelWorksheet, "worksheets/gone.xml")
	pkg.Add("xl/workbook.xml", ooxmltest.CTWorkbook, `<workbook xmlns="`+ooxmltest.NSSheet+`" xmlns:r="`+ooxmltest.NSRel+`"><sheets>`+
		`<sheet name="Sound" sheetId="1" r:id="rId1"/><sheet name="Damaged" sheetId="2" r:id="rId2"/><sheet name="Chart" sheetId="3" r:id="`+chart+`"/>`+
		`<sheet name="Gone" sheetId="4" r:id="`+gone+`"/></sheets></workbook>`)
	ctx := context.Background()
	f, err := gofastooxml.OpenBytes(pkg.Bytes(), nil)
	if err != nil {
		t.Fatal(err)
	}
	bands := func(number, band int) []gofastcore.Part {
		var out []gofastcore.Part
		for part, err := range f.Rows(ctx, number, band) {
			if err != nil {
				t.Fatalf("sheet %d: %v", number, err)
			}
			out = append(out, part)
		}
		return out
	}
	// A sheet that breaks off: the bands read before it, and a last one
	// that says so. The warning is on that one alone.
	damaged := bands(2, 10)
	if n := len(cellsOf(t, damaged...)); n != 30 {
		t.Errorf("damaged sheet: %d cells in %d bands", n, len(damaged))
	}
	for i, part := range damaged {
		if part.Has(gofastcore.WarnPartFailed) != (i == len(damaged)-1) {
			t.Errorf("damaged sheet, band %d of %d: warnings %v", i+1, len(damaged), part.Warnings)
		}
	}
	// A chart sheet and a sheet that is not in the file: one part each,
	// the same as when it is read whole.
	for _, number := range []int{3, 4} {
		whole, _ := f.Part(ctx, number)
		got := bands(number, 10)
		if len(got) != 1 || asJSON(t, got[0]) != asJSON(t, whole) {
			t.Errorf("sheet %d: %d bands, %v; read whole it is %v", number, len(got), got, whole)
		}
	}
	for _, number := range []int{0, 5} {
		for _, err := range f.Rows(ctx, number, 10) {
			if !errors.Is(err, gofastcore.ErrNoPartsSelected) {
				t.Errorf("sheet %d: %v", number, err)
			}
		}
	}
}

// Rows are not held against the limit on cells, since they are not kept;
// the decompression budget holds, and the band it is met in says so.
// Leaving the loop, or cancelling, stops the reading.
func TestRowsLimitsAndStopping(t *testing.T) {
	var rows strings.Builder
	for r := 1; r <= 3000; r++ {
		n := strconv.Itoa(r)
		rows.WriteString(xlRow(r, xlNum("A"+n, n), xlStr("B"+n, "some text in row "+n)))
	}
	data := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "Long", Data: rows.String()}).Bytes()
	ctx := context.Background()

	e := &gofastooxml.Extractor{MaxCells: 100}
	f, err := e.OpenBytes(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	whole, _ := f.Part(ctx, 1)
	if !whole.Has(gofastcore.WarnLimitExceeded) || len(cellsOf(t, whole)) > 100 {
		t.Errorf("read whole under a limit of 100 cells: %d cells, %v", len(cellsOf(t, whole)), whole.Warnings)
	}
	var bands []gofastcore.Part
	for part, err := range f.Rows(ctx, 1, 100) {
		if err != nil {
			t.Fatal(err)
		}
		if part.Has(gofastcore.WarnLimitExceeded) {
			t.Errorf("a band says a limit was reached: %v", part.Warnings)
		}
		bands = append(bands, part)
	}
	if n := len(cellsOf(t, bands...)); n != 6000 || len(bands) != 30 {
		t.Errorf("%d cells in %d bands", n, len(bands))
	}

	// The decompression budget: the sheet's part is read twice, so a
	// budget of one and a half times its size is met in the rows.
	sheet := int64(len(rows.String()))
	f, _ = gofastooxml.OpenBytes(data, &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: sheet + sheet/2}})
	bands = bands[:0]
	for part, err := range f.Rows(ctx, 1, 100) {
		if err != nil {
			t.Fatal(err)
		}
		bands = append(bands, part)
	}
	last := bands[len(bands)-1]
	if n := len(cellsOf(t, bands...)); n == 0 || n >= 6000 || !last.Has(gofastcore.WarnLimitExceeded) {
		t.Errorf("under a decompression budget: %d cells, last band's warnings %v", n, last.Warnings)
	}
	for _, part := range bands[:len(bands)-1] {
		if part.Has(gofastcore.WarnLimitExceeded) {
			t.Errorf("a band before the last says a limit was reached")
		}
	}

	// Leaving early, and cancelling.
	f, _ = gofastooxml.OpenBytes(data, nil)
	seen := 0
	for range f.Rows(ctx, 1, 10) {
		if seen++; seen == 3 {
			break
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	defer cancel()
	seen = 0
	var last2 error
	for _, err := range f.Rows(cancelled, 1, 10) {
		last2 = err
		if seen++; seen == 5 {
			cancel()
		}
	}
	if !errors.Is(last2, context.Canceled) || seen > 8 {
		t.Errorf("after cancelling: %d bands, then %v", seen, last2)
	}
	// The handle reads on afterwards.
	if part, err := f.Part(ctx, 1); err != nil || len(cellsOf(t, part)) != 6000 {
		t.Errorf("after the stopped readings: %v", err)
	}
}

// What is held while a sheet is read a band at a time does not grow with
// the sheet.
func TestRowsMemoryDoesNotGrowWithTheSheet(t *testing.T) {
	if testing.Short() {
		t.Skip("builds large sheets")
	}
	build := func(n int) []byte {
		var rows bytes.Buffer
		for r := 1; r <= n; r++ {
			s := strconv.Itoa(r)
			rows.WriteString(`<row r="` + s + `"><c r="A` + s + `"><v>` + s + `</v></c><c r="B` + s + `" t="inlineStr"><is><t>a cell of text in row ` + s + `</t></is></c>` +
				`<c r="C` + s + `"><v>` + s + `.5</v></c><c r="D` + s + `" t="inlineStr"><is><t>more text</t></is></c></row>`)
		}
		return ooxmltest.Xlsx(ooxmltest.Sheet{Name: "Long", Data: rows.String()}).Bytes()
	}
	read := func(data []byte) (peak uint64, cells int) {
		f, err := gofastooxml.OpenBytes(data, &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: -1}})
		if err != nil {
			t.Fatal(err)
		}
		peak = peakHeap(func() {
			for part, err := range f.Rows(context.Background(), 1, 1000) {
				if err != nil {
					t.Fatal(err)
				}
				for _, b := range part.Blocks {
					cells += len(b.Table.Rows) * len(b.Table.Rows[0])
				}
			}
		})
		return peak, cells
	}
	small, large := build(50_000), build(400_000)
	peakSmall, cellsSmall := read(small)
	peakLarge, cellsLarge := read(large)
	t.Logf("%d cells: peak heap %.1f MB; %d cells: %.1f MB", cellsSmall, float64(peakSmall)/(1<<20), cellsLarge, float64(peakLarge)/(1<<20))
	if cellsSmall != 200_000 || cellsLarge != 1_600_000 {
		t.Errorf("%d and %d cells", cellsSmall, cellsLarge)
	}
	// A band at a time takes a fraction of what the sheet takes whole, on
	// any system. Where the numbers can be relied on, it is also under a
	// fixed ceiling.
	held := peakHeap(func() {
		f, err := (&gofastooxml.Extractor{MaxCells: -1}).OpenBytes(large, &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: -1}})
		if err != nil {
			t.Fatal(err)
		}
		if part, err := f.Part(context.Background(), 1); err != nil || len(part.Blocks) == 0 {
			t.Fatalf("the sheet whole: %v", err)
		}
	})
	t.Logf("the long sheet whole: %.1f MB", float64(held)/(1<<20))
	if peakLarge > held/3 {
		t.Errorf("peak heap %d bytes a band at a time, %d for the whole sheet", peakLarge, held)
	}
	const ceiling = 48 << 20
	absolute := !underRace && runtime.GOOS == "linux"
	if absolute && peakLarge > ceiling {
		t.Errorf("peak heap %d MB for the long sheet is over the ceiling of %d MB", peakLarge>>20, ceiling>>20)
	}

	// The large generated workbook, if it is there: two million cells in
	// a fraction of what reading it whole takes.
	data, err := os.ReadFile(filepath.Join(ooxmltest.Root(t), "testdata", "corpus", "generated", "large.xlsx"))
	if err != nil {
		return
	}
	f, err := gofastooxml.OpenBytes(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	cells := 0
	peak := peakHeap(func() {
		for n := 1; n <= f.NumParts(); n++ {
			for part, err := range f.Rows(context.Background(), n, 1000) {
				if err != nil {
					t.Fatal(err)
				}
				for _, b := range part.Blocks {
					cells += len(b.Table.Rows) * len(b.Table.Rows[0])
				}
			}
		}
	})
	t.Logf("large.xlsx a band at a time: %d cells, peak heap %.1f MB", cells, float64(peak)/(1<<20))
	if absolute && peak > ceiling {
		t.Errorf("large.xlsx: peak heap %d MB is over the ceiling of %d MB", peak>>20, ceiling>>20)
	}
}

// A damaged sheet may give its rows out of order, and one cell twice.
// Read whole, the sheet is put in order and has the cell once, as it was
// first given. A band at a time, rows are put in order within a band and
// not across bands, which would take holding the sheet: the cell is in
// each band it falls in. Nothing the file holds is lost either way.
// (Found by the fuzz campaign of the release hardening.)
func TestRowsOfASheetThatGivesACellTwice(t *testing.T) {
	rows := xlRow(1, xlStr("A1", "first A1"), xlStr("E1", "first E1")) +
		xlRow(3, xlStr("B3", "B3")) +
		xlRow(5, xlStr("B5", "B5")) +
		xlRow(1, xlStr("D1", "late D1"), xlStr("E1", "second E1"))
	data := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: rows}).Bytes()
	f, err := gofastooxml.OpenBytes(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	whole, err := f.Part(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := cellsOf(t, whole)
	// (cellsOf gives each cell's text with its hidden flags after it.)
	if len(want) != 5 || !strings.HasPrefix(want[cellAt{0, 4}], "first E1") || !strings.HasPrefix(want[cellAt{0, 3}], "late D1") {
		t.Errorf("the sheet read whole: %v", want)
	}
	// Bands of two rows: the second row 1 is in the second band.
	got := map[cellAt][]string{}
	bands := 0
	for part, err := range f.Rows(ctx, 1, 2) {
		if err != nil {
			t.Fatal(err)
		}
		bands++
		for at, text := range cellsOf(t, part) {
			got[at] = append(got[at], text)
		}
	}
	if bands != 2 || len(got) != len(want) {
		t.Fatalf("%d bands with cells at %d places, the sheet has %d: %v", bands, len(got), len(want), got)
	}
	for at, texts := range got {
		if at == (cellAt{0, 4}) {
			if len(texts) != 2 || !strings.HasPrefix(texts[0], "first E1") || !strings.HasPrefix(texts[1], "second E1") {
				t.Errorf("E1 in the bands: %q", texts)
			}
		} else if len(texts) != 1 || texts[0] != want[at] {
			t.Errorf("%v in the bands: %q, in the sheet %q", at, texts, want[at])
		}
	}
	// With the whole sheet in one band it is the sheet read whole.
	for part, err := range f.Rows(ctx, 1, 1000) {
		if err != nil {
			t.Fatal(err)
		}
		sameCells(t, "one band", cellsOf(t, part), want)
	}
}

// With more than one worker a sheet is decompressed ahead of the scanner,
// on a goroutine of its own. The bands are the same as with one: over the
// fixtures, and without -short over the corpus and the soak set, within
// the limits and with a limit on decompressed bytes that cuts sheets off.
func TestRowsTheSameWithAnyWorkers(t *testing.T) {
	var files []string
	for _, f := range ooxmltest.LoadManifest(t, "fixtures.json") {
		if f.Format == "xlsx" {
			files = append(files, f.AbsPath(t))
		}
	}
	if !testing.Short() {
		filepath.WalkDir(filepath.Join(ooxmltest.Root(t), "testdata", "corpus"), func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".xlsx") {
				files = append(files, path)
			}
			return nil
		})
	}
	bands := func(data []byte, workers int, limits gofastcore.Limits) string {
		f, err := gofastooxml.OpenBytes(data, &gofastcore.Options{Workers: workers, Limits: limits})
		if err != nil {
			return "open: " + err.Error()
		}
		// A hash of the bands, not the bands: Rows holds no more than a
		// band, whatever the sheet, and nor does this.
		out := sha256.New()
		enc := json.NewEncoder(out)
		for _, sheet := range f.Sheets() {
			for part, err := range f.Rows(context.Background(), sheet.Number, 50) {
				if err != nil {
					fmt.Fprintf(out, "error: %v\n", err)
					break
				}
				enc.Encode(part)
			}
		}
		return fmt.Sprintf("%x", out.Sum(nil))
	}
	checked := 0
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil || len(data) > 64<<20 {
			continue
		}
		for _, limits := range []gofastcore.Limits{{}, {MaxDecodedBytes: 300 << 10}} {
			one, many := bands(data, 1, limits), bands(data, 4, limits)
			if one != many {
				t.Errorf("%s, %+v: the bands differ with one worker and with four", path, limits)
			}
		}
		checked++
	}
	t.Logf("%d workbooks", checked)
}
