package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
)

// stubResolver is a minimal SessionResolver for this package's own tests:
// a fixed map from raw token to the user it resolves to, with anything
// else rejected as ErrInvalidToken. The real, database-backed resolver
// lives in the tokens package and has its own tests.
type stubResolver map[string]AuthenticatedUser

func (s stubResolver) Authenticate(_ context.Context, rawToken, _, _ string) (AuthenticatedUser, error) {
	user, ok := s[rawToken]
	if !ok {
		return AuthenticatedUser{}, ErrInvalidToken
	}
	return user, nil
}

func TestHasAnyRole(t *testing.T) {
	u := AuthenticatedUser{ID: 1, Roles: []string{"board", "member"}}
	if !u.HasAnyRole("admin", "board") {
		t.Errorf("expected HasAnyRole(admin, board) to find board")
	}
	if u.HasAnyRole("admin") {
		t.Errorf("expected HasAnyRole(admin) to be false")
	}

	empty := AuthenticatedUser{}
	if empty.HasAnyRole("member") {
		t.Errorf("expected a user with no roles to match nothing")
	}
}

func TestRequireAuthRejectsMissingOrInvalidToken(t *testing.T) {
	a := NewAuthenticator(stubResolver{})
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })

	cases := []struct {
		name   string
		header string
	}{
		{"missing header", ""},
		{"malformed scheme", "not-bearer-scheme"},
		{"garbage token", "Bearer garbage"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called = false
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			a.RequireAuth(next).ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("expected 401, got %d", rec.Code)
			}
			if called {
				t.Errorf("expected the protected handler not to run")
			}

			var body httpapi.ErrorResponse
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if body.Error.Code != "unauthorized" {
				t.Errorf("expected error code %q, got %q", "unauthorized", body.Error.Code)
			}
		})
	}
}

func TestRequireAuthSetsUserInContext(t *testing.T) {
	resolver := stubResolver{"valid-token": AuthenticatedUser{ID: 7, Roles: []string{"admin"}}}
	a := NewAuthenticator(resolver)

	var gotUser AuthenticatedUser
	var gotOK bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotOK = UserFromContext(r.Context())
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rec := httptest.NewRecorder()
	a.RequireAuth(next).ServeHTTP(rec, req)

	if !gotOK {
		t.Fatalf("expected a user to be present in context")
	}
	if gotUser.ID != 7 || !gotUser.HasAnyRole("admin") {
		t.Errorf("unexpected user in context: %+v", gotUser)
	}
}

// TestRequireAuthRecordsTheUserForRequestLogging checks the wiring to
// httpapi.SetRequestUserID end-to-end (through RequestLogger, whose
// request-log-fields box is unexported and only reachable this way from
// outside the httpapi package) rather than just that RequireAuth calls
// some function — a real request through both middlewares is what
// actually matters here: does the final log line name the caller.
func TestRequireAuthRecordsTheUserForRequestLogging(t *testing.T) {
	resolver := stubResolver{"valid-token": AuthenticatedUser{ID: 7, Roles: []string{"admin"}}}
	a := NewAuthenticator(resolver)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(previous)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rec := httptest.NewRecorder()
	httpapi.RequestLogger(a.RequireAuth(next)).ServeHTTP(rec, req)

	if !strings.Contains(buf.String(), "user_id=7") {
		t.Errorf("expected the request log line to include user_id=7, got %q", buf.String())
	}
}

// Both ways of authenticating tell whoever counts the requests who made one, and whether an
// administrator was acting as them.
func TestRequireAuthTellsTheRequestCounterWhoMadeTheRequest(t *testing.T) {
	admin := int64(1)
	resolver := stubResolver{
		"own":          AuthenticatedUser{ID: 7, Roles: []string{"member"}},
		"impersonated": AuthenticatedUser{ID: 8, Roles: []string{"member"}, ImpersonatedBy: &admin},
	}
	a := NewAuthenticator(resolver)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	for name, wrap := range map[string]func(http.Handler) http.Handler{
		"RequireAuth":                   a.RequireAuth,
		"RequireAuthFromHeaderOrCookie": a.RequireAuthFromHeaderOrCookie,
	} {
		for token, want := range map[string]httpapi.Actor{
			"own":          {UserID: 7, Known: true},
			"impersonated": {UserID: 8, Impersonated: true, Known: true},
			"nobody":       {},
		} {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			ctx, actor := httpapi.WithActorBox(req.Context())
			wrap(next).ServeHTTP(httptest.NewRecorder(), req.WithContext(ctx))
			if *actor != want {
				t.Errorf("%s, token %q: expected %+v, got %+v", name, token, want, *actor)
			}
		}
	}
}

