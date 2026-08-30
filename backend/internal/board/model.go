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

type Member struct {
	ID        int64      `json:"id"`
	UserID    int64      `json:"user_id"`
	FirstName string     `json:"first_name,omitempty"`
	LastName  string     `json:"last_name,omitempty"`
	Position  Position   `json:"position"`
	StartDate time.Time  `json:"start_date"`
	EndDate   *time.Time `json:"end_date,omitempty"`
}

type CreateInput struct {
	UserID    int64
	Position  Position
	StartDate time.Time
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
