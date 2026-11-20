package gofastooxml_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
)

// A Word body or a sheet of opc.AheadAt or more is read in chunks, and
// decompressed ahead of the reader when it has a worker to spare. What a
// document gives is the same with one worker and with four: with no
// limits, with the limit on decompressed bytes cutting the large part or
// what comes after it, with the text and part limits, and where the large
// part is broken, so that the reader stops before its end and the budget
// left for the parts after it is what that stop leaves.
func TestLargePartsTheSameWithAnyWorkers(t *testing.T) {
	child := func(ctx context.Context, name, mime string, r io.ReaderAt, size int64, o *gofastcore.Options) (*gofastcore.Document, error) {
		return gofastooxml.Extract(ctx, r, size, o)
	}
	inner := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "Inner", Data: xlRow(1, xlStr("A1", "inside"))}).Bytes()

	// paras gives Word paragraphs of at least n bytes of XML in all.
	paras := func(n int) string {
		var b strings.Builder
		for i := 0; b.Len() < n; i++ {
			b.WriteString(ooxmltest.Para(fmt.Sprintf("paragraph %d of a long body, with some words in it", i)))
		}
		return b.String()
	}
	// rows gives a sheet's rows of at least n bytes of XML in all.
	rows := func(n int) string {
		var b strings.Builder
		for r := 1; b.Len() < n; r++ {
			b.WriteString(xlRow(r, xlStr(fmt.Sprintf("A%d", r), fmt.Sprintf("row %d", r)), fmt.Sprintf(`<c r="B%d"><v>%d</v></c>`, r, r*7)))
		}
		return b.String()
	}
	size := opc.AheadAt + opc.AheadAt/2
	broken := `<w:p><w:r><w:t>cut</w:r></w:p>`

	word := func(body string) []byte {
		pkg := ooxmltest.Docx(body)
		pkg.Rel("word/document.xml", ooxmltest.RelPackage, "embeddings/book.xlsx")
		pkg.AddBytes("word/embeddings/book.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", inner)
		return pkg.Bytes()
	}
	cases := map[string][]byte{
		"word":        word(paras(size)),
		"word broken": word(paras(size*2/3) + broken + paras(size/3)),
		"excel": ooxmltest.Xlsx(
			ooxmltest.Sheet{Name: "One", Data: rows(size)},
			ooxmltest.Sheet{Name: "Two", Data: rows(size)}).Bytes(),
		"excel broken": ooxmltest.Xlsx(
			ooxmltest.Sheet{Name: "One", Data: rows(size*2/3) + `<row r="x"><c><v>1</c></row>` + rows(size/3)},
			ooxmltest.Sheet{Name: "Two", Data: rows(size)}).Bytes(),
	}
	limits := []gofastcore.Limits{
		{},
		{MaxDecodedBytes: int64(opc.AheadAt) + 300<<10},
		{MaxDecodedBytes: int64(size) + 100<<10},
		{MaxDecodedBytes: 2*int64(size) + 100<<10},
		{MaxTextBytes: 200 << 10},
		{MaxParts: 1},
	}
	for name, data := range cases {
		if largest(t, data) < opc.AheadAt {
			t.Fatalf("%s: no part is large enough to be read ahead", name)
		}
		for _, l := range limits {
			if one, many := extractJSON(data, 1, l, child), extractJSON(data, 4, l, child); one != many {
				t.Errorf("%s, %+v: the document differs with one worker and with four", name, l)
			}
		}
	}

	if testing.Short() {
		return
	}
	checked := 0
	filepath.WalkDir(filepath.Join(ooxmltest.Root(t), "testdata", "corpus"), func(path string, d fs.DirEntry, err error) error {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".docx", ".docm", ".xlsx", ".xlsm":
		default:
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || len(data) > 64<<20 || largest(t, data) < opc.AheadAt {
			return nil
		}
		for _, l := range []gofastcore.Limits{{}, {MaxDecodedBytes: 3 << 20}} {
			if one, many := extractJSON(data, 1, l, child), extractJSON(data, 4, l, child); one != many {
				t.Errorf("%s, %+v: the document differs with one worker and with four", path, l)
			}
		}
		checked++
		return nil
	})
	t.Logf("%d corpus files with a part read ahead", checked)
}

var largePart = regexp.MustCompile(`^(word/document\d*|xl/worksheets/[^/]+)\.xml$`)

// largest gives the size, as the archive gives it, of the largest Word
// body or sheet in a package.
func largest(t *testing.T, data []byte) int {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return 0
	}
	n := 0
	for _, f := range z.File {
		if largePart.MatchString(f.Name) {
			n = max(n, int(min(f.UncompressedSize64, 1<<40)))
		}
	}
	return n
}

func extractJSON(data []byte, workers int, limits gofastcore.Limits, child gofastcore.ChildFunc) string {
	doc, err := gofastooxml.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), &gofastcore.Options{Workers: workers, Limits: limits, Child: child})
	if err != nil {
		return "error: " + err.Error()
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return "json: " + err.Error()
	}
	return string(b)
}
