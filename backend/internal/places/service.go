package places

import (
	"context"
	"errors"
	"math"
	"strings"
	"unicode/utf8"
)

type Service struct {
	repo       *Repository
	storage    *Storage
	users      UserLookup
	activities ActivityLookup
}

func NewService(repo *Repository, storage *Storage, users UserLookup) *Service {
	return &Service{repo: repo, storage: storage, users: users}
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

// cleanAltNames trims the other names, drops the blank ones and the repeated ones (ignoring case), and
// refuses too many or too long ones. It always gives a list, never nil.
func cleanAltNames(names []string) ([]string, error) {
	clean := []string{}
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		key := strings.ToLower(name)
		if name == "" || seen[key] {
			continue
		}
		if utf8.RuneCountInString(name) > MaxAltNameLength {
			return nil, ErrInvalidAltNames
		}
		seen[key] = true
		clean = append(clean, name)
	}
	if len(clean) > MaxAltNames {
		return nil, ErrInvalidAltNames
	}
	return clean, nil
}

// clean checks and trims what a place is given, without the type (see typeOf). A blank state stays
// blank here: whether it has one, and which, depends on the type.
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
	if (in.OsmType == nil) != (in.OsmID == nil) {
		return Input{}, ErrUnknownOsmElement
	}
	if in.OsmType != nil {
		t := strings.TrimSpace(*in.OsmType)
		if t != "node" && t != "way" && t != "relation" || *in.OsmID <= 0 {
			return Input{}, ErrUnknownOsmElement
		}
		in.OsmType = &t
	}
	in.State = strings.TrimSpace(in.State)
	switch State(in.State) {
	case "", StateOpen, StatePermitRequired, StateTourist, StateClosed:
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
	var err error
	if in.AltNames, err = cleanAltNames(in.AltNames); err != nil {
		return Input{}, err
	}
	in.Access, in.Description, in.TechnicalSheet = blankToNil(in.Access), blankToNil(in.Description), blankToNil(in.TechnicalSheet)
	return in, nil
}

// typeOf is the type a place is given, checking that what the place has fits it: only a cavity has a
// subtype (and it must), a system, a depth, a horizontal length and a geology, only a cavity or a canyon a technical
// sheet, and only a type that has a state gives its places one (open, when none is said). It also checks
// that the zone exists (before the statement, as a foreign key that fails would abort the transaction
// the tests run in). It answers with the input as it is to be saved.
func (s *Service) typeOf(ctx context.Context, in Input) (Type, Input, error) {
	if exists, err := s.repo.AreaExists(ctx, in.AreaID); err != nil {
		return Type{}, in, err
	} else if !exists {
		return Type{}, in, ErrUnknownArea
	}
	t, err := s.repo.GetType(ctx, in.TypeID)
	if errors.Is(err, ErrTypeNotFound) {
		return Type{}, in, ErrUnknownType
	}
	if err != nil {
		return Type{}, in, err
	}
	if (t.Kind != KindCave && (in.SubtypeID != nil || in.SystemID != nil || in.DepthM != nil ||
		in.HorizontalLengthM != nil || in.GeologyID != nil)) ||
		(t.Kind == KindGeneral && in.TechnicalSheet != nil) ||
		(!t.HasState && in.State != "") {
		return Type{}, in, ErrFieldsForKind
	}
	if t.HasState && in.State == "" {
		in.State = string(StateOpen)
	}
	if t.Kind == KindCave {
		if in.SubtypeID == nil {
			return Type{}, in, ErrSubtypeRequired
		}
		if _, err := s.repo.GetSubtype(ctx, *in.SubtypeID); errors.Is(err, ErrSubtypeNotFound) {
			return Type{}, in, ErrUnknownSubtype
		} else if err != nil {
			return Type{}, in, err
		}
	}
	if in.SystemID != nil {
		if exists, err := s.repo.SystemExists(ctx, *in.SystemID); err != nil {
			return Type{}, in, err
		} else if !exists {
			return Type{}, in, ErrUnknownSystem
		}
	}
	if in.GeologyID != nil {
		if _, err := s.repo.GetGeology(ctx, *in.GeologyID); errors.Is(err, ErrGeologyNotFound) {
			return Type{}, in, ErrUnknownGeology
		} else if err != nil {
			return Type{}, in, err
		}
	}
	return t, in, nil
}

