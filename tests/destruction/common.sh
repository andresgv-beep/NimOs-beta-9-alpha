#!/usr/bin/env bash
# common.sh — helpers compartidos por los tests de destrucción de storage.
#
# Provee:
#   - Setup/teardown de discos de prueba (loop device por defecto, USB con --usb)
#   - Guards de seguridad (NUNCA tocar boot disk ni un pool managed real)
#   - Asserts (assert_eq, assert_mount_layers, assert_readonly, ...)
#   - Logging con colores y contadores PASS/FAIL
#
# Filosofía: los discos de PRUEBA son sacrificables — se formatean a gusto.
# El único guard que queda es que el target sea de verdad un disco de prueba
# y NO /dev/sda (producción) ni el disco de arranque. Eso no limita los tests,
# solo evita el único accidente que dolería.

set -uo pipefail

# ── Colores ───────────────────────────────────────────────────────────────
if [[ -t 1 ]]; then
  C_RED=$'\e[31m'; C_GRN=$'\e[32m'; C_YEL=$'\e[33m'; C_BLU=$'\e[34m'; C_RST=$'\e[0m'
else
  C_RED=""; C_GRN=""; C_YEL=""; C_BLU=""; C_RST=""
fi

# ── Estado global del test actual ───────────────────────────────────────────
TEST_NAME="${TEST_NAME:-unknown}"
_ASSERT_FAILS=0
TEST_DISK=""          # device que usa el test (p.ej. /dev/loop0 o /dev/sdX)
TEST_IMG=""           # archivo .img si es loop
TEST_MNT="/nimos/pools/_destrtest"

log()  { echo "${C_BLU}[$TEST_NAME]${C_RST} $*"; }
ok()   { echo "${C_GRN}  ✓${C_RST} $*"; }
bad()  { echo "${C_RED}  ✗${C_RST} $*"; _ASSERT_FAILS=$((_ASSERT_FAILS+1)); }
warn() { echo "${C_YEL}  !${C_RST} $*"; }

# ── Guards de seguridad ──────────────────────────────────────────────────────
# require_root: los tests necesitan sudo (mount, mkfs, losetup).
require_root() {
  if [[ $EUID -ne 0 ]]; then
    echo "${C_RED}ERROR: los tests de destrucción requieren root (sudo).${C_RST}" >&2
    exit 1
  fi
}

