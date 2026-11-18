package places

import (
	"context"
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
func serveFile(w http.ResponseWriter, r *http.Request, f File, file *os.File) {
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
	// A file never changes under its address, but it may be removed: the browser asks every time and
	// gets a 304 when it is still there.
	w.Header().Set("Cache-Control", "private, no-cache")
	modTime := f.CreatedAt
	if info, err := file.Stat(); err == nil {
		modTime = info.ModTime()
	}
	http.ServeContent(w, r, f.FileName, modTime, file)
}

// ownerOf says which place or system a request of the files is about: the id of its path, made an owner
// by `owner` (placeOwner or systemOwner).
type ownerMaker func(int64) fileOwner

// ownerLabel says what the owner is called in a notice: "el lugar «Cueva»" or "el sistema «Cueva»".
func (h *Handler) ownerName(ctx context.Context, o fileOwner) (kind, label string, ok bool) {
	if o.isSystem() {
		sys, err := h.service.GetSystem(ctx, o.id)
		return "sistema", "«" + sys.Name + "»", err == nil
	}
	p, err := h.service.GetPlace(ctx, o.id)
	return "lugar", "«" + p.Name + "»" + inWhere(p.Where), err == nil
}

func (h *Handler) listFiles(owner ownerMaker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		list, err := h.service.ListFiles(r.Context(), owner(id))
		if err != nil {
			writeError(w, err, "could not list the files")
			return
		}
		httpapi.WriteJSON(w, http.StatusOK, list)
	}
}

func (h *Handler) addFile(owner ownerMaker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		name, content, title, ok := readUpload(w, r)
		if !ok {
			return
		}
		o := owner(id)
		f, err := h.service.AddFile(r.Context(), o, actorID(r.Context()), title, name, content)
		if err != nil {
			writeError(w, err, "could not attach the file")
			return
		}
		if kind, label, ok := h.ownerName(r.Context(), o); ok {
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("📎 %s ha adjuntado «%s» al %s %s.", f.UploadedBy, f.FileName, kind, label))
		}
		httpapi.WriteJSON(w, http.StatusCreated, f)
	}
}

func (h *Handler) file(owner ownerMaker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		fileID, ok := pathID(w, r, "fileId")
		if !ok {
			return
		}
		f, file, err := h.service.OpenFile(r.Context(), owner(id), fileID)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				err = ErrFileNotFound
			}
			writeError(w, err, "could not open the file")
			return
		}
		defer file.Close()
		serveFile(w, r, f, file)
	}
}

func (h *Handler) deleteFile(owner ownerMaker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		fileID, ok := pathID(w, r, "fileId")
		if !ok {
			return
		}
		o := owner(id)
		current, _ := auth.UserFromContext(r.Context())
		f, err := h.service.RemoveFile(r.Context(), o, fileID, current.ID, current.HasAnyRole("admin", "board"))
		if err != nil {
			writeError(w, err, "could not delete the file")
			return
		}
		if kind, label, ok := h.ownerName(r.Context(), o); ok {
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha quitado «%s» del %s %s.", h.actorLogin(r.Context()), f.FileName, kind, label))
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
