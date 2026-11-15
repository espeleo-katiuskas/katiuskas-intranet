import { escapeHtml } from './api.js';
import { formatDecimal } from './coordinates.js';
import { placeTypeLabel, STATE_LABELS } from './places-common.js';

// What the maps of the places share: Leaflet (and its clustering) loaded the first time a map is shown,
// the three layers of tiles, the markers and the legend. Leaflet is copied into web/vendor, so the page
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
// with their credit; OpenStreetMap's own tiles ask for light use, which is that of a club.
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
// Spain in view until something is fitted. Gives Leaflet's `L` and the map.
async function createMap(element) {
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
  L.control
    .layers(Object.fromEntries(LAYERS.map((l) => [l.name, layers[l.id]])), null, {
      collapsed: true,
    })
    .addTo(map);
  L.control.scale({ imperial: false }).addTo(map);
  map.on('baselayerchange', (event) => {
    const found = LAYERS.find((layer) => layer.name === event.name);
    if (found) rememberLayer(found.id);
  });
  return { L, map };
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
  map.on('click', (event) => {
    if (justClosed) return;
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
  return [...Object.entries(STATE_LABELS), ['', 'Sin estado']]
    .map(
      ([state, label]) =>
        `<span class="map-legend-item"><span class="map-legend-dot" style="background:${STATE_COLORS[state] ?? NO_STATE_COLOR}"></span>${escapeHtml(label)}</span>`,
    )
    .join('');
}

export {
  createMap,
  loadLeaflet,
  mapLegend,
  offerOnMapClick,
  placeMarker,
  placePopup,
  STATE_COLORS,
};
