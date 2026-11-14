import { apiFetch, escapeHtml, showError } from './api.js';
import { openAreaDialog } from './area-dialog.js';
import { areaPickerField, bindAreaPicker } from './area-picker.js';
import { formatDecimal, formatUtm, parseCoordinates } from './coordinates.js';
import { initModal, openModal } from './modal.js';
import { POINT_KIND_LABELS, STATE_HELP, STATE_LABELS } from './places-common.js';

// The two dialogs to write a place and one of its points, shared by /lugares (a new place) and /lugar
// (to change it, and to add and change its points). They are built the first time they are needed.

const HINT =
  '<p class="hint">Admite enlaces, **negrita**, *cursiva*, listas con «- », títulos con «# » y «---».</p>';

const COORDINATES_PLACEHOLDER = '40.4168, -3.7038   ó   30T 440290 4474257';

// What a person typed in a field of coordinates, as understood, under the field: the decimal degrees
// and the UTM of it, or that it is not understood (and the field is invalid until it is).
function bindCoordinates(input, preview) {
  const update = () => {
    const text = input.value.trim();
    preview.classList.remove('coordinates-invalid');
    if (!text) {
      preview.textContent =
        'Latitud y longitud en grados (40.4168, -3.7038) o UTM (30T 440290 4474257).';
      input.setCustomValidity('');
      return;
    }
    const found = parseCoordinates(text);
    if (!found) {
      preview.textContent = 'No se entienden las coordenadas.';
      preview.classList.add('coordinates-invalid');
      input.setCustomValidity('No se entienden las coordenadas.');
      return;
    }
    const utm = formatUtm(found.lat, found.lon);
    preview.textContent =
      `= ${formatDecimal(found.lat, found.lon)}` +
      (utm ? ` · UTM ${utm}` : '') +
      (found.assumedZone ? ' (se ha supuesto el huso 30)' : '');
    input.setCustomValidity('');
  };
  input.addEventListener('input', update);
  update();
  return update;
}

function coordinatesField(id, label) {
  return `
    <div class="field span-2">
      <label for="${id}">${label}</label>
      <input id="${id}" name="coordinates" type="text" required autocomplete="off" placeholder="${COORDINATES_PLACEHOLDER}">
      <p class="hint coordinates-preview" id="${id}-preview"></p>
    </div>`;
}

let placeDialog = null;
let pointDialog = null;

function buildDialog(id, html) {
  const dialog = document.createElement('dialog');
  dialog.id = id;
  dialog.className = 'panel modal';
  dialog.innerHTML = `<button type="button" class="modal-close" data-modal-close aria-label="Cerrar">&times;</button>${html}`;
  document.querySelector('main.content').append(dialog);
  const form = dialog.querySelector('form');
  initModal(dialog, form);
  return { dialog, form };
}

