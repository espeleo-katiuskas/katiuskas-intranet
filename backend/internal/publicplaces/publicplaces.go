// Package publicplaces is the read-only face of the catalogue of places for whoever is not signed in
// (and for whoever is, who sees more): the routes under /api/v1/public. It is a package of its own so that
// what it can say is only what is written here: every field of what it answers is copied by name from a
// place that has already been through the redaction of internal/places (Place.ViewedBy), so who wrote
// it, the activities that went there, the element of OpenStreetMap, the visibility itself and the
// files can never be in it, whatever the place has. A route that was wired here by mistake could not
// give more than this.
package publicplaces

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/access"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/places"
)

// Catalogue is what the public routes ask of the places: the data as it is, which they then redact for
// the viewer before saying anything of it.
type Catalogue interface {
	ListPlaces(ctx context.Context) ([]places.ListItem, error)
	GetPlace(ctx context.Context, id int64) (places.Place, error)
}

type Handler struct {
	catalogue     Catalogue
	authenticator *auth.Authenticator
	limiter       *httpapi.IPRateLimiter
	now           func() time.Time
}

// NewHandler makes the public routes. Each address may ask at most `limit` times per `window` (no limit
// if it is not positive), which is how scraping the whole catalogue is made costly.
func NewHandler(catalogue Catalogue, authenticator *auth.Authenticator, limit int, window time.Duration) *Handler {
	return &Handler{
		catalogue: catalogue, authenticator: authenticator, now: time.Now,
		limiter: httpapi.NewIPRateLimiter(limit, window),
	}
}

// RegisterRoutes: anybody may read what is public, so there is no Protect: the viewer is whoever has a
// token (a bad one is 401, never the public answer) or nobody. The rate limit comes first, as it costs
// nothing to the one who refuses.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/public/places", h.route(h.list))
	mux.Handle("GET /api/v1/public/places/{id}", h.route(h.get))
}

func (h *Handler) route(next http.HandlerFunc) http.Handler {
	withViewer := h.authenticator.WithViewer(http.HandlerFunc(next))
	return h.limiter.Limit(func(w http.ResponseWriter, r *http.Request) {
		withViewer.ServeHTTP(w, r)
	})
}

// cacheHeaders lets a cache in front keep what anybody may see for a minute, and keeps what a signed-in
// person sees out of every shared cache. The answer depends on who asks, so it varies on the token.
func cacheHeaders(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Vary", "Authorization")
	if r.Header.Get("Authorization") != "" {
		w.Header().Set("Cache-Control", "private, no-store")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=60, stale-while-revalidate=300")
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	items, err := h.catalogue.ListPlaces(r.Context())
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list the places")
		return
	}
	cacheHeaders(w, r)
	httpapi.WriteJSON(w, http.StatusOK, ViewList(items, auth.ViewerFromContext(r.Context()), h.now()))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	p, err := h.catalogue.GetPlace(r.Context(), id)
	// A place that is not there and one that is closed to this viewer are the same answer.
	notFound := func() {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "place not found")
	}
	if err != nil {
		if errors.Is(err, places.ErrNotFound) {
			notFound()
			return
		}
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not read the place")
		return
	}
	seen, ok := ViewPlace(p, auth.ViewerFromContext(r.Context()), h.now())
	if !ok {
		notFound()
		return
	}
	cacheHeaders(w, r)
	httpapi.WriteJSON(w, http.StatusOK, seen)
}

// ViewList is the places of a list that the viewer may know exist, each as the public may see it. The
// pages of the public site use it too, so that they and the routes say the same.
func ViewList(items []places.ListItem, viewer access.Viewer, now time.Time) []ListItem {
	list := make([]ListItem, 0, len(items))
	for _, it := range items {
		if seen, ok := it.ViewedBy(viewer, now); ok {
			list = append(list, listItemOf(seen))
		}
	}
	return list
}

// ViewPlace is a place as the public may see it, and whether the viewer may know it exists.
func ViewPlace(p places.Place, viewer access.Viewer, now time.Time) (Place, bool) {
	seen, ok := p.ViewedBy(viewer, now)
	if !ok {
		return Place{}, false
	}
	return placeOf(seen), true
}

// withheld says whether the viewer may not see a part of the place.
func withheld(parts []access.Part, part access.Part) bool {
	for _, p := range parts {
		if p == part {
			return true
		}
	}
	return false
}
