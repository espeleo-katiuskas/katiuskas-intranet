import {
  hasAnyRole,
  apiFetch,
  escapeHtml,
  formatDate,
  startImpersonation,
  roleLabel,
  positionLabels,
  typeLabels,
  activityState,
  activityStateLabels,
  showFeedback,
  showError,
} from './api.js';
import { initPage } from './layout.js';
import { initModal, openModal } from './modal.js';
import { initSessionsPanel } from './sessionsPanel.js';
import { initUserFilesBrowser } from './userFiles.js';
import { initLicenseFiles } from './licenseFiles.js';
import { trackDirtyFields, markFieldsClean } from './formDirty.js';
import { loadTimelineData, renderTimelineRow } from './timeline.js';

const viewer = await initPage({ requireAnyRole: ['admin', 'board'] });
const isRealAdmin = hasAnyRole(viewer, ['admin']);

const memberLogin = new URLSearchParams(window.location.search).get('login');
if (!memberLogin) {
  window.location.href = '/socios';
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
    .map(
      (t) =>
        `<option value="${t.id}" ${t.id === selectedId ? 'selected' : ''}>${escapeHtml(t.name)}</option>`,
    )
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
      // The API itself refuses this the same way regardless (409
      // self_admin_removal — see docs/architecture.md's Roles section):
      // someone else has to be the one to take your own admin away.
      // Never rendering the "×" for it here just means the viewer never
      // sees that request fail in the first place.
      const isOwnAdminRole = name === 'admin' && memberLogin === viewer.login;
      if (!canManageRole(name) || isOwnAdminRole) {
        const title = isOwnAdminRole
          ? ' title="No puedes quitarte a ti mismo el rol de admin"'
          : '';
        return `<span class="badge"${title}>${label}</span>`;
      }
      const roleId = rolesByName[name]?.id;
      return `<span class="badge-removable">${label}<button type="button" data-remove-role="${roleId}" title="Quitar rol">×</button></span>`;
    })
    .join('');
}

