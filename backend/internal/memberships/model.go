// Package memberships tracks membership periods (start/end date) for a
// user, and temporary leaves ("bajas temporales") within them. A user can
// have more than one period as long as they don't overlap; that
// invariant is enforced by the database, not by this package. A leave
// must fall entirely within the period it belongs to — that invariant
// isn't something a database constraint can express across two tables,
// so this package enforces it instead.
package memberships

import "time"

type Membership struct {
	ID     int64 `json:"id"`
	UserID int64 `json:"-"`
	// UserLogin is filled in either by ListActive's JOIN (which spans
	// many different people at once, the same as its FirstName/LastName
	// columns) or, for a query scoped to one already-known
	// /users/{login}, directly by the handler — see
	// licenses.Handler for the same split, including the reverse lookup
	// Update needs since it's addressed by membership id, not a user path.
	UserLogin string     `json:"user_login"`
	FirstName string     `json:"first_name,omitempty"`
	LastName  string     `json:"last_name,omitempty"`
	StartDate time.Time  `json:"start_date"`
	EndDate   *time.Time `json:"end_date,omitempty"`
	EndReason *string    `json:"end_reason,omitempty"`
}

type CreateInput struct {
	UserID    int64
	StartDate time.Time
}

// UpdateInput corrects a membership period's dates. StartDate and EndDate
// always replace whatever was there — there's no ambiguity to resolve
// between "leave the date as is" and "clear it" because the edit form
// always sends both, pre-filled with the current values, back. A nil
// EndDate means the period is open (still active), which also lets an
// admin undo an accidental "dar de baja" by clearing the date they'd set.
type UpdateInput struct {
	StartDate time.Time
	EndDate   *time.Time
	EndReason *string
}

// Leave is a "baja temporal": a bounded window, always entirely within
// one membership period, where the person steps away for a while
// without actually leaving the club. Unlike a membership period itself,
// both dates are always known up front — a temporary leave is a planned,
// finite thing, not something opened now and closed later.
type Leave struct {
	ID           int64     `json:"id"`
	MembershipID int64     `json:"membership_id"`
	StartDate    time.Time `json:"start_date"`
	EndDate      time.Time `json:"end_date"`
	Reason       *string   `json:"reason,omitempty"`
}

type CreateLeaveInput struct {
	MembershipID int64
	StartDate    time.Time
	EndDate      time.Time
	Reason       *string
}

type UpdateLeaveInput struct {
	StartDate time.Time
	EndDate   time.Time
	Reason    *string
}

// Due is one year's membership fee ("cuota"), always tied to the
// transaction that recorded its payment (migration 00014_membership_
// dues_and_license_payments.sql) — a due with no transaction behind it
// doesn't mean anything yet, so there's no "unpaid due" row to
// represent, unlike a membership period itself. Keyed by (UserID, Year)
// rather than by a specific membership, since a rejoining member's due
// for a given year isn't naturally tied to one particular period, and
// this way a year never needs to be reconciled against period
// boundaries. See Service.RecordDue for how the very first one (the
// matrícula + first year's cuota) also starts the membership itself.
type Due struct {
	ID     int64 `json:"id"`
	UserID int64 `json:"-"`
	// UserLogin is filled in by the handler, the same as License.UserLogin
	// — every route here is scoped under a known /users/{login}.
	UserLogin     string    `json:"user_login"`
	Year          int       `json:"year"`
	TransactionID int64     `json:"transaction_id"`
	CreatedAt     time.Time `json:"created_at"`
}

type CreateDueInput struct {
	UserID        int64
	Year          int
	TransactionID int64
}
