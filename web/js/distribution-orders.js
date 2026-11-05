import {
  apiFetch,
  escapeHtml,
  formatCurrency,
  formatDate,
  showError,
  isNotFound,
  showNotFound,
  showFeedback,
} from './api.js';
import { initModal, openModal } from './modal.js';
import { mountOrderForm } from './distribution-order-form.js';
import { createNumberPicker } from './numberPicker.js';
import {
  apiErrorText,
  articleLeft,
  choicesText,
  formatDateTime,
  formatIdentifiers,
  sortIdentifiers,
  hasArticles,
  isUnchosen,
  nothingToAsk,
  loadPeople,
  resolvePickerId,
  unitCost,
} from './distributionCommon.js';

// Everything about a distribution's orders in one place, for the people who put the order together
// (admin, board) and hand it out (material): what has to be bought and, for each member, what they
// asked for, how it is numbered, what is paid and what was handed out — and the actions on all of
// it. It is a section of /reparto-gestion: `ctx` gives the distribution already loaded (and
// `setDistribution`, to say that a fresher one was read), who may mark payments and
// `onOrdersChanged`, called when something done here may have changed the other sections; what is
// returned has `reload(distribution)` for when the page changes the distribution.
export async function initOrders(ctx) {
  const { id, base, canMarkPaid, setDistribution, onOrdersChanged } = ctx;
  let distribution = ctx.distribution;
  let units = [];
  let people = { entries: [], names: {} };
  const fail = (err) => showError(apiErrorText(err));
  const nameOf = (login) => people.names[login] ?? login;

  const hasOptions = () => (distribution?.options.length ?? 0) > 0;
  const articleOf = (u) => distribution?.articles?.find((a) => a.id === u.article_id);
  // Items with a number of their own (free text or described), and the ones whose
  // numbers are all known so the next free can be given.
  const isNumbered = (u) => Boolean(articleOf(u)?.has_identifiers);
  const isDescribed = (u) => (articleOf(u)?.identifier_fields ?? []).length > 0;
  const canDeliver = () => distribution?.status !== 'draft';
  // Staff (this whole page) changes orders at any time but in a draft, as the API does.
  const canEditOrder = () => distribution?.status !== 'draft';

  // How a row stands. For a numbered article each item is paid or delivered by
  // itself, and only those with a number can be; elsewhere it is counted.
  const assigned = (u) => sortIdentifiers(u.identifiers);
  const paidIds = (u) => sortIdentifiers(u.paid_identifiers);
  const deliveredIds = (u) => sortIdentifiers(u.delivered_identifiers);
  const unpaidAssigned = (u) => assigned(u).filter((i) => !paidIds(u).includes(i));
  const returnableIds = (u) => deliveredIds(u).filter((i) => !paidIds(u).includes(i));

  // What can be paid, handed out or returned from a row, in units.
  const payable = (u) => (isNumbered(u) ? unpaidAssigned(u).length : u.quantity - u.paid_quantity);
  const returnable = (u) =>
    isNumbered(u)
      ? returnableIds(u).length
      : Math.min(u.picked_up_quantity, u.quantity - u.paid_quantity);
  const payableCost = (u) => payable(u) * u.price;
  // What can be handed out from a row, in units: the numbered ones not handed out yet (without a
  // number nothing is), or what is left of a count.
  const deliverable = (u) =>
    isNumbered(u)
      ? assigned(u).filter((i) => !deliveredIds(u).includes(i)).length
      : u.quantity - u.picked_up_quantity;
  const finished = (u) => u.paid_quantity === u.quantity && u.picked_up_quantity === u.quantity;

  // ----- what has to be bought -----
  let provisional = false;

  function renderArticleOrder(order) {
    document.getElementById('order-head').innerHTML =
      '<th>Artículo</th><th>Pedidas</th><th>Máximo</th><th>Quedan</th><th>Importe</th>';
    document.getElementById('order-lines').innerHTML = distribution.articles
      .map((a) => {
        const asked = order.lines
          .filter((l) => l.article_id === a.id)
          .reduce((sum, l) => sum + l.count, 0);
        const left = articleLeft(a);
        return `<tr><td>${escapeHtml(a.name)}</td><td>${asked}</td><td>${a.max_total ?? 'Sin límite'}</td><td>${left ?? '—'}</td><td>${formatCurrency(asked * a.price)}</td></tr>`;
      })
      .join('');
    document.getElementById('order-totals').textContent =
      `${order.total_units} unidades en total, por ${formatCurrency(order.total_amount)}.`;
  }

  function renderOrder(order) {
    if (hasArticles(distribution ?? {})) {
      renderArticleOrder(order);
      return;
    }
    provisional = order.provisional;
    const noChoices = (l) => Object.keys(l.choices).length === 0;
    document.getElementById('order-lines').innerHTML = order.lines.length
      ? order.lines
          .map((l) => {
            let label;
            if (!noChoices(l)) label = escapeHtml(choicesText(l.choices, hasOptions()));
            else label = hasOptions() ? '<strong>Sin elegir</strong>' : 'Sin nada que elegir';
            return `<tr><td>${label}</td><td>${l.count}</td></tr>`;
          })
          .join('')
      : '<tr><td colspan="2">Todavía no hay nada que pedir.</td></tr>';
    const missing = hasOptions()
      ? order.lines.filter(noChoices).reduce((sum, l) => sum + l.count, 0)
      : 0;
    const missingText =
      missing === 0
        ? ''
        : missing === 1
          ? ' Falta por elegir 1 pedido.'
          : ` Faltan por elegir ${missing} pedidos.`;
    const draftText = provisional
      ? ' Es un borrador: se cuenta un pedido por cada socio en la fecha de corte, y se crearán al abrir el reparto.'
      : '';
    document.getElementById('order-totals').textContent =
      `${order.total_units} pedidos en total, por ${formatCurrency(order.total_amount)}.${missingText}${draftText}`;
  }

  // ----- the orders of each member -----
  // One line for each order with a summary (how many, how many paid, how many handed
  // out) and, unfolded, the detail: the numbers to give it, a list of its numbered
  // items to choose from, and the actions.
  const list = document.getElementById('orders-list');
  const showDone = document.getElementById('show-done');

  // Orders unfolded, what is ticked in each (numbers), and what was typed in the
  // fields: all of it survives drawing the list again after any action.
  const unfolded = new Set();
  const ticked = new Map();
  const typed = new Map();

  const tickedOf = (u) => {
    if (!ticked.has(u.id)) ticked.set(u.id, new Set());
    return ticked.get(u.id);
  };

  const unitTitle = (u) => {
    const what = u.article_name
      ? `${u.article_name} × ${u.quantity}`
      : choicesText(u.choices, hasOptions());
    return u.is_extra ? `${what} (extra)` : what;
  };

  // The line of an order: what, how many are paid and handed out, what it costs.
  function summaryRow(u) {
    const article = articleOf(u);
    const extra = [];
    if (u.registered_by_login && u.registered_by_login !== u.beneficiary_login)
      extra.push(`apuntado por ${nameOf(u.registered_by_login)} el ${formatDate(u.registered_at)}`);
    if (isUnchosen(u, hasOptions())) extra.push('sin elegir');
    const numbers = isNumbered(u)
      ? `<div class="hint">${assigned(u).length} de ${u.quantity} con número${assigned(u).length ? `: ${escapeHtml(formatIdentifiers(article, assigned(u)))}` : ''}</div>`
      : '';
    const open = unfolded.has(u.id);
    const paid = u.price > 0 ? `${u.paid_quantity} de ${u.quantity}` : '—';
    const cost = u.price > 0 ? formatCurrency(unitCost(u)) : 'Gratis';
    return `<tr class="order-row" data-order="${u.id}" tabindex="0" aria-expanded="${open}">
    <td>${escapeHtml(u.beneficiary_name)}</td>
    <td><strong>${escapeHtml(unitTitle(u))}</strong>${extra.length ? `<div class="hint">${escapeHtml(extra.join(' · '))}</div>` : ''}${numbers}</td>
    <td>${u.picked_up_quantity} de ${u.quantity}</td>
    <td>${paid}</td>
    <td>${cost}</td>
  </tr>`;
  }

  function button(act, u, label, cls = 'btn-secondary', title = '') {
    return `<button type="button" class="btn ${cls} btn-small" data-act="${act}" data-unit="${u.id}"${title ? ` title="${escapeHtml(title)}"` : ''}>${label}</button>`;
  }

  // The numbered items of an order, each with its state, to tick.
  function itemList(u) {
    const article = articleOf(u);
    const chosen = tickedOf(u);
    const items = assigned(u)
      .map((identifier) => {
        const badges = [
          paidIds(u).includes(identifier) ? '<span class="badge">pagado</span>' : '',
          deliveredIds(u).includes(identifier) ? '<span class="badge">entregado</span>' : '',
        ].join(' ');
        return `<label class="checkbox-label" style="display:inline-flex;gap:.35rem;align-items:center;white-space:nowrap">
        <input type="checkbox" data-item="${escapeHtml(identifier)}" data-unit="${u.id}"${chosen.has(identifier) ? ' checked' : ''}>
        ${escapeHtml(formatIdentifiers(article, [identifier]))} ${badges}
      </label>`;
      })
      .join('');
    return `<div class="order-items" style="display:flex;flex-wrap:wrap;gap:.35rem 1rem;max-height:14rem;overflow:auto;padding:.5rem;border:1px solid var(--border, #ddd);border-radius:6px">${items}</div>`;
  }

  // Which of the ticked numbers an action is about: the ones it would really do something to (paying
  // what is already paid, say, is nothing). The same list says which buttons to show.
  const applicable = {
    pay: (u, i) => !paidIds(u).includes(i),
    unpay: (u, i) => paidIds(u).includes(i),
    deliver: (u, i) => !deliveredIds(u).includes(i),
    undeliver: (u, i) => deliveredIds(u).includes(i),
    return: (u, i) => deliveredIds(u).includes(i) && !paidIds(u).includes(i),
    remove: (u, i) => !paidIds(u).includes(i) && !deliveredIds(u).includes(i),
  };
  const tickedFor = (u, act) => [...tickedOf(u)].filter((i) => applicable[act](u, i));

  // The line "Con lo marcado": only when something is ticked, and only the buttons that would do
  // something to what is ticked.
  function markedActions(u) {
    if (tickedOf(u).size === 0) return '';
    const buttons = [];
    const can = (act) => tickedFor(u, act).length > 0;
    if (canMarkPaid && u.price > 0) {
      if (can('pay')) buttons.push(button('pay', u, 'Marcar pagados', 'btn-primary'));
      if (can('unpay')) buttons.push(button('unpay', u, 'Quitar pago'));
    }
    if (canDeliver()) {
      if (can('deliver')) buttons.push(button('deliver', u, 'Entregar', 'btn-primary'));
      if (can('undeliver'))
        buttons.push(
          button(
            'undeliver',
            u,
            'Deshacer entrega',
            'btn-secondary',
            'Vuelven a estar sin entregar, con su número',
          ),
        );
      if (can('return'))
        buttons.push(
          button(
            'return',
            u,
            'Devolver',
            'btn-danger',
            'Salen del pedido y su número queda libre (solo lo entregado y sin pagar)',
          ),
        );
    }
    if (can('remove'))
      buttons.push(
        button(
          'remove',
          u,
          'Quitar números',
          'btn-secondary',
          'Quita el número a lo marcado (si no está pagado ni entregado): las unidades siguen en el pedido, sin número, y el número queda libre',
        ),
      );
    return buttons.length ? `<span class="hint">Con lo marcado:</span> ${buttons.join(' ')}` : '';
  }

  function selectors(u) {
    const pick = (kind, label) =>
      `<button type="button" class="link-button" data-pick="${kind}" data-unit="${u.id}">${label}</button>`;
    const parts = [
      pick('all', 'todos'),
      pick('none', 'ninguno'),
      pick('unpaid', 'sin pagar'),
      pick('undelivered', 'sin entregar'),
    ];
    if (paidIds(u).length) parts.push(pick('paid', 'pagados'));
    if (deliveredIds(u).length) parts.push(pick('delivered', 'entregados'));
    return `<span class="hint">Marcar:</span> ${parts.join(' · ')} <span class="hint" data-count-for="${u.id}">${tickedOf(u).size} marcados</span>`;
  }

  // What the person can do with the order, in the order it is wanted: give it
  // numbers, choose among its numbered items, change the order itself.
  function detail(u) {
    const blocks = [];
    if (isNumbered(u)) {
      const missing = u.quantity - assigned(u).length;
      const field = typed.get(`add-${u.id}`) ?? '';
      if (missing > 0) {
        blocks.push(`<div class="table-actions"><strong>Sin número: ${missing}.</strong>
        ${isDescribed(u) ? `${button('next', u, `Asignar ${missing === 1 ? 'el siguiente libre' : `los ${missing} siguientes libres`}`, 'btn-primary')} ${button('choose', u, 'Elegir números…')}` : `<input type="text" size="26" data-add-for="${u.id}" value="${escapeHtml(field)}" placeholder="Números concretos" aria-label="Números concretos"> ${button('add', u, 'Asignar estos')}`}
      </div>`);
      }
      if (assigned(u).length) {
        blocks.push(
          `<div class="order-select-line">${selectors(u)}</div>${itemList(u)}<div class="table-actions" data-marked-for="${u.id}">${markedActions(u)}</div>`,
        );
      }
    } else {
      const count =
        u.quantity > 1
          ? `<input type="number" min="1" max="${u.quantity}" step="1" size="4" data-qty-for="${u.id}" value="${escapeHtml(typed.get(`qty-${u.id}`) ?? '')}" placeholder="todas" aria-label="Unidades">`
          : '';
      const pay =
        canMarkPaid && u.price > 0
          ? `${payable(u) > 0 ? button('pay', u, u.quantity > 1 ? 'Marcar pagadas' : 'Marcar pagado', 'btn-primary') : ''} ${u.paid_quantity > 0 ? button('unpay', u, u.quantity > 1 ? 'Quitar pagadas' : 'Quitar pago') : ''}`
          : '';
      const delivery = canDeliver()
        ? `${u.quantity - u.picked_up_quantity > 0 ? button('deliver', u, 'Entregar', 'btn-primary') : ''} ${u.picked_up_quantity > 0 ? button('undeliver', u, 'Deshacer entrega', 'btn-secondary', 'Vuelven a estar sin entregar') : ''} ${returnable(u) > 0 ? button('return', u, 'Devolver', 'btn-danger', 'Salen del pedido') : ''}`
        : '';
      blocks.push(`<div class="table-actions">${count} ${pay} ${delivery}</div>`);
    }
    const order = [];
    if (canEditOrder() && (u.picked_up_quantity === 0 || u.article_id)) {
      if (hasOptions() || u.article_id) order.push(button('change', u, 'Cambiar el pedido'));
      if (u.picked_up_quantity === 0 && (u.is_extra || distribution.audience === 'signup'))
        order.push(button('delete', u, 'Quitar el pedido', 'btn-danger'));
    }
    if (canMarkPaid && u.price > 0 && payableCost(u) > 0)
      order.push(button('payall', u, `Pagar todo (${formatCurrency(payableCost(u))})`));
    if (canDeliver() && deliverable(u) > 0)
      order.push(button('deliverall', u, 'Entregar todo', 'btn-secondary'));
    if (order.length)
      blocks.push(
        `<div class="table-actions"><span class="hint">Pedido:</span> ${order.join(' ')}</div>`,
      );

    const last = u.picked_up_at
      ? `<div class="hint">Última entrega: ${escapeHtml(formatDateTime(u.picked_up_at))}${u.picked_up_by_login === u.beneficiary_login ? '' : `, a ${escapeHtml(nameOf(u.picked_up_by_login))}`} (apuntado por ${escapeHtml(nameOf(u.pickup_recorded_by_login))})</div>`
      : '';
    return `<tr class="order-detail" data-detail="${u.id}"><td></td><td colspan="4">${last}${blocks.join('')}</td></tr>`;
  }

  function render() {
    const shown = units.filter((u) => showDone.checked || !finished(u));
    // What is ticked can only be what the order has, still.
    for (const u of units) {
      const chosen = tickedOf(u);
      for (const identifier of [...chosen])
        if (!assigned(u).includes(identifier)) chosen.delete(identifier);
    }
    list.innerHTML = shown.length
      ? shown.map((u) => summaryRow(u) + (unfolded.has(u.id) ? detail(u) : '')).join('')
      : `<tr><td colspan="5">${
          units.length
            ? 'Nada con ese filtro.'
            : provisional
              ? 'Los pedidos de cada socio se crean al abrir el reparto.'
              : 'Todavía no ha pedido nadie.'
        }</td></tr>`;

    const total = units.reduce((sum, u) => sum + u.quantity, 0);
    const paid = units.reduce((sum, u) => sum + u.paid_quantity, 0);
    const handedOut = units.reduce((sum, u) => sum + u.picked_up_quantity, 0);
    const numbered = units.filter(isNumbered).reduce((sum, u) => sum + assigned(u).length, 0);
    const numberedTotal = units.filter(isNumbered).reduce((sum, u) => sum + u.quantity, 0);
    const parts = [`${units.length} pedidos, ${total} unidades`];
    if (units.some((u) => u.price > 0)) parts.push(`pagadas ${paid}`);
    parts.push(`entregadas ${handedOut}`);
    if (numberedTotal) parts.push(`con número ${numbered} de ${numberedTotal}`);
    document.getElementById('orders-progress').textContent = units.length
      ? `${parts.join(' · ')}.`
      : '';
  }

  showDone.addEventListener('change', render);

  // ----- editing the order -----
  const unitDialog = document.getElementById('edit-unit-dialog');
  const unitForm = document.getElementById('edit-unit-form');
  initModal(unitDialog, unitForm);
  let changing = null;

  function optionSelects(choices = {}) {
    return distribution.options
      .map(
        (option, i) => `
      <div class="field">
        <label for="eu-${i}">${escapeHtml(option.name)}</label>
        <select id="eu-${i}" data-option="${escapeHtml(option.name)}" required>
          <option value="">— Elige —</option>
          ${option.values.map((v) => `<option${choices[option.name] === v ? ' selected' : ''}>${escapeHtml(v)}</option>`).join('')}
        </select>
      </div>`,
      )
      .join('');
  }

  function openChange(u) {
    changing = u;
    document.getElementById('edit-unit-hint').textContent = `Para ${u.beneficiary_name}.`;
    const byQuantity = Boolean(u.article_id);
    document.getElementById('edit-unit-title').textContent = byQuantity
      ? `Cambiar la cantidad de ${u.article_name}`
      : 'Cambiar lo elegido';
    document.getElementById('eu-quantity-field').hidden = !byQuantity;
    document.getElementById('eu-quantity').value = u.quantity;
    document.getElementById('eu-options').innerHTML = byQuantity ? '' : optionSelects(u.choices);
    openModal(unitDialog);
  }

  unitForm.addEventListener('submit', async (event) => {
    event.preventDefault();
    if (!changing) return;
    const choices = {};
    for (const select of document.querySelectorAll('#eu-options select[data-option]')) {
      choices[select.dataset.option] = select.value;
    }
    const body = changing.article_id
      ? { quantity: Number(document.getElementById('eu-quantity').value) }
      : { choices };
    try {
      await apiFetch(`${base}/units/${changing.id}`, {
        method: 'PATCH',
        body: JSON.stringify(body),
      });
      unitDialog.close();
      showFeedback('Cambio guardado.');
      await load();
    } catch (err) {
      fail(err);
    }
  });

  // ----- numbering: choosing the free numbers with the mouse -----
  // For an article whose numbers are described, the free ones are buttons to press (see
  // numberPicker.js); the order can take no more than the numbers it is missing.
  const numbersDialog = document.getElementById('numbers-dialog');
  const numbersForm = document.getElementById('numbers-form');
  initModal(numbersDialog, numbersForm);
  const picker = createNumberPicker({
    box: document.getElementById('nd-chips'),
    filter: document.getElementById('nd-filter'),
    count: document.getElementById('nd-count'),
    onChange: () => {
      document.getElementById('nd-submit').disabled = picker.size() === 0;
    },
  });
  // The order being numbered.
  let numbering = null;

  // The free numbers of the article, as the API says now.
  async function freeNumbersOf(article) {
    const numbers = await apiFetch(`${base}/articles/${article.id}/numbers`);
    return {
      fields: numbers.fields.map((f) => f.name),
      free: numbers.items.filter((item) => !item.beneficiary_login).map((item) => item.identifier),
    };
  }

  async function openNumbersDialog(u) {
    const article = articleOf(u);
    const missing = u.quantity - assigned(u).length;
    numbering = { u, article };
    const free = await freeNumbersOf(article);
    document.getElementById('nd-hint').textContent =
      `${u.beneficiary_name}: faltan ${missing === 1 ? 'un número' : `${missing} números`} de ${article.name}. Marca los que quieras darle (como máximo ${missing}).`;
    picker.start({ ...free, max: missing });
    openModal(numbersDialog);
  }

  document.getElementById('nd-clear').addEventListener('click', () => picker.clear());
  numbersForm.addEventListener('submit', async (event) => {
    event.preventDefault();
    if (!numbering || picker.size() === 0) return;
    const submit = document.getElementById('nd-submit');
    submit.disabled = true;
    try {
      await apiFetch(`${base}/units/${numbering.u.id}/identifiers`, {
        method: 'POST',
        body: JSON.stringify({ identifiers: picker.marked() }),
      });
      numbersDialog.close();
      showFeedback('Números asignados.');
      await load();
    } catch (err) {
      fail(err);
      // Somebody may have taken one meanwhile: what is free now, keeping what is still free of what
      // was marked.
      try {
        picker.refresh(await freeNumbersOf(numbering.article));
      } catch {
        // The error above says enough.
      }
    }
  });

  // ----- handing out: who gets it -----
  const deliverDialog = document.getElementById('deliver-dialog');
  const deliverForm = document.getElementById('deliver-form');
  const deliverPerson = document.getElementById('dd-person');
  initModal(deliverDialog, deliverForm);
  let delivering = null;

  deliverForm.addEventListener('submit', async (event) => {
    event.preventDefault();
    if (!delivering) return;
    const login = resolvePickerId(people.entries, deliverPerson.value);
    if (!login) {
      showError('Elige a la persona que recoge de la lista.');
      return;
    }
    const { u, withWhich, unpaid } = delivering;
    if (unpaid && !confirm('Hay unidades sin pagar. ¿Entregar igualmente?')) return;
    try {
      await apiFetch(`${base}/pickups`, {
        method: 'POST',
        body: JSON.stringify({
          deliveries: [{ unit_id: u.id, ...withWhich }],
          picked_up_by_login: login,
        }),
      });
      deliverDialog.close();
      showFeedback(`Entregado a ${nameOf(login)}.`);
      await load();
    } catch (err) {
      fail(err);
    }
  });

  // ----- paying: in which account -----
  // Every payment (and taking one off) is a transaction in the accounting, so the
  // asset account the money went through (or comes back from) is asked each time,
  // starting from the one used last.
  const payDialog = document.getElementById('pay-dialog');
  const payForm = document.getElementById('pay-form');
  const payAccount = document.getElementById('pd-account');
  const LAST_ACCOUNT_KEY = 'katiuskas:distribution-pay-account';
  initModal(payDialog, payForm);
  let paying = null;
  let assetAccounts = [];

  function rememberedAccount() {
    try {
      return localStorage.getItem(LAST_ACCOUNT_KEY);
    } catch {
      return null;
    }
  }

  function rememberAccount(value) {
    try {
      localStorage.setItem(LAST_ACCOUNT_KEY, value);
    } catch {
      // Best effort: the choice is only a convenience.
    }
  }

  // Asks for the account and then runs `run(accountId)`; with nothing to pay
  // (everything free) there is no money to move and it runs at once.
  async function withAccount(u, { title, hint, label, run, done }) {
    if (!(u.price > 0)) {
      await run(null);
      showFeedback(done);
      return true;
    }
    paying = { run, done };
    document.getElementById('pay-title').textContent = title;
    document.getElementById('pay-hint').textContent = hint;
    document.getElementById('pd-account-label').textContent = label;
    const last = rememberedAccount();
    payAccount.innerHTML = [
      '<option value="">— Elige una cuenta —</option>',
      ...assetAccounts.map(
        (a) =>
          `<option value="${a.id}"${String(a.id) === last ? ' selected' : ''}>${escapeHtml(a.name)}</option>`,
      ),
    ].join('');
    openModal(payDialog);
    return false;
  }

  payForm.addEventListener('submit', async (event) => {
    event.preventDefault();
    if (!paying || !payAccount.value) return;
    const { run, done } = paying;
    try {
      await run(Number(payAccount.value));
      rememberAccount(payAccount.value);
      payDialog.close();
      showFeedback(done);
      await load();
    } catch (err) {
      fail(err);
    }
  });

  // ----- the actions -----
  const capitalized = (text) => text.charAt(0).toUpperCase() + text.slice(1);
  const encodeList = (items) => encodeURIComponent(items.join(','));

  function quantityOf(u) {
    const field = list.querySelector(`input[data-qty-for="${u.id}"]`);
    if (!field || field.value.trim() === '') return null;
    return Number(field.value);
  }

  // What an action does, said already when something is missing; true when it
  // changed something and the list has to be read again.
  async function perform(act, u) {
    const numbered = isNumbered(u);
    const chosen = applicable[act] ? tickedFor(u, act) : [...tickedOf(u)];
    const quantity = numbered ? null : quantityOf(u);
    if (quantity !== null && (!Number.isInteger(quantity) || quantity < 1)) {
      showError('Pon cuántas unidades (un número entero de 1 en adelante), o déjalo en blanco.');
      return false;
    }
    // Which units an action is about: the ticked numbers, or a count.
    const needsTicks = ['pay', 'unpay', 'deliver', 'undeliver', 'return', 'remove'].includes(act);
    if (numbered && needsTicks && chosen.length === 0) {
      showError('Marca antes los números a los que se refiere.');
      return false;
    }
    const withWhich = numbered ? { identifiers: chosen } : quantity ? { quantity } : {};
    const which = numbered
      ? `${chosen.length} ${chosen.length === 1 ? 'unidad' : 'unidades'}`
      : quantity
        ? `${quantity} unidades`
        : 'todo lo que se pueda';
    switch (act) {
      case 'change':
        openChange(u);
        return false;
      case 'delete':
        if (!confirm('¿Quitar este pedido?')) return false;
        await apiFetch(`${base}/units/${u.id}`, { method: 'DELETE' });
        showFeedback('Pedido quitado.');
        return true;
      case 'add': {
        const text = (typed.get(`add-${u.id}`) ?? '')
          .split(/[,\n]/)
          .map((v) => v.trim())
          .filter(Boolean);
        if (text.length === 0) {
          showError('Escribe los números, por ejemplo «0001-0010, 0015».');
          return false;
        }
        await apiFetch(`${base}/units/${u.id}/identifiers`, {
          method: 'POST',
          body: JSON.stringify({ identifiers: text }),
        });
        typed.delete(`add-${u.id}`);
        showFeedback('Números asignados.');
        return true;
      }
      case 'choose':
        // A dialog does it: nothing to reload until it is accepted.
        await openNumbersDialog(u);
        return false;
      case 'next':
        await apiFetch(`${base}/units/${u.id}/identifiers/assign`, { method: 'POST' });
        showFeedback('Números asignados.');
        return true;
      case 'remove':
        if (
          !confirm(
            `¿Quitar el número a ${which}? Siguen en el pedido, sin número, y esos números quedarán libres.`,
          )
        )
          return false;
        await apiFetch(`${base}/units/${u.id}/identifiers?identifiers=${encodeList(chosen)}`, {
          method: 'DELETE',
        });
        for (const identifier of chosen) tickedOf(u).delete(identifier);
        showFeedback('Números quitados.');
        return true;
      case 'pay':
      case 'payall': {
        const payments =
          act === 'pay' ? { payments: [{ unit_id: u.id, ...withWhich }] } : { unit_ids: [u.id] };
        return withAccount(u, {
          title: 'Registrar el pago',
          hint: `${capitalized(act === 'pay' ? which : 'todo lo que se pueda')} de ${u.beneficiary_name}.`,
          label: 'Cuenta en la que se ha pagado',
          run: (accountId) =>
            apiFetch(`${base}/payments`, {
              method: 'POST',
              body: JSON.stringify({ ...payments, account_id: accountId }),
            }),
          done: 'Marcado como pagado.',
        });
      }
      case 'unpay': {
        const reversal = numbered ? { identifiers: chosen } : quantity ? { quantity } : {};
        if (
          !numbered &&
          !quantity &&
          u.quantity > 1 &&
          !confirm('¿Quitar todo lo pagado de este pedido?')
        )
          return false;
        return withAccount(u, {
          title: 'Quitar el pago',
          hint: `${capitalized(which)} de ${u.beneficiary_name}.`,
          label: 'Cuenta de la que se devuelve el dinero',
          run: (accountId) => {
            return apiFetch(`${base}/units/${u.id}/payment/reversal`, {
              method: 'POST',
              body: JSON.stringify({
                ...reversal,
                ...(accountId ? { account_id: accountId } : {}),
              }),
            });
          },
          done: 'Marca de pagado quitada.',
        });
      }
      case 'deliver': {
        // Who gets it is asked in a dialog; the delivery is made when it is accepted.
        const unpaid = numbered
          ? chosen.some((i) => !paidIds(u).includes(i))
          : u.paid_quantity < u.quantity;
        delivering = { u, withWhich, unpaid: u.price > 0 && unpaid };
        document.getElementById('deliver-hint').textContent = `${which} de ${u.beneficiary_name}.`;
        // The member the order is for is the natural default for who collects it.
        const owner = people.entries.find((e) => e.id === u.beneficiary_login);
        deliverPerson.value = owner ? owner.label : '';
        openModal(deliverDialog);
        return false;
      }
      case 'deliverall': {
        // Everything that can be handed out, with no ticking: the dialog asks who collects it.
        const ids = numbered ? assigned(u).filter((i) => !deliveredIds(u).includes(i)) : [];
        const unpaid = numbered
          ? ids.some((i) => !paidIds(u).includes(i))
          : u.paid_quantity < u.quantity;
        delivering = {
          u,
          withWhich: numbered ? { identifiers: ids } : {},
          unpaid: u.price > 0 && unpaid,
        };
        document.getElementById('deliver-hint').textContent =
          `Todo lo que se pueda entregar de ${u.beneficiary_name}.`;
        const owner = people.entries.find((e) => e.id === u.beneficiary_login);
        deliverPerson.value = owner ? owner.label : '';
        openModal(deliverDialog);
        return false;
      }
      case 'undeliver': {
        if (!confirm(`¿Deshacer la entrega de ${which}? Siguen en el pedido, sin entregar.`))
          return false;
        const query = numbered
          ? `?identifiers=${encodeList(chosen)}`
          : quantity
            ? `?quantity=${quantity}`
            : '';
        await apiFetch(`${base}/units/${u.id}/pickup${query}`, { method: 'DELETE' });
        showFeedback('Entrega deshecha.');
        return true;
      }
      case 'return': {
        if (
          !confirm(
            `¿Devolver ${which}? Salen del pedido y, si tienen número, ese número quedará libre. No se puede deshacer.`,
          )
        )
          return false;
        await apiFetch(`${base}/units/${u.id}/return`, {
          method: 'POST',
          body: JSON.stringify(withWhich),
        });
        for (const identifier of chosen) tickedOf(u).delete(identifier);
        showFeedback('Devolución apuntada.');
        return true;
      }
      default:
        return false;
    }
  }

  // Ticks the numbers of an order that have some state, in the page and in memory.
  function pick(u, kind) {
    const paid = paidIds(u);
    const delivered = deliveredIds(u);
    const rules = {
      all: () => true,
      none: () => false,
      unpaid: (i) => !paid.includes(i),
      undelivered: (i) => !delivered.includes(i),
      paid: (i) => paid.includes(i),
      delivered: (i) => delivered.includes(i),
    };
    const chosen = tickedOf(u);
    chosen.clear();
    for (const identifier of assigned(u)) if (rules[kind](identifier)) chosen.add(identifier);
    for (const box of list.querySelectorAll(`input[data-item][data-unit="${u.id}"]`)) {
      box.checked = chosen.has(box.dataset.item);
    }
    updateCount(u);
  }

  function updateCount(u) {
    const label = list.querySelector(`[data-count-for="${u.id}"]`);
    if (label) label.textContent = `${tickedOf(u).size} marcados`;
    const actions = list.querySelector(`[data-marked-for="${u.id}"]`);
    if (actions) actions.innerHTML = markedActions(u);
  }

  list.addEventListener('change', (event) => {
    const box = event.target.closest('input[data-item]');
    if (!box) return;
    const u = units.find((x) => x.id === Number(box.dataset.unit));
    if (box.checked) tickedOf(u).add(box.dataset.item);
    else tickedOf(u).delete(box.dataset.item);
    updateCount(u);
  });

  list.addEventListener('input', (event) => {
    const add = event.target.closest('input[data-add-for]');
    if (add) typed.set(`add-${add.dataset.addFor}`, add.value);
    const qty = event.target.closest('input[data-qty-for]');
    if (qty) typed.set(`qty-${qty.dataset.qtyFor}`, qty.value);
  });

  // Unfolds (or folds) an order's detail. The lines are drawn again, so the focus goes back to the
  // one that was used, for the keyboard.
  function toggleOrder(unitId, { focus = false } = {}) {
    if (unfolded.has(unitId)) unfolded.delete(unitId);
    else unfolded.add(unitId);
    render();
    if (focus) list.querySelector(`tr.order-row[data-order="${unitId}"]`)?.focus();
  }

  // The same for the keyboard: a focused line (tabindex="0") unfolds with Enter or Space.
  list.addEventListener('keydown', (event) => {
    if (event.key !== 'Enter' && event.key !== ' ') return;
    if (!event.target.matches('tr.order-row')) return;
    event.preventDefault();
    toggleOrder(Number(event.target.dataset.order), { focus: true });
  });

  list.addEventListener('click', async (event) => {
    const picker = event.target.closest('button[data-pick]');
    if (picker) {
      pick(
        units.find((x) => x.id === Number(picker.dataset.unit)),
        picker.dataset.pick,
      );
      return;
    }
    const b = event.target.closest('button[data-act]');
    if (!b) {
      // Anywhere else on an order's line unfolds it, as a line of the ledger does — but not what is
      // a control (the detail below is a row of its own).
      const row = event.target.closest('tr.order-row');
      if (row && !event.target.closest('a, button, input, select, label')) {
        toggleOrder(Number(row.dataset.order));
      }
      return;
    }
    const u = units.find((x) => x.id === Number(b.dataset.unit));
    b.disabled = true;
    try {
      if (await perform(b.dataset.act, u)) {
        typed.delete(`qty-${u.id}`);
        await load();
      } else {
        b.disabled = false;
      }
    } catch (err) {
      b.disabled = false;
      fail(err);
      // What failed may have changed what is left (numbers, units); draw it again.
      await load();
    }
  });

  // `ready` is what drawing needs besides this data (the people): the requests all
  // go out at once and only the drawing waits.
  async function load(ready = Promise.resolve(), { notify = true } = {}) {
    try {
      const requests = [apiFetch(`${base}/order`), apiFetch(`${base}/units`), ready];
      // With articles, what is left of each changes with the orders, so the
      // distribution is read again with them.
      if (distribution && hasArticles(distribution)) requests.push(apiFetch(base));
      // The accounts money can be paid into are read once, only by who can mark payments.
      if (canMarkPaid && assetAccounts.length === 0) {
        requests.push(
          apiFetch('/accounts').then((all) => {
            const groups = new Set(all.map((a) => a.parent_id));
            assetAccounts = all.filter(
              (a) => a.effective_type === 'asset' && !a.hidden && !groups.has(a.id),
            );
          }),
        );
      }
      const [order, all, , fresh] = await Promise.all(requests);
      if (fresh) {
        distribution = fresh;
        setDistribution(fresh);
      }
      units = all;
      renderOrder(order);
      render();
      renderOrderBox();
      // What was done here (paying, handing out, numbering) changes the other sections too.
      if (notify) onOrdersChanged?.();
    } catch (err) {
      if (isNotFound(err)) {
        showNotFound();
        return;
      }
      fail(err);
    }
  }

  // ----- asking in somebody's name -----
  // The same form as the member's own page, but always for somebody else: the name is required. Staff
  // ask at any time but in a draft, as the API does, and only the people who may have the
  // distribution are offered.
  let orderPeople = { entries: [], names: {} };
  const orderBox = document.getElementById('order-box');
  const orderForm = mountOrderForm({
    form: document.getElementById('order-form'),
    base,
    getDistribution: () => distribution,
    getPeople: () => orderPeople,
    personRequired: true,
    onOrdered: () => load(),
    describeError: (err) => apiErrorText(err, distribution),
  });

  function renderOrderBox() {
    orderBox.hidden =
      !distribution || distribution.status === 'draft' || nothingToAsk(distribution);
    if (orderBox.hidden) return;
    document.getElementById('order-status').textContent =
      distribution.status === 'open'
        ? ''
        : 'El reparto no está abierto, pero se puede pedir y cambiar pedidos igualmente.';
    orderForm.render();
  }

  const peopleRequest = loadPeople(id, 'pickup');
  const orderPeopleRequest = loadPeople(id, 'order');
  const firstLoad = load(peopleRequest, { notify: false });
  people = await peopleRequest;
  orderPeople = await orderPeopleRequest;
  document.getElementById('dd-person-options').innerHTML = people.entries
    .map((e) => `<option value="${escapeHtml(e.label)}"></option>`)
    .join('');
  await firstLoad;
  // The people of the form arrive after the first drawing of it.
  renderOrderBox();
  return {
    // The page changed the distribution (its state, its data): read the orders again.
    async reload(fresh) {
      distribution = fresh;
      await load(undefined, { notify: false });
    },
  };
}