function renderRoleAddControl(person) {
  const assignable = allRoles.filter(
    (role) => !person.roles.includes(role.name) && canManageRole(role.name),
  );
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

// Somebody with none of these roles cannot sign in, so they cannot be impersonated either (the API
// answers 409 not_eligible): the button is not offered. It follows the roles, which change on this page.
const BASELINE_ROLES = ['admin', 'board', 'member'];

function renderRoles(person) {
  document.getElementById('member-roles-list').innerHTML = renderRoleBadges(person);
  document.getElementById('member-role-add').innerHTML = renderRoleAddControl(person);
  const impersonate = document.getElementById('impersonate-button');
  if (impersonate) impersonate.hidden = !person.roles.some((r) => BASELINE_ROLES.includes(r));
}

document.getElementById('member-roles-list').addEventListener('click', async (event) => {
  const button = event.target.closest('button[data-remove-role]');
  if (!button) return;
  button.disabled = true;
  try {
    await apiFetch(`/users/${memberLogin}/roles/${button.dataset.removeRole}`, {
      method: 'DELETE',
    });
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
    await apiFetch(`/users/${memberLogin}/roles`, {
      method: 'POST',
      body: JSON.stringify({ role_id: Number(roleId) }),
    });
    showFeedback('Rol añadido.');
    await loadMember();
  } catch (err) {
    button.disabled = false;
    showError(err.message);
  }
});

const editForm = document.getElementById('edit-member-form');

// Datos is a plain inline form now (the same shape profile.js's own "Mis
// datos" already uses), not a modal — board can already see every field
// directly on the page this way, so there's no separate read-only view to
// keep in sync with it; a disabled field still shows its value, just not
// editable. Only admin can submit at all here (the whole record, not just
// the contact fields a plain member can self-edit on /perfil), so every
// field is disabled for board, not just the identity-ish ones profile.js
// itself restricts.
if (!isRealAdmin) {
  for (const field of editForm.elements) {
    field.disabled = true;
  }
  document.getElementById('board-readonly-hint').hidden = false;
}

// The last record loadMember() fetched, so "Cancelar" can restore the form
// to it without a round trip — reverting unsaved edits is "put back what
// was already loaded", not "re-fetch the latest from the server".
let lastLoadedPerson = null;

trackDirtyFields(editForm);

// Chrome's address-autofill heuristic recognizes this field cluster
// (street/postal code/city/province) by name/id/label, independent of
// autocomplete="off" — Chrome deliberately ignores that attribute for
// address/payment fields specifically, precisely to stop this kind of
// page-author workaround, so it alone didn't stop the "save this
// address?" prompt in practice. A field Chrome's autofill excludes
// outright — readonly, unlike disabled, still submits its value
// normally — never looks like a real address form to it, so these start
// readonly and only become editable while actually focused, reverting
// the instant focus leaves (a click on any other element, including
// "Entrar como esta persona" below, blurs the field — and does so
// synchronously, before that click's own handler runs — so even an
// edit abandoned mid-typing is already readonly again by the time
// anything else on the page reacts to the click).
const ADDRESS_CLUSTER_FIELDS = ['address', 'postal_code', 'city', 'province'];
for (const name of ADDRESS_CLUSTER_FIELDS) {
  const field = editForm.elements[name];
  field.addEventListener('focus', () => {
    field.readOnly = false;
  });
  field.addEventListener('blur', () => {
    field.readOnly = true;
  });
}

function populateEditForm(person) {
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
  for (const name of ADDRESS_CLUSTER_FIELDS) {
    editForm.elements[name].readOnly = true;
  }
  markFieldsClean(editForm);
}

// Chrome can offer to save this address the instant the page is left —
// a link click, browser back/forward, or this page's own impersonate
// redirect below — just from noticing a filled address-shaped form in
// the DOM at that moment, even though it's never actually submitted
// (autocomplete="off" on every field here, on its own, didn't stop
// this in practice). Clearing the form right before unload is the
// backstop: nothing address-shaped left for Chrome to notice. A
// beforeunload listener also opts this page out of the back/forward
// cache, so a real back-navigation always re-runs this script and
// refetches fresh data rather than restoring a JS-preserved page with
// these fields already blanked out.
window.addEventListener('beforeunload', () => editForm.reset());

// #cancel-edit-member lives inside the same data-roles="admin" panel-actions
// as the submit button, so it's removed from the DOM entirely for board —
// nothing to wire up then (see the identical reasoning this page already
// hit once for #open-edit-member, before Datos became this inline form).
if (isRealAdmin) {
  document.getElementById('cancel-edit-member').addEventListener('click', () => {
    if (lastLoadedPerson) {
      populateEditForm(lastLoadedPerson);
    }
  });
}

// `ready` is what drawing needs besides the person (the role catalog): the
// requests all go out at once and only the drawing waits for it.
async function loadMember(ready = Promise.resolve()) {
  try {
    const person = await apiFetch(`/users/${memberLogin}`);
    await ready;
    lastLoadedPerson = person;
    document.getElementById('member-name').textContent = `${person.first_name} ${person.last_name}`;
    document.getElementById('member-email').textContent = person.email ?? '';
    document.getElementById('member-number').textContent =
      person.membership_number != null
        ? `N.º de socio: ${person.membership_number}`
        : 'Sin número de socio todavía.';
    document.getElementById('member-last-login').textContent = person.last_login_at
      ? `Último acceso: ${formatDate(person.last_login_at)}${person.last_login_ip ? ` desde ${person.last_login_ip}` : ''}`
      : 'Todavía no ha iniciado sesión.';
    renderRoles(person);
    populateEditForm(person);
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
    // Re-fetch rather than trusting `body` directly — the same pattern
    // every other mutation on this page already follows — so the form
    // reflects exactly what the server actually saved.
    await loadMember();
  } catch (err) {
    showError(err.message);
  } finally {
    submitButton.disabled = false;
  }
});

// today() is the shared upper bound for every alta/baja/baja-temporal
// date this page ever asks for — none of them can be backdated into
// the future.
function today() {
  return new Date().toISOString().slice(0, 10);
}

// #status-dialog is the one shared "pick a single date" dialog behind
// every alta/baja/baja-temporal action on this page — both starting a
// brand-new one (see membership-status-actions below) and correcting a
// single field of an existing entry (see renderTimelineEvent): a
// membership row and a leave row each carry two dates of their own
// (start/end), but every action here only ever touches one of them at
// a time, never both together, so a single date field is always
// enough. openStatusDialog takes the bounds and the save action to
// perform; validateStatusDate enforces those bounds before onSave ever
// runs (the server enforces the real invariants regardless — this is
// just so a bad date is caught before the round trip).
const statusDialog = document.getElementById('status-dialog');
initModal(statusDialog);
let statusDialogBounds = {};
let statusDialogOnSave = null;
let statusDialogSuccessMessage = '';

function openStatusDialog({
  title,
  hint,
  initial,
  minExclusive,
  minLabel,
  maxInclusive,
  onSave,
  successMessage,
}) {
  document.getElementById('status-dialog-title').textContent = title;
  const hintEl = document.getElementById('status-dialog-hint');
  hintEl.textContent = hint ?? '';
  hintEl.hidden = !hint;
  document.getElementById('status-dialog-date').value = initial ?? today();
  statusDialogBounds = { minExclusive, minLabel, maxInclusive };
  statusDialogOnSave = onSave;
  statusDialogSuccessMessage = successMessage;
  openModal(statusDialog);
}

