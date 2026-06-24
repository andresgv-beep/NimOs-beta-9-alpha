# NimOS Beta 8 — Storage State Machines

**Autor**: Andrés + Claude Opus 4.7 — Mayo 2026
**Versión**: 1.1 (Fase 1 — diseño)
**Ámbito**: lifecycles de Pool, Operation y Device con autoridad y validación

---

## 1. Propósito

Las tres entidades centrales de storage (Pool, Operation, Device) tienen un ciclo de vida con estados y transiciones permitidas. Documentarlas aquí sirve para:

1. **Onboarding futuro**: si algún día hay más manos, este doc es lo primero que leen
2. **Tests de validación**: cada transición permitida es testeable; cada prohibida también
3. **Debugging**: si algo está en un estado raro, este doc te dice si es válido o un bug
4. **Coherencia con el código**: los enums en `storage_api.md` deben coincidir con los estados aquí

**Beta 8 implementa**: las transiciones marcadas con ✅. Las marcadas con 🔒 están en el schema (CHECK constraint) pero el runtime no las usa todavía — se activarán en Beta 9+.

---

## 2. Principio: invariantes ejecutables

Las transiciones documentadas en este archivo **no son solo narrativa**. Cada una está implementada como entrada en un mapa Go (`permittedPoolTransitions`, `permittedOperationTransitions`, `permittedDeviceTransitions`) que el código consulta antes de ejecutar la transición.

Patrón:

```go
// Antes de aplicar una transición de control_state
if err := ValidatePoolTransition(currentState, newState); err != nil {
    return err  // TransitionError con código semántico
}
// Solo entonces ejecutar
```

**Consecuencia importante**: este documento y el código **no pueden divergir**. Si la tabla aquí dice "managed → observed permitido", el código lo permite. Si añades una línea aquí, añade una entrada al map. Si quitas una, quítala del map. Sin esto, en 6 meses la doc y el runtime dirán cosas distintas y nacerán bugs de transición ilegal.

Firmas Go en `storage_api.md` §X (sección "Transition validators").

---

## 3. Pool Lifecycle

Un Pool puede estar en uno de 5 estados de `control_state`. Beta 8 usa solo 2 en runtime.

### 3.1 Diagrama

```
                            ┌──────────────────────────────┐
                            │                              │
            ┌── user adopts ─┴──┐                          │
            │                   │                          │
            ▼                   │                          │
       ┌─────────┐    user      │    create operation      │
       │observed │  releases    │       (NimOS)            │
   ┌─→ │         │ ─────────→ ┌─┴───────┐                  │
   │   └─────────┘            │ managed │ ◀────────────────┘
   │      ▲                   └─────────┘
   │      │                        │
   │      │ NimOS detects          │ destroy operation
   │      │ existing BTRFS         ▼
   │      │                  ┌──────────┐
   │      │                  │ removed  │ (no row in DB)
   │      │                  └──────────┘
   │      │
   │   ┌──┴───────────┐
   │   │   <new>      │
   │   └──────────────┘
   │
   │
   │   🔒 Estados reservados para Beta 9+:
   │
   │   ┌──────────┐    ┌──────────┐    ┌──────────┐
   └─→ │ imported │    │ foreign  │    │ recovery │
       └──────────┘    └──────────┘    └──────────┘
       (de identity   (FS no       (en proceso de
        file)         reconocido)    reconciliación)
```

### 3.2 Transiciones permitidas (con autoridad)

| De | A | Trigger | Autoridad | Side effects físicos | Transaccional | Beta 8 |
|---|---|---|---|---|---|---|
| `<new>` | `managed` | `CreatePool` (usuario crea pool) | StorageService.CreatePool | Sí (wipe, mkfs, mount) | Sí | ✅ |
| `<new>` | `observed` | NimOS detecta BTRFS preexistente al arranque | Boot reconciler | No | Sí | ✅ |
| `managed` | `<removed>` | `DestroyPool` (usuario destruye pool) | StorageService.DestroyPool + Policy | Sí (unmount, wipefs) | Sí | ✅ |
| `managed` | `observed` | Usuario "libera" pool | StorageService + Policy (check no shares active) | No | Sí | ✅ |
| `observed` | `managed` | Usuario "adopta" pool | StorageService + Policy (verify BTRFS) | Sí (write identity file si falta) | Sí | ✅ |
| `<new>` | `imported` | Recovery desde identity file | Recovery reconciler | No | Sí | 🔒 |
| `<new>` | `foreign` | Detección de FS no-BTRFS | Boot reconciler | No | Sí | 🔒 |
| `managed` | `recovery` | Crash con operación a medias | RecoverPendingOperations | No (solo marca estado) | Sí | 🔒 |
| `recovery` | `managed` | Reconciliación exitosa | Recovery service | Posible (reconstruir identity file) | Sí | 🔒 |
| `imported` | `managed` | Usuario adopta pool importado | StorageService.AdoptPool | Sí (write identity file) | Sí | 🔒 |

