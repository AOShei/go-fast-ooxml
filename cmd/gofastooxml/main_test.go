package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// command runs the command and returns what it wrote and its exit status.
func command(t *testing.T, args ...string) (stdout, stderr string, status int) {
	t.Helper()
	var out, errs bytes.Buffer
	status = run(context.Background(), args, &out, &errs)
	return out.String(), errs.String(), status
}

func fixture(t *testing.T, name string) string {
	return filepath.Join(ooxmltest.Root(t), "testdata", "fixtures", name)
}

// documents decodes standard output as JSON documents, one after
// another, and checks each.
func documents(t *testing.T, stdout string) []*gofastcore.Document {
	t.Helper()
	var docs []*gofastcore.Document
	dec := json.NewDecoder(strings.NewReader(stdout))
	for dec.More() {
		doc := new(gofastcore.Document)
		if err := dec.Decode(doc); err != nil {
			t.Fatalf("standard output is not JSON documents: %v\n%.300s", err, stdout)
		}
		if err := doc.Validate(); err != nil {
			t.Errorf("%s: %v", doc.Source.Name, err)
		}
		docs = append(docs, doc)
	}
	return docs
}

func TestOneFileAsJSON(t *testing.T) {
	stdout, stderr, status := command(t, fixture(t, "basic-pptx.pptx"))
	if status != 0 {
		t.Fatalf("status %d: %s", status, stderr)
	}
	docs := documents(t, stdout)
	if len(docs) != 1 || docs[0].Source.Format != "pptx" || docs[0].Source.Name != "basic-pptx.pptx" || len(docs[0].Parts) != 5 {
		t.Fatalf("documents: %+v", docs)
	}
	// One document is indented, for people; and nothing but the document
	// is on standard output.
	if !strings.HasPrefix(stdout, "{\n  \"schema_version\"") || strings.Count(strings.TrimSpace(stdout), "\n") < 20 {
		t.Errorf("standard output begins %.60q", stdout)
	}
	// Standard error says only what a person would want to know: here,
	// nothing, as the file has no warning of severity error.
	if stderr != "" {
		t.Errorf("standard error: %q", stderr)
	}
	if _, stderr, _ := command(t, "-quiet", fixture(t, "basic-pptx.pptx")); stderr != "" {
		t.Errorf("with -quiet: %q", stderr)
	}
}

