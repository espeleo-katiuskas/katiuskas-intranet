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
| `LIBRARY_ROOT`, `USER_FILES_ROOT`, `ACTIVITY_RESOURCES_ROOT` | its own | shared with production | shared with testing |

The local environment shares nothing with the other two; it is brought up as explained in the
"Quick start" section of the [`README.md`](../README.md). Testing and production are installed
following [`deployment-no-docker.md`](deployment-no-docker.md); the rest of this page covers
what is specific to having those two on one machine.

## How updates reach each environment

- **Local:** by hand, with `docker compose up --build` (and `docker compose build api && docker
  compose up -d api` to rebuild only the API).
- **Testing follows `main`.** The host looks at the repository every minute, and when the tip of
  `main` is not what testing runs, it builds that commit and installs it. So reaching `main`
  is a deployment, not just a way of publishing commits: whatever gets there is running on
  `testing.katiuskas.es` a minute or two later.
- **Production follows the highest tag whose name is a semantic version (`MAJOR.MINOR.PATCH`,
  with an optional leading `v`)**, and runs the commit that tag points at. Nothing else changes
  production: `main` never reaches it. To roll back, tag a new, higher version.
- Since testing follows `main` and production only moves with tags, the natural flow for a
  release is to tag a commit that has already been running in testing for a while:

  ```sh
  git tag <version> <commit>
  git push origin <version>
  ```

Nothing is pushed to the host: it pulls. See "Pull-based deployment" below for how it works and
how to set it up. The only workflow in the repository, `.github/workflows/ci.yml`, checks, builds
and tests the code (and also validates the OpenAPI and builds the Docker image) on pushes to
`main` and on pull requests; it does not run on tag pushes and deploys nothing. The manual
procedure in the deployment guide remains the reference for what an update involves.

## Pull-based deployment

[`scripts/deploy.sh`](../scripts/deploy.sh) updates testing and production from the repository,
and a single systemd timer ([`deploy/systemd/`](../deploy/systemd/)) runs it every minute. There
is no webhook to authenticate and no CI runner with access to the host, which matters because the
repository is public: the host only makes outgoing, read-only requests to the repository's fixed
URL (`git ls-remote`, `git fetch`), and nothing that reaches it from the network can make it deploy
anything.

Each run asks the repository, once, for the tip of `main` and the tags, works out what each
environment should run (the tip of `main` for testing, the highest version tag for production) and
compares it with what it last deployed (`STATE_DIR/<environment>/deployed`). If both are the same,
that was all: one `git ls-remote`. Otherwise it fetches once and updates, one after the other,
the environments that need it, testing first. For each one it:

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
minutes (`RETRY_AFTER`), so a broken commit does not rebuild every minute; a new commit or
`--force` tries immediately. The script says what it does in the unit's journal
(`journalctl -u katiuskas-deploy`).

