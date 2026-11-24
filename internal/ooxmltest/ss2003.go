package ooxmltest

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/AOShei/go-fast-ooxml/internal/numfmt"
)

// SpreadsheetML2003 writes an .xlsx workbook as Excel 2003 writes an XML
// spreadsheet: its sheets' cells with their values and number formats,
// numbers shown as dates written as dates, merged cells, hidden rows,
// columns and sheets. It reads the package with encoding/xml, apart from
// the library, for the harness to hold the two readings of one workbook
// to each other. What SpreadsheetML 2003 cannot hold, or this does not
// write, is left out: formulas, comments, links, drop-down lists, tables,
// drawings. Cells hidden under a merged range are left out, as Excel
// leaves them out. ok is false for a file it cannot read.
func SpreadsheetML2003(data []byte) (xmlData []byte, ok bool) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, false
	}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[strings.ToLower(strings.TrimLeft(f.Name, "/"))] = f
	}
	read := func(name string) []byte {
		f := files[strings.ToLower(name)]
		if f == nil {
			return nil
		}
		rc, err := f.Open()
		if err != nil {
			return nil
		}
		defer rc.Close()
		b, _ := io.ReadAll(io.LimitReader(rc, 256<<20))
		return b
	}
	// The workbook, its relationships, its sheets.
	var book struct {
		Pr struct {
			Date1904 string `xml:"date1904,attr"`
		} `xml:"workbookPr"`
		Sheets []struct {
			Name  string `xml:"name,attr"`
			State string `xml:"state,attr"`
			ID    string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
		} `xml:"sheets>sheet"`
	}
	if xml.Unmarshal(read("xl/workbook.xml"), &book) != nil || len(book.Sheets) == 0 {
		return nil, false
	}
	date1904 := book.Pr.Date1904 == "1" || book.Pr.Date1904 == "true"
	var rels struct {
		Rel []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
			Type   string `xml:"Type,attr"`
		} `xml:"Relationship"`
	}
	xml.Unmarshal(read("xl/_rels/workbook.xml.rels"), &rels)
	target := map[string]string{}
	for _, r := range rels.Rel {
		t := r.Target
		if strings.HasPrefix(t, "/") {
			t = strings.TrimPrefix(t, "/")
		} else {
			t = path.Join("xl", t)
		}
		target[r.ID] = t
	}
	// Shared strings: the text of each, without its phonetic runs.
	var sst struct {
		SI []struct {
			T string `xml:"t"`
			R []struct {
				T string `xml:"t"`
			} `xml:"r"`
		} `xml:"si"`
	}
	xml.Unmarshal(read("xl/sharedStrings.xml"), &sst)
	strs := make([]string, len(sst.SI))
	for i, si := range sst.SI {
		s := si.T
		for _, r := range si.R {
			s += r.T
		}
		strs[i] = unescapeX(s)
	}
	// Styles: the number format of each cell format.
	var styles struct {
		NumFmts []struct {
			ID   int    `xml:"numFmtId,attr"`
			Code string `xml:"formatCode,attr"`
		} `xml:"numFmts>numFmt"`
		Xfs []struct {
			NumFmt int `xml:"numFmtId,attr"`
			Font   int `xml:"fontId,attr"`
		} `xml:"cellXfs>xf"`
		Fonts []struct {
			Name struct {
				Val string `xml:"val,attr"`
			} `xml:"name"`
		} `xml:"fonts>font"`
	}
	xml.Unmarshal(read("xl/styles.xml"), &styles)
	codes := map[int]string{}
	for _, f := range styles.NumFmts {
		codes[f.ID] = f.Code
	}
	fontOf := func(xf int) string {
		if xf < 0 || xf >= len(styles.Xfs) {
			return ""
		}
		if f := styles.Xfs[xf].Font; f >= 0 && f < len(styles.Fonts) {
			return styles.Fonts[f].Name.Val
		}
		return ""
	}
	codeOf := func(xf int) string {
		if xf < 0 || xf >= len(styles.Xfs) {
			return "General"
		}
		id := styles.Xfs[xf].NumFmt
		if c, ok := codes[id]; ok {
			return c
		}
		if c := numfmt.Builtin(id); c != "" {
			return c
		}
		return "General"
	}

	var body bytes.Buffer
	usedStyles := map[int]bool{}
	for _, sh := range book.Sheets {
		name := target[sh.ID]
		part := read(name)
		if part == nil {
			return nil, false
		}
		ws, ok := readWorksheet(part, strs)
		if !ok {
			return nil, false
		}
		fmt.Fprintf(&body, "<Worksheet ss:Name=\"%s\"><Table>\n", esc(sh.Name))
		for _, c := range ws.cols {
			fmt.Fprintf(&body, "<Column ss:Index=\"%d\" ss:Span=\"%d\" ss:Hidden=\"1\"/>\n", c[0]+1, c[1]-c[0])
		}
		covered := func(r, c int) bool {
			for _, m := range ws.merges {
				if r >= m[0] && r <= m[2] && c >= m[1] && c <= m[3] && (r != m[0] || c != m[1]) {
					return true
				}
			}
			return false
		}
		mergeAt := map[[2]int][4]int{}
		for _, m := range ws.merges {
			mergeAt[[2]int{m[0], m[1]}] = m
		}
		// Rows and their cells, in order; a merged range with nothing in
		// its first cell is written as an empty cell, which holds it.
		for _, r := range ws.rowOrder() {
			cells := ws.rows[r]
			if len(cells) == 0 && !ws.hidden[r] {
				continue
			}
			fmt.Fprintf(&body, "<Row ss:Index=\"%d\"", r+1)
			if ws.hidden[r] {
				body.WriteString(` ss:Hidden="1"`)
			}
			body.WriteString(">")
			lastCol := -1
			for _, c := range cells {
				// A cell under a merged range is written only if it holds
				// something, at its index: a cell with none would be placed
				// after the range.
				hidden := covered(r, c.col)
				if hidden && c.typ == "" {
					continue
				}
				body.WriteString("<Cell")
				if c.col != lastCol+1 || hidden {
					fmt.Fprintf(&body, ` ss:Index="%d"`, c.col+1)
				}
				lastCol = c.col
				if m, ok := mergeAt[[2]int{r, c.col}]; ok {
					if m[3] > m[1] {
						fmt.Fprintf(&body, ` ss:MergeAcross="%d"`, m[3]-m[1])
					}
					if m[2] > m[0] {
						fmt.Fprintf(&body, ` ss:MergeDown="%d"`, m[2]-m[0])
					}
					lastCol = m[3]
				}
				code := codeOf(c.style)
				if code != "General" || fontOf(c.style) != "" {
					usedStyles[c.style] = true
					fmt.Fprintf(&body, ` ss:StyleID="s%d"`, c.style)
				}
				body.WriteString(">")
				typ, value := c.typ, c.value
				if typ == "Number" && code != "General" && numfmt.Parse(code).IsDate() {
					if v, err := strconv.ParseFloat(value, 64); err == nil {
						if d, ok := isoDate(v, date1904); ok {
							typ, value = "DateTime", d
						}
					}
				}
				if typ != "" {
					fmt.Fprintf(&body, `<Data ss:Type="%s">%s</Data>`, typ, esc(value))
				}
				body.WriteString("</Cell>")
			}
			body.WriteString("</Row>\n")
		}
		body.WriteString("</Table>")
		if sh.State == "hidden" || sh.State == "veryHidden" {
			v := "SheetHidden"
			if sh.State == "veryHidden" {
				v = "SheetVeryHidden"
			}
			fmt.Fprintf(&body, `<WorksheetOptions xmlns="urn:schemas-microsoft-com:office:excel"><Visible>%s</Visible></WorksheetOptions>`, v)
		}
		body.WriteString("</Worksheet>\n")
	}
	var out bytes.Buffer
	out.WriteString(`<?xml version="1.0"?>` + "\n" + `<?mso-application progid="Excel.Sheet"?>` + "\n")
	out.WriteString(`<Workbook xmlns="urn:schemas-microsoft-com:office:spreadsheet" xmlns:o="urn:schemas-microsoft-com:office:office" xmlns:x="urn:schemas-microsoft-com:office:excel" xmlns:ss="urn:schemas-microsoft-com:office:spreadsheet" xmlns:html="http://www.w3.org/TR/REC-html40">` + "\n")
	if date1904 {
		out.WriteString(`<ExcelWorkbook xmlns="urn:schemas-microsoft-com:office:excel"><Date1904/></ExcelWorkbook>` + "\n")
	}
	out.WriteString(`<Styles><Style ss:ID="Default" ss:Name="Normal"/>`)
	for i := range styles.Xfs {
		if usedStyles[i] {
			fmt.Fprintf(&out, `<Style ss:ID="s%d"><Font ss:FontName="%s"/><NumberFormat ss:Format="%s"/></Style>`, i, esc(fontOf(i)), esc(codeOf(i)))
		}
	}
	out.WriteString("</Styles>\n")
	out.Write(body.Bytes())
	out.WriteString("</Workbook>\n")
	return out.Bytes(), true
}

