import { apiFetch, escapeHtml, formatCurrency, showError, showFeedback } from './api.js';
import { initPage } from './layout.js';
import {
  apiErrorText,
  formatIdentifiers,
  isUnchosen,
  pendingCost,
  pendingQuantity,
  unitLabel,
  loadPeople,
  resolvePickerId,
  statusBadge,
  formatDateTime,
} from './distributionCommon.js';

// Who has collected and who is still to: for admin, board and material, once
// the order has arrived.
await initPage({ requireAnyRole: ['admin', 'board', 'material'] });

const id = Number(new URLSearchParams(window.location.search).get('id'));
const base = `/distributions/${id}`;
document.getElementById('back-link').href = `/reparto?id=${id}`;

let distribution = null;
let units = [];
let people = { entries: [], names: {} };
const selected = new Set();
const fail = (err) => showError(apiErrorText(err));
const nameOf = (login) => people.names[login] ?? login;

const list = document.getElementById('pickup-list');
const showPicked = document.getElementById('show-picked');
const form = document.getElementById('pickup-form');
const personInput = document.getElementById('pf-person');
const submit = document.getElementById('pf-submit');
const assignBox = document.getElementById('pf-assign');

const hasOptions = () => (distribution?.options.length ?? 0) > 0;

function canCollect() {
  return distribution?.status !== 'draft';
}

// How many units of a row are still to be handed out, and how many of those
// handed out can be returned (not the ones paid: the row can't end up smaller
// than what is paid).
const remaining = (u) => u.quantity - u.picked_up_quantity;
const returnable = (u) => Math.min(u.picked_up_quantity, u.quantity - (u.paid_quantity ?? 0));

// The last delivery of a row: when, to whom and who wrote it down.
function pickedUpText(u) {
  const when = formatDateTime(u.picked_up_at);
  const to =
    u.picked_up_by_login === u.beneficiary_login
      ? ''
      : `, entregado a ${nameOf(u.picked_up_by_login)}`;
  return `${u.picked_up_quantity > 1 || u.quantity > 1 ? 'Última entrega' : 'Recogido'} el ${when}${to} (apuntado por ${nameOf(u.pickup_recorded_by_login)})`;
}

// How a row stands: what was handed out (which numbers, when it has them), what
// is assigned but not handed out yet.
function standing(u) {
  const article = articleOfUnit(u);
  const parts = [];
  if (u.quantity > 1) parts.push(`entregadas ${u.picked_up_quantity} de ${u.quantity}`);
  else parts.push(u.picked_up_quantity ? 'recogido' : 'sin recoger');
  const delivered = u.delivered_identifiers ?? [];
  if (delivered.length) parts.push(`entregados: ${formatIdentifiers(article, delivered)}`);
  const waiting = (u.identifiers ?? []).filter((i) => !delivered.includes(i));
  if (waiting.length) parts.push(`asignados sin entregar: ${formatIdentifiers(article, waiting)}`);
  return parts.join(' · ');
}

// The counter of a row and what can be done with that many: hand them out, undo
// the delivery or take them back.
function deliveryControls(u) {
  if (!canCollect()) return '';
  const left = remaining(u);
  const count =
    u.quantity > 1
      ? `<input type="number" min="1" max="${u.quantity}" step="1" size="4" value="${left || u.picked_up_quantity}" data-qty-for="${u.id}" aria-label="Unidades de ${escapeHtml(u.article_name ?? 'este pedido')}">`
      : '';
  const give =
    left > 0
      ? `<button type="button" class="btn btn-primary btn-small" data-deliver-unit="${u.id}">Entregar</button>`
      : '';
  const undo =
    u.picked_up_quantity > 0
      ? `<button type="button" class="btn btn-secondary btn-small" data-undo-unit="${u.id}" title="Vuelven a estar sin entregar; los números siguen asignados">Deshacer entrega</button>`
      : '';
  const back =
    returnable(u) > 0
      ? `<button type="button" class="btn btn-danger btn-small" data-return-unit="${u.id}" title="Salen del pedido y sus números quedan libres">Devolver</button>`
      : '';
  return `<div class="table-actions">${count}${give}${undo}${back}</div>`;
}

