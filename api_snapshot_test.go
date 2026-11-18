package gofastooxml_test

import (
	"bytes"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

// TestAPISnapshot holds the package's exported declarations to
// testdata/api.txt, so that the public surface changes only on purpose:
// a difference fails, and -update records the new surface for review.
func TestAPISnapshot(t *testing.T) {
	root := ooxmltest.Root(t)
	fset := token.NewFileSet()
	names, err := filepath.Glob(filepath.Join(root, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	pkg, err := doc.NewFromFiles(fset, files, "github.com/AOShei/go-fast-ooxml")
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	decl := func(node any) {
		// The declaration alone: its comments are documentation, not
		// surface.
		ast.Inspect(node.(ast.Node), func(n ast.Node) bool {
			switch d := n.(type) {
			case *ast.FuncDecl:
				d.Body, d.Doc = nil, nil
			case *ast.GenDecl:
				d.Doc = nil
			case *ast.ValueSpec:
				d.Doc, d.Comment = nil, nil
			case *ast.TypeSpec:
				d.Doc, d.Comment = nil, nil
			case *ast.Field:
				d.Doc, d.Comment = nil, nil
			}
			return true
		})
		if err := printer.Fprint(&out, fset, node); err != nil {
			t.Fatal(err)
		}
		out.WriteString("\n")
	}
	values := func(list []*doc.Value) {
		for _, v := range list {
			decl(v.Decl)
		}
	}
	funcs := func(list []*doc.Func) {
		for _, f := range list {
			decl(f.Decl)
		}
	}
	out.WriteString("package " + pkg.Name + "\n\n")
	values(pkg.Consts)
	values(pkg.Vars)
	funcs(pkg.Funcs)
	for _, typ := range pkg.Types {
		out.WriteString("\n")
		decl(typ.Decl)
		values(typ.Consts)
		values(typ.Vars)
		funcs(typ.Funcs)
		funcs(typ.Methods)
	}

	// Blank lines inside a declaration are where its comments were: they
	// are not surface either.
	var lines []string
	inside := false
	for _, line := range strings.Split(out.String(), "\n") {
		switch {
		case strings.HasSuffix(line, "(") || strings.HasSuffix(line, "{"):
			inside = true
		case line == ")" || line == "}":
			inside = false
		case line == "" && inside:
			continue
		}
		lines = append(lines, line)
	}
	out.Reset()
	out.WriteString(strings.Join(lines, "\n"))

	path := filepath.Join(root, "testdata", "api.txt")
	if ooxmltest.Updating() {
		if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no recorded surface (run with -update): %v", err)
	}
	if !bytes.Equal(bytes.TrimSpace(want), bytes.TrimSpace(out.Bytes())) {
		t.Errorf("the exported declarations differ from testdata/api.txt; if the change is meant, run with -update and review the diff\n%s", out.String())
	}
}
