// Package accounting tracks the club's own money: a chart of accounts
// (Account, each optionally nested under a broader one) and, against
// that chart, Transactions — every real movement of money the club
// records, each made up of two or more Entries whose amounts always
// sum to zero. Standard double-entry bookkeeping: a transaction always
// says both where money came from and where it went, never just one
// side of it. See migration 00012_accounting.sql for the schema this
// builds on, including the deferred constraint trigger that's the
// actual, unbypassable source of truth for "at least two entries,
// summing to zero" — this package's own validation exists only to
// answer with a clean error instead of a raw database one.
package accounting

import "time"

// Account is one node in the chart of accounts. Code is its own short,
// unique identifier (e.g. "570" for "Caja"); ParentID nests it under a
// broader one (nil for a top-level account).
type Account struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Code     string `json:"code"`
	ParentID *int64 `json:"parent_id,omitempty"`
	// Balance and LastTransactionAt are this account's own direct
	// activity — the sum of every entry ever posted straight to it
	// (never rolled up from child accounts) and when the most recent
	// one occurred (nil if never). Both are only ever populated by
	// ListAccounts (see GET /accounts, Repository.ListAccounts) — left
	// nil everywhere else (GetAccount, CreateAccount, UpdateAccount),
	// since computing them costs an extra join those single-account
	// paths don't need. Balance is still a non-nil pointer to exactly 0
	// for an account with no entries at all, so it reads as "computed,
	// and it's zero" rather than being indistinguishable, via
	// omitempty, from an endpoint that never computed it at all.
	Balance           *float64   `json:"balance,omitempty"`
	LastTransactionAt *time.Time `json:"last_transaction_at,omitempty"`
}

type CreateAccountInput struct {
	Name     string
	Code     string
	ParentID *int64
}

// UpdateAccountInput replaces every field at once — the same
// all-fields-required convention board.UpdateInput/memberships.UpdateInput
// already use, since the edit form always resends the current values
// rather than leaving anything ambiguous.
type UpdateAccountInput struct {
	Name     string
	Code     string
	ParentID *int64
}

// Entry is one line of a Transaction: Amount moved into (positive) or
// out of (negative) AccountID. Never created, read, or deleted on its
// own — always as part of its own Transaction.
type Entry struct {
	ID        int64   `json:"id"`
	AccountID int64   `json:"account_id"`
	Amount    float64 `json:"amount"`
}

// EntryInput is one entry to write as part of CreateTransactionInput or
// UpdateTransactionInput.
type EntryInput struct {
	AccountID int64
	Amount    float64
}

// Transaction is a single, dated, balanced movement of money — Entries
// always number two or more and always sum to exactly zero, enforced by
// migration 00012_accounting.sql's own deferred constraint trigger
// regardless of what wrote them.
type Transaction struct {
	ID          int64     `json:"id"`
	Description string    `json:"description"`
	OccurredAt  time.Time `json:"occurred_at"`
	CreatedAt   time.Time `json:"created_at"`
	Entries     []Entry   `json:"entries"`
}

type CreateTransactionInput struct {
	Description string
	OccurredAt  time.Time
	Entries     []EntryInput
}

// UpdateTransactionInput replaces a transaction's description,
// occurred_at, and its entire set of entries at once — the same
// "resend everything" convention UpdateAccountInput uses, and the only
// way to correct a wrong or unbalanced entry after the fact: there's no
// single-entry edit endpoint (see Handler.updateTransaction).
type UpdateTransactionInput struct {
	Description string
	OccurredAt  time.Time
	Entries     []EntryInput
}
