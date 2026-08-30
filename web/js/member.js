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
if (!hasAnyRole(viewer, ['admin', 'board'])) {
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

const editForm = document.getElementById('edit-member-form');

async function loadMember() {
  try {
    const person = await apiFetch(`/users/${memberId}`);
    document.getElementById('member-name').textContent = `${person.first_name} ${person.last_name}`;
    document.getElementById('member-email').textContent = person.email;
    document.getElementById('member-roles').textContent = person.roles.length
      ? `Roles: ${person.roles.map(roleLabel).join(', ')}`
      : 'Sin roles asignados.';

    editForm.first_name.value = person.first_name;
    editForm.last_name.value = person.last_name;
    editForm.email.value = person.email;
    editForm.phone.value = person.phone ?? '';
    editForm.national_id.value = person.national_id ?? '';
    editForm.birth_date.value = person.birth_date ? person.birth_date.slice(0, 10) : '';
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
    birth_date: optional('birth_date'),
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
      <td>${escapeHtml(activity.title)}</td>
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

loadMember();
loadActivities();
loadTransactions();
