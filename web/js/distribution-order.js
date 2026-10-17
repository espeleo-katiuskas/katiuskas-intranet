import { apiFetch, escapeHtml, formatCurrency, formatDate, hasAnyRole, currentUser, showError, showFeedback } from './api.js';
import { initPage } from './layout.js';
import { apiErrorText, choicesText, loadPeople, statusBadge } from './distributionCommon.js';

// What has to be bought, and what each member wants: for the people who put
// the order together (admin, board) and hand it out (material).
await initPage({ requireAnyRole: ['admin', 'board', 'material'] });
const canMarkPaid = hasAnyRole(currentUser(), ['admin', 'board']);

const id = Number(new URLSearchParams(window.location.search).get('id'));
const base = `/distributions/${id}`;
document.getElementById('back-link').href = `/distribution?id=${id}`;

let units = [];
let people = { entries: [], names: {} };
const fail = (err) => showError(apiErrorText(err));

function renderOrder(order) {
  document.getElementById('order-lines').innerHTML = order.lines.length
    ? order.lines.map((l) => `<tr><td>${escapeHtml(choicesText(l.choices))}</td><td>${l.count}</td></tr>`).join('')
    : '<tr><td colspan="2">Todavía no ha pedido nadie.</td></tr>';
  document.getElementById('order-totals').textContent =
    `${order.total_units} unidades en total, por ${formatCurrency(order.total_amount)}.`;

  const pending = document.getElementById('pending-section');
  pending.hidden = order.pending_members.length === 0;
  const n = order.pending_members.length;
  document.getElementById('pending-subtitle').textContent = n === 1
    ? '1 socio al que le corresponde y todavía no ha elegido.'
    : `${n} socios a los que les corresponde y todavía no han elegido.`;
  document.getElementById('pending-list').textContent = order.pending_members.map((p) => p.name).join(', ');
}

// Units grouped by the person they are for.
function groupByPerson(all) {
  const groups = new Map();
  for (const u of all) {
    if (!groups.has(u.beneficiary_login)) groups.set(u.beneficiary_login, { name: u.beneficiary_name, units: [] });
    groups.get(u.beneficiary_login).units.push(u);
  }
  return [...groups.entries()];
}

function unitLine(u) {
  const parts = [choicesText(u.choices)];
  if (u.is_extra) parts.push('extra');
  // Who entered it, when that isn't the person it is for.
  if (u.registered_by_login !== u.beneficiary_login) {
    parts.push(`apuntado por ${people.names[u.registered_by_login] ?? u.registered_by_login} el ${formatDate(u.registered_at)}`);
  }
  return `<li>${escapeHtml(parts.join(' · '))}</li>`;
}

function paymentCell(login, list) {
  const toPay = list.filter((u) => u.price > 0);
  if (toPay.length === 0) return 'Gratis';
  const unpaid = toPay.filter((u) => !u.paid_at);
  const owed = unpaid.reduce((sum, u) => sum + u.price, 0);
  if (!canMarkPaid) return unpaid.length ? `Pendiente: ${formatCurrency(owed)}` : 'Pagado';
  const mark = unpaid.length
    ? `<button type="button" class="btn btn-primary btn-small" data-pay-person="${escapeHtml(login)}">Marcar pagado (${formatCurrency(owed)})</button>`
    : '';
  const undo = unpaid.length < toPay.length
    ? `<button type="button" class="btn btn-secondary btn-small" data-unpay-person="${escapeHtml(login)}">${unpaid.length ? 'Quitar lo pagado' : 'Pagado · deshacer'}</button>`
    : '';
  return `<div class="table-actions">${mark}${undo}</div>`;
}

function renderPeople() {
  const groups = groupByPerson(units);
  document.getElementById('people-list').innerHTML = groups.length
    ? groups.map(([login, g]) => `
        <tr>
          <td>${escapeHtml(g.name)}</td>
          <td><ul class="plain-list">${g.units.map(unitLine).join('')}</ul></td>
          <td>${formatCurrency(g.units.reduce((sum, u) => sum + u.price, 0))}</td>
          <td>${paymentCell(login, g.units)}</td>
        </tr>`).join('')
    : '<tr><td colspan="4">Todavía no ha pedido nadie.</td></tr>';
}

document.getElementById('people-list').addEventListener('click', async (event) => {
  const pay = event.target.closest('button[data-pay-person]');
  const unpay = event.target.closest('button[data-unpay-person]');
  const button = pay ?? unpay;
  if (!button) return;
  const login = (pay ?? unpay).dataset.payPerson ?? button.dataset.unpayPerson;
  const mine = units.filter((u) => u.beneficiary_login === login && u.price > 0);
  button.disabled = true;
  try {
    if (pay) {
      const ids = mine.filter((u) => !u.paid_at).map((u) => u.id);
      await apiFetch(`${base}/payments`, { method: 'POST', body: JSON.stringify({ unit_ids: ids }) });
      showFeedback('Marcado como pagado.');
    } else {
      if (!confirm('¿Quitar la marca de pagado de las unidades de esta persona?')) {
        button.disabled = false;
        return;
      }
      for (const u of mine.filter((x) => x.paid_at)) {
        await apiFetch(`${base}/units/${u.id}/payment`, { method: 'DELETE' });
      }
      showFeedback('Marca de pagado quitada.');
    }
    await load();
  } catch (err) {
    button.disabled = false;
    fail(err);
  }
});

async function load() {
  try {
    const [order, all] = await Promise.all([apiFetch(`${base}/order`), apiFetch(`${base}/units`)]);
    units = all;
    renderOrder(order);
    renderPeople();
  } catch (err) {
    fail(err);
  }
}

people = await loadPeople();
try {
  const distribution = await apiFetch(base);
  document.getElementById('order-title').textContent = `Pedido: ${distribution.name}`;
  document.getElementById('order-meta').innerHTML = statusBadge(distribution.status);
} catch (err) {
  fail(err);
}
await load();
