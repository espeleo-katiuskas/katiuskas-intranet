# Frontend

How the web frontend (`web/`) is built and how each page works. The conventions every page shares
come first, then the pages one by one. The endpoints a page calls are in [`api.md`](api.md); the
rules behind them, in [`backend-internals.md`](backend-internals.md) and
[`architecture.md`](architecture.md).

Contents:

- [Conventions of every page](#conventions-of-every-page)
- [Material page (`/material`)](#material-page-material)
- [Library pages](#library-pages)
- [Admin page (`/admin`)](#admin-page-admin)
- [Free text with a little formatting](#free-text-with-a-little-formatting)
- [Places pages](#places-pages)
- [Zone picker ("Where it is")](#zone-picker-where-it-is)
- [Finding a point and working without a connection](#finding-a-point-and-working-without-a-connection)
- [Systems (`/sistemas`, `/sistema?id=…`)](#systems-sistemas-sistemaid)
- [Place maps and OpenStreetMap](#place-maps-and-openstreetmap)
- [Tables, links and sign-in](#tables-links-and-sign-in)
- [Pickers and what they offer](#pickers-and-what-they-offer)
- [Activity pages (`/actividad`, `/actividades`, `/lugar`)](#activity-pages-actividad-actividades-lugar)
- [Sessions and returning to the requested page](#sessions-and-returning-to-the-requested-page)
- [Dialogs and errors](#dialogs-and-errors)
- [Pages that do not exist, and inline forms](#pages-that-do-not-exist-and-inline-forms)
- [Impersonation and role override](#impersonation-and-role-override)
- [The shared client and the login page](#the-shared-client-and-the-login-page)
- [Archive and file browsers](#archive-and-file-browsers)
- [Person pages: sessions and history](#person-pages-sessions-and-history)
- [License copies on person pages](#license-copies-on-person-pages)
- [Distribution pages: the members' view](#distribution-pages-the-members-view)
- [Distribution pages: the management page](#distribution-pages-the-management-page)
- [API errors in the frontend](#api-errors-in-the-frontend)
- [Accounting page (`/contabilidad`)](#accounting-page-contabilidad)
- [Accounting pages](#accounting-pages)
- [Members and admin pages](#members-and-admin-pages)
- [Loading data in parallel](#loading-data-in-parallel)
- [Other pages](#other-pages)
- [Serving the frontend, and checking a page in a headless browser](#serving-the-frontend-and-checking-a-page-in-a-headless-browser)

## Conventions of every page

### Frontend (`web/`)

Classic multi-page site (no SPA, no build step), with one `<page>.html` + `js/<page>.js` per view,
Caddy's `try_files {path} {path}.html` making `/socios` resolve to `socios.html`. Every file name
in `web/` is kebab-case, scripts included (`js/library-item.js`).

This is a deliberate choice, not just an unexamined default: every page already revalidates its own
session and re-fetches its own data on load, so there's no cross-page state an SPA would actually
save, and this stays free of a bundler or any client-side router.

#### Sidebar, topbar and `initPage()`

The sidebar and topbar, identical on every authenticated page, are rendered once from
`js/layout.js`'s `initPage()` rather than copy-pasted into each `<page>.html` — every one of them
reduces to an empty `<aside id="sidebar">`/ `<header id="topbar">` pair for it to fill in.

`initPage()` is also each page's own session bootstrap (in place of what used to be five
near-identical lines re-typed at the top of every `<page>.js`):

- it validates the session;
- it applies role-based visibility;
- it wires the sidebar toggle, topbar greeting, and logout button (which asks "¿Cerrar la sesión?"
  first, and says that with no connection there is no way back in);
- it returns the signed-in user same as `currentUser()` would.

`.topbar-user` (the greeting and the two buttons) wraps and may shrink to the width of the bar, so a
very long name goes onto more lines instead of making the whole page wider than a phone screen.

Its own `requireAnyRole` option additionally redirects home if the signed-in user holds none of the
listed roles. These are the whole-page gates of `member.js` (admin/board), of
`accounting.js`/`account.js` (admin/board/money) and of `admin.js` (admin), tighter than the
ordinary baseline check every API route already enforces.

Which sidebar link reads as "active" is computed from `location.pathname` (`ACTIVE_ALIASES` mapping
a detail page like `/actividad` or `/socio` to the listing it was reached from,
`/actividades`/`/socios`) rather than a per-page `class="active"` written by hand — one less thing
a new page could forget.

`login.html` is the one page with no shell and no session yet, and never calls `initPage()`.

#### Collapsing the menu

The menu collapses below 800px (an overlay behind the hamburger) and, on a wide screen, can be
hidden with the "×" at the top of it:

- `api.js`'s `initSidebarToggle` puts `.sidebar-hidden` on `.shell` — which looks like the narrow
  layout, `style.css` keeps the two rules alike;
- it remembers the state in `localStorage` (`katiuskas:sidebar-hidden`, applied as `api.js` loads
  so it doesn't flash);
- there the hamburger brings the normal layout back instead of overlaying the menu (`NARROW_SCREEN`
  there has to match the media query).

#### Widths and spacing

- The content is at most 1400px wide (`.content`), the full width up to there so that tables use
  it; running text keeps 75 characters and the panels of filters and forms 960px, whatever the
  width.
- The gap under the topbar is the padding of `.content` (20px) and nothing else: the first element
  of a page has no margin above it.
- A page of the first level has no link back above its title, so it gets `--first-title-offset`
  (36px) to put the title at the height of the one of a page below it, which has the link; the
  home page, which has columns and no title, is left as it is.
- When the title of a page below is the header of a section (Sistemas, la Junta, a ledger), it is
  as far from the link as a plain title is (17px, not the 32px of a header in the middle of a
  page).

#### The 404 page

`404.html` (`js/notfound.js`) is what Caddy serves, with a 404 status and without changing the
address, for anything that isn't a page (`handle_errors` in `deploy/Caddyfile`, which is why `root`
is declared at the site level there — an error handler can't see one set inside the `handle`; API
responses are untouched).

It can't assume who is looking:

- with a session token it becomes an ordinary page (`initPage()`, menu included), imported only
  then because `api.js` reads the token as soon as it loads;
- without one it shows a plain panel linking to `/login`.

Everything it loads is by absolute path, since it is served at whatever address was asked for,
however deep — which is also why `layout.js`'s topbar logo is `/images/…` and not relative.

## Material page (`/material`)

### Managing material

`/material` is for looking at the material and its movements: each row only shows "Movimientos" and
"+ Movimiento". What is needed now and then — "+ Nuevo material" in the header, and "Editar" and
"Duplicar" on each row (the class `manage-only`) — appears when "Gestionar material" (admin, board
and material, like the buttons it shows) is pressed, and its text becomes "Terminar de gestionar"
(`aria-pressed` says which).

- The page always opens with them hidden.
- The state is the class `managing` of `main.content`, not of the rows, so it stays when the table
  is drawn again after a save, and a hidden button cannot be reached with the keyboard.
- The table is `stack-on-phone`, so on a phone each item is a card and its buttons are not off to
  the right.

### Duplicating material

Next to "Editar", each row of `/material` has "Duplicar" (when managing, see above), for creating
something similar to what already exists.

It opens the same dialog as "Nuevo material" (`openDuplicateItemDialog` in `material.js`, which
fills the form with `fillItemForm`, as an edit does) titled "Duplicar material", with a note saying
which item it starts from. Everything in it can be changed before "Crear material", which is the
ordinary `POST /material/items`: nothing about it is in the API.

What is copied:

- the type, brand, model, colour;
- the data of a rope or a bag;
- the attributes and the description;
- the prices and "Se puede prestar".

What is not copied:

- The **code is not copied**, because it is unique among all the material (and a rope or a bag must
  have one): it is left blank, and for a rope or a bag the cursor goes there.
- The quantity and the date of acquisition are not the item's data either and start as in any new
  item.

Other behaviour:

- Changing the type in the dialog drops the attributes of the previous one and keeps the rest.
- Closing the dialog without saving forgets the copy, so the next "+ Nuevo material" is empty.

### Editing material

On `/material` ("Editar" in each row when managing, for admin, board and material) the dialog of
"Nuevo material" is reused to edit an item (`material.js`, `openEditItemDialog`):

- the title and the button change;
- the type is fixed for a rope or a bag (the field is disabled) and, for general material, can be
  changed to another general type (Cuerda and Saca are not offered);
- the quantity and the date of acquisition are left out (the stock is what its movements say);
- the form is filled with the item;
- it sends `PUT /material/items/{id}`, which replaces every field (the attributes included).

#### The type select and the fields shown

The "Tipo" select lists every type from `GET /material/types` (in the order an admin has set); the
fields shown depend on the *kind* of the chosen type (rope fields for Cuerda, bag fields for Saca,
description and quantity for any other), each option carrying it in `data-kind`.

The fields come in this order:

1. the type;
2. then the type of rope, or of bag, or the quantity (for general material, on creation only);
3. brand, model and code;
4. for a rope the year of manufacture, the diameter and the length, for a bag the capacity;
5. the colour (ropes and bags);
6. for every type the date of acquisition (on creation only), the rental price, the sale price and
   "Se puede prestar";
7. the attributes of the type;
8. and last the description (the "Observaciones" of a rope or a bag).

Brand, model and code are in the form for every type, required only for ropes and bags (the model
of a bag is optional); the model field stays disabled (and empty) while there is no brand.

#### Attributes and description

The attributes of the chosen type (`attributes` in `GET /material/types`) are drawn by
`syncTypeFields` before the description, one field each: a text box, or a list of the attribute's
options (`attribute_<id>` is the name of the field), with "(opcional)" on the optional ones and
`required` on the others. They are drawn again whenever the type changes, so the values typed for
another type's attributes are dropped.

The same function says whether the description is required (`description_required` of the type;
"(opcional)" is shown next to its label otherwise). Only the attributes with a value go in the
request.

### Which material the table lists

The table of `/material` hides the items with no unit left, those that show "0 / 0" (`isGone` in
`material.js`: sold, lost, abandoned or retired). They are no longer the club's and will not come
back unless more of them is acquired, which makes them show again. A partial loss or sale leaves
units, so the item stays.

The checkbox "Mostrar también el material que ya no está (vendido, perdido, abandonado o retirado)"
under the search box lists everything. It is a filter of the page (`GET /material/items` still
returns every item), and the search applies on top of it.

### Searching the material

The search box of `/material` looks in what the table shows (`itemSearchText`: the type and the
"Características" cell, code included), ignoring accents and case.

A word of the form `>N`, `>Nm`, `<N` or `<Nm` (N a natural number) is not looked for as text: it
keeps the ropes longer (`>`) or shorter (`<`) than N metres and drops everything that is not a rope
(`parseSearch`, `matchesSearch` in `material.js`).

- The comparison is strict, so `>30` leaves out a rope of exactly 30 m.
- Several such words add up (`>25m <40`).
- The rest of the words are looked for as text as usual (`>30 petzl`).
- The box's tooltip explains it.

### Movement types on `/material`

"+ Movimiento" only offers what can be done with that item right now (`movementTypeOptionsFor` in
`material.js`; the API would refuse the rest):

- *Alquiler* if it has a rental price and a unit available;
- *Venta* if it has a sale price and a unit available;
- *Adquisición* only for general material (ropes and bags are a single unit);
- *Pérdida*, *Abandono* and *Retirada* if a unit is available;
- *Devolución* only if some of it is out on loan (a rental is closed with the "Devolver" button
  instead).

An item with nothing to offer has no "+ Movimiento" button.

#### Accounting account for a sale or rental

A *Venta* or *Alquiler* that costs something is recorded in accounting, so the dialog also asks for
"Cuenta en la que se ingresa" (`#mv-account`):

- it lists the asset accounts that are not hidden and group no others, read once from
  `GET /accounts` when the dialog is first opened, with the last one used remembered in
  `localStorage` (`katiuskas:material-account`);
- the field shows the total beside its label — the price times the quantity — and is hidden for
  what costs nothing and for the other types (`updateMovementAccountField`);
- the account goes in the request as `account_id`;
- if no account is marked for the rentals or the sales of material (in Contabilidad) the API
  answers `no_material_account`.

### Material that is out

`/material` has a "Material prestado o alquilado" button (for admin, board and material) to
`/material-prestado` (`loaned-material.js`), which has what used to be at the bottom of
`/material`:

- *Prestado*: the items whose loans have not all come back (`outstanding_loan_quantity`), with the
  units pending and, from each item's own movements, the activities they are out for (loans minus
  returns, per activity);
- *Alquilado*: the open rentals (`GET /material/movements?status=active`) with a "Devolver" button.

A loan is returned from the page of its activity, so this page only reports them.
`material-common.js` has how an item is named, shared with `/material`. In the menu it counts as
part of Material (`ACTIVE_ALIASES`).

## Library pages

### Library (`/biblioteca`, `/ejemplar?id=…`)

The two pages count as one entry of the menu, "Biblioteca" (`ACTIVE_ALIASES`). The names of the
people come from the directory (`library-common.js`), and the dates the API gives as `2026-10-01`
are shown with `dateOnly`, which builds a local date instead of parsing the string as UTC.

#### The catalogue (`biblioteca.html`/`library-items.js`)

A table with:

- a thumbnail of the cover;
- the title (a link, with where it is kept);
- type, author and year;
- a badge "Disponible"/"Prestado" — which is all a member learns about a loan: never who has it —
  or, for an item that has left the library, "Perdido"/"Retirado"/"Vendido" (grey, `statusBadge`
  in `library-common.js`).

Above it there is a search box that looks in everything an item says (accents and case ignored,
every word typed has to appear, the type included), a select of types and a "Solo los disponibles"
checkbox.

Extra controls by role:

- Admin, board and `biblioteca` also get "Tipos": a dialog that lists the types with how many items
  have each, to rename, delete — not while it has items — or add one; the page loads again after
  each change, and the filter and the selects of the forms follow.
- Admin, board and `biblioteca` also get "+ Nuevo ejemplar": a dialog, with the date it was
  acquired, today by default; creating it goes straight to the item's page, where the cover is
  added.
- Admin, board, `biblioteca` and `material` get, below, "Ejemplares prestados" (`GET
  /library/loans`: item, member, since, and a "Devolver" button that records the return today).

#### One item (`ejemplar.html`/`library-item.js`)

From top to bottom:

- the type next to the author and year under the title;
- the date it was acquired and, if it has left, "Baja" with the reason and the date;
- the cover: a plain `<img>` pointing at `/library/items/{id}/cover`, which the session cookie lets
  through, so that no blob has to be fetched for each one; the browser revalidates it when the
  cover changes;
- the data;
- "Comentarios" and "Fotos y documentos", which every member can add to: an image is shown and
  opens in a tab, anything else is a link that downloads; "Borrar" appears on what the person
  wrote, and on everything for admin, board and `biblioteca`;
- for the four roles that see loans, "Préstamos".

"Préstamos" has:

- who has the item and since when (or that it has left the library, in which case there is no form
  to lend it);
- a form to lend it (a picker of the members, a date that defaults to today and cannot be later,
  notes);
- the history;
- "Devolver", which asks for the date of the return (today by default; the date input is limited
  between the day of the loan and today).

Admin, board and `biblioteca` also have:

- "Editar ejemplar": a dialog, with the select of types filled when it opens, and the dates:
  "Adquirido el" and, to give the item its end, "Baja el" with "Motivo de la baja"; clearing both
  brings it back;
- "Añadir/Cambiar portada": a file input that sends the image at once,
  `PUT /library/items/{id}/cover`;
- "Quitar portada";
- "Borrar ejemplar".

Admin alone has "Borrar" on a loan.

#### Role-gated blocks

All of this is in `data-roles` blocks that the page removes from the DOM for the rest, so
`library-item.js` and `library-items.js` only touch those elements when the person has the role
(`canManage`, `canLoan`): a plain member's page must work with them gone.

## Admin page (`/admin`)

### Calendar of activity on `/admin`

The "Actividad" section draws the last year like the contributions of GitHub (`renderActivity` in
`admin.js`, from `GET /admin/activity`): a column per week, Monday first, a row per weekday, and a
cell per day, as a button, that is darker the more different people used the API that day (levels 0
to 4, in proportion to the busiest day shown; `.heat-*` in `style.css`).

- Choosing a day lists who used it, as links to their `/socio` page with their requests (the detail
  starts on the latest day with any activity; names come from the directory, so it is drawn again
  if that arrives after the calendar).
- It scrolls sideways by itself on a narrow screen, starting on the newest weeks.
- The days are the club's (Madrid) and are plain dates here: no time zone arithmetic.
- What was before the feature was put in place is not there.

## Free text with a little formatting

### Free text with a little formatting

The description of an activity (`/actividad`), of a distribution (`/reparto` and `/reparto-gestion`)
and of a library item (`/ejemplar`), and the comments of the library, are written as plain text and
shown through `renderRichText` (`js/rich-text.js`) into an element with the class `.rich-text`.

#### What it supports

It is not Markdown, and only this exists:

- `#`, `##` and `###` headings (drawn as `h3`, `h4` and `h5`, since the page's own title is the
  `h1`);
- `**bold**` and `*italic*` (not nested in each other, but they can hold a link);
- links as `[text](address)` and any bare `http(s)` address (without the full stop or the comma
  that ends the sentence);
- `- ` or `* ` lists and a `---` rule.

Everything else — quotes, code, images, a `#hashtag`, `5 * 3 * 2` — stays as typed.

#### Line breaks

The lines of a stretch of text keep their breaks (the `.multiline` class, `white-space: pre-wrap`,
long words broken) and a blank line starts a new one, so several paragraphs read as such. The
textareas say so in a `p.hint` under them.

#### Safety

- Every span of text is escaped before a tag is written around it and only these tags are
  produced.
- A link is only made if `safeHref` accepts it — `http`, `https`, `mailto` or a path of the
  intranet starting with a single `/` — so `javascript:`, `data:` and the like stay as text, and so
  does an address with a space or a control character (browsers drop tabs and line breaks inside
  an address, so `java<tab>script:` would be a `javascript:` one).
- A link that leaves the intranet opens in a new tab with `noopener noreferrer`.

The API stores and returns plain text; nothing about this is in it.

## Places pages

### Places (`/lugares`, `/lugar?id=…`)

#### The list (`lugares.html`/`places.js`)

A table (on a phone too: a compact one that scrolls sideways inside its box, and not the cards of
`stack-on-phone`) with these columns:

- name — a link, with its other names under it;
- type (a cavity by its subtype, "Sima"; `placeTypeLabel`), with the geology of a cave under it;
- state as a badge (Abierta, Hace falta permiso, Turística (de pago) or Cerrada: how the place can
  be visited, each with a line that explains it, `STATE_HELP` in `places-common.js`, under the
  field of the form and next to the badge on the page); a place whose type has no state shows none;
- where it is (`where`: the zone, the province and the country; the search looks in `area_names`,
  which has the community too, so "Madrid" finds the places of the province and of the community);
- its depth and its horizontal length, each in a column of its own in metres (only a cavity has
  them: a dash for the rest);
- in how many activities, not counting the cancelled ones, the place is one of those they go to
  (`activity_count`, a number, "—" when there is none).

#### Ordering

The table is ordered by name, A first, to begin with, and the headers of name, where, depth, length
and activities order it (`sortable-headers.js`, as in `/admin`): a click picks the column and
another turns it round, the columns that are numbers begin with the greatest, and a place with no
value in one (a canyon has no depth) goes last either way.

#### The "Distancia" column

When the browser can say where the person is (it has the geolocation API and the page is a secure
context: `locate()` asks once when the page opens, and the browser asks the person), the table gets
a column "Distancia" (a straight line from the person to the main coordinate, `formatDistance`,
after "Dónde") that can be ordered too, starting with the nearest.

With no position, because it is not there, was refused or does not arrive in 15 s, the table is as
it was and the column is not drawn. It works with no connection, since it needs only the GPS and
the coordinates of the list.

#### Search and filters

The table has a search that looks in the name, the other names, the type, the state and where it is
(accents and case ignored, every word has to appear, the geology of a cave included) and three
selects, by type, by subtype and by state:

- the subtype is there only when the type chosen is Cavidad;
- the state only when it is "Todos" or a type that has one;
- a filter that is hidden is also cleared, so that it never filters unseen.

#### List and map

"Lista" and "Mapa" switch the table for a map of the same places, with the same filters (see "The
map of the places" below):

- `/lugares?vista=mapa` opens it;
- `&lugar=<id>` also opens the popup of that place;
- `&sistema=<id>` starts the view on the cavities of that system.

#### Buttons on the list

- Any member gets "+ Nuevo lugar", which opens the dialog of `place-dialog.js` and goes to the page
  of the place once it is saved.
- Admin gets "Tipos y geologías", a dialog with three tables to add, rename and delete the general
  types, the subtypes of the cavities and the geologies of the caves, all with the same code
  (`bindCatalogue` in `places.js`):
  - for the types, Cavidad and Cañón show "Fijo" and cannot be touched; each says "Con estado" or
    "Sin estado", and a new one has the box "Con estado", which cannot be changed afterwards;
  - none of the subtypes and geologies is fixed; one that has places cannot be deleted.
- Admin also gets "Zonas" (see below).

#### One place (`lugar.html`/`place.js`)

Under its name, the line of what it is and where it is (`where`, the zones it is in from the most
specific one to the country), and the data:

- other names;
- the main coordinate as "41.52016, -3.63606 · Google Maps" (the degrees and a link to the map,
  and, on a phone, the button "Rumbo", see "Finding a point" below; the UTM is only in the form,
  where it is read and shown; `coordinatesHtml`, also used for each point and in the page of a
  system);
- its altitude above sea level, the geology of a cave, the depth and the horizontal length.

Then:

- "Cómo llegar", "Descripción" and, for a cave or a canyon, "Ficha técnica" — all three through
  `renderRichText`, each with its own "Editar" (see "The long texts" below);
- "Otros puntos", a table of the other points with "Editar" and "Quitar" on each;
- "Fotos y documentos": the files of the place, each with a title, its name, size and who attached
  it, "Descargar" and, for who attached it, admin and board, "Borrar"; an image is shown under it;
  a form attaches a new one;
- who added the place and who changed it last.

Any member has "Editar lugar" and "Añadir punto"; admin and board also "Borrar lugar"
(`data-roles`).

### The dialogs of a place and of a point

The two dialogs, of a place and of a point, are built the first time they are needed by
`place-dialog.js` (`openPlaceDialog`, `openPointDialog`) and shared by both pages, so there is one
form for creating and editing. The shared labels and helpers (states, kinds of point, the
coordinates as a link) are in `places-common.js`.

#### Fields that depend on the type

The fields that depend on the chosen type are in the form only for the types that have them, as in
the form of material:

- for a cavity the subtype (required, "Cueva" to begin with), the measures and the geology;
- for a cavity or a canyon the technical sheet (only in the form of a new place, see "The long
  texts");
- the state only for a type that has one (`data-has-state` on the options of the type).

What a type does not have is sent as null, or empty for the state, which is what the API accepts.

#### The long texts

How to get there, the description and the technical sheet are often very long in a cave, so the form
of an existing place does not have them (`data-new-only` in `place-dialog.js`): only the form of a
new place does. Each has its own "Editar" button in its section of `/lugar`, which opens a form with
just that text in a large box, as wide as the page allows (`place-text-edit.js`, `openTextEdit`),
so that it can be read whole. It saves with `PATCH /places/{id}` naming only that field (an empty
text clears it); with no connection it is kept in the outbox as an edit of the place, like any
other (see "Place edits" under "The outbox"). The buttons are not on the partial page of a place
that was not opened recently.

"Editar lugar" itself also sends a `PATCH`, with only the fields that changed, so saving it never
touches the long texts. If the type becomes one with no technical sheet, the sheet is cleared in
the same request.

#### Other names

The **other names** are a list (`names-list.js`): each name is added with Intro or the button and
is a chip that can be taken off, repeats and blanks are not added, and what is typed and not yet
added counts as one more when the form is sent.

#### Coordinates

The field of **coordinates** is one text box that reads decimal degrees ("40.4168, -3.7038", also
with decimal commas or the letters N, S, E, W) or UTM ("30T 440290 4474257"; without a zone the 30
is assumed, and the page says so). It shows under it what it understood — the decimals and the UTM
— or that it does not understand it, in which case the form cannot be sent.

`js/coordinates.js` does it, with no dependencies: Krüger's series to the sixth order, exact to far
less than a millimetre inside a zone (ETRS89, the datum of the Spanish UTM, is taken as WGS84,
which differs by centimetres); the API only ever sees latitude and longitude.

#### "Usar mi ubicación"

If the browser can say where the person is (it has `navigator.geolocation` and the page is a secure
context: https, or localhost), the field, in the dialog of a place and in that of a point, has a
button "Usar mi ubicación" (`bindLocate` in `place-dialog.js`) that:

- asks for the position (`getCurrentPosition`, with high accuracy, 20 seconds at most);
- writes it in the field as decimal degrees;
- fires the field's own `input` event (so the page shows the UTM and a new place is proposed its
  zone);
- says "Precisión de unos N m".

A refusal, a position that cannot be known or a delay is said under the field too. The altitude is
not taken from it, since a browser gives it over the ellipsoid and the form asks for it over sea
level.

#### Altitude

The altitude, in metres above sea level, is a field of its own (optional, between -500 and 9000)
next to the coordinates, in the dialog of a place and in that of a point, and shows after the
coordinates of a point and as its own row in the place.

## Zone picker ("Where it is")

### Where it is

**Where it is** is chosen with `area-picker.js`: one text box where a person types part of a name
(a municipality, a province, a country) and chooses among what `GET /areas?search=` finds, each
shown with the zones it is in ("Valdemanco — Madrid, España"), so that two with the same name are
told apart.

#### The box and the list

- The search ignores case and accents ("coruna" finds "A Coruña") and is made 200 ms after the
  last key.
- The list opens under the box (`.area-combo`), is used with the mouse or with the arrows, Enter
  and Escape (Escape closes the list, not the dialog).
- What is chosen is shown under the box as a chain with its kind ("Municipio · España › Madrid ›
  Valdemanco"), in which, as in the list and in the text of where a place is, the autonomous
  community is left out when there is a province (`shownAncestors`: in Spain the province says it
  already).
- Typing again undoes the choice, and the box is invalid until a zone is chosen, which the form
  needs.
- In the form the zone ("Dónde está") comes after the coordinates, the measures, the altitude and
  the geology, and (in the form of a new place) just before "Cómo llegar".

#### Proposing a zone from the coordinates

A new place starts with no zone: it is chosen every time, unless the coordinates are written first.
A pause after the last key asks `GET /areas/locate` and puts the zone it answers in the field, with
a line that says it was proposed from the coordinates. It only fills an empty field or one still
holding an earlier proposal, never what a person chose or is typing, and a failure says nothing.

#### Adding a zone that is missing

If the zone is not in the list, "¿No está? Añade una zona" opens `area-dialog.js`:

- the zone it goes in ("Dentro de", chosen with the same field, the one already chosen in the
  place's form if there is);
- its name;
- its kind, which is left blank to take that of the zones already below (the dialog says which)
  and is required for the first one below another.

Once added it is the chosen zone.

Who can add:

- Anybody can, except in Spain, whose zones are seeded (the API answers 403
  `area_spain_admin_only`, with its text in `error-texts.js`: only an admin adds there).
- An admin may also leave "Dentro de" empty to add a country (the form passes `allowRoot` to the
  dialog for an admin).

#### Managing zones ("Zonas")

To rename, move, delete or add inside a zone, admin has "Zonas" on `/lugares`
(`areas-manager.js`).

- "Añadir una zona" opens the same dialog to add a zone anywhere, with "Dentro de" optional
  (`allowRoot`; left empty it adds a country; text there that is not a choice is refused, never
  taken for "none").
- Its search field says "Elige una zona para modificarla". It finds a zone with the same field and
  shows its chain, its kind and how many zones and places are in it, with:
  - its name and kind to change;
  - "Mover dentro de": the zone it is in now, to be changed for another, or emptied to make it a
    country; after a confirmation that says that all it has goes with it;
  - "Añadir una zona dentro";
  - "Borrar zona", which is disabled for a zone with something in it (and says why).

There is no way to merge two zones here, on purpose: it moves everything in one to the other and
cannot be undone, so it is only done through the API (`POST /areas/{id}/merge`).

## Finding a point and working without a connection

### Finding a point (`/brujula`)

`brujula.html`/`compass.js` says how far a point is and which way, for walking to a cave:
`/brujula?lat=…&lon=…&nombre=…&lugar=<id>`, with the point, what to call it and the place that
"Volver al lugar" goes back to.

#### How it is reached

- The button "Rumbo" that `coordinatesHtml` puts after the coordinates of a place, of each of its
  points and of the cavities of a system leads to it.
- The button is shown only on a phone: the browser has geolocation, the page is a secure context and
  its main pointer is a finger (`(pointer: coarse)`, `canGuide` in `places-common.js`; a laptop has
  the API too and nobody walks with it).
- It is part of "Lugares" in the menu (`ACTIVE_ALIASES`).

#### What the page shows

The page watches the GPS (`watchPosition`, high accuracy) and shows:

- The distance (`formatDistance`: metres, or kilometres from a thousand).
- Under it, a circle drawn in SVG with two marks: the north (a red triangle and an upright "N") and
  the destination (a dot at the end of a line from the centre).
- A line that says in words where the destination is ("al NE (42°)") and, within 15 metres or what
  the GPS cannot tell, that the person is there.
- Which of the two sources of direction is in use, and how precise the GPS is.

The title is smaller than the others, the coordinates of the destination go at the end and the
circle is half the height of the screen at most (`46vh`), so that the distance, the circle and where
the point is all fit on the first screen of a phone.

#### Where the direction comes from

The circle is turned so that its top is the way the phone points. That comes from:

1. The compass, if there is one, smoothed round the short side of the circle:
   - `deviceorientationabsolute`, of Chrome on Android, with `deviceHeading`.
   - `webkitCompassHeading` of Safari on an iPhone, for which a button "Activar la brújula" asks the
     permission. The button shows too when no reading has come in a second and a half, since a
     Chrome that has the function may also need it.
2. Else the direction of movement that the GPS gives (`coords.heading`, taken only above 0.7 m/s):
   the phone is assumed to point where the person walks, and the last one stays when they stop for
   eight seconds.
3. With neither, the north is up and the page says to walk a few steps.

A compass reading older than two and a half seconds is not used.

#### Implementation notes

- The maths (`distanceM`, `bearingDeg`, `angleDiff`, `deviceHeading`, `cardinal`) is in
  `js/bearing.js`, with no dependencies.
- The compass is taken as it comes (magnetic north, which differs from the true one by a degree or
  so in Spain).
- A phone held in portrait is assumed, and the screen is not kept awake.

### Working without a connection (`sw.js`, `js/offline.js`)

Near a cave there is often no signal, so some pages work with none. The intranet can be installed on
a phone ("Añadir a la pantalla de inicio": `manifest.webmanifest` and the icons of `images/`, linked
from every page) but nothing needs an app.

It is a service worker, `/sw.js` (scope `/`, registered by every authenticated page through
`initOffline`), that answers from copies when the network does not, or takes more than three
seconds. The Caddy of the intranet serves `/sw.js` with `no-cache` (`docs/deployment.md`), the rest
with `max-age=3600` as before.

#### The files of the site

- Pages, scripts, styles and images are fetched network first, so on line everything is as fresh as
  ever, and each answer is kept.
- At install, the pages in `SEED_PAGES` (home, login, `/lugares`, `/lugar`, `/brujula`,
  `/actividades`, `/actividad`) and everything they load are fetched too.
- What they load is found by reading them: `src` and `href` of a page, the `import`s and the
  `/vendor/` and `/images/` paths of a script, the `url()`s of a style.
- So a page works with no connection even if nobody has opened it yet, and nothing has to be listed
  by hand: a page that has to work off line goes in `SEED_PAGES`.
- A page is one file whatever its query (`/lugar?id=3` and `/lugar?id=4`).

#### The data

The data is kept in the cache `katiuskas-data`, also network first (`dataKind` says what, `LIMITS`
how many). What is kept:

- The list of places and the catalogues the pages ask for: `GET /places`, `/places/types`,
  `/places/subtypes`, `/places/geologies`, `/systems`, `/holidays`, and `/club-info` (the "Datos de
  interés" of the home page).
- Each place that is opened: itself and what its page asks of it (`/places/{id}`, its files and
  activities and its system).
- Each activity: itself and its participants, places, guests and memoria entries
  (`/activities/{id}…`).
- The pages of the list of activities (`/activities?…`, the query is part of the question).

Only the last 20 places, the last 20 activities, 160 of those answers and 12 lists are kept; the
oldest go.

Nothing else of the API is kept: not the people, not the bytes of a file (so the text and the files
of a memoria entry are not there), not the material, not anything written. Nothing is ever sent from
the worker.

#### Refreshing

`initOffline` (`refreshPlaces`) refreshes, at most every six hours and two seconds after a page
opens:

- the list of places;
- the catalogues;
- the first page of the active activities, with the pages of the next ten of them
  (`PRELOAD_ACTIVITIES`).

So **the list is always there**, the activities that are about to take place too and the places
opened lately, with nothing to save by hand.

The same call asks the worker to look at its files again (`refresh-files`, with a conditional
request: a file that did not change costs a 304), since what the install kept would otherwise only
be renewed when a file is used on line.

#### The session

The session is the other half. `initSession` keeps the last answer of `GET /auth/me`
(`katiuskas:me`).

If the network is not there and the person is known from that copy, the page opens as a copy
(`isOffline()`):

- The network is "not there" on a failed request, which is told from a refusal: only a "no" from the
  server ends a session.
- The roles come from the copy.
- A strip at the top says "Sin conexión: se ven los datos del móvil. Tus ediciones se envían al
  volver." It is kept short on purpose: on a phone every line of it is lost to the page.
- The page is loaded again when the connection is back (`online`).
- It waits four seconds for `/auth/me` in that case. A person who has never signed in on that phone
  waits for it as before, and sees "No se puede conectar con la intranet".

Signing out, or a session that is refused, deletes the copies of the data and `katiuskas:me`
(`clearOfflineData`).

#### What works with no connection

- The menu has only "Inicio", "Actividades" and "Lugares", and the top bar has no "Ver como…"
  (`keepOfflineMenu` in `layout.js`: the other pages and that button need the API).
- `/actividades` shows only the list of active activities that is kept: "Ver todas", "Ver
  calendario" and "+ Nueva actividad" are not there. `/lugares` has no "Sistemas", "Zonas" or "Tipos
  y geologías" (all need the API); "+ Nuevo lugar" stays.
- The home page shows the data of interest from the copy and "No disponible sin conexión" for the
  Junta and the documents, which are not kept (the link to the history of the Junta is not there
  either).
- The activities that are active are listed and the page of one of the next ones opens ("Actividad
  no guardada en el móvil." for another, and the material is not loaded, without a
  modal about it).
- The list of places works, and so does the page of every place in it (a partial one if it was not
  opened recently, see below), each with its "Rumbo" button on a phone.
- The page of a place opened lately works in full, with its "Rumbo" buttons.
- `/brujula` needs nothing but the phone (the GPS and the compass).
- The map has no tiles.
- The page of a place that was not opened recently is a partial one (`loadPartial` in `place.js`,
  `err.network` with `isOffline()`): it is made of what the list of places has, which is always
  kept (name, type, state, where, coordinates, altitude and measures), with a note, "Datos
  parciales: faltan los textos, los puntos, los ficheros y las actividades.", and "No disponible
  sin conexión." where the rest would be (how to get there, the
  description, the technical sheet, the points, the files and the activities). The full "Editar
  lugar" is not offered, because the dialog would show the fields it does not have as empty;
  "Corregir datos" is (`place-quick-edit.js`: the name, the coordinate with "Usar mi ubicación"
  and the altitude, only the fields that change are kept), and so are "Añadir punto" and the form
  of files, as they only add. If the place is not even in the list, the page says "Lugar no
  guardado en el móvil.".
- Nothing can be changed, except what goes through the outbox (see "The outbox" below): any other
  change with no connection fails as it always did.

### The outbox (`js/outbox.js`)

What a person does with no connection and has to reach the server is kept on the phone, in IndexedDB
(`katiuskas-offline`, store `outbox`), and sent when it can be.

#### Kinds of entry

Four kinds of entry go through it so far:

- The text and files of memoria entries, from the form of `/actividad`. When the page is a copy, or
  when sending fails with `err.network`, `queueMemoria` queues the entry instead of showing an
  error, and says "Guardado: se enviará al haber conexión".
- The edit of a place (`place-edit`) and a point added to one (`place-point`): two kinds, both from
  the dialogs of `/lugar` (`place-dialog.js`: "Usar mi ubicación" works with no connection, it
  only needs the GPS).
- A photo or file attached to a place (`place-file`), from the form of `/lugar` through `bindFiles`'
  `queue` option. The page of a system has no queue.

A new kind of entry needs its `SENDERS` and, if sending twice would show, its `ALREADY_SENT` in
`outbox.js`.

#### Entries and the queue

- An entry is `{ kind, login, queuedAt, … }`. It belongs to the person who made it and is only ever
  sent as them, never as whoever signs in next on that phone.
- The queue of a page is drawn by `bindPendingList` (`pending-list.js`): above the memoria in
  `#memoria-pending`, below the buttons of a place in `#place-pending`. Each entry is shown as
  "Pendiente de enviar", with "Reintentar" and "Descartar".
- Signing out does not empty the queue: it is the person's work, and waits for their next session.

#### Sending

`flush(login, api)` sends the entries in order.

- A page does it 1.5 s after it opens on line (`initOffline`, called from `initPage` with `apiFetch`
  and `apiFetchForm`, so that `outbox.js` does not import `api.js`) and when the browser says
  `online`.
- Only one page does it at a time (Web Locks).
- It stops at the first request that finds no connection or a refused session.
- An entry that the server refuses stays with its `error` (shown, never retried by itself, so one
  bad entry does not block the others) until the person retries or discards it.
- Before sending, `ALREADY_SENT` looks at what the server has, so a request that arrived but whose
  answer was lost does not add the entry twice.
- When something was sent, a message says so and the event `katiuskas:outbox-sent` makes the page
  draw its lists again.

#### Place edits

The edit of a place is kept as the fields that changed (`diffPlace` in `place-body.js`), with what
each had when the person edited (`base`) and what they want (`changes`), never as the whole place.
It is sent with `PATCH /places/{id}` (`backend-internals.md`, "Partial edit"), which changes only
the fields it names, so what others changed meanwhile in other fields is not touched (`PUT`
replaces everything, and would undo it).

At send time the place is read first, only to look for a clash. If somebody changed one of those
fields since (it is neither `base` nor `changes`) nothing is sent and the entry says "El lugar ha
cambiado mientras tanto (…)" until the person looks and retries or discards. If the fields already
have the wanted values, nothing is sent either.

With no connection the dialog works with the catalogues that the phone has (types, subtypes,
systems, geologies), and the zone stays the one the place has (the search of zones and the proposal
from the coordinates need the API). Only an edit of an existing place and a new point are kept. A
new place, changing or removing a point, the link with OpenStreetMap and deleting a place still need
the connection, and their buttons are not offered.

#### Pictures and files

A picture attached to a place, on line or not, is made smaller first (`image-resize.js`):

- at most 1600 px on its longest side, as a JPEG, turned the way the phone took it;
- kept as it was if it is not a JPEG, PNG or WebP, is already small or would not get smaller.

That makes it quick to send over a weak signal and lets it fit the 10 MiB of the API. The file waits
in IndexedDB as a `File`; to avoid sending it twice, `ALREADY_SENT` looks for a file of the same
person, name and size attached after the entry was queued.

## Systems (`/sistemas`, `/sistema?id=…`)

A system is a group of cavities connected underground (see `backend-internals.md`, "Systems"). The
pages light up "Lugares" in the sidebar (`ACTIVE_ALIASES`) and are reached from "Sistemas" on
`/lugares`.

### The list of systems

`sistemas.html`/`systems.js` lists them:

- A table with the name (a link) and the cavities of each (links, with their subtype).
- A search by the name of a system or of a cavity.
- "+ Nuevo sistema" for any member: a dialog with the name and the description that goes to the page
  of the new system.

### One system

`sistema.html`/`system.js` is one system. It has, in order:

- Its name and description (`renderRichText`).
- "Editar sistema" (any member) and "Borrar sistema" (admin and board, with a confirmation that says
  the cavities stay).
- A map with all its cavities and, in grey, the other points of each (parking, approach… with their
  kind, name and cavity as a tooltip, `pointMarker`). Under it, "Ver en el mapa de todos los
  lugares"
  (as under the map of a place), which opens the big map starting on them.
- "Cavidades": a card for each cavity, described below.
- "Fotos y documentos del sistema", under the cavities.

#### Cavities of a system

Each card has everything the API gives of the cavity:

- its subtype and state, other names, where it is, coordinates, altitude, geology, depth and
  horizontal length;
- its "Cómo llegar", "Descripción" and "Ficha técnica";
- its own "Fotos y documentos" (not the system's, which are under them);
- "Quitar" to take it out of the system.

A row adds one: an input with a `datalist` of every cavity of the catalogue, "Nombre — Tipo
(dónde)", which says "en «otro sistema»" for one that is in another, and asks before moving it, as a
cavity is in one only.

Every change sends the whole set with `PUT /systems/{id}/places` and the page is drawn again, also
when it fails.

### The system on the page of a cavity

- On the page of a cavity that is in a system there is a section "Sistema", just before the map: a
  line that links to the system, its description and links to its other cavities ("Otras cavidades
  del sistema", with their subtype and where they are).
- The system's photos and documents are not there but in the "Fotos y documentos" of the cavity,
  after its own. With a system the section has the subtitles "De esta cavidad" (its files, the only
  ones that can be changed there) and "Del sistema «…»" (read only, they are changed on the page of
  the system), and the form to add a file says that it adds one to the cavity.
- The form of a cavity has "Sistema (opcional)", a select with "— Ninguno —" and the systems, only
  for the types of the cave kind.
- The photos and documents of a place and of a system are drawn and sent by the same module,
  `place-files.js` (`fileRowsHtml`, `bindFiles`, with the address `/places/3` or `/systems/4` as
  `base`).

## Place maps and OpenStreetMap

### The map of the places (`map-common.js`)

#### Libraries and base layers

Leaflet 1.9.4 and Leaflet.markercluster 1.5.3 are copied into `web/vendor/` (with their licences),
so the pages ask nobody for code, only for tiles, and no build step is needed. `loadLeaflet` adds
their scripts and styles the first time a map is shown, so a page without a map does not pay for
them.

There are four base layers, chosen with the control of the map and remembered in the browser
(`localStorage`, `katiuskas:places-map-layer`), each with its credit:

- "Topográfico (IGN)" (the default): the IGN's `mapa-raster` WMTS, which has no tiles beyond zoom 17
  and is enlarged after that.
- "Satélite (PNOA)" (Spain only).
- "Satélite (mundial)": Esri's World Imagery, for everywhere else, with no key.
- "OpenStreetMap".

The IGN's are the official source of Spain and free to use with that credit, and OpenStreetMap's own
tiles ask for light use, which is a club's.

The list of base layers (the button with the stack of squares) closes when one is chosen, since on a
touch screen nothing else would close it but a click on the map.

#### Places on the map

- A place is a dot of the colour of its state (`STATE_COLORS`, the colours of the badges; a place
  whose type has no state is grey, "Otro" in the key of the big map).
- Its popup has its name as a link, its type, its state and where it is.
- Only where a pointer can hover (`(hover: hover)`, `bindPlaceInfo`), its name is also a tooltip. On
  a phone or a tablet a tap is also a "mouseover" for the browser, which showed the tooltip and the
  popup together, so there the tap shows the popup alone.
- Nearby places are grouped (`L.markerClusterGroup`) in the browser, which is fine for the thousands
  of places a club may have.
- The map uses the same `GET /places` as the table, and every filter and the search redraw it and
  fit it to what is left.
- The small map of `/lugar` has the main coordinate in its state's colour and the other points in
  grey, with their kind and name as a tooltip, and a link to the big map centred on the place.

#### Clicks on the map

A click on a map where there is nothing opens a small popup with the coordinates and a button, which
opens a form with them written in:

- On the big map, "Crear un lugar aquí": the form of a new place, with the zone they are in already
  asked for (`GET /areas/locate`, as when they are typed).
- On the small map of a place, "Añadir un punto aquí": the form of a new point of that place.

`offerOnMapClick` in `map-common.js` does it for both, each page giving it the label and what to
open.

- A click on a place or on a point opens its own popup or tooltip, as markers do not pass their
  clicks on to the map (`bubblingMouseEvents: false`).
- The popup has its close button, and a click elsewhere on the map only closes it (or any other
  popup that is open) without opening another. Leaflet closes the popup and still sends that click,
  so the close is remembered for the turn it happens in.
- A click that closes the list of layers (or a popup, or the search box) only closes it: the map is
  told at `preclick`, before they close, so that it does not also offer to create something there.

#### The button of the whole window

Every map has, under the zoom buttons, a button that makes it take the whole window of the browser
(`addExpandButton` in `createMap`, so all of them have it).

- The container gets the class `map-expanded` (fixed, over the sidebar and the top bar, with the
  page under it not scrolling).
- Leaflet is told its new size (`invalidateSize`, whose default keeps the centre of the map in the
  middle, not the corner; `pan: false` would keep the corner).
- The same button, or Escape, gives the map back its place.
- The click on the button is stopped (`L.DomEvent.stop`), as `disableClickPropagation` alone does
  not keep a `click` from reaching the map, which would take it for a click on the place under the
  pointer and offer to create one.
- It is deliberately not the Fullscreen API: the monitor is not taken over, only the window, so the
  address bar and the tabs stay.

#### Searching the map of `/lugares`

On the map of `/lugares` alone, below it, is a magnifier (`addSearchControl`) that opens a box to
look for a place by name.

- It asks `GET /osm/search` (the elements of the OpenStreetMap mirror, to find the cave a person
  knows the name of) and `GET /areas/geocode` (towns, valleys, peaks, from Nominatim) at once.
- It asks when the text is sent, not as it is typed, since the geocoder is outside and slow. Either
  may fail or find nothing without the other being lost.
- It lists first "En OpenStreetMap, sin añadir todavía al catálogo" (the name, what it is and where:
  the municipality and the province, or its coordinates if it is in none known, so that the twenty
  "Cueva del Agua" can be told apart) and then "Otros sitios".
- Choosing a place moves the map there (to its box, or to zoom 14 if it has none) and rings it until
  the next search.
- Choosing an element goes to zoom 17, puts the layer of its category on the map if it was not (the
  control fires `osmfocus` on the map, which `osm-layer.js` listens to) and, when the points of the
  view are there, opens its popup, with "Añadir al catálogo".
- The search box closes with the magnifier, with a click on the map and with Escape (heard in the
  capture phase and stopped, so a map over the whole window is not also given back).

#### Failures and limits

If the tiles or Leaflet cannot be loaded the page says so and the rest keeps working. Nothing here
is
drawn from the server's side, and an offline map is not there.

### The points of OpenStreetMap on the map

On the map of `/lugares` alone, the selector of layers has under the base layers a box for each
category of the **points of OpenStreetMap** ("Cuevas (OpenStreetMap)", "Barrancos…", "Refugios…" and
"Vías ferratas…"; `js/osm-layer.js`, `createOsmLayer`, driven by `setUpOsm` in `places.js`, which
gets the control from `createMap`).

#### Layers

- Each category is a layer group added to the map as an overlay, so ticking or unticking its box is
  what adds or removes it.
- What is on is remembered as a list in `katiuskas:places-osm` (none the first time).

#### Where the points come from and when they are asked for

- They come from the local mirror (`GET /osm/elements`), never from OpenStreetMap.
- Only from zoom 11 up (`MIN_ZOOM`: a few dozen kilometres on screen, hundreds of points at most).
  Below that a note under the map says to zoom in.
- The map asks for the box in view plus a margin (`bbox`) and the categories that are on, when it
  stops moving (a short delay).
- It does not ask again while the view is inside what it has and the answer was not cut.
- It ignores an answer that arrives after the view moved on.
- It says how many it shows or that there are more than it can (the API cuts at 1,500).

#### How they look and what a click does

- They are grey rings drawn on a canvas (as thousands of SVG circles would be slow), apart from the
  club's places and not clustered with them.
- A click opens a popup that asks `GET /osm/elements/{type}/{id}` for the tags and shows: the name,
  the kind and the municipality and province; the useful tags (altitude, rappel, difficulty,
  capacity, fee, access, operator, description); and the website if it has one.
- The popup draws the outline of a hut or the line of a canyon until it closes.
- A click on a ring is for the ring (it does not offer to create a place).
- The credit is not in the popup: while any category is on, the map's own credit adds "Puntos ©
  colaboradores de OpenStreetMap" (the ODbL asks for it).

#### Añadir al catálogo

The popup has a button, **Añadir al catálogo**, that opens the form of a new place
(`openPlaceDialog`
with `osm`) with what OpenStreetMap says already in it (`osmDefaults`):

- The type its category suggests (Cavidad, Cañón, Refugio or Vía ferrata); the subtype and the state
  are left to the person.
- The name and the other names (`alt_name`, `name:es`).
- The altitude (`ele`).
- For a cavity, the depth and the length when the tags are numbers.
- The description with the website at the end.
- The coordinates.

The zone is the one the element already has (`area_id` of `GET /osm/elements/{type}/{id}`, read with
`GET /areas/{id}` and proposed as "Zona del elemento de OpenStreetMap: …", which the person may
change), so nothing is asked of the outside service. An element in no known zone (the sea,
Gibraltar) falls back to proposing the zone of its coordinates as with any coordinates
(`GET /areas/locate`).

A note above the form says where the data comes from. Saving sends `osm_type` and `osm_id` with the
rest, so the place keeps its element, and the page goes to the new place; that element is no longer
offered as a candidate.

### Linking a place to OpenStreetMap

The page of a place (`/lugar`) has no section for it, since it is rarely used. The section
"OpenStreetMap" is inside the dialog "Editar lugar" (`#osm-section` in `lugar.html`, hidden on the
page; `place.js` moves it into the slot `#pd-osm-slot` of the dialog when it opens and back when it
closes). It says whether the place is an element of OpenStreetMap (its type and id) and has the
buttons to link and unlink it.

Linking, unlinking and copying a value from OpenStreetMap save at once, so they close the dialog
(its
form would show the place as it was) and reload the page.

#### Linking

"Vincular con OpenStreetMap" opens a dialog (`js/osm-link.js`, built with `buildDialog` of
`place-dialog.js`) with the elements near the place that are not yet a place, from
`GET /osm/nearby`.

- One row for each element, with its name, what it is, where it is (municipality and province, if
  known), its type and id, how far it is and a button to link it.
- If it looks like the place's name, the row says "mismo nombre" or "nombre parecido". Those come
  first, then the nearest.
- A cave or a canyon is looked for among the caves or the canyons, and any other place among the
  huts and the via ferratas.
- A select widens the circle from 500 m to 1 or 2 km, and a newer search drops the answer of an
  older one.
- Choosing one calls `PUT /places/{id}/links/osm` and reloads the page.

#### Unlinking

A linked place says "Vincular con otro elemento" and has "Desvincular", which asks first and calls
`DELETE`. The element that a place is no longer appears on the map of `/lugares` as a candidate, and
does again when the link is taken away.

#### What OpenStreetMap says of its element

A linked place also shows **what OpenStreetMap says of its element** under that line, in the dialog
(`osmDataHtml` in `osm-link.js`, from `GET /osm/elements/{type}/{id}`, asked once for each element
and drawn again whenever the place is):

- the name, the other names, the coordinates (with how far they are from the place's), the altitude;
- the facts of the popup (rappel, difficulty, capacity, fee, access, operator, description);
- the website and the date it was last edited there.

It is shown as it is, never merged into the club's data, which is what the rest of the page shows.

- A badge "no coincide con el lugar" marks the name that is neither the place's name nor one of its
  other names, coordinates more than 25 m from the place's, and an altitude more than 20 m from the
  place's.
- A note says when the element is no longer in OpenStreetMap (marked as gone by a load), suggesting
  to link another or to unlink.

#### When the element has changed

When the element has changed (`osm_changes` of the place) a notice on the page itself
(`#osm-changes`, under the buttons of the place, the only trace of OpenStreetMap there) says what,
and where to look.

- Each change is shown as "antes «…», ahora «…»" under the name that a person knows it by
  (`CHANGE_LABELS`).
- A button **Marcar como revisado** (`POST /places/{id}/links/osm/review`) marks it as seen.
- The list of places marks such a place with a badge "OSM ha cambiado" (`osm_changed`).

The rows of the block that do not agree with the place have a button that copies the value into it,
with the edit of the place (`PUT /places/{id}`, built by `copyFromOsm` from the place as the page
has
it, so nothing else changes):

- "Usar como nombre" (the old name stays among the other names, so it is not lost).
- "Añadir a los otros nombres" (only the new ones).
- "Usar estas coordenadas".
- "Usar esta altitud".
- "Copiar a la descripción", which is only offered when the place has no description, since what the
  club wrote is never replaced.

Copying does not review: whoever has looked at everything says so with the other button.

### The mirror of OpenStreetMap in `/admin`

The status of the system has a group "Mirror de OpenStreetMap" (`osmGroup` in `admin.js`, from `osm`
of `GET /admin/status`). It shows:

- The elements in total.
- For each extract (España con las Baleares, Ceuta y Melilla; Islas Canarias): how many elements it
  holds (and how many are no longer in OpenStreetMap), the date of its data and how old it is in
  calendar days, so that it agrees with the date next to it ("de hoy", "hace 1 día", "hace N días";
  in bold when it is older than the limit).
- The last good load and what it did, and, if the latest load failed, when and why.
- If the last good load found municipal boundaries the club has no zone for, a line says how many
  and
  which (INE codes): the list of municipalities needs updating.

A mirror never loaded says so. The same two problems (a failed load, old data) are in the list "Hay
problemas" at the top.

## Tables, links and sign-in

### Tables on a phone

A table is scrolled sideways inside `.table-wrap` when it does not fit, which on a phone hides its
last columns, often the buttons.

A table with class `stack-on-phone` (those of `/admin`, of the library and of `/material`) is drawn
instead, under 640px, as a card per row:

- `js/stacked-tables.js`, started by `initPage`, gives every cell `data-label` with the text of its
  column's header (again whenever the rows are redrawn, with a `MutationObserver`).
- `style.css` shows each cell as its label above its value.
- The header keeps only the sortable columns, as a row "Ordenar por" of buttons, and the other
  headers go.
- A cell that spans the row (the "nothing here" message) and an empty one carry no label.

Other phone adjustments:

- The rows of "Estado del sistema" put the name above the value under that width, since its fixed
  column of 20rem left the value no room.
- `.section-header` lets its button wrap below the title instead of squeezing it.

The list of places is on purpose not one of them: it stays a table, compact and scrolled sideways,
so
that the columns can be compared.

### Links

Every `<a>` is the same colour (`--link`) whether visited or not and is underlined only on hover, as
the documents of the home page. It is one rule at the top of `style.css`, `:where(a)`, written with
`:where()` so that it has no specificity and whatever styles its own links (the sidebar, `a.btn`,
the
cards, the pills of `.section-index`…) still wins. A new kind of link that has to look different
gives itself a class, not a rule on `a`.

### Remembering the email at sign-in

To save typing the address (slow on a phone), `login.html` offers the last five addresses that
signed
in on this browser, in `localStorage` (`katiuskas:login-emails`, newest first, no repeats whatever
the case). When the email field is focused, the same menu as the pickers (`picker-search.js`) lists
them, and choosing one moves the focus to the phone.

#### What is saved and when

- Only the email is saved, not the phone, and only after a sign-in that worked.
- The address typed when asking for the link waits in `katiuskas:login-pending-email` and is moved
  to
  the list when this browser completes the sign-in: by the code, by the link opened in this tab, or,
  in the tab that asked, when another tab of the browser signs in.
- A request that is never followed through is not remembered.

#### Details

- The field has `autocomplete="off"` (and a `name` that does not say "email"), so that the browser's
  own offer of addresses it has stored from other sites does not cover this menu. The phone has it
  too, so that it is always typed.
- It also works on the club's shared tablet (`local.` address): whoever can use it can see the
  addresses of the others, which is accepted.

## Pickers and what they offer

### Searching in a picker (`picker-search.js`)

The searchable pickers are an `<input list>` over a `<datalist>`, which the browser filters by
itself
without ignoring accents ("garcia" did not find "García").

`initPickerSearch`, called by `initPage`, takes every such input over the first time it gets the
focus:

- It removes its `list` attribute and shows its own menu (`.picker-menu`, right after the input so
  it
  also shows over a dialog).
- The menu has the options of that datalist that contain every word typed, ignoring accents and
  case:
  all of them, however many, as the menu scrolls.
- It works with the arrow keys, Enter (which chooses, it does not send the form) and Escape (which
  closes the menu, not the dialog).
- Choosing writes the value into the input and fires `input` and `change`, so pages do not change.

`resolvePickerId` also takes what was typed without choosing when it leaves one single entry,
ignoring accents. A page only fills the datalist, as before.

### How a person reads in a picker

Just "Nombre Apellidos". A "#login" goes after the name only for the people who share it with
somebody else in the same list, so the two can be told apart and what is typed still resolves to one
person (`personPickerLabeler` in `api.js`; it is given the whole list the picker is made from, not
the part of it that is offered).

No other picker carries an "#id":

- The one of activities (in the movement dialog of `/material`) reads "<date> — <title>", which is
  unique (no two activities share a start date and title).
- The ones of material are explained below.

### How a piece of material reads

Every listing of material (`/material`, `/material-prestado`, the loans of `/actividad` and the
history of `/socio`) writes it with `itemLabel` (`material-common.js`). The pickers of material (the
loan picker of `/actividad`) use `itemPickerText`, which reads the same as a row of `/material`.

There is no "#<id>": two items are never exactly the same (the API refuses it), so the type, the
name and the code tell them apart.

- General material is named by the API (`display_name`: the type, brand and model, the values of its
  attributes and the description, joined with " · ").
- A rope or a bag is named by its brand and model, its own data and its colour, if it has one. The
  colour is the last part, so "azul" finds it in the search of `/material`, which looks in what the
  table shows; the form of a rope or a bag has an optional "Color".
- The label is the type, that name and, if the item has a code, "#<code>" (`itemRef`).

The table of `/material` has its own "Tipo" column, so its "Características" column starts at the
name (and ends with the code, since there is no column for it). An item that lacks something its
type
now requires is marked "Incompleto" there.

### Who the pickers offer

`GET /users/directory` says whether each person is a `member` now or a `former_member`, and the
pickers offer only whom the thing is open to (the API refuses the rest):

- Only members: the participants of an activity ("Añadir participante" on `/actividad` and the
  participants of "Nueva actividad" on `/actividades`), and the "Socio" of a loan (on `/actividad`)
  or of a rental (on `/material`).
- A member or a former member: the "Socio" of a sale on `/material`. It follows the type chosen, and
  what was typed before and no longer fits is cleared.
- Any person: the organizer of an activity.

In the participants list of `/actividad`, each name has a "Federado" or "No federado" badge
(`federated`), as information: nothing requires it.

### The form to ask for something (`distribution-order-form.js`)

The "Pedir" form is one piece of code, `mountOrderForm`, which fills a `<form>` in: the "A nombre
de"
picker, one quantity per article or one `<select>` per option, the extra checkbox and the button. It
sends the requests (one for each article with a quantity). It is mounted by:

- `/reparto`, where the name is optional: left empty it is for oneself.
- The **Pedidos** section of `/reparto-gestion`, above the checkbox of finished orders, in "Pedir a
  nombre de un socio" (`#order-box`; hidden in a draft and where there is nothing to ask for). There
  the name is required, as whoever manages an order seldom asks for themselves, and the people
  offered are those who may have the distribution.

After asking, the page reads its lists again. `optionSelects` and `readChoices` are also what the
dialog to change an order uses.

## Activity pages (`/actividad`, `/actividades`, `/lugar`)

### Signing people up on `/actividad`

- In a public activity anybody sees the "add a person" row and the "Apuntarme" button.
- In a private one only the organizer, admin and board see them. For the row, so does whoever is
  already signed up: it appears as soon as they are, as the button turns into "Darme de baja".
- "Apuntarme" is only offered to members (`canAddOthers`, `canJoinSelf` in `activity.js`).

In the participants list the "Quitar" button sits right after the name it is about
(`.participant-row`), not at the far end of the row. It is only on the rows the viewer may remove
(`canRemoveParticipant`, as the API decides): admin/board, the organizer of a private activity, and
whoever signed that person up; never on the viewer's own row, which is "Darme de baja".

### Places on `/actividad` and activities on `/lugar`

#### Places on `/actividad`

Between the header and "Participantes", `/actividad` has "Lugares": the places of the catalogue the
activity goes to, each a link to its page with its type.

- For whoever can edit the activity (`canManage`), each has "Quitar" and there is a row to add one
  (`#place-search`, an input with a `datalist` of the whole catalogue, each as "Nombre — Tipo
  (dónde)", loaded once from `GET /places` and without the ones already there).
- Every change sends the whole set with `PUT /activities/{id}/places` (`savePlaces`) and draws the
  list again, also when it fails, so that the page never shows what was not saved.
- The form to create an activity has no places: they are added on its page.

#### Places in the memoria

- The dialogs to add to the memoria and to edit an entry have "Lugar (opcional)", a select with none
  and the places of the activity (`fillPlaceSelect`; when it has none it says they are added in
  "Lugares").
- Each entry names the place it is about, as a link, after its author and date.
- Taking a place off the activity while an entry is about it is refused, and the message says so.

#### Activities on `/lugar`

`/lugar` has "Actividades", a table with the date, the activity (a link, with its state as a badge),
its type and who organizes it, the latest first (`GET /places/{id}/activities`).

### Guests on `/actividad`

Under the participants, an "Invitados" section lists the people from outside the club who go
(`GET /activities/{id}/guests`), each with a badge "Invitado de <name>" (who of the club invites
them).

- A line with the total sits at the top, after the description and the buttons and before the
  participants (`#attendance`, `renderAttendance`). It shows the people of the club signed up and,
  only when there are guests, how many and the sum.
- Whoever is signed up sees a row to add one with just a name.
- The organizer, admin and board see the row too, with a second field to choose which of the people
  signed up invites them (the datalist `#guest-inviter-options`, filled from the participants).
- The "Quitar" button of a guest is for whoever invited them, the organizer, admin and board
  (`renderGuestRow`, as the API decides).
- A person who still has guests cannot leave or be removed from the activity: the error text says to
  remove them first (`has_guests`).

### The dates of a new activity

"Nueva actividad" (`/actividades`) opens with the dates filled in (`defaultActivityDates`, in
`activities.js`, with the holidays of `GET /holidays`; if they cannot be read, only weekends count):

- The start is the first day, from today on (today included), that is a weekend or a holiday.
- The end is the last day of that run of days off: the day before the first working day after the
  start.

They are only a suggestion: both fields can be changed.

### The limit of people

"Nueva actividad" (`/actividades`) and "Editar actividad" (`/actividad`) have an optional "Máximo de
apuntados" (`max_participants`): blank is no limit, and the edit form, which resends everything,
sends `null` for a blank one.

On `/actividad` the total line (`#attendance`) says "…de un máximo de N" and, once the places are
all
taken, "la actividad está completa". Then "Apuntarme", "+ Participante" and "+ Invitado" are
disabled
with that as their tooltip (`renderAttendance`; the API refuses it too, 409 `activity_full`).

Lowering the limit under the people already going is refused with its own message
(`max_below_attendance`).

### The organizer on `/actividad`

Under the header line, "Organizador: <name>" (`#activity-leader`; a link to the person's page only
for admin and board, like every name there), once the directory has the name.

### Being signed up on `/actividad`

Under the header, a note "✓ Estás apuntado a esta actividad." shows while the viewer is among the
participants (`#joined-note`, set by `loadParticipants` together with the "Apuntarme"/"Darme de
baja" buttons), so it is not something to look for in a long list.

### Loans and returns on `/actividad`

The "+ Préstamo/devolución" dialog (`activity.js`) offers, as the material to pick, only what fits
the type chosen (`loanItemEntriesFor`):

- For a *Préstamo*, items that can be loaned and have a unit available.
- For a *Devolución*, those still out on loan for this activity (what its loans minus its returns
  say, `outstandingByItem`).

The types themselves are only offered if something fits (and with neither, the button is off),
changing the type clears a pick that no longer fits, and the quantity's `max` is what is available
or
what is out.

### An activity's state

The API only says whether an activity is `cancelled`; whether it is still to come, going on or over
is worked out from its dates by `activityState` (`api.js`):

- *Cancelada* if cancelled.
- *Realizada* if its `end_date` is before today.
- *Planificada* if its `start_date` is after today.
- *En curso* otherwise.

`activityStateLabels` has the texts; the badge's CSS class is `status-<state>`.

Who uses it:

- `activities.js`: the list, with its "active only" filter `?active=true` (which also has what ended
  less than a week ago), and the calendar, which leaves cancelled ones out.
- `activity.js`: the header and, for whoever can manage the activity, a "Cancelar actividad" button
  next to "Editar actividad", after a confirmation (`PATCH` with `cancelled: true`). It becomes
  "Reactivar actividad" while the activity is cancelled (`PATCH` with `cancelled: false`); the edit
  dialog has no state field.
- `member.js`.
- `material.js`, which asks for `?active=true` for its activity picker.

## Sessions and returning to the requested page

### A session that cannot be checked is not a session that is gone

`initSession()` throws the token away and goes to `/login` only when the server answers that it does
not accept it (a 4xx).

When `GET /auth/me` fails for a reason that says nothing about the session, it tries again twice
(after 1 and 2 seconds, `checkSession`). The reasons that say nothing about the session are:

- no network (`error.network`);
- a 5xx (a restart in the middle of a deploy, say);
- a 408 or a 429.

If it still fails, it covers the page with "No se puede conectar con la intranet"
(`showConnectionProblem`). That cover keeps the token, reloads by itself after 8 seconds and has a
"Reintentar ahora" button. Before, any failure of that request signed the person out of a session
that was still valid on the server.

### Going back to the page that was asked for

When `initSession()` finds no session (no token, or `GET /auth/me` rejects it), it keeps the address
asked for (path and query, except `/` and `/login`) in `localStorage` under `katiuskas:return-to`,
with the time, and sends the person to `/login`.

- `auth.js` calls `goAfterLogin()` (`api.js`) wherever a sign-in ends, which goes there instead of
  `/` and forgets it.
- It is localStorage and not sessionStorage because the magic link may open in another tab.
- The saved address is used only if it is less than an hour old and is a path of this site (it
  starts with a single `/`, never `//` or `/\`, and is not `/login`), so it cannot take anybody
  elsewhere.
- Signing out on purpose forgets it.

## Dialogs and errors

### Create and edit dialogs

Every "create X"/"edit X" form is a `<dialog class="panel modal">`. `.panel` already gives it the
right background/border/padding/spacing; `dialog.modal`'s own `css/style.css` rules only add what a
plain `.panel` never needed: the dimmed `::backdrop`, a max-width, and the "×" `.modal-close`
button's own positioning. It is never a `<div>` toggled to `hidden="false"` in place on the same
page the way every one of these used to work.

#### initModal

`js/modal.js`'s `initPage`-sized `initModal(dialog, form?)` is the one call each page makes per
dialog:

- It wires every `[data-modal-close]` element inside (the "×", a form's own "Cancelar") to
  `dialog.close()`.
- When a `form` is given, it resets it on the dialog's own `close` event. That event fires no
  matter *how* the dialog closed (×, Cancelar, Escape, or a successful save that calls `close()`
  itself once done), so a caller's own submit handler never resets the form itself.
- A click on the backdrop does not close a dialog, on purpose: a click outside by mistake would
  lose what was typed.

#### Opening a dialog

Opening one is always page-specific and left to the caller, through `openModal(dialog)` (same file:
`showModal()` plus restoring the page's scroll position, which browsers otherwise reset to the top).
There are two ways to call it:

- a static "+ Nueva X" button calling it directly;
- for a dialog shared by every row of a list rather than one pre-rendered per row (`accounting.js`'s
  own account edit, `account.js`'s transaction edit, three of `member.js`'s timeline dialogs), an
  `open*Dialog(record)` function that fills the shared dialog's fields from that one record first
  (`member.js`'s are `openStatusDialog`, `openEditBoardDialog` and `openEditLicenseDialog`).

The platform itself is what actually makes the rest of the page unreachable while one is open (real
modality, not a hand-rolled overlay to keep in sync) and closes it on Escape. Nothing in `modal.js`
or any page's own script needs to implement either.

### Errors do not go to a banner

`showError` (`api.js`) opens a modal `<dialog id="error-dialog">`, created on demand, titled
"Ha ocurrido un error", with the message and a "Cerrar" button.

- It is in the top layer over everything, even another open dialog, behind a dark backdrop, so the
  rest of the page can't be operated until it is closed (the button or Escape).
- A second error while it's open replaces the message.
- `showFeedback` is still the sticky `#feedback` banner for successes.
- Pages no longer have an `#error` element.

## Pages that do not exist, and inline forms

### Something that does not exist

A page about one thing answers the same way as an address that is not a page. The pages are
`/actividad?id=…`, `/cuenta?id=…`, `/socio?login=…`, `/reparto?id=…` and its `-pedidos`, `-numeros`
and `-pagos`, `/archivo?path=…` or `?view=…`.

`showNotFound()` (`api.js`) hides the page's own content and puts "Esta página no existe" in its
place, in the usual shell: no redirect and no modal.

It happens in two cases:

- The address has no usable id. `queryId()` returns a positive whole number, else null, and
  `stopAsNotFound()` also keeps the rest of the page's script from running.
- The API says the thing is not there (`isNotFound(err)`: `not_found` or `invalid_id`). The archive
  also treats `invalid_path`, `not_a_directory` and `not_a_file` that way.

After that `showError` stays silent, since the page's other requests fail for the same reason.

#### Exceptions

- The archive's `archive_unavailable` (its root directory is gone on the server):
  `showUnavailable()` puts "Ha fallado algo en el servidor" in the same place, so that a
  problem of the server does not look like a wrong address.
- Any other failure (a 500, no network) still opens the error modal.
- `login.html`'s own inline messages next to the button they answer are the exception to the
  modal.

### Inline forms for a person's own data

`perfil.html`'s own "Mis datos" and `member.js`'s own "Datos" are both left as a plain inline form
instead, not a dialog. A person's own core data is prominent enough on both pages (the entire reason
`/perfil` exists at all; the very first thing an admin or board looks at on `/socio`) that hiding it
behind an "Editar" button added friction for no real gain in clarity. Plain `<input>`s directly on
the page double as the read-only view too, so there's no separate view-only rendering to keep in
sync with whatever the form itself holds.

#### Cancelar

Populated once by `loadProfile()`/`loadMember()` and never reset to blank the way a "create new X"
dialog's own form is on close, both add a "Cancelar" button next to "Guardar cambios". It puts back
exactly what was last loaded (a plain local re-populate, not a re-fetch). This is the one bit of a
modal's own behavior (`initModal`'s reset-on-close) an inline form doesn't get for free, since
there's no `close` event here to hang it from.

#### Read-only for non-admins

`member.js`'s own version disables every field instead (not just the identity ones `profile.js`
restricts) for anyone who isn't a real admin. Board can still see a person's full data this way,
just not submit any change to it. It is the same read-only-but-visible treatment `profile.js`
already gives a plain member's own name/DNI/birth date/gender.

#### Changed fields are tinted

Since those forms stay on the page, the fields that hold something other than what was last loaded
are tinted, so that the person sees what they have touched before saving. In `js/form-dirty.js`,
`trackDirtyFields` is called once per form and `markFieldsClean` after each population, which keeps
the loaded value in `data-baseline`.

## Impersonation and role override

### Impersonation banner

The impersonation banner (`renderImpersonationBanner`) shows "Tiempo restante: 42 min; después
volverás a tu sesión" and counts it down (minutes, then seconds in the last one).

- The time comes from `impersonation_seconds_left` in `/auth/me`. It is a duration counted from when
  the answer arrived, not a moment compared with this browser's clock, so a wrong clock can't make
  it reload early or over and over.
- When it reaches zero it reloads the page, which is what sends the first request after the end (a
  401) and puts the admin back (below).
- It waits half a second more than the API's answer, which is rounded up, so that the reload does
  find the session ended.

### Impersonation (`js/api.js`)

`startImpersonation` swaps the admin's token for the impersonation's and keeps the admin's own in
`localStorage` (`katiuskas:original-token`; one started from inside another keeps the first
admin's).

`restoreOriginalSession` puts it back, if it still works (asking `/auth/me` with it, which also
renews it and its cookie). It is used by:

- the "Salir de la impersonación" button, which ends the impersonation session on the server, and
  falls back to `POST /auth/impersonate/exit` if there is nothing kept;
- `apiFetch` on any 401 while one is kept, which is what the end of the hour (or a revocation)
  looks like: it reloads `/socios` as the admin, with the note "La impersonación ha terminado y has
  vuelto a tu sesión." (a `sessionStorage` flash shown by `initSession`).

Any other sign-in (`setToken`) forgets the kept token, so it can't be restored later by mistake, and
signing out while impersonating ends the admin's own session too.

### "Entrar como esta persona"

The "Entrar como esta persona" button of `/socio` (`member.js`) is only shown, to admins, for
somebody with at least one of `admin`, `board` or `member`. With none of them the person cannot sign
in and the API refuses to impersonate them (409 `not_eligible`). The button follows the person's
roles (`renderRoles`), which can change on that page.

### "Ver como…"

"Ver como…" (`js/role-override.js`, wired by `initPage`):

- Admins have a link in the topbar (removed for anybody else by `data-roles`, and for an
  impersonation) that opens a dialog with the roles from `GET /roles` as checkboxes.
- The dialog also has buttons for the usual combinations (Socio, Socio con material, Socio con
  biblioteca, Socio con money, Junta, Administrador).
- It sends the roles with `PUT /auth/role-override`; then the page reloads as somebody with those
  roles (the topbar link and the admin-only pages go away if `admin` is not among them).

While it is in force every page shows a banner (`renderRoleOverrideBanner` in `api.js`, from
`real_roles` in `GET /auth/me`) with:

- the roles;
- the time left, counted down by the same `countDownTimeLeft` as an impersonation, reloading at
  zero;
- "Cambiar roles" (the same dialog, through a `katiuskas:role-override` event);
- "Volver a mis roles" (`DELETE /auth/role-override`, then reload).

## The shared client and the login page

### `js/api.js`, the shared client

`js/api.js` is the underlying shared client `layout.js` itself is built on. It keeps the session
token in `localStorage`, and `initSession()` calls `GET /auth/me` on every page load to validate the
token and populate `currentUser()`.

### Role-dependent visibility

`applyRoleVisibility()` removes any `data-roles="admin,board"` element the current user's roles
don't satisfy. This is client-side UI hiding only; the API is the actual authority.

A permission that depends on data loaded asynchronously for one specific record (e.g. "is the
viewer this activity's organizer?") can't be a static `data-roles` attribute, since
`applyRoleVisibility()` only runs once at page load. That case computes a plain JS boolean after the
data arrives and toggles `.hidden` directly instead (see `activity.js`'s
`canManage`/`canJoinSelf`/`canAddOthers`).

### Detail pages and links to people

- Detail pages read a login from the query string (`/socio?login=<login>`,
  `new URLSearchParams(window.location.search)`), the same pattern `login.html` uses for the
  magic-link token from an emailed URL.
- A link to a person's page (`/socio`, open only to admin and board) is drawn only for them.
  Anywhere a member, `material` or `money` can see a person (the participants of an activity, the
  holders of a numbered article), the name is plain text, since the link would only take them back
  to the home page.

### Login page

#### The login code

The login code has no URL of its own to arrive in: `login.html`'s second panel gets it typed in by
hand. So the email/phone from the first step are instead remembered in a plain JS variable across
that two-step flow.

#### Local address notice

The page is the same on every address except one thing. On a `local.…` address, `auth.js` applies
the same rule to `location.hostname` and shows `#local-notice` above the panels.

- The club's tablet is given `local.` + the public address, and the API shortens the session there
  (`docs/architecture.md`, "Short sessions").
- The notice says "Sesión local", in the accent color, saying that the address is for shared use and
  the session will close by itself in a few hours (no number, so it can't disagree with
  `LocalTokenTTL`).
- Everywhere else it stays hidden.

#### When the second panel appears

That second panel appears only when the API accepts the details:

- For a pair that matches nobody it answers 404 `unknown_details`. The first panel says so ("Ese
  correo y ese teléfono no corresponden a nadie con acceso…") and asks to check them and, if they
  are right, to contact the Junta Directiva.
- For a person with no role that gives access it answers 403 `no_access`. The panel says "No tienes
  acceso a la Intranet: solo pueden entrar los socios actuales…" (the text of `error-texts.js`),
  without asking to check what was typed.
- Otherwise the second panel says an email with a link and a code will arrive.

#### Signing in from another tab

The link from that email is usually opened in another window or tab, which signs in there while this
one keeps asking. `auth.js`'s `watchForSignInElsewhere` therefore:

- listens for the `storage` event on the `token` key (and for the tab becoming visible again, for a
  browser that doesn't deliver it);
- when a stored token really is valid (`GET /auth/me`, as at load), sends this window to `/` already
  signed in;
- changes nothing for a stale or invalid token.

#### Telegram

`login.html` also offers Telegram as a second way in, alongside (never instead of) the email/phone
form.

- On load, `auth.js`'s `loadTelegramBotInfo` calls the unauthenticated `GET /auth/telegram-bot`
  (`authapi.Handler.TelegramBotInfo`).
- It is backed by `config.TelegramBotUsername`/`TELEGRAM_BOT_USERNAME`, independent of
  `TELEGRAM_BOT_TOKEN`/`TELEGRAM_LOG_CHAT_ID`. It is not secret the way that token is, since it's
  only the public "@name" a `t.me/<username>` link needs.
- Only if a username comes back non-empty, it un-hides a note linking straight to the bot's chat.
- The note is left hidden by default so there's no dead link (or flash of one) when Telegram isn't
  configured at all, or the request itself fails.

## Archive and file browsers

### Downloading and previewing

`archive.js` itself doesn't lean on the session cookie described above (`dashboard.js`'s own simpler
Documentos/Actas/Memorias links do instead). Downloading or previewing a document from the
`/archivo` browsing UI fetches the file with `apiFetchBlob` (`api.js`) and works with the resulting
`Blob` client-side: an object URL for inline preview, a synthetic `<a download>` click to save.

A `Blob` is what lets it render Markdown inline and drive the download from a click handler rather
than a bare link, not because the route itself needs the bearer header specifically.

### Rendering documents

- Markdown documents (and the texts of an activity's memoria) render through a small
  dependency-free converter (`js/markdown.js`).
- That converter resolves each document-relative link against the document's own directory and
  rewrites it into an in-app `/archivo` navigation link.
- It only makes an anchor of a target that `safeHref` (`js/rich-text.js`) accepts (http, https,
  mailto or a path).
- HTML documents render in a fully sandboxed `<iframe>` instead, with no such link rewriting.

### Shared file-browser code

`archive.js` and `user-files.js` (the embedded per-person file browser on `/perfil`/`/socio`) share
this same listing/preview/download logic almost verbatim. `js/file-browser.js` pulls out the common
pieces both import, rather than each carrying its own near-identical copy:

- `joinPath`/`dirOf`/`nameOf`;
- `formatSize`;
- `fileKind`;
- the same document-relative link resolver;
- `downloadBlob`.

`activity.js`'s own memoria-resource downloads import just `formatSize`/`downloadBlob` from there
too, the only two of these it also needed.

## Person pages: sessions and history

### "Mis sesiones" on `/perfil`

`/perfil` also has a "Mis sesiones" section (`initSessionsPanel` in `js/sessions-panel.js`, which
`/socio` shares, see below). It lists the person's sessions not expired or revoked (`GET
/users/{login}/tokens?status=active`), each with:

- its browser and system, last use and address;
- when it was opened and when it expires;
- "Esta sesión" on the one in use;
- if an administrator entered as them, who.

For an administrator impersonating the person, the section shows the person's sessions with a note,
and no "Cerrar" buttons, since the API doesn't let sessions be ended through an impersonation.

#### Sorting and closing

- Its Último uso, Abierta and Caduca headers sort them (the same helper, here in the page itself
  since they are all loaded; a session never used counts as the oldest use), most recently used
  first to begin with.
- Each has a "Cerrar" button (`PATCH .../tokens/{id}` with `revoked: true`, after a confirmation).
- Closing the one in use is signing out, so it also clears the token and goes to `/login`.

### "Sesiones" on `/socio`

`/socio` has the same panel as "Sesiones" for an admin only. The section carries
`data-roles="admin"`, as the API gives anybody's sessions to nobody else.

- It shows the sessions of the person whose page it is, with the same columns, sorting and "Cerrar"
  buttons (an admin can end anybody's).
- It is worded for a third person, and on their own page, the admin's own.

### Activities and places history

`perfil.html`/`socio.html` both have, after the history as a member, two tables that tell what the
person has done in the club (`js/person-history.js`, `initPersonHistory(login, { own })`):

- "Actividades realizadas" (`GET /users/{login}/activities`): start and end, title as a link, type,
  state and whether they organized it or took part.
- "Lugares visitados" (`GET /users/{login}/places`): the place as a link, what it is, where, how
  many times and the date of the last one.

Only the words of the empty message change between somebody else's page and "Mi perfil".

## License copies on person pages

### The "Licencias federativas" section

`perfil.html`/`socio.html` each also render a "Licencias federativas" section: a year `<select>`,
mirroring `dashboard.js`'s own Actas asambleas, plus a grid of small clickable thumbnails rather
than a plain list of text links. It is used instead of pointing at that person's `licencias/`
subdirectory (see [Dropping files
in](backend-internals.md#dropping-files-into-the-archive-and-the-per-person-directories)) through
`user-files.js`'s generic browser.

`js/license-files.js`'s `initLicenseFiles(login, opts)`:

1. fetches that one directory's listing (`GET /users/{login}/files?path=licencias`);
2. builds the `<select>` from whatever years actually have a license on file (ascending, most recent
   selected by default; nothing conjures up an empty year, the way `loadActas` doesn't either);
3. renders each of that year's files as a `licenseCard`.

### Each `licenseCard`

- It is a thumbnail linking straight at `GET /users/{login}/files/file/licencias/<name>`. This is a
  bare `<img>`/`<a href>`, the same cookie-authenticated plain-link shape `dashboard.js`'s own
  `fileLink` already uses for Documentos/Actas/Memorias, since this route accepts the session cookie
  too (see `internal/userfiles` above), so there's no need for `apiFetchBlob`'s Blob dance.
- Underneath is a "DD-MM-YYYY — Tipo de licencia" label. It is parsed the same way `dashboard.js`'s
  own `parseActaName`/`actaYear`/`formatActaDate` read `yyyy-mm-dd(-suffix).pdf` back out of an
  acta's filename.
- Within a year, files sort by `(month, day, tipo_de_licencia)` rather than trusting the raw
  filename string, the same reasoning `loadActas`' own `renderSelectedYear` sorts by
  `(month, day, suffix)`.
- A file whose name doesn't match that convention has no year to file it under, so, the same as an
  acta named anything else, it's simply left out.

### Thumbnail files

The thumbnail itself is a companion file at `licencias/.thumbnails/<full original filename>.jpg`
(any original extension, not just a PDF's).

- It is in a dotfile-prefixed subdirectory, invisible to the plain "licencias" listing this module
  reads (`userfiles.Service.ListDirectory` skips any entry, file or directory, starting with ".").
- It is the same "invisible companion file, fetched by exact path" trick (the archive's own
  `.order.json` is a dotfile too, though the API reads that one itself).
- Nothing in the running app generates one. `scripts/generate-license-thumbnails.sh` does, on
  demand, for every license copy that lacks one (see **Commands**). One dropped in by hand next to
  the license's own file works the same.
- It is meant to be 280px on its longest side, twice `.license-thumbnail`'s own 140px display size
  in `style.css`, so it still looks sharp on a high-DPI screen.

### Thumbnail fallbacks

`licenseCard`'s `<img data-fallback-src>` tries that thumbnail first. `wireThumbnailFallbacks`
catches a load failure (the common case until the script has been run) and then:

1. falls back to the license's own file directly, only if that file is itself an image (`fileKind`,
   `file-browser.js`): there is nothing to generate a thumbnail for when the real file already is a
   small, displayable image;
2. failing that too (a PDF or anything else with no thumbnail dropped in for it yet), swaps in a
   plain 📄 placeholder of the same size rather than the browser's own broken-image icon, so the grid
   stays visually consistent whether or not every license has a real thumbnail yet.

### No license copies yet

A missing `licencias/` subdirectory 404s the same as any other unknown one
(`userfiles.Service.ListDirectory`). It is caught and shown as "no hay copias" rather than an
error: nobody's dropped a license copy in yet is the common case, not a real failure.

- `profile.js` passes `hideWhenEmpty: true` (mirroring `initUserFilesBrowser`'s own option) so "Mi
  perfil" doesn't show an empty section for the common case of no license on file yet.
- `member.js` leaves it on by default (always shown) so admin/board reviewing someone else's page
  can tell "no license copies yet" apart from a page that failed to load anything at all.

### Styles

`.info-card`/`.info-card label`/`.year-select` (the bordered-card-with-a-year-filter look Actas
asambleas already had) live in the shared `style.css` rather than the page-specific `dashboard.css`
precisely because these two pages need them too now.

- What opens a card, a list or a paragraph, has no top margin of its own, since the card's padding
  is already there: `.info-card > :first-child`.
- `.license-thumbnail-grid`/`.license-thumbnail-card`/`.license-thumbnail`/
  `.license-thumbnail-placeholder`/`.license-thumbnail-label` are this section's own, in `style.css`
  as well since `dashboard.css` stays index.html-specific.

## Distribution pages: the members' view

`/repartos`, `/reparto?id=…` and `/reparto-gestion?id=…` are the interface for **Distributions**
above. The scripts are `distributions.js`, `distribution.js` and `distribution-manage.js`, with its
sections `distribution-orders.js`, `distribution-payments.js` and `distribution-numbers.js`, sharing
`js/distribution-common.js`.

What the code and the API call a unit is a "pedido" to the people reading these pages, and an extra
unit an "extra", since "unidad" meant nothing to them. The pages are behind one "Repartos" sidebar
entry (`layout.js` maps the two detail pages to it in `ACTIVE_ALIASES`).

### The list (`/repartos`)

- Columns: when it was created (as "Enero de 2026" with `formatMonthYear`), the name, the state and
  the price.
- The state has the same four names as everywhere: "Borrador", "Se puede pedir", "Activo" and
  "Terminado" for `draft`, `open`, `closed` and `done`, each with its tooltip. A row with the
  "Puedes pedirlo" badge says "Activo" in the state column, so as not to repeat it.
- It shows "Puedes pedirlo" on the open ones whose `eligible` is true.
- It opens a new distribution from a dialog (admin/board). The options go in a textarea, one per
  line, "Talla: S, M, L" (`parseOptions`).
- It marks a distribution only some people can see with a "Restringido" badge.

### Who sees a distribution

- The page's description line says who ("lo ven: …").
- The create and edit dialogs have a "Quién lo ve" select (`visibility`) that carries
  `data-roles="admin"`, so only an administrator has it.
- `readDistributionForm` sends it only when it is there.

### The detail page (`/reparto?id=…`)

The detail page renders the conditions and, except while it is a draft, a "Pedir" form. While it is
a draft, `#ask-sections` is hidden and the orders are loaded only once it leaves the draft.

#### The "Pedir" form

- An "A nombre de" picker over `GET /users/directory`.
- One `<select>` per option.
- An extra checkbox when `extra_price` exists.
- In a distribution for all members with no options, the own order is already there and only extras
  are left. Then there is no checkbox, the request always goes as an extra, the button says "Pedido
  extra" (`extrasOnly`) and the subtitle gives the price, "Puedes pedir también extras a 8,00 €".
- `canOrderNow` repeats the API's own rule of when (open and before the deadline; admin, board and
  material at any time but in a draft, with a note saying that the distribution is not open) only to
  decide whether to show the form.

#### "Tus pedidos"

"Tus pedidos" lists the units where the signed-in person is the beneficiary or entered them, since
`GET .../units` returns everything to admin/board/material. It shows who entered each when that
isn't the person it is for.

In a distribution for all members:

- The member's own unit is already in that table: "Automático", or "Sin elegir" with a primary
  "Elegir" button until somebody chooses.
- There is no "Quitar", since the API refuses it.
- The whole "Pedir" section (`#order-section`, not just the form, so not even a "no longer admitting
  orders") is out when there is neither an option nor extras to ask for, `nothingToAsk`.

#### Table columns

The table's columns (`unitColumns`) are only the ones that mean something:

- "Elegido" only with options.
- "Tipo" (own or extra) only when extras exist.
- Nothing about payment for now: the state is "Recogido" or "No recogido". The price (the unit's
  own, frozen) is shown in a "Precio" column, but nothing about paying (no state, no amount owed):
  the intranet doesn't keep that.

#### Managing

Nothing to manage the distribution is on this page. Whoever does (admin, board and material) has one
"⚙ Gestionar este reparto" button (`#manage-link`, `data-roles="admin,board,material"`, so nobody
else even has it) that goes to the management page.

## Distribution pages: the management page

### Layout of `/reparto-gestion?id=…`

`/reparto-gestion?id=…` (`distribution-manage.js`; `requireAnyRole` admin/board/material) is the one
page to manage a distribution.

#### Buttons on the distribution

At the top, after the title, the state and the description, are the buttons that act on the
distribution itself:

- "Volver a …" / "Pasar a …": one step at a time, except that a distribution with nothing for the
  members to ask skips "Se puede pedir" (`neighbours`). `bodyFrom` rebuilds the whole body, as `PUT`
  replaces every field.
- Going back to a draft, and deleting, ask for confirmation naming what would really be lost: the
  orders that are not an empty own order of a member in a distribution for all members, which is
  only a placeholder made again on leaving the draft (`ordersWithSomethingToLose`). They say nothing
  about orders when there are none of those.
- "Editar" (the dialog, with its form): all of these are for admin and board.
- "Venta directa" (admin and board, only in a distribution with articles that is not a draft:
  `distribution-sale.js`, below).
- "Borrar" (admin only: `DELETE` is admin-only in the API, and the button is `data-roles="admin"`).
- A link "← Volver al reparto" goes back to the member's view.

#### Sections

Under the buttons, a row of links (`.section-index`) to its sections, which are what used to be
three pages:

- **Pedidos**.
- **Pagos** (admin/board, and only when something has a price).
- **Números** (only when some article has its numbers described; its link in the index says
  "Números").

Details of the sections:

- All their headings are `<h2>`, and a section has none of its own where it would only repeat the
  link.
- The orders have "Resumen de lo pedido" and "Pedidos de los socios".
- The numbering has one block per article titled "Números - <article>".
- The page hides the section and its link otherwise (`renderHeader`), also after an edit that
  changes it.

#### Restricted controls

Every control restricted by `data-roles` is *removed* from the page for anybody else. So each script
reaches those through a null-safe lookup (a bare `getElementById(...).addEventListener` on one of
them would throw before the rest of the page ran), and the payments section is only started for
admin/board.

### How the sections are wired

The page reads the distribution once and gives it to the sections, each of which is a module with an
`init…` function that returns `reload`.

- Changing the state or editing re-reads them all (`distributionChanged`).
- What is done in the orders (paying, handing out, numbering) reloads the payments and the numbers
  (`onOrdersChanged`).
- The orders section also tells the page when it read a fresher distribution (what is left of each
  article changes with the orders), so the header is drawn again.

### The Pedidos section

The **Pedidos** section is everything about the orders in one place:

- on top `GET .../order` (what has to be bought);
- below it a list with one line per order (the ones with everything paid and handed out are hidden
  unless asked for);
- a summary: what, how many are handed out and paid, what it costs.

#### Unfolding an order

Clicking the line (or Enter or Space on it, `toggleOrder`) unfolds its detail (`detail`, kept in
`unfolded`, in a row of its own), with no button for it. The line is `tabindex="0"` with
`aria-expanded`, styled like a line of the ledger of `/cuenta`: `.order-row`.

For an article with numbers (`isNumbered`), the detail shows how many are without one:

- With "Asignar los siguientes libres" and, for an article whose numbers are described, "Elegir
  números…", which opens `numbers-dialog`.
- For an article with free-text numbers, a field to type them.

It then shows the list of the article's numbered items, each with its paid / delivered state, to
tick and act on.

#### The numbers picker

`numbers-dialog` has:

- a button for each free number, in a row by the first part of the number when it has several (a
  series and its fractions), pressed to mark and unmark;
- Shift to mark from the last one pressed to this one;
- a filter box;
- never more marked than the order is missing (the rest are disabled);
- the free numbers read again if the API refuses one because somebody took it meanwhile.

All of it is `createNumberPicker` in `number-picker.js`, which the sale over the counter uses too.

#### Ticking and acting on numbers

Items are ticked (`ticked`; "todos / sin pagar / sin entregar…" tick by state, `pick`) and acted on:

- mark paid or take the payment off (admin/board);
- hand out to the person asked in a dialog (`deliver-dialog`, "Entregado a", prefilled with the
  member the order is for; only members are offered);
- undo a delivery;
- return;
- take the numbers off.

These actions are a line "Con lo marcado" (`markedActions`) that is there only while something is
ticked. It has only the buttons that would do something to what is ticked (`applicable`):

- "Marcar pagados" if some ticked number is unpaid.
- "Quitar pago" if some is paid.
- "Entregar" if some is not handed out.
- "Deshacer entrega" if some is.
- "Devolver" if some is handed out and unpaid.
- "Quitar números" if some is neither.

Each acts only on the ticked numbers it applies to, and the line is drawn again on each tick.

#### Whole-order actions

- The line "Pedido:" has "Pagar todo (importe)" and, while something can be handed out
  (`deliverable`), "Entregar todo", which asks in the same dialog who collects it.
- Elsewhere a count field (`quantityOf`) does the same as the ticks.

#### Paying and the account

- Paying and taking a payment off first ask, in `pay-dialog`, for the asset account (`withAccount`:
  the non-hidden `asset` ones of `GET /accounts`, the last chosen remembered in `localStorage`;
  skipped when the order costs nothing) and send it as `account_id`.
- The create/edit dialogs of a distribution have a "Cuenta de los pagos" select
  (`loadAccountOptions`; empty is the default account, named in its first option).

#### Changing orders and confirmations

- Changing or removing an order (`canEditOrder`) is offered at any time but in a draft, as the API
  allows.
- Everything goes through the single `perform(act, unit)`, confirming before handing over unpaid
  units, undoing, returning or taking numbers off.
- What is unfolded, ticked and typed survives redrawing.

### Numbers: order and ranges

Wherever the frontend lists numbers they are sorted (`sortIdentifiers`: part by part, digits as
numbers) and joined into ranges (`formatIdentifiers`; `compactNumbers` for a bare list such as the
numbers an error message or an entry of the list of payments names).

The Telegram messages about numbers say them the same way. `formatNumbers`/`compareNumbers` in
`numbering.go` are a Go copy of those two that must keep giving the same order and ranges. Each
message says what it changed (numbers added, taken off or newly assigned), not what was typed.

### Who the pickers offer

Both pickers ("A nombre de", "Entregado a") offer only members, never everybody on file.
`loadPeople(id, purpose)` takes the entries from `GET /distributions/{id}/people`:

- `for=order`: the members on the cutoff date (today's when there is none);
- `for=pickup`: also today's members.

Only the names shown for who did what come from `/users/directory`. The API enforces the same
(`not_eligible`, `invalid_collector`); the pickers just don't offer what would be refused.

## API errors in the frontend

`api.js`'s `apiFetch` puts the API's error `code` on the thrown `Error`, and sets its message to the
Spanish text for that code from `js/error-texts.js`.

- That file is the one catalog of every code the API can answer with.
- A code's entry is a function for the few whose message carries something to show, such as the
  names of the accounts a budget is missing, or the two totals that do not match.
- For a code it does not know, it falls back to the API's own text.
- `apiFetchForm` and `apiFetchBlob` do the same.
- A request that never reaches the server says so in Spanish instead of the browser's "Failed to
  fetch".
- A page that wants something more specific for a code still reads `err.code`
  (`distribution-common.js`'s `apiErrorText`, `account-tree.js`'s `accountingErrorText`).
- A new error code in the API needs its entry in that catalog.

## Accounting page (`/contabilidad`)

### The chart of accounts

`/contabilidad` (`accounting.js`) manages the chart of accounts from `GET`/`POST`/`PUT /accounts`
and `POST /accounts/{id}/move`. It is a tree, indented by depth, of every account's name, direct
balance, and last movement date. Each name is a plain `<a href="/cuenta?id=…">` into that one
account's own detail page, below.

There is deliberately no code column, nor codes in the parent pickers: a code only shows on that
account's own `/cuenta` page, though the create/edit dialogs still have the field to set it.

### Controls

The page has the controls to:

- create an account ("+ Nueva cuenta" dialog);
- correct one's name/code/parent (one shared edit dialog, `openEditAccountDialog`);
- swap it up or down among its own siblings.

### Checkboxes and badges

Both dialogs carry checkboxes: "Para cuotas anuales", "Para licencias", the default account of the
distributions, "Alquileres de material", "Ventas de material" and "Oculta"
(`allows_dues`/`allows_licenses`/`distribution_default`/`material_rental`/`material_sale`/`hidden`).

- They are styled by `.checkbox-label` in `style.css` so the box sits next to its text instead of
  being stretched by `.field input`'s `width: 100%`.
- They are always sent explicitly: unchecked is `false`, not omitted, since `PUT` replaces every
  field.
- Each row shows a small "Cuotas"/"Licencias"/"Repartos"/"Alquiler de material"/"Venta de
  material"/"Oculta" badge next to the account's name for whichever are on, so which accounts a
  transaction can record those against is visible without opening each one.

### Tree helpers (`js/account-tree.js`)

`js/account-tree.js` holds the `childrenByParent`/`flattenAccountTree` pair both this page and
`account.js` need.

`GET /accounts` already comes back as a pre-order, depth-first walk (see the `accounts` bullet under
[Accounting](backend-internals.md#accounting)). So filtering that flat list down to a single
`parent_id`, in the order it already arrives in, *is* that group's own sibling order. One pass
groups every `parent_id` at once, with no separate tree-building pass.

### Hidden accounts

- Hidden accounts (`hidden`) are left out of the table, and of the parent pickers, unless a "Mostrar
  cuentas ocultas (N)" checkbox is on. It is shown only when there are any, and it is a view choice,
  never persisted.
- Each is tagged "Oculta" and dimmed when shown.
- `shownAccounts` (`account-tree.js`) drops them while re-parenting their shown children onto the
  nearest shown ancestor, since filtering the flat list alone would lose those children from the
  tree walk.
- The ↑/↓ arrows follow the real sibling groups but count only shown siblings. A move repeats once
  per hidden sibling it has to pass (the endpoint swaps with the very next one, which would
  otherwise look like nothing happened).
- A hidden account stays offered as the parent picker's choice when it is the current parent,
  since dropping it would move the account to the top level on save.
- A `move`'s own response (the whole reordered list) re-renders directly from `applyAccountsList`
  rather than triggering a follow-up `GET`.

### Who sees and writes

Only admin, board and the optional "money" role ever see `/contabilidad` in the sidebar at all, or
can even land on the page (`requireAnyRole`, else redirected to `/`). That is tighter than the API
itself, which opens *reading* the chart to every baseline role the same as `GET /transactions`.

Of those, only admin and money write (`canWrite`, `hasAnyRole(user, ['admin', 'money'])`). Board
reads everything, but the page leaves out "+ Nueva cuenta" and each row's ↑ ↓ and "Editar" for it
(the API is the authority and would refuse them anyway).

## Accounting pages

### Files attached to transactions

- On `/cuenta` a transaction that has files shows a 📎 (with the number when there are several)
  after its description (`file_count` of the ledger listing).
- Opening a row also lists them, as links to `/api/v1/transactions/{id}/files/{fileId}/file` (the
  session cookie makes a plain link work).
- The new-transaction dialog has a multiple file input. The edit one lists the transaction's files,
  each with a "Quitar" that deletes it at once (after a confirmation), and the same input.
- The files chosen are uploaded one by one once the transaction is saved (`uploadTransactionFiles`
  in `js/transaction-files.js`, which the pages share), and one that fails is reported with the
  rest of what could not be registered.
- Deleting a transaction that still has files says so (`transaction_has_files`).

#### The overview page `/justificantes`

- `/justificantes` (`transaction-files-page.js`) is for admin, board and money.
- It is reached with the "Justificantes" button in the header of `/contabilidad`, not from the
  sidebar, where it counts as part of Contabilidad for the highlighting.
- It is the overview: every file from `GET /transaction-files` with its transaction's date,
  description, amount and accounts (each with its sign, linking to the account's ledger), a year
  filter and "Ver más".

### The yearly budget (`/presupuestos`)

`/presupuestos` (`budgets.js`) is the yearly budget, in the sidebar for every baseline role.

#### Year selector and display

- A year selector offers the years that have a budget from `GET /budgets`, plus this year and the
  next. "Otro año" (admin and money only) opens any year from 1900 to 2200, to look at it or to
  create its budget.
- The selector loads `GET /budgets/{year}` and draws, for income and for expenses, a table with
  each line item's name and accounts, the amount budgeted, what was really received or spent, and
  the percentage reached (a bar and the figure, red beyond 100 %).
- The totals of both parts also appear as two cards at the top.
- Accounts the saved budget does not cover are named in a notice.
- A year without a budget says so.

#### Buttons

- Only admin and money (the API is the authority) get the buttons: "Editar presupuesto" and
  "Borrar presupuesto" (`DELETE`, after a confirmation).
- For a year without a budget they get "Crear presupuesto", which opens the same dialog starting
  from the closest earlier year's items (or the later one's).

#### The edit dialog

- The dialog keeps a working copy of both parts. Each item has a name, an amount and its accounts
  as removable chips.
- A picker adds one account (leaves of the part's type not yet used in the budget) or "Todas las de
  «group»".
- As it is edited, the dialog shows both sums, the difference when they do not match, and the
  accounts still without an item.
- It saves everything with one `PUT /budgets/{year}`, whose error messages (`accounts_missing`,
  `unbalanced`...) come from the API in a modal.

### One account's ledger (`/cuenta?id=…`)

`/cuenta?id=…` (`account.js`) is one account's own ledger.

#### Access and navigation

- It is reached only by clicking an account's name on `/contabilidad`, never linked from the
  sidebar itself, so it shares that same admin/board/money gate.
- Board gets no "+ Nueva transacción", "Editar" or "Borrar" there (it can open a row's detail and
  the files attached).
- `js/layout.js`'s `ACTIVE_ALIASES` maps it back to `/contabilidad` for the sidebar's own
  highlighting, the same `/actividad`→`/actividades`/ `/socio`→`/socios` treatment every other
  detail-off-a-listing page gets.

#### Group view

- For a group (`isGroupView`) the page shows its descendants' entries, with a "Cuenta" column
  between Fecha and Descripción linking to each one's page, where the transaction is corrected.
- There is no Editar and there are no new transactions in a group.
- The header balance, like in `/contabilidad`, is the sum of all its descendants' (it comes rolled
  up from `GET /accounts`).
- A row's detail lists the other accounts relative to the account the row is on.

#### The ledger table

- It renders `GET /accounts/{id}/transactions` (see
  [Accounting](backend-internals.md#accounting)) as a table: date, description, this account's own
  entry amount, its running balance after, and per-row Editar/Borrar.
- A row also opens on a click anywhere but those buttons, or Enter/Space when focused (see below).
- The listing is paginated (`loadEntries`): the newest 50 (`ENTRIES_PAGE_SIZE`) first, and "Ver
  más" for the next ones. It asks for one entry more than a page just to know whether to show "Ver
  más".
- Above the table are a "Buscar en la descripción" box with a Desde/Hasta filter (the
  `limit`/`offset`/`from`/`to`/`q` query parameters of that route, applied with "Filtrar").

#### Row detail

- Clicking a ledger row (`tr.entry-row`, `toggleEntryDetail`) expands a detail row right below it.
  It starts under "Descripción" (an empty first cell), with no line between the two and the same
  background, so it reads as the continuation of the row above rather than as another row.
- It lists the transaction's *other* accounts: each name (just the name, no code — the code only
  appears in the page's own header) links to that account's own ledger, with the amount it gained
  (`+`) or lost (`−`; `signedCurrency`).
- The data is fetched with `GET /transactions/{id}` the first time (the listing only ever carries
  this account's own side, see `AccountEntry`) and resolved to names from `allAccounts`, which the
  page already has. It works for transactions of any number of entries.
- Under them it also lists the cuotas anuales and licencias the transaction paid for: each as the
  person's name (from `GET /users/directory`, which the page loads for everyone who can reach it,
  not just for the admin pickers) linking to `/socio`, plus year (and license type).
- These come from the `dues`/`licenses` that same response carries for admin/board only, so they
  simply don't appear for anyone else.
- Clicking the row again collapses it, a click on Editar/Borrar or on a link never toggles it, and
  any reload of the list starts every row collapsed.

#### The "+ Nueva transacción" dialog

- It is deliberately narrower than the full `POST /transactions` shape. It has a date, a
  description, and a growing list of *lines* (`setupEntryLines`), each an amount "en esta cuenta"
  plus the "otra cuenta" it comes from or goes to.
- As soon as the last line has both fields a new empty one appears below it.
- A line with neither is ignored, one with only one is an error, and the same account can't be on
  two lines.
- The account picker starts on a "— Elige una cuenta —" placeholder precisely so "filled" means
  something.
- A read-only "Total en esta cuenta" next to the date (`updateTotal`) shows the sum live, on every
  keystroke, so it counts a line whose account isn't picked yet and ignores an empty or non-numeric
  amount.
- Pressing "n" anywhere on the page opens the dialog (ignored while typing in a field, with a
  modifier held, or with a dialog already open), which puts focus on the date. The edit dialog puts
  it on the description.

#### How a save becomes entries

- On save (`entriesBody`, shared by creating a new transaction and correcting an existing one) this
  account gets one entry for the *sum* of every line's amount, added up in whole cents so floating
  point never shows up in the result.
- Each other account gets the exact negative of its own line, so the entries sum to zero without
  whoever is typing having to think about the balancing side.
- Amounts that cancel out (this account would get a zero entry) are refused.

#### Editar and Borrar

- A row's own "Editar" fetches that one transaction fresh (`GET /transactions/{id}`, never kept in
  memory alongside the ledger listing, which only ever carries this account's own side of each
  one) and turns each of its *other* entries into a line (amount negated back).
- If the transaction doesn't have exactly one entry on this account, or has no other entry (only
  possible by calling the API directly), editing is refused with an explanation instead of silently
  merging them.
- The dues/licenses sections follow every account picked on any line (`sectionsFor`), not just one.
- "Borrar" (`confirm`-gated, the same pattern `member.js`'s own delete buttons use) removes the
  whole transaction, every entry included.

#### Dates

- The date input only ever collects a plain date, but `occurred_at` is a real timestamp.
- It is sent as that date at noon UTC (not midnight) specifically so `formatDate`'s own
  local-timezone getters can never roll it back to the previous day for anyone viewing from behind
  UTC.
- Editing an existing transaction collapses whatever time of day it actually had down to this same
  convention.
- The "+ Nueva transacción" dialog opens with today's date (`todayAsInputValue`, built from local
  getters: `toISOString()` would be UTC's date, already tomorrow's for part of the evening east of
  it).

#### Dues and licenses sections

- Both dialogs also carry, for a real admin only (`data-roles="admin"`; POST dues/licenses is
  admin-only), a "Cuotas anuales a registrar" and a "Licencias nuevas a registrar" section
  (`setupDuesAndLicensesSection`, prefix `nt`/`et`).
- Each section is a list of pending chips sent as `POST /users/{login}/dues`/`licenses` once the
  transaction itself is saved.
- Each section is hidden (`hidden`) until it could actually succeed: `sectionsFor` shows dues if
  this account or any account picked on a line has `allows_dues`, licenses if any has
  `allows_licenses`. This is re-evaluated whenever a line's picker changes and whenever a dialog
  opens.
- A section that gets hidden also drops its pending chips: otherwise one added while a qualifying
  account was selected would still be sent, and only rejected by the API after the transaction was
  already saved.
- When neither section applies, a hint explains that an account has to be marked for it, in
  Contabilidad, instead of showing nothing.

##### What earlier saves registered

- Separately from that, the edit dialog lists what earlier saves already registered against the
  transaction, read-only (no ×: nothing removes a due). It reads them from the `dues`/`licenses`
  its own `GET /transactions/{id}` already returns (`setExisting`).
- A section stays visible for as long as it has something to list even when no account qualifies
  any more (the flag was since turned off, or the accounts changed): it is a record of what was
  paid, not something about to be sent.
- Its add controls follow the qualifying rule above.

##### Suggested year

- Each section's "Año" input starts from the transaction's date: that date's own year, or the next
  one in December, since a payment that late is almost always for the following year
  (`suggestedYear`).
- The inputs keep following the date until the person types in one, which is then left alone until
  the dialog is next opened (`editedYearInputs`, cleared by `reset`/`syncYears`).
- Adding a chip puts that input back on the suggestion, and the edit dialog does the same from its
  own date.

#### The "otra cuenta" picker

- Each line's "otra cuenta" picker (the same list in the create and edit dialogs) leaves hidden
  accounts out, except those the transaction being edited already uses, which stay choosable
  (`otherAccountOptions`'s `keepHidden`).
- It lists every other account by name only, no code (indented the same way `accounting.js`'s own
  pickers are, via `flattenAccountTree`).
- It excludes only this account itself, not its ordinary parent/children the way `accounting.js`'s
  own "cuenta superior" picker excludes descendants: there's no circular relationship to guard
  against here, any two distinct accounts can be on the two sides of a transaction.

#### Page header data

The page's own header (name, code, current balance) and that picker both come from one
`GET /accounts` call. That listing already carries every account's own balance, so there's no need
for a second, single-account fetch just for the header (`GET /accounts/{id}` alone never populates
it — see [Accounting](backend-internals.md#accounting)).

## Members and admin pages

### The members list (`/socios`)

#### Overview

- `/socios` (`members.js`) is a read-only, sortable table of people; every action about a person
  lives on their own `/socio?login=<login>` page, which a row opens for admin/board only.
- "+ Nueva persona" is admin-only, in the markup and in `members.js`, which loads the role catalog
  (for that dialog's role checkboxes) only for an admin.
- The list of roles itself, to create or delete one, is no longer here: it lives on `/admin` (see
  below).
- Admin/board get `FULL_COLUMNS` and `FULL_FILTERS`; a plain member gets
  `MEMBER_COLUMNS`/`MEMBER_FILTERS`, limited to what `users.MemberListing` carries (see
  **Permission tiers**).

#### Search

- Above the filters is a "Buscar persona" box (`matchesSearch`): every word typed has to appear, in
  any order and ignoring accents and case, in the first name and surnames and, for admin/board
  only (as that is all their listing carries), the phone and DNI. It does not search the
  membership number, login or email.
- The placeholder says "Nombre o apellidos" (for admin/board, "Nombre, apellidos, teléfono o DNI").
- It narrows what the filters leave and isn't persisted.

#### Filters and columns

- Row filters ("Género", "Tipo de socio", "Categoría", "Cuota anual", "Federación") and the column
  toggles sit behind a "Filtros…" button.
- Every filter starts with every option checked except "Tipo de socio", which starts with only
  "Activo" and "Baja temporal", so the default view is the current membership.
- "Cuota anual" splits people on whether `last_due_year` is the current year ("A corriente de
  pago").
- "Federación" splits them on whether `license_type` is present: `GET /users` only reports a
  license for the current year, so having one *is* being federated.
- Filters, visible columns and sort order persist in `localStorage` (`katiuskas:members-view`,
  best-effort), so "Volver a socios" from a person's page lands on the same view.
- A counter shows how many people the current filters leave.
- With nothing saved, the columns shown are "N.º socio", "Nombre", "Apellidos", "Categoría" and
  "Tipo de licencia" (each column's `default` flag); the rest are optional in the "Filtros…" panel.

### The admin page (`/admin`)

#### Overview

- `/admin` (`admin.html`/`admin.js`) is the page behind the "Admin" sidebar entry (admin only,
  `NAV_ITEMS`' `roles: ['admin']`), meant for whatever else only administrators care about.
- It starts with a row of links to its sections (`.section-index`, each one an anchor to its
  `<h2 id="section-…">`: add a section, add its link).
- Its sections are "Estado del sistema", "Roles", "Sesiones", "Festivos", "Datos de interés" and
  "Tipos de material".

#### "Estado del sistema"

- It is a block of at most 960px, each name in a column of its own with the value next to it and a
  faint line under each row, since the names and values are short (`GET /admin/status`, see
  **Admin status**).
- On top is what is wrong right now (`problems`, in a red box) or "No se ha detectado ningún
  problema".
- Then come, in this order:
  - version and commit (with the date and time of the commit, in the viewer's own time zone);
  - "Actividad" (the different people and the requests of the last hour, day and month);
  - database and migration;
  - email;
  - Telegram;
  - timeouts;
  - directories and, in "Espacio en disco", how full the disks that hold them are.
- An "Actualizar" button reloads it.

#### "Roles"

- It uses `GET`/`POST`/`DELETE /roles`.
- A "Ver" under each role unfolds who holds it, taken from `GET /users` (admin sees everybody with
  their `roles`).
- Each holder has a "×" (`DELETE /users/{login}/roles/{id}`), and a name picker with "Dar este rol"
  (`POST /users/{login}/roles`) adds one.
- The automatic roles only list their holders, and the admin's own admin role has no "×", as on
  `/socio`.
- The unfolded roles survive the re-render in `expandedRoles`.

#### "Sesiones"

- It lists every session from `GET /tokens`: a person's name from the directory, last use, address,
  browser and system ("Chrome 118 · Windows", from `browser`/`os`, with the whole User-Agent as the
  tooltip; "—" if never used), creation and expiry, and "Activa"/"Caducada"/"Revocada".
- A badge marks an impersonation's session and the admin's own "Esta sesión".
- It shows 50 at a time with "Ver más" and a checkbox for the expired and revoked ones.
- An active session can be revoked (a different warning for one's own, which signs out) or have its
  expiry changed, through `/users/{login}/tokens/{id}`.
- Its headers Persona, Último uso, Creada and Caduca sort it (`initSortableHeaders`,
  `js/sortable-headers.js`): a click or Enter on the header picks the column or turns it round, and
  ▲/▼ and `aria-sort` say which. The default is last use, most recent first for the dates and A
  first for the person.
- The sorting is done by asking the API (`sort`, `order`), so that the order holds across the pages
  "Ver más" brings.

#### "Festivos"

- These are the club holidays the activities calendar tints, shown a year at a time with ‹ ›
  (`GET /holidays?year=`).
- Each has a "Borrar" (`DELETE /holidays/{id}`).
- A "+ Nuevo festivo" dialog (`POST /holidays`) adds one; a date that already has one is the API's
  409 `duplicate_holiday`.
- "Copiar del año anterior" POSTs last year's holidays on the same day and month except where the
  year already has one. Movable ones such as Semana Santa are fixed by hand; there is no edit.

#### "Datos de interés"

- These are the labelled values the home page shows (`GET`/`POST /club-info`,
  `PUT`/`DELETE /club-info/{id}`, `PUT /club-info/order`), in a table with "Editar" and "Borrar".
- ↑ ↓ put them in order (each sends the whole new order).
- A "+ Nuevo dato" dialog, shared by creating and editing, has a label, a value and, optionally, a
  text for a copy button and the button's own text.
- The home page (`dashboard.js`, `loadClubInfo`) draws them from `GET /club-info`, with the copy
  buttons handled by one listener on the list since they are drawn after load.
- The data is not written in `index.html`, so an administrator changes it without a deploy and a
  private one (the Bizum number) is not in the repository.

#### "Tipos de material"

- It uses `GET`/`POST /material/types` and `PATCH`/`DELETE /material/types/{id}`.
- It is a table with the type, whether its description is required, its attributes and the buttons
  ↑ ↓, "Editar" and "Borrar".
- "Cuerda" and "Saca" are listed as "Fijo" with no edit or delete buttons, and a type with material
  is refused with its own message.
- ↑ ↓ set the order of the types, which is the one of the "Tipo" list of `/material` (each sends
  the whole new order to `PUT /material/types/order`; Cuerda and Saca move too).

##### The type dialog

- One dialog, shared by creating and editing, has the name, "La descripción es obligatoria" and
  "Guardar" and, below, the type's attributes (`#material-attributes-section`).
- The attributes appear only once the type exists: saving a new one keeps the dialog open, now
  editing it.

##### Attributes

- Each attribute shows its kind (texto libre or lista de opciones), an "Obligatorio" checkbox, ↑ ↓
  to order them (the order of their values in an item's name; each sends the whole new order to
  `PUT /material/types/{id}/attributes/order`), "Renombrar" and "Borrar"
  (`PATCH`/`DELETE /material/attributes/{id}`).
- An enum attribute shows its options as badges with ✎ (rename) and × (remove) and a box to add one
  (`POST /material/attributes/{id}/options`, `PATCH`/`DELETE /material/options/{id}`).
- A form at the bottom adds an attribute (name, texto libre or lista de opciones with one option
  per line, obligatorio).
- Attribute changes are applied at once, each its own request, and reload the types. An error (an
  option in use, a change that would leave two items identical) is shown with its own message.

### Licenses with no payment (`/socio`)

- On `/socio`, a real admin also has a "+ Licencia sin pago" button (`add-license-dialog`) that
  records a license with no transaction (`POST /users/{login}/licenses` without `transaction_id`).
- It is the way to register one the club didn't collect, such as someone federated elsewhere.
- Licenses the club does collect are registered from their transaction on `/cuenta`, as above.

## Loading data in parallel

A page's requests that don't depend on each other go out together, not one after another (each
wait in a row costs a full round trip, which is most of what a page load takes once the API itself
is fast).

- `/socios` asks for `/users`, `/board-members` and, for an admin, `/roles` at once and draws the
  table when the board positions are in too (`loadUsers(ready)`: only the drawing waits, not the
  slow `/users` request).
- `/admin` loads all its sections (Estado del sistema, Actividad, Roles, Sesiones, Festivos, Datos
  de interés and Tipos de material) with one `Promise.all` (the sessions are drawn again with the
  names if the directory arrives after them).
- That is the convention for a page's first load, and the rest follow it: `/actividades`,
  `/actividad`, `/material`, `/socio`, `/cuenta` and the distribution pages start every independent
  request together.
- Whatever drawing needs something else (the directory of names, the role catalog, the people)
  takes it as a `ready` promise to wait for after its own data arrives, so only the drawing waits.
- The one dependency that remains is `/reparto`, which asks for the orders only once it knows the
  status isn't draft.

## Other pages

### The history of the Junta Directiva (`/junta`)

- `board-history.js` asks `GET /board-members/history` and turns each term into up to two events:
  "<name> entra como <cargo>" on its start date and "Último día de <name> como <cargo>" on its
  `end_date`.
- They are sorted newest first (on the same day an entry comes before a last day), grouped under
  one heading per year.
- The page is open to any baseline role; names link to `/socio?login=…` only for admin and board.
- The home page links to it under the "Junta directiva" section, and the sidebar keeps "Inicio"
  highlighted (`ACTIVE_ALIASES`).

### The sale over the counter (`distribution-sale.js`)

It is for a member who asks for something and pays on the spot.

- "Venta directa", in the buttons of `/reparto-gestion` (admin and board; only in a distribution
  with articles that is not a draft: `update(distribution)` shows and hides it), opens
  `sale-dialog`.
- The dialog asks for:
  - the member (a searchable field with who may have the distribution, `loadPeople(id, 'order')`);
  - the article (not shown when there is only one);
  - the quantity, with the total and what is left.
- For an article whose numbers are described, there are the buttons of `createNumberPicker` for the
  numbers the member asks for by name (the rest are given in order) and, for free-text numbers, a
  field to type them.
- For an article that costs something, there is "Cobrar ahora" with the account (the one used last,
  shared with the payment dialog of the orders) and, always, "Entregar ahora".
- Both are ticked by default, so the usual sale is: member, "Aceptar".
- It sends a single `POST .../sales`, shows the numbers given in the confirmation and has the page
  read its sections again (`onSold`).

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
archive (`socket`, `hashlib`, `struct`, `json`). To view a page that requires being
signed in: mint a session token with `scripts/get-token.sh <login>` (dev only), navigate
to the app's own origin first, run `Runtime.evaluate` with
`localStorage.setItem('token', '<token>')` — the same key `js/api.js` itself reads on
every page load — then `Page.navigate` to the real page and, once
`Page.loadEventFired` fires and a couple of seconds have passed for that page's own
client-side fetches to resolve, `Page.captureScreenshot` (`captureBeyondViewport: true`
to get the whole page, not just the current viewport).

