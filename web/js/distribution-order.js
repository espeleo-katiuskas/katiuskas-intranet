import {
  apiFetch,
  escapeHtml,
  formatCurrency,
  formatDate,
  hasAnyRole,
  currentUser,
  showError,
  showFeedback,
} from './api.js';
import { initPage } from './layout.js';
import { initModal, openModal } from './modal.js';
import {
  apiErrorText,
  articleLeft,
  choicesText,
  hasArticles,
  loadPeople,
  pendingCost,
  pendingQuantity,
  statusBadge,
  unitCost,
  unitLabel,
} from './distributionCommon.js';

// What has to be bought, and what each member wants: for the people who put
// the order together (admin, board) and hand it out (material).
await initPage({ requireAnyRole: ['admin', 'board', 'material'] });
const canMarkPaid = hasAnyRole(currentUser(), ['admin', 'board']);

const id = Number(new URLSearchParams(window.location.search).get('id'));
const base = `/distributions/${id}`;
document.getElementById('back-link').href = `/reparto?id=${id}`;

let units = [];
let distribution = null;
let people = { entries: [], names: {} };
const fail = (err) => showError(apiErrorText(err));

const hasOptions = () => (distribution?.options.length ?? 0) > 0;

let provisional = false;

// With articles, what has to be bought is each article's total, whatever
// anybody wants of it, against how many there are.
function renderArticleOrder(order) {
  document.getElementById('order-head').innerHTML =
    '<th>Artículo</th><th>Pedidas</th><th>Máximo</th><th>Quedan</th><th>Importe</th>';
  document.getElementById('order-lines').innerHTML = distribution.articles
    .map((a) => {
      const asked = order.lines
        .filter((l) => l.article_id === a.id)
        .reduce((sum, l) => sum + l.count, 0);
      const left = articleLeft(a);
      return `<tr><td>${escapeHtml(a.name)}</td><td>${asked}</td><td>${a.max_total ?? 'Sin límite'}</td><td>${left ?? '—'}</td><td>${formatCurrency(asked * a.price)}</td></tr>`;
    })
    .join('');
  document.getElementById('order-totals').textContent =
    `${order.total_units} unidades en total, por ${formatCurrency(order.total_amount)}.`;
}

function renderOrder(order) {
  if (hasArticles(distribution ?? {})) {
    renderArticleOrder(order);
    return;
  }
  provisional = order.provisional;
  const noChoices = (l) => Object.keys(l.choices).length === 0;
  document.getElementById('order-lines').innerHTML = order.lines.length
    ? order.lines
        .map((l) => {
          let label;
          if (!noChoices(l)) label = escapeHtml(choicesText(l.choices, hasOptions()));
          else label = hasOptions() ? '<strong>Sin elegir</strong>' : 'Sin nada que elegir';
          return `<tr><td>${label}</td><td>${l.count}</td></tr>`;
        })
        .join('')
    : '<tr><td colspan="2">Todavía no hay nada que pedir.</td></tr>';
  // In a distribution for all members everybody has a unit from the start, so
  // the ones that nobody has chosen the options of are what is still missing.
  const missing = hasOptions()
    ? order.lines.filter(noChoices).reduce((sum, l) => sum + l.count, 0)
    : 0;
  const missingText =
    missing === 0
      ? ''
      : missing === 1
        ? ' Falta por elegir 1 pedido.'
        : ` Faltan por elegir ${missing} pedidos.`;
  // While it is a draft nothing is made yet, but what has to be ordered is
  // already known: one for each member on the cutoff date.
  const draftText = provisional
    ? ' Es un borrador: se cuenta un pedido por cada socio en la fecha de corte, y se crearán al abrir el reparto.'
    : '';
  document.getElementById('order-totals').textContent =
    `${order.total_units} pedidos en total, por ${formatCurrency(order.total_amount)}.${missingText}${draftText}`;
}

// Units grouped by the person they are for.
function groupByPerson(all) {
  const groups = new Map();
  for (const u of all) {
    if (!groups.has(u.beneficiary_login))
      groups.set(u.beneficiary_login, { name: u.beneficiary_name, units: [] });
    groups.get(u.beneficiary_login).units.push(u);
  }
  return [...groups.entries()];
}

// Whether the row has something to pay at all, and what is shown of it.
const costs = (u) => u.price > 0;

function paymentStatus(u) {
  if (!costs(u)) return '';
  const total = u.quantity ?? 1;
  if (u.paid_quantity === 0) return 'sin pagar';
  if (u.paid_quantity === total) return 'pagado';
  return `pagadas ${u.paid_quantity} de ${total}`;
}

