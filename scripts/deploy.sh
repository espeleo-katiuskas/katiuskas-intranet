#!/usr/bin/env bash
#
# Pull-based deployment of the testing and production environments from the
# public repository, meant to be run on the club's host through
# scripts/kick-deploy.sh: by a timer every few minutes and by the webhook receiver
# whenever GitHub reports a push, so that an update does not wait for the timer.
# Running it more often than needed costs nothing, because what it does never
# depends on who ran it; and scripts/run-queued.sh, which kick-deploy.sh goes
# through, makes a request that arrives while it is running wait for it to finish,
# and merges several into one.
#
# Nothing is ever pushed to the host: the webhook only asks the host to look, and
# there is no CI runner with access to it. The host looks at the repository by itself
# (`git ls-remote`, read-only, no credentials), decides what each environment
# should be running, and, if that is not what it runs now, builds it and installs
# it. Whatever the network says only matters as the name of a commit to fetch
# from the repository's fixed URL, so nobody who can reach the host can make it
# deploy anything.
#
#   testing     follows the tip of `main`.
#   production  follows the highest semantic-version tag (MAJOR.MINOR.PATCH, with an
#               optional leading "v"). To roll back, tag a new, higher version.
#               The tagged commit must be in the history of `main` (a tag on some
#               other branch is refused; see REQUIRE_ON_MAIN).
#
# One run asks the repository, compares the answer with what each environment
# last deployed, and returns right away if there is nothing new. Otherwise it
# fetches and updates, one after the other, the environments that need it,
# testing first. For each: check the commit out of the local clone; build the API
# (and seed-admin) with Go; apply the migrations with goose; swap in the binaries,
# `web/` and the migrations; restart the service and wait for its health check. If
# the service does not come back, the previous binary is put back (migrations are
# forward-only and stay applied). A failure in one environment does not stop the
# other one, but makes the run exit with an error. A commit that fails is not
# retried for RETRY_AFTER seconds, so a broken commit does not rebuild at every start.
#
# Usage, as the owner of the installations (not root):
#   scripts/deploy.sh [--config FILE] [--dry-run] [--force] [--adopt] [testing] [production]
# With no environment, every configured one.
#
# --config   reads the settings below from FILE (KEY=VALUE lines, like a systemd
#            EnvironmentFile) instead of from the environment. The shell sources it,
#            so it has to be trusted: it is refused if the user running the script
#            can write to it (keep it owned by root, readable by that user).
# --dry-run  says what it would deploy and exits without touching anything.
# --force    deploys even if that commit is what is already deployed, or failed
#            recently.
# --adopt    records what the repository says each environment should run as already
#            deployed, without deploying it. For the first time, when the
#            environments are already running that (see docs/environments.md).
#
# Environment (usually the EnvironmentFile of the systemd unit). An environment
# exists if its <ENV>_INSTALL_DIR is set, so a host can have only one of the two:
#   REPO_URL        https URL of the repository to follow (required)
#   STATE_DIR       where this script keeps its clone, the Go caches, a copy of
#                   goose and what it last deployed to each environment (required;
#                   a few hundred MB)
#   TESTING_INSTALL_DIR, PRODUCTION_INSTALL_DIR
#                   where each environment lives: bin/, web/, db/migrations/ and the
#                   api.env of its service
#   TESTING_SERVICE, PRODUCTION_SERVICE
#                   systemd unit of each API, restarted after installing
#                   (required for each configured environment)
#   TESTING_HEALTH_URL, PRODUCTION_HEALTH_URL
#                   URL polled after the restart. Default: /api/v1/health on the
#                   HTTP_ADDR of that environment's api.env. Set it empty to skip the check.
#   SYSTEMCTL       command to restart the units, which may carry arguments
#                   (default: "sudo -n systemctl")
#   GO              the go command (default: go). The toolchain is never
#                   downloaded (GOTOOLCHAIN=local): the installed one has to be
#                   at least what go.mod asks for.
#   GOOSE_VERSION   goose to build for the migrations (default: v3.22.1)
#   HEALTH_TIMEOUT  seconds to wait for the health check (default: 30)
#   REQUIRE_ON_MAIN 1 (default) refuses a production tag whose commit is not in the
#                   history of main; 0 allows it
#   RETRY_AFTER     seconds before a commit that failed is tried again (default: 900)
#   AFTER_DEPLOY    executable called after each successful update as
#                   `$AFTER_DEPLOY <environment> <commit> <ref>`, for example to
#                   refresh testing's data after a production deployment
#                   (docs/environments.md). If it fails the update stays recorded
#                   and the run exits with an error.
#
# Needs git, Go, flock and, for the health checks, curl on PATH.
set -euo pipefail

