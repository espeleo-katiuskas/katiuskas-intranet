// Package users manages personal data and login access. Anyone in here
// can sign in — there's no separate "granted access" step or table:
// requesting a magic link already requires knowing one of the person's
// emails *and* one of their phones (see authapi), so being in this table
// with both on file is the only gate. What a session can actually do is
// entirely down to roles, checked on every request (see the roles and
// auth packages).
package users

import "time"

type Gender string

const (
	GenderMale   Gender = "male"
	GenderFemale Gender = "female"
)

type AgeCategory string

const (
	AgeCategoryChild AgeCategory = "child"
	AgeCategoryTeen  AgeCategory = "teen"
	AgeCategoryAdult AgeCategory = "adult"
)

// ageCategoryFor buckets a birth date into a caving-club-style age
// category, based on the age the person turns *this calendar year* (not
// their exact age today) — child up to 13, teen 14-17, adult 18 and up.
// Exists so MemberListing can show a coarse age bracket without exposing
// the exact birth date it's computed from — see ForMemberListing.
func ageCategoryFor(birthDate time.Time) AgeCategory {
	ageThisYear := time.Now().Year() - birthDate.Year()
	switch {
	case ageThisYear <= 13:
		return AgeCategoryChild
	case ageThisYear <= 17:
		return AgeCategoryTeen
	default:
		return AgeCategoryAdult
	}
}