// minLabel is the whole "a/al ..." phrase (not just the bare noun), so
// the Spanish contraction ("al alta", not "a el alta") is each call
// site's own responsibility rather than something this template could
// get right generically for every one of them.
function validateStatusDate(value, { minExclusive, minLabel, maxInclusive }) {
  if (!value) return 'Indica una fecha.';
  if (maxInclusive && value > maxInclusive) return 'La fecha no puede ser posterior a hoy.';
  if (minExclusive && value <= minExclusive) {
    return `La fecha debe ser posterior ${minLabel ?? 'a la fecha anterior'} (${formatDate(minExclusive)}).`;
  }
  return null;
}

document.getElementById('save-status-dialog').addEventListener('click', async () => {
  const value = document.getElementById('status-dialog-date').value;
  const validationError = validateStatusDate(value, statusDialogBounds);
  if (validationError) {
    showError(validationError);
    return;
  }
  const button = document.getElementById('save-status-dialog');
  button.disabled = true;
  try {
    await statusDialogOnSave(value);
    showFeedback(statusDialogSuccessMessage);
    statusDialog.close();
    await loadTimeline();
    await loadMember();
  } catch (err) {
    showError(err.message);
  } finally {
    button.disabled = false;
  }
});

const editBoardDialog = document.getElementById('edit-board-dialog');
initModal(editBoardDialog);
let editingBoardMemberId = null;

function openEditBoardDialog(b) {
  editingBoardMemberId = b.id;
  document.getElementById('eb-position').value = b.position;
  document.getElementById('eb-start-date').value = b.start_date.slice(0, 10);
  document.getElementById('eb-end-date').value = b.end_date ? b.end_date.slice(0, 10) : '';
  openModal(editBoardDialog);
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
  openModal(editLicenseDialog);
}

document.getElementById('el-license-type').addEventListener('change', (event) => {
  const chosen = licenseTypesById[Number(event.target.value)];
  document.getElementById('el-other-detail-field').hidden = !chosen?.is_other;
});

// "+ Licencia sin pago": a license not tied to any transaction, the only
// way to record one the club didn't collect (someone federated
// elsewhere) — POST /users/{login}/licenses without a transaction_id.
// The ones the club does collect are registered from their transaction,
// in Contabilidad. The button lives in the admin-only panel below, so
// it doesn't exist for anyone else.
const addLicenseDialog = document.getElementById('add-license-dialog');
initModal(addLicenseDialog);
if (isRealAdmin) {
  document.getElementById('add-license-button').addEventListener('click', () => {
    document.getElementById('al-license-type').innerHTML = licenseTypeOptions(
      allLicenseTypes[0]?.id,
    );
    document.getElementById('al-other-detail-field').hidden =
      !licenseTypesById[allLicenseTypes[0]?.id]?.is_other;
    document.getElementById('al-other-detail').value = '';
    document.getElementById('al-issued-date').value = today();
    document.getElementById('al-year').value = new Date().getFullYear();
    openModal(addLicenseDialog);
    document.getElementById('al-license-type').focus();
  });

  document.getElementById('al-license-type').addEventListener('change', (event) => {
    document.getElementById('al-other-detail-field').hidden =
      !licenseTypesById[Number(event.target.value)]?.is_other;
  });

  document.getElementById('save-add-license').addEventListener('click', async () => {
    const licenseTypeId = Number(document.getElementById('al-license-type').value);
    const chosen = licenseTypesById[licenseTypeId];
    const otherDetail = document.getElementById('al-other-detail').value.trim() || null;
    if (chosen?.is_other && !otherDetail) {
      showError('Indica de qué licencia se trata.');
      return;
    }
    const issuedDate = document.getElementById('al-issued-date').value;
    const year = Number(document.getElementById('al-year').value);
    if (!issuedDate || !year) {
      showError('Indica la fecha de obtención y el año que cubre.');
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
          issued_date: issuedDate,
          year,
        }),
      });
      showFeedback('Licencia añadida.');
      addLicenseDialog.close();
      await loadTimeline();
      await loadMember();
    } catch (err) {
      showError(err.message);
    } finally {
      button.disabled = false;
    }
  });
}

// Looked up by id when a timeline event's own button is clicked later
// (open*Dialog above take the record itself, not just its id) — filled
// in fresh by buildTimelineEvents on every reload, alongside the flat
// events list itself.
let membershipsById = {};
let leavesById = {};
let licensesById = {};
let boardTermsById = {};

// The person's current alta/baja/baja-temporal status, recomputed by
// buildTimelineEvents on every reload — drives which 0-2 buttons
// renderMembershipStatusActions offers (see "Historial como socio"
// below). openMembership is the one currently-open period, if any
// (there can only ever be one — memberships' own EXCLUDE constraint
// forbids two open periods overlapping into the infinite future at
// once); openLeave is the one currently-open temporary leave, if any
// (an open-ended leave only ever fits within a period that's itself
// still open, so it's always openMembership's own, never a past
// period's); lastMembership is the single most recent period on file
// regardless of open/closed (GET /users/{login}/memberships already
// comes back newest-first), used to find the last baja's own date when
// there's no open period at all.
let membershipStatus = { openMembership: null, openLeave: null, lastMembership: null };

