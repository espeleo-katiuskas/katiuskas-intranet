#!/usr/bin/env bash
#
# Replaces the testing database with a copy of production's data, then
# migrates it up to the schema testing's own code expects, and replaces
# testing's activity resource files and transaction files with hard links to production's.
#
# Why a full drop-and-restore rather than copying rows: testing runs `main`,
# which can carry migrations production hasn't received yet, so the two
# schemas may differ. Restoring a complete dump of production (schema, data,
# sequence values and goose's own version table) gives testing exactly
# production's state, and `goose up` then applies whatever migrations testing
# is ahead by.
#
# Production is only read (pg_dump takes a consistent snapshot without
# blocking writers). Everything in the testing database is lost.
#
# ACTIVITY_RESOURCES_ROOT and TRANSACTION_FILES_ROOT hold the files the database's
# activity_resources and transaction_files rows point at, so they have to follow the database.
# Their files are hard-linked, not copied: the API never modifies a stored file (Storage.Save refuses to
# overwrite one and deleting is a plain unlink), so testing shares production's
# files without using more disk, and a write or delete in one tree can't change
# the other's. They are linked before the database is dumped, which keeps
# testing's id sequence from starting behind a file already in its tree (if it
# ever lands on one, Save refuses to overwrite it and that one upload fails).
# This needs testing to have its own ACTIVITY_RESOURCES_ROOT and TRANSACTION_FILES_ROOT, different
# from production's, on the same filesystem, and a user allowed to link production's
# files (root, or their owner: see fs.protected_hardlinks). Pass --no-files to
# leave the files alone and only copy the database.
#
# Usage, as root or as a user allowed to stop/start the testing unit:
#   TESTING_SERVICE=<testing systemd unit> \
#     scripts/copy-prod-to-testing.sh [--yes] [--no-files] <prod-env-file> <testing-env-file>
#
# The two env files are the systemd EnvironmentFile (api.env) of each
# environment. Only DATABASE_URL, ACTIVITY_RESOURCES_ROOT, TRANSACTION_FILES_ROOT (and PUBLIC_BASE_URL,
# to label the prompt) are read from them; they are parsed, never sourced.
# --yes skips the confirmation prompt, for running from automation.
#
# Environment:
#   TESTING_SERVICE  systemd unit of the testing API, stopped during the copy
#                    and started again at the end (required)
#   GOOSE_BIN        default: <directory of the testing env file>/bin/goose
#   MIGRATIONS_DIR   default: <directory of the testing env file>/db/migrations
#   SYSTEMCTL        command used to stop and start the unit; it may carry
#                    arguments, e.g. SYSTEMCTL="sudo systemctl" when running as
#                    a user allowed to run that with sudo (default: systemctl)
#
# Needs psql and pg_dump on PATH (the postgresql-client package), with a
# pg_dump at least as new as production's server.
set -euo pipefail

die() { echo "error: $*" >&2; exit 1; }
log() { echo "==> $*"; }

assume_yes=0
copy_files=1
while [[ ${1:-} == --* ]]; do
  case $1 in
    --yes) assume_yes=1 ;;
    --no-files) copy_files=0 ;;
    *) die "unknown option $1" ;;
  esac
  shift
