import { apiFetch, escapeHtml, showError } from './api.js';
import { openAreaDialog } from './area-dialog.js';
import { areaChain, areaPickerField, bindAreaPicker } from './area-picker.js';
import { initModal, openModal } from './modal.js';

// The dialog of the admin to look after the zones: find one, rename it or change its kind, merge it into
// another one below the same zone (a municipality that was added twice, or with a typo), delete it if
// nothing is in it, or add another zone inside it. It is built the first time it is opened.

let built = null;

function plural(count, one, many) {
  return `${count} ${count === 1 ? one : many}`;
}

function build() {
  if (built) return built;
  const dialog = document.createElement('dialog');
  dialog.id = 'areas-dialog';
  dialog.className = 'panel modal';
  dialog.innerHTML = `
    <button type="button" class="modal-close" data-modal-close aria-label="Cerrar">&times;</button>
    <h3>Zonas</h3>
    <p class="subtitle">Añade una zona nueva (un país, una provincia, un municipio…) o busca una que ya exista para cambiarle el nombre, moverla, fusionarla con otra, borrarla o añadir otra dentro.</p>
    <p class="success-message" id="am-feedback" hidden></p>
    <div class="panel-actions">
      <button type="button" class="btn btn-secondary btn-small" id="am-add-area">Añadir una zona</button>
    </div>
    <div class="field-grid">
      ${areaPickerField({ id: 'am-search', label: 'Elige una zona para modificarla', required: false })}
    </div>
    <div id="am-panel" hidden>
      <p class="subtitle" id="am-info"></p>
      <form id="am-rename" class="inline-form">
        <div class="field">
          <label for="am-name">Nombre</label>
          <input id="am-name" name="name" type="text" required>
        </div>
        <div class="field">
          <label for="am-kind">Tipo de zona</label>
          <input id="am-kind" name="kind" type="text" maxlength="60" required>
        </div>
        <button type="submit" class="btn btn-primary btn-small">Guardar</button>
      </form>
      <form id="am-move" class="inline-form">
        ${areaPickerField({
          id: 'am-move-parent',
          label: 'Mover dentro de (en blanco: que no esté dentro de ninguna zona)',
          required: false,
          className: '',
          placeholder: 'Escribe la zona en la que debe estar',
        })}
        <button type="submit" class="btn btn-secondary btn-small">Mover</button>
      </form>
      <form id="am-merge" class="inline-form">
        <div class="field">
          <label for="am-merge-into">Fusionar con otra zona del mismo nivel y de la misma zona superior</label>
          <select id="am-merge-into" required></select>
        </div>
        <button type="submit" class="btn btn-secondary btn-small">Fusionar</button>
      </form>
      <div class="panel-actions">
        <button type="button" class="btn btn-secondary btn-small" id="am-add">Añadir una zona dentro</button>
        <button type="button" class="btn btn-danger btn-small" id="am-delete">Borrar zona</button>
      </div>
    </div>`;
  document.querySelector('main.content').append(dialog);
  initModal(dialog);

  const panel = document.getElementById('am-panel');
  const feedback = document.getElementById('am-feedback');
  const renameForm = document.getElementById('am-rename');
  const moveForm = document.getElementById('am-move');
  const mergeForm = document.getElementById('am-merge');
  const mergeInto = document.getElementById('am-merge-into');
  const deleteButton = document.getElementById('am-delete');
  let zone = null; // The zone being looked after, as GET /areas/{id} gives it (with its counts).

  const say = (message) => {
    feedback.textContent = message;
    feedback.hidden = !message;
  };

  async function render() {
    if (!zone) {
      panel.hidden = true;
      return;
    }
    panel.hidden = false;
    document.getElementById('am-info').textContent =
      `${zone.kind} · ${areaChain(zone)} · ${plural(zone.child_count, 'zona dentro', 'zonas dentro')} · ` +
      plural(zone.place_count, 'lugar', 'lugares');
    renameForm.elements.name.value = zone.name;
    renameForm.elements.kind.value = zone.kind;
    // A country is renamed but never deleted or merged; the rest only when nothing is in them.
    const isCountry = !zone.parent_id;
    deleteButton.disabled = isCountry || zone.child_count > 0 || zone.place_count > 0;
    deleteButton.title = isCountry
      ? 'Un país no se puede borrar'
      : zone.child_count > 0
        ? 'Tiene otras zonas dentro'
        : zone.place_count > 0
          ? 'Tiene lugares'
          : '';
    mergeForm.hidden = isCountry;
    // Where it is now; changing it and sending the form moves the zone (with all that is below it).
    const here = zone.id;
    const parent = zone.parent_id ? await apiFetch(`/areas/${zone.parent_id}`) : null;
    if (zone?.id !== here) return; // Another zone was chosen meanwhile.
    moveParent.setArea(parent);
    if (!isCountry) {
      const mine = zone.id;
      const siblings = await apiFetch(`/areas?parent_id=${zone.parent_id}`);
      if (zone?.id !== mine) return; // Another zone was chosen meanwhile.
      mergeInto.innerHTML = siblings
        .filter((s) => s.id !== mine)
        .map((s) => `<option value="${s.id}">${escapeHtml(s.name)}</option>`)
        .join('');
      // With no sibling there is nothing to merge into: the form would only be an empty list.
      mergeForm.hidden = mergeInto.options.length === 0;
    }
  }

  async function load(id) {
    try {
      zone = await apiFetch(`/areas/${id}`);
      await render();
    } catch (err) {
      showError(err.message);
    }
  }

  const moveParent = bindAreaPicker(moveForm.querySelector('[data-area-picker]'), {});
  const picker = bindAreaPicker(dialog.querySelector('[data-area-picker]'), {
    onChange: (area) => {
      say('');
      if (area) load(area.id);
      else {
        zone = null;
        render();
      }
    },
  });

  renameForm.addEventListener('submit', async (event) => {
    event.preventDefault();
    try {
      const saved = await apiFetch(`/areas/${zone.id}`, {
        method: 'PUT',
        body: JSON.stringify({
          name: renameForm.elements.name.value.trim(),
          kind: renameForm.elements.kind.value.trim(),
        }),
      });
      picker.setArea(saved);
      zone = saved;
      say('Zona guardada.');
      await render();
    } catch (err) {
      showError(err.message);
    }
  });

  moveForm.addEventListener('submit', async (event) => {
    event.preventDefault();
    const target = moveParent.area;
    if ((target?.id ?? null) === (zone.parent_id ?? null)) {
      say('La zona ya está ahí.');
      return;
    }
    const where = target
      ? `dentro de «${target.name}» (${areaChain(target)})`
      : 'fuera de todas las demás: pasará a ser una zona de primer nivel, como un país';
    if (!confirm(`Se mueve «${zone.name}», con todo lo que tiene dentro, ${where}. ¿Mover?`))
      return;
    try {
      const saved = await apiFetch(`/areas/${zone.id}`, {
        method: 'PUT',
        body: JSON.stringify({
          name: zone.name,
          kind: zone.kind,
          parent_id: target ? target.id : null,
        }),
      });
      picker.setArea(saved);
      zone = saved;
      say('Zona movida.');
      await render();
    } catch (err) {
      showError(err.message);
    }
  });

  mergeForm.addEventListener('submit', async (event) => {
    event.preventDefault();
    const target = mergeInto.selectedOptions[0];
    if (
      !confirm(
        `Se pasa todo lo que hay en «${zone.name}» a «${target.text}» y se borra «${zone.name}». ¿Fusionar?`,
      )
    ) {
      return;
    }
    try {
      await apiFetch(`/areas/${zone.id}/merge`, {
        method: 'POST',
        body: JSON.stringify({ into_id: Number(target.value) }),
      });
      const into = await apiFetch(`/areas/${target.value}`);
      picker.setArea(into);
      say('Zonas fusionadas.');
    } catch (err) {
      showError(err.message);
    }
  });

  deleteButton.addEventListener('click', async () => {
    if (!confirm(`¿Borrar la zona «${zone.name}»?`)) return;
    try {
      await apiFetch(`/areas/${zone.id}`, { method: 'DELETE' });
      picker.setArea(null);
      say('Zona borrada.');
    } catch (err) {
      showError(err.message);
    }
  });

  // Only the admin has this dialog; it adds a zone anywhere, or a country if "Dentro de" is left empty,
  // which only an admin may.
  document.getElementById('am-add-area').addEventListener('click', () => {
    openAreaDialog({
      allowRoot: true,
      onSaved: async (saved) => {
        picker.setArea(saved);
        say('Zona añadida.');
      },
    });
  });

  document.getElementById('am-add').addEventListener('click', () => {
    openAreaDialog({
      parent: zone,
      onSaved: async () => {
        say('Zona añadida.');
        await load(zone.id);
      },
    });
  });

  dialog.addEventListener('close', () => {
    picker.setArea(null);
    say('');
  });
  built = { dialog, picker };
  return built;
}

// Makes the button open the dialog.
function initAreasManager(button) {
  button.addEventListener('click', () => {
    const { dialog, picker } = build();
    openModal(dialog);
    picker.focus();
  });
}

export { initAreasManager };
