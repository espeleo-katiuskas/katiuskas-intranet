import { initSession, logout, applyRoleVisibility, currentUser, apiFetch } from './api.js';

await initSession();
applyRoleVisibility();
document.getElementById('logout-button').addEventListener('click', logout);

const user = currentUser();
if (user?.role === 'member') {
  window.location.href = 'index.html';
}

function formatDate(isoDate) {
  return new Date(isoDate).toLocaleDateString('es-ES');
}

async function loadMembers() {
  const tableBody = document.getElementById('members-table-body');
  try {
    const memberships = await apiFetch('/memberships');
    tableBody.innerHTML = memberships
      .map((membership) => `
        <tr>
          <td>${membership.first_name} ${membership.last_name}</td>
          <td>${membership.membership_number ?? '—'}</td>
          <td>${formatDate(membership.start_date)}</td>
        </tr>
      `)
      .join('');
  } catch {
    tableBody.innerHTML = '<tr><td colspan="3">No se pudo cargar la lista de socios.</td></tr>';
  }
}

loadMembers();
