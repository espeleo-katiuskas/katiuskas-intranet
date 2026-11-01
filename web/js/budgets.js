import {
  apiFetch,
  escapeHtml,
  formatCurrency,
  hasAnyRole,
  showError,
  showFeedback,
} from './api.js';
import { initPage } from './layout.js';
import { accountLabel, childrenByParent } from './accountTree.js';
import { initModal, openModal } from './modal.js';

// Everybody who can sign in reads the budgets; only admin and money change them (the API is the
// authority, this only decides whether the buttons show).
const user = await initPage();
const canEdit = hasAnyRole(user, ['admin', 'money']);

const yearSelect = document.getElementById('year-select');
const noBudget = document.getElementById('no-budget');
const view = document.getElementById('budget-view');
const dialog = document.getElementById('budget-dialog');
const form = document.getElementById('budget-form');
initModal(dialog, form);

let years = []; // the years that have a budget, newest first
let budget = null; // the budget on screen, or null when its year has none
let accounts = []; // every account, as GET /accounts answers
let editor = { income: [], expenses: [] }; // the dialog's working copy: [{ name, amount, accountIds }]
const PARTS = ['income', 'expenses'];
const KIND = { income: 'income', expenses: 'expense' };

function percentText(percent) {
  return percent == null
    ? '—'
    : `${percent.toLocaleString('es-ES', { maximumFractionDigits: 2 })} %`;
}

function bar(percent) {
  const width = Math.max(0, Math.min(100, percent ?? 0));
  const over = percent != null && percent > 100 ? ' over' : '';
  return `<span class="budget-bar${over}"><span style="width:${width}%"></span></span>`;
}

function renderPart(table, part, label) {
  const rows = part.items
    .map(
      (item) => `<tr>
        <td>${escapeHtml(item.name)}</td>
        <td>${item.accounts.map((a) => escapeHtml(accountLabel(a))).join(', ')}</td>
        <td class="balance-cell">${formatCurrency(item.amount)}</td>
        <td class="balance-cell">${formatCurrency(item.actual)}</td>
        <td class="nowrap">${bar(item.percent)}${percentText(item.percent)}</td>
      </tr>`,
    )
    .join('');
  table.innerHTML = `<thead><tr><th>Partida</th><th>Cuentas</th><th class="nowrap">Presupuestado</th>
      <th class="nowrap">${label}</th><th class="nowrap">Alcanzado</th></tr></thead>
    <tbody>${rows || '<tr><td colspan="5">Sin partidas.</td></tr>'}</tbody>
    <tfoot><tr><th colspan="2">Total</th><th class="balance-cell">${formatCurrency(part.amount)}</th>
      <th class="balance-cell">${formatCurrency(part.actual)}</th>
      <th class="nowrap">${bar(part.percent)}${percentText(part.percent)}</th></tr></tfoot>`;
}

function renderBudget() {
  noBudget.hidden = budget !== null;
  view.hidden = budget === null;
  const year = Number(yearSelect.value);
  // Only admin and money have these buttons: applyRoleVisibility removes them from the page for
  // everybody else, so they may not exist.
  for (const id of ['edit-budget', 'delete-budget']) {
    const button = document.getElementById(id);
    if (button) button.hidden = !budget;
  }
  const createButton = document.getElementById('create-budget');
  if (createButton) createButton.hidden = false;
  if (!budget) {
    document.getElementById('no-budget-text').textContent = `No hay presupuesto de ${year}.`;
    return;
  }
  const warning = document.getElementById('unassigned-warning');
  warning.hidden = budget.unassigned_accounts.length === 0;
  if (!warning.hidden) {
    warning.textContent = `Hay cuentas que el presupuesto no recoge (se crearon después): ${budget.unassigned_accounts
      .map((a) => `${accountLabel(a)} (${formatCurrency(a.actual)})`)
      .join(', ')}.${canEdit ? ' Edita el presupuesto para añadirlas a una partida.' : ''}`;
  }
  document.getElementById('budget-totals').innerHTML = [
    ['Ingresos', budget.income],
    ['Gastos', budget.expenses],
  ]
    .map(
      ([name, part]) => `<div><span class="hint">${name}</span>
        <strong>${formatCurrency(part.actual)} de ${formatCurrency(part.amount)}</strong>
        ${bar(part.percent)}${percentText(part.percent)}</div>`,
    )
    .join('');
  renderPart(document.getElementById('income-table'), budget.income, 'Ingresado');
  renderPart(document.getElementById('expenses-table'), budget.expenses, 'Gastado');
}

