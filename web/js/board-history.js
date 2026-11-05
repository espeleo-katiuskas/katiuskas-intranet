import { apiFetch, escapeHtml, formatDate, hasAnyRole, positionLabels, showError } from './api.js';
import { initPage } from './layout.js';

// Every change there has been on the Junta Directiva, newest first: each term of GET
// /board-members/history is two events, when the person enters the position and its last day (if it
// has ended), put in order by date and grouped by year.
const viewer = await initPage();
// A person's page is only open to admin and board: for anybody else the name is plain text.
const canOpenPeople = hasAnyRole(viewer, ['admin', 'board']);

const container = document.getElementById('board-history');

function personName(term) {
  const name = escapeHtml(`${term.first_name} ${term.last_name}`);
  return canOpenPeople
    ? `<a href="/socio?login=${encodeURIComponent(term.user_login)}">${name}</a>`
    : name;
}

// The events of a list of terms, newest first. On one day an entry comes before a last day, as it
// reads in the order of time when the list is looked at from the bottom up.
function eventsOf(terms) {
  const events = [];
  for (const term of terms) {
    events.push({ date: term.start_date.slice(0, 10), kind: 'start', term });
    if (term.end_date) events.push({ date: term.end_date.slice(0, 10), kind: 'end', term });
  }
  const order = { start: 0, end: 1 };
  return events.sort((a, b) => b.date.localeCompare(a.date) || order[a.kind] - order[b.kind]);
}

function renderEvent(event) {
  const position = escapeHtml(positionLabels[event.term.position] ?? event.term.position);
  const text =
    event.kind === 'start'
      ? `${personName(event.term)} entra como <strong>${position}</strong>`
      : `Último día de ${personName(event.term)} como <strong>${position}</strong>`;
  return `
    <div class="detail-row participant-row">
      <span class="nowrap">${formatDate(event.date)}</span>
      <span>${text}</span>
    </div>`;
}

function render(terms) {
  if (terms.length === 0) {
    container.innerHTML = '<p>Todavía no hay ningún cambio en la Junta Directiva.</p>';
    return;
  }
  const byYear = new Map();
  for (const event of eventsOf(terms)) {
    const year = event.date.slice(0, 4);
    if (!byYear.has(year)) byYear.set(year, []);
    byYear.get(year).push(event);
  }
  container.innerHTML = [...byYear]
    .map(
      ([year, events]) =>
        `<h2>${year}</h2><div class="detail-list">${events.map(renderEvent).join('')}</div>`,
    )
    .join('');
}

try {
  render(await apiFetch('/board-members/history'));
} catch (err) {
  container.innerHTML = '<p>No se pudo cargar el histórico.</p>';
  showError(err.message);
}
