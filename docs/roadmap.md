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
- **A diff mode for the municipalities' seed**: `scripts/generate-areas-seed.py` only writes the
  whole seed, so the list of municipalities (migration 00062) has no way to be brought up to date.
  The mirror's load already says when it is behind (`/admin` shows the INE codes of the municipal
  boundaries the club has no zone for). The script would get a mode that compares the INE's list
  with the zones of the club and writes a draft migration (new municipalities, renames) plus a
  report of what needs a human decision (mergers, municipalities that disappear or change code).
  It should ignore Ceuta and Melilla (autonomous cities, migration 00074), and share the reading of
  the INE's data with the full mode (for instance in `scripts/areas_seed.py`). To be run by hand
  about once a year, whether `/admin` warns or not: the warning only catches a code that is new in
  OpenStreetMap, not a municipality that disappears in a merger (its zone stays, empty) or one that
  is renamed, and those are only seen by comparing with the INE's list.
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
  and a catalogue code. Hiding the location of some places from some people is part of opening the
  catalogue to the public (next section).

## The catalogue of places as a service open to the public

What is wanted: that the catalogue of places (maybe under another name, and reachable from another
site: a domain of its own or `www.katiuskas.es`) is a project in its own right, open to many people,
and that the intranet is its first and special client. Anybody can see the public parts; people who
register with no relation to the club (ideally with a verified email and a phone number, which needs
a way to verify a phone number) see a little more; the members of the club are a **group** that sees
everything (the coordinates, for a start) and writes. Later, other groups (another club, the
federation, a group of explorers of one system), other audiences (one person) and perhaps write
permissions for people outside the club. The board has answered the main points (see "Decided by
the board" below); what is still open is listed there.

The idea in one line: instead of a part of the intranet that is opened to the public little by
little, the catalogue is the product, with public, registered and group audiences from its first
day, and the club's members are the first group. It is reached **in stages**: first the boundary is
drawn inside this repository (no new system to run), and the catalogue is taken out into a service
of its own only when something justifies the cost (see "Work, in this order", step 7).

### Where it stands today

- Everything is for who signs in: `Authenticator.Protect` always requires admin, board or member, on
  top of the roles a route lists, so a person with no such role is refused everywhere. There is no
  anonymous access to anything but the login routes.
- Every member sees every place whole (coordinates, access, files, points, who changed it and when)
  and may change it. There is no per-place or per-field visibility.
- A person is a row of `users`, which is made for members: `birth_date` is `NOT NULL`, and the
  address, national id and emergency contact are columns of it. The roles `board`, `member` and
  `former_member` are computed from the board and the memberships.
- Sign-in is a magic link or a code sent to an email, and it identifies the person by email **and**
  phone together: the request says when they match nobody (`unknown_details`), which is acceptable
  for a closed list of members and would not be for open registration.
- The API names people by `login` everywhere (`created_by`, `updated_by`, `uploaded_by`), and a
  place has the activities that went there (`activity_places`), with their organizers; a place with
  activities is not deleted.
- The Telegram bot already proves that a phone number is the person's own: a shared contact is
  accepted only if it is the sender's (`isOwnContact`), and Telegram itself has verified that
  number.
- `www.katiuskas.es` is a static stub of three blocks and a contact address, in another repository
  (`katiuskas-web`); it has no application, no login and no legal pages.
- The page is client-rendered (no build step), so a search engine sees an empty shell. Its copies
  for working off line (`sw.js`, the outbox) keep places on the phone and send what was done with no
  connection.
- The mirror of OpenStreetMap is kept apart from the club's data on purpose because of the ODbL
  (`osm-integration.md`, section 3), and some data of places was copied from it.
- `internal/places` and `internal/areas` already talk to the rest through small local interfaces
  (`UserLookup` and the like), which is what makes drawing the boundary cheap.

### Why turn it around, and what it costs

For:

- The questions that were open about outsiders (where they are kept, where the public site lives)
  stop being a problem: they never enter `users`, and the catalogue is a site with a session of its
  own.
- The boundary is real. A route wired by mistake in the public part exposes the catalogue, not
  accounting, people or licences.
- "Closed by default" is natural when the catalogue is built for audiences, instead of an exception
  added to a system made for members.
- The border with OpenStreetMap and the ODbL is clean: club data, OSM data and public data live in
  one place with one purpose.
- It opens the door to other clients (another club, the federation). This is the only argument that
  by itself justifies a separate project, and it is the test in step 7.

Against, and what does not go away:

- **The permission model is still there**: it only moves. Groups and a table of rules (decision 2)
  stop being "later" and are the core from day one.
- **Identity between the two** (decision 4): the catalogue has to know that somebody is a member
  without learning club paperwork.
- **Couplings to cut**: `activity_places` and the rule that a place with activities is not deleted
  (a foreign key becomes a reference between services); `created_by`, `updated_by`, `uploaded_by`
  (logins); the Telegram notices of every change; and the planned technical sheet linked with the
  material.
- **Working off line**: the service worker and the outbox talk to one API today (decision 6).
- **Cost of running two systems** (a second database, deployment, migrations, CI, backups,
  monitoring) for a club with few people to maintain it. It is the main risk, and the reason to go
  in stages.
- **Governance**: who answers for the data of the catalogue (decision 7).

### The shape it aims at

- **The catalogue owns**: places, points, systems, zones, files, the mirror of OpenStreetMap, the
  accounts of registered people, the groups and the rules that say who sees and does what.
- **The intranet keeps** people, activities, money, material and everything else, and refers to a
  place by its id (an activity says "went to place 42"; it never reads the place from a table).
- **The club is a group of the catalogue.** What the catalogue knows about a member is an opaque
  subject, the group (`katiuskas`) and what the group allows; nothing of the paperwork.
- **A viewer is an assertion**, not a row of `users`: an issuer, an opaque subject, the groups it
  belongs to and an expiry. The intranet is the first issuer, and the catalogue's own sign-in for
  registered people is another. Everything that decides what is seen asks one function with that
  viewer.

### Decided for the first stage

The club has settled these for the work that comes first (the rest, below, is still to settle):

- **The levels of a place** are three, each `club` or `public`: *the place* (that it exists, with
  its name, type, state, zone and measures), *the location* (the coordinate, the other points and
  how to get there) and *the content* (the description, the technical sheet and the files). Where
  the place is closed it does not exist for whoever may not see it: no data, no count, a 404. Who
  wrote it and the activities are never public.
- **Without access to the coordinates** a viewer sees the zone of the place and no marker.
- Only **admin and board** change the visibility, never the author alone, and each change is
  announced. A level is never more open than the place. A system shows the cavities that are seen
  and its description and files follow the most closed of them. Every place starts closed and the
  only values for now are `club` and `public`.
- A place can be **protected** (bats, archaeology, private land): a lock under which nothing may be
  public.

### Decided by the board

Answered on 2026-11-26, to the questions in the page prepared for the board:

- **The site** is called «Cuevas y simas» and lives at its own domain, `https://cuevasysimas.es`
  (the third option, not the one recommended: a path of `www.katiuskas.es`). It has its own legal
  notice, privacy policy and cookies, and its session is not shared with the intranet.
- **Who answers for it**: a project with its own governance (not the club).
- **The first places opened**: a handful marked by whoever knows them, at the level of *the place*
  alone (name, type, state, zone and measures); the location and the content stay for the club.
- **What the public sees of the location**: only the zone, no marker.
- **Legal notice**: a standard template, not one written with an adviser.
- **Photos, technical sheets and texts of members**: none published at the start.
- **OpenStreetMap and maps**: the club's data kept apart from OpenStreetMap's and credited; nothing
  contributed back until the private locations are settled.
- **People outside the club** can register (the second option), with an email and a phone number.
  The phone is verified through Telegram at first; other methods may come later.

What this changes: taking the catalogue out of the intranet (step 7 below) stops being optional,
since the project has its own domain and governance; the club becomes a group of it and the
intranet a client. The accounts of the catalogue (`internal/accounts`) were made for this.

What the answers leave open (to ask the board):

- Who is legally responsible for the project (name or entity, address and contact email), which is
  what the template of the legal notice needs, and who holds the domain and the hosting.
- Which people govern and moderate it.
- Whether the phone is mandatory when registering, as it has been understood. If it is optional,
  somebody with no Telegram could register with the email alone.
- Which Telegram bot verifies the phone: one of the project's own (the recommendation, for the
  separation) or the club's.
