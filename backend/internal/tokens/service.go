package tokens

import (
	"context"
	"errors"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
)

// ErrWrongUser means the token exists but doesn't belong to the user id
// the caller expected — the admin token-management routes are nested
// under /users/{login}/tokens/{tokenID}, so this catches, say, an id for
// someone else's token and reports it as not found rather than leaking
// that it exists.
var ErrWrongUser = errors.New("token does not belong to this user")

var (
	ErrCannotImpersonateSelf = errors.New("cannot impersonate yourself")
	// ErrNotEligibleForImpersonation also covers a target id that
	// doesn't exist at all: someone with no roles at all (real or not)
	// fails the same baseline-role check a real, role-less person would.
	ErrNotEligibleForImpersonation = errors.New("this person has no baseline role and can't be impersonated")
)

type Service struct {
	repo  *Repository
	roles *roles.Service
}

func NewService(repo *Repository, rolesService *roles.Service) *Service {
	return &Service{repo: repo, roles: rolesService}
}

// Issue mints a new opaque token for userID and returns the raw value —
// the only time it's ever available, since only its hash is stored.
func (s *Service) Issue(ctx context.Context, userID int64, ttl time.Duration) (raw string, err error) {
	raw, err = auth.NewOpaqueToken()
	if err != nil {
		return "", err
	}
	if _, err := s.repo.Create(ctx, userID, auth.HashToken(raw), time.Now().Add(ttl)); err != nil {
		return "", err
	}
	return raw, nil
}

// Authenticate satisfies auth.SessionResolver: it validates the raw
// token, records the use, and resolves the caller's roles fresh (rather
// than trusting anything baked into the token itself), so a role change
// takes effect on the very next request instead of waiting for the
// token to be reissued.
func (s *Service) Authenticate(ctx context.Context, rawToken, ip, userAgent string) (auth.AuthenticatedUser, error) {
	userID, impersonatedBy, err := s.repo.Authenticate(ctx, auth.HashToken(rawToken), ip, userAgent)
	if errors.Is(err, ErrNotFound) {
		return auth.AuthenticatedUser{}, auth.ErrInvalidToken
	}
	if err != nil {
		return auth.AuthenticatedUser{}, err
	}
	roleNames, err := s.roles.ListNamesForUser(ctx, userID)
	if err != nil {
		return auth.AuthenticatedUser{}, err
	}
	return auth.AuthenticatedUser{ID: userID, Roles: roleNames, ImpersonatedBy: impersonatedBy}, nil
}

// Impersonate mints a session token for targetUserID that behaves
// exactly like one they issued themselves — same id, same live-resolved
// roles, so every existing permission check in the API already treats it
// as if targetUserID had signed in, with no special-casing needed
// anywhere else. Only someone with a baseline role can be impersonated,
// the same eligibility a magic-link sign-in itself requires (see
// auth.HasBaselineRole) — anyone else, including a target id that
// doesn't exist, would end up with a session that can't call anything.
func (s *Service) Impersonate(ctx context.Context, impersonatorID, targetUserID int64, ttl time.Duration) (string, error) {
	if impersonatorID == targetUserID {
		return "", ErrCannotImpersonateSelf
	}
	roleNames, err := s.roles.ListNamesForUser(ctx, targetUserID)
	if err != nil {
		return "", err
	}
	if !auth.HasBaselineRole(roleNames) {
		return "", ErrNotEligibleForImpersonation
	}

	raw, err := auth.NewOpaqueToken()
	if err != nil {
		return "", err
	}
	if _, err := s.repo.CreateImpersonation(ctx, targetUserID, impersonatorID, auth.HashToken(raw), time.Now().Add(ttl)); err != nil {
		return "", err
	}
	return raw, nil
}

// ExitImpersonation revokes the current impersonation session and mints
// a fresh, normal one for impersonatorID — the admin who started it —
// rather than resuming whatever token they used before, which the API
// never has the raw value of again to hand back. Trusts rawToken the
// same way Logout does: having it is what proves this call is allowed to
// end that specific session.
func (s *Service) ExitImpersonation(ctx context.Context, rawToken string, impersonatorID int64, ttl time.Duration) (string, error) {
	if err := s.repo.RevokeByHash(ctx, auth.HashToken(rawToken)); err != nil {
		return "", err
	}
	raw, err := auth.NewOpaqueToken()
	if err != nil {
		return "", err
	}
	if _, err := s.repo.Create(ctx, impersonatorID, auth.HashToken(raw), time.Now().Add(ttl)); err != nil {
		return "", err
	}
	return raw, nil
}

func (s *Service) RevokeByHash(ctx context.Context, rawToken string) error {
	return s.repo.RevokeByHash(ctx, auth.HashToken(rawToken))
}

// IDForRawToken is the id of the session a raw bearer token belongs to, or
// ErrNotFound — to tell an admin which session in a listing is their own.
func (s *Service) IDForRawToken(ctx context.Context, rawToken string) (int64, error) {
	return s.repo.IDByHash(ctx, auth.HashToken(rawToken))
}

// List is ListAll's service face: the tokens of everyone, see
// Repository.ListAll.
func (s *Service) List(ctx context.Context, activeOnly bool, limit, offset int) ([]Token, error) {
	return s.repo.ListAll(ctx, activeOnly, limit, offset)
}

func (s *Service) ListForUser(ctx context.Context, userID int64) ([]Token, error) {
	return s.repo.ListForUser(ctx, userID)
}

// get looks a token up and confirms it belongs to userID, the shared
// check both admin mutations (UpdateExpiry, Revoke) need before touching
// someone's token.
func (s *Service) get(ctx context.Context, userID, tokenID int64) (Token, error) {
	t, err := s.repo.GetByID(ctx, tokenID)
	if err != nil {
		return t, err
	}
	if t.UserID != userID {
		return t, ErrWrongUser
	}
	return t, nil
}

// Get looks up a single token, confirming it belongs to userID — the
// exported counterpart to get, for callers outside this package that
// need to know a token's current state before changing it (see
// Handler.updateExpiry/revoke's own mutation notifications).
func (s *Service) Get(ctx context.Context, userID, tokenID int64) (Token, error) {
	return s.get(ctx, userID, tokenID)
}

func (s *Service) UpdateExpiry(ctx context.Context, userID, tokenID int64, expiresAt time.Time) error {
	if _, err := s.get(ctx, userID, tokenID); err != nil {
		return err
	}
	return s.repo.UpdateExpiry(ctx, tokenID, expiresAt)
}

func (s *Service) Revoke(ctx context.Context, userID, tokenID int64) error {
	if _, err := s.get(ctx, userID, tokenID); err != nil {
		return err
	}
	return s.repo.Revoke(ctx, tokenID)
}
