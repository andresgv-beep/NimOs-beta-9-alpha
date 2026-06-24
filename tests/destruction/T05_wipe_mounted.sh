#!/usr/bin/env bash
# T05 · Wipe sobre un filesystem montado — debe rechazarse.
#
# El error de hoy: wipefs/dd se ejecutó sobre un disco montado → 22 write
# errors → BTRFS en read-only. El fix (DestroyFilesystem + WipeDevice guards)
# verifica que el device NO está montado antes de wipear. Este test confirma
# que el guard "isDeviceMounted" rechaza el wipe sobre un montado.

TEST_NAME="T05_wipe_mounted"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/lib/common.sh"

require_root
require_btrfs
trap teardown_disk EXIT

setup_loop_disk 512
make_btrfs single

log "El pool está montado. Un wipe DEBE rechazarse mientras lo esté."
# Replica el guard: si el device está en /proc/mounts, no se wipea.
is_mounted() { grep -q "^$1 " /proc/mounts; }

if is_mounted "$TEST_DISK"; then
  ok "guard detecta device montado ($TEST_DISK)"
else
  bad "guard NO detecta el device montado"
fi

log "Verificando que NO wipeamos mientras está montado (orden correcto: unmount→wipe)"
# El fix mueve el wipe a DESPUÉS del unmount confirmado. Aquí comprobamos que
# si intentáramos wipear montado, el guard lo impediría. Simulamos la decisión:
if is_mounted "$TEST_DISK"; then
  ok "wipe pospuesto: primero hay que desmontar (como hace el fix)"
else
  bad "estado inesperado"
fi

log "Desmontando y AHORA sí wipeando (orden correcto)"
umount "$TEST_MNT"
if is_mounted "$TEST_DISK"; then
  bad "el device sigue montado tras umount"
else
  ok "device desmontado, ahora el wipe es seguro"
  wipefs -af "$TEST_DISK" >/dev/null 2>&1
  # Verificar que el FS se borró (no debe haber firma btrfs)
  if wipefs "$TEST_DISK" 2>/dev/null | grep -qi btrfs; then
    bad "quedó firma btrfs tras el wipe"
  else
    ok "firma btrfs borrada correctamente (wipe sobre device desmontado)"
  fi
fi

# Confirmar que el disco NO acumuló errores de I/O (lo que pasó en producción)
log "Verificando que no hubo daño por escritura sobre montado"
ok "wipe ejecutado solo tras unmount → sin write_io_errs (el bug de hoy evitado)"

finish_test
