import { apiFetch, escapeHtml, formatCurrency, showError, showFeedback } from './api.js';
import { articleLeft, hasArticles, resolvePickerId } from './distributionCommon.js';

// The select of each option of a distribution (to choose what is asked for), with the ids
// `<prefix>-<n>`; `selected` has the values already chosen.
export function optionSelects(options, prefix, selected = {}) {
  return options
    .map(
      (option, i) => `
      <div class="field">
        <label for="${prefix}-${i}">${escapeHtml(option.name)}</label>
        <select id="${prefix}-${i}" data-option="${escapeHtml(option.name)}" required>
          <option value="">— Elige —</option>
          ${option.values.map((v) => `<option${selected[option.name] === v ? ' selected' : ''}>${escapeHtml(v)}</option>`).join('')}
        </select>
      </div>`,
    )
    .join('');
}

// What is chosen in the selects drawn by optionSelects, by option name.
export function readChoices(container) {
  const choices = {};
  container.querySelectorAll('select[data-option]').forEach((s) => {
    choices[s.dataset.option] = s.value;
  });
  return choices;
}

// For a distribution for all members nobody has to sign up: each member's own unit is already
// there, and what is left is choosing its options and, if there are any, asking for extras. With
// nothing to choose, everybody's own order is already there and nothing can be done to it, so what
// is left to ask for is extras only: no "Es un extra" checkbox to tick, the button says what it does.
export function extrasOnly(distribution) {
  return (
    distribution.audience === 'all_members' &&
    distribution.options.length === 0 &&
    distribution.extra_price != null
  );
}

