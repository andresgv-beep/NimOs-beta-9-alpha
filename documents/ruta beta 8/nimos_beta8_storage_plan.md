# NimOS Beta 8 — Plan de Refactor Storage

**Autor**: Andrés + Claude Opus 4.7 — Mayo 2026
**Estado**: Plan basado en análisis directo del código actual
**Tiempo estimado**: 3 semanas de trabajo focado

---

## 0. Principio rector

> **NimOS es fiable y responsable de los archivos y discos del usuario.**

Esto no es un juguete. Cada decisión del plan se evalúa contra:

> *Si esto fallara en una caja con 8TB de fotos familiares dentro, ¿estoy tranquilo?*

Si la respuesta es "bueno, normalmente funciona" → no es suficiente.
Si la respuesta es "sí, está diseñado para fallar de forma segura" → adelante.

---

## 0.bis Regla de scope

> **Lo simple a Beta 8. Lo complejo a Beta 9.**

Toda decisión arquitectónica se evalúa contra: *¿se implementa en una tarde o requiere días?* Si lo segundo, va a la sección 16 (futuro). Mantiene el ámbito acotado y permite **arrancar**.

Beta 8 es ambicioso pero finito. Beta 9 hereda lo aprendido.

---

## 1. Principios de diseño (invariantes)

Estas reglas son **invariantes de código**, no sugerencias. Cualquier código que las viole es un bug:

### 1.1 JSON es payload, no entidad

JSON sólo para **payloads temporales** dentro de operaciones. Nunca como representación principal de una entidad.

✅ **Bueno**: `storage_operations.data` con `{"old_device": "...", "new_device": "...", "progress": 42}`
❌ **Malo**: un fichero JSON que define un pool entero

Las entidades viven en SQLite. JSON sólo aparece en campos `TEXT` que guardan parámetros de operaciones puntuales.

### 1.2 `current_path` es cache runtime, no identidad

`/dev/sdb` cambia entre reboots. **Ninguna función** puede usar `current_path` para resolver identidad. Sólo se usa para:
- Invocar comandos del sistema (`mkfs.btrfs /dev/sdb`)
- Mostrar al usuario

La **identidad** de un disco se resuelve por `by_id_path` o `device_id` interno.

### 1.3 Policy layer separado de Storage layer

Storage **ejecuta operaciones BTRFS**. Policy **decide qué operaciones están permitidas**. Dos capas distintas:

- Storage: `btrfsDeviceAdd(poolID, deviceID)` — ejecuta el comando
- Policy: `pool.Allows(OpAddDevice)` — decide si se puede

❌ **Prohibido**: `if pool.ControlState == "managed" && pool.Profile == "raid1" { ... }` disperso por el código
✅ **Obligatorio**: `if !pool.Allows(OpAddDevice) { return ErrNotPermitted }` centralizado

### 1.4 Metadata distribuida sobreviviente

La verdad sobre el sistema vive en **dos sitios redundantes**:
- **SQLite**: source of truth operacional
- **Identity files** `.nimos-pool.json` en cada pool: para portabilidad e import recovery

Si SQLite se corrompe, hay procedimiento de reconstrucción desde identity files (§9).

### 1.5 Errores con código semántico (no sólo texto)

Los errores que vienen del policy layer tienen un **código string** identificable, no sólo mensaje libre. Esto permite que frontend y tests reaccionen al tipo de error, no parseen texto.

✅ **Bueno**: `fmt.Errorf("%s: pool is observed, cannot mutate", ErrCodePoolObserved)`
❌ **Malo**: `errors.New("pool is observed, cannot mutate")`

Los códigos son constantes Go. En Beta 9 evolucionarán a tipos estructurados (§16).

---

## 2. Objetivo

Transformar el módulo de storage:

- **De**: 5627 líneas, ZFS+BTRFS mezclado, config JSON, identidad por `/dev/sdX`, 47 bugs documentados.
- **A**: ~1500 líneas, sólo BTRFS multi-device, SQLite como source of truth, identidad estable por `by-id`, journal persistido con eventos, recovery automático, policy layer separado.

---

## 3. Inventario actual

