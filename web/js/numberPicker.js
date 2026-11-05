import { escapeHtml } from './api.js';

// Choosing free numbers with the mouse: each free number of an article whose numbers are described is
// a button to press, no more than `max` can be marked and a range is marked with Shift. Used wherever
// somebody has to say which numbers to give (the order's "Elegir números" and the sale over the
// counter). `box` is where the buttons are drawn, `filter` the search field, `count` where it says how
// many are marked and `onChange` is called after anything changes.
export function createNumberPicker({ box, filter, count, onChange = () => {} }) {
  // The free numbers (in the order the API gives them), the fields of the numbering, how many can be
  // marked, the marked ones, the last one pressed (where a range starts), the ones in view and what
  // the owner wants said after the count.
  let state = null;

  function render() {
    if (!state) return;
    const { free, fields, marked, max } = state;
    const needle = filter.value.trim().toLowerCase();
    const shown = free.filter((n) => !needle || n.toLowerCase().includes(needle));
    state.shown = shown;
    const full = marked.size >= max;
    const chip = (identifier, label) =>
      `<button type="button" class="number-chip" data-number="${escapeHtml(identifier)}" aria-pressed="${marked.has(identifier)}"${full && !marked.has(identifier) ? ' disabled' : ''}>${escapeHtml(label)}</button>`;
    if (shown.length === 0) {
      box.innerHTML = '<p class="hint">No hay ningún número libre con ese filtro.</p>';
    } else if (fields.length > 1) {
      // A row for each first part (a series), with the rest of the number on its buttons.
      const groups = new Map();
      for (const identifier of shown) {
        const [first, ...rest] = identifier.split('/');
        if (!groups.has(first)) groups.set(first, []);
        groups.get(first).push([identifier, rest.join('/')]);
      }
      box.innerHTML = [...groups]
        .map(
          ([first, items]) =>
            `<div class="number-group"><span class="number-group-label">${escapeHtml(fields[0])} ${escapeHtml(first)}</span>${items.map(([identifier, rest]) => chip(identifier, rest)).join('')}</div>`,
        )
        .join('');
    } else {
      box.innerHTML = `<div class="number-group">${shown.map((identifier) => chip(identifier, identifier)).join('')}</div>`;
    }
    count.textContent =
      `${marked.size} de ${max} marcados${full ? ': ya no se pueden marcar más' : ''}.` +
      (free.length > shown.length ? ` Se ven ${shown.length} de ${free.length} libres.` : '') +
      (state.note ? ` ${state.note}` : '');
    onChange();
  }

  box.addEventListener('click', (event) => {
    const chip = event.target.closest('button[data-number]');
    if (!chip || !state) return;
    const { marked, shown, max } = state;
    const identifier = chip.dataset.number;
    if (event.shiftKey && state.last && shown.includes(state.last)) {
      // From the last one pressed to this: marked (not unmarked), until there are as many as can be.
      const a = shown.indexOf(state.last);
      const b = shown.indexOf(identifier);
      for (const n of shown.slice(Math.min(a, b), Math.max(a, b) + 1)) {
        if (marked.size >= max) break;
        marked.add(n);
      }
    } else if (marked.has(identifier)) {
      marked.delete(identifier);
    } else if (marked.size < max) {
      marked.add(identifier);
    }
    state.last = identifier;
    render();
  });
  filter.addEventListener('input', render);

  return {
    // Begins with nothing marked: `free` are the free numbers, `fields` the names of the parts of a
    // number, `max` how many can be marked and `note` is said after the count.
    start({ free, fields, max, note = '' }) {
      state = { free, fields, max, note, marked: new Set(), last: null, shown: [] };
      filter.value = '';
      render();
    },
    // The free numbers changed (somebody took one meanwhile): keep what is marked and still free.
    refresh({ free, fields }) {
      if (!state) return;
      state.free = free;
      state.fields = fields;
      state.marked = new Set([...state.marked].filter((n) => free.includes(n)));
      render();
    },
    clear() {
      if (!state) return;
      state.marked.clear();
      state.last = null;
      render();
    },
    // Fewer (or more) numbers can be marked from now on: what is over is unmarked.
    setMax(max) {
      if (!state) return;
      state.max = max;
      state.marked = new Set([...state.free.filter((n) => state.marked.has(n))].slice(0, max));
      render();
    },
    // What is marked, in the order the numbers are listed.
    marked() {
      return state ? state.free.filter((n) => state.marked.has(n)) : [];
    },
    size() {
      return state ? state.marked.size : 0;
    },
  };
}
