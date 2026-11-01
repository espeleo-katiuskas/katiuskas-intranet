import { apiFetch, currentUser, roleLabel, showError } from './api.js';
import { initModal, openModal } from './modal.js';

// "Ver como…": an admin chooses the roles their own session acts with for a while (an hour), to see a
// page as somebody who is not an admin, or has only member, or member and money... would. It does not
// change anybody's real roles, and ends by itself or with "Volver a mis roles" in the banner every
// page shows meanwhile (api.js, renderRoleOverrideBanner).

// Common combinations. Each is offered only if all its roles exist.
const PRESETS = [
  { label: 'Socio', roles: ['member'] },
  { label: 'Socio con material', roles: ['member', 'material'] },
  { label: 'Socio con money', roles: ['member', 'money'] },
  { label: 'Junta', roles: ['board'] },
  { label: 'Administrador', roles: ['admin'] },
];

let dialog = null;
let form = null;
let allRoles = [];

function build() {
  dialog = document.createElement('dialog');
  dialog.id = 'role-override-dialog';
  dialog.className = 'panel modal';
  dialog.innerHTML = `
    <button type="button" class="modal-close" data-modal-close aria-label="Cerrar">&times;</button>
    <h3>Ver la intranet como…</h3>
    <p class="hint">Marca los roles con los que quieres ver la intranet durante una hora. Tus roles reales no cambian, y puedes volver a ellos cuando quieras desde el aviso de arriba. Hace falta al menos Administrador, Junta o Socio: sin ninguno no se puede usar nada.</p>
    <form id="role-override-form">
      <div id="role-override-presets" class="table-actions"></div>
      <div id="role-override-roles" class="field"></div>
      <div class="panel-actions">
        <button type="submit" class="btn btn-primary">Ver con estos roles</button>
        <button type="button" class="btn btn-secondary" data-modal-close>Cancelar</button>
      </div>
    </form>`;
  document.body.append(dialog);
  form = dialog.querySelector('form');
  initModal(dialog, form);

  dialog.querySelector('#role-override-presets').addEventListener('click', (event) => {
    const button = event.target.closest('button[data-preset]');
    if (!button) return;
    const roles = PRESETS[Number(button.dataset.preset)].roles;
    for (const box of form.querySelectorAll('input[name="role"]')) {
      box.checked = roles.includes(box.value);
    }
  });

  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    const roles = [...form.querySelectorAll('input[name="role"]:checked')].map((box) => box.value);
    const submit = form.querySelector('button[type="submit"]');
    submit.disabled = true;
    try {
      await apiFetch('/auth/role-override', { method: 'PUT', body: JSON.stringify({ roles }) });
      window.location.reload();
    } catch (err) {
      dialog.close();
      showError(err.message);
    } finally {
      submit.disabled = false;
    }
  });
}

async function open() {
  try {
    if (!dialog) build();
    allRoles = await apiFetch('/roles');
  } catch (err) {
    showError(err.message);
    return;
  }
  const current = currentUser().roles;
  const names = allRoles.map((role) => role.name);
  dialog.querySelector('#role-override-roles').innerHTML = allRoles
    .map(
      (role) =>
        `<label class="checkbox-label"><input type="checkbox" name="role" value="${role.name}"${
          current.includes(role.name) ? ' checked' : ''
        }> ${roleLabel(role.name)}${role.is_automatic ? ' <span class="hint">(automático)</span>' : ''}</label>`,
    )
    .join('');
  dialog.querySelector('#role-override-presets').innerHTML = PRESETS.map((preset, index) =>
    preset.roles.every((name) => names.includes(name))
      ? `<button type="button" class="btn btn-secondary btn-small" data-preset="${index}">${preset.label}</button>`
      : '',
  ).join('');
  openModal(dialog);
}

// Called once by every page's initPage: wires the topbar button (only admins have it) and the
// banner's "Cambiar roles".
function initRoleOverride() {
  const button = document.getElementById('role-override-button');
  // An impersonation is somebody else's session: there is nothing to change in it.
  if (currentUser().impersonatedBy) button?.remove();
  else button?.addEventListener('click', open);
  document.addEventListener('katiuskas:role-override', open);
}

export { initRoleOverride };
