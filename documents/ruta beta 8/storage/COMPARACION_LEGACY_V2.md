# Comparación endpoint-por-endpoint Legacy vs V2

**Generado**: auditoría 17/05/2026 madrugada

---

## LEYENDA

```
✅ = existe y funcional
⚠ = existe pero es STUB o incompleto
❌ = NO existe
🔥 = consumido por la UI hoy
🎯 = añadido en Bloque C (C1, C2, C3) hoy
```

---

## A. ENDPOINTS GET (lectura)

| Path | Legacy | V2 | UI usa | Notas |
|------|--------|-----|--------|-------|
| `/storage` o `/storage/pools` | ✅🔥 | ✅ (`/v2/pools`) | legacy | Settings.svelte sí usa v2 |
| `/storage/disks` | ✅🔥 | ✅ (`/v2/disks`) | legacy | |
| `/storage/status` | ✅🔥 | ✅ (`/v2/status`) | legacy | |
| `/storage/alerts` | ✅🔥 | ✅ (`/v2/alerts`) | legacy | |
| `/storage/capabilities` | ✅🔥 | ✅ (`/v2/capabilities`) | legacy | |
| `/storage/health` | ✅ | ❌ | nadie | endpoint legacy huérfano |
| `/storage/restorable` | ⚠🔥 stub | ⚠ stub | legacy | sustituido por /observed (C3) |
| `/storage/observed` | ✅🔥🎯 | ❌ | legacy | **FALTA en v2** ⚠ |
| `/storage/snapshots` | ✅🔥 | ✅ (`/v2/snapshots`) | legacy | |
| `/storage/scrub/status` | ✅🔥 | ✅ (`/v2/scrub/status`) | legacy | |
| `/storage/resilver/status` | ✅ | ❌ | nadie | huérfano |
| `/storage/datasets` | ✅ | ❌ | nadie | huérfano |
| `/storage/v2/devices` | ❌ | ✅ | nadie | **solo en v2** — no replicado en legacy |
| `/storage/v2/operations` | ❌ | ✅ | nadie | **solo en v2** — journal de operaciones |
| `/storage/v2/generation` | ❌ | ✅ | nadie | **solo en v2** — long-polling |

---

## B. ENDPOINTS POST (mutaciones)

| Path | Legacy | V2 | UI usa | Notas |
|------|--------|-----|--------|-------|
| `/storage/pool` (create) | ✅🔥🎯 | ✅ (`POST /v2/pools`) | legacy | Legacy tiene C3.4 doble intención; v2 no |
| `/storage/scan` | ✅🔥 | ✅ (`/v2/scan`) | legacy | |
| `/storage/wipe` | ✅🔥 | ✅ (`/v2/wipe`) | legacy | |
| `/storage/pool/destroy` | ✅🔥 | ✅ (`/v2/pool/destroy` o `DELETE /v2/pools/{id}`) | legacy | v2 tiene 2 formas (by-name + REST-ful) |
| `/storage/pool/export` | ✅🔥 | ✅ (`/v2/pool/export`) | legacy | |
| `/storage/pool/restore` | ⚠🔥 stub | ⚠ stub | legacy | sustituido por /pool/import (C3) |
| `/storage/pool/import` | ✅🔥🎯 | ❌ | legacy | **FALTA en v2** ⚠ |
| `/storage/pool/replace-disk` | ✅ | ✅ (`POST /v2/pools/{id}/devices/{did}/replace`) | nadie | |
| `/storage/pool/detach-disk` | ✅ | ✅ (`DELETE /v2/pools/{id}/devices/{did}`) | nadie | |
| `/storage/pool/attach-disk` | ✅ | ✅ (`POST /v2/pools/{id}/devices`) | nadie | |
| `/storage/pool/resilver-status` | ✅ | ❌ | nadie | huérfano |
| `/storage/backup` | ✅ | ❌ | nadie | huérfano |
| `/storage/snapshot` (crear) | ✅🔥 | ❌ | legacy | **FALTA en v2** ⚠ |
| `/storage/snapshot/rollback` | ✅🔥 | ❌ | legacy | **FALTA en v2** ⚠ |
| `/storage/scrub` (start) | ✅🔥 | ✅ (`/v2/scrub`) | legacy | |
| `/storage/dataset` | ✅ | ❌ | nadie | huérfano |
| `/storage/v2/pools/{id}/rename` | ❌ | ✅ | nadie | **solo v2** |
| `/storage/v2/pools/{id}/set-compression` | ❌ | ✅ | nadie | **solo v2** |
| `/storage/v2/pools/{id}/convert-profile` | ❌ | ✅ | nadie | **solo v2** |

---

## C. RESUMEN DE GAPS

### Gaps del V2 (lo que falta para reemplazar legacy)

