import { apiFetch, escapeHtml } from './api.js';
import { formatDecimal } from './coordinates.js';
import { placeTypeLabel, POINT_KIND_LABELS, STATE_LABELS } from './places-common.js';

// What the maps of the places share: Leaflet (and its clustering) loaded the first time a map is shown,
// the layers of tiles, the markers and the legend. Leaflet is copied into web/vendor, so the page
// asks nobody for code, only for tiles.

const SCRIPTS = [
  '/vendor/leaflet/leaflet.js',
  '/vendor/leaflet-markercluster/leaflet.markercluster.js',
];
const STYLES = [
  '/vendor/leaflet/leaflet.css',
  '/vendor/leaflet-markercluster/MarkerCluster.css',
  '/vendor/leaflet-markercluster/MarkerCluster.Default.css',
];

function addScript(src) {
  return new Promise((resolve, reject) => {
    const script = document.createElement('script');
    script.src = src;
    script.onload = resolve;
    script.onerror = () => reject(new Error(`No se pudo cargar ${src}.`));
    document.head.append(script);
  });
}

let loading = null;

// Leaflet, loaded once; the clustering plugin needs it first.
function loadLeaflet() {
  loading ??= (async () => {
    for (const href of STYLES) {
      const link = document.createElement('link');
      link.rel = 'stylesheet';
      link.href = href;
      document.head.append(link);
    }
    for (const src of SCRIPTS) await addScript(src);
    return window.L;
  })();
  loading.catch(() => {
    loading = null; // A failure is not kept: the next try asks again.
  });
  return loading;
}

const IGN = '<a href="https://www.ign.es/" target="_blank" rel="noopener">IGN</a>';
const WMTS = (path, layer) =>
  `https://www.ign.es/wmts/${path}?request=getTile&layer=${layer}&TileMatrixSet=GoogleMapsCompatible&TileMatrix={z}&TileCol={x}&TileRow={y}&format=image/jpeg`;

// The base layers, the first one by default. The IGN's are the official ones of Spain and free to use
// with their credit, and Esri's world imagery covers where PNOA does not; OpenStreetMap's own tiles ask for light use, which is that of a club.
const LAYERS = [
  {
    id: 'topo',
    name: 'Topográfico (IGN)',
    url: WMTS('mapa-raster', 'MTN'),
    attribution: `Mapa © ${IGN}`,
    maxNativeZoom: 17,
  },
  {
    id: 'satellite',
    name: 'Satélite (PNOA)',
    url: WMTS('pnoa-ma', 'OI.OrthoimageCoverage'),
    attribution: `Ortofotos PNOA © ${IGN}`,
    maxNativeZoom: 19,
  },
  {
    id: 'world',
    name: 'Satélite (mundial)',
    url: 'https://server.arcgisonline.com/ArcGIS/rest/services/World_Imagery/MapServer/tile/{z}/{y}/{x}',
    attribution:
      'Imágenes © <a href="https://www.esri.com/" target="_blank" rel="noopener">Esri</a>, Vantor, Earthstar Geographics y la comunidad de usuarios de GIS',
    maxNativeZoom: 18,
  },
  {
    id: 'osm',
    name: 'OpenStreetMap',
    url: 'https://tile.openstreetmap.org/{z}/{x}/{y}.png',
    attribution:
      '© <a href="https://www.openstreetmap.org/copyright" target="_blank" rel="noopener">OpenStreetMap</a>',
    maxNativeZoom: 19,
  },
];

const LAYER_KEY = 'katiuskas:places-map-layer';

function rememberedLayer() {
  try {
    return localStorage.getItem(LAYER_KEY);
  } catch {
    return null; // Without storage the first layer is used.
  }
}

function rememberLayer(id) {
  try {
    localStorage.setItem(LAYER_KEY, id);
  } catch {
    // Not remembered, which is all.
  }
}

