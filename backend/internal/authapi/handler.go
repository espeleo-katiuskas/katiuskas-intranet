// Package authapi exposes the public session endpoints (login, refresh,
// logout) that issue and revoke tokens on top of the users package.
package authapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"katiuskas-intranet/backend/internal/auth"
	"katiuskas-intranet/backend/internal/httpapi"
	"katiuskas-intranet/backend/internal/roles"
	"katiuskas-intranet/backend/internal/users"
)

type Handler struct {
	users         *users.Service
	roles         *roles.Service
	authenticator *auth.Authenticator
	refreshTTL    time.Duration
}

func NewHandler(usersService *users.Service, rolesService *roles.Service, authenticator *auth.Authenticator, refreshTTL time.Duration) *Handler {
	return &Handler{users: usersService, roles: rolesService, authenticator: authenticator, refreshTTL: refreshTTL}
}

type tokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	creds, err := h.users.VerifyCredentials(r.Context(), body.Username, body.Password)
	if err != nil {
		httpapi.WriteError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		return
	}
	pair, err := h.issueTokenPair(r.Context(), creds.UserID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not issue session")
		return
	}
	_ = h.users.TouchLastLogin(r.Context(), creds.UserID)
	httpapi.WriteJSON(w, http.StatusOK, pair)
}

func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	hash := hashToken(body.RefreshToken)
	userID, err := h.users.ResolveRefreshToken(r.Context(), hash)
	if err != nil {
		httpapi.WriteError(w, http.StatusUnauthorized, "invalid_token", "refresh token is invalid or expired")
		return
	}
	if _, err := h.users.GetCredentialsByUserID(r.Context(), userID); err != nil {
		httpapi.WriteError(w, http.StatusUnauthorized, "invalid_token", "account no longer exists")
		return
	}
	_ = h.users.RevokeRefreshToken(r.Context(), hash)
	pair, err := h.issueTokenPair(r.Context(), userID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not issue session")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, pair)
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	_ = h.users.RevokeRefreshToken(r.Context(), hashToken(body.RefreshToken))
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

func (h *Handler) issueTokenPair(ctx context.Context, userID int64) (tokenPair, error) {
	roleNames, err := h.roles.ListNamesForUser(ctx, userID)
	if err != nil {
		return tokenPair{}, err
	}
	access, err := h.authenticator.IssueAccessToken(userID, roleNames)
	if err != nil {
		return tokenPair{}, err
	}
	refresh, err := auth.NewRefreshToken()
	if err != nil {
		return tokenPair{}, err
	}
	if err := h.users.SaveRefreshToken(ctx, userID, hashToken(refresh), time.Now().Add(h.refreshTTL)); err != nil {
		return tokenPair{}, err
	}
	return tokenPair{AccessToken: access, RefreshToken: refresh}, nil
}

// hashToken stores only a digest of the refresh token, so a leaked database
// row can't be replayed as a session by itself.
func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