func TestRequireAuthFromHeaderOrCookieAcceptsEitherTransport(t *testing.T) {
	resolver := stubResolver{"valid-token": AuthenticatedUser{ID: 7, Roles: []string{"admin"}}}
	a := NewAuthenticator(resolver)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := a.RequireAuthFromHeaderOrCookie(next)

	t.Run("bearer header alone", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer valid-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("cookie alone", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "valid-token"})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("header takes precedence over a mismatched cookie", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer valid-token")
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "garbage"})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 (header wins), got %d", rec.Code)
		}
	})

	t.Run("neither present yields 401", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("invalid cookie value yields 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "garbage"})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})
}

// TestRequireAuthIgnoresTheSessionCookie is the important negative case:
// every *other* route in the API keeps using plain RequireAuth/Protect,
// so a cookie alone (no bearer header) must never authenticate them —
// only the specific file-serving routes that opt into
// RequireAuthFromHeaderOrCookie get that fallback.
func TestRequireAuthIgnoresTheSessionCookie(t *testing.T) {
	resolver := stubResolver{"valid-token": AuthenticatedUser{ID: 7, Roles: []string{"admin"}}}
	a := NewAuthenticator(resolver)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "valid-token"})
	rec := httptest.NewRecorder()
	a.RequireAuth(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 (RequireAuth must not fall back to the cookie), got %d", rec.Code)
	}
}

func TestRequireRole(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := RequireRole("admin", "board")(next)

	withUser := func(u AuthenticatedUser) *http.Request {
		ctx := context.WithValue(context.Background(), userContextKey, u)
		return httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	}

	t.Run("allowed", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, withUser(AuthenticatedUser{ID: 1, Roles: []string{"board"}}))
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("forbidden", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, withUser(AuthenticatedUser{ID: 1, Roles: []string{"member"}}))
		if rec.Code != http.StatusForbidden {
			t.Errorf("expected 403, got %d", rec.Code)
		}
	})

	t.Run("no user in context at all", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusForbidden {
			t.Errorf("expected 403, got %d", rec.Code)
		}
	})
}

func TestProtectComposesAuthenticationBeforeAuthorization(t *testing.T) {
	resolver := stubResolver{
		"member-token":   AuthenticatedUser{ID: 1, Roles: []string{"member"}},
		"admin-token":    AuthenticatedUser{ID: 2, Roles: []string{"admin"}},
		"outsider-token": AuthenticatedUser{ID: 3, Roles: []string{"former_member"}},
		"material-token": AuthenticatedUser{ID: 4, Roles: []string{"material"}},
	}
	a := NewAuthenticator(resolver)

	ok := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }
	adminOnly := a.Protect(ok, "admin")

	t.Run("no token yields 401, not 403", func(t *testing.T) {
		rec := httptest.NewRecorder()
		adminOnly.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("authenticated but wrong role yields 403", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer member-token")
		rec := httptest.NewRecorder()
		adminOnly.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("expected 403, got %d", rec.Code)
		}
	})

	t.Run("authenticated with the right role succeeds", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer admin-token")
		rec := httptest.NewRecorder()
		adminOnly.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("no extra roles required still needs a baseline role", func(t *testing.T) {
		open := a.Protect(ok)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer member-token")
		rec := httptest.NewRecorder()
		open.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected a baseline role (member) to pass, got %d", rec.Code)
		}
	})

	t.Run("baseline applies even without a token role outside it", func(t *testing.T) {
		open := a.Protect(ok)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer outsider-token")
		rec := httptest.NewRecorder()
		open.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("expected 403 for a role outside admin/board/member, got %d", rec.Code)
		}
	})

	t.Run("baseline applies on top of a route's own specific roles", func(t *testing.T) {
		materialOnly := a.Protect(ok, "material")
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer material-token")
		rec := httptest.NewRecorder()
		materialOnly.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("expected 403: holding \"material\" alone isn't enough without admin/board/member too, got %d", rec.Code)
		}
	})
}

// recordingResolver keeps what the middleware told the resolver about the request: the address and
// the User-Agent that end up in the session's last-use record.
type recordingResolver struct {
	ip, userAgent string
}

func (r *recordingResolver) Authenticate(_ context.Context, _, ip, userAgent string) (AuthenticatedUser, error) {
	r.ip, r.userAgent = ip, userAgent
	return AuthenticatedUser{ID: 1, Roles: []string{"member"}}, nil
}

func TestTheMiddlewareTellsTheResolverTheAddressAndUserAgent(t *testing.T) {
	for name, wrap := range map[string]func(*Authenticator, http.Handler) http.Handler{
		"RequireAuth":                   func(a *Authenticator, h http.Handler) http.Handler { return a.RequireAuth(h) },
		"RequireAuthFromHeaderOrCookie": func(a *Authenticator, h http.Handler) http.Handler { return a.RequireAuthFromHeaderOrCookie(h) },
	} {
		resolver := &recordingResolver{}
		handler := wrap(NewAuthenticator(resolver), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer abc")
		req.Header.Set("User-Agent", "TestBrowser/1.0")
		req.RemoteAddr = "203.0.113.7:4321"
		handler.ServeHTTP(httptest.NewRecorder(), req)
		if resolver.userAgent != "TestBrowser/1.0" || resolver.ip == "" {
			t.Errorf("%s: expected the address and the user agent to reach the resolver, got %+v", name, resolver)
		}
	}
}
