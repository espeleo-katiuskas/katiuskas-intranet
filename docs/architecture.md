# Arquitectura

Documento de referencia, en sincronía con el código.

## Idioma

- Código (identificadores, comentarios, logs, mensajes de error de la API): **inglés**.
- Documentación (este fichero, el README): español o inglés indistintamente.
- Frontend web (`web/`): todo el texto visible para la persona usuaria, en **español**.
  Los identificadores del propio código JS/HTML siguen en inglés.

## Componentes

```
web/ (HTML + JS, español)  →  backend/ (API REST en Go, /api/v1)  →  PostgreSQL
```

Un único servicio en Go concentra la lógica de negocio y el acceso a datos. Ningún
cliente toca PostgreSQL directamente, lo que permite añadir una app móvil o un cliente
de terminal más adelante sin duplicar reglas de negocio: comparten el contrato descrito
en `backend/openapi/openapi.yaml`.

## Permisos

Tres niveles, de más a menos acceso — cada ruta lo aplica con `Authenticator.Protect`
más, cuando hace falta, una comprobación adicional dentro del propio handler (ver
`auth.BaselineRoles`, que ya exige admin/board/member en todas las rutas protegidas):

- **`admin`** — puede hacer todo, sin excepción.
- **`board`** — puede **ver** todos los datos del API (personas, socios, licencias,
  Junta Directiva, roles, actividades, material), pero solo puede **modificar**
  actividades y material — exactamente igual que antes. Ya no puede crear ni borrar
  personas, dar de alta/baja, corregir bajas temporales, asignar licencias, gestionar
  la Junta Directiva, ni asignar/quitar roles a nadie: todo eso es admin-only ahora
  (rutas bajo `/users`, `/memberships`, `/leaves`, `/users/{userID}/licenses`,
  `/board-members`, `/users/{userID}/roles`).
- **`member`** (socio) — puede ver `GET /users`, pero no la ficha completa de cada
  persona: solo `users.MemberListing` (nombre, apellidos, género, número de socio,
  fecha de alta y de baja) para todo el mundo, no solo para sí mismo. `users.Handler.
  list` decide qué forma devolver según el rol de quien pregunta — no hay una ruta
  aparte. También puede ver `GET /board-members` (la Junta Directiva actual, con cargo,
  teléfono y correo — esa ruta es la única, aparte de las de admin/board, abierta a
  member). Sobre su propio registro, puede seguir editando datos de contacto —
  email, phone, address, postal_code, city, province, emergency_contact — pero no
  nombre, apellidos, DNI, fecha de nacimiento ni género: `users.Handler.update`
  rechaza con 403 `restricted_fields` cualquier intento de tocar esos campos si quien
  llama no es admin, en vez de ignorarlos en silencio. `GET /users/{id}` (la ficha
  completa) sigue reservado a admin/board o a la propia persona, sin cambios.

En el frontend, `/members` ahora es visible para member además de admin/board: la
lista de columnas y los filtros se reducen a lo que `MemberListing` realmente trae
(`MEMBER_COLUMNS`/`MEMBER_FILTERS` en `web/js/members.js`, en vez de las versiones
`FULL_*`), las filas dejan de ser clicables (`/member?id=…` sigue siendo solo para
admin/board), y las secciones de gestión (roles, nueva persona) llevan
`data-roles="admin"` o `"admin,board"` para que `applyRoleVisibility()` las quite del
todo. En `/member`, los botones de escritura (dar de alta, licencias, Junta Directiva,
gestión de roles, borrar persona) llevan `data-roles="admin"` en el HTML estático, y
las acciones que se generan dinámicamente por evento del timeline (los botones
"Editar" de cada fila) se filtran en JavaScript comprobando `isRealAdmin`, ya que
`data-roles` no llega a examinar contenido que todavía no existía cuando
`applyRoleVisibility()` se ejecutó.

## Modelo de datos (`db/migrations/`)

