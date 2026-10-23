import {
  apiFetch,
  currentUser,
  escapeHtml,
  formatCurrency,
  formatDate,
  hasAnyRole,
  showError,
  showFeedback,
} from './api.js';
import { initPage } from './layout.js';
import { initModal, openModal } from './modal.js';
import {
  apiErrorText,
  articleLeft,
  choicesText,
  formatDateTime,
  formatIdentifiers,
  hasArticles,
  isUnchosen,
  loadPeople,
  resolvePickerId,
  statusBadge,
  unitCost,
} from './distributionCommon.js';

// Everything about a distribution's orders in one place, for the people who put
// the order together (admin, board) and hand it out (material): what has to be
// bought and, for each member, what they asked for, how it is numbered, what is
// paid and what was handed out — and the actions on all of it.
await initPage({ requireAnyRole: ['admin', 'board', 'material'] });
const canMarkPaid = hasAnyRole(currentUser(), ['admin', 'board']);

const id = Number(new URLSearchParams(window.location.search).get('id'));
const base = `/distributions/${id}`;
document.getElementById('back-link').href = `/reparto?id=${id}`;

let distribution = null;
let units = [];
let people = { entries: [], names: {} };
const fail = (err) => showError(apiErrorText(err));
const nameOf = (login) => people.names[login] ?? login;

const hasOptions = () => (distribution?.options.length ?? 0) > 0;
const articleOf = (u) => distribution?.articles?.find((a) => a.id === u.article_id);
// Items with a number of their own (free text or described), and the ones whose
// numbers are all known so the next free can be given.
const isNumbered = (u) => Boolean(articleOf(u)?.has_identifiers);
const isDescribed = (u) => (articleOf(u)?.identifier_fields ?? []).length > 0;
const canDeliver = () => distribution?.status !== 'draft';
const canEditOrder = () => distribution?.status === 'open';

// How a row stands. For a numbered article each item is paid or delivered by
// itself, and only those with a number can be; elsewhere it is counted.
const assigned = (u) => u.identifiers ?? [];
const paidIds = (u) => u.paid_identifiers ?? [];
const deliveredIds = (u) => u.delivered_identifiers ?? [];
const unpaidAssigned = (u) => assigned(u).filter((i) => !paidIds(u).includes(i));
const returnableIds = (u) => deliveredIds(u).filter((i) => !paidIds(u).includes(i));

// What can be paid, handed out or returned from a row, in units.
const payable = (u) => (isNumbered(u) ? unpaidAssigned(u).length : u.quantity - u.paid_quantity);
const returnable = (u) =>
  isNumbered(u)
    ? returnableIds(u).length
    : Math.min(u.picked_up_quantity, u.quantity - u.paid_quantity);
const payableCost = (u) => payable(u) * u.price;
const finished = (u) => u.paid_quantity === u.quantity && u.picked_up_quantity === u.quantity;

// ----- what has to be bought -----
let provisional = false;

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
  const missing = hasOptions()
    ? order.lines.filter(noChoices).reduce((sum, l) => sum + l.count, 0)
    : 0;
  const missingText =
    missing === 0
      ? ''
      : missing === 1
        ? ' Falta por elegir 1 pedido.'
        : ` Faltan por elegir ${missing} pedidos.`;
  const draftText = provisional
    ? ' Es un borrador: se cuenta un pedido por cada socio en la fecha de corte, y se crearán al abrir el reparto.'
    : '';
  document.getElementById('order-totals').textContent =
    `${order.total_units} pedidos en total, por ${formatCurrency(order.total_amount)}.${missingText}${draftText}`;
}

// ----- the orders of each member -----
// One line for each order with a summary (how many, how many paid, how many handed
// out) and, unfolded, the detail: the numbers to give it, a list of its numbered
// items to choose from, and the actions.
const list = document.getElementById('orders-list');
const searchBox = document.getElementById('orders-search');
const showDone = document.getElementById('show-done');

// Orders unfolded, what is ticked in each (numbers), and what was typed in the
// fields: all of it survives drawing the list again after any action.
const unfolded = new Set();
const ticked = new Map();
const typed = new Map();

const tickedOf = (u) => {
  if (!ticked.has(u.id)) ticked.set(u.id, new Set());
  return ticked.get(u.id);
};

