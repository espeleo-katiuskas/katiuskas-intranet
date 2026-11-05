package licenses

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

// UserLookup translates between a person's login and their numeric id.
// This package's own routes are nested under /users/{login}/licenses,
// but the licenses table is keyed by numeric user id — users.Service
// satisfies this structurally (see main.go's wiring), without licenses
// having to import that package (it can't: users already imports this
// one).
type UserLookup interface {
	UserIDForLogin(ctx context.Context, login string) (int64, error)
	LoginForUserID(ctx context.Context, id int64) (string, error)
}

type Handler struct {
	service       *Service
	authenticator *auth.Authenticator
	users         UserLookup
	notifier      httpapi.MutationNotifier
}

func NewHandler(service *Service, authenticator *auth.Authenticator, users UserLookup, notifier httpapi.MutationNotifier) *Handler {
	return &Handler{service: service, authenticator: authenticator, users: users, notifier: notifier}
}

// actorLogin resolves the calling session's own login, for the "who
// did this" half of a mutation notification — see users.Handler's own
// copy of this same helper. Falls back to a placeholder rather than
// failing the whole request if the lookup itself errors, since a
// notification is never worth failing the actual write over.
func (h *Handler) actorLogin(ctx context.Context) string {
	current, _ := auth.UserFromContext(ctx)
	login, err := h.users.LoginForUserID(ctx, current.ID)
	if err != nil {
		return fmt.Sprintf("user#%d", current.ID)
	}
	return login
}

// POST/PUT/DELETE are admin-only: board can see everyone's licenses
// (read-only, like the rest of a person's data) but not assign, correct,
// or remove one.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/users/{login}/licenses", h.authenticator.Protect(h.listByUser))
	mux.Handle("POST /api/v1/users/{login}/licenses", h.authenticator.Protect(h.create, "admin"))
	mux.Handle("PUT /api/v1/licenses/{id}", h.authenticator.Protect(h.update, "admin"))
	mux.Handle("DELETE /api/v1/licenses/{id}", h.authenticator.Protect(h.delete, "admin"))
	mux.Handle("GET /api/v1/license-types", h.authenticator.Protect(h.listTypes, "admin"))
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

