package ooxmltest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// File is one entry of a manifest: a generated fixture (testdata/fixtures.json,
// file committed) or a corpus file (testdata/corpus.json, file downloaded by
// testdata/fetch.py and not committed).
type File struct {
	// Name identifies the file in test output and is unique in its manifest.
	Name string `json:"name"`
	// Format is docx, xlsx or pptx.
	Format string `json:"format"`
	// Path is relative to testdata/.
	Path string `json:"path"`
	// URL is where a corpus file is downloaded from; empty for fixtures.
	URL    string `json:"url,omitempty"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	// Producer is the application that wrote the file, from the file's own
	// properties: "Microsoft Office Word", "LibreOffice", "python-docx", ...
	Producer string `json:"producer,omitempty"`
	Licence  string `json:"licence"`
	// Expect is the path, relative to testdata/, of the expected content
	// of a generated fixture, in the JSON form of gofastcore.Document.
	Expect string `json:"expect,omitempty"`
	// Variant names the fixture this file is another producer's version
	// of; it then shares that fixture's expected content.
	Variant string `json:"variant,omitempty"`
	// Note says what the file is for, or what is unusual about it.
	Note string `json:"note,omitempty"`
}

// LoadManifest reads testdata/<name> under the repository root.
func LoadManifest(t testing.TB, name string) []File {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(Root(t), "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var files []File
	if err := json.Unmarshal(data, &files); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	seen := map[string]bool{}
	for _, f := range files {
		if f.Name == "" || f.Path == "" || f.SHA256 == "" || f.Licence == "" {
			t.Fatalf("%s: entry %+v lacks a name, path, hash or licence", name, f)
		}
		if seen[f.Name] {
			t.Fatalf("%s: duplicate name %q", name, f.Name)
		}
		seen[f.Name] = true
	}
	return files
}

// Read returns the file's bytes after checking them against the manifest
// hash. A file that is not present is skipped when optional is true (corpus
// files, which each developer downloads) and fails the test otherwise
// (fixtures, which are committed).
func (f File) Read(t testing.TB, optional bool) []byte {
	t.Helper()
	path := filepath.Join(Root(t), "testdata", filepath.FromSlash(f.Path))
	data, err := os.ReadFile(path)
	if err != nil {
		if optional && os.IsNotExist(err) {
			t.Skipf("%s is not downloaded; run testdata/fetch.py", f.Path)
		}
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != f.SHA256 {
		t.Fatalf("%s: sha256 is %s, manifest says %s", f.Path, got, f.SHA256)
	}
	return data
}

// AbsPath returns the file's location on disk.
func (f File) AbsPath(t testing.TB) string {
	return filepath.Join(Root(t), "testdata", filepath.FromSlash(f.Path))
}
