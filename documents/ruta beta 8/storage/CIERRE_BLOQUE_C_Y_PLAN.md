# NimOS Beta 8.1 · Fase 7 · Cierre Bloque C y Plan Final

**Fecha**: 17/05/2026
**Estado**: Bloque C completo · pendiente limpieza + migración a stack único
**Próxima sesión**: Sesión 1 — Limpieza pre-v2

---

## 1. ESTADO ACTUAL DEL PROYECTO

### 1.1 Bloques completados

```
✅ Bloque A · ZFS eliminado del runtime backend
✅ Bloque B · SQLite es fuente de verdad
✅ Bloque B v2 · Adapter funcional con create/destroy E2E
✅ Fix RAID1 capacidad correcta con discos asimétricos
✅ Bloque C1 · Storage Observer + endpoint /api/storage/observed
✅ Bloque C2 · Pre-flight enriquecido con ErrDiskHasFilesystem tipado
✅ Bloque C3.1 · Backend import (POST /api/storage/pool/import)
✅ Bloque C3.2 · UI sección Observados con import/destroy modales
✅ Bloque C3.3 · Indicadores de estado en lista de discos
✅ Bloque C3.4 · Wizard create con doble intención
✅ Hotfix · Deadlock create (instrumentación step 5.X)
✅ Hotfix · jsonHdrs import faltante
✅ Recuperación pool DATOS4 tras incidente
```

### 1.2 Estado producción NAS

```
Host:    nimosbarraca.duckdns.org (Raspberry Pi)
Daemon:  /opt/nimos/daemon/nimos-daemon
Logs:    /var/log/nimos/daemon-error.log (stderr captura todo)
DB:      /var/lib/nimos/config/nimos.db (SQLite, MaxOpenConns=1)

Pool actual: test1 (BTRFS RAID1)
  · sda (Patriot SSD 112 GB) + sdb (ST320LT020 HDD 298 GB)
  · UUID: caeb2c40-2f98-4fa3-994c-ff1c88ed6be9
  · Mount: /nimos/pools/test1
  · Capacidad usable: 119 GB
  · Estado SQLite coherente con BTRFS y fstab
  · Observer activo (interval 60s)
  · Persistencia verificada tras restart

Test E2E verificado:
  · Create pool desde UI → SQLite + BTRFS + fstab coherentes ✓
  · Desmontar (Export) → pasa a sección Observados ✓
  · Importar desde Observados → recuperado ✓
  · Indicadores discos muestran estado correcto ✓
  · Wizard create con disco huérfano → modal doble intención ✓
```

### 1.3 Stats del trabajo

```
Líneas Go tocadas:     ~3000
Líneas Svelte tocadas: ~1500
Archivos productivos:  32 storage_*.go (310 KB)
Archivos tests:        19 storage_*_test.go (155 KB)
Tests verdes:          219 PASS
Races:                 0
Bugs reales muertos:   2 (deadlock + datos perdidos por formateo)
Bugs míos:             2 (} huérfano + jsonHdrs)
```

---

## 2. MODELO MANAGED/OBSERVED

### 2.1 Concepto

Beta 8.1 introduce un modelo dual donde NimOS distingue claramente entre:

```
MANAGED · gestionado por NimOS
  · Registrado en storage_pools (SQLite)
  · Tiene entrada en /etc/fstab
  · Aparece en la UI como pool normal
  · NimOS controla su lifecycle

OBSERVED · detectado pero no gestionado
  · BTRFS existe en discos
  · NO está registrado en SQLite
  · Detectado por el Storage Observer
  · La UI lo muestra en sección "Observados"
  · El usuario decide: importar o destruir
```

### 2.2 Transiciones

```
                    create
        nothing ─────────────────→ MANAGED
                                      │
                                      │ export
                                      ▼
                                  OBSERVED
                                      │
                          ┌───────────┤
                          │ import    │ destroy
                          ▼           ▼
                       MANAGED     nothing
```

### 2.3 Detección

