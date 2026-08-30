// Package users manages personal data and login access. Anyone in here
// can sign in — there's no separate "granted access" step or table:
// requesting a magic link already requires knowing the person's email
// *and* phone (see authapi), so being in this table with both on file is
// the only gate. What a session can actually do is entirely down to
// roles, checked on every request (see the roles and auth packages).
package users

import "time"

type Gender string

const (
	GenderMale   Gender = "male"
	GenderFemale Gender = "female"
)

type User struct {
	ID               int64      `json:"id"`
	FirstName        string     `json:"first_name"`
	LastName         string     `json:"last_name"`
	NationalID       *string    `json:"national_id,omitempty"`
	BirthDate        *time.Time `json:"birth_date,omitempty"`
	Gender           *Gender    `json:"gender,omitempty"`
	Email            string     `json:"email"`
	Phone            *string    `json:"phone,omitempty"`
	Address          *string    `json:"address,omitempty"`
	PostalCode       *string    `json:"postal_code,omitempty"`
	City             *string    `json:"city,omitempty"`
	Province         *string    `json:"province,omitempty"`
	EmergencyContact *string    `json:"emergency_contact,omitempty"`
	// MembershipNumber identifies this person for as long as they're
	// associated with the club, not any single membership period —
	// someone who leaves and later rejoins keeps the same one. Set here
	// rather than per-period (see memberships.Membership) so a rejoin
	// never needs a fresh number, and never accidentally gets one. Not
	// settable through this package: memberships.Service.Create assigns
	// it automatically (see EnsureMembershipNumber) the first time a
	// person gets an alta, one past the current highest.
	MembershipNumber *int     `json:"membership_number,omitempty"`
	Roles            []string `json:"roles"`
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
	// OnLeave is only ever true alongside a currently-active membership
	// (nil MembershipEndDate): whether today falls within one of their
	// temporary leave windows, which a "member" role alone can't tell —
	// a leave never touches the membership period it belongs to.
	OnLeave bool `json:"on_leave"`
	// License* reflect the person's most recently issued federative
	// license (see licenses.Service.LatestByUser) — not settable through
	// this package, only through POST /users/{userID}/licenses.
	LicenseType       *string    `json:"license_type,omitempty"`
	LicenseIssuedDate *time.Time `json:"license_issued_date,omitempty"`
	LicenseValidUntil *time.Time `json:"license_valid_until,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}

type CreateInput struct {
	FirstName        string     `json:"first_name"`
	LastName         string     `json:"last_name"`
	NationalID       *string    `json:"national_id"`
	BirthDate        *time.Time `json:"birth_date"`
	Gender           *Gender    `json:"gender"`
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
	Gender           *Gender    `json:"gender"`
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
