package xmlscan

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"unicode/utf16"
)

const (
	nsA = FirstSpace + iota
	nsB
	nsC
)

var testSpaces = map[string]Space{"urn:a": nsA, "urn:b": nsB, "urn:c": nsC}

// dump renders the token stream in a compact form: <name@space attr=value>,
// </name>, "text". Adjacent Text tokens are joined, since how text is cut
// into tokens is not part of the contract.
func dump(s *Scanner) (string, error) {
	var sb strings.Builder
	var text []byte
	flush := func() {
		if text != nil {
			fmt.Fprintf(&sb, "%q ", text)
			text = nil
		}
	}
	for {
		switch s.Next() {
		case Start:
			flush()
			fmt.Fprintf(&sb, "<%s@%d", s.Name(), s.Space())
			for i := 0; i < s.NumAttr(); i++ {
				space, local, value := s.AttrAt(i)
				fmt.Fprintf(&sb, " %s@%d=%q", local, space, value)
			}
			sb.WriteString("> ")
		case End:
			flush()
			fmt.Fprintf(&sb, "</%s> ", s.Name())
		case Text:
			text = append(text, s.Text()...)
		case EOF:
			flush()
			return strings.TrimSpace(sb.String()), s.Err()
		}
	}
}

// eachWay runs a scan of the same input through every way of feeding it:
// all at once, and through readers that deliver it in small pieces into
// small windows, which exercises refilling and chunking.
func eachWay(t *testing.T, input []byte, opts *Options, check func(name string, s *Scanner)) {
	t.Helper()
	check("bytes", NewBytes(input, opts))
	for _, size := range []int{1, 2, 3, 7, 16, 64, 4096} {
		o := Options{}
		if opts != nil {
			o = *opts
		}
		o.BufferSize = max(size, 4)
		check(fmt.Sprintf("reader/%d", size), New(&slowReader{data: input, step: size}, &o))
	}
}

// slowReader hands out its data a few bytes at a time.
type slowReader struct {
	data []byte
	step int
}

