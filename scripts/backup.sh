#!/usr/bin/env bash
#
# Backs up an environment's data into a directory of its own: the database (a custom-format
# pg_dump, restorable with pg_restore) and the three directories of files (the club's documents,
# the per-person files and the activities' memoria files). Meant to be run once a day on the
# club's host by katiuskas-intranet-backup.timer (docs/environments.md, "Backups"), for
# production: testing's data is a copy of production's, and the three directories are shared.
#
# Usage:
#   BACKUP_DIR=<where to keep the backups> scripts/backup.sh <env-file>
#
# <env-file> is the environment's api.env (the systemd EnvironmentFile); DATABASE_URL,
# LIBRARY_ROOT, USER_FILES_ROOT and ACTIVITY_RESOURCES_ROOT are read from it, parsed, never
# sourced. Each run makes a directory BACKUP_DIR/<yyyymmdd-hhmmss>/ with
#   database.dump   pg_dump --format=custom (it includes goose's version table)
#   files/<name>/   library, user-files and activity-resources, as they were
# The files are copied with rsync, hard-linking whatever has not changed since the previous
# backup (--link-dest), so every backup is a complete tree that can be browsed or restored on its
# own and yet a day without changes costs almost no disk. Deleting an old backup never affects
# the others.
#
# The files are copied BEFORE the dump: a memoria file is stored before its row exists and
# removed after the row is deleted, so a dump taken after the copy never points at a file that
# the backup lacks (the reverse can leave an unreferenced file, which does no harm).
#
# A backup is built in <timestamp>.partial and renamed when complete and checked (pg_restore can
# read the dump); a run that fails removes it and exits with an error, so the unit shows as
# failed. Then the old ones are pruned: the newest KEEP_DAILY are kept, and of the older ones,
# the newest of each of the KEEP_WEEKLY most recent weeks.
#
# Everything is created readable only by the user running it: the files include people's
# documents. Backups on the same disk do not protect against losing it: AFTER_BACKUP can be an
# executable that copies the finished backup elsewhere (see docs/environments.md).
#
# Environment:
#   BACKUP_DIR    where the backups are kept (required)
#   KEEP_DAILY    how many of the newest backups to keep (default 14)
#   KEEP_WEEKLY   how many older weeks to keep one backup of (default 8)
#   AFTER_BACKUP  an executable, called as `$AFTER_BACKUP <backup directory>` once the backup
#                 is complete; if it fails, so does this run (the backup is kept)
#
# Needs pg_dump and pg_restore on PATH (the postgresql-client package, at least as new as the
# server) and rsync.
set -euo pipefail
umask 077

die() { echo "error: $*" >&2; exit 1; }
log() { echo "==> $*"; }

