package distributions

import (
	"context"
	"errors"
	"fmt"
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
	ErrInvalidStatus   = errors.New("status must be draft, open, closed or done")
	// ErrInvalidArticles means an article with no name or a negative price, a
	// limit below 1, or two with the same name.
	ErrInvalidArticles = errors.New("each article needs a distinct name, a price of at least 0 and, if it has a limit, one of at least 1")
	// ErrArticlesConflict means a distribution with articles was also given what
	// belongs to one without: it is for people who sign up, and has no options,
	// extras or price of its own (each article has its price).
	ErrArticlesConflict = errors.New("a distribution with articles is for whoever signs up and has no options, extras or price of its own")
	// ErrArticleRequired means asking for a distribution with articles has to say
	// which article, and how many.
	ErrArticleRequired = errors.New("say which article you are asking for")
	// ErrInvalidQuantity means a quantity outside 1..maxQuantity, or one given
	// where it doesn't belong (or missing where it does).
	ErrInvalidQuantity = errors.New("quantity is for units of an article only, and must be between 1 and 100000")
	// ErrInvalidTransition means a status change that isn't one step forward
	// or back.
	ErrInvalidTransition = errors.New("the status can only move one step at a time")
	// ErrNotOpen means the distribution isn't taking choices (any more).
	ErrNotOpen = errors.New("this distribution is not open for choosing")
	// ErrNotEligible means somebody involved can't have this distribution
	// (nor ask for it): they weren't a member on its cutoff date.
	ErrNotEligible = errors.New("the person it is for, and whoever asks for it, must have been a member on the cutoff date (a member today, when there is none)")
	// ErrInvalidChoices means the choices don't match the distribution's
	// options: one missing, an unknown one, or a value it doesn't have.
	ErrInvalidChoices = errors.New("choices must give exactly one of the listed values for every option")
	// ErrUnitLocked means a unit can't be changed any more: it was collected,
	// or paid (only admin/board change those).
	ErrUnitLocked = errors.New("this unit was already collected or paid and can't be changed")
	// ErrNotDeliverable means units can only be handed out (collections recorded,
	// numbers written down) once the distribution is no longer a draft.
	ErrNotDeliverable = errors.New("units can only be handed out once the distribution is no longer a draft")
	ErrNoUnits        = errors.New("give at least one unit")
	// ErrInvalidCollector means the person the units were handed to is neither a
	// member now nor was one on the cutoff date.
	ErrInvalidCollector = errors.New("whoever collects must be a member now or have been one on the cutoff date")
	// ErrAutomaticUnit means somebody tried to remove a member's own unit of a
	// distribution for all members, which exists for everybody who is a member
	// on the cutoff date and would simply be made again.
	ErrAutomaticUnit = errors.New("in a distribution for all members everybody has their own unit, which can't be removed")
)

const maxIdentifierLength = 60

var (
	// ErrInvalidIdentifiers means numbers given for a unit that doesn't take
	// them, or more than its quantity, or an empty or repeated one.
	ErrInvalidIdentifiers = errors.New("this unit takes no numbers, or they are empty, repeated or more than its quantity")
	// ErrIdentifiersMissing means a unit of an article with numbers was to be
	// collected without as many numbers as its quantity.
	ErrIdentifiersMissing = errors.New("units of articles with numbers need all their numbers written down before they are collected")
)

// maxQuantity bounds one row's quantity: there is no per-person limit, so this
// only keeps a typo from entering a number nobody meant.
const maxQuantity = 100000

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

