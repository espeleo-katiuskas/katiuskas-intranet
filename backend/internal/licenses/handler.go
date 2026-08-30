package licenses

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
	mux.Handle("GET /api/v1/users/{userID}/licenses", h.authenticator.Protect(h.listByUser))
	mux.Handle("POST /api/v1/users/{userID}/licenses", h.authenticator.Protect(h.create, "admin", "board"))
}

func (h *Handler) listByUser(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "user id must be numeric")
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	if !current.HasAnyRole("admin", "board") && current.ID != userID {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "members can only view their own license history")
		return
	}
	list, err := h.service.ListByUser(r.Context(), userID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list licenses")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "user id must be numeric")
		return
	}
	var body struct {
		Type       string `json:"type"`
		IssuedDate string `json:"issued_date"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if body.Type == "" {
		httpapi.WriteError(w, http.StatusBadRequest, "missing_fields", "type is required")
		return
	}
	issuedDate, err := time.Parse("2006-01-02", body.IssuedDate)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "issued_date must be YYYY-MM-DD")
		return
	}

	l, err := h.service.Create(r.Context(), CreateInput{
		UserID: userID, Type: body.Type, IssuedDate: issuedDate,
	})
	if errors.Is(err, ErrNotActiveMember) {
		httpapi.WriteError(w, http.StatusConflict, "not_active_member", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not create license")
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, l)
}
