import {
  initSession,
  logout,
  applyRoleVisibility,
  loadTopbarGreeting,
  currentUser,
  hasAnyRole,
  apiFetch,
  escapeHtml,
  formatDate,
} from './api.js';

await initSession();
applyRoleVisibility();
document.getElementById('logout-button').addEventListener('click', logout);
loadTopbarGreeting();

const user = currentUser();
// isAdmin means "full read access" (admin or board) — a plain member
// still gets this page, but with far fewer fields (see MEMBER_COLUMNS/
// MEMBER_FILTERS) and none of the admin/board management UI, which
// applyRoleVisibility() already strips via each element's data-roles.
const isAdmin = hasAnyRole(user, ['admin', 'board']);
const isRealAdmin = hasAnyRole(user, ['admin']);

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
// existing person's roles happens on their own page (/member?login=…) now.
const knownRoleLabels = { admin: 'Administrador', board: 'Junta', member: 'Socio', former_member: 'Ex-socio' };
function roleLabel(name) {
  return knownRoleLabels[name] ?? name;
}

// Same friendly labels as member.js/dashboard.js — kept in sync there
// since neither file is big enough yet to justify sharing them through
// api.js.
const positionLabels = {
  president: 'Presidente',
  vice_president: 'Vicepresidente',
  secretary: 'Secretario',
  treasurer: 'Tesorero',
  member_at_large: 'Vocal',
};

let allRoles = [];
// Role names is_automatic=true (member/former_member/board — see
// db/migrations/00003_roles.sql) never belong in the "Cargo" column:
// they're computed from alta/baja and board-term history, not
// something anyone actually holds as a distinct position or label.
let automaticRoleNames = new Set();
// Filled by loadBoardPositions() — login → their current board
// position, for whoever is on the board right now.
let boardPositionByLogin = {};

// A person's "cargo": their current board position if they have one,
// otherwise whichever non-automatic roles they hold (admin, or any
// custom role an admin created) — never both, since holding a board
// position already implies being on the Junta Directiva.
function cargoFor(person) {
  const position = boardPositionByLogin[person.login];
  if (position) return positionLabels[position] ?? position;
  return (person.roles ?? []).filter((name) => !automaticRoleNames.has(name)).map(roleLabel).join(', ');
}

// Just the catalog fetch, no DOM — used by both loadRoles() below (the
// admin/board-only roles table + "nueva persona" checkboxes) and, for
// everyone else, only to know which role names are automatic, for
// cargoFor.
async function loadRolesCatalog() {
  allRoles = await apiFetch('/roles');
  automaticRoleNames = new Set(allRoles.filter((r) => r.is_automatic).map((r) => r.name));
}

