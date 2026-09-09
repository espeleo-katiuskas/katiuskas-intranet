package accounting_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/accounting"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
)

func newRepo(t *testing.T) *accounting.Repository {
	tx := testutil.NewTx(t)
	return accounting.NewRepository(tx)
}

func mustCreateAccount(t *testing.T, repo *accounting.Repository, name, code string) accounting.Account {
	t.Helper()
	a, err := repo.CreateAccount(context.Background(), accounting.CreateAccountInput{Name: name, Code: code})
	if err != nil {
		t.Fatalf("create account %s: %v", code, err)
	}
	return a
}

func TestCreateAccount(t *testing.T) {
	repo := newRepo(t)
	a, err := repo.CreateAccount(context.Background(), accounting.CreateAccountInput{Name: "Caja", Code: "570"})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if a.ID == 0 || a.Name != "Caja" || a.Code != "570" || a.ParentID != nil {
		t.Errorf("unexpected account: %+v", a)
	}
}

func TestCreateAccountWithParent(t *testing.T) {
	repo := newRepo(t)
	parent := mustCreateAccount(t, repo, "Tesorería", "57")
	child, err := repo.CreateAccount(context.Background(), accounting.CreateAccountInput{
		Name: "Caja", Code: "570", ParentID: &parent.ID,
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
	_, err := repo.CreateAccount(context.Background(), accounting.CreateAccountInput{Name: "Caja", Code: "570", ParentID: &bogus})
	if !errors.Is(err, accounting.ErrUnknownAccount) {
		t.Errorf("expected ErrUnknownAccount, got %v", err)
	}
}

func TestCreateAccountRejectsDuplicateCode(t *testing.T) {
	repo := newRepo(t)
	mustCreateAccount(t, repo, "Caja", "570")
	_, err := repo.CreateAccount(context.Background(), accounting.CreateAccountInput{Name: "Otra caja", Code: "570"})
	if !errors.Is(err, accounting.ErrDuplicateCode) {
		t.Errorf("expected ErrDuplicateCode, got %v", err)
	}
}

func TestGetAccountNotFound(t *testing.T) {
	repo := newRepo(t)
	_, err := repo.GetAccount(context.Background(), 999999)
	if !errors.Is(err, accounting.ErrAccountNotFound) {
		t.Errorf("expected ErrAccountNotFound, got %v", err)
	}
}

func TestListAccountsOrderedByCode(t *testing.T) {
	repo := newRepo(t)
	mustCreateAccount(t, repo, "Cuotas", "700")
	mustCreateAccount(t, repo, "Caja", "570")

	list, err := repo.ListAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(list) != 2 || list[0].Code != "570" || list[1].Code != "700" {
		t.Errorf("expected [570, 700] in order, got %+v", list)
	}
	// Neither has any entries yet: still a real, computed (non-nil)
	// zero balance, not the same as never having computed one at all
	// (see GetAccount/CreateAccount/UpdateAccount, which leave it nil).
	for _, a := range list {
		if a.Balance == nil || *a.Balance != 0 {
			t.Errorf("expected a zero (but non-nil) balance for %s, got %+v", a.Code, a.Balance)
		}
		if a.LastTransactionAt != nil {
			t.Errorf("expected no last_transaction_at for %s, got %v", a.Code, a.LastTransactionAt)
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
	byCode := map[string]accounting.Account{list[0].Code: list[0], list[1].Code: list[1]}

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

	updated, err := repo.UpdateAccount(context.Background(), caja.ID, accounting.UpdateAccountInput{Name: "Caja chica", Code: "571"})
	if err != nil {
		t.Fatalf("UpdateAccount: %v", err)
	}
	if updated.Balance != nil || updated.LastTransactionAt != nil {
		t.Errorf("expected UpdateAccount to leave both nil, got %+v", updated)
	}
}

func TestUpdateAccount(t *testing.T) {
	repo := newRepo(t)
	a := mustCreateAccount(t, repo, "Caja", "570")

	updated, err := repo.UpdateAccount(context.Background(), a.ID, accounting.UpdateAccountInput{Name: "Caja chica", Code: "571"})
	if err != nil {
		t.Fatalf("UpdateAccount: %v", err)
	}
	if updated.Name != "Caja chica" || updated.Code != "571" {
		t.Errorf("unexpected account after update: %+v", updated)
	}
}

func TestUpdateAccountNotFound(t *testing.T) {
	repo := newRepo(t)
	_, err := repo.UpdateAccount(context.Background(), 999999, accounting.UpdateAccountInput{Name: "x", Code: "y"})
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
	_, err := repo.CreateAccount(context.Background(), accounting.CreateAccountInput{Name: "Caja", Code: "570", ParentID: &parent.ID})
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
	list, listErr := repo.ListTransactions(context.Background(), 10, 0)
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

	list, err := repo.ListTransactions(context.Background(), 10, 0)
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
