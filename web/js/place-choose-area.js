import { apiFetch, apiFetchForm, showError, showFeedback } from './api.js';
import { areaPickerField, bindAreaPicker } from './area-picker.js';
import { openModal } from './modal.js';
import * as outbox from './outbox.js';
import { buildDialog } from './place-dialog.js';

// A new place made with no connection whose zone was not found when it was sent (the API found none for
// its coordinates, and no place of the list was near) waits for the person to choose one. This is the
// form for that: it needs the connection, as the search of zones does, and sends the place when a zone is
// chosen.

let dialog = null;

function ensureDialog() {
  if (dialog) return dialog;
  const built = buildDialog(
    'choose-area-dialog',
    `
    <h3 id="ca-title"></h3>
    <form id="choose-area-form">
      ${areaPickerField({ id: 'ca-area', label: 'Dónde está' })}
      <div class="panel-actions">
        <button type="submit" class="btn btn-primary">Elegir y enviar</button>
        <button type="button" class="btn btn-secondary" data-modal-close>Cancelar</button>
      </div>
    </form>`,
  );
  const picker = bindAreaPicker(built.dialog.querySelector('[data-area-picker]'));
  built.dialog.addEventListener('close', () => picker.setArea(null));
  dialog = { ...built, picker };
  return dialog;
}

// Opens the form for the entry of the outbox `entry`; `onSent` is called after it was tried to send.
export function openChooseArea({ entry, onSent }) {
  const { dialog: el, form, picker } = ensureDialog();
  document.getElementById('ca-title').textContent = `Zona de «${entry.name}»`;
  form.onsubmit = async (event) => {
    event.preventDefault();
    if (!picker.area) return;
    await outbox.update(entry.id, { areaId: picker.area.id, error: null, needsArea: false });
    el.close();
    try {
      const sent = await outbox.flush(entry.login, { apiFetch, apiFetchForm });
      if (sent.length) showFeedback('Enviado.');
    } catch (err) {
      showError(err.message);
    }
    await onSent?.();
  };
  openModal(el);
}