const unitTitle = (u) => {
  const what = u.article_name
    ? `${u.article_name} × ${u.quantity}`
    : choicesText(u.choices, hasOptions());
  return u.is_extra ? `${what} (extra)` : what;
};

// The line of an order: what, how many are paid and handed out, what it costs.
function summaryRow(u) {
  const article = articleOf(u);
  const extra = [];
  if (u.registered_by_login && u.registered_by_login !== u.beneficiary_login)
    extra.push(`apuntado por ${nameOf(u.registered_by_login)} el ${formatDate(u.registered_at)}`);
  if (isUnchosen(u, hasOptions())) extra.push('sin elegir');
  const numbers = isNumbered(u)
    ? `<div class="hint">${assigned(u).length} de ${u.quantity} con número${assigned(u).length ? `: ${escapeHtml(formatIdentifiers(article, assigned(u)))}` : ''}</div>`
    : '';
  const open = unfolded.has(u.id);
  const paid = u.price > 0 ? `${u.paid_quantity} de ${u.quantity}` : '—';
  const cost = u.price > 0 ? formatCurrency(unitCost(u)) : 'Gratis';
  return `<tr class="order-row" data-order="${u.id}">
    <td>${escapeHtml(u.beneficiary_name)}</td>
    <td><strong>${escapeHtml(unitTitle(u))}</strong>${extra.length ? `<div class="hint">${escapeHtml(extra.join(' · '))}</div>` : ''}${numbers}</td>
    <td>${u.picked_up_quantity} de ${u.quantity}</td>
    <td>${paid}</td>
    <td>${cost}</td>
    <td><button type="button" class="btn btn-secondary btn-small" data-toggle="${u.id}" aria-expanded="${open}">${open ? 'Ocultar' : 'Más información'}</button></td>
  </tr>`;
}

function button(act, u, label, cls = 'btn-secondary', title = '') {
  return `<button type="button" class="btn ${cls} btn-small" data-act="${act}" data-unit="${u.id}"${title ? ` title="${escapeHtml(title)}"` : ''}>${label}</button>`;
}

// The numbered items of an order, each with its state, to tick.
function itemList(u) {
  const article = articleOf(u);
  const chosen = tickedOf(u);
  const items = assigned(u)
    .map((identifier) => {
      const badges = [
        paidIds(u).includes(identifier) ? '<span class="badge">pagado</span>' : '',
        deliveredIds(u).includes(identifier) ? '<span class="badge">entregado</span>' : '',
      ].join(' ');
      return `<label class="checkbox-label" style="display:inline-flex;gap:.35rem;align-items:center;white-space:nowrap">
        <input type="checkbox" data-item="${escapeHtml(identifier)}" data-unit="${u.id}"${chosen.has(identifier) ? ' checked' : ''}>
        ${escapeHtml(formatIdentifiers(article, [identifier]))} ${badges}
      </label>`;
    })
    .join('');
  return `<div style="display:flex;flex-wrap:wrap;gap:.35rem 1rem;max-height:14rem;overflow:auto;padding:.5rem;border:1px solid var(--border, #ddd);border-radius:6px">${items}</div>`;
}

function selectors(u) {
  const pick = (kind, label) =>
    `<button type="button" class="link-button" data-pick="${kind}" data-unit="${u.id}">${label}</button>`;
  const parts = [
    pick('all', 'todos'),
    pick('none', 'ninguno'),
    pick('unpaid', 'sin pagar'),
    pick('undelivered', 'sin entregar'),
  ];
  if (paidIds(u).length) parts.push(pick('paid', 'pagados'));
  if (deliveredIds(u).length) parts.push(pick('delivered', 'entregados'));
  return `<span class="hint">Marcar:</span> ${parts.join(' · ')} <span class="hint" data-count-for="${u.id}">${tickedOf(u).size} marcados</span>`;
}

