# Architecture

Reference document, kept in sync with the code: how the system is put together and the business
rules that fit neither in a table of routes nor in a database schema. For everything else there
are separate files:

- [`api.md`](api.md): every endpoint of the API, with who can call it, its parameters and its
  errors. The complete contract is
  [`backend/openapi/openapi.yaml`](../backend/openapi/openapi.yaml).
- [`database.md`](database.md): how the data is stored: tables, columns, constraints,
  triggers, enums and seeded data.
- [`backend-internals.md`](backend-internals.md) and [`frontend.md`](frontend.md): the long
  version of how the backend works, resource by resource, and of each page of the frontend, with
  the reasons for each decision.

Contents:

- [Language](#language) and [Components](#components).
- [Permissions](#permissions): what each access level can do.
- [Business rules](#business-rules): one subsection per resource, with its rules.
- [Backend](#backend-backend): layers, sessions, Telegram.
- [Frontend](#frontend-web): the pages and their shared mechanics.
- [Running locally](#running-locally).

## Language

- Code (identifiers, comments, logs, API error messages): **English**.
- Documentation (`docs/`; for now not the README, which is still in Spanish): **English**.
- Web frontend (`web/`): all the text visible to the user, in **Spanish**.
  The identifiers of the JS/HTML code itself stay in English.

## Components

```
web/ (HTML + JS, Spanish)  →  backend/ (REST API in Go, /api/v1)  →  PostgreSQL
```

A single Go service concentrates the business logic and the data access. No client touches
PostgreSQL directly, which makes it possible to add a mobile app or a terminal client later
without duplicating business rules: they share the contract described in
`backend/openapi/openapi.yaml` (summarized in [`api.md`](api.md)). The data is in
PostgreSQL ([`database.md`](database.md)), except for three file trees on disk (the document
archive, each person's files and those of the activities' memoria).

## Permissions

There are three levels, from most to least access. Each route applies its own with
`Authenticator.Protect` plus, when needed, an additional check inside the handler itself.
In addition, `auth.BaselineRoles` requires admin, board or member on every protected
route.

| Level | Can see | Can modify |
|---|---|---|
| `admin` | Everything | Everything, without exception |
| `board` | All the data of the API | Activities, material and the calendar's holidays |
| `member` (socio) | A reduced view of people and their own data | Their own contact data |

### `admin`

Can do everything, without exception.

### `board`

Can **see** all the data of the API: people, members, licenses, annual dues, temporary
leaves, Junta Directiva, roles, activities, material and accounting.

Can only **modify**:

- Activities (any of them, not just their own).
- Material (creating items and movements).
- The calendar's holidays.

Everything else is admin-only: board cannot create or delete people, register joining/leaving,
correct temporary leaves, register annual dues, assign licenses, manage the Junta
Directiva or assign/remove roles for anyone. The routes affected are those of `/users`,
`/memberships`, `/leaves`, `/licenses`, `/board-members` and
`/users/{login}/{licenses,dues,leaves,memberships,roles}`.

Nor can it write to accounting, which admits admin and the optional `money` role
(see "Accounting").

### `member` (socio)

**List of people.** A member can call `GET /users`, but does not get the full record
of each person, only `users.MemberListing`, and only for those who are or have been
members (`membership_start_date` not null). `users.Handler.list` decides which shape to return
depending on the role of whoever asks; there is no separate route.

Someone who has never joined (an admin-only account, for example) is excluded by `Handler.list`
directly from this list for member, even though admin/board still see them.

`MemberListing` (its fields are in [`api.md`](api.md)) carries name, gender, the age category
(see "`users`", "Age category"), the membership number, the joining and leaving dates,
`on_leave`, the federative license **of the current year** if there is one (one from another year
is not shown, and having `license_type` means being federated) and `last_due_year`, the last year
whose due they have paid.

These stay reserved to admin/board:

- The date the license was obtained.
- Its end date, which is not shown because it is always December 31st.
- `last_due_paid_at`, the date the last due was paid.

**Age category.** `age_category` (`youth`, `child`, `junior`, `senior`, `veteran_a`,
`veteran_b`: alevín, infantil, juvenil, mayor, veterano A and veterano B in the club's
regulations) is computed with `ageCategoryFor` from `birth_date`, according to the age the
person reaches in the current calendar year. It replaces the exact birth date, which is not
exposed here: the bracket is enough for what needs to be seen at a glance without revealing
anyone's birth date.

**Roles and Junta Directiva.** `MemberListing` has no `roles` field: an ordinary member
does not need to know which internal roles (`admin`, `money`, `material` or any label an
admin has created) another person has. It is non-public information, like the DNI or the
contact data, and this reduced view exists so as not to show it.

- The only exception is the board position in the Junta, which is public within the club. It
  comes from `GET /board-members`, also open to member, and from there `web/js/members.js` takes
  its "Cargo" column (`cargoFor`, shared by `FULL_COLUMNS` and `MEMBER_COLUMNS`).
  Whoever has no current position shows nothing there, never a role.
- `GET /roles` is the catalog of names and descriptions, read-only for anyone.
  It does not say who has each role, only which roles exist; assigning or removing one remains
  exclusive to admin.

**Kind of member.** Thanks to `on_leave`, "Tipo de socio" (Activo/Baja temporal/Ex-socio)
is computed the same for everyone. The corresponding filter for member does not offer "Nunca ha
sido socio", because `GET /users` never sends a member anyone who has never been a
member.

**Editing data.** On their own record, a member can edit only contact data: `email`, `phone`,
`address`, `postal_code`, `city`, `province` and `emergency_contact`. They cannot change first
name, last name, DNI, birth date or gender: `users.Handler.update` rejects with 403
`restricted_fields` any attempt to touch those fields if the caller is not admin, instead of
silently ignoring them.

**Full record.** `GET /users/{login}` stays reserved to admin/board or to the person
themselves.

### Effect on the frontend

**`/socios`.** It is visible to member as well as admin/board:

- The columns and filters are reduced to what `MemberListing` actually carries
  (`MEMBER_COLUMNS`/`MEMBER_FILTERS` in `web/js/members.js`, instead of the `FULL_*`
  versions).
- The rows are not clickable: `/socio?login=…` is only for admin/board.
- "Nueva persona" carries `data-roles="admin"` so that `applyRoleVisibility()` removes it
  altogether, and `members.js` only loads the role catalog (for the checkboxes of that dialog)
  if the user is admin. The list of roles, to create or delete one, is no longer here: it lives
  on `/admin`.

**`/socio`.** The write buttons (register joining, licenses, Junta Directiva, role
management, delete person) carry `data-roles="admin"` in the static HTML.

The actions generated dynamically for each timeline event (the "Editar" buttons of
each row) are filtered in JavaScript by checking `isRealAdmin`. This is necessary because
`data-roles` never gets to examine content that did not yet exist when
`applyRoleVisibility()` ran.

## Business rules

Each subsection collects the rules of a resource (a table or a group of related tables):
what can be done and what cannot, and why. The columns, constraints and indexes
of each table are in [`database.md`](database.md), and the routes, with who can call them,
in [`api.md`](api.md). The rules that PostgreSQL cannot express with a constraint
(because they depend on several rows or on other tables) are checked in the Go service or
repository, and it is indicated where.

### `users`

Personal data of anyone linked to the club. There is no separate table of access
accounts nor any "grant access" step (the `accounts` table is the chart of accounts, see
"Accounting"): what a session can do depends only on that person's roles (see "`roles` and
`user_roles`"), and those same roles decide whether or not a sign-in link reaches them. There
are no passwords.

The required fields when creating a person are first name, last name and birth date.
`email` and `phone` are optional, but whoever has neither registered never receives a
sign-in link.

#### Identity and login

`login` is the only identifier of a person that crosses the API: no response or
route ever exposes the internal numeric id.

- **Generation**: automatic when the person is created. First and last name go through
  `users.slugify` (accents removed, everything that is not a letter or digit collapsed into a
  hyphen) and are joined with a dot; if that combination already exists a numeric suffix is
  added (`users.Repository.generateLogin`).
- **Editing**: admin only, via `PATCH /users/{login}`. The format is checked with the same
  regular expression (`ErrInvalidLogin` if it does not comply; `ErrDuplicate` if it already
  belongs to another person).
- **Other packages**: `tokens`, `roles`, `licenses`, `memberships`, `board`, `material` and
  `activities` receive a login in their routes and translate it to the internal id with a
  minimal interface defined in each one (`UserLookup`, with `UserIDForLogin`/`LoginForUserID`)
  that `*users.Service` satisfies structurally. They cannot import `users` directly,
  because `users` already imports several of them (`roles`, `memberships`, `licenses`).
- **Mixed listings**: a listing that can mix several people at once
  (`GET /material/movements`, `GET /activities`, `GET /transactions`...) resolves the login with a
  SQL `JOIN` against `users` instead of one query per row. A single-row write
  with `INSERT`/`UPDATE ... RETURNING` (which does not admit `JOIN`) resolves it from a
  login the handler already has at hand, or with a call to `LoginForUserID`.

#### Membership number

`membership_number` lives in `users`, not in `memberships`: it identifies the person while
they are linked to the club, not a specific membership period, so leaving and rejoining
never changes it.

- It is assigned automatically, never by hand, the first time someone joins:
  `memberships.Service.Create` calls `users.Service.EnsureMembershipNumber`, which gives them
  the next number after the highest one at that moment and does nothing if the
  person already has one.
- `memberships` cannot import `users` (it is the other way round), so it exposes the function as
  `memberships.MembershipNumberAssigner` and `cmd/api`/`cmd/seed-admin` wire it up after
  building both services.

#### Age category

The category (alevín, infantil, juvenil, mayor, veterano A, veterano B) is not stored: the
backend computes it on the fly (`ageCategoryFor`) from `birth_date` and the current year
and exposes it as `age_category` in `MemberListing`. `web/js/members.js` only translates it
to its label.

#### Multiple emails and phones

`email` and `phone` can carry more than one value separated by spaces (like
`emergency_contact`, which admits several contacts as free text), instead of a separate
table: matching "any of these" is an array overlap check
(`string_to_array(...) && string_to_array(...)`), and this club-sized scale needs nothing more.

- **Uniqueness**: an email or a phone cannot belong to two people at once, even though
  each field admits several. Postgres cannot express "no row shares an element"
  with a column `UNIQUE`, so `users.Repository` checks it by hand in
  `Create`/`Update` (`emailTakenByAnotherUser`/`phoneTakenByAnotherUser`, see
  [`database.md`](database.md)).
- **Email comparison**: case-insensitive ("foo@x.com" and
  "FOO@X.COM" count as the same address), like in `FindActiveByEmailAndPhone`.
- **Phone comparison**: by digits only, like `FindActiveByEmailAndPhone`;
  "611-222-333" and "611222333" count as the same number. When signing in, moreover, the typed
  phone may be preceded by a "+34", which is ignored (`phoneDigitsForLogin`:
  only a literal "+34", never a bare "34").
- **Why**: if two people shared an email or a phone, a lookup meant to resolve to a single
  person (`FindActiveByEmailAndPhone` for the email, `FindByPhoneNumber` of
  `internal/telegrambot` for the phone) would end up resolving to whichever
  appears first, instead of failing.
- **Signing in**: both signing in and requesting the sign-in link work against
  any of the registered emails/phones (`Repository.FindActiveByEmailAndPhone`).
  The email is compared case-insensitively, like `emailMatchesUser` in
  `internal/telegrambot`, so the same address works the same whether signing in from the web
  or from Telegram.
- **Format**: since the space separates several phones, a single one cannot contain internal
  spaces (write "600111222", not "600 111 222"). Other punctuation, such as hyphens, does not
  matter.

### `magic_link_tokens`

Single-use sign-in links. Together with the link goes a 6-digit numeric code in the
same email, meant for when the email is read on a device other than the one
used to sign in: typing the code on `/login` is more practical than copying a long
URL.

#### Link and code

- Link and code represent the same sign-in attempt in the same row: consuming one
  also consumes the other (`users.Repository.ConsumeMagicLinkToken` /
  `ConsumeMagicLinkCode`).
- The link's token (`auth.NewMagicLinkToken`, 128 bits/16 bytes) is deliberately shorter
  than a session or impersonation token (`auth.NewOpaqueToken`, 256 bits/32
  bytes). It is the only opaque token in this application that ends up in a URL instead of
  always travelling in a header, so its length does matter, and it only needs to resist
  guessing attempts for as long as `MagicLinkTTL` lasts (15 minutes by default), not to keep
  being a valid credential for weeks.
- 128 bits is still the minimum OWASP recommends for a session identifier, and
  is astronomically out of reach for brute force in that window. That matters because
  `POST /auth/magic-link/consume` has no attempt limit of its own: the token's entropy
  is the only defense of that endpoint.
- A 6-digit code is much easier to guess, so `code_attempts` counts the failed
  attempts against each live row of that person and, past a limit
  (`maxMagicLinkCodeAttempts`, 8), stops accepting the code altogether, without affecting
  the link, which stays valid.

#### Request and delivery

- Both are requested at `/auth/magic-link` by giving the person's email and phone. They are only
  generated and sent by email if both match someone in `users` *and* that person
  has at least one of the roles `admin`, `board` or `member` (`auth.BaselineRoles`, see
  "Permissions"): signing in would be of no use to someone who has none, so
  nothing is sent to them.
- `/auth/magic-link` says so when the pair matches nobody who can sign in (404 `unknown_details`,
  the same whether nobody has that email and phone or the person has no role that gives access), so
  that a member who mistyped knows to check it, or to ask the Junta Directiva: the club accepts that
  this tells who has access, which is no secret within it and helps far more than it exposes, and
  the per-address limit keeps it from being scanned. For a match it is a 200 whether or not the
  email was really sent (the cooldown), so it cannot be used to send unwanted email.
- `POST /auth/magic-link/consume-code` identifies the person by email+phone just like
  the request itself (the code alone is too short to serve as a lookup key) and always
  answers with the same generic 401, also so as not to distinguish cases from the
  outside.

#### Brakes against abuse

- **Per-person cooldown** (`MagicLinkCooldown`, 60 s by default): if a link was issued to them
  less than that ago, another is not issued, with the same identical response.
- **Per-origin-address limit** (`httpapi.IPRateLimiter`, 20 calls every 15 minutes
  by default), shared by `POST /auth/magic-link` and
  `POST /auth/magic-link/consume-code`. The latter needs it because its lookup by
  email+phone happens before reaching the `code_attempts` counter. `consume` does not have it:
  its defense is the token's entropy.

#### Delivery by Telegram

Details of the bot in "Telegram".

- If the person has their Telegram account linked, they receive the same link by Telegram
  in addition to the email, never instead of it: whoever has not linked it, or if that send
  fails, still has the email.
- The logic for issuing the link (checking the role, the waiting time, creating the token,
  sending it) lives in a single internal place, `authapi.Handler.issueMagicLink`, used
  both by the web request and by the Telegram bot (`internal/telegrambot.Bot`): whoever already
  has the account linked, writes to the bot and proves their email gets the same link.
- **Web request**: the DM is fire-and-forget and the email is the fallback; there
  `FindActiveByEmailAndPhone` guarantees there is an email.
- **Bot flow** (`viaTelegramBot`): the DM is the only channel. `issueMagicLink` returns right
  after sending it and sends no email, because the DM already is the answer to what the
  person has just typed. That is why the send is synchronous and, if it fails, the error
  (`errMagicLinkTelegramDMFailed`) reaches `Bot.handleRecognizedChat`, which answers with
  `magicLinkDeliveryFailedMessage` instead of leaving the person without an answer.

#### Text of the DM

- The email (which may be read on another device and need not be answering anything
  the person has just done) always includes the sign-in code and an "if you did not
  request this, ignore this message".
- The DM copies that as is when it simply accompanies a web request.
- When it is the bot itself answering someone who has just typed their email to it
  (`handleRecognizedChat`, see "Telegram") it omits both: there is no page asking for
  that code at that moment, and the message is precisely the answer to what that
  person has just sent, not something that could arrive without their having asked for it.

#### Deleting the link DMs

A DM with a link and a code that no longer work only confuses, so it is deleted when they
expire, whether or not the person has signed in; and earlier, as soon as the person signs in
with either of the two, or as soon as a more recent link is sent to them: it only makes sense
to keep the last one. It applies to the bot's DMs and to those that accompany a web request;
only the time limit changes, the one each message states about itself.

- **Timer**: it is sent with `Client.SendMessageForID` instead of the normal `SendMessage`,
  precisely to keep the message id, and a goroutine, launched only after a successful
  send, waits for the time limit and calls `Client.DeleteMessage` with that id. The time limit of
  the bot's DM is `TelegramBotDMLinkTTL` (5 minutes by default, shorter than the real
  `MagicLinkTTL` of 15 minutes, which remains the time during which the link
  works; the message text mentions those 5). That of the DM of a web request is
  `MagicLinkTTL`, which is what its text says, because its code can still be typed from
  another device until then.
- **Sign-in**: when the person signs in (`Handler.issueSessionAndRespond`,
  whether the link or the code was consumed, and from whichever device),
  `Handler.deletePendingTelegramDM` immediately deletes that person's pending DM and
  cancels its wait: once signed in there is no point in leaving it.
- **A new link**: when a link DM is sent, that person's previous one is deleted
  (`setPendingTelegramDM` returns the one it replaces and `deleteTelegramDMs` deletes it),
  whatever the flow of each. If two consecutive requests finish sending out of
  order, the one with the lower id is deleted, which is the oldest (Telegram ids grow
  within a chat).
- **Coordination**: an internal map of the handler (`pendingTelegramDMs`, with its own
  mutex, one message per person: the latest) lets the timer, the sign-in and a new link
  delete each message without another trying to delete it as well
  afterwards. The send of a web request's DM goes in the background and records the id of the
  message, and schedules its deletion, as soon as it receives it.
- **Scope**: it is only hygiene, not something security depends on. If the deletion
  fails, or the process restarts in the meantime, the link stops working anyway
  when it expires; but an expired one that stays there is confusing, and it is one more thing
  exposed if someone later gains access to the phone or the session.

### `roles` and `user_roles`

Catalog of roles (manageable: create, delete) and who holds which (many-to-many relation).

#### Kinds of role

The system ones (`admin`, `board`, `member`, `former_member`) are the only ones to which the code
gives permissions or automatic behavior. Any other role an admin creates is an organizational
label: it unlocks nothing by itself unless explicitly programmed (`material` and `money` are of
this kind and do have an effect, see "`material_items` and `material_movements`" and "Accounting").
The automatic ones (`member`, `former_member` and `board`) are neither assigned nor removed by
anybody by hand: `POST`/`DELETE /users/{login}/roles` reject them with 409. Which roles exist and
which of them are system or automatic ones is in [`database.md`](database.md).

#### How automatic roles are computed

They are computed on the fly; `roles.Repository.ListNamesForUser` is the only place that knows
how.

- `member`: from `memberships`, if the person has a member period active right now.
- `former_member`: if they have had some period before but none active today.
- `board`: from `board_members`, if they have a board (Junta Directiva) position open right now.

That computation is what resolves every authenticated request (see "Authentication and
sessions"), so permissions based on "I am a member" or "I am on the board" always reflect the real
state of joining, not a label somebody forgot to remove.

#### Removing the `admin` role

`admin` is assigned and removed by hand, but `DELETE /users/{login}/roles/{roleID}` has two
safeguards of its own, besides the automatic-role check:

- **`self_admin_removal`** (409): an admin cannot remove their own role, even when there are
  plenty of other admins. That click is easy to make by mistake (the "×" button on their own row
  in `/socio`) and, unlike losing any other role, nobody else is looking at that moment to give it
  back: it has to be another admin who does it.
- **`last_admin`** (409): the last person with admin cannot be left without that role, so that
  there is always somebody who can manage people and roles.

`roles.Service.Remove` checks `self_admin_removal` first (`actorID == userID`), since it is the
more specific rule and does not depend on how many other admins exist.

### `tokens`

Each person's session token: opaque (its SHA-256 hash is stored, never the plain value), revocable
and with an expiry that moves with use: it expires 30 days after its last use (3 hours when it was
opened through a `local.…` address, see "Short sessions" below), every request renewing it. Its
fields are in [`database.md`](database.md) and "Authentication and sessions", further down, explains
how each request is resolved. An admin can see, re-expire or revoke any person's sessions and list
everybody's (see [`api.md`](api.md)); they never see the value of a token, only its metadata.

#### Short sessions

The club has a tablet that anyone uses, and a session left open on it should not last a month. It is
given its own address, `local.` followed by the public one (`local.intranet.katiuskas.es`: the same
site, another name for it), and the rule is about the address, not about the device: a session whose
sign-in is finished (the link opened or the code typed) through an address whose first component, up
to the first `.`, is `local`, expires `config.LocalTokenTTL` (3 hours) after its last use instead of
30 days, the cookie too (`authapi.Handler.sessionTTL`; the address is the `X-Forwarded-Host` the
reverse proxy adds, else `Host`). The login page is the same on every address except that on a
`local.…` one it opens with a prominent "Sesión local" notice saying that the session will close by
itself in a few hours, and nothing marks or remembers a device on the server:
what is the tablet's is only its bookmark, and anybody may use that address, which can only give
them a shorter session. Because it is another address it is also another site for the browser — its
own storage and cookie, so being signed in on the tablet and on the main address are two separate
things.

The link in the sign-in email has to lead to the same address, or the person who asked for it on the
tablet and opens it there would finish at the main one and get the 30-day lifetime: for a request
made through exactly `local.<the public address>` (built from `PUBLIC_BASE_URL`) the link points
there (`Handler.linkBase`); for any other, whatever the `Host` says, it points to the public
address. That matters because the `Host` of a request for somebody else's link is whatever an
attacker sends, and it must never decide where an email to a victim points. Impersonation sessions
keep their own, shorter lifetime; the one an admin gets back on leaving an impersonation follows the
same rule as any other. Setting it up (DNS and the reverse proxy's site for `local.…`) is in
`docs/environments.md`. The session's expiry is in "Mis sesiones".

#### Impersonation

`POST /users/{login}/impersonate` (admin only, not board: acting as anybody, another admin
included, is a bigger leap than the rest of what board can already do) creates a token whose
`user_id` is that person's, with an `impersonated_by` (the admin's id) stored alongside it.

- **Effect**: since roles are resolved live from `user_id`, as in any normal session, that session
  can only do the same as that person, with no special case in the rest of the API.
- **Exceptions in `authapi`**: `GET /auth/me` adds `impersonated_by` and
  `impersonation_seconds_left` to the response (so that the frontend can warn about it, and say how
  long is left) and `POST /auth/impersonate/exit` lets the admin leave the impersonation whatever
  happens to the impersonated person's roles at that moment. That is why that endpoint only requires
  `RequireAuth`, not `Protect` (which would require a baseline role): if the person had lost it
  while being impersonated, the admin would be left with no way out.
- **Leaving**: the API itself does not reuse the token the admin had before impersonating, because
  it never again has the plain value of a token already issued: `POST /auth/impersonate/exit`
  revokes the impersonation session and issues a new one for the admin. But the *browser* does have
  that value: the frontend keeps the admin's own token when the impersonation starts
  (`katiuskas:original-token`, see `docs/frontend.md`) and goes back to that session, on leaving and
  when the impersonation ends (see "Duration"), and uses the endpoint above only when it was not
  kept or no longer works.
- **Restrictions**: only somebody who currently has some baseline role (`auth.BaselineRoles`) can be
  impersonated, just as a sign-in link is not sent to somebody who lacks one, and never oneself
  (nor, from inside an impersonation, the person one already is in it, nor the admin behind it).
- **Chains**: an admin who impersonates an admin, who impersonates a third person, is recorded for
  who is really behind it: the third person's session says it was started by the *original* admin
  (`impersonated_by` is the admin behind it, never the person they were acting as), so the chain
  never gets longer than one, the banner and the session lists name the admin who is really behind
  it, leaving goes back to that admin (`POST /auth/impersonate/exit`, like the frontend's own return
  to the saved session), and the Telegram notice names the admin, the person they were acting as and
  the third.
- **Duration**: `config.ImpersonationTokenTTL`, 1 hour against the 30 days of a normal token. A
  session acting as another person is too big a risk compared with a normal one to leave it alive
  for a month if somebody forgets to leave. Its expiry is fixed: it ends an hour after it began, use
  or no use. The banner shown while impersonating says how long is left and counts it down, and
  reloads the page by itself when it reaches zero, so nobody takes a page that stayed open for
  somebody else's session once it is not; when it ends, or is revoked, the next request the page
  makes is a 401 and the frontend puts the admin back in their own session, with a note, instead of
  signing them out.
- **Acting with other roles**: to see a page as somebody with other roles, an admin does not need to
  find someone to impersonate: `PUT /auth/role-override` makes their own session act for an hour
  with the roles they choose (any existing roles, as long as one of `admin`, `board` or `member` is
  among them). Their real roles do not change, only what the session is allowed to do, and it ends
  by itself or with `DELETE /auth/role-override`. Only a real admin's own session can (not an
  impersonation), and being a real admin is what lets them change or end it even when the roles in
  force no longer include `admin`.

### `memberships`

Periods of joining/leaving as a member. A person may have several periods as long as they do not
overlap.

**Rules**

- A period cannot overlap another of the same person (the database prevents it).
- `users.Service.enrichMany` (what `GET /users` and the page of a single person use) asks each
  package, once and for the whole list, for what it adds to each `User` (`ListByUsers`,
  `OnLeaveAmong`, `LatestDues`, `CurrentByUsers` and `ListNamesForUsers`, one query with
  `ANY($1)` each): before, it was five queries per person, and with 300 members `GET /users` took
  about 215 ms instead of about 10. With each person's periods (most recent first) it separates
  the two dates it adds to each `User`:
  - `membership_start_date` is that of the *first* period (when they first joined, not the last
    time they rejoined after leaving).
  - `membership_end_date` is that of the *most recent* period (empty if they are still a member).
- This way, somebody who has been in the club several times still shows their original joining
  date, and the list of people can show and sort by joining/leaving date without a separate
  request per person.
- Correcting a period (`PATCH /memberships/{id}`) revalidates against it the temporary leaves of
  that same person that fell inside the period *as it was before* the edit: narrowing a period
  cannot be blocked by a leave that belongs to another period of theirs.
- Deleting a period (`DELETE /memberships/{id}`) does not cascade: nothing references it. It is
  meant for the frontend's "undo my last action" in `/socio`, which only offers it for the
  person's most recent joining/leaving/temporary-leave entry; that restriction lives in the
  frontend, not in the endpoint.

### `membership_dues`

Annual membership dues, next to `memberships`. Each year that a member remains one they owe a due
that covers it: the first, together with the enrollment fee (matrícula), on the same day they
join, and then one more each following year.

**Key points**

- A due with no transaction behind it does not mean anything yet, so, unlike a joining period,
  there is no row for "unpaid"; that is why `transaction_id` is mandatory.
- Keyed by `user_id` on purpose, not by `membership_id`: the due of a year of somebody who rejoins
  is not naturally tied to one particular period, and this way a year never needs to be matched
  against the boundaries of a period.
- A due is deleted together with its transaction (`ON DELETE CASCADE`: it is pure bookkeeping
  annotation, meaningless apart from it), whereas a license uses `ON DELETE RESTRICT`: it is a
  real record with a meaning of its own (proof of being federated) even apart from its payment, so
  the transaction that paid for it cannot be deleted from under it.

**Recording (`POST /users/{login}/dues`, admin only)**

- It is `Service.RecordDue`. Before anything else it checks the transaction: it has to exist
  (`ErrUnknownTransaction`) and have at least one entry on an account marked `allows_dues`
  (`ErrTransactionNotForDues`, 409 `transaction_not_for_dues`; see "Accounting"). This way, a
  rejected due never leaves a joining period open as a side effect.
- If the person has no joining period yet, it is treated as their first due and a new period is
  also opened for them (with the `start_date` of the request). Somebody who already had a period
  and rejoins is left entirely to the normal joining flow, separately.
- That is why `start_date` is optional: it is only needed (400 `start_date_required`,
  `ErrStartDateRequired`) when there is no period to open, and it is ignored if the person already
  has one; a malformed `start_date` is still 400 `invalid_date`.

**Reading (`GET /users/{login}/dues`)**

- It joins each due's transaction to add `paid_at` (the payment's `occurred_at`, not `created_at`:
  the due of a past year may be recorded long after it was paid). It only appears in this listing,
  never in the response of the `POST`.

**Frontend and limits**

- In `/socio`, each due is one more event ("Cuota anual · year") of the "Historial como socio"
  timeline, with the date of the payment and no edit or delete buttons. `/perfil` shows the same
  timeline, without buttons.
- There is no endpoint to correct or delete a due, nor any validation that the year falls inside
  an active joining period; both things are out of scope on purpose for now.

### `membership_leaves`

"Temporary leaves": a window with a start date and an optional end date (empty for a leave still
in progress, like the `end_date` of a member period).

It hangs from the person (`user_id`) directly, not from one particular `memberships` period. This
way, a single `GET /users/{login}/leaves` covers all the leaves they have had, even if they were
during different periods, without querying period by period. Two leaves of the same person cannot
overlap (the database prevents it, as with periods).

**Rules**

- The window has to be *contained* in one of that person's periods. `memberships.Repository`
  cannot express this with a `CHECK` between two tables, so it validates it in Go: it looks for
  which period (if any) fully contains those dates before each `INSERT`/`UPDATE`
  (`ErrLeaveOutsideMembership`, `Repository.findContainingMembership`).
- A temporary leave does not touch the member period it belongs to, which stays open during it.
  That is why `users.User.OnLeave` (`on_leave` in the JSON) is the only place that tells "active"
  from "temporary leave" in the list of people; it is computed only for somebody who already has
  an active period, with `memberships.Service.IsOnLeave`.

### `licenses` and `license_types`

Federative licenses and the catalog of types. The type is not free text: it is chosen from a
catalog (`license_types`, migration 00007) stored in the database instead of a Go enum, because
the club expects the list to change (a new zone or federation) and that should only need an
`INSERT`, not a deployment.

**Catalog**

A license of type "Otra" additionally requires `other_detail` saying which one it is; that
validation lives in `licenses.Service.Create`, because a `CHECK` cannot reach
`license_types.is_other` from another table. The catalog (which types exist) is in
[`database.md`](database.md).

**Year and payment**

- `year` is the calendar year it actually covers, not always that of `issued_date` (a license
  requested in December usually covers the following year). It is valid until December 31st of its
  `year`: `valid_until` is computed in Go and never stored, like the age category.
- `transaction_id` points at the transaction that paid for it (see "Accounting") and is not
  editable afterward: correcting type, date or year is a different action from deciding which
  payment it belongs to.

**Creation rules (`Service.Create`)**

- Without `transaction_id` is the only way to record a license the club has not collected (for
  example, somebody federated elsewhere), and then there is nothing else to check about the
  payment.
- If given, it has to exist (400 `unknown_transaction`) and have at least one entry on an account
  marked `allows_licenses` (migration 00017; 409 `transaction_not_for_licenses`). That check is a
  SQL query against the accounting tables, like the foreign key, without importing
  `internal/accounting`, and it goes last, after everything that depends only on the request.
- A license can only be assigned to somebody who was a member at some moment of the license's year
  (`year`; `memberships.Service.WasMemberDuringYear`), not necessarily right now nor on the day of
  `issued_date`. That is why a license can be recorded for a year in which somebody was indeed a
  member after they have left, or with a date earlier than their joining of that year. With a year
  that touches no member period, 409 `not_active_member`.

**Correction**

- `/licenses/{id}` goes by license id, not nested under the user, like `/leaves/{id}`.
- `PATCH` does not require membership in the year again, because it corrects an existing license
  instead of assigning a new one; it keeps working even if the person has already left.

**Use elsewhere**

- `users.Service` adds to `User` only each person's license of the current year
  (`licenses.Service.CurrentByUser`; one from another year does not count), whereas from the
  member period it takes the most recent. That is what `GET /users` shows.
- In `/socio`, each license appears as one more event of the joining and leaving timeline, not in
  a separate section.

### `board_members`

Who has been on the board (Junta Directiva) and in which position, with the same shape as
`memberships`: start and end date (empty = still in the position).

**Rules**

- A person cannot have two positions open at the same time, whether or not they are the same one;
  two people can hold the same position at once (several members-at-large (vocales) at the same
  time is normal).
- The position is validated in Go before touching the database (`Position.Valid`,
  `ErrInvalidPosition`, 400 `invalid_position`), both when creating and when correcting: the
  PostgreSQL enum would reject it anyway, but as a 500.

**Relation with other packages**

- The `board` package depends neither on `users` nor on `memberships` to compute the automatic
  role: it is computed with a direct SQL query against `board_members` from the `roles` package,
  like `member`/`former_member` against `memberships`. That is why it does not need the interface
  arrangement that `membership_number` does (see "`users`").
- Its `handler.go` does define, for another reason, the same minimal `UserLookup` interface as
  `tokens`/`roles`/`licenses`/`memberships`/`material`/`activities`: translating the login of its
  `/users/{login}/...` routes into an id.

**Deletion and frontend**

- As with a joining period, nothing references it by foreign key, so deleting never cascades. But
  here there is no "closing half" to go back to by clearing a field: being a single row with start
  and end together, deleting always removes the whole position, whichever of the two events it is
  clicked from.
- In `/socio`, a position appears as two more events (start and end) in the joining and leaving
  timeline, each with its "Editar" button; only the start one also carries "Borrar".

### `activities` and `activity_participants`

Club activities and who takes part. Creating an activity is open to any member with access (not
only admin/board), who can also sign up the initial participants in one go.

**Organizer and editing**

- Every activity has a mandatory organizer (`leader_id`, exposed as `leader_login`), which by
  default is whoever creates it.
- Only admin/board can name another person when creating it or reassign it afterward with `PATCH`
  (403 `forbidden` for anybody else, also for the organizer themselves, who can edit the rest of
  the fields).
- Editing or canceling it is reserved for that organizer or admin/board (`Handler.canManage`,
  which queries the activity to compare `leader_id` with the authenticated user).
- The type (`caving`, `canyoning`, `training`, `social`, `other`) can also be changed when
  editing.

**Validation**

- `type`, `status` and `visibility` are checked in Go against the values of their enums
  (`Type.Valid`, `Status.Valid`, `Visibility.Valid`): an unknown one gives 400 `invalid_type`,
  `invalid_status` or `invalid_visibility`.
- `end_date` cannot be earlier than `start_date` (400 `invalid_date_range`). When editing, it is
  compared with the already merged values, so sending only one of the two dates is fine. The
  database constraint `end_not_before_start` remains as the last barrier.

**Who can take part**

- Any `member` (a socio now) can be signed up to any activity, whatever its type (caving, canyoning
  or any other), and only a member: somebody who is not one (never was, or is a former member) is
  refused with 409 `not_a_member`, whoever signs them up and whether it is when joining later or
  when naming initial participants at creation.
- Being federated is not required for any type. The list of participants says, for each, whether
  they have a federative license of the current year (`federated`, from `licenses.Service`), as
  information.

**Visibility and participants**

Each activity has a `visibility` (`public`, the default, or `private`; migration 00005) that
decides who can add participants, not only who can sign up. `Handler.join` queries `leader_id` and
`visibility` (and, for a private one, whether the caller is signed up) before delegating to
`Service.Join`, which records who is doing it (`added_by`) and when (`added_at`); `Handler.leave`
(`DELETE .../participants/{login}`) does the symmetrical thing.

| Action | Public activity | Private activity |
|---|---|---|
| Sign oneself up | any member | only the organizer, board or admin (and somebody already signed up, who is in) |
| Add another person | anybody | organizer, board, admin or somebody already signed up |
| Remove oneself | any member | any member |
| Remove another person | admin/board, or whoever signed them up | organizer, admin/board, or whoever signed them up |

**Listing and holidays**

`GET /activities?active=true` leaves only what still needs attention: what is not cancelled and has
not ended (its `end_date` is today or later), and any activity, over or cancelled, with some loan or
rental of material not yet returned. An activity's state is not stored: only `cancelled` is (set
with `cancelled` on `POST`/`PATCH`, which also reactivates it with `false`); whether it is still to
come (*Planificada*), going on (*En curso*), over (*Realizada*) is worked out from its dates by the
frontend (`activityState` in `api.js`). Holidays (`holidays`) are managed by the same `activities`
package and are used only by the frontend's calendar view.

### `activity_resources`

The memoria of an activity: an ordered list of resources written by whoever took part in it after
the trip. Each resource is a text entry (title + `description` in text/Markdown) or an uploaded
file (title + the file, normally a photo or a PDF).

**Storage**

- The metadata lives in PostgreSQL. The content of a file resource lives apart, on disk
  (`config.ActivityResourcesRoot`), indexed by the id of the row itself. It is a named Docker
  volume, not a bind mount like `library`/`userfiles`, because here the files only arrive through
  `POST /activities/{id}/resources`, never put there by hand.
- A file is created once and never modified: there is no way to replace its bytes (the resource is
  deleted and another is added), and `Storage.Save` creates it with `O_EXCL`, so it refuses to
  overwrite one that already exists instead of truncating it. Deleting only removes a directory
  entry.
- That allows another directory tree to share the same files with hard links (see
  [`environments.md`](environments.md)) without a write in one changing the other.

**Permissions**: reading the memoria is open to any baseline role; adding resources or reordering
it can be done by admin/board, the organizer or anybody who is listed as a participant (the
organizer is not necessarily a participant, so both things are checked separately); correcting or
deleting a particular one, only whoever wrote it or admin/board. The order is replaced whole at
once, and a list that does not match the current resources exactly is rejected instead of trying
to reconcile it.

### `material_items` and `material_movements`

Catalog of the club's material and its movements. The movements go in a single table with a type
discriminator (`type`) instead of a table per type, and several `CHECK`s prevent meaningless
combinations (a sale with a return date, for example).

Every item has a type (`material_types`): two are fixed, Cuerda and Saca, and give the items their
rope and bag shape; every other one, which an admin edits freely, gives the general shape. So the
catalog items have three shapes (the item's `kind`: `rope`, `bag` and `general`), each with its own
fields; the details and what is mandatory in each shape are in [`database.md`](database.md). The
movements go in a single table with a type discriminator instead of a table per type, and several
`CHECK`s prevent meaningless combinations (a sale with a return date, for example).

**Rules**

- A rental is priced per week (`rental_price_per_week`), not per day, and is closed in one go with
  `returned_at` and `status='active'→'completed'`. `PATCH /material/movements/{id}/return` only
  applies to an open rental.
- A loan or a return is always linked to an activity (`material_movements.activity_id`, required by
  another `CHECK`).
- A return is not tied to any particular loan: it is validated in Go against how much of that item
  the activity still has outstanding (`SUM(loan.quantity) − SUM(return.quantity)` for the same
  `item_id`/`activity_id`). This way 10 carabiners can be lent and then 15 more, and 20 returned
  followed by the remaining 5, without matching each return with a loan.
- `total_quantity`, `available_quantity` and `outstanding_loan_quantity` of each item are never
  stored: they are always recomputed from its history of movements (`material_item_quantities`, a
  view).

**Permissions**: creating or modifying a movement requires `admin`, `board` or the optional role
`material`, which is seeded but is not a system one (a club that does not want that distinction can
delete it and keep using `board`); **who material goes to**: a loan or a rental (and the return of a
loan) is only for a `member` (a socio now), a sale for a member or a former member, and anybody else
is refused (409 `recipient_not_member`, `recipient_not_member_or_former`); adding material to the
catalog and editing it (`PATCH /material/items/{id}`:
not its type, and not its quantities, which only movements change) is for the same three. An item's
`code` is unique (409 `duplicate_item_code`). The list of types itself is changed by admin
alone (`POST`/`PATCH`/`DELETE /material/types`): names are unique (409 `duplicate_type`), Cuerda and
Saca can be neither renamed nor deleted (409 `fixed_type`) and a type with items is not deleted
(409 `type_in_use`).
### Distributions (`distributions`, `distribution_options`, `distribution_units`)

A distribution is something the club hands out to its members: shirts, buffs, a rope, mugs,
stickers. Each distribution is a single item; if two things have to be handled separately,
they are two distributions, except for the case of **articles** (below), for things that can be
asked for in any quantity. It is created by admin or board (everybody else can see it through
the API but not write it, except for asking for units and editing their own).

- **Audience and price.** A distribution is for `all_members` (everybody who was a member on
  `cutoff_date`, which is then required) or `signup` (only those who sign up; `cutoff_date` is
  optional and, when set, only those who were members that day can sign up: the check looks at
  the periods in `memberships`). `price` is that of the own unit (0 if it is free); the presence
  of `extra_price` is what makes extra units possible, and `max_extra` limits them per person
  and requires `extra_price`. `closes_at` is the deadline for members to choose.
- **Status** (`status`): `draft` (in preparation: it can be edited and only admin and board see it),
  `open` (members ask for units and choose the options), `closed` (members can no longer ask for or
  change anything; staff — admin, board and material — still can, see "Who can order") and `done`
  (finished). In the interface: "Borrador", "Abierto", "Cerrado" and "Terminado" (Draft, Open,
  Closed, Finished). It moves only one step at a time, forward or back. Handing out (recording
  collections and numbers) is possible with the distribution in any status except `draft`
  (`Status.Deliverable`), because the order usually arrives before the requests have been closed and
  whoever has not come for their share can do so once it is finished; in `draft` it is not (409
  `not_deliverable`).
- **Options** (`distribution_options`): what has to be chosen, with its possible values
  ("Talla": S, M, L). A distribution has as many as it needs, or none. They can be changed as
  long as nobody has asked for anything; afterwards, a `PATCH` that resends them identical does
  not count as changing them, but a different one gives 409 `has_units`.
- **Articles** (`distribution_articles`, migration `00024`): instead of being a single item with
  its own price, a distribution can hand out several that each member asks for in whatever
  quantity they want, like the Christmas lottery: "papeletas" at 5 € (200 in all) and "décimos"
  at 20 € (1000 in all). Each article has `name`, `price` (one unit) and an optional
  `max_total`: how many units there are to share among everybody (some may stay undistributed,
  but no more than what exists is handed out; without `max_total` there is no cap). There is no
  limit per member. A distribution with articles is always `signup` and has no `price`, extras
  or options of its own (400 `articles_conflict`); without articles everything works as before.
  Each person has one row per article (or more: see below), with `quantity` (the unit is still
  a row, but this row is worth as many units as `quantity`; `price` is what ONE cost, so the row
  costs `price × quantity`; without articles `quantity` is always 1), so that 200 "papeletas"
  do not have to be marked as paid or collected one by one. They are asked for with
  `POST …/units` and `article_id` + `quantity`, as many times as needed: asking again for the
  same article adds to that person's row that has not been collected yet (what was already paid
  stays as it was, so what is added is unpaid) or, if all of them are collected, makes a new one
  (`CreateArticleUnit`; there is no longer a unique index per person and article, migration
  `00027`). `max_per_person` counts all of the person's rows. It is changed with
  `PATCH …/units/{id}` and `quantity` (with the same status rules as any unit) and removed with
  `DELETE`. The cap is the total for everybody: `CreateArticleUnit` and `UpdateQuantity` lock
  the distribution's row while they count what is left, and going over gives 409
  `article_exhausted` (with how many are left). `Article.requested` says how many have been
  asked for, and `unit_count`/`picked_up_count` count units, not rows. Articles can only be
  changed while there are no units; afterwards only `max_total`, and not below what has already
  been asked for (409 `has_units`, 409 `cap_below_requested`). `GET …/order` groups by article.
  In the interface they are written one per line, "Papeletas: 5, máximo 200".
  In addition, each article can have `max_per_person` (how many one person can have; going over
  gives 409 `article_person_limit`) and `has_identifiers` (migration `00024`): each unit carries
  its number —the serial number of a "papeleta", series and fraction of a "décimo", free text of
  up to 60 characters—, which is not given when asking but when handing out. They are recorded
  with `PUT …/units/{id}/identifiers` (admin, board or material, only with the distribution out
  of `draft`, in the table `distribution_unit_identifiers`, with a number that is unique per
  article: 409 `identifier_taken`), there can be fewer than `quantity` and they can be completed
  later, but a row with numbers cannot be collected until it has all of them (409
  `identifiers_missing`, checked in `Service.MarkPickedUp`). With units already asked for,
  `max_total`, `max_per_person` (never below the largest order, 409 `cap_below_requested`) and
  `has_identifiers` can be changed. In the interface: "Papeletas: 5, máximo 200, 10 por persona,
  con números", and `/reparto-pedidos` allows assigning them.
  **Described numbers** (migration `00026`, `identifier_fields`): instead of typing the numbers
  one by one, the parts of the number and the values of each one are described, as a list of
  values and ranges (`numbering.go`): "papeletas" "Número: 0001-0200" (leading zeros are kept),
  "décimos" "Serie: 124-133, 137" and "Fracción: 1-10". Every combination is a unit that exists
  (the first part changes more slowly: 124/1…124/10, 125/1…), so it is known which ones exist,
  who has each one and how many are free. The article's maximum becomes how many there are
  (`max_total` is always recalculated, even if another value is sent) and the article carries
  numbers even if "con números" is not said. A unit's number is stored as its parts joined by a
  slash (`124/3`; no value may contain a slash). When recording numbers by hand, ranges are
  valid in the last part and by position in its values, so a gap is respected (`133/9-10`,
  `0010-0020`); every number has to exist. To avoid typing, `POST …/units/{id}/identifiers/assign`
  gives a row the next free numbers up to its quantity (409 `not_enough_numbers` if there are
  not enough), and `POST …/pickups` with `assign_numbers` does it with the rows that lack them
  when handing out (`Service.assignNumbers`, in the order they arrive).
  `GET …/articles/{id}/numbers` (admin, board or material) lists all the numbers with who has
  each one and the counters. With orders made, the numbers can be extended (another series
  bought: the 137 after the 133) but a number somebody has cannot be removed (409
  `number_in_use`). In the form each part goes on an indented line under the article
  ("Décimos: 20", "  Serie: 124-133, 137", "  Fracción: 1-10"), and `/reparto-numeros?id=…`
  shows the list with filters.
- **Units** (`distribution_units`; in the interface they are called "pedidos" (orders), and the
  extra units, "extras"): one row per unit asked for, so that two extra shirts can have
  different sizes. `beneficiary_id` is who it is for; `registered_by_id` and `registered_at`,
  who actually entered it and when (somebody else when a parent signs up their child or a
  friend adds extras for another), and they do not change when the choices are edited. `price`
  is what it cost when it was entered: changing the price afterwards does not touch what was
  already ordered. Each person has at most one own unit (unique index) and the extras that
  `max_extra` allows, counted by beneficiary, whoever enters them (`CreateUnit` locks the
  distribution's row while it counts them).
- **Automatic own unit** (migration `00022`): in an `all_members` distribution nobody has to
  sign up. As soon as the distribution stops being a draft, everybody who was a member on
  `cutoff_date` has their own unit, with no choices and nobody as `registered_by`
  (`registered_by_id` admits `NULL`), at the price the distribution has at that moment.
  `Repository.SyncAutomaticUnits` creates them (`INSERT … ON CONFLICT DO NOTHING`, so it can run
  at any time and concurrently): `Service` calls it when the distribution is opened and before
  listing units, asking for one, producing the summary and recording a collection, so that a
  member who meets the requirements later also appears. With options to choose, a unit with
  empty `choices` is one nobody has chosen yet ("Sin elegir"). Whoever chooses it —the member
  themselves with `PATCH`, or anybody with `POST … /units` in their name, which fills this one
  in instead of creating a second one— becomes whoever entered it, with the date; if there are
  no options to choose, asking for it again gives 409 `already_registered`. The own unit of an
  `all_members` distribution cannot be removed (409 `automatic_unit`): it would be created
  again. Since units exist from the moment it is opened, the options are locked (`has_units`)
  from then on. The distribution itself can be deleted (admin only; board creates and edits but
  does not delete): it takes its units with it, with whatever the members have chosen, unless
  one is paid or collected (409 `has_settled_units`), in which case those marks have to be
  undone first. The interface warns how many units are lost. A price change reaches the
  automatic units nobody has touched (not chosen —no `registered_by`—, not paid and not
  collected), so correcting the price with the distribution already open does not leave them on
  the old one; what somebody has already chosen, paid or collected keeps what it cost. **Going
  back to `draft`** starts the distribution over: all the units are deleted —everybody's own,
  the extras and, in one people sign up for, the sign-ups—, also whatever somebody may have
  chosen, which may not fit the options about to be given; the automatic ones of an
  `all_members` distribution are created again, empty, on opening it. If any is paid or
  collected it prevents this (409 `has_settled_units`) until those marks are undone. The
  interface warns beforehand that the data will be deleted, if there is any.
  **Changing the audience from `all_members` to `signup`** (in the same transaction of
  `Repository.Update`) deletes the automatic units nobody has touched —not chosen, not paid and
  not collected—, because they existed only because everybody was in; the ones somebody did
  choose stay, because that is somebody who asked for it, which is what signing up means, and so
  do the extras. An untouched one that is paid or collected prevents it (409
  `has_settled_units`). The edit dialog asks first.
- **Who appears in the pickers**. None of them offers everybody, only members
  (`GET /distributions/{id}/people`): to order, those who can have it —the members on
  `cutoff_date`, or today's if there is none—; to hand out (`for=pickup`, admin, board and
  material), also today's members, because somebody who became a member later can collect for
  others. Without `cutoff_date`, a `signup` distribution is for today's members
  (`Service.IsEligible`).
- **Visibility** (`visibility`): who can see a distribution at all, and so act on it. `all` (the
  default: everything works as described here), `material` (material, board and admin), `board`
  (board and admin) or `admin` (admin alone); each level includes the ones above it, in line with
  "whatever material can do, board can do". Below that level, every route about it — the list, the
  distribution, its units, the order, the payments, the numbers — answers 404, as if it did not
  exist. A draft is for admin and board whatever it says. Only admin sets or changes it. The
  accounting ledger is a separate matter: a payment's transaction names the article in its
  description, and the ledger is open to whoever can read accounting.
- **Who can order**. Any member, for themselves or for others (`beneficiary_login`), while the
  distribution is open and `closes_at` has not passed; admin, board and material (who put the order
  together, hand it out and fix orders) at any time but in a draft, so also after the date and while
  it is closed or done (409 `not_open` for the rest). Both the beneficiary and whoever enters it
  have to be eligible, that is, members on `cutoff_date` (409 `not_eligible`); admin, board and
  material, who enter on behalf of anybody, do not need to be (the beneficiary does). Extras belong
  to whoever asks for them (or to the member in whose name they are entered): who ends up with them
  is not said. Changing or removing a unit can be done by the person it is for, whoever entered it
  and admin/board/material (all three for anybody, and also after the deadline and with something
  paid; only admin/board mark payments), under the same rule of when; what has been handed out is
  not taken away from the owner (409 `unit_locked`), and a unit that is not an article row, once
  handed out, is touched by nobody; a paid one is touched only by admin/board/material (409
  `unit_locked`).
- **Units with and without a number**: in an article with numbers (`has_identifiers`) an order
  is a quantity (`quantity`, which can be increased or reduced), and some of those units can
  have a specific number (an identifier) assigned; the ones that do not are "any" units.
  **Only the ones with a number can be paid or handed out**, and it is said which ones: each
  identifier carries its `paid_at` and its `delivered_at` (migration `00029` for payment), so it
  is known which numbers are paid, which are handed out and which are only assigned
  (`paid_identifiers`, `delivered_identifiers`). What is handed out and unpaid can be returned,
  saying which ones; a paid or handed-out number cannot be removed from the list. Where the
  numbers are described, everything that accepts numbers (assign, pay, hand out, undo, return)
  accepts ranges (`0001-0010, 0015`, `Service.expandTokens`). Articles without numbers and
  loose units keep counting (`paid_quantity`, `picked_up_quantity`). Assigning and removing
  numbers without replacing the list: `POST` and `DELETE …/units/{id}/identifiers`.
- **Paid**: a row can be partly paid. `paid_quantity` (from 0 to `quantity`, migration `00027`)
  says how many units are paid; `paid_at` and who marked it are those of the last payment and
  are present exactly when something is paid. Only admin and board set and remove it:
  `POST /distributions/{id}/payments` adds payments, in a batch and all or nothing, with
  `payments` (`unit_id` and `quantity` units, or all the pending ones if missing) and
  `unit_ids` (everything pending in those rows); going over what is pending gives 409
  `already_paid`. `DELETE …/units/{id}/payment[?quantity=N]` removes N paid units, or all of
  them. Asking for more of something already paid does not make what is added paid, and
  `quantity` cannot be lowered below what is paid (409 `quantity_below_paid`). A row with
  something paid counts as paid for the locks above (only admin/board change it). **Every
  payment is recorded in Accounting** (migration `00032`): the distribution has an account
  (`account_id`, optional; `account_name` in the JSON) where its payments are entered and, if it
  has none, the account marked as `distribution_default` (only one; marking one removes the mark
  from the other). Both have to be of type `income`: a distribution with another account is 400
  `account_not_income`; marking as default an account that is not, or removing the `income` type
  from one that some distribution uses, is 409 `not_income_for_distributions`. If there is
  neither of the two, 409 `no_distribution_account`. When paying (`account_id` in the body) or
  removing a payment (`?account_id=`) it has to be said in which account it was paid, which has
  to exist (400 `unknown_account`) and be of type `asset` (400 `account_not_asset`); with no
  account, 400 `account_required`, unless what is paid is free, which moves no money and enters
  nothing. For each person in the request an automatic transaction is created between that asset
  account (`+total`) and the distribution's (`−total`; when a payment is removed, the other way
  round) with a description in Spanish, for example "Pago de Ana Pérez en «Lotería de Navidad»:
  3 × Papeleta (0001-0003)" or "Devolución del pago de …". An order with something paid cannot
  be deleted (409 `unit_paid`): the payment is removed first.
- **List of payments** (`distribution_payments`; only admin and board, because it says who has
  paid what): every payment that is entered leaves an entry with the date, who entered it, whose
  order it was (the one who paid: a payer distinct from the order's member is not stored), what
  and how many units, how much money and the numbers. Removing a payment writes the same entry
  with the signs changed, and each entry points to its accounting transaction
  (`transaction_id`). Deleting that transaction undoes its payments (each entry leaves unpaid
  again what it paid, or pays again what it removed, and disappears from the list) in the same
  operation; if the orders have changed and no longer allow it, nothing is deleted and it is 409
  `transaction_has_distribution_payments`. So the distribution's running total (`total_after`, a
  window `SUM` per distribution over its whole history, not just what the filter lets through)
  is the sum up to that moment and does not restart at the edge of a filter or a page. It is
  ordered from newest to oldest, with `from`/`to` and `limit`/`offset`, like an account's ledger.
  What was paid before the list existed stays as one entry per order, with the date of its last
  payment (nothing earlier was stored). In the interface, `/reparto-pagos?id=…`, which is opened
  from the "Pagos" button of the distribution itself (only if it has something to pay).
- **Collection**: `POST /distributions/{id}/pickups`, in a batch, only with the distribution out
  of `draft` and only admin, board or material. A row can be partly collected
  (`picked_up_quantity`, from 0 to `quantity`, migration `00028`), as it can be partly paid:
  `deliveries` (`unit_id` and `quantity` units, or all that are left) and `unit_ids` (everything
  that is left in those rows); going over what is left, 409 `already_picked_up`. It stores three
  things, those of the last delivery: when, who entered it (the caller) and to whom it was
  handed (`picked_up_by_login`: it can be different from the beneficiary if somebody collects
  things for others, but they have to be a member now or have been on `cutoff_date`, 409
  `invalid_collector`). It is all or nothing. In an article with numbers the units are items:
  **only the ones with an assigned number are handed out**, the `identifiers` that are given
  (assigned to the row and not handed out) or, failing that, the first assigned ones not handed
  out, and there have to be that many (409 `identifiers_missing`; with `assign_numbers` the
  missing ones are assigned first, if the numbers are described). Each handed-out number carries
  its `delivered_at` (`DeliveredIdentifiers` in the unit; what is assigned and not handed out is
  still on the shelf) and cannot be removed from the list. Three different things can be done
  afterwards: increase or reduce the order (admin/board/material, never below what has been
  handed out, 409 `quantity_below_delivered`, nor below the assigned numbers, 409
  `quantity_below_numbered`: some number has to be removed first; the others cannot touch a
  handed-out row); **undo** a delivery (`DELETE
  …/units/{id}/pickup[?quantity=N|?identifiers=a,b]`: they are no longer handed out, they stay
  in the order and keep their number); and **return** (`POST …/units/{id}/return`, `quantity` or
  `identifiers`): what is returned leaves the order, which becomes smaller (and the row is
  deleted if nothing is left), and its number becomes free. What is paid cannot be returned: the
  order cannot end up below what is paid (409 `quantity_below_paid`) and no more than what was
  handed out is returned. A row with something handed out cannot be deleted without returning it
  first. Asking again for the same article always adds to the same row.
- **Order summary** (`GET /distributions/{id}/order`): the units added up by combination of
  options; the ones nobody has chosen yet add up in one line with empty `choices`. An
  `all_members` distribution still in `draft` has no units yet (they are created when it is
  opened), but it is already known what has to be ordered: one per member on `cutoff_date`. The
  summary counts them even though they do not exist (`provisional: true`, creating nothing:
  reading it does not change the distribution) and the page says so; once it is opened, the same
  figures are already real.

### Document archive (`internal/library`)

The club's document archive (statutes, regulations, rules, activity memorias, meeting
minutes...): a directory tree on disk, not PostgreSQL. For that reason, unlike any other
resource, there is no `repository.go`/pgx: `Service` works directly against the file system.

**Origin of the data**

- Root in `config.LibraryRoot`, mounted in the container from `./library` on the host (see
  `docker-compose.yml`).
- The documents are placed by whoever administers the server, directly in `./library`.

**Access**

Read-only and open to any baseline role: there is no way to upload, rename or delete anything
through the API. The routes are in [`api.md`](api.md). A file's path is part of the URL and not a
query parameter, and the file route also accepts the session cookie (see "Authentication and
sessions") so that a document can be a normal browser link.

**Path security**

Every path given by the caller is resolved and checked against the root before touching the disk
(`Service.resolve`):

- A `".."` can never escape the root: it is first anchored under a synthetic root `/` before
  being cleaned (the same trick `net/http.FileServer` uses).
- A symlink pointing outside the tree is detected by resolving it and checking that the real
  target is still inside the root.

### Per-person files (`internal/userfiles`)

Same model as `internal/library` (a tree on disk, `config.UserFilesRoot`, mounted from
`./user-files` on the host, no PostgreSQL), but with one subdirectory per login instead of a
single shared tree. It holds each person's own documents (a scanned DNI, a signed
authorization...).

**Access**

- Same routes, listing, `?download=1`, Range support and session cookie as the archive (see
  [`api.md`](api.md)).
- Unlike the archive, they are not open to any baseline role: only the person themselves or
  admin/board can see them (403 otherwise). The check is inside the handler itself, like
  `memberships.listByUser` and `material.listMovementsByUser`.
- A person without a subdirectory of their own yet (the normal case, until somebody leaves them
  a file) sees an empty listing at their own root instead of a 404.
### Accounting (`accounts`, `transactions`, `transaction_entries`)

The club's money: a chart of accounts in a tree (`accounts`) and, against it, double-entry
transactions (`transactions`) made of entries (`transaction_entries`).

#### Chart of accounts (`accounts`)

The columns are in [`database.md`](database.md) and the routes in [`api.md`](api.md); these are
the rules.

**Type.** An account may have a type of its own (`asset`: its balance is money the club really
has; `income`: used mostly for what comes in; `expense`: mostly for what goes out). An account
with no type of its own has that of its parent account, and so on upwards: that is its effective
type (`effective_type` in the API, read-only; `type` is the own one, the one a `PATCH` replaces).
The rules that depend on the type always use the effective one:

- The account a distribution is paid into has to be `asset`.
- The account of a distribution and the one marked `distribution_default` have to be `income`.
- An account marked `allows_dues` or `allows_licenses` (the ones a transaction has to use to be
  able to record annual dues or federative licenses) has to be `income`.

Since changing a type, moving an account or creating a new one can change the type of a whole
subtree, `finishAccountChange` checks, in the same transaction as the change, that the saved
account and everything under it that has to be `income` still is (409
`not_income_for_distributions` or `not_income_for_dues_licenses`, and nothing is saved). It only
looks at that subtree, so accounts marked before the rule existed (with no type) do not block
other edits; they have to be given a type the next time they are edited.

**Flags.** `distribution_default` is the account where the payments of distributions with no
account of their own are recorded; only one account has it, and marking one takes it away from
whichever had it. `hidden` marks an account that is no longer used: it cannot be deleted if it has
history, so it is hidden from the chart and from the frontend's pickers (which show it only on
request) without touching what is recorded against it. Like the rest of the fields, a `PATCH`
replaces the flags: leaving one out turns it off.

