package gofastooxml_test

import (
	"testing"
	"time"
)

// costFloor: a hostile input read in less than this, at its full size,
// passes however its time compares: times this short are mostly noise.
const costFloor = 20 * time.Millisecond

// costsAtMost fails the test if worst takes more than k times as long as
// ref. Each is timed best of three, the two in turn, so that a slow or
// busy machine slows both alike, and each time is of as many runs as take
// 25 ms, so that a coarse clock (Windows') does not decide it: what is bounded is the work, not the
// seconds. ref is an ordinary input of the same size, or the same input at
// a quarter of the size, in which case a k of 8 says the cost is linear
// (4 times the work for 4 times the input) and not quadratic (16 times).
// A hang is still caught, by a limit of minutes.
func costsAtMost(t *testing.T, k float64, what string, ref, worst func()) {
	t.Helper()
	var bestRef, bestWorst time.Duration
	within(t, 5*time.Minute, what, func() {
		for i := range 3 {
			if d := timed(ref); i == 0 || d < bestRef {
				bestRef = d
			}
			if d := timed(worst); i == 0 || d < bestWorst {
				bestWorst = d
			}
		}
	})
	t.Logf("%s: %v, %.1f times the reference (%v)", what, bestWorst.Round(time.Millisecond), float64(bestWorst)/float64(bestRef), bestRef.Round(time.Millisecond))
	if bestWorst > costFloor && float64(bestWorst) > k*float64(bestRef) {
		t.Errorf("%s: %v, %.1f times as long as the reference (%v); at most %g times", what, bestWorst.Round(time.Millisecond), float64(bestWorst)/float64(bestRef), bestRef.Round(time.Millisecond), k)
	}
}

// timed returns the time f takes, as the mean of as many runs as take
// 25 ms.
func timed(f func()) time.Duration {
	f() // untimed, so that each side's memory is as warm as the other's
	start, n := time.Now(), 0
	for n == 0 || time.Since(start) < 25*time.Millisecond {
		f()
		n++
	}
	return time.Since(start) / time.Duration(n)
}
