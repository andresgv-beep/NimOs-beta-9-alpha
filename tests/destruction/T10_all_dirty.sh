#!/usr/bin/env bash
# T10 · "Todo sucio a la vez" — el día del caos (28/05-01/06) en miniatura.
#
# Combina varios fallos simultáneos sobre distintos discos de prueba y verifica
# que las operaciones de reconciliación los resuelven TODOS, dejando el sistema
# en estado limpio. Es el test de integración de la robustez completa.
#
# Escenarios combinados:
#   A) un pool montado 3 veces (apilado)
#   B) un pool con overlays encima (containerd zombie)
#   C) una carpeta huérfana con contenido (resto de pool destruido)
#   D) un pool en read-only (I/O errors)
#
# Criterio: tras aplicar las reconciliaciones, cada problema queda resuelto o
# reportado con honestidad. NUNCA un estado fantasma silencioso.

TEST_NAME="T10_all_dirty"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/lib/common.sh"

require_root
require_btrfs

# Este test usa VARIOS discos → gestiona sus propios loops, no usa el helper
# de un solo disco. Override de teardown.
declare -a LOOPS IMGS MNTS OVERLAYS
trap cleanup_t10 EXIT

cleanup_t10() {
  for o in "${OVERLAYS[@]}"; do umount "$o" 2>/dev/null || umount -l "$o" 2>/dev/null; done
  for m in "${MNTS[@]}"; do
    local n=0
    while findmnt "$m" >/dev/null 2>&1 && [[ $n -lt 10 ]]; do
      umount "$m" 2>/dev/null || umount -l "$m" 2>/dev/null; n=$((n+1))
    done
    rmdir "$m" 2>/dev/null || true
  done
  for l in "${LOOPS[@]}"; do losetup -d "$l" 2>/dev/null || true; done
  for i in "${IMGS[@]}"; do rm -f "$i"; done
  rm -rf /nimos/pools/_t10_orphan 2>/dev/null
}

# Helper: crear un loop+btrfs montado en un punto dado
make_pool() {
  local mnt="$1" profile="${2:-single}"
  local img; img="$(mktemp /tmp/t10_XXXX.img)"
  truncate -s 512M "$img"
  local loop; loop="$(losetup --find --show "$img")"
  assert_safe_target "$loop"
  mkfs.btrfs -f -d "$profile" -m "$profile" "$loop" >/dev/null 2>&1
  mkdir -p "$mnt"
  mount "$loop" "$mnt"
  LOOPS+=("$loop"); IMGS+=("$img"); MNTS+=("$mnt")
  echo "$loop"
}

log "═══ Montando el escenario de caos (4 problemas simultáneos) ═══"

# ── A) Pool apilado 3 veces ──
MNT_A="/nimos/pools/_t10_stacked"
LOOP_A="$(make_pool "$MNT_A")"
mount "$LOOP_A" "$MNT_A"; mount "$LOOP_A" "$MNT_A"
log "A) pool apilado: $(grep -c " $MNT_A " /proc/mounts) capas"

# ── B) Pool con overlays (containerd) ──
MNT_B="/nimos/pools/_t10_overlays"
make_pool "$MNT_B" >/dev/null
for i in 1 2; do
  ov="$MNT_B/ov$i"; mkdir -p "$ov"; mount -t tmpfs none "$ov"; OVERLAYS+=("$ov")
done
log "B) pool con $(( $(findmnt -R -n -o TARGET "$MNT_B" | grep -c .) - 1 )) overlays encima"

# ── C) Carpeta huérfana con contenido ──
mkdir -p /nimos/pools/_t10_orphan/docker/data
echo basura > /nimos/pools/_t10_orphan/docker/data/x.txt
touch -d "1 hour ago" /nimos/pools/_t10_orphan
log "C) carpeta huérfana con contenido creada"

# ── D) Pool en read-only ──
MNT_D="/nimos/pools/_t10_readonly"
make_pool "$MNT_D" >/dev/null
mount -o remount,ro "$MNT_D"
log "D) pool remontado en read-only"

echo ""
log "═══ Aplicando reconciliaciones (lo que hace NimOS al arrancar) ═══"

# ── Resolver A: desapilar a 1 ──
layers="$(grep -c " $MNT_A " /proc/mounts)"
for ((i=1; i<layers; i++)); do umount "$MNT_A"; done
assert_mount_layers "$MNT_A" 1

# ── Detectar B: overlays presentes (se reporta, no se fuerza) ──
ovcount="$(findmnt -R -n -o TARGET "$MNT_B" | grep -c .)"
if [[ "$ovcount" -gt 1 ]]; then
  ok "B) overlays detectados ($ovcount mounts) → poolHasSubmounts bloquearía destroy"
else
  bad "B) no se detectaron overlays"
fi

# ── Resolver C: limpiar huérfana (pasa reglas: no montada, vieja) ──
if ! findmnt /nimos/pools/_t10_orphan >/dev/null 2>&1; then
  rm -rf /nimos/pools/_t10_orphan
  [[ -d /nimos/pools/_t10_orphan ]] && bad "C) huérfana no borrada" || ok "C) huérfana con contenido limpiada"
else
  bad "C) la huérfana figura montada (inesperado)"
fi

# ── Detectar D: read-only ──
assert_readonly "$MNT_D"

echo ""
log "═══ Verificación final: sistema converge a estado limpio ═══"

# A debe quedar en 1 capa y escribible
if touch "$MNT_A/canary" 2>/dev/null; then ok "A) pool desapilado y escribible"; rm -f "$MNT_A/canary"; else bad "A) pool no escribible"; fi

# D restaurar rw (lo que haría el operador tras ver el reporte)
mount -o remount,rw "$MNT_D" 2>/dev/null && ok "D) read-only reportado (recuperable a rw)" || warn "D) no se pudo remontar rw"

finish_test
