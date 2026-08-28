import { initSession, logout, applyRoleVisibility, currentUser, apiFetch } from './api.js';

await initSession();
applyRoleVisibility();
document.getElementById('logout-button').addEventListener('click', logout);

const user = currentUser();

function formatDate(isoDate) {
  return new Date(isoDate).toLocaleDateString('es-ES');
}

async function loadProfile() {
  if (!user) {
    return;
  }
  try {
    const profile = await apiFetch(`/users/${user.id}`);
    document.getElementById('profile-name').textContent = `${profile.first_name} ${profile.last_name}`;
    document.getElementById('profile-email').textContent = profile.email;

    const memberships = await apiFetch(`/users/${user.id}/memberships`);
    const tableBody = document.getElementById('memberships-table-body');
    tableBody.innerHTML = memberships
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

loadProfile();
