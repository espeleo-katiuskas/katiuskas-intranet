# Taking the catalogue of places out: «Cuevas y simas»

The catalogue of places is going to be an independent project, in a repository of its own, apart
from this one. This page says what independent means, what was decided, how the two will talk to
each other and in what order it is done. The reasons for opening the catalogue to the public, and
what the board decided about it, are in [`roadmap.md`](roadmap.md).

## What independent means

- **Its own domain**: `https://cuevasysimas.es`, not a path or a subdomain of the club's.
- **Its own API and its own server**, which may well be another machine: no process, no filesystem
  and no database in common with the intranet.
- **Its own database** (PostgreSQL). No foreign key, no query and no shared table crosses between
  the two; what they share is a contract over HTTP.
- **The same stack**: Go, PostgreSQL and HTML, CSS and JavaScript with no dependency that is not
  indispensable. No shared Go module either: the few small pieces that both need (the HTTP helpers,
  the database and configuration code, the logging) are copied.

## What was decided

- The repository is called **`cuevasysimas`**, its owner is Juan Céspedes, its licence is MIT (as
  this repository's), and the history of git of the packages that move is not kept: the new
  repository starts with a copy.
- **Members of the club edit only in the intranet.** They do not need to enter the site of the
  catalogue, which has no sign-in and no editing of its own: it is a read-only site for the public.
  The pages of the intranet that list, edit and map the places stay in this repository and keep
  working off line.
- The club is one **client** of the catalogue (an issuer, in the catalogue's words); others may
  come.

## How the intranet reaches the catalogue

The intranet's own backend is the only thing that talks to the API of the catalogue for writing. The
browsers of the members keep calling the intranet, at the same addresses as today (`/api/v1/places`,
`/systems`, `/areas`, `/osm`…), and the intranet forwards the call: it checks the session, makes an
**assertion** about the person, and sends the request on to the catalogue with it. So the frontend,
the service worker and the outbox of the intranet need no change, and the roles stay the intranet's.

- **The assertion** says who is acting: the issuer, the subject (`users.subject`, the opaque
  identifier of the person), the name (the login), the groups (`katiuskas` for admin, board and
  members) and when it expires, a few minutes at most. It is signed with Ed25519 (`crypto/ed25519`,
  of the standard library); the catalogue holds the public key of each issuer it knows, and the
  intranet keeps the private one. A request carries it in the `Authorization` header. The catalogue
  never calls the intranet to check a person.
- **What the catalogue answers** is what it answers today to a viewer of that issuer and groups: the
  same rules and the same redaction (`internal/access`, `places.Visibility`), the same accounts made
  the first time a person changes something (`internal/accounts`), the same public routes and pages
  for everybody else.
- **What the intranet adds**: the number of activities that went to each place (`activity_count`),
  which the catalogue cannot know, and the two routes that are about the club's activities and not
  about places (`GET /users/{login}/places`, the places a person has been to, and `GET
  /places/{id}/activities`). They are the intranet's, and ask the catalogue for the names.

## What each side keeps of the other

- **Activities and places.** `activity_places` keeps the id of a place, with no foreign key, and the
  intranet asks the catalogue for its name and type. The catalogue knows nothing of activities.
- **Not deleting what is in use.** Today a place that activities went to cannot be deleted. Between
  two servers that becomes a register of **references**: a client says that it uses a place, and the
  catalogue does not delete a place that any client uses. (A client that stops using one says so.)
- **People.** The catalogue keeps accounts of its own, linked to the identity that each issuer knows
  a person by; it has no table of the club's members and asks for none. The intranet's refusal to
  delete a person who made something in the catalogue becomes a question over HTTP, or goes away:
  the name stays in the account either way.
- **Notices.** The catalogue tells its own Telegram channel, with a bot of its own, of what changes
  in it.

## What moves and what stays

Moves, as it is: `internal/access`, `internal/accounts`, `internal/places`, `internal/areas`,
`internal/osmmirror`, `internal/publicplaces`, `internal/publicsite`, `cmd/osmsync`, the migrations
of the schema `catalogue` (as one baseline), the OpenStreetMap mirror and its service, and the
scripts of backup, deployment and copy to testing that concern it.

Stays here: the pages of the intranet for places (`lugares`, `lugar`, `sistemas`, `sistema`,
`brujula` and their scripts), the activities and everything else; this repository gets a client of
the catalogue's API, the assertion and the forwarding.

## Order of work

1. **In this repository, without changing what members see** (done): draw the line so that nothing
   of the catalogue depends on the intranet, test it, and write the contract down. - The handlers of
   the places and of the zones no longer look people up: a notice names who acts by the name of the
   viewer. - The places a person has been to (`GET /users/{login}/places`) are the activities' route
   now, which ask the catalogue for the names (`PlaceRef` has the text of where). - The tests of the
   catalogue need none of the intranet's tables or services: they use `testutil.NewPerson` (a
   subject and a name, no row anywhere), a fake of what the activities tell the catalogue, and the
   public routes and pages as anybody sees them. - The line is a test (`internal/boundary`),
   described below.
2. **The new repository** (started; it lives in `cuevasysimas`, beside this one): the packages
   above, a baseline migration, the verification of assertions, the references and the site, with
   their own tests, CI, Dockerfile, docs and systemd units. What it has:
   - the catalogue (`places`, `areas`, `osmmirror`, `access`, `accounts`), the public routes and the
     public site, copied as they are, and `cmd/osmsync`;
   - `internal/assertion`: assertions signed with Ed25519 and checked with the key of each issuer
     (`catalogue.issuers.public_key`), and `cmd/issuer` to make keys and register a client;
   - the references (`PUT/DELETE /places/{id}/references/{key}`) that replace `HasActivities`, and
     `GET /places/refs` that replaces `PlaceLookup`; `activity_count` is no longer the catalogue's;
   - its own `auth`, `httpapi`, `config`, `testutil` and server (`cmd/cuevasysimas`), and the
     baseline migration (`00001`, the sum of 00061 to 00083 here) and `00002`.

   The intranet side is written (`internal/catalogueclient`, on when `CATALOGUE_URL` is set; how it
   works is in `backend-internals.md`, "The catalogue as a separate project"): the assertions, the
   forwarding of the members' requests with `activity_count` added, the names of places asked over
   HTTP, the references the activities make (and `cmd/syncrefs`, which sends them all), and
   migration `00084`, which drops the last foreign key into the catalogue. Without `CATALOGUE_URL`
   nothing changes. The refusal to delete a person who made something in the catalogue does not
   exist in that mode: the catalogue's accounts outlive the people.

   What is still to do before the cutover: trying both together with a copy of the real data, and
   the points that the new repository's `docs/roadmap.md` lists.
3. **The cutover, once**: the edition of places is frozen for a short time, the data and the files
   are copied, the intranet starts forwarding, and the schema `catalogue` is dropped from its
   database. There are never two sources of truth.

## The line, in a test

`internal/boundary` reads the imports of every package and of its tests, and fails for what crosses
the line:

- A package of the catalogue (`access`, `accounts`, `areas`, `osmmirror`, `places`, `publicplaces`,
  `publicsite`) may import only the others, the contract (`placelinks`) and the small infrastructure
  that is copied into the new repository (`auth`, `httpapi`, `platform/*`). Never `users`, `roles`,
  `memberships`, `licenses`, `activities` or anything else of the intranet.
- Its tests may use only a few things of `testutil` (`NewTx`, `NewTestAuthenticator`,
  `FakeResolver`, `TokenFor`, `NewPerson`, `Person`, `CatalogueAccount`): the rest makes rows of the
  intranet's tables.
- What is not the catalogue may import it only at the points of the contract: the composition root
  (`cmd/api`), the OpenStreetMap load (`cmd/osmsync`), `auth` and `users` (the viewer and the
  issuer, both of `access`), `activities` and `catalogueclient` (`placelinks`), `cmd/syncrefs` (the
  club's group) and the fixtures of `testutil`. The tests of the activities and of `cmd/api` may
  also use the real catalogue.

When the catalogue really needs something new from the intranet, the answer is to put it in the
contract, not to widen the lists of the test.

## What is still across the line, on purpose

These are what the contract replaces when the catalogue leaves; until then they are interfaces with
primitive types, injected by `cmd/api`:

- `places.ActivityLookup` (`ActivityCounts`, `HasActivities`) and `activities.PlaceLookup`
  (`PlaceRefs`), with the types of `placelinks`. In the new repository the catalogue does not know
  the activities: the counts become the intranet's, and `HasActivities` the register of references.
- `users.AuthorGuard`, which refuses to delete a person who made something in the catalogue.
- `auth.ViewerOf`, which turns a session of the intranet into the viewer that the catalogue
  understands: in the new repository it is the verification of a signed assertion.
- The foreign key from `activity_resources` to `activity_places` (both the activities'). The one
  from `activity_places` to `places` is gone (migration `00084`).

## Still open

- Which server hosts the catalogue, and who looks after it.
- How the club's Telegram channel hears of changes in the catalogue, if it is to (a webhook of the
  catalogue to the intranet is the way, and it is not needed to start).
- Who is legally responsible for the site (the board's open point), which its legal notice needs.

