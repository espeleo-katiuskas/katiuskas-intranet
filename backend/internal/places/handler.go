package places

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

// UserLookup translates a person's numeric id into their login: the API names people by login only.
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

// RegisterRoutes: everybody who can sign in reads the places and creates and changes them and their
// points (the API says who changed what and when); deleting a place is for admin and board; the types
// of place and the geologies of the caves are managed by admin alone.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	a := h.authenticator
	mux.Handle("GET /api/v1/places/types", a.Protect(h.listTypes))
	mux.Handle("POST /api/v1/places/types", a.Protect(h.createType, "admin"))
	mux.Handle("PUT /api/v1/places/types/{id}", a.Protect(h.renameType, "admin"))
	mux.Handle("DELETE /api/v1/places/types/{id}", a.Protect(h.deleteType, "admin"))

	mux.Handle("GET /api/v1/places/geologies", a.Protect(h.listGeologies))
	mux.Handle("POST /api/v1/places/geologies", a.Protect(h.createGeology, "admin"))
	mux.Handle("PUT /api/v1/places/geologies/{id}", a.Protect(h.renameGeology, "admin"))
	mux.Handle("DELETE /api/v1/places/geologies/{id}", a.Protect(h.deleteGeology, "admin"))

	mux.Handle("GET /api/v1/places", a.Protect(h.list))
	mux.Handle("POST /api/v1/places", a.Protect(h.create))
	mux.Handle("GET /api/v1/places/{id}", a.Protect(h.get))
	mux.Handle("PUT /api/v1/places/{id}", a.Protect(h.update))
	mux.Handle("DELETE /api/v1/places/{id}", a.Protect(h.delete, "admin", "board"))

	mux.Handle("POST /api/v1/places/{id}/points", a.Protect(h.addPoint))
	mux.Handle("PUT /api/v1/places/{id}/points/{pointId}", a.Protect(h.updatePoint))
	mux.Handle("DELETE /api/v1/places/{id}/points/{pointId}", a.Protect(h.deletePoint))
}

func (h *Handler) actorLogin(ctx context.Context) string {
	current, _ := auth.UserFromContext(ctx)
	login, err := h.users.LoginForUserID(ctx, current.ID)
	if err != nil {
		return fmt.Sprintf("user#%d", current.ID)
	}
	return login
}

func actorID(ctx context.Context) int64 {
	current, _ := auth.UserFromContext(ctx)
	return current.ID
}

func writeError(w http.ResponseWriter, err error, fallback string) {
	type mapping struct {
		err    error
		status int
		code   string
	}
	for _, m := range []mapping{
		{ErrNotFound, http.StatusNotFound, "not_found"},
		{ErrPointNotFound, http.StatusNotFound, "not_found"},
		{ErrTypeNotFound, http.StatusNotFound, "not_found"},
		{ErrUnknownType, http.StatusBadRequest, "unknown_place_type"},
		{ErrTypeRequired, http.StatusBadRequest, "place_type_required"},
		{ErrAreaRequired, http.StatusBadRequest, "place_area_required"},
		{ErrUnknownArea, http.StatusBadRequest, "unknown_area"},
		{ErrBlankName, http.StatusBadRequest, "place_name_required"},
		{ErrGeologyNotFound, http.StatusNotFound, "not_found"},
		{ErrUnknownGeology, http.StatusBadRequest, "unknown_geology"},
		{ErrBlankGeologyName, http.StatusBadRequest, "geology_name_required"},
		{ErrDuplicateGeology, http.StatusConflict, "duplicate_geology"},
		{ErrGeologyInUse, http.StatusConflict, "geology_in_use"},
		{ErrInvalidState, http.StatusBadRequest, "invalid_place_state"},
		{ErrInvalidCoords, http.StatusBadRequest, "invalid_coordinates"},
		{ErrInvalidAltitude, http.StatusBadRequest, "invalid_altitude"},
		{ErrInvalidMeasure, http.StatusBadRequest, "invalid_place_measure"},
		{ErrFieldsForKind, http.StatusBadRequest, "invalid_place_fields"},
		{ErrInvalidPointKind, http.StatusBadRequest, "invalid_point_kind"},
		{ErrBlankTypeName, http.StatusBadRequest, "place_type_name_required"},
		{ErrDuplicateType, http.StatusConflict, "duplicate_place_type"},
		{ErrTypeInUse, http.StatusConflict, "place_type_in_use"},
		{ErrFixedType, http.StatusConflict, "fixed_place_type"},
	} {
		if errors.Is(err, m.err) {
			httpapi.WriteError(w, m.status, m.code, err.Error())
			return
		}
	}
	httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", fallback)
}

