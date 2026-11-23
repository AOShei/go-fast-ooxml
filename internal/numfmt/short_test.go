package numfmt

import (
	"math"
	"math/rand/v2"
	"testing"
)

// From go-fast-msbin, which shares this engine: the quick way to the
// digits of a number gives the digits the general conversion gives.
func TestShortDigitsAreTheLongDigits(t *testing.T) {
	check := func(v float64) bool {
		var a, b decimal
		if !a.short(v) {
			return false
		}
		b.long(v)
		if a.n != b.n || a.exp != b.exp || string(a.d[:a.n]) != string(b.d[:b.n]) {
			t.Fatalf("%v (%x): the quick way gives %s e%d, the long way %s e%d", v, math.Float64bits(v), a.d[:a.n], a.exp, b.d[:b.n], b.exp)
		}
		return true
	}
	taken := 0
	count := func(v float64) {
		if check(v) {
			taken++
		}
	}
	// Whole numbers and numbers with decimals, of every size it takes,
	// and the numbers next to them in binary.
	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 2_000_000; i++ {
		digits := 1 + rng.IntN(15)
		limit := uint64(math.Pow10(digits))
		w := rng.Uint64N(limit)
		for k := 0; k <= 5; k++ {
			v := float64(w) / math.Pow10(k)
			count(v)
			count(math.Nextafter(v, math.Inf(1)))
			count(math.Nextafter(v, 0))
		}
	}
	// Sums and products, which miss the decimal they look like.
	for i := 0; i < 1_000_000; i++ {
		a, b := float64(rng.IntN(100000))/100, float64(rng.IntN(100000))/1000
		count(a + b)
		count(a * b)
		count(a - b + 1000)
		count(a / 3)
	}
	// Any number at all.
	for i := 0; i < 2_000_000; i++ {
		v := math.Abs(math.Float64frombits(rng.Uint64()))
		count(v)
	}
	for _, v := range []float64{1, 0.1, 0.5, 1e14, 999999999999999, 1e15 - 1, 0.0001, 1.005, 0.30000000000000004, 123456789012345, 12345678901234.5, 1234567890.12345, math.SmallestNonzeroFloat64, math.MaxFloat64, math.Inf(1), math.NaN(), 1e15, 1e-5} {
		count(v)
	}
	if taken < 10_000_000 {
		t.Errorf("the quick way took only %d numbers", taken)
	}
}
