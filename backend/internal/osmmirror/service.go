package osmmirror

import (
	"context"
	"errors"
	"math"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/areas"
)

// MaxElements is the most elements one request for a map gives: a map shows the elements of a few kilometres,
// where there are hundreds, and a request that would give thousands is for a view that is too wide.
const MaxElements = 1500

var (
	// ErrInvalidBounds means a box that is not one: not numbers, out of range, or south of it above north.
	ErrInvalidBounds = errors.New("the box is not valid: south,west,north,east in degrees")
	// ErrInvalidCategory means a category that is not one of the mirror's.
	ErrInvalidCategory = errors.New("unknown category")
	// ErrInvalidElement means a type that is not node, way or relation.
	ErrInvalidElement = errors.New("the type of an element is node, way or relation")
)

// Nearby: the most elements it gives and the widest circle it looks in.
const (
	MaxNearby       = 30
	DefaultRadiusM  = 500
	MaxNearbyRadius = 2000
)

// ErrInvalidRadius means a radius of nothing or of more than MaxNearbyRadius metres.
var ErrInvalidRadius = errors.New("the radius is 1 to 2000 metres")

// Candidate is an element near a point, with how far it is and whether its name is the one asked.
type Candidate struct {
	Point
	DistanceM int `json:"distance_m"`
	// NameMatch is "exact" (the same name, ignoring case, accents and punctuation), "similar" (one name
	// holds the other, or they share a word that is not a generic one) or empty.
	NameMatch string `json:"name_match,omitempty"`
}

// Categories are all the categories an element can have, in the order of what the club does.
var Categories = []string{CategoryCave, CategoryCanyon, CategoryMine, CategoryHut, CategoryViaFerrata}

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service { return &Service{repo: repo} }

// Elements is what is inside a box, of the categories asked for (all if none), and whether there is more than
// MaxElements of it.
func (s *Service) Elements(ctx context.Context, b Bounds, categories []string) ([]Point, bool, error) {
	ok := func(v, limit float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && math.Abs(v) <= limit }
	if !ok(b.South, 90) || !ok(b.North, 90) || !ok(b.West, 180) || !ok(b.East, 180) || b.South > b.North {
		return nil, false, ErrInvalidBounds
	}
	if len(categories) == 0 {
		categories = Categories
	}
	for _, c := range categories {
		valid := false
		for _, known := range Categories {
			valid = valid || c == known
		}
		if !valid {
			return nil, false, ErrInvalidCategory
		}
	}
	return s.repo.InBounds(ctx, b, categories, MaxElements)
}

// Element is one element with all its data.
func (s *Service) Element(ctx context.Context, typ string, id int64) (Detail, error) {
	if typ != "node" && typ != "way" && typ != "relation" {
		return Detail{}, ErrInvalidElement
	}
	return s.repo.Get(ctx, typ, id)
}

// generic are the words of a name that every cave, canyon or hut has and so say nothing of which one it is.
var generic = map[string]bool{
	"cueva": true, "cuevas": true, "sima": true, "simas": true, "torca": true, "cova": true, "covas": true,
	"barranco": true, "barranc": true, "refugio": true, "refugios": true, "abrigo": true, "mina": true,
	"cabana": true, "chozo": true, "via": true, "ferrata": true, "punto": true, "entrada": true,
}

// matchName says how much two names look like one another (see Candidate.NameMatch).
func matchName(a, b string) string {
	a, b = areas.Normalize(a), areas.Normalize(b)
	if a == "" || b == "" {
		return ""
	}
	if a == b {
		return "exact"
	}
	if len(a) >= 4 && len(b) >= 4 && (strings.Contains(a, b) || strings.Contains(b, a)) {
		return "similar"
	}
	words := map[string]bool{}
	for _, w := range strings.Fields(a) {
		if len(w) >= 5 && !generic[w] {
			words[w] = true
		}
	}
	for _, w := range strings.Fields(b) {
		if words[w] {
			return "similar"
		}
	}
	return ""
}

// DistanceM is the distance between two points on the Earth in metres (haversine).
func DistanceM(lat1, lon1, lat2, lon2 float64) float64 {
	const earth = 6371008.8
	rad := math.Pi / 180
	dLat, dLon := (lat2-lat1)*rad, (lon2-lon1)*rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earth * math.Asin(math.Sqrt(a))
}

