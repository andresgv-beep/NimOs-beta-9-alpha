# DESIGN — Storage Space Guard (STOR-SPACE)

**Proyecto:** NimOS · Módulo Storage
**Target:** Beta 9
**Estado:** DISEÑO — pendiente de aprobación
**Gobernanza:** DISCIPLINE v2.1 · Rule 16 ("External Systems Own Their Facts")
**Fecha:** 2026-06-12
**Referencias externas:** btrfs-docs (Balance, ENOSPC), btrfsmaintenance (kdave), SUSE KB 000019789, Forza wiki (Balance / ENOSPC)

---

## 0. Resumen ejecutivo

BTRFS pone el filesystem en **read-only** cuando no puede asignar un chunk nuevo de
metadata. Ocurre con el disco "al 70-80%" según `df`, sin aviso previo, y en ese estado
ni siquiera se pueden borrar archivos (borrar también escribe metadata). Es la avería
más probable de un NAS BTRFS 24/7 y **ningún competidor open-source la monitoriza ni la
previene** (OMV no; CasaOS no; btrfsmaintenance lo previene pero es un paquete externo
que el usuario debe conocer e instalar; Synology DSM lo hace internamente y es
propietario).

STOR-SPACE añade al módulo storage cuatro capas: **observación** (muestreo del espacio
real del kernel), **diagnóstico** (códigos de presión de metadata), **predicción**
(forecast de días hasta agotamiento) y **remediación** (balance filtrado automático,
acotado y seguro). Ninguna capa inventa estado: el kernel posee los hechos (Rule 16),
NimOS los observa, los persiste como historia, y actúa con operaciones journaled.

---

## 1. Modelo técnico (lo mínimo que hay que entender)

### 1.1 Asignación en dos fases

BTRFS asigna espacio en dos niveles:

1. **Chunk/block group**: reserva un bloque grande desde el espacio *unallocated*
   (data ≈ 1 GiB, metadata ≈ 256 MiB por chunk, escalando con el tamaño del FS).
2. **Extents**: escribe dentro de los chunks ya asignados.

Consecuencias:

- Si no puede asignar chunk de **data** → error de disco lleno al escribir. Molesto,
  recuperable.
- Si no puede asignar chunk de **metadata** → ENOSPC interno → **el FS pasa a
  read-only** para protegerse de corrupción. No recuperable sin intervención.

### 1.2 La trampa

Chunks de data asignados pero infrautilizados (Size ≫ Used) **secuestran** el espacio
unallocated. El `df` y nuestro `UsagePercent` actual ven espacio libre *dentro* de los
chunks de data; metadata no puede usar ese espacio. Resultado: pool "al 75%" que muere
de ENOSPC de metadata.

### 1.3 Señales observables (todas en `btrfs filesystem usage -b <mp>`)

| Señal | Campo | Significado |
|---|---|---|
| S1 | `unallocated` **por device** | Espacio de maniobra real. La señal principal y la predictiva. |
| S2 | Metadata `Size` vs `Used` | % de llenado de los chunks de metadata ya asignados. Peligroso solo combinado con S1. |
| S3 | `Global reserve (used: X)` | Reserva de emergencia del kernel. `used > 0` = el FS ya está en modo pánico. **Crítico incondicional.** |
| S4 | Data `Size` vs `Used` | Cuánto espacio liberaría un balance. Determina si la remediación es viable. |

### 1.4 Matiz RAID1 (crítico para NimOS)

Un chunk nuevo de metadata RAID1/RAID1C3 necesita espacio unallocated **en N devices
simultáneamente** (N = nº de copias). La métrica correcta NO es el unallocated total:

```
unallocated_efectivo(single)  = min(unallocated por device)        // conservador
unallocated_efectivo(raid1)   = 2º mayor unallocated por device
unallocated_efectivo(raid1c3) = 3er mayor unallocated por device
unallocated_efectivo(raid10)  = min de los 4 mayores (pares)
```

Para `single` se usa `min` por conservadurismo: el allocator puede elegir cualquier
device, pero garantizar el peor caso evita falsos negativos en arrays asimétricos.

### 1.5 Remediación: qué funciona y qué está prohibido

- **Funciona**: `btrfs balance start -dusage=N` compacta chunks de data ≤N% llenos y
  devuelve el espacio al pool unallocated.
- **Escalera obligatoria**: `usage=0` no requiere espacio de trabajo (solo libera
  chunks completamente vacíos) — es la única opción que funciona cuando el propio
  balance falla por ENOSPC. Después se escala: 0 → 5 → 10.
