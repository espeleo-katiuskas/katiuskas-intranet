package accounting

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
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
)

// UserLookup resolves the calling session's own login, for the "who
// did this" half of a mutation notification — the same narrow,
// locally-defined interface every other mutating package uses (see
// board.UserLookup), even though accounting itself keys nothing by
// user id or login the way board/licenses/memberships do.
type UserLookup interface {
	LoginForUserID(ctx context.Context, id int64) (string, error)
}

// DuesLookup and LicensesLookup say which cuotas and licencias a
// transaction paid for, for GET /transactions/{id} to include — answered
// by *memberships.Service and *licenses.Service structurally, the same
// narrow-dependency pattern as UserLookup, so this package doesn't take
// either whole service.
type DuesLookup interface {
	ListDuesForTransaction(ctx context.Context, transactionID int64) ([]memberships.Due, error)
}

type LicensesLookup interface {
	ListByTransaction(ctx context.Context, transactionID int64) ([]licenses.License, error)
}

type Handler struct {
	service       *Service
	authenticator *auth.Authenticator
	users         UserLookup
	dues          DuesLookup
	licenses      LicensesLookup
	notifier      httpapi.MutationNotifier
}

func NewHandler(service *Service, authenticator *auth.Authenticator, users UserLookup, dues DuesLookup, licenses LicensesLookup, notifier httpapi.MutationNotifier) *Handler {
	return &Handler{service: service, authenticator: authenticator, users: users, dues: dues, licenses: licenses, notifier: notifier}
}

// actorLogin resolves the calling session's own login, for the "who
// did this" half of a mutation notification — see board.Handler's own
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

// Reading is open to every baseline role (admin/board/member, see
// auth.BaselineRoles) — the club's own money is something every socio
// can see, not just admin/board. Writing (creating or correcting an
// account or a transaction) is admin-only plus the optional "money"
// role (seeded by migration 00012_accounting.sql, not a system role —
// a club that doesn't want this distinction can delete it and keep
// using admin instead), the same "admin plus one optional custom role"
// shape inventory.Handler already uses via "material" for its own
// movements.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/accounts", h.authenticator.Protect(h.listAccounts))
	mux.Handle("POST /api/v1/accounts", h.authenticator.Protect(h.createAccount, "admin", "money"))
	mux.Handle("GET /api/v1/accounts/{id}", h.authenticator.Protect(h.getAccount))
	mux.Handle("GET /api/v1/accounts/{id}/transactions", h.authenticator.Protect(h.listAccountEntries))
	mux.Handle("PATCH /api/v1/accounts/{id}", h.authenticator.Protect(h.updateAccount, "admin", "money"))
	mux.Handle("DELETE /api/v1/accounts/{id}", h.authenticator.Protect(h.deleteAccount, "admin", "money"))
	mux.Handle("POST /api/v1/accounts/{id}/move", h.authenticator.Protect(h.moveAccount, "admin", "money"))

	mux.Handle("GET /api/v1/transactions", h.authenticator.Protect(h.listTransactions))
	mux.Handle("POST /api/v1/transactions", h.authenticator.Protect(h.createTransaction, "admin", "money"))
	mux.Handle("GET /api/v1/transactions/{id}", h.authenticator.Protect(h.getTransaction))
	mux.Handle("PATCH /api/v1/transactions/{id}", h.authenticator.Protect(h.updateTransaction, "admin", "money"))
	mux.Handle("DELETE /api/v1/transactions/{id}", h.authenticator.Protect(h.deleteTransaction, "admin", "money"))
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil
}

// ---- Accounts ----

func (h *Handler) listAccounts(w http.ResponseWriter, r *http.Request) {
	list, err := h.service.ListAccounts(r.Context())
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list accounts")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) getAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	a, err := h.service.GetAccount(r.Context(), id)
	if errors.Is(err, ErrAccountNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "account not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not get account")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, a)
}

