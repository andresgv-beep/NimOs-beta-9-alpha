# Auditoría de Refactor — `backup.go`

**Objetivo:** preparar el split de god-file en módulos cohesivos, eliminar el patrón JSON-config legado, y dejar la disciplina del Beta 8 aplicada (schema embebido vía `//go:embed`, FK reales, CHECK constraints, repo dedicado por entidad).

**Estado actual del archivo:** 2837 líneas · 75 funciones · 5 dominios mezclados · 1 god-handler de 527 líneas (`handleBackupRoutes`).

**Filosofía:** no es un parche, es un re-diseño del módulo. La idea es escribir cómo DEBERÍA estar para que cuando lo ataques tengas el blueprint completo y no haya improvisación. Cada decisión queda fundamentada.

---

## 1 · Split propuesto (8 archivos · dominios cohesivos)

```
daemon/
├── backup.go                  (~80 LOC)   façade: tipos compartidos + registro de routers
├── backup_schema.go           (~70 LOC)   //go:embed backup_schema.sql + initBackupSchema()
├── backup_schema.sql          (~120 LOC)  schema canónico con FK + CHECK
├── backup_repo.go             (~600 LOC)  CRUD: devices, jobs, history, mounts
├── backup_pairing.go          (~350 LOC)  discovery + pair tokens + SSH host key + pairWithRemote
├── backup_scheduler.go        (~180 LOC)  startBackupScheduler, checkAndRunDueJobs, executeBackupJob
├── backup_retention.go        (~120 LOC)  applyRetention + applyRetentionBtrfs + extractTimestamp
├── backup_nfs.go              (~250 LOC)  NFS export/import, mount/unmount, /etc/exports management
├── backup_validators.go       (~150 LOC)  validateAddr, validateClientIP, validateBackupPath, etc
└── backup_http.go             (~450 LOC)  handleBackupRoutes split por subrutas
```

**Total:** ~2300 LOC (reducción ~20% al eliminar duplicación de extracción de campos, validación inline, etc.). Cada archivo entre 80-600 LOC, ningún god-file.

**Por qué este split y no otro:** estos 8 grupos son los **dominios reales** del módulo. Cada uno tiene un nivel de abstracción consistente y un ciclo de cambio independiente. Por ejemplo, el día que añadas restic/borg/rsync como backend, sólo tocas `backup_scheduler.go` y `backup_validators.go`. El día que sustituyas NFS por SFTP, sólo `backup_nfs.go`. Ese es el test ácido de un split correcto.

---

## 2 · Schema nuevo (`backup_schema.sql`)

Versión canónica que aplica la disciplina del Beta 8 (FK reales, CHECK, comentarios sin atajos).

