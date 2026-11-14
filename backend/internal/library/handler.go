package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
)

// UserLookup translates between a person's login and their numeric id: the API names people by login
// only, and the tables are keyed by id.
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

// The roles that manage the library. Board does whatever `biblioteca` does, so a route that names
// `biblioteca` always names board with it.
var (
	manageRoles = []string{"admin", "board", "biblioteca"}
	// loanRoles are who reads the loans and records them and their returns: material too, since it
	// already lends the club's things.
	loanRoles = []string{"admin", "board", "biblioteca", "material"}
)

// RegisterRoutes: everybody who can sign in reads the items (with whether each is available, not who
// has it) and adds comments and files; admin, board and `biblioteca` create and change items and
// covers, and remove what others added; admin, board, `biblioteca` and `material` read the loans and
// record them; deleting a loan is admin only. The files and the cover also accept the session cookie,
// so that a browser can show them (an <img>, a tab).
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	a := h.authenticator
	mux.Handle("GET /api/v1/library/types", a.Protect(h.listTypes))
	mux.Handle("POST /api/v1/library/types", a.Protect(h.createType, manageRoles...))
	mux.Handle("PUT /api/v1/library/types/{id}", a.Protect(h.renameType, manageRoles...))
	mux.Handle("DELETE /api/v1/library/types/{id}", a.Protect(h.deleteType, manageRoles...))

	mux.Handle("GET /api/v1/library/items", a.Protect(h.list))
	mux.Handle("POST /api/v1/library/items", a.Protect(h.create, manageRoles...))
	mux.Handle("GET /api/v1/library/items/{id}", a.Protect(h.get))
	mux.Handle("PUT /api/v1/library/items/{id}", a.Protect(h.update, manageRoles...))
	mux.Handle("DELETE /api/v1/library/items/{id}", a.Protect(h.delete, manageRoles...))

	mux.Handle("GET /api/v1/library/items/{id}/comments", a.Protect(h.listComments))
	mux.Handle("POST /api/v1/library/items/{id}/comments", a.Protect(h.addComment))
	mux.Handle("DELETE /api/v1/library/items/{id}/comments/{commentId}", a.Protect(h.deleteComment))

	mux.Handle("GET /api/v1/library/items/{id}/resources", a.Protect(h.listResources))
	mux.Handle("POST /api/v1/library/items/{id}/resources", a.Protect(h.addResource))
	mux.Handle("GET /api/v1/library/items/{id}/resources/{resourceId}/file", a.ProtectAllowingCookie(h.resourceFile))
	mux.Handle("DELETE /api/v1/library/items/{id}/resources/{resourceId}", a.Protect(h.deleteResource))
	mux.Handle("GET /api/v1/library/items/{id}/cover", a.ProtectAllowingCookie(h.cover))
	mux.Handle("PUT /api/v1/library/items/{id}/cover", a.Protect(h.setCover, manageRoles...))
	mux.Handle("DELETE /api/v1/library/items/{id}/cover", a.Protect(h.deleteCover, manageRoles...))

	mux.Handle("GET /api/v1/library/loans", a.Protect(h.listLoans, loanRoles...))
	mux.Handle("GET /api/v1/library/items/{id}/loans", a.Protect(h.listItemLoans, loanRoles...))
	mux.Handle("POST /api/v1/library/items/{id}/loans", a.Protect(h.lend, loanRoles...))
	mux.Handle("POST /api/v1/library/items/{id}/loans/{loanId}/return", a.Protect(h.returnLoan, loanRoles...))
	mux.Handle("DELETE /api/v1/library/items/{id}/loans/{loanId}", a.Protect(h.deleteLoan, "admin"))
}

func (h *Handler) actorLogin(ctx context.Context) string {
	current, _ := auth.UserFromContext(ctx)
	login, err := h.users.LoginForUserID(ctx, current.ID)
	if err != nil {
		return fmt.Sprintf("user#%d", current.ID)
	}
	return login
}

// manages says whether the caller is one of those who manage the library.
func manages(ctx context.Context) bool {
	current, _ := auth.UserFromContext(ctx)
	return current.HasAnyRole(manageRoles...)
}

