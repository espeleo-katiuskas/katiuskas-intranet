package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/access"
)

// The intranet says a person is in the club's group exactly when it lets them use the API at all.
func TestViewerOf(t *testing.T) {
	cases := []struct {
		name      string
		roles     []string
		wantGroup bool
	}{
		{"member", []string{"member"}, true},
		{"board", []string{"board"}, true},
		{"admin", []string{"admin"}, true},
		{"a former member", []string{"former_member"}, false},
		{"only a material role", []string{"material"}, false},
		{"no roles", nil, false},
	}
	now := time.Now()
	for _, c := range cases {
		v := ViewerOf(AuthenticatedUser{ID: 42, Subject: "subject-of-42", Login: "ana", Roles: c.roles})
		if v.Issuer != IntranetIssuer || v.Subject != "subject-of-42" || v.Name != "ana" {
			t.Errorf("%s: issuer %q subject %q", c.name, v.Issuer, v.Subject)
		}
		if got := v.InGroup(access.ClubGroup, now); got != c.wantGroup {
			t.Errorf("%s: in the club's group = %v, want %v", c.name, got, c.wantGroup)
		}
		if want := access.KindRegistered; !c.wantGroup && v.KindAt(now) != want {
			t.Errorf("%s: kind %q, want %q", c.name, v.KindAt(now), want)
		}
		// What the club's rules say follows from the group.
		if got := access.ClubOnly().Allows(v, now, access.ActionSee, access.PartCoordinates); got != c.wantGroup {
			t.Errorf("%s: sees the coordinates = %v, want %v", c.name, got, c.wantGroup)
		}
	}
}

// The viewer's subject is the person's opaque one, never their id; a session that has none is not
// anybody, whatever roles it has.
func TestViewerOfSaysNothingOfTheId(t *testing.T) {
	v := ViewerOf(AuthenticatedUser{ID: 42, Subject: "abc", Roles: []string{"member"}})
	if v.Subject == "42" {
		t.Error("the subject must not be the id")
	}
	none := ViewerOf(AuthenticatedUser{ID: 42, Roles: []string{"admin"}})
	if none.KindAt(time.Now()) != access.KindAnonymous || none.InGroup(access.ClubGroup, time.Now()) {
		t.Errorf("a session with no subject is not anybody: %+v", none)
	}
}

func TestViewerFromContext(t *testing.T) {
	if v := ViewerFromContext(t.Context()); v.Subject != "" || v.KindAt(time.Now()) != access.KindAnonymous {
		t.Errorf("a context with no session must be anonymous, got %+v", v)
	}
}

// WithViewer lets an anonymous request through, resolves a good token, and refuses a bad one instead
// of treating it as anonymous.
func TestWithViewer(t *testing.T) {
	a := NewAuthenticator(stubResolver{"good": {ID: 7, Subject: "subject-of-7", Roles: []string{"member"}}})
	var got access.Viewer
	var called bool
	handler := a.WithViewer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		got = ViewerFromContext(r.Context())
	}))
	do := func(authorization string) *httptest.ResponseRecorder {
		called, got = false, access.Viewer{}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	now := time.Now()

	if rec := do(""); rec.Code != http.StatusOK || !called || got.KindAt(now) != access.KindAnonymous {
		t.Errorf("no token: want the handler called as anonymous, got %d called=%v viewer=%+v", rec.Code, called, got)
	}
	if rec := do("Bearer good"); rec.Code != http.StatusOK || !called || got.Subject != "subject-of-7" || !got.InGroup(access.ClubGroup, now) {
		t.Errorf("good token: want the member, got %d called=%v viewer=%+v", rec.Code, called, got)
	}
	if rec := do("Bearer expired"); rec.Code != http.StatusUnauthorized || called {
		t.Errorf("bad token: want 401 and no handler, got %d called=%v", rec.Code, called)
	}
}
