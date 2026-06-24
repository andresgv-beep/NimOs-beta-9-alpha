# Storage Observer — Documento de diseño

**Fase 7 Bloque C** — NimOS Beta 8.1+ → camino a Beta 9

**Fecha**: Mayo 2026
**Estado**: Aprobado, pendiente implementación

---

## 1. Contexto: por qué existe

### El problema observado

En Beta 8.1 (post Bloques A+B) el storage tiene **una sola fuente de verdad**:
SQLite (`storage_pools`, `storage_devices`, `storage_pool_devices`).

Pero la realidad física del sistema puede divergir de lo declarado:

- Pool en SQLite con disco ausente físicamente (cable suelto, disco extraído)
- BTRFS filesystem en discos sin entrada en SQLite (pool importable)
- Pool desmontado: SQLite limpio pero datos en discos
- Pool degradado: SQLite dice "raid1 con 2 discos", el filesystem solo ve 1

**El usuario actualmente**: no puede ver esa divergencia. La UI muestra solo lo
managed. Si desmonta un pool, "desaparece" — aunque los datos siguen.

**Más grave**: si va a crear un pool sobre discos con BTRFS no registrado,
NimOS hace `wipefs` silenciosamente. Riesgo de pérdida de datos.

### Lección de Beta 7 / 8: no más "recoverable"

Beta 7 tenía un concepto "recoverable" que sugería "algo roto". El usuario
veía esa palabra y entraba en pánico cuando muchas veces nada estaba roto.

**Renombramos al modelo correcto**:

```
Managed State (SQLite)        Observed State (runtime truth)
       │                              │
       └──────── Divergence ──────────┘
```

NO es "algo roto vs no roto". Son **dos vistas de la misma realidad**
que pueden coincidir o no, y el sistema le muestra al usuario qué pasa.

---

## 2. Modelo conceptual

### Managed State

Lo que NimOS **declara que existe**. Vive en SQLite:

- `storage_pools`: pools que NimOS gestiona
- `storage_devices`: discos que NimOS ha registrado
- `storage_pool_devices`: relación N:M

Cambia solo por **acciones explícitas** del usuario (crear pool, destruir,
attach disk, etc.). Es transaccional.

### Observed State

Lo que **realmente existe en el sistema físico**. Vive in-memory:

- Filesystems BTRFS detectados (vía `btrfs filesystem show`)
- Devices físicos detectados (vía `lsblk`, `/sys/block`)
- Mount table actual (vía `/proc/self/mounts`)

Cambia por **eventos del mundo físico**: cables, USB, mounts, mkfs externo.
NO se persiste — es caché de runtime truth.

### Divergence Analysis

Función pura que toma Managed + Observed y produce divergencias:

```
divergences = analyze(managed, observed)
```

Tipos de divergencia:

| Tipo | Condición | Severidad | Acción típica |
|---|---|---|---|
| `pool_missing_device` | pool managed con N devices, observed ve N-k | warning/critical según N-k | reconectar disco / replace |
| `orphan_filesystem` | BTRFS observed sin pool managed que lo cubra | info | import o destroy |
| `unexpected_io_errors` | observed reporta IO errors en devices managed | warning | check SMART |
| `pool_unmounted` | pool managed con mount_point pero no mounted | info | mount manual o destroy |
| `profile_mismatch` | profile declarado != profile real | critical | inspección manual (raro) |

---

## 3. ObservationHealth — el estado por filesystem

Cada `ObservedBtrfs` lleva su propio health, calculado al hacer snapshot:

| Estado | Criterio | Significado UX |
|---|---|---|
| `healthy` | devices_online == devices_expected, sin IO errors | "Todo bien" verde |
| `incomplete` | devices_online < devices_expected, BTRFS reporta "missing" | "Falta 1 de 2 discos" amarillo |
| `degraded` | devices_online == expected pero hay IO errors registrados | "Funcionando con errores" amarillo |
| `partial` | devices presentes pero filesystem no monta | "Estado raro, intervención" rojo |
| `unknown` | comandos BTRFS no responden o sin permisos | gris |

