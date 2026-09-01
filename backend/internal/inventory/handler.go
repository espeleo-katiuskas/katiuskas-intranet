package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
)

// UserLookup translates between a person's login and their numeric id.
// This package's own routes are nested under /users/{login}/movements,
// but item_movements is keyed by numeric user id — users.Service
// satisfies this structurally (see main.go's wiring), without inventory
// having to import that package (it can't: users already imports this
// one).
type UserLookup interface {
	UserIDForLogin(ctx context.Context, login string) (int64, error)
	LoginForUserID(ctx context.Context, id int64) (string, error)
}

// recipientRequiredTypes are the movement types that always exchange
// the item with a specific person and so must name one — see
// MovementType. Loan and sale's recipients are optional (neither
// required nor forbidden — see recipientForbiddenTypes below for the
// types that forbid one outright).
var recipientRequiredTypes = map[MovementType]bool{
	MovementRental: true,
}

// recipientForbiddenTypes are the movement types that never have a
// recipient at all: acquisition, loss, abandonment and retirement are
// stock-level changes, not exchanges with a person.
var recipientForbiddenTypes = map[MovementType]bool{
	MovementAcquisition: true,
	MovementLoss:        true,
	MovementAbandonment: true,
	MovementRetirement:  true,
}

var validMovementTypes = map[MovementType]bool{
	MovementLoan:        true,
	MovementRental:      true,
	MovementSale:        true,
	MovementAcquisition: true,
	MovementLoss:        true,
	MovementAbandonment: true,
	MovementRetirement:  true,
}

type Handler struct {
	service       *Service
	authenticator *auth.Authenticator
	users         UserLookup
}

func NewHandler(service *Service, authenticator *auth.Authenticator, users UserLookup) *Handler {
	return &Handler{service: service, authenticator: authenticator, users: users}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/items", h.authenticator.Protect(h.listItems))
	mux.Handle("POST /api/v1/items", h.authenticator.Protect(h.createItem, "admin", "board"))
	mux.Handle("GET /api/v1/items/{id}/movements", h.authenticator.Protect(h.listItemMovements))

	// material-categories is admin/board-only, matching create: the
	// catalog only exists to populate the "new item" form's category
	// picker for gear items.
	mux.Handle("GET /api/v1/material-categories", h.authenticator.Protect(h.listMaterialCategories, "admin", "board"))

	mux.Handle("GET /api/v1/movements", h.authenticator.Protect(h.listMovements, "admin", "board", "material"))
	mux.Handle("POST /api/v1/movements", h.authenticator.Protect(h.createMovement, "admin", "board", "material"))
	mux.Handle("PATCH /api/v1/movements/{id}/return", h.authenticator.Protect(h.returnMovement, "admin", "board", "material"))
	mux.Handle("GET /api/v1/movements/{id}/returns", h.authenticator.Protect(h.listMovementReturns))

	mux.Handle("GET /api/v1/users/{login}/movements", h.authenticator.Protect(h.listMovementsByUser))
}

// resolveUserLogin looks up the numeric id behind a {login} path
// segment. Any failure — malformed, or simply nobody by that login — is
// reported the same way (404): this package only needs to know whether
// the lookup succeeded, not why it didn't.
func (h *Handler) resolveUserLogin(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := h.users.UserIDForLogin(r.Context(), r.PathValue("login"))
	if err != nil {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "user not found")
		return 0, false
	}
	return id, true
}

