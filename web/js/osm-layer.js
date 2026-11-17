import { apiFetch, escapeHtml } from './api.js';
import { canHover } from './map-common.js';

// The points of OpenStreetMap on the map of the places: the elements of the local mirror (caves, canyons,
// huts, via ferratas) that are inside the view, as grey rings apart from the club's own places. They are
// asked for only from a zoom where a few kilometres are on screen, because there are thousands of them and
// the mirror asks the API for the box in view, not for all of them. Nothing here asks OpenStreetMap
// anything: the API reads its own copy.

const MIN_ZOOM = 11;
const STORAGE_KEY = 'katiuskas:places-osm';
const CATEGORY_LABELS = {
  cave: 'Cueva',
  canyon: 'Barranco',
  hut: 'Refugio',
  via_ferrata: 'Vía ferrata',
};
const DEFAULT_CATEGORIES = ['cave', 'canyon', 'hut'];
// The credit the ODbL asks for, on the map while its data is on it.
const ATTRIBUTION =
  'Puntos © <a href="https://www.openstreetmap.org/copyright" target="_blank" rel="noopener">colaboradores de OpenStreetMap</a>';

// What is remembered between visits: whether the layer is on and which categories it shows.
function loadChoice() {
  try {
    const saved = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? 'null');
    if (saved && typeof saved.on === 'boolean' && Array.isArray(saved.categories)) {
      return {
        on: saved.on,
        categories: saved.categories.filter((c) => c in CATEGORY_LABELS),
      };
    }
  } catch {
    // Nothing stored, or a browser that does not allow it: the defaults.
  }
  return { on: false, categories: DEFAULT_CATEGORIES };
}

function saveChoice(choice) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(choice));
  } catch {
    // Not remembered, which only costs choosing again.
  }
}

// The facts of an element that are worth a line in its popup, from its OpenStreetMap tags.
function facts(tags) {
  const lines = [];
  const add = (label, value) => {
    if (value) lines.push([label, value]);
  };
  const cut = (text, n = 300) => (text.length > n ? `${text.slice(0, n)}…` : text);
  add('Altitud', tags.ele && `${tags.ele} m`);
  add('Rápel máximo', tags['max:abseil'] && `${tags['max:abseil']} m`);
  add('Dificultad', tags.rating);
  add('Capacidad', tags.capacity && `${tags.capacity} personas`);
  add('Camas', tags.beds);
  if (tags.fee === 'no') add('Tarifa', 'Gratuito');
  else if (tags.fee === 'yes') add('Tarifa', 'De pago');
  add('Acceso', tags.access);
  add('Gestiona', tags.operator);
  add('Descripción', tags.description && cut(tags.description));
  return lines;
}

function popupHtml(detail) {
  const tags = detail.tags ?? {};
  const name = tags.name || tags['name:es'] || 'Sin nombre';
  const rows = facts(tags)
    .map(
      ([label, value]) =>
        `<br><span class="subtitle">${escapeHtml(label)}:</span> ${escapeHtml(value)}`,
    )
    .join('');
  const web = /^https?:\/\//.test(tags.website ?? '')
    ? `<br><a href="${escapeHtml(tags.website)}" target="_blank" rel="noopener">Web</a>`
    : '';
  return `<strong>${escapeHtml(name)}</strong><br>
    <span class="subtitle">${escapeHtml(CATEGORY_LABELS[detail.category] ?? detail.category)} · de OpenStreetMap</span>${rows}${web}`;
}

