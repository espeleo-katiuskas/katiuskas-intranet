# Production deployment (own machine)

Guide to getting the intranet running on a machine of your own (not a cloud service) that
already has its own reverse proxy (Caddy or another) handling HTTPS for several domains. The
intranet stack does not manage TLS or certificates: the `web` container listens in plain text
only on `127.0.0.1:64710` (the same as in development), and it is the reverse proxy already on
the machine that forwards it the traffic of the domain you choose. Meant for an initial trial
with a small number of people.

It requires Docker on the machine. If disk space is limited and you would rather not pay the
cost of Docker (the engine plus the PostgreSQL/Caddy/Go images), see
[`docs/deployment-no-docker.md`](deployment-no-docker.md) instead: the same idea with native
processes managed by `systemd`.

> **Note.** The club's own host does not use this guide: it runs its testing and production
> environments as systemd units, without Docker (see
> [`deployment-no-docker.md`](deployment-no-docker.md) and
> [`environments.md`](environments.md)), and there updates are automatic. This guide is the
> Docker alternative, and everything below is run by hand. The containers do restart on their
> own if they crash or when the machine boots again (`restart: unless-stopped` in
> `docker-compose.production.yml`).

## Prerequisites

- Docker and the `docker compose` plugin installed on the machine (`docker compose version`).
  You need no Go, goose, or anything else — everything else runs inside containers.
- A reverse proxy already working on the machine (the same Caddy you use for your other
  domains, for example) able to serve HTTPS for the domain you want to use here and forward
  that traffic to `http://127.0.0.1:64710`. That reverse proxy is your responsibility, outside
  this repository — here we only document which port to point it at.
- No port of this stack needs to be opened directly to the Internet:
  `docker-compose.production.yml` does not publish PostgreSQL or the API, and `web` (Caddy)
  only listens on `127.0.0.1:64710`, the same as in development — nothing reaches the machine
  from outside without going through your reverse proxy first.

## First deployment

Clone the repository on the machine and go to its root:

```sh
git clone <repository-url> katiuskas-intranet
cd katiuskas-intranet
```

Create the configuration file from the template and fill in the real values (domain,
PostgreSQL password, email/phone of the first admin account — see the comments on each
variable in the template itself):

```sh
cp .env.production.example .env
$EDITOR .env
```

`.env` is already in `.gitignore`: it is never pushed to the repository.

Start only PostgreSQL first, apply the migrations and create the first admin account, and then
bring up the rest of the stack:

```sh
docker compose -f docker-compose.production.yml up -d postgres
docker compose -f docker-compose.production.yml --profile tools run --rm migrate
docker compose -f docker-compose.production.yml --profile tools run --rm seed-admin
docker compose -f docker-compose.production.yml up -d
```

`migrate` and `seed-admin` are one-off tasks (profile `tools`, they do not start with a plain
`up -d`) that build the backend up to its intermediate stage with the full Go toolchain — they
need outbound Internet access the first time, to download `goose` and the Go modules.
`seed-admin` is idempotent: if the email already exists, it does nothing, so running it again
by mistake has no effect.

Check that everything has started and that it responds on localhost:

```sh
docker compose -f docker-compose.production.yml ps
curl -I http://127.0.0.1:64710/
```

Finally, add a site to your existing reverse proxy for the chosen domain (the same one you put
in `DOMAIN`) that forwards to `http://127.0.0.1:64710`. For example, if that reverse proxy is
also Caddy, a block like this would be enough:

```
intranet.tudominio.org {
	reverse_proxy 127.0.0.1:64710
}
```

in the configuration of that Caddy (outside this repository), reloading its configuration
afterwards. With that, `https://<your-domain>` should serve the frontend, and
`https://<your-domain>/api/*` the API — the stack's own internal Caddy (`web`) already
forwards `/api/*` to the API.

## First sign-in

There are no passwords: sign-in is by magic link. Go to `https://<your-domain>/login` and
enter the email and phone you put in `SEED_ADMIN_EMAIL`/`SEED_ADMIN_PHONE`.

If you have not configured `SMTP_*` in `.env`, the link is not sent by real email — it is
logged in the API logs:

```sh
docker compose -f docker-compose.production.yml logs -f api
```

This is a valid option (not just a "development" one) for an initial trial with few people:
every time someone signs in, check the logs and pass them the link. When you want to send real
emails, fill in the `SMTP_*` variables in `.env` and restart the `api` service
(`docker compose -f docker-compose.production.yml up -d api`).

**Do not use `scripts/get-token.sh`** to generate tokens in this environment — its own header
says it is only meant for the local development stack, never for anything resembling
production.

## Updating to a new version

Manual procedure: the automatic updates of the club's host (see
[`environments.md`](environments.md)) are for the variant without Docker; with Docker it is
done by hand. On the production machine:

```sh
git pull
docker compose -f docker-compose.production.yml build api
docker compose -f docker-compose.production.yml --profile tools run --rm migrate
docker compose -f docker-compose.production.yml up -d
```

The frontend (`web/`) does not need rebuilding or a Caddy restart — it is served directly from
the bind-mounted file tree, the same as in development. However, `deploy/Caddyfile` sends
`Cache-Control: max-age=3600` with everything it serves from `web/`, so a browser may keep
using an old page or script for up to an hour after updating.

## Backups

None of this happens automatically; do it as often as you need (for example from a `cron`
job).

Database:

```sh
docker compose -f docker-compose.production.yml exec postgres \
  pg_dump -U intranet intranet > backup-$(date +%Y%m%d).sql
```

Files: `./library` and `./user-files` are ordinary directories on the host (back them up like
any other file); the content of the activity memorias and the documents attached to transactions
live in the named volumes `activity_resources_data` and `transaction_files_data`, for example:

```sh
docker run --rm -v katiuskas-intranet_activity_resources_data:/data -v "$(pwd)":/backup \
  alpine tar czf /backup/activity-resources-$(date +%Y%m%d).tar.gz -C /data .
docker run --rm -v katiuskas-intranet_transaction_files_data:/data -v "$(pwd)":/backup \
  alpine tar czf /backup/transaction-files-$(date +%Y%m%d).tar.gz -C /data .
```

(the `katiuskas-intranet_` prefix in the volume name depends on the name of the project
directory — check it with `docker volume ls` if it does not match).

## Security notes

- Change `SEED_ADMIN_EMAIL`/`SEED_ADMIN_PHONE` in `.env` before the first deployment: the
  default values of `cmd/seed-admin` (`admin@club.test` / `600000000`) are all too well known
  and that account can assign the rest of the roles.
- `POSTGRES_PASSWORD` does not need to be remembered or shared — only the `api` and `postgres`
  containers use it, between themselves — so it can (and should) be a long random value.
- Neither PostgreSQL nor the API is exposed directly to the Internet in
  `docker-compose.production.yml`: `web` is the only service with a published port, and only
  on `127.0.0.1:64710` — all external traffic has to go through the machine's reverse proxy
  first.
