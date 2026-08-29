// Shared HTTP client used by every page. Keeps the access token in memory
// only and relies on the refresh token (in localStorage) to restore a
// session after a full page navigation, since this is a classic multi-page
// site rather than a single-page app.
const API_BASE = '/api/v1';

let accessToken = null;

async function initSession() {
  const ok = await refreshSession();
  if (!ok) {
    window.location.href = '/login';
  }
  return ok;
}

async function refreshSession() {
  const refreshToken = localStorage.getItem('refresh_token');
  if (!refreshToken) {
    return false;
  }

  const response = await fetch(`${API_BASE}/auth/refresh`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ refresh_token: refreshToken }),
  });
  if (!response.ok) {
    localStorage.removeItem('refresh_token');
    return false;
  }

  const data = await response.json();
  accessToken = data.access_token;
  localStorage.setItem('refresh_token', data.refresh_token);
  return true;
}

async function apiFetch(path, options = {}) {
  const headers = new Headers(options.headers || {});
  headers.set('Content-Type', 'application/json');
  if (accessToken) {
    headers.set('Authorization', `Bearer ${accessToken}`);
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
  const refreshToken = localStorage.getItem('refresh_token');
  localStorage.removeItem('refresh_token');
  accessToken = null;
  if (refreshToken) {
    fetch(`${API_BASE}/auth/logout`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refresh_token: refreshToken }),
    }).catch(() => {});
  }
  window.location.href = '/login';
}

// Reads the (non-sensitive) user id and role names out of the access
// token's payload, purely for client-side display and nav filtering. The
// API is the actual authority on permissions.
function currentUser() {
  if (!accessToken) {
    return null;
  }
  try {
    const payload = JSON.parse(atob(accessToken.split('.')[1]));
    return { id: payload.user_id, roles: payload.roles ?? [] };
  } catch {
    return null;
  }
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

function setAccessTokenFromLogin(token) {
  accessToken = token;
}

// Escapes text before it's interpolated into an innerHTML template, since
// several forms now let admins enter free text (descriptions, notes,
// names) that gets rendered back into tables.
function escapeHtml(value) {
  const chars = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' };
  return String(value ?? '').replace(/[&<>"']/g, (char) => chars[char]);
}

function formatDate(isoDate) {
  return new Date(isoDate).toLocaleDateString('es-ES');
}

export {
  apiFetch,
  initSession,
  logout,
  currentUser,
  hasAnyRole,
  applyRoleVisibility,
  setAccessTokenFromLogin,
  escapeHtml,
  formatDate,
};
