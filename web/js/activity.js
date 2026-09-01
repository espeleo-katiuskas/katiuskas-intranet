import {
  initSession,
  logout,
  applyRoleVisibility,
  currentUser,
  hasAnyRole,
  apiFetch,
  escapeHtml,
  formatDate,
  pickerLabel,
  renderPickerOptions,
  resolvePickerId,
} from './api.js';

await initSession();
applyRoleVisibility();
document.getElementById('logout-button').addEventListener('click', logout);

const viewer = currentUser();
const isAdmin = hasAnyRole(viewer, ['admin', 'board']);
// Set once loadActivity() resolves and the activity's leader_login is
// known — admin/board can manage any activity, and the organizer who
// created it can manage their own. Can't be a data-roles attribute
// since it depends on data loaded asynchronously for this specific
// activity.
let canManage = isAdmin;
// Who may add a participant depends on visibility too, not just on
// canManage: a public activity works as it always has (anyone can join
// themselves; only admin/board can add someone else — the organizer
// alone gets no extra say), while a private one restricts adding
// *anyone*, self included, to the organizer/board/admin. See
// Handler.join on the backend for the same split.
let canJoinSelf = true;
let canAddOthers = isAdmin;
// Registering/returning a loan is gated the same way as on the
// material page (admin/board/material) — independent of canManage,
// since a plain member leading their own activity still can't touch
// inventory movements.
const canManageMovements = hasAnyRole(viewer, ['admin', 'board', 'material']);

const activityId = Number(new URLSearchParams(window.location.search).get('id'));
if (!activityId) {
  window.location.href = '/activities';
}

const feedbackBox = document.getElementById('feedback');
const errorBox = document.getElementById('error');

function showFeedback(message) {
  errorBox.hidden = true;
  feedbackBox.textContent = message;
  feedbackBox.hidden = false;
}

function showError(message) {
  feedbackBox.hidden = true;
  errorBox.textContent = message;
  errorBox.hidden = false;
}

const typeLabels = { caving: 'Espeleología', canyoning: 'Cañones', training: 'Formación', social: 'Social', other: 'Otro' };
const statusLabels = { planned: 'Planificada', completed: 'Realizada', cancelled: 'Cancelada' };
const visibilityLabels = { public: 'Pública', private: 'Privada' };

const editForm = document.getElementById('edit-activity-form');

// Toggles every management-only control (edit panel, "+ Participante"
// row) according to canManage — mirrors what data-roles/
// applyRoleVisibility does for static, role-only elements, but for
// something only knowable after this specific activity loads.
// Cancelling is just editing and setting status to "cancelada" (see
// the edit form's Estado field) — no separate action for it.
function applyManagementVisibility() {
  document.getElementById('edit-activity-panel').hidden = !canManage;
  document.getElementById('add-participant-row').hidden = !canAddOthers;
}

function dateRangeLabel(activity) {
  return activity.start_date === activity.end_date
    ? formatDate(activity.start_date)
    : `${formatDate(activity.start_date)} – ${formatDate(activity.end_date)}`;
}

async function loadActivity() {
  try {
    const activity = await apiFetch(`/activities/${activityId}`);
    document.getElementById('activity-title').textContent = activity.title;
    document.getElementById('activity-meta').textContent =
      `${dateRangeLabel(activity)} · ${typeLabels[activity.type] ?? activity.type} · ` +
      `${statusLabels[activity.status] ?? activity.status} · ${visibilityLabels[activity.visibility] ?? activity.visibility}`;
    document.getElementById('activity-description').textContent = activity.description ?? '';

    canManage = isAdmin || viewer?.login === activity.leader_login;
    const isPrivate = activity.visibility === 'private';
    canJoinSelf = isPrivate ? canManage : true;
    canAddOthers = isPrivate ? canManage : isAdmin;
    applyManagementVisibility();

    if (editForm) {
      editForm.title.value = activity.title;
      editForm.type.value = activity.type;
      editForm.start_date.value = activity.start_date.slice(0, 10);
      editForm.end_date.value = activity.end_date.slice(0, 10);
      editForm.status.value = activity.status;
      editForm.visibility.value = activity.visibility;
      editForm.description.value = activity.description ?? '';
      const leaderInput = document.getElementById('ea-leader');
      if (leaderInput) {
        leaderInput.value = personEntries.find((e) => e.id === activity.leader_login)?.label ?? activity.leader_login;
      }
    }
  } catch (err) {
    document.getElementById('activity-title').textContent = 'No se ha podido cargar esta actividad.';
    showError(err.message);
  }
}

