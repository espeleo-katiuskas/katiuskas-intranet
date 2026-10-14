import {
  apiFetch,
  escapeHtml,
  formatDate,
  roleLabel,
  showFeedback,
  showError,
} from './api.js';
import { initPage } from './layout.js';
import { initModal, openModal } from './modal.js';

// Everything here is for administrators only — the nav link is hidden from
// anyone else and anyone else who lands here is sent home. More sections
// will join the list of roles.
await initPage({ requireAnyRole: ['admin'] });

// "Roles": the catalog of roles — the system ones that give permissions and
// the free-form labels an admin creates (see GET/POST/DELETE /roles).
async function loadRoles() {
  const tableBody = document.getElementById('roles-table-body');
  try {
    const roles = await apiFetch('/roles');
    tableBody.innerHTML = roles
      .map((role) => `
        <tr>
          <td>${escapeHtml(roleLabel(role.name))}</td>
          <td>${escapeHtml(role.description) || '—'}</td>
          <td>
            ${role.is_system ? '<span class="badge">Del sistema</span>' : ''}
            ${role.is_automatic ? '<span class="badge" title="Se asigna solo, según el historial de socio">Automático</span>' : ''}
          </td>
          <td>${!role.is_system ? `<button type="button" class="btn btn-danger btn-small" data-delete-role="${role.id}">Borrar</button>` : ''}</td>
        </tr>
      `)
      .join('');
  } catch {
    tableBody.innerHTML = '<tr><td colspan="4">No se pudieron cargar los roles.</td></tr>';
  }
}

document.getElementById('roles-table-body').addEventListener('click', async (event) => {
  const button = event.target.closest('button[data-delete-role]');
  if (!button) return;
  if (!confirm('¿Borrar este rol? Se quitará de todas las personas que lo tengan.')) return;

  button.disabled = true;
  try {
    await apiFetch(`/roles/${button.dataset.deleteRole}`, { method: 'DELETE' });
    showFeedback('Rol borrado.');
    await loadRoles();
  } catch (err) {
    button.disabled = false;
    showError(err.message);
  }
});

const newRoleDialog = document.getElementById('new-role-dialog');
const newRoleForm = document.getElementById('new-role-form');
initModal(newRoleDialog, newRoleForm);

document.getElementById('toggle-new-role').addEventListener('click', () => {
  openModal(newRoleDialog);
});

newRoleForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  const formData = new FormData(newRoleForm);
  const body = {
    name: formData.get('name'),
    description: formData.get('description')?.toString().trim() || null,
  };

  const submitButton = newRoleForm.querySelector('button[type="submit"]');
  submitButton.disabled = true;
  try {
    await apiFetch('/roles', { method: 'POST', body: JSON.stringify(body) });
    showFeedback('Rol creado.');
    newRoleDialog.close();
    await loadRoles();
  } catch (err) {
    showError(err.message);
  } finally {
    submitButton.disabled = false;
  }
});

await loadRoles();

// "Sesiones": every session token, to see who is signed in and cut one off.
// GET /tokens lists them (the active ones, or all with the checkbox) and
// PATCH/DELETE /users/{login}/tokens/{id} change or revoke one. A page at a
// time, asking for one more than a page to know whether "Ver más" is needed.
const SESSIONS_PAGE_SIZE = 50;
const sessionsBody = document.getElementById('sessions-table-body');
const sessionsShowAll = document.getElementById('sessions-show-all');
const sessionsLoadMore = document.getElementById('sessions-load-more');
let sessionsShown = [];
let namesByLogin = {};

function formatDateTime(iso) {
  const date = new Date(iso);
  const time = `${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}`;
  return `${formatDate(iso)} ${time}`;
}

function sessionStatus(session) {
  if (session.revoked_at) return 'Revocada';
  if (new Date(session.expires_at) <= new Date()) return 'Caducada';
  return 'Activa';
}

function renderSessionRow(session) {
  const status = sessionStatus(session);
  const name = namesByLogin[session.user_login];
  const person = `<a href="/member?login=${encodeURIComponent(session.user_login)}">${escapeHtml(name ?? session.user_login)}</a>`;
  const own = session.current
    ? ' <span class="badge" title="La sesión que estás usando ahora mismo">Esta sesión</span>'
    : '';
  const impersonation = session.impersonated_by_login
    ? ` <span class="badge" title="Sesión abierta al impersonar a esta persona">Impersonada por ${escapeHtml(session.impersonated_by_login)}</span>`
    : '';
  const actions = status === 'Activa'
    ? `<div class="table-actions">
         <button type="button" class="btn btn-secondary btn-small" data-session-expiry="${session.id}">Caducidad</button>
         <button type="button" class="btn btn-danger btn-small" data-session-revoke="${session.id}">Revocar</button>
       </div>`
    : '';
  return `
    <tr>
      <td>${person}${own}${impersonation}</td>
      <td>${session.last_used_at ? formatDateTime(session.last_used_at) : 'Nunca'}</td>
      <td>${session.last_used_ip ? escapeHtml(session.last_used_ip) : '—'}</td>
      <td>${formatDateTime(session.created_at)}</td>
      <td>${formatDateTime(session.expires_at)}</td>
      <td>${status}</td>
      <td>${actions}</td>
    </tr>
  `;
}

