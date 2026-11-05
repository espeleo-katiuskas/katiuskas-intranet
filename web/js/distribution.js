import {
  apiFetch,
  currentUser,
  escapeHtml,
  formatCurrency,
  formatDate,
  hasAnyRole,
  showError,
  isNotFound,
  queryId,
  stopAsNotFound,
  showFeedback,
} from './api.js';
import { initPage } from './layout.js';
import { initModal, openModal } from './modal.js';
import { mountOrderForm, optionSelects, readChoices } from './distribution-order-form.js';
import {
  nothingToAsk,
  hasArticles,
  unitCost,
  unitLabel,
  apiErrorText,
  choicesText,
  describeDistribution,
  isUnchosen,
  loadPeople,
} from './distributionCommon.js';

await initPage();
const me = currentUser();
// Who the API treats as staff for orders: they ask for others without being eligible, go past the
// deadline, act while it is closed or done, and change what is paid.
const isStaff = hasAnyRole(me, ['admin', 'board', 'material']);

const id = queryId();
if (id === null) await stopAsNotFound();
const base = `/distributions/${id}`;
let distribution = null;
let units = [];
let people = { entries: [], names: {} };

const fail = (err) => showError(apiErrorText(err, distribution));
const nameOf = (login) => people.names[login] ?? login;

// Whether the signed-in person can ask for units now — the same rule the API
// applies: never in a draft; staff (admin, board, material) otherwise always; a member
// only while it is open and before the deadline.
function canOrderNow() {
  if (distribution.status === 'draft') return false;
  if (isStaff) return true;
  const beforeDeadline = !distribution.closes_at || new Date() < new Date(distribution.closes_at);
  return distribution.status === 'open' && beforeDeadline;
}

function renderHeader() {
  document.getElementById('distribution-title').textContent = distribution.name;
  document.title = `${distribution.name} — Club de Espeleología Katiuskas`;
  document.getElementById('distribution-meta').innerHTML = describeDistribution(distribution);
  document.getElementById('distribution-description').textContent = distribution.description ?? '';
  // Only who manages distributions has the button (data-roles removes it for everybody else).
  const manageLink = document.getElementById('manage-link');
  if (manageLink) manageLink.href = `/reparto-gestion?id=${id}`;
}

// ----- asking for units -----
const orderForm = document.getElementById('order-form');
const orderFormFields = mountOrderForm({
  form: orderForm,
  base,
  getDistribution: () => distribution,
  getPeople: () => people,
  onOrdered: () => loadUnits(),
  describeError: (err) => apiErrorText(err, distribution),
});

function allMembersHint() {
  const parts = ['Es para todos los socios: tu pedido ya está apuntado.'];
  if (distribution.options.length)
    parts.push('Elige lo que corresponda en «Tus pedidos» o, para otro socio, aquí abajo.');
  if (distribution.extra_price != null)
    parts.push(`Puedes pedir también extras a ${formatCurrency(distribution.extra_price)}.`);
  return parts.join(' ');
}

function renderOrderForm() {
  const status = document.getElementById('order-status');
  const eligible = isStaff || distribution.eligible;
  const open = canOrderNow();
  // A distribution for all members with nothing to choose and no extras has
  // nothing to ask for, ever: its own unit is there for everybody, so the
  // whole section is out — not even a "no longer admitting orders", since it
  // never did.
  const orderSection = document.getElementById('order-section');
  orderSection.hidden = nothingToAsk(distribution);
  if (orderSection.hidden) return;
  orderForm.hidden = !(open && eligible);
  if (!eligible) {
    status.textContent = distribution.cutoff_date
      ? `No te corresponde este reparto: solo es para quienes eran socios el ${formatDate(distribution.cutoff_date)}.`
      : 'No te corresponde este reparto.';
  } else if (!open) {
    status.textContent =
      distribution.status === 'open'
        ? 'Ya ha pasado la fecha límite para pedir.'
        : 'Ya no se admiten pedidos.';
  } else {
    status.textContent = hasArticles(distribution)
      ? 'Pon cuántas quieres de cada artículo (0 si no quieres), para ti o para otro socio. Si ya has pedido, lo que pidas ahora se suma a tu pedido (y puedes cambiarlo en «Tus pedidos»).'
      : distribution.audience === 'signup'
        ? 'Es para quien se apunte: pídelo para ti o para otro socio.'
        : allMembersHint();
  }
  if (open && eligible && isStaff && distribution.status !== 'open') {
    status.textContent = `El reparto no está abierto, pero tú puedes pedir y cambiar pedidos igualmente. ${status.textContent}`;
  }
  if (orderForm.hidden) return;
  orderFormFields.render();
}

