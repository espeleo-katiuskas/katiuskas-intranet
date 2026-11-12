package clubtime

import (
	"testing"
	"time"
)

// 23:30 UTC on the 6th of November is already the 7th in Madrid (UTC+1); in summer (UTC+2) it is
// 22:30 UTC that is already the next day.
func TestDayIsTheClubsNotUTCs(t *testing.T) {
	for _, c := range []struct {
		at   time.Time
		want string
	}{
		{time.Date(2026, 11, 6, 23, 30, 0, 0, time.UTC), "2026-11-07"},
		{time.Date(2026, 11, 6, 22, 30, 0, 0, time.UTC), "2026-11-06"},
		{time.Date(2026, 7, 6, 22, 30, 0, 0, time.UTC), "2026-07-07"},
		{time.Date(2026, 7, 6, 21, 30, 0, 0, time.UTC), "2026-07-06"},
	} {
		if got := Day(c.at); got != c.want {
			t.Errorf("%s: got %s, want %s", c.at, got, c.want)
		}
	}
}
