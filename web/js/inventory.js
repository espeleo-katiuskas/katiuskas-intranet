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
// Managing material transactions (loans, rentals, sales, returns) is open
// to admin/board plus the optional "material" role — item CRUD stays
// admin/board-only, so this is deliberately a separate check from isAdmin.
const canManageTransactions = hasAnyRole(user, ['admin', 'board', 'material']);

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

const itemStatusLabels = {
  available: 'Disponible',
  on_loan: 'Prestado',
  rented: 'Alquilado',
  sold: 'Vendido',
  maintenance: 'En mantenimiento',
  retired: 'De baja',
};

const typeLabels = { loan: 'Préstamo', rental: 'Alquiler', sale: 'Venta' };
const txStatusLabels = { active: 'Activa', completed: 'Completada', overdue: 'Vencida', cancelled: 'Cancelada' };

let itemsById = {};

function renderItemRow(item) {
  return `
    <tr>
      <td>${escapeHtml(item.name)}</td>
      <td>${escapeHtml(item.category) || '—'}</td>
      <td>${item.available_quantity} / ${item.total_quantity}</td>
      <td><span class="badge">${itemStatusLabels[item.status] ?? item.status}</span></td>
      <td><button type="button" class="btn btn-secondary btn-small" data-toggle-item-transactions="${item.id}">Movimientos</button></td>
    </tr>
    <tr data-item-transactions-row="${item.id}" hidden>
      <td colspan="5"><div class="detail-list" data-item-transactions-panel="${item.id}"></div></td>
    </tr>
  `;
}

function renderTransactionDetail(t) {
  const canReturn = canManageTransactions && t.status === 'active' && t.type !== 'sale';
  const dueOrReturned = t.returned_at
    ? ` · devuelto ${formatDate(t.returned_at)}`
    : t.due_date
      ? ` · vence ${formatDate(t.due_date)}`
      : '';
  return `
    <div class="detail-row">
      <span>${typeLabels[t.type] ?? t.type} · Socio n.º ${t.user_id} · cantidad ${t.quantity} ·
        ${formatDate(t.transacted_at)}${dueOrReturned} ·
        <span class="badge">${txStatusLabels[t.status] ?? t.status}</span></span>
      ${canReturn ? `<button type="button" class="btn btn-secondary btn-small" data-return="${t.id}">Registrar devolución</button>` : ''}
    </div>
  `;
}

async function loadItems() {
  const tableBody = document.getElementById('items-table-body');
  try {
    const items = await apiFetch('/items');
    itemsById = Object.fromEntries(items.map((item) => [item.id, item]));
    tableBody.innerHTML = items.length
      ? items.map(renderItemRow).join('')
      : '<tr><td colspan="5">Todavía no hay material dado de alta.</td></tr>';
    if (canManageTransactions) {
      const select = document.getElementById('nt-item');
      select.innerHTML = items.map((item) => `<option value="${item.id}">${escapeHtml(item.name)}</option>`).join('');
    }
  } catch {
    tableBody.innerHTML = '<tr><td colspan="5">No se pudo cargar el material.</td></tr>';
  }
}

document.getElementById('items-table-body').addEventListener('click', async (event) => {
  const toggleButton = event.target.closest('button[data-toggle-item-transactions]');
  if (toggleButton) {
    const itemId = toggleButton.dataset.toggleItemTransactions;
    const row = document.querySelector(`[data-item-transactions-row="${itemId}"]`);
    row.hidden = !row.hidden;
    if (!row.hidden) {
      const panel = row.querySelector(`[data-item-transactions-panel="${itemId}"]`);
      panel.innerHTML = 'Cargando…';
      try {
        const transactions = await apiFetch(`/items/${itemId}/transactions`);
        panel.innerHTML = transactions.length
          ? transactions.map(renderTransactionDetail).join('')
          : '<p>Todavía no tiene movimientos.</p>';
      } catch (err) {
        panel.innerHTML = `<p>${escapeHtml(err.message)}</p>`;
      }
    }
    return;
  }

  const returnButton = event.target.closest('button[data-return]');
  if (returnButton) {
    await handleReturn(returnButton);
  }
});

async function handleReturn(button) {
  button.disabled = true;
  try {
    await apiFetch(`/transactions/${button.dataset.return}/return`, { method: 'PATCH' });
    showFeedback('Devolución registrada.');
    await loadItems();
    if (isAdmin) await loadActiveTransactions();
  } catch (err) {
    button.disabled = false;
    showError(err.message);
  }
}

function renderActiveTransactionRow(t) {
  const itemName = itemsById[t.item_id]?.name;
  const canReturn = canManageTransactions && t.type !== 'sale';
  return `
    <tr>
      <td>${itemName ? escapeHtml(itemName) : `Material n.º ${t.item_id}`}</td>
      <td>Socio n.º ${t.user_id}</td>
      <td>${typeLabels[t.type] ?? t.type}</td>
      <td>${t.quantity}</td>
      <td>${t.due_date ? formatDate(t.due_date) : '—'}</td>
      <td>${canReturn ? `<button type="button" class="btn btn-secondary btn-small" data-return="${t.id}">Registrar devolución</button>` : '—'}</td>
    </tr>
  `;
}

async function loadActiveTransactions() {
  const tableBody = document.getElementById('active-transactions-table-body');
  try {
    const transactions = await apiFetch('/transactions?status=active');
    tableBody.innerHTML = transactions.length
      ? transactions.map(renderActiveTransactionRow).join('')
      : '<tr><td colspan="6">No hay movimientos activos.</td></tr>';
  } catch {
    tableBody.innerHTML = '<tr><td colspan="6">No se pudieron cargar los movimientos.</td></tr>';
  }
}

