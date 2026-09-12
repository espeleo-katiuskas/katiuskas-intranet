import {
  apiFetch,
  escapeHtml,
  formatDate,
  pickerLabel,
  renderPickerOptions,
  resolvePickerId,
  typeLabels,
  statusLabels,
  showFeedback,
  showError,
} from './api.js';
import { initPage } from './layout.js';

await initPage();

// Loaded once, shared only by the "new activity" participant picker now —
// per-activity participant management lives on /activity?id=… instead.
let directoryByLogin = {};
let personEntries = [];

async function loadDirectory() {
  try {
    const directory = await apiFetch('/users/directory');
    directoryByLogin = Object.fromEntries(directory.map((person) => [person.login, person]));
    personEntries = directory
      .slice()
      .sort((a, b) => `${a.last_name} ${a.first_name}`.localeCompare(`${b.last_name} ${b.first_name}`, 'es'))
      .map((person) => ({ id: person.login, label: pickerLabel(`${person.first_name} ${person.last_name}`, person.login) }));
  } catch {
    directoryByLogin = {};
    personEntries = [];
  }
}

function leaderLabel(activity) {
  const leader = directoryByLogin[activity.leader_login];
  return leader ? `${leader.first_name} ${leader.last_name}` : activity.leader_login;
}

function renderActivity(activity) {
  return `
    <tr data-activity-id="${activity.id}">
      <td>${formatDate(activity.start_date)}</td>
      <td>${formatDate(activity.end_date)}</td>
      <td>${escapeHtml(activity.title)}</td>
      <td>${typeLabels[activity.type] ?? activity.type}</td>
      <td>${escapeHtml(leaderLabel(activity))}</td>
      <td><span class="badge status-${activity.status}">${statusLabels[activity.status] ?? activity.status}</span></td>
    </tr>
  `;
}

const activitiesList = document.getElementById('activities-list');
const toggleFilterButton = document.getElementById('toggle-activity-filter');
const loadMoreButton = document.getElementById('load-more-activities');

const PAGE_SIZE = 50;
let showingActive = true;
let offset = 0;
let loadedActivities = [];

function activityQuery() {
  const params = new URLSearchParams({ limit: String(PAGE_SIZE), offset: String(offset), order: 'asc' });
  if (showingActive) {
    params.set('status', 'active');
  }
  return `/activities?${params.toString()}`;
}

async function loadActivities({ append = false } = {}) {
  try {
    const page = await apiFetch(activityQuery());
    loadedActivities = append ? loadedActivities.concat(page) : page;
    activitiesList.innerHTML = loadedActivities.length
      ? loadedActivities.map(renderActivity).join('')
      : `<tr><td colspan="6">${showingActive ? 'No hay actividades activas.' : 'Todavía no hay actividades.'}</td></tr>`;
    loadMoreButton.hidden = page.length < PAGE_SIZE;
  } catch {
    activitiesList.innerHTML = '<tr><td colspan="6">No se pudieron cargar las actividades.</td></tr>';
    loadMoreButton.hidden = true;
  }
}

toggleFilterButton.addEventListener('click', async () => {
  showingActive = !showingActive;
  offset = 0;
  toggleFilterButton.textContent = showingActive ? 'Ver todas' : 'Ver solo activas';
  await loadActivities();
});

loadMoreButton.addEventListener('click', async () => {
  offset += PAGE_SIZE;
  await loadActivities({ append: true });
});

activitiesList.addEventListener('click', (event) => {
  const row = event.target.closest('tr[data-activity-id]');
  if (!row) return;
  window.location.href = `/activity?id=${row.dataset.activityId}`;
});

// Creating an activity is open to any signed-in member, not just admin/board.
const newActivityPanel = document.getElementById('new-activity-panel');
const newActivityForm = document.getElementById('new-activity-form');
const participantSearchInput = document.getElementById('na-participant-search');
const participantOptions = document.getElementById('na-participant-options');
const participantChips = document.getElementById('na-participants-chips');
const chosenParticipants = new Map();

function renderParticipantChips() {
  participantChips.innerHTML = Array.from(chosenParticipants.entries())
    .map(([id, name]) => `<span class="badge-removable">${escapeHtml(name)}<button type="button" data-remove-chip="${id}" title="Quitar">×</button></span>`)
    .join('');
}

function addChosenParticipant() {
  const login = resolvePickerId(personEntries, participantSearchInput.value);
  if (!login) {
    showError('Escribe un nombre de la lista de socios.');
    return;
  }
  const person = directoryByLogin[login];
  chosenParticipants.set(login, `${person.first_name} ${person.last_name}`);
  participantSearchInput.value = '';
  renderParticipantChips();
}

