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
  nothingToAsk,
  statusSteps,
  apiErrorText,
  audienceLabels,
  bodyFrom,
  choicesText,
  distributionStatusLabels,
  isUnchosen,
  fillDistributionForm,
  formatDateTime,
  loadPeople,
  readDistributionForm,
  resolvePickerId,
  statusBadge,
} from './distributionCommon.js';

await initPage();
const me = currentUser();
const isStaff = hasAnyRole(me, ['admin', 'board']);

const id = Number(new URLSearchParams(window.location.search).get('id'));
const base = `/distributions/${id}`;
let distribution = null;
let units = [];
let people = { entries: [], names: {} };

const fail = (err) => showError(apiErrorText(err, distribution));
const nameOf = (login) => people.names[login] ?? login;

// What this distribution is, in a line.
function describe(d) {
  const parts = [statusBadge(d.status), escapeHtml(audienceLabels[d.audience])];
  if (d.cutoff_date) {
    parts.push(d.audience === 'signup'
      ? `solo socios a ${formatDate(d.cutoff_date)}`
      : `socios a ${formatDate(d.cutoff_date)}`);
  }
  parts.push(d.price > 0 ? `${formatCurrency(d.price)} cada uno` : 'gratis');
  if (d.extra_price != null) {
    const cap = d.max_extra != null ? `, hasta ${d.max_extra} por socio` : '';
    parts.push(`extras a ${formatCurrency(d.extra_price)}${cap}`);
  }
  if (d.closes_at) parts.push(`se elige hasta el ${formatDateTime(d.closes_at)}`);
  return parts.join(' · ');
}

// Whether the signed-in person can ask for units now — the same rule the API
// applies: only while it is open, and for a member also before the deadline
// (admin/board can go on after it).
function canOrderNow() {
  const beforeDeadline = !distribution.closes_at || new Date() < new Date(distribution.closes_at);
  return distribution.status === 'open' && (isStaff || beforeDeadline);
}

function renderHeader() {
  document.getElementById('distribution-title').textContent = distribution.name;
  document.title = `${distribution.name} — Club de Espeleología Katiuskas`;
  document.getElementById('distribution-meta').innerHTML = describe(distribution);
  document.getElementById('distribution-description').textContent = distribution.description ?? '';
  // Buttons restricted by data-roles are removed from the page for everybody
  // else, hence the optional chaining on the ones that might not be there.
  const orderLink = document.getElementById('link-order');
  const pickupLink = document.getElementById('link-pickup');
  if (orderLink) orderLink.href = `/reparto-pedido?id=${id}`;
  if (pickupLink) pickupLink.href = `/reparto-recogida?id=${id}`;
  // The actions bar only holds things for admin/board/material.
  document.getElementById('distribution-actions').hidden = !hasAnyRole(me, ['admin', 'board', 'material']);

  const steps = statusSteps(distribution);
  const index = steps.indexOf(distribution.status);
  const back = document.getElementById('step-back');
  const forward = document.getElementById('step-forward');
  if (!back || !forward) return;
  back.hidden = index <= 0;
  forward.hidden = index >= steps.length - 1;
  if (index > 0) back.textContent = `Volver a «${distributionStatusLabels[steps[index - 1]]}»`;
  if (index < steps.length - 1) forward.textContent = `Pasar a «${distributionStatusLabels[steps[index + 1]]}»`;
  // Say why "Abierto" and "Cerrado" are skipped, while it is still a draft.
  const skipHint = document.getElementById('skip-hint');
  if (skipHint) skipHint.hidden = !(nothingToAsk(distribution) && distribution.status === 'draft');
}

async function setStatus(status) {
  try {
    distribution = await apiFetch(base, { method: 'PATCH', body: JSON.stringify(bodyFrom(distribution, { status })) });
    renderAll();
    // Leaving a draft is when the orders (everybody's own, if it is for all
    // members) appear; going back to one empties them.
    if (distribution.status !== 'draft') await loadUnits();
  } catch (err) {
    fail(err);
  }
}

// Staff-only controls: for anybody else they aren't on the page at all.
function onClick(elementId, handler) {
  document.getElementById(elementId)?.addEventListener('click', handler);
}

