# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this
repository.

## Language

- **All code** (identifiers, comments, logs, API error messages): **English**.
- **Documentation** (this file, `docs/`, `README.md`): Spanish or English, either is fine.
- **Frontend** (`web/`): all user-visible text in **Spanish** — it's the interface for
  Club de Espeleología Katiuskas, a Spanish caving club. JS/HTML identifiers themselves
  stay in English.

## Commands

Local stack (Postgres + API + Caddy-served frontend):
```sh
docker compose up --build
```
API on `:64711` (direct) / behind Caddy on `:64710` with `/api/*` proxied; frontend on
`:64710`.

Migrations and first admin account (run once against a fresh database):
```sh
export DATABASE_URL=postgres://intranet:intranet@localhost:64712/intranet?sslmode=disable
make migrate-up      # goose, see db/migrations/
make seed-admin      # idempotent; creates admin@club.test / 600000000 unless SEED_ADMIN_* overrides
```
There are no passwords: sign-in is a magic link. With `SMTP_HOST` unset, the API logs the
link instead of emailing it — `docker compose logs api`, or `scripts/get-token.sh <login>`
to mint a session token directly in the database for local testing, bypassing the magic-link
flow entirely.

Production deployment (self-hosted, behind a reverse proxy the host already runs for
TLS): the club's own host runs without Docker, as systemd units —
`docs/deployment-no-docker.md`: native PostgreSQL/Caddy plus systemd-managed Go binaries
cross-compiled elsewhere and copied over. `docs/deployment.md`,
`docker-compose.production.yml` and `.env.production.example` are the Docker-based
alternative, which the club's host doesn't use. `scripts/get-token.sh` is dev-only — never
use it there.

