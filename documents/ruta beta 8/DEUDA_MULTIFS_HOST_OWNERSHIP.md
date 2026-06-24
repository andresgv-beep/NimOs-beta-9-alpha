# DEUDA — Multi-Filesystem + Host Ownership

**Estado**: NO IMPLEMENTADA · POSTPUESTA TRAS REVERT
**Origen**: Beta 8.2 (intentada y revertida 19/05/2026)
**Para**: Beta 9 (o cuando se aborde con arquitectura propia)

---

## CONTEXTO

Durante el desarrollo de Beta 8.2 se intentó añadir soporte multi-filesystem
(ext4, ntfs, fat32, xfs, exfat) para que NimOS pudiera gestionar discos
externos no-BTRFS. La implementación llegó hasta Fases 1+2 (schema +
probe genérico).

Durante el test E2E, NimOS detectó la partición FAT32 del bootloader de
Raspberry Pi (`/boot/firmware` en `/dev/mmcblk0p1`) y la mostró como
"importable como managed". Si el usuario hubiera pulsado "Importar",
NimOS habría intentado gestionar la partición boot del propio host
→ **brick al siguiente reboot**.

El bug se arregló a nivel táctico con un filtro `isSystemDisk()` que
compara discos físicos contra el del rootfs, pero la conclusión es
arquitectónica: **NimOS necesita un modelo de ownership ANTES de
permitir cualquier operación destructiva sobre filesystems detectados.**

---

## MANIFESTO DE ARQUITECTURA (Andrés, 19/05/2026)

### 1. System Ownership Classification

Toda entidad de storage se clasifica en uno de:

- **`system`**: parte del host que ejecuta NimOS (rootfs, /boot, swap, EFI, etc.)
- **`managed`**: gestionada por NimOS (pools creados desde la UI)
- **`observed`**: detectada pero no gestionada (orphans BTRFS, fs ajenos)
- **`external`**: discos externos del usuario (USBs, drives ajenos)

Y dos flags ortogonales:
- **`protected`**: NimOS NO puede tocarla (bloqueo absoluto)
- **`adoptable`**: puede importarse a managed con seguridad

### 2. Detección del host real

NO confiar en nombres (`/dev/sda`, `/boot`). Detectar dinámicamente:
- Qué discos contienen `/`
- Qué discos contienen `/boot`, EFI, swap
- Qué discos tienen layers de cifrado, mdraid, LVM activos

Herramientas: `findmnt`, `lsblk`, `blkid`, `/proc/mounts`, `/proc/swaps`.

### 3. Protección de disco completo

Si UNA partición de un disco pertenece al sistema → **TODO el disco
queda protegido**. Esto evita destruir GPT/EFI/rootfs por error sobre
particiones "vecinas".

### 4. Observed ≠ Adoptable

Que NimOS detecte una entidad no implica que pueda importarla.
Ejemplo: el bootfs es detectable pero NO adoptable.

### 5. Policy layer obligatorio

Todas las operaciones destructivas (`format`, `wipe`, `import`, `destroy`)
pasan por una capa Policy ANTES de ejecutarse:

```go
policy.CanFormat(disk) → bool, reason
policy.CanImport(fs)   → bool, reason
policy.CanDestroy(pool) → bool, reason
```

Nunca desde UI o handlers directamente.

### 6. Safe by default

Si NimOS no está seguro de si una operación es segura:
- NO ofrece format
- NO ofrece import
- NO ofrece wipe

Mejor falso negativo que brickear.

### 7. HostFootprint runtime

Al arrancar, NimOS detecta y guarda en memoria:
- Discos del sistema
- Mounts críticos
- EFI, rootfs, swap
- Crypt layers (LUKS)
- mdraid
- LVM

Toda operación pregunta: "¿afecta al host footprint?"

### 8. Backend manda, UI muestra

La UI puede ocultar botones para mejor UX, pero el backend
DEBE bloquear:
- format
- wipe
- import
- destroy

sobre storage clasificado como `system` o `protected`.

### 9. UI con secciones separadas

En lugar de mostrar todo junto:
- **System Storage**: lo que sostiene NimOS (read-only display)
- **Managed Storage**: pools que NimOS gestiona
- **Observed Storage**: detectados sin gestionar
- **External Storage**: USBs y discos externos

### 10. Regla final

> "NimOS nunca debe poder destruir accidentalmente el storage del host
> donde está ejecutándose."

---

## SCOPE DE LA IMPLEMENTACIÓN FUTURA

### Filesystems a soportar