type User struct {
	// ID is never exposed over the API — Login is this person's only
	// externally visible identifier (used in URLs and, later, as the
	// name of their private-document directory), so every JSON response
	// and path parameter that used to carry a numeric id now carries
	// this instead. ID stays on the struct because the Go layer still
	// needs it for foreign keys and permission checks.
	ID         int64     `json:"-"`
	Login      string    `json:"login"`
	FirstName  string    `json:"first_name"`
	LastName   string    `json:"last_name"`
	NationalID *string   `json:"national_id,omitempty"`
	BirthDate  time.Time `json:"birth_date"`
	Gender     *Gender   `json:"gender,omitempty"`
	// Email and Phone can each hold more than one value, space-separated
	// (like EmergencyContact already allows) — someone with a personal
	// and a work address, say. Signing in, and a magic link going out,
	// works against any one of them (see
	// Repository.FindActiveByEmailAndPhone); one email still can't
	// belong to two different people (Repository enforces that in Go,
	// since the database can no longer express it as a plain column
	// constraint once it's not a single atomic value). Email is
	// optional — someone with none on file simply can never receive a
	// magic link, the same way someone with no phone already couldn't.
	Email            *string `json:"email,omitempty"`
	Phone            *string `json:"phone,omitempty"`
	Address          *string `json:"address,omitempty"`
	PostalCode       *string `json:"postal_code,omitempty"`
	City             *string `json:"city,omitempty"`
	Province         *string `json:"province,omitempty"`
	EmergencyContact *string `json:"emergency_contact,omitempty"`
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
	// MembershipStartDate is the start of the person's *first* membership
	// period ever, and MembershipEndDate the end of their most recent one
	// (see Service.enrich) — someone who left and rejoined has more than
	// one period, and these two dates can come from different ones. A nil
	// end date with a non-nil start date means they're currently a
	// member; both nil means they've never been one.
	MembershipStartDate *time.Time `json:"membership_start_date,omitempty"`
	MembershipEndDate   *time.Time `json:"membership_end_date,omitempty"`
	// OnLeave is only ever true alongside a currently-active membership
	// (nil MembershipEndDate): whether today falls within one of their
	// temporary leave windows — a leave never touches the membership
	// period it belongs to. Also exposed on MemberListing (see
	// ForMemberListing), so a plain member's own "membership_status"
	// can tell "baja temporal" apart from "activo" too, not just
	// admin/board.
	OnLeave bool `json:"on_leave"`
	// License* reflect the person's most recently issued federative
	// license (see licenses.Service.LatestByUser) — not settable through
	// this package, only through POST /users/{login}/licenses.
	LicenseType       *string    `json:"license_type,omitempty"`
	LicenseIssuedDate *time.Time `json:"license_issued_date,omitempty"`
	LicenseValidUntil *time.Time `json:"license_valid_until,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}

type CreateInput struct {
	FirstName        string    `json:"first_name"`
	LastName         string    `json:"last_name"`
	NationalID       *string   `json:"national_id"`
	BirthDate        time.Time `json:"birth_date"`
	Gender           *Gender   `json:"gender"`
	Email            *string   `json:"email"`
	Phone            *string   `json:"phone"`
	Address          *string   `json:"address"`
	PostalCode       *string   `json:"postal_code"`
	City             *string   `json:"city"`
	Province         *string   `json:"province"`
	EmergencyContact *string   `json:"emergency_contact"`
	// RoleNames are assigned in the same call. Doesn't need — and can't
	// include — "member", "former_member", or "board": those are
	// automatic, derived from membership/board history instead of
	// granted by hand.
	RoleNames []string `json:"role_names"`
}

// UpdateInput covers every personal-data field, including ones that also
// have identity/uniqueness weight (email, national ID). Only admin can
// set every field on anyone; a person editing their own record (the
// only other case the handler allows at all) is restricted to the
// contact fields — Email, Phone, Address, PostalCode, City, Province,
// EmergencyContact — enforced in Handler.update, not here, since it's a
// permission concern rather than a data-shape one.
type UpdateInput struct {
	// Login is admin-only, same as the identity fields below it — it's
	// referenced from URLs (and, later, a filesystem directory name)
	// throughout the app, so a self-service typo or joke has a bigger
	// blast radius than a wrong phone number. Auto-generated at creation
	// time (see Repository.generateLogin); this is only for correcting
	// or deliberately changing one afterward.
	Login            *string    `json:"login"`
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
	Login     string `json:"login"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

// MemberListing is what GET /users returns to a caller who's a plain
// member (not admin or board): no DNI, address, contact info, or
// anything else that isn't already effectively public knowledge inside
// the club — just enough to see who's a socio, since when, to whom,
// whether they're currently on a temporary leave, and which
// roles/cargo they hold. AgeCategory stands in for BirthDate itself,
// which isn't exposed here: the coarse bracket is enough to know at a
// glance without revealing everyone's exact birth date. Handler.list
// also only ever includes people who are or have been a socio in this
// listing at all — someone who's never had a membership period (an
// admin-only account, say) isn't something a plain member needs to see
// here.
type MemberListing struct {
	Login               string      `json:"login"`
	FirstName           string      `json:"first_name"`
	LastName            string      `json:"last_name"`
	Gender              *Gender     `json:"gender,omitempty"`
	AgeCategory         AgeCategory `json:"age_category"`
	MembershipNumber    *int        `json:"membership_number,omitempty"`
	MembershipStartDate *time.Time  `json:"membership_start_date,omitempty"`
	MembershipEndDate   *time.Time  `json:"membership_end_date,omitempty"`
	OnLeave             bool        `json:"on_leave"`
	Roles               []string    `json:"roles"`
}

// ForMemberListing narrows a full User down to what MemberListing
// exposes — used by Handler.list so the restriction lives in one place
// rather than being reimplemented at every call site.
func (u User) ForMemberListing() MemberListing {
	return MemberListing{
		Login:               u.Login,
		FirstName:           u.FirstName,
		LastName:            u.LastName,
		Gender:              u.Gender,
		AgeCategory:         ageCategoryFor(u.BirthDate),
		MembershipNumber:    u.MembershipNumber,
		MembershipStartDate: u.MembershipStartDate,
		MembershipEndDate:   u.MembershipEndDate,
		OnLeave:             u.OnLeave,
		Roles:               u.Roles,
	}
}
