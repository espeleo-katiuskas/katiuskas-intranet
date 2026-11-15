import { apiFetch, escapeHtml } from './api.js';
import { formatDecimal, formatUtm, mapUrl } from './coordinates.js';

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

// A coordinate for reading: a link that shows it on a map, the same in UTM next to it and, if it is
// known, its altitude in metres above sea level.
function coordinatesHtml(lat, lon, altitude = null) {
  const utm = formatUtm(lat, lon);
  return (
    `<a href="${escapeHtml(mapUrl(lat, lon))}" target="_blank" rel="noopener noreferrer">${formatDecimal(lat, lon)}</a>` +
    (utm ? ` <span class="subtitle">UTM ${utm}</span>` : '') +
    (altitude != null ? ` <span class="subtitle">${altitude} m</span>` : '')
  );
}

// The measures of a cave that has either, short: "120m ↓ · 300m ↔" (depth and horizontal length), each
// with what it is as its tooltip, and each on one line. Empty if there are none.
function measuresHtml(place) {
  return [
    place.depth_m != null
      ? `<span class="nowrap" title="${place.depth_m} m de profundidad">${place.depth_m}m ↓</span>`
      : null,
    place.horizontal_length_m != null
      ? `<span class="nowrap" title="${place.horizontal_length_m} m de desarrollo horizontal">${place.horizontal_length_m}m ↔</span>`
      : null,
  ]
    .filter(Boolean)
    .join(' · ');
}

export {
  coordinatesHtml,
  loadGeologies,
  loadSubtypes,
  loadTypes,
  measuresHtml,
  placeTypeLabel,
  POINT_KIND_LABELS,
  STATE_HELP,
  STATE_LABELS,
  stateBadge,
};
