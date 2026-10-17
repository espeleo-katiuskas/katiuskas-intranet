import {
  hasAnyRole,
  apiFetch,
  escapeHtml,
  formatDate,
  knownRoleLabels,
  roleLabel,
  positionLabels,
  showFeedback,
  showError,
} from './api.js';
import { initPage } from './layout.js';
import { initModal, openModal } from './modal.js';

const user = await initPage();
// isAdmin means "full read access" (admin or board) — a plain member
// still gets this page, but with far fewer fields (see MEMBER_COLUMNS/
// MEMBER_FILTERS) and none of the admin/board management UI, which
// applyRoleVisibility() already strips via each element's data-roles.
const isAdmin = hasAnyRole(user, ['admin', 'board']);
const isRealAdmin = hasAnyRole(user, ['admin']);

// Row filters and column visibility both start hidden behind one
// "Filtros…" button — plain rows/columns, not a dialog, so there's
// nothing to initModal() here; just toggling .hidden on the panel
// underneath the button.
const filtersPanel = document.getElementById('filters-panel');
document.getElementById('toggle-filters').addEventListener('click', () => {
  filtersPanel.hidden = !filtersPanel.hidden;
});

// Filled by loadBoardPositions() — login → their current board
// position, for whoever is on the board right now.
let boardPositionByLogin = {};

// A person's "cargo" is their current board position, and nothing
// else: a plain member has no business knowing which other roles
// (admin, money, material, any custom label) someone holds — that's
// exactly the kind of internal, non-public information MemberListing
// itself already omits for them (see docs/architecture.md's own
// Permisos section) — so this never falls back to person.roles the way
// it once did, even for admin/board's own richer view of this same
// column.
function cargoFor(person) {
  const position = boardPositionByLogin[person.login];
  return position ? (positionLabels[position] ?? position) : '';
}

// The roles an admin can give someone when creating them ("Nueva persona"
// dialog, admin-only like the button that opens it). The list of roles
// itself, to create or delete one, lives on /admin.
async function loadRolesForNewPerson() {
  try {
    const allRoles = await apiFetch('/roles');
    const group = document.getElementById('np-roles-group');
    group.innerHTML = allRoles
      .filter((role) => !role.is_automatic)
      .map((role) => `
        <label>
          <input type="checkbox" name="role_names" value="${escapeHtml(role.name)}">
          ${escapeHtml(roleLabel(role.name))}
        </label>
      `)
      .join('');
  } catch {
    showError('No se pudieron cargar los roles.');
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

// The people list is purely informational and sortable: every action
// about a specific person (edit their data, manage their roles, register
// an alta/baja, delete them) lives on their own page (/socio?login=…) now.
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
  default: false,
  value: (p) => membershipStatusLabels[membershipStatusFor(p)],
  render: (person) => membershipStatusLabels[membershipStatusFor(person)],
  compare: (a, b) => MEMBERSHIP_STATUS_ORDER.indexOf(membershipStatusFor(a)) - MEMBERSHIP_STATUS_ORDER.indexOf(membershipStatusFor(b)),
};

// Same reasoning — a plain member's own MemberListing carries
// license_type too now (just the type, not the issued/valid-until
// dates admin/board still get).
const licenseTypeColumn = { ...textColumn('license_type', 'Tipo de licencia', (p) => p.license_type ?? ''), default: true };

// Latest year the person has paid their cuota (users.User.LastDueYear,
// also on MemberListing — the date it was paid is not public). Hidden
// by default: it is for checking who is up to date, which the
// "a corriente de pago" filter below does without a column.
const lastDueYearColumn = { ...numberColumn('last_due_year', 'Última cuota', (p) => p.last_due_year ?? null), default: false };

// "A corriente de pago" means the cuota of the current year is paid.
const duesStatusLabels = { upToDate: 'A corriente de pago', behind: 'No a corriente de pago' };
function duesStatusFor(person) {
  return (person.last_due_year ?? 0) >= new Date().getFullYear() ? duesStatusLabels.upToDate : duesStatusLabels.behind;
}
// "Federado" means having a license_type: GET /users only reports the
// license for the current year, so there is one exactly when they are.
const federatedLabels = { yes: 'Federado', no: 'No federado' };
const duesFilter = {
  key: 'dues_status',
  label: 'Cuota anual',
  options: Object.values(duesStatusLabels),
  value: duesStatusFor,
};
const federatedFilter = {
  key: 'federated',
  label: 'Federación',
  options: Object.values(federatedLabels),
  value: (p) => (p.license_type ? federatedLabels.yes : federatedLabels.no),
};

// Row filters narrow which people show, independent of which columns are
// visible — same show/hide idea as COLUMNS, but for rows. Each groups
// people into a fixed, known-in-advance set of values (not derived from
// whatever happens to be loaded) so the filter UI can render before the
// data even arrives; every value starts checked (a no-op) except
// membership_status's own "Ex-socio" — see activeFilterValues below.
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
  duesFilter,
  federatedFilter,
];