onClick('step-back', () => {
  const steps = statusSteps(distribution);
  const previous = steps[steps.indexOf(distribution.status) - 1];
  // Back to a draft starts it over: the API deletes every order, with what was
  // chosen on it, so say so when there is any.
  if (previous === 'draft' && distribution.unit_count > 0) {
    const count = distribution.unit_count === 1 ? '1 pedido' : `${distribution.unit_count} pedidos`;
    const again = distribution.audience === 'all_members'
      ? ' Al abrirlo otra vez se volverán a crear los de los socios, vacíos.'
      : '';
    if (!confirm(`Al volver a borrador se borrarán todos los pedidos (${count}), con lo que hayan elegido los socios, y no se podrán recuperar.${again} ¿Volver a borrador?`)) return;
  }
  setStatus(previous);
});
onClick('step-forward', () => {
  const steps = statusSteps(distribution);
  const next = steps[steps.indexOf(distribution.status) + 1];
  if (next === 'closed' && !confirm('Al cerrar el reparto ya nadie podrá pedir ni cambiar nada (para añadir algo habría que volver a abrirlo). ¿Cerrarlo?')) return;
  setStatus(next);
});

// ----- edit and delete -----
const editDialog = document.getElementById('edit-distribution-dialog');
const editForm = document.getElementById('edit-distribution-form');
initModal(editDialog);
const editAudience = document.getElementById('ed-audience');
const editCutoff = document.getElementById('ed-cutoff');
function syncEditCutoff() {
  const required = editAudience.value === 'all_members';
  editCutoff.required = required;
  document.getElementById('ed-cutoff-hint').textContent = required
    ? '(obligatorio)'
    : '(opcional: si la pones, solo podrán apuntarse los socios de ese día)';
}
editAudience.addEventListener('change', syncEditCutoff);

onClick('open-edit', () => {
  fillDistributionForm(editForm, distribution);
  syncEditCutoff();
  openModal(editDialog);
});

editForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  // Turning "todos los socios" into "quien se apunte" takes the orders nobody
  // touched with it (the API deletes them).
  if (distribution.audience === 'all_members' && editAudience.value === 'signup' && distribution.unit_count > 0
    && !confirm('Al pasarlo a «Quien se apunte» se borrarán los pedidos automáticos de los socios que no han elegido nada. Se quedan los que ya eligieron algo y los extras. ¿Seguir?')) {
    return;
  }
  const submit = editForm.querySelector('button[type="submit"]');
  submit.disabled = true;
  try {
    distribution = await apiFetch(base, { method: 'PATCH', body: JSON.stringify(readDistributionForm(editForm)) });
    editDialog.close();
    showFeedback('Reparto guardado.');
    renderAll();
  } catch (err) {
    fail(err);
  } finally {
    submit.disabled = false;
  }
});

onClick('delete-distribution', async () => {
  // Say what is lost in plain words: each "pedido" is what one member asked for
  // or has coming — in a distribution for all members that includes everybody's
  // own, which nobody asked for — and what they chose goes with it.
  const units = distribution.unit_count;
  let lost = '';
  if (units > 0) {
    const count = units === 1 ? '1 pedido' : `${units} pedidos`;
    const verb = units === 1 ? 'se perderá' : 'se perderán';
    lost = ` Tiene ${count}, con lo que hayan elegido los socios, y ${verb} con él.`;
  }
  if (!confirm(`¿Borrar el reparto «${distribution.name}»?${lost} No se puede deshacer.`)) return;
  try {
    await apiFetch(base, { method: 'DELETE' });
    window.location.href = '/repartos';
  } catch (err) {
    fail(err);
  }
});

// ----- asking for units -----
const orderForm = document.getElementById('order-form');
const personInput = document.getElementById('of-person');
const extraField = document.getElementById('of-extra-field');
const extraBox = document.getElementById('of-extra');

function optionSelects(prefix, selected = {}) {
  return distribution.options
    .map((option, i) => `
      <div class="field">
        <label for="${prefix}-${i}">${escapeHtml(option.name)}</label>
        <select id="${prefix}-${i}" data-option="${escapeHtml(option.name)}" required>
          <option value="">— Elige —</option>
          ${option.values.map((v) => `<option${selected[option.name] === v ? ' selected' : ''}>${escapeHtml(v)}</option>`).join('')}
        </select>
      </div>`)
    .join('');
}

function readChoices(container) {
  const choices = {};
  container.querySelectorAll('select[data-option]').forEach((s) => { choices[s.dataset.option] = s.value; });
  return choices;
}

// For a distribution for all members nobody has to sign up: each member's own
// unit is already there, and what is left is choosing its options and, if
// there are any, asking for extras.
// In a distribution for all members with nothing to choose, everybody's own
// order is already there and nothing can be done to it, so what is left to ask
// for is extras only: no "Es un extra" checkbox to tick, the button says what it
// does.
function extrasOnly() {
  return distribution.audience === 'all_members' && distribution.options.length === 0 && distribution.extra_price != null;
}

