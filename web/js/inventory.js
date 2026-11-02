import {
  hasAnyRole,
  apiFetch,
  escapeHtml,
  formatDate,
  normalizeForSearch,
  pickerLabel,
  renderPickerOptions,
  resolvePickerId,
  showFeedback,
  showError,
} from './api.js';
import { initPage } from './layout.js';
import { initModal, openModal } from './modal.js';
import { bagTypeLabels, itemLabel, itemTypeLabels, ropeTypeLabels } from './materialCommon.js';

const user = await initPage();
// Managing the material — adding equipment to the catalog, and its movements (loans, rentals,
// sales, acquisitions, losses, abandonments, retirements, returns) — is for admin and board plus the
// optional "material" role.
const canManageMovements = hasAnyRole(user, ['admin', 'board', 'material']);

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

// Every movement type, for display — loans still show up in an item's
// history even though they're no longer created from this page (see
// movementTypeOptions below).
const typeLabels = {
  loan: 'Préstamo',
  rental: 'Alquiler',
  sale: 'Venta',
  acquisition: 'Adquisición',
  loss: 'Pérdida',
  abandonment: 'Abandono',
  retirement: 'Retirada',
  return: 'Devolución',
};
// A loan is always tied to an activity, and it's the activity page
// (where the participants and the trip context already are) that
// registers those now — see js/activity.js. return is the other side
// of a loan and *is* offered here: material can come back in pieces
// (30 mosquetones lent for a trip might return 10 one day, 20 the
// next), each its own call against the same item and activity.
const movementTypeOptions = Object.entries(typeLabels).filter(([type]) => type !== 'loan');

// The movements this form offers for an item: only what can be done with it right now, as the API
// would otherwise refuse the rest. A rental needs a rental price and a unit available; a sale, a sale
// price and a unit available; ropes and bags are a single unit, so nothing can be acquired of them; a
// loss, abandonment or retirement takes units out of what is available; and a return only makes sense
// for material that is out on loan (a rental is closed with its own "Devolver" button instead).
function movementTypeOptionsFor(item) {
  const available = item.available_quantity > 0;
  const allowed = {
    rental: item.rentable && available,
    sale: item.sellable && available,
    acquisition: item.item_type === 'gear',
    loss: available,
    abandonment: available,
    retirement: available,
    return: item.outstanding_loan_quantity > 0,
  };
  return movementTypeOptions.filter(([type]) => allowed[type]);
}
// Rental always needs a socio; loan, sale and return's are optional;
// acquisition, loss, abandonment and retirement never get one at all —
// the "Socio" field is hidden outright for those, not just left empty.
const recipientRequiredTypes = new Set(['rental']);
const recipientForbiddenTypes = new Set(['acquisition', 'loss', 'abandonment', 'retirement']);
const movementStatusLabels = {
  active: 'Activo',
  completed: 'Completado',
  overdue: 'Vencido',
  cancelled: 'Cancelado',
};

// Only rental is ever returned via PATCH .../return, and always all at
// once — a loan's own "returned" state is the sum of separate 'return'
// movements against the same item and activity instead (see
// movementTypeOptions above), not a flag closed on the loan itself.
function isReturnable(m) {
  return m.type === 'rental';
}

let allItems = [];
let itemsById = {};

// Populated from /users/directory (see the canManageMovements block
// below) so movement rows can show a person's name instead of their
// bare login, and so the shared #mv-user-options datalist has entries
// for every inline "add movement" form.
let directoryByLogin = {};
let userEntries = [];
let activityEntries = [];

function personLabel(login) {
  const person = directoryByLogin[login];
  return person ? `${person.first_name} ${person.last_name}` : login;
}

// Accent-insensitive substring match — "cañones" and "canones" should
// both find the same items, and there'll be enough material (300+
// expected) that a plain <select> stops being usable for finding one.
function itemSearchText(item) {
  return normalizeForSearch(
    [
      itemTypeLabels[item.item_type],
      item.brand,
      item.model,
      item.code,
      item.description,
      item.category_name,
      ropeTypeLabels[item.rope_type],
      bagTypeLabels[item.bag_type],
    ]
      .filter(Boolean)
      .join(' '),
  );
}

