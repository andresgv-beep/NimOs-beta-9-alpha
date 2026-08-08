# NimOS Storage — Auditoría completa Beta 8.1

**Fecha**: 17/05/2026
**Objetivo**: identificar deuda dual (legacy + v2), JSON huérfano, código muerto.
**Estado**: NimOS Beta 8.1 con backend storage estable, modelo Managed/Observed operativo.

---

## 1. RESUMEN EJECUTIVO

```
✅ Lo que funciona
   - 32 archivos productivos storage_*.go (~310 KB)
   - 19 archivos de tests storage_*_test.go (~155 KB)
   - 219 tests PASS, 0 races
   - SQLite es fuente de verdad real
   - storage.json no se escribe nunca; solo se lee al migrar

⚠ Deuda detectada
   - 2 stacks HTTP en producción (legacy + v2)
   - 17 endpoints v2 declarados, solo 1 consumido por la UI
   - .nimos-pool.json se escribe pero NUNCA se lee (huérfano)
   - 2 endpoints sustituidos por C3 que siguen activos (restorable/restore)
   - ~11 funciones huérfanas (declaradas, no llamadas)
   - 2 archivos de tests pegados solo al v2 HTTP (~980 líneas)
```

---

## 2. MAPA DE ENDPOINTS HTTP

### 2.1 Legacy (`storage_http.go`) — CONSUMIDO POR LA UI

```
GET endpoints:
  /api/storage              ← alias de /pools
  /api/storage/pools        ✅ UI: loadAll
  /api/storage/disks        ✅ UI: loadAll
  /api/storage/status       ✅ UI: loadAll
  /api/storage/alerts       ✅ UI: loadAll
  /api/storage/capabilities ✅ UI: loadAll
  /api/storage/health
  /api/storage/restorable   ⚠ STUB (devuelve []) — UI lo llama pero siempre vacío
  /api/storage/observed     ✅ UI: loadAll (C3.1)
  /api/storage/snapshots    ✅ UI: panel snapshots
  /api/storage/scrub/status ✅ UI: scrub tab
  /api/storage/resilver/status  (no usado por UI)
  /api/storage/datasets     (no usado por UI)

POST endpoints:
  /api/storage/pool             ✅ UI: createPool (C3.4 con doble intención)
  /api/storage/scan             ✅ UI: rescanDisks
  /api/storage/wipe             ✅ UI: wipefs (C3.2 destroy orphan)
  /api/storage/pool/destroy     ✅ UI: DestroyPoolWizard
  /api/storage/pool/export      ✅ UI: ExportPoolWizard
  /api/storage/pool/restore     ⚠ STUB — UI lo llama pero devuelve error siempre
  /api/storage/pool/import      ✅ UI: C3.2 import flow
  /api/storage/pool/replace-disk    (UI no llama actualmente)
  /api/storage/pool/detach-disk     (UI no llama)
  /api/storage/pool/attach-disk     (UI no llama)
  /api/storage/pool/resilver-status (UI no llama)
  /api/storage/backup           (no usado)
  /api/storage/snapshot         ✅ UI: crear snapshot
  /api/storage/snapshot/rollback✅ UI: rollback
  /api/storage/scrub            ✅ UI: start scrub
  /api/storage/dataset          (no usado)
```

**Total legacy**: 28 rutas. UI consume **~18 efectivamente**, 10 sin uso real.

### 2.2 V2 (`storage_http_v2.go`) — MAYORMENTE HUÉRFANO

```
TODAS estas rutas están REGISTRADAS y FUNCIONAN, pero:

USADA POR LA UI:
  /api/storage/v2/pools  ⚠ usado SOLO por Settings.svelte:263
                            (resto de UI usa /api/storage/pools legacy)

NO USADAS POR LA UI:
  /api/storage/v2/pools/  (sin uso UI)
  /api/storage/v2/devices
  /api/storage/v2/operations
  /api/storage/v2/generation
  /api/storage/v2/scan
  /api/storage/v2/capabilities
  /api/storage/v2/status
  /api/storage/v2/alerts
  /api/storage/v2/restorable
  /api/storage/v2/disks
  /api/storage/v2/wipe
  /api/storage/v2/scrub
  /api/storage/v2/scrub/status
  /api/storage/v2/snapshots
  /api/storage/v2/pool/restore
  /api/storage/v2/pool/export
  /api/storage/v2/pool/destroy
```

