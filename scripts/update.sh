#!/usr/bin/env bash
# NimOS staged updater: build first, install only after every required build succeeds.
set -Eeuo pipefail

DIR="/opt/nimos"
URL="https://github.com/andresgv-beep/NimOs-beta-9-alpha/archive/refs/heads/main.tar.gz"
RESULT_FILE="/var/log/nimos/update-result.json"
LOG_FILE="/var/log/nimos/update.log"
STAGE="$(mktemp -d /var/tmp/nimos-update.XXXXXX)"

mkdir -p /var/log/nimos
cleanup() { rm -rf -- "$STAGE"; }
trap cleanup EXIT

log() { echo "[$(date -Iseconds)] $*" | tee -a "$LOG_FILE"; }
result_error() {
  local code="$1"
  printf '{"type":"error","error":"%s","time":"%s"}\n' "$code" "$(date -Iseconds)" > "$RESULT_FILE"
}
version_from() {
  grep -o '"version": *"[^"]*"' "$1/package.json" 2>/dev/null | cut -d'"' -f4 || echo "unknown"
}
tree_hash() {
  local root="$1"
  shift
  (cd "$root" && find . "$@" -type f -exec sha256sum {} \; 2>/dev/null) \
    | sort | sha256sum | cut -d' ' -f1
}

PREV="$(version_from "$DIR")"
log "Current version: $PREV"
log "Downloading update into staging..."
if ! curl --connect-timeout 10 --max-time 180 -fsSL "$URL" \
  | tar xz --strip-components=1 -C "$STAGE"; then
  log "ERROR: Download failed; installed files were not modified"
  result_error "download_failed"
  exit 1
fi

NEW="$(version_from "$STAGE")"
if [[ "$NEW" == "unknown" ]]; then
  log "ERROR: Downloaded package has no valid version"
  result_error "invalid_package"
  exit 1
fi
log "Downloaded version: $NEW"

DAEMON_HASH="$(tree_hash "$DIR/daemon" -name '*.go')"
DAEMON_HASH_NEW="$(tree_hash "$STAGE/daemon" -name '*.go')"
FRONTEND_HASH="$(tree_hash "$DIR/src")"
FRONTEND_HASH_NEW="$(tree_hash "$STAGE/src")"

DAEMON_CHANGED=false
FRONTEND_CHANGED=false
[[ "$DAEMON_HASH" != "$DAEMON_HASH_NEW" || ! -x "$DIR/daemon/nimos-daemon" ]] && DAEMON_CHANGED=true
[[ "$FRONTEND_HASH" != "$FRONTEND_HASH_NEW" || ! -f "$DIR/dist/.nimos-build" ]] && FRONTEND_CHANGED=true

if [[ "$DAEMON_CHANGED" == true ]]; then
  log "Daemon source changed — building in staging..."
  if ! command -v go >/dev/null 2>&1; then
    log "ERROR: Go compiler is not installed"
    result_error "go_missing"
    exit 1
  fi
  if ! (cd "$STAGE/daemon" && go build -o nimos-daemon .) 2>&1 | tee -a "$LOG_FILE"; then
    log "ERROR: Daemon build failed; installed files were not modified"
    result_error "build_failed"
    exit 1
  fi
  chmod 0755 "$STAGE/daemon/nimos-daemon"
fi

if [[ "$FRONTEND_CHANGED" == true ]]; then
  log "Frontend source changed or its build is incomplete — building in staging..."
  if ! command -v node >/dev/null 2>&1 || ! command -v npm >/dev/null 2>&1; then
    log "ERROR: Node.js/npm are not installed"
    result_error "node_missing"
    exit 1
  fi
  NODE_MAJOR="$(node -p 'Number(process.versions.node.split(".")[0])' 2>/dev/null || echo 0)"
  if (( NODE_MAJOR < 18 )); then
    log "ERROR: Node.js 18 or newer is required (found $(node -v 2>/dev/null || echo unknown))"
    result_error "node_too_old"
    exit 1
  fi
  if ! (cd "$STAGE" && npm ci --no-audit --no-fund && npm run build) 2>&1 | tee -a "$LOG_FILE"; then
    log "ERROR: Frontend build failed; installed files were not modified"
    result_error "frontend_build_failed"
    exit 1
  fi
  if [[ ! -f "$STAGE/dist/index.html" ]]; then
    log "ERROR: Frontend build did not create dist/index.html"
    result_error "frontend_dist_missing"
    exit 1
  fi
  printf '%s\n' "$FRONTEND_HASH_NEW" > "$STAGE/dist/.nimos-build"
fi

log "All builds passed — installing $NEW..."
systemctl stop nimos-daemon 2>/dev/null || true

tar -C "$STAGE" \
  --exclude='./dist' --exclude='./node_modules' --exclude='./.svelte-kit' \
  -cf - . | tar -C "$DIR" -xf -

if [[ "$FRONTEND_CHANGED" == true ]]; then
  rm -rf -- "$DIR/dist.next"
  cp -a "$STAGE/dist" "$DIR/dist.next"
  rm -rf -- "$DIR/dist.previous"
  [[ ! -d "$DIR/dist" ]] || mv "$DIR/dist" "$DIR/dist.previous"
  mv "$DIR/dist.next" "$DIR/dist"
fi

if [[ -f "$DIR/scripts/nimos-daemon.service" ]]; then
  cp "$DIR/scripts/nimos-daemon.service" /etc/systemd/system/nimos-daemon.service
  systemctl daemon-reload
fi
chown -R nimos:nimos "$DIR" 2>/dev/null || true

log "Restarting services..."
if ! systemctl restart nimos-daemon; then
  log "ERROR: nimos-daemon could not be restarted"
  result_error "start_failed"
  exit 1
fi
systemctl restart nimos-torrentd 2>/dev/null || true

if ! systemctl is-active --quiet nimos-daemon; then
  log "ERROR: nimos-daemon is not active after the update"
  result_error "start_failed"
  exit 1
fi

rm -rf -- "$DIR/dist.previous"
UPDATE_TYPE="none"
[[ "$FRONTEND_CHANGED" == true ]] && UPDATE_TYPE="frontend"
[[ "$DAEMON_CHANGED" == true ]] && UPDATE_TYPE="full"
log "OK: $PREV -> $NEW ($UPDATE_TYPE)"
printf '{"type":"%s","prev":"%s","new":"%s","time":"%s"}\n' \
  "$UPDATE_TYPE" "$PREV" "$NEW" "$(date -Iseconds)" > "$RESULT_FILE"
