import * as outbox from './outbox.js';

// What was done with no connection and is still in the outbox shows on the phone as if it were done: the
// pages draw what the server said with the person's pending changes on top. The overlay is applied to the
// data a page has just loaded, never saved, so when an entry is sent (and the page loads again) the
// server's own data is what shows. An edit made on top of what is shown takes what is shown as what the
// person saw (`base`), which is what the outbox needs to tell a real clash from its own earlier entries.

// The pending entries of a person of the given kinds, in the order they were made; none if the storage is
// not there (a private window).
export async function pendingEntries(login, kinds) {
  try {
    return await outbox.entries((e) => e.login === login && kinds.includes(e.kind));
  } catch {
    return [];
  }
}

// The names that go with the ids of a place, from the catalogues of the page (`types`, `subtypes`,
// `geologies`, `systems`): what a place says besides its ids.
function withNames(place, { types = [], subtypes = [], geologies = [], systems = [] }) {
  const named = (list, id) => (id == null ? undefined : list.find((x) => x.id === id)?.name);
  const type = types.find((t) => t.id === place.type_id);
  if (type) {
    place.type_name = type.name;
    place.kind = type.kind;
  }
  place.subtype = named(subtypes, place.subtype_id);
  place.geology = named(geologies, place.geology_id);
  place.system = named(systems, place.system_id);
  return place;
}

// `place` (an item of the list or a whole place) with the pending edits of it on top, in order. Returns a
// new object; the fields are those of the API, so `changes` go over it as they are.
function editedPlace(place, entries, catalogues) {
  const edits = entries.filter((e) => e.kind === 'place-edit' && e.placeId === place.id);
  if (!edits.length) return place;
  const edited = { ...place };
  for (const e of edits) Object.assign(edited, e.changes);
  return withNames(edited, catalogues);
}

// The whole place of a page with its pending edits, and its pending points added to its own (they have no
// id yet and say `pending`).
export function overlayPlace(place, entries, catalogues) {
  const edited = editedPlace(place, entries, catalogues);
  const points = entries
    .filter((e) => e.kind === 'place-point' && e.placeId === place.id)
    .map((e) => ({ ...e.point, id: null, place_id: place.id, pending: true }));
  return points.length ? { ...edited, points: [...(edited.points ?? []), ...points] } : edited;
}

// The list of places with the pending edits on top and the places made with no connection added at the
// end, each one `pending` with an id that is not a number (they have no page yet). The zone it says is the
// guess of the form, since nothing else is known.
export function overlayList(list, entries, catalogues) {
  const shown = list.map((place) => editedPlace(place, entries, catalogues));
  for (const e of entries.filter((x) => x.kind === 'place-new')) {
    const b = e.body;
    shown.push(
      withNames(
        {
          id: `pending-${e.id}`,
          pending: true,
          type_id: b.type_id,
          name: b.name,
          alt_names: b.alt_names ?? [],
          state: b.state || undefined,
          subtype_id: b.subtype_id ?? undefined,
          system_id: b.system_id ?? undefined,
          geology_id: b.geology_id ?? undefined,
          area_id: e.areaId ?? e.guessAreaId ?? null,
          area_ids: [],
          where: e.guessWhere || 'Zona por determinar',
          area_names: e.guessWhere ?? '',
          lat: b.lat,
          lon: b.lon,
          altitude_m: b.altitude_m ?? undefined,
          depth_m: b.depth_m ?? undefined,
          horizontal_length_m: b.horizontal_length_m ?? undefined,
          activity_count: 0,
          visibility: { place: 'club', location: 'club', content: 'club', protected: false },
        },
        catalogues,
      ),
    );
  }
  return shown;
}