**Convención**:
- **Autoridad**: quién en el código tiene permiso de iniciar la transición
- **Side effects físicos**: si la transición modifica el filesystem real (wipe, mount, mkfs) o solo metadata en SQLite
- **Transaccional**: si la transición debe completarse dentro de una transacción SQLite (siempre `sí` en Beta 8)

### 3.3 Transiciones prohibidas (auditadas por código)

`ValidatePoolTransition()` devuelve error con código `transition_not_permitted` para estas:

| De | A | Razón |
|---|---|---|
| `observed` | `<removed>` | NimOS no destruye lo que no gestiona |
| `foreign` | `managed` | Filesystem no soportado, no se puede gestionar |
| `foreign` | cualquier otro | Estado terminal hasta intervención manual |
| Cualquier | `<new>` | `<new>` no es un estado persistido |

### 3.4 Side effects de transiciones principales

**`<new>` → `managed` (CreatePool)**:
- Wipe de discos (con guard de `.nimos-pool.json` si existe en mountpoint candidato)
- `mkfs.btrfs` con profile elegido
- Mount en `/nimos/pools/<name>`
- Crear `.nimos-pool.json` en root del filesystem
- INSERT en `storage_pools`, `storage_pool_devices`, `storage_pool_capabilities`
- Generation incrementa

**`managed` → `<removed>` (DestroyPool)**:
- Verificar que no hay shares/services activos (Policy layer)
- Unmount del filesystem
- `wipefs` de los discos
- DELETE de `storage_pools` → CASCADE limpia pool_devices y pool_capabilities
- Operations en `storage_operations` quedan con pool_id NULL (auditoría)

**`managed` → `observed` (liberación)**:
- Cambio de metadata, sin operaciones físicas en el filesystem
- Capabilities se mantienen pero policy layer las invalida (observed no muta)
- Operación tipo `control_state_change` en histórico

**`observed` → `managed` (adopción)**:
- Verificar que el FS sigue siendo BTRFS válido
- Leer/escribir `.nimos-pool.json` si no existe
- Cambio de metadata
- Operación tipo `control_state_change` en histórico

### 3.5 Invariantes

- Un pool nunca puede tener `control_state` fuera del CHECK del schema
- Un pool en `observed` o `foreign` **no permite mutaciones** (policy layer lo enforce)
- Si `control_state = managed` y no existe `.nimos-pool.json` en el mountpoint → es un estado inconsistente → marcar como `recovery` y notificar al usuario (Beta 9)

### 3.6 📝 Nota Beta 9: subdivisión de `recovery`

El estado `recovery` cubre dos casos distintos que en Beta 9 se diferenciarán:

- **Physical recovery**: BTRFS está mal (filesystem corrupto, scrub detectó errores, disco fallando físicamente)
- **Logical recovery**: BTRFS OK pero la DB está inconsistente con la realidad (identity file falta, operation perdida tras crash, ByIDPath cambió y matching parcial)

Beta 8 reserva `recovery` como estado único. Beta 9 lo subdivide (probablemente como `recovery_physical` y `recovery_logical`) cuando se active el runtime para estos estados.

---

## 4. Operation Lifecycle

Una Operation puede estar en uno de 6 estados de `status`. Todos están en Beta 8.

### 4.1 Diagrama

