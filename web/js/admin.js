import {
  apiFetch,
  currentUser,
  escapeHtml,
  formatDate,
  formatDateTime,
  personPickerLabeler,
  resolvePickerId,
  roleLabel,
  showFeedback,
  showError,
} from './api.js';
import { formatSize } from './file-browser.js';
import { initPage } from './layout.js';
import { initModal, openModal } from './modal.js';
import { initSortableHeaders } from './sortable-headers.js';

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
    add =
      '<p class="hint">Se asigna solo, según el historial de socio: no se da ni se quita a mano.</p>';
  } else {
    const candidates = people.filter((p) => !p.roles.includes(role.name));
    const labelOf = personPickerLabeler(people);
    const options = candidates
      .map((p) => `<option value="${escapeHtml(labelOf(p))}"></option>`)
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
      const detail = open
        ? `<tr class="role-detail"><td colspan="5">${roleDetail(role)}</td></tr>`
        : '';
      return row + detail;
    })
    .join('');
}

async function loadRoles() {
  try {
    [roleCatalog, people] = await Promise.all([apiFetch('/roles'), apiFetch('/users')]);
    people.sort((a, b) =>
      `${a.first_name} ${a.last_name}`.localeCompare(`${b.first_name} ${b.last_name}`, 'es'),
    );
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
      await apiFetch(
        `/users/${encodeURIComponent(unassign.dataset.login)}/roles/${unassign.dataset.unassignRole}`,
        { method: 'DELETE' },
      );
      showFeedback('Rol quitado.');
      await loadRoles();
    } catch (err) {
      unassign.disabled = false;
      showError(err.message);
    }
  } else if (assign) {
    const roleId = Number(assign.dataset.assignRole);
    const input = roleBody.querySelector(`input[data-role-search="${roleId}"]`);
    const labelOf = personPickerLabeler(people);
    const entries = people.map((p) => ({ id: p.login, label: labelOf(p) }));
    const login = resolvePickerId(entries, input.value);
    if (!login) {
      showError('Elige a una persona de la lista.');
      return;
    }
    assign.disabled = true;
    try {
      await apiFetch(`/users/${encodeURIComponent(login)}/roles`, {
        method: 'POST',
        body: JSON.stringify({ role_id: roleId }),
      });
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

// "Sesiones": every session token, to see who is signed in and cut one off.
// GET /tokens lists them (the active ones, or all with the checkbox) and
// PATCH /users/{login}/tokens/{id} changes one (its expiry) or revokes it. A page at a
// time, asking for one more than a page to know whether "Ver más" is needed.
const SESSIONS_PAGE_SIZE = 50;
const sessionsBody = document.getElementById('sessions-table-body');
const sessionsShowAll = document.getElementById('sessions-show-all');
const sessionsLoadMore = document.getElementById('sessions-load-more');
let sessionsShown = [];
let namesByLogin = {};
// The order is the server's, so that it holds across the pages "Ver más" brings (GET /tokens?sort=&order=).
// Starts as the API's own: most recently used first.
const sessionsSort = { key: 'last_used', desc: true };

function sessionStatus(session) {
  if (session.revoked_at) return 'Revocada';
  if (new Date(session.expires_at) <= new Date()) return 'Caducada';
  return 'Activa';
}

// "Chrome 118 · Windows" from what the API worked out of the last request's User-Agent; the whole
// header is the tooltip. "—" when the session was never used (or the request sent none), and a
// "no reconocido" with the header when it is something the API can't place.
function clientCell(session) {
  const ua = session.last_used_user_agent;
  if (!ua) return '—';
  const parts = [session.browser, session.os].filter(Boolean);
  return `<span title="${escapeHtml(ua)}">${parts.length ? escapeHtml(parts.join(' · ')) : 'No reconocido'}</span>`;
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
  const actions =
    status === 'Activa'
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
      <td>${clientCell(session)}</td>
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
    : '<tr><td colspan="8">No hay sesiones.</td></tr>';
}

async function loadSessions({ append = false } = {}) {
  if (!append) sessionsShown = [];
  sessionsLoadMore.disabled = true;
  try {
    const status = sessionsShowAll.checked ? 'all' : 'active';
    const fetched = await apiFetch(
      `/tokens?status=${status}&sort=${sessionsSort.key}&order=${sessionsSort.desc ? 'desc' : 'asc'}&limit=${SESSIONS_PAGE_SIZE + 1}&offset=${sessionsShown.length}`,
    );
    sessionsShown = sessionsShown.concat(fetched.slice(0, SESSIONS_PAGE_SIZE));
    renderSessions();
    sessionsLoadMore.hidden = fetched.length <= SESSIONS_PAGE_SIZE;
  } catch (err) {
    sessionsLoadMore.hidden = true;
    sessionsBody.innerHTML = '<tr><td colspan="8">No se pudieron cargar las sesiones.</td></tr>';
    showError(err.message);
  } finally {
    sessionsLoadMore.disabled = false;
  }
}

// By person (A first), last use, creation and expiry (most recent first), either way.
initSortableHeaders(document.getElementById('sessions-head'), {
  sort: sessionsSort,
  defaultDesc: (key) => key !== 'person',
  onChange: () => loadSessions(),
});
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
      await apiFetch(`/users/${encodeURIComponent(session.user_login)}/tokens/${session.id}`, {
        method: 'PATCH',
        body: JSON.stringify({ revoked: true }),
      });
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
    await apiFetch(
      `/users/${encodeURIComponent(expiringSession.user_login)}/tokens/${expiringSession.id}`,
      {
        method: 'PATCH',
        body: JSON.stringify({ expires_at: new Date(expiryInput.value).toISOString() }),
      },
    );
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
async function loadDirectory() {
  try {
    const directory = await apiFetch('/users/directory');
    namesByLogin = Object.fromEntries(
      directory.map((p) => [p.login, `${p.first_name} ${p.last_name}`]),
    );
  } catch {
    // Without names the logins show instead.
  }
}

// "Actividad": a calendar of the last year like the contributions of GitHub, one cell per day, darker
// the more different people used the API that day (GET /admin/activity). Each cell is a button: choosing
// a day lists who used it, with their requests. The weeks start on Monday and the last one is the
// current week; the days are the club's (Madrid), and are only dates here: no time zone arithmetic.
const activityContainer = document.getElementById('activity-container');
let activityCalendar = null;
let selectedActivityDay = null;

const parseDay = (iso) => {
  const [y, m, d] = iso.split('-').map(Number);
  return new Date(y, m - 1, d);
};
const isoDay = (date) =>
  `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`;
const mondayIndex = (date) => (date.getDay() + 6) % 7;
const shortMonths = [
  'ene',
  'feb',
  'mar',
  'abr',
  'may',
  'jun',
  'jul',
  'ago',
  'sep',
  'oct',
  'nov',
  'dic',
];

function personLink(login) {
  const name = namesByLogin[login] ?? login;
  return `<a href="/socio?login=${encodeURIComponent(login)}">${escapeHtml(name)}</a>`;
}

function renderActivityDetail() {
  const detail = document.getElementById('activity-detail');
  if (!detail || !selectedActivityDay) return;
  const entry = activityCalendar.days.find((d) => d.date === selectedActivityDay);
  const date = parseDay(selectedActivityDay);
  const weekday = date.toLocaleDateString('es', { weekday: 'long' });
  const title = `${weekday[0].toUpperCase()}${weekday.slice(1)} ${formatDate(date)}`;
  if (!entry) {
    detail.innerHTML = `<h3>${escapeHtml(title)}</h3><p>Nadie usó la intranet ese día.</p>`;
    return;
  }
  const people = entry.users
    .map(
      (u) =>
        `<li>${personLink(u.login)} — ${u.requests.toLocaleString('es')} ${u.requests === 1 ? 'petición' : 'peticiones'}</li>`,
    )
    .join('');
  detail.innerHTML = `<h3>${escapeHtml(title)}: ${entry.users.length} ${entry.users.length === 1 ? 'persona' : 'personas'}</h3><ul>${people}</ul>`;
}

function renderActivity() {
  if (!activityCalendar) return;
  const { from, to, days } = activityCalendar;
  const byDate = new Map(days.map((d) => [d.date, d]));
  const most = Math.max(1, ...days.map((d) => d.users.length));
  const level = (n) => (n === 0 ? 0 : Math.min(4, Math.ceil((n * 4) / most)));

  // From the Monday of the week of the first day to the end of the week of the last one.
  const first = parseDay(from);
  const start = new Date(
    first.getFullYear(),
    first.getMonth(),
    first.getDate() - mondayIndex(first),
  );
  const last = parseDay(to);
  const weeks = Math.floor(Math.round((last - start) / 86400000) / 7) + 1;

  const cells = [];
  const months = [];
  let lastMonth = -1;
  for (let week = 0; week < weeks; week++) {
    for (let weekday = 0; weekday < 7; weekday++) {
      const date = new Date(
        start.getFullYear(),
        start.getMonth(),
        start.getDate() + week * 7 + weekday,
      );
      const iso = isoDay(date);
      if (iso < from || iso > to) {
        cells.push('<span class="heat-cell heat-empty"></span>');
        continue;
      }
      const entry = byDate.get(iso);
      const n = entry ? entry.users.length : 0;
      const text = `${formatDate(date)}: ${n === 0 ? 'nadie' : `${n} ${n === 1 ? 'persona' : 'personas'}`}`;
      cells.push(
        `<button type="button" class="heat-cell level-${level(n)}" data-day="${iso}" title="${text}" aria-label="${text}" aria-pressed="${iso === selectedActivityDay}"></button>`,
      );
      // The name of a month goes above the first week that starts in it.
      if (weekday === 0 && date.getMonth() !== lastMonth) {
        months.push({ column: week + 1, name: shortMonths[date.getMonth()] });
        lastMonth = date.getMonth();
      }
    }
  }

  // A name is about three columns wide: when the next month starts sooner than that (the first, partial
  // month of the range), the earlier name goes, so two never overlap. Each one has a single column and
  // always the first row — in a grid, an item that cannot go where it was asked is pushed to another row,
  // out of sight, and so are all the ones after it.
  const monthLabels = months
    .filter((month, i) => i === months.length - 1 || months[i + 1].column - month.column >= 3)
    .map((month) => `<span style="grid-column: ${month.column}; grid-row: 1">${month.name}</span>`);

  const people = new Set(days.flatMap((d) => d.users.map((u) => u.login)));
  const summary = days.length
    ? `${people.size} ${people.size === 1 ? 'persona distinta' : 'personas distintas'} en ${days.length} ${days.length === 1 ? 'día' : 'días'} con actividad.`
    : 'Todavía no hay actividad registrada: se empezó a guardar al desplegar esta versión.';
  activityContainer.innerHTML = `
    <p class="activity-summary">${summary}</p>
    <div class="heat">
      <div class="heat-weekdays" aria-hidden="true"><span>Lun</span><span></span><span>Mié</span><span></span><span>Vie</span><span></span><span>Dom</span></div>
      <div class="heat-scroll">
        <div class="heat-months" style="grid-template-columns: repeat(${weeks}, var(--cell))" aria-hidden="true">${monthLabels.join('')}</div>
        <div class="heat-grid" style="grid-template-columns: repeat(${weeks}, var(--cell))">${cells.join('')}</div>
      </div>
    </div>
    <div class="heat-legend" aria-hidden="true">Menos <span class="heat-cell level-0"></span><span class="heat-cell level-1"></span><span class="heat-cell level-2"></span><span class="heat-cell level-3"></span><span class="heat-cell level-4"></span> Más</div>
    <div id="activity-detail" class="activity-detail" aria-live="polite"></div>`;
  // The newest weeks are the ones that matter: on a narrow screen the calendar starts scrolled to them.
  const scroller = activityContainer.querySelector('.heat-scroll');
  scroller.scrollLeft = scroller.scrollWidth;
  renderActivityDetail();
}

async function loadActivity() {
  try {
    activityCalendar = await apiFetch('/admin/activity');
    // Starts with the latest day that has any activity chosen, if there is one.
    selectedActivityDay = activityCalendar.days.at(-1)?.date ?? null;
    renderActivity();
  } catch {
    activityContainer.textContent = 'No se pudo cargar la actividad.';
  }
}

activityContainer.addEventListener('click', (event) => {
  const cell = event.target.closest('button[data-day]');
  if (!cell) return;
  selectedActivityDay = cell.dataset.day;
  for (const other of activityContainer.querySelectorAll('button[data-day]')) {
    other.setAttribute('aria-pressed', String(other === cell));
  }
  renderActivityDetail();
});

// "Estado del sistema": a read-only picture from GET /admin/status — what is
// deployed, whether the database answers and how far its migrations are,
// whether email and Telegram are set up, the timeouts in force, whether the
// file directories exist. Nothing here can be changed from the page.
const statusContainer = document.getElementById('status-container');

// 90 → "1 minuto y medio" would be cute; plain and exact is enough here.
function formatDuration(seconds) {
  const units = [
    [86400, 'día', 'días'],
    [3600, 'hora', 'horas'],
    [60, 'minuto', 'minutos'],
  ];
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
    .map(
      ([label, value]) =>
        `<div class="detail-row"><span>${escapeHtml(label)}</span><span>${value}</span></div>`,
    )
    .join('');
  return `<h3>${escapeHtml(title)}</h3><div class="detail-list">${lines}</div>`;
}

const yesNo = (value) => (value ? 'Sí' : 'No');

// "Actividad": how many different people used the intranet and how many requests it answered in the
// last hour, day and month.
function activityGroup(activity) {
  const number = (n) => n.toLocaleString('es');
  const periods = (counts) =>
    `${number(counts.last_hour)} en la última hora, ${number(counts.last_day)} en el último día, ${number(counts.last_month)} en el último mes`;
  return statusGroup('Actividad', [
    ['Personas distintas', periods(activity.users)],
    ['Peticiones', periods(activity.requests)],
  ]);
}

function renderStatus(status) {
  const app = status.app;
  const revision = app.vcs_revision
    ? `${escapeHtml(app.vcs_revision.slice(0, 8))}${app.vcs_modified ? ' (con cambios sin commitear)' : ''}${app.vcs_time ? `, del ${formatDateTime(app.vcs_time)}` : ''}`
    : '—';
  const db = status.database;
  const settings = status.settings;
  const storageLabels = {
    archive: 'Archivo',
    user_files: 'Ficheros de las personas',
    activity_resources: 'Memorias de actividades',
    transaction_files: 'Ficheros de transacciones',
    library_files: 'Ficheros de la biblioteca',
  };

  const problems = (status.problems ?? []).map((text) => `<li>${escapeHtml(text)}</li>`).join('');

  statusContainer.innerHTML = [
    problems
      ? `<div class="status-problems"><strong>Hay problemas:</strong><ul>${problems}</ul></div>`
      : '<p class="success-message">No se ha detectado ningún problema.</p>',
    statusGroup('Aplicación', [
      [
        'Versión',
        app.version
          ? escapeHtml(app.version)
          : 'Sin versión (el binario no se compiló desde una copia de git)',
      ],
      ['Commit', revision],
      ['Go', escapeHtml(app.go_version)],
      ['Arrancó', `${formatDateTime(app.started_at)} (${ago(app.started_at)})`],
    ]),
    ...(status.activity ? [activityGroup(status.activity)] : []),
    statusGroup('Base de datos', [
      [
        'Estado',
        db.ok
          ? `Responde (${db.latency_ms} ms)`
          : `<strong>No responde</strong>: ${escapeHtml(db.error ?? '')}`,
      ],
      [
        'Última migración aplicada',
        db.migration_version != null ? String(db.migration_version) : '—',
      ],
    ]),
    statusGroup('Correo', [
      [
        'SMTP configurado',
        status.mail.smtp_configured
          ? 'Sí'
          : 'No: los enlaces de acceso solo se escriben en el log del servidor',
      ],
    ]),
    statusGroup('Telegram', [
      ['Canal de logs configurado', yesNo(status.telegram.log_channel_configured)],
      [
        'Bot en marcha',
        status.telegram.bot_running
          ? `Sí${status.telegram.bot_username ? ` (@${escapeHtml(status.telegram.bot_username)})` : ''}`
          : 'No',
      ],
    ]),
    statusGroup('Ajustes', [
      ['Dirección pública', escapeHtml(settings.public_base_url)],
      ['Una sesión caduca si no se usa en', formatDuration(settings.session_ttl_seconds)],
      [
        'En las direcciones «local.…» caduca si no se usa en',
        formatDuration(settings.local_session_ttl_seconds),
      ],
      ['Validez del enlace de acceso', formatDuration(settings.magic_link_ttl_seconds)],
      ['Espera entre dos enlaces', formatDuration(settings.magic_link_cooldown_seconds)],
      ['Validez del enlace enviado por el bot', formatDuration(settings.bot_dm_link_ttl_seconds)],
      ['Duración de una impersonación', formatDuration(settings.impersonation_ttl_seconds)],
    ]),
    statusGroup(
      'Ficheros',
      status.storage.map((dir) => [
        storageLabels[dir.name] ?? dir.name,
        `<code>${escapeHtml(dir.path)}</code> — ${dir.exists ? 'existe' : '<strong>no existe</strong>'}`,
      ]),
    ),
    statusGroup(
      'Espacio en disco',
      (status.disks ?? []).map((disk) => {
        // The paths are listed under "Ficheros"; here a disk is named by what is on it.
        const names = status.storage
          .filter((dir) => disk.paths.includes(dir.path))
          .map((dir) => storageLabels[dir.name] ?? dir.name);
        return [
          names.length === status.storage.length ? 'Todos los ficheros' : names.join(', '),
          `${disk.used_percent} % usado (quedan ${formatSize(disk.free_bytes)} de ${formatSize(disk.total_bytes)})`,
        ];
      }),
    ),
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
          .map(
            (holiday) => `
          <tr>
            <td>${describeDay(holidayDay(holiday))}</td>
            <td>${escapeHtml(holiday.name)}</td>
            <td><button type="button" class="btn btn-danger btn-small" data-delete-holiday="${holiday.id}">Borrar</button></td>
          </tr>`,
          )
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
  if (
    !holiday ||
    !confirm(`¿Borrar el festivo «${holiday.name}» del ${describeDay(holidayDay(holiday))}?`)
  )
    return;
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
      body: JSON.stringify({
        date: formData.get('date'),
        name: formData.get('name').toString().trim(),
      }),
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
    if (
      !confirm(
        `Se copiarán ${toCopy.length} festivos de ${holidaysYear - 1} a ${holidaysYear}, el mismo día y mes. Los que cambian de fecha cada año (como Semana Santa) hay que corregirlos a mano. ¿Copiarlos?`,
      )
    )
      return;
    let copied = 0;
    for (const holiday of toCopy) {
      const day = `${holidaysYear}${holidayDay(holiday).slice(4)}`;
      // 29 February has no equivalent in a year that isn't a leap one.
      if (day.endsWith('-02-29') && new Date(Date.UTC(holidaysYear, 1, 29)).getUTCMonth() !== 1)
        continue;
      try {
        await apiFetch('/holidays', {
          method: 'POST',
          body: JSON.stringify({ date: day, name: holiday.name }),
        });
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

// "Datos de interés": the labelled values the home page shows (GET /club-info) — name, tax id,
// address, bank account, the Bizum number... Stored in the database so they can change, and so a
// private one is not in the repository. Each has a place in the list (moved with ↑ ↓, which sends
// the whole new order, PUT /club-info/order) and optionally a button that copies a text of its own.
const clubInfoBody = document.getElementById('club-info-table-body');
const clubInfoDialog = document.getElementById('club-info-dialog');
const clubInfoForm = document.getElementById('club-info-form');
let clubInfoItems = [];
let editingClubInfoId = null; // null: the dialog is creating one
initModal(clubInfoDialog, clubInfoForm);

function renderClubInfo() {
  clubInfoBody.innerHTML = clubInfoItems.length
    ? clubInfoItems
        .map(
          (item, i) => `
        <tr>
          <td>${escapeHtml(item.label)}</td>
          <td>${escapeHtml(item.value)}</td>
          <td>${item.copy_value ? `${escapeHtml(item.copy_label ?? 'Copiar')}: <code>${escapeHtml(item.copy_value)}</code>` : '—'}</td>
          <td class="nowrap">
            <button type="button" class="btn btn-secondary btn-small" data-move-club-info="${item.id}" data-direction="-1" aria-label="Subir" ${i === 0 ? 'disabled' : ''}>↑</button>
            <button type="button" class="btn btn-secondary btn-small" data-move-club-info="${item.id}" data-direction="1" aria-label="Bajar" ${i === clubInfoItems.length - 1 ? 'disabled' : ''}>↓</button>
            <button type="button" class="btn btn-secondary btn-small" data-edit-club-info="${item.id}">Editar</button>
            <button type="button" class="btn btn-danger btn-small" data-delete-club-info="${item.id}">Borrar</button>
          </td>
        </tr>`,
        )
        .join('')
    : '<tr><td colspan="4">Todavía no hay datos.</td></tr>';
}

async function loadClubInfo() {
  try {
    clubInfoItems = await apiFetch('/club-info');
    renderClubInfo();
  } catch (err) {
    clubInfoBody.innerHTML = '<tr><td colspan="4">No se pudieron cargar los datos.</td></tr>';
    showError(err.message);
  }
}

function openClubInfoDialog(item) {
  editingClubInfoId = item?.id ?? null;
  document.getElementById('club-info-dialog-title').textContent = item
    ? `Editar «${item.label}»`
    : 'Nuevo dato';
  const set = (name, value) => {
    clubInfoForm.elements[name].value = value ?? '';
  };
  set('label', item?.label);
  set('value', item?.value);
  set('copy_value', item?.copy_value);
  set('copy_label', item?.copy_label);
  openModal(clubInfoDialog);
}

document
  .getElementById('toggle-new-club-info')
  .addEventListener('click', () => openClubInfoDialog(null));

clubInfoForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  const data = new FormData(clubInfoForm);
  const text = (name) => data.get(name)?.toString().trim() ?? '';
  const submitButton = clubInfoForm.querySelector('button[type="submit"]');
  submitButton.disabled = true;
  const editing = editingClubInfoId != null;
  try {
    await apiFetch(editing ? `/club-info/${editingClubInfoId}` : '/club-info', {
      method: editing ? 'PUT' : 'POST',
      body: JSON.stringify({
        label: text('label'),
        value: text('value'),
        copy_value: text('copy_value') || null,
        copy_label: text('copy_label') || null,
      }),
    });
    clubInfoDialog.close();
    showFeedback(editing ? 'Dato guardado.' : 'Dato añadido.');
    await loadClubInfo();
  } catch (err) {
    showError(err.message);
  } finally {
    submitButton.disabled = false;
  }
});

clubInfoBody.addEventListener('click', async (event) => {
  const edit = event.target.closest('button[data-edit-club-info]');
  if (edit) {
    openClubInfoDialog(clubInfoItems.find((i) => i.id === Number(edit.dataset.editClubInfo)));
    return;
  }
  const move = event.target.closest('button[data-move-club-info]');
  if (move) {
    const index = clubInfoItems.findIndex((i) => i.id === Number(move.dataset.moveClubInfo));
    const target = index + Number(move.dataset.direction);
    if (index < 0 || target < 0 || target >= clubInfoItems.length) return;
    const ids = clubInfoItems.map((i) => i.id);
    [ids[index], ids[target]] = [ids[target], ids[index]];
    try {
      clubInfoItems = await apiFetch('/club-info/order', {
        method: 'PUT',
        body: JSON.stringify({ item_ids: ids }),
      });
      renderClubInfo();
    } catch (err) {
      showError(err.message);
      await loadClubInfo();
    }
    return;
  }
  const del = event.target.closest('button[data-delete-club-info]');
  if (!del) return;
  const item = clubInfoItems.find((i) => i.id === Number(del.dataset.deleteClubInfo));
  if (!item || !confirm(`¿Quitar «${item.label}» de los datos de interés?`)) return;
  del.disabled = true;
  try {
    await apiFetch(`/club-info/${item.id}`, { method: 'DELETE' });
    showFeedback('Dato quitado.');
    await loadClubInfo();
  } catch (err) {
    del.disabled = false;
    showError(err.message);
  }
});

// "Tipos de material": the catalog every item has one of (GET/POST /material/types,
// PATCH/DELETE /material/types/{id}). Cuerda and Saca are fixed (they carry the rope and bag fields):
// they are listed but cannot be renamed or deleted; a type with items can't go either. A general
// type also says whether the description of its items is required, and has attributes (text or a
// list of options, required or not), edited in the same dialog as its name.
const materialTypesBody = document.getElementById('material-types-table-body');
const materialTypeDialog = document.getElementById('material-type-dialog');
const materialTypeForm = document.getElementById('material-type-form');
const attributesSection = document.getElementById('material-attributes-section');
const attributesList = document.getElementById('material-attributes-list');
const attributeForm = document.getElementById('material-attribute-form');
let materialTypes = [];
let editingMaterialTypeId = null; // null: the dialog is creating one
initModal(materialTypeDialog, materialTypeForm);
initModal(materialTypeDialog, attributeForm);
// Closing the dialog leaves the new-attribute form as it was found.
materialTypeDialog.addEventListener('close', () => {
  document.getElementById('ma-options-field').hidden = true;
  attributesSection.hidden = true;
});

function renderMaterialTypes() {
  materialTypesBody.innerHTML = materialTypes.length
    ? materialTypes
        .map((type, i) => {
          const general = type.kind === 'general';
          const names = type.attributes.map((a) => escapeHtml(a.name)).join(', ');
          return `
        <tr>
          <td>${escapeHtml(type.name)}</td>
          <td>${general ? (type.description_required ? 'Obligatoria' : 'Opcional') : '—'}</td>
          <td>${general ? names || '—' : '—'}</td>
          <td class="nowrap">
            <button type="button" class="btn btn-secondary btn-small" data-move-material-type="${type.id}" data-direction="-1" aria-label="Subir" ${i === 0 ? 'disabled' : ''}>↑</button>
            <button type="button" class="btn btn-secondary btn-small" data-move-material-type="${type.id}" data-direction="1" aria-label="Bajar" ${i === materialTypes.length - 1 ? 'disabled' : ''}>↓</button>${
              general
                ? `<button type="button" class="btn btn-secondary btn-small" data-edit-material-type="${type.id}">Editar</button>
            <button type="button" class="btn btn-danger btn-small" data-delete-material-type="${type.id}">Borrar</button>`
                : '<span class="badge">Fijo</span>'
            }</td>
        </tr>`;
        })
        .join('')
    : '<tr><td colspan="4">No hay tipos.</td></tr>';
}

async function loadMaterialTypes() {
  try {
    materialTypes = await apiFetch('/material/types');
    renderMaterialTypes();
    if (materialTypeDialog.open && editingMaterialTypeId != null) renderAttributes();
  } catch {
    materialTypesBody.innerHTML = '<tr><td colspan="4">No se pudieron cargar los tipos.</td></tr>';
  }
}

function openMaterialTypeDialog(type) {
  editingMaterialTypeId = type?.id ?? null;
  document.getElementById('material-type-dialog-title').textContent = type
    ? 'Editar tipo'
    : 'Nuevo tipo';
  materialTypeForm.elements.name.value = type?.name ?? '';
  materialTypeForm.elements.description_required.checked = type?.description_required ?? true;
  // The attributes belong to a type that exists: a new one gets them once it is saved.
  attributesSection.hidden = !type;
  if (type) renderAttributes();
  openModal(materialTypeDialog);
}

document
  .getElementById('toggle-new-material-type')
  .addEventListener('click', () => openMaterialTypeDialog(null));

materialTypeForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  const submitButton = materialTypeForm.querySelector('button[type="submit"]');
  submitButton.disabled = true;
  const editing = editingMaterialTypeId != null;
  try {
    const saved = await apiFetch(
      editing ? `/material/types/${editingMaterialTypeId}` : '/material/types',
      {
        method: editing ? 'PATCH' : 'POST',
        body: JSON.stringify({
          name: materialTypeForm.elements.name.value.trim(),
          description_required: materialTypeForm.elements.description_required.checked,
        }),
      },
    );
    if (editing) {
      materialTypeDialog.close();
      showFeedback('Tipo guardado.');
      await loadMaterialTypes();
    } else {
      // A new type: the dialog stays open, now editing it, so its attributes can be added.
      showFeedback('Tipo añadido. Ya puedes añadirle atributos.');
      editingMaterialTypeId = saved.id;
      document.getElementById('material-type-dialog-title').textContent = 'Editar tipo';
      attributesSection.hidden = false;
      await loadMaterialTypes();
      renderAttributes();
    }
  } catch (err) {
    showError(err.message);
  } finally {
    submitButton.disabled = false;
  }
});

// The attributes of one type: each with its data type, whether it is required, its options (an enum
// one), and the buttons to move, rename and delete it.
function renderAttributes() {
  const type = materialTypes.find((t) => t.id === editingMaterialTypeId);
  if (!type) return;
  attributesList.innerHTML = type.attributes.length
    ? type.attributes
        .map(
          (a, i) => `
      <div class="detail-list" data-attribute="${a.id}">
        <div class="detail-row">
          <strong>${escapeHtml(a.name)}</strong>
          <span>${a.data_type === 'enum' ? 'Lista de opciones' : 'Texto libre'}</span>
          <label class="checkbox-label"><input type="checkbox" data-attribute-required="${a.id}" ${a.required ? 'checked' : ''}> Obligatorio</label>
          <span class="nowrap">
            <button type="button" class="btn btn-secondary btn-small" data-move-attribute="${a.id}" data-direction="-1" aria-label="Subir" ${i === 0 ? 'disabled' : ''}>↑</button>
            <button type="button" class="btn btn-secondary btn-small" data-move-attribute="${a.id}" data-direction="1" aria-label="Bajar" ${i === type.attributes.length - 1 ? 'disabled' : ''}>↓</button>
            <button type="button" class="btn btn-secondary btn-small" data-rename-attribute="${a.id}">Renombrar</button>
            <button type="button" class="btn btn-danger btn-small" data-delete-attribute="${a.id}">Borrar</button>
          </span>
        </div>
        ${
          a.data_type === 'enum'
            ? `<div class="detail-row">
          <span>${
            a.options
              .map(
                (o) =>
                  `<span class="badge-removable">${escapeHtml(o.label)}<button type="button" data-rename-option="${o.id}" title="Renombrar esta opción" aria-label="Renombrar">✎</button><button type="button" data-delete-option="${o.id}" title="Quitar esta opción" aria-label="Quitar">×</button></span>`,
              )
              .join('') || 'Sin opciones todavía.'
          }</span>
          <span class="nowrap">
            <input type="text" data-new-option-label="${a.id}" placeholder="Nueva opción" maxlength="100">
            <button type="button" class="btn btn-secondary btn-small" data-add-option="${a.id}">Añadir</button>
          </span>
        </div>`
            : ''
        }
      </div>`,
        )
        .join('')
    : '<p>Este tipo todavía no tiene atributos.</p>';
}

function findAttribute(id) {
  return materialTypes.flatMap((t) => t.attributes).find((a) => a.id === id);
}

function findOption(id) {
  return materialTypes
    .flatMap((t) => t.attributes)
    .flatMap((a) => a.options ?? [])
    .find((o) => o.id === id);
}

// Runs one change to the attributes and reloads the lists; the error, if any, is shown.
async function changeAttributes(path, method, body, feedback) {
  try {
    await apiFetch(path, { method, body: body ? JSON.stringify(body) : undefined });
    if (feedback) showFeedback(feedback);
    await loadMaterialTypes();
    return true;
  } catch (err) {
    showError(err.message);
    await loadMaterialTypes();
    return false;
  }
}

document.getElementById('ma-data-type').addEventListener('change', (event) => {
  document.getElementById('ma-options-field').hidden = event.target.value !== 'enum';
});
attributeForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  const submitButton = attributeForm.querySelector('button[type="submit"]');
  submitButton.disabled = true;
  const isEnum = attributeForm.elements.data_type.value === 'enum';
  const ok = await changeAttributes(
    `/material/types/${editingMaterialTypeId}/attributes`,
    'POST',
    {
      name: attributeForm.elements.name.value.trim(),
      data_type: attributeForm.elements.data_type.value,
      required: attributeForm.elements.required.checked,
      options: isEnum
        ? attributeForm.elements.options.value
            .split('\n')
            .map((line) => line.trim())
            .filter(Boolean)
        : [],
    },
    'Atributo añadido.',
  );
  if (ok) {
    attributeForm.reset();
    document.getElementById('ma-options-field').hidden = true;
  }
  submitButton.disabled = false;
});

