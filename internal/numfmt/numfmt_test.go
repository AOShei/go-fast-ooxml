package numfmt

import (
	"bufio"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// normal puts a rendering into the form compared: figure spaces, which
// LibreOffice writes for padding, become spaces; runs of spaces become one;
// space at the ends goes.
func normal(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\u2007", " ")), " ")
}

// sameNumber reports whether two renderings are the same number written
// two ways, such as 1E+20 and 1E+020.
func sameNumber(a, b string) bool {
	x, err1 := strconv.ParseFloat(a, 64)
	y, err2 := strconv.ParseFloat(b, 64)
	return err1 == nil && err2 == nil && x == y
}

// TestAgainstLibreOffice holds the engine to what LibreOffice displays for
// every pair of a table of formats and values. The table is kept in
// testdata/numfmt/libreoffice.tsv; the test that checks the table itself
// against LibreOffice is in the root package.
func TestAgainstLibreOffice(t *testing.T) {
	f, err := os.Open("../../testdata/numfmt/libreoffice.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	pairs, wrong := 0, 0
	classes := map[string]int{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), "\t")
		if len(fields) != 3 {
			t.Fatalf("bad line %q", sc.Text())
		}
		code := fields[0]
		v, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			t.Fatal(err)
		}
		shown, err := strconv.Unquote(fields[2])
		if err != nil {
			t.Fatal(err)
		}
		pairs++
		out, result := Parse(code).Number(nil, v, false)
		got, want := string(out), normal(shown)
		switch {
		case got == want:
		case shown == "#FMT":
			// LibreOffice gives up on fractions and durations of very
			// large numbers. The engine does not.
			classes["LibreOffice shows an error"]++
		case result == Blank && want == "":
			// Displayed as nothing; the engine writes the value and says
			// that it is hidden.
			classes["blank, value kept"]++
			if general := string(AppendGeneral(nil, v)); got != general {
				t.Errorf("%q of %v: blank, but kept as %q, not %q", code, v, got, general)
			}
		case sameNumber(got, want) && (code == "General" || code == "@"):
			// The same number in another notation: LibreOffice writes
			// exponents with three digits and keeps sixteen-digit
			// integers plain.
			classes["notation of very large and very small numbers"]++
		default:
			wrong++
			if wrong <= 60 {
				t.Errorf("%q of %s: got %q, LibreOffice shows %q", code, fields[1], got, want)
			}
		}
	}
	t.Logf("%d pairs, %d wrong", pairs, wrong)
	for class, n := range classes {
		t.Logf("  %-50s %d", class, n)
	}
}

func number(code string, v float64) string {
	out, _ := Parse(code).Number(nil, v, false)
	return string(out)
}

// Dates and times come out in ISO 8601 whatever the format, and what is
// written depends on what the value holds.
func TestDatesAreISO(t *testing.T) {
	for _, c := range []struct {
		code string
		v    float64
		want string
	}{
		{"dd/mm/yyyy", 46085, "2026-03-04"},
		{"m/d/yy", 46085, "2026-03-04"},
		{"mmm-yy", 46085, "2026-03-04"},
		{"yyyy", 46085, "2026-03-04"},
		{"[$-F800]dddd, mmmm dd, yyyy", 46085, "2026-03-04"},
		{"[$-409]d-mmm-yy;@", 46085, "2026-03-04"},
		{"m/d/yy h:mm", 46085.604166666664, "2026-03-04T14:30:00"},
		// A format that shows the date only does not lose the time the cell holds.
		{"dd/mm/yyyy", 46085.604166666664, "2026-03-04T14:30:00"},
		// Nor a format that shows the time only, the date.
		{"h:mm", 46085.604166666664, "2026-03-04T14:30:00"},
		{"h:mm:ss", 0.6041666666666666, "14:30:00"},
		{"h:mm AM/PM", 0.75, "18:00:00"},
		{"mm:ss.0", 0.0007, "00:01:00.480"},
		{"h:mm:ss", 0, "00:00:00"},
		{"h:mm:ss", 0.999999999, "1900-01-01"}, // rounds to midnight of day 1
		{"yyyy-mm-dd", 1, "1900-01-01"},
		{"yyyy-mm-dd", 59, "1900-02-28"},
		{"yyyy-mm-dd", 60, "1900-02-29"}, // the day that never was
		{"yyyy-mm-dd", 61, "1900-03-01"},
		{"yyyy-mm-dd", 25569, "1970-01-01"},
		{"yyyy-mm-dd", 36526, "2000-01-01"},
		{"yyyy-mm-dd", 36585, "2000-02-29"},
		{"yyyy-mm-dd", 2958465, "9999-12-31"},
		{"yyyy-mm-dd", 2958465.999, "9999-12-31T23:58:33.600"},
		// Not dates the calendar has: the number itself.
		{"yyyy-mm-dd", -1, "-1"},
		{"yyyy-mm-dd", 2958466, "2958466"},
		{"yyyy-mm-dd", 1e20, "1E+20"},
		// Quoted and escaped letters are not date letters.
		{`0" days"`, 3, "3 days"},
		{`0\d`, 3, "3d"},
		{`"Total: "0.0`, 3, "Total: 3.0"},
	} {
		if got := number(c.code, c.v); got != c.want {
			t.Errorf("%q of %v = %q, want %q", c.code, c.v, got, c.want)
		}
	}
	// The 1904 system counts from 1 January 1904.
	for _, c := range []struct {
		v    float64
		want string
	}{{0, "1904-01-01"}, {1, "1904-01-02"}, {44623, "2026-03-04"}, {0.5, "12:00:00"}, {44623.5, "2026-03-04T12:00:00"}} {
		out, result := Parse("yyyy-mm-dd").Number(nil, c.v, true)
		if string(out) != c.want || result != Date {
			t.Errorf("1904 system, %v = %q (%v), want %q", c.v, out, result, c.want)
		}
	}
}