func (r *slowReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := min(r.step, len(p), len(r.data))
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

func TestTokens(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"element", `<a></a>`, `<a@0> </a>`},
		{"empty element", `<a/>`, `<a@0> </a>`},
		{"empty element with space", `<a />`, `<a@0> </a>`},
		{"nested", `<a><b><c/></b></a>`, `<a@0> <b@0> <c@0> </c> </b> </a>`},
		{"text", `<a>hello world</a>`, `<a@0> "hello world" </a>`},
		{"text around children", `<a>x<b/>y</a>`, `<a@0> "x" <b@0> </b> "y" </a>`},
		{"whitespace is text", "<a> <b/>\n</a>", `<a@0> " " <b@0> </b> "\n" </a>`},
		{"end tag with space", `<a></a >`, `<a@0> </a>`},
		{"attributes", `<a x="1" y='2'/>`, `<a@0 x@0="1" y@0="2"> </a>`},
		{"attribute spacing", "<a  x = \"1\"\n\ty='2' />", `<a@0 x@0="1" y@0="2"> </a>`},
		{"empty attribute", `<a x=""/>`, `<a@0 x@0=""> </a>`},
		{"quote inside other quote", `<a x="it's" y='say "hi"'/>`, `<a@0 x@0="it's" y@0="say \"hi\""> </a>`},
		{"> inside attribute", `<a x="1>2"/>`, `<a@0 x@0="1>2"> </a>`},
		{"/ inside attribute", `<a x="a/b"/>`, `<a@0 x@0="a/b"> </a>`},
		{"predefined entities", `<a>&lt;&gt;&amp;&quot;&apos;</a>`, `<a@0> "<>&\"'" </a>`},
		{"entities in attribute", `<a x="&lt;&amp;&#65;&#x42;"/>`, `<a@0 x@0="<&AB"> </a>`},
		{"decimal reference", `<a>&#233;</a>`, `<a@0> "é" </a>`},
		{"hex reference", `<a>&#xE9;&#Xe9;</a>`, `<a@0> "éé" </a>`},
		{"astral reference", `<a>&#x1F600;</a>`, `<a@0> "😀" </a>`},
		{"reference to line feed", `<a>a&#10;b</a>`, `<a@0> "a\nb" </a>`},
		{"cdata", `<a><![CDATA[<b>&amp;]]></a>`, `<a@0> "<b>&amp;" </a>`},
		{"cdata between text", `<a>x<![CDATA[y]]>z</a>`, `<a@0> "xyz" </a>`},
		{"empty cdata", `<a><![CDATA[]]></a>`, `<a@0> </a>`},
		{"cdata with brackets", `<a><![CDATA[a]]b]]]></a>`, `<a@0> "a]]b]" </a>`},
		{"comment", `<a><!-- <b> -- > --></a>`, `<a@0> </a>`},
		{"comment between text", `<a>x<!--c-->y</a>`, `<a@0> "xy" </a>`},
		{"declaration and pi", `<?xml version="1.0"?><?pi stuff?><a/>`, `<a@0> </a>`},
		{"text after root", "<a/>\n", `<a@0> </a> "\n"`},
		{"crlf in text", "<a>x\r\ny\rz</a>", `<a@0> "x\ny\nz" </a>`},
		{"crlf in cdata", "<a><![CDATA[x\r\ny]]></a>", `<a@0> "x\ny" </a>`},
		{"whitespace in attribute becomes space", "<a x=\"1\t2\n3\r4\"/>", `<a@0 x@0="1 2 3 4"> </a>`},
		{"crlf in attribute is one space", "<a x=\"1\r\n2\r\r\n3\"/>", `<a@0 x@0="1 2  3"> </a>`},
		{"reference in attribute stays", `<a x="1&#10;2"/>`, `<a@0 x@0="1\n2"> </a>`},
		{"utf-8", `<a é="ü">日本語 😀</a>`, `<a@0 é@0="ü"> "日本語 😀" </a>`},
		{"byte order mark", "\xEF\xBB\xBF<a/>", `<a@0> </a>`},
		// Leniency: references that cannot be decoded are kept as written.
		{"unknown entity", `<a>&nbsp;x</a>`, `<a@0> "&nbsp;x" </a>`},
		{"bare ampersand", `<a>R&D & more</a>`, `<a@0> "R&D & more" </a>`},
		{"bad character reference", `<a>&#xZZ;&#;</a>`, `<a@0> "&#xZZ;&#;" </a>`},
		{"reference to nothing valid", `<a>&#0;&#xD800;&#x110000;</a>`, "<a@0> \"\uFFFD\uFFFD\uFFFD\" </a>"},
	}
	for _, c := range cases {
		eachWay(t, []byte(c.in), nil, func(way string, s *Scanner) {
			got, err := dump(s)
			if err != nil || got != c.want {
				t.Errorf("%s (%s):\n got %s (err %v)\nwant %s", c.name, way, got, err, c.want)
			}
		})
	}
}