El Storage Observer:
- Corre cada 60s en background
- Lee `btrfs filesystem show --raw` + `/sys/block/*` + `/proc/self/mounts`
- Computa un fingerprint (sha256) para detectar cambios
- Si cambia → re-scan inmediato
- Cruza con SQLite para determinar IsManaged
- Publica snapshot atómico vía `globalObserver.Snapshot()`
- Notifica vía `notifyStorageChanged()` tras operaciones de mutación

### 2.4 Endpoints involucrados

```
GET  /api/storage/observed                 · snapshot del observer
GET  /api/storage/observed?refresh=true    · fuerza re-scan
POST /api/storage/pool/import              · observed → managed
POST /api/storage/pool/export              · managed → observed
POST /api/storage/pool/destroy             · managed → nothing
POST /api/storage/wipe                     · observed → nothing
POST /api/storage/pool (create)            · nothing → managed
                                             devuelve 409 si hay BTRFS
                                             en disco (DISK_HAS_FILESYSTEM)
```

### 2.5 Estructura de divergencia

El observer pre-computa divergencias:

```
orphan_filesystem      · BTRFS detectado no managed
pool_missing_device    · pool managed con devices missing
unexpected_io_errors   · device con io_errs > 0
pool_unmounted         · managed pero no montado
profile_mismatch       · profile en disco ≠ profile en SQLite
```

Severities: `info`, `warning`, `critical`.

---

## 3. DEUDA TÉCNICA IDENTIFICADA

### 3.1 Stack HTTP duplicado

```
storage_http.go     · legacy · 236 líneas · 28 rutas · UI usa 18
storage_http_v2.go  · v2     · 929 líneas · 18 rutas · UI usa  1
```

**Calidad arquitectónica**:
```
                  Legacy     V2
                  ──────     ───
Diseño            4/10       8/10
Modularidad       2/10       9/10
Testabilidad      3/10       9/10
HTTP semantics    4/10       9/10
Producción        9/10       1/10
```

V2 es objetivamente mejor diseño, falta uso en producción.

### 3.2 JSON huérfano

```
storage.json           ✓ correctamente migrado a SQLite (solo lectura one-shot)
.nimos-pool.json       ⚠ se escribe en cada create pero NADIE lo lee
```

### 3.3 Endpoints sustituidos por C3 pero activos

```
/api/storage/restorable      → stub, devuelve []
/api/storage/pool/restore    → stub, devuelve error siempre
```

Sustituidos por:
- `/observed` (orphans detectados)
- `/pool/import` (importar managed)

### 3.4 Funciones huérfanas detectadas

```
enrichDisksWithSmart   (storage_pool_info.go)
getAllScrubSchedules   (storage_btrfs_features.go)
getScrubSchedule       (storage_btrfs_features.go)
saveScrubSchedule      (storage_btrfs_features.go)
journalRecover         (storage_wipe.go)
readMountedBtrfsUUIDs  (storage_btrfs_probe.go)
copyFile               (storage_startup.go) ← verificar uso
formatDuration         (storage_common.go)
partitionName          (storage_disk_mgmt.go)
waitForDevice          (storage_disk_mgmt.go)
StopStorageScheduler   (storage_scheduler.go) ← verificar uso en tests
```

Estimado ~250 líneas de código muerto.

### 3.5 Endpoints legacy huérfanos (sin uso UI)

```
GET  /storage/health
GET  /storage/resilver/status
GET  /storage/datasets
POST /storage/backup
POST /storage/dataset
POST /storage/pool/replace-disk
POST /storage/pool/detach-disk
POST /storage/pool/attach-disk
POST /storage/pool/resilver-status
```

Total: 9 endpoints legacy sin consumidor.

### 3.6 Restos UI

```
Settings.svelte:263 · usa /api/storage/v2/pools
                      (el único sitio que usa v2 hoy)
```

---

## 4. GAPS DEL V2 PARA SER DROP-IN REPLACEMENT

### 4.1 Endpoints que faltan añadir

