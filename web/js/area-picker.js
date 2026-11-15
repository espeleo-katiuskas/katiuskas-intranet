import { apiFetch, escapeHtml } from './api.js';

// A field to choose a zone of the tree of zones (a country, a province, a municipality…): the person types
// part of a name and chooses among what is found, each shown with what it is in, so that two zones with
// the same name are told apart ("Valdemanco — Madrid, Comunidad de Madrid, España"). It is used to say
// where a place is, to say where a new zone goes, and by the admin to find the zone to change.

const SEARCH_DELAY_MS = 200;
const SEARCH_LIMIT = 12;

// What is above a zone, the nearest first: "Madrid, Comunidad de Madrid, España".
function aboveText(area) {
  return [...(area.ancestors ?? [])]
    .reverse()
    .map((a) => a.name)
    .join(', ');
}

// A zone for a list: "Valdemanco — Madrid, Comunidad de Madrid, España".
function areaLabel(area) {
  const above = aboveText(area);
  return above ? `${area.name} — ${above}` : area.name;
}

// The chain from the country to the zone: "España › Comunidad de Madrid › Madrid › Valdemanco".
function areaChain(area) {
  return [...(area.ancestors ?? []), area].map((a) => a.name).join(' › ');
}

// The markup of the field. Put it in a form and give the element to bindAreaPicker.
function areaPickerField({ id, label, required = true, className = 'span-2', placeholder }) {
  return `
    <div class="field area-picker ${className}" data-area-picker>
      <label for="${id}">${label}</label>
      <div class="area-combo">
        <input id="${id}" type="text" role="combobox" aria-expanded="false" aria-controls="${id}-results"
          autocomplete="off" placeholder="${placeholder ?? 'Escribe un municipio, una provincia, un país…'}"${required ? ' required' : ''}>
        <ul class="area-results" id="${id}-results" role="listbox" hidden></ul>
      </div>
      <p class="hint area-chosen" id="${id}-chosen"></p>
    </div>`;
}

// Makes the field work. It gives { area, setArea(area | null), focus() }; `area` is the zone chosen, with
// its ancestors, or null while what is typed is not a choice (and then the field is invalid, if required).
// onChange is called whenever the choice changes.
function bindAreaPicker(root, { onChange } = {}) {
  const input = root.querySelector('input');
  const list = root.querySelector('.area-results');
  const chosenLine = root.querySelector('.area-chosen');
  let required = input.required;
  let current = null;
  let results = [];
  let active = -1;
  let timer = null;
  let request = 0;

  function validity() {
    // Text that is not a choice is never taken for "no zone", even when the field is optional.
    const unchosen = required || input.value.trim() !== '';
    input.setCustomValidity(unchosen && !current ? 'Elige una zona de la lista.' : '');
  }

  function close() {
    list.hidden = true;
    input.setAttribute('aria-expanded', 'false');
    active = -1;
  }

  function showChosen() {
    chosenLine.textContent = current ? `${current.kind} · ${areaChain(current)}` : '';
  }

  function choose(area) {
    const changed = current?.id !== area?.id;
    current = area;
    input.value = area ? area.name : '';
    showChosen();
    validity();
    close();
    if (changed) onChange?.(current);
  }

  function render() {
    list.innerHTML = results.length
      ? results
          .map(
            (area, i) => `
        <li role="option" data-index="${i}" aria-selected="${i === active}">
          <strong>${escapeHtml(area.name)}</strong>
          <span class="subtitle">${escapeHtml(area.kind)}${aboveText(area) ? ` · ${escapeHtml(aboveText(area))}` : ''}</span>
        </li>`,
          )
          .join('')
      : '<li class="area-none">No hay ninguna zona con ese nombre.</li>';
    list.hidden = false;
    input.setAttribute('aria-expanded', 'true');
  }

  async function search() {
    const text = input.value.trim();
    if (text.length < 2) {
      close();
      return;
    }
    const mine = ++request;
    try {
      const found = await apiFetch(
        `/areas?search=${encodeURIComponent(text)}&limit=${SEARCH_LIMIT}`,
      );
      if (mine !== request) return; // A newer search is on its way.
      results = found;
      active = -1;
      render();
    } catch {
      close(); // Without the list the field still says it is invalid until a zone is chosen.
    }
  }

  input.addEventListener('input', () => {
    // Typing is no longer the choice made before.
    if (current && input.value !== current.name) {
      current = null;
      showChosen();
      onChange?.(null);
    }
    validity();
    clearTimeout(timer);
    timer = setTimeout(search, SEARCH_DELAY_MS);
  });

  input.addEventListener('keydown', (event) => {
    if (list.hidden) return;
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      if (!results.length) return;
      active = (active + (event.key === 'ArrowDown' ? 1 : -1) + results.length) % results.length;
      render();
    } else if (event.key === 'Enter' && active >= 0) {
      event.preventDefault(); // It chooses; it does not send the form.
      choose(results[active]);
    } else if (event.key === 'Escape') {
      event.preventDefault();
      event.stopPropagation(); // It closes the list, not the dialog the field is in.
      close();
    }
  });

  // mousedown, not click: the field loses focus before a click arrives.
  list.addEventListener('mousedown', (event) => {
    event.preventDefault();
    const item = event.target.closest('li[data-index]');
    if (item) choose(results[Number(item.dataset.index)]);
  });
  input.addEventListener('blur', close);

  validity();
  return {
    get area() {
      return current;
    },
    setArea(area) {
      choose(area);
    },
    focus() {
      input.focus();
    },
    // Whether a zone has to be chosen (an optional field may stay empty).
    setRequired(value) {
      required = value;
      input.required = value;
      validity();
    },
  };
}

export { areaChain, areaLabel, areaPickerField, bindAreaPicker };
