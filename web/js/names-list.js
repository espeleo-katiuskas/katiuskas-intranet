import { escapeHtml } from './api.js';

// A field to write a list of names (the other names of a place): each one is added with Intro or the
// button and shown as a chip that can be taken off; what is typed and not yet added is taken as one more
// when the list is read, so it is never lost by sending the form. Blank names and repeated ones (ignoring
// case) are not added.

// The markup of the field. Put it in a form and give the element to bindNamesList.
function namesListField({ id, label, hint }) {
  return `
    <div class="field span-2 names-list" data-names-list>
      <label for="${id}">${label} <span class="optional">(opcional)</span></label>
      <div class="names-chips" id="${id}-chips"></div>
      <div class="role-add-row">
        <input id="${id}" type="text" autocomplete="off" maxlength="200" placeholder="Escribe un nombre y pulsa Intro">
        <button type="button" class="btn btn-secondary btn-small" data-names-add>Añadir</button>
      </div>
      ${hint ? `<p class="hint">${hint}</p>` : ''}
    </div>`;
}

// Makes the field work. It gives { names, setNames(list) }: `names` is the list as it is, with what is
// typed at the end.
function bindNamesList(root) {
  const input = root.querySelector('input');
  const chips = root.querySelector('.names-chips');
  let list = [];

  function render() {
    chips.innerHTML = list
      .map(
        (name, i) => `
        <span class="badge badge-removable">${escapeHtml(name)}
          <button type="button" data-names-remove="${i}" aria-label="Quitar «${escapeHtml(name)}»">&times;</button>
        </span>`,
      )
      .join('');
    chips.hidden = list.length === 0;
  }

  function has(name) {
    return list.some((n) => n.toLowerCase() === name.toLowerCase());
  }

  function addTyped() {
    const name = input.value.trim();
    input.value = '';
    if (name && !has(name)) {
      list.push(name);
      render();
    }
  }

  input.addEventListener('keydown', (event) => {
    if (event.key === 'Enter') {
      event.preventDefault(); // It adds the name; it does not send the form.
      addTyped();
    }
  });
  root.querySelector('[data-names-add]').addEventListener('click', () => {
    addTyped();
    input.focus();
  });
  chips.addEventListener('click', (event) => {
    const button = event.target.closest('button[data-names-remove]');
    if (!button) return;
    list.splice(Number(button.dataset.namesRemove), 1);
    render();
  });
  render();

  return {
    get names() {
      const typed = input.value.trim();
      return typed && !has(typed) ? [...list, typed] : [...list];
    },
    setNames(names) {
      list = [];
      for (const name of names ?? []) {
        if (name.trim() && !has(name.trim())) list.push(name.trim());
      }
      input.value = '';
      render();
    },
  };
}

export { bindNamesList, namesListField };
