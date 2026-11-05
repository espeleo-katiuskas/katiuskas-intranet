package distributions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
)

// UserLookup translates between a person's login and their numeric id, the
// same small local interface every other package keyed by user id has
// (users.Service satisfies it structurally).
type UserLookup interface {
	UserIDForLogin(ctx context.Context, login string) (int64, error)
	LoginForUserID(ctx context.Context, id int64) (string, error)
}

type Handler struct {
	service       *Service
	authenticator *auth.Authenticator
	users         UserLookup
	notifier      httpapi.MutationNotifier
}

func NewHandler(service *Service, authenticator *auth.Authenticator, users UserLookup, notifier httpapi.MutationNotifier) *Handler {
	return &Handler{service: service, authenticator: authenticator, users: users, notifier: notifier}
}

// auditableFields are the Distribution fields update's notification diffs —
// every one UpdateInput can change.
var auditableFields = []string{
	"Name", "Description", "Audience", "Visibility", "Status", "ClosesAt", "CutoffDate", "Price", "ExtraPrice", "MaxExtra", "Options", "Articles", "AccountName",
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/distributions", h.authenticator.Protect(h.list))
	mux.Handle("POST /api/v1/distributions", h.authenticator.Protect(h.create, "admin", "board"))
	mux.Handle("GET /api/v1/distributions/{id}", h.authenticator.Protect(h.get))
	mux.Handle("PUT /api/v1/distributions/{id}", h.authenticator.Protect(h.update, "admin", "board"))
	mux.Handle("DELETE /api/v1/distributions/{id}", h.authenticator.Protect(h.delete, "admin"))

	mux.Handle("GET /api/v1/distributions/{id}/payments", h.authenticator.Protect(h.listPayments, "admin", "board"))
	mux.Handle("GET /api/v1/distributions/{id}/people", h.authenticator.Protect(h.people))
	mux.Handle("GET /api/v1/distributions/{id}/units", h.authenticator.Protect(h.listUnits))
	mux.Handle("POST /api/v1/distributions/{id}/units", h.authenticator.Protect(h.registerUnit))
	mux.Handle("PATCH /api/v1/distributions/{id}/units/{unitID}", h.authenticator.Protect(h.updateUnit))
	mux.Handle("DELETE /api/v1/distributions/{id}/units/{unitID}", h.authenticator.Protect(h.deleteUnit))

	mux.Handle("GET /api/v1/distributions/{id}/order", h.authenticator.Protect(h.order, "admin", "board", "material"))
	mux.Handle("POST /api/v1/distributions/{id}/pickups", h.authenticator.Protect(h.markPickedUp, "admin", "board", "material"))
	mux.Handle("DELETE /api/v1/distributions/{id}/units/{unitID}/pickup", h.authenticator.Protect(h.clearPickup, "admin", "board", "material"))
	mux.Handle("PUT /api/v1/distributions/{id}/units/{unitID}/identifiers", h.authenticator.Protect(h.setIdentifiers, "admin", "board", "material"))
	mux.Handle("POST /api/v1/distributions/{id}/units/{unitID}/identifiers", h.authenticator.Protect(h.addIdentifiers, "admin", "board", "material"))
	mux.Handle("DELETE /api/v1/distributions/{id}/units/{unitID}/identifiers", h.authenticator.Protect(h.removeIdentifiers, "admin", "board", "material"))
	mux.Handle("POST /api/v1/distributions/{id}/units/{unitID}/identifiers/assign", h.authenticator.Protect(h.assignNumbers, "admin", "board", "material"))
	mux.Handle("GET /api/v1/distributions/{id}/articles/{articleID}/numbers", h.authenticator.Protect(h.numbers, "admin", "board", "material"))
	mux.Handle("POST /api/v1/distributions/{id}/units/{unitID}/return", h.authenticator.Protect(h.returnUnits, "admin", "board", "material"))
	mux.Handle("POST /api/v1/distributions/{id}/payments", h.authenticator.Protect(h.markPaid, "admin", "board"))
	// A sale over the counter: the order, its numbers, the payment and the hand-over in one go.
	mux.Handle("POST /api/v1/distributions/{id}/sales", h.authenticator.Protect(h.sell, "admin", "board"))
	// Taking off a payment does not delete anything: it records the reverse (a payment of the other
	// sign in the log and a reverse transaction in the accounting), so it is a POST.
	mux.Handle("POST /api/v1/distributions/{id}/units/{unitID}/payment/reversal", h.authenticator.Protect(h.clearPaid, "admin", "board"))
}

func (h *Handler) actorLogin(ctx context.Context) string {
	current, _ := auth.UserFromContext(ctx)
	login, err := h.users.LoginForUserID(ctx, current.ID)
	if err != nil {
		return fmt.Sprintf("user#%d", current.ID)
	}
	return login
}