| Archivo | Líneas | Funciones | Acción |
|---|---|---|---|
| `storage_zfs_features.go` | 1090 | 28 | 🔧 Eliminar ZFS, renombrar a `storage_btrfs_features.go` |
| `storage_health.go` | 823 | 10 | ✅ Conservar (JOYA) |
| `storage_startup.go` | 729 | 17 | 🔧 Reescribir parcialmente |
| `storage_disk_mgmt.go` | 624 | 14 | 🔧 Reescribir parcialmente |
| `storage_wipe.go` | 522 | 10 | ✅ Conservar (con conexión a DB) |
| `storage_btrfs_pool.go` | 486 | 3 | 🔧 Ampliar (faltan ops) |
| `storage_zfs_pool.go` | 465 | 3 | ❌ Eliminar |
| `storage_pool_info.go` | 372 | 3 | 🔧 Solo BTRFS |
| `storage_http.go` | 198 | 1 | 🔧 Simplificar |
| `storage_common.go` | 198 | 7 | ✅ Conservar (con fixes) |
| `storage_config.go` | 120 | 6 | 🔧 Reescribir (migrar a SQLite) |
| **TOTAL** | **5627** | **102** | |

**Objetivo: ~1500 líneas, 45-50 funciones** (reducción del 73%).

---

## 4. Schema SQLite

Source of truth completo. El JSON `storage.json` desaparece.

### 4.1 `storage_metadata`

```sql
CREATE TABLE storage_metadata (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Claves usadas:
-- 'primary_pool'        → ID del pool principal
-- 'configured_at'       → timestamp del primer pool
-- 'schema_version'      → 2
-- 'global_generation'   → contador global (incrementa en cada mutación)
```

### 4.2 `storage_pools`

```sql
CREATE TABLE storage_pools (
    id TEXT PRIMARY KEY,                  -- UUID interno (estable)
    name TEXT NOT NULL UNIQUE,            -- nombre legible (puede cambiar)
    btrfs_uuid TEXT NOT NULL UNIQUE,      -- UUID del filesystem BTRFS
    profile TEXT NOT NULL,                -- single | raid1 | raid1c3 | raid10
    mount_point TEXT NOT NULL UNIQUE,
    role TEXT NOT NULL DEFAULT 'data'
        CHECK(role IN ('data', 'backup', 'cache', 'system')),
    -- Beta 8: todos los pools se crean con role='data' automáticamente.
    -- Valores reservados con consumidor planeado:
    --   'backup' → NimBackup lo usará como destino preferente
    --   'cache'  → tier SSD cache para pools de datos (Beta 10+)
    --   'system' → pool dedicado al SO, no expuesto al usuario
    -- El campo se rellena pero no se consume todavía en Beta 8.
    control_state TEXT NOT NULL DEFAULT 'managed'
        CHECK(control_state IN (
            'managed',    -- NimOS es dueño completo (Beta 8: implementado)
            'observed',   -- NimOS lo ve pero no lo toca (Beta 8: implementado)
            'imported',   -- viene de otro NimOS o de recovery (futuro)
            'foreign',    -- filesystem desconocido (futuro)
            'recovery'    -- en proceso de reconciliación (futuro)
        )),
    discovered_at TEXT,
    created_at TEXT NOT NULL,
    generation INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_pools_name ON storage_pools(name);
CREATE INDEX idx_pools_btrfs_uuid ON storage_pools(btrfs_uuid);
CREATE INDEX idx_pools_control_state ON storage_pools(control_state);
```

**Beta 8 runtime usa sólo `managed` y `observed`**. Los otros están en el schema para que añadirlos sea trivial cuando lleguen.

### 4.3 `storage_devices`

```sql
CREATE TABLE storage_devices (
    id TEXT PRIMARY KEY,                  -- UUID interno
    serial TEXT NOT NULL UNIQUE,          -- IDENTIDAD ABSOLUTA (firmware)
    by_id_path TEXT NOT NULL UNIQUE,      -- /dev/disk/by-id/ata-... (estable)
    current_path TEXT NOT NULL,           -- /dev/sdb (cache, cambia)
    wwn TEXT,                             -- identificador adicional (puede ser NULL)
    model TEXT,
    size_bytes INTEGER,
    last_seen_at TEXT,
    generation INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_devices_serial ON storage_devices(serial);
CREATE INDEX idx_devices_current_path ON storage_devices(current_path);
CREATE INDEX idx_devices_wwn ON storage_devices(wwn);
```

**Por qué `serial UNIQUE NOT NULL` y no opcional**:
- El serial es la única identidad absoluta del disco (grabada en firmware, no cambia)
- El `by_id_path` puede variar ligeramente entre controladoras SATA o tras kernel updates
- Si un disco no expone serial (USB baratos), NimOS no lo gestiona y muestra advertencia
- Cuando un disco se ve tras reboot, el matching se hace por serial primero, no por by_id_path

