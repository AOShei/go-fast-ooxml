// Command gooxml extracts the text of every file in a list with
// go-fast-ooxml, for the batch comparison (bench/batch/README.md): one
// process, WORKERS goroutines reading the files in turn (interleaved), each
// file with one worker of its own (Options.Workers 1, as the commands do
// with -j above 1); the time of each call (Extract and the text rendering,
// after the bytes are read), whether it failed, and the length of the text.
// The text goes to OUTDIR/text/NAME.txt, one line a file to
// OUTDIR/results.jsonl. The garbage collector is set as usual, by GOGC in
// the environment. GOOXML_CPUPROFILE and GOOXML_MEMPROFILE name files for
// a CPU profile and the allocations of the run.
//
//	gooxml LIST OUTDIR WORKERS
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strconv"
	"sync"
	"time"

	gofastcore "github.com/AOShei/go-fast-core"
	gofastooxml "github.com/AOShei/go-fast-ooxml"
)

type result struct {
	File  string  `json:"file"`
	S     float64 `json:"s"`
	Err   *string `json:"err"`
	Chars int     `json:"chars"`
}

func main() {
	// GOOXML_CPUPROFILE=file writes a CPU profile of the run.
	if path := os.Getenv("GOOXML_CPUPROFILE"); path != "" {
		pf, err := os.Create(path)
		if err != nil {
			panic(err)
		}
		pprof.StartCPUProfile(pf)
		defer pprof.StopCPUProfile()
	}
	// GOOXML_MEMPROFILE=file writes the allocations of the run.
	if path := os.Getenv("GOOXML_MEMPROFILE"); path != "" {
		defer func() {
			pf, err := os.Create(path)
			if err != nil {
				panic(err)
			}
			pprof.Lookup("allocs").WriteTo(pf, 0)
			pf.Close()
		}()
	}
	list, out := os.Args[1], os.Args[2]
	workers, _ := strconv.Atoi(os.Args[3])
	f, err := os.Open(list)
	if err != nil {
		panic(err)
	}
	var files []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if sc.Text() != "" {
			files = append(files, sc.Text())
		}
	}
	os.MkdirAll(filepath.Join(out, "text"), 0o755)
	extractor := &gofastooxml.Extractor{}
	res := make([]result, len(files))
	var wg sync.WaitGroup
	for k := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := k; i < len(files); i += workers {
				b, err := os.ReadFile(files[i])
				if err != nil {
					panic(err)
				}
				name := filepath.Base(files[i])
				t0 := time.Now()
				doc, err := extractor.Extract(context.Background(), bytes.NewReader(b), int64(len(b)), &gofastcore.Options{Workers: 1, Name: name})
				var text string
				if err == nil {
					text = doc.Text()
				}
				r := result{File: name, S: time.Since(t0).Seconds(), Chars: len([]rune(text))}
				if err != nil {
					s := fmt.Sprint(err)
					r.Err = &s
				} else {
					os.WriteFile(filepath.Join(out, "text", name+".txt"), []byte(text), 0o644)
				}
				res[i] = r
			}
		}()
	}
	wg.Wait()
	w, _ := os.Create(filepath.Join(out, "results.jsonl"))
	defer w.Close()
	enc := json.NewEncoder(w)
	for _, r := range res {
		enc.Encode(r)
	}
}
