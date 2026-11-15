import { apiFetch, showError } from './api.js';
import { areaPickerField, bindAreaPicker } from './area-picker.js';
import { initModal, openModal } from './modal.js';

// The dialog to add a zone below another one ("Dentro de"): anybody who can sign in may, when the zone a
// place is in is not in the list. An admin can also leave "Dentro de" empty to add a country (allowRoot).
// It is built the first time it is needed.

let built = null;

function build() {
  if (built) return built;
  const dialog = document.createElement('dialog');
  dialog.id = 'area-dialog';
  dialog.className = 'panel modal';
  dialog.innerHTML = `
    <button type="button" class="modal-close" data-modal-close aria-label="Cerrar">&times;</button>
    <h3>Añadir una zona</h3>
    <p class="hint" id="ad-intro"></p>
    <form id="area-form">
      <div class="field-grid">
        ${areaPickerField({ id: 'ad-parent', label: 'Dentro de' })}
        <div class="field span-2">
          <label for="ad-name">Nombre</label>
          <input id="ad-name" name="name" type="text" required>
        </div>
        <div class="field span-2">
          <label for="ad-kind">Tipo de zona <span class="optional" id="ad-kind-optional">(opcional)</span></label>
          <input id="ad-kind" name="kind" type="text" maxlength="60">
          <p class="hint" id="ad-kind-hint"></p>
        </div>
      </div>
      <div class="panel-actions">
        <button id="ad-submit" type="submit" class="btn btn-primary">Añadir zona</button>
        <button type="button" class="btn btn-secondary" data-modal-close>Cancelar</button>
      </div>
    </form>`;
  document.querySelector('main.content').append(dialog);
  const form = dialog.querySelector('form');
  initModal(dialog, form);

  const kindInput = form.elements.kind;
  const kindHint = document.getElementById('ad-kind-hint');
  const kindOptional = document.getElementById('ad-kind-optional');
  // The kind of the zones already below the chosen one is the default; with none, it has to be said.
  async function syncKind(parent) {
    kindInput.placeholder = '';
    kindInput.required = false;
    kindOptional.hidden = false;
    kindHint.textContent = '';
    if (!parent) {
      if (built.allowRoot) {
        kindInput.placeholder = 'País';
        kindHint.textContent = 'Si lo dejas en blanco, será «País».';
      }
      return;
    }
    try {
      const below = await apiFetch(`/areas?parent_id=${parent.id}`);
      if (below.length) {
        kindInput.placeholder = below[0].kind;
        kindHint.textContent = `Si lo dejas en blanco, será «${below[0].kind}», como las demás de «${parent.name}».`;
      } else {
        kindInput.required = true;
        kindOptional.hidden = true;
        kindHint.textContent = `Todavía no hay ninguna zona dentro de «${parent.name}»: di qué es (Provincia, Distrito, Parroquia…).`;
      }
    } catch {
      // The API says it if it is needed.
    }
  }
  const picker = bindAreaPicker(dialog.querySelector('[data-area-picker]'), { onChange: syncKind });
  dialog.addEventListener('close', () => {
    picker.setArea(null);
    syncKind(null);
  });
  built = { dialog, form, picker, syncKind, allowRoot: false };
  return built;
}

// Opens it, with `parent` (a zone with its ancestors) already chosen if there is one. `onSaved` is called
// with the zone the API answers with, which has its ancestors.
function openAreaDialog({ parent = null, allowRoot = false, onSaved } = {}) {
  const state = build();
  const { dialog, form, picker, syncKind } = state;
  state.allowRoot = allowRoot;
  picker.setRequired(!allowRoot);
  document.getElementById('ad-intro').textContent = allowRoot
    ? 'Una zona es un país, una provincia, un municipio… Se añade dentro de otra; para añadir un país, deja «Dentro de» en blanco.'
    : 'Una zona es un país, una provincia, un municipio… Se añade dentro de otra.';
  picker.setArea(parent);
  syncKind(parent);
  form.onsubmit = async (event) => {
    event.preventDefault();
    if (!picker.area && !allowRoot) return;
    const submit = form.querySelector('button[type="submit"]');
    submit.disabled = true;
    try {
      const saved = await apiFetch('/areas', {
        method: 'POST',
        body: JSON.stringify({
          parent_id: picker.area?.id ?? null,
          name: form.elements.name.value.trim(),
          kind: form.elements.kind.value.trim(),
        }),
      });
      dialog.close();
      await onSaved?.(saved);
    } catch (err) {
      showError(err.message);
    } finally {
      submit.disabled = false;
    }
  };
  openModal(dialog);
  (parent || allowRoot ? form.elements.name : picker).focus();
}

export { openAreaDialog };
