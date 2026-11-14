import {
  apiFetch,
  escapeHtml,
  hasAnyRole,
  normalizeForSearch,
  showError,
  showFeedback,
} from './api.js';
import { initAreasManager } from './areas-manager.js';
import { initPage } from './layout.js';
import { initModal, openModal } from './modal.js';
import { openPlaceDialog } from './place-dialog.js';
import {
  loadGeologies,
  loadTypes,
  measuresText,
  STATE_LABELS,
  stateBadge,
} from './places-common.js';

const user = await initPage();
const isAdmin = hasAnyRole(user, ['admin']);

const tableBody = document.getElementById('places-table-body');
const searchInput = document.getElementById('places-search');
const typeFilter = document.getElementById('type-filter');
const stateFilter = document.getElementById('state-filter');
const geologyFilter = document.getElementById('geology-filter');
const countLine = document.getElementById('places-count');
let allPlaces = [];
let types = [];
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
      place.alt_names,
      place.type_name,
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
        place.alt_names ? `<br><span class="subtitle">${escapeHtml(place.alt_names)}</span>` : ''
      }</td>
      <td>${escapeHtml(place.type_name)}${
        place.geology ? `<br><span class="subtitle">${escapeHtml(place.geology)}</span>` : ''
      }</td>
      <td>${stateBadge(place.state)}</td>
      <td>${escapeHtml(place.where)}</td>
      <td>${escapeHtml(measuresText(place)) || '—'}</td>
    </tr>`;
}

function renderPlaces() {
  const words = normalizeForSearch(searchInput.value).split(/\s+/).filter(Boolean);
  const shown = allPlaces.filter(
    (place) =>
      (!typeFilter.value || place.type_id === Number(typeFilter.value)) &&
      (!stateFilter.value || place.state === stateFilter.value) &&
      (!geologyFilter.value || place.geology_id === Number(geologyFilter.value)) &&
      words.every((word) => searchText(place).includes(word)),
  );
  tableBody.innerHTML = shown.length
    ? shown.map(renderPlace).join('')
    : `<tr><td colspan="5">${allPlaces.length ? 'Ningún lugar coincide con la búsqueda.' : 'Todavía no hay ningún lugar.'}</td></tr>`;
  countLine.hidden = allPlaces.length === 0;
  countLine.textContent = `${shown.length} de ${allPlaces.length} lugares`;
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
function catalogueRows(items, { fixed = () => false, noun }) {
  return items
    .map(
      (item) => `
      <tr>
        <td><div class="field"><input type="text" value="${escapeHtml(item.name)}" data-name="${item.id}" aria-label="Nombre ${noun}"${fixed(item) ? ' disabled' : ''}></div></td>
        <td class="nowrap">${item.place_count}</td>
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
    noun: 'del tipo',
  });
  document.getElementById('geologies-table-body').innerHTML = catalogueRows(geologies, {
    noun: 'de la geología',
  });
}

async function load() {
  try {
    const [places, loadedTypes, loadedGeologies] = await Promise.all([
      apiFetch('/places'),
      loadTypes(),
      loadGeologies(),
    ]);
    allPlaces = places;
    types = loadedTypes;
    geologies = loadedGeologies;
    setOptions(typeFilter, 'Todos', types);
    setOptions(geologyFilter, 'Todas', geologies);
    if (isAdmin) renderCatalogues();
    renderPlaces();
  } catch (err) {
    tableBody.innerHTML = '<tr><td colspan="5">No se pudieron cargar los lugares.</td></tr>';
    showError(err.message);
  }
}

searchInput.addEventListener('input', renderPlaces);
typeFilter.addEventListener('change', renderPlaces);
stateFilter.addEventListener('change', renderPlaces);
geologyFilter.addEventListener('change', renderPlaces);

// Any member can add a place; it goes straight to its page, where the rest is filled in.
document.getElementById('toggle-new-place').addEventListener('click', () => {
  openPlaceDialog({
    types,
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
        body: JSON.stringify({ name: form.elements.name.value }),
      });
      form.reset();
    }, words.added);
  });
}

if (isAdmin) {
  initAreasManager(document.getElementById('manage-areas'));
  const dialog = document.getElementById('types-dialog');
  const typeForm = document.getElementById('new-type-form');
  const geologyForm = document.getElementById('new-geology-form');
  initModal(dialog, typeForm);
  dialog.addEventListener('close', () => geologyForm.reset());
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

await load();
