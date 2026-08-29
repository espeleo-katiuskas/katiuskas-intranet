import {
  initSession,
  logout,
  applyRoleVisibility,
  currentUser,
  hasAnyRole,
  apiFetch,
  escapeHtml,
  formatDate,
} from './api.js';

await initSession();
applyRoleVisibility();
document.getElementById('logout-button').addEventListener('click', logout);

const user = currentUser();
const isAdmin = hasAnyRole(user, ['admin', 'board']);

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

const statusLabels = { planned: 'Planificada', completed: 'Realizada', cancelled: 'Cancelada' };
const statusOrder = ['planned', 'completed', 'cancelled'];

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
    ? `<div class="detail-list" data-participants-panel="${activity.id}" hidden></div>`
    : '';

  return `
    <div class="card" data-activity-id="${activity.id}">
      <h3>${escapeHtml(activity.title)}</h3>
      <p>${dateLabel} · ${escapeHtml(activity.location)}</p>
      ${activity.description ? `<p>${escapeHtml(activity.description)}</p>` : ''}
      <p>${statusMarkup}</p>
      <div class="card-actions">
        <button type="button" class="btn btn-secondary btn-small" data-join="${activity.id}">Apuntarme</button>
        <button type="button" class="btn btn-secondary btn-small" data-leave="${activity.id}">Darme de baja</button>
        ${adminActions}
      </div>
      ${editPanel}
      ${participantsPanel}
    </div>
  `;
}

async function loadActivities() {
  const list = document.getElementById('activities-list');
  try {
    const activities = await apiFetch('/activities');
    list.innerHTML = activities.length
      ? activities.map(renderActivity).join('')
      : '<p>Todavía no hay actividades.</p>';
  } catch {
    list.innerHTML = '<p>No se pudieron cargar las actividades.</p>';
  }
}

const activitiesList = document.getElementById('activities-list');

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
  }
});

async function loadParticipants(activityId, panel) {
  panel.innerHTML = 'Cargando…';
  try {
    const participants = await apiFetch(`/activities/${activityId}/participants`);
    panel.innerHTML = participants.length
      ? participants
          .map((p) => `
            <div class="detail-row">
              <span>Socio n.º ${p.user_id}${p.role === 'leader' ? ' · responsable' : ''}</span>
              <button type="button" class="btn btn-danger btn-small" data-remove-participant="${activityId}:${p.user_id}">Quitar</button>
            </div>
          `)
          .join('')
      : '<p>Todavía no hay nadie apuntado.</p>';
  } catch (err) {
    panel.innerHTML = `<p>${escapeHtml(err.message)}</p>`;
  }
}

if (isAdmin) {
  const newActivityPanel = document.getElementById('new-activity-panel');
  const newActivityForm = document.getElementById('new-activity-form');

  document.getElementById('toggle-new-activity').addEventListener('click', () => {
    newActivityPanel.hidden = !newActivityPanel.hidden;
  });

  document.getElementById('cancel-new-activity').addEventListener('click', () => {
    newActivityForm.reset();
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
    };

    const submitButton = newActivityForm.querySelector('button[type="submit"]');
    submitButton.disabled = true;
    try {
      await apiFetch('/activities', { method: 'POST', body: JSON.stringify(body) });
      showFeedback('Actividad creada.');
      newActivityForm.reset();
      newActivityPanel.hidden = true;
      await loadActivities();
    } catch (err) {
      showError(err.message);
    } finally {
      submitButton.disabled = false;
    }
  });
}

loadActivities();