// What the person can do with the order, in the order it is wanted: give it
// numbers, choose among its numbered items, change the order itself.
function detail(u) {
  const blocks = [];
  if (isNumbered(u)) {
    const missing = u.quantity - assigned(u).length;
    const field = typed.get(`add-${u.id}`) ?? '';
    if (missing > 0) {
      blocks.push(`<div class="table-actions"><strong>Sin número: ${missing}.</strong>
        ${isDescribed(u) ? button('next', u, `Asignar ${missing === 1 ? 'el siguiente libre' : `los ${missing} siguientes libres`}`, 'btn-primary') : ''}
        <input type="text" size="26" data-add-for="${u.id}" value="${escapeHtml(field)}" placeholder="Números concretos${isDescribed(u) ? ' (0001-0010, 0015)' : ''}" aria-label="Números concretos">
        ${button('add', u, 'Asignar estos')}
      </div>`);
    }
    if (assigned(u).length) {
      const pay =
        canMarkPaid && u.price > 0
          ? `${button('pay', u, 'Marcar pagados', 'btn-primary')} ${button('unpay', u, 'Quitar pago')}`
          : '';
      const delivery = canDeliver()
        ? `${button('deliver', u, 'Entregar', 'btn-primary')} ${button('undeliver', u, 'Deshacer entrega', 'btn-secondary', 'Vuelven a estar sin entregar, con su número')} ${button('return', u, 'Devolver', 'btn-danger', 'Salen del pedido y su número queda libre (solo lo entregado y sin pagar)')}`
        : '';
      blocks.push(`<div>${selectors(u)}</div>${itemList(u)}
        <div class="table-actions"><span class="hint">Con lo marcado:</span> ${pay} ${delivery} ${button('remove', u, 'Quitar números', 'btn-secondary', 'Quita el número a lo marcado (si no está pagado ni entregado): queda libre')}</div>`);
    }
  } else {
    const count =
      u.quantity > 1
        ? `<input type="number" min="1" max="${u.quantity}" step="1" size="4" data-qty-for="${u.id}" value="${escapeHtml(typed.get(`qty-${u.id}`) ?? '')}" placeholder="todas" aria-label="Unidades">`
        : '';
    const pay =
      canMarkPaid && u.price > 0
        ? `${payable(u) > 0 ? button('pay', u, u.quantity > 1 ? 'Marcar pagadas' : 'Marcar pagado', 'btn-primary') : ''} ${u.paid_quantity > 0 ? button('unpay', u, u.quantity > 1 ? 'Quitar pagadas' : 'Quitar pago') : ''}`
        : '';
    const delivery = canDeliver()
      ? `${u.quantity - u.picked_up_quantity > 0 ? button('deliver', u, 'Entregar', 'btn-primary') : ''} ${u.picked_up_quantity > 0 ? button('undeliver', u, 'Deshacer entrega', 'btn-secondary', 'Vuelven a estar sin entregar') : ''} ${returnable(u) > 0 ? button('return', u, 'Devolver', 'btn-danger', 'Salen del pedido') : ''}`
      : '';
    blocks.push(`<div class="table-actions">${count} ${pay} ${delivery}</div>`);
  }
  const order = [];
  if (canEditOrder() && (u.picked_up_quantity === 0 || u.article_id)) {
    if (hasOptions() || u.article_id) order.push(button('change', u, 'Cambiar el pedido'));
    if (u.picked_up_quantity === 0 && (u.is_extra || distribution.audience === 'signup'))
      order.push(button('delete', u, 'Quitar el pedido', 'btn-danger'));
  }
  if (canMarkPaid && u.price > 0 && payableCost(u) > 0)
    order.push(button('payall', u, `Pagar todo lo posible (${formatCurrency(payableCost(u))})`));
  if (order.length)
    blocks.push(
      `<div class="table-actions"><span class="hint">Pedido:</span> ${order.join(' ')}</div>`,
    );

  const last = u.picked_up_at
    ? `<div class="hint">Última entrega: ${escapeHtml(formatDateTime(u.picked_up_at))}${u.picked_up_by_login === u.beneficiary_login ? '' : `, a ${escapeHtml(nameOf(u.picked_up_by_login))}`} (apuntado por ${escapeHtml(nameOf(u.pickup_recorded_by_login))})</div>`
    : '';
  return `<tr class="order-detail" data-detail="${u.id}"><td></td><td colspan="5">${last}${blocks.join('')}</td></tr>`;
}

