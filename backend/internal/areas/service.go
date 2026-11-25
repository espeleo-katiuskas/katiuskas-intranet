package areas

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/access"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/accounts"
)

type Service struct {
	repo       *Repository
	boundaries Boundaries
	geocoder   Geocoder
	accounts   *accounts.Service
}

func NewService(repo *Repository, accounts *accounts.Service) *Service {
	return &Service{repo: repo, accounts: accounts}
}

// AccountOf is the account of the viewer, made if it is the first time they act: what a zone keeps of who
// added it.
func (s *Service) AccountOf(ctx context.Context, viewer access.Viewer) (accounts.Account, error) {
	return s.accounts.Ensure(ctx, viewer)
}

// HasAuthored says whether the person that the issuer knows by that subject added a zone
// (users.AuthorGuard). One who never acted has no account, and added nothing.
func (s *Service) HasAuthored(ctx context.Context, issuer, subject string) (bool, error) {
	account, found, err := s.accounts.Of(ctx, issuer, subject)
	if err != nil || !found {
		return false, err
	}
	return s.repo.HasAuthored(ctx, account.ID)
}

// WithBoundaries gives the service a source to find the zone of a point; without one, Locate finds none.
func (s *Service) WithBoundaries(b Boundaries) *Service {
	s.boundaries = b
	return s
}

// WithGeocoder gives the service a source to search places by name; without one, Geocode finds none.
func (s *Service) WithGeocoder(g Geocoder) *Service {
	s.geocoder = g
	return s
}

// MaxSearchLength is the longest text that is searched on a map.
const MaxSearchLength = 200

// Geocode is the places a text names (a town, a valley, a peak), asking an outside source
// (ErrSearchUnavailable when that fails). A blank text, or no source, finds nothing.
func (s *Service) Geocode(ctx context.Context, text string) ([]Place, error) {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) > MaxSearchLength {
		return nil, ErrTooLong
	}
	if text == "" || s.geocoder == nil {
		return []Place{}, nil
	}
	return s.geocoder.Search(ctx, text)
}

// Locate is the zone that a point is in, with its ancestors, or nil if there is none to say: the
// municipality when it is a Spanish one the seed knows (by INE code; Ceuta and Melilla, which have none, give
// the autonomous city), else the country. It asks an
// outside source (ErrLocateUnavailable when that fails), so it is a help to propose a zone, never a
// fact.
func (s *Service) Locate(ctx context.Context, lat, lon float64) (*Area, error) {
	if !validPoint(lat, lon) {
		return nil, ErrInvalidPoint
	}
	if s.boundaries == nil {
		return nil, nil
	}
	found, err := s.boundaries.Containing(ctx, lat, lon)
	if err != nil {
		return nil, err
	}
	candidates := []struct{ kind, code string }{
		{"Municipio", found.MunicipalityCode},
		// The geocoder says Gibraltar, Greenland or Bermuda by their ISO code, like a country, but they are
		// territories below the state that administers them.
		{TerritoryKind, found.CountryCode},
		{"", found.CountryCode},
	}
	if city, ok := AutonomousCityCodes[found.MunicipalityCode]; ok {
		// Ceuta and Melilla have no municipality below them: the zone is the city itself.
		candidates = append([]struct{ kind, code string }{{"Ciudad autónoma", city}}, candidates...)
	}
	for _, candidate := range candidates {
		if candidate.code == "" {
			continue
		}
		id, err := s.repo.IDByCode(ctx, candidate.kind, candidate.code)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		a, err := s.repo.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		return &a, nil
	}
	return nil, nil
}

// Children are the zones directly below a zone, or the countries if parentID is nil.
func (s *Service) Children(ctx context.Context, parentID *int64) ([]Area, error) {
	list, err := s.repo.Children(ctx, parentID)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 && parentID != nil {
		// Nothing below it: either it is a leaf or it does not exist.
		if _, err := s.repo.Get(ctx, *parentID); err != nil {
			return nil, err
		}
	}
	return list, nil
}

// SearchLimit is how many zones a search gives at most, and how many by default.
const (
	DefaultSearchLimit = 20
	MaxSearchLimit     = 50
)

// Search is the zones whose name contains the text, ignoring case, accents and punctuation.
func (s *Service) Search(ctx context.Context, text string, limit int) ([]Area, error) {
	normalized := Normalize(text)
	if normalized == "" {
		return []Area{}, nil
	}
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	list, err := s.repo.Search(ctx, normalized, min(limit, MaxSearchLimit))
	return list, err
}

