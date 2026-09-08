// Shared HTTP client used by every page. Keeps the single session token
// in localStorage (there's no separate access/refresh pair anymore, so
// nothing needs restoring after a page navigation) and re-fetches the
// signed-in identity from /auth/me on every page load, since an opaque
// token has nothing a client can decode locally.
const API_BASE = '/api/v1';

let token = localStorage.getItem('token');
let cachedUser = null;

async function initSession() {
  if (!token) {
    window.location.href = '/login';
    return false;
  }
  try {
    const me = await apiFetch('/auth/me');
    cachedUser = { login: me.login, roles: me.roles ?? [], impersonatedBy: me.impersonated_by ?? null };
    if (cachedUser.impersonatedBy) {
      await renderImpersonationBanner(cachedUser.impersonatedBy);
    }
    return true;
  } catch {
    localStorage.removeItem('token');
    token = null;
    window.location.href = '/login';
    return false;
  }
}

// Shown on every page while impersonating, so it's never something only
// the page that started it would know about. Best-effort: if the extra
// lookup for the impersonated person's own name fails, the banner still
// shows with what it already knows rather than not showing at all.
async function renderImpersonationBanner(impersonator) {
  let selfName = cachedUser.login;
  try {
    const self = await apiFetch(`/users/${cachedUser.login}`);
    selfName = `${self.first_name} ${self.last_name}`;
  } catch {
    // Keep the login-only fallback above.
  }

  const banner = document.createElement('div');
  banner.className = 'impersonation-banner';
  banner.innerHTML = `
    <span>
      Estás usando la intranet como <strong>${escapeHtml(selfName)}</strong>
      — impersonado por ${escapeHtml(impersonator.first_name ?? '')} ${escapeHtml(impersonator.last_name ?? '')}.
    </span>
    <button type="button" id="exit-impersonation-button" class="btn btn-secondary btn-small">Salir de la impersonación</button>
  `;
  document.body.prepend(banner);
  document.getElementById('exit-impersonation-button').addEventListener('click', exitImpersonation);
}

async function exitImpersonation() {
  const button = document.getElementById('exit-impersonation-button');
  button.disabled = true;
  try {
    const result = await apiFetch('/auth/impersonate/exit', { method: 'POST' });
    setToken(result.token);
    window.location.href = '/members';
  } catch (err) {
    button.disabled = false;
    alert(err.message || 'No se ha podido salir de la impersonación.');
  }
}

async function apiFetch(path, options = {}) {
  const headers = new Headers(options.headers || {});
  headers.set('Content-Type', 'application/json');
  if (token) {
    headers.set('Authorization', `Bearer ${token}`);
  }

  const response = await fetch(`${API_BASE}${path}`, { ...options, headers });

  if (!response.ok) {
    const body = await response.json().catch(() => null);
    throw new Error(body?.error?.message || 'Ha ocurrido un error inesperado.');
  }
  if (response.status === 204) {
    return null;
  }
  return response.json();
}

// Like apiFetch, but for a multipart/form-data body (a file upload) —
// the browser has to set its own Content-Type header with the
// multipart boundary itself, so this must never set one the way
// apiFetch's own Content-Type: application/json does.
async function apiFetchForm(path, formData, options = {}) {
  const headers = new Headers();
  if (token) {
    headers.set('Authorization', `Bearer ${token}`);
  }
  const response = await fetch(`${API_BASE}${path}`, { method: options.method || 'POST', headers, body: formData });
  if (!response.ok) {
    const body = await response.json().catch(() => null);
    throw new Error(body?.error?.message || 'Ha ocurrido un error inesperado.');
  }
  if (response.status === 204) {
    return null;
  }
  return response.json();
}

// Like apiFetch, but for an endpoint that returns raw bytes (a document
// library file) instead of JSON — there's no session cookie this app
// could rely on for a plain <a href>, so downloading or previewing a
// file means fetching it with the Authorization header by hand and
// working with the resulting Blob client-side.
async function apiFetchBlob(path) {
  const headers = new Headers();
  if (token) {
    headers.set('Authorization', `Bearer ${token}`);
  }
  const response = await fetch(`${API_BASE}${path}`, { headers });
  if (!response.ok) {
    const body = await response.json().catch(() => null);
    throw new Error(body?.error?.message || 'Ha ocurrido un error inesperado.');
  }
  return response.blob();
}

// Encodes a slash-separated file path for use as URL path segments —
// GET /library/file/<path> and GET /users/{login}/files/file/<path>
// take the file's path as part of the URL itself (a {path...} wildcard
// server-side), not a ?path= query parameter, so each segment needs
// its own escaping (spaces, accents, "?"...) while the slashes between
// them stay literal.
function encodeFilePath(relPath) {
  return relPath.split('/').map(encodeURIComponent).join('/');
}

