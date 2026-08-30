// Package tokens owns the single kind of bearer token this API issues
// for a signed-in session: an opaque, revocable, database-backed token
// with no separate short-lived counterpart. It also lets an admin see,
// re-expire, or revoke any token belonging to any person, since a single
// long-lived credential is worth being able to audit and cut off.
package tokens

import "time"

type Token struct {
	ID         int64      `json:"id"`
	UserID     int64      `json:"user_id"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	LastUsedIP *string    `json:"last_used_ip,omitempty"`
}
