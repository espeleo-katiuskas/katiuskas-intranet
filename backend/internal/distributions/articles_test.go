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

	// Asking again adds to what was asked for: no need to edit it.
	if again := decode[distributions.Unit](t, mustStatus(t, ask(e, d, member, 0, 1), http.StatusCreated)); again.ID != papeletas.ID || again.Quantity != 8 {
		t.Errorf("asking again should add to the same row: %+v", again)
	}

	got := decode[distributions.Distribution](t, mustStatus(t, e.do("GET", distributionPath(d.ID), member.token, nil), http.StatusOK))
	if got.Articles[0].Requested != 8 || got.Articles[1].Requested != 4 || got.UnitCount != 12 {
		t.Errorf("unexpected totals: %+v, %d units", got.Articles, got.UnitCount)
	}

	order := decode[distributions.Order](t, mustStatus(t, e.do("GET", distributionPath(d.ID)+"/order", admin.token, nil), http.StatusOK))
	if order.TotalUnits != 12 || order.TotalAmount != 8*5+4*20 || len(order.Lines) != 2 ||
		deref(order.Lines[0].ArticleName) != "Papeleta" || order.Lines[0].Count != 8 ||
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
	// Not below what is paid; above it, and what is added is not paid.
	expect(t, e.do("PATCH", path, admin.token, map[string]any{"quantity": 1}), http.StatusConflict, "quantity_below_paid")
	more := decode[distributions.Unit](t, mustStatus(t, e.do("PATCH", path, admin.token, map[string]any{"quantity": 12}), http.StatusOK))
	if more.Quantity != 12 || more.PaidQuantity != 9 {
		t.Errorf("unexpected unit: %+v", more)
	}

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

// lotteryWithNumbers has the numbers described instead of free text: papeletas
// 0001-0200 and décimos in series 124 to 133 and 137, with fractions 1 to 10.
func lotteryWithNumbers() map[string]any {
	body := lottery()
	body["articles"] = []map[string]any{
		{"name": "Papeleta", "price": 5, "identifier_fields": []map[string]any{{"name": "Número", "spec": "0001-0200"}}},
		{"name": "Décimo", "price": 20, "identifier_fields": []map[string]any{
			{"name": "Serie", "spec": "124-133, 137"}, {"name": "Fracción", "spec": "1-10"},
		}},
	}
	return body
}

func newDeliveringLottery(t *testing.T, e *env, admin person, body map[string]any) distributions.Distribution {
	t.Helper()
	d := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, body), http.StatusCreated))
	body["status"] = "open"
	return decode[distributions.Distribution](t, mustStatus(t, e.do("PATCH", distributionPath(d.ID), admin.token, body), http.StatusOK))
}

func TestDescribedNumbersSetWhatThereIsToGiveOut(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	d := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, lotteryWithNumbers()), http.StatusCreated))
	papeleta, decimo := d.Articles[0], d.Articles[1]
	// There are exactly as many of each as combinations, and they have numbers.
	if papeleta.IdentifierTotal != 200 || *papeleta.MaxTotal != 200 || !papeleta.HasIdentifiers ||
		decimo.IdentifierTotal != 110 || *decimo.MaxTotal != 110 || len(decimo.IdentifierFields) != 2 {
		t.Errorf("unexpected articles: %+v", d.Articles)
	}
	// What is written as a maximum is ignored: it is the number of items.
	body := lotteryWithNumbers()
	body["articles"].([]map[string]any)[0]["max_total"] = 5
	other := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, body), http.StatusCreated))
	if *other.Articles[0].MaxTotal != 200 {
		t.Errorf("max_total should be the number of items: %+v", other.Articles[0])
	}

	for name, fields := range map[string][]map[string]any{
		"no name":       {{"name": " ", "spec": "1-3"}},
		"repeated name": {{"name": "A", "spec": "1-3"}, {"name": "A", "spec": "1-3"}},
		"empty spec":    {{"name": "A", "spec": ""}},
		"backwards":     {{"name": "A", "spec": "5-1"}},
		"repeated":      {{"name": "A", "spec": "1-5, 3"}},
		"separator":     {{"name": "A", "spec": "1/2"}},
		"too many":      {{"name": "A", "spec": "1-1000"}, {"name": "B", "spec": "1-1000"}},
	} {
		body := lottery()
		body["articles"] = []map[string]any{{"name": "X", "price": 1, "identifier_fields": fields}}
		rec := e.do("POST", "/api/v1/distributions", admin.token, body)
		if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "invalid_numbering" {
			t.Errorf("%s: expected 400 invalid_numbering, got %d %s", name, rec.Code, rec.Body.String())
		}
	}
}

