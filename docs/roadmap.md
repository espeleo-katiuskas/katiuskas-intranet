# Pending work

What is planned and not done yet. Each item says what is wanted and the questions to settle before
building it; nothing in it is decided. When one is started its design goes to the usual documents
(`api.md`, `database.md`, `backend-internals.md`, `frontend.md`) and the item is removed from here.

## Catalogue of places (caves, shafts, canyons…)

The first phase is done and documented (`api.md`, `database.md`, `backend-internals.md`,
`frontend.md`): the types, the places with their kind, state, main coordinate and texts, their other
points, their photos and documents, the map (on `/lugares` and on each place), and the list and
the page of each. What is left, in this order:

- **Activities**: a table joining places and activities, shown on the page of each activity and on
  that of each place (what has been done there).
- **Tracks**: the way of an approach or of a canyon, as a GPX attached to the place and drawn on its
  map.
- **Systems**: a group of caves and shafts that are connected underground, which is one cave as far
  as the underground goes although each entrance is a place of its own (and has its own coordinate,
  photos and state). A system has a name and its own texts, and its places are listed on its page
  and on the page of each one; on the map its entrances are shown together, and the depth and the
  development of the whole may be worked out or given. To settle: whether a place may be in more
  than one system (a connection found later may join two), whether only caves can be in one (a
  canyon cannot, but a shaft is a cave), whether the connection is a fact of the system or a link
  between two places (to say which entrance joins which, and whether it is a proven connection or
  only a suspected one), and who creates, changes and dissolves them (any member, like a place).
- **OpenStreetMap layer**: the cave entrances of OpenStreetMap (`natural=cave_entrance`, about 9,400
  in Spain, almost all with a name and a coordinate, few with an altitude, a description or a web
  page) shown on the map as grey dots, apart from the catalogue, so that it keeps being the club's
  own. A member who sees one worth having presses "Añadir al catálogo" and gets the dialog of a new
  place with the name, the other names, the coordinate and the altitude filled in, and the id of the
  OpenStreetMap element kept in the new place (a column in `places`) so that the same entrance is
  not offered again, nor added twice. They would be asked for the area on view only, by the API (an
  Overpass query with a cache, not from the browser: the public instances ask for light use and an
  identifiable client) and never stored. The zone of a place added this way would be proposed with
  `GET /areas/locate`, which already exists (the form of a place uses it when the coordinates are
  written), and the member confirms or changes it. To settle: what tells a shaft from a cave (only
  the name does), the state a place added this way starts with, and the credit: the data is ODbL, so
  the layer says where it comes from and the id of OpenStreetMap is kept, and the catalogue must not
  be published as a database without looking at what that asks for.
- **Later**: a technical sheet made of pitches (with their lengths, which would let the rope needed
  be worked out and linked with the material), other fields of a canyon (drop, length, difficulty),
  a catalogue code, and whether the location of some places should be hidden from some people (today
  all who can sign in see everything).
