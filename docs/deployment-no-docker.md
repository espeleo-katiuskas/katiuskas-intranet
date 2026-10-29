# Production deployment without Docker (server with little disk)

An alternative to [`docs/deployment.md`](deployment.md) for a self-hosted server with little disk
space, where the cost of Docker (the engine itself, plus the PostgreSQL and Caddy images and the Go
toolchain of every build) is not worth it. Here everything runs as a native process managed by
`systemd`: PostgreSQL installed from the distribution's package, and two static Go binaries (the API
and `goose` for the migrations) built on another machine and copied over with `scp` — the server
never needs the Go toolchain installed, not even during an update.

Written for Debian/Ubuntu (`apt`); on another distribution the package names and the package manager
change (`dnf`, `pacman`...), but the structure is the same.

If the server already has its own Caddy handling HTTPS for other domains (the most common case),
there is no need to install a new one or to start an intermediate internal proxy: just add one more
site to that same Caddy (see step 5), which serves the frontend from disk and acts as a reverse
proxy to the API at `127.0.0.1:64711` — the same Caddy you already use for everything else. If
instead the existing reverse proxy is something else (nginx, Traefik...) that cannot serve static
files and forward `/api/*` as easily, step 5 also explains the alternative of a local Caddy of its
own at `127.0.0.1:64710` — the same entry point the Docker guide uses.

> **Several environments on one host.** This is the guide the club's host follows: it runs testing
> and production side by side on one machine, each updated automatically (the host pulls `main`, and
> semver tags, respectively). To install a second environment, repeat this guide with its own
> directory, unit, database and ports — see [`environments.md`](environments.md) for what must be
> different and what is shared. (On the club's host those are `/opt/katiuskas-intranet-stable` and
> `/opt/katiuskas-intranet-testing`, with the units `katiuskas-intranet-stable.service` and
> `katiuskas-intranet-testing.service`; this guide's `/opt/katiuskas-intranet` and `katiuskas-api`
> are just generic names.)

## Overview of the target machine

```
/opt/katiuskas-intranet/
├── bin/
│   ├── katiuskas-api      # built on another machine, copied with scp
│   ├── seed-admin         # same
│   └── goose              # same — only needed to migrate, not on every start
├── web/                   # static copy of the repository's web/
├── db/migrations/         # copy of the repository's db/migrations/ (only the .sql files)
├── data/
│   ├── library/           # LIBRARY_ROOT
│   ├── user-files/        # USER_FILES_ROOT
│   └── activity-resources/ # ACTIVITY_RESOURCES_ROOT
└── api.env                # environment variables of the systemd service, not readable by others
```

Everything owned by a dedicated system user, with no login shell:

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
sudo -u postgres psql -c "CREATE ROLE intranet WITH LOGIN PASSWORD '<long-random-password>';"
sudo -u postgres psql -c "CREATE DATABASE intranet OWNER intranet;"
```

The Debian/Ubuntu package already leaves PostgreSQL listening only on `localhost` and accepting
password connections from there (`pg_hba.conf`, method `scram-sha-256`/`md5` for
`host ... 127.0.0.1/32`) — there is no need to touch the configuration unless your installation has
already modified it. Check it with:

```sh
psql "postgres://intranet:<password>@localhost:5432/intranet?sslmode=disable" -c '\conninfo'
```

## 2. Building the binaries (on another machine)

On any machine with Go installed — your own computer, for example — with the repository cloned:

```sh
cd backend

# Server architecture: check with `uname -m` on the server.
#   x86_64  → GOARCH=amd64
#   aarch64 → GOARCH=arm64
export GOOS=linux GOARCH=amd64 CGO_ENABLED=0

go build -o katiuskas-api ./cmd/api
go build -o seed-admin ./cmd/seed-admin
# The version shown by Admin → Estado del sistema is recorded by Go itself, from
# git (the tag, or a commit after it, and `+dirty` if there are uncommitted
# changes): you have to build from the clone, with `git` installed.

