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
  setStatus(steps[steps.indexOf(distribution.status) - 1]);
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
function allMembersHint() {
  const parts = ['Es para todos los socios: tu pedido ya está apuntado.'];
  if (distribution.options.length) parts.push('Elige lo que corresponda en «Tus pedidos» o, para otro socio, aquí abajo.');
  if (distribution.extra_price != null) parts.push('Puedes pedir también extras.');
  return parts.join(' ');
}

function renderOrderForm() {
  const status = document.getElementById('order-status');
  const eligible = isStaff || distribution.eligible;
  const open = canOrderNow();
  // In a distribution for all members everybody's own unit is already there,
  // so the form only has something to do when there are options to choose for
  // somebody else, or extras to ask for.
  const nothingToAsk = distribution.audience === 'all_members' && distribution.options.length === 0 && distribution.extra_price == null;
  orderForm.hidden = !(open && eligible) || nothingToAsk;
  if (distribution.status === 'draft') {
    status.textContent = 'Es un borrador: los socios todavía no lo ven.';
  } else if (!eligible) {
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
  extraField.hidden = distribution.extra_price == null;
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
        is_extra: extraBox.checked,
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

function unitState(u) {
  const paid = u.paid_at ? 'Pagado' : (u.price > 0 ? 'Sin pagar' : 'Gratis');
  const picked = u.picked_up_at ? ' · Recogido' : '';
  return paid + picked;
}

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

function renderUnits() {
  const mine = mineOnly(units);
  unitsList.innerHTML = mine.length
    ? mine.map((u) => {
      const forMe = u.beneficiary_login === me.login;
      const byMe = u.registered_by_login === me.login;
      const hasOptions = distribution.options.length > 0;
      const unchosen = isUnchosen(u, hasOptions);
      // Who entered it matters when it wasn't the person it is for. Nobody did
      // for an own unit of a distribution for all members until somebody chooses.
      let entered;
      if (!u.registered_by_login) entered = 'Automático';
      else if (forMe && byMe) entered = formatDate(u.registered_at);
      else entered = `${formatDate(u.registered_at)} por ${escapeHtml(byMe ? 'ti' : nameOf(u.registered_by_login))}`;
      // An own unit of a distribution for all members is for everybody and
      // can't be removed (the API refuses); everything else can.
      const removable = u.is_extra || distribution.audience === 'signup';
      const actions = canChange(u)
        ? `<div class="table-actions">
             ${hasOptions ? `<button type="button" class="btn ${unchosen ? 'btn-primary' : 'btn-secondary'} btn-small" data-change-unit="${u.id}">${unchosen ? 'Elegir' : 'Cambiar'}</button>` : ''}
             ${removable ? `<button type="button" class="btn btn-danger btn-small" data-delete-unit="${u.id}">Quitar</button>` : ''}
           </div>`
        : '';
      return `
        <tr>
          <td>${escapeHtml(forMe ? 'Ti' : u.beneficiary_name)}</td>
          <td>${unchosen ? '<strong>Sin elegir</strong>' : escapeHtml(choicesText(u.choices, hasOptions))}</td>
          <td>${u.is_extra ? 'Extra' : 'Propia'}</td>
          <td>${u.price > 0 ? formatCurrency(u.price) : 'Gratis'}</td>
          <td>${entered}</td>
          <td>${unitState(u)}</td>
          <td>${actions}</td>
        </tr>`;
    }).join('')
    : '<tr><td colspan="7">Todavía no hay nada a tu nombre.</td></tr>';
  const owed = mine.filter((u) => !u.paid_at).reduce((sum, u) => sum + u.price, 0);
  document.getElementById('units-total').textContent = owed > 0 ? `Pendiente de pagar: ${formatCurrency(owed)}.` : '';
}

async function loadUnits() {
  try {
    units = await apiFetch(`${base}/units`);
    renderUnits();
  } catch (err) {
    unitsList.innerHTML = '<tr><td colspan="7">No se pudieron cargar los pedidos.</td></tr>';
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
await loadUnits();
