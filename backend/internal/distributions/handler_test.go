package distributions_test

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

	"github.com/jackc/pgx/v5"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/distributions"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
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

func (f *fakeNotifier) waitForMessage(t *testing.T) string {
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
	t.Fatal("timed out waiting for a mutation notification")
	return ""
}

type env struct {
	mux      *http.ServeMux
	resolver testutil.FakeResolver
	tx       pgx.Tx
	notifier *fakeNotifier
	// cash is an asset account payments are made in unless a test names another.
	cash int64
}

func newEnv(t *testing.T) *env {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	authenticator, resolver := testutil.NewTestAuthenticator()
	notifier := &fakeNotifier{}
	mux := http.NewServeMux()
	service := distributions.NewService(distributions.NewRepository(tx))
	distributions.NewHandler(service, authenticator, usersService, notifier).RegisterRoutes(mux)
	e := &env{mux: mux, resolver: resolver, tx: tx, notifier: notifier}
	ctx := context.Background()
	if err := tx.QueryRow(ctx, `INSERT INTO accounts (name, type, position) VALUES ('Caja', 'asset', 1) RETURNING id`).Scan(&e.cash); err != nil {
		t.Fatalf("insert cash account: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO accounts (name, type, position, distribution_default) VALUES ('Repartos', 'income', 2, true)`); err != nil {
		t.Fatalf("insert default account: %v", err)
	}
	return e
}

// person is a user with a token for the given roles.
type person struct {
	testutil.TestUser
	token string
}

// newPerson inserts a user; memberSince, when not empty, gives them a membership
// period starting that day.
func (e *env) newPerson(t *testing.T, memberSince string, roleNames ...string) person {
	t.Helper()
	u := testutil.InsertUser(t, e.tx, "")
	if memberSince != "" {
		if _, err := e.tx.Exec(context.Background(), `INSERT INTO memberships (user_id, start_date) VALUES ($1, $2)`, u.ID, memberSince); err != nil {
			t.Fatalf("insert membership: %v", err)
		}
	}
	return person{TestUser: u, token: testutil.TokenFor(e.resolver, u.ID, roleNames...)}
}

func (e *env) do(method, path, token string, body any) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	// Taking a payment off says which account the money goes back from; most tests don't care.
	if method == http.MethodPost && strings.HasSuffix(path, "/payment/reversal") {
		m, _ := body.(map[string]any)
		if m == nil {
			m = map[string]any{}
		}
		if _, ok := m["account_id"]; !ok {
			m["account_id"] = e.cash
		}
		body = m
	}
	if body != nil {
		b, _ := json.Marshal(body)
		if method == http.MethodPost && strings.HasSuffix(path, "/payments") {
			var m map[string]any
			if json.Unmarshal(b, &m) == nil {
				if _, ok := m["account_id"]; !ok {
					m["account_id"] = e.cash
					b, _ = json.Marshal(m)
				}
			}
		}
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
	e.mux.ServeHTTP(rec, req)
	return rec
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body httpapi.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body.Error.Code
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
	if code != "" && errorCode(t, rec) != code {
		t.Fatalf("expected error code %q, got %q", code, errorCode(t, rec))
	}
}

// shirt is a request body for a distribution of shirts: free, an extra costing
// 8 up to 2 per person, a size to choose, for whoever was a member on 2026-10-01.
func shirt() map[string]any {
	return map[string]any{
		"name":        "Camiseta",
		"audience":    "all_members",
		"cutoff_date": "2026-10-01",
		"price":       0,
		"extra_price": 8,
		"max_extra":   2,
		"options":     []map[string]any{{"name": "Talla", "values": []string{"S", "M", "L"}}},
	}
}

// newOpenShirt creates the shirt as admin and opens it.
func newOpenShirt(t *testing.T, e *env, admin person) distributions.Distribution {
	t.Helper()
	d := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, shirt()), http.StatusCreated))
	return setStatus(t, e, admin, d.ID, "open")
}

func mustStatus(t *testing.T, rec *httptest.ResponseRecorder, status int) *httptest.ResponseRecorder {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("expected %d, got %d: %s", status, rec.Code, rec.Body.String())
	}
	return rec
}

func setStatus(t *testing.T, e *env, admin person, id int64, status string) distributions.Distribution {
	t.Helper()
	body := shirt()
	body["status"] = status
	rec := mustStatus(t, e.do("PATCH", fmt.Sprintf("/api/v1/distributions/%d", id), admin.token, body), http.StatusOK)
	return decode[distributions.Distribution](t, rec)
}

func unitByID(t *testing.T, e *env, by person, distributionID, id int64) distributions.Unit {
	t.Helper()
	for _, u := range decode[[]distributions.Unit](t, mustStatus(t, e.do("GET", unitsPath(distributionID), by.token, nil), http.StatusOK)) {
		if u.ID == id {
			return u
		}
	}
	t.Fatalf("unit %d not listed", id)
	return distributions.Unit{}
}

func unitsPath(id int64) string { return fmt.Sprintf("/api/v1/distributions/%d/units", id) }

func register(e *env, id int64, by person, body map[string]any) *httptest.ResponseRecorder {
	return e.do("POST", unitsPath(id), by.token, body)
}

func TestCreateIsForAdminAndBoardOnly(t *testing.T) {
	e := newEnv(t)
	member := e.newPerson(t, "2020-01-01", "member")
	board := e.newPerson(t, "2020-01-01", "board")
	expect(t, e.do("POST", "/api/v1/distributions", member.token, shirt()), http.StatusForbidden, "forbidden")

	rec := mustStatus(t, e.do("POST", "/api/v1/distributions", board.token, shirt()), http.StatusCreated)
	d := decode[distributions.Distribution](t, rec)
	if d.Status != distributions.StatusDraft || d.Name != "Camiseta" || d.CreatedByLogin != board.Login {
		t.Errorf("unexpected distribution: %+v", d)
	}
	if len(d.Options) != 1 || d.Options[0].Name != "Talla" || len(d.Options[0].Values) != 3 {
		t.Errorf("unexpected options: %+v", d.Options)
	}
	if msg := e.notifier.waitForMessage(t); !strings.Contains(msg, "Camiseta") || !strings.Contains(msg, board.Login) {
		t.Errorf("unexpected notification: %q", msg)
	}
}

func TestCreateValidation(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	cases := map[string]struct {
		change func(map[string]any)
		code   string
	}{
		"no name":            {func(b map[string]any) { b["name"] = "  " }, "invalid_name"},
		"bad audience":       {func(b map[string]any) { b["audience"] = "nobody" }, "invalid_audience"},
		"all members no day": {func(b map[string]any) { delete(b, "cutoff_date") }, "cutoff_required"},
		"bad date":           {func(b map[string]any) { b["cutoff_date"] = "01/10/2026" }, "invalid_date"},
		"negative price":     {func(b map[string]any) { b["price"] = -1 }, "invalid_price"},
		"cap without extras": {func(b map[string]any) { delete(b, "extra_price") }, "invalid_max_extra"},
		"option without values": {func(b map[string]any) {
			b["options"] = []map[string]any{{"name": "Talla", "values": []string{}}}
		}, "invalid_options"},
		"repeated option": {func(b map[string]any) {
			b["options"] = []map[string]any{{"name": "Talla", "values": []string{"S"}}, {"name": "Talla", "values": []string{"M"}}}
		}, "invalid_options"},
		"repeated value": {func(b map[string]any) {
			b["options"] = []map[string]any{{"name": "Talla", "values": []string{"S", "S"}}}
		}, "invalid_options"},
	}
	for name, c := range cases {
		body := shirt()
		c.change(body)
		rec := e.do("POST", "/api/v1/distributions", admin.token, body)
		if rec.Code != http.StatusBadRequest || errorCode(t, rec) != c.code {
			t.Errorf("%s: expected 400 %s, got %d %s", name, c.code, rec.Code, rec.Body.String())
		}
	}
}

func TestSignupDistributionMayHaveACutoffDateOrNot(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	joinedLater := e.newPerson(t, "2026-10-10", "member") // a member now, not on the cutoff date
	outsider := e.newPerson(t, "", "admin")               // not a member on any day
	for _, withDate := range []bool{false, true} {
		body := map[string]any{"name": "Pegatinas", "audience": "signup", "price": 1}
		if withDate {
			body["cutoff_date"] = "2026-10-01"
		}
		d := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, body), http.StatusCreated))
		if (d.CutoffDate != nil) != withDate {
			t.Errorf("cutoff date %v, expected present=%v", d.CutoffDate, withDate)
		}
		setPath := fmt.Sprintf("/api/v1/distributions/%d", d.ID)
		body["status"] = "open"
		mustStatus(t, e.do("PATCH", setPath, admin.token, body), http.StatusOK)
		// Without a date it is for the members of today; with one, for those of
		// that day. Somebody who was never a member can't have it either way.
		expect(t, register(e, d.ID, outsider, map[string]any{}), http.StatusConflict, "not_eligible")
		rec := register(e, d.ID, joinedLater, map[string]any{})
		if withDate {
			expect(t, rec, http.StatusConflict, "not_eligible")
		} else {
			mustStatus(t, rec, http.StatusCreated)
		}
	}
}

