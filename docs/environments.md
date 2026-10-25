# Environments: local, testing and production

The project runs in three environments:

| | Local | Testing | Production |
|---|---|---|---|
| Where | development machine | the club's host | the club's host (the same machine as testing) |
| How it runs | Docker (`docker-compose.yml`) | systemd unit, without Docker | systemd unit, without Docker |
| URL | `http://localhost:64710` | `https://testing.katiuskas.es` | `https://intranet.katiuskas.es` |
| What for | trying changes before committing or pushing | trying what is already in `main` | the real members |
| Updated by | `docker compose up --build`, by hand | every push to `main` | every push of a semver tag |
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
- **A push to `main` updates testing automatically.** So a push to `main` is a deployment, not
  just a way of publishing commits: whatever reaches `main` is running on
  `testing.katiuskas.es` once the update finishes.
- **A push of a tag whose name is a semantic version (`MAJOR.MINOR.PATCH`) updates
  production** to the commit that tag points at. Nothing else changes production: pushes to
  `main` never reach it.
- Since testing follows `main` and production only moves with tags, the natural flow for a
  release is to tag a commit that has already been running in testing for a while:

  ```sh
  git tag <version> <commit>
  git push origin <version>
  ```

The automation that performs these updates is not part of this repository. The only workflow
here, `.github/workflows/ci.yml`, checks, builds and tests the code (and also validates the
OpenAPI and builds the Docker image) on pushes to `main` and on pull requests; it does not run
on tag pushes and deploys nothing. The manual procedure in the deployment guide remains the
reference for what an update involves.

## Testing's data is a copy of production's

Every time production is deployed, production's data is copied into testing's database. The
two databases are separate only because the schema can differ: a commit on `main` with
migrations reaches testing before production. What triggers that copy on each deployment is
not part of this repository, but the script that does it is,
[`scripts/copy-prod-to-testing.sh`](../scripts/copy-prod-to-testing.sh): it empties testing's
database, restores into it a full dump of production's (schema, data, sequence values and
goose's version table) and applies with `goose` the migrations testing is ahead by. In
addition, unless `--no-files` is given, it replaces the files in testing's
`ACTIVITY_RESOURCES_ROOT` with hard links to production's (see the section "Options for
`ACTIVITY_RESOURCES_ROOT`"). Production is only read. It is run as root or as a user who can
stop and start testing's unit:

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
