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
// Managing material movements (loans, rentals, sales, acquisitions,
// losses, abandonments, retirements, returns) is open to admin/board
// plus the optional "material" role — item CRUD stays admin/board-only,
// so this is deliberately a separate check from isAdmin.
const canManageMovements = hasAnyRole(user, ['admin', 'board', 'material']);

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
  lost: 'Perdido',
  abandoned: 'Abandonado',
  maintenance: 'En mantenimiento',
  retired: 'De baja',
};

const itemTypeLabels = { rope: 'Cuerda', bag: 'Saca', gear: 'Material' };
const ropeTypeLabels = { caving: 'Espeleología', canyoning: 'Cañones' };
const bagTypeLabels = { caving: 'Espeleología', canyoning: 'Cañones', quick: 'Rápida', special: 'Especial' };

const typeLabels = {
  loan: 'Préstamo',
  rental: 'Alquiler',
  sale: 'Venta',
  acquisition: 'Adquisición',
  loss: 'Pérdida',
  abandonment: 'Abandono',
  retirement: 'Retirada',
};
// Movement types with a recipient — everything else (acquisition, loss,
// abandonment, retirement) has no socio, though loss/abandonment can
// still carry one when it's known who was carrying the item.
const recipientRequiredTypes = new Set(['loan', 'rental', 'sale']);
const movementStatusLabels = { active: 'Activo', completed: 'Completado', overdue: 'Vencido', cancelled: 'Cancelado' };

let itemsById = {};
// Reassigned below when canManageMovements is true; loadItems() (run
// unconditionally) calls it every time the item picker is repopulated,
// so it has to exist at module scope even when there's nothing to do.
let updateMovementQuantityVisibility = () => {};

// Populated from /users/directory (see the canManageMovements block
// below) so movement rows can show a person's name instead of their
// bare login.
let directoryByLogin = {};

function personLabel(login) {
  const person = directoryByLogin[login];
  return person ? `${person.first_name} ${person.last_name}` : login;
}

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
  const parts = [item.category_name, brandModel || null, item.description || null];
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
      <td><button type="button" class="btn btn-secondary btn-small" data-toggle-item-movements="${item.id}">Movimientos</button></td>
    </tr>
    <tr data-item-movements-row="${item.id}" hidden>
      <td colspan="6"><div class="detail-list" data-item-movements-panel="${item.id}"></div></td>
    </tr>
  `;
}

// Only loan and rental are ever returnable — a sale (or acquisition/
// loss/abandonment/retirement) is settled the moment it's recorded, so
// this checks the type itself rather than just status: a stray 'active'
// sale row (from before movements existed, say) still shouldn't offer
// a return button.
function isReturnable(m) {
  return m.type === 'loan' || m.type === 'rental';
}

function renderMovementDetail(m) {
  const canReturn = canManageMovements && isReturnable(m) && m.status === 'active';
  const returned = m.returned_at ? ` · devuelto ${formatDate(m.returned_at)}` : '';
  const who = m.user_login ? ` · Socio ${escapeHtml(personLabel(m.user_login))}` : '';
  const notes = m.notes ? ` · ${escapeHtml(m.notes)}` : '';
  return `
    <div class="detail-row">
      <span>${formatDate(m.transacted_at)} · ${typeLabels[m.type] ?? m.type}${who} · cantidad ${m.quantity}${returned}${notes} ·
        <span class="badge">${movementStatusLabels[m.status] ?? m.status}</span></span>
      ${canReturn ? `<button type="button" class="btn btn-secondary btn-small" data-return="${m.id}">Registrar devolución</button>` : ''}
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
    if (canManageMovements) {
      const select = document.getElementById('nt-item');
      select.innerHTML = items
        .map((item) => `<option value="${item.id}" data-item-type="${item.item_type}">${itemTypeLabels[item.item_type] ?? item.item_type} — ${itemLabel(item)}</option>`)
        .join('');
      updateMovementQuantityVisibility();
    }
  } catch {
    tableBody.innerHTML = '<tr><td colspan="6">No se pudo cargar el material.</td></tr>';
  }
}

