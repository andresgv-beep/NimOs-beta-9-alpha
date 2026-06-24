# NimOS Beta 8 — Storage HTTP API

**Autor**: Andrés + Claude Opus 4.7 — Mayo 2026
**Versión**: 1.0 (Fase 1 — diseño)
**Ámbito**: endpoints REST del módulo storage

---

## 1. Principios de diseño

### 1.1 REST por recursos, no por acciones

Los endpoints se diseñan alrededor de **entidades** (pools, devices, operations) y verbos HTTP estándar, no como wrappers de comandos shell.

✅ **Bueno**: `POST /api/storage/pools/:id/devices`
❌ **Malo**: `POST /api/storage/addDevice`

### 1.1.bis Excepción: mutaciones de metadata vía endpoints específicos

Para modificar campos individuales del pool (name, role, compression, scrub policy), **no se usa PATCH genérico**. En su lugar, cada mutación tiene su endpoint específico:

```
POST /api/storage/pools/:id/rename
POST /api/storage/pools/:id/change-role
POST /api/storage/pools/:id/set-compression
POST /api/storage/pools/:id/set-scrub-policy
```

**Razón**: PATCH genérico es ambiguo y peligroso en sistemas con invariantes. Cada campo tiene su propia validación, sus propias políticas y sus propios side-effects. Endpoints específicos:

- Hacen explícita la operación en logs y auditoría
- Permiten validación específica por campo
- No se pueden "extender por error" cuando se añaden campos nuevos al schema
- Facilitan el rate-limiting selectivo si hace falta
- Son más fáciles de mockear en tests

Si en el futuro hay más campos mutables (encryption, quota, etc.), cada uno tiene su endpoint propio. **NUNCA se introduce un PATCH genérico**.

### 1.2 Mutaciones devuelven Operation, no resultado

**Toda mutación** (sync o async) genera una `Operation` persistida en SQLite. La diferencia entre sync y async no es "si se registra", sino "si la respuesta HTTP espera al resultado":

- **Mutaciones sync** (rename, change-role, set-compression, set-scrub-policy): el handler crea Operation, ejecuta, marca completed/failed, **responde con `{entity, operation}` en una sola llamada HTTP (200 OK)**.
- **Mutaciones async** (create_pool, replace_device, scrub, balance): el handler crea Operation en estado `in_progress`, lanza goroutine de background, **responde inmediatamente con `{operation}` (202 Accepted)**. El cliente hace polling de `/api/storage/operations/:id`.

**Beneficio del modelo unificado**: timeline completo, auditoría de todas las acciones, modelo mental único para el frontend. Una row más en SQLite por mutación es coste despreciable.

El mapeo sync/async de cada operación está definido en `operationModeMap` (ver `storage_api.md` §2.3). Es la verdad única; los handlers no pueden cambiar el modo por su cuenta.

### 1.3 Respuestas siempre con estructura

Ningún endpoint devuelve `{"success": true}` solo. Las respuestas siempre incluyen:
- **Entidad actualizada** (en operaciones síncronas)
- **Operation creada** (en operaciones asíncronas)
- **Error con código semántico** (en fallos)

### 1.4 Códigos HTTP correctos

| Código | Uso |
|---|---|
| `200 OK` | Lectura exitosa, mutación síncrona exitosa |
| `201 Created` | Recurso creado (POST con resultado inmediato) |
| `202 Accepted` | Operación asíncrona aceptada y en curso (devuelve Operation) |
| `400 Bad Request` | Body inválido, parámetros faltantes |
| `403 Forbidden` | Pool no permite la operación (policy layer) |
| `404 Not Found` | Pool/device/operation no existe |
| `409 Conflict` | Estado del recurso impide la operación (op en curso, nombre duplicado) |
| `500 Internal Server Error` | Bug del daemon |

### 1.5 Formato de errores

Todos los errores devuelven JSON con `code` semántico y `message` legible:

```json
{
  "error": {
    "code": "pool_observed",
    "message": "Operation not permitted on observed pool"
  }
}
```

