import {
  initSession,
  logout,
  applyRoleVisibility,
  currentUser,
  hasAnyRole,
  apiFetch,
  escapeHtml,
  formatDate,
  renderPickerOptions,
  resolvePickerId,
} from './api.js';

await initSession();
applyRoleVisibility();
document.getElementById('logout-button').addEventListener('click', logout);

const viewer = currentUser();
const isAdmin = hasAnyRole(viewer, ['admin', 'board']);

const activityId = Number(new URLSearchParams(window.location.search).get('id'));
if (!activityId) {
  window.location.href = '/activities';
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

const typeLabels = { caving: 'Espeleología', training: 'Formación', outdoor: 'Exterior', social: 'Social', other: 'Otra' };
const statusLabels = { planned: 'Planificada', completed: 'Realizada', cancelled: 'Cancelada' };

const editForm = document.getElementById('edit-activity-form');

async function loadActivity() {
  try {
    const activity = await apiFetch(`/activities/${activityId}`);
    const dateLabel = activity.end_date
      ? `${formatDate(activity.start_date)} – ${formatDate(activity.end_date)}`
      : formatDate(activity.start_date);
    document.getElementById('activity-title').textContent = activity.title;
    document.getElementById('activity-meta').textContent =
      `${dateLabel} · ${activity.location} · ${typeLabels[activity.type] ?? activity.type} · ${statusLabels[activity.status] ?? activity.status}`;
    document.getElementById('activity-description').textContent = activity.description ?? '';

    if (editForm) {
      editForm.title.value = activity.title;
      editForm.location.value = activity.location;
      editForm.status.value = activity.status;
      editForm.description.value = activity.description ?? '';
    }
  } catch (err) {
    document.getElementById('activity-title').textContent = 'No se ha podido cargar esta actividad.';
    showError(err.message);
  }
}

editForm?.addEventListener('submit', async (event) => {
  event.preventDefault();
  const formData = new FormData(editForm);
  const body = {
    title: formData.get('title'),
    location: formData.get('location'),
    status: formData.get('status'),
    description: formData.get('description')?.toString().trim() || null,
  };

  const submitButton = editForm.querySelector('button[type="submit"]');
  submitButton.disabled = true;
  try {
    await apiFetch(`/activities/${activityId}`, { method: 'PATCH', body: JSON.stringify(body) });
    showFeedback('Actividad actualizada.');
    await loadActivity();
  } catch (err) {
    showError(err.message);
  } finally {
    submitButton.disabled = false;
  }
});

document.getElementById('cancel-activity')?.addEventListener('click', async () => {
  if (!confirm('¿Cancelar esta actividad?')) return;
  const button = document.getElementById('cancel-activity');
  button.disabled = true;
  try {
    await apiFetch(`/activities/${activityId}`, { method: 'DELETE' });
    showFeedback('Actividad cancelada.');
    await loadActivity();
  } catch (err) {
    button.disabled = false;
    showError(err.message);
  }
});

// Loaded once so participant names can link to their own /member?id=…
// page instead of showing a bare id, and (for admin/board) so the "add
// participant" picker can search by name.
let directoryById = {};
let personEntries = [];

async function loadDirectory() {
  try {
    const directory = await apiFetch('/users/directory');
    directoryById = Object.fromEntries(directory.map((person) => [person.id, person]));
    personEntries = directory
      .slice()
      .sort((a, b) => `${a.last_name} ${a.first_name}`.localeCompare(`${b.last_name} ${b.first_name}`, 'es'))
      .map((person) => ({ id: person.id, label: `${person.first_name} ${person.last_name} #${person.id}` }));
  } catch {
    directoryById = {};
    personEntries = [];
  }
}

function renderParticipantRow(p) {
  const person = directoryById[p.user_id];
  const name = person ? `${person.first_name} ${person.last_name}` : `Socio n.º ${p.user_id}`;
  return `
    <div class="detail-row">
      <span><a href="/member?id=${p.user_id}">${escapeHtml(name)}</a>${p.role === 'leader' ? ' · responsable' : ''}</span>
      ${isAdmin ? `<button type="button" class="btn btn-danger btn-small" data-remove-participant="${p.user_id}">Quitar</button>` : ''}
    </div>
  `;
}

async function loadParticipants() {
  const container = document.getElementById('participants-list');
  try {
    const participants = await apiFetch(`/activities/${activityId}/participants`);
    container.innerHTML = participants.length
      ? participants.map(renderParticipantRow).join('')
      : '<p>Todavía no hay nadie apuntado.</p>';

    const alreadyIn = participants.some((p) => p.user_id === viewer?.id);
    document.getElementById('join-activity').hidden = alreadyIn;
    document.getElementById('leave-activity').hidden = !alreadyIn;

    const optionsEl = document.getElementById('participant-options');
    if (optionsEl) {
      const alreadyInSet = new Set(participants.map((p) => p.user_id));
      const candidates = personEntries.filter((entry) => !alreadyInSet.has(entry.id));
      optionsEl.innerHTML = renderPickerOptions(candidates);
    }
  } catch (err) {
    container.innerHTML = `<p>${escapeHtml(err.message)}</p>`;
  }
}

document.getElementById('join-activity').addEventListener('click', async () => {
  try {
    await apiFetch(`/activities/${activityId}/participants`, { method: 'POST' });
    showFeedback('Te has apuntado.');
    await loadParticipants();
  } catch (err) {
    showError(err.message);
  }
});

document.getElementById('leave-activity').addEventListener('click', async () => {
  try {
    await apiFetch(`/activities/${activityId}/participants/${viewer.id}`, { method: 'DELETE' });
    showFeedback('Te has dado de baja de la actividad.');
    await loadParticipants();
  } catch (err) {
    showError(err.message);
  }
});

document.getElementById('add-participant')?.addEventListener('click', async () => {
  const input = document.getElementById('participant-search');
  const personId = resolvePickerId(personEntries, input.value);
  if (!personId) {
    showError('Escribe un nombre de la lista de socios.');
    return;
  }
  try {
    await apiFetch(`/activities/${activityId}/participants`, {
      method: 'POST',
      body: JSON.stringify({ user_id: personId }),
    });
    input.value = '';
    await loadParticipants();
  } catch (err) {
    showError(err.message);
  }
});

document.getElementById('participants-list').addEventListener('click', async (event) => {
  const button = event.target.closest('button[data-remove-participant]');
  if (!button) return;
  button.disabled = true;
  try {
    await apiFetch(`/activities/${activityId}/participants/${button.dataset.removeParticipant}`, { method: 'DELETE' });
    await loadParticipants();
  } catch (err) {
    button.disabled = false;
    showError(err.message);
  }
});

await loadDirectory();
loadActivity();
loadParticipants();