// Nearby is the elements within radius metres of a point (DefaultRadiusM if it is 0), of the categories
// asked for (all if none), that are not yet a place: those whose name looks like `name` first, then the
// nearest, at most MaxNearby. It is what a person looks at to say which element a place of the catalogue is.
func (s *Service) Nearby(ctx context.Context, lat, lon float64, radius int, name string, categories []string) ([]Candidate, error) {
	ok := func(v, limit float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && math.Abs(v) <= limit }
	if !ok(lat, 90) || !ok(lon, 180) {
		return nil, ErrInvalidBounds
	}
	if radius == 0 {
		radius = DefaultRadiusM
	}
	if radius < 1 || radius > MaxNearbyRadius {
		return nil, ErrInvalidRadius
	}
	// The box around the circle: a degree of latitude is about 111.3 km, and one of longitude is shorter
	// the further from the equator.
	dLat := float64(radius) / 111320
	dLon := dLat / math.Max(math.Cos(lat*math.Pi/180), 0.01)
	box := Bounds{South: math.Max(lat-dLat, -90), North: math.Min(lat+dLat, 90), West: lon - dLon, East: lon + dLon}
	if box.West < -180 || box.East > 180 {
		box.West, box.East = -180, 180 // Near the antimeridian: all the longitudes, and the distance filters.
	}
	points, _, err := s.Elements(ctx, box, categories)
	if err != nil {
		return nil, err
	}
	var out []Candidate
	for _, p := range points {
		d := DistanceM(lat, lon, p.Lat, p.Lon)
		if d <= float64(radius) {
			out = append(out, Candidate{Point: p, DistanceM: int(math.Round(d)), NameMatch: matchName(name, p.Name)})
		}
	}
	rank := map[string]int{"exact": 0, "similar": 1, "": 2}
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].NameMatch] != rank[out[j].NameMatch] {
			return rank[out[i].NameMatch] < rank[out[j].NameMatch]
		}
		return out[i].DistanceM < out[j].DistanceM
	})
	if len(out) > MaxNearby {
		out = out[:MaxNearby]
	}
	return out, nil
}

// MaxSearchResults is the most elements a search by name gives.
const MaxSearchResults = 20

// searchPool is how many elements the database gives a search to rank: more than are shown, so that the best
// are among them even when a very common word ("cueva") matches thousands.
const searchPool = 400

// ErrInvalidQuery means a text to search that is too long.
var ErrInvalidQuery = errors.New("the text to search is too long")

// MaxQueryLength is the longest text a search takes, in characters.
const MaxQueryLength = 200

// rankName says how well a name answers the words searched, from 0 (the same) to 3: 1 if it starts with them,
// 2 if every word is a whole word of it, 3 if they are only inside it.
func rankName(name string, query string, words []string) int {
	n := areas.Normalize(name)
	switch {
	case n == "":
		return 4
	case n == query:
		return 0
	case strings.HasPrefix(n, query):
		return 1
	}
	have := map[string]bool{}
	for _, w := range strings.Fields(n) {
		have[w] = true
	}
	for _, w := range words {
		if !have[w] {
			return 3
		}
	}
	return 2
}

// Search is the elements, not yet a place, whose name holds the words of the text (case, accents and punctuation
// do not count), of the categories asked for (all if none): the same name first, then those that start with the
// text, then those that have all its words, then the rest, and in each the club's own activity first (caves,
// canyons, mines, huts, via ferratas) and the shorter name, at most MaxSearchResults. It is how a person finds
// the cave they know the name of, to make a place of it.
func (s *Service) Search(ctx context.Context, text string, categories []string) ([]Point, error) {
	if utf8.RuneCountInString(text) > MaxQueryLength {
		return nil, ErrInvalidQuery
	}
	query := areas.Normalize(text)
	var words []string
	for _, w := range strings.Fields(query) {
		if utf8.RuneCountInString(w) >= 2 {
			words = append(words, w)
		}
	}
	if len(words) == 0 {
		return []Point{}, nil
	}
	if len(categories) == 0 {
		categories = Categories
	}
	for _, c := range categories {
		if !slices.Contains(Categories, c) {
			return nil, ErrInvalidCategory
		}
	}
	found, err := s.repo.SearchByWords(ctx, words, categories, searchPool)
	if err != nil {
		return nil, err
	}
	rank := func(p Point) int {
		best := 4
		for _, name := range p.names {
			best = min(best, rankName(name, query, words))
		}
		return best
	}
	order := func(c string) int { return slices.Index(Categories, c) }
	sort.SliceStable(found, func(i, j int) bool {
		a, b := found[i], found[j]
		if ra, rb := rank(a), rank(b); ra != rb {
			return ra < rb
		}
		if oa, ob := order(a.Category), order(b.Category); oa != ob {
			return oa < ob
		}
		if la, lb := utf8.RuneCountInString(a.Name), utf8.RuneCountInString(b.Name); la != lb {
			return la < lb
		}
		return a.ID < b.ID
	})
	if len(found) > MaxSearchResults {
		found = found[:MaxSearchResults]
	}
	if found == nil {
		found = []Point{}
	}
	return found, nil
}