document.getElementById('active-transactions-table-body')?.addEventListener('click', async (event) => {
  const returnButton = event.target.closest('button[data-return]');
  if (returnButton) {
    await handleReturn(returnButton);
  }
});

if (isAdmin) {
  const newItemPanel = document.getElementById('new-item-panel');
  const newItemForm = document.getElementById('new-item-form');

  document.getElementById('toggle-new-item').addEventListener('click', () => {
    newItemPanel.hidden = !newItemPanel.hidden;
  });

  document.getElementById('cancel-new-item').addEventListener('click', () => {
    newItemForm.reset();
    newItemPanel.hidden = true;
  });

  newItemForm.addEventListener('submit', async (event) => {
    event.preventDefault();
    const formData = new FormData(newItemForm);
    const optionalText = (key) => formData.get(key)?.toString().trim() || null;
    const optionalNumber = (key) => (formData.get(key) ? Number(formData.get(key)) : null);

    const body = {
      name: formData.get('name'),
      description: optionalText('description'),
      category: optionalText('category'),
      code: optionalText('code'),
      total_quantity: Number(formData.get('total_quantity')) || 1,
      rental_price_per_week: optionalNumber('rental_price_per_week'),
      sale_price: optionalNumber('sale_price'),
    };

    const submitButton = newItemForm.querySelector('button[type="submit"]');
    submitButton.disabled = true;
    try {
      await apiFetch('/items', { method: 'POST', body: JSON.stringify(body) });
      showFeedback('Material creado.');
      newItemForm.reset();
      newItemPanel.hidden = true;
      await loadItems();
    } catch (err) {
      showError(err.message);
    } finally {
      submitButton.disabled = false;
    }
  });
}

// Both socios and activities pile up over time, so a plain <select> stops
// being usable — these are text inputs backed by a <datalist> instead,
// searchable as you type. See pickerLabel/renderPickerOptions/
// resolvePickerId in api.js.
let userEntries = [];
let activityEntries = [];

if (canManageTransactions) {
  const userInput = document.getElementById('nt-user');
  const userOptions = document.getElementById('nt-user-options');
  apiFetch('/users/directory')
    .then((people) => {
      userEntries = people
        .slice()
        .sort((a, b) => `${a.last_name} ${a.first_name}`.localeCompare(`${b.last_name} ${b.first_name}`, 'es'))
        .map((person) => ({ id: person.id, label: pickerLabel(`${person.first_name} ${person.last_name}`, person.id) }));
      userOptions.innerHTML = renderPickerOptions(userEntries);
    })
    .catch(() => showError('No se pudo cargar la lista de socios.'));

  const activityInput = document.getElementById('nt-activity');
  const activityOptions = document.getElementById('nt-activity-options');
  apiFetch('/activities?status=active')
    .then((activities) => {
      activityEntries = activities.map((activity) => ({
        id: activity.id,
        label: pickerLabel(`${formatDate(activity.start_date)} — ${activity.title}`, activity.id),
      }));
      activityOptions.innerHTML = renderPickerOptions(activityEntries);
    })
    .catch(() => showError('No se pudo cargar la lista de actividades.'));

  const typeSelect = document.getElementById('nt-type');
  const dueDateField = document.getElementById('nt-due-date-field');
  const priceField = document.getElementById('nt-price-field');
  const activityField = document.getElementById('nt-activity-field');

  function updateTransactionFieldsVisibility() {
    const type = typeSelect.value;
    dueDateField.hidden = type === 'sale';
    priceField.hidden = type === 'loan';
    // Loans and rentals always happen as part of an activity; a sale
    // doesn't (see the loan_and_rental_require_activity constraint).
    activityField.hidden = type === 'sale';
    activityInput.required = type !== 'sale';
  }
  typeSelect.addEventListener('change', updateTransactionFieldsVisibility);
  updateTransactionFieldsVisibility();

  const newTransactionForm = document.getElementById('new-transaction-form');
  newTransactionForm.addEventListener('submit', async (event) => {
    event.preventDefault();
    const formData = new FormData(newTransactionForm);
    const type = formData.get('type');

    const userId = resolvePickerId(userEntries, userInput.value);
    if (!userId) {
      showError('Elige un socio de la lista.');
      return;
    }
    let activityId = null;
    if (!activityField.hidden) {
      activityId = resolvePickerId(activityEntries, activityInput.value);
      if (!activityId) {
        showError('Elige una actividad de la lista.');
        return;
      }
    }

    const body = {
      item_id: Number(formData.get('item_id')),
      user_id: userId,
      type,
      quantity: Number(formData.get('quantity')) || 1,
      due_date: type !== 'sale' ? formData.get('due_date') || null : null,
      price: type !== 'loan' && formData.get('price') ? Number(formData.get('price')) : null,
      activity_id: activityId,
    };

    const submitButton = newTransactionForm.querySelector('button[type="submit"]');
    submitButton.disabled = true;
    try {
      await apiFetch('/transactions', { method: 'POST', body: JSON.stringify(body) });
      showFeedback('Transacción registrada.');
      newTransactionForm.reset();
      updateTransactionFieldsVisibility();
      await loadItems();
      await loadActiveTransactions();
    } catch (err) {
      showError(err.message);
    } finally {
      submitButton.disabled = false;
    }
  });

  loadActiveTransactions();
}

loadItems();