```sql
-- =============================================================================
-- NimOS Beta 8 — Backup Module Schema
-- =============================================================================
-- Source of truth de las tablas del módulo backup. Aplicado por
-- initBackupSchema() en arranque del daemon, DESPUÉS de
-- initNimosCoreSchema() (FK a nimos_secrets para pair tokens).
--
-- Reglas:
--   1. PRAGMA foreign_keys = ON OBLIGATORIO.
--   2. Idempotente (IF NOT EXISTS).
--   3. CHECK constraints en todos los enums (type, status, fs_type).
--   4. NO se guardan arrays JSON serializados en TEXT columns. Si una
--      entidad tiene cardinalidad N, va en tabla separada con FK.
-- =============================================================================

PRAGMA foreign_keys = ON;

-- ─── backup_devices ────────────────────────────────────────────────────────
-- Cada fila representa un peer NimOS emparejado.
-- LOGIC-023: per-device flag allow_ip_auth para migración progresiva del
-- IP-fallback. Default 0 (token-only). Admin lo activa per-device durante
-- la transición y lo limpia cuando re-parea.
CREATE TABLE IF NOT EXISTS backup_devices (
    id                    TEXT PRIMARY KEY,
    name                  TEXT NOT NULL,
    addr                  TEXT NOT NULL,
    type                  TEXT NOT NULL DEFAULT 'nas'
        CHECK(type IN ('nas', 'client')),

    -- Pair token: hash del token que ESTE device usa para autenticar SUS requests entrantes
    pair_token_hash       TEXT DEFAULT '',
    -- Pair token saliente: el token del remoto, lo enviamos como X-Pair-Token
    -- al hacer requests a ellos. Cifrado vía nimos_secrets en lugar de TEXT plano.
    outbound_secret_id    TEXT,

    -- SSH host key pinning (LOGIC-021)
    ssh_host_key          TEXT DEFAULT '',

    -- LOGIC-023b: IP-only fallback per-device. Default 0 (secure).
    allow_ip_auth         INTEGER NOT NULL DEFAULT 0 CHECK(allow_ip_auth IN (0,1)),

    -- WireGuard tunnel state
    wg_active             INTEGER NOT NULL DEFAULT 0 CHECK(wg_active IN (0,1)),
    wg_public_key         TEXT DEFAULT '',
    wg_endpoint           TEXT DEFAULT '',
    wg_allowed_ips        TEXT DEFAULT '',
    wg_local_ip           TEXT DEFAULT '',

    created_at            TEXT NOT NULL,

    FOREIGN KEY (outbound_secret_id) REFERENCES nimos_secrets(id) ON DELETE SET NULL
);

-- ─── backup_device_purposes ─────────────────────────────────────────────────
-- Cardinalidad N → tabla aparte (anti-pattern: NO guardar JSON en backup_devices.purposes).
CREATE TABLE IF NOT EXISTS backup_device_purposes (
    device_id  TEXT NOT NULL,
    purpose    TEXT NOT NULL
        CHECK(purpose IN ('source', 'target', 'mutual')),
    PRIMARY KEY (device_id, purpose),
    FOREIGN KEY (device_id) REFERENCES backup_devices(id) ON DELETE CASCADE
);

-- ─── backup_sync_pairs ──────────────────────────────────────────────────────
-- Cardinalidad N → tabla aparte. Source path local ↔ Dest path remoto.
CREATE TABLE IF NOT EXISTS backup_sync_pairs (
    device_id     TEXT NOT NULL,
    local_path    TEXT NOT NULL,
    remote_path   TEXT NOT NULL,
    PRIMARY KEY (device_id, local_path, remote_path),
    FOREIGN KEY (device_id) REFERENCES backup_devices(id) ON DELETE CASCADE
);

-- ─── backup_jobs ────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS backup_jobs (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    device_id      TEXT NOT NULL,
    fs_type        TEXT NOT NULL DEFAULT 'btrfs'
        CHECK(fs_type IN ('btrfs')),   -- Beta 8 = BTRFS-only
    source         TEXT NOT NULL,
    dest           TEXT NOT NULL,
    schedule       TEXT NOT NULL DEFAULT 'daily 02:00',
    retention      TEXT NOT NULL DEFAULT '30d',
    enabled        INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
    status         TEXT NOT NULL DEFAULT 'ok'
        CHECK(status IN ('ok', 'running', 'error', 'paused')),
    last_run       TEXT,           -- nullable: nunca corrido
    next_run       TEXT,           -- nullable: disabled o sin schedule
    last_size      INTEGER NOT NULL DEFAULT 0,
    last_snap      TEXT DEFAULT '',
    created_at     TEXT NOT NULL,

    FOREIGN KEY (device_id) REFERENCES backup_devices(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_backup_jobs_next_run
    ON backup_jobs(next_run) WHERE enabled = 1 AND next_run IS NOT NULL;

-- ─── backup_history ─────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS backup_history (
    id             TEXT PRIMARY KEY,
    job_id         TEXT NOT NULL,
    device_id      TEXT NOT NULL,
    dest           TEXT NOT NULL,
    ok             INTEGER NOT NULL CHECK(ok IN (0,1)),
    bytes          INTEGER NOT NULL DEFAULT 0,
    duration_sec   INTEGER NOT NULL DEFAULT 0,
    error_msg      TEXT,
    occurred_at    TEXT NOT NULL,

    FOREIGN KEY (job_id)    REFERENCES backup_jobs(id) ON DELETE CASCADE,
    FOREIGN KEY (device_id) REFERENCES backup_devices(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_backup_history_job ON backup_history(job_id, occurred_at);

-- ─── backup_remote_mounts ───────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS backup_remote_mounts (
    device_id      TEXT NOT NULL,
    share_name     TEXT NOT NULL,
    remote_path    TEXT NOT NULL,
    mount_point    TEXT NOT NULL,
    device_addr    TEXT NOT NULL,    -- snapshot of addr at mount time
    mounted_at     TEXT NOT NULL,

    PRIMARY KEY (device_id, share_name),
    FOREIGN KEY (device_id) REFERENCES backup_devices(id) ON DELETE CASCADE
);
```

