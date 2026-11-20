import { currentUser, showFeedback } from './api.js';
import { formatDecimal, parseCoordinates } from './coordinates.js';
import { openModal } from './modal.js';
import * as outbox from './outbox.js';
import { describeFields, sameValue } from './place-body.js';
import { bindCoordinates, bindLocate, buildDialog, coordinatesField } from './place-dialog.js';

// The short edit of a place that is on the page only in part (with no connection, from what the list of
// places has): its name, its main coordinate ("Usar mi ubicación" works with no signal) and its altitude.
// The full edit is not offered then, as it would clear what the page does not have; this one only names
// the fields it changes, which the outbox sends as a PATCH when the connection is back.

let dialog = null;

function ensureDialog() {
  if (dialog) return dialog;
  const built = buildDialog(
    'quick-edit-dialog',
    `
    <h3>Corregir datos del lugar</h3>
    <form id="quick-edit-form">
      <div class="field-grid">
        <div class="field span-2">
          <label for="qe-name">Nombre</label>
          <input id="qe-name" name="name" type="text" required>
        </div>
        ${coordinatesField('qe-coordinates', 'Coordenadas principales')}
        <div class="field span-2">
          <label for="qe-altitude">Altitud (m) <span class="optional">(opcional)</span></label>
          <input id="qe-altitude" name="altitude_m" type="number" min="-500" max="9000" step="1">
          <p class="hint">Metros sobre el nivel del mar.</p>
        </div>
      </div>
      <div class="panel-actions">
        <button type="submit" class="btn btn-primary">Guardar</button>
        <button type="button" class="btn btn-secondary" data-modal-close>Cancelar</button>
      </div>
    </form>`,
  );
  const updateCoordinates = bindCoordinates(
    built.form.elements.coordinates,
    document.getElementById('qe-coordinates-preview'),
  );
  bindLocate(built.form.elements.coordinates, 'qe-coordinates');
  built.dialog.addEventListener('close', updateCoordinates);
  dialog = { ...built, updateCoordinates };
  return dialog;
}

// Opens the dialog for `place` (an item of the list of places); `onQueued` is called once the change is kept.
export function openQuickEdit({ place, onQueued }) {
  const { dialog: el, form, updateCoordinates } = ensureDialog();
  form.elements.name.value = place.name;
  form.elements.coordinates.value = formatDecimal(place.lat, place.lon);
  form.elements.altitude_m.value = place.altitude_m ?? '';
  updateCoordinates();
  form.onsubmit = async (event) => {
    event.preventDefault();
    const found = parseCoordinates(form.elements.coordinates.value);
    const name = form.elements.name.value.trim();
    if (!found || !name) return;
    const altitude =
      form.elements.altitude_m.value === '' ? null : Number(form.elements.altitude_m.value);
    const original = {
      name: place.name,
      lat: place.lat,
      lon: place.lon,
      altitude_m: place.altitude_m ?? null,
    };
    const wanted = { name, lat: found.lat, lon: found.lon, altitude_m: altitude };
    const base = {};
    const changes = {};
    for (const [field, value] of Object.entries(wanted)) {
      if (!sameValue(value, original[field])) {
        base[field] = original[field];
        changes[field] = value;
      }
    }
    // The coordinates are written as text with five decimals: unchanged text is no change.
    if (form.elements.coordinates.value.trim() === formatDecimal(place.lat, place.lon)) {
      delete base.lat;
      delete changes.lat;
      delete base.lon;
      delete changes.lon;
    }
    el.close();
    if (!Object.keys(changes).length) {
      showFeedback('No hay nada que cambiar.');
      return;
    }
    await outbox.enqueue({
      kind: 'place-edit',
      login: currentUser().login,
      placeId: place.id,
      placeName: place.name,
      base,
      changes,
    });
    showFeedback(
      `Guardado (${describeFields(Object.keys(changes))}): se enviará al haber conexión.`,
    );
    await onQueued?.();
  };
  openModal(el);
}
