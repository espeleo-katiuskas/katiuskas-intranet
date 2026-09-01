package activityresources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/activities"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
)

// maxUploadSize caps a single file resource — generous for a photo or a
// scanned PDF, small enough that one upload can't fill the disk.
const maxUploadSize = 25 << 20 // 25 MiB

// UserLookup translates a numeric user id into the login it belongs to.
// This package's routes never take a login as input (a resource's
// author is always the caller's own session, never someone named in
// the request) — this is only needed to resolve Resource.AuthorLogin
// after Create's INSERT ... RETURNING, which can't JOIN against the row
// it just wrote (see Handler.create). users.Service satisfies this
// structurally (see main.go's wiring).
type UserLookup interface {
	LoginForUserID(ctx context.Context, id int64) (string, error)
}

// ActivityLookup is the subset of *activities.Service this package
// needs, to decide who may contribute to (or reorder) a given
// activity's memoria: its own organizer, or anyone who took part in
// it. Unlike UserLookup, this isn't here to route around an import
// cycle — activities doesn't need anything from this package, so
// *activities.Service could be depended on directly — it's just kept
// narrow so handler_test.go can stub it without a real activities.Service
// (and, in turn, without touching Postgres for those tests).
type ActivityLookup interface {
	Get(ctx context.Context, id int64) (activities.Activity, error)
	IsParticipant(ctx context.Context, activityID, userID int64) (bool, error)
}

type Handler struct {
	service       *Service
	authenticator *auth.Authenticator
	activities    ActivityLookup
	users         UserLookup
}

func NewHandler(service *Service, authenticator *auth.Authenticator, activitiesService ActivityLookup, users UserLookup) *Handler {
	return &Handler{service: service, authenticator: authenticator, activities: activitiesService, users: users}
}

// Every route requires only baseline auth — read access is open to any
// baseline role (admin/board/member), same as the rest of an activity's
// own data; who may write is narrower and checked inline per handler
// (requireContributor for create/reorder, author-or-admin/board for
// update/delete), since neither is a fixed role list the way, say,
// POST /holidays is.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/activities/{id}/resources", h.authenticator.Protect(h.list))
	mux.Handle("POST /api/v1/activities/{id}/resources", h.authenticator.Protect(h.create))
	mux.Handle("PUT /api/v1/activities/{id}/resources/order", h.authenticator.Protect(h.reorder))
	mux.Handle("GET /api/v1/activities/{id}/resources/{resourceID}", h.authenticator.Protect(h.get))
	mux.Handle("PATCH /api/v1/activities/{id}/resources/{resourceID}", h.authenticator.Protect(h.update))
	mux.Handle("DELETE /api/v1/activities/{id}/resources/{resourceID}", h.authenticator.Protect(h.delete))
	mux.Handle("GET /api/v1/activities/{id}/resources/{resourceID}/file", h.authenticator.Protect(h.file))
}

func (h *Handler) resolveActivityID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "activity id must be numeric")
		return 0, false
	}
	return id, true
}

// resolveResource looks up {resourceID} and checks it actually belongs
// to activityID — a resource id that exists but under a different
// activity's URL is reported the same as one that doesn't exist at
// all, rather than working anyway.
func (h *Handler) resolveResource(w http.ResponseWriter, r *http.Request, activityID int64) (Resource, bool) {
	resourceID, err := strconv.ParseInt(r.PathValue("resourceID"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "resource id must be numeric")
		return Resource{}, false
	}
	res, err := h.service.Get(r.Context(), resourceID)
	if errors.Is(err, ErrNotFound) || (err == nil && res.ActivityID != activityID) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "resource not found")
		return Resource{}, false
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not load resource")
		return Resource{}, false
	}
	return res, true
}

// requireContributor writes an error response and returns false unless
// the caller may contribute to activityID's memoria: admin/board, its
// organizer, or anyone who took part in it (activities.IsParticipant) —
// 404 if the activity itself doesn't exist, distinct from 403 for
// someone who just isn't allowed to touch it.
func (h *Handler) requireContributor(w http.ResponseWriter, r *http.Request, activityID int64) bool {
	activity, err := h.activities.Get(r.Context(), activityID)
	if errors.Is(err, activities.ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "activity not found")
		return false
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not load activity")
		return false
	}
	current, _ := auth.UserFromContext(r.Context())
	if current.HasAnyRole("admin", "board") || activity.LeaderID == current.ID {
		return true
	}
	isParticipant, err := h.activities.IsParticipant(r.Context(), activityID, current.ID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not check participation")
		return false
	}
	if !isParticipant {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "only someone who took part in this activity, or admin/board, can contribute to its memoria")
		return false
	}
	return true
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	activityID, ok := h.resolveActivityID(w, r)
	if !ok {
		return
	}
	list, err := h.service.ListByActivity(r.Context(), activityID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list resources")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	activityID, ok := h.resolveActivityID(w, r)
	if !ok {
		return
	}
	res, ok := h.resolveResource(w, r, activityID)
	if !ok {
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, res)
}