El frontend reacciona al `code`, no parsea el `message`.

---

## 2. Endpoints

### 2.1 Pools

#### `GET /api/storage/pools`

Lista todos los pools del sistema.

**Query params** (opcionales):
- `control_state=managed|observed` — filtrar por estado
- `role=data|backup` — filtrar por role

**Response 200**:
```json
{
  "pools": [
    {
      "id": "a3f9b2c1-1234-5678-abcd-ef0123456789",
      "name": "Multimedia",
      "btrfs_uuid": "8d7e6f5a-...",
      "profile": "raid1",
      "mount_point": "/nimos/pools/multimedia",
      "role": "data",
      "control_state": "managed",
      "created_at": "2026-04-15T10:30:00Z",
      "generation": 12,
      "capabilities": ["snapshots", "balance", "replace_device", "scrub", "add_device", "remove_device", "convert_profile"],
      "devices_count": 2,
      "usage": {
        "total_bytes": 4000000000000,
        "used_bytes": 1200000000000,
        "free_bytes": 2800000000000
      },
      "health": "ok"
    }
  ]
}
```

#### `GET /api/storage/pools/:id`

Detalle de un pool. `:id` puede ser el UUID interno o el nombre.

**Response 200**:
```json
{
  "pool": {
    "id": "a3f9b2c1-...",
    "name": "Multimedia",
    "btrfs_uuid": "8d7e6f5a-...",
    "profile": "raid1",
    "mount_point": "/nimos/pools/multimedia",
    "role": "data",
    "control_state": "managed",
    "created_at": "2026-04-15T10:30:00Z",
    "generation": 12,
    "capabilities": [...],
    "devices": [
      {
        "id": "dev-uuid-1",
        "serial": "WD-WCC4N1234567",
        "by_id_path": "/dev/disk/by-id/ata-WDC_WD40EFRX-...",
        "current_path": "/dev/sdb",
        "model": "WDC WD40EFRX-68N32N0",
        "size_bytes": 4000000000000,
        "added_at": "2026-04-15T10:30:00Z"
      },
      {
        "id": "dev-uuid-2",
        "serial": "ST-WMC4N7654321",
        "by_id_path": "/dev/disk/by-id/ata-ST4000VN008-...",
        "current_path": "/dev/sdc",
        "model": "ST4000VN008-2DR166",
        "size_bytes": 4000000000000,
        "added_at": "2026-04-15T10:30:00Z"
      }
    ],
    "usage": {
      "total_bytes": 4000000000000,
      "used_bytes": 1200000000000,
      "free_bytes": 2800000000000,
      "data_ratio": 2.0,
      "metadata_ratio": 2.0
    },
    "health": {
      "status": "ok",
      "reasons": []
    },
    "running_operations": []
  }
}
```

**Response 404**:
```json
{ "error": { "code": "pool_not_found", "message": "Pool 'foo' not found" } }
```

#### `POST /api/storage/pools`

Crea un nuevo pool. **Operación asíncrona**.

**Body**:
```json
{
  "name": "Multimedia",
  "profile": "raid1",
  "device_ids": ["dev-uuid-1", "dev-uuid-2"],
  "role": "data"
}
```

**Validaciones síncronas** (antes de devolver):
- `name` regex `^[a-zA-Z0-9_-]{3,32}$`, único entre pools
- `profile` válido (single, raid1, raid1c3, raid10)
- `device_ids` cumple mínimo del profile (2 para raid1, 3 para raid1c3, 4 para raid10)
- Todos los devices existen, están disponibles, no están en uso

**Response 202**:
```json
{
  "operation": {
    "id": "op-abc123",
    "type": "create_pool",
    "status": "in_progress",
    "started_at": "2026-05-12T09:15:00Z",
    "data": {
      "name": "Multimedia",
      "profile": "raid1",
      "device_ids": ["dev-uuid-1", "dev-uuid-2"]
    }
  }
}
```

