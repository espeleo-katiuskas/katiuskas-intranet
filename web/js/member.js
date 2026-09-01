import {
  initSession,
  logout,
  applyRoleVisibility,
  currentUser,
  hasAnyRole,
  apiFetch,
  escapeHtml,
  formatDate,
  setToken,
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

const memberLogin = new URLSearchParams(window.location.search).get('login');
if (!memberLogin) {
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
const knownRoleLabels = { admin: 'Administración', board: 'Junta', member: 'Socio', former_member: 'Ex-socio' };
function roleLabel(name) {
  return knownRoleLabels[name] ?? name;
}

// "member"/"former_member"/"board" are computed from membership/board
// history and can't be granted or revoked by hand at all. Everything
// else is admin-only now — board lost the ability to modify a person's
// data, role assignment included.
function canManageRole(roleName) {
  if (rolesByName[roleName]?.is_automatic) return false;
  return isRealAdmin;
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

// Loaded once (admin-only endpoint) so both the "+ Licencia" form and
// each existing license's inline edit panel can build their type
// <select> from the same catalog.
let allLicenseTypes = [];
let licenseTypesById = {};

function licenseTypeOptions(selectedId) {
  return allLicenseTypes
    .map((t) => `<option value="${t.id}" ${t.id === selectedId ? 'selected' : ''}>${escapeHtml(t.name)}</option>`)
    .join('');
}

async function loadLicenseTypesCatalog() {
  if (!isRealAdmin) return;
  try {
    allLicenseTypes = await apiFetch('/license-types');
    licenseTypesById = Object.fromEntries(allLicenseTypes.map((t) => [t.id, t]));
    document.getElementById('add-license-type').innerHTML = licenseTypeOptions();
  } catch {
    allLicenseTypes = [];
    licenseTypesById = {};
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
    await apiFetch(`/users/${memberLogin}/roles/${button.dataset.removeRole}`, { method: 'DELETE' });
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
    await apiFetch(`/users/${memberLogin}/roles`, { method: 'POST', body: JSON.stringify({ role_id: Number(roleId) }) });
    showFeedback('Rol añadido.');
    await loadMember();
  } catch (err) {
    button.disabled = false;
    showError(err.message);
  }
});

const editForm = document.getElementById('edit-member-form');

// Board can still see every field here (loadMember populates them
// regardless), but PATCH /users/{login} now only accepts a self-edit, and
// board is never "self" on someone else's page — so there's nothing a
// submit could do here but fail. Disabling the whole form instead of
// just hiding the submit button keeps the fields visibly read-only
// rather than looking editable and then erroring out.
if (isAdmin && !isRealAdmin) {
  for (const field of editForm.elements) {
    field.disabled = true;
  }
  document.getElementById('board-readonly-hint').hidden = false;
}

async function loadMember() {
  try {
    const person = await apiFetch(`/users/${memberLogin}`);
    document.getElementById('member-name').textContent = `${person.first_name} ${person.last_name}`;
    document.getElementById('member-email').textContent = person.email;
    document.getElementById('member-number').textContent = person.membership_number != null
      ? `N.º de socio: ${person.membership_number}`
      : 'Sin número de socio todavía.';
    document.getElementById('member-last-login').textContent = person.last_login_at
      ? `Último acceso: ${formatDate(person.last_login_at)}${person.last_login_ip ? ` desde ${person.last_login_ip}` : ''}`
      : 'Todavía no ha iniciado sesión.';
    renderRoles(person);

    editForm.first_name.value = person.first_name;
    editForm.last_name.value = person.last_name;
    editForm.email.value = person.email;
    editForm.phone.value = person.phone ?? '';
    editForm.national_id.value = person.national_id ?? '';
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
    birth_date: formData.get('birth_date'),
    email: optional('email'),
    phone: optional('phone'),
    national_id: optional('national_id'),
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
    await apiFetch(`/users/${memberLogin}`, { method: 'PATCH', body: JSON.stringify(body) });
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

// Same field-grid look as the edit panels, but with empty inputs — used
// to create a new leave rather than correct an existing one.
function renderLeaveAddPanel(membershipId) {
  return `
    <div class="edit-fields" data-add-leave-panel="${membershipId}" hidden>
      <div class="field-grid">
        <div class="field">
          <label>Fecha de inicio</label>
          <input type="date" data-new-leave-field="start_date">
        </div>
        <div class="field">
          <label>Fecha de fin</label>
          <input type="date" data-new-leave-field="end_date">
        </div>
        <div class="field span-2">
          <label>Motivo <span class="optional">(opcional)</span></label>
          <input type="text" data-new-leave-field="reason">
        </div>
      </div>
      <button type="button" class="btn btn-primary btn-small" data-save-new-leave="${membershipId}">Guardar</button>
    </div>
  `;
}

const positionLabels = {
  president: 'Presidente',
  vice_president: 'Vicepresidente',
  secretary: 'Secretario',
  treasurer: 'Tesorero',
  member_at_large: 'Vocal',
};

function renderBoardEditPanel(b) {
  const options = Object.entries(positionLabels)
    .map(([value, label]) => `<option value="${value}" ${value === b.position ? 'selected' : ''}>${label}</option>`)
    .join('');
  return `
    <div class="edit-fields" data-edit-board-panel="${b.id}" hidden>
      <div class="field-grid">
        <div class="field">
          <label>Cargo</label>
          <select data-board-field="position">${options}</select>
        </div>
        <div class="field">
          <label>Fecha de inicio</label>
          <input type="date" data-board-field="start_date" value="${b.start_date.slice(0, 10)}">
        </div>
        <div class="field">
          <label>Fecha de fin <span class="optional">(vacío = sigue en la junta)</span></label>
          <input type="date" data-board-field="end_date" value="${b.end_date ? b.end_date.slice(0, 10) : ''}">
        </div>
      </div>
      <button type="button" class="btn btn-primary btn-small" data-save-board="${b.id}">Guardar</button>
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

function renderLicenseEditPanel(l) {
  const chosen = licenseTypesById[l.license_type_id];
  return `
    <div class="edit-fields" data-edit-license-panel="${l.id}" hidden>
      <div class="field-grid">
        <div class="field">
          <label>Tipo de licencia</label>
          <select data-license-field="license_type_id" data-license-type-select="${l.id}">${licenseTypeOptions(l.license_type_id)}</select>
        </div>
        <div class="field" data-license-other-detail-field="${l.id}" ${chosen?.is_other ? '' : 'hidden'}>
          <label>¿Cuál?</label>
          <input type="text" data-license-field="other_detail" value="${escapeHtml(l.other_detail ?? '')}">
        </div>
        <div class="field">
          <label>Fecha de obtención</label>
          <input type="date" data-license-field="issued_date" value="${l.issued_date.slice(0, 10)}">
        </div>
      </div>
      <button type="button" class="btn btn-primary btn-small" data-save-license="${l.id}">Guardar</button>
    </div>
  `;
}

const eventLabels = {
  alta: 'Alta',
  baja: 'Baja',
  leave_start: 'Inicio de baja temporal',
  leave_end: 'Fin de baja temporal',
  license: 'Licencia federativa',
  board_start: 'Entra en la Junta Directiva',
  board_end: 'Sale de la Junta Directiva',
};

// Builds one flat, chronological list of events out of every membership
// period, every temporary leave within it (a "baja temporal" always
// belongs to exactly one period — see docs/architecture.md), every
// federative license this person has ever been issued, and every board
// term they've served (those are scoped to the person directly, not to
// any one membership period, the same way licenses are).
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

  const licenses = await apiFetch(`/users/${memberLogin}/licenses`).catch(() => []);
  for (const l of licenses) {
    events.push({ date: l.issued_date, type: 'license', license: l, reason: l.type });
  }

  const boardTerms = await apiFetch(`/users/${memberLogin}/board-members`).catch(() => []);
  for (const b of boardTerms) {
    const positionLabel = positionLabels[b.position] ?? b.position;
    events.push({ date: b.start_date, type: 'board_start', boardMember: b, reason: positionLabel });
    if (b.end_date) {
      events.push({ date: b.end_date, type: 'board_end', boardMember: b, reason: positionLabel });
    }
  }

  events.sort((a, b) => a.date.localeCompare(b.date));
  return events;
}

function renderTimelineEvent(event) {
  const reasonText = event.reason ? ` · ${escapeHtml(event.reason)}` : '';
  let actions = '';
  let panel = '';

  // These buttons are rendered fresh on every timeline reload, so
  // data-roles in static HTML can't reach them — gating admin-only
  // actions here is the only place it can happen.
  if (isRealAdmin && event.type === 'alta') {
    actions = `
      <button type="button" class="btn btn-secondary btn-small" data-toggle-edit-membership="${event.membership.id}">Editar</button>
      <button type="button" class="btn btn-secondary btn-small" data-toggle-add-leave="${event.membership.id}">+ Baja temporal</button>
    `;
    panel = renderMembershipEditPanel(event.membership) + renderLeaveAddPanel(event.membership.id);
  } else if (isRealAdmin && event.type === 'leave_start') {
    actions = `
      <button type="button" class="btn btn-secondary btn-small" data-toggle-edit-leave="${event.leave.id}">Editar</button>
      <button type="button" class="btn btn-danger btn-small" data-delete-leave="${event.leave.id}">Borrar</button>
    `;
    panel = renderLeaveEditPanel(event.leave);
  } else if (isRealAdmin && event.type === 'license') {
    actions = `
      <button type="button" class="btn btn-secondary btn-small" data-toggle-edit-license="${event.license.id}">Editar</button>
      <button type="button" class="btn btn-danger btn-small" data-delete-license="${event.license.id}">Borrar</button>
    `;
    panel = renderLicenseEditPanel(event.license);
  } else if (isRealAdmin && event.type === 'board_start') {
    actions = `
      <button type="button" class="btn btn-secondary btn-small" data-toggle-edit-board="${event.boardMember.id}">Editar</button>
    `;
    panel = renderBoardEditPanel(event.boardMember);
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
    const memberships = await apiFetch(`/users/${memberLogin}/memberships`);
    const events = await buildTimelineEvents(memberships);
    container.innerHTML = events.length
      ? events.map(renderTimelineEvent).join('')
      : '<p>Todavía no ha sido socio.</p>';
  } catch (err) {
    container.innerHTML = `<p>${escapeHtml(err.message)}</p>`;
  }
}

// "Dar de alta"/"+ Licencia"/"+ Junta directiva" and their panels all
// have data-roles="admin" in the markup — POST memberships/licenses/
// board-members is admin-only now, so applyRoleVisibility() already
// removed these elements for board; nothing to wire up in that case.
if (isRealAdmin) {
  document.getElementById('start-membership').addEventListener('click', () => {
    const panel = document.getElementById('add-membership-panel');
    const input = document.getElementById('add-membership-start-date');
    if (panel.hidden && !input.value) {
      input.value = new Date().toISOString().slice(0, 10);
    }
    panel.hidden = !panel.hidden;
  });

  document.getElementById('save-add-membership').addEventListener('click', async () => {
    const input = document.getElementById('add-membership-start-date');
    if (!input.value) {
      showError('Indica la fecha de alta.');
      return;
    }

    const button = document.getElementById('save-add-membership');
    button.disabled = true;
    try {
      await apiFetch(`/users/${memberLogin}/memberships`, {
        method: 'POST',
        body: JSON.stringify({ start_date: input.value }),
      });
      showFeedback('Alta registrada.');
      document.getElementById('add-membership-panel').hidden = true;
      input.value = '';
      await loadTimeline();
      await loadMember();
    } catch (err) {
      showError(err.message);
    } finally {
      button.disabled = false;
    }
  });
}

document.getElementById('timeline-list').addEventListener('change', (event) => {
  const select = event.target.closest('select[data-license-type-select]');
  if (!select) return;
  const chosen = licenseTypesById[Number(select.value)];
  const field = document.querySelector(`[data-license-other-detail-field="${select.dataset.licenseTypeSelect}"]`);
  field.hidden = !chosen?.is_other;
});

document.getElementById('timeline-list').addEventListener('click', async (event) => {
  const toggleButton = event.target.closest('button[data-toggle-edit-membership]');
  if (toggleButton) {
    const panel = document.querySelector(`[data-edit-membership-panel="${toggleButton.dataset.toggleEditMembership}"]`);
    panel.hidden = !panel.hidden;
    return;
  }

  const toggleAddLeaveButton = event.target.closest('button[data-toggle-add-leave]');
  if (toggleAddLeaveButton) {
    const panel = document.querySelector(`[data-add-leave-panel="${toggleAddLeaveButton.dataset.toggleAddLeave}"]`);
    panel.hidden = !panel.hidden;
    return;
  }

  const saveNewLeaveButton = event.target.closest('button[data-save-new-leave]');
  if (saveNewLeaveButton) {
    const membershipId = saveNewLeaveButton.dataset.saveNewLeave;
    const panel = document.querySelector(`[data-add-leave-panel="${membershipId}"]`);
    const field = (name) => panel.querySelector(`[data-new-leave-field="${name}"]`).value;
    const startDate = field('start_date');
    const endDate = field('end_date');
    if (!startDate || !endDate) {
      showError('Indica la fecha de inicio y de fin de la baja temporal.');
      return;
    }

    saveNewLeaveButton.disabled = true;
    try {
      await apiFetch(`/memberships/${membershipId}/leaves`, {
        method: 'POST',
        body: JSON.stringify({ start_date: startDate, end_date: endDate, reason: field('reason').trim() || null }),
      });
      showFeedback('Baja temporal registrada.');
      await loadTimeline();
    } catch (err) {
      saveNewLeaveButton.disabled = false;
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

  const toggleEditLicenseButton = event.target.closest('button[data-toggle-edit-license]');
  if (toggleEditLicenseButton) {
    const panel = document.querySelector(`[data-edit-license-panel="${toggleEditLicenseButton.dataset.toggleEditLicense}"]`);
    panel.hidden = !panel.hidden;
    return;
  }

  const saveLicenseButton = event.target.closest('button[data-save-license]');
  if (saveLicenseButton) {
    const licenseId = saveLicenseButton.dataset.saveLicense;
    const editPanel = document.querySelector(`[data-edit-license-panel="${licenseId}"]`);
    const field = (name) => editPanel.querySelector(`[data-license-field="${name}"]`).value;
    const licenseTypeId = Number(field('license_type_id'));
    const chosen = licenseTypesById[licenseTypeId];
    const otherDetail = field('other_detail').trim() || null;
    if (chosen?.is_other && !otherDetail) {
      showError('Indica de qué licencia se trata.');
      return;
    }
    const body = {
      license_type_id: licenseTypeId,
      other_detail: chosen?.is_other ? otherDetail : null,
      issued_date: field('issued_date'),
    };

    saveLicenseButton.disabled = true;
    try {
      await apiFetch(`/licenses/${licenseId}`, { method: 'PATCH', body: JSON.stringify(body) });
      showFeedback('Licencia actualizada.');
      await loadTimeline();
    } catch (err) {
      saveLicenseButton.disabled = false;
      showError(err.message);
    }
    return;
  }

  const deleteLicenseButton = event.target.closest('button[data-delete-license]');
  if (deleteLicenseButton) {
    if (!confirm('¿Borrar esta licencia?')) return;
    deleteLicenseButton.disabled = true;
    try {
      await apiFetch(`/licenses/${deleteLicenseButton.dataset.deleteLicense}`, { method: 'DELETE' });
      await loadTimeline();
    } catch (err) {
      deleteLicenseButton.disabled = false;
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
    return;
  }

  const toggleEditBoardButton = event.target.closest('button[data-toggle-edit-board]');
  if (toggleEditBoardButton) {
    const panel = document.querySelector(`[data-edit-board-panel="${toggleEditBoardButton.dataset.toggleEditBoard}"]`);
    panel.hidden = !panel.hidden;
    return;
  }

  const saveBoardButton = event.target.closest('button[data-save-board]');
  if (saveBoardButton) {
    const boardMemberId = saveBoardButton.dataset.saveBoard;
    const panel = document.querySelector(`[data-edit-board-panel="${boardMemberId}"]`);
    const field = (name) => panel.querySelector(`[data-board-field="${name}"]`).value;

    const body = {
      position: field('position'),
      start_date: field('start_date'),
      end_date: field('end_date') || null,
    };

    saveBoardButton.disabled = true;
    try {
      await apiFetch(`/board-members/${boardMemberId}`, { method: 'PATCH', body: JSON.stringify(body) });
      showFeedback('Cargo actualizado.');
      await loadTimeline();
      await loadMember();
    } catch (err) {
      saveBoardButton.disabled = false;
      showError(err.message);
    }
  }
});

// Impersonating yourself is rejected by the API anyway, but there's no
// reason to even offer it on your own page.
const impersonateButton = document.getElementById('impersonate-button');
if (impersonateButton) {
  if (memberLogin === viewer.login) {
    impersonateButton.remove();
  } else {
    impersonateButton.addEventListener('click', async () => {
      if (!confirm('¿Entrar en la intranet como esta persona? Podrás salir de la impersonación en cualquier momento.')) return;
      impersonateButton.disabled = true;
      try {
        const result = await apiFetch(`/users/${memberLogin}/impersonate`, { method: 'POST' });
        setToken(result.token);
        window.location.href = '/';
      } catch (err) {
        impersonateButton.disabled = false;
        showError(err.message);
      }
    });
  }
}

const activityTypeLabels = { caving: 'Espeleología', canyoning: 'Cañones', training: 'Formación', social: 'Social', other: 'Otro' };
const activityStatusLabels = { planned: 'Planificada', completed: 'Realizada', cancelled: 'Cancelada' };

function renderActivityRow(activity) {
  const role = activity.leader_login === memberLogin ? 'Organizador' : 'Participante';
  return `
    <tr>
      <td>${formatDate(activity.start_date)}</td>
      <td>${formatDate(activity.end_date)}</td>
      <td><a href="/activity?id=${activity.id}">${escapeHtml(activity.title)}</a></td>
      <td>${activityTypeLabels[activity.type] ?? activity.type}</td>
      <td><span class="badge status-${activity.status}">${activityStatusLabels[activity.status] ?? activity.status}</span></td>
      <td>${role}</td>
    </tr>
  `;
}

async function loadActivities() {
  const tableBody = document.getElementById('activities-table-body');
  try {
    const activities = await apiFetch(`/users/${memberLogin}/activities`);
    tableBody.innerHTML = activities.length
      ? activities.map(renderActivityRow).join('')
      : '<tr><td colspan="6">Todavía no ha participado en ninguna actividad.</td></tr>';
  } catch {
    tableBody.innerHTML = '<tr><td colspan="6">No se pudo cargar el historial de actividades.</td></tr>';
  }
}

// Only loan/rental/sale ever have a recipient, so this person's own
// movement history (GET /users/{login}/movements) never contains an
// acquisition, loss, abandonment or retirement — no labels needed here
// for those.
const movementTypeLabels = { loan: 'Préstamo', rental: 'Alquiler', sale: 'Venta', return: 'Devolución' };
const movementStatusLabels = { active: 'Activo', completed: 'Completado', overdue: 'Vencido', cancelled: 'Cancelado' };
const ropeTypeLabels = { caving: 'Espeleología', canyoning: 'Cañones' };
const bagTypeLabels = { caving: 'Espeleología', canyoning: 'Cañones', quick: 'Rápida', special: 'Especial' };

// A rope/bag is identified by brand+model (plus its type-specific
// attributes); gear may have no known brand/model at all, so its
// category — always set — anchors the label instead.
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

// Only a rental ever carries a returned_at — a loan's own "returned"
// state is the sum of separate 'return' movements (visible as their
// own rows in this same history) against the same item and activity,
// not a flag on the loan itself.
function returnedText(m) {
  return m.returned_at ? `Devuelto el ${formatDate(m.returned_at)}` : '—';
}

function renderMovementRow(m, itemsById) {
  const item = itemsById[m.item_id];
  const returned = returnedText(m);
  return `
    <tr>
      <td>${item ? itemLabel(item) : `Material n.º ${m.item_id}`}</td>
      <td>${movementTypeLabels[m.type] ?? m.type}</td>
      <td>${m.quantity}</td>
      <td>${formatDate(m.transacted_at)}</td>
      <td>${returned}</td>
      <td><span class="badge">${movementStatusLabels[m.status] ?? m.status}</span></td>
    </tr>
  `;
}

async function loadMovements() {
  const tableBody = document.getElementById('movements-table-body');
  try {
    const [items, movements] = await Promise.all([
      apiFetch('/items').catch(() => []),
      apiFetch(`/users/${memberLogin}/movements`),
    ]);
    const itemsById = Object.fromEntries(items.map((item) => [item.id, item]));
    tableBody.innerHTML = movements.length
      ? movements.map((m) => renderMovementRow(m, itemsById)).join('')
      : '<tr><td colspan="6">Todavía no tiene movimientos de material.</td></tr>';
  } catch {
    tableBody.innerHTML = '<tr><td colspan="6">No se pudo cargar el historial de material.</td></tr>';
  }
}

// A federative license shows up as just another event on the same
// altas-y-bajas timeline (see buildTimelineEvents) rather than in a
// section of its own. Both this and the board-term block below have
// data-roles="admin" on their buttons/panels — POST licenses/
// board-members is admin-only now, so board never has these elements.
if (isRealAdmin) {
  document.getElementById('add-license').addEventListener('click', () => {
    const panel = document.getElementById('add-license-panel');
    const dateInput = document.getElementById('add-license-issued-date');
    if (panel.hidden && !dateInput.value) {
      dateInput.value = new Date().toISOString().slice(0, 10);
    }
    panel.hidden = !panel.hidden;
  });

  document.getElementById('add-license-type').addEventListener('change', (event) => {
    const chosen = licenseTypesById[Number(event.target.value)];
    document.getElementById('add-license-other-detail-field').hidden = !chosen?.is_other;
  });

  document.getElementById('save-add-license').addEventListener('click', async () => {
    const typeSelect = document.getElementById('add-license-type');
    const otherDetailInput = document.getElementById('add-license-other-detail');
    const dateInput = document.getElementById('add-license-issued-date');
    const licenseTypeId = Number(typeSelect.value);
    const chosen = licenseTypesById[licenseTypeId];
    if (!licenseTypeId || !dateInput.value) {
      showError('Indica el tipo de licencia y la fecha de obtención.');
      return;
    }
    const otherDetail = otherDetailInput.value.trim() || null;
    if (chosen?.is_other && !otherDetail) {
      showError('Indica de qué licencia se trata.');
      return;
    }

    const button = document.getElementById('save-add-license');
    button.disabled = true;
    try {
      await apiFetch(`/users/${memberLogin}/licenses`, {
        method: 'POST',
        body: JSON.stringify({
          license_type_id: licenseTypeId,
          other_detail: chosen?.is_other ? otherDetail : null,
          issued_date: dateInput.value,
        }),
      });
      showFeedback('Licencia registrada.');
      document.getElementById('add-license-panel').hidden = true;
      otherDetailInput.value = '';
      document.getElementById('add-license-other-detail-field').hidden = true;
      dateInput.value = '';
      await loadTimeline();
    } catch (err) {
      showError(err.message);
    } finally {
      button.disabled = false;
    }
  });

  // A board term is also just another event on the same timeline, and
  // holding one is what the automatic "board" role is computed from
  // (see docs/architecture.md) — there's no separate way to grant it.
  document.getElementById('add-board-term').addEventListener('click', () => {
    const panel = document.getElementById('add-board-panel');
    const dateInput = document.getElementById('add-board-start-date');
    if (panel.hidden && !dateInput.value) {
      dateInput.value = new Date().toISOString().slice(0, 10);
    }
    panel.hidden = !panel.hidden;
  });

  document.getElementById('save-add-board-term').addEventListener('click', async () => {
    const positionSelect = document.getElementById('add-board-position');
    const dateInput = document.getElementById('add-board-start-date');
    if (!dateInput.value) {
      showError('Indica la fecha de inicio en la Junta Directiva.');
      return;
    }

    const button = document.getElementById('save-add-board-term');
    button.disabled = true;
    try {
      await apiFetch(`/users/${memberLogin}/board-members`, {
        method: 'POST',
        body: JSON.stringify({ position: positionSelect.value, start_date: dateInput.value }),
      });
      showFeedback('Cargo registrado.');
      document.getElementById('add-board-panel').hidden = true;
      dateInput.value = '';
      await loadTimeline();
      await loadMember();
    } catch (err) {
      showError(err.message);
    } finally {
      button.disabled = false;
    }
  });
}

await loadRolesCatalog();
await loadLicenseTypesCatalog();
loadMember();
loadTimeline();
loadActivities();
loadMovements();
