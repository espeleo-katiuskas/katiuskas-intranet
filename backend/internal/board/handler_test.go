package board_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/board"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
)

func newHandlerTestServer(t *testing.T) (*http.ServeMux, testutil.FakeResolver, pgx.Tx) {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	service := board.NewService(board.NewRepository(tx))
	authenticator, resolver := testutil.NewTestAuthenticator()

	mux := http.NewServeMux()
	board.NewHandler(service, authenticator, usersService).RegisterRoutes(mux)
	return mux, resolver, tx
}

func doRequest(mux *http.ServeMux, method, path, token string, body any) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestHandlerListActiveOpenToAnyBaselineRole(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)

	for _, role := range []string{"admin", "board", "member"} {
		token := testutil.TokenFor(resolver, 1, role)
		if rec := doRequest(mux, http.MethodGet, "/api/v1/board-members", token, nil); rec.Code != http.StatusOK {
			t.Errorf("expected %s to list active board members, got %d: %s", role, rec.Code, rec.Body.String())
		}
	}
}

// TestHandlerCreateRequiresAdminNotBoard covers the CLAUDE.md rule
// directly: board can read everything here but can't write — changing
// who's on the Junta Directiva is person data, same as the rest of a
// profile, which board lost write access to entirely.
func TestHandlerCreateRequiresAdminNotBoard(t *testing.T) {
	mux, resolver, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")

	boardToken := testutil.TokenFor(resolver, 1, "board")
	path := fmt.Sprintf("/api/v1/users/%s/board-members", target.Login)
	body := map[string]string{"position": "secretary", "start_date": "2020-01-01"}
	if rec := doRequest(mux, http.MethodPost, path, boardToken, body); rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from creating a board term, got %d", rec.Code)
	}

	adminToken := testutil.TokenFor(resolver, 2, "admin")
	rec := doRequest(mux, http.MethodPost, path, adminToken, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created board.Member
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.UserLogin != target.Login {
		t.Errorf("expected user_login %q, got %q", target.Login, created.UserLogin)
	}
	if created.Position != board.PositionSecretary {
		t.Errorf("expected position secretary, got %q", created.Position)
	}
}

func TestHandlerCreateRejectsMalformedStartDate(t *testing.T) {
	mux, resolver, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	path := fmt.Sprintf("/api/v1/users/%s/board-members", target.Login)
	rec := doRequest(mux, http.MethodPost, path, adminToken, map[string]string{"position": "secretary", "start_date": "not-a-date"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 invalid_date, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandlerCreateRejectsAnOverlappingTerm exercises the exclusion
// constraint board.Repository.Create translates into ErrOverlap: nobody
// holds two board terms at once, regardless of position (see
// repository_test.go's own TestCreateRejectsOverlappingTerm).
func TestHandlerCreateRejectsAnOverlappingTerm(t *testing.T) {
	mux, resolver, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	path := fmt.Sprintf("/api/v1/users/%s/board-members", target.Login)

	first := doRequest(mux, http.MethodPost, path, adminToken, map[string]string{"position": "president", "start_date": "2020-01-01"})
	if first.Code != http.StatusCreated {
		t.Fatalf("expected the first term to be created, got %d: %s", first.Code, first.Body.String())
	}

	second := doRequest(mux, http.MethodPost, path, adminToken, map[string]string{"position": "treasurer", "start_date": "2021-01-01"})
	if second.Code != http.StatusConflict {
		t.Errorf("expected 409 overlapping_period for a second open term, got %d: %s", second.Code, second.Body.String())
	}
}

func TestHandlerUpdateRequiresAdminNotBoard(t *testing.T) {
	mux, resolver, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	createPath := fmt.Sprintf("/api/v1/users/%s/board-members", target.Login)
	created := doRequest(mux, http.MethodPost, createPath, adminToken, map[string]string{"position": "secretary", "start_date": "2020-01-01"})
	if created.Code != http.StatusCreated {
		t.Fatalf("setup create: %d %s", created.Code, created.Body.String())
	}
	var member board.Member
	if err := json.Unmarshal(created.Body.Bytes(), &member); err != nil {
		t.Fatalf("decode: %v", err)
	}

	updatePath := fmt.Sprintf("/api/v1/board-members/%d", member.ID)
	body := map[string]string{"position": "secretary", "start_date": "2020-01-01", "end_date": "2022-01-01"}

	boardToken := testutil.TokenFor(resolver, 1, "board")
	if rec := doRequest(mux, http.MethodPatch, updatePath, boardToken, body); rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from updating a board term, got %d", rec.Code)
	}

	rec := doRequest(mux, http.MethodPatch, updatePath, adminToken, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var updated board.Member
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.EndDate == nil {
		t.Errorf("expected end_date to be set, got %+v", updated)
	}
	if updated.UserLogin != target.Login {
		t.Errorf("expected user_login %q resolved back from the term's user id, got %q", target.Login, updated.UserLogin)
	}
}

func TestHandlerUpdateRejectsNonNumericID(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	body := map[string]string{"position": "secretary", "start_date": "2020-01-01"}
	rec := doRequest(mux, http.MethodPatch, "/api/v1/board-members/not-a-number", adminToken, body)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 invalid_id, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerUpdateNotFound(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	body := map[string]string{"position": "secretary", "start_date": "2020-01-01"}
	rec := doRequest(mux, http.MethodPatch, "/api/v1/board-members/999999", adminToken, body)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 not_found, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandlerListByUserIsSelfOrPrivileged covers listByUser's own
// access check directly: a plain member may see their own board
// history, admin/board may see anyone's, but a different plain member
// may see nobody else's.
func TestHandlerListByUserIsSelfOrPrivileged(t *testing.T) {
	mux, resolver, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	path := fmt.Sprintf("/api/v1/users/%s/board-members", target.Login)

	selfToken := testutil.TokenFor(resolver, target.ID, "member")
	if rec := doRequest(mux, http.MethodGet, path, selfToken, nil); rec.Code != http.StatusOK {
		t.Errorf("expected the target themselves to view their own board history, got %d: %s", rec.Code, rec.Body.String())
	}

	otherMemberToken := testutil.TokenFor(resolver, target.ID+1, "member")
	if rec := doRequest(mux, http.MethodGet, path, otherMemberToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected a different plain member to be forbidden, got %d", rec.Code)
	}

	adminToken := testutil.TokenFor(resolver, target.ID+2, "admin")
	if rec := doRequest(mux, http.MethodGet, path, adminToken, nil); rec.Code != http.StatusOK {
		t.Errorf("expected admin to view anyone's board history, got %d: %s", rec.Code, rec.Body.String())
	}

	boardToken := testutil.TokenFor(resolver, target.ID+3, "board")
	if rec := doRequest(mux, http.MethodGet, path, boardToken, nil); rec.Code != http.StatusOK {
		t.Errorf("expected board to view anyone's board history, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerListByUserUnknownLoginIsNotFound(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	rec := doRequest(mux, http.MethodGet, "/api/v1/users/no-such-login/board-members", adminToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 not_found for an unknown login, got %d", rec.Code)
	}
}