// ----- the units shown -----
const unitsList = document.getElementById('units-list');

// Nothing about payment is shown yet: a unit is collected or it isn't.
const unitState = (u) => {
  if (u.picked_up_quantity === 0) return 'No recogido';
  return u.picked_up_quantity === u.quantity
    ? 'Recogido'
    : `Recogidas ${u.picked_up_quantity} de ${u.quantity}`;
};

function mineOnly(all) {
  // Admin, board and material get every unit from the API; this page is
  // about what the signed-in person asked for.
  return all.filter((u) => u.beneficiary_login === me.login || u.registered_by_login === me.login);
}

function canChange(u) {
  // What was handed out locks it, except for staff in a row of an article, which
  // can't go below what went.
  if (u.picked_up_quantity > 0 && (!isStaff || !u.article_id)) return false;
  if (u.paid_quantity > 0 && !isStaff) return false;
  return canOrderNow();
}

// The columns "Tus pedidos" needs: no "Elegido" when there is nothing to choose,
// and no "Tipo" (own or extra) when there can be no extras. The price is shown,
// but nothing about paying: that isn't something the intranet keeps.
function unitColumns() {
  const columns = [{ key: 'for', label: 'Para' }];
  if (hasArticles(distribution)) columns.push({ key: 'article', label: 'Artículo' });
  if (distribution.options.length > 0) columns.push({ key: 'chosen', label: 'Elegido' });
  if (distribution.extra_price != null) columns.push({ key: 'kind', label: 'Tipo' });
  columns.push(
    { key: 'price', label: 'Precio' },
    { key: 'entered', label: 'Apuntado' },
    { key: 'state', label: 'Estado' },
    { key: 'actions', label: '' },
  );
  return columns;
}

function unitCell(key, u, hasOptions) {
  const forMe = u.beneficiary_login === me.login;
  const byMe = u.registered_by_login === me.login;
  const unchosen = isUnchosen(u, hasOptions);
  switch (key) {
    case 'for':
      return escapeHtml(forMe ? 'Ti' : u.beneficiary_name);
    case 'article':
      return escapeHtml(unitLabel(u, false, distribution.articles));
    case 'chosen':
      return unchosen
        ? '<strong>Sin elegir</strong>'
        : escapeHtml(choicesText(u.choices, hasOptions));
    case 'kind':
      return u.is_extra ? 'Extra' : 'Propia';
    case 'price':
      return unitCost(u) > 0 ? formatCurrency(unitCost(u)) : 'Gratis';
    case 'entered':
      // Who entered it matters when it wasn't the person it is for. Nobody did
      // for an own unit of a distribution for all members until somebody chooses.
      if (!u.registered_by_login) return 'Automático';
      if (forMe && byMe) return formatDate(u.registered_at);
      return `${formatDate(u.registered_at)} por ${escapeHtml(byMe ? 'ti' : nameOf(u.registered_by_login))}`;
    case 'state':
      return unitState(u);
    default: {
      // An own unit of a distribution for all members is for everybody and
      // can't be removed (the API refuses); everything else can.
      const removable = u.is_extra || distribution.audience === 'signup';
      if (!canChange(u)) return '';
      return `<div class="table-actions">
        ${hasOptions || u.article_id ? `<button type="button" class="btn ${unchosen ? 'btn-primary' : 'btn-secondary'} btn-small" data-change-unit="${u.id}">${unchosen ? 'Elegir' : 'Cambiar'}</button>` : ''}
        ${removable ? `<button type="button" class="btn btn-danger btn-small" data-delete-unit="${u.id}">Quitar</button>` : ''}
      </div>`;
    }
  }
}

