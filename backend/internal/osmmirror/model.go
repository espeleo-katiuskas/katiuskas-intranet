// Package osmmirror keeps a local copy of the elements of OpenStreetMap that matter to the club
// (docs/osm-integration.md): caves, canyons, mines, huts and via ferratas. It is fed by cmd/osmsync from the
// extracts that Geofabrik publishes, never from a request, and only that command writes the table
// `osm_elements`.
package osmmirror

import (
	"strings"
	"time"
)

// Element is one element of OpenStreetMap as the mirror keeps it.
type Element struct {
	Type      string // "node", "way" or "relation".
	ID        int64
	Version   int
	Timestamp time.Time
	Category  string
	Lat, Lon  float64
	Tags      map[string]string
	// Geometry is the GeoJSON of the outline of a way or a relation; nil for a node.
	Geometry map[string]any
	// AreaID is the zone the point is in (a municipality, in Spain), 0 if it is in none that is known (set by
	// AssignZones, from the boundaries of the extract).
	AreaID int64
}

// Key is what identifies an element: an id is only unique among those of its own type.
type Key struct {
	Type string
	ID   int64
}

func (e Element) Key() Key { return Key{e.Type, e.ID} }

// The categories of an element. The club's activity is caving and canyoning, so caves and canyons are what
// the mirror is for, and the mines that are explored like caves go right after; huts go with them (people
// sleep in them before and after), and via ferratas, which the club only uses now and then, are there
// because they cost nothing and are the least important.
const (
	CategoryCave       = "cave"
	CategoryCanyon     = "canyon"
	CategoryMine       = "mine"
	CategoryHut        = "hut"
	CategoryViaFerrata = "via_ferrata"
)

// Filter says which elements are relevant: Expression is what `osmium tags-filter` takes to cut an extract
// down (it may let more through than wanted), and Match is the exact test applied to what comes out.
// Routes marks the filters of relations that `osmium export` cannot turn into a shape (a route is a list
// of ways, not an area): those are cut apart and read by ReadRoutes.
type Filter struct {
	Category   string
	Expression string
	Match      func(tags map[string]string) bool
	Routes     bool
}

// Filters are the elements that matter. Closed ways are houses and caves are nodes: the shapes of the
// elements come from `osmium export`.
var Filters = []Filter{
	{Category: CategoryCave, Expression: "nwr/natural=cave_entrance", Match: func(t map[string]string) bool {
		return t["natural"] == "cave_entrance"
	}},
	// A canyon is a route relation (route=canyoning) whose members are the ways of the stream, or a way
	// (or a node) tagged sport=canyoning, which is how the others are mapped.
	{Category: CategoryCanyon, Expression: "nwr/sport=canyoning", Match: func(t map[string]string) bool {
		return t["sport"] == "canyoning"
	}},
	{Category: CategoryCanyon, Expression: "r/route=canyoning", Routes: true, Match: func(t map[string]string) bool {
		return t["route"] == "canyoning"
	}},
	// A mine: an adit (a horizontal gallery), a shaft or a mine that is no longer worked, but not one that
	// takes water out (resource=water: the "galerías" of the Canary Islands, tunnels dug to bring the water of
	// the mountain out, which are not what the club explores). Not the sinkholes (natural=sinkhole) either,
	// which are mostly hollows in the ground and not cavities.
	{Category: CategoryMine, Expression: "nwr/man_made=adit,mineshaft", Match: func(t map[string]string) bool {
		return (t["man_made"] == "adit" || t["man_made"] == "mineshaft") && !digsWater(t)
	}},
	{Category: CategoryMine, Expression: "nwr/historic=mine", Match: func(t map[string]string) bool {
		return t["historic"] == "mine" && !digsWater(t)
	}},
	// A hut: the two tags that mean it, or a basic hut under an amenity=shelter (which on its own is mostly
	// a bus stop or a picnic shelter), or an amenity=shelter with no type at all whose name says "refugio".
	{Category: CategoryHut, Expression: "nwr/tourism=wilderness_hut,alpine_hut", Match: func(t map[string]string) bool {
		return t["tourism"] == "wilderness_hut" || t["tourism"] == "alpine_hut"
	}},
	{Category: CategoryHut, Expression: "nwr/amenity=shelter", Match: func(t map[string]string) bool {
		if t["amenity"] != "shelter" {
			return false
		}
		switch t["shelter_type"] {
		case "basic_hut":
			return true
		case "":
			return strings.Contains(strings.ToLower(t["name"]), "refugio")
		}
		return false
	}},
	{Category: CategoryViaFerrata, Expression: "nwr/highway=via_ferrata", Match: func(t map[string]string) bool {
		return t["highway"] == "via_ferrata"
	}},
}

// digsWater says that what a mine takes out is water, among other things or only: `resource` is a list
// separated by semicolons ("coal;water").
func digsWater(t map[string]string) bool {
	for _, r := range strings.Split(t["resource"], ";") {
		if strings.EqualFold(strings.TrimSpace(r), "water") {
			return true
		}
	}
	return false
}

// Expressions are the arguments of `osmium tags-filter` for the filters whose elements `osmium export` can
// turn into shapes, and RouteExpressions for those of the routes.
func Expressions() []string { return expressions(false) }

func RouteExpressions() []string { return expressions(true) }

func expressions(routes bool) []string {
	var out []string
	for _, f := range Filters {
		if f.Routes == routes {
			out = append(out, f.Expression)
		}
	}
	return out
}

// Classify says which category an element's tags put it in, or "" if none: the elements that come out of an
// extract only because a way or a relation refers to them are not what is wanted.
func Classify(tags map[string]string) string {
	for _, f := range Filters {
		if f.Match(tags) {
			return f.Category
		}
	}
	return ""
}
