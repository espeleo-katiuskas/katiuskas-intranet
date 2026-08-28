package inventory

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"katiuskas-intranet/backend/internal/auth"
	"katiuskas-intranet/backend/internal/httpapi"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Route("/items", func(r chi.Router) {
		r.Get("/", h.listItems)
		r.With(auth.RequireRole("admin", "board")).Post("/", h.createItem)
		r.Get("/{id}/transactions", h.listItemTransactions)
	})

	r.Route("/transactions", func(r chi.Router) {
		r.With(auth.RequireRole("admin", "board")).Get("/", h.listTransactions)
		r.With(auth.RequireRole("admin", "board")).Post("/", h.createTransaction)
		r.With(auth.RequireRole("admin", "board")).Patch("/{id}/return", h.returnTransaction)
	})
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
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
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

func (h *Handler) createTransaction(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ItemID   int64           `json:"item_id"`
		UserID   int64           `json:"user_id"`
		Type     TransactionType `json:"type"`
		Quantity int32           `json:"quantity"`
		DueDate  *string         `json:"due_date"`
		Price    *float64        `json:"price"`
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
		DueDate: dueDate, Price: body.Price, RecordedBy: &recordedBy,
	})
	if errors.Is(err, ErrInsufficientStock) {
		httpapi.WriteError(w, http.StatusConflict, "insufficient_stock", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not create transaction")
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, t)
}

func (h *Handler) returnTransaction(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	t, err := h.service.ReturnTransaction(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "active transaction not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not return transaction")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, t)
}
