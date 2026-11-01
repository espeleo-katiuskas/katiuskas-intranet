package budgets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
)

// UserLookup is the one thing the handler needs from the users package, for the "who did this" of
// a notification.
type UserLookup interface {
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

// RegisterRoutes: anybody who can sign in reads budgets; only admin and money change them.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/budgets", h.authenticator.Protect(h.list))
	mux.Handle("GET /api/v1/budgets/{year}", h.authenticator.Protect(h.get))
	mux.Handle("PUT /api/v1/budgets/{year}", h.authenticator.Protect(h.save, "admin", "money"))
	mux.Handle("DELETE /api/v1/budgets/{year}", h.authenticator.Protect(h.delete, "admin", "money"))
}

func (h *Handler) actorLogin(ctx context.Context) string {
	current, _ := auth.UserFromContext(ctx)
	login, err := h.users.LoginForUserID(ctx, current.ID)
	if err != nil {
		return fmt.Sprintf("user#%d", current.ID)
	}
	return login
}

func writeError(w http.ResponseWriter, err error, fallback string) {
	status, code := http.StatusInternalServerError, "internal_error"
	switch {
	case errors.Is(err, ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, ErrInvalidYear):
		status, code = http.StatusBadRequest, "invalid_year"
	case errors.Is(err, ErrInvalidItem):
		status, code = http.StatusBadRequest, "invalid_item"
	case errors.Is(err, ErrDuplicateItem):
		status, code = http.StatusBadRequest, "duplicate_item"
	case errors.Is(err, ErrItemWithoutAccount):
		status, code = http.StatusBadRequest, "item_without_accounts"
	case errors.Is(err, ErrUnknownAccount):
		status, code = http.StatusBadRequest, "unknown_account"
	case errors.Is(err, ErrNotBudgetable):
		status, code = http.StatusBadRequest, "account_not_budgetable"
	case errors.Is(err, ErrTypeMismatch):
		status, code = http.StatusBadRequest, "account_type_mismatch"
	case errors.Is(err, ErrDuplicateAccount):
		status, code = http.StatusBadRequest, "duplicate_account"
	case errors.Is(err, ErrAccountsMissing):
		status, code = http.StatusConflict, "accounts_missing"
	case errors.Is(err, ErrUnbalanced):
		status, code = http.StatusConflict, "unbalanced"
	default:
		httpapi.WriteError(w, status, code, fallback)
		return
	}
	httpapi.WriteError(w, status, code, err.Error())
}

func parseYear(w http.ResponseWriter, r *http.Request) (int, bool) {
	year, err := strconv.Atoi(r.PathValue("year"))
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_year", "year must be numeric")
		return 0, false
	}
	return year, true
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	out, err := h.service.List(r.Context())
	if err != nil {
		writeError(w, err, "could not list the budgets")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	year, ok := parseYear(w, r)
	if !ok {
		return
	}
	b, err := h.service.Get(r.Context(), year)
	if err != nil {
		writeError(w, err, "could not fetch the budget")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, b)
}

type itemBody struct {
	Name       string  `json:"name"`
	Amount     float64 `json:"amount"`
	AccountIDs []int64 `json:"account_ids"`
}

type saveBody struct {
	Income   []itemBody `json:"income"`
	Expenses []itemBody `json:"expenses"`
}

// toCents turns an amount into cents, refusing more than two decimals, negatives and absurd sizes.
func toCents(amount float64) (int64, bool) {
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 || amount > 1e11 {
		return 0, false
	}
	cents := math.Round(amount * 100)
	if math.Abs(amount*100-cents) > 1e-6 {
		return 0, false
	}
	return int64(cents), true
}

func (b saveBody) input() (Input, error) {
	convert := func(items []itemBody) ([]ItemInput, error) {
		out := make([]ItemInput, 0, len(items))
		for _, it := range items {
			cents, ok := toCents(it.Amount)
			if !ok {
				return nil, ErrInvalidItem
			}
			out = append(out, ItemInput{Name: strings.TrimSpace(it.Name), Cents: cents, AccountIDs: it.AccountIDs})
		}
		return out, nil
	}
	income, err := convert(b.Income)
	if err != nil {
		return Input{}, err
	}
	expenses, err := convert(b.Expenses)
	if err != nil {
		return Input{}, err
	}
	return Input{Income: income, Expenses: expenses}, nil
}

func (h *Handler) save(w http.ResponseWriter, r *http.Request) {
	year, ok := parseYear(w, r)
	if !ok {
		return
	}
	var body saveBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	in, err := body.input()
	if err != nil {
		writeError(w, err, "")
		return
	}
	before, beforeErr := h.service.Get(r.Context(), year)
	if beforeErr != nil && !errors.Is(beforeErr, ErrNotFound) {
		writeError(w, beforeErr, "could not fetch the budget")
		return
	}
	after, err := h.service.Save(r.Context(), year, in)
	if err != nil {
		writeError(w, err, "could not save the budget")
		return
	}
	actor := h.actorLogin(r.Context())
	if beforeErr != nil {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🆕 %s ha creado el presupuesto de %d: ingresos y gastos de %s €.",
			actor, year, euros(after.Income.Amount)))
	} else if changes := describeChanges(before, after); len(changes) > 0 {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha cambiado el presupuesto de %d:\n%s",
			actor, year, strings.Join(changes, "\n")))
	}
	httpapi.WriteJSON(w, http.StatusOK, after)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	year, ok := parseYear(w, r)
	if !ok {
		return
	}
	if err := h.service.Delete(r.Context(), year); err != nil {
		writeError(w, err, "could not delete the budget")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha borrado el presupuesto de %d.", h.actorLogin(r.Context()), year))
	w.WriteHeader(http.StatusNoContent)
}

func euros(v float64) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', 2, 64), ".", ",", 1)
}

func accountNames(accounts []AccountRef) string {
	names := make([]string, len(accounts))
	for i, a := range accounts {
		names[i] = a.Name
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// describeChanges lists, in Spanish like every notification, what differs between two versions of
// a budget: line items added or removed, amounts and accounts changed.
func describeChanges(before, after Budget) []string {
	var out []string
	for _, p := range []struct {
		label         string
		before, after []Item
	}{{"ingresos", before.Income.Items, after.Income.Items}, {"gastos", before.Expenses.Items, after.Expenses.Items}} {
		old := map[string]Item{}
		for _, it := range p.before {
			old[it.Name] = it
		}
		seen := map[string]bool{}
		for _, it := range p.after {
			seen[it.Name] = true
			prev, existed := old[it.Name]
			switch {
			case !existed:
				out = append(out, fmt.Sprintf("• Partida de %s añadida: «%s», %s € (%s)", p.label, it.Name, euros(it.Amount), accountNames(it.Accounts)))
			default:
				if prev.Amount != it.Amount {
					out = append(out, fmt.Sprintf("• «%s» (%s): %s € → %s €", it.Name, p.label, euros(prev.Amount), euros(it.Amount)))
				}
				if accountNames(prev.Accounts) != accountNames(it.Accounts) {
					out = append(out, fmt.Sprintf("• «%s» (%s): cuentas %s → %s", it.Name, p.label, accountNames(prev.Accounts), accountNames(it.Accounts)))
				}
			}
		}
		for _, it := range p.before {
			if !seen[it.Name] {
				out = append(out, fmt.Sprintf("• Partida de %s quitada: «%s», %s €", p.label, it.Name, euros(it.Amount)))
			}
		}
	}
	return out
}
