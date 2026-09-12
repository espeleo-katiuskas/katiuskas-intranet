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

function renderHeader(account) {
  document.getElementById('account-title').textContent = accountLabel(account);
  document.getElementById('account-balance-line').textContent = `Saldo actual: ${formatCurrency(account.balance ?? 0)}`;
}

// The "otra cuenta" picker excludes only this account itself — unlike
// accounting.js's own "cuenta superior" picker, there's no circular
// relationship to guard against here: any two distinct accounts can be
// the two sides of a transaction, including a child of this one or
// this one's own parent.
function renderOtherAccountOptions(accounts) {
  const select = document.getElementById('nt-other-account');
  select.innerHTML = flattenAccountTree(accounts)
    .filter(({ account }) => account.id !== accountId)
    .map(({ account, depth }) => `<option value="${account.id}">${'— '.repeat(depth)}${escapeHtml(accountLabel(account))}</option>`)
    .join('');
}

// Both the header's own current balance and the "otra cuenta" options
// come from the one GET /accounts call — that listing already carries
// every account's own balance (GET /accounts/{id} alone doesn't, see
// CLAUDE.md's own Accounting section), so there's no need for a
// second, single-account fetch just for this page's own header.
async function loadAccount() {
  try {
    const accounts = await apiFetch('/accounts');
    const account = accounts.find((a) => a.id === accountId);
    if (!account) {
      showError('Esta cuenta no existe.');
      return;
    }
    renderHeader(account);
    renderOtherAccountOptions(accounts);
  } catch {
    showError('No se pudo cargar la cuenta.');
  }
}

function renderEntryRow(entry) {
  return `
    <tr>
      <td>${formatDate(entry.occurred_at)}</td>
      <td>${escapeHtml(entry.description)}</td>
      <td class="balance-cell">${formatCurrency(entry.amount)}</td>
      <td class="balance-cell">${formatCurrency(entry.balance_after)}</td>
    </tr>
  `;
}

async function loadEntries() {
  const tableBody = document.getElementById('entries-table-body');
  try {
    const entries = await apiFetch(`/accounts/${accountId}/transactions`);
    tableBody.innerHTML = entries.length
      ? entries.map(renderEntryRow).join('')
      : '<tr><td colspan="4">Todavía no hay transacciones en esta cuenta.</td></tr>';
  } catch {
    tableBody.innerHTML = '<tr><td colspan="4">No se pudieron cargar las transacciones.</td></tr>';
  }
}

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
// always exactly two entries: this account gets amount as typed, the
// chosen other account gets its exact negative, so the pair already
// sums to zero without the person ever having to think about the
// second side of the entry themselves.
newTransactionForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  const formData = new FormData(newTransactionForm);
  const amount = Number(formData.get('amount'));
  const otherAccountId = Number(formData.get('other_account_id'));
  const dateValue = formData.get('occurred_at');

  const body = {
    description: formData.get('description').toString().trim(),
    // occurred_at is a real timestamp server-side, but this form only
    // ever asks for a date (see account.html) — pinned to noon UTC,
    // not midnight, so the date shown afterward (formatDate, which
    // reads local getters) never rolls back a day for anyone viewing
    // from a timezone behind UTC.
    occurred_at: `${dateValue}T12:00:00Z`,
    entries: [
      { account_id: accountId, amount },
      { account_id: otherAccountId, amount: -amount },
    ],
  };

  const submitButton = newTransactionForm.querySelector('button[type="submit"]');
  submitButton.disabled = true;
  try {
    await apiFetch('/transactions', { method: 'POST', body: JSON.stringify(body) });
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