// GET /users gives a plain member first_name/last_name/gender/
// membership_number/membership_start_date/membership_end_date/
// on_leave/age_category (see users.MemberListing) — enough to
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
  duesFilter,
  federatedFilter,
];

const FILTERS = isAdmin ? FULL_FILTERS : MEMBER_FILTERS;

// Every filter starts with every option checked (a no-op) except
// membership_status, which starts with only "Activo"/"Baja temporal"
// checked — this page's own default view is meant for the club's
// current membership, not its history or people who never joined at
// all, so neither a former member nor someone who's never been a socio
// shows up until someone deliberately asks for them.
// restoreViewState() below still overrides this from localStorage when
// a saved preference exists.
const activeFilterValues = Object.fromEntries(FILTERS.map((f) => {
  const options = f.key === 'membership_status'
    ? f.options.filter((opt) => opt === membershipStatusLabels.active || opt === membershipStatusLabels.on_leave)
    : f.options;
  return [f.key, new Set(options)];
}));

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
  { ...numberColumn('membership_number', 'N.º socio', (p) => p.membership_number ?? null), default: true },
  { ...textColumn('first_name', 'Nombre', (p) => p.first_name), default: true },
  { ...textColumn('last_name', 'Apellidos', (p) => p.last_name), default: true },
  { ...textColumn('gender', 'Género', (p) => (p.gender ? genderLabels[p.gender] ?? p.gender : '')), default: false },
  { ...textColumn('national_id', 'DNI', (p) => p.national_id ?? ''), default: false },
  { ...dateColumn('birth_date', 'Fecha de nacimiento', (p) => p.birth_date), default: false },
  { ...textColumn('phone', 'Teléfono', (p) => p.phone ?? ''), default: false },
  { ...textColumn('email', 'Email', (p) => p.email ?? ''), default: false },
  { ...textColumn('address', 'Dirección', (p) => p.address ?? ''), default: false },
  { ...textColumn('postal_code', 'Código postal', (p) => p.postal_code ?? ''), default: false },
  { ...textColumn('city', 'Ciudad', (p) => p.city ?? ''), default: false },
  { ...textColumn('province', 'Provincia', (p) => p.province ?? ''), default: false },
  { ...dateColumn('membership_start_date', 'Fecha de alta', (p) => p.membership_start_date), default: false },
  { ...dateColumn('membership_end_date', 'Fecha de baja', (p) => p.membership_end_date), default: false },
  {
    key: 'category',
    label: 'Categoría',
    default: true,
    value: categoryFor,
    render: categoryFor,
    compare: (a, b) => CATEGORY_ORDER.indexOf(categoryFor(a)) - CATEGORY_ORDER.indexOf(categoryFor(b)),
  },
  { ...membershipStatusColumn },
  {
    key: 'cargo',
    label: 'Cargo',
    default: false,
    value: cargoFor,
    render: (person) => cargoFor(person) || '—',
    compare: (a, b) => cargoFor(a).localeCompare(cargoFor(b), 'es'),
  },
  { ...licenseTypeColumn },
  { ...lastDueYearColumn },
];