// pathID reads a numeric path value, answering 400 itself if it is not one.
func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return 0, false
	}
	return id, true
}

// ----- types -----

type typeBody struct {
	Name string `json:"name"`
}

func (h *Handler) listTypes(w http.ResponseWriter, r *http.Request) {
	list, err := h.service.ListTypes(r.Context())
	if err != nil {
		writeError(w, err, "could not list the types")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) createType(w http.ResponseWriter, r *http.Request) {
	var body typeBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	t, err := h.service.CreateType(r.Context(), body.Name)
	if err != nil {
		writeError(w, err, "could not create the type")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🏷️ %s ha añadido el tipo de lugar «%s».", h.actorLogin(r.Context()), t.Name))
	httpapi.WriteJSON(w, http.StatusCreated, t)
}

func (h *Handler) renameType(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var body typeBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	before, beforeErr := h.service.GetType(r.Context(), id)
	t, err := h.service.RenameType(r.Context(), id, body.Name)
	if err != nil {
		writeError(w, err, "could not rename the type")
		return
	}
	if beforeErr == nil && before.Name != t.Name {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha cambiado el tipo de lugar «%s» por «%s».",
			h.actorLogin(r.Context()), before.Name, t.Name))
	}
	httpapi.WriteJSON(w, http.StatusOK, t)
}

