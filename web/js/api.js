// Shared HTTP client used by every page. Keeps the single session token
// in localStorage (there's no separate access/refresh pair anymore, so
// nothing needs restoring after a page navigation) and re-fetches the
// signed-in identity from /auth/me on every page load, since an opaque
// token has nothing a client can decode locally.
import { errorText } from './errorTexts.js';

const API_BASE = '/api/v1';

let token = localStorage.getItem('token');
let cachedUser = null;

// While an admin is impersonating somebody, their own session's token is kept here, so that they get
// that session back: on leaving the impersonation, and when it ends on its own (it lasts an hour) or is
// revoked. Any other sign-in forgets it (see setToken), so it can't be left behind to be restored later.
const ORIGINAL_TOKEN_KEY = 'katiuskas:original-token';
// A message for the next page, across the navigation that follows (sessionStorage, so it dies with the tab).
const FLASH_KEY = 'katiuskas:flash';

// The page somebody asked for when they had no session, kept (in localStorage, so it survives the
// trip through the magic link, which may open in another tab) to take them back to it after signing
// in. It expires, so an old one does not surprise somebody who signs in much later.
const RETURN_TO_KEY = 'katiuskas:return-to';
const RETURN_TO_MAX_AGE_MS = 60 * 60 * 1000;

function redirectToLogin() {
  const { pathname, search } = window.location;
  if (pathname !== '/' && pathname !== '/login') {
    try {
      localStorage.setItem(
        RETURN_TO_KEY,
        JSON.stringify({ path: pathname + search, at: Date.now() }),
      );
    } catch {
      // Without storage the person just lands on the home page after signing in.
    }
  }
  window.location.href = '/login';
}

// Where to go after signing in: the page that was asked for, if one is remembered and is a path of
// this site (never an address that would take the person elsewhere), else the home page.
function goAfterLogin() {
  let target = '/';
  try {
    const saved = JSON.parse(localStorage.getItem(RETURN_TO_KEY) ?? 'null');
    localStorage.removeItem(RETURN_TO_KEY);
    if (
      saved &&
      typeof saved.path === 'string' &&
      Date.now() - saved.at < RETURN_TO_MAX_AGE_MS &&
      /^\/(?![/\\])/.test(saved.path) &&
      !saved.path.startsWith('/login')
    ) {
      target = saved.path;
    }
  } catch {
    // Malformed or unreadable: the home page.
  }
  window.location.href = target;
}

async function initSession() {
  if (!token) {
    redirectToLogin();
    return false;
  }
  try {
    const me = await apiFetch('/auth/me');
    cachedUser = {
      login: me.login,
      firstName: me.first_name ?? '',
      lastName: me.last_name ?? '',
      roles: me.roles ?? [],
      impersonatedBy: me.impersonated_by ?? null,
      impersonationSecondsLeft: me.impersonation_seconds_left ?? null,
      // Set only while an admin acts with other roles (see /auth/role-override): their own roles and
      // how long the change has left.
      realRoles: me.real_roles ?? null,
      roleOverrideSecondsLeft: me.role_override_seconds_left ?? null,
    };
    if (cachedUser.impersonatedBy) {
      renderImpersonationBanner(cachedUser.impersonatedBy);
    } else if (cachedUser.realRoles) {
      renderRoleOverrideBanner();
    }
    const flash = sessionStorage.getItem(FLASH_KEY);
    if (flash) {
      sessionStorage.removeItem(FLASH_KEY);
      showFeedback(flash);
    }
    return true;
  } catch {
    localStorage.removeItem('token');
    token = null;
    redirectToLogin();
    return false;
  }
}