# goose is not a dependency of the module (see go.mod) — it is built separately,
# pinned to v3.22.1 instead of `@latest`, so that building this binary always
# gives the same goose no matter when or where it is done. There is no need to
# install goose permanently on any machine; just build this binary once.
GOBIN="$(pwd)" go install github.com/pressly/goose/v3/cmd/goose@v3.22.1
```

All three are static binaries (`CGO_ENABLED=0`): they do not depend on any library of the server's
system, only the operating system and the architecture have to match.

Copy them, together with the frontend and the migrations, to the server:

```sh
scp katiuskas-api seed-admin goose user@server:/opt/katiuskas-intranet/bin/
scp -r ../web/. user@server:/opt/katiuskas-intranet/web/
scp -r ../db/migrations/. user@server:/opt/katiuskas-intranet/db/migrations/
ssh user@server sudo chown -R katiuskas:katiuskas /opt/katiuskas-intranet
```

Since `/opt/katiuskas-intranet` is owned by `katiuskas`, the user you `scp` as needs write
permission there (for example, a common group with `g+w` permissions on those directories);
otherwise, copy the files to their own `$HOME` and move them afterwards with `sudo`.

## 3. Migrations and first admin account

From the server (or remotely, with `DATABASE_URL` pointing at it if PostgreSQL were reachable from
outside — it is not the case here, so do it on the server itself):

```sh
export DATABASE_URL="postgres://intranet:<password>@localhost:5432/intranet?sslmode=disable"

sudo -u katiuskas \
  /opt/katiuskas-intranet/bin/goose -dir /opt/katiuskas-intranet/db/migrations postgres "$DATABASE_URL" up

sudo -u katiuskas env DATABASE_URL="$DATABASE_URL" \
  SEED_ADMIN_EMAIL=your-real-email@yourdomain.org SEED_ADMIN_PHONE=600111222 \
  /opt/katiuskas-intranet/bin/seed-admin
