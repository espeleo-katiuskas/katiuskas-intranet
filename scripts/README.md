# Scripts

Command-line tools of the project. Each script explains its usage and options in the comment at
the top of the file itself; `generate-license-thumbnails.sh` also with `--help`, and
`get-token.sh` shows it when it is run wrongly.

| Script | What it does | Where it is used |
| --- | --- | --- |
| `get-token.sh` | Creates a session token for a person directly in the database (by login, or looking the person up by email or name with `--user`) and prints it, to try the API with `curl` or to enter the web without going through the sign-in link. | Local development only, with `docker compose` up. **Never in production.** |
| `copy-prod-to-testing.sh` | Replaces the testing database with a copy of the production one, migrates it up to the schema the testing code expects and links (hard links) the production activity memoria files. It only reads production; everything that was in testing is lost. | The club's host, on every production deployment. See [`environments.md`](../docs/environments.md). |
| `generate-license-thumbnails.sh` | Creates the missing (or outdated) thumbnails of the federative license copies in `user-files/<login>/licencias/`: the first page of a PDF, or a reduced version of an image. | Whenever license copies are added, both locally and in production. See [`deployment-no-docker.md`](../docs/deployment-no-docker.md). |

## Examples

```sh
scripts/get-token.sh admin.club
scripts/get-token.sh --user garcia

TESTING_SERVICE=<testing systemd unit> \
  scripts/copy-prod-to-testing.sh [--yes] [--no-files] <production api.env> <testing api.env>

scripts/generate-license-thumbnails.sh --dry-run
```
