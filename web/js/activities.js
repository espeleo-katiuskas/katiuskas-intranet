import {
  initSession,
  logout,
  applyRoleVisibility,
  currentUser,
  hasAnyRole,
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

const user = currentUser();
const isAdmin = hasAnyRole(user, ['admin', 'board']);

const feedbackBox = document.getElementById('feedback');
const errorBox = document.getElementById('error');

// Loaded once and shared by the "new activity" participant picker, the
// per-activity participants panel (to show names instead of bare ids) and
// its "add participant" control.
let directory = [];
let directoryById = {};
let personEntries = [];

async function loadDirectory() {
  try {
    directory = await apiFetch('/users/directory');
    directoryById = Object.fromEntries(directory.map((person) => [person.id, person]));
    personEntries = directory
      .slice()
      .sort((a, b) => `${a.last_name} ${a.first_name}`.localeCompare(`${b.last_name} ${b.first_name}`, 'es'))
      .map((person) => ({ id: person.id, label: pickerLabel(`${person.first_name} ${person.last_name}`, person.id) }));
  } catch {
    directory = [];
    directoryById = {};
    personEntries = [];
  }
}

function personLabel(userId) {
  const person = directoryById[userId];
  return person ? `${person.first_name} ${person.last_name}` : `Socio n.º ${userId}`;
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
const statusOrder = ['planned', 'completed', 'cancelled'];
const typeLabels = { caving: 'Espeleología', training: 'Formación', outdoor: 'Exterior', social: 'Social', other: 'Otra' };

function renderStatusOptions(current) {
  return statusOrder
    .map((s) => `<option value="${s}" ${s === current ? 'selected' : ''}>${statusLabels[s]}</option>`)
    .join('');
}

function renderActivity(activity) {
  const dateLabel = activity.end_date
    ? `${formatDate(activity.start_date)} – ${formatDate(activity.end_date)}`
    : formatDate(activity.start_date);

  const statusMarkup = isAdmin
    ? `<select class="status-select" data-status-for="${activity.id}">${renderStatusOptions(activity.status)}</select>`
    : `<span class="badge status-${activity.status}">${statusLabels[activity.status] ?? activity.status}</span>`;

  const adminActions = isAdmin
    ? `
      <button type="button" class="btn btn-secondary btn-small" data-toggle-edit="${activity.id}">Editar</button>
      <button type="button" class="btn btn-secondary btn-small" data-toggle-participants="${activity.id}">Participantes</button>
      <button type="button" class="btn btn-danger btn-small" data-cancel="${activity.id}">Cancelar</button>
    `
    : '';

  const editPanel = isAdmin
    ? `
      <div class="edit-fields" data-edit-panel="${activity.id}" hidden>
        <div class="field">
          <label>Título</label>
          <input type="text" data-edit-field="title" value="${escapeHtml(activity.title)}">
        </div>
        <div class="field">
          <label>Lugar</label>
          <input type="text" data-edit-field="location" value="${escapeHtml(activity.location)}">
        </div>
        <div class="field">
          <label>Descripción</label>
          <textarea data-edit-field="description">${escapeHtml(activity.description ?? '')}</textarea>
        </div>
        <button type="button" class="btn btn-primary btn-small" data-save-edit="${activity.id}">Guardar</button>
      </div>
    `
    : '';

  const participantsPanel = isAdmin
    ? `
      <div class="detail-list" data-participants-panel="${activity.id}" hidden>
        <div data-participants-rows="${activity.id}"></div>
        <div class="role-add-row">
          <input type="text" list="participant-options-${activity.id}" data-participant-search="${activity.id}" autocomplete="off" placeholder="Escribe un nombre…">
          <datalist id="participant-options-${activity.id}"></datalist>
          <button type="button" class="btn btn-secondary btn-small" data-add-participant="${activity.id}">+ Participante</button>
        </div>
      </div>
    `
    : '';

  return `
    <tr data-activity-id="${activity.id}">
      <td>${dateLabel}</td>
      <td>${escapeHtml(activity.title)}</td>
      <td>${escapeHtml(activity.location)}</td>
      <td>${typeLabels[activity.type] ?? activity.type}</td>
      <td>${statusMarkup}</td>
      <td class="table-actions">
        <button type="button" class="btn btn-secondary btn-small" data-join="${activity.id}">Apuntarme</button>
        <button type="button" class="btn btn-secondary btn-small" data-leave="${activity.id}">Darme de baja</button>
        ${adminActions}
      </td>
    </tr>
    <tr data-activity-detail-row="${activity.id}">
      <td colspan="6">
        ${activity.description ? `<p>${escapeHtml(activity.description)}</p>` : ''}
        ${editPanel}
        ${participantsPanel}
      </td>
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

activitiesList.addEventListener('change', async (event) => {
  const select = event.target.closest('select[data-status-for]');
  if (!select) return;

  select.disabled = true;
  try {
    await apiFetch(`/activities/${select.dataset.statusFor}`, {
      method: 'PATCH',
      body: JSON.stringify({ status: select.value }),
    });
    showFeedback('Estado actualizado.');
  } catch (err) {
    showError(err.message);
  } finally {
    select.disabled = false;
  }
});

activitiesList.addEventListener('click', async (event) => {
  const joinButton = event.target.closest('button[data-join]');
  if (joinButton) {
    joinButton.disabled = true;
    try {
      await apiFetch(`/activities/${joinButton.dataset.join}/participants`, { method: 'POST' });
      showFeedback('Te has apuntado.');
    } catch (err) {
      showError(err.message);
    } finally {
      joinButton.disabled = false;
    }
    return;
  }

  const leaveButton = event.target.closest('button[data-leave]');
  if (leaveButton) {
    leaveButton.disabled = true;
    try {
      await apiFetch(`/activities/${leaveButton.dataset.leave}/participants/${user.id}`, { method: 'DELETE' });
      showFeedback('Te has dado de baja de la actividad.');
    } catch (err) {
      showError(err.message);
    } finally {
      leaveButton.disabled = false;
    }
    return;
  }

  const cancelButton = event.target.closest('button[data-cancel]');
  if (cancelButton) {
    if (!confirm('¿Cancelar esta actividad?')) return;
    cancelButton.disabled = true;
    try {
      await apiFetch(`/activities/${cancelButton.dataset.cancel}`, { method: 'DELETE' });
      showFeedback('Actividad cancelada.');
      await loadActivities();
    } catch (err) {
      cancelButton.disabled = false;
      showError(err.message);
    }
    return;
  }

  const toggleEditButton = event.target.closest('button[data-toggle-edit]');
  if (toggleEditButton) {
    const panel = activitiesList.querySelector(`[data-edit-panel="${toggleEditButton.dataset.toggleEdit}"]`);
    panel.hidden = !panel.hidden;
    return;
  }

  const saveEditButton = event.target.closest('button[data-save-edit]');
  if (saveEditButton) {
    const activityId = saveEditButton.dataset.saveEdit;
    const panel = activitiesList.querySelector(`[data-edit-panel="${activityId}"]`);
    const body = {
      title: panel.querySelector('[data-edit-field="title"]').value,
      location: panel.querySelector('[data-edit-field="location"]').value,
      description: panel.querySelector('[data-edit-field="description"]').value || null,
    };
    saveEditButton.disabled = true;
    try {
      await apiFetch(`/activities/${activityId}`, { method: 'PATCH', body: JSON.stringify(body) });
      showFeedback('Actividad actualizada.');
      await loadActivities();
    } catch (err) {
      saveEditButton.disabled = false;
      showError(err.message);
    }
    return;
  }

  const toggleParticipantsButton = event.target.closest('button[data-toggle-participants]');
  if (toggleParticipantsButton) {
    const activityId = toggleParticipantsButton.dataset.toggleParticipants;
    const panel = activitiesList.querySelector(`[data-participants-panel="${activityId}"]`);
    panel.hidden = !panel.hidden;
    if (!panel.hidden) {
      await loadParticipants(activityId, panel);
    }
    return;
  }

  const removeParticipantButton = event.target.closest('button[data-remove-participant]');
  if (removeParticipantButton) {
    const [activityId, participantId] = removeParticipantButton.dataset.removeParticipant.split(':');
    removeParticipantButton.disabled = true;
    try {
      await apiFetch(`/activities/${activityId}/participants/${participantId}`, { method: 'DELETE' });
      const panel = activitiesList.querySelector(`[data-participants-panel="${activityId}"]`);
      await loadParticipants(activityId, panel);
    } catch (err) {
      removeParticipantButton.disabled = false;
      showError(err.message);
    }
    return;
  }

  const addParticipantButton = event.target.closest('button[data-add-participant]');
  if (addParticipantButton) {
    const activityId = addParticipantButton.dataset.addParticipant;
    const panel = activitiesList.querySelector(`[data-participants-panel="${activityId}"]`);
    const input = panel.querySelector(`[data-participant-search="${activityId}"]`);
    const personId = resolvePickerId(personEntries, input.value);
    if (!personId) {
      showError('Escribe un nombre de la lista de socios.');
      return;
    }

    addParticipantButton.disabled = true;
    try {
      await apiFetch(`/activities/${activityId}/participants`, {
        method: 'POST',
        body: JSON.stringify({ user_id: personId }),
      });
      input.value = '';
      await loadParticipants(activityId, panel);
    } catch (err) {
      showError(err.message);
    } finally {
      addParticipantButton.disabled = false;
    }
  }
});

async function loadParticipants(activityId, panel) {
  const rows = panel.querySelector(`[data-participants-rows="${activityId}"]`);
  const searchInput = panel.querySelector(`[data-participant-search="${activityId}"]`);
  const searchOptions = panel.querySelector(`#participant-options-${activityId}`);
  rows.innerHTML = 'Cargando…';
  try {
    const participants = await apiFetch(`/activities/${activityId}/participants`);
    rows.innerHTML = participants.length
      ? participants
          .map((p) => `
            <div class="detail-row">
              <span>${escapeHtml(personLabel(p.user_id))}${p.role === 'leader' ? ' · responsable' : ''}</span>
              <button type="button" class="btn btn-danger btn-small" data-remove-participant="${activityId}:${p.user_id}">Quitar</button>
            </div>
          `)
          .join('')
      : '<p>Todavía no hay nadie apuntado.</p>';

    const alreadyIn = new Set(participants.map((p) => p.user_id));
    const candidates = personEntries.filter((entry) => !alreadyIn.has(entry.id));
    searchOptions.innerHTML = renderPickerOptions(candidates);
    searchInput.disabled = candidates.length === 0;
  } catch (err) {
    rows.innerHTML = `<p>${escapeHtml(err.message)}</p>`;
  }
}

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