func TestNamespaces(t *testing.T) {
	opts := &Options{Spaces: testSpaces}
	cases := []struct{ name, in, want string }{
		{"default namespace", `<r xmlns="urn:a"><c/></r>`,
			fmt.Sprintf(`<r@%d xmlns@0="urn:a"> <c@%d> </c> </r>`, nsA, nsA)},
		{"prefixes", `<a:r xmlns:a="urn:a" xmlns:b="urn:b"><b:c/><a:d/></a:r>`,
			fmt.Sprintf(`<a:r@%d a@%d="urn:a" b@%d="urn:b"> <b:c@%d> </b:c> <a:d@%d> </a:d> </a:r>`, nsA, SpaceUnknown, SpaceUnknown, nsB, nsA)},
		{"the prefix is not the namespace", `<x:r xmlns:x="urn:b"><x:c/></x:r>`,
			fmt.Sprintf(`<x:r@%d x@%d="urn:b"> <x:c@%d> </x:c> </x:r>`, nsB, SpaceUnknown, nsB)},
		{"scope ends with the element", `<r><c xmlns="urn:a"><d/></c><e/></r>`,
			fmt.Sprintf(`<r@0> <c@%d xmlns@0="urn:a"> <d@%d> </d> </c> <e@0> </e> </r>`, nsA, nsA)},
		{"rebinding a prefix", `<p:r xmlns:p="urn:a"><p:c xmlns:p="urn:b"><p:d/></p:c><p:e/></p:r>`,
			fmt.Sprintf(`<p:r@%d p@%d="urn:a"> <p:c@%d p@%d="urn:b"> <p:d@%d> </p:d> </p:c> <p:e@%d> </p:e> </p:r>`, nsA, SpaceUnknown, nsB, SpaceUnknown, nsB, nsA)},
		{"undeclaring the default", `<r xmlns="urn:a"><c xmlns=""><d/></c></r>`,
			fmt.Sprintf(`<r@%d xmlns@0="urn:a"> <c@0 xmlns@0=""> <d@0> </d> </c> </r>`, nsA)},
		{"attributes take no default namespace", `<r xmlns="urn:a" xmlns:b="urn:b" x="1" b:y="2"/>`,
			fmt.Sprintf(`<r@%d xmlns@0="urn:a" b@%d="urn:b" x@0="1" y@%d="2"> </r>`, nsA, SpaceUnknown, nsB)},
		{"declared after use in the same tag", `<b:r b:y="2" xmlns:b="urn:b"/>`,
			fmt.Sprintf(`<b:r@%d y@%d="2" b@%d="urn:b"> </b:r>`, nsB, nsB, SpaceUnknown)},
		{"unlisted namespace", `<r xmlns="urn:other"/>`, fmt.Sprintf(`<r@%d xmlns@0="urn:other"> </r>`, SpaceUnknown)},
		{"undeclared prefix", `<q:r q:x="1"/>`, fmt.Sprintf(`<q:r@%d x@%d="1"> </q:r>`, SpaceUnknown, SpaceUnknown)},
		{"xml prefix", `<r xml:space="preserve"/>`, fmt.Sprintf(`<r@0 space@%d="preserve"> </r>`, SpaceXML)},
		{"namespace written with a reference", `<r xmlns="urn&#58;a"/>`, fmt.Sprintf(`<r@%d xmlns@0="urn:a"> </r>`, nsA)},
		{"attribute named like a declaration", `<r xmlnsfoo="1"/>`, `<r@0 xmlnsfoo@0="1"> </r>`},
		{"sibling with another prefix after a nested declaration", `<a:r xmlns:a="urn:a"><a:x><b:y xmlns:b="urn:b"/></a:x><a:z/></a:r>`,
			fmt.Sprintf(`<a:r@%d a@%d="urn:a"> <a:x@%d> <b:y@%d b@%d="urn:b"> </b:y> </a:x> <a:z@%d> </a:z> </a:r>`, nsA, SpaceUnknown, nsA, nsB, SpaceUnknown, nsA)},
	}
	for _, c := range cases {
		eachWay(t, []byte(c.in), opts, func(way string, s *Scanner) {
			got, err := dump(s)
			if err != nil || got != c.want {
				t.Errorf("%s (%s):\n got %s (err %v)\nwant %s", c.name, way, got, err, c.want)
			}
		})
	}
}

func TestAttrLookup(t *testing.T) {
	s := NewBytes([]byte(`<r xmlns:a="urn:a" xmlns:b="urn:b" val="plain" a:val="in a" b:val="in &amp; b" a:id="7"/>`), &Options{Spaces: testSpaces})
	if s.Next() != Start || !s.Is(NoSpace, "r") || s.Is(nsA, "r") || s.Is(NoSpace, "x") {
		t.Fatalf("first token: kind or name wrong (%s@%d)", s.Name(), s.Space())
	}
	plain, ok1 := s.Attr(NoSpace, "val")
	inA, ok2 := s.Attr(nsA, "val")
	inB, ok3 := s.Attr(nsB, "val")
	// All three must stay valid together, including the one that needed decoding.
	if !ok1 || !ok2 || !ok3 || string(plain) != "plain" || string(inA) != "in a" || string(inB) != "in & b" {
		t.Errorf("got %q %q %q", plain, inA, inB)
	}
	if _, ok := s.Attr(nsC, "val"); ok {
		t.Error("found an attribute in a namespace it is not in")
	}
	if _, ok := s.Attr(NoSpace, "id"); ok {
		t.Error("a prefixed attribute matched NoSpace")
	}
	if v, ok := s.AttrAny("id"); !ok || string(v) != "7" {
		t.Errorf("AttrAny(id) = %q, %v", v, ok)
	}
	if _, ok := s.AttrAny("missing"); ok {
		t.Error("AttrAny found a missing attribute")
	}
	if s.NumAttr() != 6 {
		t.Errorf("NumAttr = %d", s.NumAttr())
	}
}

