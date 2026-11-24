package places_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/access"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/places"
)

// servedWith is a second router over the same catalogue whose handler asks other rules.
func (e *env) servedWith(rules access.Rules) *http.ServeMux {
	mux := http.NewServeMux()
	places.NewHandler(e.service, e.authenticator, e.users, e.notifier).WithRules(rules).RegisterRoutes(mux)
	return mux
}

func call(mux *http.ServeMux, method, path, token string) int {
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code
}

// The routes the rules gate, one of each kind: those that read, those that change, the reference
// data, the systems and the files.
var gatedRoutes = []struct{ method, path string }{
	{"GET", "/api/v1/places"},
	{"GET", "/api/v1/places/987654"},
	{"GET", "/api/v1/places/types"},
	{"GET", "/api/v1/places/987654/files"},
	{"GET", "/api/v1/systems"},
	{"GET", "/api/v1/users/someone/places"},
	{"POST", "/api/v1/places"},
	{"PUT", "/api/v1/places/987654"},
	{"PATCH", "/api/v1/places/987654"},
	{"DELETE", "/api/v1/places/987654"},
	{"POST", "/api/v1/places/987654/points"},
	{"POST", "/api/v1/places/987654/files"},
	{"POST", "/api/v1/systems"},
	{"PUT", "/api/v1/systems/987654/places"},
	{"POST", "/api/v1/places/types"},
}

// The rules decide, not the role: with none every route is refused to everybody who signs in, an admin
// included; with the club's, only changing needs a role on top; and a rule for seeing opens the
// reading and nothing that changes.
func TestRulesGateEveryRoute(t *testing.T) {
	e := newEnv(t)
	people := map[string]string{"member": e.member.token, "board": e.board.token, "admin": e.admin.token}

	denyAll := e.servedWith(access.Rules{})
	for name, token := range people {
		for _, r := range gatedRoutes {
			if got := call(denyAll, r.method, r.path, token); got != http.StatusForbidden {
				t.Errorf("no rules, %s: %s %s = %d, want 403", name, r.method, r.path, got)
			}
		}
	}

	// Seeing open to the club, and writing open to nobody: the reading goes through (to a 200, or to a
	// 404 for the place that is not there), the changes do not.
	seeOnly := e.servedWith(access.Rules{{Part: access.PartPlace, Action: access.ActionSee, Audience: access.Group(access.ClubGroup)}})
	for name, token := range people {
		for _, r := range gatedRoutes {
			got := call(seeOnly, r.method, r.path, token)
			if r.method == "GET" && got == http.StatusForbidden {
				t.Errorf("see only, %s: %s %s refused", name, r.method, r.path)
			}
			if r.method != "GET" && got != http.StatusForbidden {
				t.Errorf("see only, %s: %s %s = %d, want 403", name, r.method, r.path, got)
			}
		}
	}

	// Seeing open to anybody registered is the same for a person who signs in, and the club's rules,
	// which every other test uses, refuse nobody of them.
	for name, rules := range map[string]access.Rules{
		"registered may see": {{Part: access.PartPlace, Action: access.ActionSee, Audience: access.Registered()}},
		"public may see":     {{Part: access.PartPlace, Action: access.ActionSee, Audience: access.Public()}},
	} {
		mux := e.servedWith(rules)
		if got := call(mux, "GET", "/api/v1/places", e.member.token); got != http.StatusOK {
			t.Errorf("%s: GET /places = %d, want 200", name, got)
		}
		if got := call(mux, "POST", "/api/v1/places", e.member.token); got != http.StatusForbidden {
			t.Errorf("%s: POST /places = %d, want 403", name, got)
		}
	}
	if got := call(e.mux, "GET", "/api/v1/places", e.member.token); got != http.StatusOK {
		t.Errorf("the club's rules: GET /places = %d, want 200", got)
	}
}

// A route added without the gate would open to anybody the rules do not mean: every route of the
// handler goes through it.
func TestEveryRouteIsGated(t *testing.T) {
	src, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatal(err)
	}
	routes := regexp.MustCompile(`(?m)^\s*mux\.Handle\(.*$`).FindAllString(string(src), -1)
	if len(routes) < 30 {
		t.Fatalf("found only %d routes in handler.go: has the way they are registered changed?", len(routes))
	}
	for _, line := range routes {
		if !strings.Contains(line, "h.gate(") {
			t.Errorf("a route without the gate: %s", strings.TrimSpace(line))
		}
	}
}
