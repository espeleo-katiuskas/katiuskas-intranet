import {
  apiFetch,
  currentUser,
  escapeHtml,
  hasAnyRole,
  isOffline,
  showError,
  showFeedback,
} from './api.js';
import { openAreaDialog } from './area-dialog.js';
import { areaChain, areaPickerField, bindAreaPicker } from './area-picker.js';
import { formatDecimal, formatUtm, parseCoordinates } from './coordinates.js';
import { initModal, openModal } from './modal.js';
import { bindNamesList, namesListField } from './names-list.js';
import * as outbox from './outbox.js';
import { describeFields, diffPlace } from './place-body.js';
import { guessText, nearestPlace } from './place-zone-guess.js';
import { POINT_KIND_LABELS, STATE_HELP, STATE_LABELS } from './places-common.js';

// The two dialogs to write a place and one of its points, shared by /lugares (a new place) and /lugar
// (to change it, and to add and change its points). They are built the first time they are needed.

const HINT =
  '<p class="hint">Admite enlaces, **negrita**, *cursiva*, listas con «- », títulos con «# » y «---».</p>';

// How long after the last key the zone of the coordinates is asked for.
const LOOKUP_DELAY_MS = 700;

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

// Whether the browser can say where the person is: it has the geolocation API and the page is a secure
// context (https, or localhost), which is the only place a browser answers it.
function canLocate() {
  return Boolean(navigator.geolocation) && window.isSecureContext;
}

function coordinatesField(id, label) {
  return `
    <div class="field span-2">
      <label for="${id}">${label}</label>
      <input id="${id}" name="coordinates" type="text" required autocomplete="off" placeholder="${COORDINATES_PLACEHOLDER}">
      <p class="hint coordinates-preview" id="${id}-preview"></p>${
        canLocate()
          ? `
      <p class="coordinates-locate">
        <button type="button" class="btn btn-secondary btn-small" id="${id}-locate">Usar mi ubicación</button>
        <span class="hint" id="${id}-locate-status" role="status"></span>
      </p>`
          : ''
      }
    </div>`;
}

const LOCATE_ERRORS = {
  1: 'No se ha dado permiso para usar la ubicación.',
  2: 'No se ha podido saber dónde estás.',
  3: 'Se ha tardado demasiado en saber dónde estás.',
};

// The button "Usar mi ubicación" of a field of coordinates (when the browser has one): it asks the browser
// where the person is, writes it in the field as decimal degrees and lets the field's own listeners see it
// (the preview, and the zone that is proposed from the coordinates), and says how precise it is. The
// altitude is not taken: the browser gives it over the ellipsoid and the form asks for it over the sea.
function bindLocate(input, id) {
  const button = document.getElementById(`${id}-locate`);
  if (!button) return;
  const status = document.getElementById(`${id}-locate-status`);
  button.addEventListener('click', () => {
    button.disabled = true;
    status.textContent = 'Buscando tu posición…';
    navigator.geolocation.getCurrentPosition(
      ({ coords }) => {
        input.value = formatDecimal(coords.latitude, coords.longitude);
        input.dispatchEvent(new Event('input', { bubbles: true }));
        status.textContent = `Precisión de unos ${Math.round(coords.accuracy)} m.`;
        button.disabled = false;
      },
      (error) => {
        status.textContent = LOCATE_ERRORS[error.code] ?? LOCATE_ERRORS[2];
        button.disabled = false;
      },
      { enableHighAccuracy: true, timeout: 20000, maximumAge: 0 },
    );
  });
  // A dialog opened again starts without what was said the last time.
  input.closest('dialog')?.addEventListener('close', () => {
    status.textContent = '';
    button.disabled = false;
  });
}

