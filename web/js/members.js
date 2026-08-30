import {
  initSession,
  logout,
  applyRoleVisibility,
  currentUser,
  hasAnyRole,
  apiFetch,
  escapeHtml,
  formatDate,
} from './api.js';

await initSession();
applyRoleVisibility();
document.getElementById('logout-button').addEventListener('click', logout);

const user = currentUser();
const isAdmin = hasAnyRole(user, ['admin', 'board']);
const isRealAdmin = hasAnyRole(user, ['admin']);
if (!isAdmin) {
  window.location.href = '/';
}

const feedbackBox = document.getElementById('feedback');
const errorBox = document.getElementById('error');

function showFeedback(message) {
  errorBox.hidden = true;
  feedbackBox.textContent = message;
  feedbackBox.hidden = false;
}

function showError(message) {
  feedbackBox.hidden = true;
  errorBox.textContent = message;
  errorBox.hidden = false;
}

// The system roles (see docs/architecture.md) get a friendlier Spanish
// label; anything an admin created shows as typed. Only used here for the
// role catalog table and the "Nueva persona" checkboxes — managing an
// existing person's roles happens on their own page (/member?id=…) now.
const knownRoleLabels = { admin: 'Administración', board: 'Directiva', member: 'Socio', former_member: 'Ex-socio' };
function roleLabel(name) {
  return knownRoleLabels[name] ?? name;
}

let allRoles = [];

