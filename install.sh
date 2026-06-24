#!/usr/bin/env bash
# ╔══════════════════════════════════════════════════════════════╗
# ║  NimOS Beta 9 Installer                                  ║
# ║  Transforms Ubuntu/Debian Server into a NimOS NAS          ║
# ║  curl -fsSL https://raw.githubusercontent.com/               ║
# ║    andresgv-beep/NimOs-beta-9/main/install.sh | sudo bash║
# ╚══════════════════════════════════════════════════════════════╝

set -euo pipefail

# ── Config ──
NIMOS_VERSION="9.0-alpha"
NIMOS_REPO="https://github.com/andresgv-beep/NimOs-beta-9"
NIMOS_BRANCH="main"
INSTALL_DIR="/opt/nimos"
DATA_DIR="/var/lib/nimos"
CONFIG_DIR="/etc/nimos"
LOG_DIR="/var/log/nimos"
NIMOS_USER="nimos"
NIMOS_PORT="${NIMOS_PORT:-5000}"

# ── Colors ──
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

log()   { echo -e "${GREEN}[NimOS]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARNING]${NC} $*"; }
err()   { echo -e "${RED}[ERROR]${NC} $*" >&2; }
step()  { echo -e "\n${CYAN}${BOLD}━━━ $* ━━━${NC}"; }
ok()    { echo -e "  ${GREEN}✔${NC} $*"; }

# ── Pre-flight checks ──
preflight() {
  step "Pre-flight checks"

  # Must be root
  if [[ $EUID -ne 0 ]]; then
    err "This installer must be run as root (use sudo)"
    exit 1
  fi

  # Check OS
  if [[ ! -f /etc/os-release ]]; then
    err "Cannot detect OS. NimOS requires Ubuntu 22.04+ or Debian 12+"
    exit 1
  fi
  source /etc/os-release
  if [[ "$ID" != "ubuntu" && "$ID" != "debian" ]]; then
    warn "Detected $PRETTY_NAME — NimOS is tested on Ubuntu/Debian. Proceeding anyway..."
  fi
  ok "OS: $PRETTY_NAME"

  # Check architecture
  ARCH=$(uname -m)
  if [[ "$ARCH" != "x86_64" && "$ARCH" != "aarch64" && "$ARCH" != "armv7l" ]]; then
    err "Unsupported architecture: $ARCH (need x86_64, aarch64, or armv7l)"
    exit 1
  fi
  ok "Architecture: $ARCH"

  # Check memory
  MEM_KB=$(grep MemTotal /proc/meminfo | awk '{print $2}')
  MEM_MB=$((MEM_KB / 1024))
  MEM_GB=$((MEM_MB / 1024))
  if [[ $MEM_MB -lt 512 ]]; then
    warn "Only ${MEM_MB}MB RAM detected. NimOS recommends at least 1GB."
  fi
  ok "Memory: ${MEM_MB}MB (${MEM_GB}GB)"

  # Storage backend: BTRFS only since Beta 8 (ZFS removed)
  # Btrfs works everywhere (x86, ARM, any RAM)
  INSTALL_BTRFS=true
  ok "Storage backend: BTRFS"

  # Check disk space (need at least 2GB free)
  FREE_KB=$(df / | tail -1 | awk '{print $4}')
  FREE_MB=$((FREE_KB / 1024))
  if [[ $FREE_MB -lt 2048 ]]; then
    err "Need at least 2GB free disk space. Only ${FREE_MB}MB available on /"
    exit 1
  fi
  ok "Disk space: ${FREE_MB}MB free"

  # Check internet
  if ! ping -c1 -W3 1.1.1.1 &>/dev/null && ! ping -c1 -W3 8.8.8.8 &>/dev/null; then
    err "No internet connection detected"
    exit 1
  fi
  ok "Internet: connected"
}