- **`users`** — datos personales de cualquier persona vinculada al club, incluidos
  `last_login_at` y `last_login_ip` (la IP de la última vez que inició sesión —
  `X-Forwarded-For` si existe, ya que en producción hay un proxy inverso por delante;
  la propia conexión si no). No hay tabla `accounts` ni ningún otro paso de "conceder
  acceso": lo que puede hacer una sesión depende solo de los roles de esa persona (ver
  más abajo), y esos mismos roles deciden si le llega o no un enlace de acceso — no hay
  contraseñas. `membership_number` también vive aquí, no en `memberships`: identifica a
  la persona mientras esté vinculada al club, no un periodo de alta concreto, así que
  darse de baja y volver a darse de alta nunca se lo cambia. Es un entero no negativo y
  único (`CHECK`/índice único, ver `db/migrations/00012_membership_number.sql`),
  asignado automáticamente — nunca a mano — la primera vez que a alguien se le da de
  alta: `memberships.Service.Create` llama a `users.Service.EnsureMembershipNumber`
  (expuesta como `memberships.MembershipNumberAssigner`, ya que `memberships` no puede
  importar `users` — es al revés — así que `cmd/api`/`cmd/seed-admin` la conectan tras
  construir ambos servicios), que le pone el siguiente número tras el más alto que haya
  en ese momento, y no hace nada si la persona ya tiene uno. `gender` (tipo enum de
  PostgreSQL, `male`/`female`) es opcional y nulo por defecto — nadie lo rellena al
  crear una persona salvo que se indique explícitamente. La "categoría" (infantil,
  juvenil, mayores) no se guarda: `web/js/members.js` la calcula al vuelo a partir de
  `birth_date` y el año actual, igual que cualquier otra columna derivada de la lista.
  `email` y `phone` pueden llevar más de un valor separados por espacio — igual que
  `emergency_contact` ya admitía varios contactos en texto libre — en vez de una tabla
  aparte: encajar "alguno de estos" es una comprobación de solape de arrays
  (`string_to_array(...) && string_to_array(...)`), y esta escala de club no necesita
  más. Eso sí impide seguir usando un `UNIQUE` de columna en `email` — un mismo correo
  sigue sin poder pertenecer a dos personas, pero Postgres no puede expresar "ninguna
  fila comparte un elemento" así, de modo que `users.Repository` lo comprueba a mano en
  `Create`/`Update` (ver `db/migrations/00013_multiple_contacts.sql`). Iniciar sesión y
  el enlace de acceso funcionan contra cualquiera de los correos/teléfonos registrados
  (`Repository.FindActiveByEmailAndPhone`); como el espacio pasa a separar varios
  teléfonos, uno solo ya no puede llevar espacios internos como formato (escribe
  "600111222", no "600 111 222" — otra puntuación como los guiones sigue dando igual).
- **`magic_link_tokens`** — enlaces de acceso de un solo uso (hash del token, caducidad,
  marca de consumido). Se piden en `/auth/magic-link` dando el email y el teléfono de la
  persona; solo se genera uno y se envía por correo si ambos coinciden con alguien de
  `users` *y* esa persona tiene al menos uno de los roles `admin`, `board` o `member`
  (`auth.BaselineRoles`, ver más abajo) — a quien no tiene ninguno no le serviría de
  nada iniciar sesión, así que no se le manda enlace. La respuesta de `/auth/magic-link`
  es idéntica en los tres casos (no coincide nadie, coincide alguien sin rol, se envía
  el enlace de verdad) — así no sirve para averiguar quién está en el sistema, ni quién
  tiene acceso, ni para mandar correo no deseado.
- **`roles`** / **`user_roles`** — catálogo de roles (gestionable: crear, borrar) y
  quién tiene cuáles (muchos a muchos). `admin`, `board`, `member` y `former_member`
  son roles de *sistema* (`is_system`): son los únicos a los que el código realmente
  da permisos o comportamiento automático, y no se pueden borrar. Cualquier otro rol
  que cree un admin sirve como etiqueta organizativa — no desbloquea nada por sí solo
  salvo que se programe explícitamente.
  `member`, `former_member` y `board` son además *automáticos* (`is_automatic`): nadie
  los asigna ni los quita a mano (`POST`/`DELETE /users/{userID}/roles` los rechaza con
  409). Se calculan al vuelo — `roles.Repository.ListNamesForUser` es el único sitio que
  sabe cómo—: `member` a partir de `memberships` si la persona tiene un periodo de socio
  activo ahora mismo, `former_member` si ha tenido alguno antes pero ninguno activo hoy,
  y `board` a partir de `board_members` si tiene un cargo de la Junta Directiva abierto
  ahora mismo. Ese cálculo es lo que resuelve cada petición autenticada (ver más abajo),
  así que los permisos basados en "soy socio" o "soy de la junta" siempre reflejan el
  estado real de alta, no una etiqueta que alguien olvidó quitar.
