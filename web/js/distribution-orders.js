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
const list = document.getElementById('orders-list');
const searchBox = document.getElementById('orders-search');
const showDone = document.getElementById('show-done');
const personInput = document.getElementById('pf-person');
const assignBox = document.getElementById('pf-assign');

// What was typed in a row's fields and isn't acted on yet, so that drawing the
// list again after any action doesn't lose it.
const drafts = new Map();

function groupByPerson(all) {
  const groups = new Map();
  for (const u of all) {
    if (!groups.has(u.beneficiary_login))
      groups.set(u.beneficiary_login, { name: u.beneficiary_name, units: [] });
    groups.get(u.beneficiary_login).units.push(u);
  }
  return [...groups.entries()];
}

const unitTitle = (u) => {
  const what = u.article_name
    ? `${u.article_name} × ${u.quantity}`
    : choicesText(u.choices, hasOptions());
  return u.is_extra ? `${what} (extra)` : what;
};

// A row's state in a few lines.
function standing(u) {
  const article = articleOf(u);
  const lines = [];
  if (isNumbered(u)) {
    const unnumbered = u.quantity - assigned(u).length;
    lines.push(
      `Con número: ${assigned(u).length ? formatIdentifiers(article, assigned(u)) : 'ninguno'} (${assigned(u).length} de ${u.quantity})${unnumbered ? ` · sin número: ${unnumbered}` : ''}`,
    );
    lines.push(
      `Pagados: ${paidIds(u).length ? formatIdentifiers(article, paidIds(u)) : 'ninguno'} · Entregados: ${deliveredIds(u).length ? formatIdentifiers(article, deliveredIds(u)) : 'ninguno'}`,
    );
  } else {
    const parts = [];
    if (u.price > 0) parts.push(`pagadas ${u.paid_quantity} de ${u.quantity}`);
    parts.push(`entregadas ${u.picked_up_quantity} de ${u.quantity}`);
    lines.push(parts.join(' · '));
  }
  if (u.picked_up_at) {
    const to =
      u.picked_up_by_login === u.beneficiary_login ? '' : `, a ${nameOf(u.picked_up_by_login)}`;
    lines.push(
      `Última entrega: ${formatDateTime(u.picked_up_at)}${to} (apuntado por ${nameOf(u.pickup_recorded_by_login)})`,
    );
  }
  return lines;
}

function button(act, u, label, cls = 'btn-secondary', title = '') {
  return `<button type="button" class="btn ${cls} btn-small" data-act="${act}" data-unit="${u.id}"${title ? ` title="${escapeHtml(title)}"` : ''}>${label}</button>`;
}

function group(label, buttons) {
  const shown = buttons.filter(Boolean);
  return shown.length ? `<span class="hint">${label}:</span> ${shown.join(' ')}` : '';
}