**Setting it up** (once, on the club's host, as root):

```sh
# The host builds, so it needs git and Go (the toolchain is not downloaded: it has to be at
# least the version go.mod asks for). The build and the Go caches take a few hundred MB;
# `STATE_DIR/cache` can be deleted whenever disk is needed (`chmod -R u+w` it first: Go makes
# its module cache read-only), and the next run rebuilds it.
sudo mkdir -p /opt/katiuskas-deploy /var/lib/katiuskas-deploy
sudo chown katiuskas:katiuskas /var/lib/katiuskas-deploy

# Root-owned copies, so that what the timer runs is not something the unprivileged user
# (or the code it builds) can change. Install them again when these files change.
sudo install -m 0755 scripts/deploy.sh /opt/katiuskas-deploy/deploy.sh
sudo install -m 0755 scripts/copy-prod-to-testing.sh /opt/katiuskas-deploy/copy-prod-to-testing.sh
sudo install -m 0755 deploy/systemd/after-production.sh.example \
  /opt/katiuskas-deploy/after-production.sh

# The settings: copy the example and edit REPO_URL, the directories and the units.
sudo install -m 0640 -g katiuskas deploy/systemd/deploy.env.example \
  /etc/katiuskas-deploy.env

sudo install -m 0644 deploy/systemd/katiuskas-deploy.service \
  deploy/systemd/katiuskas-deploy.timer /etc/systemd/system/
sudo systemctl daemon-reload
```

The only privileged things are restarting the APIs' units and, for the copy of production's data
into testing, stopping and starting testing's. A sudoers rule (`sudo visudo -f
/etc/sudoers.d/katiuskas-deploy`) allows exactly that and nothing else:

```
katiuskas ALL=(root) NOPASSWD: /usr/bin/systemctl restart katiuskas-api, \
  /usr/bin/systemctl restart katiuskas-api-testing, \
  /usr/bin/systemctl stop katiuskas-api-testing, /usr/bin/systemctl start katiuskas-api-testing
```

Before starting the timer, say what each environment is running now, so that the first run does
not deploy it again (and, for production, run the copy of its data into testing):

```sh
run() {
  sudo -u katiuskas bash -c \
    "set -a; . /etc/katiuskas-deploy.env; set +a; /opt/katiuskas-deploy/deploy.sh $*"
}
run --dry-run      # says what it would deploy to each environment
run --adopt        # "both are already running that"
sudo systemctl enable --now katiuskas-deploy.timer
```

`--adopt` records the repository's current answer as deployed, so use it only if the environments
really run that; to adopt only one of them, give its name (`run --adopt production`). If an
environment runs something else, skip it for that one and let the first run deploy it. A host with
only one of the environments just leaves the other's `..._INSTALL_DIR` out of the settings.

What to protect on the repository's side, because it is now what decides what runs:

- **`main`**: require pull requests (and, if there is more than one maintainer, a review), and
  forbid direct pushes and force pushes. Testing runs whatever gets there, with a copy of the real
  data.
- **Tags**: a ruleset so that only the maintainers can create tags, since a tag deploys to
  production. A tag whose commit is not in `main` is refused by the script anyway, but it does
  not stop one that is.
- **Actions** (if the repository is public): require approval for runs from outside collaborators.

## Testing's data is a copy of production's

Every time production is deployed, production's data is copied into testing's database. The two
databases are separate only because the schema can differ: a commit on `main` with migrations
reaches testing before production. It is run by the `AFTER_DEPLOY` hook of the production update
(see "Pull-based deployment"), and the script that does it is
[`scripts/copy-prod-to-testing.sh`](../scripts/copy-prod-to-testing.sh): it empties testing's
database, restores into it a full dump of production's (schema, data, sequence values and goose's
version table) and applies with `goose` the migrations testing is ahead by. In addition, unless
`--no-files` is given, it replaces the files in testing's `ACTIVITY_RESOURCES_ROOT` with hard links
to production's (see the section "Options for `ACTIVITY_RESOURCES_ROOT`"). Production is only read.
It is run as root or as a user who can stop and start testing's unit:

```sh
TESTING_SERVICE=<testing unit> \
  scripts/copy-prod-to-testing.sh [--yes] [--no-files] <production api.env> <testing api.env>
```

If that user does it with `sudo`, `SYSTEMCTL="sudo systemctl"` (it accepts arguments, for
example `"sudo -n systemctl"`) makes it stop and start the unit with it. That is the only
thing that uses `sudo`: the rest needs no privileges, but does need to be able to read both
`api.env` files (the deployment guide leaves them with `chmod 600`, owned by `katiuskas`) and,
for the files step, to be the owner of production's files (`katiuskas`) or root.

It reads `DATABASE_URL` and `ACTIVITY_RESOURCES_ROOT` from the two `api.env` files (without
executing them) and needs `psql` and `pg_dump` (package `postgresql-client`, with a `pg_dump`
at least as new as production's server) and the `goose` binary, which by default it looks for
in `bin/goose` next to testing's `api.env`, with the migrations in `db/migrations/` (as in the
deployment guide; `GOOSE_BIN` and `MIGRATIONS_DIR` change that); it asks you to type the name
of testing's database before dropping it (`--yes` skips that, for automation); it refuses to
continue if the two URLs point at the same database, if testing's role is not the owner of the
`public` schema, or if production has a migration that testing's code does not know; and, if
something fails once the unit is stopped, it leaves it stopped. Consequences of the copy:

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

`LIBRARY_ROOT` (club documents), `USER_FILES_ROOT` (each person's documents) and
`ACTIVITY_RESOURCES_ROOT` (files attached to the activity memorias) point at the same
directories in both environments. Both services must be able to read them and to write where
needed.

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
- **`ACTIVITY_RESOURCES_ROOT` is written to from the API, and that makes it delicate.** Each
  file is stored as `<activity id>/<resource id>`; the API creates it when a file-type
  resource is uploaded (and refuses, with an error, to overwrite one that already exists) and
  removes it from disk when that resource is deleted. Since testing's database is a copy of
  production's, its resources have the same ids and, therefore, the same paths. As long as the
  directory is the same:
  - **deleting a file-type resource in testing deletes the real file**, which production still
    references;
  - **uploading a file in testing occupies a path that production may need later**: each
    database assigns resource ids from its own sequence, and testing's and production's may
    coincide. Whoever uploads second gets an error (500), whether testing or production;
    without that refusal to overwrite, the second would have silently overwritten the first's
    file.

  Until this directory stops being shared, uploading or deleting memoria files in testing is
  not safe; the text of a memoria, which lives only in the database, can be touched without
  risk. The options for ceasing to share it are in the next section.

## Options for `ACTIVITY_RESOURCES_ROOT`

1. **Shared (the current situation).** There is no copy and no extra space, but both risks
   above apply: it is only safe if no memoria files are uploaded or deleted in testing.
2. **A directory of testing's own, with a normal copy of production's.** Each production
   deployment copies the directory again, always from production to testing (for example with
   `rsync -a --delete`). It isolates the two environments completely; the cost is the space
   and copy time, because they are photos and PDFs.
3. **A directory of testing's own, copied with hard links.** The files are not duplicated:
   each one is the same inode with an entry in each tree, so testing takes no extra space
   (apart from the directories and whatever is uploaded in testing). After the copy, uploading
   a file in testing creates a new one only in testing, and deleting a resource in testing
   leaves production's file intact. It is safe because the API never modifies a file that is
   already stored:
   - when creating, `Storage.Save` opens the file with `O_EXCL`, so it never truncates an
     existing one, which is the only thing that would change the content in both trees at
     once;
   - when deleting, `os.Remove` removes only that tree's entry, and the file stays in the
     other;
   - no operation rewrites a resource's bytes (to change them you have to delete the resource
     and create another).

   Conditions:
   - the two directories have to be on the same filesystem (`stat -c %d` gives the same number
     for both);
   - the copy has to be made by the user who owns the files (`katiuskas`) or root: with
     `fs.protected_hardlinks=1`, which is the usual setting, Linux does not allow creating a
     hard link to somebody else's file;
   - testing has to have its own `ACTIVITY_RESOURCES_ROOT`, different from production's;
   - the testing service has to run as the same user as production's: the activity
     directories are created with the same owner and mode as production's (`cp -a`), so
     another user could read the files but not upload or delete in them.

   This is what `scripts/copy-prod-to-testing.sh` does (except with `--no-files`): with
   testing stopped and before the database dump, it empties the contents of testing's
   directory, without deleting the directory itself, and fills it with `cp -al` from
   production's. Before stopping anything it checks that testing has a directory of its own
   (not the same as production's, nor one inside the other), that it is not at the root of the
   filesystem, that it contains only numeric activity directories (if there is anything else,
   it refuses to empty it), that both are on the same filesystem and that a hard link can be
   created. While the two environments still share a directory, the script refuses to run this
   step: change testing's `ACTIVITY_RESOURCES_ROOT` first, or use `--no-files` to copy only the
   database.

   Limitation: it depends on no future code modifying a stored file in place (thumbnails,
   image resizing...), because it would do so in production too without warning.
   `Storage.Save` notes this in its comment.

**Sequences and copy order (options 2 and 3).** Each resource id comes from a PostgreSQL
sequence (`bigserial`), and an upload in testing can only collide with a file that already
exists in its tree if testing's sequence reaches an id that tree already has. That cannot be
guaranteed by copying in one order or another: it depends on how the database is copied and
which ids exist on each side. What can be done is reduce the probability:

- if the database is copied whole (`pg_dump` and restore, which carry the sequence values),
  copy the files *before* the dump, so that production cannot have created any between one and
  the other and left in the tree an id ahead of the sequence;
- if only the rows are copied, testing's sequence has to be adjusted afterwards with `setval`,
  at least above the highest id in the file tree.

Even so, neither the order nor the state of the sequences can corrupt anything: `Save` never
overwrites an existing file, so the worst that can happen is that one particular upload in
testing fails with an error (a new id that lands on a file that already exists, for example an
orphan from production), which in testing is acceptable.

## Setting up the second environment from the deployment guide

The guide describes a single installation with fixed names. For the second environment you
have to repeat it changing:

- the install directory (`/opt/katiuskas-intranet`);
- the name of the systemd unit (`katiuskas-api`) and the environment file;
- the PostgreSQL database and role (`intranet`);
- the listen ports (`64711`, and `64710` if a local Caddy is used);
- the domain, both in the reverse proxy and in `PUBLIC_BASE_URL`;
- the Telegram variables, with a second bot.

`LIBRARY_ROOT`, `USER_FILES_ROOT` and `ACTIVITY_RESOURCES_ROOT` are the only paths pointed at
the same place in both; see the previous section for what that implies.

## Backups

The backup that matters is production's database (see the "Backups" section of the deployment
guide): testing's is filled with that same data on every production deployment. The three
shared directories only need to be backed up once.
