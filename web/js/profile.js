import {
  initSession,
  logout,
  applyRoleVisibility,
  currentUser,
  apiFetch,
  formatDate,
} from './api.js';

await initSession();
applyRoleVisibility();
document.getElementById('logout-button').addEventListener('click', logout);

const user = currentUser();

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
    const profile = await apiFetch(`/users/${user.id}`);
    document.getElementById('profile-name').textContent = `${profile.first_name} ${profile.last_name}`;
    document.getElementById('profile-email').textContent = profile.email;

    editForm.first_name.value = profile.first_name;
    editForm.last_name.value = profile.last_name;
    editForm.phone.value = profile.phone ?? '';
    editForm.address.value = profile.address ?? '';
    editForm.emergency_contact_name.value = profile.emergency_contact_name ?? '';
    editForm.emergency_contact_phone.value = profile.emergency_contact_phone ?? '';

    const memberships = await apiFetch(`/users/${user.id}/memberships`);
    const tableBody = document.getElementById('memberships-table-body');
    tableBody.innerHTML = memberships.length
      ? memberships
          .map((membership) => `
            <tr>
              <td>${formatDate(membership.start_date)}</td>
              <td>${membership.end_date ? formatDate(membership.end_date) : 'Activa'}</td>
            </tr>
          `)
          .join('')
      : '<tr><td colspan="2">Todavía no has sido socio.</td></tr>';
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
    phone: optional('phone'),
    address: optional('address'),
    emergency_contact_name: optional('emergency_contact_name'),
    emergency_contact_phone: optional('emergency_contact_phone'),
  };

  const submitButton = editForm.querySelector('button[type="submit"]');
  submitButton.disabled = true;
  try {
    await apiFetch(`/users/${user.id}`, { method: 'PATCH', body: JSON.stringify(body) });
    showFeedback('Datos actualizados.');
    document.getElementById('profile-name').textContent = `${body.first_name} ${body.last_name}`;
  } catch (err) {
    showError(err.message);
  } finally {
    submitButton.disabled = false;
  }
});

loadProfile();
