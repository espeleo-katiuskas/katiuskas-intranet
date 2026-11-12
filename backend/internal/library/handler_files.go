package library

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strings"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
)

// readUpload reads the file of a multipart form (field "file", at most MaxFileSize), answering the
// error itself if there is none or it is too large. title is the optional text field of that name.
func readUpload(w http.ResponseWriter, r *http.Request) (name string, content []byte, title *string, ok bool) {
	tooLarge := func() {
		httpapi.WriteError(w, http.StatusRequestEntityTooLarge, "file_too_large",
			fmt.Sprintf("file exceeds the %d MiB limit", MaxFileSize>>20))
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxFileSize+1<<20)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		var big *http.MaxBytesError
		if errors.As(err, &big) {
			tooLarge()
			return "", nil, nil, false
		}
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed multipart form")
		return "", nil, nil, false
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("file")
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "file_required", "a file is required")
		return "", nil, nil, false
	}
	defer file.Close()
	if header.Size > MaxFileSize {
		tooLarge()
		return "", nil, nil, false
	}
	content, err = io.ReadAll(io.LimitReader(file, MaxFileSize+1))
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "could not read the file")
		return "", nil, nil, false
	}
	if len(content) > MaxFileSize {
		tooLarge()
		return "", nil, nil, false
	}
	if v := r.FormValue("title"); v != "" {
		title = &v
	}
	return header.Filename, content, title, true
}

// serveFile answers with a stored file. Whatever it is, it must never run as part of the intranet:
// a PDF or an image was recognised by its bytes and is shown by the browser as such (a sandbox
// policy would stop Chrome's PDF viewer); anything else is only ever downloaded, and sandboxed too.
func serveFile(w http.ResponseWriter, r *http.Request, f Resource, file *os.File) {
	disposition := "attachment"
	if Viewable(f.ContentType) && r.URL.Query().Get("download") != "1" {
		disposition = "inline"
	}
	w.Header().Set("Content-Type", f.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"`, disposition, strings.ReplaceAll(f.FileName, `"`, "'")))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if disposition == "attachment" {
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	}
	// A cover is replaced under the same address: the browser asks every time, and gets a 304 when
	// nothing changed.
	w.Header().Set("Cache-Control", "private, no-cache")
	modTime := f.CreatedAt
	if info, err := file.Stat(); err == nil {
		modTime = info.ModTime()
	}
	http.ServeContent(w, r, f.FileName, modTime, file)
}

func (h *Handler) listResources(w http.ResponseWriter, r *http.Request) {
	itemID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	list, err := h.service.ListResources(r.Context(), itemID)
	if err != nil {
		writeError(w, err, "could not list the files")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) addResource(w http.ResponseWriter, r *http.Request) {
	itemID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	name, content, title, ok := readUpload(w, r)
	if !ok {
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	f, err := h.service.AddResource(r.Context(), itemID, current.ID, title, name, content)
	if err != nil {
		writeError(w, err, "could not attach the file")
		return
	}
	if b, err := h.service.Get(r.Context(), itemID); err == nil {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("📎 %s ha adjuntado «%s» al ejemplar «%s».", f.UploadedBy, f.FileName, b.Title))
	}
	httpapi.WriteJSON(w, http.StatusCreated, f)
}

func (h *Handler) resourceFile(w http.ResponseWriter, r *http.Request) {
	itemID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	resourceID, ok := pathID(w, r, "resourceId")
	if !ok {
		return
	}
	f, file, err := h.service.OpenResource(r.Context(), itemID, resourceID)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			err = ErrResourceNotFound
		}
		writeError(w, err, "could not open the file")
		return
	}
	defer file.Close()
	serveFile(w, r, f, file)
}

func (h *Handler) deleteResource(w http.ResponseWriter, r *http.Request) {
	itemID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	resourceID, ok := pathID(w, r, "resourceId")
	if !ok {
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	f, err := h.service.RemoveResource(r.Context(), itemID, resourceID, current.ID, manages(r.Context()))
	if err != nil {
		writeError(w, err, "could not delete the file")
		return
	}
	if b, err := h.service.Get(r.Context(), itemID); err == nil {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha quitado «%s» del ejemplar «%s».", h.actorLogin(r.Context()), f.FileName, b.Title))
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) cover(w http.ResponseWriter, r *http.Request) {
	itemID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	f, file, err := h.service.OpenCover(r.Context(), itemID)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			err = ErrCoverNotFound
		}
		writeError(w, err, "could not open the cover")
		return
	}
	defer file.Close()
	serveFile(w, r, f, file)
}

func (h *Handler) setCover(w http.ResponseWriter, r *http.Request) {
	itemID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	name, content, _, ok := readUpload(w, r)
	if !ok {
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	f, err := h.service.SetCover(r.Context(), itemID, current.ID, name, content)
	if err != nil {
		writeError(w, err, "could not set the cover")
		return
	}
	if b, err := h.service.Get(r.Context(), itemID); err == nil {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🖼️ %s ha cambiado la portada del ejemplar «%s».", f.UploadedBy, b.Title))
	}
	httpapi.WriteJSON(w, http.StatusOK, f)
}

func (h *Handler) deleteCover(w http.ResponseWriter, r *http.Request) {
	itemID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := h.service.RemoveCover(r.Context(), itemID); err != nil {
		writeError(w, err, "could not remove the cover")
		return
	}
	if b, err := h.service.Get(r.Context(), itemID); err == nil {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha quitado la portada del ejemplar «%s».", h.actorLogin(r.Context()), b.Title))
	}
	w.WriteHeader(http.StatusNoContent)
}
