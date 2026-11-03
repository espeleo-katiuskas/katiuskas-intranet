import {
  apiFetch,
  hasAnyRole,
  isNotFound,
  queryId,
  showError,
  showFeedback,
  stopAsNotFound,
} from './api.js';
import { initPage } from './layout.js';
import { initModal, openModal } from './modal.js';
import {
  STATUS_ORDER,
  apiErrorText,
  bodyFrom,
  describeDistribution,
  distributionStatusLabels,
  fillDistributionForm,
  loadAccountOptions,
  readDistributionForm,
  syncArticleFields,
} from './distributionCommon.js';
import { initNumbers } from './distribution-numbers.js';
import { initOrders } from './distribution-orders.js';
import { initPayments } from './distribution-payments.js';

// Everything for the people who manage one distribution (admin, board and material) in one page: at
// the top what can be done to the distribution itself (take it to another state, edit it, delete
// it) and below, as sections, its orders, its payments and its numbers. It is reached with the
// "Gestionar este reparto" button of the distribution's own page.
const viewer = await initPage({ requireAnyRole: ['admin', 'board', 'material'] });
const canMarkPaid = hasAnyRole(viewer, ['admin', 'board']);
// A person's page is only open to admin and board: for material the name is plain text.
const canOpenPeople = canMarkPaid;

const id = queryId();
if (id === null) await stopAsNotFound();
const base = `/distributions/${id}`;
document.getElementById('back-link').href = `/reparto?id=${id}`;

let distribution = null;
const fail = (err) => showError(apiErrorText(err, distribution));

try {
  distribution = await apiFetch(base);
} catch (err) {
  if (isNotFound(err)) await stopAsNotFound();
  fail(err);
  throw err;
}

// Whether there is something to pay (so a list of payments makes sense) and some article has its
// numbers described (so there is something to list).
const hasPrices = () =>
  distribution.price > 0 ||
  (distribution.extra_price ?? 0) > 0 ||
  distribution.articles.some((a) => a.price > 0);
const hasNumbers = () => distribution.articles.some((a) => a.identifier_fields.length > 0);

// The sections: each one has `reload`, for when the distribution changes. What data-roles removed
// from the page (payments, for material) is not there to start.
const sections = [];

function renderHeader() {
  document.getElementById('distribution-title').textContent = distribution.name;
  document.title = `${distribution.name} — Gestionar — Club de Espeleología Katiuskas`;
  document.getElementById('distribution-meta').innerHTML = describeDistribution(distribution);
  document.getElementById('distribution-description').textContent = distribution.description ?? '';
  // Buttons restricted by data-roles are removed from the page for everybody else, hence the
  // optional chaining on the ones that might not be there.
  const index = STATUS_ORDER.indexOf(distribution.status);
  const back = document.getElementById('step-back');
  const forward = document.getElementById('step-forward');
  if (back && forward) {
    back.hidden = index <= 0;
    forward.hidden = index >= STATUS_ORDER.length - 1;
    if (index > 0) {
      back.textContent = `Volver a «${distributionStatusLabels[STATUS_ORDER[index - 1]]}»`;
    }
    if (index < STATUS_ORDER.length - 1) {
      forward.textContent = `Pasar a «${distributionStatusLabels[STATUS_ORDER[index + 1]]}»`;
    }
  }
  // Payments only where there is something to pay, numbers only where some article has them.
  for (const [name, shown] of [
    ['payments', hasPrices()],
    ['numbers', hasNumbers()],
  ]) {
    const section = document.getElementById(`section-${name}`);
    if (section) section.hidden = !shown;
    const link = document.getElementById(`index-${name}`);
    if (link) link.hidden = !shown;
  }
}

// The distribution changed here (state, data): draw the header and read every section again.
async function distributionChanged() {
  renderHeader();
  await Promise.all(sections.map((section) => section.reload(distribution)));
}

async function setStatus(status) {
  try {
    distribution = await apiFetch(base, {
      method: 'PATCH',
      body: JSON.stringify(bodyFrom(distribution, { status })),
    });
    // Leaving a draft is when the orders (everybody's own, if it is for all members) appear; going
    // back to one empties them: the orders section reads them again.
    await distributionChanged();
  } catch (err) {
    fail(err);
  }
}

// Staff-only controls: for anybody else they aren't on the page at all.
function onClick(elementId, handler) {
  document.getElementById(elementId)?.addEventListener('click', handler);
}