// Units typed into a numbers field and not saved yet, so that drawing the list
// again (every tick of a checkbox does) doesn't lose them.
const draftIdentifiers = new Map();

// Whether the unit's article has a number on each unit, and so a field to write
// them down while the distribution is being delivered.
function takesIdentifiers(u) {
  return (
    canCollect() && distribution?.articles?.some((a) => a.id === u.article_id && a.has_identifiers)
  );
}

// An article whose numbers are described has every number that exists known, so
// the next free ones can be given automatically and ranges can be typed.
const articleOfUnit = (u) => distribution?.articles?.find((a) => a.id === u.article_id);
const isNumbered = (u) => (articleOfUnit(u)?.identifier_fields ?? []).length > 0;

function identifiersEditor(u) {
  if (!takesIdentifiers(u)) return '';
  const article = articleOfUnit(u);
  const value = draftIdentifiers.get(u.id) ?? formatIdentifiers(article, u.identifiers, false);
  const hint = isNumbered(u) ? ' (rangos como 0001-0010 valen)' : '';
  const missing = u.quantity - (u.identifiers ?? []).length;
  const assign =
    isNumbered(u) && missing > 0
      ? `<button type="button" class="btn btn-primary btn-small" data-assign-ids="${u.id}">Asignar ${missing === 1 ? 'el siguiente libre' : `los ${missing} siguientes libres`}</button>`
      : '';
  return `<div class="table-actions">
    <input type="text" size="34" data-ids-for="${u.id}" value="${escapeHtml(value)}" placeholder="${u.quantity} ${u.quantity === 1 ? 'número' : 'números'}${hint}" aria-label="Números de ${escapeHtml(u.article_name)}">
    <button type="button" class="btn btn-secondary btn-small" data-save-ids="${u.id}">Guardar números</button>
    ${assign}
  </div>`;
}

function unitRow(u) {
  const label = [unitLabel(u, hasOptions(), distribution?.articles), u.is_extra ? 'extra' : null]
    .filter(Boolean)
    .join(' · ');
  const checkbox =
    canCollect() && remaining(u) > 0
      ? `<input type="checkbox" data-unit="${u.id}"${selected.has(u.id) ? ' checked' : ''}> `
      : '';
  const last = u.picked_up_at ? ` — ${escapeHtml(pickedUpText(u))}` : '';
  return `<li><label>${checkbox}${escapeHtml(label)}</label> (${escapeHtml(standing(u))})${last}${deliveryControls(u)}${identifiersEditor(u)}</li>`;
}

function paymentText(group) {
  const toPay = group.filter((u) => u.price > 0);
  if (toPay.length === 0) return 'Gratis';
  const owed = toPay.reduce((sum, u) => sum + pendingCost(u), 0);
  return owed > 0 ? `Pendiente: ${formatCurrency(owed)}` : 'Pagado';
}

function render() {
  const groups = new Map();
  for (const u of units) {
    if (!groups.has(u.beneficiary_login))
      groups.set(u.beneficiary_login, { name: u.beneficiary_name, units: [] });
    groups.get(u.beneficiary_login).units.push(u);
  }
  const shown = [...groups.values()].filter(
    (g) => showPicked.checked || g.units.some((u) => remaining(u) > 0),
  );
  list.innerHTML = shown.length
    ? shown
        .map(
          (g) => `
        <tr>
          <td>${escapeHtml(g.name)}</td>
          <td><ul class="plain-list">${g.units
            .filter((u) => showPicked.checked || remaining(u) > 0)
            .map(unitRow)
            .join('')}</ul></td>
          <td>${paymentText(g.units)}</td>
        </tr>`,
        )
        .join('')
    : `<tr><td colspan="3">${units.length ? 'No queda nadie por recoger.' : 'Todavía no ha pedido nadie.'}</td></tr>`;

  const done = units.reduce((sum, u) => sum + u.picked_up_quantity, 0);
  const total = units.reduce((sum, u) => sum + u.quantity, 0);
  const waiting = new Set(units.filter((u) => remaining(u) > 0).map((u) => u.beneficiary_login))
    .size;
  document.getElementById('pickup-progress').textContent =
    canCollect() || done > 0
      ? `Entregadas ${done} de ${total} unidades. ${waiting === 1 ? 'Falta 1 socio' : `Faltan ${waiting} socios`} por recoger.`
      : '';
  form.hidden = !canCollect();
  // Only offered where some article has its numbers described.
  document.getElementById('pf-assign-field').hidden = !(distribution?.articles ?? []).some(
    (a) => a.identifier_fields.length > 0,
  );
  document.getElementById('pf-selected').textContent = selected.size
    ? selected.size === 1
      ? '1 pedido marcado.'
      : `${selected.size} pedidos marcados.`
    : '';
  submit.disabled = selected.size === 0;
}

