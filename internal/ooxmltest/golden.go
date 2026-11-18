package ooxmltest

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files with the current output")

// Updating reports whether the test run was asked to rewrite stored results
// (golden files, recorded baselines) instead of comparing with them.
func Updating() bool { return *update }

// Golden compares got with the golden file testdata/golden/<name> under
// root, the repository root. With -update it rewrites the file instead;
// the change is then reviewed in the diff.
func Golden(t testing.TB, root, name string, got []byte) {
	t.Helper()
	path := filepath.Join(root, "testdata", "golden", filepath.FromSlash(name))
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden file missing (run with -update to create it): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("output differs from %s (run with -update and review the diff)\n--- got ---\n%s\n--- want ---\n%s",
			path, clip(got), clip(want))
	}
}

func clip(b []byte) []byte {
	const limit = 2000
	if len(b) > limit {
		return append(append([]byte{}, b[:limit]...), "\n[...]"...)
	}
	return b
}

// Root returns the repository root, found by walking up from the working
// directory to the directory that holds go.mod.
func Root(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the working directory")
		}
		dir = parent
	}
}
