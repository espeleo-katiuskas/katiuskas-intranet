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
import { accountTypes, flattenAccountTree, shownAccounts, accountLabel } from './accountTree.js';
import { initModal, openModal } from './modal.js';

// Reached only from /contabilidad's own account tree — same admin/money
// gate as that page (see accounting.js's own doc comment for why this
// is tighter than the API itself, which opens reading to every
// baseline role).
const viewer = await initPage({ requireAnyRole: ['admin', 'money'] });

// Attaching a due or a brand-new license to a transaction (see
// setupDuesAndLicensesSection below) hits POST /users/{login}/dues and
// POST /users/{login}/licenses, both admin-only at the API itself —
// tighter than this whole page's own admin/money gate, so those two
// sections only ever show for a real admin, never for someone who only
// holds "money" (data-roles="admin" in cuenta.html already hides the
// markup for them; this just keeps this file from wiring up listeners
// on elements that, for them, were never rendered in the first place).
const isRealAdmin = hasAnyRole(viewer, ['admin']);

const accountId = Number(new URLSearchParams(window.location.search).get('id'));
if (!accountId) {
  window.location.href = '/contabilidad';
}

let allAccounts = []; // from the last GET /accounts — reused by every "otra cuenta" picker on this page, new-transaction and edit alike
let editingTransactionId = null; // which transaction #edit-transaction-dialog's fields currently belong to — set right before it opens, read back on save

// userEntries is the people directory, loaded for everyone: it turns the
// logins in a transaction's detail (the cuotas and licencias it paid
// for) into names, and, for a real admin only, feeds the socio pickers
// of both dialogs' "cuotas anuales"/"licencias nuevas" sections — see
// setupDuesAndLicensesSection. allLicenseTypes backs the license-type
// picker, so it too is only loaded for a real admin (POST dues/licenses
// is admin-only, so there's nothing for anyone else to pick).
let userEntries = [];

