# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Language

- **All code** (identifiers, comments, logs, API error messages): **English**.
- **Documentation** (this file, `docs/`, `README.md`): Spanish or English, either is fine.
- **Frontend** (`web/`): all user-visible text in **Spanish** — it's the interface for a
  Spanish caving club. JS/HTML identifiers themselves stay in English.

## Commands

Local stack (Postgres + API + Caddy-served frontend):
```sh
docker compose up --build
```
API on `:8080` (direct) / behind Caddy on `:8000` with `/api/*` proxied; frontend on `:8000`.

Migrations and first admin account (run once against a fresh database):
```sh
export DATABASE_URL=postgres://intranet:intranet@localhost:5433/intranet?sslmode=disable
make migrate-up      # goose, see db/migrations/
make seed-admin      # idempotent; creates admin@club.test / 600000000 unless SEED_ADMIN_* overrides
```
There are no passwords: sign-in is a magic link. With `SMTP_HOST` unset, the API logs the
link instead of emailing it — `docker compose logs api` or `scripts/get-token.sh <email>
<phone>` to fetch one for local testing without copy-pasting from logs.

Backend tests (from `backend/`):
```sh
docker exec -it katiuskas-intranet-postgres-1 psql -U intranet -d postgres -c "CREATE DATABASE intranet_test;"
export TEST_DATABASE_URL=postgres://intranet:intranet@localhost:5433/intranet_test?sslmode=disable
go test ./...                                    # all packages
go test ./internal/users/...                     # one package
go test ./internal/users/... -run TestCreate      # one test
```
Every package touching Postgres (all but `auth`, `config`, `mail`, `httpapi`) runs against
a real test database — no mocked data access, since Postgres constraints (exclusion,
foreign keys, uniqueness) are part of what's under test. `internal/testutil.NewTx`
migrates once and hands each test its own transaction, rolled back on cleanup. Without
`TEST_DATABASE_URL` reachable, those tests `t.Skip` instead of failing.

Other:
```sh
make run                          # go run ./cmd/api, no Docker
make build                        # backend binary to bin/api
go vet ./...                      # from backend/
go build ./...                    # from backend/ — cheapest full compile check
python3 -c "import yaml; yaml.safe_load(open('backend/openapi/openapi.yaml'))"  # validate the spec after editing it
```
Rebuilding/redeploying just the API after a backend change:
```sh
docker compose build api && docker compose up -d api
```
The frontend needs no rebuild — Caddy bind-mounts `web/` live (see `deploy/Caddyfile`).

Club documents (estatutos, actas...) shown at `/library`: drop files into `./library/` next
to `docker-compose.yml` (bind-mounted into the API container, auto-created if missing) — no
rebuild, no restart, nothing through the API itself. That directory is gitignored.

## Architecture

**Layers**: `backend/internal/httpapi` (router assembly, `ClientIP`, request logging,
panic recovery, `WriteJSON`/`WriteError`) → one package per domain resource (`users`,
`roles`, `memberships`, `licenses`, `board`, `activities`, `inventory`, `tokens`), each
split into `model.go` / `repository.go` (pgx) / `service.go` / `handler.go`. The domain
layer never imports `net/http`; only `handler.go` knows HTTP exists. Each resource package
owns and registers its own routes via `RegisterRoutes(mux)`, wired together in
`cmd/api/main.go`. **`internal/library`** is the one exception to this shape: it serves a
read-only document tree from disk (`config.LibraryRoot`), not Postgres, so it has no
`repository.go` — `service.go` works directly against the filesystem, resolving and
validating every caller-given path against root before touching disk (`Service.resolve`),
since there's no database constraint to lean on for that instead.

**`db.PGX` interface** (`internal/platform/db`): the subset of `*pgxpool.Pool` a
repository actually calls. Both a pool and a `pgx.Tx` satisfy it, so production code
builds a repository on the pool while tests build the same repository on a transaction
that gets rolled back — the repository code never knows the difference.

