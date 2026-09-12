# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

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
API on `:64711` (direct) / behind Caddy on `:64710` with `/api/*` proxied; frontend on `:64710`.

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
TLS): see `docs/deployment.md`, `docker-compose.production.yml`,
`.env.production.example`. `scripts/get-token.sh` is dev-only — never use it there. For a
disk-constrained host, `docs/deployment-no-docker.md` covers the same deploy without
Docker — native PostgreSQL/Caddy plus systemd-managed Go binaries cross-compiled
elsewhere and copied over.

Backend tests (from `backend/`):
```sh
docker exec -it katiuskas-intranet-postgres-1 psql -U intranet -d postgres -c "CREATE DATABASE intranet_test;"
export TEST_DATABASE_URL=postgres://intranet:intranet@localhost:64712/intranet_test?sslmode=disable
go test ./...                                    # all packages
go test ./internal/users/...                     # one package
go test ./internal/users/... -run TestCreate      # one test
```
Every package touching Postgres (all but `auth`, `config`, `mail`, `httpapi`, `library`,
`userfiles`) runs against a real test database — no mocked data access, since Postgres
constraints (exclusion, foreign keys, uniqueness) are part of what's under test.
`internal/testutil.NewTx`
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

Per-person documents (a scanned DNI, a signed waiver...) shown at `/users/{login}/files`:
drop files into `./user-files/<login>/` next to `docker-compose.yml` (same bind-mount,
auto-create, gitignored, no rebuild/restart story as `./library` above), one subdirectory
per login.

## Architecture

**Layers**: `backend/internal/httpapi` (router assembly, `ClientIP`, request logging,
panic recovery, `WriteJSON`/`WriteError`) → one package per domain resource (`users`,
`roles`, `memberships`, `licenses`, `board`, `activities`, `inventory`, `tokens`,
`accounting`), each
split into `model.go` / `repository.go` (pgx) / `service.go` / `handler.go`. The domain
layer never imports `net/http`; only `handler.go` knows HTTP exists. Each resource package
owns and registers its own routes via `RegisterRoutes(mux)`, wired together in
`cmd/api/main.go`. **`internal/library`** (and **`internal/userfiles`**, see below) is the
one exception to this shape: it serves a read-only document tree from disk
(`config.LibraryRoot`), not Postgres, so it has no `repository.go` — `service.go` works
directly against the filesystem, resolving and
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
The same session token is also mirrored into an `HttpOnly`/`Secure`/`SameSite=Lax` cookie
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
(identity+roles resolved from context, no extra DB round trip — the frontend calls this
once per page load since an opaque token has nothing to decode). The magic-link token
itself (`auth.NewMagicLinkToken`, 128 bits/16 bytes) is deliberately smaller than a
session or impersonation token (`auth.NewOpaqueToken`, 256 bits/32 bytes) — it's the one
opaque token in this app that ends up in a URL rather than only ever sent as a header, so
its length actually matters, and it only needs to resist guessing within `MagicLinkTTL`'s
own short window rather than staying a valid bearer credential for weeks; 128 bits is
still the OWASP-recommended *minimum* for a session identifier, astronomically out of
reach for brute-forcing in that window given `POST /auth/magic-link/consume` has no
rate limit of its own (unlike the login code below, capped by a per-row attempt
counter) — token entropy alone is what carries this endpoint's security. A magic-link
email also
carries a 6-digit login code — hashed alongside the link's token in the same
`magic_link_tokens` row, so consuming either one consumes both — for signing in on a
device other than the one the email was read on; `POST /auth/magic-link/consume-code`
identifies the caller by email+phone (the code alone is too short to look someone up by)
and a per-row attempt counter bounds brute-forcing it without affecting the link itself.
That lookup runs before the attempt counter is ever reached, though, so — unlike
`/auth/magic-link/consume` above, which needs none — this route shares the same
per-calling-address `httpapi.IPRateLimiter` as the initial `POST /auth/magic-link` request
(`authapi.Handler`'s own `ipLimiter`, one limiter/budget covering both routes): without it,
an address could scan through many different email/phone guesses here with nothing to slow
it down, the same gap the initial request's own throttle exists to close.
Both this and the initial `POST /auth/magic-link` request resolve email+phone through the
same `users.Repository.FindActiveByEmailAndPhone`, whose email side matches
case-insensitively — the same as `internal/telegrambot`'s own `emailMatchesUser` check, so
the same address behaves the same way whether someone signs in from the web or from
Telegram.
Whoever's linked their Telegram account (see **Telegram** below) gets the same link and
code by DM too, alongside — never instead of — the email.

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
number), both called from `Create` and `Update` and both returning `ErrDuplicate` on a
hit. Neither an individual email nor an individual phone may belong to two different
people at once, even though either field can hold several — sharing either would make a
lookup meant to resolve to exactly one person land on whichever one happens to come back
first instead: `FindActiveByEmailAndPhone` for email, `internal/telegrambot.Bot`'s own
phone-based account resolution (`users.Repository.FindByPhoneNumber`) for phone.

