package tokens

import (
	"context"
	"errors"
	"time"

	"katiuskas-intranet/backend/internal/auth"
	"katiuskas-intranet/backend/internal/roles"
)

// ErrWrongUser means the token exists but doesn't belong to the user id
// the caller expected — the admin token-management routes are nested
// under /users/{userID}/tokens/{tokenID}, so this catches, say, an id for
// someone else's token and reports it as not found rather than leaking
// that it exists.
var ErrWrongUser = errors.New("token does not belong to this user")

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
func (s *Service) Authenticate(ctx context.Context, rawToken, ip string) (auth.AuthenticatedUser, error) {
	userID, err := s.repo.Authenticate(ctx, auth.HashToken(rawToken), ip)
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
	return auth.AuthenticatedUser{ID: userID, Roles: roleNames}, nil
}

func (s *Service) RevokeByHash(ctx context.Context, rawToken string) error {
	return s.repo.RevokeByHash(ctx, auth.HashToken(rawToken))
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
