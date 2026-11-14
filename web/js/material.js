import {
  hasAnyRole,
  apiFetch,
  escapeHtml,
  formatCurrency,
  formatDate,
  normalizeForSearch,
  personPickerLabeler,
  renderPickerOptions,
  resolvePickerId,
  showFeedback,
  showError,
} from './api.js';
import { initPage } from './layout.js';
import { initModal, openModal } from './modal.js';
import { itemLabel, itemPickerText } from './material-common.js';

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
    acquisition: item.kind === 'general',
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
// expected) that a plain <select> stops being usable for finding one. It looks in what the table
// shows (the type and the "Características" cell, code included), so what is seen can be found.
function itemSearchText(item) {
  return normalizeForSearch(itemPickerText(item));
}

// A word of the search like ">30", ">30m", "<20" or "<20m" does not look for text: it keeps the ropes longer
// (">") or shorter ("<") than that many metres, and drops everything that is not a rope. The rest of the
// search is looked for as text, as always. Both are strict: ">30" leaves out a rope of exactly 30 m.
const lengthWord = /^([<>])(\d+)m?$/i;

function parseSearch(raw) {
  const lengths = [];
  const words = [];
  for (const word of raw.trim().split(/\s+/).filter(Boolean)) {
    const match = lengthWord.exec(word);
    if (match) lengths.push({ longer: match[1] === '>', metres: Number(match[2]) });
    else words.push(word);
  }
  return { text: normalizeForSearch(words.join(' ')), lengths };
}

function matchesSearch(item, { text, lengths }) {
  const fits = ({ longer, metres }) => {
    if (item.kind !== 'rope') return false;
    return longer ? item.length_m > metres : item.length_m < metres;
  };
  return lengths.every(fits) && (!text || itemSearchText(item).includes(text));
}