**Permission tiers**: `admin` has full read/write access everywhere, including a person's
core data and role assignment. `board` can read everything (the full `User` record,
licenses, roles) but can only write activities and material — every person-data,
role-assignment, license, and Junta Directiva write endpoint rejects board even though it
passes the baseline check. `member` gets `MemberListing` instead of `User` from
`GET /users` (name, gender, computed `age_category` — never the exact `birth_date` it's
bucketed from — membership number, alta/baja dates, and `license_type` — just the
type, never `license_issued_date`/`license_valid_until`; no DNI or contact info) and may
only edit their own contact fields (`email`, `phone`, `address`,
`postal_code`, `city`, `province`, `emergency_contact`) via `PATCH /users/{login}` —
anything else in that body gets 403 `restricted_fields` instead of being silently ignored.
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
already uses for its own type-specific fields. Metadata lives in Postgres
(`Repository`); a file resource's bytes live on disk instead
(`Storage`, `config.ActivityResourcesRoot` — a *named Docker volume*, not a host bind
mount like `library`/`userfiles`, since files arrive exclusively through
`POST /activities/{id}/resources` rather than being dropped in by hand), keyed by the
row's own id rather than any caller-given path, so unlike `library`/`userfiles` there's no
directory tree to browse and no path-escape risk to guard against. That same `POST` is
always `multipart/form-data`, even for a text resource (just one with no file part), since
a file resource has to carry an actual upload — capped at 25 MiB
(`activityresources.maxUploadSize`). Every resource has a `position`, a dense `1..N` per
activity that a fresh one is appended after; `PUT /activities/{id}/resources/order`
replaces the whole order at once from a caller-given list of every resource id (rejecting
anything short of an exact match, `reorder_mismatch`, rather than reconciling a partial or
stale one) instead of a move-this-one-item endpoint that would have to shuffle the rest
itself. Reading (`GET`) is open to any baseline role, same as the rest of an activity's own
data; contributing (`POST`, the reorder `PUT`) requires admin/board, the activity's own
organizer, or anyone signed up in `activity_participants` (`Handler.requireContributor`) —
note the organizer isn't automatically a participant row unless they happened to list
themselves when creating the activity (see `activities.Repository.Create`), so this checks
`Activity.LeaderID` *and* `activities.Service.IsParticipant` separately, not just the
latter; editing or deleting a specific resource (`PATCH`/`DELETE`) is narrower still —
only that resource's own author, or admin/board, even for someone else who took part in
the same activity.

