package inventory

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"katiuskas-intranet/backend/internal/auth"
	"katiuskas-intranet/backend/internal/httpapi"
)

type Handler struct {
	service       *Service
	authenticator *auth.Authenticator
}

func NewHandler(service *Service, authenticator *auth.Authenticator) *Handler {
	return &Handler{service: service, authenticator: authenticator}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/items", h.authenticator.Protect(h.listItems))
	mux.Handle("POST /api/v1/items", h.authenticator.Protect(h.createItem, "admin", "board"))
	mux.Handle("GET /api/v1/items/{id}/transactions", h.authenticator.Protect(h.listItemTransactions))

	mux.Handle("GET /api/v1/transactions", h.authenticator.Protect(h.listTransactions, "admin", "board", "material"))
	mux.Handle("POST /api/v1/transactions", h.authenticator.Protect(h.createTransaction, "admin", "board", "material"))
	mux.Handle("PATCH /api/v1/transactions/{id}/return", h.authenticator.Protect(h.returnTransaction, "admin", "board", "material"))

	mux.Handle("GET /api/v1/users/{userID}/transactions", h.authenticator.Protect(h.listTransactionsByUser))
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
	userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "user id must be numeric")
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
		UserID     int64           `json:"user_id"`
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
		ItemID: body.ItemID, UserID: body.UserID, Type: body.Type, Quantity: body.Quantity,
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
	httpapi.WriteJSON(w, http.StatusOK, t)
}
