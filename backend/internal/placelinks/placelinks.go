// Package placelinks holds what the catalogue of places (internal/places) and the club's activities
// (internal/activities) say to each other, so that neither imports the other: an activity names the
// places it goes to by id, and the catalogue only knows how many activities went to a place, not who.
package placelinks

// PlaceRef is a place as an activity says it: enough to name it and link to it.
type PlaceRef struct {
	ID int64 `json:"id"`
	// Name is its name, and TypeName what it is (the subtype of a cavity, else its type).
	Name     string `json:"name"`
	TypeName string `json:"type_name"`
	// Where is the zone and the ones it is in, from the most specific to the country, without the autonomous
	// community when there is a province: "Valdemanco, Madrid, España".
	Where string `json:"where"`
}
