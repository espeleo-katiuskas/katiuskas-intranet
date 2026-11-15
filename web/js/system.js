import {
  apiFetch,
  escapeHtml,
  formatDateTime,
  hasAnyRole,
  isNotFound,
  queryId,
  renderPickerOptions,
  resolvePickerId,
  showError,
  showFeedback,
  showNotFound,
  stopAsNotFound,
} from './api.js';
import { initPage } from './layout.js';
import { createMap, placeMarker, placePopup } from './map-common.js';
import { initModal, openModal } from './modal.js';
import { bindFiles, fileRowsHtml } from './place-files.js';
import { coordinatesHtml, placeTypeLabel, STATE_HELP, stateBadge } from './places-common.js';
import { renderRichText } from './rich-text.js';

// The page of a system of cavities: its name and description, all its cavities with everything about
// each, a map of them and the photos and documents of the system. Any member changes it.

const user = await initPage();
const systemId = queryId();
if (systemId === null) await stopAsNotFound();

let system = null;
let files = [];
let names = {};
let cavityEntries = []; // The catalogue's cavities as the picker offers them: { id, label, name, system }.
// Admin and board remove any file; anybody else only their own.
const canManageFiles = hasAnyRole(user, ['admin', 'board']);

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

const detailRow = (label, html) =>
  html
    ? `<div class="detail-row"><span class="subtitle">${label}</span><span>${html}</span></div>`
    : '';

// One cavity of the system with everything the API gives of it: what it is, where, its coordinate and
// measures, its other names and its texts, and "Quitar" to take it out of the system.
function cavityCard(p) {
  const rows = [
    detailRow('Dónde', escapeHtml(p.where)),
    detailRow('Coordenadas', coordinatesHtml(p.lat, p.lon)),
    detailRow('Altitud', p.altitude_m != null ? `${p.altitude_m} m sobre el nivel del mar` : ''),
    detailRow('Geología', p.geology ? escapeHtml(p.geology) : ''),
    detailRow('Profundidad', p.depth_m != null ? `${p.depth_m} m` : ''),
    detailRow(
      'Desarrollo horizontal',
      p.horizontal_length_m != null ? `${p.horizontal_length_m} m` : '',
    ),
  ].join('');
  const text = (title, value) =>
    value ? `<h4>${title}</h4><div class="rich-text">${renderRichText(value)}</div>` : '';
  return `
    <article class="panel system-cavity">
      <div class="comment-head">
        <h3><a href="/lugar?id=${p.id}">${escapeHtml(p.name)}</a>
          <span class="badge">${escapeHtml(placeTypeLabel(p))}</span>
          ${stateBadge(p.state)}</h3>
        <button type="button" class="btn btn-secondary btn-small" data-remove-cavity="${p.id}">Quitar</button>
      </div>
      ${p.alt_names.length ? `<p class="subtitle">También: ${escapeHtml(p.alt_names.join(' · '))}</p>` : ''}
      ${p.state ? `<p class="subtitle">${escapeHtml(STATE_HELP[p.state] ?? '')}</p>` : ''}
      <div class="detail-list">${rows}</div>
      ${text('Cómo llegar', p.access)}
      ${text('Descripción', p.description)}
      ${text('Ficha técnica', p.technical_sheet)}
    </article>`;
}

let map = null;

async function renderMap() {
  const element = document.getElementById('system-map');
  if (!system.places.length) {
    element.hidden = true;
    return;
  }
  element.hidden = false;
  try {
    map ??= await createMap(element);
  } catch {
    element.outerHTML = '<p class="subtitle">No se pudo cargar el mapa.</p>';
    return;
  }
  const { L } = map;
  map.group?.remove();
  map.group = L.featureGroup().addTo(map.map);
  for (const p of system.places) {
    placeMarker(L, p).bindPopup(placePopup(p)).bindTooltip(p.name).addTo(map.group);
  }
  map.map.fitBounds(map.group.getBounds(), { padding: [30, 30], maxZoom: 16 });
}