document.getElementById('na-add-participant').addEventListener('click', addChosenParticipant);
participantSearchInput.addEventListener('keydown', (event) => {
  if (event.key === 'Enter') {
    event.preventDefault();
    addChosenParticipant();
  }
});
participantChips.addEventListener('click', (event) => {
  const removeButton = event.target.closest('button[data-remove-chip]');
  if (!removeButton) return;
  chosenParticipants.delete(removeButton.dataset.removeChip);
  renderParticipantChips();
});

// Awaited before the first loadActivities() call at the bottom of this
// file, so the organizer column has names to show on first paint
// instead of falling back to "Socio n.º …" until a re-render happens.
await loadDirectory();
participantOptions.innerHTML = renderPickerOptions(personEntries);
const leaderOptions = document.getElementById('na-leader-options');
if (leaderOptions) leaderOptions.innerHTML = renderPickerOptions(personEntries);

document.getElementById('toggle-new-activity').addEventListener('click', () => {
  newActivityPanel.hidden = !newActivityPanel.hidden;
});

document.getElementById('cancel-new-activity').addEventListener('click', () => {
  newActivityForm.reset();
  chosenParticipants.clear();
  renderParticipantChips();
  newActivityPanel.hidden = true;
});

newActivityForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  const formData = new FormData(newActivityForm);
  const optional = (key) => formData.get(key)?.toString().trim() || null;

  const body = {
    title: formData.get('title'),
    type: formData.get('type'),
    start_date: formData.get('start_date'),
    end_date: formData.get('end_date'),
    description: optional('description'),
    visibility: formData.get('visibility'),
    participant_logins: Array.from(chosenParticipants.keys()),
  };

  // Only present at all for admin/board (see data-roles on the field);
  // left blank, the organizer defaults to the caller as always.
  const leaderInput = document.getElementById('na-leader');
  if (leaderInput && leaderInput.value.trim()) {
    const leaderLogin = resolvePickerId(personEntries, leaderInput.value);
    if (!leaderLogin) {
      showError('Elige un organizador de la lista, o déjalo vacío.');
      return;
    }
    body.leader_login = leaderLogin;
  }

  const submitButton = newActivityForm.querySelector('button[type="submit"]');
  submitButton.disabled = true;
  try {
    await apiFetch('/activities', { method: 'POST', body: JSON.stringify(body) });
    showFeedback('Actividad creada.');
    newActivityForm.reset();
    chosenParticipants.clear();
    renderParticipantChips();
    newActivityPanel.hidden = true;
    offset = 0;
    await loadActivities();
  } catch (err) {
    showError(err.message);
  } finally {
    submitButton.disabled = false;
  }
});

// Calendar view: the whole current year, month by month, with weekends
// and club holidays marked (same tint — hovering a day shows whether
// it's a holiday, and its name, in the native tooltip) and any day with
// an activity showing its title(s), so it's clear at a glance which
// activity days land on a holiday/weekend and which don't.
const MONTH_NAMES = [
  'enero', 'febrero', 'marzo', 'abril', 'mayo', 'junio',
  'julio', 'agosto', 'septiembre', 'octubre', 'noviembre', 'diciembre',
];
const WEEKDAY_NAMES = ['L', 'M', 'X', 'J', 'V', 'S', 'D'];
const THIS_YEAR = new Date().getFullYear();
let calendarYear = THIS_YEAR;

