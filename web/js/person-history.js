import {
  activityState,
  activityStateLabels,
  apiFetch,
  escapeHtml,
  formatDate,
  typeLabels,
} from './api.js';

// The two tables that tell what a person has done in the club: "Actividades realizadas" (the activities they
// organized or took part in, GET /users/{login}/activities) and "Lugares visitados" (the places those
// activities went to that have started and were not cancelled, with how many times and when the last one
// ended, GET /users/{login}/places). The page of a member (/socio) and "Mi perfil" both have them, the
// first for somebody else and the second for oneself, which only changes the words of the empty message.
// The page has <tbody id="activities-table-body"> and <tbody id="visited-places-body">.

function activityRow(activity, login) {
  const role = activity.leader_login === login ? 'Organizador' : 'Participante';
  return `
    <tr>
      <td>${formatDate(activity.start_date)}</td>
      <td>${formatDate(activity.end_date)}</td>
      <td><a href="/actividad?id=${activity.id}">${escapeHtml(activity.title)}</a></td>
      <td>${typeLabels[activity.type] ?? activity.type}</td>
      <td><span class="badge status-${activityState(activity)}">${activityStateLabels[activityState(activity)]}</span></td>
      <td>${role}</td>
    </tr>
  `;
}

function placeRow(place) {
  return `
    <tr>
      <td><a href="/lugar?id=${place.id}">${escapeHtml(place.name)}</a></td>
      <td>${escapeHtml(place.type_name)}</td>
      <td>${escapeHtml(place.where)}</td>
      <td>${place.visits}</td>
      <td class="nowrap">${formatDate(place.last_visit)}</td>
    </tr>
  `;
}

async function loadActivities(login, own) {
  const tableBody = document.getElementById('activities-table-body');
  try {
    const activities = await apiFetch(`/users/${login}/activities`);
    tableBody.innerHTML = activities.length
      ? activities.map((activity) => activityRow(activity, login)).join('')
      : `<tr><td colspan="6">Todavía no ${own ? 'has participado' : 'ha participado'} en ninguna actividad.</td></tr>`;
  } catch {
    tableBody.innerHTML =
      '<tr><td colspan="6">No se pudo cargar el historial de actividades.</td></tr>';
  }
}

async function loadPlaces(login, own) {
  const tableBody = document.getElementById('visited-places-body');
  try {
    const places = await apiFetch(`/users/${login}/places`);
    tableBody.innerHTML = places.length
      ? places.map(placeRow).join('')
      : `<tr><td colspan="5">Todavía no ${own ? 'has visitado' : 'ha visitado'} ningún lugar.</td></tr>`;
  } catch {
    tableBody.innerHTML =
      '<tr><td colspan="5">No se pudieron cargar los lugares visitados.</td></tr>';
  }
}

// Fills both tables for the person with that login; `own` is for their own page.
export function initPersonHistory(login, { own = false } = {}) {
  loadActivities(login, own);
  loadPlaces(login, own);
}
