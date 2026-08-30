import {
  initSession,
  logout,
  applyRoleVisibility,
  currentUser,
  hasAnyRole,
  apiFetch,
  escapeHtml,
  formatDate,
} from './api.js';

await initSession();
applyRoleVisibility();
document.getElementById('logout-button').addEventListener('click', logout);

const viewer = currentUser();
const isAdmin = hasAnyRole(viewer, ['admin', 'board']);
const isRealAdmin = hasAnyRole(viewer, ['admin']);
if (!isAdmin) {
  window.location.href = '/';
}

const memberId = Number(new URLSearchParams(window.location.search).get('id'));
if (!memberId) {
  window.location.href = '/members';
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

// Same friendly labels as members.js — kept in sync there since neither
// file is big enough yet to justify sharing them through api.js.
const knownRoleLabels = { admin: 'Administración', board: 'Directiva', member: 'Socio', former_member: 'Ex-socio' };
function roleLabel(name) {
  return knownRoleLabels[name] ?? name;
}

// "member"/"former_member" are computed from membership history and
// can't be granted or revoked by hand at all. Anything else, any
// admin/board caller can manage except "admin", which only an admin can
// grant or revoke (avoids a board account minting new admins).
function canManageRole(roleName) {
  if (rolesByName[roleName]?.is_automatic) return false;
  return roleName === 'admin' ? isRealAdmin : isAdmin;
}

let allRoles = [];
let rolesByName = {};

async function loadRolesCatalog() {
  try {
    allRoles = await apiFetch('/roles');
    rolesByName = Object.fromEntries(allRoles.map((role) => [role.name, role]));
  } catch {
    allRoles = [];
    rolesByName = {};
  }
}

function renderRoleBadges(person) {
  if (person.roles.length === 0) {
    return '<p>Sin roles asignados.</p>';
  }
  return person.roles
    .map((name) => {
      const label = escapeHtml(roleLabel(name));
      if (!canManageRole(name)) {
        return `<span class="badge">${label}</span>`;
      }
      const roleId = rolesByName[name]?.id;
      return `<span class="badge-removable">${label}<button type="button" data-remove-role="${roleId}" title="Quitar rol">×</button></span>`;
    })
    .join('');
}

function renderRoleAddControl(person) {
  const assignable = allRoles.filter((role) => !person.roles.includes(role.name) && canManageRole(role.name));
  if (assignable.length === 0) {
    return '';
  }
  const options = assignable
    .map((role) => `<option value="${role.id}">${escapeHtml(roleLabel(role.name))}</option>`)
    .join('');
  return `
    <div class="role-add-row">
      <select id="role-select">${options}</select>
      <button type="button" id="add-role" class="btn btn-secondary btn-small">+ Rol</button>
    </div>
  `;
}

function renderRoles(person) {
  document.getElementById('member-roles-list').innerHTML = renderRoleBadges(person);
  document.getElementById('member-role-add').innerHTML = renderRoleAddControl(person);
}

document.getElementById('member-roles-list').addEventListener('click', async (event) => {
  const button = event.target.closest('button[data-remove-role]');
  if (!button) return;
  button.disabled = true;
  try {
    await apiFetch(`/users/${memberId}/roles/${button.dataset.removeRole}`, { method: 'DELETE' });
    showFeedback('Rol quitado.');
    await loadMember();
  } catch (err) {
    button.disabled = false;
    showError(err.message);
  }
});

document.getElementById('member-role-add').addEventListener('click', async (event) => {
  const button = event.target.closest('#add-role');
  if (!button) return;
  const roleId = document.getElementById('role-select').value;
  button.disabled = true;
  try {
    await apiFetch(`/users/${memberId}/roles`, { method: 'POST', body: JSON.stringify({ role_id: Number(roleId) }) });
    showFeedback('Rol añadido.');
    await loadMember();
  } catch (err) {
    button.disabled = false;
    showError(err.message);
  }
});

const editForm = document.getElementById('edit-member-form');

async function loadMember() {
  try {
    const person = await apiFetch(`/users/${memberId}`);
    document.getElementById('member-name').textContent = `${person.first_name} ${person.last_name}`;
    document.getElementById('member-email').textContent = person.email;
    document.getElementById('member-last-login').textContent = person.last_login_at
      ? `Último acceso: ${formatDate(person.last_login_at)}${person.last_login_ip ? ` desde ${person.last_login_ip}` : ''}`
      : 'Todavía no ha iniciado sesión.';
    renderRoles(person);

    editForm.first_name.value = person.first_name;
    editForm.last_name.value = person.last_name;
    editForm.email.value = person.email;
    editForm.phone.value = person.phone ?? '';
    editForm.national_id.value = person.national_id ?? '';
    editForm.membership_number.value = person.membership_number ?? '';
    editForm.birth_date.value = person.birth_date ? person.birth_date.slice(0, 10) : '';
    editForm.gender.value = person.gender ?? '';
    editForm.address.value = person.address ?? '';
    editForm.postal_code.value = person.postal_code ?? '';
    editForm.city.value = person.city ?? '';
    editForm.province.value = person.province ?? '';
    editForm.emergency_contact.value = person.emergency_contact ?? '';
  } catch (err) {
    document.getElementById('member-email').textContent = 'No se ha podido cargar esta persona.';
    showError(err.message);
  }
}

editForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  const formData = new FormData(editForm);
  const optional = (key) => formData.get(key)?.toString().trim() || null;

  const body = {
    first_name: formData.get('first_name'),
    last_name: formData.get('last_name'),
    email: formData.get('email'),
    phone: optional('phone'),
    national_id: optional('national_id'),
    membership_number: optional('membership_number'),
    birth_date: optional('birth_date'),
    gender: optional('gender'),
    address: optional('address'),
    postal_code: optional('postal_code'),
    city: optional('city'),
    province: optional('province'),
    emergency_contact: optional('emergency_contact'),
  };

  const submitButton = editForm.querySelector('button[type="submit"]');
  submitButton.disabled = true;
  try {
    await apiFetch(`/users/${memberId}`, { method: 'PATCH', body: JSON.stringify(body) });
    showFeedback('Datos actualizados.');
    document.getElementById('member-name').textContent = `${body.first_name} ${body.last_name}`;
    document.getElementById('member-email').textContent = body.email;
  } catch (err) {
    showError(err.message);
  } finally {
    submitButton.disabled = false;
  }
});

