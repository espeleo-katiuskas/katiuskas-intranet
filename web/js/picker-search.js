import { normalizeForSearch } from './api.js';

// The searchable pickers of the pages are an <input list="…"> over a <datalist>, which the browser filters
// by itself — and it is case-insensitive but not accent-insensitive, so «garcia» did not find «García». This
// takes those inputs over: on first focus it removes the `list` attribute (so the browser shows nothing of its
// own) and shows its own menu with the options of that datalist whose text contains every word typed, ignoring
// accents and case. It works for every such input on the page, also those drawn later (it listens on the
// document), and leaves what a page does with the input as it was: choosing an option writes its value into
// the input and fires `input` and `change`, so the page cannot tell it from the browser's own.
//
// The menu is placed right after the input, so inside a <dialog> (in the top layer) it stays above it. It lists
// every option that matches, however many (the menu scrolls): a cap would leave people out of a picker that
// is opened to choose among all of them.

let menu = null;
let current = null; // The input the menu is for.
let active = -1;

function optionsOf(input) {
  const list = document.getElementById(input.dataset.list);
  return list ? [...list.options].map((o) => o.value) : [];
}

function matches(input) {
  const words = normalizeForSearch(input.value).split(/\s+/).filter(Boolean);
  return optionsOf(input).filter((value) => {
    const text = normalizeForSearch(value);
    return words.every((w) => text.includes(w));
  });
}

function hide() {
  menu?.remove();
  menu = null;
  current = null;
  active = -1;
}

function highlight(index) {
  const items = menu ? [...menu.children] : [];
  if (items.length === 0) return;
  active = (index + items.length) % items.length;
  for (const [i, li] of items.entries()) li.classList.toggle('active', i === active);
  items[active].scrollIntoView({ block: 'nearest' });
}

function choose(value) {
  if (!current) return;
  const input = current;
  input.value = value;
  hide();
  input.dispatchEvent(new Event('input', { bubbles: true }));
  input.dispatchEvent(new Event('change', { bubbles: true }));
}

function show(input) {
  const found = matches(input);
  if (found.length === 0 || (found.length === 1 && found[0] === input.value)) {
    hide();
    return;
  }
  if (!menu || current !== input) {
    hide();
    menu = document.createElement('ul');
    menu.className = 'picker-menu';
    menu.setAttribute('role', 'listbox');
    // Placed against the input inside the same positioned ancestor, so it follows it wherever it is.
    menu.style.left = `${input.offsetLeft}px`;
    menu.style.top = `${input.offsetTop + input.offsetHeight}px`;
    menu.style.minWidth = `${input.offsetWidth}px`;
    // Pressing on an option must not take the focus off the input (which would hide the menu first).
    menu.addEventListener('mousedown', (event) => {
      event.preventDefault();
      const li = event.target.closest('li');
      if (li) choose(li.dataset.value);
    });
    input.after(menu);
    current = input;
  }
  menu.innerHTML = '';
  for (const value of found) {
    const li = document.createElement('li');
    li.setAttribute('role', 'option');
    li.dataset.value = value;
    li.textContent = value;
    menu.append(li);
  }
  active = -1;
}

// Takes the input over from the browser's own list the first time it is reached.
function adopt(input) {
  if (input.hasAttribute('list')) {
    input.dataset.list = input.getAttribute('list');
    input.removeAttribute('list');
  }
  return input.dataset.list !== undefined;
}

export function initPickerSearch() {
  document.addEventListener('focusin', (event) => {
    const input = event.target;
    if (input instanceof HTMLInputElement && input.matches('input[list], input[data-list]')) {
      adopt(input);
      show(input);
    }
  });
  document.addEventListener('input', (event) => {
    const input = event.target;
    if (input instanceof HTMLInputElement && input.dataset.list !== undefined) show(input);
  });
  document.addEventListener('keydown', (event) => {
    const input = event.target;
    if (!menu || input !== current) return;
    if (event.key === 'ArrowDown') {
      event.preventDefault();
      highlight(active + 1);
    } else if (event.key === 'ArrowUp') {
      event.preventDefault();
      highlight(active - 1);
    } else if (event.key === 'Enter' && active >= 0) {
      // Choosing an option is not sending the form.
      event.preventDefault();
      choose(menu.children[active].dataset.value);
    } else if (event.key === 'Escape') {
      // Closes the menu, not the dialog it may be in.
      event.preventDefault();
      event.stopPropagation();
      hide();
    }
  });
  document.addEventListener('focusout', (event) => {
    if (event.target === current) hide();
  });
}
