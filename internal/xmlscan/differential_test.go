package xmlscan

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// The differential test holds the tokenizer to encoding/xml on real XML:
// every XML part of every fixture and of every downloaded corpus file. Where
// the standard library reads a part, this package must read it too and see
// the same elements, attributes and text.
//
// Both token streams are written in one plain form. Differences that are by
// design are removed first: how text is split into tokens, and white space
// in attribute values. This package turns literal tabs and line ends there
// into spaces, as XML requires, and keeps ones written as references;
// encoding/xml keeps both. So tabs and line feeds in attribute values are
// compared as spaces on both sides, and the exact rule is left to the unit
// tests.

func ourStream(s *Scanner) ([]byte, error) {
	var out []byte
	for {
		switch s.Next() {
		case Start:
			out = append(out, '\x01', '<')
			out = append(out, s.Name()...)
			for i := 0; i < s.NumAttr(); i++ {
				_, _, value := s.AttrAt(i)
				out = append(out, ' ')
				out = append(out, s.AttrNameAt(i)...)
				out = append(out, '=')
				for _, c := range value {
					if c == '\t' || c == '\n' {
						c = ' '
					}
					out = append(out, c)
				}
				out = append(out, '\x02')
			}
		case End:
			out = append(out, '\x01', '/')
			out = append(out, s.Name()...)
		case Text:
			if n := len(out); n == 0 || out[n-1] != '\x03' {
				out = append(out, '\x01', 'T')
			} else {
				out = out[:n-1] // join with the text token before
			}
			out = append(out, s.Text()...)
			out = append(out, '\x03')
		case EOF:
			return out, s.Err()
		}
	}
}

var attrSpaces = strings.NewReplacer("\t", " ", "\n", " ")

// Reasons the reference reading cannot be compared with. RawToken does not
// check that end tags match start tags, and it reads markup declarations
// (<!ELEMENT ...>, <!ENTITY ...>), which this package refuses.
var (
	errNotNested = errors.New("end tags do not match start tags")
	errDirective = errors.New("input has a markup declaration")
)

func referenceStream(data []byte) ([]byte, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var out []byte
	var open []xml.Name
	name := func(n xml.Name) {
		if n.Space != "" {
			out = append(out, n.Space...)
			out = append(out, ':')
		}
		out = append(out, n.Local...)
	}
	for {
		tok, err := dec.RawToken()
		if err == io.EOF {
			if len(open) > 0 {
				return out, errNotNested
			}
			return out, nil
		}
		if err != nil {
			return out, err
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			open = append(open, tok.Name)
			out = append(out, '\x01', '<')
			name(tok.Name)
			for _, a := range tok.Attr {
				out = append(out, ' ')
				name(a.Name)
				out = append(out, '=')
				out = append(out, attrSpaces.Replace(a.Value)...)
				out = append(out, '\x02')
			}
		case xml.EndElement:
			if len(open) == 0 || open[len(open)-1] != tok.Name {
				return out, errNotNested
			}
			open = open[:len(open)-1]
			out = append(out, '\x01', '/')
			name(tok.Name)
		case xml.Directive:
			return out, errDirective
		case xml.CharData:
			if len(tok) == 0 {
				continue
			}
			if n := len(out); n == 0 || out[n-1] != '\x03' {
				out = append(out, '\x01', 'T')
			} else {
				out = out[:n-1]
			}
			out = append(out, tok...)
			out = append(out, '\x03')
		}
	}
}

