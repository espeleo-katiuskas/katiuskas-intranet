package clubinfo_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/clubinfo"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
)

type fakeNotifier struct {
	mu   sync.Mutex
	sent []string
}

func (f *fakeNotifier) Send(_ context.Context, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, text)
	return nil
}

func (f *fakeNotifier) wait(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		if len(f.sent) > 0 {
			msg := f.sent[0]
			f.sent = f.sent[1:]
			f.mu.Unlock()
			return msg
		}
		f.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for a notification")
	return ""
}

type env struct {
	mux      *http.ServeMux
	notifier *fakeNotifier
	admin    string
	board    string
	member   string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	tx := testutil.NewTx(t)
	// The migration seeds what the home page used to show; these tests start from an empty list.
	if _, err := tx.Exec(context.Background(), `DELETE FROM club_info_items`); err != nil {
		t.Fatal(err)
	}
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	authenticator, resolver := testutil.NewTestAuthenticator()
	notifier := &fakeNotifier{}
	mux := http.NewServeMux()
	clubinfo.NewHandler(clubinfo.NewService(clubinfo.NewRepository(tx)), authenticator, usersService, notifier).RegisterRoutes(mux)
	admin := testutil.InsertUser(t, tx, "")
	board := testutil.InsertUser(t, tx, "")
	member := testutil.InsertUser(t, tx, "")
	return &env{
		mux: mux, notifier: notifier,
		admin:  testutil.TokenFor(resolver, admin.ID, "admin"),
		board:  testutil.TokenFor(resolver, board.ID, "board"),
		member: testutil.TokenFor(resolver, member.ID, "member"),
	}
}

func (e *env) do(method, path, token string, body any) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

func expect(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("expected %d, got %d: %s", status, rec.Code, rec.Body.String())
	}
	if code != "" {
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body.Error.Code != code {
			t.Fatalf("expected error code %q, got %q", code, body.Error.Code)
		}
	}
}

func create(t *testing.T, e *env, body map[string]any) clubinfo.Item {
	t.Helper()
	rec := e.do("POST", "/api/v1/club-info", e.admin, body)
	expect(t, rec, http.StatusCreated, "")
	return decode[clubinfo.Item](t, rec)
}

func labels(items []clubinfo.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Label
	}
	return out
}

func TestListIsForAnyoneSignedIn(t *testing.T) {
	e := newEnv(t)
	create(t, e, map[string]any{"label": "NIF", "value": "G-1", "copy_value": "G1", "copy_label": "Copiar NIF"})
	create(t, e, map[string]any{"label": "Bizum", "value": "600 000 000"})

	expect(t, e.do("GET", "/api/v1/club-info", "", nil), http.StatusUnauthorized, "")
	for _, token := range []string{e.member, e.board, e.admin} {
		items := decode[[]clubinfo.Item](t, e.do("GET", "/api/v1/club-info", token, nil))
		if got := strings.Join(labels(items), ","); got != "NIF,Bizum" {
			t.Errorf("expected the items in order, got %q", got)
		}
	}
	items := decode[[]clubinfo.Item](t, e.do("GET", "/api/v1/club-info", e.member, nil))
	if items[0].CopyValue == nil || *items[0].CopyValue != "G1" || *items[0].CopyLabel != "Copiar NIF" || items[1].CopyValue != nil {
		t.Errorf("unexpected copy fields: %+v", items)
	}
}

func TestOnlyAdminWrites(t *testing.T) {
	e := newEnv(t)
	item := create(t, e, map[string]any{"label": "Nombre", "value": "Club"})
	body := map[string]any{"label": "x", "value": "y"}
	path := fmt.Sprintf("/api/v1/club-info/%d", item.ID)
	for _, token := range []string{e.member, e.board} {
		expect(t, e.do("POST", "/api/v1/club-info", token, body), http.StatusForbidden, "forbidden")
		expect(t, e.do("PUT", path, token, body), http.StatusForbidden, "forbidden")
		expect(t, e.do("DELETE", path, token, nil), http.StatusForbidden, "forbidden")
		expect(t, e.do("PUT", "/api/v1/club-info/order", token, map[string]any{"item_ids": []int64{item.ID}}), http.StatusForbidden, "forbidden")
	}
}

func TestCreateValidatesAndNormalizes(t *testing.T) {
	e := newEnv(t)
	long := strings.Repeat("a", 501)
	for name, c := range map[string]struct {
		body map[string]any
		code string
	}{
		"no label":                {map[string]any{"label": "  ", "value": "v"}, "invalid_label"},
		"label too long":          {map[string]any{"label": strings.Repeat("a", 101), "value": "v"}, "invalid_label"},
		"no value":                {map[string]any{"label": "l", "value": " "}, "invalid_value"},
		"value too long":          {map[string]any{"label": "l", "value": long}, "invalid_value"},
		"copy label without text": {map[string]any{"label": "l", "value": "v", "copy_label": "Copiar"}, "invalid_copy"},
		"copy text too long":      {map[string]any{"label": "l", "value": "v", "copy_value": long}, "invalid_copy"},
	} {
		if rec := e.do("POST", "/api/v1/club-info", e.admin, c.body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d: %s", name, rec.Code, rec.Body.String())
		} else if got := decode[struct {
			Error struct{ Code string } `json:"error"`
		}](t, rec).Error.Code; got != c.code {
			t.Errorf("%s: expected %s, got %s", name, c.code, got)
		}
	}
	expect(t, e.do("POST", "/api/v1/club-info", e.admin, "not an object"), http.StatusBadRequest, "invalid_body")

	// Blanks around what was typed go; a blank button text or text to copy is no button.
	item := create(t, e, map[string]any{"label": "  Teléfono  ", "value": " 600 ", "copy_value": "  ", "copy_label": ""})
	if item.Label != "Teléfono" || item.Value != "600" || item.CopyValue != nil || item.CopyLabel != nil {
		t.Errorf("unexpected normalization: %+v", item)
	}
}

