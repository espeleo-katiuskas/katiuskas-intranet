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

function renderUserRow(person) {
  return `
    <tr>
      <td>${escapeHtml(person.first_name)} ${escapeHtml(person.last_name)}</td>
      <td>${escapeHtml(person.email)}</td>
      <td><span class="badge">${roleLabels[person.role] ?? person.role ?? '—'}</span></td>
      <td class="table-actions">
        <button type="button" class="btn btn-secondary btn-small" data-start-membership="${person.id}">Dar de alta</button>
        <button type="button" class="btn btn-secondary btn-small" data-toggle-edit-person="${person.id}">Editar</button>
        <button type="button" class="btn btn-danger btn-small" data-delete-person="${person.id}">Borrar</button>
      </td>
    </tr>
    <tr data-edit-person-row="${person.id}" hidden>
      <td colspan="4">
        <div class="edit-fields">
          <div class="field-grid">
            <div class="field">
              <label>Nombre</label>
              <input type="text" data-edit-field="first_name" value="${escapeHtml(person.first_name)}">
            </div>
            <div class="field">
              <label>Apellidos</label>
              <input type="text" data-edit-field="last_name" value="${escapeHtml(person.last_name)}">
            </div>
            <div class="field">
              <label>Teléfono</label>
              <input type="tel" data-edit-field="phone" value="${escapeHtml(person.phone ?? '')}">
            </div>
            <div class="field span-2">
              <label>Dirección</label>
              <input type="text" data-edit-field="address" value="${escapeHtml(person.address ?? '')}">
            </div>
            <div class="field">
              <label>Contacto de emergencia</label>
              <input type="text" data-edit-field="emergency_contact_name" value="${escapeHtml(person.emergency_contact_name ?? '')}">
            </div>
            <div class="field">
              <label>Teléfono de emergencia</label>
              <input type="tel" data-edit-field="emergency_contact_phone" value="${escapeHtml(person.emergency_contact_phone ?? '')}">
            </div>
          </div>
          <button type="button" class="btn btn-primary btn-small" data-save-edit-person="${person.id}">Guardar</button>
        </div>
      </td>
    </tr>
  `;
}

async function loadUsers() {
  const tableBody = document.getElementById('users-table-body');
  try {
    const users = await apiFetch('/users');
    tableBody.innerHTML = users.length
      ? users.map(renderUserRow).join('')
      : '<tr><td colspan="4">Todavía no hay personas registradas.</td></tr>';
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

const usersTableBody = document.getElementById('users-table-body');

usersTableBody.addEventListener('click', async (event) => {
  const startButton = event.target.closest('button[data-start-membership]');
  if (startButton) {
    const startDate = prompt('Fecha de alta (AAAA-MM-DD):', new Date().toISOString().slice(0, 10));
    if (!startDate) return;
    const membershipNumber = prompt('Número de socio (opcional):') || null;

    startButton.disabled = true;
    try {
      await apiFetch(`/users/${startButton.dataset.startMembership}/memberships`, {
        method: 'POST',
        body: JSON.stringify({ start_date: startDate, membership_number: membershipNumber }),
      });
      showFeedback('Alta registrada.');
      await loadMemberships();
    } catch (err) {
      showError(err.message);
    } finally {
      startButton.disabled = false;
    }
    return;
  }

  const toggleEditButton = event.target.closest('button[data-toggle-edit-person]');
  if (toggleEditButton) {
    const row = usersTableBody.querySelector(`[data-edit-person-row="${toggleEditButton.dataset.toggleEditPerson}"]`);
    row.hidden = !row.hidden;
    return;
  }

  const saveEditButton = event.target.closest('button[data-save-edit-person]');
  if (saveEditButton) {
    const personId = saveEditButton.dataset.saveEditPerson;
    const row = usersTableBody.querySelector(`[data-edit-person-row="${personId}"]`);
    const field = (name) => row.querySelector(`[data-edit-field="${name}"]`).value.trim() || null;

    const body = {
      first_name: field('first_name'),
      last_name: field('last_name'),
      phone: field('phone'),
      address: field('address'),
      emergency_contact_name: field('emergency_contact_name'),
      emergency_contact_phone: field('emergency_contact_phone'),
    };

    saveEditButton.disabled = true;
    try {
      await apiFetch(`/users/${personId}`, { method: 'PATCH', body: JSON.stringify(body) });
      showFeedback('Datos actualizados.');
      await loadUsers();
    } catch (err) {
      saveEditButton.disabled = false;
      showError(err.message);
    }
    return;
  }

  const deleteButton = event.target.closest('button[data-delete-person]');
  if (deleteButton) {
    if (!confirm('¿Borrar a esta persona? Se perderá su ficha, su acceso y su historial de socio.')) return;
    deleteButton.disabled = true;
    try {
      await apiFetch(`/users/${deleteButton.dataset.deletePerson}`, { method: 'DELETE' });
      showFeedback('Persona borrada.');
      await loadUsers();
      await loadMemberships();
    } catch (err) {
      deleteButton.disabled = false;
      showError(err.message);
    }
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