// A person's display name from their login — the picker's own label
// without its "#id" suffix — falling back to the login itself.
function personName(login) {
  return userEntries.find((e) => e.id === login)?.label.replace(/ #.*$/, '') ?? login;
}
let allLicenseTypes = [];
let licenseTypesById = {};

async function loadCatalogs() {
  try {
    const directory = await apiFetch('/users/directory');
    userEntries = directory
      .slice()
      .sort((a, b) =>
        `${a.first_name} ${a.last_name}`.localeCompare(`${b.first_name} ${b.last_name}`, 'es'),
      )
      .map((person) => ({
        id: person.login,
        label: pickerLabel(`${person.first_name} ${person.last_name}`, person.login),
      }));
    if (isRealAdmin) {
      for (const id of [
        'nt-due-user-options',
        'et-due-user-options',
        'nt-license-user-options',
        'et-license-user-options',
      ]) {
        document.getElementById(id).innerHTML = renderPickerOptions(userEntries);
      }
    }
  } catch {
    showError('No se pudo cargar la lista de socios.');
  }
  if (!isRealAdmin) return;
  try {
    allLicenseTypes = await apiFetch('/license-types');
    licenseTypesById = Object.fromEntries(allLicenseTypes.map((t) => [t.id, t]));
    const options = allLicenseTypes
      .map((t) => `<option value="${t.id}">${escapeHtml(t.name)}</option>`)
      .join('');
    document.getElementById('nt-license-type').innerHTML = options;
    document.getElementById('et-license-type').innerHTML = options;
  } catch {
    showError('No se pudo cargar el catálogo de licencias.');
  }
}

// Backs both dialogs' "cuotas anuales"/"licencias nuevas" sections
// (prefix "nt" for the create dialog, "et" for the edit one — see
// cuenta.html). Nothing added here is sent anywhere until the
// transaction itself is actually saved: "+ Añadir cuota"/"+ Añadir
// licencia" only appends to a pending, client-side-only list (rendered
// as removable chips); submitPending walks that list afterward, one
// sequential POST per item, once the transaction's own id and date are
// known. A single transaction can cover several socios' cuotas, or a
// mix of cuotas and brand-new licenses, all at once. Editing an
// existing transaction can only ever add to what it already paid for:
// there's no way to remove, from this dialog, a due/license some
// earlier save already linked to it.
function setupDuesAndLicensesSection(prefix) {
  let pendingDues = [];
  let pendingLicenses = [];

  function renderDueChips() {
    document.getElementById(`${prefix}-dues-chips`).innerHTML = pendingDues
      .map(
        (due, index) =>
          `<span class="badge-removable">${escapeHtml(due.label)}<button type="button" data-remove-due="${index}" title="Quitar">×</button></span>`,
      )
      .join('');
  }

  function renderLicenseChips() {
    document.getElementById(`${prefix}-licenses-chips`).innerHTML = pendingLicenses
      .map(
        (license, index) =>
          `<span class="badge-removable">${escapeHtml(license.label)}<button type="button" data-remove-license="${index}" title="Quitar">×</button></span>`,
      )
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

  // The cuota's and the license's "Año" inputs start out filled with the
  // year the transaction's date points at — that date's own year, or the
  // next one if it falls in December, since a payment that late is almost
  // always for the following year (the same reasoning behind a license
  // having its own year, separate from issued_date: see
  // licenses.CreateInput.Year). They follow the date while the person
  // hasn't typed in them; one they've edited themselves is left alone
  // from then on, until the dialog is next opened.
  const dateInput = document.getElementById(`${prefix}-date`);
  const yearInputs = [
    document.getElementById(`${prefix}-due-year`),
    document.getElementById(`${prefix}-license-year`),
  ];
  const editedYearInputs = new Set();

  function suggestedYear() {
    const [year, month] = dateInput.value.split('-').map(Number);
    if (!year || !month) return '';
    return String(month === 12 ? year + 1 : year);
  }

  function fillYears() {
    const suggested = suggestedYear();
    for (const input of yearInputs) {
      if (!editedYearInputs.has(input)) input.value = suggested;
    }
  }

  for (const input of yearInputs) {
    input.addEventListener('input', () => editedYearInputs.add(input));
  }
  dateInput.addEventListener('input', fillYears);

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
    document
      .getElementById(rowPrefix)
      .closest('.role-add-row')
      .addEventListener('keydown', (event) => {
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
    const personLabel =
      userEntries.find((e) => e.id === login)?.label.replace(/ #.*$/, '') ?? login;
    pendingDues.push({ login, year, label: `${personLabel} · ${year}` });
    userInput.value = '';
    editedYearInputs.delete(yearInput);
    fillYears();
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
      showError(
        'Elige un socio y un tipo de licencia de la lista, e indica el año, antes de añadir la licencia.',
      );
      return;
    }
    const otherDetail = otherDetailInput.value.trim() || null;
    if (chosen?.is_other && !otherDetail) {
      showError('Indica de qué licencia se trata.');
      return;
    }
    const personLabel =
      userEntries.find((e) => e.id === login)?.label.replace(/ #.*$/, '') ?? login;
    const typeLabel = chosen?.is_other ? `${chosen.name}: ${otherDetail}` : (chosen?.name ?? '?');
    pendingLicenses.push({
      login,
      licenseTypeId,
      otherDetail: chosen?.is_other ? otherDetail : null,
      year,
      label: `${personLabel} · ${typeLabel} · ${year}`,
    });
    userInput.value = '';
    typeSelect.selectedIndex = 0;
    otherDetailInput.value = '';
    otherDetailInput.hidden = true;
    editedYearInputs.delete(yearInput);
    fillYears();
    renderLicenseChips();
  });

  function reset() {
    pendingDues = [];
    pendingLicenses = [];
    editedYearInputs.clear();
    renderDueChips();
    renderLicenseChips();
    setExisting({ dues: [], licenses: [] });
  }

  // Called each time the dialog opens, once its date is filled in: starts
  // both year inputs over from the date, whatever was typed last time.
  function syncYears() {
    editedYearInputs.clear();
    fillYears();
  }

  // Each section shows up for one of two reasons, tracked separately:
  //  - it can ADD: dues if either of the transaction's two accounts is
  //    marked allows_dues, licenses if either is marked allows_licenses
  //    (see sectionsFor, setVisibility). Whatever can't add is also
  //    emptied — a chip added while the other account was selected must
  //    not still be sent once the person has switched to one that
  //    doesn't qualify, since the API would only reject it
  //    (transaction_not_for_dues/_licenses) after the transaction itself
  //    was already saved;
  //  - it already HAS something to show: what an earlier save registered
  //    against this transaction (setExisting — only the edit dialog ever
  //    has any). That stays visible even if no account qualifies any
  //    more (the flag was since turned off, or the accounts changed): it
  //    is a record of what was paid, not something about to be sent.
  // When neither section can add, a hint says why and where to change it.
  const duesField = document.getElementById(`${prefix}-dues-field`);
  const licensesField = document.getElementById(`${prefix}-licenses-field`);
  const duesAddRow = document.getElementById(`${prefix}-add-due`).closest('.role-add-row');
  const licensesAddRow = document.getElementById(`${prefix}-add-license`).closest('.role-add-row');
  const duesHint = document.getElementById(`${prefix}-dues-hint`);
  const licensesHint = document.getElementById(`${prefix}-licenses-hint`);
  const flagsHint = document.getElementById(`${prefix}-flags-hint`);
  const datesHint = document.getElementById(`${prefix}-dates-hint`);
  const duesExisting = document.getElementById(`${prefix}-dues-existing`);
  const licensesExisting = document.getElementById(`${prefix}-licenses-existing`);
  let canAdd = { dues: false, licenses: false };
  let existing = { dues: [], licenses: [] };

  function applyVisibility() {
    duesField.hidden = !(canAdd.dues || existing.dues.length);
    licensesField.hidden = !(canAdd.licenses || existing.licenses.length);
    duesAddRow.hidden = !canAdd.dues;
    licensesAddRow.hidden = !canAdd.licenses;
    if (duesHint) duesHint.hidden = !canAdd.dues;
    if (licensesHint) licensesHint.hidden = !canAdd.licenses;
    flagsHint.hidden = canAdd.dues || canAdd.licenses;
    if (datesHint) datesHint.hidden = !(canAdd.dues || canAdd.licenses);
  }

  function setVisibility({ dues, licenses }) {
    canAdd = { dues, licenses };
    if (!dues) {
      pendingDues = [];
      renderDueChips();
    }
    if (!licenses) {
      pendingLicenses = [];
      renderLicenseChips();
    }
    applyVisibility();
  }

  // What an earlier save already registered against this transaction,
  // as read-only chips (no ×: there's no endpoint to remove a due, and
  // removing a license isn't something this dialog offers) — only the
  // edit dialog has the containers for them; the create dialog never
  // has anything to show.
  function setExisting({ dues, licenses }) {
    existing = { dues, licenses };
    if (duesExisting) {
      duesExisting.innerHTML = dues
        .map(
          (due) =>
            `<span class="badge">${escapeHtml(`${personName(due.user_login)} · ${due.year}`)}</span>`,
        )
        .join(' ');
    }
    if (licensesExisting) {
      licensesExisting.innerHTML = licenses
        .map(
          (license) =>
            `<span class="badge">${escapeHtml(`${personName(license.user_login)} · ${license.type} · ${license.year}`)}</span>`,
        )
        .join(' ');
    }
    applyVisibility();
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
          body: JSON.stringify({
            year: due.year,
            transaction_id: transactionId,
            start_date: occurredOnDate,
          }),
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
  setVisibility({ dues: false, licenses: false });
  return { reset, submitPending, setVisibility, setExisting, syncYears };
}

const newTransactionDuesLicenses = isRealAdmin ? setupDuesAndLicensesSection('nt') : null;
const editTransactionDuesLicenses = isRealAdmin ? setupDuesAndLicensesSection('et') : null;

// Backs both dialogs' "cantidad + otra cuenta" lines (prefix "nt"/"et", the
// #nt-lines/#et-lines containers in cuenta.html). A transaction made here
// has as many lines as the person fills in: each one is an amount for
// THIS account and the other account that amount comes from or goes to,
// and as soon as the last line has both, a new empty one appears below it.
// On save this account gets the sum of every amount, and each other
// account the opposite of its own line (see entriesBody) — so the
// entries always sum to zero without anyone having to think about the
// balancing side.
//
// A line with neither field filled is ignored; one with only one is an
// error, so a half-typed line can't be silently dropped. The account
// picker starts on "— Elige una cuenta —" rather than on the first
// account, which is what lets "filled" mean something: otherwise every
// new line would already count as filled the moment it appeared.
function setupEntryLines(prefix, onChange) {
  const container = document.getElementById(`${prefix}-lines`);
  // Hidden accounts this dialog's transaction already uses (edit only).
  let keepHidden = new Set();
  const noAccount = '<option value="">— Elige una cuenta —</option>';

  // Only the first line carries visible labels; the rest line up under it
  // and are named for screen readers by aria-label.
  function lineHtml(isFirst, { amount = '', accountId = null } = {}) {
    return `
      <div class="entry-line">
        <div class="field">
          ${isFirst ? '<label>Cantidad en esta cuenta</label>' : ''}
          <input type="number" step="0.01" data-line-amount aria-label="Cantidad en esta cuenta" value="${amount}">
        </div>
        <div class="field">
          ${isFirst ? '<label>Otra cuenta</label>' : ''}
          <select data-line-account aria-label="Otra cuenta">${noAccount}${otherAccountOptions(accountId, keepHidden)}</select>
        </div>
      </div>`;
  }

  // Replaces every line with the given ones, plus the empty one that
  // always sits last.
  function setLines(lines) {
    keepHidden = new Set(lines.map((line) => line.accountId));
    container.innerHTML = [...lines, {}].map((line, index) => lineHtml(index === 0, line)).join('');
    updateTotal();
  }

  // The read-only "Total en esta cuenta" next to the date: what this
  // account will receive, i.e. the sum of every amount typed so far (in
  // whole cents, like entriesBody), updated on every keystroke — so it
  // counts a line whose account isn't chosen yet, and ignores one whose
  // amount is empty or not a number.
  function updateTotal() {
    const cents = rows().reduce((sum, row) => {
      const amount = Number(row.amountText);
      return row.amountText !== '' && Number.isFinite(amount)
        ? sum + Math.round(amount * 100)
        : sum;
    }, 0);
    document.getElementById(`${prefix}-total`).value = formatCurrency(cents / 100);
  }

  function rows() {
    return [...container.querySelectorAll('.entry-line')].map((element) => ({
      amountText: element.querySelector('[data-line-amount]').value.trim(),
      accountValue: element.querySelector('[data-line-account]').value,
    }));
  }

  function selectedAccountIds() {
    return rows()
      .filter((row) => row.accountValue !== '')
      .map((row) => Number(row.accountValue));
  }

  // The lines the person filled in, validated — throws with a message for
  // the submit handler to show if they aren't usable: a half-filled line,
  // a zero amount (an entry can't be zero), no lines at all, or amounts
  // that cancel out (this account would be left with a zero entry).
  function read() {
    const lines = [];
    for (const row of rows()) {
      if (row.amountText === '' && row.accountValue === '') continue;
      if (row.amountText === '' || row.accountValue === '') {
        throw new Error('Completa la cantidad y la otra cuenta de cada línea, o déjala vacía.');
      }
      const amount = Number(row.amountText);
      if (!Number.isFinite(amount) || amount === 0) {
        throw new Error('La cantidad de una línea no puede ser cero.');
      }
      lines.push({ amount, accountId: Number(row.accountValue) });
    }
    if (new Set(lines.map((line) => line.accountId)).size !== lines.length) {
      throw new Error(
        'Una misma cuenta no puede aparecer en dos líneas: suma sus cantidades en una sola.',
      );
    }
    if (!lines.length)
      throw new Error('Añade al menos una línea, con su cantidad y su otra cuenta.');
    if (lines.reduce((sum, line) => sum + Math.round(line.amount * 100), 0) === 0) {
      throw new Error(
        'La suma de las cantidades no puede ser cero: esta cuenta se quedaría sin apunte.',
      );
    }
    return lines;
  }

  function onEdited() {
    const last = rows().at(-1);
    if (last && last.amountText !== '' && last.accountValue !== '') {
      container.insertAdjacentHTML('beforeend', lineHtml(false));
    }
    updateTotal();
    onChange();
  }
  container.addEventListener('input', onEdited);
  container.addEventListener('change', onEdited);

  return { setLines, read, selectedAccountIds };
}

const transactionLines = {
  nt: setupEntryLines('nt', () => refreshDuesLicensesSections('nt')),
  et: setupEntryLines('et', () => refreshDuesLicensesSections('et')),
};

// A transaction can record cuotas if this account or any of the other
// accounts on its lines is marked allows_dues, and licencias if any is
// marked allows_licenses (the API refuses it otherwise — see
// memberships.Service.RecordDue/licenses.Service.Create — so these
// sections only ever appear when they could actually succeed).
function sectionsFor(otherAccountIds) {
  const involved = [accountId, ...otherAccountIds].map((id) =>
    allAccounts.find((a) => a.id === id),
  );
  return {
    dues: involved.some((account) => account?.allows_dues),
    licenses: involved.some((account) => account?.allows_licenses),
  };
}

// Re-evaluates one dialog's sections from the accounts its lines have
// picked so far. A no-op for anyone who isn't a real admin, who never
// gets the sections at all.
function refreshDuesLicensesSections(prefix) {
  const section = prefix === 'nt' ? newTransactionDuesLicenses : editTransactionDuesLicenses;
  if (!section) return;
  section.setVisibility(sectionsFor(transactionLines[prefix].selectedAccountIds()));
}

function renderHeader(account) {
  document.getElementById('account-title').textContent = accountLabel(account);
  document.getElementById('account-balance-line').textContent =
    `Saldo actual: ${formatCurrency(account.balance ?? 0)}`;
  // What the account is mainly for, when it says (nothing acts on it yet).
  const type = accountTypes[account.type];
  const typeLine = document.getElementById('account-type-line');
  typeLine.hidden = !type;
  if (type) typeLine.textContent = `Tipo: ${type.label} (${type.hint})`;
}

// The "otra cuenta" pickers exclude only this account itself — unlike
// accounting.js's own "cuenta superior" picker, there's no circular
// relationship to guard against here: any two distinct accounts can be
// the two sides of a transaction, including a child of this one or
// this one's own parent. selectedId is nullable — unset for a new
// empty line, set to the line's own account when reopening a
// transaction for editing. Hidden accounts (no longer used — see
// Account.Hidden) are left out, except those in keepHidden: the ones the
// transaction being edited already uses, which have to stay choosable
// or the dialog couldn't even show what it currently says.
function otherAccountOptions(selectedId, keepHidden = new Set()) {
  return flattenAccountTree(
    shownAccounts(allAccounts, (a) => !a.hidden || keepHidden.has(a.id) || a.id === accountId),
  )
    .filter(({ account }) => account.id !== accountId)
    .map(({ account, depth }) => {
      const selected = account.id === selectedId ? 'selected' : '';
      return `<option value="${account.id}" ${selected}>${'— '.repeat(depth)}${escapeHtml(account.name)}</option>`;
    })
    .join('');
}

// Both the header's own current balance and the "otra cuenta" options
// come from the one GET /accounts call — that listing already carries
// every account's own balance (GET /accounts/{id} alone doesn't, see
// docs/architecture.md's own accounts section), so there's no need for
// a second, single-account fetch just for this page's own header.
async function loadAccount() {
  try {
    allAccounts = await apiFetch('/accounts');
    const account = allAccounts.find((a) => a.id === accountId);
    if (!account) {
      showError('Esta cuenta no existe.');
      return;
    }
    renderHeader(account);
    transactionLines.nt.setLines([]);
    refreshDuesLicensesSections('nt');
  } catch {
    showError('No se pudo cargar la cuenta.');
  }
}

// The transaction shape shared by creating a new one and correcting an
// existing one. Each line is an amount for THIS account plus the other
// account it comes from or goes to: this account's own entry is the sum
// of every amount, each other account's its line's exact negative, so the
// entries already sum to zero without whoever's filling this in ever
// having to think about the balancing side. The sum is added up in whole
// cents — 0.1 + 0.2 in floating point is 0.30000000000000004, not an
// amount anyone typed.
function entriesBody(dateValue, description, lines) {
  const totalCents = lines.reduce((sum, line) => sum + Math.round(line.amount * 100), 0);
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
      { account_id: accountId, amount: totalCents / 100 },
      ...lines.map((line) => ({ account_id: line.accountId, amount: -line.amount })),
    ],
  };
}

// A money amount with an explicit "+" when it's positive, so which way it
// moved reads at a glance (formatCurrency already carries the "-").
function signedCurrency(amount) {
  return `${amount > 0 ? '+' : ''}${formatCurrency(amount)}`;
}

function renderEntryRow(entry) {
  return `
    <tr class="entry-row" data-entry-row="${entry.transaction_id}" tabindex="0" aria-expanded="false">
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

// The ledger is read a page at a time (the newest ENTRIES_PAGE_SIZE
// first, "Ver más" for the next ones), optionally within a Desde/Hasta
// range, since after a few years an account holds thousands of entries.
// One more than a page is requested each time purely to know whether a
// "Ver más" is worth showing, without a separate count endpoint.
const ENTRIES_PAGE_SIZE = 50;
const entriesFromInput = document.getElementById('entries-from');
const entriesToInput = document.getElementById('entries-to');
const entriesLoadMore = document.getElementById('entries-load-more');
const entriesTableBody = document.getElementById('entries-table-body');
let entriesLoaded = 0;
let entriesFilter = { from: '', to: '' };

function entriesQuery(offset) {
  const params = new URLSearchParams({ limit: ENTRIES_PAGE_SIZE + 1, offset });
  if (entriesFilter.from) params.set('from', entriesFilter.from);
  if (entriesFilter.to) params.set('to', entriesFilter.to);
  return `/accounts/${accountId}/transactions?${params}`;
}

// Loads the first page again under the current filter, or — with
// append — the page after what's already shown.
async function loadEntries({ append = false } = {}) {
  if (!append) entriesLoaded = 0;
  entriesLoadMore.disabled = true;
  try {
    const fetched = await apiFetch(entriesQuery(entriesLoaded));
    const hasMore = fetched.length > ENTRIES_PAGE_SIZE;
    const page = fetched.slice(0, ENTRIES_PAGE_SIZE);
    const html = page.map(renderEntryRow).join('');
    if (append) {
      entriesTableBody.insertAdjacentHTML('beforeend', html);
    } else {
      const filtered = entriesFilter.from || entriesFilter.to;
      entriesTableBody.innerHTML = page.length
        ? html
        : `<tr><td colspan="5">${filtered ? 'No hay transacciones en ese rango de fechas.' : 'Todavía no hay transacciones en esta cuenta.'}</td></tr>`;
    }
    entriesLoaded += page.length;
    entriesLoadMore.hidden = !hasMore;
  } catch (err) {
    if (!append)
      entriesTableBody.innerHTML =
        '<tr><td colspan="5">No se pudieron cargar las transacciones.</td></tr>';
    entriesLoadMore.hidden = true;
    showError(err.message);
  } finally {
    entriesLoadMore.disabled = false;
  }
}

entriesLoadMore.addEventListener('click', () => loadEntries({ append: true }));

document.getElementById('entries-filter').addEventListener('submit', (event) => {
  event.preventDefault();
  if (
    entriesFromInput.value &&
    entriesToInput.value &&
    entriesToInput.value < entriesFromInput.value
  ) {
    showError('La fecha «Hasta» no puede ser anterior a «Desde».');
    return;
  }
  entriesFilter = { from: entriesFromInput.value, to: entriesToInput.value };
  loadEntries();
});

document.getElementById('entries-filter-clear').addEventListener('click', () => {
  entriesFromInput.value = '';
  entriesToInput.value = '';
  entriesFilter = { from: '', to: '' };
  loadEntries();
});

// The other accounts this transaction touched, each with what it added to
// (+) or took out of (−) that account. The ledger listing only ever
// carries this account's own side of each transaction (see
// AccountEntry's doc comment), so the full transaction is fetched the
// first time a row is opened; account names come from allAccounts,
// which this page already has.
function renderEntryDetail(transaction) {
  const others = transaction.entries.filter((e) => e.account_id !== accountId);
  let html = '';
  if (others.length) {
    const items = others
      .map((e) => {
        const account = allAccounts.find((a) => a.id === e.account_id);
        const name = account ? account.name : `Cuenta #${e.account_id}`;
        return `<li><a href="/cuenta?id=${e.account_id}">${escapeHtml(name)}</a><span class="balance-cell">${signedCurrency(e.amount)}</span></li>`;
      })
      .join('');
    html += `<div class="entry-detail-title">Otras cuentas de esta transacción (+ entra, − sale)</div><ul class="entry-detail-list">${items}</ul>`;
  } else {
    html += '<p class="entry-detail-title">Esta transacción no afecta a ninguna otra cuenta.</p>';
  }

  // The cuotas and licencias this transaction paid for. The API only
  // sends them to admin/board (see GET /transactions/{id}), so for anyone
  // else both are simply absent and nothing is listed.
  const personLink = (login, text) =>
    `<a href="/socio?login=${encodeURIComponent(login)}">${escapeHtml(personName(login))}</a> · ${escapeHtml(text)}`;
  const dues = transaction.dues ?? [];
  if (dues.length) {
    html += `<div class="entry-detail-title">Cuotas anuales</div><ul class="entry-detail-list">${dues
      .map((due) => `<li>${personLink(due.user_login, String(due.year))}</li>`)
      .join('')}</ul>`;
  }
  const licenses = transaction.licenses ?? [];
  if (licenses.length) {
    html += `<div class="entry-detail-title">Licencias</div><ul class="entry-detail-list">${licenses
      .map(
        (license) =>
          `<li>${personLink(license.user_login, `${license.type} · ${license.year}`)}</li>`,
      )
      .join('')}</ul>`;
  }
  return html;
}