function ensurePlaceDialog() {
  if (placeDialog) return placeDialog;
  const { dialog, form } = buildDialog(
    'place-dialog',
    `
    <h3 id="pd-title">Nuevo lugar</h3>
    <form id="place-form">
      <div class="field-grid">
        <div class="field">
          <label for="pd-type">Tipo</label>
          <select id="pd-type" name="type_id" required></select>
        </div>
        <div class="field">
          <label for="pd-state">Estado</label>
          <select id="pd-state" name="state">
            ${Object.entries(STATE_LABELS)
              .map(([value, label]) => `<option value="${value}">${label}</option>`)
              .join('')}
          </select>
          <p class="hint" id="pd-state-help"></p>
        </div>
        <div class="field span-2">
          <label for="pd-name">Nombre</label>
          <input id="pd-name" name="name" type="text" required>
        </div>
        <div class="field span-2">
          <label for="pd-alt-names">Otros nombres <span class="optional">(opcional)</span></label>
          <input id="pd-alt-names" name="alt_names" type="text">
        </div>
        ${areaPickerField({ id: 'pd-area', label: 'Dónde está' })}
        <p class="hint span-2 area-add">
          Elige la zona más concreta que sepas (el municipio, la provincia o solo el país).
          <button type="button" class="link-button" id="pd-area-add">¿No está? Añade una zona</button>
        </p>
        ${coordinatesField('pd-coordinates', 'Coordenadas principales')}
        <div class="field" data-kind-field="cave">
          <label for="pd-depth">Profundidad (m) <span class="optional">(opcional)</span></label>
          <input id="pd-depth" name="depth_m" type="number" min="0" step="1">
        </div>
        <div class="field" data-kind-field="cave">
          <label for="pd-length">Desarrollo horizontal (m) <span class="optional">(opcional)</span></label>
          <input id="pd-length" name="horizontal_length_m" type="number" min="0" step="1">
        </div>
        <div class="field">
          <label for="pd-altitude">Altitud (m) <span class="optional">(opcional)</span></label>
          <input id="pd-altitude" name="altitude_m" type="number" min="-500" max="9000" step="1">
          <p class="hint">Metros sobre el nivel del mar.</p>
        </div>
        <div class="field" data-kind-field="cave">
          <label for="pd-geology">Geología <span class="optional">(opcional)</span></label>
          <select id="pd-geology" name="geology_id"></select>
          <p class="hint">El material en el que se formó la cueva.</p>
        </div>
        <div class="field span-2">
          <label for="pd-access">Cómo llegar <span class="optional">(opcional)</span></label>
          <textarea id="pd-access" name="access"></textarea>
          ${HINT}
        </div>
        <div class="field span-2">
          <label for="pd-description">Descripción <span class="optional">(opcional)</span></label>
          <textarea id="pd-description" name="description"></textarea>
          ${HINT}
        </div>
        <div class="field span-2" data-kind-field="cave,canyon">
          <label for="pd-sheet">Ficha técnica: qué instalación falta <span class="optional">(opcional)</span></label>
          <textarea id="pd-sheet" name="technical_sheet"></textarea>
          ${HINT}
        </div>
      </div>
      <div class="panel-actions">
        <button id="pd-submit" type="submit" class="btn btn-primary">Crear lugar</button>
        <button type="button" class="btn btn-secondary" data-modal-close>Cancelar</button>
      </div>
    </form>`,
  );
  const typeSelect = form.elements.type_id;
  const kindOf = () => typeSelect.selectedOptions[0]?.dataset.kind ?? 'general';
  // What depends on the kind of the chosen type (the measures of a cave, the technical sheet of a cave
  // or a canyon) is only in the form for those.
  const syncKind = () => {
    form.querySelectorAll('[data-kind-field]').forEach((el) => {
      el.hidden = !el.dataset.kindField.split(',').includes(kindOf());
    });
  };
  typeSelect.addEventListener('change', syncKind);
  // What the chosen state means, under it.
  const syncState = () => {
    document.getElementById('pd-state-help').textContent =
      STATE_HELP[form.elements.state.value] ?? '';
  };
  form.elements.state.addEventListener('change', syncState);
  const updateCoordinates = bindCoordinates(
    form.elements.coordinates,
    document.getElementById('pd-coordinates-preview'),
  );
  const areaPicker = bindAreaPicker(dialog.querySelector('[data-area-picker]'));
  // A zone that is not there is added, and chosen.
  document.getElementById('pd-area-add').addEventListener('click', () => {
    openAreaDialog({ parent: areaPicker.area, onSaved: (area) => areaPicker.setArea(area) });
  });
  dialog.addEventListener('close', () => {
    // initModal resets the form on close: bring the rest back in line with it.
    areaPicker.setArea(null);
    updateCoordinates();
    syncKind();
    syncState();
  });
  syncState();
  placeDialog = { dialog, form, kindOf, syncKind, syncState, updateCoordinates, areaPicker };
  return placeDialog;
}