func (s *Service) ListPlaces(ctx context.Context) ([]ListItem, error) {
	list, err := s.repo.ListPlaces(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]*ListItem, len(list))
	for i := range list {
		items[i] = &list[i]
	}
	if err := s.countActivities(ctx, items...); err != nil {
		return nil, err
	}
	return list, nil
}

func (s *Service) GetPlace(ctx context.Context, id int64) (Place, error) {
	p, err := s.repo.GetPlace(ctx, id)
	return s.namePlace(ctx, p, err)
}

func (s *Service) CreatePlace(ctx context.Context, userID int64, in Input) (Place, error) {
	in, err := clean(in)
	if err != nil {
		return Place{}, err
	}
	t, in, err := s.typeOf(ctx, in)
	if err != nil {
		return Place{}, err
	}
	p, err := s.repo.CreatePlace(ctx, userID, t, in)
	return s.namePlace(ctx, p, err)
}

func (s *Service) UpdatePlace(ctx context.Context, id, userID int64, in Input) (Place, error) {
	in, err := clean(in)
	if err != nil {
		return Place{}, err
	}
	t, in, err := s.typeOf(ctx, in)
	if err != nil {
		return Place{}, err
	}
	p, err := s.repo.UpdatePlace(ctx, id, userID, t, in)
	return s.namePlace(ctx, p, err)
}

// patchAttempts is how many times a partial edit starts again when somebody saves the place between
// its read and its write.
const patchAttempts = 3

// PatchPlace changes some fields of a place and leaves the rest as they are: it reads the place,
// gives its fields as an Input to `apply`, which changes what the edit says, and saves the result
// with the same checks as a whole edit. It writes only if the place is as it was read (when it
// is not, it starts again with the new one), so what somebody saved meanwhile is never overwritten.
func (s *Service) PatchPlace(ctx context.Context, id, userID int64, apply func(Input) (Input, error)) (Place, error) {
	for range patchAttempts {
		current, err := s.repo.GetPlace(ctx, id)
		if err != nil {
			return Place{}, err
		}
		in, err := apply(inputOf(current))
		if err != nil {
			return Place{}, err
		}
		in, err = clean(in)
		if err != nil {
			return Place{}, err
		}
		t, in, err := s.typeOf(ctx, in)
		if err != nil {
			return Place{}, err
		}
		p, err := s.repo.UpdatePlaceIfUnchanged(ctx, id, userID, t, in, current.UpdatedAt)
		if errors.Is(err, ErrConcurrentUpdate) {
			continue
		}
		return s.namePlace(ctx, p, err)
	}
	return Place{}, ErrConcurrentUpdate
}

// inputOf is the Input that a place is as it stands: what an edit that changes nothing would send.
func inputOf(p Place) Input {
	return Input{
		TypeID: p.TypeID, Name: p.Name, AltNames: p.AltNames, State: string(p.State),
		SubtypeID: p.SubtypeID, SystemID: p.SystemID, AreaID: p.AreaID, Lat: &p.Lat, Lon: &p.Lon,
		AltitudeM: p.AltitudeM, Access: p.Access, Description: p.Description, DepthM: p.DepthM,
		HorizontalLengthM: p.HorizontalLengthM, GeologyID: p.GeologyID, TechnicalSheet: p.TechnicalSheet,
	}
}

