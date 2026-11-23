package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
)

// The part writer (from go-fast-msbin, whose command shares it) writes the
// bytes json.Encoder writes for a part, for cells that need escaping, for
// text that holds what the writer looks for, for empty and nil rows and
// tables, indented and not, at several depths.
func TestPartIsWrittenAsTheEncoderWritesIt(t *testing.T) {
	rows := [][]string{
		{"plain", "with \"quotes\"", `a\b`, "two\nlines", "a\ttab"},
		{"café", "日本語", "line\u2028separator", "paragraph\u2029separator", "<b>&amp;</b>"},
		{"not \xff UTF-8", "\x01control", "", "\"rows\": []", `"blocks":[]`},
		nil,
		{},
		{"one"},
	}
	parts := []gofastcore.Part{
		{Kind: gofastcore.PartSheet, Number: 1, Name: `"blocks": []`, Blocks: []gofastcore.Block{
			{Kind: gofastcore.KindParagraph, Text: "before\n      \"rows\": []\n    \"blocks\": []", Attrs: map[string]string{"rows": "[]"}},
			{Kind: gofastcore.KindTable, Table: &gofastcore.Table{Rows: rows, HeaderRows: 1, Merges: []gofastcore.Merge{{Row: 0, Col: 0, RowSpan: 1, ColSpan: 2}},
				Cells: []gofastcore.CellDetail{{Row: 0, Col: 1, Formula: "=A1"}}}, Loc: gofastcore.Location{Range: "A1:E6"}},
			{Kind: gofastcore.KindTable, Table: &gofastcore.Table{Rows: [][]string{}}},
			{Kind: gofastcore.KindTable, Table: &gofastcore.Table{}},
			{Kind: gofastcore.KindTable, Table: &gofastcore.Table{Rows: [][]string{{"x"}}}, Display: gofastcore.DisplayHidden},
		}, Warnings: []gofastcore.Warning{{Code: "part.no_text", Severity: gofastcore.SeverityInfo, Message: `"blocks": []`}}},
		{Kind: gofastcore.PartBody, Number: 2, Blocks: []gofastcore.Block{}},
		{Kind: gofastcore.PartBody, Number: 3},
		{Kind: gofastcore.PartSlide, Number: 4, Width: 720, Height: 540, Blocks: []gofastcore.Block{{Kind: gofastcore.KindHeading, Level: 1, Text: "A title"}}},
	}
	for _, indent := range []bool{false, true} {
		for depth := 0; depth < 4; depth++ {
			for i := range parts {
				var want bytes.Buffer
				enc := json.NewEncoder(&want)
				enc.SetEscapeHTML(false)
				if indent {
					enc.SetIndent(strings.Repeat("  ", depth), "  ")
				}
				if err := enc.Encode(&parts[i]); err != nil {
					t.Fatal(err)
				}
				var got bytes.Buffer
				w := bufio.NewWriter(&got)
				if err := newPartWriter(w, indent).part(&parts[i], depth); err != nil {
					t.Fatal(err)
				}
				w.Flush()
				if !bytes.Equal(got.Bytes(), bytes.TrimSuffix(want.Bytes(), []byte("\n"))) {
					t.Errorf("part %d, indent %v, depth %d:\n got %s\nwant %s", parts[i].Number, indent, depth, got.Bytes(), want.Bytes())
				}
			}
		}
	}
}
