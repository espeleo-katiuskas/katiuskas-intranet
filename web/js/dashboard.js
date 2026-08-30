import { initSession, logout, applyRoleVisibility, apiFetch, escapeHtml } from './api.js';

await initSession();
applyRoleVisibility();
document.getElementById('logout-button').addEventListener('click', logout);

const positionLabels = {
  president: 'Presidente',
  vice_president: 'Vicepresidente',
  secretary: 'Secretario',
  treasurer: 'Tesorero',
  member_at_large: 'Vocal',
};

// GET /board-members is open to anyone with a baseline role (admin,
// board, or member) — anyone can see who's currently on the Junta
// Directiva and how to reach them.
async function loadBoardMembers() {
  const tableBody = document.getElementById('board-table-body');
  try {
    const boardMembers = await apiFetch('/board-members');
    tableBody.innerHTML = boardMembers.length
      ? boardMembers
          .map((b) => `
            <tr>
              <td>${escapeHtml(b.first_name)}</td>
              <td>${escapeHtml(b.last_name)}</td>
              <td>${escapeHtml(positionLabels[b.position] ?? b.position)}</td>
              <td>${escapeHtml(b.phone ?? '')}</td>
              <td>${escapeHtml(b.email ?? '')}</td>
            </tr>
          `)
          .join('')
      : '<tr><td colspan="5">Ahora mismo no hay nadie en la Junta Directiva.</td></tr>';
  } catch {
    tableBody.innerHTML = '<tr><td colspan="5">No se pudo cargar la Junta Directiva.</td></tr>';
  }
}

loadBoardMembers();
