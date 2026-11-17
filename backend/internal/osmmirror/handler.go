package osmmirror

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
)

type Handler struct {
	service       *Service
	authenticator *auth.Authenticator
}

func NewHandler(service *Service, authenticator *auth.Authenticator) *Handler {
	return &Handler{service: service, authenticator: authenticator}
}

// RegisterRoutes: whoever can sign in reads the mirror. Nothing here changes it: only cmd/osmsync does.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	a := h.authenticator
	mux.Handle("GET /api/v1/osm/elements", a.Protect(h.list))
	mux.Handle("GET /api/v1/osm/elements/{type}/{id}", a.Protect(h.get))
	mux.Handle("GET /api/v1/osm/nearby", a.Protect(h.nearby))
}

func writeError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, ErrInvalidBounds):
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_bbox", err.Error())
	case errors.Is(err, ErrInvalidRadius):
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_radius", err.Error())
	case errors.Is(err, ErrInvalidCategory):
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_osm_category", err.Error())
	case errors.Is(err, ErrInvalidElement):
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_osm_element", err.Error())
	case errors.Is(err, ErrNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "not_found", err.Error())
	default:
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", fallback)
	}
}

// parseBounds reads ?bbox=south,west,north,east.
func parseBounds(text string) (Bounds, bool) {
	parts := strings.Split(text, ",")
	if len(parts) != 4 {
		return Bounds{}, false
	}
	var v [4]float64
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return Bounds{}, false
		}
		v[i] = f
	}
	return Bounds{South: v[0], West: v[1], North: v[2], East: v[3]}, true
}

// list gives the elements in a box (?bbox=south,west,north,east), optionally of some categories
// (?category=cave,canyon): {"elements": [Point], "truncated": bool}.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	b, ok := parseBounds(query.Get("bbox"))
	if !ok {
		writeError(w, ErrInvalidBounds, "")
		return
	}
	var categories []string
	for _, c := range strings.Split(query.Get("category"), ",") {
		if c = strings.TrimSpace(c); c != "" {
			categories = append(categories, c)
		}
	}
	points, more, err := h.service.Elements(r.Context(), b, categories)
	if err != nil {
		writeError(w, err, "could not read the OpenStreetMap elements")
		return
	}
	// The mirror changes once a week at most, and the map asks for the same box again and again.
	w.Header().Set("Cache-Control", "private, max-age=300")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"elements": points, "truncated": more})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	d, err := h.service.Element(r.Context(), r.PathValue("type"), id)
	if err != nil {
		writeError(w, err, "could not read the OpenStreetMap element")
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=300")
	httpapi.WriteJSON(w, http.StatusOK, d)
}

// nearby gives the elements near a point that are not yet a place, for linking a place of the catalogue to
// one (?lat=&lon=, ?radius= in metres, ?name= to rank those that look like it, ?category= as in list):
// {"elements": [Candidate]}.
func (h *Handler) nearby(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	lat, latErr := strconv.ParseFloat(query.Get("lat"), 64)
	lon, lonErr := strconv.ParseFloat(query.Get("lon"), 64)
	if latErr != nil || lonErr != nil {
		writeError(w, ErrInvalidBounds, "")
		return
	}
	radius := 0
	if text := query.Get("radius"); text != "" {
		n, err := strconv.Atoi(text)
		if err != nil {
			writeError(w, ErrInvalidRadius, "")
			return
		}
		radius = n
	}
	var categories []string
	for _, c := range strings.Split(query.Get("category"), ",") {
		if c = strings.TrimSpace(c); c != "" {
			categories = append(categories, c)
		}
	}
	found, err := h.service.Nearby(r.Context(), lat, lon, radius, query.Get("name"), categories)
	if err != nil {
		writeError(w, err, "could not look for the elements near the point")
		return
	}
	if found == nil {
		found = []Candidate{}
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"elements": found})
}