### 4.4 `storage_pool_devices`

```sql
CREATE TABLE storage_pool_devices (
    pool_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    added_at TEXT NOT NULL,

    PRIMARY KEY (pool_id, device_id),

    FOREIGN KEY (pool_id)
        REFERENCES storage_pools(id) ON DELETE CASCADE,
    FOREIGN KEY (device_id)
        REFERENCES storage_devices(id) ON DELETE RESTRICT
);
```

### 4.5 `storage_operations`

```sql
CREATE TABLE storage_operations (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,                   -- create_pool, add_device, etc.
    pool_id TEXT,
    status TEXT NOT NULL
        CHECK(status IN ('pending', 'in_progress', 'completed', 'failed', 'rolled_back')),
    started_at TEXT NOT NULL,
    completed_at TEXT,
    error TEXT,                           -- mensaje libre del error
    error_code TEXT,                      -- código semántico del error (ver §5)
    data TEXT,                            -- JSON payload temporal

    FOREIGN KEY (pool_id)
        REFERENCES storage_pools(id) ON DELETE SET NULL
);

CREATE INDEX idx_operations_status ON storage_operations(status);
CREATE INDEX idx_operations_pool_id ON storage_operations(pool_id);
CREATE INDEX idx_operations_started_at ON storage_operations(started_at DESC);
```

**`ON DELETE SET NULL`** en pool_id → histórico se conserva aunque el pool se destruya.

### 4.6 `storage_events`

```sql
CREATE TABLE storage_events (
    id TEXT PRIMARY KEY,
    operation_id TEXT NOT NULL,
    timestamp TEXT NOT NULL,
    level TEXT NOT NULL
        CHECK(level IN ('debug', 'info', 'warn', 'error')),
    message TEXT NOT NULL,

    FOREIGN KEY (operation_id)
        REFERENCES storage_operations(id) ON DELETE CASCADE
);

CREATE INDEX idx_events_operation ON storage_events(operation_id);
CREATE INDEX idx_events_timestamp ON storage_events(timestamp DESC);
```

### 4.7 `storage_pool_capabilities`

```sql
CREATE TABLE storage_pool_capabilities (
    pool_id TEXT NOT NULL,
    capability TEXT NOT NULL,

    PRIMARY KEY (pool_id, capability),
    FOREIGN KEY (pool_id)
        REFERENCES storage_pools(id) ON DELETE CASCADE
);

-- Capacidades posibles:
-- 'snapshots', 'balance', 'replace_device', 'add_device', 'remove_device',
-- 'convert_profile', 'scrub', 'compression', 'resize'
```

En Beta 8 todos los pools managed BTRFS se crean con el set completo automáticamente.

---

## 5. Policy Layer

Capa separada que decide qué operaciones permite un pool. Implementación simple en Beta 8 (`bool + código de error string`). Evolucionará a tipos estructurados en Beta 9 (§16).

```go
// Operaciones
type Operation string

const (
    OpDestroyPool       Operation = "destroy_pool"
    OpAddDevice         Operation = "add_device"
    OpRemoveDevice      Operation = "remove_device"
    OpReplaceDevice     Operation = "replace_device"
    OpConvertProfile    Operation = "convert_profile"
    OpStartScrub        Operation = "start_scrub"
    OpCreateSnapshot    Operation = "create_snapshot"
    OpDeleteSnapshot    Operation = "delete_snapshot"
)

// Estados de control
type ControlState string

const (
    ControlStateManaged  ControlState = "managed"
    ControlStateObserved ControlState = "observed"
    // Definidos para futuro: imported, foreign, recovery
)

// Códigos de error semánticos (constantes string)
// Se usan en error_code de storage_operations y en respuestas HTTP
const (
    ErrCodePoolObserved        = "pool_observed"
    ErrCodePoolNotFound        = "pool_not_found"
    ErrCodeCapabilityMissing   = "capability_missing"
    ErrCodeOperationInProgress = "operation_in_progress"
    ErrCodeDeviceInUse         = "device_in_use"
    ErrCodeDeviceMissing       = "device_missing"
    ErrCodeProfileInvalid      = "profile_invalid"
    ErrCodeInsufficientDisks   = "insufficient_disks"
    ErrCodeMinDisksReached     = "min_disks_reached"
    ErrCodeServicesActive      = "services_active"
)

// Allows: respuesta binaria simple (Beta 8)
func (p *Pool) Allows(op Operation) bool {
    if p.ControlState != ControlStateManaged {
        return false
    }
    return p.HasCapability(capabilityFor(op))
}

// Helpers
func (p *Pool) IsManaged() bool  { return p.ControlState == ControlStateManaged }
func (p *Pool) IsObserved() bool { return p.ControlState == ControlStateObserved }

func capabilityFor(op Operation) string {
    switch op {
    case OpAddDevice, OpRemoveDevice:
        return "device_management"
    case OpReplaceDevice:
        return "replace_device"
    case OpConvertProfile:
        return "convert_profile"
    case OpStartScrub:
        return "scrub"
    case OpCreateSnapshot, OpDeleteSnapshot:
        return "snapshots"
    case OpDestroyPool:
        return ""  // permitido por defecto si managed
    }
    return ""
}
```

