#!/usr/bin/env bash
#
# Runs a job, coalescing concurrent requests into at most one queued rerun.
#
# Every call is a request to run the job. If nothing is running, the job runs. If it
# is already running, the request is remembered (a flag file) and this call returns
# at once; when the running job finishes it sees the flag and runs the job once more.
# However many requests arrive while it runs, they are all that one rerun: what the
# job looks at is read when it starts, so a single run after the last request has
# seen everything that was requested before it.
#
# The caller that finds nothing running is the one that does the work and returns
# when it and the reruns are done, so it has the job's output and, as exit status,
# that of the last run; a caller that only queued a request exits 0 straight away.
#
# Usage:
#   QUEUE_DIR=<directory> scripts/run-queued.sh <command> [arguments...]
#
# QUEUE_DIR holds the lock and the flag (create it writable by every user that may
# make a request). Used by scripts/kick-deploy.sh, which both the timer and the
# webhook run, so that a push that arrives while a deployment is running is not lost
# and a burst of pushes costs one more deployment, not one for each.
set -u

: "${QUEUE_DIR:?set QUEUE_DIR to the directory for the lock and the flag}"
(($# > 0)) || {
  echo "usage: QUEUE_DIR=<directory> $0 <command> [arguments...]" >&2
  exit 2
}

lock_file=$QUEUE_DIR/run.lock
pending_file=$QUEUE_DIR/pending

mkdir -p "$QUEUE_DIR" || exit 1

# 1. Register the request. It has to happen BEFORE trying to take the lock, so that
#    whoever holds it cannot finish without having seen this request.
touch "$pending_file" || exit 1

exec 9>"$lock_file" || exit 1

status=0
while true; do
  # 2. If another instance holds the lock, it will find the flag when it finishes:
  #    this request is already queued.
  flock -n 9 || exit 0

  # 3. Take the flag right before each run; requests that arrive during the run put
  #    it back, which gives exactly one more run.
  while rm "$pending_file" 2>/dev/null; do
    "$@"
    status=$?
    if ((status != 0)); then
      echo "$(date -Is) $1 failed with exit code $status" >&2
    fi
  done

  # 4. Release the lock and look at the flag once more: a request that came after
  #    the last check and before the unlock would otherwise be left unattended.
  flock -u 9
  [[ -e $pending_file ]] || exit "$status"
done
