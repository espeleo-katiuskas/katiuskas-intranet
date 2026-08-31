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

const itemTypeLabels = { rope: 'Cuerda', bag: 'Saca', gear: 'Material' };
const ropeTypeLabels = { caving: 'Espeleología', canyoning: 'Cañones' };
const bagTypeLabels = { caving: 'Espeleología', canyoning: 'Cañones', quick: 'Rápida', special: 'Especial' };

const typeLabels = { loan: 'Préstamo', rental: 'Alquiler', sale: 'Venta' };
const txStatusLabels = { active: 'Activa', completed: 'Completada', overdue: 'Vencida', cancelled: 'Cancelada' };

let itemsById = {};
// Reassigned below when canManageTransactions is true; loadItems() (run
// unconditionally) calls it every time the item picker is repopulated,
// so it has to exist at module scope even when there's nothing to do.
let updateTransactionQuantityVisibility = () => {};

// A rope/bag is identified by brand+model (plus its type-specific
// attributes); gear may have no known brand/model at all, so its
// category — always set — anchors the label instead.
function itemLabel(item) {
  const brandModel = [item.brand, item.model].filter(Boolean).join(' ');
  if (item.item_type === 'rope') {
    const parts = [brandModel || null, `${item.diameter_mm} mm`, ropeTypeLabels[item.rope_type] ?? item.rope_type, item.manufacture_year, item.length_m ? `${item.length_m} m` : null];
    return escapeHtml(parts.filter(Boolean).join(' · '));
  }
  if (item.item_type === 'bag') {
    const parts = [brandModel || null, `${item.capacity_liters} L`, bagTypeLabels[item.bag_type] ?? item.bag_type];
    return escapeHtml(parts.filter(Boolean).join(' · '));
  }
  const parts = [item.category_name, brandModel || null];
  return escapeHtml(parts.filter(Boolean).join(' — '));
}

function renderItemRow(item) {
  return `
    <tr>
      <td>${itemTypeLabels[item.item_type] ?? item.item_type}</td>
      <td>${itemLabel(item)}</td>
      <td>${item.code ? escapeHtml(item.code) : '—'}</td>
      <td>${item.available_quantity} / ${item.total_quantity}</td>
      <td><span class="badge">${itemStatusLabels[item.status] ?? item.status}</span></td>
      <td><button type="button" class="btn btn-secondary btn-small" data-toggle-item-transactions="${item.id}">Movimientos</button></td>
    </tr>
    <tr data-item-transactions-row="${item.id}" hidden>
      <td colspan="6"><div class="detail-list" data-item-transactions-panel="${item.id}"></div></td>
    </tr>
  `;
}