function renderUnits() {
  const mine = mineOnly(units);
  const columns = unitColumns();
  const hasOptions = distribution.options.length > 0;
  document.getElementById('units-head').innerHTML = columns
    .map((c) => `<th>${c.label}</th>`)
    .join('');
  unitsList.innerHTML = mine.length
    ? mine
        .map(
          (u) =>
            `<tr>${columns.map((c) => `<td>${unitCell(c.key, u, hasOptions)}</td>`).join('')}</tr>`,
        )
        .join('')
    : `<tr><td colspan="${columns.length}">Todavía no hay nada a tu nombre.</td></tr>`;
  const total = mine.reduce((sum, u) => sum + unitCost(u), 0);
  document.getElementById('units-total').textContent =
    total > 0 ? `Total de lo que has pedido: ${formatCurrency(total)}.` : '';
}

async function loadUnits() {
  try {
    // With articles, what is left of each changes as people ask, so the
    // distribution is read again with the units and everything drawn anew.
    if (hasArticles(distribution)) {
      [units, distribution] = await Promise.all([apiFetch(`${base}/units`), apiFetch(base)]);
      renderAll();
    } else {
      units = await apiFetch(`${base}/units`);
      renderUnits();
    }
  } catch (err) {
    unitsList.innerHTML = `<tr><td colspan="${distribution ? unitColumns().length : 4}">No se pudieron cargar los pedidos.</td></tr>`;
    fail(err);
  }
}

const unitDialog = document.getElementById('edit-unit-dialog');
const unitForm = document.getElementById('edit-unit-form');
initModal(unitDialog, unitForm);
let changing = null;

unitsList.addEventListener('click', async (event) => {
  const change = event.target.closest('button[data-change-unit]');
  const remove = event.target.closest('button[data-delete-unit]');
  if (change) {
    changing = units.find((u) => u.id === Number(change.dataset.changeUnit));
    document.getElementById('edit-unit-hint').textContent =
      `Para ${changing.beneficiary_login === me.login ? 'ti' : changing.beneficiary_name}.`;
    // A row of an article changes in how many; any other, in what was chosen.
    const byQuantity = Boolean(changing.article_id);
    document.getElementById('edit-unit-title').textContent = byQuantity
      ? `Cambiar la cantidad de ${changing.article_name}`
      : 'Cambiar lo elegido';
    document.getElementById('eu-quantity-field').hidden = !byQuantity;
    document.getElementById('eu-quantity').value = changing.quantity;
    document.getElementById('eu-options').innerHTML = byQuantity
      ? ''
      : optionSelects(distribution.options, 'eu', changing.choices);
    openModal(unitDialog);
  } else if (remove) {
    if (!confirm('¿Quitar este pedido?')) return;
    remove.disabled = true;
    try {
      await apiFetch(`${base}/units/${remove.dataset.deleteUnit}`, { method: 'DELETE' });
      showFeedback('Unidad quitada.');
      await loadUnits();
    } catch (err) {
      remove.disabled = false;
      fail(err);
    }
  }
});

unitForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  if (!changing) return;
  const body = changing.article_id
    ? { quantity: Number(document.getElementById('eu-quantity').value) }
    : { choices: readChoices(document.getElementById('eu-options')) };
  try {
    await apiFetch(`${base}/units/${changing.id}`, {
      method: 'PATCH',
      body: JSON.stringify(body),
    });
    unitDialog.close();
    showFeedback('Cambio guardado.');
    await loadUnits();
  } catch (err) {
    fail(err);
  }
});

function renderAll() {
  // While it is a draft nobody can ask for anything, so neither the form nor the
  // list of what has been asked for has a reason to be there.
  document.getElementById('ask-sections').hidden = distribution.status === 'draft';
  renderHeader();
  renderOrderForm();
  renderUnits();
}

// Both requests go out at once; the page needs the two to draw.
const peopleRequest = loadPeople(id, 'order');
const distributionRequest = apiFetch(base);
people = await peopleRequest;
try {
  distribution = await distributionRequest;
} catch (err) {
  if (isNotFound(err)) await stopAsNotFound();
  document.getElementById('distribution-title').textContent = 'No se ha podido cargar este reparto';
  fail(err);
  throw err;
}
renderAll();
if (distribution.status !== 'draft') await loadUnits();
