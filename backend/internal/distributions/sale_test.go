package distributions_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/distributions"
)

func salesPath(id int64) string { return distributionPath(id) + "/sales" }

func unitsOf(t *testing.T, e *env, by person, d distributions.Distribution) []distributions.Unit {
	t.Helper()
	return decode[[]distributions.Unit](t, mustStatus(t, e.do("GET", unitsPath(d.ID), by.token, nil), http.StatusOK))
}

func TestASaleOfANumberedArticleOrdersNumbersCollectsAndHandsOut(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	a := e.newPerson(t, "2020-01-01", "member")
	d := newDeliveringLottery(t, e, admin, lotteryWithNumbers())

	got := decode[distributions.Unit](t, mustStatus(t, e.do("POST", salesPath(d.ID), admin.token, map[string]any{
		"beneficiary_login": a.Login, "article_id": d.Articles[1].ID, "quantity": 1,
		"paid": true, "account_id": e.cash, "delivered": true,
	}), http.StatusCreated))
	if strings.Join(got.Identifiers, ",") != "124/1" || got.PaidQuantity != 1 || got.PickedUpQuantity != 1 ||
		strings.Join(got.PaidIdentifiers, ",") != "124/1" || strings.Join(got.DeliveredIdentifiers, ",") != "124/1" {
		t.Errorf("unexpected unit after the sale: %+v", got)
	}
	if paid, entries := e.paidQuantity(t, got.ID); paid != 1 || entries != 1 {
		t.Errorf("expected the payment to be recorded once, got paid=%d entries=%d", paid, entries)
	}

	// Numbers asked for by name come first and the rest is given from the next free; a second
	// sale to the same person only collects and hands out what it adds.
	second := decode[distributions.Unit](t, mustStatus(t, e.do("POST", salesPath(d.ID), admin.token, map[string]any{
		"beneficiary_login": a.Login, "article_id": d.Articles[1].ID, "quantity": 2, "identifiers": []string{"125/3"},
		"delivered": true,
	}), http.StatusCreated))
	if second.ID != got.ID || second.Quantity != 3 || strings.Join(second.Identifiers, ",") != "124/1,125/3,124/2" {
		t.Errorf("unexpected row after the second sale: %+v", second)
	}
	if second.PaidQuantity != 1 || second.PickedUpQuantity != 3 {
		t.Errorf("only the new numbers should have been handed out and none paid: %+v", second)
	}
}

func TestASaleOfACountedArticleLeavesWhatWasBeforeAlone(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	a := e.newPerson(t, "2020-01-01", "member")
	d := newDeliveringLottery(t, e, admin, lottery())
	earlier := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 1, 2), http.StatusCreated))

	got := decode[distributions.Unit](t, mustStatus(t, e.do("POST", salesPath(d.ID), admin.token, map[string]any{
		"beneficiary_login": a.Login, "article_id": d.Articles[1].ID, "quantity": 3,
		"paid": true, "account_id": e.cash, "delivered": true,
	}), http.StatusCreated))
	if got.ID != earlier.ID || got.Quantity != 5 || got.PaidQuantity != 3 || got.PickedUpQuantity != 3 {
		t.Errorf("the two ordered before must stay unpaid and not handed out: %+v", got)
	}
}

func TestASaleCanOnlyOrderAndAFreeOneNeedsNoAccount(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	a := e.newPerson(t, "2020-01-01", "member")
	d := newDeliveringLottery(t, e, admin, lottery())

	got := decode[distributions.Unit](t, mustStatus(t, e.do("POST", salesPath(d.ID), admin.token, map[string]any{
		"beneficiary_login": a.Login, "article_id": d.Articles[0].ID, "quantity": 4,
	}), http.StatusCreated))
	if got.Quantity != 4 || got.PaidQuantity != 0 || got.PickedUpQuantity != 0 {
		t.Errorf("nothing should be paid or handed out: %+v", got)
	}
}

func TestASaleIsRefusedWithoutChangingAnything(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	board := e.newPerson(t, "2020-01-01", "member", "board")
	material := e.newPerson(t, "2020-01-01", "member", "material")
	member := e.newPerson(t, "2020-01-01", "member")
	stranger := e.newPerson(t, "")
	d := newDeliveringLottery(t, e, admin, lottery())
	sale := func(by person, who person, extra map[string]any) map[string]any {
		body := map[string]any{"beneficiary_login": who.Login, "article_id": d.Articles[1].ID, "quantity": 1}
		for k, v := range extra {
			body[k] = v
		}
		return body
	}

	expect(t, e.do("POST", salesPath(d.ID), material.token, sale(material, member, nil)), http.StatusForbidden, "forbidden")
	expect(t, e.do("POST", salesPath(d.ID), member.token, sale(member, member, nil)), http.StatusForbidden, "forbidden")
	// Only whoever may have the distribution can be sold to.
	expect(t, e.do("POST", salesPath(d.ID), admin.token, sale(admin, stranger, map[string]any{"delivered": true})), http.StatusConflict, "not_eligible")
	expect(t, e.do("POST", salesPath(d.ID), admin.token, sale(admin, member, map[string]any{"paid": true})), http.StatusBadRequest, "account_required")
	// The money failing leaves no order behind.
	expect(t, e.do("POST", salesPath(d.ID), admin.token, sale(admin, member, map[string]any{"paid": true, "account_id": 999999})), http.StatusBadRequest, "unknown_account")
	if units := unitsOf(t, e, admin, d); len(units) != 0 {
		t.Errorf("a failed sale left orders behind: %+v", units)
	}
	// Board may sell, and a draft cannot be sold from.
	mustStatus(t, e.do("POST", salesPath(d.ID), board.token, sale(board, member, nil)), http.StatusCreated)
	draft := decode[distributions.Distribution](t, mustStatus(t, e.do("POST", "/api/v1/distributions", admin.token, lottery()), http.StatusCreated))
	body := sale(admin, member, nil)
	body["article_id"] = draft.Articles[1].ID
	expect(t, e.do("POST", salesPath(draft.ID), admin.token, body), http.StatusConflict, "not_deliverable")
}

func TestAFailedSaleToSomebodyWithARowPutsTheRowBack(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	a := e.newPerson(t, "2020-01-01", "member")
	d := newDeliveringLottery(t, e, admin, lottery())
	earlier := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 1, 2), http.StatusCreated))

	expect(t, e.do("POST", salesPath(d.ID), admin.token, map[string]any{
		"beneficiary_login": a.Login, "article_id": d.Articles[1].ID, "quantity": 3, "paid": true, "account_id": 999999,
	}), http.StatusBadRequest, "unknown_account")
	if got := unitByID(t, e, admin, d.ID, earlier.ID); got.Quantity != 2 {
		t.Errorf("the row should be back to %d: %s", 2, fmt.Sprint(got.Quantity))
	}
}
