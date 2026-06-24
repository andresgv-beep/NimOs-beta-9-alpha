# NimOS Beta 8 — Storage Invariants

**Autor**: Andrés + Claude Opus 4.7 — Mayo 2026
**Versión**: 1.0 (Fase 1 — diseño)
**Ámbito**: invariantes no negociables del módulo storage

---

## Cómo usar este documento

Las invariantes aquí enumeradas son **reglas duras** del módulo storage. No son sugerencias, no son "buenas prácticas". Son condiciones que el código debe cumplir siempre, sin excepción.

**Referenciar desde el código** con comentarios del estilo:

```go
// Verificar identidad por Serial, nunca por CurrentPath.
// see: storage_invariants.md#3.1
device := repo.GetDeviceBySerial(disk.Serial)
```

**Si una invariante se rompe**, es un bug crítico independientemente de si "funciona" en el caso concreto. Las invariantes existen para que casos raros (crash en mal momento, race condition, disco que cambia de puerto) no causen pérdida de datos o estados imposibles.

**Lista corta a propósito**. Si añadimos invariantes constantemente, dejan de tener fuerza. Solo las 5 reglas no negociables del módulo.

---

## 1. JSON es payload, no entidad

**Regla**: las entidades del dominio (Pool, Device, Operation) viven en SQLite. JSON solo aparece como **payload temporal** dentro de campos `TEXT` de operaciones (parámetros, progreso).

**Por qué**: SQLite da atomicidad transaccional, queries reales, integridad referencial. JSON como source of truth da race conditions sutiles, lecturas/escrituras parciales, drift entre archivo y memoria. El módulo storage de Beta 7 falló por esto.

**Anti-pattern (PROHIBIDO)**:
```go
// MAL — no escribir entidades a un archivo JSON
ioutil.WriteFile("/var/lib/nimos/pools.json", data, 0644)
```

**Pattern correcto**:
```go
// BIEN — entidades en SQLite, JSON solo en data temporal de operations
op := &Operation{
    Type: OpTypeReplaceDevice,
    Data: jsonRaw(`{"old_device": "...", "new_device": "...", "progress": 42}`),
}
repo.CreateOperation(ctx, tx, op)
```

**Excepción documentada**: el archivo `.nimos-pool.json` en el root de cada pool. **No es una entidad** — es un identity file portátil que viaja con el disco para recovery e import. Cumple la regla porque la entidad sigue viviendo en SQLite; el identity file es metadata redundante para survivability.

---

## 2. Policy layer separado de Storage layer

**Regla**: la decisión de "¿puedo hacer esta operación?" vive en `PolicyChecker`. La ejecución de "haz esta operación" vive en `StorageService`. **Nunca se mezclan**.

**Por qué**: la lógica de permisos dispersa por handlers es el patrón anti-arquitectónico más común en sistemas que crecen. En 6 meses tienes 12 sitios distintos que verifican "¿es managed?" y "¿hay balance corriendo?" con código duplicado y sutilmente distinto. Cuando hay que cambiar una regla, hay que cambiarla en 12 sitios.

**Anti-pattern (PROHIBIDO)**:
```go
// MAL — política dispersa
func handleReplaceDevice(...) {
    if pool.ControlState != "managed" {
        return error("not allowed")
    }
    if pool.BalanceRunning {
        return error("balance active")
    }
    if pool.Health == "degraded" {
        // ... lógica de permisos en el handler
    }
}
```

**Pattern correcto**:
```go
// BIEN — política centralizada
func handleReplaceDevice(...) {
    if allowed, code := policy.AllowsWithReason(pool, OpTypeReplaceDevice); !allowed {
        return jsonErrorWithCode(403, code, "Operation not permitted")
    }
    return service.ReplaceDevice(...)
}
```

Detalle en `storage_api.md` §5 (PolicyChecker).

---

## 3. Identidad de devices

### 3.1 Serial es la identidad absoluta

**Regla**: el matching de devices entre reboots se hace **por `Serial`** (firmware del disco, absoluto). Nunca por `CurrentPath` o `ByIDPath`.