- **`limit=N`** acota el balance a N chunks por pasada. En Raspberry Pi es
  obligatorio: un balance sin límite puede tardar horas y saturar IO.
- **PROHIBIDO**: balance de metadata (`-musage`) automático. Compactar metadata
  reduce el margen libre dentro de sus chunks y AUMENTA el riesgo de ENOSPC. Solo es
  legítimo en conversión de perfil (eso ya lo gestiona ConvertProfile) — nunca como
  mantenimiento.
- **Bonus kernel ≥ 5.19**: sysfs `/sys/fs/btrfs/<FSID>/allocation/data/bg_reclaim_threshold`
  hace que el kernel reclame automáticamente block groups bajo el umbral (default 0 =
  solo vacíos). Ponerlo a 50 es prevención gratuita sin daemon.

Referencia de umbrales de la industria (btrfsmaintenance, defaults desde v0.5):
`dusage=10`, `musage=5` (musage solo manual). openSUSE de serie ejecuta la escalera
0/5/10 mensualmente.

---

## 2. Principios de diseño

- **P1 — Rule 16**: el kernel posee los hechos del espacio. NimOS jamás cachea como
  verdad lo que puede releer; la tabla de historia es *historia*, no estado.
- **P2 — Diagnóstico ≠ acción**: CollectDiagnostics calcula, no ejecuta. La
  remediación es una Operation explícita, journaled, visible en la UI.
- **P3 — Remediación acotada**: todo balance automático lleva `limit` y filtro
  `dusage`. Nunca un balance sin filtros. Nunca `-musage`.
- **P4 — Transiciones, no estados**: las notificaciones disparan en cambio de estado
  (patrón del monitor SMART), nunca en bucle.
- **P5 — Degradación elegante**: si `fi usage` falla o el kernel es viejo, el guard
  se degrada a no-op con log, jamás bloquea el health loop.

---

## 3. Arquitectura por capas

```
                    ┌─────────────────────────────────────┐
                    │  kernel BTRFS (SOT del espacio)      │
                    └──────────────┬──────────────────────┘
                                   │ btrfs filesystem usage -b
                    ┌──────────────▼──────────────────────┐
  CAPA 1            │  SpaceObserver                      │
  Observación       │  · parser ampliado (executor_real)  │
                    │  · sample → pool_space_history      │
                    └──────────────┬──────────────────────┘
                                   │ PoolSpaceSample
                    ┌──────────────▼──────────────────────┐
  CAPA 2+3          │  SpaceDiagnostics (en Collect-      │
  Diagnóstico       │  Diagnostics) + SpaceForecast       │
  + Predicción      │  · metadata_pressure_warning        │
                    │  · metadata_pressure_critical       │
                    │  · global_reserve_in_use            │
                    │  · metadata_exhaustion_forecast     │
                    └──────┬───────────────────┬──────────┘
                           │                   │
              ┌────────────▼─────┐   ┌─────────▼───────────┐
  CAPA 4      │  SpaceReclaimer  │   │  Notificaciones     │
  Acción      │  · Operation     │   │  · addNotification  │
              │    reclaim_space │   │    con dedupe por   │
              │  · escalera      │   │    transición       │
              │    0→5→10 +limit │   │  · UI pool detail   │
              └──────────────────┘   └─────────────────────┘
```

---

## 4. Capa 1 — Observación

### 4.1 Parser ampliado

`storage_executor_real.go` ya ejecuta `btrfs filesystem usage -b` (~línea 501, lectura
de Total/Used). Se amplía la struct de salida:

```go
// PoolSpaceInfo — lectura completa de `btrfs filesystem usage -b`.
// Rule 16: snapshot puntual del kernel, jamás se persiste como estado.
type PoolSpaceInfo struct {
    DeviceSize        int64            // Overall: Device size
    DeviceAllocated   int64            // Overall: Device allocated
    DeviceUnallocated int64            // Overall: Device unallocated (suma)
    UnallocatedByDev  map[string]int64 // sección Unallocated: por device
    MetadataSize      int64            // Metadata,<profile>: Size
    MetadataUsed      int64            // Metadata,<profile>: Used
    DataSize          int64            // Data,<profile>: Size
    DataUsed          int64            // Data,<profile>: Used
    GlobalReserveSize int64            // Global reserve: total
    GlobalReserveUsed int64            // Global reserve: (used: X)
}
```

