package clean

import (
	"strings"
	"testing"
	"unicode/utf8"
)

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

func TestLabel(t *testing.T) {
	for in, want := range map[string]string{
		"word/media/image1.png":        "word/media/image1.png",
		"  padded  ":                   "padded",
		"we\x01ird\tname\nwith breaks": "weird name with breaks",
		"bad \xff byte":                "bad \uFFFD byte",
		"caf\u00e9":                    "caf\u00e9",
		"":                             "",
	} {
		if got := Label(in); got != want {
			t.Errorf("Label(%q) = %q, want %q", in, got, want)
		}
	}
	long := Label(strings.Repeat("\u00e9", 5000))
	if len(long) > maxLabel+3 || !utf8.ValidString(long) || len(long) < maxLabel/2 {
		t.Errorf("a long label came out %d bytes, valid=%v", len(long), utf8.ValidString(long))
	}
}

// The run of printable ASCII is found eight bytes at a time. Every byte
// value at every place in the eight must stop it where a byte-by-byte
// reading does, and the cleaned text must be what it was without the
// fast path.
func TestPrintableRun(t *testing.T) {
	slow := func(b []byte) int {
		n := 0
		for n < len(b) && b[n] >= 0x20 && b[n] < 0x7F {
			n++
		}
		return n
	}
	base := []byte("The quick brown fox jumps over ~ ")
	for at := 0; at < len(base); at++ {
		for c := 0; c < 256; c++ {
			b := append([]byte(nil), base...)
			b[at] = byte(c)
			for start := 0; start < 9; start++ {
				if got, want := printable(b[start:]), slow(b[start:]); got != want {
					t.Fatalf("byte %#x at %d, from %d: %d, want %d", c, at, start, got, want)
				}
			}
		}
	}
	// Two bytes that stop the run, in every pair of places: a borrow from
	// the first must not hide or invent the second.
	for _, pair := range [][2]byte{{0x00, 0x7F}, {0x7F, 0x00}, {0x1F, 0x80}, {0xFF, 0x20}, {0x7F, 0x7E}, {0x1F, 0x20}, {0x00, 0x21}} {
		for i := 0; i < 16; i++ {
			for j := 0; j < 16; j++ {
				b := []byte("abcdefghijklmnopqrstuvwx")
				b[j] = pair[1]
				b[i] = pair[0]
				if got, want := printable(b), slow(b); got != want {
					t.Fatalf("%#x at %d and %#x at %d: %d, want %d", pair[0], i, pair[1], j, got, want)
				}
			}
		}
	}
}

func BenchmarkAppend(b *testing.B) {
	for name, text := range map[string]string{
		"plain":    strings.Repeat("The quarterly figures for the northern region, as reported. ", 20),
		"accented": strings.Repeat("Les chiffres trimestriels de la région du nord, tels que déclarés. ", 20),
		"cjk":      strings.Repeat("北部地域の四半期の数字", 60),
	} {
		src := []byte(text)
		dst := make([]byte, 0, 2*len(src))
		b.Run(name, func(b *testing.B) {
			b.SetBytes(int64(len(src)))
			for b.Loop() {
				dst, _ = Append(dst[:0], src, Ligatures)
			}
		})
	}
}
