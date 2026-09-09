package accounting

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/db"
)

var (
	ErrAccountNotFound = errors.New("account not found")
	// ErrUnknownAccount means an account_id given as input (an
	// account's own parent_id, or an entry's account_id) doesn't exist
	// — distinct from ErrAccountNotFound, which is for a path-addressed
	// /accounts/{id} that doesn't exist, the same "Unknown vs NotFound"
	// split activities.ErrParticipantNotFound uses for its own
	// input-referenced ids.
	ErrUnknownAccount = errors.New("referenced account does not exist")
	// ErrDuplicateCode means another account already has this code.
	ErrDuplicateCode = errors.New("another account already has this code")
	// ErrAccountInUse means this account can't be deleted: it's still
	// referenced by a child account (its own parent_id) or a posted
	// transaction entry, and deleting it would either orphan that
	// subtree or silently erase financial history.
	ErrAccountInUse = errors.New("account is still referenced by a child account or a transaction entry")

	ErrTransactionNotFound = errors.New("transaction not found")
	// ErrTooFewEntries means fewer than two entries were given — a
	// transaction always moves money between at least two accounts.
	ErrTooFewEntries = errors.New("a transaction needs at least two entries")
	// ErrInvalidAmount means one of the given entries has a zero
	// amount — a no-op leg, meaningless in a ledger.
	ErrInvalidAmount = errors.New("entry amount must not be zero")
	// ErrUnbalanced means the given entries don't sum to zero. Checked
	// here only as a best-effort early exit; migration
	// 00012_accounting.sql's own deferred constraint trigger is what
	// actually enforces this, at commit, regardless of what called it.
	ErrUnbalanced = errors.New("transaction entries must sum to zero")
)

const (
	uniqueViolationCode     = "23505"
	foreignKeyViolationCode = "23503"
	checkViolationCode      = "23514"
)

func pgErrCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

type Repository struct {
	pool db.PGX
}

func NewRepository(pool db.PGX) *Repository {
	return &Repository{pool: pool}
}

// ---- Accounts ----

func (r *Repository) CreateAccount(ctx context.Context, in CreateAccountInput) (Account, error) {
	var a Account
	err := r.pool.QueryRow(ctx, `
		INSERT INTO accounts (name, code, parent_id)
		VALUES ($1,$2,$3)
		RETURNING id, name, code, parent_id
	`, in.Name, in.Code, in.ParentID).Scan(&a.ID, &a.Name, &a.Code, &a.ParentID)
	switch pgErrCode(err) {
	case uniqueViolationCode:
		return Account{}, ErrDuplicateCode
	case foreignKeyViolationCode:
		return Account{}, ErrUnknownAccount
	}
	return a, err
}

func (r *Repository) GetAccount(ctx context.Context, id int64) (Account, error) {
	var a Account
	err := r.pool.QueryRow(ctx, `
		SELECT id, name, code, parent_id FROM accounts WHERE id = $1
	`, id).Scan(&a.ID, &a.Name, &a.Code, &a.ParentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrAccountNotFound
	}
	return a, err
}

