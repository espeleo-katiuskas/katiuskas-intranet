import {
  apiFetch,
  escapeHtml,
  formatDate,
  formatCurrency,
  showFeedback,
  showError,
  hasAnyRole,
  pickerLabel,
  renderPickerOptions,
  resolvePickerId,
} from './api.js';
import { initPage } from './layout.js';
import { flattenAccountTree, accountLabel } from './accountTree.js';
import { initModal, openModal } from './modal.js';

// Reached only from /accounting's own account tree — same admin/money
// gate as that page (see accounting.js's own doc comment for why this
// is tighter than the API itself, which opens reading to every
// baseline role).
const viewer = await initPage({ requireAnyRole: ['admin', 'money'] });

// Attaching a due or a brand-new license to a transaction (see
// setupDuesAndLicensesSection below) hits POST /users/{login}/dues and
// POST /users/{login}/licenses, both admin-only at the API itself —
// tighter than this whole page's own admin/money gate, so those two
// sections only ever show for a real admin, never for someone who only
// holds "money" (data-roles="admin" in account.html already hides the
// markup for them; this just keeps this file from wiring up listeners
// on elements that, for them, were never rendered in the first place).
const isRealAdmin = hasAnyRole(viewer, ['admin']);

const accountId = Number(new URLSearchParams(window.location.search).get('id'));
if (!accountId) {
  window.location.href = '/accounting';
}

let allAccounts = []; // from the last GET /accounts — reused by every "otra cuenta" picker on this page, new-transaction and edit alike
let editingTransactionId = null; // which transaction #edit-transaction-dialog's fields currently belong to — set right before it opens, read back on save

// userEntries/allLicenseTypes back the socio and license-type pickers in
// both dialogs' own "cuotas anuales"/"licencias nuevas" sections — see
// setupDuesAndLicensesSection. Loaded once, only for a real admin
// (POST dues/licenses is admin-only, so there's nothing for anyone else
// to pick here).
let userEntries = [];
let allLicenseTypes = [];
let licenseTypesById = {};

async function loadAdminPickerCatalogs() {
  if (!isRealAdmin) return;
  try {
    const directory = await apiFetch('/users/directory');
    userEntries = directory
      .slice()
      .sort((a, b) => `${a.last_name} ${a.first_name}`.localeCompare(`${b.last_name} ${b.first_name}`, 'es'))
      .map((person) => ({ id: person.login, label: pickerLabel(`${person.first_name} ${person.last_name}`, person.login) }));
    for (const id of ['nt-due-user-options', 'et-due-user-options', 'nt-license-user-options', 'et-license-user-options']) {
      document.getElementById(id).innerHTML = renderPickerOptions(userEntries);
    }
  } catch {
    showError('No se pudo cargar la lista de socios.');
  }
  try {
    allLicenseTypes = await apiFetch('/license-types');
    licenseTypesById = Object.fromEntries(allLicenseTypes.map((t) => [t.id, t]));
    const options = allLicenseTypes.map((t) => `<option value="${t.id}">${escapeHtml(t.name)}</option>`).join('');
    document.getElementById('nt-license-type').innerHTML = options;
    document.getElementById('et-license-type').innerHTML = options;
  } catch {
    showError('No se pudo cargar el catálogo de licencias.');
  }
}