**Auth is a single opaque, revocable, database-backed session token** — no JWT, no
separate access/refresh pair. `internal/auth` only defines `AuthenticatedUser`,
`Authenticator.Protect`/`RequireAuth`/`RequireRole`, and a `SessionResolver` interface;
it has no database dependency. `internal/tokens` is the real resolver: it hashes the raw
token, validates and touches `last_used_at`/`last_used_ip` in one query, then resolves
roles *live* (not baked into the token) via `roles.Service`. `tokens` also exposes
admin-only endpoints (`GET`/`PATCH`/`DELETE /users/{login}/tokens[/{tokenID}]`) to audit,
re-expire, or revoke any token, plus `POST /users/{login}/impersonate` (admin-only,
issues a short-lived token — `config.ImpersonationTokenTTL` — that resolves as the target
person in every way except `AuthenticatedUser.ImpersonatedBy`, which names the admin) and
`POST /auth/impersonate/exit` to end one and get a fresh normal token back.
`internal/authapi` glues magic-link request/consume/logout and `GET /auth/me`
(identity+roles resolved from context, no extra DB round trip — the frontend calls this
once per page load since an opaque token has nothing to decode). A magic-link email also
carries a 6-digit login code — hashed alongside the link's token in the same
`magic_link_tokens` row, so consuming either one consumes both — for signing in on a
device other than the one the email was read on; `POST /auth/magic-link/consume-code`
identifies the caller by email+phone (the code alone is too short to look someone up by)
and a per-row attempt counter bounds brute-forcing it without affecting the link itself.

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
stale role to clean up.

**Login is the only external identifier for a person**: `users.login` (`text UNIQUE NOT
NULL`, migration `00009_users_login.sql`) is generated automatically at creation — first
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

**Permission tiers**: `admin` has full read/write access everywhere, including a person's
core data and role assignment. `board` can read everything (the full `User` record,
licenses, roles) but can only write activities and material — every person-data,
role-assignment, license, and Junta Directiva write endpoint rejects board even though it
passes the baseline check. `member` gets `MemberListing` instead of `User` from
`GET /users` (name, gender, computed `age_category` — never the exact `birth_date` it's
bucketed from — membership number, alta/baja dates; no DNI, contact info, license, or role
details) and may only edit their own contact fields (`email`, `phone`, `address`,
`postal_code`, `city`, `province`, `emergency_contact`) via `PATCH /users/{login}` —
anything else in that body gets 403 `restricted_fields` instead of being silently ignored.

**Activities**: every activity has a mandatory `leader_id` (exposed as `leader_login`; the
organizer, always set to whoever creates it — never chosen by the caller) who, along with
admin/board, is the only one who can edit or cancel it (`Handler.canManage`). Its `type`
(`caving`/`canyoning`/`training`/`social`/`other`) gates a federation check: joining — or
being named as an initial participant at creation time — a `caving`/`canyoning` activity
requires a currently valid federative license (`licenses.Service.IsCurrentlyFederated`),
else 409 `not_federated`. Its `visibility` (`public`, the default, or `private`) gates who
may add a participant at all, not just who may join: public works as it always has (anyone
can sign themselves up, admin/board can add anyone else); private restricts adding
*anyone* — self included — to the organizer, board, or admin (`Handler.join`).

**Licenses**: a license's type isn't free text — it's a `license_type_id` referencing the
`license_types` catalog, seeded in the database (migration `00007_licenses.sql`) rather
than a Go enum, so the list can grow with an `INSERT` instead of a deploy. Its `is_other`
escape-hatch entry ("Otra") requires an `other_detail` string saying which license it
actually is; every other entry forbids one (shared validation:
`licenses.normalizeOtherDetail`, used by both `Create` and `Update`).
`PATCH`/`DELETE /licenses/{id}` (admin-only) correct or remove an assigned license without
re-checking active-membership status the way `POST` does — that gate is for assigning a
*new* license, not fixing a typo on one someone already has.