// The zone of a place as the picker wants it: the last of its path, with the ones before as ancestors.
function areaOfPlace(place) {
  const path = place.area_path ?? [];
  return path.length ? { ...path[path.length - 1], ancestors: path.slice(0, -1) } : null;
}

// The last zone chosen for a place is kept in the browser, to start the next one there.
const LAST_AREA_KEY = 'katiuskas:places-last-area';

function lastArea() {
  try {
    return JSON.parse(localStorage.getItem(LAST_AREA_KEY));
  } catch {
    return null;
  }
}

function rememberArea(area) {
  try {
    localStorage.setItem(
      LAST_AREA_KEY,
      JSON.stringify({ id: area.id, kind: area.kind, name: area.name, ancestors: area.ancestors }),
    );
  } catch {
    // Storage blocked: it is only a convenience.
  }
}

const text = (form, name) => form.elements[name].value.trim() || null;
const whole = (form, name) =>
  form.elements[name].value === '' ? null : Number(form.elements[name].value);

// Opens the dialog to create a place, or to change `place`. `types` are those of GET /places/types and
// `geologies` those of GET /places/geologies. `onSaved` is called with the place the API answers with, once it is saved.
function openPlaceDialog({ types, geologies, place = null, onSaved }) {
  const { dialog, form, kindOf, syncKind, syncState, updateCoordinates, areaPicker } =
    ensurePlaceDialog();
  document.getElementById('pd-title').textContent = place ? 'Editar lugar' : 'Nuevo lugar';
  document.getElementById('pd-submit').textContent = place ? 'Guardar' : 'Crear lugar';
  form.elements.type_id.innerHTML = types
    .map((t) => `<option value="${t.id}" data-kind="${t.kind}">${escapeHtml(t.name)}</option>`)
    .join('');
  form.elements.geology_id.innerHTML =
    '<option value="">—</option>' +
    geologies.map((g) => `<option value="${g.id}">${escapeHtml(g.name)}</option>`).join('');
  if (place) {
    const set = (name, value) => {
      form.elements[name].value = value ?? '';
    };
    set('type_id', place.type_id);
    set('geology_id', place.geology_id);
    set('state', place.state);
    set('name', place.name);
    set('alt_names', place.alt_names);
    set('coordinates', formatDecimal(place.lat, place.lon));
    set('altitude_m', place.altitude_m);
    set('depth_m', place.depth_m);
    set('horizontal_length_m', place.horizontal_length_m);
    set('access', place.access);
    set('description', place.description);
    set('technical_sheet', place.technical_sheet);
  }
  // Where it is: the zone of the place, or the last one somebody chose here (the places come in groups
  // of the same area).
  areaPicker.setArea(place ? areaOfPlace(place) : lastArea());
  updateCoordinates();
  syncKind();
  syncState();
  form.onsubmit = async (event) => {
    event.preventDefault();
    const found = parseCoordinates(form.elements.coordinates.value);
    if (!found || !areaPicker.area) return;
    const kind = kindOf();
    const submit = form.querySelector('button[type="submit"]');
    submit.disabled = true;
    try {
      const saved = await apiFetch(place ? `/places/${place.id}` : '/places', {
        method: place ? 'PUT' : 'POST',
        body: JSON.stringify({
          type_id: Number(form.elements.type_id.value),
          name: form.elements.name.value.trim(),
          alt_names: text(form, 'alt_names'),
          state: form.elements.state.value,
          area_id: areaPicker.area.id,
          lat: found.lat,
          lon: found.lon,
          altitude_m: whole(form, 'altitude_m'),
          access: text(form, 'access'),
          description: text(form, 'description'),
          // What the kind of the type does not have is left out, as the API refuses it.
          depth_m: kind === 'cave' ? whole(form, 'depth_m') : null,
          horizontal_length_m: kind === 'cave' ? whole(form, 'horizontal_length_m') : null,
          geology_id: kind === 'cave' ? whole(form, 'geology_id') : null,
          technical_sheet:
            kind === 'cave' || kind === 'canyon' ? text(form, 'technical_sheet') : null,
        }),
      });
      rememberArea(areaPicker.area);
      dialog.close();
      await onSaved?.(saved);
    } catch (err) {
      showError(err.message);
    } finally {
      submit.disabled = false;
    }
  };
  openModal(dialog);
}

