package props

import (
	"testing"
	"time"
)

func TestParseTime(t *testing.T) {
	for in, want := range map[string]time.Time{
		"2026-03-04T09:30:00Z":      time.Date(2026, 3, 4, 9, 30, 0, 0, time.UTC),
		"2026-03-04T09:30:00+02:00": time.Date(2026, 3, 4, 7, 30, 0, 0, time.UTC),
		"2026-03-04T09:30:00.123Z":  time.Date(2026, 3, 4, 9, 30, 0, 123000000, time.UTC),
		"2026-03-04T09:30:00":       time.Date(2026, 3, 4, 9, 30, 0, 0, time.UTC),
		"2026-03-04T09:30:00.5":     time.Date(2026, 3, 4, 9, 30, 0, 500000000, time.UTC),
		"2026-03-04T09:30":          time.Date(2026, 3, 4, 9, 30, 0, 0, time.UTC),
		"2026-03-04":                time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC),
		"2026-03":                   time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		"2026":                      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		"":                          {},
		"yesterday":                 {},
		"04/03/2026":                {},
		"2026-13-40T00:00:00Z":      {},
	} {
		if got := parseTime(in); !got.Equal(want) {
			t.Errorf("parseTime(%q) = %v, want %v", in, got, want)
		}
	}
}
