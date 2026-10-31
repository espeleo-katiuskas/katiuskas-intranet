// Package tokens owns the single kind of bearer token this API issues
// for a signed-in session: an opaque, revocable, database-backed token
// with no separate short-lived counterpart. It also lets an admin see,
// re-expire, or revoke any token belonging to any person, since a single
// long-lived credential is worth being able to audit and cut off.
package tokens

import "time"

type Token struct {
	ID     int64 `json:"id"`
	UserID int64 `json:"-"`
	// UserLogin is only populated by queries that JOIN against users
	// (GetByID, ListForUser) — Create and CreateImpersonation return a
	// Token whose caller only ever checks the error, so they skip the
	// join and leave this blank rather than pay for it needlessly.
	UserLogin  string     `json:"user_login"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	LastUsedIP *string    `json:"last_used_ip,omitempty"`
	// LastUsedUserAgent is the User-Agent of the last request made with the token, as sent
	// (cut to a sane length); Browser and OS are worked out from it when a token is read
	// (DetectClientFromUA), never stored.
	LastUsedUserAgent *string `json:"last_used_user_agent,omitempty"`
	Browser           string  `json:"browser,omitempty"`
	OS                string  `json:"os,omitempty"`
	// ImpersonatedBy/ImpersonatedByLogin are set only for a token minted
	// by POST /users/{login}/impersonate: the admin who started it, not
	// UserID/UserLogin (whose session this actually is).
	// Current marks, only in GET /tokens, the one session that is making the
	// request — so the admin looking at the list can tell which one is theirs
	// (revoking it signs them out).
	Current             bool    `json:"current,omitempty"`
	ImpersonatedBy      *int64  `json:"-"`
	ImpersonatedByLogin *string `json:"impersonated_by_login,omitempty"`
}