// listTypes is admin-only, matching create: the catalog only exists to
// populate the "assign a license" form.
func (h *Handler) listTypes(w http.ResponseWriter, r *http.Request) {
	list, err := h.service.ListTypes(r.Context())
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list license types")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) listByUser(w http.ResponseWriter, r *http.Request) {
	login := r.PathValue("login")
	userID, ok := h.resolveUserLogin(w, r)
	if !ok {
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	if !current.HasAnyRole("admin", "board") && current.ID != userID {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "members can only view their own license history")
		return
	}
	list, err := h.service.ListByUser(r.Context(), userID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list licenses")
		return
	}
	for i := range list {
		list[i].UserLogin = login
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	login := r.PathValue("login")
	userID, ok := h.resolveUserLogin(w, r)
	if !ok {
		return
	}
	var body struct {
		LicenseTypeID int64   `json:"license_type_id"`
		OtherDetail   *string `json:"other_detail"`
		IssuedDate    string  `json:"issued_date"`
		Year          int     `json:"year"`
		TransactionID *int64  `json:"transaction_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if body.LicenseTypeID == 0 || body.Year == 0 {
		httpapi.WriteError(w, http.StatusBadRequest, "missing_fields", "license_type_id and year are required")
		return
	}
	issuedDate, err := time.Parse("2006-01-02", body.IssuedDate)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "issued_date must be YYYY-MM-DD")
		return
	}

	l, err := h.service.Create(r.Context(), CreateInput{
		UserID: userID, LicenseTypeID: body.LicenseTypeID, OtherDetail: body.OtherDetail,
		IssuedDate: issuedDate, Year: body.Year, TransactionID: body.TransactionID,
	})
	if errors.Is(err, ErrNotActiveMember) {
		httpapi.WriteError(w, http.StatusConflict, "not_active_member", err.Error())
		return
	}
	if errors.Is(err, ErrTypeNotFound) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_license_type", err.Error())
		return
	}
	if errors.Is(err, ErrOtherDetailRequired) || errors.Is(err, ErrOtherDetailNotAllowed) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_other_detail", err.Error())
		return
	}
	if errors.Is(err, ErrUnknownTransaction) {
		httpapi.WriteError(w, http.StatusBadRequest, "unknown_transaction", err.Error())
		return
	}
	if errors.Is(err, ErrTransactionNotForLicenses) {
		httpapi.WriteError(w, http.StatusConflict, "transaction_not_for_licenses", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not create license")
		return
	}
	l.UserLogin = login
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🆕 %s ha asignado la licencia «%s» a %s (año %d, emitida el %s).",
		h.actorLogin(r.Context()), l.Type, login, l.Year, l.IssuedDate.Format("2006-01-02")))
	httpapi.WriteJSON(w, http.StatusCreated, l)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	var body struct {
		LicenseTypeID int64   `json:"license_type_id"`
		OtherDetail   *string `json:"other_detail"`
		IssuedDate    string  `json:"issued_date"`
		Year          int     `json:"year"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if body.LicenseTypeID == 0 || body.Year == 0 {
		httpapi.WriteError(w, http.StatusBadRequest, "missing_fields", "license_type_id and year are both required")
		return
	}
	issuedDate, err := time.Parse("2006-01-02", body.IssuedDate)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "issued_date must be YYYY-MM-DD")
		return
	}

	// Fetched before applying anything, purely to describe exactly what
	// changed in the mutation notification below (see auditableFields)
	// — a failure here isn't reason enough to fail the actual edit, so
	// it's only ever consulted if it succeeded.
	before, beforeErr := h.service.Get(r.Context(), id)

	l, err := h.service.Update(r.Context(), id, UpdateInput{
		LicenseTypeID: body.LicenseTypeID, OtherDetail: body.OtherDetail, IssuedDate: issuedDate, Year: body.Year,
	})
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "license not found")
		return
	}
	if errors.Is(err, ErrTypeNotFound) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_license_type", err.Error())
		return
	}
	if errors.Is(err, ErrOtherDetailRequired) || errors.Is(err, ErrOtherDetailNotAllowed) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_other_detail", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not update license")
		return
	}
	login, err := h.users.LoginForUserID(r.Context(), l.UserID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not resolve license owner")
		return
	}
	l.UserLogin = login
	if beforeErr == nil {
		if diff := httpapi.DiffFields(before, l, auditableFields...); len(diff) > 0 {
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha editado la licencia de %s:\n%s",
				h.actorLogin(r.Context()), login, strings.Join(diff, "\n")))
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, l)
}

// auditableFields are the License fields update's own mutation
// notification diffs (see httpapi.DiffFields). Type stands in for
// UpdateInput's own LicenseTypeID/OtherDetail pair: Type is already the
// human-readable label computed from both ("<catalog name>" normally,
// "<catalog name>: <other_detail>" for the "Otra" escape hatch), so
// diffing it instead of the raw numeric LicenseTypeID keeps the
// notification readable, and still shows an OtherDetail-only change
// (a corrected "Otra" detail with the same LicenseTypeID) since that
// changes Type's own text too.
var auditableFields = []string{"Type", "IssuedDate", "Year"}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	// Fetched before deleting, purely so the mutation notification below
	// can still name the license and its owner afterward — there's
	// nothing left to look up once Delete succeeds.
	target, targetErr := h.service.Get(r.Context(), id)

	err = h.service.Delete(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "license not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not delete license")
		return
	}
	if targetErr == nil {
		if login, loginErr := h.users.LoginForUserID(r.Context(), target.UserID); loginErr == nil {
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha borrado la licencia «%s» de %s.",
				h.actorLogin(r.Context()), target.Type, login))
		}
	}
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}
