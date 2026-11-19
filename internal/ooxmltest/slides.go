package ooxmltest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// LOSlide is a slide as LibreOffice imports it: the paragraphs of each
// named group of shapes on it (a SmartArt diagram is imported as a group
// named as its frame is), and all its paragraphs.
type LOSlide struct {
	Groups map[string][]string
	All    []string
}

// LibreOfficeSlidesOK converts a presentation to flat ODF with
// LibreOffice and returns its slides, or false if LibreOffice cannot open
// it in time. The result is cached as the other conversions are.
func LibreOfficeSlidesOK(t testing.TB, pptx []byte) ([]LOSlide, bool) {
	t.Helper()
	bin, err := exec.LookPath("soffice")
	if err != nil {
		t.Skip("LibreOffice (soffice) is not installed")
	}
	out, ok := convertSlides(bin, pptx, 90*time.Second)
	if !ok {
		return nil, false
	}
	slides, err := parseFODP(bytes.NewReader(out))
	return slides, err == nil
}

// WarmLibreOfficeSlides converts many presentations at once into the cache.
func WarmLibreOfficeSlides(t testing.TB, files [][]byte) {
	t.Helper()
	bin, err := exec.LookPath("soffice")
	if err != nil {
		t.Skip("LibreOffice (soffice) is not installed")
	}
	Warm(files, func(data []byte) { convertSlides(bin, data, 90*time.Second) })
}

func convertSlides(bin string, pptx []byte, timeout time.Duration) ([]byte, bool) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	sum := sha256.Sum256(append([]byte("soffice:fodp\x00"), pptx...))
	dir := filepath.Join(base, "gofastooxml-oracle", hex.EncodeToString(sum[:16]))
	if os.MkdirAll(dir, 0o755) != nil {
		return nil, false
	}
	if _, err := os.Stat(filepath.Join(dir, "done")); err != nil {
		in := filepath.Join(dir, "input.pptx")
		if os.WriteFile(in, pptx, 0o644) != nil {
			return nil, false
		}
		runContained(timeout, bin, "-env:UserInstallation=file://"+filepath.Join(dir, "profile"),
			"--headless", "--norestore", "--convert-to", "fodp", "--outdir", dir, in)
		tidy(dir)
		os.Remove(in)
		os.WriteFile(filepath.Join(dir, "done"), nil, 0o644)
	}
	out, err := os.ReadFile(filepath.Join(dir, "input.fodp"))
	return out, err == nil
}

const (
	odfDraw = "urn:oasis:names:tc:opendocument:xmlns:drawing:1.0"
	odfText = "urn:oasis:names:tc:opendocument:xmlns:text:1.0"
)

// parseFODP reads the slides of a flat ODF presentation: the paragraphs
// on each page, and those of each named group.
func parseFODP(r io.Reader) ([]LOSlide, error) {
	dec := xml.NewDecoder(r)
	var slides []LOSlide
	var groups []string // names of the groups the reader is inside
	var para *strings.Builder
	inPage, notes := false, 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return slides, nil
		}
		if err != nil {
			return slides, err
		}
		switch e := tok.(type) {
		case xml.StartElement:
			switch {
			case e.Name.Space == odfDraw && e.Name.Local == "page":
				inPage = true
				slides = append(slides, LOSlide{Groups: map[string][]string{}})
			case e.Name.Space == "urn:oasis:names:tc:opendocument:xmlns:presentation:1.0" && e.Name.Local == "notes":
				notes++
			case inPage && e.Name.Space == odfDraw && e.Name.Local == "g":
				name := ""
				for _, a := range e.Attr {
					if a.Name.Space == odfDraw && a.Name.Local == "name" {
						name = a.Value
					}
				}
				groups = append(groups, name)
			case inPage && notes == 0 && e.Name.Space == odfText && e.Name.Local == "p":
				para = &strings.Builder{}
			case para != nil && e.Name.Space == odfText && (e.Name.Local == "s" || e.Name.Local == "tab"):
				para.WriteByte(' ')
			case para != nil && e.Name.Space == odfText && e.Name.Local == "line-break":
				para.WriteByte('\n')
			}
		case xml.CharData:
			if para != nil {
				para.Write(e)
			}
		case xml.EndElement:
			switch {
			case e.Name.Space == odfDraw && e.Name.Local == "page":
				inPage = false
			case e.Name.Space == "urn:oasis:names:tc:opendocument:xmlns:presentation:1.0" && e.Name.Local == "notes":
				notes--
			case inPage && e.Name.Space == odfDraw && e.Name.Local == "g" && len(groups) > 0:
				groups = groups[:len(groups)-1]
			case para != nil && e.Name.Space == odfText && e.Name.Local == "p":
				text := strings.TrimSpace(para.String())
				para = nil
				if text == "" || len(slides) == 0 {
					continue
				}
				sl := &slides[len(slides)-1]
				sl.All = append(sl.All, text)
				if len(groups) > 0 && groups[0] != "" {
					sl.Groups[groups[0]] = append(sl.Groups[groups[0]], text)
				}
			}
		}
	}
}
