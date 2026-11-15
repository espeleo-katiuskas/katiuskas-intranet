import {
  apiFetch,
  escapeHtml,
  hasAnyRole,
  normalizeForSearch,
  showError,
  showFeedback,
} from './api.js';
import { initAreasManager } from './areas-manager.js';
import { createMap, mapLegend, offerOnMapClick, placeMarker, placePopup } from './map-common.js';
import { initPage } from './layout.js';
import { initModal, openModal } from './modal.js';
import { openPlaceDialog } from './place-dialog.js';
import {
  loadGeologies,
  loadSubtypes,
  loadSystems,
  loadTypes,
  measuresHtml,
  placeTypeLabel,
  STATE_LABELS,
  stateBadge,
} from './places-common.js';

const user = await initPage();
const isAdmin = hasAnyRole(user, ['admin']);

const tableBody = document.getElementById('places-table-body');
const searchInput = document.getElementById('places-search');
const typeFilter = document.getElementById('type-filter');
const stateFilter = document.getElementById('state-filter');
const subtypeFilter = document.getElementById('subtype-filter');
const countLine = document.getElementById('places-count');
let allPlaces = [];
let types = [];
let subtypes = [];
let systems = [];
let geologies = [];

stateFilter.innerHTML += Object.entries(STATE_LABELS)
  .map(([value, label]) => `<option value="${value}">${label}</option>`)
  .join('');

// What the search looks in: what a row shows (the zone it is in, and so its province, its community and
// its country too, and the geology of a cave), and the other names of the place.
function searchText(place) {
  return normalizeForSearch(
    [
      place.name,
      ...place.alt_names,
      place.type_name,
      place.subtype,
      place.geology,
      STATE_LABELS[place.state],
      place.where,
    ]
      .filter(Boolean)
      .join(' '),
  );
}

function renderPlace(place) {
  return `
    <tr>
      <td><a href="/lugar?id=${place.id}"><strong>${escapeHtml(place.name)}</strong></a>${
        place.alt_names.length
          ? `<br><span class="subtitle">${escapeHtml(place.alt_names.join(' · '))}</span>`
          : ''
      }</td>
      <td>${escapeHtml(placeTypeLabel(place))}${
        place.geology ? `<br><span class="subtitle">${escapeHtml(place.geology)}</span>` : ''
      }</td>
      <td>${stateBadge(place.state)}</td>
      <td>${escapeHtml(place.where)}</td>
      <td>${measuresHtml(place) || '—'}</td>
    </tr>`;
}

// ----- the map: the same places the table shows, with the same filters -----

const params = new URLSearchParams(window.location.search);
let mapView = params.get('vista') === 'mapa';
const focusId = Number(params.get('lugar')) || null; // A place to show first, from its own page.
let mapState = null; // { L, map, cluster, markers }, once the map has been made.
let mapStarting = null;
let focused = false;

async function startMap() {
  mapStarting ??= createMap(document.getElementById('places-map')).then(({ L, map }) => {
    const cluster = L.markerClusterGroup({ showCoverageOnHover: false, maxClusterRadius: 45 });
    map.addLayer(cluster);
    // A click where there is no place offers to create one there.
    offerOnMapClick(L, map, {
      label: 'Crear un lugar aquí',
      open: (coordinates) =>
        openPlaceDialog({
          types,
          subtypes,
          systems,
          geologies,
          coordinates,
          onSaved: (place) => {
            window.location.href = `/lugar?id=${place.id}`;
          },
        }),
    });
    mapState = { L, map, cluster, markers: new Map() };
    return mapState;
  });
  return mapStarting;
}

async function renderMap(shown) {
  const note = document.getElementById('map-note');
  let state;
  try {
    state = await startMap();
  } catch (err) {
    mapStarting = null;
    note.textContent = err.message;
    return;
  }
  const { L, map, cluster, markers } = state;
  cluster.clearLayers();
  markers.clear();
  for (const place of shown) {
    const marker = placeMarker(L, place).bindPopup(placePopup(place));
    marker.bindTooltip(place.name);
    markers.set(place.id, marker);
  }
  cluster.addLayers([...markers.values()]);
  note.textContent = allPlaces.length
    ? `${shown.length} de ${allPlaces.length} lugares en el mapa.`
    : 'Todavía no hay ningún lugar.';
  const target = !focused && focusId ? markers.get(focusId) : null;
  focused = true;
  if (target) {
    // Close enough for it not to be inside a group; the group draws its markers once the view moved.
    map.once('moveend', () => target.openPopup());
    map.setView(target.getLatLng(), 16, { animate: false });
  } else if (shown.length) {
    map.fitBounds(L.latLngBounds(shown.map((p) => [p.lat, p.lon])), {
      padding: [30, 30],
      maxZoom: 15,
    });
  }
}