func TestNextFreeNumbersAreGivenInOrder(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	material := e.newPerson(t, "2020-01-01", "member", "material")
	a := e.newPerson(t, "2020-01-01", "member")
	b := e.newPerson(t, "2020-01-01", "member")
	d := newDeliveringLottery(t, e, admin, lotteryWithNumbers())
	pa := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 0, 3), http.StatusCreated))
	pb := decode[distributions.Unit](t, mustStatus(t, ask(e, d, b, 0, 2), http.StatusCreated))
	da := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 1, 12), http.StatusCreated))

	assign := func(u distributions.Unit, by person) *httptest.ResponseRecorder {
		return e.do("POST", fmt.Sprintf("%s/%d/identifiers/assign", unitsPath(d.ID), u.ID), by.token, nil)
	}
	expect(t, assign(pa, a), http.StatusForbidden, "forbidden")
	got := decode[distributions.Unit](t, mustStatus(t, assign(pa, material), http.StatusOK))
	if strings.Join(got.Identifiers, ",") != "0001,0002,0003" {
		t.Errorf("unexpected numbers: %v", got.Identifiers)
	}
	got = decode[distributions.Unit](t, mustStatus(t, assign(pb, material), http.StatusOK))
	if strings.Join(got.Identifiers, ",") != "0004,0005" {
		t.Errorf("the next ones should follow: %v", got.Identifiers)
	}
	// Asking again changes nothing; the first field changes slowest.
	again := decode[distributions.Unit](t, mustStatus(t, assign(pb, material), http.StatusOK))
	if len(again.Identifiers) != 2 {
		t.Errorf("nothing more was needed: %v", again.Identifiers)
	}
	got = decode[distributions.Unit](t, mustStatus(t, assign(da, material), http.StatusOK))
	if strings.Join(got.Identifiers, ",") != "124/1,124/2,124/3,124/4,124/5,124/6,124/7,124/8,124/9,124/10,125/1,125/2" {
		t.Errorf("unexpected numbers: %v", got.Identifiers)
	}

	// An article with no numbers described has nothing to give.
	plain := newDeliveringLottery(t, e, admin, lottery())
	u := decode[distributions.Unit](t, mustStatus(t, ask(e, plain, a, 0, 1), http.StatusCreated))
	expect(t, e.do("POST", fmt.Sprintf("%s/%d/identifiers/assign", unitsPath(plain.ID), u.ID), material.token, nil), http.StatusBadRequest, "article_not_numbered")
}

func TestTypedNumbersMayBeRangesAndMustExist(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	a := e.newPerson(t, "2020-01-01", "member")
	b := e.newPerson(t, "2020-01-01", "member")
	d := newDeliveringLottery(t, e, admin, lotteryWithNumbers())
	pa := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 0, 6), http.StatusCreated))
	da := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 1, 4), http.StatusCreated))
	set := func(u distributions.Unit, numbers ...string) *httptest.ResponseRecorder {
		return e.do("PUT", fmt.Sprintf("%s/%d/identifiers", unitsPath(d.ID), u.ID), admin.token, map[string]any{"identifiers": numbers})
	}

	got := decode[distributions.Unit](t, mustStatus(t, set(pa, "0010-0012", "0020", "0100-0101"), http.StatusOK))
	if strings.Join(got.Identifiers, ",") != "0010,0011,0012,0020,0100,0101" {
		t.Errorf("unexpected numbers: %v", got.Identifiers)
	}
	// The last field may be a range, and the gaps of its values count: 133 is
	// followed by 137 in the series, not 134.
	got = decode[distributions.Unit](t, mustStatus(t, set(da, "133/9-10", "137/1-2"), http.StatusOK))
	if strings.Join(got.Identifiers, ",") != "133/9,133/10,137/1,137/2" {
		t.Errorf("unexpected numbers: %v", got.Identifiers)
	}
	for _, bad := range [][]string{{"0201"}, {"0000"}, {"1"}, {"124/11"}, {"134/1"}, {"124"}, {"0005-0003"}, {"abc"}} {
		unit := pa
		if strings.Contains(bad[0], "/") || bad[0] == "124" {
			unit = da
		}
		expect(t, set(unit, bad...), http.StatusBadRequest, "invalid_identifiers")
	}
	// A number somebody has is taken, whoever asks.
	pb := decode[distributions.Unit](t, mustStatus(t, ask(e, d, b, 0, 1), http.StatusCreated))
	expect(t, set(pb, "0011"), http.StatusConflict, "identifier_taken")
}