**Licenses**: a license's type isn't free text — it's a `license_type_id` referencing the
`license_types` catalog, seeded in the database (migration `00007_licenses.sql`) rather
than a Go enum, so the list can grow with an `INSERT` instead of a deploy. Its `is_other`
escape-hatch entry ("Otra") requires an `other_detail` string saying which license it
actually is; every other entry forbids one (shared validation:
`licenses.normalizeOtherDetail`, used by both `Create` and `Update`).
`PATCH`/`DELETE /licenses/{id}` (admin-only) correct or remove an assigned license without
re-checking membership status the way `POST` does — that gate is for assigning a
*new* license, not fixing a typo on one someone already has. `POST`'s own gate
(`memberships.Repository.WasMemberOn`) checks membership as of the license's own
`issued_date`, not "right now" — assigning, after the fact, a license from a period when
someone genuinely was a socio must still work even if they've since caused baja; only a
date that never fell inside any of their membership periods is rejected
(`ErrNotActiveMember`, HTTP 409 `not_active_member`). A license also has its own `Year`
(migration `00014_membership_dues_and_license_payments.sql`) — the calendar year it
actually covers, *not* always the same as `issued_date`'s own year, since a license
requested in December often covers the *following* year — and a required
`TransactionID` naming the transaction that recorded its payment (see Accounting below):
required at creation (`ErrUnknownTransaction`, 400 `unknown_transaction`, if it doesn't
name a real transaction), and never itself editable afterward — `UpdateInput` deliberately
has no `TransactionID` field, since correcting a license's type, date, or year is a
different action from re-litigating which payment it belongs to (the same distinction
`accounting.UpdateAccountInput`'s missing `Position` field draws).

**Membership dues** (`membership_dues`, alongside `memberships`): every year a socio
remains a member, they owe a cuota to cover it — the first one (alongside the one-time
matrícula) the same day they join, then once more each following year for as long as they
stay a socio. `memberships.Due` is one row per `(user_id, year)` covered
(`UNIQUE`, `ErrDueAlreadyRecorded` on a second one for the same year), always tied to the
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
`GET /users/{login}/dues` mirrors licenses' own self-or-admin/board read split. There's no
endpoint to correct or remove a due, and no validation that a due's `year` actually falls
within an active membership period — both deliberately out of scope for now.
`membership_dues.transaction_id` cascades on delete (`ON DELETE CASCADE`) — a due is pure
bookkeeping metadata with no meaning apart from its transaction — while
`licenses.transaction_id` uses the default `ON DELETE RESTRICT` instead: a license is a
real, independently meaningful record (proof of federation) even apart from its payment,
so the transaction that paid for one can't be deleted out from under it at all.

**Document library** (`internal/library`): serves club documents (estatutos, reglamentos,
actas...) read-only from a plain directory tree bind-mounted from `./library` on the host
— see Commands above — not the database. `GET /library?path=...` lists a directory
(subdirectories first, then files, both alphabetical; dotfiles never listed);
`GET /library/file?path=...` streams a file's raw bytes, inline by default or as an
attachment with `?download=1`, with HTTP Range support via `http.ServeContent`. Both are
open to any baseline role and read-only — nothing here lets a caller upload, rename, or
delete anything.

**Per-person files** (`internal/userfiles`): the same read-only, filesystem-backed model as
the document library, one subdirectory per login instead of one shared tree — a scanned
DNI, a signed waiver, medical paperwork. `GET /users/{login}/files[?path=...]` and
`GET /users/{login}/files/file?path=...` mirror `/library`'s own listing/streaming shape
exactly (including `?download=1` and Range support), but are *not* open to every baseline
role: only that person themselves, or admin/board, may see them (403 otherwise) — checked
inline in the handler (`Handler.resolveAccess`), the same self-or-privileged pattern
`memberships.Handler.listByUser`/`inventory.Handler.listMovementsByUser` use, since who may
see a specific person's files isn't a fixed role list the way `/library`'s is. A login with
no subdirectory of its own yet — most people, until someone drops a file in for them — gets
an empty listing at their own root rather than a 404.

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
not built here. Six things currently talk to Telegram, all independent of each other.
Five are fire-and-forget (their own goroutine, a bounded timeout, a background context
detached from the request/statement that triggered them — so a slow or unreachable
Telegram API never adds latency anywhere); startup is the one exception, described below:
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
- `authapi.Handler.notifyMagicLinkViaTelegram` DMs a sign-in link to whoever has
  linked their Telegram account (`users.Repository.TelegramChatIDForUser`). Whether the
  email `issueMagicLink` (the shared core both `sendMagicLinkIfEligible`, for the web
  endpoint, and the exported `IssueMagicLinkForTelegramUser`, for
  `internal/telegrambot.Bot` above, resolve their target person into before calling)
  also sends depends on which of those two callers triggered it (`issueMagicLink`'s own
  `viaTelegramBot` bool, threaded through): riding along a web request that merely
  happens to also have Telegram linked, the DM is sent alongside the email, not instead
  of it, so someone with no Telegram linked (or a failed lookup/send) still has the email
  to fall back on — but when it's the bot's own `IssueMagicLinkForTelegramUser` replying
  to someone who just typed their email into the chat (see `handleRecognizedChat`
  below), `issueMagicLink` returns right after the DM and skips the email outright: that
  DM already *is* the answer to what they just typed, so a follow-up email would only be
  redundant, not a fallback worth having. Wording matches this split: the email, and a
  web-triggered DM, both include the login code, a "recibes este mensaje porque..." line
  naming the login page as this request's origin (the `requestOrigin` constant, since
  neither of those two is ever built for a bot-triggered request), and a "if you didn't
  request this, ignore it" line — since either might be read on a device other than the
  one signing in, or simply wasn't expected — while the bot-flow DM drops all three: no
  code-entry page is open anywhere for that code to be useful for, and asking "was this
  you?" makes no sense for a message that *is* the direct reply to what they themselves
  just typed, not something that could show up unprompted. `IssueMagicLinkForTelegramUser`
  is also the one caller where email genuinely can be absent on file at all —
  `FindActiveByEmailAndPhone` guarantees one for the web path, but resolving straight
  from a linked chat id doesn't — though that's beside the point for the skip above,
  which applies to every bot-triggered request the same way regardless of whether one's
  on file. Unlike
  `notifier`/`notifyLogin` above, which always target the fixed log channel, this needs
  `authapi.TelegramMessenger` (`*telegram.Client` satisfies it structurally) to DM
  whichever `chat_id` that specific person has — a second dependency alongside
  `notifier`, wired from the same `botClient` `cmd/api/main.go` also gives
  `internal/telegrambot`, and `nil` (not gated behind a separate config check) whenever
  Telegram isn't configured at all, disabling this DM outright rather than falling
  through to some no-op default. The chat id lookup runs synchronously on the
  request's own context, same reasoning as `notifyLogin`. For a web-triggered DM, only
  the network call itself is deferred to a goroutine, fire-and-forget — any failure (the
  lookup, or the send) is logged and otherwise swallowed, since the email is this path's
  own real fallback regardless of whether the DM goes through. A bot-flow DM
  (`viaTelegramBot`) has no such fallback — `issueMagicLink` skips the email entirely for
  it — so every step, send included, runs synchronously instead and reports a failure
  anywhere along the way as `errMagicLinkTelegramDMFailed`, all the way back through
  `IssueMagicLinkForTelegramUser` to `Bot.handleRecognizedChat`, which replies with
  `magicLinkDeliveryFailedMessage` instead of leaving the person with silence (previously
  this was only ever logged, a genuinely silent dead end for whoever just proved their
  email to the bot). Running synchronously here is no different from every other call
  `Bot.handle`'s own call chain already makes straight to the Telegram API
  (`SendMessage`/`SendMessageRequestingContact`/`DeleteMessage`, all synchronous) — `Run`
  processes one update at a time with no per-update goroutine of its own, so blocking
  briefly on this one call is already this bot's normal way of working, not a departure
  from it. The delete itself is still deferred to its own goroutine, and only ever
  scheduled at all for a bot-flow DM once its (now synchronous) send has actually
  succeeded: sends via `Client.SendMessageForID` (unlike plain `SendMessage`, this hands
  back the sent message's own id), sleeps for
  `Options.TelegramBotDMLinkTTL` (5 minutes by default, `config.Config`'s own
  hardcoded default — deliberately much shorter than `MagicLinkTTL`, the link's real,
  unaffected expiry: typing an email straight into the bot happens live in the chat, so a
  tight window reads as more trustworthy than the same span an email — read who knows
  when — gets, and this message's own wording states that same shorter number, not
  `MagicLinkTTL`), then calls `Client.DeleteMessage` on it. Purely a hygiene nicety, not a
  security gate — the link inside keeps working for the rest of the real `MagicLinkTTL`
  regardless of whether this delete succeeds, or even runs at all (a process restart in
  between just loses it, the same as any other best-effort cleanup in this app) — but a
  dead link lingering in the chat is misleading, and one more thing exposed for no reason
  if the phone/session is compromised later. A web-triggered DM (`!viaTelegramBot`) skips
  this scheduling entirely and is never auto-deleted: unlike the bot-flow DM, it has no
  equivalent moment where it's definitely done being useful — the code in it, especially,
  might still be typed into the login page from a different device well after it arrives.
- `httpapi.NotifyAsync`/`DiffFields` (`internal/httpapi/audit.go`) are the shared pieces
  every resource package's own mutation notifications are built from. `NotifyAsync` is
  the fire-and-forget goroutine/timeout dance every Telegram send in this app already
  used, pulled out once so nobody duplicates it. `DiffFields(before, after, fields...)`
  reflects over a *named* subset of a struct's fields — never every field on it, since a
  derived/computed one (a computed role list, a membership date recalculated elsewhere)
  must never show up as a spurious "change" just because it was recomputed at a slightly
  different moment than "before" — comparing the pair and returning one "field: old →
  new" line per field that actually differs (a nil pointer formats as `(vacío)`, a date
  as a plain `YYYY-MM-DD`). Every resource package with a mutating route (`users`,
  `roles`, `tokens`, `memberships`, `licenses`, `activities`, `inventory`, `board`,
  `activityresources`, `accounting`) takes an `httpapi.MutationNotifier` in its own `NewHandler` —
  wired to `telegramSender` directly from `cmd/api/main.go`, not gated behind a "don't
  spam every single request" check the way the predecessor of this whole thing needed,
  since one message per meaningful action isn't noisy the way one per every mutating
  request (a routine token touch included) was — and sends its own specific message once
  a create/update/delete actually succeeds: who did it (`actorLogin`, resolved from the
  session's own id through that package's existing `UserLookup`, falling back to
  `user#<id>` rather than failing the whole request if the lookup itself errors) and what
  changed. An `update` handler fetches the record via `Service.Get` *before* applying the
  patch and diffs it against the result using `auditableFields` — a package-level var
  whose names exactly match that endpoint's own `UpdateInput` — so nothing that endpoint
  can't actually touch can appear as a spurious change; an edit whose diff comes back
  empty (every field resent unchanged) sends nothing, rather than an "edited" message
  with nothing in it. A `delete` handler fetches the target the same way, purely so it
  can still be named once it's gone. This whole mechanism replaced an earlier
  `httpapi.NotifyMutatingCalls` middleware that sent a blanket "[API] `<method>` `<path>`
  -> `<status>`" for literally every write regardless of what it actually changed — every
  route it covered now has a strictly more informative replacement, except `authapi`'s
  own session-lifecycle routes (magic-link request/consume, logout, impersonate/exit),
  which were never business-data mutations and already have their own dedicated
  notifications elsewhere in this list.
