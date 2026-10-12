package memberships

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/db"
)

var (
	ErrNotFound = errors.New("membership not found")
	ErrOverlap  = errors.New("membership period overlaps an existing one for this user")
	// ErrLeaveNotFound and ErrLeaveOverlap mirror ErrNotFound/ErrOverlap,
	// just for membership_leaves instead of memberships.
	ErrLeaveNotFound = errors.New("temporary leave not found")
	ErrLeaveOverlap  = errors.New("temporary leave period overlaps an existing one for this person")
	// ErrLeaveOutsideMembership means the leave's date range doesn't fall
	// entirely within any single one of this person's own membership
	// periods — the one invariant this package has to check itself,
	// since it spans two tables.
	ErrLeaveOutsideMembership = errors.New("temporary leave must fall entirely within a membership period this person actually had")
	// ErrDueAlreadyRecorded means this user already has a due on file for
	// that year (membership_dues' own UNIQUE (user_id, year)) — one
	// payment per person per year, never two.
	ErrDueAlreadyRecorded = errors.New("a due for this user and year has already been recorded")
	// ErrUnknownTransaction means the given transaction_id doesn't name an
	// existing transaction — every due has to be tied to the payment that
	// recorded it, the same requirement licenses.CreateInput places on a
	// license.
	ErrUnknownTransaction = errors.New("transaction_id does not name an existing transaction")
	// ErrTransactionNotForDues means the transaction exists but none of
	// its entries is posted to an account flagged allows_dues (migration
	// 00017_accounts_dues_licenses_flags.sql) — only a transaction that
	// touches such an account can be the payment behind a due.
	ErrTransactionNotForDues = errors.New("transaction has no entry on an account that accepts membership dues")
)

// exclusionViolationCode is the PostgreSQL error code raised when the
// EXCLUDE USING gist constraint on memberships.period rejects an insert.
const exclusionViolationCode = "23P01"

// uniqueViolationCode and foreignKeyViolationCode are the PostgreSQL
// error codes CreateDue maps to ErrDueAlreadyRecorded/ErrUnknownTransaction.
const (
	uniqueViolationCode     = "23505"
	foreignKeyViolationCode = "23503"
)

type Repository struct {
	pool db.PGX
}

func NewRepository(pool db.PGX) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Create(ctx context.Context, in CreateInput) (Membership, error) {
	var m Membership
	err := r.pool.QueryRow(ctx, `
		INSERT INTO memberships (user_id, start_date)
		VALUES ($1,$2)
		RETURNING id, user_id, start_date, end_date, end_reason
	`, in.UserID, in.StartDate,
	).Scan(&m.ID, &m.UserID, &m.StartDate, &m.EndDate, &m.EndReason)

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == exclusionViolationCode {
		return m, ErrOverlap
	}
	return m, err
}