```
ENDPOINTS A AÑADIR EN V2 (críticos):
   1. GET  /v2/observed              ← C1
   2. POST /v2/pools/import          ← C3.1 import flow
   3. POST /v2/snapshots             ← crear snapshot (UI lo usa)
   4. POST /v2/snapshots/{id}/rollback ← rollback (UI lo usa)

INTEGRACIONES PENDIENTES:
   5. POST /v2/pools (createPool) debe devolver 409 con DISK_HAS_FILESYSTEM
      tal como hace C3.4 en legacy. El service ya tiene la info.

DETALLE: el v2 expone `POST /v2/pool/restore` que apunta a un stub. Cuando
        eliminemos restorable/restore, también hay que quitar este endpoint
        del v2.
```

### Gaps del LEGACY (lo que solo tiene v2)

```
FUNCIONALIDAD V2 SIN EQUIVALENTE LEGACY:
   1. GET  /v2/devices       — list cruda de devices
   2. GET  /v2/operations    — journal de operations en curso/históricas
   3. GET  /v2/generation    — generation counter para long-polling

   ESTAS SON FEATURES ARQUITECTÓNICAS QUE LEGACY NUNCA TENDRÁ.
   Si decidimos migrar a v2, ganamos automáticamente.

OPERACIONES POOL-LEVEL EXTRA:
   4. POST /v2/pools/{id}/rename
   5. POST /v2/pools/{id}/set-compression
   6. POST /v2/pools/{id}/convert-profile

   ESTAS SON OPERACIONES DE GESTIÓN QUE LA UI NO USA HOY pero
   son funcionalidades reales del service (Beta 9 las necesitará).
```

---

## D. CALIDAD ARQUITECTÓNICA

### Patrón legacy (`storage_http.go`)

```go
func handleStorageRoutes(w http.ResponseWriter, r *http.Request) {
    if method == "GET" {
        switch urlPath {
        case "/api/storage/pools":
            jsonOk(w, getStoragePoolsGo())
        case "/api/storage/disks":
            jsonOk(w, detectStorageDisksGo())
        // ... 14 cases más
        }
    } else if method == "POST" {
        body, _ := readBody(r)  // map[string]interface{}
        switch urlPath {
        case "/api/storage/pool":
            jsonOk(w, createPoolBtrfs(body))  // todo dinámico
        // ... 14 cases más
        }
    }
}
```

**Características**:
- ✅ Compacto, fácil de leer linealmente
- ✅ Cada case es trivial de extender
- ❌ NO usa structs de request — todo `bodyStr(body, "name")`
- ❌ NO valida tipos en el body
- ❌ Siempre devuelve 200, mete error en body — semánticamente incorrecto
- ❌ NO tests unitarios del handler — solo cobertura indirecta
- ⚠ C3.4 introdujo la primera excepción: 409 para DISK_HAS_FILESYSTEM

### Patrón v2 (`storage_http_v2.go`)

```go
type StorageHTTPHandler struct {
    service *StorageService
}

func (h *StorageHTTPHandler) createPool(w http.ResponseWriter, r *http.Request) {
    var req CreatePoolRequest
    if err := decodeJSONBody(r, &req); err != nil {
        writeError(w, ErrCodeBadRequest, err.Error())
        return
    }
    op, err := h.service.CreatePool(r.Context(), req)
    if err != nil {
        writeServiceError(w, err)
        return
    }
    writeData(w, http.StatusOK, op)
}
```

**Características**:
- ✅ Handler con dependencias inyectadas (`service`) — testeable unitariamente
- ✅ Structs tipados para request (`CreatePoolRequest`)
- ✅ `decodeJSONBody` valida JSON antes de tocar el service
- ✅ `writeServiceError` mapea errores semánticos a HTTP codes correctos
- ✅ Códigos HTTP correctos: 200/201/400/404/409/500
- ✅ Wrapper de respuesta estándar: `{data: ...}` o `{error: {code, message}}`
- ✅ `methodNotAllowed` con header `Allow` (RFC-compliant)
- ✅ Tests directos del handler con 982 líneas
- ✅ REST-ful: recursos en URL, verbo HTTP correcto

### Veredicto puro arquitectónico

```
                  Legacy     V2
                  ──────     ───
Diseño            4/10       8/10
Modularidad       2/10       9/10
Testabilidad      3/10       9/10
HTTP semantics    4/10       9/10
Producción        9/10       1/10
Validación input  3/10       8/10
Documentación     3/10       8/10
```

---

## E. ESFUERZO PARA COMPLETAR V2

Para que v2 sea drop-in replacement del legacy:

### Endpoints a añadir (4 críticos)

```
1. handleObserved        (GET /v2/observed)               ~50 líneas
2. handleImport          (POST /v2/pools/import)          ~80 líneas
   · Reusa importPoolBtrfs (storage_btrfs_import.go)
3. handleSnapshotCreate  (POST /v2/snapshots)             ~30 líneas
4. handleSnapshotRollback(POST /v2/snapshots/rollback)    ~30 líneas
```