- **`tokens`** — el token de sesión de cada persona: opaco (hash SHA-256 guardado, nunca
  el valor en claro), revocable, con `expires_at`, y `last_used_at`/`last_used_ip` (misma
  lógica de `X-Forwarded-For` que `users.last_login_ip`) actualizados en cada petición
  que lo usa. Un admin puede ver, re-caducar o revocar el token de cualquier persona —
  `GET`/`PATCH`/`DELETE /users/{userID}/tokens[/{tokenID}]` —, pero nunca ver el valor
  del token en sí, solo sus metadatos.
  **Impersonación**: `POST /users/{userID}/impersonate` (solo admin, no board — actuar
  como cualquiera, incluido otro admin, es un salto mayor que el resto de lo que board ya
  puede hacer) crea un token cuyo `user_id` es el de esa persona y con un
  `impersonated_by` (el id del admin) guardado junto a él. Como los roles se resuelven en
  vivo a partir de `user_id` (igual que cualquier sesión normal), esa sesión ya sólo puede
  hacer lo mismo que esa persona sin ningún caso especial en el resto del API — la única
  excepción vive en `authapi`: `GET /auth/me` añade `impersonated_by` a la respuesta
  (para que el frontend pueda avisarlo) y `POST /auth/impersonate/exit` deja salir de la
  impersonación pase lo que pase con los roles de la persona impersonada en ese momento
  (por eso ese endpoint sólo exige `RequireAuth`, no `Protect`, que exigiría un rol base —
  si la persona lo hubiera perdido mientras se la impersonaba, ese admin se quedaría sin
  forma de salir). Salir no reutiliza el token que tenía el admin antes de impersonar —el
  API nunca vuelve a tener el valor en claro de un token ya emitido—, sino que revoca la
  sesión de impersonación y emite una nueva para el admin. Sólo se puede impersonar a
  quien tenga ahora mismo algún rol base (`auth.BaselineRoles`) — igual que un enlace de
  acceso no se manda a quien no lo tiene —, y nunca a uno mismo. El token de impersonación
  dura mucho menos que uno normal (`config.ImpersonationTokenTTL`, 1 hora frente a 30
  días): una sesión actuando como otra persona es un riesgo mayor que una normal como para
  dejarla viva un mes si alguien se olvida de salir.
- `GET /users/directory` — abierto a cualquier persona autenticada, devuelve solo id y
  nombre de quienes tienen acceso (nada de teléfono, dirección, DNI...). Existe para que
  un socio cualquiera pueda elegir participantes al crear una actividad sin que el API
  tenga que abrirle `GET /users`, que sí expone datos personales y sigue reservado a
  admin/board.
- **`memberships`** — periodos de alta/baja de socio. Una persona puede tener varios
  periodos si no se solapan; lo garantiza una restricción `EXCLUDE USING gist` de
  PostgreSQL sobre `(user_id, period)`, no una comprobación manual.
  `memberships.Service.LatestByUser` expone el periodo más reciente de una persona
  (activo o no); `users.Service` lo incorpora a cada `User` como
  `membership_start_date`/`membership_end_date`, para que la lista de personas pueda
  mostrar y ordenar por fecha de alta/baja sin una petición aparte por persona.
- **`membership_leaves`** — "bajas temporales": una ventana con fecha de inicio y fin
  (ambas obligatorias desde el principio, a diferencia de un periodo de socio) dentro
  de un periodo de `memberships` concreto — la persona se ausenta un tiempo sin dejar
  de ser socia. Que la ventana quede *contenida* en el periodo de socio no es algo que
  una restricción `CHECK` pueda comprobar entre dos tablas, y este proyecto no usa
  triggers, así que `memberships.Repository` lo valida en Go antes de cada
  `INSERT`/`UPDATE` (`ErrLeaveOutsideMembership`); que dos bajas del mismo periodo no
  se solapen sí lo garantiza `EXCLUDE USING gist`, igual que con los propios periodos.
  Rutas bajo `/memberships/{membershipID}/leaves` y `/leaves/{id}`, admin/board. Una
  baja temporal no toca el periodo de socio al que pertenece — sigue abierto durante
  ella —, así que `users.User.OnLeave` (`on_leave` en el JSON) es el único sitio que
  distingue "activo" de "baja temporal" en la lista de personas; se calcula solo para
  quien ya tiene un alta activa, con `memberships.Service.IsOnLeave`.