func writeError(w http.ResponseWriter, err error, fallback string) {
	type mapping struct {
		err    error
		status int
		code   string
	}
	for _, m := range []mapping{
		{ErrNotFound, http.StatusNotFound, "not_found"},
		{ErrCommentNotFound, http.StatusNotFound, "not_found"},
		{ErrResourceNotFound, http.StatusNotFound, "not_found"},
		{ErrCoverNotFound, http.StatusNotFound, "not_found"},
		{ErrLoanNotFound, http.StatusNotFound, "not_found"},
		{ErrTypeNotFound, http.StatusNotFound, "not_found"},
		{ErrBlankTitle, http.StatusBadRequest, "title_required"},
		{ErrAcquiredRequired, http.StatusBadRequest, "acquired_on_required"},
		{ErrInvalidEndReason, http.StatusBadRequest, "invalid_end_reason"},
		{ErrEndIncomplete, http.StatusBadRequest, "end_incomplete"},
		{ErrInvalidItemDates, http.StatusBadRequest, "invalid_item_dates"},
		{ErrItemEnded, http.StatusConflict, "item_ended"},
		{ErrItemOnLoan, http.StatusConflict, "item_on_loan"},
		{ErrTypeRequired, http.StatusBadRequest, "type_required"},
		{ErrUnknownType, http.StatusBadRequest, "unknown_library_type"},
		{ErrBlankTypeName, http.StatusBadRequest, "type_name_required"},
		{ErrTypeNameTooLong, http.StatusBadRequest, "type_name_too_long"},
		{ErrDuplicateType, http.StatusConflict, "duplicate_library_type"},
		{ErrTypeInUse, http.StatusConflict, "library_type_in_use"},
		{ErrInvalidYear, http.StatusBadRequest, "invalid_item_year"},
		{ErrBlankComment, http.StatusBadRequest, "comment_required"},
		{ErrCommentTooLong, http.StatusBadRequest, "comment_too_long"},
		{ErrEmptyFile, http.StatusBadRequest, "file_required"},
		{ErrNotAnImage, http.StatusBadRequest, "cover_not_image"},
		{ErrTitleTooLong, http.StatusBadRequest, "file_title_too_long"},
		{ErrInvalidDate, http.StatusBadRequest, "invalid_date"},
		{ErrInvalidDates, http.StatusBadRequest, "invalid_dates"},
		{ErrNotYours, http.StatusForbidden, "not_yours"},
		{ErrItemHasLoans, http.StatusConflict, "item_has_loans"},
		{ErrNotAvailable, http.StatusConflict, "item_not_available"},
		{ErrAlreadyReturned, http.StatusConflict, "loan_already_returned"},
		{ErrRecipientNotMember, http.StatusConflict, "loan_recipient_not_member"},
	} {
		if errors.Is(err, m.err) {
			httpapi.WriteError(w, m.status, m.code, err.Error())
			return
		}
	}
	httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", fallback)
}

// pathID reads a numeric path value, answering 400 itself if it is not one.
func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return 0, false
	}
	return id, true
}

// itemBody is what creating or editing an item takes.
type itemBody struct {
	TypeID      int64   `json:"type_id"`
	Title       string  `json:"title"`
	Author      *string `json:"author"`
	Publisher   *string `json:"publisher"`
	Year        *int    `json:"year"`
	ISBN        *string `json:"isbn"`
	Location    *string `json:"location"`
	Description *string `json:"description"`
	AcquiredOn  string  `json:"acquired_on"`
	EndedOn     *string `json:"ended_on"`
	EndReason   *string `json:"end_reason"`
}

func (b itemBody) input() Input { return Input(b) }

// ----- types -----

