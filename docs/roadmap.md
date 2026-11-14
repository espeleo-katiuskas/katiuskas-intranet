# Pending work

What is planned and not done yet. Each item says what is wanted and the questions to settle before
building it; nothing in it is decided. When one is started its design goes to the usual documents
(`api.md`, `database.md`, `backend-internals.md`, `frontend.md`) and the item is removed from here.

## Catalogue of places (caves, shafts, canyons…)

The first phase is done and documented (`api.md`, `database.md`, `backend-internals.md`,
`frontend.md`): the types, the places with their kind, state, main coordinate and texts, their other
points, and the list and the page of each. What is left, in this order:

- **Files**: photos, texts and documents attached to a place (like the memoria of an activity: a
  text or a file with a title, on disk keyed by the row id and never rewritten). To settle: the
  types allowed, the maximum size, who removes what others added, and thumbnails for the photos.
- **The map**: all the places on one map, grouped when the view is wide (Leaflet and its clustering,
  copied into the repository, with a light listing of the places for it), and a small map on the
  page of each place with all its points. To settle: the source of the tiles (a topographic and a
  satellite layer; OpenStreetMap's standard tiles limit heavy use and the IGN is the Spanish
  official source) and whether the grouping stays in the browser (fine for a few thousand places) or
  moves to the server.
- **Activities**: a table joining places and activities, shown on the page of each activity and on
  that of each place (what has been done there).
- **Tracks**: the way of an approach or of a canyon, as a GPX attached to the place and drawn on its
  map.
- **Later**: a technical sheet made of pitches (with their lengths, which would let the rope needed
  be worked out and linked with the material), other fields of a canyon (drop, length, difficulty),
  a catalogue code, and whether the location of some places should be hidden from some people (today
  all who can sign in see everything).