func TestSkipAndAppendText(t *testing.T) {
	s := NewBytes([]byte(`<r><skip><a>no</a><b/></skip><t>one <i>two</i> three</t><e/><after/></r>`), nil)
	s.Next() // r
	s.Next() // skip
	if !s.Skip() || s.Depth() != 1 {
		t.Fatalf("Skip left depth %d", s.Depth())
	}
	if s.Next() != Start || string(s.Local()) != "t" {
		t.Fatalf("after Skip: %s", s.Name())
	}
	if got := string(s.AppendText([]byte("> "))); got != "> one two three" {
		t.Errorf("AppendText = %q", got)
	}
	if s.Next() != Start || string(s.Local()) != "e" {
		t.Fatalf("after AppendText: %s", s.Name())
	}
	if !s.Skip() { // an empty element
		t.Fatal("Skip on an empty element failed")
	}
	if s.Next() != Start || string(s.Local()) != "after" {
		t.Errorf("after skipping an empty element: %s", s.Name())
	}
	// Skip reports false when the input ends first.
	s = NewBytes([]byte(`<r><a>`), nil)
	s.Next()
	if s.Skip() || s.Err() == nil {
		t.Error("Skip on truncated input reported success")
	}
}

func TestMalformed(t *testing.T) {
	cases := []struct {
		name, in string
		want     error
	}{
		{"unclosed element", `<a><b></b>`, ErrSyntax},
		{"mismatched end tag", `<a></b>`, ErrSyntax},
		{"crossed tags", `<a><b></a></b>`, ErrSyntax},
		{"end tag without start", `</a>`, ErrSyntax},
		{"unterminated tag", `<a x="1"`, ErrSyntax},
		{"unterminated attribute", `<a x="1></a>`, ErrSyntax},
		{"unquoted attribute", `<a x=1/>`, ErrSyntax},
		{"attribute without value", `<a x/>`, ErrSyntax},
		{"tag without name", `< a/>`, ErrSyntax},
		{"stray slash", `<a / >`, ErrSyntax},
		{"unterminated comment", `<a><!-- x`, ErrSyntax},
		{"unterminated cdata", `<a><![CDATA[x`, ErrSyntax},
		{"unterminated end tag", `<a></a`, ErrSyntax},
		{"lone <", `<a><`, ErrSyntax},
		{"unknown markup", `<a><!FOO></a>`, ErrSyntax},
		{"doctype", `<!DOCTYPE a><a/>`, ErrDTD},
		{"doctype lower case", `<!doctype a><a/>`, ErrDTD},
		{"entity expansion attack", `<?xml version="1.0"?><!DOCTYPE lolz [<!ENTITY lol "lol"><!ENTITY lol2 "&lol;&lol;&lol;&lol;">]><lolz>&lol2;</lolz>`, ErrDTD},
		{"unsupported encoding", `<?xml version="1.0" encoding="EBCDIC"?><a/>`, ErrEncoding},
	}
	for _, c := range cases {
		eachWay(t, []byte(c.in), nil, func(way string, s *Scanner) {
			out, err := dump(s)
			if !errors.Is(err, c.want) {
				t.Errorf("%s (%s): error %v, want %v (tokens: %s)", c.name, way, err, c.want, out)
			}
			if s.Next() != EOF {
				t.Errorf("%s (%s): Next after an error returned a token", c.name, way)
			}
		})
	}
}

