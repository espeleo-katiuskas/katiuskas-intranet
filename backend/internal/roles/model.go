// Package roles manages the set of roles a club can define and which
// people hold which ones. Several roles (admin, board, member,
// former_member) are seeded by migration and marked as system roles:
// they're the ones actually wired to permissions or automatic behavior
// elsewhere in the API and can't be renamed away or deleted. Any other
// role an admin creates is free for the club to use as an organizational
// label — it doesn't unlock anything on its own.
//
// "member" and "former_member" are also automatic: nobody grants or
// revokes them by hand. A person holds "member" exactly when they have a
// currently active membership period, and "former_member" exactly when
// they've had one before but don't now — see
// Repository.ListNamesForUser, which computes both from the memberships
// table on every read instead of storing them in user_roles.
package roles

type Role struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	IsSystem    bool    `json:"is_system"`
	IsAutomatic bool    `json:"is_automatic"`
}

type CreateInput struct {
	Name        string
	Description *string
}