function applyView(map) {
  mapView = map;
  document.getElementById('map-view').hidden = !map;
  document.getElementById('list-view').hidden = map;
  document.getElementById('view-list').setAttribute('aria-pressed', String(!map));
  document.getElementById('view-map').setAttribute('aria-pressed', String(map));
}

function showView(map) {
  applyView(map);
  const url = new URL(window.location.href);
  if (map) url.searchParams.set('vista', 'mapa');
  else url.searchParams.delete('vista');
  url.searchParams.delete('lugar');
  window.history.replaceState(null, '', url);
  if (map) {
    renderPlaces();
    mapState?.map.invalidateSize(); // It was hidden, so it did not know its size.
  }
}

document.getElementById('map-legend').innerHTML = mapLegend();
document.getElementById('view-list').addEventListener('click', () => showView(false));
document.getElementById('view-map').addEventListener('click', () => showView(true));

// The subtype only makes sense for the cavities, and the state only for the types that have one (or
// for all of them): the filter that does not apply is hidden, and forgotten, so that it never filters
// without being seen.
function syncFilters() {
  const type = types.find((t) => t.id === Number(typeFilter.value));
  const showSubtype = type?.kind === 'cave';
  const showState = !type || type.has_state;
  document.getElementById('subtype-filter-field').hidden = !showSubtype;
  document.getElementById('state-filter-field').hidden = !showState;
  if (!showSubtype) subtypeFilter.value = '';
  if (!showState) stateFilter.value = '';
}

function renderPlaces() {
  const words = normalizeForSearch(searchInput.value).split(/\s+/).filter(Boolean);
  const shown = allPlaces.filter(
    (place) =>
      (!typeFilter.value || place.type_id === Number(typeFilter.value)) &&
      (!stateFilter.value || place.state === stateFilter.value) &&
      (!subtypeFilter.value || place.subtype_id === Number(subtypeFilter.value)) &&
      words.every((word) => searchText(place).includes(word)),
  );
  tableBody.innerHTML = shown.length
    ? shown.map(renderPlace).join('')
    : `<tr><td colspan="5">${allPlaces.length ? 'Ningún lugar coincide con la búsqueda.' : 'Todavía no hay ningún lugar.'}</td></tr>`;
  countLine.hidden = allPlaces.length === 0;
  countLine.textContent = `${shown.length} de ${allPlaces.length} lugares`;
  if (mapView) renderMap(shown);
}

// The options of a filter, keeping what was chosen if it is still there.
function setOptions(select, all, list) {
  const chosen = select.value;
  select.innerHTML = `<option value="">${all}</option>${list
    .map((item) => `<option value="${item.id}">${escapeHtml(item.name)}</option>`)
    .join('')}`;
  if (list.some((item) => String(item.id) === chosen)) select.value = chosen;
}

// The rows of the table of a catalogue (the types, the geologies): its name to change, how many places
// have it, and the buttons. `fixed` says which items cannot be touched.
function catalogueRows(items, { fixed = () => false, note = () => '', noun }) {
  return items
    .map(
      (item) => `
      <tr>
        <td><div class="field"><input type="text" value="${escapeHtml(item.name)}" data-name="${item.id}" aria-label="Nombre ${noun}"${fixed(item) ? ' disabled' : ''}></div></td>
        <td class="nowrap">${item.place_count}${
          note(item) ? `<br><span class="subtitle">${escapeHtml(note(item))}</span>` : ''
        }</td>
        <td class="nowrap">${
          fixed(item)
            ? '<span class="subtitle">Fijo</span>'
            : `<button type="button" class="btn btn-secondary btn-small" data-save="${item.id}">Guardar</button>
          <button type="button" class="btn btn-danger btn-small" data-delete="${item.id}"${item.place_count ? ' disabled title="Tiene lugares"' : ''}>Borrar</button>`
        }</td>
      </tr>`,
    )
    .join('');
}

function renderCatalogues() {
  document.getElementById('types-table-body').innerHTML = catalogueRows(types, {
    fixed: (t) => t.kind !== 'general',
    note: (t) => (t.has_state ? 'Con estado' : 'Sin estado'),
    noun: 'del tipo',
  });
  document.getElementById('subtypes-table-body').innerHTML = catalogueRows(subtypes, {
    noun: 'del subtipo',
  });
  document.getElementById('geologies-table-body').innerHTML = catalogueRows(geologies, {
    noun: 'de la geología',
  });
}

