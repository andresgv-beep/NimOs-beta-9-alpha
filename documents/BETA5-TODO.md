# NimOS Beta 5 — Estado y Pendientes

## Fecha: 2026-03-15 (actualizado)
## Repo: NimOs-beta-5

---

## ✅ COMPLETADO HOY

### Flujo Docker/Apps (era prioridad alta)
- [x] Launcher carga Docker apps desde `/api/docker/installed-apps`
- [x] Launcher abre Docker apps en iframe (`isWebApp: true`)
- [x] WebApp.svelte creado — iframe con loading/error/ready states
- [x] WindowFrame detecta `win.isWebApp` y renderiza WebApp
- [x] AppStore procesa env: `${CONFIG_PATH}`, `{RANDOM}`, `TZ`, `HOST_IP`
- [x] AppStore endpoints correctos: `/api/docker/stack`, `installed-apps`, `DELETE stack/{id}`
- [x] Docker install automático desde AppStore (timeout 300s)
- [x] `ProtectSystem=false` en service para permitir instalar Docker
- [x] Jellyfin e Immich instalados y corriendo en iframe dentro de NimOS

### Backend fixes
- [x] `docker.go` — install con context timeout + logging
- [x] `storage.go` — appendFstab con fallback device path + no duplicados + nofail
- [x] `AppStore.svelte` — response parsing `data.apps` (no array directo)
- [x] fstab manual para el pool RAID1

### Sesión anterior
- [x] Stores: auth.js, theme.js, windows.js
- [x] Login + SetupWizard + Desktop + Taskbar + WindowFrame
- [x] Controles ventana propios (3 líneas colores)
- [x] Drag + resize + minimize + maximize
- [x] FileManager, Settings, NimTorrent, AppStore portados

---

## 🔴 PENDIENTE — BUGS ACTIVOS (próxima sesión)

### 1. Carpeta docker/data/containers no se crea automáticamente
**Problema:** Docker compose falla con "no such file or directory" al crear containers porque `/nimos/pools/volume/docker/data/containers/` no existe.
**Fix:** En `dockerInstall()` de docker.go, después de crear las carpetas, añadir:
```go
os.MkdirAll(filepath.Join(dockerDataPath, "containers"), 0755)
```
**Archivo:** `daemon/docker.go` línea ~651

### 2. Ruteamiento iframe vs pestaña
**Problema:** Algunas apps se abren en iframe pero no en pestaña externa, y viceversa. Apps que bloquean iframe (X-Frame-Options: DENY) deberían tener `external: true` en el catálogo.
**Fix:** 
- Revisar cada app en `catalog.json` y marcar `"external": true` las que bloquean iframe
- En WebApp.svelte: si iframe falla por X-Frame-Options, auto-abrir en pestaña
- En Launcher: apps `external` abren directo en `window.open()` (ya implementado)
**Archivos:** `catalog.json` (repo appstore), `src/lib/apps/WebApp.svelte`

### 3. Apps que dejaron de funcionar
**Problema:** Algunas apps que funcionaban antes ya no van.
**Necesita:** Testear cada app instalada, identificar errores en consola (F12), arreglar caso por caso.
**Posibles causas:** puertos mal mapeados, env variables faltantes, contenedores caídos.

### 4. Docker share — permisos de lectura/escritura
**Problema:** El share "docker" muestra "0 items" porque las carpetas son propiedad de root.
**Fix:** 
- En `dockerInstall()`: crear share automático "docker" apuntando al pool
- Permisos: `chmod -R 755` en carpetas docker excepto `data/` (interna de Docker)
- Solo mostrar carpetas relevantes al usuario: `containers/`, `stacks/`, `volumes/`
**Archivo:** `daemon/docker.go`

### 5. fstab automático — storage.go
**Problema:** Al crear pool RAID, `blkid` puede no devolver UUID para md devices recién creados.
**Fix:** Ya arreglado con fallback a device path, pendiente de verificar en instalación limpia.

---

## 🟡 PENDIENTE — FUNCIONALIDAD (siguiente fase)

### 6. Containers manager
- [ ] Crear `Containers.svelte` — lista contenedores con start/stop/restart
- [ ] Endpoint: `/api/docker/status` → `containers[]`
- [ ] Acciones: `/api/docker/container/{id}/{action}`
- [ ] Logs de contenedores

### 7. AppStore — install wizard mejorado
- [ ] Barra de progreso durante instalación
- [ ] Credenciales post-install (definidas en catalog.json)
- [ ] Refrescar launcher/taskbar después de instalar

### 8. Taskbar — Docker apps
- [ ] Taskbar carga apps Docker instaladas
- [ ] Iconos de apps corriendo junto a las del sistema

---

## 🔵 PENDIENTE — POLISH (futuro)

### 9. Temas y scaling
- [ ] CSS tokens globales para midnight/dark/light
- [ ] Accent color dinámico
- [ ] Scaling para 1080p / 1440p / 4K

### 10. Widgets desktop
- [ ] Clock, DiskPool, SystemMonitor, Network, NimTorrent

### 11. Apps por portar
- [ ] MediaPlayer, SystemMonitor, Context menus

### 12. Limpieza repo
- [ ] Borrar `src/components/` (duplicado viejo)
- [ ] Actualizar URLs en catalog.json
- [ ] Sincronizar repo con archivos del NAS

---

## ARCHIVOS MODIFICADOS HOY

```
NUEVOS:
  src/lib/apps/WebApp.svelte

MODIFICADOS:
  src/lib/apps/AppStore.svelte
  src/lib/components/WindowFrame.svelte
  src/lib/components/Launcher.svelte
  daemon/docker.go
  daemon/storage.go
  scripts/nimos-daemon.service
```

## COMANDOS ÚTILES

```bash
# Recompilar daemon
cd /opt/nimos/daemon
sudo go mod tidy && sudo go build -o /tmp/nimos-daemon .
sudo systemctl stop nimos-daemon
sudo cp /tmp/nimos-daemon /opt/nimos/daemon/nimos-daemon
sudo systemctl start nimos-daemon

# Recompilar frontend
cd /opt/nimos && npm run build

# Logs
sudo tail -f /var/log/nimos/daemon-error.log
sudo tail -f /var/log/nimos/daemon.log

# Docker
docker ps
docker logs jellyfin

# Test endpoints (F12 Console)
fetch('/api/docker/status', {headers:{'Authorization':'Bearer '+localStorage.getItem('nimos_token')}}).then(r=>r.json()).then(console.log)
fetch('/api/docker/installed-apps', {headers:{'Authorization':'Bearer '+localStorage.getItem('nimos_token')}}).then(r=>r.json()).then(console.log)
```