// This person's own currently-open board term, if any — drives
// renderBoardStatusActions' own "+ Junta directiva"/"Deja el cargo de
// X" toggle the same way membershipStatus drives
// renderMembershipStatusActions.
let currentBoardTerm = null;

// Which single event — among only the "cambios de estado" (alta, baja,
// inicio/fin de baja temporal), not licenses or board terms, which
// have their own independent delete affordance already — is the most
// recent one overall. Only that one ever gets a "Borrar" button (see
// renderTimelineEvent): undoing history further back than the single
// most recent action isn't offered, only correcting it (Editar).
let lastStatusEventKey = null;

// Fetches everything the timeline is built from and keeps the lookups the
// action buttons need (see js/timeline.js for the events themselves).
async function buildTimelineEvents(memberships) {
  const data = await loadTimelineData(memberLogin, memberships);
  ({
    membershipsById,
    leavesById,
    licensesById,
    boardTermsById,
    currentBoardTerm,
    membershipStatus,
    lastStatusEventKey,
  } = data);
  return data.events;
}

function renderTimelineEvent(event) {
  const isLastStatusEvent = event.statusKey && event.statusKey === lastStatusEventKey;
  let actions = '';

  // These buttons are rendered fresh on every timeline reload, so
  // data-roles in static HTML can't reach them — gating admin-only
  // actions here is the only place it can happen. Editing here only
  // ever corrects the one date this specific entry represents (start
  // for alta/leave_start, end for baja/leave_end) — never the other
  // date on the same underlying membership/leave row, and never the
  // associated alta/leave_start it's paired with. "Borrar" only ever
  // shows on the single most recent cambio de estado overall (see
  // lastStatusEventKey) — deleting a "closing" entry (baja, leave_end)
  // just undoes the close (clears that date back to open, via the same
  // PATCH endpoint editing already uses); deleting an "opening" entry
  // that has nothing after it (alta or leave_start with no closing
  // counterpart) removes the whole record, since there's nothing left
  // without it.
  if (isRealAdmin && event.type === 'alta') {
    actions = `
      <button type="button" class="btn btn-secondary btn-small" data-edit-alta="${event.membership.id}">Editar</button>
      ${isLastStatusEvent ? `<button type="button" class="btn btn-danger btn-small" data-delete-alta="${event.membership.id}">Borrar</button>` : ''}
    `;
  } else if (isRealAdmin && event.type === 'baja') {
    actions = `
      <button type="button" class="btn btn-secondary btn-small" data-edit-baja="${event.membership.id}">Editar</button>
      ${isLastStatusEvent ? `<button type="button" class="btn btn-danger btn-small" data-delete-baja="${event.membership.id}">Borrar</button>` : ''}
    `;
  } else if (isRealAdmin && event.type === 'leave_start') {
    actions = `
      <button type="button" class="btn btn-secondary btn-small" data-edit-leave-start="${event.leave.id}">Editar</button>
      ${isLastStatusEvent ? `<button type="button" class="btn btn-danger btn-small" data-delete-leave-start="${event.leave.id}">Borrar</button>` : ''}
    `;
  } else if (isRealAdmin && event.type === 'leave_end') {
    actions = `
      <button type="button" class="btn btn-secondary btn-small" data-edit-leave-end="${event.leave.id}">Editar</button>
      ${isLastStatusEvent ? `<button type="button" class="btn btn-danger btn-small" data-delete-leave-end="${event.leave.id}">Borrar</button>` : ''}
    `;
  } else if (isRealAdmin && event.type === 'license') {
    actions = `
      <button type="button" class="btn btn-secondary btn-small" data-edit-license="${event.license.id}">Editar</button>
      <button type="button" class="btn btn-danger btn-small" data-delete-license="${event.license.id}">Borrar</button>
    `;
  } else if (isRealAdmin && event.type === 'board_start') {
    actions = `
      <button type="button" class="btn btn-secondary btn-small" data-edit-board="${event.boardMember.id}">Editar</button>
      <button type="button" class="btn btn-danger btn-small" data-delete-board="${event.boardMember.id}">Borrar</button>
    `;
  }

  return renderTimelineRow(event, actions);
}

