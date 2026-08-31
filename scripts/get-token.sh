#!/usr/bin/env bash
# Mints a session token directly in the database for local dev use,
# printing it so it can be reused in further curl calls. Skips the real
# sign-in flow entirely — no magic link, no SMTP, no log-scraping, just
# an INSERT — so this is only ever meant for a local dev stack, never
# anything resembling production.
#
# Usage:
#   scripts/get-token.sh <login>
#   scripts/get-token.sh --user <login|email|name substring>
#
# The --user form looks a person up more loosely (their login, email, or
# part of their name) for when you don't remember their exact login — a
# convenience only sensible for local dev.
#
# Requires: the docker compose stack running.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

# How long the minted token stays valid — matches config.go's real
# session TokenTTL default (30 days), overridable for a test that wants
# a short-lived or already-expired one.
TOKEN_TTL_DAYS="${TOKEN_TTL_DAYS:-30}"

psql() {
  docker compose exec -T postgres psql -U intranet -d intranet -v ON_ERROR_STOP=1 -tA "$@"
}

usage() {
  echo "Usage:" >&2
  echo "  $0 <login>" >&2
  echo "  $0 --user <login|email|name substring>" >&2
  exit 1
}

[ $# -ge 1 ] || usage

if [ "$1" = "--user" ]; then
  [ $# -eq 2 ] || usage
  search="${2//\'/\'\'}"

  matches=$(psql -F'|' -c "
    SELECT login, email, first_name || ' ' || last_name
    FROM users
    WHERE login = '$search'
       OR email ILIKE '%$search%'
       OR (first_name || ' ' || last_name) ILIKE '%$search%'
    ORDER BY login
  ")
  match_count=$(grep -c . <<<"$matches" || true)

  if [ "$match_count" -eq 0 ]; then
    echo "No user matches '$search'." >&2
    exit 1
  fi
  if [ "$match_count" -gt 1 ]; then
    echo "Several users match '$search' — be more specific:" >&2
    awk -F'|' '{printf "  %-25s %-25s %s\n", $1, $3, $2}' <<<"$matches" >&2
    exit 1
  fi

  login=$(cut -d'|' -f1 <<<"$matches")
else
  [ $# -eq 1 ] || usage
  login="$1"
fi

login_esc="${login//\'/\'\'}"

user_id=$(psql -c "SELECT id FROM users WHERE login = '$login_esc'")
if [ -z "$user_id" ]; then
  echo "No user with login '$login'." >&2
  exit 1
fi

raw_token=$(openssl rand -hex 32)
token_hash=$(printf '%s' "$raw_token" | sha256sum | cut -d' ' -f1)

psql -c "
  INSERT INTO tokens (user_id, token_hash, expires_at)
  VALUES ($user_id, '$token_hash', now() + interval '$TOKEN_TTL_DAYS days')
" > /dev/null

# Mirrors roles.Repository.ListNamesForUser: explicitly assigned roles,
# plus member/former_member/board computed the same way the API itself
# resolves them live on every request.
roles=$(psql -c "
  SELECT COALESCE(string_agg(name, ', '), '(none)') FROM (
    SELECT r.name FROM roles r JOIN user_roles ur ON ur.role_id = r.id WHERE ur.user_id = $user_id

    UNION

    SELECT 'member' WHERE EXISTS (
      SELECT 1 FROM memberships m WHERE m.user_id = $user_id AND m.period @> CURRENT_DATE
    )

    UNION

    SELECT 'former_member' WHERE EXISTS (
      SELECT 1 FROM memberships m WHERE m.user_id = $user_id
    ) AND NOT EXISTS (
      SELECT 1 FROM memberships m WHERE m.user_id = $user_id AND m.period @> CURRENT_DATE
    )

    UNION

    SELECT 'board' WHERE EXISTS (
      SELECT 1 FROM board_members bm WHERE bm.user_id = $user_id AND bm.period @> CURRENT_DATE
    )
  ) names
")

echo "Minted a token for $login (roles: $roles)." >&2
echo >&2
echo "export TOKEN=$raw_token"