function logout() {
  const current = token;
  localStorage.removeItem('token');
  token = null;
  cachedUser = null;
  if (current) {
    fetch(`${API_BASE}/auth/logout`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ token: current }),
    }).catch(() => {});
  }
  window.location.href = '/login';
}

// Returns the identity and roles fetched by initSession(), purely for
// client-side display and nav filtering. The API is the actual authority
// on permissions.
function currentUser() {
  return cachedUser;
}

// Whether the given user holds at least one of the named roles.
function hasAnyRole(user, names) {
  return !!user && user.roles.some((role) => names.includes(role));
}

// Removes any element marked with data-roles="admin,board" unless the
// current user holds at least one of those roles.
function applyRoleVisibility() {
  const user = currentUser();
  document.querySelectorAll('[data-roles]').forEach((el) => {
    if (!hasAnyRole(user, el.dataset.roles.split(','))) {
      el.remove();
    }
  });
}

// Wires #sidebar-toggle (the hamburger button in .topbar-brand, CSS
// only shows under style.css's narrow-screen media query) to open/close
// #sidebar as a whole — below that width the sidebar starts collapsed
// entirely (not just its own nav), and opening it overlays the current
// screen instead of pushing the page's own content down. A no-op on
// /login, the one page with no sidebar shell to toggle at all.
function initSidebarToggle() {
  const toggle = document.getElementById('sidebar-toggle');
  const sidebar = document.getElementById('sidebar');
  if (!toggle || !sidebar) return;
  toggle.addEventListener('click', () => {
    const isOpen = sidebar.classList.toggle('open');
    toggle.setAttribute('aria-expanded', String(isOpen));
    toggle.textContent = isOpen ? '✕' : '☰';
  });
}

// Fills the shared shell's "Hola, <nombre>" greeting (#topbar-user-name
// — every logged-in page's .topbar has one, see style.css) with the
// signed-in person's first name. A no-op on a page with no such element
// (there is none, but this keeps a stray/renamed id from throwing
// instead of just skipping the greeting). Falls back to the login if
// the profile fetch fails — still identifies who's signed in, just less
// friendly than a first name.
async function loadTopbarGreeting() {
  const el = document.getElementById('topbar-user-name');
  if (!el) return;
  try {
    const profile = await apiFetch(`/users/${currentUser().login}`);
    el.textContent = profile.first_name;
  } catch {
    el.textContent = currentUser().login;
  }
}

// Called right after consuming a magic link, when the frontend receives
// its first (and only) session token for this sign-in.
function setToken(raw) {
  token = raw;
  localStorage.setItem('token', raw);
}

// Escapes text before it's interpolated into an innerHTML template, since
// several forms now let admins enter free text (descriptions, notes,
// names) that gets rendered back into tables.
function escapeHtml(value) {
  const chars = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' };
  return String(value ?? '').replace(/[&<>"']/g, (char) => chars[char]);
}

function formatDate(isoDate) {
  const date = new Date(isoDate);
  const day = String(date.getDate()).padStart(2, '0');
  const month = String(date.getMonth() + 1).padStart(2, '0');
  return `${day}/${month}/${date.getFullYear()}`;
}

// A plain <select> stops being usable once there are hundreds of
// people or activities to choose from, especially with no obvious
// ordering. These three helpers back a searchable alternative instead:
// a text <input list="..."> paired with a <datalist>, which gives free
// incremental filtering as the person types, for the price of a couple
// of small utility functions instead of a whole new widget.
//
// Each option's visible label embeds a "#<id>" suffix a person is
// unlikely to type themselves, so a search that narrows down to one
// match can still be resolved back to an id unambiguously even when two
// entries would otherwise look identical (two people with the same
// name, two activities with the same title).
function pickerLabel(text, id) {
  return `${text} #${id}`;
}

function renderPickerOptions(entries) {
  return entries.map((e) => `<option value="${escapeHtml(e.label)}"></option>`).join('');
}

// Resolves what's currently typed in the picker input back to an id, or
// null if it doesn't exactly match one of the offered options (so a
// half-typed search can't be submitted as if it were a real pick).
function resolvePickerId(entries, typedValue) {
  const value = (typedValue ?? '').trim();
  const match = entries.find((e) => e.label === value);
  return match ? match.id : null;
}

export {
  API_BASE,
  apiFetch,
  apiFetchBlob,
  apiFetchForm,
  initSession,
  logout,
  currentUser,
  hasAnyRole,
  applyRoleVisibility,
  initSidebarToggle,
  loadTopbarGreeting,
  setToken,
  escapeHtml,
  encodeFilePath,
  formatDate,
  pickerLabel,
  renderPickerOptions,
  resolvePickerId,
};