editForm?.addEventListener('submit', async (event) => {
  event.preventDefault();
  const formData = new FormData(editForm);
  const body = {
    title: formData.get('title'),
    type: formData.get('type'),
    start_date: formData.get('start_date'),
    end_date: formData.get('end_date'),
    status: formData.get('status'),
    visibility: formData.get('visibility'),
    description: formData.get('description')?.toString().trim() || null,
  };

  // Only present at all for admin/board (see data-roles on the field) —
  // reassigning the organizer is narrower than canManage, which the
  // current leader also satisfies.
  const leaderInput = document.getElementById('ea-leader');
  if (leaderInput && leaderInput.value.trim()) {
    const leaderLogin = resolvePickerId(personEntries, leaderInput.value);
    if (!leaderLogin) {
      showError('Elige un organizador de la lista.');
      return;
    }
    body.leader_login = leaderLogin;
  }

  const submitButton = editForm.querySelector('button[type="submit"]');
  submitButton.disabled = true;
  try {
    await apiFetch(`/activities/${activityId}`, { method: 'PATCH', body: JSON.stringify(body) });
    showFeedback('Actividad actualizada.');
    await loadActivity();
  } catch (err) {
    showError(err.message);
  } finally {
    submitButton.disabled = false;
  }
});

// Loaded once so participant names can link to their own /member?login=…
// page instead of showing a bare login, and (for admin/board) so the
// "add participant" picker can search by name.
let directoryByLogin = {};
let personEntries = [];

async function loadDirectory() {
  try {
    const directory = await apiFetch('/users/directory');
    directoryByLogin = Object.fromEntries(directory.map((person) => [person.login, person]));
    personEntries = directory
      .slice()
      .sort((a, b) => `${a.last_name} ${a.first_name}`.localeCompare(`${b.last_name} ${b.first_name}`, 'es'))
      .map((person) => ({ id: person.login, label: `${person.first_name} ${person.last_name} #${person.login}` }));
    const loanUserOptions = document.getElementById('loan-user-options');
    if (loanUserOptions) loanUserOptions.innerHTML = renderPickerOptions(personEntries);
    const leaderOptions = document.getElementById('ea-leader-options');
    if (leaderOptions) leaderOptions.innerHTML = renderPickerOptions(personEntries);
  } catch {
    directoryByLogin = {};
    personEntries = [];
  }
}

function personLabel(login) {
  const person = directoryByLogin[login];
  return person ? `${person.first_name} ${person.last_name}` : login;
}

function renderParticipantRow(p) {
  const name = personLabel(p.user_login);
  return `
    <div class="detail-row">
      <span><a href="/member?login=${p.user_login}">${escapeHtml(name)}</a>${p.role === 'leader' ? ' · organizador' : ''}</span>
      ${canManage ? `<button type="button" class="btn btn-danger btn-small" data-remove-participant="${p.user_login}">Quitar</button>` : ''}
    </div>
  `;
}

async function loadParticipants() {
  const container = document.getElementById('participants-list');
  try {
    const participants = await apiFetch(`/activities/${activityId}/participants`);
    container.innerHTML = participants.length
      ? participants.map(renderParticipantRow).join('')
      : '<p>Todavía no hay nadie apuntado.</p>';

    const alreadyIn = participants.some((p) => p.user_login === viewer?.login);
    document.getElementById('join-activity').hidden = alreadyIn || !canJoinSelf;
    document.getElementById('leave-activity').hidden = !alreadyIn;

    const optionsEl = document.getElementById('participant-options');
    if (optionsEl) {
      const alreadyInSet = new Set(participants.map((p) => p.user_login));
      const candidates = personEntries.filter((entry) => !alreadyInSet.has(entry.id));
      optionsEl.innerHTML = renderPickerOptions(candidates);
    }
  } catch (err) {
    container.innerHTML = `<p>${escapeHtml(err.message)}</p>`;
  }
}

document.getElementById('join-activity').addEventListener('click', async () => {
  try {
    await apiFetch(`/activities/${activityId}/participants`, { method: 'POST' });
    showFeedback('Te has apuntado.');
    await loadParticipants();
  } catch (err) {
    showError(err.message);
  }
});

document.getElementById('leave-activity').addEventListener('click', async () => {
  try {
    await apiFetch(`/activities/${activityId}/participants/${viewer.login}`, { method: 'DELETE' });
    showFeedback('Te has dado de baja de la actividad.');
    await loadParticipants();
  } catch (err) {
    showError(err.message);
  }
});

document.getElementById('add-participant')?.addEventListener('click', async () => {
  const input = document.getElementById('participant-search');
  const login = resolvePickerId(personEntries, input.value);
  if (!login) {
    showError('Escribe un nombre de la lista de socios.');
    return;
  }
  try {
    await apiFetch(`/activities/${activityId}/participants`, {
      method: 'POST',
      body: JSON.stringify({ login }),
    });
    input.value = '';
    await loadParticipants();
  } catch (err) {
    showError(err.message);
  }
});

