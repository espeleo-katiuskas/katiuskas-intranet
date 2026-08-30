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

const user = currentUser();
const isAdmin = hasAnyRole(user, ['admin', 'board']);
const isRealAdmin = hasAnyRole(user, ['admin']);
if (!isAdmin) {
  window.location.href = '/';
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

// The system roles (see docs/architecture.md) get a friendlier Spanish
// label; anything an admin created shows as typed.
const knownRoleLabels = { admin: 'Administración', board: 'Directiva', member: 'Socio', former_member: 'Ex-socio' };
function roleLabel(name) {
  return knownRoleLabels[name] ?? name;
}

// "member"/"former_member" are computed from membership history — see
// docs/architecture.md — and can't be granted or revoked by hand at all,
// regardless of who's asking. Anything else, any caller can manage
// except "admin", which only an admin can grant or revoke (avoids a
// board account minting new admins).
function canManageRole(roleName) {
  if (rolesByName[roleName]?.is_automatic) return false;
  return roleName === 'admin' ? isRealAdmin : isAdmin;
}

let allRoles = [];
let rolesByName = {};

async function loadRoles() {
  const tableBody = document.getElementById('roles-table-body');
  try {
    allRoles = await apiFetch('/roles');
    rolesByName = Object.fromEntries(allRoles.map((role) => [role.name, role]));
    tableBody.innerHTML = allRoles
      .map((role) => `
        <tr>
          <td>${escapeHtml(roleLabel(role.name))}</td>
          <td>${escapeHtml(role.description) || '—'}</td>
          <td>
            ${role.is_system ? '<span class="badge">Del sistema</span>' : ''}
            ${role.is_automatic ? '<span class="badge" title="Se asigna solo, según el historial de socio">Automático</span>' : ''}
          </td>
          <td>${!role.is_system && isRealAdmin ? `<button type="button" class="btn btn-danger btn-small" data-delete-role="${role.id}">Borrar</button>` : ''}</td>
        </tr>
      `)
      .join('');

    const group = document.getElementById('np-roles-group');
    if (group) {
      group.innerHTML = allRoles
        .filter((role) => !role.is_automatic && (role.name !== 'admin' || isRealAdmin))
        .map((role) => `
          <label>
            <input type="checkbox" name="role_names" value="${escapeHtml(role.name)}">
            ${escapeHtml(roleLabel(role.name))}
          </label>
        `)
        .join('');
    }
  } catch {
    tableBody.innerHTML = '<tr><td colspan="4">No se pudieron cargar los roles.</td></tr>';
  }
}

document.getElementById('roles-table-body').addEventListener('click', async (event) => {
  const button = event.target.closest('button[data-delete-role]');
  if (!button) return;
  if (!confirm('¿Borrar este rol? Se quitará de todas las personas que lo tengan.')) return;

  button.disabled = true;
  try {
    await apiFetch(`/roles/${button.dataset.deleteRole}`, { method: 'DELETE' });
    showFeedback('Rol borrado.');
    await loadRoles();
    await loadUsers();
  } catch (err) {
    button.disabled = false;
    showError(err.message);
  }
});

if (isRealAdmin) {
  const newRolePanel = document.getElementById('new-role-panel');
  const newRoleForm = document.getElementById('new-role-form');

  document.getElementById('toggle-new-role').addEventListener('click', () => {
    newRolePanel.hidden = !newRolePanel.hidden;
  });

  document.getElementById('cancel-new-role').addEventListener('click', () => {
    newRoleForm.reset();
    newRolePanel.hidden = true;
  });

  newRoleForm.addEventListener('submit', async (event) => {
    event.preventDefault();
    const formData = new FormData(newRoleForm);
    const body = {
      name: formData.get('name'),
      description: formData.get('description')?.toString().trim() || null,
    };

    const submitButton = newRoleForm.querySelector('button[type="submit"]');
    submitButton.disabled = true;
    try {
      await apiFetch('/roles', { method: 'POST', body: JSON.stringify(body) });
      showFeedback('Rol creado.');
      newRoleForm.reset();
      newRolePanel.hidden = true;
      await loadRoles();
    } catch (err) {
      showError(err.message);
    } finally {
      submitButton.disabled = false;
    }
  });
}

// Kept in memory so "Dar de baja" can send the period's current
// start_date back — PATCH /memberships/{id} always replaces it, since
// that's also what lets an admin correct it later (see loadHistory).
let activeMembershipsById = {};

async function loadMemberships() {
  const tableBody = document.getElementById('memberships-table-body');
  try {
    const memberships = await apiFetch('/memberships');
    activeMembershipsById = Object.fromEntries(memberships.map((m) => [m.id, m]));
    tableBody.innerHTML = memberships.length
      ? memberships
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
          .join('')
      : '<tr><td colspan="4">Todavía no hay socios activos.</td></tr>';
  } catch {
    tableBody.innerHTML = '<tr><td colspan="4">No se pudo cargar la lista de socios.</td></tr>';
  }
}

function renderRoleBadges(person) {
  return person.roles
    .map((name) => {
      const label = escapeHtml(roleLabel(name));
      if (!canManageRole(name)) {
        return `<span class="badge">${label}</span>`;
      }
      const roleId = rolesByName[name]?.id;
      return `<span class="badge-removable">${label}<button type="button" data-remove-role="${person.id}:${roleId}" title="Quitar rol">×</button></span>`;
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
      <select data-role-select="${person.id}">${options}</select>
      <button type="button" class="btn btn-secondary btn-small" data-add-role="${person.id}">+ Rol</button>
    </div>
  `;
}

function renderUserRow(person) {
  const lastLogin = person.last_login_at
    ? `<span title="${person.last_login_ip ? escapeHtml(person.last_login_ip) : 'IP desconocida'}">${formatDate(person.last_login_at)}</span>`
    : '<span class="badge">Nunca</span>';
  return `
    <tr>
      <td><a href="/member?id=${person.id}">${escapeHtml(person.first_name)} ${escapeHtml(person.last_name)}</a></td>
      <td>${escapeHtml(person.email)}</td>
      <td>${lastLogin}</td>
      <td>${renderRoleBadges(person)}${renderRoleAddControl(person)}</td>
      <td class="table-actions">
        <button type="button" class="btn btn-secondary btn-small" data-start-membership="${person.id}">Dar de alta</button>
        <button type="button" class="btn btn-secondary btn-small" data-toggle-history="${person.id}">Historial</button>
        <button type="button" class="btn btn-secondary btn-small" data-toggle-edit-person="${person.id}">Editar</button>
        <button type="button" class="btn btn-danger btn-small" data-delete-person="${person.id}">Borrar</button>
      </td>
    </tr>
    <tr data-history-row="${person.id}" hidden>
      <td colspan="5">
        <div class="detail-list" data-history-rows="${person.id}"></div>
      </td>
    </tr>
    <tr data-edit-person-row="${person.id}" hidden>
      <td colspan="5">
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
              <label>Email</label>
              <input type="email" data-edit-field="email" value="${escapeHtml(person.email)}">
            </div>
            <div class="field">
              <label>Teléfono</label>
              <input type="tel" data-edit-field="phone" value="${escapeHtml(person.phone ?? '')}">
            </div>
            <div class="field">
              <label>DNI</label>
              <input type="text" data-edit-field="national_id" value="${escapeHtml(person.national_id ?? '')}">
            </div>
            <div class="field">
              <label>Fecha de nacimiento</label>
              <input type="date" data-edit-field="birth_date" value="${person.birth_date ? person.birth_date.slice(0, 10) : ''}">
            </div>
            <div class="field span-2">
              <label>Dirección</label>
              <input type="text" data-edit-field="address" value="${escapeHtml(person.address ?? '')}">
            </div>
            <div class="field">
              <label>Código postal</label>
              <input type="text" data-edit-field="postal_code" value="${escapeHtml(person.postal_code ?? '')}">
            </div>
            <div class="field">
              <label>Ciudad</label>
              <input type="text" data-edit-field="city" value="${escapeHtml(person.city ?? '')}">
            </div>
            <div class="field">
              <label>Provincia</label>
              <input type="text" data-edit-field="province" value="${escapeHtml(person.province ?? '')}">
            </div>
            <div class="field span-2">
              <label>Contacto de emergencia</label>
              <textarea data-edit-field="emergency_contact" placeholder="Nombre, teléfono, correo... uno o varios contactos">${escapeHtml(person.emergency_contact ?? '')}</textarea>
            </div>
          </div>
          <button type="button" class="btn btn-primary btn-small" data-save-edit-person="${person.id}">Guardar</button>
        </div>
      </td>
    </tr>
  `;
}

function renderMembershipHistoryRow(m) {
  return `
    <div class="detail-row" data-membership-row="${m.id}">
      <span>${escapeHtml(m.membership_number) || 'Sin número'} · ${formatDate(m.start_date)} –
        ${m.end_date ? formatDate(m.end_date) : 'activa'}</span>
      <button type="button" class="btn btn-secondary btn-small" data-toggle-edit-membership="${m.id}">Editar</button>
    </div>
    <div class="edit-fields" data-edit-membership-panel="${m.id}" hidden>
      <div class="field-grid">
        <div class="field">
          <label>N.º de socio</label>
          <input type="text" data-membership-field="membership_number" value="${escapeHtml(m.membership_number ?? '')}">
        </div>
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

async function loadMembershipHistory(personId, container) {
  container.innerHTML = 'Cargando…';
  try {
    const history = await apiFetch(`/users/${personId}/memberships`);
    container.innerHTML = history.length
      ? history.map(renderMembershipHistoryRow).join('')
      : '<p>Todavía no ha sido socio.</p>';
  } catch (err) {
    container.innerHTML = `<p>${escapeHtml(err.message)}</p>`;
  }
}

async function loadUsers() {
  const tableBody = document.getElementById('users-table-body');
  try {
    const users = await apiFetch('/users');
    tableBody.innerHTML = users.length
      ? users.map(renderUserRow).join('')
      : '<tr><td colspan="5">Todavía no hay personas registradas.</td></tr>';
  } catch {
    tableBody.innerHTML = '<tr><td colspan="5">No se pudo cargar la lista de personas.</td></tr>';
  }
}

document.getElementById('memberships-table-body').addEventListener('click', async (event) => {
  const button = event.target.closest('button[data-end-membership]');
  if (!button) return;

  const endDate = prompt('Fecha de baja (AAAA-MM-DD):', new Date().toISOString().slice(0, 10));
  if (!endDate) return;
  const endReason = prompt('Motivo de la baja (opcional):') || null;
  const membership = activeMembershipsById[button.dataset.endMembership];

  button.disabled = true;
  try {
    await apiFetch(`/memberships/${button.dataset.endMembership}`, {
      method: 'PATCH',
      body: JSON.stringify({
        start_date: membership.start_date.slice(0, 10),
        end_date: endDate,
        end_reason: endReason,
      }),
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

  const addRoleButton = event.target.closest('button[data-add-role]');
  if (addRoleButton) {
    const personId = addRoleButton.dataset.addRole;
    const select = usersTableBody.querySelector(`select[data-role-select="${personId}"]`);
    const roleId = select.value;

    addRoleButton.disabled = true;
    try {
      await apiFetch(`/users/${personId}/roles`, {
        method: 'POST',
        body: JSON.stringify({ role_id: Number(roleId) }),
      });
      showFeedback('Rol añadido.');
      await loadUsers();
    } catch (err) {
      addRoleButton.disabled = false;
      showError(err.message);
    }
    return;
  }

  const removeRoleButton = event.target.closest('button[data-remove-role]');
  if (removeRoleButton) {
    const [personId, roleId] = removeRoleButton.dataset.removeRole.split(':');
    removeRoleButton.disabled = true;
    try {
      await apiFetch(`/users/${personId}/roles/${roleId}`, { method: 'DELETE' });
      showFeedback('Rol quitado.');
      await loadUsers();
    } catch (err) {
      removeRoleButton.disabled = false;
      showError(err.message);
    }
    return;
  }

  const toggleHistoryButton = event.target.closest('button[data-toggle-history]');
  if (toggleHistoryButton) {
    const personId = toggleHistoryButton.dataset.toggleHistory;
    const row = usersTableBody.querySelector(`[data-history-row="${personId}"]`);
    row.hidden = !row.hidden;
    if (!row.hidden) {
      const container = row.querySelector(`[data-history-rows="${personId}"]`);
      await loadMembershipHistory(personId, container);
    }
    return;
  }

  const toggleEditMembershipButton = event.target.closest('button[data-toggle-edit-membership]');
  if (toggleEditMembershipButton) {
    const panel = usersTableBody.querySelector(`[data-edit-membership-panel="${toggleEditMembershipButton.dataset.toggleEditMembership}"]`);
    panel.hidden = !panel.hidden;
    return;
  }

  const saveMembershipButton = event.target.closest('button[data-save-membership]');
  if (saveMembershipButton) {
    const membershipId = saveMembershipButton.dataset.saveMembership;
    const panel = usersTableBody.querySelector(`[data-edit-membership-panel="${membershipId}"]`);
    const container = panel.closest('[data-history-rows]');
    const personId = container.dataset.historyRows;
    const field = (name) => panel.querySelector(`[data-membership-field="${name}"]`).value;

    const body = {
      membership_number: field('membership_number').trim() || null,
      start_date: field('start_date'),
      end_date: field('end_date') || null,
    };

    saveMembershipButton.disabled = true;
    try {
      await apiFetch(`/memberships/${membershipId}`, { method: 'PATCH', body: JSON.stringify(body) });
      showFeedback('Periodo actualizado.');
      await loadMembershipHistory(personId, container);
      await loadMemberships();
    } catch (err) {
      saveMembershipButton.disabled = false;
      showError(err.message);
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
      email: field('email'),
      national_id: field('national_id'),
      birth_date: field('birth_date'),
      phone: field('phone'),
      address: field('address'),
      postal_code: field('postal_code'),
      city: field('city'),
      province: field('province'),
      emergency_contact: field('emergency_contact'),
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
    postal_code: optional('postal_code'),
    city: optional('city'),
    province: optional('province'),
    emergency_contact: optional('emergency_contact'),
    role_names: formData.getAll('role_names'),
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

await loadRoles();
loadMemberships();
loadUsers();
