# NimOS Beta 7 — Changelog completo

**Fecha:** 2026-04-07
**Base:** NimOS Beta 6
**Auditoría original:** 77 findings (9 críticos, 40 lógica, 10 hardening, 12 refactoring, 6 docs)

---

## 🔴 CRÍTICOS DE SEGURIDAD — 9/9 cerrados

| ID | Vulnerabilidad | Fix |
|---|---|---|
| CRIT-001 | Command injection en certbot (domain/email) | `runSafeLong()` + validación `isValidDomain`/`isValidEmail` |
| CRIT-002 | Command injection en DDNS (token/domain) | `runSafe("curl",...)` + validación token |
| CRIT-003 | Terminal shell sin audit log ni sanitización cwd | `exec.Command` con `c.Dir`, audit log con user/IP, blacklist destructivos, config flag `terminalEnabled` |
| CRIT-004 | Snapshot name injection (ZFS dataset traversal) | Validación `isValidSnap()`/`isValidSnapName()` regex |
| CRIT-005 | Shell injection en backup.go (~26 llamadas) | Todo migrado a `runSafe()` y primitivas core |
| CRIT-006 | QR code shell injection | `runSafeInput()` pipe via stdin |
| CRIT-007 | Docker env var injection | Args array completo sin shell, env vars como `-e KEY=VAL` directo |
| CRIT-008 | Token de sesión expuesto en URLs de descarga | Endpoint `POST /api/files/download-token` (one-time-use, 60s), download via `?dl=` |
| CRIT-009 | Update script sin verificación | `runSafe("curl",...)` + SHA256 checksum verification |

## 🔒 SEGURIDAD — Infraestructura nueva

### Funciones de ejecución segura (main.go)
- `runSafe(cmd, args...)` — exec.Command directo, sin shell
- `runSafeInput(stdin, cmd, args...)` — pipe via stdin
- `runSafeLong(cmd, args, timeout)` — con timeout personalizado
- `runShellStatic(command)` — shell con guard anti-interpolación (`%s`, `%d`, `%v` rechazados)
- `runShellLongStatic(command, timeout)` — shell con timeout + guard

### Validadores de input (main.go)
- `isValidDomain()`, `isValidEmail()`, `isValidSnap()`, `isValidSnapName()`
- `isValidDev()`, `isValidUnixUser()`, `isValidContainer()`, `isValidWgIface()`
- `isValidSafePath()`
- `reAlphanumDash` regex para subdominios

### Migración `run()` → `runShellStatic()`
- **Antes:** 176 `run(fmt.Sprintf)` inyectables + ~135 `run()` estáticos = ~311 total
- **Ahora:** 0 inyectables, 419 llamadas seguras (`runSafe`/`runCmd`), 44 `runShellStatic` (pipes/chains estáticos con guard)
- Guard: rechaza cualquier comando con `%s`, `%d`, `%v` — impide regresiones futuras

## 🔐 HARDENING

| ID | Fix |
|---|---|
| HARD-002 | `filepath.EvalSymlinks()` en `validatePathWithinShare` — bloquea symlink escape |
| HARD-005 | QR SVG: solo renderiza si empieza con `<svg` y no contiene `<script>` |
| HARD-006 | `{@html}` eliminado de ConfirmDialog |
| HARD-007 | `http://` hardcoded → `window.location.protocol` |
| HARD-008 | Security comment en `highlight()` de Notes |
| X-Forwarded-For | `clientIP()` solo confía en XFF si `RemoteAddr` es `127.0.0.1` (nginx local) |
| Cookie flags | `Set-Cookie` con `HttpOnly`, `Secure`, `SameSite=Strict` en login/setup/logout |
| CSP | `unsafe-inline` necesario en `script-src` para Svelte/Vite — quitar requiere nonces CSP level 3 (futuro) |
| Upload RAM | Legacy: bajado a 50MB + `MaxBytesReader` + 8MB buffer. Chunked: ya era streaming a disco |
| Terminal | Config flag `terminalEnabled`, blacklist de comandos destructivos, max 4096 chars |
| Docker install | Download script → verificar → ejecutar (no `curl \| sh`) |
| apt-get drivers | `exec.Command("apt-get", action, "-y", pkg)` directo sin shell |
| ufw commands | Migrados a `runSafe("ufw", args...)` y `runSafeInput()` |
| dnsToken | Validación `ContainsAny` antes de escribir en bash hooks de certbot |

## 🟠 BUGS DE LÓGICA