// The controls of a row: change the order, number it, pay, hand out, return.
function controls(u) {
  const parts = [];
  const orderButtons = [];
  if (canEditOrder() && (u.picked_up_quantity === 0 || u.article_id)) {
    if (hasOptions() || u.article_id) orderButtons.push(button('change', u, 'Cambiar'));
    const removable =
      u.picked_up_quantity === 0 && (u.is_extra || distribution.audience === 'signup');
    if (removable) orderButtons.push(button('delete', u, 'Quitar pedido', 'btn-danger'));
  }
  if (orderButtons.length) parts.push(group('Pedido', orderButtons));

  if (isNumbered(u)) {
    const draft = drafts.get(`sel-${u.id}`) ?? '';
    const missing = u.quantity - assigned(u).length;
    const field = `<input type="text" size="34" data-sel-for="${u.id}" value="${escapeHtml(draft)}" placeholder="Números${isDescribed(u) ? ' (0001-0010, 0015)' : ''}" aria-label="Números de ${escapeHtml(u.article_name)}">`;
    const numbering = group('Números', [
      button('add', u, 'Asignar', 'btn-secondary', 'Da al pedido los números escritos'),
      isDescribed(u) && missing > 0
        ? button(
            'next',
            u,
            `Asignar los ${missing === 1 ? 'siguiente libre' : `${missing} siguientes libres`}`,
            'btn-primary',
          )
        : '',
      button(
        'remove',
        u,
        'Quitar números',
        'btn-secondary',
        'Quita los escritos (si no están pagados ni entregados): quedan libres',
      ),
    ]);
    const pay =
      canMarkPaid && u.price > 0
        ? group('Pago', [
            unpaidAssigned(u).length ? button('pay', u, 'Marcar pagados', 'btn-primary') : '',
            paidIds(u).length ? button('unpay', u, 'Quitar pago') : '',
          ])
        : '';
    const delivery = canDeliver()
      ? group('Entrega', [
          u.quantity - u.picked_up_quantity > 0
            ? button('deliver', u, 'Entregar', 'btn-primary')
            : '',
          u.picked_up_quantity > 0
            ? button(
                'undeliver',
                u,
                'Deshacer entrega',
                'btn-secondary',
                'Vuelven a estar sin entregar, con su número',
              )
            : '',
          returnable(u) > 0
            ? button(
                'return',
                u,
                'Devolver',
                'btn-danger',
                'Salen del pedido y su número queda libre',
              )
            : '',
        ])
      : '';
    parts.push(`<div>${field}</div>`, numbering, pay, delivery);
  } else {
    const draft = drafts.get(`qty-${u.id}`) ?? '';
    const count =
      u.quantity > 1
        ? `<input type="number" min="1" max="${u.quantity}" step="1" size="4" data-qty-for="${u.id}" value="${escapeHtml(draft)}" placeholder="todas" aria-label="Unidades">`
        : '';
    const pay =
      canMarkPaid && u.price > 0
        ? group('Pago', [
            payable(u) > 0
              ? button('pay', u, u.quantity > 1 ? 'Marcar pagadas' : 'Marcar pagado', 'btn-primary')
              : '',
            u.paid_quantity > 0
              ? button('unpay', u, u.quantity > 1 ? 'Quitar pagadas' : 'Quitar pago')
              : '',
          ])
        : '';
    const delivery = canDeliver()
      ? group('Entrega', [
          u.quantity - u.picked_up_quantity > 0
            ? button('deliver', u, 'Entregar', 'btn-primary')
            : '',
          u.picked_up_quantity > 0
            ? button(
                'undeliver',
                u,
                'Deshacer entrega',
                'btn-secondary',
                'Vuelven a estar sin entregar',
              )
            : '',
          returnable(u) > 0
            ? button('return', u, 'Devolver', 'btn-danger', 'Salen del pedido')
            : '',
        ])
      : '';
    parts.push(count ? `<div>${count}</div>` : '', pay, delivery);
  }
  return `<div class="table-actions">${parts.filter(Boolean).join(' ')}</div>`;
}

function unitBlock(u) {
  const by =
    u.registered_by_login && u.registered_by_login !== u.beneficiary_login
      ? ` · apuntado por ${nameOf(u.registered_by_login)} el ${formatDate(u.registered_at)}`
      : '';
  const cost = u.price > 0 ? ` · ${formatCurrency(unitCost(u))}` : '';
  const unchosen = isUnchosen(u, hasOptions()) ? ' · <strong>sin elegir</strong>' : '';
  return `<li class="order-unit">
    <div><strong>${escapeHtml(unitTitle(u))}</strong>${escapeHtml(cost)}${escapeHtml(by)}${unchosen}</div>
    ${standing(u)
      .map((line) => `<div class="hint">${escapeHtml(line)}</div>`)
      .join('')}
    ${controls(u)}
  </li>`;
}