// Expands (or collapses) the row right below the clicked one. The detail
// row is created empty first and filled in once the fetch answers, so
// the click feels instant; closing it while that's still loading just
// detaches it, and the late answer lands on a node nobody sees.
async function toggleEntryDetail(row) {
  const existing = row.nextElementSibling;
  if (existing?.classList.contains('entry-detail')) {
    existing.remove();
    row.setAttribute('aria-expanded', 'false');
    return;
  }
  const detail = document.createElement('tr');
  detail.className = 'entry-detail';
  // An empty first cell, so the detail lines up under "Descripción" rather
  // than starting at the row's left edge, where it would read as another
  // row of the table instead of the continuation of the one above it.
  detail.innerHTML = '<td></td><td colspan="4">Cargando…</td>';
  row.after(detail);
  row.setAttribute('aria-expanded', 'true');
  try {
    const transaction = await apiFetch(`/transactions/${row.dataset.entryRow}`);
    detail.lastElementChild.innerHTML = renderEntryDetail(transaction);
  } catch {
    detail.lastElementChild.textContent = 'No se pudo cargar el detalle de esta transacción.';
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
// is one shared dialog, not one per row. Each of the transaction's other
// entries becomes a line (its amount negated back into "cantidad en esta
// cuenta"); a transaction with more than one entry on THIS account (only
// possible by calling the API directly) is reported instead of silently
// merged into one, and the dialog never opens for it.
async function openEditTransactionDialog(transactionId) {
  try {
    const transaction = await apiFetch(`/transactions/${transactionId}`);
    const ownEntries = transaction.entries.filter((e) => e.account_id === accountId);
    const otherEntries = transaction.entries.filter((e) => e.account_id !== accountId);
    if (ownEntries.length !== 1 || !otherEntries.length) {
      showError(
        'Esta transacción tiene más de un apunte en esta cuenta, o ninguno: corrígela por la API — aquí cada línea es una otra cuenta.',
      );
      return;
    }
    editingTransactionId = transactionId;
    document.getElementById('et-date').value = transaction.occurred_at.slice(0, 10);
    editTransactionDuesLicenses?.syncYears();
    document.getElementById('et-description').value = transaction.description;
    transactionLines.et.setLines(
      otherEntries.map((e) => ({ amount: -e.amount, accountId: e.account_id })),
    );
    refreshDuesLicensesSections('et');
    // GET /transactions/{id} also says which cuotas and licencias this
    // transaction already paid for, but only to admin/board — read-only
    // here (see setupDuesAndLicensesSection's setExisting). Absent for
    // anyone else, who never has these sections at all.
    editTransactionDuesLicenses?.setExisting({
      dues: transaction.dues ?? [],
      licenses: transaction.licenses ?? [],
    });
    openModal(editTransactionDialog);
    document.getElementById('et-description').focus();
  } catch (err) {
    showError(err.message);
  }
}

editTransactionForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  const dateValue = document.getElementById('et-date').value;
  const description = document.getElementById('et-description').value.trim();

  const submitButton = editTransactionForm.querySelector('button[type="submit"]');
  submitButton.disabled = true;
  try {
    const lines = transactionLines.et.read();
    await apiFetch(`/transactions/${editingTransactionId}`, {
      method: 'PATCH',
      body: JSON.stringify(entriesBody(dateValue, description, lines)),
    });
    // editingTransactionId itself never changes on a PATCH, so any
    // dues/licenses added here are tied to the same transaction the
    // edit just corrected — see setupDuesAndLicensesSection's own doc
    // comment on why this can only ever add to it, never replace what a
    // previous save already linked.
    const pendingErrors = editTransactionDuesLicenses
      ? await editTransactionDuesLicenses.submitPending(editingTransactionId, dateValue)
      : [];
    // Closed first: while a dialog is open, showError writes into it (see
    // api.js), and this message has to outlive it.
    editTransactionDialog.close();
    if (pendingErrors.length) {
      showError(`Transacción actualizada, pero no se pudo registrar: ${pendingErrors.join(' · ')}`);
    } else {
      showFeedback('Transacción actualizada.');
    }
    await loadAccount();
    await loadEntries();
  } catch (err) {
    showError(err.message);
  } finally {
    submitButton.disabled = false;
  }
});

