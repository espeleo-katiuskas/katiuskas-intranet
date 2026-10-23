import { apiFetch, escapeHtml, formatCurrency, showError, showFeedback } from './api.js';
import { initPage } from './layout.js';
import {
  apiErrorText,
  formatIdentifiers,
  isUnchosen,
  unitCost,
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

function pickedUpText(u) {
  const when = formatDateTime(u.picked_up_at);
  const to =
    u.picked_up_by_login === u.beneficiary_login
      ? ''
      : `, entregado a ${nameOf(u.picked_up_by_login)}`;
  return `Recogido el ${when}${to} (apuntado por ${nameOf(u.pickup_recorded_by_login)})`;
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
  if (u.picked_up_at) {
    return `<li>${escapeHtml(label)} — ${escapeHtml(pickedUpText(u))}
      <button type="button" class="link-button" data-undo-unit="${u.id}">Deshacer</button>${identifiersEditor(u)}</li>`;
  }
  const checkbox = canCollect()
    ? `<input type="checkbox" data-unit="${u.id}"${selected.has(u.id) ? ' checked' : ''}> `
    : '';
  return `<li><label>${checkbox}${escapeHtml(label)}</label>${identifiersEditor(u)}</li>`;
}

function paymentText(group) {
  const toPay = group.filter((u) => u.price > 0);
  if (toPay.length === 0) return 'Gratis';
  const owed = toPay.filter((u) => !u.paid_at).reduce((sum, u) => sum + unitCost(u), 0);
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
    (g) => showPicked.checked || g.units.some((u) => !u.picked_up_at),
  );
  list.innerHTML = shown.length
    ? shown
        .map(
          (g) => `
        <tr>
          <td>${escapeHtml(g.name)}</td>
          <td><ul class="plain-list">${g.units
            .filter((u) => showPicked.checked || !u.picked_up_at)
            .map(unitRow)
            .join('')}</ul></td>
          <td>${paymentText(g.units)}</td>
        </tr>`,
        )
        .join('')
    : `<tr><td colspan="3">${units.length ? 'No queda nadie por recoger.' : 'Todavía no ha pedido nadie.'}</td></tr>`;

  const done = units.filter((u) => u.picked_up_at).length;
  const waiting = new Set(units.filter((u) => !u.picked_up_at).map((u) => u.beneficiary_login))
    .size;
  document.getElementById('pickup-progress').textContent =
    canCollect() || done > 0
      ? `Recogidas ${done} de ${units.length} pedidos. ${waiting === 1 ? 'Falta 1 socio' : `Faltan ${waiting} socios`} por recoger.`
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
  const undo = event.target.closest('button[data-undo-unit]');
  if (!undo) return;
  if (!confirm('¿Deshacer esta recogida?')) return;
  undo.disabled = true;
  try {
    await apiFetch(`${base}/units/${undo.dataset.undoUnit}/pickup`, { method: 'DELETE' });
    showFeedback('Recogida deshecha.');
    await load();
  } catch (err) {
    undo.disabled = false;
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
    chosen.some((u) => u.price > 0 && !u.paid_at) &&
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
      if (!units.some((u) => u.id === unitId && !u.picked_up_at)) selected.delete(unitId);
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
