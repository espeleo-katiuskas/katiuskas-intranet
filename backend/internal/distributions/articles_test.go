package distributions_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/distributions"
)

// lottery is a request body for the Christmas lottery: two articles asked for in
// any quantity, with a limit on how many there are of each, for whoever signs up.
func lottery() map[string]any {
	return map[string]any{
		"name":     "Lotería de Navidad",
		"audience": "signup",
		"price":    0,
		"articles": []map[string]any{
			{"name": "Papeleta", "price": 5, "max_total": 200},
			{"name": "Décimo", "price": 20, "max_total": 1000},
		},
	}
}

func distributionPath(id int64) string { return fmt.Sprintf("/api/v1/distributions/%d", id) }

// newOpenLottery creates the lottery as admin and opens it.
func newOpenLottery(t *testing.T, e *env, admin person) distributions.Distribution {
	t.Helper()
	d := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, lottery()), http.StatusCreated))
	body := lottery()
	body["status"] = "open"
	return decode[distributions.Distribution](t, mustStatus(t, e.do("PATCH", distributionPath(d.ID), admin.token, body), http.StatusOK))
}

func ask(e *env, d distributions.Distribution, by person, article int, quantity int) *httptest.ResponseRecorder {
	return register(e, d.ID, by, map[string]any{"article_id": d.Articles[article].ID, "quantity": quantity})
}

func TestArticlesAreCreatedInOrderWithTheirLimits(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	d := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, lottery()), http.StatusCreated))
	if len(d.Articles) != 2 || d.Articles[0].Name != "Papeleta" || d.Articles[0].Price != 5 || *d.Articles[0].MaxTotal != 200 ||
		d.Articles[1].Name != "Décimo" || d.Articles[1].Price != 20 || *d.Articles[1].MaxTotal != 1000 || d.Articles[1].Requested != 0 {
		t.Errorf("unexpected articles: %+v", d.Articles)
	}
	// A distribution without articles still lists none, not null.
	plain := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, shirt()), http.StatusCreated))
	if plain.Articles == nil || len(plain.Articles) != 0 {
		t.Errorf("expected an empty list, got %#v", plain.Articles)
	}
}

func TestArticlesValidation(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	cases := map[string]struct {
		change func(map[string]any)
		code   string
	}{
		"no name":         {func(b map[string]any) { b["articles"] = []map[string]any{{"name": " ", "price": 5}} }, "invalid_articles"},
		"negative price":  {func(b map[string]any) { b["articles"] = []map[string]any{{"name": "A", "price": -1}} }, "invalid_articles"},
		"limit below one": {func(b map[string]any) { b["articles"] = []map[string]any{{"name": "A", "price": 1, "max_total": 0}} }, "invalid_articles"},
		"repeated name": {func(b map[string]any) {
			b["articles"] = []map[string]any{{"name": "A", "price": 1}, {"name": "A", "price": 2}}
		}, "invalid_articles"},
		"for all members": {func(b map[string]any) { b["audience"] = "all_members"; b["cutoff_date"] = "2026-10-01" }, "articles_conflict"},
		"with a price":    {func(b map[string]any) { b["price"] = 3 }, "articles_conflict"},
		"with extras":     {func(b map[string]any) { b["extra_price"] = 3 }, "articles_conflict"},
		"with options":    {func(b map[string]any) { b["options"] = []map[string]any{{"name": "Talla", "values": []string{"S"}}} }, "articles_conflict"},
	}
	for name, c := range cases {
		body := lottery()
		c.change(body)
		rec := e.do("POST", "/api/v1/distributions", admin.token, body)
		if rec.Code != http.StatusBadRequest || errorCode(t, rec) != c.code {
			t.Errorf("%s: expected 400 %s, got %d %s", name, c.code, rec.Code, rec.Body.String())
		}
	}
}