func TestSeveralInputs(t *testing.T) {
	dir := t.TempDir()
	// A directory with Office files in it and below it, and other things.
	copyTo := func(name, to string) {
		data, err := os.ReadFile(fixture(t, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(to, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	copyTo("lists.docx", filepath.Join(dir, "b", "lists.DOCX"))
	copyTo("values.xlsx", filepath.Join(dir, "a.xlsx"))
	copyTo("basic-pptx.pptx", filepath.Join(dir, "b", "c", "deck.pptx"))
	copyTo("values.xlsx", filepath.Join(dir, "b", "~$values.xlsx")) // the lock file Office leaves beside an open file
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not an Office file"), 0o644)

	stdout, stderr, status := command(t, "-quiet", fixture(t, "tables.docx"), dir)
	if status != 0 {
		t.Fatalf("status %d: %s", status, stderr)
	}
	// Each document is one line, in the order of the arguments, and
	// within a directory by path.
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	docs := documents(t, stdout)
	var names []string
	for _, doc := range docs {
		names = append(names, doc.Source.Name)
	}
	if got := strings.Join(names, " "); got != "tables.docx a.xlsx deck.pptx lists.DOCX" || len(lines) != 4 {
		t.Errorf("%d lines, documents %q", len(lines), got)
	}

	// A file that cannot be read does not stop the others, and is the
	// reason for the exit status.
	os.WriteFile(filepath.Join(dir, "broken.docx"), []byte("not a zip archive at all"), 0o644)
	stdout, stderr, status = command(t, dir, filepath.Join(dir, "absent.docx"))
	if status != 1 || len(documents(t, stdout)) != 3 {
		t.Errorf("status %d, %d documents", status, len(documents(t, stdout)))
	}
	if !strings.Contains(stderr, "broken.docx: ") || !strings.Contains(stderr, "absent.docx") {
		t.Errorf("standard error: %q", stderr)
	}
}

func TestMarkdownAndText(t *testing.T) {
	stdout, _, status := command(t, "-format", "markdown", "-part-markers", fixture(t, "basic-pptx.pptx"))
	if status != 0 {
		t.Fatal(status)
	}
	for _, want := range []string{"<!-- slide 1: Project Update -->", "# Project Update", "## Autumn 2026", "- Status\n    - Milestones reached", "| Region | Q1 | Q2 |", "Backup slide content", "Text placed beside the slide"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("Markdown lacks %q:\n%s", want, stdout)
		}
	}
	// Text the file declares not shown is in the output unless it is
	// asked to be left out: all of it, or what has the reasons named. Text
	// placed beside the slide has no reason: positions are not judged.
	stdout, _, _ = command(t, "-format", "markdown", "-exclude-display", fixture(t, "basic-pptx.pptx"))
	if strings.Contains(stdout, "Backup slide content") || !strings.Contains(stdout, "Text placed beside the slide") || !strings.Contains(stdout, "# Agenda") {
		t.Errorf("with -exclude-display:\n%s", stdout)
	}
	stdout, _, _ = command(t, "-format", "text", "-exclude-display=no_ink", fixture(t, "basic-pptx.pptx"))
	if !strings.Contains(stdout, "Backup slide content") {
		t.Errorf("with -exclude-display=no_ink:\n%s", stdout)
	}
	// Several inputs: each is headed by its name.
	stdout, _, _ = command(t, "-quiet", "-format", "text", fixture(t, "lists.docx"), fixture(t, "layout.xlsx"))
	first, second := strings.Index(stdout, "==> "+fixture(t, "lists.docx")+" <=="), strings.Index(stdout, "==> "+fixture(t, "layout.xlsx")+" <==")
	if first != 0 || second < first {
		t.Errorf("headings at %d and %d:\n%.200s", first, second, stdout)
	}
	stdout, _, _ = command(t, "-quiet", "-format", "markdown", fixture(t, "lists.docx"), fixture(t, "layout.xlsx"))
	if !strings.HasPrefix(stdout, "<!-- "+fixture(t, "lists.docx")+" -->\n") {
		t.Errorf("Markdown of two files begins %.80q", stdout)
	}
	// Running headers and footers are left out of rendered text unless
	// asked for.
	deck := ooxmltest.Pptx(ooxmltest.Slide{Shapes: ooxmltest.TextShape(2, 10, 10, 200, 40, "Slide text")}).
		Master("", ooxmltest.TextShape(9, 10, 500, 200, 20, "Confidential"), "", "")
	path := filepath.Join(t.TempDir(), "deck.pptx")
	os.WriteFile(path, deck.Bytes(), 0o644)
	without, _, _ := command(t, "-format", "text", path)
	with, _, _ := command(t, "-format", "text", "-furniture", path)
	if strings.Contains(without, "Confidential") || !strings.Contains(with, "Confidential") || !strings.Contains(without, "Slide text") {
		t.Errorf("without -furniture %q, with %q", without, with)
	}
}

func TestOptions(t *testing.T) {
	// Parts by number.
	stdout, _, status := command(t, "-quiet", "-parts", "2-3", fixture(t, "basic-pptx.pptx"))
	docs := documents(t, stdout)
	if status != 0 || len(docs[0].Parts) != 2 || docs[0].Parts[0].Number != 2 {
		t.Errorf("-parts 2-3: status %d, parts %+v", status, docs[0].Parts)
	}
	stdout, _, _ = command(t, "-quiet", "-parts", "4", fixture(t, "basic-pptx.pptx"))
	if docs := documents(t, stdout); len(docs[0].Parts) != 1 || docs[0].Parts[0].Number != 4 {
		t.Errorf("-parts 4: %+v", docs[0].Parts)
	}
	if _, stderr, status := command(t, "-parts", "9", fixture(t, "basic-pptx.pptx")); status != 1 || !strings.Contains(stderr, "5 slides") {
		t.Errorf("-parts 9: status %d, %q", status, stderr)
	}

	// Pictures, and the files embedded in a document.
	inner := ooxmltest.Xlsx(ooxmltest.Sheet{Name: "Inner", Data: `<row r="1"><c r="A1" t="inlineStr"><is><t>inside the workbook</t></is></c></row>`}).Bytes()
	pkg := ooxmltest.Docx(ooxmltest.Para("See the picture.") +
		`<w:p><w:r><w:drawing><wp:inline><wp:extent cx="914400" cy="457200"/><wp:docPr id="1" name="Picture 1" descr="A picture"/>` +
		`<a:graphic><a:graphicData><pic:pic><pic:blipFill><a:blip r:embed="rId9"/></pic:blipFill></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>`)
	for pkg.Rel("word/document.xml", ooxmltest.RelImage, "media/image1.png") != "rId9" {
	}
	pkg.AddBytes("word/media/image1.png", "image/png", []byte("\x89PNG\r\n\x1a\nnot really a picture"))
	pkg.Rel("word/document.xml", ooxmltest.RelPackage, "embeddings/book.xlsx")
	pkg.AddBytes("word/embeddings/book.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", inner)
	path := filepath.Join(t.TempDir(), "report.docx")
	os.WriteFile(path, pkg.Bytes(), 0o644)
	images := func(mode string) (blocks, withBytes int, doc *gofastcore.Document) {
		args := []string{"-quiet", path}
		if mode != "" {
			args = append([]string{"-images", mode}, args...)
		}
		stdout, stderr, status := command(t, args...)
		if status != 0 {
			t.Fatalf("-images %s: status %d: %s", mode, status, stderr)
		}
		doc = documents(t, stdout)[0]
		for _, b := range doc.Parts[0].Blocks {
			if b.Image != nil {
				blocks++
				if len(b.Image.Data) > 0 {
					withBytes++
				}
			}
		}
		return blocks, withBytes, doc
	}
	if blocks, withBytes, _ := images(""); blocks != 1 || withBytes != 0 {
		t.Errorf("without -images: %d picture blocks, %d with bytes", blocks, withBytes)
	}
	if blocks, _, _ := images("none"); blocks != 0 {
		t.Errorf("-images none: %d picture blocks", blocks)
	}
	if blocks, withBytes, _ := images("meta"); blocks != 1 || withBytes != 0 {
		t.Errorf("-images meta: %d blocks, %d with bytes", blocks, withBytes)
	}
	blocks, withBytes, doc := images("data")
	if blocks != 1 || withBytes != 1 {
		t.Errorf("-images data: %d blocks, %d with bytes", blocks, withBytes)
	}
	if len(doc.Children) != 1 || doc.Children[0].Source.Format != "xlsx" || !strings.Contains(doc.Children[0].Text(), "inside the workbook") {
		t.Errorf("the embedded workbook: %+v", doc.Children)
	}

	// Formatting as spans.
	bold := filepath.Join(t.TempDir(), "bold.docx")
	os.WriteFile(bold, ooxmltest.Docx(`<w:p><w:r><w:t xml:space="preserve">Plain and </w:t></w:r><w:r><w:rPr><w:b/></w:rPr><w:t>bold</w:t></w:r></w:p>`).Bytes(), 0o644)
	if stdout, _, _ := command(t, "-quiet", bold); len(documents(t, stdout)[0].Parts[0].Blocks[0].Spans) != 0 {
		t.Error("spans without -spans")
	}
	stdout, _, _ = command(t, "-quiet", "-spans", bold)
	spans := 0
	for _, b := range documents(t, stdout)[0].Parts[0].Blocks {
		spans += len(b.Spans)
	}
	if spans == 0 {
		t.Error("-spans gave no spans")
	}

	// Limits.
	stdout, stderr, status := command(t, "-max-cells", "4", fixture(t, "values.xlsx"))
	if status != 0 || !documents(t, stdout)[0].Parts[0].Has(gofastcore.WarnLimitExceeded) || !strings.Contains(stderr, "limit.exceeded") {
		t.Errorf("-max-cells 4: status %d, %q", status, stderr)
	}
	if _, stderr, status := command(t, "-max-decoded-bytes", "100", fixture(t, "values.xlsx")); status != 1 || !strings.Contains(stderr, "values.xlsx: ") {
		t.Errorf("-max-decoded-bytes 100: status %d, %q", status, stderr)
	}

	// To a file.
	out := filepath.Join(t.TempDir(), "out.json")
	stdout, _, status = command(t, "-quiet", "-o", out, fixture(t, "lists.docx"))
	written, err := os.ReadFile(out)
	if status != 0 || stdout != "" || err != nil || len(documents(t, string(written))) != 1 {
		t.Errorf("-o: status %d, standard output %q, file %v", status, stdout, err)
	}
	if stdout, _, status := command(t, "-version"); status != 0 || !strings.HasPrefix(stdout, "gofastooxml ") {
		t.Errorf("-version: %d %q", status, stdout)
	}
}

func TestPassword(t *testing.T) {
	path := filepath.Join(ooxmltest.Root(t), "testdata", "encrypted", "basic-docx.agile.docx")
	stdout, stderr, status := command(t, "-password", "correct horse", path)
	if status != 0 || stderr != "" {
		t.Fatalf("status %d: %s", status, stderr)
	}
	doc := documents(t, stdout)[0]
	if doc.Source.Format != "docx" || !strings.Contains(doc.Text(), "Quarterly Report") || len(doc.Warnings) != 1 || doc.Warnings[0].Code != "ooxml.decrypted" {
		t.Errorf("format %q, warnings %v", doc.Source.Format, doc.Warnings)
	}
	// Without the password, or with another, the file is not read, and
	// the reason is the one a person needs.
	for password, want := range map[string]string{"": "password required", "guess": "wrong password"} {
		stdout, stderr, status := command(t, "-password", password, path)
		if status != 1 || stdout != "" || !strings.Contains(stderr, want) {
			t.Errorf("password %q: status %d, standard error %q", password, status, stderr)
		}
	}
}

func TestUsageErrors(t *testing.T) {
	file := fixture(t, "lists.docx")
	for name, args := range map[string][]string{
		"no input":                   {},
		"an unknown flag":            {"-nonsense", file},
		"an unknown format":          {"-format", "pdf", file},
		"an unknown picture mode":    {"-images", "all", file},
		"a range that is not one":    {"-parts", "five", file},
		"a range the wrong way":      {"-parts", "5-2", file},
		"a reason that is not one":   {"-exclude-display=sneaky", file},
		"a reason beside a real one": {"-exclude-display=hidden,sneaky", file},
		"a reason no longer known":   {"-exclude-display=tiny", file},
	} {
		stdout, stderr, status := command(t, args...)
		if status != 2 || stdout != "" || stderr == "" {
			t.Errorf("%s: status %d, standard output %q, standard error %q", name, status, stdout, stderr)
		}
	}
	if _, stderr, status := command(t, "-h"); status != 0 || !strings.Contains(stderr, "usage: gofastooxml") {
		t.Errorf("-h: status %d, %q", status, stderr)
	}
}

func TestInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errs bytes.Buffer
	if status := run(ctx, []string{fixture(t, "lists.docx")}, &out, &errs); status != 1 || out.Len() != 0 {
		t.Errorf("status %d, standard output %q, standard error %q", status, out.String(), errs.String())
	}
}

// Every file of the corpus through the command: what it prints is a valid
// document, or it prints nothing for the file, says why, and exits 1.
func TestCorpusThroughTheCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("reads the whole corpus")
	}
	read, refused := 0, 0
	for _, f := range ooxmltest.LoadManifest(t, "corpus.json") {
		path := f.AbsPath(t)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		stdout, stderr, status := command(t, "-quiet", path)
		switch status {
		case 0:
			if len(documents(t, stdout)) != 1 {
				t.Errorf("%s: no document", f.Name)
			}
			read++
		case 1:
			if stdout != "" || !strings.Contains(stderr, path+": ") {
				t.Errorf("%s: refused with standard output %.80q and standard error %q", f.Name, stdout, stderr)
			}
			refused++
		default:
			t.Errorf("%s: status %d", f.Name, status)
		}
	}
	t.Logf("%d files read, %d refused", read, refused)
	if read == 0 {
		t.Skip("no corpus files downloaded")
	}
}
