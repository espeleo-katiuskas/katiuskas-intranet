package clubinfo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
)

// UserLookup is the one thing the handler needs from the users package, for the "who did this" of
// a notification.
type UserLookup interface {
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

// RegisterRoutes: anybody who can sign in reads the list (it is what the home page shows); only
// admin changes it.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/club-info", h.authenticator.Protect(h.list))
	mux.Handle("POST /api/v1/club-info", h.authenticator.Protect(h.create, "admin"))
	mux.Handle("PUT /api/v1/club-info/order", h.authenticator.Protect(h.reorder, "admin"))
	mux.Handle("PATCH /api/v1/club-info/{id}", h.authenticator.Protect(h.update, "admin"))
	mux.Handle("DELETE /api/v1/club-info/{id}", h.authenticator.Protect(h.delete, "admin"))
}

func (h *Handler) actorLogin(ctx context.Context) string {
	current, _ := auth.UserFromContext(ctx)
	login, err := h.users.LoginForUserID(ctx, current.ID)
	if err != nil {
		return fmt.Sprintf("user#%d", current.ID)
	}
	return login
}

// auditableFields are the Item fields an update's notification diffs — every one Input can change.
var auditableFields = []string{"Label", "Value", "CopyValue", "CopyLabel"}

type itemBody struct {
	Label     string  `json:"label"`
	Value     string  `json:"value"`
	CopyValue *string `json:"copy_value"`
	CopyLabel *string `json:"copy_label"`
}

func (b itemBody) input() Input {
	return Input(b)
}

func writeError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, ErrInvalidLabel):
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_label", err.Error())
	case errors.Is(err, ErrInvalidValue):
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_value", err.Error())
	case errors.Is(err, ErrInvalidCopy):
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_copy", err.Error())
	case errors.Is(err, ErrReorderMismatch):
		httpapi.WriteError(w, http.StatusConflict, "reorder_mismatch", err.Error())
	default:
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", fallback)
	}
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.List(r.Context())
	if err != nil {
		writeError(w, err, "could not list the club's data")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, items)
}

// The notifications say what changes, values included, like those of any other resource: the Telegram
// channel is private, and it already carries personal data.
func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var body itemBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	item, err := h.service.Create(r.Context(), body.input())
	if err != nil {
		writeError(w, err, "could not create the item")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🆕 %s ha añadido el dato de interés «%s»: %s.", h.actorLogin(r.Context()), item.Label, item.Value))
	httpapi.WriteJSON(w, http.StatusCreated, item)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var body itemBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	before, err := h.service.Get(r.Context(), id)
	if err != nil {
		writeError(w, err, "could not fetch the item")
		return
	}
	item, err := h.service.Update(r.Context(), id, body.input())
	if err != nil {
		writeError(w, err, "could not update the item")
		return
	}
	if diff := httpapi.DiffFields(before, item, auditableFields...); len(diff) > 0 {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha editado el dato de interés «%s»:\n%s",
			h.actorLogin(r.Context()), item.Label, strings.Join(diff, "\n")))
	}
	httpapi.WriteJSON(w, http.StatusOK, item)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	item, err := h.service.Get(r.Context(), id)
	if err != nil {
		writeError(w, err, "could not fetch the item")
		return
	}
	if err := h.service.Delete(r.Context(), id); err != nil {
		writeError(w, err, "could not delete the item")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha quitado el dato de interés «%s» (%s).", h.actorLogin(r.Context()), item.Label, item.Value))
	w.WriteHeader(http.StatusNoContent)
}

// reorder replaces the whole order from the ids of every item, and answers with the list in it.
func (h *Handler) reorder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ItemIDs []int64 `json:"item_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if err := h.service.Reorder(r.Context(), body.ItemIDs); err != nil {
		writeError(w, err, "could not reorder the items")
		return
	}
	items, err := h.service.List(r.Context())
	if err != nil {
		writeError(w, err, "could not list the club's data")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha reordenado los datos de interés.", h.actorLogin(r.Context())))
	httpapi.WriteJSON(w, http.StatusOK, items)
}

func parseID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return 0, false
	}
	return id, true
}
