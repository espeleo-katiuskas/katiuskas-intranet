// Makes the column headers of a table that carry a data-sort-key sortable: a click (or Enter or
// Space, the headers take focus) picks the column, or turns it round if it already is the one; the
// header says which with ▲ / ▼ and aria-sort. It does not sort anything: `sort` is the page's own
// { key, desc } and onChange is where it re-sorts or reloads, as it has to for a list the server
// pages through. A header is
//   <th data-sort-key="last_used">Último uso<span class="sort-arrow" aria-hidden="true"></span></th>
export function initSortableHeaders(headRow, { sort, defaultDesc, onChange }) {
  const headers = [...headRow.querySelectorAll('th[data-sort-key]')];

  function refresh() {
    for (const th of headers) {
      const active = th.dataset.sortKey === sort.key;
      th.setAttribute('aria-sort', active ? (sort.desc ? 'descending' : 'ascending') : 'none');
      th.querySelector('.sort-arrow').textContent = active ? (sort.desc ? ' ▼' : ' ▲') : '';
    }
  }

  function choose(th) {
    const key = th.dataset.sortKey;
    if (sort.key === key) {
      sort.desc = !sort.desc;
    } else {
      sort.key = key;
      sort.desc = defaultDesc(key);
    }
    refresh();
    onChange();
  }

  for (const th of headers) {
    th.classList.add('sortable');
    th.tabIndex = 0;
    th.addEventListener('click', () => choose(th));
    th.addEventListener('keydown', (event) => {
      if (event.key === 'Enter' || event.key === ' ') {
        event.preventDefault();
        choose(th);
      }
    });
  }
  refresh();
}
