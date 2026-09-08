import {
  initSession,
  logout,
  applyRoleVisibility,
  initSidebarToggle,
  loadTopbarGreeting,
  currentUser,
  hasAnyRole,
  apiFetch,
  formatDate,
} from './api.js';
import { initUserFilesBrowser } from './userFiles.js';

await initSession();
applyRoleVisibility();
document.getElementById('logout-button').addEventListener('click', logout);
initSidebarToggle();
loadTopbarGreeting();

const user = currentUser();
const isAdmin = hasAnyRole(user, ['admin']);

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

const editForm = document.getElementById('edit-profile-form');

async function loadProfile() {
  if (!user) {
    return;
  }
  try {
    const profile = await apiFetch(`/users/${user.login}`);
    document.getElementById('profile-name').textContent = `${profile.first_name} ${profile.last_name}`;
    document.getElementById('profile-email').textContent = profile.email ?? '';

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

    // Only admin can change name, DNI, birth date, or gender — everyone
    // else can see their own, but self-service edits are limited to
    // contact fields (see users.UpdateInput). Disabling (not hiding)
    // them keeps the values visible while excluding them from the
    // submitted form — a disabled field is never part of FormData.
    if (!isAdmin) {
      editForm.first_name.disabled = true;
      editForm.last_name.disabled = true;
      editForm.national_id.disabled = true;
      editForm.birth_date.disabled = true;
      editForm.gender.disabled = true;
      document.getElementById('restricted-fields-hint').hidden = false;
    }

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
    await apiFetch(`/users/${user.login}`, { method: 'PATCH', body: JSON.stringify(body) });
    showFeedback('Datos actualizados.');
    document.getElementById('profile-name').textContent = `${body.first_name} ${body.last_name}`;
    document.getElementById('profile-email').textContent = body.email;
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
