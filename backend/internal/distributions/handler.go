package distributions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"Name", "Description", "Audience", "Status", "ClosesAt", "CutoffDate", "Price", "ExtraPrice", "MaxExtra", "Options",
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/distributions", h.authenticator.Protect(h.list))
	mux.Handle("POST /api/v1/distributions", h.authenticator.Protect(h.create, "admin", "board"))
	mux.Handle("GET /api/v1/distributions/{id}", h.authenticator.Protect(h.get))
	mux.Handle("PATCH /api/v1/distributions/{id}", h.authenticator.Protect(h.update, "admin", "board"))
	mux.Handle("DELETE /api/v1/distributions/{id}", h.authenticator.Protect(h.delete, "admin", "board"))

	mux.Handle("GET /api/v1/distributions/{id}/units", h.authenticator.Protect(h.listUnits))
	mux.Handle("POST /api/v1/distributions/{id}/units", h.authenticator.Protect(h.registerUnit))
	mux.Handle("PATCH /api/v1/distributions/{id}/units/{unitID}", h.authenticator.Protect(h.updateUnit))
	mux.Handle("DELETE /api/v1/distributions/{id}/units/{unitID}", h.authenticator.Protect(h.deleteUnit))

	mux.Handle("GET /api/v1/distributions/{id}/order", h.authenticator.Protect(h.order, "admin", "board", "material"))
	mux.Handle("POST /api/v1/distributions/{id}/pickups", h.authenticator.Protect(h.markPickedUp, "admin", "board", "material"))
	mux.Handle("DELETE /api/v1/distributions/{id}/units/{unitID}/pickup", h.authenticator.Protect(h.clearPickup, "admin", "board", "material"))
	mux.Handle("POST /api/v1/distributions/{id}/payments", h.authenticator.Protect(h.markPaid, "admin", "board"))
	mux.Handle("DELETE /api/v1/distributions/{id}/units/{unitID}/payment", h.authenticator.Protect(h.clearPaid, "admin", "board"))
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
		{ErrCutoffRequired, http.StatusBadRequest, "cutoff_required"},
		{ErrInvalidPrice, http.StatusBadRequest, "invalid_price"},
		{ErrInvalidMaxExtra, http.StatusBadRequest, "invalid_max_extra"},
		{ErrInvalidOptions, http.StatusBadRequest, "invalid_options"},
		{ErrInvalidStatus, http.StatusBadRequest, "invalid_status"},
		{ErrInvalidChoices, http.StatusBadRequest, "invalid_choices"},
		{ErrNoUnits, http.StatusBadRequest, "no_units"},
		{ErrUnknownUser, http.StatusBadRequest, "unknown_user"},
		{ErrInvalidTransition, http.StatusConflict, "invalid_status_transition"},
		{ErrHasUnits, http.StatusConflict, "has_units"},
		{ErrNotOpen, http.StatusConflict, "not_open"},
		{ErrNotEligible, http.StatusConflict, "not_eligible"},
		{ErrAlreadyRegistered, http.StatusConflict, "already_registered"},
		{ErrExtrasNotAllowed, http.StatusConflict, "extras_not_allowed"},
		{ErrTooManyExtras, http.StatusConflict, "too_many_extras"},
		{ErrUnitLocked, http.StatusConflict, "unit_locked"},
		{ErrNotReceived, http.StatusConflict, "not_received"},
		{ErrAlreadyPickedUp, http.StatusConflict, "already_picked_up"},
		{ErrAlreadyPaid, http.StatusConflict, "already_paid"},
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

// canSeeEveryone is who can read every unit of a distribution: the people who
// manage it and whoever hands it out.
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

