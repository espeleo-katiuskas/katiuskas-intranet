import {
  activityState,
  activityStateLabels,
  apiFetch,
  hasAnyRole,
  escapeHtml,
  formatDate,
  formatDateTime,
  isNotFound,
  isOffline,
  queryId,
  showError,
  showFeedback,
  showNotFound,
  stopAsNotFound,
  typeLabels,
} from './api.js';
import { initPage } from './layout.js';
import { bindFiles, fileRowsHtml } from './place-files.js';
import { copyFromOsm, openOsmLinkDialog, osmChangesHtml, osmDataHtml } from './osm-link.js';
import { describeFields } from './place-body.js';
import { openPlaceDialog, openPointDialog } from './place-dialog.js';
import { openQuickEdit } from './place-quick-edit.js';
import { bindPendingList } from './pending-list.js';
import {
  coordinatesHtml,
  loadGeologies,
  loadSubtypes,
  loadSystems,
  loadTypes,
  placeTypeLabel,
  POINT_KIND_LABELS,
  STATE_HELP,
  stateBadge,
} from './places-common.js';
import { createMap, offerOnMapClick, placeMarker, placePopup, pointMarker } from './map-common.js';
import { renderRichText } from './rich-text.js';

const user = await initPage();
const placeId = queryId();
if (placeId === null) await stopAsNotFound();

let place = null;
let types = [];
let subtypes = [];
let systems = [];
let geologies = [];
let names = {};
let files = [];
let system = null; // The system the cavity is in, with its cavities, or null.
let systemFiles = [];
let activities = [];
// Admin and board remove any file; anybody else only their own.
const canManageFiles = hasAnyRole(user, ['admin', 'board']);

// The names of the people (from the directory, open to every signed-in user).
async function loadNames() {
  try {
    const directory = await apiFetch('/users/directory');
    return Object.fromEntries(directory.map((p) => [p.login, `${p.first_name} ${p.last_name}`]));
  } catch {
    return {}; // Without names the logins show instead.
  }
}

function personLink(login) {
  return `<a href="/socio?login=${encodeURIComponent(login)}">${escapeHtml(names[login] ?? login)}</a>`;
}

function dateRange(activity) {
  return activity.start_date === activity.end_date
    ? formatDate(activity.start_date)
    : `${formatDate(activity.start_date)} – ${formatDate(activity.end_date)}`;
}

// What has been done here, and is to be: the activities that go to the place, the latest first.
function renderActivities() {
  document.getElementById('place-activities-body').innerHTML = activities.length
    ? activities
        .map((a) => {
          const state = activityState(a);
          return `
      <tr>
        <td class="nowrap">${dateRange(a)}</td>
        <td><a href="/actividad?id=${a.id}"><strong>${escapeHtml(a.title)}</strong></a>
          <span class="badge status-${state}">${escapeHtml(activityStateLabels[state] ?? state)}</span></td>
        <td>${escapeHtml(typeLabels[a.type] ?? a.type)}</td>
        <td>${personLink(a.leader_login)}</td>
      </tr>`;
        })
        .join('')
    : '<tr><td colspan="4">Todavía no se ha hecho ninguna actividad aquí.</td></tr>';
}

// The system this cavity is in: a line that links to it, its description and links to the other cavities.
function renderSystem() {
  const section = document.getElementById('system-section');
  section.hidden = !system;
  if (!system) return;
  document.getElementById('system-intro').innerHTML =
    `Esta cavidad forma parte del sistema <a href="/sistema?id=${system.id}"><strong>${escapeHtml(system.name)}</strong></a>.`;
  document.getElementById('system-description').innerHTML = system.description
    ? renderRichText(system.description)
    : '';
  const others = system.places.filter((p) => p.id !== place.id);
  document.getElementById('system-places').innerHTML = others.length
    ? others
        .map(
          (p) =>
            `<li><a href="/lugar?id=${p.id}">${escapeHtml(p.name)}</a> <span class="subtitle">${escapeHtml(placeTypeLabel(p))}${p.where ? ` · ${escapeHtml(p.where)}` : ''}</span></li>`,
        )
        .join('')
    : '<li class="subtitle">Todavía es la única cavidad del sistema.</li>';
}

