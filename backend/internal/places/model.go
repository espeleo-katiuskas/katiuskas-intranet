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

// Kind is the shape of a place, from its type: Cavidad is a cave and Cañón is a canyon, and every other
// type an admin adds is general.
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

// The most other names a place may have, and the longest of them, in characters.
const (
	MaxAltNames      = 20
	MaxAltNameLength = 200
)

var (
	ErrNotFound = errors.New("place not found")
	// ErrConcurrentUpdate is a partial edit that kept finding the place changed by somebody else
	// between reading it and writing it.
	ErrConcurrentUpdate = errors.New("the place was changed by somebody else meanwhile, try again")
	ErrPointNotFound    = errors.New("point not found")
	ErrTypeNotFound     = errors.New("type of place not found")
	// ErrUnknownOsmElement is a place given an OpenStreetMap element that is not in the mirror, or only
	// the type or only the id of one.
	ErrUnknownOsmElement = errors.New("there is no such OpenStreetMap element in the mirror: osm_type (node, way or relation) and osm_id go together")
	// ErrOsmElementInUse means another place already is that OpenStreetMap element.
	ErrOsmElementInUse = errors.New("another place already is that OpenStreetMap element")
	// ErrNotLinked means a place that is not an OpenStreetMap element was asked about its element.
	ErrNotLinked = errors.New("the place is not an element of OpenStreetMap")
	// ErrUnknownType is a place given a type that does not exist.
	ErrUnknownType     = errors.New("there is no such type of place")
	ErrTypeRequired    = errors.New("the type of the place is required")
	ErrBlankName       = errors.New("the name of the place is required")
	ErrAreaRequired    = errors.New("the zone of the place is required")
	ErrUnknownArea     = errors.New("there is no such zone")
	ErrInvalidState    = errors.New("the state must be open, permit_required, tourist or closed")
	ErrInvalidCoords   = errors.New("the coordinates are not valid: latitude -90 to 90 and longitude -180 to 180")
	ErrInvalidAltitude = errors.New("the altitude is not valid: -500 to 9000 metres above sea level")
	ErrInvalidMeasure  = errors.New("a measure of the place is not valid")
	ErrFieldsForKind   = errors.New("the subtype, the depth, the horizontal length and the geology are only for cavities, the technical sheet for cavities and canyons, and the state for the types that have one")
	// ErrSubtypeRequired is a cavity given no subtype (cueva, sima, torca...).
	ErrSubtypeRequired  = errors.New("the subtype of a cavity is required")
	ErrUnknownSubtype   = errors.New("there is no such subtype")
	ErrSubtypeNotFound  = errors.New("subtype not found")
	ErrBlankSubtypeName = errors.New("the name of the subtype is required")
	ErrDuplicateSubtype = errors.New("there is already a subtype with that name")
	ErrSubtypeInUse     = errors.New("the subtype has places and cannot be deleted")
	ErrSystemNotFound   = errors.New("system not found")
	ErrBlankSystemName  = errors.New("the name of the system is required")
	ErrSystemNameLength = errors.New("the name of the system is too long")
	ErrDuplicateSystem  = errors.New("there is already a system with that name")
	ErrUnknownSystem    = errors.New("there is no such system")
	// ErrNotACavity means something that is not a cavity was given a system: only cavities are in one.
	ErrNotACavity = errors.New("only a cavity can be in a system")
	// ErrUnknownPlace is one of the places given to a system that is not there.
	ErrUnknownPlace = errors.New("one of the given places does not exist")
	// ErrInvalidAltNames is a list of other names with too many, or one too long.
	ErrInvalidAltNames  = errors.New("the other names are at most 20, of at most 200 characters each")
	ErrUnknownGeology   = errors.New("there is no such geology")
	ErrGeologyNotFound  = errors.New("geology not found")
	ErrBlankGeologyName = errors.New("the name of the geology is required")
	ErrDuplicateGeology = errors.New("there is already a geology with that name")
	ErrGeologyInUse     = errors.New("the geology has places and cannot be deleted")
	ErrInvalidPointKind = errors.New("the kind of the point must be parking, approach, waypoint, access or other")
	ErrBlankTypeName    = errors.New("the name of the type is required")
	ErrDuplicateType    = errors.New("there is already a type with that name")
	ErrTypeInUse        = errors.New("the type has places and cannot be deleted")
	// ErrHasActivities means a place that activities went to cannot be deleted: their history stays.
	ErrHasActivities = errors.New("the place has activities and cannot be deleted")
	// ErrFixedType means Cueva, Sima and Cañón cannot be renamed or deleted: the kinds hang from them.
	ErrFixedType = errors.New("the types of caves and canyons are fixed")
)

