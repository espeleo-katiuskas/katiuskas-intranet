import {
  API_BASE,
  apiFetch,
  apiFetchForm,
  hasAnyRole,
  escapeHtml,
  formatDateTime,
  isNotFound,
  queryId,
  showError,
  showFeedback,
  showNotFound,
  stopAsNotFound,
} from './api.js';
import { formatSize } from './file-browser.js';
import { initPage } from './layout.js';
import { openPlaceDialog, openPointDialog } from './place-dialog.js';
import {
  coordinatesHtml,
  loadGeologies,
  loadTypes,
  POINT_KIND_LABELS,
  STATE_HELP,
  stateBadge,
} from './places-common.js';
import { createMap, offerNewPlace, placeMarker, placePopup } from './map-common.js';
import { renderRichText } from './rich-text.js';

const user = await initPage();
const placeId = queryId();
if (placeId === null) await stopAsNotFound();

let place = null;
let types = [];
let geologies = [];
let names = {};
let files = [];
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

function fileUrl(file, download = false) {
  return `${API_BASE}/places/${placeId}/files/${file.id}/file${download ? '?download=1' : ''}`;
}

function renderFiles() {
  document.getElementById('place-files').innerHTML = files.length
    ? files
        .map((f) => {
          const label = f.title || f.file_name;
          const preview = f.content_type.startsWith('image/')
            ? `<a href="${fileUrl(f)}" target="_blank" rel="noopener"><img src="${fileUrl(f)}" alt="${escapeHtml(label)}" class="resource-image" loading="lazy"></a>`
            : '';
          const removable = f.uploaded_by === user.login || canManageFiles;
          return `
        <div class="comment">
          <div class="comment-head">
            <span><a href="${fileUrl(f)}" target="_blank" rel="noopener"><strong>${escapeHtml(label)}</strong></a>
              <span class="subtitle">${escapeHtml(f.file_name)} · ${formatSize(f.file_size)} · ${personLink(f.uploaded_by)}, ${formatDateTime(f.created_at)}</span></span>
            <span class="nowrap">
              <a class="btn btn-secondary btn-small" href="${fileUrl(f, true)}">Descargar</a>
              ${removable ? `<button type="button" class="btn btn-secondary btn-small" data-delete-file="${f.id}">Borrar</button>` : ''}
            </span>
          </div>
          ${preview}
        </div>`;
        })
        .join('')
    : '<p>Todavía no hay fotos ni documentos.</p>';
}

// The small map: the main coordinate, in the colour of the state, and the other points in grey, each
// with its kind and name when pointed at. It is made once and drawn again after every change.
let placeMap = null;

async function renderMap() {
  document.getElementById('place-map-link').href = `/lugares?vista=mapa&lugar=${place.id}`;
  try {
    if (!placeMap) {
      placeMap = await createMap(document.getElementById('place-map'));
      // Where nothing is, a click offers to create another place there (once, not at each drawing).
      placeMap.map.on('click', (event) =>
        offerNewPlace(placeMap.L, placeMap.map, event.latlng, { types, geologies }),
      );
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
  for (const point of place.points) {
    L.circleMarker([point.lat, point.lon], {
      bubblingMouseEvents: false,
      radius: 6,
      weight: 2,
      color: '#ffffff',
      fillColor: '#6b6f68',
      fillOpacity: 0.95,
    })
      .bindTooltip(
        [POINT_KIND_LABELS[point.kind] ?? point.kind, point.name].filter(Boolean).join(': '),
      )
      .addTo(group);
  }
  map.fitBounds(group.getBounds(), { padding: [30, 30], maxZoom: 16 });
}

function render() {
  document.title = `${place.name} — Club de Espeleología Katiuskas`;
  document.getElementById('place-name').textContent = place.name;
  document.getElementById('place-meta').textContent = [place.type_name, place.where]
    .filter(Boolean)
    .join(' · ');
  document.getElementById('place-state').innerHTML =
    `${stateBadge(place.state)} <span class="subtitle">${escapeHtml(STATE_HELP[place.state] ?? '')}</span>`;

  const rows = [
    ['Otros nombres', place.alt_names ? escapeHtml(place.alt_names) : null],
    // Where it is: each zone, from the country down, under the name of its level.
    ...place.area_path.map((zone) => [escapeHtml(zone.kind), escapeHtml(zone.name)]),
    ['Coordenadas', coordinatesHtml(place.lat, place.lon)],
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
        <td>${coordinatesHtml(point.lat, point.lon, point.altitude_m)}</td>
        <td>${point.note ? escapeHtml(point.note) : ''}</td>
        <td class="nowrap">
          <button type="button" class="btn btn-secondary btn-small" data-edit-point="${point.id}">Editar</button>
          <button type="button" class="btn btn-danger btn-small" data-delete-point="${point.id}">Quitar</button>
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
  renderMap();
}

async function load() {
  try {
    const [loaded, loadedFiles, loadedTypes, loadedGeologies, loadedNames] = await Promise.all([
      apiFetch(`/places/${placeId}`),
      apiFetch(`/places/${placeId}/files`),
      loadTypes(),
      loadGeologies(),
      loadNames(),
    ]);
    place = loaded;
    files = loadedFiles;
    types = loadedTypes;
    geologies = loadedGeologies;
    names = loadedNames;
    render();
  } catch (err) {
    if (isNotFound(err)) {
      showNotFound();
      return;
    }
    showError(err.message);
  }
}

document.getElementById('edit-place').addEventListener('click', () => {
  openPlaceDialog({
    types,
    geologies,
    place,
    onSaved: async () => {
      showFeedback('Lugar guardado.');
      await load();
    },
  });
});

document.getElementById('add-point').addEventListener('click', () => {
  openPointDialog({
    placeId,
    onSaved: async () => {
      showFeedback('Punto añadido.');
      await load();
    },
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

document.getElementById('file-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const form = event.currentTarget;
  const submit = form.querySelector('button[type="submit"]');
  submit.disabled = true;
  try {
    await apiFetchForm(`/places/${placeId}/files`, new FormData(form));
    form.reset();
    showFeedback('Fichero añadido.');
    await load();
  } catch (err) {
    showError(err.message);
  } finally {
    submit.disabled = false;
  }
});

document.getElementById('place-files').addEventListener('click', async (event) => {
  const button = event.target.closest('button[data-delete-file]');
  if (!button || !confirm('¿Quitar este fichero?')) return;
  try {
    await apiFetch(`/places/${placeId}/files/${button.dataset.deleteFile}`, { method: 'DELETE' });
    showFeedback('Fichero quitado.');
    await load();
  } catch (err) {
    showError(err.message);
  }
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

await load();
