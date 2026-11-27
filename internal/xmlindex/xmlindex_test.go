package xmlindex

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core/xmlscan"
)

func read(t *testing.T, xml string, opts Options) *Index {
	t.Helper()
	x, err := Read(strings.NewReader(xml), int64(len(xml)), opts)
	if err != nil {
		t.Fatal(err)
	}
	return x
}

func section(t *testing.T, x *Index, e *Element) string {
	t.Helper()
	b, err := io.ReadAll(x.Section(e))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestElements(t *testing.T) {
	xml := `<?xml version="1.0"?><!-- before --><r:root xmlns:r="urn:r" xmlns="urn:d" a="1">` +
		`<sheet xmlns:x="urn:x" Name="One &amp; Two"><x:opts><x:a/></x:opts><table><![CDATA[</table>]]><row/></table></sheet>` +
		`text<r:empty/><sheet Name='2'>x</sheet></r:root>trailing`
	x := read(t, xml, Options{Descend: func(e *Element, depth int) bool { return e.Local == "sheet" }})
	root := x.Root
	if !root.Is("urn:r", "root") || len(root.Children) != 3 || x.Damage != nil {
		t.Fatalf("root %+v, damage %v", root, x.Damage)
	}
	if v, _ := root.Attr("", "a"); v != "1" {
		t.Errorf("attribute a: %q", v)
	}
	one := root.Children[0]
	if !one.Is("urn:d", "sheet") || len(one.Children) != 2 || !one.Children[0].Is("urn:x", "opts") {
		t.Errorf("first sheet: %+v", one)
	}
	if v, _ := one.Attr("", "Name"); v != "One & Two" {
		t.Errorf("name: %q", v)
	}
	if got := section(t, x, one.Children[1]); got != `<table><![CDATA[</table>]]><row/></table>` {
		t.Errorf("table: %s", got)
	}
	// The table is read with the namespaces around it.
	var s xmlscan.Scanner
	table := one.Children[1]
	s.Reset(x.Section(table), &xmlscan.Options{Spaces: map[string]xmlscan.Space{"urn:d": xmlscan.FirstSpace}, Bindings: table.Outer})
	if s.Next(); !s.Is(xmlscan.FirstSpace, "table") {
		t.Errorf("table read as %s in %d", s.Name(), s.Space())
	}
	if got := section(t, x, root.Children[2]); got != `<sheet Name='2'>x</sheet>` {
		t.Errorf("second sheet: %s", got)
	}
	if e := root.Children[1]; !e.Is("urn:r", "empty") || e.End-e.Start != int64(len(`<r:empty/>`)) {
		t.Errorf("empty element: %+v", e)
	}
}

func TestDamageAndRefusals(t *testing.T) {
	xml := `<root><a>1</a><b>2</b><c>3`
	x := read(t, xml, Options{})
	if x.Damage == nil || len(x.Root.Children) != 3 || x.Root.Children[1].End == 0 || x.Root.Children[2].End != 0 {
		t.Errorf("damage %v, children %d", x.Damage, len(x.Root.Children))
	}
	for name, c := range map[string]struct {
		xml string
		err error
	}{
		"empty":     {"", ErrNoRoot},
		"no root":   {`<?xml version="1.0"?><!-- c -->`, ErrNoRoot},
		"DTD":       {`<!DOCTYPE r [<!ENTITY e "x">]><r/>`, xmlscan.ErrDTD},
		"too many":  {`<r><a/><a/><a/></r>`, ErrTooMany},
		"too large": {"\xff\xfe<\x00r\x00r\x00r\x00/\x00>\x00", ErrTooLarge},
	} {
		_, err := Read(strings.NewReader(c.xml), int64(len(c.xml)), Options{MaxElements: 3, MaxCopy: 4})
		if !errors.Is(err, c.err) {
			t.Errorf("%s: %v, want %v", name, err, c.err)
		}
	}
	// A DTD inside an element not gone into is that element's to refuse.
	x = read(t, `<r><a><!DOCTYPE x></a><b/></r>`, Options{})
	if x.Damage != nil || len(x.Root.Children) != 2 {
		t.Errorf("damage %v", x.Damage)
	}
}
