package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// whole is the JSON of the document read whole, as the command wrote it
// before it wrote a part at a time.
func whole(t testing.TB, e *gofastooxml.Extractor, path string, o *gofastcore.Options, indent bool) ([]byte, error) {
	doc, err := e.ExtractFile(context.Background(), path, o)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if indent {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(doc); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), nil
}

// streamed is the JSON the command writes a part at a time.
func streamed(e *gofastooxml.Extractor, path string, o *gofastcore.Options, indent bool) ([]byte, error) {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	_, _, err := streamFile(context.Background(), e, path, o, w, indent)
	w.Flush()
	return buf.Bytes(), err
}

// sameJSON checks one file under one set of options: the same bytes, or
// the same error and nothing written.
func sameJSON(t *testing.T, path string, o gofastcore.Options, maxCells int64, indent bool) bool {
	t.Helper()
	e := &gofastooxml.Extractor{MaxCells: maxCells}
	o.Child = func(ctx context.Context, name, mime string, r io.ReaderAt, size int64, co *gofastcore.Options) (*gofastcore.Document, error) {
		return e.Extract(ctx, r, size, co)
	}
	o.Name = filepath.Base(path)
	a, errA := whole(t, e, path, &o, indent)
	b, errB := streamed(e, path, &o, indent)
	switch {
	case (errA == nil) != (errB == nil):
		t.Errorf("%s %+v: read whole: %v; a part at a time: %v", path, o.Limits, errA, errB)
		return false
	case errA != nil:
		if errA.Error() != errB.Error() || len(b) > 0 {
			t.Errorf("%s: errors %q and %q, %d bytes written", path, errA, errB, len(b))
			return false
		}
		return true
	case !bytes.Equal(a, b):
		i := 0
		for i < len(a) && i < len(b) && a[i] == b[i] {
			i++
		}
		from := max(i-80, 0)
		t.Errorf("%s (workers %d, limits %+v, indent %v): differ at byte %d of %d and %d:\nwhole:    %q\nstreamed: %q",
			path, o.Workers, o.Limits, indent, i, len(a), len(b), a[from:min(i+80, len(a))], b[from:min(i+80, len(b))])
		return false
	}
	return true
}

// The JSON written a part at a time is the JSON of the document read
// whole, byte for byte: over the fixtures, and without -short over the
// corpus and the soak set, with one worker and with several, indented
// and on one line, and with limits low enough to be met.
func TestStreamedJSONIsTheSame(t *testing.T) {
	var files []string
	for _, f := range ooxmltest.LoadManifest(t, "fixtures.json") {
		files = append(files, f.AbsPath(t))
	}
	if !testing.Short() {
		root := filepath.Join(ooxmltest.Root(t), "testdata", "corpus")
		filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && extensions[strings.ToLower(filepath.Ext(path))] {
				files = append(files, path)
			}
			return nil
		})
	}
	type variant struct {
		opts     gofastcore.Options
		maxCells int64
		indent   bool
	}
	variants := []variant{
		{opts: gofastcore.Options{Workers: 1}, indent: true},
		{opts: gofastcore.Options{}, indent: false},
		{opts: gofastcore.Options{Spans: true, Images: gofastcore.ImagesData, Parts: gofastcore.PartRange{First: 2, Last: 3}}, indent: true},
	}
	// Limits that a good share of files meet, in the parts or in the
	// files they embed.
	tight := gofastcore.Options{}
	tight.Limits.MaxTextBytes = 64 << 10
	tight.Limits.MaxDecodedBytes = 4 << 20
	variants = append(variants, variant{opts: tight, maxCells: 5000})
	differ := 0
	for _, path := range files {
		for _, v := range variants {
			if !sameJSON(t, path, v.opts, v.maxCells, v.indent) {
				differ++
				break
			}
		}
		if differ >= 10 {
			t.Fatal("stopping after 10 files that differ")
		}
	}
	t.Logf("%d files, %d variants each", len(files), len(variants))
}

// The command's own output for one file is the indented JSON of the
// document read whole; for several, one line each.
func TestCommandJSONIsTheSame(t *testing.T) {
	a, b := fixture(t, "basic-pptx.pptx"), fixture(t, "lists.docx")
	e := &gofastooxml.Extractor{}
	opts := gofastcore.Options{}
	opts.Child = func(ctx context.Context, name, mime string, r io.ReaderAt, size int64, o *gofastcore.Options) (*gofastcore.Document, error) {
		return e.Extract(ctx, r, size, o)
	}
	want := func(path string, indent bool) string {
		o := opts
		o.Name = filepath.Base(path)
		out, err := whole(t, e, path, &o, indent)
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	if got, _, _ := command(t, "-quiet", a); got != want(a, true) {
		t.Errorf("one file:\n%s\nwant:\n%s", got, want(a, true))
	}
	if got, _, _ := command(t, "-quiet", a, b); got != want(a, false)+want(b, false) {
		t.Error(fmt.Sprintf("two files: %d bytes, want %d", len(got), len(want(a, false)+want(b, false))))
	}
}