function renderSessions() {
  sessionsBody.innerHTML = sessionsShown.length
    ? sessionsShown.map(renderSessionRow).join('')
    : '<tr><td colspan="7">No hay sesiones.</td></tr>';
}

async function loadSessions({ append = false } = {}) {
  if (!append) sessionsShown = [];
  sessionsLoadMore.disabled = true;
  try {
    const status = sessionsShowAll.checked ? 'all' : 'active';
    const fetched = await apiFetch(`/tokens?status=${status}&limit=${SESSIONS_PAGE_SIZE + 1}&offset=${sessionsShown.length}`);
    sessionsShown = sessionsShown.concat(fetched.slice(0, SESSIONS_PAGE_SIZE));
    renderSessions();
    sessionsLoadMore.hidden = fetched.length <= SESSIONS_PAGE_SIZE;
  } catch (err) {
    sessionsLoadMore.hidden = true;
    sessionsBody.innerHTML = '<tr><td colspan="7">No se pudieron cargar las sesiones.</td></tr>';
    showError(err.message);
  } finally {
    sessionsLoadMore.disabled = false;
  }
}

sessionsShowAll.addEventListener('change', () => loadSessions());
sessionsLoadMore.addEventListener('click', () => loadSessions({ append: true }));

const expiryDialog = document.getElementById('session-expiry-dialog');
const expiryForm = document.getElementById('session-expiry-form');
const expiryInput = document.getElementById('session-expiry-input');
initModal(expiryDialog, expiryForm);
let expiringSession = null;

// datetime-local wants "YYYY-MM-DDTHH:MM" in the viewer's own time.
function toLocalInputValue(iso) {
  const date = new Date(iso);
  const pad = (n) => String(n).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

sessionsBody.addEventListener('click', async (event) => {
  const revoke = event.target.closest('button[data-session-revoke]');
  const expiry = event.target.closest('button[data-session-expiry]');
  if (revoke) {
    const session = sessionsShown.find((s) => s.id === Number(revoke.dataset.sessionRevoke));
    if (!session) return;
    const question = session.current
      ? 'Esta es la sesión que estás usando: si la revocas se cerrará y tendrás que volver a entrar. ¿Revocarla?'
      : `¿Revocar esta sesión de ${namesByLogin[session.user_login] ?? session.user_login}? Se cerrará en el acto.`;
    if (!confirm(question)) return;
    revoke.disabled = true;
    try {
      await apiFetch(`/users/${encodeURIComponent(session.user_login)}/tokens/${session.id}`, { method: 'DELETE' });
      showFeedback('Sesión revocada.');
      await loadSessions();
    } catch (err) {
      revoke.disabled = false;
      showError(err.message);
    }
  } else if (expiry) {
    expiringSession = sessionsShown.find((s) => s.id === Number(expiry.dataset.sessionExpiry));
    if (!expiringSession) return;
    document.getElementById('session-expiry-hint').textContent =
      `Sesión de ${namesByLogin[expiringSession.user_login] ?? expiringSession.user_login}, que caduca ahora el ${formatDateTime(expiringSession.expires_at)}.`;
    expiryInput.value = toLocalInputValue(expiringSession.expires_at);
    openModal(expiryDialog);
  }
});

expiryForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  if (!expiringSession) return;
  const submitButton = expiryForm.querySelector('button[type="submit"]');
  submitButton.disabled = true;
  try {
    await apiFetch(`/users/${encodeURIComponent(expiringSession.user_login)}/tokens/${expiringSession.id}`, {
      method: 'PATCH',
      body: JSON.stringify({ expires_at: new Date(expiryInput.value).toISOString() }),
    });
    showFeedback('Caducidad cambiada.');
    expiryDialog.close();
    await loadSessions();
  } catch (err) {
    showError(err.message);
  } finally {
    submitButton.disabled = false;
  }
});

