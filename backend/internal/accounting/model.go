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
// unique identifier (e.g. "570" for "Caja") — optional, since a purely
// organizational grouping node might never get one the way a real
// ledger account does (see migration 00013_accounts_code_optional.sql);
// nil rather than "" for "no code", so an empty string is never itself
// a real value two different accounts could collide on under the
// column's own UNIQUE constraint (see Handler.normalizeCode, which
// keeps that distinction all the way from the request body down).
// ParentID nests it under a broader one (nil for a top-level account).
// AllowsDues/AllowsLicenses mark the accounts a transaction has to touch
// to be allowed to record membership dues (cuotas anuales) or federative
// licenses (see migration 00017_accounts_dues_licenses_flags.sql, and
// memberships.Service.RecordDue/licenses.Service.Create, which refuse a
// transaction with no entry on a flagged account). Unlike Code or
// ParentID they're plain bools, always present in the JSON — false is a
// real answer, not an absent value. Hidden marks an account that's no
// longer used (closed, obsolete): it can't be deleted — its history has
// to stay — so it's only left out of what the frontend shows by default
// (see migration 00019_accounts_hidden.sql). Nothing in the API treats
// it differently: it's listed, readable, editable and postable like any
// other, and the flag is only ever acted on by whoever reads it.
type Account struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	Code           *string `json:"code,omitempty"`
	ParentID       *int64  `json:"parent_id,omitempty"`
	AllowsDues     bool    `json:"allows_dues"`
	AllowsLicenses bool    `json:"allows_licenses"`
	Hidden         bool    `json:"hidden"`
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
	Name           string
	Code           *string
	ParentID       *int64
	AllowsDues     bool
	AllowsLicenses bool
	Hidden         bool
}

// UpdateAccountInput replaces every field at once — the same
// all-fields-required convention board.UpdateInput/memberships.UpdateInput
// already use, since the edit form always resends the current values
// rather than leaving anything ambiguous. There's no Position field
// here — reordering only ever happens through MoveAccount/MoveDirection
// below, never as a side effect of an otherwise ordinary edit. Code is
// the one field that isn't actually required to be non-empty (see
// Account's own doc comment) — resending nil/"" here clears an
// existing code, same as never having set one. The two flags follow the
// same replace-everything rule: leaving one out of the request means
// false, not "keep what it was" — Hidden included.
type UpdateAccountInput struct {
	Name           string
	Code           *string
	ParentID       *int64
	AllowsDues     bool
	AllowsLicenses bool
	Hidden         bool
}

// MoveDirection is which adjacent sibling (same parent_id) to swap an
// account's display position with — see Repository.MoveAccount.
type MoveDirection string

const (
	MoveUp   MoveDirection = "up"
	MoveDown MoveDirection = "down"
)

// Entry is one line of a Transaction: Amount moved into (positive) or
// out of (negative) AccountID. Never created, read, or deleted on its
// own — always as part of its own Transaction.
type Entry struct {
	ID        int64   `json:"id"`
	AccountID int64   `json:"account_id"`
	Amount    float64 `json:"amount"`
}

// AccountEntry is one line of a single account's own ledger — GET
// /accounts/{id}/transactions's own view (see
// ListFilter pages and narrows GET /transactions and
// GET /accounts/{id}/transactions: Limit/Offset page through the
// newest-first order, and From/To (both optional, both inclusive, whole
// UTC days) keep only what occurred within that range. Both listings
// grow without bound as years of ledger pile up, so neither ever answers
// with everything.
type ListFilter struct {
	Limit  int
	Offset int
	From   *time.Time // start of the first day included
	To     *time.Time // start of the last day included
}

// Repository.ListAccountEntries), not a generic cross-account listing:
// this account's own entry from one transaction that touched it,
// alongside that transaction's own date/description and, unlike Entry
// itself, this account's own running BalanceAfter once that entry is
// applied (in occurred_at order — the same "at, then id" tiebreak
// ListTransactions already sorts by, so two transactions dated the
// same day still resolve deterministically). Listed newest first,
// matching every other listing in this app, even though BalanceAfter
// is necessarily computed oldest-first internally.
type AccountEntry struct {
	TransactionID int64     `json:"transaction_id"`
	OccurredAt    time.Time `json:"occurred_at"`
	Description   string    `json:"description"`
	Amount        float64   `json:"amount"`
	BalanceAfter  float64   `json:"balance_after"`
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