// listAccountEntries is this one account's own ledger — see
// AccountEntry's own doc comment. Checks the account actually exists
// first (GetAccount, discarded otherwise) purely to 404 rather than
// silently answer an empty list the same way a real, entry-less
// account would — ListAccountEntries itself can't tell those two cases
// apart on its own.
func (h *Handler) listAccountEntries(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	if _, err := h.service.GetAccount(r.Context(), id); errors.Is(err, ErrAccountNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "account not found")
		return
	} else if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not get account")
		return
	}
	list, err := h.service.ListAccountEntries(r.Context(), id)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list account entries")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func decodeAccountBody(r *http.Request) (CreateAccountInput, error) {
	var body struct {
		Name           string  `json:"name"`
		Code           *string `json:"code"`
		ParentID       *int64  `json:"parent_id"`
		AllowsDues     bool    `json:"allows_dues"`
		AllowsLicenses bool    `json:"allows_licenses"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return CreateAccountInput{}, err
	}
	return CreateAccountInput{
		Name: body.Name, Code: normalizeCode(body.Code), ParentID: body.ParentID,
		AllowsDues: body.AllowsDues, AllowsLicenses: body.AllowsLicenses,
	}, nil
}

// normalizeCode trims whitespace and treats an empty result the same
// as an absent code (nil) — code is optional (see Account's own doc
// comment), so "" and omitted both mean "no code" rather than ""
// itself becoming a real, storable value: since accounts.code is still
// UNIQUE, a second account also sent as "" would otherwise collide
// with the first instead of the two simply both having no code.
func normalizeCode(code *string) *string {
	if code == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*code)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// accountLabel names an account for a mutation notification: its name,
// plus its code in parentheses when it has one — some accounts
// deliberately don't (see Account's own doc comment).
func accountLabel(a Account) string {
	if a.Code != nil {
		return fmt.Sprintf("%s (%s)", a.Name, *a.Code)
	}
	return a.Name
}

func (h *Handler) createAccount(w http.ResponseWriter, r *http.Request) {
	in, err := decodeAccountBody(r)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		httpapi.WriteError(w, http.StatusBadRequest, "missing_fields", "name is required")
		return
	}
	a, err := h.service.CreateAccount(r.Context(), in)
	if writeAccountError(w, err) {
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🆕 %s ha creado la cuenta «%s».",
		h.actorLogin(r.Context()), accountLabel(a)))
	httpapi.WriteJSON(w, http.StatusCreated, a)
}

func (h *Handler) updateAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	in, err := decodeAccountBody(r)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		httpapi.WriteError(w, http.StatusBadRequest, "missing_fields", "name is required")
		return
	}

	// Fetched before applying anything, purely to describe exactly
	// what changed in the mutation notification below (see
	// accountAuditableFields) — a failure here isn't reason enough to
	// fail the actual edit, so it's only ever consulted if it
	// succeeded. Same pattern board.Handler.update uses.
	before, beforeErr := h.service.GetAccount(r.Context(), id)

	a, err := h.service.UpdateAccount(r.Context(), id, UpdateAccountInput(in))
	if writeAccountError(w, err) {
		return
	}
	if beforeErr == nil {
		if diff := httpapi.DiffFields(before, a, accountAuditableFields...); len(diff) > 0 {
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha editado la cuenta «%s»:\n%s",
				h.actorLogin(r.Context()), accountLabel(a), strings.Join(diff, "\n")))
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, a)
}

func (h *Handler) deleteAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	a, err := h.service.GetAccount(r.Context(), id)
	if errors.Is(err, ErrAccountNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "account not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not get account")
		return
	}
	if err := h.service.DeleteAccount(r.Context(), id); writeAccountError(w, err) {
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha borrado la cuenta «%s».",
		h.actorLogin(r.Context()), accountLabel(a)))
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

// moveAccount swaps this account's display position with its
// immediately preceding ("up") or following ("down") sibling — see
// Repository.MoveAccount — then answers with the whole, freshly
// reordered GET /accounts tree, the same "return the full list after
// reordering it" shape activityresources.Handler's own memoria
// reorder endpoint uses, so a caller can just re-render from this
// response instead of following up with its own GET.
func (h *Handler) moveAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	var body struct {
		Direction MoveDirection `json:"direction"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	a, err := h.service.MoveAccount(r.Context(), id, body.Direction)
	if writeAccountError(w, err) {
		return
	}
	list, err := h.service.ListAccounts(r.Context())
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list accounts")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha reordenado la cuenta «%s».",
		h.actorLogin(r.Context()), accountLabel(a)))
	httpapi.WriteJSON(w, http.StatusOK, list)
}

