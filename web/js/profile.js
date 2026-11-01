import {
  hasAnyRole,
  apiFetch,
  escapeHtml,
  formatDateTime,
  logout,
  showFeedback,
  showError,
} from './api.js';
import { initPage } from './layout.js';
import { initSortableHeaders } from './sortableHeaders.js';
import { initUserFilesBrowser } from './userFiles.js';
import { initLicenseFiles } from './licenseFiles.js';
import { trackDirtyFields, markFieldsClean } from './formDirty.js';
import { loadTimelineData, renderTimelineRow } from './timeline.js';

const user = await initPage();
const isAdmin = hasAnyRole(user, ['admin']);

const editForm = document.getElementById('edit-profile-form');

// Only admin can change name, DNI, birth date, or gender — everyone else
// can see their own, but self-service edits are limited to contact fields
// (see users.UpdateInput). Disabling (not hiding) them keeps the values
// visible while excluding them from the submitted form — a disabled field
// is never part of FormData.
if (!isAdmin) {
  editForm.first_name.disabled = true;
  editForm.last_name.disabled = true;
  editForm.national_id.disabled = true;
  editForm.birth_date.disabled = true;
  editForm.gender.disabled = true;
  document.getElementById('restricted-fields-hint').hidden = false;
}

// The last record loadProfile() fetched, so "Cancelar" can restore the
// form to it without a round trip — reverting unsaved edits is "put back
// what was already loaded", not "re-fetch the latest from the server".
let lastLoadedProfile = null;

trackDirtyFields(editForm);

// Chrome's address-autofill heuristic recognizes this field cluster
// (street/postal code/city/province) by name/id/label, independent of
// autocomplete="off" — Chrome deliberately ignores that attribute for
// address/payment fields specifically, precisely to stop this kind of
// page-author workaround, so it alone didn't stop the "save this
// address?" prompt in practice (reported against /socio's own version
// of this form). A field Chrome's autofill excludes outright —
// readonly, unlike disabled, still submits its value normally — never
// looks like a real address form to it, so these start readonly and
// only become editable while actually focused, reverting the instant
// focus leaves (a click on any other element blurs the field
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

function populateEditForm(profile) {
  editForm.first_name.value = profile.first_name;
  editForm.last_name.value = profile.last_name;
  editForm.email.value = profile.email ?? '';
  editForm.phone.value = profile.phone ?? '';
  editForm.national_id.value = profile.national_id ?? '';
  editForm.birth_date.value = profile.birth_date ? profile.birth_date.slice(0, 10) : '';
  editForm.gender.value = profile.gender ?? '';
  editForm.address.value = profile.address ?? '';
  editForm.postal_code.value = profile.postal_code ?? '';
  editForm.city.value = profile.city ?? '';
  editForm.province.value = profile.province ?? '';
  editForm.emergency_contact.value = profile.emergency_contact ?? '';
  for (const name of ADDRESS_CLUSTER_FIELDS) {
    editForm.elements[name].readOnly = true;
  }
  markFieldsClean(editForm);
}

// Chrome can offer to save this address the instant the page is left —
// a link click, browser back/forward — just from noticing a filled
// address-shaped form in the DOM at that moment, even though it's never
// actually submitted (autocomplete="off" on every field here, on its
// own, didn't stop this in practice on /socio's own version of this
// form). Clearing the form right before unload is the backstop: nothing
// address-shaped left for Chrome to notice. A beforeunload listener
// also opts this page out of the back/forward cache, so a real
// back-navigation always re-runs this script and refetches fresh data
// rather than restoring a JS-preserved page with these fields already
// blanked out.
window.addEventListener('beforeunload', () => editForm.reset());

document.getElementById('cancel-edit-profile').addEventListener('click', () => {
  if (lastLoadedProfile) {
    populateEditForm(lastLoadedProfile);
  }
});

async function loadProfile() {
  if (!user) {
    return;
  }
  try {
    const profile = await apiFetch(`/users/${user.login}`);
    lastLoadedProfile = profile;
    document.getElementById('profile-name').textContent =
      `${profile.first_name} ${profile.last_name}`;
    document.getElementById('profile-email').textContent = profile.email ?? '';
    populateEditForm(profile);

    // The same history as /socio, without its action buttons. Each part
    // of it that fails to load just shows nothing (see loadTimelineData).
    const memberships = await apiFetch(`/users/${user.login}/memberships`);
    const { events } = await loadTimelineData(user.login, memberships);
    document.getElementById('memberships-section').hidden = events.length === 0;
    document.getElementById('timeline-list').innerHTML = events
      .map((event) => renderTimelineRow(event))
      .join('');
  } catch {
    document.getElementById('profile-email').textContent = 'No se ha podido cargar tu perfil.';
  }
}

editForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  const formData = new FormData(editForm);
  const optional = (key) => formData.get(key)?.toString().trim() || null;

  const body = {
    email: optional('email'),
    phone: optional('phone'),
    address: optional('address'),
    postal_code: optional('postal_code'),
    city: optional('city'),
    province: optional('province'),
    emergency_contact: optional('emergency_contact'),
  };
  // first_name/last_name/national_id/birth_date/gender are disabled (and
  // so absent from FormData) for anyone but admin — but these keys still
  // need to be left out of the body entirely rather than sent as null:
  // the backend tells "never sent" apart from "explicitly cleared to
  // null" (see users.Nullable), and a self-edit that touches any of them
  // at all is rejected as restricted_fields, even a null that changes
  // nothing. Sending nothing here is what makes submitting this form
  // unchanged actually a no-op for a plain member instead of an error.
  if (isAdmin) {
    body.first_name = formData.get('first_name');
    body.last_name = formData.get('last_name');
    body.birth_date = formData.get('birth_date');
    body.national_id = optional('national_id');
    body.gender = optional('gender');
  }

  const submitButton = editForm.querySelector('button[type="submit"]');
  submitButton.disabled = true;
  try {
    await apiFetch(`/users/${user.login}`, { method: 'PATCH', body: JSON.stringify(body) });
    showFeedback('Datos actualizados.');
    // Re-fetch rather than trusting `body` directly — the same pattern
    // member.js's own edit form follows — so the form reflects exactly
    // what the server actually saved.
    await loadProfile();
  } catch (err) {
    showError(err.message);
  } finally {
    submitButton.disabled = false;
  }
});

// "Mis sesiones": where the person is signed in (GET /users/{login}/tokens?status=active, the
// ones not expired or revoked), each of which they can close (DELETE .../tokens/{id}). Closing the
// one in use is signing out, so that one signs out of this browser too.
const sessionsBody = document.getElementById('sessions-list');
let sessions = [];
// Impersonating this person, an admin sees their sessions — as the person would — but the API doesn't
// let sessions be ended through an impersonation, so the buttons are not offered, and a note says why.
const impersonating = Boolean(user?.impersonatedBy);
document.getElementById('sessions-impersonating').hidden = !impersonating;
// Sorted here (they are all loaded): by last use, when it was opened and when it expires, either way. A
// session never used counts as the oldest use. Starts with the most recently used first.
const sessionsSort = { key: 'last_used', desc: true };
const sessionDate = {
  last_used: (s) => s.last_used_at ?? '',
  created: (s) => s.created_at,
  expires: (s) => s.expires_at,
};

function renderSessions() {
  const sorted = sessions.slice().sort((a, b) => {
    const [x, y] = [sessionDate[sessionsSort.key](a), sessionDate[sessionsSort.key](b)];
    // ISO timestamps compare as text; the id breaks a tie the same way for both directions, so the
    // order is stable.
    const byDate = x < y ? -1 : x > y ? 1 : 0;
    return (sessionsSort.desc ? -byDate : byDate) || b.id - a.id;
  });
  sessionsBody.innerHTML = sorted.length
    ? sorted.map(sessionRow).join('')
    : '<tr><td colspan="6">No tienes ninguna sesión abierta.</td></tr>';
}

function sessionRow(session) {
  const parts = [session.browser, session.os].filter(Boolean);
  const client = session.last_used_user_agent
    ? `<span title="${escapeHtml(session.last_used_user_agent)}">${parts.length ? escapeHtml(parts.join(' · ')) : 'Navegador desconocido'}</span>`
    : '—';
  const own = session.current ? ' <span class="badge">Esta sesión</span>' : '';
  const byAdmin = session.impersonated_by_login
    ? ` <span class="badge" title="Un administrador entró como tú, por ejemplo para ayudarte">Abierta por ${escapeHtml(session.impersonated_by_login)}</span>`
    : '';
  return `
    <tr>
      <td>${client}${own}${byAdmin}</td>
      <td>${session.last_used_at ? formatDateTime(session.last_used_at) : 'Nunca'}</td>
      <td>${session.last_used_ip ? escapeHtml(session.last_used_ip) : '—'}</td>
      <td>${formatDateTime(session.created_at)}</td>
      <td>${formatDateTime(session.expires_at)}</td>
      <td>${impersonating ? '' : `<button type="button" class="btn btn-danger btn-small" data-close-session="${session.id}">${session.current ? 'Cerrar esta sesión' : 'Cerrar'}</button>`}</td>
    </tr>`;
}

async function loadSessions() {
  try {
    sessions = await apiFetch(`/users/${encodeURIComponent(user.login)}/tokens?status=active`);
    renderSessions();
  } catch (err) {
    sessionsBody.innerHTML = '<tr><td colspan="6">No se pudieron cargar las sesiones.</td></tr>';
    showError(err.message);
  }
}

initSortableHeaders(document.getElementById('sessions-head'), {
  sort: sessionsSort,
  defaultDesc: () => true,
  onChange: renderSessions,
});

sessionsBody.addEventListener('click', async (event) => {
  const button = event.target.closest('button[data-close-session]');
  if (!button) return;
  const session = sessions.find((s) => s.id === Number(button.dataset.closeSession));
  if (!session) return;
  const question = session.current
    ? 'Es la sesión que estás usando ahora: si la cierras saldrás de la intranet y tendrás que volver a entrar. ¿Cerrarla?'
    : '¿Cerrar esta sesión? Ese dispositivo tendrá que volver a pedir un enlace para entrar.';
  if (!confirm(question)) return;
  button.disabled = true;
  try {
    await apiFetch(`/users/${encodeURIComponent(user.login)}/tokens/${session.id}`, {
      method: 'DELETE',
    });
    if (session.current) {
      logout();
      return;
    }
    showFeedback('Sesión cerrada.');
    await loadSessions();
  } catch (err) {
    button.disabled = false;
    showError(err.message);
  }
});

loadProfile();
if (user) {
  loadSessions();
  initLicenseFiles(user.login, { hideWhenEmpty: true });
  initUserFilesBrowser(user.login, { hideWhenEmpty: true });
}