// The pickers offer only members: those who may have the distribution to ask
// for it, and also whoever is a member now to hand it to.
func TestPeoplePickersOfferOnlyMembers(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	material := e.newPerson(t, "2020-01-01", "material", "member")
	both := e.newPerson(t, "2020-01-01", "member")        // a member on the cutoff date and now
	joinedLater := e.newPerson(t, "2026-10-10", "member") // a member now only
	left := e.newPerson(t, "2020-01-01", "member")        // a member on the cutoff date only
	if _, err := e.tx.Exec(context.Background(), `UPDATE memberships SET end_date = '2026-10-05' WHERE user_id = $1`, left.ID); err != nil {
		t.Fatal(err)
	}
	e.newPerson(t, "", "member") // never a member
	d := newOpenShirt(t, e, admin)
	path := fmt.Sprintf("/api/v1/distributions/%d/people", d.ID)
	logins := func(token, purpose string) map[string]bool {
		got := map[string]bool{}
		for _, p := range decode[[]distributions.Person](t, mustStatus(t, e.do("GET", path+"?for="+purpose, token, nil), http.StatusOK)) {
			got[p.Login] = true
		}
		return got
	}

	order := logins(both.token, "order")
	if len(order) != 3 || !order[both.Login] || !order[left.Login] || !order[material.Login] || order[joinedLater.Login] {
		t.Errorf("to ask for it: the members of the cutoff date only, got %v", order)
	}
	pickup := logins(material.token, "pickup")
	if len(pickup) != 4 || !pickup[joinedLater.Login] || !pickup[left.Login] || !pickup[both.Login] || !pickup[material.Login] {
		t.Errorf("to hand it to: those of the cutoff date and of now, got %v", pickup)
	}
	expect(t, e.do("GET", path+"?for=pickup", both.token, nil), http.StatusForbidden, "forbidden")
	// Without a cutoff date, the members of today.
	body := map[string]any{"name": "Pegatinas", "audience": "signup", "price": 1, "status": "open"}
	s := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, body), http.StatusCreated))
	mustStatus(t, e.do("PATCH", fmt.Sprintf("/api/v1/distributions/%d", s.ID), admin.token, body), http.StatusOK)
	now := map[string]bool{}
	for _, p := range decode[[]distributions.Person](t, mustStatus(t, e.do("GET", fmt.Sprintf("/api/v1/distributions/%d/people?for=order", s.ID), both.token, nil), http.StatusOK)) {
		now[p.Login] = true
	}
	if len(now) != 3 || !now[joinedLater.Login] || now[left.Login] {
		t.Errorf("with no cutoff date, the members of today: got %v", now)
	}
}

func TestDraftsAreHiddenFromMembers(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	member := e.newPerson(t, "2020-01-01", "member")
	d := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, shirt()), http.StatusCreated))
	path := fmt.Sprintf("/api/v1/distributions/%d", d.ID)

	expect(t, e.do("GET", path, member.token, nil), http.StatusNotFound, "not_found")
	expect(t, e.do("GET", unitsPath(d.ID), member.token, nil), http.StatusNotFound, "not_found")
	expect(t, register(e, d.ID, member, map[string]any{"choices": map[string]string{"Talla": "M"}}), http.StatusNotFound, "not_found")
	if list := decode[[]distributions.Distribution](t, e.do("GET", "/api/v1/distributions", member.token, nil)); len(list) != 0 {
		t.Errorf("a member saw a draft: %+v", list)
	}
	if list := decode[[]distributions.Distribution](t, e.do("GET", "/api/v1/distributions", admin.token, nil)); len(list) != 1 {
		t.Errorf("admin should see the draft, got %+v", list)
	}

	setStatus(t, e, admin, d.ID, "open")
	got := decode[distributions.Distribution](t, mustStatus(t, e.do("GET", path, member.token, nil), http.StatusOK))
	if got.Eligible == nil || !*got.Eligible {
		t.Errorf("a member since 2020 should be eligible: %+v", got.Eligible)
	}
	stranger := e.newPerson(t, "2026-11-01", "member")
	got = decode[distributions.Distribution](t, mustStatus(t, e.do("GET", path, stranger.token, nil), http.StatusOK))
	if got.Eligible == nil || *got.Eligible {
		t.Errorf("somebody who joined after the cutoff should not be eligible: %+v", got.Eligible)
	}
}

func TestStatusMovesOneStepAtATime(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	d := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, shirt()), http.StatusCreated))
	path := fmt.Sprintf("/api/v1/distributions/%d", d.ID)

	body := shirt()
	body["status"] = "closed"
	expect(t, e.do("PATCH", path, admin.token, body), http.StatusConflict, "invalid_status_transition")
	body["status"] = "later"
	expect(t, e.do("PATCH", path, admin.token, body), http.StatusBadRequest, "invalid_status")
	for _, status := range []string{"open", "closed", "open", "closed", "done", "closed"} {
		if got := setStatus(t, e, admin, d.ID, status); string(got.Status) != status {
			t.Fatalf("expected %s, got %s", status, got.Status)
		}
	}
}

// Units are handed out in any state but draft — the order often arrives before
// the asking has ended, and somebody may come for theirs once it is done.
// Everybody's own unit of a distribution for all members is made when it
// opens, so it can be handed out at once.
func TestUnitsAreHandedOutInEveryStateButDraft(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	member := e.newPerson(t, "2020-01-01", "member")
	material := e.newPerson(t, "2020-01-01", "material", "member")

	plain := map[string]any{"name": "Taza", "audience": "all_members", "cutoff_date": "2026-10-01", "price": 3}
	d := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, plain), http.StatusCreated))
	path := fmt.Sprintf("/api/v1/distributions/%d", d.ID)
	patch := func(status string) *httptest.ResponseRecorder {
		b := map[string]any{}
		for k, v := range plain {
			b[k] = v
		}
		b["status"] = status
		return e.do("PATCH", path, admin.token, b)
	}
	// No shortcut for a distribution with nothing to ask: it moves a step at a time.
	expect(t, patch("done"), http.StatusConflict, "invalid_status_transition")
	mustStatus(t, patch("open"), http.StatusOK)

	unit := unitFor(t, e, admin, d.ID, member.Login)
	pickup := func() *httptest.ResponseRecorder {
		return e.do("POST", path+"/pickups", material.token, map[string]any{"unit_ids": []int64{unit.ID}, "picked_up_by_login": member.Login})
	}
	undo := func() {
		mustStatus(t, e.do("DELETE", fmt.Sprintf("%s/units/%d/pickup", path, unit.ID), material.token, nil), http.StatusNoContent)
	}
	mustStatus(t, pickup(), http.StatusNoContent)
	undo()
	mustStatus(t, patch("closed"), http.StatusOK)
	mustStatus(t, pickup(), http.StatusNoContent)
	undo()
	mustStatus(t, patch("done"), http.StatusOK)
	mustStatus(t, pickup(), http.StatusNoContent)
	undo()
	for _, status := range []string{"closed", "open", "draft"} {
		mustStatus(t, patch(status), http.StatusOK)
	}
	// Nothing can be handed out of a draft (it has no units, either).
	expect(t, pickup(), http.StatusConflict, "not_deliverable")
}

func TestRegisterOwnUnitAndForSomeoneElse(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	parent := e.newPerson(t, "2020-01-01", "member")
	child := e.newPerson(t, "2024-01-01", "member")
	d := newOpenShirt(t, e, admin)
	e.notifier.waitForMessage(t) // creation
	e.notifier.waitForMessage(t) // opening

	rec := mustStatus(t, register(e, d.ID, parent, map[string]any{"choices": map[string]string{"Talla": "L"}}), http.StatusCreated)
	own := decode[distributions.Unit](t, rec)
	if own.BeneficiaryLogin != parent.Login || deref(own.RegisteredByLogin) != parent.Login || own.IsExtra || own.Price != 0 || own.Choices["Talla"] != "L" {
		t.Errorf("unexpected own unit: %+v", own)
	}
	if msg := e.notifier.waitForMessage(t); !strings.Contains(msg, parent.Login+" ha pedido") || strings.Contains(msg, " para ") {
		t.Errorf("unexpected notification: %q", msg)
	}

	// The parent signs the child up: it is for the child, and says who did it.
	rec = mustStatus(t, register(e, d.ID, parent, map[string]any{"beneficiary_login": child.Login, "choices": map[string]string{"Talla": "S"}}), http.StatusCreated)
	forChild := decode[distributions.Unit](t, rec)
	if forChild.BeneficiaryLogin != child.Login || deref(forChild.RegisteredByLogin) != parent.Login || forChild.RegisteredAt.IsZero() {
		t.Errorf("unexpected unit for the child: %+v", forChild)
	}
	if msg := e.notifier.waitForMessage(t); !strings.Contains(msg, parent.Login) || !strings.Contains(msg, "para "+child.Login) {
		t.Errorf("notification should name both: %q", msg)
	}

	expect(t, register(e, d.ID, parent, map[string]any{"choices": map[string]string{"Talla": "M"}}), http.StatusConflict, "already_registered")
	expect(t, register(e, d.ID, parent, map[string]any{"beneficiary_login": "nobody.here", "choices": map[string]string{"Talla": "M"}}), http.StatusNotFound, "not_found")
}