func TestPickingUpCanGiveTheMissingNumbersOnTheWay(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	material := e.newPerson(t, "2020-01-01", "member", "material")
	a := e.newPerson(t, "2020-01-01", "member")
	b := e.newPerson(t, "2020-01-01", "member")
	d := newDeliveringLottery(t, e, admin, lotteryWithNumbers())
	pa := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 0, 3), http.StatusCreated))
	pb := decode[distributions.Unit](t, mustStatus(t, ask(e, d, b, 0, 2), http.StatusCreated))
	pickups := distributionPath(d.ID) + "/pickups"

	expect(t, e.do("POST", pickups, material.token, map[string]any{"unit_ids": []int64{pa.ID}, "picked_up_by_login": a.Login}), http.StatusConflict, "identifiers_missing")
	mustStatus(t, e.do("POST", pickups, material.token, map[string]any{"unit_ids": []int64{pa.ID, pb.ID}, "picked_up_by_login": a.Login, "assign_numbers": true}), http.StatusNoContent)
	if u := unitByID(t, e, admin, d.ID, pb.ID); strings.Join(u.Identifiers, ",") != "0004,0005" || u.PickedUpAt == nil {
		t.Errorf("unexpected unit: %+v", u)
	}
}

func TestNotEnoughFreeNumbers(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	a := e.newPerson(t, "2020-01-01", "member")
	body := lotteryWithNumbers()
	body["articles"] = []map[string]any{{"name": "Papeleta", "price": 5, "identifier_fields": []map[string]any{{"name": "Número", "spec": "1-5"}}}}
	d := newDeliveringLottery(t, e, admin, body)
	expect(t, ask(e, d, a, 0, 6), http.StatusConflict, "article_exhausted")
	u := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 0, 5), http.StatusCreated))
	// Somebody took numbers by hand that the automatic ones would have used; the
	// ones left are fewer than needed only if the total allowed more than exists,
	// which a described article never does — so shrink the numbers instead.
	mustStatus(t, e.do("PUT", fmt.Sprintf("%s/%d/identifiers", unitsPath(d.ID), u.ID), admin.token, map[string]any{"identifiers": []string{"3-4"}}), http.StatusOK)
	got := decode[distributions.Unit](t, mustStatus(t, e.do("POST", fmt.Sprintf("%s/%d/identifiers/assign", unitsPath(d.ID), u.ID), admin.token, nil), http.StatusOK))
	if strings.Join(got.Identifiers, ",") != "3,4,1,2,5" {
		t.Errorf("the free ones fill in, in order: %v", got.Identifiers)
	}
}

func TestOverviewOfNumbersSaysWhoHasEachAndHowManyAreFree(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	material := e.newPerson(t, "2020-01-01", "member", "material")
	a := e.newPerson(t, "2020-01-01", "member")
	d := newDeliveringLottery(t, e, admin, lotteryWithNumbers())
	u := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 0, 3), http.StatusCreated))
	mustStatus(t, e.do("POST", fmt.Sprintf("%s/%d/identifiers/assign", unitsPath(d.ID), u.ID), material.token, nil), http.StatusOK)

	path := fmt.Sprintf("%s/articles/%d/numbers", distributionPath(d.ID), d.Articles[0].ID)
	expect(t, e.do("GET", path, a.token, nil), http.StatusForbidden, "forbidden")
	n := decode[distributions.Numbers](t, mustStatus(t, e.do("GET", path, material.token, nil), http.StatusOK))
	if n.Total != 200 || n.Assigned != 3 || n.Free != 197 || len(n.Items) != 200 ||
		n.Items[0].Identifier != "0001" || deref(n.Items[0].BeneficiaryLogin) != a.Login || n.Items[3].BeneficiaryLogin != nil || n.Items[199].Identifier != "0200" {
		t.Errorf("unexpected overview: %+v", n)
	}
	decimos := decode[distributions.Numbers](t, mustStatus(t, e.do("GET", fmt.Sprintf("%s/articles/%d/numbers", distributionPath(d.ID), d.Articles[1].ID), material.token, nil), http.StatusOK))
	if decimos.Total != 110 || decimos.Items[10].Identifier != "125/1" || decimos.Items[109].Identifier != "137/10" || decimos.Items[100].Identifier != "137/1" {
		t.Errorf("unexpected overview of the décimos: %+v", decimos.Items[100:])
	}
	expect(t, e.do("GET", fmt.Sprintf("%s/articles/%d/numbers", distributionPath(d.ID), d.Articles[0].ID+1000), material.token, nil), http.StatusBadRequest, "unknown_article")
	plain := newDeliveringLottery(t, e, admin, lottery())
	expect(t, e.do("GET", fmt.Sprintf("%s/articles/%d/numbers", distributionPath(plain.ID), plain.Articles[0].ID), material.token, nil), http.StatusBadRequest, "article_not_numbered")
}