function render() {
  const needle = searchBox.value.trim().toLowerCase();
  const shown = units.filter(
    (u) =>
      (!needle ||
        u.beneficiary_name.toLowerCase().includes(needle) ||
        assigned(u).some((i) => i.toLowerCase().includes(needle))) &&
      (showDone.checked || !finished(u)),
  );
  // What is ticked can only be what the order has, still.
  for (const u of units) {
    const chosen = tickedOf(u);
    for (const identifier of [...chosen])
      if (!assigned(u).includes(identifier)) chosen.delete(identifier);
  }
  list.innerHTML = shown.length
    ? shown.map((u) => summaryRow(u) + (unfolded.has(u.id) ? detail(u) : '')).join('')
    : `<tr><td colspan="6">${
        units.length
          ? 'Nada con ese filtro.'
          : provisional
            ? 'Los pedidos de cada socio se crean al abrir el reparto.'
            : 'Todavía no ha pedido nadie.'
      }</td></tr>`;

  const total = units.reduce((sum, u) => sum + u.quantity, 0);
  const paid = units.reduce((sum, u) => sum + u.paid_quantity, 0);
  const handedOut = units.reduce((sum, u) => sum + u.picked_up_quantity, 0);
  const numbered = units.filter(isNumbered).reduce((sum, u) => sum + assigned(u).length, 0);
  const numberedTotal = units.filter(isNumbered).reduce((sum, u) => sum + u.quantity, 0);
  const parts = [`${units.length} pedidos, ${total} unidades`];
  if (units.some((u) => u.price > 0)) parts.push(`pagadas ${paid}`);
  parts.push(`entregadas ${handedOut}`);
  if (numberedTotal) parts.push(`con número ${numbered} de ${numberedTotal}`);
  document.getElementById('orders-progress').textContent = units.length
    ? `${parts.join(' · ')}.`
    : '';
}

searchBox.addEventListener('input', render);
showDone.addEventListener('change', render);

// ----- editing the order -----
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

function openChange(u) {
  changing = u;
  document.getElementById('edit-unit-hint').textContent = `Para ${u.beneficiary_name}.`;
  const byQuantity = Boolean(u.article_id);
  document.getElementById('edit-unit-title').textContent = byQuantity
    ? `Cambiar la cantidad de ${u.article_name}`
    : 'Cambiar lo elegido';
  document.getElementById('eu-quantity-field').hidden = !byQuantity;
  document.getElementById('eu-quantity').value = u.quantity;
  document.getElementById('eu-options').innerHTML = byQuantity ? '' : optionSelects(u.choices);
  openModal(unitDialog);
}

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

// ----- handing out: who gets it -----
const deliverDialog = document.getElementById('deliver-dialog');
const deliverForm = document.getElementById('deliver-form');
const deliverPerson = document.getElementById('dd-person');
initModal(deliverDialog, deliverForm);
let delivering = null;

deliverForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  if (!delivering) return;
  const login = resolvePickerId(people.entries, deliverPerson.value);
  if (!login) {
    showError('Elige a la persona que recoge de la lista.');
    return;
  }
  const { u, withWhich, unpaid } = delivering;
  if (unpaid && !confirm('Hay unidades sin pagar. ¿Entregar igualmente?')) return;
  try {
    await apiFetch(`${base}/pickups`, {
      method: 'POST',
      body: JSON.stringify({
        deliveries: [{ unit_id: u.id, ...withWhich }],
        picked_up_by_login: login,
      }),
    });
    deliverDialog.close();
    showFeedback(`Entregado a ${nameOf(login)}.`);
    await load();
  } catch (err) {
    fail(err);
  }
});

// ----- the actions -----
const encodeList = (items) => encodeURIComponent(items.join(','));

function quantityOf(u) {
  const field = list.querySelector(`input[data-qty-for="${u.id}"]`);
  if (!field || field.value.trim() === '') return null;
  return Number(field.value);
}

