package publicplaces

import (
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/access"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/places"
)

// Coordinates is the main coordinate of a place, which is only there for a viewer who may see the
// location: otherwise there is no marker, and the zone is all there is.
type Coordinates struct {
	Lat       float64 `json:"lat"`
	Lon       float64 `json:"lon"`
	AltitudeM *int    `json:"altitude_m,omitempty"`
}

// Measures are the depth and the horizontal length of a cave, in metres, and what it is formed in.
type Measures struct {
	DepthM            *int    `json:"depth_m,omitempty"`
	HorizontalLengthM *int    `json:"horizontal_length_m,omitempty"`
	Geology           *string `json:"geology,omitempty"`
}

// ListItem is a place as the list of the public gives it: what the table and the filters need.
type ListItem struct {
	ID       int64    `json:"id"`
	Name     string   `json:"name"`
	AltNames []string `json:"alt_names"`
	// Type is what it is (Cavidad, Cañón…), Subtype what a cavity is (cueva, sima…), absent for the rest.
	Type    string  `json:"type"`
	Subtype *string `json:"subtype,omitempty"`
	// State is how it can be visited, absent for a place whose type has none.
	State places.State `json:"state,omitempty"`
	// Where is the zone and the ones it is in, from the most specific to the country, and ZoneIDs those
	// zones, the country first, so that a list can be filtered by any of them.
	Where   string  `json:"where"`
	ZoneIDs []int64 `json:"zone_ids"`
	// Measures is only there if the place has any.
	Measures *Measures `json:"measures,omitempty"`
	// Coordinates is only there for a viewer who may see the location.
	Coordinates *Coordinates `json:"coordinates,omitempty"`
}

// Point is one of the other points of a place (a car park, where the approach starts…).
type Point struct {
	Kind      string  `json:"kind"`
	Name      *string `json:"name,omitempty"`
	Lat       float64 `json:"lat"`
	Lon       float64 `json:"lon"`
	AltitudeM *int    `json:"altitude_m,omitempty"`
	Note      *string `json:"note,omitempty"`
}

// Zone is a zone of the path of a place.
type Zone struct {
	ID   int64  `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// Location is what a viewer who may see where a place is gets of it: the other points and how to get there.
type Location struct {
	Points []Point `json:"points"`
	Access *string `json:"access,omitempty"`
}

// Content is what a viewer who may see what is said of a place gets of it.
type Content struct {
	Description    *string `json:"description,omitempty"`
	TechnicalSheet *string `json:"technical_sheet,omitempty"`
}

// Place is a place as the public page of it gives it. Its parts that depend on the visibility
// (Coordinates, Location, Content) are absent, not empty, when the viewer may not see them.
type Place struct {
	ListItem
	// Zones is the zone of the place and the ones it is in, the country first, each with its kind.
	Zones    []Zone    `json:"zones"`
	Location *Location `json:"location,omitempty"`
	Content  *Content  `json:"content,omitempty"`
}

func listItemOf(p places.ListItem) ListItem {
	item := ListItem{
		ID: p.ID, Name: p.Name, AltNames: p.AltNames, Type: p.TypeName, Subtype: p.Subtype, State: p.State,
		Where: p.Where, ZoneIDs: p.AreaIDs,
	}
	if item.AltNames == nil {
		item.AltNames = []string{}
	}
	if item.ZoneIDs == nil {
		item.ZoneIDs = []int64{}
	}
	if p.DepthM != nil || p.HorizontalLengthM != nil || p.Geology != nil {
		item.Measures = &Measures{DepthM: p.DepthM, HorizontalLengthM: p.HorizontalLengthM, Geology: p.Geology}
	}
	if !withheld(p.Withheld, access.PartCoordinates) {
		item.Coordinates = &Coordinates{Lat: p.Lat, Lon: p.Lon, AltitudeM: p.AltitudeM}
	}
	return item
}

// placeOf copies, field by field, what the public may know of a place that has been redacted for the
// viewer. What is not copied here is never said: nothing of who wrote it, the activities, the element of
// OpenStreetMap, the visibility or the files.
func placeOf(p places.Place) Place {
	out := Place{ListItem: listItemOf(p.ListItem), Zones: make([]Zone, len(p.AreaPath))}
	for i, a := range p.AreaPath {
		out.Zones[i] = Zone{ID: a.ID, Kind: a.Kind, Name: a.Name}
	}
	if !withheld(p.Withheld, access.PartPoints) || !withheld(p.Withheld, access.PartAccess) {
		loc := &Location{Points: []Point{}}
		if !withheld(p.Withheld, access.PartPoints) {
			for _, pt := range p.Points {
				loc.Points = append(loc.Points, Point{
					Kind: string(pt.Kind), Name: pt.Name, Lat: pt.Lat, Lon: pt.Lon, AltitudeM: pt.AltitudeM, Note: pt.Note,
				})
			}
		}
		if !withheld(p.Withheld, access.PartAccess) {
			loc.Access = p.Access
		}
		out.Location = loc
	}
	if !withheld(p.Withheld, access.PartSheet) {
		out.Content = &Content{Description: p.Description, TechnicalSheet: p.TechnicalSheet}
	}
	return out
}