// The colour of the marker of each state, as the badges of the lists; the places that have none are grey.
const NO_STATE_COLOR = '#8a8f87';
const STATE_COLORS = {
  open: '#3f8a56',
  permit_required: '#d19a1f',
  tourist: '#3a76b5',
  closed: '#a1453d',
};

// A map in the element, with the layers (the one chosen last time is the one shown), the scale and
// Spain in view until something is fitted, and the box to search a place by name if `search` is set
// (only the map of the list has it). Gives Leaflet's `L`, the map and its control of layers.
async function createMap(element, { search = false } = {}) {
  const L = await loadLeaflet();
  const map = L.map(element, { maxZoom: 19, worldCopyJump: true }).setView([40.2, -3.7], 6);
  const layers = Object.fromEntries(
    LAYERS.map((layer) => [
      layer.id,
      L.tileLayer(layer.url, {
        attribution: layer.attribution,
        maxNativeZoom: layer.maxNativeZoom,
        maxZoom: 19,
      }),
    ]),
  );
  const first = layers[rememberedLayer()] ? rememberedLayer() : LAYERS[0].id;
  layers[first].addTo(map);
  const layersControl = L.control
    .layers(Object.fromEntries(LAYERS.map((l) => [l.name, layers[l.id]])), null, {
      collapsed: true,
    })
    .addTo(map);
  L.control.scale({ imperial: false }).addTo(map);
  addExpandButton(L, map, element);
  if (search) addSearchControl(L, map);
  map.on('baselayerchange', (event) => {
    // On a touch screen nothing makes the list of layers go away once one is chosen (it closes when the
    // pointer leaves, and a finger does not): choosing is closing it.
    layersControl.collapse();
    const found = LAYERS.find((layer) => layer.name === event.name);
    if (found) rememberLayer(found.id);
  });
  return { L, map, layersControl };
}

const EXPAND_ICON =
  '<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><path d="M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5" fill="none" stroke="currentColor" stroke-width="2"/></svg>';
const SHRINK_ICON =
  '<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><path d="M9 4v5H4M15 4v5h5M9 20v-5H4M15 20v-5h5" fill="none" stroke="currentColor" stroke-width="2"/></svg>';

// A button under the zoom ones that makes the map take the whole window of the browser (and another
// click, or Escape, gives it back its place on the page). It does not use the Fullscreen API: the
// monitor is not taken over, only the window, and the page keeps its own address bar and tabs.
function addExpandButton(L, map, element) {
  let expanded = false;
  let button = null;
  // The map leaves the flow of the page when it covers the window, which shortens the page and makes the
  // browser scroll it up: a block as tall as the map stays in its place, and the scroll is put back.
  let spacer = null;
  let scrollY = 0;

  function set(value) {
    if (value) {
      scrollY = window.scrollY;
      spacer = document.createElement('div');
      spacer.style.height = `${element.offsetHeight}px`;
      element.before(spacer);
    }
    expanded = value;
    element.classList.toggle('map-expanded', expanded);
    document.documentElement.classList.toggle('map-is-expanded', expanded);
    button.innerHTML = expanded ? SHRINK_ICON : EXPAND_ICON;
    const label = expanded ? 'Salir de la pantalla completa' : 'Pantalla completa';
    button.title = label;
    button.setAttribute('aria-label', label);
    // The container changed its size: Leaflet has to know, and by default it keeps the centre of the map
    // where it was (not the corner), so the same place stays in the middle.
    map.invalidateSize({ animate: false });
    if (!expanded && spacer) {
      spacer.remove();
      spacer = null;
      window.scrollTo(0, scrollY);
    }
    if (expanded) document.addEventListener('keydown', onKey);
    else document.removeEventListener('keydown', onKey);
  }

  function onKey(event) {
    if (event.key === 'Escape') set(false);
  }

  const Expand = L.Control.extend({
    options: { position: 'topleft' },
    onAdd() {
      const container = L.DomUtil.create('div', 'leaflet-bar leaflet-control');
      button = L.DomUtil.create('a', 'map-expand-button', container);
      button.href = '#';
      button.setAttribute('role', 'button');
      L.DomEvent.disableClickPropagation(container);
      // The click is the button's alone: it must not reach the map, which would take it for a click on
      // the place under the pointer (and offer to create a place there).
      L.DomEvent.on(button, 'click', (event) => {
        L.DomEvent.stop(event);
        set(!expanded);
      });
      button.innerHTML = EXPAND_ICON;
      button.title = 'Pantalla completa';
      button.setAttribute('aria-label', 'Pantalla completa');
      return container;
    },
  });
  new Expand().addTo(map);
}