func TestNumbersCanGrowButNotTakeAwayWhatSomebodyHas(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	a := e.newPerson(t, "2020-01-01", "member")
	d := newDeliveringLottery(t, e, admin, lotteryWithNumbers())
	u := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 1, 2), http.StatusCreated))
	mustStatus(t, e.do("PUT", fmt.Sprintf("%s/%d/identifiers", unitsPath(d.ID), u.ID), admin.token, map[string]any{"identifiers": []string{"137/1-2"}}), http.StatusOK)

	update := func(serie string) *httptest.ResponseRecorder {
		body := lotteryWithNumbers()
		body["status"] = "open"
		body["articles"].([]map[string]any)[1]["identifier_fields"] = []map[string]any{{"name": "Serie", "spec": serie}, {"name": "Fracción", "spec": "1-10"}}
		return e.do("PATCH", distributionPath(d.ID), admin.token, body)
	}
	// Another series bought: it is added, and with it what there is to give out.
	got := decode[distributions.Distribution](t, mustStatus(t, update("124-133, 137, 140"), http.StatusOK))
	if got.Articles[1].IdentifierTotal != 120 || *got.Articles[1].MaxTotal != 120 {
		t.Errorf("unexpected article: %+v", got.Articles[1])
	}
	// One somebody has can't go; one nobody has can.
	expect(t, update("124-133"), http.StatusConflict, "number_in_use")
	mustStatus(t, update("124-130, 137"), http.StatusOK)
	// And no fewer than the units asked for.
	mustStatus(t, ask(e, got, e.newPerson(t, "2020-01-01", "member"), 1, 78), http.StatusCreated)
	expect(t, update("137"), http.StatusConflict, "cap_below_requested")
}

func TestAskingAgainAddsToTheRowWhateverIsPaidOrHandedOutInIt(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	a := e.newPerson(t, "2020-01-01", "member")
	body := lottery()
	body["articles"] = []map[string]any{{"name": "Papeleta", "price": 5, "max_per_person": 10}}
	d := newDeliveringLottery(t, e, admin, body)

	first := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 0, 4), http.StatusCreated))
	mustStatus(t, e.do("POST", distributionPath(d.ID)+"/payments", admin.token, map[string]any{"unit_ids": []int64{first.ID}}), http.StatusNoContent)
	// A member can add to what is paid, which stays as it was.
	row := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 0, 3), http.StatusCreated))
	if row.ID != first.ID || row.Quantity != 7 || row.PaidQuantity != 4 {
		t.Errorf("unexpected row: %+v", row)
	}
	// The cap counts everything the person has.
	rec := ask(e, d, a, 0, 4)
	expect(t, rec, http.StatusConflict, "article_person_limit")
	if !strings.Contains(rec.Body.String(), "10 at most") {
		t.Errorf("should say the cap: %s", rec.Body.String())
	}

	// Part is handed out; asking again adds to the same row, the new units still to
	// be handed out.
	mustStatus(t, e.do("POST", distributionPath(d.ID)+"/pickups", admin.token, map[string]any{
		"deliveries": []map[string]any{{"unit_id": first.ID, "quantity": 5}}, "picked_up_by_login": a.Login,
	}), http.StatusNoContent)
	again := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 0, 3), http.StatusCreated))
	if again.ID != first.ID || again.Quantity != 10 || again.PickedUpQuantity != 5 || again.PaidQuantity != 4 {
		t.Errorf("unexpected row: %+v", again)
	}
}

