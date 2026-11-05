# Frontend

How the web frontend (`web/`) is built and how each page works. The conventions every page shares
come first, then the pages one by one. The endpoints a page calls are in [`api.md`](api.md); the
rules behind them, in [`backend-internals.md`](backend-internals.md) and
[`architecture.md`](architecture.md).

**Frontend** (`web/`): classic multi-page site (no SPA, no build step — a deliberate choice, not
just an unexamined default: every page already revalidates its own session and re-fetches its own
data on load, so there's no cross-page state an SPA would actually save, and this stays free of a
bundler or any client-side router), one `<page>.html` + `js/<page>.js` per view, Caddy's `try_files
{path} {path}.html` making `/socios` resolve to `socios.html`. The sidebar and topbar, identical on
every authenticated page, are rendered once from `js/layout.js`'s `initPage()` rather than
copy-pasted into each `<page>.html` — every one of them reduces to an empty `<aside id="sidebar">`/
`<header id="topbar">` pair for it to fill in. `initPage()` is also each page's own session
bootstrap (in place of what used to be five near-identical lines re-typed at the top of every
`<page>.js`): it validates the session, applies role-based visibility, and wires the sidebar toggle,
topbar greeting, and logout button, returning the signed-in user same as `currentUser()` would. The
menu collapses below 800px (an overlay behind the hamburger) and, on a wide screen, can be hidden
with the "×" at the top of it: `api.js`'s `initSidebarToggle` puts `.sidebar-hidden` on `.shell` —
which looks like the narrow layout, `style.css` keeps the two rules alike — and remembers it in
`localStorage` (`katiuskas:sidebar-hidden`, applied as `api.js` loads so it doesn't flash); there
the hamburger brings the normal layout back instead of overlaying the menu (`NARROW_SCREEN` there
has to match the media query). The content is at most 1400px wide (`.content`), the full width up to
there so that tables use it; running text keeps 75 characters and the panels of filters and forms
960px, whatever the width. Its own `requireAnyRole` option additionally redirects home if the
signed-in user holds none of the listed roles — the whole-page gates of `member.js` (admin/board),
of `accounting.js`/`account.js` (admin/board/money) and of `admin.js` (admin), tighter than the
ordinary baseline check every API route already enforces. Which sidebar link reads as "active" is
computed from `location.pathname` (`ACTIVE_ALIASES` mapping a detail page like `/actividad` or
`/socio` to the listing it was reached from, `/actividades`/`/socios`) rather than a per-page
`class="active"` written by hand — one less thing a new page could forget. `login.html` is the one
page with no shell and no session yet, and never calls `initPage()`. `404.html` (`js/notfound.js`)
is what Caddy serves, with a 404 status and without changing the address, for anything that isn't a
page (`handle_errors` in `deploy/Caddyfile`, which is why `root` is declared at the site level there
— an error handler can't see one set inside the `handle`; API responses are untouched). It can't
assume who is looking: with a session token it becomes an ordinary page (`initPage()`, menu
included), imported only then because `api.js` reads the token as soon as it loads; without one it
shows a plain panel linking to `/login`. Everything it loads is by absolute path, since it is served
at whatever address was asked for, however deep — which is also why `layout.js`'s topbar logo is
`/images/…` and not relative.

**Editing material.** On `/material` ("Editar" in each row, for admin, board and material) the
dialog of "Nuevo material" is reused to edit an item (`material.js`, `openEditItemDialog`): the
title and the button change, the type is fixed for a rope or a bag (the field is disabled) and, for
general material, can be changed to another general type (Cuerda and Saca are not offered), the
quantity and the date of acquisition are left out (the stock is what its movements say) and the form
is filled with the item; it sends `PUT /material/items/{id}`, which replaces every field. The
"Tipo" select lists every type from `GET /material/types` (Cuerda and Saca first); the fields shown
depend on the *kind* of the chosen type (rope fields for Cuerda, bag fields for Saca, description
and quantity for any other), each option carrying it in `data-kind`. Brand, model and code are in
the form for every type, required only for ropes and bags; the model field stays disabled (and
empty) while there is no brand.

**Material that is out.** `/material` has a "Material prestado o alquilado" button (for admin, board
and material) to `/material-prestado` (`loanedMaterial.js`), which has what used to be at the bottom
of `/material`: *Prestado*, the items whose loans have not all come back
(`outstanding_loan_quantity`), with the units pending and, from each item's own movements, the
activities they are out for (loans minus returns, per activity), and *Alquilado*, the open rentals
(`GET /material/movements?status=active`) with a "Devolver" button. A loan is returned from the page
of its activity, so this page only reports them. `materialCommon.js` has how an item is named,
shared with `/material`. In the menu it counts as part of Material (`ACTIVE_ALIASES`).

**Movement types on `/material`.** "+ Movimiento" only offers what can be done with that item right
now (`movementTypeOptionsFor` in `material.js`; the API would refuse the rest): *Alquiler* if it
has a rental price and a unit available, *Venta* if it has a sale price and a unit available,
*Adquisición* only for general material (ropes and bags are a single unit), *Pérdida*, *Abandono*
and *Retirada* if a unit is available, and *Devolución* only if some of it is out on loan (a rental
is closed with the "Devolver" button instead). An item with nothing to offer has no "+ Movimiento"
button.

**How a person reads in a picker.** Just "Nombre Apellidos". A "#login" goes after the name only for the
people who share it with somebody else in the same list, so the two can be told apart and what is typed
still resolves to one person (`personPickerLabeler` in `api.js`; it is given the whole list the
picker is made from, not the part of it that is offered). Pickers of other things (activities,
material items) still carry their "#id".

**How a piece of material reads.** Every listing of material (`/material`, `/material-prestado`, the
loans of `/actividad` and the history of `/socio`) writes it with `itemLabel` (`materialCommon.js`),
which ends with "#<code>" or, for an item with no code, "#<id>" (`itemRef`); the picker of the loans
of `/actividad` ends the same way. A code is never only digits, so the two cannot be mistaken.

**Who the pickers offer.** `GET /users/directory` says whether each person is a `member` now or a
`former_member`, and the pickers offer only whom the thing is open to (the API refuses the rest):
the participants of an activity — "Añadir participante" on `/actividad` and the participants of
"Nueva actividad" on `/actividades` — and the "Socio" of a loan (on `/actividad`) or of a rental
(on `/material`) are only members; the "Socio" of a sale on `/material` is a member or a former
member (it follows the type chosen, and what was typed before and no longer fits is cleared). The
organizer of an activity is still any person. In the participants list of `/actividad`, each name
has a "Federado" or "No federado" badge (`federated`), as information: nothing requires it.

**Signing people up on `/actividad`.** In a public activity anybody sees the "add a person" row and
the "Apuntarme" button; in a private one only the organizer, admin and board (and, for the row,
whoever is already signed up: it appears as soon as they are, as the button turns into "Darme de
baja"). "Apuntarme" is only offered to members (`canAddOthers`, `canJoinSelf` in `activity.js`).

In the participants list the "Quitar" button sits right after the name it is about
(`.participant-row`), not at the far end of the row, and only on the rows the viewer may remove
(`canRemoveParticipant`, as the API decides: admin/board, the organizer of a private activity, and
whoever signed that person up; never on the viewer's own row, which is "Darme de baja").

**Guests on `/actividad`.** Under the participants, an "Invitados" section lists the people from
outside the club who go (`GET /activities/{id}/guests`), each with a badge "Invitado de <name>" (who
of the club invites them). A line with the total (people of the club signed up and, only when there
are guests, how many and the sum: `#attendance`, `renderAttendance`) sits at the top, after the
description and the buttons and before the participants. Whoever is signed up sees a row to add one
with just a name; the organizer, admin and board see it too, with a second field to choose which of
the people signed up invites them (the datalist `#guest-inviter-options`, filled from the
participants). The "Quitar" button of a guest is for whoever invited them, the organizer, admin and
board (`renderGuestRow`, as the API decides). A person who still has guests cannot leave or be
removed from the activity: the error text says to remove them first (`has_guests`).

