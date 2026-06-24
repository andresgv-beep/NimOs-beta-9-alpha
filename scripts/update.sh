#!/usr/bin/env bash
# NimOS Update Script — 100% Go architecture
set -euo pipefail

DIR="/opt/nimos"
URL="https://github.com/andresgv-beep/NimOs-beta-9/archive/refs/heads/main.tar.gz"
RESULT_FILE="/var/log/nimos/update-result.json"
LOG_FILE="/var/log/nimos/update.log"

mkdir -p /var/log/nimos

log() { echo "[$(date -Iseconds)] $*" | tee -a "$LOG_FILE"; }

# Get current version from package.json (simple grep, no node)
PREV=$(grep -o '"version": *"[^"]*"' "$DIR/package.json" 2>/dev/null | cut -d'"' -f4 || echo "unknown")
log "Current version: $PREV"

# Save checksums to detect changes
DAEMON_HASH=$(find "$DIR/daemon" -name "*.go" -exec md5sum {} \; 2>/dev/null | sort | md5sum | cut -d' ' -f1)
FRONTEND_HASH=$(find "$DIR/src" -type f -exec md5sum {} \; 2>/dev/null | sort | md5sum | cut -d' ' -f1)

# Download latest
log "Downloading update..."
if ! curl -fsSL "$URL" | tar xz --strip-components=1 --overwrite -C "$DIR"; then
  log "ERROR: Download failed"
  echo '{"type":"error","error":"download_failed","time":"'$(date -Iseconds)'"}' > "$RESULT_FILE"
  exit 1
fi

NEW=$(grep -o '"version": *"[^"]*"' "$DIR/package.json" 2>/dev/null | cut -d'"' -f4 || echo "unknown")
log "Downloaded version: $NEW"

# Check what changed
DAEMON_HASH_NEW=$(find "$DIR/daemon" -name "*.go" -exec md5sum {} \; 2>/dev/null | sort | md5sum | cut -d' ' -f1)
FRONTEND_HASH_NEW=$(find "$DIR/src" -type f -exec md5sum {} \; 2>/dev/null | sort | md5sum | cut -d' ' -f1)

DAEMON_CHANGED=false
FRONTEND_CHANGED=false
[ "$DAEMON_HASH" != "$DAEMON_HASH_NEW" ] && DAEMON_CHANGED=true
[ "$FRONTEND_HASH" != "$FRONTEND_HASH_NEW" ] && FRONTEND_CHANGED=true

# Rebuild Go daemon if source changed
if [ "$DAEMON_CHANGED" = true ]; then
  log "Daemon source changed — rebuilding..."

  if ! command -v go &>/dev/null; then
    log "Installing Go compiler..."
    apt-get install -y -qq golang-go 2>/dev/null || true
  fi

  if command -v go &>/dev/null; then
    cd "$DIR/daemon"
    systemctl stop nimos-daemon 2>/dev/null || true
    go mod tidy 2>/dev/null

    if go build -o "$DIR/daemon/nimos-daemon" . 2>&1 | tee -a "$LOG_FILE"; then
      chmod 755 "$DIR/daemon/nimos-daemon"
      log "nimos-daemon rebuilt successfully"
    else
      log "ERROR: Go build failed"
      echo '{"type":"error","error":"build_failed","prev":"'"$PREV"'","new":"'"$NEW"'","time":"'$(date -Iseconds)'"}' > "$RESULT_FILE"
      # Try to restart with old binary
      systemctl start nimos-daemon 2>/dev/null || true
      exit 1
    fi
    cd "$DIR"
  else
    log "WARNING: Go not available — cannot rebuild daemon"
  fi

  # Update service file if changed
  if [ -f "$DIR/scripts/nimos-daemon.service" ]; then
    cp "$DIR/scripts/nimos-daemon.service" /etc/systemd/system/nimos-daemon.service
    systemctl daemon-reload
  fi

  # Rebuild NimTorrent if source changed
  if [ -f "$DIR/torrentd/main.cpp" ] && command -v g++ &>/dev/null; then
    TORRENT_HASH=$(md5sum "$DIR/torrentd/main.cpp" 2>/dev/null | cut -d' ' -f1)
    if [ -f /usr/local/bin/nimos-torrentd ]; then
      log "Checking NimTorrent..."
      cd "$DIR/torrentd"
      if make -q 2>/dev/null; then
        log "NimTorrent up to date"
      else
        systemctl stop nimos-torrentd 2>/dev/null || true
        if make 2>&1 | tee -a "$LOG_FILE"; then
          cp nimos-torrentd /usr/local/bin/nimos-torrentd
          log "NimTorrent rebuilt"
        fi
      fi
      cd "$DIR"
    fi
  fi

  # Rebuild frontend if source also changed
  if [ "$FRONTEND_CHANGED" = true ] && command -v npm &>/dev/null; then
    log "Frontend source also changed — rebuilding..."
    cd "$DIR"
    npm install 2>&1 | tail -5 | tee -a "$LOG_FILE"
    npm run build 2>&1 | tee -a "$LOG_FILE" || log "WARNING: Frontend build failed"
  fi

  # Restart services
  log "Restarting services..."
  systemctl restart nimos-daemon
  systemctl restart nimos-torrentd 2>/dev/null || true
  sleep 3

  if systemctl is-active --quiet nimos-daemon; then
    log "OK: $PREV -> $NEW (daemon rebuilt + restarted)"
    echo '{"type":"full","prev":"'"$PREV"'","new":"'"$NEW"'","time":"'$(date -Iseconds)'"}' > "$RESULT_FILE"
  else
    log "ERROR: nimos-daemon failed to start after update"
    echo '{"type":"error","error":"start_failed","prev":"'"$PREV"'","new":"'"$NEW"'","time":"'$(date -Iseconds)'"}' > "$RESULT_FILE"
    exit 1
  fi

elif [ "$FRONTEND_CHANGED" = true ]; then
  log "Frontend source changed — rebuilding..."
  cd "$DIR"
  if command -v npm &>/dev/null; then
    npm install 2>&1 | tail -5 | tee -a "$LOG_FILE"
    if npm run build 2>&1 | tee -a "$LOG_FILE"; then
      log "Frontend rebuilt successfully"
    else
      log "ERROR: Frontend build failed"
      echo '{"type":"error","error":"frontend_build_failed","prev":"'"$PREV"'","new":"'"$NEW"'","time":"'$(date -Iseconds)'"}' > "$RESULT_FILE"
      exit 1
    fi
  else
    log "WARNING: npm not available — cannot rebuild frontend"
  fi
  log "OK: $PREV -> $NEW (reload browser)"
  echo '{"type":"frontend","prev":"'"$PREV"'","new":"'"$NEW"'","time":"'$(date -Iseconds)'"}' > "$RESULT_FILE"

else
  log "No changes detected"
  echo '{"type":"none","prev":"'"$PREV"'","new":"'"$NEW"'","time":"'$(date -Iseconds)'"}' > "$RESULT_FILE"
fi
