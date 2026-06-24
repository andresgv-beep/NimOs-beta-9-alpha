#!/usr/bin/env bash
# T04 · Unmount con un filesystem montado encima (submount) — valida que el
# sistema detecta los submounts y NO desmonta a ciegas (lo que crearía un
# pool fantasma). Replica la lógica de poolHasSubmounts (findmnt -R cuenta >1).
#
# Este es el escenario exacto del bug de containerd: overlays montados encima
# del pool. El fix aborta limpio en vez de hacer umount -l lazy.

TEST_NAME="T04_busy_unmount"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/lib/common.sh"

require_root
require_btrfs
trap cleanup EXIT

cleanup() {
  umount "$TEST_MNT/sub" 2>/dev/null || umount -l "$TEST_MNT/sub" 2>/dev/null
  teardown_disk
}

setup_loop_disk 512
make_btrfs single

log "Montando un tmpfs ENCIMA del pool (simula overlay de Docker)"
mkdir -p "$TEST_MNT/sub"
mount -t tmpfs none "$TEST_MNT/sub"

log "Comprobando detección de submounts (findmnt -R debe ver >1)"
layers="$(findmnt -R -n -o TARGET "$TEST_MNT" 2>/dev/null | grep -c .)"
if [[ "$layers" -gt 1 ]]; then
  ok "poolHasSubmounts detectaría el submount (findmnt -R = $layers)"
else
  bad "no se detectó el submount (findmnt -R = $layers)"
fi

log "Verificando que un umount estricto del pool FALLA (correcto: hay algo encima)"
if umount "$TEST_MNT" 2>/dev/null; then
  bad "el umount tuvo éxito con un submount activo — peligro de fantasma"
  # si por lo que sea se desmontó, lo remontamos para el teardown
  mount "$TEST_DISK" "$TEST_MNT" 2>/dev/null || true
else
  ok "el umount estricto falló con submount activo (aborta limpio, sin lazy)"
fi

log "Tras soltar el submount, el umount SÍ debe funcionar"
umount "$TEST_MNT/sub"
if umount "$TEST_MNT" 2>/dev/null; then
  ok "umount limpio tras soltar el submount"
  mount "$TEST_DISK" "$TEST_MNT"   # remontar para teardown ordenado
else
  bad "umount falló incluso sin submounts"
fi

finish_test