done
[[ $# -eq 2 ]] || die "usage: TESTING_SERVICE=<unit> $0 [--yes] [--no-files] <prod-env-file> <testing-env-file>"
prod_env=$1
testing_env=$2
: "${TESTING_SERVICE:?set TESTING_SERVICE to the systemd unit of the testing API}"
# Split once on whitespace (no globbing) so SYSTEMCTL can be "sudo systemctl".
read -r -a systemctl_cmd <<<"${SYSTEMCTL:-systemctl}"
testing_dir=$(dirname "$testing_env")
GOOSE_BIN=${GOOSE_BIN:-$testing_dir/bin/goose}
MIGRATIONS_DIR=${MIGRATIONS_DIR:-$testing_dir/db/migrations}

for f in "$prod_env" "$testing_env"; do
  [[ -r $f ]] || die "cannot read $f"
done
command -v "${systemctl_cmd[0]}" >/dev/null || die "${systemctl_cmd[0]} not found (set SYSTEMCTL)"
command -v psql >/dev/null || die "psql not found (install postgresql-client)"
command -v pg_dump >/dev/null || die "pg_dump not found (install postgresql-client)"
command -v "$GOOSE_BIN" >/dev/null || die "goose not found at $GOOSE_BIN (set GOOSE_BIN)"
[[ -d $MIGRATIONS_DIR ]] || die "no migrations directory at $MIGRATIONS_DIR (set MIGRATIONS_DIR)"

env_value() {
  local value
  value=$(grep -E "^$2=" "$1" | tail -n1) || true
  value=${value#"$2="}
  value=${value#[\"\']}
  value=${value%[\"\']}
  printf '%s' "$value"
}

urldecode() {
  local rest=$1 out=
  while [[ $rest =~ ^([^%]*)%([0-9A-Fa-f]{2})(.*)$ ]]; do
    out+=${BASH_REMATCH[1]}$(printf "\\x${BASH_REMATCH[2]}")
    rest=${BASH_REMATCH[3]}
  done
  printf '%s' "$out$rest"
}

# conn[<side>.<field>] holds the parsed pieces of each side's DATABASE_URL, so
# connections can be made through PG* environment variables: unlike a URL
# argument, a password there never shows up in `ps`.
declare -A conn
parse_url() {
  local side=$1 url=$2 re='^postgres(ql)?://([^:@/]*)(:([^@]*))?@([^:/?]*)(:([0-9]+))?/([^?]*)(\?(.*))?$'
  [[ $url =~ $re ]] || die "cannot parse DATABASE_URL for $side"
  conn[$side.user]=$(urldecode "${BASH_REMATCH[2]}")
  conn[$side.password]=$(urldecode "${BASH_REMATCH[4]}")
  conn[$side.host]=${BASH_REMATCH[5]}
  conn[$side.port]=${BASH_REMATCH[7]:-5432}
  conn[$side.db]=$(urldecode "${BASH_REMATCH[8]}")
  conn[$side.sslmode]=prefer
  if [[ ${BASH_REMATCH[10]} =~ sslmode=([^\&]*) ]]; then
    conn[$side.sslmode]=${BASH_REMATCH[1]}
  fi
}

# pg <src|tgt> <command...>: runs a libpq client against that database.
pg() {
  local side=$1
  shift
  env PGHOST="${conn[$side.host]}" PGPORT="${conn[$side.port]}" PGUSER="${conn[$side.user]}" \
    PGPASSWORD="${conn[$side.password]}" PGDATABASE="${conn[$side.db]}" \
    PGSSLMODE="${conn[$side.sslmode]}" "$@"
}

prod_url=$(env_value "$prod_env" DATABASE_URL)
testing_url=$(env_value "$testing_env" DATABASE_URL)
[[ -n $prod_url ]] || die "no DATABASE_URL in $prod_env"
[[ -n $testing_url ]] || die "no DATABASE_URL in $testing_env"
parse_url src "$prod_url"
parse_url tgt "$testing_url"

normalize_host() {
  case $1 in
    '' | localhost | 127.0.0.1 | ::1) echo local ;;
    *) echo "$1" ;;
  esac
}
src_id="$(normalize_host "${conn[src.host]}"):${conn[src.port]}/${conn[src.db]}"
tgt_id="$(normalize_host "${conn[tgt.host]}"):${conn[tgt.port]}/${conn[tgt.db]}"
[[ $src_id != "$tgt_id" ]] || die "production and testing point at the same database ($src_id)"

# Fail before touching anything if either database is unreachable or the
# source doesn't look like an intranet database.
pg tgt psql -qAt -c 'SELECT 1' </dev/null >/dev/null || die "cannot connect to the testing database"
# The testing role empties and refills its own database, so it must own schema
# public — true for the database owner on PostgreSQL 15+, where that schema
# belongs to pg_database_owner.
owns_public=$(pg tgt psql -qAt -c "SELECT pg_has_role(current_user, nspowner, 'MEMBER') FROM pg_namespace WHERE nspname = 'public'" </dev/null)
[[ $owns_public == t ]] \
  || die "the testing role (${conn[tgt.user]}) doesn't own schema public in ${conn[tgt.db]}; it must be the database owner on PostgreSQL 15 or later"
prod_version=$(pg src psql -qAt -c 'SELECT COALESCE(max(version_id), 0) FROM goose_db_version WHERE is_applied' </dev/null) \
  || die "cannot read goose_db_version from the production database — is it the right one?"
latest_migration=$(ls "$MIGRATIONS_DIR" | sed -n 's/^0*\([0-9][0-9]*\)_.*\.sql$/\1/p' | sort -n | tail -n1)
[[ -n $latest_migration ]] || die "no migration files in $MIGRATIONS_DIR"
# Production ahead of testing's own code would leave testing running against a
# schema it doesn't know; that means the wrong checkout is deployed there.
((prod_version <= latest_migration)) \
  || die "production is at migration $prod_version but testing's migrations only go up to $latest_migration"

# The two directories the API writes to, which follow the database: for each, the variable that
# names it in api.env and config.go's default for when it is unset.
file_vars=(ACTIVITY_RESOURCES_ROOT TRANSACTION_FILES_ROOT)
declare -A file_defaults=(
  [ACTIVITY_RESOURCES_ROOT]=/data/activity-resources
  [TRANSACTION_FILES_ROOT]=/data/transaction-files
)
declare -A prod_files testing_files

if ((copy_files)); then
  for var in "${file_vars[@]}"; do
    prod_dir=$(env_value "$prod_env" "$var")
    testing_dir=$(env_value "$testing_env" "$var")
    prod_dir=$(realpath -m -- "${prod_dir:-${file_defaults[$var]}}")
    testing_dir=$(realpath -m -- "${testing_dir:-${file_defaults[$var]}}")
    prod_files[$var]=$prod_dir
    testing_files[$var]=$testing_dir

    [[ -d $prod_dir ]] || die "production's $var ($prod_dir) is not a directory"
    [[ $prod_dir != "$testing_dir" ]] \
      || die "production and testing share the same $var ($prod_dir); give testing its own directory first (see docs/environments.md), or pass --no-files"
    case "$prod_dir/" in "$testing_dir"/*) die "production's $var is inside testing's ($testing_dir)" ;; esac
    case "$testing_dir/" in "$prod_dir"/*) die "testing's $var ($testing_dir) is inside production's" ;; esac
    # The contents of testing's directory are about to be deleted, so refuse
    # anything that isn't clearly one of these trees: a top-level
    # directory, or one holding anything but numeric id directories.
    [[ $testing_dir =~ ^/[^/]+(/[^/]+)+$ ]] \
      || die "testing's $var ($testing_dir) is too close to the filesystem root to empty safely"
    if [[ -d $testing_dir ]]; then
      stray=$(find "$testing_dir" -regextype posix-extended -mindepth 1 -maxdepth 1 \( ! -type d -o ! -regex '.*/[0-9]+' \) -print -quit)
      [[ -z $stray ]] \
        || die "testing's $var ($testing_dir) contains $stray, which is not a numeric id directory; refusing to empty it"
      probe_dir=$testing_dir
    else
      probe_dir=$(dirname "$testing_dir")
      [[ -d $probe_dir ]] || die "the parent of testing's $var ($probe_dir) does not exist"
    fi
    [[ $(stat -c %d "$prod_dir") == "$(stat -c %d "$probe_dir")" ]] \
      || die "$prod_dir and $probe_dir are on different filesystems, so hard links between them are impossible"
    sample=$(find "$prod_dir" -type f -print -quit)
    if [[ -n $sample ]]; then
      probe="$probe_dir/.link-probe.$$"
      ln -- "$sample" "$probe" 2>/dev/null \
        || die "cannot hard-link production's files from here (fs.protected_hardlinks only lets a user link files they own, other than root)"
      rm -f -- "$probe"
    fi
  done
  # The two trees must not overlap each other either.
  [[ ${testing_files[ACTIVITY_RESOURCES_ROOT]} != "${testing_files[TRANSACTION_FILES_ROOT]}" ]] \
    || die "testing's ACTIVITY_RESOURCES_ROOT and TRANSACTION_FILES_ROOT are the same directory"
fi

label() { [[ -z $1 ]] || printf ' (%s)' "$1"; }
if ((copy_files)); then
  files_line=""
  for var in "${file_vars[@]}"; do
    files_line+="  $var: ${testing_files[$var]} emptied, then hard-linked from ${prod_files[$var]}"$'\n'
  done
  files_line=${files_line%$'\n'}
  lost="the testing database and its activity and transaction files"
else
  files_line="  files:               left alone (--no-files)"
  lost="the testing database"
fi
cat <<EOF
About to REPLACE the testing database with a copy of production's data:
  production (source): ${conn[src.db]} on ${conn[src.host]:-local}:${conn[src.port]}$(label "$(env_value "$prod_env" PUBLIC_BASE_URL)"), migration $prod_version
  testing    (target): ${conn[tgt.db]} on ${conn[tgt.host]:-local}:${conn[tgt.port]}$(label "$(env_value "$testing_env" PUBLIC_BASE_URL)")
$files_line
  testing service:     $TESTING_SERVICE (stopped during the copy)
Everything currently in $lost will be lost.
EOF
if ((!assume_yes)); then
  read -r -p "Type the name of the testing database (${conn[tgt.db]}) to continue: " answer \
    || die "aborted: no confirmation given"
  [[ $answer == "${conn[tgt.db]}" ]] || die "aborted"
fi

log "stopping $TESTING_SERVICE"
"${systemctl_cmd[@]}" stop "$TESTING_SERVICE"
trap 'echo "error: the copy failed; $TESTING_SERVICE was left stopped" >&2' ERR

if ((copy_files)); then
  # Before dumping the database, so no file in testing's tree can be newer than
  # the database snapshot (see the note at the top).
  for var in "${file_vars[@]}"; do
    log "replacing testing's $var with hard links to production's"
    mkdir -p -- "${testing_files[$var]}"
    find "${testing_files[$var]}" -mindepth 1 -delete
    cp -al -- "${prod_files[$var]}"/. "${testing_files[$var]}"/
    log "linked $(find "${testing_files[$var]}" -type f | wc -l) files"
  done
fi

log "emptying the testing database"
PGOPTIONS='-c client_min_messages=warning' \
  pg tgt psql -q -v ON_ERROR_STOP=1 -c 'DROP SCHEMA IF EXISTS public CASCADE' -c 'CREATE SCHEMA public' </dev/null

log "restoring a dump of production into it"
pg src pg_dump --no-owner --no-acl | pg tgt psql -q -v ON_ERROR_STOP=1 --single-transaction >/dev/null

log "applying testing's own migrations"
GOOSE_DRIVER=postgres GOOSE_DBSTRING="$testing_url" "$GOOSE_BIN" -dir "$MIGRATIONS_DIR" up

log "starting $TESTING_SERVICE"
"${systemctl_cmd[@]}" start "$TESTING_SERVICE"
trap - ERR

testing_version=$(pg tgt psql -qAt -c 'SELECT COALESCE(max(version_id), 0) FROM goose_db_version WHERE is_applied' </dev/null)
log "done: testing now holds production's data, migrated from $prod_version to $testing_version"