func TestRegisterChecksChoicesEligibilityAndState(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	member := e.newPerson(t, "2020-01-01", "member")
	late := e.newPerson(t, "2026-11-01", "member")
	former := e.newPerson(t, "2018-01-01", "member")
	if _, err := e.tx.Exec(context.Background(), `UPDATE memberships SET end_date = '2025-12-31' WHERE user_id = $1`, former.ID); err != nil {
		t.Fatal(err)
	}
	created := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, shirt()), http.StatusCreated))
	id := created.ID

	// Still a draft: nobody can ask for it yet, not even the admin.
	expect(t, register(e, id, admin, map[string]any{"choices": map[string]string{"Talla": "M"}}), http.StatusConflict, "not_open")
	setStatus(t, e, admin, id, "open")

	for name, choices := range map[string]map[string]string{
		"missing":      {},
		"unknown":      {"Talla": "M", "Color": "rojo"},
		"wrong value":  {"Talla": "XXL"},
		"wrong option": {"Size": "M"},
	} {
		if rec := register(e, id, member, map[string]any{"choices": choices}); rec.Code != http.StatusBadRequest || errorCode(t, rec) != "invalid_choices" {
			t.Errorf("%s: expected 400 invalid_choices, got %d %s", name, rec.Code, rec.Body.String())
		}
	}
	expect(t, register(e, id, late, map[string]any{"choices": map[string]string{"Talla": "M"}}), http.StatusConflict, "not_eligible")
	expect(t, register(e, id, former, map[string]any{"choices": map[string]string{"Talla": "M"}}), http.StatusConflict, "not_eligible") // left before the cutoff day
	expect(t, register(e, id, member, map[string]any{"beneficiary_login": late.Login, "choices": map[string]string{"Talla": "M"}}), http.StatusConflict, "not_eligible")
	// Somebody who isn't eligible can't ask for another either; staff can.
	expect(t, register(e, id, late, map[string]any{"beneficiary_login": member.Login, "choices": map[string]string{"Talla": "M"}, "is_extra": true}), http.StatusConflict, "not_eligible")

	setStatus(t, e, admin, id, "closed")
	board := e.newPerson(t, "2020-01-01", "member", "board")
	material := e.newPerson(t, "", "material", "member")
	expect(t, register(e, id, member, map[string]any{"choices": map[string]string{"Talla": "M"}}), http.StatusConflict, "not_open")
	// Once it is closed members cannot ask, but staff (admin, board, material) can: orders are
	// often fixed once the asking is over.
	mustStatus(t, register(e, id, admin, map[string]any{"beneficiary_login": member.Login, "choices": map[string]string{"Talla": "M"}}), http.StatusCreated)
	setStatus(t, e, admin, id, "done")
	expect(t, register(e, id, member, map[string]any{"choices": map[string]string{"Talla": "S"}, "is_extra": true}), http.StatusConflict, "not_open")
	mustStatus(t, register(e, id, material, map[string]any{"beneficiary_login": member.Login, "choices": map[string]string{"Talla": "S"}, "is_extra": true}), http.StatusCreated)
	mustStatus(t, register(e, id, board, map[string]any{"beneficiary_login": member.Login, "choices": map[string]string{"Talla": "S"}, "is_extra": true}), http.StatusCreated)
	// Still not for somebody who is not eligible: that is about the person, not the state.
	expect(t, register(e, id, material, map[string]any{"beneficiary_login": late.Login, "choices": map[string]string{"Talla": "M"}}), http.StatusConflict, "not_eligible")
}

// Staff (admin, board and material) can change and remove orders whatever the state of the
// distribution (but a draft); members only while it is open.
func TestStaffChangeOrdersWhateverTheState(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	material := e.newPerson(t, "2020-01-01", "member", "material")
	board := e.newPerson(t, "2020-01-01", "member", "board")
	member := e.newPerson(t, "2020-01-01", "member")
	other := e.newPerson(t, "2020-01-01", "member")
	d := newOpenShirt(t, e, admin)

	unit := decode[distributions.Unit](t, mustStatus(t, register(e, d.ID, member, map[string]any{"choices": map[string]string{"Talla": "M"}}), http.StatusCreated))
	// Everybody's own unit is there already and cannot be removed; extras can.
	extra := func(by person) distributions.Unit {
		return decode[distributions.Unit](t, mustStatus(t, register(e, d.ID, by, map[string]any{"is_extra": true, "choices": map[string]string{"Talla": "S"}}), http.StatusCreated))
	}
	memberExtra, otherExtra := extra(member), extra(other)
	path := func(u distributions.Unit) string { return fmt.Sprintf("%s/%d", unitsPath(d.ID), u.ID) }
	choose := func(by person, u distributions.Unit, size string) *httptest.ResponseRecorder {
		return e.do("PATCH", path(u), by.token, map[string]any{"choices": map[string]string{"Talla": size}})
	}

	for _, status := range []string{"closed", "done"} {
		setStatus(t, e, admin, d.ID, status)
		expect(t, choose(member, unit, "L"), http.StatusConflict, "not_open")
		mustStatus(t, choose(board, unit, "L"), http.StatusOK)
		mustStatus(t, choose(material, unit, "L"), http.StatusOK)
		mustStatus(t, choose(admin, unit, "S"), http.StatusOK)
		setStatus(t, e, admin, d.ID, "closed")
	}

	setStatus(t, e, admin, d.ID, "done")
	expect(t, e.do("DELETE", path(otherExtra), member.token, nil), http.StatusForbidden, "forbidden")
	expect(t, e.do("DELETE", path(memberExtra), member.token, nil), http.StatusConflict, "not_open")
	mustStatus(t, e.do("DELETE", path(memberExtra), material.token, nil), http.StatusNoContent)
	mustStatus(t, e.do("DELETE", path(otherExtra), board.token, nil), http.StatusNoContent)

	// What was handed out stays final even for them, in any state.
	setStatus(t, e, admin, d.ID, "closed")
	setStatus(t, e, admin, d.ID, "open")
	again := extra(member)
	mustStatus(t, e.do("POST", fmt.Sprintf("/api/v1/distributions/%d/pickups", d.ID), material.token,
		map[string]any{"unit_ids": []int64{again.ID}, "picked_up_by_login": member.Login}), http.StatusNoContent)
	setStatus(t, e, admin, d.ID, "closed")
	expect(t, choose(material, again, "L"), http.StatusConflict, "unit_locked")
	expect(t, choose(board, again, "L"), http.StatusConflict, "unit_locked")
	expect(t, choose(admin, again, "L"), http.StatusConflict, "unit_locked")
}

func TestDeadlineStopsMembersButNotStaff(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	member := e.newPerson(t, "2020-01-01", "member")
	other := e.newPerson(t, "2020-01-01", "member")
	body := shirt()
	body["closes_at"] = time.Now().Add(-time.Hour).Format(time.RFC3339)
	body["status"] = "open"
	d := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, body), http.StatusCreated))
	mustStatus(t, e.do("PATCH", fmt.Sprintf("/api/v1/distributions/%d", d.ID), admin.token, body), http.StatusOK)

	expect(t, register(e, d.ID, member, map[string]any{"choices": map[string]string{"Talla": "M"}}), http.StatusConflict, "not_open")
	mustStatus(t, register(e, d.ID, admin, map[string]any{"beneficiary_login": other.Login, "choices": map[string]string{"Talla": "M"}}), http.StatusCreated)
}