// writeAccountError maps an accounting account error to its HTTP
// response and reports whether it wrote one at all (false for a nil
// err, meaning the caller should carry on).
func writeAccountError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, ErrAccountNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "account not found")
	case errors.Is(err, ErrUnknownAccount):
		httpapi.WriteError(w, http.StatusBadRequest, "unknown_parent_account", err.Error())
	case errors.Is(err, ErrDuplicateCode):
		httpapi.WriteError(w, http.StatusConflict, "duplicate_code", err.Error())
	case errors.Is(err, ErrAccountInUse):
		httpapi.WriteError(w, http.StatusConflict, "account_in_use", err.Error())
	case errors.Is(err, ErrCircularParent):
		httpapi.WriteError(w, http.StatusConflict, "circular_parent", err.Error())
	case errors.Is(err, ErrInvalidDirection):
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_direction", err.Error())
	default:
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not save account")
	}
	return true
}

// accountAuditableFields are the Account fields updateAccount's own
// mutation notification diffs (see httpapi.DiffFields) — matching
// UpdateAccountInput's field set exactly.
var accountAuditableFields = []string{"Name", "Code", "ParentID", "AllowsDues", "AllowsLicenses"}

// ---- Transactions ----

func (h *Handler) listTransactions(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	// A negative offset (a malformed or hand-edited query string) used to
	// reach Postgres's own OFFSET clause untouched, which rejects it
	// outright — a raw, unhandled 500 instead of just starting from the
	// beginning, the same way limit above is defaulted rather than
	// rejected for an out-of-range value.
	if offset < 0 {
		offset = 0
	}
	list, err := h.service.ListTransactions(r.Context(), limit, offset)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list transactions")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) getTransaction(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	t, err := h.service.GetTransaction(r.Context(), id)
	if errors.Is(err, ErrTransactionNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "transaction not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not get transaction")
		return
	}

	// Who paid which cuota or licencia is something only that person,
	// admin and board see (see GET /users/{login}/dues), while a
	// transaction itself is open to every baseline role — so the two
	// lists only go out to admin/board, always present for them (empty
	// when the transaction paid for none), and absent for anyone else.
	current, _ := auth.UserFromContext(r.Context())
	if !current.HasAnyRole("admin", "board") {
		httpapi.WriteJSON(w, http.StatusOK, t)
		return
	}
	dues, err := h.dues.ListDuesForTransaction(r.Context(), id)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list the transaction's dues")
		return
	}
	paidLicenses, err := h.licenses.ListByTransaction(r.Context(), id)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list the transaction's licenses")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, transactionWithPayments{Transaction: t, Dues: dues, Licenses: paidLicenses})
}

// transactionWithPayments is a Transaction plus what it paid for — only
// ever the shape of GET /transactions/{id} for admin/board, never what
// create/update/list answer with.
type transactionWithPayments struct {
	Transaction
	Dues     []memberships.Due  `json:"dues"`
	Licenses []licenses.License `json:"licenses"`
}

type entryBody struct {
	AccountID int64   `json:"account_id"`
	Amount    float64 `json:"amount"`
}

// transactionBody is shared by createTransaction/updateTransaction —
// occurred_at decodes straight into time.Time (encoding/json parses
// RFC 3339 natively), unlike the plain YYYY-MM-DD "date" fields
// elsewhere in this API (activities.start_date, licenses.issued_date,
// ...), which need a manual time.Parse since RFC 3339 isn't what those
// represent. occurred_at is a real timestamp, not just a date, so the
// default format is exactly what's wanted here.
type transactionBody struct {
	Description string      `json:"description"`
	OccurredAt  time.Time   `json:"occurred_at"`
	Entries     []entryBody `json:"entries"`
}

func decodeTransactionBody(r *http.Request) (transactionBody, error) {
	var body transactionBody
	err := json.NewDecoder(r.Body).Decode(&body)
	return body, err
}

func toEntryInputs(entries []entryBody) []EntryInput {
	out := make([]EntryInput, len(entries))
	for i, e := range entries {
		out[i] = EntryInput(e)
	}
	return out
}

// positiveEntriesTotal sums only the positive side of a transaction's
// entries — since every transaction's entries always sum to exactly
// zero (see Transaction's own doc comment), that's the actual amount of
// money the transaction moved, used in its own creation notification
// below rather than a meaningless "sums to zero" or an entry count.
func positiveEntriesTotal(entries []Entry) float64 {
	var total float64
	for _, e := range entries {
		if e.Amount > 0 {
			total += e.Amount
		}
	}
	return total
}

