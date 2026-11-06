import {
  apiFetch,
  escapeHtml,
  formatDate,
  formatCurrency,
  hasAnyRole,
  showFeedback,
  showError,
} from './api.js';
import { initPage } from './layout.js';
import {
  accountingErrorText,
  accountTypeOptions,
  childrenByParent,
  flattenAccountTree,
  shownAccounts,
} from './accountTree.js';
import { initModal, openModal } from './modal.js';

// This whole page is restricted to admin, board and money, tighter than the API itself
// (GET /accounts is open to every baseline role — see docs/architecture.md's own accounts
// section): reading the club's finances is for the people who run it, and the nav only offers it
// to them. Of those, admin and money can also write here (POST/PATCH/move all accept "admin" or
// "money"); board only reads, so it gets none of the buttons that change something.
const viewer = await initPage({ requireAnyRole: ['admin', 'board', 'money'] });
const canWrite = hasAnyRole(viewer, ['admin', 'money']);
if (!canWrite) document.getElementById('toggle-new-account').remove();

let flattened = []; // [{ account, depth, isFirst, isLast }], one entry per account, in display order — see accountTree.js
let allAccounts = []; // every account, hidden ones included, exactly as GET /accounts answered
let showHidden = false; // whether the table (and the parent pickers) include hidden accounts — a view choice, never persisted
let accountsById = {}; // rebuilt alongside flattened — so a row's own "Editar" can look its account up without a fresh fetch
let editingAccountId = null; // which account #edit-account-dialog's fields currently belong to — set right before it opens, read back on save

// Every id descending from (not including) accountId — used to keep an
// edit's own "cuenta superior" picker from offering the account itself
// or one of its own descendants, the same circular_parent case
// UpdateAccount rejects server-side (see docs/architecture.md's own
// accounts section); checking client-side too just means a bad choice
// is obviously wrong before submitting instead of only after.
function descendantIds(accountId) {
  const map = childrenByParent(allAccounts);
  const ids = new Set();
  function walk(id) {
    for (const child of map.get(id) ?? []) {
      ids.add(child.id);
      walk(child.id);
    }
  }
  walk(accountId);
  return ids;
}

// selectedParentId/excludeId are both nullable: excludeId is only set
// while editing an existing account (nothing to exclude when creating
// a new one).
function parentSelectOptions(selectedParentId, excludeId) {
  const excluded = excludeId != null ? descendantIds(excludeId) : new Set();
  if (excludeId != null) excluded.add(excludeId);
  // A hidden account is only offered while hidden ones are shown, or when
  // it is this account's current parent — dropping that one would make
  // saving silently move the account to the top level.
  // One that has transactions can't have child accounts (an account either
  // groups others or has transactions), so it isn't offered either — unless it is
  // the current parent.
  // (A group's last_transaction_at is its descendants', so only a leaf counts.)
  const parents = new Set(allAccounts.map((a) => a.parent_id));
  const offered = shownAccounts(
    allAccounts,
    (account) =>
      (showHidden || !account.hidden || account.id === selectedParentId) &&
      (parents.has(account.id) ||
        account.last_transaction_at == null ||
        account.id === selectedParentId),
  );
  const options = flattenAccountTree(offered)
    .filter(({ account }) => !excluded.has(account.id))
    .map(({ account, depth }) => {
      const selected = account.id === selectedParentId ? 'selected' : '';
      return `<option value="${account.id}" ${selected}>${'— '.repeat(depth)}${escapeHtml(account.name)}</option>`;
    })
    .join('');
  const noneSelected = selectedParentId == null ? 'selected' : '';
  return `<option value="" ${noneSelected}>— Ninguna (cuenta de primer nivel) —</option>${options}`;
}

// Small tags next to an account's name for what it's marked for
// (allows_dues/allows_licenses, the default of distributions, the rentals and sales of material), so which accounts a transaction can
// record cuotas or licencias against is visible at a glance, not only
// by opening each one's edit dialog.
function accountTags(account) {
  const tags = [];
  if (account.allows_dues) tags.push('<span class="badge">Cuotas</span>');
  if (account.allows_licenses) tags.push('<span class="badge">Licencias</span>');
  if (account.distribution_default) tags.push('<span class="badge">Repartos</span>');
  if (account.material_rental) tags.push('<span class="badge">Alquiler de material</span>');
  if (account.material_sale) tags.push('<span class="badge">Venta de material</span>');
  if (account.hidden) tags.push('<span class="badge">Oculta</span>');
  return tags.length ? ` ${tags.join(' ')}` : '';
}

