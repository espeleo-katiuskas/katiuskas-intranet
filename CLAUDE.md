# CLAUDE.md

Guidance for working in this repository. It is short on purpose: it holds what has to be at hand
in every session — conventions, commands and the invariants that are easy to break. The long
explanations live in `docs/`; read the one that matches the task before changing a resource.

## Language

- **All code** (identifiers, comments, logs, API error messages): **English**.
- **Documentation** (`docs/`): **English**, in every file. The `README.md` is still in Spanish for
  now. The Spanish words of the club's domain stay as they are when quoting what the interface shows.
- **Frontend** (`web/`): all user-visible text in **Spanish** — it's the interface for
  Club de Espeleología Katiuskas, a Spanish caving club. JS/HTML identifiers stay in English.

## Where to look

| File | What it has |
|---|---|
| `docs/api.md` | every endpoint: who can call it, parameters, errors, objects. `backend/openapi/openapi.yaml` is the contract |
| `docs/database.md` | tables, columns, constraints, triggers, seeded data, migrations |
| `docs/backend-internals.md` | the long form, resource by resource: users/auth/roles, activities, distributions, licenses, dues, leaves, library, Telegram, accounting, test helpers |
| `docs/frontend.md` | the conventions every page shares and each page in detail |
| `docs/architecture.md` | overview and business rules |
| `docs/environments.md`, `docs/deployment*.md` | local / testing / production, and how to deploy |

Keep them in sync in the same change as the code: a route, body or error → `openapi.yaml` and
`docs/api.md`; a migration → `docs/database.md`; a rule or invariant → its section in
`docs/backend-internals.md` (and `architecture.md` if it is mentioned there); a page → `docs/frontend.md`.
`docs/*.md` never mention this file.

## Commands

Local stack (Postgres + API + Caddy-served frontend); API on `:64711`, frontend on `:64710` with
`/api/*` proxied:
```sh
docker compose up --build
docker compose build api && docker compose up -d api     # after a backend change; the frontend needs no rebuild
```
Migrations and first admin (once per fresh database); there are no passwords, sign-in is a magic link:
```sh
export DATABASE_URL=postgres://intranet:intranet@localhost:64712/intranet?sslmode=disable
make migrate-up      # goose, db/migrations/
make seed-admin      # idempotent; admin@club.test / 600000000 unless SEED_ADMIN_* overrides
```
With `SMTP_HOST` unset the API logs the link instead of emailing it (`docker compose logs api`), or
mint a session token directly with `scripts/get-token.sh <login>` — **dev only, never on testing or
production**.

Backend tests (from `backend/`); they run against a real Postgres, never mocked:
```sh
docker exec katiuskas-intranet-postgres-1 psql -U intranet -d postgres -c "CREATE DATABASE intranet_test;"
export TEST_DATABASE_URL=postgres://intranet:intranet@localhost:64712/intranet_test?sslmode=disable
go test ./...    # or ./internal/users/...   or  -run TestCreate
```
Without `TEST_DATABASE_URL` those tests `t.Skip`. The test database is only checked for existing at
all, never for which migrations it has: **after adding a migration drop and recreate it**
(`DROP DATABASE intranet_test`, then the `CREATE DATABASE` above) or tests fail with "column … does not
exist". CI starts fresh each run.

Other checks: `go build ./...`, `go vet ./...` (from `backend/`), `make run` (no Docker),
`make build`, and after editing the spec
`python3 -c "import yaml; yaml.safe_load(open('backend/openapi/openapi.yaml'))"`.
Before committing Go code (global rule): `go fmt`, `go build`, `go vet`, `go test -race`,
`staticcheck`, `govulncheck`, in that order.

Frontend (plain ES modules, no build step, so these are the only safety net), before a commit that
touches `web/`:
```sh
for f in web/js/*.js; do node --check "$f"; done
biome lint web/js
biome format web/js        # --write to apply it: two spaces, single quotes, 100 columns
```
To see a page, drive headless Chromium over the DevTools protocol with a small Python script
(details in `docs/frontend.md`); start it once in the background:
```sh
chromium --headless=new --disable-gpu --no-sandbox --user-data-dir=/tmp/chromium-profile --remote-debugging-port=9333 about:blank &
```
Sign in there with `localStorage.setItem('token', '<token from get-token.sh>')` before navigating.

Dropping files in (no rebuild or restart; both directories are gitignored): club documents into
`./library/`, per-person files into `./user-files/<login>/`, a license copy into that person's
`licencias/` as `yyyy-mm-dd-tipo_de_licencia.ext` (thumbnails with
`scripts/generate-license-thumbnails.sh`). See `docs/backend-internals.md`.

### Environments (`docs/environments.md`)

