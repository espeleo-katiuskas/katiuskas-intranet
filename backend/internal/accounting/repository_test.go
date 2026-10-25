package accounting_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/accounting"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
)

func newRepo(t *testing.T) *accounting.Repository {
	tx := testutil.NewTx(t)
	return accounting.NewRepository(tx)
}

var income = accounting.AccountTypeIncome // the type accounts for dues or licenses need

func strPtr(s string) *string { return &s }

// codeOf reads an Account's own Code back out as a plain string ("" for
// nil) — Code is *string (see Account's own doc comment), but every
// test that predates that still wants to compare/format it as one.
func codeOf(a accounting.Account) string {
	if a.Code == nil {
		return ""
	}
	return *a.Code
}

func mustCreateAccount(t *testing.T, repo *accounting.Repository, name, code string) accounting.Account {
	t.Helper()
	a, err := repo.CreateAccount(context.Background(), accounting.CreateAccountInput{Name: name, Code: strPtr(code)})
	if err != nil {
		t.Fatalf("create account %s: %v", code, err)
	}
	return a
}

func TestCreateAccount(t *testing.T) {
	repo := newRepo(t)
	a, err := repo.CreateAccount(context.Background(), accounting.CreateAccountInput{Name: "Caja", Code: strPtr("570")})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if a.ID == 0 || a.Name != "Caja" || codeOf(a) != "570" || a.ParentID != nil {
		t.Errorf("unexpected account: %+v", a)
	}
}

func TestCreateAccountWithParent(t *testing.T) {
	repo := newRepo(t)
	parent := mustCreateAccount(t, repo, "Tesorería", "57")
	child, err := repo.CreateAccount(context.Background(), accounting.CreateAccountInput{
		Name: "Caja", Code: strPtr("570"), ParentID: &parent.ID,
	})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if child.ParentID == nil || *child.ParentID != parent.ID {
		t.Errorf("expected parent_id %d, got %+v", parent.ID, child.ParentID)
	}
}

func TestCreateAccountRejectsUnknownParent(t *testing.T) {
	repo := newRepo(t)
	bogus := int64(999999)
	_, err := repo.CreateAccount(context.Background(), accounting.CreateAccountInput{Name: "Caja", Code: strPtr("570"), ParentID: &bogus})
	if !errors.Is(err, accounting.ErrUnknownAccount) {
		t.Errorf("expected ErrUnknownAccount, got %v", err)
	}
}

func TestCreateAccountRejectsDuplicateCode(t *testing.T) {
	repo := newRepo(t)
	mustCreateAccount(t, repo, "Caja", "570")
	_, err := repo.CreateAccount(context.Background(), accounting.CreateAccountInput{Name: "Otra caja", Code: strPtr("570")})
	if !errors.Is(err, accounting.ErrDuplicateCode) {
		t.Errorf("expected ErrDuplicateCode, got %v", err)
	}
}

// TestCreateAccountWithoutCode and TestCreateAccountAllowsSeveralWithNoCode
// cover code actually being optional (migration
// 00013_accounts_code_optional.sql): a nil Code is stored as a real SQL
// NULL, and — unlike two accounts sharing the same non-empty code,
// which accounts.code's own UNIQUE constraint rejects — any number of
// accounts can each have no code at all, since Postgres treats every
// NULL as distinct from every other for uniqueness purposes.
func TestCreateAccountWithoutCode(t *testing.T) {
	repo := newRepo(t)
	a, err := repo.CreateAccount(context.Background(), accounting.CreateAccountInput{Name: "Grupo de reporting"})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if a.Code != nil {
		t.Errorf("expected a nil code, got %q", *a.Code)
	}
}

func TestCreateAccountAllowsSeveralWithNoCode(t *testing.T) {
	repo := newRepo(t)
	if _, err := repo.CreateAccount(context.Background(), accounting.CreateAccountInput{Name: "Primero"}); err != nil {
		t.Fatalf("create 1st account with no code: %v", err)
	}
	if _, err := repo.CreateAccount(context.Background(), accounting.CreateAccountInput{Name: "Segundo"}); err != nil {
		t.Errorf("expected a 2nd account with no code to succeed too, got %v", err)
	}
}

// TestUpdateAccountCanClearCode covers the other direction: an account
// created with a code can have it cleared back to nil through an
// ordinary update, the same as it could never have had one at all.
func TestUpdateAccountCanClearCode(t *testing.T) {
	repo := newRepo(t)
	a := mustCreateAccount(t, repo, "Caja", "570")
	updated, err := repo.UpdateAccount(context.Background(), a.ID, accounting.UpdateAccountInput{Name: a.Name})
	if err != nil {
		t.Fatalf("UpdateAccount: %v", err)
	}
	if updated.Code != nil {
		t.Errorf("expected the code to be cleared, got %q", *updated.Code)
	}
}

// An account allows neither dues nor licenses until told otherwise —
// nothing can be recorded against a transaction until an admin marks the
// accounts that actually receive those payments (migration 00017).
func TestCreateAccountFlagsDefaultToFalse(t *testing.T) {
	repo := newRepo(t)
	a := mustCreateAccount(t, repo, "Caja", "570")
	if a.AllowsDues || a.AllowsLicenses {
		t.Errorf("expected both flags false by default, got %+v", a)
	}
}