// writeError maps what the service and repository report to a response.
func writeError(w http.ResponseWriter, err error, internalMessage string) {
	known := []struct {
		err    error
		status int
		code   string
	}{
		{ErrNotFound, http.StatusNotFound, "not_found"},
		{ErrUnitNotFound, http.StatusNotFound, "not_found"},
		{ErrInvalidName, http.StatusBadRequest, "invalid_name"},
		{ErrInvalidAudience, http.StatusBadRequest, "invalid_audience"},
		{ErrInvalidVisibility, http.StatusBadRequest, "invalid_visibility"},
		{ErrCutoffRequired, http.StatusBadRequest, "cutoff_required"},
		{ErrInvalidPrice, http.StatusBadRequest, "invalid_price"},
		{ErrInvalidMaxExtra, http.StatusBadRequest, "invalid_max_extra"},
		{ErrInvalidOptions, http.StatusBadRequest, "invalid_options"},
		{ErrInvalidStatus, http.StatusBadRequest, "invalid_status"},
		{ErrInvalidArticles, http.StatusBadRequest, "invalid_articles"},
		{ErrArticlesConflict, http.StatusBadRequest, "articles_conflict"},
		{ErrArticleRequired, http.StatusBadRequest, "article_required"},
		{ErrInvalidQuantity, http.StatusBadRequest, "invalid_quantity"},
		{ErrUnknownArticle, http.StatusBadRequest, "unknown_article"},
		{ErrArticleExhausted, http.StatusConflict, "article_exhausted"},
		{ErrPersonLimit, http.StatusConflict, "article_person_limit"},
		{ErrInvalidIdentifiers, http.StatusBadRequest, "invalid_identifiers"},
		{ErrIdentifierNotAssigned, http.StatusBadRequest, "identifier_not_assigned"},
		{ErrNotPaid, http.StatusConflict, "identifier_not_paid"},
		{ErrNotDelivered, http.StatusConflict, "identifier_not_delivered"},
		{ErrIdentifierTaken, http.StatusConflict, "identifier_taken"},
		{ErrIdentifiersMissing, http.StatusConflict, "identifiers_missing"},
		{ErrInvalidNumbering, http.StatusBadRequest, "invalid_numbering"},
		{ErrNumberInUse, http.StatusConflict, "number_in_use"},
		{ErrNotEnoughNumbers, http.StatusConflict, "not_enough_numbers"},
		{ErrNotNumbered, http.StatusBadRequest, "article_not_numbered"},
		{ErrCapBelowRequested, http.StatusConflict, "cap_below_requested"},
		{ErrInvalidChoices, http.StatusBadRequest, "invalid_choices"},
		{ErrNoUnits, http.StatusBadRequest, "no_units"},
		{ErrInvalidCollector, http.StatusConflict, "invalid_collector"},
		{ErrUnknownUser, http.StatusBadRequest, "unknown_user"},
		{ErrInvalidTransition, http.StatusConflict, "invalid_status_transition"},
		{ErrHasUnits, http.StatusConflict, "has_units"},
		{ErrHasSettledUnits, http.StatusConflict, "has_settled_units"},
		{ErrNotOpen, http.StatusConflict, "not_open"},
		{ErrNotEligible, http.StatusConflict, "not_eligible"},
		{ErrAlreadyRegistered, http.StatusConflict, "already_registered"},
		{ErrExtrasNotAllowed, http.StatusConflict, "extras_not_allowed"},
		{ErrTooManyExtras, http.StatusConflict, "too_many_extras"},
		{ErrUnitLocked, http.StatusConflict, "unit_locked"},
		{ErrNotDeliverable, http.StatusConflict, "not_deliverable"},
		{ErrAlreadyPickedUp, http.StatusConflict, "already_picked_up"},
		{ErrAlreadyPaid, http.StatusConflict, "already_paid"},
		{ErrBelowPaid, http.StatusConflict, "quantity_below_paid"},
		{ErrBelowDelivered, http.StatusConflict, "quantity_below_delivered"},
		{ErrBelowNumbered, http.StatusConflict, "quantity_below_numbered"},
		{ErrUnknownAccount, http.StatusBadRequest, "unknown_account"},
		{ErrAccountNotIncome, http.StatusBadRequest, "account_not_income"},
		{ErrAccountHasChildren, http.StatusConflict, "account_has_children"},
		{ErrAccountNotAsset, http.StatusBadRequest, "account_not_asset"},
		{ErrAccountRequired, http.StatusBadRequest, "account_required"},
		{ErrNoDistributionAccount, http.StatusConflict, "no_distribution_account"},
		{ErrUnitPaid, http.StatusConflict, "unit_paid"},
		{ErrAutomaticUnit, http.StatusConflict, "automatic_unit"},
	}
	for _, k := range known {
		if errors.Is(err, k.err) {
			httpapi.WriteError(w, k.status, k.code, err.Error())
			return
		}
	}
	httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", internalMessage)
}