// The membership/leave edit panels are the same regardless of which
// timeline event (alta/baja, inicio/fin) opened them — a period or a
// leave is always corrected as a whole, both dates at once — so each is
// rendered once per record and only ever attached to that record's
// first (earliest) event: alta for a period, inicio for a leave.
function renderMembershipEditPanel(m) {
  return `
    <div class="edit-fields" data-edit-membership-panel="${m.id}" hidden>
      <div class="field-grid">
        <div class="field">
          <label>Fecha de alta</label>
          <input type="date" data-membership-field="start_date" value="${m.start_date.slice(0, 10)}">
        </div>
        <div class="field">
          <label>Fecha de baja <span class="optional">(vacío = sigue activa)</span></label>
          <input type="date" data-membership-field="end_date" value="${m.end_date ? m.end_date.slice(0, 10) : ''}">
        </div>
      </div>
      <button type="button" class="btn btn-primary btn-small" data-save-membership="${m.id}">Guardar</button>
    </div>
  `;
}

function renderLeaveEditPanel(l) {
  return `
    <div class="edit-fields" data-edit-leave-panel="${l.id}" hidden>
      <div class="field-grid">
        <div class="field">
          <label>Fecha de inicio</label>
          <input type="date" data-leave-field="start_date" value="${l.start_date.slice(0, 10)}">
        </div>
        <div class="field">
          <label>Fecha de fin</label>
          <input type="date" data-leave-field="end_date" value="${l.end_date.slice(0, 10)}">
        </div>
        <div class="field span-2">
          <label>Motivo <span class="optional">(opcional)</span></label>
          <input type="text" data-leave-field="reason" value="${escapeHtml(l.reason ?? '')}">
        </div>
      </div>
      <button type="button" class="btn btn-primary btn-small" data-save-leave="${l.id}">Guardar</button>
    </div>
  `;
}

const eventLabels = {
  alta: 'Alta',
  baja: 'Baja',
  leave_start: 'Inicio de baja temporal',
  leave_end: 'Fin de baja temporal',
};

// Builds one flat, chronological list of events out of every membership
// period and every temporary leave within it — a "baja temporal" always
// belongs to exactly one period (see docs/architecture.md), so fetching
// each period's leaves alongside it is enough to cover the whole history.
async function buildTimelineEvents(memberships) {
  const events = [];
  for (const m of memberships) {
    events.push({ date: m.start_date, type: 'alta', membership: m });
    if (m.end_date) {
      events.push({ date: m.end_date, type: 'baja', membership: m, reason: m.end_reason });
    }
    const leaves = await apiFetch(`/memberships/${m.id}/leaves`).catch(() => []);
    for (const l of leaves) {
      events.push({ date: l.start_date, type: 'leave_start', membership: m, leave: l, reason: l.reason });
      events.push({ date: l.end_date, type: 'leave_end', membership: m, leave: l, reason: l.reason });
    }
  }
  events.sort((a, b) => b.date.localeCompare(a.date));
  return events;
}