async function loadTimeline() {
  const container = document.getElementById('timeline-list');
  container.innerHTML = 'Cargando…';
  try {
    const memberships = await apiFetch(`/users/${memberLogin}/memberships`);
    const events = await buildTimelineEvents(memberships);
    // #membership-status-actions/#board-status-actions live inside the
    // same data-roles="admin" panel-actions their own click listeners
    // are already guarded for (see below) — removed from the DOM
    // entirely for board, so setting their innerHTML unconditionally
    // threw here for every board viewer, landing the error's own
    // message in this section instead of the timeline.
    if (isRealAdmin) {
      document.getElementById('membership-status-actions').innerHTML =
        renderMembershipStatusActions();
      document.getElementById('board-status-actions').innerHTML = renderBoardStatusActions();
    }
    container.innerHTML = events.length
      ? events.map(renderTimelineEvent).join('')
      : '<p>Todavía no ha sido socio.</p>';
  } catch (err) {
    container.innerHTML = `<p>${escapeHtml(err.message)}</p>`;
  }
}

// The "Historial como socio" section header shows exactly one of three shapes,
// computed from membershipStatus (see buildTimelineEvents):
//   - no open membership at all: a single "Alta en el club" button.
//   - an open membership, no open leave (activo): "Baja temporal" and
//     "Baja definitiva".
//   - an open membership *and* an open leave (de baja temporal ahora
//     mismo): "Fin baja temporal" and "Baja definitiva" — the latter
//     both ends the leave and the membership itself, with the same
//     date, in one action (see the click handler below).
// Re-rendered on every loadTimeline() alongside the rest of the
// timeline, since the right shape depends on data that only arrives
// with it.
function renderMembershipStatusActions() {
  const { openMembership, openLeave } = membershipStatus;
  if (!openMembership) {
    return '<button type="button" class="btn btn-secondary btn-small" data-status-action="alta">Alta en el club</button>';
  }
  if (openLeave) {
    return `
      <button type="button" class="btn btn-secondary btn-small" data-status-action="fin-baja-temporal">Fin baja temporal</button>
      <button type="button" class="btn btn-secondary btn-small" data-status-action="baja-definitiva-en-baja-temporal">Baja definitiva</button>
    `;
  }
  return `
    <button type="button" class="btn btn-secondary btn-small" data-status-action="baja-temporal">Baja temporal</button>
    <button type="button" class="btn btn-secondary btn-small" data-status-action="baja-definitiva">Baja definitiva</button>
  `;
}

// Alongside membership-status-actions: "+ Junta directiva" only for a
// currently active socio (no open membership at all means there's
// nobody here to put on the board) — toggling to "Deja el cargo de X"
// once they actually hold one, rather than showing both at once.
function renderBoardStatusActions() {
  if (!membershipStatus.openMembership) return '';
  if (currentBoardTerm) {
    const label = escapeHtml(
      positionLabels[currentBoardTerm.position] ?? currentBoardTerm.position,
    );
    return `<button type="button" class="btn btn-secondary btn-small" data-status-action="leave-board">Deja el cargo de ${label}</button>`;
  }
  return '<button type="button" class="btn btn-secondary btn-small" data-status-action="join-board">+ Junta directiva</button>';
}

