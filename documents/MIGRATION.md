# NimOS Beta 8 · Plan de Migración app por app

Este documento es la guía operativa para migrar cada app desde Beta 7 al lenguaje v3.
Orden recomendado por dependencia y dificultad progresiva.

---

## Flujo genérico para cada app

**Antes de empezar cualquier app:**
1. Abrir en paralelo la pantalla correspondiente del design system v3 (mockups en la conversación de diseño con Claude)
2. Abrir el archivo original en `../NimOs-beta-7-main/src/lib/apps/<App>.svelte`
3. Leer el `<script>` completo entendiendo todo lo que hace
4. Crear rama feature en git: `git checkout -b migrate/<appid>`

**Durante la migración:**
1. Copiar el `<script>` al stub (conservar toda la lógica)
2. Reescribir el template HTML desde cero usando primitivas v3
3. Borrar el `<style>` del componente (o reducirlo a 10-20 líneas de ajustes específicos)
4. Verificar todos los endpoints API que usa (`grep -n fetch <App>.svelte`)
5. Probar con daemon Go real de Beta 7 (mismos endpoints)

**Al terminar:**
1. Screenshots antes/después (motivación + documentación)
2. Merge a `main`
3. Commit diario como "punto de no retorno"

---

## 🔵 Fase 1 · Apps livianas (2-3 días)

Apps pequeñas para coger ritmo y validar el design system en contextos variados.

### 1. NetworkApp (185 líneas)

**Referencia Beta 7**: `src/lib/apps/NetworkApp.svelte`
**Complejidad**: ⭐ baja

**Estructura esperada:**
- Sidebar: Interfaces · WireGuard · DDNS · Firewall
- Contenido principal: tabla de interfaces con stats (rx/tx/speed)
- KPICards arriba: Interfaz activa, Ancho de banda, Conexiones

**Primitivas a usar:**
- `AppShell` + sections
- `KPICard` (3-4 arriba)
- `DenseTable` con columnas: `40px 1fr 120px 80px 80px 80px`
- `LED` para estado de interfaz
- `Sparkline` para rx/tx mini
- `BevelButton` para "Añadir interfaz", "Escanear red"

**Endpoints API** (no tocar):
- `GET /api/network/interfaces`
- `GET /api/network/stats`
- `GET /api/wireguard/config`

**Checklist:**
- [ ] Script copiado
- [ ] Template v3 escrito
- [ ] Style eliminado
- [ ] Endpoints verificados
- [ ] Probado con daemon real

---

### 2. NimTorrent (430 líneas)

**Referencia Beta 7**: `src/lib/apps/NimTorrent.svelte`
**Complejidad**: ⭐⭐ media

**Estructura:**
- Sidebar: Todos · Descargando · Completados · Semillas · Errores
- Tabla densa con: nombre, tamaño, progreso (StripeProgressBar), velocidad ↓/↑, peers, ratio, ETA
- Topbar: búsqueda, botón "Añadir torrent", botón "Añadir magnet"
- Footer: DHT status, peers totales, bandwidth total

**Primitivas:**
- `DenseTable`
- `StripeProgressBar` inline en cada fila
- `KPICard` para stats globales
- `Sparkline` para bandwidth histórico
- `BevelButton`, `IconButton`

**Endpoints:**
- `GET /api/torrents/list`
- `POST /api/torrents/add`
- `PUT /api/torrents/{id}/pause`
- etc.

**Notas especiales:**
- El backend es el daemon C++ `torrentd` · no se toca
- Comunicación HTTP localhost entre Go daemon y torrentd
- Mantener el listado en tiempo real (polling cada 2s)

---

### 3. AppStore (550 líneas)

**Referencia Beta 7**: `src/lib/apps/AppStore.svelte`
**Complejidad**: ⭐⭐ media

**Estructura:**
- Sidebar: Categorías (Media, Desarrollo, Red, Seguridad, Backup, etc.)
- Grid de apps con: icono grande (usa iconos 3D del contenedor), nombre, descripción breve, tag "DOCKER"
- Click en app → modal/ventana con README, versión, tamaño, botón "Instalar"
- Tab "Instaladas" con apps activas y botones start/stop/update/uninstall

**Primitivas:**
- `AppShell` con sections por categoría
- Cards custom (grid simple sin DenseTable)
- `BevelButton` "Instalar" / "Actualizar" / "Desinstalar"
- `StripeProgressBar` durante install/pull Docker
- `Badge` para "UPDATE DISPONIBLE", "INSTALADA"
- `EmptyState` para cuando no hay apps en una categoría

