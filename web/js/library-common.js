import {
  API_BASE,
  apiFetch,
  escapeHtml,
  formatDate,
  personPickerLabeler,
  renderPickerOptions,
} from './api.js';

// What the pages of the library (/biblioteca and /ejemplar) share.

// A date the API gives as "2026-10-01" (a day, with no time zone), shown as the rest of the intranet
// shows dates. new Date('2026-10-01') would be midnight UTC, which is the day before in some zones.
function dateOnly(iso) {
  const [year, month, day] = iso.split('-').map(Number);
  return formatDate(new Date(year, month - 1, day));
}

// The day it is for the browser, "2026-11-12", for the date fields: none of the dates of the library
// can be in the future.
function today() {
  const now = new Date();
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`;
}

// Why an item left the library for good, as the API names it, and how a page says it.
const END_REASONS = { lost: 'Perdido', withdrawn: 'Retirado', sold: 'Vendido' };

// The badge of an item: whether it is in the library (available or out on loan) or has left it.
function statusBadge(item) {
  if (item.end_reason) {
    return `<span class="badge badge-gone">${END_REASONS[item.end_reason] ?? 'Baja'}</span>`;
  }
  return item.available
    ? '<span class="badge badge-ok">Disponible</span>'
    : '<span class="badge badge-out">Prestado</span>';
}

// Where the cover of an item is: an <img> can point straight at it (the session cookie is enough). The
// address does not change when the cover does, so the browser is told to ask again (the API answers
// 304 when nothing changed).
function coverUrl(itemId) {
  return `${API_BASE}/library/items/${itemId}/cover`;
}

function coverImage(item, className) {
  return item.has_cover
    ? `<img src="${coverUrl(item.id)}" alt="Portada de «${escapeHtml(item.title)}»" class="${className}" loading="lazy">`
    : `<span class="${className} library-cover-empty" aria-hidden="true"></span>`;
}

// The names of the people (from the directory, open to every signed-in user) and the members, as entries
// for a picker: an item can only be lent to a member.
async function loadPeople() {
  let namesByLogin = {};
  let memberEntries = [];
  try {
    const directory = await apiFetch('/users/directory');
    namesByLogin = Object.fromEntries(
      directory.map((p) => [p.login, `${p.first_name} ${p.last_name}`]),
    );
    const labelOf = personPickerLabeler(directory);
    memberEntries = directory
      .filter((p) => p.member)
      .sort((a, b) =>
        `${a.first_name} ${a.last_name}`.localeCompare(`${b.first_name} ${b.last_name}`, 'es'),
      )
      .map((p) => ({ id: p.login, label: labelOf(p) }));
  } catch {
    // Without names the logins show instead, and there is no list of members to choose from.
  }
  return { namesByLogin, memberEntries, memberOptions: renderPickerOptions(memberEntries) };
}

// The types of item (book, magazine, map…), as the API lists them: with how many items have each.
function loadTypes() {
  return apiFetch('/library/types');
}

// The <option>s of a select of types, with one chosen (or none, so the person has to choose).
function typeOptions(types, selectedId) {
  const choose = selectedId ? '' : '<option value="" disabled selected>Elige un tipo…</option>';
  return (
    choose +
    types
      .map(
        (t) =>
          `<option value="${t.id}"${t.id === selectedId ? ' selected' : ''}>${escapeHtml(t.name)}</option>`,
      )
      .join('')
  );
}

function personLink(namesByLogin, login) {
  return `<a href="/socio?login=${encodeURIComponent(login)}">${escapeHtml(namesByLogin[login] ?? login)}</a>`;
}

export {
  coverImage,
  coverUrl,
  dateOnly,
  END_REASONS,
  loadPeople,
  loadTypes,
  personLink,
  statusBadge,
  today,
  typeOptions,
};