**The limit of people.** "Nueva actividad" (`/actividades`) and "Editar actividad" (`/actividad`)
have an optional "Máximo de apuntados" (`max_participants`): blank is no limit, and the edit form,
which resends everything, sends `null` for a blank one. On `/actividad` the total line
(`#attendance`) says "…de un máximo de N" and, once the places are all taken, "la actividad está
completa", and then "Apuntarme", "+ Participante" and "+ Invitado" are disabled with that as their
tooltip (`renderAttendance`; the API refuses it too, 409 `activity_full`). Lowering the limit under
the people already going is refused with its own message (`max_below_attendance`).

**The organizer on `/actividad`.** Under the header line, "Organizador: <name>" (`#activity-leader`;
a link to the person's page only for admin and board, like every name there), once the directory has
the name.

**Being signed up on `/actividad`.** Under the header, a note "✓ Estás apuntado a esta actividad."
shows while the viewer is among the participants (`#joined-note`, set by `loadParticipants` together
with the "Apuntarme"/"Darme de baja" buttons), so it is not something to look for in a long list.

**Loans and returns on `/actividad`.** The "+ Préstamo/devolución" dialog (`activity.js`) offers, as
the material to pick, only what fits the type chosen (`loanItemEntriesFor`): for a *Préstamo*, items
that can be loaned and have a unit available; for a *Devolución*, those still out on loan for this
activity (what its loans minus its returns say, `outstandingByItem`). The types themselves are only
offered if something fits (and with neither, the button is off), changing the type clears a pick
that no longer fits, and the quantity's `max` is what is available or what is out.

**An activity's state.** The API only says whether an activity is `cancelled`; whether it is still
to come, going on or over is worked out from its dates by `activityState` (`api.js`): *Cancelada* if
cancelled, *Realizada* if its `end_date` is before today, *Planificada* if its `start_date` is after
today, *En curso* otherwise (`activityStateLabels` has the texts; the badge's CSS class is
`status-<state>`). `activities.js` (list, with its "active only" filter `?active=true`, and the
calendar, which leaves cancelled ones out), `activity.js` (the header, and, for whoever can manage
the activity, a "Cancelar actividad" button next to "Editar actividad", after a confirmation —
`PATCH` with `cancelled: true` — which becomes "Reactivar actividad" while it is cancelled, `PATCH`
with `cancelled: false`; the edit dialog has no state field) and `member.js` use it, and
`material.js` asks for `?active=true` for its activity picker.

**Going back to the page that was asked for.** When `initSession()` finds no session (no token, or
`GET /auth/me` rejects it), it keeps the address asked for (path and query, except `/` and
`/login`) in `localStorage` under `katiuskas:return-to`, with the time, and sends the person to
`/login`; `auth.js` calls `goAfterLogin()` (`api.js`) wherever a sign-in ends, which goes there
instead of `/` and forgets it. It is localStorage and not sessionStorage because the magic link
may open in another tab. The saved address is used only if it is less than an hour old and is a
path of this site (it starts with a single `/`, never `//` or `/\`, and is not `/login`), so it
cannot take anybody elsewhere. Signing out on purpose forgets it.

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
for successes — pages no longer have an `#error` element.

**Something that does not exist.** A page about one thing (`/actividad?id=…`, `/cuenta?id=…`,
`/socio?login=…`, `/reparto?id=…` and its `-pedidos`, `-numeros` and `-pagos`, `/archivo?path=…` or
`?view=…`) answers the same way as an address that is not a page: `showNotFound()` (`api.js`) hides
the page's own content and puts "Esta página no existe" in its place, in the usual shell — no
redirect and no modal. It happens when the address has no usable id (`queryId()`: a positive whole
number, else null; `stopAsNotFound()` also keeps the rest of the page's script from running) and
when the API says the thing is not there (`isNotFound(err)`: `not_found` or `invalid_id`; the
library also treats `invalid_path`, `not_a_directory` and `not_a_file` that way). After that
`showError` stays silent, since the page's other requests fail for the same reason. Any other
failure (a 500, no network) still opens the error modal. `login.html`'s own inline messages next to
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

The impersonation banner (`renderImpersonationBanner`) shows "Tiempo restante: 42 min; después
volverás a tu sesión" and counts it down (minutes, then seconds in the last one), from
`impersonation_seconds_left` in `/auth/me` — a duration counted from when the answer arrived, not a
moment compared with this browser's clock, so a wrong clock can't make it reload early or over and
over — and when it reaches zero it reloads the page, which is what sends the first request after the
end (a 401) and puts the admin back (below). It waits half a second more than the API's answer,
which is rounded up, so that the reload does find the session ended.

Impersonation (`js/api.js`): `startImpersonation` swaps the admin's token for the impersonation's
and keeps the admin's own in `localStorage` (`katiuskas:original-token`; one started from inside
another keeps the first admin's). `restoreOriginalSession` puts it back, if it still works (asking
`/auth/me` with it, which also renews it and its cookie): the "Salir de la impersonación" button
uses it — ending the impersonation session on the server, and falling back to `POST
/auth/impersonate/exit` if there is nothing kept — and so does `apiFetch` on any 401 while one is
kept, which is what the end of the hour (or a revocation) looks like: it reloads `/socios` as the
admin, with the note "La impersonación ha terminado y has vuelto a tu sesión." (a `sessionStorage`
flash shown by `initSession`). Any other sign-in (`setToken`) forgets the kept token, so it can't be
restored later by mistake, and signing out while impersonating ends the admin's own session too.

The "Entrar como esta persona" button of `/socio` (`member.js`) is only shown, to admins, for
somebody with at least one of `admin`, `board` or `member`: with none of them the person cannot sign
in and the API refuses to impersonate them (409 `not_eligible`). It follows the person's roles
(`renderRoles`), which can change on that page.

"Ver como…" (`js/roleOverride.js`, wired by `initPage`): admins have a link in the topbar (removed
for anybody else by `data-roles`, and for an impersonation) that opens a dialog with the roles from
`GET /roles` as checkboxes, plus buttons for the usual combinations (Socio, Socio con material,
Socio con money, Junta, Administrador), and sends them with `PUT /auth/role-override`; then the page
reloads as somebody with those roles (the topbar link and the admin-only pages go away if `admin` is
not among them). While it is in force every page shows a banner (`renderRoleOverrideBanner` in
`api.js`, from `real_roles` in `GET /auth/me`) with the roles, the time left (counted down by the
same `countDownTimeLeft` as an impersonation, reloading at zero) and "Cambiar roles" (the same
dialog, through a `katiuskas:role-override` event) and "Volver a mis roles" (`DELETE
/auth/role-override`, then reload).

`js/api.js` is the underlying shared client `layout.js` itself is built on: it keeps the session
token in `localStorage`, and `initSession()` calls `GET /auth/me` on every page load to validate the
token and populate `currentUser()`. `applyRoleVisibility()` removes any `data-roles="admin,board"`
element the current user's roles don't satisfy — client-side UI hiding only; the API is the actual
authority. A permission that depends on data loaded asynchronously for one specific record (e.g. "is
the viewer this activity's organizer?") can't be a static `data-roles` attribute, since
`applyRoleVisibility()` only runs once at page load — that case computes a plain JS boolean after
the data arrives and toggles `.hidden` directly instead (see `activity.js`'s
`canManage`/`canJoinSelf`/`canAddOthers`). Detail pages read a login from the query string
(`/socio?login=<login>`, `new URLSearchParams(window.location.search)`), same pattern `login.html`
uses for the magic-link token from an emailed URL. A link to a person's page (`/socio`, open only to
admin and board) is drawn only for them: anywhere a member, `material` or `money` can see a person
(the participants of an activity, the holders of a numbered article), the name is plain text, since
the link would only take them back to the home page. Its login code has no URL of its own to arrive
in — `login.html`'s second panel gets it typed in by hand — so the email/phone from the first step
are instead remembered in a plain JS variable across that two-step flow. The page is the same on
every address except one thing: on a `local.…` address (the club's tablet is given `local.` + the
public address, and the API shortens the session there, `docs/architecture.md`, "Short sessions"),
`auth.js` applies the same rule to `location.hostname` and shows `#local-notice` above the panels —
"Sesión local", in the accent color, saying that the address is for shared use and the session will
close by itself in a few hours (no number, so it can't disagree with `LocalTokenTTL`). Everywhere
else it stays hidden. That second panel appears only when the API accepts the details; for a pair
that matches nobody who can sign in it answers 404 `unknown_details`, and the first panel says so
("Ese correo y ese teléfono no corresponden a nadie con acceso…") and asks to check them and, if
they are right, to contact the Junta Directiva. The panel says an email with a link and a code will
arrive. The link from that email is usually opened in another window or tab, which signs in there
while this one keeps asking: `auth.js`'s `watchForSignInElsewhere` therefore listens for the
`storage` event on the `token` key (and for the tab becoming visible again, for a browser that
doesn't deliver it) and, when a stored token really is valid (`GET /auth/me`, as at load), sends
this window to `/` already signed in — a stale or invalid one changes nothing. `login.html` also
offers Telegram as a second way in, alongside (never instead of) the email/phone form: on load,
`auth.js`'s `loadTelegramBotInfo` calls the unauthenticated `GET /auth/telegram-bot`
(`authapi.Handler.TelegramBotInfo`, backed by `config.TelegramBotUsername`/`TELEGRAM_BOT_USERNAME` —
independent of `TELEGRAM_BOT_TOKEN`/`TELEGRAM_LOG_CHAT_ID`, and not secret the way that token is,
since it's only the public "@name" a `t.me/<username>` link needs) and, only if a username comes
back non-empty, un-hides a note linking straight to the bot's chat — left hidden by default so
there's no dead link (or flash of one) when Telegram isn't configured at all, or the request itself
fails.

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

`/perfil` also has a "Mis sesiones" section (`initSessionsPanel` in `js/sessionsPanel.js`, which
`/socio` shares, see below): the person's sessions not expired or revoked (`GET
/users/{login}/tokens?status=active`), each with its browser and system, last use and address, when
it was opened and when it expires, "Esta sesión" on the one in use and, if an administrator entered
as them, who (and for an administrator impersonating the person the section shows the person's
sessions with a note, and no "Cerrar" buttons, since the API doesn't let sessions be ended through
an impersonation); its Último uso, Abierta and Caduca headers sort them (the same helper, here in
the page itself since they are all loaded; a session never used counts as the oldest use), most
recently used first to begin with; each has a "Cerrar" button (`PATCH .../tokens/{id}` with
`revoked: true`, after a confirmation), and closing the one in use is signing out, so it also clears
the token and goes to `/login`. `/socio` has the same panel as "Sesiones" for an admin only (the
section carries `data-roles="admin"`, as the API gives anybody's sessions to nobody else): the
sessions of the person whose page it is, with the same columns, sorting and "Cerrar" buttons (an
admin can end anybody's), worded for a third person, and on their own page, the admin's own.
`perfil.html`/`socio.html` each also render a "Licencias federativas" section — a year `<select>`,
mirroring `dashboard.js`'s own Actas asambleas, plus a grid of small clickable thumbnails rather
than a plain list of text links — instead of pointing at that person's `licencias/` subdirectory
(see [Dropping files
in](backend-internals.md#dropping-files-into-the-library-and-the-per-person-directories)) through
`userFiles.js`'s generic browser: `js/licenseFiles.js`'s `initLicenseFiles(login, opts)` fetches
that one directory's listing (`GET /users/{login}/files?path=licencias`), builds the `<select>` from
whatever years actually have a license on file (ascending, most recent selected by default — nothing
conjures up an empty year the way `loadActas` doesn't either), and renders each of that year's files
as a `licenseCard`: a thumbnail linking straight at `GET /users/{login}/files/file/licencias/<name>`
(a bare `<img>`/`<a href>`, the same cookie-authenticated plain-link shape `dashboard.js`'s own
`fileLink` already uses for Documentos/Actas/Memorias, since this route accepts the session cookie
too — see `internal/userfiles` above — so there's no need for `apiFetchBlob`'s Blob dance), with a
"DD-MM-YYYY — Tipo de licencia" label underneath parsed the same way `dashboard.js`'s own
`parseActaName`/`actaYear`/`formatActaDate` read `yyyy-mm-dd(-suffix).pdf` back out of an acta's
filename; within a year, files sort by `(month, day, tipo_de_licencia)` rather than trusting the raw
filename string, the same reasoning `loadActas`' own `renderSelectedYear` sorts by `(month, day,
suffix)`. A file whose name doesn't match that convention has no year to file it under, so — the
same as an acta named anything else — it's simply left out.

The thumbnail itself is a companion file at
`licencias/.thumbnails/<full original filename>.jpg` (any original extension — not just a
PDF's) — a dotfile-prefixed subdirectory, invisible to the plain "licencias" listing this
module reads (`userfiles.Service.ListDirectory` skips any entry, file or directory,
starting with "."), the same "invisible companion file, fetched by exact path" trick
(the library's own `.order.json` is a dotfile too, though the API reads that one itself).
Nothing in the running app generates
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
(`userfiles.Service.ListDirectory`), caught and shown as "no hay copias" rather than an error —
nobody's dropped a license copy in yet is the common case, not a real failure. `profile.js` passes
`hideWhenEmpty: true` (mirroring `initUserFilesBrowser`'s own option) so "Mi perfil" doesn't show an
empty section for the common case of no license on file yet; `member.js` leaves it on by default
(always shown) so admin/board reviewing someone else's page can tell "no license copies yet" apart
from a page that failed to load anything at all. `.info-card`/`.info-card label`/`.year-select` (the
bordered-card-with-a-year-filter look Actas asambleas already had) live in the shared `style.css`
rather than the page-specific `dashboard.css` precisely because these two pages need them too now
(what opens a card, a list or a paragraph, has no top margin of its own, since the card's padding is
already there: `.info-card > :first-child`); `.license-thumbnail-grid`/`.license-thumbnail-card`/
`.license-thumbnail`/`.license-thumbnail-placeholder`/`.license-thumbnail-label` are this section's
own, `style.css` as well since `dashboard.css` stays index.html-specific.

`/repartos`, `/reparto?id=…` and `/reparto-gestion?id=…` (`distributions.js`, `distribution.js` and
`distribution-manage.js` with its sections `distribution-orders.js`, `distribution-payments.js` and
`distribution-numbers.js`, sharing `js/distributionCommon.js`) are the interface for
**Distributions** above — where what the code and the API call a unit is a "pedido" to the people
reading these pages, and an extra unit an "extra", since "unidad" meant nothing to them — behind one
"Repartos" sidebar entry (`layout.js` maps the two detail pages to it in `ACTIVE_ALIASES`). The list
(columns: when it was created, as "Enero de 2026" with `formatMonthYear`, the name, the state and
the price; the state has the same four names as everywhere: "Borrador", "Se puede pedir", "Activo"
and "Terminado" for `draft`, `open`, `closed` and `done`, each with its tooltip; except that a row
with the "Puedes pedirlo" badge says "Activo" in the state column, so as not to repeat it) opens a
new distribution from a dialog (admin/board; the options go in a textarea, one per line, "Talla: S,
M, L", `parseOptions`) and shows "Puedes pedirlo" on the open ones whose `eligible` is true. The
detail page renders the conditions and, except while it is a draft (`#ask-sections` is hidden then,
and the orders are loaded only once it leaves the draft), a "Pedir" form (an "A nombre de" picker
over `GET /users/directory`, one `<select>` per option, an extra checkbox when `extra_price` exists
— except in a distribution for all members with no options, where the own order is already there and
only extras are left: no checkbox, the request always goes as an extra, the button says "Pedido
extra" (`extrasOnly`) and the subtitle gives the price, "Puedes pedir también extras a 8,00 €" —;
`/repartos`' list marks a distribution only some people can see with a "Restringido" badge, and the
page's description line says who ("lo ven: …"); the create and edit dialogs have a "Quién lo ve"
select (`visibility`) that carries `data-roles="admin"`, so only an administrator has it, and
`readDistributionForm` sends it only when it is there; `canOrderNow` repeats the API's own rule of
when (open and before the deadline; admin, board and material at any time but in a draft, with a
note saying that the distribution is not open) only to decide whether to show it) and "Tus pedidos"
— the units where the signed-in person is the beneficiary or entered them, since `GET .../units`
returns everything to admin/board/material — with who entered each when that isn't the person it is
for. In a distribution for all members the member's own unit is already in that table ("Automático",
"Sin elegir" with a primary "Elegir" button until somebody chooses, no "Quitar" since the API
refuses it), and the whole "Pedir" section (`#order-section`, not just the form, so not even a "no
longer admitting orders") is out when there is neither an option nor extras to ask for,
`nothingToAsk`. The table's columns (`unitColumns`) are only the ones that mean something: "Elegido"
only with options, "Tipo" (own or extra) only when extras exist, and nothing about payment for now —
the state is "Recogido" or "No recogido"; the price (the unit's own, frozen) is shown in a "Precio"
column, but nothing about paying (no state, no amount owed): the intranet doesn't keep that. Nothing
to manage the distribution is on this page: whoever does (admin, board and material) has one "⚙
Gestionar este reparto" button (`#manage-link`, `data-roles="admin,board,material"`, so nobody else
even has it) that goes to the management page.

`/reparto-gestion?id=…` (`distribution-manage.js`; `requireAnyRole` admin/board/material) is the one
page to manage a distribution. At the top, after the title, the state and the description, are the
buttons that act on the distribution itself: "Volver a …" / "Pasar a …" (one step at a time, except
that a distribution with nothing for the members to ask skips "Se puede pedir", `neighbours`:
`bodyFrom` rebuilds the whole body, as `PUT` replaces every field; going back to a draft, and
deleting, ask for confirmation naming what would really be lost: the orders that are not an empty
own order of a member in a distribution for all members, which is only a placeholder made again on
leaving the draft (`ordersWithSomethingToLose`), and say nothing about orders when there are none of
those), "Editar" (the dialog, with its form) — all for admin and board — "Venta directa" (admin and
board, only in a distribution with articles that is not a draft: `distribution-sale.js`, below) and "Borrar" (admin only:
`DELETE` is admin-only in the API, and the button is `data-roles="admin"`); a link "← Volver al
reparto" goes back to the member's view. Under them, a row of links (`.section-index`) to its
sections, which are what used to be three pages: **Pedidos**, **Pagos** (admin/board, and only when
something has a price) and **Números** (only when some article has its numbers described; its link
in the index says "Números"). All their headings are `<h2>`, and a section has none of its own where
it would only repeat the link: the orders have "Resumen de lo pedido" and "Pedidos de los socios",
and the numbering has one block per article titled "Números - <article>". The page hides the section
and its link otherwise (`renderHeader`), also after an edit that changes it. Every control
restricted by `data-roles` is *removed* from the page for anybody else, so each script reaches those
through a null-safe lookup (a bare `getElementById(...).addEventListener` on one of them would throw
before the rest of the page ran) and the payments section is only started for admin/board. The page
reads the distribution once and gives it to the sections, each of which is a module with an `init…`
function that returns `reload`: changing the state or editing re-reads them all
(`distributionChanged`), and what is done in the orders (paying, handing out, numbering) reloads the
payments and the numbers (`onOrdersChanged`); the orders section also tells the page when it read a
fresher distribution (what is left of each article changes with the orders), so the header is drawn
again. The **Pedidos** section is everything about the orders in one place: on top `GET .../order`
(what has to be bought), below it a list with one line per order (searchable by member or number;
the ones with everything paid and handed out are hidden unless asked for) and a summary: what, how
many are handed out and paid, what it costs. clicking the line (or Enter or Space on it,
`toggleOrder`; the line is `tabindex="0"` with `aria-expanded`, styled like a line of the ledger of
`/cuenta`: `.order-row`) unfolds its detail (`detail`, kept in `unfolded`, in a row of its own),
with no button for it: for an article with numbers (`isNumbered`) how many are without one (with
"Asignar los siguientes libres" and, for an article whose numbers are described, "Elegir números…",
which opens `numbers-dialog`: a button for each free number, in a row by the first part of the
number when it has several (a series and its fractions), pressed to mark and unmark, with Shift to
mark from the last one pressed to this one, a filter box, and never more marked than the order is
missing — the rest are disabled — and the free numbers read again if the API refuses one because
somebody took it meanwhile (all of it is `createNumberPicker` in `numberPicker.js`, which the sale
over the counter uses too); for an article with free-text numbers, a field to type them) and the
list of its numbered items, each with its paid / delivered state, to tick (`ticked`; "todos / sin
pagar / sin entregar…" tick by state, `pick`) and act on — mark paid or take the payment off
(admin/board), hand out to the person asked in a dialog (`deliver-dialog`, "Entregado a", prefilled
with the member the order is for; only members are offered), undo a delivery, return, take the
numbers off. These actions are a line "Con lo marcado" (`markedActions`) that is there only while
something is ticked and has only the buttons that would do something to what is ticked
(`applicable`: "Marcar pagados" if some ticked number is unpaid, "Quitar pago" if some is paid,
"Entregar" if some is not handed out, "Deshacer entrega" if some is, "Devolver" if some is handed
out and unpaid, "Quitar números" if some is neither); each acts only on the ticked numbers it
applies to, and the line is drawn again on each tick. The line "Pedido:" has "Pagar todo (importe)"
and, while something can be handed out (`deliverable`), "Entregar todo", which asks in the same
dialog who collects it. Elsewhere a count field (`quantityOf`) does the same as the ticks. Paying
and taking a payment off first ask, in `pay-dialog`, for the asset account (`withAccount`: the
non-hidden `asset` ones of `GET /accounts`, the last chosen remembered in `localStorage`; skipped
when the order costs nothing) and send it as `account_id`. The create/edit dialogs of a distribution
have a "Cuenta de los pagos" select (`loadAccountOptions`; empty is the default account, named in
its first option). Changing or removing an order (`canEditOrder`) is offered at any time but in a
draft, as the API allows. Everything goes through the single `perform(act, unit)`, confirming before
handing over unpaid units, undoing, returning or taking numbers off. Wherever the frontend lists
numbers they are sorted (`sortIdentifiers`: part by part, digits as numbers) and joined into ranges
(`formatIdentifiers`; `compactNumbers` for a bare list such as the numbers an error message or an
entry of the list of payments names). The Telegram messages about numbers say them the same way
(`formatNumbers`/`compareNumbers` in `numbering.go`, a Go copy of those two that must keep giving
the same order and ranges; each message says what it changed — numbers added, taken off or newly
assigned — not what was typed). What is unfolded, ticked and typed survives redrawing. Both pickers
("A nombre de", "Entregado a") offer only members, never everybody on file: `loadPeople(id,
purpose)` takes the entries from `GET /distributions/{id}/people` — `for=order`, the members on the
cutoff date (today's when there is none); `for=pickup`, also today's members — and only the names
shown for who did what come from `/users/directory`. The API enforces the same (`not_eligible`,
`invalid_collector`), the pickers just don't offer what would be refused. `api.js`'s `apiFetch` puts
the API's error `code` on the thrown `Error`, and sets its message to the Spanish text for that code
from `js/errorTexts.js` (the one catalog of every code the API can answer with; a function for the
few whose message carries something to show, such as the names of the accounts a budget is missing,
or the two totals that do not match), falling back to the API's own text for a code it does not
know. `apiFetchForm` and `apiFetchBlob` do the same, and a request that never reaches the server
says so in Spanish instead of the browser's "Failed to fetch". A page that wants something more
specific for a code still reads `err.code` (`distributionCommon.js`'s `apiErrorText`,
`accountTree.js`'s `accountingErrorText`). A new error code in the API needs its entry in that
catalog.

`/contabilidad` (`accounting.js`) manages the chart of accounts from `GET`/`POST`/`PUT /accounts`
and `POST /accounts/{id}/move` — a tree, indented by depth, of every account's name, direct balance,
and last movement date (each name a plain `<a href="/cuenta?id=…">` into that one account's own
detail page, below). There is deliberately no code column, nor codes in the parent pickers: a code
only shows on that account's own `/cuenta` page, though the create/edit dialogs still have the field
to set it. The page also has the controls to create an account ("+ Nueva cuenta" dialog), correct
one's name/code/parent (one shared edit dialog, `openEditAccountDialog`), and swap it up or down
among its own siblings. Both dialogs carry three checkboxes, "Para cuotas anuales", "Para licencias"
and "Oculta" (`allows_dues`/`allows_licenses`/`hidden`, styled by `.checkbox-label` in `style.css`
so the box sits next to its text instead of being stretched by `.field input`'s `width: 100%`),
always sent explicitly — unchecked is `false`, not omitted, since `PUT` replaces every field — and
each row shows a small "Cuotas"/"Licencias"/"Oculta" badge next to the account's name for whichever
are on, so which accounts a transaction can record those against is visible without opening each
one. `js/accountTree.js` holds the `childrenByParent`/`flattenAccountTree` pair both this page and
`account.js` need: `GET /accounts` already comes back as a pre-order, depth-first walk (see the
`accounts` bullet under [Accounting](backend-internals.md#accounting)), so filtering that flat list
down to a single `parent_id`, in the order it already arrives in, *is* that group's own sibling
order — one pass groups every `parent_id` at once, no separate tree-building pass. Hidden accounts
(`hidden`) are left out of the table, and of the parent pickers, unless a "Mostrar cuentas ocultas
(N)" checkbox — shown only when there are any — is on (a view choice, never persisted); each is
tagged "Oculta" and dimmed when shown; `shownAccounts` (`accountTree.js`) drops them while
re-parenting their shown children onto the nearest shown ancestor, since filtering the flat list
alone would lose those children from the tree walk. The ↑/↓ arrows follow the real sibling groups
but count only shown siblings, and a move repeats once per hidden sibling it has to pass (the
endpoint swaps with the very next one, which would otherwise look like nothing happened). A hidden
account stays offered as the parent picker's choice when it is the current parent — dropping it
would move the account to the top level on save. A `move`'s own response (the whole reordered list)
re-renders directly from `applyAccountsList` rather than triggering a follow-up `GET`. Only admin,
board and the optional "money" role ever see `/contabilidad` in the sidebar at all, or can even land
on the page (`requireAnyRole`, else redirected to `/`) — tighter than the API itself, which opens
*reading* the chart to every baseline role the same as `GET /transactions`. Of those, only admin and
money write (`canWrite`, `hasAnyRole(user, ['admin', 'money'])`): board reads everything, but the
page leaves out "+ Nueva cuenta" and each row's ↑ ↓ and "Editar" for it (the API is the authority
and would refuse them anyway).

**Files attached to transactions.** On `/cuenta` a transaction that has files shows a 📎 (with the
number when there are several) after its description (`file_count` of the ledger listing); opening a
row also lists them, as links to `/api/v1/transactions/{id}/files/{fileId}/file` (the session cookie
makes a plain link work). The new-transaction dialog has a multiple file input, and the edit one
lists the transaction's files, each with a "Quitar" that deletes it at once (after a confirmation),
and the same input; the files chosen are uploaded one by one once the transaction is saved
(`uploadTransactionFiles` in `js/transactionFiles.js`, which the pages share), and one that fails is
reported with the rest of what could not be registered. Deleting a transaction that still has files
says so (`transaction_has_files`). `/justificantes` (`transactionFilesPage.js`, for admin, board and
money, reached with the "Justificantes" button in the header of `/contabilidad`, not from the
sidebar, where it counts as part of Contabilidad for the highlighting) is the overview: every file
from `GET /transaction-files` with its transaction's date, description, amount and accounts (each
with its sign, linking to the account's ledger), a year filter and "Ver más".

`/presupuestos` (`budgets.js`) is the yearly budget, in the sidebar for every baseline role. A year
selector (the years that have a budget from `GET /budgets`, plus this year and the next; "Otro año"
(admin and money only) opens any year from 1900 to 2200, to look at it or to create its budget)
loads `GET /budgets/{year}` and draws, for income and for expenses, a table with each line item's
name and accounts, the amount budgeted, what was really received or spent, and the percentage
reached (a bar and the figure, red beyond 100 %), with the totals of both parts also as two cards at
the top; accounts the saved budget does not cover are named in a notice. A year without a budget
says so. Only admin and money (the API is the authority) get the buttons: "Editar presupuesto" and
"Borrar presupuesto" (`DELETE`, after a confirmation), and, for a year without one, "Crear
presupuesto", which opens the same dialog starting from the closest earlier year's items (or the
later one's). The dialog keeps a working copy of both parts: each item has a name, an amount and its
accounts as removable chips, and a picker that adds one account (leaves of the part's type not yet
used in the budget) or "Todas las de «group»"; it shows both sums, the difference when they do not
match, and the accounts still without an item as it is edited, and saves everything with one `PUT
/budgets/{year}`, whose error messages (`accounts_missing`, `unbalanced`...) come from the API in a
modal.

`/cuenta?id=…` (`account.js`) is one account's own ledger (for a group, `isGroupView`: its
descendants' entries with a "Cuenta" column between Fecha and Descripción linking to each one's
page, where the transaction is corrected — no Editar, no new transactions, a header balance that,
like in `/contabilidad`, is the sum of all its descendants' (it comes rolled up from `GET
/accounts`) — and a row's detail lists the other accounts relative to the account the row is on) —
reached only by clicking an account's name on `/contabilidad`, never linked from the sidebar itself,
so it shares that same admin/board/money gate, and board gets no "+ Nueva transacción", "Editar" or
"Borrar" there (it can open a row's detail and the files attached) (and `js/layout.js`'s
`ACTIVE_ALIASES` maps it back to `/contabilidad` for the sidebar's own highlighting, the same
`/actividad`→`/actividades`/ `/socio`→`/socios` treatment every other detail-off-a-listing page
gets). Renders `GET /accounts/{id}/transactions` (see [Accounting](backend-internals.md#accounting))
as a table — date, description, this account's own entry amount, its running balance after, and
per-row Editar/Borrar (a row also opens on a click anywhere but those buttons, or Enter/Space when
focused — see below). The listing is paginated (`loadEntries`): the newest 50 (`ENTRIES_PAGE_SIZE`)
first, "Ver más" for the next ones, and above it a "Buscar en la descripción" box with a Desde/Hasta
filter (the `limit`/`offset`/`from`/`to`/`q` query parameters of that route, applied with "Filtrar";
it asks for one entry more than a page just to know whether to show "Ver más"). A "+ Nueva
transacción" dialog, deliberately narrower than the full `POST /transactions` shape, creates
transactions: a date, a description, and a growing list of *lines* (`setupEntryLines`), each an
amount "en esta cuenta" plus the "otra cuenta" it comes from or goes to. As soon as the last line
has both fields a new empty one appears below it; a line with neither is ignored, one with only one
is an error, the same account can't be on two lines, and the account picker starts on a "— Elige una
cuenta —" placeholder precisely so "filled" means something. On save (`entriesBody`, shared by
creating a new transaction and correcting an existing one) this account gets one entry for the *sum*
of every line's amount, added up in whole cents so floating point never shows up in the result, and
each other account gets the exact negative of its own line — so the entries sum to zero without
whoever is typing having to think about the balancing side. Amounts that cancel out (this account
would get a zero entry) are refused. A read-only "Total en esta cuenta" next to the date
(`updateTotal`) shows that sum live, on every keystroke, so it counts a line whose account isn't
picked yet and ignores an empty or non-numeric amount. A row's own "Editar" fetches that one
transaction fresh (`GET /transactions/{id}`, never kept in memory alongside the ledger listing,
which only ever carries this account's own side of each one) and turns each of its *other* entries
into a line (amount negated back); if it doesn't have exactly one entry on this account, or has no
other entry (only possible by calling the API directly), editing is refused with an explanation
instead of silently merging them. The dues/licenses sections follow every account picked on any line
(`sectionsFor`), not just one. "Borrar" (`confirm`-gated, the same pattern `member.js`'s own delete
buttons use) removes the whole transaction, every entry included. Clicking a ledger row
(`tr.entry-row`, `toggleEntryDetail`) expands a detail row right below it — starting under
"Descripción" (an empty first cell), with no line between the two and the same background, so it
reads as the continuation of the row above rather than as another row — listing the transaction's
*other* accounts — each name (just the name, no code — the code only appears in the page's own
header) linking to that account's own ledger, with the amount it gained (`+`) or lost (`−`;
`signedCurrency`) — fetched with `GET /transactions/{id}` the first time (the listing only ever
carries this account's own side, see `AccountEntry`) and resolved to names from `allAccounts`, which
the page already has; it works for transactions of any number of entries. Under them it also lists
the cuotas anuales and licencias the transaction paid for — each as the person's name (from `GET
/users/directory`, which the page loads for everyone who can reach it, not just for the admin
pickers) linking to `/socio`, plus year (and license type) — from the `dues`/`licenses` that same
response carries for admin/board only, so they simply don't appear for anyone else. Clicking the row
again collapses it, a click on Editar/Borrar or on a link never toggles it, and any reload of the
list starts every row collapsed. Pressing "n" anywhere on the page opens the new-transaction dialog
(ignored while typing in a field, with a modifier held, or with a dialog already open), which puts
focus on the date; the edit dialog puts it on the description. The date input only ever collects a
plain date, but `occurred_at` is a real timestamp — sent as that date at noon UTC (not midnight)
specifically so `formatDate`'s own local-timezone getters can never roll it back to the previous day
for anyone viewing from behind UTC; editing an existing transaction collapses whatever time of day
it actually had down to this same convention. Both dialogs also carry, for a real admin only
(`data-roles="admin"`; POST dues/licenses is admin-only), a "Cuotas anuales a registrar" and a
"Licencias nuevas a registrar" section (`setupDuesAndLicensesSection`, prefix `nt`/`et`), each a
list of pending chips sent as `POST /users/{login}/dues`/`licenses` once the transaction itself is
saved. Each section is hidden (`hidden`) until it could actually succeed: `sectionsFor` shows dues
if this account or any account picked on a line has `allows_dues`, licenses if any has
`allows_licenses`, re-evaluated whenever a line's picker changes and whenever a dialog opens, and a
section that gets hidden also drops its pending chips — otherwise one added while a qualifying
account was selected would still be sent, and only rejected by the API after the transaction was
already saved. Separately from that, the edit dialog lists what earlier saves already registered
against the transaction, read-only (no ×: nothing removes a due) from the `dues`/`licenses` its own
`GET /transactions/{id}` already returns (`setExisting`): a section stays visible for as long as it
has something to list even when no account qualifies any more (the flag was since turned off, or the
accounts changed) — a record of what was paid, not something about to be sent — while its add
controls follow the qualifying rule above. When neither section applies, a hint explains that an
account has to be marked for it, in Contabilidad, instead of showing nothing. The "+ Nueva
transacción" dialog opens with today's date (`todayAsInputValue`, built from local getters:
`toISOString()` would be UTC's date, already tomorrow's for part of the evening east of it), and
each section's "Año" input starts from the transaction's date — that date's own year, or the next
one in December, since a payment that late is almost always for the following year
(`suggestedYear`). The inputs keep following the date until the person types in one, which is then
left alone until the dialog is next opened (`editedYearInputs`, cleared by `reset`/`syncYears`);
adding a chip puts that input back on the suggestion, and the edit dialog does the same from its own
date. Each line's "otra cuenta" picker (the same list in the create and edit dialogs) leaves hidden
accounts out — except those the transaction being edited already uses, which stay choosable
(`otherAccountOptions`'s `keepHidden`) — and lists every other account by name only, no code
(indented the same way `accounting.js`'s own pickers are, via `flattenAccountTree`) — excluding only
this account itself, not its ordinary parent/children the way `accounting.js`'s own "cuenta
superior" picker excludes descendants: there's no circular relationship to guard against here, any
two distinct accounts can be on the two sides of a transaction. The page's own header (name, code,
current balance) and that picker both come from one `GET /accounts` call — that listing already
carries every account's own balance, so there's no need for a second, single-account fetch just for
the header (`GET /accounts/{id}` alone never populates it — see
[Accounting](backend-internals.md#accounting)).

`/socios` (`members.js`) is a read-only, sortable table of people; every action about a person lives
on their own `/socio?login=<login>` page, which a row opens for admin/board only. "+ Nueva persona"
is admin-only, in the markup and in `members.js`, which loads the role catalog (for that dialog's
role checkboxes) only for an admin. The list of roles itself, to create or delete one, is no longer
here: it lives on `/admin`, the page behind the "Admin" sidebar entry (admin only, `NAV_ITEMS`'
`roles: ['admin']`), meant for whatever else only administrators care about —
`admin.html`/`admin.js`. It starts with a row of links to its sections (`.section-index`, each one
an anchor to its `<h2 id="section-…">`: add a section, add its link), and for now holds an "Estado
del sistema" section — a block of at most 960px, each name in a column of its own with the value
next to it and a faint line under each row, since the names and values are short (`GET
/admin/status`, see **Admin status**: on top, what is wrong right now (`problems`, in a red box) or
"No se ha detectado ningún problema"; then version and commit, database and migration, email,
Telegram, timeouts, directories and, in "Espacio en disco", how full the disks that hold them are,
with an "Actualizar" button), a "Roles" section (`GET`/`POST`/`DELETE /roles`; a "Ver" under each
role unfolds who holds it, taken from `GET /users` — admin sees everybody with their `roles` — with
a "×" per person (`DELETE /users/{login}/roles/{id}`) and a name picker with "Dar este rol" (`POST
/users/{login}/roles`); the automatic roles only list their holders, and the admin's own admin role
has no "×", as on `/socio`; the unfolded roles survive the re-render in `expandedRoles`) and a
"Sesiones" one: every session from `GET /tokens` (a person's name from the directory, last use,
address, browser and system ("Chrome 118 · Windows", from `browser`/`os`, with the whole User-Agent
as the tooltip; "—" if never used), creation and expiry, "Activa"/"Caducada"/"Revocada", a badge on
an impersonation's and on the admin's own "Esta sesión"), 50 at a time with "Ver más" and a checkbox
for the expired and revoked ones, where an active one can be revoked (a different warning for one's
own, which signs out) or have its expiry changed, through `/users/{login}/tokens/{id}`; its headers
Persona, Último uso, Creada and Caduca sort it (`initSortableHeaders`, `js/sortableHeaders.js`: a
click or Enter on the header picks the column or turns it round, ▲/▼ and `aria-sort` say which; the
most recent first for the dates and A first for the person to begin with, last use to begin with the
page) by asking the API (`sort`, `order`), so that the order holds across the pages "Ver más"
brings, and a "Festivos" one: the club holidays the activities calendar tints, a year at a time with
‹ › (`GET /holidays?year=`), each with a "Borrar" (`DELETE /holidays/{id}`), a "+ Nuevo festivo"
dialog (`POST /holidays`; a date that already has one is the API's 409 `duplicate_holiday`) and
"Copiar del año anterior", which POSTs last year's holidays on the same day and month except where
the year already has one (movable ones such as Semana Santa are fixed by hand; there is no edit) and
a "Datos de interés" one: the labelled values the home page shows (`GET`/`POST /club-info`,
`PUT`/`DELETE /club-info/{id}`, `PUT /club-info/order`), in a table with "Editar" and "Borrar", ↑
↓ to put them in order (each sends the whole new order) and a "+ Nuevo dato" dialog shared by
creating and editing (a label, a value and, optionally, a text for a copy button and the button's
own text) and a "Tipos de material" one (`GET`/`POST /material/types`, `PATCH`/`DELETE
/material/types/{id}`: a table with "Renombrar" and "Borrar" and a dialog shared by creating and
renaming; "Cuerda" and "Saca" are listed as "Fijo" with no buttons, and a type with material is
refused with its own message). The home page (`dashboard.js`, `loadClubInfo`) draws them from `GET
/club-info`, with the copy buttons handled by one listener on the list since they are drawn after
load: the data is not written in `index.html`, so an administrator changes it without a deploy and a
private one (the Bizum number) is not in the repository. Admin/board get `FULL_COLUMNS` and
`FULL_FILTERS`; a plain member gets `MEMBER_COLUMNS`/`MEMBER_FILTERS`, limited to what
`users.MemberListing` carries (see **Permission tiers**). Row filters ("Género", "Tipo de socio",
"Categoría", "Cuota anual", "Federación") and the column toggles sit behind a "Filtros…" button, and
above it a "Buscar persona" box (`matchesSearch`: every word typed has to appear, in any order and
ignoring accents and case, in the first name and surnames and — admin/board only, as that is all
their listing carries — the phone and DNI; not the membership number, login or email. The
placeholder says "Nombre o apellidos" (for admin/board, "Nombre, apellidos, teléfono o DNI"); it
narrows what the filters leave and isn't persisted). Every filter starts with every option checked
except "Tipo de socio", which starts with only "Activo" and "Baja temporal", so the default view is
the current membership. "Cuota anual" splits people on whether `last_due_year` is the current year
("A corriente de pago"), and "Federación" on whether `license_type` is present — `GET /users` only
reports a license for the current year, so having one *is* being federated. Filters, visible columns
and sort order persist in `localStorage` (`katiuskas:members-view`, best-effort), so "Volver a
socios" from a person's page lands on the same view, and a counter shows how many people the current
filters leave. With nothing saved, the columns shown are "N.º socio", "Nombre", "Apellidos",
"Categoría" and "Tipo de licencia" (each column's `default` flag); the rest are optional in the
"Filtros…" panel.

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

**The history of the Junta Directiva (`/junta`).** `board-history.js` asks `GET
/board-members/history` and turns each term into up to two events: "<name> entra como <cargo>" on
its start date and "Último día de <name> como <cargo>" on its `end_date`. They are sorted newest
first (on the same day an entry comes before a last day), grouped under one heading per year. Open to
any baseline role; names link to `/socio?login=…` only for admin and board. The home page links to
it under the "Junta directiva" section, and the sidebar keeps "Inicio" highlighted
(`ACTIVE_ALIASES`).

**The sale over the counter (`distribution-sale.js`).** For a member who asks for something and pays on
the spot. "Venta directa", in the buttons of `/reparto-gestion` (admin and board; only in a
distribution with articles that is not a draft: `update(distribution)` shows and hides it), opens
`sale-dialog`: the member (a searchable field with who may have the distribution, `loadPeople(id,
'order')`), the article (not shown when there is only one) and the quantity, with the total and what
is left; for an article whose numbers are described, the buttons of `createNumberPicker` for the
numbers the member asks for by name — the rest are given in order — and, for free-text numbers, a
field to type them; for an article that costs something, "Cobrar ahora" with the account (the one
used last, shared with the payment dialog of the orders) and, always, "Entregar ahora". Both are
ticked by default, so the usual sale is: member, "Aceptar". It sends a single `POST .../sales`, shows
the numbers given in the confirmation and has the page read its sections again (`onSold`).

## Serving the frontend, and checking a page in a headless browser

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
its site block (`docs/deployment.md`).

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
signed in: mint a session token with `scripts/get-token.sh <login>` (dev only), navigate
to the app's own origin first, run `Runtime.evaluate` with
`localStorage.setItem('token', '<token>')` — the same key `js/api.js` itself reads on
every page load — then `Page.navigate` to the real page and, once
`Page.loadEventFired` fires and a couple of seconds have passed for that page's own
client-side fetches to resolve, `Page.captureScreenshot` (`captureBeyondViewport: true`
to get the whole page, not just the current viewport).
