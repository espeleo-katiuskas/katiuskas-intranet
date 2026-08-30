import {
  initSession,
  logout,
  applyRoleVisibility,
  apiFetch,
  escapeHtml,
  formatDate,
  pickerLabel,
  renderPickerOptions,
  resolvePickerId,
} from './api.js';

await initSession();
applyRoleVisibility();
document.getElementById('logout-button').addEventListener('click', logout);

const feedbackBox = document.getElementById('feedback');
const errorBox = document.getElementById('error');

// Loaded once, shared only by the "new activity" participant picker now —
// per-activity participant management lives on /activity?id=… instead.
let directoryById = {};
let personEntries = [];

async function loadDirectory() {
  try {
    const directory = await apiFetch('/users/directory');
    directoryById = Object.fromEntries(directory.map((person) => [person.id, person]));
    personEntries = directory
      .slice()
      .sort((a, b) => `${a.last_name} ${a.first_name}`.localeCompare(`${b.last_name} ${b.first_name}`, 'es'))
      .map((person) => ({ id: person.id, label: pickerLabel(`${person.first_name} ${person.last_name}`, person.id) }));
  } catch {
    directoryById = {};
    personEntries = [];
  }
}

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

const statusLabels = { planned: 'Planificada', completed: 'Realizada', cancelled: 'Cancelada' };
const typeLabels = { caving: 'Espeleología', training: 'Formación', outdoor: 'Exterior', social: 'Social', other: 'Otra' };

function renderActivity(activity) {
  const dateLabel = activity.end_date
    ? `${formatDate(activity.start_date)} – ${formatDate(activity.end_date)}`
    : formatDate(activity.start_date);

  return `
    <tr data-activity-id="${activity.id}">
      <td>${dateLabel}</td>
      <td>${escapeHtml(activity.title)}</td>
      <td>${escapeHtml(activity.location)}</td>
      <td>${typeLabels[activity.type] ?? activity.type}</td>
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
  const params = new URLSearchParams({ limit: String(PAGE_SIZE), offset: String(offset) });
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
      : `<tr><td colspan="5">${showingActive ? 'No hay actividades activas.' : 'Todavía no hay actividades.'}</td></tr>`;
    loadMoreButton.hidden = page.length < PAGE_SIZE;
  } catch {
    activitiesList.innerHTML = '<tr><td colspan="5">No se pudieron cargar las actividades.</td></tr>';
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
  const personId = resolvePickerId(personEntries, participantSearchInput.value);
  if (!personId) {
    showError('Escribe un nombre de la lista de socios.');
    return;
  }
  const person = directoryById[personId];
  chosenParticipants.set(personId, `${person.first_name} ${person.last_name}`);
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
  chosenParticipants.delete(Number(removeButton.dataset.removeChip));
  renderParticipantChips();
});

loadDirectory().then(() => {
  participantOptions.innerHTML = renderPickerOptions(personEntries);
});

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
    location: formData.get('location'),
    start_date: formData.get('start_date'),
    end_date: optional('end_date'),
    description: optional('description'),
    difficulty: optional('difficulty') ? Number(optional('difficulty')) : null,
    max_participants: optional('max_participants') ? Number(optional('max_participants')) : null,
    participant_user_ids: Array.from(chosenParticipants.keys()),
  };

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

loadActivities();