func TestAskingForSeveralOfEachArticleCostsQuantityTimesPrice(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	member := e.newPerson(t, "2020-01-01", "member")
	friend := e.newPerson(t, "2020-01-01", "member")
	d := newOpenLottery(t, e, admin)
	e.notifier.waitForMessage(t) // creation
	e.notifier.waitForMessage(t) // opening

	// Nothing says a person can't have both articles, nor how many.
	papeletas := decode[distributions.Unit](t, mustStatus(t, ask(e, d, member, 0, 7), http.StatusCreated))
	if papeletas.Quantity != 7 || papeletas.Price != 5 || deref(papeletas.ArticleName) != "Papeleta" || papeletas.IsExtra || papeletas.BeneficiaryLogin != member.Login {
		t.Errorf("unexpected unit: %+v", papeletas)
	}
	if msg := e.notifier.waitForMessage(t); !strings.Contains(msg, "7 × «Papeleta»") {
		t.Errorf("notification should say what and how many: %q", msg)
	}
	mustStatus(t, ask(e, d, member, 1, 3), http.StatusCreated)
	mustStatus(t, register(e, d.ID, member, map[string]any{"beneficiary_login": friend.Login, "article_id": d.Articles[1].ID, "quantity": 1}), http.StatusCreated)

	// A second row for the same article is a change of quantity, not another ask.
	expect(t, ask(e, d, member, 0, 1), http.StatusConflict, "already_registered")

	got := decode[distributions.Distribution](t, mustStatus(t, e.do("GET", distributionPath(d.ID), member.token, nil), http.StatusOK))
	if got.Articles[0].Requested != 7 || got.Articles[1].Requested != 4 || got.UnitCount != 11 {
		t.Errorf("unexpected totals: %+v, %d units", got.Articles, got.UnitCount)
	}

	order := decode[distributions.Order](t, mustStatus(t, e.do("GET", distributionPath(d.ID)+"/order", admin.token, nil), http.StatusOK))
	if order.TotalUnits != 11 || order.TotalAmount != 7*5+4*20 || len(order.Lines) != 2 ||
		deref(order.Lines[0].ArticleName) != "Papeleta" || order.Lines[0].Count != 7 ||
		deref(order.Lines[1].ArticleName) != "Décimo" || order.Lines[1].Count != 4 {
		t.Errorf("unexpected order: %+v", order)
	}
}

func TestAskingNeedsAnArticleAndASensibleQuantity(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	member := e.newPerson(t, "2020-01-01", "member")
	d := newOpenLottery(t, e, admin)
	id := d.Articles[0].ID

	expect(t, register(e, d.ID, member, map[string]any{"quantity": 2}), http.StatusBadRequest, "article_required")
	for _, q := range []int{0, -3, 100001} {
		expect(t, register(e, d.ID, member, map[string]any{"article_id": id, "quantity": q}), http.StatusBadRequest, "invalid_quantity")
	}
	expect(t, register(e, d.ID, member, map[string]any{"article_id": id + 100000, "quantity": 1}), http.StatusBadRequest, "unknown_article")
	expect(t, register(e, d.ID, member, map[string]any{"article_id": id, "quantity": 1, "is_extra": true}), http.StatusConflict, "extras_not_allowed")

	// An article of another distribution isn't one of this one's.
	other := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, lottery()), http.StatusCreated))
	expect(t, register(e, d.ID, member, map[string]any{"article_id": other.Articles[0].ID, "quantity": 1}), http.StatusBadRequest, "unknown_article")

	// And one without articles takes neither.
	shirts := newOpenShirt(t, e, admin)
	expect(t, register(e, shirts.ID, member, map[string]any{"article_id": id, "quantity": 1, "choices": map[string]string{"Talla": "M"}}), http.StatusBadRequest, "unknown_article")
	expect(t, register(e, shirts.ID, member, map[string]any{"quantity": 2, "choices": map[string]string{"Talla": "M"}}), http.StatusBadRequest, "invalid_quantity")
}