**Cambios estructurales vs schema actual:**

1. **FK reales**: `backup_jobs.device_id` ahora referencia `backup_devices.id` con `ON DELETE CASCADE`. Hoy un device borrado deja jobs huérfanos en DB.
2. **CHECK constraints** en todos los enums (`type`, `fs_type`, `status`, `enabled`, etc.). Beta 8 pattern.
3. **JSON serializado eliminado**: `purposes` y `sync_pairs` se promueven a sus propias tablas con FK CASCADE. Esto resuelve el anti-pattern "guardar arrays como JSON en TEXT" que el propio `schema.sql` del proyecto prohíbe explícitamente.
4. **Pair token outbound cifrado**: en vez de `pair_token_outbound TEXT`, ahora `outbound_secret_id TEXT` con FK a `nimos_secrets`. Aprovecha la infraestructura AES-GCM que ya existe en el proyecto. El token deja de estar en plaintext en la DB.
5. **`allow_ip_auth` per-device** (LOGIC-023b): resuelve P1-4 sin toggle global ni `security.json`. Default 0 = seguro.
6. **`last_run`/`next_run` nullable**: hoy se guardan como `''`, que obliga a parsing con `if str == ""` en lugar de `IS NULL`. SQL idiomático.
7. **`error_msg` en history**: hoy el fallo se loguea pero no queda en DB.
8. **Index parcial en `next_run`**: el scheduler hoy hace `SELECT *` y filtra en Go. Con este index, la query `WHERE enabled = 1 AND next_run < ?` usa B-tree.

---

## 3 · Hallazgos de bugs y deuda (nuevos en esta pasada)

### 🟠 P1-2 confirmado · Command injection en `executeBackupJob`

```go
// backup.go:854
cmdStr = fmt.Sprintf("btrfs send -p %s %s | ssh %s root@%s 'btrfs receive %s'",
    lastSnapPath, snapPath, sshOpts, remoteAddr, dest)
out, ok := runShellStatic(cmdStr)
```

Ya identificado en pasada 2. **Aprovechar refactor** para sustituir por exec.Pipe nativo:

```go
// backup_scheduler.go (post-refactor)
func executeBtrfsSend(ctx context.Context, snapPath, lastSnapPath, remoteAddr, dest, deviceID string) error {
    sshArgs := sshArgsForDevice(deviceID)  // []string, NO concatenado
    sshArgs = append(sshArgs, "root@"+remoteAddr, "btrfs receive "+shellQuote(dest))

    sendArgs := []string{"send"}
    if lastSnapPath != "" {
        sendArgs = append(sendArgs, "-p", lastSnapPath)
    }
    sendArgs = append(sendArgs, snapPath)

    sendCmd := exec.CommandContext(ctx, "btrfs", sendArgs...)
    sshCmd := exec.CommandContext(ctx, "ssh", sshArgs...)

    pipe, err := sendCmd.StdoutPipe()
    if err != nil { return fmt.Errorf("stdout pipe: %w", err) }
    sshCmd.Stdin = pipe

    if err := sshCmd.Start(); err != nil { return fmt.Errorf("ssh start: %w", err) }
    if err := sendCmd.Run(); err != nil { return fmt.Errorf("btrfs send: %w", err) }
    if err := sshCmd.Wait(); err != nil { return fmt.Errorf("ssh wait: %w", err) }
    return nil
}
```

Sólo queda un punto donde `dest` aún se pasa a `btrfs receive` por argumento — ese sí va vía SSH al remoto, así que el remote recibe el string como argumento de `btrfs receive` en su shell. Mitigación: `shellQuote(dest)` para escapado defensivo + validación previa en `validateBackupPath`.

### 🟠 P1-3 confirmado · Inyección en `/etc/exports` por `clientIP`

Ya identificado. Fix en `backup_validators.go`:

```go
// validateClientIP acepta IP o CIDR. Rechaza cualquier otra cosa.
func validateClientIP(s string) error {
    if net.ParseIP(s) != nil { return nil }
    if _, _, err := net.ParseCIDR(s); err == nil { return nil }
    return fmt.Errorf("invalid clientIP: must be IP or CIDR notation")
}
```

Aplicar en `handleNFSExport` antes de llamar a `addNFSExport`.

### 🟠 P1-4 confirmado · Fallback IP-only spoofable

Resuelto vía `allow_ip_auth` per-device en el schema nuevo. Lectura:

```go
// backup_pairing.go (post-refactor)
func verifyPairedDevice(r *http.Request) *Device {
    if dev := verifyPairToken(r); dev != nil { return dev }

    remoteIP := extractRemoteIP(r)
    dev, _ := dbBackupDeviceGetByAddr(remoteIP)
    if dev == nil { return nil }
    if !dev.AllowIPAuth {
        return nil  // device exists but didn't opt-in to IP fallback
    }
    logMsg("SECURITY WARN: device %q via IP-fallback from %s — re-pair to use token auth", dev.Name, remoteIP)
    return dev
}
```

### 🟠 **NUEVO** P1-5 · `addr` no validado en `dbBackupDeviceCreate` y `POST /api/backup/devices`

**`backup.go:1622-1690`** — el handler de creación de device hace TrimPrefix/TrimRight/strip-port pero **nunca valida** que `addr` sea una hostname o IP real. Un admin (o un dispositivo paired remoto vía mutual pairing en `POST /api/backup/devices` línea 1689 con auth Bearer) puede registrar un device con `addr = "1.2.3.4;rm -rf /tmp/foo"`. Ese `addr` después llega a `executeBackupJob` y se interpola en el `ssh root@%s` (P1-2). Encadena con P1-2.

```go
// backup_validators.go
func validateDeviceAddr(s string) (string, error) {
    s = strings.TrimSpace(s)
    s = strings.TrimPrefix(s, "https://")
    s = strings.TrimPrefix(s, "http://")
    s = strings.TrimRight(s, "/")
    // strip :port if numeric
    if idx := strings.LastIndex(s, ":"); idx > 0 {
        if _, err := strconv.Atoi(s[idx+1:]); err == nil {
            s = s[:idx]
        }
    }
    if s == "" { return "", fmt.Errorf("addr is empty") }
    // Must be valid IP or hostname (RFC 1123)
    if net.ParseIP(s) != nil { return s, nil }
    if isValidHostname(s) { return s, nil }
    return "", fmt.Errorf("addr must be a valid IP or hostname")
}
func isValidHostname(s string) bool {
    if len(s) > 253 { return false }
    re := regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)*[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)
    return re.MatchString(s)
}
```

### 🟠 **NUEVO** P1-6 · `POST /api/backup/pair/update-addr` autenticación por IP únicamente

**`backup.go:1860`** — endpoint que un peer remoto invoca para decirle "ya no me localices en mi IP normal, ahora estoy en este otro `tunnelAddr`". Identifica el device a actualizar por `r.RemoteAddr`. Sin pair-token check, sin admin auth.

**Exploit:** un atacante en LAN que pueda hablar al daemon (no necesita estar paired, sólo accesible TCP) puede llamar a este endpoint y **redirigir el `addr` de un device legítimo** a su propia IP. La próxima vez que ese device sea destino de un job, el `btrfs send | ssh root@$attacker_ip` envía los datos al atacante. Filtración total del backup a un peer no autorizado.

Está bajo el guard `requireAdmin` por estar en la sección general de routes pero... espera, déjame verificar:

```go
// Línea 1860 — está DESPUÉS del requireAdmin en línea 1482. OK, sí está gated.
```

Falsa alarma parcial: requiere admin auth. **Pero el patrón sigue siendo frágil**: usa IP para identificar device en lugar de pair token. Cualquier admin que dispare este endpoint con un body manipulado puede redirigir devices. Más limpio: el endpoint debería requerir el pair token del device que se actualiza, no sólo admin auth + IP match.

Lo bajo a **P2 nuevo**: "redirección de addr vía endpoint admin que confía en IP source para identificar device".

### 🟠 **NUEVO** P1-7 · `pairWithRemote` sin verificación TLS contra host pinning