Local Docker stack (where changes are tried first), **testing** (`testing.katiuskas.es`) and
**production** (`intranet.katiuskas.es`): the last two are systemd installations side by side on
the club's host (`/opt/katiuskas-intranet-testing` and `/opt/katiuskas-intranet-stable`, units
`katiuskas-intranet-testing.service` and `katiuskas-intranet-stable.service`; the deployment tool
is in `/opt/katiuskas-intranet-deploy`), each with its own database, directory and Telegram bot,
sharing `LIBRARY_ROOT`, `USER_FILES_ROOT` and `ACTIVITY_RESOURCES_ROOT`. Production deploys without Docker
(`docs/deployment-no-docker.md`).

- **The host pulls**: `scripts/deploy.sh` (run by one systemd unit, `deploy/systemd/`) asks GitHub what
  each environment should run — the tip of `main` for testing, the highest semver tag for production —
  and updates what differs. it is started within seconds of a push (the webhook receiver creates a file that a systemd path unit
  watches; a request that arrives while a deployment runs is queued, several merge into one rerun)
  and, as a safety net, by a timer every 15 minutes; its output is in the unit's journal;
  **nothing in the request decides what is deployed** (the receiver only checks GitHub's signature). So merging into `main` is a live
  deploy, and migrations run against a copy of real data. `ci.yml` only tests. Roll production back by
  tagging a new, higher version.
- Production is backed up every night (`scripts/backup.sh`, a timer; database and files, with a retention and an optional hook
  to copy them off the host: `docs/environments.md`, "Backups").
- Failures are told by Telegram: the API watches its own status (database, directories, disks: `adminstatus.Monitor`)
  and a failed deployment or backup runs `scripts/notify-failure.sh` (`docs/environments.md`, "Alerts when something fails").
- Testing's database is a copy of production's, refreshed on every production deploy
  (`scripts/copy-prod-to-testing.sh`, from `deploy.sh`'s `AFTER_DEPLOY` hook): it holds real personal data and anything done there is overwritten.
- Because the directories are shared, don't grant admin/board in testing (it would open every member's
  personal documents) and don't delete or upload memoria files there (they carry production's ids).
- `web/` is served by Caddy with `Cache-Control: max-age=3600`, so a browser may keep an old script up to
  an hour after a deploy.

## Architecture essentials

Each point is the short version; the reasons and the edge cases are in `docs/backend-internals.md`.

**Layers.** `backend/internal/httpapi` (router, request logging, `WriteJSON`/`WriteError`) → one package
per resource (`users`, `roles`, `memberships`, `licenses`, `board`, `activities`, `activityresources`,
`distributions`, `inventory`, `tokens`, `accounting`), each `model.go` / `repository.go` (pgx) /
`service.go` / `handler.go`; only `handler.go` knows HTTP. Each package registers its routes with
`RegisterRoutes(mux)`, wired in `cmd/api/main.go`. `internal/library`, `internal/userfiles` serve a
read-only directory tree from disk (no repository; every caller path goes through `Service.resolve`) and
`adminstatus` only reads. Packages that can't import each other use small local interfaces satisfied
structurally (`UserLookup`, `DuesLookup`, `LicensesLookup`…).

**`db.PGX`.** Repositories take the subset of `*pgxpool.Pool` they call, so tests give them a
transaction (`testutil.NewTx`) that is rolled back. A `Begin` on top of a transaction is a *savepoint*:
deferred constraint triggers never fire there, which is why accounting forces
`SET CONSTRAINTS … IMMEDIATE` and puts it back.

**Auth.** One opaque, revocable, database-backed session token (hash stored; no JWT, no refresh pair),
roles resolved live on every request (`internal/tokens`). `Authenticator.Protect` *always* also requires
admin, board or member on top of the roles a route lists, so someone holding only `material` or `money`
is refused everywhere. The token is mirrored in the `katiuskas_session` cookie only for
`ProtectAllowingCookie`, used by the file-serving routes of library and userfiles (and nowhere else).
Magic-link token 128 bits (it travels in a URL), 6-digit code bounded by `code_attempts`; the request and
code-consume routes share one `IPRateLimiter`; the answers don't reveal who has access. Impersonation is
admin-only, short-lived (`ImpersonationTokenTTL`) and `POST /auth/impersonate/exit` needs only `RequireAuth`.

**Roles.** `admin`, `board`, `member`, `former_member` are system roles; `board`, `member`, `former_member`
are automatic (computed live in `roles.Repository.ListNamesForUser` from `board_members` and
`memberships`, never granted by hand, 409). Revoking `admin` is refused from the last admin
(`last_admin`) and from oneself (`self_admin_removal`); deleting the last admin too.

**A person is only ever identified by `login`** (generated from the name, admin-editable). No response or
path carries the numeric user id; packages translate through their local `UserLookup`. Listings that mix
people `JOIN` against `users` instead of one lookup per row. `email` and `phone` hold several values
separated by a space; uniqueness across people is checked in Go (case-insensitive email, digits-only
phone), not by a constraint.

**Permission tiers.** `admin` everything. `board` reads everything and writes only activities, holidays
and inventory. `member` gets `MemberListing` (not `User`) from `GET /users` — never the birth date,
roles, DNI or contact — and may edit only its own contact fields (anything else is 403
`restricted_fields`). `GET /users` is unpaginated on purpose. Accounting writes: admin or `money`;
inventory movements: admin, board or `material`.