func TestExtrasPriceCapAndWhoAddedThem(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	member := e.newPerson(t, "2020-01-01", "member")
	friend := e.newPerson(t, "2020-01-01", "member")
	d := newOpenShirt(t, e, admin)
	extra := func(by person, forLogin string) *httptest.ResponseRecorder {
		body := map[string]any{"is_extra": true, "choices": map[string]string{"Talla": "M"}}
		if forLogin != "" {
			body["beneficiary_login"] = forLogin
		}
		return register(e, d.ID, by, body)
	}

	// An extra doesn't need the own unit first, costs the extra price, and
	// the cap counts them by who they are for, whoever entered them.
	first := decode[distributions.Unit](t, mustStatus(t, extra(member, ""), http.StatusCreated))
	if !first.IsExtra || first.Price != 8 {
		t.Errorf("unexpected extra: %+v", first)
	}
	second := decode[distributions.Unit](t, mustStatus(t, extra(friend, member.Login), http.StatusCreated))
	if second.BeneficiaryLogin != member.Login || deref(second.RegisteredByLogin) != friend.Login {
		t.Errorf("an extra by a friend should name both: %+v", second)
	}
	expect(t, extra(member, ""), http.StatusConflict, "too_many_extras")
	expect(t, extra(friend, member.Login), http.StatusConflict, "too_many_extras")
	mustStatus(t, extra(friend, ""), http.StatusCreated)

	// A price change afterwards doesn't touch what was already asked for.
	body := shirt()
	body["extra_price"] = 20
	body["status"] = "open"
	mustStatus(t, e.do("PATCH", fmt.Sprintf("/api/v1/distributions/%d", d.ID), admin.token, body), http.StatusOK)
	units := decode[[]distributions.Unit](t, mustStatus(t, e.do("GET", unitsPath(d.ID), admin.token, nil), http.StatusOK))
	for _, u := range units {
		if u.IsExtra && u.Price != 8 {
			t.Errorf("a price changed afterwards: %+v", u)
		}
	}
}

func TestExtrasNotAllowedWithoutExtraPrice(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	member := e.newPerson(t, "2020-01-01", "member")
	body := map[string]any{"name": "Taza", "audience": "all_members", "cutoff_date": "2026-10-01", "price": 3, "status": "open"}
	d := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, body), http.StatusCreated))
	mustStatus(t, e.do("PATCH", fmt.Sprintf("/api/v1/distributions/%d", d.ID), admin.token, body), http.StatusOK)

	expect(t, register(e, d.ID, member, map[string]any{"is_extra": true}), http.StatusConflict, "extras_not_allowed")
	// Nothing to choose, so there is nothing to ask for: the unit is already there.
	expect(t, register(e, d.ID, member, map[string]any{}), http.StatusConflict, "already_registered")
	units := decode[[]distributions.Unit](t, mustStatus(t, e.do("GET", unitsPath(d.ID), member.token, nil), http.StatusOK))
	if len(units) != 1 || units[0].Price != 3 || len(units[0].Choices) != 0 || units[0].BeneficiaryLogin != member.Login {
		t.Errorf("unexpected units: %+v", units)
	}
}

func TestUnitsAreVisibleToThoseInvolvedAndStaff(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	material := e.newPerson(t, "2020-01-01", "material", "member")
	parent := e.newPerson(t, "2020-01-01", "member")
	child := e.newPerson(t, "2024-01-01", "member")
	outsider := e.newPerson(t, "2020-01-01", "member")
	d := newOpenShirt(t, e, admin)
	choices := map[string]string{"Talla": "M"}
	mustStatus(t, register(e, d.ID, parent, map[string]any{"beneficiary_login": child.Login, "choices": choices}), http.StatusCreated)
	mustStatus(t, register(e, d.ID, outsider, map[string]any{"choices": choices}), http.StatusCreated)

	count := func(p person) int {
		return len(decode[[]distributions.Unit](t, mustStatus(t, e.do("GET", unitsPath(d.ID), p.token, nil), http.StatusOK)))
	}
	// The child sees it was entered for them, whoever entered it sees it too.
	// (Everybody who is a member on the cutoff day also has their own unit,
	// which is what the parent's second one and the staff's four are.)
	if count(child) != 1 || count(parent) != 2 || count(outsider) != 1 || count(admin) != 4 || count(material) != 4 {
		t.Errorf("unexpected visibility: child %d parent %d outsider %d admin %d material %d",
			count(child), count(parent), count(outsider), count(admin), count(material))
	}
}

func TestChangingAndRemovingAUnit(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	parent := e.newPerson(t, "2020-01-01", "member")
	child := e.newPerson(t, "2024-01-01", "member")
	stranger := e.newPerson(t, "2020-01-01", "member")
	d := newOpenShirt(t, e, admin)
	unit := decode[distributions.Unit](t, mustStatus(t, register(e, d.ID, parent, map[string]any{"beneficiary_login": child.Login, "choices": map[string]string{"Talla": "M"}}), http.StatusCreated))
	path := fmt.Sprintf("%s/%d", unitsPath(d.ID), unit.ID)

	expect(t, e.do("PATCH", path, stranger.token, map[string]any{"choices": map[string]string{"Talla": "S"}}), http.StatusForbidden, "forbidden")
	expect(t, e.do("DELETE", path, stranger.token, nil), http.StatusForbidden, "forbidden")
	expect(t, e.do("PATCH", path, parent.token, map[string]any{"choices": map[string]string{"Talla": "XXL"}}), http.StatusBadRequest, "invalid_choices")

	// The child (whom it is for) can change it; who entered it stays the parent.
	changed := decode[distributions.Unit](t, mustStatus(t, e.do("PATCH", path, child.token, map[string]any{"choices": map[string]string{"Talla": "S"}}), http.StatusOK))
	if changed.Choices["Talla"] != "S" || deref(changed.RegisteredByLogin) != parent.Login || !changed.RegisteredAt.Equal(unit.RegisteredAt) {
		t.Errorf("unexpected unit after the change: %+v", changed)
	}

	// Once paid, only staff change it; once collected, nobody.
	mustStatus(t, e.do("POST", fmt.Sprintf("/api/v1/distributions/%d/payments", d.ID), admin.token, map[string]any{"unit_ids": []int64{unit.ID}}), http.StatusNoContent)
	expect(t, e.do("PATCH", path, parent.token, map[string]any{"choices": map[string]string{"Talla": "L"}}), http.StatusConflict, "unit_locked")
	mustStatus(t, e.do("PATCH", path, admin.token, map[string]any{"choices": map[string]string{"Talla": "L"}}), http.StatusOK)

	// The own unit of a distribution for all members can't be removed (it would
	// just be made again); an extra can.
	expect(t, e.do("DELETE", path, admin.token, nil), http.StatusConflict, "automatic_unit")
	extra := decode[distributions.Unit](t, mustStatus(t, register(e, d.ID, admin, map[string]any{"beneficiary_login": child.Login, "is_extra": true, "choices": map[string]string{"Talla": "M"}}), http.StatusCreated))
	extraPath := fmt.Sprintf("%s/%d", unitsPath(d.ID), extra.ID)
	mustStatus(t, e.do("DELETE", extraPath, admin.token, nil), http.StatusNoContent)
	expect(t, e.do("GET", extraPath, admin.token, nil), http.StatusMethodNotAllowed, "")
	expect(t, e.do("DELETE", extraPath, admin.token, nil), http.StatusNotFound, "not_found")
}

func TestOrderSummary(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	board := e.newPerson(t, "2020-01-01", "board")
	members := []person{e.newPerson(t, "2020-01-01", "member"), e.newPerson(t, "2020-01-01", "member"), e.newPerson(t, "2020-01-01", "member")}
	e.newPerson(t, "2026-11-01", "member") // not a member on the cutoff day
	d := newOpenShirt(t, e, admin)
	for i, size := range []string{"M", "M", "L"} {
		mustStatus(t, register(e, d.ID, members[i], map[string]any{"choices": map[string]string{"Talla": size}}), http.StatusCreated)
	}
	mustStatus(t, register(e, d.ID, members[0], map[string]any{"is_extra": true, "choices": map[string]string{"Talla": "S"}}), http.StatusCreated)

	path := fmt.Sprintf("/api/v1/distributions/%d/order", d.ID)
	expect(t, e.do("GET", path, members[0].token, nil), http.StatusForbidden, "forbidden")
	order := decode[distributions.Order](t, mustStatus(t, e.do("GET", path, board.token, nil), http.StatusOK))
	got := map[string]int{}
	for _, l := range order.Lines {
		if len(l.Choices) == 0 {
			got["unchosen"] = l.Count
		} else {
			got[l.Choices["Talla"]] = l.Count
		}
	}
	// Everybody who was a member on the cutoff day has their own unit without
	// asking: the board member never chose, so theirs adds up as unchosen. The
	// admin has no membership and the late one wasn't a member that day, so
	// neither has one.
	if order.TotalUnits != 5 || order.TotalAmount != 8 || got["M"] != 2 || got["L"] != 1 || got["S"] != 1 || got["unchosen"] != 1 {
		t.Errorf("unexpected order: %+v", order)
	}
}