// What the categories of the elements of the OpenStreetMap mirror are called (osm-layer.js, osm-link.js).
const OSM_CATEGORY_LABELS = {
  cave: 'Cueva',
  canyon: 'Barranco',
  mine: 'Mina',
  hut: 'Refugio',
  via_ferrata: 'Vía ferrata',
};

const SEARCH_ICON =
  '<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><circle cx="10" cy="10" r="6" fill="none" stroke="currentColor" stroke-width="2"/><path d="M15 15l6 6" stroke="currentColor" stroke-width="2"/></svg>';

// A button under the one of the whole window that opens a box to look for a place by name (a town, a
// valley, a peak) and moves the map there. The API asks an outside geocoder, which is slow and has a
// limit, so it searches when the text is sent, not as it is typed. The found place is marked with a ring
// that stays until the next search.
function addSearchControl(L, map) {
  let ring = null;

  // Where a search takes the map: a ring at the place, and the view on it (on its box if it has one).
  function show(place, zoom = 14) {
    ring?.remove();
    ring = L.circleMarker([place.lat, place.lon], {
      radius: 14,
      weight: 3,
      color: '#c0392b',
      fill: false,
      interactive: false,
    }).addTo(map);
    const b = place.bounds;
    if (b)
      map.fitBounds(
        [
          [b.south, b.west],
          [b.north, b.east],
        ],
        { maxZoom: 16 },
      );
    else map.setView([place.lat, place.lon], zoom);
  }

  const Search = L.Control.extend({
    options: { position: 'topleft' },
    onAdd() {
      const container = L.DomUtil.create('div', 'map-search leaflet-control');
      const bar = L.DomUtil.create('div', 'leaflet-bar', container);
      const button = L.DomUtil.create('a', 'map-search-button', bar);
      button.href = '#';
      button.setAttribute('role', 'button');
      button.title = 'Buscar un sitio';
      button.setAttribute('aria-label', 'Buscar un sitio');
      button.innerHTML = SEARCH_ICON;
      const panel = L.DomUtil.create('div', 'map-search-panel', container);
      panel.hidden = true;
      const form = L.DomUtil.create('form', 'map-search-form', panel);
      const input = L.DomUtil.create('input', '', form);
      input.type = 'search';
      input.maxLength = 200;
      input.placeholder = 'Cueva, pueblo, valle, pico…';
      input.setAttribute('aria-label', 'Buscar un sitio');
      const go = L.DomUtil.create('button', 'btn btn-secondary btn-small', form);
      go.type = 'submit';
      go.textContent = 'Buscar';
      const results = L.DomUtil.create('ul', 'map-search-results', panel);
      L.DomEvent.disableClickPropagation(container);
      L.DomEvent.disableScrollPropagation(container);

      const say = (text) => {
        results.replaceChildren();
        const li = document.createElement('li');
        li.className = 'subtitle';
        li.textContent = text;
        results.append(li);
      };
      // Escape closes the box (and only that: with the map over the whole window it would also give the
      // window back), which is why it is heard before the other listeners of the document.
      const onKey = (event) => {
        if (event.key !== 'Escape') return;
        event.stopPropagation();
        setOpen(false);
      };
      function setOpen(open) {
        panel.hidden = !open;
        if (open) {
          document.addEventListener('keydown', onKey, true);
          input.focus();
        } else {
          document.removeEventListener('keydown', onKey, true);
        }
      }
      L.DomEvent.on(button, 'click', (event) => {
        L.DomEvent.stop(event);
        setOpen(panel.hidden);
      });
      // A click on the map, away from the box, closes it (and offerOnMapClick knows it was only that).
      map.on('click', () => setOpen(false));
      L.DomEvent.on(form, 'submit', async (event) => {
        L.DomEvent.stop(event);
        const text = input.value.trim();
        if (!text) return;
        go.disabled = true;
        say('Buscando…');
        // The elements of the OpenStreetMap mirror that have that name (a cave to make a place of) and the places
        // the geocoder knows; either may fail or find nothing without the other being lost.
        const [elements, sites] = await Promise.allSettled([
          apiFetch(`/osm/search?q=${encodeURIComponent(text)}`),
          apiFetch(`/areas/geocode?q=${encodeURIComponent(text)}`),
        ]);
        const fromOsm = elements.status === 'fulfilled' ? elements.value.elements : [];
        const places = sites.status === 'fulfilled' ? sites.value : [];
        results.replaceChildren();
        const heading = (label) => {
          const li = document.createElement('li');
          li.className = 'map-search-heading subtitle';
          li.textContent = label;
          results.append(li);
        };
        const item = (label, detail, choose) => {
          const li = document.createElement('li');
          const button = document.createElement('button');
          button.type = 'button';
          button.textContent = label;
          if (detail) {
            const small = document.createElement('span');
            small.className = 'subtitle';
            small.textContent = detail;
            button.append(small);
          }
          button.addEventListener('click', () => {
            choose();
            setOpen(false);
          });
          li.append(button);
          results.append(li);
        };
        if (fromOsm.length) {
          heading('En OpenStreetMap, sin añadir todavía al catálogo');
          for (const element of fromOsm) {
            item(
              element.name,
              `${OSM_CATEGORY_LABELS[element.category] ?? element.category} · ${formatDecimal(element.lat, element.lon)}`,
              () => {
                show(element, 17);
                // The layer of its category is put on the map and its popup opened (osm-layer.js).
                map.fire('osmfocus', {
                  elementType: element.type,
                  elementId: element.id,
                  category: element.category,
                });
              },
            );
          }
        }
        if (places.length) {
          if (fromOsm.length) heading('Otros sitios');
          for (const place of places) item(place.name, '', () => show(place));
        }
        if (!fromOsm.length && !places.length) {
          const failed = [elements, sites].find((r) => r.status === 'rejected');
          say(failed ? failed.reason.message : 'No se ha encontrado nada.');
        }
        go.disabled = false;
      });
      return container;
    },
  });
  new Search().addTo(map);
}

