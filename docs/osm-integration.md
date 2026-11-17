# OpenStreetMap as the base of the places (draft)

A working document, not a decision: it lists what would have to be done to make OpenStreetMap
(OSM) the main source of the catalogue of places, so that it can be refined before anything is
built. When a part is decided its design goes to the usual documents (`api.md`, `database.md`,
`backend-internals.md`, `frontend.md`) and it is removed from here. Questions that need an answer
are marked **To settle**, and collected at the end.

**Status.** Phase 2 of section 13 is built: the mirror (`osm_elements`, `osm_sync_runs` and
`places.osm_type`/`osm_id`), `cmd/osmsync` and its timer. How it works is in `database.md` and
`backend-internals.md` ("The mirror of OpenStreetMap") and how it is installed in `environments.md`
("OpenStreetMap mirror"); sections 5 and 11 below keep only what is still to be settled or done.
What the first version does differently from this draft: the mark of a gone element is `deleted_at`
(a time) and not a flag; each row has a `category` (cave, canyon, mine, hut, via ferrata) and
`content_hash`; ways and relations are in from the start; canyons are read apart from the others,
since a `route=canyoning` relation is a list of ways and `osmium export` does not turn it into a
shape; mines are loaded as a category of their own (adits, shafts and historic mines, which the club
explores like any cavity); and sinkholes are not loaded. Caves, canyons and mines come first, as the
club's activity, and via ferratas, which it only uses now and then, last.

## 1. What is wanted

- A place may have, optionally, the OSM element it is (type and id).
- A local copy of the OSM elements that matter, to start with every `natural=cave_entrance`
  (Spain first, maybe the whole world), refreshed now and then, so that no page or request of the
  intranet asks OSM anything.
- Every element of that copy is available as a place: it can be seen on the maps and in the
  lists, used (an activity goes to it, a memoria entry names it) and completed or corrected with
  the club's own data.
- Later, maybe: send the corrections back to OSM.
- OSM's services must not be loaded: no queries per page view, and the bulk data comes from files
  meant to be downloaded.

## 2. What is known

- Today OSM has about 73,500 `natural=cave_entrance` elements in the world: 72,800 nodes, 684
  ways and 8 relations (taginfo, November 2026). About 9,400 are in Spain. It is small: a table
  with all of them is a few tens of megabytes, and a bounding-box query over it is instant with a
  plain index on latitude and longitude, so **PostGIS is not needed** for this (it would be for
  the areas, see 6).
- Most have a name and a coordinate; few have an altitude, a description or a web page. OSM does
  not say what kind of cavity it is (cave, shaft, mine…) in any reliable tag, so that part stays
  the club's.
- The data is under the **ODbL** (see 3).
- The existing design pieces that this reuses: the zone lookup of a point (`GET /areas/locate`,
  Nominatim), the zones seeded from the INE (migration 00062), the catalogue of places
  (migration 00061 and following) and the map components (`js/map-common.js`).

## 3. Licence: what the ODbL asks

To be confirmed by the board (this is a reading, not legal advice):

- **Attribution**: "© OpenStreetMap contributors" wherever the data is shown (the maps already
  say it for the tiles; the data needs it too, on the page of a place that has OSM data, and a
  link to the copyright page).
- **Share-alike** applies to a *derivative database* that is *publicly used*: if the club's
  database mixes OSM data with its own and that database is made public, it must be offered
  under the ODbL. The intranet is behind a login, which is not public use, but the point of
  "publicly" is something to settle if the catalogue is ever opened (see 10).
- The way to keep the club's own work free of any doubt is to **keep the OSM copy in its own
  table** and join it to the club's data by the OSM id (a *collective* database, not a merged
  one), and to store the club's corrections as the club's, never as altered OSM rows.
- Keep the **date and the version** of what was downloaded (also useful for the diffs).
- Contributing back (see 9) has its own rules: the Contributor Terms and the community's import
  guidelines.

## 4. Where the data comes from