type ssCell struct {
	col        int
	style      int
	typ, value string
}

type worksheet struct {
	rows   map[int][]ssCell
	hidden map[int]bool
	cols   [][2]int
	merges [][4]int // r0, c0, r1, c1
}

func (w *worksheet) rowOrder() []int {
	var rows []int
	seen := map[int]bool{}
	for r := range w.rows {
		rows = append(rows, r)
		seen[r] = true
	}
	for r := range w.hidden {
		if !seen[r] {
			rows = append(rows, r)
		}
	}
	slices.Sort(rows)
	return rows
}

// readWorksheet reads a worksheet's cells as SpreadsheetML 2003 types and
// values, its hidden rows and columns and its merged ranges.
func readWorksheet(part []byte, strs []string) (*worksheet, bool) {
	w := &worksheet{rows: map[int][]ssCell{}, hidden: map[int]bool{}}
	d := xml.NewDecoder(bytes.NewReader(part))
	row, col := -1, -1
	var cur *ssCell
	var inV, inT bool
	var text strings.Builder
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, false
		}
		switch t := tok.(type) {
		case xml.StartElement:
			attr := func(name string) (string, bool) {
				for _, a := range t.Attr {
					if a.Name.Local == name && a.Name.Space == "" {
						return a.Value, true
					}
				}
				return "", false
			}
			switch t.Name.Local {
			case "col":
				lo, _ := strconv.Atoi(first(attr("min")))
				hi, _ := strconv.Atoi(first(attr("max")))
				h, _ := attr("hidden")
				if wd, ok := attr("width"); ok && zero(wd) {
					h = "1" // a column of no width
				}
				if (h == "1" || h == "true") && lo >= 1 && hi >= lo && hi <= 16384 {
					w.cols = append(w.cols, [2]int{lo - 1, hi - 1})
				}
			case "row":
				row++
				if v, ok := attr("r"); ok {
					if r, err := strconv.Atoi(v); err == nil && r >= 1 {
						row = r - 1
					}
				}
				col = -1
				if h, _ := attr("hidden"); h == "1" || h == "true" {
					w.hidden[row] = true
				} else if ht, ok := attr("ht"); ok && zero(ht) {
					w.hidden[row] = true // a row of no height
				}
			case "c":
				col++
				if v, ok := attr("r"); ok {
					if c, _, ok := splitRef(v); ok {
						col = c
					}
				}
				cur = &ssCell{col: col}
				cur.style, _ = strconv.Atoi(first(attr("s")))
				cur.typ, _ = attr("t")
				text.Reset()
			case "v":
				inV = true
			case "t":
				inT = cur != nil
			case "rPh":
				d.Skip() // phonetic text, not shown
			case "mergeCell":
				if v, ok := attr("ref"); ok {
					a, b, _ := strings.Cut(v, ":")
					c0, r0, ok1 := splitRef(a)
					c1, r1, ok2 := splitRef(b)
					if ok1 && ok2 {
						w.merges = append(w.merges, [4]int{min(r0, r1), min(c0, c1), max(r0, r1), max(c0, c1)})
					}
				}
			}
		case xml.CharData:
			if inV || inT {
				text.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v":
				inV = false
			case "t":
				inT = false
			case "c":
				if cur == nil || row < 0 {
					continue
				}
				v := text.String()
				switch cur.typ {
				case "s":
					i, err := strconv.Atoi(strings.TrimSpace(v))
					if err != nil || i < 0 || i >= len(strs) {
						v = ""
					} else {
						v = strs[i]
					}
					cur.typ = "String"
				case "inlineStr", "str":
					cur.typ, v = "String", unescapeX(v)
				case "b":
					cur.typ = "Boolean"
					if strings.TrimSpace(v) == "true" {
						v = "1"
					}
				case "e":
					cur.typ = "Error"
				case "d":
					cur.typ = "DateTime"
				default:
					cur.typ = "Number"
					v = strings.TrimSpace(v)
				}
				if v == "" {
					cur.typ = ""
				}
				cur.value = v
				w.rows[row] = append(w.rows[row], *cur)
				cur = nil
			}
		}
	}
	// Cells in order, the first of two at one place.
	for r, cells := range w.rows {
		slices.SortStableFunc(cells, func(a, b ssCell) int { return a.col - b.col })
		kept := cells[:0]
		for i, c := range cells {
			if i > 0 && c.col == cells[i-1].col {
				continue
			}
			kept = append(kept, c)
		}
		w.rows[r] = kept
	}
	return w, true
}