function renderTimelineEvent(event) {
  const reasonText = event.reason ? ` · ${escapeHtml(event.reason)}` : '';
  let actions = '';
  let panel = '';

  if (event.type === 'alta') {
    actions = `
      <button type="button" class="btn btn-secondary btn-small" data-toggle-edit-membership="${event.membership.id}">Editar</button>
      <button type="button" class="btn btn-secondary btn-small" data-add-leave="${event.membership.id}">+ Baja temporal</button>
    `;
    panel = renderMembershipEditPanel(event.membership);
  } else if (event.type === 'leave_start') {
    actions = `
      <button type="button" class="btn btn-secondary btn-small" data-toggle-edit-leave="${event.leave.id}">Editar</button>
      <button type="button" class="btn btn-danger btn-small" data-delete-leave="${event.leave.id}">Borrar</button>
    `;
    panel = renderLeaveEditPanel(event.leave);
  }

  return `
    <div class="detail-row">
      <span>${formatDate(event.date)} — ${eventLabels[event.type]}${reasonText}</span>
      <span>${actions}</span>
    </div>
    ${panel}
  `;
}

async function loadTimeline() {
  const container = document.getElementById('timeline-list');
  container.innerHTML = 'Cargando…';
  try {
    const memberships = await apiFetch(`/users/${memberId}/memberships`);
    const events = await buildTimelineEvents(memberships);
    container.innerHTML = events.length
      ? events.map(renderTimelineEvent).join('')
      : '<p>Todavía no ha sido socio.</p>';
  } catch (err) {
    container.innerHTML = `<p>${escapeHtml(err.message)}</p>`;
  }
}

document.getElementById('start-membership').addEventListener('click', async () => {
  const startDate = prompt('Fecha de alta (AAAA-MM-DD):', new Date().toISOString().slice(0, 10));
  if (!startDate) return;

  try {
    await apiFetch(`/users/${memberId}/memberships`, {
      method: 'POST',
      body: JSON.stringify({ start_date: startDate }),
    });
    showFeedback('Alta registrada.');
    await loadTimeline();
    await loadMember();
  } catch (err) {
    showError(err.message);
  }
});

document.getElementById('timeline-list').addEventListener('click', async (event) => {
  const toggleButton = event.target.closest('button[data-toggle-edit-membership]');
  if (toggleButton) {
    const panel = document.querySelector(`[data-edit-membership-panel="${toggleButton.dataset.toggleEditMembership}"]`);
    panel.hidden = !panel.hidden;
    return;
  }

  const addLeaveButton = event.target.closest('button[data-add-leave]');
  if (addLeaveButton) {
    const membershipId = addLeaveButton.dataset.addLeave;
    const startDate = prompt('Fecha de inicio de la baja temporal (AAAA-MM-DD):');
    if (!startDate) return;
    const endDate = prompt('Fecha de fin (AAAA-MM-DD):');
    if (!endDate) return;
    const reason = prompt('Motivo (opcional):') || null;

    addLeaveButton.disabled = true;
    try {
      await apiFetch(`/memberships/${membershipId}/leaves`, {
        method: 'POST',
        body: JSON.stringify({ start_date: startDate, end_date: endDate, reason }),
      });
      showFeedback('Baja temporal registrada.');
      await loadTimeline();
    } catch (err) {
      addLeaveButton.disabled = false;
      showError(err.message);
    }
    return;
  }

  const toggleEditLeaveButton = event.target.closest('button[data-toggle-edit-leave]');
  if (toggleEditLeaveButton) {
    const panel = document.querySelector(`[data-edit-leave-panel="${toggleEditLeaveButton.dataset.toggleEditLeave}"]`);
    panel.hidden = !panel.hidden;
    return;
  }

  const saveLeaveButton = event.target.closest('button[data-save-leave]');
  if (saveLeaveButton) {
    const leaveId = saveLeaveButton.dataset.saveLeave;
    const editPanel = document.querySelector(`[data-edit-leave-panel="${leaveId}"]`);
    const field = (name) => editPanel.querySelector(`[data-leave-field="${name}"]`).value;
    const body = {
      start_date: field('start_date'),
      end_date: field('end_date'),
      reason: field('reason').trim() || null,
    };

    saveLeaveButton.disabled = true;
    try {
      await apiFetch(`/leaves/${leaveId}`, { method: 'PATCH', body: JSON.stringify(body) });
      showFeedback('Baja temporal actualizada.');
      await loadTimeline();
    } catch (err) {
      saveLeaveButton.disabled = false;
      showError(err.message);
    }
    return;
  }

  const deleteLeaveButton = event.target.closest('button[data-delete-leave]');
  if (deleteLeaveButton) {
    if (!confirm('¿Borrar esta baja temporal?')) return;
    deleteLeaveButton.disabled = true;
    try {
      await apiFetch(`/leaves/${deleteLeaveButton.dataset.deleteLeave}`, { method: 'DELETE' });
      await loadTimeline();
    } catch (err) {
      deleteLeaveButton.disabled = false;
      showError(err.message);
    }
    return;
  }

  const saveButton = event.target.closest('button[data-save-membership]');
  if (saveButton) {
    const membershipId = saveButton.dataset.saveMembership;
    const panel = document.querySelector(`[data-edit-membership-panel="${membershipId}"]`);
    const field = (name) => panel.querySelector(`[data-membership-field="${name}"]`).value;

    const body = {
      start_date: field('start_date'),
      end_date: field('end_date') || null,
    };

    saveButton.disabled = true;
    try {
      await apiFetch(`/memberships/${membershipId}`, { method: 'PATCH', body: JSON.stringify(body) });
      showFeedback('Periodo actualizado.');
      await loadTimeline();
      await loadMember();
    } catch (err) {
      saveButton.disabled = false;
      showError(err.message);
    }
  }
});

