import { initSession, logout, applyRoleVisibility, apiFetch } from './api.js';

await initSession();
applyRoleVisibility();
document.getElementById('logout-button').addEventListener('click', logout);

const statusLabels = {
  planned: 'Planificada',
  completed: 'Realizada',
  cancelled: 'Cancelada',
};

function renderActivity(activity) {
  const date = new Date(activity.start_date).toLocaleDateString('es-ES');
  return `
    <div class="card">
      <h3>${activity.title}</h3>
      <p>${date} · ${activity.location}</p>
      <p><span class="badge status-${activity.status}">${statusLabels[activity.status] ?? activity.status}</span></p>
      <button type="button" data-join="${activity.id}" class="link-button">Apuntarme</button>
    </div>
  `;
}

async function joinActivity(activityId, button) {
  button.disabled = true;
  try {
    await apiFetch(`/activities/${activityId}/participants`, { method: 'POST' });
    button.textContent = 'Apuntado/a';
  } catch {
    button.disabled = false;
    alert('No se ha podido completar la inscripción.');
  }
}

async function loadActivities() {
  const list = document.getElementById('activities-list');
  try {
    const activities = await apiFetch('/activities');
    list.innerHTML = activities.map(renderActivity).join('');
    list.querySelectorAll('button[data-join]').forEach((button) => {
      button.addEventListener('click', () => joinActivity(button.dataset.join, button));
    });
  } catch {
    list.innerHTML = '<p>No se pudieron cargar las actividades.</p>';
  }
}

loadActivities();