**Total v2**: 18 rutas. UI consume **1**. **17 son código muerto** desde el punto de vista del cliente.

---

## 3. JSON HUÉRFANO

### 3.1 storage.json — ESTADO CORRECTO

```
✅ NO se escribe (verificado: 0 callers de WriteFile sobre storageConfigFile)
✅ Solo se lee desde storage_migrate_json.go (one-shot al boot)
✅ Tras migración se renombra a storage.json.migrated-<timestamp>
✅ getStorageConfigFull/saveStorageConfigFull son adapters: leen/escriben SQLite
```

**Conclusión**: storage.json YA NO ES un JSON huérfano. Es un artifact histórico del migrador. ✓

### 3.2 .nimos-pool.json — HUÉRFANO REAL

```
⚠ Se escribe en cada createPool (storage_btrfs_pool.go:304)
⚠ NADIE lo lee. Originalmente se leía en restorePoolFromIdentity, ahora stub.
⚠ Es ~150 bytes por pool, no es crítico pero ensucia el filesystem
```

**Decisión recomendada**: o (a) eliminar `writePoolIdentity` y la llamada en el step 5.2,
o (b) reimplementar el flujo de restore para que lo use. **Mi voto: (a)**, ya que C3 cubre el caso con observer + import.

### 3.3 Backup config a pool — RESIDUO

```
backupConfigToPoolGo() (storage_startup.go:342) incluye storage.json en
la lista de archivos a backupear:

    configFiles := []string{
        "/var/lib/nimos/config/nimos.db",     ← ✅ correcto
        "/var/lib/nimos/config/storage.json", ← ⚠ ya no existe en disco
        "/var/lib/nimos/config/docker.json",
        ...
    }
```

**Decisión recomendada**: quitar `storage.json` de la lista. ya que SQLite es fuente de verdad.

---

## 4. FUNCIONES HUÉRFANAS DETECTADAS

```
storage_pool_info.go:9      enrichDisksWithSmart   (1 sola definición, 0 callers)
storage_btrfs_features.go   getAllScrubSchedules   (sin callers)
storage_btrfs_features.go   getScrubSchedule       (sin callers)
storage_btrfs_features.go   saveScrubSchedule      (sin callers)
storage_wipe.go             journalRecover         (sin callers)
storage_btrfs_probe.go      readMountedBtrfsUUIDs  (sin callers)
storage_startup.go          copyFile               (1 caller: él mismo en comentario)
storage_common.go           formatDuration         (sin callers)
storage_disk_mgmt.go        partitionName          (sin callers)
storage_disk_mgmt.go        waitForDevice          (sin callers)
storage_scheduler.go        StopStorageScheduler   (1 caller: tests)
```

**Total**: ~11 funciones, estimado ~250 líneas de código muerto.

---

## 5. ENDPOINTS SUSTITUIDOS POR C3 PERO AÚN ACTIVOS

```
/api/storage/restorable      → siempre devuelve []
/api/storage/pool/restore    → siempre devuelve error "pool_restore_unsupported"
```

**Justificación de eliminación**: el modelo Managed/Observed (C3) **sustituye completamente** el flujo de restore:
- Pool desmontado → aparece en `/observed` como orphan
- Usuario lo recupera vía `/pool/import` (C3.1)
- UI ya tiene el flujo en sección "Observados"

**Recomendación**:
1. Mantener los endpoints como aliases legacy que respondan algo razonable (status 410 Gone, o redirect a /observed)
2. O eliminarlos del backend + quitar referencias UI

---

## 6. TESTS — IMPACTO DE LA LIMPIEZA