// Shown on every page while impersonating, so it's never something only
// the page that started it would know about. The impersonated person's own name
// comes with GET /auth/me, so there is nothing more to ask; the login stands in
// if a name is missing.
function renderImpersonationBanner(impersonator) {
  const selfName = `${cachedUser.firstName} ${cachedUser.lastName}`.trim() || cachedUser.login;

  const banner = document.createElement('div');
  banner.className = 'impersonation-banner';
  banner.innerHTML = `
    <span>
      Estás usando la intranet como <strong>${escapeHtml(selfName)}</strong>
      — impersonado por ${escapeHtml(impersonator.first_name ?? '')} ${escapeHtml(impersonator.last_name ?? '')}.
      <span id="impersonation-time-left" hidden>Tiempo restante: <strong></strong>; después volverás a tu sesión.</span>
    </span>
    <button type="button" id="exit-impersonation-button" class="btn btn-secondary btn-small">Salir de la impersonación</button>
  `;
  document.body.prepend(banner);
  document.getElementById('exit-impersonation-button').addEventListener('click', exitImpersonation);
  if (cachedUser.impersonationSecondsLeft != null) {
    countDownTimeLeft(cachedUser.impersonationSecondsLeft, 'impersonation-time-left');
  }
}

// Shown on every page while an admin acts with other roles than their own, to see what somebody
// with those roles sees: it says which, for how long, and has the two ways out. The dialog to choose
// the roles lives in roleOverride.js, which listens for this event.
function renderRoleOverrideBanner() {
  const banner = document.createElement('div');
  banner.className = 'impersonation-banner';
  banner.innerHTML = `
    <span>
      Estás viendo la intranet con los roles: <strong>${cachedUser.roles.map((r) => escapeHtml(roleLabel(r))).join(', ')}</strong>.
      <span id="role-override-time-left" hidden>Tiempo restante: <strong></strong>; después volverás a tus roles.</span>
    </span>
    <button type="button" id="change-role-override-button" class="btn btn-secondary btn-small">Cambiar roles</button>
    <button type="button" id="exit-role-override-button" class="btn btn-secondary btn-small">Volver a mis roles</button>
  `;
  document.body.prepend(banner);
  document
    .getElementById('change-role-override-button')
    .addEventListener('click', () =>
      document.dispatchEvent(new CustomEvent('katiuskas:role-override')),
    );
  document.getElementById('exit-role-override-button').addEventListener('click', clearRoleOverride);
  if (cachedUser.roleOverrideSecondsLeft != null) {
    countDownTimeLeft(cachedUser.roleOverrideSecondsLeft, 'role-override-time-left');
  }
}

// Puts the roles back to the admin's own and reloads: what the page showed was for the other ones.
async function clearRoleOverride() {
  const button = document.getElementById('exit-role-override-button');
  if (button) button.disabled = true;
  try {
    await apiFetch('/auth/role-override', { method: 'DELETE' });
    window.location.reload();
  } catch (err) {
    if (button) button.disabled = false;
    showError(err.message);
  }
}

// "42 min" while there is a minute or more, "35 s" in the last one.
function formatTimeLeft(ms) {
  return ms >= 60000 ? `${Math.ceil(ms / 60000)} min` : `${Math.max(0, Math.ceil(ms / 1000))} s`;
}

// An impersonation, or acting with other roles, ends on its own (its expiry is fixed), and a page that
// stayed open would go on looking like somebody else's long after it stopped being so. So the banner
// says how long is left, counting down from when the API said it (a duration, not a moment, so a
// wrong clock in this browser can't move it), and when it reaches zero the page reloads: for an
// impersonation the first request after the end is a 401, which puts the admin back in their own
// session (see restoreOriginalSession), and for other roles the API simply answers with the own ones.
function countDownTimeLeft(secondsLeft, holderId) {
  const receivedAt = Date.now();
  const holder = document.getElementById(holderId);
  const label = holder.querySelector('strong');
  holder.hidden = false;
  const tick = () => {
    // Half a second more than the API's answer: it rounds up, and reloading before the session has
    // really ended would only show the banner again, with almost nothing left.
    const left = secondsLeft * 1000 + 500 - (Date.now() - receivedAt);
    if (left <= 0) {
      window.location.reload();
      return;
    }
    label.textContent = formatTimeLeft(left);
    setTimeout(tick, left >= 60000 ? 1000 : 250);
  };
  tick();
}

