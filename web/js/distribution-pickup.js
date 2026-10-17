import { apiFetch, escapeHtml, formatCurrency, showError, showFeedback } from './api.js';
import { initPage } from './layout.js';
import { apiErrorText, choicesText, isUnchosen, loadPeople, resolvePickerId, statusBadge, formatDateTime } from './distributionCommon.js';

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

const hasOptions = () => (distribution?.options.length ?? 0) > 0;

function canCollect() {
  return distribution?.status === 'delivering';
}

function pickedUpText(u) {
  const when = formatDateTime(u.picked_up_at);
  const to = u.picked_up_by_login === u.beneficiary_login ? '' : `, entregado a ${nameOf(u.picked_up_by_login)}`;
  return `Recogido el ${when}${to} (apuntado por ${nameOf(u.pickup_recorded_by_login)})`;
}

function unitRow(u) {
  const label = [choicesText(u.choices, hasOptions()), u.is_extra ? 'extra' : null].filter(Boolean).join(' · ');
  if (u.picked_up_at) {
    return `<li>${escapeHtml(label)} — ${escapeHtml(pickedUpText(u))}
      <button type="button" class="link-button" data-undo-unit="${u.id}">Deshacer</button></li>`;
  }
  const checkbox = canCollect()
    ? `<input type="checkbox" data-unit="${u.id}"${selected.has(u.id) ? ' checked' : ''}> `
    : '';
  return `<li><label>${checkbox}${escapeHtml(label)}</label></li>`;
}

function paymentText(group) {
  const toPay = group.filter((u) => u.price > 0);
  if (toPay.length === 0) return 'Gratis';
  const owed = toPay.filter((u) => !u.paid_at).reduce((sum, u) => sum + u.price, 0);
  return owed > 0 ? `Pendiente: ${formatCurrency(owed)}` : 'Pagado';
}

function render() {
  const groups = new Map();
  for (const u of units) {
    if (!groups.has(u.beneficiary_login)) groups.set(u.beneficiary_login, { name: u.beneficiary_name, units: [] });
    groups.get(u.beneficiary_login).units.push(u);
  }
  const shown = [...groups.values()].filter((g) => showPicked.checked || g.units.some((u) => !u.picked_up_at));
  list.innerHTML = shown.length
    ? shown.map((g) => `
        <tr>
          <td>${escapeHtml(g.name)}</td>
          <td><ul class="plain-list">${g.units.filter((u) => showPicked.checked || !u.picked_up_at).map(unitRow).join('')}</ul></td>
          <td>${paymentText(g.units)}</td>
        </tr>`).join('')
    : `<tr><td colspan="3">${units.length ? 'No queda nadie por recoger.' : 'Todavía no ha pedido nadie.'}</td></tr>`;

  const done = units.filter((u) => u.picked_up_at).length;
  const waiting = new Set(units.filter((u) => !u.picked_up_at).map((u) => u.beneficiary_login)).size;
  document.getElementById('pickup-progress').textContent = canCollect() || done > 0
    ? `Recogidas ${done} de ${units.length} unidades. ${waiting === 1 ? 'Falta 1 socio' : `Faltan ${waiting} socios`} por recoger.`
    : '';
  form.hidden = !canCollect();
  document.getElementById('pf-selected').textContent = selected.size ? `${selected.size} unidades marcadas.` : '';
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

list.addEventListener('click', async (event) => {
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
  if (chosen.some((u) => u.price > 0 && !u.paid_at) && !confirm('Hay unidades que todavía no están pagadas. ¿Apuntar la recogida igualmente?')) {
    return;
  }
  if (chosen.some((u) => isUnchosen(u, hasOptions())) && !confirm('Hay unidades de las que nadie ha elegido todavía lo que son. ¿Apuntar la recogida igualmente?')) {
    return;
  }
  submit.disabled = true;
  try {
    await apiFetch(`${base}/pickups`, { method: 'POST', body: JSON.stringify({ unit_ids: [...selected], picked_up_by_login: login }) });
    showFeedback(`Recogida apuntada: ${chosen.length} unidades entregadas a ${nameOf(login)}.`);
    selected.clear();
    personInput.value = '';
    autoFilled = '';
    await load();
  } catch (err) {
    fail(err);
    submit.disabled = false;
  }
});

async function load() {
  try {
    units = await apiFetch(`${base}/units`);
    // Whatever was marked may have been collected since.
    for (const unitId of [...selected]) {
      if (!units.some((u) => u.id === unitId && !u.picked_up_at)) selected.delete(unitId);
    }
    render();
  } catch (err) {
    fail(err);
  }
}

people = await loadPeople();
document.getElementById('pf-person-options').innerHTML =
  people.entries.map((e) => `<option value="${escapeHtml(e.label)}"></option>`).join('');
try {
  distribution = await apiFetch(base);
  document.getElementById('pickup-title').textContent = `Recogida: ${distribution.name}`;
  document.getElementById('pickup-meta').innerHTML = canCollect()
    ? statusBadge(distribution.status)
    : `${statusBadge(distribution.status)} · Solo se puede apuntar una recogida con el reparto «En entrega».`;
} catch (err) {
  fail(err);
}
await load();