func parseID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return 0, false
	}
	return id, true
}

func isStaff(u auth.AuthenticatedUser) bool { return u.HasAnyRole("admin", "board") }

// canSeeEveryone is who can read every unit of a distribution, and who is staff for
// changing orders: the people who manage it and whoever hands it out.
func canSeeEveryone(u auth.AuthenticatedUser) bool { return u.HasAnyRole("admin", "board", "material") }

type optionBody struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

func optionInputs(in []optionBody) []OptionInput {
	out := make([]OptionInput, len(in))
	for i, o := range in {
		out[i] = OptionInput(o)
	}
	return out
}

type articleBody struct {
	Name             string            `json:"name"`
	Price            float64           `json:"price"`
	MaxTotal         *int              `json:"max_total"`
	MaxPerPerson     *int              `json:"max_per_person"`
	HasIdentifiers   bool              `json:"has_identifiers"`
	IdentifierFields []IdentifierField `json:"identifier_fields"`
}

func articleInputs(in []articleBody) []ArticleInput {
	out := make([]ArticleInput, len(in))
	for i, a := range in {
		out[i] = ArticleInput(a)
	}
	return out
}

type distributionBody struct {
	Name        string         `json:"name"`
	Description *string        `json:"description"`
	Audience    Audience       `json:"audience"`
	Visibility  *Visibility    `json:"visibility"`
	ClosesAt    *time.Time     `json:"closes_at"`
	CutoffDate  *string        `json:"cutoff_date"`
	Price       float64        `json:"price"`
	ExtraPrice  *float64       `json:"extra_price"`
	MaxExtra    *int           `json:"max_extra"`
	Options     *[]optionBody  `json:"options"`
	Articles    *[]articleBody `json:"articles"`
	AccountID   *int64         `json:"account_id"`
	Status      *Status        `json:"status"`
}

