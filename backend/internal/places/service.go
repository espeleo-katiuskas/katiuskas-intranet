package places

import (
	"context"
	"errors"
	"math"
	"strings"
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

// blankToNil trims a text and gives nil for a blank one, so that "" is never stored as if it were a value.
func blankToNil(p *string) *string {
	if p == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*p)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// validCoordinates says whether a latitude and a longitude are given and fit WGS84.
func validCoordinates(lat, lon *float64) bool {
	if lat == nil || lon == nil {
		return false
	}
	ok := func(v, limit float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= -limit && v <= limit }
	return ok(*lat, 90) && ok(*lon, 180)
}

// validAltitude says whether an altitude, which is optional, is one that can be.
func validAltitude(altitude *int) bool {
	return altitude == nil || (*altitude >= MinAltitudeM && *altitude <= MaxAltitudeM)
}

// clean checks and trims what a place is given, without the type (see kindOf). A blank state is
// "open".
func clean(in Input) (Input, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return Input{}, ErrBlankName
	}
	if in.TypeID <= 0 {
		return Input{}, ErrTypeRequired
	}
	if in.AreaID <= 0 {
		return Input{}, ErrAreaRequired
	}
	in.State = strings.TrimSpace(in.State)
	if in.State == "" {
		in.State = string(StateOpen)
	}
	switch State(in.State) {
	case StateOpen, StatePermitRequired, StateTourist, StateClosed:
	default:
		return Input{}, ErrInvalidState
	}
	if !validCoordinates(in.Lat, in.Lon) {
		return Input{}, ErrInvalidCoords
	}
	if !validAltitude(in.AltitudeM) {
		return Input{}, ErrInvalidAltitude
	}
	if (in.DepthM != nil && (*in.DepthM < 0 || *in.DepthM > MaxDepthM)) ||
		(in.HorizontalLengthM != nil && (*in.HorizontalLengthM < 0 || *in.HorizontalLengthM > MaxHorizontalLengthM)) {
		return Input{}, ErrInvalidMeasure
	}
	in.AltNames = blankToNil(in.AltNames)
	in.Access, in.Description, in.TechnicalSheet = blankToNil(in.Access), blankToNil(in.Description), blankToNil(in.TechnicalSheet)
	return in, nil
}

// kindOf is the kind of the type a place is given, checking that what the place has fits it: only a
// cave has a depth, a horizontal length and a geology, and only a cave or a canyon a technical sheet. It also
// checks that the zone exists (before the statement, as a foreign key that fails would abort the
// transaction the tests run in).
func (s *Service) kindOf(ctx context.Context, in Input) (Kind, error) {
	if exists, err := s.repo.AreaExists(ctx, in.AreaID); err != nil {
		return "", err
	} else if !exists {
		return "", ErrUnknownArea
	}
	t, err := s.repo.GetType(ctx, in.TypeID)
	if errors.Is(err, ErrTypeNotFound) {
		return "", ErrUnknownType
	}
	if err != nil {
		return "", err
	}
	if (t.Kind != KindCave && (in.DepthM != nil || in.HorizontalLengthM != nil || in.GeologyID != nil)) ||
		(t.Kind == KindGeneral && in.TechnicalSheet != nil) {
		return "", ErrFieldsForKind
	}
	if in.GeologyID != nil {
		if _, err := s.repo.GetGeology(ctx, *in.GeologyID); errors.Is(err, ErrGeologyNotFound) {
			return "", ErrUnknownGeology
		} else if err != nil {
			return "", err
		}
	}
	return t.Kind, nil
}

func (s *Service) ListPlaces(ctx context.Context) ([]ListItem, error) { return s.repo.ListPlaces(ctx) }

func (s *Service) GetPlace(ctx context.Context, id int64) (Place, error) {
	return s.repo.GetPlace(ctx, id)
}

func (s *Service) CreatePlace(ctx context.Context, userID int64, in Input) (Place, error) {
	in, err := clean(in)
	if err != nil {
		return Place{}, err
	}
	kind, err := s.kindOf(ctx, in)
	if err != nil {
		return Place{}, err
	}
	return s.repo.CreatePlace(ctx, userID, kind, in)
}

func (s *Service) UpdatePlace(ctx context.Context, id, userID int64, in Input) (Place, error) {
	in, err := clean(in)
	if err != nil {
		return Place{}, err
	}
	kind, err := s.kindOf(ctx, in)
	if err != nil {
		return Place{}, err
	}
	return s.repo.UpdatePlace(ctx, id, userID, kind, in)
}

func (s *Service) DeletePlace(ctx context.Context, id int64) error {
	return s.repo.DeletePlace(ctx, id)
}

// ----- points -----

func cleanPoint(in PointInput) (PointInput, error) {
	switch PointKind(strings.TrimSpace(in.Kind)) {
	case PointParking, PointApproach, PointWaypoint, PointAccess, PointOther:
		in.Kind = strings.TrimSpace(in.Kind)
	default:
		return PointInput{}, ErrInvalidPointKind
	}
	if !validCoordinates(in.Lat, in.Lon) {
		return PointInput{}, ErrInvalidCoords
	}
	if !validAltitude(in.AltitudeM) {
		return PointInput{}, ErrInvalidAltitude
	}
	in.Name, in.Note = blankToNil(in.Name), blankToNil(in.Note)
	return in, nil
}