// What an action does, said already when something is missing; true when it
// changed something and the list has to be read again.
async function perform(act, u) {
  const numbered = isNumbered(u);
  const chosen = [...tickedOf(u)];
  const quantity = numbered ? null : quantityOf(u);
  if (quantity !== null && (!Number.isInteger(quantity) || quantity < 1)) {
    showError('Pon cuántas unidades (un número entero de 1 en adelante), o déjalo en blanco.');
    return false;
  }
  // Which units an action is about: the ticked numbers, or a count.
  const needsTicks = ['pay', 'unpay', 'deliver', 'undeliver', 'return', 'remove'].includes(act);
  if (numbered && needsTicks && chosen.length === 0) {
    showError('Marca antes los números a los que se refiere.');
    return false;
  }
  const withWhich = numbered ? { identifiers: chosen } : quantity ? { quantity } : {};
  const which = numbered
    ? `${chosen.length} ${chosen.length === 1 ? 'unidad' : 'unidades'}`
    : quantity
      ? `${quantity} unidades`
      : 'todo lo que se pueda';
  switch (act) {
    case 'change':
      openChange(u);
      return false;
    case 'delete':
      if (!confirm('¿Quitar este pedido?')) return false;
      await apiFetch(`${base}/units/${u.id}`, { method: 'DELETE' });
      showFeedback('Pedido quitado.');
      return true;
    case 'add': {
      const text = (typed.get(`add-${u.id}`) ?? '')
        .split(/[,\n]/)
        .map((v) => v.trim())
        .filter(Boolean);
      if (text.length === 0) {
        showError('Escribe los números, por ejemplo «0001-0010, 0015».');
        return false;
      }
      await apiFetch(`${base}/units/${u.id}/identifiers`, {
        method: 'POST',
        body: JSON.stringify({ identifiers: text }),
      });
      typed.delete(`add-${u.id}`);
      showFeedback('Números asignados.');
      return true;
    }
    case 'next':
      await apiFetch(`${base}/units/${u.id}/identifiers/assign`, { method: 'POST' });
      showFeedback('Números asignados.');
      return true;
    case 'remove':
      if (!confirm(`¿Quitar el número a ${which}? Quedan libres.`)) return false;
      await apiFetch(`${base}/units/${u.id}/identifiers?identifiers=${encodeList(chosen)}`, {
        method: 'DELETE',
      });
      for (const identifier of chosen) tickedOf(u).delete(identifier);
      showFeedback('Números quitados.');
      return true;
    case 'pay':
      await apiFetch(`${base}/payments`, {
        method: 'POST',
        body: JSON.stringify({ payments: [{ unit_id: u.id, ...withWhich }] }),
      });
      showFeedback('Marcado como pagado.');
      return true;
    case 'payall':
      await apiFetch(`${base}/payments`, {
        method: 'POST',
        body: JSON.stringify({ unit_ids: [u.id] }),
      });
      showFeedback('Marcado como pagado.');
      return true;
    case 'unpay': {
      const query = numbered
        ? `?identifiers=${encodeList(chosen)}`
        : quantity
          ? `?quantity=${quantity}`
          : '';
      if (
        !numbered &&
        !quantity &&
        u.quantity > 1 &&
        !confirm('¿Quitar todo lo pagado de este pedido?')
      )
        return false;
      await apiFetch(`${base}/units/${u.id}/payment${query}`, { method: 'DELETE' });
      showFeedback('Marca de pagado quitada.');
      return true;
    }
    case 'deliver': {
      // Who gets it is asked in a dialog; the delivery is made when it is accepted.
      const unpaid = numbered
        ? chosen.some((i) => !paidIds(u).includes(i))
        : u.paid_quantity < u.quantity;
      delivering = { u, withWhich, unpaid: u.price > 0 && unpaid };
      document.getElementById('deliver-hint').textContent = `${which} de ${u.beneficiary_name}.`;
      // The member the order is for is the natural default for who collects it.
      const owner = people.entries.find((e) => e.id === u.beneficiary_login);
      deliverPerson.value = owner ? owner.label : '';
      openModal(deliverDialog);
      return false;
    }
    case 'undeliver': {
      if (!confirm(`¿Deshacer la entrega de ${which}? Siguen en el pedido, sin entregar.`))
        return false;
      const query = numbered
        ? `?identifiers=${encodeList(chosen)}`
        : quantity
          ? `?quantity=${quantity}`
          : '';
      await apiFetch(`${base}/units/${u.id}/pickup${query}`, { method: 'DELETE' });
      showFeedback('Entrega deshecha.');
      return true;
    }
    case 'return': {
      if (
        !confirm(
          `¿Devolver ${which}? Salen del pedido y, si tienen número, queda libre. No se puede deshacer.`,
        )
      )
        return false;
      await apiFetch(`${base}/units/${u.id}/return`, {
        method: 'POST',
        body: JSON.stringify(withWhich),
      });
      for (const identifier of chosen) tickedOf(u).delete(identifier);
      showFeedback('Devolución apuntada.');
      return true;
    }
    default:
      return false;
  }
}