// While a distribution for all members is still a draft it has no units, but
// what has to be ordered is already known: one for each member on the cutoff
// date. The summary says so, and says it is provisional.
func TestOrderOfADraftForAllMembersCountsEveryoneWhoQualifies(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	board := e.newPerson(t, "2020-01-01", "board")
	e.newPerson(t, "2020-01-01", "member")
	e.newPerson(t, "2021-05-01", "member")
	e.newPerson(t, "2026-10-10", "member") // joined after the cutoff date
	body := shirt()
	body["price"] = 5
	d := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, body), http.StatusCreated))
	path := fmt.Sprintf("/api/v1/distributions/%d/order", d.ID)

	order := decode[distributions.Order](t, mustStatus(t, e.do("GET", path, board.token, nil), http.StatusOK))
	if !order.Provisional || order.TotalUnits != 3 || order.TotalAmount != 15 || len(order.Lines) != 1 || len(order.Lines[0].Choices) != 0 || order.Lines[0].Count != 3 {
		t.Errorf("a draft should count one for each of the three members: %+v", order)
	}
	// Reading it made nothing: units appear when it opens.
	if units := decode[[]distributions.Unit](t, mustStatus(t, e.do("GET", unitsPath(d.ID), admin.token, nil), http.StatusOK)); len(units) != 0 {
		t.Errorf("a draft has no units, got %+v", units)
	}

	// Open, they are real, and the same numbers are no longer provisional.
	body["status"] = "open"
	mustStatus(t, e.do("PATCH", fmt.Sprintf("/api/v1/distributions/%d", d.ID), admin.token, body), http.StatusOK)
	order = decode[distributions.Order](t, mustStatus(t, e.do("GET", path, board.token, nil), http.StatusOK))
	if order.Provisional || order.TotalUnits != 3 || order.TotalAmount != 15 {
		t.Errorf("once open the numbers should be the same and real: %+v", order)
	}
}

func TestCollectionNeedsDeliveryToHaveBegunAndNamesBothPeople(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	material := e.newPerson(t, "2020-01-01", "material", "member")
	parent := e.newPerson(t, "2020-01-01", "member")
	child := e.newPerson(t, "2024-01-01", "member")
	d := newOpenShirt(t, e, admin)
	own := decode[distributions.Unit](t, mustStatus(t, register(e, d.ID, parent, map[string]any{"choices": map[string]string{"Talla": "M"}}), http.StatusCreated))
	forChild := decode[distributions.Unit](t, mustStatus(t, register(e, d.ID, parent, map[string]any{"beneficiary_login": child.Login, "choices": map[string]string{"Talla": "S"}}), http.StatusCreated))
	pickups := fmt.Sprintf("/api/v1/distributions/%d/pickups", d.ID)
	both := []int64{own.ID, forChild.ID}

	// Open, closed or done, yes (the state it is in isn't what the checks below test).
	setStatus(t, e, admin, d.ID, "closed")
	expect(t, e.do("POST", pickups, parent.token, map[string]any{"unit_ids": both, "picked_up_by_login": parent.Login}), http.StatusForbidden, "forbidden")
	expect(t, e.do("POST", pickups, material.token, map[string]any{"unit_ids": []int64{}, "picked_up_by_login": parent.Login}), http.StatusBadRequest, "no_units")
	expect(t, e.do("POST", pickups, material.token, map[string]any{"unit_ids": both, "picked_up_by_login": "nobody.here"}), http.StatusNotFound, "not_found")
	// Only a member (now, or on the cutoff date) can be who the units are handed to.
	stranger := e.newPerson(t, "", "member")
	expect(t, e.do("POST", pickups, material.token, map[string]any{"unit_ids": both, "picked_up_by_login": stranger.Login}), http.StatusConflict, "invalid_collector")
	expect(t, e.do("POST", pickups, material.token, map[string]any{"unit_ids": []int64{own.ID, 999999}, "picked_up_by_login": parent.Login}), http.StatusNotFound, "not_found")

	// The parent collects both, the child's included: the units say who wrote it
	// down (material) and who got them (the parent), not the beneficiary.
	mustStatus(t, e.do("POST", pickups, material.token, map[string]any{"unit_ids": both, "picked_up_by_login": parent.Login}), http.StatusNoContent)
	units := decode[[]distributions.Unit](t, mustStatus(t, e.do("GET", unitsPath(d.ID), material.token, nil), http.StatusOK))
	for _, u := range units {
		if u.ID != own.ID && u.ID != forChild.ID {
			continue // material's own unit, which nobody has collected
		}
		if u.PickedUpAt == nil || u.PickupRecordedByLogin == nil || *u.PickupRecordedByLogin != material.Login ||
			u.PickedUpByLogin == nil || *u.PickedUpByLogin != parent.Login {
			t.Errorf("unexpected collection on %+v", u)
		}
	}
	if got := decode[distributions.Distribution](t, e.do("GET", fmt.Sprintf("/api/v1/distributions/%d", d.ID), admin.token, nil)); got.PickedUpCount != 2 || got.UnitCount != 3 {
		t.Errorf("unexpected counts: %+v", got)
	}
	expect(t, e.do("POST", pickups, material.token, map[string]any{"unit_ids": []int64{own.ID}, "picked_up_by_login": parent.Login}), http.StatusConflict, "already_picked_up")

	// Undoing one is for the same people.
	undo := fmt.Sprintf("%s/%d/pickup", unitsPath(d.ID), own.ID)
	expect(t, e.do("DELETE", undo, parent.token, nil), http.StatusForbidden, "forbidden")
	mustStatus(t, e.do("DELETE", undo, material.token, nil), http.StatusNoContent)
	units = decode[[]distributions.Unit](t, e.do("GET", unitsPath(d.ID), material.token, nil))
	for _, u := range units {
		if (u.ID == own.ID || u.BeneficiaryLogin == material.Login) != (u.PickedUpAt == nil) {
			t.Errorf("only the undone unit and material's own should be pending: %+v", u)
		}
	}
}

func TestPaymentsAreMarkedByAdminAndBoardOnly(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	board := e.newPerson(t, "2020-01-01", "board")
	material := e.newPerson(t, "2020-01-01", "material", "member")
	member := e.newPerson(t, "2020-01-01", "member")
	d := newOpenShirt(t, e, admin)
	extra := decode[distributions.Unit](t, mustStatus(t, register(e, d.ID, member, map[string]any{"is_extra": true, "choices": map[string]string{"Talla": "M"}}), http.StatusCreated))
	payments := fmt.Sprintf("/api/v1/distributions/%d/payments", d.ID)
	ids := map[string]any{"unit_ids": []int64{extra.ID}}

	expect(t, e.do("POST", payments, material.token, ids), http.StatusForbidden, "forbidden")
	expect(t, e.do("POST", payments, member.token, ids), http.StatusForbidden, "forbidden")
	mustStatus(t, e.do("POST", payments, board.token, ids), http.StatusNoContent)
	expect(t, e.do("POST", payments, admin.token, ids), http.StatusConflict, "already_paid")

	paid := unitByID(t, e, admin, d.ID, extra.ID)
	if paid.PaidAt == nil || paid.PaidMarkedByLogin == nil || *paid.PaidMarkedByLogin != board.Login {
		t.Errorf("unexpected payment on %+v", paid)
	}
	undo := fmt.Sprintf("%s/%d/payment/reversal", unitsPath(d.ID), extra.ID)
	expect(t, e.do("POST", undo, material.token, nil), http.StatusForbidden, "forbidden")
	// The old DELETE is gone: taking a payment off records its reverse, it deletes nothing.
	if rec := e.do("DELETE", strings.TrimSuffix(undo, "/reversal"), admin.token, nil); rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
		t.Errorf("expected DELETE .../payment not to exist, got %d", rec.Code)
	}
	mustStatus(t, e.do("POST", undo, admin.token, nil), http.StatusNoContent)
	if gone := unitByID(t, e, admin, d.ID, extra.ID); gone.PaidAt != nil || gone.PaidMarkedByLogin != nil {
		t.Errorf("payment should be gone: %+v", gone)
	}
}