// decodeDistribution reads a create/update body, with the cutoff date parsed.
// An empty audience means the default, all_members.
func decodeDistribution(w http.ResponseWriter, r *http.Request) (distributionBody, *time.Time, bool) {
	var body distributionBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return body, nil, false
	}
	if body.Audience == "" {
		body.Audience = AudienceAllMembers
	}
	var cutoff *time.Time
	if body.CutoffDate != nil && *body.CutoffDate != "" {
		date, err := time.Parse("2006-01-02", *body.CutoffDate)
		if err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "cutoff_date must be YYYY-MM-DD")
			return body, nil, false
		}
		cutoff = &date
	}
	return body, cutoff, true
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	current, _ := auth.UserFromContext(r.Context())
	list, err := h.service.List(r.Context(), isStaff(current))
	if err != nil {
		writeError(w, err, "could not list distributions")
		return
	}
	visibleList := list[:0]
	for _, d := range list {
		if canSee(current, d) {
			visibleList = append(visibleList, d)
		}
	}
	list = visibleList
	for i := range list {
		if err := h.fillEligible(r.Context(), &list[i], current.ID); err != nil {
			writeError(w, err, "could not check eligibility")
			return
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) fillEligible(ctx context.Context, d *Distribution, userID int64) error {
	eligible, err := h.service.IsEligible(ctx, *d, userID)
	if err != nil {
		return err
	}
	d.Eligible = &eligible
	return nil
}

// canSee says whether a caller may see a distribution, and so act on it at all:
// drafts exist only for admin and board, and its visibility keeps it from anybody
// below the level it names (each level includes the ones above it).
func canSee(u auth.AuthenticatedUser, d Distribution) bool {
	if d.Status == StatusDraft && !isStaff(u) {
		return false
	}
	return visibleTo(u, d)
}

// visibleTo is the part of canSee that comes from the distribution's visibility.
func visibleTo(u auth.AuthenticatedUser, d Distribution) bool {
	switch d.Visibility {
	case VisibilityMaterial:
		return u.HasAnyRole("admin", "board", "material")
	case VisibilityBoard:
		return u.HasAnyRole("admin", "board")
	case VisibilityAdmin:
		return u.HasAnyRole("admin")
	default:
		return true
	}
}

// visible fetches a distribution for a caller: one the caller cannot see (see
// canSee) is a 404, as if it did not exist, for every route about it.
func (h *Handler) visible(w http.ResponseWriter, r *http.Request, id int64) (Distribution, bool) {
	current, _ := auth.UserFromContext(r.Context())
	d, err := h.service.Get(r.Context(), id)
	if err == nil && !canSee(current, d) {
		err = ErrNotFound
	}
	if err != nil {
		writeError(w, err, "could not fetch distribution")
		return Distribution{}, false
	}
	return d, true
}

// accessible is visible without the rule about drafts, for the routes of the people who put an
// order together and hand it out, which have always worked on a draft too (and answer for it
// themselves: nothing can be handed out of one).
func (h *Handler) accessible(w http.ResponseWriter, r *http.Request, id int64) (Distribution, bool) {
	current, _ := auth.UserFromContext(r.Context())
	d, err := h.service.Get(r.Context(), id)
	if err == nil && !visibleTo(current, d) {
		err = ErrNotFound
	}
	if err != nil {
		writeError(w, err, "could not fetch distribution")
		return Distribution{}, false
	}
	return d, true
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}
	d, ok := h.visible(w, r, id)
	if !ok {
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	if err := h.fillEligible(r.Context(), &d, current.ID); err != nil {
		writeError(w, err, "could not check eligibility")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, d)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	body, cutoff, ok := decodeDistribution(w, r)
	if !ok {
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	in := CreateInput{
		Name: body.Name, Description: body.Description, Audience: body.Audience, ClosesAt: body.ClosesAt,
		CutoffDate: cutoff, Price: body.Price, ExtraPrice: body.ExtraPrice, MaxExtra: body.MaxExtra,
		AccountID: body.AccountID, CreatedBy: current.ID,
	}
	if body.Visibility != nil {
		// Only admin sets who can see a distribution: anybody else can only leave it at the default.
		if *body.Visibility != VisibilityAll && !current.HasAnyRole("admin") {
			writeVisibilityForbidden(w)
			return
		}
		in.Visibility = *body.Visibility
	}
	if body.Options != nil {
		in.Options = optionInputs(*body.Options)
	}
	if body.Articles != nil {
		in.Articles = articleInputs(*body.Articles)
	}
	d, err := h.service.Create(r.Context(), in)
	if err != nil {
		writeError(w, err, "could not create distribution")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🆕 %s ha creado el reparto «%s».", h.actorLogin(r.Context()), d.Name))
	httpapi.WriteJSON(w, http.StatusCreated, d)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}
	body, cutoff, ok := decodeDistribution(w, r)
	if !ok {
		return
	}
	before, ok := h.visible(w, r, id)
	if !ok {
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	in := UpdateInput{
		Name: body.Name, Description: body.Description, Audience: body.Audience, ClosesAt: body.ClosesAt,
		CutoffDate: cutoff, Price: body.Price, ExtraPrice: body.ExtraPrice, MaxExtra: body.MaxExtra,
		AccountID: body.AccountID, Status: body.Status,
	}
	// Only admin changes who can see it; sending back what it already is, as a form does, is fine.
	if body.Visibility != nil && *body.Visibility != before.Visibility {
		if !current.HasAnyRole("admin") {
			writeVisibilityForbidden(w)
			return
		}
		in.Visibility = body.Visibility
	}
	if body.Options != nil {
		options := optionInputs(*body.Options)
		in.Options = &options
	}
	if body.Articles != nil {
		articles := articleInputs(*body.Articles)
		in.Articles = &articles
	}
	d, err := h.service.Update(r.Context(), id, in)
	if err != nil {
		writeError(w, err, "could not update distribution")
		return
	}
	if diff := httpapi.DiffFields(before, d, auditableFields...); len(diff) > 0 {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha editado el reparto «%s»:\n%s",
			h.actorLogin(r.Context()), d.Name, strings.Join(diff, "\n")))
	}
	httpapi.WriteJSON(w, http.StatusOK, d)
}

func writeVisibilityForbidden(w http.ResponseWriter) {
	httpapi.WriteError(w, http.StatusForbidden, "forbidden", "only admin can change who can see a distribution")
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}
	d, err := h.service.Get(r.Context(), id)
	if err != nil {
		writeError(w, err, "could not fetch distribution")
		return
	}
	if err := h.service.Delete(r.Context(), id); err != nil {
		writeError(w, err, "could not delete distribution")
		return
	}
	lost := ""
	if d.UnitCount > 0 {
		lost = fmt.Sprintf(" (con sus %d unidades)", d.UnitCount)
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha borrado el reparto «%s»%s.", h.actorLogin(r.Context()), d.Name, lost))
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

// people lists who the pickers of the pages offer, so nobody has to choose from
// everybody on file: for=order (any baseline role) are those who may have the
// distribution; for=pickup (admin, board and material) also whoever is a member
// now.
func (h *Handler) people(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}
	d, ok := h.visible(w, r, id)
	if !ok {
		return
	}
	forPickup := r.URL.Query().Get("for") == "pickup"
	if current, _ := auth.UserFromContext(r.Context()); forPickup && !canSeeEveryone(current) {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "insufficient permissions")
		return
	}
	list, err := h.service.People(r.Context(), d, forPickup)
	if err != nil {
		writeError(w, err, "could not list people")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

// listUnits shows everything to the people who manage or hand out the
// distribution, and to anybody else only the units that are for them or that
// they entered.
func (h *Handler) listUnits(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}
	if _, ok := h.visible(w, r, id); !ok {
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	var involving *int64
	if !canSeeEveryone(current) {
		involving = &current.ID
	}
	units, err := h.service.ListUnits(r.Context(), id, involving)
	if err != nil {
		writeError(w, err, "could not list units")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, units)
}

func (h *Handler) registerUnit(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		BeneficiaryLogin *string           `json:"beneficiary_login"`
		IsExtra          bool              `json:"is_extra"`
		Choices          map[string]string `json:"choices"`
		ArticleID        *int64            `json:"article_id"`
		Quantity         int               `json:"quantity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	d, ok := h.visible(w, r, id)
	if !ok {
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	beneficiaryID, beneficiaryLogin := current.ID, ""
	if body.BeneficiaryLogin != nil {
		bid, err := h.users.UserIDForLogin(r.Context(), *body.BeneficiaryLogin)
		if err != nil {
			httpapi.WriteError(w, http.StatusNotFound, "not_found", "user not found")
			return
		}
		beneficiaryID, beneficiaryLogin = bid, *body.BeneficiaryLogin
	}
	unit, err := h.service.RegisterUnit(r.Context(), RegisterInput{
		DistributionID: id, BeneficiaryID: beneficiaryID, RegisteredByID: current.ID,
		IsExtra: body.IsExtra, Choices: body.Choices, ArticleID: body.ArticleID, Quantity: body.Quantity,
		Staff: canSeeEveryone(current),
	})
	if err != nil {
		writeError(w, err, "could not register unit")
		return
	}
	what := "«" + d.Name + "»"
	if unit.ArticleName != nil {
		what = fmt.Sprintf("%d × «%s» de %s", unit.Quantity, *unit.ArticleName, what)
	}
	if body.IsExtra {
		what = "una unidad extra de " + what
	}
	if beneficiaryID == current.ID {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🆕 %s ha pedido %s.", h.actorLogin(r.Context()), what))
	} else {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🆕 %s ha pedido %s para %s.", h.actorLogin(r.Context()), what, beneficiaryLogin))
	}
	httpapi.WriteJSON(w, http.StatusCreated, unit)
}

// sell is a sale over the counter, which would otherwise take an order, numbering, a payment and a
// delivery one after the other: it does all of them at once for the person in the body.
func (h *Handler) sell(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		BeneficiaryLogin string            `json:"beneficiary_login"`
		IsExtra          bool              `json:"is_extra"`
		Choices          map[string]string `json:"choices"`
		ArticleID        *int64            `json:"article_id"`
		Quantity         int               `json:"quantity"`
		Identifiers      []string          `json:"identifiers"`
		Paid             bool              `json:"paid"`
		AccountID        int64             `json:"account_id"`
		Delivered        bool              `json:"delivered"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	d, ok := h.accessible(w, r, id)
	if !ok {
		return
	}
	beneficiaryID, err := h.users.UserIDForLogin(r.Context(), body.BeneficiaryLogin)
	if err != nil {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	unit, err := h.service.Sell(r.Context(), SaleInput{
		DistributionID: id, BeneficiaryID: beneficiaryID, StaffID: current.ID, IsExtra: body.IsExtra,
		Choices: body.Choices, ArticleID: body.ArticleID, Quantity: body.Quantity, Identifiers: body.Identifiers,
		Paid: body.Paid, AccountID: body.AccountID, Delivered: body.Delivered,
	})
	if err != nil {
		writeError(w, err, "could not record the sale")
		return
	}
	what := "«" + d.Name + "»"
	if unit.ArticleName != nil {
		what = fmt.Sprintf("%d × «%s» de %s", body.Quantity, *unit.ArticleName, what)
	}
	var did []string
	if body.Paid {
		did = append(did, "cobrado")
	}
	if body.Delivered {
		did = append(did, "entregado")
	}
	done := ""
	if len(did) > 0 {
		done = " (" + strings.Join(did, " y ") + ")"
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🛒 %s ha vendido %s a %s%s.",
		h.actorLogin(r.Context()), what, body.BeneficiaryLogin, done))
	httpapi.WriteJSON(w, http.StatusCreated, unit)
}

// ownUnit fetches a unit and checks the caller may change it: the person it
// is for, whoever entered it, or admin/board/material (who hand things out and
// fix orders for whoever they are), who also go past the deadline, change a paid
// unit and act while it is closed or done: for changing orders they count as
// staff (Service's `staff`).
func (h *Handler) ownUnit(w http.ResponseWriter, r *http.Request) (Distribution, Unit, bool) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return Distribution{}, Unit{}, false
	}
	unitID, ok := parseID(w, r, "unitID")
	if !ok {
		return Distribution{}, Unit{}, false
	}
	d, ok := h.accessible(w, r, id)
	if !ok {
		return Distribution{}, Unit{}, false
	}
	unit, err := h.service.GetUnit(r.Context(), id, unitID)
	if err != nil {
		writeError(w, err, "could not fetch unit")
		return Distribution{}, Unit{}, false
	}
	current, _ := auth.UserFromContext(r.Context())
	if !canSeeEveryone(current) && current.ID != unit.BeneficiaryID && (unit.RegisteredByID == nil || current.ID != *unit.RegisteredByID) {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "only the person it is for, whoever entered it, material, board or admin can change this")
		return Distribution{}, Unit{}, false
	}
	return d, unit, true
}