El cliente hace polling de `GET /api/storage/operations/op-abc123` hasta que `status` sea `completed` o `failed`. Cuando completa, hace `GET /api/storage/pools/:newID` para obtener el pool creado.

**Posibles errores**:
- `400 bad_request`: body inválido, regex de nombre falla
- `409 pool_name_taken`: ya existe un pool con ese nombre
- `400 device_not_eligible`: algún disco es boot disk o demasiado pequeño
- `400 device_in_use`: algún disco ya pertenece a otro pool
- `400 insufficient_disks`: menos discos de los que requiere el profile

#### `DELETE /api/storage/pools/:id`

Destruye un pool. **Operación asíncrona**. Falla si hay shares o services activos.

**Query params**:
- `force=true` — destruir incluso si hay shares (los elimina también). Default `false`.

**Response 202**:
```json
{
  "operation": {
    "id": "op-xyz789",
    "type": "destroy_pool",
    "pool_id": "a3f9b2c1-...",
    "status": "in_progress",
    "started_at": "2026-05-12T09:20:00Z"
  }
}
```

**Posibles errores**:
- `404 pool_not_found`
- `403 pool_observed`: pool en estado observed
- `409 services_active`: hay shares/services usando el pool (sin `force=true`)

#### `POST /api/storage/pools/:id/rename`

Renombra un pool. **Operación síncrona** (genera Operation con status `completed` inmediato).

**Body**:
```json
{
  "new_name": "MisMultimedia"
}
```

**Validaciones**:
- `new_name` cumple regex `^[a-zA-Z0-9_-]{3,32}$`
- `new_name` único entre pools
- Pool debe ser `managed` (los `observed` no se pueden renombrar)

**Response 200**:
```json
{
  "pool": { /* pool actualizado con el nuevo nombre */ },
  "operation": {
    "id": "op-xyz",
    "type": "rename_pool",
    "status": "completed",
    "started_at": "2026-05-12T10:00:00.000Z",
    "completed_at": "2026-05-12T10:00:00.012Z",
    "data": { "from": "Multimedia", "to": "MisMultimedia" }
  }
}
```

**Posibles errores**:
- `400 bad_request`: regex de nombre falla
- `409 pool_name_taken`: otro pool ya tiene ese nombre
- `403 pool_observed`: pool en estado observed

**Notas**:
- El `id` interno NO cambia. Solo el `name` legible.
- El `mount_point` tampoco cambia automáticamente (sigue siendo `/nimos/pools/<antiguo>`). Cambiarlo es operación aparte que requiere unmount/remount, y de momento no se ofrece.
- Las shares siguen funcionando porque referencian el `id`, no el `name`.
- La operación queda registrada en histórico para auditoría.

---

#### `POST /api/storage/pools/:id/change-role`

Cambia el role del pool. **Operación síncrona** (genera Operation con status `completed` inmediato).

**Body**:
```json
{
  "new_role": "backup"
}
```

**Response 200**:
```json
{
  "pool": { /* pool actualizado */ },
  "operation": {
    "id": "op-xyz",
    "type": "change_role",
    "status": "completed",
    "data": { "from": "data", "to": "backup" }
  }
}
```

**Notas**:
- En Beta 8 los valores activos son `data` (default) y `backup`. Los otros (`cache`, `system`) están reservados.
- Cambiar role **no mueve datos ni reconfigura nada en Beta 8**. Es solo metadata.

**⚠️ Nota importante sobre evolución futura**:

En Beta 9+ este endpoint dejará de ser puramente metadata. Cuando exista NimBackup, replication scheduler, retention policies, cambiar role puede tener **side-effects**:

- `role: data → backup` podría disparar: activación de retention policy, registro como destino de NimBackup, cambio de visibilidad en otros nodos, aplicación de quotas
- `role: backup → data` podría requerir: validar que no hay backups dependientes, desactivar replicación

Esto significa que **el endpoint puede pasar a ser async en versiones futuras**. El cliente debe diseñarse para soportar ambos casos:

```javascript
const res = await fetch('/api/storage/pools/:id/change-role', { method: 'POST', body });
const data = await res.json();
if (res.status === 202) {
    // Beta 9+: cambio de role con side-effects, async
    pollOperation(data.operation.id);
} else {
    // Beta 8: completado en la respuesta
    updateUI(data.pool);
}
```

Beta 8 siempre devuelve 200. Beta 9+ podría devolver 202 cuando role tenga consumers reales.

---

#### `POST /api/storage/pools/:id/set-compression`

Configura la compresión del pool. **Operación síncrona** (genera Operation con status `completed` inmediato).

**Body**:
```json
{
  "algorithm": "zstd",
  "level": 3
}
```

**Algoritmos soportados**:
- `none` — sin compresión (level ignorado)
- `zstd` — recomendado, levels 1-15 (default 3)
- `lzo` — más rápido pero peor ratio (sin levels)

**Response 200**:
```json
{
  "pool": { /* pool actualizado con compression */ },
  "operation": {
    "id": "op-xyz",
    "type": "set_compression",
    "status": "completed",
    "data": { "from": "none", "to": "zstd:3" }
  }
}
```

**Notas**:
- BTRFS aplica compresión **solo a archivos nuevos** desde este punto. Los archivos existentes no se recomprimen automáticamente.
- Para recomprimir todo el pool: ejecutar `btrfs filesystem defrag -r -czstd /mnt/pool` después (operación pesada, no se hace automáticamente).
- En el schema añadir columna `compression TEXT` a `storage_pools` con default `none`.

**Posibles errores**:
- `400 bad_request`: algoritmo inválido
- `400 bad_request`: level fuera de rango (zstd 1-15)
- `403 pool_observed`

---

#### `POST /api/storage/pools/:id/set-scrub-policy`

Configura la política de scrub automático para el pool. **Operación síncrona** (genera Operation con status `completed` inmediato).

**Body**:
```json
{
  "frequency": "monthly",
  "day_of_month": 1,
  "hour": 3,
  "enabled": true
}
```

**Frecuencias soportadas**:
- `none` / `enabled: false` — scrub solo manual
- `weekly` — cada semana, requiere `day_of_week` (0-6) y `hour` (0-23)
- `monthly` — cada mes, requiere `day_of_month` (1-28) y `hour` (0-23)

**Response 200**:
```json
{
  "pool": { /* pool actualizado */ },
  "operation": {
    "id": "op-xyz",
    "type": "set_scrub_policy",
    "status": "completed",
    "data": { "from": {...}, "to": {...} }
  }
}
```

**Notas**:
- El scheduler de scrubs ya existe en Beta 7 (`storage_zfs_features.go`, será renombrado a `storage_btrfs_features.go`).
- Este endpoint expone la configuración por API, hoy probablemente está hardcoded o solo en DB.
- La política se guarda en la tabla `scrub_schedule` existente (no se cambia el schema de esa tabla).

**Posibles errores**:
- `400 bad_request`: frecuencia inválida o parámetros inconsistentes (ej: `monthly` sin `day_of_month`)
- `403 pool_observed`

---

### 2.2 Pool Devices (subrecurso)

#### `POST /api/storage/pools/:id/devices`

Añade un disco a un pool existente. **Operación asíncrona**.

**Body**:
```json
{
  "device_id": "dev-uuid-3"
}
```

**Response 202**:
```json
{
  "operation": {
    "id": "op-def456",
    "type": "add_device",
    "pool_id": "a3f9b2c1-...",
    "status": "in_progress",
    "data": {
      "device_id": "dev-uuid-3"
    }
  }
}
```

**Posibles errores**:
- `403 pool_observed`
- `403 capability_missing`: pool no soporta add_device
- `400 device_in_use`
- `400 device_missing`: disco no presente físicamente

#### `DELETE /api/storage/pools/:id/devices/:deviceID`

Quita un disco del pool. **Operación asíncrona** (rebalance automático).