- `internal/telegrambot.Bot`, the one thing here that *receives* from Telegram rather
  than only sending: `Run` long-polls `Client.GetUpdates` (chosen over a webhook —
  no public HTTPS endpoint to expose or coordinate with a reverse proxy, works the same
  behind NAT or with no domain at all) in a loop started with `go bot.Run(ctx)` from
  `cmd/api/main.go`, gated on the same `telegramConfigured` flag (both
  `TELEGRAM_BOT_TOKEN`/`TELEGRAM_LOG_CHAT_ID` set) as `telegramClient` above — every linking
  attempt has to reach the log channel, so a bot with no channel configured can't do its
  job anyway. A failed `GetUpdates` call (Telegram's own long-poll connection dropping or
  timing out for a moment — near-daily in practice, almost always resolved by the very
  next attempt) is logged at `WARN`, below `telegram.LogHandler`'s own forwarding
  threshold (`slog.LevelError`, see below) — only once failures actually repeat
  (`getUpdatesFailureLogThreshold` consecutive ones) does `Run` escalate to `ERROR`, and
  only for that one call, not for every one of however many more follow — a longer real
  outage doesn't re-notify the log channel on every 5-second retry for as long as it
  lasts. `users.Repository.UserIDForTelegramChatID` tells apart a chat this
  doesn't recognize yet — every other message from one gets a greeting plus a "share your
  contact" keyboard button (`Client.SendMessageRequestingContact`, sent with
  `is_persistent: true` so the client shows that keyboard open by default instead of
  collapsed behind its own keyboard-icon toggle; the greeting text also spells out where
  that button is and how to reveal it if a client doesn't honor the flag, since nothing
  about a Telegram custom keyboard is otherwise self-explanatory to someone who's never
  seen one) — from one it does:
  an already-linked chat no longer gets the contact-share menu again, but a message from
  one doesn't *instantly* trigger a sign-in link either — that was tried and reverted as
  a security regression: it reduced logging in via Telegram to merely having the phone/
  session (whoever's holding an already-open Telegram app), dropping the "must know
  *both* phone and email" requirement the web flow enforces. Being linked doesn't imply
  being eligible to sign in at all, either — `canUseTelegramLogin` gates both this and the
  link confirmation below on the same baseline-role check every route in this API
  enforces (`auth.HasBaselineRole`, in practice an active socio via the automatic
  "member" role, or admin/board) plus having both an email and a phone on file: the web
  magic-link request already requires both as input just to find a match
  (`FindActiveByEmailAndPhone`), but a Telegram-linked account is resolved straight from
  its chat id, so nothing there guarantees either is actually on file the way
  `issueMagicLink`'s own tolerance for a missing email (see below) might suggest. Someone
  who fails this check never gets asked for their email — every message from them instead
  gets `unauthorizedMessage` ("este bot está disponible únicamente para usuarios
  autorizados. Si crees que deberías tener acceso, ponte en contacto con el club para
  comprobar tu situación"). Otherwise, `Bot.handleRecognizedChat` asks a first factor be
  proven a second time: it replies with `emailPrompt` ("si quieres entrar en la intranet,
  envíame tu dirección de correo electrónico") to any message that isn't the account's
  own registered email (`emailMatchesUser`, checked case-insensitively against the same
  space-separated multi-email convention `FindActiveByEmailAndPhone` uses) — but
  `emailAttemptReply` gives a specific, actionable reply for *why* a given message didn't
  match instead of that same generic prompt every time: `nonTextEmailReply` for an
  attachment instead of text (an empty `Text` always means something that isn't text —
  Telegram never sends a real text message with an empty body), `onlyEmailReply` for more
  than one word, `notAnEmailReplyFormat` for a single word with no "@" at all (repeating
  the word back), and `unknownEmailReplyFormat` for something shaped like an email that
  just isn't one of this account's own (repeating the address back, plus a "contact the
  club if you think this is wrong" suggestion) — only a genuine single-word match skips
  all of these and proceeds. Once it matches, it
  calls `Client.DeleteMessage` — Telegram lets a bot delete a message it received in a
  private chat — to scrub that email out of the chat history for whoever might get hold of
  the phone/session afterward (best-effort: a failed delete, e.g. an old message past
  Telegram's 48h window, still lets `magicLinkIssuer.IssueMagicLinkForTelegramUser` go
  through), then finally issues the fresh sign-in link exactly as if they'd requested one
  from the web (see `authapi.Handler` below).
  `magicLinkIssuer` is a local interface (`*authapi.Handler` satisfies it structurally,
  the same pattern as `botClient`) wired in from `cmd/api/main.go`, which is also where
  `authHandler` is hoisted into its own variable instead of being built inline in
  `httpapi.Routes`, purely so both the router and the bot can share the one instance. A
  shared contact's phone number is matched
  against `users.phone` via `users.Repository.FindByPhoneNumber` — not quite the
  exact-digits match `FindActiveByEmailAndPhone` uses for magic-link requests, because
  Telegram always includes the country code (`"34611222333"`) while a phone on file here
  never does (see `CreateInput`/every `SEED_ADMIN_PHONE` default), so digits must equal a
  candidate exactly *or* equal Spain's `"34"` prepended to one. Deliberately not a
  same-length-agnostic suffix match: that also matched a French `"+33666777888"` against
  a Spanish `"666777888"` on file (every digit but the country code is shared), letting
  anyone claim to be whoever's number happened to end the same way. Before any of that,
  `isOwnContact` checks the shared contact is actually the sender's own — Telegram lets
  a message carry *any* contact card, not just the sender's (a manually typed vCard, or
  someone else's saved contact), and only the "share contact" button this bot itself
  sends guarantees `Contact.UserID` equals the sender's own `From.ID`. Skipping this
  check was a real vulnerability, found and fixed after shipping: sharing a real socio's
  phone number in someone else's name linked *that socio's* account to the *sender's*
  chat, handing the sender that socio's magic-link/login code from then on — an
  impersonation path, not just a data-quality one, the same shape as the
  `FindByPhoneNumber` country-code issue above. A mismatch gets a rejection reply and its
  own `Bot.notifyChannelRejectedForeignContact` warning (worth flagging above a routine
  no-match, since it's a possible impersonation attempt) and goes no further — the "share
  contact" keyboard stays up so they can retry with their real one. A match calls
  `users.Repository.SetTelegramChatID` and confirms via `Bot.replyToLinkedContact`, which
  checks the same `canUseTelegramLogin` eligibility gate right away rather than waiting
  for a later message to find out (`emailPrompt` follows the confirmation as a second
  message for someone actually eligible; someone who isn't gets a single combined reply
  instead — `unauthorizedAfterLinkReplyFormat`, "¡Gracias! Te hemos identificado como
  Nombre Apellidos. Sin embargo, este bot está disponible únicamente para usuarios
  autorizados. ..." — rather than "you're linked" followed by "but you can't use this" as
  two separate messages with nothing useful in between); no match replies saying so, plus
  `contactClubHint` ("si crees que
  deberías tener acceso, ponte en contacto con el club para comprobar tu situación" — the
  same suggestion `unauthorizedMessage` ends with) since a phone genuinely not on file is
  exactly the kind of thing worth a human checking. Both the match and no-match replies
  use `Client.SendMessageRemovingKeyboard` instead of a plain `SendMessage`, since it
  really was their own contact either way and tapping the button again could only ever
  resend that same phone number for the same answer — unlike the rejection case above,
  where the button staying up is the point. Either way, `Bot.notifyChannel` reports the
  attempt to the log channel — who sent it,
  the phone number, and whether it matched — telling the club about every attempt, not
  just successful ones. `users.User.TelegramChatID` is deliberately left
  out of `userSelect`/`scanUser` (the query every other `users` method uses) since
  nothing else needs it yet — checking or reading it always goes through
  `UserIDForTelegramChatID`/`FindByPhoneNumber` directly, never through a generic `Get`.
  A link made this way is only as trustworthy as the phone number it was matched
  against, so `users.Repository.Update` clears `telegram_chat_id` back to `NULL`
  whenever a `PATCH` actually changes `phone` (including clearing it to nothing) —
  otherwise a chat linked to an old number would keep resolving to the person who
  used to have it. Sending the same phone back, or a `PATCH` that doesn't touch
  `phone` at all, leaves an existing link alone (`stringPtrsEqual`, treating two
  `nil`s as equal); only an actual change to the value clears it.
- `cmd/api/main.go` sends "🚀 API started" once, right before `http.ListenAndServe` —
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
literal `""`s would), and an optional `parent_id` nesting it under a
broader one — the frontend builds any tree view it wants client-side from that) and,
against that chart, `transactions`: every real movement of money the club records, each
made of two or more `entries` (an `account_id` and an `amount`, positive into that
account or negative out of it) whose amounts always sum to exactly zero — standard
double-entry bookkeeping, so a transaction always says both where money came from and
where it went, never just one side of it. `GET /accounts` also joins in each account's
own direct `balance` (summed from every entry ever posted straight to it) and
`last_transaction_at` (the most recent one's `occurred_at`) — never rolled up from a
child account, and never present on any other `accounts` route (`GET`/`POST`/`PATCH
/accounts/{id}` are all just the row itself, no extra join); a `LEFT JOIN` against
`transaction_entries`/`transactions` means an account with no entries at all still gets
one row back rather than being silently missing, `COALESCE`d to a zero `balance` (a
real, computed zero — never absent the way it is everywhere else `Account` shows up)
while `last_transaction_at` is simply `NULL`. "At least two entries" and "sums to zero" both
depend on every sibling row, not just one, so neither can be a plain `CHECK` — both are
enforced instead by `transaction_entries_balance`, a `DEFERRABLE INITIALLY DEFERRED`
constraint trigger (migration `00012_accounting.sql`) that only actually runs at commit,
after every entry a write touched is in place, rather than rejecting a transaction's
entries one at a time as `accounting.Repository.CreateTransaction`/`UpdateTransaction`
insert them individually inside their own DB transaction (the same `pool.Begin` pattern
`activities.Repository.Create` already uses for an activity's initial participants).
The trigger explicitly skips its own check once the `transactions` row itself is also
gone (checked first, before counting/summing entries) — deleting a whole transaction
cascades to every one of its entries, each of which fires this same trigger, and without
that guard the last entry to go would look like "a transaction with zero (too few)
entries" instead of a transaction that's correctly, completely gone.
`Repository.CreateTransaction`/`UpdateTransaction` don't just rely on the deferred
default, though: right after inserting entries they run `SET CONSTRAINTS
transaction_entries_balance IMMEDIATE` on their own DB transaction to force the check
right then (mapping a violation to `ErrUnbalanced` from that call instead of a later
`Commit`), then set it back to `DEFERRED` before returning — necessary because
`pool.Begin` on top of an already-open `pgx.Tx` (exactly what every test does, via
`testutil.NewTx`) opens a *savepoint*, not a real nested transaction, and Postgres only
ever fires a deferred trigger at a real `COMMIT`, never at a savepoint's own `RELEASE` —
so without forcing it immediate, the check would silently never run at all under test,
and resetting back to `DEFERRED` afterward matters too, since `SET CONSTRAINTS`'s effect
otherwise outlives the savepoint that set it (unlike a `ROLLBACK`, a successful `RELEASE
SAVEPOINT` doesn't undo it), which would make the *next* multi-row entry insert
anywhere else in the same enclosing transaction fail spuriously, checked one row too
early. `UpdateTransaction` corrects a transaction by replacing its description,
`occurred_at`, and its *entire* set of entries at once (delete every existing one, insert
the given set) rather than editing a single entry in place — there's no single-entry
edit endpoint. `occurred_at` is a real timestamp, not just a date the way
`activities.start_date`/`licenses.issued_date` are, so it decodes straight into a Go
`time.Time` (RFC 3339, `encoding/json`'s own default) rather than needing the manual
`time.Parse` every plain-date field elsewhere needs.
Reading (`GET /accounts`, `GET /transactions`, and their `/{id}` forms) is open to any
baseline role — the club's own finances are something every socio can see, not just
admin/board. Writing needs admin or the optional, non-system "money" role (seeded by
migration `00012_accounting.sql`, deletable by a club that doesn't want the distinction),
the same "admin plus one optional custom role" shape `material` already has for
inventory movements — board itself gets no special write access here, same as it has
none for licenses or a person's own data. Every write still goes through the same
baseline-role floor as every other route: holding only `money` with none of
admin/board/member is rejected everywhere, this included.
Every account also has a `position` (migration `00012_accounting.sql`) — its
manual display order among its own siblings (same `parent_id`, `NULL` counted together
as one top-level group), not unique or gap-free on purpose, since it's only ever used to
`ORDER BY`, never to identify a row. `CreateAccount` appends a new account after its own
sibling group's current highest position, the same "`MAX(position) + 1` in the same
`INSERT`'s own subquery" pattern `activityresources.Repository.Create` uses for a
resource's position within its activity. The only way to actually change one afterward
is `POST /accounts/{id}/move`, admin/money-gated same as every other accounting write:
`{"direction": "up"}` swaps it with the sibling immediately before it, `"down"` the one
immediately after — a harmless no-op, not an error, if there's no such sibling (already
first/last within its own group) — and answers with the whole, freshly reordered `GET
/accounts` list (same "return the full list after reordering" shape the memoria's own
reorder endpoint uses) rather than just the moved account, so a caller can re-render
straight from the response. `GET /accounts` itself walks the tree in pre-order,
depth-first: every top-level account in its own `position` order, each immediately
followed by all of its own descendants (recursively, each in their own `position` order,
however deep) before the next top-level sibling — `Repository.ListAccounts`'s own
`tree` CTE computes this via the standard "materialized path" trick (an array of every
`position` from a real root down to each account; Postgres compares arrays
element-by-element, so ordering by it reads out exactly that walk) rather than assembling
the tree in Go. Anchored only at genuine roots (`parent_id IS NULL`), that walk can never
loop forever even against a cycle somehow already among non-root accounts — nothing in
such a cycle has `parent_id IS NULL`, so nothing in it could ever seed the walk to begin
with; it would instead just silently vanish from the listing, which is exactly why
`UpdateAccount` rejects (`ErrCircularParent`, 409 `circular_parent`) setting a
`parent_id` that is, or descends from, the account being edited — checked by walking
*up* from the proposed parent (a query genuinely capable of looping forever against
pre-existing bad data, unlike the listing's own downward walk), so that one's own
recursion is capped at a depth no real chart of accounts would ever approach, purely so
it can't hang even then.
`GET /accounts/{id}/transactions` is one account's own ledger — every `AccountEntry` it
was ever posted against (not the same shape as a bare `Entry`: each one inlines its own
transaction's `description`/`occurred_at`, and a `balance_after` instead of just an
`amount`), newest first same as `GET /transactions` itself.
`Repository.ListAccountEntries` computes that running balance in a single pass, but has
to do it oldest-first internally (a running balance only ever makes sense walked forward
through time) and reverse the whole slice once at the end — the opposite order from how
it reads out. Open to any baseline role, same as every other read in this package;
`Handler.listAccountEntries` checks the account actually exists first (404, not just an
empty list) since `ListAccountEntries` alone can't tell "no such account" apart from "a
real account nobody's ever posted against."

**Testing helpers**: `internal/testutil` provides `NewTx` (migrated, isolated
transaction), `InsertUser`/`InsertActivity`/`RoleID`/`LicenseTypeID` fixtures (the
`TestUser` `InsertUser` returns carries a `Login` too, for tests exercising a
login-addressed route), and
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

**Migrations** (`db/migrations/`, goose-numbered): kept squashed to the *current* schema
shape, not the historical sequence of changes — no renamed columns, no lossy "down"
transformations, no comments about what something used to be called. When schema work
piles up several incremental migrations, squash them back into the relevant per-resource
file(s) and verify schema-equivalence by applying the old and new sets to throwaway
databases and diffing `pg_dump --schema-only` output, rather than leaving the ALTER trail
in place.

**Frontend** (`web/`): classic multi-page site (no SPA, no build step — a deliberate
choice, not just an unexamined default: every page already revalidates its own session
and re-fetches its own data on load, so there's no cross-page state an SPA would actually
save, and this stays free of a bundler or any client-side router), one `<page>.html` +
`js/<page>.js` per view, Caddy's `try_files {path} {path}.html` making `/members` resolve
to `members.html`. The sidebar and topbar, identical on every authenticated page, are
rendered once from `js/layout.js`'s `initPage()` rather than copy-pasted into each
`<page>.html` — every one of them reduces to an empty `<aside id="sidebar">`/
`<header id="topbar">` pair for it to fill in. `initPage()` is also each page's own
session bootstrap (in place of what used to be five near-identical lines re-typed at the
top of every `<page>.js`): it validates the session, applies role-based visibility, and
wires the sidebar toggle, topbar greeting, and logout button, returning the signed-in user
same as `currentUser()` would. Its own `requireAnyRole` option additionally redirects home
if the signed-in user holds none of the listed roles — `member.js`/`accounting.js`'s own
whole-page gates, tighter than the ordinary baseline check every API route already
enforces. Which sidebar link reads as "active" is computed from `location.pathname`
(`ACTIVE_ALIASES` mapping a detail page like `/activity` or `/member` to the listing it
was reached from, `/activities`/`/members`) rather than a per-page `class="active"`
written by hand — one less thing a new page could forget. `login.html` is the one page
with no shell and no session yet, and never calls `initPage()`.

Every "create X"/"edit X" form is a `<dialog class="panel modal">` (`.panel` already
gives it the right background/border/padding/spacing — `dialog.modal`'s own
`css/style.css` rules only add what a plain `.panel` never needed: the dimmed
`::backdrop`, a max-width, and the "×" `.modal-close` button's own positioning) — never a
`<div>` toggled to `hidden="false"` in place on the same page the way every one of these
used to work. `js/modal.js`'s `initPage`-sized `initModal(dialog, form?)` is the one call
each page makes per dialog: it wires every `[data-modal-close]` element inside (the "×",
a form's own "Cancelar") and a click that lands on the dialog element's own box rather
than bubbling from its content (the standard way to detect a backdrop click on a native
`<dialog>`) to `dialog.close()`, and — when a `form` is given — resets it on the dialog's
own `close` event, which fires no matter *how* it closed (×, Cancelar, backdrop, Escape,
or a successful save that calls `close()` itself once done), so a caller's own submit
handler never resets the form itself. Opening one is always page-specific and left to the
caller: a static "+ Nueva X" button calling `showModal()` directly, or — for a dialog
shared by every row of a list rather than one pre-rendered per row (`accounting.js`'s own
account edit, `account.js`'s transaction edit, three of `member.js`'s timeline dialogs) —
an `open*Dialog(record)` function that fills the shared dialog's fields from that one
record first. A dialog that's actually a *correction* to an existing record (`member.js`'s
own "Datos", populated once by `loadMember()` and never re-opened with fresh values the
way the shared timeline dialogs are) deliberately skips the `form` argument to
`initModal` — resetting it back to blank on every close would be wrong for one of these,
unlike every "create new X" dialog that wants exactly that. The platform itself is what
actually makes the rest of the page unreachable while one is open (real modality, not a
hand-rolled overlay to keep in sync) and closes it on Escape — nothing in `modal.js` or
any page's own script needs to implement either. `profile.html`'s own "Mis datos" is the
one exception left as a plain inline form: unlike every page above, editing your own data
*is* this page's entire reason to exist, not one form competing for attention alongside
several other unrelated sections, so hiding it behind a button would only cost a click for
no corresponding gain in clarity.

`js/api.js` is the
underlying shared client `layout.js` itself is built on: it keeps the session token in
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
`login.html` also offers Telegram as a second way in, alongside (never instead of) the
email/phone form: on load, `auth.js`'s `loadTelegramBotInfo` calls the unauthenticated
`GET /auth/telegram-bot` (`authapi.Handler.TelegramBotInfo`, backed by
`config.TelegramBotUsername`/`TELEGRAM_BOT_USERNAME` — independent of
`TELEGRAM_BOT_TOKEN`/`TELEGRAM_LOG_CHAT_ID`, and not secret the way that token is, since
it's only the public "@name" a `t.me/<username>` link needs) and, only if a username
comes back non-empty, un-hides a note linking straight to the bot's chat — left hidden
by default so there's no dead link (or flash of one) when Telegram isn't configured at
all, or the request itself fails.

`library.js` itself doesn't lean on the session cookie described above (`dashboard.js`'s
own simpler Documentos/Actas/Memorias links do instead) — downloading or previewing a
document from the `/library` browsing UI fetches the file with `apiFetchBlob` (`api.js`)
and works with the resulting `Blob` client-side — an object URL for inline preview, a
synthetic `<a download>` click to save — since a `Blob` is what lets it render Markdown
inline and drive the download from a click handler rather than a bare link, not because
the route itself needs the bearer header specifically. Markdown
documents render through a small dependency-free converter (`js/markdown.js`) that
resolves each document-relative link against the document's own directory and rewrites it
into an in-app `/library` navigation link; HTML documents render in a fully sandboxed
`<iframe>` instead, with no such link rewriting. `library.js` and `userFiles.js` (the
embedded per-person file browser on `/profile`/`/member`) share this same
listing/preview/download logic almost verbatim — `js/fileBrowser.js` pulls out the
common pieces (`joinPath`/`dirOf`/`nameOf`, `formatSize`, `fileKind`, the same
document-relative link resolver, `downloadBlob`) both import, rather than each carrying
its own near-identical copy; `activity.js`'s own memoria-resource downloads import just
`formatSize`/`downloadBlob` from there too, the only two of these it also needed.

`/accounting` (`accounting.js`) manages the chart of accounts from `GET`/`POST`/`PATCH
/accounts` and `POST /accounts/{id}/move` — a tree, indented by depth, of every account's
name, code, direct balance, and last movement date (each name a plain `<a href="/account?
id=…">` into that one account's own detail page, below), with its own controls to create
an account, correct one's name/code/parent (an inline edit panel per row, the same
toggled-`<div class="edit-fields">` pattern `member.js` uses for a membership/board/leave
period), and swap it up or down among its own siblings. `js/accountTree.js` holds the
`childrenByParent`/`flattenAccountTree` pair both this page and `account.js` need:
`GET /accounts` already comes back as a pre-order, depth-first walk (see the `accounts`
bullet under **Accounting** above), so filtering that flat list down to a single
`parent_id`, in the order it already arrives in, *is* that group's own sibling order — one
pass groups every `parent_id` at once, no separate tree-building pass. A `move`'s own
response (the whole reordered list) re-renders directly from `applyAccountsList` rather
than triggering a follow-up `GET`. Only admin and the optional "money" role ever see
`/accounting` in the sidebar at all, or can even land on the page
(`hasAnyRole(user, ['admin', 'money'])`, else redirected to `/`) — tighter than the API
itself, which opens *reading* the chart to every baseline role the same as
`GET /transactions`; admin/money can already write here too, so nothing on the page needs
a narrower check than that one gate.

`/account?id=…` (`account.js`) is one account's own ledger — reached only by clicking an
account's name on `/accounting`, never linked from the sidebar itself, so it shares that
same admin/money gate (and `js/layout.js`'s `ACTIVE_ALIASES` maps it back to
`/accounting` for the sidebar's own highlighting, the same `/activity`→`/activities`/
`/member`→`/members` treatment every other detail-off-a-listing page gets). Renders
`GET /accounts/{id}/transactions` (see **Accounting** above) as a table — date, description,
this account's own entry amount, its running balance after, and per-row Editar/Borrar — and
a "+ Nueva transacción" panel deliberately narrower than the full `POST /transactions`
shape: just a date, a description, an amount, and one other account, always producing
exactly two entries (this account's own, as typed, and the chosen other account's exact
negative — `twoEntryBody`, shared by creating a new transaction and correcting an existing
one). A row's own "Editar" fetches that one transaction fresh (`GET /transactions/{id}`,
never kept in memory alongside the ledger listing, which only ever carries this account's
own side of each one) and only ever offers this same simplified two-field edit — if it
doesn't have exactly two entries (only possible by calling the API directly; this page
never creates one that way), editing is refused with an explanation instead of silently
mis-editing it; "Borrar" (`confirm`-gated, the same pattern `member.js`'s own delete
buttons use) removes the whole transaction, both entries included. The date input only
ever collects a plain date, but `occurred_at` is a real timestamp — sent as that date at
noon UTC (not midnight) specifically so `formatDate`'s own local-timezone getters can never
roll it back to the previous day for anyone viewing from behind UTC; editing an existing
transaction collapses whatever time of day it actually had down to this same convention.
The "otra cuenta" picker (shared by the create panel and every row's own edit panel) lists
every other account (indented the same way `accounting.js`'s own pickers are, via
`flattenAccountTree`) — excluding only this account itself, not its ordinary
parent/children the way `accounting.js`'s own "cuenta superior" picker excludes
descendants: there's no circular relationship to guard against here, any two distinct
accounts can be the two sides of a transaction. The page's own header (name, code, current
balance) and that picker both come from one `GET /accounts` call — that listing already
carries every account's own balance, so there's no need for a second, single-account fetch
just for the header (`GET /accounts/{id}` alone never populates it — see **Accounting**
above).

**API contract**: `backend/openapi/openapi.yaml` is the source of truth for the HTTP
surface — keep it in sync with handler changes (routes, request/response schemas, role
requirements described in each path's `description`).