// create takes a multipart/form-data body (never plain JSON) since it
// has to accommodate an actual file upload for a file resource — a
// text resource just happens to be a multipart request with no file
// part in it, rather than a separate endpoint for each type. Fields:
// type (text|file), title, description (text only), file (file only).
func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	activityID, ok := h.resolveActivityID(w, r)
	if !ok {
		return
	}
	if !h.requireContributor(w, r, activityID) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			httpapi.WriteError(w, http.StatusRequestEntityTooLarge, "file_too_large", fmt.Sprintf("file exceeds the %d MiB limit", maxUploadSize>>20))
			return
		}
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed multipart form")
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		httpapi.WriteError(w, http.StatusBadRequest, "title_required", "title is required")
		return
	}

	current, _ := auth.UserFromContext(r.Context())
	var (
		res Resource
		err error
	)
	switch ResourceType(r.FormValue("type")) {
	case ResourceText:
		description := strings.TrimSpace(r.FormValue("description"))
		if description == "" {
			httpapi.WriteError(w, http.StatusBadRequest, "description_required", "a text resource requires a description")
			return
		}
		res, err = h.service.CreateText(r.Context(), activityID, current.ID, title, description)
	case ResourceFile:
		file, header, ferr := r.FormFile("file")
		if ferr != nil {
			httpapi.WriteError(w, http.StatusBadRequest, "file_required", "a file resource requires a file")
			return
		}
		defer file.Close()
		contentType := header.Header.Get("Content-Type")
		if contentType == "" || contentType == "application/octet-stream" {
			if guessed := mime.TypeByExtension(filepath.Ext(header.Filename)); guessed != "" {
				contentType = guessed
			}
		}
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		res, err = h.service.CreateFile(r.Context(), activityID, current.ID, title, header.Filename, contentType, header.Size, file)
	default:
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_type", "type must be text or file")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not create resource")
		return
	}
	if login, lerr := h.users.LoginForUserID(r.Context(), current.ID); lerr == nil {
		res.AuthorLogin = login
	}
	httpapi.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	activityID, ok := h.resolveActivityID(w, r)
	if !ok {
		return
	}
	res, ok := h.resolveResource(w, r, activityID)
	if !ok {
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	if !current.HasAnyRole("admin", "board") && current.ID != res.AuthorID {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "only this resource's own author, or admin/board, can edit it")
		return
	}
	var body struct {
		Title       *string `json:"title"`
		Description *string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	updated, err := h.service.Update(r.Context(), res.ID, UpdateInput{Title: body.Title, Description: body.Description})
	if errors.Is(err, ErrDescriptionNotAllowed) {
		httpapi.WriteError(w, http.StatusBadRequest, "description_not_allowed", err.Error())
		return
	}
	if errors.Is(err, ErrInvalidFields) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_fields", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not update resource")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, updated)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	activityID, ok := h.resolveActivityID(w, r)
	if !ok {
		return
	}
	res, ok := h.resolveResource(w, r, activityID)
	if !ok {
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	if !current.HasAnyRole("admin", "board") && current.ID != res.AuthorID {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "only this resource's own author, or admin/board, can delete it")
		return
	}
	if err := h.service.Delete(r.Context(), res.ID); err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not delete resource")
		return
	}
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

// file streams a file resource's raw content — not_a_file for a text
// resource, which never has one. ?download=1 asks for it as an
// attachment; anything else serves it inline, same as
// library.Handler.file/userfiles.Handler.file.
func (h *Handler) file(w http.ResponseWriter, r *http.Request) {
	activityID, ok := h.resolveActivityID(w, r)
	if !ok {
		return
	}
	res, ok := h.resolveResource(w, r, activityID)
	if !ok {
		return
	}
	_, f, err := h.service.OpenFile(r.Context(), res.ID)
	if errors.Is(err, ErrNotAFile) {
		httpapi.WriteError(w, http.StatusBadRequest, "not_a_file", err.Error())
		return
	}
	if err != nil {
		slog.Error("open activity resource file", "error", err)
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not open file")
		return
	}
	defer f.Close()

	contentType := "application/octet-stream"
	if res.ContentType != nil {
		contentType = *res.ContentType
	}
	fileName := "file"
	if res.FileName != nil {
		fileName = *res.FileName
	}
	disposition := "inline"
	if r.URL.Query().Get("download") == "1" {
		disposition = "attachment"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"`, disposition, sanitizeFilename(fileName)))
	modTime := res.CreatedAt
	if fi, err := f.Stat(); err == nil {
		modTime = fi.ModTime()
	}
	http.ServeContent(w, r, fileName, modTime, f)
}

// reorder replaces the whole memoria's order in one call — the caller
// sends every resource id in its new order, rather than a
// move-this-one endpoint that would have to shuffle the rest of them
// itself (see Repository.Reorder).
func (h *Handler) reorder(w http.ResponseWriter, r *http.Request) {
	activityID, ok := h.resolveActivityID(w, r)
	if !ok {
		return
	}
	if !h.requireContributor(w, r, activityID) {
		return
	}
	var body struct {
		ResourceIDs []int64 `json:"resource_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if err := h.service.Reorder(r.Context(), activityID, body.ResourceIDs); err != nil {
		if errors.Is(err, ErrReorderMismatch) {
			httpapi.WriteError(w, http.StatusConflict, "reorder_mismatch", err.Error())
			return
		}
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not reorder resources")
		return
	}
	list, err := h.service.ListByActivity(r.Context(), activityID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list resources")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

// sanitizeFilename keeps a filename out of the surrounding
// Content-Disposition quotes it's placed in — same defensive move as
// library.sanitizeFilename/userfiles.sanitizeFilename, cheap insurance
// against a stray quote in an uploaded file's original name.
func sanitizeFilename(name string) string {
	return strings.ReplaceAll(name, `"`, "'")
}
