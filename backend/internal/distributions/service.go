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
	if u.PickedUpAt != nil || (u.PaidAt != nil && !staff) {
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
	if d.Audience == AudienceAllMembers && !u.IsExtra {
		return ErrAutomaticUnit
	}
	return s.repo.DeleteUnit(ctx, distributionID, id)
}

// MarkPickedUp records the collection of the given units, while the distribution
// is not a draft: pickedUpByID is who they were handed to (any person), recordedByID
// who wrote it down.
func (s *Service) MarkPickedUp(ctx context.Context, distributionID int64, ids []int64, recordedByID, pickedUpByID int64, assignNumbers bool) error {
	if len(ids) == 0 {
		return ErrNoUnits
	}
	d, err := s.repo.Get(ctx, distributionID)
	if err != nil {
		return err
	}
	if !d.Status.Deliverable() {
		return ErrNotDeliverable
	}
	ok, err := s.repo.WasMemberNowOrOn(ctx, pickedUpByID, d.CutoffDate)
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidCollector
	}
	if err := s.repo.SyncAutomaticUnits(ctx, d); err != nil {
		return err
	}
	// Units of articles with described numbers can be given the next free ones
	// on the way, in the order they are listed.
	if assignNumbers {
		for _, id := range ids {
			u, err := s.repo.GetUnit(ctx, distributionID, id)
			if err != nil {
				return err
			}
			if article, ok := articleOf(d, u); ok && len(article.IdentifierFields) > 0 {
				if _, err := s.assignNumbers(ctx, d, u); err != nil {
					return err
				}
			}
		}
	}
	missing, err := s.repo.MissingIdentifiers(ctx, distributionID, ids)
	if err != nil {
		return err
	}
	if missing > 0 {
		return ErrIdentifiersMissing
	}
	return s.repo.MarkPickedUp(ctx, distributionID, ids, recordedByID, pickedUpByID)
}

// SetIdentifiers writes down the numbers of the items handed to the person, for
// a row of an article that has them, once the distribution is no longer a draft.
// There can be fewer than its quantity (the rest are written later), never more,
// and none empty or repeated.
func (s *Service) SetIdentifiers(ctx context.Context, distributionID, id int64, identifiers []string) (Unit, error) {
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
	// Where the numbers of the article are described, what is typed may use
	// ranges, and each has to be one of those that exist.
	var n *numbering
	for _, a := range d.Articles {
		if u.ArticleID != nil && a.ID == *u.ArticleID {
			if n, err = buildNumbering(a.IdentifierFields); err != nil {
				return Unit{}, err
			}
		}
	}
	if n != nil {
		if identifiers, err = n.expand(identifiers); err != nil {
			return Unit{}, err
		}
	}
	clean := make([]string, 0, len(identifiers))
	for _, identifier := range identifiers {
		identifier = strings.TrimSpace(identifier)
		if identifier == "" || len(identifier) > maxIdentifierLength || slices.Contains(clean, identifier) || (n != nil && !n.valid(identifier)) {
			return Unit{}, ErrInvalidIdentifiers
		}
		clean = append(clean, identifier)
	}
	if len(clean) > u.Quantity {
		return Unit{}, ErrInvalidIdentifiers
	}
	if err := s.repo.SetIdentifiers(ctx, distributionID, id, clean); err != nil {
		return Unit{}, err
	}
	return s.repo.GetUnit(ctx, distributionID, id)
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
// as it still lacks to have one per unit, in the order the numbering lists them
// (the first field changing slowest), keeping the ones it has. Nothing is done
// for a unit that already has all of them.
func (s *Service) assignNumbers(ctx context.Context, d Distribution, u Unit) (Unit, error) {
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
	need := u.Quantity - len(u.Identifiers)
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
	return s.assignNumbers(ctx, d, u)
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
				item.UnitID, item.BeneficiaryLogin, item.BeneficiaryName = &h.UnitID, &h.Login, &h.Name
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