```

Do not leave the default values of `cmd/seed-admin` (`admin@club.test` / `600000000`): they are far
too well known and that account can assign all the other roles. `seed-admin` is idempotent — if the
email already exists, it does nothing.

## 4. systemd service for the API

`/opt/katiuskas-intranet/api.env` (readable only by `katiuskas`: `chmod 600`):

```sh
sudo tee /opt/katiuskas-intranet/api.env >/dev/null <<'EOF'
HTTP_ADDR=127.0.0.1:64711
DATABASE_URL=postgres://intranet:<password>@localhost:5432/intranet?sslmode=disable
PUBLIC_BASE_URL=https://intranet.tudominio.org
LIBRARY_ROOT=/opt/katiuskas-intranet/data/library
USER_FILES_ROOT=/opt/katiuskas-intranet/data/user-files
ACTIVITY_RESOURCES_ROOT=/opt/katiuskas-intranet/data/activity-resources
# Leave the four SMTP_* commented out to send no real email: the magic
# sign-in links are then logged in `journalctl -u katiuskas-api`.
#SMTP_HOST=smtp.tudominio.org
#SMTP_PORT=587
#SMTP_USERNAME=
#SMTP_PASSWORD=
#SMTP_FROM=intranet@tudominio.org
# Leave the two TELEGRAM_* commented out to not use Telegram at all:
# unexpected API errors are only logged in
# `journalctl -u katiuskas-api`, as before, with no notice of
# logins or of each creation/edit/removal, and the bot does not react to
# anyone who writes to it. Fill them in to activate everything: a notice
# every time the API starts, errors, a notice for each sign-in, the sign-in
# link and code also by Telegram (in addition to the email) to whoever
# has the account linked, a message of its own for each person/member/
# role/activity/equipment/... that is created, edited or deleted (who did it
# and, for an edit, what changed), and the bot asking for the contact of
# anyone who writes to it without being linked yet, to associate their
# Telegram account with their phone.
#TELEGRAM_BOT_TOKEN=
#TELEGRAM_LOG_CHAT_ID=
# TELEGRAM_BOT_USERNAME is independent of the two above: the bot's public
# name (without the "@"), only so that the sign-in page offers "sign in
# with Telegram" with a direct link. It only makes sense if you also fill
# in TELEGRAM_BOT_TOKEN above.
#TELEGRAM_BOT_USERNAME=
# With Telegram configured, the API also warns there when the database stops
# answering, a directory goes missing or a disk is almost full. This is how
# full (in percent) a disk holding the three directories above may be before
# that; 90 if left out, 0 turns that one warning off.
#DISK_ALERT_PERCENT=90
EOF
sudo chown katiuskas:katiuskas /opt/katiuskas-intranet/api.env
sudo chmod 600 /opt/katiuskas-intranet/api.env
```

`HTTP_ADDR=127.0.0.1:64711`, unlike the `:64711` (all interfaces) of the development
`docker-compose.yml`: here there is no isolated Docker network separating the API from the rest of
the machine, so it has to be restricted to localhost explicitly — only Caddy, on the same host,
needs to talk to it.

systemd unit, `/etc/systemd/system/katiuskas-api.service`:

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

`enable` makes it start by itself when the machine reboots; `Restart=on-failure` makes it restart by
itself if the process dies — the exact equivalent of `restart: unless-stopped` in the Docker
version.

## 5. Caddy

### You already have Caddy on the machine (the usual case)

Do not install a second Caddy or an internal proxy in between: add one more site block to your
existing `Caddyfile` (or to the file it imports, if you use `import` to separate sites), with the
intranet's real domain. That same Caddy serves the frontend from disk, acts as a reverse proxy to
the API, and handles this domain's HTTPS exactly as it already does for your other sites:

```
intranet.tudominio.org {
	encode zstd gzip
	handle /api/* {
		reverse_proxy 127.0.0.1:64711
	}
	root * /opt/katiuskas-intranet/web
	handle {
		header Cache-Control "max-age=3600"
		try_files {path} {path}.html
		file_server
	}
	handle_errors {
		@notfound `{err.status_code} == 404`
		handle @notfound {
			rewrite * /404
			try_files {path} {path}.html
			header Cache-Control "no-cache"
			file_server
		}
	}
}
```

```sh
sudo systemctl reload caddy
```

The line `header Cache-Control "max-age=3600"` is not optional. With no cache header at all,
browsers work out for themselves how long a file is valid (a tenth of its age), so a file unchanged
for weeks can be served stale for days after a deployment. A fixed hour is a trade-off: the
alternative, `no-cache` (asking the server about every file, which answers with a `304` as long as
it has not changed), made changing page take almost a second over a real connection, because each
page drags along a chain of scripts (each one imports the next, so they load one after another) and
each one cost a round trip. In exchange, after a deployment a browser may keep an old page or script
for up to an hour, and mix them with new files (sections that never appear, forms that do not send
new fields...): whoever needs to see the change at once forces a refresh (how depends on the browser
and the device, and on a phone or a tablet there is no easy way: hence an hour and not a day). If
another period is needed, that is the number to change.

The line `encode zstd gzip` compresses what travels as text (the scripts, the styles and the API's
JSON: `GET /users` alone goes from over 100 KB) and leaves images and PDFs as they are, since they
are already compressed. Without it, each page downloads several times more bytes than necessary,
which is noticeable above all on a phone.

The `handle_errors` block makes any address that is not a page show `404.html` with a 404 status,
without redirecting: the address bar keeps what was typed. That is why `root` goes outside the
`handle`, at site level: `handle_errors` would not see the one inside. The API's responses
(`/api/*`) do not go through here and remain its own.

If that Caddy runs as a user that cannot read `/opt/katiuskas-intranet/web` (owned by `katiuskas`),
give it read access, for example by adding that user to the `katiuskas` group, or with `o+rX`
permissions on `/opt/katiuskas-intranet/web`.

### If the existing reverse proxy is not Caddy (nginx, Traefik...)

If your reverse proxy does not serve static files and forward `/api/*` as easily, install a local
Caddy dedicated only to this, listening on `127.0.0.1:64710`, and make your existing reverse proxy
forward the domain's traffic to it — the same entry point the Docker guide uses:

```sh
sudo apt install -y debian-keyring debian-archive-keyring apt-transport-https curl
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
  | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
  | sudo tee /etc/apt/sources.list.d/caddy-stable.list
sudo apt update
sudo apt install -y caddy
```

This already installs its own systemd service (`caddy.service`), reading `/etc/caddy/Caddyfile`.
Replace it with the equivalent of the repository's `deploy/Caddyfile`, but pointing at
`127.0.0.1:64711` instead of the Docker service name `api`, and explicitly bound to localhost only
(`127.0.0.1:64710`, not a bare `:64710` — without Docker's isolated network, `:64710` would listen
on all interfaces):

```sh
sudo tee /etc/caddy/Caddyfile >/dev/null <<'EOF'
127.0.0.1:64710 {
	encode zstd gzip
	handle /api/* {
		reverse_proxy 127.0.0.1:64711
	}
	root * /opt/katiuskas-intranet/web
	handle {
		header Cache-Control "max-age=3600"
		try_files {path} {path}.html
		file_server
	}
	handle_errors {
		@notfound `{err.status_code} == 404`
		handle @notfound {
			rewrite * /404
			try_files {path} {path}.html
			header Cache-Control "no-cache"
			file_server
		}
	}
}
EOF
sudo systemctl reload caddy
```

Then, in your existing reverse proxy (outside this repository), add a site for the chosen domain
that forwards to `http://127.0.0.1:64710` — the same step as in `docs/deployment.md`.

## Updating to a new version

Manual procedure. On the club's host, instead, a timer pulls `main` (testing) and the semver tags
(production) and updates them automatically — see [`environments.md`](environments.md).

On the build machine:

```sh
git pull
cd backend
export GOOS=linux GOARCH=amd64 CGO_ENABLED=0   # adjust GOARCH if needed
go build -o katiuskas-api ./cmd/api
```

On the server:

```sh
scp katiuskas-api user@server:/opt/katiuskas-intranet/bin/katiuskas-api.new
ssh user@server 'sudo -u katiuskas mv /opt/katiuskas-intranet/bin/katiuskas-api.new /opt/katiuskas-intranet/bin/katiuskas-api'
scp -r ../db/migrations/. user@server:/opt/katiuskas-intranet/db/migrations/
ssh user@server 'sudo -u katiuskas /opt/katiuskas-intranet/bin/goose -dir /opt/katiuskas-intranet/db/migrations postgres "postgres://intranet:<password>@localhost:5432/intranet?sslmode=disable" up'
ssh user@server 'sudo systemctl restart katiuskas-api'
```

The frontend (`web/`) does not need rebuilding or a Caddy restart — copy the tree again with
`scp -r ../web/. user@server:/opt/katiuskas-intranet/web/` if it has changed; Caddy serves it
directly from disk (with the one-hour cache described in step 5).

## Backups

The club's host backs up production every night with `scripts/backup.sh`, from a systemd timer
(see [`environments.md`](environments.md), "Backups", which also says how to restore). By hand,
the same as in `docs/deployment.md` but over real disk paths instead of Docker volumes:

```sh
sudo -u postgres pg_dump intranet > backup-$(date +%Y%m%d).sql
tar czf activity-resources-$(date +%Y%m%d).tar.gz -C /opt/katiuskas-intranet/data activity-resources
```

`/opt/katiuskas-intranet/data/library` and `/opt/katiuskas-intranet/data/user-files` are ordinary
directories — copy them like any other file on the system.

### License thumbnails

The copies of each person's licenses (`user-files/<login>/licencias/`) are shown in `/perfil` and
`/socio` with a thumbnail, which the application does not generate by itself. To create the missing
ones (and renew those older than their file), run the repository's script, as the user that owns the
files and pointing at the production directory:

```sh
sudo -u katiuskas scripts/generate-license-thumbnails.sh --root /opt/katiuskas-intranet/data/user-files
```

It needs `pdftoppm` (package `poppler-utils`) for PDFs and ImageMagick (`imagemagick`) for images;
`--help` lists the other options (`--dry-run`, `--force`, or just some logins). It is safe to run as
many times as needed: whatever is already up to date is skipped.

## Security notes

- Change `SEED_ADMIN_EMAIL`/`SEED_ADMIN_PHONE` before the first deployment, as in
  `docs/deployment.md`.
- `api.env` contains the PostgreSQL password — `chmod 600`, owned by `katiuskas`, never in the
  repository.
- Neither PostgreSQL nor the API listens on any external interface: PostgreSQL only accepts local
  connections and `HTTP_ADDR` is fixed to `127.0.0.1:64711` — all external traffic goes first
  through Caddy (your existing one, or a local one at `127.0.0.1:64710` if you use another reverse
  proxy — see step 5), as with Docker.
- **Do not use `scripts/get-token.sh`** here: besides being development-only, it depends on the
  local `docker compose` stack, which does not even exist in this deployment.