async function loadRoles() {
  const tableBody = document.getElementById('roles-table-body');
  try {
    await loadRolesCatalog();
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

// GET /board-members is open to any baseline role — loaded for
// everyone now, since the "Cargo" column is offered in both
// FULL_COLUMNS and MEMBER_COLUMNS below.
async function loadBoardPositions() {
  try {
    const boardMembers = await apiFetch('/board-members');
    boardPositionByLogin = Object.fromEntries(boardMembers.map((m) => [m.user_login, m.position]));
  } catch {
    boardPositionByLogin = {};
  }
}

// The whole "Roles" section has data-roles="admin"/"admin,board" in the
// markup, so applyRoleVisibility() removes it from the DOM entirely for
// a plain member — nothing here to wire up in that case.
if (isAdmin) {
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
}

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
// an alta/baja, delete them) lives on their own page (/member?login=…) now.
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

function numberColumn(key, label, getValue) {
  return {
    key,
    label,
    value: getValue,
    render: (person) => {
      const raw = getValue(person);
      return raw == null ? '—' : String(raw);
    },
    // Missing values (never assigned one) sort first.
    compare: (a, b) => (getValue(a) ?? -Infinity) - (getValue(b) ?? -Infinity),
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

// Género is stored as-is; categoría never is — it's derived from
// birth_date and the current year, the same way any other page computes
// something from data it already has instead of asking the API for it.
const genderLabels = { male: 'Masculino', female: 'Femenino' };

// The club's own age category (per its normativa), based on the age a
// person turns *this calendar year* (not today's exact age) — everyone
// holds the same category from January 1st through December 31st,
// whatever age they turn that year: alevín up to 9, infantil 10-13,
// juvenil 14-17, mayor 18-44, veterano A 45-54, veterano B 55 and up.
// birth_date is mandatory (users.CreateInput/db NOT NULL) — nobody in
// the system can lack one, so there's no "unknown category" case to
// handle here.
const CATEGORY_ORDER = ['Alevín', 'Infantil', 'Juvenil', 'Mayor', 'Veterano A', 'Veterano B'];
function categoryFor(person) {
  const ageThisYear = new Date().getFullYear() - new Date(person.birth_date).getFullYear();
  if (ageThisYear <= 9) return 'Alevín';
  if (ageThisYear <= 13) return 'Infantil';
  if (ageThisYear <= 17) return 'Juvenil';
  if (ageThisYear <= 44) return 'Mayor';
  if (ageThisYear <= 54) return 'Veterano A';
  return 'Veterano B';
}

// A plain member's listing doesn't include birth_date (see
// users.MemberListing) — just the already-bucketed age_category the API
// computes from it (youth/child/junior/senior/veteran_a/veteran_b,
// always present, same guarantee as categoryFor's own birth_date
// above — the English identifiers are this codebase's own convention,
// not what the normativa itself calls them, see users.AgeCategory), so
// this maps that instead of deriving it locally like categoryFor does
// for the full listing.
const AGE_CATEGORY_LABELS = {
  youth: 'Alevín',
  child: 'Infantil',
  junior: 'Juvenil',
  senior: 'Mayor',
  veteran_a: 'Veterano A',
  veteran_b: 'Veterano B',
};
const AGE_CATEGORY_ORDER = ['youth', 'child', 'junior', 'senior', 'veteran_a', 'veteran_b'];
function memberCategoryFor(person) {
  return AGE_CATEGORY_LABELS[person.age_category];
}

// A person's membership status for filtering purposes isn't just the
// alta/baja dates: someone on a temporary leave still counts as "active"
// for role/access purposes (see docs/architecture.md), but should filter
// separately from both "activo" and "ex-socio".
const membershipStatusLabels = {
  active: 'Activo',
  on_leave: 'Baja temporal',
  former: 'Ex-socio',
  none: 'Nunca ha sido socio',
};
function membershipStatusFor(person) {
  if (!person.membership_start_date) return 'none';
  if (person.membership_end_date) return 'former';
  return person.on_leave ? 'on_leave' : 'active';
}
const MEMBERSHIP_STATUS_ORDER = ['active', 'on_leave', 'former', 'none'];

// Shared between FULL_COLUMNS and MEMBER_COLUMNS — a plain member's own
// MemberListing carries membership_start_date/end_date/on_leave same as
// User does, so this column works identically for both.
const membershipStatusColumn = {
  key: 'membership_status',
  label: 'Tipo de socio',
  default: true,
  value: (p) => membershipStatusLabels[membershipStatusFor(p)],
  render: (person) => membershipStatusLabels[membershipStatusFor(person)],
  compare: (a, b) => MEMBERSHIP_STATUS_ORDER.indexOf(membershipStatusFor(a)) - MEMBERSHIP_STATUS_ORDER.indexOf(membershipStatusFor(b)),
};

// Same reasoning — a plain member's own MemberListing carries
// license_type too now (just the type, not the issued/valid-until
// dates admin/board still get).
const licenseTypeColumn = { ...textColumn('license_type', 'Tipo de licencia', (p) => p.license_type ?? ''), default: false };

// Row filters narrow which people show, independent of which columns are
// visible — same show/hide idea as COLUMNS, but for rows. Each groups
// people into a fixed, known-in-advance set of values (not derived from
// whatever happens to be loaded) so the filter UI can render before the
// data even arrives, and every value starts checked so filtering opens
// as a no-op.
const FULL_FILTERS = [
  {
    key: 'gender',
    label: 'Género',
    options: ['Masculino', 'Femenino', 'Sin especificar'],
    value: (p) => (p.gender ? genderLabels[p.gender] ?? p.gender : 'Sin especificar'),
  },
  {
    key: 'membership_status',
    label: 'Tipo de socio',
    options: Object.values(membershipStatusLabels),
    value: (p) => membershipStatusLabels[membershipStatusFor(p)],
  },
  {
    key: 'category',
    label: 'Categoría',
    options: [...CATEGORY_ORDER],
    value: categoryFor,
  },
];

// GET /users gives a plain member first_name/last_name/gender/
// membership_number/membership_start_date/membership_end_date/
// on_leave/age_category/roles (see users.MemberListing) — enough to
// offer the same filters as FULL_FILTERS, just built from
// memberCategoryFor instead of categoryFor (age_category, not
// birth_date, is what's actually there). membership_status drops
// "Nunca ha sido socio": GET /users never actually sends a plain
// member anyone who's never been a socio in the first place, so that
// option would always be empty.
const MEMBER_FILTERS = [
  {
    key: 'gender',
    label: 'Género',
    options: ['Masculino', 'Femenino', 'Sin especificar'],
    value: (p) => (p.gender ? genderLabels[p.gender] ?? p.gender : 'Sin especificar'),
  },
  {
    key: 'membership_status',
    label: 'Tipo de socio',
    options: [membershipStatusLabels.active, membershipStatusLabels.on_leave, membershipStatusLabels.former],
    value: (p) => membershipStatusLabels[membershipStatusFor(p)],
  },
  {
    key: 'category',
    label: 'Categoría',
    options: [...CATEGORY_ORDER],
    value: memberCategoryFor,
  },
];

const FILTERS = isAdmin ? FULL_FILTERS : MEMBER_FILTERS;

const activeFilterValues = Object.fromEntries(FILTERS.map((f) => [f.key, new Set(f.options)]));

function renderFilters() {
  const container = document.getElementById('row-filters');
  container.innerHTML = FILTERS
    .map((f) => `
      <div class="filter-group">
        <span class="filter-group-label">${escapeHtml(f.label)}</span>
        ${f.options
          .map((opt) => `
            <label>
              <input type="checkbox" data-filter="${f.key}" value="${escapeHtml(opt)}" ${activeFilterValues[f.key].has(opt) ? 'checked' : ''}>
              ${escapeHtml(opt)}
            </label>
          `)
          .join('')}
      </div>
    `)
    .join('');
}

document.getElementById('row-filters').addEventListener('change', (event) => {
  const checkbox = event.target.closest('input[data-filter]');
  if (!checkbox) return;
  const values = activeFilterValues[checkbox.dataset.filter];
  if (checkbox.checked) {
    values.add(checkbox.value);
  } else {
    values.delete(checkbox.value);
  }
  saveViewState();
  renderUsersTable();
});

function filteredPeople() {
  return people.filter((p) => FILTERS.every((f) => activeFilterValues[f.key].has(f.value(p))));
}

const FULL_COLUMNS = [
  { ...numberColumn('membership_number', 'N.º de socio', (p) => p.membership_number ?? null), default: false },
  { ...textColumn('first_name', 'Nombre', (p) => p.first_name), default: true },
  { ...textColumn('last_name', 'Apellidos', (p) => p.last_name), default: true },
  { ...textColumn('gender', 'Género', (p) => (p.gender ? genderLabels[p.gender] ?? p.gender : '')), default: false },
  { ...textColumn('national_id', 'DNI', (p) => p.national_id ?? ''), default: false },
  { ...dateColumn('birth_date', 'Fecha de nacimiento', (p) => p.birth_date), default: true },
  { ...textColumn('phone', 'Teléfono', (p) => p.phone ?? ''), default: false },
  { ...textColumn('email', 'Email', (p) => p.email ?? ''), default: false },
  { ...textColumn('address', 'Dirección', (p) => p.address ?? ''), default: false },
  { ...textColumn('postal_code', 'Código postal', (p) => p.postal_code ?? ''), default: false },
  { ...textColumn('city', 'Ciudad', (p) => p.city ?? ''), default: false },
  { ...textColumn('province', 'Provincia', (p) => p.province ?? ''), default: false },
  { ...dateColumn('membership_start_date', 'Fecha de alta', (p) => p.membership_start_date), default: true },
  { ...dateColumn('membership_end_date', 'Fecha de baja', (p) => p.membership_end_date), default: true },
  {
    key: 'category',
    label: 'Categoría',
    default: false,
    value: categoryFor,
    render: categoryFor,
    compare: (a, b) => CATEGORY_ORDER.indexOf(categoryFor(a)) - CATEGORY_ORDER.indexOf(categoryFor(b)),
  },
  { ...membershipStatusColumn },
  {
    key: 'cargo',
    label: 'Cargo',
    default: true,
    value: cargoFor,
    render: (person) => cargoFor(person) || '—',
    compare: (a, b) => cargoFor(a).localeCompare(cargoFor(b), 'es'),
  },
  { ...licenseTypeColumn },
];

// GET /users only sends a plain member these fields for anyone but
// themselves (see users.MemberListing) — the rest of FULL_COLUMNS would
// just render as "—" for every row, so they're not offered at all.
const MEMBER_COLUMNS = [
  { ...numberColumn('membership_number', 'N.º de socio', (p) => p.membership_number ?? null), default: true },
  { ...textColumn('first_name', 'Nombre', (p) => p.first_name), default: true },
  { ...textColumn('last_name', 'Apellidos', (p) => p.last_name), default: true },
  { ...textColumn('gender', 'Género', (p) => (p.gender ? genderLabels[p.gender] ?? p.gender : '')), default: true },
  { ...dateColumn('membership_start_date', 'Fecha de alta', (p) => p.membership_start_date), default: true },
  { ...dateColumn('membership_end_date', 'Fecha de baja', (p) => p.membership_end_date), default: true },
  {
    key: 'category',
    label: 'Categoría',
    default: false,
    value: memberCategoryFor,
    render: memberCategoryFor,
    compare: (a, b) => AGE_CATEGORY_ORDER.indexOf(a.age_category) - AGE_CATEGORY_ORDER.indexOf(b.age_category),
  },
  { ...membershipStatusColumn },
  {
    key: 'cargo',
    label: 'Cargo',
    default: true,
    value: cargoFor,
    render: (person) => cargoFor(person) || '—',
    compare: (a, b) => cargoFor(a).localeCompare(cargoFor(b), 'es'),
  },
  { ...licenseTypeColumn },
];

const COLUMNS = isAdmin ? FULL_COLUMNS : MEMBER_COLUMNS;
const columnsByKey = Object.fromEntries(COLUMNS.map((c) => [c.key, c]));

let visibleColumns = new Set(COLUMNS.filter((c) => c.default).map((c) => c.key));
let sortKey = 'last_name';
let sortDir = 'asc';
let people = [];

// Persist the whole view — filters, visible columns, sort — across
// navigation, so leaving to a person's page and clicking "Volver a
// socios" lands back on the same view instead of a reset one. Per
// browser only, and never worth failing the page over (private
// browsing, a full quota...), so every read/write is best-effort.
const VIEW_STATE_KEY = 'katiuskas:members-view';

function saveViewState() {
  try {
    localStorage.setItem(VIEW_STATE_KEY, JSON.stringify({
      visibleColumns: [...visibleColumns],
      sortKey,
      sortDir,
      filters: Object.fromEntries(FILTERS.map((f) => [f.key, [...activeFilterValues[f.key]]])),
    }));
  } catch {
    // Not persisting the view isn't worth surfacing to the user.
  }
}

// Applied once at startup. Unknown column keys or filter values (e.g.
// after a column gets renamed or removed) are dropped rather than
// crashing the page on stale saved state.
function restoreViewState() {
  let saved;
  try {
    saved = JSON.parse(localStorage.getItem(VIEW_STATE_KEY));
  } catch {
    return;
  }
  if (!saved) return;

  if (Array.isArray(saved.visibleColumns)) {
    const known = saved.visibleColumns.filter((key) => columnsByKey[key]);
    if (known.length > 0) visibleColumns = new Set(known);
  }
  if (typeof saved.sortKey === 'string' && columnsByKey[saved.sortKey]) {
    sortKey = saved.sortKey;
  }
  if (saved.sortDir === 'asc' || saved.sortDir === 'desc') {
    sortDir = saved.sortDir;
  }
  if (saved.filters) {
    for (const f of FILTERS) {
      const values = saved.filters[f.key];
      if (Array.isArray(values)) {
        activeFilterValues[f.key] = new Set(values.filter((v) => f.options.includes(v)));
      }
    }
  }
}
restoreViewState();

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
  saveViewState();
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
  saveViewState();
  renderUsersTable();
});

function renderUserRow(person) {
  const cells = COLUMNS
    .filter((c) => visibleColumns.has(c.key))
    .map((c) => `<td>${c.render(person)}</td>`)
    .join('');
  return `<tr data-person-login="${person.login}">${cells}</tr>`;
}

function renderUsersTable() {
  renderFilters();
  renderColumnToggle();
  renderUsersTableHead();

  const tableBody = document.getElementById('users-table-body');
  if (people.length === 0) {
    tableBody.innerHTML = `<tr><td colspan="${Math.max(visibleColumns.size, 1)}">Todavía no hay personas registradas.</td></tr>`;
    return;
  }

  const visible = filteredPeople();
  if (visible.length === 0) {
    tableBody.innerHTML = `<tr><td colspan="${Math.max(visibleColumns.size, 1)}">Ninguna persona coincide con los filtros.</td></tr>`;
    return;
  }

  const column = columnsByKey[sortKey];
  const sorted = visible.slice().sort((a, b) => (sortDir === 'asc' ? column.compare(a, b) : column.compare(b, a)));
  tableBody.innerHTML = sorted.map(renderUserRow).join('');
}

// /member?login=… is still admin/board-only — a plain member's row click
// wouldn't go anywhere but back here, so it's not wired up at all, and
// the row-hover affordance is removed to match.
if (isAdmin) {
  document.getElementById('users-table-body').addEventListener('click', (event) => {
    const row = event.target.closest('tr[data-person-login]');
    if (!row) return;
    window.location.href = `/member?login=${row.dataset.personLogin}`;
  });
} else {
  document.getElementById('users-table-body').classList.remove('clickable-rows');
}

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

// "+ Nueva persona" has data-roles="admin" in the markup — POST /users
// is admin-only now, so there's nothing to wire up for anyone else
// (applyRoleVisibility already removed the button, panel, and form).
if (isRealAdmin) {
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
      birth_date: formData.get('birth_date'),
      email: optional('email'),
      phone: optional('phone'),
      national_id: optional('national_id'),
      gender: optional('gender'),
      address: optional('address'),
      postal_code: optional('postal_code'),
      city: optional('city'),
      province: optional('province'),
      emergency_contact: optional('emergency_contact'),
      role_names: formData.getAll('role_names'),
    };

    const membershipStartDate = optional('membership_start_date');

    const submitButton = newPersonForm.querySelector('button[type="submit"]');
    submitButton.disabled = true;
    try {
      const person = await apiFetch('/users', { method: 'POST', body: JSON.stringify(body) });
      let feedback = 'Persona creada.';
      if (membershipStartDate) {
        try {
          await apiFetch(`/users/${person.login}/memberships`, {
            method: 'POST',
            body: JSON.stringify({ start_date: membershipStartDate }),
          });
          feedback = 'Persona creada y dada de alta como socio.';
        } catch (err) {
          feedback = `Persona creada, pero no se pudo dar de alta: ${err.message}`;
        }
      }
      showFeedback(feedback);
      newPersonForm.reset();
      newPersonPanel.hidden = true;
      await loadUsers();
    } catch (err) {
      showError(err.message);
    } finally {
      submitButton.disabled = false;
    }
  });
}

// Both feed the "Cargo" column, needed for admin/board and a plain
// member alike now — loadRoles() also renders the admin/board-only
// roles table and "nueva persona" checkboxes, so only that half is
// gated to isAdmin.
if (isAdmin) {
  await loadRoles();
} else {
  try {
    await loadRolesCatalog();
  } catch {
    // Cargo just won't resolve non-automatic roles if this fails — not
    // worth blocking the rest of the page over.
  }
}
await loadBoardPositions();
loadUsers();