func (h *Handler) deleteType(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	t, getErr := h.service.GetType(r.Context(), id)
	if err := h.service.DeleteType(r.Context(), id); err != nil {
		writeError(w, err, "could not delete the type")
		return
	}
	if getErr == nil {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha borrado el tipo de lugar «%s».", h.actorLogin(r.Context()), t.Name))
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- geologies -----

func (h *Handler) listGeologies(w http.ResponseWriter, r *http.Request) {
	list, err := h.service.ListGeologies(r.Context())
	if err != nil {
		writeError(w, err, "could not list the geologies")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) createGeology(w http.ResponseWriter, r *http.Request) {
	var body typeBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	g, err := h.service.CreateGeology(r.Context(), body.Name)
	if err != nil {
		writeError(w, err, "could not create the geology")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🪨 %s ha añadido la geología «%s» a los lugares.", h.actorLogin(r.Context()), g.Name))
	httpapi.WriteJSON(w, http.StatusCreated, g)
}

func (h *Handler) renameGeology(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var body typeBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	before, beforeErr := h.service.GetGeology(r.Context(), id)
	g, err := h.service.RenameGeology(r.Context(), id, body.Name)
	if err != nil {
		writeError(w, err, "could not rename the geology")
		return
	}
	if beforeErr == nil && before.Name != g.Name {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha cambiado la geología «%s» por «%s».",
			h.actorLogin(r.Context()), before.Name, g.Name))
	}
	httpapi.WriteJSON(w, http.StatusOK, g)
}

func (h *Handler) deleteGeology(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	g, getErr := h.service.GetGeology(r.Context(), id)
	if err := h.service.DeleteGeology(r.Context(), id); err != nil {
		writeError(w, err, "could not delete the geology")
		return
	}
	if getErr == nil {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha borrado la geología «%s».", h.actorLogin(r.Context()), g.Name))
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- places -----

// placeBody is what creating or editing a place takes.
type placeBody struct {
	TypeID            int64    `json:"type_id"`
	Name              string   `json:"name"`
	AltNames          *string  `json:"alt_names"`
	State             string   `json:"state"`
	AreaID            int64    `json:"area_id"`
	Lat               *float64 `json:"lat"`
	Lon               *float64 `json:"lon"`
	AltitudeM         *int     `json:"altitude_m"`
	Access            *string  `json:"access"`
	Description       *string  `json:"description"`
	DepthM            *int     `json:"depth_m"`
	HorizontalLengthM *int     `json:"horizontal_length_m"`
	GeologyID         *int64   `json:"geology_id"`
	TechnicalSheet    *string  `json:"technical_sheet"`
}

func (b placeBody) input() Input { return Input(b) }

// placeAuditFields are the Place fields an edit's notification diffs: those Input can change (the type
// by its name).
var placeAuditFields = []string{
	"TypeName", "Name", "AltNames", "State", "Where", "Lat", "Lon", "AltitudeM", "Access",
	"Description", "DepthM", "HorizontalLengthM", "Geology", "TechnicalSheet",
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.service.ListPlaces(r.Context())
	if err != nil {
		writeError(w, err, "could not list the places")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	p, err := h.service.GetPlace(r.Context(), id)
	if err != nil {
		writeError(w, err, "could not read the place")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var body placeBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	p, err := h.service.CreatePlace(r.Context(), actorID(r.Context()), body.input())
	if err != nil {
		writeError(w, err, "could not create the place")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("📍 %s ha añadido el lugar «%s» (%s).", h.actorLogin(r.Context()), p.Name, p.TypeName))
	httpapi.WriteJSON(w, http.StatusCreated, p)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var body placeBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	before, beforeErr := h.service.GetPlace(r.Context(), id)
	p, err := h.service.UpdatePlace(r.Context(), id, actorID(r.Context()), body.input())
	if err != nil {
		writeError(w, err, "could not edit the place")
		return
	}
	if beforeErr == nil {
		if diff := httpapi.DiffFields(before, p, placeAuditFields...); len(diff) > 0 {
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha editado el lugar «%s»:\n%s",
				h.actorLogin(r.Context()), before.Name, strings.Join(diff, "\n")))
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	// Read before deleting, so the notice can still name it.
	p, getErr := h.service.GetPlace(r.Context(), id)
	if err := h.service.DeletePlace(r.Context(), id); err != nil {
		writeError(w, err, "could not delete the place")
		return
	}
	if getErr == nil {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha borrado el lugar «%s».", h.actorLogin(r.Context()), p.Name))
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- points -----

type pointBody struct {
	Kind      string   `json:"kind"`
	Name      *string  `json:"name"`
	Lat       *float64 `json:"lat"`
	Lon       *float64 `json:"lon"`
	AltitudeM *int     `json:"altitude_m"`
	Note      *string  `json:"note"`
}

func (b pointBody) input() PointInput { return PointInput(b) }

// pointLabel names a point in a notice: its name, or what kind of point it is.
func pointLabel(p Point) string {
	if p.Name != nil {
		return *p.Name
	}
	return string(p.Kind)
}

func (h *Handler) addPoint(w http.ResponseWriter, r *http.Request) {
	placeID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var body pointBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	pt, err := h.service.AddPoint(r.Context(), placeID, actorID(r.Context()), body.input())
	if err != nil {
		writeError(w, err, "could not add the point")
		return
	}
	if p, err := h.service.GetPlace(r.Context(), placeID); err == nil {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("📍 %s ha añadido el punto «%s» (%s) al lugar «%s».",
			h.actorLogin(r.Context()), pointLabel(pt), pt.Kind, p.Name))
	}
	httpapi.WriteJSON(w, http.StatusCreated, pt)
}

func (h *Handler) updatePoint(w http.ResponseWriter, r *http.Request) {
	placeID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	id, ok := pathID(w, r, "pointId")
	if !ok {
		return
	}
	var body pointBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	pt, err := h.service.UpdatePoint(r.Context(), placeID, id, actorID(r.Context()), body.input())
	if err != nil {
		writeError(w, err, "could not edit the point")
		return
	}
	if p, err := h.service.GetPlace(r.Context(), placeID); err == nil {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha cambiado el punto «%s» (%s) del lugar «%s».",
			h.actorLogin(r.Context()), pointLabel(pt), pt.Kind, p.Name))
	}
	httpapi.WriteJSON(w, http.StatusOK, pt)
}

func (h *Handler) deletePoint(w http.ResponseWriter, r *http.Request) {
	placeID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	id, ok := pathID(w, r, "pointId")
	if !ok {
		return
	}
	// Read before deleting, so the notice can still name it.
	pt, getErr := h.service.GetPoint(r.Context(), placeID, id)
	if err := h.service.DeletePoint(r.Context(), placeID, id, actorID(r.Context())); err != nil {
		writeError(w, err, "could not delete the point")
		return
	}
	if p, err := h.service.GetPlace(r.Context(), placeID); err == nil && getErr == nil {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha quitado el punto «%s» (%s) del lugar «%s».",
			h.actorLogin(r.Context()), pointLabel(pt), pt.Kind, p.Name))
	}
	w.WriteHeader(http.StatusNoContent)
}
