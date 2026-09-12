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

// Reached only from /accounting's own account tree — same admin/money
// gate as that page (see accounting.js's own doc comment for why this
// is tighter than the API itself, which opens reading to every
// baseline role).
await initPage({ requireAnyRole: ['admin', 'money'] });

const accountId = Number(new URLSearchParams(window.location.search).get('id'));
if (!accountId) {
  window.location.href = '/accounting';
}

let allAccounts = []; // from the last GET /accounts — reused by every "otra cuenta" picker on this page, new-transaction and per-row edit alike

function renderHeader(account) {
  document.getElementById('account-title').textContent = accountLabel(account);
  document.getElementById('account-balance-line').textContent = `Saldo actual: ${formatCurrency(account.balance ?? 0)}`;
}

// The "otra cuenta" picker excludes only this account itself — unlike
// accounting.js's own "cuenta superior" picker, there's no circular
// relationship to guard against here: any two distinct accounts can be
// the two sides of a transaction, including a child of this one or
// this one's own parent. selectedId is nullable — unset for the "+
// Nueva transacción" panel's own picker, set to the transaction's
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
          <button type="button" class="btn btn-secondary btn-small" data-toggle-edit-entry="${entry.transaction_id}">Editar</button>
          <button type="button" class="btn btn-danger btn-small" data-delete-entry="${entry.transaction_id}">Borrar</button>
        </div>
      </td>
    </tr>
    <tr data-edit-entry-row="${entry.transaction_id}" hidden>
      <td colspan="5"><div class="edit-fields" data-edit-entry-panel="${entry.transaction_id}"></div></td>
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

// The ledger listing only ever carries this account's own side of each
// transaction (see AccountEntry's own doc comment) — editing needs the
// full transaction, entries included, so this fetches it fresh every
// time a row's own "Editar" is opened rather than trying to keep a
// second copy of every transaction in memory. Only ever offers the
// simplified two-field edit this page's own create form uses; a
// transaction with more than two entries (only possible by calling the
// API directly — this page never creates one) is reported instead of
// silently mis-edited.
async function toggleEntryEdit(transactionId) {
  const row = document.querySelector(`[data-edit-entry-row="${transactionId}"]`);
  if (!row.hidden) {
    row.hidden = true;
    return;
  }

  const panel = row.querySelector(`[data-edit-entry-panel="${transactionId}"]`);
  panel.innerHTML = 'Cargando…';
  row.hidden = false;
  try {
    const transaction = await apiFetch(`/transactions/${transactionId}`);
    if (transaction.entries.length !== 2) {
      row.hidden = true;
      showError('Esta transacción tiene más de dos apuntes: corrígela por la API — esta página solo edita transacciones simples de dos apuntes.');
      return;
    }
    const ownEntry = transaction.entries.find((e) => e.account_id === accountId);
    const otherEntry = transaction.entries.find((e) => e.account_id !== accountId);
    if (!ownEntry || !otherEntry) {
      row.hidden = true;
      showError('No se han podido determinar los apuntes de esta transacción.');
      return;
    }
    panel.innerHTML = `
      <div class="field-grid">
        <div class="field">
          <label>Fecha</label>
          <input type="date" data-entry-field="occurred_at" value="${transaction.occurred_at.slice(0, 10)}">
        </div>
        <div class="field span-2">
          <label>Descripción</label>
          <input type="text" data-entry-field="description" value="${escapeHtml(transaction.description)}">
        </div>
        <div class="field">
          <label>Cantidad en esta cuenta</label>
          <input type="number" step="0.01" data-entry-field="amount" value="${ownEntry.amount}">
        </div>
        <div class="field">
          <label>Otra cuenta</label>
          <select data-entry-field="other_account_id">${otherAccountOptions(otherEntry.account_id)}</select>
        </div>
      </div>
      <button type="button" class="btn btn-primary btn-small" data-save-entry="${transactionId}">Guardar</button>
    `;
  } catch (err) {
    row.hidden = true;
    showError(err.message);
  }
}

async function saveEntry(button) {
  const transactionId = button.dataset.saveEntry;
  const panel = document.querySelector(`[data-edit-entry-panel="${transactionId}"]`);
  const dateValue = panel.querySelector('[data-entry-field="occurred_at"]').value;
  const description = panel.querySelector('[data-entry-field="description"]').value.trim();
  const amount = Number(panel.querySelector('[data-entry-field="amount"]').value);
  const otherAccountId = Number(panel.querySelector('[data-entry-field="other_account_id"]').value);

  button.disabled = true;
  try {
    await apiFetch(`/transactions/${transactionId}`, {
      method: 'PATCH',
      body: JSON.stringify(twoEntryBody(dateValue, description, amount, otherAccountId)),
    });
    showFeedback('Transacción actualizada.');
    await loadAccount();
    await loadEntries();
  } catch (err) {
    showError(err.message);
    button.disabled = false;
  }
}

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
  const toggleButton = event.target.closest('button[data-toggle-edit-entry]');
  if (toggleButton) {
    await toggleEntryEdit(Number(toggleButton.dataset.toggleEditEntry));
    return;
  }

  const deleteButton = event.target.closest('button[data-delete-entry]');
  if (deleteButton) {
    await deleteTransaction(Number(deleteButton.dataset.deleteEntry));
    return;
  }

  const saveButton = event.target.closest('button[data-save-entry]');
  if (saveButton) {
    await saveEntry(saveButton);
  }
});

const newTransactionPanel = document.getElementById('new-transaction-panel');
const newTransactionForm = document.getElementById('new-transaction-form');

document.getElementById('toggle-new-transaction').addEventListener('click', () => {
  newTransactionPanel.hidden = !newTransactionPanel.hidden;
});

document.getElementById('cancel-new-transaction').addEventListener('click', () => {
  newTransactionForm.reset();
  newTransactionPanel.hidden = true;
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
    newTransactionForm.reset();
    newTransactionPanel.hidden = true;
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
