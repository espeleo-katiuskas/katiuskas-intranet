# OpenStreetMap in the intranet

How the catalogue of places uses OpenStreetMap (OSM): what is built, why it is built that way,
what OSM has that the club can use, and what is still open. The details of each part are in the
usual documents (`api.md`, `database.md`, `backend-internals.md`, `frontend.md` and
`environments.md`); this one keeps the reasons, the findings about the data, and what is not done.

## 1. What it does today

- **A local mirror** of the OSM elements that matter to the club: caves, canyons, mines, huts and
  via ferratas of Spain and the Canary Islands (about 13,500 elements), loaded once a week by
  `cmd/osmsync` from the extracts that Geofabrik publishes. Nothing in the intranet asks OSM
  anything: pages and requests read the club's own copy (`osm_elements`).
- **The map of `/lugares`** shows them as grey rings, one layer for each category in the selector
  of layers, from zoom 11, with a popup. The magnifier searches them by name (and says in which
  municipality each one is) as well as the places that Nominatim knows.
- **"Añadir al catálogo"** makes a place of an element: the form comes filled in with its names,
  altitude, description, type and zone, and the place keeps which element it is.
- **A place that already exists can be linked** to an element (the candidates near it, by
  distance and name). The page of a linked place then shows what OSM says next to what the club
  says, offers to copy a value, and tells what changed in the element until somebody says it has
  looked.
- **`/admin`** says how the mirror is, and a failed load or data that is too old is a problem that
  the monitor tells by Telegram.

## 2. Decisions, and why

**The mirror is a table of its own, joined to the places by the type and the id of the element.**
Under the ODbL that keeps what is OSM's apart from what is the club's (a *collective* database, not
a merged one), and the club's corrections are stored as the club's, never as altered OSM rows.
Rows are never deleted: one that is gone from OSM is marked, since a place may point at it.

**An element becomes a place on demand, not by importing them all.** A row of `places` needs a
type, a subtype, a state, a zone and an author, none of which OSM has: importing 13,500 of them
would mean making those up, and filling the lists and counters with places nobody asked for. A
view that unions both tables would make every query, filter and counter understand two sources.
So the elements are *candidates* (on the map, in the search) and become a place the first time a
member adds one, choosing what OSM does not say. The catalogue is built that way, since it
has hardly any places of its own.

**The data comes from Geofabrik's extracts, not from Overpass or the planet file.**

| Source | Good for | Bad for |
|---|---|---|
| Overpass API (public) | one-off exploration | regular use: often at capacity (half a minute per query, or no answer) and it asks for light use |
| Geofabrik extracts (`download.geofabrik.de`) | the first load and the regular ones: one `.osm.pbf` a region, refreshed daily | a whole region to keep a few thousand elements; needs `osmium` to cut it |
| Planet file | the whole world | tens of gigabytes for 70,000 elements |
| An Overpass of our own | frequent complex queries | a service to run |

Geofabrik cuts by region and not by country, so **Spain takes two files**: `europe/spain` (1.5 GB,
with the Balearic Islands, Ceuta, Melilla and the small islands) and `africa/canary-islands`
(60 MB). They do not overlap, so each is cut on its own. A download is a conditional request
(`If-Modified-Since`, to the file of the day that the `-latest` address redirects to) with a client
name that says who calls, and what is kept is tiny: the heavy part is one pass over a file, once a
week. Between full loads a daily Overpass query with `newer:` would give what changed, but not
what was deleted, so it would not replace them.

**Ways and relations are in from the start.** OSM draws many things as shapes: three out of four
huts are the outline of a building, and a canyon is a route made of several ways. `osmium export`
builds the polygons (and `ReadRoutes` the routes, which it cannot), the point of an element is one
*inside* its shape (the centre of an L-shaped building can fall outside it) or the middle of a
line, and the outline is kept to draw it. An element is identified by **type and id**: node 123
and way 123 are different things. A known limit: if a mapper replaces a node by a way (draws the
building around the point), the id changes, so a place linked to the node is told that its element
is gone, and the new way is a candidate to link again by hand.