**Uso en handlers HTTP**:

```go
func handleReplaceDevice(body) {
    pool := repo.GetPool(poolID)

    if !pool.Allows(OpReplaceDevice) {
        code := ErrCodeCapabilityMissing
        if pool.IsObserved() {
            code = ErrCodePoolObserved
        }
        return jsonErrorWithCode(403, code, "Operation not permitted on this pool")
    }
    // ... ejecutar
}
```

**Respuesta HTTP con código**:

```go
func jsonErrorWithCode(status int, code, message string) {
    return map[string]interface{}{
        "error": message,
        "code":  code,
    }
}
```

Frontend puede reaccionar por código sin parsear mensaje:

```javascript
if (response.code === "pool_observed") {
    showBanner("Este pool es de sólo lectura");
}
```

---

## 6. Lo bueno que tienes (conservar)

### `storage_health.go` (823 líneas)

Función pura `ComputePoolHealth()` como reducer. Sólo cambios:
- Eliminar `parseZpoolDiskStatus` (líneas 43-99)
- Simplificar `normalizeVdevType` (sin casos raidz)

### `storage_wipe.go` (522 líneas)

Sistema `Steps/Undo/Journal` con `runSteps()`. Conservar y **conectar a las nuevas tablas**: cada paso emite a `storage_events`, errores tipados con código a `storage_operations.error_code`.

### `storage_common.go` (198 líneas)

Conservar con **fixes**:
- `removeFstabEntry` (línea 81): parsear por campos en vez de `strings.Contains`
- `cleanOrphanPoolDirs` (línea 131): guard antes de `os.RemoveAll`

---

## 7. Lo que se elimina

### `storage_zfs_pool.go` — DELETE completo (465 líneas)

### Mitad de `storage_zfs_features.go` — DELETE ~600 líneas

**Conservar** (BTRFS o genérico): `btrfsSnapshotCreate/Destroy`, `getBtrfsScrubStatus`, `formatDuration`, `bodyFloat`, todo el scrub scheduler.

**Eliminar**: `zfsSnapshotCreate/Destroy`, `resolveZpoolName`, `getZfsScrubStatus`, `parseZfsSize`.

**Reestructurar sólo BTRFS**: `listSnapshots`, `createSnapshot`, `deleteSnapshot`, `rollbackSnapshot`, `startScrub`, `getScrubStatus`, `listDatasets/createDataset/deleteDataset` (→ subvolumes).

Renombrar archivo a `storage_btrfs_features.go`.

### Funciones ZFS en `storage_disk_mgmt.go` — DELETE ~200 líneas

`detachDiskZfs`, `attachDiskZfs`, `replaceDiskZfs`, `getResilverStatus` (→ `getBalanceStatus`).
Eliminar switch `case "zfs"/"btrfs"` en handlers. Llamada directa BTRFS.

### Funciones ZFS en `storage_startup.go`

`zfsAutoImportOnStartup`, `startZfsScheduler` → DELETE.
`scanForRestorablePoolsGo` → reescribir sólo BTRFS.
`detectStorageDisksGo` → reescribir usando by-id y SQLite.

### Funciones ZFS en `storage_pool_info.go`

`getZfsPoolInfo` (200 líneas), `parseZpoolDiskStatus` → DELETE.

---

## 8. Lo que se amplía (BTRFS multi-device)

### En `storage_btrfs_pool.go`

**Operaciones nuevas**:
1. `addDeviceToFilesystem(poolID, deviceID)` — `btrfs device add` online
2. `removeDeviceFromFilesystem(poolID, deviceID)` — `btrfs device remove` + rebalance
3. `convertProfile(poolID, newProfile)` — `btrfs balance start -dconvert=X -mconvert=X`
4. `replaceFailedDevice(poolID, oldDeviceID, newDeviceID)` — `btrfs replace start`

