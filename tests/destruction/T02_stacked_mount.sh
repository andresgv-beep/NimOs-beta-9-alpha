#!/usr/bin/env bash
# T02 · Mounts apilados — valida que el sistema desapila a 1 capa.
#
# Simula el bug histórico: el mismo pool montado N veces sobre el mismo punto.
# Verifica que tras desapilar queda exactamente 1 capa (lo que hace
# reconcileMountState al arrancar, y lo que el fix de doble-mount previene).
#
# NOTA: este test valida el MECANISMO de desapilado a nivel sistema. No arranca
# el daemon; comprueba que la operación de desapilar (que reconcileMountState
# ejecuta) deja el estado correcto.

TEST_NAME="T02_stacked_mount"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/lib/common.sh"

require_root
require_btrfs
trap teardown_disk EXIT

setup_loop_disk 512
make_btrfs single

log "Apilando el pool 3 veces sobre $TEST_MNT (simula el bug)"
mount "$TEST_DISK" "$TEST_MNT"
mount "$TEST_DISK" "$TEST_MNT"
# (make_btrfs ya hizo el primer mount → total 3)

assert_mount_layers "$TEST_MNT" 3

log "Desapilando hasta dejar 1 (lo que hace reconcileUnstack)"
# Replica la lógica de reconcileUnstack: desmontar (capas-1) veces
layers="$(grep -c " $TEST_MNT " /proc/mounts)"
for ((i=1; i<layers; i++)); do
  umount "$TEST_MNT"
done

assert_mount_layers "$TEST_MNT" 1

log "Verificando que sigue accesible (escritura real)"
if touch "$TEST_MNT/canary" 2>/dev/null; then
  ok "el pool es escribible tras desapilar"
  rm -f "$TEST_MNT/canary"
else
  bad "el pool NO es escribible tras desapilar"
fi

finish_test
