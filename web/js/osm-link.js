import { apiFetch, escapeHtml, formatDate, normalizeForSearch, showError } from './api.js';
import { formatDecimal } from './coordinates.js';
import { openModal } from './modal.js';
import { CATEGORY_LABELS, facts } from './osm-layer.js';
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

// ----- what OpenStreetMap says of the element a place is -----

// Further apart than this (metres), the point of OpenStreetMap is not the place's.
const FAR_M = 25;
// A difference of altitude above this (metres) is told.
const HIGHER_M = 20;

function metresBetween(lat1, lon1, lat2, lon2) {
  const rad = Math.PI / 180;
  const a =
    Math.sin(((lat2 - lat1) * rad) / 2) ** 2 +
    Math.cos(lat1 * rad) * Math.cos(lat2 * rad) * Math.sin(((lon2 - lon1) * rad) / 2) ** 2;
  return 2 * 6371008.8 * Math.asin(Math.sqrt(a));
}

// The data of an element of OpenStreetMap next to a place, as rows of the page of the place: what OpenStreetMap
// says, as it says it, and a mark where it does not agree with the place. Nothing is changed: the place's
// own values are what the rest of the page shows.
function osmDataHtml(place, element) {
  const tags = element.tags ?? {};
  const rows = [];
  const add = (label, html, differs = false) => {
    if (html) {
      const mark = differs ? ' <span class="badge osm-diff">no coincide con el lugar</span>' : '';
      rows.push(
        `<div class="detail-row"><span class="subtitle">${escapeHtml(label)}</span><span>${html}${mark}</span></div>`,
      );
    }
  };
  const name = (tags.name || tags['name:es'] || '').trim();
  const known = [place.name, ...place.alt_names].map(normalizeForSearch);
  add('Nombre', name && escapeHtml(name), name !== '' && !known.includes(normalizeForSearch(name)));
  const others = (tags.alt_name ?? '')
    .split(';')
    .map((n) => n.trim())
    .filter(Boolean);
  add('Otros nombres', others.length ? escapeHtml(others.join(' · ')) : '');
  const away = Math.round(metresBetween(place.lat, place.lon, element.lat, element.lon));
  add(
    'Coordenadas',
    `${escapeHtml(formatDecimal(element.lat, element.lon))}${away > 0 ? ` <span class="subtitle">(a ${away} m del punto del lugar)</span>` : ''}`,
    away > FAR_M,
  );
  const ele = Number.parseFloat(tags.ele);
  add(
    'Altitud',
    Number.isFinite(ele) ? `${Math.round(ele)} m` : '',
    Number.isFinite(ele) && place.altitude_m != null && Math.abs(ele - place.altitude_m) > HIGHER_M,
  );
  for (const [label, value] of facts(tags)) {
    if (label !== 'Altitud') add(label, escapeHtml(value));
  }
  if (/^https?:\/\//.test(tags.website ?? '')) {
    add(
      'Web',
      `<a href="${escapeHtml(tags.website)}" target="_blank" rel="noopener">${escapeHtml(tags.website)}</a>`,
    );
  }
  add('Editado en OpenStreetMap', escapeHtml(formatDate(element.timestamp)));
  const gone = element.deleted_at
    ? `<p class="hint">Este elemento ya no está en OpenStreetMap desde el ${escapeHtml(formatDate(element.deleted_at))}: lo han borrado o ya no tiene las etiquetas que buscamos. Conviene vincular el lugar con otro elemento o desvincularlo.</p>`
    : '';
  return `${gone}<div class="detail-list">${rows.join('')}</div>`;
}

export { openOsmLinkDialog, osmDataHtml };
