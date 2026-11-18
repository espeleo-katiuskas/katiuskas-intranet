package areas

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
)

// UserLookup translates a person's numeric id into their login, for the notices.
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

// RegisterRoutes: everybody who can sign in reads the zones and adds one below another; adding a
// country (a zone below none), renaming, moving, merging and deleting them is for admin alone.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	a := h.authenticator
	mux.Handle("GET /api/v1/areas", a.Protect(h.list))
	mux.Handle("GET /api/v1/areas/locate", a.Protect(h.locate))
	mux.Handle("GET /api/v1/areas/geocode", a.Protect(h.geocode))
	mux.Handle("GET /api/v1/areas/{id}", a.Protect(h.get))
	mux.Handle("POST /api/v1/areas", a.Protect(h.create))
	mux.Handle("PUT /api/v1/areas/{id}", a.Protect(h.update, "admin"))
	mux.Handle("DELETE /api/v1/areas/{id}", a.Protect(h.delete, "admin"))
	mux.Handle("POST /api/v1/areas/{id}/merge", a.Protect(h.merge, "admin"))
}

func (h *Handler) actorLogin(ctx context.Context) string {
	current, _ := auth.UserFromContext(ctx)
	login, err := h.users.LoginForUserID(ctx, current.ID)
	if err != nil {
		return fmt.Sprintf("user#%d", current.ID)
	}
	return login
}

func writeError(w http.ResponseWriter, err error, fallback string) {
	type mapping struct {
		err    error
		status int
		code   string
	}
	for _, m := range []mapping{
		{ErrNotFound, http.StatusNotFound, "not_found"},
		{ErrUnknownParent, http.StatusBadRequest, "unknown_area"},
		{ErrRootAdminOnly, http.StatusForbidden, "area_root_admin_only"},
		{ErrInvalidPoint, http.StatusBadRequest, "invalid_coordinates"},
		{ErrLocateUnavailable, http.StatusBadGateway, "locate_unavailable"},
		{ErrSearchUnavailable, http.StatusBadGateway, "search_unavailable"},
		{ErrMoveCycle, http.StatusConflict, "area_move_cycle"},
		{ErrBlankName, http.StatusBadRequest, "area_name_required"},
		{ErrKindRequired, http.StatusBadRequest, "area_kind_required"},
		{ErrTooLong, http.StatusBadRequest, "area_text_too_long"},
		{ErrMergeSelf, http.StatusBadRequest, "area_merge_self"},
		{ErrDuplicate, http.StatusConflict, "duplicate_area"},
		{ErrHasChildren, http.StatusConflict, "area_has_children"},
		{ErrInUse, http.StatusConflict, "area_in_use"},
		{ErrMergeParents, http.StatusConflict, "area_merge_parents"},
	} {
		if errors.Is(err, m.err) {
			httpapi.WriteError(w, m.status, m.code, err.Error())
			return
		}
	}
	httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", fallback)
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return 0, false
	}
	return id, true
}

// list answers three questions: ?search= the zones whose name has the text, with their ancestors;
// ?parent_id= the zones directly below one; nothing, the countries.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	var list []Area
	var err error
	switch {
	case query.Has("search"):
		limit, _ := strconv.Atoi(query.Get("limit"))
		list, err = h.service.Search(r.Context(), query.Get("search"), limit)
	case query.Has("parent_id"):
		parent, parseErr := strconv.ParseInt(query.Get("parent_id"), 10, 64)
		if parseErr != nil {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "parent_id must be numeric")
			return
		}
		list, err = h.service.Children(r.Context(), &parent)
	default:
		list, err = h.service.Children(r.Context(), nil)
	}
	if err != nil {
		writeError(w, err, "could not list the zones")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

// locate proposes the zone a point (?lat=&lon=) is in, asking an outside source: {"area": Area | null}.
func (h *Handler) locate(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	lat, latErr := strconv.ParseFloat(query.Get("lat"), 64)
	lon, lonErr := strconv.ParseFloat(query.Get("lon"), 64)
	if latErr != nil || lonErr != nil {
		writeError(w, ErrInvalidPoint, "could not look up the zone")
		return
	}
	a, err := h.service.Locate(r.Context(), lat, lon)
	if err != nil {
		writeError(w, err, "could not look up the zone")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]*Area{"area": a})
}