**Response 202**:
```json
{
  "operation": {
    "id": "op-ghi789",
    "type": "remove_device",
    "pool_id": "a3f9b2c1-...",
    "status": "in_progress",
    "data": {
      "device_id": "dev-uuid-3"
    }
  }
}
```

**Posibles errores**:
- `404 pool_not_found` / `404 device_not_found`
- `400 min_disks_reached`: la operación dejaría el pool sin redundancia mínima

#### `POST /api/storage/pools/:id/devices/:deviceID/replace`

Reemplaza un disco fallido por otro. **Operación asíncrona** (usa `btrfs replace`, no add+remove).

**Body**:
```json
{
  "new_device_id": "dev-uuid-4"
}
```

**Response 202**:
```json
{
  "operation": {
    "id": "op-jkl012",
    "type": "replace_device",
    "pool_id": "a3f9b2c1-...",
    "status": "in_progress",
    "data": {
      "old_device_id": "dev-uuid-3",
      "new_device_id": "dev-uuid-4",
      "progress": 0
    }
  }
}
```

El campo `data.progress` se actualiza en background y puede consultarse vía `GET /api/storage/operations/:opID`.

---

### 2.3 Pool Operations (subrecursos)

#### `POST /api/storage/pools/:id/profile`

Convierte el profile del pool. **Operación asíncrona**.

**Body**:
```json
{
  "new_profile": "raid1c3"
}
```

**Response 202**:
```json
{
  "operation": {
    "id": "op-mno345",
    "type": "convert_profile",
    "pool_id": "a3f9b2c1-...",
    "status": "in_progress",
    "data": {
      "from_profile": "raid1",
      "to_profile": "raid1c3"
    }
  }
}
```

**Posibles errores**:
- `400 insufficient_disks`: el nuevo profile requiere más discos de los que tiene el pool
- `400 profile_invalid`

#### `POST /api/storage/pools/:id/scrub`

Inicia un scrub manual. **Operación asíncrona**.

**Response 202**:
```json
{
  "operation": {
    "id": "op-pqr678",
    "type": "start_scrub",
    "pool_id": "a3f9b2c1-...",
    "status": "in_progress"
  }
}
```

#### `GET /api/storage/pools/:id/scrub`

Estado del scrub actual o último completado.

**Response 200**:
```json
{
  "scrub": {
    "status": "running",
    "progress": 47.2,
    "started_at": "2026-05-12T08:00:00Z",
    "data_scrubbed_bytes": 567000000000,
    "errors": 0
  }
}
```

#### `GET /api/storage/pools/:id/balance`

Estado del balance actual.

**Response 200**:
```json
{
  "balance": {
    "status": "running",
    "progress": 23.5,
    "started_at": "2026-05-12T09:00:00Z",
    "chunks_balanced": 47,
    "chunks_total": 200
  }
}
```

#### `POST /api/storage/pools/:id/balance/pause`

Pausa un balance en curso. **Operación síncrona**.

**Response 200**: estado actualizado del balance.

#### `POST /api/storage/pools/:id/balance/resume`

Reanuda un balance pausado. **Operación síncrona**.

---

### 2.4 Snapshots (subrecurso)

#### `GET /api/storage/pools/:id/snapshots`

Lista snapshots del pool.

**Response 200**:
```json
{
  "snapshots": [
    {
      "name": "auto-2026-05-12-0300",
      "subvolume": "shares",
      "created_at": "2026-05-12T03:00:00Z",
      "read_only": true,
      "size_bytes": 0
    }
  ]
}
```

#### `POST /api/storage/pools/:id/snapshots`

Crea un snapshot. **Operación asíncrona**.

**Body**:
```json
{
  "subvolume": "shares",
  "name": "manual-before-update",
  "read_only": true
}
```

**Response 202**: Operation.

#### `DELETE /api/storage/pools/:id/snapshots/:name`

Elimina un snapshot. **Operación asíncrona**.

**Response 202**: Operation.

---

### 2.5 Devices

#### `GET /api/storage/devices`

Lista todos los discos detectados.