// Every day number from 1 March 1900 to the end of 9999 gives the date
// the standard library computes.
func TestEveryDay(t *testing.T) {
	origin := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	var buf []byte
	step := 1
	if testing.Short() {
		step = 37
	}
	for serial := 61; serial <= lastDay; serial += step {
		buf = appendDay(buf[:0], int64(serial), false)
		if want := origin.AddDate(0, 0, serial).Format("2006-01-02"); string(buf) != want {
			t.Fatalf("day %d = %s, want %s", serial, buf, want)
		}
	}
	origin = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)
	for serial := 0; serial <= 100000; serial++ {
		buf = appendDay(buf[:0], int64(serial), true)
		if want := origin.AddDate(0, 0, serial).Format("2006-01-02"); string(buf) != want {
			t.Fatalf("1904 system, day %d = %s, want %s", serial, buf, want)
		}
	}
}

func TestWhatIsADate(t *testing.T) {
	for code, want := range map[string]bool{
		"General": false, "0.00": false, "#,##0": false, "0%": false, "0.00E+00": false, "@": false, "# ?/?": false,
		"m/d/yyyy": true, "d-mmm-yy": true, "h:mm": true, "mm:ss": true, "mm:ss.0": true, "h:mm:ss AM/PM": true, "yyyy": true,
		"[$-409]mmmm d, yyyy": true, "[Red]dd/mm/yy": true, `yyyy"年"m"月"d"日"`: true,
		"[h]:mm:ss": false, // a duration, shown as written
		`0" d"`:     false, `0\h`: false, `"day "0`: false, "[Red]0.0": false, "[DBNum1]0": false, "[>100]0": false,
	} {
		if got := Parse(code).IsDate(); got != want {
			t.Errorf("IsDate(%q) = %v", code, got)
		}
	}
	for id, want := range map[int]bool{0: false, 1: false, 9: false, 14: true, 15: true, 20: true, 22: true, 27: true, 36: true, 45: true, 46: false, 47: true, 49: false, 57: true, 100: false} {
		if got := Parse(Builtin(id)).IsDate(); got != want {
			t.Errorf("built-in format %d: IsDate = %v", id, got)
		}
	}
}

func TestBuiltinFormats(t *testing.T) {
	for _, c := range []struct {
		id   int
		v    float64
		want string
	}{
		{0, 1234.5, "1234.5"}, {1, 1234.5, "1235"}, {2, 1234.5, "1234.50"}, {3, 1234.5, "1,235"}, {4, 1234.5, "1,234.50"},
		{5, -1234.5, "($1,235)"}, {7, 1234.5, "$1,234.50"}, {8, -1234.5, "($1,234.50)"}, {9, 0.125, "13%"}, {10, 0.125, "12.50%"},
		{11, 1234.5, "1.23E+03"}, {12, 2.5, "2 1/2"}, {13, 2.75, "2 3/4"}, {37, -1234.5, "(1,235)"}, {39, 1234.5, "1,234.50"},
		{41, 0, "-"}, {42, 1234.5, "$1,235"}, {43, -1234.5, "(1,234.50)"}, {44, 0, "$-"}, {44, 1234.5, "$1,234.50"},
		{46, 1.5, "36:00:00"}, {48, 12345, "12.3E+3"}, {49, 12, "12"}, {14, 46085, "2026-03-04"}, {22, 46085.5, "2026-03-04T12:00:00"},
		{999, 1.5, "1.5"},
	} {
		if got := number(Builtin(c.id), c.v); got != c.want {
			t.Errorf("built-in format %d of %v = %q, want %q", c.id, c.v, got, c.want)
		}
	}
}