// Rope/bag show their item type ("Cuerda"/"Saca"); gear shows its
// category instead ("Mosquetones"...) — "Material" alone would be true
// of every gear row and say nothing useful for telling them apart.
function itemTypeColumn(item) {
  if (item.item_type === 'gear') {
    return escapeHtml(item.category_name ?? itemTypeLabels.gear);
  }
  return itemTypeLabels[item.item_type] ?? item.item_type;
}

function renderItemRow(item) {
  return `
    <tr>
      <td>${itemTypeColumn(item)}</td>
      <td>${itemLabel(item, { includeCategory: false })}</td>
      <td>${item.code ? escapeHtml(item.code) : '—'}</td>
      <td>${item.available_quantity} / ${item.total_quantity}</td>
      <td><span class="badge">${itemStatusLabels[item.status] ?? item.status}</span></td>
      <td>
        <div class="table-actions">
          <button type="button" class="btn btn-secondary btn-small" data-toggle-item-movements="${item.id}">Movimientos</button>
          ${canManageMovements && movementTypeOptionsFor(item).length ? `<button type="button" class="btn btn-secondary btn-small" data-add-movement="${item.id}">+ Movimiento</button>` : ''}
          ${canManageMovements ? `<button type="button" class="btn btn-secondary btn-small" data-edit-item="${item.id}">Editar</button>` : ''}
        </div>
      </td>
    </tr>
    <tr data-item-movements-row="${item.id}" hidden>
      <td colspan="6"><div class="detail-list" data-item-movements-panel="${item.id}"></div></td>
    </tr>
  `;
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

// Fetches and renders one item's movement history — read-only; adding
// one is #new-movement-dialog below, a separate modal rather than
// something embedded in this same expanded row. Called both when a
// row's "Movimientos" toggle opens it, and to refresh that same view
// after an action (add/return) elsewhere forces the whole table to
// reload.
async function expandItemMovements(itemId) {
  const row = document.querySelector(`[data-item-movements-row="${itemId}"]`);
  if (!row) return; // filtered out of the current search results
  row.hidden = false;
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

// #new-movement-dialog is one shared dialog, not one per item — there's
// no separate "nuevo movimiento" section with its own material picker
// either, since with 300+ items a second search-to-find-the-item step
// would just duplicate the one the items table itself already is;
// opening it for a given item just remembers which one (movementItemId)
// and shows its own label, rather than the dialog carrying an id of its
// own. Rope/bag skip the quantity field entirely (always exactly one
// unit — the server forces it anyway).
let movementItemId = null;

function updateMovementFormVisibility() {
  const type = document.getElementById('mv-type').value;
  // A rental can optionally be tied to an activity (a loan always is,
  // but loan is never an option in this form — see
  // movementTypeOptions). return is the other side of a loan and is
  // always required to name the same activity it's returning material
  // against — Repository.CreateMovement validates it against how much
  // of that item this activity still has outstanding. Every other type
  // has no such notion at all, so the field only shows for those two.
  const activityField = document.getElementById('mv-activity-field');
  const activityInput = document.getElementById('mv-activity');
  activityField.hidden = type !== 'rental' && type !== 'return';
  if (activityField.hidden) activityInput.value = '';
  activityInput.required = type === 'return';
  document.getElementById('mv-activity-hint').hidden = activityInput.required;

  // Acquisition/loss/abandonment/retirement never have a recipient —
  // the field is hidden outright (and cleared) rather than just left
  // optional, matching recipient_only_for_loan_rental_sale.
  const userField = document.getElementById('mv-user-field');
  const userInput = document.getElementById('mv-user');
  const forbidsRecipient = recipientForbiddenTypes.has(type);
  userField.hidden = forbidsRecipient;
  if (forbidsRecipient) userInput.value = '';
  const requiresRecipient = recipientRequiredTypes.has(type);
  userInput.required = requiresRecipient;
  document.getElementById('mv-user-hint').hidden = requiresRecipient || forbidsRecipient;
}

document.getElementById('mv-type').addEventListener('change', updateMovementFormVisibility);

function openNewMovementDialog(itemId) {
  const item = itemsById[itemId];
  if (!item) return;
  movementItemId = itemId;
  document.getElementById('new-movement-item-label').innerHTML = itemLabel(item);
  const isSingleUnit = item.item_type === 'rope' || item.item_type === 'bag';
  document.getElementById('mv-quantity-field').hidden = isSingleUnit;
  document.getElementById('mv-type').innerHTML = movementTypeOptionsFor(item)
    .map(([value, label]) => `<option value="${value}">${label}</option>`)
    .join('');
  updateMovementFormVisibility();
  openModal(document.getElementById('new-movement-dialog'));
}

function renderFilteredItems() {
  const query = normalizeForSearch(document.getElementById('item-search').value);
  const filtered = query
    ? allItems.filter((item) => itemSearchText(item).includes(query))
    : allItems;
  const tableBody = document.getElementById('items-table-body');
  tableBody.innerHTML = filtered.length
    ? filtered.map(renderItemRow).join('')
    : `<tr><td colspan="6">${allItems.length ? 'Ningún material coincide con la búsqueda.' : 'Todavía no hay material dado de alta.'}</td></tr>`;
}

document.getElementById('item-search').addEventListener('input', renderFilteredItems);

async function loadItems() {
  const tableBody = document.getElementById('items-table-body');
  try {
    allItems = await apiFetch('/items');
    itemsById = Object.fromEntries(allItems.map((item) => [item.id, item]));
    renderFilteredItems();
  } catch {
    tableBody.innerHTML = '<tr><td colspan="6">No se pudo cargar el material.</td></tr>';
  }
}

// Set below, for whoever can manage the material: opens the dialog of "Nuevo material" to edit an item.
let openEditItemDialog = null;

document.getElementById('items-table-body').addEventListener('click', async (event) => {
  const toggleButton = event.target.closest('button[data-toggle-item-movements]');
  if (toggleButton) {
    const itemId = Number(toggleButton.dataset.toggleItemMovements);
    const row = document.querySelector(`[data-item-movements-row="${itemId}"]`);
    const opening = row.hidden;
    row.hidden = !row.hidden;
    if (opening) await expandItemMovements(itemId);
    return;
  }

  const editItemButton = event.target.closest('button[data-edit-item]');
  if (editItemButton) {
    openEditItemDialog?.(Number(editItemButton.dataset.editItem));
    return;
  }

  const addMovementButton = event.target.closest('button[data-add-movement]');
  if (addMovementButton) {
    openNewMovementDialog(Number(addMovementButton.dataset.addMovement));
    return;
  }

  const returnButton = event.target.closest('button[data-return]');
  if (returnButton) {
    await handleReturn(returnButton);
  }
});

const newMovementDialog = document.getElementById('new-movement-dialog');
const newMovementForm = document.getElementById('new-movement-form');
initModal(newMovementDialog, newMovementForm);

newMovementForm.addEventListener('submit', async (event) => {
  event.preventDefault();

  const type = document.getElementById('mv-type').value;
  const userInput = document.getElementById('mv-user');
  const activityField = document.getElementById('mv-activity-field');
  const activityInput = document.getElementById('mv-activity');
  const quantityField = document.getElementById('mv-quantity-field');
  const quantityInput = document.getElementById('mv-quantity');
  const notesInput = document.getElementById('mv-notes');

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
  if (!activityField.hidden && activityInput.value.trim()) {
    activityId = resolvePickerId(activityEntries, activityInput.value);
    if (!activityId) {
      showError('Elige una actividad de la lista, o déjalo vacío.');
      return;
    }
  } else if (type === 'return') {
    showError(
      'Elige una actividad de la lista: una devolución siempre va asociada a la actividad para la que se prestó el material.',
    );
    return;
  }

  const body = {
    item_id: movementItemId,
    login: userLogin,
    type,
    quantity: quantityField.hidden ? 1 : Number(quantityInput.value) || 1,
    // No date field in this form — always today (transacted_at left
    // unset defaults to now() server-side).
    activity_id: activityId,
    notes: notesInput.value.trim() || null,
  };

  const submitButton = newMovementForm.querySelector('button[type="submit"]');
  submitButton.disabled = true;
  try {
    await apiFetch('/movements', { method: 'POST', body: JSON.stringify(body) });
    showFeedback('Movimiento registrado.');
    newMovementDialog.close();
    await loadItems();
    await expandItemMovements(movementItemId);
  } catch (err) {
    showError(err.message);
  } finally {
    submitButton.disabled = false;
  }
});

// Only ever reached for a rental — the sole type still returned this
// way, always all at once (see isReturnable).
async function handleReturn(button) {
  button.disabled = true;
  // If this return button lives inside an item's expanded movements
  // panel, remember which item so it can be reopened after the
  // reload below wipes the whole items table.
  const itemId = button.closest('[data-item-movements-panel]')?.dataset.itemMovementsPanel;
  try {
    await apiFetch(`/movements/${button.dataset.return}/return`, { method: 'PATCH' });
    showFeedback('Devolución registrada.');
    await loadItems();
    if (itemId) await expandItemMovements(Number(itemId));
  } catch (err) {
    button.disabled = false;
    showError(err.message);
  }
}

if (canManageMovements) {
  const newItemDialog = document.getElementById('new-item-dialog');
  const newItemForm = document.getElementById('new-item-form');
  const itemTypeSelect = document.getElementById('ni-item-type');
  const descriptionField = document.getElementById('ni-description');
  const categorySelect = document.getElementById('ni-category');
  // The dialog is shared: the id of the item it is editing, or null when it creates one.
  let editingItemId = null;

  // Only rope/bag/gear-specific controls toggle required — the fields
  // shared by every type (acquired_at, prices, loanable) stay as they
  // are regardless of item_type.
  const requiredWhenVisible = [
    'ni-brand',
    'ni-model',
    'ni-code',
    'ni-rope-type',
    'ni-diameter-mm',
    'ni-manufacture-year',
    'ni-length-m',
    'ni-bag-type',
    'ni-capacity-liters',
    'ni-category',
  ];

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
    // What only makes sense when creating (the quantity and the date of acquisition: the stock is
    // whatever the movements say) is out of an edit.
    document.querySelectorAll('[data-create-only]').forEach((el) => {
      if (editingItemId !== null) el.hidden = true;
      else if (!el.dataset.itemTypeField) el.hidden = false;
    });
    document.getElementById('ni-title').textContent =
      editingItemId !== null ? 'Editar material' : 'Nuevo material';
    document.getElementById('ni-submit').textContent =
      editingItemId !== null ? 'Guardar' : 'Crear material';
    itemTypeSelect.disabled = editingItemId !== null;
  }
  itemTypeSelect.addEventListener('change', updateItemTypeFieldsVisibility);
  updateItemTypeFieldsVisibility();

  apiFetch('/material-categories')
    .then((categories) => {
      categorySelect.innerHTML = categories
        .map((c) => `<option value="${c.id}">${escapeHtml(c.name)}</option>`)
        .join('');
    })
    .catch(() => showError('No se pudo cargar la lista de categorías.'));

  initModal(newItemDialog, newItemForm);
  // The dialog's own "close" (initModal already resets the form there,
  // regardless of how it closed) reverts item_type back to its default
  // option — recompute which fields that implies right after, same as
  // the very first render below.
  newItemDialog.addEventListener('close', () => {
    editingItemId = null;
    updateItemTypeFieldsVisibility();
  });

  openEditItemDialog = (itemId) => {
    const item = itemsById[itemId];
    if (!item) return;
    editingItemId = itemId;
    itemTypeSelect.value = item.item_type;
    updateItemTypeFieldsVisibility();
    const set = (name, value) => {
      newItemForm.elements[name].value = value ?? '';
    };
    set('brand', item.brand);
    set('model', item.model);
    set('code', item.code);
    set('rope_type', item.rope_type);
    set('diameter_mm', item.diameter_mm);
    set('manufacture_year', item.manufacture_year);
    set('length_m', item.length_m);
    set('bag_type', item.bag_type);
    set('capacity_liters', item.capacity_liters);
    set('category_id', item.category_id);
    set('rental_price_per_week', item.rental_price_per_week);
    set('sale_price', item.sale_price);
    set('description', item.description);
    newItemForm.elements.loanable.checked = item.loanable;
    openModal(newItemDialog);
  };

  document.getElementById('toggle-new-item').addEventListener('click', () => {
    openModal(newItemDialog);
  });

  newItemForm.addEventListener('submit', async (event) => {
    event.preventDefault();
    const formData = new FormData(newItemForm);
    const editing = editingItemId !== null ? itemsById[editingItemId] : null;
    // A disabled field is not in the FormData: when editing, the type is the item's own.
    const type = editing ? editing.item_type : formData.get('item_type');
    const optionalText = (key) => formData.get(key)?.toString().trim() || null;
    const optionalNumber = (key) => (formData.get(key) ? Number(formData.get(key)) : null);

    const body = {
      description: optionalText('description'),
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
    }
    if (editing) {
      // An edit replaces every field, and the form has no brand, model or code for gear: keep them.
      if (type === 'gear') {
        body.brand = editing.brand ?? null;
        body.model = editing.model ?? null;
        body.code = editing.code ?? null;
      }
    } else {
      body.item_type = type;
      body.acquired_at = optionalText('acquired_at');
      if (type === 'gear') body.total_quantity = Number(formData.get('total_quantity')) || 1;
    }

    const submitButton = newItemForm.querySelector('button[type="submit"]');
    submitButton.disabled = true;
    try {
      if (editing) {
        await apiFetch(`/items/${editing.id}`, { method: 'PATCH', body: JSON.stringify(body) });
      } else {
        await apiFetch('/items', { method: 'POST', body: JSON.stringify(body) });
      }
      showFeedback(editing ? 'Material actualizado.' : 'Material creado.');
      newItemDialog.close();
      await loadItems();
    } catch (err) {
      showError(err.message);
    } finally {
      submitButton.disabled = false;
    }
  });
}

// Started together with the socio/activity directories below, not before
// them: none depends on another, so they travel in the same round trip.
const itemsLoaded = loadItems();

if (canManageMovements) {
  // Both socios and activities pile up over time, so a plain <select>
  // stops being usable — these shared datalists back a text input in
  // every item's inline add-movement form instead, searchable as you
  // type. See pickerLabel/renderPickerOptions/resolvePickerId in api.js.
  const directoryLoaded = apiFetch('/users/directory')
    .then((people) => {
      directoryByLogin = Object.fromEntries(people.map((person) => [person.login, person]));
      userEntries = people
        .slice()
        .sort((a, b) =>
          `${a.first_name} ${a.last_name}`.localeCompare(`${b.first_name} ${b.last_name}`, 'es'),
        )
        .map((person) => ({
          id: person.login,
          label: pickerLabel(`${person.first_name} ${person.last_name}`, person.login),
        }));
      document.getElementById('mv-user-options').innerHTML = renderPickerOptions(userEntries);
    })
    .catch(() => showError('No se pudo cargar la lista de socios.'));

  apiFetch('/activities?active=true')
    .then((activities) => {
      activityEntries = activities.map((activity) => ({
        id: activity.id,
        label: pickerLabel(`${formatDate(activity.start_date)} — ${activity.title}`, activity.id),
      }));
      document.getElementById('mv-activity-options').innerHTML =
        renderPickerOptions(activityEntries);
    })
    .catch(() => showError('No se pudo cargar la lista de actividades.'));

  // Awaited so the "Socio" column has names to show on first paint
  // instead of falling back to the bare login until a re-render happens.
  await directoryLoaded;
}

await itemsLoaded;
