# Despliegue en producción (máquina propia)

Guía para poner la intranet en marcha en una máquina propia (no en un servicio de nube)
que ya tiene su propio reverse proxy (Caddy u otro) gestionando HTTPS para varios
dominios. El stack de la intranet no gestiona TLS ni certificados: el contenedor `web`
escucha en texto plano solo en `127.0.0.1:64710` (igual que en desarrollo), y es el
reverse proxy ya existente en la máquina el que le reenvía el tráfico del dominio que
elijas. Pensada para una prueba inicial con un número reducido de personas.

Requiere Docker en la máquina. Si el disco es limitado y prefieres no pagar el coste de
Docker (el motor más las imágenes de PostgreSQL/Caddy/Go), ver en su lugar
[`docs/deployment-no-docker.md`](deployment-no-docker.md), la misma idea con procesos
nativos gestionados por `systemd`.

Todo lo que sigue lo ejecutas tú directamente en la máquina de destino — no hay ningún
paso de despliegue automático (ni CI/CD, ni redeploy en cada `git push`). Lo único
"automático" es que los contenedores se reinician solos si se caen o si la máquina
arranca de nuevo (`restart: unless-stopped` en `docker-compose.production.yml`); publicar
una versión nueva del código sigue siendo un paso manual, ver [Actualizar a una versión
nueva](#actualizar-a-una-versión-nueva) más abajo.

## Requisitos previos

- Docker y el plugin `docker compose` instalados en la máquina (`docker compose version`).
  No hace falta Go, goose, ni nada más — todo lo demás corre dentro de contenedores.
- Un reverse proxy ya funcionando en la máquina (el mismo Caddy que uses para tus otros
  dominios, por ejemplo) capaz de servir HTTPS para el dominio que quieras usar aquí y
  reenviar ese tráfico a `http://127.0.0.1:64710`. Ese reverse proxy es responsabilidad
  tuya, fuera de este repositorio — aquí solo se documenta a qué puerto apuntarlo.
- Ningún puerto de este stack necesita abrirse directamente a Internet:
  `docker-compose.production.yml` no publica PostgreSQL ni la API, y `web` (Caddy) solo
  escucha en `127.0.0.1:64710`, igual que en desarrollo — nada llega desde fuera de la
  máquina sin pasar antes por tu reverse proxy.

## Primer despliegue

Clona el repositorio en la máquina y sitúate en su raíz:

```sh
git clone <url-del-repositorio> katiuskas-intranet
cd katiuskas-intranet
```

Crea el fichero de configuración a partir de la plantilla y rellena los valores reales
(dominio, contraseña de PostgreSQL, email/teléfono de la primera cuenta admin — ver los
comentarios de cada variable en la propia plantilla):

```sh
cp .env.production.example .env
$EDITOR .env
```

`.env` ya está en `.gitignore`: nunca se sube al repositorio.

Arranca primero solo PostgreSQL, aplica las migraciones y crea la primera cuenta de
administración, y luego levanta el resto del stack:

```sh
docker compose -f docker-compose.production.yml up -d postgres
docker compose -f docker-compose.production.yml --profile tools run --rm migrate
docker compose -f docker-compose.production.yml --profile tools run --rm seed-admin
docker compose -f docker-compose.production.yml up -d
```

`migrate` y `seed-admin` son tareas puntuales (perfil `tools`, no arrancan con `up -d`
sin más) que compilan el backend hasta su fase intermedia con el toolchain de Go completo
— necesitan acceso a Internet saliente la primera vez, para descargar `goose` y los
módulos de Go. `seed-admin` es idempotente: si el email ya existe, no hace nada, así que
volver a ejecutarlo por error no tiene efecto.

Comprueba que todo ha arrancado y que responde en localhost:

```sh
docker compose -f docker-compose.production.yml ps
curl -I http://127.0.0.1:64710/
```

Por último, añade un sitio a tu reverse proxy existente para el dominio elegido (el mismo
que pusiste en `DOMAIN`) que reenvíe a `http://127.0.0.1:64710`. Por ejemplo, si ese
reverse proxy es también Caddy, bastaría un bloque como:

```
intranet.tudominio.org {
	reverse_proxy 127.0.0.1:64710
}
```

en la configuración de ese Caddy (fuera de este repositorio), recargando su configuración
después. Con eso `https://<tu-dominio>` debería servir el frontend, y
`https://<tu-dominio>/api/*` la API — el propio Caddy interno del stack (`web`) ya reenvía
`/api/*` hacia la API.

## Primer inicio de sesión

No hay contraseñas: el acceso es por enlace mágico. Ve a `https://<tu-dominio>/login` e
introduce el email y el teléfono que pusiste en `SEED_ADMIN_EMAIL`/`SEED_ADMIN_PHONE`.

Si no has configurado `SMTP_*` en `.env`, el enlace no se envía por correo real — se
registra en los logs de la API:

```sh
docker compose -f docker-compose.production.yml logs -f api
```

Esto es una opción válida (no solo "de desarrollo") para una prueba inicial con pocas
personas: cada vez que alguien inicie sesión, revisa los logs y pásale el enlace. Cuando
quieras enviar correos de verdad, rellena las variables `SMTP_*` en `.env` y reinicia el
servicio `api` (`docker compose -f docker-compose.production.yml up -d api`).

**No uses `scripts/get-token.sh`** para generar tokens en este entorno — su propia
cabecera indica que solo está pensado para el stack de desarrollo local, nunca para nada
parecido a producción.

## Actualizar a una versión nueva

Publicar una versión nueva del código es un paso manual: no hay redeploy automático en
cada `git push`. En la máquina de producción:

```sh
git pull
docker compose -f docker-compose.production.yml build api
docker compose -f docker-compose.production.yml --profile tools run --rm migrate
docker compose -f docker-compose.production.yml up -d
```

El frontend (`web/`) no necesita reconstruirse ni reiniciar Caddy — se sirve directamente
desde el árbol de ficheros bind-mounted, igual que en desarrollo.

## Copias de seguridad

Nada de esto ocurre automáticamente; hazlo con la periodicidad que necesites (por ejemplo
desde una tarea de `cron`).

Base de datos:

```sh
docker compose -f docker-compose.production.yml exec postgres \
  pg_dump -U intranet intranet > backup-$(date +%Y%m%d).sql
```

Ficheros: `./library` y `./user-files` son directorios normales en el host (haz una copia
como la de cualquier otro fichero); el contenido de las memorias de actividad vive en el
volumen con nombre `activity_resources_data`, por ejemplo:

```sh
docker run --rm -v katiuskas-intranet_activity_resources_data:/data -v "$(pwd)":/backup \
  alpine tar czf /backup/activity-resources-$(date +%Y%m%d).tar.gz -C /data .
```

(el prefijo `katiuskas-intranet_` en el nombre del volumen depende del nombre del
directorio del proyecto — compruébalo con `docker volume ls` si no coincide).

## Notas de seguridad

- Cambia `SEED_ADMIN_EMAIL`/`SEED_ADMIN_PHONE` en `.env` antes del primer despliegue: los
  valores por defecto de `cmd/seed-admin` (`admin@club.test` / `600000000`) son de sobra
  conocidos y esa cuenta puede asignar el resto de roles.
- `POSTGRES_PASSWORD` no necesita recordarse ni compartirse — solo la usan entre sí los
  contenedores `api` y `postgres` — así que puede (y debe) ser un valor largo y aleatorio.
- Ni PostgreSQL ni la API quedan expuestos directamente a Internet en
  `docker-compose.production.yml`: `web` es el único servicio con un puerto publicado, y
  solo en `127.0.0.1:64710` — todo el tráfico externo tiene que pasar antes por el
  reverse proxy de la máquina.