function paymentCell(login, units) {
  const toPay = units.filter((u) => u.price > 0);
  if (toPay.length === 0) return 'Gratis';
  const total = toPay.reduce((sum, u) => sum + unitCost(u), 0);
  const paid = toPay.reduce((sum, u) => sum + u.price * u.paid_quantity, 0);
  const summary =
    paid === 0
      ? `Pendiente: ${formatCurrency(total)}`
      : paid === total
        ? 'Pagado'
        : `Pagado ${formatCurrency(paid)} · pendiente ${formatCurrency(total - paid)}`;
  const due = toPay.reduce((sum, u) => sum + payableCost(u), 0);
  const quick =
    canMarkPaid && due > 0
      ? `<button type="button" class="btn btn-primary btn-small" data-pay-person="${escapeHtml(login)}">Marcar todo lo posible como pagado (${formatCurrency(due)})</button>`
      : '';
  return `<div>${escapeHtml(summary)}</div><div class="table-actions">${quick}</div>`;
}

function render() {
  const needle = searchBox.value.trim().toLowerCase();
  const groups = groupByPerson(units).filter(
    ([, g]) =>
      (!needle || g.name.toLowerCase().includes(needle)) &&
      (showDone.checked || g.units.some((u) => !finished(u))),
  );
  list.innerHTML = groups.length
    ? groups
        .map(
          ([login, g]) => `
        <tr>
          <td>${escapeHtml(g.name)}</td>
          <td><ul class="plain-list">${g.units.map(unitBlock).join('')}</ul></td>
          <td>${formatCurrency(g.units.reduce((sum, u) => sum + unitCost(u), 0))}<br>${paymentCell(login, g.units)}</td>
        </tr>`,
        )
        .join('')
    : `<tr><td colspan="3">${
        units.length
          ? 'Nadie con ese filtro.'
          : provisional
            ? 'Los pedidos de cada socio se crean al abrir el reparto.'
            : 'Todavía no ha pedido nadie.'
      }</td></tr>`;

  const total = units.reduce((sum, u) => sum + u.quantity, 0);
  const paid = units.reduce((sum, u) => sum + u.paid_quantity, 0);
  const handedOut = units.reduce((sum, u) => sum + u.picked_up_quantity, 0);
  const numbered = units.filter(isNumbered).reduce((sum, u) => sum + assigned(u).length, 0);
  const numberedTotal = units.filter(isNumbered).reduce((sum, u) => sum + u.quantity, 0);
  const parts = [`${total} unidades pedidas`];
  if (units.some((u) => u.price > 0)) parts.push(`pagadas ${paid}`);
  parts.push(`entregadas ${handedOut}`);
  if (numberedTotal) parts.push(`con número ${numbered} de ${numberedTotal}`);
  document.getElementById('orders-progress').textContent = units.length
    ? `${parts.join(' · ')}.`
    : '';
  document.getElementById('pf-assign-field').hidden = !(distribution?.articles ?? []).some(
    (a) => a.identifier_fields.length > 0,
  );
}

searchBox.addEventListener('input', render);
showDone.addEventListener('change', render);

list.addEventListener('input', (event) => {
  const sel = event.target.closest('input[data-sel-for]');
  if (sel) drafts.set(`sel-${sel.dataset.selFor}`, sel.value);
  const qty = event.target.closest('input[data-qty-for]');
  if (qty) drafts.set(`qty-${qty.dataset.qtyFor}`, qty.value);
});

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

// ----- the actions -----
// What a row's fields say: the numbers typed, or how many units.
function selection(u) {
  const field = list.querySelector(`input[data-sel-for="${u.id}"]`);
  return field
    ? field.value
        .split(/[,\n]/)
        .map((v) => v.trim())
        .filter(Boolean)
    : [];
}

function quantityOf(u) {
  const field = list.querySelector(`input[data-qty-for="${u.id}"]`);
  if (!field || field.value.trim() === '') return null;
  return Number(field.value);
}

const encodeList = (items) => encodeURIComponent(items.join(','));