```
Si se elimina storage_http_v2.go:
  ❌ storage_http_v2_test.go              (492 líneas) → ELIMINAR
  ❌ storage_http_v2_mutations_test.go    (490 líneas) → ELIMINAR

Se quedan SIN tocar (testan lógica interna):
  ✅ storage_repo.go / storage_repo_test.go
  ✅ storage_repo_ops.go / storage_repo_ops_test.go
  ✅ storage_service.go / storage_service_test.go
  ✅ storage_service_devices_test.go
  ✅ storage_service_create_test.go
  ✅ storage_executor*.go / storage_executor_real_test.go
  ✅ storage_observer*.go / storage_observer_test.go
  ✅ storage_btrfs_import*.go / storage_btrfs_import_test.go
  ✅ storage_btrfs_probe*.go (sin test directo)
  ✅ storage_legacy_adapter*.go / storage_legacy_adapter_test.go
  ✅ storage_migrate_json*.go / storage_migrate_json_test.go
  ✅ storage_reconciler*.go / storage_reconciler_test.go
  ✅ storage_scanner*.go / storage_scanner_test.go
  ✅ storage_recovery*.go / storage_recovery_test.go
  ✅ storage_policy*.go / storage_policy_test.go
  ✅ storage_wipe*.go / storage_wipe_test.go
  ✅ storage_create_pool_validate_test.go
  ✅ storage_integration_test.go
```

**Trade-off**: pierdes 982 líneas de tests del handler v2, pero solo testaban una API que la UI no usa.
**Justificación**: tests del HTTP v2 solo demuestran que el HTTP v2 funciona, no aportan cobertura única a la lógica de negocio (esa ya está cubierta por los tests del service y del repo).

---

## 7. PLAN DE UNIFICACIÓN RECOMENDADO

### Fase A — Limpieza segura (sin tocar producción)

```
A1. Quitar .nimos-pool.json:
    · Eliminar writePoolIdentity() de storage_common.go
    · Eliminar step 5.2 — writePoolIdentity de createPoolBtrfs
    · Borrar archivos .nimos-pool.json existentes en pools managed

A2. Quitar restorable/restore (sustituidos por C3):
    · Eliminar handler restorable, restorePoolFromIdentity
    · Eliminar /api/storage/restorable, /api/storage/pool/restore de routing
    · Eliminar scanForRestorablePoolsGo (stub)
    · UI: quitar pestaña/sección "Restore" (ya hay observers que cubren ese flujo)

A3. Limpiar funciones huérfanas:
    · 11 funciones identificadas en sección 4
    · go build + go vet + tests para verificar
```

### Fase B — Migrar Settings.svelte fuera de v2

```
B1. Settings.svelte:263 usa /api/storage/v2/pools
    → cambiar a /api/storage/pools (legacy)
    → wrapper unwrapV2 ya tolera ambos formatos

B2. Verificar UI sin referencias a /v2/*
B3. Test E2E: crear pool, listar, destruir, exportar, importar
```

### Fase C — Eliminar v2 HTTP completo

```
C1. Borrar storage_http_v2.go (~860 líneas)
C2. Borrar storage_http_v2_test.go + storage_http_v2_mutations_test.go (~982 líneas)
C3. Quitar Register() de http.go:381
C4. Quitar storageHTTPHandler de storage_boot.go:47
C5. Verificar build, tests, race
```

### Fase D — Limpieza JSON residual

```
D1. Quitar storage.json de la lista de backupConfigToPoolGo
D2. Verificar que no quedan referencias a storage.json fuera del migrador
D3. Considerar eliminar el migrador entero si está claro que producción ya migró
```

---

## 8. RIESGOS Y MITIGACIONES

### Riesgo 1: clientes externos del v2
```
¿Algún script, CLI, o herramienta externa usa /api/storage/v2/*?
Si el usuario no tiene scripts custom, riesgo nulo.
Mitigación: grep en /opt/nimos del NAS antes de borrar.
```

### Riesgo 2: el "v2 público" tiene endpoints SIN equivalente legacy
```
Estos endpoints v2 NO existen en legacy:
  /api/storage/v2/operations
  /api/storage/v2/generation
  /api/storage/v2/devices

Si se eliminan, se pierde funcionalidad de:
  - Inspeccionar operations journal
  - Long-polling con generations
  - Listado de devices crudos

Mitigación: añadirlos a legacy ANTES de borrar v2, si se consideran útiles.
Mi voto: NO añadir. Si los necesitamos en Beta 9, los reimplementamos con diseño limpio.
```

