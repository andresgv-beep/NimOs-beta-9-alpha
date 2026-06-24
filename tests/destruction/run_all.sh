#!/usr/bin/env bash
# run_all.sh — runner maestro de los tests de destrucción de storage.
#
# Descubre y ejecuta los tests TNN_*.sh, cada uno aislado, y reporta una tabla
# PASS/FAIL al final.
#
# Uso:
#   sudo ./run_all.sh                    # todos los tests, sobre loop devices
#   sudo ./run_all.sh T02 T04            # solo los tests indicados
#   sudo ALLOW_USB=1 USB_DISK=/dev/sdX ./run_all.sh   # permitir disco USB real
#   sudo ./run_all.sh --clean            # barre residuos de corridas anteriores
#
# SEGURIDAD: por defecto solo usa loop devices (cero hardware real). Para usar
# un disco USB real hay que pasar ALLOW_USB=1 explícitamente; aun así, los
# guards rechazan el boot disk y cualquier pool de producción.

set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [[ $EUID -ne 0 ]]; then
  echo "Los tests de destrucción requieren root. Usa: sudo $0" >&2
  exit 1
fi

# --clean: barre cualquier residuo de tests anteriores (mounts _t10/_destrtest,
# loops e .img de test) y sale. Útil si una corrida petó a medias o tras un
# reinicio con residuos colgando. Solo toca cosas con prefijos de test.
if [[ "${1:-}" == "--clean" ]]; then
  source "$HERE/lib/common.sh"
  echo "Barriendo residuos de tests de destrucción..."
  cleanup_test_residue
  echo "Hecho. Estado actual:"
  echo "  mounts de test:  $(findmnt -rno TARGET 2>/dev/null | grep -cE '/nimos/pools/(_t10|_destrtest)') restantes"
  echo "  loops de test:   $(losetup -l -n -O BACK-FILE 2>/dev/null | grep -cE 'destrtest_|t10_') restantes"
  exit 0
fi


# Colores
if [[ -t 1 ]]; then
  C_RED=$'\e[31m'; C_GRN=$'\e[32m'; C_YEL=$'\e[33m'; C_RST=$'\e[0m'; C_BOLD=$'\e[1m'
else
  C_RED=""; C_GRN=""; C_YEL=""; C_RST=""; C_BOLD=""
fi

# Selección de tests: argumentos (T02 T04...) o todos.
declare -a TESTS
if [[ $# -gt 0 ]]; then
  for arg in "$@"; do
    match="$(find "$HERE" -maxdepth 1 -name "${arg}*.sh" | head -1)"
    [[ -n "$match" ]] && TESTS+=("$match") || echo "${C_YEL}aviso: no existe test $arg${C_RST}"
  done
else
  while IFS= read -r f; do TESTS+=("$f"); done < <(find "$HERE" -maxdepth 1 -name 'T[0-9][0-9]_*.sh' | sort)
fi

if [[ ${#TESTS[@]} -eq 0 ]]; then
  echo "No hay tests que ejecutar."; exit 1
fi

echo "${C_BOLD}═══ Tests de destrucción de storage NimOS ═══${C_RST}"
echo "Modo discos: ${ALLOW_USB:+USB real permitido (ALLOW_USB=1)}${ALLOW_USB:-loop devices}"
echo "Tests a correr: ${#TESTS[@]}"
echo ""

declare -a RESULTS
PASS=0; FAIL=0; SKIP=0

for test in "${TESTS[@]}"; do
  name="$(basename "$test" .sh)"
  echo "${C_BOLD}─── $name ───${C_RST}"
  # Pasar el entorno (ALLOW_USB, USB_DISK) a cada test
  ALLOW_USB="${ALLOW_USB:-0}" USB_DISK="${USB_DISK:-}" bash "$test"
  rc=$?
  case $rc in
    0)  RESULTS+=("${C_GRN}PASS${C_RST}  $name"); PASS=$((PASS+1)) ;;
    77) RESULTS+=("${C_YEL}SKIP${C_RST}  $name"); SKIP=$((SKIP+1)) ;;
    *)  RESULTS+=("${C_RED}FAIL${C_RST}  $name"); FAIL=$((FAIL+1)) ;;
  esac
  echo ""
done

# ── Tabla resumen ────────────────────────────────────────────────────────────
echo "${C_BOLD}═══ Resumen ═══${C_RST}"
for r in "${RESULTS[@]}"; do echo "  $r"; done
echo ""
echo "${C_BOLD}Total:${C_RST} $((PASS+FAIL+SKIP))  ${C_GRN}PASS: $PASS${C_RST}  ${C_RED}FAIL: $FAIL${C_RST}  ${C_YEL}SKIP: $SKIP${C_RST}"

[[ $FAIL -eq 0 ]] && exit 0 || exit 1