// LinkOsm makes a place the OpenStreetMap element of the mirror that osmType and osmID name, instead of
// the one it was, if any. Only the pair and the place are read: nothing of the place is changed with it.
func (s *Service) LinkOsm(ctx context.Context, id, userID int64, osmType string, osmID int64) (Place, error) {
	osmType = strings.TrimSpace(osmType)
	if osmType != "node" && osmType != "way" && osmType != "relation" || osmID <= 0 {
		return Place{}, ErrUnknownOsmElement
	}
	p, err := s.repo.SetOsm(ctx, id, userID, &osmType, &osmID)
	return s.namePlace(ctx, p, err)
}

// ReviewOsm says that a person has looked at what changed in the element of the place: from now on only what
// changes after this is told.
func (s *Service) ReviewOsm(ctx context.Context, id int64) (Place, error) {
	p, err := s.repo.ReviewOsm(ctx, id)
	return s.namePlace(ctx, p, err)
}

// UnlinkOsm says the place is not an element of OpenStreetMap any more (the element stays in the mirror,
// and is a candidate again).
func (s *Service) UnlinkOsm(ctx context.Context, id, userID int64) (Place, error) {
	p, err := s.repo.SetOsm(ctx, id, userID, nil, nil)
	return s.namePlace(ctx, p, err)
}

// DeletePlace removes a place with its points and its files.
func (s *Service) DeletePlace(ctx context.Context, id int64) error {
	// A place that activities went to is not deleted: their history stays.
	if has, err := s.activities.HasActivities(ctx, id); err != nil {
		return err
	} else if has {
		return ErrHasActivities
	}
	files, err := s.repo.DeletePlace(ctx, id)
	if err != nil {
		return err
	}
	for _, fileID := range files {
		s.removeBytes(id, fileID)
	}
	return nil
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

func (s *Service) CreateType(ctx context.Context, name string, hasState bool) (Type, error) {
	name, err := cleanTypeName(name)
	if err != nil {
		return Type{}, err
	}
	if err := s.freeTypeName(ctx, name, 0); err != nil {
		return Type{}, err
	}
	return s.repo.CreateType(ctx, name, hasState)
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

// ----- subtypes -----

func (s *Service) ListSubtypes(ctx context.Context) ([]Subtype, error) {
	return s.repo.ListSubtypes(ctx)
}

func (s *Service) GetSubtype(ctx context.Context, id int64) (Subtype, error) {
	return s.repo.GetSubtype(ctx, id)
}

func cleanSubtypeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ErrBlankSubtypeName
	}
	return name, nil
}

// freeSubtypeName says that no subtype other than `except` has the name (ignoring case): checked before
// the statement, which would otherwise fail and abort the transaction the tests run in.
func (s *Service) freeSubtypeName(ctx context.Context, name string, except int64) error {
	g, err := s.repo.SubtypeNamed(ctx, name)
	if errors.Is(err, ErrSubtypeNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if g.ID != except {
		return ErrDuplicateSubtype
	}
	return nil
}

func (s *Service) CreateSubtype(ctx context.Context, name string) (Subtype, error) {
	name, err := cleanSubtypeName(name)
	if err != nil {
		return Subtype{}, err
	}
	if err := s.freeSubtypeName(ctx, name, 0); err != nil {
		return Subtype{}, err
	}
	return s.repo.CreateSubtype(ctx, name)
}

func (s *Service) RenameSubtype(ctx context.Context, id int64, name string) (Subtype, error) {
	name, err := cleanSubtypeName(name)
	if err != nil {
		return Subtype{}, err
	}
	if _, err := s.repo.GetSubtype(ctx, id); err != nil {
		return Subtype{}, err
	}
	if err := s.freeSubtypeName(ctx, name, id); err != nil {
		return Subtype{}, err
	}
	return s.repo.RenameSubtype(ctx, id, name)
}

// DeleteSubtype removes a subtype no place has.
func (s *Service) DeleteSubtype(ctx context.Context, id int64) error {
	g, err := s.repo.GetSubtype(ctx, id)
	if err != nil {
		return err
	}
	if g.PlaceCount > 0 {
		return ErrSubtypeInUse
	}
	return s.repo.DeleteSubtype(ctx, id)
}
