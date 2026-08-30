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

## Architecture

**Layers**: `backend/internal/httpapi` (router assembly, `ClientIP`, request logging,
panic recovery, `WriteJSON`/`WriteError`) → one package per domain resource (`users`,
`roles`, `memberships`, `activities`, `inventory`, `tokens`), each split into
`model.go` / `repository.go` (pgx) / `service.go` / `handler.go`. The domain layer never
imports `net/http`; only `handler.go` knows HTTP exists. Each resource package owns and
registers its own routes via `RegisterRoutes(mux)`, wired together in `cmd/api/main.go`.

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
admin-only endpoints (`GET`/`PATCH`/`DELETE /users/{userID}/tokens[/{tokenID}]`) to audit,
re-expire, or revoke any token. `internal/authapi` glues magic-link request/consume/logout
and `GET /auth/me` (identity+roles resolved from context, no extra DB round trip — the
frontend calls this once per page load since an opaque token has nothing to decode).

**Baseline access policy**: `Authenticator.Protect(handler, roles...)` *always* also
requires at least one of `auth.BaselineRoles` (`admin`, `board`, `member`), on top of
whatever specific roles a route lists — holding only, say, `material` without also being
admin/board/member blocks every route, including material ones. `RequestMagicLink` won't
even send a sign-in link to someone matching on email+phone but holding no baseline role.

**Roles**: `admin`/`board`/`member`/`former_member` are system roles (`is_system`) wired
to real behavior; anything else an admin creates is just an organizational label. `member`
and `former_member` are *automatic* (`is_automatic`): never granted/revoked by hand
(rejected with 409), computed live in `roles.Repository.ListNamesForUser` from whether the
person has a currently-active `memberships` period — so losing membership revokes API
access on the very next request, no stale role to clean up.

**Testing helpers**: `internal/testutil` provides `NewTx` (migrated, isolated
transaction), `InsertUser`/`InsertActivity`/`RoleID` fixtures, and `FakeResolver` +
`NewTestAuthenticator`/`TokenFor` — an in-memory `auth.SessionResolver` so handler tests
in every *other* package can mint bearer tokens for arbitrary users/roles without hitting
the real `tokens` table (only the `auth` and `tokens` packages' own tests exercise real
session mechanics; `auth`'s internal tests use a local stub instead of `testutil`, since
`testutil` imports `auth` and an internal test file can't import back).

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
UI hiding only; the API is the actual authority. Detail pages read an id from the query
string (`/member?id=<id>`, `new URLSearchParams(window.location.search)`), same pattern
`login.html` uses for the magic-link token.

**API contract**: `backend/openapi/openapi.yaml` is the source of truth for the HTTP
surface — keep it in sync with handler changes (routes, request/response schemas, role
requirements described in each path's `description`).
