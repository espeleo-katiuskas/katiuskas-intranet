# Despliegue en producción sin Docker (servidor con poco disco)

Alternativa a [`docs/deployment.md`](deployment.md) para un servidor propio con poco
espacio en disco, donde el coste de Docker (el propio motor, más las imágenes de
PostgreSQL, Caddy y el toolchain de Go de cada build) no compensa. Aquí todo corre como
proceso nativo gestionado por `systemd`: PostgreSQL instalado por el paquete de la
distribución, y dos binarios de Go estáticos (la API y `goose` para las migraciones)
compilados en otra máquina y copiados por `scp` — el servidor nunca necesita el toolchain
de Go instalado, ni siquiera durante una actualización.

Escrita para Debian/Ubuntu (`apt`); en otra distribución cambian los nombres de paquete y
el gestor (`dnf`, `pacman`...), pero la estructura es la misma.

Si el servidor ya tiene su propio Caddy gestionando HTTPS para otros dominios (el caso
más habitual), no hace falta instalar uno nuevo ni levantar un proxy interno intermedio:
basta con añadir un sitio más a ese mismo Caddy (ver el paso 5), que sirve el frontend
desde disco y hace de reverse proxy hacia la API en `127.0.0.1:64711` — el mismo Caddy
que ya usas para lo demás. Si en cambio el reverse proxy existente es otra cosa (nginx,
Traefik...) que no puede servir ficheros estáticos y reenviar `/api/*` con la misma
facilidad, el paso 5 explica también la alternativa de un Caddy local propio en
`127.0.0.1:64710` — el mismo punto de entrada que usa la guía con Docker.

## Resumen de la máquina de destino

```
/opt/katiuskas-intranet/
├── bin/
│   ├── katiuskas-api      # compilado en otra máquina, copiado por scp
│   ├── seed-admin         # idem
│   └── goose              # idem — solo hace falta para migrar, no en cada arranque
├── web/                   # copia estática de web/ del repositorio
├── db/migrations/         # copia de db/migrations/ del repositorio (solo los .sql)
├── data/
│   ├── library/           # LIBRARY_ROOT
│   ├── user-files/        # USER_FILES_ROOT
│   └── activity-resources/ # ACTIVITY_RESOURCES_ROOT
└── api.env                # variables de entorno del servicio systemd, no legible por otros
```

Todo propiedad de un usuario de sistema dedicado, sin shell de login:

```sh
sudo useradd --system --create-home --home-dir /opt/katiuskas-intranet \
  --shell /usr/sbin/nologin katiuskas
sudo mkdir -p /opt/katiuskas-intranet/{bin,web,db/migrations,data/library,data/user-files,data/activity-resources}
sudo chown -R katiuskas:katiuskas /opt/katiuskas-intranet
```

## 1. PostgreSQL

```sh
sudo apt update
sudo apt install -y postgresql
sudo -u postgres psql -c "CREATE ROLE intranet WITH LOGIN PASSWORD '<contraseña-larga-aleatoria>';"
sudo -u postgres psql -c "CREATE DATABASE intranet OWNER intranet;"
```

El paquete de Debian/Ubuntu ya deja PostgreSQL escuchando solo en `localhost` y acepta
conexiones por contraseña desde ahí (`pg_hba.conf`, método `scram-sha-256`/`md5` para
`host ... 127.0.0.1/32`) — no hace falta tocar la configuración salvo que tu instalación
la tenga ya modificada. Compruébalo con:

```sh
psql "postgres://intranet:<contraseña>@localhost:5432/intranet?sslmode=disable" -c '\conninfo'
```

## 2. Compilar los binarios (en otra máquina)

En cualquier máquina con Go instalado — tu propio ordenador, por ejemplo — con el
repositorio clonado:

```sh
cd backend

# Arquitectura del servidor: comprueba con `uname -m` en el servidor.
#   x86_64  → GOARCH=amd64
#   aarch64 → GOARCH=arm64
export GOOS=linux GOARCH=amd64 CGO_ENABLED=0

go build -o katiuskas-api ./cmd/api
go build -o seed-admin ./cmd/seed-admin

# goose no es una dependencia del módulo (ver go.mod) — se compila aparte,
# fijado a v3.22.1: es la última versión que sigue soportando Go 1.22 (la
# versión mínima que pide backend/go.mod). No hace falta instalar goose de
# forma permanente en ninguna máquina; solo compilar este binario una vez.
GOBIN="$(pwd)" go install github.com/pressly/goose/v3/cmd/goose@v3.22.1
```

Los tres son binarios estáticos (`CGO_ENABLED=0`): no dependen de ninguna librería del
sistema del servidor, solo hace falta que coincida el sistema operativo y la arquitectura.

Cópialos, junto con el frontend y las migraciones, al servidor:

```sh
scp katiuskas-api seed-admin goose usuario@servidor:/opt/katiuskas-intranet/bin/
scp -r ../web/. usuario@servidor:/opt/katiuskas-intranet/web/
scp -r ../db/migrations/. usuario@servidor:/opt/katiuskas-intranet/db/migrations/
ssh usuario@servidor sudo chown -R katiuskas:katiuskas /opt/katiuskas-intranet
```

## 3. Migraciones y primera cuenta de administración

Desde el servidor (o en remoto, con `DATABASE_URL` apuntando a él si PostgreSQL fuera
alcanzable desde fuera — no es el caso aquí, así que hazlo en el propio servidor):

```sh
export DATABASE_URL="postgres://intranet:<contraseña>@localhost:5432/intranet?sslmode=disable"

sudo -u katiuskas DATABASE_URL="$DATABASE_URL" \
  /opt/katiuskas-intranet/bin/goose -dir /opt/katiuskas-intranet/db/migrations postgres "$DATABASE_URL" up

sudo -u katiuskas env DATABASE_URL="$DATABASE_URL" \
  SEED_ADMIN_EMAIL=tu-email-real@tudominio.org SEED_ADMIN_PHONE=600111222 \
  /opt/katiuskas-intranet/bin/seed-admin
```

No dejes los valores por defecto de `cmd/seed-admin` (`admin@club.test` / `600000000`):
son de sobra conocidos y esa cuenta puede asignar el resto de roles. `seed-admin` es
idempotente — si el email ya existe, no hace nada.

## 4. Servicio systemd de la API

`/opt/katiuskas-intranet/api.env` (solo legible por `katiuskas`: `chmod 600`):

```sh
sudo tee /opt/katiuskas-intranet/api.env >/dev/null <<'EOF'
HTTP_ADDR=127.0.0.1:64711
DATABASE_URL=postgres://intranet:<contraseña>@localhost:5432/intranet?sslmode=disable
PUBLIC_BASE_URL=https://intranet.tudominio.org
LIBRARY_ROOT=/opt/katiuskas-intranet/data/library
USER_FILES_ROOT=/opt/katiuskas-intranet/data/user-files
ACTIVITY_RESOURCES_ROOT=/opt/katiuskas-intranet/data/activity-resources
# Deja las cuatro SMTP_* comentadas para no enviar correo real: los enlaces
# mágicos de acceso se registran entonces en `journalctl -u katiuskas-api`.
#SMTP_HOST=smtp.tudominio.org
#SMTP_PORT=587
#SMTP_USERNAME=
#SMTP_PASSWORD=
#SMTP_FROM=intranet@tudominio.org
# Deja las dos TELEGRAM_* comentadas para no usar Telegram en absoluto:
# los errores inesperados del API solo se registran en
# `journalctl -u katiuskas-api`, igual que hasta ahora, sin aviso de
# logins ni de llamadas al API, y el bot no reacciona a nadie que le
# escriba. Rellénalas para activarlo todo: un aviso cada vez que arranca
# el API, errores, un aviso por cada inicio de sesión, un aviso por cada
# llamada al API que sea POST/PUT/PATCH/DELETE, y el bot pidiendo el
# contacto a quien le escriba sin estar ya vinculado para asociar su
# cuenta de Telegram con su teléfono.
#TELEGRAM_BOT_TOKEN=
#TELEGRAM_LOG_CHAT_ID=
EOF
sudo chown katiuskas:katiuskas /opt/katiuskas-intranet/api.env
sudo chmod 600 /opt/katiuskas-intranet/api.env
```

`HTTP_ADDR=127.0.0.1:64711`, a diferencia del `:64711` (todas las interfaces) del
`docker-compose.yml` de desarrollo: aquí no hay red aislada de Docker separando la API
del resto de la máquina, así que hay que restringirla a localhost explícitamente — solo
Caddy, en el mismo host, necesita hablar con ella.

Unidad systemd, `/etc/systemd/system/katiuskas-api.service`:

```ini
[Unit]
Description=Katiuskas Intranet API
After=network.target postgresql.service

[Service]
Type=simple
User=katiuskas
Group=katiuskas
WorkingDirectory=/opt/katiuskas-intranet
EnvironmentFile=/opt/katiuskas-intranet/api.env
ExecStart=/opt/katiuskas-intranet/bin/katiuskas-api
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
```

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now katiuskas-api
sudo systemctl status katiuskas-api
```

`enable` hace que arranque solo al reiniciar la máquina; `Restart=on-failure` hace que se
reinicie solo si el proceso cae — el equivalente exacto de `restart: unless-stopped` en la
versión con Docker.

## 5. Caddy

### Ya tienes Caddy en la máquina (caso habitual)

No instales un segundo Caddy ni un proxy interno de por medio: añade un bloque de sitio
más a tu `Caddyfile` existente (o al fichero que importe, si usas `import` para separar
sitios), con el dominio real de la intranet. Ese mismo Caddy sirve el frontend desde
disco, hace de reverse proxy hacia la API, y gestiona el HTTPS de este dominio exactamente
igual que ya hace con tus otros sitios:

```
intranet.tudominio.org {
	handle /api/* {
		reverse_proxy 127.0.0.1:64711
	}
	handle {
		root * /opt/katiuskas-intranet/web
		try_files {path} {path}.html
		file_server
	}
}
```

```sh
sudo systemctl reload caddy
```

Si ese Caddy corre como un usuario que no puede leer `/opt/katiuskas-intranet/web`
(propiedad de `katiuskas`), dale acceso de lectura, por ejemplo añadiendo a ese usuario al
grupo `katiuskas`, o con permisos `o+rX` en `/opt/katiuskas-intranet/web`.

### Si el reverse proxy existente no es Caddy (nginx, Traefik...)

Si tu reverse proxy no sirve ficheros estáticos y reenvía `/api/*` con la misma
facilidad, instala un Caddy local dedicado solo para esto, escuchando en
`127.0.0.1:64710`, y haz que tu reverse proxy existente le reenvíe el tráfico del dominio
— el mismo punto de entrada que usa la guía con Docker:

```sh
sudo apt install -y debian-keyring debian-archive-keyring apt-transport-https curl
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
  | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
  | sudo tee /etc/apt/sources.list.d/caddy-stable.list
sudo apt update
sudo apt install -y caddy
```

Esto ya instala su propio servicio systemd (`caddy.service`), leyendo
`/etc/caddy/Caddyfile`. Sustitúyelo por el equivalente de `deploy/Caddyfile` del
repositorio, pero apuntando a `127.0.0.1:64711` en vez del nombre de servicio Docker
`api`, y explícitamente ligado solo a localhost (`127.0.0.1:64710`, no `:64710` a secas
— sin la red aislada de Docker, `:64710` escucharía en todas las interfaces):

```sh
sudo tee /etc/caddy/Caddyfile >/dev/null <<'EOF'
127.0.0.1:64710 {
	handle /api/* {
		reverse_proxy 127.0.0.1:64711
	}
	handle {
		root * /opt/katiuskas-intranet/web
		try_files {path} {path}.html
		file_server
	}
}
EOF
sudo systemctl reload caddy
```

Luego, en tu reverse proxy existente (fuera de este repositorio), añade un sitio para el
dominio elegido que reenvíe a `http://127.0.0.1:64710` — el mismo paso que en
`docs/deployment.md`.

## Actualizar a una versión nueva

En la máquina de compilación:

```sh
git pull
cd backend
export GOOS=linux GOARCH=amd64 CGO_ENABLED=0   # ajusta GOARCH si hace falta
go build -o katiuskas-api ./cmd/api
```

En el servidor:

```sh
scp katiuskas-api usuario@servidor:/opt/katiuskas-intranet/bin/katiuskas-api.new
ssh usuario@servidor 'sudo -u katiuskas mv /opt/katiuskas-intranet/bin/katiuskas-api.new /opt/katiuskas-intranet/bin/katiuskas-api'
scp -r db/migrations/. usuario@servidor:/opt/katiuskas-intranet/db/migrations/
ssh usuario@servidor 'sudo -u katiuskas DATABASE_URL="postgres://intranet:<contraseña>@localhost:5432/intranet?sslmode=disable" /opt/katiuskas-intranet/bin/goose -dir /opt/katiuskas-intranet/db/migrations postgres "$DATABASE_URL" up'
ssh usuario@servidor 'sudo systemctl restart katiuskas-api'
```

El frontend (`web/`) no necesita reconstruirse ni reiniciar Caddy — vuelve a copiar el
árbol con `scp -r` si ha cambiado; Caddy lo sirve directamente desde disco.

## Copias de seguridad

Igual que en `docs/deployment.md`, pero sobre rutas de disco reales en vez de volúmenes
Docker:

```sh
sudo -u postgres pg_dump intranet > backup-$(date +%Y%m%d).sql
tar czf activity-resources-$(date +%Y%m%d).tar.gz -C /opt/katiuskas-intranet/data activity-resources
```

`/opt/katiuskas-intranet/data/library` y `/opt/katiuskas-intranet/data/user-files` son
directorios normales — cópialos como cualquier otro fichero del sistema.

## Notas de seguridad

- Cambia `SEED_ADMIN_EMAIL`/`SEED_ADMIN_PHONE` antes del primer despliegue, igual que en
  `docs/deployment.md`.
- `api.env` contiene la contraseña de PostgreSQL — `chmod 600`, propiedad de `katiuskas`,
  nunca en el repositorio.
- Ni PostgreSQL ni la API escuchan en ninguna interfaz externa: PostgreSQL solo acepta
  conexiones locales y `HTTP_ADDR` está fijado a `127.0.0.1:64711` — todo el tráfico
  externo pasa antes por Caddy (el tuyo ya existente, o uno local en `127.0.0.1:64710`
  si usas otro reverse proxy — ver paso 5), igual que con Docker.
- **No uses `scripts/get-token.sh`** aquí: además de ser solo para desarrollo, depende
  del stack de `docker compose` local, que ni siquiera existe en este despliegue.
