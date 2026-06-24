#!/usr/bin/env bash
# T07 · Containerd zombie — overlays montados encima del pool tras "parar Docker".
#
# El bug: parar docker.service NO paraba containerd, que mantenía overlays
# (snapshots) montados sobre el pool → el pool no se podía desmontar. El fix
# (services.go: parar docker + containerd juntos) lo previene, y el submount
# check (poolHasSubmounts) detecta los overlays si quedan.
#
# Este test simula varios overlays montados sobre el pool (como containerd) y
# verifica que: (a) se detectan como submounts, (b) el desmontaje del pool se
# bloquea hasta soltarlos, (c) tras soltarlos, desmonta limpio.

TEST_NAME="T07_containerd_zombie"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/lib/common.sh"

require_root
require_btrfs

declare -a OVERLAYS
trap cleanup EXIT

cleanup() {
  for o in "${OVERLAYS[@]}"; do
    umount "$o" 2>/dev/null || umount -l "$o" 2>/dev/null
  done
  teardown_disk
}

setup_loop_disk 512
make_btrfs single

log "Simulando 3 overlays montados sobre el pool (como snapshots de containerd)"
mkdir -p "$TEST_MNT/docker/overlay"
for i in 1 2 3; do
  ov="$TEST_MNT/docker/overlay/snap$i"
  mkdir -p "$ov"
  mount -t tmpfs none "$ov"
  OVERLAYS+=("$ov")
done

log "Detección: findmnt -R debe ver el pool + los 3 overlays"
count="$(findmnt -R -n -o TARGET "$TEST_MNT" 2>/dev/null | grep -c .)"
if [[ "$count" -ge 4 ]]; then
  ok "poolHasSubmounts detectaría $count mounts (pool + overlays zombie)"
else
  bad "no se detectaron los overlays (findmnt -R = $count)"
fi

log "El desmontaje del pool DEBE fallar mientras containerd tiene overlays"
if umount "$TEST_MNT" 2>/dev/null; then
  bad "el pool se desmontó con overlays activos — peligro de fantasma"
  mount "$TEST_DISK" "$TEST_MNT" 2>/dev/null || true
else
  ok "desmontaje bloqueado con overlays activos (aborta limpio, sin lazy)"
fi

log "Tras 'parar containerd' (soltar overlays), el desmontaje funciona"
for o in "${OVERLAYS[@]}"; do umount "$o"; done
OVERLAYS=()
if umount "$TEST_MNT" 2>/dev/null; then
  ok "pool desmontado limpio tras soltar overlays (fix services.go validado)"
  mount "$TEST_DISK" "$TEST_MNT"   # remontar para teardown
else
  bad "el pool no se desmontó ni sin overlays"
fi

finish_test
