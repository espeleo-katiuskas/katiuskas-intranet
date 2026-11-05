import { apiFetch, escapeHtml, formatCurrency, showError, showFeedback } from './api.js';
import { initModal, openModal } from './modal.js';
import {
  apiErrorText,
  articleLeft,
  formatIdentifiers,
  loadPeople,
  resolvePickerId,
} from './distributionCommon.js';
import { createNumberPicker } from './numberPicker.js';

// A sale over the counter, for admin and board: a member asks for something and pays on the spot, so
// the order, its numbers, the payment and the hand-over are written down in a single dialog instead of
// going through the orders one step at a time. It is only for distributions with articles. It is
// opened with the "Venta directa" button of /reparto-gestion; what is returned has `update(distribution)`
// for when the distribution changes (the button is there only when something can be sold) and
// `onSold` is called after a sale, so the page reads its sections again.
export async function initSale({ id, base, distribution: first, onSold }) {
  let distribution = first;
  const button = document.getElementById('open-sale');
  const dialog = document.getElementById('sale-dialog');
  const form = document.getElementById('sale-form');
  if (!button || !dialog) return { update() {} };
  initModal(dialog, form);

  const personInput = document.getElementById('sd-person');
  const articleSelect = document.getElementById('sd-article');
  const quantityInput = document.getElementById('sd-quantity');
  const typedInput = document.getElementById('sd-typed-input');
  const paidBox = document.getElementById('sd-paid');
  const deliveredBox = document.getElementById('sd-delivered');
  const accountSelect = document.getElementById('sd-account');
  const submit = document.getElementById('sd-submit');
  const LAST_ACCOUNT_KEY = 'katiuskas:distribution-pay-account';

  let people = { entries: [], names: {} };
  let assetAccounts = null;
  const picker = createNumberPicker({
    box: document.getElementById('sd-chips'),
    filter: document.getElementById('sd-filter'),
    count: document.getElementById('sd-count'),
  });

  // Only a distribution with articles can be sold from, and not while it is a draft.
  function update(fresh) {
    distribution = fresh;
    button.hidden = distribution.status === 'draft' || (distribution.articles ?? []).length === 0;
  }
  update(distribution);

  const article = () => distribution.articles.find((a) => a.id === Number(articleSelect.value));
  const quantity = () => Number(quantityInput.value);
  const described = (a) => (a?.identifier_fields ?? []).length > 0;

  function rememberedAccount() {
    try {
      return localStorage.getItem(LAST_ACCOUNT_KEY);
    } catch {
      return null;
    }
  }

  async function loadAccounts() {
    if (assetAccounts) return;
    const all = await apiFetch('/accounts');
    const groups = new Set(all.map((a) => a.parent_id));
    assetAccounts = all.filter(
      (a) => a.effective_type === 'asset' && !a.hidden && !groups.has(a.id),
    );
  }

  // The free numbers of the article, as the API says now.
  async function freeNumbersOf(a) {
    const numbers = await apiFetch(`${base}/articles/${a.id}/numbers`);
    return {
      fields: numbers.fields.map((f) => f.name),
      free: numbers.items.filter((item) => !item.beneficiary_login).map((item) => item.identifier),
    };
  }

  // What the chosen article needs from the dialog: the buttons for its numbers, the field for typing
  // them, and the money part only if it costs something.
  async function showArticle() {
    const a = article();
    const priced = a.price > 0;
    document.getElementById('sd-money').hidden = !priced;
    document.getElementById('sd-account-field').hidden = !priced || !paidBox.checked;
    document.getElementById('sd-numbers').hidden = !described(a);
    document.getElementById('sd-typed').hidden = !(a.has_identifiers && !described(a));
    updateTotal();
    if (described(a)) {
      try {
        picker.start({ ...(await freeNumbersOf(a)), max: Math.max(quantity() || 1, 1) });
      } catch (err) {
        showError(apiErrorText(err, distribution));
      }
    }
  }

  function updateTotal() {
    const a = article();
    const q = quantity();
    const left = articleLeft(a);
    const parts = [];
    if (Number.isInteger(q) && q > 0 && a.price > 0) {
      parts.push(`Total: ${formatCurrency(a.price * q)}`);
    }
    if (left !== null) parts.push(`Quedan ${left}.`);
    document.getElementById('sd-total').textContent = parts.join(' · ');
  }

  articleSelect.addEventListener('change', showArticle);
  quantityInput.addEventListener('input', () => {
    updateTotal();
    if (described(article())) picker.setMax(Math.max(quantity() || 1, 1));
  });
  paidBox.addEventListener('change', () => {
    document.getElementById('sd-account-field').hidden = !paidBox.checked;
  });

  button.addEventListener('click', async () => {
    try {
      const [loaded] = await Promise.all([loadPeople(id, 'order'), loadAccounts()]);
      people = loaded;
    } catch (err) {
      showError(apiErrorText(err, distribution));
      return;
    }
    document.getElementById('sd-person-options').innerHTML = people.entries
      .map((e) => `<option value="${escapeHtml(e.label)}"></option>`)
      .join('');
    articleSelect.innerHTML = distribution.articles
      .map(
        (a) =>
          `<option value="${a.id}">${escapeHtml(a.name)}${a.price > 0 ? ` — ${escapeHtml(formatCurrency(a.price))}` : ''}</option>`,
      )
      .join('');
    // With a single article there is nothing to choose.
    document.getElementById('sd-article-field').hidden = distribution.articles.length === 1;
    const last = rememberedAccount();
    accountSelect.innerHTML = [
      '<option value="">— Elige una cuenta —</option>',
      ...assetAccounts.map(
        (a) =>
          `<option value="${a.id}"${String(a.id) === last ? ' selected' : ''}>${escapeHtml(a.name)}</option>`,
      ),
    ].join('');
    await showArticle();
    openModal(dialog);
    personInput.focus();
  });

  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    const login = resolvePickerId(people.entries, personInput.value);
    if (!login) {
      showError('Escribe un nombre de la lista de socios.');
      return;
    }
    const a = article();
    const paid = a.price > 0 && paidBox.checked;
    if (paid && !accountSelect.value) {
      showError('Elige la cuenta en la que se ha pagado.');
      return;
    }
    const marked = described(a) ? picker.marked() : [];
    const typed = typedInput.value
      .split(',')
      .map((n) => n.trim())
      .filter(Boolean);
    submit.disabled = true;
    try {
      const unit = await apiFetch(`${base}/sales`, {
        method: 'POST',
        body: JSON.stringify({
          beneficiary_login: login,
          article_id: a.id,
          quantity: quantity(),
          identifiers: described(a) ? marked : typed,
          paid,
          account_id: paid ? Number(accountSelect.value) : 0,
          delivered: deliveredBox.checked,
        }),
      });
      if (paid) {
        try {
          localStorage.setItem(LAST_ACCOUNT_KEY, accountSelect.value);
        } catch {
          // Best effort: the choice is only a convenience.
        }
      }
      dialog.close();
      // The numbers this sale gave are the last ones of the row: typed ones first, then the next free.
      const sold = (unit.identifiers ?? []).slice(-quantity());
      const numbers = sold.length ? `: ${formatIdentifiers(a, sold)}` : '';
      showFeedback(
        `Venta apuntada a ${people.names[login] ?? login} (${quantity()} × ${a.name}${numbers}).`,
      );
      await onSold();
    } catch (err) {
      showError(apiErrorText(err, distribution));
      // Somebody may have taken one of the numbers meanwhile.
      if (described(a)) {
        try {
          picker.refresh(await freeNumbersOf(a));
        } catch {
          // The error above says enough.
        }
      }
    } finally {
      submit.disabled = false;
    }
  });

  return { update };
}