// The photos and documents of the system, among those of the cavity, after its own (they are changed on
// the page of the system); with the system, the two lists say whose they are.
function renderSystemFiles() {
  const block = document.getElementById('system-files-block');
  block.hidden = !system;
  document.getElementById('own-files-title').hidden = !system;
  document.getElementById('own-form-title').hidden = !system;
  if (!system) return;
  document.getElementById('system-files-title').innerHTML =
    `Del sistema <a href="/sistema?id=${system.id}">${escapeHtml(system.name)}</a>`;
  document.getElementById('system-files').innerHTML = fileRowsHtml(systemFiles, {
    base: `/systems/${system.id}`,
    personLink,
  });
}

function renderFiles() {
  document.getElementById('place-files').innerHTML = fileRowsHtml(files, {
    base: `/places/${placeId}`,
    canRemove: (f) => f.uploaded_by === user.login || canManageFiles,
    personLink,
  });
}

// The small map: the main coordinate, in the colour of the state, and the other points in grey, each
// with its kind and name when pointed at. It is made once and drawn again after every change.
let placeMap = null;

async function renderMap() {
  document.getElementById('place-map-link').href = `/lugares?vista=mapa&lugar=${place.id}`;
  try {
    if (!placeMap) {
      placeMap = await createMap(document.getElementById('place-map'));
      // Where nothing is, a click offers to add a point of this place there (once, not at each drawing).
      offerOnMapClick(placeMap.L, placeMap.map, {
        label: 'Añadir un punto aquí',
        open: (coordinates) =>
          openPointDialog({
            placeId,
            coordinates,
            onSaved: async () => {
              showFeedback('Punto añadido.');
              await load();
            },
          }),
      });
    }
  } catch {
    document.getElementById('place-map').outerHTML =
      '<p class="subtitle">No se pudo cargar el mapa.</p>';
    return;
  }
  const { L, map } = placeMap;
  placeMap.group?.remove();
  const group = L.featureGroup().addTo(map);
  placeMap.group = group;
  placeMarker(L, place).bindPopup(placePopup(place)).addTo(group);
  for (const point of place.points) pointMarker(L, point).addTo(group);
  map.fitBounds(group.getBounds(), { padding: [30, 30], maxZoom: 16 });
}

// What the page that guides to a point calls it: its own name, or what it is, and the place it belongs to.
function pointGuideName(point) {
  return `${point.name || (POINT_KIND_LABELS[point.kind] ?? point.kind)} — ${place.name}`;
}

const OSM_TYPE_NAMES = { node: 'nodo', way: 'vía', relation: 'relación' };

// Whether the place is an element of OpenStreetMap, and the buttons to say it is or that it is not.
function renderOsm() {
  const linked = place.osm_type && place.osm_id;
  document.getElementById('osm-status').textContent = linked
    ? `Este lugar está vinculado con un elemento de OpenStreetMap (${OSM_TYPE_NAMES[place.osm_type] ?? place.osm_type} ${place.osm_id}).`
    : 'Este lugar no está vinculado con ningún elemento de OpenStreetMap. Vincularlo evita que su punto salga en el mapa como un candidato más.';
  document.getElementById('osm-link').textContent = linked
    ? 'Vincular con otro elemento'
    : 'Vincular con OpenStreetMap';
  document.getElementById('osm-unlink').hidden = !linked;
  document.getElementById('osm-changes').innerHTML = osmChangesHtml(place);
  loadOsmData(linked ? `${place.osm_type}/${place.osm_id}` : null);
}

let osmShown = null; // The element whose data is on the page, as "type/id"...
let osmElement = null; // ...and what the mirror says of it.

// What OpenStreetMap says of the element, next to the place: asked of the mirror once for each element, and
// drawn again whenever the place is (an edit of the place changes what does not agree with it).
async function loadOsmData(key) {
  const box = document.getElementById('osm-data');
  if (key === null) {
    osmShown = osmElement = null;
    box.innerHTML = '';
    return;
  }
  if (key === osmShown && osmElement) {
    box.innerHTML = osmDataHtml(place, osmElement);
    return;
  }
  osmShown = key;
  osmElement = null;
  box.innerHTML = '';
  try {
    const element = await apiFetch(`/osm/elements/${key}`);
    if (osmShown !== key) return;
    osmElement = element;
    box.innerHTML = osmDataHtml(place, element);
  } catch (err) {
    if (osmShown === key) box.textContent = err.message;
  }
}