if (isRealAdmin) {
  document.getElementById('membership-status-actions').addEventListener('click', (event) => {
    const button = event.target.closest('button[data-status-action]');
    if (!button) return;
    const { openMembership, openLeave } = membershipStatus;

    if (button.dataset.statusAction === 'alta') {
      const lastEnd = membershipStatus.lastMembership?.end_date ?? null;
      openStatusDialog({
        title: 'Alta en el club',
        hint: lastEnd
          ? `La fecha debe ser posterior a la última baja (${formatDate(lastEnd)}) y no puede ser posterior a hoy.`
          : 'La fecha no puede ser posterior a hoy.',
        minExclusive: lastEnd ? lastEnd.slice(0, 10) : null,
        minLabel: 'a la última baja',
        maxInclusive: today(),
        successMessage: 'Alta registrada.',
        onSave: (date) =>
          apiFetch(`/users/${memberLogin}/memberships`, {
            method: 'POST',
            body: JSON.stringify({ start_date: date }),
          }),
      });
      return;
    }

    if (button.dataset.statusAction === 'baja-temporal') {
      openStatusDialog({
        title: 'Baja temporal',
        hint: `La fecha debe ser posterior al alta actual (${formatDate(openMembership.start_date)}) y no puede ser posterior a hoy.`,
        minExclusive: openMembership.start_date.slice(0, 10),
        minLabel: 'al alta actual',
        maxInclusive: today(),
        successMessage: 'Baja temporal registrada.',
        onSave: (date) =>
          apiFetch(`/users/${memberLogin}/leaves`, {
            method: 'POST',
            body: JSON.stringify({ start_date: date, end_date: null, reason: null }),
          }),
      });
      return;
    }

    if (button.dataset.statusAction === 'baja-definitiva') {
      openStatusDialog({
        title: 'Baja definitiva',
        hint: `La fecha debe ser posterior al alta actual (${formatDate(openMembership.start_date)}) y no puede ser posterior a hoy.`,
        minExclusive: openMembership.start_date.slice(0, 10),
        minLabel: 'al alta actual',
        maxInclusive: today(),
        successMessage: 'Baja registrada.',
        onSave: (date) =>
          apiFetch(`/memberships/${openMembership.id}`, {
            method: 'PATCH',
            body: JSON.stringify({
              start_date: openMembership.start_date.slice(0, 10),
              end_date: date,
            }),
          }),
      });
      return;
    }

    if (button.dataset.statusAction === 'fin-baja-temporal') {
      openStatusDialog({
        title: 'Fin de la baja temporal',
        hint: `La fecha debe ser posterior al inicio de la baja temporal (${formatDate(openLeave.start_date)}) y no puede ser posterior a hoy.`,
        minExclusive: openLeave.start_date.slice(0, 10),
        minLabel: 'al inicio de la baja temporal',
        maxInclusive: today(),
        successMessage: 'Baja temporal finalizada.',
        onSave: (date) =>
          apiFetch(`/leaves/${openLeave.id}`, {
            method: 'PATCH',
            body: JSON.stringify({
              start_date: openLeave.start_date.slice(0, 10),
              end_date: date,
              reason: openLeave.reason ?? null,
            }),
          }),
      });
      return;
    }

    if (button.dataset.statusAction === 'baja-definitiva-en-baja-temporal') {
      openStatusDialog({
        title: 'Baja definitiva',
        hint:
          'Termina la baja temporal actual y da de baja definitiva, con la misma fecha. ' +
          `Debe ser posterior al inicio de la baja temporal (${formatDate(openLeave.start_date)}) y no puede ser posterior a hoy.`,
        minExclusive: openLeave.start_date.slice(0, 10),
        minLabel: 'al inicio de la baja temporal',
        maxInclusive: today(),
        successMessage: 'Baja definitiva registrada.',
        onSave: async (date) => {
          await apiFetch(`/leaves/${openLeave.id}`, {
            method: 'PATCH',
            body: JSON.stringify({
              start_date: openLeave.start_date.slice(0, 10),
              end_date: date,
              reason: openLeave.reason ?? null,
            }),
          });
          await apiFetch(`/memberships/${openMembership.id}`, {
            method: 'PATCH',
            body: JSON.stringify({
              start_date: openMembership.start_date.slice(0, 10),
              end_date: date,
            }),
          });
        },
      });
    }
  });
}

document.getElementById('save-edit-board').addEventListener('click', async () => {
  const body = {
    position: document.getElementById('eb-position').value,
    start_date: document.getElementById('eb-start-date').value,
    end_date: document.getElementById('eb-end-date').value || null,
  };
  const button = document.getElementById('save-edit-board');
  button.disabled = true;
  try {
    await apiFetch(`/board-members/${editingBoardMemberId}`, {
      method: 'PATCH',
      body: JSON.stringify(body),
    });
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
    await apiFetch(`/licenses/${editingLicenseId}`, {
      method: 'PATCH',
      body: JSON.stringify(body),
    });
    showFeedback('Licencia actualizada.');
    editLicenseDialog.close();
    await loadTimeline();
  } catch (err) {
    showError(err.message);
  } finally {
    button.disabled = false;
  }
});

// A small helper so every "Borrar" below shares the same confirm +
// disable + reload shape, differing only in the confirmation text and
// the actual request.
async function performTimelineDelete(button, confirmText, request) {
  if (!confirm(confirmText)) return;
  button.disabled = true;
  try {
    await request();
    await loadTimeline();
    await loadMember();
  } catch (err) {
    button.disabled = false;
    showError(err.message);
  }
}