// The person the units are for is the natural default for who collects them,
// when all the marked ones are for the same person.
let autoFilled = '';
function suggestRecipient() {
  const logins = new Set(units.filter((u) => selected.has(u.id)).map((u) => u.beneficiary_login));
  const entry = logins.size === 1 ? people.entries.find((e) => e.id === [...logins][0]) : null;
  const suggestion = entry ? entry.label : '';
  if (personInput.value === '' || personInput.value === autoFilled) personInput.value = suggestion;
  autoFilled = suggestion;
}

list.addEventListener('change', (event) => {
  const box = event.target.closest('input[data-unit]');
  if (!box) return;
  const unitId = Number(box.dataset.unit);
  if (box.checked) selected.add(unitId);
  else selected.delete(unitId);
  suggestRecipient();
  render();
});

list.addEventListener('input', (event) => {
  const field = event.target.closest('input[data-ids-for]');
  if (field) draftIdentifiers.set(Number(field.dataset.idsFor), field.value);
});

list.addEventListener('click', async (event) => {
  const assign = event.target.closest('button[data-assign-ids]');
  if (assign) {
    const unitId = Number(assign.dataset.assignIds);
    assign.disabled = true;
    try {
      const updated = await apiFetch(`${base}/units/${unitId}/identifiers/assign`, {
        method: 'POST',
      });
      draftIdentifiers.delete(unitId);
      showFeedback('Números asignados.');
      units = units.map((u) => (u.id === unitId ? updated : u));
      render();
    } catch (err) {
      assign.disabled = false;
      fail(err);
    }
    return;
  }
  const save = event.target.closest('button[data-save-ids]');
  if (save) {
    const unitId = Number(save.dataset.saveIds);
    const field = list.querySelector(`input[data-ids-for="${unitId}"]`);
    const identifiers = field.value
      .split(/[,\n]/)
      .map((v) => v.trim())
      .filter(Boolean);
    save.disabled = true;
    try {
      const updated = await apiFetch(`${base}/units/${unitId}/identifiers`, {
        method: 'PUT',
        body: JSON.stringify({ identifiers }),
      });
      draftIdentifiers.delete(unitId);
      showFeedback('Números guardados.');
      units = units.map((u) => (u.id === unitId ? updated : u));
      render();
    } catch (err) {
      save.disabled = false;
      fail(err);
    }
    return;
  }
  const deliver = event.target.closest('button[data-deliver-unit]');
  const undo = event.target.closest('button[data-undo-unit]');
  const giveBack = event.target.closest('button[data-return-unit]');
  const button = deliver ?? undo ?? giveBack;
  if (!button) return;
  const unitId = Number(
    deliver?.dataset.deliverUnit ?? undo?.dataset.undoUnit ?? giveBack?.dataset.returnUnit,
  );
  const u = units.find((x) => x.id === unitId);
  // How many: what is typed next to the row (a row of one has no field).
  const field = list.querySelector(`input[data-qty-for="${unitId}"]`);
  const quantity = field ? Number(field.value) : u.quantity;
  if (!Number.isInteger(quantity) || quantity < 1) {
    showError('Pon cuántas unidades (un número entero de 1 en adelante).');
    return;
  }
  const word = quantity === 1 ? '1 unidad' : `${quantity} unidades`;
  try {
    button.disabled = true;
    if (deliver) {
      const login = resolvePickerId(people.entries, personInput.value);
      if (!login) {
        showError('Elige antes, abajo, a la persona que recoge («Entregado a»).');
        button.disabled = false;
        return;
      }
      if (
        u.price > 0 &&
        u.paid_quantity < u.picked_up_quantity + quantity &&
        !confirm('Hay unidades que todavía no están pagadas. ¿Entregarlas igualmente?')
      ) {
        button.disabled = false;
        return;
      }
      await apiFetch(`${base}/pickups`, {
        method: 'POST',
        body: JSON.stringify({
          deliveries: [{ unit_id: unitId, quantity }],
          picked_up_by_login: login,
          assign_numbers: assignBox.checked,
        }),
      });
      showFeedback(`Entregadas ${word} a ${nameOf(login)}.`);
    } else if (undo) {
      if (!confirm(`¿Deshacer la entrega de ${word}? Siguen siendo del pedido, sin entregar.`)) {
        button.disabled = false;
        return;
      }
      await apiFetch(`${base}/units/${unitId}/pickup?quantity=${quantity}`, { method: 'DELETE' });
      showFeedback('Entrega deshecha.');
    } else {
      if (
        !confirm(
          `¿Devolver ${word}? Salen del pedido, y si tienen número, queda libre. No se puede deshacer.`,
        )
      ) {
        button.disabled = false;
        return;
      }
      await apiFetch(`${base}/units/${unitId}/return`, {
        method: 'POST',
        body: JSON.stringify({ quantity }),
      });
      showFeedback('Devolución apuntada.');
    }
    await load();
  } catch (err) {
    button.disabled = false;
    fail(err);
  }
});