func TestCreateAccountStoresTheFlagsAndEveryReadSeesThem(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	created, err := repo.CreateAccount(ctx, accounting.CreateAccountInput{
		Name: "Cuotas", Code: strPtr("700"), Type: &income, AllowsDues: true,
	})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if !created.AllowsDues || created.AllowsLicenses {
		t.Errorf("expected only allows_dues on the created account, got %+v", created)
	}

	got, err := repo.GetAccount(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if !got.AllowsDues || got.AllowsLicenses {
		t.Errorf("GetAccount lost the flags: %+v", got)
	}

	list, err := repo.ListAccounts(ctx)
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	var listed *accounting.Account
	for i := range list {
		if list[i].ID == created.ID {
			listed = &list[i]
		}
	}
	if listed == nil || !listed.AllowsDues || listed.AllowsLicenses {
		t.Errorf("ListAccounts lost the flags: %+v", listed)
	}
}

// UpdateAccountInput replaces every field at once, the two flags
// included: sending neither turns both off rather than keeping them.
func TestUpdateAccountReplacesTheFlags(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	a, err := repo.CreateAccount(ctx, accounting.CreateAccountInput{
		Name: "Cuotas y licencias", Code: strPtr("700"), Type: &income, AllowsDues: true, AllowsLicenses: true,
	})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	onlyLicenses, err := repo.UpdateAccount(ctx, a.ID, accounting.UpdateAccountInput{
		Name: a.Name, Code: a.Code, Type: &income, AllowsLicenses: true,
	})
	if err != nil {
		t.Fatalf("UpdateAccount: %v", err)
	}
	if onlyLicenses.AllowsDues || !onlyLicenses.AllowsLicenses {
		t.Errorf("expected only allows_licenses to remain, got %+v", onlyLicenses)
	}

	neither, err := repo.UpdateAccount(ctx, a.ID, accounting.UpdateAccountInput{Name: a.Name, Code: a.Code})
	if err != nil {
		t.Fatalf("UpdateAccount: %v", err)
	}
	if neither.AllowsDues || neither.AllowsLicenses {
		t.Errorf("expected both flags cleared, got %+v", neither)
	}
}

func TestMoveAccountReturnsTheFlags(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	mustCreateAccount(t, repo, "Primera", "1")
	second, err := repo.CreateAccount(ctx, accounting.CreateAccountInput{Name: "Segunda", Code: strPtr("2"), Type: &income, AllowsDues: true})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	moved, err := repo.MoveAccount(ctx, second.ID, accounting.MoveUp)
	if err != nil {
		t.Fatalf("MoveAccount: %v", err)
	}
	if !moved.AllowsDues || moved.AllowsLicenses {
		t.Errorf("expected MoveAccount to return the account's flags, got %+v", moved)
	}
}

func TestGetAccountNotFound(t *testing.T) {
	repo := newRepo(t)
	_, err := repo.GetAccount(context.Background(), 999999)
	if !errors.Is(err, accounting.ErrAccountNotFound) {
		t.Errorf("expected ErrAccountNotFound, got %v", err)
	}
}

// TestListAccountsOrderedByCreationPosition covers the simple,
// no-nesting case: siblings (here, both top-level) list in the order
// they were created — CreateAccount appends each one after its
// sibling group's current highest Position, so creation order and
// display order start out the same until MoveAccount changes it.
func TestListAccountsOrderedByCreationPosition(t *testing.T) {
	repo := newRepo(t)
	mustCreateAccount(t, repo, "Cuotas", "700")
	mustCreateAccount(t, repo, "Caja", "570")

	list, err := repo.ListAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(list) != 2 || codeOf(list[0]) != "700" || codeOf(list[1]) != "570" {
		t.Errorf("expected [700, 570] (creation order), got %+v", list)
	}
	// Neither has any entries yet: still a real, computed (non-nil)
	// zero balance, not the same as never having computed one at all
	// (see GetAccount/CreateAccount/UpdateAccount, which leave it nil).
	for _, a := range list {
		if a.Balance == nil || *a.Balance != 0 {
			t.Errorf("expected a zero (but non-nil) balance for %s, got %+v", codeOf(a), a.Balance)
		}
		if a.LastTransactionAt != nil {
			t.Errorf("expected no last_transaction_at for %s, got %v", codeOf(a), a.LastTransactionAt)
		}
	}
}

// TestListAccountsComputesBalanceAndLastTransactionAt is the core of
// the feature: Balance sums every entry ever posted directly to that
// account (across as many transactions as touched it), and
// LastTransactionAt is the most recent occurred_at among them —
// neither rolled up from any child account.
func TestListAccountsComputesBalanceAndLastTransactionAt(t *testing.T) {
	repo := newRepo(t)
	caja := mustCreateAccount(t, repo, "Caja", "570")
	cuotas := mustCreateAccount(t, repo, "Cuotas", "700")

	older := time.Now().Add(-48 * time.Hour)
	newer := time.Now()
	if _, err := repo.CreateTransaction(context.Background(), accounting.CreateTransactionInput{
		Description: "Primera", OccurredAt: older,
		Entries: []accounting.EntryInput{{AccountID: caja.ID, Amount: 10}, {AccountID: cuotas.ID, Amount: -10}},
	}); err != nil {
		t.Fatalf("create 1st transaction: %v", err)
	}
	if _, err := repo.CreateTransaction(context.Background(), accounting.CreateTransactionInput{
		Description: "Segunda", OccurredAt: newer,
		Entries: []accounting.EntryInput{{AccountID: caja.ID, Amount: 5}, {AccountID: cuotas.ID, Amount: -5}},
	}); err != nil {
		t.Fatalf("create 2nd transaction: %v", err)
	}

	list, err := repo.ListAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	byCode := map[string]accounting.Account{codeOf(list[0]): list[0], codeOf(list[1]): list[1]}

	cajaListed := byCode["570"]
	if cajaListed.Balance == nil || *cajaListed.Balance != 15 {
		t.Errorf("expected Caja balance 15, got %+v", cajaListed.Balance)
	}
	cuotasListed := byCode["700"]
	if cuotasListed.Balance == nil || *cuotasListed.Balance != -15 {
		t.Errorf("expected Cuotas balance -15, got %+v", cuotasListed.Balance)
	}
	// A small tolerance rather than an exact round-trip match — Postgres
	// timestamptz has microsecond precision, Go's time.Time more.
	for code, a := range byCode {
		if a.LastTransactionAt == nil || a.LastTransactionAt.Sub(newer).Abs() > time.Millisecond {
			t.Errorf("expected %s's last_transaction_at close to the 2nd transaction's occurred_at, got %v", code, a.LastTransactionAt)
		}
	}
}

// TestGetCreateUpdateAccountLeaveBalanceNil covers the other half:
// unlike ListAccounts, none of these ever populate Balance/
// LastTransactionAt — they're each just the accounts row itself, no
// extra join.
func TestGetCreateUpdateAccountLeaveBalanceNil(t *testing.T) {
	repo := newRepo(t)
	caja := mustCreateAccount(t, repo, "Caja", "570")
	if caja.Balance != nil || caja.LastTransactionAt != nil {
		t.Errorf("expected CreateAccount to leave both nil, got %+v", caja)
	}

	fetched, err := repo.GetAccount(context.Background(), caja.ID)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if fetched.Balance != nil || fetched.LastTransactionAt != nil {
		t.Errorf("expected GetAccount to leave both nil, got %+v", fetched)
	}

	updated, err := repo.UpdateAccount(context.Background(), caja.ID, accounting.UpdateAccountInput{Name: "Caja chica", Code: strPtr("571")})
	if err != nil {
		t.Fatalf("UpdateAccount: %v", err)
	}
	if updated.Balance != nil || updated.LastTransactionAt != nil {
		t.Errorf("expected UpdateAccount to leave both nil, got %+v", updated)
	}
}

func mustCreateChildAccount(t *testing.T, repo *accounting.Repository, name, code string, parentID int64) accounting.Account {
	t.Helper()
	a, err := repo.CreateAccount(context.Background(), accounting.CreateAccountInput{Name: name, Code: strPtr(code), ParentID: &parentID})
	if err != nil {
		t.Fatalf("create child account %s: %v", code, err)
	}
	return a
}

func codesOf(list []accounting.Account) []string {
	codes := make([]string, len(list))
	for i, a := range list {
		codes[i] = codeOf(a)
	}
	return codes
}

// TestListAccountsChildrenBeforeNextSibling covers the tree-shaped walk
// itself: a top-level account's own children (and their own children,
// arbitrarily deep) list immediately below it, before the next
// top-level sibling — rather than a flat listing by Position alone
// regardless of nesting.
func TestListAccountsChildrenBeforeNextSibling(t *testing.T) {
	repo := newRepo(t)
	tesoreria := mustCreateAccount(t, repo, "Tesorería", "57")
	ingresos := mustCreateAccount(t, repo, "Ingresos", "70") // another top-level sibling, created after Tesorería
	caja := mustCreateChildAccount(t, repo, "Caja", "570", tesoreria.ID)
	banco := mustCreateChildAccount(t, repo, "Banco", "572", tesoreria.ID)
	cajaFuerte := mustCreateChildAccount(t, repo, "Caja fuerte", "5700", caja.ID) // a grandchild

	list, err := repo.ListAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	want := []string{codeOf(tesoreria), codeOf(caja), codeOf(cajaFuerte), codeOf(banco), codeOf(ingresos)}
	if got := codesOf(list); !reflect.DeepEqual(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestMoveAccountUpSwapsWithPrecedingSibling(t *testing.T) {
	repo := newRepo(t)
	a := mustCreateAccount(t, repo, "Primera", "1")
	b := mustCreateAccount(t, repo, "Segunda", "2")
	c := mustCreateAccount(t, repo, "Tercera", "3")

	if _, err := repo.MoveAccount(context.Background(), c.ID, accounting.MoveUp); err != nil {
		t.Fatalf("MoveAccount up: %v", err)
	}
	list, err := repo.ListAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	want := []string{codeOf(a), codeOf(c), codeOf(b)}
	if got := codesOf(list); !reflect.DeepEqual(got, want) {
		t.Errorf("expected %v after moving Tercera up, got %v", want, got)
	}
}

func TestMoveAccountDownSwapsWithFollowingSibling(t *testing.T) {
	repo := newRepo(t)
	a := mustCreateAccount(t, repo, "Primera", "1")
	b := mustCreateAccount(t, repo, "Segunda", "2")
	c := mustCreateAccount(t, repo, "Tercera", "3")

	if _, err := repo.MoveAccount(context.Background(), a.ID, accounting.MoveDown); err != nil {
		t.Fatalf("MoveAccount down: %v", err)
	}
	list, err := repo.ListAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	want := []string{codeOf(b), codeOf(a), codeOf(c)}
	if got := codesOf(list); !reflect.DeepEqual(got, want) {
		t.Errorf("expected %v after moving Primera down, got %v", want, got)
	}
}

func TestMoveAccountUpAtStartIsANoOp(t *testing.T) {
	repo := newRepo(t)
	a := mustCreateAccount(t, repo, "Primera", "1")
	b := mustCreateAccount(t, repo, "Segunda", "2")

	if _, err := repo.MoveAccount(context.Background(), a.ID, accounting.MoveUp); err != nil {
		t.Fatalf("MoveAccount up: %v", err)
	}
	list, err := repo.ListAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	want := []string{codeOf(a), codeOf(b)}
	if got := codesOf(list); !reflect.DeepEqual(got, want) {
		t.Errorf("expected no change moving the first account up, got %v", got)
	}
}

// TestMoveAccountOnlySwapsWithinSameParent confirms a move never
// reaches across to an account with a different parent_id, even one
// that would otherwise be the "next" one in the flat listing.
func TestMoveAccountOnlySwapsWithinSameParent(t *testing.T) {
	repo := newRepo(t)
	tesoreria := mustCreateAccount(t, repo, "Tesorería", "57")
	ingresos := mustCreateAccount(t, repo, "Ingresos", "70")
	caja := mustCreateChildAccount(t, repo, "Caja", "570", tesoreria.ID) // Tesorería's only child

	if _, err := repo.MoveAccount(context.Background(), caja.ID, accounting.MoveDown); err != nil {
		t.Fatalf("MoveAccount down: %v", err)
	}
	list, err := repo.ListAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	want := []string{codeOf(tesoreria), codeOf(caja), codeOf(ingresos)}
	if got := codesOf(list); !reflect.DeepEqual(got, want) {
		t.Errorf("expected %v (unchanged), got %v", want, got)
	}
}

func TestMoveAccountNotFound(t *testing.T) {
	repo := newRepo(t)
	if _, err := repo.MoveAccount(context.Background(), 999999, accounting.MoveUp); !errors.Is(err, accounting.ErrAccountNotFound) {
		t.Errorf("expected ErrAccountNotFound, got %v", err)
	}
}

func TestMoveAccountRejectsInvalidDirection(t *testing.T) {
	repo := newRepo(t)
	a := mustCreateAccount(t, repo, "Caja", "570")
	if _, err := repo.MoveAccount(context.Background(), a.ID, accounting.MoveDirection("sideways")); !errors.Is(err, accounting.ErrInvalidDirection) {
		t.Errorf("expected ErrInvalidDirection, got %v", err)
	}
}

func TestUpdateAccountRejectsSelfAsParent(t *testing.T) {
	repo := newRepo(t)
	a := mustCreateAccount(t, repo, "Caja", "570")
	_, err := repo.UpdateAccount(context.Background(), a.ID, accounting.UpdateAccountInput{Name: a.Name, Code: a.Code, ParentID: &a.ID})
	if !errors.Is(err, accounting.ErrCircularParent) {
		t.Errorf("expected ErrCircularParent, got %v", err)
	}
}

// TestUpdateAccountRejectsIndirectCycle covers the deeper case: not
// just "an account can't be its own parent" but "an account can't be
// nested under one of its own descendants either", however many levels
// removed.
func TestUpdateAccountRejectsIndirectCycle(t *testing.T) {
	repo := newRepo(t)
	parent := mustCreateAccount(t, repo, "Tesorería", "57")
	child := mustCreateChildAccount(t, repo, "Caja", "570", parent.ID)

	_, err := repo.UpdateAccount(context.Background(), parent.ID, accounting.UpdateAccountInput{
		Name: parent.Name, Code: parent.Code, ParentID: &child.ID,
	})
	if !errors.Is(err, accounting.ErrCircularParent) {
		t.Errorf("expected ErrCircularParent, got %v", err)
	}
}

func TestUpdateAccount(t *testing.T) {
	repo := newRepo(t)
	a := mustCreateAccount(t, repo, "Caja", "570")

	updated, err := repo.UpdateAccount(context.Background(), a.ID, accounting.UpdateAccountInput{Name: "Caja chica", Code: strPtr("571")})
	if err != nil {
		t.Fatalf("UpdateAccount: %v", err)
	}
	if updated.Name != "Caja chica" || codeOf(updated) != "571" {
		t.Errorf("unexpected account after update: %+v", updated)
	}
}

func TestUpdateAccountNotFound(t *testing.T) {
	repo := newRepo(t)
	_, err := repo.UpdateAccount(context.Background(), 999999, accounting.UpdateAccountInput{Name: "x", Code: strPtr("y")})
	if !errors.Is(err, accounting.ErrAccountNotFound) {
		t.Errorf("expected ErrAccountNotFound, got %v", err)
	}
}

func TestDeleteAccount(t *testing.T) {
	repo := newRepo(t)
	a := mustCreateAccount(t, repo, "Caja", "570")
	if err := repo.DeleteAccount(context.Background(), a.ID); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
	if _, err := repo.GetAccount(context.Background(), a.ID); !errors.Is(err, accounting.ErrAccountNotFound) {
		t.Errorf("expected the account to be gone, got %v", err)
	}
}

func TestDeleteAccountNotFound(t *testing.T) {
	repo := newRepo(t)
	if err := repo.DeleteAccount(context.Background(), 999999); !errors.Is(err, accounting.ErrAccountNotFound) {
		t.Errorf("expected ErrAccountNotFound, got %v", err)
	}
}

// TestDeleteAccountRejectedWhenUsedAsParent and
// TestDeleteAccountRejectedWhenReferencedByAnEntry both cover
// ErrAccountInUse — deleting an account that's still referenced (by a
// child account, or by a posted transaction entry) is rejected rather
// than silently orphaning either.
func TestDeleteAccountRejectedWhenUsedAsParent(t *testing.T) {
	repo := newRepo(t)
	parent := mustCreateAccount(t, repo, "Tesorería", "57")
	_, err := repo.CreateAccount(context.Background(), accounting.CreateAccountInput{Name: "Caja", Code: strPtr("570"), ParentID: &parent.ID})
	if err != nil {
		t.Fatalf("create child account: %v", err)
	}

	if err := repo.DeleteAccount(context.Background(), parent.ID); !errors.Is(err, accounting.ErrAccountInUse) {
		t.Errorf("expected ErrAccountInUse, got %v", err)
	}
}

func TestDeleteAccountRejectedWhenReferencedByAnEntry(t *testing.T) {
	repo := newRepo(t)
	caja := mustCreateAccount(t, repo, "Caja", "570")
	cuotas := mustCreateAccount(t, repo, "Cuotas", "700")
	mustCreateTransaction(t, repo, "Cuota", caja.ID, 10, cuotas.ID, -10)

	if err := repo.DeleteAccount(context.Background(), caja.ID); !errors.Is(err, accounting.ErrAccountInUse) {
		t.Errorf("expected ErrAccountInUse, got %v", err)
	}
}

// mustCreateTransaction is a two-entry balanced transaction, the
// common case most tests below build on.
func mustCreateTransaction(t *testing.T, repo *accounting.Repository, description string, accountA int64, amountA float64, accountB int64, amountB float64) accounting.Transaction {
	t.Helper()
	tr, err := repo.CreateTransaction(context.Background(), accounting.CreateTransactionInput{
		Description: description,
		OccurredAt:  time.Now(),
		Entries: []accounting.EntryInput{
			{AccountID: accountA, Amount: amountA},
			{AccountID: accountB, Amount: amountB},
		},
	})
	if err != nil {
		t.Fatalf("create transaction %q: %v", description, err)
	}
	return tr
}

// TestListAccountEntriesRunningBalanceAndNewestFirst covers the whole
// point of the endpoint: each of an account's own entries, in
// occurred_at order, accumulating into a running BalanceAfter — but
// listed back newest first, the same convention every other listing
// in this app uses.
func TestListAccountEntriesRunningBalanceAndNewestFirst(t *testing.T) {
	repo := newRepo(t)
	caja := mustCreateAccount(t, repo, "Caja", "570")
	cuotas := mustCreateAccount(t, repo, "Cuotas", "700")

	older := time.Now().Add(-48 * time.Hour)
	middle := time.Now().Add(-24 * time.Hour)
	newer := time.Now()
	mustCreateTransactionAt(t, repo, "Primera", older, caja.ID, 100, cuotas.ID, -100)
	mustCreateTransactionAt(t, repo, "Segunda", middle, caja.ID, -30, cuotas.ID, 30)
	mustCreateTransactionAt(t, repo, "Tercera", newer, caja.ID, 10, cuotas.ID, -10)

	entries, err := repo.ListAccountEntries(context.Background(), caja.ID, accounting.ListFilter{Limit: 50})
	if err != nil {
		t.Fatalf("ListAccountEntries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d: %+v", len(entries), entries)
	}

	// Newest first: Tercera, Segunda, Primera.
	wantDescriptions := []string{"Tercera", "Segunda", "Primera"}
	wantAmounts := []float64{10, -30, 100}
	wantBalances := []float64{80, 70, 100} // 100, then 100-30=70, then 70+10=80 — read newest first
	for i, e := range entries {
		if e.Description != wantDescriptions[i] {
			t.Errorf("entry %d: expected description %q, got %q", i, wantDescriptions[i], e.Description)
		}
		if e.Amount != wantAmounts[i] {
			t.Errorf("entry %d: expected amount %v, got %v", i, wantAmounts[i], e.Amount)
		}
		if e.BalanceAfter != wantBalances[i] {
			t.Errorf("entry %d: expected balance_after %v, got %v", i, wantBalances[i], e.BalanceAfter)
		}
	}
}

func TestListAccountEntriesEmptyForAnAccountWithNoEntries(t *testing.T) {
	repo := newRepo(t)
	caja := mustCreateAccount(t, repo, "Caja", "570")

	entries, err := repo.ListAccountEntries(context.Background(), caja.ID, accounting.ListFilter{Limit: 50})
	if err != nil {
		t.Fatalf("ListAccountEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no entries, got %+v", entries)
	}
}

// mustCreateTransactionAt is mustCreateTransaction with an explicit
// occurred_at, for tests that need entries in a specific chronological
// order rather than whatever time.Now() happens to be at each call.
func mustCreateTransactionAt(t *testing.T, repo *accounting.Repository, description string, occurredAt time.Time, accountA int64, amountA float64, accountB int64, amountB float64) accounting.Transaction {
	t.Helper()
	tr, err := repo.CreateTransaction(context.Background(), accounting.CreateTransactionInput{
		Description: description,
		OccurredAt:  occurredAt,
		Entries: []accounting.EntryInput{
			{AccountID: accountA, Amount: amountA},
			{AccountID: accountB, Amount: amountB},
		},
	})
	if err != nil {
		t.Fatalf("create transaction %q: %v", description, err)
	}
	return tr
}

func TestCreateTransactionBalanced(t *testing.T) {
	repo := newRepo(t)
	caja := mustCreateAccount(t, repo, "Caja", "570")
	cuotas := mustCreateAccount(t, repo, "Cuotas", "700")

	tr := mustCreateTransaction(t, repo, "Cuota mensual", caja.ID, 10, cuotas.ID, -10)
	if tr.ID == 0 || len(tr.Entries) != 2 {
		t.Fatalf("unexpected transaction: %+v", tr)
	}
	if tr.Entries[0].Amount != 10 || tr.Entries[1].Amount != -10 {
		t.Errorf("unexpected entry amounts: %+v", tr.Entries)
	}
}

func TestCreateTransactionMoreThanTwoEntries(t *testing.T) {
	repo := newRepo(t)
	caja := mustCreateAccount(t, repo, "Caja", "570")
	banco := mustCreateAccount(t, repo, "Banco", "572")
	cuotas := mustCreateAccount(t, repo, "Cuotas", "700")

	tr, err := repo.CreateTransaction(context.Background(), accounting.CreateTransactionInput{
		Description: "Reparto",
		OccurredAt:  time.Now(),
		Entries: []accounting.EntryInput{
			{AccountID: caja.ID, Amount: 6},
			{AccountID: banco.ID, Amount: 4},
			{AccountID: cuotas.ID, Amount: -10},
		},
	})
	if err != nil {
		t.Fatalf("CreateTransaction: %v", err)
	}
	if len(tr.Entries) != 3 {
		t.Errorf("expected 3 entries, got %d", len(tr.Entries))
	}
}

func TestCreateTransactionRejectsTooFewEntries(t *testing.T) {
	repo := newRepo(t)
	caja := mustCreateAccount(t, repo, "Caja", "570")

	_, err := repo.CreateTransaction(context.Background(), accounting.CreateTransactionInput{
		Description: "Una sola entrada",
		OccurredAt:  time.Now(),
		Entries:     []accounting.EntryInput{{AccountID: caja.ID, Amount: 10}},
	})
	if !errors.Is(err, accounting.ErrTooFewEntries) {
		t.Errorf("expected ErrTooFewEntries, got %v", err)
	}
}

func TestCreateTransactionRejectsZeroAmount(t *testing.T) {
	repo := newRepo(t)
	caja := mustCreateAccount(t, repo, "Caja", "570")
	cuotas := mustCreateAccount(t, repo, "Cuotas", "700")

	_, err := repo.CreateTransaction(context.Background(), accounting.CreateTransactionInput{
		Description: "Entrada nula",
		OccurredAt:  time.Now(),
		Entries: []accounting.EntryInput{
			{AccountID: caja.ID, Amount: 0},
			{AccountID: cuotas.ID, Amount: 0},
		},
	})
	if !errors.Is(err, accounting.ErrInvalidAmount) {
		t.Errorf("expected ErrInvalidAmount, got %v", err)
	}
}

// TestCreateTransactionRejectsUnbalancedEntries is the core invariant:
// exercised here through the Go API, but actually enforced by
// migration 00012_accounting.sql's own deferred constraint trigger at
// commit — this confirms the repository correctly surfaces that as
// ErrUnbalanced instead of a raw database error.
func TestCreateTransactionRejectsUnbalancedEntries(t *testing.T) {
	repo := newRepo(t)
	caja := mustCreateAccount(t, repo, "Caja", "570")
	cuotas := mustCreateAccount(t, repo, "Cuotas", "700")

	_, err := repo.CreateTransaction(context.Background(), accounting.CreateTransactionInput{
		Description: "Desbalanceada",
		OccurredAt:  time.Now(),
		Entries: []accounting.EntryInput{
			{AccountID: caja.ID, Amount: 10},
			{AccountID: cuotas.ID, Amount: -5},
		},
	})
	if !errors.Is(err, accounting.ErrUnbalanced) {
		t.Errorf("expected ErrUnbalanced, got %v", err)
	}

	// Nothing should have been left behind — the whole write rolls
	// back, transaction row included, not just the entries.
	list, listErr := repo.ListTransactions(context.Background(), accounting.ListFilter{Limit: 10})
	if listErr != nil {
		t.Fatalf("ListTransactions: %v", listErr)
	}
	if len(list) != 0 {
		t.Errorf("expected no transaction to have been left behind, got %+v", list)
	}
}

func TestCreateTransactionRejectsUnknownAccount(t *testing.T) {
	repo := newRepo(t)
	caja := mustCreateAccount(t, repo, "Caja", "570")

	_, err := repo.CreateTransaction(context.Background(), accounting.CreateTransactionInput{
		Description: "Cuenta desconocida",
		OccurredAt:  time.Now(),
		Entries: []accounting.EntryInput{
			{AccountID: caja.ID, Amount: 10},
			{AccountID: 999999, Amount: -10},
		},
	})
	if !errors.Is(err, accounting.ErrUnknownAccount) {
		t.Errorf("expected ErrUnknownAccount, got %v", err)
	}
}

func TestGetTransactionNotFound(t *testing.T) {
	repo := newRepo(t)
	_, err := repo.GetTransaction(context.Background(), 999999)
	if !errors.Is(err, accounting.ErrTransactionNotFound) {
		t.Errorf("expected ErrTransactionNotFound, got %v", err)
	}
}

func TestListTransactionsMostRecentFirst(t *testing.T) {
	repo := newRepo(t)
	caja := mustCreateAccount(t, repo, "Caja", "570")
	cuotas := mustCreateAccount(t, repo, "Cuotas", "700")

	older, err := repo.CreateTransaction(context.Background(), accounting.CreateTransactionInput{
		Description: "Más antigua",
		OccurredAt:  time.Now().Add(-48 * time.Hour),
		Entries:     []accounting.EntryInput{{AccountID: caja.ID, Amount: 1}, {AccountID: cuotas.ID, Amount: -1}},
	})
	if err != nil {
		t.Fatalf("create older transaction: %v", err)
	}
	newer := mustCreateTransaction(t, repo, "Más reciente", caja.ID, 2, cuotas.ID, -2)

	list, err := repo.ListTransactions(context.Background(), accounting.ListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}
	if len(list) != 2 || list[0].ID != newer.ID || list[1].ID != older.ID {
		t.Errorf("expected [newer, older], got %+v", list)
	}
	if len(list[0].Entries) != 2 || len(list[1].Entries) != 2 {
		t.Errorf("expected each listed transaction to carry its own entries, got %+v", list)
	}
}

// TestUpdateTransactionReplacesEntries covers UpdateTransaction's own
// "delete every existing entry, insert the given set" behavior.
func TestUpdateTransactionReplacesEntries(t *testing.T) {
	repo := newRepo(t)
	caja := mustCreateAccount(t, repo, "Caja", "570")
	cuotas := mustCreateAccount(t, repo, "Cuotas", "700")
	tr := mustCreateTransaction(t, repo, "Original", caja.ID, 10, cuotas.ID, -10)

	updated, err := repo.UpdateTransaction(context.Background(), tr.ID, accounting.UpdateTransactionInput{
		Description: "Corregida",
		OccurredAt:  tr.OccurredAt,
		Entries: []accounting.EntryInput{
			{AccountID: caja.ID, Amount: 20},
			{AccountID: cuotas.ID, Amount: -20},
		},
	})
	if err != nil {
		t.Fatalf("UpdateTransaction: %v", err)
	}
	if updated.Description != "Corregida" || len(updated.Entries) != 2 || updated.Entries[0].Amount != 20 {
		t.Errorf("unexpected transaction after update: %+v", updated)
	}

	fetched, err := repo.GetTransaction(context.Background(), tr.ID)
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if len(fetched.Entries) != 2 || fetched.Entries[0].Amount != 20 {
		t.Errorf("expected the replaced entries to persist, got %+v", fetched.Entries)
	}
}

// TestUpdateTransactionRejectsUnbalancedEntriesLeavesOriginalIntact
// covers atomicity: an update that would leave the transaction
// unbalanced rolls back entirely, so the transaction keeps its
// original, still-balanced entries rather than being left half-edited.
func TestUpdateTransactionRejectsUnbalancedEntriesLeavesOriginalIntact(t *testing.T) {
	repo := newRepo(t)
	caja := mustCreateAccount(t, repo, "Caja", "570")
	cuotas := mustCreateAccount(t, repo, "Cuotas", "700")
	tr := mustCreateTransaction(t, repo, "Original", caja.ID, 10, cuotas.ID, -10)

	_, err := repo.UpdateTransaction(context.Background(), tr.ID, accounting.UpdateTransactionInput{
		Description: "Rota",
		OccurredAt:  tr.OccurredAt,
		Entries: []accounting.EntryInput{
			{AccountID: caja.ID, Amount: 20},
			{AccountID: cuotas.ID, Amount: -5},
		},
	})
	if !errors.Is(err, accounting.ErrUnbalanced) {
		t.Fatalf("expected ErrUnbalanced, got %v", err)
	}

	fetched, err := repo.GetTransaction(context.Background(), tr.ID)
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if fetched.Description != "Original" || len(fetched.Entries) != 2 || fetched.Entries[0].Amount != 10 {
		t.Errorf("expected the original transaction to survive a rolled-back update untouched, got %+v", fetched)
	}
}

func TestUpdateTransactionNotFound(t *testing.T) {
	repo := newRepo(t)
	caja := mustCreateAccount(t, repo, "Caja", "570")
	cuotas := mustCreateAccount(t, repo, "Cuotas", "700")

	_, err := repo.UpdateTransaction(context.Background(), 999999, accounting.UpdateTransactionInput{
		Description: "x",
		OccurredAt:  time.Now(),
		Entries:     []accounting.EntryInput{{AccountID: caja.ID, Amount: 1}, {AccountID: cuotas.ID, Amount: -1}},
	})
	if !errors.Is(err, accounting.ErrTransactionNotFound) {
		t.Errorf("expected ErrTransactionNotFound, got %v", err)
	}
}

// TestDeleteTransactionCascadesToEntriesWithoutFalseRejection is the
// regression test for the trigger's own commit-time
// "does the transaction row still exist" guard (see migration
// 00012_accounting.sql): deleting a whole transaction cascades to
// every one of its entries, each of which fires the same balance
// trigger — without that guard, this would wrongly fail as "too few
// entries" once the last one is removed.
func TestDeleteTransactionCascadesToEntriesWithoutFalseRejection(t *testing.T) {
	repo := newRepo(t)
	caja := mustCreateAccount(t, repo, "Caja", "570")
	cuotas := mustCreateAccount(t, repo, "Cuotas", "700")
	tr := mustCreateTransaction(t, repo, "A borrar", caja.ID, 10, cuotas.ID, -10)

	if err := repo.DeleteTransaction(context.Background(), tr.ID); err != nil {
		t.Fatalf("DeleteTransaction: %v", err)
	}
	if _, err := repo.GetTransaction(context.Background(), tr.ID); !errors.Is(err, accounting.ErrTransactionNotFound) {
		t.Errorf("expected the transaction to be gone, got %v", err)
	}
}

func TestDeleteTransactionNotFound(t *testing.T) {
	repo := newRepo(t)
	if err := repo.DeleteTransaction(context.Background(), 999999); !errors.Is(err, accounting.ErrTransactionNotFound) {
		t.Errorf("expected ErrTransactionNotFound, got %v", err)
	}
}

func TestCreateTransactionRejectsDuplicateAccount(t *testing.T) {
	repo := newRepo(t)
	caja := mustCreateAccount(t, repo, "Caja", "570")
	cuotas := mustCreateAccount(t, repo, "Cuotas", "700")

	_, err := repo.CreateTransaction(context.Background(), accounting.CreateTransactionInput{
		Description: "Cuenta repetida",
		OccurredAt:  time.Now(),
		Entries: []accounting.EntryInput{
			{AccountID: caja.ID, Amount: 10},
			{AccountID: caja.ID, Amount: 5},
			{AccountID: cuotas.ID, Amount: -15},
		},
	})
	if !errors.Is(err, accounting.ErrDuplicateAccount) {
		t.Errorf("expected ErrDuplicateAccount, got %v", err)
	}
}

// TestListAccountEntriesPagingAndRangeKeepRunningBalance checks that a
// page or a date range is just a window over the same ledger: the
// balance_after of an entry doesn't restart from zero at the window's
// edge.
func TestListAccountEntriesPagingAndRangeKeepRunningBalance(t *testing.T) {
	repo := newRepo(t)
	caja := mustCreateAccount(t, repo, "Caja", "570")
	cuotas := mustCreateAccount(t, repo, "Cuotas", "700")
	day := func(s string) time.Time {
		d, err := time.Parse("2006-01-02", s)
		if err != nil {
			t.Fatal(err)
		}
		return d.Add(12 * time.Hour)
	}
	mustCreateTransactionAt(t, repo, "Enero", day("2024-01-10"), caja.ID, 100, cuotas.ID, -100)
	mustCreateTransactionAt(t, repo, "Febrero", day("2024-02-10"), caja.ID, -30, cuotas.ID, 30)
	mustCreateTransactionAt(t, repo, "Marzo", day("2024-03-10"), caja.ID, 10, cuotas.ID, -10)

	page, err := repo.ListAccountEntries(context.Background(), caja.ID, accounting.ListFilter{Limit: 1, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Description != "Febrero" || page[0].BalanceAfter != 70 {
		t.Errorf("second page: expected Febrero with balance 70, got %+v", page)
	}

	from, to := day("2024-02-10").Truncate(24*time.Hour), day("2024-03-10").Truncate(24*time.Hour)
	ranged, err := repo.ListAccountEntries(context.Background(), caja.ID, accounting.ListFilter{Limit: 50, From: &from, To: &to})
	if err != nil {
		t.Fatal(err)
	}
	if len(ranged) != 2 || ranged[0].Description != "Marzo" || ranged[0].BalanceAfter != 80 || ranged[1].BalanceAfter != 70 {
		t.Errorf("range: expected Marzo (80) and Febrero (70), got %+v", ranged)
	}
}

func TestListTransactionsPagingAndRange(t *testing.T) {
	repo := newRepo(t)
	caja := mustCreateAccount(t, repo, "Caja", "570")
	cuotas := mustCreateAccount(t, repo, "Cuotas", "700")
	day := func(s string) time.Time {
		d, _ := time.Parse("2006-01-02", s)
		return d.Add(12 * time.Hour)
	}
	mustCreateTransactionAt(t, repo, "A", day("2023-12-31"), caja.ID, 1, cuotas.ID, -1)
	mustCreateTransactionAt(t, repo, "B", day("2024-01-01"), caja.ID, 1, cuotas.ID, -1)
	mustCreateTransactionAt(t, repo, "C", day("2024-12-31"), caja.ID, 1, cuotas.ID, -1)
	mustCreateTransactionAt(t, repo, "D", day("2025-01-01"), caja.ID, 1, cuotas.ID, -1)

	from, _ := time.Parse("2006-01-02", "2024-01-01")
	to, _ := time.Parse("2006-01-02", "2024-12-31")
	list, err := repo.ListTransactions(context.Background(), accounting.ListFilter{Limit: 50, From: &from, To: &to})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Description != "C" || list[1].Description != "B" {
		t.Errorf("2024 range (both ends inclusive): expected C, B, got %+v", list)
	}

	page, err := repo.ListTransactions(context.Background(), accounting.ListFilter{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].Description != "B" || page[1].Description != "A" {
		t.Errorf("second page: expected B, A, got %+v", page)
	}
}

// TestAccountHiddenFlagRoundTrips covers the flag everywhere an account
// is read back: it starts false, is stored on create, shows in the
// listing, and is replaced (so cleared again) by an update.
func TestAccountHiddenFlagRoundTrips(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	plain := mustCreateAccount(t, repo, "Caja", "570")
	if plain.Hidden {
		t.Errorf("expected an account to start visible, got %+v", plain)
	}
	old, err := repo.CreateAccount(ctx, accounting.CreateAccountInput{Name: "Antigua", Code: strPtr("999"), Hidden: true})
	if err != nil {
		t.Fatal(err)
	}
	if !old.Hidden {
		t.Errorf("expected the created account to be hidden, got %+v", old)
	}

	listed, err := repo.ListAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hidden := map[string]bool{}
	for _, a := range listed {
		hidden[a.Name] = a.Hidden
	}
	if hidden["Caja"] || !hidden["Antigua"] {
		t.Errorf("listing should carry the flag, got %v", hidden)
	}

	shown, err := repo.UpdateAccount(ctx, old.ID, accounting.UpdateAccountInput{Name: "Antigua", Code: strPtr("999")})
	if err != nil {
		t.Fatal(err)
	}
	if shown.Hidden {
		t.Errorf("expected an update that leaves hidden out to clear it, got %+v", shown)
	}
}

// An account that groups others has no entries of its own: ListAccounts gives it
// the sum of its descendants' balances (however deep) and their latest movement.
func TestListAccountsRollsUpGroupsFromTheirDescendants(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	group := mustCreateAccount(t, repo, "Bancos", "572")
	bank, err := repo.CreateAccount(ctx, accounting.CreateAccountInput{Name: "Santander", ParentID: &group.ID})
	if err != nil {
		t.Fatal(err)
	}
	checking, err := repo.CreateAccount(ctx, accounting.CreateAccountInput{Name: "Corriente", ParentID: &bank.ID})
	if err != nil {
		t.Fatal(err)
	}
	other, err := repo.CreateAccount(ctx, accounting.CreateAccountInput{Name: "BBVA", ParentID: &group.ID})
	if err != nil {
		t.Fatal(err)
	}
	income := mustCreateAccount(t, repo, "Ingresos", "705")
	older := time.Now().Add(-72 * time.Hour)
	newer := time.Now().Add(-24 * time.Hour)
	for _, tc := range []struct {
		account int64
		amount  float64
		at      time.Time
	}{{checking.ID, 100, older}, {other.ID, 40, newer}} {
		if _, err := repo.CreateTransaction(ctx, accounting.CreateTransactionInput{
			Description: "Cuota", OccurredAt: tc.at,
			Entries: []accounting.EntryInput{{AccountID: tc.account, Amount: tc.amount}, {AccountID: income.ID, Amount: -tc.amount}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := repo.ListAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int64]accounting.Account{}
	for _, a := range list {
		byID[a.ID] = a
	}
	for _, c := range []struct {
		id      int64
		balance float64
		last    time.Time
	}{{group.ID, 140, newer}, {bank.ID, 100, older}, {checking.ID, 100, older}, {other.ID, 40, newer}} {
		a := byID[c.id]
		if a.Balance == nil || *a.Balance != c.balance || a.LastTransactionAt == nil || a.LastTransactionAt.Sub(c.last).Abs() > time.Millisecond {
			t.Errorf("%s: expected balance %v and last %v, got %v and %v", a.Name, c.balance, c.last, a.Balance, a.LastTransactionAt)
		}
	}
}