function renderItemRow(item) {
  return `
    <tr>
      <td>${escapeHtml(item.type_name)}</td>
      <td>${itemLabel(item, { includeType: false })}${item.incomplete && canManageMovements ? ' <span class="badge" title="Le falta algo que su tipo exige: edítalo para completarlo">Incompleto</span>' : ''}</td>
      <td>${item.available_quantity} / ${item.total_quantity}</td>
      <td><span class="badge">${itemStatusLabels[item.status] ?? item.status}</span></td>
      <td>
        <div class="table-actions">
          <button type="button" class="btn btn-secondary btn-small" data-toggle-item-movements="${item.id}">Movimientos</button>
          ${canManageMovements && movementTypeOptionsFor(item).length ? `<button type="button" class="btn btn-secondary btn-small" data-add-movement="${item.id}">+ Movimiento</button>` : ''}
          ${canManageMovements ? `<button type="button" class="btn btn-secondary btn-small" data-edit-item="${item.id}">Editar</button>` : ''}
          ${canManageMovements ? `<button type="button" class="btn btn-secondary btn-small" data-duplicate-item="${item.id}">Duplicar</button>` : ''}
        </div>
      </td>
    </tr>
    <tr data-item-movements-row="${item.id}" hidden>
      <td colspan="5"><div class="detail-list" data-item-movements-panel="${item.id}"></div></td>
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
    const movements = await apiFetch(`/material/items/${itemId}/movements`);
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

// A sale or a rental that costs something is recorded in the accounting: the dialog asks for the
// asset account (caja, banco…) the money goes into, read once and remembered between uses.
const LAST_ACCOUNT_KEY = 'katiuskas:material-account';
let assetAccounts = null;

async function loadAssetAccounts() {
  if (assetAccounts) return;
  const all = await apiFetch('/accounts');
  const groups = new Set(all.map((a) => a.parent_id));
  assetAccounts = all.filter((a) => a.effective_type === 'asset' && !a.hidden && !groups.has(a.id));
}

// What the movement being filled in costs: the item's price of its type, times the quantity (a rental
// charges its first week).
function movementTotal() {
  const item = itemsById[movementItemId];
  const type = document.getElementById('mv-type').value;
  if (!item) return 0;
  const price =
    type === 'sale' ? item.sale_price : type === 'rental' ? item.rental_price_per_week : null;
  const quantity = document.getElementById('mv-quantity-field').hidden
    ? 1
    : Number(document.getElementById('mv-quantity').value) || 1;
  return (price ?? 0) * quantity;
}

// The account field is there only for what costs something.
function updateMovementAccountField() {
  const total = movementTotal();
  const field = document.getElementById('mv-account-field');
  const select = document.getElementById('mv-account');
  field.hidden = total <= 0;
  select.required = !field.hidden;
  document.getElementById('mv-total').textContent = field.hidden
    ? ''
    : `(${formatCurrency(total)})`;
}

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
  refreshUserOptions();
  updateMovementAccountField();
}

// Who can be picked as the "Socio" depends on the type: a loan or a rental (and the return of a loan)
// is for a member; a sale, for a member or a former member. What was typed before and no longer fits
// is cleared.
function userEntriesFor(type) {
  return userEntries.filter((e) => e.member || (type === 'sale' && e.formerMember));
}

function refreshUserOptions() {
  const userInput = document.getElementById('mv-user');
  const type = document.getElementById('mv-type').value;
  const entries = userEntriesFor(type);
  document.getElementById('mv-user-options').innerHTML = renderPickerOptions(entries);
  if (userInput.value.trim() && resolvePickerId(entries, userInput.value) == null) {
    userInput.value = '';
  }
}

// The dialogs only exist for whoever manages the material (the others get them removed by data-roles).
if (canManageMovements) {
  document.getElementById('mv-type').addEventListener('change', updateMovementFormVisibility);
  document.getElementById('mv-quantity').addEventListener('input', updateMovementAccountField);
}

async function openNewMovementDialog(itemId) {
  const item = itemsById[itemId];
  if (!item) return;
  movementItemId = itemId;
  try {
    await loadAssetAccounts();
  } catch {
    showError('No se pudo cargar la lista de cuentas.');
    return;
  }
  const remembered = (() => {
    try {
      return localStorage.getItem(LAST_ACCOUNT_KEY);
    } catch {
      return null;
    }
  })();
  document.getElementById('mv-account').innerHTML = assetAccounts
    .map(
      (a) =>
        `<option value="${a.id}"${String(a.id) === remembered ? ' selected' : ''}>${escapeHtml(a.name)}</option>`,
    )
    .join('');
  document.getElementById('new-movement-item-label').innerHTML = itemLabel(item);
  const isSingleUnit = item.kind === 'rope' || item.kind === 'bag';
  document.getElementById('mv-quantity-field').hidden = isSingleUnit;
  document.getElementById('mv-type').innerHTML = movementTypeOptionsFor(item)
    .map(([value, label]) => `<option value="${value}">${label}</option>`)
    .join('');
  updateMovementFormVisibility();
  openModal(document.getElementById('new-movement-dialog'));
}

// Material with no unit left (shown as "0 / 0": sold, lost, abandoned or retired) is no longer the
// club's and will not come back unless more of it is acquired, so the table hides it unless asked
// to show all. A partial loss or sale leaves units, and the item stays.
const isGone = (item) => item.total_quantity === 0;

function renderFilteredItems() {
  const search = parseSearch(document.getElementById('item-search').value);
  const showAll = document.getElementById('show-all-items').checked;
  const visible = showAll ? allItems : allItems.filter((item) => !isGone(item));
  const filtered =
    search.text || search.lengths.length
      ? visible.filter((item) => matchesSearch(item, search))
      : visible;
  const tableBody = document.getElementById('items-table-body');
  tableBody.innerHTML = filtered.length
    ? filtered.map(renderItemRow).join('')
    : `<tr><td colspan="5">${visible.length ? 'Ningún material coincide con la búsqueda.' : 'Todavía no hay material dado de alta.'}</td></tr>`;
}

document.getElementById('item-search').addEventListener('input', renderFilteredItems);
document.getElementById('show-all-items').addEventListener('change', renderFilteredItems);

async function loadItems() {
  const tableBody = document.getElementById('items-table-body');
  try {
    allItems = await apiFetch('/material/items');
    itemsById = Object.fromEntries(allItems.map((item) => [item.id, item]));
    renderFilteredItems();
  } catch {
    tableBody.innerHTML = '<tr><td colspan="5">No se pudo cargar el material.</td></tr>';
  }
}

// Set below, for whoever can manage the material: opens the dialog of "Nuevo material" to edit an item.
let openEditItemDialog = null;
// The same dialog, to create a new item that starts as a copy of one that exists.
let openDuplicateItemDialog = null;

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

  const duplicateItemButton = event.target.closest('button[data-duplicate-item]');
  if (duplicateItemButton) {
    openDuplicateItemDialog?.(Number(duplicateItemButton.dataset.duplicateItem));
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
if (canManageMovements) initModal(newMovementDialog, newMovementForm);

newMovementForm?.addEventListener('submit', async (event) => {
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
    userLogin = resolvePickerId(userEntriesFor(type), userInput.value);
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
  const accountSelect = document.getElementById('mv-account');
  if (!document.getElementById('mv-account-field').hidden) {
    body.account_id = Number(accountSelect.value);
    try {
      localStorage.setItem(LAST_ACCOUNT_KEY, accountSelect.value);
    } catch {
      // Remembering the account is a convenience only.
    }
  }

  const submitButton = newMovementForm.querySelector('button[type="submit"]');
  submitButton.disabled = true;
  try {
    await apiFetch('/material/movements', { method: 'POST', body: JSON.stringify(body) });
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
    await apiFetch(`/material/movements/${button.dataset.return}/return`, { method: 'POST' });
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
  const typeSelect = document.getElementById('ni-type');
  const descriptionField = document.getElementById('ni-description');
  // The dialog is shared: the id of the item it is editing, or null when it creates one.
  let editingItemId = null;
  // Or the id of the item whose data it starts with, when it creates a copy of it.
  let duplicatingItemId = null;
  // The catalog of types, with the attributes of each (what the form of a general item asks for).
  let materialTypes = [];

  // Only the controls specific to a kind (rope, bag, general) toggle required — the fields
  // shared by every kind (acquired_at, prices, loanable) stay as they are.
  const requiredWhenVisible = [
    'ni-rope-type',
    'ni-diameter-mm',
    'ni-manufacture-year',
    'ni-length-m',
    'ni-bag-type',
    'ni-capacity-liters',
  ];

  function syncModelWithBrand() {
    const brand = document.getElementById('ni-brand');
    const model = document.getElementById('ni-model');
    model.disabled = brand.value.trim() === '';
    if (model.disabled) model.value = '';
  }
  document.getElementById('ni-brand').addEventListener('input', syncModelWithBrand);

  // The kind (rope, bag or general) of the chosen type: Cuerda and Saca are the fixed types of the
  // rope and bag kinds, and every other type is general.
  function selectedKind() {
    return typeSelect.selectedOptions[0]?.dataset.kind ?? 'general';
  }

  function selectedType() {
    return materialTypes.find((t) => String(t.id) === typeSelect.value);
  }

  // What depends on the chosen type besides its kind: whether the description is required, and one
  // field per attribute of the type (a text box, or a list of its options), before the description.
  // Called whenever the type changes, so the values typed for another type's attributes are dropped.
  function syncTypeFields() {
    const type = selectedType();
    const kind = selectedKind();
    const descriptionRequired = kind === 'general' && (type?.description_required ?? true);
    descriptionField.required = descriptionRequired;
    document.getElementById('ni-description-optional').hidden = descriptionRequired;
    newItemForm.querySelectorAll('[data-attribute-field]').forEach((el) => {
      el.remove();
    });
    const fields = (type?.attributes ?? []).map((attribute) => {
      const field = document.createElement('div');
      field.className = 'field';
      field.dataset.attributeField = attribute.id;
      const id = `ni-attribute-${attribute.id}`;
      const optional = attribute.required ? '' : ' <span class="optional">(opcional)</span>';
      const control =
        attribute.data_type === 'enum'
          ? `<select id="${id}" name="attribute_${attribute.id}" ${attribute.required ? 'required' : ''}>
              <option value="">${attribute.required ? 'Elige…' : '—'}</option>
              ${attribute.options
                .map((o) => `<option value="${o.id}">${escapeHtml(o.label)}</option>`)
                .join('')}
            </select>`
          : `<input id="${id}" name="attribute_${attribute.id}" type="text" ${attribute.required ? 'required' : ''}>`;
      field.innerHTML = `<label for="${id}">${escapeHtml(attribute.name)}${optional}</label>${control}`;
      return field;
    });
    descriptionField.closest('.field').before(...fields);
  }

  function updateKindFieldsVisibility() {
    const kind = selectedKind();
    document.querySelectorAll('[data-kind-field]').forEach((el) => {
      el.hidden = !el.dataset.kindField.split(',').includes(kind);
    });
    for (const id of requiredWhenVisible) {
      const input = document.getElementById(id);
      input.required = !input.closest('[data-kind-field]').hidden;
    }
    // description is the "Descripción" of general material (required or not, depending on its type:
    // see syncTypeFields) and the optional "Observaciones" of a rope or a bag — see the two toggled
    // label spans above the textarea.
    // Brand, model and code are in every type's form, but only a rope or a bag must have them. A model
    // is the model of a brand: it can only be written once there is a brand.
    for (const id of ['ni-brand', 'ni-model', 'ni-code']) {
      document.getElementById(id).required = kind === 'rope' || kind === 'bag';
    }
    syncModelWithBrand();
    // What only makes sense when creating (the quantity and the date of acquisition: the stock is
    // whatever the movements say) is out of an edit.
    document.querySelectorAll('[data-create-only]').forEach((el) => {
      if (editingItemId !== null) el.hidden = true;
      else if (!el.dataset.kindField) el.hidden = false;
    });
    let title = 'Nuevo material';
    if (editingItemId !== null) title = 'Editar material';
    else if (duplicatingItemId !== null) title = 'Duplicar material';
    document.getElementById('ni-title').textContent = title;
    document.getElementById('ni-duplicate-note').hidden = duplicatingItemId === null;
    document.getElementById('ni-submit').textContent =
      editingItemId !== null ? 'Guardar' : 'Crear material';
    // Editing: a rope or a bag keeps its type, and general material may change it, but only to another
    // general one (so Cuerda and Saca are not offered).
    const editingKind = editingItemId !== null ? itemsById[editingItemId]?.kind : null;
    typeSelect.disabled = editingKind != null && editingKind !== 'general';
    for (const option of typeSelect.options) {
      option.disabled = option.hidden =
        editingKind === 'general' && option.dataset.kind !== 'general';
    }
    syncTypeFields();
  }
  typeSelect.addEventListener('change', updateKindFieldsVisibility);
  updateKindFieldsVisibility();

  // The types come in the order an admin has set (Cuerda and Saca first until moved); each option
  // remembers its kind.
  apiFetch('/material/types')
    .then((types) => {
      materialTypes = types;
      typeSelect.innerHTML = types
        .map((t) => `<option value="${t.id}" data-kind="${t.kind}">${escapeHtml(t.name)}</option>`)
        .join('');
      updateKindFieldsVisibility();
    })
    .catch(() => showError('No se pudo cargar la lista de tipos de material.'));

  initModal(newItemDialog, newItemForm);
  // The dialog's own "close" (initModal already resets the form there,
  // regardless of how it closed) reverts the type back to its first
  // option — recompute which fields that implies right after, same as
  // the very first render below.
  newItemDialog.addEventListener('close', () => {
    editingItemId = null;
    duplicatingItemId = null;
    updateKindFieldsVisibility();
  });

  // Puts an item's data in the form, for the type already chosen: all of it but the code, and the
  // quantity and date of acquisition, which are not the item's data.
  function fillItemForm(item, { withCode }) {
    const set = (name, value) => {
      newItemForm.elements[name].value = value ?? '';
    };
    set('brand', item.brand);
    set('model', item.model);
    if (withCode) set('code', item.code);
    syncModelWithBrand();
    set('color', item.color);
    set('rope_type', item.rope_type);
    set('diameter_mm', item.diameter_mm);
    set('manufacture_year', item.manufacture_year);
    set('length_m', item.length_m);
    set('bag_type', item.bag_type);
    set('capacity_liters', item.capacity_liters);
    set('rental_price_per_week', item.rental_price_per_week);
    set('sale_price', item.sale_price);
    set('description', item.description);
    for (const value of item.attributes ?? []) {
      set(`attribute_${value.attribute_id}`, value.option_id ?? value.value);
    }
    newItemForm.elements.loanable.checked = item.loanable;
  }

  openEditItemDialog = (itemId) => {
    const item = itemsById[itemId];
    if (!item) return;
    editingItemId = itemId;
    typeSelect.value = item.type_id;
    updateKindFieldsVisibility();
    fillItemForm(item, { withCode: true });
    openModal(newItemDialog);
  };

  // A new item that starts as a copy: everything is filled in and can be changed before saving. The
  // code is not copied — it is unique among all the material (and a rope or a bag must have one) — so
  // it is left blank, and for a rope or a bag the cursor goes there.
  openDuplicateItemDialog = (itemId) => {
    const item = itemsById[itemId];
    if (!item) return;
    duplicatingItemId = itemId;
    typeSelect.value = item.type_id;
    updateKindFieldsVisibility();
    fillItemForm(item, { withCode: false });
    const needsCode = item.kind === 'rope' || item.kind === 'bag';
    document.getElementById('ni-duplicate-note').textContent =
      `Empieza como copia de «${itemPickerText(item)}»: cambia lo que haga falta antes de crearlo.` +
      (needsCode ? ' Escribe su código, que no puede repetirse.' : '');
    openModal(newItemDialog);
    if (needsCode) document.getElementById('ni-code').focus();
  };

  document.getElementById('toggle-new-item').addEventListener('click', () => {
    openModal(newItemDialog);
  });

  newItemForm.addEventListener('submit', async (event) => {
    event.preventDefault();
    const formData = new FormData(newItemForm);
    const editing = editingItemId !== null ? itemsById[editingItemId] : null;
    // A disabled field is not in the FormData: when editing, the kind is the item's own.
    const kind = editing ? editing.kind : selectedKind();
    const optionalText = (key) => formData.get(key)?.toString().trim() || null;
    const optionalNumber = (key) => (formData.get(key) ? Number(formData.get(key)) : null);

    const body = {
      description: optionalText('description'),
      rental_price_per_week: optionalNumber('rental_price_per_week'),
      sale_price: optionalNumber('sale_price'),
      loanable: formData.get('loanable') === 'on',
    };
    body.brand = optionalText('brand');
    body.model = optionalText('model');
    body.code = optionalText('code');
    if (kind === 'rope' || kind === 'bag') body.color = optionalText('color');
    if (kind === 'rope') {
      body.rope_type = formData.get('rope_type');
      body.diameter_mm = Number(formData.get('diameter_mm'));
      body.manufacture_year = Number(formData.get('manufacture_year'));
      body.length_m = Number(formData.get('length_m'));
    } else if (kind === 'bag') {
      body.bag_type = formData.get('bag_type');
      body.capacity_liters = Number(formData.get('capacity_liters'));
    }
    // The attributes of the type: only those with a value are sent.
    body.attributes = [];
    for (const attribute of selectedType()?.attributes ?? []) {
      const value = optionalText(`attribute_${attribute.id}`);
      if (value === null) continue;
      body.attributes.push(
        attribute.data_type === 'enum'
          ? { attribute_id: attribute.id, option_id: Number(value) }
          : { attribute_id: attribute.id, value },
      );
    }
    // A disabled select is not in the FormData: a rope or a bag keeps its type.
    if (formData.get('type_id')) body.type_id = Number(formData.get('type_id'));
    if (!editing) {
      body.acquired_at = optionalText('acquired_at');
      if (kind === 'general') body.total_quantity = Number(formData.get('total_quantity')) || 1;
    }

    const submitButton = newItemForm.querySelector('button[type="submit"]');
    submitButton.disabled = true;
    try {
      if (editing) {
        await apiFetch(`/material/items/${editing.id}`, {
          method: 'PUT',
          body: JSON.stringify(body),
        });
      } else {
        await apiFetch('/material/items', { method: 'POST', body: JSON.stringify(body) });
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
  // type. See personPickerLabeler/renderPickerOptions/resolvePickerId in api.js.
  const directoryLoaded = apiFetch('/users/directory')
    .then((people) => {
      directoryByLogin = Object.fromEntries(people.map((person) => [person.login, person]));
      const labelOf = personPickerLabeler(people);
      userEntries = people
        .slice()
        .sort((a, b) =>
          `${a.first_name} ${a.last_name}`.localeCompare(`${b.first_name} ${b.last_name}`, 'es'),
        )
        .map((person) => ({
          id: person.login,
          label: labelOf(person),
          member: person.member,
          formerMember: person.former_member,
        }));
      refreshUserOptions();
    })
    .catch(() => showError('No se pudo cargar la lista de socios.'));

  apiFetch('/activities?active=true')
    .then((activities) => {
      activityEntries = activities.map((activity) => ({
        id: activity.id,
        label: `${formatDate(activity.start_date)} — ${activity.title}`,
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