func TestTextThroughAFormat(t *testing.T) {
	for _, c := range []struct {
		code, text, want string
		result           Result
	}{
		{"General", "hello", "hello", Shown},
		{"0.00", "hello", "hello", Shown},
		{"@", "007", "007", Shown},
		{`"Name: "@`, "Ada", "Name: Ada", Shown},
		{`0;-0;0;"<"@">"`, "x", "<x>", Shown},
		{`_(* #,##0_);_(* \(#,##0\);_(* "-"_);_(@_)`, "x", " x ", Shown},
		// Three sections say nothing about text, which is shown as it is.
		{";;", "seen", "seen", Shown},
		// A fourth, empty one hides it. The text is kept and reported.
		{";;;", "unseen", "unseen", Blank},
		{`0;0;0;"-"`, "replaced", "replaced", Blank},
		{";;;", "  ", "", Shown},
		{"@ @", "twice", "twice ", Shown}, // written once
	} {
		out, result := Parse(c.code).Text(nil, c.text)
		if string(out) != c.want || result != c.result {
			t.Errorf("%q of text %q = %q (%v), want %q (%v)", c.code, c.text, out, result, c.want, c.result)
		}
	}
}

// A number the format shows nothing for is written in full and reported,
// except zero, which formats blank as a matter of course.
func TestBlankedNumbers(t *testing.T) {
	for _, c := range []struct {
		code   string
		v      float64
		want   string
		result Result
	}{
		{";;;", 1234.5, "1234.5", Blank},
		{";;;", -7, "-7", Blank},
		{";;;", 0, "", Shown},
		{"0;;", -3, "-3", Blank},
		{"#", 0, "", Shown},
		{"#", 0.2, "0.2", Blank},
		{`""`, 5, "5", Blank},
		{`" "`, 5, "5", Blank},
		{`"-"`, 5, "-", Shown}, // replaced by text that is displayed: not blank
	} {
		out, result := Parse(c.code).Number(nil, c.v, false)
		if string(out) != c.want || result != c.result {
			t.Errorf("%q of %v = %q (%v), want %q (%v)", c.code, c.v, out, result, c.want, c.result)
		}
	}
}

// Codes that are not well formed, and odd corners, do not fail.
func TestOddCodes(t *testing.T) {
	for _, c := range []struct {
		code string
		v    float64
		want string
	}{
		{"", 1.5, "1.5"},
		{"[Red", 1.5, "1.5"}, // unclosed bracket: General
		{`"unclosed`, 1.5, "unclosed"},
		{"[>x]0", 1.5, "1.5"},   // not a condition: General
		{"0.0.0", 1.5, "1.5.0"}, // a second point is a character
		{"0/0", 5, "5/1"},       // placeholders either side of a bar: a fraction
		{".00", 12.5, "12.50"},
		{"0.", 12, "12"},
		{"%", 0.5, "%"},
		{"#,", 12345, "12"},
		{",0", 5, ",5"},
		{"0;0;0;0;0", -2, "2"},              // sections beyond the fourth are ignored
		{strings.Repeat("0", 2000), 1, "1"}, // too long: General
		{"[$-409]General", 2.5, "2.5"},
		{`General" units"`, -2.5, "-2.5 units"},
		{"General;-General;\"zero\"", 0, "zero"},
		{"0\\", 5, "5"},
		{"0_", 5, "5"},
		{"0*", 5, "5"},
		{"@@@", 5, "5"}, // the value is written once
	} {
		if got := number(c.code, c.v); got != c.want {
			t.Errorf("%q of %v = %q, want %q", c.code, c.v, got, c.want)
		}
	}
	if got := number("0.00", math.NaN()); got != "0" {
		t.Errorf("NaN = %q", got)
	}
	if got := number("0.00", math.Inf(1)); got != "0" {
		t.Errorf("infinity = %q", got)
	}
}

func FuzzNumFmt(f *testing.F) {
	for _, code := range []string{"General", "#,##0.00;[Red](#,##0.00)", "0.00E+00", "# ??/??", "[h]:mm:ss.00", "yyyy-mm-dd h:mm", `_("$"* #,##0_);_(@_)`, "[>=100]0;[<0]\"n\";0.0", "?/16", "0,,%", "[$€-407]0", "@", ";;;"} {
		for _, v := range []float64{0, -1.5, 1e300, 1e-300, 46085.5} {
			f.Add(code, v, "text")
		}
	}
	f.Fuzz(func(t *testing.T, code string, v float64, text string) {
		format := Parse(code)
		out, _ := format.Number(nil, v, false)
		// A format cannot make a small file into a large output: what it
		// writes is bounded by its own length and the digits of a number.
		if limit := 2*len(code) + 2048; len(out) > limit {
			t.Errorf("%q of %v wrote %d bytes", code, v, len(out))
		}
		out, _ = format.Number(out[:0], v, true)
		out, _ = format.Text(out[:0], text)
		if limit := 2*len(code) + len(text) + 64; len(out) > limit {
			t.Errorf("%q of text wrote %d bytes", code, len(out))
		}
		format.IsDate()
	})
}