// Type is a type of place, with how many places have it.
type Type struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Kind Kind   `json:"kind"`
	// HasState says whether the places of the type have a state (open, permit_required...): Cavidad, Cañón
	// and Vía ferrata do, and what an admin adds may or may not.
	HasState   bool `json:"has_state"`
	PlaceCount int  `json:"place_count"`
}

// Subtype is what a cavity is (cueva, sima, torca, mina, abrigo…), with how many places have it.
type Subtype struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
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
	ID       int64  `json:"id"`
	TypeID   int64  `json:"type_id"`
	TypeName string `json:"type_name"`
	Kind     Kind   `json:"kind"`
	Name     string `json:"name"`
	// AltNames are the other names it is known by, always a list (empty if there are none).
	AltNames []string `json:"alt_names"`
	// State is how it can be visited; absent for a place whose type has none.
	State State `json:"state,omitempty"`
	// SubtypeID and Subtype (its name) say what a cavity is: cueva, sima…
	SubtypeID *int64  `json:"subtype_id,omitempty"`
	Subtype   *string `json:"subtype,omitempty"`
	// SystemID and System (its name) say which system of cavities it is in, if it is in one.
	SystemID *int64  `json:"system_id,omitempty"`
	System   *string `json:"system,omitempty"`
	// AreaID is the most specific zone it is in; AreaIDs are that zone and the ones it is in, the
	// country first, so that a list can be filtered by any of them; Where says them, from the most
	// specific one to the country, without the autonomous community when there is a province (in Spain the
	// province says it already): "Valdemanco, Madrid, España". AreaNames are the names of all of them, the
	// community too, for a search to look in.
	AreaID    int64   `json:"area_id"`
	AreaIDs   []int64 `json:"area_ids"`
	Where     string  `json:"where"`
	AreaNames string  `json:"area_names"`
	Lat       float64 `json:"lat"`
	Lon       float64 `json:"lon"`
	// AltitudeM is the altitude of the main coordinate, in metres above sea level, if it is known.
	AltitudeM         *int `json:"altitude_m,omitempty"`
	DepthM            *int `json:"depth_m,omitempty"`
	HorizontalLengthM *int `json:"horizontal_length_m,omitempty"`
	// GeologyID and Geology (its name) say what a cave is formed in, if it is known.
	GeologyID *int64  `json:"geology_id,omitempty"`
	Geology   *string `json:"geology,omitempty"`
	// ActivityCount is in how many activities that are not cancelled the place is one of the places they go to
	// (activity_places).
	ActivityCount int `json:"activity_count"`
	// OsmChanged says that the OpenStreetMap element the place is has changed since it was linked or last
	// looked at (Place.OsmChanges says what); absent when it has not, or the place is not one.
	OsmChanged bool `json:"osm_changed,omitempty"`
}