func TestOptionsAreLockedOnceUnitsExistButTheDistributionCanBeDeleted(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	member := e.newPerson(t, "2020-01-01", "member")
	d := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, shirt()), http.StatusCreated))
	path := fmt.Sprintf("/api/v1/distributions/%d", d.ID)

	// While it is a draft nobody has a unit, so the options can be replaced.
	body := shirt()
	body["options"] = []map[string]any{{"name": "Talla", "values": []string{"M", "L"}}, {"name": "Color", "values": []string{"rojo"}}}
	updated := decode[distributions.Distribution](t, mustStatus(t, e.do("PATCH", path, admin.token, body), http.StatusOK))
	if len(updated.Options) != 2 || updated.Options[1].Name != "Color" {
		t.Errorf("unexpected options: %+v", updated.Options)
	}

	// Opening it gives every member their own unit, and from then on the options
	// are what those units' choices are values of.
	body["status"] = "open"
	mustStatus(t, e.do("PATCH", path, admin.token, body), http.StatusOK)
	mustStatus(t, register(e, d.ID, member, map[string]any{"choices": map[string]string{"Talla": "M", "Color": "rojo"}}), http.StatusCreated)

	// Sending back exactly what is there is fine; a different set is not.
	mustStatus(t, e.do("PATCH", path, admin.token, body), http.StatusOK)
	body["options"] = []map[string]any{{"name": "Talla", "values": []string{"M", "L", "XL"}}, {"name": "Color", "values": []string{"rojo"}}}
	expect(t, e.do("PATCH", path, admin.token, body), http.StatusConflict, "has_units")
	// Leaving them out keeps them, so the rest can still be edited.
	delete(body, "options")
	body["name"] = "Camiseta 2026"
	kept := decode[distributions.Distribution](t, mustStatus(t, e.do("PATCH", path, admin.token, body), http.StatusOK))
	if kept.Name != "Camiseta 2026" || len(kept.Options) != 2 {
		t.Errorf("unexpected distribution: %+v", kept)
	}

	// It has units (everybody's own, made on opening) and can still be deleted,
	// units and all — unless one was paid or collected, which has to be undone
	// first.
	unit := unitFor(t, e, admin, d.ID, member.Login)
	mustStatus(t, e.do("POST", path+"/payments", admin.token, map[string]any{"unit_ids": []int64{unit.ID}}), http.StatusNoContent)
	expect(t, e.do("DELETE", path, admin.token, nil), http.StatusConflict, "has_settled_units")
	mustStatus(t, e.do("POST", fmt.Sprintf("%s/units/%d/payment/reversal", path, unit.ID), admin.token, nil), http.StatusNoContent)
	mustStatus(t, e.do("DELETE", path, admin.token, nil), http.StatusNoContent)
	var left int
	if err := e.tx.QueryRow(context.Background(), `SELECT count(*) FROM distribution_units WHERE distribution_id = $1`, d.ID).Scan(&left); err != nil || left != 0 {
		t.Errorf("the units should go with the distribution: %d left (%v)", left, err)
	}
	found := false
	for i := 0; i < 20 && !found; i++ {
		msg := e.notifier.waitForMessage(t)
		found = strings.Contains(msg, "ha borrado el reparto") && strings.Contains(msg, "unidades")
	}
	if !found {
		t.Error("the notification should say the units went too")
	}

	empty := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, shirt()), http.StatusCreated))
	expect(t, e.do("DELETE", fmt.Sprintf("/api/v1/distributions/%d", empty.ID), member.token, nil), http.StatusForbidden, "forbidden")
	// Board create and edit distributions, but only admin delete them.
	board := e.newPerson(t, "2020-01-01", "board")
	expect(t, e.do("DELETE", fmt.Sprintf("/api/v1/distributions/%d", empty.ID), board.token, nil), http.StatusForbidden, "forbidden")
	mustStatus(t, e.do("DELETE", fmt.Sprintf("/api/v1/distributions/%d", empty.ID), admin.token, nil), http.StatusNoContent)
	expect(t, e.do("GET", fmt.Sprintf("/api/v1/distributions/%d", empty.ID), admin.token, nil), http.StatusNotFound, "not_found")
}

// Everybody who was a member on the cutoff date of a distribution for all
// members has their own unit without asking, with nothing chosen yet.
func TestEveryMemberOnTheCutoffDateGetsTheirOwnUnit(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	ana := e.newPerson(t, "2020-01-01", "member")
	beto := e.newPerson(t, "2020-01-01", "member")
	late := e.newPerson(t, "2026-10-10", "member") // a member today, but not on 2026-10-01
	d := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, shirt()), http.StatusCreated))

	// A draft has nobody yet.
	if units := decode[[]distributions.Unit](t, mustStatus(t, e.do("GET", unitsPath(d.ID), admin.token, nil), http.StatusOK)); len(units) != 0 {
		t.Fatalf("a draft should have no units, got %+v", units)
	}
	setStatus(t, e, admin, d.ID, "open")

	units := decode[[]distributions.Unit](t, mustStatus(t, e.do("GET", unitsPath(d.ID), admin.token, nil), http.StatusOK))
	logins := map[string]distributions.Unit{}
	for _, u := range units {
		logins[u.BeneficiaryLogin] = u
	}
	if len(units) != 2 || logins[ana.Login].ID == 0 || logins[beto.Login].ID == 0 {
		t.Fatalf("expected a unit each for ana and beto only, got %+v", units)
	}
	if u := logins[ana.Login]; len(u.Choices) != 0 || u.RegisteredByLogin != nil || u.IsExtra || u.Price != 0 {
		t.Errorf("an automatic unit starts empty and entered by nobody: %+v", u)
	}
	// They are in their own list without having done anything, and the late
	// member is not in anybody's.
	mine := decode[[]distributions.Unit](t, mustStatus(t, e.do("GET", unitsPath(d.ID), ana.token, nil), http.StatusOK))
	if len(mine) != 1 || mine[0].BeneficiaryLogin != ana.Login {
		t.Errorf("ana should see her own unit: %+v", mine)
	}
	if _, found := logins[late.Login]; found {
		t.Error("somebody who joined after the cutoff date got a unit")
	}

	// A member whose membership counts for the cutoff date is added later on:
	// they appear the next time units are looked at.
	e.newPerson(t, "2021-01-01", "member")
	if all := decode[[]distributions.Unit](t, e.do("GET", unitsPath(d.ID), admin.token, nil)); len(all) != 3 {
		t.Errorf("a later member with a right to it should appear, got %d units", len(all))
	}

	// Closing it keeps what everybody has.
	setStatus(t, e, admin, d.ID, "closed")
	if all := decode[[]distributions.Unit](t, e.do("GET", unitsPath(d.ID), admin.token, nil)); len(all) != 3 {
		t.Errorf("closing should not change the units, got %d", len(all))
	}
}

// A price correction reaches the automatic units nobody has touched, and nothing
// somebody already agreed to or paid.
func TestPriceChangeReachesUntouchedAutomaticUnitsOnly(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	payer := e.newPerson(t, "2020-01-01", "board")
	chooser := e.newPerson(t, "2020-01-01", "member")
	idle := e.newPerson(t, "2020-01-01", "member")
	d := newOpenShirt(t, e, admin) // free, extras at 8
	path := fmt.Sprintf("/api/v1/distributions/%d", d.ID)
	mustStatus(t, register(e, d.ID, chooser, map[string]any{"choices": map[string]string{"Talla": "M"}}), http.StatusCreated)
	extra := decode[distributions.Unit](t, mustStatus(t, register(e, d.ID, chooser, map[string]any{"is_extra": true, "choices": map[string]string{"Talla": "S"}}), http.StatusCreated))
	mustStatus(t, e.do("POST", path+"/payments", admin.token, map[string]any{"unit_ids": []int64{unitFor(t, e, admin, d.ID, payer.Login).ID}}), http.StatusNoContent)

	price := func(login string) float64 { return unitFor(t, e, admin, d.ID, login).Price }
	body := shirt()
	body["price"] = 5
	mustStatus(t, e.do("PATCH", path, admin.token, body), http.StatusOK) // still open
	if price(idle.Login) != 5 || price(chooser.Login) != 0 || price(payer.Login) != 0 {
		t.Errorf("only the untouched unit should follow the price: idle %v, chosen %v, paid %v",
			price(idle.Login), price(chooser.Login), price(payer.Login))
	}
	if unitByID(t, e, admin, d.ID, extra.ID).Price != 8 {
		t.Error("an extra keeps its own price")
	}
}

