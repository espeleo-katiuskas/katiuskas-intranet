import {
  apiFetch,
  escapeHtml,
  hasAnyRole,
  normalizeForSearch,
  showError,
  showFeedback,
} from './api.js';
import {
  coverImage,
  dateOnly,
  loadPeople,
  loadTypes,
  personLink,
  typeOptions,
} from './libraryCommon.js';
import { initPage } from './layout.js';
import { initModal, openModal } from './modal.js';

const user = await initPage();
// Who creates items (the Junta and the library), and who sees and records the loans (those and the
// material role). The API is the authority: this only decides what is shown.
const canManage = hasAnyRole(user, ['admin', 'board', 'biblioteca']);
const canLoan = hasAnyRole(user, ['admin', 'board', 'biblioteca', 'material']);

const tableBody = document.getElementById('items-table-body');
const searchInput = document.getElementById('library-search');
const typeFilter = document.getElementById('type-filter');
const onlyAvailable = document.getElementById('only-available');
const countLine = document.getElementById('library-count');
const loansBody = document.getElementById('open-loans-table-body');
let allItems = [];
let types = [];
let openLoans = [];
let people = { namesByLogin: {}, memberEntries: [] };

// What the search looks in: everything a row shows, and the rest of what is known of the item.
function searchText(item) {
  return normalizeForSearch(
    [
      item.title,
      item.type,
      item.author,
      item.publisher,
      item.year,
      item.isbn,
      item.location,
      item.description,
    ]
      .filter(Boolean)
      .join(' '),
  );
}

function renderItem(item) {
  return `
    <tr>
      <td class="library-thumb-cell">${coverImage(item, 'library-thumb')}</td>
      <td><a href="/ejemplar?id=${item.id}"><strong>${escapeHtml(item.title)}</strong></a>${
        item.location ? `<br><span class="subtitle">${escapeHtml(item.location)}</span>` : ''
      }</td>
      <td>${escapeHtml(item.type)}</td>
      <td>${item.author ? escapeHtml(item.author) : '—'}</td>
      <td class="nowrap">${item.year ?? '—'}</td>
      <td>${
        item.available
          ? '<span class="badge badge-ok">Disponible</span>'
          : '<span class="badge badge-out">Prestado</span>'
      }</td>
    </tr>`;
}

function renderItems() {
  const words = normalizeForSearch(searchInput.value).split(/\s+/).filter(Boolean);
  const shown = allItems.filter(
    (item) =>
      (!onlyAvailable.checked || item.available) &&
      (!typeFilter.value || item.type_id === Number(typeFilter.value)) &&
      words.every((word) => searchText(item).includes(word)),
  );
  tableBody.innerHTML = shown.length
    ? shown.map(renderItem).join('')
    : `<tr><td colspan="6">${allItems.length ? 'Ningún ejemplar coincide con la búsqueda.' : 'Todavía no hay nada en la biblioteca.'}</td></tr>`;
  countLine.hidden = allItems.length === 0;
  countLine.textContent = `${shown.length} de ${allItems.length} ejemplares`;
}

function renderLoans() {
  if (!canLoan) return;
  loansBody.innerHTML = openLoans.length
    ? openLoans
        .map(
          (loan) => `
      <tr>
        <td><a href="/ejemplar?id=${loan.item_id}">${escapeHtml(loan.item_title)}</a></td>
        <td>${personLink(people.namesByLogin, loan.user_login)}</td>
        <td class="nowrap">${dateOnly(loan.lent_at)}</td>
        <td><button type="button" class="btn btn-secondary btn-small" data-return="${loan.item_id}:${loan.id}">Devolver</button></td>
      </tr>`,
        )
        .join('')
    : '<tr><td colspan="4">No hay ningún ejemplar prestado.</td></tr>';
}

// The types feed the filter, and (for those who manage) the form of a new item and the dialog of types.
function setTypes(list) {
  types = list;
  const chosen = typeFilter.value;
  typeFilter.innerHTML = `<option value="">Todos</option>${list
    .map((t) => `<option value="${t.id}">${escapeHtml(t.name)}</option>`)
    .join('')}`;
  if (list.some((t) => String(t.id) === chosen)) typeFilter.value = chosen;
  if (canManage) renderTypes();
}