# ── Install system dependencies ──
install_deps() {
  step "Installing system dependencies"

  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq

  # Core packages
  log "Installing core packages..."
  apt-get install -y -qq \
    curl wget git ca-certificates gnupg lsb-release \
    smartmontools hdparm lm-sensors \
    gdisk parted \
    samba \
    vsftpd \
    ufw \
    avahi-daemon \
    acl

  ok "Core packages installed"

  # ── Btrfs — always install ──
  log "Installing Btrfs..."
  if apt-get install -y -qq btrfs-progs 2>/dev/null; then
    if command -v mkfs.btrfs &>/dev/null; then
      ok "Btrfs installed and available"
    else
      warn "btrfs-progs installed but mkfs.btrfs not found"
    fi
  else
    warn "btrfs-progs not available — trying alternative..."
    apt-get install -y -qq btrfs-tools 2>/dev/null || warn "Btrfs tools not available"
  fi

  # ── ZFS removed in Beta 8 · BTRFS-only ──
  # (block intentionally left as a marker; daemon no longer supports ZFS)

  # Save storage backend capability for the daemon
  STORAGE_BACKEND="btrfs"

  mkdir -p "$DATA_DIR/config"
  echo "{\"storageBackend\":\"$STORAGE_BACKEND\",\"arch\":\"$ARCH\",\"ramGB\":$MEM_GB}" > "$DATA_DIR/config/system-caps.json"
  ok "System capabilities saved (backend: $STORAGE_BACKEND)"

  # Optional packages (nice to have, don't fail)
  log "Installing optional packages..."
  apt-get install -y -qq nfs-kernel-server 2>/dev/null || warn "nfs-kernel-server not available"
  apt-get install -y -qq ntfs-3g 2>/dev/null || warn "ntfs-3g not available"
  apt-get install -y -qq exfat-fuse 2>/dev/null || warn "exfat-fuse not available"
  apt-get install -y -qq exfat-utils 2>/dev/null || apt-get install -y -qq exfatprogs 2>/dev/null || warn "exfat utils not available"
  apt-get install -y -qq qrencode 2>/dev/null || warn "qrencode not available (2FA QR codes)"

  # Torrent engine dependencies
  log "Installing torrent engine dependencies..."
  apt-get install -y -qq libtorrent-rasterbar-dev libboost-system-dev g++ make 2>/dev/null || warn "libtorrent not available — NimTorrent will be disabled"

  # Verify critical tools
  local missing=""
  command -v smbd &>/dev/null || missing="$missing samba"
  command -v mkfs.btrfs &>/dev/null || missing="$missing btrfs-progs"
  command -v smartctl &>/dev/null || missing="$missing smartmontools"

  if [[ -n "$missing" ]]; then
    err "Failed to install critical packages:$missing"
    err "Try: apt-get install -y$missing"
    exit 1
  fi

  ok "All critical packages verified"
}

# ── Docker ──
install_docker() {
  step "Docker"
  ok "Docker available in App Store — install after creating a storage pool"
}

# ── Create NimOS user and directories ──
setup_user() {
  step "Setting up NimOS user and directories"

  # Create system user
  if ! id "$NIMOS_USER" &>/dev/null; then
    useradd -r -s /bin/bash -m -d /home/$NIMOS_USER $NIMOS_USER
    ok "User '$NIMOS_USER' created"
  else
    ok "User '$NIMOS_USER' already exists"
  fi

  # Add to required groups
  usermod -aG sudo $NIMOS_USER 2>/dev/null || true

  # Create directories
  mkdir -p "$INSTALL_DIR"
  mkdir -p "$DATA_DIR"/{apps,shares,backups,thumbnails,config,userdata,volumes}
  mkdir -p "$CONFIG_DIR"
  mkdir -p "$LOG_DIR"
  mkdir -p /nimos/pools

  ok "Directories created"
}

