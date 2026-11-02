import {
  apiFetch,
  escapeHtml,
  formatCurrency,
  isNotFound,
  queryId,
  showError,
  showNotFound,
  stopAsNotFound,
} from './api.js';
import { initPage } from './layout.js';
import { compactNumbers, formatDateTime } from './distributionCommon.js';

// Every payment marked in one distribution, newest first, with what it had collected
// after each one. Admin and board: it says who paid.
await initPage({ requireAnyRole: ['admin', 'board'] });

const id = queryId();
if (id === null) await stopAsNotFound();
const base = `/distributions/${id}`;
document.getElementById('back-link').href = `/reparto?id=${id}`;

const PAGE_SIZE = 50;
const list = document.getElementById('payments-list');
const moreButton = document.getElementById('load-more');
const fromFilter = document.getElementById('filter-from');
const toFilter = document.getElementById('filter-to');

let offset = 0;

// "−5,00 €" for what was taken off, "5,00 €" for what was paid.
function signedCurrency(amount) {
  return amount < 0 ? `−${formatCurrency(-amount)}` : formatCurrency(amount);
}

// What the entry is about: how many and of what, with the numbers when it has
// them (consecutive ones as a range).
function what(e) {
  const taken = e.quantity < 0;
  const count = Math.abs(e.quantity);
  const text = `${count} × ${e.description}`;
  const numbers = e.identifiers.length ? ` (${escapeHtml(compactNumbers(e.identifiers))})` : '';
  return `${escapeHtml(text)}${numbers}${taken ? ' <span class="badge">pago retirado</span>' : ''}`;
}

function row(e) {
  return `<tr>
    <td class="nowrap">${escapeHtml(formatDateTime(e.occurred_at))}</td>
    <td>${escapeHtml(e.recorded_by_name)}</td>
    <td>${what(e)}</td>
    <td><a href="/socio?login=${encodeURIComponent(e.paid_by_login)}">${escapeHtml(e.paid_by_name)}</a></td>
    <td class="balance-cell">${signedCurrency(e.amount)}</td>
    <td class="balance-cell">${formatCurrency(e.total_after)}</td>
  </tr>`;
}

// The newest PAGE_SIZE, and more on request; it asks for one more than a page
// just to know whether there is another.
async function load({ more = false } = {}) {
  if (!more) offset = 0;
  const params = new URLSearchParams({ limit: PAGE_SIZE + 1, offset });
  if (fromFilter.value) params.set('from', fromFilter.value);
  if (toFilter.value) params.set('to', toFilter.value);
  try {
    const entries = await apiFetch(`${base}/payments?${params}`);
    const page = entries.slice(0, PAGE_SIZE);
    const rows = page.map(row).join('');
    if (more) list.insertAdjacentHTML('beforeend', rows);
    else
      list.innerHTML = rows || '<tr><td colspan="6">No hay ningún pago con ese filtro.</td></tr>';
    offset += page.length;
    moreButton.hidden = entries.length <= PAGE_SIZE;
  } catch (err) {
    if (isNotFound(err)) {
      showNotFound();
      return;
    }
    list.innerHTML = '<tr><td colspan="6">No se pudieron cargar los pagos.</td></tr>';
    showError(err.message);
  }
}

for (const field of [fromFilter, toFilter]) {
  field.addEventListener('change', () => load());
}
moreButton.addEventListener('click', () => load({ more: true }));

async function showDistribution() {
  try {
    const distribution = await apiFetch(base);
    document.getElementById('payments-title').textContent = `Pagos: ${distribution.name}`;
    document.title = `Pagos: ${distribution.name} — Club de Espeleología Katiuskas`;
  } catch (err) {
    if (isNotFound(err)) showNotFound();
    else showError(err.message);
  }
}

await Promise.all([showDistribution(), load()]);