func TestRecover(t *testing.T) {
	opts := &Options{Recover: true}
	cases := []struct {
		name, in, want string
		repairs        int
	}{
		{"well formed input is untouched", `<a><b>x</b></a>`, `<a@0> <b@0> "x" </b> </a>`, 0},
		{"unclosed child", `<a><div>one<br>two</div><c/></a>`, `<a@0> <div@0> "one" <br@0> "two" </br> </div> <c@0> </c> </a>`, 1},
		{"several left open", `<a><b><c><d>x</b>y</a>`, `<a@0> <b@0> <c@0> <d@0> "x" </d> </c> </b> "y" </a>`, 1},
		{"end tag that closes nothing", `<a>x</nope>y</a>`, `<a@0> "xy" </a>`, 1},
		{"end tag before any start", `</a><b/>`, `<b@0> </b>`, 1},
		{"input ends with elements open", `<a><b>x`, `<a@0> <b@0> "x" </b> </a>`, 1},
		{"crossed tags", `<a><b></a></b>`, `<a@0> <b@0> </b> </a>`, 2},
	}
	for _, c := range cases {
		eachWay(t, []byte(c.in), opts, func(way string, s *Scanner) {
			got, err := dump(s)
			if err != nil || got != c.want || s.Repairs != c.repairs || s.Depth() != 0 {
				t.Errorf("%s (%s):\n got %s (err %v, repairs %d, depth %d)\nwant %s (repairs %d)", c.name, way, got, err, s.Repairs, s.Depth(), c.want, c.repairs)
			}
		})
	}
	// Markup that is cut off is still an error: there is nothing to recover to.
	for _, in := range []string{`<a><b x="1`, `<a><!-- x`, `<a><![CDATA[x`} {
		if _, err := dump(NewBytes([]byte(in), opts)); !errors.Is(err, ErrSyntax) {
			t.Errorf("%q with Recover: %v", in, err)
		}
	}
}

func TestRecoverGivesUpEventually(t *testing.T) {
	in := "<a>" + strings.Repeat("</x>", maxRepairs+5) + "</a>"
	s := NewBytes([]byte(in), &Options{Recover: true})
	if _, err := dump(s); !errors.Is(err, ErrSyntax) || s.Repairs != maxRepairs+1 {
		t.Errorf("err %v, repairs %d", err, s.Repairs)
	}
	in = "<a>" + strings.Repeat("</x>", maxRepairs) + "</a>"
	if _, err := dump(NewBytes([]byte(in), &Options{Recover: true})); err != nil {
		t.Errorf("at the bound: %v", err)
	}
}

// With many prefixes in scope the scanner switches to a table. The answers
// must be the same as with few, through shadowing and back out of it.
func TestManyNamespacePrefixes(t *testing.T) {
	var decl strings.Builder
	for i := range 40 {
		fmt.Fprintf(&decl, ` xmlns:n%d="urn:other%d"`, i, i)
	}
	in := `<r xmlns="urn:a" xmlns:b="urn:b"` + decl.String() + `>` +
		`<b:x b:k="1"/><c/><n7:y/>` +
		`<b:in xmlns:b="urn:c" xmlns:n7="urn:a"><b:x b:k="2"/><n7:y/><c xmlns=""/></b:in>` +
		`<b:x/><n7:y/><zz:q/></r>`
	want := []string{
		fmt.Sprintf("r@%d", nsA), fmt.Sprintf("b:x@%d k@%d", nsB, nsB), fmt.Sprintf("c@%d", nsA), fmt.Sprintf("n7:y@%d", SpaceUnknown),
		fmt.Sprintf("b:in@%d", nsC), fmt.Sprintf("b:x@%d k@%d", nsC, nsC), fmt.Sprintf("n7:y@%d", nsA), "c@0",
		fmt.Sprintf("b:x@%d", nsB), fmt.Sprintf("n7:y@%d", SpaceUnknown), fmt.Sprintf("zz:q@%d", SpaceUnknown),
	}
	eachWay(t, []byte(in), &Options{Spaces: testSpaces}, func(way string, s *Scanner) {
		var got []string
		for s.Next() != EOF {
			if s.Kind() != Start {
				continue
			}
			item := fmt.Sprintf("%s@%d", s.Name(), s.Space())
			for i := 0; i < s.NumAttr(); i++ {
				if space, local, _ := s.AttrAt(i); string(local) == "k" {
					item += fmt.Sprintf(" k@%d", space)
				}
			}
			got = append(got, item)
		}
		if s.Err() != nil || strings.Join(got, " | ") != strings.Join(want, " | ") {
			t.Errorf("%s: err %v\n got %v\nwant %v", way, s.Err(), got, want)
		}
	})
	// A scanner reused for a document with few prefixes forgets the old ones.
	s := NewBytes([]byte(in), &Options{Spaces: testSpaces})
	for s.Next() != EOF {
	}
	s.ResetBytes([]byte(`<b:x/>`), &Options{Spaces: testSpaces})
	if s.Next() != Start || s.Space() != SpaceUnknown {
		t.Errorf("after reset: b:x has space %d", s.Space())
	}
}