**Endpoints:**
- `GET /api/appstore/catalog`
- `POST /api/appstore/install/{app}`
- `DELETE /api/appstore/uninstall/{app}`
- `GET /api/docker/installed-apps`

---

## 🟢 Fase 2 · Apps centrales (2-3 semanas)

Apps más complejas que son el núcleo del NAS.

### 4. NimHealth (360 líneas)

**Referencia Beta 7**: `src/lib/apps/NimHealth.svelte`
**Mockup retro**: ya diseñado (dashboard + vista detalle)
**Complejidad**: ⭐⭐⭐ media-alta

**Vista principal (dashboard):**
- 4 KPIs arriba: CPU, RAM, Disco I/O, Red — cada uno con corner brackets, valor grande, sparkline del histórico
- Tabs: Todos · Activos · Detenidos · Degradados · Errores
- Tabla de servicios con: icono tipo (docker/native/system), nombre + tag, LED estado, CPU inline bar, RAM inline bar, uptime, I/O, acciones

**Vista detalle:**
- Botón ‹ Volver
- Header: icono + nombre + tag + LED estado
- Auto-refresh indicator (2s)
- InfoBlock con: ID, PID, Pool, Path, Owner, Health, Uptime, Last restart
- Botones grandes: Detener (danger), Reiniciar, Ver logs, Editar config
- SectionHead Dependencias → lista de deps con tags (path/pool/port/service) y required/optional
- SectionHead Logs → CmdOutputLog con follow activo

**Primitivas:**
- `KPICard` (×4)
- `DenseTable`
- `LED`, `StripeProgressBar` (para CPU/RAM inline)
- `SectionHead`
- `CmdOutputLog` (con `follow={true}`)
- `BevelButton` (primary, danger, default)
- `Badge` para tags

**Endpoints:**
- `GET /api/services`
- `GET /api/hardware/stats` (polling cada 2s)
- `GET /api/services/{id}/logs`
- `POST /api/services/{id}/start`
- `POST /api/services/{id}/stop`

**Gotchas:**
- El flatten de Docker children (parent + children planos) está en el original · mantener
- Los 12-slot history arrays para sparklines están bien · reusar

---

### 5. NimShield (nueva en Beta 8)

**Referencia Beta 7**: disperso en `Settings.svelte` sección security
**Mockup retro**: ya diseñado (Live view con eventos + panel lateral)
**Complejidad**: ⭐⭐⭐ media-alta (aunque es una extracción, no migración)

**Estructura:**
- Sidebar: Live · Eventos · Reglas · Honeypots · Políticas
- KPIs: Eventos/min, Bloqueos totales, Honeypots activos, IPs baneadas
- Live event log (CmdOutputLog con follow)
- Panel lateral: honeypot grid + rules activity bars

**Endpoints (ya existen en daemon):**
- `GET /api/shield/events`
- `GET /api/shield/rules`
- `GET /api/shield/honeypots`
- `POST /api/shield/rules/reload`

**Nota**: NimShield existe en el daemon Go (`daemon/shield.go`) desde Beta 7, solo no tenía UI propia. Este es oportunidad de extraerlo.

---

### 6. FileManager (876 líneas)

**Referencia Beta 7**: `src/lib/apps/FileManager.svelte`
**Mockup retro**: ya diseñado
**Complejidad**: ⭐⭐⭐⭐ alta

**Estructura:**
- Sidebar con tree de carpetas + pools montados diferenciados por color
- Toolbar: nav arrows, breadcrumb, search, view toggle
- Action bar: upload, new folder, download, compress, copy, cut, delete
- Tabla densa de archivos con: check, icono tipo, nombre, tamaño, tipo, modificado, propietario, permisos
- Context menu al click derecho con todas las acciones

**Primitivas:**
- `DenseTable` con selección múltiple
- `TextInput` para search
- `BevelButton` para acciones
- `IconButton` para toolbar arrows
- Context menu (nuevo componente ad-hoc)
- `StripeProgressBar` para uploads en curso

**Endpoints:**
- `GET /api/files?share=X&path=Y`
- `POST /api/files/upload` (chunked)
- `POST /api/files/mkdir`
- `DELETE /api/files`
- `POST /api/files/copy` `POST /api/files/move`
- `POST /api/files/compress` `POST /api/files/extract`