func (h *Handler) listItems(w http.ResponseWriter, r *http.Request) {
	list, err := h.service.ListItems(r.Context())
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list items")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) createItem(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Type               ItemType  `json:"item_type"`
		Brand              *string   `json:"brand"`
		Model              *string   `json:"model"`
		Code               *string   `json:"code"`
		Description        *string   `json:"description"`
		AcquiredAt         *string   `json:"acquired_at"`
		TotalQuantity      int32     `json:"total_quantity"`
		Loanable           *bool     `json:"loanable"`
		RentalPricePerWeek *float64  `json:"rental_price_per_week"`
		SalePrice          *float64  `json:"sale_price"`
		RopeType           *RopeType `json:"rope_type"`
		DiameterMM         *float64  `json:"diameter_mm"`
		ManufactureYear    *int32    `json:"manufacture_year"`
		LengthM            *int32    `json:"length_m"`
		BagType            *BagType  `json:"bag_type"`
		CapacityLiters     *float64  `json:"capacity_liters"`
		CategoryID         *int64    `json:"category_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if !(body.Type == ItemTypeRope || body.Type == ItemTypeBag || body.Type == ItemTypeGear) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_item_type", "item_type must be rope, bag or gear")
		return
	}
	var acquiredAt *time.Time
	if body.AcquiredAt != nil && *body.AcquiredAt != "" {
		d, err := time.Parse("2006-01-02", *body.AcquiredAt)
		if err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "acquired_at must be YYYY-MM-DD")
			return
		}
		acquiredAt = &d
	}
	loanable := true
	if body.Loanable != nil {
		loanable = *body.Loanable
	}
	item, err := h.service.CreateItem(r.Context(), CreateItemInput{
		Type: body.Type, Brand: body.Brand, Model: body.Model, Code: body.Code,
		Description: body.Description, AcquiredAt: acquiredAt, InitialQuantity: body.TotalQuantity,
		Loanable: loanable, RentalPricePerWeek: body.RentalPricePerWeek, SalePrice: body.SalePrice,
		RopeType: body.RopeType, DiameterMM: body.DiameterMM, ManufactureYear: body.ManufactureYear,
		LengthM: body.LengthM, BagType: body.BagType, CapacityLiters: body.CapacityLiters, CategoryID: body.CategoryID,
	})
	if errors.Is(err, ErrCategoryNotFound) {
		httpapi.WriteError(w, http.StatusBadRequest, "unknown_category", err.Error())
		return
	}
	if errors.Is(err, ErrSingleUnitItem) {
		httpapi.WriteError(w, http.StatusBadRequest, "single_unit_item", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not create item")
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, item)
}

func (h *Handler) listMaterialCategories(w http.ResponseWriter, r *http.Request) {
	list, err := h.service.ListMaterialCategories(r.Context())
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list material categories")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) listItemMovements(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	list, err := h.service.ListMovementsByItem(r.Context(), id)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list movements")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) listMovements(w http.ResponseWriter, r *http.Request) {
	status := MovementStatus(r.URL.Query().Get("status"))
	if status == "" {
		status = MovementActive
	}
	list, err := h.service.ListMovementsByStatus(r.Context(), status)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list movements")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

// listMovementsByUser lets a person see their own material history, or
// admin/board/material see anyone's — the same self-or-those-roles check
// memberships.listByUser uses, since a movement can carry a price and is
// more sensitive than, say, activity participation.
func (h *Handler) listMovementsByUser(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.resolveUserLogin(w, r)
	if !ok {
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	if !current.HasAnyRole("admin", "board", "material") && current.ID != userID {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "members can only view their own material history")
		return
	}
	list, err := h.service.ListMovementsByUser(r.Context(), userID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list movements")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) createMovement(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ItemID       int64        `json:"item_id"`
		Login        string       `json:"login"`
		Type         MovementType `json:"type"`
		Quantity     int32        `json:"quantity"`
		TransactedAt *string      `json:"transacted_at"`
		ActivityID   *int64       `json:"activity_id"`
		Notes        *string      `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if !validMovementTypes[body.Type] {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_type", "unrecognized movement type")
		return
	}
	if recipientRequiredTypes[body.Type] && body.Login == "" {
		httpapi.WriteError(w, http.StatusBadRequest, "login_required", "rental movements require a login")
		return
	}
	if recipientForbiddenTypes[body.Type] && body.Login != "" {
		httpapi.WriteError(w, http.StatusBadRequest, "recipient_not_allowed", "acquisition, loss, abandonment and retirement movements have no recipient")
		return
	}
	var userID *int64
	if body.Login != "" {
		id, err := h.users.UserIDForLogin(r.Context(), body.Login)
		if err != nil {
			httpapi.WriteError(w, http.StatusNotFound, "not_found", "user not found")
			return
		}
		userID = &id
	}
	transactedAt := time.Now()
	if body.TransactedAt != nil && *body.TransactedAt != "" {
		d, err := time.Parse("2006-01-02", *body.TransactedAt)
		if err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "transacted_at must be YYYY-MM-DD")
			return
		}
		transactedAt = d
	}
	current, _ := auth.UserFromContext(r.Context())
	recordedBy := current.ID
	m, err := h.service.CreateMovement(r.Context(), CreateMovementInput{
		ItemID: body.ItemID, UserID: userID, Type: body.Type, Quantity: body.Quantity,
		TransactedAt: transactedAt, RecordedBy: &recordedBy, ActivityID: body.ActivityID, Notes: body.Notes,
	})
	if errors.Is(err, ErrInsufficientStock) {
		httpapi.WriteError(w, http.StatusConflict, "insufficient_stock", err.Error())
		return
	}
	if errors.Is(err, ErrActivityRequired) {
		httpapi.WriteError(w, http.StatusBadRequest, "activity_required", err.Error())
		return
	}
	if errors.Is(err, ErrNotLoanable) {
		httpapi.WriteError(w, http.StatusConflict, "not_loanable", err.Error())
		return
	}
	if errors.Is(err, ErrSingleUnitItem) {
		httpapi.WriteError(w, http.StatusConflict, "single_unit_item", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not create movement")
		return
	}
	if body.Login != "" {
		m.UserLogin = &body.Login
	}
	if recordedByLogin, err := h.users.LoginForUserID(r.Context(), recordedBy); err == nil {
		m.RecordedByLogin = &recordedByLogin
	}
	httpapi.WriteJSON(w, http.StatusCreated, m)
}