func TestSyntaxErrorCarriesOffset(t *testing.T) {
	s := NewBytes([]byte(`<a><b></c></a>`), nil)
	_, err := dump(s)
	if err == nil || !strings.Contains(err.Error(), "byte 6") || !strings.Contains(err.Error(), "</c>") {
		t.Errorf("error = %v", err)
	}
}

func TestLimits(t *testing.T) {
	deep := strings.Repeat("<a>", 100) + strings.Repeat("</a>", 100)
	eachWay(t, []byte(deep), &Options{MaxDepth: 100}, func(way string, s *Scanner) {
		if _, err := dump(s); err != nil {
			t.Errorf("depth at the limit (%s): %v", way, err)
		}
	})
	eachWay(t, []byte(deep), &Options{MaxDepth: 99}, func(way string, s *Scanner) {
		if _, err := dump(s); !errors.Is(err, ErrDepth) {
			t.Errorf("depth over the limit (%s): %v", way, err)
		}
	})
	// A default-configured scanner stops a document nested a million deep.
	bomb := strings.Repeat("<a>", 1_000_000)
	if _, err := dump(NewBytes([]byte(bomb), nil)); !errors.Is(err, ErrDepth) {
		t.Errorf("deep nesting with defaults: %v", err)
	}

	// A tag must fit in MaxTokenBytes; text need not.
	giant := `<a x="` + strings.Repeat("v", 5000) + `"/>`
	s := New(strings.NewReader(giant), &Options{BufferSize: 64, MaxTokenBytes: 1024})
	if _, err := dump(s); !errors.Is(err, ErrTokenTooLarge) {
		t.Errorf("giant attribute: %v", err)
	}
	s = New(strings.NewReader(giant), &Options{BufferSize: 64, MaxTokenBytes: 8192})
	if _, err := dump(s); err != nil {
		t.Errorf("attribute under the limit: %v", err)
	}
	long := "<a>" + strings.Repeat("text ", 4000) + "</a>"
	s = New(strings.NewReader(long), &Options{BufferSize: 64, MaxTokenBytes: 128})
	out, err := dump(s)
	if err != nil || len(out) < 20000 {
		t.Errorf("long text in a small window: err %v, %d bytes", err, len(out))
	}
}

