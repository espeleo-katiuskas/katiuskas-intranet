import { apiFetch, escapeHtml, formatCurrency, formatMonthYear, showError } from './api.js';
import { initPage } from './layout.js';
import { initModal, openModal } from './modal.js';
import {
  hasArticles,
  loadAccountOptions,
  readDistributionForm,
  statusBadge,
  visibilityLabels,
  syncArticleFields,
} from './distributionCommon.js';

await initPage();

const list = document.getElementById('distributions-list');

function priceText(d) {
  if (hasArticles(d)) {
    return d.articles
      .map((a) => `${a.name} ${a.price > 0 ? formatCurrency(a.price) : 'gratis'}`)
      .join(' · ');
  }
  const own = d.price > 0 ? formatCurrency(d.price) : 'Gratis';
  return d.extra_price != null ? `${own} (extra: ${formatCurrency(d.extra_price)})` : own;
}

// A badge for a distribution only some people can see.
function restricted(d) {
  if (!d.visibility || d.visibility === 'all') return '';
  return ` <span class="badge" title="${escapeHtml(`Lo ven: ${visibilityLabels[d.visibility]}`)}">Restringido</span>`;
}

function renderRow(d) {
  // Drafts are only listed for the people who manage them; for anyone else
  // "eligible" says whether it is for them.
  const mine =
    d.status === 'open' && d.eligible
      ? '<span class="badge status-planned">Puedes pedirlo</span>'
      : '';
  return `
    <tr data-distribution-id="${d.id}">
      <td class="nowrap">${formatMonthYear(d.created_at)}</td>
      <td>${escapeHtml(d.name)}${restricted(d)}</td>
      <td>${statusBadge(d.status)}</td>
      <td>${escapeHtml(priceText(d))}</td>
      <td>${mine}</td>
    </tr>`;
}

async function loadDistributions() {
  try {
    const distributions = await apiFetch('/distributions');
    list.innerHTML = distributions.length
      ? distributions.map(renderRow).join('')
      : '<tr><td colspan="5">Todavía no hay repartos.</td></tr>';
  } catch (err) {
    list.innerHTML = '<tr><td colspan="5">No se pudieron cargar los repartos.</td></tr>';
    showError(err.message);
  }
}

list.addEventListener('click', (event) => {
  const row = event.target.closest('tr[data-distribution-id]');
  if (row) window.location.href = `/reparto?id=${row.dataset.distributionId}`;
});

const dialog = document.getElementById('new-distribution-dialog');
const form = document.getElementById('new-distribution-form');
initModal(dialog, form);

// A distribution for everybody is for whoever was a member on a day, so the
// day is required there; for a sign-up one it is optional.
const audience = document.getElementById('nd-audience');
const cutoff = document.getElementById('nd-cutoff');
function syncCutoff() {
  const required = audience.value === 'all_members';
  cutoff.required = required;
  document.getElementById('nd-cutoff-hint').textContent = required
    ? '(obligatorio)'
    : '(opcional: si la pones, solo podrán apuntarse los socios de ese día)';
}
audience.addEventListener('change', syncCutoff);
dialog.addEventListener('close', syncCutoff);
syncCutoff();

// With articles, the fields that only fit a single article are switched off.
function syncArticles() {
  syncArticleFields(form);
  syncCutoff();
}
form.elements.articles.addEventListener('input', syncArticles);
dialog.addEventListener('close', syncArticles);

// Only admin and board have the button; for anybody else it isn't on the page.
document
  .getElementById('toggle-new-distribution')
  ?.addEventListener('click', () => openModal(dialog));

form.addEventListener('submit', async (event) => {
  event.preventDefault();
  const submit = form.querySelector('button[type="submit"]');
  submit.disabled = true;
  try {
    const created = await apiFetch('/distributions', {
      method: 'POST',
      body: JSON.stringify(readDistributionForm(form)),
    });
    window.location.href = `/reparto?id=${created.id}`;
  } catch (err) {
    showError(err.message);
    submit.disabled = false;
  }
});

await loadAccountOptions(form);
await loadDistributions();
