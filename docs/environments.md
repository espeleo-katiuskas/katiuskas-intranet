# Environments: local, testing and production

The project runs in three environments:

| | Local | Testing | Production |
|---|---|---|---|
| Where | development machine | the club's host | the club's host (the same machine as testing) |
| How it runs | Docker (`docker-compose.yml`) | systemd unit, without Docker | systemd unit, without Docker |
| URL | `http://localhost:64710` | `https://testing.katiuskas.es` | `https://intranet.katiuskas.es` |
| What for | trying changes before committing or pushing | trying what is already in `main` | the real members |
| Updated by | `docker compose up --build`, by hand | the host, when `main` changes | the host, when a new semver tag appears |
| Database | its own, disposable | its own | its own |
| Data | whatever is created locally | a copy of production's | the real data |
| Telegram bot | optional | its own | its own |
| `LIBRARY_ROOT`, `USER_FILES_ROOT` | its own | shared with production | shared with testing |
| `ACTIVITY_RESOURCES_ROOT`, `TRANSACTION_FILES_ROOT` | its own | its own (production's files, hard-linked by the copy) | its own |

On the club's host the two environments are installed in `/opt/katiuskas-intranet-testing` and
`/opt/katiuskas-intranet-stable` (the production one is called "stable" there), run as the
systemd units `katiuskas-intranet-testing.service` and `katiuskas-intranet-stable.service`, and
the tool that updates them lives in `/opt/katiuskas-intranet-deploy` (the machine also has other
things of the club, hence the names). The examples in this page use those names; the deployment
guide uses generic ones (`/opt/katiuskas-intranet`, `katiuskas-api`).

The local environment shares nothing with the other two; it is brought up as explained in the
"Quick start" section of the [`README.md`](../README.md). Testing and production are installed
following [`deployment.md`](deployment.md); the rest of this page covers
what is specific to having those two on one machine.

## How updates reach each environment

- **Local:** by hand, with `docker compose up --build` (and `docker compose build api && docker
  compose up -d api` to rebuild only the API).
- **Testing follows `main`.** When the tip of `main` is not what testing runs, the host builds that
  commit and installs it. So reaching `main` is a deployment, not just a way of publishing commits:
  whatever gets there is running on `testing.katiuskas.es` as soon as the build finishes (GitHub's
  webhook wakes the host up within seconds of the push; if that fails, a timer finds it within 15
  minutes).
- **Production follows the highest tag whose name is a semantic version (`MAJOR.MINOR.PATCH`,
  with an optional leading `v`)**, and runs the commit that tag points at. Nothing else changes
  production: `main` never reaches it. To roll back, tag a new, higher version.
- Since testing follows `main` and production only moves with tags, the natural flow for a
  release is to tag a commit that has already been running in testing for a while:

  ```sh
  git tag <version> <commit>
  git push origin <version>
  ```

Nothing is pushed to the host: it pulls. See "Pull-based deployment" below for how it works and how
to set it up. The only workflow in the repository, `.github/workflows/ci.yml`, checks the code on
pushes to `main` and on pull requests — Go: `gofmt`, build, `go vet`, `staticcheck` and the tests
with `-race`; a separate job for `govulncheck`; the frontend: `node --check` and `biome ci` over
`web/js`; and it validates the OpenAPI and builds the Docker image — and it does not run on tag
pushes and deploys nothing. They are the same checks that are run before a commit, so a commit that
skipped one still fails here. The manual procedure in the deployment guide remains the reference for
what an update involves.

## Pull-based deployment

[`scripts/deploy.sh`](../scripts/deploy.sh) updates testing and production from the repository.
Two things start it, and both start the same systemd unit, `katiuskas-intranet-deploy.service`: a
timer every 15 minutes, which is the safety net, and a small webhook receiver whenever GitHub
reports a push, which is what makes an update start within seconds. Because it is always that unit
that runs it, everything the script prints is in one place, the unit's journal (`journalctl -u
katiuskas-intranet-deploy`), whoever asked for the run, which is where to look when a deployment
fails.

The repository is public, so what matters is that **the webhook does not decide anything**. The
script never uses what the request says: it asks the repository by itself (`git ls-remote`, `git
fetch`, read-only, to its fixed URL) what each environment should run. A forged request can at most
start a run that finds nothing new, which costs one `git ls-remote`; the receiver still checks
GitHub's signature, so that not just anybody can make the host query GitHub over and over. There
is no CI runner with access to the host either.

**Requests are queued, not lost.** The receiver does not run anything: it creates an empty file,
`queue/pending` under `STATE_DIR`, and a systemd path unit
([`katiuskas-intranet-deploy.path`](../deploy/systemd/katiuskas-intranet-deploy.path)) starts the
service whenever that file exists. The service removes the file as it starts, so a push that lands
while a deployment is building creates it again and the unit is started once more right after the
run, and ten pushes in a row cost one more deployment, not ten. The receiver needs no `sudo` and has
nothing to wait for (GitHub gives up after ten seconds): creating the file is all it does. The
timer starts the same unit; if one is already running, that start does nothing.

Each run asks the repository for the tip of `main` and the tags, works out what each environment
should run (the tip of `main` for testing, the highest version tag for production) and compares it
with what it last deployed (`STATE_DIR/<environment>/deployed`). If both are the same, that was
all: one `git ls-remote`. Otherwise it fetches and updates, one after the other, the environments
that need it, testing first. For each one it:

1. checks the commit out of its own clone (for production, only if the tagged commit is in the
   history of `main`; a tag anywhere else is refused);
2. builds the API and `seed-admin` with Go, and goose (pinned, built once);
3. applies the migrations with goose, before touching what is installed, so that if one fails what
   runs is untouched;
4. installs the binaries, `web/` and the migrations (the directories are swapped, not copied in
   place) and restarts the API's unit;
5. polls the health check until it answers, and if it does not, puts the previous binary back
   (migrations are forward-only and stay applied);
6. records what it deployed and, if there is an `AFTER_DEPLOY` hook, runs it.

A failure in one environment does not stop the other: the run goes on with it and exits with an
error at the end (so the unit shows as failed). A commit that fails is not tried again for 15
minutes (`RETRY_AFTER`), so a broken commit does not rebuild at every request; a new commit or
`--force` tries immediately. The script says what it does in the unit's journal (`journalctl -u
katiuskas-intranet-deploy`, however the run was started).

**Setting it up** (once, on the club's host, as root):

```sh
# The host builds, so it needs git and Go (the toolchain is not downloaded: it has to be at
# least the version go.mod asks for). The build and the Go caches take a few hundred MB;
# `STATE_DIR/cache` can be deleted whenever disk is needed (`chmod -R u+w` it first: Go makes
# its module cache read-only), and the next run rebuilds it.
sudo mkdir -p /opt/katiuskas-intranet-deploy /var/lib/katiuskas-intranet-deploy/queue
sudo chown -R intranet:intranet /var/lib/katiuskas-intranet-deploy
# The webhook receiver's user owns nothing and only has to be able to create a file in
# the queue directory.
sudo useradd --system --no-create-home --shell /usr/sbin/nologin katiuskas-intranet-webhook
sudo chgrp katiuskas-intranet-webhook /var/lib/katiuskas-intranet-deploy/queue
sudo chmod 0770 /var/lib/katiuskas-intranet-deploy/queue

# Root-owned copies, so that what the timer and the webhook make run is not something the
# unprivileged user (or the code it builds) can change. Install them again when these files change.
sudo install -m 0755 scripts/deploy.sh scripts/copy-prod-to-testing.sh \
  /opt/katiuskas-intranet-deploy/
sudo install -m 0755 deploy/systemd/after-production.sh.example \
  /opt/katiuskas-intranet-deploy/after-production.sh

# The settings, next to the scripts: copy the example and check REPO_URL, the directories and
# the units. deploy.sh sources the file, so it and /opt/katiuskas-intranet-deploy are root's and the
# intranet user can only read it (deploy.sh refuses it otherwise). It has no secrets: those
# stay in each environment's api.env.
sudo install -m 0640 -g intranet deploy/systemd/deploy.env.example \
  /opt/katiuskas-intranet-deploy/deploy.env

sudo install -m 0644 deploy/systemd/katiuskas-intranet-deploy.service \
  deploy/systemd/katiuskas-intranet-deploy.timer deploy/systemd/katiuskas-intranet-deploy.path \
  /etc/systemd/system/
sudo systemctl daemon-reload
```

The only privileged things are restarting the APIs' units and, for the copy of production's data
into testing, stopping and starting testing's. A sudoers rule (`sudo visudo -f
/etc/sudoers.d/katiuskas-intranet-deploy`) allows exactly that and nothing else. The webhook
receiver runs as another user, `katiuskas-intranet-webhook`, which owns nothing of the installations
and needs no `sudo` at all (it only creates a file):

```
intranet ALL=(root) NOPASSWD: /usr/bin/systemctl restart katiuskas-intranet-stable.service, \
  /usr/bin/systemctl restart katiuskas-intranet-testing.service, \
  /usr/bin/systemctl stop katiuskas-intranet-testing.service, \
  /usr/bin/systemctl start katiuskas-intranet-testing.service
```

Before starting the timer, say what each environment is running now, so that the first run does
not deploy it again (and, for production, run the copy of its data into testing):

```sh
run() {
  sudo -u intranet /opt/katiuskas-intranet-deploy/deploy.sh \
    --config /opt/katiuskas-intranet-deploy/deploy.env "$@"
}
run --dry-run      # says what it would deploy to each environment
run --adopt        # "both are already running that"
sudo systemctl enable --now katiuskas-intranet-deploy.timer katiuskas-intranet-deploy.path
```

`--adopt` records the repository's current answer as deployed, so use it only if the environments
really run that; to adopt only one of them, give its name (`run --adopt production`). If an
environment runs something else, skip it for that one and let the first run deploy it. A host with
only one of the environments just leaves the other's `..._INSTALL_DIR` out of the settings.

**The webhook receiver.** It is [pushhook](https://github.com/cespedes/pushhook), a small program
(Go standard library only, about 900 lines with its tests) that listens on a local port behind the
site's reverse proxy, which terminates TLS. It verifies GitHub's HMAC signature with the secret
of each configured repository, so a request signed for another repository is refused, and runs
that repository's command, directly (no shell), only for a push; a `ping` is answered `200 pong`
without running anything, so GitHub's test shows green. It answers `202` at once and runs the
command in the background, one run at a time per repository, and logs one line per request, with
the reason when it rejects one. Its command here only creates the file `queue/pending`
(see above), so nothing in the request ends up in anything that runs.

It runs the command for a push to any branch, not just `main` and the tags: a push to a
feature branch makes one deployment check that finds nothing new (one `git ls-remote`), which is
harmless.

The program is built on the host, from a release that has been read (`v0.0.1`; `go install` needs
the Go that the host already has for `deploy.sh`; `go.mod` of pushhook asks for a recent one):

```sh
GOBIN="$HOME/pushhook-build" go install github.com/cespedes/pushhook@v0.0.1
sudo install -m 0755 "$HOME/pushhook-build/pushhook" /usr/local/bin/pushhook
```

Its configuration is
[`deploy/webhook/pushhook.conf.example`](../deploy/webhook/pushhook.conf.example): the address
to listen on, and one line with the GitHub repository's name, the webhook's secret and the command,
`/usr/bin/touch /var/lib/katiuskas-intranet-deploy/queue/pending`. The file holds the secret, so
only the receiver's user can read it. The unit
[`katiuskas-intranet-webhook.service`](../deploy/webhook/katiuskas-intranet-webhook.service) runs
it as that user, with nothing writable but the queue directory:

```sh
sudo mkdir /etc/katiuskas-intranet-webhook
sudo install -m 0640 -g katiuskas-intranet-webhook deploy/webhook/pushhook.conf.example \
  /etc/katiuskas-intranet-webhook/pushhook.conf     # then put a secret in it
sudo install -m 0644 deploy/webhook/katiuskas-intranet-webhook.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now katiuskas-intranet-webhook.service
```

(The user `katiuskas-intranet-webhook` is the one created in the setup above, for the queue
directory.) The reverse
proxy then sends that one path to it (for Caddy, which adds the `X-Forwarded-For` header that
pushhook logs the client's address from):

```
handle /hooks/katiuskas-intranet-deploy {
	reverse_proxy 127.0.0.1:9000
}
```

In GitHub, the repository's webhook is: payload URL `https://<the host's
domain>/hooks/katiuskas-intranet-deploy`, content type `application/json`, the same secret as in
`pushhook.conf`, SSL verification on, and only the push event. Use a long random secret, which
only the receiver's user and GitHub know.

*Other receivers.* pushhook is what the club's host runs, but nothing depends on it: what the
deployment needs from a receiver is only to

- accept `POST`s of GitHub's push events and check the signature: the `X-Hub-Signature-256`
  header is `sha256=` followed by the HMAC-SHA256 of the raw body with the webhook's secret,
  compared in constant time; if it does not match, refuse and do nothing;
- answer success to GitHub's `ping`, and optionally ignore what is not worth a run (other events,
  refs other than `main` and the tags);
- create the empty file `/var/lib/katiuskas-intranet-deploy/queue/pending`, as a user that can
  write in that directory, and answer right away without waiting for anything.

[adnanh/webhook](https://github.com/adnanh/webhook) can do that with one hook whose command is
`/usr/bin/touch` with that path as its only argument, and trigger rules that match the
`payload-hmac-sha256` of the header above, the event (`ping`, or `push` with a `ref` that matches
`^refs/(heads/main|tags/.+)$`), reading the secret from the environment with `-template`
and answering `403` for a request that does not match the rules (a rule cannot answer success to a
`ping` without running the command, which here only costs one deployment check). A receiver of
one's own is about forty lines in any language: verify the HMAC, create the file.

**Trying it.** In GitHub, the webhook's *Recent Deliveries* tab shows each delivery and what the
receiver answered, and *Redeliver* sends it again. What the answers mean, with pushhook (the
journal of the receiver, `journalctl -u katiuskas-intranet-webhook`, has one line for each one,
with the reason when it was refused):

- `200 pong`: the answer to GitHub's `ping`; the signature is right and nothing was run.
- `202 accepted`: a push, with a valid signature, to the configured repository. The file was
  created and the deployment starts (`journalctl -u katiuskas-intranet-deploy`;
  `systemctl status katiuskas-intranet-deploy.path` says whether the path unit is waiting).
- `200 ignored`: a validly signed event that is not a push.
- `401 invalid signature`: the secret in GitHub is not the one in `pushhook.conf`, the
  repository's name is not the configured one (`unconfigured github repo "..."` in the journal),
  or the content type is not `application/json`.

What to protect on the repository's side, because it is now what decides what runs:

- **`main`**: require pull requests (and, if there is more than one maintainer, a review), and
  forbid direct pushes and force pushes. Testing runs whatever gets there, with a copy of the real
  data.
- **Tags**: a ruleset so that only the maintainers can create tags, since a tag deploys to
  production. A tag whose commit is not in `main` is refused by the script anyway, but it does
  not stop one that is.
- **Actions** (if the repository is public): require approval for runs from outside collaborators.
  The ones the workflow uses are pinned to a commit, not a tag, which can be moved afterwards.
- **Dependencies**: Dependabot (`.github/dependabot.yml`) opens a pull request every week for what
  has a newer version: the Go modules (all in one), the actions (it updates the commit and the
  version in the comment), the base images of the API's Dockerfile and the images of the compose
  file. A major version of PostgreSQL is left out on purpose: it does not read the previous one's
  data, so moving to it is a dump and restore done by hand. Merging one of these pull requests is a
  deploy to testing like any other (see above), so wait for CI to pass and look at what changes
  first.

## Testing's data is a copy of production's

Every time production is deployed, production's data is copied into testing's database. The two
databases are separate only because the schema can differ: a commit on `main` with migrations
reaches testing before production. It is run by the `AFTER_DEPLOY` hook of the production update
(see "Pull-based deployment"), and the script that does it is
[`scripts/copy-prod-to-testing.sh`](../scripts/copy-prod-to-testing.sh): it empties testing's
database, restores into it a full dump of production's (schema, data, sequence values and goose's
version table) and applies with `goose` the migrations testing is ahead by. In addition, unless
`--no-files` is given, it replaces the files in testing's `ACTIVITY_RESOURCES_ROOT` and
`TRANSACTION_FILES_ROOT` with hard links to production's (see "Files the API writes: each
environment has its own"). Production is only read.
It is run as root or as a user who can stop and start testing's unit:

```sh
TESTING_SERVICE=<testing unit> \
  scripts/copy-prod-to-testing.sh [--yes] [--no-files] <production api.env> <testing api.env>
```

If that user does it with `sudo`, `SYSTEMCTL="sudo systemctl"` (it accepts arguments, for
example `"sudo -n systemctl"`) makes it stop and start the unit with it. That is the only
thing that uses `sudo`: the rest needs no privileges, but does need to be able to read both
`api.env` files (the deployment guide leaves them with `chmod 600`, owned by `intranet`) and,
for the files step, to be the owner of production's files (`intranet`) or root.

It reads `DATABASE_URL`, `ACTIVITY_RESOURCES_ROOT` and `TRANSACTION_FILES_ROOT` from the two
`api.env` files (without executing them) and needs `psql` and `pg_dump` (package
`postgresql-client`, with a `pg_dump` at least as new as production's server) and the `goose`
binary, which by default it looks for in `bin/goose` next to testing's `api.env`, with the
migrations in `db/migrations/` (as in the deployment guide; `GOOSE_BIN` and `MIGRATIONS_DIR` change
that); it asks you to type the name of testing's database before dropping it (`--yes` skips that,
for automation); it refuses to continue if the two URLs point at the same database, if testing's
role is not the owner of the `public` schema, or if production has a migration that testing's code
does not know; and, if something fails once the unit is stopped, it leaves it stopped. Consequences
of the copy:

- **Whatever is done in testing is lost at the next production deployment.** Joinings,
  edits, roles, Telegram links... Between two deployments testing diverges from production,
  and the next copy overwrites it.
- **Testing contains real personal data** of the members (DNI, email, phone, emergency
  contact...), and the same people have the same roles there as in production. Accessing
  testing is accessing real data.
- **Testing knows the real emails and phones.** If it has `SMTP_*` configured, it will send
  real emails to real members; without `SMTP_HOST`, sign-in links are only logged in the API
  logs.

## What each environment has of its own (testing and production)

Everything in this table has to differ between testing and production:

| Setting | Why it cannot be shared |
|---|---|
| Public URL (`PUBLIC_BASE_URL`) | Sign-in links include it, so each environment must point at its own domain. |
| Database (`DATABASE_URL`) | They are two separate databases, never two environments in one, because the schema can differ: testing receives each migration first, production only when a tag is pushed. The data, on the other hand, is not independent (see the previous section). |
| Install directory | The code, `web/`, the binaries and the environment file (`api.env`) live in each environment. |
| Listen address (`HTTP_ADDR`, and the local Caddy's port if one is used) | Two processes cannot listen on the same port, so each environment needs its own, and each domain's reverse proxy must forward to its own. |
| Telegram bot (`TELEGRAM_BOT_TOKEN`, `TELEGRAM_BOT_USERNAME`) | The bot polls Telegram with long polling, and a bot can only be polled by one process at a time (a second simultaneous poll gets a conflict error). In addition, the sign-in page links to the bot named by `TELEGRAM_BOT_USERNAME`, so each environment must point at its own. |

The Telegram account links (`users.telegram_chat_id`) live in the database, so they are copied
to testing along with the rest of production's data. But they are chat identifiers, and
testing's bot can only write to someone who has previously started a conversation with it.

## What testing and production share

`LIBRARY_ROOT` (club documents) and `USER_FILES_ROOT` (each person's documents) point at the same
directories in both environments. Both services must be able to read them.

- **`LIBRARY_ROOT` and `USER_FILES_ROOT`: no write conflicts.** The API only reads these trees
  (nothing is uploaded, renamed or deleted through it), so a file left in either directory is
  visible in both environments immediately.
- **Each person's files are addressed by login.** The directory `USER_FILES_ROOT/<login>/` is
  what the person with that login sees, and admin and board can read any login's directory.
  Since testing's data is a copy of production's, the logins are the same in both
  environments, and each person sees in testing the same documents as in production. The
  consequence is that **giving admin or board to someone in testing (to try something, for
  example) gives them read access to the real personal documents of all the members** (a
  scanned DNI, a signed waiver...), because the directory is the same as production's.
## Files the API writes: each environment has its own

`ACTIVITY_RESOURCES_ROOT` (the files attached to the activity memorias) and
`TRANSACTION_FILES_ROOT` (the tickets and invoices attached to transactions) are different: the API
creates and deletes files in them, and testing's database is a copy of production's, so its rows
have the same ids and therefore the same paths (`<activity id>/<resource id>` and
`<transaction id>/<file id>`). In a shared directory:

- **deleting a file in testing would delete the real file**, which production still references;
- **uploading in testing would occupy a path production may need later**: each database assigns ids
  from its own sequence, and testing's and production's may coincide. Whoever uploads second gets an
  error (500), because the API refuses to overwrite an existing file.

So each environment has a directory of its own for each of the two (set in its `api.env`, on the
same filesystem), and the copy of production's data into testing fills testing's with **hard links**
to production's files. Nothing is duplicated: each file is the same inode with an entry in each
tree, so testing takes no extra space (apart from the directories and whatever is uploaded in
testing). After the copy, uploading in testing creates a file only in testing, and deleting in
testing leaves production's file intact. It is safe because the API never modifies a file that is
already stored:

- when creating, `Storage.Save` opens the file with `O_EXCL`, so it never truncates an existing
  one, which is the only thing that would change the content in both trees at once;
- when deleting, `os.Remove` removes only that tree's entry, and the file stays in the other;
- no operation rewrites a file's bytes (to change one you delete it and create another).

Conditions:

- the two directories of each kind have to be on the same filesystem (`stat -c %d` gives the same
  number for both);
- the copy has to be made by the user who owns the files (`intranet`) or root: with
  `fs.protected_hardlinks=1`, which is the usual setting, Linux does not allow creating a hard link
  to somebody else's file;
- testing has to have its own directory, different from production's (and not inside it);
- the testing service has to run as the same user as production's: the directories are created
  with the same owner and mode as production's (`cp -a`), so another user could read the files but
  not upload or delete in them.

This is what `scripts/copy-prod-to-testing.sh` does (except with `--no-files`): with testing stopped
and before the database dump, it empties the contents of each of testing's two directories, without
deleting the directories themselves, and fills them with `cp -al` from production's. Before stopping
anything it checks, for each, that testing has a directory of its own (not the same as production's,
nor one inside the other, and the two of testing different from each other), that it is not at the
root of the filesystem, that it contains only numeric directories (if there is anything else, it
refuses to empty it), that both are on the same filesystem and that a hard link can be created. If
an environment still shares a directory with the other, the script refuses to run this step: give
testing its own directory first, or use `--no-files` to copy only the database. Both variables have
to be set in both `api.env` files (the API creates a missing directory when it starts, and the copy
and the backup stop with an error if one is missing).

Limitation: it depends on no future code modifying a stored file in place (thumbnails, image
resizing...), because it would do so in production too without warning. Each `Storage.Save` notes
this in its comment.

**Sequences and copy order.** Each id comes from a PostgreSQL sequence, and an upload in testing can
only collide with a file that already exists in its tree if testing's sequence reaches an id that
tree already has. That cannot be guaranteed by copying in one order or another, but it can be made
unlikely: the database is copied whole (`pg_dump` and restore carry the sequence values) and the
files are linked *before* the dump, so production cannot have created any between one and the other
and left in the tree an id ahead of the sequence. Even so, nothing can be corrupted: `Save` never
overwrites an existing file, so the worst that can happen is that one particular upload in testing
fails with an error (a new id that lands on a file that already exists, for example an orphan from
production), which in testing is acceptable.

## Setting up the second environment from the deployment guide

The guide describes a single installation with fixed names. For the second environment you
have to repeat it changing:

- the install directory (`/opt/katiuskas-intranet`);
- the name of the systemd unit (`katiuskas-api`) and the environment file;
- the PostgreSQL database and role (`intranet`);
- the listen ports (`64711`, and `64710` if a local Caddy is used);
- the domain, both in the reverse proxy and in `PUBLIC_BASE_URL`;
- the Telegram variables, with a second bot.

`LIBRARY_ROOT` and `USER_FILES_ROOT` are the only paths pointed at the same place in both; the two
directories the API writes to (`ACTIVITY_RESOURCES_ROOT`, `TRANSACTION_FILES_ROOT`) are different in
each: see the previous sections for what that implies.

## Backups

The backup that matters is production's: testing's database is filled with that same data on every
production deployment, and the two shared directories (`LIBRARY_ROOT`, `USER_FILES_ROOT`) only
need to be backed up once. Production's backup holds the database and **every file the API creates
or reads**: those two and the ones it writes (`ACTIVITY_RESOURCES_ROOT`, `TRANSACTION_FILES_ROOT`).
[`scripts/backup.sh`](../scripts/backup.sh) does it, every night, from a systemd timer
([`katiuskas-intranet-backup.timer`](../deploy/systemd/katiuskas-intranet-backup.timer)).

Each run makes a directory `BACKUP_DIR/<yyyymmdd-hhmmss>/` with:

- `database.dump`: `pg_dump --format=custom` of production's database, goose's version table
  included, which is restored with `pg_restore`;
- `files/library`, `files/user-files`, `files/activity-resources` and `files/transaction-files`: the
  four directories (the last two are the files the API itself writes), copied
  with `rsync` and hard-linking whatever has not changed since the previous backup. Every backup
  is therefore a complete tree, and yet a night without changes costs almost no disk (the files
  are mostly photos and PDFs that are never modified); deleting an old backup does not affect the
  others.

The files are copied before the dump, so that a row in the dump rarely points at a file the backup
lacks. A backup is built in a `.partial` directory, renamed only when complete and when
`pg_restore` can read the dump back, and a run that fails removes it and exits with an error (the
unit shows as failed). Afterwards the old ones are pruned: the newest 14 are kept (`KEEP_DAILY`),
and of the older ones the newest of each of the last 8 weeks (`KEEP_WEEKLY`). Everything is created
readable only by its user, because the files include people's documents.

**Setting it up** (once, on the club's host, as root; it needs `rsync` and the PostgreSQL client,
`pg_dump` and `pg_restore`, at least as new as the server):

```sh
sudo install -d -o intranet -g intranet -m 0700 /var/backups/katiuskas-intranet
sudo install -m 0755 scripts/backup.sh /opt/katiuskas-intranet-deploy/
sudo install -m 0644 deploy/systemd/backup.env.example /opt/katiuskas-intranet-deploy/backup.env
sudo install -m 0644 deploy/systemd/katiuskas-intranet-backup.service \
  deploy/systemd/katiuskas-intranet-backup.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl start katiuskas-intranet-backup.service     # the first one, to see it work
journalctl -u katiuskas-intranet-backup
sudo systemctl enable --now katiuskas-intranet-backup.timer
```

Edit `backup.env` for another place to keep them (better another disk than the data's) or another
retention.

**A copy off the host.** Backups on the same machine do not survive losing it, so it is worth
copying them elsewhere. `AFTER_BACKUP` in `backup.env` is an executable that is called, as the
same user, with the finished backup's directory as its argument; if it fails, the run fails (the
backup itself stays). For example, to mirror the whole backup directory, hard links and pruning
included, to another machine over `ssh` (with a key that user has, and that can only do this):

```sh
#!/bin/sh
# /opt/katiuskas-intranet-deploy/after-backup.sh
exec rsync -aH --delete --exclude .lock "$BACKUP_DIR/" backup@other-host:katiuskas-intranet/
```

**Restoring** (to a new machine, or after losing data). Try it first on a scratch database. With
the API stopped:

```sh
B=/var/backups/katiuskas-intranet/<the backup's directory>
pg_restore --clean --if-exists --no-owner --dbname "$DATABASE_URL" "$B/database.dump"
for d in library user-files activity-resources transaction-files; do
  rsync -a --delete "$B/files/$d/" "<the environment's directory for $d>/"
done
```

`--clean` drops each object before recreating it, so the target database may be empty or hold the
current (damaged) data. Start the API again afterwards; migrations newer than the dump's, if the
code is ahead of it, are applied by the next deployment (or with `goose up`).

## License thumbnails

The copies of the federative licenses in each person's files (`USER_FILES_ROOT/<login>/licencias/`)
are shown in `/perfil` and `/socio` with a thumbnail that the application does not generate by
itself (see "Dropping files into the library and the per-person directories" in
`docs/backend-internals.md`).
[`scripts/generate-license-thumbnails.sh`](../scripts/generate-license-thumbnails.sh) makes the
missing ones, and renews those older than their file, and a systemd timer
([`katiuskas-intranet-thumbnails.timer`](../deploy/systemd/katiuskas-intranet-thumbnails.timer))
runs it every 15 minutes, so a license copy dropped in has its thumbnail within minutes. The script
only does what is missing, so a run with nothing new is just a look at the directories. It also
tidies each `.thumbnails`: whatever is in there that is not the thumbnail (`<filename>.jpg`) of a
license copy still in `licencias/` that the page lists (the thumbnail of a deleted or renamed copy,
any other file, subdirectories) is removed; only a temporary file less than an hour old is left,
in case another run is writing it, and a `.thumbnails` that is a link is not touched.

The service runs as `intranet` (the owner of the files, so the thumbnails are theirs too) and gets
only what it needs, because the tools that read the people's uploads (poppler, ImageMagick,
Ghostscript) have a history of security problems: no network, no new privileges, no `/home`, and no
secrets — its only setting is `USER_FILES_ROOT`, in a file of its own, not production's `api.env`.
Setting it up, on the club's host, as root (the tools are packages: `apt install poppler-utils
imagemagick`):

```sh
sudo install -m 0755 scripts/generate-license-thumbnails.sh /opt/katiuskas-intranet-deploy/
sudo install -m 0644 deploy/systemd/thumbnails.env.example /opt/katiuskas-intranet-deploy/thumbnails.env
sudoedit /opt/katiuskas-intranet-deploy/thumbnails.env       # USER_FILES_ROOT, as in production's api.env
sudo install -m 0644 deploy/systemd/katiuskas-intranet-thumbnails.service \
  deploy/systemd/katiuskas-intranet-thumbnails.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl start katiuskas-intranet-thumbnails.service     # the first one, to see it work
journalctl -u katiuskas-intranet-thumbnails
sudo systemctl enable --now katiuskas-intranet-thumbnails.timer
```

It is one run for the whole `USER_FILES_ROOT`, which testing and production share, so it is
installed once, for production. A run that fails (a missing tool, a directory that is not there) is
told by Telegram like a failed backup (see "Alerts when something fails"). To make every thumbnail
again, run the script by hand with `--force` as `intranet`.

## Alerts when something fails

Nobody is going to read the journal every day, so the things that can go wrong without anyone
noticing are told by Telegram, to the log channel of production's bot (the one the API already
writes its errors and the sign-in notices to). Two mechanisms, for two kinds of failure:

- **What the API itself can see**, which the `/admin` page shows as "Hay problemas" (the
  `problems` of `GET /admin/status`, see "Admin status" in `docs/backend-internals.md`): the
  database not answering, a storage directory that does not exist, and a disk almost full
  (`DISK_ALERT_PERCENT` in `api.env`: how full, in percent, a filesystem holding `LIBRARY_ROOT`,
  `USER_FILES_ROOT`, `ACTIVITY_RESOURCES_ROOT` or `TRANSACTION_FILES_ROOT` may be, 90 by default, 0
  to turn it off). The API
  looks every 5 minutes and speaks when the set of problems changes ("⚠️ intranet.katiuskas.es: •
  Disco casi lleno (93 % usado, quedan 2.1 GB): /srv/…", then "✅ … ya no hay ningún problema"),
  and again once a day while one remains. Each API (testing's and production's) does it through
  its own bot, so there is nothing to install: it works as soon as Telegram is configured.
- **A deployment, a backup or a run of the thumbnails that fails**: those units name
  [`katiuskas-intranet-notify-failure@.service`](../deploy/systemd/katiuskas-intranet-notify-failure@.service)
  in `OnFailure=`, which runs [`scripts/notify-failure.sh`](../scripts/notify-failure.sh): it sends
  "⚠️ Ha fallado katiuskas-intranet-backup.service en <host>" with the last lines of that unit's
  journal. A unit that keeps failing (a broken commit is tried again every 15 minutes) is told once,
  and again after 6 hours (`NOTIFY_EVERY`) if it has not recovered; a run that succeeds clears that,
  so the next failure is told at once. A deployment is a failure whenever it exits with an error,
  including the update of one environment failing while the other works.

Setting up the second one, on the club's host, as root (the units of the deployment and of the
backup have to be installed again, since they now name it):

```sh
sudo install -m 0755 scripts/notify-failure.sh /opt/katiuskas-intranet-deploy/
sudo install -m 0644 deploy/systemd/katiuskas-intranet-notify-failure@.service \
  deploy/systemd/katiuskas-intranet-deploy.service deploy/systemd/katiuskas-intranet-backup.service \
  /etc/systemd/system/
sudo mkdir -p /var/lib/katiuskas-intranet-deploy/notified
sudo chown intranet:intranet /var/lib/katiuskas-intranet-deploy/notified
sudo systemctl daemon-reload
```

It needs `curl` and Telegram configured in production's `api.env` (`TELEGRAM_BOT_TOKEN` and
`TELEGRAM_LOG_CHAT_ID`). For the message to include the journal's last lines, the `intranet`
user has to be able to read it (`sudo usermod -aG systemd-journal intranet`); without that it
only names the unit. To try it, make the unit fail on purpose and look at the chat:

```sh
sudo systemd-run --unit=try-failure --property=User=intranet \
  --property=OnFailure=katiuskas-intranet-notify-failure@try-failure.service /bin/false
```

What this does not cover, because it needs something that is not running to say it: the API being
down (or the whole host), and the webhook receiver not working (the 15-minute timer is what
makes a deployment happen anyway). A check from outside the host, such as an uptime monitor that
asks for `https://intranet.katiuskas.es/api/v1/health`, is the way to cover those.

## The shared tablet: an address for short sessions

The club has a tablet that anyone uses, and a session left open on it should not stay valid for 30
days. The login page is the same everywhere and nothing marks the tablet as special; what changes is
the address it is given: a session opened through an address whose first component, up to the first
`.`, is `local` expires 3 hours (`config.LocalTokenTTL`) after its last use instead of 30 days. So
the tablet uses `local.` followed by the usual address — `local.intranet.katiuskas.es` for
production, `local.testing.katiuskas.es` for testing. The rule is in `docs/architecture.md`, "Short
sessions". What has to be set up, once per environment:

1. **A DNS record** for `local.intranet.katiuskas.es`, the same as for `intranet.katiuskas.es`.
2. **The same site in the reverse proxy**, with both addresses. In the club's Caddy (see
   `docs/deployment.md`, "Caddy") it is a second address on the site block, which then
   also gets its own certificate:

   ```
   intranet.katiuskas.es, local.intranet.katiuskas.es {
   	…the rest of the block as it is…
   }
   ```

   Caddy passes the address the browser used to the API (`Host`, and `X-Forwarded-Host`, which the
   API reads first); a reverse proxy that rewrites it has to keep `X-Forwarded-Host` as the
   original.
3. **On the tablet**, `https://local.intranet.katiuskas.es/` as the browser's bookmark or start
   page. Nothing else: every person signs in as always, and the login page shows a "Sesión local"
   notice there, saying that the session will close by itself in a few hours.

`PUBLIC_BASE_URL` stays the main address. The link in the sign-in email follows the address the
request was made through only when it is exactly `local.` plus the public address (so it is opened
on the tablet and finishes there, with the short session); for any other it points to the public
one.

Things to know. The tablet's address is another site for the browser, with its own storage: being
signed in there and on the main address are separate. Anybody can use that address, on any device,
and all it does is give them a shorter session. The sessions are listed in `/admin` and in each
person's "Mis sesiones", with their expiry; those opened on the tablet before this was set up last
their 30 days of lifetime (renewed with use) until closed there.