function allMembersHint() {
  const parts = ['Es para todos los socios: tu pedido ya está apuntado.'];
  if (distribution.options.length) parts.push('Elige lo que corresponda en «Tus pedidos» o, para otro socio, aquí abajo.');
  if (distribution.extra_price != null) parts.push(`Puedes pedir también extras a ${formatCurrency(distribution.extra_price)}.`);
  return parts.join(' ');
}

function renderOrderForm() {
  const status = document.getElementById('order-status');
  const eligible = isStaff || distribution.eligible;
  const open = canOrderNow();
  // A distribution for all members with nothing to choose and no extras has
  // nothing to ask for, ever: its own unit is there for everybody, so the
  // whole section is out — not even a "no longer admitting orders", since it
  // never did.
  const orderSection = document.getElementById('order-section');
  orderSection.hidden = nothingToAsk(distribution);
  if (orderSection.hidden) return;
  orderForm.hidden = !(open && eligible);
  if (!eligible) {
    status.textContent = distribution.cutoff_date
      ? `No te corresponde este reparto: solo es para quienes eran socios el ${formatDate(distribution.cutoff_date)}.`
      : 'No te corresponde este reparto.';
  } else if (!open) {
    status.textContent = distribution.status === 'open'
      ? 'Ya ha pasado la fecha límite para pedir.'
      : 'Ya no se admiten pedidos.';
  } else {
    status.textContent = distribution.audience === 'signup'
      ? 'Es para quien se apunte: pídelo para ti o para otro socio.'
      : allMembersHint();
  }
  if (orderForm.hidden) return;
  document.getElementById('of-options').innerHTML = optionSelects('of');
  extraField.hidden = distribution.extra_price == null || extrasOnly();
  orderForm.querySelector('button[type="submit"]').textContent = extrasOnly() ? 'Pedido extra' : 'Pedir';
  document.getElementById('of-extra-label').textContent =
    `Es un extra (${formatCurrency(distribution.extra_price ?? 0)}${distribution.max_extra != null ? `, máximo ${distribution.max_extra} por socio` : ''})`;
}

orderForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  let beneficiary = null;
  if (personInput.value.trim()) {
    beneficiary = resolvePickerId(people.entries, personInput.value);
    if (!beneficiary) {
      showError('Elige a una persona de la lista, o deja el campo vacío para pedirlo para ti.');
      return;
    }
  }
  const submit = orderForm.querySelector('button[type="submit"]');
  submit.disabled = true;
  try {
    await apiFetch(`${base}/units`, {
      method: 'POST',
      body: JSON.stringify({
        beneficiary_login: beneficiary,
        is_extra: extrasOnly() || extraBox.checked,
        choices: readChoices(document.getElementById('of-options')),
      }),
    });
    showFeedback('Pedido apuntado.');
    personInput.value = '';
    extraBox.checked = false;
    await loadUnits();
  } catch (err) {
    fail(err);
  } finally {
    submit.disabled = false;
  }
});

// ----- the units shown -----
const unitsList = document.getElementById('units-list');

// Nothing about payment is shown yet: a unit is collected or it isn't.
const unitState = (u) => (u.picked_up_at ? 'Recogido' : 'No recogido');

function mineOnly(all) {
  // Admin, board and material get every unit from the API; this page is
  // about what the signed-in person asked for.
  return all.filter((u) => u.beneficiary_login === me.login || u.registered_by_login === me.login);
}

function canChange(u) {
  if (u.picked_up_at) return false;
  if (u.paid_at && !isStaff) return false;
  return canOrderNow();
}

// The columns "Tus pedidos" needs: no "Elegido" when there is nothing to choose,
// and no "Tipo" (own or extra) when there can be no extras. The price is shown,
// but nothing about paying: that isn't something the intranet keeps.
function unitColumns() {
  const columns = [{ key: 'for', label: 'Para' }];
  if (distribution.options.length > 0) columns.push({ key: 'chosen', label: 'Elegido' });
  if (distribution.extra_price != null) columns.push({ key: 'kind', label: 'Tipo' });
  columns.push({ key: 'price', label: 'Precio' }, { key: 'entered', label: 'Apuntado' }, { key: 'state', label: 'Estado' }, { key: 'actions', label: '' });
  return columns;
}