func (s *Service) Get(ctx context.Context, id int64) (Area, error) {
	a, err := s.repo.Get(ctx, id)
	return a, err
}

// cleanText checks a name and a kind (which may be blank, to be filled in) and normalizes the name.
func cleanText(name, kind string) (string, string, string, error) {
	name, kind = strings.TrimSpace(name), strings.TrimSpace(kind)
	normalized := Normalize(name)
	if name == "" || normalized == "" {
		return "", "", "", ErrBlankName
	}
	if utf8.RuneCountInString(name) > MaxNameLength || utf8.RuneCountInString(kind) > MaxKindLength {
		return "", "", "", ErrTooLong
	}
	return name, kind, normalized, nil
}

// Create adds a zone below another, or a country when parentID is nil. Its kind is the one the zones
// below that one already have, unless it is given; with none below it yet, it has to be given; a country
// is a "País" unless it is said otherwise.
//
// Only an admin adds a zone in Spain (below it at any depth): they are loaded from the INE's lists.
// createdBy is the account of whoever adds it, which the zone keeps.
func (s *Service) Create(ctx context.Context, parentID *int64, name, kind string, admin bool, createdBy int64) (Area, error) {
	name, kind, normalized, err := cleanText(name, kind)
	if err != nil {
		return Area{}, err
	}
	if parentID != nil {
		parent, err := s.repo.Get(ctx, *parentID)
		if errors.Is(err, ErrNotFound) {
			return Area{}, ErrUnknownParent
		} else if err != nil {
			return Area{}, err
		}
		if !admin {
			root := parent.ID
			if len(parent.Ancestors) > 0 {
				root = parent.Ancestors[0].ID
			}
			spain, err := s.repo.IDByCode(ctx, "", SpainCode)
			if err != nil && !errors.Is(err, ErrNotFound) {
				return Area{}, err
			}
			if err == nil && root == spain {
				return Area{}, ErrSpainAdminOnly
			}
		}
	}
	if kind == "" {
		if parentID == nil {
			kind = CountryKind
		} else {
			if kind, err = s.repo.KindBelow(ctx, *parentID); err != nil {
				return Area{}, err
			}
			if kind == "" {
				return Area{}, ErrKindRequired
			}
		}
	}
	taken, err := s.repo.NameTaken(ctx, parentID, normalized, 0)
	if err != nil {
		return Area{}, err
	}
	if taken {
		return Area{}, ErrDuplicate
	}
	a, err := s.repo.Create(ctx, parentID, kind, name, normalized, createdBy)
	return a, err
}

// Update renames a zone and, if a kind is given, changes it; with move.Set it also puts it below another
// zone (or makes it a country). Nothing changes if any part is refused.
func (s *Service) Update(ctx context.Context, id int64, name, kind string, move Move) (Area, error) {
	current, err := s.repo.Get(ctx, id)
	if err != nil {
		return Area{}, err
	}
	name, kind, normalized, err := cleanText(name, kind)
	if err != nil {
		return Area{}, err
	}
	if kind == "" {
		kind = current.Kind
	}
	target := current.ParentID
	if move.Set {
		target = move.ID
		if target != nil {
			if *target == id {
				return Area{}, ErrMoveCycle
			}
			if _, err := s.repo.Get(ctx, *target); errors.Is(err, ErrNotFound) {
				return Area{}, ErrUnknownParent
			} else if err != nil {
				return Area{}, err
			}
		}
	}
	taken, err := s.repo.NameTaken(ctx, target, normalized, id)
	if err != nil {
		return Area{}, err
	}
	if taken {
		return Area{}, ErrDuplicate
	}
	a, err := s.repo.Update(ctx, id, kind, name, normalized, move)
	return a, err
}

// Delete removes a zone that has no zones below it and no places (a country too, if it has none).
func (s *Service) Delete(ctx context.Context, id int64) error {
	a, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	switch {
	case a.ChildCount > 0:
		return ErrHasChildren
	case a.PlaceCount > 0:
		return ErrInUse
	}
	return s.repo.Delete(ctx, id)
}

// Merge moves everything in a zone to another below the same one, and deletes the first. Two countries
// are below the same one: the root of the tree.
func (s *Service) Merge(ctx context.Context, from, into int64) error {
	if from == into {
		return ErrMergeSelf
	}
	return s.repo.Merge(ctx, from, into)
}