type distributionBody struct {
	Name        string        `json:"name"`
	Description *string       `json:"description"`
	Audience    Audience      `json:"audience"`
	ClosesAt    *time.Time    `json:"closes_at"`
	CutoffDate  *string       `json:"cutoff_date"`
	Price       float64       `json:"price"`
	ExtraPrice  *float64      `json:"extra_price"`
	MaxExtra    *int          `json:"max_extra"`
	Options     *[]optionBody `json:"options"`
	Status      *Status       `json:"status"`
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

// visible fetches a distribution for a caller: drafts exist only for whoever
// manages them, and are a 404 for everybody else.
func (h *Handler) visible(w http.ResponseWriter, r *http.Request, id int64) (Distribution, bool) {
	current, _ := auth.UserFromContext(r.Context())
	d, err := h.service.Get(r.Context(), id)
	if err == nil && d.Status == StatusDraft && !isStaff(current) {
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
		CreatedBy: current.ID,
	}
	if body.Options != nil {
		in.Options = optionInputs(*body.Options)
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
	before, beforeErr := h.service.Get(r.Context(), id)
	in := UpdateInput{
		Name: body.Name, Description: body.Description, Audience: body.Audience, ClosesAt: body.ClosesAt,
		CutoffDate: cutoff, Price: body.Price, ExtraPrice: body.ExtraPrice, MaxExtra: body.MaxExtra,
		Status: body.Status,
	}
	if body.Options != nil {
		options := optionInputs(*body.Options)
		in.Options = &options
	}
	d, err := h.service.Update(r.Context(), id, in)
	if err != nil {
		writeError(w, err, "could not update distribution")
		return
	}
	if beforeErr == nil {
		if diff := httpapi.DiffFields(before, d, auditableFields...); len(diff) > 0 {
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha editado el reparto «%s»:\n%s",
				h.actorLogin(r.Context()), d.Name, strings.Join(diff, "\n")))
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, d)
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
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha borrado el reparto «%s».", h.actorLogin(r.Context()), d.Name))
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
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
		IsExtra: body.IsExtra, Choices: body.Choices, Staff: isStaff(current),
	})
	if err != nil {
		writeError(w, err, "could not register unit")
		return
	}
	what := "«" + d.Name + "»"
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

// ownUnit fetches a unit and checks the caller may change it: the person it
// is for, whoever entered it, or admin/board.
func (h *Handler) ownUnit(w http.ResponseWriter, r *http.Request) (Distribution, Unit, bool) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return Distribution{}, Unit{}, false
	}
	unitID, ok := parseID(w, r, "unitID")
	if !ok {
		return Distribution{}, Unit{}, false
	}
	d, ok := h.visible(w, r, id)
	if !ok {
		return Distribution{}, Unit{}, false
	}
	unit, err := h.service.GetUnit(r.Context(), id, unitID)
	if err != nil {
		writeError(w, err, "could not fetch unit")
		return Distribution{}, Unit{}, false
	}
	current, _ := auth.UserFromContext(r.Context())
	if !isStaff(current) && current.ID != unit.BeneficiaryID && current.ID != unit.RegisteredByID {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "only the person it is for, whoever entered it, board or admin can change this")
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
		Choices map[string]string `json:"choices"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	updated, err := h.service.UpdateChoices(r.Context(), d.ID, unit.ID, body.Choices, isStaff(current))
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
	if err := h.service.DeleteUnit(r.Context(), d.ID, unit.ID, isStaff(current)); err != nil {
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
	var body struct {
		UnitIDs         []int64 `json:"unit_ids"`
		PickedUpByLogin string  `json:"picked_up_by_login"`
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
	d, err := h.service.Get(r.Context(), id)
	if err != nil {
		writeError(w, err, "could not fetch distribution")
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	if err := h.service.MarkPickedUp(r.Context(), id, body.UnitIDs, current.ID, pickedUpBy); err != nil {
		writeError(w, err, "could not record the collection")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("📦 %s ha apuntado que %s ha recogido %d unidades de «%s».",
		h.actorLogin(r.Context()), body.PickedUpByLogin, len(body.UnitIDs), d.Name))
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

func (h *Handler) clearPickup(w http.ResponseWriter, r *http.Request) {
	d, unit, ok := h.unitForStaff(w, r)
	if !ok {
		return
	}
	if err := h.service.ClearPickup(r.Context(), d.ID, unit.ID); err != nil {
		writeError(w, err, "could not undo the collection")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("↩️ %s ha deshecho la recogida de una unidad de «%s» de %s.",
		h.actorLogin(r.Context()), d.Name, unit.BeneficiaryLogin))
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

func (h *Handler) markPaid(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		UnitIDs []int64 `json:"unit_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	d, err := h.service.Get(r.Context(), id)
	if err != nil {
		writeError(w, err, "could not fetch distribution")
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	if err := h.service.MarkPaid(r.Context(), id, body.UnitIDs, current.ID); err != nil {
		writeError(w, err, "could not mark as paid")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("💶 %s ha marcado como pagadas %d unidades de «%s».",
		h.actorLogin(r.Context()), len(body.UnitIDs), d.Name))
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

func (h *Handler) clearPaid(w http.ResponseWriter, r *http.Request) {
	d, unit, ok := h.unitForStaff(w, r)
	if !ok {
		return
	}
	if err := h.service.ClearPaid(r.Context(), d.ID, unit.ID); err != nil {
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
	d, err := h.service.Get(r.Context(), id)
	if err != nil {
		writeError(w, err, "could not fetch distribution")
		return Distribution{}, Unit{}, false
	}
	unit, err := h.service.GetUnit(r.Context(), id, unitID)
	if err != nil {
		writeError(w, err, "could not fetch unit")
		return Distribution{}, Unit{}, false
	}
	return d, unit, true
}