- **`licenses`** / **`license_types`** — licencias federativas. El tipo ya no es texto
  libre: se elige de un catálogo (`license_types`, migración 00017) guardado en la base
  de datos en vez de un enum de Go, precisamente porque el club espera que la lista
  cambie con el tiempo (una zona nueva, una federación nueva) y eso no debería necesitar
  un despliegue de código — solo un `INSERT`. El catálogo trae sembradas todas las
  combinaciones de zona (A/B/B1) × modalidad (BASICA/PLUS) × franja de edad (adulto,
  Infantil, Juvenil), más ZERO/ASEDEB/FAE/FERM/FMM, más "Otra" como válvula de escape
  (`is_other`) para cualquier licencia que no encaje: una licencia de tipo "Otra" exige
  además `other_detail` diciendo cuál es en realidad, validación que vive en
  `licenses.Service.Create` (un `CHECK` no puede alcanzar `license_types.is_other` desde
  otra tabla). `GET /license-types` expone el catálogo, solo admin, igual que la propia
  asignación. De carácter anual: válida desde `issued_date` hasta el 31 de diciembre de
  ese mismo año (`valid_until`, calculado en Go a partir de `issued_date`, nunca
  almacenado — igual que la "categoría" de `web/js/members.js`, no vale la pena guardar
  algo que se deriva trivialmente de otro campo). Solo se le puede asignar una licencia a
  quien tenga ahora mismo un periodo de `memberships` abierto
  (`memberships.Service.IsActiveMember`); intentarlo con alguien que nunca ha sido socio
  o que ya ha causado baja devuelve 409 `not_active_member`. `users.Service` incorpora la
  licencia más reciente de cada persona a `User` (`licenses.Service.LatestByUser`), igual
  que hace con el periodo de socio más reciente. Rutas bajo `/users/{userID}/licenses`:
  `GET` abierto a la propia persona o admin/board, `POST` solo admin;
  `PATCH`/`DELETE /licenses/{id}` (por id de licencia, no anidadas bajo el usuario, igual
  que `/leaves/{id}`) también solo admin — `PATCH` no vuelve a exigir alta activa, porque
  corrige una licencia ya existente en vez de asignar una nueva, así que sigue
  funcionando aunque la persona ya haya causado baja. En `/member`, cada
  licencia aparece como un evento más dentro del mismo timeline de altas y bajas, no en
  una sección aparte.
- **`board_members`** — quién ha estado en la Junta Directiva y con qué cargo
  (`president`/`vice_president`/`secretary`/`treasurer`/`member_at_large` — vocal, tipo
  `board_position`), con la misma forma que `memberships`: `start_date`/`end_date`
  (vacío = sigue en el cargo) y un `EXCLUDE USING gist (user_id WITH =, period WITH &&)`
  que impide que una persona tenga dos cargos abiertos a la vez, sean o no el mismo — no
  hay restricción, en cambio, sobre que dos personas tengan el mismo cargo simultáneamente
  (por ejemplo varios vocales a la vez es normal). El paquete `board` no depende de
  `users` ni de `memberships` — no necesita el juego de interfaz que sí hace falta para
  `membership_number` (ver más abajo), porque el rol automático `board` se calcula con
  una consulta SQL directa contra `board_members` desde el propio paquete `roles`, igual
  que `member`/`former_member` contra `memberships`. Rutas bajo
  `/users/{userID}/board-members` (`GET` propia persona o admin/board, `POST` solo
  admin/board), `GET /board-members` (la Junta Directiva actual), y
  `PATCH /board-members/{id}` para corregir cargo o fechas. En `/member`, un cargo
  aparece como dos eventos más (inicio y fin) en el mismo timeline de altas y bajas.
- **`activities`** / **`activity_participants`** — actividades del club y quién participa.
  Crear una actividad está abierto a cualquier socio con acceso (no solo admin/board), que
  además puede apuntar de una vez a los participantes iniciales. Toda actividad tiene un
  organizador obligatorio (`leader_id`), fijado siempre a quien la crea — nunca elegible
  por quien llama al endpoint —, y editarla o cancelarla está reservado a ese organizador
  o a admin/board (`Handler.canManage`, que consulta la actividad para comparar
  `leader_id` con el usuario autenticado). El tipo (`caving`, `canyoning`, `training`,
  `social`, `other`) también se puede cambiar al editar. Apuntar a un participante en una
  actividad de tipo `caving` o `canyoning` exige que esa persona tenga una licencia
  federativa vigente (`licenses.Service.IsCurrentlyFederated`); si no, la llamada se
  rechaza con 409 `not_federated` — tanto al unirse después como al dar de alta
  participantes iniciales en la propia creación.
- **`inventory_items`** / **`item_transactions`** — catálogo de material y sus movimientos
  (`loan`, `rental`, `sale`), modelados en una única tabla con un discriminador de tipo
  en vez de tres tablas paralelas, con `CHECK` que impide, por ejemplo, una venta con
  fecha de devolución. El alquiler se tarifica por semana (`rental_price_per_week`), no
  por día. Un préstamo o un alquiler siempre va vinculado a una actividad
  (`item_transactions.activity_id`, exigido por otro `CHECK`); una venta no lo necesita,
  porque el material sale del inventario del club para siempre, actividad o no. Crear o
  modificar una transacción de material requiere el rol `admin`, `board` o el rol
  opcional `material` (sembrado por la migración pero no de sistema: un club que no
  quiera esa distinción puede borrarlo y seguir usando `board`); dar de alta el propio
  material del catálogo sigue reservado a admin/board.