// geocode finds the places a name (?q=) is, asking an outside source, to look for them on a map:
// [{name, lat, lon, bounds}].
func (h *Handler) geocode(w http.ResponseWriter, r *http.Request) {
	found, err := h.service.Geocode(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		writeError(w, err, "could not search for the place")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, found)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	a, err := h.service.Get(r.Context(), id)
	if err != nil {
		writeError(w, err, "could not read the zone")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, a)
}

// parentName is the name of the zone a zone is directly in, for a notice.
func sameParent(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func parentName(a Area) string {
	if len(a.Ancestors) == 0 {
		return ""
	}
	return a.Ancestors[len(a.Ancestors)-1].Name
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ParentID *int64 `json:"parent_id"`
		Name     string `json:"name"`
		Kind     string `json:"kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	// No parent (or 0) makes a country, which only an admin may.
	if body.ParentID != nil && *body.ParentID == 0 {
		body.ParentID = nil
	}
	if current, _ := auth.UserFromContext(r.Context()); body.ParentID == nil && !current.HasAnyRole("admin") {
		writeError(w, ErrRootAdminOnly, "could not add the zone")
		return
	}
	a, err := h.service.Create(r.Context(), body.ParentID, body.Name, body.Kind)
	if err != nil {
		writeError(w, err, "could not add the zone")
		return
	}
	if parent := parentName(a); parent == "" {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗺️ %s ha añadido la zona «%s» (%s), que no está dentro de ninguna otra.",
			h.actorLogin(r.Context()), a.Name, a.Kind))
	} else {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗺️ %s ha añadido la zona «%s» (%s) en «%s».",
			h.actorLogin(r.Context()), a.Name, a.Kind, parent))
	}
	httpapi.WriteJSON(w, http.StatusCreated, a)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	// parent_id is only looked at when it is in the body: null makes the zone a country.
	var body struct {
		Name     string          `json:"name"`
		Kind     string          `json:"kind"`
		ParentID json.RawMessage `json:"parent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	var move Move
	if len(body.ParentID) > 0 {
		move.Set = true
		var parent *int64
		if err := json.Unmarshal(body.ParentID, &parent); err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "parent_id must be a number or null")
			return
		}
		move.ID = parent
	}
	before, beforeErr := h.service.Get(r.Context(), id)
	a, err := h.service.Update(r.Context(), id, body.Name, body.Kind, move)
	if err != nil {
		writeError(w, err, "could not change the zone")
		return
	}
	if beforeErr == nil && (before.Name != a.Name || before.Kind != a.Kind) {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha cambiado la zona «%s» (%s) por «%s» (%s).",
			h.actorLogin(r.Context()), before.Name, before.Kind, a.Name, a.Kind))
	}
	if beforeErr == nil && !sameParent(before.ParentID, a.ParentID) {
		before, now := parentName(before), parentName(a)
		switch {
		case before == "":
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗺️ %s ha movido la zona «%s» (%s), que no estaba dentro de ninguna otra, a «%s».",
				h.actorLogin(r.Context()), a.Name, a.Kind, now))
		case now == "":
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗺️ %s ha sacado la zona «%s» (%s) de «%s»: ya no está dentro de ninguna otra.",
				h.actorLogin(r.Context()), a.Name, a.Kind, before))
		default:
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗺️ %s ha movido la zona «%s» (%s) de «%s» a «%s».",
				h.actorLogin(r.Context()), a.Name, a.Kind, before, now))
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, a)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	a, getErr := h.service.Get(r.Context(), id)
	if err := h.service.Delete(r.Context(), id); err != nil {
		writeError(w, err, "could not delete the zone")
		return
	}
	if getErr == nil {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha borrado la zona «%s» (%s).", h.actorLogin(r.Context()), a.Name, a.Kind))
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) merge(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		IntoID int64 `json:"into_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	from, fromErr := h.service.Get(r.Context(), id)
	into, intoErr := h.service.Get(r.Context(), body.IntoID)
	if err := h.service.Merge(r.Context(), id, body.IntoID); err != nil {
		writeError(w, err, "could not merge the zones")
		return
	}
	if fromErr == nil && intoErr == nil {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🔀 %s ha fusionado la zona «%s» en «%s» (%s).",
			h.actorLogin(r.Context()), from.Name, into.Name, into.Kind))
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]int64{"id": body.IntoID})
}
