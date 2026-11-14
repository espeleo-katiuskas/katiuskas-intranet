// Package places is the club's catalogue of places: caves and shafts, canyons, via ferratas, huts…
// A place has a type, one main coordinate and any number of other points (where to park, where the
// approach starts, a waypoint, each access). What it is further made of depends on the kind of its
// type: a cave has a depth and a horizontal length, and a cave or a canyon has a technical sheet. Any
// member creates and changes places; the API says who and when.
package places

import (
	"errors"
	"time"
)

// Kind is the shape of a place, from its type: Cueva and Sima are caves and Cañón is a canyon, and
// every other type an admin adds is general.
type Kind string

const (
	KindCave    Kind = "cave"
	KindCanyon  Kind = "canyon"
	KindGeneral Kind = "general"
)

// State says under which conditions a place can be visited: freely (open), only with a permit that has
// to be asked for, as a tourist attraction that has to be paid for, or not at all (closed).
type State string

const (
	StateOpen           State = "open"
	StatePermitRequired State = "permit_required"
	StateTourist        State = "tourist"
	StateClosed         State = "closed"
)

// PointKind says what one of the other points of a place is.
type PointKind string

const (
	PointParking  PointKind = "parking"
	PointApproach PointKind = "approach"
	PointWaypoint PointKind = "waypoint"
	PointAccess   PointKind = "access"
	PointOther    PointKind = "other"
)

// The lowest and the highest altitude a coordinate may be given, in metres above sea level: from beyond the
// shore of the Dead Sea to beyond the highest of the Himalayas, only to catch a typo.
const (
	MinAltitudeM = -500
	MaxAltitudeM = 9000
)

// The largest measures a cave may be given, in metres: far over any real one, only to catch a typo.
const (
	MaxDepthM            = 20000
	MaxHorizontalLengthM = 2000000
)

var (
	ErrNotFound      = errors.New("place not found")
	ErrPointNotFound = errors.New("point not found")
	ErrTypeNotFound  = errors.New("type of place not found")
	// ErrUnknownType is a place given a type that does not exist.
	ErrUnknownType      = errors.New("there is no such type of place")
	ErrTypeRequired     = errors.New("the type of the place is required")
	ErrBlankName        = errors.New("the name of the place is required")
	ErrAreaRequired     = errors.New("the zone of the place is required")
	ErrUnknownArea      = errors.New("there is no such zone")
	ErrInvalidState     = errors.New("the state must be open, permit_required, tourist or closed")
	ErrInvalidCoords    = errors.New("the coordinates are not valid: latitude -90 to 90 and longitude -180 to 180")
	ErrInvalidAltitude  = errors.New("the altitude is not valid: -500 to 9000 metres above sea level")
	ErrInvalidMeasure   = errors.New("a measure of the place is not valid")
	ErrFieldsForKind    = errors.New("the depth, the horizontal length and the geology are only for caves, and the technical sheet for caves and canyons")
	ErrUnknownGeology   = errors.New("there is no such geology")
	ErrGeologyNotFound  = errors.New("geology not found")
	ErrBlankGeologyName = errors.New("the name of the geology is required")
	ErrDuplicateGeology = errors.New("there is already a geology with that name")
	ErrGeologyInUse     = errors.New("the geology has places and cannot be deleted")
	ErrInvalidPointKind = errors.New("the kind of the point must be parking, approach, waypoint, access or other")
	ErrBlankTypeName    = errors.New("the name of the type is required")
	ErrDuplicateType    = errors.New("there is already a type with that name")
	ErrTypeInUse        = errors.New("the type has places and cannot be deleted")
	// ErrFixedType means Cueva, Sima and Cañón cannot be renamed or deleted: the kinds hang from them.
	ErrFixedType = errors.New("the types of caves and canyons are fixed")
)

// Type is a type of place, with how many places have it.
type Type struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Kind       Kind   `json:"kind"`
	PlaceCount int    `json:"place_count"`
}

// Geology is what a cave can be formed in (limestone, gypsum, ice, lava…), with how many places have it.
type Geology struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	PlaceCount int    `json:"place_count"`
}

// ListItem is a place as the listing gives it: what the table and the filters need, without the long
// texts and the other points.
type ListItem struct {
	ID       int64   `json:"id"`
	TypeID   int64   `json:"type_id"`
	TypeName string  `json:"type_name"`
	Kind     Kind    `json:"kind"`
	Name     string  `json:"name"`
	AltNames *string `json:"alt_names,omitempty"`
	State    State   `json:"state"`
	// AreaID is the most specific zone it is in; AreaIDs are that zone and the ones it is in, the
	// country first, so that a list can be filtered by any of them; Where says them, from the most
	// specific one to the country: "Valdemanco, Madrid, Comunidad de Madrid, España".
	AreaID  int64   `json:"area_id"`
	AreaIDs []int64 `json:"area_ids"`
	Where   string  `json:"where"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	// AltitudeM is the altitude of the main coordinate, in metres above sea level, if it is known.
	AltitudeM         *int `json:"altitude_m,omitempty"`
	DepthM            *int `json:"depth_m,omitempty"`
	HorizontalLengthM *int `json:"horizontal_length_m,omitempty"`
	// GeologyID and Geology (its name) say what a cave is formed in, if it is known.
	GeologyID *int64  `json:"geology_id,omitempty"`
	Geology   *string `json:"geology,omitempty"`
}

// AreaRef is a zone of the path of a place.
type AreaRef struct {
	ID   int64  `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// Place is a place with everything about it.
type Place struct {
	ListItem
	Access         *string `json:"access,omitempty"`
	Description    *string `json:"description,omitempty"`
	TechnicalSheet *string `json:"technical_sheet,omitempty"`
	// AreaPath is the zone and the ones it is in, the country first, each with its kind.
	AreaPath []AreaRef `json:"area_path"`
	// CreatedBy and UpdatedBy are logins (the last person who changed the place or its points).
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedBy string    `json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
	Points    []Point   `json:"points"`
}

// Input is what creating or editing a place takes: every field the person can change (an edit
// replaces them all). The coordinates are pointers only to tell a missing one from zero.
type Input struct {
	TypeID            int64
	Name              string
	AltNames          *string
	State             string
	AreaID            int64
	Lat               *float64
	Lon               *float64
	AltitudeM         *int
	Access            *string
	Description       *string
	DepthM            *int
	HorizontalLengthM *int
	GeologyID         *int64
	TechnicalSheet    *string
}

// Point is one of the other points of a place.
type Point struct {
	ID      int64     `json:"id"`
	PlaceID int64     `json:"place_id"`
	Kind    PointKind `json:"kind"`
	Name    *string   `json:"name,omitempty"`
	Lat     float64   `json:"lat"`
	Lon     float64   `json:"lon"`
	// AltitudeM is its altitude in metres above sea level, if it is known.
	AltitudeM *int      `json:"altitude_m,omitempty"`
	Note      *string   `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// PointInput is what adding or editing a point takes.
type PointInput struct {
	Kind      string
	Name      *string
	Lat       *float64
	Lon       *float64
	AltitudeM *int
	Note      *string
}
