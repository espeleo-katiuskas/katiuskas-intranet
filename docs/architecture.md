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

- **`users`** — datos personales de cualquier persona vinculada al club.
- **`credentials`** — usuario/contraseña, 1 a 1 con `users`. Separada de `users` porque
  no toda persona registrada tiene acceso web.
- **`roles`** / **`user_roles`** — catálogo de roles (gestionable: crear, borrar) y
  quién tiene cuáles (muchos a muchos). `admin`, `board` y `member` son roles de
  *sistema* (`is_system`): son los únicos a los que el código realmente da permisos, y
  no se pueden borrar. Cualquier otro rol que cree un admin sirve como etiqueta
  organizativa — no desbloquea nada por sí solo salvo que se programe explícitamente.
- **`refresh_tokens`** — sesiones de refresco, revocables.
- **`memberships`** — periodos de alta/baja de socio. Una persona puede tener varios
  periodos si no se solapan; lo garantiza una restricción `EXCLUDE USING gist` de
  PostgreSQL sobre `(user_id, period)`, no una comprobación manual.
- **`activities`** / **`activity_participants`** — actividades del club y quién participa.
- **`inventory_items`** / **`item_transactions`** — catálogo de material y sus movimientos
  (`loan`, `rental`, `sale`), modelados en una única tabla con un discriminador de tipo
  en vez de tres tablas paralelas, con `CHECK` que impide, por ejemplo, una venta con
  fecha de devolución.

## Backend (`backend/`)

Capas separadas por paquete: `internal/httpapi` (router y helpers HTTP), `internal/auth`
(contraseñas con bcrypt, JWT, middleware de autenticación/autorización), `internal/authapi`
(login/refresh/logout) y un paquete por recurso de dominio (`users`, `memberships`,
`activities`, `inventory`), cada uno con `model.go` / `repository.go` (pgx) / `service.go`
/ `handler.go`. El dominio no depende de `net/http`; el handler es la única capa que sabe
que existe HTTP.

Sesión: JWT de acceso de vida corta (15 min) + refresh token opaco persistido (hash
SHA-256, no el token en claro) y revocable — pensado para que un cliente de terminal
pueda mantener sesión sin cookies de navegador.

`cmd/seed-admin` crea la primera cuenta `admin`: `POST /users` ya exige ese rol, así que
sin este comando no habría forma de entrar en una base de datos recién migrada.
Idempotente — no hace nada si el usuario ya existe.

## Frontend (`web/`)

Páginas multi-vista clásicas (no una SPA), con un cliente `fetch` compartido
(`web/js/api.js`) que añade el token a cada petición y renueva la sesión al cargar cada
página a partir del refresh token guardado en `localStorage`.

## Cómo ejecutar en local

```sh
docker compose up -d postgres
export DATABASE_URL=postgres://intranet:intranet@localhost:5433/intranet?sslmode=disable
make migrate-up          # requiere goose: https://github.com/pressly/goose
make seed-admin          # crea admin / changeme123
export JWT_SECRET=dev-secret
make run                 # sirve la API en :8080
```

El frontend es estático: se puede servir con cualquier servidor de ficheros apuntando a
`web/` (o con `docker compose up` completo, que además levanta Caddy en `:8000`
proxificando `/api/*` hacia el backend).