// The marker of a place: a dot of the colour of its state.
function placeMarker(L, place) {
  return L.circleMarker([place.lat, place.lon], {
    // A click on a place is for the place, not for the map under it (which offers to create one).
    bubblingMouseEvents: false,
    radius: 8,
    weight: 2,
    color: '#ffffff',
    fillColor: STATE_COLORS[place.state] ?? NO_STATE_COLOR,
    fillOpacity: 0.95,
  });
}

// The marker of one of the other points of a place (parking, approach…): a smaller grey dot, with its
// kind and name as a tooltip, plus `of` (the name of the place) when the map has points of several.
function pointMarker(L, point, of = null) {
  const label = [POINT_KIND_LABELS[point.kind] ?? point.kind, point.name]
    .filter(Boolean)
    .join(': ');
  return L.circleMarker([point.lat, point.lon], {
    bubblingMouseEvents: false,
    radius: 6,
    weight: 2,
    color: '#ffffff',
    fillColor: '#6b6f68',
    fillOpacity: 0.95,
  }).bindTooltip(of ? `${label} (${of})` : label);
}

// Whether the device has a pointer that can hover: a mouse does, a finger does not.
const canHover = () => window.matchMedia('(hover: hover)').matches;

// The information of a place on its marker: a popup when it is clicked or tapped and, where there is a
// pointer that can hover, its name as a tooltip. A finger has no hover, but the browser pretends it
// had one (the tap is also a "mouseover"), which showed the tooltip and the popup together: so where
// nothing can hover (a phone, a tablet) there is no tooltip, and the tap shows the popup alone.
function bindPlaceInfo(marker, place) {
  marker.bindPopup(placePopup(place));
  if (canHover()) marker.bindTooltip(place.name);
  return marker;
}

