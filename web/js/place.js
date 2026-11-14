import {
  apiFetch,
  escapeHtml,
  formatDateTime,
  isNotFound,
  queryId,
  showError,
  showFeedback,
  showNotFound,
  stopAsNotFound,
} from './api.js';
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
import { renderRichText } from './rich-text.js';

await initPage();
const placeId = queryId();
if (placeId === null) await stopAsNotFound();

let place = null;
let types = [];
let geologies = [];
let names = {};

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
}

async function load() {
  try {
    const [loaded, loadedTypes, loadedGeologies, loadedNames] = await Promise.all([
      apiFetch(`/places/${placeId}`),
      loadTypes(),
      loadGeologies(),
      loadNames(),
    ]);
    place = loaded;
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

// Only admin and board have the button (data-roles removes it for the rest).
document.getElementById('delete-place')?.addEventListener('click', async () => {
  if (!confirm(`¿Borrar «${place.name}», con sus puntos?`)) return;
  try {
    await apiFetch(`/places/${placeId}`, { method: 'DELETE' });
    window.location.href = '/lugares';
  } catch (err) {
    showError(err.message);
  }
});

await load();
