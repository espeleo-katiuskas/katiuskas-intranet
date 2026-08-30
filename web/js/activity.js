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
// Set once loadActivity() resolves and the activity's leader_id is
// known — admin/board can manage any activity, and the organizer who
// created it can manage their own. Can't be a data-roles attribute
// since it depends on data loaded asynchronously for this specific
// activity.
let canManage = isAdmin;

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

const typeLabels = { caving: 'Espeleología', canyoning: 'Cañones', training: 'Formación', social: 'Social', other: 'Otro' };
const statusLabels = { planned: 'Planificada', completed: 'Realizada', cancelled: 'Cancelada' };

const editForm = document.getElementById('edit-activity-form');

// Toggles every management-only control (edit panel, "+ Participante"
// row, "Zona de peligro") according to canManage — mirrors what
// data-roles/applyRoleVisibility does for static, role-only elements,
// but for something only knowable after this specific activity loads.
function applyManagementVisibility() {
  document.getElementById('edit-activity-panel').hidden = !canManage;
  document.getElementById('add-participant-row').hidden = !canManage;
  document.getElementById('danger-zone-header').hidden = !canManage;
  document.getElementById('cancel-activity').hidden = !canManage;
}

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

    canManage = isAdmin || viewer?.id === activity.leader_id;
    applyManagementVisibility();

    if (editForm) {
      editForm.title.value = activity.title;
      editForm.type.value = activity.type;
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
    type: formData.get('type'),
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
      ${canManage ? `<button type="button" class="btn btn-danger btn-small" data-remove-participant="${p.user_id}">Quitar</button>` : ''}
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
await loadActivity();
loadParticipants();