// The request an action makes, or null when something is missing (said already).
async function perform(act, u) {
  const numbered = isNumbered(u);
  const sel = numbered ? selection(u) : [];
  const quantity = numbered ? null : quantityOf(u);
  if (quantity !== null && (!Number.isInteger(quantity) || quantity < 1)) {
    showError('Pon cuántas unidades (un número entero de 1 en adelante), o déjalo en blanco.');
    return false;
  }
  const withWhich = numbered
    ? sel.length
      ? { identifiers: sel }
      : {}
    : quantity
      ? { quantity }
      : {};
  const which = numbered
    ? sel.length
      ? `${sel.length === 1 ? 'el número' : 'los números'} ${sel.join(', ')}`
      : 'todo lo que se pueda'
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
    case 'add':
    case 'remove':
      if (sel.length === 0) {
        showError('Escribe los números, por ejemplo «0001-0010, 0015».');
        return false;
      }
      if (act === 'add') {
        await apiFetch(`${base}/units/${u.id}/identifiers`, {
          method: 'POST',
          body: JSON.stringify({ identifiers: sel }),
        });
        showFeedback('Números asignados.');
      } else {
        if (!confirm(`¿Quitar los números ${sel.join(', ')} del pedido? Quedan libres.`))
          return false;
        await apiFetch(`${base}/units/${u.id}/identifiers?identifiers=${encodeList(sel)}`, {
          method: 'DELETE',
        });
        showFeedback('Números quitados.');
      }
      return true;
    case 'next':
      await apiFetch(`${base}/units/${u.id}/identifiers/assign`, { method: 'POST' });
      showFeedback('Números asignados.');
      return true;
    case 'pay':
      await apiFetch(`${base}/payments`, {
        method: 'POST',
        body: JSON.stringify({ payments: [{ unit_id: u.id, ...withWhich }] }),
      });
      showFeedback('Marcado como pagado.');
      return true;
    case 'unpay': {
      const query = numbered
        ? sel.length
          ? `?identifiers=${encodeList(sel)}`
          : ''
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
      const login = resolvePickerId(people.entries, personInput.value);
      if (!login) {
        showError('Elige antes, arriba, a la persona que recoge («Entregado a»).');
        return false;
      }
      if (
        u.price > 0 &&
        u.paid_quantity < u.quantity &&
        !confirm('Hay unidades de este pedido sin pagar. ¿Entregar igualmente?')
      )
        return false;
      await apiFetch(`${base}/pickups`, {
        method: 'POST',
        body: JSON.stringify({
          deliveries: [{ unit_id: u.id, ...withWhich }],
          picked_up_by_login: login,
          assign_numbers: assignBox.checked,
        }),
      });
      showFeedback(`Entregado a ${nameOf(login)}.`);
      return true;
    }
    case 'undeliver': {
      if (!confirm(`¿Deshacer la entrega de ${which}? Siguen en el pedido, sin entregar.`))
        return false;
      const query = numbered
        ? sel.length
          ? `?identifiers=${encodeList(sel)}`
          : ''
        : quantity
          ? `?quantity=${quantity}`
          : '';
      await apiFetch(`${base}/units/${u.id}/pickup${query}`, { method: 'DELETE' });
      showFeedback('Entrega deshecha.');
      return true;
    }
    case 'return': {
      if (numbered && sel.length === 0) {
        showError('Escribe cuáles se devuelven («0001-0010, 0015»).');
        return false;
      }
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
      showFeedback('Devolución apuntada.');
      return true;
    }
    default:
      return false;
  }
}

list.addEventListener('click', async (event) => {
  const pay = event.target.closest('button[data-pay-person]');
  if (pay) {
    const mine = units.filter(
      (x) => x.beneficiary_login === pay.dataset.payPerson && x.price > 0 && payable(x) > 0,
    );
    pay.disabled = true;
    try {
      await apiFetch(`${base}/payments`, {
        method: 'POST',
        body: JSON.stringify({ unit_ids: mine.map((x) => x.id) }),
      });
      showFeedback('Marcado como pagado.');
      await load();
    } catch (err) {
      pay.disabled = false;
      fail(err);
    }
    return;
  }
  const b = event.target.closest('button[data-act]');
  if (!b) return;
  const u = units.find((x) => x.id === Number(b.dataset.unit));
  b.disabled = true;
  try {
    if (await perform(b.dataset.act, u)) {
      drafts.delete(`sel-${u.id}`);
      drafts.delete(`qty-${u.id}`);
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
document.getElementById('pf-person-options').innerHTML = people.entries
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