| ID | Fix |
|---|---|
| LOGIC-001 | `storageConfigMu sync.RWMutex` para storage.json read/write |
| LOGIC-002 | `storageAlertsMu sync.RWMutex` para storageAlertsGo |
| LOGIC-003 | BTRFS capacity: `btrfs filesystem usage -b` (correcto en RAID) con fallback a `df` |
| LOGIC-006 | Wildcard CORS `*` eliminado de 4 endpoints (download + torrent) |
| LOGIC-007 | Session: 7 días → 24h sliding expiry (renueva en cada request) |
| LOGIC-009 | StoragePanel `scrubInterval` leak → añadido a `onDestroy` |
| LOGIC-010 | SystemPanel `updatePollId` leak → `onDestroy` añadido |
| LOGIC-011 | Settings `updatePollId` leak → `onDestroy` añadido |
| zpool check | `runSafe` ok bool verificado en pool creation + auto-import |

## 🔵 REFACTORING

| ID | Fix |
|---|---|
| REF-002 | `storage_stubs.go` (1625 líneas) → eliminado, split en 5 archivos: `storage_config.go`, `storage_startup.go`, `storage_pool_info.go`, `storage_http.go`, `storage_disk_mgmt.go` |
| REF-003 | `hdrs()` centralizado en `auth.js` con `hdrs()` y `jsonHdrs()`, 12 componentes migrados |
| REF-007 | Versión actualizada a `6.0.0-beta` |
| REF-009 | 4 primitivas core de snapshot (`zfsSnapshotCreate/Destroy`, `btrfsSnapshotCreate/Destroy`), backup y storage las comparten |
| REF-011 | `getStorageConfigGo()` eliminada, un solo lector `getStorageConfigFull()` |
| Orphans | `restorePoolGo()`, `runExec()`, `removePoolFromConfig()` eliminadas |
| Regex | Pre-compiladas fuera de loops (`reSdDisk`, `reNvmeDisk`, `reVdDisk`) |
| Apps | `SECURITY BOUNDARY` comments en install/uninstall/check commands |
| Repo | Todas las referencias `NimOs-beta-6` → `NimOs-beta-7` (install.sh, update.sh, service, hardware.go, package.json) |

## ⬜ CLEANUP

- `instrucciones.md` duplicado eliminado de raíz
- `BETA5-TODO.md` obsoleto eliminado
- `storage_pools.go` vacío (ya eliminado)
- Archivos con `(1)` renombrados: `NIMHEALTH-UX-SPEC-v1.md`, `NOTIFICATIONS-SPEC.md`

## 📊 STATS FINALES

| Métrica | Valor |
|---|---|
| `runSafe()` | 263 |
| `runSafeInput()` | 7 |
| `runSafeLong()` | 2 |
| `runCmd()` | 147 |
| **Total seguro** | **419** |
| `runShellStatic()` (con guard) | 44 |
| `run(fmt.Sprintf)` inyectable | **0** |
| Validadores regex | 9 funciones |
| Archivos Go daemon | 29 |

## ⏳ PENDIENTE (no bloquea Beta 7 stable)

- CSP nonces: Eliminar `unsafe-inline` de `script-src` requiere nonces CSP level 3 — Svelte/Vite genera inline scripts en build time, necesita cambio de arquitectura en cómo se sirve el frontend (medio día de trabajo)
- LOGIC-038: FTP `ssl_enable=NO` en install.sh
- LOGIC-039: Nginx sin `limit_req_zone`
- HARD-003: App proxy strips X-Frame-Options (necesario para iframe)
- HARD-010: `find` síncrono en shares con millones de archivos
- MediaPlayer: token de sesión en URL de streaming (`<audio>` src)
- NimHealth: race condition de timing al cargar servicios (preexistente)

## 🔧 FIXES POST-BUILD (errores de compilación encontrados durante instalación)

- `storage_config.go` — imports duplicados del script de split (bloque viejo del stubs)
- `storage_disk_mgmt.go` — import `"os"` no usado
- `storage_pool_info.go` — import `"net/http"` no usado
- `storage_startup.go` — import `"os/exec"` huérfano tras borrar `runExec()`
- `shares.go` — imports `"encoding/json"` y `"os"` huérfanos tras borrar `getStorageConfigGo()`
- `hardware.go` — `getSession(r)` no existía, cambiado a pasar `session` como parámetro
- `storage_zfs_pool.go` — `runSafe` ignoraba bool `ok` en check de pool existente
- `http.go` — CSP `script-src 'self'` sin `unsafe-inline` bloqueaba Svelte → revertido