document.getElementById('delete-member').addEventListener('click', async () => {
  if (!confirm('¿Borrar a esta persona? Se perderá su ficha, su acceso y su historial de socio.')) return;
  const button = document.getElementById('delete-member');
  button.disabled = true;
  try {
    await apiFetch(`/users/${memberId}`, { method: 'DELETE' });
    window.location.href = '/members';
  } catch (err) {
    button.disabled = false;
    showError(err.message);
  }
});

const activityTypeLabels = { caving: 'Espeleología', training: 'Formación', outdoor: 'Exterior', social: 'Social', other: 'Otra' };
const activityStatusLabels = { planned: 'Planificada', completed: 'Realizada', cancelled: 'Cancelada' };

function renderActivityRow(activity) {
  const dateLabel = activity.end_date
    ? `${formatDate(activity.start_date)} – ${formatDate(activity.end_date)}`
    : formatDate(activity.start_date);
  const role = activity.leader_id === memberId ? 'Responsable' : 'Participante';
  return `
    <tr>
      <td>${dateLabel}</td>
      <td><a href="/activity?id=${activity.id}">${escapeHtml(activity.title)}</a></td>
      <td>${escapeHtml(activity.location)}</td>
      <td>${activityTypeLabels[activity.type] ?? activity.type}</td>
      <td><span class="badge status-${activity.status}">${activityStatusLabels[activity.status] ?? activity.status}</span></td>
      <td>${role}</td>
    </tr>
  `;
}

async function loadActivities() {
  const tableBody = document.getElementById('activities-table-body');
  try {
    const activities = await apiFetch(`/users/${memberId}/activities`);
    tableBody.innerHTML = activities.length
      ? activities.map(renderActivityRow).join('')
      : '<tr><td colspan="6">Todavía no ha participado en ninguna actividad.</td></tr>';
  } catch {
    tableBody.innerHTML = '<tr><td colspan="6">No se pudo cargar el historial de actividades.</td></tr>';
  }
}

const transactionTypeLabels = { loan: 'Préstamo', rental: 'Alquiler', sale: 'Venta' };
const transactionStatusLabels = { active: 'Activa', completed: 'Completada', overdue: 'Vencida', cancelled: 'Cancelada' };

function renderTransactionRow(t, itemsById) {
  const itemName = itemsById[t.item_id]?.name;
  const dueOrReturned = t.returned_at
    ? `Devuelto ${formatDate(t.returned_at)}`
    : t.due_date
      ? `Vence ${formatDate(t.due_date)}`
      : '—';
  return `
    <tr>
      <td>${itemName ? escapeHtml(itemName) : `Material n.º ${t.item_id}`}</td>
      <td>${transactionTypeLabels[t.type] ?? t.type}</td>
      <td>${t.quantity}</td>
      <td>${formatDate(t.transacted_at)}</td>
      <td>${dueOrReturned}</td>
      <td><span class="badge">${transactionStatusLabels[t.status] ?? t.status}</span></td>
    </tr>
  `;
}

async function loadTransactions() {
  const tableBody = document.getElementById('transactions-table-body');
  try {
    const [items, transactions] = await Promise.all([
      apiFetch('/items').catch(() => []),
      apiFetch(`/users/${memberId}/transactions`),
    ]);
    const itemsById = Object.fromEntries(items.map((item) => [item.id, item]));
    tableBody.innerHTML = transactions.length
      ? transactions.map((t) => renderTransactionRow(t, itemsById)).join('')
      : '<tr><td colspan="6">Todavía no tiene movimientos de material.</td></tr>';
  } catch {
    tableBody.innerHTML = '<tr><td colspan="6">No se pudo cargar el historial de material.</td></tr>';
  }
}

await loadRolesCatalog();
loadMember();
loadTimeline();
loadActivities();
loadTransactions();