document.getElementById('timeline-list').addEventListener('click', async (event) => {
  const editAltaButton = event.target.closest('button[data-edit-alta]');
  if (editAltaButton) {
    const m = membershipsById[Number(editAltaButton.dataset.editAlta)];
    openStatusDialog({
      title: 'Editar alta',
      initial: m.start_date.slice(0, 10),
      successMessage: 'Alta actualizada.',
      onSave: (date) =>
        apiFetch(`/memberships/${m.id}`, {
          method: 'PATCH',
          body: JSON.stringify({
            start_date: date,
            end_date: m.end_date ? m.end_date.slice(0, 10) : null,
          }),
        }),
    });
    return;
  }

  const deleteAltaButton = event.target.closest('button[data-delete-alta]');
  if (deleteAltaButton) {
    await performTimelineDelete(
      deleteAltaButton,
      '¿Borrar esta alta? Esta acción no se puede deshacer.',
      () => apiFetch(`/memberships/${deleteAltaButton.dataset.deleteAlta}`, { method: 'DELETE' }),
    );
    return;
  }

  const editBajaButton = event.target.closest('button[data-edit-baja]');
  if (editBajaButton) {
    const m = membershipsById[Number(editBajaButton.dataset.editBaja)];
    openStatusDialog({
      title: 'Editar baja',
      initial: m.end_date.slice(0, 10),
      successMessage: 'Baja actualizada.',
      onSave: (date) =>
        apiFetch(`/memberships/${m.id}`, {
          method: 'PATCH',
          body: JSON.stringify({ start_date: m.start_date.slice(0, 10), end_date: date }),
        }),
    });
    return;
  }

  const deleteBajaButton = event.target.closest('button[data-delete-baja]');
  if (deleteBajaButton) {
    const m = membershipsById[Number(deleteBajaButton.dataset.deleteBaja)];
    await performTimelineDelete(
      deleteBajaButton,
      '¿Deshacer esta baja? La persona volverá a estar de alta.',
      () =>
        apiFetch(`/memberships/${m.id}`, {
          method: 'PATCH',
          body: JSON.stringify({ start_date: m.start_date.slice(0, 10), end_date: null }),
        }),
    );
    return;
  }

  const editLeaveStartButton = event.target.closest('button[data-edit-leave-start]');
  if (editLeaveStartButton) {
    const l = leavesById[Number(editLeaveStartButton.dataset.editLeaveStart)];
    openStatusDialog({
      title: 'Editar inicio de baja temporal',
      initial: l.start_date.slice(0, 10),
      successMessage: 'Baja temporal actualizada.',
      onSave: (date) =>
        apiFetch(`/leaves/${l.id}`, {
          method: 'PATCH',
          body: JSON.stringify({
            start_date: date,
            end_date: l.end_date ? l.end_date.slice(0, 10) : null,
            reason: l.reason ?? null,
          }),
        }),
    });
    return;
  }

  const deleteLeaveStartButton = event.target.closest('button[data-delete-leave-start]');
  if (deleteLeaveStartButton) {
    await performTimelineDelete(
      deleteLeaveStartButton,
      '¿Borrar esta baja temporal? Esta acción no se puede deshacer.',
      () =>
        apiFetch(`/leaves/${deleteLeaveStartButton.dataset.deleteLeaveStart}`, {
          method: 'DELETE',
        }),
    );
    return;
  }

  const editLeaveEndButton = event.target.closest('button[data-edit-leave-end]');
  if (editLeaveEndButton) {
    const l = leavesById[Number(editLeaveEndButton.dataset.editLeaveEnd)];
    openStatusDialog({
      title: 'Editar fin de baja temporal',
      initial: l.end_date.slice(0, 10),
      successMessage: 'Baja temporal actualizada.',
      onSave: (date) =>
        apiFetch(`/leaves/${l.id}`, {
          method: 'PATCH',
          body: JSON.stringify({
            start_date: l.start_date.slice(0, 10),
            end_date: date,
            reason: l.reason ?? null,
          }),
        }),
    });
    return;
  }

  const deleteLeaveEndButton = event.target.closest('button[data-delete-leave-end]');
  if (deleteLeaveEndButton) {
    const l = leavesById[Number(deleteLeaveEndButton.dataset.deleteLeaveEnd)];
    await performTimelineDelete(
      deleteLeaveEndButton,
      '¿Deshacer el fin de esta baja temporal? Volverá a estar en curso.',
      () =>
        apiFetch(`/leaves/${l.id}`, {
          method: 'PATCH',
          body: JSON.stringify({
            start_date: l.start_date.slice(0, 10),
            end_date: null,
            reason: l.reason ?? null,
          }),
        }),
    );
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
      await apiFetch(`/licenses/${deleteLicenseButton.dataset.deleteLicense}`, {
        method: 'DELETE',
      });
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
    return;
  }

  const deleteBoardButton = event.target.closest('button[data-delete-board]');
  if (deleteBoardButton) {
    await performTimelineDelete(
      deleteBoardButton,
      '¿Borrar este cargo en la junta directiva? Esta acción no se puede deshacer.',
      () =>
        apiFetch(`/board-members/${deleteBoardButton.dataset.deleteBoard}`, { method: 'DELETE' }),
    );
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
      if (
        !confirm(
          '¿Entrar en la intranet como esta persona? Podrás salir de la impersonación en cualquier momento.',
        )
      )
        return;
      // Readonly-until-focused and the beforeunload reset (see
      // ADDRESS_CLUSTER_FIELDS above) still weren't enough on their own
      // to stop Chrome from offering to save this address specifically
      // when leaving via this button — cleared directly, right here,
      // before anything else happens.
      editForm.address.value = '';
      impersonateButton.disabled = true;
      try {
        const result = await apiFetch(`/users/${memberLogin}/impersonate`, { method: 'POST' });
        startImpersonation(result.token);
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
      <td><a href="/actividad?id=${activity.id}">${escapeHtml(activity.title)}</a></td>
      <td>${typeLabels[activity.type] ?? activity.type}</td>
      <td><span class="badge status-${activityState(activity)}">${activityStateLabels[activityState(activity)]}</span></td>
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
    tableBody.innerHTML =
      '<tr><td colspan="6">No se pudo cargar el historial de actividades.</td></tr>';
  }
}

// Only loan/rental/sale ever have a recipient, so this person's own
// movement history (GET /users/{login}/material/movements) never contains an
// acquisition, loss, abandonment or retirement — no labels needed here
// for those.
const movementTypeLabels = {
  loan: 'Préstamo',
  rental: 'Alquiler',
  sale: 'Venta',
  return: 'Devolución',
};
const movementStatusLabels = {
  active: 'Activo',
  completed: 'Completado',
  overdue: 'Vencido',
  cancelled: 'Cancelado',
};
const ropeTypeLabels = { caving: 'Espeleología', canyoning: 'Cañones' };
const bagTypeLabels = {
  caving: 'Espeleología',
  canyoning: 'Cañones',
  quick: 'Rápida',
  special: 'Especial',
};

// A rope/bag is identified by brand+model (plus its type-specific
// attributes); a general item may have no known brand/model at all, so its
// category — always set — anchors the label instead.
function itemLabel(item) {
  const brandModel = [item.brand, item.model].filter(Boolean).join(' ');
  if (item.type === 'rope') {
    const parts = [
      brandModel || null,
      `${item.diameter_mm} mm`,
      ropeTypeLabels[item.rope_type] ?? item.rope_type,
      item.manufacture_year,
      item.length_m ? `${item.length_m} m` : null,
    ];
    return escapeHtml(parts.filter(Boolean).join(' · '));
  }
  if (item.type === 'bag') {
    const parts = [
      brandModel || null,
      `${item.capacity_liters} L`,
      bagTypeLabels[item.bag_type] ?? item.bag_type,
    ];
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
      apiFetch('/material/items').catch(() => []),
      apiFetch(`/users/${memberLogin}/material/movements`),
    ]);
    const itemsById = Object.fromEntries(items.map((item) => [item.id, item]));
    tableBody.innerHTML = movements.length
      ? movements.map((m) => renderMovementRow(m, itemsById)).join('')
      : '<tr><td colspan="6">Todavía no tiene movimientos de material.</td></tr>';
  } catch {
    tableBody.innerHTML =
      '<tr><td colspan="6">No se pudo cargar el historial de material.</td></tr>';
  }
}