func (h *Handler) listTypes(w http.ResponseWriter, r *http.Request) {
	list, err := h.service.ListTypes(r.Context())
	if err != nil {
		writeError(w, err, "could not list the types")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

type typeBody struct {
	Name string `json:"name"`
}

func (h *Handler) createType(w http.ResponseWriter, r *http.Request) {
	var body typeBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	t, err := h.service.CreateType(r.Context(), body.Name)
	if err != nil {
		writeError(w, err, "could not create the type")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🏷️ %s ha añadido el tipo «%s» a la biblioteca.", h.actorLogin(r.Context()), t.Name))
	httpapi.WriteJSON(w, http.StatusCreated, t)
}

func (h *Handler) renameType(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var body typeBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	before, beforeErr := h.service.GetType(r.Context(), id)
	t, err := h.service.RenameType(r.Context(), id, body.Name)
	if err != nil {
		writeError(w, err, "could not rename the type")
		return
	}
	if beforeErr == nil && before.Name != t.Name {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha cambiado el tipo «%s» de la biblioteca por «%s».",
			h.actorLogin(r.Context()), before.Name, t.Name))
	}
	httpapi.WriteJSON(w, http.StatusOK, t)
}

func (h *Handler) deleteType(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	t, getErr := h.service.GetType(r.Context(), id)
	if err := h.service.DeleteType(r.Context(), id); err != nil {
		writeError(w, err, "could not delete the type")
		return
	}
	if getErr == nil {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha borrado el tipo «%s» de la biblioteca.", h.actorLogin(r.Context()), t.Name))
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- items -----

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.service.List(r.Context())
	if err != nil {
		writeError(w, err, "could not list the items")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	b, err := h.service.Get(r.Context(), id)
	if err != nil {
		writeError(w, err, "could not read the item")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var body itemBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	b, err := h.service.Create(r.Context(), body.input())
	if err != nil {
		writeError(w, err, "could not create the item")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("📚 %s ha añadido el ejemplar «%s».", h.actorLogin(r.Context()), b.Title))
	httpapi.WriteJSON(w, http.StatusCreated, b)
}

// itemAuditFields are the Item fields an edit's notification diffs: those Input can change.
var itemAuditFields = []string{"Type", "Title", "Author", "Publisher", "Year", "ISBN", "Location", "Description",
	"AcquiredOn", "EndedOn", "EndReason"}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var body itemBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	before, beforeErr := h.service.Get(r.Context(), id)
	b, err := h.service.Update(r.Context(), id, body.input())
	if err != nil {
		writeError(w, err, "could not edit the item")
		return
	}
	if beforeErr == nil {
		if diff := httpapi.DiffFields(before, b, itemAuditFields...); len(diff) > 0 {
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha editado el ejemplar «%s»:\n%s",
				h.actorLogin(r.Context()), before.Title, strings.Join(diff, "\n")))
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	// Read before deleting, so the notice can still name it.
	b, getErr := h.service.Get(r.Context(), id)
	if err := h.service.Delete(r.Context(), id); err != nil {
		writeError(w, err, "could not delete the item")
		return
	}
	if getErr == nil {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha borrado el ejemplar «%s».", h.actorLogin(r.Context()), b.Title))
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- comments -----

func (h *Handler) listComments(w http.ResponseWriter, r *http.Request) {
	itemID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	list, err := h.service.ListComments(r.Context(), itemID)
	if err != nil {
		writeError(w, err, "could not list the comments")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) addComment(w http.ResponseWriter, r *http.Request) {
	itemID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	c, err := h.service.AddComment(r.Context(), itemID, current.ID, body.Body)
	if err != nil {
		writeError(w, err, "could not add the comment")
		return
	}
	if b, err := h.service.Get(r.Context(), itemID); err == nil {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("💬 %s ha comentado el ejemplar «%s».", c.UserLogin, b.Title))
	}
	httpapi.WriteJSON(w, http.StatusCreated, c)
}

func (h *Handler) deleteComment(w http.ResponseWriter, r *http.Request) {
	itemID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	commentID, ok := pathID(w, r, "commentId")
	if !ok {
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	c, err := h.service.DeleteComment(r.Context(), itemID, commentID, current.ID, manages(r.Context()))
	if err != nil {
		writeError(w, err, "could not delete the comment")
		return
	}
	if b, err := h.service.Get(r.Context(), itemID); err == nil {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha quitado un comentario de %s en el ejemplar «%s».",
			h.actorLogin(r.Context()), c.UserLogin, b.Title))
	}
	w.WriteHeader(http.StatusNoContent)
}
