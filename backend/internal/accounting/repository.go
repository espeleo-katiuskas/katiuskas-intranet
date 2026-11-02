package accounting

import (
	"context"
	"errors"
	"strings"

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
	// ErrNotIncomeForDistributions means the account would be (or stay) the one
	// distributions record their payments in without being of type income: the
	// default account for distributions, or an account a distribution names, has
	// to be an income account.
	ErrNotIncomeForDistributions = errors.New("an account used for the payments of distributions has to be of type income")
	// ErrAccountHasChildren means an entry was posted to an account that has child
	// accounts, and ErrParentHasEntries an account was given as parent (on create
	// or edit) when it already has entries: an account either groups others or
	// has transactions, never both (migration 00033).
	ErrAccountHasChildren = errors.New("an account with child accounts can't have transactions: use one of its children")
	ErrParentHasEntries   = errors.New("an account with transactions can't have child accounts")
	// ErrNotIncomeForDuesLicenses means the same for an account marked for annual
	// dues or federative licenses (allows_dues/allows_licenses): it has to be of
	// type income.
	ErrNotIncomeForDuesLicenses = errors.New("an account marked for dues or licenses has to be of type income")
	// ErrDuplicateCode means another account already has this code.
	ErrDuplicateCode = errors.New("another account already has this code")
	// ErrAccountInUse means this account can't be deleted: it's still
	// referenced by a child account (its own parent_id) or a posted
	// transaction entry, and deleting it would either orphan that
	// subtree or silently erase financial history.
	ErrAccountInUse = errors.New("account is still referenced by a child account or a transaction entry")
	// ErrTransactionHasLicenses means the transaction can't be deleted:
	// a federative license still names it as its payment
	// (licenses.transaction_id is ON DELETE RESTRICT, since a license
	// outlives the record of how it was paid). Its dues, by contrast,
	// are removed with it.
	ErrTransactionHasLicenses = errors.New("transaction is still the payment of one or more licenses")
	// ErrTransactionHasDistributionPayments means the transaction recorded the
	// payment of something in a distribution and can't be deleted: deleting one
	// undoes those payments, but the orders have changed since and no longer allow
	// it (or, with no such hook, the payment is still there). The payment has to be
	// taken off in the distribution instead.
	// ErrTransactionHasFiles means documents (tickets, invoices) are still attached to the
	// transaction: they have to be removed first.
	ErrTransactionHasFiles                = errors.New("transaction still has attached files: remove them first")
	ErrTransactionHasDistributionPayments = errors.New("transaction recorded payments of a distribution that can't be undone any more: take them off there")
	// ErrCircularParent means the given parent_id is this account
	// itself, or one of its own descendants — setting it would make
	// this account a descendant of one of its own descendants, a cycle
	// GET /accounts's own tree traversal (Repository.ListAccounts, a
	// top-down walk starting only from real parent_id IS NULL roots)
	// can't ever reach, so an account caught in one would just quietly
	// vanish from the listing instead of erroring anywhere obvious.
	ErrCircularParent = errors.New("parent_id would make this account a descendant of itself")
	// ErrInvalidDirection means MoveAccount's direction wasn't "up" or
	// "down".
	ErrInvalidDirection = errors.New(`direction must be "up" or "down"`)

	ErrTransactionNotFound = errors.New("transaction not found")
	// ErrTooFewEntries means fewer than two entries were given — a
	// transaction always moves money between at least two accounts.
	ErrTooFewEntries = errors.New("a transaction needs at least two entries")
	// ErrDuplicateAccount means the same account appears on more than one
	// entry of one transaction — each account gets a single entry, whose
	// amount already says everything that moved through it.
	ErrDuplicateAccount = errors.New("an account can appear only once in a transaction")
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

// pgConstraint is the name of the constraint a database error is about, which
// the triggers of the accounting tables set to say which rule they enforce.
func pgConstraint(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.ConstraintName
	}
	return ""
}

func pgErrCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

