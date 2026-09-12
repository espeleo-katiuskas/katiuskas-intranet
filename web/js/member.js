import {
  hasAnyRole,
  apiFetch,
  escapeHtml,
  formatDate,
  setToken,
  knownRoleLabels,
  roleLabel,
  positionLabels,
  typeLabels,
  statusLabels,
  showFeedback,
  showError,
} from './api.js';
import { initPage } from './layout.js';
import { initModal } from './modal.js';
import { initUserFilesBrowser } from './userFiles.js';

const viewer = await initPage({ requireAnyRole: ['admin', 'board'] });
const isAdmin = hasAnyRole(viewer, ['admin', 'board']);
const isRealAdmin = hasAnyRole(viewer, ['admin']);

const memberLogin = new URLSearchParams(window.location.search).get('login');
if (!memberLogin) {
  window.location.href = '/members';
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

// Loaded once (admin-only endpoint) so the edit-license-dialog's own
// type <select> can be built from it — a new license is no longer
// created from this page at all (see account.js), only corrected here.
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

const editMemberDialog = document.getElementById('edit-member-dialog');
const editForm = document.getElementById('edit-member-form');
// Deliberately not initModal(editMemberDialog, editForm) — unlike every
// other dialog on this page, this one is a correction to an existing
// record, always freshly populated by loadMember() rather than starting
// blank each time, so resetting it back to blank defaults on close (the
// behavior every *other* dialog here wants) would be wrong for this one.
initModal(editMemberDialog);
document.getElementById('open-edit-member').addEventListener('click', () => editMemberDialog.showModal());

// Board can still see every field here (loadMember populates them
// regardless), but PATCH /users/{login} now only accepts a self-edit, and
// board is never "self" on someone else's page — so there's nothing a
// submit could do here but fail. Disabling the whole form instead of
// just hiding the submit button keeps the fields visibly read-only
// rather than looking editable and then erroring out — the toggle
// button itself is relabelled to match ("Ver datos", not "Editar
// datos"), since board can open this dialog too, just not submit it.
if (isAdmin && !isRealAdmin) {
  for (const field of editForm.elements) {
    field.disabled = true;
  }
  document.getElementById('board-readonly-hint').hidden = false;
  document.getElementById('open-edit-member').textContent = 'Ver datos';
} else {
  document.getElementById('open-edit-member').textContent = 'Editar datos';
}

async function loadMember() {
  try {
    const person = await apiFetch(`/users/${memberLogin}`);
    document.getElementById('member-name').textContent = `${person.first_name} ${person.last_name}`;
    document.getElementById('member-email').textContent = person.email ?? '';
    document.getElementById('member-number').textContent = person.membership_number != null
      ? `N.º de socio: ${person.membership_number}`
      : 'Sin número de socio todavía.';
    document.getElementById('member-last-login').textContent = person.last_login_at
      ? `Último acceso: ${formatDate(person.last_login_at)}${person.last_login_ip ? ` desde ${person.last_login_ip}` : ''}`
      : 'Todavía no ha iniciado sesión.';
    renderRoles(person);

    editForm.first_name.value = person.first_name;
    editForm.last_name.value = person.last_name;
    editForm.email.value = person.email ?? '';
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
    editMemberDialog.close();
  } catch (err) {
    showError(err.message);
  } finally {
    submitButton.disabled = false;
  }
});

// Each timeline event's own "Editar" (or "+ Baja temporal") opens one
// of these shared dialogs rather than a per-event panel pre-rendered
// inline — populating it from the record looked up by id (see the
// *ById maps buildTimelineEvents fills in) instead of baking values
// into markup that would otherwise sit hidden in the DOM once per
// event, most of which nobody ever opens.
const editMembershipDialog = document.getElementById('edit-membership-dialog');
initModal(editMembershipDialog);
let editingMembershipId = null;

function openEditMembershipDialog(m) {
  editingMembershipId = m.id;
  document.getElementById('em-membership-start-date').value = m.start_date.slice(0, 10);
  document.getElementById('em-membership-end-date').value = m.end_date ? m.end_date.slice(0, 10) : '';
  editMembershipDialog.showModal();
}

// #leave-dialog is shared by "+ Baja temporal" (adding one, POST) and
// "Editar" on an existing one (PATCH) — same three fields either way,
// just a different title/submit target. leaveMode/leaveTargetId track
// which of the two a save is actually for.
const leaveDialog = document.getElementById('leave-dialog');
initModal(leaveDialog);
let leaveMode = null; // 'add' | 'edit'
let leaveTargetId = null; // a membership id (add) or a leave id (edit)