// Starts an impersonation session in this browser: its token replaces the admin's, whose own is kept
// (see ORIGINAL_TOKEN_KEY). One started from inside another keeps the first admin's, which is where
// leaving, or the end, goes back to.
function startImpersonation(impersonationToken) {
  const original = cachedUser?.impersonatedBy ? localStorage.getItem(ORIGINAL_TOKEN_KEY) : token;
  setToken(impersonationToken);
  if (original) localStorage.setItem(ORIGINAL_TOKEN_KEY, original);
}

// Goes back to the admin's own session, if it was kept and still works: the impersonation session is
// ended (best effort) and the saved token takes its place. False when there is none, or it no longer
// works (it expired, was revoked, or the network failed): the caller falls back to what it did before.
async function restoreOriginalSession() {
  const original = localStorage.getItem(ORIGINAL_TOKEN_KEY);
  if (!original) return false;
  localStorage.removeItem(ORIGINAL_TOKEN_KEY);
  try {
    // Asking who it is also renews it, and sets its cookie again in place of the impersonation's.
    const response = await fetch(`${API_BASE}/auth/me`, {
      headers: { Authorization: `Bearer ${original}` },
    });
    if (!response.ok) return false;
  } catch {
    return false;
  }
  const impersonation = token;
  if (impersonation) {
    fetch(`${API_BASE}/auth/logout`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ token: impersonation }),
    }).catch(() => {});
  }
  setToken(original);
  return true;
}

async function exitImpersonation() {
  const button = document.getElementById('exit-impersonation-button');
  button.disabled = true;
  if (await restoreOriginalSession()) {
    window.location.href = '/socios';
    return;
  }
  try {
    const result = await apiFetch('/auth/impersonate/exit', { method: 'POST' });
    setToken(result.token);
    window.location.href = '/socios';
  } catch (err) {
    button.disabled = false;
    alert(err.message || 'No se ha podido salir de la impersonación.');
  }
}

// fetch, but a request that never reached the server (no network, the server down) fails with a
// message in Spanish instead of the browser's own ("Failed to fetch").
async function fetchOrExplain(url, options) {
  try {
    return await fetch(url, options);
  } catch {
    throw new Error(
      'No se ha podido conectar con el servidor. Comprueba tu conexión e inténtalo de nuevo.',
    );
  }
}

// The Error for a failed response: its message in Spanish when the API's error code is known (see
// errorTexts.js), else the API's own. The code rides along on the error, for a page that wants to say
// it in its own words instead.
async function errorOf(response) {
  const body = await response.json().catch(() => null);
  const error = new Error(
    errorText(body?.error?.code, body?.error?.message) ||
      body?.error?.message ||
      'Ha ocurrido un error inesperado.',
  );
  error.code = body?.error?.code;
  return error;
}