**Por qué**: `CurrentPath` (`/dev/sdb`) cambia entre reboots según orden de detección del kernel. `ByIDPath` es estable pero puede variar entre controladoras SATA o tras kernel updates. `Serial` está grabado en firmware del disco — no cambia hasta que el disco muere físicamente.

**Anti-pattern (PROHIBIDO)**:
```go
// MAL — identidad por current_path
if disk.CurrentPath == "/dev/sdb" {
    pool.AddDisk(disk)
}
```

**Pattern correcto**:
```go
// BIEN — identidad por serial
existing := repo.GetDeviceBySerial(disk.Serial)
if existing != nil {
    // Disco conocido. Actualizar ByIDPath y CurrentPath si cambiaron.
    repo.UpdateDeviceByIDPath(existing.ID, disk.ByIDPath)
    repo.UpdateDeviceCurrentPath(existing.ID, disk.CurrentPath)
}
```

### 3.2 ByIDPath y CurrentPath son cache, no identidad

**Regla**: ninguna función puede decidir nada crítico a partir de `ByIDPath` o `CurrentPath`. Estos campos solo sirven para:
- Invocar comandos del sistema (`mkfs.btrfs /dev/disk/by-id/...`)
- Mostrar información al usuario en la UI

**Si una función toma una decisión a partir de `CurrentPath`, es un bug**.

### 3.3 Discos sin serial no se gestionan

**Regla**: si un disco no expone serial (extremadamente raro, USB baratos sin firmware), NimOS **no lo gestiona**. Se muestra advertencia al usuario y no se permite añadir a pools.

**Por qué**: sin identidad absoluta, no podemos garantizar que el disco que reaparece tras reboot sea el mismo que estaba antes. Para algo que se llama "fiable y responsable", esto es no negociable.

---

## 4. No borrar a ciegas

**Regla**: ninguna función puede invocar `os.RemoveAll`, `wipefs`, `dd`, `mkfs` (u operación destructiva equivalente) sobre un recurso del sistema sin haber verificado primero que ese recurso pertenece a NimOS.

**Por qué**: un bug de parsing, un string vacío, una ruta mal construida, y una operación destructiva borra el filesystem del sistema operativo. Pasa más a menudo de lo que la gente reconoce. Los guardianes defensivos cuestan microsegundos y previenen pérdida de datos catastrófica.

### 4.1 Borrado de directorios

**Pattern correcto** para borrar un directorio de pool:

```go
// safeRemovePoolDir verifica que el directorio pertenece a NimOS
// antes de borrarlo.
func safeRemovePoolDir(path string) error {
    // 1. Verificar que existe el identity file
    identityPath := filepath.Join(path, ".nimos-pool.json")
    if _, err := os.Stat(identityPath); os.IsNotExist(err) {
        return fmt.Errorf("refusing to remove %s: no .nimos-pool.json found", path)
    }

    // 2. Verificar que el contenido es el esperado
    entries, err := os.ReadDir(path)
    if err != nil {
        return err
    }
    if len(entries) > expectedMaxEntries {
        return fmt.Errorf("refusing to remove %s: unexpected content (%d entries)", path, len(entries))
    }

    // 3. Solo entonces, borrar
    return os.RemoveAll(path)
}
```

### 4.2 Wipe de discos

**Regla**: antes de hacer `wipefs` sobre un disco, verificar:
- El disco no es el disco de boot del sistema
- El disco no está montado actualmente
- El usuario ha confirmado explícitamente (en el contexto de operación destructiva)

### 4.3 Identity file

**Regla**: el archivo `.nimos-pool.json` se crea al inicializar un pool y se mantiene durante toda su vida. Su presencia es la **señal canónica** de que un directorio/filesystem pertenece a NimOS.

Si quieres "marcar" un filesystem como NimOS, escribir este archivo. Si quieres "verificar" si un filesystem es NimOS, leer este archivo. No usar otros métodos (nombre del directorio, presencia de subdirectorios, etc.).

---

## 5. Atomicidad en SQLite

### 5.1 PRAGMA foreign_keys = ON obligatorio