// Backs both dialogs' "cuotas anuales"/"licencias nuevas" sections
// (prefix "nt" for the create dialog, "et" for the edit one — see
// account.html). Nothing added here is sent anywhere until the
// transaction itself is actually saved: "+ Añadir cuota"/"+ Añadir
// licencia" only appends to a pending, client-side-only list (rendered
// as removable chips); submitPending walks that list afterward, one
// sequential POST per item, once the transaction's own id and date are
// known. A single transaction can cover several socios' cuotas, or a
// mix of cuotas and brand-new licenses, all at once — see CLAUDE.md's
// own Accounting section. Editing an existing transaction can only ever
// add to what it already paid for: there's no way to remove, from this
// dialog, a due/license some earlier save already linked to it.
function setupDuesAndLicensesSection(prefix) {
  let pendingDues = [];
  let pendingLicenses = [];

  function renderDueChips() {
    document.getElementById(`${prefix}-dues-chips`).innerHTML = pendingDues
      .map((due, index) => `<span class="badge-removable">${escapeHtml(due.label)}<button type="button" data-remove-due="${index}" title="Quitar">×</button></span>`)
      .join('');
  }

  function renderLicenseChips() {
    document.getElementById(`${prefix}-licenses-chips`).innerHTML = pendingLicenses
      .map((license, index) => `<span class="badge-removable">${escapeHtml(license.label)}<button type="button" data-remove-license="${index}" title="Quitar">×</button></span>`)
      .join('');
  }

  document.getElementById(`${prefix}-dues-chips`).addEventListener('click', (event) => {
    const button = event.target.closest('button[data-remove-due]');
    if (!button) return;
    pendingDues.splice(Number(button.dataset.removeDue), 1);
    renderDueChips();
  });

  document.getElementById(`${prefix}-licenses-chips`).addEventListener('click', (event) => {
    const button = event.target.closest('button[data-remove-license]');
    if (!button) return;
    pendingLicenses.splice(Number(button.dataset.removeLicense), 1);
    renderLicenseChips();
  });

  document.getElementById(`${prefix}-license-type`).addEventListener('change', (event) => {
    const chosen = licenseTypesById[Number(event.target.value)];
    document.getElementById(`${prefix}-license-other-detail`).hidden = !chosen?.is_other;
  });

  // Both "add" rows sit inside the transaction's own <form> (so their
  // picker inputs can share its field-grid layout) — without this,
  // pressing Enter while typing in one of them would submit the whole
  // transaction early instead of just adding the chip, since that's a
  // plain text input's default behavior inside any <form>. Enter here
  // triggers the row's own "+ Añadir" button instead.
  for (const rowPrefix of [`${prefix}-add-due`, `${prefix}-add-license`]) {
    document.getElementById(rowPrefix).closest('.role-add-row').addEventListener('keydown', (event) => {
      if (event.key !== 'Enter') return;
      event.preventDefault();
      document.getElementById(rowPrefix).click();
    });
  }

  document.getElementById(`${prefix}-add-due`).addEventListener('click', () => {
    const userInput = document.getElementById(`${prefix}-due-user`);
    const yearInput = document.getElementById(`${prefix}-due-year`);
    const login = resolvePickerId(userEntries, userInput.value);
    const year = Number(yearInput.value);
    if (!login || !year) {
      showError('Elige un socio de la lista e indica el año, antes de añadir la cuota.');
      return;
    }
    const personLabel = userEntries.find((e) => e.id === login)?.label.replace(/ #.*$/, '') ?? login;
    pendingDues.push({ login, year, label: `${personLabel} · ${year}` });
    userInput.value = '';
    yearInput.value = '';
    renderDueChips();
  });

  document.getElementById(`${prefix}-add-license`).addEventListener('click', () => {
    const userInput = document.getElementById(`${prefix}-license-user`);
    const typeSelect = document.getElementById(`${prefix}-license-type`);
    const otherDetailInput = document.getElementById(`${prefix}-license-other-detail`);
    const yearInput = document.getElementById(`${prefix}-license-year`);
    const login = resolvePickerId(userEntries, userInput.value);
    const licenseTypeId = Number(typeSelect.value);
    const chosen = licenseTypesById[licenseTypeId];
    const year = Number(yearInput.value);
    if (!login || !licenseTypeId || !year) {
      showError('Elige un socio y un tipo de licencia de la lista, e indica el año, antes de añadir la licencia.');
      return;
    }
    const otherDetail = otherDetailInput.value.trim() || null;
    if (chosen?.is_other && !otherDetail) {
      showError('Indica de qué licencia se trata.');
      return;
    }
    const personLabel = userEntries.find((e) => e.id === login)?.label.replace(/ #.*$/, '') ?? login;
    const typeLabel = chosen?.is_other ? `${chosen.name}: ${otherDetail}` : (chosen?.name ?? '?');
    pendingLicenses.push({ login, licenseTypeId, otherDetail: chosen?.is_other ? otherDetail : null, year, label: `${personLabel} · ${typeLabel} · ${year}` });
    userInput.value = '';
    typeSelect.selectedIndex = 0;
    otherDetailInput.value = '';
    otherDetailInput.hidden = true;
    yearInput.value = '';
    renderLicenseChips();
  });

  function reset() {
    pendingDues = [];
    pendingLicenses = [];
    renderDueChips();
    renderLicenseChips();
  }

  // Sends every pending due/license, tied to transactionId, once the
  // transaction itself is safely saved — sequential, not parallel: a
  // clearer one-item-at-a-time failure story than Promise.all's
  // all-or-nothing rejection, for what's expected to be at most a
  // handful of calls. Each failure is collected rather than thrown
  // immediately, since the transaction itself already succeeded by this
  // point — one bad chip shouldn't stop the rest from going through.
  async function submitPending(transactionId, occurredOnDate) {
    const errors = [];
    for (const due of pendingDues) {
      try {
        await apiFetch(`/users/${due.login}/dues`, {
          method: 'POST',
          body: JSON.stringify({ year: due.year, transaction_id: transactionId, start_date: occurredOnDate }),
        });
      } catch (err) {
        errors.push(`cuota de ${due.label}: ${err.message}`);
      }
    }
    for (const license of pendingLicenses) {
      try {
        await apiFetch(`/users/${license.login}/licenses`, {
          method: 'POST',
          body: JSON.stringify({
            license_type_id: license.licenseTypeId,
            other_detail: license.otherDetail,
            issued_date: occurredOnDate,
            year: license.year,
            transaction_id: transactionId,
          }),
        });
      } catch (err) {
        errors.push(`licencia de ${license.label}: ${err.message}`);
      }
    }
    reset();
    return errors;
  }

  renderDueChips();
  renderLicenseChips();
  return { reset, submitPending };
}

const newTransactionDuesLicenses = isRealAdmin ? setupDuesAndLicensesSection('nt') : null;
const editTransactionDuesLicenses = isRealAdmin ? setupDuesAndLicensesSection('et') : null;

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
if (editTransactionDuesLicenses) {
  editTransactionDialog.addEventListener('close', () => editTransactionDuesLicenses.reset());
}

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
    openModal(editTransactionDialog);
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
    // editingTransactionId itself never changes on a PATCH, so any
    // dues/licenses added here are tied to the same transaction the
    // edit just corrected — see setupDuesAndLicensesSection's own doc
    // comment on why this can only ever add to it, never replace what a
    // previous save already linked.
    const pendingErrors = editTransactionDuesLicenses
      ? await editTransactionDuesLicenses.submitPending(editingTransactionId, dateValue)
      : [];
    if (pendingErrors.length) {
      showError(`Transacción actualizada, pero no se pudo registrar: ${pendingErrors.join(' · ')}`);
    } else {
      showFeedback('Transacción actualizada.');
    }
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
if (newTransactionDuesLicenses) {
  newTransactionDialog.addEventListener('close', () => newTransactionDuesLicenses.reset());
}

document.getElementById('toggle-new-transaction').addEventListener('click', () => {
  openModal(newTransactionDialog);
});

// Only four fields (date, description, amount, other account) drive the
// transaction itself — always exactly two entries: see twoEntryBody.
// Any pending dues/licenses (admin-only, see
// setupDuesAndLicensesSection) are sent afterward, once the new
// transaction's own id is known.
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
    const transaction = await apiFetch('/transactions', { method: 'POST', body: JSON.stringify(twoEntryBody(dateValue, description, amount, otherAccountId)) });
    const pendingErrors = newTransactionDuesLicenses
      ? await newTransactionDuesLicenses.submitPending(transaction.id, dateValue)
      : [];
    if (pendingErrors.length) {
      showError(`Transacción creada, pero no se pudo registrar: ${pendingErrors.join(' · ')}`);
    } else {
      showFeedback('Transacción creada.');
    }
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
await loadAdminPickerCatalogs();