[[ $# -eq 1 ]] || die "usage: BACKUP_DIR=<directory> $0 <env-file>"
env_file=$1
: "${BACKUP_DIR:?set BACKUP_DIR to the directory the backups are kept in}"
KEEP_DAILY=${KEEP_DAILY:-14}
KEEP_WEEKLY=${KEEP_WEEKLY:-8}
[[ $KEEP_DAILY =~ ^[0-9]+$ && $KEEP_DAILY -ge 1 ]] || die "KEEP_DAILY must be a number, at least 1"
[[ $KEEP_WEEKLY =~ ^[0-9]+$ ]] || die "KEEP_WEEKLY must be a number"
[[ -r $env_file ]] || die "cannot read $env_file"
for tool in pg_dump pg_restore rsync flock; do
  command -v "$tool" >/dev/null || die "$tool not found"
done

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

# The connection goes through PG* environment variables: unlike a URL argument, a password there
# never shows up in `ps`.
database_url=$(env_value "$env_file" DATABASE_URL)
[[ -n $database_url ]] || die "no DATABASE_URL in $env_file"
re='^postgres(ql)?://([^:@/]*)(:([^@]*))?@([^:/?]*)(:([0-9]+))?/([^?]*)(\?(.*))?$'
[[ $database_url =~ $re ]] || die "cannot parse DATABASE_URL"
export PGUSER PGPASSWORD PGHOST PGPORT PGDATABASE PGSSLMODE
PGUSER=$(urldecode "${BASH_REMATCH[2]}")
PGPASSWORD=$(urldecode "${BASH_REMATCH[4]}")
PGHOST=${BASH_REMATCH[5]}
PGPORT=${BASH_REMATCH[7]:-5432}
PGDATABASE=$(urldecode "${BASH_REMATCH[8]}")
PGSSLMODE=prefer
if [[ ${BASH_REMATCH[10]} =~ sslmode=([^\&]*) ]]; then
  PGSSLMODE=${BASH_REMATCH[1]}
fi

# The directories to copy: the name each has in the backup, and where it is.
declare -A sources=(
  [library]=$(env_value "$env_file" LIBRARY_ROOT)
  [user-files]=$(env_value "$env_file" USER_FILES_ROOT)
  [activity-resources]=$(env_value "$env_file" ACTIVITY_RESOURCES_ROOT)
)
for name in "${!sources[@]}"; do
  [[ -n ${sources[$name]} ]] || die "no value for the $name directory in $env_file"
  [[ -d ${sources[$name]} ]] || die "the $name directory ${sources[$name]} does not exist"
done

mkdir -p "$BACKUP_DIR"
chmod 700 "$BACKUP_DIR"
exec 9>"$BACKUP_DIR/.lock"
flock -n 9 || die "another backup is running"

# What an earlier run that was killed left behind.
rm -rf "$BACKUP_DIR"/*.partial

# The newest complete backup, whose files the new one links to when they have not changed.
previous=$(find "$BACKUP_DIR" -mindepth 1 -maxdepth 1 -type d -name '[0-9]*-[0-9][0-9][0-9][0-9][0-9][0-9]' | sort | tail -n1)

stamp=$(date +%Y%m%d-%H%M%S)
partial=$BACKUP_DIR/$stamp.partial
final=$BACKUP_DIR/$stamp
[[ ! -e $final ]] || die "$final already exists"
trap 'rm -rf "$partial"' EXIT
mkdir "$partial"

for name in library user-files activity-resources; do
  log "copying $name from ${sources[$name]}"
  link=()
  if [[ -n $previous && -d $previous/files/$name ]]; then
    link=(--link-dest="$previous/files/$name")
  fi
  mkdir -p "$partial/files/$name"
  rsync -a --delete "${link[@]}" "${sources[$name]}/" "$partial/files/$name/"
done

log "dumping the database $PGDATABASE"
pg_dump --format=custom >"$partial/database.dump"
pg_restore --list "$partial/database.dump" >/dev/null || die "the dump cannot be read back"

mv "$partial" "$final"
trap - EXIT
log "backup complete: $final ($(du -sh --apparent-size "$final" | cut -f1), of which the database is $(du -h "$final/database.dump" | cut -f1))"

# Pruning: the newest KEEP_DAILY, then the newest backup of each of the KEEP_WEEKLY most recent
# other weeks. Going from the newest down, a week that already has a kept backup (a daily one, or
# the newer one of that week) does not keep another.
index=0
weeks_kept=0
declare -A seen_week
while read -r dir; do
  name=${dir##*/}
  week=$(date -d "${name:0:8}" +%G-%V)
  index=$((index + 1))
  if ((index <= KEEP_DAILY)); then
    seen_week[$week]=1
    continue
  fi
  if [[ -z ${seen_week[$week]:-} ]] && ((weeks_kept < KEEP_WEEKLY)); then
    seen_week[$week]=1
    weeks_kept=$((weeks_kept + 1))
    continue
  fi
  log "removing the old backup $name"
  rm -rf "$dir"
done < <(find "$BACKUP_DIR" -mindepth 1 -maxdepth 1 -type d -name '[0-9]*-[0-9][0-9][0-9][0-9][0-9][0-9]' | sort -r)

if [[ -n ${AFTER_BACKUP:-} ]]; then
  [[ -x $AFTER_BACKUP ]] || die "AFTER_BACKUP ($AFTER_BACKUP) is not executable"
  log "running $AFTER_BACKUP"
  "$AFTER_BACKUP" "$final" || die "$AFTER_BACKUP failed (the backup itself is complete)"
fi
