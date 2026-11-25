import { apiFetch, showError, showFeedback } from './api.js';
import { openModal } from './modal.js';
import { buildDialog } from './place-dialog.js';

// How open a place is (admin and board only): three levels, each for the club alone or for anybody,
// and a lock for the places that must never be public. Nothing is public yet: this only says what an
// opening would show, and every place starts closed.

export const AUDIENCE_LABELS = { club: 'Solo el club', public: 'Público' };

// What the page says of how open a place is, in one line.
export function visibilityText(visibility) {
  if (!visibility) return '';
  const { place, location, content, protected: locked } = visibility;
  const lock = locked ? ' · Protegido: no puede ser público' : '';
  if (place === 'club') return `Solo para el club${lock}`;
  return (
    `Lugar: ${AUDIENCE_LABELS[place].toLowerCase()} · ` +
    `Localización: ${AUDIENCE_LABELS[location].toLowerCase()} · ` +
    `Contenido: ${AUDIENCE_LABELS[content].toLowerCase()}${lock}`
  );
}

const levelField = (name, label, hint) => `
  <div class="field span-2">
    <label for="pv-${name}">${label}</label>
    <select id="pv-${name}" name="${name}">
      <option value="club">${AUDIENCE_LABELS.club}</option>
      <option value="public">${AUDIENCE_LABELS.public}</option>
    </select>
    <p class="hint">${hint}</p>
  </div>`;

let dialog = null;

function ensureDialog() {
  if (dialog) return dialog;
  const built = buildDialog(
    'visibility-dialog',
    `
    <h3>Visibilidad del lugar</h3>
    <p class="hint">
      Ahora mismo nada es público: esto dice qué se enseñaría el día que se abra el catálogo. Quien
      no pueda ver el lugar no sabe que existe.
    </p>
    <form id="visibility-form">
      <div class="field-grid">
        ${levelField('place', 'El lugar', 'Que existe, con su nombre, tipo, estado, zona y medidas.')}
        ${levelField('location', 'La localización', 'Las coordenadas, los demás puntos y cómo llegar. No puede estar más abierta que el lugar.')}
        ${levelField('content', 'El contenido', 'La descripción, la ficha técnica y los ficheros. No puede estar más abierto que el lugar.')}
        <div class="field span-2">
          <label class="checkbox-label" for="pv-protected"><input id="pv-protected" name="protected" type="checkbox"> Lugar protegido</label>
          <p class="hint">
            Para murciélagos, yacimientos o terrenos privados: un lugar protegido no puede tener nada
            público. Quién lo escribió y las actividades que fueron nunca son públicos.
          </p>
        </div>
      </div>
      <div class="panel-actions">
        <button type="submit" class="btn btn-primary">Guardar</button>
        <button type="button" class="btn btn-secondary" data-modal-close>Cancelar</button>
      </div>
    </form>`,
  );
  // What cannot be chosen is closed, so that the form never sends what the server would refuse.
  const sync = () => {
    const { place, location, content, protected: locked } = built.form.elements;
    if (locked.checked) place.value = 'club';
    place.disabled = locked.checked;
    const closed = locked.checked || place.value === 'club';
    for (const level of [location, content]) {
      if (closed) level.value = 'club';
      level.disabled = closed;
    }
  };
  built.form.addEventListener('change', sync);
  dialog = { ...built, sync };
  return dialog;
}

// Opens the dialog for `place`; `onSaved` runs once it is saved.
export function openVisibilityDialog({ place, onSaved }) {
  const { dialog: el, form, sync } = ensureDialog();
  const visibility = place.visibility ?? {
    place: 'club',
    location: 'club',
    content: 'club',
    protected: false,
  };
  form.elements.place.value = visibility.place;
  form.elements.location.value = visibility.location;
  form.elements.content.value = visibility.content;
  form.elements.protected.checked = Boolean(visibility.protected);
  sync();
  form.onsubmit = async (event) => {
    event.preventDefault();
    // Disabled fields are not read by the form, so the values are taken from it by name.
    const body = {
      place: form.elements.place.value,
      location: form.elements.location.value,
      content: form.elements.content.value,
      protected: form.elements.protected.checked,
    };
    try {
      await apiFetch(`/places/${place.id}/settings/visibility`, {
        method: 'PUT',
        body: JSON.stringify(body),
      });
    } catch (err) {
      showError(err.message);
      return;
    }
    el.close();
    showFeedback('Visibilidad guardada.');
    await onSaved?.();
  };
  openModal(el);
}
