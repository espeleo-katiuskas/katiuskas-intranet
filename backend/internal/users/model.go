// Package users manages personal data and login access. Anyone in here
// can sign in — there's no separate "granted access" step or table:
// requesting a magic link already requires knowing the person's email
// *and* phone (see authapi), so being in this table with both on file is
// the only gate. What a session can actually do is entirely down to
// roles, checked on every request (see the roles and auth packages).
package users

import "time"

type User struct {
	ID               int64      `json:"id"`
	FirstName        string     `json:"first_name"`
	LastName         string     `json:"last_name"`
	NationalID       *string    `json:"national_id,omitempty"`
	BirthDate        *time.Time `json:"birth_date,omitempty"`
	Email            string     `json:"email"`
	Phone            *string    `json:"phone,omitempty"`
	Address          *string    `json:"address,omitempty"`
	PostalCode       *string    `json:"postal_code,omitempty"`
	City             *string    `json:"city,omitempty"`
	Province         *string    `json:"province,omitempty"`
	EmergencyContact *string    `json:"emergency_contact,omitempty"`
	Roles            []string   `json:"roles"`
	// LastLoginAt/LastLoginIP are informational only — updated on every
	// successful magic-link consume (see authapi.Handler.issueTokenPair),
	// never checked for authorization.
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	LastLoginIP *string    `json:"last_login_ip,omitempty"`
	// MembershipStartDate/MembershipEndDate reflect the person's most
	// recent membership period (see memberships.Service.LatestByUser): a
	// nil end date with a non-nil start date means they're currently a
	// member; both nil means they've never been one.
	MembershipStartDate *time.Time `json:"membership_start_date,omitempty"`
	MembershipEndDate   *time.Time `json:"membership_end_date,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
}

type CreateInput struct {
	FirstName        string     `json:"first_name"`
	LastName         string     `json:"last_name"`
	NationalID       *string    `json:"national_id"`
	BirthDate        *time.Time `json:"birth_date"`
	Email            string     `json:"email"`
	Phone            *string    `json:"phone"`
	Address          *string    `json:"address"`
	PostalCode       *string    `json:"postal_code"`
	City             *string    `json:"city"`
	Province         *string    `json:"province"`
	EmergencyContact *string    `json:"emergency_contact"`
	// RoleNames are assigned in the same call. Doesn't need — and can't
	// include — "member" or "former_member": those are automatic,
	// derived from membership history instead of granted by hand.
	RoleNames []string `json:"role_names"`
}

// UpdateInput covers every personal-data field, including ones that also
// have identity/uniqueness weight (email, national ID). The API applies the
// same permission rule to all of them: admin/board can edit anyone, a
// member can only edit their own record — there's no per-field split.
type UpdateInput struct {
	FirstName        *string    `json:"first_name"`
	LastName         *string    `json:"last_name"`
	Email            *string    `json:"email"`
	NationalID       *string    `json:"national_id"`
	BirthDate        *time.Time `json:"birth_date"`
	Phone            *string    `json:"phone"`
	Address          *string    `json:"address"`
	PostalCode       *string    `json:"postal_code"`
	City             *string    `json:"city"`
	Province         *string    `json:"province"`
	EmergencyContact *string    `json:"emergency_contact"`
}

// DirectoryEntry is the bare minimum needed to let any member pick a
// person out of a list — e.g. an activity's leader or participants —
// without exposing the personal data GET /users returns to admin/board.
type DirectoryEntry struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}
