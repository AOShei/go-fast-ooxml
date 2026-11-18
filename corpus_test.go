package gofastooxml_test

import (
	"os"
	"testing"

	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// The corpus is downloaded, not committed, so files may be absent. Every
// file that is present must be the one the manifest describes: same bytes,
// and a package of the stated format unless the manifest notes why not.
func TestCorpusManifest(t *testing.T) {
	files := ooxmltest.LoadManifest(t, "corpus.json")
	present, sound := 0, 0
	for _, f := range files {
		if f.URL == "" {
			t.Errorf("%s: corpus entry without a URL", f.Name)
		}
		if _, err := os.Stat(f.AbsPath(t)); err != nil {
			continue
		}
		present++
		data := f.Read(t, true)
		format, err := ooxmltest.Sniff(data)
		switch {
		case f.Note != "":
			// Recorded as damaged, encrypted, hostile or not an Office
			// package. Nothing is asserted about how it is classified; it
			// is in the corpus to be survived.
		case err != nil:
			t.Errorf("%s: %v, but the manifest records nothing unusual", f.Name, err)
		case format != f.Format:
			t.Errorf("%s: sniffed %q, manifest says %q", f.Name, format, f.Format)
		default:
			sound++
		}
	}
	t.Logf("corpus: %d listed, %d present, %d sound packages", len(files), present, sound)
	if present == 0 {
		t.Skip("no corpus files downloaded; run python3 testdata/fetch.py")
	}
}
