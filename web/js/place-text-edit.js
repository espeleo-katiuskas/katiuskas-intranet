import { apiFetch, isOffline, showError, showFeedback } from './api.js';
import { openModal } from './modal.js';
import { buildDialog, queuePlaceEdit } from './place-dialog.js';

// The long texts of a place (how to get there, the description, the technical sheet) are edited one at a
// time, each in a form of its own with the whole text in front, not in the form of the place: they are
// often very long. It changes only that field (PATCH /places/{id}); with no connection the change is kept
// in the outbox like any other edit of the place (see queuePlaceEdit).

let dialog = null;

function ensureDialog() {
  if (dialog) return dialog;
  const built = buildDialog(
    'text-edit-dialog',
    `
    <h3 id="te-title"></h3>
    <form id="text-edit-form">
      <div class="field">
        <label for="te-text" id="te-label"></label>
        <textarea id="te-text" name="text" rows="18"></textarea>
        <p class="hint">Admite enlaces, **negrita**, *cursiva*, listas con «- », títulos con «# » y «---».</p>
      </div>
      <div class="panel-actions">
        <button type="submit" class="btn btn-primary">Guardar</button>
        <button type="button" class="btn btn-secondary" data-modal-close>Cancelar</button>
      </div>
    </form>`,
  );
  built.dialog.classList.add('wide');
  dialog = built;
  return dialog;
}

// Opens the form of `field` ("access", "description" or "technical_sheet") of `place`, titled `title`.
// `onSaved` is called once it is saved and `onQueued` once it is kept to be sent later.
export function openTextEdit({ place, field, title, onSaved, onQueued }) {
  const { dialog: el, form } = ensureDialog();
  document.getElementById('te-title').textContent = `Editar: ${title}`;
  document.getElementById('te-label').textContent = title;
  form.elements.text.value = place[field] ?? '';
  form.onsubmit = async (event) => {
    event.preventDefault();
    const value = form.elements.text.value.trim() || null;
    if (value === (place[field] ?? null)) {
      el.close();
      showFeedback('No hay nada que cambiar.');
      return;
    }
    const submit = form.querySelector('button[type="submit"]');
    submit.disabled = true;
    try {
      let saved = null;
      if (!isOffline()) {
        try {
          saved = await apiFetch(`/places/${place.id}`, {
            method: 'PATCH',
            body: JSON.stringify({ [field]: value }),
          });
        } catch (err) {
          // The connection went away after the page opened: the change waits.
          if (!err.network) throw err;
        }
      }
      if (saved) {
        el.close();
        await onSaved?.(saved);
      } else {
        await queuePlaceEdit(place, { [field]: value });
        el.close();
        await onQueued?.();
      }
    } catch (err) {
      showError(err.message);
    } finally {
      submit.disabled = false;
    }
  };
  openModal(el);
}
