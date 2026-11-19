import { apiFetch, escapeHtml } from './api.js';
import { formatDecimal, googleMapsUrl } from './coordinates.js';

// What the pages of the places (/lugares and /lugar) share.

// The state of a place says under which conditions it can be visited.
const STATE_LABELS = {
  open: 'Abierta',
  permit_required: 'Hace falta permiso',
  tourist: 'Turística (de pago)',
  closed: 'Cerrada',
};

// What each state means, for whoever has to choose one and for whoever reads it.
const STATE_HELP = {
  open: 'Se puede ir libremente.',
  permit_required: 'Hay que pedir permiso antes de ir.',
  tourist: 'Está explotada y hay que pagar para visitarla.',
  closed: 'No se puede visitar.',
};

// The class of the badge of each state.
const STATE_BADGES = {
  open: 'badge-ok',
  permit_required: 'badge-warn',
  tourist: 'badge-info',
  closed: 'badge-out',
};

const POINT_KIND_LABELS = {
  parking: 'Aparcamiento',
  approach: 'Inicio de la aproximación',
  waypoint: 'Punto del camino',
  access: 'Acceso',
  other: 'Otro',
};

// The badge of a state, or nothing for a place whose type has none.
function stateBadge(state) {
  if (!state) return '';
  return `<span class="badge ${STATE_BADGES[state] ?? ''}">${escapeHtml(STATE_LABELS[state] ?? state)}</span>`;
}

// The types of place, with how many places have each and the kind that decides what a place of the
// type has (cave, canyon or general).
function loadTypes() {
  return apiFetch('/places/types');
}

// The systems of cavities, with their cavities: the form of a cavity offers them.
function loadSystems() {
  return apiFetch('/systems');
}

// What a cavity is (cueva, sima, torca, mina, abrigo…), with how many places have each.
function loadSubtypes() {
  return apiFetch('/places/subtypes');
}

// What kind of place it is, as people say it: a cavity by its subtype ("Sima"), any other place by
// its type.
function placeTypeLabel(place) {
  return place.subtype ?? place.type_name;
}

// What a cave can be formed in (limestone, gypsum, ice, lava…), with how many places have each.
function loadGeologies() {
  return apiFetch('/places/geologies');
}

// A coordinate for reading: the decimal degrees, "41.52016, -3.63606", with links that show it on
// Google Maps and on OpenStreetMap ("41.52016, -3.63606 · Google Maps · OpenStreetMap") and, if it is
// known, its altitude in metres above sea level.
// Whether the browser is on a phone that can say where the person is: it has the geolocation API, the page
// is a secure context (the only place a browser answers it) and its main way of pointing is a finger. A
// laptop has the API too, but nobody walks to a cave with it.
function canGuide() {
  return (
    Boolean(navigator.geolocation) &&
    window.isSecureContext &&
    window.matchMedia('(pointer: coarse)').matches
  );
}

// The coordinates of a place or a point with a link to Google Maps, and, on a phone, a button to the page that
// says how far it is and which way (/brujula). `guide` is { name, placeId }: what the page calls the point
// and the place it goes back to. The altitude, if there is one, goes after.
function coordinatesHtml(lat, lon, { altitude = null, guide = null } = {}) {
  const link = (url, name) =>
    `<a href="${escapeHtml(url)}" target="_blank" rel="noopener noreferrer">${name}</a>`;
  const query = guide
    ? new URLSearchParams({
        lat,
        lon,
        nombre: guide.name,
        ...(guide.placeId ? { lugar: guide.placeId } : {}),
      })
    : null;
  return (
    `<span class="nowrap">${formatDecimal(lat, lon)}</span>` +
    ` · ${link(googleMapsUrl(lat, lon), 'Google Maps')}` +
    (guide && canGuide()
      ? ` <a class="btn btn-secondary btn-small" href="/brujula?${escapeHtml(query.toString())}">Guiarme hasta aquí</a>`
      : '') +
    (altitude != null ? ` <span class="subtitle">${altitude} m</span>` : '')
  );
}

export {
  coordinatesHtml,
  loadGeologies,
  loadSubtypes,
  loadSystems,
  loadTypes,
  placeTypeLabel,
  POINT_KIND_LABELS,
  STATE_HELP,
  STATE_LABELS,
  stateBadge,
};