// normalizeArticles trims every name and rejects an article with no name, a
// negative price or a limit below 1, or a repeated name.
func normalizeArticles(articles []ArticleInput) ([]ArticleInput, error) {
	out := make([]ArticleInput, 0, len(articles))
	names := map[string]bool{}
	for _, a := range articles {
		name := strings.TrimSpace(a.Name)
		if name == "" || names[name] || a.Price < 0 || (a.MaxTotal != nil && *a.MaxTotal < 1) || (a.MaxPerPerson != nil && *a.MaxPerPerson < 1) {
			return nil, ErrInvalidArticles
		}
		names[name] = true
		article := ArticleInput{Name: name, Price: a.Price, MaxTotal: a.MaxTotal, MaxPerPerson: a.MaxPerPerson, HasIdentifiers: a.HasIdentifiers}
		// Described numbers: there are exactly as many items as combinations, which
		// is what there is to give out, and each has a number.
		article.IdentifierFields = []IdentifierField{}
		for _, f := range a.IdentifierFields {
			article.IdentifierFields = append(article.IdentifierFields, IdentifierField{Name: strings.TrimSpace(f.Name), Spec: strings.TrimSpace(f.Spec)})
		}
		n, err := buildNumbering(article.IdentifierFields)
		if err != nil {
			return nil, err
		}
		if n != nil {
			total := n.total()
			article.MaxTotal = &total
			article.HasIdentifiers = true
		}
		out = append(out, article)
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

// validateArticleKind checks that a distribution with articles has nothing that
// belongs to one without.
func validateArticleKind(f fields, articles, options int) error {
	if articles == 0 {
		return nil
	}
	if f.audience != AudienceSignup || f.price != 0 || f.extraPrice != nil || f.maxExtra != nil || options > 0 {
		return ErrArticlesConflict
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
	articles, err := normalizeArticles(in.Articles)
	if err != nil {
		return Distribution{}, err
	}
	in.Articles = articles
	if err := validateArticleKind(fields{in.Name, in.Audience, in.CutoffDate, in.Price, in.ExtraPrice, in.MaxExtra}, len(articles), len(options)); err != nil {
		return Distribution{}, err
	}
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
	current, err := s.repo.Get(ctx, id)
	if err != nil {
		return Distribution{}, err
	}
	if in.Articles != nil {
		articles, err := normalizeArticles(*in.Articles)
		if err != nil {
			return Distribution{}, err
		}
		in.Articles = &articles
	}
	// What the distribution will have once in is applied: a nil Articles or
	// Options leaves the current ones.
	articles, options := len(current.Articles), len(current.Options)
	if in.Articles != nil {
		articles = len(*in.Articles)
	}
	if in.Options != nil {
		options = len(*in.Options)
	}
	if err := validateArticleKind(fields{in.Name, in.Audience, in.CutoffDate, in.Price, in.ExtraPrice, in.MaxExtra}, articles, options); err != nil {
		return Distribution{}, err
	}
	if in.Status != nil {
		if !in.Status.Valid() {
			return Distribution{}, ErrInvalidStatus
		}
		step := in.Status.index() - current.Status.index()
		if step < -1 || step > 1 {
			return Distribution{}, ErrInvalidTransition
		}
	}
	d, err := s.repo.Update(ctx, id, in)
	if err != nil {
		return Distribution{}, err
	}
	// Opening a distribution for all members is when everybody gets their unit.
	if err := s.repo.SyncAutomaticUnits(ctx, d); err != nil {
		return Distribution{}, err
	}
	return s.repo.Get(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	return s.repo.Delete(ctx, id)
}

// IsEligible reports whether userID may have d: whoever was a member on its
// cutoff date, or a member today when it has none (a distribution people sign
// up for, with no date to look back at).
func (s *Service) IsEligible(ctx context.Context, d Distribution, userID int64) (bool, error) {
	if d.CutoffDate == nil {
		return s.repo.WasMemberNowOrOn(ctx, userID, nil)
	}
	return s.repo.WasMemberOn(ctx, userID, *d.CutoffDate)
}

// People lists who the pickers offer: for asking, those who may have the
// distribution (see IsEligible); for collecting, also whoever is a member now,
// since somebody who joined since can collect for others.
func (s *Service) People(ctx context.Context, d Distribution, forPickup bool) ([]Person, error) {
	if forPickup {
		return s.repo.ListMembers(ctx, true, d.CutoffDate)
	}
	return s.repo.ListMembers(ctx, d.CutoffDate == nil, d.CutoffDate)
}

// checkOpen says whether choices can be entered or changed now: only while the
// distribution is open — once it is closed nothing changes, for anybody, and
// reopening it is how a late addition is made. Members can also only before the
// deadline; staff (admin/board) can keep going after it for as long as it is
// open.
func (s *Service) checkOpen(d Distribution, staff bool) error {
	if d.Status != StatusOpen {
		return ErrNotOpen
	}
	if !staff && d.ClosesAt != nil && !s.now().Before(*d.ClosesAt) {
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
	// A distribution with articles takes a quantity of one of them; one without
	// takes neither.
	if len(d.Articles) > 0 {
		if in.ArticleID == nil {
			return Unit{}, ErrArticleRequired
		}
		if in.IsExtra {
			return Unit{}, ErrExtrasNotAllowed
		}
		if in.Quantity < 1 || in.Quantity > maxQuantity {
			return Unit{}, ErrInvalidQuantity
		}
	} else if in.ArticleID != nil {
		return Unit{}, ErrUnknownArticle
	} else if in.Quantity != 0 {
		return Unit{}, ErrInvalidQuantity
	}
	if in.Choices == nil {
		in.Choices = map[string]string{}
	}
	if err := validateChoices(d.Options, in.Choices); err != nil {
		return Unit{}, err
	}
	// Everybody's own unit has to exist before asking for one, so that asking
	// for it can choose its options instead of making a second one.
	if err := s.repo.SyncAutomaticUnits(ctx, d); err != nil {
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
	if in.ArticleID != nil {
		return s.repo.CreateArticleUnit(ctx, in)
	}
	return s.repo.CreateUnit(ctx, in)
}

func (s *Service) ListUnits(ctx context.Context, distributionID int64, involving *int64) ([]Unit, error) {
	d, err := s.repo.Get(ctx, distributionID)
	if err != nil {
		return nil, err
	}
	if err := s.repo.SyncAutomaticUnits(ctx, d); err != nil {
		return nil, err
	}
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
	// What is paid is for staff to change; what was handed out too, and in a unit
	// that isn't a row of an article (choices, one unit) not even them: a row of an
	// article just can't get smaller than what was handed out.
	if (u.PaidQuantity > 0 && !staff) || (u.PickedUpQuantity > 0 && (!staff || u.ArticleID == nil)) {
		return ErrUnitLocked
	}
	return nil
}

func (s *Service) UpdateChoices(ctx context.Context, distributionID, id int64, choices map[string]string, actorID int64, staff bool) (Unit, error) {
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
	return s.repo.UpdateChoices(ctx, distributionID, id, choices, actorID)
}

// UpdateQuantity changes how many units a row of an article stands for, under
// the same rules as changing what was chosen.
func (s *Service) UpdateQuantity(ctx context.Context, distributionID, id int64, quantity int, staff bool) (Unit, error) {
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
	if u.ArticleID == nil || quantity < 1 || quantity > maxQuantity {
		return Unit{}, ErrInvalidQuantity
	}
	return s.repo.UpdateQuantity(ctx, distributionID, id, quantity)
}

func (s *Service) DeleteUnit(ctx context.Context, distributionID, id int64, staff bool, actorID int64) error {
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
	// Units handed out have to be returned first.
	if u.PickedUpQuantity > 0 {
		return ErrUnitLocked
	}
	if d.Audience == AudienceAllMembers && !u.IsExtra {
		return ErrAutomaticUnit
	}
	return s.repo.DeleteUnit(ctx, distributionID, id, actorID)
}

// MarkPickedUp records that the deliveries — some units of a row each, see
// Delivery — were handed to pickedUpByID (any person), written down by
// recordedByID, while the distribution is not a draft; it says how many units
// went. For rows of articles with numbers, the units are the assigned numbers not
// delivered yet, and with assignNumbers the missing ones are first given from the
// next free (articles with described numbers).
func (s *Service) MarkPickedUp(ctx context.Context, distributionID int64, deliveries []Delivery, recordedByID, pickedUpByID int64, assignNumbers bool) (int, error) {
	if len(deliveries) == 0 {
		return 0, ErrNoUnits
	}
	for _, dl := range deliveries {
		if dl.Quantity < 0 || dl.Quantity > maxQuantity {
			return 0, ErrInvalidQuantity
		}
	}
	d, err := s.repo.Get(ctx, distributionID)
	if err != nil {
		return 0, err
	}
	if !d.Status.Deliverable() {
		return 0, ErrNotDeliverable
	}
	ok, err := s.repo.WasMemberNowOrOn(ctx, pickedUpByID, d.CutoffDate)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, ErrInvalidCollector
	}
	if err := s.repo.SyncAutomaticUnits(ctx, d); err != nil {
		return 0, err
	}
	// What is typed to say which units go can use ranges where the numbers are
	// described.
	for i, dl := range deliveries {
		u, err := s.repo.GetUnit(ctx, distributionID, dl.UnitID)
		if err != nil {
			return 0, err
		}
		if deliveries[i].Identifiers, err = expandTokens(d, u, dl.Identifiers); err != nil {
			return 0, err
		}
	}
	if assignNumbers {
		for _, dl := range deliveries {
			u, err := s.repo.GetUnit(ctx, distributionID, dl.UnitID)
			if err != nil {
				return 0, err
			}
			article, ok := articleOf(d, u)
			if !ok || len(article.IdentifierFields) == 0 || len(dl.Identifiers) > 0 {
				continue
			}
			// The ones to hand out are assigned numbers not delivered yet: give
			// as many more as are missing.
			n := dl.Quantity
			if n == 0 {
				n = u.Quantity - u.PickedUpQuantity
			}
			missing := n - (len(u.Identifiers) - len(u.DeliveredIdentifiers))
			if missing > 0 && u.PickedUpQuantity+n <= u.Quantity {
				if _, err := s.assignNumbers(ctx, d, u, len(u.Identifiers)+missing); err != nil {
					return 0, err
				}
			}
		}
	}
	return s.repo.Deliver(ctx, distributionID, deliveries, recordedByID, pickedUpByID)
}

// numberingOf is the described numbering of the unit's article, or nil when its
// numbers aren't described (or it has none).
func numberingOf(d Distribution, u Unit) (*numbering, error) {
	article, ok := articleOf(d, u)
	if !ok {
		return nil, nil
	}
	return buildNumbering(article.IdentifierFields)
}

// expandTokens trims what was typed to say which items an operation is about and,
// where the numbers of the article are described, expands the ranges in it
// ("0001-0010"; see numbering.expand).
func expandTokens(d Distribution, u Unit, tokens []string) ([]string, error) {
	if len(tokens) == 0 {
		return nil, nil
	}
	n, err := numberingOf(d, u)
	if err != nil {
		return nil, err
	}
	if n != nil {
		return n.expand(tokens)
	}
	out := make([]string, 0, len(tokens))
	for _, token := range tokens {
		out = append(out, strings.TrimSpace(token)) // an empty one is refused later
	}
	return out, nil
}

// checkIdentifiers cleans a list of numbers for the unit: none empty, too long,
// repeated or, where they are described, one that doesn't exist, and no more than
// the unit's quantity.
func checkIdentifiers(d Distribution, u Unit, identifiers []string) ([]string, error) {
	n, err := numberingOf(d, u)
	if err != nil {
		return nil, err
	}
	clean := make([]string, 0, len(identifiers))
	for _, identifier := range identifiers {
		identifier = strings.TrimSpace(identifier)
		if identifier == "" || len(identifier) > maxIdentifierLength || slices.Contains(clean, identifier) || (n != nil && !n.valid(identifier)) {
			return nil, ErrInvalidIdentifiers
		}
		clean = append(clean, identifier)
	}
	if len(clean) > u.Quantity {
		return nil, ErrInvalidIdentifiers
	}
	return clean, nil
}

// changeIdentifiers is what SetIdentifiers, AddIdentifiers and RemoveIdentifiers
// have in common: it fetches the unit, expands what was typed, builds the new list
// of numbers from the current one with change, and saves it, while the
// distribution is not a draft.
func (s *Service) changeIdentifiers(ctx context.Context, distributionID, id int64, typed []string, change func(current, typed []string) ([]string, error)) (Unit, error) {
	d, err := s.repo.Get(ctx, distributionID)
	if err != nil {
		return Unit{}, err
	}
	u, err := s.repo.GetUnit(ctx, distributionID, id)
	if err != nil {
		return Unit{}, err
	}
	if !d.Status.Deliverable() {
		return Unit{}, ErrNotDeliverable
	}
	expanded, err := expandTokens(d, u, typed)
	if err != nil {
		return Unit{}, err
	}
	changed, err := change(u.Identifiers, expanded)
	if err != nil {
		return Unit{}, err
	}
	clean, err := checkIdentifiers(d, u, changed)
	if err != nil {
		return Unit{}, err
	}
	if err := s.repo.SetIdentifiers(ctx, distributionID, id, clean); err != nil {
		return Unit{}, err
	}
	return s.repo.GetUnit(ctx, distributionID, id)
}

// SetIdentifiers replaces the numbers of a row of an article that has them. There
// can be fewer than its quantity (the rest are written later), never more, and
// none empty or repeated; the ones paid or handed out have to stay.
func (s *Service) SetIdentifiers(ctx context.Context, distributionID, id int64, identifiers []string) (Unit, error) {
	return s.changeIdentifiers(ctx, distributionID, id, identifiers, func(_, typed []string) ([]string, error) { return typed, nil })
}

// AddIdentifiers gives the row more numbers, keeping those it has.
func (s *Service) AddIdentifiers(ctx context.Context, distributionID, id int64, identifiers []string) (Unit, error) {
	return s.changeIdentifiers(ctx, distributionID, id, identifiers, func(current, typed []string) ([]string, error) {
		return append(slices.Clone(current), typed...), nil
	})
}

// RemoveIdentifiers takes numbers off the row, which has to have them and not
// have paid or handed them out: they are free again.
func (s *Service) RemoveIdentifiers(ctx context.Context, distributionID, id int64, identifiers []string) (Unit, error) {
	if len(identifiers) == 0 {
		return Unit{}, ErrInvalidIdentifiers
	}
	return s.changeIdentifiers(ctx, distributionID, id, identifiers, func(current, typed []string) ([]string, error) {
		for _, identifier := range typed {
			if !slices.Contains(current, identifier) {
				return nil, ErrInvalidIdentifiers
			}
		}
		return slices.DeleteFunc(slices.Clone(current), func(identifier string) bool { return slices.Contains(typed, identifier) }), nil
	})
}

// ClearPickup undoes a delivery of quantity units of the row (all of what was
// delivered when 0), or of the given delivered identifiers: they go back to not
// being handed out, and the numbers stay assigned to the row.
func (s *Service) ClearPickup(ctx context.Context, distributionID, id int64, quantity int, identifiers []string) error {
	if quantity < 0 {
		return ErrInvalidQuantity
	}
	d, u, err := s.distributionAndUnit(ctx, distributionID, id)
	if err != nil {
		return err
	}
	if identifiers, err = expandTokens(d, u, identifiers); err != nil {
		return err
	}
	return s.repo.ClearPickup(ctx, distributionID, id, quantity, identifiers)
}

// distributionAndUnit fetches both.
func (s *Service) distributionAndUnit(ctx context.Context, distributionID, id int64) (Distribution, Unit, error) {
	d, err := s.repo.Get(ctx, distributionID)
	if err != nil {
		return Distribution{}, Unit{}, err
	}
	u, err := s.repo.GetUnit(ctx, distributionID, id)
	return d, u, err
}

// ReturnUnits takes back units that were handed out and aren't paid: they leave
// the order (it gets smaller) and their numbers are free again; see
// Repository.ReturnUnits.
func (s *Service) ReturnUnits(ctx context.Context, distributionID, id int64, quantity int, identifiers []string) error {
	d, u, err := s.distributionAndUnit(ctx, distributionID, id)
	if err != nil {
		return err
	}
	if !d.Status.Deliverable() {
		return ErrNotDeliverable
	}
	if quantity < 0 {
		return ErrInvalidQuantity
	}
	if identifiers, err = expandTokens(d, u, identifiers); err != nil {
		return err
	}
	return s.repo.ReturnUnits(ctx, distributionID, id, quantity, identifiers)
}

func (s *Service) MarkPaid(ctx context.Context, distributionID int64, payments []Payment, markedByID int64) (int, error) {
	if len(payments) == 0 {
		return 0, ErrNoUnits
	}
	for _, p := range payments {
		if p.Quantity < 0 || p.Quantity > maxQuantity {
			return 0, ErrInvalidQuantity
		}
	}
	d, err := s.repo.Get(ctx, distributionID)
	if err != nil {
		return 0, err
	}
	for i, p := range payments {
		u, err := s.repo.GetUnit(ctx, distributionID, p.UnitID)
		if err != nil {
			return 0, err
		}
		if payments[i].Identifiers, err = expandTokens(d, u, p.Identifiers); err != nil {
			return 0, err
		}
	}
	return s.repo.MarkPaid(ctx, distributionID, payments, markedByID)
}

// ClearPaid takes quantity units off what is paid in a row (all when 0), or the
// given paid numbers.
func (s *Service) ClearPaid(ctx context.Context, distributionID, id int64, quantity int, identifiers []string, byID int64) error {
	d, u, err := s.distributionAndUnit(ctx, distributionID, id)
	if err != nil {
		return err
	}
	if identifiers, err = expandTokens(d, u, identifiers); err != nil {
		return err
	}
	return s.repo.ClearPaid(ctx, distributionID, id, quantity, identifiers, byID)
}

// ListPayments lists the payments of every distribution, newest first.
func (s *Service) ListPayments(ctx context.Context, f PaymentFilter) ([]PaymentEntry, error) {
	return s.repo.ListPayments(ctx, f)
}

func (s *Service) Order(ctx context.Context, distributionID int64) (Order, error) {
	d, err := s.repo.Get(ctx, distributionID)
	if err != nil {
		return Order{}, err
	}
	if err := s.repo.SyncAutomaticUnits(ctx, d); err != nil {
		return Order{}, err
	}
	return s.repo.Order(ctx, d)
}

// articleOf finds the article of a unit among the distribution's.
func articleOf(d Distribution, u Unit) (Article, bool) {
	for _, a := range d.Articles {
		if u.ArticleID != nil && a.ID == *u.ArticleID {
			return a, true
		}
	}
	return Article{}, false
}

// assignNumbers gives the unit as many of the next free numbers of its article
// as it lacks to have target of them, in the order the numbering lists them
// (the first field changing slowest), keeping the ones it has. Nothing is done
// for a unit that already has that many.
func (s *Service) assignNumbers(ctx context.Context, d Distribution, u Unit, target int) (Unit, error) {
	article, ok := articleOf(d, u)
	if !ok {
		return Unit{}, ErrNotNumbered
	}
	n, err := buildNumbering(article.IdentifierFields)
	if err != nil {
		return Unit{}, err
	}
	if n == nil {
		return Unit{}, ErrNotNumbered
	}
	need := target - len(u.Identifiers)
	if need <= 0 {
		return u, nil
	}
	assigned, err := s.repo.ArticleIdentifiers(ctx, article.ID)
	if err != nil {
		return Unit{}, err
	}
	taken := make(map[string]bool, len(assigned))
	for _, identifier := range assigned {
		taken[identifier] = true
	}
	var next []string
	n.each(func(identifier string) bool {
		if !taken[identifier] {
			next = append(next, identifier)
		}
		return len(next) < need
	})
	if len(next) < need {
		return Unit{}, fmt.Errorf("%w: %d free, %d needed", ErrNotEnoughNumbers, len(next), need)
	}
	if err := s.repo.SetIdentifiers(ctx, d.ID, u.ID, append(slices.Clone(u.Identifiers), next...)); err != nil {
		return Unit{}, err
	}
	return s.repo.GetUnit(ctx, d.ID, u.ID)
}

// AssignNumbers is assignNumbers for one unit, once the distribution is no
// longer a draft.
func (s *Service) AssignNumbers(ctx context.Context, distributionID, id int64) (Unit, error) {
	d, err := s.repo.Get(ctx, distributionID)
	if err != nil {
		return Unit{}, err
	}
	u, err := s.repo.GetUnit(ctx, distributionID, id)
	if err != nil {
		return Unit{}, err
	}
	if !d.Status.Deliverable() {
		return Unit{}, ErrNotDeliverable
	}
	return s.assignNumbers(ctx, d, u, u.Quantity)
}

// Numbers lists every item of a numbered article with who has it.
func (s *Service) Numbers(ctx context.Context, distributionID, articleID int64) (Numbers, error) {
	d, err := s.repo.Get(ctx, distributionID)
	if err != nil {
		return Numbers{}, err
	}
	for _, a := range d.Articles {
		if a.ID != articleID {
			continue
		}
		n, err := buildNumbering(a.IdentifierFields)
		if err != nil {
			return Numbers{}, err
		}
		if n == nil {
			return Numbers{}, ErrNotNumbered
		}
		holders, err := s.repo.Holders(ctx, a.ID)
		if err != nil {
			return Numbers{}, err
		}
		out := Numbers{ArticleID: a.ID, Fields: a.IdentifierFields, Total: n.total(), Items: make([]NumberItem, 0, n.total())}
		n.each(func(identifier string) bool {
			item := NumberItem{Identifier: identifier}
			if h, ok := holders[identifier]; ok {
				item.UnitID, item.BeneficiaryLogin, item.BeneficiaryName, item.Delivered, item.Paid = &h.UnitID, &h.Login, &h.Name, h.Delivered, h.Paid
				out.Assigned++
			}
			out.Items = append(out.Items, item)
			return true
		})
		out.Free = out.Total - out.Assigned
		return out, nil
	}
	return Numbers{}, ErrUnknownArticle
}