// returnMovement records one return event, defaulting to a full return
// (whatever's still outstanding) when the body is empty or omits
// quantity — the same one-call behavior this endpoint always had.
// Giving a smaller quantity is a partial return: 30 mosquetones lent
// for a trip can come back as 10 one day and 20 the next, each its own
// call here, leaving the movement active until the last one closes it.
func (h *Handler) returnMovement(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	var body struct {
		Quantity   *int32  `json:"quantity"`
		ReturnedAt *string `json:"returned_at"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
			return
		}
	}
	returnedAt := time.Now()
	if body.ReturnedAt != nil && *body.ReturnedAt != "" {
		d, err := time.Parse("2006-01-02", *body.ReturnedAt)
		if err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "returned_at must be YYYY-MM-DD")
			return
		}
		returnedAt = d
	}
	current, _ := auth.UserFromContext(r.Context())
	recordedBy := current.ID
	m, err := h.service.ReturnMovement(r.Context(), id, body.Quantity, returnedAt, &recordedBy)
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "active movement not found")
		return
	}
	if errors.Is(err, ErrInvalidReturnQuantity) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_quantity", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not return movement")
		return
	}
	if m.UserID != nil {
		if login, err := h.users.LoginForUserID(r.Context(), *m.UserID); err == nil {
			m.UserLogin = &login
		}
	}
	if recordedByLogin, err := h.users.LoginForUserID(r.Context(), recordedBy); err == nil {
		m.RecordedByLogin = &recordedByLogin
	}
	httpapi.WriteJSON(w, http.StatusOK, m)
}

// listMovementReturns backs the return history shown alongside a
// movement's ReturnedQuantity — open to any baseline role, matching
// GET /items/{id}/movements. Repository.ListMovementReturns already
// joins in recorded_by_login, unlike the single-row RETURNING writes
// above that have to resolve it afterward.
func (h *Handler) listMovementReturns(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	list, err := h.service.ListMovementReturns(r.Context(), id)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list returns")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}
