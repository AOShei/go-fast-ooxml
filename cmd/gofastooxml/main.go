// Command gofastooxml extracts Word, Excel and PowerPoint files into the
// shared document model of the go-fast libraries and prints them as JSON,
// Markdown or plain text.
//
//	gofastooxml [flags] file-or-directory...
//
// A directory stands for the Office files in it and below it. With one
// input the JSON is indented; with several, each document is one line.
// Only the documents go to standard output: everything said about them
// goes to standard error, and the exit status is 1 if any input could
// not be read.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

// The extensions of the files this command picks out of a directory.
var extensions = map[string]bool{
	".docx": true, ".docm": true, ".dotx": true, ".dotm": true,
	".xlsx": true, ".xlsm": true, ".xltx": true, ".xltm": true,
	".pptx": true, ".pptm": true, ".ppsx": true, ".ppsm": true, ".potx": true, ".potm": true,
}

// hiddenFlag is --exclude-hidden: given alone it stands for every
// reason, and with a value for the reasons named.
type hiddenFlag struct{ reasons gofastcore.Hidden }

func (h *hiddenFlag) IsBoolFlag() bool { return true }
func (h *hiddenFlag) String() string {
	if h.reasons == 0 {
		return ""
	}
	return h.reasons.String()
}

func (h *hiddenFlag) Set(v string) error {
	switch v {
	case "true", "all":
		h.reasons = gofastcore.HiddenAny
		return nil
	case "false", "":
		h.reasons = 0
		return nil
	}
	var reasons gofastcore.Hidden
	if err := reasons.UnmarshalText([]byte(strings.ReplaceAll(v, ",", "+"))); err != nil {
		return err
	}
	if !reasons.Valid() || reasons == 0 {
		return fmt.Errorf("unknown reason in %q; the reasons are %s", v, strings.ReplaceAll((gofastcore.HiddenAny&^gofastcore.HiddenUnknown).String(), "+", ", "))
	}
	h.reasons = reasons
	return nil
}

