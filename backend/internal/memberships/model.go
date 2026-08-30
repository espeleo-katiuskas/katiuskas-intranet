// Package memberships tracks membership periods (start/end date) for a
// user. A user can have more than one period as long as they don't overlap;
// that invariant is enforced by the database, not by this package.
package memberships

import "time"

type Membership struct {
	ID        int64      `json:"id"`
	UserID    int64      `json:"user_id"`
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