function renderAccountRow({ account, depth, isFirst, isLast }) {
  return `
    <tr${account.hidden ? ' class="account-hidden"' : ''}>
      <td style="padding-left: ${1.25 * depth + 0.75}rem"><a href="/cuenta?id=${account.id}">${escapeHtml(account.name)}</a>${accountTags(account)}</td>
      <td class="balance-cell">${formatCurrency(account.balance ?? 0)}</td>
      <td class="nowrap">${account.last_transaction_at ? formatDate(account.last_transaction_at) : '—'}</td>
      <td>
        ${
          canWrite
            ? `<div class="table-actions">
          ${isFirst ? '' : `<button type="button" class="btn btn-secondary btn-small" data-account-move-up="${account.id}">↑</button>`}
          ${isLast ? '' : `<button type="button" class="btn btn-secondary btn-small" data-account-move-down="${account.id}">↓</button>`}
          <button type="button" class="btn btn-secondary btn-small" data-edit-account="${account.id}">Editar</button>
        </div>`
            : ''
        }
      </td>
    </tr>
  `;
}

function renderAccountsTable() {
  const tableBody = document.getElementById('accounts-table-body');
  tableBody.innerHTML = flattened.length
    ? flattened.map(renderAccountRow).join('')
    : '<tr><td colspan="4">Todavía no hay ninguna cuenta creada.</td></tr>';
}

// Applies a fresh accounts list (from GET /accounts, or from the
// reordered list POST /accounts/{id}/move already answers with —
// either way the same shape) without a redundant extra round trip.
function applyAccountsList(list) {
  allAccounts = list;
  accountsById = Object.fromEntries(list.map((account) => [account.id, account]));
  renderView();
}

// Builds what's on screen from allAccounts and showHidden: hidden
// accounts are left out unless asked for (see shownAccounts for how
// their shown children stay in the tree). "First"/"last" for the move
// arrows follow the accounts' real sibling groups (same parent_id), not
// the display's, since that's what a move actually swaps within — but
// counting only the siblings that are shown, so an arrow never appears
// that would only trade places with a row nobody can see.
function renderView() {
  const isShown = (account) => showHidden || !account.hidden;
  const groups = childrenByParent(allAccounts.filter(isShown));
  flattened = flattenAccountTree(shownAccounts(allAccounts, isShown)).map((entry) => {
    const siblings = groups.get(accountsById[entry.account.id].parent_id ?? 'root');
    return {
      ...entry,
      isFirst: siblings[0].id === entry.account.id,
      isLast: siblings.at(-1).id === entry.account.id,
    };
  });
  renderAccountsTable();
  document.getElementById('na-parent').innerHTML = parentSelectOptions(null, null);

  const hiddenCount = allAccounts.filter((account) => account.hidden).length;
  document.getElementById('show-hidden-wrap').hidden = hiddenCount === 0;
  document.getElementById('show-hidden-label').textContent =
    `Mostrar cuentas ocultas (${hiddenCount})`;
}

document.getElementById('show-hidden-accounts').addEventListener('change', (event) => {
  showHidden = event.target.checked;
  renderView();
});

async function loadAccounts() {
  const tableBody = document.getElementById('accounts-table-body');
  try {
    applyAccountsList(await apiFetch('/accounts'));
  } catch {
    tableBody.innerHTML = '<tr><td colspan="4">No se pudo cargar el plan de cuentas.</td></tr>';
  }
}

// A move swaps with the very next sibling, hidden or not — so with
// hidden accounts out of view it has to be repeated until it has passed
// them all, or the click would seem to do nothing.
async function moveAccount(id, direction) {
  try {
    const parentId = accountsById[id].parent_id ?? null;
    const siblings = allAccounts.filter((account) => (account.parent_id ?? null) === parentId);
    const step = direction === 'up' ? -1 : 1;
    let index = siblings.findIndex((account) => account.id === id) + step;
    let moves = 1;
    while (!showHidden && siblings[index]?.hidden) {
      moves += 1;
      index += step;
    }
    let list;
    for (let i = 0; i < moves; i += 1) {
      list = await apiFetch(`/accounts/${id}/move`, {
        method: 'POST',
        body: JSON.stringify({ direction }),
      });
    }
    applyAccountsList(list);
  } catch (err) {
    showError(accountingErrorText(err));
  }
}

// #edit-account-dialog is one shared dialog, not one per row — opening
// it for a given account just fills in its own current values first
// (editingAccountId tracks which one a save is actually for, since the
// dialog's own fields carry no id of their own).
const editAccountDialog = document.getElementById('edit-account-dialog');
const editAccountForm = document.getElementById('edit-account-form');
initModal(editAccountDialog, editAccountForm);