// GetByID is used by Update to merge partial changes onto the existing
// row before writing it back (the same pattern users.Update follows).
func (r *Repository) GetByID(ctx context.Context, id int64) (Membership, error) {
	var m Membership
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, start_date, end_date, end_reason
		FROM memberships WHERE id = $1
	`, id).Scan(&m.ID, &m.UserID, &m.StartDate, &m.EndDate, &m.EndReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

// Delete removes a membership period outright — nothing else
// references memberships.id by foreign key (membership_leaves keys off
// user_id instead, see migration 00016_membership_leaves_by_user.sql),
// so this never cascades into anything. Used by the frontend's own
// "undo my last action" story: only ever offered there for the single
// most recent alta/baja/baja-temporal entry a person has, but that's a
// UI-level rule, not something this method itself enforces — any
// membership period can be deleted by id.
func (r *Repository) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM memberships WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Update also re-validates this user's own existing temporary leaves
// against the period's new, possibly narrower start/end date —
// shrinking a membership's own period could just as easily strand an
// already-existing leave outside it as a bad leave edit could (see
// UpdateLeave, which checks the same invariant from the other
// direction); this package still has to check it itself either way,
// since no CHECK constraint spans both tables.
func (r *Repository) Update(ctx context.Context, id int64, in UpdateInput) (Membership, error) {
	before, err := r.GetByID(ctx, id)
	if err != nil {
		return before, err
	}
	candidate := before
	if in.EndReason != nil {
		candidate.EndReason = in.EndReason
	}
	candidate.StartDate = in.StartDate
	candidate.EndDate = in.EndDate

	if err := r.leavesStillFitWithin(ctx, before, candidate); err != nil {
		return Membership{}, err
	}

	_, err = r.pool.Exec(ctx, `
		UPDATE memberships SET start_date = $1, end_date = $2, end_reason = $3
		WHERE id = $4
	`, candidate.StartDate, candidate.EndDate, candidate.EndReason, id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == exclusionViolationCode {
		return candidate, ErrOverlap
	}
	return candidate, err
}

// leavesStillFitWithin reports (as ErrLeaveOutsideMembership, nil
// otherwise) whether every one of this user's own temporary leaves that
// currently falls within before's own start/end still falls entirely
// within candidate's new start/end — used by Update before it actually
// narrows a membership period, so an edit can't silently strand a leave
// outside the period it belongs to. A leave that already belongs to a
// different one of this user's own periods (before doesn't contain it
// either) is left alone — narrowing this period can't possibly affect it.
func (r *Repository) leavesStillFitWithin(ctx context.Context, before, candidate Membership) error {
	leaves, err := r.ListLeavesByUser(ctx, candidate.UserID)
	if err != nil {
		return err
	}
	for _, l := range leaves {
		if !withinMembershipPeriod(before, l.StartDate, l.EndDate) {
			continue
		}
		if !withinMembershipPeriod(candidate, l.StartDate, l.EndDate) {
			return ErrLeaveOutsideMembership
		}
	}
	return nil
}

func (r *Repository) ListByUser(ctx context.Context, userID int64) ([]Membership, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, start_date, end_date, end_reason
		FROM memberships WHERE user_id = $1 ORDER BY start_date DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Membership{}
	for rows.Next() {
		var m Membership
		if err := rows.Scan(&m.ID, &m.UserID, &m.StartDate, &m.EndDate, &m.EndReason); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

// WasMemberOn reports whether userID had an open membership period
// covering date — used to gate assigning a federative license: not to
// "is this person a member right now", but to "were they a member on
// the day this license says it was issued", so a license from while
// someone was still a socio can still be recorded after they've since
// caused baja. Pass time.Now() for the "currently a member" question
// itself.
func (r *Repository) WasMemberOn(ctx context.Context, userID int64, date time.Time) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM memberships WHERE user_id = $1 AND period @> $2::date)
	`, userID, date).Scan(&exists)
	return exists, err
}