function isoDate(date) {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`;
}

// One entry per date (YYYY-MM-DD) that's a holiday and/or has at least
// one activity touching it (an activity "touches" every day from its
// start_date to its end_date, not just the start).
function buildCalendarDayInfo(activityList, holidays) {
  const days = {};
  const dayFor = (key) => (days[key] ??= { holidayName: null, activities: [] });

  for (const holiday of holidays) {
    dayFor(holiday.date.slice(0, 10)).holidayName = holiday.name;
  }
  for (const activity of activityList) {
    if (activity.status === 'cancelled') continue;
    let cursor = new Date(`${activity.start_date.slice(0, 10)}T00:00:00`);
    const end = new Date(`${activity.end_date.slice(0, 10)}T00:00:00`);
    while (cursor <= end) {
      dayFor(isoDate(cursor)).activities.push(activity);
      cursor = new Date(cursor.getFullYear(), cursor.getMonth(), cursor.getDate() + 1);
    }
  }
  return days;
}

function renderCalendarDay(date, info) {
  const key = isoDate(date);
  const weekday = date.getDay(); // 0 = Sunday
  const isWeekend = weekday === 0 || weekday === 6;
  const isToday = key === isoDate(new Date());
  const activities = info?.activities ?? [];

  const classes = ['calendar-day'];
  if (isWeekend || info?.holidayName) classes.push('calendar-day-non-working');
  if (isToday) classes.push('calendar-day-today');

  const titleParts = [info?.holidayName ? `Festivo: ${info.holidayName}` : isWeekend ? 'Fin de semana' : null];
  const shown = activities.slice(0, 2);
  const rest = activities.length - shown.length;
  const chips = shown
    .map((a) => `<a class="calendar-day-activity" href="/activity?id=${a.id}" title="${escapeHtml(a.title)}">${escapeHtml(a.title)}</a>`)
    .join('');
  const more = rest > 0 ? `<span class="calendar-day-more">+${rest} más</span>` : '';

  return `
    <div class="${classes.join(' ')}" title="${escapeHtml(titleParts.filter(Boolean).join(' · '))}">
      <span class="calendar-day-number">${date.getDate()}</span>
      ${chips}${more}
    </div>
  `;
}

function renderCalendarMonth(month, dayInfo) {
  const firstOfMonth = new Date(calendarYear, month, 1);
  // getDay() is 0 = Sunday..6 = Saturday; the week here starts Monday.
  const leadingBlanks = (firstOfMonth.getDay() + 6) % 7;
  const daysInMonth = new Date(calendarYear, month + 1, 0).getDate();

  const blanks = Array.from({ length: leadingBlanks }, () => '<div class="calendar-day calendar-day-empty"></div>').join('');
  const days = Array.from({ length: daysInMonth }, (_, i) => {
    const date = new Date(calendarYear, month, i + 1);
    return renderCalendarDay(date, dayInfo[isoDate(date)]);
  }).join('');

  return `
    <div class="calendar-month">
      <h3>${MONTH_NAMES[month]} ${calendarYear}</h3>
      <div class="calendar-grid">
        ${WEEKDAY_NAMES.map((w) => `<div class="calendar-weekday">${w}</div>`).join('')}
        ${blanks}${days}
      </div>
    </div>
  `;
}

const calendarViewContainer = document.getElementById('calendar-view-container');
const calendarView = document.getElementById('calendar-view');
const listView = document.getElementById('list-view');
const toggleCalendarButton = document.getElementById('toggle-calendar-view');
const calendarYearLabel = document.getElementById('calendar-year-label');
const calendarTodayYearButton = document.getElementById('calendar-today-year');
let calendarOpened = false;

async function loadCalendar() {
  calendarYearLabel.textContent = String(calendarYear);
  calendarTodayYearButton.hidden = calendarYear === THIS_YEAR;
  calendarView.innerHTML = 'Cargando…';
  try {
    // The 100 most recent activities overall (order=desc explicit here
    // regardless of the list view's own oldest-first default, since
    // "most recent" is what actually matters when this 100-item cap
    // has to leave something out) — plenty for a single year's
    // calendar at this club's actual scale; there's no date-range
    // filter on GET /activities to ask for just one year.
    const [holidays, activityPage] = await Promise.all([
      apiFetch(`/holidays?year=${calendarYear}`),
      apiFetch('/activities?limit=100&order=desc'),
    ]);
    const yearActivities = activityPage.filter((a) => {
      const start = a.start_date.slice(0, 4);
      const end = a.end_date.slice(0, 4);
      return start <= String(calendarYear) && end >= String(calendarYear);
    });
    const dayInfo = buildCalendarDayInfo(yearActivities, holidays);
    calendarView.innerHTML = Array.from({ length: 12 }, (_, month) => renderCalendarMonth(month, dayInfo)).join('');
  } catch (err) {
    calendarView.innerHTML = `<p>${escapeHtml(err.message)}</p>`;
  }
}

toggleCalendarButton.addEventListener('click', async () => {
  const showingCalendar = calendarViewContainer.hidden;
  calendarViewContainer.hidden = !showingCalendar;
  listView.hidden = showingCalendar;
  // "Ver todas / Ver solo activas" only affects the list — the
  // calendar always shows every non-cancelled activity in the year, so
  // it'd be misleading to leave that button up while the calendar's
  // the one on screen.
  toggleFilterButton.hidden = showingCalendar;
  toggleCalendarButton.textContent = showingCalendar ? 'Ver lista' : 'Ver calendario';
  if (showingCalendar && !calendarOpened) {
    calendarOpened = true;
    await loadCalendar();
  }
});

document.getElementById('calendar-prev-year').addEventListener('click', async () => {
  calendarYear -= 1;
  await loadCalendar();
});

document.getElementById('calendar-next-year').addEventListener('click', async () => {
  calendarYear += 1;
  await loadCalendar();
});

calendarTodayYearButton.addEventListener('click', async () => {
  calendarYear = THIS_YEAR;
  await loadCalendar();
});

loadActivities();