// Going back to a draft starts it over: every unit goes, everybody's own (made
// automatically), the extras and the sign-ups, with what was chosen on them —
// unless one is paid or collected — and opening again makes the automatic ones
// anew.
func TestBackToDraftDeletesEveryUnit(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	chooser := e.newPerson(t, "2020-01-01", "member")
	idle := e.newPerson(t, "2020-01-01", "member")
	d := newOpenShirt(t, e, admin) // free, extras at 8, size to choose
	path := fmt.Sprintf("/api/v1/distributions/%d", d.ID)
	mustStatus(t, register(e, d.ID, chooser, map[string]any{"choices": map[string]string{"Talla": "M"}}), http.StatusCreated)
	mustStatus(t, register(e, d.ID, chooser, map[string]any{"is_extra": true, "choices": map[string]string{"Talla": "S"}}), http.StatusCreated)
	back := func() *httptest.ResponseRecorder {
		body := shirt()
		body["status"] = "draft"
		return e.do("PATCH", path, admin.token, body)
	}
	count := func() int {
		return len(decode[[]distributions.Unit](t, mustStatus(t, e.do("GET", unitsPath(d.ID), admin.token, nil), http.StatusOK)))
	}
	if count() != 3 {
		t.Fatalf("expected an own unit each and an extra, got %d", count())
	}

	// A paid one stops it, and nothing is lost.
	idleUnit := unitFor(t, e, admin, d.ID, idle.Login)
	mustStatus(t, e.do("POST", path+"/payments", admin.token, map[string]any{"unit_ids": []int64{idleUnit.ID}}), http.StatusNoContent)
	expect(t, back(), http.StatusConflict, "has_settled_units")
	if count() != 3 {
		t.Errorf("a refused change must leave the units alone, got %d", count())
	}
	mustStatus(t, e.do("POST", fmt.Sprintf("%s/units/%d/payment/reversal", path, idleUnit.ID), admin.token, nil), http.StatusNoContent)

	// Without marks, back to a draft deletes all of them, the extra included.
	got := decode[distributions.Distribution](t, mustStatus(t, back(), http.StatusOK))
	if got.Status != distributions.StatusDraft || got.UnitCount != 0 || count() != 0 {
		t.Errorf("expected a draft with no units at all, got %+v (%d units)", got, count())
	}

	// With nothing left, the options can be replaced, and opening again makes
	// everybody's own unit anew, empty.
	body := shirt()
	body["options"] = []map[string]any{{"name": "Talla", "values": []string{"M", "L"}}}
	body["status"] = "open"
	mustStatus(t, e.do("PATCH", path, admin.token, body), http.StatusOK)
	if count() != 2 {
		t.Errorf("opening again should make an own unit each, got %d", count())
	}
	if u := unitFor(t, e, admin, d.ID, chooser.Login); len(u.Choices) != 0 || u.RegisteredByLogin != nil {
		t.Errorf("what was chosen went with the old unit: %+v", u)
	}
}

// The same for a distribution people sign up for: back to a draft takes every
// sign-up with it.
func TestBackToDraftDeletesTheSignUpsToo(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	member := e.newPerson(t, "2020-01-01", "member")
	body := map[string]any{"name": "Pegatinas", "audience": "signup", "price": 1, "extra_price": 2, "status": "open"}
	d := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, body), http.StatusCreated))
	path := fmt.Sprintf("/api/v1/distributions/%d", d.ID)
	mustStatus(t, e.do("PATCH", path, admin.token, body), http.StatusOK)
	mustStatus(t, register(e, d.ID, member, map[string]any{}), http.StatusCreated)
	mustStatus(t, register(e, d.ID, member, map[string]any{"is_extra": true}), http.StatusCreated)
	body["status"] = "draft"
	got := decode[distributions.Distribution](t, mustStatus(t, e.do("PATCH", path, admin.token, body), http.StatusOK))
	if got.UnitCount != 0 {
		t.Errorf("a draft keeps no sign-ups, got %d units", got.UnitCount)
	}
}

// Turning a distribution for all members into one people sign up for takes the
// automatic units nobody touched with it; whoever did choose has asked for it, so
// that stays — and a paid or collected one stops the change.
func TestSwitchingToSignupDeletesTheUntouchedAutomaticUnits(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	chooser := e.newPerson(t, "2020-01-01", "member")
	idle := e.newPerson(t, "2020-01-01", "member")
	paid := e.newPerson(t, "2020-01-01", "member")
	d := newOpenShirt(t, e, admin)
	path := fmt.Sprintf("/api/v1/distributions/%d", d.ID)
	mustStatus(t, register(e, d.ID, chooser, map[string]any{"choices": map[string]string{"Talla": "M"}}), http.StatusCreated)
	extra := decode[distributions.Unit](t, mustStatus(t, register(e, d.ID, chooser, map[string]any{"is_extra": true, "choices": map[string]string{"Talla": "S"}}), http.StatusCreated))
	toSignup := func() *httptest.ResponseRecorder {
		body := shirt()
		body["audience"] = "signup"
		body["status"] = "open"
		return e.do("PATCH", path, admin.token, body)
	}
	logins := func() map[string]bool {
		got := map[string]bool{}
		for _, u := range decode[[]distributions.Unit](t, mustStatus(t, e.do("GET", unitsPath(d.ID), admin.token, nil), http.StatusOK)) {
			got[fmt.Sprintf("%s/%v", u.BeneficiaryLogin, u.IsExtra)] = true
		}
		return got
	}

	// One untouched unit is paid: the change is refused and nothing is lost.
	paidUnit := unitFor(t, e, admin, d.ID, paid.Login)
	mustStatus(t, e.do("POST", path+"/payments", admin.token, map[string]any{"unit_ids": []int64{paidUnit.ID}}), http.StatusNoContent)
	expect(t, toSignup(), http.StatusConflict, "has_settled_units")
	if len(logins()) != 4 {
		t.Errorf("a refused change must leave the units alone, got %v", logins())
	}
	mustStatus(t, e.do("POST", fmt.Sprintf("%s/units/%d/payment/reversal", path, paidUnit.ID), admin.token, nil), http.StatusNoContent)

	// Without marks: the untouched ones go; the chosen one and the extra stay.
	got := decode[distributions.Distribution](t, mustStatus(t, toSignup(), http.StatusOK))
	if got.Audience != distributions.AudienceSignup {
		t.Fatalf("expected signup, got %s", got.Audience)
	}
	left := logins()
	if len(left) != 2 || !left[chooser.Login+"/false"] || !left[chooser.Login+"/true"] || left[idle.Login+"/false"] || left[paid.Login+"/false"] {
		t.Errorf("only what the chooser asked for should be left, got %v", left)
	}
	if unitByID(t, e, admin, d.ID, extra.ID).ID != extra.ID {
		t.Error("the extra should have stayed")
	}
	// It is a distribution people sign up for now: nothing is made on its own.
	if len(logins()) != 2 {
		t.Errorf("a signup distribution makes no automatic units, got %v", logins())
	}
}

// Choosing what the automatic unit should be takes whoever does it as who
// entered it, and nobody is left unattributed for good.
func TestChoosingForAnAutomaticUnitSaysWhoDidIt(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	parent := e.newPerson(t, "2020-01-01", "member")
	child := e.newPerson(t, "2024-01-01", "member")
	self := e.newPerson(t, "2020-01-01", "member")
	d := newOpenShirt(t, e, admin)
	sizeM := map[string]string{"Talla": "M"}

	// The parent chooses the child's size: the unit already exists, so asking for
	// it fills it in, as entered by the parent at that moment.
	filled := decode[distributions.Unit](t, mustStatus(t, register(e, d.ID, parent, map[string]any{"beneficiary_login": child.Login, "choices": sizeM}), http.StatusCreated))
	if filled.BeneficiaryLogin != child.Login || deref(filled.RegisteredByLogin) != parent.Login || filled.Choices["Talla"] != "M" {
		t.Errorf("unexpected unit: %+v", filled)
	}
	// There is only one own unit: a second time is already_registered, and the
	// child can still change it themselves without taking the credit.
	expect(t, register(e, d.ID, parent, map[string]any{"beneficiary_login": child.Login, "choices": sizeM}), http.StatusConflict, "already_registered")
	path := fmt.Sprintf("%s/%d", unitsPath(d.ID), filled.ID)
	changed := decode[distributions.Unit](t, mustStatus(t, e.do("PATCH", path, child.token, map[string]any{"choices": map[string]string{"Talla": "S"}}), http.StatusOK))
	if deref(changed.RegisteredByLogin) != parent.Login || changed.Choices["Talla"] != "S" {
		t.Errorf("a later change keeps who entered it: %+v", changed)
	}

	// Choosing one's own, from "Tus pedidos", is the same through PATCH.
	own := unitFor(t, e, self, d.ID, self.Login)
	if own.RegisteredByLogin != nil {
		t.Fatalf("not chosen yet, so entered by nobody: %+v", own)
	}
	ownPath := fmt.Sprintf("%s/%d", unitsPath(d.ID), own.ID)
	chosen := decode[distributions.Unit](t, mustStatus(t, e.do("PATCH", ownPath, self.token, map[string]any{"choices": sizeM}), http.StatusOK))
	if deref(chosen.RegisteredByLogin) != self.Login || chosen.RegisteredAt.IsZero() {
		t.Errorf("choosing takes the person as who entered it: %+v", chosen)
	}
	// Without options to choose there is nothing to attribute.
	expect(t, e.do("PATCH", ownPath, self.token, map[string]any{"choices": map[string]string{"Talla": "XXL"}}), http.StatusBadRequest, "invalid_choices")
}