async function deleteTransaction(transactionId) {
  if (
    !confirm('¿Borrar esta transacción? También se elimina el apunte que crea en la otra cuenta.')
  )
    return;
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
    return;
  }

  // Anywhere else on a row — never on the buttons above, nor a link
  // inside an already expanded detail (that's another row entirely).
  const row = event.target.closest('tr.entry-row');
  if (row && !event.target.closest('a, button')) await toggleEntryDetail(row);
});

// The same for the keyboard: a focused row (tabindex="0") opens with
// Enter or Space, as a button would.
document.getElementById('entries-table-body').addEventListener('keydown', async (event) => {
  if (event.key !== 'Enter' && event.key !== ' ') return;
  if (!event.target.matches('tr.entry-row')) return;
  event.preventDefault();
  await toggleEntryDetail(event.target);
});

const newTransactionDialog = document.getElementById('new-transaction-dialog');
const newTransactionForm = document.getElementById('new-transaction-form');
initModal(newTransactionDialog, newTransactionForm);
if (newTransactionDuesLicenses) {
  newTransactionDialog.addEventListener('close', () => newTransactionDuesLicenses.reset());
}

// Today in the viewer's own timezone, as the YYYY-MM-DD an <input
// type="date"> wants — toISOString() would be UTC's date, which is
// already tomorrow's for part of the evening anywhere east of it.
function todayAsInputValue() {
  const now = new Date();
  const pad = (n) => String(n).padStart(2, '0');
  return `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`;
}