async function loadYear(year) {
  try {
    budget = await apiFetch(`/budgets/${year}`);
  } catch (err) {
    if (err.code !== 'not_found') throw err;
    budget = null;
  }
  renderBudget();
}

function fillYearSelect(selected) {
  const current = new Date().getFullYear();
  const all = [...new Set([...years, current, current + 1, selected])].sort((a, b) => b - a);
  yearSelect.innerHTML = all
    .map((y) => `<option value="${y}"${y === selected ? ' selected' : ''}>${y}</option>`)
    .join('');
}

// --- the editor ---

const leaves = () => {
  const parents = new Set(accounts.map((a) => a.parent_id).filter((id) => id != null));
  return accounts.filter((a) => !parents.has(a.id));
};

function descendantLeaves(groupId) {
  const map = childrenByParent(accounts);
  const out = [];
  const walk = (id) => {
    for (const child of map.get(id) ?? []) {
      if (map.has(child.id)) walk(child.id);
      else out.push(child);
    }
  };
  walk(groupId);
  return out;
}

const usedIds = () => new Set(PARTS.flatMap((p) => editor[p].flatMap((i) => i.accountIds)));
const accountById = (id) => accounts.find((a) => a.id === id);

function addOptions(part) {
  const used = usedIds();
  const kind = KIND[part];
  const free = leaves().filter((a) => a.effective_type === kind && !used.has(a.id));
  const groups = accounts.filter(
    (g) =>
      g.effective_type === kind &&
      childrenByParent(accounts).has(g.id) &&
      descendantLeaves(g.id).some((a) => a.effective_type === kind && !used.has(a.id)),
  );
  return (
    '<option value="">Añadir cuenta…</option>' +
    groups
      .map((g) => `<option value="g${g.id}">Todas las de «${escapeHtml(accountLabel(g))}»</option>`)
      .join('') +
    free
      .map(
        (a) =>
          `<option value="a${a.id}">${escapeHtml(accountLabel(a))}${a.hidden ? ' (oculta)' : ''}</option>`,
      )
      .join('')
  );
}

function renderEditor() {
  for (const part of PARTS) {
    document.getElementById(`${part}-editor`).innerHTML = editor[part]
      .map(
        (item, index) => `<div class="budget-item" data-part="${part}" data-index="${index}">
          <input type="text" data-field="name" placeholder="Nombre de la partida" value="${escapeHtml(item.name)}" aria-label="Nombre">
          <input type="number" data-field="amount" min="0" step="0.01" placeholder="Importe" value="${item.amount}" aria-label="Importe">
          <div>${item.accountIds
            .map((id) => {
              const a = accountById(id);
              return `<span class="badge-removable">${escapeHtml(a ? accountLabel(a) : `#${id}`)}<button type="button" data-remove-account="${id}" title="Quitar">×</button></span>`;
            })
            .join('')}
            <select data-add-account aria-label="Añadir cuenta">${addOptions(part)}</select></div>
          <button type="button" class="btn btn-secondary btn-small" data-remove-item>Quitar</button>
        </div>`,
      )
      .join('');
  }
  updateSummary();
}

function sum(part) {
  return editor[part].reduce(
    (total, item) => total + Math.round(Number(item.amount || 0) * 100),
    0,
  );
}

function updateSummary() {
  const income = sum('income');
  const expenses = sum('expenses');
  const balance = document.getElementById('budget-balance');
  const balanced = income === expenses;
  balance.classList.toggle('bad', !balanced);
  balance.textContent = `Ingresos: ${formatCurrency(income / 100)} · Gastos: ${formatCurrency(expenses / 100)}${
    balanced ? '' : ` · Diferencia: ${formatCurrency(Math.abs(income - expenses) / 100)}`
  }`;
  const used = usedIds();
  const missing = leaves().filter(
    (a) => ['income', 'expense'].includes(a.effective_type) && !a.hidden && !used.has(a.id),
  );
  const box = document.getElementById('budget-missing');
  box.hidden = missing.length === 0;
  box.textContent = `Cuentas sin partida: ${missing.map(accountLabel).join(', ')}.`;
}

function openEditor(source) {
  editor = {
    income: source.income.items.map(toEditorItem),
    expenses: source.expenses.items.map(toEditorItem),
  };
  document.getElementById('budget-dialog-title').textContent = `Presupuesto de ${yearSelect.value}`;
  renderEditor();
  openModal(dialog);
}

const toEditorItem = (item) => ({
  name: item.name,
  amount: item.amount,
  accountIds: item.accounts.map((a) => a.id),
});