```go
func computeObservationHealth(o *ObservedBtrfs) string {
    if !o.CanProbe {
        return "unknown"
    }
    if o.DevicesOnline < o.DevicesExpected {
        return "incomplete"
    }
    if o.IOErrorCount > 0 {
        return "degraded"
    }
    if !o.IsMounted && o.HasMountPoint {
        return "partial"
    }
    return "healthy"
}
```

---

## 4. Arquitectura del Observer

```
┌──────────────────────────────────────────────────────────────────┐
│                       StorageObserver                             │
│                                                                   │
│  ┌────────────────┐    ┌──────────────────┐                     │
│  │ Periodic ticker│    │  triggerCh       │ ← InvalidateNow()   │
│  │   60s          │    │  buffered=1      │                     │
│  └────────┬───────┘    └──────────┬───────┘                     │
│           │                       │                              │
│           └────────────┬──────────┘                              │
│                        ▼                                          │
│         ┌─────────────────────────────────┐                      │
│         │   tryReconcile()                 │                      │
│         │   (single-flight via mu)         │                      │
│         │                                  │                      │
│         │   1. fingerprint = compute()    │                      │
│         │   2. if fingerprint == last:    │                      │
│         │        return                    │ ← cheap skip         │
│         │   3. else:                       │                      │
│         │        snap = fullScan()         │                      │
│         │        divergences = analyze()  │                      │
│         │        atomic.Store(snap)       │                      │
│         │        generation++              │                      │
│         │        notify(eventCh)           │ ← futuro             │
│         └─────────────────────────────────┘                      │
│                                                                   │
│                   ↑ atomic.Pointer ↑                              │
│         GET /api/storage/observed → snap                          │
└──────────────────────────────────────────────────────────────────┘
```

### Fingerprinting barato

Coste objetivo: <10ms en sistemas con muchos discos.

```go
type Fingerprint struct {
    // Hash sha256 de:
    //   - lista ordenada de /sys/block (excluye loop, ram)
    //   - contenido de /proc/self/mounts
    //   - mtime de /run/blkid/blkid.tab (si existe)
    Hash [32]byte
}

func computeFingerprint() Fingerprint
```

Si el fingerprint no cambia, **skip del scan caro** (que cuesta 200-2000ms).

### Triggers

Tres orígenes que disparan reconcile:

| Origen | Latencia | Coste |
|---|---|---|
| Periodic 60s | máx 60s | scan si fingerprint cambió |
| `InvalidateNow()` interno | inmediato | scan forzado completo |
| `?refresh=true` en endpoint | inmediato | scan forzado completo |

### Puntos de invalidación (interno)

Todos llaman a `notifyStorageChanged()` que llama a `observer.InvalidateNow()`:

- `createPoolBtrfs` tras `create_dirs_and_config`
- `destroyPoolBtrfs` tras unmount + wipe
- `exportPoolBtrfs` tras unmount
- `attachDiskToPool` (futuro)
- `detachDiskFromPool` (futuro)
- `wipeDiskGo` tras wipefs
- Storage scheduler reconciler (cuando detecta cambios)

### Concurrencia

- **Lecturas**: `atomic.Pointer[ObservedSnapshot]`. Lock-free.
- **Escrituras (reconcile)**: `mu sync.Mutex` con `TryLock()`. Si está bloqueado, salta. No spawnea scans paralelos.
- **Trigger channel**: `chan struct{}, buffered=1` con send no-blocking. Drops dups.

---

## 5. Estructuras de datos