func TestTheLimitOfAnArticleIsTheTotalAcrossEveryone(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	a := e.newPerson(t, "2020-01-01", "member")
	b := e.newPerson(t, "2020-01-01", "member")
	body := lottery()
	body["articles"] = []map[string]any{{"name": "Papeleta", "price": 5, "max_total": 10}, {"name": "Décimo", "price": 20}}
	d := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, body), http.StatusCreated))
	body["status"] = "open"
	d = decode[distributions.Distribution](t, mustStatus(t, e.do("PATCH", distributionPath(d.ID), admin.token, body), http.StatusOK))

	first := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 0, 6), http.StatusCreated))
	// Four left: five is too many, even for somebody who has nothing yet.
	rec := ask(e, d, b, 0, 5)
	expect(t, rec, http.StatusConflict, "article_exhausted")
	if !strings.Contains(rec.Body.String(), "4 left") {
		t.Errorf("the answer should say how many are left: %s", rec.Body.String())
	}
	second := decode[distributions.Unit](t, mustStatus(t, ask(e, d, b, 0, 4), http.StatusCreated))

	// Everything is taken: nobody can raise theirs, and an unlimited article is
	// unaffected.
	patch := func(u distributions.Unit, by person, q int) *httptest.ResponseRecorder {
		return e.do("PATCH", fmt.Sprintf("%s/%d", unitsPath(d.ID), u.ID), by.token, map[string]any{"quantity": q})
	}
	expect(t, patch(first, a, 7), http.StatusConflict, "article_exhausted")
	mustStatus(t, ask(e, d, a, 1, 500), http.StatusCreated)

	// Lowering one frees units for the other.
	if u := decode[distributions.Unit](t, mustStatus(t, patch(first, a, 3), http.StatusOK)); u.Quantity != 3 || u.Price != 5 {
		t.Errorf("unexpected unit: %+v", u)
	}
	mustStatus(t, patch(second, b, 7), http.StatusOK)
	expect(t, patch(second, b, 8), http.StatusConflict, "article_exhausted")

	// Quitting one frees them too.
	mustStatus(t, e.do("DELETE", fmt.Sprintf("%s/%d", unitsPath(d.ID), first.ID), a.token, nil), http.StatusNoContent)
	got := decode[distributions.Distribution](t, mustStatus(t, e.do("GET", distributionPath(d.ID), a.token, nil), http.StatusOK))
	if got.Articles[0].Requested != 7 {
		t.Errorf("expected 7 requested, got %+v", got.Articles[0])
	}
	mustStatus(t, ask(e, d, a, 0, 3), http.StatusCreated)
}

func TestChangingAQuantityFollowsTheRulesOfChangingAUnit(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	member := e.newPerson(t, "2020-01-01", "member")
	other := e.newPerson(t, "2020-01-01", "member")
	d := newOpenLottery(t, e, admin)
	u := decode[distributions.Unit](t, mustStatus(t, ask(e, d, member, 0, 2), http.StatusCreated))
	path := fmt.Sprintf("%s/%d", unitsPath(d.ID), u.ID)

	expect(t, e.do("PATCH", path, other.token, map[string]any{"quantity": 3}), http.StatusForbidden, "forbidden")
	for _, body := range []map[string]any{{"quantity": 0}, {"quantity": 100001}, {"choices": map[string]string{}}, {}} {
		expect(t, e.do("PATCH", path, member.token, body), http.StatusBadRequest, "invalid_quantity")
	}
	changed := decode[distributions.Unit](t, mustStatus(t, e.do("PATCH", path, member.token, map[string]any{"quantity": 9}), http.StatusOK))
	if changed.Quantity != 9 || deref(changed.RegisteredByLogin) != member.Login {
		t.Errorf("unexpected unit: %+v", changed)
	}

	// Paid, only staff change it; collected, nobody does.
	mustStatus(t, e.do("POST", distributionPath(d.ID)+"/payments", admin.token, map[string]any{"unit_ids": []int64{u.ID}}), http.StatusNoContent)
	expect(t, e.do("PATCH", path, member.token, map[string]any{"quantity": 1}), http.StatusConflict, "unit_locked")
	mustStatus(t, e.do("PATCH", path, admin.token, map[string]any{"quantity": 1}), http.StatusOK)

	// A unit of a distribution without articles has no quantity to change.
	shirts := newOpenShirt(t, e, admin)
	shirt := decode[distributions.Unit](t, mustStatus(t, register(e, shirts.ID, member, map[string]any{"choices": map[string]string{"Talla": "M"}}), http.StatusCreated))
	expect(t, e.do("PATCH", fmt.Sprintf("%s/%d", unitsPath(shirts.ID), shirt.ID), member.token, map[string]any{"quantity": 2}), http.StatusBadRequest, "invalid_quantity")
}

