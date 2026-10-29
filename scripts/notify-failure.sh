#!/usr/bin/env bash
#
# Tells the administrators, by Telegram, that a systemd unit has failed. Started by systemd
# through katiuskas-intranet-notify-failure@.service, which the deployment and backup units name in
# OnFailure=, so that a run that fails is noticed without anybody having to look at the journal
# (docs/environments.md, "Alerts when something fails").
#
# Usage:
#   scripts/notify-failure.sh <env-file> <unit>
#
# <env-file> is an environment's api.env (production's): TELEGRAM_BOT_TOKEN and
# TELEGRAM_LOG_CHAT_ID are read from it, parsed, never sourced, and the message goes to that log
# channel, the same one the API writes to. The message says which unit failed, where, and the last
# lines of its journal when the user can read it (it has to be in the systemd-journal group).
#
# A unit that keeps failing (a broken commit that is tried again every 15 minutes, a network
# that is down) is told once, not at every attempt: the unit's name is remembered in
# NOTIFY_STATE_DIR/<unit>, and while that file is younger than NOTIFY_EVERY seconds nothing more
# is sent about it. The unit removes the file when a run succeeds (ExecStartPost=), so the next
# failure is told at once.
#
# Environment:
#   NOTIFY_STATE_DIR  where it remembers what it has told (default:
#                     /var/lib/katiuskas-intranet-deploy/notified)
#   NOTIFY_EVERY      seconds before the same unit is told again (default: 21600, six hours)
#
# Needs curl.
set -euo pipefail
umask 077

die() { echo "error: $*" >&2; exit 1; }

[[ $# -eq 2 ]] || die "usage: $0 <env-file> <unit>"
env_file=$1
unit=$2
[[ $unit =~ ^[A-Za-z0-9:_.@-]+$ ]] || die "invalid unit name"
[[ -r $env_file ]] || die "cannot read $env_file"
command -v curl >/dev/null || die "curl not found"
state_dir=${NOTIFY_STATE_DIR:-/var/lib/katiuskas-intranet-deploy/notified}
every=${NOTIFY_EVERY:-21600}
[[ $every =~ ^[0-9]+$ ]] || die "NOTIFY_EVERY must be a number of seconds"

env_value() {
  local value
  value=$(grep -E "^$2=" "$1" | tail -n1) || true
  value=${value#"$2="}
  value=${value#[\"\']}
  value=${value%[\"\']}
  printf '%s' "$value"
}

token=$(env_value "$env_file" TELEGRAM_BOT_TOKEN)
chat=$(env_value "$env_file" TELEGRAM_LOG_CHAT_ID)
[[ -n $token && -n $chat ]] || die "no TELEGRAM_BOT_TOKEN and TELEGRAM_LOG_CHAT_ID in $env_file"

mkdir -p "$state_dir"
marker=$state_dir/$unit
if [[ -e $marker ]] && (($(date +%s) - $(stat -c %Y "$marker") < every)); then
  echo "$unit failed again; already told less than $every s ago"
  exit 0
fi

tail_of_journal=$(journalctl -u "$unit" -n 12 --no-pager -o cat 2>/dev/null | tail -c 3000) || true
message="⚠️ Ha fallado $unit en $(hostname)"
if [[ -n $tail_of_journal ]]; then
  message+=$'\n\n'"$tail_of_journal"
fi
message+=$'\n\n'"journalctl -u $unit"

# The token is in the URL, which is given to curl on its standard input so that it does not show up
# in the list of processes.
printf 'url = "https://api.telegram.org/bot%s/sendMessage"\n' "$token" |
  curl --silent --show-error --fail --max-time 20 --config - \
    --data-urlencode "chat_id=$chat" --data-urlencode "text=$message" >/dev/null ||
  die "could not send the message to Telegram"

touch "$marker"
echo "told that $unit failed"