// IsOnLeave reports whether userID is currently within one of their
// temporary leave windows — used by users.Service to tell "activo" apart
// from "baja temporal" on the people list, a distinction that isn't
// visible in the "member" role itself (a leave doesn't end the
// membership period it belongs to). A single user_id lookup now that
// membership_leaves carries it directly — no join through memberships
// needed (migration 00016_membership_leaves_by_user.sql).
func (r *Repository) IsOnLeave(ctx context.Context, userID int64) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM membership_leaves WHERE user_id = $1 AND period @> CURRENT_DATE)
	`, userID).Scan(&exists)
	return exists, err
}

func (r *Repository) ListActive(ctx context.Context) ([]Membership, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT m.id, m.user_id, u.login, m.start_date, m.end_date, m.end_reason,
			u.first_name, u.last_name
		FROM memberships m
		JOIN users u ON u.id = m.user_id
		WHERE m.period @> CURRENT_DATE
		ORDER BY u.last_name, u.first_name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Membership{}
	for rows.Next() {
		var m Membership
		if err := rows.Scan(&m.ID, &m.UserID, &m.UserLogin, &m.StartDate, &m.EndDate,
			&m.EndReason, &m.FirstName, &m.LastName); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

// withinMembershipPeriod reports whether [start, end] falls entirely
// within m's period — the containment check CreateLeave and UpdateLeave
// run before ever touching the database, since a CHECK constraint can't
// reach across to the memberships table. end is nil for a leave that's
// still ongoing (no end_date yet) — that only fits within a membership
// period that's itself still open: a leave can't be left dangling past
// a period that has since closed, so a nil end against an m.EndDate
// that isn't nil is never contained, the same way a closed leave whose
// own end_date falls after m.EndDate isn't.
func withinMembershipPeriod(m Membership, start time.Time, end *time.Time) bool {
	if start.Before(m.StartDate) {
		return false
	}
	if m.EndDate == nil {
		return true
	}
	return end != nil && !end.After(*m.EndDate)
}

// findContainingMembership returns the single membership period (if
// any) among userID's own history that fully contains [start, end] —
// there's at most one, since none of a single user's own periods ever
// overlap (see memberships' own EXCLUDE constraint). Returns
// ErrLeaveOutsideMembership if none does.
func (r *Repository) findContainingMembership(ctx context.Context, userID int64, start time.Time, end *time.Time) (Membership, error) {
	periods, err := r.ListByUser(ctx, userID)
	if err != nil {
		return Membership{}, err
	}
	for _, m := range periods {
		if withinMembershipPeriod(m, start, end) {
			return m, nil
		}
	}
	return Membership{}, ErrLeaveOutsideMembership
}

func (r *Repository) CreateLeave(ctx context.Context, in CreateLeaveInput) (Leave, error) {
	if _, err := r.findContainingMembership(ctx, in.UserID, in.StartDate, in.EndDate); err != nil {
		return Leave{}, err
	}

	var l Leave
	err := r.pool.QueryRow(ctx, `
		INSERT INTO membership_leaves (user_id, start_date, end_date, reason)
		VALUES ($1,$2,$3,$4)
		RETURNING id, user_id, start_date, end_date, reason
	`, in.UserID, in.StartDate, in.EndDate, in.Reason,
	).Scan(&l.ID, &l.UserID, &l.StartDate, &l.EndDate, &l.Reason)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == exclusionViolationCode {
		return l, ErrLeaveOverlap
	}
	return l, err
}

func (r *Repository) GetLeaveByID(ctx context.Context, id int64) (Leave, error) {
	var l Leave
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, start_date, end_date, reason
		FROM membership_leaves WHERE id = $1
	`, id).Scan(&l.ID, &l.UserID, &l.StartDate, &l.EndDate, &l.Reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return l, ErrLeaveNotFound
	}
	return l, err
}

// UpdateLeave re-validates containment on every correction, not just at
// creation — an edit could just as easily push the leave's dates
// outside every one of this person's own periods as a bad initial
// input could.
func (r *Repository) UpdateLeave(ctx context.Context, id int64, in UpdateLeaveInput) (Leave, error) {
	l, err := r.GetLeaveByID(ctx, id)
	if err != nil {
		return l, err
	}
	if _, err := r.findContainingMembership(ctx, l.UserID, in.StartDate, in.EndDate); err != nil {
		return l, err
	}
	if in.Reason != nil {
		l.Reason = in.Reason
	}
	l.StartDate = in.StartDate
	l.EndDate = in.EndDate

	_, err = r.pool.Exec(ctx, `
		UPDATE membership_leaves SET start_date = $1, end_date = $2, reason = $3
		WHERE id = $4
	`, l.StartDate, l.EndDate, l.Reason, id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == exclusionViolationCode {
		return l, ErrLeaveOverlap
	}
	return l, err
}

func (r *Repository) DeleteLeave(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM membership_leaves WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaveNotFound
	}
	return nil
}

func (r *Repository) ListLeavesByUser(ctx context.Context, userID int64) ([]Leave, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, start_date, end_date, reason
		FROM membership_leaves WHERE user_id = $1 ORDER BY start_date DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Leave{}
	for rows.Next() {
		var l Leave
		if err := rows.Scan(&l.ID, &l.UserID, &l.StartDate, &l.EndDate, &l.Reason); err != nil {
			return nil, err
		}
		list = append(list, l)
	}
	return list, rows.Err()
}