func TestArticlesAreFixedOnceSomebodyAskedButTheirLimitsCanChange(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	member := e.newPerson(t, "2020-01-01", "member")
	d := newOpenLottery(t, e, admin)
	mustStatus(t, ask(e, d, member, 0, 150), http.StatusCreated)

	update := func(articles []map[string]any) *httptest.ResponseRecorder {
		body := lottery()
		body["status"] = "open"
		body["articles"] = articles
		return e.do("PATCH", distributionPath(d.ID), admin.token, body)
	}
	// Sending everything back as it was is no change.
	mustStatus(t, update(lottery()["articles"].([]map[string]any)), http.StatusOK)
	// A different price, a different name, one fewer or one more: not any more.
	expect(t, update([]map[string]any{{"name": "Papeleta", "price": 6, "max_total": 200}, {"name": "Décimo", "price": 20, "max_total": 1000}}), http.StatusConflict, "has_units")
	expect(t, update([]map[string]any{{"name": "Boleto", "price": 5, "max_total": 200}, {"name": "Décimo", "price": 20, "max_total": 1000}}), http.StatusConflict, "has_units")
	expect(t, update([]map[string]any{{"name": "Papeleta", "price": 5, "max_total": 200}}), http.StatusConflict, "has_units")
	// The limit can go up, down to what is taken, or away; not below it.
	expect(t, update([]map[string]any{{"name": "Papeleta", "price": 5, "max_total": 149}, {"name": "Décimo", "price": 20, "max_total": 1000}}), http.StatusConflict, "cap_below_requested")
	got := decode[distributions.Distribution](t, mustStatus(t, update([]map[string]any{{"name": "Papeleta", "price": 5, "max_total": 150}, {"name": "Décimo", "price": 20}}), http.StatusOK))
	if *got.Articles[0].MaxTotal != 150 || got.Articles[1].MaxTotal != nil || got.Articles[0].Requested != 150 {
		t.Errorf("unexpected articles: %+v", got.Articles)
	}
	expect(t, ask(e, got, e.newPerson(t, "2020-01-01", "member"), 0, 1), http.StatusConflict, "article_exhausted")

	// Without units they can be replaced altogether, and turned into none.
	empty := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, lottery()), http.StatusCreated))
	body := lottery()
	body["articles"] = []map[string]any{{"name": "Camiseta", "price": 12}}
	got = decode[distributions.Distribution](t, mustStatus(t, e.do("PATCH", distributionPath(empty.ID), admin.token, body), http.StatusOK))
	if len(got.Articles) != 1 || got.Articles[0].Name != "Camiseta" {
		t.Errorf("unexpected articles: %+v", got.Articles)
	}
	// Leaving the field out leaves them as they are.
	delete(body, "articles")
	got = decode[distributions.Distribution](t, mustStatus(t, e.do("PATCH", distributionPath(empty.ID), admin.token, body), http.StatusOK))
	if len(got.Articles) != 1 {
		t.Errorf("articles should have been left alone: %+v", got.Articles)
	}
	body["articles"] = []map[string]any{}
	got = decode[distributions.Distribution](t, mustStatus(t, e.do("PATCH", distributionPath(empty.ID), admin.token, body), http.StatusOK))
	if len(got.Articles) != 0 {
		t.Errorf("expected no articles: %+v", got.Articles)
	}
}

func TestAnArticleDistributionCantBecomeOneWithExtrasOrOptions(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	d := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, lottery()), http.StatusCreated))
	body := lottery()
	body["extra_price"] = 3
	expect(t, e.do("PATCH", distributionPath(d.ID), admin.token, body), http.StatusBadRequest, "articles_conflict")
	// Leaving articles out keeps them, so the same rule applies.
	body = lottery()
	delete(body, "articles")
	body["audience"] = "all_members"
	body["cutoff_date"] = "2026-10-01"
	expect(t, e.do("PATCH", distributionPath(d.ID), admin.token, body), http.StatusBadRequest, "articles_conflict")
}