```go
// ObservedSnapshot — la "foto" del observed state en un instante.
// Inmutable una vez creada. La UI/handlers leen via atomic.Load.
type ObservedSnapshot struct {
    Generation  uint64
    Timestamp   time.Time

    Filesystems  []ObservedBtrfs   // BTRFS detectados (con o sin managed)
    LooseDevices []ObservedDevice  // discos sin filesystem útil
    Divergences  []Divergence      // analysis pre-computado

    // Métricas internas
    ScanDurationMs int64
    FingerprintHash [32]byte
}

// ObservedBtrfs — un filesystem BTRFS detectado en el sistema.
type ObservedBtrfs struct {
    UUID        string           // UUID del filesystem
    Label       string           // label si lo tiene
    Profile     string           // raid1/single/raid10/etc (data profile)
    MetaProfile string           // metadata profile (suele coincidir)

    Devices            []ObservedDevice  // miembros del filesystem
    DevicesExpected    int               // total expected (de btrfs filesystem show)
    DevicesOnline      int               // visibles físicamente
    DevicesMissing     int               // expected - online

    IsMounted     bool
    MountPoint    string  // si está montado
    HasMountPoint bool    // si esperaríamos que monte (heurística)

    SizeBytes     int64
    UsedBytes     int64
    FreeBytes     int64   // statfs real, no estimated

    IOErrorCount  int64   // total errors agregado de todos los devices

    // Cruce con managed
    IsManaged     bool    // ¿hay pool en SQLite que cubre este filesystem?
    ManagedPoolID string  // si IsManaged=true
    ManagedPoolName string

    // Estado computado
    ObservationHealth string  // healthy/incomplete/degraded/partial/unknown

    // Diagnóstico
    CanProbe      bool    // true si todos los comandos respondieron
    LastSeen      time.Time
}

type ObservedDevice struct {
    Path       string  // /dev/sda
    ByIDPath   string  // /dev/disk/by-id/...
    SizeBytes  int64
    InFS       string  // UUID del FS al que pertenece, si pertenece a alguno
    IOErrors   int64   // errors específicos de este device
    Present    bool    // visible físicamente AHORA
}

type Divergence struct {
    Type      string  // "pool_missing_device" / "orphan_filesystem" / ...
    Severity  string  // "info" / "warning" / "critical"

    PoolID    string  // si la divergencia afecta a un pool managed
    PoolName  string

    Detail    string  // mensaje legible para usuario
    Hint      string  // sugerencia de acción
}
```

---

## 6. API HTTP

### `GET /api/storage/observed`

```json
{
  "generation": 47,
  "timestamp": "2026-05-17T18:00:00Z",
  "filesystems": [
    {
      "uuid": "884ec939-...",
      "label": "DATOS4",
      "profile": "raid1",
      "devices_expected": 2,
      "devices_online": 2,
      "is_mounted": true,
      "mount_point": "/nimos/pools/DATOS4",
      "size_bytes": 119123456789,
      "used_bytes": 335872,
      "free_bytes": 119123120917,
      "is_managed": true,
      "managed_pool_id": "abc-uuid",
      "managed_pool_name": "DATOS4",
      "observation_health": "healthy",
      "can_probe": true,
      "last_seen": "2026-05-17T18:00:00Z"
    }
  ],
  "loose_devices": [],
  "divergences": []
}
```

### `GET /api/storage/observed?refresh=true`

Fuerza scan inmediato (escape hatch para el "refresh button" de la UI).

### Long polling futuro (no en C1)

```
GET /api/storage/observed?since_generation=47

Si generation actual > 47:
  200 OK con snapshot
Si generation actual == 47:
  304 Not Modified (esperar)
```

---

## 7. Decisiones clave (rationale)

### ¿Por qué 60s y no 30s?

Sistemas con muchos discos: `blkid` + `lsblk` + `btrfs filesystem show` pueden
costar 200-2000ms. A 30s eso es ruido constante de I/O y wake-ups.

Pero polling-only siempre se siente laggy. Por eso combinamos:

- **60s baseline** = safety net
- **InvalidateNow() en ops internas** = instantáneo cuando NimOS sabe que cambió
- **Manual refresh button** = escape hatch para el usuario

### ¿Por qué fingerprint antes del scan?

Sin fingerprint, scan cuesta 200-2000ms cada vez. Con fingerprint, 95% de los
ciclos terminan en <10ms porque nada cambió.

Esto escala. De 2 discos hoy a 50+ discos mañana sin sufrir.

### ¿Por qué in-memory y no SQLite?

El observed state es runtime truth. Cambia rápido por eventos externos
(cable suelto, USB conectado). No queremos churn de escrituras a SQLite
por cambios transitorios.

Además: tras reboot el observer rescanea al arranque. La caché reaparece
naturalmente. No hace falta persistencia.

