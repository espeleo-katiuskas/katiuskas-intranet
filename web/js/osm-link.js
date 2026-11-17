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
  // A row: what OpenStreetMap says, a mark if it does not agree with the place, and the button that copies it
  // to the place (`copy` is the name of the field, see copyFromOsm).
  const add = (label, html, { differs = false, copy = null, copyText = '' } = {}) => {
    if (!html) return;
    const mark = differs ? ' <span class="badge osm-diff">no coincide con el lugar</span>' : '';
    const button = copy
      ? ` <button type="button" class="btn btn-secondary btn-small" data-osm-copy="${copy}">${escapeHtml(copyText)}</button>`
      : '';
    rows.push(
      `<div class="detail-row"><span class="subtitle">${escapeHtml(label)}</span><span>${html}${mark}${button}</span></div>`,
    );
  };
  const name = (tags.name || tags['name:es'] || '').trim();
  const known = [place.name, ...place.alt_names].map(normalizeForSearch);
  const nameDiffers = name !== '' && !known.includes(normalizeForSearch(name));
  add('Nombre', name && escapeHtml(name), {
    differs: nameDiffers,
    copy: nameDiffers ? 'name' : null,
    copyText: 'Usar como nombre',
  });
  const others = osmAltNames(tags);
  const newOthers = others.filter((n) => !known.includes(normalizeForSearch(n)));
  add('Otros nombres', others.length ? escapeHtml(others.join(' · ')) : '', {
    copy: newOthers.length ? 'alt_names' : null,
    copyText: 'Añadir a los otros nombres',
  });
  const away = Math.round(metresBetween(place.lat, place.lon, element.lat, element.lon));
  add(
    'Coordenadas',
    `${escapeHtml(formatDecimal(element.lat, element.lon))}${away > 0 ? ` <span class="subtitle">(a ${away} m del punto del lugar)</span>` : ''}`,
    {
      differs: away > FAR_M,
      copy: away > FAR_M ? 'position' : null,
      copyText: 'Usar estas coordenadas',
    },
  );
  const ele = Number.parseFloat(tags.ele);
  const hasEle = Number.isFinite(ele);
  add('Altitud', hasEle ? `${Math.round(ele)} m` : '', {
    differs: hasEle && place.altitude_m != null && Math.abs(ele - place.altitude_m) > HIGHER_M,
    copy: hasEle && place.altitude_m !== Math.round(ele) ? 'ele' : null,
    copyText: 'Usar esta altitud',
  });
  for (const [label, value] of facts(tags)) {
    if (label === 'Altitud') continue;
    // The description is copied only into a place that has none: it never replaces what the club wrote.
    const copy = label === 'Descripción' && !place.description?.trim();
    add(label, escapeHtml(value), {
      copy: copy ? 'description' : null,
      copyText: 'Copiar a la descripción',
    });
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

function osmAltNames(tags) {
  return (tags.alt_name ?? '')
    .split(';')
    .map((n) => n.trim())
    .filter(Boolean);
}

// The body of the edit of a place that copies one value of its OpenStreetMap element into it, leaving the rest
// as it is: "name" (the old name stays among the other names, so that it is not lost), "alt_names" (the new
// ones are added), "position", "ele" (the altitude) and "description" (only into a place that has none).
function copyFromOsm(place, element, field) {
  const tags = element.tags ?? {};
  const body = {
    type_id: place.type_id,
    name: place.name,
    alt_names: [...place.alt_names],
    state: place.state ?? '',
    subtype_id: place.subtype_id ?? null,
    system_id: place.system_id ?? null,
    area_id: place.area_id,
    lat: place.lat,
    lon: place.lon,
    altitude_m: place.altitude_m ?? null,
    access: place.access ?? null,
    description: place.description ?? null,
    depth_m: place.depth_m ?? null,
    horizontal_length_m: place.horizontal_length_m ?? null,
    geology_id: place.geology_id ?? null,
    technical_sheet: place.technical_sheet ?? null,
  };
  const has = (list, name) => list.some((n) => normalizeForSearch(n) === normalizeForSearch(name));
  if (field === 'name') {
    body.name = (tags.name || tags['name:es']).trim();
    if (!has(body.alt_names, place.name)) body.alt_names.push(place.name);
    body.alt_names = body.alt_names.filter(
      (n) => normalizeForSearch(n) !== normalizeForSearch(body.name),
    );
  } else if (field === 'alt_names') {
    for (const n of osmAltNames(tags)) {
      if (!has([body.name, ...body.alt_names], n)) body.alt_names.push(n);
    }
  } else if (field === 'position') {
    body.lat = element.lat;
    body.lon = element.lon;
  } else if (field === 'ele') {
    body.altitude_m = Math.round(Number.parseFloat(tags.ele));
  } else if (field === 'description') {
    body.description = (tags.description ?? '').trim();
  }
  return body;
}

// What OpenStreetMap's keys are called for the person who reads what changed.
const CHANGE_LABELS = {
  name: 'Nombre',
  'name:es': 'Nombre en español',
  alt_name: 'Otros nombres',
  ele: 'Altitud (m)',
  description: 'Descripción',
  website: 'Web',
  access: 'Acceso',
  fee: 'Tarifa',
  capacity: 'Capacidad',
  beds: 'Camas',
  operator: 'Gestiona',
  'max:abseil': 'Rápel máximo (m)',
  rating: 'Dificultad',
  depth: 'Profundidad (m)',
  length: 'Desarrollo (m)',
  position: 'Posición',
};

// The notice of what changed in the element since the place was linked or the changes were last looked at.
function osmChangesHtml(place) {
  const changes = place.osm_changes ?? [];
  if (changes.length === 0) return '';
  const show = (value) => (value == null ? '<em>nada</em>' : `«${escapeHtml(value)}»`);
  const lines = changes
    .map((c) => {
      if (c.key === 'removed') return '<li>El elemento ya no está en OpenStreetMap.</li>';
      const label = CHANGE_LABELS[c.key] ?? `Etiqueta ${c.key}`;
      return `<li><strong>${escapeHtml(label)}:</strong> antes ${show(c.before)}, ahora ${show(c.after)}</li>`;
    })
    .join('');
  return `<div class="osm-changes">
    <p><strong>OpenStreetMap ha cambiado este elemento</strong> desde que se vinculó o se revisó por última vez:</p>
    <ul>${lines}</ul>
    <p class="hint">Mira lo que dice abajo y copia al lugar lo que quieras; no se cambia nada solo.</p>
    <button type="button" class="btn btn-secondary btn-small" data-osm-review>Marcar como revisado</button>
  </div>`;
}

export { copyFromOsm, openOsmLinkDialog, osmChangesHtml, osmDataHtml };