**Document library** (`internal/library`): serves club documents (estatutos, reglamentos,
actas...) read-only from a plain directory tree bind-mounted from `./library` on the host
— see Commands above — not the database. `GET /library?path=...` lists a directory
(subdirectories first, then files, both alphabetical; dotfiles never listed);
`GET /library/file?path=...` streams a file's raw bytes, inline by default or as an
attachment with `?download=1`, with HTTP Range support via `http.ServeContent`. Both are
open to any baseline role and read-only — nothing here lets a caller upload, rename, or
delete anything.

**Testing helpers**: `internal/testutil` provides `NewTx` (migrated, isolated
transaction), `InsertUser`/`InsertActivity`/`RoleID`/`LicenseTypeID` fixtures (the
`TestUser` `InsertUser` returns carries a `Login` too, for tests exercising a
login-addressed route), and
`FakeResolver` + `NewTestAuthenticator`/`TokenFor` — an in-memory `auth.SessionResolver`
so handler tests in every *other* package can mint bearer tokens for arbitrary
users/roles without hitting the real `tokens` table (only the `auth` and `tokens`
packages' own tests exercise real session mechanics; `auth`'s internal tests use a local
stub instead of `testutil`, since `testutil` imports `auth` and an internal test file
can't import back). `internal/library` needs none of this — its tests build a `Service`
directly on a `t.TempDir()` tree, no Postgres involved at all.

**Migrations** (`db/migrations/`, goose-numbered): kept squashed to the *current* schema
shape, not the historical sequence of changes — no renamed columns, no lossy "down"
transformations, no comments about what something used to be called. When schema work
piles up several incremental migrations, squash them back into the relevant per-resource
file(s) and verify schema-equivalence by applying the old and new sets to throwaway
databases and diffing `pg_dump --schema-only` output, rather than leaving the ALTER trail
in place.

**Frontend** (`web/`): classic multi-page site (no SPA, no build step), one `<page>.html`
+ `js/<page>.js` per view, Caddy's `try_files {path} {path}.html` making `/members`
resolve to `members.html`. `js/api.js` is the shared client: keeps the session token in
`localStorage`, and `initSession()` calls `GET /auth/me` on every page load to validate
the token and populate `currentUser()`. `applyRoleVisibility()` removes any
`data-roles="admin,board"` element the current user's roles don't satisfy — client-side
UI hiding only; the API is the actual authority. A permission that depends on data loaded
asynchronously for one specific record (e.g. "is the viewer this activity's organizer?")
can't be a static `data-roles` attribute, since `applyRoleVisibility()` only runs once at
page load — that case computes a plain JS boolean after the data arrives and toggles
`.hidden` directly instead (see `activity.js`'s `canManage`/`canJoinSelf`/`canAddOthers`).
Detail pages read a login from the query string (`/member?login=<login>`,
`new URLSearchParams(window.location.search)`), same pattern `login.html` uses for the
magic-link token from an emailed URL. Its login code has no URL of its own to arrive
in — `login.html`'s second panel gets it typed in by hand — so the email/phone from the
first step are instead remembered in a plain JS variable across that two-step flow.

Downloading or previewing a `/library` document has no session cookie to ride along on a
plain `<a href>` (the API only ever authenticates via a bearer header), so it fetches the
file with `apiFetchBlob` (`api.js`) and works with the resulting `Blob` client-side — an
object URL for inline preview, a synthetic `<a download>` click to save. Markdown
documents render through a small dependency-free converter (`js/markdown.js`) that
resolves each document-relative link against the document's own directory and rewrites it
into an in-app `/library` navigation link; HTML documents render in a fully sandboxed
`<iframe>` instead, with no such link rewriting.

**API contract**: `backend/openapi/openapi.yaml` is the source of truth for the HTTP
surface — keep it in sync with handler changes (routes, request/response schemas, role
requirements described in each path's `description`).