function render() {
  renderOsm();
  document.title = `${place.name} — Club de Espeleología Katiuskas`;
  document.getElementById('place-name').textContent = place.name;
  document.getElementById('place-meta').textContent = [place.type_name, place.subtype, place.where]
    .filter(Boolean)
    .join(' · ');
  // A place whose type has no state does not say one.
  const stateLine = document.getElementById('place-state');
  stateLine.hidden = !place.state;
  stateLine.innerHTML = place.state
    ? `${stateBadge(place.state)} <span class="subtitle">${escapeHtml(STATE_HELP[place.state] ?? '')}</span>`
    : '';

  const rows = [
    ['Otros nombres', place.alt_names.length ? escapeHtml(place.alt_names.join(' · ')) : null],
    [
      'Coordenadas',
      coordinatesHtml(place.lat, place.lon, { guide: { name: place.name, placeId: place.id } }),
    ],
    ['Altitud', place.altitude_m != null ? `${place.altitude_m} m sobre el nivel del mar` : null],
    ['Geología', place.geology ? escapeHtml(place.geology) : null],
    ['Profundidad', place.depth_m != null ? `${place.depth_m} m` : null],
    [
      'Desarrollo horizontal',
      place.horizontal_length_m != null ? `${place.horizontal_length_m} m` : null,
    ],
  ].filter(([, value]) => value);
  document.getElementById('place-details').innerHTML = rows
    .map(
      ([label, html]) =>
        `<div class="detail-row"><span class="subtitle">${label}</span><span>${html}</span></div>`,
    )
    .join('');

  const text = (id, value, empty) => {
    document.getElementById(id).innerHTML = value
      ? renderRichText(value)
      : `<p class="subtitle">${empty}</p>`;
  };
  text('place-access', place.access, 'Todavía no se ha explicado cómo llegar.');
  text('place-description', place.description, 'Todavía no hay descripción.');
  // Only a cave or a canyon has a technical sheet.
  const hasSheet = place.kind === 'cave' || place.kind === 'canyon';
  document.getElementById('sheet-section').hidden = !hasSheet;
  if (hasSheet) text('place-sheet', place.technical_sheet, 'Todavía no hay ficha técnica.');

  document.getElementById('points-table-body').innerHTML = place.points.length
    ? place.points
        .map(
          (point) => `
      <tr>
        <td>${escapeHtml(POINT_KIND_LABELS[point.kind] ?? point.kind)}</td>
        <td>${point.name ? escapeHtml(point.name) : '—'}</td>
        <td>${coordinatesHtml(point.lat, point.lon, {
          altitude: point.altitude_m,
          guide: { name: pointGuideName(point), placeId: place.id },
        })}</td>
        <td>${point.note ? escapeHtml(point.note) : ''}</td>
        <td class="nowrap">${
          isOffline()
            ? ''
            : `
          <button type="button" class="btn btn-secondary btn-small" data-edit-point="${point.id}">Editar</button>
          <button type="button" class="btn btn-danger btn-small" data-delete-point="${point.id}">Quitar</button>`
        }
        </td>
      </tr>`,
        )
        .join('')
    : '<tr><td colspan="5">Todavía no hay más puntos que el principal.</td></tr>';

  const created = `Añadido por ${personLink(place.created_by)} el ${formatDateTime(place.created_at)}.`;
  const changed =
    place.updated_at !== place.created_at
      ? ` Último cambio de ${personLink(place.updated_by)} el ${formatDateTime(place.updated_at)}.`
      : '';
  document.getElementById('place-authorship').innerHTML = created + changed;
  renderFiles();
  renderSystem();
  renderSystemFiles();
  renderActivities();
  renderMap();
}