die() { echo "error: $*" >&2; exit 1; }
log() { echo "==> $*"; }

dry_run=0
force=0
adopt=0
config=
while [[ ${1:-} == --* ]]; do
  case $1 in
    --config)
      shift
      config=${1:-}
      [[ -n $config ]] || die "--config needs a file"
      ;;
    --dry-run) dry_run=1 ;;
    --force) force=1 ;;
    --adopt) adopt=1 ;;
    *) die "unknown option $1" ;;
  esac
  shift
done
((EUID != 0)) || die "run as the owner of the installations, not as root"

if [[ -n $config ]]; then
  [[ -r $config ]] || die "cannot read $config"
  [[ ! -w $config ]] || die "$config is writable by $(id -un), and it is sourced as shell code: make it owned by root"
  set -a
  # shellcheck source=/dev/null
  . "$config"
  set +a
fi

: "${REPO_URL:?set REPO_URL to the repository to follow}"
: "${STATE_DIR:?set STATE_DIR to the working directory of this script}"
GO=${GO:-go}
GOOSE_VERSION=${GOOSE_VERSION:-v3.22.1}
HEALTH_TIMEOUT=${HEALTH_TIMEOUT:-30}
REQUIRE_ON_MAIN=${REQUIRE_ON_MAIN:-1}
RETRY_AFTER=${RETRY_AFTER:-900}
# Split once on whitespace (no globbing) so SYSTEMCTL can be "sudo -n systemctl".
read -r -a systemctl_cmd <<<"${SYSTEMCTL:-sudo -n systemctl}"

# setting <environment> <NAME>: the value of TESTING_<NAME> or PRODUCTION_<NAME>.
setting() {
  local var=${1^^}_$2
  printf '%s' "${!var:-}"
}

# Which environments (always in this order: testing first), and are they configured?
for e in "$@"; do
  [[ $e == testing || $e == production ]] || die "the environments are testing and production, not '$e'"