```
                          ┌──────────┐
                          │ <new>    │
                          └────┬─────┘
                               │
                               │ INSERT en storage_operations
                               ▼
                          ┌──────────┐
            ┌────────────│ pending  │
            │             └────┬─────┘
            │                  │ daemon empieza la op
            │                  ▼
            │             ┌─────────────┐
            │             │ in_progress │ ◀─── stays here while running
            │             └──┬──────┬───┘
            │                │      │
            │     success    │      │   failure
            │                ▼      ▼
            │           ┌──────────┐    ┌────────┐
            │           │completed │    │ failed │
            │           └──────────┘    └────┬───┘
            │                                │
            │                                │ has rollback steps?
            │                                ▼
            │                          ┌─────────────┐
            │                          │rolled_back  │
            │                          └─────────────┘
            │
            │ user cancels before starting
            ▼
       ┌──────────┐
       │cancelled │
       └──────────┘
```

### 4.2 Estados y transiciones (con autoridad)

| De | A | Trigger | Autoridad | Side effects físicos | Beta 8 |
|---|---|---|---|---|---|
| `<new>` | `pending` | INSERT al crear la op | StorageService (cualquier Command) | No | ✅ |
| `pending` | `in_progress` | Worker recoge la op para ejecutar | Operation executor (goroutine) | Sí (inicia comando BTRFS) | ✅ |
| `pending` | `cancelled` | Usuario cancela antes de empezar | StorageService.CancelOperation + Policy | No | ✅ |
| `in_progress` | `completed` | Comando BTRFS termina con éxito | Operation executor | Ya completados | ✅ |
| `in_progress` | `failed` | Comando BTRFS devuelve error o timeout | Operation executor | Ya ejecutados (con error) | ✅ |
| `failed` | `rolled_back` | Steps de rollback ejecutados con éxito | Rollback runner (`runSteps` reverse) | Sí (undo de steps) | ✅ |

### 4.3 Transiciones prohibidas

`ValidateOperationTransition()` rechaza:

| De | A | Razón |
|---|---|---|
| `completed` | cualquier otro | Estado final inmutable |
| `failed` | `completed` | Una op no puede "des-fallarse" |
| `rolled_back` | cualquier otro | Estado final inmutable |
| `cancelled` | cualquier otro | Estado final inmutable |
| `in_progress` | `pending` | No se retrocede en el ciclo |
| `in_progress` | `cancelled` | Cancelación de op en curso no soportada en Beta 8 |

### 4.4 Tiempos esperados por tipo

**Sync ops** (rename, change_role, set_compression, set_scrub_policy):
- `pending` y `in_progress` duran ~milisegundos
- En la práctica el cliente ve directamente `completed`

**Async ops** (create_pool, replace_device, etc.):
- `pending`: 0-segundos (típicamente la goroutine arranca enseguida)
- `in_progress`: segundos a horas
  - create_pool: 10-60 segundos
  - replace_device: minutos a horas (depende del tamaño)
  - scrub: horas (depende del tamaño y velocidad de disco)

### 4.5 Recovery tras crash

Si el daemon crashea con operations en estado `in_progress`, al rearrancar:

1. `RecoverPendingOperations()` busca todas con `status IN ('pending', 'in_progress')`
2. Para cada una, consulta a BTRFS si la operación física sigue corriendo:
   - **BTRFS dice "running"** → mantener en `in_progress`, monitorizar
   - **BTRFS dice "stopped/finished"** → verificar resultado, marcar `completed` o `failed`
   - **BTRFS dice "no operation"** → marcar `failed` con `error_code = "crashed_during_operation"`, intentar rollback si la op tenía Steps con Undo definidos
3. Las ops sin Steps de rollback quedan en `failed` y requieren intervención manual del usuario

Detalle completo en `storage_api.md` §4.3 (`RecoverPendingOperations`).

### 4.6 Invariantes

- Una operation **siempre** tiene `started_at`
- `completed_at` solo existe en estados finales (`completed`, `failed`, `rolled_back`, `cancelled`)
- `error` y `error_code` solo se rellenan si `status = failed` o `rolled_back`
- INV-1 (schema): solo 1 layout-op activa por pool a la vez
- INV-2 (schema): solo 1 scrub activo por pool a la vez

---

## 5. Device Lifecycle

Un Device representa un disco físico. Su lifecycle es más sencillo que Pool/Operation, pero importa porque trackea presencia/ausencia entre reboots.

