#!/usr/bin/env bash
# NimOS Uninstaller
# Usage: sudo /opt/nimos/scripts/uninstall.sh

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

log()  { echo -e "${GREEN}[NimOS]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARNING]${NC} $*"; }

if [[ $EUID -ne 0 ]]; then
  echo -e "${RED}Run with sudo: sudo $0${NC}"
  exit 1
fi

echo -e "${CYAN}${BOLD}☁️  NimOS Uninstaller${NC}"
echo ""

# ── Confirmation ──
echo -e "${YELLOW}This will remove NimOS from your system.${NC}"
echo ""
echo "What will be removed:"
echo "  • NimOS service and application (/opt/nimos)"
echo "  • Configuration (/etc/nimos)"
echo "  • Logs (/var/log/nimos)"
echo "  • Avahi service file"
echo ""
echo "What will NOT be removed:"
echo "  • Docker and Docker containers"
echo "  • Samba and SMB shares"
echo "  • User data (/var/lib/nimos)"
echo "  • Node.js"
echo ""

read -p "Are you sure? Type 'yes' to confirm: " CONFIRM
if [[ "$CONFIRM" != "yes" ]]; then
  echo "Cancelled."
  exit 0
fi

echo ""

# ── Stop service ──
log "Stopping NimOS..."
systemctl stop nimos 2>/dev/null || true
systemctl disable nimos 2>/dev/null || true
rm -f /etc/systemd/system/nimos.service
systemctl daemon-reload

# ── Remove app ──
log "Removing application..."
rm -rf /opt/nimos

# ── Remove config ──
log "Removing configuration..."
rm -rf /etc/nimos

# ── Remove logs ──
log "Removing logs..."
rm -rf /var/log/nimos
rm -f /etc/logrotate.d/nimos

# ── Remove avahi service ──
log "Removing Avahi service..."
rm -f /etc/avahi/services/nimos.service
systemctl restart avahi-daemon 2>/dev/null || true

# ── Ask about data ──
echo ""
read -p "Also remove user data at /var/lib/nimos? (y/N): " REMOVE_DATA
if [[ "$REMOVE_DATA" == "y" || "$REMOVE_DATA" == "Y" ]]; then
  log "Removing user data..."
  rm -rf /var/lib/nimos
  log "User data removed"
else
  log "User data preserved at /var/lib/nimos"
fi

# ── Ask about Docker ──
echo ""
read -p "Also remove Docker? (y/N): " REMOVE_DOCKER
if [[ "$REMOVE_DOCKER" == "y" || "$REMOVE_DOCKER" == "Y" ]]; then
  warn "Stopping all Docker containers..."
  docker stop $(docker ps -q) 2>/dev/null || true
  apt-get purge -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin 2>/dev/null || true
  rm -rf /var/lib/docker
  log "Docker removed"
else
  log "Docker preserved"
fi

# ── Done ──
echo ""
echo -e "${GREEN}${BOLD}✔ NimOS has been uninstalled${NC}"
echo ""
echo "To reinstall:"
echo "  curl -fsSL https://get.nimos.dev/install | sudo bash"
echo ""