async function load() {
  try {
    const [places, loadedTypes, loadedSubtypes, loadedSystems, loadedGeologies] = await Promise.all(
      [apiFetch('/places'), loadTypes(), loadSubtypes(), loadSystems(), loadGeologies()],
    );
    allPlaces = places;
    types = loadedTypes;
    subtypes = loadedSubtypes;
    systems = loadedSystems;
    geologies = loadedGeologies;
    setOptions(typeFilter, 'Todos', types);
    setOptions(subtypeFilter, 'Todos', subtypes);
    syncFilters();
    if (isAdmin) renderCatalogues();
    renderPlaces();
  } catch (err) {
    tableBody.innerHTML = '<tr><td colspan="5">No se pudieron cargar los lugares.</td></tr>';
    showError(err.message);
  }
}

searchInput.addEventListener('input', renderPlaces);
typeFilter.addEventListener('change', () => {
  syncFilters();
  renderPlaces();
});
stateFilter.addEventListener('change', renderPlaces);
subtypeFilter.addEventListener('change', renderPlaces);

// Any member can add a place; it goes straight to its page, where the rest is filled in.
document.getElementById('toggle-new-place').addEventListener('click', () => {
  openPlaceDialog({
    types,
    subtypes,
    systems,
    geologies,
    onSaved: (place) => {
      window.location.href = `/lugar?id=${place.id}`;
    },
  });
});

// Makes the table of a catalogue and its form work: rename and delete (with a confirmation), and add
// one. After each change everything is loaded again and the dialog stays open. `path` is that of the
// API ("/places/types") and `words` the texts of the messages.
function bindCatalogue({ tbody, form, path, words }) {
  async function change(action, feedback) {
    try {
      await action();
      showFeedback(feedback);
      await load();
    } catch (err) {
      showError(err.message);
    }
  }
  tbody.addEventListener('click', (event) => {
    const save = event.target.closest('button[data-save]');
    if (save) {
      const id = save.dataset.save;
      const name = tbody.querySelector(`input[data-name="${id}"]`).value;
      change(
        () => apiFetch(`${path}/${id}`, { method: 'PUT', body: JSON.stringify({ name }) }),
        words.saved,
      );
      return;
    }
    const del = event.target.closest('button[data-delete]');
    if (del && confirm(words.confirmDelete)) {
      change(() => apiFetch(`${path}/${del.dataset.delete}`, { method: 'DELETE' }), words.deleted);
    }
  });
  form.addEventListener('submit', (event) => {
    event.preventDefault();
    change(async () => {
      await apiFetch(path, {
        method: 'POST',
        body: JSON.stringify({
          name: form.elements.name.value,
          // Only the form of a new type has it: whether its places have a state.
          ...(form.elements.has_state ? { has_state: form.elements.has_state.checked } : {}),
        }),
      });
      form.reset();
    }, words.added);
  });
}

if (isAdmin) {
  initAreasManager(document.getElementById('manage-areas'));
  const dialog = document.getElementById('types-dialog');
  const typeForm = document.getElementById('new-type-form');
  const subtypeForm = document.getElementById('new-subtype-form');
  const geologyForm = document.getElementById('new-geology-form');
  initModal(dialog, typeForm);
  dialog.addEventListener('close', () => {
    subtypeForm.reset();
    geologyForm.reset();
  });
  document.getElementById('manage-types').addEventListener('click', () => openModal(dialog));
  bindCatalogue({
    tbody: document.getElementById('types-table-body'),
    form: typeForm,
    path: '/places/types',
    words: {
      saved: 'Tipo guardado.',
      added: 'Tipo añadido.',
      deleted: 'Tipo borrado.',
      confirmDelete: '¿Borrar este tipo?',
    },
  });
  bindCatalogue({
    tbody: document.getElementById('subtypes-table-body'),
    form: subtypeForm,
    path: '/places/subtypes',
    words: {
      saved: 'Subtipo guardado.',
      added: 'Subtipo añadido.',
      deleted: 'Subtipo borrado.',
      confirmDelete: '¿Borrar este subtipo?',
    },
  });
  bindCatalogue({
    tbody: document.getElementById('geologies-table-body'),
    form: geologyForm,
    path: '/places/geologies',
    words: {
      saved: 'Geología guardada.',
      added: 'Geología añadida.',
      deleted: 'Geología borrada.',
      confirmDelete: '¿Borrar esta geología?',
    },
  });
}

applyView(mapView);
await load();