### 5.1 Diagrama

```
                ┌──────────┐
                │ <unknown>│ (disco físico no visto aún)
                └────┬─────┘
                     │
                     │ scanAndRegisterDevices ve un disco con serial
                     ▼
                ┌──────────┐
        ┌─────→ │ detected │ ←──────┐
        │       └────┬─────┘        │
        │            │              │
        │            │ user lo      │ user lo quita del pool
        │            │ asigna a     │ (RemoveDevice OK)
        │            │ un pool      │
        │            ▼              │
        │       ┌──────────┐        │
        │       │ assigned │ ──────┘
        │       └────┬─────┘
        │            │
        │            │ disco desaparece del bus
        │            │ (unplug, fallo)
        │            ▼
        │       ┌──────────┐
        │       │ missing  │
        │       └────┬─────┘
        │            │
        │            │ disco reaparece
        │            │ (replug, recovery)
        │            ▼
        │       ┌──────────┐
        │       │ assigned │ (vuelve)
        │       └──────────┘
        │
        │  reaparición tras unplug breve
        │  con serial conocido
        └────────────────
```

### 5.2 Estados (proyectados, no materializados)

A diferencia de Pool y Operation, el "estado" de un Device **NO está en una columna `state`**. Se proyecta de campos existentes:

| Estado | Cómo se proyecta | Significado |
|---|---|---|
| `detected` | Row existe, sin entrada en `storage_pool_devices` | Disco visto físicamente, no asignado |
| `assigned` | Row existe, hay entrada en `storage_pool_devices` | En un pool |
| `missing` | Row existe, `last_seen_at` > N minutos atrás | Esperado pero no visto |
| `<unknown>` | No hay row con ese serial | Aún no detectado |

**Decisión de diseño**: el estado es proyección, no materialización. Ventaja: cero riesgo de inconsistencia entre columnas. Desventaja: cada query lo computa.

### 5.3 📝 Nota Beta 9: tensión entre proyección y UX

`missing` se proyecta de `last_seen_at`, pero **para el usuario es un estado real**. Cuando el dashboard muestra "Disco missing desde hace 2 horas, alerta crítica", el usuario lo percibe como tan real como `assigned` o `detected`.

Esto va a crear tensión cuando crezca la cantidad de devices o cuando las queries de UI se hagan lentas:

- **Proyección pura** (Beta 8): cada query computa el estado. Simple, sin riesgo. Coste: O(devices) por query.
- **Proyección materializada** (Beta 9+): añadir columna `health_state` actualizada por el background reconciler cada N segundos. Más rápido para queries de UI pero requiere mantener coherencia.

Beta 8 va con proyección pura porque tenemos pocos devices y queries baratas. Beta 9 evaluará si materializar tiene sentido.

### 5.4 Transiciones permitidas (con autoridad)

| De | A | Trigger | Autoridad | Side effects físicos | Beta 8 |
|---|---|---|---|---|---|
| `<unknown>` | `detected` | Primera vez que se ve el disco | scanAndRegisterDevices (background) | No | ✅ |
| `detected` | `assigned` | `AddDeviceToPool` exitosa | StorageService.AddDeviceToPool | Sí (btrfs device add) | ✅ |
| `assigned` | `detected` | `RemoveDeviceFromPool` exitosa | StorageService.RemoveDeviceFromPool | Sí (btrfs device remove + rebalance) | ✅ |
| `assigned` | `missing` | Disco no visto en N ciclos | Background reconciler | No | ✅ |
| `detected` | `missing` | Disco libre no visto en N ciclos | Background reconciler | No | ✅ |
| `missing` | `assigned` | Disco reaparece (matching por serial) | Background reconciler | No | ✅ |
| `missing` | `detected` | Disco reaparece estando libre | Background reconciler | No | ✅ |

### 5.5 Cuándo se considera `missing`

Si un disco que está en la DB no aparece en el scan físico durante **3 ciclos consecutivos del background loop** (por defecto cada 5 min → ~15 min sin verse), se considera `missing`.

El usuario ve un badge en la UI. Si el disco estaba en un pool, el pool se marca como `degraded` (BTRFS lo detecta automáticamente).

### 5.6 Matching tras reaparición