// People's names come from the directory, open to every signed-in user.
try {
  const directory = await apiFetch('/users/directory');
  namesByLogin = Object.fromEntries(directory.map((p) => [p.login, `${p.first_name} ${p.last_name}`]));
} catch {
  // Without names the logins show instead.
}

// "Estado del sistema": a read-only picture from GET /admin/status — what is
// deployed, whether the database answers and how far its migrations are,
// whether email and Telegram are set up, the timeouts in force, whether the
// file directories exist. Nothing here can be changed from the page.
const statusContainer = document.getElementById('status-container');

// 90 → "1 minuto y medio" would be cute; plain and exact is enough here.
function formatDuration(seconds) {
  const units = [[86400, 'día', 'días'], [3600, 'hora', 'horas'], [60, 'minuto', 'minutos']];
  for (const [size, one, many] of units) {
    if (seconds >= size && seconds % size === 0) {
      const n = seconds / size;
      return `${n} ${n === 1 ? one : many}`;
    }
  }
  return `${seconds} segundos`;
}

function ago(iso) {
  const minutes = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 60000));
  if (minutes < 60) return `hace ${minutes} min`;
  if (minutes < 2 * 24 * 60) return `hace ${Math.round(minutes / 60)} h`;
  return `hace ${Math.round(minutes / (24 * 60))} días`;
}

function statusGroup(title, rows) {
  const lines = rows
    .map(([label, value]) => `<div class="detail-row"><span>${escapeHtml(label)}</span><span>${value}</span></div>`)
    .join('');
  return `<h3>${escapeHtml(title)}</h3><div class="detail-list">${lines}</div>`;
}

const yesNo = (value) => (value ? 'Sí' : 'No');

function renderStatus(status) {
  const app = status.app;
  const revision = app.vcs_revision
    ? `${escapeHtml(app.vcs_revision.slice(0, 8))}${app.vcs_modified ? ' (con cambios sin commitear)' : ''}${app.vcs_time ? `, del ${formatDate(app.vcs_time)}` : ''}`
    : '—';
  const db = status.database;
  const settings = status.settings;
  const storageLabels = { library: 'Biblioteca', user_files: 'Ficheros de las personas', activity_resources: 'Memorias de actividades' };

  statusContainer.innerHTML = [
    statusGroup('Aplicación', [
      ['Versión', app.version ? escapeHtml(app.version) : 'Sin versión (se indica al compilar: <code>make build</code>, o <code>--build-arg VERSION</code> en Docker)'],
      ['Commit', revision],
      ['Go', escapeHtml(app.go_version)],
      ['Arrancó', `${formatDateTime(app.started_at)} (${ago(app.started_at)})`],
    ]),
    statusGroup('Base de datos', [
      ['Estado', db.ok ? `Responde (${db.latency_ms} ms)` : `<strong>No responde</strong>: ${escapeHtml(db.error ?? '')}`],
      ['Última migración aplicada', db.migration_version != null ? String(db.migration_version) : '—'],
    ]),
    statusGroup('Correo', [
      ['SMTP configurado', status.mail.smtp_configured ? 'Sí' : 'No: los enlaces de acceso solo se escriben en el log del servidor'],
    ]),
    statusGroup('Telegram', [
      ['Canal de logs configurado', yesNo(status.telegram.log_channel_configured)],
      ['Bot en marcha', status.telegram.bot_running ? `Sí${status.telegram.bot_username ? ` (@${escapeHtml(status.telegram.bot_username)})` : ''}` : 'No'],
    ]),
    statusGroup('Ajustes', [
      ['Dirección pública', escapeHtml(settings.public_base_url)],
      ['Duración de una sesión', formatDuration(settings.session_ttl_seconds)],
      ['Validez del enlace de acceso', formatDuration(settings.magic_link_ttl_seconds)],
      ['Espera entre dos enlaces', formatDuration(settings.magic_link_cooldown_seconds)],
      ['Validez del enlace enviado por el bot', formatDuration(settings.bot_dm_link_ttl_seconds)],
      ['Duración de una impersonación', formatDuration(settings.impersonation_ttl_seconds)],
    ]),
    statusGroup('Ficheros', status.storage.map((dir) => [
      storageLabels[dir.name] ?? dir.name,
      `<code>${escapeHtml(dir.path)}</code> — ${dir.exists ? 'existe' : '<strong>no existe</strong>'}`,
    ])),
  ].join('');
}

async function loadStatus() {
  try {
    renderStatus(await apiFetch('/admin/status'));
  } catch (err) {
    statusContainer.textContent = 'No se pudo cargar el estado del sistema.';
    showError(err.message);
  }
}

document.getElementById('refresh-status').addEventListener('click', loadStatus);

await loadStatus();
await loadSessions();