func (s *Service) AddPoint(ctx context.Context, placeID, userID int64, in PointInput) (Point, error) {
	in, err := cleanPoint(in)
	if err != nil {
		return Point{}, err
	}
	return s.repo.AddPoint(ctx, placeID, userID, in)
}

func (s *Service) GetPoint(ctx context.Context, placeID, id int64) (Point, error) {
	return s.repo.GetPoint(ctx, placeID, id)
}

func (s *Service) UpdatePoint(ctx context.Context, placeID, id, userID int64, in PointInput) (Point, error) {
	in, err := cleanPoint(in)
	if err != nil {
		return Point{}, err
	}
	return s.repo.UpdatePoint(ctx, placeID, id, userID, in)
}

func (s *Service) DeletePoint(ctx context.Context, placeID, id, userID int64) error {
	return s.repo.DeletePoint(ctx, placeID, id, userID)
}

// ----- types -----

func (s *Service) ListTypes(ctx context.Context) ([]Type, error) { return s.repo.ListTypes(ctx) }

func (s *Service) GetType(ctx context.Context, id int64) (Type, error) {
	return s.repo.GetType(ctx, id)
}

func cleanTypeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ErrBlankTypeName
	}
	return name, nil
}

// freeTypeName says that no type other than `except` has the name (ignoring case): checked before the
// statement, which would otherwise fail and abort the transaction the tests run in.
func (s *Service) freeTypeName(ctx context.Context, name string, except int64) error {
	t, err := s.repo.TypeNamed(ctx, name)
	if errors.Is(err, ErrTypeNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if t.ID != except {
		return ErrDuplicateType
	}
	return nil
}

func (s *Service) CreateType(ctx context.Context, name string) (Type, error) {
	name, err := cleanTypeName(name)
	if err != nil {
		return Type{}, err
	}
	if err := s.freeTypeName(ctx, name, 0); err != nil {
		return Type{}, err
	}
	return s.repo.CreateType(ctx, name)
}

// RenameType renames a general type: the types of caves and canyons are fixed.
func (s *Service) RenameType(ctx context.Context, id int64, name string) (Type, error) {
	name, err := cleanTypeName(name)
	if err != nil {
		return Type{}, err
	}
	t, err := s.repo.GetType(ctx, id)
	if err != nil {
		return Type{}, err
	}
	if t.Kind != KindGeneral {
		return Type{}, ErrFixedType
	}
	if err := s.freeTypeName(ctx, name, id); err != nil {
		return Type{}, err
	}
	return s.repo.RenameType(ctx, id, name)
}

// DeleteType removes a general type no place has.
func (s *Service) DeleteType(ctx context.Context, id int64) error {
	t, err := s.repo.GetType(ctx, id)
	if err != nil {
		return err
	}
	if t.Kind != KindGeneral {
		return ErrFixedType
	}
	if t.PlaceCount > 0 {
		return ErrTypeInUse
	}
	return s.repo.DeleteType(ctx, id)
}

// ----- geologies -----

func (s *Service) ListGeologies(ctx context.Context) ([]Geology, error) {
	return s.repo.ListGeologies(ctx)
}

func (s *Service) GetGeology(ctx context.Context, id int64) (Geology, error) {
	return s.repo.GetGeology(ctx, id)
}

func cleanGeologyName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ErrBlankGeologyName
	}
	return name, nil
}

// freeGeologyName says that no geology other than `except` has the name (ignoring case): checked before
// the statement, which would otherwise fail and abort the transaction the tests run in.
func (s *Service) freeGeologyName(ctx context.Context, name string, except int64) error {
	g, err := s.repo.GeologyNamed(ctx, name)
	if errors.Is(err, ErrGeologyNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if g.ID != except {
		return ErrDuplicateGeology
	}
	return nil
}

func (s *Service) CreateGeology(ctx context.Context, name string) (Geology, error) {
	name, err := cleanGeologyName(name)
	if err != nil {
		return Geology{}, err
	}
	if err := s.freeGeologyName(ctx, name, 0); err != nil {
		return Geology{}, err
	}
	return s.repo.CreateGeology(ctx, name)
}

func (s *Service) RenameGeology(ctx context.Context, id int64, name string) (Geology, error) {
	name, err := cleanGeologyName(name)
	if err != nil {
		return Geology{}, err
	}
	if _, err := s.repo.GetGeology(ctx, id); err != nil {
		return Geology{}, err
	}
	if err := s.freeGeologyName(ctx, name, id); err != nil {
		return Geology{}, err
	}
	return s.repo.RenameGeology(ctx, id, name)
}

// DeleteGeology removes a geology no place has.
func (s *Service) DeleteGeology(ctx context.Context, id int64) error {
	g, err := s.repo.GetGeology(ctx, id)
	if err != nil {
		return err
	}
	if g.PlaceCount > 0 {
		return ErrGeologyInUse
	}
	return s.repo.DeleteGeology(ctx, id)
}
