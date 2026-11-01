import { apiFetch, escapeHtml, formatDateTime, logout, showError, showFeedback } from './api.js';
import { initSortableHeaders } from './sortableHeaders.js';

// The sessions of a person: where they are signed in (GET /users/{login}/tokens?status=active, the ones
// not expired or revoked), each of which can be closed (DELETE .../tokens/{id}). It is the person
// themselves on /perfil ("Mis sesiones") and an admin on /socio, where it is anybody's. Closing the one
// in use is signing out, so that one signs out of this browser too. The page has the markup
// (#sessions-impersonating, #sessions-head, #sessions-list).
//
// `own` is whether the person looking is the one whose sessions they are (it only changes the wording),
// and `impersonating` whether this is an impersonation: seeing the sessions of the person being
// impersonated is allowed — it is what they would see as themselves — but the API does not let
// sessions be ended through one, so the buttons are not offered, and a note says why.
function initSessionsPanel({ login, own, impersonating = false }) {
  const body = document.getElementById('sessions-list');
  let sessions = [];
  document.getElementById('sessions-impersonating').hidden = !impersonating;
  // Sorted here (they are all loaded): by last use, when it was opened and when it expires, either
  // way. A session never used counts as the oldest use. Starts with the most recently used first.
  const sort = { key: 'last_used', desc: true };
  const dateOf = {
    last_used: (s) => s.last_used_at ?? '',
    created: (s) => s.created_at,
    expires: (s) => s.expires_at,
  };

  function render() {
    const sorted = sessions.slice().sort((a, b) => {
      const [x, y] = [dateOf[sort.key](a), dateOf[sort.key](b)];
      // ISO timestamps compare as text; the id breaks a tie the same way for both directions, so the
      // order is stable.
      const byDate = x < y ? -1 : x > y ? 1 : 0;
      return (sort.desc ? -byDate : byDate) || b.id - a.id;
    });
    body.innerHTML = sorted.length
      ? sorted.map(row).join('')
      : `<tr><td colspan="6">${own ? 'No tienes' : 'No tiene'} ninguna sesión abierta.</td></tr>`;
  }

  function row(session) {
    const parts = [session.browser, session.os].filter(Boolean);
    const client = session.last_used_user_agent
      ? `<span title="${escapeHtml(session.last_used_user_agent)}">${parts.length ? escapeHtml(parts.join(' · ')) : 'Navegador desconocido'}</span>`
      : '—';
    const current = session.current ? ' <span class="badge">Esta sesión</span>' : '';
    const byAdmin = session.impersonated_by_login
      ? ` <span class="badge" title="${own ? 'Un administrador entró como tú, por ejemplo para ayudarte' : 'Un administrador entró como esta persona'}">Abierta por ${escapeHtml(session.impersonated_by_login)}</span>`
      : '';
    return `
    <tr>
      <td>${client}${current}${byAdmin}</td>
      <td>${session.last_used_at ? formatDateTime(session.last_used_at) : 'Nunca'}</td>
      <td>${session.last_used_ip ? escapeHtml(session.last_used_ip) : '—'}</td>
      <td>${formatDateTime(session.created_at)}</td>
      <td>${formatDateTime(session.expires_at)}</td>
      <td>${impersonating ? '' : `<button type="button" class="btn btn-danger btn-small" data-close-session="${session.id}">${session.current ? 'Cerrar esta sesión' : 'Cerrar'}</button>`}</td>
    </tr>`;
  }

  async function load() {
    try {
      sessions = (await apiFetch(`/users/${encodeURIComponent(login)}/tokens?status=active`)) ?? [];
      render();
    } catch (err) {
      body.innerHTML = '<tr><td colspan="6">No se pudieron cargar las sesiones.</td></tr>';
      showError(err.message);
    }
  }

  initSortableHeaders(document.getElementById('sessions-head'), {
    sort,
    defaultDesc: () => true,
    onChange: render,
  });

  body.addEventListener('click', async (event) => {
    const button = event.target.closest('button[data-close-session]');
    if (!button) return;
    const session = sessions.find((s) => s.id === Number(button.dataset.closeSession));
    if (!session) return;
    const question = session.current
      ? 'Es la sesión que estás usando ahora: si la cierras saldrás de la intranet y tendrás que volver a entrar. ¿Cerrarla?'
      : '¿Cerrar esta sesión? Ese dispositivo tendrá que volver a pedir un enlace para entrar.';
    if (!confirm(question)) return;
    button.disabled = true;
    try {
      await apiFetch(`/users/${encodeURIComponent(login)}/tokens/${session.id}`, {
        method: 'DELETE',
      });
      if (session.current) {
        logout();
        return;
      }
      showFeedback('Sesión cerrada.');
      await load();
    } catch (err) {
      button.disabled = false;
      showError(err.message);
    }
  });

  return load();
}

export { initSessionsPanel };