**Hierarchy.**

- The hierarchy only groups: an account has child accounts or it has transactions, never both.
  Two database triggers enforce it (migration 00033), so nothing gets around it, not even the
  transactions of distribution payments: posting a transaction to an account with children is
  409 `account_has_children`; creating an account under another that already has entries, or
  moving it there, is 409 `parent_has_entries`. What already existed before the migration is
  left alone.
- `DELETE /accounts/{id}` only works if the account has no child accounts and no entries
  (409 `account_in_use`).
- `PATCH` rejects with 409 `circular_parent` a `parent_id` that is the account itself or one of
  its descendants.
- `POST /accounts/{id}/move` swaps the position with the sibling immediately before or after it,
  with no error if it is already the first or the last of its group.

#### Transactions and entries (`transactions`, `transaction_entries`)

- Each transaction is made of two or more entries that add up to exactly zero: standard
  double-entry bookkeeping. That condition cannot be a `CHECK` (it would only see its own row):
  it is checked by a constraint trigger deferred until `COMMIT` (see [`database.md`](database.md)).
- An account cannot appear twice within one transaction: its single entry already says everything
  that went through it.
- Correcting a transaction (`PATCH`) replaces its description, its date and the whole set of
  entries at once; there is no editing of a single entry.
- Documents (tickets, invoices) can be attached to a transaction, up to 10 MiB each: the metadata in
  the database (`transaction_files`), the bytes on disk (`TRANSACTION_FILES_ROOT`). Everybody who
  can sign in sees that a transaction has files, admin, board and money read them, admin and money
  attach and remove. A transaction that still has files cannot be deleted (409
  `transaction_has_files`).