// With no connection and a place that was not opened lately, the page is made of what the list of places
// has (that is kept always): the name, type, state, where, coordinates and measures. What only the page
// of the place has (how to get there, the description, the points, the files, the activities) is not
// there, and the page says so. The full edit of the place is not offered (it would clear the fields that
// are not here); the short one (name, coordinate, altitude) is, and so are a point or a file, which only add.
async function loadPartial() {
  try {
    const item = (await apiFetch('/places')).find((p) => p.id === placeId);
    if (!item) return false;
    place = {
      ...item,
      alt_names: item.alt_names ?? [],
      access: null,
      description: null,
      technical_sheet: null,
      points: [],
    };
    system = null;
    systemFiles = [];
    files = [];
    activities = [];
    render();
    const missing = '<p class="subtitle">No se guarda en este móvil para verlo sin conexión.</p>';
    for (const id of ['place-access', 'place-description', 'place-sheet', 'place-files']) {
      document.getElementById(id).innerHTML = missing;
    }
    document.getElementById('points-table-body').innerHTML =
      '<tr><td colspan="5">No se guardan en este móvil para verlos sin conexión.</td></tr>';
    document.getElementById('place-activities-body').innerHTML =
      '<tr><td colspan="4">No se guardan en este móvil para verlas sin conexión.</td></tr>';
    document.getElementById('place-authorship').textContent = '';
    document.getElementById('partial-note').hidden = false;
    document.getElementById('edit-place').hidden = true;
    document.getElementById('quick-edit-place').hidden = false;
    return true;
  } catch {
    return false;
  }
}

async function load() {
  document.getElementById('partial-note').hidden = true;
  document.getElementById('edit-place').hidden = false;
  document.getElementById('quick-edit-place').hidden = true;
  try {
    const [
      loaded,
      loadedFiles,
      loadedActivities,
      loadedTypes,
      loadedSubtypes,
      loadedSystems,
      loadedGeologies,
      loadedNames,
    ] = await Promise.all([
      apiFetch(`/places/${placeId}`),
      apiFetch(`/places/${placeId}/files`),
      apiFetch(`/places/${placeId}/activities`),
      loadTypes(),
      loadSubtypes(),
      loadSystems(),
      loadGeologies(),
      loadNames(),
    ]);
    place = loaded;
    // The system it is in, with its files: its description and documents are shown here too.
    [system, systemFiles] = loaded.system_id
      ? await Promise.all([
          apiFetch(`/systems/${loaded.system_id}`),
          apiFetch(`/systems/${loaded.system_id}/files`),
        ])
      : [null, []];
    files = loadedFiles;
    activities = loadedActivities;
    types = loadedTypes;
    subtypes = loadedSubtypes;
    systems = loadedSystems;
    geologies = loadedGeologies;
    names = loadedNames;
    render();
  } catch (err) {
    if (isNotFound(err)) {
      showNotFound();
      return;
    }
    // With no connection, a place that has not been opened lately is not on the phone.
    if (err.network && isOffline()) {
      if (await loadPartial()) return;
      showError(
        'Este lugar no está guardado en este móvil para verlo sin conexión. Desde «Lugares» puedes ponerle rumbo igualmente.',
      );
      return;
    }
    showError(err.message);
  }
}

// The OpenStreetMap section (the link, what it says of the element and the buttons to copy it) lives inside
// the dialog "Editar lugar": it is moved there while the dialog is open and back, hidden, when it closes.
const osmSection = document.getElementById('osm-section');
const osmHome = osmSection.parentElement;
const editDialog = () => document.getElementById('place-dialog');

// What was done with no connection and waits to be sent (js/outbox.js): the changes to this place and
// the points added to it.
const renderPending = bindPendingList({
  container: document.getElementById('place-pending'),
  login: user.login,
  matches: (e) => e.kind.startsWith('place-') && e.placeId === placeId,
  describe: (e) =>
    e.kind === 'place-edit'
      ? `Cambios en el lugar: ${describeFields(Object.keys(e.changes))}`
      : e.kind === 'place-file'
        ? `${e.file.type.startsWith('image/') ? 'Foto' : 'Fichero'}: ${e.title || e.fileName}`
        : `Punto nuevo: ${POINT_KIND_LABELS[e.point.kind] ?? e.point.kind}${e.point.name ? ` «${e.point.name}»` : ''}`,
  reload: load,
});
const queued = () => renderPending();

// The short edit of the partial page (see loadPartial): the name, the coordinate and the altitude.
document.getElementById('quick-edit-place').addEventListener('click', () => {
  openQuickEdit({ place, onQueued: queued });
});

