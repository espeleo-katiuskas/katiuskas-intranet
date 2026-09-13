import {
  hasAnyRole,
  apiFetch,
  formatDate,
  showFeedback,
  showError,
} from './api.js';
import { initPage } from './layout.js';
import { initUserFilesBrowser } from './userFiles.js';
import { trackDirtyFields, markFieldsClean } from './formDirty.js';

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
// address?" prompt in practice (reported against /member's own version
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
  field.addEventListener('focus', () => { field.readOnly = false; });
  field.addEventListener('blur', () => { field.readOnly = true; });
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
// own, didn't stop this in practice on /member's own version of this
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
    document.getElementById('profile-name').textContent = `${profile.first_name} ${profile.last_name}`;
    document.getElementById('profile-email').textContent = profile.email ?? '';
    populateEditForm(profile);

    const memberships = await apiFetch(`/users/${user.login}/memberships`);
    document.getElementById('memberships-section').hidden = memberships.length === 0;
    document.getElementById('memberships-table-body').innerHTML = memberships
      .map((membership) => `
        <tr>
          <td>${formatDate(membership.start_date)}</td>
          <td>${membership.end_date ? formatDate(membership.end_date) : 'Activa'}</td>
        </tr>
      `)
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

loadProfile();
if (user) {
  initUserFilesBrowser(user.login, { hideWhenEmpty: true });
}
