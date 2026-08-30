#!/usr/bin/env bash
# Requests and consumes a magic-link sign-in against the local dev stack,
# printing the resulting session token so it can be reused in further
# curl calls.
#
# Usage:
#   scripts/get-token.sh <email> <phone>
#   scripts/get-token.sh --user <id|email|name substring>
#
# The --user form looks the person's email/phone up in the database
# directly (docker compose exec postgres) — a convenience only sensible
# for local dev, since the API itself never reveals whether a user
# exists (see /auth/magic-link's anti-enumeration design).
#
# Requires: the docker compose stack running, and SMTP_HOST unset (the
# docker-compose.yml default) so the API logs the sign-in link instead
# of actually emailing it.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

API_BASE="${API_BASE:-http://localhost:8000/api/v1}"

usage() {
  echo "Usage:" >&2
  echo "  $0 <email> <phone>" >&2
  echo "  $0 --user <id|email|name substring>" >&2
  exit 1
}

[ $# -ge 1 ] || usage

if [ "$1" = "--user" ]; then
  [ $# -eq 2 ] || usage
  search="${2//\'/\'\'}"

  matches=$(docker compose exec -T postgres psql -U intranet -d intranet -tA -F'|' -c "
    SELECT id, email, phone, first_name || ' ' || last_name
    FROM users
    WHERE id::text = '$search'
       OR email ILIKE '%$search%'
       OR (first_name || ' ' || last_name) ILIKE '%$search%'
    ORDER BY id
  ")
  match_count=$(grep -c . <<<"$matches" || true)

  if [ "$match_count" -eq 0 ]; then
    echo "No user matches '$search'." >&2
    exit 1
  fi
  if [ "$match_count" -gt 1 ]; then
    echo "Several users match '$search' — be more specific:" >&2
    awk -F'|' '{printf "  #%s  %-25s  %s\n", $1, $4, $2}' <<<"$matches" >&2
    exit 1
  fi

  email=$(cut -d'|' -f2 <<<"$matches")
  phone=$(cut -d'|' -f3 <<<"$matches")
  if [ -z "$phone" ]; then
    echo "That user has no phone on file, so they can't sign in (email and phone must both match)." >&2
    exit 1
  fi
else
  [ $# -eq 2 ] || usage
  email="$1"
  phone="$2"
fi

echo "Requesting a sign-in link for $email..." >&2
# Recorded before the request so the log search below can reject a stale
# link from an earlier run — the API replies 200 either way (by design,
# see /auth/magic-link's anti-enumeration behavior), even when a
# per-account cooldown silently suppresses issuing a new one.
requested_at=$(date -u +%Y-%m-%dT%H:%M:%S.%6NZ)
curl -sf -o /dev/null -X POST "$API_BASE/auth/magic-link" \
  -H "Content-Type: application/json" \
  -d "$(python3 -c 'import json,sys; print(json.dumps({"email": sys.argv[1], "phone": sys.argv[2]}))' "$email" "$phone")"

# The API only logs the link (doesn't send it) when SMTP_HOST isn't
# configured. Poll the recent logs for it, giving the line a moment to land.
token=""
for _ in 1 2 3 4 5; do
  token=$(docker compose logs api --tail 50 2>/dev/null | python3 -c "
import json, re, sys
from datetime import datetime, timezone

email, requested_at = sys.argv[1], sys.argv[2]
cutoff = datetime.strptime(requested_at, '%Y-%m-%dT%H:%M:%S.%fZ').replace(tzinfo=timezone.utc)
token = ''
for line in sys.stdin:
    brace = line.find('{')
    if brace == -1:
        continue
    try:
        entry = json.loads(line[brace:])
    except ValueError:
        continue
    if entry.get('to') != email:
        continue
    try:
        logged_at = datetime.strptime(entry['time'][:26] + 'Z', '%Y-%m-%dT%H:%M:%S.%fZ').replace(tzinfo=timezone.utc)
    except (KeyError, ValueError):
        continue
    if logged_at < cutoff:
        continue
    m = re.search(r'token=([A-Za-z0-9_-]+)', entry.get('body', ''))
    if m:
        token = m.group(1)
print(token)
" "$email" "$requested_at")
  [ -n "$token" ] && break
  sleep 1
done

if [ -z "$token" ]; then
  echo "Couldn't find a fresh logged sign-in link for $email." >&2
  echo "Either SMTP_HOST is actually configured (check a real inbox instead)," >&2
  echo "or a link was already requested for this account in the last minute" >&2
  echo "and the per-account cooldown suppressed issuing a new one — wait a" >&2
  echo "bit and try again." >&2
  exit 1
fi

echo "Consuming the link..." >&2
response=$(curl -sf -X POST "$API_BASE/auth/magic-link/consume" \
  -H "Content-Type: application/json" \
  -d "$(python3 -c 'import json,sys; print(json.dumps({"token": sys.argv[1]}))' "$token")")

session_token=$(python3 -c "import json,sys; print(json.load(sys.stdin)['token'])" <<<"$response")

# The token is opaque (nothing to decode locally, unlike the old JWT), so
# ask the API itself who it belongs to. This can 403 — a person with no
# admin/board/member role can still sign in, they just can't call any API
# endpoint (including this one), so don't let that abort the script.
me=$(curl -s "$API_BASE/auth/me" -H "Authorization: Bearer $session_token")
roles=$(python3 -c "
import json, sys
body = json.load(sys.stdin)
print(', '.join(body['roles']) or '(none)' if 'roles' in body else '(no baseline role, so /auth/me itself is forbidden)')
" <<<"$me")

echo "Signed in as $email (roles: $roles)." >&2
echo >&2
echo "export TOKEN=$session_token"
