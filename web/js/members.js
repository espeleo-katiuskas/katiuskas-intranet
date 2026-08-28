import {
  initSession,
  logout,
  applyRoleVisibility,
  currentUser,
  apiFetch,
  escapeHtml,
  formatDate,
} from './api.js';

await initSession();
applyRoleVisibility();
document.getElementById('logout-button').addEventListener('click', logout);

const user = currentUser();
if (user?.role === 'member') {
  window.location.href = 'index.html';
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

const roleLabels = { admin: 'Administración', board: 'Directiva', member: 'Socio' };

async function loadMemberships() {
  const tableBody = document.getElementById('memberships-table-body');
  try {
    const memberships = await apiFetch('/memberships');
    tableBody.innerHTML = memberships
      .map((membership) => `
        <tr>
          <td>${membership.first_name ? escapeHtml(`${membership.first_name} ${membership.last_name}`) : `Socio n.º ${membership.user_id}`}</td>
          <td>${escapeHtml(membership.membership_number) || '—'}</td>
          <td>${formatDate(membership.start_date)}</td>
          <td class="table-actions">
            <button type="button" class="btn btn-danger btn-small" data-end-membership="${membership.id}">Dar de baja</button>
          </td>
        </tr>
      `)
      .join('');
  } catch {
    tableBody.innerHTML = '<tr><td colspan="4">No se pudo cargar la lista de socios.</td></tr>';
  }
}

async function loadUsers() {
  const tableBody = document.getElementById('users-table-body');
  try {
    const users = await apiFetch('/users');
    tableBody.innerHTML = users
      .map((person) => `
        <tr>
          <td>${escapeHtml(person.first_name)} ${escapeHtml(person.last_name)}</td>
          <td>${escapeHtml(person.email)}</td>
          <td><span class="badge">${roleLabels[person.role] ?? person.role ?? '—'}</span></td>
          <td class="table-actions">
            <button type="button" class="btn btn-secondary btn-small" data-start-membership="${person.id}">Dar de alta</button>
          </td>
        </tr>
      `)
      .join('');
  } catch {
    tableBody.innerHTML = '<tr><td colspan="4">No se pudo cargar la lista de personas.</td></tr>';
  }
}

document.getElementById('memberships-table-body').addEventListener('click', async (event) => {
  const button = event.target.closest('button[data-end-membership]');
  if (!button) return;

  const endDate = prompt('Fecha de baja (AAAA-MM-DD):', new Date().toISOString().slice(0, 10));
  if (!endDate) return;
  const endReason = prompt('Motivo de la baja (opcional):') || null;

  button.disabled = true;
  try {
    await apiFetch(`/memberships/${button.dataset.endMembership}`, {
      method: 'PATCH',
      body: JSON.stringify({ end_date: endDate, end_reason: endReason }),
    });
    showFeedback('Baja registrada.');
    await loadMemberships();
  } catch (err) {
    button.disabled = false;
    showError(err.message);
  }
});

document.getElementById('users-table-body').addEventListener('click', async (event) => {
  const button = event.target.closest('button[data-start-membership]');
  if (!button) return;

  const startDate = prompt('Fecha de alta (AAAA-MM-DD):', new Date().toISOString().slice(0, 10));
  if (!startDate) return;
  const membershipNumber = prompt('Número de socio (opcional):') || null;

  button.disabled = true;
  try {
    await apiFetch(`/users/${button.dataset.startMembership}/memberships`, {
      method: 'POST',
      body: JSON.stringify({ start_date: startDate, membership_number: membershipNumber }),
    });
    showFeedback('Alta registrada.');
    await loadMemberships();
  } catch (err) {
    showError(err.message);
  } finally {
    button.disabled = false;
  }
});

const newPersonPanel = document.getElementById('new-person-panel');
const newPersonForm = document.getElementById('new-person-form');

document.getElementById('toggle-new-person').addEventListener('click', () => {
  newPersonPanel.hidden = !newPersonPanel.hidden;
});

document.getElementById('cancel-new-person').addEventListener('click', () => {
  newPersonForm.reset();
  newPersonPanel.hidden = true;
});

newPersonForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  const formData = new FormData(newPersonForm);
  const optional = (key) => formData.get(key)?.toString().trim() || null;

  const body = {
    first_name: formData.get('first_name'),
    last_name: formData.get('last_name'),
    email: formData.get('email'),
    phone: optional('phone'),
    national_id: optional('national_id'),
    birth_date: optional('birth_date'),
    address: optional('address'),
    emergency_contact_name: optional('emergency_contact_name'),
    emergency_contact_phone: optional('emergency_contact_phone'),
    username: formData.get('username'),
    password: formData.get('password'),
    role: formData.get('role'),
  };

  const submitButton = newPersonForm.querySelector('button[type="submit"]');
  submitButton.disabled = true;
  try {
    await apiFetch('/users', { method: 'POST', body: JSON.stringify(body) });
    showFeedback('Persona creada.');
    newPersonForm.reset();
    newPersonPanel.hidden = true;
    await loadUsers();
  } catch (err) {
    showError(err.message);
  } finally {
    submitButton.disabled = false;
  }
});

loadMemberships();
loadUsers();
