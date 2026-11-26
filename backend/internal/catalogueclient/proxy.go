package catalogueclient

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
)

// ActivityCounter says in how many activities that are not cancelled each place is one of those they go
// to: what the catalogue cannot know, and the intranet adds to what it answers (activities.Service).
type ActivityCounter interface {
	ActivityCounts(ctx context.Context, placeIDs []int64) (map[int64]int, error)
}

// maxInjectedBody is the largest answer of the catalogue that is read to add the counts of activities; a
// larger one goes through as it is (with every count at zero).
const maxInjectedBody = 32 << 20

// proxiedPrefixes are the parts of the API that the catalogue answers; every route under them is
// forwarded as it is.
var proxiedPrefixes = []string{"/api/v1/places", "/api/v1/systems", "/api/v1/areas", "/api/v1/osm"}

// Proxy forwards what the members ask of the catalogue under the same addresses the intranet always
// had (/api/v1/places…), so that the frontend, its service worker and its outbox do not change: the
// session of the person is replaced by an assertion about them, and the answers that list places get
// the number of activities that went to each.
type Proxy struct {
	client        *Client
	authenticator *auth.Authenticator
	counts        ActivityCounter
	rp            *httputil.ReverseProxy
}

// NewProxy makes the proxy. counts may be nil, and then no count is added.
func NewProxy(client *Client, authenticator *auth.Authenticator, counts ActivityCounter) *Proxy {
	p := &Proxy{client: client, authenticator: authenticator, counts: counts}
	p.rp = &httputil.ReverseProxy{
		Rewrite:        p.rewrite,
		ModifyResponse: p.modifyResponse,
		ErrorHandler:   p.fail,
	}
	return p
}

func (p *Proxy) rewrite(r *httputil.ProxyRequest) {
	r.SetURL(p.client.base)
	r.SetXForwarded()
	// What the catalogue gets is an assertion, never the session of the intranet: not as a token, and
	// not as the cookie that file routes also accept.
	r.Out.Header.Del("Cookie")
	r.Out.Header.Del("Accept-Encoding") // So that the answer can be read to add the counts.
	token, err := p.client.Assertion(r.In.Context())
	if err != nil {
		// Protect has put a person there, so this is a session without a subject: say nobody.
		r.Out.Header.Set("Authorization", "Bearer invalid")
		return
	}
	r.Out.Header.Set("Authorization", "Bearer "+token)
}

func (p *Proxy) fail(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("the catalogue does not answer", "method", r.Method, "path", r.URL.Path, "error", err)
	httpapi.WriteError(w, http.StatusBadGateway, "catalogue_unavailable", "the catalogue of places does not answer")
}

// RegisterRoutes forwards every route under the prefixes of the catalogue. The files are the one thing
// that is also read with the session cookie (an image in a page cannot send the header).
func (p *Proxy) RegisterRoutes(mux *http.ServeMux) {
	plain := p.authenticator.Protect(http.HandlerFunc(p.rp.ServeHTTP))
	cookie := p.authenticator.ProtectAllowingCookie(http.HandlerFunc(p.rp.ServeHTTP))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && isFileRoute(r.URL.Path) {
			cookie.ServeHTTP(w, r)
			return
		}
		plain.ServeHTTP(w, r)
	})
	for _, prefix := range proxiedPrefixes {
		mux.Handle(prefix, handler)
		mux.Handle(prefix+"/", handler)
	}
}

// isFileRoute is /places/{id}/files/{fileId}/file (and of a system): the bytes of a file.
func isFileRoute(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	return len(parts) == 7 && parts[4] == "files" && parts[6] == "file" &&
		(parts[2] == "places" || parts[2] == "systems")
}

func (p *Proxy) modifyResponse(resp *http.Response) error {
	if p.counts == nil || resp.Request.Method != http.MethodGet || resp.StatusCode != http.StatusOK ||
		!strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") ||
		resp.ContentLength > maxInjectedBody {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxInjectedBody+1))
	_ = resp.Body.Close()
	if err != nil {
		return err
	}
	if len(body) <= maxInjectedBody {
		if injected, err := p.inject(resp.Request.Context(), body); err != nil {
			slog.Error("count the activities of the places", "error", err)
		} else {
			body = injected
		}
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	return nil
}

// inject adds activity_count to every place of a JSON answer: those of a listing, a place, the cavities of
// a system. A place is recognised by what only it has (id, type_id, kind and area_ids). The catalogue says
// which parts of a place its viewer may not see (withheld); the activities are one of them, and their
// count then stays at zero.
func (p *Proxy) inject(ctx context.Context, body []byte) ([]byte, error) {
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		return body, nil // Not what is being looked for: through as it is.
	}
	var places []map[string]any
	collectPlaces(doc, &places)
	if len(places) == 0 {
		return body, nil
	}
	ids := make([]int64, 0, len(places))
	for _, pl := range places {
		if id, ok := pl["id"].(float64); ok && !withholdsActivities(pl) {
			ids = append(ids, int64(id))
		}
	}
	counts, err := p.counts.ActivityCounts(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, pl := range places {
		id, _ := pl["id"].(float64)
		if withholdsActivities(pl) {
			pl["activity_count"] = 0
		} else {
			pl["activity_count"] = counts[int64(id)]
		}
	}
	return json.Marshal(doc)
}

func collectPlaces(v any, out *[]map[string]any) {
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			collectPlaces(e, out)
		}
	case map[string]any:
		_, hasID := x["id"]
		_, hasType := x["type_id"]
		_, hasKind := x["kind"]
		_, hasAreas := x["area_ids"]
		if hasID && hasType && hasKind && hasAreas {
			*out = append(*out, x)
		}
		for _, e := range x {
			collectPlaces(e, out)
		}
	}
}

func withholdsActivities(place map[string]any) bool {
	parts, _ := place["withheld"].([]any)
	for _, part := range parts {
		if part == "activities" {
			return true
		}
	}
	return false
}
