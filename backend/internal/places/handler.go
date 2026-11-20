package places

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
)

// UserLookup translates a person's numeric id into their login: the API names people by login only.
type UserLookup interface {
	LoginForUserID(ctx context.Context, id int64) (string, error)
	UserIDForLogin(ctx context.Context, login string) (int64, error)
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
// points and attach files (the API says who changed what and when); a file is removed by whoever
// attached it, admin or board; deleting a place is for admin and board; the types of place and the
// geologies of the caves are managed by admin alone. The bytes of a file also accept the session
// cookie, so that an image can be shown by the page.
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

	mux.Handle("GET /api/v1/places/subtypes", a.Protect(h.listSubtypes))
	mux.Handle("POST /api/v1/places/subtypes", a.Protect(h.createSubtype, "admin"))
	mux.Handle("PUT /api/v1/places/subtypes/{id}", a.Protect(h.renameSubtype, "admin"))
	mux.Handle("DELETE /api/v1/places/subtypes/{id}", a.Protect(h.deleteSubtype, "admin"))

	mux.Handle("GET /api/v1/places", a.Protect(h.list))
	mux.Handle("GET /api/v1/users/{login}/places", a.Protect(h.visitedBy))
	mux.Handle("POST /api/v1/places", a.Protect(h.create))
	mux.Handle("GET /api/v1/places/{id}", a.Protect(h.get))
	mux.Handle("PUT /api/v1/places/{id}", a.Protect(h.update))
	mux.Handle("PATCH /api/v1/places/{id}", a.Protect(h.patch))
	mux.Handle("DELETE /api/v1/places/{id}", a.Protect(h.delete, "admin", "board"))
	mux.Handle("PUT /api/v1/places/{id}/links/osm", a.Protect(h.linkOsm))
	mux.Handle("DELETE /api/v1/places/{id}/links/osm", a.Protect(h.unlinkOsm))
	mux.Handle("POST /api/v1/places/{id}/links/osm/review", a.Protect(h.reviewOsm))

	mux.Handle("POST /api/v1/places/{id}/points", a.Protect(h.addPoint))
	mux.Handle("PUT /api/v1/places/{id}/points/{pointId}", a.Protect(h.updatePoint))
	mux.Handle("DELETE /api/v1/places/{id}/points/{pointId}", a.Protect(h.deletePoint))

	mux.Handle("GET /api/v1/places/{id}/files", a.Protect(h.listFiles(placeOwner)))
	mux.Handle("POST /api/v1/places/{id}/files", a.Protect(h.addFile(placeOwner)))
	mux.Handle("GET /api/v1/places/{id}/files/{fileId}/file", a.ProtectAllowingCookie(h.file(placeOwner)))
	mux.Handle("DELETE /api/v1/places/{id}/files/{fileId}", a.Protect(h.deleteFile(placeOwner)))

	mux.Handle("GET /api/v1/systems", a.Protect(h.listSystems))
	mux.Handle("POST /api/v1/systems", a.Protect(h.createSystem))
	mux.Handle("GET /api/v1/systems/{id}", a.Protect(h.getSystem))
	mux.Handle("PUT /api/v1/systems/{id}", a.Protect(h.updateSystem))
	mux.Handle("DELETE /api/v1/systems/{id}", a.Protect(h.deleteSystem, "admin", "board"))
	mux.Handle("PUT /api/v1/systems/{id}/places", a.Protect(h.setSystemPlaces))
	mux.Handle("GET /api/v1/systems/{id}/files", a.Protect(h.listFiles(systemOwner)))
	mux.Handle("POST /api/v1/systems/{id}/files", a.Protect(h.addFile(systemOwner)))
	mux.Handle("GET /api/v1/systems/{id}/files/{fileId}/file", a.ProtectAllowingCookie(h.file(systemOwner)))
	mux.Handle("DELETE /api/v1/systems/{id}/files/{fileId}", a.Protect(h.deleteFile(systemOwner)))
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
		{ErrConcurrentUpdate, http.StatusConflict, "concurrent_update"},
		{ErrPointNotFound, http.StatusNotFound, "not_found"},
		{ErrFileNotFound, http.StatusNotFound, "not_found"},
		{ErrEmptyFile, http.StatusBadRequest, "file_required"},
		{ErrTitleTooLong, http.StatusBadRequest, "file_title_too_long"},
		{ErrNotYours, http.StatusForbidden, "place_file_not_yours"},
		{ErrTypeNotFound, http.StatusNotFound, "not_found"},
		{ErrUnknownType, http.StatusBadRequest, "unknown_place_type"},
		{ErrUnknownOsmElement, http.StatusBadRequest, "unknown_osm_element"},
		{ErrOsmElementInUse, http.StatusConflict, "osm_element_in_use"},
		{ErrNotLinked, http.StatusConflict, "place_not_linked"},
		{ErrTypeRequired, http.StatusBadRequest, "place_type_required"},
		{ErrAreaRequired, http.StatusBadRequest, "place_area_required"},
		{ErrUnknownArea, http.StatusBadRequest, "unknown_area"},
		{ErrBlankName, http.StatusBadRequest, "place_name_required"},
		{ErrGeologyNotFound, http.StatusNotFound, "not_found"},
		{ErrUnknownGeology, http.StatusBadRequest, "unknown_geology"},
		{ErrBlankGeologyName, http.StatusBadRequest, "geology_name_required"},
		{ErrDuplicateGeology, http.StatusConflict, "duplicate_geology"},
		{ErrGeologyInUse, http.StatusConflict, "geology_in_use"},
		{ErrSubtypeNotFound, http.StatusNotFound, "not_found"},
		{ErrUnknownSubtype, http.StatusBadRequest, "unknown_subtype"},
		{ErrSubtypeRequired, http.StatusBadRequest, "place_subtype_required"},
		{ErrBlankSubtypeName, http.StatusBadRequest, "subtype_name_required"},
		{ErrDuplicateSubtype, http.StatusConflict, "duplicate_subtype"},
		{ErrSubtypeInUse, http.StatusConflict, "subtype_in_use"},
		{ErrInvalidAltNames, http.StatusBadRequest, "invalid_alt_names"},
		{ErrSystemNotFound, http.StatusNotFound, "not_found"},
		{ErrBlankSystemName, http.StatusBadRequest, "system_name_required"},
		{ErrSystemNameLength, http.StatusBadRequest, "system_name_too_long"},
		{ErrDuplicateSystem, http.StatusConflict, "duplicate_system"},
		{ErrUnknownSystem, http.StatusBadRequest, "unknown_system"},
		{ErrNotACavity, http.StatusBadRequest, "not_a_cavity"},
		{ErrUnknownPlace, http.StatusBadRequest, "unknown_place"},
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
		{ErrHasActivities, http.StatusConflict, "place_has_activities"},
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

// visitedBy gives the places a person has been to: those of the activities they took part in that have
// started and are not cancelled.
func (h *Handler) visitedBy(w http.ResponseWriter, r *http.Request) {
	userID, err := h.users.UserIDForLogin(r.Context(), r.PathValue("login"))
	if err != nil {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	list, err := h.service.VisitedBy(r.Context(), userID)
	if err != nil {
		writeError(w, err, "could not list the places visited")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

// ----- types -----

type typeBody struct {
	Name string `json:"name"`
	// HasState is for a new type only: whether its places have a state. It does not change afterwards.
	HasState bool `json:"has_state"`
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
	t, err := h.service.CreateType(r.Context(), body.Name, body.HasState)
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

// ----- subtypes -----

func (h *Handler) listSubtypes(w http.ResponseWriter, r *http.Request) {
	list, err := h.service.ListSubtypes(r.Context())
	if err != nil {
		writeError(w, err, "could not list the subtypes")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) createSubtype(w http.ResponseWriter, r *http.Request) {
	var body typeBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	g, err := h.service.CreateSubtype(r.Context(), body.Name)
	if err != nil {
		writeError(w, err, "could not create the subtype")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🧭 %s ha añadido el subtipo de cavidad «%s».", h.actorLogin(r.Context()), g.Name))
	httpapi.WriteJSON(w, http.StatusCreated, g)
}

func (h *Handler) renameSubtype(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var body typeBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	before, beforeErr := h.service.GetSubtype(r.Context(), id)
	g, err := h.service.RenameSubtype(r.Context(), id, body.Name)
	if err != nil {
		writeError(w, err, "could not rename the subtype")
		return
	}
	if beforeErr == nil && before.Name != g.Name {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha cambiado el subtipo de cavidad «%s» por «%s».",
			h.actorLogin(r.Context()), before.Name, g.Name))
	}
	httpapi.WriteJSON(w, http.StatusOK, g)
}

func (h *Handler) deleteSubtype(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	g, getErr := h.service.GetSubtype(r.Context(), id)
	if err := h.service.DeleteSubtype(r.Context(), id); err != nil {
		writeError(w, err, "could not delete the subtype")
		return
	}
	if getErr == nil {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha borrado el subtipo de cavidad «%s».", h.actorLogin(r.Context()), g.Name))
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- places -----

// placeBody is what creating or editing a place takes.
type placeBody struct {
	TypeID            int64    `json:"type_id"`
	Name              string   `json:"name"`
	AltNames          []string `json:"alt_names"`
	State             string   `json:"state"`
	SubtypeID         *int64   `json:"subtype_id"`
	SystemID          *int64   `json:"system_id"`
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
	// OsmType and OsmID are for a place made from an element of the OpenStreetMap mirror; only a creation
	// takes them, an edit does not read them.
	OsmType *string `json:"osm_type"`
	OsmID   *int64  `json:"osm_id"`
}

func (b placeBody) input() Input { return Input(b) }

// inWhere ends a notice that names a place with where it is (" en Madrid, Comunidad de Madrid, España"),
// or with nothing if its zone is not known.
func inWhere(where string) string {
	if where == "" {
		return ""
	}
	return " en " + where
}

// placeAuditFields are the Place fields an edit's notification diffs: those Input can change (the type
// by its name).
var placeAuditFields = []string{
	"TypeName", "Subtype", "System", "Name", "AltNames", "State", "Where", "Lat", "Lon", "AltitudeM", "Access",
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
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("📍 %s ha añadido el lugar «%s» (%s)%s.",
		h.actorLogin(r.Context()), p.Name, p.TypeName, inWhere(p.Where)))
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
	in := body.input()
	in.OsmType, in.OsmID = nil, nil // The element a place is does not change with an edit.
	p, err := h.service.UpdatePlace(r.Context(), id, actorID(r.Context()), in)
	if err != nil {
		writeError(w, err, "could not edit the place")
		return
	}
	if beforeErr == nil {
		if diff := httpapi.DiffFields(before, p, placeAuditFields...); len(diff) > 0 {
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha editado el lugar «%s»%s:\n%s",
				h.actorLogin(r.Context()), before.Name, inWhere(before.Where), strings.Join(diff, "\n")))
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, p)
}

// patchFields are the fields of a place that a partial edit may name: those of an edit, but not the
// OpenStreetMap element (a place is linked with its own routes).
var patchFields = map[string]bool{
	"type_id": true, "name": true, "alt_names": true, "state": true, "subtype_id": true,
	"system_id": true, "area_id": true, "lat": true, "lon": true, "altitude_m": true, "access": true,
	"description": true, "depth_m": true, "horizontal_length_m": true, "geology_id": true,
	"technical_sheet": true,
}

// patch (PATCH /places/{id}) changes only the fields the body names and leaves the rest of the place as
// it is: a field that is not there is not touched, and one that is there as null is cleared. The result
// has to be a valid place, exactly as for a whole edit.
func (h *Handler) patch(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	raw, err := io.ReadAll(r.Body)
	var named map[string]json.RawMessage
	if err != nil || json.Unmarshal(raw, &named) != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if len(named) == 0 {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "no field to change")
		return
	}
	for field := range named {
		if !patchFields[field] {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "unknown field "+field)
			return
		}
	}
	before, beforeErr := h.service.GetPlace(r.Context(), id)
	p, err := h.service.PatchPlace(r.Context(), id, actorID(r.Context()), func(current Input) (Input, error) {
		// Decoding into a body that already has the place's values changes only what the JSON names.
		body := placeBody(current)
		if err := json.Unmarshal(raw, &body); err != nil {
			return Input{}, errInvalidBody
		}
		return body.input(), nil
	})
	if errors.Is(err, errInvalidBody) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "a field has the wrong type")
		return
	}
	if err != nil {
		writeError(w, err, "could not edit the place")
		return
	}
	if beforeErr == nil {
		if diff := httpapi.DiffFields(before, p, placeAuditFields...); len(diff) > 0 {
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha editado el lugar «%s»%s:\n%s",
				h.actorLogin(r.Context()), before.Name, inWhere(before.Where), strings.Join(diff, "\n")))
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, p)
}