document.getElementById('participants-list').addEventListener('click', async (event) => {
  const button = event.target.closest('button[data-remove-participant]');
  if (!button) return;
  button.disabled = true;
  try {
    await apiFetch(`/activities/${activityId}/participants/${button.dataset.removeParticipant}`, { method: 'DELETE' });
    await loadParticipants();
  } catch (err) {
    button.disabled = false;
    showError(err.message);
  }
});

// A loan is always tied to an activity, and it's common for one trip
// to have plenty of gear going out — this section lets admin/board/
// material register (and return) those loans right here instead of
// hunting the item down on the material page. Item type/rope/bag
// labels are duplicated from js/inventory.js and js/member.js, the
// same way those two already duplicate them from each other.
const itemTypeLabels = { rope: 'Cuerda', bag: 'Saca', gear: 'Material' };
const ropeTypeLabels = { caving: 'Espeleología', canyoning: 'Cañones' };
const bagTypeLabels = { caving: 'Espeleología', canyoning: 'Cañones', quick: 'Rápida', special: 'Especial' };

function itemLabel(item) {
  const brandModel = [item.brand, item.model].filter(Boolean).join(' ');
  if (item.item_type === 'rope') {
    const parts = [brandModel || null, `${item.diameter_mm} mm`, ropeTypeLabels[item.rope_type] ?? item.rope_type, item.manufacture_year, item.length_m ? `${item.length_m} m` : null];
    return escapeHtml(parts.filter(Boolean).join(' · '));
  }
  if (item.item_type === 'bag') {
    const parts = [brandModel || null, `${item.capacity_liters} L`, bagTypeLabels[item.bag_type] ?? item.bag_type];
    return escapeHtml(parts.filter(Boolean).join(' · '));
  }
  const parts = [item.category_name, brandModel || null, item.description || null];
  return escapeHtml(parts.filter(Boolean).join(' — '));
}

// Plain-text counterpart of itemLabel for the picker's <option> values —
// renderPickerOptions/pickerLabel already escape it, so this must not.
function itemPickerText(item) {
  const brandModel = [item.brand, item.model].filter(Boolean).join(' ');
  const typeLabel = itemTypeLabels[item.item_type] ?? item.item_type;
  const parts = item.item_type === 'gear'
    ? [typeLabel, item.category_name, brandModel || null, item.description || null]
    : [typeLabel, brandModel || null, item.code];
  return parts.filter(Boolean).join(' · ');
}

let itemsById = {};
let itemEntries = [];

async function loadItems() {
  if (!canManageMovements) return;
  const optionsEl = document.getElementById('loan-item-options');
  try {
    const items = await apiFetch('/items');
    itemsById = Object.fromEntries(items.map((item) => [item.id, item]));
    itemEntries = items.map((item) => ({ id: item.id, label: pickerLabel(itemPickerText(item), item.id) }));
    if (optionsEl) optionsEl.innerHTML = renderPickerOptions(itemEntries);
  } catch {
    showError('No se pudo cargar la lista de material.');
  }
}

// Sums this activity's loan and return movements per item — a return
// isn't linked to any one specific loan row, so what's still
// outstanding is always every loan minus every return for that item,
// not a per-movement "returned_quantity" (see
// Repository.CreateMovement on the backend).
function aggregateActivityLoans(movements) {
  const byItem = new Map();
  for (const m of movements) {
    if (m.type !== 'loan' && m.type !== 'return') continue;
    const totals = byItem.get(m.item_id) ?? { loaned: 0, returned: 0 };
    if (m.type === 'loan') totals.loaned += m.quantity;
    else totals.returned += m.quantity;
    byItem.set(m.item_id, totals);
  }
  return byItem;
}

function renderActivityLoanRow(itemId, totals) {
  const item = itemsById[itemId];
  const outstanding = totals.loaned - totals.returned;
  return `
    <tr>
      <td>${item ? itemLabel(item) : `Material n.º ${itemId}`}</td>
      <td>${outstanding}</td>
      <td>${totals.loaned}</td>
      <td>${outstanding > 0 ? `<button type="button" class="btn btn-secondary btn-small" data-prefill-return="${itemId}">Devolver</button>` : '—'}</td>
    </tr>
  `;
}