document.getElementById('edit-place').addEventListener('click', () => {
  openPlaceDialog({
    types,
    subtypes,
    systems,
    geologies,
    place,
    onSaved: async () => {
      showFeedback('Lugar guardado.');
      await load();
    },
    onQueued: queued,
  });
  // Linking to OpenStreetMap needs the connection (and its mirror).
  if (isOffline()) return;
  document.getElementById('pd-osm-slot').append(osmSection);
  osmSection.hidden = false;
  editDialog().addEventListener(
    'close',
    () => {
      osmSection.hidden = true;
      osmHome.append(osmSection);
    },
    { once: true },
  );
});

document.getElementById('osm-link').addEventListener('click', () => {
  openOsmLinkDialog({
    place,
    onLinked: async () => {
      editDialog().close(); // Its form would show what the place was before.
      showFeedback('Lugar vinculado con OpenStreetMap.');
      await load();
    },
  });
});

// The buttons of what OpenStreetMap says and of what changed: copy a value into the place, or say that the
// changes have been looked at.
document.getElementById('osm-data').addEventListener('click', async (event) => {
  const button = event.target.closest('button[data-osm-copy]');
  if (!button || !osmElement) return;
  button.disabled = true;
  try {
    await apiFetch(`/places/${placeId}`, {
      method: 'PUT',
      body: JSON.stringify(copyFromOsm(place, osmElement, button.dataset.osmCopy)),
    });
    editDialog().close();
    showFeedback('Lugar actualizado con el dato de OpenStreetMap.');
    await load();
  } catch (err) {
    button.disabled = false;
    showError(err.message);
  }
});

document.getElementById('osm-changes').addEventListener('click', async (event) => {
  const button = event.target.closest('button[data-osm-review]');
  if (!button) return;
  button.disabled = true;
  try {
    await apiFetch(`/places/${placeId}/links/osm/review`, { method: 'POST' });
    showFeedback('Cambios de OpenStreetMap revisados.');
    await load();
  } catch (err) {
    button.disabled = false;
    showError(err.message);
  }
});

document.getElementById('osm-unlink').addEventListener('click', async () => {
  if (!confirm('¿Desvincular este lugar de OpenStreetMap? Su elemento volverá a salir en el mapa.'))
    return;
  try {
    await apiFetch(`/places/${placeId}/links/osm`, { method: 'DELETE' });
    editDialog().close();
    showFeedback('Lugar desvinculado de OpenStreetMap.');
    await load();
  } catch (err) {
    showError(err.message);
  }
});

document.getElementById('add-point').addEventListener('click', () => {
  openPointDialog({
    placeId,
    onSaved: async () => {
      showFeedback('Punto añadido.');
      await load();
    },
    onQueued: queued,
  });
});

document.getElementById('points-table-body').addEventListener('click', async (event) => {
  const edit = event.target.closest('button[data-edit-point]');
  if (edit) {
    const point = place.points.find((p) => p.id === Number(edit.dataset.editPoint));
    openPointDialog({
      placeId,
      point,
      onSaved: async () => {
        showFeedback('Punto guardado.');
        await load();
      },
    });
    return;
  }
  const del = event.target.closest('button[data-delete-point]');
  if (del && confirm('¿Quitar este punto?')) {
    try {
      await apiFetch(`/places/${placeId}/points/${del.dataset.deletePoint}`, { method: 'DELETE' });
      showFeedback('Punto quitado.');
      await load();
    } catch (err) {
      showError(err.message);
    }
  }
});

bindFiles({
  list: document.getElementById('place-files'),
  form: document.getElementById('file-form'),
  base: `/places/${placeId}`,
  reload: load,
  queue: { login: user.login, placeId, onQueued: queued },
});

// Only admin and board have the button (data-roles removes it for the rest).
document.getElementById('delete-place')?.addEventListener('click', async () => {
  if (!confirm(`¿Borrar «${place.name}», con sus puntos y sus ficheros?`)) return;
  try {
    await apiFetch(`/places/${placeId}`, { method: 'DELETE' });
    window.location.href = '/lugares';
  } catch (err) {
    showError(err.message);
  }
});

if (isOffline()) {
  // What needs the connection is not offered.
  document.getElementById('delete-place')?.setAttribute('hidden', '');
}
await Promise.all([load(), renderPending()]);