Parsing: la sección `Unallocated:` lista `path  bytes` por device. Con `-b` todos los
valores vienen en bytes — sin conversión de unidades. Si el FS tiene perfiles
múltiples transitorios (a media conversión), se suman las filas Metadata,* y Data,*.

### 4.2 Helper de unallocated efectivo

```go
// effectiveUnallocated — espacio real disponible para un chunk nuevo de
// metadata según el perfil. Ver §1.4 del diseño.
func effectiveUnallocated(profile Profile, byDev map[string]int64) int64
```

Tests unitarios con tablas: simétrico, asimétrico extremo (8TB+1TB), device a 0,
perfiles single/raid1/raid1c3/raid10.

### 4.3 Persistencia de historia

Nueva tabla en `storage_schema.sql`:

```sql
-- §9 STOR-SPACE · Historia de espacio por pool.
-- Un sample por pool por ciclo del health loop (5 min).
-- Es HISTORIA para el forecast, jamás fuente de verdad (Rule 16).
CREATE TABLE IF NOT EXISTS pool_space_history (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    pool_id             TEXT    NOT NULL,
    sampled_at          TEXT    NOT NULL,  -- RFC3339
    unallocated_eff     INTEGER NOT NULL,  -- bytes, ya por perfil
    unallocated_total   INTEGER NOT NULL,
    metadata_size       INTEGER NOT NULL,
    metadata_used       INTEGER NOT NULL,
    data_size           INTEGER NOT NULL,
    data_used           INTEGER NOT NULL,
    global_reserve_used INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_space_history_pool_time
    ON pool_space_history(pool_id, sampled_at);
```

Retención: barrido en el propio health loop, `DELETE ... WHERE sampled_at < now-30d`
(mismo patrón que la retención de network). ~8.6k filas/pool/mes — irrelevante para
SQLite.

El sampler vive en `checkStorageHealthGo` (storage_startup.go:337), que pasa de ser
"solo % de uso" a ser el ciclo de salud completo del módulo.

---

## 5. Capas 2+3 — Diagnóstico y predicción

### 5.1 Nuevos códigos de diagnóstico (CollectDiagnostics, storage_health.go)

| Código | Condición | Severidad |
|---|---|---|
| `metadata_pressure_warning` | `unallocated_eff < 2 GiB` **Y** `metadata_used/metadata_size ≥ 0.85` | warning |
| `metadata_pressure_critical` | `unallocated_eff < 512 MiB` **O** (`ratio ≥ 0.95` **Y** `unallocated_eff < 1 GiB`) | critical |
| `global_reserve_in_use` | `global_reserve_used > 0` | critical (incondicional, sin heurística) |
| `metadata_exhaustion_forecast` | forecast < 14 días (ver §5.2) | warning |
| `space_reclaim_recommended` | `data_size − data_used > 10%` de device_size **Y** `unallocated_eff < 4 GiB` | info |

Constantes con nombre y override por env (patrón `OMV_*` adaptado a `NIMOS_*`):

```go
const (
    spaceWarnUnallocEff   = 2 << 30   // 2 GiB
    spaceCritUnallocEff   = 512 << 20 // 512 MiB — no cabe ni un chunk de metadata
    spaceWarnMetaRatio    = 0.85
    spaceCritMetaRatio    = 0.95
    spaceForecastHorizon  = 14        // días
)
```

Nota de calibración: 512 MiB cubre el chunk de metadata más grande observable (1 GiB
se da solo en FS muy grandes; si Beta 10 soporta pools >50 TiB, revisar). Los pools
pequeños (<50 GiB) usan umbrales a escala: `min(spaceWarnUnallocEff, deviceSize/20)`.

### 5.2 Forecast

Regresión lineal simple sobre `unallocated_eff` de los últimos 7 días (mínimo 24
samples = 2 horas para emitir; con menos, no hay forecast). Pendiente ≥ 0 → sin
forecast (el espacio crece o está estable). Pendiente < 0:

```
días_restantes = unallocated_eff_actual / |pendiente_bytes_por_día|
```

Sin EMA, sin suavizado exótico. Si en hardware real hay ruido (balance, borrados
masivos), se itera — primero la versión simple, verificada en la Pi (lección del
Motor CUDA Omega: ground truth antes que sofisticación).

### 5.3 La bocina (cierre del gap de Beta 8.1)

Este diseño **incluye** la conexión diagnostics → notificaciones que quedó
identificada en la auditoría:

- Mapa en memoria `lastDiagState[poolID][code] → severity` (mutex propio).
- Transición `ausente→warning`, `warning→critical`, `*→ausente (recuperado)` →
  `addNotification` con el patrón exacto del monitor SMART (hardware.go:1722+).
- Sin transición → silencio. Cero spam.
- Aprovechar el mismo mecanismo para conectar los códigos YA existentes
  (`disk_missing`, `smart_critical`, `pool_degraded`) — un solo PR cierra la bocina
  entera, no solo la de espacio.

---

## 6. Capa 4 — Remediación

### 6.1 Nueva Operation

```go
OpTypeReclaimSpace OperationType = "reclaim_space" // async, journaled
```

- **Modo**: async (tabla `OperationModeAsync`), goroutine con `context.Background()`
  — mismo patrón documentado en ConvertProfile (storage_service.go:1379+).
- **Exclusión**: se añade `reclaim_space` al índice `idx_one_layout_op_per_pool`. Un
  reclaim no convive con un convert/add/remove en el mismo pool. BTRFS tampoco lo
  permitiría.
- **Recovery**: caso nuevo en `resolveOrphanOperation` → un reclaim interrumpido es
  **inocuo por diseño** (balance filtrado a medias no deja drift de layout) →
  outcome `rolled_back` con mensaje, no `inconclusive`. Es la única op de layout que
  puede resolverse con certeza tras crash.

### 6.2 Ejecución: la escalera

```go
// runReclaimLadder — ejecuta la escalera de balance filtrado.
// Cada peldaño revalida: si unallocated_eff ya supera el objetivo, para.
// PROHIBIDO -musage (ver DISCIPLINE: STOR-SPACE-R1).
func (s *StorageService) runReclaimLadder(ctx context.Context, mp string) error {
    steps := []string{"-dusage=0", "-dusage=5,limit=4", "-dusage=10,limit=4"}
    target := int64(4 << 30) // 4 GiB de margen y paramos
    for _, f := range steps {
        if s.readUnallocEff(mp) >= target { return nil }
        if err := s.btrfs.BalanceFiltered(ctx, mp, f); err != nil {
            return err // el peldaño 0 nunca falla por ENOSPC; si falla, abortar
        }
    }
    return nil
}
```

Guard previo (lección OMV): consultar `btrfs balance status` antes de lanzar — si hay
balance en curso (de un convert, p. ej.), la operation falla limpia con
`ErrCodeBalanceInProgress`, no se encola.

### 6.3 Disparadores

1. **Programado**: junto al scrub scheduler (storage_btrfs_features.go), semanal por
   defecto, tabla `reclaim_schedule` (mismo shape que `scrub_schedule`). Peldaño
   suave único: `-dusage=10,limit=2`. Minutos de IO, no horas.
2. **Reactivo**: al transicionar a `metadata_pressure_warning`, el health loop
   encola un `reclaim_space` automático (config on/off, default ON, en NimSettings).
   En `critical` con reclaim ya fallido → solo notificación urgente con
   instrucciones; no insistir en bucle (`max 1 auto-reclaim / 24h / pool`).
3. **Manual**: botón "Liberar espacio de maniobra" en el detalle del pool.

### 6.4 Prevención a nivel kernel (one-shot al boot)

En `runStorageStartupTasks`, goroutine best-effort (patrón exacto de
`enableQuotaOnAllPools`, storage_boot.go:102+):

```
si kernel ≥ 5.19:
    para cada pool montado:
        echo 50 > /sys/fs/btrfs/<FSID>/allocation/data/bg_reclaim_threshold
```

Idempotente, falla en silencio con log si el sysfs no existe. NO tocar el threshold
de metadata.

---

## 7. API HTTP (storage_http_v2.go)

| Endpoint | Método | Descripción |
|---|---|---|
| `/api/v2/storage/pools/{id}/space` | GET | PoolSpaceInfo actual + forecast + último diagnóstico |
| `/api/v2/storage/pools/{id}/space/history` | GET | serie temporal (query: `days`, default 7) |
| `/api/v2/storage/pools/{id}/reclaim` | POST | encola OpTypeReclaimSpace (respeta If-Match/generation, CRIT-1) |
| `/api/v2/storage/reclaim-schedule` | GET/PUT | configuración del programado |

Errores nuevos: `ErrCodeBalanceInProgress`, `ErrCodeReclaimCooldown`.

### UI (fuera de alcance de este doc, requisitos mínimos)

