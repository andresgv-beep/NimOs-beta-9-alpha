#!/usr/bin/env bash
# T03 · Pool "robado" por udisks2 — valida que la entrada fstab lo previene.
#
# El bug: CreatePool no persistía en /etc/fstab → al reiniciar, NimOS no
# remontaba el pool en /nimos/pools/ y udisks2 (auto-mount del escritorio) lo
# montaba en /media/<user>/. El fix (appendFstab en CreatePool) lo previene:
# con la entrada fstab + nofail, systemd monta el pool en su sitio al arrancar,
# ANTES de que udisks2 pueda tocarlo.
#
# Este test verifica la lógica: un pool CON entrada fstab se monta en el sitio
# correcto vía `mount <mountpoint>`; uno SIN entrada, no (y sería vulnerable
# al robo). No invoca udisks2 real (no está en headless), valida el mecanismo.

TEST_NAME="T03_udisks_steal"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/lib/common.sh"

require_root
require_btrfs

FSTAB_BAK=""
trap cleanup EXIT

cleanup() {
  # Restaurar fstab si lo tocamos
  if [[ -n "$FSTAB_BAK" && -f "$FSTAB_BAK" ]]; then
    cp "$FSTAB_BAK" /etc/fstab
    rm -f "$FSTAB_BAK"
  fi
  teardown_disk
}

setup_loop_disk 512
make_btrfs single

uuid="$(blkid -s UUID -o value "$TEST_DISK")"
log "Pool de prueba UUID=$uuid"

# Desmontar para simular el arranque (disco presente, no montado aún)
umount "$TEST_MNT"
assert_not_mounted "$TEST_MNT"

# ── Escenario A: SIN entrada fstab (el bug) ──
log "Escenario A: sin entrada en fstab — 'mount <mp>' debe fallar"
if mount "$TEST_MNT" 2>/dev/null; then
  bad "se montó sin entrada fstab (inesperado)"
  umount "$TEST_MNT" 2>/dev/null
else
  ok "sin fstab, 'mount <mp>' falla → aquí udisks2 robaría el disco a /media/"
fi

# ── Escenario B: CON entrada fstab + nofail (el fix) ──
log "Escenario B: añadiendo entrada fstab con nofail (lo que hace appendFstab)"
FSTAB_BAK="$(mktemp)"
cp /etc/fstab "$FSTAB_BAK"
echo "UUID=$uuid $TEST_MNT btrfs defaults,nofail,noatime 0 0" >> /etc/fstab

# findmnt --verify no debe quejarse de la entrada nueva
if findmnt --verify 2>&1 | grep -qiE "$TEST_MNT.*(error|fail)"; then
  bad "findmnt --verify reporta problema con la entrada fstab"
else
  ok "findmnt --verify acepta la entrada fstab"
fi

log "Con fstab, 'mount <mp>' monta en el sitio CORRECTO (gana a udisks2)"
if mount "$TEST_MNT" 2>/dev/null; then
  ok "montado en $TEST_MNT vía fstab (udisks2 ya no puede robarlo)"
  assert_mount_layers "$TEST_MNT" 1
else
  bad "no se montó pese a la entrada fstab"
fi

finish_test