```
GET  /api/storage/v2/observed                ← reemplaza legacy /observed
POST /api/storage/v2/pools/import            ← reemplaza legacy /pool/import
POST /api/storage/v2/snapshots               ← reemplaza legacy /snapshot
POST /api/storage/v2/snapshots/rollback      ← reemplaza legacy /snapshot/rollback
```

### 4.2 Integración pendiente

```
POST /api/storage/v2/pools (createPool):
  Devolver 409 con DISK_HAS_FILESYSTEM cuando preFlightCheck detecta
  filesystem existente. Hoy lo hace legacy (Bloque C3.4).
```

### 4.3 Cleanup propio del v2

```
Eliminar handler stubs:
  · handlePoolRestore (POST /v2/pool/restore)
  · handleRestorable  (GET /v2/restorable)
```

### 4.4 Estimación

```
Líneas a añadir:   ~200 Go productivo
Líneas a borrar:   ~50  (stubs)
Tests:             ~150 líneas
Tiempo:            ~90 minutos
```

---

## 5. PLAN DE CIERRE FASE 7

### 5.1 SESIÓN 1 · Limpieza pre-v2 (~75 min)

**Objetivo**: dejar legacy y v2 limpios antes de empezar la migración.

```
Bloque L1 · .nimos-pool.json huérfano
  · Quitar writePoolIdentity() de storage_common.go
  · Quitar step 5.2 — writePoolIdentity de createPoolBtrfs
  · Decidir: borrar .nimos-pool.json existentes o dejar como artifact

Bloque L2 · Stubs restorable/restore (legacy + v2)
  · Borrar handleRestorable de legacy
  · Borrar restorePoolFromIdentity stub
  · Borrar scanForRestorablePoolsGo
  · Borrar handlePoolRestore + handleRestorable de v2
  · UI: quitar pestaña/sección "Restore" (sustituida por Observados)

Bloque L3 · Funciones huérfanas (11 detectadas)
  · Verificar cada una con grep para descartar falsos positivos
  · Eliminar las confirmadas como muertas
  · go build + tests para validar

Bloque L4 · JSON residual en backupConfigToPoolGo
  · Quitar storage.json de la lista de archivos a backupear
  · Evaluar si la función entera tiene sentido (solo SQLite ahora)

Bloque L5 · Endpoints legacy sin uso UI (9 detectados)
  · /health, /resilver/status, /datasets, /backup, /dataset
  · /pool/replace-disk, /detach-disk, /attach-disk, /resilver-status
  · Borrar handlers + casos del switch

VERIFICACIÓN
  · go vet ./...
  · go test
  · go test -race
  · Deploy real en NAS
  · Test E2E manual:
    · Create pool, export, import, destroy
    · Sección Observados aparece tras export
    · Indicadores en lista de discos correctos
```

### 5.2 SESIÓN 2 · Completar V2 (~90 min)

```
Bloque V2.1 · Endpoints faltantes (4)
  · handleObserved        → reuse globalObserver.Snapshot()
  · handleImport          → reuse importPoolBtrfs
  · handleSnapshotCreate  → reuse createSnapshot
  · handleSnapshotRollback→ reuse rollbackSnapshot

Bloque V2.2 · Integración C3.4 en createPool v2
  · El service.CreatePool() ya invoca preFlightCheck
  · Mapear ErrDiskHasFilesystem → ServiceError con DISK_HAS_FILESYSTEM
  · writeServiceError ya mapea a 409

Bloque V2.3 · Tests
  · Tests directos de los 4 nuevos handlers (~150 líneas)

VERIFICACIÓN
  · Deploy
  · Test con curl de cada endpoint v2
  · Legacy sigue funcionando en paralelo (UI no migrada aún)
```

### 5.3 SESIÓN 3 · Migrar UI a V2 (~120 min)