func TestCollectionAndPaymentOfArticleRowsAndDeletingThem(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	member := e.newPerson(t, "2020-01-01", "member")
	d := newOpenLottery(t, e, admin)
	u := decode[distributions.Unit](t, mustStatus(t, ask(e, d, member, 1, 4), http.StatusCreated))
	// Units are handed out while it is still open, without closing it first.
	mustStatus(t, e.do("POST", distributionPath(d.ID)+"/pickups", admin.token, map[string]any{"unit_ids": []int64{u.ID}, "picked_up_by_login": member.Login}), http.StatusNoContent)

	got := decode[distributions.Distribution](t, mustStatus(t, e.do("GET", distributionPath(d.ID), admin.token, nil), http.StatusOK))
	if got.UnitCount != 4 || got.PickedUpCount != 4 {
		t.Errorf("a row of 4 counts as 4: %d units, %d collected", got.UnitCount, got.PickedUpCount)
	}
	// What was collected is final, and stops the distribution from being deleted.
	expect(t, e.do("DELETE", distributionPath(d.ID), admin.token, nil), http.StatusConflict, "has_settled_units")
	mustStatus(t, e.do("DELETE", fmt.Sprintf("%s/%d/pickup", unitsPath(d.ID), u.ID), admin.token, nil), http.StatusNoContent)
	mustStatus(t, e.do("DELETE", distributionPath(d.ID), admin.token, nil), http.StatusNoContent)
}

func TestBackToDraftDeletesTheArticleRowsToo(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	member := e.newPerson(t, "2020-01-01", "member")
	d := newOpenLottery(t, e, admin)
	mustStatus(t, ask(e, d, member, 0, 5), http.StatusCreated)
	body := lottery()
	body["status"] = "draft"
	got := decode[distributions.Distribution](t, mustStatus(t, e.do("PATCH", distributionPath(d.ID), admin.token, body), http.StatusOK))
	if got.UnitCount != 0 || got.Articles[0].Requested != 0 {
		t.Errorf("everything asked for should be gone: %+v", got)
	}
}

func TestAnArticleMayLimitWhatOnePersonCanHave(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	a := e.newPerson(t, "2020-01-01", "member")
	body := lottery()
	body["articles"] = []map[string]any{{"name": "Décimo", "price": 20, "max_per_person": 5}}
	d := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, body), http.StatusCreated))
	if d.Articles[0].MaxPerPerson == nil || *d.Articles[0].MaxPerPerson != 5 {
		t.Fatalf("unexpected article: %+v", d.Articles[0])
	}
	body["status"] = "open"
	d = decode[distributions.Distribution](t, mustStatus(t, e.do("PATCH", distributionPath(d.ID), admin.token, body), http.StatusOK))

	expect(t, ask(e, d, a, 0, 6), http.StatusConflict, "article_person_limit")
	u := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 0, 5), http.StatusCreated))
	patch := func(q int) *httptest.ResponseRecorder {
		return e.do("PATCH", fmt.Sprintf("%s/%d", unitsPath(d.ID), u.ID), a.token, map[string]any{"quantity": q})
	}
	expect(t, patch(6), http.StatusConflict, "article_person_limit")
	mustStatus(t, patch(2), http.StatusOK)

	// Once somebody has asked, the cap can be raised or lowered to what the
	// biggest request is, not below.
	update := func(max int) *httptest.ResponseRecorder {
		body["articles"] = []map[string]any{{"name": "Décimo", "price": 20, "max_per_person": max}}
		return e.do("PATCH", distributionPath(d.ID), admin.token, body)
	}
	expect(t, update(1), http.StatusConflict, "cap_below_requested")
	mustStatus(t, update(2), http.StatusOK)
	body["articles"] = []map[string]any{{"name": "Décimo", "price": 20, "max_per_person": 0}}
	expect(t, e.do("PATCH", distributionPath(d.ID), admin.token, body), http.StatusBadRequest, "invalid_articles")
}