func TestARowCanBePaidInPartAndAddingToItLeavesTheRestPending(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	board := e.newPerson(t, "2020-01-01", "member", "board")
	a := e.newPerson(t, "2020-01-01", "member")
	d := newDeliveringLottery(t, e, admin, lotteryWithNumbers())
	papeletas := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 0, 10), http.StatusCreated))
	decimos := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 1, 4), http.StatusCreated))
	payments := distributionPath(d.ID) + "/payments"
	pay := func(by person, body map[string]any) *httptest.ResponseRecorder {
		return e.do("POST", payments, by.token, body)
	}
	unit := func(u distributions.Unit) distributions.Unit { return unitByID(t, e, admin, d.ID, u.ID) }

	// Only admin and board, as before.
	expect(t, pay(a, map[string]any{"unit_ids": []int64{papeletas.ID}}), http.StatusForbidden, "forbidden")
	// Part of a row: three of the ten.
	mustStatus(t, pay(board, map[string]any{"payments": []map[string]any{{"unit_id": papeletas.ID, "quantity": 3}}}), http.StatusNoContent)
	if u := unit(papeletas); u.PaidQuantity != 3 || u.PaidAt == nil || deref(u.PaidMarkedByLogin) != board.Login {
		t.Errorf("unexpected unit: %+v", u)
	}
	// Any part, more than once, together with whole rows: what is pending is 7.
	mustStatus(t, pay(admin, map[string]any{
		"payments": []map[string]any{{"unit_id": papeletas.ID, "quantity": 2}},
		"unit_ids": []int64{decimos.ID},
	}), http.StatusNoContent)
	if u := unit(papeletas); u.PaidQuantity != 5 {
		t.Errorf("expected 5 paid: %+v", u)
	}
	if u := unit(decimos); u.PaidQuantity != 4 {
		t.Errorf("a whole row means all that is pending: %+v", u)
	}
	// Whatever is left is what "all" means; more than is pending, or nothing
	// pending, is refused, and refuses the whole batch.
	expect(t, pay(admin, map[string]any{"payments": []map[string]any{{"unit_id": papeletas.ID, "quantity": 6}}}), http.StatusConflict, "already_paid")
	expect(t, pay(admin, map[string]any{"unit_ids": []int64{decimos.ID}}), http.StatusConflict, "already_paid")
	expect(t, pay(admin, map[string]any{"payments": []map[string]any{{"unit_id": papeletas.ID, "quantity": 1}, {"unit_id": decimos.ID, "quantity": 1}}}), http.StatusConflict, "already_paid")
	if u := unit(papeletas); u.PaidQuantity != 5 {
		t.Errorf("a refused batch must change nothing: %+v", u)
	}
	expect(t, pay(admin, map[string]any{"payments": []map[string]any{{"unit_id": 999999, "quantity": 1}}}), http.StatusNotFound, "not_found")
	expect(t, pay(admin, map[string]any{"payments": []map[string]any{{"unit_id": papeletas.ID, "quantity": -1}}}), http.StatusBadRequest, "invalid_quantity")
	expect(t, pay(admin, map[string]any{}), http.StatusBadRequest, "no_units")

	// Asking for more doesn't make what is added paid: 5 of 15.
	more := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 0, 5), http.StatusCreated))
	if more.ID != papeletas.ID || more.Quantity != 15 || more.PaidQuantity != 5 {
		t.Errorf("unexpected unit: %+v", more)
	}

	// Taking some off, or all of it.
	clear := func(q string) *httptest.ResponseRecorder {
		return e.do("DELETE", fmt.Sprintf("%s/%d/payment%s", unitsPath(d.ID), papeletas.ID, q), board.token, nil)
	}
	expect(t, clear("?quantity=6"), http.StatusBadRequest, "invalid_quantity")
	expect(t, clear("?quantity=0"), http.StatusBadRequest, "invalid_quantity")
	mustStatus(t, clear("?quantity=2"), http.StatusNoContent)
	if u := unit(papeletas); u.PaidQuantity != 3 || u.PaidAt == nil {
		t.Errorf("unexpected unit: %+v", u)
	}
	mustStatus(t, clear(""), http.StatusNoContent)
	if u := unit(papeletas); u.PaidQuantity != 0 || u.PaidAt != nil || u.PaidMarkedByLogin != nil {
		t.Errorf("nothing should be paid: %+v", u)
	}
}

func TestMaterialCanChangeAndRemoveAnybodysOrder(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	material := e.newPerson(t, "2020-01-01", "member", "material")
	owner := e.newPerson(t, "2020-01-01", "member")
	other := e.newPerson(t, "2020-01-01", "member")
	d := newOpenLottery(t, e, admin)
	u := decode[distributions.Unit](t, mustStatus(t, ask(e, d, owner, 0, 2), http.StatusCreated))
	path := fmt.Sprintf("%s/%d", unitsPath(d.ID), u.ID)

	expect(t, e.do("PATCH", path, other.token, map[string]any{"quantity": 3}), http.StatusForbidden, "forbidden")
	expect(t, e.do("DELETE", path, other.token, nil), http.StatusForbidden, "forbidden")
	// Material isn't the owner, nor entered it, and changes it all the same.
	if got := decode[distributions.Unit](t, mustStatus(t, e.do("PATCH", path, material.token, map[string]any{"quantity": 3}), http.StatusOK)); got.Quantity != 3 || deref(got.RegisteredByLogin) != owner.Login {
		t.Errorf("unexpected unit: %+v", got)
	}
	// What is paid locks it for its owner, not for material.
	mustStatus(t, e.do("POST", distributionPath(d.ID)+"/payments", admin.token, map[string]any{"unit_ids": []int64{u.ID}}), http.StatusNoContent)
	expect(t, e.do("PATCH", path, owner.token, map[string]any{"quantity": 4}), http.StatusConflict, "unit_locked")
	mustStatus(t, e.do("PATCH", path, material.token, map[string]any{"quantity": 4}), http.StatusOK)
	// Marking payments stays for admin/board.
	expect(t, e.do("POST", distributionPath(d.ID)+"/payments", material.token, map[string]any{"unit_ids": []int64{u.ID}}), http.StatusForbidden, "forbidden")
	mustStatus(t, e.do("DELETE", path, material.token, nil), http.StatusNoContent)
}

