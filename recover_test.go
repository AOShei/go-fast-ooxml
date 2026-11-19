package gofastooxml_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// directoryAt returns where the directory of a sound archive begins: the
// end of its last member.
func directoryAt(t testing.TB, data []byte) int {
	t.Helper()
	end := bytes.LastIndex(data, []byte("PK\x05\x06"))
	if end < 0 {
		t.Fatal("no end record")
	}
	return int(binary.LittleEndian.Uint32(data[end+16:]))
}

// repaired returns the document's input.repaired warning, if it has one.
func repaired(doc *gofastcore.Document) (string, bool) {
	for _, w := range doc.Warnings {
		if w.Code == gofastcore.WarnInputRepaired {
			return w.Message, w.Severity == gofastcore.SeverityWarning
		}
	}
	return "", false
}

// withoutRepair returns the document's JSON form without what a recovered
// file differs in: its size and the warning that it was recovered.
func withoutRepair(t testing.TB, doc *gofastcore.Document) string {
	t.Helper()
	copy := *doc
	copy.Source.Size = 0
	copy.Warnings = nil
	for _, w := range doc.Warnings {
		if w.Code != gofastcore.WarnInputRepaired {
			copy.Warnings = append(copy.Warnings, w)
		}
	}
	return asJSON(t, &copy)
}

// A file cut short in transit has lost the directory at its end, and
// with it what a zip reader goes by. The members are still there, each
// behind its own header: a file that lost only its directory gives the
// document it gave whole, and says how it was read.
func TestArchiveWithoutItsDirectory(t *testing.T) {
	for _, s := range samples(t) {
		opts := &gofastcore.Options{Child: childExtractor, Workers: 3}
		whole, err := extract(t, s.data, opts)
		if err != nil {
			t.Fatal(err)
		}
		at := directoryAt(t, s.data)
		// Cut exactly there, in the middle of the directory, and one
		// byte before the end.
		for _, cut := range []int{at, at + (len(s.data)-at)/2, len(s.data) - 1} {
			doc, err := extract(t, s.data[:cut], opts)
			if err != nil {
				t.Errorf("%s cut at %d of %d: %v", s.name, cut, len(s.data), err)
				continue
			}
			ooxmltest.CheckDocument(t, doc)
			message, warning := repaired(doc)
			if !warning || !strings.Contains(message, "cut short") || strings.Contains(message, "breaks off") {
				t.Errorf("%s cut at %d: warning %q", s.name, cut, message)
			}
			if withoutRepair(t, doc) != withoutRepair(t, whole) {
				t.Errorf("%s cut at %d: the document differs from the whole file's", s.name, cut)
			}
		}
		if _, was := repaired(whole); was {
			t.Errorf("%s: a sound file says it was recovered", s.name)
		}
	}
}

// A file cut anywhere before that gives what its remaining members hold:
// a document, with the part that breaks off cut there and saying so, or,
// where too little is left to tell what the file is, the error for a
// damaged file. Never a panic, never a document without the warning.
func TestArchiveCutAnywhere(t *testing.T) {
	read, refused := 0, 0
	for _, s := range samples(t) {
		whole, err := extract(t, s.data, nil)
		if err != nil {
			t.Fatal(err)
		}
		all := len(whole.Text())
		at := directoryAt(t, s.data)
		step := max(at/60, 1)
		longest := 0
		for cut := 0; cut < at; cut += step {
			var doc *gofastcore.Document
			var err error
			within(t, 5*time.Second, s.name, func() { doc, err = extract(t, s.data[:cut], nil) })
			checkOutcome(t, s.name+" cut", doc, err)
			if err != nil {
				if !errors.Is(err, gofastcore.ErrMalformed) {
					t.Errorf("%s cut at %d: %v", s.name, cut, err)
				}
				refused++
				continue
			}
			read++
			if _, warning := repaired(doc); !warning {
				t.Errorf("%s cut at %d: a document with no input.repaired warning", s.name, cut)
			}
			if n := len(doc.Text()); n > all {
				t.Errorf("%s cut at %d: %d bytes of text, the whole file has %d", s.name, cut, n, all)
			} else {
				longest = max(longest, n)
			}
		}
		// Cut late, most of the text is still read.
		if all > 200 && longest < all/2 {
			t.Errorf("%s: at most %d of %d bytes of text were read from a cut file", s.name, longest, all)
		}
	}
	t.Logf("%d cut files gave a document, %d were refused", read, refused)
	if read < refused/4 {
		t.Errorf("only %d of %d cut files gave a document", read, read+refused)
	}
}