### ¿Por qué buffered=1 + drop?

Si entre `InvalidateNow()` y el siguiente scan llegan 10 invalidations
más, solo necesitamos **un** scan que las cubre todas. Drop evita work
duplicado.

### ¿Por qué atomic.Pointer y no RWMutex?

Las lecturas son MUY frecuentes (cada vez que la UI consulta).
Atomic.Pointer da lecturas lock-free. Solo el reconcile bloquea.

### ¿Por qué divergence analysis pre-computado?

Calcular divergencias en el snapshot (no on-demand) hace que:
- Las lecturas del endpoint sean baratas
- La lógica de análisis vive en un solo sitio
- El testing es determinista

---

## 8. Plan de implementación (bloques)

### Bloque C1 — Observer base

Archivos:
- `storage_observe_types.go` — structs (ObservedSnapshot, ObservedBtrfs, etc.)
- `storage_btrfs_probe.go` — funciones que ejecutan los comandos btrfs/blkid
- `storage_observer.go` — el loop con fingerprint + atomic snapshot
- `main.go` — startObserver al boot, stopObserver al shutdown
- `storage_http.go` — `GET /api/storage/observed`
- `storage_observer_test.go` — fingerprint determinista, concurrencia, triggers

Tras C1:
- Endpoint responde con snapshot real
- 0 races (verificado con -race)
- Tests verdes
- UI no toca todavía

**Punto de control**: `curl /api/storage/observed` muestra el filesystem real.

### Bloque C2 — Pre-flight check enriquecido

- Tipo `ErrDiskHasFilesystem` con contexto observed
- `preFlightCheck` consulta el observer cache
- `createPoolBtrfs` maneja error tipado, propaga a UI
- `notifyStorageChanged()` integrado en create/destroy/export/wipe

Tras C2:
- Intentar crear pool sobre disco con BTRFS devuelve error rico
- Wipe / create / destroy invalidan observer instantáneamente

### Bloque C3 — UI con modelo Managed/Observed

- Sección "Observados" en StorageApp cuando hay divergence
- Wizard de creación con doble intención (importar vs destruir)
- Confirmación fuerte para destrucción de FS existente
- Refresh button en barra de Storage

Tras C3:
- Usuario nunca pierde datos por accidente
- Modelo mental claro en pantalla

---

## 9. Anti-patrones explícitos (NO hacer)

- ❌ Scan completo cada 30s sin fingerprint
- ❌ RWMutex en lecturas del snapshot (uso atomic.Pointer)
- ❌ Re-ejecutar `blkid` en cada lectura del endpoint
- ❌ Bloquear daemon shutdown esperando un scan en curso
- ❌ Persistir el snapshot en SQLite
- ❌ Llamar concepto "recoverable" en backend o UI
- ❌ Hacer wipe automático sin doble confirmación
- ❌ Auto-importar pools observados sin acción del usuario

---

## 10. Futuro (no en C1-C3)

### Eventos

```go
type DivergenceEvent struct {
    Generation  uint64
    Type        string
    PoolID      string
    Detail      string
    Severity    string
    Timestamp   time.Time
}

// Suscriptores: notification system, log, futura interfaz NIMA
```

### Long polling con since_generation

UI sin polling cada N segundos. Backend mantiene la conexión hasta cambio.

### Auto-reconcile reactivo

Si un disco vuelve después de estar missing, intentar mount automático.
Si se detecta IO errors persistentes, marcar para SMART check.

### Scheduler de SMART en función del observer

Si el observer detecta IO errors, prioriza SMART scan en ese disco.

---

## 11. Glosario

- **Managed**: lo que NimOS declara que existe (SQLite)
- **Observed**: lo que NimOS detecta físicamente (runtime cache)
- **Divergence**: diferencia entre managed y observed
- **Fingerprint**: hash barato del estado del sistema, para skip de scans
- **Reconcile**: comparar managed vs observed, computar divergencias
- **Snapshot**: una foto del observed state (inmutable, versionada por generation)
- **Generation**: contador monotónico que aumenta cada vez que el snapshot cambia

---

**Aprobado por Andrés. Implementación arranca en Bloque C1.**
