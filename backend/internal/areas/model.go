// Package areas is the tree of zones of the world — states and, below each, whatever its
// administrative divisions are — that says where a place is. Nothing here knows the levels: a zone has
// a `kind`, the text that names its level ("Provincia"), and the roots are the states of the United
// Nations, which are seeded. Anybody adds a zone below another; an admin adds a country, and renames,
// moves, merges and deletes zones.
package areas

import (
	"errors"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// SpainCode is the ISO 3166-1 alpha-2 code of the country whose zones are loaded from the INE.
const SpainCode = "ES"

// The longest a name and a kind may be, in characters.
const (
	MaxNameLength = 200
	MaxKindLength = 60
)

var (
	ErrNotFound = errors.New("zone not found")
	// ErrUnknownParent is a zone to be added below one that does not exist.
	ErrUnknownParent = errors.New("there is no such zone to add it to")
	// ErrRootAdminOnly is a zone with no zone above it (a country) asked for by somebody who is not admin.
	ErrRootAdminOnly = errors.New("only an admin can add a zone that is not in another one (a country)")
	// ErrSpainAdminOnly is a zone to be added in Spain by somebody who is not admin: the zones of Spain (communities,
	// provinces, municipalities) are loaded from the INE's lists, and only an admin touches them by hand.
	ErrSpainAdminOnly = errors.New("the zones of Spain are loaded automatically: only an admin can add one")
	ErrBlankName      = errors.New("the name of the zone is required")
	ErrKindRequired   = errors.New("the kind of the zone is required: there is no other zone below this one to take it from")
	ErrTooLong        = errors.New("the name or the kind of the zone is too long")
	ErrDuplicate      = errors.New("there is already a zone with that name there")
	ErrHasChildren    = errors.New("the zone has zones below it and cannot be deleted")
	ErrInUse          = errors.New("the zone has places and cannot be deleted")
	// ErrMergeParents means two zones are merged only if they are below the same one.
	ErrMergeParents = errors.New("only two zones below the same one can be merged")
	ErrMergeSelf    = errors.New("a zone cannot be merged into itself")
	// ErrInvalidPoint is a latitude and a longitude that are not WGS84.
	ErrInvalidPoint = errors.New("the coordinates are not valid: latitude -90 to 90 and longitude -180 to 180")
	// ErrMoveCycle means a zone cannot be moved into itself or into one of the zones below it.
	ErrMoveCycle = errors.New("a zone cannot be moved into itself or into a zone below it")
)

// Ref is a zone as a link of a chain: its ancestors, or the path of a place.
type Ref struct {
	ID   int64  `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// TerritoryKind is the kind of a territory that ISO 3166-1 has a code for but that is not a state (Gibraltar,
// Greenland, Puerto Rico…): it is below the state that administers it and its code is the ISO one.
const TerritoryKind = "Territorio"

// CountryKind is the kind a zone that is in no other one has when none is given.
const CountryKind = "País"

// Move says where a zone goes when it is edited: Set is false when it stays where it is, and ID is nil
// when it becomes a country (in no other zone).
type Move struct {
	Set bool
	ID  *int64
}

// Area is a zone, with how many zones are directly below it and how many places are directly in it.
type Area struct {
	ID       int64   `json:"id"`
	ParentID *int64  `json:"parent_id,omitempty"`
	Kind     string  `json:"kind"`
	Name     string  `json:"name"`
	Code     *string `json:"code,omitempty"`
	// CreatedBy is the login of whoever added the zone through the API; absent for the zones that the seeds
	// load, and for those added before it was recorded.
	CreatedBy string `json:"created_by,omitempty"`
	// ChildCount and PlaceCount are the zones and the places directly below it, not in all its descendants.
	ChildCount int `json:"child_count"`
	PlaceCount int `json:"place_count"`
	// Ancestors are the zones it is in, the country first, without the zone itself; given by a search
	// and by GET /areas/{id}, not by the list of the zones below one (they all share them).
	Ancestors []Ref `json:"ancestors,omitempty"`
}

// extra are the letters that are not a base letter and a mark, so unicode normalization does not split
// them.
var extra = map[rune]string{'ß': "ss", 'æ': "ae", 'œ': "oe", 'ø': "o", 'ł': "l", 'đ': "d", 'ð': "d", 'þ': "th", 'ı': "i"}

// Normalize is a name in lower case, without accents, and with every run of anything but letters and
// digits as one space: "Donostia-San Sebastián" is "donostia san sebastian". What the search and the
// uniqueness of the names use. The seeded `norm_name` is the same, and a test checks it.
func Normalize(name string) string {
	var b strings.Builder
	space := true
	for _, r := range norm.NFD.String(strings.ToLower(name)) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if repl, ok := extra[r]; ok {
			b.WriteString(repl)
			space = false
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
			continue
		}
		if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSuffix(b.String(), " ")
}

// AutonomousCityCodes says which zone of kind "Ciudad autónoma" is the municipality that has each INE code:
// Ceuta (51001) and Melilla (52001) are autonomous cities, with no province and no municipality below, so
// OpenStreetMap and Nominatim name their municipality (51001, 52001) and the zone is the city, whose code
// (18, 19) is the one of the community.
var AutonomousCityCodes = map[string]string{"51001": "18", "52001": "19"}
