package clean

import "testing"

func TestString(t *testing.T) {
	cases := []struct {
		name, in string
		flags    Flags
		want     string
		removed  int
	}{
		{"plain", "hello world", Prose, "hello world", 0},
		{"plain kept verbatim", "  hello  ", Verbatim, "  hello  ", 0},
		{"plain trimmed", " \n\thello \n", Prose, "hello", 0},
		{"tab and line feed are content", "a\tb\nc", Prose, "a\tb\nc", 0},
		{"non-ASCII", " naïve café 日本語 😀 ", Prose, "naïve café 日本語 😀", 0},
		{"no-break space is trimmed", "\u00A0x\u00A0", Prose, "x", 0},
		{"carriage return", "a\r\nb\rc", Prose, "a\nb\nc", 0},
		{"vertical tab and form feed", "a\vb\fc", Prose, "a\nb\nc", 0},
		{"unicode line separators", "a\u0085b\u2028c\u2029d", Prose, "a\nb\nc\nd", 0},
		{"control characters removed", "a\x00b\x01c\x1fd\x7fe", Prose, "abcde", 4},
		{"C1 control characters removed", "a\u0080b\u009fc", Prose, "abc", 2},
		{"invalid UTF-8", "a\xffb\xc3", Prose, "a\uFFFDb\uFFFD", 0},
		{"ligatures expanded in prose", "o\uFB03ce \uFB01le \uFB02at \uFB00 \uFB04 \uFB05 \uFB06", Prose, "office file flat ff ffl st st", 0},
		{"ligatures kept verbatim", "\uFB01le", Verbatim, "\uFB01le", 0},
		{"verbatim still loses control characters", "\tif x {\x00\n", Verbatim, "\tif x {\n", 1},
		{"empty", "", Prose, "", 0},
		{"only white space", " \n\t ", Prose, "", 0},
		{"only control characters", "\x00\x01", Prose, "", 2},
	}
	for _, c := range cases {
		got, removed := String([]byte(c.in), c.flags)
		if got != c.want || removed != c.removed {
			t.Errorf("%s: got %q (%d removed), want %q (%d removed)", c.name, got, removed, c.want, c.removed)
		}
	}
}

func TestPlainTextDoesNotAllocateTwice(t *testing.T) {
	in := []byte("an ordinary cell value")
	if n := testing.AllocsPerRun(100, func() { String(in, Prose) }); n > 1 {
		t.Errorf("%v allocations for plain text, want 1 (the string itself)", n)
	}
}
