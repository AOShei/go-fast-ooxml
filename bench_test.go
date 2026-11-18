package gofastooxml_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// BenchmarkExtract times Extract on the large generated files, the ones the
// speed targets are set on. They are made by testdata/gen/gen.py --large
// and are not tracked; the benchmark skips a file that is absent.
//
// MB/s is of the file as stored. PowerPoint content is not extracted yet,
// so its number is for identifying the file and reading its properties
// only. "single" is one worker; "all" is as many as there are processors,
// which matters where a file has parts that can be read side by side: the
// sheets of a workbook.
func BenchmarkExtract(b *testing.B) {
	for _, name := range []string{"large.docx", "large.xlsx", "large.pptx"} {
		data, err := os.ReadFile(filepath.Join(ooxmltest.Root(b), "testdata", "corpus", "generated", name))
		for _, mode := range []struct {
			name    string
			workers int
		}{{"single", 1}, {"all", 0}} {
			b.Run(name+"/"+mode.name, func(b *testing.B) {
				if err != nil {
					b.Skipf("%s is not generated: %v", name, err)
				}
				opts := &gofastcore.Options{Workers: mode.workers}
				b.SetBytes(int64(len(data)))
				b.ReportAllocs()
				for b.Loop() {
					doc, err := gofastooxml.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), opts)
					if err != nil || len(doc.Parts) == 0 {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// peakHeap runs fn and returns the largest size the heap reached while it
// ran, over what it was before: live objects and garbage not yet
// collected, which is what the process holds.
func peakHeap(fn func()) uint64 {
	const name = "/memory/classes/heap/objects:bytes"
	read := func() uint64 {
		s := []metrics.Sample{{Name: name}}
		metrics.Read(s)
		return s[0].Value.Uint64()
	}
	runtime.GC()
	base := read()
	var peak atomic.Uint64
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(500 * time.Microsecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				if v := read(); v > peak.Load() {
					peak.Store(v)
				}
			}
		}
	}()
	fn()
	close(stop)
	<-done
	if v := read(); v > peak.Load() {
		peak.Store(v)
	}
	if peak.Load() < base {
		return 0
	}
	return peak.Load() - base
}

// TestLargeWorkbookMemory holds the Excel reader to its memory ceiling on
// the large generated workbook: two million cells, 100 MB of XML.
func TestLargeWorkbookMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("reads the large generated workbook")
	}
	data, err := os.ReadFile(filepath.Join(ooxmltest.Root(t), "testdata", "corpus", "generated", "large.xlsx"))
	if err != nil {
		t.Skipf("large.xlsx is not generated: %v", err)
	}
	const ceiling = 300 << 20
	for _, workers := range []int{1, 0} {
		var cells int
		peak := peakHeap(func() {
			doc, err := gofastooxml.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), &gofastcore.Options{Workers: workers})
			if err != nil {
				t.Fatal(err)
			}
			for i := range doc.Parts {
				for _, b := range doc.Parts[i].Blocks {
					cells += len(b.Table.Rows) * len(b.Table.Rows[0])
				}
			}
		})
		t.Logf("workers %d: %d cells, peak heap %d MB", workers, cells, peak>>20)
		if peak > ceiling {
			t.Errorf("workers %d: peak heap %d MB is over the ceiling of %d MB", workers, peak>>20, ceiling>>20)
		}
	}
}