type Repository struct {
	pool db.PGX
	// beforeDelete, when set, runs inside the transaction that deletes an
	// accounting transaction, before the delete itself: it is how the payments of
	// distributions a transaction recorded are undone along with it (this package
	// can't know about them). Whatever it returns is the delete's error.
	beforeDelete func(ctx context.Context, tx db.PGX, transactionID int64) error
}

// BeforeDeleteTransaction sets what runs before a transaction is deleted, in the
// same database transaction.
func (r *Repository) BeforeDeleteTransaction(f func(ctx context.Context, tx db.PGX, transactionID int64) error) {
	r.beforeDelete = f
}

func NewRepository(pool db.PGX) *Repository {
	return &Repository{pool: pool}
}

// ---- Accounts ----

// CreateAccount appends the new account after whatever its own sibling
// group's (same parent_id, NULL included) current highest position is
// — the MAX(position) subquery and the INSERT run as one statement, so
// there's no separate round trip a concurrent create against the same
// group could race against. Same pattern
// activityresources.Repository.Create uses for a resource's own
// position within its activity.
func (r *Repository) CreateAccount(ctx context.Context, in CreateAccountInput) (Account, error) {
	// In its own (sub)transaction: taking the distributions' default mark off
	// another account and giving it to this one is one change, and a rejected
	// create (a duplicate code) must not leave the other one without it.
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback(ctx)

	if in.DistributionDefault {
		if _, err := tx.Exec(ctx, `UPDATE accounts SET distribution_default = false WHERE distribution_default`); err != nil {
			return Account{}, err
		}
	}
	var a Account
	err = tx.QueryRow(ctx, `
		INSERT INTO accounts (name, code, parent_id, type, allows_dues, allows_licenses, hidden, distribution_default, position)
		SELECT $1, $2, $3, $7, $4, $5, $6, $8, COALESCE(MAX(position), 0) + 1
		FROM accounts WHERE parent_id IS NOT DISTINCT FROM $3
		RETURNING id, name, code, parent_id, type, allows_dues, allows_licenses, hidden, distribution_default
	`, in.Name, in.Code, in.ParentID, in.AllowsDues, in.AllowsLicenses, in.Hidden, in.Type, in.DistributionDefault,
	).Scan(&a.ID, &a.Name, &a.Code, &a.ParentID, &a.Type, &a.AllowsDues, &a.AllowsLicenses, &a.Hidden, &a.DistributionDefault)
	if pgConstraint(err) == "parent_has_entries" {
		return Account{}, ErrParentHasEntries
	}
	switch pgErrCode(err) {
	case uniqueViolationCode:
		return Account{}, ErrDuplicateCode
	case foreignKeyViolationCode:
		return Account{}, ErrUnknownAccount
	}
	if err != nil {
		return Account{}, err
	}
	if err := finishAccountChange(ctx, tx, &a); err != nil {
		return Account{}, err
	}
	return a, tx.Commit(ctx)
}

// finishAccountChange runs, inside the transaction that created or edited an
// account, what can only be known once it is in place: its effective type (the
// function reads the table, so not in the same statement) and that nothing
// that has to be income stopped being so — the account of a distribution and the
// default one, and the accounts marked for dues or licenses. A new or moved
// account, or a changed type, can change the type of a whole subtree, so it is the
// account and everything below it that is checked, not accounts elsewhere that
// may predate the rule.
func finishAccountChange(ctx context.Context, tx pgx.Tx, a *Account) error {
	if err := tx.QueryRow(ctx, `SELECT account_effective_type($1)`, a.ID).Scan(&a.EffectiveType); err != nil {
		return err
	}
	var distributionsBroken, duesLicensesBroken bool
	if err := tx.QueryRow(ctx, `
		WITH RECURSIVE subtree AS (
			SELECT id FROM accounts WHERE id = $1
			UNION ALL
			SELECT a.id FROM accounts a JOIN subtree s ON a.parent_id = s.id
		)
		SELECT
			EXISTS (
				SELECT 1 FROM distributions d
				WHERE d.account_id IN (SELECT id FROM subtree)
				  AND account_effective_type(d.account_id) IS DISTINCT FROM 'income'
			) OR EXISTS (
				SELECT 1 FROM accounts
				WHERE id IN (SELECT id FROM subtree) AND distribution_default
				  AND account_effective_type(id) IS DISTINCT FROM 'income'
			),
			EXISTS (
				SELECT 1 FROM accounts
				WHERE id IN (SELECT id FROM subtree) AND (allows_dues OR allows_licenses)
				  AND account_effective_type(id) IS DISTINCT FROM 'income'
			)
	`, a.ID).Scan(&distributionsBroken, &duesLicensesBroken); err != nil {
		return err
	}
	if distributionsBroken {
		return ErrNotIncomeForDistributions
	}
	if duesLicensesBroken {
		return ErrNotIncomeForDuesLicenses
	}
	return nil
}