// GET /users only sends a plain member these fields for anyone but
// themselves (see users.MemberListing) — the rest of FULL_COLUMNS would
// just render as "—" for every row, so they're not offered at all.
const MEMBER_COLUMNS = [
  { ...numberColumn('membership_number', 'N.º socio', (p) => p.membership_number ?? null), default: true },
  { ...textColumn('first_name', 'Nombre', (p) => p.first_name), default: true },
  { ...textColumn('last_name', 'Apellidos', (p) => p.last_name), default: true },
  { ...textColumn('gender', 'Género', (p) => (p.gender ? genderLabels[p.gender] ?? p.gender : '')), default: false },
  { ...dateColumn('membership_start_date', 'Fecha de alta', (p) => p.membership_start_date), default: false },
  { ...dateColumn('membership_end_date', 'Fecha de baja', (p) => p.membership_end_date), default: false },
  {
    key: 'category',
    label: 'Categoría',
    default: true,
    value: memberCategoryFor,
    render: memberCategoryFor,
    compare: (a, b) => AGE_CATEGORY_ORDER.indexOf(a.age_category) - AGE_CATEGORY_ORDER.indexOf(b.age_category),
  },
  { ...membershipStatusColumn },
  {
    key: 'cargo',
    label: 'Cargo',
    default: false,
    value: cargoFor,
    render: (person) => cargoFor(person) || '—',
    compare: (a, b) => cargoFor(a).localeCompare(cargoFor(b), 'es'),
  },
  { ...licenseTypeColumn },
  { ...lastDueYearColumn },
];

const COLUMNS = isAdmin ? FULL_COLUMNS : MEMBER_COLUMNS;
const columnsByKey = Object.fromEntries(COLUMNS.map((c) => [c.key, c]));

let visibleColumns = new Set(COLUMNS.filter((c) => c.default).map((c) => c.key));
let sortKey = 'first_name';
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

// "N personas en esta vista": how many rows the current filters leave,
// not how many people are on file. Hidden while there's nothing to count
// (the list hasn't loaded, or failed to).
function setPeopleCount(count) {
  const element = document.getElementById('people-count');
  element.hidden = count == null;
  if (count != null) element.textContent = `${count} ${count === 1 ? 'persona' : 'personas'} en esta vista`;
}

function renderUsersTable() {
  renderFilters();
  renderColumnToggle();
  renderUsersTableHead();

  const tableBody = document.getElementById('users-table-body');
  if (people.length === 0) {
    setPeopleCount(null);
    tableBody.innerHTML = `<tr><td colspan="${Math.max(visibleColumns.size, 1)}">Todavía no hay personas registradas.</td></tr>`;
    return;
  }

  const visible = filteredPeople();
  setPeopleCount(visible.length);
  if (visible.length === 0) {
    tableBody.innerHTML = `<tr><td colspan="${Math.max(visibleColumns.size, 1)}">Ninguna persona coincide con los filtros.</td></tr>`;
    return;
  }

  const column = columnsByKey[sortKey];
  const sorted = visible.slice().sort((a, b) => (sortDir === 'asc' ? column.compare(a, b) : column.compare(b, a)));
  tableBody.innerHTML = sorted.map(renderUserRow).join('');
}

// /socio?login=… is still admin/board-only — a plain member's row click
// wouldn't go anywhere but back here, so it's not wired up at all, and
// the row-hover affordance is removed to match.
if (isAdmin) {
  document.getElementById('users-table-body').addEventListener('click', (event) => {
    const row = event.target.closest('tr[data-person-login]');
    if (!row) return;
    window.location.href = `/socio?login=${row.dataset.personLogin}`;
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
    setPeopleCount(null);
    tableBody.innerHTML = '<tr><td>No se pudo cargar la lista de personas.</td></tr>';
  }
}

// "+ Nueva persona" has data-roles="admin" in the markup — POST /users
// is admin-only now, so there's nothing to wire up for anyone else
// (applyRoleVisibility already removed the button, panel, and form).
if (isRealAdmin) {
  const newPersonDialog = document.getElementById('new-person-dialog');
  const newPersonForm = document.getElementById('new-person-form');
  initModal(newPersonDialog, newPersonForm);

  document.getElementById('toggle-new-person').addEventListener('click', () => {
    openModal(newPersonDialog);
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
      newPersonDialog.close();
      await loadUsers();
    } catch (err) {
      showError(err.message);
    } finally {
      submitButton.disabled = false;
    }
  });
}

// The "Cargo" column only ever needs board positions (see cargoFor); the
// role catalog is only for the admin's "nueva persona" checkboxes.
if (isRealAdmin) {
  await loadRolesForNewPerson();
}
await loadBoardPositions();
loadUsers();
