package gofastooxml_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// The packages that read a file's content work on the bytes they are
// given and on nothing else: they open no file, start no program and
// reach no network. They cannot, because they do not import the packages
// that would let them. Only the public package opens a file, in
// ExtractFile, and the test harness runs other programs.
func TestReadersImportNothingThatReachesOutside(t *testing.T) {
	root := filepath.Join(ooxmltest.Root(t), "internal")
	dirs, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, dir := range dirs {
		if !dir.IsDir() || dir.Name() == "ooxmltest" {
			continue
		}
		files, err := filepath.Glob(filepath.Join(root, dir.Name(), "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			checked++
			for _, imp := range parsed.Imports {
				path, _ := strconv.Unquote(imp.Path.Value)
				for _, banned := range []string{"os", "net", "syscall", "unsafe", "plugin", "runtime/debug"} {
					if path == banned || strings.HasPrefix(path, banned+"/") {
						t.Errorf("internal/%s/%s imports %q", dir.Name(), filepath.Base(file), path)
					}
				}
			}
		}
	}
	if checked < 30 {
		t.Errorf("only %d files were checked", checked)
	}
}