func unitFor(t *testing.T, e *env, by person, distributionID int64, beneficiary string) distributions.Unit {
	t.Helper()
	for _, u := range decode[[]distributions.Unit](t, mustStatus(t, e.do("GET", unitsPath(distributionID), by.token, nil), http.StatusOK)) {
		if u.BeneficiaryLogin == beneficiary && !u.IsExtra {
			return u
		}
	}
	t.Fatalf("no own unit for %s", beneficiary)
	return distributions.Unit{}
}

// A distribution people sign up for makes no units by itself.
func TestSignupDistributionsMakeNoUnitsOnTheirOwn(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	e.newPerson(t, "2020-01-01", "member")
	body := map[string]any{"name": "Pegatinas", "audience": "signup", "price": 1, "status": "open"}
	d := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, body), http.StatusCreated))
	mustStatus(t, e.do("PATCH", fmt.Sprintf("/api/v1/distributions/%d", d.ID), admin.token, body), http.StatusOK)
	if units := decode[[]distributions.Unit](t, mustStatus(t, e.do("GET", unitsPath(d.ID), admin.token, nil), http.StatusOK)); len(units) != 0 {
		t.Errorf("nobody signed up, so there should be no units: %+v", units)
	}
}

func TestEditNotificationListsWhatChanged(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	d := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, shirt()), http.StatusCreated))
	e.notifier.waitForMessage(t)
	body := shirt()
	body["price"] = 5
	mustStatus(t, e.do("PATCH", fmt.Sprintf("/api/v1/distributions/%d", d.ID), admin.token, body), http.StatusOK)
	if msg := e.notifier.waitForMessage(t); !strings.Contains(msg, "Price: 0 → 5") {
		t.Errorf("unexpected notification: %q", msg)
	}
	// Sending it again unchanged says nothing.
	mustStatus(t, e.do("PATCH", fmt.Sprintf("/api/v1/distributions/%d", d.ID), admin.token, body), http.StatusOK)
	e.notifier.mu.Lock()
	defer e.notifier.mu.Unlock()
	time.Sleep(10 * time.Millisecond)
	if len(e.notifier.sent) != 0 {
		t.Errorf("an unchanged edit should say nothing, got %v", e.notifier.sent)
	}
}

// A distribution's visibility keeps whoever is below the level it names from seeing it, and so
// from doing anything with it: every route answers as if it did not exist.
func TestVisibilityRestrictsWhoSeesAndActs(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	board := e.newPerson(t, "2020-01-01", "member", "board")
	material := e.newPerson(t, "2020-01-01", "member", "material")
	member := e.newPerson(t, "2020-01-01", "member")
	people := map[string]person{"admin": admin, "board": board, "material": material, "member": member}

	// Who sees what: each level includes the ones above it.
	cases := []struct {
		visibility string
		sees       []string
	}{
		{"all", []string{"admin", "board", "material", "member"}},
		{"material", []string{"admin", "board", "material"}},
		{"board", []string{"admin", "board"}},
		{"admin", []string{"admin"}},
	}
	for _, c := range cases {
		body := shirt()
		body["visibility"] = c.visibility
		d := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, body), http.StatusCreated))
		if string(d.Visibility) != c.visibility {
			t.Errorf("created with %s, came back as %s", c.visibility, d.Visibility)
		}
		setStatus(t, e, admin, d.ID, "open")
		path := fmt.Sprintf("/api/v1/distributions/%d", d.ID)
		for name, p := range people {
			sees := false
			for _, s := range c.sees {
				sees = sees || s == name
			}
			status := http.StatusNotFound
			if sees {
				status = http.StatusOK
			}
			label := c.visibility + "/" + name
			if rec := e.do("GET", path, p.token, nil); rec.Code != status {
				t.Errorf("%s: GET distribution expected %d, got %d", label, status, rec.Code)
			}
			if rec := e.do("GET", unitsPath(d.ID), p.token, nil); rec.Code != status {
				t.Errorf("%s: GET units expected %d, got %d", label, status, rec.Code)
			}
			if rec := e.do("GET", path+"/people?for=order", p.token, nil); rec.Code != status {
				t.Errorf("%s: GET people expected %d, got %d", label, status, rec.Code)
			}
			listed := false
			for _, item := range decode[[]distributions.Distribution](t, mustStatus(t, e.do("GET", "/api/v1/distributions", p.token, nil), http.StatusOK)) {
				listed = listed || item.ID == d.ID
			}
			if listed != sees {
				t.Errorf("%s: listed=%v, expected %v", label, listed, sees)
			}
			// The routes of those who hand things out are the same: a 403 for the role, then a 404.
			if name != "member" {
				if rec := e.do("GET", path+"/order", p.token, nil); rec.Code != status {
					t.Errorf("%s: GET order expected %d, got %d", label, status, rec.Code)
				}
			}
		}
		// Acting on it is as much "not there" as looking at it.
		if !(c.visibility == "all") {
			expect(t, register(e, d.ID, member, map[string]any{"choices": map[string]string{"Talla": "M"}}), http.StatusNotFound, "not_found")
		}
		mustStatus(t, register(e, d.ID, admin, map[string]any{"beneficiary_login": member.Login, "choices": map[string]string{"Talla": "M"}}), http.StatusCreated)
	}
}

func TestVisibilityDefaultsToAllAndOnlyAdminChangesIt(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	board := e.newPerson(t, "2020-01-01", "member", "board")
	path := func(d distributions.Distribution) string { return fmt.Sprintf("/api/v1/distributions/%d", d.ID) }

	// Left out, it is "all"; board can create one, but not set anything else.
	d := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", board.token, shirt()), http.StatusCreated))
	if d.Visibility != distributions.VisibilityAll {
		t.Errorf("expected the default all, got %q", d.Visibility)
	}
	body := shirt()
	body["visibility"] = "all"
	mustStatus(t, e.do("POST", "/api/v1/distributions", board.token, body), http.StatusCreated)
	body["visibility"] = "board"
	expect(t, e.do("POST", "/api/v1/distributions", board.token, body), http.StatusForbidden, "forbidden")
	body["visibility"] = "secret"
	expect(t, e.do("POST", "/api/v1/distributions", admin.token, body), http.StatusBadRequest, "invalid_visibility")

	// Editing: a form that sends back what it is is fine, anything else needs admin.
	same := shirt()
	same["visibility"] = "all"
	mustStatus(t, e.do("PATCH", path(d), board.token, same), http.StatusOK)
	mustStatus(t, e.do("PATCH", path(d), board.token, shirt()), http.StatusOK) // left out: kept
	changed := shirt()
	changed["visibility"] = "material"
	expect(t, e.do("PATCH", path(d), board.token, changed), http.StatusForbidden, "forbidden")
	got := decode[distributions.Distribution](t, mustStatus(t, e.do("PATCH", path(d), admin.token, changed), http.StatusOK))
	if got.Visibility != distributions.VisibilityMaterial {
		t.Errorf("admin should change it, got %q", got.Visibility)
	}
	// Of the notifications of everything done above, the one for admin's edit names the change.
	found := false
	for i := 0; i < 20 && !found; i++ {
		msg := e.notifier.waitForMessage(t)
		found = strings.Contains(msg, "Visibility") && strings.Contains(msg, "material")
	}
	if !found {
		t.Error("the notification should say that the visibility changed")
	}
	// Left out in a later edit, it stays.
	got = decode[distributions.Distribution](t, mustStatus(t, e.do("PATCH", path(d), admin.token, shirt()), http.StatusOK))
	if got.Visibility != distributions.VisibilityMaterial {
		t.Errorf("leaving it out should keep it, got %q", got.Visibility)
	}
	changed["visibility"] = "secret"
	expect(t, e.do("PATCH", path(d), admin.token, changed), http.StatusBadRequest, "invalid_visibility")

	// Board can see one for board and edit everything but who sees it; not one for admin.
	changed["visibility"] = "board"
	mustStatus(t, e.do("PATCH", path(d), admin.token, changed), http.StatusOK)
	mustStatus(t, e.do("PATCH", path(d), board.token, shirt()), http.StatusOK)
	changed["visibility"] = "admin"
	mustStatus(t, e.do("PATCH", path(d), admin.token, changed), http.StatusOK)
	expect(t, e.do("PATCH", path(d), board.token, shirt()), http.StatusNotFound, "not_found")
}

// Visibility does not make a draft visible: drafts are for admin and board whatever it says.
func TestVisibilityDoesNotOpenUpADraft(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	material := e.newPerson(t, "2020-01-01", "member", "material")
	body := shirt()
	body["visibility"] = "material"
	d := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, body), http.StatusCreated))
	expect(t, e.do("GET", fmt.Sprintf("/api/v1/distributions/%d", d.ID), material.token, nil), http.StatusNotFound, "not_found")
	setStatus(t, e, admin, d.ID, "open")
	mustStatus(t, e.do("GET", fmt.Sprintf("/api/v1/distributions/%d", d.ID), material.token, nil), http.StatusOK)
}