// createOsmLayer puts the layer on the map and gives what the page needs to drive it. `note` is the element
// that says what is on screen (or that the map has to be zoomed in).
function createOsmLayer(L, map, { note }) {
  const renderer = L.canvas({ padding: 0.5 }); // Thousands of rings are cheap on a canvas, not as SVG.
  const group = L.layerGroup().addTo(map);
  const details = new Map();
  let choice = loadChoice();
  let loaded = null; // { bounds, key, truncated } of the last request that was answered.
  let sequence = 0;
  let timer = null;
  let outline = null;

  const say = (text) => {
    note.textContent = text;
    note.hidden = !text;
  };

  function clear() {
    group.clearLayers();
    loaded = null;
  }

  function credit(on) {
    if (on) map.attributionControl.addAttribution(ATTRIBUTION);
    else map.attributionControl.removeAttribution(ATTRIBUTION);
  }

  async function detailOf(point) {
    const key = `${point.type}/${point.id}`;
    if (!details.has(key)) details.set(key, await apiFetch(`/osm/elements/${key}`));
    return details.get(key);
  }

  function dropOutline() {
    outline?.remove();
    outline = null;
  }

  function marker(point) {
    const ring = L.circleMarker([point.lat, point.lon], {
      renderer,
      // A click on one is for it, not for the map under it (which offers to create a place).
      bubblingMouseEvents: false,
      radius: 6,
      weight: 2,
      color: '#5b6770',
      fillColor: '#ffffff',
      fillOpacity: 0.5,
    });
    if (canHover()) {
      ring.bindTooltip(`${point.name || 'Sin nombre'} (${CATEGORY_LABELS[point.category] ?? ''})`);
    }
    ring.bindPopup('Cargando…', { maxWidth: 300 });
    ring.on('popupopen', async () => {
      try {
        const detail = await detailOf(point);
        ring.setPopupContent(popupHtml(detail));
        dropOutline();
        if (detail.geometry) {
          outline = L.geoJSON(detail.geometry, {
            interactive: false,
            style: { color: '#5b6770', weight: 3, fillOpacity: 0.15 },
          }).addTo(map);
        }
      } catch (err) {
        ring.setPopupContent(escapeHtml(err.message));
      }
    });
    ring.on('popupclose', dropOutline);
    return ring;
  }

  // The box to ask for: the view plus a margin, so that moving a little does not ask again.
  function wanted() {
    const b = map.getBounds().pad(0.25);
    const inside = (v, limit) => Math.max(-limit, Math.min(limit, v));
    const wraps = b.getWest() < -180 || b.getEast() > 180;
    return L.latLngBounds(
      [inside(b.getSouth(), 90), wraps ? -180 : b.getWest()],
      [inside(b.getNorth(), 90), wraps ? 180 : b.getEast()],
    );
  }

  async function refresh() {
    credit(choice.on);
    if (!choice.on) {
      clear();
      say('');
      return;
    }
    if (choice.categories.length === 0) {
      clear();
      say('Elige qué puntos de OpenStreetMap quieres ver.');
      return;
    }
    if (map.getZoom() < MIN_ZOOM) {
      clear();
      say('Acerca el mapa para ver los puntos de OpenStreetMap.');
      return;
    }
    const key = [...choice.categories].sort().join(',');
    const view = map.getBounds();
    if (loaded && loaded.key === key && !loaded.truncated && loaded.bounds.contains(view)) return;
    const box = wanted();
    const mine = ++sequence;
    const bbox = [box.getSouth(), box.getWest(), box.getNorth(), box.getEast()].join(',');
    let answer;
    try {
      answer = await apiFetch(
        `/osm/elements?bbox=${bbox}&category=${encodeURIComponent(choice.categories.join(','))}`,
      );
    } catch (err) {
      if (mine === sequence) say(err.message);
      return;
    }
    if (mine !== sequence) return; // The view moved on, and a later request is the one that counts.
    group.clearLayers();
    for (const point of answer.elements) group.addLayer(marker(point));
    loaded = { bounds: box, key, truncated: answer.truncated };
    say(
      answer.truncated
        ? 'Hay más puntos de OpenStreetMap de los que se muestran: acerca el mapa.'
        : `${answer.elements.length} puntos de OpenStreetMap a la vista.`,
    );
  }

  function schedule() {
    clearTimeout(timer);
    timer = setTimeout(refresh, 300);
  }
  map.on('moveend', schedule);

  return {
    choice: () => ({ on: choice.on, categories: [...choice.categories] }),
    set(next) {
      choice = { on: next.on, categories: next.categories };
      saveChoice(choice);
      loaded = null;
      refresh();
    },
    refresh,
  };
}

export { CATEGORY_LABELS, MIN_ZOOM, createOsmLayer };
