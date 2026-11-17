# OpenStreetMap as the base of the places (draft)

A working document, not a decision: it lists what would have to be done to make OpenStreetMap
(OSM) the main source of the catalogue of places, so that it can be refined before anything is
built. When a part is decided its design goes to the usual documents (`api.md`, `database.md`,
`backend-internals.md`, `frontend.md`) and it is removed from here. Questions that need an answer
are marked **To settle**, and collected at the end.

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

| Column | What |
|---|---|
| `osm_type`, `osm_id` | `node`, `way` or `relation` and its id: the primary key |
| `version`, `osm_timestamp` | of the element as downloaded |
| `lat`, `lon` | the point: the node itself, or a point inside the shape of a way or relation |
| `geometry` | `jsonb` GeoJSON of a way or relation (its outline), null for a node |
| `tags` | `jsonb`, all the tags as they are |
| `deleted` | true once it is gone from OSM (the row is kept: a place may point at it) |
| `fetched_at` | when this row was last confirmed by a download |

and a log of the loads (`osm_sync_runs`: when, from which file, its date, how many new, changed
and deleted, how long, the error if any), which `adminstatus` could show.

In `places`: `osm_type` and `osm_id`, both null or both set, **unique** (an element is at most one
place), with a foreign key to the mirror.

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

A command (`backend/cmd/osmsync`, or a script) run by a systemd timer next to the deployment
(`deploy/systemd/`), with its own failure notice (`scripts/notify-failure.sh`):

1. Download the extract of each country (conditional request, client name that says who calls,
   never more often than the extract is refreshed).
2. Cut it: `osmium tags-filter` with the configured filters (see 12), `osmium export` to
   GeoJSON lines.
3. Compute the zone of each point (6).
4. In one transaction: upsert what is new or has a higher `version`, mark `deleted` what the
   previous run had and this one does not, flag the places whose element changed, write the
   `osm_sync_runs` row. It is idempotent: running it twice leaves the same.
5. Throw away the downloaded file (or keep the last one, on a disk with room: check `adminstatus`).

Tests: a tiny `.osm` file in `testdata`, loaded twice, with a changed and a deleted element
in the second run. Testing would run it too (it is a copy of production's database, and a real
download is harmless there), and the production→testing copy needs nothing new because the
mirror is in the database.

## 12. Which elements are relevant

A list of filters in configuration, each with the place type it suggests, so that it grows
without code:

| Filter | Suggests | Notes |
|---|---|---|
| `natural=cave_entrance` | Cavidad | the first and main one |
| `natural=sinkhole`, `man_made=mineshaft`, `man_made=adit`, `historic=mine` | Cavidad (sima, mina) | check how many and how reliable before adding |
| `highway=via_ferrata` | Vía ferrata | always ways: a point in the middle, and the line kept (see 5) |
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

How many there are, from taginfo (the whole world, November 2026; the count for Spain alone is still
to be had, see below): `tourism=wilderness_hut` 17,100 (8,900 nodes), `tourism=alpine_hut` 14,100
(7,000 nodes) and `amenity=shelter` with `shelter_type=basic_hut` 8,300. That is the reason for
requiring the `shelter_type`: `amenity=shelter` has 700,000 elements, of which `public_transport`
(258,000), `picnic_shelter` (112,000), `gazebo` (59,000), `weather_shelter` (26,000) and `lean_to`
(23,000) are the biggest, and none of them is a refugio. The `shelter_type` also has typos and
variants in other languages (`hut`, `basic`, `refugio_forestal/montaña`, `chozo`…), each with a
handful of elements, which are not worth chasing.

The attempt to count them for Spain with Overpass failed twice on the public instances (an empty
answer, and "at capacity, all query workers are busy"), which is one more reason for downloading
the data instead of asking for it (4): the count will come for free from the first extract of
Spain, and the filters of this section should be tried against it before being fixed.

Some will be mapped with only a name that says "Refugio": those are found by linking by hand an
existing place (see 8), not by guessing.


## 13. Phases

1. **Decide** the questions below; confirm the licence reading with the board.
2. **Mirror**: table, the load for Spain with `natural=cave_entrance` (nodes), the log, the
   command and the timer. Nothing visible yet except in `adminstatus`.
3. **Show**: the candidates on the map of `/lugares` (bounding box), with attribution, and
   "Usar" creating the place with the OSM id (the option chosen in 7).
4. **Link** the places that already exist (matching by distance and name, reviewed by hand) and
   the field-by-field display of 8, with the flag when OSM changes.
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