- Deleting a transaction takes its entries with it and, by cascade, the dues it paid. If any
  license names it as its payment it cannot be deleted (409 `transaction_has_licenses`), because
  `licenses.transaction_id` is `ON DELETE RESTRICT`: a license is valid even if the trace of how
  it was paid is lost. If it was the payment of a distribution, deleting it undoes that payment
  (see "Distributions (`distributions`, `distribution_options`, `distribution_units`)").

#### Balances and listings

- `GET /accounts` walks the tree depth-first (each account followed by all its descendants before
  the next sibling) and adds to each one its `balance` and its `last_transaction_at`. An account
  that groups others, with no movements of its own, carries the sum of the balances of everything
  under it and the most recent date of its descendants.
- The ledger of an account (`GET /accounts/{id}/transactions`) carries a running `balance_after`,
  computed over the account's whole history before the range and the page are applied, so it does
  not restart at the edge of a window or change when searching. For an account that groups
  others, the ledger lists the entries of all its descendants, each with the account it is on,
  and `balance_after` is the accumulated total of all of them together up to that moment, not the
  balance of the entry's own account.
- The search by words of the description (`q`) normalizes vowels with an accent, `ü` and `ñ` with
  `translate()` on both sides, without needing the `unaccent` extension.

#### Permissions