func TestUpdateReplacesAndTheNotificationSaysWhatChanged(t *testing.T) {
	e := newEnv(t)
	item := create(t, e, map[string]any{"label": "Bizum", "value": "611 111 111", "copy_value": "611111111", "copy_label": "Copiar"})
	if msg := e.notifier.wait(t); !strings.Contains(msg, "«Bizum»") || !strings.Contains(msg, "611 111 111") {
		t.Errorf("the creation notice should say what was added: %q", msg)
	}
	path := fmt.Sprintf("/api/v1/club-info/%d", item.ID)

	rec := e.do("PUT", path, e.admin, map[string]any{"label": "Bizum", "value": "622 222 222"})
	expect(t, rec, http.StatusOK, "")
	updated := decode[clubinfo.Item](t, rec)
	if updated.Value != "622 222 222" || updated.CopyValue != nil || updated.CopyLabel != nil || updated.Position != item.Position {
		t.Errorf("a field left out should turn the button off and keep the place: %+v", updated)
	}
	// Like any other edit, it says what changed: field, before and after.
	msg := e.notifier.wait(t)
	for _, want := range []string{"«Bizum»", "Value: 611 111 111 → 622 222 222", "CopyValue: 611111111 → (vacío)", "CopyLabel: Copiar → (vacío)"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the edit notice should contain %q: %q", want, msg)
		}
	}
	if strings.Contains(msg, "\nLabel:") {
		t.Errorf("an unchanged field should not be listed: %q", msg)
	}

	// Sending the same again changes nothing, so it says nothing.
	expect(t, e.do("PUT", path, e.admin, map[string]any{"label": "Bizum", "value": "622 222 222"}), http.StatusOK, "")
	expect(t, e.do("PUT", "/api/v1/club-info/999999", e.admin, map[string]any{"label": "a", "value": "b"}), http.StatusNotFound, "not_found")
	expect(t, e.do("PUT", "/api/v1/club-info/abc", e.admin, map[string]any{"label": "a", "value": "b"}), http.StatusBadRequest, "invalid_id")
	expect(t, e.do("PUT", path, e.admin, map[string]any{"label": "", "value": "b"}), http.StatusBadRequest, "invalid_label")
	time.Sleep(50 * time.Millisecond)
	e.notifier.mu.Lock()
	extra := len(e.notifier.sent)
	e.notifier.mu.Unlock()
	if extra != 0 {
		t.Errorf("an edit that changes nothing should notify nothing, got %d more", extra)
	}
}

func TestDelete(t *testing.T) {
	e := newEnv(t)
	item := create(t, e, map[string]any{"label": "Bizum", "value": "600"})
	e.notifier.wait(t)
	path := fmt.Sprintf("/api/v1/club-info/%d", item.ID)
	expect(t, e.do("DELETE", path, e.admin, nil), http.StatusNoContent, "")
	if msg := e.notifier.wait(t); !strings.Contains(msg, "«Bizum»") || !strings.Contains(msg, "600") {
		t.Errorf("unexpected notification: %q", msg)
	}
	expect(t, e.do("DELETE", path, e.admin, nil), http.StatusNotFound, "not_found")
	if items := decode[[]clubinfo.Item](t, e.do("GET", "/api/v1/club-info", e.member, nil)); len(items) != 0 {
		t.Errorf("expected none left, got %+v", items)
	}
}

func TestReorderReplacesTheWholeOrder(t *testing.T) {
	e := newEnv(t)
	a := create(t, e, map[string]any{"label": "A", "value": "1"})
	b := create(t, e, map[string]any{"label": "B", "value": "2"})
	c := create(t, e, map[string]any{"label": "C", "value": "3"})

	rec := e.do("PUT", "/api/v1/club-info/order", e.admin, map[string]any{"item_ids": []int64{c.ID, a.ID, b.ID}})
	expect(t, rec, http.StatusOK, "")
	if got := strings.Join(labels(decode[[]clubinfo.Item](t, rec)), ""); got != "CAB" {
		t.Errorf("expected CAB, got %s", got)
	}
	if got := strings.Join(labels(decode[[]clubinfo.Item](t, e.do("GET", "/api/v1/club-info", e.member, nil))), ""); got != "CAB" {
		t.Errorf("the order should be kept, got %s", got)
	}
	// A new one goes last.
	d := create(t, e, map[string]any{"label": "D", "value": "4"})
	if d.Position <= c.Position {
		t.Errorf("a new item should go after the last one: %+v", d)
	}

	// Anything but exactly every item, each once, is refused and changes nothing.
	for name, ids := range map[string][]int64{
		"short":     {a.ID, b.ID},
		"repeated":  {a.ID, a.ID, b.ID, c.ID},
		"unknown":   {a.ID, b.ID, c.ID, 999999},
		"duplicate": {a.ID, b.ID, c.ID, c.ID},
	} {
		if rec := e.do("PUT", "/api/v1/club-info/order", e.admin, map[string]any{"item_ids": ids}); rec.Code != http.StatusConflict {
			t.Errorf("%s: expected 409, got %d", name, rec.Code)
		}
	}
	if got := strings.Join(labels(decode[[]clubinfo.Item](t, e.do("GET", "/api/v1/club-info", e.member, nil))), ""); got != "CABD" {
		t.Errorf("a refused order should change nothing, got %s", got)
	}
}
