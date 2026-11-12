import { apiFetch, escapeHtml, formatDate, showError, showFeedback } from './api.js';
import { initPage } from './layout.js';
import { itemLabel } from './material-common.js';

// The material that is still the club's but is not in it right now: what is out on loan (the units of
// each item that its loans have not got back, summed over every activity, and for which activities)
// and the rentals still open. The page is for those who manage the material (the API's
// `/material/movements` is for them too).
await initPage({ requireAnyRole: ['admin', 'board', 'material'] });

const loanedBody = document.getElementById('loaned-table-body');
const rentedBody = document.getElementById('rented-table-body');
let itemsById = {};
let people = {};

const personName = (login) => {
  const person = people[login];
  return person ? `${person.first_name} ${person.last_name}` : login;
};

// What each activity still has out of an item: its loans minus its returns, activity by activity
// (a return is not tied to a loan, but to the item and the activity), only those with something out.
function outstandingByActivity(movements) {
  const byActivity = new Map();
  for (const m of movements) {
    if ((m.type !== 'loan' && m.type !== 'return') || m.activity_id == null) continue;
    byActivity.set(
      m.activity_id,
      (byActivity.get(m.activity_id) ?? 0) + (m.type === 'loan' ? m.quantity : -m.quantity),
    );
  }
  return [...byActivity].filter(([, outstanding]) => outstanding > 0);
}

async function loadLoaned() {
  try {
    const loaned = Object.values(itemsById).filter((item) => item.outstanding_loan_quantity > 0);
    if (loaned.length === 0) {
      loanedBody.innerHTML =
        '<tr><td colspan="3">No hay material prestado pendiente de devolver.</td></tr>';
      return;
    }
    // Which activities each item is out for: its own movements, and the titles of those activities.
    const movementsOf = await Promise.all(
      loaned.map((item) => apiFetch(`/material/items/${item.id}/movements`)),
    );
    const perItem = loaned.map((item, i) => [item, outstandingByActivity(movementsOf[i])]);
    const activityIds = [...new Set(perItem.flatMap(([, list]) => list.map(([id]) => id)))];
    const titles = Object.fromEntries(
      await Promise.all(
        activityIds.map(async (id) => [id, (await apiFetch(`/activities/${id}`)).title]),
      ),
    );
    loanedBody.innerHTML = perItem
      .map(
        ([item, list]) => `<tr>
          <td>${itemLabel(item)}</td>
          <td>${item.outstanding_loan_quantity}</td>
          <td>${
            list
              .map(
                ([id, outstanding]) =>
                  `<a href="/actividad?id=${id}">${escapeHtml(titles[id] ?? `Actividad ${id}`)}</a> (${outstanding})`,
              )
              .join('<br>') || '—'
          }</td>
        </tr>`,
      )
      .join('');
  } catch (err) {
    loanedBody.innerHTML = '<tr><td colspan="3">No se pudo cargar el material prestado.</td></tr>';
    showError(err.message);
  }
}

async function loadRented() {
  try {
    // Only a rental stays open; anything else that came back as "active" is not a rental to return.
    const rentals = (await apiFetch('/material/movements?status=active')).filter(
      (m) => m.type === 'rental',
    );
    rentedBody.innerHTML = rentals.length
      ? rentals
          .map((m) => {
            const item = itemsById[m.item_id];
            return `<tr>
              <td>${formatDate(m.transacted_at)}</td>
              <td>${item ? itemLabel(item) : `Material n.º ${m.item_id}`}</td>
              <td>${m.user_login ? escapeHtml(personName(m.user_login)) : '—'}</td>
              <td>${m.quantity}</td>
              <td><button type="button" class="btn btn-secondary btn-small" data-return="${m.id}">Devolver</button></td>
            </tr>`;
          })
          .join('')
      : '<tr><td colspan="5">No hay alquileres abiertos.</td></tr>';
  } catch (err) {
    rentedBody.innerHTML = '<tr><td colspan="5">No se pudieron cargar los alquileres.</td></tr>';
    showError(err.message);
  }
}

rentedBody.addEventListener('click', async (event) => {
  const button = event.target.closest('button[data-return]');
  if (!button) return;
  button.disabled = true;
  try {
    await apiFetch(`/material/movements/${button.dataset.return}/return`, { method: 'POST' });
    showFeedback('Devolución registrada.');
    await load();
  } catch (err) {
    button.disabled = false;
    showError(err.message);
  }
});

async function load() {
  const items = await apiFetch('/material/items');
  itemsById = Object.fromEntries(items.map((item) => [item.id, item]));
  await Promise.all([loadLoaned(), loadRented()]);
}

try {
  const directory = await apiFetch('/users/directory');
  people = Object.fromEntries(directory.map((person) => [person.login, person]));
} catch {
  // The rentals then show a login instead of a name.
}
try {
  await load();
} catch (err) {
  showError(err.message);
}
