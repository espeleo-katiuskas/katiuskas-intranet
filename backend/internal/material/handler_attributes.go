package material

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
)

// The routes of the attributes of a type and the options of an enum attribute. All admin only.

// pathID reads the numeric {id} of the request, answering 400 itself if it is not one.
func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return 0, false
	}
	return id, true
}

func (h *Handler) createAttribute(w http.ResponseWriter, r *http.Request) {
	typeID, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Name     string            `json:"name"`
		DataType AttributeDataType `json:"data_type"`
		Required bool              `json:"required"`
		Options  []string          `json:"options"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "invalid JSON body")
		return
	}
	a, err := h.service.CreateAttribute(r.Context(), typeID, body.Name, body.DataType, body.Required, body.Options)
	if err != nil {
		writeTypeError(w, err, "create the attribute of")
		return
	}
	h.notifyAttribute(r, "ha añadido el atributo «%s» al tipo de material «%s».", a)
	httpapi.WriteJSON(w, http.StatusCreated, a)
}

func (h *Handler) updateAttribute(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Name     *string `json:"name"`
		Required *bool   `json:"required"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || (body.Name == nil && body.Required == nil) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "give a name and/or required")
		return
	}
	before, beforeErr := h.service.GetAttribute(r.Context(), id)
	a, err := h.service.UpdateAttribute(r.Context(), id, body.Name, body.Required)
	if err != nil {
		writeTypeError(w, err, "update the attribute of")
		return
	}
	if beforeErr == nil {
		if before.Name != a.Name {
			h.notifyAttribute(r, fmt.Sprintf("ha cambiado el nombre del atributo «%s» por «%%s» en el tipo de material «%%s».", escapePercent(before.Name)), a)
		}
		if before.Required != a.Required {
			state := "ya no es obligatorio"
			if a.Required {
				state = "pasa a ser obligatorio"
			}
			h.notifyAttribute(r, "ha cambiado el atributo «%s» del tipo de material «%s»: "+state+".", a)
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, a)
}

func (h *Handler) deleteAttribute(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	a, err := h.service.DeleteAttribute(r.Context(), id)
	if err != nil {
		writeTypeError(w, err, "delete the attribute of")
		return
	}
	h.notifyAttribute(r, "ha borrado el atributo «%s» del tipo de material «%s».", a)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) reorderAttributes(w http.ResponseWriter, r *http.Request) {
	typeID, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		AttributeIDs []int64 `json:"attribute_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "invalid JSON body")
		return
	}
	if err := h.service.ReorderAttributes(r.Context(), typeID, body.AttributeIDs); err != nil {
		writeTypeError(w, err, "reorder the attributes of")
		return
	}
	t, err := h.service.GetMaterialType(r.Context(), typeID)
	if err != nil {
		writeTypeError(w, err, "reorder the attributes of")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗂️ %s ha cambiado el orden de los atributos del tipo de material «%s».",
		h.actorLogin(r.Context()), t.Name))
	httpapi.WriteJSON(w, http.StatusOK, t)
}

func (h *Handler) createOption(w http.ResponseWriter, r *http.Request) {
	attributeID, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "invalid JSON body")
		return
	}
	o, err := h.service.CreateOption(r.Context(), attributeID, body.Label)
	if err != nil {
		writeTypeError(w, err, "create the option of")
		return
	}
	h.notifyOption(r, "ha añadido la opción «%s» al atributo «%s» del tipo de material «%s».", o.Label, attributeID)
	httpapi.WriteJSON(w, http.StatusCreated, o)
}

func (h *Handler) renameOption(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "invalid JSON body")
		return
	}
	before, attr, beforeErr := h.service.GetOption(r.Context(), id)
	o, err := h.service.RenameOption(r.Context(), id, body.Label)
	if err != nil {
		writeTypeError(w, err, "rename the option of")
		return
	}
	if beforeErr == nil && before.Label != o.Label {
		h.notifyOption(r, fmt.Sprintf("ha cambiado la opción «%s» por «%%s» en el atributo «%%s» del tipo de material «%%s».", escapePercent(before.Label)), o.Label, attr.ID)
	}
	httpapi.WriteJSON(w, http.StatusOK, o)
}

func (h *Handler) deleteOption(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	o, attr, optErr := h.service.GetOption(r.Context(), id)
	if err := h.service.DeleteOption(r.Context(), id); err != nil {
		writeTypeError(w, err, "delete the option of")
		return
	}
	if optErr == nil {
		h.notifyOption(r, "ha borrado la opción «%s» del atributo «%s» del tipo de material «%s».", o.Label, attr.ID)
	}
	w.WriteHeader(http.StatusNoContent)
}

// notifyAttribute announces a change to an attribute; format takes the attribute's name and its
// type's.
func (h *Handler) notifyAttribute(r *http.Request, format string, a Attribute) {
	typeName := fmt.Sprintf("tipo#%d", a.TypeID)
	if t, err := h.service.GetMaterialType(r.Context(), a.TypeID); err == nil {
		typeName = t.Name
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗂️ %s "+format, h.actorLogin(r.Context()), a.Name, typeName))
}

// notifyOption announces a change to an option; format takes the label, the attribute's name and its
// type's. The attribute is looked up again since the option's row does not say it.
func (h *Handler) notifyOption(r *http.Request, format, label string, attributeID int64) {
	attrName, typeName := fmt.Sprintf("atributo#%d", attributeID), "?"
	if a, err := h.service.GetAttribute(r.Context(), attributeID); err == nil {
		attrName = a.Name
		if t, err := h.service.GetMaterialType(r.Context(), a.TypeID); err == nil {
			typeName = t.Name
		}
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗂️ %s "+format, h.actorLogin(r.Context()), label, attrName, typeName))
}

// escapePercent protects a text that is spliced into a format string.
func escapePercent(s string) string {
	return strings.ReplaceAll(s, "%", "%%")
}