```
StorageApp.svelte:
  · loadAll: cambiar 7 endpoints a /v2/*
  · submitImport → POST /v2/pools/import
  · submitDestroyOrphan → wipe via /v2/wipe
  · openWipeDialog → /v2/wipe
  · refreshObserved → GET /v2/observed?refresh

CreatePoolWizard.svelte:
  · POST /v2/pools (en lugar de legacy)
  · El modal de colisión sigue funcionando (mismo formato 409)

DestroyPoolWizard.svelte:
  · DELETE /v2/pools/{id}
  · O POST /v2/pool/destroy (compat by-name)

ExportPoolWizard.svelte:
  · POST /v2/pool/export

Settings.svelte:
  · Ya usa /v2/pools ✓

VERIFICACIÓN
  · Deploy con AMBOS stacks activos
  · La UI nueva solo apunta a /v2/*
  · Si algo falla → rollback UI con 1 commit
  · Test E2E completo en NAS real
```

### 5.4 SESIÓN 4 · Eliminar Legacy (~60 min)

```
Pre-flight:
  · grep recursivo en UI: 0 referencias a /api/storage/* (sin /v2/)
  · Si quedan, migrar antes de borrar

Eliminación:
  · Borrar storage_http.go (236 líneas)
  · Borrar funciones huérfanas que quedaran
  · Verificar que ningún archivo Go importa de storage_http.go
  · Eliminar route registration de http.go o main.go

VERIFICACIÓN FINAL
  · go build + vet + tests + race
  · Deploy
  · Test E2E full en NAS
  · Stack único, código limpio
```

---

## 6. RIESGOS Y MITIGACIONES

### Riesgo 1: Falsos positivos en huérfanas

```
Algunas funciones marcadas como huérfanas pueden tener uso:
  · Vía reflection
  · Llamadas indirectas con punteros a función
  · Tests que las consumen sin que el script las detecte

Mitigación: verificar cada una con grep + revisar manualmente
            antes de borrar
```

### Riesgo 2: Tests del legacy en cascada

```
Si la UI no migra todo a v2 perfectamente, tests E2E pueden fallar.

Mitigación: legacy queda corriendo en paralelo durante migración UI.
            Eliminación de legacy SOLO al final, con UI estable.
```

### Riesgo 3: Endpoints v2 con bugs latentes

```
17 endpoints v2 no han sido ejercidos en producción.
Pueden tener bugs ocultos.

Mitigación: Sesión 2 incluye tests E2E con curl antes de migrar UI.
            Cualquier bug se arregla con UI aún en legacy.
```

### Riesgo 4: .nimos-pool.json puede ser leído por algo no Go

```
Aunque ningún archivo Go lo lee, podría haber scripts externos,
backups, herramientas de migración que lo usen.

Mitigación: NO borrar los .nimos-pool.json existentes en pools.
            Solo dejar de generarlos para pools nuevos.
            Borrarlos físicamente solo cuando se destruya el pool.
```

---

## 7. DECISIONES PENDIENTES

### D1 · ¿Mantener `restorable` como compat hint?

Opciones:
- **A**: borrar completamente
- **B**: dejar respondiendo `{"deprecated": true, "use": "/observed"}`
- **C**: redirect 308 a /observed

Mi voto: **A**, simplicidad. UI ya está migrada a /observed.

### D2 · ¿Snapshots en v2 con repo o con funciones legacy?

Opciones:
- **A**: Handler v2 llama a `createSnapshot()` legacy (rápido, funciona)
- **B**: Refactorizar para que el service exponga `service.CreateSnapshot()` (puro)

Mi voto: **A** para Beta 8.1, **B** para Beta 9 cuando el service crezca.

### D3 · ¿Operaciones avanzadas (rename, set-compression, convert-profile) en UI?

V2 tiene endpoints para estas, legacy no, UI tampoco las usa.

Opciones:
- **A**: dejar handlers v2 sin uso UI (preparado para Beta 9)
- **B**: borrar handlers v2 también (YAGNI)

Mi voto: **A**, el código v2 está limpio, no estorba.

### D4 · ¿Eliminar el migrador `storage_migrate_json.go`?

Si todas las instalaciones en producción ya migraron, el migrador es muerto.

Opciones:
- **A**: dejar el migrador para usuarios que aún no actualizaron
- **B**: borrar — asumir que todo el mundo está al día