document.getElementById('toggle-new-transaction').addEventListener('click', () => {
  document.getElementById('nt-date').value = todayAsInputValue();
  newTransactionDuesLicenses?.syncYears();
  transactionLines.nt.setLines([]);
  refreshDuesLicensesSections('nt');
  openModal(newTransactionDialog);
  document.getElementById('nt-date').focus();
});

// "n" opens the new-transaction dialog, as long as the keystroke isn't
// meant for something else: a modifier held (Ctrl+N and friends belong
// to the browser), a field being typed in, or a dialog already open.
document.addEventListener('keydown', (event) => {
  if (event.key !== 'n' || event.ctrlKey || event.metaKey || event.altKey) return;
  if (event.target.closest('input, textarea, select, [contenteditable]')) return;
  if (document.querySelector('dialog[open]')) return;
  event.preventDefault();
  document.getElementById('toggle-new-transaction').click();
});

// The date, the description and the lines (see setupEntryLines) drive the
// transaction itself — this account's entry plus one per line: see
// entriesBody. Any pending dues/licenses (admin-only, see
// setupDuesAndLicensesSection) are sent afterward, once the new
// transaction's own id is known.
newTransactionForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  const dateValue = document.getElementById('nt-date').value;
  const description = document.getElementById('nt-description').value.trim();

  const submitButton = newTransactionForm.querySelector('button[type="submit"]');
  submitButton.disabled = true;
  try {
    const lines = transactionLines.nt.read();
    const transaction = await apiFetch('/transactions', {
      method: 'POST',
      body: JSON.stringify(entriesBody(dateValue, description, lines)),
    });
    const pendingErrors = newTransactionDuesLicenses
      ? await newTransactionDuesLicenses.submitPending(transaction.id, dateValue)
      : [];
    // Closed first: see the same note in the edit handler above.
    newTransactionDialog.close();
    if (pendingErrors.length) {
      showError(`Transacción creada, pero no se pudo registrar: ${pendingErrors.join(' · ')}`);
    } else {
      showFeedback('Transacción creada.');
    }
    await loadAccount();
    await loadEntries();
  } catch (err) {
    showError(err.message);
  } finally {
    submitButton.disabled = false;
  }
});

// Independent requests: all go out at once.
await Promise.all([loadAccount(), loadEntries(), loadCatalogs()]);