**`backup.go:1993-2170`** — durante el pairing inicial, el daemon llama `https://addr:5009/api/auth/login` con credenciales del admin. **No hay TLS host key pinning** ni verificación contra cert conocido. El primer pairing es vulnerable a MITM activo en el path al peer.

Esto es **inherente al "trust on first connection"** y honestamente, sin un canal out-of-band para verificar identidad, no hay solución perfecta. Pero hay mitigaciones:

1. **TOFU explícito en UI**: el daemon muestra al admin el hash del cert remoto y pide confirmación. Hoy se conecta y confía silenciosamente.
2. **Comparar SSH host key vs cert TLS**: el SSH host key se fetcha luego (LOGIC-021). Si los dos no coinciden con la misma identidad criptográfica, alertar.
3. **Pin del cert remoto en `backup_devices.tls_cert_fingerprint` post-pairing**: futuras conexiones validan contra ese fingerprint, no contra la CA.

Lo dejo apuntado como **P1-7 / TOFU strategy** para la fase de pairing del refactor. No es trivial.

### 🟡 P2-5 confirmado · Retention `maxAge XOR maxCount`

Ya identificado. Solución en `backup_retention.go`:

```go
// Aplicar ambas reglas como UNION. Siempre conservar el más reciente.
func computeSnapshotsToDelete(snaps []string, maxAge time.Duration, maxCount int) []string {
    toDelete := map[string]bool{}
    sort.Strings(snaps)  // timestamps sort correctly

    if maxAge > 0 {
        cutoff := time.Now().UTC().Add(-maxAge)
        for _, snap := range snaps {
            ts := extractTimestamp(snap)
            if ts.IsZero() {
                logMsg("retention: cannot parse timestamp from %q — skipping", snap)
                continue
            }
            if ts.Before(cutoff) { toDelete[snap] = true }
        }
    }
    if maxCount > 0 && len(snaps) > maxCount {
        for _, snap := range snaps[:len(snaps)-maxCount] {
            toDelete[snap] = true
        }
    }
    // Conservar el más reciente siempre
    if len(snaps) > 0 { delete(toDelete, snaps[len(snaps)-1]) }

    result := make([]string, 0, len(toDelete))
    for s := range toDelete { result = append(result, s) }
    return result
}
```

### 🟡 **NUEVO** P2-7 · `checkAndRunDueJobs` lanza `go executeBackupJob` sin límite de paralelismo

**`backup.go:1449`** — si 10 jobs vencen a las 02:00 (configuración típica "daily 02:00"), se lanzan 10 `btrfs send | ssh` en paralelo. Si cada uno consume 100 MB/s, saturación de NIC + IO de disco. El `backupRunningJobs` previene double-execution **del mismo job**, no de jobs distintos.

Solución: worker pool con semáforo configurable (default 2 concurrent jobs).

```go
// backup_scheduler.go
var backupSem = make(chan struct{}, 2)  // configurable

func runJobAsync(job Job) {
    go func() {
        backupSem <- struct{}{}
        defer func() { <-backupSem }()
        executeBackupJob(job)
    }()
}
```

### 🟡 **NUEVO** P2-8 · Sin `context` propagation en `executeBackupJob`

Una request HTTP que dispara `POST /api/backup/run/:id` lanza el job en goroutine. Si el cliente HTTP se desconecta, el job **sigue corriendo sin forma de cancelarlo**. Si el daemon recibe SIGTERM mid-backup, el `btrfs send | ssh` queda zombie hasta timeout TCP (~2-3 minutos).

Pasar `ctx context.Context` por toda la cadena `executeBackupJob → executeBtrfsSend → exec.CommandContext`. Cancelable. Y al shutdown, el signal handler cancela el context raíz, drenando con timeout 20s (parte de P2-6 del audit original).

### 🟡 **NUEVO** P2-9 · `dbBackupDeviceList` deserializa JSON purposes/sync_pairs en cada llamada

```go
// backup.go:255-320
if json.Unmarshal([]byte(purposesJSON), &purposes) == nil { ... }
if json.Unmarshal([]byte(syncPairsJSON), &syncPairs) == nil { ... }
```