| Source | What it gives | Good for | Bad for |
|---|---|---|---|
| Overpass API (public) | the elements that match a query, as JSON | one-off exploration | regular use: often at capacity (half a minute per query, or no answer), asks for light use |
| Geofabrik country extracts (`download.geofabrik.de`) | the whole OSM of a country as `.osm.pbf` (about a gigabyte for Spain), refreshed daily, with daily diffs | the first load and regular reloads | needs a tool to cut it (`osmium`); the whole country for a few thousand points |
| Planet file and its replication diffs (`planet.openstreetmap.org`) | everything, with minutely/hourly/daily diffs | the whole world | tens of gigabytes for the first copy; too much for 73,000 elements |
| Our own Overpass instance | queries without limits | frequent complex queries | a service to run: not worth it for this |

**Proposal**: Geofabrik extracts, one per country that the club wants (Spain first; others added
as needed, or a continent), cut down with `osmium tags-filter` to the elements that matter, so
that what is kept is tiny and the heavy part is only a download and a pass over a file, done by
the host a few times a week at a quiet hour, with a conditional request (`If-Modified-Since`) and
a client name that says who it is. A single Overpass query for `natural=cave_entrance` in the
world is an acceptable way to get the very first load of the other countries in one go (about
73,000 elements), if the public instance answers at that moment.

**Spain takes two files**, because Geofabrik cuts by region and not by country: the Spanish
extract (`europe/spain-latest.osm.pbf`, 1.5 GB in November 2026, which also has the Balearic
Islands, Ceuta, Melilla and the other small islands) and the Canary Islands
(`africa/canary-islands-latest.osm.pbf`, 60 MB, in the African extracts because of where the
islands are). The two do not overlap, so each is filtered on its own and the results are added
together. So the list of what is downloaded is a configuration of *files*, not of countries, and
each file has its own URL, date and `osm_sync_runs` row. The `-latest` addresses redirect to the
file of the day (`spain-261116.osm.pbf`), and the redirect is what `If-Modified-Since` should be
sent to.

A cheaper way to stay current between full reloads is a daily Overpass query with
`newer:"<last run>"` for the same tags: it returns only what changed. It does not say what was
*deleted*, so a full reload (or the Geofabrik diffs) is still needed now and then to find those.

**To settle**: how often (weekly looks enough for caves), which countries, and whether the host
may have `osmium-tool` installed (it is a package; the deployment is without Docker,
`docs/deployment.md`).

## 5. What would be stored

A table apart from `places`, one row per OSM element that is relevant (a mirror, never edited by
members):

(Built: the columns are in `database.md`, "`osm_elements`". The log of the loads is `osm_sync_runs`,
which `adminstatus` could show.)

In `places` (built): `osm_type` and `osm_id`, both null or both set, **unique** (an element is at
most one place), with a foreign key to the mirror.

**Nodes, ways and relations.** A place is a point, but OSM draws many things as shapes. The
Refugio de la Zapatilla is a node, and another `tourism=wilderness_hut` is a *way*: a closed line
of nodes that goes around the building, which OSM reads as a polygon. For caves this hardly
matters (684 ways and 8 relations of 73,500), but for huts it does: of the 17,100 elements with
`tourism=wilderness_hut`, only 8,900 are nodes, so about half are ways or relations, and the
same goes for `alpine_hut`. So **ways and relations are in from the first version**:

- The extract is cut with `osmium tags-filter` (which keeps the nodes a way refers to unless told
  not to) and exported with `osmium export`, which builds the polygon of a closed way and the
  multipolygon of a relation, so no geometry is worked out by hand.
