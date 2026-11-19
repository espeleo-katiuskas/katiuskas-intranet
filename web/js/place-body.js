// The body of the edit of a place (`PUT /places/{id}`), and what an edit made with no connection
// has to say to be sent later. No imports: outbox.js uses it, and must not depend on the pages.

// What the API asks for when it replaces a place, from a place as the API gives it: all of it,
// since what is left out is cleared.
export function placeBody(place) {
  return {
    type_id: place.type_id,
    name: place.name,
    alt_names: [...(place.alt_names ?? [])],
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
}

// What each field is called for the person who reads that it changed.
export const FIELD_LABELS = {
  type_id: 'Tipo',
  name: 'Nombre',
  alt_names: 'Otros nombres',
  state: 'Estado',
  subtype_id: 'Subtipo',
  system_id: 'Sistema',
  area_id: 'Zona',
  lat: 'Coordenadas',
  lon: 'Coordenadas',
  altitude_m: 'Altitud',
  access: 'Cómo llegar',
  description: 'Descripción',
  depth_m: 'Profundidad',
  horizontal_length_m: 'Desarrollo horizontal',
  geology_id: 'Geología',
  technical_sheet: 'Ficha técnica',
};

// Two values of a field are the same: numbers within a hair (a coordinate goes through text), the
// rest as they are, lists included.
export function sameValue(a, b) {
  if (typeof a === 'number' && typeof b === 'number') return Math.abs(a - b) < 1e-7;
  return JSON.stringify(a ?? null) === JSON.stringify(b ?? null);
}

// The fields of `body` that differ from `place`: { base, changes }, with what the place had and what
// the person wants for each, only for those.
export function diffPlace(place, body) {
  const original = placeBody(place);
  const base = {};
  const changes = {};
  for (const [field, value] of Object.entries(body)) {
    if (field in original && !sameValue(value, original[field])) {
      base[field] = original[field];
      changes[field] = value;
    }
  }
  return { base, changes };
}

// The names of the fields of a list of changes, once each, as a text.
export function describeFields(fields) {
  return [...new Set(fields.map((f) => FIELD_LABELS[f] ?? f))].join(', ');
}