// A row's own payment controls (admin/board): how many units, and mark them as
// paid or take them off. A row of one unit has nothing to count.
function paymentControls(u) {
  if (!canMarkPaid || !costs(u)) return '';
  const total = u.quantity ?? 1;
  const pending = pendingQuantity(u);
  const count =
    total > 1
      ? `<input type="number" min="1" max="${total}" step="1" size="4" value="${pending || u.paid_quantity}" data-qty-for="${u.id}" aria-label="Unidades de ${escapeHtml(unitLabel(u, false, distribution?.articles))}">`
      : '';
  const mark = pending
    ? `<button type="button" class="btn btn-primary btn-small" data-pay-unit="${u.id}">Marcar ${total > 1 ? 'pagadas' : 'pagado'}</button>`
    : '';
  const undo = u.paid_quantity
    ? `<button type="button" class="btn btn-secondary btn-small" data-unpay-unit="${u.id}">Quitar ${total > 1 ? 'pagadas' : 'pagado'}</button>`
    : '';
  return `<div class="table-actions">${count}${mark}${undo}</div>`;
}

// Changing or removing somebody's order: material, board and admin can, even if it
// is paid, as long as nothing in it is collected (the API has the last word). The own unit of everybody in a
// distribution for all members can't be removed.
function canChangeUnit(u) {
  // A row handed out in part can still change (not below what went), but a unit
  // that isn't a row of an article can't.
  return (u.picked_up_quantity === 0 || Boolean(u.article_id)) && distribution?.status === 'open';
}

function changeControls(u) {
  if (!canChangeUnit(u)) return '';
  const changeable = hasOptions() || u.article_id;
  const removable = u.is_extra || distribution.audience === 'signup';
  return `<div class="table-actions">
    ${changeable ? `<button type="button" class="btn btn-secondary btn-small" data-change-unit="${u.id}">Cambiar</button>` : ''}
    ${removable ? `<button type="button" class="btn btn-danger btn-small" data-delete-unit="${u.id}">Quitar</button>` : ''}
  </div>`;
}

function unitLine(u) {
  const parts = [unitLabel(u, hasOptions(), distribution?.articles)];
  if (u.is_extra) parts.push('extra');
  // Who entered it, when that isn't the person it is for (nobody did for an
  // own unit nobody has chosen yet).
  if (u.registered_by_login && u.registered_by_login !== u.beneficiary_login) {
    parts.push(
      `apuntado por ${people.names[u.registered_by_login] ?? u.registered_by_login} el ${formatDate(u.registered_at)}`,
    );
  }
  const status = paymentStatus(u);
  if (status) parts.push(status);
  return `<li>${escapeHtml(parts.join(' · '))}${paymentControls(u)}${changeControls(u)}</li>`;
}

// What the person has paid of what they owe, and the controls for all of it at
// once (each row has its own too).
function paymentCell(login, list) {
  const toPay = list.filter(costs);
  if (toPay.length === 0) return 'Gratis';
  const owed = toPay.reduce((sum, u) => sum + pendingCost(u), 0);
  const total = toPay.reduce((sum, u) => sum + unitCost(u), 0);
  const summary =
    owed === 0
      ? 'Pagado'
      : owed === total
        ? `Pendiente: ${formatCurrency(owed)}`
        : `Pagado ${formatCurrency(total - owed)} · pendiente ${formatCurrency(owed)}`;
  if (!canMarkPaid) return summary;
  const mark =
    owed > 0
      ? `<button type="button" class="btn btn-primary btn-small" data-pay-person="${escapeHtml(login)}">Marcar todo pagado (${formatCurrency(owed)})</button>`
      : '';
  const undo =
    owed < total
      ? `<button type="button" class="btn btn-secondary btn-small" data-unpay-person="${escapeHtml(login)}">Quitar todo lo pagado</button>`
      : '';
  return `<div>${escapeHtml(summary)}</div><div class="table-actions">${mark}${undo}</div>`;
}

function renderPeople() {
  const groups = groupByPerson(units);
  document.getElementById('people-list').innerHTML = groups.length
    ? groups
        .map(
          ([login, g]) => `
        <tr>
          <td>${escapeHtml(g.name)}</td>
          <td><ul class="plain-list">${g.units.map(unitLine).join('')}</ul></td>
          <td>${formatCurrency(g.units.reduce((sum, u) => sum + unitCost(u), 0))}</td>
          <td>${paymentCell(login, g.units)}</td>
        </tr>`,
        )
        .join('')
    : `<tr><td colspan="4">${provisional ? 'Los pedidos de cada socio se crean al abrir el reparto.' : 'Todavía no ha pedido nadie.'}</td></tr>`;
}

const unitDialog = document.getElementById('edit-unit-dialog');
const unitForm = document.getElementById('edit-unit-form');
initModal(unitDialog, unitForm);
let changing = null;

function optionSelects(choices = {}) {
  return distribution.options
    .map(
      (option, i) => `
      <div class="field">
        <label for="eu-${i}">${escapeHtml(option.name)}</label>
        <select id="eu-${i}" data-option="${escapeHtml(option.name)}" required>
          <option value="">— Elige —</option>
          ${option.values.map((v) => `<option${choices[option.name] === v ? ' selected' : ''}>${escapeHtml(v)}</option>`).join('')}
        </select>
      </div>`,
    )
    .join('');
}

