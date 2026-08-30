// Package authapi exposes the public session endpoints: request a
// magic-link sign-in email, consume that link, and log out. There are no
// passwords anywhere in this API, and no separate refresh token either —
// signing in issues one opaque, revocable token that's used directly on
// every request until it's revoked or expires.
package authapi

import (
	"context"
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
	"katiuskas-intranet/backend/internal/tokens"
	"katiuskas-intranet/backend/internal/users"
)

// Options are the settings that shape magic-link emails: how long a link
// lives, how soon another one can be requested for the same person, how
// long a freshly issued session token lasts before an admin needs to
// extend it, and where the frontend is served (to build the link's URL).
type Options struct {
	TokenTTL          time.Duration
	MagicLinkTTL      time.Duration
	MagicLinkCooldown time.Duration
	PublicBaseURL     string
}

type Handler struct {
	users         *users.Service
	roles         *roles.Service
	tokens        *tokens.Service
	authenticator *auth.Authenticator
	mailer        mail.Sender
	opts          Options
}

func NewHandler(usersService *users.Service, rolesService *roles.Service, tokensService *tokens.Service, authenticator *auth.Authenticator, mailer mail.Sender, opts Options) *Handler {
	return &Handler{users: usersService, roles: rolesService, tokens: tokensService, authenticator: authenticator, mailer: mailer, opts: opts}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/magic-link", h.RequestMagicLink)
	mux.HandleFunc("POST /api/v1/auth/magic-link/consume", h.ConsumeMagicLink)
	mux.HandleFunc("POST /api/v1/auth/logout", h.Logout)
	mux.Handle("GET /api/v1/auth/me", h.authenticator.Protect(h.Me))
}

// RequestMagicLink always answers the same way regardless of whether
// email+phone matched anyone, whether that person holds a role that
// could actually use the API (see auth.BaselineRoles), or whether a
// link was actually sent (a recent one might already be on its way) —
// telling those cases apart would let this endpoint be used to check
// who's in the system, or to spam a real person's inbox.
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
		"message": "If those details match someone in our records, an email with a sign-in link is on its way.",
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

	// Signing in would be pointless for someone who can't call the API
	// anyway (see auth.BaselineRoles) — don't hand out a link, and don't
	// even let the cooldown check below reveal that this email/phone
	// combination exists but has no baseline role.
	roleNames, err := h.roles.ListNamesForUser(ctx, user.ID)
	if err != nil {
		return err
	}
	if !auth.HasBaselineRole(roleNames) {
		return nil
	}

	recently, err := h.users.HasRecentMagicLinkToken(ctx, user.ID, time.Now().Add(-h.opts.MagicLinkCooldown))
	if err != nil {
		return err
	}
	if recently {
		return nil
	}

	rawToken, err := auth.NewOpaqueToken()
	if err != nil {
		return err
	}
	if err := h.users.CreateMagicLinkToken(ctx, user.ID, auth.HashToken(rawToken), time.Now().Add(h.opts.MagicLinkTTL)); err != nil {
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

	userID, err := h.users.ConsumeMagicLinkToken(r.Context(), auth.HashToken(body.Token))
	if errors.Is(err, users.ErrInvalidToken) {
		httpapi.WriteError(w, http.StatusUnauthorized, "invalid_token", "invalid or expired sign-in link")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not verify sign-in link")
		return
	}

	sessionToken, err := h.tokens.Issue(r.Context(), userID, h.opts.TokenTTL)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not issue session")
		return
	}
	_ = h.users.TouchLastLogin(r.Context(), userID, httpapi.ClientIP(r))
	httpapi.WriteJSON(w, http.StatusOK, map[string]string{"token": sessionToken})
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	_ = h.tokens.RevokeByHash(r.Context(), body.Token)
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

// Me reports the identity and roles of whoever the request's bearer
// token belongs to, resolved by the very auth middleware that let the
// request through — so it costs no extra database round trip. The
// frontend calls this once per page load to know who's signed in and
// what to show them, instead of decoding anything out of the token
// itself (there's nothing to decode: it's opaque).
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"id": user.ID, "roles": user.Roles})
}