**Profiles soportados**:

| Profile | Min disks | Capacidad | Tolerancia |
|---|---|---|---|
| `single` | 1 | 100% | Ninguna |
| `raid1` | 2 | 50% | 1 disco |
| `raid1c3` | 3 | 33% | 2 discos |
| `raid10` | 4 | 50% | 1 disco por par |

**NO soportados**: raid5, raid6, raid1c4.

### En `storage_btrfs_features.go`

**Nuevas**: `getBalanceStatus(poolID)`, `pauseBalance(poolID)`, `resumeBalance(poolID)`.

---

## 9. Procedimiento de recovery desde identity files

Si SQLite se corrompe, NimOS puede reconstruir state escaneando pools físicos.

**Algoritmo**:
1. Escanear discos físicos vía `lsblk -J -o NAME,UUID,FSTYPE`
2. Para cada disco con `FSTYPE=btrfs`:
   a. Montar temporalmente en `/mnt/nimos-recovery/<uuid>`
   b. Leer `.nimos-pool.json` del root
   c. Si existe: insertar pool en DB con `control_state='imported'`, `discovered_at=now`
   d. Si no existe: insertar como `control_state='foreign'`
   e. Desmontar
3. El usuario revisa la lista en UI y decide qué adoptar

**Se documenta pero NO se implementa en Beta 8**. Schema preparado.

---

## 10. Mapa de dependencias

### Llamadas DESDE storage al resto del daemon

```
storage → notifications.go  (addNotification)
storage → shares.go         (dbSharesCreate, dbSharesDelete, dbSharesListRaw)
storage → services.go       (checkPoolDependencies, canDestroyPool, dbServiceDeleteByPool)
storage → http.go           (handleOp para share.delete)
storage → hardware.go       (hasBtrfs)
storage → main.go           (smartHistory, smartMu)
storage → db.go             (handle SQLite)
```

### Contrato público a preservar

```go
getStorageConfigFull() map[string]interface{}   // ahora lee de SQLite
saveStorageConfigFull(config)                    // ahora escribe a SQLite
hasPoolGo() bool
getStoragePoolsGo() []map[string]interface{}
detectStorageDisksGo() map[string]interface{}
```

`docker.go`, `shares.go`, `services.go` no necesitan cambios.

---

## 11. Plan de ejecución en 6 fases — 3 semanas

### Fase 1 — Preparación y diseño (1-2 días)

- Crear rama `beta8/storage-refactor`
- Documento de firmas Go y schema definitivo
- Diseñar API HTTP final

### Fase 2 — Schema SQLite + capa de acceso (3-4 días)

Día 1:
- `CREATE TABLE` de las 7 tablas en `db.go`
- `initStorageSchema()` con `IF NOT EXISTS`
- Inicializar metadata (`schema_version=2`, `global_generation=0`)

Día 2:
- `storage_repo.go` — capa de acceso con métodos por tabla
- Transacciones explícitas en operaciones multi-tabla
- Helper `incrementGeneration()` en cada mutación

Día 3:
- `storage_policy.go` — constantes Operation, ControlState, ErrCode*
- `Pool.Allows()`, `Pool.HasCapability()`, helpers
- `jsonErrorWithCode()` en HTTP

Día 4:
- Tests manuales del repo y policy con DB en `/tmp`
- Verificar FK, cascades, CHECK constraints, índices

### Fase 3 — Core BTRFS multi-device (6 días)

Día 1: `scanAndRegisterDevices()` — escanea `/dev/disk/by-id/`, llena `storage_devices`
Día 2: `createFilesystemBtrfs` con persistencia DB + journal + events
Día 3: `addDeviceToFilesystem`, `removeDeviceFromFilesystem`
Día 4: `convertProfile`, `replaceFailedDevice` (sin el bug del `wipefs`)
Día 5: `destroyFilesystemBtrfs`, `exportFilesystemBtrfs`
Día 6: Tests manuales con discos loopback

### Fase 4 — Recovery + monitoring (2-3 días)

Día 1:
- `recoverPendingOperations()` al arranque
- `reconcileDevicesAtBoot()` — actualiza `current_path`
- Adaptar journal de `runSteps` para escribir a `storage_operations` + `storage_events`