function unitCell(key, u, hasOptions) {
  const forMe = u.beneficiary_login === me.login;
  const byMe = u.registered_by_login === me.login;
  const unchosen = isUnchosen(u, hasOptions);
  switch (key) {
    case 'for':
      return escapeHtml(forMe ? 'Ti' : u.beneficiary_name);
    case 'chosen':
      return unchosen ? '<strong>Sin elegir</strong>' : escapeHtml(choicesText(u.choices, hasOptions));
    case 'kind':
      return u.is_extra ? 'Extra' : 'Propia';
    case 'price':
      return u.price > 0 ? formatCurrency(u.price) : 'Gratis';
    case 'entered':
      // Who entered it matters when it wasn't the person it is for. Nobody did
      // for an own unit of a distribution for all members until somebody chooses.
      if (!u.registered_by_login) return 'Automático';
      if (forMe && byMe) return formatDate(u.registered_at);
      return `${formatDate(u.registered_at)} por ${escapeHtml(byMe ? 'ti' : nameOf(u.registered_by_login))}`;
    case 'state':
      return unitState(u);
    default: {
      // An own unit of a distribution for all members is for everybody and
      // can't be removed (the API refuses); everything else can.
      const removable = u.is_extra || distribution.audience === 'signup';
      if (!canChange(u)) return '';
      return `<div class="table-actions">
        ${hasOptions ? `<button type="button" class="btn ${unchosen ? 'btn-primary' : 'btn-secondary'} btn-small" data-change-unit="${u.id}">${unchosen ? 'Elegir' : 'Cambiar'}</button>` : ''}
        ${removable ? `<button type="button" class="btn btn-danger btn-small" data-delete-unit="${u.id}">Quitar</button>` : ''}
      </div>`;
    }
  }
}

function renderUnits() {
  const mine = mineOnly(units);
  const columns = unitColumns();
  const hasOptions = distribution.options.length > 0;
  document.getElementById('units-head').innerHTML = columns.map((c) => `<th>${c.label}</th>`).join('');
  unitsList.innerHTML = mine.length
    ? mine.map((u) => `<tr>${columns.map((c) => `<td>${unitCell(c.key, u, hasOptions)}</td>`).join('')}</tr>`).join('')
    : `<tr><td colspan="${columns.length}">Todavía no hay nada a tu nombre.</td></tr>`;
}

async function loadUnits() {
  try {
    units = await apiFetch(`${base}/units`);
    renderUnits();
  } catch (err) {
    unitsList.innerHTML = `<tr><td colspan="${distribution ? unitColumns().length : 4}">No se pudieron cargar los pedidos.</td></tr>`;
    fail(err);
  }
}

const unitDialog = document.getElementById('edit-unit-dialog');
const unitForm = document.getElementById('edit-unit-form');
initModal(unitDialog, unitForm);
let changing = null;

unitsList.addEventListener('click', async (event) => {
  const change = event.target.closest('button[data-change-unit]');
  const remove = event.target.closest('button[data-delete-unit]');
  if (change) {
    changing = units.find((u) => u.id === Number(change.dataset.changeUnit));
    document.getElementById('edit-unit-hint').textContent =
      `Para ${changing.beneficiary_login === me.login ? 'ti' : changing.beneficiary_name}.`;
    document.getElementById('eu-options').innerHTML = optionSelects('eu', changing.choices);
    openModal(unitDialog);
  } else if (remove) {
    if (!confirm('¿Quitar este pedido?')) return;
    remove.disabled = true;
    try {
      await apiFetch(`${base}/units/${remove.dataset.deleteUnit}`, { method: 'DELETE' });
      showFeedback('Unidad quitada.');
      await loadUnits();
    } catch (err) {
      remove.disabled = false;
      fail(err);
    }
  }
});

unitForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  if (!changing) return;
  try {
    await apiFetch(`${base}/units/${changing.id}`, {
      method: 'PATCH',
      body: JSON.stringify({ choices: readChoices(document.getElementById('eu-options')) }),
    });
    unitDialog.close();
    showFeedback('Cambio guardado.');
    await loadUnits();
  } catch (err) {
    fail(err);
  }
});

function renderAll() {
  // While it is a draft nobody can ask for anything, so neither the form nor the
  // list of what has been asked for has a reason to be there.
  document.getElementById('ask-sections').hidden = distribution.status === 'draft';
  renderHeader();
  renderOrderForm();
  renderUnits();
}

people = await loadPeople(id, 'order');
document.getElementById('of-person-options').innerHTML =
  people.entries.map((e) => `<option value="${escapeHtml(e.label)}"></option>`).join('');
try {
  distribution = await apiFetch(base);
} catch (err) {
  document.getElementById('distribution-title').textContent = 'Reparto no encontrado';
  fail(err);
  throw err;
}
renderAll();
if (distribution.status !== 'draft') await loadUnits();
