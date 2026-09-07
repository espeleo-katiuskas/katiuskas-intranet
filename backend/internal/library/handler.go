package library

import (
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

type Handler struct {
	service       *Service
	authenticator *auth.Authenticator
}

func NewHandler(service *Service, authenticator *auth.Authenticator) *Handler {
	return &Handler{service: service, authenticator: authenticator}
}

// Both routes are baseline-only (admin/board/member, see
// auth.BaselineRoles) — no extra role required, since this library is
// meant for every socio, and there's nothing here to write. /file also
// accepts the session cookie (ProtectAllowingCookie), not just the
// bearer header — see auth.RequireAuthFromHeaderOrCookie — so it works
// as a plain URL a browser can navigate to directly (a new tab, "Save
// As") and still get the file's real name, not just via fetch(). Its
// own path is a URL path segment, not a ?path= query parameter — the
// {path...} wildcard (Go 1.22+ ServeMux) matches every remaining
// segment, slashes included, so a nested file's URL reads as a real
// path (/library/file/actas/2026-02-01.pdf) rather than
// /library/file?path=actas%2F2026-02-01.pdf.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/library", h.authenticator.Protect(h.list))
	mux.Handle("GET /api/v1/library/file/{path...}", h.authenticator.ProtectAllowingCookie(h.file))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	listing, err := h.service.ListDirectory(r.URL.Query().Get("path"))
	switch {
	case errors.Is(err, ErrNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "directory not found")
	case errors.Is(err, ErrInvalidPath):
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_path", err.Error())
	case errors.Is(err, ErrNotDirectory):
		httpapi.WriteError(w, http.StatusBadRequest, "not_a_directory", err.Error())
	case err != nil:
		slog.Error("list library directory", "error", err)
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
	relPath := r.PathValue("path")
	f, err := h.service.OpenFile(relPath)
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
		slog.Error("open library file", "error", err)
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
