// Package users manages personal data and login access. A person can exist
// here without ever getting access (Account), which is why the two are
// kept in separate tables behind a single package.
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
	// HasAccess is whether this person can sign in at all (has an accounts
	// row). It says nothing about *which* roles they hold.
	HasAccess bool      `json:"has_access"`
	Roles     []string  `json:"roles"`
	CreatedAt time.Time `json:"created_at"`
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
	// GrantAccess creates an accounts row so this person can sign in with
	// a magic link. Since that link is only ever requested by matching
	// email *and* phone, Phone is required when this is true.
	GrantAccess bool `json:"grant_access"`
	// RoleNames defaults to ["member"] when empty (see handler).
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

// Account is a person's ability to sign in at all — separate from Roles,
// which govern what they can do once signed in.
type Account struct {
	UserID   int64
	IsActive bool
}

// DirectoryEntry is the bare minimum needed to let any member pick a
// person out of a list — e.g. an activity's leader or participants —
// without exposing the personal data GET /users returns to admin/board.
type DirectoryEntry struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}
