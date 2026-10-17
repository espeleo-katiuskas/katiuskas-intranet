package distributions

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
)

var (
	ErrInvalidName     = errors.New("name is required")
	ErrInvalidAudience = errors.New("audience must be all_members or signup")
	ErrCutoffRequired  = errors.New("a distribution for all members needs a cutoff_date")
	ErrInvalidPrice    = errors.New("prices can't be negative")
	ErrInvalidMaxExtra = errors.New("max_extra needs an extra_price and can't be negative")
	ErrInvalidOptions  = errors.New("each option needs a name and at least one value, and names and values can't repeat")
	ErrInvalidStatus   = errors.New("status must be draft, open, closed, ordered, received or done")
	// ErrInvalidTransition means a status change that isn't one step forward
	// or back.
	ErrInvalidTransition = errors.New("the status can only move one step at a time")
	// ErrNotOpen means the distribution isn't taking choices (any more).
	ErrNotOpen = errors.New("this distribution is not open for choosing")
	// ErrNotEligible means somebody involved can't have this distribution
	// (nor ask for it): they weren't a member on its cutoff date.
	ErrNotEligible = errors.New("the person it is for, and whoever asks for it, must have been a member on the cutoff date")
	// ErrInvalidChoices means the choices don't match the distribution's
	// options: one missing, an unknown one, or a value it doesn't have.
	ErrInvalidChoices = errors.New("choices must give exactly one of the listed values for every option")
	// ErrUnitLocked means a unit can't be changed any more: it was collected,
	// or paid (only admin/board change those).
	ErrUnitLocked = errors.New("this unit was already collected or paid and can't be changed")
	// ErrNotReceived means collections can only be recorded once the order
	// has arrived.
	ErrNotReceived = errors.New("the order has not been received yet")
	ErrNoUnits     = errors.New("give at least one unit")
)

type Service struct {
	repo *Repository
	now  func() time.Time
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo, now: time.Now}
}

// normalizeOptions trims every name and value, and rejects an option with no
// name or values, or a repeated option name or value.
func normalizeOptions(options []OptionInput) ([]OptionInput, error) {
	out := make([]OptionInput, 0, len(options))
	names := map[string]bool{}
	for _, o := range options {
		name := strings.TrimSpace(o.Name)
		if name == "" || len(o.Values) == 0 || names[name] {
			return nil, ErrInvalidOptions
		}
		names[name] = true
		values := make([]string, 0, len(o.Values))
		for _, v := range o.Values {
			v = strings.TrimSpace(v)
			if v == "" || slices.Contains(values, v) {
				return nil, ErrInvalidOptions
			}
			values = append(values, v)
		}
		out = append(out, OptionInput{Name: name, Values: values})
	}
	return out, nil
}

type fields struct {
	name       string
	audience   Audience
	cutoffDate *time.Time
	price      float64
	extraPrice *float64
	maxExtra   *int
}

func validateFields(f fields) error {
	if strings.TrimSpace(f.name) == "" {
		return ErrInvalidName
	}
	if !f.audience.Valid() {
		return ErrInvalidAudience
	}
	if f.audience == AudienceAllMembers && f.cutoffDate == nil {
		return ErrCutoffRequired
	}
	if f.price < 0 || (f.extraPrice != nil && *f.extraPrice < 0) {
		return ErrInvalidPrice
	}
	if f.maxExtra != nil && (*f.maxExtra < 0 || f.extraPrice == nil) {
		return ErrInvalidMaxExtra
	}
	return nil
}

func (s *Service) Create(ctx context.Context, in CreateInput) (Distribution, error) {
	in.Name = strings.TrimSpace(in.Name)
	if err := validateFields(fields{in.Name, in.Audience, in.CutoffDate, in.Price, in.ExtraPrice, in.MaxExtra}); err != nil {
		return Distribution{}, err
	}
	options, err := normalizeOptions(in.Options)
	if err != nil {
		return Distribution{}, err
	}
	in.Options = options
	return s.repo.Create(ctx, in)
}

func (s *Service) Get(ctx context.Context, id int64) (Distribution, error) {
	return s.repo.Get(ctx, id)
}

func (s *Service) List(ctx context.Context, includeDrafts bool) ([]Distribution, error) {
	return s.repo.List(ctx, includeDrafts)
}

func (s *Service) Update(ctx context.Context, id int64, in UpdateInput) (Distribution, error) {
	in.Name = strings.TrimSpace(in.Name)
	if err := validateFields(fields{in.Name, in.Audience, in.CutoffDate, in.Price, in.ExtraPrice, in.MaxExtra}); err != nil {
		return Distribution{}, err
	}
	if in.Options != nil {
		options, err := normalizeOptions(*in.Options)
		if err != nil {
			return Distribution{}, err
		}
		in.Options = &options
	}
	if in.Status != nil {
		if !in.Status.Valid() {
			return Distribution{}, ErrInvalidStatus
		}
		current, err := s.repo.Get(ctx, id)
		if err != nil {
			return Distribution{}, err
		}
		if step := in.Status.index() - current.Status.index(); step < -1 || step > 1 {
			return Distribution{}, ErrInvalidTransition
		}
	}
	return s.repo.Update(ctx, id, in)
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	return s.repo.Delete(ctx, id)
}

// IsEligible reports whether userID may have d: whoever was a member on its
// cutoff date, or anybody when it has none (a distribution people sign up for).
func (s *Service) IsEligible(ctx context.Context, d Distribution, userID int64) (bool, error) {
	if d.CutoffDate == nil {
		return true, nil
	}
	return s.repo.WasMemberOn(ctx, userID, *d.CutoffDate)
}