Coste irrelevante con 5 devices, real con 50+. Y los errores de unmarshal se silencian. Resuelto al promover purposes/sync_pairs a tablas separadas con FK (cambio del schema arriba).

### 🟡 **NUEVO** P2-10 · `probeNimOS` y `scanLANForNimOS` usan HTTP (no HTTPS) sin verificación

```go
client.Get(fmt.Sprintf("http://%s:5000/api/auth/status", addr))
```

Discovery es opt-in (no inicia pairing solo, sólo lista candidatos), pero el `name` y `version` mostrados al admin **vienen del response del candidato**. Un atacante en LAN puede responder a esa probe diciéndose llamar "Backup-Casa-Principal-NimOS" para engañar al admin a hacer pairing con la IP equivocada. Mitigación: mostrar IP al admin de forma prominente en UI, no sólo nombre.

### 🔵 **NUEVO** P3-7 · `extractPathSegment` es código duplicado de stdlib

```go
func extractPathSegment(path, prefix, suffix string) string {
    s := strings.TrimPrefix(path, prefix)
    s = strings.TrimSuffix(s, suffix)
    return s
}
```

Cuando muevas a `net/http` con router moderno (chi, gorilla) o vars en context, esto desaparece. Pero si conservas el switch manual, al menos centraliza esta función fuera de `backup.go` — la usan otros módulos también con seguridad.

### 🔵 **NUEVO** P3-8 · `time.Now().UTC()` repetido sin clock injection

15+ call sites usan `time.Now().UTC()` directamente. Tests no pueden controlar el tiempo. NimOS ya tiene `storage_clock.go` con un Clock interface — extiende ese patrón al módulo backup. Tests de retención, scheduler y "next_run computation" se vuelven deterministas.

### 🔵 **NUEVO** P3-9 · `applyRetention` ignora errores de `btrfsSnapshotDestroy`

```go
for _, snap := range toDelete {
    snapPath := fmt.Sprintf("%s/%s", snapDir, snap)
    logMsg("backup: retention cleanup — deleting subvolume %s", snapPath)
    btrfsSnapshotDestroy(snapPath)   // ← ignora return value
}
```

Si una deleción falla (snapshot mounted, perms, IO), el siguiente run lo reintentará silenciosamente para siempre. Y si fallan TODAS las deleciones (problema del filesystem), nadie se entera. Mínimo: log de error + métrica "retention_delete_failures".

---

## 4 · Eliminación del patrón JSON-config sin piedad

### Inventario de la deuda JSON-config a nivel proyecto

```
docker.json              → docker_helpers.go:30
security.json            → hardware.go:1350 (terminalEnabled), storage_startup.go:354
shares.json              → main.go:42
users.json               → main.go:43
ddns.json                → network.go:63
remote-access.json       → network.go:65
smb.json                 → network.go:66
proxy-rules.json         → network.go:67
webdav.json              → network.go:68
storage.json             → storage_config.go:27 (legacy migrado per memoria)
wireguard-state.json     → wireguard.go:58
```

11 archivos JSON-config flotantes. **No es objetivo de este refactor migrarlos todos** — sería expansión de scope. Pero el refactor de backup **no debe añadir un 12º**, y donde toque algo aledaño (la flag `allow_ip_auth` que iba a `security.json` en mi fix revertido) ya se elimina porque va a la tabla `backup_devices`.

**Sugerencia (separada de este refactor):** el día que ataques la migración JSON→SQL del resto, el patrón es claro y replicable:

- Cada módulo con `module.json` → `module_settings` tabla key-value, O campos en tabla específica de su entidad.
- Esquema embebido `//go:embed` por módulo (patrón ya canonizado en `network_schema`, `apps_schema`, `nimos_core_schema`).
- Migración one-shot al boot: si `module.json` existe Y la tabla está vacía, leer JSON y poblar tabla. Tras éxito, renombrar JSON a `.migrated`. Idempotente.

Pero insisto: eso es proyecto aparte. Aquí, en backup, **el JSON-config no entra**.

---

## 5 · Lo que el refactor preserva intacto (decisión consciente)