func (h *Handler) updateUnit(w http.ResponseWriter, r *http.Request) {
	d, unit, ok := h.ownUnit(w, r)
	if !ok {
		return
	}
	var body struct {
		Choices  map[string]string `json:"choices"`
		Quantity *int              `json:"quantity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	// What can change on a unit depends on what it is: how many, for a row of an
	// article; what was chosen, for any other.
	if unit.ArticleID != nil || body.Quantity != nil {
		if body.Quantity == nil || unit.ArticleID == nil {
			writeError(w, ErrInvalidQuantity, "")
			return
		}
		updated, err := h.service.UpdateQuantity(r.Context(), d.ID, unit.ID, *body.Quantity, canSeeEveryone(current))
		if err != nil {
			writeError(w, err, "could not update unit")
			return
		}
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha cambiado a %d la cantidad de «%s» para %s en «%s».",
			h.actorLogin(r.Context()), updated.Quantity, *updated.ArticleName, unit.BeneficiaryLogin, d.Name))
		httpapi.WriteJSON(w, http.StatusOK, updated)
		return
	}
	if body.Choices == nil {
		httpapi.WriteError(w, http.StatusBadRequest, "missing_fields", "send choices (or quantity, for an article)")
		return
	}
	updated, err := h.service.UpdateChoices(r.Context(), d.ID, unit.ID, body.Choices, current.ID, canSeeEveryone(current))
	if err != nil {
		writeError(w, err, "could not update unit")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha cambiado lo elegido para %s en «%s».",
		h.actorLogin(r.Context()), unit.BeneficiaryLogin, d.Name))
	httpapi.WriteJSON(w, http.StatusOK, updated)
}

func (h *Handler) deleteUnit(w http.ResponseWriter, r *http.Request) {
	d, unit, ok := h.ownUnit(w, r)
	if !ok {
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	if err := h.service.DeleteUnit(r.Context(), d.ID, unit.ID, canSeeEveryone(current)); err != nil {
		writeError(w, err, "could not delete unit")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha quitado una unidad de «%s» para %s.",
		h.actorLogin(r.Context()), d.Name, unit.BeneficiaryLogin))
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

func (h *Handler) order(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}
	if _, ok := h.accessible(w, r, id); !ok {
		return
	}
	order, err := h.service.Order(r.Context(), id)
	if err != nil {
		writeError(w, err, "could not summarize the order")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, order)
}

func (h *Handler) markPickedUp(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}
	// Whole rows (all that is still to hand out of each) by unit_ids, or part of a
	// row by deliveries; both may come together.
	var body struct {
		UnitIDs    []int64 `json:"unit_ids"`
		Deliveries []struct {
			UnitID      int64    `json:"unit_id"`
			Quantity    int      `json:"quantity"`
			Identifiers []string `json:"identifiers"`
		} `json:"deliveries"`
		PickedUpByLogin string `json:"picked_up_by_login"`
		AssignNumbers   bool   `json:"assign_numbers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	pickedUpBy, err := h.users.UserIDForLogin(r.Context(), body.PickedUpByLogin)
	if err != nil {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	d, ok := h.accessible(w, r, id)
	if !ok {
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	var deliveries []Delivery
	for _, unitID := range body.UnitIDs {
		deliveries = append(deliveries, Delivery{UnitID: unitID})
	}
	for _, dl := range body.Deliveries {
		deliveries = append(deliveries, Delivery{UnitID: dl.UnitID, Quantity: dl.Quantity, Identifiers: dl.Identifiers})
	}
	delivered, err := h.service.MarkPickedUp(r.Context(), id, deliveries, current.ID, pickedUpBy, body.AssignNumbers)
	if err != nil {
		writeError(w, err, "could not record the collection")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("📦 %s ha apuntado que %s ha recogido %d unidades de «%s».",
		h.actorLogin(r.Context()), body.PickedUpByLogin, delivered, d.Name))
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

func (h *Handler) setIdentifiers(w http.ResponseWriter, r *http.Request) {
	d, unit, ok := h.unitForStaff(w, r)
	if !ok {
		return
	}
	var body struct {
		Identifiers []string `json:"identifiers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	updated, err := h.service.SetIdentifiers(r.Context(), d.ID, unit.ID, body.Identifiers)
	if err != nil {
		writeError(w, err, "could not write down the numbers")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🔢 %s ha apuntado los números de «%s» de %s en «%s»: %s.",
		h.actorLogin(r.Context()), deref(unit.ArticleName), unit.BeneficiaryLogin, d.Name, formatNumbers(updated.Identifiers)))
	httpapi.WriteJSON(w, http.StatusOK, updated)
}

// addIdentifiers gives a row more numbers, keeping those it has.
func (h *Handler) addIdentifiers(w http.ResponseWriter, r *http.Request) {
	d, unit, ok := h.unitForStaff(w, r)
	if !ok {
		return
	}
	var body struct {
		Identifiers []string `json:"identifiers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	updated, err := h.service.AddIdentifiers(r.Context(), d.ID, unit.ID, body.Identifiers)
	if err != nil {
		writeError(w, err, "could not add the numbers")
		return
	}
	// Said as what was added — the numbers it has now that it didn't have — in order
	// and as ranges, whatever way they were typed.
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🔢 %s ha asignado números de «%s» a %s en «%s»: %s.",
		h.actorLogin(r.Context()), deref(unit.ArticleName), unit.BeneficiaryLogin, d.Name,
		formatNumbers(withoutThose(updated.Identifiers, unit.Identifiers))))
	httpapi.WriteJSON(w, http.StatusOK, updated)
}

// removeIdentifiers takes numbers (?identifiers=0001-0003,0010) off a row, which
// must not have paid or handed them out: they are free again.
func (h *Handler) removeIdentifiers(w http.ResponseWriter, r *http.Request) {
	d, unit, ok := h.unitForStaff(w, r)
	if !ok {
		return
	}
	var identifiers []string
	if list := r.URL.Query().Get("identifiers"); list != "" {
		identifiers = strings.Split(list, ",")
	}
	updated, err := h.service.RemoveIdentifiers(r.Context(), d.ID, unit.ID, identifiers)
	if err != nil {
		writeError(w, err, "could not remove the numbers")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🔢 %s ha quitado números de «%s» a %s en «%s»: %s.",
		h.actorLogin(r.Context()), deref(unit.ArticleName), unit.BeneficiaryLogin, d.Name,
		formatNumbers(withoutThose(unit.Identifiers, updated.Identifiers))))
	httpapi.WriteJSON(w, http.StatusOK, updated)
}

// assignNumbers gives the unit the next free numbers of its article up to its
// quantity, keeping those it already has.
func (h *Handler) assignNumbers(w http.ResponseWriter, r *http.Request) {
	d, unit, ok := h.unitForStaff(w, r)
	if !ok {
		return
	}
	updated, err := h.service.AssignNumbers(r.Context(), d.ID, unit.ID)
	if err != nil {
		writeError(w, err, "could not assign numbers")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🔢 %s ha asignado los números de «%s» de %s en «%s»: %s.",
		h.actorLogin(r.Context()), deref(unit.ArticleName), unit.BeneficiaryLogin, d.Name,
		formatNumbers(withoutThose(updated.Identifiers, unit.Identifiers))))
	httpapi.WriteJSON(w, http.StatusOK, updated)
}

// numbers lists every item of a numbered article and who has it.
func (h *Handler) numbers(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}
	articleID, ok := parseID(w, r, "articleID")
	if !ok {
		return
	}
	if _, ok := h.accessible(w, r, id); !ok {
		return
	}
	numbers, err := h.service.Numbers(r.Context(), id, articleID)
	if err != nil {
		writeError(w, err, "could not list the numbers")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, numbers)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (h *Handler) clearPickup(w http.ResponseWriter, r *http.Request) {
	d, unit, ok := h.unitForStaff(w, r)
	if !ok {
		return
	}
	// ?quantity=N (or ?identifiers=a,b) undoes only that much of the delivery.
	quantity := 0
	if q := r.URL.Query().Get("quantity"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 1 {
			writeError(w, ErrInvalidQuantity, "")
			return
		}
		quantity = n
	}
	var identifiers []string
	if list := r.URL.Query().Get("identifiers"); list != "" {
		identifiers = strings.Split(list, ",")
	}
	if err := h.service.ClearPickup(r.Context(), d.ID, unit.ID, quantity, identifiers); err != nil {
		writeError(w, err, "could not undo the collection")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("↩️ %s ha deshecho la recogida de una unidad de «%s» de %s.",
		h.actorLogin(r.Context()), d.Name, unit.BeneficiaryLogin))
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

// returnUnits takes back units that were handed out and aren't paid: they leave
// the order and their numbers are free again.
func (h *Handler) returnUnits(w http.ResponseWriter, r *http.Request) {
	d, unit, ok := h.unitForStaff(w, r)
	if !ok {
		return
	}
	var body struct {
		Quantity    int      `json:"quantity"`
		Identifiers []string `json:"identifiers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if err := h.service.ReturnUnits(r.Context(), d.ID, unit.ID, body.Quantity, body.Identifiers); err != nil {
		writeError(w, err, "could not return the units")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("↩️ %s ha devuelto unidades de «%s» de %s en «%s».",
		h.actorLogin(r.Context()), deref(unit.ArticleName), unit.BeneficiaryLogin, d.Name))
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

// listPayments is the list of payments of one distribution, newest first, with
// what it had collected after each one; admin and board only, as it says who paid
// what. from/to (YYYY-MM-DD, inclusive) and limit/offset (default 50, at most
// 100) narrow and page it.
func (h *Handler) listPayments(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}
	if _, ok := h.accessible(w, r, id); !ok {
		return
	}
	q := r.URL.Query()
	filter := PaymentFilter{DistributionID: &id}
	filter.Limit, _ = strconv.Atoi(q.Get("limit"))
	filter.Offset, _ = strconv.Atoi(q.Get("offset"))
	if filter.Limit <= 0 || filter.Limit > 100 {
		filter.Limit = 50
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	for _, field := range []struct {
		name string
		dest **time.Time
	}{{"from", &filter.From}, {"to", &filter.To}} {
		raw := q.Get(field.name)
		if raw == "" {
			continue
		}
		day, err := time.Parse("2006-01-02", raw)
		if err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", field.name+" must be a date as YYYY-MM-DD")
			return
		}
		*field.dest = &day
	}
	if filter.From != nil && filter.To != nil && filter.To.Before(*filter.From) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_date_range", "to must not be before from")
		return
	}
	list, err := h.service.ListPayments(r.Context(), filter)
	if err != nil {
		writeError(w, err, "could not list the payments")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) markPaid(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}
	// Whole rows (what is still to pay of each) by unit_ids, or part of a row by
	// payments; both may come together.
	var body struct {
		UnitIDs []int64 `json:"unit_ids"`
		// AccountID is the asset account the payment was made in.
		AccountID *int64 `json:"account_id"`
		Payments  []struct {
			UnitID      int64    `json:"unit_id"`
			Quantity    int      `json:"quantity"`
			Identifiers []string `json:"identifiers"`
		} `json:"payments"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	d, ok := h.accessible(w, r, id)
	if !ok {
		return
	}
	var payments []Payment
	for _, unitID := range body.UnitIDs {
		payments = append(payments, Payment{UnitID: unitID})
	}
	for _, p := range body.Payments {
		payments = append(payments, Payment{UnitID: p.UnitID, Quantity: p.Quantity, Identifiers: p.Identifiers})
	}
	current, _ := auth.UserFromContext(r.Context())
	var assetAccount int64
	if body.AccountID != nil {
		assetAccount = *body.AccountID
	}
	marked, err := h.service.MarkPaid(r.Context(), id, payments, current.ID, assetAccount)
	if err != nil {
		writeError(w, err, "could not mark as paid")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("💶 %s ha marcado como pagadas %d unidades de «%s».",
		h.actorLogin(r.Context()), marked, d.Name))
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

func (h *Handler) clearPaid(w http.ResponseWriter, r *http.Request) {
	d, unit, ok := h.unitForStaff(w, r)
	if !ok {
		return
	}
	// quantity takes only that many units off what is paid; without it, all of it. identifiers are
	// the numbers of a numbered article to take off. account_id is the asset account the money goes
	// back from.
	var body struct {
		AccountID   int64    `json:"account_id"`
		Quantity    *int     `json:"quantity"`
		Identifiers []string `json:"identifiers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	quantity := 0
	if body.Quantity != nil {
		if *body.Quantity < 1 {
			writeError(w, ErrInvalidQuantity, "")
			return
		}
		quantity = *body.Quantity
	}
	identifiers, assetAccount := body.Identifiers, body.AccountID
	current, _ := auth.UserFromContext(r.Context())
	if err := h.service.ClearPaid(r.Context(), d.ID, unit.ID, quantity, identifiers, current.ID, assetAccount); err != nil {
		writeError(w, err, "could not undo the payment")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("↩️ %s ha quitado la marca de pagado de una unidad de «%s» de %s.",
		h.actorLogin(r.Context()), d.Name, unit.BeneficiaryLogin))
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

// unitForStaff fetches a distribution and one of its units for a route whose
// role check already ran in RegisterRoutes.
func (h *Handler) unitForStaff(w http.ResponseWriter, r *http.Request) (Distribution, Unit, bool) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return Distribution{}, Unit{}, false
	}
	unitID, ok := parseID(w, r, "unitID")
	if !ok {
		return Distribution{}, Unit{}, false
	}
	d, ok := h.accessible(w, r, id)
	if !ok {
		return Distribution{}, Unit{}, false
	}
	unit, err := h.service.GetUnit(r.Context(), id, unitID)
	if err != nil {
		writeError(w, err, "could not fetch unit")
		return Distribution{}, Unit{}, false
	}
	return d, unit, true
}
