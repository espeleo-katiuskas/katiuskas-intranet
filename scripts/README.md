# Scripts

Command-line tools of the project. Each script explains its usage and options in the comment at
the top of the file itself; `generate-license-thumbnails.sh` also with `--help`, and
`get-token.sh` shows it when it is run wrongly.

| Script | What it does | Where it is used |
| --- | --- | --- |
| `get-token.sh` | Creates a session token for a person directly in the database (by login, or looking the person up by email or name with `--user`) and prints it, to try the API with `curl` or to enter the web without going through the sign-in link. | Local development only, with `docker compose` up. **Never in production.** |
| `copy-prod-to-testing.sh` | Replaces the testing database with a copy of the production one, migrates it up to the schema the testing code expects and links (hard links) the production activity memoria files. It only reads production; everything that was in testing is lost. | The club's host, on every production deployment. See [`environments.md`](../docs/environments.md). |
| `deploy.sh` | Updates testing and production from the repository: finds what each should run (the tip of `main`, or the highest version tag), builds it, applies the migrations, installs it and restarts the service, with a health check and rollback of the binary. Nothing in a request decides what is deployed. | The club's host, run by a systemd unit (the timer, and the webhook through a path unit). See [`environments.md`](../docs/environments.md) ("Pull-based deployment"). |
| `backup.sh` | Backs up an environment's data into a directory per run: the database (`pg_dump` custom format) and the three directories of files (`rsync`, hard-linking what has not changed), checks the dump can be read back, prunes old backups (the newest 14 and one per week for 8 weeks) and can call a hook to copy the result off the host. | The club's host, every night from a systemd timer, for production. See [`environments.md`](../docs/environments.md) ("Backups"). |
| `notify-failure.sh` | Tells by Telegram (to the log channel of the environment's bot) that a systemd unit failed, with the last lines of its journal, once per unit every six hours while it keeps failing. | The club's host, through `katiuskas-intranet-notify-failure@.service`, which the deployment and backup units start with `OnFailure=`. See [`environments.md`](../docs/environments.md) ("Alerts when something fails"). |
| `generate-license-thumbnails.sh` | Creates the missing (or outdated) thumbnails of the federative license copies in `user-files/<login>/licencias/`: the first page of a PDF, or a reduced version of an image. | Locally by hand whenever license copies are added; on the club's host, every 15 minutes from a systemd timer (`katiuskas-intranet-thumbnails.timer`). See [`environments.md`](../docs/environments.md) ("License thumbnails"). |

## Examples

```sh
scripts/get-token.sh admin.club
scripts/get-token.sh --user garcia

TESTING_SERVICE=<testing systemd unit> \
  scripts/copy-prod-to-testing.sh [--yes] [--no-files] <production api.env> <testing api.env>

scripts/generate-license-thumbnails.sh --dry-run

scripts/deploy.sh --config /opt/katiuskas-intranet-deploy/deploy.env --dry-run
```
