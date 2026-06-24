#!/usr/bin/env bash
# T06 · Pool en read-only — valida que se detecta el estado ro.
#
# BTRFS se remonta en `ro` tras errores de I/O (lo que pasó en producción).
# El fix R2 (poolMountIsReadOnly) detecta ese estado y lo reporta como
# crítico con causa clara, en vez de dejar que el usuario choque con EIO.
#
# Aquí remontamos el pool de prueba en ro y verificamos que la detección
# (findmnt OPTIONS busca "ro") lo pilla.

TEST_NAME="T06_readonly"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/lib/common.sh"

require_root
require_btrfs
trap teardown_disk EXIT

setup_loop_disk 512
make_btrfs single

log "Estado inicial: el pool debe ser rw"
if findmnt -no OPTIONS "$TEST_MNT" | tr ',' '\n' | grep -qx "ro"; then
  bad "el pool arranca en ro (inesperado)"
else
  ok "pool en rw inicialmente"
fi

log "Remontando en read-only (simula la auto-protección de BTRFS tras I/O errors)"
mount -o remount,ro "$TEST_MNT"

log "Verificando que la detección (poolMountIsReadOnly) lo pilla"
assert_readonly "$TEST_MNT"

log "Confirmando que una escritura falla (el síntoma del EIO en la UI)"
if touch "$TEST_MNT/canary" 2>/dev/null; then
  bad "se pudo escribir en un pool ro (inesperado)"
  rm -f "$TEST_MNT/canary" 2>/dev/null
else
  ok "escritura rechazada en ro (el health debe reportarlo como crítico)"
fi

# Restaurar rw para teardown limpio
mount -o remount,rw "$TEST_MNT" 2>/dev/null || true

finish_test