// What the popup of a place says: its name as a link, what it is and where, and its state.
function placePopup(place) {
  return `<strong><a href="/lugar?id=${place.id}">${escapeHtml(place.name)}</a></strong><br>
    ${escapeHtml([placeTypeLabel(place), STATE_LABELS[place.state]].filter(Boolean).join(' · '))}<br>
    <span class="subtitle">${escapeHtml(place.where ?? '')}</span>`;
}

// A click on a map where there is nothing offers to do something there: a small popup with the
// coordinates and a button, `label`, that calls `open({ lat, lon })` (to open the form of a new place or of
// a new point with them written in). It has its close button, and a click elsewhere only closes it.
function offerOnMapClick(L, map, { label, open }) {
  // Leaflet closes an open popup on a click outside it, and then still sends the click: that one only
  // closed the popup, so it must not open another. The close and the click come in the same turn.
  let justClosed = false;
  map.on('popupclose', () => {
    justClosed = true;
    setTimeout(() => {
      justClosed = false;
    }, 0);
  });
  // Likewise a click that closes the list of layers (Leaflet closes it on a click on the map, which on a
  // touch screen is the only way) or the box to search a place must only close it. It is told before they
  // close, at "preclick".
  // A touch screen also fakes mouse events before the click, and leaving the list with the "mouse" closes
  // it, so by "preclick" it may be closed already: whether it was open is noted when the finger goes down.
  let closingLayers = false;
  const isOpen = () =>
    !!map
      .getContainer()
      .querySelector('.leaflet-control-layers-expanded, .map-search-panel:not([hidden])');
  let openAtPress = false;
  map.getContainer().addEventListener(
    'pointerdown',
    () => {
      openAtPress = isOpen();
    },
    true,
  );
  map.on('preclick', () => {
    closingLayers = openAtPress || isOpen();
    openAtPress = false;
  });
  map.on('click', (event) => {
    if (justClosed) return;
    if (closingLayers) {
      closingLayers = false;
      return;
    }
    const { lat, lng } = event.latlng;
    const box = document.createElement('div');
    const where = document.createElement('p');
    where.className = 'subtitle';
    where.textContent = formatDecimal(lat, lng);
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'btn btn-secondary btn-small';
    button.textContent = label;
    button.addEventListener('click', () => {
      map.closePopup();
      open({ lat, lon: lng });
    });
    box.append(where, button);
    L.popup().setLatLng(event.latlng).setContent(box).openOn(map);
  });
}

// The key of the colours, as markup.
function mapLegend() {
  return [...Object.entries(STATE_LABELS), ['', 'Otro']]
    .map(
      ([state, label]) =>
        `<span class="map-legend-item"><span class="map-legend-dot" style="background:${STATE_COLORS[state] ?? NO_STATE_COLOR}"></span>${escapeHtml(label)}</span>`,
    )
    .join('');
}

export {
  OSM_CATEGORY_LABELS,
  canHover,
  createMap,
  loadLeaflet,
  mapLegend,
  offerOnMapClick,
  bindPlaceInfo,
  placeMarker,
  pointMarker,
  placePopup,
  STATE_COLORS,
};