Reading accounts and transactions is open to any baseline role: the club's accounts are
something any member can see. Writing is for admin or the optional `money` role; board has no
special write access here, just as it has none over licenses or over a person's data.
`GET /transactions/{id}` adds `dues` and `licenses` (the dues and licenses that transaction paid
for) only for admin and board, because who paid what is private to that person.
`Handler.getTransaction` fills them in through two minimal interfaces (`DuesLookup`,
`LicensesLookup`) that `memberships.Service` and `licenses.Service` satisfy.

## Backend (`backend/`)

### Budgets (`budgets`, `budget_items`, `budget_item_accounts`)

The club's plan for money, one budget per calendar year (1 January to 31 December), so that it can
be compared with what really happened. The routes are in [`api.md`](api.md), the columns in
[`database.md`](database.md) and the details in
[`backend-internals.md`](backend-internals.md#budgets).

- A budget has two parts, **income** and **expenses**, each made of line items ("partidas") with an
  estimated amount. Each item stands for one or more accounts of the part's type (an account's
  effective type, see Chart of accounts), and an account is in one item only. The items can change
  from one year to the next: a new year's budget is a new one, which the page starts from the
  closest earlier year's.
- **The two parts of a year add up to the same amount**, and **a year's budget covers every income
  and expense account** of the ledger (leaf accounts: a group is covered through its children;
  hidden accounts are optional). Both are checked when a budget is saved. An account created
  afterwards is not in the saved budget; the page shows it as not covered, and the next save has to
  include it.