attributesList.addEventListener('change', async (event) => {
  const box = event.target.closest('input[data-attribute-required]');
  if (!box) return;
  await changeAttributes(
    `/material/attributes/${box.dataset.attributeRequired}`,
    'PATCH',
    { required: box.checked },
    'Atributo guardado.',
  );
});

attributesList.addEventListener('click', async (event) => {
  const move = event.target.closest('button[data-move-attribute]');
  if (move) {
    const type = materialTypes.find((t) => t.id === editingMaterialTypeId);
    const ids = type.attributes.map((a) => a.id);
    const from = ids.indexOf(Number(move.dataset.moveAttribute));
    const to = from + Number(move.dataset.direction);
    if (to < 0 || to >= ids.length) return;
    [ids[from], ids[to]] = [ids[to], ids[from]];
    await changeAttributes(
      `/material/types/${editingMaterialTypeId}/attributes/order`,
      'PUT',
      { attribute_ids: ids },
      null,
    );
    return;
  }
  const rename = event.target.closest('button[data-rename-attribute]');
  if (rename) {
    const attribute = findAttribute(Number(rename.dataset.renameAttribute));
    const name = prompt('Nuevo nombre del atributo:', attribute.name)?.trim();
    if (name && name !== attribute.name) {
      await changeAttributes(
        `/material/attributes/${attribute.id}`,
        'PATCH',
        { name },
        'Atributo guardado.',
      );
    }
    return;
  }
  const del = event.target.closest('button[data-delete-attribute]');
  if (del) {
    const attribute = findAttribute(Number(del.dataset.deleteAttribute));
    if (
      confirm(
        `¿Borrar el atributo «${attribute.name}»? El material que lo tenga perderá ese valor.`,
      )
    ) {
      await changeAttributes(
        `/material/attributes/${attribute.id}`,
        'DELETE',
        null,
        'Atributo borrado.',
      );
    }
    return;
  }
  const add = event.target.closest('button[data-add-option]');
  if (add) {
    const input = attributesList.querySelector(
      `input[data-new-option-label="${add.dataset.addOption}"]`,
    );
    const label = input.value.trim();
    if (label) {
      await changeAttributes(
        `/material/attributes/${add.dataset.addOption}/options`,
        'POST',
        { label },
        'Opción añadida.',
      );
    }
    return;
  }
  const renameOption = event.target.closest('button[data-rename-option]');
  if (renameOption) {
    const option = findOption(Number(renameOption.dataset.renameOption));
    const label = prompt('Nuevo nombre de la opción:', option.label)?.trim();
    if (label && label !== option.label) {
      await changeAttributes(
        `/material/options/${option.id}`,
        'PATCH',
        { label },
        'Opción guardada.',
      );
    }
    return;
  }
  const delOption = event.target.closest('button[data-delete-option]');
  if (delOption) {
    const option = findOption(Number(delOption.dataset.deleteOption));
    if (confirm(`¿Quitar la opción «${option.label}»?`)) {
      await changeAttributes(`/material/options/${option.id}`, 'DELETE', null, 'Opción quitada.');
    }
  }
});