**The zone of each element comes from the boundaries of its own extract.** Asking Nominatim for
each element was out of the question (one request a second, and bulk use is against its policy),
and PostGIS would have been a new dependency for production, the backup and the prod→testing copy.
Each load also cuts the municipal boundaries (`admin_level=8`) and gives each element the zone
whose polygon holds its point (`osm_elements.area_id`; 97.7 % get one, in 2,196 municipalities; the
rest are on the coast, where a sea cave is outside the polygon, or in Gibraltar, Ceuta and the
Basque condominiums). Ceuta and Melilla are autonomous cities, with no province or municipality
below them, so their zone is the city, found by the INE code of their municipality (51001, 52001).
Melilla's boundary is in the extract, but Ceuta's is not a closed polygon in it (it shares its
border with Morocco, which the extract cuts), so the elements of Ceuta have no zone yet. A
`ZoneResolver` says which zone of the club a boundary is: Spain's reads the `ref:ine`. Another
country needs a rule of its own and its zones in the catalogue. Country polygons were tried as a
fallback and cannot be used: in an extract that cuts it, the relation of a country is not a closed
polygon (only Andorra's was built). The column is `ON DELETE SET NULL`, the one foreign key that is
not `RESTRICT` (a test names it and says why): the value is derived, so deleting a zone must not be
stopped by it, and merging two moves the elements.

**The two sides of a linked place are shown, not merged.** The OSM value is shown as OSM says it,
the club's value wins in the club's own pages, and nothing is copied except by a person pressing
the button of that field. A load that changes the element does not change the place: it is
flagged until somebody looks (the place keeps a snapshot of what it saw, `osm_seen`, and the API
compares). An element that disappears leaves the place as it was, and says so. The description is
never replaced with OSM's, only copied into a place that has none.

**Which elements: what the club does first.** Caves, canyons and mines, then huts (where people
sleep), and via ferratas, which the club only uses now and then, last. Sinkholes are left out
(mostly hollows in the ground). The mines that take water out are left out too (see 4).

## 3. Licence: what the ODbL asks

To be confirmed by the board (this is a reading, not legal advice):

- **Attribution**: "© OpenStreetMap contributors" wherever the data is shown. The map says it
  while the layer is on, and its credit links to the copyright page.
- **Share-alike** applies to a *derivative database* that is *publicly used*: if the club's
  database mixed OSM data with its own and were made public, it would have to be offered under the
  ODbL. The intranet is behind a login, which is not public use, and keeping the mirror apart
  (see 2) keeps the club's own data out of any doubt. If the catalogue were ever opened to the
  public, this has to be looked at on purpose.
- Keep the **date and the version** of what was downloaded (the mirror does).
- Contributing back (see 5) has its own rules.

## 4. What OSM has: the findings

Measured on the extracts of 16 November 2026 (Spain and the Canary Islands, 1.56 GB, cut with
`osmium tags-filter` in 21 seconds). Elements, not shapes; a closed way is exported twice by
`osmium export`, as a line and as a polygon, and counted once. In the world, `natural=cave_entrance`
has 73,500 elements.

| Filter | Elements | With a name | Nodes / ways / relations |
|---|---|---|---|
| `natural=cave_entrance` | 9,476 | 8,171 | 9,424 / 51 / 1 |
| `route=canyoning` | 269 (Spain) | 267 | 0 / 1 / 268 |
| `man_made=adit` | 1,358 | 1,076 | 1,311 / 47 / 0 |
| `man_made=mineshaft` | 801 | 211 | 382 / 418 / 1 |
| `historic=mine` | 175 | 121 | 112 / 62 / 1 |
| `tourism=wilderness_hut` | 862 | 740 | 195 / 665 / 2 |
| `tourism=alpine_hut` | 445 | 410 | 150 / 294 / 1 |
| `amenity=shelter` + `shelter_type=basic_hut` | 346 | 246 | 104 / 242 / 0 |
| `highway=via_ferrata` | 309 | 238 | 17 / 292 / 0 |
| `natural=sinkhole` (not loaded) | 1,353 | 394 | 1,210 / 142 / 1 |

**Caves.** 99.5 % are nodes and 86 % have a name; few have anything else: 15 % an altitude (`ele`),
8 % a description, 4 % `wikidata`, 1.4 % a website, and 2 a `cave:ref`, 7 a `depth` and 19 a
`length`. OSM gives the club **a name and a point**, and little more: the depth, the length and the
technical sheet remain the club's to write, and OSM does not say what kind of cavity it is.

**Huts.** OSM has no single tag for what the club calls a *refugio*:

- `tourism=wilderness_hut` is the unstaffed, free hut (the Refugio de la Zapatilla: `access=yes`,
  `fee=no`, `fireplace=yes`, `website=…`; the Refugio de la Torreta, `way/426692100`, is the same
  kind drawn as a building, with `capacity=12`, `beds=6` and `toilets=no`).
- `tourism=alpine_hut` is the mountain hut, usually staffed, with `reservation`, `beds`, `operator`.
- `amenity=shelter` is a hut only with `shelter_type=basic_hut`: it has 700,000 elements, mostly
  bus stops (258,000), picnic shelters (112,000), gazebos, weather shelters and lean-tos, so a bare
  `amenity=shelter` is not taken. The exception is one **with no `shelter_type` whose name says
  "refugio"** (278 in Spain, "Refugio Arroyetas": real refugios libres in many cases), which is.
- `building=hut` or `building=cabin` alone is too vague (sheds, shepherds' huts). A name that says
  "Refugio" on anything else is a hostel, a bunker or a bus stop: those are found by linking a
  place by hand, not by guessing.

There are 1,653 huts with the three tags, three out of four of them ways, and 1,396 have a name.
They have more text than the caves when mapped with care (`fee` 19 %, `capacity` 14 %,
`description` 13 %, `access` 13 %, `website` 12 %, `operator` 11 %, `ele` 24 %); the place shows it
as text, since a refugio has no state.

**Canyons** are not points but **route relations**: `type=route`, `route=canyoning`, whose members
are the ways of the stream (`sport=canyoning` on a way or a node is the other way they are mapped,
6 elements; `natural=gorge` has 34 and is not a descent). 271 relations in the extract, 208 with one
way and the rest up to 28. Tags: `name` (267), `max:abseil`, the longest rappel in metres (98),
`rating`, the difficulty (63), `website` (39), `alt_name` (37). The geometry is the **line of the
stream**: there is no entrance or exit point, and the direction of the line is not that of the
descent. The mirror keeps the line (a `MultiLineString`, which is also what the "Tracks" of
`roadmap.md` want to draw) and, for now, the middle of it as the point. A place should take the
upstream end, or the club's own entrance, but that has to be checked against canyons the club
knows. It is far fewer than the canyons of Spain, so OSM gives the club a trace and a link for
some, not the catalogue: the entrance, exit and parking points are written by the club in
`place_points`. A way that is a member of a relation and carries the tag has a row of its own, so
the same stream can be there twice. The scale of the `rating` is not in the tag and has to be seen
in the data before it is shown as a grade.

**Mines** are a `man_made=adit` (a horizontal gallery), a `man_made=mineshaft` or a
`historic=mine`: cavities that the club explores like any other, so the place made from one starts
as a Cavidad of the subtype Mina. There are 2,335 elements with those tags, but 757 take water out
(`resource` has `water`, alone or in a list like `coal;water`): 735 in the Canary Islands, where
most of their 842 are the "galerías", the tunnels dug to bring the water of the mountain out, and
22 in the rest of Spain. They are left out **everywhere**, by the tag and not by the name: 27 adits
of the mainland are called "Galería" and are mines, and the name would miss the water ones that
are not. That leaves 1,578 mines; 6 galleries of the Canary Islands have no `resource` and are
still taken, since nothing says what they are. What a load had already stored before the rule is
marked as gone by the next one.

## 5. What is open

**Small things to do:**

- A nearest-municipality fallback for the 2 % of the elements that are in no municipality (the
  caves on the coast), and a way to give a zone to the elements of Ceuta, whose boundary is not a
  closed polygon in the extract of Spain (for instance a box of its own for each autonomous city).
- The point of a canyon (the upstream end, or the entrance the club knows) and the scale of its
  rating.
- The list of candidates for linking does not say in which municipality each is, and the map of a
  place does not draw the outline of its OSM element.
- Other countries: loading them is adding extracts, but their elements have no zone until their
  zones are in the catalogue and there is a rule for them.
- Using the daily diffs of Geofabrik instead of the whole extract, if the load is ever run more
  often than weekly.

**Decisions that are the club's:**

- **The licence reading**, to be confirmed by the board (3).
- **Locations that should be private.** Some cave locations are left out of public maps on
  purpose, to protect the cave or the visitors. Today everybody who signs in sees everything (it is
  in `roadmap.md`, as "hide the location of some places"), and anything copied from the club into
  OSM is public for good. It has to be decided before contributing.
- **Opening the catalogue to the public** would make the "publicly used" of the ODbL real.
- **Contributing back to OSM** is optional and the rest does not depend on it. The options are
  nothing, a link to the OSM editor on the page of a place (cheap, and needs no integration), or an
  integration:
  - It needs **OAuth 2** with an OSM account (API 0.6; the accounts are personal): each member's
    own, which keeps the authorship honest and means the intranet holds tokens of another service
    for each person, or one account of the club, which is simpler and goes against OSM's guidance.
    A development server (`api06.dev.openstreetmap.org`) exists to try it.
  - The edits would be queued as **pending edits** (element, base version, the tags to set, who
    proposed and who approved), shown to admin and board, and sent as one changeset each; an edit is
    made on the current version of the element, and if it moved on it is a conflict to look at.
  - **The community's rules**: only what the contributor knows from the ground or from sources that
    allow it (never copied from a copyrighted guide or database), and a mass upload is an *import*
    that must be discussed with the community first. One element by hand is not.

**Dropped:** a page to match in bulk the places that already exist (the club has three or four, so
linking from the page of each is enough), an import of every element as a place, and PostGIS.

**To watch on the first load after a deployment** that adds a category or a column of the mirror
(the mines, the zones): every element changes once, so the group "Mirror de OpenStreetMap" of
`/admin` shows a large number of changed ones. It does not touch the places, whose notice looks at
the tags and the point of their element. A whole load takes about a minute on the machine where it
was tried.
