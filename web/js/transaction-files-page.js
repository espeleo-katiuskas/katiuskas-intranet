import { apiFetch, escapeHtml, formatCurrency, formatDate, showError } from './api.js';
import { initPage } from './layout.js';
import { transactionFileLink } from './transaction-files.js';

// The sidebar and the API both limit this page to admin, board and money; the API is the authority.
await initPage({ requireAnyRole: ['admin', 'board', 'money'] });

const PAGE_SIZE = 50;
const yearFilter = document.getElementById('year-filter');
const tableBody = document.getElementById('files-table-body');
const loadMore = document.getElementById('files-load-more');
const empty = document.getElementById('files-empty');
let loaded = 0;

function accountsCell(entries) {
  return entries
    .map((e) => {
      const name = `<a href="/cuenta?id=${e.account_id}">${escapeHtml(e.account_name)}</a>`;
      const sign = e.amount > 0 ? '+' : '';
      return `${name} <span class="hint nowrap">${sign}${formatCurrency(e.amount)}</span>`;
    })
    .join('<br>');
}

function row(item) {
  const t = item.transaction;
  return `<tr>
    <td class="nowrap">${formatDate(t.occurred_at)}</td>
    <td>${escapeHtml(t.description)}</td>
    <td class="balance-cell">${formatCurrency(t.amount)}</td>
    <td>${accountsCell(t.entries)}</td>
    <td>${transactionFileLink(item)}</td>
  </tr>`;
}

async function load({ append = false } = {}) {
  if (!append) loaded = 0;
  const params = new URLSearchParams({ limit: PAGE_SIZE + 1, offset: loaded });
  if (yearFilter.value) params.set('year', yearFilter.value);
  let items;
  try {
    items = await apiFetch(`/transaction-files?${params}`);
  } catch (err) {
    showError(err.message);
    return;
  }
  const more = items.length > PAGE_SIZE;
  const shown = items.slice(0, PAGE_SIZE);
  loaded += shown.length;
  const html = shown.map(row).join('');
  if (append) tableBody.insertAdjacentHTML('beforeend', html);
  else tableBody.innerHTML = html;
  empty.hidden = loaded > 0;
  loadMore.hidden = !more;
}

// Years to pick from: this one and the previous ones back to the oldest transaction's would need a
// query of its own, so the last ten are offered, plus "all".
const thisYear = new Date().getFullYear();
yearFilter.innerHTML =
  '<option value="">Todos</option>' +
  Array.from({ length: 10 }, (_, i) => thisYear - i)
    .map((y) => `<option value="${y}">${y}</option>`)
    .join('');
yearFilter.addEventListener('change', () => load());
loadMore.addEventListener('click', () => load({ append: true }));
await load();
