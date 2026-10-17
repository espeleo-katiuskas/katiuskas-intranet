import {
  apiFetch,
  currentUser,
  escapeHtml,
  formatDate,
  pickerLabel,
  resolvePickerId,
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
// the free-form labels an admin creates (see GET/POST/DELETE /roles) — and who
// has each one. The holders come from GET /users (admin sees everybody, with
// their roles), and a role is given or taken with POST/DELETE
// /users/{login}/roles. The automatic roles (member, board, former_member)
// follow the person's history, so those only show who has them.
let roleCatalog = [];
let people = [];
const expandedRoles = new Set();
const roleBody = document.getElementById('roles-table-body');
const viewerLogin = currentUser().login;

const fullName = (person) => `${person.first_name} ${person.last_name}`;
const holdersOf = (role) => people.filter((p) => p.roles.includes(role.name));

function holderChip(role, person) {
  const name = escapeHtml(fullName(person));
  const link = `<a href="/socio?login=${encodeURIComponent(person.login)}">${name}</a>`;
  if (role.is_automatic) return `<span class="badge">${link}</span>`;
  // The API refuses taking your own admin away (409 self_admin_removal); the
  // button just isn't offered, as on /socio.
  if (role.name === 'admin' && person.login === viewerLogin) {
    return `<span class="badge" title="No puedes quitarte a ti mismo el rol de admin">${link}</span>`;
  }
  return `<span class="badge-removable">${link}<button type="button" data-unassign-role="${role.id}" data-login="${escapeHtml(person.login)}" title="Quitar este rol">×</button></span>`;
}

function roleDetail(role) {
  const holders = holdersOf(role);
  const list = holders.length
    ? holders.map((p) => holderChip(role, p)).join('')
    : '<p class="hint">Nadie tiene este rol.</p>';
  let add = '';
  if (role.is_automatic) {
    add = '<p class="hint">Se asigna solo, según el historial de socio: no se da ni se quita a mano.</p>';
  } else {
    const candidates = people.filter((p) => !p.roles.includes(role.name));
    const options = candidates
      .map((p) => `<option value="${escapeHtml(pickerLabel(fullName(p), p.login))}"></option>`)
      .join('');
    add = `
      <div class="role-add-row">
        <input type="text" list="role-people-${role.id}" data-role-search="${role.id}" autocomplete="off" placeholder="Escribe un nombre…">
        <datalist id="role-people-${role.id}">${options}</datalist>
        <button type="button" class="btn btn-secondary btn-small" data-assign-role="${role.id}">Dar este rol</button>
      </div>`;
  }
  return `<div class="role-holders">${list}</div>${add}`;
}

function renderRoles() {
  roleBody.innerHTML = roleCatalog
    .map((role) => {
      const open = expandedRoles.has(role.id);
      const count = holdersOf(role).length;
      const row = `
        <tr>
          <td>${escapeHtml(roleLabel(role.name))}</td>
          <td>${escapeHtml(role.description) || '—'}</td>
          <td>
            ${count}
            <button type="button" class="link-button" data-toggle-role="${role.id}">${open ? 'Ocultar' : 'Ver'}</button>
          </td>
          <td>
            ${role.is_system ? '<span class="badge">Del sistema</span>' : ''}
            ${role.is_automatic ? '<span class="badge" title="Se asigna solo, según el historial de socio">Automático</span>' : ''}
          </td>
          <td>${!role.is_system ? `<button type="button" class="btn btn-danger btn-small" data-delete-role="${role.id}">Borrar</button>` : ''}</td>
        </tr>`;
      const detail = open ? `<tr class="role-detail"><td colspan="5">${roleDetail(role)}</td></tr>` : '';
      return row + detail;
    })
    .join('');
}

async function loadRoles() {
  try {
    [roleCatalog, people] = await Promise.all([apiFetch('/roles'), apiFetch('/users')]);
    people.sort((a, b) => `${a.last_name} ${a.first_name}`.localeCompare(`${b.last_name} ${b.first_name}`, 'es'));
    renderRoles();
  } catch {
    roleBody.innerHTML = '<tr><td colspan="5">No se pudieron cargar los roles.</td></tr>';
  }
}

roleBody.addEventListener('click', async (event) => {
  const toggle = event.target.closest('button[data-toggle-role]');
  const remove = event.target.closest('button[data-delete-role]');
  const unassign = event.target.closest('button[data-unassign-role]');
  const assign = event.target.closest('button[data-assign-role]');

  if (toggle) {
    const id = Number(toggle.dataset.toggleRole);
    if (expandedRoles.has(id)) expandedRoles.delete(id);
    else expandedRoles.add(id);
    renderRoles();
  } else if (remove) {
    if (!confirm('¿Borrar este rol? Se quitará de todas las personas que lo tengan.')) return;
    remove.disabled = true;
    try {
      await apiFetch(`/roles/${remove.dataset.deleteRole}`, { method: 'DELETE' });
      expandedRoles.delete(Number(remove.dataset.deleteRole));
      showFeedback('Rol borrado.');
      await loadRoles();
    } catch (err) {
      remove.disabled = false;
      showError(err.message);
    }
  } else if (unassign) {
    unassign.disabled = true;
    try {
      await apiFetch(`/users/${encodeURIComponent(unassign.dataset.login)}/roles/${unassign.dataset.unassignRole}`, { method: 'DELETE' });
      showFeedback('Rol quitado.');
      await loadRoles();
    } catch (err) {
      unassign.disabled = false;
      showError(err.message);
    }
  } else if (assign) {
    const roleId = Number(assign.dataset.assignRole);
    const input = roleBody.querySelector(`input[data-role-search="${roleId}"]`);
    const entries = people.map((p) => ({ id: p.login, label: pickerLabel(fullName(p), p.login) }));
    const login = resolvePickerId(entries, input.value);
    if (!login) {
      showError('Elige a una persona de la lista.');
      return;
    }
    assign.disabled = true;
    try {
      await apiFetch(`/users/${encodeURIComponent(login)}/roles`, { method: 'POST', body: JSON.stringify({ role_id: roleId }) });
      showFeedback('Rol dado.');
      await loadRoles();
    } catch (err) {
      assign.disabled = false;
      showError(err.message);
    }
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
  const person = `<a href="/socio?login=${encodeURIComponent(session.user_login)}">${escapeHtml(name ?? session.user_login)}</a>`;
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
  const storageLabels = { library: 'Archivo', user_files: 'Ficheros de las personas', activity_resources: 'Memorias de actividades' };

  statusContainer.innerHTML = [
    statusGroup('Aplicación', [
      ['Versión', app.version ? escapeHtml(app.version) : 'Sin versión (el binario no se compiló desde una copia de git)'],
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

// "Festivos": the club holidays the activities calendar tints, one year at a
// time (GET/POST /holidays, DELETE /holidays/{id}). There is no edit: a
// mistake is corrected by deleting the holiday and adding it again.
const WEEKDAYS = ['domingo', 'lunes', 'martes', 'miércoles', 'jueves', 'viernes', 'sábado'];
const holidaysBody = document.getElementById('holidays-table-body');
let holidaysYear = new Date().getFullYear();
let holidaysShown = [];

// The API sends a date as midnight UTC; only its YYYY-MM-DD part matters.
const holidayDay = (holiday) => holiday.date.slice(0, 10);

function describeDay(day) {
  const [year, month, date] = day.split('-').map(Number);
  const weekday = WEEKDAYS[new Date(Date.UTC(year, month - 1, date)).getUTCDay()];
  return `${String(date).padStart(2, '0')}-${String(month).padStart(2, '0')}-${year} (${weekday})`;
}

async function loadHolidays() {
  document.getElementById('holidays-year').textContent = holidaysYear;
  try {
    holidaysShown = await apiFetch(`/holidays?year=${holidaysYear}`);
    holidaysBody.innerHTML = holidaysShown.length
      ? holidaysShown
        .map((holiday) => `
          <tr>
            <td>${describeDay(holidayDay(holiday))}</td>
            <td>${escapeHtml(holiday.name)}</td>
            <td><button type="button" class="btn btn-danger btn-small" data-delete-holiday="${holiday.id}">Borrar</button></td>
          </tr>`)
        .join('')
      : '<tr><td colspan="3">No hay festivos en este año.</td></tr>';
  } catch (err) {
    holidaysBody.innerHTML = '<tr><td colspan="3">No se pudieron cargar los festivos.</td></tr>';
    showError(err.message);
  }
}

document.getElementById('holidays-prev').addEventListener('click', () => {
  holidaysYear -= 1;
  loadHolidays();
});
document.getElementById('holidays-next').addEventListener('click', () => {
  holidaysYear += 1;
  loadHolidays();
});

holidaysBody.addEventListener('click', async (event) => {
  const button = event.target.closest('button[data-delete-holiday]');
  if (!button) return;
  const holiday = holidaysShown.find((h) => h.id === Number(button.dataset.deleteHoliday));
  if (!holiday || !confirm(`¿Borrar el festivo «${holiday.name}» del ${describeDay(holidayDay(holiday))}?`)) return;
  button.disabled = true;
  try {
    await apiFetch(`/holidays/${holiday.id}`, { method: 'DELETE' });
    showFeedback('Festivo borrado.');
    await loadHolidays();
  } catch (err) {
    button.disabled = false;
    showError(err.message);
  }
});

const holidayDialog = document.getElementById('new-holiday-dialog');
const holidayForm = document.getElementById('new-holiday-form');
initModal(holidayDialog, holidayForm);

document.getElementById('toggle-new-holiday').addEventListener('click', () => {
  // Starts on the year being looked at, so the date is a click or two away.
  document.getElementById('nh-date').value = `${holidaysYear}-01-01`;
  openModal(holidayDialog);
});

holidayForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  const formData = new FormData(holidayForm);
  const submitButton = holidayForm.querySelector('button[type="submit"]');
  submitButton.disabled = true;
  try {
    await apiFetch('/holidays', {
      method: 'POST',
      body: JSON.stringify({ date: formData.get('date'), name: formData.get('name').toString().trim() }),
    });
    const year = Number(formData.get('date').toString().slice(0, 4));
    holidayDialog.close();
    if (year) holidaysYear = year;
    showFeedback('Festivo añadido.');
    await loadHolidays();
  } catch (err) {
    showError(err.message);
  } finally {
    submitButton.disabled = false;
  }
});

// Most holidays fall on the same day every year, so a new year starts from
// the previous one's. A day that already has a holiday is left alone. The
// ones that move (Semana Santa) need fixing by hand afterwards.
document.getElementById('holidays-copy').addEventListener('click', async () => {
  const button = document.getElementById('holidays-copy');
  button.disabled = true;
  try {
    const previous = await apiFetch(`/holidays?year=${holidaysYear - 1}`);
    const taken = new Set(holidaysShown.map((h) => holidayDay(h).slice(5)));
    const toCopy = previous.filter((h) => !taken.has(holidayDay(h).slice(5)));
    if (!previous.length) {
      showError(`No hay festivos en ${holidaysYear - 1} que copiar.`);
      return;
    }
    if (!toCopy.length) {
      showFeedback(`Todos los festivos de ${holidaysYear - 1} ya están en ${holidaysYear}.`);
      return;
    }
    if (!confirm(`Se copiarán ${toCopy.length} festivos de ${holidaysYear - 1} a ${holidaysYear}, el mismo día y mes. Los que cambian de fecha cada año (como Semana Santa) hay que corregirlos a mano. ¿Copiarlos?`)) return;
    let copied = 0;
    for (const holiday of toCopy) {
      const day = `${holidaysYear}${holidayDay(holiday).slice(4)}`;
      // 29 February has no equivalent in a year that isn't a leap one.
      if (day.endsWith('-02-29') && new Date(Date.UTC(holidaysYear, 1, 29)).getUTCMonth() !== 1) continue;
      try {
        await apiFetch('/holidays', { method: 'POST', body: JSON.stringify({ date: day, name: holiday.name }) });
        copied += 1;
      } catch (err) {
        showError(`Se copiaron ${copied} festivos; falló «${holiday.name}»: ${err.message}`);
        await loadHolidays();
        return;
      }
    }
    showFeedback(`Copiados ${copied} festivos de ${holidaysYear - 1}.`);
    await loadHolidays();
  } catch (err) {
    showError(err.message);
  } finally {
    button.disabled = false;
  }
});

await loadStatus();
await loadSessions();
await loadHolidays();
