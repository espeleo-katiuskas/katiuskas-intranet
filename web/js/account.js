import {
  apiFetch,
  escapeHtml,
  formatDate,
  formatCurrency,
  showFeedback,
  showError,
} from './api.js';
import { initPage } from './layout.js';
import { flattenAccountTree, accountLabel } from './accountTree.js';
import { initModal } from './modal.js';

// Reached only from /accounting's own account tree — same admin/money
// gate as that page (see accounting.js's own doc comment for why this
// is tighter than the API itself, which opens reading to every
// baseline role).
await initPage({ requireAnyRole: ['admin', 'money'] });

const accountId = Number(new URLSearchParams(window.location.search).get('id'));
if (!accountId) {
  window.location.href = '/accounting';
}

let allAccounts = []; // from the last GET /accounts — reused by every "otra cuenta" picker on this page, new-transaction and edit alike
let editingTransactionId = null; // which transaction #edit-transaction-dialog's fields currently belong to — set right before it opens, read back on save

function renderHeader(account) {
  document.getElementById('account-title').textContent = accountLabel(account);
  document.getElementById('account-balance-line').textContent = `Saldo actual: ${formatCurrency(account.balance ?? 0)}`;
}

// The "otra cuenta" picker excludes only this account itself — unlike
// accounting.js's own "cuenta superior" picker, there's no circular
// relationship to guard against here: any two distinct accounts can be
// the two sides of a transaction, including a child of this one or
// this one's own parent. selectedId is nullable — unset for the "+
// Nueva transacción" dialog's own picker, set to the transaction's
// current other account when reopening one for editing.
function otherAccountOptions(selectedId) {
  return flattenAccountTree(allAccounts)
    .filter(({ account }) => account.id !== accountId)
    .map(({ account, depth }) => {
      const selected = account.id === selectedId ? 'selected' : '';
      return `<option value="${account.id}" ${selected}>${'— '.repeat(depth)}${escapeHtml(accountLabel(account))}</option>`;
    })
    .join('');
}

// Both the header's own current balance and the "otra cuenta" options
// come from the one GET /accounts call — that listing already carries
// every account's own balance (GET /accounts/{id} alone doesn't, see
// CLAUDE.md's own Accounting section), so there's no need for a
// second, single-account fetch just for this page's own header.
async function loadAccount() {
  try {
    allAccounts = await apiFetch('/accounts');
    const account = allAccounts.find((a) => a.id === accountId);
    if (!account) {
      showError('Esta cuenta no existe.');
      return;
    }
    renderHeader(account);
    document.getElementById('nt-other-account').innerHTML = otherAccountOptions(null);
  } catch {
    showError('No se pudo cargar la cuenta.');
  }
}

// The two-entry transaction shape shared by creating a new one and
// correcting an existing one: this account gets amount as typed, the
// chosen other account gets its exact negative, so the pair already
// sums to zero without whoever's filling this in ever having to think
// about the second side of the entry themselves.
function twoEntryBody(dateValue, description, amount, otherAccountId) {
  return {
    description,
    // occurred_at is a real timestamp server-side, but this form only
    // ever asks for a date — pinned to noon UTC, not midnight, so the
    // date shown afterward (formatDate, which reads local getters)
    // never rolls back a day for anyone viewing from a timezone behind
    // UTC. Editing an existing transaction collapses whatever time of
    // day it actually had down to this same noon-UTC convention too —
    // a deliberate simplification, consistent with this page only ever
    // asking for a date, never a time.
    occurred_at: `${dateValue}T12:00:00Z`,
    entries: [
      { account_id: accountId, amount },
      { account_id: otherAccountId, amount: -amount },
    ],
  };
}

function renderEntryRow(entry) {
  return `
    <tr>
      <td>${formatDate(entry.occurred_at)}</td>
      <td>${escapeHtml(entry.description)}</td>
      <td class="balance-cell">${formatCurrency(entry.amount)}</td>
      <td class="balance-cell">${formatCurrency(entry.balance_after)}</td>
      <td>
        <div class="table-actions">
          <button type="button" class="btn btn-secondary btn-small" data-edit-entry="${entry.transaction_id}">Editar</button>
          <button type="button" class="btn btn-danger btn-small" data-delete-entry="${entry.transaction_id}">Borrar</button>
        </div>
      </td>
    </tr>
  `;
}

async function loadEntries() {
  const tableBody = document.getElementById('entries-table-body');
  try {
    const entries = await apiFetch(`/accounts/${accountId}/transactions`);
    tableBody.innerHTML = entries.length
      ? entries.map(renderEntryRow).join('')
      : '<tr><td colspan="5">Todavía no hay transacciones en esta cuenta.</td></tr>';
  } catch {
    tableBody.innerHTML = '<tr><td colspan="5">No se pudieron cargar las transacciones.</td></tr>';
  }
}