function renderTransactionDetail(t) {
  const canReturn = canManageTransactions && t.status === 'active' && t.type !== 'sale';
  const returned = t.returned_at ? ` · devuelto ${formatDate(t.returned_at)}` : '';
  return `
    <div class="detail-row">
      <span>${typeLabels[t.type] ?? t.type} · Socio ${t.user_login} · cantidad ${t.quantity} ·
        ${formatDate(t.transacted_at)}${returned} ·
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
      : '<tr><td colspan="6">Todavía no hay material dado de alta.</td></tr>';
    if (canManageTransactions) {
      const select = document.getElementById('nt-item');
      select.innerHTML = items
        .map((item) => `<option value="${item.id}" data-item-type="${item.item_type}">${itemTypeLabels[item.item_type] ?? item.item_type} — ${itemLabel(item)}</option>`)
        .join('');
      updateTransactionQuantityVisibility();
    }
  } catch {
    tableBody.innerHTML = '<tr><td colspan="6">No se pudo cargar el material.</td></tr>';
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
  const item = itemsById[t.item_id];
  const canReturn = canManageTransactions && t.type !== 'sale';
  return `
    <tr>
      <td>${item ? itemLabel(item) : `Material n.º ${t.item_id}`}</td>
      <td>Socio ${t.user_login}</td>
      <td>${typeLabels[t.type] ?? t.type}</td>
      <td>${t.quantity}</td>
      <td>${formatDate(t.transacted_at)}</td>
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
  const itemTypeSelect = document.getElementById('ni-item-type');
  const descriptionField = document.getElementById('ni-description');
  const categorySelect = document.getElementById('ni-category');

  // Only rope/bag/gear-specific controls toggle required — the fields
  // shared by every type (acquired_at, prices, loanable) stay as they
  // are regardless of item_type.
  const requiredWhenVisible = ['ni-brand', 'ni-model', 'ni-code', 'ni-rope-type', 'ni-diameter-mm', 'ni-manufacture-year', 'ni-length-m', 'ni-bag-type', 'ni-capacity-liters', 'ni-category'];

  function updateItemTypeFieldsVisibility() {
    const type = itemTypeSelect.value;
    document.querySelectorAll('[data-item-type-field]').forEach((el) => {
      el.hidden = !el.dataset.itemTypeField.split(',').includes(type);
    });
    for (const id of requiredWhenVisible) {
      const input = document.getElementById(id);
      input.required = !input.closest('[data-item-type-field]').hidden;
    }
    // description is the required "Descripción" for gear, and the
    // optional "Observaciones" for rope/bag — see the two toggled
    // label spans above the textarea.
    descriptionField.required = type === 'gear';
  }
  itemTypeSelect.addEventListener('change', updateItemTypeFieldsVisibility);
  updateItemTypeFieldsVisibility();

  apiFetch('/material-categories')
    .then((categories) => {
      categorySelect.innerHTML = categories.map((c) => `<option value="${c.id}">${escapeHtml(c.name)}</option>`).join('');
    })
    .catch(() => showError('No se pudo cargar la lista de categorías.'));

  document.getElementById('toggle-new-item').addEventListener('click', () => {
    newItemPanel.hidden = !newItemPanel.hidden;
  });

  document.getElementById('cancel-new-item').addEventListener('click', () => {
    newItemForm.reset();
    newItemPanel.hidden = true;
    updateItemTypeFieldsVisibility();
  });

  newItemForm.addEventListener('submit', async (event) => {
    event.preventDefault();
    const formData = new FormData(newItemForm);
    const type = formData.get('item_type');
    const optionalText = (key) => formData.get(key)?.toString().trim() || null;
    const optionalNumber = (key) => (formData.get(key) ? Number(formData.get(key)) : null);

    const body = {
      item_type: type,
      description: optionalText('description'),
      acquired_at: optionalText('acquired_at'),
      rental_price_per_week: optionalNumber('rental_price_per_week'),
      sale_price: optionalNumber('sale_price'),
      loanable: formData.get('loanable') === 'on',
    };
    if (type === 'rope' || type === 'bag') {
      body.brand = formData.get('brand');
      body.model = formData.get('model');
      body.code = formData.get('code');
    }
    if (type === 'rope') {
      body.rope_type = formData.get('rope_type');
      body.diameter_mm = Number(formData.get('diameter_mm'));
      body.manufacture_year = Number(formData.get('manufacture_year'));
      body.length_m = Number(formData.get('length_m'));
    } else if (type === 'bag') {
      body.bag_type = formData.get('bag_type');
      body.capacity_liters = Number(formData.get('capacity_liters'));
    } else {
      body.category_id = Number(formData.get('category_id'));
      body.total_quantity = Number(formData.get('total_quantity')) || 1;
    }

    const submitButton = newItemForm.querySelector('button[type="submit"]');
    submitButton.disabled = true;
    try {
      await apiFetch('/items', { method: 'POST', body: JSON.stringify(body) });
      showFeedback('Material creado.');
      newItemForm.reset();
      newItemPanel.hidden = true;
      updateItemTypeFieldsVisibility();
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
        .map((person) => ({ id: person.login, label: pickerLabel(`${person.first_name} ${person.last_name}`, person.login) }));
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
  const activityField = document.getElementById('nt-activity-field');
  const itemSelect = document.getElementById('nt-item');
  const quantityField = document.getElementById('nt-quantity-field');
  const quantityInput = document.getElementById('nt-quantity');

  function updateTransactionFieldsVisibility() {
    const type = typeSelect.value;
    // Loans and rentals always happen as part of an activity; a sale
    // doesn't (see the loan_and_rental_require_activity constraint).
    activityField.hidden = type === 'sale';
    activityInput.required = type !== 'sale';
  }
  typeSelect.addEventListener('change', updateTransactionFieldsVisibility);
  updateTransactionFieldsVisibility();

  // rope/bag are individually tracked (always total_quantity 1), so
  // there's no quantity to choose once one of those is selected.
  updateTransactionQuantityVisibility = function () {
    const selected = itemSelect.selectedOptions[0];
    const isSingleUnit = selected?.dataset.itemType === 'rope' || selected?.dataset.itemType === 'bag';
    quantityField.hidden = isSingleUnit;
    if (isSingleUnit) quantityInput.value = '1';
  };
  itemSelect.addEventListener('change', updateTransactionQuantityVisibility);

  const newTransactionForm = document.getElementById('new-transaction-form');
  newTransactionForm.addEventListener('submit', async (event) => {
    event.preventDefault();
    const formData = new FormData(newTransactionForm);
    const type = formData.get('type');

    const userLogin = resolvePickerId(userEntries, userInput.value);
    if (!userLogin) {
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
      login: userLogin,
      type,
      quantity: Number(formData.get('quantity')) || 1,
      transacted_at: formData.get('transacted_at') || null,
      activity_id: activityId,
    };

    const submitButton = newTransactionForm.querySelector('button[type="submit"]');
    submitButton.disabled = true;
    try {
      await apiFetch('/transactions', { method: 'POST', body: JSON.stringify(body) });
      showFeedback('Transacción registrada.');
      newTransactionForm.reset();
      updateTransactionFieldsVisibility();
      updateTransactionQuantityVisibility();
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