```
ext4    → Linux nativo, perms UNIX completos
ntfs    → Windows, requiere ntfs-3g, uid/gid en mount
fat32   → Universal, sin perms UNIX
xfs     → Linux moderno (RHEL/CentOS default), perms UNIX
exfat   → Discos grandes, sin perms UNIX
```

### NO soportados (detectables pero no gestionables)

```
zfs        → Otro proyecto (BTRFS-only decisión arquitectónica)
lvm2       → Volúmenes lógicos (futuro lejano)
mdraid     → Software RAID (futuro lejano)
luks       → Cifrado (necesita decrypt flow + key mgmt)
hfs+/apfs  → Mac (raro en NAS Linux)
f2fs       → Flash optimizado (caso específico)
```

Para estos, NimOS los detecta pero los marca como `observed` SIN flag
`adoptable`. La UI los muestra como "Detectado pero no gestionable".

---

## ESTIMACIÓN HONESTA

```
Fase 0 — HostFootprint detector             ~2h
   · findRootDevice, findBootDevices, findSwapDevices
   · LUKS/mdraid/LVM probe (read-only inspect)
   · Cache runtime + invalidation al hotplug
   · Tests

Fase 1 — Schema con ownership               ~1.5h
   · storage_pools.ownership (system|managed|observed|external)
   · storage_pools.adoptable BOOLEAN
   · storage_pools.protected BOOLEAN
   · Tabla storage_host_devices (discos del sistema)

Fase 2 — Policy layer                       ~3h
   · CanFormat(disk) → (bool, reason)
   · CanImport(fs) → (bool, reason)
   · CanDestroy(pool) → (bool, reason)
   · CanWipe(disk) → (bool, reason)
   · Tests exhaustivos

Fase 3 — Probe pluggable multi-fs           ~2h
   · ProbeBackend interface
   · btrfsProbe + ext4Probe + ntfsProbe + fatProbe + xfsProbe + exfatProbe
   · Cruce con HostFootprint para clasificar ownership

Fase 4 — Mount/Unmount pluggable            ~2h
   · MountBackend por fs_type
   · Permisos uid/gid para ntfs/fat/exfat
   · fstab management seguro

Fase 5 — Refactor endpoints                 ~2.5h
   · Todos los POST destructivos pasan por Policy
   · 401/403 con razón clara si bloqueado
   · Tests de seguridad

Fase 6 — UI con 4 secciones                 ~2h
   · System Storage (read-only)
   · Managed Storage
   · Observed Storage
   · External Storage
   · Botones condicionales según ownership/adoptable

Fase 7 — Tests E2E intensivos               ~3h
   · Test contra Raspberry Pi (mmcblk)
   · Test contra NVMe boot
   · Test contra SATA con dual-boot
   · Test con LUKS volumes
   · Test con mdraid
   · Test con cambio en caliente

TOTAL HONESTO: ~18-20h en 6-8 sesiones disciplinadas
```

---

## LECCIONES APRENDIDAS

1. **Detectar ≠ Gestionar**: que blkid reporte un FS no significa
   que NimOS deba ofrecer importarlo.

2. **Filtrar por mount point es frágil**: paths "raros" como
   `/boot/firmware` (Pi), `/persist` (Android), `/sysroot` (immutable distros)
   rompen las listas estáticas. Comparar discos físicos es robusto.

3. **El miedo del arquitecto es información**: si te da miedo activar
   un feature porque puede romper sistemas de usuarios → ese feature
   no está listo. No lo metas hasta que la deuda esté pagada.

4. **Safe-by-default > feature-completo**: mejor que NimOS no sepa
   gestionar un USB ext4 ahora a que NimOS adopte por error la
   partición boot.

5. **Test E2E en hardware real es OBLIGATORIO**: el bug del bootfs
   NO habría salido en CI con discos mock. Salió en la primera prueba
   en Raspberry Pi real.

---

## ESTADO ACTUAL TRAS REVERT (19/05/2026)

```
✅ Storage Beta 8.1 cerrado E2E (BTRFS-only)
✅ Schema v2 (sin multi-fs)
✅ Observer detecta solo BTRFS
✅ Build/test/race/vet TODO VERDE
✅ Probado en NAS real funcional
```

**NimOS sigue siendo BTRFS-only puro hasta que se aborde esta deuda
con la arquitectura completa propuesta arriba.**

---

## CRITERIO PARA EMPEZAR

Empezar esta deuda CUANDO:
- Network module esté cerrado (otra deuda en progreso)
- O al menos un módulo grande de Beta 9 esté E2E verificado
- O cuando sea NECESARIO funcionalmente (usuarios pidiendo USB support)

NO empezar:
- Como "side project" en una sesión casual
- Sin las 18-20h de tiempo disponibles
- Sin el manifesto de arquitectura claro encima de la mesa
