package gofastooxml_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// rangeWithin reports whether range a (A1 notation) lies inside range b.
func rangeWithin(t testing.TB, a, b string) bool {
	ab := strings.SplitN(a, ":", 2)
	bb := strings.SplitN(b, ":", 2)
	if len(ab) == 1 {
		ab = append(ab, ab[0])
	}
	if len(bb) == 1 {
		bb = append(bb, bb[0])
	}
	a0, a1, b0, b1 := parseA1(t, ab[0]), parseA1(t, ab[1]), parseA1(t, bb[0]), parseA1(t, bb[1])
	return a0.row >= b0.row && a0.col >= b0.col && a1.row <= b1.row && a1.col <= b1.col
}

// Pivot tables and pivot caches against openpyxl: each pivot table whose
// region has cells is a table block with its name and role pivot, inside
// the range the pivot table says it fills; each cache whose source is not
// in the workbook is a table in the stored part with its fields as the
// header row and a row for each record.
func TestPivotsAsOpenpyxl(t *testing.T) {
	if testing.Short() {
		t.Skip("reads the corpus")
	}
	if _, ok := ooxmltest.PythonBin(t); !ok {
		t.Skip("the Python comparison environment is not set up")
	}
	var contents [][]byte
	var names []string
	for _, f := range ooxmltest.LoadManifest(t, "corpus.json") {
		if f.Format != "xlsx" || f.Note != "" {
			continue
		}
		if data, err := os.ReadFile(f.AbsPath(t)); err == nil && bytes.Contains(data, []byte("pivotTable")) {
			contents, names = append(contents, data), append(names, f.Name)
		}
	}
	if os.Getenv("GOFAST_PIVOTS_SOAK") != "" {
		root := filepath.Join(ooxmltest.Root(t), "testdata", "corpus", "soak")
		filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			ext := strings.ToLower(filepath.Ext(path))
			if err != nil || d.IsDir() || ext != ".xlsx" && ext != ".xlsm" {
				return nil
			}
			if data, err := os.ReadFile(path); err == nil && bytes.Contains(data, []byte("pivotTable")) && len(data) < 32<<20 {
				rel, _ := filepath.Rel(root, path)
				contents, names = append(contents, data), append(names, "soak/"+rel)
			}
			return nil
		})
	}
	if len(contents) == 0 {
		t.Skip("no workbooks with pivot tables")
	}
	ooxmltest.WarmPython(t, "openpyxl-pivots", "xlsx", contents)
	var pivots, found, empty, caches, cachesFound int
	var notes []string
	for i, data := range contents {
		doc, err := extract(t, data, nil)
		if err != nil {
			continue
		}
		res := ooxmltest.Python(t, "openpyxl-pivots", data, "xlsx")
		if res.Error != "" {
			continue
		}
		var oracle struct {
			Sheets []struct {
				Sheet  string `json:"sheet"`
				Pivots []struct {
					Name string `json:"name"`
					Ref  string `json:"ref"`
				} `json:"pivots"`
			} `json:"sheets"`
			Caches []struct {
				Fields  []string `json:"fields"`
				Records *int     `json:"records"`
			} `json:"caches"`
		}
		if json.Unmarshal([]byte(res.Text), &oracle) != nil {
			continue
		}
		parts := map[string]*gofastcore.Part{}
		var stored *gofastcore.Part
		for k := range doc.Parts {
			p := &doc.Parts[k]
			parts[p.Name] = p
			if p.Kind == gofastcore.PartStored {
				stored = p
			}
		}
		for _, sh := range oracle.Sheets {
			part := parts[sh.Sheet]
			if part == nil {
				continue
			}
			for _, pv := range sh.Pivots {
				if pv.Ref == "" {
					continue
				}
				pivots++
				ok, cells := false, false
				for _, b := range part.Blocks {
					if b.Table == nil || b.Loc.Range == "" || b.Attrs[gofastcore.AttrRole] == gofastcore.RoleChart {
						continue
					}
					if rangeWithin(t, b.Loc.Range, pv.Ref) {
						cells = true
						if b.Attrs[gofastcore.AttrRole] == gofastcore.RolePivot && b.Attrs[gofastcore.AttrName] == pv.Name {
							ok = true
						}
					}
				}
				switch {
				case ok:
					found++
				case !cells:
					empty++ // laid out when the file is opened: no cells yet
				default:
					notes = append(notes, fmt.Sprintf("%s %s: pivot table %q at %s not found", names[i], sh.Sheet, pv.Name, pv.Ref))
				}
			}
		}
		used := map[int]bool{}
		for _, c := range oracle.Caches {
			caches++
			hit := false
			if stored != nil {
				for k, b := range stored.Blocks {
					if used[k] || b.Attrs[gofastcore.AttrStored] != gofastcore.StoredPivotCache || b.Table == nil {
						continue
					}
					if strings.Join(b.Table.Rows[0], "\x00") != strings.Join(c.Fields, "\x00") {
						continue
					}
					if c.Records != nil && len(b.Table.Rows)-1 != *c.Records {
						continue
					}
					used[k], hit = true, true
					break
				}
			}
			if hit {
				cachesFound++
			} else {
				notes = append(notes, fmt.Sprintf("%s: a cache of fields %q (records %v) not in the stored part", names[i], c.Fields, c.Records))
			}
		}
	}
	t.Logf("%d workbooks: %d pivot tables, %d found, %d with no cells yet; %d caches with their source gone, %d in the stored part", len(contents), pivots, found, empty, caches, cachesFound)
	sort.Strings(notes)
	for k, n := range notes {
		if k == 40 {
			t.Logf("... and %d more", len(notes)-k)
			break
		}
		t.Log(n)
	}
	if len(notes) > 0 {
		t.Errorf("%d differences from openpyxl", len(notes))
	}
}

