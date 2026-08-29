// Package roles manages the set of roles a club can define and which
// people hold which ones. Three roles (admin, board, member) are seeded by
// migration and marked as system roles: they're the ones actually wired to
// permissions elsewhere in the API and can't be renamed away or deleted.
// Any other role an admin creates is free for the club to use as an
// organizational label — it doesn't unlock anything on its own.
package roles

type Role struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	IsSystem    bool    `json:"is_system"`
}

type CreateInput struct {
	Name        string
	Description *string
}
