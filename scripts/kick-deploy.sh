#!/bin/sh
#
# Requests a deployment: runs deploy.sh through run-queued.sh, so that requests that
# arrive while one is running are queued and merged into a single rerun. It is what
# both the systemd unit (started by the timer) and the webhook receiver execute, and
# the one command the receiver's user is allowed to run (as the owner of the
# installations; see the sudoers rule in docs/environments.md).
#
# Meant to be installed, owned by root and not writable by anybody else, next to
# deploy.sh and run-queued.sh (docs/environments.md). The paths and the queue
# directory can be changed with these variables, mostly for trying it out:
#   DEPLOY_DIR     where the three scripts are (default: /opt/katiuskas-deploy)
#   DEPLOY_CONFIG  deploy.sh's settings file (default: /etc/katiuskas-deploy.env)
#   QUEUE_DIR      where run-queued.sh keeps its lock and flag (default:
#                  /var/lib/katiuskas-deploy/queue)
DEPLOY_DIR=${DEPLOY_DIR:-/opt/katiuskas-deploy}
DEPLOY_CONFIG=${DEPLOY_CONFIG:-/etc/katiuskas-deploy.env}
QUEUE_DIR=${QUEUE_DIR:-/var/lib/katiuskas-deploy/queue}
export QUEUE_DIR
exec "$DEPLOY_DIR/run-queued.sh" "$DEPLOY_DIR/deploy.sh" --config "$DEPLOY_CONFIG"
