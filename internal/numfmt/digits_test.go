package numfmt

import (
	"strconv"
	"testing"
)

// The fifteenth digit is rounded from the shortest decimal that is read
// back as the value, half up, as LibreOffice does, not from the binary
// value; and a duration that rounds to nothing has no sign.
func TestFifteenthDigitAndZeroDurations(t *testing.T) {
	for _, c := range []struct {
		code string
		v    float64
		want string
	}{
		{"General", 664.5600532184905, "664.560053218491"}, // the binary value rounded would end in 490
		{"General", 535314.2866377655, "535314.286637766"},
		{"General", 9.999999999999995, "10"}, // the digits carry into a new one
		{"General", -9.999999999999995, "-10"},
		{"General", 0.1 + 0.2, "0.3"},
		{"General", 1234.5678, "1234.5678"},
		{"0.000", 1.0005, "1.001"},
		{"[h]:mm:ss", -1e-12, "0:00:00"},
		{"[h]:mm:ss", -0.0001, "-0:00:09"},
		{"[ss].00", -1e-10, "00.00"},
		{"[mm]:ss", -1, "-1440:00"},
	} {
		if c.code == "General" && c.v == 9.999999999999995 && strconv.FormatFloat(c.v, 'g', -1, 64) != "9.999999999999995" {
			t.Fatalf("the carry case is not the decimal it is meant to be: %v", c.v)
		}
		if got := number(c.code, c.v); got != c.want {
			t.Errorf("%q of %v: %q, want %q", c.code, c.v, got, c.want)
		}
	}
}
