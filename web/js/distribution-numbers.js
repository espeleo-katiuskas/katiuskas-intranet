import { apiFetch, escapeHtml, isNotFound, showError, showNotFound } from './api.js';
import { apiErrorText } from './distributionCommon.js';

// Every number that exists of the articles whose numbers are described, who has each and how many
// are free: for the people who hand them out (admin, board, material). It is a section of
// /reparto-gestion; what is returned has `reload(distribution)`.
export function initNumbers({ base, canOpenPeople }) {
  const fail = (err) => showError(apiErrorText(err));

  // Past this many rows a table is hard to read and slow to draw: the filters
  // narrow it down.
  const MAX_ROWS = 500;

  const container = document.getElementById('numbers-articles');
  const state = new Map();

  function parts(item) {
    return item.identifier.split('/');
  }

  function matches(item, view) {
    if (view.show === 'free' && item.beneficiary_login) return false;
    if (view.show === 'taken' && !item.beneficiary_login) return false;
    const needle = view.search.trim().toLowerCase();
    if (!needle) return true;
    return `${item.identifier} ${item.beneficiary_name ?? ''}`.toLowerCase().includes(needle);
  }

  function renderRows(articleId) {
    const { numbers, view } = state.get(articleId);
    const shown = numbers.items.filter((item) => matches(item, view));
    const rows = shown.slice(0, MAX_ROWS).map((item) => {
      const cells = parts(item)
        .map((p) => `<td>${escapeHtml(p)}</td>`)
        .join('');
      const holder = item.beneficiary_login
        ? `${
            canOpenPeople
              ? `<a href="/socio?login=${encodeURIComponent(item.beneficiary_login)}">${escapeHtml(item.beneficiary_name)}</a>`
              : escapeHtml(item.beneficiary_name)
          } <span class="hint">${item.delivered ? '(entregado)' : '(asignado, sin entregar)'}</span>`
        : '<span class="hint">Libre</span>';
      return `<tr>${cells}<td>${holder}</td></tr>`;
    });
    const more =
      shown.length > MAX_ROWS
        ? `<tr><td colspan="${numbers.fields.length + 1}">Se muestran los ${MAX_ROWS} primeros de ${shown.length}: filtra para ver el resto.</td></tr>`
        : '';
    document.getElementById(`rows-${articleId}`).innerHTML =
      rows.join('') + more ||
      `<tr><td colspan="${numbers.fields.length + 1}">No hay ninguno con ese filtro.</td></tr>`;
  }

  function renderArticle(article, numbers) {
    const head = numbers.fields.map((f) => `<th>${escapeHtml(f.name)}</th>`).join('');
    return `
    <div class="section-header">
      <div>
        <h2>${escapeHtml(article.name)}</h2>
        <p class="subtitle">${numbers.assigned} asignados · <strong>${numbers.free} libres</strong> de ${numbers.total}</p>
      </div>
    </div>
    <div class="field-grid">
      <div class="field">
        <label for="show-${article.id}">Mostrar</label>
        <select id="show-${article.id}" data-show="${article.id}">
          <option value="all">Todos</option>
          <option value="free">Solo los libres</option>
          <option value="taken">Solo los asignados</option>
        </select>
      </div>
      <div class="field">
        <label for="search-${article.id}">Buscar <span class="optional">(un número o un nombre)</span></label>
        <input id="search-${article.id}" type="search" data-search="${article.id}" autocomplete="off">
      </div>
    </div>
    <div class="table-wrap">
      <table>
        <thead><tr>${head}<th>Quién lo tiene</th></tr></thead>
        <tbody id="rows-${article.id}"></tbody>
      </table>
    </div>`;
  }

  container.addEventListener('change', (event) => {
    const select = event.target.closest('select[data-show]');
    if (!select) return;
    state.get(Number(select.dataset.show)).view.show = select.value;
    renderRows(Number(select.dataset.show));
  });
  container.addEventListener('input', (event) => {
    const input = event.target.closest('input[data-search]');
    if (!input) return;
    state.get(Number(input.dataset.search)).view.search = input.value;
    renderRows(Number(input.dataset.search));
  });

  // Read again for the distribution it is given (its articles say which have numbers described).
  async function load(distribution) {
    try {
      const numbered = distribution.articles.filter((a) => a.identifier_fields.length > 0);
      const all = await Promise.all(
        numbered.map((a) => apiFetch(`${base}/articles/${a.id}/numbers`)),
      );
      container.innerHTML = numbered.length
        ? numbered.map((a, i) => renderArticle(a, all[i])).join('')
        : '<p>Ningún artículo de este reparto tiene sus números descritos.</p>';
      state.clear();
      numbered.forEach((a, i) => {
        state.set(a.id, { numbers: all[i], view: { show: 'all', search: '' } });
        renderRows(a.id);
      });
    } catch (err) {
      container.innerHTML = '';
      if (isNotFound(err)) showNotFound();
      else fail(err);
    }
  }

  return { reload: load };
}