// A Word document cut in the middle of its body gives the paragraphs
// before the cut, and the body says it is cut off.
func TestWordBodyCutShort(t *testing.T) {
	var body strings.Builder
	for i := range 3000 {
		// Text that does not compress to nothing, so that the cut falls
		// well inside the body.
		body.WriteString(ooxmltest.Para("Paragraph " + strings.Repeat(string(rune('a'+i%26)), 1+i%17) + " " + strings.Repeat("x", i%7)))
	}
	data := ooxmltest.Docx(body.String()).Bytes()
	whole, err := extract(t, data, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Where the body's member is in the archive.
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var start, length int64
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			start, _ = f.DataOffset()
			length = int64(f.CompressedSize64)
		}
	}
	if length < 2000 {
		t.Fatalf("the body is %d bytes compressed", length)
	}
	doc, err := extract(t, data[:start+length/2], nil)
	if err != nil {
		t.Fatal(err)
	}
	ooxmltest.CheckDocument(t, doc)
	message, _ := repaired(doc)
	got, all := len(doc.Parts[0].Blocks), len(whole.Parts[0].Blocks)
	if !strings.Contains(message, "word/document.xml, breaks off") || got < all/4 || got >= all || !doc.Parts[0].Has(gofastcore.WarnPartFailed) {
		t.Errorf("%d of %d paragraphs; warnings %q, %v", got, all, message, doc.Parts[0].Warnings)
	}
	// What was read is the beginning of the document, in order.
	for i, b := range doc.Parts[0].Blocks[:got-1] {
		if b.Text != whole.Parts[0].Blocks[i].Text {
			t.Fatalf("paragraph %d is %q, in the whole file %q", i+1, b.Text, whole.Parts[0].Blocks[i].Text)
		}
	}
}

// Recovery reads what the limits allow and no more.
func TestRecoveryIsBounded(t *testing.T) {
	// Members whose length follows them are decompressed to find where
	// they end: a bomb among them meets the decompression budget.
	pkg := ooxmltest.Docx(ooxmltest.Para("x"))
	pkg.AddBytes("word/media/zeros.bin", "application/octet-stream", make([]byte, 64<<20))
	data := pkg.Bytes()
	cut := data[:directoryAt(t, data)]
	within(t, 20*time.Second, "a bomb in a file without a directory", func() {
		_, err := extract(t, cut, &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: 1 << 20}})
		if !errors.Is(err, gofastcore.ErrLimitExceeded) {
			t.Errorf("over the decompression budget: %v", err)
		}
	})
	if doc, err := extract(t, cut, nil); err != nil || !strings.Contains(doc.Text(), "x") {
		t.Errorf("within the default budget: %v", err)
	}
	// More members than may be in an archive.
	many := ooxmltest.Docx(ooxmltest.Para("x"))
	for i := range 50 {
		many.AddBytes("word/media/"+strings.Repeat("a", i+1)+".bin", "application/octet-stream", []byte{1})
	}
	data = many.Bytes()
	e := &gofastooxml.Extractor{MaxArchiveMembers: 20}
	cut = data[:directoryAt(t, data)]
	if _, err := e.Extract(context.Background(), bytes.NewReader(cut), int64(len(cut)), nil); !errors.Is(err, gofastcore.ErrLimitExceeded) {
		t.Errorf("more members than allowed: %v", err)
	}
	// Bytes that only begin like an archive.
	for _, junk := range [][]byte{
		[]byte("PK\x03\x04"),
		append([]byte("PK\x03\x04"), make([]byte, 26)...),
		append([]byte("PK\x03\x04"), bytes.Repeat([]byte{0xFF}, 300)...),
		append([]byte("PK\x03\x04\x14\x00\x08\x00\x08\x00"), bytes.Repeat([]byte("PK\x03\x04"), 500)...),
	} {
		doc, err := extract(t, junk, nil)
		if doc != nil || !errors.Is(err, gofastcore.ErrMalformed) {
			t.Errorf("%d bytes that begin like an archive: %v", len(junk), err)
		}
	}
}