// Text larger than the window arrives in pieces. No piece may end inside a
// character, a reference or a CR LF pair.
func TestChunkedTextIsCutCleanly(t *testing.T) {
	unit := "aé日😀&amp;&#x1F600;\r\n&lt;b"
	body := strings.Repeat(unit, 300)
	want := strings.Repeat("aé日😀&😀\n<b", 300)
	for _, size := range []int{4, 5, 6, 7, 8, 9, 13, 16, 31, 64} {
		for _, wrap := range []string{"<a>%s</a>", "<a><![CDATA[%s]]></a>"} {
			in := fmt.Sprintf(wrap, body)
			expect := want
			if strings.Contains(wrap, "CDATA") {
				expect = strings.ReplaceAll(body, "\r\n", "\n") // no decoding inside CDATA
			}
			s := New(strings.NewReader(in), &Options{BufferSize: size})
			var got []byte
			pieces := 0
			for {
				k := s.Next()
				if k == EOF {
					break
				}
				if k == Text {
					pieces++
					piece := s.Text()
					if !bytes.Equal(bytes.ToValidUTF8(piece, []byte("?")), piece) {
						t.Fatalf("window %d: a piece is not valid UTF-8: %q", size, piece)
					}
					got = append(got, piece...)
				}
			}
			if s.Err() != nil || string(got) != expect {
				t.Fatalf("window %d, %s: err %v, text differs (got %d bytes, want %d)", size, wrap, s.Err(), len(got), len(expect))
			}
			if pieces < 2 {
				t.Errorf("window %d: text arrived in %d piece; the test does not exercise chunking", size, pieces)
			}
		}
	}
}

func TestLargeCommentAndPI(t *testing.T) {
	in := "<a><!--" + strings.Repeat("- -> x ", 5000) + "--><b/></a>"
	for _, size := range []int{4, 5, 64} {
		got, err := dump(New(strings.NewReader(in), &Options{BufferSize: size, MaxTokenBytes: 256}))
		if err != nil || got != `<a@0> <b@0> </b> </a>` {
			t.Errorf("window %d: %s, %v", size, got, err)
		}
	}
}

func utf16Bytes(s string, order binary.AppendByteOrder, bom bool) []byte {
	var out []byte
	if bom {
		out = order.AppendUint16(out, 0xFEFF)
	}
	for _, u := range utf16.Encode([]rune(s)) {
		out = order.AppendUint16(out, u)
	}
	return out
}

func TestEncodings(t *testing.T) {
	const doc = `<?xml version="1.0" encoding="UTF-16"?><a x="é">日本語 😀 &amp; done</a>`
	const want = `<a@0 x@0="é"> "日本語 😀 & done" </a>`
	inputs := map[string][]byte{
		"utf-16le with mark":    utf16Bytes(doc, binary.LittleEndian, true),
		"utf-16be with mark":    utf16Bytes(doc, binary.BigEndian, true),
		"utf-16le without mark": utf16Bytes(doc, binary.LittleEndian, false),
		"utf-16be without mark": utf16Bytes(doc, binary.BigEndian, false),
		"utf-8 mislabelled":     []byte(doc),
		"latin-1":               []byte("<?xml version='1.0' encoding='ISO-8859-1'?><a x=\"\xE9\">caf\xE9 \xA9</a>"),
		"windows-1252":          []byte("<?xml version=\"1.0\" encoding=\"windows-1252\"?><a x=\"\xE9\">\x93quoted\x94 \x80 5 \x85</a>"),
	}
	wants := map[string]string{
		"latin-1":      `<a@0 x@0="é"> "café ©" </a>`,
		"windows-1252": `<a@0 x@0="é"> "“quoted” € 5 …" </a>`,
	}
	for name, in := range inputs {
		w := want
		if v, ok := wants[name]; ok {
			w = v
		}
		eachWay(t, in, nil, func(way string, s *Scanner) {
			got, err := dump(s)
			if err != nil || got != w {
				t.Errorf("%s (%s):\n got %s (err %v)\nwant %s", name, way, got, err, w)
			}
		})
	}
	// Damaged UTF-16 must not stop the scan or produce invalid UTF-8.
	bad := utf16Bytes("<a>x", binary.LittleEndian, true)
	bad = append(bad, 0x3D, 0xD8) // a high surrogate with nothing after it
	bad = append(bad, utf16Bytes("y</a>", binary.LittleEndian, false)...)
	bad = append(bad, 0x41) // half a code unit
	got, _ := dump(NewBytes(bad, nil))
	if !strings.Contains(got, "x\uFFFDy") {
		t.Errorf("damaged UTF-16: %q", got)
	}
}

func TestRepairsAreCounted(t *testing.T) {
	s := NewBytes([]byte(`<a x="R&D">&nbsp; & &amp;</a>`), nil)
	for s.Next() != EOF {
		if s.Kind() == Start {
			s.Attr(NoSpace, "x")
		}
	}
	if s.Err() != nil || s.Repairs != 3 {
		t.Errorf("err %v, repairs %d, want 3", s.Err(), s.Repairs)
	}
}