function openAddLeaveDialog(membershipId) {
  leaveMode = 'add';
  leaveTargetId = membershipId;
  document.getElementById('leave-dialog-title').textContent = 'Nueva baja temporal';
  document.getElementById('leave-start-date').value = '';
  document.getElementById('leave-end-date').value = '';
  document.getElementById('leave-reason').value = '';
  leaveDialog.showModal();
}

function openEditLeaveDialog(l) {
  leaveMode = 'edit';
  leaveTargetId = l.id;
  document.getElementById('leave-dialog-title').textContent = 'Editar baja temporal';
  document.getElementById('leave-start-date').value = l.start_date.slice(0, 10);
  document.getElementById('leave-end-date').value = l.end_date.slice(0, 10);
  document.getElementById('leave-reason').value = l.reason ?? '';
  leaveDialog.showModal();
}

const editBoardDialog = document.getElementById('edit-board-dialog');
initModal(editBoardDialog);
let editingBoardMemberId = null;

function openEditBoardDialog(b) {
  editingBoardMemberId = b.id;
  document.getElementById('eb-position').value = b.position;
  document.getElementById('eb-start-date').value = b.start_date.slice(0, 10);
  document.getElementById('eb-end-date').value = b.end_date ? b.end_date.slice(0, 10) : '';
  editBoardDialog.showModal();
}

const editLicenseDialog = document.getElementById('edit-license-dialog');
initModal(editLicenseDialog);
let editingLicenseId = null;

function openEditLicenseDialog(l) {
  editingLicenseId = l.id;
  const chosen = licenseTypesById[l.license_type_id];
  document.getElementById('el-license-type').innerHTML = licenseTypeOptions(l.license_type_id);
  document.getElementById('el-other-detail-field').hidden = !chosen?.is_other;
  document.getElementById('el-other-detail').value = l.other_detail ?? '';
  document.getElementById('el-issued-date').value = l.issued_date.slice(0, 10);
  document.getElementById('el-year').value = l.year;
  editLicenseDialog.showModal();
}

document.getElementById('el-license-type').addEventListener('change', (event) => {
  const chosen = licenseTypesById[Number(event.target.value)];
  document.getElementById('el-other-detail-field').hidden = !chosen?.is_other;
});

const eventLabels = {
  alta: 'Alta',
  baja: 'Baja',
  leave_start: 'Inicio de baja temporal',
  leave_end: 'Fin de baja temporal',
  license: 'Licencia federativa',
  board_start: 'Entra en la Junta Directiva',
  board_end: 'Sale de la Junta Directiva',
};

// Looked up by id when a timeline event's own button is clicked later
// (open*Dialog above take the record itself, not just its id) — filled
// in fresh by buildTimelineEvents on every reload, alongside the flat
// events list itself.
let membershipsById = {};
let leavesById = {};
let licensesById = {};
let boardTermsById = {};