// A pivot table is the cells it fills, as one table with its name and
// role pivot. A cache whose source is a sheet of the workbook is not
// read again; one whose source is a connection is, in the stored part,
// flagged: its records, with values by number taken from the field's
// shared items, or, with no records kept, the values of each field.
func TestPivotTablesAndCaches(t *testing.T) {
	const relBase = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/"
	data := xlRow(1, xlStr("A1", "Region"), xlStr("B1", "Sales")) + xlRow(2, xlStr("A2", "North"), xlNum("B2", "10")) +
		xlRow(5, xlStr("D5", "Row Labels"), xlStr("E5", "Sum of Sales")) + xlRow(6, xlStr("D6", "North"), xlNum("E6", "10")) +
		xlRow(7, xlStr("D7", "Grand Total"), xlNum("E7", "10"))
	pkg := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "Data", Data: data})
	pkg.Rel(firstSheet, relBase+"pivotTable", "../pivotTables/pivotTable1.xml")
	pkg.Add("xl/pivotTables/pivotTable1.xml", "application/vnd.openxmlformats-officedocument.spreadsheetml.pivotTable+xml",
		`<pivotTableDefinition xmlns="`+ooxmltest.NSSheet+`" name="SalesPivot" cacheId="1"><location ref="D5:E7" firstHeaderRow="1" firstDataRow="1" firstDataCol="1"/></pivotTableDefinition>`)
	cacheDef := func(source, fields string, records bool) string {
		rid := ""
		if records {
			rid = ` r:id="rId1"`
		}
		return `<pivotCacheDefinition xmlns="` + ooxmltest.NSSheet + `" xmlns:r="` + ooxmltest.NSRel + `"` + rid + `>` + source + `<cacheFields>` + fields + `</cacheFields></pivotCacheDefinition>`
	}
	ct := "application/vnd.openxmlformats-officedocument.spreadsheetml.pivotCacheDefinition+xml"
	// 1: from a sheet of the workbook. 2: from a connection, with records.
	// 3: from another workbook's sheet that is not here, no records.
	pkg.Add("xl/pivotCache/pivotCacheDefinition1.xml", ct, cacheDef(`<cacheSource type="worksheet"><worksheetSource ref="A1:B2" sheet="Data"/></cacheSource>`,
		`<cacheField name="Region"><sharedItems/></cacheField><cacheField name="Margin" formula="Sales*0.1" databaseField="0"><sharedItems/></cacheField>`, false))
	pkg.Add("xl/pivotCache/pivotCacheDefinition2.xml", ct, cacheDef(`<cacheSource type="external" connectionId="1"/>`,
		`<cacheField name="Customer"><sharedItems><s v="Ada"/><s v="Grace"/></sharedItems></cacheField><cacheField name="Spent"><sharedItems containsNumber="1"/></cacheField>`+
			`<cacheField name="When"><sharedItems/></cacheField><cacheField name="Paid"><sharedItems/></cacheField>`, true))
	pkg.Rel("xl/pivotCache/pivotCacheDefinition2.xml", relBase+"pivotCacheRecords", "pivotCacheRecords2.xml")
	pkg.Add("xl/pivotCache/pivotCacheRecords2.xml", "application/vnd.openxmlformats-officedocument.spreadsheetml.pivotCacheRecords+xml",
		`<pivotCacheRecords xmlns="`+ooxmltest.NSSheet+`"><r><x v="1"/><n v="1250.5"/><d v="2026-03-04T00:00:00"/><b v="1"/></r><r><x v="0"/><n v="7"/><d v="2026-03-05T14:30:00"/><m/></r></pivotCacheRecords>`)
	pkg.Add("xl/pivotCache/pivotCacheDefinition3.xml", ct, cacheDef(`<cacheSource type="worksheet"><worksheetSource ref="A1:A3" sheet="Archive"/></cacheSource>`,
		`<cacheField name="Code"><sharedItems><s v="X1"/><s v="Y2"/></sharedItems></cacheField><cacheField name="Kind"><sharedItems><s v="old"/></sharedItems></cacheField>`, false))
	var caches strings.Builder
	for i := 1; i <= 3; i++ {
		id := pkg.Rel("xl/workbook.xml", relBase+"pivotCacheDefinition", fmt.Sprintf("pivotCache/pivotCacheDefinition%d.xml", i))
		fmt.Fprintf(&caches, `<pivotCache cacheId="%d" r:id="%s"/>`, i, id)
	}
	pkg.Add("xl/workbook.xml", ooxmltest.CTWorkbook, `<workbook xmlns="`+ooxmltest.NSSheet+`" xmlns:r="`+ooxmltest.NSRel+`"><sheets><sheet name="Data" sheetId="1" r:id="rId1"/></sheets><pivotCaches>`+caches.String()+`</pivotCaches></workbook>`)
	doc := excel(t, pkg, nil)
	expectSheets(t, doc,
		`sheet 1 "Data"`,
		`  table A1:B2 inferred [["Region" "Sales"] ["North" "10"]]`,
		`  table D5:E7 name=SalesPivot role=pivot header=1 [["Row Labels" "Sum of Sales"] ["North" "10"] ["Grand Total" "10"]]`,
		`stored 2 ""`,
		`  table  stored=pivot_cache header=1 [["Customer" "Spent" "When" "Paid"] ["Grace" "1250.5" "2026-03-04" "TRUE"] ["Ada" "7" "2026-03-05T14:30:00" ""]]`,
		`  table  stored=pivot_cache header=1 [["Code" "Kind"] ["X1" "old"] ["Y2" ""]]`,
		`  paragraph  "Margin: =Sales*0.1" name=Margin stored=pivot_cache`,
	)
}