### Riesgo 3: tests del service interno pueden depender del HTTP v2 de forma indirecta
```
Verificación: go test después de borrar v2 — esperado 0 fallos.
Si fallan, son tests con dependencias mal aisladas → refactor menor.
```

---

## 9. ESTIMACIÓN TIEMPO POR FASE

```
Fase A  · Limpieza segura          ~45 min
Fase B  · Settings.svelte → legacy ~20 min
Fase C  · Eliminar v2 HTTP         ~60 min
Fase D  · Limpieza JSON residual   ~30 min

TOTAL                              ~2h 30min
```

---

## 10. ESTADO FINAL ESPERADO TRAS LIMPIEZA

```
Antes:                              Después:
─────────                           ─────────
HTTP stacks:           2            HTTP stacks:           1
Endpoints declarados:  46           Endpoints declarados:  ~18
Endpoints sin uso:     27           Endpoints sin uso:     0
JSON huérfano:         2            JSON huérfano:         0
Funciones huérfanas:   ~11          Funciones huérfanas:   0
Líneas storage_*.go:   ~10.5k       Líneas storage_*.go:   ~9.0k
Líneas tests:          ~6.5k        Líneas tests:          ~5.5k
Tests verdes:          219          Tests verdes:          ~190 (sin v2_test)
```

---

## 11. RECOMENDACIÓN FINAL

**Atacar las 4 fases en orden, una sesión cada una**.
NO hacerlo todo en una sesión maratoniana — el riesgo de cascada es alto.

**Orden de prioridad por valor / riesgo**:

1. **Fase A** (mañana o cuando descanses) — riesgo BAJO, valor MEDIO
2. **Fase B** (segunda sesión) — riesgo BAJO, valor BAJO
3. **Fase D** (tercera sesión) — riesgo BAJO, valor BAJO
4. **Fase C** (última sesión, con todo lo anterior estable) — riesgo MEDIO, valor ALTO

Cada fase termina con: build + vet + tests + race + deploy + verificación E2E manual.

---

## ARCHIVOS REFERENCIADOS

Productivos a TOCAR durante limpieza:
- storage_common.go (quitar writePoolIdentity)
- storage_btrfs_pool.go (quitar step 5.2)
- storage_startup.go (quitar scanForRestorablePoolsGo, restorePoolFromIdentity, ajustar backup)
- storage_http.go (quitar restore/restorable cases)
- http.go (quitar Register de v2)
- storage_boot.go (quitar inicialización de storageHTTPHandler)
- storage_pool_info.go (quitar enrichDisksWithSmart)
- storage_btrfs_features.go (quitar scrub schedules muertas)
- storage_disk_mgmt.go (quitar partitionName, waitForDevice)
- storage_btrfs_probe.go (quitar readMountedBtrfsUUIDs)
- storage_wipe.go (quitar journalRecover)

A BORRAR completos:
- storage_http_v2.go
- storage_http_v2_test.go
- storage_http_v2_mutations_test.go

UI a TOCAR:
- Settings.svelte:263 (cambiar /v2/pools → /pools)
- StorageApp.svelte (quitar pestaña Restore si decidimos quitar)

Sin cambios:
- storage_service.go, storage_repo.go, storage_repo_ops.go
- storage_observer.go, storage_observe_types.go, storage_btrfs_probe.go (resto)
- storage_legacy_adapter.go (este se queda — es el adapter SQLite↔legacy)
- storage_scanner.go, storage_reconciler.go, storage_scheduler.go
- storage_btrfs_import.go (el nuevo de C3)
- storage_wipe.go (excepto journalRecover huérfana)
- storage_recovery.go (sin uso?)
- storage_policy.go
- storage_clock.go
- storage_executor*.go
- storage_health.go
- storage_types.go
- storage_schema.go
- storage_config.go (se queda — adapter mínimo)
- storage_migrate_json.go (se queda — migrador one-shot)

