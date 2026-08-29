# Intranet del club de espeleología

Base de datos en PostgreSQL, API REST en Go y frontend web en JavaScript, pensados para
que la misma API sirva también a una futura app móvil o a un cliente de terminal.

- `db/migrations/` — esquema de PostgreSQL (migraciones con [goose](https://github.com/pressly/goose)).
- `backend/` — API REST en Go (`/api/v1`), ver `backend/openapi/openapi.yaml` para el contrato completo.
- `web/` — páginas web (HTML + JS) para socios y directiva.
- `docs/architecture.md` — arquitectura y decisiones de diseño.
- `docker-compose.yml` — PostgreSQL + API + frontend detrás de Caddy, para levantar todo junto.

## Idioma

Todo el código (identificadores, comentarios, logs y mensajes de error de la API) está
en inglés. La documentación puede estar en español o inglés. El frontend web, al ser la
interfaz de las personas socias del club, está enteramente en español.

## Puesta en marcha rápida

```sh
docker compose up --build
```

Esto levanta PostgreSQL, la API en `http://localhost:8080` y el frontend en
`http://localhost:8000` (con `/api/*` proxificado hacia la API). Falta aplicar las
migraciones la primera vez:

```sh
export DATABASE_URL=postgres://intranet:intranet@localhost:5433/intranet?sslmode=disable
make migrate-up
make seed-admin
```

`seed-admin` crea la primera cuenta de administración — necesaria porque `POST /users` ya
exige ser `admin` o `board`, así que sin ella no habría forma de entrar en una base de
datos recién migrada. No hay contraseña: por defecto crea `admin@club.test` con el
teléfono `600000000`; para otros valores, exporta `SEED_ADMIN_EMAIL`, `SEED_ADMIN_PHONE`,
`SEED_ADMIN_FIRST_NAME` o `SEED_ADMIN_LAST_NAME` antes de ejecutarlo. Es idempotente: si
el email ya existe, no hace nada.

Para entrar, ve a `http://localhost:8000/login.html`, escribe ese email y ese teléfono, y
mira los logs de la API (`docker compose logs api`) para encontrar el enlace de acceso —
sin `SMTP_HOST` configurado, el correo se registra en el log en vez de enviarse de verdad.

Para desarrollo sin Docker, ver `docs/architecture.md`.
