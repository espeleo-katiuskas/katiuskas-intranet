package transactionfiles

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
)

// UserLookup resolves the "who did this" of a notification.
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

// RegisterRoutes: admin, board and money read; admin and money attach and remove. The file itself
// also accepts the session cookie, so that it can be opened in a browser tab.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/transaction-files", h.authenticator.Protect(h.listAll, "admin", "board", "money"))
	mux.Handle("GET /api/v1/transactions/{id}/files", h.authenticator.Protect(h.list, "admin", "board", "money"))
	mux.Handle("POST /api/v1/transactions/{id}/files", h.authenticator.Protect(h.create, "admin", "money"))
	mux.Handle("GET /api/v1/transactions/{id}/files/{fileId}/file", h.authenticator.ProtectAllowingCookie(h.file, "admin", "board", "money"))
	mux.Handle("DELETE /api/v1/transactions/{id}/files/{fileId}", h.authenticator.Protect(h.delete, "admin", "money"))
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
	switch {
	case errors.Is(err, ErrNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, ErrTransactionNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "transaction_not_found", err.Error())
	case errors.Is(err, ErrEmptyFile):
		httpapi.WriteError(w, http.StatusBadRequest, "file_required", err.Error())
	default:
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", fallback)
	}
}

func parseIDs(w http.ResponseWriter, r *http.Request, withFile bool) (transactionID, fileID int64, ok bool) {
	transactionID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err == nil && withFile {
		fileID, err = strconv.ParseInt(r.PathValue("fileId"), 10, 64)
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return 0, 0, false
	}
	return transactionID, fileID, true
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	transactionID, _, ok := parseIDs(w, r, false)
	if !ok {
		return
	}
	files, err := h.service.ListByTransaction(r.Context(), transactionID)
	if err != nil {
		writeError(w, err, "could not list the files")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, files)
}

func (h *Handler) listAll(w http.ResponseWriter, r *http.Request) {
	filter := ListFilter{Limit: 50}
	q := r.URL.Query()
	if v := q.Get("year"); v != "" {
		year, err := strconv.Atoi(v)
		if err != nil || year < 1900 || year > 2200 {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_year", "year must be between 1900 and 2200")
			return
		}
		filter.Year = &year
	}
	if v := q.Get("limit"); v != "" {
		limit, err := strconv.Atoi(v)
		if err != nil || limit < 1 || limit > 200 {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 200")
			return
		}
		filter.Limit = limit
	}
	if v := q.Get("offset"); v != "" {
		offset, err := strconv.Atoi(v)
		if err != nil || offset < 0 {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_offset", "offset must be zero or more")
			return
		}
		filter.Offset = offset
	}
	list, err := h.service.List(r.Context(), filter)
	if err != nil {
		writeError(w, err, "could not list the files")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	transactionID, _, ok := parseIDs(w, r, false)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxFileSize+1<<20)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpapi.WriteError(w, http.StatusRequestEntityTooLarge, "file_too_large", fmt.Sprintf("file exceeds the %d MiB limit", MaxFileSize>>20))
			return
		}
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed multipart form")
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("file")
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "file_required", "a file is required")
		return
	}
	defer file.Close()
	if header.Size > MaxFileSize {
		httpapi.WriteError(w, http.StatusRequestEntityTooLarge, "file_too_large", fmt.Sprintf("file exceeds the %d MiB limit", MaxFileSize>>20))
		return
	}
	content, err := io.ReadAll(io.LimitReader(file, MaxFileSize+1))
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "could not read the file")
		return
	}
	if len(content) > MaxFileSize {
		httpapi.WriteError(w, http.StatusRequestEntityTooLarge, "file_too_large", fmt.Sprintf("file exceeds the %d MiB limit", MaxFileSize>>20))
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	f, description, err := h.service.Add(r.Context(), transactionID, current.ID, header.Filename, content)
	if err != nil {
		writeError(w, err, "could not attach the file")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("📎 %s ha adjuntado «%s» a la transacción «%s».",
		h.actorLogin(r.Context()), f.FileName, description))
	httpapi.WriteJSON(w, http.StatusCreated, f)
}

func (h *Handler) file(w http.ResponseWriter, r *http.Request) {
	transactionID, fileID, ok := parseIDs(w, r, true)
	if !ok {
		return
	}
	f, file, err := h.service.Open(r.Context(), transactionID, fileID)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			err = ErrNotFound
		}
		writeError(w, err, "could not open the file")
		return
	}
	defer file.Close()
	disposition := "attachment"
	if Viewable(f.ContentType) && r.URL.Query().Get("download") != "1" {
		disposition = "inline"
	}
	w.Header().Set("Content-Type", f.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"`, disposition, strings.ReplaceAll(f.FileName, `"`, "'")))
	// Whatever the file is, it must never run as part of the intranet. A PDF or an image was
	// recognised by its bytes and is shown by the browser as such (a sandbox policy would stop
	// Chrome's PDF viewer); anything else is only ever downloaded, and is sandboxed as well.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if disposition == "attachment" {
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	}
	modTime := f.CreatedAt
	if info, err := file.Stat(); err == nil {
		modTime = info.ModTime()
	}
	http.ServeContent(w, r, f.FileName, modTime, file)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	transactionID, fileID, ok := parseIDs(w, r, true)
	if !ok {
		return
	}
	f, description, err := h.service.Remove(r.Context(), transactionID, fileID)
	if err != nil {
		writeError(w, err, "could not delete the file")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha quitado «%s» de la transacción «%s».",
		h.actorLogin(r.Context()), f.FileName, description))
	w.WriteHeader(http.StatusNoContent)
}