async function loadRoles() {
  const tableBody = document.getElementById('roles-table-body');
  try {
    allRoles = await apiFetch('/roles');
    tableBody.innerHTML = allRoles
      .map((role) => `
        <tr>
          <td>${escapeHtml(roleLabel(role.name))}</td>
          <td>${escapeHtml(role.description) || '—'}</td>
          <td>
            ${role.is_system ? '<span class="badge">Del sistema</span>' : ''}
            ${role.is_automatic ? '<span class="badge" title="Se asigna solo, según el historial de socio">Automático</span>' : ''}
          </td>
          <td>${!role.is_system && isRealAdmin ? `<button type="button" class="btn btn-danger btn-small" data-delete-role="${role.id}">Borrar</button>` : ''}</td>
        </tr>
      `)
      .join('');

    const group = document.getElementById('np-roles-group');
    if (group) {
      group.innerHTML = allRoles
        .filter((role) => !role.is_automatic && (role.name !== 'admin' || isRealAdmin))
        .map((role) => `
          <label>
            <input type="checkbox" name="role_names" value="${escapeHtml(role.name)}">
            ${escapeHtml(roleLabel(role.name))}
          </label>
        `)
        .join('');
    }
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

if (isRealAdmin) {
  const newRolePanel = document.getElementById('new-role-panel');
  const newRoleForm = document.getElementById('new-role-form');

  document.getElementById('toggle-new-role').addEventListener('click', () => {
    newRolePanel.hidden = !newRolePanel.hidden;
  });

  document.getElementById('cancel-new-role').addEventListener('click', () => {
    newRoleForm.reset();
    newRolePanel.hidden = true;
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
      newRoleForm.reset();
      newRolePanel.hidden = true;
      await loadRoles();
    } catch (err) {
      showError(err.message);
    } finally {
      submitButton.disabled = false;
    }
  });
}

// The people list is purely informational and sortable: every action
// about a specific person (edit their data, manage their roles, register
// an alta/baja, delete them) lives on their own page (/member?id=…) now.
// Each column knows how to render a cell and how to compare two people
// for sorting, so both concerns stay in one place.
function textColumn(key, label, getValue) {
  return {
    key,
    label,
    value: getValue,
    render: (person) => escapeHtml(getValue(person)) || '—',
    compare: (a, b) => getValue(a).localeCompare(getValue(b), 'es'),
  };
}

function dateColumn(key, label, getValue) {
  return {
    key,
    label,
    value: getValue,
    render: (person) => {
      const raw = getValue(person);
      return raw ? formatDate(raw) : '—';
    },
    // ISO date/timestamp strings sort correctly lexicographically; empty
    // values (never had one) sort first.
    compare: (a, b) => (getValue(a) ?? '').localeCompare(getValue(b) ?? ''),
  };
}

const COLUMNS = [
  { ...textColumn('first_name', 'Nombre', (p) => p.first_name), default: true },
  { ...textColumn('last_name', 'Apellidos', (p) => p.last_name), default: true },
  { ...textColumn('email', 'Email', (p) => p.email), default: false },
  { ...textColumn('phone', 'Teléfono', (p) => p.phone ?? ''), default: false },
  { ...textColumn('national_id', 'DNI', (p) => p.national_id ?? ''), default: false },
  { ...textColumn('membership_number', 'N.º de socio', (p) => p.membership_number ?? ''), default: false },
  { ...dateColumn('birth_date', 'Fecha de nacimiento', (p) => p.birth_date), default: true },
  { ...textColumn('address', 'Dirección', (p) => p.address ?? ''), default: false },
  { ...textColumn('postal_code', 'Código postal', (p) => p.postal_code ?? ''), default: false },
  { ...textColumn('city', 'Ciudad', (p) => p.city ?? ''), default: false },
  { ...textColumn('province', 'Provincia', (p) => p.province ?? ''), default: false },
  { ...dateColumn('membership_start_date', 'Fecha de alta', (p) => p.membership_start_date), default: true },
  {
    ...dateColumn('membership_end_date', 'Fecha de baja', (p) => p.membership_end_date),
    default: true,
    render: (person) => {
      if (person.membership_end_date) return formatDate(person.membership_end_date);
      return person.membership_start_date ? '<span class="badge">Activa</span>' : '—';
    },
  },
];
const columnsByKey = Object.fromEntries(COLUMNS.map((c) => [c.key, c]));

let visibleColumns = new Set(COLUMNS.filter((c) => c.default).map((c) => c.key));
let sortKey = 'last_name';
let sortDir = 'asc';
let people = [];

function renderColumnToggle() {
  const container = document.getElementById('column-toggle');
  container.innerHTML = COLUMNS
    .map((c) => `
      <label>
        <input type="checkbox" data-toggle-column="${c.key}" ${visibleColumns.has(c.key) ? 'checked' : ''}>
        ${escapeHtml(c.label)}
      </label>
    `)
    .join('');
}

document.getElementById('column-toggle').addEventListener('change', (event) => {
  const checkbox = event.target.closest('input[data-toggle-column]');
  if (!checkbox) return;
  if (checkbox.checked) {
    visibleColumns.add(checkbox.dataset.toggleColumn);
  } else {
    visibleColumns.delete(checkbox.dataset.toggleColumn);
  }
  renderUsersTable();
});

function renderUsersTableHead() {
  const headRow = document.getElementById('users-table-head');
  headRow.innerHTML = COLUMNS
    .filter((c) => visibleColumns.has(c.key))
    .map((c) => {
      const indicator = c.key === sortKey ? (sortDir === 'asc' ? ' ▲' : ' ▼') : '';
      return `<th class="sortable" data-sort-column="${c.key}">${escapeHtml(c.label)}${indicator}</th>`;
    })
    .join('');
}

document.getElementById('users-table-head').addEventListener('click', (event) => {
  const th = event.target.closest('th[data-sort-column]');
  if (!th) return;
  const key = th.dataset.sortColumn;
  if (sortKey === key) {
    sortDir = sortDir === 'asc' ? 'desc' : 'asc';
  } else {
    sortKey = key;
    sortDir = 'asc';
  }
  renderUsersTable();
});

function renderUserRow(person) {
  const cells = COLUMNS
    .filter((c) => visibleColumns.has(c.key))
    .map((c) => `<td>${c.render(person)}</td>`)
    .join('');
  return `<tr data-person-id="${person.id}">${cells}</tr>`;
}

function renderUsersTable() {
  renderColumnToggle();
  renderUsersTableHead();

  const tableBody = document.getElementById('users-table-body');
  if (people.length === 0) {
    tableBody.innerHTML = `<tr><td colspan="${Math.max(visibleColumns.size, 1)}">Todavía no hay personas registradas.</td></tr>`;
    return;
  }

  const column = columnsByKey[sortKey];
  const sorted = people.slice().sort((a, b) => (sortDir === 'asc' ? column.compare(a, b) : column.compare(b, a)));
  tableBody.innerHTML = sorted.map(renderUserRow).join('');
}

document.getElementById('users-table-body').addEventListener('click', (event) => {
  const row = event.target.closest('tr[data-person-id]');
  if (!row) return;
  window.location.href = `/member?id=${row.dataset.personId}`;
});

async function loadUsers() {
  const tableBody = document.getElementById('users-table-body');
  try {
    people = await apiFetch('/users');
    renderUsersTable();
  } catch {
    people = [];
    tableBody.innerHTML = '<tr><td>No se pudo cargar la lista de personas.</td></tr>';
  }
}

const newPersonPanel = document.getElementById('new-person-panel');
const newPersonForm = document.getElementById('new-person-form');

document.getElementById('toggle-new-person').addEventListener('click', () => {
  newPersonPanel.hidden = !newPersonPanel.hidden;
});

document.getElementById('cancel-new-person').addEventListener('click', () => {
  newPersonForm.reset();
  newPersonPanel.hidden = true;
});

newPersonForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  const formData = new FormData(newPersonForm);
  const optional = (key) => formData.get(key)?.toString().trim() || null;

  const body = {
    first_name: formData.get('first_name'),
    last_name: formData.get('last_name'),
    email: formData.get('email'),
    phone: optional('phone'),
    national_id: optional('national_id'),
    birth_date: optional('birth_date'),
    address: optional('address'),
    postal_code: optional('postal_code'),
    city: optional('city'),
    province: optional('province'),
    emergency_contact: optional('emergency_contact'),
    role_names: formData.getAll('role_names'),
  };

  const submitButton = newPersonForm.querySelector('button[type="submit"]');
  submitButton.disabled = true;
  try {
    await apiFetch('/users', { method: 'POST', body: JSON.stringify(body) });
    showFeedback('Persona creada.');
    newPersonForm.reset();
    newPersonPanel.hidden = true;
    await loadUsers();
  } catch (err) {
    showError(err.message);
  } finally {
    submitButton.disabled = false;
  }
});

await loadRoles();
loadUsers();
