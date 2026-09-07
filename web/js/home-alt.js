import { initSession, logout, applyRoleVisibility, apiFetch, escapeHtml, currentUser } from './api.js';

await initSession();
applyRoleVisibility();
document.getElementById('logout-button').addEventListener('click', logout);

// Falls back to the login if the profile fetch fails — still a valid
// enough greeting, just less friendly than a first name.
async function loadUserName() {
  const nameEl = document.getElementById('alt-user-name');
  try {
    const profile = await apiFetch(`/users/${currentUser().login}`);
    nameEl.textContent = profile.first_name;
  } catch {
    nameEl.textContent = currentUser().login;
  }
}

loadUserName();

// Same labels/endpoint as the regular homepage's own Junta Directiva
// table (js/dashboard.js) — GET /board-members is open to anyone with a
// baseline role, just rendered as cards here instead of a table row.
const positionLabels = {
  president: 'Presidente',
  vice_president: 'Vicepresidente',
  secretary: 'Secretario',
  treasurer: 'Tesorero',
  member_at_large: 'Vocal',
};

async function loadBoardMembers() {
  const grid = document.getElementById('alt-board-grid');
  try {
    const boardMembers = await apiFetch('/board-members');
    grid.innerHTML = boardMembers.length
      ? boardMembers
          .map(
            (b) => `
              <div class="alt-board-card">
                <span class="alt-board-dot"></span>
                <div>
                  <div class="alt-board-name">${escapeHtml(b.first_name)}</div>
                  <div class="alt-board-lastname">${escapeHtml(b.last_name)}</div>
                  <div class="alt-board-meta">
                    <span>${escapeHtml(positionLabels[b.position] ?? b.position)}</span>
                    <span>${escapeHtml(b.phone ?? '')}</span>
                  </div>
                </div>
              </div>
            `
          )
          .join('')
      : '<p class="subtitle">Ahora mismo no hay nadie en la Junta Directiva.</p>';
  } catch {
    grid.innerHTML = '<p class="subtitle">No se pudo cargar la Junta Directiva.</p>';
  }
}

loadBoardMembers();