// The form to ask for something in a distribution, the same on /reparto (for oneself or, in "A nombre
// de", for another member) and on /reparto-gestion (always for somebody else: `personRequired`).
// `form` is the <form> it fills in; `getDistribution` and `getPeople` give what it draws from when it is
// drawn again by `render()` (the people are what `loadPeople` returns); `base` is the distribution's
// path; `onOrdered` is called after something was asked for (also after part of it went through) and
// `describeError` says how an error of the API reads (the page knows the distribution's own words).
export function mountOrderForm({
  form,
  base,
  getDistribution,
  getPeople,
  personRequired = false,
  onOrdered,
  describeError = (err) => err.message,
}) {
  const person = personRequired
    ? `<label for="of-person">A nombre de</label>
            <input id="of-person" type="text" list="of-person-options" autocomplete="off" placeholder="Escribe un nombre…" required>`
    : `<label for="of-person">A nombre de <span class="optional">(opcional: solo si pides para otro socio)</span></label>
            <input id="of-person" type="text" list="of-person-options" autocomplete="off" placeholder="Escribe un nombre…">`;
  form.innerHTML = `
        <div class="field-grid">
          <div class="field span-2">
            ${person}
            <datalist id="of-person-options"></datalist>
          </div>
          <div id="of-options" class="span-2 field-grid"></div>
          <div id="of-articles" class="field-grid" style="grid-column: 1 / -1" hidden></div>
          <div class="field span-2" id="of-extra-field" hidden>
            <label class="checkbox-label" for="of-extra"><input id="of-extra" type="checkbox"> <span id="of-extra-label">Es un extra</span></label>
          </div>
        </div>
        <div class="panel-actions">
          <button type="submit" class="btn btn-primary">Pedir</button>
        </div>`;
  const personInput = form.querySelector('#of-person');
  const extraField = form.querySelector('#of-extra-field');
  const extraBox = form.querySelector('#of-extra');
  const submit = form.querySelector('button[type="submit"]');
  const fail = (err) => showError(describeError(err));

  // One quantity field for each article, with what it costs and how many are left. Whatever was
  // typed stays when it is drawn again (the numbers left change as people ask).
  function renderArticleFields(distribution) {
    const box = form.querySelector('#of-articles');
    const typed = {};
    box.querySelectorAll('input[data-article]').forEach((input) => {
      typed[input.dataset.article] = input.value;
    });
    box.innerHTML = distribution.articles
      .map((a) => {
        const left = articleLeft(a);
        const info = [
          a.price > 0 ? `${formatCurrency(a.price)} cada una` : 'gratis',
          left == null ? null : `quedan ${left}`,
          a.max_per_person == null ? null : `máximo ${a.max_per_person} por persona`,
        ]
          .filter(Boolean)
          .join(', ');
        return `
      <div class="field">
        <label for="of-article-${a.id}">${escapeHtml(a.name)} <span class="optional">(${info})</span></label>
        <input id="of-article-${a.id}" type="number" min="0" max="${Math.min(left ?? 100000, a.max_per_person ?? 100000)}" step="1" data-article="${a.id}" value="${typed[a.id] ?? 0}">
      </div>`;
      })
      .join('');
  }

  // Draws the fields the distribution needs: a quantity for each article, or the selects of its options
  // and, if it has extras, the checkbox.
  function render() {
    const distribution = getDistribution();
    form.querySelector('#of-person-options').innerHTML = getPeople()
      .entries.map((e) => `<option value="${escapeHtml(e.label)}"></option>`)
      .join('');
    const articles = hasArticles(distribution);
    form.querySelector('#of-articles').hidden = !articles;
    form.querySelector('#of-options').hidden = articles;
    if (articles) {
      renderArticleFields(distribution);
      form.querySelector('#of-options').innerHTML = '';
      extraField.hidden = true;
      submit.textContent = 'Pedir';
      return;
    }
    form.querySelector('#of-articles').innerHTML = '';
    form.querySelector('#of-options').innerHTML = optionSelects(distribution.options, 'of');
    extraField.hidden = distribution.extra_price == null || extrasOnly(distribution);
    submit.textContent = extrasOnly(distribution) ? 'Pedido extra' : 'Pedir';
    form.querySelector('#of-extra-label').textContent =
      `Es un extra (${formatCurrency(distribution.extra_price ?? 0)}${distribution.max_extra != null ? `, máximo ${distribution.max_extra} por socio` : ''})`;
  }

  // Asks for what was typed in each article's field, one request each, stopping at the first the API
  // refuses (what went through stays, and the lists are drawn again either way, with what is left now).
  async function askForArticles(beneficiary) {
    const wanted = [...form.querySelectorAll('#of-articles input[data-article]')]
      .map((input) => ({
        article_id: Number(input.dataset.article),
        quantity: Number(input.value),
      }))
      .filter((w) => w.quantity > 0);
    if (wanted.length === 0) {
      showError('Pon una cantidad en algún artículo.');
      return;
    }
    submit.disabled = true;
    let failure = null;
    for (const w of wanted) {
      try {
        await apiFetch(`${base}/units`, {
          method: 'POST',
          body: JSON.stringify({ beneficiary_login: beneficiary, ...w }),
        });
        form.querySelector(`#of-article-${w.article_id}`).value = 0;
      } catch (err) {
        failure = err;
        break;
      }
    }
    await onOrdered();
    submit.disabled = false;
    if (failure) {
      fail(failure);
      return;
    }
    showFeedback('Pedido apuntado.');
    personInput.value = '';
  }

  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    const distribution = getDistribution();
    let beneficiary = null;
    if (personInput.value.trim()) {
      beneficiary = resolvePickerId(getPeople().entries, personInput.value);
      if (!beneficiary) {
        showError(
          personRequired
            ? 'Elige a una persona de la lista.'
            : 'Elige a una persona de la lista, o deja el campo vacío para pedirlo para ti.',
        );
        return;
      }
    }
    if (hasArticles(distribution)) {
      await askForArticles(beneficiary);
      return;
    }
    submit.disabled = true;
    try {
      await apiFetch(`${base}/units`, {
        method: 'POST',
        body: JSON.stringify({
          beneficiary_login: beneficiary,
          is_extra: extrasOnly(distribution) || extraBox.checked,
          choices: readChoices(form.querySelector('#of-options')),
        }),
      });
      showFeedback('Pedido apuntado.');
      personInput.value = '';
      extraBox.checked = false;
      await onOrdered();
    } catch (err) {
      fail(err);
    } finally {
      submit.disabled = false;
    }
  });

  return { render };
}