done
configured=()
for e in testing production; do
  if (($# > 0)) && [[ " $* " != *" $e "* ]]; then
    continue
  fi
  if [[ -z $(setting "$e" INSTALL_DIR) ]]; then
    (($# == 0)) || die "$e is not configured (set ${e^^}_INSTALL_DIR)"
    continue
  fi
  configured+=("$e")
done
((${#configured[@]} > 0)) || die "no environment is configured (set TESTING_INSTALL_DIR and/or PRODUCTION_INSTALL_DIR)"

# Reads KEY from an env file without executing it.
env_value() {
  local value
  value=$(grep -E "^$2=" "$1" | tail -n1) || true
  value=${value#"$2="}
  value=${value#[\"\']}
  value=${value%[\"\']}
  printf '%s' "$value"
}

need_curl=0
for e in "${configured[@]}"; do
  install_dir=$(setting "$e" INSTALL_DIR)
  [[ -n $(setting "$e" SERVICE) ]] || die "set ${e^^}_SERVICE to the systemd unit of the $e API"
  [[ -r $install_dir/api.env ]] || die "cannot read $install_dir/api.env"
  [[ -w $install_dir ]] || die "$install_dir is not writable by $(id -un)"
  health_var=${e^^}_HEALTH_URL
  [[ -n ${!health_var+set} && -z ${!health_var} ]] || need_curl=1
done
for tool in git flock "$GO"; do
  command -v "$tool" >/dev/null || die "$tool not found on PATH"
done
((!need_curl)) || command -v curl >/dev/null || die "curl not found on PATH (or set the health URLs empty to skip the check)"

mkdir -p "$STATE_DIR"
exec 9>"$STATE_DIR/lock"
if ! flock -n 9; then
  log "another update is running; nothing to do"
  exit 0
fi

src=$STATE_DIR/src

# deploy_env <environment>: updates it to its target commit, or dies trying.
deploy_env() {
  local e=$1 ref=${target_ref[$1]} sha=${target_sha[$1]}
  local install_dir service state api_env build
  install_dir=$(setting "$e" INSTALL_DIR)
  service=$(setting "$e" SERVICE)
  state=$STATE_DIR/$e
  api_env=$install_dir/api.env
  build=$STATE_DIR/build

  log "deploying $ref ($sha) to $e"

  git -C "$src" cat-file -e "$sha^{commit}" 2>/dev/null || die "the repository said $ref is $sha, but it could not be fetched"
  if [[ $e == production && $REQUIRE_ON_MAIN == 1 ]]; then
    git -C "$src" merge-base --is-ancestor "$sha" origin/main ||
      die "tag $ref ($sha) is not in the history of main; refusing to deploy it to production"
  fi
  git -C "$src" checkout --quiet --force --detach "$sha"
  git -C "$src" clean --quiet -fdx

  # ---- build
  rm -rf "$build"
  mkdir -p "$build"
  log "building the API and seed-admin"
  (
    cd "$src/backend"
    "$GO" build -o "$build/katiuskas-api" ./cmd/api
    "$GO" build -o "$build/seed-admin" ./cmd/seed-admin
  )
  if [[ ! -x $STATE_DIR/bin/goose ]]; then
    log "building goose $GOOSE_VERSION"
    (cd "$STATE_DIR" && GOFLAGS= GOBIN="$STATE_DIR/bin" "$GO" install "github.com/pressly/goose/v3/cmd/goose@$GOOSE_VERSION")
  fi
  cp -p "$STATE_DIR/bin/goose" "$build/goose"

  # ---- migrate
  # Before touching anything installed: if a migration fails, what runs is
  # untouched. goose reads the connection from the environment, so the password
  # never shows up in `ps`.
  local database_url
  database_url=$(env_value "$api_env" DATABASE_URL)
  [[ -n $database_url ]] || die "no DATABASE_URL in $api_env"
  log "applying migrations"
  GOOSE_DRIVER=postgres GOOSE_DBSTRING=$database_url \
    "$build/goose" -dir "$src/db/migrations" up

  # ---- install
  log "installing"
  mkdir -p "$install_dir/bin" "$install_dir/db"
  if [[ -e $install_dir/bin/katiuskas-api ]]; then
    cp -p "$install_dir/bin/katiuskas-api" "$install_dir/bin/katiuskas-api.prev"
  fi
  local bin dir
  for bin in katiuskas-api seed-admin goose; do
    install -m 0755 "$build/$bin" "$install_dir/bin/.$bin.new"
    mv -f "$install_dir/bin/.$bin.new" "$install_dir/bin/$bin"
  done
  # Directories are swapped, not synchronized in place, so that a request never
  # sees half of a copy.
  for dir in web db/migrations; do
    rm -rf "$install_dir/$dir.new" "$install_dir/$dir.old"
    mkdir -p "$install_dir/$dir.new"
    cp -a "$src/$dir/." "$install_dir/$dir.new/"
    [[ ! -e $install_dir/$dir ]] || mv "$install_dir/$dir" "$install_dir/$dir.old"
    mv "$install_dir/$dir.new" "$install_dir/$dir"
    rm -rf "$install_dir/$dir.old"
  done

  # ---- restart and check
  log "restarting $service"
  "${systemctl_cmd[@]}" restart "$service"
  local health_var=${e^^}_HEALTH_URL health_url addr
  if [[ -n ${!health_var+set} ]]; then
    health_url=${!health_var}
  else
    addr=$(env_value "$api_env" HTTP_ADDR)
    addr=${addr:-:64711}
    health_url=http://127.0.0.1:${addr##*:}/api/v1/health
  fi
  if [[ -n $health_url ]]; then
    local healthy=0 i
    for ((i = 0; i < HEALTH_TIMEOUT; i++)); do
      if curl -fsS --max-time 2 -o /dev/null "$health_url" 2>/dev/null; then
        healthy=1
        break
      fi
      sleep 1
    done
    if ((!healthy)); then
      echo "error: $health_url did not answer within $HEALTH_TIMEOUT s" >&2
      if [[ -e $install_dir/bin/katiuskas-api.prev ]]; then
        log "putting the previous binary back"
        install -m 0755 "$install_dir/bin/katiuskas-api.prev" "$install_dir/bin/.katiuskas-api.new"
        mv -f "$install_dir/bin/.katiuskas-api.new" "$install_dir/bin/katiuskas-api"
        "${systemctl_cmd[@]}" restart "$service" || true
      fi
      exit 1
    fi
  fi

  printf '%s %s\n' "$ref" "$sha" >"$state/deployed.tmp"
  mv -f "$state/deployed.tmp" "$state/deployed"
  rm -f "$state/failed"
  log "$e is now at $ref ($sha)"
}

# ---- what should be running -------------------------------------------------
# One question to the repository for both: the tip of main and every tag. An
# annotated tag appears as the tag object and, on a second line ending in ^{},
# the commit it points at; the commit is what counts.
refs=$(git ls-remote "$REPO_URL" 'refs/heads/main' 'refs/tags/*')
main_sha=$(awk '$2 == "refs/heads/main" { print $1 }' <<<"$refs")
latest_tag=$(awk '
  $2 ~ /^refs\/tags\// { name = $2; sub("^refs/tags/", "", name)
    if (name ~ /\^\{\}$/) { sub(/\^\{\}$/, "", name); peeled[name] = $1 } else plain[name] = $1 }
  END { for (n in plain) print n, (n in peeled ? peeled[n] : plain[n]) }' <<<"$refs" |
  grep -E '^v?[0-9]+\.[0-9]+\.[0-9]+ ' | sort -V -k1,1 | tail -n1) || true

declare -A target_ref target_sha
for e in "${configured[@]}"; do
  if [[ $e == testing ]]; then
    [[ -n $main_sha ]] || die "$REPO_URL has no branch main"
    target_ref[$e]=main
    target_sha[$e]=$main_sha
  else
    [[ -n $latest_tag ]] || die "$REPO_URL has no semantic-version tag"
    target_ref[$e]=${latest_tag% *}
    target_sha[$e]=${latest_tag#* }
  fi
done

# Which of them need an update (or --adopt, or just a message)?
pending=()
for e in "${configured[@]}"; do
  ref=${target_ref[$e]}
  sha=${target_sha[$e]}
  state=$STATE_DIR/$e
  mkdir -p "$state"
  if ((adopt)); then
    printf '%s %s\n' "$ref" "$sha" >"$state/deployed"
    rm -f "$state/failed"
    log "recorded $ref ($sha) as deployed to $e"
    continue
  fi
  if ((!force)); then
    if [[ -r $state/deployed && $(<"$state/deployed") == "$ref $sha" ]]; then
      log "$e is already at $ref ($sha)"
      continue
    fi
    if [[ -r $state/failed ]]; then
      read -r failed_ref failed_sha failed_at <"$state/failed" || true
      if [[ ${failed_ref:-} == "$ref" && ${failed_sha:-} == "$sha" ]] && (($(date +%s) - ${failed_at:-0} < RETRY_AFTER)); then
        log "$e: $ref ($sha) failed recently; not trying again until $RETRY_AFTER s have passed"
        continue
      fi
    fi
  fi
  if ((dry_run)); then
    log "would deploy $ref ($sha) to $e; now: $(cat "$state/deployed" 2>/dev/null || echo nothing)"
    continue
  fi
  pending+=("$e")
done
((${#pending[@]} > 0)) || exit 0

# ---- fetch -----------------------------------------------------------------
mkdir -p "$STATE_DIR/cache" "$STATE_DIR/bin"
export GOCACHE=$STATE_DIR/cache/go-build GOMODCACHE=$STATE_DIR/cache/mod GOTOOLCHAIN=local GOFLAGS=-mod=readonly
export CGO_ENABLED=0 GOOS=linux
GOARCH=$("$GO" env GOARCH)
export GOARCH

if [[ -d $src/.git ]]; then
  git -C "$src" remote set-url origin "$REPO_URL"
  git -C "$src" fetch --quiet --tags --force --prune origin '+refs/heads/main:refs/remotes/origin/main'
else
  rm -rf "$src"
  git clone --quiet "$REPO_URL" "$src"
fi

# ---- update, testing first --------------------------------------------------
# Testing first, so that if production's update refreshes testing's data from
# production's (AFTER_DEPLOY), testing's code and migrations are already the new ones.
status=0
for e in "${pending[@]}"; do
  # Each environment in a subshell with errexit on, whose failure is recorded and
  # does not stop the next one (errexit is ignored inside an `if` or `||`, hence
  # the explicit set +e / $? around it).
  set +e
  (
    set -e
    deploy_env "$e"
  )
  rc=$?
  set -e
  if ((rc != 0)); then
    printf '%s %s %s\n' "${target_ref[$e]}" "${target_sha[$e]}" "$(date +%s)" >"$STATE_DIR/$e/failed"
    echo "error: updating $e failed" >&2
    status=1
    continue
  fi
  if [[ -n ${AFTER_DEPLOY:-} ]]; then
    log "running $AFTER_DEPLOY"
    "$AFTER_DEPLOY" "$e" "${target_sha[$e]}" "${target_ref[$e]}" || {
      echo "error: $AFTER_DEPLOY failed (the update of $e is done)" >&2
      status=1
    }
  fi
done
exit "$status"