func TestARowCanBeHandedOutInPartAndTheRestLater(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	material := e.newPerson(t, "2020-01-01", "member", "material")
	a := e.newPerson(t, "2020-01-01", "member")
	d := newDeliveringLottery(t, e, admin, lottery())
	u := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 0, 8), http.StatusCreated))
	pickups := distributionPath(d.ID) + "/pickups"
	deliver := func(body map[string]any) *httptest.ResponseRecorder {
		body["picked_up_by_login"] = a.Login
		return e.do("POST", pickups, material.token, body)
	}
	row := func() distributions.Unit { return unitByID(t, e, admin, d.ID, u.ID) }

	mustStatus(t, deliver(map[string]any{"deliveries": []map[string]any{{"unit_id": u.ID, "quantity": 3}}}), http.StatusNoContent)
	if got := row(); got.PickedUpQuantity != 3 || got.PickedUpAt == nil || deref(got.PickedUpByLogin) != a.Login {
		t.Errorf("unexpected row: %+v", got)
	}
	// Any part, more than once, but not more than is left: 5.
	mustStatus(t, deliver(map[string]any{"deliveries": []map[string]any{{"unit_id": u.ID, "quantity": 2}}}), http.StatusNoContent)
	expect(t, deliver(map[string]any{"deliveries": []map[string]any{{"unit_id": u.ID, "quantity": 4}}}), http.StatusConflict, "already_picked_up")
	expect(t, deliver(map[string]any{"deliveries": []map[string]any{{"unit_id": u.ID, "quantity": -1}}}), http.StatusBadRequest, "invalid_quantity")
	expect(t, deliver(map[string]any{"deliveries": []map[string]any{{"unit_id": u.ID, "identifiers": []string{"1"}}}}), http.StatusBadRequest, "invalid_identifiers")
	// A whole row means all that is left; then there is nothing more to hand out.
	mustStatus(t, deliver(map[string]any{"unit_ids": []int64{u.ID}}), http.StatusNoContent)
	if got := row(); got.PickedUpQuantity != 8 {
		t.Errorf("everything should be handed out: %+v", got)
	}
	expect(t, deliver(map[string]any{"unit_ids": []int64{u.ID}}), http.StatusConflict, "already_picked_up")
	if got := decode[distributions.Distribution](t, mustStatus(t, e.do("GET", distributionPath(d.ID), admin.token, nil), http.StatusOK)); got.PickedUpCount != 8 {
		t.Errorf("picked_up_count counts units: %d", got.PickedUpCount)
	}

	// Undoing is also by parts, and everything at once without saying.
	undo := func(q string) *httptest.ResponseRecorder {
		return e.do("DELETE", fmt.Sprintf("%s/%d/pickup%s", unitsPath(d.ID), u.ID, q), material.token, nil)
	}
	expect(t, undo("?quantity=9"), http.StatusBadRequest, "invalid_quantity")
	mustStatus(t, undo("?quantity=3"), http.StatusNoContent)
	if got := row(); got.PickedUpQuantity != 5 || got.PickedUpAt == nil {
		t.Errorf("unexpected row: %+v", got)
	}
	mustStatus(t, undo(""), http.StatusNoContent)
	if got := row(); got.PickedUpQuantity != 0 || got.PickedUpAt != nil || got.PickedUpByLogin != nil {
		t.Errorf("nothing should be handed out: %+v", got)
	}
}