dialog.addEventListener('input', (event) => {
  const row = event.target.closest('.budget-item');
  if (!row || !event.target.dataset.field) return;
  editor[row.dataset.part][Number(row.dataset.index)][event.target.dataset.field] =
    event.target.value;
  updateSummary();
});

dialog.addEventListener('change', (event) => {
  const row = event.target.closest('.budget-item');
  if (!row || !('addAccount' in event.target.dataset) || !event.target.value) return;
  const item = editor[row.dataset.part][Number(row.dataset.index)];
  const kind = KIND[row.dataset.part];
  const used = usedIds();
  const value = event.target.value;
  const id = Number(value.slice(1));
  const toAdd =
    value[0] === 'g'
      ? descendantLeaves(id).filter((a) => a.effective_type === kind && !used.has(a.id))
      : [accountById(id)];
  item.accountIds.push(...toAdd.map((a) => a.id));
  renderEditor();
});

dialog.addEventListener('click', (event) => {
  const add = event.target.closest('[data-add-item]');
  if (add) {
    editor[add.dataset.addItem].push({ name: '', amount: '', accountIds: [] });
    renderEditor();
    return;
  }
  const row = event.target.closest('.budget-item');
  if (!row) return;
  const items = editor[row.dataset.part];
  const index = Number(row.dataset.index);
  if (event.target.closest('[data-remove-item]')) {
    items.splice(index, 1);
    renderEditor();
  }
  const removeAccount = event.target.closest('[data-remove-account]');
  if (removeAccount) {
    const id = Number(removeAccount.dataset.removeAccount);
    items[index].accountIds = items[index].accountIds.filter((x) => x !== id);
    renderEditor();
  }
});

form.addEventListener('submit', async (event) => {
  event.preventDefault();
  const body = {};
  for (const part of PARTS) {
    body[part] = editor[part].map((item) => ({
      name: item.name,
      amount: Number(item.amount || 0),
      account_ids: item.accountIds,
    }));
  }
  const year = Number(yearSelect.value);
  try {
    budget = await apiFetch(`/budgets/${year}`, { method: 'PUT', body: JSON.stringify(body) });
  } catch (err) {
    showError(err.message);
    return;
  }
  if (!years.includes(year)) years = [year, ...years].sort((a, b) => b - a);
  dialog.close();
  renderBudget();
  showFeedback(`Presupuesto de ${year} guardado.`);
});

// The structure a new budget starts from: the closest earlier year's, or else the closest later
// one's; with no budget at all, one empty part each.
async function templateFor(year) {
  const before = years.filter((y) => y < year).sort((a, b) => b - a)[0];
  const after = years.filter((y) => y > year).sort((a, b) => a - b)[0];
  const from = before ?? after;
  if (from == null) return { income: { items: [] }, expenses: { items: [] } };
  return apiFetch(`/budgets/${from}`);
}

document.getElementById('edit-budget')?.addEventListener('click', () => openEditor(budget));
document.getElementById('create-budget')?.addEventListener('click', async () => {
  openEditor(await templateFor(Number(yearSelect.value)));
});
document.getElementById('delete-budget')?.addEventListener('click', async () => {
  const year = Number(yearSelect.value);
  if (!confirm(`¿Borrar el presupuesto de ${year}?`)) return;
  try {
    await apiFetch(`/budgets/${year}`, { method: 'DELETE' });
  } catch (err) {
    showError(err.message);
    return;
  }
  years = years.filter((y) => y !== year);
  budget = null;
  renderBudget();
  showFeedback(`Presupuesto de ${year} borrado.`);
});

yearSelect.addEventListener('change', () => loadYear(Number(yearSelect.value)));

// Any year can be opened (and, with a year that has no budget, created): the selector only lists
// the years that have one plus this year and the next.
async function goToOtherYear() {
  const input = document.getElementById('year-other');
  const year = Number(input.value);
  if (!Number.isInteger(year) || year < 1900 || year > 2200) {
    showError('El año tiene que estar entre 1900 y 2200.');
    return;
  }
  input.value = '';
  fillYearSelect(year);
  await loadYear(year);
}
document.getElementById('year-go').addEventListener('click', goToOtherYear);
document.getElementById('year-other').addEventListener('keydown', (event) => {
  if (event.key === 'Enter') {
    event.preventDefault();
    goToOtherYear();
  }
});

const [summaries, accountList] = await Promise.all([apiFetch('/budgets'), apiFetch('/accounts')]);
years = summaries.map((s) => s.year);
accounts = accountList;
const current = new Date().getFullYear();
const initial = years.includes(current) ? current : (years[0] ?? current);
fillYearSelect(initial);
await loadYear(initial);
