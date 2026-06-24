# Tests de destrucción · Storage NimOS

Batería de tests que simulan los fallos reales del lifecycle de pools (los que
ocurrieron en producción los días 28/05–01/06) y verifican que NimOS se
recupera o degrada con honestidad, sin corromper datos ni mentir sobre el
estado.

## Filosofía

Cada test provoca un fallo concreto y comprueba una de dos cosas:
1. **Recuperación**: el sistema vuelve solo a estado limpio.
2. **Degradación honesta**: si no puede recuperar, lo reporta con causa clara
   y no corrompe nada.

**NUNCA** se acepta un estado fantasma silencioso (BD dice una cosa, kernel
otra, sin aviso).

## Seguridad

- Por defecto, **solo loop devices** (archivos `.img` con `losetup`). Cero
  hardware real, corre en cualquier sitio.
- Para usar un **disco USB real** de prueba: `ALLOW_USB=1 USB_DISK=/dev/sdX`.
- Guards inviolables: los tests **abortan** si el target es el disco de
  arranque o aloja un pool de producción (`/nimos/pools/*` montado). Esto no
  limita la destrucción de discos de prueba — solo evita el único accidente
  que dolería.

## Uso

```bash
# Todos los tests, sobre loop devices
sudo ./run_all.sh

# Solo algunos
sudo ./run_all.sh T02 T04

# Con un disco USB real de prueba (se formatea por completo)
sudo ALLOW_USB=1 USB_DISK=/dev/sdX ./run_all.sh
```

Salida: tabla `PASS` / `FAIL` / `SKIP` por test. `SKIP` aparece si el entorno
no soporta btrfs montable (p.ej. algunos contenedores); en la Raspberry, con
btrfs nativo, nunca salta.

## Tests

| Test | Simula | Verifica |
|------|--------|----------|
| T02 | Mounts apilados (pool montado N veces) | El desapilado deja 1 capa (reconcileMountState) |
| T04 | Unmount con submount encima (overlay Docker) | poolHasSubmounts detecta y aborta sin lazy (no fantasma) |
| T05 | Wipe sobre FS montado | El guard rechaza; wipe solo tras unmount confirmado |
| T06 | Pool en read-only por I/O errors | poolMountIsReadOnly lo detecta (R2) |

*(T01, T03, T07–T10 pendientes de añadir: crash en create/destroy, robo por
udisks2, containerd zombie, huérfana con contenido, y el combo "todo sucio".)*

## Añadir un test nuevo

1. Crear `TNN_nombre.sh` copiando la estructura de uno existente.
2. `source lib/common.sh`, llamar `require_root` + `require_btrfs`.
3. Usar `setup_loop_disk`, `make_btrfs`, los `assert_*`, y `finish_test`.
4. El runner lo descubre solo (patrón `T[0-9][0-9]_*.sh`).

## Requisitos

`btrfs-progs`, `util-linux` (losetup, findmnt, wipefs), bash, root.