Cuando un disco reaparece tras estar `missing`, el matching se hace **por serial primero**. El `by_id_path` puede haber cambiado (ej. cambió de puerto SATA). Esto se gestiona en `scanAndRegisterDevices`:

```
para cada disco físico visto:
    if disco.serial existe en DB:
        UPDATE storage_devices SET
            current_path = disco.current_path,
            by_id_path = disco.by_id_path,        ← se actualiza si cambió
            last_seen_at = now,
            generation = generation + 1
        WHERE serial = disco.serial
```

### 5.7 No borrar devices jamás

**Principio**: los devices nunca se eliminan automáticamente de `storage_devices`, ni siquiera cuando llevan meses missing.

Justificación:
- **Auditoría**: saber qué discos pasaron por el sistema y cuándo
- **Histórico SMART**: el `smart_history` referencia el device por su id
- **Reaparición**: si el disco se conecta a otra máquina y luego vuelve, lo reconocemos por serial
- **Troubleshooting**: "ese disco con problemas hace 6 meses ¿qué pool tenía?"

Limpieza manual de devices históricos solo se hace por intervención explícita del usuario (futuro endpoint `DELETE /api/storage/devices/:id`, no en Beta 8).

### 5.8 Invariantes

- **El serial nunca cambia** una vez registrado. Si un disco "cambia de serial" es físicamente otro disco
- `current_path` puede cambiar libremente (cache runtime)
- `by_id_path` puede cambiar entre reboots, pero raramente
- Un device en `assigned` no se puede borrar (FK RESTRICT en `storage_pool_devices`)
- Devices nunca se borran físicamente de `storage_devices` salvo intervención manual

---

## 6. Interacción entre lifecycles

Los tres lifecycles no son independientes. Hay puntos de acoplamiento que importan:

### 6.1 Pool ↔ Operation

- Una operación `create_pool` lleva un Pool de `<new>` a `managed` cuando completa
- Una operación `destroy_pool` lleva un Pool de `managed` a `<removed>` cuando completa
- Si una operación falla a la mitad (`failed`), el Pool puede quedar en estado inconsistente → marcar como `recovery` (Beta 9)

### 6.2 Operation ↔ Device

- Una operación `add_device` lleva el Device de `detected` a `assigned` cuando completa
- Una operación `remove_device` lleva el Device de `assigned` a `detected` cuando completa
- Una operación `replace_device` cambia DOS devices simultáneamente: el viejo de `assigned` a `detected`, el nuevo de `detected` a `assigned`

### 6.3 Pool ↔ Device

- Borrar un Pool (CASCADE en pool_devices) deja a los Devices en `detected` automáticamente
- Borrar un Device manualmente está prohibido si está `assigned` (RESTRICT)

### 6.4 Atomicidad

Las transiciones que tocan múltiples entidades **siempre se hacen en una transacción SQLite**. No puede pasar que un `add_device` complete a medias dejando el pool actualizado pero el device en estado inconsistente. Si la operación falla en cualquier paso, la transacción se revierte completamente.

---

## 7. Para tests

Los tests de Fase 2 deben validar:

**Test de transiciones permitidas**: por cada transición de las tablas (§3.2, §4.2, §5.4), simular el trigger y verificar que el estado final es el esperado.

**Test de transiciones prohibidas**: intentar transiciones marcadas como "prohibidas" y verificar que `ValidateXxxTransition()` devuelve `TransitionError` con código `transition_not_permitted`.

**Test de mapa exhaustivo**: por cada par `(estado_origen, estado_destino)` posible (cartesiano de los enums), verificar que `Validate*` da el mismo resultado que dice este documento. Esto detecta divergencias entre doc y código automáticamente.

**Test de invariantes con concurrencia**: lanzar 2 operaciones `add_device` simultáneas al mismo pool y verificar que solo una entra (INV-1 del schema lo rechaza).

**Test de recovery**: insertar manualmente operations en `in_progress`, reiniciar el `StorageService`, verificar que `RecoverPendingOperations` las gestiona correctamente.

**Test de autoridad**: verificar que las transiciones no pueden iniciarse desde fuera de la autoridad permitida (ej. el HTTP handler no puede saltarse el StorageService para cambiar control_state directo).

---

*Documento generado en Fase 1 del refactor Beta 8.*
