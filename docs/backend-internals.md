# Backend internals

The long form of how the backend works, resource by resource: the invariants and the reasons behind
them, which are rarely visible in the code. [`architecture.md`](architecture.md) is the shorter
overview, [`api.md`](api.md) lists the endpoints and [`database.md`](database.md) the
tables; the frontend has its own file, [`frontend.md`](frontend.md). When something changes in a
resource, change its section here.

Contents:

- [Layers and packages](#layers-and-packages)
- [Authentication and sessions](#authentication-and-sessions)
- [Baseline access policy](#baseline-access-policy)
- [Roles](#roles)
- [Login as the only external identifier](#login-as-the-only-external-identifier)
- [Several emails and phones per person](#several-emails-and-phones-per-person)
- [Permission tiers](#permission-tiers)
- [Activities](#activities)
- [Activity resources (memoria)](#activity-resources-memoria)
- [Distributions](#distributions)
- [Distribution articles](#distribution-articles)
- [Licenses](#licenses)
- [Membership dues](#membership-dues)
- [Temporary leaves](#temporary-leaves)
- [Library](#library)
- [Document archive](#document-archive)
- [Per-person files](#per-person-files)
- [Club information](#club-information)
- [Admin status](#admin-status)
- [Telegram](#telegram)
- [Accounting](#accounting)
- [Budgets](#budgets)
- [Transaction files](#transaction-files)
- [Testing helpers](#testing-helpers)

## Layers and packages

**Layers**: `backend/internal/httpapi` (router assembly, `ClientIP`, request logging, panic
recovery, `WriteJSON`/`WriteError`) → one package per domain resource (`users`, `roles`,
`memberships`, `licenses`, `board`, `activities`, `activityresources`, `distributions`, `material`,
`tokens`, `accounting`, `clubinfo`, `budgets`, `transactionfiles`; `adminstatus` is the odd one out,
see below, and `requestcount` is a middleware, not a resource), each split into `model.go` /
`repository.go` (pgx) / `service.go` / `handler.go`. The
domain layer never imports `net/http`; only `handler.go` knows HTTP exists. Each resource package
owns and registers its own routes via `RegisterRoutes(mux)`, wired together in `cmd/api/main.go`.
**`internal/archive`** (and **`internal/userfiles`**, see below) is the one exception to this shape:
it serves a read-only document tree from disk (`config.ArchiveRoot`), not Postgres, so it has no
`repository.go` — `service.go` works directly against the filesystem, resolving and validating every
caller-given path against root before touching disk (`Service.resolve`), since there's no database
constraint to lean on for that instead.

**`db.PGX` interface** (`internal/platform/db`): the subset of `*pgxpool.Pool` a
repository actually calls. Both a pool and a `pgx.Tx` satisfy it, so production code
builds a repository on the pool while tests build the same repository on a transaction
that gets rolled back — the repository code never knows the difference.

## Authentication and sessions

A sign-in finished through an address whose first component is `local`
(`local.intranet.katiuskas.es`, the club's shared tablet) gets a session that expires
`config.LocalTokenTTL` (3 hours, cookie included) after its last use instead of `TokenTTL` (30
days): `Handler.sessionTTL` picks it from `requestHost` (the first `X-Forwarded-Host`, else `Host`,
without port, lower case; `isLocalHost`), and it can only shorten (none configured, or a longer one,
leaves the usual). It is the address that decides, not a device: nothing is stored about one, and a
request cannot ask for a longer session; the same for the session an admin gets back on leaving an
impersonation. The link in the email follows the address too (`Handler.linkBase`): for a request
made through exactly `local.<PUBLIC_BASE_URL's host>` it points there, else to `PUBLIC_BASE_URL`
whatever the `Host` says, so that nobody can forge where an email to somebody else points. See
"Short sessions" in `docs/architecture.md`.

**Auth is a single opaque, revocable, database-backed session token** — no JWT, no separate
access/refresh pair. `internal/auth` only defines `AuthenticatedUser`,
`Authenticator.Protect`/`RequireAuth`/`RequireRole`, and a `SessionResolver` interface; it has no
database dependency. `internal/tokens` is the real resolver: it hashes the raw token, validates and
touches `last_used_at`/`last_used_ip`/`last_used_user_agent` (the address and `User-Agent` the
middleware passes, `Authenticator` → `SessionResolver.Authenticate`) in one query, then resolves
roles *live* (not baked into the token) via `roles.Service`. `tokens` also exposes endpoints to
audit and end sessions: `GET /users/{login}/tokens` and `PATCH …/{tokenID}` (`revoked: true`; a
session is never deleted) are for that person themselves (who sees and closes their own sessions
from `/perfil`, `?status=active`) or admin (`sessionOwner`: anybody else, or a login nobody has, is
a 403 so it can't be used to check which exist; somebody impersonating the person sees their
sessions, as the person would in their profile, but is not them acting for themselves, so can't end
them — the admin leaves the impersonation first (`sessionOwner`'s `ending`); closing one's own says
"ha cerrado una de sus sesiones" in the log channel), while `PATCH` (the expiry), `GET /tokens` and
`POST /users/{login}/impersonate` are admin-only; `GET /tokens` lists everyone's — the active ones
by default, `?status=all` for the history, paged by `limit`/`offset` — which marks the caller's own
session `current`, found by the hash of its bearer token) to audit, re-expire, or revoke any token,
plus `POST /users/{login}/impersonate` (admin-only, issues a short-lived token —
`config.ImpersonationTokenTTL` — that resolves as the target person in every way except
`AuthenticatedUser.ImpersonatedBy`, which names the admin) and `POST /auth/impersonate/exit` to end
one and get a fresh normal token back. `PUT`/`DELETE /auth/role-override` let a real admin make
their *own* session act for an hour with other roles (`tokens.role_override`, migration `00041`) to
see what somebody with them sees, without touching anybody's real roles (which would run into the
automatic roles, the last-admin rule and the risk of locking oneself out): `Service.Authenticate`
still resolves the person's roles live, and answers with those of the override as
`AuthenticatedUser.Roles` while it has not ended (`RoleOverrideUntil`), keeping the own ones in
`RealRoles`. The routes use `RequireAuth` and check `RealRoles` for `admin` themselves, because
under the override the roles in force may not include it; an impersonation session is refused (409
`impersonating`); the roles must exist and include `admin`, `board` or `member` (`no_baseline_role`:
with none nothing could be used). `GET /auth/me` adds `real_roles` and `role_override_seconds_left`
while one is in force. Neither changing the roles nor putting them back is announced on Telegram (an
admin does it often and it only adds noise, unlike an impersonation, which is). The same session
token is also mirrored into an `HttpOnly`/`Secure`/`SameSite=Lax` cookie (`auth.SetSessionCookie`,
name `katiuskas_session`, scoped to `/api/v1`) whenever one is issued or refreshed (sign-in,
impersonation start/exit) — every ordinary route still only ever looks at the bearer header
(`auth.Protect`/`RequireAuth`), so this cookie existing changes nothing about them. It exists purely
for `auth.ProtectAllowingCookie`/`RequireAuthFromHeaderOrCookie`, which `internal/archive` and
`internal/userfiles` use on their own file-serving `GET` routes specifically (nowhere else) so a
document/file can be a plain, browser-navigable `<a href>` link — opened directly in a new tab, or
saved by the browser's own "Save As" with the real filename — instead of needing `apiFetchBlob`'s
fetch-then-`Blob` dance the way `archive.js` otherwise uses for everything else;
`web/js/dashboard.js`'s own Documentos/Actas/Memorias links lean on exactly this to stay plain
links. `ClearSessionCookie` removes it on logout, so a signed-out browser can't open a file link
straight from history or a bookmark anymore. `internal/authapi` glues magic-link
request/consume/logout and `GET /auth/me` (identity+roles resolved from context, one light query for
the login and name — the frontend calls this once per page load since an opaque token has nothing to
decode; the name is there so the topbar's greeting and the impersonation banner don't need a second
request for the person's record, which every page used to wait behind). The magic-link token itself
(`auth.NewMagicLinkToken`, 128 bits/16 bytes) is deliberately smaller than a session or
impersonation token (`auth.NewOpaqueToken`, 256 bits/32 bytes) — it's the one opaque token in this
app that ends up in a URL rather than only ever sent as a header, so its length actually matters,
and it only needs to resist guessing within `MagicLinkTTL`'s own short window rather than staying a
valid bearer credential for weeks; 128 bits is still the OWASP-recommended *minimum* for a session
identifier, astronomically out of reach for brute-forcing in that window given `POST
/auth/magic-link/consume` has no rate limit of its own (unlike the login code below, capped by a
per-row attempt counter) — token entropy alone is what carries this endpoint's security. A
magic-link email also carries a 6-digit login code — hashed alongside the link's token in the same
`magic_link_tokens` row, so consuming either one consumes both — for signing in on a device other
than the one the email was read on; `POST /auth/magic-link/consume-code` identifies the caller by
email+phone (the code alone is too short to look someone up by) and a per-row attempt counter bounds
brute-forcing it without affecting the link itself. That lookup runs before the attempt counter is
ever reached, though, so — unlike `/auth/magic-link/consume` above, which needs none — this route
shares the same per-calling-address `httpapi.IPRateLimiter` as the initial `POST /auth/magic-link`
request (`authapi.Handler`'s own `ipLimiter`, one limiter/budget covering both routes): without it,
an address could scan through many different email/phone guesses here with nothing to slow it down,
the same gap the initial request's own throttle exists to close. Both this and the initial `POST
/auth/magic-link` request resolve email+phone through the same
`users.Repository.FindActiveByEmailAndPhone`, whose email side matches case-insensitively — the same
as `internal/telegrambot`'s own `emailMatchesUser` check, so the same address behaves the same way
whether someone signs in from the web or from Telegram. Whoever's linked their Telegram account (see
**Telegram** below) gets the same link and code by DM too, alongside — never instead of — the email.

**Logs** are for a person to read, not for a tool to parse (nothing in the club ever will, and
they are looked at by hand now and then): `internal/platform/logging` has the `slog.Handler` both
commands use, one line per record, `2026-10-28 16:10:32 INFO  message key=value ...` (a value
with spaces is quoted, and one that spans several lines, such as a panic's stack or the body of
the email that is logged when no SMTP is configured, is written below the line, indented, under
its key). Under systemd (`JOURNAL_STREAM` set) the time is left out, since the journal stamps
every line. The request line reads `GET /api/v1/users 200 12ms user_id=3`.

`httpapi.RequestLogger`'s per-request log line includes `user_id` once `auth.RequireAuth`
resolves one. That takes a small mutable box placed in the request context before
dispatch (`httpapi.SetRequestUserID`) rather than just reading it back from context after
the handler returns — `RequireAuth` builds its *own* new request via `r.WithContext`
deeper in the chain, which `RequestLogger`'s own `r` variable never sees again.

## Baseline access policy

**Baseline access policy**: `Authenticator.Protect(handler, roles...)` *always* also
requires at least one of `auth.BaselineRoles` (`admin`, `board`, `member`), on top of
whatever specific roles a route lists — holding only, say, `material` without also being
admin/board/member blocks every route, including material ones. `RequestMagicLink` won't
even send a sign-in link to someone matching on email+phone but holding no baseline role.

## Roles

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

## Login as the only external identifier

**Login is the only external identifier for a person**: `users.login` (`text UNIQUE NOT
NULL`, migration `00002_users.sql`) is generated automatically at creation — first
and last name slugified (accents stripped, `users.slugify`) and dot-joined, with a numeric
suffix on collision (`users.Repository.generateLogin`) — and admin-editable afterward via
`PATCH /users/{login}` (format-checked, `ErrInvalidLogin` on a bad one; `ErrDuplicate` if
another person already has it). No response or path anywhere in the API ever carries a
person's numeric database id; every package that used to key a route or a JSON field on
one (`tokens`, `roles`, `licenses`, `memberships`, `board`, `material`, `activities`) now
takes a login instead, translated to the internal id via a small, locally-defined
`UserLookup` interface (`UserIDForLogin`/`LoginForUserID`) that `*users.Service` satisfies
structurally — those packages can't import `users` directly, since `users` already imports
several of them. A listing that can span several different people at once (e.g.
`GET /transactions`, `GET /activities`) resolves the login via a SQL `JOIN` against `users`
instead of a lookup per row; a single-row write via `INSERT`/`UPDATE ... RETURNING` (which
can't `JOIN`) resolves it from a login the handler already has in scope, or with a
follow-up `LoginForUserID` call.

## Several emails and phones per person

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

## Permission tiers

**Permission tiers**: `admin` has full read/write access everywhere, including a person's
core data and role assignment. `board` can read everything (the full `User` record,
licenses, roles) but can only write activities (and the holiday calendar) and material
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
activity, material and accounting pickers use to choose a person without seeing personal
data.
The role catalog itself (`GET /roles`) is open to any baseline role, read-only regardless
of who's asking, and lists only the catalog's names/descriptions — never who holds which —
since that much is just organizational labels; assigning/revoking one is still admin-only.
The optional `material` role is never above `board`: whatever `material` can do, `board` can do too,
so a route or check that names `material` always names `board` with it.

## Activities

**Activities**: every activity has a mandatory `leader_id` (exposed as `leader_login`; the
organizer, defaulting to whoever creates it) who, along with admin/board, is the only one who can
edit or cancel it (`Handler.canManage`). admin/board can instead name someone else as `leader_login`
at creation time, or reassign an existing activity's organizer via `PATCH` — the current organizer
can't do either for themselves, even though they can edit every other field. Whatever its `type`
(`caving`/`canyoning`/`training`/`social`/`other`), joining — or being named as an initial
participant at creation time — requires being a member now (`Service.checkMembers`, through the
`RoleLookup` that `roles.Service` satisfies), else 409 `not_a_member`; being federated is not
required, and `GET .../participants` only says for each whether they are (`federated`, one
`licenses.Service.CurrentByUsers` query for all of them). Its `visibility` (`public`, the default,
or `private`) gates who may add a participant at all, not just who may join: in a public one anybody
can sign up anybody (themselves included); a private one restricts adding *anyone* — self included —
to the organizer, board, admin, or somebody who is already signed up (`Handler.join`, which asks
`Service.IsParticipant` for the last). Whoever signs somebody up is recorded with the date
(`activity_participants.added_by`, `added_at`, shown as `added_by_login` and `added_at`;
`Repository.AddParticipant`, and `CreateInput.CreatedBy` for those named at creation); signing up
somebody who is already in changes nothing, the first record stays. Removing one (`Handler.leave`,
`DELETE .../participants/{login}`): the person themselves, admin and board, a private activity's own
organizer (who can add anyone, so can remove anyone; on a public one they get no such extra say) and
whoever signed that person up (`Service.AddedBy`, whatever the visibility): somebody can always undo
what they did.

## Activity resources (memoria)

**Activity resources / memoria** (`internal/activityresources`): after a trip, whoever
took part in it writes up its memoria as an ordered list of resources — either a text
entry (title + a plain-text/Markdown `description`) or an uploaded file (title + the file
itself, typically a photo or PDF), discriminated by `activity_resources.type` and kept
consistent with a set of `CHECK` constraints (migration `00010_activity_resources.sql`),
the same "description only for text, file fields only for file" shape `material_movements`
already uses for its own type-specific fields. Metadata lives in Postgres (`Repository`);
a file resource's bytes live on disk instead (`Storage`, `config.ActivityResourcesRoot` —
a *named Docker volume*, not a host bind mount like `archive`/`userfiles`, since files
arrive exclusively through `POST /activities/{id}/resources` rather than being dropped in
by hand), keyed by the row's own id rather than any caller-given path, so unlike
`archive`/`userfiles` there's no directory tree to browse and no path-escape risk to guard
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
`archive`/`userfiles` file routes takes only the bearer header (no cookie).

## Distributions

**Distributions** (`internal/distributions`, migration `00021_distributions.sql`): things the club
hands out to its members (shirts, buffs, a rope, mugs). Each distribution is a single article — two
things to handle separately are two distributions — with a `status` (`draft` → `open` → `closed` →
`done`, one step at a time, forward or back (`nothingToAsk`, for all members with no options and no
extras, skips `open`: draft and closed are next to each other); a draft is editable and exists only
for admin/board, members ask for units only while `open`, `visibility` (`all`, `material`, `board`,
`admin`, migration `00035`) keeps whoever is below the level it names from seeing it at all
(`canSee`/`visibleTo` and `Handler.visible`/`accessible`: every route about the distribution answers
404, the list leaves it out; a draft stays for admin/board whatever it says; only admin sets it,
board sending anything but the current value gets 403, and leaving it out of a PUT keeps it),
`closed` freezes what members can ask for (staff can still enter and change orders, see below), and
units can be handed out — collections recorded, numbers written down — in any state but `draft`
(`Status.Deliverable`, `ErrNotDeliverable`, 409 `not_deliverable`), since the order often arrives
before the asking has ended; migration `00023` replaced the former `ordered`/`received` by
`closed`/`delivering` and `00025` removed `delivering` (mapped to `closed`), as handing out doesn't
depend on closing), an `audience` (`all_members`, everyone who was a member on `cutoff_date`, which
is then required; or `signup`, where `cutoff_date` is optional and, when set, limits who can sign up
to those members — `Repository.WasMemberOn`, a SQL check against `memberships.period`; with no
cutoff date it is for today's members, `WasMemberNowOrOn`), a `price` (0 = free), an optional
`extra_price` (its presence is what makes extras possible) and `max_extra` per person (which needs
`extra_price`), a `closes_at` deadline, and `distribution_options` (a name with its possible values,
e.g. "Talla": S/M/L; replaceable only while no unit exists, `ErrHasUnits`, and
`Repository.sameOptions` makes sending back the same set a no-op so a form can resend everything).
What people ask for are `distribution_units`, one row per unit (two extra shirts can have two
sizes): `beneficiary_id` is who it is for, `registered_by_id`/`registered_at` who actually entered
it — somebody else when a parent signs a child up or a friend adds extras for another, and never
overwritten by editing the choices (`UpdateChoices` touches `choices` only) — and `price` is frozen
when entered, so changing the distribution's price later doesn't change what somebody agreed to. A
person has at most one own (non-extra) unit (partial unique index, `ErrAlreadyRegistered`) and
extras up to `max_extra` counted by beneficiary whoever entered them: `CreateUnit` locks the
distribution row (`FOR UPDATE`) while counting so two concurrent requests can't both slip under the
cap. Anyone can ask for a unit for themselves or, with `beneficiary_login`, for somebody else, while
the distribution is `open` and before `closes_at`; staff (admin, board and material,
`canSeeEveryone`) at any time but in a draft, so also after the deadline and while it is `closed` or
`done` — orders are often fixed once the asking is over (a draft has no orders, for anybody:
`ErrNotOpen`; `Service.checkOpen`). Both the beneficiary and, unless staff, whoever enters it have
to be eligible (`ErrNotEligible`); an extra belongs to whoever it is entered for, with no notion of
who ends up with the item. Changing/removing a unit is for its beneficiary, whoever entered it and
admin/board/material (`Handler.ownUnit`; they all go past the deadline and change a paid unit,
though only admin/board mark payments, and while it is closed or done), under the same rules; what
was handed out is locked for the owner (a unit that isn't a row of an article is final once handed
out, even for staff; in a row of an article staff can still resize it but not below
`picked_up_quantity`, `ErrBelowDelivered`) and a paid one only changes through admin/board/material
(`ErrUnitLocked`). Every payment mark is also written into `distribution_payments` (migration
`00030`, the list of payments: `logPayment`, called by `MarkPaid` and `ClearPaid` in the same
transaction; negative entries for what is taken off, `unit_id` null if its order is gone since — an
order with something paid can't be deleted, `ErrUnitPaid`, 409 `unit_paid`: the payment is taken off
first), which `GET /distributions/{id}/payments` (admin/board, one distribution at a time) serves
newest first with `total_after`, a window `SUM` per distribution over its whole history so it
doesn't restart at a filter or page edge — the same idea as the accounting ledger's `balance_after`.
The person who paid is the order's beneficiary; there is no separate payer. Payment is per row and
can be partial (migration `00027`): `paid_quantity` of its `quantity` are paid, with
`paid_at`/`paid_marked_by_id` as when and by whom the last payment was marked, set exactly when
something is paid (`CHECK`s). Admin/board only: `POST .../payments` adds payments, all-or-nothing
(`Repository.MarkPaid`: `payments` of `quantity` units of a row, all that is pending when 0, plus
`unit_ids` for whole rows; more than pending is `ErrAlreadyPaid`), `POST
.../units/{id}/payment/reversal` (`quantity`: N) takes units off (`ClearPaid`). Asking for more of
something paid leaves the added units unpaid, and `UpdateQuantity` refuses to go below what is paid
(`ErrBelowPaid`); a row with anything paid is "paid" for `checkEditable`. Pickups still mark a whole
row. **Payments are recorded in accounting** (migration `00032_distribution_accounts.sql`): every
distribution has an optional `account_id` (the account its payments go to; `account_name` in the
JSON) and, when null, the account marked `accounts.distribution_default` — both have to be of type
`income`: a distribution naming another type is 400 `account_not_income` (`checkIncomeAccount`),
marking one as the default is refused by `accounting` (`ErrNotIncomeForDistributions`, 409
`not_income_for_distributions`), as is taking the `income` type off an account some distribution
names — (at most one, a partial unique index; marking one in
`accounting.Repository.Create`/`UpdateAccount` unmarks the other) is used — none of the two is 409
`no_distribution_account`. `POST .../payments` and `POST .../payment/reversal` take `account_id` in
the body: the account the money was paid into or goes back from, which has to exist
(`ErrUnknownAccount`, 400), be of type `asset` (`ErrAccountNotAsset`), and is required whenever
money moves (`ErrAccountRequired`; what costs nothing needs no account and records nothing). In the
same database transaction `recordPaymentTransactions` writes, per beneficiary of the request, a
transaction with two entries — the asset `+total`, the distribution's account `−total`, signs
changed when a payment is taken off — by direct SQL (not through `internal/accounting`, which this
package doesn't import), with a Spanish description of who and what ("Pago de Nombre Apellidos en
«Reparto»: 3 × Papeleta (0001-0003)"; "Devolución del pago de …"), and sets
`distribution_payments.transaction_id` of the entries it wrote (`ON DELETE RESTRICT`). A payment
made by mistake is undone by deleting its transaction: `accounting.Repository` runs a hook
(`BeforeDeleteTransaction`, wired in `cmd/api/main.go` to `distributions.UndoPaymentsOf`) in the
same database transaction before deleting it, which unpays what each entry of the list of payments
paid (or pays again what it took off) and removes those entries; if an order has changed since and
no longer allows it, nothing is deleted and the answer is 409
`transaction_has_distribution_payments` (`ErrPaymentsNotUndoable`). Asking again for an article
(`CreateArticleUnit`) adds to the person's row of it that isn't collected yet, else makes a new row
— the per-person unique index is gone, and `max_per_person` (`checkQuantity`) counts all their rows.
Collection (`POST .../pickups`, batch, admin/board/material, only once it is not a draft) can be
partial like payment (migration `00028`): `picked_up_quantity` of a row's `quantity` are handed out,
and `picked_up_at`, `pickup_recorded_by_id` (the caller) and `picked_up_by_id` (whom it was handed
to: not necessarily the beneficiary, as when somebody collects for others, but a member now or on
the cutoff date, `ErrInvalidCollector`, 409 `invalid_collector`) are those of the last delivery, set
exactly when something was handed out (`CHECK`s). `Repository.Deliver` takes `deliveries`
(`Delivery`: a row, a quantity or all that is left, optionally the identifiers) plus `unit_ids` for
whole rows, all-or-nothing, `ErrAlreadyPickedUp` for more than is left. For an article with numbers
each delivered unit is one of the row's identifiers (`distribution_unit_identifiers.delivered_at`):
only the assigned and not delivered can go (the given ones or the first, else
`ErrIdentifiersMissing`; `Service.MarkPickedUp` assigns the missing ones first with
`assign_numbers`), and a delivered number can't be dropped from the row's list. `ClearPickup`
(`DELETE .../pickup[?quantity|?identifiers]`) un-delivers, keeping the numbers assigned;
`ReturnUnits` (`POST .../return`) takes units back for good: the row shrinks (deleted at zero), the
numbers are deleted so they are free, and it can't go below what is paid (`ErrBelowPaid`) nor return
more than was delivered. A row with anything delivered can't be deleted. `GET .../order`
(admin/board/material) sums the units by combination of choices (`GROUP BY` the `choices` jsonb)
with the totals, and for units nobody has chosen the options of yet add up under empty choices. For
an `all_members` distribution still in `draft` — which has no units yet, they are made on opening —
the summary already counts one per member on the cutoff date, without making them
(`Order.Provisional`, set by `Repository.Order`; the page says so), since what has to be ordered is
known from the start. `GET /distributions` and `GET /distributions/{id}` add `eligible` for the
caller; `GET .../units` shows admin, board and material every unit and anybody else only the ones
for them or entered by them.

**Sale over the counter** (`POST /distributions/{id}/sales`, admin/board; `Service.Sell`,
`sale.go`). What takes four steps in the orders — enter the order, give it numbers, mark it paid,
hand it out — in one request, for the usual case of a member paying and taking the item at the club.
It only works with articles and out of a draft. It checks what it can before writing (that the
person is eligible, so `ErrNotEligible` comes before anything about collecting; the account when
something has to be paid; the numbers written by hand when they are free text), then calls the same
service methods as the separate routes, so every rule is the one of the separate step:
`RegisterUnit` (as staff), `AddIdentifiers` for the numbers asked for by name, `assignNumbers` for
the rest, `MarkPaid` and `Deliver`. It pays and hands out only the items the sale adds (the numbers
it gave, or `quantity` of a counted row), not what the person had ordered before in the same row.
The steps are separate database transactions, so if one fails the sale undoes the previous ones as
far as it can (the payment through `ClearPaid`, the numbers, and the row: removed if the sale made
it, put back to its old quantity if it already existed); the error reported is the one that made it
fail.

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

## Distribution articles

**Distribution articles** (`distribution_articles`, migration `00024`): the other kind of
distribution, for things asked for in any quantity with a cap on how many exist in all (the
Christmas lottery: 200 "papeletas" at 5 €, 1000 "décimos" at 20 €). A distribution with articles
(`Distribution.Articles`, each with `price` and an optional `max_total`) is always `signup` with no
price, extras or options of its own (`ErrArticlesConflict`, checked by `validateArticleKind` against
what the distribution will be once an edit is applied, so a nil `Articles`/`Options` counts as the
current ones); without articles everything above works as it always did. A person has one
`distribution_units` row per article with a `quantity` (`article_id` set, never `is_extra`, empty
`choices`: `CHECK`s of the migration) instead of one row per unit, so paid/collected marks apply to
the whole row; `price` stays what ONE unit cost, the row costs `price × quantity` (`unitCost` in the
frontend), and `quantity` is 1 outside articles. The own-unit unique index is now `WHERE NOT
is_extra AND article_id IS NULL` — `SyncAutomaticUnits`' `ON CONFLICT` has to repeat that exact
predicate — and articles have their own `(article_id, beneficiary_id)` one.
`Repository.CreateArticleUnit`/`UpdateQuantity` lock the distribution row (`FOR UPDATE`) while they
count what is left of the article (`remainingOf`), so two people asking at once can't both take the
last units (`ErrArticleExhausted`, 409 `article_exhausted`, with how many are left in the message).
Once any unit exists only each article's `max_total` can be edited, and not below
`Article.requested` (`replaceArticles`: `ErrHasUnits`/`ErrCapBelowRequested`);
`unit_count`/`picked_up_count` and the order summary count units (`sum(quantity)`), and `GET
…/order` groups by article. An article's numbers can be *described* (migration `00026`,
`identifier_fields`, `numbering.go`): parts with a spec of values and ranges ("Serie: 124-133, 137",
"Número: 0001-0200", zero padding kept) whose every combination is an item that exists, so what is
free, who has what and the next free ones are known. `max_total` is then always the number of items
(`normalizeArticles` overwrites it) and `has_identifiers` is implied; a unit's number is its parts
joined by `/`; typed numbers may use a range in the last part by position in its values
(`numbering.expand`, so gaps are respected) and must exist (`valid`); `POST …/identifiers/assign`
and `assign_numbers` on `POST …/pickups` (`Service.assignNumbers`) give the next free ones in
listing order; `GET …/articles/{id}/numbers` lists every item with its holder. With units present
the numbers can grow but not leave out one somebody has (`ErrNumberInUse`, in `replaceArticles`).
Migration `00024` also has `max_per_person` (checked with `max_total` in `checkQuantity`,
`ErrPersonLimit`, 409 `article_person_limit`) and `has_identifiers`: such an article's rows carry
one number per item in `distribution_unit_identifiers` (unique per article, `ErrIdentifierTaken`),
written with `PUT …/units/{id}/identifiers` (`Service.SetIdentifiers`, admin/board/material, only
once the distribution is not a draft, at most `quantity`, completed later if need be) and required
in full before collection (`MissingIdentifiers`, `ErrIdentifiersMissing`); with units present,
`max_total`, `max_per_person` and `has_identifiers` are the editable parts of an article.
`Article.String` is what the edit notification diffs (without `Requested`, which moves as people
ask). Frontend: one line per article in the "Artículos" textarea, `Nombre: precio[, máximo N][, N
por persona][, con números]` (`parseArticles`), which switches off the fields that don't apply
(`syncArticleFields`); `/reparto` shows a quantity field per article with what is left, "Tus
pedidos" rows are changed by quantity, the "Pedidos" section of `/reparto-gestion` (below) shows
each article against its cap and has every action on the orders, and its "Números" section
(`distribution-numbers.js`, there when some article has described numbers) lists every number with
who has it, whether it is handed out and (if the article has a price) paid, filterable, and how many
are free. The numbers are described in the articles textarea
with indented lines under the article (` Serie: 124-133, 137`).

## Licenses

**Licenses**: a license's type isn't free text — it's a `license_type_id` referencing the
`license_types` catalog, seeded in the database (migration `00007_licenses.sql`) rather
than a Go enum, so the list can grow with an `INSERT` instead of a deploy. Its `is_other`
escape-hatch entry ("Otra") requires an `other_detail` string saying which license it
actually is; every other entry forbids one (shared validation:
`licenses.normalizeOtherDetail`, used by both `Create` and `Update`). The catalog itself
is `GET /license-types` (admin-only, like `POST`: it only feeds the "assign a license"
form). `PUT`/`DELETE /licenses/{id}` (admin-only) correct or remove an assigned license
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

## Membership dues

**Membership dues** (`membership_dues`, alongside `memberships`): every year a socio remains a
member, they owe a cuota to cover it — the first one (alongside the one-time matrícula) the same day
they join, then once more each following year for as long as they stay a socio. `memberships.Due` is
one row per `(user_id, year)` covered (`UNIQUE`, `ErrDueAlreadyRecorded` on a second one for the
same year), always tied to the `transaction_id` that actually recorded the payment — a due with no
transaction behind it doesn't mean anything yet, so unlike a membership period itself there's no
"unpaid" row to represent. Deliberately keyed by `user_id`, not `membership_id`: a rejoining
member's due for a given year isn't naturally tied to one specific membership period, and this way a
year never needs to be reconciled against period boundaries. `POST /users/{login}/dues` (admin-only)
is `Service.RecordDue`: if the person has *no* membership period on file at all yet, this is treated
as their very first due, so a new membership period is also started for them (the request's own
`start_date` becomes its `StartDate`) — the same alta a person otherwise gets through `POST
/users/{login}/memberships`; someone with a prior, since-closed membership who's rejoining is left
entirely to that existing, separate alta flow instead (`HasAnyMembership` is already true for them,
so nothing extra happens). The request's `start_date` is therefore optional: it's only required —
400 `start_date_required`, `ErrStartDateRequired` — when there is no membership on file to open, and
ignored otherwise (a malformed one is still a 400 `invalid_date`). Before any of that, `RecordDue`
checks the transaction itself — it has to exist (`ErrUnknownTransaction`) and have at least one
entry on an account marked `allows_dues` (`ErrTransactionNotForDues`, 409
`transaction_not_for_dues`; `Repository.TransactionAllowsDues`, the dues counterpart of licenses'
check above) — so a rejected due can never leave a membership period started as a side effect. `GET
/users/{login}/dues` mirrors licenses' own self-or-admin/board read split, and joins each due's
transaction to add `paid_at` (the payment's own `occurred_at` — not `created_at`, since a past
year's due can be registered long after it was paid), only from this listing, never on `POST`'s
response, the same "only from the listing" pattern as `Account.Balance`. That is what `/socio` and
`/perfil` show: `/socio`'s "Historial como socio" timeline gets one "Cuota anual · <year>" event per
due, dated by `paid_at` and with no Editar/Borrar (there's no endpoint for either) — its date has to
keep the same midnight-UTC string shape every other event's has, since the timeline sorts them as
plain strings and a bare `YYYY-MM-DD` would sort before an alta of the same day instead of tying
with it; on a tie the order is alta, then the cuota, then a license paid the same day
(`TIMELINE_TIE_BREAK_PRIORITY`) — and `/perfil` shows the very same timeline, only without the
buttons: `js/timeline.js` (`loadTimelineData`, `renderTimelineRow`) builds and renders it for both
pages, `member.js` adding its action buttons to each row. That needed `GET /users/{login}/leaves` to
open to the person themselves, like their memberships, licenses and dues. A due can be removed
(`DELETE /dues/{id}`, only the due) but there's no endpoint to correct one, and no validation that a
due's `year` actually falls within an active membership period — both deliberately out of scope for
now. `membership_dues.transaction_id` is deleted with the transaction
(`Repository.DeleteTransaction` does it, nothing cascades) — a due is pure bookkeeping metadata with
no meaning apart from its transaction — while `licenses.transaction_id` (nullable, see above) makes
the delete refuse instead: a license is a real, independently meaningful record (proof of
federation) even apart from its payment, so the transaction that paid for one can't be deleted out
from under it at all. `/socio` has a "+ Licencia sin pago" button (admin-only, in the same panel as
the alta/baja actions) opening a dialog that `POST`s a license with no `transaction_id` — the way to
record one the club didn't collect; the ones it does collect are registered from their transaction,
in Contabilidad (`/cuenta`). The license then shows in the "Historial como socio" like any other,
with Editar/Borrar.

## Temporary leaves

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
membership period (`PUT /memberships/{id}`) re-validates every one of that person's
leaves that currently falls within the period *as it stood before* the edit — not every
leave they've ever had — so narrowing one period can't be blocked by a leave that actually
belongs to a different one of their own periods.
`POST /users/{login}/leaves`/`GET .../leaves` are admin-only-to-write,
self-or-admin/board-to-read (like the person's memberships, licenses and dues, so
`/perfil` can show the same history as `/socio`) — `PUT`/`DELETE /leaves/{id}` stay
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

## Material types and attributes

The package is `internal/material`, page `/material`. A material item has a *type* from the catalog
`material_types`, and the type decides its *kind*: Cuerda (`rope`) and Saca (`bag`) are fixed, one
of each, and their items are individual objects with their own columns (code, make and model,
`diameter_mm`, `capacity_liters`...); every other type is `general`, which an admin adds, renames
and deletes from `/admin`, and its items are stock (`total_quantity` comes from the movements).
The types have an order (`material_types.position`, changed with `PUT /material/types/order`, which
takes every type once, Cuerda and Saca included): it is the order of `GET /material/types`, so of
the "Tipo" list of the item form, and of the items in the catalog; a new type goes last.

**Attributes.** A general type defines attributes (`material_type_attributes`): colour, closure,
shape... Each is `text` (any value) or `enum` (one of the type's options in
`material_attribute_options`), and required or optional. An item keeps its values in the jsonb
column `attributes`, keyed by the attribute's id: a string for a text attribute, the option's id
for an enum one. Storing the option by id is what lets an admin rename an option and have every
item follow; deleting one that an item has is refused (`option_in_use`). Only attributes that have
a value are stored, never `null` or `""`: `buildAttributes` (`attributes.go`) trims texts, drops
blanks and checks every value against the type's definition (an attribute that is not the type's,
one given twice, an option of another attribute or a value of the wrong kind is `invalid_attribute`;
a required one with no value is `missing_attribute`). It runs on create and update, inside the same
transaction that reads the type, and `UpdateItem` always replaces the attributes with the body's,
so changing an item's type simply sends the new type's. A rope and a bag take none (their type has
none) and `attributes_only_for_general` backs that in the database. The data type of an attribute
never changes after it is created, since the values are stored by it. Nothing cascades in the
database (every foreign key is `RESTRICT`): deleting an attribute removes its options and its value
from the items of the type, and deleting a type removes its attributes and options first.

**Description.** `material_types.description_required` (default true, so every type kept behaving
as before) says whether a general item must have a description (`item_description_required`). It is
checked in `checkItem`, not by a constraint, since it depends on the type; for a rope or a bag the
description ("Observaciones") is always optional. Making a type's description or an attribute
required later does not touch the items already saved: the API marks the ones that lack it
`incomplete`, and editing fixes them.

**No two items the same.** The unique index `material_items_identity_key` over type, brand, model,
code, a hash of the description and a hash of the attributes (with `NULLS NOT DISTINCT`) refuses an
identical item (409 `duplicate_material`): identical units are one item with a quantity. That is why
the values are normalised before they are stored: `"Petzl"` and `"Petzl "` would otherwise be two
items. The same index guards the admin: deleting an attribute removes its value from the items of
the type, and if that would make two of them identical the whole change is rolled back
(`would_duplicate_items`). Changing the `required` flag, the name of an attribute or the label of an
option can never make items identical.

**How an item is named.** `display_name` is built by the API (`decorate`, `attributes.go`) so that
every listing, movement and Telegram notification names an item the same way: the type, brand and
model, the *values* of its attributes (not their names) in the order of the type's attributes
(`position`, changed with `PUT /material/types/{id}/attributes/order`), and, for general material,
the description at the end, joined with " · ": "Mosquetones · Petzl Attache · aluminio ·
asimétrico · con seguro". The catalog is read once per request (`loadTypes`, three small queries),
not once per item, so listing hundreds of items costs the same as listing one.

**Sales and rentals are recorded in accounting** (migration `00055_material_accounting.sql`), as the
payment of a distribution is. `POST /material/movements` takes `account_id`, the asset account the
money was paid into, and `CreateMovement` writes, in the same database transaction as the movement,
a transaction with two entries by direct SQL (`recordMovementTransaction`, `accounting.go`: the
package does not import `accounting`): the asset `+total`, the income account marked
`accounts.material_sale` or `accounts.material_rental` `−total`, dated `transacted_at`, with a
description in Spanish ("Venta de 2 × Mosquetones · Petzl Attache a Nombre Apellidos"; "Alquiler
de …") and the movement's `transaction_id` pointing at it. The total is the movement's `price` (the
item's price when it was made) times `quantity`; a rental charges its first week, since how long it
lasts is not known. What costs nothing records nothing and needs no account. The account has to
exist (`ErrUnknownAccount`), be of type `asset` (`ErrAccountNotAsset`), have no children
(`ErrAccountHasChildren`) and be given when money moves (`ErrAccountRequired`); an income account
has to be marked for that type (`ErrNoMaterialAccount`, 409 `no_material_account`). The two marks
work like `distribution_default`: at most one account each (partial unique indexes), marking one in
`accounting.Repository.Create`/`UpdateAccount` takes it off the other, and both have to be
`income` (409 `not_income_for_material`, checked by `finishAccountChange`). Unlike a distribution's
payment, deleting the *transaction* is refused (409 `transaction_has_material_movement`, from the
foreign key): the movement is deleted instead (`DELETE /material/movements/{id}`), which deletes
its transaction and entries in the same database transaction — unless the transaction has files
attached (409 `transaction_has_files`). A rental's return changes nothing in accounting.

## Library

`internal/library` is the club's library — books, magazines, maps, topographies…, each row one copy
of whatever it is, called an *item* — not `internal/archive`, the read-only tree of club documents
on disk, which is a different thing (it was called the library before this one). The data is in
Postgres (migration `00058_library.sql`) and the bytes of the files on disk (`LIBRARY_FILES_ROOT`).

**A row is one copy.** Two copies of a title are two `items` rows: there is no quantity or stock, as
there is for material, since an item is lent to a person and comes back whole.

**Types.** Every item has a type from the `library_types` catalogue (Libro, Revista, Mapa,
Topografía and Otro are seeded; whoever manages the library adds, renames and deletes them:
`GET/POST /library/types`, `PUT/DELETE /library/types/{id}`). `type_id` is required when creating or
editing an item (400 `type_required`, or `unknown_library_type` for one that does not exist) and the
item comes back with `type_id` and the `type` name, which the edit notice diffs like any other
field. Names are unique ignoring case (409 `duplicate_library_type`) and a type with items cannot be
deleted (409 `library_type_in_use`): both are checked in the service before the statement, because a
failed statement would abort the transaction the tests run in; the unique index and the foreign key
stay as the backstop. The codes have `library` in them because `duplicate_type` and `type_in_use`
already mean the same for the types of material, with another text in `error-texts.js`.

**Who can do what.** Everybody who can sign in (`Protect` with no role: member, board or admin)
reads every item and adds comments and files; admin, board and the `biblioteca` role create, edit
and delete items and set their covers and remove what others added; admin, board, `biblioteca` and
`material` read the loans and record loans and returns; deleting a loan is admin only. `biblioteca`
is a plain role in the `roles` table (like `material` and `money`, seeded by the migration and not a
system role), never above board: whatever it can do, board can do, so a route that names it names
`board` and `admin` with it (`manageRoles`, `loanRoles` in `handler.go`). A comment or a file may
also be removed by whoever wrote it, which `Service.DeleteComment`/`RemoveResource` check against
the `privileged` flag the handler works out from the caller's roles (403 `not_yours`).

**Availability, not borrower.** An `Item` carries `available` — true when it has no open loan,
worked out in `itemSelect` — and never who has it: nothing a plain member can read names the
borrower. The loans (`GET /library/loans`, `GET /library/items/{id}/loans`) are for the roles above,
and a test checks that no route open to a member contains the borrower's login.

**Loans.** `library_loans` has `lent_at` and `returned_at` (dates; the item is out while
`returned_at` is null) and a partial unique index allows one open loan per item, which `CreateLoan`
also takes the item's row lock for, so that two people recording at once cannot both win (the loser
is 409 `item_not_available`). The borrower has to be a member now (`RoleLookup.HasAnyRole(member)`,
409 `loan_recipient_not_member`), as for material. The dates default to today and may be earlier (a
loan that was forgotten is written down later) but never later than today, and a return cannot be
before its loan began (400 `invalid_dates`); "today" is the club's day, `internal/platform/clubtime`
(`Europe/Madrid`), not the server's or the database's, so recording at half past midnight there
works. An item that has ever been lent cannot be deleted (409 `item_has_loans`): its history stays.

**Comments and files.** A comment has at most 2000 characters (counted in characters, not bytes). A
file (at most 10 MiB, `MaxFileSize`) is a `library_resources` row plus its bytes under
`LIBRARY_FILES_ROOT/<item id>/<row id>`: the row is created first, since the path is keyed by its
id, and removed again if saving the bytes fails. The bytes are created once with `O_EXCL` and
**never rewritten** — the prod→testing copy hard-links the files, so a rewrite in place would
corrupt both trees. The type is decided from the bytes (`detectType`): only a PDF or an image is
shown in the page (`inline`); anything else is an attachment with a sandboxing
`Content-Security-Policy`, always with `X-Content-Type-Options: nosniff`, so a file can never run as
part of the intranet. The file and cover routes also accept the session cookie
(`ProtectAllowingCookie`), so that an `<img>` or a link opens them.

**The cover** is a `library_resources` row with `is_cover` (a partial unique index allows one per
item), set with `PUT /library/items/{id}/cover` (an image, recognised by its bytes: 400
`cover_not_image`). Replacing it is a transaction that deletes the old row and inserts the new one,
then saves the new bytes and removes the old ones: a new cover is a new file, never a rewrite. It is
not counted in `resource_count` nor listed with the other files, and `DELETE
/library/items/{id}/resources/{id}` answers 404 for it (it goes with `DELETE
/library/items/{id}/cover`). Its address does not change, so `serveFile` sends `Cache-Control:
private, no-cache` and the browser revalidates (`ServeContent` answers 304).

**Deleting an item** (`Repository.Delete`, one transaction with the row locked) refuses one that has
loans and otherwise deletes its comments and the rows of its files, then the item, and the service
removes the bytes afterwards — failing to remove a file only leaves it unreferenced, it never undoes
the delete. Nothing cascades in the database, so a person who has loans, comments or files cannot be
deleted from the intranet (409 `has_references`).

**Configuration.** `LIBRARY_FILES_ROOT` names the directory; unset, it is a directory called
`library-files` next to `ACTIVITY_RESOURCES_ROOT` (`config.Load`), so that an installation whose
`api.env` predates the library still starts. The status page lists it with the other storage
directories (`library_files`), `scripts/backup.sh` backs it up and `scripts/copy-prod-to-testing.sh`
hard-links it into testing, each applying the same default.

The Telegram notices say that an item was added, edited (a diff of the changed fields), deleted,
lent, returned, commented, or that a file or a cover was added or removed.

## Document archive

**Document archive** (`internal/archive`): serves club documents (estatutos, reglamentos, actas...)
read-only from a plain directory tree bind-mounted from `./archive` on the host — see [Dropping
files in](#dropping-files-into-the-archive-and-the-per-person-directories) — not the database. `GET
/archive?path=...` lists a directory (subdirectories first, then files, each group alphabetical
except for what the directory's own optional `.order.json` — a JSON array of names — puts first, in
its order; dotfiles never listed, that one included, though it can be fetched by its path); `GET
/archive/file/{path...}` streams a file's raw bytes (the path is the rest of the URL, slashes
included — e.g. `/archive/file/Actas/2026-02-01.pdf` — not a `?path=` query parameter), inline by
default or as an attachment with `?download=1`, with HTTP Range support via `http.ServeContent`.
Both are open to any baseline role and read-only — nothing here lets a caller upload, rename, or
delete anything.

## Per-person files

**Per-person files** (`internal/userfiles`): the same read-only, filesystem-backed model
as the document archive, one subdirectory per login instead of one shared tree — a scanned
DNI, a signed waiver, medical paperwork. `GET /users/{login}/files[?path=...]` and
`GET /users/{login}/files/file/{path...}` mirror `/archive`'s own listing/streaming shape
exactly (including the path-in-URL file route, `?download=1` and Range support), but are
*not* open to every baseline role: only that person themselves, or admin/board, may see
them (403 otherwise) — checked inline in the handler (`Handler.resolveAccess`), the same
self-or-privileged pattern
`memberships.Handler.listByUser`/`material.Handler.listMovementsByUser` use, since who
may see a specific person's files isn't a fixed role list the way `/archive`'s is. A login
with no subdirectory of its own yet — most people, until someone drops a file in for them
— gets an empty listing at their own root rather than a 404.

## Club information

**Club information** (`internal/clubinfo`): the "datos de interés" of the home page (the club's
name, tax id, address, bank account, the Bizum number...) as rows of `club_info_items` (migration
`00036`), instead of text fixed in `index.html`: they do change now and then, and the Bizum number
is not meant to be in the repository. Each is a `label`, a `value` and, optionally, a button that
copies another text (`copy_value`, `copy_label`: the tax id without its dash, the IBAN without
spaces). `GET /club-info` is open to any baseline role (it is what the home page draws); `POST`,
`PUT /club-info/{id}`, `DELETE` and `PUT /club-info/order` are admin only. An update replaces every
field, so a `copy_value` left out turns the button off, and keeps the item's place; a new one goes
last, and the order is replaced as a whole from every id (`Repository.Reorder`,
`409 reorder_mismatch` for anything else), like the memoria's. Blank texts are trimmed away and a
blank copy text means no button (`normalize`). The Telegram notices say what changes, values
included, like those of any other resource (an edit lists each field with its old and new value,
through `httpapi.DiffFields` over `auditableFields`): the channel is private and already carries
personal data. The migration seeds nothing, so no datum is in the repository: the home page shows
nothing until they are entered from `/admin`.

## Admin status

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

It also says how full the disks are (`disks`: one entry per filesystem that holds some of the
three storage directories that exist, with the percentage `df` shows, taken from `statfs`) and
what is wrong right now (`problems`, sentences in Spanish): the database not answering, a
storage directory that does not exist, a disk at `DISK_ALERT_PERCENT` (90 by default, 0 turns
that check off) or more. `Monitor` (`monitor.go`, started from `cmd/api` with the same
Telegram sender as the error logs) looks at that same status every 5 minutes, the first time a
minute after starting, and warns when the *set* of problems changes — a new one ("⚠️
intranet.katiuskas.es: …", one line each), or all gone ("✅ … ya no hay ningún problema") —
and again once a day while one remains, not at every look. A problem is identified by a key
that leaves out the details in its text (a disk going from 91 % to 92 % is the same problem).
It can only warn while the API is running: that the API itself, a backup or a deployment fails
is told by systemd (`docs/environments.md`, "Alerts when something fails").

**Activity** (`activity`, only in the response of `GET /admin/status`, not in what the monitor
looks at): how many different people and how many requests there were in the last hour, day and
month. People are counted from `tokens.last_used_at` — the session's last use, so a session counts
in the period of its last use only —, one person once however many sessions they have, leaving out
the sessions of an administrator acting as somebody else (`impersonated_by`). Requests come from
`request_counts`, filled by `internal/requestcount`: a middleware that wraps the whole router in
`cmd/api` counts, in memory and per minute, every request under `/api/` except
`/api/v1/health` (it counts when the request arrives, so refused, failed and non-existent routes
count too), and a loop adds the counts to the table (`INSERT … ON CONFLICT DO UPDATE`, additive)
every five minutes; with the database failing the counts stay in memory for the next flush. What is
still in memory is also stored when the API shuts down (see **Orderly shutdown**) and before
`GET /admin/status` reads the figures (`Handler.SetFlusher`), so what the page shows is up to date;
only a crash or a kill loses the last few minutes. If the figures cannot be read, `activity` is left
out and the rest of the status is answered as usual.

**Calendar of activity** (`GET /admin/activity`, `activityCalendar` in `adminstatus/activity.go`):
who used the API each day, for the calendar of `/admin`. It is kept in `user_activity` (a row per
person and day), written by the same `requestcount.Counter` as the totals. The counter learns who
made a request from `httpapi.Actor`: it puts an empty box in the request's context (`WithActorBox`)
before passing it on, `auth.RequireAuth` (and the cookie variant) fills it (`SetRequestActor`, with
the person and whether an administrator is acting as them) and, once the request is over, the
counter counts it for that person on that day — so a request nobody signed in for counts for nobody,
and an impersonation counts for nobody either, since it is not the person's own activity. The day is
the club's, `Europe/Madrid` (`requestcount.Day`, with `time/tzdata` embedded so that it does not
depend on the host): a person using the intranet at half past midnight there is on the new day.
Flushing keeps what cannot be stored for the next time, except activity of a person who no longer
exists (a foreign key violation, which is dropped), and deleting a person deletes their rows
(`users.Repository.Delete`). Nothing earlier than this feature is known. The endpoint asks the
counter to store what it still has in memory, as the status does, and answers with the days that
have any activity in the last `days` days (365 by default, 730 at most), by login: the page looks
the names up in the directory. It is admin only: it says who used the intranet and when, and it
holds counts, never what they looked at.

## Orderly shutdown

`cmd/api` runs the server with `http.Server` and a context cancelled by SIGTERM or SIGINT
(`signal.NotifyContext`; systemd and `docker stop` send SIGTERM), and `serve` does, in this order:
`Shutdown` (it stops accepting connections and lets the requests being answered finish), then
`finish`, which waits for the work those requests started in the background — the Telegram
notifications and the sends of `authapi`, tracked by `httpapi.Go`/`httpapi.WaitBackground` and
`authapi.Handler.WaitForBackgroundSends` — and stores the request counts that are still in memory
(`requestcount.Counter.Flush`); the connection pool is closed last. All of it has `shutdownTimeout`
(25 s) to end; what is not over by then is given up (and logged), never waited for. The cancelled
context also stops the Telegram bot's long poll, the monitor and the counter's loop. A request
that ends well is therefore not cut off by a deploy, and a notification or an email that was on its
way still leaves. Work that sleeps until some deadline, like the deletion of a magic-link message
in Telegram after the link's lifetime, is *not* tracked (it would hold the shutdown up for as
long): it is lost with the process, as it always was. A crash or a `SIGKILL` skips all of this.
systemd's default `TimeoutStopSec` (90 s) leaves room for it, and `docker-compose.yml` asks for
`stop_grace_period: 30s`, since docker kills after 10 s unless told otherwise.

## Telegram

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
  caller is told as well, with a 404 `unknown_details` for both (the club accepts that this
  says who has access: it helps members who mistyped, and the per-address limit keeps it from
  being scanned) — and a successful request, or one held back by the cooldown, reports nothing.
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
  `tokens`, `memberships`, `licenses`, `activities`, `material`, `board`,
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
  touch can appear as a spurious change; `roles` and `material` have no diffed update at
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
- `cmd/api/main.go` sends "🚀 API started" once, right before `http.ListenAndServe` — with the
  version and the date and time of the last commit when known, in the server's time zone ("🚀 API
  started (version v0.3.6, commit del 17-10-2026 12:00)", or just one of the two, or neither:
  `startupMessage`, from `adminstatus.ReadBuild`, the same build info `/admin` shows) —
  the one notification that isn't fire-and-forget: it's a single call at process
  startup, not something on any request or statement's critical path, so blocking on it
  (bounded to 10s, same timeout every other notification uses for its own goroutine)
  costs nothing a user could ever notice. Still unconditional rather than gated like the
  bot above (which genuinely can't do anything useful without a channel to report to) —
  a restart is rare enough that falling through to `NoopSender`'s own info log when
  Telegram isn't configured is no noisier than logging it directly would've been anyway.

## Accounting

**Accounting** (`internal/accounting`): the club's own money — a chart of accounts (`accounts`:
name, an optional but unique-when-present `code` (migration `00013_accounts_code_optional.sql`; a
real ledger account tends to have one, e.g. "570" for "Caja", but a purely organizational grouping
node might never get one — `""`/blank is normalized to no code at all, `Handler.normalizeCode`, the
same "not itself a storable value" treatment `licenses.normalizeOtherDetail` gives an empty
`other_detail`, so two codeless accounts never collide under the column's own `UNIQUE` constraint
the way two literal `""`s would), an optional `parent_id` nesting it under a broader one — the
frontend builds any tree view it wants client-side from that — and two plain booleans, `allows_dues`
and `allows_licenses` (migration `00017_accounts_dues_licenses_flags.sql`, both `false` until an
admin or the "money" role marks them): which accounts a transaction has to have an entry on to be
allowed to record membership dues or federative licenses, enforced by
`memberships.Service.RecordDue` and `licenses.Service.Create` (see **Membership dues**/**Licenses**
above) and always present in every `Account` JSON, `false` included, unlike the omitted-when-absent
`code`/`parent_id`; like every other field, `UpdateAccountInput` replaces them wholesale, so a
`PUT` that leaves one out turns it off) and `hidden` (migration `00019_accounts_hidden.sql`, same
always-present/replaced-wholesale shape): an account that's no longer used. An account may also have
a `type` (migration `00031_accounts_type.sql`, the `account_type` enum: `asset` — its balance is
money the club really has —, `income`, `expense`; optional and left out of the JSON when absent like
`code`, a blank one normalized to none by `normalizeType`, `PUT` replacing it, an unknown one 400
`invalid_type`): it is shown on `/cuenta`'s header (`accountTypes` in `account-tree.js`), chosen in
the create/edit dialogs, and the account a distribution's payment is made in has to be an `asset`.
An account with no type of its own has its parent's, and so on up (migration `00034`, SQL function
`account_effective_type`): `type` in the JSON is the own one, the one `PUT` replaces, and
`effective_type` the one that counts, which every rule (the asset paid in, the income account of a
distribution, `distribution_default`) and the frontend's pickers and `/cuenta`'s header use. Since a
change of type, of parent or a new account can alter a whole subtree, `finishAccountChange`
re-checks in the same transaction that the account saved and everything below it that has to be
income still is — a distribution's account and the default one (409 `not_income_for_distributions`)
and an account marked `allows_dues`/`allows_licenses` (409 `not_income_for_dues_licenses`) — and
rolls back if not. Only that subtree is checked, so accounts that were marked before this rule (none
had a type then) don't block edits elsewhere; they have to be given type income the next time they
are edited. `distribution_default` (migration `00032`, always present in the JSON, replaced
wholesale by `PUT`, with a "Repartos" badge and a checkbox in the dialogs) marks the account
distributions record their payments in when they have none of their own. One with entries can't be
deleted — they're history — so it's flagged instead, and *only the frontend acts on it*: the API
lists, reads, edits and posts to a hidden account like any other. `DELETE /accounts/{id}`
(admin/money) exists only for an account nothing references yet — one with a child account or a
posted entry answers 409 `account_in_use` (`ErrAccountInUse`, from the foreign-key violation).
Likewise `DELETE /transactions/{id}` answers 409 `transaction_has_licenses`
(`ErrTransactionHasLicenses`) while a license still names the transaction as its payment
(`licenses.transaction_id` is `ON DELETE RESTRICT`); its dues, by contrast, are deleted with it.
Against that chart sit `transactions`: every real movement of money the club records, each made of
two or more `entries` (an `account_id` and an `amount`, positive into that account or negative out
of it) whose amounts always sum to exactly zero — standard double-entry bookkeeping, so a
transaction always says both where money came from and where it went, never just one side of it.
Each account also appears at most once per transaction (`transaction_entries_account_once`, a
`UNIQUE (transaction_id, account_id)` from migration `00018`; `validateEntries` checks it in Go
before touching the database, alongside the two-entry minimum and non-zero amounts,
`ErrDuplicateAccount`, 400 `duplicate_account`) — its one entry's amount already says everything
that moved through it, and the ledger's per-account running balance and "other accounts" detail both
assume it. `GET /accounts` also joins in each account's `balance` (summed from every entry ever
posted to it) and `last_transaction_at` (the most recent one's `occurred_at`) — for an account that
groups others, which has no entries of its own, those of everything below it (a `closure` CTE of
every account with its descendants), so a group shows the sum of its children's balances and the
latest of their last movements — and never present on any other `accounts` route
(`GET`/`POST`/`PUT /accounts/{id}` are all just the row itself, no extra join); a `LEFT JOIN`
against `transaction_entries`/`transactions` means an account with no entries at all still gets one
row back rather than being silently missing, `COALESCE`d to a zero `balance` (a real, computed zero
— never absent the way it is everywhere else `Account` shows up) while `last_transaction_at` is
simply `NULL`. "At least two entries" and "sums to zero" both depend on every sibling row, not just
one, so neither can be a plain `CHECK` — both are enforced instead by `transaction_entries_balance`,
a `DEFERRABLE INITIALLY DEFERRED` constraint trigger (migration `00012_accounting.sql`) that only
actually runs at commit, after every entry a write touched is in place, rather than rejecting a
transaction's entries one at a time as `accounting.Repository.CreateTransaction`/`UpdateTransaction`
insert them individually inside their own DB transaction (the same `pool.Begin` pattern
`activities.Repository.Create` already uses for an activity's initial participants). The trigger
explicitly skips its own check once the `transactions` row itself is also gone (checked first,
before counting/summing entries) — deleting a whole transaction deletes every one of its entries
(explicitly, just before), each of which fires this same trigger, and without that guard the last
entry to go would look like "a transaction with zero (too few) entries" instead of a transaction
that's correctly, completely gone. `Repository.CreateTransaction`/`UpdateTransaction` don't just
rely on the deferred default, though: right after inserting entries they run `SET CONSTRAINTS
transaction_entries_balance IMMEDIATE` on their own DB transaction to force the check right then
(mapping a violation to `ErrUnbalanced` from that call instead of a later `Commit`), then set it
back to `DEFERRED` before returning — necessary because `pool.Begin` on top of an already-open
`pgx.Tx` (exactly what every test does, via `testutil.NewTx`) opens a *savepoint*, not a real nested
transaction, and Postgres only ever fires a deferred trigger at a real `COMMIT`, never at a
savepoint's own `RELEASE` — so without forcing it immediate, the check would silently never run at
all under test, and resetting back to `DEFERRED` afterward matters too, since `SET CONSTRAINTS`'s
effect otherwise outlives the savepoint that set it (unlike a `ROLLBACK`, a successful `RELEASE
SAVEPOINT` doesn't undo it), which would make the *next* multi-row entry insert anywhere else in the
same enclosing transaction fail spuriously, checked one row too early. `UpdateTransaction` corrects
a transaction by replacing its description, `occurred_at`, and its *entire* set of entries at once
(delete every existing one, insert the given set) rather than editing a single entry in place —
there's no single-entry edit endpoint. `occurred_at` is a real timestamp, not just a date the way
`activities.start_date`/`licenses.issued_date` are, so it decodes straight into a Go `time.Time`
(RFC 3339, `encoding/json`'s own default) rather than needing the manual `time.Parse` every
plain-date field elsewhere needs. Reading (`GET /accounts`, `GET /transactions`, and their `/{id}`
forms) is open to any baseline role — the club's own finances are something every socio can see, not
just admin/board — with one addition: for admin/board only, `GET /transactions/{id}` also carries
`dues` and `licenses`, the cuotas and licenses that transaction paid for (always present for them,
`[]` when none; absent for anyone else, since who paid which is private to that person, admin and
board — see `GET /users/{login}/dues`). `Handler.getTransaction` fills them from
`DuesLookup`/`LicensesLookup`, two narrow interfaces `*memberships.Service`
(`ListDuesForTransaction`) and `*licenses.Service` (`ListByTransaction`) satisfy structurally — the
same pattern as `UserLookup` — rather than this package joining their tables itself; the wrapper
type `transactionWithPayments` exists only for that one response, never for create/update/list.
Writing needs admin or the optional, non-system "money" role (seeded by migration
`00012_accounting.sql`, deletable by a club that doesn't want the distinction), the same "admin plus
one optional custom role" shape `material` already has for material movements — board itself gets no
special write access here, same as it has none for licenses or a person's own data. Every write
still goes through the same baseline-role floor as every other route: holding only `money` with none
of admin/board/member is rejected everywhere, this included. Every account also has a `position`
(migration `00012_accounting.sql`) — its manual display order among its own siblings (same
`parent_id`, `NULL` counted together as one top-level group), not unique or gap-free on purpose,
since it's only ever used to `ORDER BY`, never to identify a row. `CreateAccount` appends a new
account after its own sibling group's current highest position, the same "`MAX(position) + 1` in the
same `INSERT`'s own subquery" pattern `activityresources.Repository.Create` uses for a resource's
position within its activity. The only way to actually change one afterward is `POST
/accounts/{id}/move`, admin/money-gated same as every other accounting write: `{"direction": "up"}`
swaps it with the sibling immediately before it, `"down"` the one immediately after — a harmless
no-op, not an error, if there's no such sibling (already first/last within its own group) — and
answers with the whole, freshly reordered `GET /accounts` list (same "return the full list after
reordering" shape the memoria's own reorder endpoint uses) rather than just the moved account, so a
caller can re-render straight from the response. `GET /accounts` itself walks the tree in pre-order,
depth-first: every top-level account in its own `position` order, each immediately followed by all
of its own descendants (recursively, each in their own `position` order, however deep) before the
next top-level sibling — `Repository.ListAccounts`'s own `tree` CTE computes this via the standard
"materialized path" trick (an array of every `position` from a real root down to each account;
Postgres compares arrays element-by-element, so ordering by it reads out exactly that walk) rather
than assembling the tree in Go. Anchored only at genuine roots (`parent_id IS NULL`), that walk can
never loop forever even against a cycle somehow already among non-root accounts — nothing in such a
cycle has `parent_id IS NULL`, so nothing in it could ever seed the walk to begin with; it would
instead just silently vanish from the listing, which is exactly why `UpdateAccount` rejects
(`ErrCircularParent`, 409 `circular_parent`) setting a `parent_id` that is, or descends from, the
account being edited — checked by walking *up* from the proposed parent (a query genuinely capable
of looping forever against pre-existing bad data, unlike the listing's own downward walk), so that
one's own recursion is capped at a depth no real chart of accounts would ever approach, purely so it
can't hang even then. The hierarchy only groups: an account has child accounts or has transactions,
never both (migration `00033`, two triggers so that no path skips it, the transactions of a
distribution's payments included): an entry on an account with children is 409
`account_has_children` (`ErrAccountHasChildren`, on creating or correcting a transaction), and
giving as parent — creating under it or moving to it — an account that already has entries is 409
`parent_has_entries` (`ErrParentHasEntries`); the triggers say which in the error's
`ConstraintName`. The frontend leaves out of the parent pickers the accounts that have transactions
(`last_transaction_at`) and shows the ones with children disabled in the "otra cuenta" picker, hides
"+ Nueva transacción" on a group's page and leaves them out of the distribution and payment pickers.
`GET /accounts/{id}/transactions` is one account's own ledger — or, for one that groups others (it
has no entries of its own), that of everything below it: every entry of all its descendants, each
with the `account_id`/`account_name` it is on and a `balance_after` that is the running total of all
of them together (ordered by date, transaction and entry), not the balance of the account the entry
is on — the `AccountEntry` rows it was posted against, a page at a time (not the same shape as a
bare `Entry`: each one inlines its own transaction's `description`/`occurred_at`, and a
`balance_after` instead of just an `amount`), newest first same as `GET /transactions` itself.
`Repository.ListAccountEntries` computes that running balance oldest-first (a running balance only
ever makes sense walked forward through time) and serves the page newest-first, the opposite order
from how it accumulates. Both this and `GET /transactions` take the same `limit`/`offset` (default
50, at most 100) and optional `from`/`to` (inclusive `YYYY-MM-DD` days; `parseListFilter`, a bad one
is 400 `invalid_date`/`invalid_date_range`) — `ListFilter` — since a few years of ledger is
thousands of rows. A `q` of words keeps only the transactions whose description has every one, in
any order and ignoring case and accents (`ListFilter.Words`, `descriptionHasWords`/ `likePatterns`:
`translate()` of the Spanish accented letters on both sides and `LIKE`, with `%`, `_` and `\`
escaped, so it needs no `unaccent` extension); on the ledger it is applied after the running
balance, which doesn't change with it. The ledger's `balance_after` is a window `SUM` over the
account's *whole* history, computed in a subquery before the range and page are applied, so it never
restarts from zero at the edge of a window. Open to any baseline role, same as every other read in
this package; `Handler.listAccountEntries` checks the account actually exists first (404, not just
an empty list) since `ListAccountEntries` alone can't tell "no such account" apart from "a real
account nobody's ever posted against."

## Budgets

**Budgets** (`internal/budgets`, migration `00039`): the budget of a calendar year has two parts,
income and expenses, each made of line items ("partidas") with an estimated `amount`, and each item
stands for one or more accounts. `GET /budgets` and `GET /budgets/{year}` are open to any baseline
role; `PUT` and `DELETE` need admin or `money` (and, like everywhere, a baseline role on top).
`PUT` creates the year's budget or replaces it whole in one transaction (`Repository.replace`), so
there is no per-item endpoint. `Service.Save` checks, in Go (they span rows, and an account's type
can change later): the name is not blank and unique in its part (case-insensitively); the amount is
zero or more with at most two decimals (it travels as a JSON number and is turned into cents); the
item has at least one account; every account exists, is a leaf (a group is
`account_not_budgetable`), has the part's effective type (`account_effective_type`: income in
`income`, expense in `expenses`) and is in only one item; **every leaf account of effective type
income or expense that is not hidden is in some item** (`accounts_missing`, naming them; hidden
accounts may be left out or put in); and the two parts add up to the same (`unbalanced`, with both
totals). The database only keeps what a row says by itself: one budget per year, `amount >= 0`, and
an account in one item per budget.

What was really received or spent is computed on read from the entries of the accounts, by the
transaction's `occurred_at` inside the calendar year (UTC): an income account's actual is minus the
sum of its entries, an expense account's is the sum. An item's percentage is `actual / amount`
(`null` when the amount is zero) and the parts have their own. An account created after the budget
was saved is not in it: `GET` lists it in `unassigned_accounts` with what it moved, and the next
`PUT` has to include it. An account that appears in a budget cannot be deleted (foreign key). The
Telegram notices (`describeChanges`) say that a budget was created, deleted or changed (items
added or removed, amounts and accounts changed), values included.

## Transaction files

**Transaction files** (`internal/transactionfiles`, migration `00040`): tickets, invoices and the
like attached to an accounting transaction. The metadata is in `transaction_files`
and **the bytes are on disk** in `TRANSACTION_FILES_ROOT` (at most 10 MiB each, `MaxFileSize`), the
way an activity's memoria keeps its files (`Storage`: `<transaction id>/<row id>`, created with
`O_EXCL` and **never rewritten**, because testing shares production's files by hard link, see
`docs/environments.md`). The row is created first and the bytes saved after it (the path is keyed by
the row's id); if saving fails the row is removed again, and deleting removes the row first and the
bytes after (a failure there only leaves an unreferenced file, logged); a row whose bytes are
missing answers 404. `scripts/backup.sh` copies the directory and `scripts/copy-prod-to-testing.sh`
hard-links it into testing's own. Every route is under `/transactions/{id}/files`, plus
`GET /transaction-files` (the overview). Admin, board and money read (they can hold personal or
bank details, so not every member); admin and money attach and remove. Everybody who can sign in
only gets to know that a transaction has files: `file_count` is in accounting's `Transaction`
(`GetTransaction`, `ListTransactions`, `UpdateTransaction`) and in `AccountEntry`, counted by a
subquery.

An upload (`multipart/form-data`, field `file`) is read whole, refused with 413 `file_too_large`
past the limit and with 400 `file_required` when empty; the name keeps only its base (no path), and
**the type is decided from the bytes** (`detectType`: a PDF or an image by content, anything else
by the extension, never one of those): the file is sent `inline` only when it is a PDF or an image
(`Viewable`), otherwise as an attachment with a sandboxing `Content-Security-Policy`, always with
`X-Content-Type-Options: nosniff`, so a file can never run as part of the intranet. The file route
also accepts the session cookie (`ProtectAllowingCookie`) so that a link opens it in a tab. A file
is addressed with its transaction (`/transactions/{id}/files/{fileId}/...` is 404 if it belongs to
another one).

`transaction_files.transaction_id` has no delete action, so **a transaction that still has files
cannot be deleted** (`accounting.ErrTransactionHasFiles`, 409 `transaction_has_files`): the files
are removed first. The overview (`Repository.List`) joins the transaction and its entries with the
account names, ordered by the transaction's date, and takes `year`, `limit` and `offset`. The
Telegram notices say that a file was attached or removed and from which transaction.

## Testing helpers

**Testing helpers**: `internal/testutil` provides `NewTx` (migrated, isolated
transaction), `InsertUser`/`InsertActivity`/`RoleID`/`LicenseTypeID` fixtures (the
`TestUser` `InsertUser` returns carries a `Login` too, for tests exercising a
login-addressed route), `InsertTransaction`/`InsertTransactionOn` (a real, balanced
two-entry transaction for tests that record a due or a license against one:
`InsertTransaction` posts it to an account that allows both, `InsertTransactionOn(t, tx,
allowsDues, allowsLicenses)` picks the flags, for the cases that must be rejected), and
`InsertTransactionAt(t, tx, occurredAt)` (the same as `InsertTransaction` with a chosen
`occurred_at`, for tests that care when a payment happened), `MaterialTypeID` (looks
up a material type, fixed or seeded, by name, like `RoleID`/`LicenseTypeID`), `UniqueEmail` (a
per-test, per-call unique address, which `InsertUser` itself uses), and
`FakeResolver` + `NewTestAuthenticator`/`TokenFor` — an in-memory `auth.SessionResolver`
so handler tests in every *other* package can mint bearer tokens for arbitrary
users/roles without hitting the real `tokens` table (only the `auth` and `tokens`
packages' own tests exercise real session mechanics; `auth`'s internal tests use a local
stub instead of `testutil`, since `testutil` imports `auth` and an internal test file
can't import back). `internal/archive` (and `internal/userfiles`, structured the same way)
needs none of the Postgres fixtures — each package's own `service_test.go` builds a
`Service` directly on a `t.TempDir()` tree, no Postgres involved at all; only their
`handler_test.go`s reach for `FakeResolver`/`TokenFor` to mint bearer tokens for the
role/self checks their routes enforce.

## Dropping files into the archive and the per-person directories

Club documents (estatutos, actas...) shown at `/archivo`: drop files into `./archive/` next
to `docker-compose.yml` (bind-mounted into the API container, auto-created if missing) — no
rebuild, no restart, nothing through the API itself. That directory is gitignored. The listing is
alphabetical; to put some files first (the home page's Documentos, Actas and Memorias lists use
this), add a `.order.json` to that directory, a JSON array of names such as
`["estatutos.pdf", "reglamento.pdf"]`: those come first, in that order, and the rest follow
alphabetically. A name that is not there is skipped, and a file that is not a valid array is
ignored (the API logs a warning). Editing it takes effect on the next request.
The home page's three lists read three fixed directories, named exactly like this (the file system
is case-sensitive): `./archive/Documentos`, `./archive/Actas` (files named `yyyy-mm-dd.pdf`, or
`yyyy-mm-dd-suffix.pdf` when there are several that day) and `./archive/Memorias`.

Per-person documents (a scanned DNI, a signed waiver...) shown at `/users/{login}/files`: drop files
into `./user-files/<login>/` next to `docker-compose.yml` (same bind-mount, auto-create, gitignored,
no rebuild/restart story as `./archive` above), one subdirectory per login. A copy of each
federative license goes in that same person's `licencias/` subdirectory, named
`yyyy-mm-dd-tipo_de_licencia.ext` (image or PDF); an optional thumbnail for one goes in
`licencias/.thumbnails/`, named after that same original filename plus `.jpg` (e.g.
`licencias/.thumbnails/2025-03-15-federativa.pdf.jpg`) — see [`frontend.md`](frontend.md) for how
`/perfil` and `/socio` read both conventions back out. `scripts/generate-license-thumbnails.sh`
makes every missing (or out-of-date) thumbnail from the license copies — first page of a PDF, or a
smaller version of an image, 280px on the longest side — and only needs `pdftoppm`/ImageMagick on
the machine running it (`--help` for the options: `--root`, `--force`, `--dry-run`, or just some
logins). It also removes from each `.thumbnails` everything that is not the thumbnail of a license
copy still in that directory, so a deleted or renamed copy leaves nothing behind. On the club's
host a systemd timer runs it every 15 minutes (`docs/environments.md`, "License thumbnails").