async function apiFetch(path, options = {}) {
  const headers = new Headers(options.headers || {});
  headers.set('Content-Type', 'application/json');
  if (token) {
    headers.set('Authorization', `Bearer ${token}`);
  }

  const response = await fetchOrExplain(`${API_BASE}${path}`, { ...options, headers });

  // An impersonation session that ended (it lasts an hour) or was revoked: back to the admin's own
  // session, with a note saying so, instead of an error — the page is reloaded as the admin.
  if (response.status === 401 && (await restoreOriginalSession())) {
    sessionStorage.setItem(FLASH_KEY, 'La impersonación ha terminado y has vuelto a tu sesión.');
    window.location.href = '/socios';
    return new Promise(() => {});
  }

  if (!response.ok) {
    throw await errorOf(response);
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
  const response = await fetchOrExplain(`${API_BASE}${path}`, {
    method: options.method || 'POST',
    headers,
    body: formData,
  });
  if (!response.ok) {
    throw await errorOf(response);
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
    throw await errorOf(response);
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
  // Signing out while impersonating ends the admin's own session too, not only the impersonation.
  const sessions = [token, localStorage.getItem(ORIGINAL_TOKEN_KEY)].filter(Boolean);
  localStorage.removeItem('token');
  localStorage.removeItem(ORIGINAL_TOKEN_KEY);
  // Signing out on purpose is not a request for the page that was open.
  localStorage.removeItem(RETURN_TO_KEY);
  token = null;
  cachedUser = null;
  for (const session of sessions) {
    fetch(`${API_BASE}/auth/logout`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ token: session }),
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
// only shows under style.css's narrow-screen media query) to open
// #sidebar as a whole — below that width the sidebar starts collapsed
// entirely (not just its own nav), and opening it overlays the current
// screen (including the topbar underneath it) instead of pushing the
// page's own content down. Tapping anywhere outside the menu closes it
// again; the toggle also still toggles (a second activation closes it
// too), which mostly matters for a keyboard/AT user tabbing to it,
// since visually it now sits covered behind the open menu (style.css
// gives .sidebar.open a higher z-index, and this element none of its
// own to rise back above it) rather than disappearing the way an
// earlier version had it. It's never actually hidden via [hidden]
// while the menu is open, unlike that earlier version — that used to
// drop it out of .topbar-brand's flex layout, visibly shifting the
// title over into the gap it left behind; staying covered instead of
// removed keeps its slot in that layout without needing it to stay
// visible. A no-op on /login, the one page with no sidebar shell to
// toggle at all.
//
// On a wide screen the menu is in the page, and can be hidden with the button at
// the top of it (the same "×" the narrow layout has to close its overlay): `.shell`
// gets `.sidebar-hidden`, which makes it look like the narrow layout (menu parked
// off-screen, hamburger showing), and the choice is remembered. There the hamburger
// doesn't overlay the menu but brings back the normal layout. Which behaviour
// applies is decided by the same width as the narrow-screen media query in
// style.css (NARROW_SCREEN, keep them in step).
const NARROW_SCREEN = '(max-width: 800px)';
const SIDEBAR_HIDDEN_KEY = 'katiuskas:sidebar-hidden';

function sidebarHiddenSaved() {
  try {
    return localStorage.getItem(SIDEBAR_HIDDEN_KEY) === '1';
  } catch {
    return false;
  }
}

function saveSidebarHidden(hidden) {
  try {
    if (hidden) localStorage.setItem(SIDEBAR_HIDDEN_KEY, '1');
    else localStorage.removeItem(SIDEBAR_HIDDEN_KEY);
  } catch {
    // Not remembered, which is all right.
  }
}

// Applied as this file loads, before the menu is drawn, so a menu the person had
// hidden doesn't flash on the page first. A no-op where there is no shell (/login).
document.querySelector('.shell')?.classList.toggle('sidebar-hidden', sidebarHiddenSaved());

function initSidebarToggle() {
  const toggle = document.getElementById('sidebar-toggle');
  const sidebar = document.getElementById('sidebar');
  const shell = document.querySelector('.shell');
  if (!toggle || !sidebar || !shell) return;
  const narrow = window.matchMedia(NARROW_SCREEN);

  // What the toggle says about the menu: on a wide screen whether it is in the page,
  // on a narrow one whether the overlay is open.
  function syncExpanded() {
    const expanded = narrow.matches
      ? sidebar.classList.contains('open')
      : !shell.classList.contains('sidebar-hidden');
    toggle.setAttribute('aria-expanded', String(expanded));
    document
      .getElementById('sidebar-close')
      ?.setAttribute('aria-label', narrow.matches ? 'Cerrar menú' : 'Ocultar menú');
  }

  function setOpen(isOpen) {
    sidebar.classList.toggle('open', isOpen);
    syncExpanded();
  }

  function setHidden(hidden) {
    shell.classList.toggle('sidebar-hidden', hidden);
    saveSidebarHidden(hidden);
    syncExpanded();
  }

  toggle.addEventListener('click', () => {
    if (narrow.matches) setOpen(!sidebar.classList.contains('open'));
    else setHidden(!shell.classList.contains('sidebar-hidden'));
  });
  document.getElementById('sidebar-close')?.addEventListener('click', () => {
    if (narrow.matches) setOpen(false);
    else setHidden(true);
  });
  document.addEventListener('click', (event) => {
    if (
      sidebar.classList.contains('open') &&
      !sidebar.contains(event.target) &&
      !toggle.contains(event.target)
    ) {
      setOpen(false);
    }
  });
  // Resizing across the breakpoint: an overlay left open has no meaning on the
  // wide layout.
  narrow.addEventListener('change', () => {
    sidebar.classList.remove('open');
    syncExpanded();
  });
  syncExpanded();
}

// Fills the shared shell's "Hola, <nombre>" greeting (#topbar-user-name
// — every logged-in page's .topbar has one, see style.css) with the
// signed-in person's first name, which GET /auth/me already gave
// (initSession) — it used to ask for the whole record just for that, a request
// that every page waited behind. A no-op on a page with no such element (there
// is none, but this keeps a stray/renamed id from throwing instead of just
// skipping the greeting). Falls back to the login if there is no name.
function loadTopbarGreeting() {
  const el = document.getElementById('topbar-user-name');
  if (!el) return;
  el.textContent = currentUser().firstName || currentUser().login;
}

// Called right after consuming a magic link, when the frontend receives
// its first (and only) session token for this sign-in.
function setToken(raw) {
  token = raw;
  localStorage.setItem('token', raw);
  // Whatever signs in forgets the admin's session kept for an impersonation (startImpersonation
  // saves it again right after, when that is what is happening).
  localStorage.removeItem(ORIGINAL_TOKEN_KEY);
}

// Escapes text before it's interpolated into an innerHTML template, since
// several forms now let admins enter free text (descriptions, notes,
// names) that gets rendered back into tables.
function escapeHtml(value) {
  const chars = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' };
  return String(value ?? '').replace(/[&<>"']/g, (char) => chars[char]);
}

// Friendly Spanish label for a system role (see docs/architecture.md) —
// anything an admin created shows as typed instead. Shared by members.js
// (role catalog table, "Nueva persona" checkboxes) and member.js (a
// person's own roles panel) — previously defined identically in both.
const knownRoleLabels = {
  admin: 'Administrador',
  board: 'Junta',
  member: 'Socio',
  former_member: 'Ex-socio',
};
function roleLabel(name) {
  return knownRoleLabels[name] ?? name;
}

// Friendly Spanish label for a Junta Directiva position. Shared by
// dashboard.js, members.js (cargoFor) and member.js — previously
// defined identically in all three.
const positionLabels = {
  president: 'Presidente',
  vice_president: 'Vicepresidente',
  secretary: 'Secretario',
  treasurer: 'Tesorero',
  member_at_large: 'Vocal',
};

// Friendly Spanish labels for an activity's type/status, shared by every
// page that lists or shows one (activities.js, activity.js) — previously
// defined identically in both.
const typeLabels = {
  caving: 'Espeleología',
  canyoning: 'Cañones',
  training: 'Formación',
  social: 'Social',
  other: 'Otro',
};

// Where an activity is, worked out from what the API sends: only `cancelled` is stored, and whether it
// is still to come, going on or over follows from its dates and today's. The result is also the suffix of
// the badge's CSS class (`.badge.status-<state>`).
const activityStateLabels = {
  planned: 'Planificada',
  ongoing: 'En curso',
  completed: 'Realizada',
  cancelled: 'Cancelada',
};
function activityState(activity) {
  if (activity.cancelled) return 'cancelled';
  const now = new Date();
  const pad = (n) => String(n).padStart(2, '0');
  const today = `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`;
  if (activity.end_date.slice(0, 10) < today) return 'completed';
  if (activity.start_date.slice(0, 10) > today) return 'planned';
  return 'ongoing';
}

// The success banner every page provides via its own #feedback <p> (see
// e.g. socios.html) — previously copy-pasted verbatim into every page script
// instead of living here. Looked up by id on every call rather than cached at
// module load, so it keeps working regardless of when a page's own script
// runs relative to the element existing in the DOM. Errors don't use a banner:
// see showError below.
function showFeedback(message) {
  const feedbackBox = document.getElementById('feedback');
  if (feedbackBox) {
    feedbackBox.textContent = message;
    feedbackBox.hidden = false;
  }
}

// An error has to be seen: written to a box at the top of the page it could
// be far from the button that was pressed, or just go unnoticed. So it goes
// into a modal <dialog> of its own, in the top layer over everything —
// including another dialog that happens to be open, which it leaves dimmed
// and inert underneath — behind a dark backdrop, and nothing else can be
// operated until it is closed (the "Cerrar" button or Escape). Created the first time it's needed, so no page carries markup
// for it; a second error while it's open replaces the message.
function errorDialog() {
  let dialog = document.getElementById('error-dialog');
  if (dialog) return dialog;
  dialog = document.createElement('dialog');
  dialog.id = 'error-dialog';
  dialog.className = 'panel modal error-modal';
  dialog.setAttribute('role', 'alertdialog');
  dialog.setAttribute('aria-labelledby', 'error-dialog-title');
  dialog.innerHTML = `
    <h3 id="error-dialog-title">Ha ocurrido un error</h3>
    <p id="error-dialog-message"></p>
    <div class="panel-actions">
      <button type="button" class="btn btn-primary" data-error-close>Cerrar</button>
    </div>`;
  dialog.querySelector('[data-error-close]').addEventListener('click', () => dialog.close());
  document.body.append(dialog);
  return dialog;
}

// What a page does when the thing it is about does not exist — an address like /actividad?id=foo or
// /socio?login=nadie, whatever the kind of page: the same as an address that is not a page at all
// (404.html): the content is replaced by "Esta página no existe", with no redirect and no modal.
// The page's own elements stay (hidden), so the code that is still running does not trip over them,
// and from then on showError says nothing: the requests of the page that fail as well (they all
// refer to what is not there) would only pile modals on top of it.
let notFoundShown = false;

// A positive whole number from the address (?id=12), or null when it is missing or anything else.
function queryId(name = 'id') {
  const value = new URLSearchParams(window.location.search).get(name);
  return /^[1-9]\d*$/.test(value ?? '') ? Number(value) : null;
}

// Whether an error of the API says that what was asked for is not there (or cannot be: an id
// that is not a number).
function isNotFound(err) {
  return err?.code === 'not_found' || err?.code === 'invalid_id';
}

function showNotFound() {
  if (notFoundShown) return;
  notFoundShown = true;
  document.getElementById('error-dialog')?.close();
  const main = document.querySelector('main.content');
  if (!main) return;
  for (const child of main.children) child.hidden = true;
  const notFound = document.createElement('div');
  notFound.id = 'not-found';
  notFound.innerHTML = `
    <h1>Esta página no existe</h1>
    <p>
      La dirección no corresponde a nada que exista: puede que el enlace esté mal escrito o que lo
      que buscas se haya borrado. Usa el menú para ir a otra página, o <a href="/">vuelve al
      inicio</a>.
    </p>`;
  main.append(notFound);
}

// showNotFound for a page whose own code must not go on (nothing it would draw exists): the
// promise never settles, so the rest of the module does not run.
function stopAsNotFound() {
  showNotFound();
  return new Promise(() => {});
}

function showError(message) {
  if (notFoundShown) return;
  const feedbackBox = document.getElementById('feedback');
  if (feedbackBox) feedbackBox.hidden = true;
  const dialog = errorDialog();
  dialog.querySelector('#error-dialog-message').textContent = message;
  if (!dialog.open) {
    // showModal() would otherwise send the page back to the top (as openModal says).
    const { scrollX, scrollY } = window;
    dialog.showModal();
    window.scrollTo(scrollX, scrollY);
  }
}

function formatDate(isoDate) {
  const date = new Date(isoDate);
  const day = String(date.getDate()).padStart(2, '0');
  const month = String(date.getMonth() + 1).padStart(2, '0');
  return `${day}-${month}-${date.getFullYear()}`;
}

const MONTHS = [
  'Enero',
  'Febrero',
  'Marzo',
  'Abril',
  'Mayo',
  'Junio',
  'Julio',
  'Agosto',
  'Septiembre',
  'Octubre',
  'Noviembre',
  'Diciembre',
];

// "Enero de 2026": just the month and the year, for what is placed in time loosely.
function formatMonthYear(iso) {
  const date = new Date(iso);
  return `${MONTHS[date.getMonth()]} de ${date.getFullYear()}`;
}

// "21-10-2026 18:05": the date and the time (the viewer's own), for what has a moment.
function formatDateTime(iso) {
  const date = new Date(iso);
  const time = `${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}`;
  return `${formatDate(iso)} ${time}`;
}

// The club's own money (internal/accounting) is always euros — shared by
// accounting.js's chart-of-accounts balances and, later, its own
// transactions page.
function formatCurrency(amount) {
  // useGrouping "always": the es-ES locale by default only groups
  // thousands from five digits up (10.000,00 but 1000,00), so a 4-digit
  // amount would sit next to a grouped balance without its separator.
  return `${Number(amount).toLocaleString('es-ES', { minimumFractionDigits: 2, maximumFractionDigits: 2, useGrouping: 'always' })} €`;
}

// Lowercase and without accents, so a search for "cañones" and one for
// "canones" find the same things.
function normalizeForSearch(value) {
  return (value ?? '')
    .toString()
    .normalize('NFD')
    .replace(/[\u0300-\u036f]/g, '')
    .toLowerCase();
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

// How the people of a list read in a picker: just "Nombre Apellidos", and "#login" after it only
// for those who share their name with somebody else in the list (so the two can be told apart and
// what is typed still resolves to one person). Gets the whole list the pickers are made from and
// returns what to write for each person.
function personPickerLabeler(people) {
  const key = (p) => `${p.first_name} ${p.last_name}`.trim().toLowerCase();
  const count = new Map();
  for (const p of people) count.set(key(p), (count.get(key(p)) ?? 0) + 1);
  return (p) => {
    const name = `${p.first_name} ${p.last_name}`;
    return count.get(key(p)) > 1 ? pickerLabel(name, p.login) : name;
  };
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
  if (match) return match.id;
  // What was typed without choosing from the menu still counts when it leaves one single person, ignoring
  // accents and case ("garcia" for "García").
  const words = normalizeForSearch(value).split(/\s+/).filter(Boolean);
  if (words.length === 0) return null;
  const found = entries.filter((e) => words.every((w) => normalizeForSearch(e.label).includes(w)));
  return found.length === 1 ? found[0].id : null;
}

export {
  API_BASE,
  goAfterLogin,
  normalizeForSearch,
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
  startImpersonation,
  clearRoleOverride,
  escapeHtml,
  encodeFilePath,
  knownRoleLabels,
  roleLabel,
  positionLabels,
  typeLabels,
  activityState,
  activityStateLabels,
  showFeedback,
  showError,
  queryId,
  isNotFound,
  showNotFound,
  stopAsNotFound,
  formatDate,
  formatMonthYear,
  formatDateTime,
  formatCurrency,
  pickerLabel,
  personPickerLabeler,
  renderPickerOptions,
  resolvePickerId,
};