function render() {
  document.title = `${system.name} — Club de Espeleología Katiuskas`;
  document.getElementById('system-name').textContent = system.name;
  const n = system.places.length;
  document.getElementById('system-meta').textContent =
    n === 1 ? 'Sistema de 1 cavidad' : `Sistema de ${n} cavidades`;
  document.getElementById('system-description').innerHTML = system.description
    ? renderRichText(system.description)
    : '<p class="subtitle">Todavía no hay descripción.</p>';
  document.getElementById('system-places').innerHTML = n
    ? system.places.map(cavityCard).join('')
    : '<p>Todavía no tiene ninguna cavidad.</p>';
  document.getElementById('system-files').innerHTML = fileRowsHtml(files, {
    base: `/systems/${systemId}`,
    canRemove: (f) => f.uploaded_by === user.login || canManageFiles,
    personLink,
  });
  const created = `Creado por ${personLink(system.created_by)} el ${formatDateTime(system.created_at)}.`;
  const changed =
    system.updated_at !== system.created_at
      ? ` Último cambio de ${personLink(system.updated_by)} el ${formatDateTime(system.updated_at)}.`
      : '';
  document.getElementById('system-authorship').innerHTML = created + changed;
  const here = new Set(system.places.map((p) => p.id));
  document.getElementById('cavity-options').innerHTML = renderPickerOptions(
    cavityEntries.filter((entry) => !here.has(entry.id)),
  );
  renderMap();
}

async function load() {
  try {
    const [loaded, loadedFiles, loadedNames, places] = await Promise.all([
      apiFetch(`/systems/${systemId}`),
      apiFetch(`/systems/${systemId}/files`),
      loadNames(),
      apiFetch('/places'),
    ]);
    system = loaded;
    files = loadedFiles;
    names = loadedNames;
    // Only a cavity can be in a system; one that is in another says so, since adding it moves it.
    cavityEntries = places
      .filter((p) => p.kind === 'cave')
      .map((p) => ({
        id: p.id,
        name: p.name,
        system: p.system_id && p.system_id !== system.id ? p.system : null,
        label: `${p.name} — ${placeTypeLabel(p)} (${p.where})${
          p.system_id && p.system_id !== system.id ? ` · en «${p.system}»` : ''
        }`,
      }));
    render();
  } catch (err) {
    if (isNotFound(err)) {
      showNotFound();
      return;
    }
    showError(err.message);
  }
}

// The cavities as they are to be, sent whole.
async function saveCavities(ids, feedback) {
  try {
    await apiFetch(`/systems/${systemId}/places`, {
      method: 'PUT',
      body: JSON.stringify({ place_ids: ids }),
    });
    showFeedback(feedback);
  } catch (err) {
    showError(err.message);
  }
  await load();
}

document.getElementById('add-cavity').addEventListener('click', async () => {
  const input = document.getElementById('cavity-search');
  const here = new Set(system.places.map((p) => p.id));
  const entries = cavityEntries.filter((entry) => !here.has(entry.id));
  const id = resolvePickerId(entries, input.value);
  if (!id) {
    showError('Escribe el nombre de una cavidad de la lista.');
    return;
  }
  const entry = entries.find((e) => e.id === id);
  if (
    entry.system &&
    !confirm(`«${entry.name}» está en el sistema «${entry.system}»: pasará a este. ¿Seguir?`)
  ) {
    return;
  }
  input.value = '';
  await saveCavities([...here, id], 'Cavidad añadida.');
});

document.getElementById('system-places').addEventListener('click', async (event) => {
  const button = event.target.closest('button[data-remove-cavity]');
  if (!button) return;
  const removed = Number(button.dataset.removeCavity);
  await saveCavities(
    system.places.map((p) => p.id).filter((id) => id !== removed),
    'Cavidad quitada del sistema.',
  );
});

const editDialog = document.getElementById('edit-system-dialog');
const editForm = document.getElementById('edit-system-form');
initModal(editDialog, editForm);
document.getElementById('edit-system').addEventListener('click', () => {
  editForm.elements.name.value = system.name;
  editForm.elements.description.value = system.description ?? '';
  openModal(editDialog);
});
editForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  const submit = editForm.querySelector('button[type="submit"]');
  submit.disabled = true;
  try {
    await apiFetch(`/systems/${systemId}`, {
      method: 'PUT',
      body: JSON.stringify({
        name: editForm.elements.name.value.trim(),
        description: editForm.elements.description.value.trim() || null,
      }),
    });
    editDialog.close();
    showFeedback('Sistema guardado.');
    await load();
  } catch (err) {
    showError(err.message);
  } finally {
    submit.disabled = false;
  }
});

bindFiles({
  list: document.getElementById('system-files'),
  form: document.getElementById('file-form'),
  base: `/systems/${systemId}`,
  reload: load,
});

// Only admin and board have the button (data-roles removes it for the rest).
document.getElementById('delete-system')?.addEventListener('click', async () => {
  if (
    !confirm(
      `¿Borrar el sistema «${system.name}», con sus ficheros? Sus cavidades se quedan, sin sistema.`,
    )
  ) {
    return;
  }
  try {
    await apiFetch(`/systems/${systemId}`, { method: 'DELETE' });
    window.location.href = '/sistemas';
  } catch (err) {
    showError(err.message);
  }
});

await load();
