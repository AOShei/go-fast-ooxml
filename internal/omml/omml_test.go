package omml_test

import (
	"strings"
	"testing"

	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/omml"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

const head = `<m:oMath xmlns:m="http://schemas.openxmlformats.org/officeDocument/2006/math" xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">`

func linear(t testing.TB, xml string) string {
	t.Helper()
	var s xmlscan.Scanner
	s.ResetBytes([]byte(xml), &xmlscan.Options{Spaces: ns.Spaces})
	for s.Next() != xmlscan.Start {
	}
	out := string(omml.Linear(&s, nil))
	if s.Err() != nil {
		t.Fatalf("%v", s.Err())
	}
	return out
}

func r(text string) string { return `<m:r><m:t>` + text + `</m:t></m:r>` }
func e(x string) string    { return `<m:e>` + x + `</m:e>` }

func TestLinear(t *testing.T) {
	cases := []struct{ name, xml, want string }{
		{"text", r("E=mc"), "E=mc"},
		{"exponent", `<m:sSup>` + e(r("x")) + `<m:sup>` + r("2") + `</m:sup></m:sSup>`, "x^2"},
		{"exponent of a sum", `<m:sSup>` + e(r("e")) + `<m:sup>` + r("-x") + `</m:sup></m:sSup>`, "e^(-x)"},
		{"subscript and exponent", `<m:sSubSup>` + e(r("x")) + `<m:sub>` + r("i") + `</m:sub><m:sup>` + r("10") + `</m:sup></m:sSubSup>`, "x_i^10"},
		{"two letters as a base", `<m:sSub>` + e(r("ab")) + `<m:sub>` + r("1") + `</m:sub></m:sSub>`, "(ab)_1"},
		{"fraction", `<m:f><m:num>` + r("a+b") + `</m:num><m:den>` + r("2") + `</m:den></m:f>`, "(a+b)/2"},
		// What a structure does not have is kept after it: an element of
		// another name, a second of one it has one of.
		{"fraction with a part of no such name", `<m:f><m:nu>` + r("0") + `</m:nu></m:f>`, "/ 0"},
		{"fraction with two numerators", `<m:f><m:num>` + r("a") + `</m:num><m:num>` + r("b") + `</m:num><m:den>` + r("c") + `</m:den></m:f>`, "a/c b"},
		{"subscript with an exponent", `<m:sSub>` + e(r("x")) + `<m:sub>` + r("i") + `</m:sub><m:sup>` + r("2") + `</m:sup></m:sSub>`, "x_i 2"},
		{"linear fraction", `<m:f><m:fPr><m:type m:val="lin"/></m:fPr><m:num>` + r("1") + `</m:num><m:den>` + r("n") + `</m:den></m:f>`, "1∕n"},
		{"binomial", `<m:d><m:e><m:f><m:fPr><m:type m:val="noBar"/></m:fPr><m:num>` + r("n") + `</m:num><m:den>` + r("k") + `</m:den></m:f></m:e></m:d>`, "(n¦k)"},
		{"square root", `<m:rad><m:radPr><m:degHide m:val="1"/></m:radPr><m:deg/>` + e(r("x+1")) + `</m:rad>`, "√(x+1)"},
		{"cube root", `<m:rad><m:deg>` + r("3") + `</m:deg>` + e(r("x")) + `</m:rad>`, "∛x"},
		{"nth root", `<m:rad><m:deg>` + r("n") + `</m:deg>` + e(r("x")) + `</m:rad>`, "√(n&x)"},
		{"sum", `<m:nary><m:naryPr><m:chr m:val="∑"/></m:naryPr><m:sub>` + r("i=1") + `</m:sub><m:sup>` + r("n") + `</m:sup>` +
			e(`<m:sSub>`+e(r("x"))+`<m:sub>`+r("i")+`</m:sub></m:sSub>`) + `</m:nary>`, "∑_(i=1)^n▒(x_i)"},
		{"integral", `<m:nary><m:naryPr><m:subHide m:val="1"/><m:supHide m:val="1"/></m:naryPr><m:sub/><m:sup/>` + e(r("f")) + `</m:nary>`, "∫▒f"},
		{"brackets", `<m:d><m:dPr><m:begChr m:val="["/><m:endChr m:val="]"/></m:dPr>` + e(r("a")) + e(r("b")) + `</m:d>`, "[a│b]"},
		{"no opening bracket", `<m:d><m:dPr><m:begChr m:val=""/><m:endChr m:val="}"/></m:dPr>` + e(r("x")) + `</m:d>`, "x}"},
		{"function", `<m:func><m:fName>` + r("sin") + `</m:fName>` + e(r("θ")) + `</m:func>`, "sin\u2061θ"},
		{"limit", `<m:func><m:fName><m:limLow>` + e(r("lim")) + `<m:lim>` + r("n→∞") + `</m:lim></m:limLow></m:fName>` + e(r("a")) + `</m:func>`,
			"lim┬(n→∞)\u2061a"},
		{"accent", `<m:acc>` + e(r("x")) + `</m:acc>`, "x\u0302"},
		{"overbar", `<m:bar><m:barPr><m:pos m:val="top"/></m:barPr>` + e(r("AB")) + `</m:bar>`, "¯(AB)"},
		{"matrix", `<m:m><m:mr>` + e(r("a")) + e(r("b")) + `</m:mr><m:mr>` + e(r("c")) + e(r("d")) + `</m:mr></m:m>`, "■(a&b@c&d)"},
		{"equation array", `<m:eqArr>` + e(r("x=1")) + e(r("y=2")) + `</m:eqArr>`, "█(x=1@y=2)"},
		{"ordinary text", `<m:r><m:rPr><m:nor/></m:rPr><m:t>if </m:t></m:r>` + r("x>0"), `"if "x>0`},
		{"a structure then a letter", `<m:sSup>` + e(r("x")) + `<m:sup>` + r("k") + `</m:sup></m:sSup><m:sSup>` + e(r("a")) + `<m:sup>` + r("n") + `</m:sup></m:sSup>` + r("+1"), "x^k a^n+1"},
		{"a fraction then brackets", `<m:f><m:num>` + r("1") + `</m:num><m:den>` + r("2") + `</m:den></m:f><m:d>` + e(r("α+β")) + `</m:d>`, "1/2 (α+β)"},
		{"properties are not text", `<m:ctrlPr><w:rPr><w:rFonts w:ascii="Cambria Math"/></w:rPr></m:ctrlPr>` + r("y"), "y"},
	}
	for _, c := range cases {
		if got := linear(t, head+c.xml+`</m:oMath>`); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	// Equations of a display are on lines of their own.
	para := `<m:oMathPara xmlns:m="http://schemas.openxmlformats.org/officeDocument/2006/math"><m:oMathParaPr><m:jc m:val="center"/></m:oMathParaPr>` +
		`<m:oMath>` + r("a=1") + `</m:oMath><m:oMath>` + r("b=2") + `</m:oMath></m:oMathPara>`
	if got := linear(t, para); got != "a=1\nb=2" {
		t.Errorf("display: %q", got)
	}
}

// Every character of the equation's text is in its linear form, in order.
func FuzzLinear(f *testing.F) {
	f.Add(head + `<m:f><m:num>` + r("a") + `</m:num><m:den><m:sSup>` + e(r("b")) + `<m:sup>` + r("2") + `</m:sup></m:sSup></m:den></m:f></m:oMath>`)
	f.Add(head + `<m:nary><m:sub>` + r("i") + `</m:sub>` + e(`<m:d>`+e(r("x"))+`</m:d>`) + `</m:nary></m:oMath>`)
	f.Fuzz(func(t *testing.T, xml string) {
		var s xmlscan.Scanner
		s.ResetBytes([]byte(xml), &xmlscan.Options{Spaces: ns.Spaces})
		for k := s.Next(); k != xmlscan.Start; k = s.Next() {
			if k == xmlscan.EOF {
				return
			}
		}
		out := string(omml.Linear(&s, nil))
		if s.Err() != nil {
			return
		}
		// The text of m:t elements, read again.
		var text strings.Builder
		var t2 xmlscan.Scanner
		t2.ResetBytes([]byte(xml), &xmlscan.Options{Spaces: ns.Spaces})
		for k := t2.Next(); k != xmlscan.EOF; k = t2.Next() {
			if k == xmlscan.Start && t2.Is(ns.Math, "t") && !inProps(xml) {
				text.Write(t2.AppendText(nil))
			}
		}
		if !subsequence(text.String(), out) {
			t.Fatalf("%q is not in %q", text.String(), out)
		}
	})
}

// inProps leaves out inputs with property elements, whose text is not
// equation text.
func inProps(xml string) bool { return strings.Contains(xml, "Pr") }

func subsequence(sub, s string) bool {
	rs := []rune(s)
	i := 0
	for _, c := range sub {
		for i < len(rs) && rs[i] != c {
			i++
		}
		if i == len(rs) {
			return false
		}
		i++
	}
	return true
}
