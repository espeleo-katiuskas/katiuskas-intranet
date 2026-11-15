package places

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
)

// systemBody is what creating or editing a system takes.
type systemBody struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
}

func (h *Handler) listSystems(w http.ResponseWriter, r *http.Request) {
	list, err := h.service.ListSystems(r.Context())
	if err != nil {
		writeError(w, err, "could not list the systems")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) getSystem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	s, err := h.service.GetSystem(r.Context(), id)
	if err != nil {
		writeError(w, err, "could not read the system")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, s)
}

func (h *Handler) createSystem(w http.ResponseWriter, r *http.Request) {
	var body systemBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	s, err := h.service.CreateSystem(r.Context(), actorID(r.Context()), body.Name, body.Description)
	if err != nil {
		writeError(w, err, "could not create the system")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🕳️ %s ha creado el sistema «%s».", h.actorLogin(r.Context()), s.Name))
	httpapi.WriteJSON(w, http.StatusCreated, s)
}

// systemAuditFields are the System fields an edit's notification diffs.
var systemAuditFields = []string{"Name", "Description"}

func (h *Handler) updateSystem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var body systemBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	before, beforeErr := h.service.GetSystem(r.Context(), id)
	s, err := h.service.UpdateSystem(r.Context(), id, actorID(r.Context()), body.Name, body.Description)
	if err != nil {
		writeError(w, err, "could not edit the system")
		return
	}
	if beforeErr == nil {
		if diff := httpapi.DiffFields(before, s, systemAuditFields...); len(diff) > 0 {
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha editado el sistema «%s»:\n%s",
				h.actorLogin(r.Context()), before.Name, strings.Join(diff, "\n")))
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, s)
}

func (h *Handler) deleteSystem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	// Read before deleting, so the notice can still name it.
	s, getErr := h.service.GetSystem(r.Context(), id)
	if err := h.service.DeleteSystem(r.Context(), id); err != nil {
		writeError(w, err, "could not delete the system")
		return
	}
	if getErr == nil {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha borrado el sistema «%s».", h.actorLogin(r.Context()), s.Name))
	}
	w.WriteHeader(http.StatusNoContent)
}

func refNames(list []SystemPlaceRef) string {
	names := make([]string, len(list))
	for i, p := range list {
		names[i] = "«" + p.Name + "»"
	}
	return strings.Join(names, ", ")
}

// setSystemPlaces makes the cavities of a system exactly the given ones.
func (h *Handler) setSystemPlaces(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		PlaceIDs []int64 `json:"place_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	before, after, err := h.service.SetSystemPlaces(r.Context(), id, actorID(r.Context()), body.PlaceIDs)
	if err != nil {
		writeError(w, err, "could not set the cavities of the system")
		return
	}
	if refNames(before) != refNames(after) {
		if s, err := h.service.GetSystem(r.Context(), id); err == nil {
			text := "ninguna"
			if len(after) > 0 {
				text = refNames(after)
			}
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🕳️ %s ha cambiado las cavidades del sistema «%s»: %s.",
				h.actorLogin(r.Context()), s.Name, text))
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, after)
}
