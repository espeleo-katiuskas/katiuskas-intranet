// Package board tracks who has served on the Junta Directiva (board) and
// in what position, over time — start/end date periods, the same idea as
// memberships.Membership, since serving on the board is itself a kind of
// temporary status a person holds. The "board" role (see
// roles.Repository.ListNamesForUser) is computed live from whoever
// currently has an open period here, the same way "member"/
// "former_member" are computed from memberships — nobody grants or
// revokes it by hand anymore (see roles.ErrAutomaticRole).
package board

import "time"

type Position string

const (
	PositionPresident     Position = "president"
	PositionVicePresident Position = "vice_president"
	PositionSecretary     Position = "secretary"
	PositionTreasurer     Position = "treasurer"
	PositionMemberAtLarge Position = "member_at_large"
)

// Valid reports whether p is one of the positions the database's
// board_position enum accepts. Checked in Go (Service.Create/Update) so a
// misspelled one is a 400 rather than the 500 Postgres' own "invalid input
// value for enum" error would otherwise become.
func (p Position) Valid() bool {
	switch p {
	case PositionPresident, PositionVicePresident, PositionSecretary, PositionTreasurer, PositionMemberAtLarge:
		return true
	}
	return false
}

type Member struct {
	ID     int64 `json:"id"`
	UserID int64 `json:"-"`
	// UserLogin is filled in either by ListActive's JOIN (which spans
	// many different people at once, the same as its FirstName/LastName
	// columns) or, for a query scoped to one already-known
	// /users/{login}, directly by the handler — see
	// licenses.Handler for the same split, including the reverse lookup
	// Update needs since it's addressed by board-term id, not a user path.
	UserLogin string `json:"user_login"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	// Email/Phone are only ever populated by ListActive, joined in from
	// users — any signed-in member can see them here (cargo + how to
	// reach the current Junta Directiva), same as admin/board already
	// could through the person's own profile. Both are optional: not
	// everyone on the board necessarily has an email on file.
	Email     *string    `json:"email,omitempty"`
	Phone     *string    `json:"phone,omitempty"`
	Position  Position   `json:"position"`
	StartDate time.Time  `json:"start_date"`
	EndDate   *time.Time `json:"end_date,omitempty"`
}

type CreateInput struct {
	UserID    int64
	Position  Position
	StartDate time.Time
	EndDate   *time.Time // nil: the term is open
}

// UpdateInput corrects a board term's dates and position — all three
// always replace whatever was there, the same all-fields-required
// approach memberships.UpdateInput uses, since the edit form always
// sends the current values back rather than leaving anything ambiguous.
type UpdateInput struct {
	Position  Position
	StartDate time.Time
	EndDate   *time.Time
}
