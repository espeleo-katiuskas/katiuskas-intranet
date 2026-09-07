import { API_BASE, initSession, logout, applyRoleVisibility, apiFetch, escapeHtml, encodeFilePath, currentUser } from './api.js';

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

// Documentos/Actas asambleas/Memorias de actividades all read straight
// from a fixed /library folder (library/documentos, library/actas,
// library/memorias — see home-alt.html) rather than from any
// club-specific endpoint, so admins can add or remove a PDF just by
// dropping it in, no deploy needed. A folder's own optional
// .order.json — a plain JSON array of file names, invisible in
// /library's own listing since it starts with a dot but still fetchable
// by exact path (Service.OpenFile applies no such filter) — decides
// which files show first and in what order; anything present in the
// folder but not listed there follows afterward, in the alphabetical
// order /library's own listing already returns everything in.
// Reordering is then just editing that one file inside ./library.
async function loadOrderedFiles(dirPath) {
  const listing = await apiFetch(`/library?path=${encodeURIComponent(dirPath)}`);
  const files = listing.entries.filter((entry) => entry.type === 'file');

  let order = [];
  try {
    const manifest = await apiFetch(`/library/file/${encodeFilePath(`${dirPath}/.order.json`)}`);
    if (Array.isArray(manifest)) order = manifest;
  } catch {
    // No .order.json (or it failed to load) — alphabetical order below covers everything.
  }

  const remaining = new Map(files.map((f) => [f.name, f]));
  const ordered = [];
  for (const name of order) {
    const file = remaining.get(name);
    if (file) {
      ordered.push(file);
      remaining.delete(name);
    }
  }
  for (const file of files) {
    if (remaining.has(file.name)) ordered.push(file);
  }
  return ordered;
}

function stripExtension(name) {
  const i = name.lastIndexOf('.');
  return i === -1 ? name : name.slice(0, i);
}

// Every Documentos/Actas/Memorias entry is a plain link straight to
// /library/file, opened in a new tab — no fetch/blob juggling needed:
// initSession() (see api.js) also gets the API to set the session as an
// HttpOnly cookie (auth.SetSessionCookie), which GET /library/file
// accepts as well as the bearer header specifically so a plain,
// browser-navigated URL like this one works and the browser's own
// "Save As" picks up the file's real name — a blob: URL created from a
// fetch() response can't carry that name at all.
function fileLink(dirPath, name, label) {
  const href = `${API_BASE}/library/file/${encodeFilePath(`${dirPath}/${name}`)}`;
  return `<a href="${href}" target="_blank" rel="noopener">${escapeHtml(label)}</a>`;
}

// Actas are named YYYY-MM-DD.pdf so alphabetical order doubles as
// chronological order; only the display label gets reformatted to
// DD-MM-YYYY. actaYear pulls the year out for the year <select> below;
// both return null for a name that doesn't match the convention, so
// such a file is simply left out of the year-filtered view.
function formatActaDate(name) {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(stripExtension(name));
  return match ? `${match[3]}-${match[2]}-${match[1]}` : stripExtension(name);
}

function actaYear(name) {
  const match = /^(\d{4})-\d{2}-\d{2}$/.exec(stripExtension(name));
  return match ? match[1] : null;
}

async function loadDocumentos() {
  const list = document.getElementById('alt-documentos-list');
  try {
    const files = await loadOrderedFiles('documentos');
    list.innerHTML = files.length
      ? files.map((f) => `<li>${fileLink('documentos', f.name, stripExtension(f.name))}</li>`).join('')
      : '<li>No hay documentos disponibles.</li>';
  } catch {
    list.innerHTML = '<li>No se pudieron cargar los documentos.</li>';
  }
}

// There are normally 2-4 actas a year, going back to 2006, so unlike
// Documentos/Memorias they're filtered down to one year at a time
// rather than all listed together. The year <select> is built from
// whatever years actually have a file (ascending, most recent selected
// by default) instead of a fixed range, so a year with no assembly
// never shows up as an empty option, and .order.json (see
// loadOrderedFiles) still decides the order actas appear in *within* a
// year, in case that's ever needed.
async function loadActas() {
  const yearSelect = document.getElementById('alt-actas-year');
  const list = document.getElementById('alt-actas-list');
  try {
    const files = (await loadOrderedFiles('actas')).filter((f) => actaYear(f.name) !== null);
    const years = [...new Set(files.map((f) => actaYear(f.name)))].sort();
    if (!years.length) {
      yearSelect.innerHTML = '';
      list.innerHTML = '<li>No hay actas disponibles.</li>';
      return;
    }

    yearSelect.innerHTML = years.map((year) => `<option value="${year}">${year}</option>`).join('');
    yearSelect.value = years[years.length - 1];

    const renderSelectedYear = () => {
      const yearFiles = files.filter((f) => actaYear(f.name) === yearSelect.value);
      list.innerHTML = yearFiles
        .map((f) => `<li>${fileLink('actas', f.name, formatActaDate(f.name))}</li>`)
        .join('');
    };
    yearSelect.addEventListener('change', renderSelectedYear);
    renderSelectedYear();
  } catch {
    yearSelect.innerHTML = '';
    list.innerHTML = '<li>No se pudieron cargar las actas.</li>';
  }
}

async function loadMemorias() {
  const container = document.getElementById('alt-memorias-list');
  try {
    const files = await loadOrderedFiles('memorias');
    container.innerHTML = files.length
      ? files.map((f) => fileLink('memorias', f.name, stripExtension(f.name))).join(' · ')
      : 'No hay memorias disponibles.';
  } catch {
    container.innerHTML = 'No se pudieron cargar las memorias.';
  }
}

loadDocumentos();
loadActas();
loadMemorias();

// "Copiar NIF"/"Copiar IBAN" — the only two buttons here backed by
// real data (see home-alt.html); everything in the "Logos" grid below
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
