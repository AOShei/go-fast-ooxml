package codepage

import "testing"

func TestDecode(t *testing.T) {
	cases := []struct {
		cp        int
		in, want  string
		undecoded int
	}{
		{1252, "Caf\xe9 \x80 \x81", "Café € \uFFFD", 1},
		{1251, "\xcf\xf0\xe8\xe2\xe5\xf2", "Привет", 0},
		{1250, "\x8a\xe8", "Šč", 0},
		{10000, "\x8e\xa5", "é•", 0},
		{936, "\xc4\xe3\xba\xc3, \x80", "你好, €", 0},
		{950, "\xa7\x41\xa6\x6e", "你好", 0},
		{932, "\x82\xb1\x82\xf1 \xb1", "こん ｱ", 0},
		{949, "\xbe\xc8\xb3\xe7", "안녕", 0},
		{936, "ab\xc4", "ab\uFFFD", 1}, // a lead byte with nothing after it
		{28591, "\xe9", "é", 0},
		{65001, "é\xff", "é\uFFFD", 1},
		{1361, "a\xe9", "a\uFFFD", 1}, // not known
	}
	for _, c := range cases {
		got, n := Decode(c.cp, []byte(c.in))
		if got != c.want || n != c.undecoded {
			t.Errorf("%d %q: %q, %d; want %q, %d", c.cp, c.in, got, n, c.want, c.undecoded)
		}
	}
	if !Known(1258) || !Known(65001) || Known(1361) {
		t.Error("Known")
	}
}

func FuzzDecode(f *testing.F) {
	f.Add(936, []byte("\xc4\xe3\xba"))
	f.Fuzz(func(t *testing.T, cp int, b []byte) {
		s, n := Decode(cp, b)
		if n > len(b) || len(s) > 3*len(b) {
			t.Fatalf("%d bytes gave %d bytes, %d undecoded", len(b), len(s), n)
		}
	})
}