Día 2:
- Background loop que actualiza `last_seen_at`
- Adaptar `storage_health.go` para usar DB
- Detección de pools observed (BTRFS no creado por NimOS)

Día 3:
- Testing manual de escenarios de recovery (matar daemon a media operación)

### Fase 5 — Limpieza ZFS + bugs (3 días)

Día 1:
- `git rm storage_zfs_pool.go`
- Eliminar ZFS de `storage_zfs_features.go` y renombrar
- Eliminar ZFS de `storage_disk_mgmt.go`

Día 2:
- Eliminar `zfsAutoImportOnStartup`, `startZfsScheduler`, `getZfsPoolInfo`
- Eliminar `hasZfs` y referencias
- Verificar compilación limpia

Día 3:
- Bugfix `removeFstabEntry` (parsear por campos)
- Bugfix `cleanOrphanPoolDirs` (guard)
- Bugfix `smartMu.Lock` → `RLock` en lecturas

### Fase 6 — Frontend (4 días)

Día 1:
- `CreatePoolWizard.svelte` — eliminar ZFS/raidz, añadir raid1c3
- Wizard 3 pasos: discos (by-id visible) → profile → confirmación

Día 2:
- `StorageApp.svelte` — eliminar ZFS
- Controles: "Añadir disco", "Reemplazar disco", "Convertir profile"
- Distinción visual managed vs observed

Día 3:
- **Activity timeline** usando `storage_operations` + `storage_events`
- Modal de operaciones con preview de capacidad
- Indicador de balance/scrub en progreso

Día 4:
- Testing manual end-to-end
- Errores en frontend reaccionan a `code`, no parsean `message`

---

## 12. Riesgos identificados

### Riesgo 1: SQLite corrupto

**Mitigación**: WAL mode. Identity files permiten reconstrucción (§9).

### Riesgo 2: BTRFS multi-device bugs propios

**Mitigación**: Fase 3 con testing loopback antes de frontend.

### Riesgo 3: Identidad by-id no disponible

**Mitigación**: fallback wwn → serial → current_path con alerta visible.

### Riesgo 4: Agotamiento

**Mitigación**: 2-3h/día, no maratones. Si 3 sesiones frustrantes seguidas → parar y revisar.

**Este es el riesgo más importante. Más que cualquier bug técnico.**

---

## 13. Lo que NO está en este plan

- Apps de "internet offline" (YouTube local, Wikipedia)
- Separación de nodos cómputo/storage
- Orquestación host-agent / cluster
- NIMA / asistente AI integrado
- mdadm como capa intermedia
- Soporte filesystems no-BTRFS (ext4, xfs)
- Estados `imported`, `foreign`, `recovery` — schema preparado, runtime no
- Recovery desde identity files — documentado, no implementado
- Adopción observed → managed — schema lo permite, UI no

Todo esto va al archivo `nimos_ideas_futuras.md`.

---

## 14. Criterio de "terminado" para Beta 8

- [ ] Daemon compila sin warnings ZFS
- [ ] `wc -l storage_*.go` < 1700 líneas
- [ ] Source of truth en SQLite, no `storage.json`
- [ ] Discos por `by-id`, fallback a wwn/serial
- [ ] Policy layer centralizada (`pool.Allows()`)
- [ ] Códigos de error semánticos (constantes string)
- [ ] `control_state` modelado (runtime `managed`/`observed`)
- [ ] Capabilities en tabla separada
- [ ] Crear single, raid1, raid1c3, raid10
- [ ] Añadir disco a filesystem existente
- [ ] Reemplazar disco fallido sin perder datos
- [ ] Convertir profiles online
- [ ] Operaciones en `storage_operations` con código de error
- [ ] Eventos en `storage_events`
- [ ] Recovery automático tras reinicio
- [ ] `storage_devices` mantiene histórico
- [ ] `generation` se incrementa en cada mutación
- [ ] UI activity timeline
- [ ] UI distingue managed vs observed
- [ ] Bugs críticos arreglados
- [ ] Frontend sin opciones ZFS
- [ ] Documentación: "BTRFS only, by-id stable, SQLite-backed, policy-driven"

---

## 15. Filosofía

**NimOS es fiable y responsable de los archivos y discos del usuario.**

No es un juguete. Aunque hoy sólo lo uses tú, gestiona datos reales con responsabilidad real.

El refactor de Beta 8 sube el nivel de NimOS de:
- "Código que ejecuta comandos BTRFS y los guarda en JSON"

a:
- "Sistema con conceptos separados: entidad, capacidad, autoridad, operación, evento."