// ListAccounts returns every account, ordered by code — the natural
// chart-of-accounts order, and stable regardless of parent/child
// nesting (the frontend builds any tree view it wants from ParentID
// itself, same as members.js's own client-side grouping elsewhere).
func (r *Repository) ListAccounts(ctx context.Context) ([]Account, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, name, code, parent_id FROM accounts ORDER BY code
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Account{}
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.ID, &a.Name, &a.Code, &a.ParentID); err != nil {
			return nil, err
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

func (r *Repository) UpdateAccount(ctx context.Context, id int64, in UpdateAccountInput) (Account, error) {
	var a Account
	err := r.pool.QueryRow(ctx, `
		UPDATE accounts SET name = $1, code = $2, parent_id = $3
		WHERE id = $4
		RETURNING id, name, code, parent_id
	`, in.Name, in.Code, in.ParentID, id).Scan(&a.ID, &a.Name, &a.Code, &a.ParentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrAccountNotFound
	}
	switch pgErrCode(err) {
	case uniqueViolationCode:
		return Account{}, ErrDuplicateCode
	case foreignKeyViolationCode:
		return Account{}, ErrUnknownAccount
	}
	return a, err
}

func (r *Repository) DeleteAccount(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, id)
	if err != nil {
		if pgErrCode(err) == foreignKeyViolationCode {
			return ErrAccountInUse
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrAccountNotFound
	}
	return nil
}

// ---- Transactions ----

// CreateTransaction inserts the transaction and every one of its
// entries as one DB transaction (see Repository.pool.Begin — a
// transaction on top of whatever this repository is itself already
// built on, a real pool in production or a pgx.Tx in tests, same as
// activities.Repository.Create does for an activity's initial
// participants), so an unbalanced set — or one that fails partway,
// e.g. a bad account_id — rolls back the whole thing instead of
// leaving a half-written transaction behind. validateEntries only
// catches the two things a plain Go loop can check without hitting the
// database at all (fewer than two entries, a zero-amount one); "sums to
// zero" is left entirely to the deferred constraint trigger, which
// operates on Postgres's own exact numeric arithmetic rather than
// float64 sums that a handful of money values can leave with residue.
func (r *Repository) CreateTransaction(ctx context.Context, in CreateTransactionInput) (Transaction, error) {
	if err := validateEntries(in.Entries); err != nil {
		return Transaction{}, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Transaction{}, err
	}
	defer tx.Rollback(ctx)

	var t Transaction
	err = tx.QueryRow(ctx, `
		INSERT INTO transactions (description, occurred_at)
		VALUES ($1,$2)
		RETURNING id, description, occurred_at, created_at
	`, in.Description, in.OccurredAt).Scan(&t.ID, &t.Description, &t.OccurredAt, &t.CreatedAt)
	if err != nil {
		return Transaction{}, err
	}

	entries, err := insertEntries(ctx, tx, t.ID, in.Entries)
	if err != nil {
		return Transaction{}, err
	}
	t.Entries = entries

	if err := checkBalance(ctx, tx); err != nil {
		return Transaction{}, err
	}
	return t, tx.Commit(ctx)
}

func (r *Repository) GetTransaction(ctx context.Context, id int64) (Transaction, error) {
	var t Transaction
	err := r.pool.QueryRow(ctx, `
		SELECT id, description, occurred_at, created_at FROM transactions WHERE id = $1
	`, id).Scan(&t.ID, &t.Description, &t.OccurredAt, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transaction{}, ErrTransactionNotFound
	}
	if err != nil {
		return Transaction{}, err
	}
	entries, err := r.entriesForTransaction(ctx, id)
	if err != nil {
		return Transaction{}, err
	}
	t.Entries = entries
	return t, nil
}

// ListTransactions returns the limit most recent transactions (most
// recently occurred first), each with its own entries — one query for
// the transactions themselves, then one batched query for every one of
// their entries (rather than one query per transaction) to keep this to
// two round trips regardless of how many transactions are returned.
func (r *Repository) ListTransactions(ctx context.Context, limit, offset int) ([]Transaction, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, description, occurred_at, created_at FROM transactions
		ORDER BY occurred_at DESC, id DESC LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	list := []Transaction{}
	ids := make([]int64, 0)
	for rows.Next() {
		var t Transaction
		if err := rows.Scan(&t.ID, &t.Description, &t.OccurredAt, &t.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, t)
		ids = append(ids, t.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return list, nil
	}

	entryRows, err := r.pool.Query(ctx, `
		SELECT transaction_id, id, account_id, amount FROM transaction_entries
		WHERE transaction_id = ANY($1) ORDER BY transaction_id, id
	`, ids)
	if err != nil {
		return nil, err
	}
	defer entryRows.Close()

	byTransaction := make(map[int64][]Entry, len(ids))
	for entryRows.Next() {
		var transactionID int64
		var e Entry
		if err := entryRows.Scan(&transactionID, &e.ID, &e.AccountID, &e.Amount); err != nil {
			return nil, err
		}
		byTransaction[transactionID] = append(byTransaction[transactionID], e)
	}
	if err := entryRows.Err(); err != nil {
		return nil, err
	}
	for i := range list {
		list[i].Entries = byTransaction[list[i].ID]
	}
	return list, nil
}

// UpdateTransaction replaces a transaction's description, occurred_at
// and its entire set of entries at once (delete every existing entry,
// insert the given set) inside one DB transaction — the deferred
// constraint trigger only validates the final state at commit, so the
// brief moment with zero entries mid-replace is never itself checked.
func (r *Repository) UpdateTransaction(ctx context.Context, id int64, in UpdateTransactionInput) (Transaction, error) {
	if err := validateEntries(in.Entries); err != nil {
		return Transaction{}, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Transaction{}, err
	}
	defer tx.Rollback(ctx)

	var t Transaction
	err = tx.QueryRow(ctx, `
		UPDATE transactions SET description = $1, occurred_at = $2
		WHERE id = $3
		RETURNING id, description, occurred_at, created_at
	`, in.Description, in.OccurredAt, id).Scan(&t.ID, &t.Description, &t.OccurredAt, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transaction{}, ErrTransactionNotFound
	}
	if err != nil {
		return Transaction{}, err
	}

	if _, err := tx.Exec(ctx, `DELETE FROM transaction_entries WHERE transaction_id = $1`, id); err != nil {
		return Transaction{}, err
	}
	entries, err := insertEntries(ctx, tx, id, in.Entries)
	if err != nil {
		return Transaction{}, err
	}
	t.Entries = entries

	if err := checkBalance(ctx, tx); err != nil {
		return Transaction{}, err
	}
	return t, tx.Commit(ctx)
}

func (r *Repository) DeleteTransaction(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM transactions WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrTransactionNotFound
	}
	return nil
}

func (r *Repository) entriesForTransaction(ctx context.Context, transactionID int64) ([]Entry, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, account_id, amount FROM transaction_entries
		WHERE transaction_id = $1 ORDER BY id
	`, transactionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := []Entry{}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.AccountID, &e.Amount); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// insertEntries writes each of inputs against transactionID within tx,
// returning them with their own newly-assigned ids.
// checkBalance forces transaction_entries_balance (deferred to commit
// by default, see migration 00012_accounting.sql) to run right now
// instead, surfacing a violation as ErrUnbalanced from this call rather
// than from the later tx.Commit. This isn't just tidiness: pool.Begin
// on top of an already-open pgx.Tx (as tests do, via testutil.NewTx —
// see db.PGX's own doc comment) opens a SAVEPOINT, not a real nested
// transaction, and Postgres only fires a deferred trigger at a real
// COMMIT — never at a savepoint's own RELEASE, which is all
// tx.Commit(ctx) actually does in that case. Forcing it immediate here
// makes the check run (and any violation surface) regardless of
// whether tx sits directly on the pool or, in a test, one savepoint
// deep inside another transaction that's never really committed at
// all.
//
// On success, it explicitly resets the mode back to DEFERRED before
// returning — SET CONSTRAINTS's effect otherwise outlives this
// savepoint (a RELEASE SAVEPOINT, unlike a ROLLBACK, doesn't undo a
// session-level setting changed inside it), which would make the
// *next* multi-row entry insert anywhere else in the same enclosing
// transaction fail spuriously, checked one row too early, against a
// still-incomplete set. A failed check leaves this savepoint aborted
// instead, relying on the caller's own deferred tx.Rollback to discard
// everything about this attempt, IMMEDIATE mode included — a ROLLBACK
// (unlike RELEASE) does undo it.
func checkBalance(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SET CONSTRAINTS transaction_entries_balance IMMEDIATE`)
	if err != nil {
		if pgErrCode(err) == checkViolationCode {
			return ErrUnbalanced
		}
		return err
	}
	_, err = tx.Exec(ctx, `SET CONSTRAINTS transaction_entries_balance DEFERRED`)
	return err
}

func insertEntries(ctx context.Context, tx pgx.Tx, transactionID int64, inputs []EntryInput) ([]Entry, error) {
	entries := make([]Entry, 0, len(inputs))
	for _, in := range inputs {
		var e Entry
		err := tx.QueryRow(ctx, `
			INSERT INTO transaction_entries (transaction_id, account_id, amount)
			VALUES ($1,$2,$3)
			RETURNING id, account_id, amount
		`, transactionID, in.AccountID, in.Amount).Scan(&e.ID, &e.AccountID, &e.Amount)
		if err != nil {
			switch pgErrCode(err) {
			case foreignKeyViolationCode:
				return nil, ErrUnknownAccount
			case checkViolationCode:
				return nil, ErrInvalidAmount
			}
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// validateEntries catches what's cheap to check in Go, before ever
// touching the database: fewer than two entries, or one with a zero
// amount. Whether they actually sum to zero is left to the database's
// own deferred constraint trigger (see the doc comment on
// CreateTransaction).
func validateEntries(entries []EntryInput) error {
	if len(entries) < 2 {
		return ErrTooFewEntries
	}
	for _, e := range entries {
		if e.Amount == 0 {
			return ErrInvalidAmount
		}
	}
	return nil
}