func first(s string, _ bool) string { return s }

func zero(s string) bool {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return err == nil && v == 0
}

// splitRef reads a reference such as B12: column and row, from 0.
func splitRef(s string) (col, row int, ok bool) {
	s = strings.ReplaceAll(s, "$", "")
	i := 0
	for i < len(s) && (s[i] >= 'A' && s[i] <= 'Z' || s[i] >= 'a' && s[i] <= 'z') {
		col = col*26 + int(s[i]|0x20-'a') + 1
		i++
	}
	r, err := strconv.Atoi(s[i:])
	if i == 0 || i > 3 || err != nil || r < 1 {
		return 0, 0, false
	}
	return col - 1, r - 1, true
}

// isoDate writes a serial number as SpreadsheetML 2003 writes a date.
func isoDate(v float64, date1904 bool) (string, bool) {
	if v < 0 || v > 2958465 || math.IsNaN(v) {
		return "", false
	}
	var base time.Time
	switch {
	case date1904:
		base = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)
	case v >= 61:
		base = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	case v >= 60:
		return "", false // 29 February 1900, which was not a day
	default:
		base = time.Date(1899, 12, 31, 0, 0, 0, 0, time.UTC)
	}
	ms := int64(math.Round(v * 86400000))
	t := time.Unix(base.Unix()+ms/1000, (ms%1000)*1e6).UTC()
	return t.Format("2006-01-02T15:04:05.000"), true
}

// unescapeX decodes the _xHHHH_ escapes of text in a package.
func unescapeX(s string) string {
	if !strings.Contains(s, "_x") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if i+7 <= len(s) && s[i] == '_' && s[i+1] == 'x' && s[i+6] == '_' {
			if v, err := strconv.ParseUint(s[i+2:i+6], 16, 16); err == nil {
				b.WriteRune(rune(v))
				i += 6
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func esc(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return strings.ReplaceAll(b.String(), `"`, "&#34;")
}
