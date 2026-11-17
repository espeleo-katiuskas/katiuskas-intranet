package osmmirror

import (
	"context"
	"errors"
	"math"
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

// Categories are all the categories an element can have, in the order of what the club does.
var Categories = []string{CategoryCave, CategoryCanyon, CategoryHut, CategoryViaFerrata}

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