- The place gets a **point inside the shape**, not the average of its corners: the centre of a
  concave outline (an L-shaped building, a ring) can fall outside it. Shapely's
  `representative_point()` (or PostGIS's `ST_PointOnSurface`, if it is ever added) gives one that
  is always inside. For an open way (a line, a via ferrata) it is the middle of the line, and the
  whole line is kept for drawing it.
- The outline is stored (`geometry`) so that a map can draw the building, or the track of a via
  ferrata, instead of only a dot; the dot is what the lists and the clusters use.
- The key of an element is **type and id**, and an id is not shared between types: node 123 and
  way 123 are different things. Everything that refers to one (the foreign key of a place, the
  link to osm.org, the editor) carries both (`osm.org/way/123`).
- A mapper can **replace a node by a way** (draw the building around the point that stood for it)
  and the id changes: the old node is deleted and the way is new. A place that pointed at the node
  would be flagged as having lost its element (8) when it has really moved to a new one a few
  metres away. The matching by distance and similar name that offers links (8) also covers this:
  a deleted element with a new one at the same spot and the same name is offered as its
  replacement, to be accepted with one click.

**To settle**: whether the mirror keeps every tag (simple, bigger) or only the ones that are read
(smaller, but a new need means a reload).

## 6. Zones of the elements

A place needs a zone (`area_id`, at least the country). Asking Nominatim for every element is out
of the question (one request a second, and bulk use is against its policy: 73,000 would take a day
and is abuse). The options:

1. Compute it **offline with the boundaries from the same extract** (`boundary=administrative`
   with `admin_level` 8 and, in Spain, `ref:ine`), once per load, with `osmium` plus a script
   (`ogr2ogr`/shapely) that finds the municipality of each point. This is the solid way.
2. Add PostGIS to the database just for this (and `ST_Contains`). It is simpler to write but it is
   a new dependency for production, the backup and the prod→testing copy; today none of it is
   needed.
3. Leave `area_id` empty until a member touches the place, and then propose it with
   `GET /areas/locate` as the form does today. Cheap, but the lists cannot say "in Madrid" for
   elements nobody has opened.

**Proposal**: 1 for Spain (where the seed has the INE codes) and the country alone, by a coarse
box or by `reverse` of the extract's country polygons, for the rest. The result is stored in the
mirror (`area_id`, nullable) so that it is not computed on every request.

## 7. How the elements become places

This is the central decision. "Every OSM element is available as a place" can mean:

- **A. Eager import**: every element gets a row in `places`. All the existing code (lists, maps,
  activities, memoria, files, filters) works with no change, and a place is a place. But a row of
  `places` needs a type, a subtype (required for a cavity), a state, a zone, `created_by` and
  `updated_by` (a person), none of which OSM has: they would be made up (a system user, a
  default subtype "Cueva", state "open"), and a nightly import would put 9,400 (or 73,000)
  rows in the lists and the counters that nobody asked for.
- **B. Lazy promotion**: the mirror is shown on the maps and found by the search as a *candidate*
  (grey dot, "de OpenStreetMap"); the first time a member **uses** it (opens it and presses
  "Usar", adds it to an activity or to a memoria entry, or edits it) a row of `places` is created
  from it, with the OSM id, the name, the other names, the point and the altitude copied, and the
  member chooses what OSM does not say (the subtype, the state). Until then it costs nothing and
  pollutes nothing.
- **C. Virtual**: a view that unions `places` and the mirror, so that everything is a "place" to
  the API. Elegant, but every query, filter and counter must understand two sources and the
  mirror would need the columns of `places` (type, state) that it lacks.

**Proposal**: B. It gives "all the points are there and can be used" without making up data, and
the cost is a second source in the map and in the search box, which is small. A needs a decision
on the made-up fields; C is the most work. The existing catalogue (the club's own places) keeps
being the club's, and a member can **link** an existing place to an OSM element (see 8) instead of
creating a duplicate.

**To settle**: A, B or C; and, if B, whether "using" is one explicit step or happens when a
member edits.

## 8. Data of both sides

Once a place has an OSM element there are two values for the same thing. Rules, to be refined:

- The OSM value is **shown** as such ("según OpenStreetMap: …", with the date), never silently
  merged into the club's.
- The club's value, when there is one, **wins** in the club's own pages; a field the club left
  empty may show OSM's. Editing a field writes the club's value in `places` and leaves the mirror
  alone.
- Mapping of tags to fields, to be made looking at what the data really has (taginfo and a sample
  of Spain): `name`, `alt_name`/`name:*` → names, `ele` → altitude, `description` →
  description, `website`, `wikidata`/`wikipedia`, `cave:ref` (the catalogue number of a cavity,
  6,000 values in OSM), `access`/`fee` → state (perhaps only as a hint), `check_date`.
- The coordinate: when the club moved it from OSM's on purpose, the place keeps its own; a
  place that is still at OSM's point follows OSM when the element is moved (a flag
  `follows_osm_position`, or comparing at load time).
- When a **load changes an element** a place points at, the place is not changed; it is flagged
  ("OSM ha cambiado: nombre, altitud") so that someone looks, and the flag is cleared by looking.
- When an element **disappears** (deleted, or no longer matching the tags), the place stays and
  says so.
- **Duplicates**: a candidate within about 200 metres of a club place and with a similar name
  (accents and case ignored, as `areas.Normalize` does) is offered as a possible link; there is a
  one-time matching of the places that already exist (a list to review by hand, not automatic).

**To settle**: which fields follow OSM and which are only suggestions, and who resolves a
conflict (anybody, or admin and board).

## 9. Contributing back to OSM (optional, later)

Everything here is a possibility, and the first versions should not depend on it:

- It needs **OAuth 2** with an OSM account (the API is 0.6; accounts are personal). The choices
  are each member's own account, which keeps the authorship honest and means the intranet holds
  tokens of other services per person, or one account of the club, which is simpler and goes
  against OSM's guidance of personal accounts. A development server (`api06.dev.openstreetmap.org`)
  exists for trying it.
- The club's edits would be queued as **pending edits** (`osm_pending_edits`: element, base
  version, the tags to set, who proposed, who approved), shown to admin/board, and sent as one
  changeset per approval. An edit is made on the current version of the element: if it moved on,
  it is a conflict to look at.