# assert_safe_target <device>: aborta si el device es el boot disk o aloja un
# pool managed real. Esta es la ÚNICA atadura — protege producción, no limita
# la destrucción de discos de prueba.
assert_safe_target() {
  local dev="$1"
  if [[ -z "$dev" ]]; then
    echo "${C_RED}ERROR: device de prueba vacío.${C_RST}" >&2; exit 1
  fi

  # Resolver el disco base (sdа1 → sda, nvme0n1p2 → nvme0n1)
  local base; base="$(lsblk -no pkname "$dev" 2>/dev/null | head -1)"
  [[ -n "$base" ]] && base="/dev/$base" || base="$dev"

  # 1. ¿Es el disco de arranque? (donde está montado /)
  local rootsrc; rootsrc="$(findmnt -no SOURCE / 2>/dev/null)"
  local rootbase; rootbase="$(lsblk -no pkname "$rootsrc" 2>/dev/null | head -1)"
  [[ -n "$rootbase" ]] && rootbase="/dev/$rootbase"
  if [[ "$base" == "$rootbase" || "$dev" == "$rootsrc" ]]; then
    echo "${C_RED}ABORT: $dev es (parte de) el disco de arranque. Jamás.${C_RST}" >&2
    exit 1
  fi

  # 2. ¿Aloja un pool montado en /nimos/pools (que NO sea nuestro mnt de test)?
  while read -r src tgt; do
    [[ "$tgt" == "$TEST_MNT" ]] && continue
    if [[ "$tgt" == /nimos/pools/* && ( "$src" == "$dev" || "$src" == "$base"* ) ]]; then
      echo "${C_RED}ABORT: $dev aloja un pool en producción ($tgt). No se toca.${C_RST}" >&2
      exit 1
    fi
  done < <(findmnt -rno SOURCE,TARGET 2>/dev/null)

  # 3. Lista blanca de tipos: loop (siempre OK) o el device pasado con --usb
  if [[ "$dev" == /dev/loop* ]]; then
    return 0
  fi
  if [[ "${ALLOW_USB:-0}" == "1" ]]; then
    warn "Usando disco real $dev (modo --usb). Se formateará por completo."
    return 0
  fi

  echo "${C_RED}ABORT: $dev no es loop y no se pasó --usb. Por seguridad, no se toca.${C_RST}" >&2
  exit 1
}

# require_btrfs: salta el test (SKIP, exit 0 especial) si el kernel no soporta
# btrfs montable. Evita falsos FAIL en entornos sin el módulo (p.ej. algunos
# contenedores). En la Pi, que corre btrfs nativo, nunca salta.
require_btrfs() {
  if ! grep -qw btrfs /proc/filesystems 2>/dev/null; then
    # Intentar cargar el módulo
    modprobe btrfs 2>/dev/null || true
  fi
  if ! grep -qw btrfs /proc/filesystems 2>/dev/null; then
    echo "${C_YEL}[$TEST_NAME] SKIP — el kernel no soporta btrfs montable (entorno sin módulo)${C_RST}"
    exit 77   # código convencional de "skip"
  fi
}

# ── Setup / teardown de discos ───────────────────────────────────────────────
# setup_loop_disk <size_mb>: crea un .img y lo asocia a un loop device.
setup_loop_disk() {
  local size="${1:-512}"
  TEST_IMG="$(mktemp /tmp/destrtest_XXXX.img)"
  truncate -s "${size}M" "$TEST_IMG"
  TEST_DISK="$(losetup --find --show "$TEST_IMG")"
  log "loop device: $TEST_DISK (img: $TEST_IMG, ${size}MB)"
  assert_safe_target "$TEST_DISK"
}

# setup_usb_disk <device>: usa un disco USB real (requiere ALLOW_USB=1).
setup_usb_disk() {
  TEST_DISK="$1"
  TEST_IMG=""
  log "disco USB real: $TEST_DISK"
  assert_safe_target "$TEST_DISK"
}

# unmount_hard <mountpoint>: desmonta a conciencia — mata procesos que lo
# ocupen (como los shims que vimos en producción), reintenta, y usa lazy como
# último recurso. Deja el mountpoint libre o lo intenta con todas.
unmount_hard() {
  local mp="$1" n=0
  while findmnt "$mp" >/dev/null 2>&1 && [[ $n -lt 10 ]]; do
    # Si hay procesos con archivos abiertos ahí, matarlos (es un test, no prod)
    fuser -km "$mp" 2>/dev/null || true
    umount "$mp" 2>/dev/null || umount -l "$mp" 2>/dev/null
    sleep 0.3
    n=$((n+1))
  done
  rmdir "$mp" 2>/dev/null || true
}

# free_loop <device>: libera un loop device a conciencia (reintenta).
free_loop() {
  local dev="$1"
  [[ "$dev" == /dev/loop* ]] || return 0
  local n=0
  while losetup "$dev" >/dev/null 2>&1 && [[ $n -lt 5 ]]; do
    losetup -d "$dev" 2>/dev/null || true
    sleep 0.3
    n=$((n+1))
  done
}

# teardown_disk: desmonta todo, libera loop, borra img. Idempotente y robusto.
teardown_disk() {
  unmount_hard "$TEST_MNT"
  if [[ -n "$TEST_DISK" && "$TEST_DISK" == /dev/loop* ]]; then
    free_loop "$TEST_DISK"
  fi
  [[ -n "$TEST_IMG" && -f "$TEST_IMG" ]] && rm -f "$TEST_IMG"
  TEST_DISK=""; TEST_IMG=""
}

# cleanup_test_residue: barre TODO residuo de tests anteriores que pudiera
# haber quedado de una corrida que petó a medias. Solo toca cosas con los
# prefijos de test (_t10, _destrtest) y loops asociados a .img de test.
# NUNCA toca pools de producción (los prefijos son inequívocos).
cleanup_test_residue() {
  # 1. Desmontar cualquier mountpoint de test bajo /nimos/pools
  while read -r mp; do
    [[ -n "$mp" ]] && unmount_hard "$mp"
  done < <(findmnt -rno TARGET 2>/dev/null | grep -E "/nimos/pools/(_t10|_destrtest)")

  # 2. Liberar loops asociados a imágenes de test
  while read -r dev backing; do
    if [[ "$backing" == *destrtest_* || "$backing" == *t10_* ]]; then
      free_loop "$dev"
    fi
  done < <(losetup -l -n -O NAME,BACK-FILE 2>/dev/null)

  # 3. Borrar imágenes de test sueltas
  rm -f /tmp/destrtest_*.img /tmp/t10_*.img 2>/dev/null

  # 4. Borrar mount points de test vacíos
  rmdir /nimos/pools/_t10_* /nimos/pools/_destrtest* 2>/dev/null || true
}

# make_btrfs <profile>: formatea TEST_DISK como BTRFS y lo monta en TEST_MNT.
make_btrfs() {
  local profile="${1:-single}"
  mkfs.btrfs -f -d "$profile" -m "$profile" "$TEST_DISK" >/dev/null 2>&1
  mkdir -p "$TEST_MNT"
  mount "$TEST_DISK" "$TEST_MNT"
}

# ── Asserts ──────────────────────────────────────────────────────────────────
assert_eq() {
  local got="$1" want="$2" msg="${3:-}"
  if [[ "$got" == "$want" ]]; then ok "$msg (=$got)"; else bad "$msg — got '$got', want '$want'"; fi
}

# assert_mount_layers <mountpoint> <n>: verifica n capas montadas exactas.
assert_mount_layers() {
  local mp="$1" want="$2"
  local got; got="$(grep -c " $mp " /proc/mounts 2>/dev/null || echo 0)"
  assert_eq "$got" "$want" "capas montadas en $mp"
}

# assert_readonly <mountpoint>: pasa si está montado ro.
assert_readonly() {
  local mp="$1"
  if findmnt -no OPTIONS "$mp" 2>/dev/null | tr ',' '\n' | grep -qx "ro"; then
    ok "$mp está en read-only (esperado)"
  else
    bad "$mp NO está en read-only"
  fi
}

# assert_not_mounted <mountpoint>
assert_not_mounted() {
  local mp="$1"
  if findmnt "$mp" >/dev/null 2>&1; then bad "$mp sigue montado (no debería)"; else ok "$mp desmontado"; fi
}

# ── Resultado del test ───────────────────────────────────────────────────────
# finish_test: imprime el veredicto y sale con código apropiado.
finish_test() {
  if [[ $_ASSERT_FAILS -eq 0 ]]; then
    echo "${C_GRN}[$TEST_NAME] PASS${C_RST}"
    exit 0
  else
    echo "${C_RED}[$TEST_NAME] FAIL ($_ASSERT_FAILS asserts)${C_RST}"
    exit 1
  fi
}