func TestResetReusesBuffers(t *testing.T) {
	s := NewBytes([]byte(`<a><b>&amp;</b></a>`), nil)
	first, _ := dump(s)
	s.ResetBytes([]byte(`<c x="&lt;"/>`), nil)
	second, err := dump(s)
	if err != nil || first != `<a@0> <b@0> "&" </b> </a>` || second != `<c@0 x@0="<"> </c>` {
		t.Errorf("%s / %s (%v)", first, second, err)
	}
	s.Reset(strings.NewReader(`<d>text</d>`), nil)
	third, err := dump(s)
	if err != nil || third != `<d@0> "text" </d>` {
		t.Errorf("%s (%v)", third, err)
	}
	// An error does not survive a reset.
	s.ResetBytes([]byte(`<a>`), nil)
	dump(s)
	s.ResetBytes([]byte(`<a/>`), nil)
	if out, err := dump(s); err != nil || out != `<a@0> </a>` {
		t.Errorf("after an error: %s (%v)", out, err)
	}
}

func TestReaderErrorIsReported(t *testing.T) {
	boom := errors.New("disk on fire")
	s := New(io.MultiReader(strings.NewReader("<a><b>"), &failingReader{boom}), nil)
	if _, err := dump(s); !errors.Is(err, boom) {
		t.Errorf("error = %v", err)
	}
	s = New(&stuckReader{}, nil)
	if _, err := dump(s); !errors.Is(err, io.ErrNoProgress) {
		t.Errorf("stuck reader: %v", err)
	}
}

type failingReader struct{ err error }

func (f *failingReader) Read([]byte) (int, error) { return 0, f.err }

type stuckReader struct{}

func (stuckReader) Read([]byte) (int, error) { return 0, nil }

func TestInputIsNotModified(t *testing.T) {
	in := []byte("<a x=\"1\r\n2&amp;\">t\r\n&lt;<![CDATA[c\r\nd]]></a>")
	orig := append([]byte(nil), in...)
	if _, err := dump(NewBytes(in, nil)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(in, orig) {
		t.Error("NewBytes modified its input")
	}
}

// Reading a part must not allocate once the scanner's buffers have grown.
func TestNoAllocationPerToken(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`<worksheet xmlns="urn:a" xmlns:r="urn:b"><sheetData>`)
	for r := 1; r <= 200; r++ {
		fmt.Fprintf(&sb, `<row r="%d" spans="1:4">`, r)
		for c := 0; c < 4; c++ {
			fmt.Fprintf(&sb, `<c r="%c%d" s="1" t="s"><v>%d</v></c><c r:id="x" t="inlineStr"><is><t xml:space="preserve">a &amp; b</t></is></c>`, 'A'+c, r, r*c)
		}
		sb.WriteString(`</row>`)
	}
	sb.WriteString(`</sheetData></worksheet>`)
	data := []byte(sb.String())
	opts := &Options{Spaces: testSpaces}
	s := NewBytes(data, opts)
	scan := func() {
		s.ResetBytes(data, opts)
		for {
			switch s.Next() {
			case Start:
				if s.Is(nsA, "c") {
					s.Attr(NoSpace, "r")
					s.Attr(nsB, "id")
				}
			case Text:
				_ = s.Text()
			case EOF:
				if s.Err() != nil {
					t.Fatal(s.Err())
				}
				return
			}
		}
	}
	scan() // grow the buffers
	if n := testing.AllocsPerRun(20, scan); n != 0 {
		t.Errorf("%v allocations per document, want 0", n)
	}
	// The same through a reader.
	r := bytes.NewReader(data)
	read := func() {
		r.Reset(data)
		s.Reset(r, opts)
		for s.Next() != EOF {
		}
	}
	read()
	if n := testing.AllocsPerRun(20, read); n != 0 {
		t.Errorf("%v allocations per document through a reader, want 0", n)
	}
}
