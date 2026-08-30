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
    cachedUser = { id: me.id, roles: me.roles ?? [] };
    return true;
  } catch {
    localStorage.removeItem('token');
    token = null;
    window.location.href = '/login';
    return false;
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
  return `${day}-${month}-${date.getFullYear()}`;
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
  apiFetch,
  initSession,
  logout,
  currentUser,
  hasAnyRole,
  applyRoleVisibility,
  setToken,
  escapeHtml,
  formatDate,
  pickerLabel,
  renderPickerOptions,
  resolvePickerId,
};