- `GET /users/{userID}/activities` y `GET /users/{userID}/transactions` — el historial de
  una persona visto desde su propia ficha, en vez de desde la actividad o la transacción.
  El primero está tan abierto como `listParticipants` (cualquier persona autenticada);
  el segundo exige ser la propia persona o tener `admin`, `board` o `material`, igual que
  `GET /transactions`, porque una transacción lleva precio.

## Backend (`backend/`)

Capas separadas por paquete: `internal/httpapi` (router y helpers HTTP), `internal/auth`
(middleware de autenticación/autorización — ya no hay contraseñas que gestionar; no sabe
nada de JWT ni de base de datos, solo delega en un `SessionResolver`), `internal/tokens`
(el `SessionResolver` real: valida un token opaco contra la base de datos, calcula los
roles al vuelo, y expone la gestión de tokens para admin), `internal/mail` (envío de
correo: registra el mensaje en el log si no hay `SMTP_HOST` configurado, lo manda de
verdad si lo hay), `internal/authapi` (magic-link/logout/me) y un paquete por recurso de
dominio (`users`, `roles`, `memberships`, `licenses`, `board`, `activities`, `inventory`), cada uno con
`model.go` / `repository.go` (pgx) / `service.go` / `handler.go`. El dominio no depende
de `net/http`; el handler es la única capa que sabe que existe HTTP.

`auth.Authenticator.Protect`, el punto único por el que cada ruta se registra, exige
siempre tener al menos uno de los roles `admin`, `board` o `member` — un suelo que se
aplica *además* de los roles concretos que pida esa ruta, no un valor por defecto que
esos roles sustituyan. Alguien con solo, por ejemplo, `material` (sin ser también
admin, board o socio activo) no puede usar el API en absoluto, ni siquiera las rutas
de material. Como `member` es automático (se calcula de tener una alta activa ahora
mismo), esto también corta el acceso en el momento en que alguien deja de ser socio.

Sesión: un único token opaco y revocable (tabla `tokens`), sin par access/refresh — la
seguridad extra de un token de acceso de vida corta no compensaba la complejidad, ni
para el cliente ni para quien tuviera que operar contra el API a mano (scripts, un
cliente de terminal). El token se usa directamente como credencial en cada petición;
como es opaco, el cliente no puede leer nada de él — `GET /auth/me` es la forma de
saber quién es y qué roles tiene la sesión actual. Empezarla requiere primero un enlace
de `/auth/magic-link` (15 min de validez, un solo uso).

`cmd/seed-admin` crea la primera cuenta `admin`: `POST /users` ya exige ese rol, así que
sin este comando no habría forma de entrar en una base de datos recién migrada.
Idempotente — no hace nada si el email ya existe. Como no hay contraseña, para entrar
hay que pedir un enlace desde la página de acceso con `SEED_ADMIN_EMAIL` y
`SEED_ADMIN_PHONE`, y mirar los logs de la API (o el buzón, si hay SMTP configurado).

## Frontend (`web/`)

Páginas multi-vista clásicas (no una SPA), con un cliente `fetch` compartido
(`web/js/api.js`) que añade el token (guardado en `localStorage`) a cada petición y, al
cargar cada página, llama a `GET /auth/me` para confirmar que sigue siendo válido y
saber qué roles tiene quien lo usa.

Cada fila de la tabla de personas en `/members` enlaza a `/member?id=<id>`
(`member.html`/`member.js`, solo admin/board): la ficha de esa persona, con sus datos
editables y su historial de actividades y material, leído de
`GET /users/{userID}/activities` y `GET /users/{userID}/transactions`.

## Cómo ejecutar en local

```sh
docker compose up -d postgres
export DATABASE_URL=postgres://intranet:intranet@localhost:5433/intranet?sslmode=disable
make migrate-up          # requiere goose: https://github.com/pressly/goose
make seed-admin          # crea la cuenta admin (sin contraseña: acceso por magic link)
make run                 # sirve la API en :8080
```

El frontend es estático: se puede servir con cualquier servidor de ficheros apuntando a
`web/` (o con `docker compose up` completo, que además levanta Caddy en `:8000`
proxificando `/api/*` hacia el backend).