**Query params**:
- `available=true` — solo discos no asignados a ningún pool
- `in_pool=:poolID` — solo discos del pool indicado

**Response 200**:
```json
{
  "devices": [
    {
      "id": "dev-uuid-1",
      "serial": "WD-WCC4N1234567",
      "by_id_path": "/dev/disk/by-id/ata-WDC_WD40EFRX-...",
      "current_path": "/dev/sdb",
      "wwn": "0x50014ee2bcd45678",
      "model": "WDC WD40EFRX-68N32N0",
      "size_bytes": 4000000000000,
      "in_pool": "a3f9b2c1-...",
      "available": false,
      "last_seen_at": "2026-05-12T09:30:00Z",
      "smart": {
        "status": "PASSED",
        "temperature_c": 38,
        "power_on_hours": 12450,
        "reallocated_sectors": 0
      }
    }
  ]
}
```

#### `GET /api/storage/devices/:id`

Detalle de un disco, incluyendo histórico SMART.

**Response 200**:
```json
{
  "device": { /* device + smart_history */ }
}
```

---

### 2.6 Operations

#### `GET /api/storage/operations`

Lista de operaciones (histórico).

**Query params**:
- `status=pending|in_progress|completed|failed|rolled_back|cancelled`
- `pool_id=:id`
- `type=create_pool|add_device|...`
- `since=2026-05-01T00:00:00Z`
- `limit=50` (default 50, max 200)

**Response 200**:
```json
{
  "operations": [
    {
      "id": "op-abc123",
      "type": "create_pool",
      "pool_id": null,
      "status": "completed",
      "started_at": "2026-05-12T09:15:00Z",
      "completed_at": "2026-05-12T09:15:42Z",
      "data": { /* payload original */ }
    }
  ]
}
```

#### `GET /api/storage/operations/:id`

Detalle de una operación incluyendo eventos.

**Response 200**:
```json
{
  "operation": {
    "id": "op-abc123",
    "type": "create_pool",
    "pool_id": "a3f9b2c1-...",
    "status": "completed",
    "started_at": "2026-05-12T09:15:00Z",
    "completed_at": "2026-05-12T09:15:42Z",
    "error": null,
    "error_code": null,
    "data": {
      "name": "Multimedia",
      "profile": "raid1",
      "device_ids": ["dev-uuid-1", "dev-uuid-2"]
    },
    "events": [
      { "timestamp": "2026-05-12T09:15:00Z", "level": "info", "message": "Operation started" },
      { "timestamp": "2026-05-12T09:15:01Z", "level": "info", "message": "Wiping /dev/disk/by-id/ata-WDC_..." },
      { "timestamp": "2026-05-12T09:15:03Z", "level": "info", "message": "Wiping /dev/disk/by-id/ata-ST..." },
      { "timestamp": "2026-05-12T09:15:05Z", "level": "info", "message": "Running mkfs.btrfs -draid1 -mraid1 ..." },
      { "timestamp": "2026-05-12T09:15:38Z", "level": "info", "message": "Filesystem created, UUID 8d7e6f5a-..." },
      { "timestamp": "2026-05-12T09:15:40Z", "level": "info", "message": "Mounted at /nimos/pools/multimedia" },
      { "timestamp": "2026-05-12T09:15:42Z", "level": "info", "message": "Operation completed" }
    ]
  }
}
```

#### `POST /api/storage/operations/:id/cancel`

Intenta cancelar una operación en curso. No todas las operaciones soportan cancelación (ej: un balance se puede pausar, no cancelar; un create_pool no se puede cancelar una vez iniciado el mkfs).

**Response 200** (si cancelable y aceptado):
```json
{
  "operation": { /* op con status: cancelled */ }
}
```

**Response 409** (si no cancelable en su estado actual):
```json
{
  "error": { "code": "operation_not_cancellable", "message": "Operation in state 'in_progress' cannot be cancelled" }
}
```

---

## 3. Polling pattern

Para operaciones asíncronas, el cliente sigue este patrón:

```javascript
// 1. Iniciar operación
const res = await fetch('/api/storage/pools', { method: 'POST', body: JSON.stringify(spec) });
const { operation } = await res.json();

// 2. Polling
while (true) {
    await sleep(2000);
    const opRes = await fetch(`/api/storage/operations/${operation.id}`);
    const { operation: current } = await opRes.json();

    if (current.status === 'completed') {
        // Hacer GET del nuevo pool (id en data)
        break;
    }
    if (current.status === 'failed') {
        showError(current.error_code, current.error);
        break;
    }
    updateProgress(current.data.progress);
}
```

**Frecuencia de polling recomendada**: 2 segundos para operaciones interactivas, 10 segundos para operaciones largas (balance, scrub).

**Optimización opcional (Beta 9)**: añadir endpoint `GET /api/storage/generation` que devuelve solo el contador global. El cliente puede pollear ese endpoint (respuesta diminuta) y solo refrescar listas completas cuando cambie.

---

## 4. Endpoints legacy (compatibilidad Beta 7)

Para soportar el frontend de Beta 7 mientras se migra al nuevo, se conservan los siguientes endpoints. **Todos van marcados como deprecated** y se eliminan en Beta 9.

### `POST /api/storage/detectDisks` (LEGACY)

Equivalente a `GET /api/storage/devices?available=true`. Internamente delega.

**Response**: shape antiguo del Beta 7 para no romper frontend.

### `POST /api/storage/createPool` (LEGACY)

Equivalente a `POST /api/storage/pools`. Devuelve `{success: true, pool_name: "..."}` (shape antiguo). Internamente espera a que la operación complete antes de devolver (síncrono).

### Y otros del Beta 7

`/api/storage/destroyPool`, `/api/storage/detachDisk`, `/api/storage/attachDisk`, `/api/storage/replaceDisk` — todos delegan al nuevo modelo pero devuelven shapes antiguos.

**Containment**: todos los endpoints legacy se implementan en un único archivo `storage_legacy_http.go` y siguen las mismas reglas que las funciones legacy (§8 de `storage_api.md`).

---

## 5. Códigos de error completos

Lista de todos los códigos que puede devolver la API. El frontend debe poder reaccionar a cada uno.

| Código | HTTP | Significado |
|---|---|---|
| `pool_not_found` | 404 | Pool inexistente |
| `pool_name_taken` | 409 | Nombre de pool duplicado |
| `pool_observed` | 403 | Pool en estado observed, no permite mutación |
| `capability_missing` | 403 | Operación no soportada por este pool |
| `operation_in_progress` | 409 | Ya hay una op corriendo en este pool |
| `operation_not_found` | 404 | Operation inexistente |
| `operation_not_cancellable` | 409 | Operación no puede cancelarse en su estado actual |
| `device_not_found` | 404 | Device inexistente |
| `device_in_use` | 400 | Disco asignado a otro pool |
| `device_missing` | 400 | Disco no presente físicamente |
| `device_not_eligible` | 400 | Disco demasiado pequeño / boot / no apto |
| `profile_invalid` | 400 | Profile desconocido o no soportado |
| `insufficient_disks` | 400 | Faltan discos para el profile pedido |
| `min_disks_reached` | 400 | remove dejaría el pool sin redundancia |
| `services_active` | 409 | Shares/services usando el pool |
| `mount_failed` | 500 | Falló el mount |
| `unmount_failed` | 500 | Falló el unmount |
| `btrfs_command_failed` | 500 | Comando btrfs devolvió error |
| `internal` | 500 | Bug del daemon (logear) |
| `bad_request` | 400 | Body inválido genérico |

---

## 6. Versión de la API

La API se versiona en la URL: `/api/storage/...` es **v1** (implícito).

Si en el futuro hace falta v2 incompatible, se servirá en `/api/v2/storage/...` manteniendo v1 funcional durante un tiempo.

Beta 8 no necesita versionado explícito todavía.

---

*Documento generado en Fase 1 del refactor Beta 8.*