// compare returns "" if the two readings of data agree, else a description.
func compare(data []byte) string {
	// encoding/xml returns a byte order mark as text; this package skips it.
	want, refErr := referenceStream(bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF")))
	if refErr != nil {
		// encoding/xml rejects more than this package does (unknown
		// entities, control characters, other encodings). All that is
		// required here is that this package does not crash or hang.
		scanners := []*Scanner{NewBytes(data, nil), NewBytes(data, &Options{Recover: true})}
		if len(data) <= 4<<10 {
			scanners = append(scanners, New(&slowReader{data: data, step: 5}, &Options{BufferSize: 16, Recover: true}))
		}
		for _, s := range scanners {
			for s.Next() != EOF {
			}
			if s.Err() == nil && s.Depth() != 0 {
				return "scan ended cleanly with elements still open"
			}
		}
		return ""
	}
	ways := map[string]*Scanner{
		"bytes":  NewBytes(data, nil),
		"reader": New(bytes.NewReader(data), nil),
	}
	// Five bytes at a time into a 16-byte window makes every construct
	// straddle a refill. It also re-reads a long tag from its start on each
	// refill, which is quadratic, so it is kept to small inputs. A real
	// source hands over kilobytes at a time and does not have that cost.
	if len(data) <= 4<<10 {
		ways["small window"] = New(&slowReader{data: data, step: 5}, &Options{BufferSize: 16})
	}
	for way, s := range ways {
		got, err := ourStream(s)
		if errors.Is(err, ErrDepth) {
			continue // refused on purpose; encoding/xml reads on
		}
		if err != nil {
			return way + ": encoding/xml reads it, this package fails: " + err.Error()
		}
		if !bytes.Equal(got, want) {
			i := 0
			for i < len(got) && i < len(want) && got[i] == want[i] {
				i++
			}
			from := max(0, i-60)
			return way + ": streams differ at byte " + itoa(i) + "\n got ..." + printable(got[from:min(len(got), i+60)]) +
				"\nwant ..." + printable(want[from:min(len(want), i+60)])
		}
	}
	return ""
}

func itoa(i int) string { return strconv.Itoa(i) }

func printable(b []byte) string {
	return strings.NewReplacer("\x01", " | ", "\x02", ";", "\x03", "").Replace(string(b))
}

func TestAgainstEncodingXML(t *testing.T) {
	if testing.Short() {
		t.Skip("reads every XML part of the corpus")
	}
	root := ooxmltest.Root(t)
	var files []string
	for _, manifest := range []string{"fixtures.json", "corpus.json"} {
		for _, f := range ooxmltest.LoadManifest(t, manifest) {
			path := filepath.Join(root, "testdata", filepath.FromSlash(f.Path))
			if _, err := os.Stat(path); err == nil {
				files = append(files, path)
			}
		}
	}

	type result struct{ parts, compared int }
	var (
		mu       sync.Mutex
		total    result
		failures []string
		wg       sync.WaitGroup
		work     = make(chan string)
	)
	for range runtime.GOMAXPROCS(0) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range work {
				data, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
				if err != nil {
					continue
				}
				for _, m := range zr.File {
					lower := strings.ToLower(m.Name)
					if !strings.HasSuffix(lower, ".xml") && !strings.HasSuffix(lower, ".rels") && !strings.HasSuffix(lower, ".vml") {
						continue
					}
					rc, err := m.Open()
					if err != nil {
						continue
					}
					// The limit keeps the corpus's decompression bombs out of memory.
					part, err := io.ReadAll(io.LimitReader(rc, 64<<20))
					rc.Close()
					if err != nil || len(part) == 64<<20 {
						continue
					}
					problem := compare(part)
					mu.Lock()
					total.parts++
					if problem != "" && len(failures) < 10 {
						failures = append(failures, filepath.Base(path)+" "+m.Name+": "+problem)
					}
					mu.Unlock()
				}
			}
		}()
	}
	for _, f := range files {
		work <- f
	}
	close(work)
	wg.Wait()
	for _, f := range failures {
		t.Error(f)
	}
	t.Logf("%d files, %d XML parts compared with encoding/xml", len(files), total.parts)
	if total.parts == 0 {
		t.Fatal("no XML parts found")
	}
}

// FuzzXML holds the tokenizer to encoding/xml on arbitrary input: where the
// standard library reads the input as well-formed XML, this package must
// see the same tokens, through every way of feeding it; on anything else it
// must neither panic nor hang nor end cleanly with elements left open.
func FuzzXML(f *testing.F) {
	for _, seed := range []string{
		``, `<a/>`, `<a x="1" y='2'>text &amp; more<b/><![CDATA[c]]><!-- d --></a>`,
		`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:p xmlns:w="urn:w"><w:r><w:t xml:space="preserve"> x </w:t></w:r></w:p>`,
		"<a>\r\n&#13;&#x1F600;&lt;</a>", `<a><b></a></b>`, `<a x="1>2" y="a&#10;b"/>`, "\xEF\xBB\xBF<a/>",
		"\xFF\xFE<\x00a\x00/\x00>\x00", `<!DOCTYPE a [<!ENTITY b "c">]><a>&b;</a>`, `<a>&nbsp;</a>`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if problem := compare(data); problem != "" {
			t.Fatalf("%s\ninput: %q", problem, data)
		}
	})
}