func (r *Repository) GetAccount(ctx context.Context, id int64) (Account, error) {
	var a Account
	err := r.pool.QueryRow(ctx, `
		SELECT id, name, code, parent_id, type, allows_dues, allows_licenses, hidden, distribution_default FROM accounts WHERE id = $1
	`, id).Scan(&a.ID, &a.Name, &a.Code, &a.ParentID, &a.Type, &a.AllowsDues, &a.AllowsLicenses, &a.Hidden, &a.DistributionDefault)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrAccountNotFound
	}
	if err != nil {
		return Account{}, err
	}
	err = r.pool.QueryRow(ctx, `SELECT account_effective_type($1)`, id).Scan(&a.EffectiveType)
	return a, err
}

// ListAccounts returns every account in display order: a pre-order
// depth-first walk of the chart of accounts — every top-level account
// in turn (by its own Position, see MoveAccount), each immediately
// followed by all of its own descendants (recursively, each in their
// own Position order) before moving on to the next top-level sibling —
// each with its own direct Balance and LastTransactionAt (never rolled
// up from child accounts).
//
// tree's own path column is the standard "materialized path" trick for
// exactly this: the sequence of Position values from a real root (only
// ever accounts.parent_id IS NULL seeds this) down to each account, so
// ordering by it (Postgres compares arrays element-by-element) reads
// out precisely that walk — a node's own descendants always sort
// immediately after it and before its next sibling, however deep they
// go, without this query needing to know the tree's depth up front.
// Anchored only at genuine roots, this can never loop forever even if
// bad data somewhere formed a cycle among non-root accounts (nothing
// in such a cycle has parent_id IS NULL, so nothing in it could ever
// seed tree in the first place) — see ErrCircularParent, which exists
// to stop that from happening to begin with, since an account caught
// in one would otherwise just silently vanish from this listing.
//
// The LEFT JOINs mean an account with no entries at all still gets one
// row back (COALESCE'd to a zero balance; MAX over zero rows is
// already NULL on its own for last_transaction_at), rather than being
// silently missing from the result the way an inner join would leave
// it.
func (r *Repository) ListAccounts(ctx context.Context) ([]Account, error) {
	rows, err := r.pool.Query(ctx, `
		WITH RECURSIVE tree AS (
			SELECT id, ARRAY[position] AS path
			FROM accounts WHERE parent_id IS NULL
			UNION ALL
			SELECT a.id, tree.path || a.position
			FROM accounts a JOIN tree ON a.parent_id = tree.id
		), closure AS (
			-- Every account with itself and everything below it: an account that
			-- groups others has no entries, its balance and last movement are those
			-- of its descendants.
			SELECT id AS ancestor, id AS descendant FROM accounts
			UNION ALL
			SELECT closure.ancestor, a.id FROM accounts a JOIN closure ON a.parent_id = closure.descendant
		)
		SELECT a.id, a.name, a.code, a.parent_id, a.type, a.allows_dues, a.allows_licenses, a.hidden, a.distribution_default,
			account_effective_type(a.id),
			COALESCE(SUM(te.amount), 0) AS balance,
			MAX(t.occurred_at) AS last_transaction_at
		FROM accounts a
		JOIN tree ON tree.id = a.id
		JOIN closure ON closure.ancestor = a.id
		LEFT JOIN transaction_entries te ON te.account_id = closure.descendant
		LEFT JOIN transactions t ON t.id = te.transaction_id
		GROUP BY a.id, tree.path
		ORDER BY tree.path
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Account{}
	for rows.Next() {
		var a Account
		var balance float64
		if err := rows.Scan(&a.ID, &a.Name, &a.Code, &a.ParentID, &a.Type, &a.AllowsDues, &a.AllowsLicenses, &a.Hidden, &a.DistributionDefault, &a.EffectiveType, &balance, &a.LastTransactionAt); err != nil {
			return nil, err
		}
		a.Balance = &balance
		list = append(list, a)
	}
	return list, rows.Err()
}

// ListAccountEntries returns every entry ever posted against this one
// account (GET /accounts/{id}/transactions), each carrying its own
// transaction's date/description and this account's own running
// balance once that entry is applied. Queried in occurred_at (then id)
// order specifically so BalanceAfter can be accumulated correctly —
// oldest first, since a running balance only ever makes sense walked
// forward through time — and the page itself is served newest first,
// the same convention ListTransactions's own ORDER BY already uses. Doesn't
// itself check the account exists (see Handler.listAccountEntries,
// which calls GetAccount first) — an unknown id here would just come
// back an empty, not-an-error list, indistinguishable from a real
// account with no entries yet.
const (
	accentedLetters = "áàâäéèêëíìîïóòôöúùûüñ"
	plainLetters    = "aaaaeeeeiiiioooouuuun"
)

// descriptionHasWords is the SQL condition that every pattern of the array
// parameter (from likePatterns) matches the column, with case and accents
// ignored on both sides: it works wherever the unaccent extension isn't there.
func descriptionHasWords(column, patterns string) string {
	return `NOT EXISTS (SELECT 1 FROM unnest(` + patterns + `::text[]) AS p
		WHERE translate(lower(` + column + `), '` + accentedLetters + `', '` + plainLetters + `') NOT LIKE p)`
}

// likePatterns turns the words searched for into LIKE patterns that contain
// each one, normalized the way descriptionHasWords normalizes the column.
func likePatterns(words []string) []string {
	accented, plain := []rune(accentedLetters), []rune(plainLetters)
	patterns := make([]string, 0, len(words))
	for _, word := range words {
		var b strings.Builder
		b.WriteByte('%')
		for _, c := range strings.ToLower(word) {
			for i, a := range accented {
				if c == a {
					c = plain[i]
					break
				}
			}
			if c == '%' || c == '_' || c == '\\' {
				b.WriteByte('\\')
			}
			b.WriteRune(c)
		}
		b.WriteByte('%')
		patterns = append(patterns, b.String())
	}
	return patterns
}

func (r *Repository) ListAccountEntries(ctx context.Context, accountID int64, filter ListFilter) ([]AccountEntry, error) {
	// The entries are those of the account and of everything below it in the
	// hierarchy: for one that groups others (and so has none of its own) all of its
	// descendants', the balance running over their sum, in date order.
	// The running balance is a window over *every* entry of the account,
	// computed in the inner query before the date range and the page are
	// applied outside it — so an entry's balance_after is the same
	// whichever window it's seen through, not a balance restarting from
	// zero at the top of the page.
	rows, err := r.pool.Query(ctx, `
		WITH RECURSIVE subtree AS (
			SELECT id FROM accounts WHERE id = $1
			UNION ALL
			SELECT a.id FROM accounts a JOIN subtree s ON a.parent_id = s.id
		)
		SELECT transaction_id, account_id, account_name, occurred_at, description, amount, balance_after, file_count FROM (
			SELECT t.id AS transaction_id, te.id AS entry_id, te.account_id, a.name AS account_name,
				t.occurred_at, t.description, te.amount::float8 AS amount,
				(SELECT COUNT(*) FROM transaction_files f WHERE f.transaction_id = t.id)::int AS file_count,
				(SUM(te.amount) OVER (ORDER BY t.occurred_at, t.id, te.id))::float8 AS balance_after
			FROM transaction_entries te
			JOIN transactions t ON t.id = te.transaction_id
			JOIN accounts a ON a.id = te.account_id
			WHERE te.account_id IN (SELECT id FROM subtree)
		) ledger
		WHERE ($2::timestamptz IS NULL OR occurred_at >= $2)
		  AND ($3::timestamptz IS NULL OR occurred_at < $3::timestamptz + interval '1 day')
		  AND `+descriptionHasWords("description", "$6")+`
		ORDER BY occurred_at DESC, transaction_id DESC, entry_id DESC
		LIMIT $4 OFFSET $5
	`, accountID, filter.From, filter.To, filter.Limit, filter.Offset, likePatterns(filter.Words))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []AccountEntry{}
	for rows.Next() {
		var e AccountEntry
		if err := rows.Scan(&e.TransactionID, &e.AccountID, &e.AccountName, &e.OccurredAt, &e.Description, &e.Amount, &e.BalanceAfter, &e.FileCount); err != nil {
			return nil, err
		}
		list = append(list, e)
	}
	return list, rows.Err()
}

func (r *Repository) UpdateAccount(ctx context.Context, id int64, in UpdateAccountInput) (Account, error) {
	if in.ParentID != nil {
		cyclic, err := r.wouldCreateCycle(ctx, id, *in.ParentID)
		if err != nil {
			return Account{}, err
		}
		if cyclic {
			return Account{}, ErrCircularParent
		}
	}

	// Own (sub)transaction, as in CreateAccount: the default mark moves from one
	// account to this one or doesn't move at all.
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback(ctx)

	if in.DistributionDefault {
		if _, err := tx.Exec(ctx, `UPDATE accounts SET distribution_default = false WHERE distribution_default AND id <> $1`, id); err != nil {
			return Account{}, err
		}
	}
	var a Account
	err = tx.QueryRow(ctx, `
		UPDATE accounts SET name = $1, code = $2, parent_id = $3, type = $8, allows_dues = $4, allows_licenses = $5, hidden = $6,
			distribution_default = $9
		WHERE id = $7
		RETURNING id, name, code, parent_id, type, allows_dues, allows_licenses, hidden, distribution_default
	`, in.Name, in.Code, in.ParentID, in.AllowsDues, in.AllowsLicenses, in.Hidden, id, in.Type, in.DistributionDefault,
	).Scan(&a.ID, &a.Name, &a.Code, &a.ParentID, &a.Type, &a.AllowsDues, &a.AllowsLicenses, &a.Hidden, &a.DistributionDefault)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrAccountNotFound
	}
	if pgConstraint(err) == "parent_has_entries" {
		return Account{}, ErrParentHasEntries
	}
	switch pgErrCode(err) {
	case uniqueViolationCode:
		return Account{}, ErrDuplicateCode
	case foreignKeyViolationCode:
		return Account{}, ErrUnknownAccount
	}
	if err != nil {
		return Account{}, err
	}
	if err := finishAccountChange(ctx, tx, &a); err != nil {
		return Account{}, err
	}
	return a, tx.Commit(ctx)
}

// wouldCreateCycle reports whether setting accountID's parent to
// proposedParentID would make accountID a descendant of one of its own
// descendants (proposedParentID == accountID itself included, the
// simplest case: an account can't be its own parent). Walks upward
// from proposedParentID via parent_id, capped at a depth no real chart
// of accounts would ever approach, purely so this can never loop
// forever even against data a cycle somehow already reached — a bound
// this specific check needs that ListAccounts's own recursive walk
// (anchored only at real roots) never does.
func (r *Repository) wouldCreateCycle(ctx context.Context, accountID, proposedParentID int64) (bool, error) {
	const maxDepth = 100
	var exists bool
	err := r.pool.QueryRow(ctx, `
		WITH RECURSIVE ancestors AS (
			SELECT id, parent_id, 0 AS depth FROM accounts WHERE id = $1
			UNION ALL
			SELECT a.id, a.parent_id, ancestors.depth + 1
			FROM accounts a JOIN ancestors ON a.id = ancestors.parent_id
			WHERE ancestors.depth < $3
		)
		SELECT EXISTS (SELECT 1 FROM ancestors WHERE id = $2)
	`, proposedParentID, accountID, maxDepth).Scan(&exists)
	return exists, err
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

// MoveAccount swaps this account's Position with its adjacent sibling
// (same parent_id, NULL included) — the one immediately before it for
// MoveUp, immediately after for MoveDown — so it displays right before
// or right after that sibling in ListAccounts instead. A harmless
// no-op (not an error) if there's no such sibling: this account is
// already first or last within its own parent_id group in that
// direction.
//
// Every account in the whole sibling group is locked (FOR UPDATE) up
// front, in a fixed id order (ORDER BY id), rather than locking just
// this account first and then whichever one turns out to be its
// adjacent sibling — the previous approach, which could deadlock: two
// concurrent opposite-direction moves on the same adjacent pair (A
// moving down past B, B moving up past A, say) would each lock their
// own "self" row first and then reach for the other's, the exact
// lock-ordering inversion that produces one (Postgres would abort one
// of the two with error 40P01, deadlock_detected, surfacing as a raw,
// unhandled 500 — not in pgErrCode's own recognized codes). Locking
// the whole group in the same id order up front instead means any two
// concurrent moves within it always queue up behind whichever
// transaction locked first, never each waiting on the other. A chart
// of accounts' own sibling groups are small enough in practice that
// locking the whole group instead of just the two rows that end up
// mattering costs nothing worth optimizing away.
func (r *Repository) MoveAccount(ctx context.Context, id int64, direction MoveDirection) (Account, error) {
	if direction != MoveUp && direction != MoveDown {
		return Account{}, ErrInvalidDirection
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback(ctx)

	// Read this account's own parent_id first, unlocked — just enough
	// to know which sibling group to lock next; not itself a source of
	// the same race, since every MoveAccount call locks that whole
	// group before making any decision based on it.
	var parentID *int64
	err = tx.QueryRow(ctx, `SELECT parent_id FROM accounts WHERE id = $1`, id).Scan(&parentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrAccountNotFound
	}
	if err != nil {
		return Account{}, err
	}

	rows, err := tx.Query(ctx, `
		SELECT id, name, code, parent_id, type, allows_dues, allows_licenses, hidden, distribution_default, position FROM accounts
		WHERE parent_id IS NOT DISTINCT FROM $1
		ORDER BY id
		FOR UPDATE
	`, parentID)
	if err != nil {
		return Account{}, err
	}
	defer rows.Close()

	type sibling struct {
		Account
		position int
	}
	var target sibling
	found := false
	siblings := []sibling{}
	for rows.Next() {
		var s sibling
		if err := rows.Scan(&s.ID, &s.Name, &s.Code, &s.ParentID, &s.Type, &s.AllowsDues, &s.AllowsLicenses, &s.Hidden, &s.DistributionDefault, &s.position); err != nil {
			return Account{}, err
		}
		if s.ID == id {
			target = s
			found = true
		}
		siblings = append(siblings, s)
	}
	if err := rows.Err(); err != nil {
		return Account{}, err
	}
	if !found {
		return Account{}, ErrAccountNotFound
	}

	// The adjacent sibling in the requested direction: the closest
	// position below target's own for MoveUp, above it for MoveDown —
	// among rows already locked above, not a further query.
	var adjacent *sibling
	for i := range siblings {
		s := &siblings[i]
		if s.ID == id {
			continue
		}
		if direction == MoveUp && s.position < target.position && (adjacent == nil || s.position > adjacent.position) {
			adjacent = s
		}
		if direction == MoveDown && s.position > target.position && (adjacent == nil || s.position < adjacent.position) {
			adjacent = s
		}
	}
	if adjacent == nil {
		return target.Account, tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx, `UPDATE accounts SET position = $1 WHERE id = $2`, adjacent.position, id); err != nil {
		return Account{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE accounts SET position = $1 WHERE id = $2`, target.position, adjacent.ID); err != nil {
		return Account{}, err
	}
	return target.Account, tx.Commit(ctx)
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
		SELECT id, description, occurred_at, created_at,
			(SELECT COUNT(*) FROM transaction_files WHERE transaction_id = transactions.id)::int
		FROM transactions WHERE id = $1
	`, id).Scan(&t.ID, &t.Description, &t.OccurredAt, &t.CreatedAt, &t.FileCount)
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

// ListTransactions returns one page of transactions (most recently
// occurred first), optionally limited to a date range, each with its own entries — one query for
// the transactions themselves, then one batched query for every one of
// their entries (rather than one query per transaction) to keep this to
// two round trips regardless of how many transactions are returned.
func (r *Repository) ListTransactions(ctx context.Context, filter ListFilter) ([]Transaction, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, description, occurred_at, created_at,
			(SELECT COUNT(*) FROM transaction_files WHERE transaction_id = transactions.id)::int
		FROM transactions
		WHERE ($1::timestamptz IS NULL OR occurred_at >= $1)
		  AND ($2::timestamptz IS NULL OR occurred_at < $2::timestamptz + interval '1 day')
		  AND `+descriptionHasWords("description", "$5")+`
		ORDER BY occurred_at DESC, id DESC LIMIT $3 OFFSET $4
	`, filter.From, filter.To, filter.Limit, filter.Offset, likePatterns(filter.Words))
	if err != nil {
		return nil, err
	}
	list := []Transaction{}
	ids := make([]int64, 0)
	for rows.Next() {
		var t Transaction
		if err := rows.Scan(&t.ID, &t.Description, &t.OccurredAt, &t.CreatedAt, &t.FileCount); err != nil {
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
		RETURNING id, description, occurred_at, created_at,
			(SELECT COUNT(*) FROM transaction_files WHERE transaction_id = transactions.id)::int
	`, in.Description, in.OccurredAt, id).Scan(&t.ID, &t.Description, &t.OccurredAt, &t.CreatedAt, &t.FileCount)
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
	// Inside its own (sub)transaction so a rejected delete is rolled back
	// on its own: under test, where every call shares one outer
	// transaction, the foreign-key error would otherwise leave that whole
	// transaction aborted.
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if r.beforeDelete != nil {
		if err := r.beforeDelete(ctx, tx, id); err != nil {
			return err
		}
	}
	// Nothing cascades: the entries of the transaction and the dues it paid go with it, deleted here.
	if _, err := tx.Exec(ctx, `DELETE FROM membership_dues WHERE transaction_id = $1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM transaction_entries WHERE transaction_id = $1`, id); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM transactions WHERE id = $1`, id)
	if pgErrCode(err) == foreignKeyViolationCode {
		// Which thing still points at it: a license, a payment of a distribution or a file.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && strings.Contains(pgErr.TableName, "distribution_payments") {
			return ErrTransactionHasDistributionPayments
		}
		if errors.As(err, &pgErr) && pgErr.TableName == "transaction_files" {
			return ErrTransactionHasFiles
		}
		return ErrTransactionHasLicenses
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrTransactionNotFound
	}
	return tx.Commit(ctx)
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
			if pgConstraint(err) == "account_has_children" {
				return nil, ErrAccountHasChildren
			}
			switch pgErrCode(err) {
			case foreignKeyViolationCode:
				return nil, ErrUnknownAccount
			case checkViolationCode:
				return nil, ErrInvalidAmount
			case uniqueViolationCode:
				return nil, ErrDuplicateAccount
			}
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// validateEntries catches what's cheap to check in Go, before ever
// touching the database: fewer than two entries (so never none), one with
// a zero amount, or the same account twice (also enforced by the
// transaction_entries_account_once unique constraint). Whether they actually sum to zero is left to the database's
// own deferred constraint trigger (see the doc comment on
// CreateTransaction).
func validateEntries(entries []EntryInput) error {
	if len(entries) < 2 {
		return ErrTooFewEntries
	}
	seen := make(map[int64]bool, len(entries))
	for _, e := range entries {
		if e.Amount == 0 {
			return ErrInvalidAmount
		}
		if seen[e.AccountID] {
			return ErrDuplicateAccount
		}
		seen[e.AccountID] = true
	}
	return nil
}
