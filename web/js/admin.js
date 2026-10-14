import {
  apiFetch,
  escapeHtml,
  roleLabel,
  showFeedback,
  showError,
} from './api.js';
import { initPage } from './layout.js';
import { initModal, openModal } from './modal.js';

// Everything here is for administrators only — the nav link is hidden from
// anyone else and anyone else who lands here is sent home. More sections
// will join the list of roles.
await initPage({ requireAnyRole: ['admin'] });

// "Roles": the catalog of roles — the system ones that give permissions and
// the free-form labels an admin creates (see GET/POST/DELETE /roles).
async function loadRoles() {
  const tableBody = document.getElementById('roles-table-body');
  try {
    const roles = await apiFetch('/roles');
    tableBody.innerHTML = roles
      .map((role) => `
        <tr>
          <td>${escapeHtml(roleLabel(role.name))}</td>
          <td>${escapeHtml(role.description) || '—'}</td>
          <td>
            ${role.is_system ? '<span class="badge">Del sistema</span>' : ''}
            ${role.is_automatic ? '<span class="badge" title="Se asigna solo, según el historial de socio">Automático</span>' : ''}
          </td>
          <td>${!role.is_system ? `<button type="button" class="btn btn-danger btn-small" data-delete-role="${role.id}">Borrar</button>` : ''}</td>
        </tr>
      `)
      .join('');
  } catch {
    tableBody.innerHTML = '<tr><td colspan="4">No se pudieron cargar los roles.</td></tr>';
  }
}

document.getElementById('roles-table-body').addEventListener('click', async (event) => {
  const button = event.target.closest('button[data-delete-role]');
  if (!button) return;
  if (!confirm('¿Borrar este rol? Se quitará de todas las personas que lo tengan.')) return;

  button.disabled = true;
  try {
    await apiFetch(`/roles/${button.dataset.deleteRole}`, { method: 'DELETE' });
    showFeedback('Rol borrado.');
    await loadRoles();
  } catch (err) {
    button.disabled = false;
    showError(err.message);
  }
});

const newRoleDialog = document.getElementById('new-role-dialog');
const newRoleForm = document.getElementById('new-role-form');
initModal(newRoleDialog, newRoleForm);

document.getElementById('toggle-new-role').addEventListener('click', () => {
  openModal(newRoleDialog);
});

newRoleForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  const formData = new FormData(newRoleForm);
  const body = {
    name: formData.get('name'),
    description: formData.get('description')?.toString().trim() || null,
  };

  const submitButton = newRoleForm.querySelector('button[type="submit"]');
  submitButton.disabled = true;
  try {
    await apiFetch('/roles', { method: 'POST', body: JSON.stringify(body) });
    showFeedback('Rol creado.');
    newRoleDialog.close();
    await loadRoles();
  } catch (err) {
    showError(err.message);
  } finally {
    submitButton.disabled = false;
  }
});

await loadRoles();
