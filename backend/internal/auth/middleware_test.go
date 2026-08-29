package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"katiuskas-intranet/backend/internal/httpapi"
)

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
	a := NewAuthenticator("secret", time.Minute)
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
	a := NewAuthenticator("secret", time.Minute)
	token, err := a.IssueAccessToken(7, []string{"admin"})
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	var gotUser AuthenticatedUser
	var gotOK bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotOK = UserFromContext(r.Context())
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	a.RequireAuth(next).ServeHTTP(rec, req)

	if !gotOK {
		t.Fatalf("expected a user to be present in context")
	}
	if gotUser.ID != 7 || !gotUser.HasAnyRole("admin") {
		t.Errorf("unexpected user in context: %+v", gotUser)
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
	a := NewAuthenticator("secret", time.Minute)
	memberToken, err := a.IssueAccessToken(1, []string{"member"})
	if err != nil {
		t.Fatalf("issue member token: %v", err)
	}
	adminToken, err := a.IssueAccessToken(2, []string{"admin"})
	if err != nil {
		t.Fatalf("issue admin token: %v", err)
	}

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
		req.Header.Set("Authorization", "Bearer "+memberToken)
		rec := httptest.NewRecorder()
		adminOnly.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("expected 403, got %d", rec.Code)
		}
	})

	t.Run("authenticated with the right role succeeds", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()
		adminOnly.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("no roles required means any authenticated user passes", func(t *testing.T) {
		open := a.Protect(ok)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+memberToken)
		rec := httptest.NewRecorder()
		open.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rec.Code)
		}
	})
}
