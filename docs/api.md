# HTTP API

Quick reference for the endpoints of the club API. The complete contract, with every field
and every error, is in [`backend/openapi/openapi.yaml`](../backend/openapi/openapi.yaml); this
file is the summary for whoever only wants to know what exists and how to call it. How the
data is stored is in [`database.md`](database.md) and the business rules are in
[`architecture.md`](architecture.md).

Contents:

- [Conventions](#conventions)
- [System and session](#system-and-session)
- [People](#people)
- [Roles](#roles)
- [Sessions and impersonation](#sessions-and-impersonation)
- [Members](#members)
- [Activities](#activities)
- [Activity memoria](#activity-memoria)
- [Equipment](#equipment)
- [Distributions](#distributions)
- [Document archive and per-person files](#document-archive-and-per-person-files)
- [Accounting](#accounting)
- [Objects](#objects)

## Conventions

- **Base**: all routes hang from `/api/v1`. Bodies and responses are JSON, except
  files (`/library/file/…`, `/users/{login}/files/file/…`, the file of a memoria resource), which
  are the raw bytes, and the creation of a memoria resource, which is `multipart/form-data`.
- **Session**: there are no passwords. `POST /auth/magic-link` sends a link with a code; it is
  exchanged for an opaque token with `POST /auth/magic-link/consume` (or `…/consume-code`) and that
  token goes in every request: `Authorization: Bearer <token>`. The token expires 30 days after its
  last use (every use renews it) and can be revoked. The file routes (`/library/file/…`,
  `/users/{login}/files/file/…`) also accept the `katiuskas_session` cookie, which the API sets on
  sign-in, so that a document can be an ordinary browser link.
- **Baseline role**: almost every route with a session also requires being `admin`, `board` or
  `member` (a member with an active joining). Someone who only has another role, such as `material`
  or `money`, gets 403 everywhere. In the lists below, "Any baseline role" means exactly that.
  "The person themselves" is the person who owns the resource, identified by the `login` in the
  route.
- **People**: always named by `login` (for example `maria.garcia`), never by a numeric id. The
  other resources (activity, license, token…) do have a numeric id.
- **Dates**: a "date" field is sent as `YYYY-MM-DD`, but the response returns it as an RFC 3339
  timestamp at midnight UTC (`2024-03-15T00:00:00Z`): read only its first ten characters. A
  "datetime" field is a real timestamp.
- **Errors**: `{"error": {"code": "…", "message": "…"}}`, with the matching HTTP code. A
  malformed JSON is always `400 invalid_body` and an unexpected failure `500 internal_error`; they
  are not repeated on each route. The codes below come from the OpenAPI and were extracted from it
  automatically.
- **Replacement in `PATCH`**: in almost all of them, what is not sent is turned off or emptied
  (the whole record is replaced), not left as it was. The opposite is said in the route's
  description.
- **Paging**: the listings that grow without limit accept `limit` and `offset`. An out-of-range
  value goes back to the default (50 almost always) instead of giving an error.
- **Notation**: in bodies, a `*` marks the required field.

## System and session

No passwords: people sign in with a single-use link (or its 6-digit code) that arrives by email and,
if the person has linked Telegram, by Telegram too.

- **GET** `/health` — *Public*. Checks that the process is alive and reaches PostgreSQL. 200 or 503.
  - returns: 200 `JSON`
- **POST** `/auth/magic-link` — *Public*. Asks for a sign-in link with the person's email and phone.
  If the pair matches nobody who can sign in (or the person has no role that gives access) it says
  so, so that whoever mistyped knows to check it or to ask the board. Limited by origin address.
  - body: `email*`, `phone*`
  - returns: 200 `JSON`
  - errors: 400 `invalid_body`, `missing_fields`; 404 `unknown_details`; 429 `rate_limited`
- **POST** `/auth/magic-link/consume` — *Public*. Exchanges the link's token for a session token.
  - body: `token*`
  - The session expires 3 hours after its last use instead of 30 days when the request is made
    through an address whose first label is `local` (`local.intranet.katiuskas.es`: the club's
    shared tablet)
  - returns: 200 `JSON`
  - errors: 400 `invalid_body`; 401 `invalid_token`
- **POST** `/auth/magic-link/consume-code` — *Public*. Exchanges the 6-digit code (plus email and
  phone) for a session token. Same limit by origin.
  - body: `email*`, `phone*`, `code*`
  - Same session lifetime rule as above
  - returns: 200 `JSON`
  - errors: 400 `invalid_body`, `missing_fields`; 401 `invalid_code`; 429 `rate_limited`
- **POST** `/auth/logout` — *Public*. Revokes the session token (knowing the token is the proof).
  Always answers 204.
  - body: `token*`
  - returns: 204
  - errors: 400 `invalid_body`
- **GET** `/auth/telegram-bot` — *Public*. Public username of the Telegram bot, to offer it as a
  second way to sign in. Empty if not configured.
  - returns: 200 `JSON`
- **GET** `/auth/me` — *Any baseline role*. Who owns the token and which roles they have now.
  Carries `impersonated_by` and `impersonation_seconds_left` (how long the impersonation has left,
  as a duration) in an impersonation session. It also renews the session cookie for as
  long as the session has left (the frontend calls it on every page load).
  - returns: 200 `JSON`
- **POST** `/auth/impersonate/exit` — *Any valid session*. Ends an impersonation and returns a new
  token for the admin who started it. (The frontend only uses it when it no longer has the admin's
  own session to go back to.)
  - returns: 200 `JSON`
  - errors: 409 `not_impersonating`
- **GET** `/admin/status` — *Admin*. Version, database and migration, email, Telegram, timeouts,
  directories and how full their disks are, and what is wrong right now, of the running process.
  Never includes secrets.
  - returns: 200 `AdminStatus`

## Club information

The "datos de interés" of the home page, as labelled values in the database. Readable by anybody who
can sign in; only admin changes them.

- **GET** `/club-info` — *Any baseline role*. The items, in their order.
  - returns: 200 `[ClubInfoItem]`
- **POST** `/club-info` — *Admin*. Adds an item, at the end.
  - body: `label*`, `value*`, `copy_value`, `copy_label`
  - returns: 201 `ClubInfoItem`
  - errors: 400 `invalid_label`, `invalid_value`, `invalid_copy`
- **PATCH** `/club-info/{id}` — *Admin*. Replaces the item's fields (a `copy_value` or `copy_label`
  left out turns the button off); it keeps its place.
  - body: the same as creating
  - returns: 200 `ClubInfoItem`
  - errors: 400 as above; 404 `not_found`
- **DELETE** `/club-info/{id}` — *Admin*.
  - returns: 204; errors: 404 `not_found`
- **PUT** `/club-info/order` — *Admin*. Puts the items in the order of the ids given, which have to
  be all of them, each once.
  - body: `item_ids*`
  - returns: 200 `[ClubInfoItem]`
  - errors: 409 `reorder_mismatch`

## People

A person is always identified by their `login`, never by a numeric id.

- **GET** `/users` — *Any baseline role*. List of people. Admin and board get the full record
  (`User`); a member gets `MemberListing`, only of those who are or have been members.
  - returns: 200 `[User or MemberListing]`
- **POST** `/users` — *Admin*. Creates a person. The `login` is generated automatically and comes in
  the response.
  - body: `login`, `first_name*`, `last_name*`, `email`, `national_id`, `birth_date*`, `gender`,
    `phone`, `address`, `postal_code`, `city`, `province`, `emergency_contact`, `role_names`
  - returns: 201 `User`
  - errors: 400 `invalid_body`, `invalid_date`, `unknown_role`, `role_names`, `automatic_role`,
    `former_member`; 409 `duplicate`
- **GET** `/users/directory` — *Any baseline role*. Only `login`, first name and last name of
  everyone, to pick someone in a selector.
  - returns: 200 `[DirectoryEntry]`
- **GET** `/users/{login}` — *The person themselves, admin or board*. Full record of a person.
  - returns: 200 `User`
- **PATCH** `/users/{login}` — *Admin; the person themselves only their contact details*. Modifies
  personal data. A member who touches other fields gets 403 `restricted_fields`.
  - body: `login`, `first_name`, `last_name`, `email`, `national_id`, `birth_date`, `gender`,
    `phone`, `address`, `postal_code`, `city`, `province`, `emergency_contact`
  - returns: 200 `User`
  - errors: 400 `invalid_body`, `invalid_date`, `invalid_login`; 403 `forbidden`,
    `restricted_fields`; 409 `duplicate`
- **DELETE** `/users/{login}` — *Admin*. Deletes the person along with their history. The last admin
  cannot be deleted.
  - returns: 204
  - errors: 409 `cannot_delete_self`, `has_references`, `last_admin`

## Roles

`member`, `former_member` and `board` are automatic: they are computed from joinings and board
positions and are not assigned by hand.

- **GET** `/roles` — *Any baseline role*. Catalog of roles (name and description), not who holds
  them.
  - returns: 200 `[Role]`
- **POST** `/roles` — *Admin*. Creates a role.
  - body: `name*`, `description`
  - returns: 201 `Role`
  - errors: 400 `invalid_body`, `invalid_name`; 409 `duplicate`
- **DELETE** `/roles/{id}` — *Admin*. Deletes a role (system roles cannot be deleted).
  - returns: 204
  - errors: 400 `invalid_id`; 409 `system_role`, `former_member`
- **POST** `/users/{login}/roles` — *Admin*. Gives a role to a person. Rejects the automatic ones.
  - body: `role_id*`
  - returns: 204
  - errors: 400 `invalid_body`; 404 `not_found`; 409 `automatic_role`
- **DELETE** `/users/{login}/roles/{roleID}` — *Admin*. Removes a role. An admin cannot remove their
  own (`self_admin_removal`) and the club cannot be left without an admin (`last_admin`).
  - returns: 204
  - errors: 400 `invalid_id`; 404 `not_found`; 409 `self_admin_removal`, `last_admin`,
    `automatic_role`

## Sessions and impersonation

- **GET** `/tokens` — *Admin*. Everyone's sessions: the active ones by default; `status=all` adds
  the history. Marks the caller's own as `current`. `sort` (`last_used`, the default, `created`,
  `expires` or `person`, by name as it is read, then login) and `order` (`asc` or `desc`; the most
  recent first for the dates, A first for `person`) say in what order, which holds across pages:
  ties break by id, so paging neither repeats nor skips. A session never used counts as the oldest
  use.
  - query: `status`, `sort`, `order`, `limit`, `offset`
  - returns: 200 `[Token]`
  - errors: 400 `invalid_status`, `invalid_sort`, `invalid_order`
- **GET** `/users/{login}/tokens` — *That person, or admin*. The sessions a person has had, newest
  first, without the token value, with the one making the request marked `current`: all of them by
  default (the audit view), or only the ones not expired or revoked with `?status=active` (what a
  person sees of their own). Anybody else gets 403, also for a login nobody has, so it cannot be
  used to check which exist (admin gets 404). A session started by impersonating the person does see
  theirs, as the person would in their profile.
  - returns: 200 `[Token]`
  - errors: 400 `invalid_status`; 403 `forbidden`; 404 `not_found` (admin)
- **PATCH** `/users/{login}/tokens/{tokenID}` — *Admin*. Changes the expiry of a session. Since a
  session expires after its last use, this also sets how long it lasts from now on (from now to that
  moment), so a later use does not undo it; a moment in the past ends it.
  - body: `expires_at*`
  - returns: 204
  - errors: 400 `invalid_body`
- **DELETE** `/users/{login}/tokens/{tokenID}` — *That person, or admin*. Revokes a session; the
  next request with it gets 401. A person closes their own sessions this way, the one in use
  included (that is signing out); not another's, and not through a session started by impersonating
  them (403): it can see the person's sessions but not end them, the admin leaves the impersonation
  first.
  - returns: 204
  - errors: 400 `invalid_id`; 403 `forbidden`; 404 `not_found`
- **POST** `/users/{login}/impersonate` — *Admin*. Opens a short session (1 hour) that acts as that
  person. From inside an impersonation it is recorded as started by the original admin (the one
  behind it), not by the person they were acting as.
  - returns: 201 `JSON`
  - errors: 409 `cannot_impersonate_self` (oneself, the person the session already is, or the admin
    behind it), `not_eligible`

## Members

Joinings and leavings, annual dues, federative licenses, temporary leaves and the board (Junta
Directiva).

- **GET** `/users/{login}/memberships` — *The person themselves, admin or board*. Joining and
  leaving periods of a person.
  - returns: 200 `[Membership]`
- **POST** `/users/{login}/memberships` — *Admin*. Opens a membership period. Assigns the membership
  number the first time.
  - body: `start_date*`
  - returns: 201 `Membership`
  - errors: 400 `invalid_body`, `invalid_date`; 409 `overlapping_period`
- **GET** `/memberships` — *Admin or board*. Who is a member today.
  - returns: 200 `[Membership]`
- **PATCH** `/memberships/{id}` — *Admin*. Corrects the dates of a period or closes it.
  - body: `start_date*`, `end_date`, `end_reason`
  - returns: 200 `Membership`
  - errors: 400 `invalid_id`; 409 `overlapping_period`, `outside_membership_period`
- **DELETE** `/memberships/{id}` — *Admin*. Deletes a period.
  - returns: 204
- **GET** `/users/{login}/dues` — *The person themselves, admin or board*. Registered annual dues,
  with the payment date.
  - returns: 200 `[Due]`
- **POST** `/users/{login}/dues` — *Admin*. Registers an annual due tied to the transaction that
  collected it. If the person had no joining at all, it also opens one.
  - body: `year*`, `transaction_id*`, `start_date`
  - returns: 201 `Due`
  - errors: 400 `invalid_body`, `missing_fields`, `invalid_date`, `start_date_required`,
    `unknown_transaction`; 409 `due_already_recorded`, `transaction_not_for_dues`
- **GET** `/users/{login}/licenses` — *The person themselves, admin or board*. Federative licenses
  of a person.
  - returns: 200 `[License]`
- **POST** `/users/{login}/licenses` — *Admin*. Assigns a license. Only to someone who was a member
  at some point of its year.
  - body: `license_type_id*`, `other_detail`, `issued_date*`, `year*`, `transaction_id`
  - returns: 201 `License`
  - errors: 400 `invalid_body`, `missing_fields`, `invalid_date`, `invalid_license_type`,
    `invalid_other_detail`, `unknown_transaction`; 409 `not_active_member`,
    `transaction_not_for_licenses`
- **PATCH** `/licenses/{id}` — *Admin*. Corrects a license.
  - body: `license_type_id*`, `other_detail`, `issued_date*`, `year*`
  - returns: 200 `License`
  - errors: 400 `invalid_id`, `invalid_body`, `missing_fields`, `invalid_date`,
    `invalid_license_type`, `invalid_other_detail`
- **DELETE** `/licenses/{id}` — *Admin*. Deletes a license.
  - returns: 204
- **GET** `/license-types` — *Admin*. Catalog of license types.
  - returns: 200 `[LicenseType]`
- **GET** `/users/{login}/leaves` — *The person themselves, admin or board*. Temporary leaves of a
  person.
  - returns: 200 `[Leave]`
- **POST** `/users/{login}/leaves` — *Admin*. Registers a temporary leave; it has to fall within a
  membership period.
  - body: `start_date*`, `end_date`, `reason`
  - returns: 201 `Leave`
  - errors: 400 `invalid_body`; 409 `outside_membership_period`, `overlapping_period`
- **PATCH** `/leaves/{id}` — *Admin*. Corrects a temporary leave.
  - body: `start_date*`, `end_date`, `reason`
  - returns: 200 `Leave`
  - errors: 400 `invalid_id`; 409 `outside_membership_period`, `overlapping_period`
- **DELETE** `/leaves/{id}` — *Admin*. Deletes a temporary leave.
  - returns: 204
- **GET** `/board-members` — *Any baseline role*. Current board (Junta Directiva), with position and
  contact.
  - returns: 200 `[BoardMember]`
- **GET** `/users/{login}/board-members` — *The person themselves, admin or board*. Board positions
  of a person.
  - returns: 200 `[BoardMember]`
- **POST** `/users/{login}/board-members` — *Admin*. Opens a board position.
  - body: `position*`, `start_date*`
  - returns: 201 `BoardMember`
  - errors: 400 `invalid_body`; 409 `overlapping_period`
- **PATCH** `/board-members/{id}` — *Admin*. Corrects the position or the dates, or closes it.
  - body: `position*`, `start_date*`, `end_date`
  - returns: 200 `BoardMember`
  - errors: 400 `invalid_id`; 409 `overlapping_period`
- **DELETE** `/board-members/{id}` — *Admin*. Deletes a board position.
  - returns: 204

## Activities

- **GET** `/activities` — *Any baseline role*. Paged list, most recent first. `status=active` leaves
  only the ones that still need attention.
  - query: `status`, `order`, `limit`, `offset`
  - returns: 200 `[Activity]`
- **POST** `/activities` — *Any baseline role*. Creates an activity (with initial participants if
  wanted). The organizer is whoever creates it, unless admin or board name someone else.
  - body: `title*`, `type`, `start_date*`, `end_date*`, `description`, `visibility`,
    `participant_logins`, `leader_login`
  - returns: 201 `Activity`
  - errors: 400 `invalid_body`, `invalid_date`, `invalid_date_range`, `invalid_type`,
    `invalid_visibility`, `unknown_participant`; 403 `forbidden`; 404 `not_found`; 409
    `not_federated`
- **GET** `/activities/{id}` — *Any baseline role*. One activity.
  - returns: 200 `Activity`
  - errors: 400 `invalid_id`
- **PATCH** `/activities/{id}` — *Organizer, admin or board*. Modifies an activity. Changing the
  organizer is only for admin or board.
  - body: `title`, `type`, `start_date`, `end_date`, `description`, `status`, `visibility`,
    `leader_login`
  - returns: 200 `Activity`
  - errors: 400 `invalid_id`, `invalid_body`, `invalid_date`, `invalid_date_range`, `invalid_type`,
    `invalid_status`, `invalid_visibility`; 403 `forbidden`; 404 `not_found`
- **DELETE** `/activities/{id}` — *Organizer, admin or board*. Cancels an activity.
  - returns: 204
  - errors: 400 `invalid_id`
- **GET** `/activities/{id}/participants` — *Any baseline role*. Participants.
  - returns: 200 `[Participant]`
  - errors: 400 `invalid_id`
- **POST** `/activities/{id}/participants` — *Depends on the visibility*. Sign up, or sign up
  another person. In a public one each person signs themselves up; in a private one only the
  organizer, admin or board. Caving and canyoning ones require a license for the year
  (`not_federated`).
  - body: `login`
  - returns: 204
  - errors: 400 `invalid_id`, `invalid_body`; 404 `not_found`; 409 `not_federated`
- **DELETE** `/activities/{id}/participants/{login}` — *Depends on the visibility*. Leave the
  activity, or remove another person (admin or board, or the organizer in a private one).
  - returns: 204
  - errors: 400 `invalid_id`
- **GET** `/users/{login}/activities` — *Any baseline role*. Activity history of a person (as
  organizer or participant).
  - returns: 200 `[Activity]`
- **GET** `/holidays` — *Any baseline role*. Club holidays that the calendar marks.
  - query: `year`
  - returns: 200 `[Holiday]`
- **POST** `/holidays` — *Admin or board*. Adds a holiday.
  - body: `date*`, `name*`
  - returns: 201 `Holiday`
  - errors: 400 `invalid_body`; 409 `duplicate_holiday`
- **DELETE** `/holidays/{id}` — *Admin or board*. Deletes a holiday.
  - returns: 204
  - errors: 400 `invalid_id`

## Activity memoria

The memoria (the write-up of an activity) is an ordered list of resources: text entries or uploaded
files (up to 25 MiB).

- **GET** `/activities/{id}/resources` — *Any baseline role*. Resources of the memoria, in order.
  - returns: 200 `[ActivityResource]`
  - errors: 400 `invalid_id`
- **POST** `/activities/{id}/resources` — *Admin, board, organizer or participant*. Adds a resource.
  Always `multipart/form-data`, the text one too.
  - body: `type*`, `title*`, `description`, `file`
  - returns: 201 `ActivityResource`
  - errors: 400 `invalid_id`, `invalid_body`, `invalid_type`, `title_required`,
    `description_required`, `file_required`; 413 `file_too_large`
- **PUT** `/activities/{id}/resources/order` — *Admin, board, organizer or participant*. Replaces
  the whole order with the complete list of ids.
  - body: `resource_ids*`
  - returns: 200 `[ActivityResource]`
  - errors: 400 `invalid_id`, `invalid_body`; 409 `reorder_mismatch`
- **GET** `/activities/{id}/resources/{resourceID}` — *Any baseline role*. One resource.
  - returns: 200 `ActivityResource`
  - errors: 400 `invalid_id`; 404 `not_found`
- **PATCH** `/activities/{id}/resources/{resourceID}` — *Its author, admin or board*. Changes title
  or description.
  - body: `title`, `description`
  - returns: 200 `ActivityResource`
  - errors: 400 `invalid_id`
- **DELETE** `/activities/{id}/resources/{resourceID}` — *Its author, admin or board*. Deletes the
  resource and its file.
  - returns: 204
  - errors: 400 `invalid_id`
- **GET** `/activities/{id}/resources/{resourceID}/file` — *Any baseline role*. Bytes of a
  resource's file (`?download=1` to download it).
  - query: `download`
  - errors: 400 `not_a_file`

## Equipment

- **GET** `/items` — *Any baseline role*. Catalog of equipment.
  - returns: 200 `[Item]`
- **POST** `/items` — *Admin or board*. Adds an item (rope, bag or generic equipment).
  - body: `item_type*`, `brand`, `model`, `code`, `description`, `acquired_at`, `total_quantity`,
    `loanable`, `rental_price_per_week`, `sale_price`, `rope_type`, `diameter_mm`,
    `manufacture_year`, `length_m`, `bag_type`, `capacity_liters`, `category_id`
  - returns: 201 `Item`
  - errors: 400 `invalid_body`, `invalid_item_type`, `invalid_date`, `unknown_category`,
    `single_unit_item`, `invalid_item_fields`
- **GET** `/material-categories` — *Admin or board*. Categories of generic equipment.
  - returns: 200 `[MaterialCategory]`
- **GET** `/items/{id}/movements` — *Any baseline role*. Movement history of an item.
  - returns: 200 `[Movement]`
  - errors: 400 `invalid_id`
- **GET** `/movements` — *Admin, board or material*. Movements: the active ones by default, or all
  those of an activity with `activity_id`.
  - query: `status`, `activity_id`
  - returns: 200 `[Movement]`
  - errors: 400 `invalid_activity_id`
- **POST** `/movements` — *Admin, board or material*. Records a movement (loan, rental, sale,
  joining, write-off, return).
  - body: `item_id*`, `login`, `type*`, `quantity`, `transacted_at`, `activity_id`, `notes`
  - returns: 201 `Movement`
  - errors: 400 `invalid_body`, `invalid_date`, `invalid_type`, `login_required`,
    `recipient_not_allowed`, `activity_required`; 404 `not_found`; 409 `insufficient_stock`,
    `not_loanable`, `not_rentable`, `single_unit_item`, `return_exceeds_loaned`
- **PATCH** `/movements/{id}/return` — *Admin, board or material*. Closes an open rental.
  - returns: 200 `Movement`
  - errors: 400 `invalid_id`; 404 `not_found`
- **GET** `/users/{login}/movements` — *The person themselves, admin, board or material*. Purchases,
  rentals and loans of a person.
  - returns: 200 `[Movement]`

## Distributions

A distribution is something the club hands out to its members (shirts, lottery...). Statuses:
`draft`, `open`, `closed`, `done`. Paying and taking off a payment require saying in which `asset`
account it was made (`account_id`) and record a transaction in the distribution's account.

**Visibility.** A distribution's `visibility` says who can see it, and so do anything about it:
`all` (the default, as it always was), `material` (material, board and admin), `board` (board and
admin) or `admin`. For anybody below that level every route about the distribution — the list, the
distribution, its units, orders, payments, numbers — answers as if it did not exist (404
`not_found`). A draft is only for admin and board whatever its `visibility` says. Only admin sets or
changes it.

- **GET** `/distributions` — *Any baseline role*. Distributions, with `eligible` for the caller. A
  draft is only seen by admin and board, and one whose `visibility` is above the caller's role is
  left out (see below).
  - returns: 200 `[Distribution]`
- **POST** `/distributions` — *Admin or board*. Creates a distribution, as a draft.
  - body: `name*`, `description`, `audience`, `visibility`, `closes_at`, `cutoff_date`,
    `account_id`, `price`, `extra_price`, `max_extra`, `options`, `articles`, `status`
  - returns: 201 `Distribution`
  - errors: 400 `invalid_name`, `invalid_visibility`; 403 `forbidden` (board setting a `visibility`
    other than `all`)
- **GET** `/distributions/{id}` — *Any baseline role*. One distribution (a draft is 404 for everyone
  but admin and board, and so is one the caller's role is below its `visibility`).
  - returns: 200 `Distribution`
- **PATCH** `/distributions/{id}` — *Admin or board*. Replaces all the fields (`options`,
  `articles`, `status` and `visibility` are left as they are when not sent); it also serves to
  change the status one step. Only admin changes `visibility` (403 `forbidden` for board; sending
  back the current one is fine).
  - body: `name*`, `description`, `audience`, `visibility`, `closes_at`, `cutoff_date`,
    `account_id`, `price`, `extra_price`, `max_extra`, `options`, `articles`, `status`
  - returns: 200 `Distribution`
  - errors: 409 `invalid_status_transition`
- **DELETE** `/distributions/{id}` — *Admin*. Deletes the distribution with its orders, unless one
  of them is paid or collected.
  - returns: 204
  - errors: 409 `has_settled_units`
- **GET** `/distributions/{id}/people` — *`for=order`: any baseline role; `for=pickup`: admin, board
  or material*. Members that the ordering and collection selectors offer.
  - query: `for`
  - returns: 200 `[{login, first_name, last_name}]`
- **GET** `/distributions/{id}/units` — *Admin, board or material see all; everyone else their own*.
  Orders of the distribution.
  - returns: 200 `[DistributionUnit]`
- **POST** `/distributions/{id}/units` — *Any baseline role*. Orders a unit (or a quantity of an
  article), for oneself or for another member. Members only while the distribution is open and
  before its deadline; admin, board and material at any time but in a draft.
  - body: `beneficiary_login`, `is_extra`, `article_id`, `quantity`, `choices`
  - returns: 201 `DistributionUnit`
  - errors: 400 `invalid_choices`; 409 `not_open`
- **PATCH** `/distributions/{id}/units/{unitID}` — *The person it is for, whoever entered it, admin,
  board or material*. Changes the choices or the quantity.
  - body: `choices`, `quantity`
  - returns: 200 `DistributionUnit`
  - errors: 400 `invalid_choices`, `invalid_quantity`; 409 `not_open`
- **DELETE** `/distributions/{id}/units/{unitID}` — *The person it is for, whoever entered it,
  admin, board or material*. Removes an order (not the automatic one of a distribution for
  everybody, nor one with something paid).
  - returns: 204
  - errors: 409 `not_open`
- **GET** `/distributions/{id}/order` — *Admin, board or material*. What has to be ordered: units
  summed by combination of options.
  - returns: 200 `DistributionOrder`
- **GET** `/distributions/{id}/articles/{articleID}/numbers` — *Admin, board or material*. All the
  numbers of an article and who has each one.
  - returns: 200 `JSON`
  - errors: 400 `article_not_numbered`, `unknown_article`
- **PUT** `/distributions/{id}/units/{unitID}/identifiers` — *Admin, board or material*. Replaces
  the numbers of an order.
  - body: `identifiers`
  - returns: 200 `DistributionUnit`
  - errors: 400 `invalid_identifiers`; 409 `not_deliverable`, `identifier_taken`
- **POST** `/distributions/{id}/units/{unitID}/identifiers` — *Admin, board or material*. Adds
  numbers to an order.
  - body: `identifiers`
  - returns: 200 `DistributionUnit`
  - errors: 400 `invalid_identifiers`; 409 `not_deliverable`, `identifier_taken`
- **DELETE** `/distributions/{id}/units/{unitID}/identifiers` — *Admin, board or material*. Removes
  numbers from an order.
  - query: `identifiers`*
  - returns: 200 `DistributionUnit`
  - errors: 400 `invalid_identifiers`; 409 `not_deliverable`
- **POST** `/distributions/{id}/units/{unitID}/identifiers/assign` — *Admin, board or material*.
  Gives an order the next free numbers.
  - returns: 200 `DistributionUnit`
  - errors: 400 `article_not_numbered`; 409 `not_deliverable`
- **POST** `/distributions/{id}/pickups` — *Admin, board or material*. Records collections, in a
  batch and all or nothing.
  - body: `unit_ids`, `deliveries`, `picked_up_by_login*`, `assign_numbers`
  - returns: 204
  - errors: 400 `no_units`; 409 `not_deliverable`
- **DELETE** `/distributions/{id}/units/{unitID}/pickup` — *Admin, board or material*. Undoes a
  collection.
  - query: `quantity`, `identifiers`
  - returns: 204
- **POST** `/distributions/{id}/units/{unitID}/return` — *Admin, board or material*. Returns
  collected units: they leave the order and their numbers become free.
  - body: `quantity`, `identifiers`
  - returns: 204
  - errors: 400 `invalid_quantity`, `invalid_identifiers`; 409 `not_deliverable`,
    `quantity_below_paid`
- **GET** `/distributions/{id}/payments` — *Admin or board*. List of the distribution's payments,
  with the running total.
  - query: `from`, `to`, `limit`, `offset`
  - returns: 200 `[DistributionPayment]`
  - errors: 400 `invalid_date`, `invalid_date_range`
- **POST** `/distributions/{id}/payments` — *Admin or board*. Marks payments, in a batch and all or
  nothing, and records them in Accounting.
  - body: `account_id`, `unit_ids`, `payments`
  - returns: 204
  - errors: 400 `no_units`; 409 `already_paid`
- **DELETE** `/distributions/{id}/units/{unitID}/payment` — *Admin or board*. Takes off a payment,
  with its reverse transaction.
  - query: `account_id`, `quantity`, `identifiers`
  - returns: 204
  - errors: 400 `account_required`; 409 `no_distribution_account`

## Document archive and per-person files

Directory trees on disk, read-only: none of this is in the database.

- **GET** `/library` — *Any baseline role*. Lists a directory of the club's archive (`?path=`), in
  the order of its optional `.order.json` when it has one, else alphabetically.
  - query: `path`
  - returns: 200 `LibraryListing`
  - errors: 400 `invalid_path`, `not_a_directory`
- **GET** `/library/file/{path}` — *Any baseline role*. Content of a document. It also accepts the
  session cookie, for ordinary browser links.
  - query: `download`
  - errors: 400 `invalid_path`, `not_a_file`
- **GET** `/users/{login}/files` — *The person themselves, admin or board*. Lists a directory of a
  person's files.
  - query: `path`
  - returns: 200 `UserFileListing`
  - errors: 400 `invalid_path`, `not_a_directory`
- **GET** `/users/{login}/files/file/{path}` — *The person themselves, admin or board*. Content of a
  person's file (also with the session cookie).
  - query: `download`
  - errors: 400 `invalid_path`, `not_a_file`

## Accounting

Chart of accounts as a tree and double-entry transactions. Reading for any baseline role; writing
for admin or the optional `money` role. `GET /transactions` and an account's ledger are paged with
`limit` (50 by default, at most 100) and `offset`, and narrowed with `from` and `to` (inclusive
days, `YYYY-MM-DD`) and `q` (words that must all be in the description, in any order and ignoring
case and accents).

- **GET** `/accounts` — *Any baseline role*. Chart of accounts in tree order, with `balance` and
  `last_transaction_at` (in a group, those of its descendants).
  - returns: 200 `[Account]`
- **POST** `/accounts` — *Admin or money*. Creates an account.
  - body: `name*`, `code`, `parent_id`, `type`, `allows_dues`, `allows_licenses`,
    `distribution_default`, `hidden`
  - returns: 201 `Account`
  - errors: 400 `invalid_body`, `missing_fields`, `invalid_type`, `unknown_parent_account`; 409
    `duplicate_code`, `parent_has_entries`
- **GET** `/accounts/{id}` — *Any baseline role*. One account.
  - returns: 200 `Account`
  - errors: 400 `invalid_id`
- **PATCH** `/accounts/{id}` — *Admin or money*. Corrects name, code, parent account, type and
  flags. Replaces all the fields.
  - body: `name*`, `code`, `parent_id`, `type`, `allows_dues`, `allows_licenses`,
    `distribution_default`, `hidden`
  - returns: 200 `Account`
  - errors: 400 `invalid_id`; 409 `duplicate_code`
- **DELETE** `/accounts/{id}` — *Admin or money*. Deletes an account with no children and no
  entries.
  - returns: 204
  - errors: 400 `invalid_id`; 409 `account_in_use`
- **POST** `/accounts/{id}/move` — *Admin or money*. Swaps the order with the previous (`up`) or
  next (`down`) sibling. Returns the whole chart.
  - body: `direction*`
  - returns: 200 `[Account]`
  - errors: 400 `invalid_id`
- **GET** `/accounts/{id}/transactions` — *Any baseline role*. Ledger of the account, with running
  balance. For an account that groups others, the entries of all its descendants.
  - query: `limit`, `offset`, `from`, `to`, `q`
  - returns: 200 `[AccountEntry]`
  - errors: 400 `invalid_id`
- **GET** `/transactions` — *Any baseline role*. Transactions, most recent first.
  - query: `limit`, `offset`, `from`, `to`, `q`
  - returns: 200 `[Transaction]`
  - errors: 400 `invalid_date`, `invalid_date_range`
- **POST** `/transactions` — *Admin or money*. Records a transaction: two or more entries that sum
  to zero.
  - body: `description*`, `occurred_at*`, `entries*`
  - returns: 201 `Transaction`
  - errors: 400 `invalid_body`; 409 `unbalanced`, `account_has_children`
- **GET** `/transactions/{id}` — *Any baseline role*. One transaction; admin and board also see the
  dues and licenses it paid.
  - returns: 200 `JSON`
  - errors: 400 `invalid_id`
- **PATCH** `/transactions/{id}` — *Admin or money*. Replaces description, date and entries.
  - body: `description*`, `occurred_at*`, `entries*`
  - returns: 200 `Transaction`
  - errors: 400 `invalid_id`; 409 `unbalanced`
- **DELETE** `/transactions/{id}` — *Admin or money*. Deletes the transaction; if it was the payment
  of a distribution, it undoes it.
  - returns: 204
  - errors: 400 `invalid_id`; 409 `transaction_has_licenses`,
    `transaction_has_distribution_payments`

## Objects

Fields of the objects the API returns or receives (`*` = required when sending it; a field without a
mark may be missing or null). The detail of each one is in `components.schemas` of the OpenAPI.

### `Error`

| Field | Type |
|---|---|
| `error` | object |

Nested fields:

- `error`: `{code, message}`

### `AdminStatus`

| Field | Type |
|---|---|
| `app` | object |
| `database` | object |
| `mail` | object |
| `telegram` | object |
| `settings` | object |
| `storage` | [object] |
| `disks` | [object] |
| `problems` | [text] |

Nested fields:

- `app`: `{version, vcs_revision, vcs_time, vcs_modified, go_version, started_at}`
- `database`: `{ok, latency_ms, error, migration_version}`
- `mail`: `{smtp_configured}`
- `telegram`: `{log_channel_configured, bot_running, bot_username}`
- `settings`: `{public_base_url, session_ttl_seconds, local_session_ttl_seconds,
  magic_link_ttl_seconds, magic_link_cooldown_seconds, impersonation_ttl_seconds,
  bot_dm_link_ttl_seconds}`
- `storage`: `[{name, path, exists}]`
- `disks`: `[{paths, total_bytes, free_bytes, used_percent}]`, one per filesystem that holds some of
  the storage directories
- `problems` is a list of sentences in Spanish saying what is wrong (database not answering, a
  missing directory, a disk at `DISK_ALERT_PERCENT` or more); empty when all is well

### `Token`

| Field | Type |
|---|---|
| `id` | integer |
| `user_login` | text |
| `created_at` | datetime |
| `expires_at` | datetime |
| `revoked_at` | datetime |
| `last_used_at` | datetime |
| `last_used_ip` | text |
| `last_used_user_agent` | text |
| `browser` | text |
| `os` | text |
| `current` | boolean |
| `impersonated_by_login` | text |

### `Role`

| Field | Type |
|---|---|
| `id` | integer |
| `name` | text |
| `description` | text |
| `is_system` | boolean |
| `is_automatic` | boolean |

### `User`

| Field | Type |
|---|---|
| `login` | text |
| `first_name` | text |
| `last_name` | text |
| `national_id` | text |
| `birth_date` | date |
| `gender` | Gender |
| `email` | text |
| `phone` | text |
| `address` | text |
| `postal_code` | text |
| `city` | text |
| `province` | text |
| `emergency_contact` | text |
| `roles` | [text] |
| `last_login_at` | datetime |
| `last_login_ip` | text |
| `membership_number` | integer |
| `membership_start_date` | date |
| `membership_end_date` | date |
| `on_leave` | boolean |
| `last_due_year` | integer |
| `last_due_paid_at` | datetime |
| `license_type` | text |
| `license_year` | integer |
| `license_issued_date` | date |
| `created_at` | datetime |

### `MemberListing`

| Field | Type |
|---|---|
| `login` | text |
| `first_name` | text |
| `last_name` | text |
| `gender` | Gender |
| `age_category` | youth/child/junior/senior/veteran_a/veteran_b |
| `membership_number` | integer |
| `membership_start_date` | date |
| `membership_end_date` | date |
| `on_leave` | boolean |
| `license_type` | text |
| `license_year` | integer |
| `last_due_year` | integer |

### `DirectoryEntry`

| Field | Type |
|---|---|
| `login` | text |
| `first_name` | text |
| `last_name` | text |

### `CreateUserInput`

| Field | Type |
|---|---|
| `login` | text |
| `first_name*` | text |
| `last_name*` | text |
| `email` | text |
| `national_id` | text |
| `birth_date*` | date |
| `gender` | object |
| `phone` | text |
| `address` | text |
| `postal_code` | text |
| `city` | text |
| `province` | text |
| `emergency_contact` | text |
| `role_names` | [text] |

### `UpdateUserInput`

| Field | Type |
|---|---|
| `login` | text |
| `first_name` | text |
| `last_name` | text |
| `email` | text |
| `national_id` | text |
| `birth_date` | date |
| `gender` | object |
| `phone` | text |
| `address` | text |
| `postal_code` | text |
| `city` | text |
| `province` | text |
| `emergency_contact` | text |

### `Membership`

| Field | Type |
|---|---|
| `id` | integer |
| `user_login` | text |
| `first_name` | text |
| `last_name` | text |
| `start_date` | date |
| `end_date` | date |
| `end_reason` | text |

### `Leave`

| Field | Type |
|---|---|
| `id` | integer |
| `user_login` | text |
| `start_date` | date |
| `end_date` | date |
| `reason` | text |

### `Due`

| Field | Type |
|---|---|
| `id` | integer |
| `user_login` | text |
| `year` | integer |
| `transaction_id` | integer |
| `created_at` | datetime |
| `paid_at` | datetime |

### `LicenseType`

| Field | Type |
|---|---|
| `id` | integer |
| `name` | text |
| `is_other` | boolean |

### `License`

| Field | Type |
|---|---|
| `id` | integer |
| `user_login` | text |
| `license_type_id` | integer |
| `type` | text |
| `other_detail` | text |
| `issued_date` | date |
| `year` | integer |
| `valid_until` | date |
| `transaction_id` | integer |
| `created_at` | datetime |

### `BoardMember`

| Field | Type |
|---|---|
| `id` | integer |
| `user_login` | text |
| `first_name` | text |
| `last_name` | text |
| `email` | text |
| `phone` | text |
| `position` | BoardPosition |
| `start_date` | date |
| `end_date` | date |

### `Activity`

| Field | Type |
|---|---|
| `id` | integer |
| `title` | text |
| `type` | caving/canyoning/training/social/other |
| `start_date` | date |
| `end_date` | date |
| `description` | text |
| `status` | planned/completed/cancelled |
| `leader_login` | text |
| `visibility` | public/private |

### `CreateActivityInput`

| Field | Type |
|---|---|
| `title*` | text |
| `type` | caving/canyoning/training/social/other |
| `start_date*` | date |
| `end_date*` | date |
| `description` | text |
| `visibility` | public/private |
| `participant_logins` | [text] |
| `leader_login` | text |

### `UpdateActivityInput`

| Field | Type |
|---|---|
| `title` | text |
| `type` | caving/canyoning/training/social/other |
| `start_date` | date |
| `end_date` | date |
| `description` | text |
| `status` | planned/completed/cancelled |
| `visibility` | public/private |
| `leader_login` | text |

### `Participant`

| Field | Type |
|---|---|
| `activity_id` | integer |
| `user_login` | text |
| `role` | leader/participant |
| `confirmed` | boolean |

### `Distribution`

| Field | Type |
|---|---|
| `id` | integer |
| `name` | text |
| `description` | text |
| `audience` | all_members/signup |
| `visibility` | all/material/board/admin |
| `status` | draft/open/closed/done |
| `closes_at` | datetime |
| `cutoff_date` | date |
| `account_id` | integer |
| `account_name` | text |
| `price` | number |
| `extra_price` | number |
| `max_extra` | integer |
| `options` | [object] |
| `articles` | [object] |
| `created_by_login` | text |
| `created_at` | datetime |
| `unit_count` | integer |
| `picked_up_count` | integer |
| `eligible` | boolean |

Nested fields:

- `options`: `[{name, values}]`
- `articles`: `[{id, name, price, max_total, max_per_person, has_identifiers, identifier_fields,
  identifier_total, identifiers_assigned, requested}]`

### `DistributionInput`

| Field | Type |
|---|---|
| `name*` | text |
| `description` | text |
| `audience` | all_members/signup |
| `closes_at` | datetime |
| `cutoff_date` | date |
| `account_id` | integer |
| `price` | number |
| `extra_price` | number |
| `max_extra` | integer |
| `options` | [object] |
| `articles` | [object] |
| `status` | draft/open/closed/done |

Nested fields:

- `options`: `[{name, values}]`
- `articles`: `[{name, price, max_total, max_per_person, has_identifiers, identifier_fields}]`

### `IdentifierField`

| Field | Type |
|---|---|
| `name*` | text |
| `spec*` | text |

### `DistributionUnit`

| Field | Type |
|---|---|
| `id` | integer |
| `distribution_id` | integer |
| `beneficiary_login` | text |
| `beneficiary_name` | text |
| `registered_by_login` | text |
| `registered_at` | datetime |
| `is_extra` | boolean |
| `article_id` | integer |
| `article_name` | text |
| `identifiers` | [text] |
| `quantity` | integer |
| `paid_quantity` | integer |
| `choices` | object |
| `price` | number |
| `paid_at` | datetime |
| `paid_marked_by_login` | text |
| `picked_up_quantity` | integer |
| `delivered_identifiers` | [text] |
| `paid_identifiers` | [text] |
| `picked_up_at` | datetime |
| `pickup_recorded_by_login` | text |
| `picked_up_by_login` | text |

### `DistributionPayment`

| Field | Type |
|---|---|
| `id` | integer |
| `distribution_id` | integer |
| `distribution_name` | text |
| `unit_id` | integer |
| `transaction_id` | integer |
| `occurred_at` | datetime |
| `recorded_by_login` | text |
| `recorded_by_name` | text |
| `paid_by_login` | text |
| `paid_by_name` | text |
| `description` | text |
| `quantity` | integer |
| `amount` | number |
| `identifiers` | [text] |
| `total_after` | number |

### `DistributionOrder`

| Field | Type |
|---|---|
| `lines` | [object] |
| `total_units` | integer |
| `total_amount` | number |
| `provisional` | boolean |

Nested fields:

- `lines`: `[{article_id, article_name, choices, count}]`

### `ClubInfoItem`

| Field | Type |
|---|---|
| `id` | integer |
| `position` | integer |
| `label` | text |
| `value` | text |
| `copy_value` | text |
| `copy_label` | text |

`copy_value` is the text a button next to the value copies, and `copy_label` the button's text
(it needs a `copy_value`); both are left out when the item has no button.

### `Holiday`

| Field | Type |
|---|---|
| `id` | integer |
| `date` | date |
| `name` | text |

### `ActivityResource`

| Field | Type |
|---|---|
| `id` | integer |
| `activity_id` | integer |
| `type` | text/file |
| `author_login` | text |
| `title` | text |
| `description` | text |
| `file_name` | text |
| `file_size` | integer |
| `content_type` | text |
| `position` | integer |
| `created_at` | datetime |

### `MaterialCategory`

| Field | Type |
|---|---|
| `id` | integer |
| `name` | text |

### `Item`

| Field | Type |
|---|---|
| `id` | integer |
| `item_type` | rope/bag/gear |
| `brand` | text |
| `model` | text |
| `code` | text |
| `description` | text |
| `total_quantity` | integer |
| `available_quantity` | integer |
| `outstanding_loan_quantity` | integer |
| `status` | available/on_loan/rented/sold/lost/abandoned/maintenance/retired |
| `loanable` | boolean |
| `rentable` | boolean |
| `sellable` | boolean |
| `rental_price_per_week` | number |
| `sale_price` | number |
| `rope_type` | caving/canyoning |
| `diameter_mm` | number |
| `manufacture_year` | integer |
| `length_m` | integer |
| `bag_type` | caving/canyoning/quick/special |
| `capacity_liters` | number |
| `category_id` | integer |
| `category_name` | text |

### `CreateItemInput`

| Field | Type |
|---|---|
| `item_type*` | rope/bag/gear |
| `brand` | text |
| `model` | text |
| `code` | text |
| `description` | text |
| `acquired_at` | date |
| `total_quantity` | integer |
| `loanable` | boolean |
| `rental_price_per_week` | number |
| `sale_price` | number |
| `rope_type` | caving/canyoning |
| `diameter_mm` | number |
| `manufacture_year` | integer |
| `length_m` | integer |
| `bag_type` | caving/canyoning/quick/special |
| `capacity_liters` | number |
| `category_id` | integer |

### `Movement`

| Field | Type |
|---|---|
| `id` | integer |
| `item_id` | integer |
| `user_login` | text |
| `type` | loan/rental/sale/acquisition/loss/abandonment/retirement/return |
| `quantity` | integer |
| `transacted_at` | date |
| `returned_at` | date |
| `price` | number |
| `status` | active/completed/overdue/cancelled |
| `recorded_by_login` | text |
| `activity_id` | integer |
| `notes` | text |

### `LibraryEntry`

| Field | Type |
|---|---|
| `name` | text |
| `type` | dir/file |
| `size` | integer |
| `modified_at` | datetime |

### `LibraryListing`

| Field | Type |
|---|---|
| `path` | text |
| `entries` | [LibraryEntry] |

### `UserFileEntry`

| Field | Type |
|---|---|
| `name` | text |
| `type` | dir/file |
| `size` | integer |
| `modified_at` | datetime |

### `UserFileListing`

| Field | Type |
|---|---|
| `path` | text |
| `entries` | [UserFileEntry] |

### `Account`

| Field | Type |
|---|---|
| `id` | integer |
| `name` | text |
| `code` | text |
| `parent_id` | integer |
| `type` | asset/income/expense |
| `effective_type` | asset/income/expense |
| `allows_dues` | boolean |
| `allows_licenses` | boolean |
| `distribution_default` | boolean |
| `hidden` | boolean |
| `balance` | number |
| `last_transaction_at` | datetime |

### `AccountEntry`

| Field | Type |
|---|---|
| `transaction_id` | integer |
| `account_id` | integer |
| `account_name` | text |
| `occurred_at` | datetime |
| `description` | text |
| `amount` | number |
| `balance_after` | number |

### `Entry`

| Field | Type |
|---|---|
| `id` | integer |
| `account_id` | integer |
| `amount` | number |

### `EntryInput`

| Field | Type |
|---|---|
| `account_id*` | integer |
| `amount*` | number |

### `Transaction`

| Field | Type |
|---|---|
| `id` | integer |
| `description` | text |
| `occurred_at` | datetime |
| `created_at` | datetime |
| `entries` | [Entry] |

### Enumerations

- **`Gender`**: male / female
- **`BoardPosition`**: president / vice_president / secretary / treasurer / member_at_large