document.getElementById('items-table-body').addEventListener('click', async (event) => {
  const toggleButton = event.target.closest('button[data-toggle-item-movements]');
  if (toggleButton) {
    const itemId = toggleButton.dataset.toggleItemMovements;
    const row = document.querySelector(`[data-item-movements-row="${itemId}"]`);
    row.hidden = !row.hidden;
    if (!row.hidden) {
      const panel = row.querySelector(`[data-item-movements-panel="${itemId}"]`);
      panel.innerHTML = 'Cargando…';
      try {
        const movements = await apiFetch(`/items/${itemId}/movements`);
        panel.innerHTML = movements.length
          ? movements.map(renderMovementDetail).join('')
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
    await apiFetch(`/movements/${button.dataset.return}/return`, { method: 'PATCH' });
    showFeedback('Devolución registrada.');
    await loadItems();
    if (isAdmin) await loadActiveMovements();
  } catch (err) {
    button.disabled = false;
    showError(err.message);
  }
}

function renderActiveMovementRow(m) {
  const item = itemsById[m.item_id];
  const canReturn = canManageMovements && isReturnable(m);
  return `
    <tr>
      <td>${formatDate(m.transacted_at)}</td>
      <td>${item ? itemLabel(item) : `Material n.º ${m.item_id}`}</td>
      <td>${m.user_login ? escapeHtml(personLabel(m.user_login)) : '—'}</td>
      <td>${typeLabels[m.type] ?? m.type}</td>
      <td>${m.quantity}</td>
      <td>${canReturn ? `<button type="button" class="btn btn-secondary btn-small" data-return="${m.id}">Registrar devolución</button>` : '—'}</td>
    </tr>
  `;
}

async function loadActiveMovements() {
  const tableBody = document.getElementById('active-movements-table-body');
  try {
    // A sale (or acquisition/loss/abandonment/retirement) is never
    // really "active" — only loan/rental can be — so this filters
    // defensively in case stray data ever says otherwise.
    const movements = (await apiFetch('/movements?status=active')).filter(isReturnable);
    tableBody.innerHTML = movements.length
      ? movements.map(renderActiveMovementRow).join('')
      : '<tr><td colspan="6">No hay movimientos activos.</td></tr>';
  } catch {
    tableBody.innerHTML = '<tr><td colspan="6">No se pudieron cargar los movimientos.</td></tr>';
  }
}

document.getElementById('active-movements-table-body')?.addEventListener('click', async (event) => {
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

// Awaited before loadActiveMovements() below, so itemsById is populated
// (and the "Material" column can show a real label instead of falling
// back to "Material n.º …") by the time it renders.
await loadItems();

if (canManageMovements) {
  const userInput = document.getElementById('nt-user');
  const userOptions = document.getElementById('nt-user-options');
  const directoryLoaded = apiFetch('/users/directory')
    .then((people) => {
      directoryByLogin = Object.fromEntries(people.map((person) => [person.login, person]));
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
  const userOptionalHint = document.getElementById('nt-user-optional-hint');
  const itemSelect = document.getElementById('nt-item');
  const quantityField = document.getElementById('nt-quantity-field');
  const quantityInput = document.getElementById('nt-quantity');

  function updateMovementFieldsVisibility() {
    const type = typeSelect.value;
    // Loans and rentals always happen as part of an activity (see the
    // activity_required_for_loan_and_rental constraint); every other
    // type leaves it optional.
    const requiresActivity = type === 'loan' || type === 'rental';
    activityField.hidden = !requiresActivity;
    activityInput.required = requiresActivity;
    // loan/rental/sale exchange the item with a specific socio;
    // acquisition/loss/abandonment/retirement don't, though a socio can
    // still be given for loss/abandonment when it's known who had it.
    userOptionalHint.hidden = recipientRequiredTypes.has(type);
  }
  typeSelect.addEventListener('change', updateMovementFieldsVisibility);
  updateMovementFieldsVisibility();

  // rope/bag are individually tracked (always exactly one unit), so
  // there's no quantity to choose once one of those is selected.
  updateMovementQuantityVisibility = function () {
    const selected = itemSelect.selectedOptions[0];
    const isSingleUnit = selected?.dataset.itemType === 'rope' || selected?.dataset.itemType === 'bag';
    quantityField.hidden = isSingleUnit;
    if (isSingleUnit) quantityInput.value = '1';
  };
  itemSelect.addEventListener('change', updateMovementQuantityVisibility);
  updateMovementQuantityVisibility();

  const newMovementForm = document.getElementById('new-movement-form');
  newMovementForm.addEventListener('submit', async (event) => {
    event.preventDefault();
    const formData = new FormData(newMovementForm);
    const type = formData.get('type');

    let userLogin = null;
    if (userInput.value.trim()) {
      userLogin = resolvePickerId(userEntries, userInput.value);
      if (!userLogin) {
        showError('Elige un socio de la lista, o déjalo vacío.');
        return;
      }
    } else if (recipientRequiredTypes.has(type)) {
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
      notes: formData.get('notes')?.toString().trim() || null,
    };

    const submitButton = newMovementForm.querySelector('button[type="submit"]');
    submitButton.disabled = true;
    try {
      await apiFetch('/movements', { method: 'POST', body: JSON.stringify(body) });
      showFeedback('Movimiento registrado.');
      newMovementForm.reset();
      updateMovementFieldsVisibility();
      updateMovementQuantityVisibility();
      await loadItems();
      await loadActiveMovements();
    } catch (err) {
      showError(err.message);
    } finally {
      submitButton.disabled = false;
    }
  });

  // Awaited so the "Socio" column has names to show on first paint
  // instead of falling back to the bare login until a re-render happens.
  await directoryLoaded;
  loadActiveMovements();
}
