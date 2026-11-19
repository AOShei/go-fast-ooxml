package gofastooxml_test

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

var speedTargets = flag.Bool("speed", false, "hold the large files to the speed targets too (needs a quiet machine)")

// allocated runs fn a few times and returns the smallest number of
// allocations and of bytes allocated that one run made. The smallest,
// because the runtime's own allocations come and go; a run is alone on
// one processor so that nothing else is counted.
func allocated(fn func()) (allocs, size uint64) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	fn() // what is set up once is not counted
	allocs, size = ^uint64(0), ^uint64(0)
	var before, after runtime.MemStats
	for range 3 {
		runtime.ReadMemStats(&before)
		fn()
		runtime.ReadMemStats(&after)
		allocs = min(allocs, after.Mallocs-before.Mallocs)
		size = min(size, after.TotalAlloc-before.TotalAlloc)
	}
	return allocs, size
}

// What reading costs in allocations grows with the document only as fast
// as the document does, and by little: these are budgets for one more
// paragraph, one more cell, one more slide. Each is the difference
// between a document and one twice its length, so that what a file costs
// whatever its length is left out.
//
// The budgets are about one and a half times what was measured when they
// were set; a change that goes over one has made every large file cost
// more, and has to say why.
func TestAllocationBudgets(t *testing.T) {
	if underRace {
		t.Skip("the race detector allocates too")
	}
	sentence := "The quarterly figures for the northern region were revised after the audit. "
	word := func(n int) []byte {
		var body strings.Builder
		for i := range n {
			body.WriteString(ooxmltest.Para(fmt.Sprintf("Paragraph %d. %s%s", i, sentence, sentence)))
		}
		return ooxmltest.Docx(body.String()).Bytes()
	}
	excel := func(n int) []byte {
		var rows strings.Builder
		for r := 1; r <= n/4; r++ {
			row := strconv.Itoa(r)
			rows.WriteString(xlRow(r, xlNum("A"+row, row), xlStr("B"+row, "region "+row), xlNum("C"+row, "1234.5"), xlStr("D"+row, "revised")))
		}
		return ooxmltest.Xlsx(ooxmltest.Sheet{Name: "S", Data: rows.String()}).Bytes()
	}
	powerpoint := func(n int) []byte {
		slides := make([]ooxmltest.Slide, n)
		for i := range slides {
			slides[i].Shapes = ooxmltest.Shape(2, ph("title", -1), "", ooxmltest.TextBody(dpara("", plain(fmt.Sprintf("Slide %d", i+1))))) +
				box(3, 10, 100, 600, 400, "", dpara("", plain(sentence)), dpara("", plain(sentence)), dpara("", plain(sentence)))
		}
		return deck(slides...).Bytes()
	}
	for _, c := range []struct {
		name, unit string
		build      func(n int) []byte
		n          int
		allocs     float64 // allocations for one more unit
		bytes      float64 // bytes allocated for one more unit
	}{
		{"Word", "paragraph", word, 4000, 1.5, 1800},       // measured: 1.0 and 1,220 (a block and its text)
		{"Excel", "cell", excel, 40000, 0.02, 320},         // measured: 0.004 and 208
		{"PowerPoint", "slide", powerpoint, 200, 95, 7500}, // measured: 63 and 4,980 (a part of the archive, its relationships, its blocks)
	} {
		opts := &gofastcore.Options{Workers: 1}
		cost := func(n int) (float64, float64) {
			data := c.build(n)
			allocs, size := allocated(func() {
				if _, err := gofastooxml.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), opts); err != nil {
					t.Fatal(err)
				}
			})
			return float64(allocs), float64(size)
		}
		a1, b1 := cost(c.n)
		a2, b2 := cost(2 * c.n)
		allocs, size := (a2-a1)/float64(c.n), (b2-b1)/float64(c.n)
		t.Logf("%s: one more %s costs %.3f allocations and %.0f bytes (budgets %.3g and %.0f); a document of %d: %.0f allocations",
			c.name, c.unit, allocs, size, c.allocs, c.bytes, c.n, a1)
		if allocs > c.allocs {
			t.Errorf("%s: %.3f allocations for one more %s, budget %.3g", c.name, allocs, c.unit, c.allocs)
		}
		if size > c.bytes {
			t.Errorf("%s: %.0f bytes allocated for one more %s, budget %.0f", c.name, size, c.unit, c.bytes)
		}
	}
}

// The large generated files, on which the targets of the plan are set:
// what reading each allocates is held under a ceiling, and with -speed
// the time is held to its target too. Time is not held otherwise: it is
// the machine's as much as the library's.
func TestLargeFileBudgets(t *testing.T) {
	if underRace || testing.Short() {
		t.Skip("reads the large files, without the race detector")
	}
	for _, c := range []struct {
		name        string
		allocs      uint64
		megabytes   uint64
		single, all time.Duration // the speed targets: one worker, all
	}{
		{"large.docx", 65_000, 48, 110 * time.Millisecond, 110 * time.Millisecond},
		{"large.xlsx", 12_000, 500, 800 * time.Millisecond, 300 * time.Millisecond},
		{"large.pptx", 215_000, 24, 100 * time.Millisecond, 40 * time.Millisecond},
	} {
		data, err := os.ReadFile(filepath.Join(ooxmltest.Root(t), "testdata", "corpus", "generated", c.name))
		if err != nil {
			t.Skipf("%s is not generated: %v", c.name, err)
		}
		read := func(workers int) func() {
			opts := &gofastcore.Options{Workers: workers}
			return func() {
				if _, err := gofastooxml.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), opts); err != nil {
					t.Fatal(err)
				}
			}
		}
		allocs, size := allocated(read(1))
		t.Logf("%s: %d allocations, %d MB allocated (ceilings %d and %d)", c.name, allocs, size>>20, c.allocs, c.megabytes)
		if allocs > c.allocs || size>>20 > c.megabytes {
			t.Errorf("%s: %d allocations and %d MB, over the ceilings of %d and %d", c.name, allocs, size>>20, c.allocs, c.megabytes)
		}
		if !*speedTargets {
			continue
		}
		for _, mode := range []struct {
			name    string
			workers int
			target  time.Duration
		}{{"one worker", 1, c.single}, {"all workers", 0, c.all}} {
			best := time.Hour
			for range 7 {
				start := time.Now()
				read(mode.workers)()
				best = min(best, time.Since(start))
			}
			t.Logf("%s, %s: %s (target %s)", c.name, mode.name, best.Round(time.Millisecond), mode.target)
			if best > mode.target {
				t.Errorf("%s, %s: %s, over the target of %s", c.name, mode.name, best, mode.target)
			}
		}
	}
}