**Activities.** Mandatory `leader_id`; only the organizer, admin and board edit or cancel; naming or
changing the organizer is admin/board only. `caving`/`canyoning` need a current federative license
(`not_federated`). `private` visibility restricts who adds and removes participants. Memoria resources
(`activityresources`): metadata in Postgres, file bytes on disk keyed by the row id, created once with
`O_EXCL` and **never rewritten** — other directory trees share them by hard link, so a rewrite in place
would corrupt both.

**Licenses, dues, leaves.** A license's type comes from the `license_types` catalog; `year` is the year
it covers, valid to 31 December (never stored); it may be assigned to anyone who was a socio at some
point of that year; an optional `transaction_id` must point at a transaction with an entry on an
`allows_licenses` account. A due is one row per `(user, year)`, always tied to its transaction
(`allows_dues` account), deleted with it; the first due of a person with no membership also opens the
membership. A temporary leave hangs off the person and must fall inside one of their membership periods
(checked in Go). Periods never overlap (an `EXCLUDE` constraint).

**Distributions** (the club hands things out; `draft → open → closed → done`, deliverable in any state
but `draft`). Units are rows per person (or per person and article with a `quantity`); `price` is frozen
when entered; an `all_members` distribution gives every member on `cutoff_date` an automatic own unit
(`SyncAutomaticUnits`). Payment and collection are per row and partial (`paid_quantity`,
`picked_up_quantity`); articles can have described numbers (`Serie: 124-133`), and numbered items are paid
and delivered one by one. Counting what is left of an article locks the distribution row (`FOR UPDATE`).
**Every payment is recorded in accounting**: the account paid in (`account_id`, an `asset`) against the
distribution's own income account or the one marked `distribution_default`, one transaction per person,
and deleting that transaction undoes the payment (`UndoPaymentsOf`, via a hook wired in `cmd/api`).

**Accounting.** Double entry: a transaction has two or more entries that sum to zero, enforced by a deferred
constraint trigger (see `db.PGX` above). An account has child accounts **or** transactions, never both
(triggers, migration 00033). An account without a type inherits its parent's: rules use
`effective_type`, and `finishAccountChange` re-checks, in the same transaction, that distribution
accounts and the default stay `income`, and that `allows_dues`/`allows_licenses` accounts are `income`.
`GET /accounts` rolls balances and last movement up through the tree; the ledger of a group lists its
descendants with a combined running balance.

**Telegram** (`internal/telegram`, `internal/telegrambot`): plain `net/http` client, no SDK. Notifications
are fire-and-forget (own goroutine, bounded timeout, detached context), with two exceptions: the startup
message and the bot-flow magic-link DM. Every resource package takes an `httpapi.MutationNotifier` and
sends one message per meaningful change (`httpapi.NotifyAsync`, `DiffFields` over a named field list that
matches that endpoint's `UpdateInput`). The bot long-polls; a linked chat does **not** log in on any
message — it must prove the registered email, and a shared contact is checked to be the sender's own
(`isOwnContact`), both security rules.

**Testing helpers.** `internal/testutil`: `NewTx`, `InsertUser`/`InsertActivity`, `InsertTransaction*`,
`RoleID`/`LicenseTypeID`/`MaterialCategoryID`, `UniqueEmail`, and `FakeResolver` + `NewTestAuthenticator`/
`TokenFor` to mint bearer tokens for any role without the real `tokens` table.

## Frontend essentials (`web/`)

Plain multi-page site, no SPA and no build step, on purpose: every page revalidates its session and
reloads its data. One `<page>.html` + `js/<page>.js` per view.

- Every authenticated page calls `await initPage({ requireAnyRole })` (`js/layout.js`): it validates the
  session (`GET /auth/me`), renders sidebar and topbar, applies `data-roles="admin,board"` visibility
  (UI only — the API is the authority) and returns the user. `login.html` is the only page without it.
  Permissions that depend on data loaded later are computed in JS and toggle `.hidden`.
- Every create/edit form is a `<dialog class="panel modal">` set up with `initModal(dialog, form?)` and
  opened with `openModal(dialog)` (`js/modal.js`, which keeps the page's scroll position).
- Errors: `showError(message)` opens a modal over everything that has to be closed, and `showFeedback` is
  the sticky success banner. There is no `#error` element.
- `js/api.js` keeps the token in `localStorage` (`token`), `apiFetch` puts the API's error `code` on the
  thrown `Error`, and the page's independent requests go out together, drawing waiting on a `ready`
  promise only where needed.
- Dates and amounts of a table stay on one line (`.nowrap`, `.balance-cell`); money is
  `formatCurrency`, dates `formatDate`.
- Search boxes normalize accents and case (`normalizeForSearch` in `api.js`).
- All user-visible text in Spanish; identifiers in English.
- Per-page details (distributions, accounting, members, license thumbnails, admin…) are in
  `docs/frontend.md`.
