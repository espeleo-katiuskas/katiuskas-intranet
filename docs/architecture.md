# Arquitectura

Documento de referencia, en sincronía con el código. La versión narrada y con diagramas
vive en este artifact: https://claude.ai/code/artifact/21ceb4cf-5acd-443b-8531-403bc76099b0
(los nombres de tablas y rutas de ese documento estaban en español; aquí, y en el código,
están en inglés).

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

## Modelo de datos (`db/migrations/`)

- **`users`** — datos personales de cualquier persona vinculada al club, incluidos
  `last_login_at` y `last_login_ip` (la IP de la última vez que inició sesión —
  `X-Forwarded-For` si existe, ya que en producción hay un proxy inverso por delante;
  la propia conexión si no). No hay tabla `accounts` ni ningún otro paso de "conceder
  acceso": lo que puede hacer una sesión depende solo de los roles de esa persona (ver
  más abajo), y esos mismos roles deciden si le llega o no un enlace de acceso — no hay
  contraseñas.
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
  `member` y `former_member` son además *automáticos* (`is_automatic`): nadie los
  asigna ni los quita a mano (`POST`/`DELETE /users/{userID}/roles` los rechaza con
  409). Se calculan al vuelo a partir de `memberships` —
  `roles.Repository.ListNamesForUser` es el único sitio que sabe cómo—: `member` si la
  persona tiene un periodo de socio activo ahora mismo, `former_member` si ha tenido
  alguno antes pero ninguno activo hoy. Ese cálculo es lo que resuelve cada petición
  autenticada (ver más abajo), así que los permisos basados en "soy socio" siempre
  reflejan el estado real de alta, no una etiqueta que alguien olvidó quitar.
- **`tokens`** — el token de sesión de cada persona: opaco (hash SHA-256 guardado, nunca
  el valor en claro), revocable, con `expires_at`, y `last_used_at`/`last_used_ip` (misma
  lógica de `X-Forwarded-For` que `users.last_login_ip`) actualizados en cada petición
  que lo usa. Un admin puede ver, re-caducar o revocar el token de cualquier persona —
  `GET`/`PATCH`/`DELETE /users/{userID}/tokens[/{tokenID}]` —, pero nunca ver el valor
  del token en sí, solo sus metadatos.
- `GET /users/directory` — abierto a cualquier persona autenticada, devuelve solo id y
  nombre de quienes tienen acceso (nada de teléfono, dirección, DNI...). Existe para que
  un socio cualquiera pueda elegir participantes al crear una actividad sin que el API
  tenga que abrirle `GET /users`, que sí expone datos personales y sigue reservado a
  admin/board.
- **`memberships`** — periodos de alta/baja de socio. Una persona puede tener varios
  periodos si no se solapan; lo garantiza una restricción `EXCLUDE USING gist` de
  PostgreSQL sobre `(user_id, period)`, no una comprobación manual.
- **`activities`** / **`activity_participants`** — actividades del club y quién participa.
  Crear una actividad está abierto a cualquier socio con acceso (no solo admin/board), que
  además puede apuntar de una vez a los participantes iniciales; editarla o cancelarla
  sigue reservado a admin/board.
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

## Backend (`backend/`)

Capas separadas por paquete: `internal/httpapi` (router y helpers HTTP), `internal/auth`
(middleware de autenticación/autorización — ya no hay contraseñas que gestionar; no sabe
nada de JWT ni de base de datos, solo delega en un `SessionResolver`), `internal/tokens`
(el `SessionResolver` real: valida un token opaco contra la base de datos, calcula los
roles al vuelo, y expone la gestión de tokens para admin), `internal/mail` (envío de
correo: registra el mensaje en el log si no hay `SMTP_HOST` configurado, lo manda de
verdad si lo hay), `internal/authapi` (magic-link/logout/me) y un paquete por recurso de
dominio (`users`, `roles`, `memberships`, `activities`, `inventory`), cada uno con
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
