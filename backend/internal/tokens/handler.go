package tokens

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
	service               *Service
	authenticator         *auth.Authenticator
	impersonationTokenTTL time.Duration
}

func NewHandler(service *Service, authenticator *auth.Authenticator, impersonationTokenTTL time.Duration) *Handler {
	return &Handler{service: service, authenticator: authenticator, impersonationTokenTTL: impersonationTokenTTL}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/users/{userID}/tokens", h.authenticator.Protect(h.list, "admin"))
	mux.Handle("PATCH /api/v1/users/{userID}/tokens/{tokenID}", h.authenticator.Protect(h.updateExpiry, "admin"))
	mux.Handle("DELETE /api/v1/users/{userID}/tokens/{tokenID}", h.authenticator.Protect(h.revoke, "admin"))
	mux.Handle("POST /api/v1/users/{userID}/impersonate", h.authenticator.Protect(h.impersonate, "admin"))
}

// impersonate is admin-only, deliberately not "admin"/"board": handing
// board members the ability to act as anyone (including other admins)
// is a bigger step than the rest of what they can already do.
func (h *Handler) impersonate(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "user id must be numeric")
		return
	}
	current, _ := auth.UserFromContext(r.Context())

	raw, err := h.service.Impersonate(r.Context(), current.ID, userID, h.impersonationTokenTTL)
	if errors.Is(err, ErrCannotImpersonateSelf) {
		httpapi.WriteError(w, http.StatusConflict, "cannot_impersonate_self", err.Error())
		return
	}
	if errors.Is(err, ErrNotEligibleForImpersonation) {
		httpapi.WriteError(w, http.StatusConflict, "not_eligible", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not start impersonation")
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, map[string]string{"token": raw})
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "user id must be numeric")
		return
	}
	list, err := h.service.ListForUser(r.Context(), userID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list tokens")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) updateExpiry(w http.ResponseWriter, r *http.Request) {
	userID, tokenID, ok := h.pathIDs(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if body.ExpiresAt.IsZero() {
		httpapi.WriteError(w, http.StatusBadRequest, "missing_fields", "expires_at is required")
		return
	}

	err := h.service.UpdateExpiry(r.Context(), userID, tokenID, body.ExpiresAt)
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrWrongUser) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "token not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not update token")
		return
	}
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

func (h *Handler) revoke(w http.ResponseWriter, r *http.Request) {
	userID, tokenID, ok := h.pathIDs(w, r)
	if !ok {
		return
	}
	err := h.service.Revoke(r.Context(), userID, tokenID)
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrWrongUser) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "token not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not revoke token")
		return
	}
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

func (h *Handler) pathIDs(w http.ResponseWriter, r *http.Request) (userID, tokenID int64, ok bool) {
	userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "user id must be numeric")
		return 0, 0, false
	}
	tokenID, err = strconv.ParseInt(r.PathValue("tokenID"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "token id must be numeric")
		return 0, 0, false
	}
	return userID, tokenID, true
}