**Regla**: la conexión SQLite **siempre** se abre con foreign keys activadas. Sin esto, los CASCADE y RESTRICT del schema son decorativos.

```go
db, err := sql.Open("sqlite",
    dbPath + "?_journal_mode=WAL&_busy_timeout=10000&_foreign_keys=ON")
```

**Verificación en tests**:
```go
// Si las FK están activadas, esto debe fallar
_, err := db.Exec(`INSERT INTO storage_pool_devices (pool_id, device_id, added_at)
                   VALUES ('fake', 'fake', ?)`, now)
assert.Error(t, err, "FK should have rejected this")
```

### 5.2 Mutaciones multi-tabla en transacción

**Regla**: cualquier operación que toque más de una tabla **debe** ejecutarse dentro de una transacción SQLite explícita.

**Anti-pattern (PROHIBIDO)**:
```go
// MAL — sin transacción, puede quedar inconsistente si falla a la mitad
repo.CreatePool(ctx, nil, pool)
repo.AssignDeviceToPool(ctx, nil, pool.ID, device.ID)
repo.SetPoolCapabilities(ctx, nil, pool.ID, caps)
```

**Pattern correcto**:
```go
// BIEN — todo dentro de transacción
tx, err := db.BeginTx(ctx, nil)
if err != nil { return err }
defer tx.Rollback()  // safe: no-op si commit ya pasó

if err := repo.CreatePool(ctx, tx, pool); err != nil { return err }
if err := repo.AssignDeviceToPool(ctx, tx, pool.ID, device.ID); err != nil { return err }
if err := repo.SetPoolCapabilities(ctx, tx, pool.ID, caps); err != nil { return err }
if err := repo.IncrementGlobalGeneration(ctx, tx); err != nil { return err }

return tx.Commit()
```

### 5.3 Generation incrementa en cada mutación

**Regla**: toda mutación (INSERT, UPDATE, DELETE en cualquier tabla del módulo storage) incrementa:
1. La `generation` de la entidad afectada (Pool.generation, Device.generation)
2. El `global_generation` de `storage_metadata`

Esto se hace dentro de la misma transacción que la mutación.

**Helper centralizado**:
```go
// incrementGeneration debe llamarse al final de cada mutación, dentro de tx
func (r *StorageRepo) incrementGeneration(tx *sql.Tx, entityTable, entityID string) error {
    // 1. Incrementar generation de la entidad
    _, err := tx.Exec(fmt.Sprintf(`UPDATE %s SET generation = generation + 1 WHERE id = ?`,
                                  entityTable), entityID)
    if err != nil { return err }

    // 2. Incrementar global_generation
    _, err = tx.Exec(`UPDATE storage_metadata
                      SET value = CAST(CAST(value AS INTEGER) + 1 AS TEXT)
                      WHERE key = 'global_generation'`)
    return err
}
```

---

## Resumen

| # | Invariante | Aplicación |
|---|---|---|
| 1 | JSON es payload, no entidad | Entidades en SQLite. JSON solo en `Operation.Data` |
| 2 | Policy separado de Storage | `policy.Allows()` antes de `service.Execute()` |
| 3.1 | Identidad por Serial | Matching de devices por `serial`, nunca `current_path` |
| 3.2 | ByIDPath y CurrentPath son cache | No decidir nada crítico a partir de estos campos |
| 3.3 | Sin serial no se gestiona | Disco sin serial → advertencia, no añadir a pool |
| 4.1 | Borrado de directorios protegido | `safeRemovePoolDir` verifica `.nimos-pool.json` |
| 4.2 | Wipe de discos verificado | Comprobar boot disk, mount, confirmación |
| 4.3 | Identity file es la señal canónica | `.nimos-pool.json` marca propiedad NimOS |
| 5.1 | PRAGMA foreign_keys = ON | Obligatorio en cadena de conexión |
| 5.2 | Mutaciones multi-tabla en transacción | `BeginTx` + defer Rollback + Commit |
| 5.3 | Generation incrementa en cada mutación | Helper centralizado dentro de tx |

---

*Documento generado en Fase 1 del refactor Beta 8.
Las invariantes son no negociables. Si encuentras código que las viola, es un bug.*