// Builds one flat, chronological list of events out of every membership
// period, every temporary leave within it (a "baja temporal" always
// belongs to exactly one period — see docs/architecture.md), every
// federative license this person has ever been issued, and every board
// term they've served (those are scoped to the person directly, not to
// any one membership period, the same way licenses are).
async function buildTimelineEvents(memberships) {
  const events = [];
  membershipsById = Object.fromEntries(memberships.map((m) => [m.id, m]));
  leavesById = {};
  for (const m of memberships) {
    events.push({ date: m.start_date, type: 'alta', membership: m });
    if (m.end_date) {
      events.push({ date: m.end_date, type: 'baja', membership: m, reason: m.end_reason });
    }
    const leaves = await apiFetch(`/memberships/${m.id}/leaves`).catch(() => []);
    for (const l of leaves) {
      leavesById[l.id] = l;
      events.push({ date: l.start_date, type: 'leave_start', membership: m, leave: l, reason: l.reason });
      events.push({ date: l.end_date, type: 'leave_end', membership: m, leave: l, reason: l.reason });
    }
  }

  const licenses = await apiFetch(`/users/${memberLogin}/licenses`).catch(() => []);
  licensesById = Object.fromEntries(licenses.map((l) => [l.id, l]));
  for (const l of licenses) {
    events.push({ date: l.issued_date, type: 'license', license: l, reason: l.type });
  }

  const boardTerms = await apiFetch(`/users/${memberLogin}/board-members`).catch(() => []);
  boardTermsById = Object.fromEntries(boardTerms.map((b) => [b.id, b]));
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

  // These buttons are rendered fresh on every timeline reload, so
  // data-roles in static HTML can't reach them — gating admin-only
  // actions here is the only place it can happen.
  if (isRealAdmin && event.type === 'alta') {
    actions = `
      <button type="button" class="btn btn-secondary btn-small" data-edit-membership="${event.membership.id}">Editar</button>
      <button type="button" class="btn btn-secondary btn-small" data-add-leave="${event.membership.id}">+ Baja temporal</button>
    `;
  } else if (isRealAdmin && event.type === 'leave_start') {
    actions = `
      <button type="button" class="btn btn-secondary btn-small" data-edit-leave="${event.leave.id}">Editar</button>
      <button type="button" class="btn btn-danger btn-small" data-delete-leave="${event.leave.id}">Borrar</button>
    `;
  } else if (isRealAdmin && event.type === 'license') {
    actions = `
      <button type="button" class="btn btn-secondary btn-small" data-edit-license="${event.license.id}">Editar</button>
      <button type="button" class="btn btn-danger btn-small" data-delete-license="${event.license.id}">Borrar</button>
    `;
  } else if (isRealAdmin && event.type === 'board_start') {
    actions = `
      <button type="button" class="btn btn-secondary btn-small" data-edit-board="${event.boardMember.id}">Editar</button>
    `;
  }

  return `
    <div class="detail-row">
      <span>${formatDate(event.date)} — ${eventLabels[event.type]}${reasonText}</span>
      <span>${actions}</span>
    </div>
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
const addMembershipDialog = document.getElementById('add-membership-dialog');
initModal(addMembershipDialog);
// No <form> here to reset for free on close (see initModal) — a plain
// panel, like the original always was — so this clears its one field
// by hand, on every close (cancel, ×, Escape, or after a successful
// save), not just after saving the way the pre-modal version did.
addMembershipDialog.addEventListener('close', () => {
  document.getElementById('add-membership-start-date').value = '';
});

if (isRealAdmin) {
  document.getElementById('start-membership').addEventListener('click', () => {
    const input = document.getElementById('add-membership-start-date');
    if (!input.value) {
      input.value = new Date().toISOString().slice(0, 10);
    }
    addMembershipDialog.showModal();
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
      addMembershipDialog.close();
      await loadTimeline();
      await loadMember();
    } catch (err) {
      showError(err.message);
    } finally {
      button.disabled = false;
    }
  });
}

document.getElementById('save-edit-membership').addEventListener('click', async () => {
  const body = {
    start_date: document.getElementById('em-membership-start-date').value,
    end_date: document.getElementById('em-membership-end-date').value || null,
  };
  const button = document.getElementById('save-edit-membership');
  button.disabled = true;
  try {
    await apiFetch(`/memberships/${editingMembershipId}`, { method: 'PATCH', body: JSON.stringify(body) });
    showFeedback('Periodo actualizado.');
    editMembershipDialog.close();
    await loadTimeline();
    await loadMember();
  } catch (err) {
    showError(err.message);
  } finally {
    button.disabled = false;
  }
});

document.getElementById('save-leave').addEventListener('click', async () => {
  const startDate = document.getElementById('leave-start-date').value;
  const endDate = document.getElementById('leave-end-date').value;
  if (!startDate || !endDate) {
    showError('Indica la fecha de inicio y de fin de la baja temporal.');
    return;
  }
  const reason = document.getElementById('leave-reason').value.trim() || null;
  const button = document.getElementById('save-leave');
  button.disabled = true;
  try {
    if (leaveMode === 'add') {
      await apiFetch(`/memberships/${leaveTargetId}/leaves`, {
        method: 'POST',
        body: JSON.stringify({ start_date: startDate, end_date: endDate, reason }),
      });
      showFeedback('Baja temporal registrada.');
    } else {
      await apiFetch(`/leaves/${leaveTargetId}`, {
        method: 'PATCH',
        body: JSON.stringify({ start_date: startDate, end_date: endDate, reason }),
      });
      showFeedback('Baja temporal actualizada.');
    }
    leaveDialog.close();
    await loadTimeline();
  } catch (err) {
    showError(err.message);
  } finally {
    button.disabled = false;
  }
});

document.getElementById('save-edit-board').addEventListener('click', async () => {
  const body = {
    position: document.getElementById('eb-position').value,
    start_date: document.getElementById('eb-start-date').value,
    end_date: document.getElementById('eb-end-date').value || null,
  };
  const button = document.getElementById('save-edit-board');
  button.disabled = true;
  try {
    await apiFetch(`/board-members/${editingBoardMemberId}`, { method: 'PATCH', body: JSON.stringify(body) });
    showFeedback('Cargo actualizado.');
    editBoardDialog.close();
    await loadTimeline();
    await loadMember();
  } catch (err) {
    showError(err.message);
  } finally {
    button.disabled = false;
  }
});

document.getElementById('save-edit-license').addEventListener('click', async () => {
  const licenseTypeId = Number(document.getElementById('el-license-type').value);
  const chosen = licenseTypesById[licenseTypeId];
  const otherDetail = document.getElementById('el-other-detail').value.trim() || null;
  if (chosen?.is_other && !otherDetail) {
    showError('Indica de qué licencia se trata.');
    return;
  }
  const body = {
    license_type_id: licenseTypeId,
    other_detail: chosen?.is_other ? otherDetail : null,
    issued_date: document.getElementById('el-issued-date').value,
    year: Number(document.getElementById('el-year').value),
  };
  const button = document.getElementById('save-edit-license');
  button.disabled = true;
  try {
    await apiFetch(`/licenses/${editingLicenseId}`, { method: 'PATCH', body: JSON.stringify(body) });
    showFeedback('Licencia actualizada.');
    editLicenseDialog.close();
    await loadTimeline();
  } catch (err) {
    showError(err.message);
  } finally {
    button.disabled = false;
  }
});

document.getElementById('timeline-list').addEventListener('click', async (event) => {
  const editMembershipButton = event.target.closest('button[data-edit-membership]');
  if (editMembershipButton) {
    openEditMembershipDialog(membershipsById[Number(editMembershipButton.dataset.editMembership)]);
    return;
  }

  const addLeaveButton = event.target.closest('button[data-add-leave]');
  if (addLeaveButton) {
    openAddLeaveDialog(Number(addLeaveButton.dataset.addLeave));
    return;
  }

  const editLeaveButton = event.target.closest('button[data-edit-leave]');
  if (editLeaveButton) {
    openEditLeaveDialog(leavesById[Number(editLeaveButton.dataset.editLeave)]);
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

  const editLicenseButton = event.target.closest('button[data-edit-license]');
  if (editLicenseButton) {
    openEditLicenseDialog(licensesById[Number(editLicenseButton.dataset.editLicense)]);
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

  const editBoardButton = event.target.closest('button[data-edit-board]');
  if (editBoardButton) {
    openEditBoardDialog(boardTermsById[Number(editBoardButton.dataset.editBoard)]);
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

function renderActivityRow(activity) {
  const role = activity.leader_login === memberLogin ? 'Organizador' : 'Participante';
  return `
    <tr>
      <td>${formatDate(activity.start_date)}</td>
      <td>${formatDate(activity.end_date)}</td>
      <td><a href="/activity?id=${activity.id}">${escapeHtml(activity.title)}</a></td>
      <td>${typeLabels[activity.type] ?? activity.type}</td>
      <td><span class="badge status-${activity.status}">${statusLabels[activity.status] ?? activity.status}</span></td>
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

// Board-term creation shows up as just another event on the same
// altas-y-bajas timeline (see buildTimelineEvents). It has
// data-roles="admin" on its own button/panel — POST board-members is
// admin-only now, so board never has these elements. A federative
// license's own creation isn't here at all: it now always happens from
// the transaction that pays for it (see account.js), never from this
// page directly.
if (isRealAdmin) {
  // A board term is also just another event on the same timeline, and
  // holding one is what the automatic "board" role is computed from
  // (see docs/architecture.md) — there's no separate way to grant it.
  const addBoardDialog = document.getElementById('add-board-dialog');
  initModal(addBoardDialog);
  // No <form> here (see the add-membership-dialog comment above for why).
  addBoardDialog.addEventListener('close', () => {
    document.getElementById('add-board-position').selectedIndex = 0;
    document.getElementById('add-board-start-date').value = '';
  });

  document.getElementById('add-board-term').addEventListener('click', () => {
    const dateInput = document.getElementById('add-board-start-date');
    if (!dateInput.value) {
      dateInput.value = new Date().toISOString().slice(0, 10);
    }
    addBoardDialog.showModal();
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
      addBoardDialog.close();
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
initUserFilesBrowser(memberLogin);