const editTransactionDialog = document.getElementById('edit-transaction-dialog');
const editTransactionForm = document.getElementById('edit-transaction-form');
initModal(editTransactionDialog, editTransactionForm);

// The ledger listing only ever carries this account's own side of each
// transaction (see AccountEntry's own doc comment) — editing needs the
// full transaction, entries included, so this fetches it fresh every
// time a row's own "Editar" is opened rather than trying to keep a
// second copy of every transaction in memory. #edit-transaction-dialog
// is one shared dialog, not one per row — only ever offers the same
// simplified two-field edit the create dialog uses; a transaction with
// more than two entries (only possible by calling the API directly —
// this page never creates one that way) is reported instead of
// silently mis-edited, and the dialog never opens for it.
async function openEditTransactionDialog(transactionId) {
  try {
    const transaction = await apiFetch(`/transactions/${transactionId}`);
    if (transaction.entries.length !== 2) {
      showError('Esta transacción tiene más de dos apuntes: corrígela por la API — esta página solo edita transacciones simples de dos apuntes.');
      return;
    }
    const ownEntry = transaction.entries.find((e) => e.account_id === accountId);
    const otherEntry = transaction.entries.find((e) => e.account_id !== accountId);
    if (!ownEntry || !otherEntry) {
      showError('No se han podido determinar los apuntes de esta transacción.');
      return;
    }
    editingTransactionId = transactionId;
    document.getElementById('et-date').value = transaction.occurred_at.slice(0, 10);
    document.getElementById('et-description').value = transaction.description;
    document.getElementById('et-amount').value = ownEntry.amount;
    document.getElementById('et-other-account').innerHTML = otherAccountOptions(otherEntry.account_id);
    editTransactionDialog.showModal();
  } catch (err) {
    showError(err.message);
  }
}

editTransactionForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  const dateValue = document.getElementById('et-date').value;
  const description = document.getElementById('et-description').value.trim();
  const amount = Number(document.getElementById('et-amount').value);
  const otherAccountId = Number(document.getElementById('et-other-account').value);

  const submitButton = editTransactionForm.querySelector('button[type="submit"]');
  submitButton.disabled = true;
  try {
    await apiFetch(`/transactions/${editingTransactionId}`, {
      method: 'PATCH',
      body: JSON.stringify(twoEntryBody(dateValue, description, amount, otherAccountId)),
    });
    showFeedback('Transacción actualizada.');
    editTransactionDialog.close();
    await loadAccount();
    await loadEntries();
  } catch (err) {
    showError(err.message);
  } finally {
    submitButton.disabled = false;
  }
});

async function deleteTransaction(transactionId) {
  if (!confirm('¿Borrar esta transacción? También se elimina el apunte que crea en la otra cuenta.')) return;
  try {
    await apiFetch(`/transactions/${transactionId}`, { method: 'DELETE' });
    showFeedback('Transacción borrada.');
    await loadAccount();
    await loadEntries();
  } catch (err) {
    showError(err.message);
  }
}

document.getElementById('entries-table-body').addEventListener('click', async (event) => {
  const editButton = event.target.closest('button[data-edit-entry]');
  if (editButton) {
    await openEditTransactionDialog(Number(editButton.dataset.editEntry));
    return;
  }

  const deleteButton = event.target.closest('button[data-delete-entry]');
  if (deleteButton) {
    await deleteTransaction(Number(deleteButton.dataset.deleteEntry));
  }
});

const newTransactionDialog = document.getElementById('new-transaction-dialog');
const newTransactionForm = document.getElementById('new-transaction-form');
initModal(newTransactionDialog, newTransactionForm);

document.getElementById('toggle-new-transaction').addEventListener('click', () => {
  newTransactionDialog.showModal();
});

// Only four fields (date, description, amount, other account) —
// always exactly two entries: see twoEntryBody.
newTransactionForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  const formData = new FormData(newTransactionForm);
  const amount = Number(formData.get('amount'));
  const otherAccountId = Number(formData.get('other_account_id'));
  const dateValue = formData.get('occurred_at');
  const description = formData.get('description').toString().trim();

  const submitButton = newTransactionForm.querySelector('button[type="submit"]');
  submitButton.disabled = true;
  try {
    await apiFetch('/transactions', { method: 'POST', body: JSON.stringify(twoEntryBody(dateValue, description, amount, otherAccountId)) });
    showFeedback('Transacción creada.');
    newTransactionDialog.close();
    await loadAccount();
    await loadEntries();
  } catch (err) {
    showError(err.message);
  } finally {
    submitButton.disabled = false;
  }
});

await loadAccount();
await loadEntries();
