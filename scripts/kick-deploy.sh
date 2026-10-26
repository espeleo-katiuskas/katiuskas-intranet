#!/usr/bin/env bash
#
# Requests a deployment: runs deploy.sh, coalescing concurrent requests into at most one
# queued rerun. It is what both the systemd unit (started by the timer) and the webhook
# receiver execute, and the one command the receiver's user is allowed to run (as the owner
# of the installations; see the sudoers rule in docs/environments.md).
#
# Every call is a request to deploy. If nothing is running, deploy.sh runs. If it is already
# running, the request is remembered (a flag file) and this call returns at once; when the
# running deploy.sh finishes it sees the flag and runs once more. However many requests
# arrive while it runs, they are all that one rerun: what deploy.sh looks at (the
# repository) is read when it starts, so a single run after the last request has seen
# everything that was requested before it. So a push that lands while a deployment is
# building is deployed right after it, and a burst of pushes costs one more deployment, not
# one for each.
#
# The caller that finds nothing running is the one that does the work and returns when it
# and the reruns are done, so it has deploy.sh's output and, as exit status, that of the
# last run; a caller that only queued a request exits 0 straight away. (A webhook receiver
# must therefore not wait for it.)
#
# Meant to be installed, owned by root and not writable by anybody else, next to deploy.sh
# (docs/environments.md). The paths can be changed with these variables, mostly for trying
# it out:
#   DEPLOY_DIR     where deploy.sh is (default: /opt/katiuskas-deploy)
#   DEPLOY_CONFIG  deploy.sh's settings file (default: /opt/katiuskas-deploy/deploy.env)
#   QUEUE_DIR      where the lock and the flag are kept, writable by every user that may
#                  make a request (default: /var/lib/katiuskas-deploy/queue)
set -u

DEPLOY_DIR=${DEPLOY_DIR:-/opt/katiuskas-deploy}
DEPLOY_CONFIG=${DEPLOY_CONFIG:-$DEPLOY_DIR/deploy.env}
QUEUE_DIR=${QUEUE_DIR:-/var/lib/katiuskas-deploy/queue}
job=("$DEPLOY_DIR/deploy.sh" --config "$DEPLOY_CONFIG")

lock_file=$QUEUE_DIR/run.lock
pending_file=$QUEUE_DIR/pending

mkdir -p "$QUEUE_DIR" || exit 1

# 1. Register the request. It has to happen BEFORE trying to take the lock, so that whoever
#    holds it cannot finish without having seen this request.
touch "$pending_file" || exit 1

exec 9>"$lock_file" || exit 1

status=0
while true; do
  # 2. If another instance holds the lock, it will find the flag when it finishes: this
  #    request is already queued.
  flock -n 9 || exit 0

  # 3. Take the flag right before each run; requests that arrive during the run put it
  #    back, which gives exactly one more run.
  while rm "$pending_file" 2>/dev/null; do
    "${job[@]}"
    status=$?
    if ((status != 0)); then
      echo "$(date -Is) deploy.sh failed with exit code $status" >&2
    fi
  done

  # 4. Release the lock and look at the flag once more: a request that came after the last
  #    check and before the unlock would otherwise be left unattended.
  flock -u 9
  [[ -e $pending_file ]] || exit "$status"
done