// Board-term creation shows up as just another event on the same
// "Historial como socio" timeline (see buildTimelineEvents). It has
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
  // No <form> here — a plain panel, so this clears its own fields by
  // hand on every close (cancel, ×, Escape, or after a successful save)
  // instead of getting that for free the way initModal(dialog, form)
  // would.
  addBoardDialog.addEventListener('close', () => {
    document.getElementById('add-board-position').selectedIndex = 0;
    document.getElementById('add-board-start-date').value = '';
  });

  document.getElementById('board-status-actions').addEventListener('click', (event) => {
    const button = event.target.closest('button[data-status-action]');
    if (!button) return;

    if (button.dataset.statusAction === 'join-board') {
      const dateInput = document.getElementById('add-board-start-date');
      if (!dateInput.value) {
        dateInput.value = today();
      }
      openModal(addBoardDialog);
      return;
    }

    if (button.dataset.statusAction === 'leave-board') {
      const label = positionLabels[currentBoardTerm.position] ?? currentBoardTerm.position;
      openStatusDialog({
        title: `Dejar el cargo de ${label}`,
        hint: `La fecha debe ser posterior al inicio en el cargo (${formatDate(currentBoardTerm.start_date)}) y no puede ser posterior a hoy.`,
        minExclusive: currentBoardTerm.start_date.slice(0, 10),
        minLabel: 'al inicio en el cargo',
        maxInclusive: today(),
        successMessage: 'Cargo finalizado.',
        onSave: (date) =>
          apiFetch(`/board-members/${currentBoardTerm.id}`, {
            method: 'PATCH',
            body: JSON.stringify({
              position: currentBoardTerm.position,
              start_date: currentBoardTerm.start_date.slice(0, 10),
              end_date: date,
            }),
          }),
      });
    }
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

const rolesLoaded = loadRolesCatalog();
loadLicenseTypesCatalog();
loadMember(rolesLoaded);
loadTimeline();
loadActivities();
loadMovements();
initLicenseFiles(memberLogin);
initUserFilesBrowser(memberLogin);
// An admin sees anybody's sessions (the API gives them to nobody else, and the section is removed
// for the rest); on their own page they are their own.
if (document.getElementById('sessions-list')) {
  initSessionsPanel({ login: memberLogin, own: memberLogin === viewer.login });
}
