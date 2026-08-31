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
// This package's own routes are nested under /users/{login}/transactions,
// but item_transactions is keyed by numeric user id — users.Service
// satisfies this structurally (see main.go's wiring), without inventory
// having to import that package (it can't: users already imports this
// one).
type UserLookup interface {
	UserIDForLogin(ctx context.Context, login string) (int64, error)
	LoginForUserID(ctx context.Context, id int64) (string, error)
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
	mux.Handle("GET /api/v1/items/{id}/transactions", h.authenticator.Protect(h.listItemTransactions))

	mux.Handle("GET /api/v1/transactions", h.authenticator.Protect(h.listTransactions, "admin", "board", "material"))
	mux.Handle("POST /api/v1/transactions", h.authenticator.Protect(h.createTransaction, "admin", "board", "material"))
	mux.Handle("PATCH /api/v1/transactions/{id}/return", h.authenticator.Protect(h.returnTransaction, "admin", "board", "material"))

	mux.Handle("GET /api/v1/users/{login}/transactions", h.authenticator.Protect(h.listTransactionsByUser))
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
	var in CreateItemInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if in.TotalQuantity <= 0 {
		in.TotalQuantity = 1
	}
	item, err := h.service.CreateItem(r.Context(), in)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not create item")
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, item)
}

func (h *Handler) listItemTransactions(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	list, err := h.service.ListTransactionsByItem(r.Context(), id)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list transactions")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) listTransactions(w http.ResponseWriter, r *http.Request) {
	status := TransactionStatus(r.URL.Query().Get("status"))
	if status == "" {
		status = TransactionActive
	}
	list, err := h.service.ListTransactionsByStatus(r.Context(), status)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list transactions")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

// listTransactionsByUser lets a person see their own material history, or
// admin/board/material see anyone's — the same self-or-those-roles check
// memberships.listByUser uses, since a transaction carries a price and is
// more sensitive than, say, activity participation.
func (h *Handler) listTransactionsByUser(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.resolveUserLogin(w, r)
	if !ok {
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	if !current.HasAnyRole("admin", "board", "material") && current.ID != userID {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "members can only view their own material history")
		return
	}
	list, err := h.service.ListTransactionsByUser(r.Context(), userID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list transactions")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) createTransaction(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ItemID     int64           `json:"item_id"`
		Login      string          `json:"login"`
		Type       TransactionType `json:"type"`
		Quantity   int32           `json:"quantity"`
		DueDate    *string         `json:"due_date"`
		Price      *float64        `json:"price"`
		ActivityID *int64          `json:"activity_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	userID, err := h.users.UserIDForLogin(r.Context(), body.Login)
	if err != nil {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	var dueDate *time.Time
	if body.DueDate != nil {
		d, err := time.Parse("2006-01-02", *body.DueDate)
		if err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "due_date must be YYYY-MM-DD")
			return
		}
		dueDate = &d
	}
	current, _ := auth.UserFromContext(r.Context())
	recordedBy := current.ID
	t, err := h.service.CreateTransaction(r.Context(), CreateTransactionInput{
		ItemID: body.ItemID, UserID: userID, Type: body.Type, Quantity: body.Quantity,
		DueDate: dueDate, Price: body.Price, RecordedBy: &recordedBy, ActivityID: body.ActivityID,
	})
	if errors.Is(err, ErrInsufficientStock) {
		httpapi.WriteError(w, http.StatusConflict, "insufficient_stock", err.Error())
		return
	}
	if errors.Is(err, ErrActivityRequired) {
		httpapi.WriteError(w, http.StatusBadRequest, "activity_required", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not create transaction")
		return
	}
	t.UserLogin = body.Login
	if recordedByLogin, err := h.users.LoginForUserID(r.Context(), recordedBy); err == nil {
		t.RecordedByLogin = &recordedByLogin
	}
	httpapi.WriteJSON(w, http.StatusCreated, t)
}

func (h *Handler) returnTransaction(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	t, err := h.service.ReturnTransaction(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "active transaction not found")
		return
	}
	if errors.Is(err, ErrCannotReturnSale) {
		httpapi.WriteError(w, http.StatusConflict, "cannot_return_sale", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not return transaction")
		return
	}
	login, err := h.users.LoginForUserID(r.Context(), t.UserID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not resolve transaction owner")
		return
	}
	t.UserLogin = login
	if t.RecordedBy != nil {
		if login, err := h.users.LoginForUserID(r.Context(), *t.RecordedBy); err == nil {
			t.RecordedByLogin = &login
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, t)
}
