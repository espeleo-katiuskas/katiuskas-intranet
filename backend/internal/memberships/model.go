// Package memberships tracks membership periods (start/end date) for a
// user. A user can have more than one period as long as they don't overlap;
// that invariant is enforced by the database, not by this package.
package memberships

import "time"

type Membership struct {
	ID               int64      `json:"id"`
	UserID           int64      `json:"user_id"`
	FirstName        string     `json:"first_name,omitempty"`
	LastName         string     `json:"last_name,omitempty"`
	MembershipNumber *string    `json:"membership_number,omitempty"`
	StartDate        time.Time  `json:"start_date"`
	EndDate          *time.Time `json:"end_date,omitempty"`
	EndReason        *string    `json:"end_reason,omitempty"`
}

type CreateInput struct {
	UserID           int64
	MembershipNumber *string
	StartDate        time.Time
}

type EndInput struct {
	EndDate   time.Time
	EndReason *string
}