- Everybody who can sign in reads budgets; only admin and `money` change them.
- Consulting a year shows, per item and per part, the amount budgeted, what was really received
  (income) or spent (expenses) in the year by the accounts of the item, and the percentage reached.

### Layers and packages

The backend is separated into layers by package. The domain does not depend on `net/http`: each
resource's `handler.go` is the only layer that knows HTTP exists.

- `internal/httpapi`: router and HTTP helpers.
- `internal/auth`: authentication and authorization middleware. It knows nothing about JWT or
  the database; it only delegates to a `SessionResolver`. There are no passwords to manage.
- `internal/tokens`: the real `SessionResolver`. It validates an opaque token against the
  database, computes the roles on the fly and exposes token management for admin.
- `internal/authapi`: magic link, logout and `GET /auth/me`.
- `internal/mail`: sending email. If `SMTP_HOST` is not set, it writes the message to the log; if
  it is, it really sends it.
- `internal/telegram` and `internal/telegrambot`: client of the Telegram bot API and the bot
  that receives messages (see the "Telegram" subsection).
- One package per domain resource: `users`, `roles`, `tokens`, `memberships`,
  `licenses`, `board`, `activities`, `activityresources`, `distributions`, `material`,
  `accounting`, `clubinfo` (the home page's "datos de interés", editable from `/admin`) and
  `budgets` (the yearly budgets) and `transactionfiles` (documents attached to transactions).
  Each one is split into `model.go`, `repository.go` (pgx), `service.go` and `handler.go`.
- `adminstatus` (`GET /admin/status`) is not a domain resource: it only reads, and has a single
  `handler.go` (see "Estado del sistema" in the `/admin` page).

`internal/library` and `internal/userfiles` are the exception to that pattern: since there is
nothing in PostgreSQL behind either of them, they have no `repository.go`; their `service.go`
works directly against the file system (see "Document archive (`internal/library`)" and
"Per-person files (`internal/userfiles`)").

`GET /api/v1/health` (what a reverse proxy or systemd reaches under `/api/*`) requires no
authentication and has a timeout of its own of 2 s, so that it can be checked that the process
is alive and reaches PostgreSQL without any session token.

### Authentication and sessions

`auth.Authenticator.Protect` is the single point through which every route is registered. It
always requires holding at least one of the roles `admin`, `board` or `member`. It is a floor
applied on top of the specific roles the route asks for, not a default that those roles replace.

- Whoever has only, for example, `material` (without also being admin, board or an active
  member) cannot use the API at all, not even the material routes.
- Since `member` is automatic (computed from having an active joining right now), this floor also
  cuts access off the moment someone stops being a member.

The session is a single opaque, revocable token (table `tokens`), with no access/refresh pair:
the extra security of a short-lived access token did not make up for the complexity, either for
the client or for whoever works against the API by hand (scripts, a terminal client).

- The token is used directly as the credential on every request.
- Since it is opaque, the client cannot read anything from it: `GET /auth/me` is the way to know
  who the current session is and what roles it has.
- Starting a session first requires a link from `/auth/magic-link` (valid for 15 min, single
  use).

The same token is also mirrored into an `HttpOnly`/`Secure`/`SameSite=Lax` cookie
(`katiuskas_session`, limited to `/api/v1`) every time a session is issued or renewed (signing
in, starting or ending an impersonation). `POST /auth/logout` deletes it.

- No ordinary route looks at it: all of them keep reading only the `Authorization` header.
- It exists only for `auth.ProtectAllowingCookie`, which the file routes of `internal/library`
  and `internal/userfiles` use (and only those).
- Thanks to it, a document can be a normal link that the browser opens in another tab or saves
  under its real name, without downloading it with `fetch` and turning it into a `Blob`.

### First admin account

`cmd/seed-admin` creates the first `admin` account: `POST /users` already requires that role, so
without this command there would be no way into a freshly migrated database.

- It is idempotent: it does nothing if the email already exists.
- Since there is no password, to sign in you have to request a link from the sign-in page with
  `SEED_ADMIN_EMAIL` and `SEED_ADMIN_PHONE`, and look at the API logs (or the mailbox, if SMTP is
  configured).

### System status (`internal/adminstatus`)

`GET /admin/status` (admin only, read-only; what it returns is in [`api.md`](api.md)) tells how
the process is doing and how it is set up, so that nobody has to log into the server to find out.
It never includes anything secret: no tokens, passwords or connection strings. `cmd/api` hands it
what is known at startup (`adminstatus.Info`) and the handler adds what can change on each
request. A database that does not answer is reported in the body (`database.ok` false), not with
an error code.

The version is the one Go recorded for the main module (`debug.ReadBuildInfo().Main.Version`: a
tag if it was built from a tagged commit, or a pseudo-version with the commit and `+dirty` if
there were changes; nothing has to be passed at build time, and it is empty if the binary was not
built from a git checkout, as in the Docker image). For Go to record it, `go.mod` and `go.sum`
are at the repository root and not in `backend/` (it only stamps a version when the module root
is the repository's); that is why the Docker images are built with the root as their context,
trimmed by `.dockerignore`.

### Telegram

`internal/telegram` is a minimal client of the Telegram bot API, with no dependencies (only
`net/http` and `encoding/json`). Today it is used for seven independent functions, six of them
"fire-and-forget" (they never add latency to what triggers them) and one synchronous.

#### Outgoing notices

| Notice | When it is sent |
| --- | --- |
| Error-level logs | Every record of error level or above, in addition to being logged normally. |
| Login | One notice for each login (`authapi.Handler.notifyLogin`). |
| Failed access | When someone types on `/login` details that do not work, so that an admin can see it (`authapi.Handler.notifyAccessProblem`): "⚠️ Acceso fallido" if no person has that email and that phone (what was typed is shown, trimmed and on one line) or if the person has no role that gives access (their login), and "⚠️ Código de acceso rechazado" if the code is wrong or has expired (the login) or is typed with details nobody has. Whoever types it is told too (404 `unknown_details`), and a request that works does not notify. |
| Magic link by DM | The same link (and, depending on the case, the code; see "Permissions") by DM to whoever has their account linked (`authapi.Handler.notifyMagicLinkViaTelegram`). |
| Mutations | A message of its own for each action that creates, edits or deletes something: person, role, membership, license, activity, material, board position, resource of an activity's memoria, token, account or transaction. |
| Startup | `cmd/api/main.go` sends "🚀 API started" right before starting the server, with the version and the date of the last commit if they are known — "🚀 API started (version v0.3.6, commit del 17-10-2026)"; only one of the two, or neither, if that is all there is — (`startupMessage`, from `adminstatus.ReadBuild`: the same information Go records in the binary and `/admin` shows). It is the synchronous exception (same 10 s limit): a single call at startup competes with no request, so blocking on it costs nothing noticeable. |
| Bot | `internal/telegrambot.Bot`, the only one that also *receives* messages, by long polling and not by webhook (see "Telegram bot"). |

The mutation notices say who did it and, for an edit, exactly what changed field by field. They
are built with `httpapi.NotifyAsync` and `DiffFields` (`internal/httpapi/audit.go`):

- `DiffFields` compares only the fields that endpoint can really touch, never all those of the
  struct, so that a field computed elsewhere (such as the list of roles) does not appear as a
  change that never actually happened.
- It returns one line "field: before → after" for each field that really differs.
- Each package with write routes receives its own `httpapi.MutationNotifier` from
  `cmd/api/main.go`. The notices are per significant action, not per request, so a mere touch of
  `last_used_at` on a token notifies nothing.

Without `TELEGRAM_BOT_TOKEN`/`TELEGRAM_LOG_CHAT_ID` configured:

- The `Sender` is a `NoopSender` and the notices (errors, logins, mutations, startup) fall back
  to writing the message to the log, like `mail` without `SMTP_HOST`.
- The magic link by Telegram is simply not attempted (nobody can have a `telegram_chat_id`
  without the bot running).
- Only the bot is left out, because it could not notify the channel of anything.

#### Telegram bot

`internal/telegrambot.Bot` receives messages by long polling. The flow depends on whether the
chat is already linked to a person (`users.Repository.UserIDForTelegramChatID`).

Unknown chat:

1. Any message gets a greeting and a button to share the contact, in a custom keyboard with
   `is_persistent: true` so that the client shows it open instead of hidden behind its own
   keyboard icon.
2. The text of the greeting explains where that button is and how to reveal it if the client does
   not honor the flag, because nothing about a Telegram custom keyboard is obvious to someone who
   has not seen one before.
3. On receiving a contact, `isOwnContact` checks that it is the sender's own, before looking at
   the phone (see "Foreign contact", further down in this same list).
4. If it is theirs, the phone is looked up with `users.Repository.FindByPhoneNumber` (see "Lookup
   by phone", below).
5. With a match, their `telegram_chat_id` is linked and `Bot.replyToLinkedContact` confirms it.
   With no match, they are told so and `contactClubHint` is added ("si crees que deberías tener
   acceso, ponte en contacto con el club para comprobar tu situación", the same advice
   `unauthorizedMessage` ends with), because a phone that really is not in the database is
   exactly the case that deserves someone at the club checking by hand.
6. In both cases the reply goes with `Client.SendMessageRemovingKeyboard` instead of a normal
   `SendMessage` (it was their own contact, and pressing the button again would only resend the
   same phone for the same answer) and the channel is notified of who tried, with which phone
   and whether it matched.

Foreign contact (`isOwnContact`):

- Telegram allows attaching anybody's contact (a hand-written vCard, or another person's saved
  contact); only the "share contact" button the bot itself sends guarantees that the contact is
  the sender's.
- Without this check, sharing a real member's phone in another person's name linked *that
  member's* account to the chat of *whoever sent it*: an impersonation path, not just a
  data-quality problem.
- A foreign contact is rejected with a reply and a notice of its own to the channel (more serious
  than a mere "no match", being a possible impersonation attempt), without ever looking at the
  phone. The "share contact" keyboard stays, so that they can retry with their own.

Lookup by phone (`FindByPhoneNumber`):

- It requires that the country prefix Telegram always adds be Spain's ("34").
- It is never a simple suffix match: that would let a French "+33666777888" pass as a Spanish
  "666777888", since they share every digit except the prefix.

Confirmation of the link (`Bot.replyToLinkedContact`) checks `canUseTelegramLogin` right there,
instead of waiting for a later message to find out:

- Whoever can sign in gets `emailPrompt` as a second, separate message.
- Whoever cannot gets a single combined reply (`unauthorizedAfterLinkReplyFormat`, "¡Gracias! Te
  hemos identificado como Nombre Apellidos. Sin embargo, este bot está disponible únicamente
  para usuarios autorizados. ..."), instead of splitting the news into two messages with nothing
  useful in between.

Already linked chat (normal message, not a contact): it does not repeat the share-contact menu,
but it does not trigger a sign-in link instantly on any message either. That was tried and
reverted as a security regression: having the phone (or the already open Telegram session) of a
member was enough to get in, without the requirement of knowing phone *and* email at once that
the web flow does have. The flow is:

1. `canUseTelegramLogin` requires the same baseline role as any API route
   (`auth.HasBaselineRole`: in practice, an active member through the automatic "member" role, or
   admin/board) and also that the person have an email and a phone on file. Being linked does not
   imply being able to sign in: the magic-link request from the web already requires both as
   input (`FindActiveByEmailAndPhone`), but a linked chat is resolved directly by its
   `telegram_chat_id`, so nothing guarantees they have them.
2. Whoever does not meet it is never asked for the email: any message from them gets
   `unauthorizedMessage` ("este bot está disponible únicamente para usuarios autorizados. Si
   crees que deberías tener acceso, ponte en contacto con el club para comprobar tu situación").
3. Whoever meets it is asked by `Bot.handleRecognizedChat` for a second factor: nothing happens
   until a message is the account's registered email (`emailMatchesUser`, case-insensitive,
   against the same format of several emails separated by spaces that
   `FindActiveByEmailAndPhone` uses).
4. Any other message gets a specific reply from `emailAttemptReply` depending on *why* it did not
   match (see the next list), except if the chat had been silent for more than
   `idleBeforeEmailPrompt` (30 minutes): someone who has not written for a long time surely does
   not remember that what the bot expects is their email, and an error sounds like a reproach, so
   they get the usual `emailPrompt` ("si quieres entrar en la Intranet de Katiuskas, envíame tu
   dirección de correo electrónico"). `Bot.noteMessage` records when each chat last wrote (in
   memory, so after a restart a chat counts as idle until it writes again) and every message
   counts as activity: the reply after the `emailPrompt` is specific again. Only an exact
   single-word match skips all this.
5. When it matches, it deletes that message from the chat with `Client.DeleteMessage` (the bot
   API allows deleting a message the bot itself received in a private chat), so that the email
   is not left in view of whoever later gets at the phone or the session. It is best-effort: if
   the deletion fails (for example, a message more than 48 h old, Telegram's limit), the link is
   sent anyway.
6. It replies with a new sign-in link, exactly as if it had been requested from the web
   (`authapi.Handler.IssueMagicLinkForTelegramUser`; see "Permissions").

Replies of `emailAttemptReply`:

| Reply | When |
| --- | --- |
| `nonTextEmailReply` | What arrives is not text (an empty `Text` always means that: Telegram never sends a real text message with an empty body). |
| `onlyEmailReply` | It has more than one word. |
| `notAnEmailReplyFormat` | It is a single word with no "@" (it is repeated in the reply). |
| `unknownEmailReplyFormat` | It has the shape of an email but is not any of those registered on the account (the address is repeated and contacting the club is suggested if they think it is a mistake). |

Link and change of phone: the link is only worth as much as the number it was made with. That is
why `users.Repository.Update` clears `telegram_chat_id` (leaves it `NULL`) as soon as a `PATCH`
really changes the person's phone, or leaves it empty. Otherwise a chat linked to an old number
would keep resolving to whoever no longer has it. Resending the same phone, or a `PATCH` that
does not even touch it, leaves the link as it is.

Link and unreachable chat: the link is also forgotten when the bot finds out it can no longer
talk to that person. `telegram.Client` returns Telegram's `ok=false` answers as a
`*telegram.APIError` (with its `ErrorCode`, the `error_code` Telegram sends, and its
`Description`), and `telegram.IsUnreachable` is true for any 403 Forbidden —every way of not
being able to talk to someone (they blocked the bot, closed or deactivated their account) comes
with one— or if the description contains "chat not found" or "user not found" (a 400 for a chat
that no longer exists). Then the send of the sign-in DM (`notifyMagicLinkViaTelegram`, in either
of the two flows) calls `users.Service.ClearTelegramChatID`: keeping the id would only fail again
at every sign-in. Any other failure (a timeout, a rate limit) leaves the link as it is, and the
person can link again by sharing their contact with the bot. It only covers the sign-in DMs, not
the bot's other replies in a chat. The send of a web request's DM runs in its own goroutine,
which `Handler.WaitForBackgroundSends` makes it possible to wait for (the tests use it).

## Frontend (`web/`)

### Shared mechanics

Classic multi-page site (not an SPA), with no build step: one `<page>.html` plus its
`js/<page>.js` per view, which Caddy resolves without extension (`/socios` → `socios.html`).

- **API client.** `web/js/api.js` is a shared `fetch` client that adds the token (kept in
  `localStorage`) to every request and, on loading each page, calls `GET /auth/me` to confirm
  that the token is still valid and to know who it is (login and name, with which it draws the
  topbar greeting and the impersonation notice without asking for anything else) and what roles
  it has.
- **Page structure.** `js/layout.js` (`initPage()`) draws the sidebar and the topbar once,
  validates the session and applies visibility by role. It also supports `requireAnyRole`, which
  redirects to the home page whoever has none of the given roles.
- **Visibility by role.** It is done with `data-roles` and is only hiding on the client: the
  authority is the API.
- **Forms.** The create/edit ones are modal `<dialog>`s (`js/modal.js`, `initModal`); errors that
  happen with one open are shown inside it.

### Pages

| Page | What it shows | Who can access | Key behaviors |
| --- | --- | --- | --- |
| `/` (`index.html`/`dashboard.js`) | "Datos de interés" of the club (`GET /club-info`, edited from `/admin`: nothing is written in the page), Junta Directiva (`GET /board-members`) and links to Documentos, Actas and Memorias. | Any baseline role. | The links are normal links thanks to the session cookie. |
| `/socios` | List of people. | Sidebar: admin, board and member. | For a member it reduces columns and filters to `MemberListing` and its rows are not clickable; for admin and board, each row leads to `/socio?login=<login>`. |
| `/socio?login=…` (`socio.html`/`member.js`) | A person's page (see below). | Only admin and board. | Only admin can save the data; board sees it disabled. Admin also sees and can close the person's sessions. |
| `/perfil` | Own page: editable contact data, the same "Historial como socio" as `/socio` (without buttons), licenses, "Mis sesiones" (the sessions not expired or revoked, each of which can be closed) and files. | Any baseline role. | Includes the same viewer of own files as `/socio`. |
| `/actividades`, `/actividad?id=…` | List and detail of an activity: participants, lent material and memoria. | Any baseline role. | The list includes a yearly calendar view that marks weekends and holidays (there is no separate calendar page). |
| `/material` | Material catalog and its movements. | Any baseline role. | "Nuevo material" and the movements (loan, rental, sale), for admin, board and the `material` role. |
| `/material-prestado` (`material-prestado.html`/`loanedMaterial.js`) | The material that is still the club's but not in it right now: *Prestado* (every item with loans not yet returned, the units pending and the activities they are out for, each linking to its page) and *Alquilado* (the open rentals, with "Devolver"). | Admin, board and the `material` role; not in the sidebar, reached with the "Material prestado o alquilado" button of `/material`. | Material that is lost, abandoned or retired is not on it, as it is not the club's any more. |
| `/archivo` | The club's document archive. | Any baseline role. | `js/fileBrowser.js` shares the listing and preview logic with `userFiles.js`; Markdown documents are rendered with `js/markdown.js`. |
| `/repartos` | List of distributions (what the club hands out to members) with status, price and deadline; "Puedes pedirlo" on the open ones that apply to whoever is looking. "+ Nuevo reparto" opens a dialog. | Any baseline role; drafts and "+ Nuevo reparto" only for admin and board. | Options are written one per line, "Talla: S, M, L". |
| `/reparto?id=…` | One distribution: description, status and conditions; "Pedir" form (for oneself or, in "A nombre de", for another member; an "Es un extra" checkbox if there is one; in a distribution for all members with no options, where the own order is already made and only extras are left, there is no checkbox, the button says "Pedido extra" and the help gives the price: "Puedes pedir también extras a 8,00 €") and "Tus pedidos" (what has been ordered for oneself or entered by oneself, with who entered it, its price and whether it is collected or not —nothing about payment: the intranet does not keep that—; without the "Elegido" column if there is nothing to choose, nor "Tipo" if there can be no extras; "Cambiar" and "Quitar" while possible). In a distribution for all members everyone already has their own unit ("Automático", "Sin elegir" until somebody chooses, with "Elegir" instead of "Cambiar" and no "Quitar"), and the form serves to choose for another member or to order extras. A distribution for all members with neither options nor extras has no "Pedir" section (there is nothing to order). While the distribution is a draft there is neither "Pedir" nor "Tus pedidos" (nobody can order anything yet). Admin and board, in addition: move the status one step forward or back, edit, and only admin, delete; admin, board and material: links to the next two pages. | Any baseline role; the form only appears if the distribution admits orders now and applies to the person. | The API's error codes are put into the club's own words (`apiErrorText`). |
| `/reparto-pagos?id=…` | Payments of a distribution: chronological list (newest first) of everything collected in it: date, who entered it, what (how many units and of what, with the numbers), who paid it, amount and what has been collected in the distribution up to that moment. Withdrawn payments appear as negatives. Date filter and "Ver más". | Admin and board. | `requireAnyRole`; reached with the "Pagos" button of the distribution's page, which only appears if it has something to pay. |
| `/reparto-pedidos?id=…` | Orders, all on one page. At the top, the summary for placing the order (units by combination of options or by article, with the total and what is left). Below, one line per order (with a search by member or number) with a summary: what it is, how many units are handed out and paid, and the amount; "Más información" unfolds it. Where articles carry a number, numbers are assigned to what is missing (the next free ones or the ones typed) and there is a list of the numbered items, each with its state (paid, handed out), in which one ticks those to be paid (admin and board), handed out (a dialog asks to whom, "Entregado a", with the member themself by default), returned, to undo the handing out or to take the number off. For articles with no number, the same with a number of units. It records when, who entered it and to whom it was handed. It warns if something is handed out unpaid. | Admin, board and material (payment, only admin and board). | `requireAnyRole`; replaces the old summary and collection pages. |
| `/reparto-numeros?id=…` | Numbers: for each article with described numbers, all that exist with who has each one and how many are left free, filterable by free or assigned and by number or name. | Admin, board and material. | `requireAnyRole`; the link appears in the distribution only if some article has described numbers. |
| `/404` (`404.html`) | The page Caddy shows, with status 404 and without changing the address, for any address that is not a page (`handle_errors` in `deploy/Caddyfile`). With a session it is a normal page, with the menu to go to another one; without it, a plain panel with a link to `/login`. | Anyone. | It uses absolute paths, because it is served at the address that was asked for, however deep. |
| `/contabilidad` | Chart of accounts in a tree, with balance, hidden accounts on request and reordering. | Admin, board and `money` role (sidebar and page); only admin and `money` change anything. | Narrower than the API, which opens reading to any baseline role. Board sees everything and has no create, edit or reorder buttons. |
| `/presupuestos` (`presupuestos.html`/`budgets.js`) | The budget of a year (selector): income and expenses tables with each line item's accounts, budgeted amount, what was really received or spent and the percentage reached (with a bar), the totals of both parts, and a notice for accounts the budget does not cover. | Sidebar: any baseline role. Editing: admin and money. | Admin and money get "Editar presupuesto", "Borrar presupuesto" and, for a year without one, "Crear presupuesto" (it starts from the closest earlier year's items); the editor adds accounts one at a time or a whole group's, and shows both sums and the accounts still without an item as it is edited. |
| `/admin` (`admin.html`/`admin.js`) | What only interests administrators; for now, the list of roles with create and delete (`GET`/`POST`/`DELETE /roles`), and, unfoldable under each one, which people have it (from `GET /users`), with "×" to remove it and a search with "Dar este rol" to assign it (`POST`/`DELETE /users/{login}/roles`); the automatic roles only show who has them, and one is not offered to remove one's own admin role; and the open sessions (`GET /tokens`): who, when and from where, with "Revocar" and "Caducidad" for the active ones (and a different warning if one's own is revoked); and the system status (`GET /admin/status`): version, database and migration, email, Telegram, timeouts and directories. Also the holidays of the activities calendar, year by year (`GET`/`POST /holidays`, `DELETE /holidays/{id}`): with "Nuevo festivo", "Borrar" and "Copiar del año anterior" (same day and month, except where there is one already; the movable ones, such as Semana Santa, are corrected by hand). | Only admin: the "Admin" entry of the menu is seen only by them and anyone else is sent to the home page. | It is meant to grow with more administration sections. |
| `/cuenta?id=…` | Ledger of an account. | Admin, board and `money` role; only admin and `money` change anything. | Creating and correcting transactions and, from their dialogs, recording dues and licenses against the transaction. |
| `/justificantes` (`justificantes.html`/`transactionFilesPage.js`) | Every file attached to a transaction, with the transaction's date, description, amount and accounts (each entry with its sign), a year filter and "Ver más". | Admin, board and `money` role; not in the sidebar, reached with the "Justificantes" button of `/contabilidad`. | A file opens in a tab (a PDF or an image is shown, anything else downloads). The accounts link to their ledger. |
| `login.html` | Sign-in by email and phone, with Telegram as a second way. | No session. | It is the only page without a sidebar or `initPage()`. If a session is started in another window (the link in the email is usually opened in another one), this one moves to `/` by itself: `auth.js` watches the `token` in `localStorage` and checks it with `GET /auth/me`. |

### A person's page (`/socio`)

For the person being looked at, the page gathers:

- their editable data (only admin can save; board sees it disabled) and their roles;
- the "Historial como socio": joinings, leavings, temporary leaves, dues, licenses and Junta
  positions in a single timeline;
- their history of activities and material (`GET /users/{login}/activities` and
  `GET /users/{login}/material/movements`);
- their federative licenses (`js/licenseFiles.js`: thumbnails of the copies in `licencias/` among
  the person's files; the thumbnails are generated, on demand, by
  `scripts/generate-license-thumbnails.sh`);
- their own files (`GET /users/{login}/files`, `web/js/userFiles.js`): the same embedded viewer,
  with the same breadcrumbs and preview, also appears on `/perfil` so that each member sees
  their own.

### Telegram sign-in on `login.html`

`login.html` offers Telegram as a second way to sign in, next to the email and phone form (never
in its place). On loading, `auth.js` calls `GET /auth/telegram-bot` (without authentication,
with no secret data: the bot's public name is as public as the `t.me/<username>` link built with
it) and, only if a username comes back, shows a notice with a direct link to the bot's chat.

That name is configured with `TELEGRAM_BOT_USERNAME`, independent of
`TELEGRAM_BOT_TOKEN`/`TELEGRAM_LOG_CHAT_ID` (see "Telegram").

## Running locally

```sh
docker compose up -d postgres
export DATABASE_URL=postgres://intranet:intranet@localhost:64712/intranet?sslmode=disable
make migrate-up          # requires goose: https://github.com/pressly/goose
make seed-admin          # creates the admin account (no password: sign-in by magic link)
make run                 # serves the API on :64711
```

The frontend is static: it can be served with any file server pointing at `web/` (or with the
full `docker compose up`, which also brings up Caddy on `:64710` proxying `/api/*` to the
backend).
