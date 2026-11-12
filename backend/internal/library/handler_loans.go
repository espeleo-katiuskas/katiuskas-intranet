package library

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
)

// listLoans is the loans of every item: those still out by default, all with ?status=all.
func (h *Handler) listLoans(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status != "" && status != "open" && status != "all" {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_status", "status must be open or all")
		return
	}
	list, err := h.service.ListLoans(r.Context(), status != "all")
	if err != nil {
		writeError(w, err, "could not list the loans")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) listItemLoans(w http.ResponseWriter, r *http.Request) {
	itemID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	list, err := h.service.ListLoansOfItem(r.Context(), itemID)
	if err != nil {
		writeError(w, err, "could not list the loans")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) lend(w http.ResponseWriter, r *http.Request) {
	itemID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		Login  string  `json:"login"`
		LentAt *string `json:"lent_at"`
		Notes  *string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if body.Login == "" {
		httpapi.WriteError(w, http.StatusBadRequest, "loan_login_required", "a loan needs the login of the member it is for")
		return
	}
	userID, err := h.users.UserIDForLogin(r.Context(), body.Login)
	if err != nil {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	loan, err := h.service.Lend(r.Context(), itemID, userID, current.ID, body.LentAt, body.Notes)
	if err != nil {
		writeError(w, err, "could not record the loan")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("📚 %s ha apuntado el préstamo del ejemplar «%s» a %s.",
		loan.RecordedByLogin, loan.ItemTitle, loan.UserLogin))
	httpapi.WriteJSON(w, http.StatusCreated, loan)
}

func (h *Handler) returnLoan(w http.ResponseWriter, r *http.Request) {
	itemID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	loanID, ok := pathID(w, r, "loanId")
	if !ok {
		return
	}
	// The body is optional: a return with none is a return today.
	var body struct {
		ReturnedAt *string `json:"returned_at"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
			return
		}
	}
	loan, err := h.service.Return(r.Context(), itemID, loanID, body.ReturnedAt)
	if err != nil {
		writeError(w, err, "could not record the return")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("📚 %s ha apuntado la devolución del ejemplar «%s» (de %s).",
		h.actorLogin(r.Context()), loan.ItemTitle, loan.UserLogin))
	httpapi.WriteJSON(w, http.StatusOK, loan)
}

func (h *Handler) deleteLoan(w http.ResponseWriter, r *http.Request) {
	itemID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	loanID, ok := pathID(w, r, "loanId")
	if !ok {
		return
	}
	// Read before deleting, so the notice can still name it.
	loan, getErr := h.service.GetLoan(r.Context(), itemID, loanID)
	if err := h.service.DeleteLoan(r.Context(), itemID, loanID); err != nil {
		writeError(w, err, "could not delete the loan")
		return
	}
	if getErr == nil {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha borrado un préstamo del ejemplar «%s» (a %s).",
			h.actorLogin(r.Context()), loan.ItemTitle, loan.UserLogin))
	}
	w.WriteHeader(http.StatusNoContent)
}