- **Rules of the OSM community**: only what the contributor knows from the ground or from sources
  that allow it (never copied from a copyrighted guide or database), and a **mass upload** is an
  *import* that must be discussed with the community first. One element by hand is not.
- **Sensitive places**: some cave locations are left out of public maps on purpose (to protect
  the cave or the visitors). Sending a location that the club keeps private to OSM cannot be undone
  and OSM is public, and an OSM point that we copy becomes club data. This ties with "hide the
  location of some places" of `docs/roadmap.md` and has to be decided first.
- Cheaper alternative: a "Abrir en OpenStreetMap" link (and "Editar" with the iD editor
  prefilled at that element) on the page of a place, with no integration at all.

## 10. The intranet side

Once there is a mirror:

- **API**: `GET /osm/features?bbox=…` (the candidates in the view, with a cap and clustering or
  simplification at low zoom), the search by name across both sources (`/places?search=` plus
  the mirror's names, or one endpoint for both), `POST /places` accepting an OSM id, and
  `PUT /places/{id}/osm` to link or unlink. `osm_*` fields in the place object.
- **Maps**: the candidates as grey dots below the club's places, loaded for the bounding box of
  the view (not all 73,000 at once), the existing "Añadir al catálogo" dialog opened with the
  data filled in, and the credit of OSM in the attribution.
- **Place page**: where the data comes from, the OSM link, what OSM says next to what the club
  says (see 8), and the flag of a change.
- **Lists and counters**: only places (the club's); the candidates are not counted.
- **Admin**: the state of the load (last run, errors) and a button to run it now, perhaps.
- **Opening the catalogue to the public** would make the "publicly used" of the ODbL real: it is
  out of this plan until it is looked at on purpose.

## 11. The load itself

Built (`backend/cmd/osmsync`, `internal/osmmirror`; see `backend-internals.md`), except for step 3
of the original plan, the zone of each point, which is section 6: the elements are loaded without a
zone for now. Tests: the filters, the points of the shapes (a square, an L), the load of a changed,
a deleted and a returned element, the refusal of a damaged extract and the conditional download, all
with tiny inputs (`internal/osmmirror/*_test.go`). Testing does not run the load, because it gets
production's database with the mirror in it.

**Still to see**: how long the load takes on the club's host, what the Geofabrik policy says about
how often an extract may be downloaded (a weekly run is well within what is usual), and whether the
diffs (`.osc` files) are worth using instead of the whole extract if it is ever run more often.

## 12. Which elements are relevant

A list of filters in configuration, each with the place type it suggests, so that it grows
without code:

| Filter | Suggests | Notes |
|---|---|---|
| `natural=cave_entrance` | Cavidad | the first and main one |
| `man_made=adit`, `man_made=mineshaft`, `historic=mine`, without `resource=water` | Cavidad, subtype Mina | built; see "Mines" |
| `natural=sinkhole` | none | left out: mostly hollows in the ground, 1,350 in Spain and only 394 with a name |
| `highway=via_ferrata` | Vía ferrata | always ways: a point in the middle, and the line kept (see 5) |
| `route=canyoning` (relations), `sport=canyoning` | Cañón | built; a line, no points; see "Canyons" |
| `tourism=wilderness_hut`, `tourism=alpine_hut`, `amenity=shelter` + `shelter_type=basic_hut` | Refugio | see below |
| `natural=spring`, `natural=peak` | none | probably out: the Nominatim search of the maps is enough |

**To settle**: only caves to begin with (recommended), and which of the rest has a use for the
club.

**Huts.** OSM has no single tag for what the club calls a *refugio*:

- `tourism=wilderness_hut` is the unstaffed, free hut (the Refugio de la Zapatilla: `access=yes`,
  `fee=no`, `building=yes`, `fireplace=yes`, `bench=yes`, `website=…`).
  The Refugio de la Torreta (`way/426692100`, four corners, closed, a few metres across) is the
  same kind drawn as a building, with `capacity=12`, `beds=6`, `mattress=no`, `reservation=no`,
  `drinking_water=no`, `toilets=no` and `shower=no`: the extra tags of a hut that has been mapped
  with care, which a place would show as text.
- `tourism=alpine_hut` is the mountain hut, usually staffed, with `reservation`, `beds` and
  `operator`.
- `amenity=shelter` is only a hut with `shelter_type=basic_hut`: without that tag it is mostly a
  bus shelter or a covered picnic area, so a bare `amenity=shelter` must not be taken.
- `building=hut` or `building=cabin` alone is too vague (sheds, shepherds' huts): a hint at most.

What a hut offers is read as text, not as a state (a refugio has no state, `has_state`): `access`,
`fee`, `capacity`, `beds`, `fireplace`, `drinking_water`, `ele` (the altitude), `website`.

How many there are in the whole world, from taginfo (November 2026; Spain is measured below):
`tourism=wilderness_hut` 17,100 (8,900 nodes), `tourism=alpine_hut` 14,100 (7,000 nodes) and
`amenity=shelter` with `shelter_type=basic_hut` 8,300. That is the reason for requiring the
`shelter_type`: `amenity=shelter` has 700,000 elements, of which `public_transport` (258,000),
`picnic_shelter` (112,000), `gazebo` (59,000), `weather_shelter` (26,000) and `lean_to` (23,000) are
the biggest, and none of them is a refugio. The `shelter_type` also has typos and variants in other
languages (`hut`, `basic`, `refugio_forestal/montaña`, `chozo`…), each with a handful of elements,
which are not worth chasing.

**Measured on the extracts of 16 November 2026** (Spain and the Canary Islands, 1.56 GB, cut with
`osmium tags-filter` in 21 seconds on this machine; what is left is under 1 MB, and exporting it
with `osmium export` takes a few seconds more). Elements, not shapes (a closed way is exported
twice by `osmium export`, as a line and as a polygon: count it once):

| Filter | Elements | With a name | Nodes / ways / relations |
|---|---|---|---|
| `natural=cave_entrance` | 9,476 | 8,171 | 9,424 / 51 / 1 |
| `natural=sinkhole` | 1,353 | 394 | 1,210 / 142 / 1 |
| `man_made=adit` | 1,358 | 1,076 | 1,311 / 47 / 0 |
| `man_made=mineshaft` | 801 | 211 | 382 / 418 / 1 |
| `historic=mine` | 175 | 121 | 112 / 62 / 1 |
| `tourism=wilderness_hut` | 862 | 740 | 195 / 665 / 2 |
| `tourism=alpine_hut` | 445 | 410 | 150 / 294 / 1 |
| `amenity=shelter` + `shelter_type=basic_hut` | 346 | 246 | 104 / 242 / 0 |
| `highway=via_ferrata` | 309 | 238 | 17 / 292 / 0 |
| `route=canyoning` | 269 (Spain) | 267 | 0 / 1 / 268 |

- **Huts: 1,653 elements** with the three tags above, and **three out of four are ways** (the
  buildings), so ways are not an extra but the normal case for a refugio. 1,396 have a name.
- **Caves: 9,476, of which 99.5% are nodes.** 86% have a name; few have anything else: 15% an
  altitude (`ele`), 8% a description, 4% `wikidata`, 1.4% a website, and 2 a `cave:ref`, 7 a
  `depth` and 19 a `length`. So OSM gives the club a **name and a point**, and little more: the
  depth, the length and the sheet remain the club's to write.
- **Huts have more text**, when mapped with care: `fee` 19%, `capacity` 14%, `description` 13%,
  `access` 13%, `website` 12%, `operator` 11%, `ele` 24%; `beds` only 13 times.
- **`amenity=shelter` is mostly not a hut**: of 9,100 elements, 2,005 have no `shelter_type`,
  1,920 are bus stops, 683 `sun_shelter`, 545 `lean_to`, 451 `picnic_shelter`, 384
  `weather_shelter`, 333 `rock_shelter`, 249 `gazebo`: the filter must keep only
  `basic_hut` (346).
- **Mines**: 2,334 adits, shafts and mines, of which half have no name (shafts: 26% do), and a
  third are ways. See "Mines" below.

(An attempt to count the huts of Spain with Overpass failed twice before this, on two public
instances: an empty answer and "at capacity". The extract gave all of this in under a minute.)

**What the tags miss.** A scan of the whole Spanish file for names with "refugio" (34 seconds,
`osmium cat` into `grep`) gives 1,977 elements, of which: 398 `wilderness_hut`, 180 `alpine_hut`
and 91 `basic_hut` (the three filters, so already taken), **278 `amenity=shelter` with no
`shelter_type`** (most with just that tag and a name: "Refugio Arroyetas", "Refugio de la Plana de
Diego"; real refugios libres in many cases), 160 buildings (some `building=hut`, many are
`building=bunker`, anti-aircraft shelters), 13 caves, and 390 of other things (hostels,
guest houses, a restaurant, a church, bus stops, a trailhead) that are not what the club means.
So **a name that says "Refugio" on an `amenity=shelter` is a good extra candidate** (278 more), and
the other names are not, which agrees with taking them only by hand (see below).

Some will be mapped with only a name that says "Refugio": those are found by linking by hand an
existing place (see 8), not by guessing.


**Canyons.** Canyons are not mapped as points but as **route relations**: `type=route` with
`route=canyoning`, whose members are the ways of the stream. In the Spanish extract (the Canary
Islands were not searched) there are 268 of them, plus one loose way; nothing else is used for it:
`sport=canyoning` has 6 elements, `natural=gorge` 34 (gorges, not descents) and `canyon=*` or
`canyoning=*` none.

- Tags: `name` on 267 ("Barranco de las Palomeras del Flumen"), `max:abseil` (the longest rappel
  in metres) on 98, `rating` (the difficulty) on 63, `website` on 39 and `alt_name` on 37. A few
  have `description`, `access`, `source` or a `fixme`.
- The geometry is the **line of the stream**, not a place: there is no entrance or exit point,
  and the direction of the line is not guaranteed to be that of the descent. The mirror stores
  the line as the outline (a `MultiLineString` of the member ways, which is also what the "Tracks"
  of `docs/roadmap.md` want to draw) and, for now, the middle of it as the point. A place should
  take the upstream end instead, or the club's own entrance, but that has to be checked against
  canyons the club knows before it is chosen, and the line gives both ends once it is chosen.
- Built from the extract of 16 November 2026: 271 relations (268 in Spain, 3 in the Canary Islands),
  208 with one way and the rest with up to 28, plus 5 ways and a node tagged `sport=canyoning` on
  their own. A way that is a member of a relation also has its own row if it carries the tag, so
  the same stream can be there twice: matching them is for the linking of section 8.
- It is far fewer than the canyons there are in Spain (not counted here), so OSM would give the
  club a trace and a link for some, not the catalogue: what the club knows is more complete, and
  its own entrance, exit and parking points would be written in `place_points`.
- `max:abseil` and `rating` are read as text on the place (the rating's scale is not stated in
  the tag and has to be seen in the data before it is shown as a grade).


**Mines.** Built as the category `mine`: a `man_made=adit` (a horizontal gallery), a
`man_made=mineshaft` or a `historic=mine`, **unless what it takes out is water** (`resource` has
`water`, alone or in a list like `coal;water`). The club explores mines like any cavity, so the
place made from one starts as a cavity of the type Cavidad and the subtype Mina, and a cavity is
looked for among the caves and the mines when it is linked.

In the extract of 16 November 2026 there are 2,335 elements with those tags in Spain and the Canary
Islands (1,493 and 842), but 757 of them take water out: 735 in the Canary Islands, where most of
the 842 are the "galerías", tunnels dug to bring the water of the mountain out (tagged
`man_made=adit` and `resource=water`), and 22 in the rest of Spain. They are man-made tunnels like
any adit, but not what the club explores, so they are left out, **everywhere** and not only in the
islands: `resource=water` says what they are, which a name ("Galería …") would not (27 adits of the
mainland are called that and are mines). That leaves **1,578 mines** (1,471 and 107). A few
galleries of the Canary Islands (6) have no `resource` and are still taken, since nothing says what
they are. An element that was loaded before the rule is marked as gone by the next load, not
deleted, as with any other.

## 13. Phases

1. **Decide** the questions below; confirm the licence reading with the board.
2. **Mirror** (done): the table, the load of Spain for caves, huts and via ferratas (nodes, ways and
   relations; and canyons, which are routes), the log, the command and the timer.
3. **Show and use** (done): `GET /osm/elements` and the points of OpenStreetMap as grey rings in the
   selector of layers of the map of `/lugares`, from zoom 11 up and by the box in view, with a popup
   (the data, a button) and the ODbL credit on the map. "Añadir al catálogo" opens the form of a new
   place with the name, other names, altitude, description, type and coordinates filled in and the
   zone proposed from them (`/areas/locate`, so the zones of the elements are not needed for this),
   and `POST /places` keeps `osm_type` and `osm_id`: promotion on demand, the option chosen in 7. An
   element that is a place is no longer a candidate. The magnifier of the map also searches the
   mirror by name (`GET /osm/search`), which is how a cave is found to make a place of it, since the
   catalogue is built from OpenStreetMap and not by hand (production has three or four places).
4. **Link** (done, but for one thing): linking and unlinking a place that already exists
   (`PUT`/`DELETE /places/{id}/links/osm`, the candidates near it by distance and name with `GET
   /osm/nearby`, and the dialog and the section "OpenStreetMap" of the page of a place), the display
   of what OpenStreetMap says of the element next to the place, marking what does not agree (name,
   coordinates, altitude), buttons to copy a value into the place, and the notice of what changed in
   the element since the place was linked or last reviewed (`places.osm_seen`, `osm_changes`, `POST
   /places/{id}/links/osm/review`, the badge in the list). Nothing is merged or changed by itself.
   **Still to do**: a list to review in one go the places that already exist, which depends on how
   many places the club has that are not linked.
5. **Zones** for the elements (6), the other countries, the other filters (12).
6. **Contribute** (9), if wanted.

## 14. To settle (all of it)

1. Is the OSM mirror kept as a table apart, joined by id (3, 5)? (Recommended.)
2. Eager import, lazy promotion or a virtual view (7)? (Recommended: lazy.)
3. How is the zone of each element found: offline with the boundaries of the extract, PostGIS,
   or on first use (6)?
4. Spain only, or other countries too; how often is it refreshed (4)?
5. May the host have `osmium-tool`, and is a gigabyte of download a few times a week fine (4)?
6. Which tags become which fields, and which side wins when they differ (8)?
7. Ways and relations come in from the start (5), with the point inside the shape and the outline
   kept: confirm that is what is wanted, and whether the outline is drawn on the maps.
8. Contributing back: not at all, a link to the OSM editor, or an integration (9)? And, in any
   case, what to do about places whose location is private?
9. Does the club ever want to open the catalogue to the public (3, 10)?
