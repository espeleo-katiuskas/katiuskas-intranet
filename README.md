# Intranet del Club de Espeleología Katiuskas

Base de datos en PostgreSQL, API REST en Go y frontend web en JavaScript, pensados para
que la misma API sirva también a una futura app móvil o a un cliente de terminal. Más
información sobre el club en [katiuskas.es](https://katiuskas.es/).

- [`db/migrations/`](db/migrations/) — esquema de PostgreSQL (migraciones con
  [goose](https://github.com/pressly/goose)).
- [`backend/`](backend/) — API REST en Go (`/api/v1`), ver
  [`backend/openapi/openapi.yaml`](backend/openapi/openapi.yaml) para el contrato completo.
- [`web/`](web/) — páginas web (HTML + JS) para socios y junta directiva.
- [`docs/architecture.md`](docs/architecture.md) — arquitectura y reglas de negocio.
- [`docs/api.md`](docs/api.md) — lista de endpoints de la API, para quien quiera usarla sin leer
  el OpenAPI.
- [`docs/database.md`](docs/database.md) — cómo se guardan los datos: tablas, columnas y
  restricciones.
- [`docs/backend-internals.md`](docs/backend-internals.md) y [`docs/frontend.md`](docs/frontend.md)
  — el detalle de cómo funciona el backend, recurso a recurso, y el frontend, página a página.
- [`scripts/`](scripts/) — herramientas de línea de comandos; ver
  [`scripts/README.md`](scripts/README.md).
- [`docker-compose.yml`](docker-compose.yml) — PostgreSQL + API + frontend detrás de Caddy, para
  levantar todo junto.
- [`docs/deployment.md`](docs/deployment.md) — cómo desplegar en producción en una máquina propia,
  con systemd y sin Docker.
- [`docs/environments.md`](docs/environments.md) — los tres entornos (local con Docker, y testing
  y producción con systemd en el host del club) y cómo se actualiza cada uno.

## Idioma

Todo el código (identificadores, comentarios, logs y mensajes de error de la API) está
en inglés, y también toda la documentación de `docs/` y de `scripts/` (este `README.md` queda en
español por ahora). El frontend web, al ser la
interfaz de las personas socias del club, está enteramente en español.

## Puesta en marcha rápida

```sh
docker compose up --build
```

Esto levanta PostgreSQL, la API en `http://localhost:64711` y el frontend en
`http://localhost:64710` (con `/api/*` proxificado hacia la API). Falta aplicar las
migraciones la primera vez (hace falta tener Go y el binario `goose` instalados en la
máquina; `seed-admin` se ejecuta con `go run`):

```sh
export DATABASE_URL=postgres://intranet:intranet@localhost:64712/intranet?sslmode=disable
make migrate-up
make seed-admin
```

`seed-admin` crea la primera cuenta de administración — necesaria porque `POST /users` ya
exige ser `admin`, así que sin ella no habría forma de entrar en una base de
datos recién migrada. No hay contraseña: por defecto crea `admin@club.test` con el
teléfono `600000000`; para otros valores, exporta `SEED_ADMIN_EMAIL`, `SEED_ADMIN_PHONE`,
`SEED_ADMIN_FIRST_NAME`, `SEED_ADMIN_LAST_NAME` o `SEED_ADMIN_BIRTH_DATE` (`YYYY-MM-DD`)
antes de ejecutarlo. Es idempotente: si el email ya existe, no hace nada.

Para entrar, ve a `http://localhost:64710/login`, escribe ese email y ese teléfono, y
mira los logs de la API (`docker compose logs api`) para encontrar el enlace de acceso —
sin `SMTP_HOST` configurado, el correo se registra en el log en vez de enviarse de verdad.

Para desarrollo sin Docker, `make run` sirve la API en `:64711` (necesita `DATABASE_URL` apuntando a
un PostgreSQL accesible); más detalles en [`docs/architecture.md`](docs/architecture.md).

## Tests

```sh
docker exec -it katiuskas-intranet-postgres-1 psql -U intranet -d postgres -c "CREATE DATABASE intranet_test;"
export TEST_DATABASE_URL=postgres://intranet:intranet@localhost:64712/intranet_test?sslmode=disable
cd backend && go test ./...
```

Cada paquete que toca PostgreSQL (todos menos `auth`, `config`, `mail`, `httpapi`,
`telegram`, `library` y `userfiles` — estos dos últimos sirven ficheros del disco, no de
la base de datos; los demás no persisten nada en absoluto) usa una base de datos de
pruebas real — no hay mocks del acceso a datos, las restricciones de PostgreSQL (solapes,
claves foráneas, uniqueness) son parte de lo que se prueba. `internal/testutil` aplica
las migraciones una sola vez y le da a cada test su propia transacción, deshecha al
terminar, así que los tests no interfieren entre sí. Solo comprueba si el esquema existe,
no qué migraciones tiene: tras añadirse una migración nueva hay que borrar y volver a crear
`intranet_test`, o los tests fallan con errores del tipo "column ... does not exist". Sin
`TEST_DATABASE_URL` (o si no hay Postgres accesible), esos tests se saltan solos en vez de
fallar — `go test ./...` funciona igual en una máquina sin Postgres. Para borrar y volver a
crear la base de datos de pruebas:

```sh
docker exec -it katiuskas-intranet-postgres-1 psql -U intranet -d postgres -c "DROP DATABASE intranet_test;"
docker exec -it katiuskas-intranet-postgres-1 psql -U intranet -d postgres -c "CREATE DATABASE intranet_test;"
```
