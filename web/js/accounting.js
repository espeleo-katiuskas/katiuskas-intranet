import {
  apiFetch,
  escapeHtml,
  formatDate,
  formatCurrency,
  showFeedback,
  showError,
} from './api.js';
import { initPage } from './layout.js';
import { childrenByParent, flattenAccountTree } from './accountTree.js';
import { initModal } from './modal.js';

// This whole page is restricted to admin/money, tighter than the API
// itself (GET /accounts is open to every baseline role — see
// CLAUDE.md's own Accounting section) — reading the club's finances is
// fine for any socio, but managing the chart of accounts itself isn't
// something to even show as a destination in the nav for anyone else.
// Both roles can also write here (POST/PATCH/move all accept "admin"
// or "money"), so nothing on this page needs a narrower check than
// this one gate.
await initPage({ requireAnyRole: ['admin', 'money'] });

let flattened = []; // [{ account, depth, isFirst, isLast }], one entry per account, in display order — see accountTree.js
let accountsById = {}; // rebuilt alongside flattened — so a row's own "Editar" can look its account up without a fresh fetch
let editingAccountId = null; // which account #edit-account-dialog's fields currently belong to — set right before it opens, read back on save

// Every id descending from (not including) accountId — used to keep an
// edit's own "cuenta superior" picker from offering the account itself
// or one of its own descendants, the same circular_parent case
// UpdateAccount rejects server-side (see CLAUDE.md's Accounting
// section); checking client-side too just means a bad choice is
// obviously wrong before submitting instead of only after.
function descendantIds(accountId) {
  const map = childrenByParent(flattened.map((entry) => entry.account));
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
  const options = flattened
    .filter(({ account }) => !excluded.has(account.id))
    .map(({ account, depth }) => {
      const selected = account.id === selectedParentId ? 'selected' : '';
      const codeSuffix = account.code ? ` (${escapeHtml(account.code)})` : '';
      return `<option value="${account.id}" ${selected}>${'— '.repeat(depth)}${escapeHtml(account.name)}${codeSuffix}</option>`;
    })
    .join('');
  const noneSelected = selectedParentId == null ? 'selected' : '';
  return `<option value="" ${noneSelected}>— Ninguna (cuenta de primer nivel) —</option>${options}`;
}

function renderAccountRow({ account, depth, isFirst, isLast }) {
  return `
    <tr>
      <td style="padding-left: ${1.25 * depth + 0.75}rem"><a href="/account?id=${account.id}">${escapeHtml(account.name)}</a></td>
      <td>${account.code ? escapeHtml(account.code) : '—'}</td>
      <td class="balance-cell">${formatCurrency(account.balance ?? 0)}</td>
      <td>${account.last_transaction_at ? formatDate(account.last_transaction_at) : '—'}</td>
      <td>
        <div class="table-actions">
          ${isFirst ? '' : `<button type="button" class="btn btn-secondary btn-small" data-account-move-up="${account.id}">↑</button>`}
          ${isLast ? '' : `<button type="button" class="btn btn-secondary btn-small" data-account-move-down="${account.id}">↓</button>`}
          <button type="button" class="btn btn-secondary btn-small" data-edit-account="${account.id}">Editar</button>
        </div>
      </td>
    </tr>
  `;
}

function renderAccountsTable() {
  const tableBody = document.getElementById('accounts-table-body');
  tableBody.innerHTML = flattened.length
    ? flattened.map(renderAccountRow).join('')
    : '<tr><td colspan="5">Todavía no hay ninguna cuenta creada.</td></tr>';
}

// Applies a fresh accounts list (from GET /accounts, or from the
// reordered list POST /accounts/{id}/move already answers with —
// either way the same shape) without a redundant extra round trip.
function applyAccountsList(list) {
  flattened = flattenAccountTree(list);
  accountsById = Object.fromEntries(list.map((account) => [account.id, account]));
  renderAccountsTable();
  document.getElementById('na-parent').innerHTML = parentSelectOptions(null, null);
}

async function loadAccounts() {
  const tableBody = document.getElementById('accounts-table-body');
  try {
    applyAccountsList(await apiFetch('/accounts'));
  } catch {
    tableBody.innerHTML = '<tr><td colspan="5">No se pudo cargar el plan de cuentas.</td></tr>';
  }
}

async function moveAccount(id, direction) {
  try {
    applyAccountsList(await apiFetch(`/accounts/${id}/move`, { method: 'POST', body: JSON.stringify({ direction }) }));
  } catch (err) {
    showError(err.message);
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
  document.getElementById('ea-parent').innerHTML = parentSelectOptions(account.parent_id ?? null, account.id);
  editAccountDialog.showModal();
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
  };

  const submitButton = editAccountForm.querySelector('button[type="submit"]');
  submitButton.disabled = true;
  try {
    await apiFetch(`/accounts/${editingAccountId}`, { method: 'PATCH', body: JSON.stringify(body) });
    showFeedback('Cuenta actualizada.');
    editAccountDialog.close();
    await loadAccounts();
  } catch (err) {
    showError(err.message);
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

document.getElementById('toggle-new-account').addEventListener('click', () => {
  newAccountDialog.showModal();
});

newAccountForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  const formData = new FormData(newAccountForm);
  const parentValue = formData.get('parent_id');
  const body = {
    name: formData.get('name').toString().trim(),
    code: formData.get('code').toString().trim() || null,
    parent_id: parentValue ? Number(parentValue) : null,
  };

  const submitButton = newAccountForm.querySelector('button[type="submit"]');
  submitButton.disabled = true;
  try {
    await apiFetch('/accounts', { method: 'POST', body: JSON.stringify(body) });
    showFeedback('Cuenta creada.');
    newAccountDialog.close();
    await loadAccounts();
  } catch (err) {
    showError(err.message);
  } finally {
    submitButton.disabled = false;
  }
});

await loadAccounts();