func (h *Handler) createTransaction(w http.ResponseWriter, r *http.Request) {
	in, err := decodeTransactionBody(r)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body, or occurred_at isn't RFC 3339")
		return
	}
	if strings.TrimSpace(in.Description) == "" {
		httpapi.WriteError(w, http.StatusBadRequest, "missing_fields", "description is required")
		return
	}
	t, err := h.service.CreateTransaction(r.Context(), CreateTransactionInput{
		Description: in.Description, OccurredAt: in.OccurredAt, Entries: toEntryInputs(in.Entries),
	})
	if writeTransactionError(w, err) {
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🆕 %s ha creado la transacción «%s» (%.2f€).",
		h.actorLogin(r.Context()), t.Description, positiveEntriesTotal(t.Entries)))
	httpapi.WriteJSON(w, http.StatusCreated, t)
}

func (h *Handler) updateTransaction(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	in, err := decodeTransactionBody(r)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body, or occurred_at isn't RFC 3339")
		return
	}
	if strings.TrimSpace(in.Description) == "" {
		httpapi.WriteError(w, http.StatusBadRequest, "missing_fields", "description is required")
		return
	}

	before, beforeErr := h.service.GetTransaction(r.Context(), id)

	t, err := h.service.UpdateTransaction(r.Context(), id, UpdateTransactionInput{
		Description: in.Description, OccurredAt: in.OccurredAt, Entries: toEntryInputs(in.Entries),
	})
	if writeTransactionError(w, err) {
		return
	}
	if beforeErr == nil {
		diff := httpapi.DiffFields(before, t, transactionAuditableFields...)
		if entriesDiffer(before.Entries, t.Entries) {
			diff = append(diff, fmt.Sprintf("Entries: %d apunte(s)", len(t.Entries)))
		}
		if len(diff) > 0 {
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha editado la transacción «%s»:\n%s",
				h.actorLogin(r.Context()), t.Description, strings.Join(diff, "\n")))
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, t)
}

func (h *Handler) deleteTransaction(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	t, err := h.service.GetTransaction(r.Context(), id)
	if errors.Is(err, ErrTransactionNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "transaction not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not get transaction")
		return
	}
	if err := h.service.DeleteTransaction(r.Context(), id); writeTransactionError(w, err) {
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha borrado la transacción «%s».",
		h.actorLogin(r.Context()), t.Description))
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

// writeTransactionError maps an accounting transaction error to its
// HTTP response and reports whether it wrote one at all (false for a
// nil err, meaning the caller should carry on).
func writeTransactionError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, ErrTransactionNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "transaction not found")
	case errors.Is(err, ErrTooFewEntries):
		httpapi.WriteError(w, http.StatusBadRequest, "too_few_entries", err.Error())
	case errors.Is(err, ErrInvalidAmount):
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_amount", err.Error())
	case errors.Is(err, ErrDuplicateAccount):
		httpapi.WriteError(w, http.StatusBadRequest, "duplicate_account", err.Error())
	case errors.Is(err, ErrUnknownAccount):
		httpapi.WriteError(w, http.StatusBadRequest, "unknown_account", err.Error())
	case errors.Is(err, ErrUnbalanced):
		httpapi.WriteError(w, http.StatusConflict, "unbalanced", err.Error())
	default:
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not save transaction")
	}
	return true
}

// transactionAuditableFields are the Transaction fields
// updateTransaction's own mutation notification diffs via
// httpapi.DiffFields — Entries isn't in this list (a slice diffs badly
// as one "before → after" line); entriesDiffer covers that separately.
var transactionAuditableFields = []string{"Description", "OccurredAt"}

// entriesDiffer reports whether two transactions' entries differ in
// count, account, or amount — order-sensitive, since both come back
// from the database ordered by id (see
// Repository.entriesForTransaction/insertEntries), so a genuine
// reorder-with-no-other-change would still (correctly) count as a
// difference.
func entriesDiffer(before, after []Entry) bool {
	if len(before) != len(after) {
		return true
	}
	for i := range before {
		if before[i].AccountID != after[i].AccountID || before[i].Amount != after[i].Amount {
			return true
		}
	}
	return false
}
