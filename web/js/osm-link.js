import { apiFetch, escapeHtml, showError } from './api.js';
import { openModal } from './modal.js';
import { CATEGORY_LABELS } from './osm-layer.js';
import { buildDialog } from './place-dialog.js';

// The dialog to say which element of OpenStreetMap a place of the catalogue is: the elements near it that
// are not yet a place, those whose name looks like the place's first and then the nearest, for a person to
// choose one. It is for the place page (/lugar); the elements come from the local mirror, not from
// OpenStreetMap.

const NODE_NAMES = { node: 'nodo', way: 'vía', relation: 'relación' };
const MATCH_LABELS = { exact: 'mismo nombre', similar: 'nombre parecido' };

// What a place may be, by the kind of its type: a cave or a canyon is looked for among the caves or the
// canyons, and any other place among the huts and the via ferratas.
function categoriesOf(place) {
  if (place.kind === 'cave') return 'cave';
  if (place.kind === 'canyon') return 'canyon';
  return 'hut,via_ferrata';
}

function distanceText(metres) {
  return metres < 1000 ? `a ${metres} m` : `a ${(metres / 1000).toFixed(1).replace('.', ',')} km`;
}

function candidateHtml(c) {
  const name = c.name ? escapeHtml(c.name) : '<em>Sin nombre</em>';
  const details = [
    CATEGORY_LABELS[c.category] ?? c.category,
    `${NODE_NAMES[c.type] ?? c.type} ${c.id}`,
    distanceText(c.distance_m),
    MATCH_LABELS[c.name_match],
  ].filter(Boolean);
  return `<li>
    <div><strong>${name}</strong><br><span class="subtitle">${escapeHtml(details.join(' · '))}</span></div>
    <button type="button" class="btn btn-secondary btn-small" data-type="${c.type}" data-id="${c.id}">Vincular</button>
  </li>`;
}

let dialogParts = null;

function ensureDialog() {
  if (dialogParts) return dialogParts;
  const { dialog } = buildDialog(
    'osm-link-dialog',
    `
    <h3>Vincular con OpenStreetMap</h3>
    <p class="hint" id="osm-link-intro"></p>
    <div class="field">
      <label for="osm-link-radius">Buscar a menos de</label>
      <select id="osm-link-radius">
        <option value="500">500 m</option>
        <option value="1000">1 km</option>
        <option value="2000">2 km</option>
      </select>
    </div>
    <p class="subtitle" id="osm-link-note"></p>
    <ul class="osm-candidates" id="osm-link-list"></ul>`,
  );
  dialogParts = {
    dialog,
    intro: dialog.querySelector('#osm-link-intro'),
    radius: dialog.querySelector('#osm-link-radius'),
    note: dialog.querySelector('#osm-link-note'),
    list: dialog.querySelector('#osm-link-list'),
  };
  return dialogParts;
}

function openOsmLinkDialog({ place, onLinked }) {
  const { dialog, intro, radius, note, list } = ensureDialog();
  intro.textContent = `Elementos de OpenStreetMap cerca de «${place.name}» que todavía no son un lugar del catálogo. Elige el que es.`;
  radius.value = '500';
  let asked = 0; // The newest search: an older answer is dropped.

  async function search() {
    const mine = ++asked;
    list.replaceChildren();
    note.textContent = 'Buscando…';
    try {
      const query = new URLSearchParams({
        lat: place.lat,
        lon: place.lon,
        radius: radius.value,
        name: place.name,
        category: categoriesOf(place),
      });
      const { elements } = await apiFetch(`/osm/nearby?${query}`);
      if (mine !== asked) return;
      note.textContent = elements.length
        ? ''
        : 'No hay elementos de OpenStreetMap libres a esa distancia. Prueba con un radio mayor.';
      list.innerHTML = elements.map(candidateHtml).join('');
    } catch (err) {
      if (mine === asked) note.textContent = err.message;
    }
  }

  radius.onchange = search;
  list.onclick = async (event) => {
    const button = event.target.closest('button[data-id]');
    if (!button) return;
    button.disabled = true;
    try {
      const linked = await apiFetch(`/places/${place.id}/links/osm`, {
        method: 'PUT',
        body: JSON.stringify({ osm_type: button.dataset.type, osm_id: Number(button.dataset.id) }),
      });
      dialog.close();
      await onLinked?.(linked);
    } catch (err) {
      button.disabled = false;
      showError(err.message);
    }
  };
  openModal(dialog);
  search();
}

export { openOsmLinkDialog };