// errInvalidBody is a field of a partial edit with a value of the wrong type.
var errInvalidBody = errors.New("invalid body")

// linkOsm (PUT /places/{id}/links/osm: not /places/{id}/osm, which the routes of the types would clash with) says which element of the OpenStreetMap mirror a place is ({osm_type, osm_id}), instead of the one
// it was; it answers with the place.
func (h *Handler) linkOsm(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		OsmType string `json:"osm_type"`
		OsmID   int64  `json:"osm_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	p, err := h.service.LinkOsm(r.Context(), id, actorID(r.Context()), body.OsmType, body.OsmID)
	if err != nil {
		writeError(w, err, "could not link the place to the OpenStreetMap element")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🔗 %s ha vinculado el lugar «%s»%s con un elemento de OpenStreetMap (%s %d).",
		h.actorLogin(r.Context()), p.Name, inWhere(p.Where), body.OsmType, body.OsmID))
	httpapi.WriteJSON(w, http.StatusOK, p)
}

// reviewOsm says that what changed in the OpenStreetMap element of a place has been looked at: what it is now
// is what the place has seen of it. It answers with the place.
func (h *Handler) reviewOsm(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	p, err := h.service.ReviewOsm(r.Context(), id)
	if err != nil {
		writeError(w, err, "could not mark the changes of the OpenStreetMap element as seen")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, p)
}

// unlinkOsm takes away the link of a place to an element of the OpenStreetMap mirror.
func (h *Handler) unlinkOsm(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	p, err := h.service.UnlinkOsm(r.Context(), id, actorID(r.Context()))
	if err != nil {
		writeError(w, err, "could not unlink the place from OpenStreetMap")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🔗 %s ha desvinculado el lugar «%s»%s de OpenStreetMap.",
		h.actorLogin(r.Context()), p.Name, inWhere(p.Where)))
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
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha borrado el lugar «%s»%s.",
			h.actorLogin(r.Context()), p.Name, inWhere(p.Where)))
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
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("📍 %s ha añadido el punto «%s» (%s) al lugar «%s»%s.",
			h.actorLogin(r.Context()), pointLabel(pt), pt.Kind, p.Name, inWhere(p.Where)))
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
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha cambiado el punto «%s» (%s) del lugar «%s»%s.",
			h.actorLogin(r.Context()), pointLabel(pt), pt.Kind, p.Name, inWhere(p.Where)))
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
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha quitado el punto «%s» (%s) del lugar «%s»%s.",
			h.actorLogin(r.Context()), pointLabel(pt), pt.Kind, p.Name, inWhere(p.Where)))
	}
	w.WriteHeader(http.StatusNoContent)
}