// GET /movements?activity_id= returns every movement tied to this
// activity — chiefly loan and return, which is what this list is built
// from; anything else that happens to share this activity_id (a
// rental, say) is simply ignored here.
async function loadActivityLoans() {
  if (!canManageMovements) return;
  const tableBody = document.getElementById('activity-loans-table-body');
  if (!tableBody) return;
  try {
    const movements = await apiFetch(`/movements?activity_id=${activityId}`);
    const byItem = [...aggregateActivityLoans(movements)].filter(([, totals]) => totals.loaned > 0);
    tableBody.innerHTML = byItem.length
      ? byItem.map(([itemId, totals]) => renderActivityLoanRow(itemId, totals)).join('')
      : '<tr><td colspan="4">No hay material prestado para esta actividad.</td></tr>';
  } catch {
    tableBody.innerHTML = '<tr><td colspan="4">No se pudieron cargar los préstamos.</td></tr>';
  }
}

const loanTypeSelect = document.getElementById('loan-type');
const loanItemInput = document.getElementById('loan-item');
const loanQuantityField = document.getElementById('loan-quantity-field');
const loanQuantityInput = document.getElementById('loan-quantity');
const loanSubmitButton = document.getElementById('loan-submit');

// rope/bag are individually tracked (always exactly one unit), so
// there's no quantity to choose once one of those is picked.
function updateLoanQuantityVisibility() {
  if (!loanItemInput) return;
  const itemId = resolvePickerId(itemEntries, loanItemInput.value);
  const item = itemId != null ? itemsById[itemId] : null;
  const isSingleUnit = item?.item_type === 'rope' || item?.item_type === 'bag';
  loanQuantityField.hidden = isSingleUnit;
  if (isSingleUnit) loanQuantityInput.value = '1';
}
loanItemInput?.addEventListener('input', updateLoanQuantityVisibility);

function updateLoanSubmitLabel() {
  if (!loanTypeSelect || !loanSubmitButton) return;
  loanSubmitButton.textContent = loanTypeSelect.value === 'return' ? 'Registrar devolución' : 'Registrar préstamo';
}
loanTypeSelect?.addEventListener('change', updateLoanSubmitLabel);

// Clicking "Devolver" on a row above jumps straight to the shared form
// with this item and "Devolución" preselected, instead of a separate
// per-row quantity control — the quantity to return still needs typing
// in (it can be less than everything outstanding), so this only saves
// picking the item and type by hand.
document.getElementById('activity-loans-table-body')?.addEventListener('click', (event) => {
  const button = event.target.closest('button[data-prefill-return]');
  if (!button || !loanItemInput) return;
  const itemId = Number(button.dataset.prefillReturn);
  const entry = itemEntries.find((e) => e.id === itemId);
  if (!entry) return;
  if (loanTypeSelect) loanTypeSelect.value = 'return';
  updateLoanSubmitLabel();
  loanItemInput.value = entry.label;
  updateLoanQuantityVisibility();
  document.getElementById('new-loan-form').scrollIntoView({ behavior: 'smooth', block: 'center' });
  loanQuantityInput?.focus();
});

document.getElementById('new-loan-form')?.addEventListener('submit', async (event) => {
  event.preventDefault();
  // event.currentTarget reverts to null once dispatch finishes, which
  // happens before the `await apiFetch(...)` below resolves — capture
  // it now, not after.
  const form = event.currentTarget;
  const type = loanTypeSelect ? loanTypeSelect.value : 'loan';
  const itemId = resolvePickerId(itemEntries, loanItemInput.value);
  if (!itemId) {
    showError('Elige un material de la lista.');
    return;
  }
  // Optional either way — a loan or return's mandatory link is this
  // activity, not necessarily one specific person.
  const userInput = document.getElementById('loan-user');
  let login = null;
  if (userInput.value.trim()) {
    login = resolvePickerId(personEntries, userInput.value);
    if (!login) {
      showError('Elige un socio de la lista, o déjalo vacío.');
      return;
    }
  }
  const notesInput = document.getElementById('loan-notes');

  const body = {
    item_id: itemId,
    login,
    type,
    quantity: loanQuantityField.hidden ? 1 : Number(loanQuantityInput.value) || 1,
    activity_id: activityId,
    // No date field in this form — always today (transacted_at left
    // unset defaults to now() server-side).
    notes: notesInput.value.trim() || null,
  };

  const submitButton = form.querySelector('button[type="submit"]');
  submitButton.disabled = true;
  try {
    await apiFetch('/movements', { method: 'POST', body: JSON.stringify(body) });
    showFeedback(type === 'return' ? 'Devolución registrada.' : 'Préstamo registrado.');
    form.reset();
    updateLoanQuantityVisibility();
    updateLoanSubmitLabel();
    await loadActivityLoans();
  } catch (err) {
    showError(err.message);
  } finally {
    submitButton.disabled = false;
  }
});

await loadDirectory();
await loadActivity();
loadParticipants();
await loadItems();
loadActivityLoans();
