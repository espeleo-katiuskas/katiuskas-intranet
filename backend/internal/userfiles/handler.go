package userfiles

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
)

func init() {
	// Same rationale as internal/archive's identical init(): Go's mime
	// package falls back to a small built-in table when the host has no
	// /etc/mime.types (true of the alpine-based API image), and that
	// table doesn't include every extension a person's own documents
	// realistically use. Registered here too (not just relied on via
	// archive's own init()) so this package behaves the same whether or
	// not archive happens to also be imported into the same binary.
	for ext, typ := range map[string]string{
		".md":   "text/markdown; charset=utf-8",
		".txt":  "text/plain; charset=utf-8",
		".doc":  "application/msword",
		".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		".xls":  "application/vnd.ms-excel",
		".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		".odt":  "application/vnd.oasis.opendocument.text",
		".ods":  "application/vnd.oasis.opendocument.spreadsheet",
		".csv":  "text/csv; charset=utf-8",
	} {
		_ = mime.AddExtensionType(ext, typ)
	}
}

// UserLookup translates between a person's login and their numeric id.
// This package's own routes are nested under /users/{login}/files, but
// the filesystem tree is organized by login directly (see Service) —
// this is only needed to check the login actually exists (404 for an
// unknown one) and to compare against the caller's own id for the
// self-or-privileged check below. users.Service satisfies this
// structurally (see main.go's wiring), without userfiles having to
// import that package.
type UserLookup interface {
	UserIDForLogin(ctx context.Context, login string) (int64, error)
}

type Handler struct {
	service       *Service
	authenticator *auth.Authenticator
	users         UserLookup
}

func NewHandler(service *Service, authenticator *auth.Authenticator, users UserLookup) *Handler {
	return &Handler{service: service, authenticator: authenticator, users: users}
}

// Both routes require only baseline auth at Protect — the actual access
// check (this person themselves, or admin/board) happens inside each
// handler, the same self-or-privileged pattern
// memberships.Handler.listByUser and material.Handler.listMovementsByUser
// use, since who may see a specific person's files isn't a fixed role
// list the way, say, GET /archive is. /file also accepts the session
// cookie (ProtectAllowingCookie, see auth.RequireAuthFromHeaderOrCookie),
// not just the bearer header, so it works as a plain URL a browser can
// navigate to directly and still get the file's real name. Its own path
// is a URL path segment, not a ?path= query parameter — same
// {path...} wildcard shape as internal/archive's own /file route.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/users/{login}/files", h.authenticator.Protect(h.list))
	mux.Handle("GET /api/v1/users/{login}/files/file/{path...}", h.authenticator.ProtectAllowingCookie(h.file))
}

// resolveAccess looks up the numeric id behind a {login} path segment
// and checks the caller is allowed to see that person's files: the
// person themselves, or admin/board. A login that doesn't belong to
// anyone and a login that belongs to someone else entirely are reported
// the same way callers who shouldn't be probing for either shouldn't be
// able to tell apart: 404 not_found for the first, 403 forbidden for
// the second (only the second actually needs the distinction, since a
// real login is being refused rather than simply not found).
func (h *Handler) resolveAccess(w http.ResponseWriter, r *http.Request) (string, bool) {
	login := r.PathValue("login")
	userID, err := h.users.UserIDForLogin(r.Context(), login)
	if err != nil {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "user not found")
		return "", false
	}
	current, _ := auth.UserFromContext(r.Context())
	if !current.HasAnyRole("admin", "board") && current.ID != userID {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "members can only access their own files")
		return "", false
	}
	return login, true
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	login, ok := h.resolveAccess(w, r)
	if !ok {
		return
	}
	listing, err := h.service.ListDirectory(login, r.URL.Query().Get("path"))
	switch {
	case errors.Is(err, ErrNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "directory not found")
	case errors.Is(err, ErrInvalidPath):
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_path", err.Error())
	case errors.Is(err, ErrNotDirectory):
		httpapi.WriteError(w, http.StatusBadRequest, "not_a_directory", err.Error())
	case err != nil:
		slog.Error("list user files directory", "error", err)
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list directory")
	default:
		httpapi.WriteJSON(w, http.StatusOK, listing)
	}
}

// file streams a document's raw content. ?download=1 asks for it as an
// attachment (a Save dialog); anything else serves it inline, for the
// frontend to fetch and either render itself (Markdown) or hand to the
// browser to display (PDF, image, HTML).
func (h *Handler) file(w http.ResponseWriter, r *http.Request) {
	login, ok := h.resolveAccess(w, r)
	if !ok {
		return
	}
	relPath := r.PathValue("path")
	f, err := h.service.OpenFile(login, relPath)
	switch {
	case errors.Is(err, ErrNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "file not found")
		return
	case errors.Is(err, ErrInvalidPath):
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_path", err.Error())
		return
	case errors.Is(err, ErrIsDirectory):
		httpapi.WriteError(w, http.StatusBadRequest, "not_a_file", err.Error())
		return
	case err != nil:
		slog.Error("open user file", "error", err)
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not open file")
		return
	}
	defer f.Content.Close()

	contentType := mime.TypeByExtension(filepath.Ext(f.Name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	disposition := "inline"
	if r.URL.Query().Get("download") == "1" {
		disposition = "attachment"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"`, disposition, sanitizeFilename(f.Name)))
	http.ServeContent(w, r, f.Name, f.ModifiedAt, f.Content)
}

// sanitizeFilename keeps a filename out of the surrounding
// Content-Disposition quotes it's placed in — names come from our own
// directory listing, not user input, but a stray quote in a document's
// filename would otherwise produce a malformed header.
func sanitizeFilename(name string) string {
	return strings.ReplaceAll(name, `"`, "'")
}