1. **`generatePairToken` con `crypto/rand`**: bien hecho. Mantener.
2. **`sha256Hex` para hash de tokens**: bien. Mantener.
3. **LOGIC-021 SSH host key pinning**: idea correcta, implementación limpia. Mantener, sólo mover a `backup_pairing.go`.
4. **LOGIC-022 root_squash/all_squash en NFS exports**: bien razonado, mantener.
5. **`fetchSSHHostKey` en goroutine async tras pair create**: bien, sólo añadir `recover()` en el wrapper goroutine.
6. **El concepto de "mutual pairing"** (el local pide token al remoto Y le da el suyo): conceptualmente correcto. Refinar implementación.
7. **`buildShareViews(ctx, dbShares)` con context propagation**: aprovecha cuando refactorices el resto del flujo HTTP.

---

## 6 · Orden recomendado de ataque del refactor

Por dependencias y para mantener el daemon funcional en cada commit:

**Sprint 1 — Foundation (no rompe nada visible)**
1. Crear `backup_schema.sql` + `backup_schema.go` con tablas nuevas, idempotente.
2. Crear `backup_validators.go` con `validateDeviceAddr`, `validateClientIP`, `validateBackupPath`, `validateSchedule`, `validateRetention`. Sin call sites aún.
3. Tests unitarios de validators (target: 80%+ coverage en este archivo).

**Sprint 2 — Migration (lectura compatible, escritura nueva)**
4. Función migrate one-shot que lee tabla vieja + JSON inline (`purposes_json`, `sync_pairs_json`) y puebla tablas nuevas (`backup_device_purposes`, `backup_sync_pairs`). Idempotente.
5. `backup_repo.go`: nueva API que lee de las tablas nuevas. La vieja queda como deprecated wrapper.
6. Tests del repo con DB temporal en `:memory:`.

**Sprint 3 — Domain split (el "gran" refactor)**
7. Mover `pairWithRemote`, discovery, host key, tokens → `backup_pairing.go`.
8. Mover scheduler + execute → `backup_scheduler.go`. **Aquí entra el fix P1-2** (exec.Pipe en lugar de Sprintf+sh).
9. Mover retention → `backup_retention.go`. Fix P2-5 al pasar.
10. Mover NFS → `backup_nfs.go`. Fix P1-3 (validateClientIP).

**Sprint 4 — HTTP layer**
11. Split de `handleBackupRoutes` por subroute → `backup_http.go`. Cada sub-handler es una función pequeña.
12. Fix P1-5 (validateDeviceAddr aplicado en handlers).
13. Worker pool del scheduler (P2-7) + context propagation (P2-8).

**Sprint 5 — Cleanup**
14. Eliminar `backup.go` original. El nuevo `backup.go` es sólo el façade con tipos y registro.
15. Borrar `purposes_json` y `sync_pairs_json` de `backup_devices` (ALTER TABLE DROP COLUMN — SQLite 3.35+).
16. Tests integration end-to-end con DB temporal: pair flow, job execute, retention, NFS mount.

**Tiempo estimado:** 4-6 sesiones de 3-4 horas cada una si trabajas con buena energía. Más si lo intercalas con otros frentes (UI, NimShield, etc.).

---

## 7 · Lectura final

`backup.go` no está MAL escrito. Está **escrito en una época anterior** del proyecto, antes de que cristalizara la disciplina Beta 8 (schema embebido, FK reales, CHECK, schema separado por módulo). Y al mantenerse 2837 líneas en un solo archivo durante todo este tiempo, fue acumulando capas que cada una individualmente tenía sentido pero juntas crearon el god-file.

Es el ÚLTIMO módulo grande del daemon que sigue el patrón viejo. Storage Beta 8, network, shield, nimhealth, apps — todos siguen el patrón nuevo. Una vez backup se alinee, **el daemon entero está homogéneo arquitectónicamente**, y la disciplina deja de tener excepciones.

Mi opinión sin paños calientes: este refactor vale la pena. No es deuda urgente (no hay usuarios afectados hoy) pero es deuda de coherencia. Y la coherencia es lo que hace que un proyecto open-source se sienta serio cuando alguien lo lee — un módulo que se siente "diferente" del resto es un olor a "hubo prisa, no se terminó". Con backup alineado, NimOS Beta 8 tiene la firma técnica completa.

Cuando lo ataques, este documento es tu mapa. Cualquier divergencia es porque tú lo decides, no porque improvises.