### Integraciones

```
5. createPool del v2 debe devolver 409 con DISK_HAS_FILESYSTEM
   · El service.CreatePool() ya invoca preFlightCheck internamente
   · Solo hay que mapear ErrDiskHasFilesystem → ServiceError
   · ~20 líneas
```

### Cleanup del propio v2

```
6. Eliminar handlePoolRestore (es stub, sustituido por /import)
7. Eliminar handleRestorable (es stub, sustituido por /observed)
```

### Total para completar v2

```
Líneas a añadir:    ~200
Líneas a eliminar:  ~50  (stubs)
Tests a añadir:     ~150 líneas para los 4 nuevos endpoints
Tiempo estimado:    ~90 minutos
```

---

## F. RECOMENDACIÓN HONESTA

### Opción 1 — Completar V2 ahora (tu propuesta)

**Pros**:
- Mejor arquitectura final
- Status codes HTTP correctos en todo
- Validación tipada en request bodies
- Tests directos del handler
- Long-polling con /generation listo
- /devices y /operations gratis

**Contras**:
- ~5 horas totales repartidas (completar v2 + migrar UI + eliminar legacy)
- Riesgo bajo pero no nulo durante migración UI

### Opción 2 — Quedarse con legacy mejorado

**Pros**:
- Cero riesgo
- ~3 horas totales (limpieza + mejoras incrementales)
- Mantiene C3 sin tocar

**Contras**:
- Legacy nunca será arquitectónicamente bonito
- Sigues con switch case monolítico
- HTTP semantics nunca completos

### Mi voto honesto

**Opción 1 (completar v2)**.

Razones:
1. **El v2 ya está al 80%**. Es absurdo tirarlo cuando le faltan 4 endpoints.
2. **Legacy en paralelo es red de seguridad real**: cualquier fallo en v2 durante migración → la UI cambia URL y vuelve a legacy.
3. **El esfuerzo de migrar UI es similar** en ambas opciones (en una migras al v2 nuevo, en otra mejoras el legacy). Mejor hacerlo una vez bien.
4. **Beta 8.1 se cierra LIMPIA** — sin dos stacks, sin huérfanos, sin deuda.
5. **El v2 tiene cosas que legacy nunca tendrá**: operations journal, generation counter. Estas son las bases de un sistema profesional.

### Pero atención

Esto es trabajo cuidadoso. **No empezar a las 1 AM con sueño**. Mañana en sesión planificada.

---

## G. PLAN CONCRETO SI VAMOS A POR V2

```
Sesión 1 · Completar V2  (~90 min)
   Bloque V2.1 — Endpoints faltantes:
      · handleObserved
      · handleImport
      · handleSnapshotCreate
      · handleSnapshotRollback
   Bloque V2.2 — Integración C3.4 en createPool v2
   Bloque V2.3 — Eliminar stubs handlePoolRestore + handleRestorable
   Verificación: build, tests, race, vet

Sesión 2 · Migrar UI a V2 (~120 min)
   StorageApp.svelte:
      · loadAll → /v2/pools, /v2/disks, /v2/status, /v2/alerts, /v2/capabilities, /v2/observed
      · submitImport → /v2/pools/import
      · submitDestroyOrphan → wipe via /v2/wipe
   CreatePoolWizard.svelte:
      · POST /v2/pools con doble intención
   DestroyPoolWizard.svelte:
      · DELETE /v2/pools/{id}
   ExportPoolWizard.svelte:
      · POST /v2/pool/export
   Settings.svelte:
      · ya usa /v2/pools ✓
   Test E2E completo

Sesión 3 · Eliminar Legacy (~60 min)
   · Verificar UI sin referencias a /api/storage/* (solo /v2/*)
   · Borrar storage_http.go
   · Borrar handlers legacy huérfanos (resilver, datasets, backup)
   · Borrar restorable, restorePoolFromIdentity, scanForRestorablePoolsGo
   · Limpiar funciones huérfanas (~11 detectadas)
   · Eliminar writePoolIdentity y .nimos-pool.json
   · Verificar todo

Total: ~4-5 horas en 3 sesiones planificadas
```

---

## H. RIESGOS Y MITIGACIONES

```
Riesgo 1: bug en v2 durante migración UI
  Mitigación: legacy queda corriendo en paralelo. Rollback en 1 commit.

Riesgo 2: tests del v2 cubren rutas pero no producción real
  Mitigación: deploy intermedio entre sesión 1 y 2, prueba manual del v2 con curl.

Riesgo 3: funcionalidad de Snapshots NO está en service v2
  Verificación necesaria: ¿el service expone CreateSnapshot/RollbackSnapshot?
  Si no → más trabajo del estimado.
```

---

**FIN DE AUDITORÍA.**
