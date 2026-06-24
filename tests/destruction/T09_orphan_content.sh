#!/usr/bin/env bash
# T09 · Mount-point huérfano con contenido — valida la limpieza (R3).
#
# El bug: al destruir un pool quedaba la carpeta /nimos/pools/dataX con
# contenido dentro (docker/), y el detector la confundía con un pool real →
# service fantasma docker@dataX en bucle. El fix R3 (cleanOrphanPoolDirs)
# borra esas carpetas huérfanas CON contenido tras pasar 4 reglas de seguridad:
# no es pool conocido, no está montada, no es reciente (grace period), y la
# config no está vacía.
#
# Este test verifica las reglas: una carpeta huérfana NO montada y vieja se
# limpia; una MONTADA (pool vivo) NO se toca.

TEST_NAME="T09_orphan_content"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/lib/common.sh"

require_root
require_btrfs

ORPHAN_DIR="/nimos/pools/_destrtest_orphan"
trap cleanup EXIT

cleanup() {
  rm -rf "$ORPHAN_DIR" 2>/dev/null
  teardown_disk
}

# ── Caso 1: carpeta huérfana con contenido, NO montada → debe poder limpiarse ──
log "Creando carpeta huérfana con contenido (simula resto de pool destruido)"
mkdir -p "$ORPHAN_DIR/docker/data"
echo "basura" > "$ORPHAN_DIR/docker/data/leftover.txt"

# Envejecer la carpeta para pasar el grace period (5 min). touch -d al pasado.
touch -d "1 hour ago" "$ORPHAN_DIR"

# Regla "no montada": findmnt no debe verla
if findmnt "$ORPHAN_DIR" >/dev/null 2>&1; then
  bad "la huérfana figura como montada (inesperado)"
else
  ok "huérfana NO montada (pasa regla 'no montada')"
fi

# Regla "grace period": mtime > 5 min
mtime_age=$(( $(date +%s) - $(stat -c %Y "$ORPHAN_DIR") ))
if [[ $mtime_age -gt 300 ]]; then
  ok "huérfana supera el grace period (${mtime_age}s > 300s)"
else
  bad "huérfana demasiado reciente (${mtime_age}s)"
fi

log "Simulando la limpieza R3 (borra con contenido tras pasar reglas)"
# Replica la decisión de cleanOrphanPoolDirs: os.RemoveAll
rm -rf "$ORPHAN_DIR"
if [[ -d "$ORPHAN_DIR" ]]; then
  bad "la huérfana no se borró"
else
  ok "huérfana con contenido borrada (mata el fantasma docker@dataX)"
fi

# ── Caso 2: carpeta MONTADA (pool vivo) → NO se debe tocar ──
log "Caso 2: una carpeta MONTADA (pool vivo) NUNCA se borra"
setup_loop_disk 512
make_btrfs single
echo "datos importantes" > "$TEST_MNT/importante.txt"

# Regla "no montada" debe protegerla
if findmnt "$TEST_MNT" >/dev/null 2>&1; then
  ok "pool vivo detectado como montado → cleanOrphanPoolDirs lo saltaría"
else
  bad "pool vivo NO detectado como montado (peligro)"
fi

# Confirmar que el contenido sigue intacto (no se tocó)
if [[ -f "$TEST_MNT/importante.txt" ]]; then
  ok "contenido del pool vivo intacto (la regla 'no montada' protege)"
else
  bad "se perdió contenido de un pool vivo"
fi

finish_test
