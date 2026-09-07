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

// A person's phone can hold more than one number, space-separated (see
// users.User.Phone) — board-members exposes whatever's on file as one
// string, so only the first is shown here rather than the raw
// space-separated list.
function firstPhone(phone) {
  return (phone ?? '').split(' ')[0];
}

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
                    <span class="alt-board-phone">${escapeHtml(firstPhone(b.phone))}</span>
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

// "Copiar NIF"/"Copiar IBAN" — the only two buttons here backed by
// real data (see home-alt.html); everything else in "Actas asambleas"
// is still a static placeholder with no endpoint behind it, so those
// links are just kept from navigating anywhere.
document.querySelectorAll('[data-copy]').forEach((button) => {
  const originalLabel = button.textContent;
  button.addEventListener('click', async () => {
    try {
      await navigator.clipboard.writeText(button.dataset.copy);
      button.textContent = '¡Copiado!';
    } catch {
      button.textContent = 'No se pudo copiar';
    }
    setTimeout(() => {
      button.textContent = originalLabel;
    }, 1500);
  });
});

document.querySelectorAll('.alt-static a[href="#"]').forEach((link) => {
  link.addEventListener('click', (event) => event.preventDefault());
});