func TestOnlyNumbersAssignedCanBeHandedOutAndTheyAreWhichOnesWent(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	a := e.newPerson(t, "2020-01-01", "member")
	d := newDeliveringLottery(t, e, admin, lotteryWithNumbers())
	u := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 0, 5), http.StatusCreated))
	pickups := distributionPath(d.ID) + "/pickups"
	deliver := func(by distributions.Unit, body map[string]any) *httptest.ResponseRecorder {
		body["picked_up_by_login"] = a.Login
		return e.do("POST", pickups, admin.token, body)
	}
	row := func() distributions.Unit { return unitByID(t, e, admin, d.ID, u.ID) }
	setNumbers := func(numbers ...string) {
		mustStatus(t, e.do("PUT", fmt.Sprintf("%s/%d/identifiers", unitsPath(d.ID), u.ID), admin.token, map[string]any{"identifiers": numbers}), http.StatusOK)
	}

	// Nothing is assigned: nothing can go.
	expect(t, deliver(u, map[string]any{"deliveries": []map[string]any{{"unit_id": u.ID, "quantity": 1}}}), http.StatusConflict, "identifiers_missing")
	setNumbers("0001-0003")
	// Two of the three assigned: the first two, in the order assigned.
	mustStatus(t, deliver(u, map[string]any{"deliveries": []map[string]any{{"unit_id": u.ID, "quantity": 2}}}), http.StatusNoContent)
	if got := row(); got.PickedUpQuantity != 2 || strings.Join(got.DeliveredIdentifiers, ",") != "0001,0002" || len(got.Identifiers) != 3 {
		t.Errorf("unexpected row: %+v", got)
	}
	// Only one is assigned and not delivered: two are too many, whichever quantity.
	expect(t, deliver(u, map[string]any{"deliveries": []map[string]any{{"unit_id": u.ID, "quantity": 2}}}), http.StatusConflict, "identifiers_missing")
	// Which one goes can be said; it has to be assigned and not delivered.
	expect(t, deliver(u, map[string]any{"deliveries": []map[string]any{{"unit_id": u.ID, "identifiers": []string{"0001"}}}}), http.StatusBadRequest, "invalid_identifiers")
	expect(t, deliver(u, map[string]any{"deliveries": []map[string]any{{"unit_id": u.ID, "identifiers": []string{"0150"}}}}), http.StatusBadRequest, "invalid_identifiers")
	expect(t, deliver(u, map[string]any{"deliveries": []map[string]any{{"unit_id": u.ID, "identifiers": []string{"0003"}, "quantity": 2}}}), http.StatusBadRequest, "invalid_quantity")
	mustStatus(t, deliver(u, map[string]any{"deliveries": []map[string]any{{"unit_id": u.ID, "identifiers": []string{"0003"}}}}), http.StatusNoContent)
	// With "assign" the missing ones are given on the way, from the next free.
	mustStatus(t, deliver(u, map[string]any{"deliveries": []map[string]any{{"unit_id": u.ID, "quantity": 2}}, "assign_numbers": true}), http.StatusNoContent)
	got := row()
	if got.PickedUpQuantity != 5 || strings.Join(got.DeliveredIdentifiers, ",") != "0001,0002,0003,0004,0005" {
		t.Errorf("unexpected row: %+v", got)
	}
	// Numbers handed out can't be dropped from the list.
	expect(t, e.do("PUT", fmt.Sprintf("%s/%d/identifiers", unitsPath(d.ID), u.ID), admin.token, map[string]any{"identifiers": []string{"0001-0004"}}), http.StatusBadRequest, "invalid_identifiers")

	// Undoing a delivery keeps the numbers assigned, only not handed out.
	mustStatus(t, e.do("DELETE", fmt.Sprintf("%s/%d/pickup?identifiers=0002,0004", unitsPath(d.ID), u.ID), admin.token, nil), http.StatusNoContent)
	got = row()
	if got.PickedUpQuantity != 3 || strings.Join(got.DeliveredIdentifiers, ",") != "0001,0003,0005" || len(got.Identifiers) != 5 {
		t.Errorf("unexpected row: %+v", got)
	}
	expect(t, e.do("DELETE", fmt.Sprintf("%s/%d/pickup?identifiers=0002", unitsPath(d.ID), u.ID), admin.token, nil), http.StatusBadRequest, "invalid_identifiers")

	// The overview says which are handed out.
	n := decode[distributions.Numbers](t, mustStatus(t, e.do("GET", fmt.Sprintf("%s/articles/%d/numbers", distributionPath(d.ID), d.Articles[0].ID), admin.token, nil), http.StatusOK))
	if !n.Items[0].Delivered || n.Items[1].Delivered || n.Items[1].BeneficiaryLogin == nil || n.Items[5].BeneficiaryLogin != nil {
		t.Errorf("unexpected overview: %+v", n.Items[:6])
	}
}