materialTypesBody.addEventListener('click', async (event) => {
  // ↑ ↓: the order of the types is the one of the "Tipo" list of /material; each sends the whole new
  // order. Cuerda and Saca can be moved too.
  const move = event.target.closest('button[data-move-material-type]');
  if (move) {
    const ids = materialTypes.map((t) => t.id);
    const from = ids.indexOf(Number(move.dataset.moveMaterialType));
    const to = from + Number(move.dataset.direction);
    if (to < 0 || to >= ids.length) return;
    [ids[from], ids[to]] = [ids[to], ids[from]];
    move.disabled = true;
    try {
      await apiFetch('/material/types/order', {
        method: 'PUT',
        body: JSON.stringify({ type_ids: ids }),
      });
    } catch (err) {
      showError(err.message);
    }
    await loadMaterialTypes();
    return;
  }
  const edit = event.target.closest('button[data-edit-material-type]');
  if (edit) {
    openMaterialTypeDialog(
      materialTypes.find((c) => c.id === Number(edit.dataset.editMaterialType)),
    );
    return;
  }
  const del = event.target.closest('button[data-delete-material-type]');
  if (!del) return;
  const type = materialTypes.find((c) => c.id === Number(del.dataset.deleteMaterialType));
  if (!type || !confirm(`¿Borrar el tipo «${type.name}»?`)) return;
  del.disabled = true;
  try {
    await apiFetch(`/material/types/${type.id}`, { method: 'DELETE' });
    showFeedback('Tipo borrado.');
    await loadMaterialTypes();
  } catch (err) {
    del.disabled = false;
    showError(err.message);
  }
});

// Every section asks for its own data and none needs another's, so all of it
// goes out at once — seven requests one after another were most of this page's
// wait. The sessions show names from the directory, so if the directory
// arrives after them they are drawn again with the names.
await Promise.all([
  loadRoles(),
  loadDirectory().then(() => {
    if (sessionsShown.length) renderSessions();
    // The detail of a day names people: drawn again if the directory came after the calendar.
    renderActivityDetail();
  }),
  loadStatus(),
  loadActivity(),
  loadSessions(),
  loadHolidays(),
  loadClubInfo(),
  loadMaterialTypes(),
]);