Eso es **policy layer**. Eso es **arquitectura para crecer**.

Si esto fallara con 8TB de fotos familiares dentro, **estamos tranquilos**.

---

## 15.bis Decisiones de diseño finales (añadidas la noche del 11 de mayo)

Decisiones técnicas adicionales que se aplican a la implementación de Fases 1-3. Apuntadas para no perderlas:

### 15.bis.1 — Queries vs Commands (Operation pattern)

Las mutaciones largas **NO devuelven `error` solo**. Devuelven `*Operation` con el registro ya creado en `storage_operations`, en estado `pending` o `in_progress`.

**Queries** (lecturas) → devuelven entidades:

```go
func (s *StorageService) GetPool(ctx context.Context, id string) (*Pool, error)
func (s *StorageService) ListDevices(ctx context.Context) ([]Device, error)
func (s *StorageService) GetOperation(ctx context.Context, opID string) (*Operation, error)
```

**Commands** (mutaciones) → devuelven la operación creada:

```go
func (s *StorageService) CreatePool(ctx context.Context, spec PoolSpec) (*Operation, error)
func (s *StorageService) AddDeviceToPool(ctx context.Context, poolID, deviceID string) (*Operation, error)
func (s *StorageService) ReplaceDevice(ctx context.Context, poolID, oldID, newID string) (*Operation, error)
func (s *StorageService) ConvertProfile(ctx context.Context, poolID, newProfile string) (*Operation, error)
func (s *StorageService) StartScrub(ctx context.Context, poolID string) (*Operation, error)
```

**Por qué**: NimOS ya tiene infraestructura async (`storage_operations`, `storage_events`, timeline, recovery). Las mutaciones importantes son **long-running** por naturaleza (replace, scrub, convert, rebalance). Modelar todas como Operations desde el principio mantiene coherencia API/frontend/scheduler.

**Respuesta HTTP**:

```json
{
  "operation": {
    "id": "op-abc123",
    "type": "replace_device",
    "status": "running",
    "started_at": "2026-05-11T22:30:00Z"
  }
}
```

El frontend hace polling de `GET /api/storage/operations/:id` para ver progreso.

### 15.bis.2 — API REST por recursos (no RPC)

La API se diseña alrededor de **recursos**, no de acciones tipo shell.

**Pools**:

```
GET    /api/storage/pools             — Lista
GET    /api/storage/pools/:id         — Detalle
POST   /api/storage/pools             — Crear (devuelve Operation)
DELETE /api/storage/pools/:id         — Destruir (devuelve Operation)
```

**Devices**:

```
GET    /api/storage/devices           — Lista discos detectados
GET    /api/storage/devices/:id       — Detalle
```

**Mutaciones como subrecursos**:

```
POST   /api/storage/pools/:id/devices              — Añadir disco al pool
DELETE /api/storage/pools/:id/devices/:deviceID    — Quitar disco del pool
POST   /api/storage/pools/:id/replace              — Reemplazar disco (body con old/new)
POST   /api/storage/pools/:id/profile              — Convertir profile (body con newProfile)
POST   /api/storage/pools/:id/scrub                — Iniciar scrub
POST   /api/storage/pools/:id/snapshots            — Crear snapshot
```

**Operations**:

```
GET    /api/storage/operations                — Lista (con filtros)
GET    /api/storage/operations/:id             — Detalle (incluye events)
POST   /api/storage/operations/:id/cancel      — Cancelar (si aplica)
```

**Regla**: ninguna respuesta es `{"success": true}` a secas. Siempre devuelve:
- Entidad actualizada (en queries/updates síncronos)
- Operation creada (en commands largos)

### 15.bis.3 — SQLite: `PRAGMA foreign_keys = ON` obligatorio

**Crítico**: SQLite **no aplica foreign keys por defecto**. Hay que activarlas explícitamente en cada conexión.

En `db.go`, al abrir la conexión:

```go
db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=10000&_foreign_keys=ON")
```

O explícitamente tras abrir:

```go
db.Exec("PRAGMA foreign_keys = ON;")
```

**Verificar en tests** que los FK funcionan:

```go
// Debería fallar: intenta insertar pool_device con pool_id inexistente
_, err := db.Exec(`INSERT INTO storage_pool_devices (pool_id, device_id, added_at) VALUES ('fake', 'fake', ?)`, now)
assert.Error(t, err, "FK should have rejected this")
```

Sin esto, los CASCADE y RESTRICT del schema son **decorativos**. Es un error silencioso clásico de SQLite.

