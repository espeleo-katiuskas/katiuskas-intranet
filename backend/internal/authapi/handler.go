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

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/mail"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/tokens"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
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
	mux.HandleFunc("POST /api/v1/auth/magic-link/consume-code", h.ConsumeMagicLinkCode)
	mux.HandleFunc("POST /api/v1/auth/logout", h.Logout)
	mux.Handle("GET /api/v1/auth/me", h.authenticator.Protect(h.Me))
	// RequireAuth only, not Protect: exiting impersonation must work even
	// if the impersonated person has since lost their baseline role (a
	// lapsed membership, say) — otherwise that admin would be stuck
	// impersonating someone whose session can no longer call anything,
	// including this.
	mux.Handle("POST /api/v1/auth/impersonate/exit", h.authenticator.RequireAuth(http.HandlerFunc(h.ExitImpersonation)))
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
	code, err := auth.NewLoginCode()
	if err != nil {
		return err
	}
	if err := h.users.CreateMagicLinkToken(ctx, user.ID, auth.HashToken(rawToken), auth.HashToken(code), time.Now().Add(h.opts.MagicLinkTTL)); err != nil {
		return err
	}

	link := fmt.Sprintf("%s/login?token=%s", h.opts.PublicBaseURL, rawToken)
	subject := "Accede a la intranet del Club de Espeleología Katiuskas"
	body := fmt.Sprintf(
		"Hola %s,\n\n"+
			"Entra en la intranet del Club de Espeleología Katiuskas con este enlace, válido durante %d minutos:\n%s\n\n"+
			"¿Leyendo este correo desde otro dispositivo? También puedes escribir este código en la "+
			"página de acceso: %s\n\n"+
			"Si no has pedido este acceso, puedes ignorar este mensaje.",
		user.FirstName, int(h.opts.MagicLinkTTL.Minutes()), link, code,
	)
	// user.Email may hold more than one address, space-separated (see
	// users.User.Email) — mail.Sender expects a comma-separated list.
	return h.mailer.Send(ctx, strings.Join(strings.Fields(user.Email), ", "), subject, body)
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
	h.issueSessionAndRespond(w, r, userID)
}

// ConsumeMagicLinkCode is the code counterpart to ConsumeMagicLink, for
// reading the email on one device and signing in on another — email and
// phone identify the person exactly as requesting a link does (the code
// alone is too low-entropy to look someone up by), and the response is
// the same generic 401 whether the code was wrong, expired, already
// used, guessed too many times, or the email/phone didn't match anyone
// at all: nothing here should tell those cases apart from the outside.
func (h *Handler) ConsumeMagicLinkCode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
		Phone string `json:"phone"`
		Code  string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if strings.TrimSpace(body.Email) == "" || strings.TrimSpace(body.Phone) == "" || strings.TrimSpace(body.Code) == "" {
		httpapi.WriteError(w, http.StatusBadRequest, "missing_fields", "email, phone, and code are all required")
		return
	}

	user, err := h.users.FindActiveByEmailAndPhone(r.Context(), body.Email, body.Phone)
	if err != nil {
		if !errors.Is(err, users.ErrNotFound) {
			slog.Error("consume magic link code: lookup failed", "error", err)
		}
		httpapi.WriteError(w, http.StatusUnauthorized, "invalid_code", "invalid or expired code")
		return
	}
	userID, err := h.users.ConsumeMagicLinkCode(r.Context(), user.ID, auth.HashToken(body.Code))
	if errors.Is(err, users.ErrInvalidToken) {
		httpapi.WriteError(w, http.StatusUnauthorized, "invalid_code", "invalid or expired code")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not verify code")
		return
	}
	h.issueSessionAndRespond(w, r, userID)
}

func (h *Handler) issueSessionAndRespond(w http.ResponseWriter, r *http.Request, userID int64) {
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
	login, err := h.users.LoginForUserID(r.Context(), user.ID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not resolve identity")
		return
	}
	response := map[string]any{"login": login, "roles": user.Roles}
	if user.ImpersonatedBy != nil {
		response["impersonated_by"] = h.impersonatorSummary(r.Context(), *user.ImpersonatedBy)
	}
	httpapi.WriteJSON(w, http.StatusOK, response)
}

// impersonatorSummary is best-effort: it still gives the frontend banner
// something to show even if, say, the admin's own account was since
// deleted (an empty object) — never a numeric id.
func (h *Handler) impersonatorSummary(ctx context.Context, impersonatorID int64) map[string]any {
	impersonator, err := h.users.Get(ctx, impersonatorID)
	if err != nil {
		return map[string]any{}
	}
	return map[string]any{"login": impersonator.Login, "first_name": impersonator.FirstName, "last_name": impersonator.LastName}
}

// ExitImpersonation ends the current impersonation session and hands
// back a fresh, normal token for the admin who started it — see
// tokens.Service.ExitImpersonation for why it's a new token rather than
// the one they had before.
func (h *Handler) ExitImpersonation(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	if user.ImpersonatedBy == nil {
		httpapi.WriteError(w, http.StatusConflict, "not_impersonating", "this session isn't an impersonation")
		return
	}
	rawToken, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || rawToken == "" {
		httpapi.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
		return
	}

	newToken, err := h.tokens.ExitImpersonation(r.Context(), rawToken, *user.ImpersonatedBy, h.opts.TokenTTL)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not exit impersonation")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]string{"token": newToken})
}
