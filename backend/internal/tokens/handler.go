package tokens

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
)

// UserLookup resolves between a login and the numeric id it names. This
// package's own routes are nested under /users/{login}/..., but its
// tables are keyed by that person's numeric id, so every handler needs
// this translation before it can call into Service — users.Service
// satisfies this structurally (see main.go's wiring), without tokens
// having to import that package. LoginForUserID is only needed to name
// the *actor* in a mutation notification (see actorLogin) — the
// target's own login is always already in scope from the URL itself.
type UserLookup interface {
	UserIDForLogin(ctx context.Context, login string) (int64, error)
	LoginForUserID(ctx context.Context, id int64) (string, error)
}

type Handler struct {
	service               *Service
	authenticator         *auth.Authenticator
	users                 UserLookup
	impersonationTokenTTL time.Duration
	notifier              httpapi.MutationNotifier
	// publicBaseURL is only used to decide the impersonation session
	// cookie's Secure attribute — see auth.SetSessionCookie.
	publicBaseURL string
}

func NewHandler(service *Service, authenticator *auth.Authenticator, users UserLookup, impersonationTokenTTL time.Duration, notifier httpapi.MutationNotifier, publicBaseURL string) *Handler {
	return &Handler{service: service, authenticator: authenticator, users: users, impersonationTokenTTL: impersonationTokenTTL, notifier: notifier, publicBaseURL: publicBaseURL}
}

// actorLogin resolves the calling session's own login, for the "who
// did this" half of a mutation notification — every write endpoint in
// this handler needs it, so it's pulled out rather than repeated at
// each call site. Falls back to a placeholder rather than failing the
// whole request if the lookup itself errors, since a notification is
// never worth failing the actual write over.
func (h *Handler) actorLogin(ctx context.Context) string {
	current, _ := auth.UserFromContext(ctx)
	login, err := h.users.LoginForUserID(ctx, current.ID)
	if err != nil {
		return fmt.Sprintf("user#%d", current.ID)
	}
	return login
}

// loginOf is the login of a user by id, for a notification, with the same fallback as actorLogin.
func (h *Handler) loginOf(ctx context.Context, id int64) string {
	login, err := h.users.LoginForUserID(ctx, id)
	if err != nil {
		return fmt.Sprintf("user#%d", id)
	}
	return login
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/tokens", h.authenticator.Protect(h.listAll, "admin"))
	// A person sees and revokes their own sessions; an admin, anybody's (see sessionOwner). Changing
	// a session's expiry stays admin only.
	mux.Handle("GET /api/v1/users/{login}/tokens", h.authenticator.Protect(h.list))
	mux.Handle("PATCH /api/v1/users/{login}/tokens/{tokenID}", h.authenticator.Protect(h.updateExpiry, "admin"))
	mux.Handle("DELETE /api/v1/users/{login}/tokens/{tokenID}", h.authenticator.Protect(h.revoke))
	mux.Handle("POST /api/v1/users/{login}/impersonate", h.authenticator.Protect(h.impersonate, "admin"))
	// RequireAuth only, not Protect: while an admin acts with other roles (maybe none of them admin,
	// maybe not even one that can call anything else) they have to be able to change that and to put
	// it back; who may is checked from the person's own roles (see roleOverrideAllowed).
	mux.Handle("PUT /api/v1/auth/role-override", h.authenticator.RequireAuth(http.HandlerFunc(h.setRoleOverride)))
	mux.Handle("DELETE /api/v1/auth/role-override", h.authenticator.RequireAuth(http.HandlerFunc(h.clearRoleOverride)))
}

// roleOverrideAllowed says whether the session may act with other roles: only a real admin's own
// session, never an impersonation (it is somebody else's session already).
func roleOverrideAllowed(w http.ResponseWriter, user auth.AuthenticatedUser) bool {
	if user.ImpersonatedBy != nil {
		httpapi.WriteError(w, http.StatusConflict, "impersonating", "an impersonation session can not act with other roles")
		return false
	}
	for _, role := range user.RealRoles {
		if role == "admin" {
			return true
		}
	}
	httpapi.WriteError(w, http.StatusForbidden, "forbidden", "insufficient permissions")
	return false
}

func rawBearer(w http.ResponseWriter, r *http.Request) (string, bool) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || raw == "" {
		httpapi.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
		return "", false
	}
	return raw, true
}