func TestItemsOfAnArticleWithNumbersNeedThemBeforeCollection(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	material := e.newPerson(t, "2020-01-01", "member", "material")
	a := e.newPerson(t, "2020-01-01", "member")
	b := e.newPerson(t, "2020-01-01", "member")
	body := lottery()
	body["articles"] = []map[string]any{
		{"name": "Papeleta", "price": 5, "has_identifiers": true},
		{"name": "Pegatina", "price": 1},
	}
	d := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, body), http.StatusCreated))
	body["status"] = "open"
	d = decode[distributions.Distribution](t, mustStatus(t, e.do("PATCH", distributionPath(d.ID), admin.token, body), http.StatusOK))
	papeletas := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 0, 3), http.StatusCreated))
	other := decode[distributions.Unit](t, mustStatus(t, ask(e, d, b, 0, 2), http.StatusCreated))
	stickers := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 1, 2), http.StatusCreated))

	ids := func(u distributions.Unit, by person, numbers ...string) *httptest.ResponseRecorder {
		return e.do("PUT", fmt.Sprintf("%s/%d/identifiers", unitsPath(d.ID), u.ID), by.token, map[string]any{"identifiers": numbers})
	}
	// They are written down in any state but draft, done included.
	for _, status := range []string{"closed", "done"} {
		body["status"] = status
		mustStatus(t, e.do("PATCH", distributionPath(d.ID), admin.token, body), http.StatusOK)
	}

	// Only staff, material included, write them; a member can't.
	expect(t, ids(papeletas, a, "1"), http.StatusForbidden, "forbidden")
	// Not more than the quantity, no empty or repeated one, none for an article without numbers.
	expect(t, ids(papeletas, admin, "1", "2", "3", "4"), http.StatusBadRequest, "invalid_identifiers")
	expect(t, ids(papeletas, admin, "1", " "), http.StatusBadRequest, "invalid_identifiers")
	expect(t, ids(papeletas, admin, "1", "1"), http.StatusBadRequest, "invalid_identifiers")
	expect(t, ids(stickers, admin, "1"), http.StatusBadRequest, "invalid_identifiers")

	pickup := func(u distributions.Unit, who person) *httptest.ResponseRecorder {
		return e.do("POST", distributionPath(d.ID)+"/pickups", material.token, map[string]any{"unit_ids": []int64{u.ID}, "picked_up_by_login": who.Login})
	}
	// Fewer than the quantity can be saved, but not collected.
	got := decode[distributions.Unit](t, mustStatus(t, ids(papeletas, material, " A-001 ", "A-002"), http.StatusOK))
	if len(got.Identifiers) != 2 || got.Identifiers[0] != "A-001" {
		t.Errorf("unexpected identifiers: %v", got.Identifiers)
	}
	expect(t, pickup(papeletas, a), http.StatusConflict, "identifiers_missing")
	// A number is unique within the article, across people.
	expect(t, ids(other, admin, "A-002", "A-009"), http.StatusConflict, "identifier_taken")
	mustStatus(t, ids(other, admin, "B-001", "B-002"), http.StatusOK)
	// Replacing is possible; then it can be collected.
	mustStatus(t, ids(papeletas, admin, "A-001", "A-002", "A-003"), http.StatusOK)
	mustStatus(t, pickup(papeletas, a), http.StatusNoContent)
	// An article without numbers needs none.
	mustStatus(t, pickup(stickers, a), http.StatusNoContent)
	if u := unitByID(t, e, admin, d.ID, papeletas.ID); len(u.Identifiers) != 3 || u.Identifiers[2] != "A-003" {
		t.Errorf("unexpected unit: %+v", u)
	}
	// Turning numbers on or off stays possible after people have asked.
	body["articles"] = []map[string]any{{"name": "Papeleta", "price": 5}, {"name": "Pegatina", "price": 1}}
	got2 := decode[distributions.Distribution](t, mustStatus(t, e.do("PATCH", distributionPath(d.ID), admin.token, body), http.StatusOK))
	if got2.Articles[0].HasIdentifiers {
		t.Errorf("numbers should be off: %+v", got2.Articles[0])
	}
}