// checkOpen says whether choices can be entered or changed now. Members can
// while it is open and before its deadline; staff (admin/board) can also
// after the deadline and while it is closed, until it has been ordered.
func (s *Service) checkOpen(d Distribution, staff bool) error {
	if staff {
		if d.Status == StatusOpen || d.Status == StatusClosed {
			return nil
		}
		return ErrNotOpen
	}
	if d.Status != StatusOpen || (d.ClosesAt != nil && !s.now().Before(*d.ClosesAt)) {
		return ErrNotOpen
	}
	return nil
}

func validateChoices(options []Option, choices map[string]string) error {
	if len(choices) != len(options) {
		return ErrInvalidChoices
	}
	for _, o := range options {
		value, ok := choices[o.Name]
		if !ok || !slices.Contains(o.Values, value) {
			return ErrInvalidChoices
		}
	}
	return nil
}

// RegisterUnit enters one unit for the beneficiary, who has to be eligible.
func (s *Service) RegisterUnit(ctx context.Context, in RegisterInput) (Unit, error) {
	d, err := s.repo.Get(ctx, in.DistributionID)
	if err != nil {
		return Unit{}, err
	}
	if err := s.checkOpen(d, in.Staff); err != nil {
		return Unit{}, err
	}
	if in.Choices == nil {
		in.Choices = map[string]string{}
	}
	if err := validateChoices(d.Options, in.Choices); err != nil {
		return Unit{}, err
	}
	// Both the person it is for and, unless staff, the one entering it have
	// to be eligible: asking for a distribution is for those it is for, and
	// they can do it for themselves or for another of them.
	people := []int64{in.BeneficiaryID}
	if !in.Staff && in.RegisteredByID != in.BeneficiaryID {
		people = append(people, in.RegisteredByID)
	}
	for _, id := range people {
		eligible, err := s.IsEligible(ctx, d, id)
		if err != nil {
			return Unit{}, err
		}
		if !eligible {
			return Unit{}, ErrNotEligible
		}
	}
	return s.repo.CreateUnit(ctx, in)
}

func (s *Service) ListUnits(ctx context.Context, distributionID int64, involving *int64) ([]Unit, error) {
	return s.repo.ListUnits(ctx, distributionID, involving)
}

func (s *Service) GetUnit(ctx context.Context, distributionID, id int64) (Unit, error) {
	return s.repo.GetUnit(ctx, distributionID, id)
}

// checkEditable is checkOpen plus the unit's own state: collected is final,
// and paid only changes through staff.
func (s *Service) checkEditable(d Distribution, u Unit, staff bool) error {
	if err := s.checkOpen(d, staff); err != nil {
		return err
	}
	if u.PickedUpAt != nil || (u.PaidAt != nil && !staff) {
		return ErrUnitLocked
	}
	return nil
}

func (s *Service) UpdateChoices(ctx context.Context, distributionID, id int64, choices map[string]string, staff bool) (Unit, error) {
	d, err := s.repo.Get(ctx, distributionID)
	if err != nil {
		return Unit{}, err
	}
	u, err := s.repo.GetUnit(ctx, distributionID, id)
	if err != nil {
		return Unit{}, err
	}
	if err := s.checkEditable(d, u, staff); err != nil {
		return Unit{}, err
	}
	if choices == nil {
		choices = map[string]string{}
	}
	if err := validateChoices(d.Options, choices); err != nil {
		return Unit{}, err
	}
	return s.repo.UpdateChoices(ctx, distributionID, id, choices)
}

func (s *Service) DeleteUnit(ctx context.Context, distributionID, id int64, staff bool) error {
	d, err := s.repo.Get(ctx, distributionID)
	if err != nil {
		return err
	}
	u, err := s.repo.GetUnit(ctx, distributionID, id)
	if err != nil {
		return err
	}
	if err := s.checkEditable(d, u, staff); err != nil {
		return err
	}
	return s.repo.DeleteUnit(ctx, distributionID, id)
}

// MarkPickedUp records the collection of the given units, once the order has
// arrived: pickedUpByID is who they were handed to (any person), recordedByID
// who wrote it down.
func (s *Service) MarkPickedUp(ctx context.Context, distributionID int64, ids []int64, recordedByID, pickedUpByID int64) error {
	if len(ids) == 0 {
		return ErrNoUnits
	}
	d, err := s.repo.Get(ctx, distributionID)
	if err != nil {
		return err
	}
	if d.Status != StatusReceived {
		return ErrNotReceived
	}
	return s.repo.MarkPickedUp(ctx, distributionID, ids, recordedByID, pickedUpByID)
}

func (s *Service) ClearPickup(ctx context.Context, distributionID, id int64) error {
	return s.repo.ClearPickup(ctx, distributionID, id)
}

func (s *Service) MarkPaid(ctx context.Context, distributionID int64, ids []int64, markedByID int64) error {
	if len(ids) == 0 {
		return ErrNoUnits
	}
	if _, err := s.repo.Get(ctx, distributionID); err != nil {
		return err
	}
	return s.repo.MarkPaid(ctx, distributionID, ids, markedByID)
}

func (s *Service) ClearPaid(ctx context.Context, distributionID, id int64) error {
	return s.repo.ClearPaid(ctx, distributionID, id)
}

func (s *Service) Order(ctx context.Context, distributionID int64) (Order, error) {
	d, err := s.repo.Get(ctx, distributionID)
	if err != nil {
		return Order{}, err
	}
	return s.repo.Order(ctx, d)
}
