import { initSession, logout, applyRoleVisibility, apiFetch } from './api.js';

await initSession();
applyRoleVisibility();
document.getElementById('logout-button').addEventListener('click', logout);

const statusLabels = {
  available: 'Disponible',
  on_loan: 'Prestado',
  rented: 'Alquilado',
  sold: 'Vendido',
  maintenance: 'En mantenimiento',
  retired: 'De baja',
};

async function loadItems() {
  const tableBody = document.getElementById('items-table-body');
  try {
    const items = await apiFetch('/items');
    tableBody.innerHTML = items
      .map((item) => `
        <tr>
          <td>${item.name}</td>
          <td>${item.category ?? '—'}</td>
          <td>${item.available_quantity} / ${item.total_quantity}</td>
          <td><span class="badge">${statusLabels[item.status] ?? item.status}</span></td>
        </tr>
      `)
      .join('');
  } catch {
    tableBody.innerHTML = '<tr><td colspan="4">No se pudo cargar el material.</td></tr>';
  }
}

loadItems();