function openEditAccountDialog(account) {
  editingAccountId = account.id;
  document.getElementById('ea-name').value = account.name;
  document.getElementById('ea-code').value = account.code ?? '';
  document.getElementById('ea-parent').innerHTML = parentSelectOptions(
    account.parent_id ?? null,
    account.id,
  );
  document.getElementById('ea-type').innerHTML = accountTypeOptions(account.type ?? null);
  document.getElementById('ea-allows-dues').checked = Boolean(account.allows_dues);
  document.getElementById('ea-allows-licenses').checked = Boolean(account.allows_licenses);
  document.getElementById('ea-distribution-default').checked = Boolean(
    account.distribution_default,
  );
  document.getElementById('ea-material-rental').checked = Boolean(account.material_rental);
  document.getElementById('ea-material-sale').checked = Boolean(account.material_sale);
  document.getElementById('ea-hidden').checked = Boolean(account.hidden);
  openModal(editAccountDialog);
}

editAccountForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  const parentValue = document.getElementById('ea-parent').value;
  const body = {
    name: document.getElementById('ea-name').value.trim(),
    // Optional — a blank input clears an existing code, same as an
    // account that never had one (Handler.normalizeCode does the same
    // normalization server-side too, so this isn't the only guard).
    code: document.getElementById('ea-code').value.trim() || null,
    parent_id: parentValue ? Number(parentValue) : null,
    // None when "Sin tipo" is chosen: PATCH replaces it like every other field.
    type: document.getElementById('ea-type').value || null,
    // PATCH replaces every field, these two included — a box left
    // unchecked is sent as false, not omitted.
    allows_dues: document.getElementById('ea-allows-dues').checked,
    allows_licenses: document.getElementById('ea-allows-licenses').checked,
    distribution_default: document.getElementById('ea-distribution-default').checked,
    material_rental: document.getElementById('ea-material-rental').checked,
    material_sale: document.getElementById('ea-material-sale').checked,
    hidden: document.getElementById('ea-hidden').checked,
  };

  const submitButton = editAccountForm.querySelector('button[type="submit"]');
  submitButton.disabled = true;
  try {
    await apiFetch(`/accounts/${editingAccountId}`, {
      method: 'PUT',
      body: JSON.stringify(body),
    });
    showFeedback('Cuenta actualizada.');
    editAccountDialog.close();
    await loadAccounts();
  } catch (err) {
    showError(accountingErrorText(err));
  } finally {
    submitButton.disabled = false;
  }
});

document.getElementById('accounts-table-body').addEventListener('click', async (event) => {
  const editButton = event.target.closest('button[data-edit-account]');
  if (editButton) {
    openEditAccountDialog(accountsById[Number(editButton.dataset.editAccount)]);
    return;
  }

  const moveUpButton = event.target.closest('button[data-account-move-up]');
  if (moveUpButton) {
    await moveAccount(Number(moveUpButton.dataset.accountMoveUp), 'up');
    return;
  }

  const moveDownButton = event.target.closest('button[data-account-move-down]');
  if (moveDownButton) {
    await moveAccount(Number(moveDownButton.dataset.accountMoveDown), 'down');
  }
});

const newAccountDialog = document.getElementById('new-account-dialog');
const newAccountForm = document.getElementById('new-account-form');
initModal(newAccountDialog, newAccountForm);
document.getElementById('na-type').innerHTML = accountTypeOptions();

document.getElementById('toggle-new-account')?.addEventListener('click', () => {
  openModal(newAccountDialog);
});

newAccountForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  const formData = new FormData(newAccountForm);
  const parentValue = formData.get('parent_id');
  const body = {
    name: formData.get('name').toString().trim(),
    code: formData.get('code').toString().trim() || null,
    parent_id: parentValue ? Number(parentValue) : null,
    type: document.getElementById('na-type').value || null,
    allows_dues: document.getElementById('na-allows-dues').checked,
    allows_licenses: document.getElementById('na-allows-licenses').checked,
    distribution_default: document.getElementById('na-distribution-default').checked,
    material_rental: document.getElementById('na-material-rental').checked,
    material_sale: document.getElementById('na-material-sale').checked,
    hidden: document.getElementById('na-hidden').checked,
  };

  const submitButton = newAccountForm.querySelector('button[type="submit"]');
  submitButton.disabled = true;
  try {
    await apiFetch('/accounts', { method: 'POST', body: JSON.stringify(body) });
    showFeedback('Cuenta creada.');
    newAccountDialog.close();
    await loadAccounts();
  } catch (err) {
    showError(accountingErrorText(err));
  } finally {
    submitButton.disabled = false;
  }
});

await loadAccounts();