function ensurePointDialog() {
  if (pointDialog) return pointDialog;
  const { dialog, form } = buildDialog(
    'point-dialog',
    `
    <h3 id="pt-title">Nuevo punto</h3>
    <form id="point-form">
      <div class="field-grid">
        <div class="field">
          <label for="pt-kind">Qué es</label>
          <select id="pt-kind" name="kind" required>
            ${Object.entries(POINT_KIND_LABELS)
              .map(([value, label]) => `<option value="${value}">${label}</option>`)
              .join('')}
          </select>
        </div>
        <div class="field">
          <label for="pt-name">Nombre <span class="optional">(opcional)</span></label>
          <input id="pt-name" name="name" type="text">
        </div>
        ${coordinatesField('pt-coordinates', 'Coordenadas')}
        <div class="field span-2">
          <label for="pt-altitude">Altitud (m) <span class="optional">(opcional)</span></label>
          <input id="pt-altitude" name="altitude_m" type="number" min="-500" max="9000" step="1">
        </div>
        <div class="field span-2">
          <label for="pt-note">Nota <span class="optional">(opcional)</span></label>
          <textarea id="pt-note" name="note"></textarea>
        </div>
      </div>
      <div class="panel-actions">
        <button id="pt-submit" type="submit" class="btn btn-primary">Añadir punto</button>
        <button type="button" class="btn btn-secondary" data-modal-close>Cancelar</button>
      </div>
    </form>`,
  );
  const updateCoordinates = bindCoordinates(
    form.elements.coordinates,
    document.getElementById('pt-coordinates-preview'),
  );
  dialog.addEventListener('close', updateCoordinates);
  pointDialog = { dialog, form, updateCoordinates };
  return pointDialog;
}

// Opens the dialog to add a point to the place `placeId`, or to change `point`.
function openPointDialog({ placeId, point = null, onSaved }) {
  const { dialog, form, updateCoordinates } = ensurePointDialog();
  document.getElementById('pt-title').textContent = point ? 'Editar punto' : 'Nuevo punto';
  document.getElementById('pt-submit').textContent = point ? 'Guardar' : 'Añadir punto';
  if (point) {
    form.elements.kind.value = point.kind;
    form.elements.name.value = point.name ?? '';
    form.elements.coordinates.value = formatDecimal(point.lat, point.lon);
    form.elements.altitude_m.value = point.altitude_m ?? '';
    form.elements.note.value = point.note ?? '';
  }
  updateCoordinates();
  form.onsubmit = async (event) => {
    event.preventDefault();
    const found = parseCoordinates(form.elements.coordinates.value);
    if (!found) return;
    const submit = form.querySelector('button[type="submit"]');
    submit.disabled = true;
    try {
      const saved = await apiFetch(
        point ? `/places/${placeId}/points/${point.id}` : `/places/${placeId}/points`,
        {
          method: point ? 'PUT' : 'POST',
          body: JSON.stringify({
            kind: form.elements.kind.value,
            name: text(form, 'name'),
            lat: found.lat,
            lon: found.lon,
            altitude_m: whole(form, 'altitude_m'),
            note: text(form, 'note'),
          }),
        },
      );
      dialog.close();
      await onSaved?.(saved);
    } catch (err) {
      showError(err.message);
    } finally {
      submit.disabled = false;
    }
  };
  openModal(dialog);
}

export { openPlaceDialog, openPointDialog };