// HasAnyMembership reports whether userID has ever had a membership
// period at all, regardless of whether it's still open — used by
// Service.RecordDue to tell "this is their very first due" (no period
// on file yet, so one must be started alongside it) apart from a
// rejoining former member, who's left to the existing, separate "dar de
// alta" flow instead.
func (r *Repository) HasAnyMembership(ctx context.Context, userID int64) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships WHERE user_id = $1)`, userID).Scan(&exists)
	return exists, err
}

// TransactionAllowsDues reports whether transactionID exists and, if it
// does, whether at least one of its entries is posted to an account
// flagged allows_dues. Answered in SQL against accounting's own tables —
// the same way this package's due rows already reference transactions by
// foreign key — rather than by importing internal/accounting.
func (r *Repository) TransactionAllowsDues(ctx context.Context, transactionID int64) (exists, allowed bool, err error) {
	err = r.pool.QueryRow(ctx, `
		SELECT
			EXISTS (SELECT 1 FROM transactions WHERE id = $1),
			EXISTS (
				SELECT 1 FROM transaction_entries te
				JOIN accounts a ON a.id = te.account_id
				WHERE te.transaction_id = $1 AND a.allows_dues
			)
	`, transactionID).Scan(&exists, &allowed)
	return exists, allowed, err
}

func (r *Repository) CreateDue(ctx context.Context, in CreateDueInput) (Due, error) {
	var d Due
	err := r.pool.QueryRow(ctx, `
		INSERT INTO membership_dues (user_id, year, transaction_id)
		VALUES ($1,$2,$3)
		RETURNING id, user_id, year, transaction_id, created_at
	`, in.UserID, in.Year, in.TransactionID,
	).Scan(&d.ID, &d.UserID, &d.Year, &d.TransactionID, &d.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case uniqueViolationCode:
				return d, ErrDueAlreadyRecorded
			case foreignKeyViolationCode:
				return d, ErrUnknownTransaction
			}
		}
		return d, err
	}
	return d, nil
}

// ListDuesForTransaction returns every due recorded against one
// transaction — a single payment can cover several socios' cuotas — each
// with its person's login (joined here, since unlike ListDuesForUser's
// callers nobody has a single known /users/{login} to fill it in from)
// and its PaidAt, ordered by that person's name.
func (r *Repository) ListDuesForTransaction(ctx context.Context, transactionID int64) ([]Due, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT d.id, d.user_id, u.login, d.year, d.transaction_id, d.created_at, t.occurred_at
		FROM membership_dues d
		JOIN users u ON u.id = d.user_id
		JOIN transactions t ON t.id = d.transaction_id
		WHERE d.transaction_id = $1
		ORDER BY u.last_name, u.first_name, d.year
	`, transactionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Due{}
	for rows.Next() {
		var d Due
		if err := rows.Scan(&d.ID, &d.UserID, &d.UserLogin, &d.Year, &d.TransactionID, &d.CreatedAt, &d.PaidAt); err != nil {
			return nil, err
		}
		list = append(list, d)
	}
	return list, rows.Err()
}

// LatestDueForUser returns the person's due for the most recent year
// they've paid (nil if they've never paid one), with PaidAt filled in
// like ListDuesForUser — the one row users.Service.enrich needs, without
// reading every year just to look at the first.
func (r *Repository) LatestDueForUser(ctx context.Context, userID int64) (*Due, error) {
	var d Due
	err := r.pool.QueryRow(ctx, `
		SELECT d.id, d.user_id, d.year, d.transaction_id, d.created_at, t.occurred_at
		FROM membership_dues d
		JOIN transactions t ON t.id = d.transaction_id
		WHERE d.user_id = $1 ORDER BY d.year DESC LIMIT 1
	`, userID).Scan(&d.ID, &d.UserID, &d.Year, &d.TransactionID, &d.CreatedAt, &d.PaidAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (r *Repository) ListDuesForUser(ctx context.Context, userID int64) ([]Due, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT d.id, d.user_id, d.year, d.transaction_id, d.created_at, t.occurred_at
		FROM membership_dues d
		JOIN transactions t ON t.id = d.transaction_id
		WHERE d.user_id = $1 ORDER BY d.year DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Due{}
	for rows.Next() {
		var d Due
		if err := rows.Scan(&d.ID, &d.UserID, &d.Year, &d.TransactionID, &d.CreatedAt, &d.PaidAt); err != nil {
			return nil, err
		}
		list = append(list, d)
	}
	return list, rows.Err()
}
