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
```

Para desarrollo sin Docker, ver `docs/architecture.md`.