There are three environments (full detail in `docs/environments.md`): the local Docker
stack above, where changes are tried before committing or pushing, and testing
(`testing.katiuskas.es`) and production (`intranet.katiuskas.es`), two systemd
installations side by side on the club's host, each with its own database, install
directory and Telegram bot, sharing `LIBRARY_ROOT`, `USER_FILES_ROOT` and
`ACTIVITY_RESOURCES_ROOT`. Updates are automatic and triggered by pushes: **every push to
`main` updates testing, and every push of a semver tag updates production**, so pushing to
`main` is a live deploy rather than just publishing commits. That automation isn't defined
in this repo (`ci.yml` only tests). Testing's database holds a copy of production's data,
refreshed on every production deploy by `scripts/copy-prod-to-testing.sh` (a full
dump/restore of production followed by `goose up`, plus — unless `--no-files` — replacing
testing's own `ACTIVITY_RESOURCES_ROOT` with hard links to production's files, which it
refuses to do while the two environments share one directory; the two databases are
separate only because the schema can differ while `main` carries migrations production
hasn't received), so testing
contains real personal data and anything done there is overwritten at the next production
deploy. The shared directories make two things dangerous to do in testing: because
`user-files/<login>/` is keyed by login, granting someone admin/board there gives them
read access to every member's personal documents; and because `activityresources.Storage`
creates and deletes `<activity id>/<resource id>` files and testing's rows carry
production's ids, deleting a memoria file in testing deletes the real file production still
references, and uploading one can occupy a path production's own next upload then fails on
(`Save` refuses to overwrite). `docs/environments.md` lays out the options for
`ACTIVITY_RESOURCES_ROOT`, including a separate directory copied from production with hard
links (no extra disk space; since `Save` never overwrites, no copy order or sequence state
can corrupt a shared file — at worst a testing upload fails).

Backend tests (from `backend/`):
```sh
docker exec -it katiuskas-intranet-postgres-1 psql -U intranet -d postgres -c "CREATE DATABASE intranet_test;"
export TEST_DATABASE_URL=postgres://intranet:intranet@localhost:64712/intranet_test?sslmode=disable
go test ./...                                    # all packages
go test ./internal/users/...                     # one package
go test ./internal/users/... -run TestCreate      # one test
```
Every package touching Postgres (all but `auth`, `config`, `mail`, `httpapi`, `telegram`,
`library`, `userfiles`) runs against a real test database — no mocked data access, since
Postgres constraints (exclusion, foreign keys, uniqueness) are part of what's under test.
`internal/testutil.NewTx`
migrates once and hands each test its own transaction, rolled back on cleanup. Without
`TEST_DATABASE_URL` reachable, those tests `t.Skip` instead of failing. It only checks
whether the schema exists at all, never which migrations it has: a test database created
before a new migration was added has to be dropped and recreated (`DROP DATABASE
intranet_test` then the `CREATE DATABASE` above), or tests fail with errors like "column
... does not exist". CI starts from a fresh one every run.

Other:
```sh
make run                          # go run ./cmd/api, no Docker
make build                        # backend binary to bin/api
go vet ./...                      # from backend/
go build ./...                    # from backend/ — cheapest full compile check
python3 -c "import yaml; yaml.safe_load(open('backend/openapi/openapi.yaml'))"  # validate the spec after editing it
```
Checks for the frontend, before a commit that touches `web/` (the files are plain ES
modules, with no build step to catch a mistake): `node --check` parses a file without
running it, `biome lint web/js` reports unused imports and variables and the like, and
`biome format web/js` says whether the code is laid out to the one style the project has
(two spaces, single quotes, lines of at most 100 characters; `biome format --write web/js`
rewrites the files to it). All of it is configured in `biome.json`, and only covers
`web/js`:
```sh
for f in web/js/*.js; do node --check "$f"; done
biome lint web/js
biome format web/js
```
None of them runs a page; for that, the headless Chromium below.

Rebuilding/redeploying just the API after a backend change:
```sh
docker compose build api && docker compose up -d api
```
The frontend needs no rebuild — Caddy bind-mounts `web/` live (see `deploy/Caddyfile`),
and sends `Cache-Control: max-age=3600` for everything it serves from there: without any
cache header, browsers guess a freshness lifetime of a tenth of the file's age, so a file
unchanged for weeks can be served stale for days after a deploy. A fixed hour is the
trade-off: `no-cache` (revalidate each file, a cheap 304) made every navigation take about
a second over a real connection — a page loads a chain of scripts that import one another,
so each costs a revalidation round trip in sequence. The price is that after a deploy a
browser may keep an old page or script for up to an hour, mixed with new ones; the people
who use the intranet daily are the developers, who know to reload the page bypassing the
cache (how depends on the browser and device, and on a phone or tablet there is no easy
way — hence an hour and not a day). Only `web/` goes through this rule: documents and
files come from `/api/*`, which Caddy leaves to the API. The same Caddyfile compresses
whatever travels as text (`encode zstd gzip`: the scripts and styles are about a third of
their size, and the API's JSON — `GET /users` is over 100 KB — much less than that), and
leaves images and PDFs alone. The club's own Caddy, outside this repo, needs both lines in
its site block (`docs/deployment-no-docker.md`).

Club documents (estatutos, actas...) shown at `/archivo`: drop files into `./library/` next
to `docker-compose.yml` (bind-mounted into the API container, auto-created if missing) — no
rebuild, no restart, nothing through the API itself. That directory is gitignored.

Per-person documents (a scanned DNI, a signed waiver...) shown at `/users/{login}/files`:
drop files into `./user-files/<login>/` next to `docker-compose.yml` (same bind-mount,
auto-create, gitignored, no rebuild/restart story as `./library` above), one subdirectory
per login. A copy of each federative license goes in that same person's `licencias/`
subdirectory, named `yyyy-mm-dd-tipo_de_licencia.ext` (image or PDF); an optional
thumbnail for one goes in `licencias/.thumbnails/`, named after that same original
filename plus `.jpg` (e.g. `licencias/.thumbnails/2025-03-15-federativa.pdf.jpg`) — see
**Frontend** below for how `/perfil` and `/socio` read both conventions back out.
`scripts/generate-license-thumbnails.sh` makes every missing (or out-of-date) thumbnail
from the license copies — first page of a PDF, or a smaller version of an image, 280px on
the longest side — and only needs `pdftoppm`/ImageMagick on the machine running it
(`--help` for the options: `--root`, `--force`, `--dry-run`, or just some logins).

Checking how a page actually looks, in an environment with no GUI: Claude Code's own
browser-automation tool ("Claude in Chrome") needs a real, windowed Google Chrome plus a
direct Anthropic plan login, neither of which exists on a headless dev box — but
`chromium` (`sudo apt-get install chromium`; a system package, not part of this repo) run
headless and driven over the Chrome DevTools Protocol works just as well for a quick
visual check. Launch it once, keeping it running in the background:
```sh
chromium --headless=new --disable-gpu --no-sandbox \
  --user-data-dir=/tmp/chromium-profile --remote-debugging-port=9333 about:blank &
```
Then script it — no Node/Playwright available either, so a plain Python script talking
raw WebSocket frames to `ws://localhost:9333/devtools/page/<id>` (the id comes from
`PUT http://localhost:9333/json/new?<url>`) works, needing nothing beyond the standard
library (`socket`, `hashlib`, `struct`, `json`). To view a page that requires being
signed in: mint a session token with `scripts/get-token.sh <login>` (see above), navigate
to the app's own origin first, run `Runtime.evaluate` with
`localStorage.setItem('token', '<token>')` — the same key `js/api.js` itself reads on
every page load — then `Page.navigate` to the real page and, once
`Page.loadEventFired` fires and a couple of seconds have passed for that page's own
client-side fetches to resolve, `Page.captureScreenshot` (`captureBeyondViewport: true`
to get the whole page, not just the current viewport).

## Architecture

**Layers**: `backend/internal/httpapi` (router assembly, `ClientIP`, request logging,
panic recovery, `WriteJSON`/`WriteError`) → one package per domain resource (`users`,
`roles`, `memberships`, `licenses`, `board`, `activities`, `activityresources`,
`distributions`, `inventory`, `tokens`, `accounting`; `adminstatus` is the odd one out,
see below), each split into `model.go` / `repository.go` (pgx) / `service.go` /
`handler.go`. The domain layer never imports `net/http`; only `handler.go` knows HTTP
exists. Each resource package owns and registers its own routes via `RegisterRoutes(mux)`,
wired together in `cmd/api/main.go`. **`internal/library`** (and **`internal/userfiles`**,
see below) is the one exception to this shape: it serves a read-only document tree from
disk (`config.LibraryRoot`), not Postgres, so it has no `repository.go` — `service.go`
works directly against the filesystem, resolving and validating every caller-given path
against root before touching disk (`Service.resolve`), since there's no database
constraint to lean on for that instead.

**`db.PGX` interface** (`internal/platform/db`): the subset of `*pgxpool.Pool` a
repository actually calls. Both a pool and a `pgx.Tx` satisfy it, so production code
builds a repository on the pool while tests build the same repository on a transaction
that gets rolled back — the repository code never knows the difference.

**Auth is a single opaque, revocable, database-backed session token** — no JWT, no
separate access/refresh pair. `internal/auth` only defines `AuthenticatedUser`,
`Authenticator.Protect`/`RequireAuth`/`RequireRole`, and a `SessionResolver` interface; it
has no database dependency. `internal/tokens` is the real resolver: it hashes the raw
token, validates and touches `last_used_at`/`last_used_ip` in one query, then resolves
roles *live* (not baked into the token) via `roles.Service`. `tokens` also exposes
admin-only endpoints (`GET`/`PATCH`/`DELETE /users/{login}/tokens[/{tokenID}]`, and
`GET /tokens` listing everyone's — the active ones by default, `?status=all` for the
history, paged by `limit`/`offset` — which marks the caller's own session `current`, found
by the hash of its bearer token) to audit, re-expire, or revoke any token, plus
`POST /users/{login}/impersonate` (admin-only, issues a short-lived token —
`config.ImpersonationTokenTTL` — that resolves as the target person in every way except
`AuthenticatedUser.ImpersonatedBy`, which names the admin) and
`POST /auth/impersonate/exit` to end one and get a fresh normal token back. The same
session token is also mirrored into an `HttpOnly`/`Secure`/`SameSite=Lax` cookie
(`auth.SetSessionCookie`, name `katiuskas_session`, scoped to `/api/v1`) whenever one is
issued or refreshed (sign-in, impersonation start/exit) — every ordinary route still only
ever looks at the bearer header (`auth.Protect`/`RequireAuth`), so this cookie existing
changes nothing about them. It exists purely for
`auth.ProtectAllowingCookie`/`RequireAuthFromHeaderOrCookie`, which `internal/library` and
`internal/userfiles` use on their own file-serving `GET` routes specifically (nowhere
else) so a document/file can be a plain, browser-navigable `<a href>` link — opened
directly in a new tab, or saved by the browser's own "Save As" with the real filename —
instead of needing `apiFetchBlob`'s fetch-then-`Blob` dance the way `library.js` otherwise
uses for everything else; `web/js/dashboard.js`'s own Documentos/Actas/Memorias links lean
on exactly this to stay plain links. `ClearSessionCookie` removes it on logout, so a
signed-out browser can't open a file link straight from history or a bookmark anymore.
`internal/authapi` glues magic-link request/consume/logout and `GET /auth/me`
(identity+roles resolved from context, one light query for the login and name — the
frontend calls this once per page load since an opaque token has nothing to decode; the
name is there so the topbar's greeting and the impersonation banner don't need a second
request for the person's record, which every page used to wait behind). The magic-link token
itself (`auth.NewMagicLinkToken`, 128 bits/16 bytes) is deliberately smaller than a
session or impersonation token (`auth.NewOpaqueToken`, 256 bits/32 bytes) — it's the one
opaque token in this app that ends up in a URL rather than only ever sent as a header, so
its length actually matters, and it only needs to resist guessing within `MagicLinkTTL`'s
own short window rather than staying a valid bearer credential for weeks; 128 bits is
still the OWASP-recommended *minimum* for a session identifier, astronomically out of
reach for brute-forcing in that window given `POST /auth/magic-link/consume` has no rate
limit of its own (unlike the login code below, capped by a per-row attempt counter) —
token entropy alone is what carries this endpoint's security. A magic-link email also
carries a 6-digit login code — hashed alongside the link's token in the same
`magic_link_tokens` row, so consuming either one consumes both — for signing in on a
device other than the one the email was read on; `POST /auth/magic-link/consume-code`
identifies the caller by email+phone (the code alone is too short to look someone up by)
and a per-row attempt counter bounds brute-forcing it without affecting the link itself.
That lookup runs before the attempt counter is ever reached, though, so — unlike
`/auth/magic-link/consume` above, which needs none — this route shares the same
per-calling-address `httpapi.IPRateLimiter` as the initial `POST /auth/magic-link` request
(`authapi.Handler`'s own `ipLimiter`, one limiter/budget covering both routes): without
it, an address could scan through many different email/phone guesses here with nothing to
slow it down, the same gap the initial request's own throttle exists to close. Both this
and the initial `POST /auth/magic-link` request resolve email+phone through the same
`users.Repository.FindActiveByEmailAndPhone`, whose email side matches case-insensitively
— the same as `internal/telegrambot`'s own `emailMatchesUser` check, so the same address
behaves the same way whether someone signs in from the web or from Telegram. Whoever's
linked their Telegram account (see **Telegram** below) gets the same link and code by DM
too, alongside — never instead of — the email.

`httpapi.RequestLogger`'s per-request log line includes `user_id` once `auth.RequireAuth`
resolves one. That takes a small mutable box placed in the request context before
dispatch (`httpapi.SetRequestUserID`) rather than just reading it back from context after
the handler returns — `RequireAuth` builds its *own* new request via `r.WithContext`
deeper in the chain, which `RequestLogger`'s own `r` variable never sees again.

**Baseline access policy**: `Authenticator.Protect(handler, roles...)` *always* also
requires at least one of `auth.BaselineRoles` (`admin`, `board`, `member`), on top of
whatever specific roles a route lists — holding only, say, `material` without also being
admin/board/member blocks every route, including material ones. `RequestMagicLink` won't
even send a sign-in link to someone matching on email+phone but holding no baseline role.

**Roles**: `admin`/`board`/`member`/`former_member` are system roles (`is_system`) wired
to real behavior; anything else an admin creates is just an organizational label. `board`,
`member`, and `former_member` are all *automatic* (`is_automatic`): never granted/revoked
by hand (rejected with 409), computed live in `roles.Repository.ListNamesForUser` from
whether the person currently has an open `board_members` or `memberships` period — so
losing membership, or a board term ending, revokes access on the very next request, no
stale role to clean up. Revoking the one non-automatic system role, "admin", has two
guards of its own beyond the automatic-role check above: `roles.Service.Remove` rejects
(`ErrLastAdmin`, 409 `last_admin`) taking it from the last person who holds it — someone
has to be left who can still manage people and roles at all — and, checked first since
it's the more specific rule and doesn't depend on how many other admins exist,
(`ErrSelfAdminRemoval`, 409 `self_admin_removal`) an admin trying to remove their own
admin role, full stop. Losing your own admin access has to be something another admin
does to you, not a mistake you make removing it yourself — a stray click on your own row's
"×" on `/socio` (`web/js/member.js`'s own `renderRoleBadges`, which doesn't even render
that button for your own admin badge, though the API enforces this regardless of what any
particular client does) is exactly the failure mode this exists to rule out. Deleting a
person (`DELETE /users/{login}`, admin-only) has the same last-admin guard:
`users.Service.Delete` returns `roles.ErrLastAdmin` (409 `last_admin`) for the only
remaining admin.

**Login is the only external identifier for a person**: `users.login` (`text UNIQUE NOT
NULL`, migration `00002_users.sql`) is generated automatically at creation — first
and last name slugified (accents stripped, `users.slugify`) and dot-joined, with a numeric
suffix on collision (`users.Repository.generateLogin`) — and admin-editable afterward via
`PATCH /users/{login}` (format-checked, `ErrInvalidLogin` on a bad one; `ErrDuplicate` if
another person already has it). No response or path anywhere in the API ever carries a
person's numeric database id; every package that used to key a route or a JSON field on
one (`tokens`, `roles`, `licenses`, `memberships`, `board`, `inventory`, `activities`) now
takes a login instead, translated to the internal id via a small, locally-defined
`UserLookup` interface (`UserIDForLogin`/`LoginForUserID`) that `*users.Service` satisfies
structurally — those packages can't import `users` directly, since `users` already imports
several of them. A listing that can span several different people at once (e.g.
`GET /transactions`, `GET /activities`) resolves the login via a SQL `JOIN` against `users`
instead of a lookup per row; a single-row write via `INSERT`/`UPDATE ... RETURNING` (which
can't `JOIN`) resolves it from a login the handler already has in scope, or with a
follow-up `LoginForUserID` call.

**Email and phone can each hold more than one value**, space-separated (`users.email`/
`users.phone`, no separate table) — signing in and a magic link work against any one of
them (`Repository.FindActiveByEmailAndPhone`). That rules out a plain column `UNIQUE`
constraint, since Postgres can't express "no two rows share an element" that way, so
`users.Repository` checks it in Go instead: `emailTakenByAnotherUser` (an array-overlap
`EXISTS`, case-insensitive — "foo@x.com" and "FOO@X.COM" count as the same address, the
same as `FindActiveByEmailAndPhone`'s own login lookup) and `phoneTakenByAnotherUser` (the
same shape, but comparing digits only, so "611-222-333" and "611222333" count as the same
number; `FindActiveByEmailAndPhone` also ignores a leading "+34" in the phone typed on the
login page, see `phoneDigitsForLogin`), both called from `Create` and `Update` and both
returning `ErrDuplicate` on a hit. Neither an individual email nor an individual phone may
belong to two different people at once, even though either field can hold several —
sharing either would make a lookup meant to resolve to exactly one person land on
whichever one happens to come back first instead: `FindActiveByEmailAndPhone` for email,
`internal/telegrambot.Bot`'s own phone-based account resolution
(`users.Repository.FindByPhoneNumber`) for phone.

**Permission tiers**: `admin` has full read/write access everywhere, including a person's
core data and role assignment. `board` can read everything (the full `User` record,
licenses, roles) but can only write activities (and the holiday calendar) and inventory
(creating items, lending/returning material) — every person-data,
role-assignment, license, and Junta Directiva write endpoint rejects board even though it
passes the baseline check. `member` gets `MemberListing` instead of `User` from
`GET /users` (name, gender, computed `age_category` — never the exact `birth_date` it's
bucketed from — membership number, alta/baja dates, `on_leave` (currently on a temporary
leave), `license_type` and `license_year` —
the license for the *current year* only (`licenses.Service.CurrentByUser`; an older one,
or one bought in December for next year, isn't reported, so having a `license_type` is
being federated; there's no end date because a license always runs to December 31st),
never `license_issued_date` — and `last_due_year` (the latest year whose cuota they paid;
the date, `last_due_paid_at`, stays admin/board-only, both computed in
`users.Service.enrichMany` via `memberships.Service.LatestDues`); no DNI or contact info).
That overlay (roles, membership dates, leave, license, last due) is asked of each package
once for the whole list — `roles.ListNamesForUsers`, `memberships.ListByUsers`/
`OnLeaveAmong`/`LatestDues`, `licenses.CurrentByUsers`, each one query with `ANY($1)` —
instead of once per person: `GET /users` used to cost five queries a head, and for three
hundred people that was about 215 ms of the 3 ms every other endpoint takes (now about
10 ms). One person goes through the same `enrichMany`, so a listing can't say something
different from the person's own page; the per-person methods stay for who needs just one
(`tokens`' own role lookup on every request, say).
`/socios` (`web/js/members.js`) filters on those two for everyone — "A corriente de pago"
is `last_due_year` ≥ the current year, "Federado" is having a `license_type` — offers
`last_due_year` as an optional "Última cuota" column, and shows a line between the filters
and the table reading "N personas en esta vista" (`setPeopleCount`: the rows the current
filters leave, not everyone on file). A member may also edit their own contact fields
(`email`, `phone`, `address`, `postal_code`, `city`, `province`, `emergency_contact`) via
`PATCH /users/{login}`; anything else in that body gets 403 `restricted_fields` instead of
being silently ignored.
`MemberListing` deliberately has no `roles` field at all: a plain member has no business
knowing which non-public roles (`admin`, `money`, `material`, or any custom label an admin
created) someone else holds — that's exactly the kind of internal, non-public information
this narrower view exists to keep out, the same as DNI or contact info. The one exception
is a board position, which *is* public knowledge inside the club — that comes from the
separate, baseline-open `GET /board-members` instead, which is where `web/js/members.js`'s
own "Cargo" column (shared by `FULL_COLUMNS` and `MEMBER_COLUMNS` alike, `cargoFor`) gets
it from; a person with no current board position shows nothing in that column, never a
role name. `GET /users` also only ever includes people who are or have been a socio (a
non-nil `membership_start_date`) in a plain member's own listing — someone with no
membership period at all (an admin-only account, say) is left out of it entirely, even
though admin/board still see them (`Handler.list`). That restriction is applied by
`users.Repository.List` itself (an `EXISTS` against `memberships`), not filtered back out
in Go after the fact, so a non-socio row is never even fetched from Postgres. `GET /users`
is deliberately unpaginated — the club's membership grows by roughly 15 people a year,
nowhere near the scale where returning everyone on file in one response is a real cost.
Everywhere people are listed or offered in a picker they are in order of name as it is read
("Nombre Apellidos"), not of surname: the SQL of `GET /users`, the directory, the
memberships, dues and licenses listings and the distributions' people orders by
`first_name, last_name`, and the pages that build a picker sort again on the same key.
`GET /users/directory` (any baseline role) is the name-only alternative: `login`,
`first_name` and `last_name` of everyone on file, with no socio filter — what the
activity, inventory and accounting pickers use to choose a person without seeing personal
data.
The role catalog itself (`GET /roles`) is open to any baseline role, read-only regardless
of who's asking, and lists only the catalog's names/descriptions — never who holds which —
since that much is just organizational labels; assigning/revoking one is still admin-only.

**Activities**: every activity has a mandatory `leader_id` (exposed as `leader_login`; the
organizer, defaulting to whoever creates it) who, along with admin/board, is the only one
who can edit or cancel it (`Handler.canManage`). admin/board can instead name someone else
as `leader_login` at creation time, or reassign an existing activity's organizer via
`PATCH` — the current organizer can't do either for themselves, even though they can edit
every other field. Its `type`
(`caving`/`canyoning`/`training`/`social`/`other`) gates a federation check: joining — or
being named as an initial participant at creation time — a `caving`/`canyoning` activity
requires a currently valid federative license (`licenses.Service.IsCurrentlyFederated`),
else 409 `not_federated`. Its `visibility` (`public`, the default, or `private`) gates who
may add a participant at all, not just who may join: public works as it always has (anyone
can sign themselves up, admin/board can add anyone else); private restricts adding
*anyone* — self included — to the organizer, board, or admin (`Handler.join`).
Removing one (`Handler.leave`, `DELETE .../participants/{login}`) mirrors this: a plain
member can only remove themselves regardless of visibility, but a private activity's own
organizer can remove anyone, the same as they can add anyone — on a public one they get no
such extra say, same as for adding.

**Activity resources / memoria** (`internal/activityresources`): after a trip, whoever
took part in it writes up its memoria as an ordered list of resources — either a text
entry (title + a plain-text/Markdown `description`) or an uploaded file (title + the file
itself, typically a photo or PDF), discriminated by `activity_resources.type` and kept
consistent with a set of `CHECK` constraints (migration `00010_activity_resources.sql`),
the same "description only for text, file fields only for file" shape `item_movements`
already uses for its own type-specific fields. Metadata lives in Postgres (`Repository`);
a file resource's bytes live on disk instead (`Storage`, `config.ActivityResourcesRoot` —
a *named Docker volume*, not a host bind mount like `library`/`userfiles`, since files
arrive exclusively through `POST /activities/{id}/resources` rather than being dropped in
by hand), keyed by the row's own id rather than any caller-given path, so unlike
`library`/`userfiles` there's no directory tree to browse and no path-escape risk to guard
against. A file is created once and never modified afterward — there's no way to replace
its bytes (delete the resource and add another), `Storage.Save` creates it with `O_EXCL`
so it refuses (`os.ErrExist`) to touch one that already exists, and `Delete` is a plain
unlink. That invariant is what lets another directory tree share the same files by hard
link (see `docs/environments.md`) without a write in one changing the other: any future
code that rewrites a stored file in place would silently break it. That same `POST` is
always `multipart/form-data`, even for a text resource (just one with no file part), since
a file resource has to carry an actual upload — capped at 25 MiB
(`activityresources.maxUploadSize`). Every resource has a `position`, a dense `1..N` per
activity that a fresh one is appended after; `PUT /activities/{id}/resources/order`
replaces the whole order at once from a caller-given list of every resource id (rejecting
anything short of an exact match, `reorder_mismatch`, rather than reconciling a partial or
stale one) instead of a move-this-one-item endpoint that would have to shuffle the rest
itself. Reading (`GET`) is open to any baseline role, same as the rest of an activity's
own data; contributing (`POST`, the reorder `PUT`) requires admin/board, the activity's
own organizer, or anyone signed up in `activity_participants`
(`Handler.requireContributor`) — note the organizer isn't automatically a participant row
unless they happened to list themselves when creating the activity (see
`activities.Repository.Create`), so this checks `Activity.LeaderID` *and*
`activities.Service.IsParticipant` separately, not just the latter; editing or deleting a
specific resource (`PATCH`/`DELETE`) is narrower still — only that resource's own author,
or admin/board, even for someone else who took part in the same activity. A file's bytes
come from `GET /activities/{id}/resources/{resourceID}/file`, which unlike
`library`/`userfiles` file routes takes only the bearer header (no cookie).

**Distributions** (`internal/distributions`, migration `00021_distributions.sql`): things
the club hands out to its members (shirts, buffs, a rope, mugs). Each distribution is a
single article — two things to handle separately are two distributions — with a `status`
(`draft` → `open` → `closed` → `done`, one step at a time, forward or back; a draft is
editable and exists only for admin/board, members ask for units only while `open`, `closed`
freezes what can be asked for, and units can be handed out — collections recorded, numbers
written down — in any state but `draft` (`Status.Deliverable`, `ErrNotDeliverable`,
409 `not_deliverable`), since the order often arrives before the asking has ended; migration
`00023` replaced the former `ordered`/`received` by `closed`/`delivering` and `00025`
removed `delivering` (mapped to `closed`), as handing out doesn't depend on closing), an `audience` (`all_members`,
everyone who was a member on `cutoff_date`, which is then required; or `signup`, where
`cutoff_date` is optional and, when set, limits who can sign up to those members —
`Repository.WasMemberOn`, a SQL check against `memberships.period`; with no cutoff date it
is for today's members, `WasMemberNowOrOn`), a `price` (0 = free),
an optional `extra_price` (its presence is what makes extras possible) and `max_extra` per
person (which needs `extra_price`), a `closes_at` deadline, and `distribution_options` (a
name with its possible values, e.g. "Talla": S/M/L; replaceable only while no unit exists,
`ErrHasUnits`, and `Repository.sameOptions` makes sending back the same set a no-op so a
form can resend everything). What people ask for are `distribution_units`, one row per
unit (two extra shirts can have two sizes): `beneficiary_id` is who it is for,
`registered_by_id`/`registered_at` who actually entered it — somebody else when a parent
signs a child up or a friend adds extras for another, and never overwritten by editing the
choices (`UpdateChoices` touches `choices` only) — and `price` is frozen when entered, so
changing the distribution's price later doesn't change what somebody agreed to. A person
has at most one own (non-extra) unit (partial unique index, `ErrAlreadyRegistered`) and
extras up to `max_extra` counted by beneficiary whoever entered them: `CreateUnit` locks
the distribution row (`FOR UPDATE`) while counting so two concurrent requests can't both
slip under the cap. Anyone can ask for a unit for themselves or, with `beneficiary_login`,
for somebody else, while the distribution is `open` and before `closes_at`; admin/board
also after the deadline, but only while `open` — a closed distribution changes for nobody,
reopening it is how a late addition is made (`Service.checkOpen`). Both the beneficiary
and, unless admin/board, whoever enters it have to be eligible (`ErrNotEligible`); an
extra belongs to whoever it is entered for, with no notion of who ends up with the item.
Changing/removing a unit is for its beneficiary, whoever entered it and admin/board/material
(`Handler.ownUnit`; material counts as staff for it, so like admin/board it can also go past
the deadline and change a paid unit, though only admin/board mark payments), under
the same rules; what was handed out is locked for the owner (a unit that isn't a row of an
article is final once handed out, even for staff; in a row of an article staff can still resize it
but not below `picked_up_quantity`, `ErrBelowDelivered`) and a paid one only changes through admin/board/material
(`ErrUnitLocked`). Every payment mark is also written into `distribution_payments` (migration `00030`, the list of
payments: `logPayment`, called by `MarkPaid` and `ClearPaid` in the same
transaction; negative entries for what is taken off, `unit_id` null if its order is
gone since — an order with something paid can't be deleted, `ErrUnitPaid`, 409 `unit_paid`: the payment
is taken off first), which `GET /distributions/{id}/payments` (admin/board, one distribution at a time) serves newest first with
`total_after`, a window `SUM` per distribution over its whole history so it doesn't restart at a
filter or page edge — the same idea as the accounting ledger's `balance_after`. The person who
paid is the order's beneficiary; there is no separate payer. Payment is per row and can be partial (migration `00027`): `paid_quantity` of its
`quantity` are paid, with `paid_at`/`paid_marked_by_id` as when and by whom the last payment was
marked, set exactly when something is paid (`CHECK`s). Admin/board only: `POST .../payments`
adds payments, all-or-nothing (`Repository.MarkPaid`: `payments` of `quantity` units of a row,
all that is pending when 0, plus `unit_ids` for whole rows; more than pending is
`ErrAlreadyPaid`), `DELETE .../units/{id}/payment[?quantity=N]` takes units off
(`ClearPaid`). Asking for more of something paid leaves the added units unpaid, and
`UpdateQuantity` refuses to go below what is paid (`ErrBelowPaid`); a row with anything paid
is "paid" for `checkEditable`. Pickups still mark a whole row. **Payments are
recorded in accounting** (migration `00032_distribution_accounts.sql`): every distribution has an
optional `account_id` (the account its payments go to; `account_name` in the JSON) and, when null, the
account marked `accounts.distribution_default` — both have to be of type `income`: a distribution naming
another type is 400 `account_not_income` (`checkIncomeAccount`), marking one as the default is refused by
`accounting` (`ErrNotIncomeForDistributions`, 409 `not_income_for_distributions`), as is taking the
`income` type off an account some distribution names — (at most one, a partial unique index; marking one in
`accounting.Repository.Create`/`UpdateAccount` unmarks the other) is used — none of the two is 409
`no_distribution_account`. `POST .../payments` takes `account_id` in the body and `DELETE
.../payment` as `?account_id=`: the account the money was paid into or goes back from, which has to
exist (`ErrUnknownAccount`, 400), be of type `asset` (`ErrAccountNotAsset`), and is required whenever money moves (`ErrAccountRequired`;
what costs nothing needs no account and records nothing). In the same database transaction
`recordPaymentTransactions` writes, per beneficiary of the request, a transaction with two entries —
the asset `+total`, the distribution's account `−total`, signs changed when a payment is taken off —
by direct SQL (not through `internal/accounting`, which this package doesn't import), with a Spanish
description of who and what ("Pago de Nombre Apellidos en «Reparto»: 3 × Papeleta (0001-0003)";
"Devolución del pago de …"), and sets `distribution_payments.transaction_id` of the entries it
wrote (`ON DELETE RESTRICT`). A payment made by mistake is undone by deleting its transaction:
`accounting.Repository` runs a hook (`BeforeDeleteTransaction`, wired in `cmd/api/main.go` to
`distributions.UndoPaymentsOf`) in the same database transaction before deleting it, which unpays
what each entry of the list of payments paid (or pays again what it took off) and removes those
entries; if an order has changed since and no longer allows it, nothing is deleted and the answer is
409 `transaction_has_distribution_payments` (`ErrPaymentsNotUndoable`).
Asking again for an article (`CreateArticleUnit`) adds to the person's row of it that isn't
collected yet, else makes a new row — the per-person unique index is gone, and
`max_per_person` (`checkQuantity`) counts all their rows. Collection (`POST .../pickups`,
batch, admin/board/material, only once it is not a draft) can be partial like payment (migration
`00028`): `picked_up_quantity` of a row's `quantity` are handed out, and `picked_up_at`,
`pickup_recorded_by_id` (the caller) and `picked_up_by_id` (whom it was handed to: not necessarily
the beneficiary, as when somebody collects for others, but a member now or on the cutoff date,
`ErrInvalidCollector`, 409 `invalid_collector`) are those of the last delivery, set exactly when
something was handed out (`CHECK`s). `Repository.Deliver` takes `deliveries` (`Delivery`: a
row, a quantity or all that is left, optionally the identifiers) plus `unit_ids` for whole rows,
all-or-nothing, `ErrAlreadyPickedUp` for more than is left. For an article with numbers each
delivered unit is one of the row's identifiers (`distribution_unit_identifiers.delivered_at`):
only the assigned and not delivered can go (the given ones or the first, else
`ErrIdentifiersMissing`; `Service.MarkPickedUp` assigns the missing ones first with
`assign_numbers`), and a delivered number can't be dropped from the row's list. `ClearPickup`
(`DELETE .../pickup[?quantity|?identifiers]`) un-delivers, keeping the numbers assigned;
`ReturnUnits` (`POST .../return`) takes units back for good: the row shrinks (deleted at zero),
the numbers are deleted so they are free, and it can't go below what is paid (`ErrBelowPaid`)
nor return more than was delivered. A row with anything delivered can't be deleted. `GET .../order` (admin/board/material) sums the
units by combination of choices (`GROUP BY` the `choices` jsonb) with the totals, and for
units nobody has chosen the options of yet add up under empty choices. For an `all_members`
distribution still in `draft` — which has no units yet, they are made on opening — the
summary already counts one per member on the cutoff date, without making them
(`Order.Provisional`, set by `Repository.Order`; the page says so), since what has to be
ordered is known from the start.
`GET /distributions` and `GET /distributions/{id}` add `eligible` for the caller;
`GET .../units` shows admin, board and material every unit and anybody else only the ones
for them or entered by them.

In a distribution for `all_members` nobody signs up: each member on the cutoff date has
their own unit from the moment the distribution leaves `draft`
(`Repository.SyncAutomaticUnits`, `INSERT … ON CONFLICT DO NOTHING`, migration
`00022_distribution_units_automatic.sql` making `registered_by_id` nullable). The unit
starts with empty `choices`, nobody as `registered_by` and the distribution's price at
that moment; `Service` runs the sync on opening and before listing units, asking for one,
summarizing the order and recording a collection, so a member who qualifies later shows up
as well. With options to choose, empty `choices` is "not chosen yet". Whoever chooses —
the member through `PATCH`, anybody through `POST …/units` for them, which fills the
existing unit in (`CreateUnit`) instead of making a second one — becomes who entered it,
with the moment; a repeated ask is 409 `already_registered`, and such a unit can't be
removed (`ErrAutomaticUnit`, 409 `automatic_unit`) since the sync would recreate it. Units
exist from opening on, so the options are locked from then (deleting the distribution,
admin only, is not: it takes its units with it, unless one is paid or collected,
`ErrHasSettledUnits`, 409 `has_settled_units`, until those marks are undone). A price
change reaches the automatic units nobody has touched — not chosen
(`registered_by_id IS NULL`), not paid, not collected — in the same transaction as the
edit (`Repository.Update`), so correcting the price of a distribution already open leaves
none of them on the old one; anything somebody has chosen, paid or collected keeps what it
cost, and extras always do. Going back to `draft` starts it over: every unit is deleted in
that same transaction — everybody's own, the extras and, in one people sign up for, the
sign-ups, with what was chosen on them, which may not fit the options about to be given —
and the automatic ones of an `all_members` distribution are made again, empty, on opening;
a paid or collected one stops it (`ErrHasSettledUnits`) until the marks are undone.
Changing the audience from `all_members` to `signup` (same transaction in
`Repository.Update`) deletes the automatic units nobody has touched — not chosen, not
paid, not collected — since they exist only because everybody was in; what somebody did
choose stays, as that is somebody who asked for it, which is signing up, and so do extras;
a paid or collected untouched one refuses it (`ErrHasSettledUnits`). The edit dialog asks
first. The page confirms before stepping back.

**Distribution articles** (`distribution_articles`, migration `00024`): the other kind of
distribution, for things asked for in any quantity with a cap on how many exist in all (the
Christmas lottery: 200 "papeletas" at 5 €, 1000 "décimos" at 20 €). A distribution with
articles (`Distribution.Articles`, each with `price` and an optional `max_total`) is always
`signup` with no price, extras or options of its own (`ErrArticlesConflict`, checked by
`validateArticleKind` against what the distribution will be once an edit is applied, so a
nil `Articles`/`Options` counts as the current ones); without articles everything above
works as it always did. A person has one `distribution_units` row per article with a
`quantity` (`article_id` set, never `is_extra`, empty `choices`: `CHECK`s of the
migration) instead of one row per unit, so paid/collected marks apply to the whole row;
`price` stays what ONE unit cost, the row costs `price × quantity` (`unitCost` in the
frontend), and `quantity` is 1 outside articles. The own-unit unique index is now
`WHERE NOT is_extra AND article_id IS NULL` — `SyncAutomaticUnits`' `ON CONFLICT` has to
repeat that exact predicate — and articles have their own `(article_id, beneficiary_id)` one.
`Repository.CreateArticleUnit`/`UpdateQuantity` lock the distribution row (`FOR UPDATE`) while
they count what is left of the article (`remainingOf`), so two people asking at once can't
both take the last units (`ErrArticleExhausted`, 409 `article_exhausted`, with how many are
left in the message). Once any unit exists only each article's `max_total` can be edited,
and not below `Article.requested` (`replaceArticles`: `ErrHasUnits`/`ErrCapBelowRequested`);
`unit_count`/`picked_up_count` and the order summary count units (`sum(quantity)`), and
`GET …/order` groups by article. An article's numbers can be *described* (migration `00026`,
`identifier_fields`, `numbering.go`): parts with a spec of values and ranges ("Serie:
124-133, 137", "Número: 0001-0200", zero padding kept) whose every combination is an item
that exists, so what is free, who has what and the next free ones are known. `max_total` is
then always the number of items (`normalizeArticles` overwrites it) and `has_identifiers` is
implied; a unit's number is its parts joined by `/`; typed numbers may use a range in the last
part by position in its values (`numbering.expand`, so gaps are respected) and must exist
(`valid`); `POST …/identifiers/assign` and `assign_numbers` on `POST …/pickups`
(`Service.assignNumbers`) give the next free ones in listing order; `GET
…/articles/{id}/numbers` lists every item with its holder. With units present the numbers can
grow but not leave out one somebody has (`ErrNumberInUse`, in `replaceArticles`). Migration `00024` also has `max_per_person` (checked with
`max_total` in `checkQuantity`, `ErrPersonLimit`, 409 `article_person_limit`) and
`has_identifiers`: such an article's rows carry one number per item in
`distribution_unit_identifiers` (unique per article, `ErrIdentifierTaken`), written with
`PUT …/units/{id}/identifiers` (`Service.SetIdentifiers`, admin/board/material, only once the
distribution is not a draft, at most `quantity`, completed later if need be) and required in full before
collection (`MissingIdentifiers`, `ErrIdentifiersMissing`); with units present, `max_total`,
`max_per_person` and `has_identifiers` are the editable parts of an article. `Article.String` is what the edit notification diffs
(without `Requested`, which moves as people ask). Frontend: one line per article in the
"Artículos" textarea, `Nombre: precio[, máximo N][, N por persona][, con números]` (`parseArticles`), which switches off the
fields that don't apply (`syncArticleFields`); `/reparto` shows a quantity field per article
with what is left, "Tus pedidos" rows are changed by quantity, `/reparto-pedidos` (below) shows
each article against its cap and has every action on the orders, and
`/reparto-numeros` (`distribution-numbers.js`, linked from `/reparto` when some article has
described numbers) lists every number with who has it, filterable, and how many are free. The
numbers are described in the articles textarea with indented lines under the article
(`  Serie: 124-133, 137`).

**Licenses**: a license's type isn't free text — it's a `license_type_id` referencing the
`license_types` catalog, seeded in the database (migration `00007_licenses.sql`) rather
than a Go enum, so the list can grow with an `INSERT` instead of a deploy. Its `is_other`
escape-hatch entry ("Otra") requires an `other_detail` string saying which license it
actually is; every other entry forbids one (shared validation:
`licenses.normalizeOtherDetail`, used by both `Create` and `Update`). The catalog itself
is `GET /license-types` (admin-only, like `POST`: it only feeds the "assign a license"
form). `PATCH`/`DELETE /licenses/{id}` (admin-only) correct or remove an assigned license
without re-checking membership status the way `POST` does — that gate is for assigning a
*new* license, not fixing a typo on one someone already has. `POST`'s own gate
(`memberships.Repository.WasMemberDuringYear`) checks that the person was a socio at some
point of the license's own `Year` — not "right now", and not on `issued_date` either:
assigning, after the fact, a license for a year someone genuinely belonged to the club
must still work even if they've since caused baja, or if it's dated before they joined (or
after they left) that same year; only a year that never overlapped any of their membership
periods is rejected (`ErrNotActiveMember`, HTTP 409 `not_active_member`). A license also
has its own `Year` (migration `00014_membership_dues_and_license_payments.sql`) — the
calendar year it actually covers, *not* always the same as `issued_date`'s own year, since
a license requested in December often covers the *following* year — and an *optional*
`TransactionID` (a `*int64`, null since migration
`00020_licenses_transaction_optional.sql`) naming the transaction that recorded its
payment (see Accounting below): omitting it is the only way to record a license the club
didn't collect — someone federated elsewhere — and then there's no transaction to check;
one that is given must exist (`ErrUnknownTransaction`, 400 `unknown_transaction`, if it
doesn't name a real transaction), and it's never itself editable afterward — `UpdateInput`
deliberately has no `TransactionID` field, since correcting a license's type, date, or
year is a different action from re-litigating which payment it belongs to (the same
distinction `accounting.UpdateAccountInput`'s missing `Position` field draws). That
transaction must also (when given) have at least one entry on an account marked
`allows_licenses` (migration `00017_accounts_dues_licenses_flags.sql`; see Accounting
below): `Service.Create` asks `Repository.TransactionAllowsLicenses` — a SQL check against
accounting's own tables, the same way a license already references its transaction by
foreign key, without importing `internal/accounting` — and answers
`ErrTransactionNotForLicenses`, 409 `transaction_not_for_licenses`, otherwise. It runs
last, after every check that only needs the request itself, so an existing request keeps
the error it always got first.

**Membership dues** (`membership_dues`, alongside `memberships`): every year a socio
remains a member, they owe a cuota to cover it — the first one (alongside the one-time
matrícula) the same day they join, then once more each following year for as long as they
stay a socio. `memberships.Due` is one row per `(user_id, year)` covered (`UNIQUE`,
`ErrDueAlreadyRecorded` on a second one for the same year), always tied to the
`transaction_id` that actually recorded the payment — a due with no transaction behind it
doesn't mean anything yet, so unlike a membership period itself there's no "unpaid" row to
represent. Deliberately keyed by `user_id`, not `membership_id`: a rejoining member's due
for a given year isn't naturally tied to one specific membership period, and this way a
year never needs to be reconciled against period boundaries. `POST /users/{login}/dues`
(admin-only) is `Service.RecordDue`: if the person has *no* membership period on file at
all yet, this is treated as their very first due, so a new membership period is also
started for them (the request's own `start_date` becomes its `StartDate`) — the same alta
a person otherwise gets through `POST /users/{login}/memberships`; someone with a prior,
since-closed membership who's rejoining is left entirely to that existing, separate alta
flow instead (`HasAnyMembership` is already true for them, so nothing extra happens).
The request's `start_date` is therefore optional: it's only required — 400
`start_date_required`, `ErrStartDateRequired` — when there is no membership on file to open,
and ignored otherwise (a malformed one is still a 400 `invalid_date`).
Before any of that, `RecordDue` checks the transaction itself — it has to exist
(`ErrUnknownTransaction`) and have at least one entry on an account marked `allows_dues`
(`ErrTransactionNotForDues`, 409 `transaction_not_for_dues`;
`Repository.TransactionAllowsDues`, the dues counterpart of licenses' check above) — so a
rejected due can never leave a membership period started as a side effect.
`GET /users/{login}/dues` mirrors licenses' own self-or-admin/board read split, and joins
each due's transaction to add `paid_at` (the payment's own `occurred_at` — not
`created_at`, since a past year's due can be registered long after it was paid), only from
this listing, never on `POST`'s response, the same "only from the listing" pattern as
`Account.Balance`. That is what `/socio` and `/perfil` show: `/socio`'s "Historial como
socio" timeline gets one "Cuota anual · <year>" event per due, dated by `paid_at` and with
no Editar/Borrar (there's no endpoint for either) — its date has to keep the same
midnight-UTC string shape every other event's has, since the timeline sorts them as plain
strings and a bare `YYYY-MM-DD` would sort before an alta of the same day instead of tying
with it; on a tie the order is alta, then the cuota, then a license paid the same day
(`TIMELINE_TIE_BREAK_PRIORITY`) — and `/perfil` shows the very same timeline, only
without the buttons: `js/timeline.js` (`loadTimelineData`, `renderTimelineRow`) builds and
renders it for both pages, `member.js` adding its action buttons to each row. That
needed `GET /users/{login}/leaves` to open to the person themselves, like their
memberships, licenses and dues. There's no endpoint to correct or remove a due, and no
validation that a due's `year` actually falls within an active membership period — both
deliberately out of scope for now. `membership_dues.transaction_id` cascades on delete
(`ON DELETE CASCADE`) — a due is pure bookkeeping metadata with no meaning apart from its
transaction — while `licenses.transaction_id` (nullable, see above) uses the default
`ON DELETE RESTRICT` instead: a license is a real, independently meaningful record (proof
of federation) even apart from its payment, so the transaction that paid for one can't be
deleted out from under it at all. `/socio` has a "+ Licencia sin pago" button
(admin-only, in the same panel as the alta/baja actions) opening a dialog that `POST`s a
license with no `transaction_id` — the way to record one the club didn't collect; the ones
it does collect are registered from their transaction, in Contabilidad (`/cuenta`). The
license then shows in the "Historial como socio" like any other, with Editar/Borrar.

**Temporary leaves** (`membership_leaves`): a "baja temporal" belongs to the person
directly (`user_id`), not to one specific `memberships` period — the same `user_id`-keyed
shape `licenses`/`membership_dues` already have (migration
`00016_membership_leaves_by_user.sql`, replacing an earlier `membership_id` foreign key).
That means `GET /users/{login}/leaves` and `memberships.Repository.IsOnLeave` are both a
single lookup, covering every leave this person has ever taken across however many
separate alta/baja spans they've had, with no join through `memberships` needed. A leave
still has to fall entirely within one of that person's own membership periods, though —
that invariant can't be a foreign key anymore, so
`memberships.Repository. findContainingMembership` checks it in Go on every
`CreateLeave`/`UpdateLeave`, by finding which single period (if any) covers the given
dates; there's at most one, since a person's own periods never overlap. Shrinking a
membership period (`PATCH /memberships/{id}`) re-validates every one of that person's
leaves that currently falls within the period *as it stood before* the edit — not every
leave they've ever had — so narrowing one period can't be blocked by a leave that actually
belongs to a different one of their own periods.
`POST /users/{login}/leaves`/`GET .../leaves` are admin-only-to-write,
self-or-admin/board-to-read (like the person's memberships, licenses and dues, so
`/perfil` can show the same history as `/socio`) — `PATCH`/`DELETE /leaves/{id}` stay
addressed by the leave's own id. `DELETE /memberships/{id}` (admin-only) removes a
membership period outright — nothing references one by foreign key, so it never cascades —
meant for the frontend's own "undo my last action" story on `/socio` (see Frontend
below), which only ever offers it for a person's single most recent
alta/baja/baja-temporal entry; that's a UI-level rule, not something the endpoint itself
enforces. `DELETE /board-members/{id}` (admin-only) is the same idea for a board term —
also never cascades, nothing references one by foreign key either — but with no "closing
half" distinction to make: unlike a membership period or a leave, a board term is a single
row covering both its own start and end (see `board.Member`), so deleting it always
removes the whole term, not just clears a field back to open.

**Document library** (`internal/library`): serves club documents (estatutos, reglamentos,
actas...) read-only from a plain directory tree bind-mounted from `./library` on the host
— see Commands above — not the database. `GET /library?path=...` lists a directory
(subdirectories first, then files, both alphabetical; dotfiles never listed);
`GET /library/file/{path...}` streams a file's raw bytes (the path is the rest of the URL,
slashes included — e.g. `/library/file/actas/2026-02-01.pdf` — not a `?path=` query
parameter), inline by default or as an attachment with `?download=1`, with HTTP Range
support via `http.ServeContent`. Both are open to any baseline role and read-only —
nothing here lets a caller upload, rename, or delete anything.

**Per-person files** (`internal/userfiles`): the same read-only, filesystem-backed model
as the document library, one subdirectory per login instead of one shared tree — a scanned
DNI, a signed waiver, medical paperwork. `GET /users/{login}/files[?path=...]` and
`GET /users/{login}/files/file/{path...}` mirror `/library`'s own listing/streaming shape
exactly (including the path-in-URL file route, `?download=1` and Range support), but are
*not* open to every baseline role: only that person themselves, or admin/board, may see
them (403 otherwise) — checked inline in the handler (`Handler.resolveAccess`), the same
self-or-privileged pattern
`memberships.Handler.listByUser`/`inventory.Handler.listMovementsByUser` use, since who
may see a specific person's files isn't a fixed role list the way `/library`'s is. A login
with no subdirectory of its own yet — most people, until someone drops a file in for them
— gets an empty listing at their own root rather than a 404.

**Admin status** (`internal/adminstatus`): `GET /admin/status` (admin-only, read-only)
says how the running process is doing and how it is set up, so nobody has to log into the
server to find out. `cmd/api` hands it an `Info` (the start time, the timeouts, whether
SMTP and Telegram are configured and the bot running, the three storage roots), and on
each request it adds what can change: what Go recorded in the binary
(`debug.ReadBuildInfo`) — the main module's version (`Main.Version`: a tag, or a
pseudo-version with the commit and `+dirty`, when built from a git checkout;
`moduleVersion` turns Go's "(devel)" placeholder into nothing, as for the Docker image,
whose build context has no `.git`; nothing is passed at build time, which is why
`go.mod`/`go.sum` live at the repository root rather than in `backend/`: Go only stamps a
version when the module root is the git root, and the import paths already read
`…/katiuskas-intranet/backend/…`; the Docker builds therefore use the repository root as
their context, trimmed by `.dockerignore`) and the commit —, the Go version, whether the
database answers and how fast (`SELECT 1`) with the latest migration goose applied
(`goose_db_version`, read in its own subtransaction so a missing table can't abort a
caller's transaction; absent when unreadable, as under the tests' own migrator), and
whether each storage directory exists. A database that doesn't answer is reported in the
body, not as an error status. It never includes a secret: no tokens, passwords or
connection strings.

**Telegram** (`internal/telegram`): a minimal client for the Telegram Bot HTTP API
(https://core.telegram.org/bots/api) — plain `net/http`/`encoding/json`, no SDK
dependency, matching the "no muchas dependencias" ask this package was built to satisfy.
Configured via `TELEGRAM_BOT_TOKEN`/`TELEGRAM_LOG_CHAT_ID`; unset, `Sender` falls back to
`NoopSender` (logs instead of sending), the same optional-with-a-console-fallback shape
`internal/mail` already uses for SMTP. Every message is plain text, not JSON, and
carries no timestamp — deliberately readable at a glance on a phone rather than a log
line. `bot.go` adds the general, not-chat-specific pieces this package's own `Sender`/
`BotSender` don't need — `Client.SendMessage`/`SendMessageRequestingContact` (to whichever
chat, not just the fixed log one) and `Client.GetUpdates` (long-polling, see
`internal/telegrambot` below) — kept separate from, and never refactored to share code
with, `BotSender`'s own already-shipped, already-tested HTTP-call logic. Delivering the
existing magic-link/login code by Telegram DM too (see `authapi.Handler.
notifyMagicLinkViaTelegram` below) is as far as this goes — a dedicated Telegram-native
login flow that skips email entirely is still a separate, larger piece of future work,
not built here. Seven things currently talk to Telegram, all independent of each other.
One of them (the bot) also receives; the rest only send. Their sends are fire-and-forget
(their own goroutine, a bounded timeout, a background context detached from the
request/statement that triggered them — so a slow or unreachable Telegram API never adds
latency anywhere), with two deliberate exceptions described below: the startup message and
the send of a bot-flow magic-link DM.
- `LogHandler` wraps the root `slog.Handler` in `cmd/api/main.go` and forwards every
  record at `slog.LevelError` or above (panics, JSON-encode failures — see
  `httpapi.RequestLogger`/`middleware.go`) in addition to (never instead of) logging it
  normally. Each attribute is sent as `key: value`; a multi-line one (a panic's full
  goroutine stack trace, chiefly) is trimmed to its first few lines (`summarize`,
  `maxAttrLines`) — the untrimmed value still reaches stdout, trimming only affects what
  Telegram sees.
- `authapi.Handler.notifyLogin` sends "🔑 Login: `<login>` (`<ip>`)" once a magic-link
  consume (either the link or the code) successfully issues a session — the *only* part
  that's synchronous is resolving the login from the user id, on the request's own
  context, right alongside `TouchLastLogin`; only the actual network call to Telegram is
  deferred to a goroutine (a background goroutine independently touching the database
  isn't safe against a request-scoped `pgx.Tx` — every handler test shares one, see
  `internal/authapi/handler_test.go`'s `capturingNotifier`).
- `authapi.Handler.notifyAccessProblem` reports to that same channel when someone types
  details on `/login` that don't work, so an admin can see it — a typo, or a person who
  should have access: "⚠️ Acceso fallido" for an email and phone nobody has (the typed
  values, trimmed and on one line by `logSafe`, plus the caller's address) or a person with
  no role that gives access (their login), and "⚠️ Código de acceso rechazado" for a code
  that is wrong or expired (the person's login) or typed with details nobody has. The
  caller is told nothing different — the answers stay the same so as not to reveal who has
  access — and a successful request, or one held back by the cooldown, reports nothing.
  `RequestMagicLink` learns which case it was from `sendMagicLinkIfEligible`'s
  `magicLinkOutcome`; the send is fire-and-forget like `notifyLogin`'s.
- `authapi.Handler.notifyMagicLinkViaTelegram` DMs a sign-in link to whoever has linked
  their Telegram account (`users.Repository.TelegramChatIDForUser`). Whether the email
  `issueMagicLink` (the shared core both `sendMagicLinkIfEligible`, for the web endpoint,
  and the exported `IssueMagicLinkForTelegramUser`, for `internal/telegrambot.Bot` above,
  resolve their target person into before calling) also sends depends on which of those
  two callers triggered it (`issueMagicLink`'s own `viaTelegramBot` bool, threaded
  through): riding along a web request that merely happens to also have Telegram linked,
  the DM is sent alongside the email, not instead of it, so someone with no Telegram
  linked (or a failed lookup/send) still has the email to fall back on — but when it's the
  bot's own `IssueMagicLinkForTelegramUser` replying to someone who just typed their email
  into the chat (see `handleRecognizedChat` below), `issueMagicLink` returns right after
  the DM and skips the email outright: that DM already *is* the answer to what they just
  typed, so a follow-up email would only be redundant, not a fallback worth having.
  Wording matches this split: the email, and a web-triggered DM, both include the login
  code, a "recibes este mensaje porque..." line naming the login page as this request's
  origin (the `requestOrigin` constant, since neither of those two is ever built for a
  bot-triggered request), and a "if you didn't request this, ignore it" line — since
  either might be read on a device other than the one signing in, or simply wasn't
  expected — while the bot-flow DM drops all three: no code-entry page is open anywhere
  for that code to be useful for, and asking "was this you?" makes no sense for a message
  that *is* the direct reply to what they themselves just typed, not something that could
  show up unprompted. `IssueMagicLinkForTelegramUser` is also the one caller where email
  genuinely can be absent on file at all — `FindActiveByEmailAndPhone` guarantees one for
  the web path, but resolving straight from a linked chat id doesn't — though that's
  beside the point for the skip above, which applies to every bot-triggered request the
  same way regardless of whether one's on file. Unlike `notifier`/`notifyLogin` above,
  which always target the fixed log channel, this needs `authapi.TelegramMessenger`
  (`*telegram.Client` satisfies it structurally) to DM whichever `chat_id` that specific
  person has — a second dependency alongside `notifier`, wired from the same `botClient`
  `cmd/api/main.go` also gives `internal/telegrambot`, and `nil` (not gated behind a
  separate config check) whenever Telegram isn't configured at all, disabling this DM
  outright rather than falling through to some no-op default. The chat id lookup runs
  synchronously on the request's own context, same reasoning as `notifyLogin`. For a
  web-triggered DM, only the network call itself is deferred to a goroutine,
  fire-and-forget — any failure (the lookup, or the send) is logged and otherwise
  swallowed, since the email is this path's own real fallback regardless of whether the DM
  goes through. A bot-flow DM (`viaTelegramBot`) has no such fallback — `issueMagicLink`
  skips the email entirely for it — so every step, send included, runs synchronously
  instead and reports a failure anywhere along the way as `errMagicLinkTelegramDMFailed`,
  all the way back through `IssueMagicLinkForTelegramUser` to `Bot.handleRecognizedChat`,
  which replies with `magicLinkDeliveryFailedMessage` instead of leaving the person with
  silence (previously this was only ever logged, a genuinely silent dead end for whoever
  just proved their email to the bot). Running synchronously here is no different from
  every other call `Bot.handle`'s own call chain already makes straight to the Telegram
  API (`SendMessage`/`SendMessageRequestingContact`/`DeleteMessage`, all synchronous) —
  `Run` processes one update at a time with no per-update goroutine of its own, so
  blocking briefly on this one call is already this bot's normal way of working, not a
  departure from it. The delete itself is still deferred to its own goroutine, and only
  ever scheduled at all for a bot-flow DM once its (now synchronous) send has actually
  succeeded: sends via `Client.SendMessageForID` (unlike plain `SendMessage`, this hands
  back the sent message's own id), sleeps for `Options.TelegramBotDMLinkTTL` (5 minutes by
  default, `config.Config`'s own hardcoded default — deliberately much shorter than
  `MagicLinkTTL`, the link's real, unaffected expiry: typing an email straight into the
  bot happens live in the chat, so a tight window reads as more trustworthy than the same
  span an email — read who knows when — gets, and this message's own wording states that
  same shorter number, not `MagicLinkTTL`), then calls `Client.DeleteMessage` on it —
  unless the person actually signs in first: `Handler.issueSessionAndRespond` (reached
  from `ConsumeMagicLink` or `ConsumeMagicLinkCode`, whichever channel the link/code that
  succeeded actually came through) calls `Handler.deletePendingTelegramDM(userID)` on
  every successful sign-in, which — only when this same user has a DM still tracked, a
  no-op otherwise — deletes that message right away, cancelling its scheduled sleep
  instead of waiting out the rest of the delay: once someone has actually signed in,
  there's no reason left to leave the link sitting in the chat.
  `Handler.pendingTelegramDMs` (a `map[int64]pendingTelegramDM`, `userID` keyed,
  `pendingTelegramDMsMu`-guarded) is what makes this possible, and it holds one message
  per person, the latest: `scheduleCleanup` registers the chat/message id and a
  `context.CancelFunc` for its own sleep right after sending (`setPendingTelegramDM`),
  which hands back the DM it replaces for `deleteTelegramDMs` to delete at once — only the
  latest link is worth having in the chat. (Two requests in quick succession can have
  their background sends finish out of order; Telegram message ids grow within a chat, so
  the lower id is the one deleted.) Either side — the timer itself, or
  `deletePendingTelegramDM` — removes the entry once it's actually deleted, so the other
  one can't also try to delete an already-gone message. Purely a hygiene nicety either
  way, not a security gate — the link inside keeps working for the rest of the real
  `MagicLinkTTL` regardless of whether either delete path succeeds, or even runs at all (a
  process restart in between just loses it, the same as any other best-effort cleanup in
  this app) — but a dead link lingering in the chat is misleading, and one more thing
  exposed for no reason if the phone/session is compromised later. A web-triggered DM
  (`!viaTelegramBot`) is tracked and cleaned up the same way, once its background send has
  the message id — the only difference is the delay: it states, and waits out, the real
  `MagicLinkTTL` (15 minutes), since its code might still be typed into the login page
  from another device until then. So every link DM is deleted when its link and code stop
  working, or earlier at the person's sign-in (link or code, from whichever device), and a
  newer link DM — from either flow — deletes the previous one.
- `httpapi.NotifyAsync`/`DiffFields` (`internal/httpapi/audit.go`) are the shared pieces
  every resource package's own mutation notifications are built from. `NotifyAsync` (a
  no-op for a `nil` notifier, i.e. Telegram not configured) is the fire-and-forget
  goroutine/timeout dance every Telegram send in this app already used, pulled out once so
  nobody duplicates it. `DiffFields(before, after, fields...)` reflects over a *named*
  subset of a struct's fields — never every field on it, since a derived/computed one (a
  computed role list, a membership date recalculated elsewhere) must never show up as a
  spurious "change" just because it was recomputed at a slightly different moment than
  "before" — comparing the pair and returning one "field: old → new" line per field that
  actually differs (a nil pointer formats as `(vacío)`, a date as a plain `YYYY-MM-DD`),
  *and* renders differently once formatted — a nil pointer and one pointing at a zero
  value (an empty string, a zero `time.Time`) are still genuinely different values, but
  both display as `(vacío)`, so a field that merely swapped between those two produces no
  line: the point of the message is what a human reading it would actually notice changed,
  not every byte that did. Every resource package with a mutating route (`users`, `roles`,
  `tokens`, `memberships`, `licenses`, `activities`, `inventory`, `board`,
  `activityresources`, `distributions`, `accounting`) takes an `httpapi.MutationNotifier`
  in its own `NewHandler` — wired to `telegramSender` directly from `cmd/api/main.go`, not
  gated behind a "don't spam every single request" check the way the predecessor of this
  whole thing needed, since one message per meaningful action isn't noisy the way one per
  every mutating request (a routine token touch included) was — and sends its own specific
  message once a create/update/delete actually succeeds: who did it (`actorLogin`,
  resolved from the session's own id through that package's existing `UserLookup`, falling
  back to `user#<id>` rather than failing the whole request if the lookup itself errors)
  and what changed. A diffing `update` handler fetches the record via `Service.Get`
  *before* applying the patch and diffs it against the result using a package-level list
  of field names (`auditableFields`; `accounting` has `accountAuditableFields` and
  `transactionAuditableFields`; `memberships` names its fields inline) that exactly
  matches that endpoint's own `UpdateInput` — so nothing that endpoint can't actually
  touch can appear as a spurious change; `roles` and `inventory` have no diffed update at
  all, and `tokens` reports an expiry change by hand. An edit whose diff comes back empty
  (every field resent unchanged) sends nothing, rather than an "edited" message with
  nothing in it. A `delete` handler fetches the target the same way, purely so it can
  still be named once it's gone. This whole mechanism replaced an earlier
  `httpapi.NotifyMutatingCalls` middleware that sent a blanket "[API] `<method>` `<path>`
  -> `<status>`" for literally every write regardless of what it actually changed — every
  business-data route it covered now has a strictly more informative replacement.
  `authapi`'s session-lifecycle routes (magic-link request, logout, impersonate/exit) were
  never business-data mutations and send nothing now; only a successful sign-in
  (`notifyLogin` above), a failed attempt (`notifyAccessProblem` above) and the start of
  an impersonation (`tokens`: "🎭 ... ha impersonado a ...") are reported.
- `internal/telegrambot.Bot`, the one thing here that *receives* from Telegram rather than
  only sending: `Run` long-polls `Client.GetUpdates` (chosen over a webhook — no public
  HTTPS endpoint to expose or coordinate with a reverse proxy, works the same behind NAT
  or with no domain at all) in a loop started with `go bot.Run(ctx)` from
  `cmd/api/main.go`, gated on the same `telegramConfigured` flag (both
  `TELEGRAM_BOT_TOKEN`/`TELEGRAM_LOG_CHAT_ID` set) that gates the `botClient` shared with
  `authapi`'s DM above — every linking attempt has to reach the log channel, so a bot with
  no channel configured can't do its job anyway. A failed `GetUpdates` call (Telegram's
  own long-poll connection dropping or timing out for a moment — near-daily in practice,
  almost always resolved by the very next attempt) is logged at `WARN`, below
  `telegram.LogHandler`'s own forwarding threshold (`slog.LevelError`, see the first
  bullet) — only once they have gone on without a single success for
  `getUpdatesFailureLogAfter` (5 minutes: a duration and not a count of failures, since
  one can take milliseconds or the client's whole 40 s timeout, and Telegram has its own
  bad minutes — rate limiting, a Bad Gateway, requests left hanging, every night around
  3:10 — that are nothing to act on) does `Run` escalate to `ERROR` (`getUpdatesFailures`),
  and only for that one call, not for every one of however many more follow — a longer
  real outage doesn't re-notify the log channel on every 5-second retry for as long as it
  lasts.
  `users.Repository.UserIDForTelegramChatID` tells apart a chat this doesn't recognize yet
  — every message from one gets a greeting plus a "share your contact" keyboard button
  (`Client.SendMessageRequestingContact`, sent with `is_persistent: true` so the client
  shows that keyboard open by default instead of collapsed behind its own keyboard-icon
  toggle; the greeting text also spells out where that button is and how to reveal it if a
  client doesn't honor the flag, since nothing about a Telegram custom keyboard is
  otherwise self-explanatory to someone who's never seen one). An already-linked chat no
  longer gets the contact-share menu again, but a message from one doesn't *instantly*
  trigger a sign-in link either — that was tried and reverted as a security regression: it
  reduced logging in via Telegram to merely having the phone/ session (whoever's holding
  an already-open Telegram app), dropping the "must know *both* phone and email"
  requirement the web flow enforces. Being linked doesn't imply being eligible to sign in
  at all, either — `canUseTelegramLogin` gates both this and the link confirmation below
  on the same baseline-role check every route in this API enforces
  (`auth.HasBaselineRole`, in practice an active socio via the automatic "member" role, or
  admin/board) plus having both an email and a phone on file: the web magic-link request
  already requires both as input just to find a match (`FindActiveByEmailAndPhone`), but a
  Telegram-linked account is resolved straight from its chat id, so nothing there
  guarantees either is actually on file the way `issueMagicLink`'s own tolerance for a
  missing email (see the magic-link DM bullet above) might suggest. Someone who fails this
  check never gets asked for their email — every message from them instead gets
  `unauthorizedMessage` ("Este bot está disponible únicamente para usuarios autorizados.
  Si crees que deberías tener acceso, ponte en contacto con el club para comprobar tu
  situación"). Otherwise, `Bot.handleRecognizedChat` asks a first factor be proven a
  second time: nothing happens until a message is the account's own registered email
  (`emailMatchesUser`, checked case-insensitively against the same space-separated
  multi-email convention `FindActiveByEmailAndPhone` uses). Any other message gets a
  specific, actionable reply from `emailAttemptReply` saying *why* it didn't match
  (`emailPrompt`, "Si quieres entrar en la Intranet de Katiuskas, envíame tu dirección de
  correo electrónico", follows the link confirmation, see below, and is the reply to a
  failed attempt only from an idle chat, see below): `nonTextEmailReply` for an attachment
  instead of text (an empty `Text` always means something that isn't text — Telegram never
  sends a real text message with an empty body), `onlyEmailReply` for more than one word,
  `notAnEmailReplyFormat` for a single word with no "@" at all (repeating the word back),
  and `unknownEmailReplyFormat` for something shaped like an email that just isn't one of
  this account's own (repeating the address back, plus a "contact the club if you think
  this is wrong" suggestion) — only a genuine single-word match skips all of these and
  proceeds. Those replies read as a reproach to someone who hasn't written for a long time
  and most likely doesn't remember that their email is what the bot expects, so a chat
  whose previous message was more than `idleBeforeEmailPrompt` (30 minutes) ago gets plain
  `emailPrompt` instead of any of them. `Bot.noteMessage` records when each chat last
  wrote (`lastMessageAt`, in memory, guarded by `mu`, with `now` as an injectable clock; a
  chat the bot hasn't heard from since it started counts as idle, which is the friendlier
  side) and `handle` passes the answer down to `handleRecognizedChat`; every message
  counts as activity, so the reply after the prompt is back to specific. Once it matches,
  it calls `Client.DeleteMessage` — Telegram lets a bot delete a message it received in a
  private chat — to scrub that email out of the chat history for whoever might get hold of
  the phone/session afterward (best-effort: a failed delete, e.g. an old message past
  Telegram's 48h window, still lets `magicLinkIssuer.IssueMagicLinkForTelegramUser` go
  through), then finally issues the fresh sign-in link exactly as if they'd requested one
  from the web (see `authapi.Handler` above). `magicLinkIssuer` is a local interface
  (`*authapi.Handler` satisfies it structurally, the same pattern as `botClient`) wired in
  from `cmd/api/main.go`, which is also where `authHandler` is hoisted into its own
  variable instead of being built inline in `httpapi.Routes`, purely so both the router
  and the bot can share the one instance. A shared contact's phone number is matched
  against `users.phone` via `users.Repository.FindByPhoneNumber` — not quite the
  exact-digits match `FindActiveByEmailAndPhone` uses for magic-link requests, because
  Telegram always includes the country code (`"34611222333"`) while a phone on file here
  never does (see `CreateInput`/every `SEED_ADMIN_PHONE` default), so digits must equal a
  candidate exactly *or* equal Spain's `"34"` prepended to one. Deliberately not a
  same-length-agnostic suffix match: that also matched a French `"+33666777888"` against a
  Spanish `"666777888"` on file (every digit but the country code is shared), letting
  anyone claim to be whoever's number happened to end the same way. Before any of that,
  `isOwnContact` checks the shared contact is actually the sender's own — Telegram lets a
  message carry *any* contact card, not just the sender's (a manually typed vCard, or
  someone else's saved contact), and only the "share contact" button this bot itself sends
  guarantees `Contact.UserID` equals the sender's own `From.ID`. Skipping this check was a
  real vulnerability, found and fixed after shipping: sharing a real socio's phone number
  in someone else's name linked *that socio's* account to the *sender's* chat, handing the
  sender that socio's magic-link/login code from then on — an impersonation path, not just
  a data-quality one, the same shape as the `FindByPhoneNumber` country-code issue above.
  A mismatch gets a rejection reply and its own `Bot.notifyChannelRejectedForeignContact`
  warning (worth flagging above a routine no-match, since it's a possible impersonation
  attempt) and goes no further — the "share contact" keyboard stays up so they can retry
  with their real one. A match calls `users.Repository.SetTelegramChatID` and confirms via
  `Bot.replyToLinkedContact`, which checks the same `canUseTelegramLogin` eligibility gate
  right away rather than waiting for a later message to find out (`emailPrompt` follows
  the confirmation as a second message for someone actually eligible; someone who isn't
  gets a single combined reply instead — `unauthorizedAfterLinkReplyFormat`, "¡Gracias! Te
  hemos identificado como Nombre Apellidos. Sin embargo, este bot está disponible
  únicamente para usuarios autorizados. ..." — rather than "you're linked" followed by
  "but you can't use this" as two separate messages with nothing useful in between); no
  match replies saying so, plus `contactClubHint` ("Si crees que deberías tener acceso,
  ponte en contacto con el club para comprobar tu situación" — the same suggestion
  `unauthorizedMessage` ends with) since a phone genuinely not on file is exactly the kind
  of thing worth a human checking. Both the match and no-match replies use
  `Client.SendMessageRemovingKeyboard` instead of a plain `SendMessage`, since it really
  was their own contact either way and tapping the button again could only ever resend
  that same phone number for the same answer — unlike the rejection case above, where the
  button staying up is the point. Either way, `Bot.notifyChannel` reports the attempt to
  the log channel — who sent it, the phone number, and whether it matched — telling the
  club about every attempt, not just successful ones. `users.User.TelegramChatID` is
  deliberately left out of `userSelect`/`scanUser` (the query every other `users` method
  uses) since nothing else needs it yet — checking or reading it always goes through
  `UserIDForTelegramChatID`/`FindByPhoneNumber` directly, never through a generic `Get`. A
  link made this way is only as trustworthy as the phone number it was matched against, so
  `users.Repository.Update` clears `telegram_chat_id` back to `NULL` whenever a `PATCH`
  actually changes `phone` (including clearing it to nothing) — otherwise a chat linked to
  an old number would keep resolving to the person who used to have it. Sending the same
  phone back, or a `PATCH` that doesn't touch `phone` at all, leaves an existing link
  alone (`stringPtrsEqual`, treating two `nil`s as equal); only an actual change to the
  value clears it. The link is also forgotten when the bot turns out to be unable to talk
  to the person: `telegram.Client` returns Telegram's `ok=false` answers as an
  `*telegram.APIError` (its `ErrorCode`, the `error_code` Telegram sends, and its
  `Description`), and `telegram.IsUnreachable` is true for any 403 Forbidden — every way
  of not being able to talk to someone (blocked the bot, closed or deactivated their
  account) comes with one — or when the description contains "chat not found" or "user not
  found" (a 400 for a chat that no longer exists). Then `authapi`'s DM send
  (`notifyMagicLinkViaTelegram`, either flow) calls `users.Service.ClearTelegramChatID`,
  since keeping the id would only fail again at every sign-in. Any other failure (a
  timeout, a rate limit) leaves the link alone. The send of a web request's DM runs in its
  own goroutine, tracked so `Handler.WaitForBackgroundSends` can wait for it and for that
  unlinking (tests use it: the shared transaction isn't for concurrent use). The bot's own
  replies in a chat aren't covered — only the sign-in DMs.
- `cmd/api/main.go` sends "🚀 API started" once, right before `http.ListenAndServe` — with
  the version and the date of the last commit when known ("🚀 API started (version v0.3.6,
  commit del 17-10-2026)", or just one of the two, or neither: `startupMessage`, from
  `adminstatus.ReadBuild`, the same build info `/admin` shows) —
  the one notification that isn't fire-and-forget: it's a single call at process
  startup, not something on any request or statement's critical path, so blocking on it
  (bounded to 10s, same timeout every other notification uses for its own goroutine)
  costs nothing a user could ever notice. Still unconditional rather than gated like the
  bot above (which genuinely can't do anything useful without a channel to report to) —
  a restart is rare enough that falling through to `NoopSender`'s own info log when
  Telegram isn't configured is no noisier than logging it directly would've been anyway.

**Accounting** (`internal/accounting`): the club's own money — a chart of accounts
(`accounts`: name, an optional but unique-when-present `code` (migration
`00013_accounts_code_optional.sql`; a real ledger account tends to have one, e.g. "570"
for "Caja", but a purely organizational grouping node might never get one — `""`/blank is
normalized to no code at all, `Handler.normalizeCode`, the same "not itself a storable
value" treatment `licenses.normalizeOtherDetail` gives an empty `other_detail`, so two
codeless accounts never collide under the column's own `UNIQUE` constraint the way two
literal `""`s would), an optional `parent_id` nesting it under a broader one — the
frontend builds any tree view it wants client-side from that — and two plain booleans,
`allows_dues` and `allows_licenses` (migration `00017_accounts_dues_licenses_flags.sql`,
both `false` until an admin or the "money" role marks them): which accounts a transaction
has to have an entry on to be allowed to record membership dues or federative licenses,
enforced by `memberships.Service.RecordDue` and `licenses.Service.Create` (see
**Membership dues**/**Licenses** above) and always present in every `Account` JSON,
`false` included, unlike the omitted-when-absent `code`/`parent_id`; like every other
field, `UpdateAccountInput` replaces them wholesale, so a `PATCH` that leaves one out
turns it off) and `hidden` (migration `00019_accounts_hidden.sql`, same
always-present/replaced-wholesale shape): an account that's no longer used. An account may also have
a `type` (migration `00031_accounts_type.sql`, the `account_type` enum: `asset` — its balance is money
the club really has —, `income`, `expense`; optional and left out of the JSON when absent like `code`,
a blank one normalized to none by `normalizeType`, `PATCH` replacing it, an unknown one 400
`invalid_type`): it is shown on `/cuenta`'s header (`accountTypes` in
`accountTree.js`), chosen in the create/edit dialogs, and the account a distribution's
payment is made in has to be an `asset`. `distribution_default` (migration `00032`, always present in
the JSON, replaced wholesale by `PATCH`, with a "Repartos" badge and a checkbox in the dialogs) marks
the account distributions record their payments in when they have none of their own. One with
entries can't be deleted — they're history — so it's flagged instead, and *only the
frontend acts on it*: the API lists, reads, edits and posts to a hidden account like any
other. `DELETE /accounts/{id}` (admin/money) exists only for an account nothing references
yet — one with a child account or a posted entry answers 409 `account_in_use`
(`ErrAccountInUse`, from the foreign-key violation). Likewise `DELETE /transactions/{id}`
answers 409 `transaction_has_licenses` (`ErrTransactionHasLicenses`) while a license still
names the transaction as its payment (`licenses.transaction_id` is `ON DELETE RESTRICT`);
its dues, by contrast, cascade. Against that chart sit
`transactions`: every real movement of money the club records, each
made of two or more `entries` (an `account_id` and an `amount`, positive into that account
or negative out of it) whose amounts always sum to exactly zero — standard double-entry
bookkeeping, so a transaction always says both where money came from and where it went,
never just one side of it. Each account also appears at most once per transaction
(`transaction_entries_account_once`, a `UNIQUE (transaction_id, account_id)` from
migration `00018`; `validateEntries` checks it in Go before touching the database,
alongside the two-entry minimum and non-zero amounts, `ErrDuplicateAccount`, 400
`duplicate_account`) — its one entry's amount already says everything that moved through
it, and the ledger's per-account running balance and "other accounts" detail both assume
it. `GET /accounts` also joins in each account's own direct `balance` (summed from every
entry ever posted straight to it) and `last_transaction_at` (the most recent one's
`occurred_at`) — never rolled up from a child account, and never present on any other
`accounts` route (`GET`/`POST`/`PATCH /accounts/{id}` are all just the row itself, no
extra join); a `LEFT JOIN` against `transaction_entries`/`transactions` means an account
with no entries at all still gets one row back rather than being silently missing,
`COALESCE`d to a zero `balance` (a real, computed zero — never absent the way it is
everywhere else `Account` shows up) while `last_transaction_at` is simply `NULL`. "At
least two entries" and "sums to zero" both depend on every sibling row, not just one, so
neither can be a plain `CHECK` — both are enforced instead by
`transaction_entries_balance`, a `DEFERRABLE INITIALLY DEFERRED` constraint trigger
(migration `00012_accounting.sql`) that only actually runs at commit, after every entry a
write touched is in place, rather than rejecting a transaction's entries one at a time as
`accounting.Repository.CreateTransaction`/`UpdateTransaction` insert them individually
inside their own DB transaction (the same `pool.Begin` pattern
`activities.Repository.Create` already uses for an activity's initial participants). The
trigger explicitly skips its own check once the `transactions` row itself is also gone
(checked first, before counting/summing entries) — deleting a whole transaction cascades
to every one of its entries, each of which fires this same trigger, and without that guard
the last entry to go would look like "a transaction with zero (too few) entries" instead
of a transaction that's correctly, completely gone.
`Repository.CreateTransaction`/`UpdateTransaction` don't just rely on the deferred
default, though: right after inserting entries they run
`SET CONSTRAINTS transaction_entries_balance IMMEDIATE` on their own DB transaction to
force the check right then (mapping a violation to `ErrUnbalanced` from that call instead
of a later `Commit`), then set it back to `DEFERRED` before returning — necessary because
`pool.Begin` on top of an already-open `pgx.Tx` (exactly what every test does, via
`testutil.NewTx`) opens a *savepoint*, not a real nested transaction, and Postgres only
ever fires a deferred trigger at a real `COMMIT`, never at a savepoint's own `RELEASE` —
so without forcing it immediate, the check would silently never run at all under test, and
resetting back to `DEFERRED` afterward matters too, since `SET CONSTRAINTS`'s effect
otherwise outlives the savepoint that set it (unlike a `ROLLBACK`, a successful
`RELEASE SAVEPOINT` doesn't undo it), which would make the *next* multi-row entry insert
anywhere else in the same enclosing transaction fail spuriously, checked one row too
early. `UpdateTransaction` corrects a transaction by replacing its description,
`occurred_at`, and its *entire* set of entries at once (delete every existing one, insert
the given set) rather than editing a single entry in place — there's no single-entry edit
endpoint. `occurred_at` is a real timestamp, not just a date the way
`activities.start_date`/`licenses.issued_date` are, so it decodes straight into a Go
`time.Time` (RFC 3339, `encoding/json`'s own default) rather than needing the manual
`time.Parse` every plain-date field elsewhere needs. Reading (`GET /accounts`,
`GET /transactions`, and their `/{id}` forms) is open to any baseline role — the club's
own finances are something every socio can see, not just admin/board — with one addition:
for admin/board only, `GET /transactions/{id}` also carries `dues` and `licenses`, the
cuotas and licenses that transaction paid for (always present for them, `[]` when none;
absent for anyone else, since who paid which is private to that person, admin and board —
see `GET /users/{login}/dues`). `Handler.getTransaction` fills them from
`DuesLookup`/`LicensesLookup`, two narrow interfaces `*memberships.Service`
(`ListDuesForTransaction`) and `*licenses.Service` (`ListByTransaction`) satisfy
structurally — the same pattern as `UserLookup` — rather than this package joining their
tables itself; the wrapper type `transactionWithPayments` exists only for that one
response, never for create/update/list. Writing needs admin or the optional, non-system
"money" role (seeded by migration `00012_accounting.sql`, deletable by a club that doesn't
want the distinction), the same "admin plus one optional custom role" shape `material`
already has for inventory movements — board itself gets no special write access here, same
as it has none for licenses or a person's own data. Every write still goes through the
same baseline-role floor as every other route: holding only `money` with none of
admin/board/member is rejected everywhere, this included. Every account also has a
`position` (migration `00012_accounting.sql`) — its manual display order among its own
siblings (same `parent_id`, `NULL` counted together as one top-level group), not unique or
gap-free on purpose, since it's only ever used to `ORDER BY`, never to identify a row.
`CreateAccount` appends a new account after its own sibling group's current highest
position, the same "`MAX(position) + 1` in the same `INSERT`'s own subquery" pattern
`activityresources.Repository.Create` uses for a resource's position within its activity.
The only way to actually change one afterward is `POST /accounts/{id}/move`,
admin/money-gated same as every other accounting write: `{"direction": "up"}` swaps it
with the sibling immediately before it, `"down"` the one immediately after — a harmless
no-op, not an error, if there's no such sibling (already first/last within its own group)
— and answers with the whole, freshly reordered `GET /accounts` list (same "return the
full list after reordering" shape the memoria's own reorder endpoint uses) rather than
just the moved account, so a caller can re-render straight from the response.
`GET /accounts` itself walks the tree in pre-order, depth-first: every top-level account
in its own `position` order, each immediately followed by all of its own descendants
(recursively, each in their own `position` order, however deep) before the next top-level
sibling — `Repository.ListAccounts`'s own `tree` CTE computes this via the standard
"materialized path" trick (an array of every `position` from a real root down to each
account; Postgres compares arrays element-by-element, so ordering by it reads out exactly
that walk) rather than assembling the tree in Go. Anchored only at genuine roots
(`parent_id IS NULL`), that walk can never loop forever even against a cycle somehow
already among non-root accounts — nothing in such a cycle has `parent_id IS NULL`, so
nothing in it could ever seed the walk to begin with; it would instead just silently
vanish from the listing, which is exactly why `UpdateAccount` rejects
(`ErrCircularParent`, 409 `circular_parent`) setting a `parent_id` that is, or descends
from, the account being edited — checked by walking *up* from the proposed parent (a query
genuinely capable of looping forever against pre-existing bad data, unlike the listing's
own downward walk), so that one's own recursion is capped at a depth no real chart of
accounts would ever approach, purely so it can't hang even then. The hierarchy only groups: an
account has child accounts or has transactions, never both (migration `00033`, two triggers so that
no path skips it, the transactions of a distribution's payments included): an entry on an account
with children is 409 `account_has_children` (`ErrAccountHasChildren`, on creating or correcting a
transaction), and giving as parent — creating under it or moving to it — an account that already
has entries is 409 `parent_has_entries` (`ErrParentHasEntries`); the triggers say which in the
error's `ConstraintName`. The frontend leaves out of the parent pickers the accounts that have
transactions (`last_transaction_at`) and shows the ones with children disabled in the "otra cuenta"
picker, hides "+ Nueva transacción" on a group's page and leaves them out of the distribution and
payment pickers.
`GET /accounts/{id}/transactions` is one account's own ledger — the `AccountEntry` rows it
was posted against, a page at a time (not the same shape as a bare `Entry`: each one
inlines its own transaction's `description`/`occurred_at`, and a `balance_after` instead
of just an `amount`), newest first same as `GET /transactions` itself.
`Repository.ListAccountEntries` computes that running balance oldest-first (a running
balance only ever makes sense walked forward through time) and serves the page
newest-first, the opposite order from how it accumulates. Both this and
`GET /transactions` take the same `limit`/`offset` (default 50, at most 100) and optional
`from`/`to` (inclusive `YYYY-MM-DD` days; `parseListFilter`, a bad one is 400
`invalid_date`/`invalid_date_range`) — `ListFilter` — since a few years of ledger is
thousands of rows. A `q` of words keeps only the transactions whose description has every one,
in any order and ignoring case and accents (`ListFilter.Words`, `descriptionHasWords`/
`likePatterns`: `translate()` of the Spanish accented letters on both sides and `LIKE`, with
`%`, `_` and `\` escaped, so it needs no `unaccent` extension); on the ledger it is applied
after the running balance, which doesn't change with it. The ledger's `balance_after` is a window `SUM` over the account's
*whole* history, computed in a subquery before the range and page are applied, so it never
restarts from zero at the edge of a window. Open to any baseline role, same as every other
read in this package; `Handler.listAccountEntries` checks the account actually exists
first (404, not just an empty list) since `ListAccountEntries` alone can't tell "no such
account" apart from "a real account nobody's ever posted against."

**Testing helpers**: `internal/testutil` provides `NewTx` (migrated, isolated
transaction), `InsertUser`/`InsertActivity`/`RoleID`/`LicenseTypeID` fixtures (the
`TestUser` `InsertUser` returns carries a `Login` too, for tests exercising a
login-addressed route), `InsertTransaction`/`InsertTransactionOn` (a real, balanced
two-entry transaction for tests that record a due or a license against one:
`InsertTransaction` posts it to an account that allows both, `InsertTransactionOn(t, tx,
allowsDues, allowsLicenses)` picks the flags, for the cases that must be rejected), and
`InsertTransactionAt(t, tx, occurredAt)` (the same as `InsertTransaction` with a chosen
`occurred_at`, for tests that care when a payment happened), `MaterialCategoryID` (looks
up a seeded inventory category by name, like `RoleID`/`LicenseTypeID`), `UniqueEmail` (a
per-test, per-call unique address, which `InsertUser` itself uses), and
`FakeResolver` + `NewTestAuthenticator`/`TokenFor` — an in-memory `auth.SessionResolver`
so handler tests in every *other* package can mint bearer tokens for arbitrary
users/roles without hitting the real `tokens` table (only the `auth` and `tokens`
packages' own tests exercise real session mechanics; `auth`'s internal tests use a local
stub instead of `testutil`, since `testutil` imports `auth` and an internal test file
can't import back). `internal/library` (and `internal/userfiles`, structured the same way)
needs none of the Postgres fixtures — each package's own `service_test.go` builds a
`Service` directly on a `t.TempDir()` tree, no Postgres involved at all; only their
`handler_test.go`s reach for `FakeResolver`/`TokenFor` to mint bearer tokens for the
role/self checks their routes enforce.

**Frontend** (`web/`): classic multi-page site (no SPA, no build step — a deliberate
choice, not just an unexamined default: every page already revalidates its own session and
re-fetches its own data on load, so there's no cross-page state an SPA would actually
save, and this stays free of a bundler or any client-side router), one `<page>.html` +
`js/<page>.js` per view, Caddy's `try_files {path} {path}.html` making `/socios` resolve
to `socios.html`. The sidebar and topbar, identical on every authenticated page, are
rendered once from `js/layout.js`'s `initPage()` rather than copy-pasted into each
`<page>.html` — every one of them reduces to an empty `<aside id="sidebar">`/
`<header id="topbar">` pair for it to fill in. `initPage()` is also each page's own
session bootstrap (in place of what used to be five near-identical lines re-typed at the
top of every `<page>.js`): it validates the session, applies role-based visibility, and
wires the sidebar toggle, topbar greeting, and logout button, returning the signed-in user
same as `currentUser()` would. The menu collapses below 800px (an overlay behind the hamburger)
and, on a wide screen, can be hidden with the "×" at the top of it: `api.js`'s
`initSidebarToggle` puts `.sidebar-hidden` on `.shell` — which looks like the narrow layout,
`style.css` keeps the two rules alike — and remembers it in `localStorage`
(`katiuskas:sidebar-hidden`, applied as `api.js` loads so it doesn't flash); there the hamburger
brings the normal layout back instead of overlaying the menu (`NARROW_SCREEN` there has to match
the media query). Its own `requireAnyRole` option additionally redirects home
if the signed-in user holds none of the listed roles — the whole-page gates of `member.js`
(admin/board), of `accounting.js`/`account.js` (admin/money) and of `admin.js` (admin),
tighter than the ordinary
baseline check every API route already enforces. Which sidebar link reads as "active" is
computed from `location.pathname` (`ACTIVE_ALIASES` mapping a detail page like `/actividad`
or `/socio` to the listing it was reached from, `/actividades`/`/socios`) rather than a
per-page `class="active"` written by hand — one less thing a new page could forget.
`login.html` is the one page with no shell and no session yet, and never calls
`initPage()`. `404.html` (`js/notfound.js`) is what Caddy serves, with a 404 status and
without changing the address, for anything that isn't a page (`handle_errors` in
`deploy/Caddyfile`, which is why `root` is declared at the site level there — an error
handler can't see one set inside the `handle`; API responses are untouched). It can't assume
who is looking: with a session token it becomes an ordinary page (`initPage()`, menu
included), imported only then because `api.js` reads the token as soon as it loads; without
one it shows a plain panel linking to `/login`. Everything it loads is by absolute path,
since it is served at whatever address was asked for, however deep — which is also why
`layout.js`'s topbar logo is `/images/…` and not relative.

Every "create X"/"edit X" form is a `<dialog class="panel modal">` (`.panel` already gives
it the right background/border/padding/spacing — `dialog.modal`'s own `css/style.css`
rules only add what a plain `.panel` never needed: the dimmed `::backdrop`, a max-width,
and the "×" `.modal-close` button's own positioning) — never a `<div>` toggled to
`hidden="false"` in place on the same page the way every one of these used to work.
`js/modal.js`'s `initPage`-sized `initModal(dialog, form?)` is the one call each page
makes per dialog: it wires every `[data-modal-close]` element inside (the "×", a form's
own "Cancelar") and a click that lands on the dialog element's own box rather than
bubbling from its content (the standard way to detect a backdrop click on a native
`<dialog>`) to `dialog.close()`, and — when a `form` is given — resets it on the dialog's
own `close` event, which fires no matter *how* it closed (×, Cancelar, backdrop, Escape,
or a successful save that calls `close()` itself once done), so a caller's own submit
handler never resets the form itself. Opening one is always page-specific and left to the
caller, through `openModal(dialog)` (same file: `showModal()` plus restoring the page's
scroll position, which browsers otherwise reset to the top): a static "+ Nueva X" button
calling it directly, or — for a dialog shared by every row of a list rather than one
pre-rendered per row (`accounting.js`'s own account edit, `account.js`'s transaction edit,
three of `member.js`'s timeline dialogs) — an `open*Dialog(record)` function that fills
the shared dialog's fields from that one record first (`member.js`'s are
`openStatusDialog`, `openEditBoardDialog` and `openEditLicenseDialog`). The platform
itself is what actually makes the rest of the page unreachable while one is open (real
modality, not a hand-rolled overlay to keep in sync) and closes it on Escape — nothing in
`modal.js` or any page's own script needs to implement either. The same goes for errors:
Errors don't go to a banner: `showError` (`api.js`) opens a modal `<dialog id="error-dialog">`,
created on demand, titled "Ha ocurrido un error", with the message and a "Cerrar" button. It is in
the top layer over everything, even another open dialog, behind a dark backdrop, so the rest of the
page can't be operated until it is closed (the button, Escape or a click on the backdrop); a second
error while it's open replaces the message. `showFeedback` is still the sticky `#feedback` banner
for successes — pages no longer have an `#error` element. `login.html`'s own inline messages next to
the button they answer are the exception. `perfil.html`'s own "Mis datos" and `member.js`'s
own "Datos" are both left as a plain inline form instead, not a dialog: a person's own
core data is prominent enough on both pages (the entire reason `/perfil` exists at all;
the very first thing an admin or board looks at on `/socio`) that hiding it behind an
"Editar" button added friction for no real gain in clarity — plain `<input>`s directly on
the page double as the read-only view too, so there's no separate view-only rendering to
keep in sync with whatever the form itself holds. Populated once by
`loadProfile()`/`loadMember()` and never reset to blank the way a "create new X" dialog's
own form is on close, both add a "Cancelar" button next to "Guardar cambios" that puts
back exactly what was last loaded (a plain local re-populate, not a re-fetch) — the one
bit of a modal's own behavior (`initModal`'s reset-on-close) an inline form doesn't get
for free, since there's no `close` event here to hang it from. `member.js`'s own version
disables every field instead (not just the identity ones `profile.js` restricts) for
anyone who isn't a real admin — board can still see a person's full data this way, just
not submit any change to it, the same read-only-but-visible treatment `profile.js` already
gives a plain member's own name/DNI/birth date/gender.

`js/api.js` is the underlying shared client `layout.js` itself is built on: it keeps the
session token in `localStorage`, and `initSession()` calls `GET /auth/me` on every page
load to validate the token and populate `currentUser()`. `applyRoleVisibility()` removes
any `data-roles="admin,board"` element the current user's roles don't satisfy —
client-side UI hiding only; the API is the actual authority. A permission that depends on
data loaded asynchronously for one specific record (e.g. "is the viewer this activity's
organizer?") can't be a static `data-roles` attribute, since `applyRoleVisibility()` only
runs once at page load — that case computes a plain JS boolean after the data arrives and
toggles `.hidden` directly instead (see `activity.js`'s
`canManage`/`canJoinSelf`/`canAddOthers`). Detail pages read a login from the query string
(`/socio?login=<login>`, `new URLSearchParams(window.location.search)`), same pattern
`login.html` uses for the magic-link token from an emailed URL. Its login code has no URL
of its own to arrive in — `login.html`'s second panel gets it typed in by hand — so the
email/phone from the first step are instead remembered in a plain JS variable across that
two-step flow. That second panel appears whatever was typed, since the API answers the
same way for details it doesn't know (so as not to reveal who has access), and so it never
claims anything was sent: it says an email with a link and a code *will* arrive if the
details are right. The link from that email is usually opened in another window or tab,
which signs in there while this one keeps asking: `auth.js`'s `watchForSignInElsewhere`
therefore listens for the `storage` event on the `token` key (and for the tab becoming
visible again, for a browser that doesn't deliver it) and, when a stored token really is
valid (`GET /auth/me`, as at load), sends this window to `/` already signed in — a stale
or invalid one changes nothing. `login.html` also offers Telegram as a second way in,
alongside (never instead of) the email/phone form: on load, `auth.js`'s
`loadTelegramBotInfo` calls the unauthenticated `GET /auth/telegram-bot`
(`authapi.Handler.TelegramBotInfo`, backed by
`config.TelegramBotUsername`/`TELEGRAM_BOT_USERNAME` — independent of
`TELEGRAM_BOT_TOKEN`/`TELEGRAM_LOG_CHAT_ID`, and not secret the way that token is, since
it's only the public "@name" a `t.me/<username>` link needs) and, only if a username comes
back non-empty, un-hides a note linking straight to the bot's chat — left hidden by
default so there's no dead link (or flash of one) when Telegram isn't configured at all,
or the request itself fails.

`library.js` itself doesn't lean on the session cookie described above (`dashboard.js`'s
own simpler Documentos/Actas/Memorias links do instead) — downloading or previewing a
document from the `/archivo` browsing UI fetches the file with `apiFetchBlob` (`api.js`)
and works with the resulting `Blob` client-side — an object URL for inline preview, a
synthetic `<a download>` click to save — since a `Blob` is what lets it render Markdown
inline and drive the download from a click handler rather than a bare link, not because
the route itself needs the bearer header specifically. Markdown
documents render through a small dependency-free converter (`js/markdown.js`) that
resolves each document-relative link against the document's own directory and rewrites it
into an in-app `/archivo` navigation link; HTML documents render in a fully sandboxed
`<iframe>` instead, with no such link rewriting. `library.js` and `userFiles.js` (the
embedded per-person file browser on `/perfil`/`/socio`) share this same
listing/preview/download logic almost verbatim — `js/fileBrowser.js` pulls out the
common pieces (`joinPath`/`dirOf`/`nameOf`, `formatSize`, `fileKind`, the same
document-relative link resolver, `downloadBlob`) both import, rather than each carrying
its own near-identical copy; `activity.js`'s own memoria-resource downloads import just
`formatSize`/`downloadBlob` from there too, the only two of these it also needed.

`perfil.html`/`socio.html` each also render a "Licencias federativas" section — a year
`<select>`, mirroring `dashboard.js`'s own Actas asambleas, plus a grid of small
clickable thumbnails rather than a plain list of text links — instead of pointing at
that person's `licencias/` subdirectory (see Commands above) through `userFiles.js`'s
generic browser: `js/licenseFiles.js`'s `initLicenseFiles(login, opts)` fetches that one
directory's listing (`GET /users/{login}/files?path=licencias`), builds the `<select>`
from whatever years actually have a license on file (ascending, most recent selected by
default — nothing conjures up an empty year the way `loadActas` doesn't either), and
renders each of that year's files as a `licenseCard`: a thumbnail linking straight at
`GET /users/{login}/files/file/licencias/<name>` (a bare `<img>`/`<a href>`, the same
cookie-authenticated plain-link shape `dashboard.js`'s own `fileLink` already uses for
Documentos/Actas/Memorias, since this route accepts the session cookie too — see
`internal/userfiles` above — so there's no need for `apiFetchBlob`'s Blob dance), with a
"DD-MM-YYYY — Tipo de licencia" label underneath parsed the same way
`dashboard.js`'s own `parseActaName`/`actaYear`/`formatActaDate` read
`yyyy-mm-dd(-suffix).pdf` back out of an acta's filename; within a year, files sort by
`(month, day, tipo_de_licencia)` rather than trusting the raw filename string, the same
reasoning `loadActas`' own `renderSelectedYear` sorts by `(month, day, suffix)`. A file
whose name doesn't match that convention has no year to file it under, so — the same as
an acta named anything else — it's simply left out.

The thumbnail itself is a companion file at
`licencias/.thumbnails/<full original filename>.jpg` (any original extension — not just a
PDF's) — a dotfile-prefixed subdirectory, invisible to the plain "licencias" listing this
module reads (`userfiles.Service.ListDirectory` skips any entry, file or directory,
starting with "."), the same "invisible companion file, fetched by exact path" trick
`dashboard.js`'s own `.order.json` already relies on. Nothing in the running app generates
one; `scripts/generate-license-thumbnails.sh` does, on demand, for every license copy that
lacks one (see **Commands**); one dropped in by hand next to the license's own file works
the same. It is meant to be 280px on its longest side — twice `.license-thumbnail`'s own
140px display size in `style.css`, so it still looks sharp on a high-DPI screen.
`licenseCard`'s `<img data-fallback-src>` tries that thumbnail first;
`wireThumbnailFallbacks` catches a load failure (the common case until the script has been
run) and falls back to the license's own file directly only if that file is itself an
image (`fileKind`, `fileBrowser.js`) — nothing to generate a thumbnail for when the real
file already is a small, displayable image — and, failing that too (a PDF or anything else
with no thumbnail dropped in for it yet), swaps in a plain 📄 placeholder of the same size
rather than the browser's own broken-image icon, so the grid stays visually consistent
whether or not every license has a real thumbnail yet.

A missing `licencias/` subdirectory 404s the same as any other unknown one
(`userfiles.Service.ListDirectory`), caught and shown as "no hay copias" rather than an
error — nobody's dropped a license copy in yet is the common case, not a real failure.
`profile.js` passes `hideWhenEmpty: true` (mirroring `initUserFilesBrowser`'s own option)
so "Mi perfil" doesn't show an empty section for the common case of no license on file
yet; `member.js` leaves it on by default (always shown) so admin/board reviewing someone
else's page can tell "no license copies yet" apart from a page that failed to load
anything at all. `.info-card`/`.info-card label`/`.year-select` (the
bordered-card-with-a-year-filter look Actas asambleas already had) live in the shared
`style.css` rather than the page-specific `dashboard.css` precisely because these two
pages need them too now; `.license-thumbnail-grid`/`.license-thumbnail-card`/
`.license-thumbnail`/`.license-thumbnail-placeholder`/`.license-thumbnail-label` are
this section's own, `style.css` as well since `dashboard.css` stays index.html-specific.

`/repartos`, `/reparto?id=…`, `/reparto-pedidos?id=…` and `/reparto-pagos?id=…`
(`distributions.js`, `distribution.js`, `distribution-orders.js`, `distribution-payments.js` — the
list of one distribution's payments, reached by the "Pagos" button of its page (admin/board, only
when it has something to pay), with a date range and "Ver más" like the ledger of an account —
sharing `js/distributionCommon.js`) are the interface for **Distributions** above — where
what the code and the API call a unit is a "pedido" to the people reading these pages, and
an extra unit an "extra", since "unidad" meant nothing to them — behind one "Repartos"
sidebar entry (`layout.js` maps the three detail pages to it in `ACTIVE_ALIASES`). The
list opens a new distribution from a dialog (admin/board; the options go in a textarea,
one per line, "Talla: S, M, L", `parseOptions`) and shows "Puedes pedirlo" on the open
ones whose `eligible` is true. The detail page renders the conditions and, except while it
is a draft (`#ask-sections` is hidden then, and the orders are loaded only once it leaves
the draft), a "Pedir" form (an "A nombre de" picker over `GET /users/directory`, one
`<select>` per option, an extra checkbox when `extra_price` exists — except in a
distribution for all members with no options, where the own order is already there and
only extras are left: no checkbox, the request always goes as an extra, the button says
"Pedido extra" (`extrasOnly`) and the subtitle gives the price, "Puedes pedir también
extras a 8,00 €" —; `canOrderNow` repeats the API's own open/deadline rule only to decide
whether to show it) and "Tus pedidos" — the units where the signed-in person is the
beneficiary or entered them, since `GET .../units` returns everything to
admin/board/material — with who entered each when that isn't the person it is for. In a
distribution for all members the member's own unit is already in that table ("Automático",
"Sin elegir" with a primary "Elegir" button until somebody chooses, no "Quitar" since the
API refuses it), and the whole "Pedir" section (`#order-section`, not just the form, so
not even a "no longer admitting orders") is out when there is neither an option nor extras
to ask for, `nothingToAsk`. The table's columns (`unitColumns`) are only the ones that
mean something: "Elegido" only with options, "Tipo" (own or extra) only when extras exist,
and nothing about payment for now — the state is "Recogido" or "No recogido"; the price
(the unit's own, frozen) is shown in a "Precio" column, but nothing about paying (no
state, no amount owed): the intranet doesn't keep that. Admin/board move the status one
step at a time (`bodyFrom` rebuilds the whole body, as `PATCH` replaces every field) and
edit, and only admin deletes (`DELETE` is admin-only in the API, and the button is
`data-roles="admin"`); every control restricted by `data-roles` is *removed* from the page
for anybody else, so each script reaches those through a null-safe lookup (a bare
`getElementById(...).addEventListener` on one of them would throw before the rest of the
page ran). `/reparto-pedidos` (`requireAnyRole` admin/board/material; the old summary and
collection pages are this one) is everything about the orders in one place: on top `GET
.../order` (what has to be bought), below it a list with one line per order (searchable by member
or number; the ones with everything paid and handed out are hidden unless asked for) and a summary:
what, how many are handed out and paid, what it costs. "Más información" unfolds its detail
(`detail`, kept in `unfolded`): for an article with numbers (`isNumbered`) how many are without one
(with "Asignar los siguientes libres" or typed numbers) and the list of its numbered items, each
with its paid / delivered state, to tick (`ticked`; "todos / sin pagar / sin entregar…" tick by
state, `pick`) and act on — mark paid or take the payment off (admin/board), hand out to the
person asked in a dialog (`deliver-dialog`, "Entregado a", prefilled with the member the order is
for; only members are offered), undo a delivery, return, take the numbers off; elsewhere a count field (`quantityOf`) does the same.
Paying and taking a payment off first ask, in `pay-dialog`, for the asset account (`withAccount`: the non-hidden `asset` ones of `GET /accounts`, the last chosen remembered in `localStorage`; skipped when the order costs nothing) and send it as `account_id`. The create/edit dialogs of a distribution have a "Cuenta de los pagos" select (`loadAccountOptions`; empty is the default account, named in its first option). Everything goes through the single `perform(act, unit)`, confirming before handing over unpaid
units, undoing, returning or taking numbers off. Wherever the frontend lists numbers they are sorted (`sortIdentifiers`: part by part, digits
as numbers) and joined into ranges (`formatIdentifiers`; `compactNumbers` for a bare list such as
the numbers an error message or an entry of the list of payments names). The Telegram messages about numbers say them the same way
(`formatNumbers`/`compareNumbers` in `numbering.go`, a Go copy of those two that must keep giving
the same order and ranges; each message says what it changed — numbers added, taken off or newly
assigned — not what was typed). What is unfolded, ticked and typed survives
redrawing.
Both pickers ("A nombre de", "Entregado a") offer only members, never everybody on file:
`loadPeople(id, purpose)` takes the entries from `GET /distributions/{id}/people` —
`for=order`, the members on the cutoff date (today's when there is none); `for=pickup`,
also today's members — and only the names shown for who did what come from
`/users/directory`. The API enforces the same (`not_eligible`, `invalid_collector`), the
pickers just don't offer what would be refused. `api.js`'s `apiFetch` now puts the API's
error `code` on the thrown `Error`, which `distributionCommon.js`'s `apiErrorText` maps to
Spanish messages (falling back to the API's own text).

`/contabilidad` (`accounting.js`) manages the chart of accounts from
`GET`/`POST`/`PATCH /accounts` and `POST /accounts/{id}/move` — a tree, indented by depth,
of every account's name, direct balance, and last movement date (each name a plain
`<a href="/cuenta?id=…">` into that one account's own detail page, below). There is
deliberately no code column, nor codes in the parent pickers: a code only shows on that
account's own `/cuenta` page, though the create/edit dialogs still have the field to set
it. The page also has the controls to create an account ("+ Nueva cuenta" dialog), correct
one's name/code/parent (one shared edit dialog, `openEditAccountDialog`), and swap it up
or down among its own siblings. Both dialogs carry three checkboxes, "Para cuotas
anuales", "Para licencias" and "Oculta" (`allows_dues`/`allows_licenses`/`hidden`, styled
by `.checkbox-label` in `style.css` so the box sits next to its text instead of being
stretched by `.field input`'s `width: 100%`), always sent explicitly — unchecked is
`false`, not omitted, since `PATCH` replaces every field — and each row shows a small
"Cuotas"/"Licencias"/"Oculta" badge next to the account's name for whichever are on, so
which accounts a transaction can record those against is visible without opening each
one. `js/accountTree.js` holds the
`childrenByParent`/`flattenAccountTree` pair both this page and `account.js` need:
`GET /accounts` already comes back as a pre-order, depth-first walk (see the `accounts`
bullet under **Accounting** above), so filtering that flat list down to a single
`parent_id`, in the order it already arrives in, *is* that group's own sibling order — one
pass groups every `parent_id` at once, no separate tree-building pass. Hidden accounts
(`hidden`) are left out of the table, and of the parent pickers, unless a "Mostrar cuentas
ocultas (N)" checkbox — shown only when there are any — is on (a view choice, never
persisted); each is tagged "Oculta" and dimmed when shown;
`shownAccounts` (`accountTree.js`) drops them while re-parenting their shown children onto
the nearest shown ancestor, since filtering the flat list alone would lose those children
from the tree walk. The ↑/↓ arrows follow the real sibling groups but count only shown
siblings, and a move repeats once per hidden sibling it has to pass (the endpoint swaps
with the very next one, which would otherwise look like nothing happened). A hidden
account stays offered as the parent picker's choice when it is the current parent —
dropping it would move the account to the top level on save. A `move`'s own response (the
whole reordered list) re-renders directly from `applyAccountsList` rather than triggering
a follow-up `GET`. Only admin and the optional "money" role ever see `/contabilidad` in the
sidebar at all, or can even land on the page (`hasAnyRole(user, ['admin', 'money'])`, else
redirected to `/`) — tighter than the API itself, which opens *reading* the chart to every
baseline role the same as `GET /transactions`; admin/money can already write here too, so
nothing on the page needs a narrower check than that one gate.

`/cuenta?id=…` (`account.js`) is one account's own ledger — reached only by clicking an
account's name on `/contabilidad`, never linked from the sidebar itself, so it shares that
same admin/money gate (and `js/layout.js`'s `ACTIVE_ALIASES` maps it back to `/contabilidad`
for the sidebar's own highlighting, the same `/actividad`→`/actividades`/
`/socio`→`/socios` treatment every other detail-off-a-listing page gets). Renders
`GET /accounts/{id}/transactions` (see **Accounting** above) as a table — date,
description, this account's own entry amount, its running balance after, and per-row
Editar/Borrar (a row also opens on a click anywhere but those buttons, or Enter/Space when
focused — see below). The listing is paginated (`loadEntries`): the newest 50
(`ENTRIES_PAGE_SIZE`) first, "Ver más" for the next ones, and above it a "Buscar en la descripción" box with a Desde/Hasta filter
(the `limit`/`offset`/`from`/`to`/`q` query parameters of that route, applied with "Filtrar"; it asks for one entry
more than a page just to know whether to show "Ver más"). A "+ Nueva transacción" dialog,
deliberately narrower than the full `POST /transactions` shape, creates transactions: a
date, a description, and a growing list of *lines* (`setupEntryLines`), each an amount "en
esta cuenta" plus the "otra cuenta" it comes from or goes to. As soon as the last line has
both fields a new empty one appears below it; a line with neither is ignored, one with
only one is an error, the same account can't be on two lines, and the account picker
starts on a "— Elige una cuenta —" placeholder precisely so "filled" means something. On
save (`entriesBody`, shared by creating a new transaction and correcting an existing one)
this account gets one entry for the *sum* of every line's amount, added up in whole cents
so floating point never shows up in the result, and each other account gets the exact
negative of its own line — so the entries sum to zero without whoever is typing having to
think about the balancing side. Amounts that cancel out (this account would get a zero
entry) are refused. A read-only "Total en esta cuenta" next to the date (`updateTotal`)
shows that sum live, on every keystroke, so it counts a line whose account isn't picked
yet and ignores an empty or non-numeric amount. A row's own "Editar" fetches that one
transaction fresh (`GET /transactions/{id}`, never kept in memory alongside the ledger
listing, which only ever carries this account's own side of each one) and turns each of
its *other* entries into a line (amount negated back); if it doesn't have exactly one
entry on this account, or has no other entry (only possible by calling the API directly),
editing is refused with an explanation instead of silently merging them. The dues/licenses
sections follow every account picked on any line (`sectionsFor`), not just one. "Borrar"
(`confirm`-gated, the same pattern `member.js`'s own delete buttons use) removes the whole
transaction, every entry included. Clicking a ledger row (`tr.entry-row`,
`toggleEntryDetail`) expands a detail row right below it — starting under "Descripción"
(an empty first cell), with no line between the two and the same background, so it reads
as the continuation of the row above rather than as another row — listing the
transaction's *other* accounts — each name (just the name, no code — the code only appears
in the page's own header) linking to that account's own ledger, with the amount it gained
(`+`) or lost (`−`; `signedCurrency`) — fetched with `GET /transactions/{id}` the first
time (the listing only ever carries this account's own side, see `AccountEntry`) and
resolved to names from `allAccounts`, which the page already has; it works for
transactions of any number of entries. Under them it also lists the cuotas anuales and
licencias the transaction paid for — each as the person's name (from
`GET /users/directory`, which the page loads for everyone who can reach it, not just for
the admin pickers) linking to `/socio`, plus year (and license type) — from the
`dues`/`licenses` that same response carries for admin/board only, so they simply don't
appear for anyone else. Clicking the row again collapses it, a click on Editar/Borrar or
on a link never toggles it, and any reload of the list starts every row collapsed.
Pressing "n" anywhere on the page opens the new-transaction dialog (ignored while typing
in a field, with a modifier held, or with a dialog already open), which puts focus on the
date; the edit dialog puts it on the description. The date input only ever collects a
plain date, but `occurred_at` is a real timestamp — sent as that date at noon UTC (not
midnight) specifically so `formatDate`'s own local-timezone getters can never roll it back
to the previous day for anyone viewing from behind UTC; editing an existing transaction
collapses whatever time of day it actually had down to this same convention. Both dialogs
also carry, for a real admin only (`data-roles="admin"`; POST dues/licenses is
admin-only), a "Cuotas anuales a registrar" and a "Licencias nuevas a registrar" section
(`setupDuesAndLicensesSection`, prefix `nt`/`et`), each a list of pending chips sent as
`POST /users/{login}/dues`/`licenses` once the transaction itself is saved. Each section
is hidden (`hidden`) until it could actually succeed: `sectionsFor` shows dues if this
account or any account picked on a line has `allows_dues`, licenses if any has
`allows_licenses`, re-evaluated whenever a line's picker changes and whenever a dialog
opens, and a section that gets hidden also drops its pending chips — otherwise one added
while a qualifying account was selected would still be sent, and only rejected by the API
after the transaction was already saved. Separately from that, the edit dialog lists what
earlier saves already registered against the transaction, read-only (no ×: nothing removes
a due) from the `dues`/`licenses` its own `GET /transactions/{id}` already returns
(`setExisting`): a section stays visible for as long as it has something to list even when
no account qualifies any more (the flag was since turned off, or the accounts changed) — a
record of what was paid, not something about to be sent — while its add controls follow
the qualifying rule above. When neither section applies, a hint explains that an account
has to be marked for it, in Contabilidad, instead of showing nothing. The "+ Nueva
transacción" dialog opens with today's date (`todayAsInputValue`, built from local
getters: `toISOString()` would be UTC's date, already tomorrow's for part of the evening
east of it), and each section's "Año" input starts from the transaction's date — that
date's own year, or the next one in December, since a payment that late is almost always
for the following year (`suggestedYear`). The inputs keep following the date until the
person types in one, which is then left alone until the dialog is next opened
(`editedYearInputs`, cleared by `reset`/`syncYears`); adding a chip puts that input back
on the suggestion, and the edit dialog does the same from its own date. Each line's "otra
cuenta" picker (the same list in the create and edit dialogs) leaves hidden accounts out —
except those the transaction being edited already uses, which stay choosable
(`otherAccountOptions`'s `keepHidden`) — and lists every other account by name only, no
code (indented the same way `accounting.js`'s own pickers are, via `flattenAccountTree`) —
excluding only this account itself, not its ordinary parent/children the way
`accounting.js`'s own "cuenta superior" picker excludes descendants: there's no circular
relationship to guard against here, any two distinct accounts can be on the two sides of a
transaction. The page's own header (name, code, current balance) and that picker both come
from one `GET /accounts` call — that listing already carries every account's own balance,
so there's no need for a second, single-account fetch just for the header
(`GET /accounts/{id}` alone never populates it — see **Accounting** above).

`/socios` (`members.js`) is a read-only, sortable table of people; every action about a
person lives on their own `/socio?login=<login>` page, which a row opens for admin/board
only. "+ Nueva persona" is admin-only, in the markup and in `members.js`, which loads the
role catalog (for that dialog's role checkboxes) only for an admin. The list of roles
itself, to create or delete one, is no longer here: it lives on `/admin`, the page behind
the "Admin" sidebar entry (admin only, `NAV_ITEMS`' `roles: ['admin']`), meant for
whatever else only administrators care about — `admin.html`/`admin.js`, which for now
holds an "Estado del sistema" section (`GET /admin/status`, see **Admin status**: version
and commit, database and migration, email, Telegram, timeouts, directories, with an
"Actualizar" button), a "Roles" section (`GET`/`POST`/`DELETE /roles`; a "Ver" under each
role unfolds who holds it, taken from `GET /users` — admin sees everybody with their
`roles` — with a "×" per person (`DELETE /users/{login}/roles/{id}`) and a name picker
with "Dar este rol" (`POST /users/{login}/roles`); the automatic roles only list their
holders, and the admin's own admin role has no "×", as on `/socio`; the unfolded roles
survive the re-render in `expandedRoles`) and a "Sesiones" one: every session from
`GET /tokens` (a person's name from the directory, last use and address, creation and
expiry, "Activa"/"Caducada"/"Revocada", a badge on an impersonation's and on the admin's
own "Esta sesión"), 50 at a time with "Ver más" and a checkbox for the expired and revoked
ones, where an active one can be revoked (a different warning for one's own, which signs
out) or have its expiry changed, through `/users/{login}/tokens/{id}`, and a "Festivos"
one: the club holidays the activities calendar tints, a year at a time with ‹ ›
(`GET /holidays?year=`), each with a "Borrar" (`DELETE /holidays/{id}`), a "+ Nuevo
festivo" dialog (`POST /holidays`; a date that already has one is the API's 409
`duplicate_holiday`) and "Copiar del año anterior", which POSTs last year's holidays on
the same day and month except where the year already has one (movable ones such as Semana
Santa are fixed by hand; there is no edit). Admin/board get `FULL_COLUMNS` and
`FULL_FILTERS`; a plain member gets `MEMBER_COLUMNS`/`MEMBER_FILTERS`, limited to what
`users.MemberListing` carries (see **Permission tiers**). Row filters ("Género", "Tipo de
socio", "Categoría", "Cuota anual", "Federación") and the column toggles sit behind a
"Filtros…" button, and above it a "Buscar persona" box (`matchesSearch`: every word typed has to appear, in any order and ignoring accents and case, in the name, membership number, login and — admin/board only, as that is all their listing carries — email, phone and DNI; it narrows what the filters leave and isn't persisted). Every filter starts with every option checked except "Tipo de socio",
which starts with only "Activo" and "Baja temporal", so the default view is the current
membership. "Cuota anual" splits people on whether `last_due_year` is the current year ("A
corriente de pago"), and "Federación" on whether `license_type` is present — `GET /users`
only reports a license for the current year, so having one *is* being federated. Filters,
visible columns and sort order persist in `localStorage` (`katiuskas:members-view`,
best-effort), so "Volver a socios" from a person's page lands on the same view, and a
counter shows how many people the current filters leave. With nothing saved, the columns
shown are "N.º socio", "Nombre", "Apellidos", "Categoría" and "Tipo de licencia" (each
column's `default` flag); the rest are optional in the "Filtros…" panel.

A page's requests that don't depend on each other go out together, not one after another
(each wait in a row costs a full round trip, which is most of what a page load takes once
the API itself is fast): `/socios` asks for `/users`, `/board-members` and, for an admin,
`/roles` at once and draws the table when the board positions are in too
(`loadUsers(ready)` — only the drawing waits, not the slow `/users` request), and `/admin`
loads its five sections with one `Promise.all` (the sessions are drawn again with the names
if the directory arrives after them). That is the convention for a page's first load, and
the rest follow it: `/actividades`, `/actividad`, `/material`, `/socio`, `/cuenta` and the
distribution pages start every independent request together, and whatever drawing needs
something else (the directory of names, the role catalog, the people) takes it as a `ready`
promise to wait for after its own data arrives, so only the drawing waits. The one
dependency that remains is `/reparto`, which asks for the orders only once it knows the
status isn't draft.

On `/socio`, a real admin also has a "+ Licencia sin pago" button (`add-license-dialog`)
that records a license with no transaction (`POST /users/{login}/licenses` without
`transaction_id`) — the way to register one the club didn't collect, such as someone
federated elsewhere. Licenses the club does collect are registered from their transaction
on `/cuenta`, as above.

**API contract**: `backend/openapi/openapi.yaml` is the source of truth for the HTTP
surface — keep it in sync with handler changes (routes, request/response schemas, role
requirements described in each path's `description`).
