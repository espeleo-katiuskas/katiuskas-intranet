import { apiFetch, escapeHtml, normalizeForSearch, showError } from './api.js';
import { initPage } from './layout.js';
import { initModal, openModal } from './modal.js';

// The list of systems of cavities, with a form to create one. Any member may.

await initPage();

const tableBody = document.getElementById('systems-table-body');
const searchInput = document.getElementById('systems-search');
const countLine = document.getElementById('systems-count');
let systems = [];

function searchText(system) {
  return normalizeForSearch([system.name, ...system.places.map((p) => p.name)].join(' '));
}

function renderSystem(system) {
  const cavities = system.places.length
    ? system.places
        .map(
          (p) =>
            `<a href="/lugar?id=${p.id}">${escapeHtml(p.name)}</a>${
              p.subtype ? ` <span class="subtitle">(${escapeHtml(p.subtype)})</span>` : ''
            }`,
        )
        .join(', ')
    : '<span class="subtitle">Todavía sin cavidades</span>';
  return `
    <tr>
      <td><a href="/sistema?id=${system.id}"><strong>${escapeHtml(system.name)}</strong></a></td>
      <td>${cavities}</td>
    </tr>`;
}

function render() {
  const words = normalizeForSearch(searchInput.value).split(/\s+/).filter(Boolean);
  const shown = systems.filter((s) => words.every((word) => searchText(s).includes(word)));
  tableBody.innerHTML = shown.length
    ? shown.map(renderSystem).join('')
    : `<tr><td colspan="2">${systems.length ? 'Ningún sistema coincide con la búsqueda.' : 'Todavía no hay ningún sistema.'}</td></tr>`;
  countLine.hidden = systems.length === 0;
  countLine.textContent = `${shown.length} de ${systems.length} sistemas`;
}

async function load() {
  try {
    systems = await apiFetch('/systems');
    render();
  } catch (err) {
    tableBody.innerHTML = '<tr><td colspan="2">No se pudieron cargar los sistemas.</td></tr>';
    showError(err.message);
  }
}

searchInput.addEventListener('input', render);

const dialog = document.getElementById('new-system-dialog');
const form = document.getElementById('new-system-form');
initModal(dialog, form);
document.getElementById('toggle-new-system').addEventListener('click', () => {
  openModal(dialog);
  form.elements.name.focus();
});
form.addEventListener('submit', async (event) => {
  event.preventDefault();
  const submit = form.querySelector('button[type="submit"]');
  submit.disabled = true;
  try {
    const saved = await apiFetch('/systems', {
      method: 'POST',
      body: JSON.stringify({
        name: form.elements.name.value.trim(),
        description: form.elements.description.value.trim() || null,
      }),
    });
    // It goes straight to its page, where its cavities and files are added.
    window.location.href = `/sistema?id=${saved.id}`;
  } catch (err) {
    showError(err.message);
    submit.disabled = false;
  }
});

await load();