Mi voto: **A**, mantener al menos hasta Beta 9. Es código pequeño.

### D5 · ¿Mantener `enrichDisksWithSmart` o reemplazar?

Hay un comentario en storage_health.go diciendo "Full per-disk enrichment 
replacing enrichDisksWithSmart". Verificar si el reemplazo está completo.

Opciones:
- **A**: borrar si confirmado reemplazo
- **B**: mantener si todavía hay paths sin migrar

A verificar en Sesión 1 L3.

---

## 8. CONTEXTO PARA RETOMAR EN CHAT NUEVO

### Quick-start

```
Cuando retomes:

1. "Socio, retomamos NimOS Beta 8.1 Fase 7. 
    Sesión 1 — Limpieza pre-v2.
    Plan según doc consolidado."

2. Subir si tienes a mano:
   · storage_http.go (legacy actual)
   · storage_http_v2.go (v2 actual)
   · storage_btrfs_pool.go (con C3 aplicado)
   · storage_legacy_adapter.go
   · /home/claude/ui-revert/StorageApp.svelte
   · /home/claude/ui-revert/CreatePoolWizard.svelte

3. Empezar por verificación de huérfanas (L3) 
   antes de borrar nada
```

### Top of mind hoy

```
· test1 es el pool en producción, sustituye temporalmente a DATOS4
· Memoria de Andrés: rebautizar pool a algo decente en algún momento
· Cuando se elimine legacy completamente, el "primary_pool" en
  storage_metadata debe seguir apuntando al pool real
· Logs del daemon en /var/log/nimos/daemon-error.log (no journalctl)
· /var/log/nimos/daemon.log está vacío — todo va a error.log
  (cosa a arreglar en Beta 9 quizás)
```

### Lecciones aprendidas en esta sesión

```
1. Verificar siempre que el binario en producción tiene el código nuevo
   antes de teorizar bugs:
      sudo strings /opt/nimos/daemon/nimos-daemon | grep "string-única"

2. Los logs reales están en /var/log/nimos/daemon-error.log, NO en
   journalctl. journalctl solo ve los mensajes systemd.

3. Cuando añado funciones nuevas, verificar TODOS los imports antes
   de empaquetar (el bug jsonHdrs es por eso).

4. SQLite con MaxOpenConns=1 + scheduler + observer + adapter pueden
   crear contención. El patrón "ScanDevices ANTES del lock" es defensivo
   y debe mantenerse.

5. Calibrar valoraciones contra datos concretos, no adjetivos emocionales.
```

---

## 9. ARCHIVOS DE ESTA FASE

### Documentos generados (todos en /mnt/user-data/outputs)

```
docs_storage_observer/storage_observer_design.md
auditoria_storage/AUDITORIA_STORAGE.md
auditoria_storage_http/COMPARACION_LEGACY_V2.md
CIERRE_BLOQUE_C_Y_PLAN.md                ← este doc
```

### Otros docs storage del proyecto (referencia)

```
storage_api.md
storage_http_api.md
storage_invariants.md
storage_state_machines.md
nimos_beta8_storage_plan.md
beta8_fase5_addendum.md
```

### Outputs de código entregados (en sesiones anteriores)

```
bloque_a_zfs_cleanup/        (6 archivos)
bloque_b_legacy_adapter/     (3 archivos)
bloque_b_v2_storage_flow/    (2 archivos)
ui_revert_to_legacy/         (5 svelte)
bloque_c1_observer/          (6 archivos)
bloque_c2_preflight/         (3 archivos)
hotfix_deadlock_create/      (4 archivos)
bloque_c3_1_import/          (4 archivos)
bloque_c3_2_ui_observed/     (StorageApp.svelte)
bloque_c3_3_disk_indicators/ (StorageApp.svelte + CreatePoolWizard.svelte)
bloque_c3_4_wizard_collision/(4 archivos backend + UI)
hotfix_jsonHdrs/             (StorageApp.svelte)
```

---

**FIN DEL DOCUMENTO.**

Cuando empieces la próxima sesión, este doc es el punto único de continuidad. 🛠️
