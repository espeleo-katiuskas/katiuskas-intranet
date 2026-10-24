import { apiFetch, escapeHtml, formatCurrency, showError } from './api.js';
import { initPage } from './layout.js';
import { formatDateTime, formatIdentifiers } from './distributionCommon.js';

// Every payment marked in the distributions, newest first, with what each
// distribution had collected after each one. Admin and board: it says who paid.
await initPage({ requireAnyRole: ['admin', 'board'] });

const PAGE_SIZE = 50;
const list = document.getElementById('payments-list');
const moreButton = document.getElementById('load-more');
const distributionFilter = document.getElementById('filter-distribution');
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
  const pseudo = {
    identifier_fields: e.identifiers.some((i) => i.includes('/'))
      ? [{ name: '' }, { name: '' }]
      : [],
  };
  const numbers = e.identifiers.length
    ? ` (${escapeHtml(formatIdentifiers(pseudo, e.identifiers, false))})`
    : '';
  return `${escapeHtml(text)}${numbers}${taken ? ' <span class="badge">pago retirado</span>' : ''}`;
}

function row(e) {
  return `<tr>
    <td>${escapeHtml(formatDateTime(e.occurred_at))}</td>
    <td>${e.distribution_name ? `<a href="/reparto-pedidos?id=${e.distribution_id}">${escapeHtml(e.distribution_name)}</a>` : ''}</td>
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
  if (distributionFilter.value) params.set('distribution_id', distributionFilter.value);
  if (fromFilter.value) params.set('from', fromFilter.value);
  if (toFilter.value) params.set('to', toFilter.value);
  try {
    const entries = await apiFetch(`/distribution-payments?${params}`);
    const page = entries.slice(0, PAGE_SIZE);
    const rows = page.map(row).join('');
    if (more) list.insertAdjacentHTML('beforeend', rows);
    else
      list.innerHTML = rows || '<tr><td colspan="7">No hay ningún pago con ese filtro.</td></tr>';
    offset += page.length;
    moreButton.hidden = entries.length <= PAGE_SIZE;
  } catch (err) {
    list.innerHTML = '<tr><td colspan="7">No se pudieron cargar los pagos.</td></tr>';
    showError(err.message);
  }
}

for (const field of [distributionFilter, fromFilter, toFilter]) {
  field.addEventListener('change', () => load());
}
moreButton.addEventListener('click', () => load({ more: true }));

// The distributions with something to pay are the ones worth choosing.
async function loadDistributions() {
  try {
    const all = await apiFetch('/distributions');
    const payable = all.filter(
      (d) => d.price > 0 || (d.extra_price ?? 0) > 0 || (d.articles ?? []).some((a) => a.price > 0),
    );
    distributionFilter.insertAdjacentHTML(
      'beforeend',
      payable.map((d) => `<option value="${d.id}">${escapeHtml(d.name)}</option>`).join(''),
    );
  } catch {
    // Without the choices the list still shows everything.
  }
}

await Promise.all([loadDistributions(), load()]);