- When registration opens: after the first places are opened (proposed) or before.

### Decisions to settle

The points that the board answered above (the site and its domain, governance, the legal notice,
the phone through Telegram, the content, OpenStreetMap, and the location shown to the public) are
kept below for their reasoning, with the options that were not chosen.

**1. What each part of a place is.** Before a mechanism, a classification. A starting point to be
discussed by the people who know the caves, one row per part:

| Part | Public? | Notes |
|---|---|---|
| name, type, subtype, state, zone | likely yes | the state ("hace falta permiso") is useful and protective |
| depth, horizontal length, geology | likely yes | |
| main coordinate | per place | the sensitive one, see 3 |
| other points (parking, approach) | per point or per place | a car park is rarely secret, an entrance is |
| how to get there | per place | often gives the location away as well as the coordinates |
| description, technical sheet | per place | the sheet has safety content: who is responsible for it? |
| photos and documents | per file | the author's consent, EXIF, and what they show (an entrance) |
| activities that went there | no | they belong to the intranet; at most "visited N times" |
| who created / changed it | no | it would publish the logins of members; at most a group name |
| system of cavities | follows its cavities | a system links caves: it may reveal where two of them are |

**2. The permission model.** Three dimensions: the audience (anybody, registered, a group, one
person), the part (the whole place, the coordinates, a file…) and the action (see, write). Options:

- *A level per place and per part*: an ordered scale (`public`, `registered`, `group`) in a column
  of the place and a few columns for the parts that can differ. Small and easy to test, but it
  cannot say "this other club" or "this person", which is what the inverted idea wants early.
- *A table of rules* (`resource`, `part`, `audience`, `action`) with groups of people: it covers
  everything that was said, at the price of a rule engine, a screen to manage it and slower lists.
- Recommended: build the *question* once, whatever answers it. A single function (`CanSee(viewer,
  place, part)`, later `Can(viewer, action, resource, part)`) that every handler uses, backed by
  the table of rules from the start but with only the few audiences of the first stage in use
  (`public`, `registered`, the club's group). Never decide visibility in a handler or in the
  frontend.

Things to settle with it: whether a place with no decision is closed (recommended: **nothing becomes
public unless somebody says so**, and the migration puts every existing place at the club's group);
who may change a visibility (admin and board of the owning group, never the author alone, each
change announced as the others are); whether a part may be *more* open than its place (a public car
park of a members-only cave: probably not); what a system of cavities shows; and who owns a place
when there are several groups (the group that created it, and may it be shared with another).

**3. Coordinates.** The decision that matters most, because a leaked location cannot be taken back
(it is copied, and OpenStreetMap is public for good; `osm-integration.md`, section 5). Options for
what a viewer without access sees: nothing; the zone only (municipality) with no marker; a point
made imprecise on the server (snapped to a grid of a few kilometres) and said to be approximate.
Whatever it is, **it is done on the server**: the list `GET /places` sends the coordinates of every
place today, so the public list, the public map, the distance column, the copies for working off
line, the Telegram notices and the search by zone must all come from the same redacted view. An
imprecise point must not be one that several requests can average away (a random offset each time),
and a place whose coordinates are hidden must not be found on the map by other data (its zone, its
OpenStreetMap link, a point of its own).

**4. Identity: who the viewers are and how the club reaches the catalogue.** Viewers: anonymous,
registered (no relation to the club), a member of a group (the club first), board or admin of it,
and later other groups. Two questions:

*Where registered people are kept.* In a table of the catalogue's own (`accounts`: email, name
chosen, verified channels, created, last seen), never in `users`: `users` is club paperwork
(`birth_date` is required, the national id and address are there), the lists of people would have to
filter outsiders out forever, and one forgotten filter shows outsiders to members or the opposite.
Their sign-in cannot tell whether an email exists (no `unknown_details`), the opposite of the club's
flow. A person who joins the club later is linked to their account by a proof (a verified email),
never by a guess.

*How a member gets in.* Options:

- *The intranet issues a signed assertion* (short-lived, opaque subject, group, expiry) that the
  catalogue verifies with a public key. Nothing of the member's data travels, the catalogue never
  calls the intranet, and a member who leaves loses access when the assertion expires (so the life
  is short, minutes, and the intranet asks for a new one while the session lasts). **Recommended.**
- *Accounts of the catalogue, linked to the intranet by a proof*: every member would have to make an
  account and keep two sign-ins. More friction for the club and no gain.
- *The catalogue asks the intranet on every request*: couples the two and breaks off line.

Settle: the opaque subject (a random id kept in the intranet, never the `login`, so that the
catalogue cannot be joined with the club's data), how a person is removed at once (a revocation list
the catalogue reads, on top of the expiry), and whether the catalogue and the intranet share a
session (one sign-in, which wants one registrable domain and a shared cookie) or are separate
origins (simpler and safer; the intranet gets the assertion and passes it).

**5. Verifying an email and a phone.** The email is easy and exists already (the magic link). A
phone is the hard part, and first one has to say what it is for: to make a second factor like the
club's sign-in, to make mass fake accounts costly, or to be able to call the person. Options:

- *SMS code*: works for everybody, costs money for each message, needs a provider (a data processor
  to put in the privacy policy), and is a target of SMS pumping fraud, so it needs rate limits and a
  budget cap.
- *WhatsApp code*: the same with other costs and Meta's templates.
- *Telegram*: free, and the bot already checks that a shared contact is the sender's own, but only
  for people who have Telegram.
- *Email only, with a phone that is declared and not verified*, plus a CAPTCHA that respects privacy
  (Turnstile or similar) and rate limits per IP and per address: weaker and much cheaper.
- Recommended to start: mandatory verified email; phone optional, verified with Telegram when given;
  SMS only if it is decided it is worth paying for. An unverified phone must never be treated as
  one in any decision (for instance to match a person with a member).

Settle also: how long unverified accounts live, what happens on deleting an account (the GDPR gives
a right to it and to a copy), and whether a registered email that matches a member is offered to
link (it must be proved, never guessed).

**6. Where it lives.** Two questions that are separate: the code and the address.

*The code.* First a package boundary in this repository and this binary (cheap, reversible), later,
if step 7 says so, a repository, a database and a deployment of its own. What makes the later cut
mechanical is decided now: places, systems, zones, files and the mirror in their own schema with no
foreign key to `users` or `activities`; no import of those packages; the viewer as an assertion.

*The address.* `www.katiuskas.es` is a stub: a static `index.html` (three blocks: who we are, what
we do, where we meet, and a contact address), a `404.html` and the logo, in a repository of its own
(`katiuskas-web`), with no script, no build and no CMS, and no legal notice or privacy page. So
there is nothing to embed the catalogue into; the catalogue is a site of its own that the stub links
to (and that may borrow its look). Options:

- *Under the same domain* (`www.katiuskas.es/cuevas/…`, name to settle): the web server serves the
  stub's files and sends that path to the catalogue. One origin, no CORS, indexed under the club's
  domain. It suits the first stages, with one binary.
- *A subdomain* (`cuevas.katiuskas.es`): separate origin, a clean cut between the stub and the
  application, a little more DNS and certificates. It suits the catalogue as a service of its own.
- *A domain of its own*: it does not inherit the club's name in search results. It makes sense only
  if the project stops being the club's (decision 7).

Settle: where the stub is served from today (not recorded here: a bare repository next to the
checkout suggests a push-to-deploy) and whether it moves to the host that runs the intranet; what
the section is called ("Lugares" is what it is today, and it is not only caves); and the pages the
stub lacks: a legal notice with who the club is (the footer has its legal name, "Club Deportivo
Elemental de Espeleología Katiuskas de Móstoles"), a privacy policy and a contact more than an
address in the text.

*Members and working off line.* Today the pages of places work with no connection through `sw.js`
and the outbox. While it is one binary nothing changes. Once the catalogue is separate, the
intranet's pages either call it from the browser (CORS and a token got by exchanging the assertion)
or the intranet proxies it. The first is cleaner and keeps working off line; the second is simpler.
The public site has its own service worker (or none): the intranet's keeps data on the phone that a
visitor must not get.

What the public site needs that the intranet does not:

- Pages a search engine can read: **server-rendered** HTML (Go templates, or a static site generated
  from it) with titles, descriptions, Open Graph, a sitemap and `robots.txt`, and URLs with a name
  in them (`/cavidad/sima-de-la-pena`) instead of `?id=3`. The intranet pages stay as they are.
- A separate group of routes for it (for instance `/api/v1/public/…`) that returns the redacted view
  and nothing else, so that a route wired by mistake cannot expose the full one.
- Caching (headers and a cache in front: the public view changes rarely), rate limits for readers
  who are not signed in, and protection against scraping the whole catalogue.
- Maps: the tiles of the IGN, Esri and OpenStreetMap each have terms for a public site (credit, and
  for Esri and OpenStreetMap limits that a public site can exceed). Nominatim must not be called
  from the browsers of the public.

**7. Governance.** An open project needs somebody who answers for it. Settle with the board: whether
the catalogue stays the club's (the club is the controller of the data and the owner of the domain)
or becomes a project with its own governance; who moderates, answers what is reported and takes
things down; what another group that joins may do with its own places and what the club may do with
them; and whether the data is offered under an open licence (which also makes the ODbL question
easier). Without this the technical stages 6 and 7 are premature.

**8. Licences and law.** To be seen with the board, and a lawyer where it matters (none of this is
advice):

- *GDPR and LOPDGDD*: a privacy policy, who is the controller, the legal basis, retention, rights of
  access and deletion, processors (an SMS provider, a hosting, a CAPTCHA), and the cookies (only
  technical ones, so no banner, if that stays true). What passes from the intranet to the catalogue
  (decision 4) is part of it.
- *OpenStreetMap*: opening the catalogue makes the "publicly used" of the ODbL real
  (`osm-integration.md`, section 3). Either what came from OpenStreetMap is kept apart and credited
  ("© OpenStreetMap contributors") and the club's own data stays outside the derivative database, or
  the club accepts the share-alike for what it mixes. The fields copied from OSM when a place is
  made from an element need to be known and marked.
- *Photos and texts*: members' content published openly needs a licence or a consent from the author
  (and a way to take it down); EXIF of a photo can hold its position (the pictures that are made
  smaller lose it, the others do not).
- *Safety and conservation*: technical sheets and access texts published openly invite people into
  caves; a notice that the club is not responsible, and protected sites (bats, archaeology, private
  land or a landowner's permission) that must not be public whatever the rest says.
- *The federation* may have rules or its own catalogue to agree with.

**9. Writing.** The proposal is to leave it for outsiders for later. Options when it comes:
registered people can *propose* (a correction, a photo, a new place) into a queue that the owning
group's admin or board approve, which needs a moderation screen, notices and a way to undo; or they
can only comment. Direct writing by anyone who registers is not advised. For the club's members
nothing changes at first. Settle whether writing permissions are per place (the author, a group of
explorers of a system) and whether edits are attributed publicly.

### Work, in this order

Each step can be deployed alone and none makes anything public before the last ones.

1. **Decide** what has to be decided first: 1 (the classification), 3 (the policy for coordinates),
   4 (identity), 6 (the site and its name), 7 (governance) and 8 (the legal points). Without them
   the next steps are guesses; 2 and 5 can wait for the experience of the first stage. *Done*,
   except the points that "Decided by the board" leaves open.
2. **Draw the boundary inside the repository** (no change in behaviour): `internal/places` (and
   `areas`, the mirror) import nothing from `users` or `activities` and use small local interfaces;
   their tables go in a schema of their own through a new migration, with no foreign key to `users`
   or `activities` (`activity_places` keeps the place id as a plain reference, and the "a place with
   activities is not deleted" check goes through an interface of the activities); `created_by` and
   the like stop being a key to a person and become an opaque reference the intranet translates to a
   name. Existing migrations are never edited. *Done so far*: the code of `places` and `areas` no
   longer joins `users`, their columns have no foreign key to `users` (migration `00080`) and
   `users.Service.Delete` asks the catalogue instead (`AuthorGuard`). The catalogue has accounts of
   its own (`internal/accounts`, migration `00083`): what it keeps of who made something is the id
   of an account, a foreign key with a meaning inside the catalogue; the intranet of the club is the
   first service (an issuer) and each person who acts has a person account, linked to the identity
   the intranet knows them by (a random `users.subject`) and made the first time they change
   something. So the catalogue holds no number of the intranet's, and a client other than the
   intranet only needs to be registered. The code of `places` no longer reads `activities` nor the
   other way round: they refer to each other by id through `places.ActivityLookup` and
   `activities.PlaceLookup`, with the types they exchange in `internal/placelinks`;
   `activity_places` belongs to the activities, and the foreign key to `places` is kept for now as a
   backstop. The tables of the catalogue are in a schema of their own, `catalogue` (migration
   `00081`), and the SQL names them with it. *Left*: the last foreign key that crosses the border,
   that of `activity_places` to `places`.
3. **A viewer and one place for the question.** A `Viewer` as an assertion (issuer, subject,
   groups, expiry), an optional-authentication middleware next to `Protect`, the intranet as the
   first issuer (the assertion is made from the session that exists) and one question every handler
   asks, backed by rules, with tests that cross every viewer with every audience, part and action.
   Nothing is public yet: every place is for the club's group. *Done*: `internal/access` (the
   `Viewer`, the audiences, the parts and `Rules.Allows`), `auth.ViewerOf`,
   `Authenticator.WithViewer`, and a gate on every route of the places that asks the rules
   (`ClubOnly()` for now, so nothing changed for anybody). *Left*: the rules per place, in the data
   (step 4), and the parts asked where the answer carries them. The subject of the viewer is the
   person's opaque one, no longer their id, and the catalogue gives them an account of its own.
4. **Visibility in the data**: the audiences in `places` (three levels and a lock), a migration that
   puts everything at the club, the choice in the place's page (admin and board only), a notice for
   each change, and the redaction of what a viewer may not see. *Done*: migration `00082`, `PUT
   /places/{id}/settings/visibility`, the dialog "Visibilidad", the notice, and the redaction
   (`ViewedBy` on places, the listing and systems, applied where they are read, with tests that
   cross every viewer with every visibility). *Left* (they are the next step's, since only the club
   can reach the routes today): redacting the lists of systems, the files, the points and the
   activities, the map and the copies for working off line, as each route is opened.
5. **The public read API and pages**: the separate group of routes, the redacted projection (no
   logins, no activities, coordinates as decided), server-rendered pages with their URLs, sitemap,
   caches, rate limits, credits for maps and for OpenStreetMap. Tried in testing with the real data
   copy and a checklist of what an anonymous reader can and cannot get, including the sources other
   than the page (the API, the sitemap, the tiles, the notices, the files by id). *Done*: the
   routes (`internal/publicplaces`: `GET /public/places` and `/public/places/{id}`), the projection,
   the rate limit and the cache headers, with tests that cross every visibility with who asks.
   *Left*: the systems, the files and the points apart from the place, the pages with their
   addresses and sitemap, the domain `cuevasysimas.es` answered by this process, the credits for the
   maps and the checklist in testing.
6. **Registered users and opening**: the table of accounts, the sign-up with a verified email, the
   optional phone, the sign-in that does not tell who exists, the account page (data, deletion,
   export), the limits against abuse; then the domain, the links from `www.katiuskas.es`,
   `robots.txt`, a first small set of places marked public by the people who know the caves, and a
   way to take any of them down at once.
7. **Take it out into a service of its own.** The board chose a project with its own domain and
   governance, so this is the plan and not an option. What it takes: its own repository, database,
   deployment, backups and monitoring; the intranet reaching it with the assertion (and a browser
   token if the pages call it directly); `activity_places` working with a reference that may point
   to a place that is gone; and the notices and the off-line copies moved with it. It can come
   before or after the first opening, since the boundary is drawn either way; until then the same
   process can answer the new domain.
8. **Later**: other groups and per-person rules (more use of the table of rules), proposals and
   moderation (9), the phone verified by SMS if it is wanted, and contributing to OpenStreetMap
   (`osm-integration.md`, section 5) once the question of private locations is closed.

### Risks

- **A location published by mistake is for good.** The default must be closed, every route that
  carries a coordinate must go through the one redaction, and a public place must be marked by a
  person on purpose.
- **Leaks through the side doors**: the list with all the coordinates, the sitemap, the map tiles
  and markers, the Telegram notices, the files served by id, the copies kept on a phone, and
  `created_by` and the activities of a place.
- **A mistake in the assertion**: a wrong group, a key that leaks, an expiry that is too long. It
  decides who is a member, so it needs tests of its own and a way to rotate the key.
- **Outsiders in the club's lists**: the reason to keep them apart from `users`.
- **Two systems for few hands**: the cost of running a second service, and of keeping a reference
  between services consistent, is only worth paying for a reason (step 7).
- **Abuse**: fake accounts, SMS pumping, scraping, and spam through any text field that is public.
- **Content and responsibility**: safety information and protected sites published without the
  right to publish them, and nobody who owns the answer (decision 7).
- **Cost and care**: a public site is read all day, so caching, monitoring and a person who answers
  what is reported are part of the work, not an extra.