### 15.bis.4 — State machines documentadas

Aunque las transiciones de estado están implícitas en el código, documentarlas en una página corta es valioso para:
- Onboarding futuro (si algún día hay más manos)
- Tests que verifican que no se permitan transiciones inválidas
- Debugging cuando algo está en un estado raro

**Pool lifecycle**:

```
[detected] ──(no existe en DB)──→ observed
              ──(adopta el usuario)──→ managed
              ──(error filesystem)──→ foreign

[managed]  ──(usuario libera)──→ observed
           ──(crash recovery)──→ recovery
           ──(destroy)──→ [removed]
```

**Operation lifecycle**:

```
pending ──→ in_progress ──→ completed
                       ──→ failed ──(rollback)──→ rolled_back
                       ──(usuario cancela)──→ cancelled
```

**Device lifecycle**:

```
[unknown] ──(detected)──→ detected
detected  ──(añadido a pool)──→ assigned
assigned  ──(desaparece del bus)──→ missing
missing   ──(reaparece)──→ assigned
assigned  ──(reemplazado)──→ replaced
assigned  ──(quitado)──→ detected
```

Estos diagramas viven en `docs/storage_state_machines.md` (corto, una página).

**Beta 8 implementa**: las transiciones necesarias para `managed` y `observed` en Pool, todas las de Operation, y todas las de Device excepto `replaced` (que es Beta 9 cuando se haga reconciliación profunda).

---

## 16. Roadmap futuro (Beta 9 y posterior)

Cosas que se han discutido durante el diseño y **NO entran en Beta 8 por ámbito**, pero el schema y los principios actuales **las soportan sin migración**.

### Beta 9 / Beta 10 — Consumidores de `role`

El campo `role` del schema `storage_pools` está reservado para los siguientes consumidores planeados. Beta 8 lo rellena con `'data'` por defecto pero ninguno de los consumidores está implementado todavía:

**NimBackup (Beta 9)**: la app NimBackup filtrará pools por `role='backup'` para presentarlos como destinos preferentes. El usuario podrá marcar un pool como backup desde la UI y NimBackup lo usará automáticamente.

**Nodos remotos (Beta 10+)**: cuando un nodo NimOS se conecta como sirviente y expone su pool al anfitrión, el `role='backup'` indica al sistema principal que ese pool remoto es para backups, no para datos primarios.

**Cache tiering (Beta 10+)**: pools con `role='cache'` se montarían como tier SSD frente a pools `role='data'`. Aceleración transparente de lecturas.

**Pool del sistema (futuro)**: `role='system'` se reserva para un pool donde NimOS mismo guarda su estado (DB, configs). No expuesto en UI de usuario final.

**Importante**: estos consumidores se documentan en el código actual con comentarios `TODO(beta9)`, `TODO(beta10)` en `storage_repo.go` cerca de la definición del tipo `Role`. Cuando llegue el momento de implementarlos, los TODOs son el punto de partida.

### Beta 9 — Policy layer estructurada

Evolucionar `Allows() bool + ErrCode string` a `CheckPermission() *PolicyError`:

```go
type PolicyError struct {
    Code    PolicyErrorCode
    Message string
    Details map[string]any
}

func (p *Pool) CheckPermission(op Operation) *PolicyError {
    // Devuelve nil si permitido
    // Devuelve PolicyError con código + detalles si no
}
```

**Por qué Beta 9 y no Beta 8**: la migración es mecánica (find-and-replace asistido) y los códigos string ya existen. Cuando hagan falta detalles ricos (qué balance está activo, qué servicios bloquean, qué redundancia falta), se hace en un día.

### Beta 9 — Estados de control extendidos

Activar runtime de `imported`, `foreign`, `recovery`. Implementar el procedimiento de recovery desde identity files (§9).

### Beta 9 — Filesystems adicionales

Añadir soporte de ext4, xfs externos (sólo observed). El schema con `control_state` y `capabilities` ya lo soporta.

### Beta 10+ — Multi-nodo

Si llegan ideas como:
- Internet offline (YouTube local, Wikipedia, archive web)
- Separación cómputo/storage en nodos
- Orquestación host-agent
- NIMA integrado

Tienen su propio diseño separado y no comprometen la arquitectura de storage actual.

---

*Plan creado a partir del análisis directo del código actual de NimOS Beta 8.
Schema y arquitectura diseñados con Andrés. Documento generado por Claude Opus 4.7.*