**Features críticas a preservar:**
- Chunked uploads con `.nimchunks/` (Beta 7 lo tiene resuelto)
- Clipboard copy/cut persistente
- Context menu con submenu "Abrir con..."
- Download token one-time-use (CRIT-008 de Beta 7)
- Drag & drop de archivos desde el OS

---

### 7. StorageApp (904 líneas) + StoragePanel (2850 líneas)

**Referencia Beta 7**: `src/lib/apps/StorageApp.svelte` + `StoragePanel.svelte`
**Mockup retro**: ya diseñado (con vertical LED bars)
**Complejidad**: ⭐⭐⭐⭐⭐ muy alta (la más grande)

**Estructura:**
- Sidebar: Resumen · Discos · Snapshots · Restaurar · Scrub · SMART · Herramientas
- Summary bar con 4 KPIs: Volúmenes, Discos, Capacidad, Salud
- Lista de pool pills colapsables con:
  - Info: tipo (ZFS/BTRFS), layout (RAIDZ1/...), discos, uso
  - Expanded: vertical LED bars de distribución + donut + disk table
- Context actions por pool: Snapshot, Scrub, Import, Export, Destruir

**Primitivas:**
- `KPICard` (×4 en summary)
- `DenseTable` para discos dentro de pool expandido
- `LED` abundante
- **Vertical LED Bars** (primitiva no creada aún — ver nota)
- `CornerBrackets` ya integrado en KPICard
- `BevelButton` danger para destructivas

**Primitivas que CREAR antes de esta app:**
- `VerticalLEDBars.svelte` — las barras ecualizador del concept de storage

**Endpoints (bien probados en Beta 7):**
- `GET /api/storage/status` — returns pool info granular
- `GET /api/disks/list`
- `POST /api/storage/scrub/{pool}`
- `POST /api/storage/export/{pool}`
- `POST /api/disks/smart/{disk}`

**Features CRÍTICAS a preservar:**
- Diagnostic Layer + State Reducer pattern (`CollectDiagnostics` → `ComputePoolHealth`)
- SMART monitoring con background goroutine
- Stale sdX device path auto-correction desde `zpool status -P`
- Export pool function ZFS + BTRFS (solo si está hecho en Beta 7)

---

## 🟡 Fase 3 · Apps grandes (2 semanas)

### 8. NimBackup (424 líneas)

**Referencia Beta 7**: `src/lib/apps/NimBackup.svelte`
**Complejidad**: ⭐⭐⭐ media-alta

**Estructura esperada:**
- Sidebar: Jobs · Historial · Destinos · Restaurar
- Tabla de backup jobs con schedule, last run, próximo, tamaño, status
- Crear job modal con campos: source pool, destination, schedule cron, retention policy
- Vista de restore: timeline de snapshots navegable

**Primitivas:**
- `DenseTable` + `StripeProgressBar` durante runs activos
- `BevelButton`, `Badge`
- Calendar/timeline custom para snapshots

**Endpoints:**
- `GET /api/backup/jobs`
- `POST /api/backup/jobs`
- `POST /api/backup/run/{job}`
- `GET /api/backup/history`

---

### 9. Settings (1595 líneas) — la grande

**Referencia Beta 7**: `src/lib/apps/Settings.svelte`
**Complejidad**: ⭐⭐⭐⭐⭐ muy alta (muchas secciones)

**Sub-secciones:**
- Apariencia (accent color, wallpaper, escala UI, CRT overlay)
- Usuarios (listado, permisos, ACLs para NimTorrent)
- Red (DDNS, SSL, HTTPS)
- WireGuard (WAN pairing, clientes)
- Sistema (hostname, idioma, zona horaria)
- Notificaciones (categorías, canales)
- Avanzado (terminal enabled flag, logs level, export settings)
- Info (versión, daemon status, contribuir)

**Primitivas:**
- Lots of forms — posiblemente necesitas crear primitivas `FormField`, `Select`, `Toggle`, `Slider`
- `SectionHead` abundante
- `BevelButton` (primary para Apply, danger para reset/delete)

**Plan**: dividir en sub-componentes:
- `src/lib/apps/settings/AppearanceSection.svelte`
- `src/lib/apps/settings/UsersSection.svelte`
- etc.
- `Settings.svelte` solo hace routing interno de sección activa

**Primitivas que CREAR**:
- `FormField.svelte` — wrapper de label + input + help
- `Select.svelte` — dropdown bevel
- `Toggle.svelte` — on/off switch retro (con `[✓]`/`[ ]` o similar)
- `Slider.svelte` — range con tacho bevelado