showPicked.addEventListener('change', render);

form.addEventListener('submit', async (event) => {
  event.preventDefault();
  const login = resolvePickerId(people.entries, personInput.value);
  if (!login) {
    showError('Elige a la persona que recoge de la lista.');
    return;
  }
  const chosen = units.filter((u) => selected.has(u.id));
  if (
    chosen.some((u) => u.price > 0 && pendingQuantity(u) > 0) &&
    !confirm('Hay pedidos que todavía no están pagados. ¿Apuntar la recogida igualmente?')
  ) {
    return;
  }
  if (
    chosen.some((u) => isUnchosen(u, hasOptions())) &&
    !confirm(
      'Hay pedidos de los que nadie ha elegido todavía lo que son. ¿Apuntar la recogida igualmente?',
    )
  ) {
    return;
  }
  submit.disabled = true;
  try {
    await apiFetch(`${base}/pickups`, {
      method: 'POST',
      body: JSON.stringify({
        unit_ids: [...selected],
        picked_up_by_login: login,
        assign_numbers: assignBox.checked,
      }),
    });
    showFeedback(
      `Recogida apuntada: ${chosen.length === 1 ? '1 pedido entregado' : `${chosen.length} pedidos entregados`} a ${nameOf(login)}.`,
    );
    selected.clear();
    personInput.value = '';
    autoFilled = '';
    await load();
  } catch (err) {
    fail(err);
    submit.disabled = false;
  }
});

// `ready` is what drawing needs besides the units (the people and the
// distribution): the requests all go out at once and only the drawing waits.
async function load(ready = Promise.resolve()) {
  try {
    units = await apiFetch(`${base}/units`);
    await ready;
    // Whatever was marked may have been collected since.
    for (const unitId of [...selected]) {
      if (!units.some((u) => u.id === unitId && remaining(u) > 0)) selected.delete(unitId);
    }
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
  document.getElementById('pickup-title').textContent = `Recogida: ${distribution.name}`;
  document.getElementById('pickup-meta').innerHTML = canCollect()
    ? statusBadge(distribution.status)
    : `${statusBadge(distribution.status)} · No se puede apuntar una recogida con el reparto en borrador.`;
} catch (err) {
  fail(err);
}
await firstLoad;