- Detalle de pool: "Espacio de maniobra: X GiB" con semáforo (verde >4 GiB,
  ámbar 2-4, rojo <2) + sparkline de 7 días desde `/space/history`.
- Widget de salud: el semáforo agregado del peor pool.

---

## 8. Plan de fases

**Fase A — Observación** *(sin riesgo, solo lectura)*
Parser ampliado + `effectiveUnallocated` + tabla + sampler en health loop + endpoint
GET `/space`.
✓ Verificación: en la Pi con el pool RAID1 real, los valores coinciden con
`btrfs fi usage -b` a mano. Samples acumulándose en SQLite. Test del parser con
fixtures de salidas reales (capturar del NAS: simétrico, asimétrico, a media
conversión).

**Fase B — Diagnóstico + bocina** *(sin riesgo, solo alertas)*
Códigos nuevos en CollectDiagnostics + dedupe por transición + conexión de los
códigos existentes (disk_missing, smart, degraded) a notificaciones.
✓ Verificación en hardware: llenar un pool de pruebas con `fallocate` hasta disparar
warning → notificación única; liberar → notificación de recuperado; sin spam en 24h
de soak.

**Fase C — Remediación** *(riesgo controlado)*
OpTypeReclaimSpace + escalera + guard de balance + recovery case + endpoint POST +
trigger reactivo con cooldown.
✓ Verificación: pool de pruebas fragmentado artificialmente (escribir/borrar miles de
archivos), confirmar que la escalera recupera unallocated; matar el daemon a media
escalera → recovery lo resuelve como rolled_back; convert en curso → reclaim falla
limpio.

**Fase D — Programado + forecast + kernel knob**
reclaim_schedule + regresión + bg_reclaim_threshold al boot.
✓ Verificación: 7 días de soak en producción (nimbarraca) con consumo real; forecast
coherente con el ritmo observado; el programado semanal corre y notifica resultado.

Orden estricto A→B→C→D. A+B son la bocina con datos nuevos; C+D la autonomía.

---

## 9. Reglas para DISCIPLINE (propuestas)

- **STOR-SPACE-R1**: NUNCA `-musage` en remediación automática. El balance de
  metadata solo existe dentro de ConvertProfile.
- **STOR-SPACE-R2**: todo balance automático lleva filtro `dusage` Y `limit`. Un
  balance sin filtros no puede originarse en código de NimOS.
- **STOR-SPACE-R3**: la tabla `pool_space_history` es historia, no estado. Ningún
  código de decisión lee de ella excepto el forecast. Las decisiones leen del kernel.
- **STOR-SPACE-R4**: las notificaciones de salud disparan solo en transición de
  estado. Un código que notifique en bucle es un bug P1.

---

## 10. Touch points (resumen por archivo)

| Archivo | Cambio |
|---|---|
| `storage_executor_real.go` | parser PoolSpaceInfo completo |
| `storage_executor.go` / `_mock.go` | firma en interfaz + mock |
| `storage_types.go` | PoolSpaceInfo, OpTypeReclaimSpace, constantes de umbral |
| `storage_schema.sql` | §9 pool_space_history, reclaim_schedule, índice layout-op ampliado |
| `storage_health.go` | 5 códigos nuevos en CollectDiagnostics + effectiveUnallocated |
| `storage_startup.go` | checkStorageHealthGo: sampler + retención + dedupe/notif |
| `storage_service.go` | ReclaimSpace (operation async) + runReclaimLadder |
| `storage_recovery.go` | caso reclaim_space → rolled_back |
| `storage_btrfs_features.go` | reclaim scheduler (junto al de scrub) + guard is_running |
| `storage_boot.go` | bg_reclaim_threshold one-shot |
| `storage_http_v2.go` | 4 endpoints |
| `hardware.go` | (nada — el dedupe de espacio vive en storage, patrón copiado de SMART) |

Estimación: Fase A ~1 sesión, B ~1 sesión, C ~2 sesiones, D ~1 sesión + soak.

---

## 11. Lo que este diseño NO hace (alcance negativo)

- No balancea metadata. Nunca. (R1)
- No hace defrag (rompe el sharing de extents con snapshots — fuera de alcance,
  evaluable en Beta 10 con la retention de snapshots).
- No predice con modelos: regresión lineal y a correr. Si el ruido lo exige, se
  itera con datos reales del soak.
- No toca el flujo de ConvertProfile existente.
- No resuelve la retention de snapshots (fragilidad #4 de la auditoría) — diseño
  aparte, aunque comparte la motivación ENOSPC.
