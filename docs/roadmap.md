# Pending work

What is planned and not done yet. Each item says what is wanted and the questions to settle before
building it; nothing in it is decided. When one is started its design goes to the usual documents
(`api.md`, `database.md`, `backend-internals.md`, `frontend.md`) and the item is removed from here.

## Catalogue of places (caves, shafts, canyons…)

The first phase is done and documented (`api.md`, `database.md`, `backend-internals.md`,
`frontend.md`): the types, the places with their kind, state, main coordinate and texts, their other
points, their photos and documents, the map (on `/lugares` and on each place), the activities that
went to each place, the systems of cavities, and the list and the page of each. What is left, in
this order:

- **Tracks**: the way of an approach or of a canyon, as a GPX attached to the place and drawn on its
  map.
- **OpenStreetMap**: built, as a local mirror of the elements that matter, a layer on the map, a
  search by name, "Añadir al catálogo", the link of a place to its element and the notice of what
  changed there. What is left (a few small things, and the decisions about private locations and
  about contributing back to OpenStreetMap) is in section 5 of `osm-integration.md`.
- **Coordinates for the zones**: `lat` and `lon` (optional) on `areas`, filled once from an open
  source (the IGN's *Nomenclátor Geográfico de Municipios y Entidades de Población* for Spain,
  GeoNames for countries and provinces), matching the names of the seed (migration 00062), which
  will not all match (bilingual names, articles) and need a review. It would make the search of a
  zone on the map instant and independent of Nominatim, and would allow "see this zone on the map",
  ordering places by distance and centring the map on the zone of an activity. Today the map
  searches names with `GET /areas/geocode`, which asks Nominatim, so this is only worth it if one
  of those is wanted.
- **Later**: a technical sheet made of pitches (with their lengths, which would let the rope needed
  be worked out and linked with the material), other fields of a canyon (drop, length, difficulty),
  a catalogue code, and whether the location of some places should be hidden from some people (today
  all who can sign in see everything).