onClick('step-back', () => {
  const previous = STATUS_ORDER[STATUS_ORDER.indexOf(distribution.status) - 1];
  // Back to a draft starts it over: the API deletes every order, with what was chosen on it, so say
  // so when there is any.
  if (previous === 'draft' && distribution.unit_count > 0) {
    const count = distribution.unit_count === 1 ? '1 pedido' : `${distribution.unit_count} pedidos`;
    const again =
      distribution.audience === 'all_members'
        ? ' Al abrirlo otra vez se volverán a crear los de los socios, vacíos.'
        : '';
    if (
      !confirm(
        `Al volver a borrador se borrarán todos los pedidos (${count}), con lo que hayan elegido los socios, y no se podrán recuperar.${again} ¿Volver a borrador?`,
      )
    )
      return;
  }
  setStatus(previous);
});
onClick('step-forward', () => {
  const next = STATUS_ORDER[STATUS_ORDER.indexOf(distribution.status) + 1];
  if (
    next === 'closed' &&
    !confirm(
      'Al cerrar el reparto ya nadie podrá pedir ni cambiar nada (para añadir algo habría que volver a abrirlo). ¿Cerrarlo?',
    )
  )
    return;
  setStatus(next);
});

// ----- edit and delete -----
const editDialog = document.getElementById('edit-distribution-dialog');
const editForm = document.getElementById('edit-distribution-form');
initModal(editDialog);
const editAudience = document.getElementById('ed-audience');
const editCutoff = document.getElementById('ed-cutoff');
function syncEditCutoff() {
  const required = editAudience.value === 'all_members';
  editCutoff.required = required;
  document.getElementById('ed-cutoff-hint').textContent = required
    ? '(obligatorio)'
    : '(opcional: si la pones, solo podrán apuntarse los socios de ese día)';
}
editAudience.addEventListener('change', syncEditCutoff);
// With articles, the fields that only fit a single article are switched off.
editForm.elements.articles.addEventListener('input', () => {
  syncArticleFields(editForm);
  syncEditCutoff();
});

onClick('open-edit', async () => {
  await loadAccountOptions(editForm);
  fillDistributionForm(editForm, distribution);
  syncEditCutoff();
  openModal(editDialog);
});

editForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  // Turning "todos los socios" into "quien se apunte" takes the orders nobody touched with it (the
  // API deletes them).
  if (
    distribution.audience === 'all_members' &&
    editAudience.value === 'signup' &&
    distribution.unit_count > 0 &&
    !confirm(
      'Al pasarlo a «Quien se apunte» se borrarán los pedidos automáticos de los socios que no han elegido nada. Se quedan los que ya eligieron algo y los extras. ¿Seguir?',
    )
  ) {
    return;
  }
  const submit = editForm.querySelector('button[type="submit"]');
  submit.disabled = true;
  try {
    distribution = await apiFetch(base, {
      method: 'PATCH',
      body: JSON.stringify(readDistributionForm(editForm)),
    });
    editDialog.close();
    showFeedback('Reparto guardado.');
    await distributionChanged();
  } catch (err) {
    fail(err);
  } finally {
    submit.disabled = false;
  }
});

onClick('delete-distribution', async () => {
  // Say what is lost in plain words: each "pedido" is what one member asked for or has coming — in a
  // distribution for all members that includes everybody's own, which nobody asked for — and what
  // they chose goes with it.
  const units = distribution.unit_count;
  let lost = '';
  if (units > 0) {
    const count = units === 1 ? '1 pedido' : `${units} pedidos`;
    const verb = units === 1 ? 'se perderá' : 'se perderán';
    lost = ` Tiene ${count}, con lo que hayan elegido los socios, y ${verb} con él.`;
  }
  if (!confirm(`¿Borrar el reparto «${distribution.name}»?${lost} No se puede deshacer.`)) return;
  try {
    await apiFetch(base, { method: 'DELETE' });
    window.location.href = '/repartos';
  } catch (err) {
    fail(err);
  }
});

renderHeader();
// The sections ask for what they need at once; each one draws when its own data arrives.
const numbers = initNumbers({ base, canOpenPeople });
sections.push(numbers);
let payments = null;
const loading = [
  numbers.reload(distribution),
  initOrders({
    id,
    base,
    canMarkPaid,
    distribution,
    // A section read a fresher distribution (what is left of each article changes with the orders).
    setDistribution: (fresh) => {
      distribution = fresh;
      renderHeader();
    },
    // Paying, handing out or numbering in the orders changes the payments and the numbers.
    onOrdersChanged: () => {
      numbers.reload(distribution);
      payments?.reload();
    },
  }).then((orders) => sections.push(orders)),
];
if (canMarkPaid) {
  loading.push(
    initPayments({ base }).then((section) => {
      payments = section;
      sections.push(section);
    }),
  );
}
await Promise.all(loading);