// run is the command: it reads args and writes documents to stdout and
// messages to stderr, and returns the exit status.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gofastooxml", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var (
		format   = flags.String("format", "json", "what to print: json, markdown or text")
		output   = flags.String("o", "", "write to this file instead of standard output")
		parts    = flags.String("parts", "", "parts to read, by number: 3, or 2-5 (the body of a Word document is part 1; sheets and slides count from 1)")
		images   = flags.String("images", "none", "pictures: none, meta (where and how large) or data (their bytes too)")
		spans    = flags.Bool("spans", false, "report bold, italic and the like, and links, as spans")
		workers  = flags.Int("workers", 0, "sheets or slides read at a time (0: one for each CPU)")
		password = flags.String("password", "", "password of an encrypted file")
		maxCells = flags.Int64("max-cells", 0, "cells kept from a workbook or from the tables of a presentation (0: the default, 20 million; -1: no limit)")
		maxBytes = flags.Int64("max-decoded-bytes", 0, "decompressed bytes read from a file (0: the default, 2 GiB; -1: no limit)")
		quiet    = flags.Bool("quiet", false, "say nothing about files that were read; failures are still reported")
		version  = flags.Bool("version", false, "print the version and exit")
		// For Markdown and text.
		furniture = flags.Bool("furniture", false, "in Markdown and text, include running headers and footers")
		markers   = flags.Bool("part-markers", false, "in Markdown and text, mark where each part begins")
	)
	var exclude hiddenFlag
	flags.Var(&exclude, "exclude-hidden", "in Markdown and text, leave out hidden text: all of it, or with =reasons only what is hidden for those reasons, such as tiny,low_contrast (JSON always has everything, flagged)")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: gofastooxml [flags] file-or-directory...")
		fmt.Fprintln(stderr, "Extracts Word, Excel and PowerPoint files. A directory stands for the Office files in it and below it.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *version {
		fmt.Fprintln(stdout, "gofastooxml", moduleVersion())
		return 0
	}
	fail := func(format string, args ...any) int {
		fmt.Fprintf(stderr, "gofastooxml: "+format+"\n", args...)
		return 2
	}
	if flags.NArg() == 0 {
		flags.Usage()
		return 2
	}
	if *format != "json" && *format != "markdown" && *format != "text" {
		return fail("unknown format %q: json, markdown or text", *format)
	}
	opts := gofastcore.Options{Workers: *workers, Spans: *spans, Password: *password}
	opts.Limits.MaxDecodedBytes = *maxBytes
	switch *images {
	case "none":
	case "meta":
		opts.Images = gofastcore.ImagesMeta
	case "data":
		opts.Images = gofastcore.ImagesData
	default:
		return fail("unknown value %q for -images: none, meta or data", *images)
	}
	if *parts != "" {
		first, last, ok := strings.Cut(*parts, "-")
		if !ok {
			last = first
		}
		a, err1 := strconv.Atoi(strings.TrimSpace(first))
		b, err2 := strconv.Atoi(strings.TrimSpace(last))
		if err1 != nil || err2 != nil || a < 1 || b < a {
			return fail("-parts wants a number or a range such as 2-5, not %q", *parts)
		}
		opts.Parts = gofastcore.PartRange{First: a, Last: b}
	}
	extractor := &gofastooxml.Extractor{MaxCells: *maxCells}
	// A file embedded in a document is read too if it is an Office file.
	opts.Child = func(ctx context.Context, name, mime string, r io.ReaderAt, size int64, o *gofastcore.Options) (*gofastcore.Document, error) {
		return extractor.Extract(ctx, r, size, o)
	}

	// The inputs: the files named, and the Office files in the
	// directories named.
	var inputs []string
	status := 0
	for _, arg := range flags.Args() {
		info, err := os.Stat(arg)
		if err != nil {
			fmt.Fprintf(stderr, "gofastooxml: %v\n", err)
			status = 1
			continue
		}
		if !info.IsDir() {
			inputs = append(inputs, arg)
			continue
		}
		var found []string
		err = filepath.WalkDir(arg, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				fmt.Fprintf(stderr, "gofastooxml: %v\n", err)
				status = 1
				return nil
			}
			// Office keeps a lock file named "~$..." beside an open file.
			if !d.IsDir() && extensions[strings.ToLower(filepath.Ext(path))] && !strings.HasPrefix(d.Name(), "~$") {
				found = append(found, path)
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(stderr, "gofastooxml: %v\n", err)
			status = 1
		}
		sort.Strings(found)
		inputs = append(inputs, found...)
	}

	out := stdout
	if *output != "" {
		file, err := os.Create(*output)
		if err != nil {
			fmt.Fprintf(stderr, "gofastooxml: %v\n", err)
			return 1
		}
		defer file.Close()
		out = file
	}
	w := bufio.NewWriter(out)
	defer w.Flush()
	render := &gofastcore.RenderOptions{Furniture: *furniture, PartMarkers: *markers, ExcludeHidden: exclude.reasons}

	written := 0
	for _, path := range inputs {
		if ctx.Err() != nil {
			fmt.Fprintln(stderr, "gofastooxml: interrupted")
			return 1
		}
		o := opts
		o.Name = filepath.Base(path)
		doc, err := extractor.ExtractFile(ctx, path, &o)
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", path, err)
			status = 1
			continue
		}
		if !*quiet {
			if note := remarks(doc); note != "" {
				fmt.Fprintf(stderr, "%s: %s\n", path, note)
			}
		}
		switch *format {
		case "json":
			enc := json.NewEncoder(w)
			enc.SetEscapeHTML(false)
			if len(inputs) == 1 {
				enc.SetIndent("", "  ")
			}
			err = enc.Encode(doc)
		case "markdown":
			if len(inputs) > 1 {
				if written > 0 {
					fmt.Fprintln(w)
				}
				fmt.Fprintf(w, "<!-- %s -->\n\n", strings.ReplaceAll(path, "--", "- -"))
			}
			if err = doc.WriteMarkdown(w, render); err == nil {
				_, err = fmt.Fprintln(w)
			}
		default:
			if len(inputs) > 1 {
				if written > 0 {
					fmt.Fprintln(w)
				}
				fmt.Fprintf(w, "==> %s <==\n", path)
			}
			if err = doc.WriteText(w, render); err == nil {
				_, err = fmt.Fprintln(w)
			}
		}
		if err == nil {
			err = w.Flush()
		}
		if err != nil {
			fmt.Fprintf(stderr, "gofastooxml: writing %s: %v\n", path, err)
			return 1
		}
		written++
	}
	return status
}

// remarks sums up what a document's warnings say that a person running
// the command would want to know without reading the JSON: text that is
// hidden, and anything of severity error.
func remarks(doc *gofastcore.Document) string {
	counts := map[string]int{}
	for _, w := range doc.AllWarnings() {
		if w.Severity == gofastcore.SeverityError || w.Code == gofastcore.WarnTextHidden {
			counts[w.Code]++
		}
	}
	if len(counts) == 0 {
		return ""
	}
	codes := make([]string, 0, len(counts))
	for code := range counts {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for i, code := range codes {
		if n := counts[code]; n > 1 {
			codes[i] = fmt.Sprintf("%s (%d)", code, n)
		}
	}
	return "warnings: " + strings.Join(codes, ", ")
}

// moduleVersion is the version this command was built from.
func moduleVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}