// setRoleOverride lets an admin act, for the length of an impersonation, with the roles in the body
// instead of their own, to see what somebody with those roles sees. Nothing about anybody's real
// roles changes.
func (h *Handler) setRoleOverride(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	if !roleOverrideAllowed(w, user) {
		return
	}
	raw, ok := rawBearer(w, r)
	if !ok {
		return
	}
	var body struct {
		Roles []string `json:"roles"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	roles, until, err := h.service.SetRoleOverride(r.Context(), raw, body.Roles, h.impersonationTokenTTL)
	switch {
	case errors.Is(err, ErrNoRoles), errors.Is(err, ErrUnknownRole), errors.Is(err, ErrNoBaselineRole):
		code := "invalid_roles"
		if errors.Is(err, ErrUnknownRole) {
			code = "unknown_role"
		} else if errors.Is(err, ErrNoBaselineRole) {
			code = "no_baseline_role"
		}
		httpapi.WriteError(w, http.StatusBadRequest, code, err.Error())
		return
	case err != nil:
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not change the roles")
		return
	}
	// Not announced on Telegram: an admin changes roles often, to see what others see, and it only
	// adds noise (unlike impersonating somebody, which does say).
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"roles":        roles,
		"seconds_left": int(time.Until(until).Seconds()),
	})
}

// clearRoleOverride puts the session back to the person's own roles. It does nothing, and says so
// with the same 204, if there was no override.
func (h *Handler) clearRoleOverride(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	if !roleOverrideAllowed(w, user) {
		return
	}
	raw, ok := rawBearer(w, r)
	if !ok {
		return
	}
	if err := h.service.ClearRoleOverride(r.Context(), raw); err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not put the roles back")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resolveUserLogin looks up the numeric id behind a {login} path
// segment. Any failure — malformed, or simply nobody by that login — is
// reported the same way (404): this package only needs to know whether
// the lookup succeeded, not why it didn't.
func (h *Handler) resolveUserLogin(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := h.users.UserIDForLogin(r.Context(), r.PathValue("login"))
	if err != nil {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "user not found")
		return 0, false
	}
	return id, true
}

// impersonate is admin-only, deliberately not "admin"/"board": handing
// board members the ability to act as anyone (including other admins)
// is a bigger step than the rest of what they can already do.
func (h *Handler) impersonate(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.resolveUserLogin(w, r)
	if !ok {
		return
	}
	current, _ := auth.UserFromContext(r.Context())

	// Starting one from inside another (an admin impersonating an admin, who impersonates a third
	// person) records the admin who is really behind it, not the person they are acting as: the new
	// session is theirs, so ending it, or its end, goes back to them, and it is the one named in the
	// session lists and the banner. The chain never gets longer than one.
	impersonatorID := current.ID
	if current.ImpersonatedBy != nil {
		impersonatorID = *current.ImpersonatedBy
	}
	// Nor can somebody impersonate the person they already are in this session.
	if userID == current.ID {
		httpapi.WriteError(w, http.StatusConflict, "cannot_impersonate_self", ErrCannotImpersonateSelf.Error())
		return
	}

	raw, err := h.service.Impersonate(r.Context(), impersonatorID, userID, h.impersonationTokenTTL)
	if errors.Is(err, ErrCannotImpersonateSelf) {
		httpapi.WriteError(w, http.StatusConflict, "cannot_impersonate_self", err.Error())
		return
	}
	if errors.Is(err, ErrNotEligibleForImpersonation) {
		httpapi.WriteError(w, http.StatusConflict, "not_eligible", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not start impersonation")
		return
	}
	if current.ImpersonatedBy != nil {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🎭 %s ha impersonado a %s, desde su impersonación de %s.",
			h.loginOf(r.Context(), impersonatorID), r.PathValue("login"), h.actorLogin(r.Context())))
	} else {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🎭 %s ha impersonado a %s.",
			h.actorLogin(r.Context()), r.PathValue("login")))
	}
	// Also carried as a cookie (see auth.SetSessionCookie) so a file
	// opened straight in a new tab from library/userfiles while
	// impersonating resolves as the impersonated person too, exactly
	// like every other request the frontend makes with this same token.
	auth.SetSessionCookie(w, raw, h.publicBaseURL, h.impersonationTokenTTL)
	httpapi.WriteJSON(w, http.StatusCreated, map[string]string{"token": raw})
}

// listAll is the admin's overview of everyone's sessions: by default only
// the active ones (not revoked, not expired), most recently used first;
// ?status=all adds the history. sort (last_used, the default, created,
// expires or person) and order (asc or desc: the most recent first for the
// dates, A first for person) say in what order, and it holds across pages.
// limit (default 100, at most 200) and offset page through it; an out-of-range limit or negative offset falls
// back to its default, like the other listings. Revoking or re-expiring one
// still goes through /users/{login}/tokens/{tokenID}.
func (h *Handler) listAll(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	activeOnly := true
	switch q.Get("status") {
	case "", "active":
	case "all":
		activeOnly = false
	default:
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_status", "status must be active or all")
		return
	}
	order := DefaultListOrder
	if by := SortKey(q.Get("sort")); by != "" {
		if !by.Valid() {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_sort", "sort must be last_used, created, expires or person")
			return
		}
		order = ListOrder{By: by, Desc: by.DefaultDesc()}
	}
	switch q.Get("order") {
	case "":
	case "asc":
		order.Desc = false
	case "desc":
		order.Desc = true
	default:
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_order", "order must be asc or desc")
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	list, err := h.service.List(r.Context(), activeOnly, order, limit, offset)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list tokens")
		return
	}
	h.markCurrent(r, list)
	httpapi.WriteJSON(w, http.StatusOK, list)
}

// sessionOwner is resolveUserLogin for the routes a person may use on their own sessions: an admin
// may name anybody (404 for a login nobody has); anybody else only themselves, and a request for
// another's — or for a login nobody has, so that this cannot be used to check which exist — is a
// 403. Somebody impersonating that person sees their sessions as the person would (it is what they
// would see as themselves), but cannot end them (ending = true): a session started by impersonating
// someone is not that person acting for themselves, and ending sessions is not something to do
// in their name. The admin who wants to leaves the impersonation first.
func (h *Handler) sessionOwner(w http.ResponseWriter, r *http.Request, ending bool) (int64, bool) {
	current, _ := auth.UserFromContext(r.Context())
	userID, err := h.users.UserIDForLogin(r.Context(), r.PathValue("login"))
	if current.HasAnyRole("admin") {
		if err != nil {
			httpapi.WriteError(w, http.StatusNotFound, "not_found", "user not found")
			return 0, false
		}
		return userID, true
	}
	if err != nil || userID != current.ID {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "you can only see and end your own sessions")
		return 0, false
	}
	if ending && current.ImpersonatedBy != nil {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "sessions can't be ended through an impersonation: leave it first")
		return 0, false
	}
	return userID, true
}

// markCurrent flags, in list, the session the request is made with — if it is there; not finding
// it (a miss, or a lookup that fails) only means nothing is marked.
func (h *Handler) markCurrent(r *http.Request, list []Token) {
	raw, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if raw == "" {
		return
	}
	if currentID, err := h.service.IDForRawToken(r.Context(), raw); err == nil {
		for i := range list {
			list[i].Current = list[i].ID == currentID
		}
	}
}

// list is a person's sessions, newest first, with the one in use marked `current`. Everything by
// default (the audit view an admin gets); ?status=active leaves out the expired and revoked ones,
// which is what a person sees of their own.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.sessionOwner(w, r, false)
	if !ok {
		return
	}
	activeOnly := false
	switch r.URL.Query().Get("status") {
	case "", "all":
	case "active":
		activeOnly = true
	default:
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_status", "status must be active or all")
		return
	}
	list, err := h.service.ListForUser(r.Context(), userID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list tokens")
		return
	}
	if activeOnly {
		now := time.Now()
		active := list[:0]
		for _, t := range list {
			if t.RevokedAt == nil && t.ExpiresAt.After(now) {
				active = append(active, t)
			}
		}
		list = active
	}
	h.markCurrent(r, list)
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) updateExpiry(w http.ResponseWriter, r *http.Request) {
	userID, tokenID, ok := h.pathIDs(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if body.ExpiresAt.IsZero() {
		httpapi.WriteError(w, http.StatusBadRequest, "missing_fields", "expires_at is required")
		return
	}

	// Fetched before applying anything, purely to describe the old
	// expiry in the mutation notification below — a failure here isn't
	// reason enough to fail the actual edit, so it's only ever
	// consulted if it succeeded.
	before, beforeErr := h.service.Get(r.Context(), userID, tokenID)

	err := h.service.UpdateExpiry(r.Context(), userID, tokenID, body.ExpiresAt)
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrWrongUser) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "token not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not update token")
		return
	}
	if beforeErr == nil {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha cambiado la caducidad de un token de %s: %s → %s.",
			h.actorLogin(r.Context()), before.UserLogin,
			before.ExpiresAt.Format("2006-01-02 15:04"), body.ExpiresAt.Format("2006-01-02 15:04")))
	}
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

func (h *Handler) revoke(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.sessionOwner(w, r, true)
	if !ok {
		return
	}
	tokenID, ok := h.tokenID(w, r)
	if !ok {
		return
	}
	// Fetched before revoking, purely so the mutation notification below
	// can still name whose token it was.
	target, targetErr := h.service.Get(r.Context(), userID, tokenID)

	err := h.service.Revoke(r.Context(), userID, tokenID)
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrWrongUser) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "token not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not revoke token")
		return
	}
	if targetErr == nil {
		if current, _ := auth.UserFromContext(r.Context()); current.ID == userID {
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha cerrado una de sus sesiones.", target.UserLogin))
		} else {
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha revocado un token de %s.",
				h.actorLogin(r.Context()), target.UserLogin))
		}
	}
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

func (h *Handler) pathIDs(w http.ResponseWriter, r *http.Request) (userID, tokenID int64, ok bool) {
	userID, ok = h.resolveUserLogin(w, r)
	if !ok {
		return 0, 0, false
	}
	tokenID, ok = h.tokenID(w, r)
	return userID, tokenID, ok
}

func (h *Handler) tokenID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	tokenID, err := strconv.ParseInt(r.PathValue("tokenID"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "token id must be numeric")
		return 0, false
	}
	return tokenID, true
}
