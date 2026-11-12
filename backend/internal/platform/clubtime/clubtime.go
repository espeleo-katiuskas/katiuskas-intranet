// Package clubtime says what day it is for the club: the calendar day of its own time zone
// (Europe/Madrid), not of the server's or UTC's. A person recording a loan at half past midnight there
// is on the new day, whatever the host or the database say.
package clubtime

import (
	"time"
	_ "time/tzdata" // the zone must be there whatever the host has installed
)

// Zone is the club's time zone.
var Zone = mustLoad("Europe/Madrid")

func mustLoad(name string) *time.Location {
	zone, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return zone
}

// Day is the calendar day, "2026-11-07", that a moment falls on in the club's time zone.
func Day(at time.Time) string {
	return at.In(Zone).Format("2006-01-02")
}

// Today is the day it is now.
func Today() string {
	return Day(time.Now())
}