function renderTypes() {
  document.getElementById('types-table-body').innerHTML = types
    .map(
      (t) => `
      <tr>
        <td><div class="field"><input type="text" maxlength="60" value="${escapeHtml(t.name)}" data-type-name="${t.id}" aria-label="Nombre del tipo"></div></td>
        <td class="nowrap">${t.item_count}</td>
        <td class="nowrap">
          <button type="button" class="btn btn-secondary btn-small" data-save-type="${t.id}">Guardar</button>
          <button type="button" class="btn btn-danger btn-small" data-delete-type="${t.id}"${t.item_count ? ' disabled title="Tiene ejemplares"' : ''}>Borrar</button>
        </td>
      </tr>`,
    )
    .join('');
}

async function load() {
  try {
    const requests = [apiFetch('/library/items'), loadPeople(), loadTypes()];
    if (canLoan) requests.push(apiFetch('/library/loans'));
    const [items, loaded, loadedTypes, loans] = await Promise.all(requests);
    allItems = items;
    people = loaded;
    setTypes(loadedTypes);
    openLoans = loans ?? [];
    renderItems();
    renderLoans();
  } catch (err) {
    tableBody.innerHTML = '<tr><td colspan="6">No se pudo cargar la biblioteca.</td></tr>';
    showError(err.message);
  }
}

searchInput.addEventListener('input', renderItems);
onlyAvailable.addEventListener('change', renderItems);
typeFilter.addEventListener('change', renderItems);

// The table of loans is only in the page for those who can see it (data-roles removes it for the rest).
if (canLoan) {
  loansBody.addEventListener('click', async (event) => {
    const button = event.target.closest('button[data-return]');
    if (!button) return;
    const [itemId, loanId] = button.dataset.return.split(':');
    button.disabled = true;
    try {
      await apiFetch(`/library/items/${itemId}/loans/${loanId}/return`, { method: 'POST' });
      showFeedback('Devolución apuntada.');
      await load();
    } catch (err) {
      button.disabled = false;
      showError(err.message);
    }
  });
}

if (canManage) {
  const dialog = document.getElementById('new-item-dialog');
  const form = document.getElementById('new-item-form');
  initModal(dialog, form);
  document.getElementById('toggle-new-item').addEventListener('click', () => {
    form.elements.type_id.innerHTML = typeOptions(types, null);
    openModal(dialog);
  });
  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    const text = (name) => form.elements[name].value.trim() || null;
    const submit = form.querySelector('button[type="submit"]');
    submit.disabled = true;
    try {
      const item = await apiFetch('/library/items', {
        method: 'POST',
        body: JSON.stringify({
          type_id: Number(form.elements.type_id.value),
          title: form.elements.title.value.trim(),
          author: text('author'),
          publisher: text('publisher'),
          year: form.elements.year.value ? Number(form.elements.year.value) : null,
          isbn: text('isbn'),
          location: text('location'),
          description: text('description'),
        }),
      });
      // To its own page, where the cover and the files are added.
      window.location.href = `/ejemplar?id=${item.id}`;
    } catch (err) {
      showError(err.message);
      submit.disabled = false;
    }
  });

  // ----- the types of item -----

  const typesDialog = document.getElementById('types-dialog');
  const typesBody = document.getElementById('types-table-body');
  const typeForm = document.getElementById('new-type-form');
  initModal(typesDialog, typeForm);
  document.getElementById('manage-types').addEventListener('click', () => openModal(typesDialog));

  // Runs a change of the types and loads everything again; the dialog stays open.
  async function changeTypes(action, feedback) {
    try {
      await action();
      showFeedback(feedback);
      await load();
    } catch (err) {
      showError(err.message);
    }
  }

  typesBody.addEventListener('click', (event) => {
    const save = event.target.closest('button[data-save-type]');
    if (save) {
      const id = save.dataset.saveType;
      const name = typesBody.querySelector(`input[data-type-name="${id}"]`).value;
      changeTypes(
        () => apiFetch(`/library/types/${id}`, { method: 'PUT', body: JSON.stringify({ name }) }),
        'Tipo guardado.',
      );
      return;
    }
    const del = event.target.closest('button[data-delete-type]');
    if (del && confirm('¿Borrar este tipo?')) {
      changeTypes(
        () => apiFetch(`/library/types/${del.dataset.deleteType}`, { method: 'DELETE' }),
        'Tipo borrado.',
      );
    }
  });
  typeForm.addEventListener('submit', (event) => {
    event.preventDefault();
    changeTypes(async () => {
      await apiFetch('/library/types', {
        method: 'POST',
        body: JSON.stringify({ name: typeForm.elements.name.value }),
      });
      typeForm.reset();
    }, 'Tipo añadido.');
  });
}

await load();