// Ticks the numbers of an order that have some state, in the page and in memory.
function pick(u, kind) {
  const paid = paidIds(u);
  const delivered = deliveredIds(u);
  const rules = {
    all: () => true,
    none: () => false,
    unpaid: (i) => !paid.includes(i),
    undelivered: (i) => !delivered.includes(i),
    paid: (i) => paid.includes(i),
    delivered: (i) => delivered.includes(i),
  };
  const chosen = tickedOf(u);
  chosen.clear();
  for (const identifier of assigned(u)) if (rules[kind](identifier)) chosen.add(identifier);
  for (const box of list.querySelectorAll(`input[data-item][data-unit="${u.id}"]`)) {
    box.checked = chosen.has(box.dataset.item);
  }
  updateCount(u);
}

function updateCount(u) {
  const label = list.querySelector(`[data-count-for="${u.id}"]`);
  if (label) label.textContent = `${tickedOf(u).size} marcados`;
}

list.addEventListener('change', (event) => {
  const box = event.target.closest('input[data-item]');
  if (!box) return;
  const u = units.find((x) => x.id === Number(box.dataset.unit));
  if (box.checked) tickedOf(u).add(box.dataset.item);
  else tickedOf(u).delete(box.dataset.item);
  updateCount(u);
});

list.addEventListener('input', (event) => {
  const add = event.target.closest('input[data-add-for]');
  if (add) typed.set(`add-${add.dataset.addFor}`, add.value);
  const qty = event.target.closest('input[data-qty-for]');
  if (qty) typed.set(`qty-${qty.dataset.qtyFor}`, qty.value);
});

list.addEventListener('click', async (event) => {
  const toggle = event.target.closest('button[data-toggle]');
  if (toggle) {
    const unitId = Number(toggle.dataset.toggle);
    if (unfolded.has(unitId)) unfolded.delete(unitId);
    else unfolded.add(unitId);
    render();
    return;
  }
  const picker = event.target.closest('button[data-pick]');
  if (picker) {
    pick(
      units.find((x) => x.id === Number(picker.dataset.unit)),
      picker.dataset.pick,
    );
    return;
  }
  const b = event.target.closest('button[data-act]');
  if (!b) return;
  const u = units.find((x) => x.id === Number(b.dataset.unit));
  b.disabled = true;
  try {
    if (await perform(b.dataset.act, u)) {
      typed.delete(`qty-${u.id}`);
      await load();
    } else {
      b.disabled = false;
    }
  } catch (err) {
    b.disabled = false;
    fail(err);
    // What failed may have changed what is left (numbers, units); draw it again.
    await load();
  }
});

// `ready` is what drawing needs besides this data (the people): the requests all
// go out at once and only the drawing waits.
async function load(ready = Promise.resolve()) {
  try {
    const requests = [apiFetch(`${base}/order`), apiFetch(`${base}/units`), ready];
    // With articles, what is left of each changes with the orders, so the
    // distribution is read again with them.
    if (distribution && hasArticles(distribution)) requests.push(apiFetch(base));
    const [order, all, , fresh] = await Promise.all(requests);
    if (fresh) distribution = fresh;
    units = all;
    renderOrder(order);
    render();
  } catch (err) {
    fail(err);
  }
}

const peopleRequest = loadPeople(id, 'pickup');
// Assigned as soon as it arrives, so the drawing of the first load sees it.
const distributionRequest = apiFetch(base).then((d) => (distribution = d));
const firstLoad = load(Promise.all([peopleRequest, distributionRequest.catch(() => {})]));
people = await peopleRequest;
document.getElementById('dd-person-options').innerHTML = people.entries
  .map((e) => `<option value="${escapeHtml(e.label)}"></option>`)
  .join('');
try {
  distribution = await distributionRequest;
  document.getElementById('orders-title').textContent = `Pedidos: ${distribution.name}`;
  document.title = `Pedidos: ${distribution.name} — Club de Espeleología Katiuskas`;
  document.getElementById('orders-meta').innerHTML = statusBadge(distribution.status);
} catch (err) {
  fail(err);
}
await firstLoad;