// VisitedPlace is a place a person has been to, with how often.
type VisitedPlace struct {
	ID int64 `json:"id"`
	// TypeName is what it is: the subtype of a cavity ("Sima"), else its type.
	TypeName string `json:"type_name"`
	Name     string `json:"name"`
	Where    string `json:"where"`
	// Visits is in how many activities, started and not cancelled, the person took part and that went to the
	// place, and LastVisit the end date of the latest of them.
	Visits    int       `json:"visits"`
	LastVisit time.Time `json:"last_visit"`
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
	// OsmType and OsmID are the OpenStreetMap element the place is, when it is one (the mirror has it).
	OsmType *string `json:"osm_type,omitempty"`
	OsmID   *int64  `json:"osm_id,omitempty"`
	// OsmChanges is what is different in the element now from what it was when the place was linked or last
	// looked at, which a person reviews (POST /places/{id}/links/osm/review says it has been).
	OsmChanges []OsmChange `json:"osm_changes,omitempty"`
	// AreaPath is the zone and the ones it is in, the country first, each with its kind.
	AreaPath []AreaRef `json:"area_path"`
	// CreatedBy and UpdatedBy are logins (the last person who changed the place or its points). The
	// repository only knows the ids of those people (CreatedByID, UpdatedByID: the places never read
	// the table of users); the Service turns them into logins.
	CreatedBy   string    `json:"created_by"`
	CreatedByID int64     `json:"-"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedBy   string    `json:"updated_by"`
	UpdatedByID int64     `json:"-"`
	UpdatedAt   time.Time `json:"updated_at"`
	Points      []Point   `json:"points"`
	// FileCount is how many files are attached to it (GET /places/{id}/files lists them).
	FileCount int `json:"file_count"`
}

// Input is what creating or editing a place takes: every field the person can change (an edit
// replaces them all). The coordinates are pointers only to tell a missing one from zero.
type Input struct {
	TypeID            int64
	Name              string
	AltNames          []string
	State             string
	SubtypeID         *int64
	SystemID          *int64
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
	// OsmType and OsmID say which OpenStreetMap element the place is. They are given when a place is
	// created, from an element of the mirror, and an edit leaves them as they were.
	OsmType *string
	OsmID   *int64
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

// MaxFileSize is the largest file that can be attached to a place, in bytes.
const MaxFileSize = 10 << 20 // 10 MiB

// MaxFileTitleLength is the longest caption a file may have, in characters.
const MaxFileTitleLength = 200

var (
	ErrFileNotFound = errors.New("file not found")
	ErrEmptyFile    = errors.New("the file is empty")
	// ErrTitleTooLong is a caption of a file longer than MaxFileTitleLength.
	ErrTitleTooLong = errors.New("the title of the file is too long")
	// ErrNotYours means somebody tried to remove a file that somebody else attached, being neither
	// admin nor board.
	ErrNotYours = errors.New("only who attached it, admin or board can remove it")
)

// File is the metadata of a file attached to a place (never its bytes, except through Open).
type File struct {
	ID int64 `json:"id"`
	// PlaceID or SystemID: whose file it is (exactly one is there).
	PlaceID     *int64    `json:"place_id,omitempty"`
	SystemID    *int64    `json:"system_id,omitempty"`
	Title       *string   `json:"title,omitempty"`
	FileName    string    `json:"file_name"`
	ContentType string    `json:"content_type"`
	FileSize    int64     `json:"file_size"`
	UploadedBy  string    `json:"uploaded_by"` // a login, from UploadedByID (see Place.CreatedBy)
	CreatedAt   time.Time `json:"created_at"`
	// UploadedByID is who attached it, for the Service to turn into UploadedBy.
	UploadedByID int64 `json:"-"`
}

// MaxSystemNameLength is the longest name a system may have, in characters.
const MaxSystemNameLength = 200

// SystemPlaceRef is a cavity of a system, as the list of systems says it.
type SystemPlaceRef struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Subtype string `json:"subtype,omitempty"`
}

// SystemListItem is a system as the listing gives it: its name and its cavities.
type SystemListItem struct {
	ID     int64            `json:"id"`
	Name   string           `json:"name"`
	Places []SystemPlaceRef `json:"places"`
}

// SystemPlace is a cavity of a system with everything the page of the system shows of it: what the
// listing of places has, the long texts and its files.
type SystemPlace struct {
	ListItem
	Access         *string `json:"access,omitempty"`
	Description    *string `json:"description,omitempty"`
	TechnicalSheet *string `json:"technical_sheet,omitempty"`
	// Files are the photos and documents of the cavity itself (not those of the system), oldest first.
	Files []File `json:"files"`
	// Points are its other points (parking, approach…), for the map of the system.
	Points []Point `json:"points"`
}

// System is a group of cavities connected underground, with a description and files of its own.
type System struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	// CreatedBy and UpdatedBy are logins (the last person who changed the system, its cavities or its files).
	CreatedBy   string    `json:"created_by"`
	CreatedByID int64     `json:"-"` // see Place.CreatedBy
	CreatedAt   time.Time `json:"created_at"`
	UpdatedBy   string    `json:"updated_by"`
	UpdatedByID int64     `json:"-"`
	UpdatedAt   time.Time `json:"updated_at"`
	// FileCount is how many files are attached to it (GET /systems/{id}/files lists them).
	FileCount int `json:"file_count"`
	// Places are its cavities, by name, with everything about each.
	Places []SystemPlace `json:"places"`
}