---

## 🟣 Fase 4 · Utilities (opcional, 1 semana)

### 10. Terminal (nueva)

**Complejidad**: ⭐⭐⭐ (depende del backend)

**Estructura:**
- Full-screen terminal emulator
- Conexión vía `/api/terminal/exec` (ya existe en Beta 7 con audit log)
- Soporta cd, ls, comandos básicos
- Sin interactive shell (por seguridad, blacklist de comandos destructivos)

**Endpoints:**
- `POST /api/terminal/exec` (con `terminalEnabled` flag)

**Primitivas:**
- `CmdOutputLog` reutilizada o adaptada

**Nota de seguridad**: audit log del CRIT-003 de Beta 7 debe mantenerse.

---

### 11. Notes (496 líneas)

**Referencia Beta 7**: `src/lib/apps/Notes.svelte`
**Complejidad**: ⭐⭐ media

**Estructura:**
- Sidebar: árbol de notas (carpetas + notas)
- Editor central (textarea monospace o syntax highlight básico)
- Toolbar: save, delete, export markdown

**Primitivas:**
- `AppShell` con tree sidebar
- `BevelButton`

---

## 📐 Primitivas adicionales a crear durante la migración

Según avanza la migración van a hacer falta primitivas que no existen todavía:

| Primitiva | Cuándo se necesita | Complejidad |
|---|---|---|
| `FormField` | Settings, SetupWizard | ⭐ |
| `Select` | Settings, NimBackup | ⭐⭐ |
| `Toggle` | Settings | ⭐ |
| `Slider` | Settings (scale, intensity) | ⭐⭐ |
| `VerticalLEDBars` | StorageApp | ⭐⭐⭐ |
| `Modal` | varias | ⭐⭐ |
| `ConfirmDialog` | FileManager, Storage | ⭐⭐ |
| `Tooltip` | varias | ⭐⭐ |
| `ContextMenu` | FileManager, Taskbar | ⭐⭐⭐ |
| `Breadcrumb` | FileManager | ⭐ |
| `TreeView` | FileManager, Notes | ⭐⭐⭐ |
| `InfoGrid` (nuevo v3) | NimHealth detalle, Storage | ⭐ |
| `TransferActivityWidget` | opcional si quieres inline | ⭐⭐ |

Cada una cuando se necesite, no todas de golpe.

---

## 📊 Estimación total

- Fase 1 (3 apps livianas): **2-3 días** a 6-8h/día
- Fase 2 (4 apps centrales): **3-4 semanas** a 10h/semana
- Fase 3 (2 apps grandes): **2 semanas** a 10h/semana
- Fase 4 (2 utilities): **1 semana** opcional

**Total realista**: 7-9 semanas a ritmo sostenible (10-15h/semana).
**Total acelerado**: 4-5 semanas a ritmo intenso (20h/semana).

---

## ✅ Checklist de release para Beta 8.0

Antes de promocionar Beta 8 como producción:

- [ ] Todas las apps de Fase 1-3 funcionando (no stubs)
- [ ] Fase 4 al menos en stub funcional (no bloqueante)
- [ ] Login + SetupWizard probados
- [ ] Taskbar con todas las integraciones (transferencias, notificaciones)
- [ ] Launcher filtrando bien apps Docker dinámicas
- [ ] QA manual de cada app (crear, editar, borrar, scroll, resize)
- [ ] Mobile check (al menos que no explote, aunque no esté optimizado)
- [ ] Screenshots para launch público/blog
- [ ] README final con screenshots
- [ ] Tag de release: `v0.8.0`
- [ ] Mantener Beta 7 accesible como fallback durante 1 mes

---

## 🚨 Cosas que NO tocar en esta migración

Para evitar el scope creep y que la migración no termine nunca:

- **Backend Go (daemon)** — sigue tal cual. Si encuentras un bug, se arregla en Beta 7 y se cherry-picka, no se rehace.
- **DB SQLite** — mismo schema. Mismos migraciones.
- **Autenticación JWT** — mismo flujo.
- **torrentd (C++)** — sigue tal cual.
- **HELIOS** — es un proyecto separado, no entra en Beta 8.
- **Tests de seguridad** — los 215/218 de Beta 7 siguen aplicando al daemon compartido.

Lo único que cambia es el frontend. Todo lo demás se mantiene.