func TestHandedOutUnitsCanBeReturnedUnlessPaidAndTheirNumbersAreFreed(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	a := e.newPerson(t, "2020-01-01", "member")
	d := newDeliveringLottery(t, e, admin, lotteryWithNumbers())
	u := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 0, 5), http.StatusCreated))
	other := decode[distributions.Unit](t, mustStatus(t, ask(e, d, e.newPerson(t, "2020-01-01", "member"), 0, 2), http.StatusCreated))
	unitPath := fmt.Sprintf("%s/%d", unitsPath(d.ID), u.ID)
	row := func() distributions.Unit { return unitByID(t, e, admin, d.ID, u.ID) }
	free := func() int {
		return decode[distributions.Numbers](t, mustStatus(t, e.do("GET", fmt.Sprintf("%s/articles/%d/numbers", distributionPath(d.ID), d.Articles[0].ID), admin.token, nil), http.StatusOK)).Free
	}
	giveBack := func(body map[string]any) *httptest.ResponseRecorder {
		return e.do("POST", unitPath+"/return", admin.token, body)
	}
	// Four of the five are handed out (numbers given on the way), two are paid.
	mustStatus(t, e.do("POST", distributionPath(d.ID)+"/pickups", admin.token, map[string]any{
		"deliveries": []map[string]any{{"unit_id": u.ID, "quantity": 4}}, "picked_up_by_login": a.Login, "assign_numbers": true,
	}), http.StatusNoContent)
	mustStatus(t, e.do("POST", distributionPath(d.ID)+"/payments", admin.token, map[string]any{"payments": []map[string]any{{"unit_id": u.ID, "quantity": 2}}}), http.StatusNoContent)
	if got := row(); strings.Join(got.Identifiers, ",") != "0001,0002,0003,0004" {
		t.Fatalf("unexpected numbers: %v", got.Identifiers)
	}
	before := free()

	// Nothing handed out can't come back; neither more than was handed out, nor
	// what would leave less than is paid (5 ordered, 2 paid: 3 at most).
	expect(t, giveBack(map[string]any{"quantity": 5}), http.StatusBadRequest, "invalid_quantity")
	expect(t, giveBack(map[string]any{"identifiers": []string{"0005"}}), http.StatusBadRequest, "invalid_identifiers")
	// Without saying, all that can: min(4 delivered, 5-2) = 3, the last delivered.
	mustStatus(t, giveBack(map[string]any{"quantity": 3}), http.StatusNoContent)
	got := row()
	if got.Quantity != 2 || got.PickedUpQuantity != 1 || got.PaidQuantity != 2 || strings.Join(got.Identifiers, ",") != "0001" || strings.Join(got.DeliveredIdentifiers, ",") != "0001" {
		t.Errorf("unexpected row: %+v", got)
	}
	if free() != before+3 {
		t.Errorf("the numbers returned should be free: %d -> %d", before, free())
	}
	// The one still handed out is paid-for territory now: 2 ordered, 2 paid.
	expect(t, giveBack(map[string]any{"quantity": 1}), http.StatusConflict, "quantity_below_paid")
	mustStatus(t, e.do("DELETE", unitPath+"/payment", admin.token, nil), http.StatusNoContent)
	// Specific numbers, and the row goes when nothing is left.
	mustStatus(t, giveBack(map[string]any{"identifiers": []string{"0001"}}), http.StatusNoContent)
	if got := row(); got.Quantity != 1 || got.PickedUpQuantity != 0 || got.PickedUpAt != nil || len(got.Identifiers) != 0 {
		t.Errorf("unexpected row: %+v", got)
	}
	mustStatus(t, e.do("POST", distributionPath(d.ID)+"/pickups", admin.token, map[string]any{
		"deliveries": []map[string]any{{"unit_id": u.ID}}, "picked_up_by_login": a.Login, "assign_numbers": true,
	}), http.StatusNoContent)
	mustStatus(t, giveBack(map[string]any{}), http.StatusNoContent)
	for _, listed := range decode[[]distributions.Unit](t, mustStatus(t, e.do("GET", unitsPath(d.ID), admin.token, nil), http.StatusOK)) {
		if listed.ID == u.ID {
			t.Errorf("a row with nothing left should be gone: %+v", listed)
		}
	}
	// Not in a draft, and not for somebody who isn't staff.
	expect(t, e.do("POST", fmt.Sprintf("%s/%d/return", unitsPath(d.ID), other.ID), a.token, map[string]any{}), http.StatusForbidden, "forbidden")
}

func TestARowHandedOutInPartCanGrowAndShrinkButNotBelowWhatWentOut(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	a := e.newPerson(t, "2020-01-01", "member")
	d := newDeliveringLottery(t, e, admin, lottery())
	u := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 0, 6), http.StatusCreated))
	path := fmt.Sprintf("%s/%d", unitsPath(d.ID), u.ID)
	mustStatus(t, e.do("POST", distributionPath(d.ID)+"/pickups", admin.token, map[string]any{
		"deliveries": []map[string]any{{"unit_id": u.ID, "quantity": 4}}, "picked_up_by_login": a.Login,
	}), http.StatusNoContent)

	// The owner can't touch it any more; staff can, down to what went out.
	expect(t, e.do("PATCH", path, a.token, map[string]any{"quantity": 7}), http.StatusConflict, "unit_locked")
	expect(t, e.do("PATCH", path, admin.token, map[string]any{"quantity": 3}), http.StatusConflict, "quantity_below_delivered")
	if got := decode[distributions.Unit](t, mustStatus(t, e.do("PATCH", path, admin.token, map[string]any{"quantity": 4}), http.StatusOK)); got.Quantity != 4 || got.PickedUpQuantity != 4 {
		t.Errorf("unexpected row: %+v", got)
	}
	mustStatus(t, e.do("PATCH", path, admin.token, map[string]any{"quantity": 9}), http.StatusOK)
	// Units handed out have to be returned before the row can go.
	expect(t, e.do("DELETE", path, admin.token, nil), http.StatusConflict, "unit_locked")
}
