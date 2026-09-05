// Package licenses tracks federative licenses ("licencias federativas")
// held by a person: a type chosen from a fixed-but-evolving catalog
// (license_types, see migration 00007) together with the date it was
// issued. "Otra" is the catalog's escape hatch — its IsOther flag is
// true — and requires OtherDetail to say which license it actually is.
// A license is annual: valid from IssuedDate through December 31 of
// that same year. Only someone who had an open membership period on
// IssuedDate itself can be assigned one — not necessarily right now, so
// a license from while someone was a socio can still be recorded after
// they've since caused baja; see Service.Create.
package licenses

import "time"

// LicenseType is one entry in the license name catalog — kept in the
// database rather than a Go enum, since the list of valid names changes
// over time (a new zone, a new federation) without needing a code
// deploy.
type LicenseType struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	IsOther bool   `json:"is_other"`
}

type License struct {
	ID     int64  `json:"id"`
	UserID int64  `json:"-"`
	// UserLogin is filled in by the handler, not this package's queries:
	// every route here is either scoped under a known /users/{login}, or
	// resolves the owner separately after the fact (see
	// Handler.update) — cheaper than joining against users on every
	// query just to expose an identifier the caller already gave us.
	UserLogin     string `json:"user_login"`
	LicenseTypeID int64  `json:"license_type_id"`
	// Type is the human-readable label: the catalog name, or
	// "<name>: <other_detail>" when the chosen type is "Otra".
	Type        string    `json:"type"`
	OtherDetail *string   `json:"other_detail,omitempty"`
	IssuedDate  time.Time `json:"issued_date"`
	// ValidUntil is always December 31 of IssuedDate's year — computed,
	// never stored, since it's fully determined by IssuedDate.
	ValidUntil time.Time `json:"valid_until"`
	CreatedAt  time.Time `json:"created_at"`
}

type CreateInput struct {
	UserID        int64
	LicenseTypeID int64
	// OtherDetail is required when LicenseTypeID refers to the "Otra"
	// entry, and forbidden otherwise — see Service.Create.
	OtherDetail *string
	IssuedDate  time.Time
}

// UpdateInput corrects a license: every field is always sent together
// as a full replace, mirroring memberships.UpdateInput — there's no
// ambiguity between "leave as is" and "clear it" to resolve. Same
// OtherDetail rule as CreateInput; see Service.Update.
type UpdateInput struct {
	LicenseTypeID int64
	OtherDetail   *string
	IssuedDate    time.Time
}