// What the zone line of a new place made with no connection says (set by openPlaceDialog).
let refreshZoneNote = () => {};
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
    <p class="hint" id="pd-osm-note" hidden></p>
    <form id="place-form">
      <div class="field-grid">
        <div class="field">
          <label for="pd-type">Tipo</label>
          <select id="pd-type" name="type_id" required></select>
        </div>
        <div class="field" data-kind-field="cave">
          <label for="pd-subtype">Subtipo</label>
          <select id="pd-subtype" name="subtype_id"></select>
          <p class="hint">Qué es la cavidad: cueva, sima, torca, mina, abrigo…</p>
        </div>
        <div class="field" data-kind-field="cave">
          <label for="pd-system">Sistema <span class="optional">(opcional)</span></label>
          <select id="pd-system" name="system_id"></select>
          <p class="hint">Si esta cavidad se comunica bajo tierra con otras. Los sistemas se crean en «Sistemas».</p>
        </div>
        <div class="field" id="pd-state-field">
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
        ${namesListField({ id: 'pd-alt-names', label: 'Otros nombres', hint: 'Con los que también se la conoce.' })}
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
        ${areaPickerField({ id: 'pd-area', label: 'Dónde está' })}
        <p class="hint" id="pd-area-offline" hidden></p>
        <p class="hint area-proposal" id="pd-area-proposal" hidden></p>
        <p class="hint area-add">
          Elige la zona más concreta que sepas (el municipio, la provincia o solo el país); si has
          escrito las coordenadas, se propone una.
          <button type="button" class="link-button" id="pd-area-add">¿No está? Añade una zona</button>
        </p>
        <div class="field span-2" data-new-only>
          <label for="pd-access">Cómo llegar <span class="optional">(opcional)</span></label>
          <textarea id="pd-access" name="access"></textarea>
          ${HINT}
        </div>
        <div class="field span-2" data-new-only>
          <label for="pd-description">Descripción <span class="optional">(opcional)</span></label>
          <textarea id="pd-description" name="description"></textarea>
          ${HINT}
        </div>
        <div class="field span-2" data-kind-field="cave,canyon" data-new-only>
          <label for="pd-sheet">Ficha técnica: qué instalación falta <span class="optional">(opcional)</span></label>
          <textarea id="pd-sheet" name="technical_sheet"></textarea>
          ${HINT}
        </div>
      </div>
      <div id="pd-osm-slot"></div>
      <div class="panel-actions">
        <button id="pd-submit" type="submit" class="btn btn-primary">Crear lugar</button>
        <button type="button" class="btn btn-secondary" data-modal-close>Cancelar</button>
      </div>
    </form>`,
  );
  const typeSelect = form.elements.type_id;
  const kindOf = () => typeSelect.selectedOptions[0]?.dataset.kind ?? 'general';
  const hasStateOf = () => typeSelect.selectedOptions[0]?.dataset.hasState === 'true';
  // What depends on the kind of the chosen type (the subtype and the measures of a cavity, the technical
  // sheet of a cavity or a canyon) is only in the form for those, and the state only for the types that
  // have one. The subtype is required when it is there.
  const syncKind = () => {
    // The long texts (how to get there, the description, the technical sheet) are only in the form of
    // a new place: an existing one has them edited one by one from its page (place-text-edit.js).
    form.querySelectorAll('[data-kind-field], [data-new-only]').forEach((el) => {
      const kinds = el.dataset.kindField;
      const fits = kinds === undefined || kinds.split(',').includes(kindOf());
      el.hidden = !fits || (form.dataset.editing === 'true' && el.dataset.newOnly !== undefined);
    });
    form.elements.subtype_id.required = kindOf() === 'cave';
    // A cavity starts as a Cueva (the first subtype) unless it already is something else.
    if (kindOf() === 'cave' && !form.elements.subtype_id.value) {
      form.elements.subtype_id.selectedIndex = 0;
    }
    document.getElementById('pd-state-field').hidden = !hasStateOf();
  };
  typeSelect.addEventListener('change', syncKind);
  // What the chosen state means, under it.
  const syncState = () => {
    document.getElementById('pd-state-help').textContent =
      STATE_HELP[form.elements.state.value] ?? '';
  };
  form.elements.state.addEventListener('change', syncState);
  const namesList = bindNamesList(dialog.querySelector('[data-names-list]'));
  const updateCoordinates = bindCoordinates(
    form.elements.coordinates,
    document.getElementById('pd-coordinates-preview'),
  );
  bindLocate(form.elements.coordinates, 'pd-coordinates');
  const areaPicker = bindAreaPicker(dialog.querySelector('[data-area-picker]'), {
    onChange: () => {
      // A person's own choice, or an emptied field, is no longer the proposal.
      if (!proposing && areaPicker.area?.id !== proposedId) clearProposal();
    },
  });
  const proposal = document.getElementById('pd-area-proposal');
  let proposedId = null; // The zone that was proposed from the coordinates and has not been touched since.
  let proposing = false; // While the proposal itself is being put in the field.
  let lookup = 0; // The newest lookup: an older answer is dropped.
  let lookupTimer = null;
  function clearProposal() {
    proposedId = null;
    proposal.hidden = true;
  }
  // When the coordinates change, ask which zone they are in and put it in "Dónde está" if that is empty
  // or still holds an earlier proposal. It only helps: a failure says nothing, and a zone that a person
  // chose is never replaced.
  async function proposeArea() {
    const found = parseCoordinates(form.elements.coordinates.value);
    const mine = ++lookup;
    const typing = !areaPicker.area && document.getElementById('pd-area').value.trim() !== '';
    if (!found || typing || (areaPicker.area && areaPicker.area.id !== proposedId)) return;
    try {
      const { area } = await apiFetch(`/areas/locate?lat=${found.lat}&lon=${found.lon}`);
      const nowTyping = !areaPicker.area && document.getElementById('pd-area').value.trim() !== '';
      if (mine !== lookup || nowTyping || (areaPicker.area && areaPicker.area.id !== proposedId)) {
        return;
      }
      if (!area) {
        if (proposedId) areaPicker.setArea(null);
        clearProposal();
        return;
      }
      proposing = true;
      areaPicker.setArea(area);
      proposing = false;
      proposedId = area.id;
      proposal.textContent = `Zona propuesta a partir de las coordenadas: ${areaChain(area)}. Cámbiala si no es esa.`;
      proposal.hidden = false;
    } catch {
      proposing = false; // The lookup is only a help.
    }
  }
  form.elements.coordinates.addEventListener('input', () => {
    refreshZoneNote();
    clearTimeout(lookupTimer);
    lookupTimer = setTimeout(proposeArea, LOOKUP_DELAY_MS);
  });
  // A zone that is not there is added, and chosen.
  document.getElementById('pd-area-add').addEventListener('click', () => {
    // Only an admin can add a country (a zone in no other one).
    openAreaDialog({
      parent: areaPicker.area,
      allowRoot: hasAnyRole(currentUser(), ['admin']),
      onSaved: (area) => areaPicker.setArea(area),
    });
  });
  dialog.addEventListener('close', () => {
    // initModal resets the form on close: bring the rest back in line with it.
    namesList.setNames([]);
    clearTimeout(lookupTimer);
    lookup += 1;
    clearProposal();
    areaPicker.setArea(null);
    updateCoordinates();
    syncKind();
    syncState();
  });
  // Puts a zone that is already known (the one of an element of OpenStreetMap) in "Dónde está", as a proposal
  // like the one of the coordinates: a person may change it, and then it is theirs.
  function proposeKnownArea(area, text) {
    lookup += 1; // An answer of the zone of the coordinates that is still coming is dropped.
    proposing = true;
    areaPicker.setArea(area);
    proposing = false;
    proposedId = area.id;
    proposal.textContent = text;
    proposal.hidden = false;
  }
  syncState();
  placeDialog = {
    dialog,
    form,
    namesList,
    hasStateOf,
    kindOf,
    syncKind,
    syncState,
    updateCoordinates,
    areaPicker,
    proposeArea,
    proposeKnownArea,
  };
  return placeDialog;
}

// The zone of a place as the picker wants it: the last of its path, with the ones before as ancestors.
function areaOfPlace(place) {
  const path = place.area_path ?? [];
  return path.length ? { ...path[path.length - 1], ancestors: path.slice(0, -1) } : null;
}

const text = (form, name) => form.elements[name].value.trim() || null;
const whole = (form, name) =>
  form.elements[name].value === '' ? null : Number(form.elements[name].value);

// Opens the dialog to create a place, or to change `place`. `types` are those of GET /places/types,
// `subtypes` those of GET /places/subtypes, `systems` those of GET /systems and `geologies` those of
// GET /places/geologies. `onSaved` is called with the place the API answers with, once it is saved (and `onQueued`, with no
// argument, when an edit was kept to be sent as there is no connection). A new place may start at `coordinates`
// ({ lat, lon }, from a click on a map): they are written in the form and the zone they are in is asked
// for at once.
// What a place made from an element of OpenStreetMap starts with, from its tags: the type that its category
// suggests (a mine is a cavity of the subtype Mina; what OpenStreetMap does not say, the subtype of any
// other cavity and the state, is left to the person), its names, altitude and, for a cavity, the depth
// and the length when they are numbers, and the description with the website at the end.
function osmDefaults(osm, types, subtypes) {
  const tags = osm.tags ?? {};
  const name = (tags.name || tags['name:es'] || '').trim();
  const byKind = (kind) => types.find((t) => t.kind === kind);
  const byName = (wanted) => types.find((t) => t.name === wanted);
  const type =
    {
      cave: byKind('cave'),
      canyon: byKind('canyon'),
      mine: byKind('cave'), // A mine is a cavity of the subtype Mina.
      hut: byName('Refugio'),
      via_ferrata: byName('Vía ferrata'),
    }[osm.category] ?? null;
  const altNames = [];
  for (const other of [...(tags.alt_name ?? '').split(';'), tags['name:es'] ?? '']) {
    const trimmed = other.trim();
    if (trimmed && trimmed !== name && !altNames.includes(trimmed)) altNames.push(trimmed);
  }
  const whole = (value) => {
    const n = Math.round(Number.parseFloat(value));
    return Number.isFinite(n) && n >= 0 ? n : null;
  };
  const altitude = Number.parseFloat(tags.ele);
  const isCave = type?.kind === 'cave';
  const website = /^https?:\/\//.test(tags.website ?? '') ? `Web: ${tags.website}` : '';
  return {
    typeId: type?.id ?? null,
    subtypeId:
      osm.category === 'mine' ? (subtypes.find((s) => s.name === 'Mina')?.id ?? null) : null,
    name,
    altNames,
    altitude: Number.isFinite(altitude) ? Math.round(altitude) : null,
    depth: isCave ? whole(tags.depth) : null,
    length: isCave ? whole(tags.length) : null,
    description: [(tags.description ?? '').trim(), website].filter(Boolean).join('\n\n'),
  };
}

// With no connection the edit of a place is kept in the outbox (js/outbox.js) as the fields that
// changed, with what each had, so that it can be sent on the place as it is by then.
export async function queuePlaceEdit(place, body) {
  const { base, changes } = diffPlace(place, body);
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
  showFeedback(`Guardado (${describeFields(Object.keys(changes))}): se enviará al haber conexión.`);
}

// A new place made with no connection waits in the outbox. `body` is the place as the API takes it (with
// the zone the person chose, if they could), and `near` the nearest place of the list, whose zone is the
// guess the outbox falls back on if the API finds none (see the sender of `place-new` in outbox.js).
async function queueNewPlace(body, near) {
  const { area_id: areaId, ...rest } = body;
  await outbox.enqueue({
    kind: 'place-new',
    login: currentUser().login,
    name: body.name,
    body: rest,
    areaId,
    guessAreaId: near?.place.area_id ?? null,
    guessWhere: near?.place.where ?? '',
  });
  showFeedback('Guardado: se enviará al haber conexión.');
}

function openPlaceDialog({
  types,
  subtypes,
  systems = [],
  geologies,
  place = null,
  coordinates = null,
  osm = null,
  nearby = [],
  onSaved,
  onQueued,
}) {
  const {
    dialog,
    form,
    namesList,
    hasStateOf,
    kindOf,
    syncKind,
    syncState,
    updateCoordinates,
    areaPicker,
    proposeArea,
    proposeKnownArea,
  } = ensurePlaceDialog();
  form.dataset.editing = place ? 'true' : 'false';
  // A new place made with no connection cannot choose its zone (the search of zones needs the API): the form
  // says which one it will probably get, and the place waits in the outbox until it is sent.
  const offlineNew = !place && isOffline();
  areaPicker.setRequired(!offlineNew);
  dialog.querySelector('[data-area-picker]').hidden = offlineNew;
  dialog.querySelector('.area-add').hidden = offlineNew;
  const zoneNote = document.getElementById('pd-area-offline');
  zoneNote.hidden = !offlineNew;
  refreshZoneNote = () => {
    if (!offlineNew) return;
    const found = parseCoordinates(form.elements.coordinates.value);
    zoneNote.textContent = found
      ? guessText(found.lat, found.lon, nearby)
      : 'Escribe las coordenadas: la zona se propone a partir de ellas.';
  };
  document.getElementById('pd-title').textContent = place ? 'Editar lugar' : 'Nuevo lugar';
  const osmNote = document.getElementById('pd-osm-note');
  osmNote.hidden = !osm;
  osmNote.textContent = osm
    ? 'Datos tomados de OpenStreetMap: revísalos y completa lo que falte (la zona se propone a partir de las coordenadas).'
    : '';
  document.getElementById('pd-submit').textContent = place ? 'Guardar' : 'Crear lugar';
  form.elements.type_id.innerHTML = types
    .map(
      (t) =>
        `<option value="${t.id}" data-kind="${t.kind}" data-has-state="${t.has_state}">${escapeHtml(t.name)}</option>`,
    )
    .join('');
  // The subtypes of a cavity: the first (Cueva) is the one a new place starts with.
  form.elements.subtype_id.innerHTML = subtypes
    .map((s) => `<option value="${s.id}">${escapeHtml(s.name)}</option>`)
    .join('');
  form.elements.system_id.innerHTML =
    '<option value="">— Ninguno —</option>' +
    systems.map((s) => `<option value="${s.id}">${escapeHtml(s.name)}</option>`).join('');
  form.elements.geology_id.innerHTML =
    '<option value="">—</option>' +
    geologies.map((g) => `<option value="${g.id}">${escapeHtml(g.name)}</option>`).join('');
  if (place) {
    const set = (name, value) => {
      form.elements[name].value = value ?? '';
    };
    set('type_id', place.type_id);
    set('geology_id', place.geology_id);
    set('subtype_id', place.subtype_id);
    set('system_id', place.system_id);
    set('state', place.state || 'open');
    set('name', place.name);
    namesList.setNames(place.alt_names);
    set('coordinates', formatDecimal(place.lat, place.lon));
    set('altitude_m', place.altitude_m);
    set('depth_m', place.depth_m);
    set('horizontal_length_m', place.horizontal_length_m);
    set('access', place.access);
    set('description', place.description);
    set('technical_sheet', place.technical_sheet);
  }
  const fromOsm = !place && osm ? osmDefaults(osm, types, subtypes) : null;
  if (fromOsm) {
    const set = (name, value) => {
      form.elements[name].value = value ?? '';
    };
    if (fromOsm.typeId) set('type_id', fromOsm.typeId);
    if (fromOsm.subtypeId) set('subtype_id', fromOsm.subtypeId);
    set('name', fromOsm.name);
    namesList.setNames(fromOsm.altNames);
    set('altitude_m', fromOsm.altitude);
    set('depth_m', fromOsm.depth);
    set('horizontal_length_m', fromOsm.length);
    set('description', fromOsm.description);
  }
  // Where it is: the zone of the place; a new one starts with none, to be chosen.
  areaPicker.setArea(place ? areaOfPlace(place) : null);
  const start = fromOsm ? { lat: osm.lat, lon: osm.lon } : coordinates;
  if (!place && start) form.elements.coordinates.value = formatDecimal(start.lat, start.lon);
  updateCoordinates();
  refreshZoneNote();
  if (!place && start) {
    if (fromOsm && osm.area_id) {
      // The element has its zone already (the load worked it out): no need to ask where the coordinates are,
      // which would go to an outside service. If it cannot be read, the coordinates are asked as always.
      apiFetch(`/areas/${osm.area_id}`)
        .then((area) => {
          // Not if the dialog was closed meanwhile, or the person already chose a zone or is typing one.
          const typing = document.getElementById('pd-area').value.trim() !== '';
          if (!dialog.open || areaPicker.area || typing) return;
          proposeKnownArea(
            area,
            `Zona del elemento de OpenStreetMap: ${areaChain(area)}. Cámbiala si no es esa.`,
          );
        })
        .catch(() => proposeArea());
    } else {
      proposeArea();
    }
  }
  syncKind();
  syncState();
  form.onsubmit = async (event) => {
    event.preventDefault();
    const found = parseCoordinates(form.elements.coordinates.value);
    if (!found || (!offlineNew && !areaPicker.area)) return;
    const kind = kindOf();
    const submit = form.querySelector('button[type="submit"]');
    submit.disabled = true;
    try {
      const body = {
        type_id: Number(form.elements.type_id.value),
        name: form.elements.name.value.trim(),
        alt_names: namesList.names,
        // Only a type that has a state gives its places one.
        state: hasStateOf() ? form.elements.state.value : '',
        subtype_id: kind === 'cave' ? Number(form.elements.subtype_id.value) : null,
        system_id: kind === 'cave' ? whole(form, 'system_id') : null,
        area_id: areaPicker.area?.id ?? null,
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
        // The element of OpenStreetMap it is made from, for a new place only.
        ...(!place && osm ? { osm_type: osm.type, osm_id: osm.id } : {}),
      };
      let saved = null;
      let changes = null;
      if (place) {
        // An edit names only what changed (PATCH). The long texts are not in this form, and a place that
        // becomes of a kind with no technical sheet must lose the one it has.
        delete body.access;
        delete body.description;
        delete body.technical_sheet;
        if (kind === 'general' && place.technical_sheet != null) body.technical_sheet = null;
        changes = diffPlace(place, body).changes;
        if (!Object.keys(changes).length) {
          dialog.close();
          showFeedback('No hay nada que cambiar.');
          return;
        }
      }
      if (!offlineNew && !(place && isOffline())) {
        try {
          saved = await apiFetch(place ? `/places/${place.id}` : '/places', {
            method: place ? 'PATCH' : 'POST',
            body: JSON.stringify(place ? changes : body),
          });
        } catch (err) {
          // The connection went away after the page opened: the place or its edit waits.
          if (!err.network) throw err;
        }
      }
      if (!saved) {
        if (place) {
          await queuePlaceEdit(place, body);
        } else {
          await queueNewPlace(body, nearestPlace(found.lat, found.lon, nearby));
        }
        dialog.close();
        await onQueued?.();
        return;
      }
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
  bindLocate(form.elements.coordinates, 'pt-coordinates');
  dialog.addEventListener('close', updateCoordinates);
  pointDialog = { dialog, form, updateCoordinates };
  return pointDialog;
}

// Opens the dialog to add a point to the place `placeId`, or to change `point`.
function openPointDialog({ placeId, point = null, coordinates = null, onSaved, onQueued }) {
  const { dialog, form, updateCoordinates } = ensurePointDialog();
  document.getElementById('pt-title').textContent = point ? 'Editar punto' : 'Nuevo punto';
  document.getElementById('pt-submit').textContent = point ? 'Guardar' : 'Añadir punto';
  if (point) {
    form.elements.kind.value = point.kind;
    form.elements.name.value = point.name ?? '';
    form.elements.coordinates.value = formatDecimal(point.lat, point.lon);
    form.elements.altitude_m.value = point.altitude_m ?? '';
    form.elements.note.value = point.note ?? '';
  } else if (coordinates) {
    // A new point may start at the coordinates of a click on the map.
    form.elements.coordinates.value = formatDecimal(coordinates.lat, coordinates.lon);
  }
  updateCoordinates();
  form.onsubmit = async (event) => {
    event.preventDefault();
    const found = parseCoordinates(form.elements.coordinates.value);
    if (!found) return;
    const submit = form.querySelector('button[type="submit"]');
    submit.disabled = true;
    try {
      const body = {
        kind: form.elements.kind.value,
        name: text(form, 'name'),
        lat: found.lat,
        lon: found.lon,
        altitude_m: whole(form, 'altitude_m'),
        note: text(form, 'note'),
      };
      let saved = null;
      if (point || !isOffline()) {
        try {
          saved = await apiFetch(
            point ? `/places/${placeId}/points/${point.id}` : `/places/${placeId}/points`,
            { method: point ? 'PUT' : 'POST', body: JSON.stringify(body) },
          );
        } catch (err) {
          // A new point waits for the connection; changing one needs it.
          if (point || !err.network) throw err;
        }
      }
      if (!saved) {
        await outbox.enqueue({
          kind: 'place-point',
          login: currentUser().login,
          placeId,
          point: body,
        });
        showFeedback('Guardado: se enviará al haber conexión.');
        dialog.close();
        await onQueued?.();
        return;
      }
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

export {
  bindCoordinates,
  bindLocate,
  buildDialog,
  coordinatesField,
  openPlaceDialog,
  openPointDialog,
};
