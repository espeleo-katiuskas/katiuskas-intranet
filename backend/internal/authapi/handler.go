// Package authapi exposes the public session endpoints: request a
// magic-link sign-in email, consume that link, refresh a session, and log
// out. There are no passwords anywhere in this API.
package authapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"katiuskas-intranet/backend/internal/auth"
	"katiuskas-intranet/backend/internal/httpapi"
	"katiuskas-intranet/backend/internal/mail"
	"katiuskas-intranet/backend/internal/roles"
	"katiuskas-intranet/backend/internal/users"
)

// Options are the settings that shape magic-link emails: how long a link
// lives, how soon another one can be requested for the same person, the
// refresh token lifetime, and where the frontend is served (to build the
// link's URL).
type Options struct {
	RefreshTTL        time.Duration
	MagicLinkTTL      time.Duration
	MagicLinkCooldown time.Duration
	PublicBaseURL     string
}

type Handler struct {
	users         *users.Service
	roles         *roles.Service
	authenticator *auth.Authenticator
	mailer        mail.Sender
	opts          Options
}

func NewHandler(usersService *users.Service, rolesService *roles.Service, authenticator *auth.Authenticator, mailer mail.Sender, opts Options) *Handler {
	return &Handler{users: usersService, roles: rolesService, authenticator: authenticator, mailer: mailer, opts: opts}
}

type tokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// RequestMagicLink always answers the same way regardless of whether
// email+phone matched anyone, whether that person has access, or whether
// a link was actually sent (a recent one might already be on its way) —
// telling those cases apart would let this endpoint be used to check
// who's a member, or to spam a real person's inbox.
func (h *Handler) RequestMagicLink(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
		Phone string `json:"phone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if strings.TrimSpace(body.Email) == "" || strings.TrimSpace(body.Phone) == "" {
		httpapi.WriteError(w, http.StatusBadRequest, "missing_fields", "email and phone are both required")
		return
	}

	if err := h.sendMagicLinkIfEligible(r.Context(), body.Email, body.Phone); err != nil {
		slog.Error("magic link request failed", "error", err)
	}

	httpapi.WriteJSON(w, http.StatusOK, map[string]string{
		"message": "If those details match an account with access, an email with a sign-in link is on its way.",
	})
}

func (h *Handler) sendMagicLinkIfEligible(ctx context.Context, email, phone string) error {
	user, err := h.users.FindActiveByEmailAndPhone(ctx, email, phone)
	if errors.Is(err, users.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	recently, err := h.users.HasRecentMagicLinkToken(ctx, user.ID, time.Now().Add(-h.opts.MagicLinkCooldown))
	if err != nil {
		return err
	}
	if recently {
		return nil
	}

	rawToken, err := auth.NewRefreshToken()
	if err != nil {
		return err
	}
	if err := h.users.CreateMagicLinkToken(ctx, user.ID, hashToken(rawToken), time.Now().Add(h.opts.MagicLinkTTL)); err != nil {
		return err
	}

	link := fmt.Sprintf("%s/login?token=%s", h.opts.PublicBaseURL, rawToken)
	subject := "Accede a la intranet del Club de Espeleología Katiuskas"
	body := fmt.Sprintf(
		"Hola %s,\n\n"+
			"Entra en la intranet del Club de Espeleología Katiuskas con este enlace, válido durante %d minutos:\n%s\n\n"+
			"Si no has pedido este acceso, puedes ignorar este mensaje.",
		user.FirstName, int(h.opts.MagicLinkTTL.Minutes()), link,
	)
	return h.mailer.Send(ctx, user.Email, subject, body)
}

func (h *Handler) ConsumeMagicLink(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}

	userID, err := h.users.ConsumeMagicLinkToken(r.Context(), hashToken(body.Token))
	if errors.Is(err, users.ErrInvalidToken) {
		httpapi.WriteError(w, http.StatusUnauthorized, "invalid_token", "invalid or expired sign-in link")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not verify sign-in link")
		return
	}

	pair, err := h.issueTokenPair(r.Context(), userID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not issue session")
		return
	}
	_ = h.users.TouchLastLogin(r.Context(), userID)
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
	if _, err := h.users.GetAccountByUserID(r.Context(), userID); err != nil {
		httpapi.WriteError(w, http.StatusUnauthorized, "invalid_token", "account no longer has access")
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
	if err := h.users.SaveRefreshToken(ctx, userID, hashToken(refresh), time.Now().Add(h.opts.RefreshTTL)); err != nil {
		return tokenPair{}, err
	}
	return tokenPair{AccessToken: access, RefreshToken: refresh}, nil
}

// hashToken stores only a digest of an opaque token (refresh or
// magic-link), so a leaked database row can't be replayed as a session or
// a sign-in link by itself.
func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