# ── Install NimOS application ──
install_nimos() {
  step "Installing NimOS application"

  # Download via tarball (no git auth needed)
  TARBALL_URL="https://github.com/andresgv-beep/NimOs-beta-9/archive/refs/heads/${NIMOS_BRANCH}.tar.gz"
  
  if [[ -d "$INSTALL_DIR/daemon" ]]; then
    log "Updating existing installation..."
    curl -fsSL "$TARBALL_URL" | tar xz --strip-components=1 --overwrite -C "$INSTALL_DIR"
  else
    log "Downloading NimOS..."
    mkdir -p "$INSTALL_DIR"
    curl -fsSL "$TARBALL_URL" | tar xz --strip-components=1 --overwrite -C "$INSTALL_DIR"
  fi

  cd "$INSTALL_DIR"

  # Set permissions
  chown -R $NIMOS_USER:$NIMOS_USER "$INSTALL_DIR"
  chown -R $NIMOS_USER:$NIMOS_USER "$DATA_DIR"
  chown -R $NIMOS_USER:$NIMOS_USER "$CONFIG_DIR"
  chown -R $NIMOS_USER:$NIMOS_USER "$LOG_DIR"

  # ── Build NimTorrent daemon ──
  if command -v g++ &>/dev/null && command -v make &>/dev/null && dpkg -l libtorrent-rasterbar-dev &>/dev/null 2>&1; then
    log "Building NimTorrent daemon..."
    
    systemctl stop nimos-torrentd 2>/dev/null || true
    
    cd "$INSTALL_DIR/torrentd"

    if [[ ! -f httplib.h ]]; then
      curl -fsSLO https://raw.githubusercontent.com/yhirose/cpp-httplib/master/httplib.h 2>/dev/null || true
    fi

    if [[ -f httplib.h ]]; then
      make clean >/dev/null 2>&1 || true
      if make > /tmp/nimtorrent-build.log 2>&1; then
        cp nimos-torrentd /usr/local/bin/nimos-torrentd
        chmod 755 /usr/local/bin/nimos-torrentd
        mkdir -p /var/lib/nimos/torrentd/state /run/nimos /data/torrents

        if [[ ! -f /etc/nimos/torrent.conf ]]; then
          mkdir -p /etc/nimos
          cp torrent.conf /etc/nimos/torrent.conf
        fi

        cp nimos-torrentd.service /etc/systemd/system/
        systemctl daemon-reload
        systemctl enable nimos-torrentd 2>/dev/null || true
        chown -R $NIMOS_USER:$NIMOS_USER /var/lib/nimos /data/torrents 2>/dev/null || true

        ok "NimTorrent daemon built and installed"
      else
        warn "NimTorrent build failed — torrent features disabled"
        warn "Build error (last 25 lines) — full log at /tmp/nimtorrent-build.log:"
        tail -n 25 /tmp/nimtorrent-build.log 2>/dev/null | sed 's/^/      /' >&2
      fi
    else
      warn "httplib.h download failed — NimTorrent disabled"
    fi
    cd "$INSTALL_DIR"
  else
    warn "libtorrent not available — NimTorrent disabled"
  fi

  # Migrate from old homedir-based config
  for OLD_DIR in /root/.nimos /home/*/.nimos; do
    if [ -d "$OLD_DIR/config" ] && [ ! -f "$DATA_DIR/config/users.json" ]; then
      log "Migrating config from $OLD_DIR to $DATA_DIR..."
      cp -n "$OLD_DIR/config/"*.json "$DATA_DIR/config/" 2>/dev/null || true
      [ -d "$OLD_DIR/userdata" ] && cp -rn "$OLD_DIR/userdata/"* "$DATA_DIR/userdata/" 2>/dev/null || true
      [ -d "$OLD_DIR/volumes" ] && cp -rn "$OLD_DIR/volumes/"* "$DATA_DIR/volumes/" 2>/dev/null || true
      chown -R $NIMOS_USER:$NIMOS_USER "$DATA_DIR"
      ok "Migrated data from $OLD_DIR"
    fi
  done

  ok "NimOS installed to $INSTALL_DIR"
}

# ── Write NimOS config ──
write_config() {
  step "Writing configuration"

  cat > "$CONFIG_DIR/nimos.env" << EOF
# NimOS Configuration
# Generated by installer on $(date -Iseconds)

# Server
NIMOS_PORT=$NIMOS_PORT
NIMOS_HOST=0.0.0.0
NIMOS_DATA_DIR=$DATA_DIR
NIMOS_LOG_DIR=$LOG_DIR

# Security (change these!)
# NIMOS_HTTPS=true
# NIMOS_CERT=/etc/nimos/cert.pem
# NIMOS_KEY=/etc/nimos/key.pem

# Features
NIMOS_DOCKER=true
NIMOS_SAMBA=true
NIMOS_UPNP=true
EOF

  chmod 600 "$CONFIG_DIR/nimos.env"
  chown $NIMOS_USER:$NIMOS_USER "$CONFIG_DIR/nimos.env"

  ok "Config written to $CONFIG_DIR/nimos.env"
}

# ── Create systemd service ──
install_service() {
  step "Creating systemd service"

  # Log rotation
  cat > /etc/logrotate.d/nimos << EOF
$LOG_DIR/*.log {
    daily
    missingok
    rotate 14
    compress
    delaycompress
    notifempty
    copytruncate
}
EOF

  systemctl daemon-reload

  # ── Build and install nimos-daemon (Go binary) ──
  if [ -d "$INSTALL_DIR/daemon" ] && [ -f "$INSTALL_DIR/daemon/main.go" ]; then
    log "Building nimos-daemon (Go)..."

    if ! command -v go &>/dev/null; then
      log "Installing Go compiler..."
      apt-get install -y -qq golang-go 2>/dev/null || warn "Failed to install Go — daemon will not be built"
    fi

    if command -v go &>/dev/null; then
      cd "$INSTALL_DIR/daemon"
      systemctl stop nimos-daemon 2>/dev/null || true
      go mod tidy 2>&1 | tail -3
      if go build -o "$INSTALL_DIR/daemon/nimos-daemon" . 2>&1 | tail -10; then
        if [ -f "$INSTALL_DIR/daemon/nimos-daemon" ]; then
          chmod 755 "$INSTALL_DIR/daemon/nimos-daemon"
          ok "nimos-daemon built ($(du -h $INSTALL_DIR/daemon/nimos-daemon | cut -f1))"
        else
          err "Go build finished but binary not found"
        fi
      else
        err "nimos-daemon build failed — see errors above"
      fi
      cd "$INSTALL_DIR"
    else
      err "Go compiler unavailable — daemon cannot be built"
    fi
  else
    warn "daemon/ folder not found in repo — skipping Go build"
  fi

  # ── Install nimos-daemon service ──
  if [ -f "$INSTALL_DIR/scripts/nimos-daemon.service" ]; then
    cp "$INSTALL_DIR/scripts/nimos-daemon.service" /etc/systemd/system/nimos-daemon.service
    systemctl daemon-reload
    systemctl enable nimos-daemon
    ok "nimos-daemon service installed"
  fi

  # ── Remove legacy Node.js service if present ──
  if systemctl is-enabled nimos 2>/dev/null; then
    systemctl stop nimos 2>/dev/null || true
    systemctl disable nimos 2>/dev/null || true
    rm -f /etc/systemd/system/nimos.service
    systemctl daemon-reload
    ok "Legacy Node.js service removed"
  fi

  ok "Services created and enabled"
}

# ── Configure firewall ──
setup_firewall() {
  step "Configuring firewall (ufw)"

  ufw default deny incoming 2>/dev/null || true
  ufw default allow outgoing 2>/dev/null || true

  # ── Defaults mínimos (B1) ──
  # Solo lo imprescindible para administrar NimOS. Los puertos de servicios
  # (SMB, NFS, FTP, WebDAV, Torrent) NO se abren de fábrica: NimOS los abre
  # cuando el usuario activa el servicio desde la UI, y los cierra al
  # desactivarlo. Así la superficie de ataque sigue al uso real.
  ufw allow 22/tcp comment 'SSH' 2>/dev/null || true
  ufw allow "$NIMOS_PORT"/tcp comment 'NimOS Web UI' 2>/dev/null || true
  ufw allow 5353/udp comment 'Avahi (mDNS)' 2>/dev/null || true

  echo "y" | ufw enable 2>/dev/null || true

  ok "Firewall configured (mínimo: SSH + Web UI + mDNS; los servicios abren su puerto al activarse)"
}

# ── Configure Samba ──
setup_samba() {
  step "Configuring Samba"

  [[ -f /etc/samba/smb.conf ]] && cp /etc/samba/smb.conf /etc/samba/smb.conf.bak

  cat > /etc/samba/smb.conf << 'EOF'
[global]
   workgroup = WORKGROUP
   server string = NimOS NAS
   server role = standalone server
   log file = /var/log/samba/log.%m
   max log size = 1000
   logging = file
   panic action = /usr/share/samba/panic-action %d
   obey pam restrictions = yes
   unix password sync = yes
   map to guest = bad user
   usershare allow guests = no
   min protocol = SMB2
   max protocol = SMB3

# Shares are managed by NimOS web interface
EOF

  systemctl disable smbd nmbd 2>/dev/null || true
  systemctl stop smbd nmbd 2>/dev/null || true

  ok "Samba configured"
}

# ── Configure vsftpd ──
setup_ftp() {
  step "Configuring FTP (vsftpd)"

  [[ -f /etc/vsftpd.conf ]] && cp /etc/vsftpd.conf /etc/vsftpd.conf.bak

  cat > /etc/vsftpd.conf << 'EOF'
# NimOS FTP Configuration
listen=YES
listen_ipv6=NO
anonymous_enable=NO
local_enable=YES
write_enable=YES
local_umask=022
dirmessage_enable=YES
use_localtime=YES
xferlog_enable=YES
connect_from_port_20=YES
chroot_local_user=YES
allow_writeable_chroot=YES
secure_chroot_dir=/var/run/vsftpd/empty
pam_service_name=vsftpd
# Passive mode
pasv_enable=YES
pasv_min_port=55000
pasv_max_port=55999
# Security — enable SSL to protect credentials on the wire
ssl_enable=YES
rsa_cert_file=/etc/ssl/certs/ssl-cert-snakeoil.pem
rsa_private_key_file=/etc/ssl/private/ssl-cert-snakeoil.key
allow_anon_ssl=NO
force_local_data_ssl=YES
force_local_logins_ssl=YES
ssl_tlsv1=NO
ssl_sslv2=NO
ssl_sslv3=NO
ssl_tlsv1_1=NO
ssl_tlsv1_2=YES
EOF

  # Ensure self-signed cert exists (Ubuntu ships ssl-cert package)
  if [ ! -f /etc/ssl/certs/ssl-cert-snakeoil.pem ]; then
    apt-get install -y -qq ssl-cert 2>/dev/null || true
  fi

  systemctl disable vsftpd 2>/dev/null || true
  systemctl stop vsftpd 2>/dev/null || true

  ok "FTP configured but DISABLED (opt-in: actívalo desde la UI; NimOS abrirá el puerto 21 + pasivo 55000-55999 al hacerlo)"
}

# ── Configure Nginx ──
setup_caddy() {
  step "Configuring Caddy (Reverse Proxy + HTTPS)"

  # nginx/apache fuera: Caddy es el único reverse proxy (puertos 80/443).
  systemctl stop apache2 2>/dev/null || true
  systemctl disable apache2 2>/dev/null || true
  systemctl stop nginx 2>/dev/null || true
  systemctl disable nginx 2>/dev/null || true

  # ── Instalar Caddy desde el repo oficial (si no está) ──
  if ! command -v caddy &>/dev/null; then
    log "Installing Caddy from official repo..."
    apt-get install -y -qq debian-keyring debian-archive-keyring apt-transport-https curl
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
      | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg 2>/dev/null
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
      > /etc/apt/sources.list.d/caddy-stable.list 2>/dev/null
    apt-get update -qq
    apt-get install -y -qq caddy
    ok "Caddy installed"
  else
    ok "Caddy already present"
  fi

  # ── Binario custom con el plugin DNS de DuckDNS ──
  # El Caddy de apt NO trae dns.providers.duckdns, necesario para que NimOS
  # obtenga certs por DNS-01 (sin abrir puertos en el router). Descargamos
  # un build oficial con el plugin desde caddyserver.com (compila bajo
  # demanda para cada arquitectura: x64, arm64, armv7...) y lo instalamos
  # con dpkg-divert para que los upgrades de apt no lo pisen (apt
  # actualizará caddy.default, no nuestro binario).
  if ! caddy list-modules 2>/dev/null | grep -q "dns.providers.duckdns"; then
    log "Installing custom Caddy build (DuckDNS DNS-01 plugin)..."
    case "$(uname -m)" in
      x86_64)  CADDY_ARCH="amd64" ;;
      aarch64) CADDY_ARCH="arm64" ;;
      armv7l)  CADDY_ARCH="arm&arm=7" ;;
      armv6l)  CADDY_ARCH="arm&arm=6" ;;
      *)       CADDY_ARCH="" ;;
    esac
    if [ -n "$CADDY_ARCH" ] && curl -fsSL -o /tmp/caddy-custom \
        "https://caddyserver.com/api/download?os=linux&arch=${CADDY_ARCH}&p=github.com%2Fcaddy-dns%2Fduckdns"; then
      chmod +x /tmp/caddy-custom
      if /tmp/caddy-custom list-modules 2>/dev/null | grep -q "dns.providers.duckdns"; then
        systemctl stop caddy 2>/dev/null || true
        dpkg-divert --divert /usr/bin/caddy.default --rename /usr/bin/caddy 2>/dev/null || true
        cp /tmp/caddy-custom /usr/bin/caddy
        chmod +x /usr/bin/caddy
        ok "Custom Caddy installed (duckdns plugin · arch $(uname -m))"
      else
        warn "Downloaded binary lacks duckdns module — keeping stock Caddy (DNS-01 certs disabled)"
      fi
    else
      warn "Could not download custom Caddy for arch '$(uname -m)' — keeping stock Caddy (DNS-01 certs disabled)"
    fi
  else
    ok "Caddy already has duckdns plugin"
  fi

  # ── Config base de Caddy (JSON nativo, NO Caddyfile) ──
  # MODELO 1: el panel de NimOS vive aquí, en el config base. El daemon NO
  # lo gestiona — solo añade/quita rutas de APPS bajo el grupo @id
  # "nimos_apps". Así el panel sigue accesible aunque el daemon falle.
  #
  # El server "nimos" tiene dos rutas, en orden:
  #   1. Subroute @id "nimos_apps" (vacío al inicio) → lo llena el daemon
  #      con las apps expuestas. Va PRIMERO para que un subdominio de app
  #      tenga prioridad sobre el catch-all del panel.
  #   2. Catch-all → reverse proxy al panel NimOS (:NIMOS_PORT).
  #
  # Caddy gestiona los certs ACME automáticamente para cualquier host que
  # aparezca en un match (incluidas las apps que añada el daemon).
  mkdir -p /etc/caddy
  cat > /etc/caddy/caddy.json << EOF
{
  "admin": {
    "listen": "127.0.0.1:2019"
  },
  "apps": {
    "tls": {
      "certificates": {
        "automate": []
      },
      "automation": {
        "policies": [
          {
            "@id": "nimos_tls",
            "subjects": []
          }
        ]
      }
    },
    "http": {
      "http_port": ${NIMOS_HTTP_PORT:-80},
      "https_port": ${NIMOS_HTTPS_PORT:-443},
      "servers": {
        "nimos": {
          "listen": [":${NIMOS_HTTP_PORT:-80}", ":${NIMOS_HTTPS_PORT:-443}"],
          "tls_connection_policies": [{}],
          "routes": [
            {
              "@id": "nimos_apps",
              "handle": [
                {
                  "handler": "subroute",
                  "routes": []
                }
              ]
            },
            {
              "handle": [
                {
                  "handler": "headers",
                  "response": {
                    "set": {
                      "Referrer-Policy": ["strict-origin-when-cross-origin"],
                      "Permissions-Policy": ["camera=(), microphone=(), geolocation=(), payment=()"]
                    }
                  }
                },
                {
                  "handler": "reverse_proxy",
                  "upstreams": [{ "dial": "127.0.0.1:$NIMOS_PORT" }],
                  "flush_interval": -1
                }
              ]
            }
          ]
        }
      }
    }
  }
}
EOF

  # ── Servicio systemd usando el JSON nativo ──
  # Sobrescribimos el ExecStart por defecto de Caddy (que usa Caddyfile) para
  # que cargue SIEMPRE nuestro config JSON al arrancar.
  #
  # IMPORTANTE: NO usamos --resume. Con --resume, Caddy reanudaría el último
  # estado autoguardado en lugar de cargar nuestro --config, lo que provoca
  # que cargue el Caddyfile por defecto (srv0) en vez del nuestro. Sin
  # --resume, Caddy carga nuestro JSON base cada arranque, de forma
  # determinista. Las rutas de apps que inyecta el daemon NO se pierden de
  # forma permanente: el reconciler las re-sincroniza cada 30s, así que si
  # Caddy reinicia, las apps vuelven solas (modelo declarativo autorreparable).
  mkdir -p /etc/systemd/system/caddy.service.d
  cat > /etc/systemd/system/caddy.service.d/nimos.conf << EOF
[Service]
ExecStart=
ExecStart=/usr/bin/caddy run --environ --config /etc/caddy/caddy.json
ExecReload=
ExecReload=/usr/bin/caddy reload --config /etc/caddy/caddy.json --force
EOF

  # Borrar cualquier autosave previo del Caddyfile por defecto, para que no
  # interfiera (Caddy lo crea al arrancar la primera vez tras apt install).
  rm -f /var/lib/caddy/.config/caddy/autosave.json 2>/dev/null || true
  rm -f /root/.config/caddy/autosave.json 2>/dev/null || true

  systemctl daemon-reload
  systemctl enable caddy 2>/dev/null || true
  systemctl restart caddy

  ok "Caddy configured (panel served + apps group ready · HTTPS automatic)"
}

# ── Configure Avahi ──
setup_avahi() {
  step "Configuring mDNS (Avahi)"

  HOSTNAME=$(hostname)

  cat > /etc/avahi/services/nimos.service << EOF
<?xml version="1.0" standalone='no'?>
<!DOCTYPE service-group SYSTEM "avahi-service.dtd">
<service-group>
  <name replace-wildcards="yes">NimOS on %h</name>
  <service>
    <type>_http._tcp</type>
    <port>$NIMOS_PORT</port>
  </service>
  <service>
    <type>_smb._tcp</type>
    <port>445</port>
  </service>
</service-group>
EOF

  systemctl restart avahi-daemon 2>/dev/null || true
  ok "mDNS: ${HOSTNAME}.local"
}

# ── Start NimOS ──
start_nimos() {
  step "Starting NimOS"

  # Build frontend
  if [ -f "$INSTALL_DIR/package.json" ]; then
    cd "$INSTALL_DIR"

    # Asegurar Node.js 20.x (SvelteKit 2 requires Node 193+; 20 LTS recomendado)
    NODE_OK=false
    if command -v node &>/dev/null; then
      NODE_VER=$(node -v 2>/dev/null | sed 's/^v//' | cut -d. -f1)
      if [[ -n "$NODE_VER" && "$NODE_VER" -ge 18 ]]; then
        NODE_OK=true
        ok "Node.js $(node -v) already installed"
      else
        warn "Node.js $(node -v) too old — installing Node 20 LTS"
      fi
    fi

    if [[ "$NODE_OK" != true ]]; then
      log "Installing Node.js 20 LTS from NodeSource..."
      curl -fsSL https://deb.nodesource.com/setup_20.x | bash - 2>&1 | grep -v "^$" || true
      apt-get install -y -qq nodejs
      if command -v node &>/dev/null; then
        ok "Node.js $(node -v) installed"
      else
        err "Node.js installation failed — frontend cannot be built"
        return 1
      fi
    fi

    log "Installing frontend dependencies (npm install, ~1-2 min)..."
    if npm install --no-audit --no-fund --loglevel=error; then
      ok "Frontend dependencies installed"
    else
      err "npm install failed — check $INSTALL_DIR for errors"
      return 1
    fi

    log "Building frontend (vite build, ~30-60s)..."
    if npm run build 2>&1 | tail -5; then
      if [ -d "$INSTALL_DIR/dist" ] && [ -f "$INSTALL_DIR/dist/index.html" ]; then
        ok "Frontend built successfully ($(du -sh $INSTALL_DIR/dist | cut -f1))"
      else
        err "Build completed but dist/index.html not found"
        return 1
      fi
    else
      err "Frontend build failed"
      return 1
    fi

    # El user nimos debe ser dueño del dist/ (lo sirve el daemon)
    chown -R $NIMOS_USER:$NIMOS_USER "$INSTALL_DIR/dist"
  else
    warn "package.json not found — skipping frontend build"
  fi

  systemctl start nimos-daemon 2>/dev/null || true
  systemctl start nimos-torrentd 2>/dev/null || true

  # Wait for daemon to start
  for i in $(seq 1 15); do
    if curl -s -o /dev/null -w "%{http_code}" "http://localhost:$NIMOS_PORT" 2>/dev/null | grep -q "200\|301"; then
      ok "NimOS is running!"
      return
    fi
    sleep 1
  done

  warn "NimOS may still be starting. Check: systemctl status nimos-daemon"
}

# ── Print summary ──
print_summary() {
  LOCAL_IPS=$(hostname -I | tr ' ' '\n' | grep -E '^(192|10|172)' | head -3)
  HOSTNAME=$(hostname)

  # Detect what storage backends are available (BTRFS only since Beta 8)
  HAS_BTRFS="no"
  command -v mkfs.btrfs &>/dev/null && HAS_BTRFS="yes"

  echo ""
  echo -e "${GREEN}${BOLD}"
  echo "╔══════════════════════════════════════════════════════════════╗"
  echo "║                                                              ║"
  echo "║   ☁️  NimOS v${NIMOS_VERSION} installed successfully!       ║"
  echo "║                                                              ║"
  echo "╚══════════════════════════════════════════════════════════════╝"
  echo -e "${NC}"
  echo -e "  ${BOLD}Access NimOS:${NC}"
  for ip in $LOCAL_IPS; do
    echo -e "    ${CYAN}→ http://${ip}:${NIMOS_PORT}${NC}"
  done
  echo -e "    ${CYAN}→ http://${HOSTNAME}.local:${NIMOS_PORT}${NC}  (mDNS)"
  echo ""
  echo -e "  ${BOLD}Storage:${NC}"
  echo -e "    Btrfs:   ${HAS_BTRFS}"
  echo -e "    Docker:  Available in App Store (installs on pool)"
  echo ""
  echo -e "  ${BOLD}Services:${NC}"
  echo -e "    Go:      $(go version 2>/dev/null | cut -d' ' -f3 || echo 'not found')"
  echo -e "    Samba:   $(smbd --version 2>/dev/null || echo 'not found')"
  echo -e "    FTP:     $(vsftpd -v 2>&1 | head -1 2>/dev/null || echo 'not found')"
  echo -e "    NFS:     $(cat /proc/fs/nfsd/versions 2>/dev/null && echo 'installed' || echo 'not found')"
  echo -e "    Certbot: $(certbot --version 2>/dev/null || echo 'not found')"
  echo -e "    UFW:     $(ufw status 2>/dev/null | head -1 || echo 'not found')"
  echo ""
  echo -e "  ${BOLD}Paths:${NC}"
  echo -e "    Application: ${INSTALL_DIR}"
  echo -e "    Data:        ${DATA_DIR}"
  echo -e "    Config:      ${CONFIG_DIR}/nimos.env"
  echo -e "    Logs:        ${LOG_DIR}"
  echo ""
  echo -e "  ${YELLOW}⚠️  First time? Open the web UI to create your admin account.${NC}"
  echo ""
}

# ══════════════════════════════════════
#  Main
# ══════════════════════════════════════

main() {
  echo -e "${CYAN}${BOLD}"
  echo "   ☁️  NimOS Installer v${NIMOS_VERSION}"
  echo "   Transforming Ubuntu/Debian Server into your personal NAS"
  echo -e "${NC}"

  preflight
  install_deps
  install_docker
  setup_user
  install_nimos
  write_config
  install_service
  setup_firewall
  setup_samba
  setup_ftp
  setup_caddy
  setup_avahi
  start_nimos
  print_summary
}

main "$@"