document.getElementById('people-list').addEventListener('click', async (event) => {
  const change = event.target.closest('button[data-change-unit]');
  const remove = event.target.closest('button[data-delete-unit]');
  if (change) {
    changing = units.find((u) => u.id === Number(change.dataset.changeUnit));
    document.getElementById('edit-unit-hint').textContent = `Para ${changing.beneficiary_name}.`;
    const byQuantity = Boolean(changing.article_id);
    document.getElementById('edit-unit-title').textContent = byQuantity
      ? `Cambiar la cantidad de ${changing.article_name}`
      : 'Cambiar lo elegido';
    document.getElementById('eu-quantity-field').hidden = !byQuantity;
    document.getElementById('eu-quantity').value = changing.quantity;
    document.getElementById('eu-options').innerHTML = byQuantity
      ? ''
      : optionSelects(changing.choices);
    openModal(unitDialog);
    return;
  }
  if (remove) {
    if (!confirm('¿Quitar este pedido?')) return;
    remove.disabled = true;
    try {
      await apiFetch(`${base}/units/${remove.dataset.deleteUnit}`, { method: 'DELETE' });
      showFeedback('Pedido quitado.');
      await load();
    } catch (err) {
      remove.disabled = false;
      fail(err);
    }
    return;
  }
  const payUnit = event.target.closest('button[data-pay-unit]');
  const unpayUnit = event.target.closest('button[data-unpay-unit]');
  const pay = event.target.closest('button[data-pay-person]');
  const unpay = event.target.closest('button[data-unpay-person]');
  const button = payUnit ?? unpayUnit ?? pay ?? unpay;
  if (!button) return;
  button.disabled = true;
  try {
    if (payUnit || unpayUnit) {
      // Some units of one row: as many as the number typed next to it.
      const unitId = Number((payUnit ?? unpayUnit).dataset.payUnit ?? button.dataset.unpayUnit);
      const row = units.find((u) => u.id === unitId);
      const field = document.querySelector(`input[data-qty-for="${unitId}"]`);
      const quantity = field ? Number(field.value) : (row.quantity ?? 1);
      if (!Number.isInteger(quantity) || quantity < 1) {
        showError('Pon cuántas unidades (un número entero de 1 en adelante).');
        button.disabled = false;
        return;
      }
      if (payUnit) {
        await apiFetch(`${base}/payments`, {
          method: 'POST',
          body: JSON.stringify({ payments: [{ unit_id: unitId, quantity }] }),
        });
        showFeedback(
          quantity === 1
            ? 'Marcada 1 unidad como pagada.'
            : `Marcadas ${quantity} unidades como pagadas.`,
        );
      } else {
        await apiFetch(`${base}/units/${unitId}/payment?quantity=${quantity}`, {
          method: 'DELETE',
        });
        showFeedback('Marca de pagado quitada.');
      }
    } else {
      const login = (pay ?? unpay).dataset.payPerson ?? button.dataset.unpayPerson;
      const mine = units.filter((u) => u.beneficiary_login === login && costs(u));
      if (pay) {
        const ids = mine.filter((u) => pendingQuantity(u) > 0).map((u) => u.id);
        await apiFetch(`${base}/payments`, {
          method: 'POST',
          body: JSON.stringify({ unit_ids: ids }),
        });
        showFeedback('Marcado como pagado.');
      } else {
        if (!confirm('¿Quitar la marca de pagado de los pedidos de esta persona?')) {
          button.disabled = false;
          return;
        }
        for (const u of mine.filter((x) => x.paid_quantity > 0)) {
          await apiFetch(`${base}/units/${u.id}/payment`, { method: 'DELETE' });
        }
        showFeedback('Marca de pagado quitada.');
      }
    }
    await load();
  } catch (err) {
    button.disabled = false;
    fail(err);
  }
});

// `ready` is what drawing needs besides this data (the people): the requests
// all go out at once and only the drawing waits.
async function load(ready = Promise.resolve()) {
  try {
    const [order, all] = await Promise.all([
      apiFetch(`${base}/order`),
      apiFetch(`${base}/units`),
      ready,
    ]);
    units = all;
    renderOrder(order);
    renderPeople();
  } catch (err) {
    fail(err);
  }
}

const peopleRequest = loadPeople(id);
// Assigned as soon as it arrives, so the drawing of the first load sees it.
const distributionRequest = apiFetch(base).then((d) => (distribution = d));
const firstLoad = load(Promise.all([peopleRequest, distributionRequest.catch(() => {})]));
people = await peopleRequest;
try {
  distribution = await distributionRequest;
  document.getElementById('order-title').textContent = `Pedido: ${distribution.name}`;
  document.getElementById('order-meta').innerHTML = statusBadge(distribution.status);
} catch (err) {
  fail(err);
}
await firstLoad;

unitForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  if (!changing) return;
  const choices = {};
  for (const select of document.querySelectorAll('#eu-options select[data-option]')) {
    choices[select.dataset.option] = select.value;
  }
  const body = changing.article_id
    ? { quantity: Number(document.getElementById('eu-quantity').value) }
    : { choices };
  try {
    await apiFetch(`${base}/units/${changing.id}`, { method: 'PATCH', body: JSON.stringify(body) });
    unitDialog.close();
    showFeedback('Cambio guardado.');
    await load();
  } catch (err) {
    fail(err);
  }
});
