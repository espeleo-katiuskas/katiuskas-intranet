// Package memberships tracks membership periods (start/end date) for a
// user, and temporary leaves ("bajas temporales") the person takes
// during them. A user can have more than one period as long as they
// don't overlap; that invariant is enforced by the database, not by
// this package. A leave belongs to the person directly, not to one
// specific period, but must still fall entirely within one of their own
// periods — that invariant isn't something a database constraint can
// express across two tables, so this package enforces it itself; see
// Leave's own doc comment.
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

// Leave is a "baja temporal": a bounded window where the person steps
// away for a while without actually leaving the club. It belongs to
// the person directly (UserID), not to one specific membership period
// — that keeps every one of a person's own leaves, across however many
// separate alta/baja spans they've had, a single user_id lookup (no
// join through memberships needed just to answer "is this person
// currently on leave" or "how many leaves has this person had" — see
// Repository.IsOnLeave). It still must fall entirely within one of that
// person's own membership periods, though: that invariant isn't
// something a database constraint can express across two tables, so
// CreateLeave/UpdateLeave check it themselves, by finding which single
// period (if any) covers the given dates — see
// Repository.findContainingMembership (migration
// 00016_membership_leaves_by_user.sql). EndDate can be nil — the same
// "still open" shape a membership period's own EndDate already has —
// for a leave that's still ongoing and doesn't have a known end yet;
// see withinMembershipPeriod for what that means for containment.
type Leave struct {
	ID     int64 `json:"id"`
	UserID int64 `json:"-"`
	// UserLogin is filled in by the handler — see License.UserLogin's own
	// doc comment for the same split (a query already scoped under a
	// known /users/{login} vs. one addressed by the leave's own id,
	// which resolves it via a follow-up LoginForUserID call instead).
	UserLogin string     `json:"user_login"`
	StartDate time.Time  `json:"start_date"`
	EndDate   *time.Time `json:"end_date,omitempty"`
	Reason    *string    `json:"reason,omitempty"`
}

type CreateLeaveInput struct {
	UserID    int64
	StartDate time.Time
	EndDate   *time.Time
	Reason    *string
}

type UpdateLeaveInput struct {
	StartDate time.Time
	EndDate   *time.Time
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
