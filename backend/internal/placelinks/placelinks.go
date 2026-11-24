// Package placelinks holds what the catalogue of places (internal/places) and the club's activities
// (internal/activities) say to each other, so that neither imports the other: an activity names the
// places it goes to by id, and the catalogue only knows how many activities went to a place, not who.
package placelinks

import "time"

// PlaceRef is a place as an activity says it: enough to name it and link to it.
type PlaceRef struct {
	ID int64 `json:"id"`
	// Name is its name, and TypeName what it is (the subtype of a cavity, else its type).
	Name     string `json:"name"`
	TypeName string `json:"type_name"`
}

// PlaceVisit is how often a person went to a place: in how many activities, started and not cancelled,
// they took part that went there, and the end date of the latest of them.
type PlaceVisit struct {
	PlaceID   int64
	Visits    int
	LastVisit time.Time
}
