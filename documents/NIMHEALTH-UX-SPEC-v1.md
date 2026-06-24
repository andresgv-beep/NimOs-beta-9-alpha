# NimOS System Health — UX Spec v1

## Qué es esta app

No es un "App Manager". No es un "Task Manager".
Es la respuesta a: **"¿qué le pasa a mi NAS?"**

El usuario abre esta app cuando algo va mal, algo va lento, o quiere hacer algo
destructivo (destruir pool, wipear disco) sin romper el sistema.

---

## Nombre

**NimHealth** (o System Health dentro de NimSettings)

No "App Manager" — eso suena a instalar/desinstalar apps.
No "Task Manager" — eso suena a Windows.
Esto es diagnóstico + control + protección.

---

## Preguntas que resuelve

1. **"¿Por qué mi NAS va lento?"**
   → Qué servicio está consumiendo CPU/RAM/disco ahora mismo

2. **"¿Por qué NimTorrent no funciona?"**
   → Está stopped, su pool no está montado, o el daemon crasheó

3. **"¿Por qué Containers no responde?"**
   → Docker está running pero el engine está degraded, o el pool está lleno

4. **"¿Qué está usando mi pool volume2?"**
   → Docker, NimTorrent, NimBackup, 5 shares — X GB ocupados

5. **"¿Puedo destruir este pool sin romper nada?"**
   → No: Docker y NimTorrent dependen de él. Detén esto primero.

6. **"¿Qué tarea está matando mi NAS?"**
   → NimTorrent está descargando 8 torrents, 95% de I/O de disco

7. **"¿Puedo reiniciar este servicio sin romper nada?"**
   → Sí, NimTorrent no tiene dependientes. O: No, Docker tiene 3 containers activos.

---

## Vistas

### Vista 1: Dashboard de salud (vista principal)

Lo que ves al abrir la app. Un vistazo de 2 segundos te dice si algo va mal.

```
┌─────────────────────────────────────────────────────────┐
│  System Health                                    [⟳]   │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌────────┐  │
│  │ CPU      │  │ RAM      │  │ Disco    │  │ Red    │  │
│  │ 23%      │  │ 4.2/16GB │  │ 45MB/s   │  │ 12MB/s │  │
│  │ ▁▂▃▂▁▃  │  │ ▇▇▇▇░░  │  │ ▃▅▇▅▃▂  │  │ ▂▃▂▁▂▃ │  │
│  └──────────┘  └──────────┘  └──────────┘  └────────┘  │
│                                                         │
│  Servicios                                              │
│  ┌─────────────────────────────────────────────────┐    │
│  │ ● Docker          running  healthy    volume2   │    │
│  │ ● NimTorrent      running  healthy    volume2   │    │
│  │ ○ NimBackup       stopped             volume2   │    │
│  │ ✕ Virtual Machines error   unreachable          │    │
│  └─────────────────────────────────────────────────┘    │
│                                                         │
│  ⚠ 1 servicio con error · 1 detenido                   │
└─────────────────────────────────────────────────────────┘
```

**Elementos:**

- **Métricas del sistema** — CPU, RAM, I/O disco, red. Mini gráficos en tiempo real (últimos 60s).
  No es un monitor completo (eso ya es System Monitor) — es un resumen rápido para
  correlacionar "el NAS va lento" con "NimTorrent está al 95% de disco".

- **Lista de servicios** — Solo servicios con proceso backend (type != 'ui').
  Cada uno muestra: nombre, status, health, pool. Un vistazo.
  Color del indicador: verde (running/healthy), gris (stopped), rojo (error), ámbar (starting/degraded).

- **Barra de resumen** — "3 activos · 1 detenido · 1 error". Alerta visual si hay errores.

**Interacción:**
- Click en un servicio → abre Vista 2 (detalle)
- El dashboard se refresca cada 5 segundos (polling ligero)

---

### Vista 2: Detalle de servicio

Lo que ves al clickar un servicio. Responde a "¿qué le pasa a ESTE servicio?"

```
┌─────────────────────────────────────────────────────────┐
│  ← Docker (Containers)                            [⟳]  │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  Estado                                                 │
│  ┌─────────────────────────────────────────────────┐    │
│  │  Status    ● running                            │    │
│  │  Health    healthy                               │    │
│  │  Uptime    3d 14h 22m                           │    │
│  │  Gestor    docker (systemctl)                    │    │
│  └─────────────────────────────────────────────────┘    │
│                                                         │
│  Recursos                                               │
│  ┌─────────────────────────────────────────────────┐    │
│  │  CPU       12%    ▃▅▃▂▃▅▇▅▃                    │    │
│  │  RAM       1.2 GB                                │    │
│  │  Disco     /nimos/pools/volume2/docker          │    │
│  │            23.4 GB usados                        │    │
│  └─────────────────────────────────────────────────┘    │
│                                                         │
│  Dependencias                                           │
│  ┌─────────────────────────────────────────────────┐    │
│  │  pool   volume2         requerido               │    │
│  │  path   .../docker      requerido               │    │
│  └─────────────────────────────────────────────────┘    │
│                                                         │
│  Acciones                                               │
│  ┌──────────┐  ┌────────────┐  ┌──────────────────┐    │
│  │ Detener  │  │ Reiniciar  │  │ Ver logs         │    │
│  └──────────┘  └────────────┘  └──────────────────┘    │
│                                                         │
│  ⚠ Detener Docker parará todos los containers activos   │
└─────────────────────────────────────────────────────────┘
```

**Elementos:**

- **Estado** — Status, health, uptime, quién lo gestiona. Claro y directo.

- **Recursos** — CPU y RAM de ese servicio específico (no del sistema).
  Path en el pool y espacio usado. Si el disco está >90% → warning visual.

- **Dependencias** — Qué necesita este servicio para funcionar.
  Pool, share, path. Con nivel (requerido/soft/opcional).
  Si alguna dependencia está rota → se muestra en rojo con explicación.

- **Acciones** — Detener, Reiniciar, Ver logs.
  Si detener tiene consecuencias (ej: Docker para containers), se muestra warning.
  Si el servicio está en error, el botón principal es "Reiniciar" o "Diagnosticar".

- **Warning contextual** — "Detener Docker parará todos los containers activos".
  "NimTorrent tiene 3 descargas en progreso". Información antes de actuar.

**Interacción:**
- Botón Detener → confirmación con impacto ("Esto parará 3 containers")
- Botón Reiniciar → stop + start con spinner
- Botón Ver logs → últimas 50 líneas del servicio (journalctl / docker logs)

---

### Vista 3: Pool Dependencies (se abre desde Storage Manager)

Esto NO es una vista dentro de NimHealth — es un **modal que aparece en Storage Manager**
cuando el usuario intenta destruir un pool que tiene servicios activos.

```
┌─────────────────────────────────────────────────────────┐
│  ⚠ No se puede destruir "volume2"                      │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  Estos servicios dependen de este pool y están activos: │
│                                                         │
│  ┌─────────────────────────────────────────────────┐    │
│  │ ● Docker          running    [Detener]          │    │
│  │ ● NimTorrent      running    [Detener]          │    │
│  └─────────────────────────────────────────────────┘    │
│                                                         │
│  Detén todos los servicios antes de destruir el pool.   │
│                                                         │
│  ┌───────────────┐  ┌───────────────────────────────┐   │
│  │   Cancelar    │  │  Detener todos y destruir     │   │
│  └───────────────┘  └───────────────────────────────┘   │
│                                                         │
│  ○ NimBackup        stopped    (no bloquea)            │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

**Flujo:**

1. Usuario en Storage Manager → click "Destruir pool"
2. Frontend llama `GET /api/services/dependencies?pool=volume2`
3. Si `canDestroy: false`:
   - Muestra este modal con la lista de servicios activos
   - Cada servicio tiene botón "Detener" que llama `POST /api/services/{id}/stop`
   - Cuando todos están stopped, el botón "Destruir" se habilita
4. Si `canDestroy: true`:
   - Modal de confirmación normal ("¿Estás seguro? Los datos se perderán")
5. Si `canForce: true` (solo soft dependencies):
   - Muestra warning pero permite continuar con confirmación extra

**Botón "Detener todos y destruir":**
- Solo aparece si el usuario es admin
- Ejecuta stop secuencial de todos los servicios
- Spinner mientras para cada uno
- Cuando todos stopped → ejecuta destroy automáticamente
- Si algún stop falla → se detiene y muestra el error

---

## Datos que necesita cada vista

### Dashboard (Vista 1)

```
GET /api/services                    → lista de servicios con status/health
GET /api/hardware/stats              → CPU, RAM, disco, red (ya existe)
```

Polling cada 5 segundos. Payload ligero (~500 bytes).

### Detalle de servicio (Vista 2)

```
GET /api/services/{id}               → detalle del servicio
GET /api/services/{id}/dependencies  → dependencias (se puede incluir en el GET anterior)
GET /api/services/{id}/stats         → CPU/RAM del proceso específico (NUEVO)
GET /api/services/{id}/logs          → últimas N líneas de log (NUEVO)
```

### Modal de destroy pool (Vista 3)

```
GET /api/services/dependencies?pool=X  → ya existe
POST /api/services/{id}/stop           → ya existe
DELETE /api/storage/pools/{pool}        → ya existe (se modifica para consultar deps)
```

---

## Endpoints nuevos necesarios

### GET /api/services/{id}/stats

Devuelve CPU y RAM del proceso del servicio.

```json
{
  "cpu": 12.3,
  "ram": 1258291200,
  "ramFormatted": "1.2 GB",
  "diskUsed": 25123456789,
  "diskFormatted": "23.4 GB"
}
```

Implementación:
- systemd: `systemctl show {unit} --property=MemoryCurrent,CPUUsageNSec`
- docker: `docker stats --no-stream --format "{{.CPUPerc}}|{{.MemUsage}}"`
- internal: `/proc/{pid}/stat` + `/proc/{pid}/status`
- Disk: `du -sb {path}` (con cache — no ejecutar en cada request)

### GET /api/services/{id}/logs

Devuelve las últimas N líneas de log.

```json
{
  "lines": [
    {"timestamp": "2026-04-01T10:23:45Z", "message": "Started NimTorrent daemon"},
    {"timestamp": "2026-04-01T10:23:46Z", "message": "Listening on 127.0.0.1:9091"}
  ]
}
```

Implementación:
- systemd: `journalctl -u {unit} -n 50 --no-pager -o json`
- docker: `docker logs --tail 50 {container}`
- internal: `tail -50 /var/log/nimos/daemon.log`

---

## Integración con Storage Manager

Storage Manager NO necesita cambios en su UI actual excepto uno:

**Antes de ejecutar destroy pool**, el handler de destroy debe:

1. Llamar a `canDestroyPool(poolName)` (ya implementado en services.go)
2. Si no puede → devolver 409 con la lista de dependencias
3. El frontend de Storage Manager muestra el modal de Vista 3

Esto se hace en `destroyPoolZfs` y `destroyPoolBtrfs` (storage_zfs_pool.go y storage_btrfs_pool.go).

---

## Qué NO es esta app

- **No es System Monitor** — System Monitor muestra gráficos históricos, temperaturas, SMART.
  NimHealth muestra el estado AHORA y permite actuar.

- **No es un App Store** — No instalas ni desinstalas apps aquí.
  Aquí controlas lo que ya está instalado y corriendo.

- **No es Storage Manager** — No creas ni destruyes pools aquí.
  Pero Storage Manager te redirige aquí cuando necesitas parar servicios antes de destruir.

- **No reemplaza los logs** — Muestra las últimas líneas para diagnóstico rápido.
  Para análisis profundo, el usuario va a Terminal.

---

## Flujos de usuario completos

### "Mi NAS va lento"

```
1. Abre NimHealth
2. Ve Dashboard: CPU 95%, disco I/O al máximo
3. Ve que NimTorrent está consumiendo 90% de I/O
4. Click en NimTorrent → Vista detalle
5. Ve: 8 torrents activos, 45 MB/s de escritura
6. Click "Detener" → confirma
7. NimTorrent para → sistema vuelve a la normalidad
8. Más tarde: reinicia NimTorrent cuando quiera
```

### "Quiero destruir un pool"

```
1. Abre Storage Manager
2. Click "Destruir pool volume2"
3. Storage Manager consulta dependencias
4. Modal: "Docker y NimTorrent están usando este pool"
5. Click "Detener" en Docker → espera → stopped ✓
6. Click "Detener" en NimTorrent → espera → stopped ✓
7. Botón "Destruir" se habilita
8. Click "Destruir" → confirmación final → pool destruido
9. service_instances se limpia automáticamente
```

### "Containers no responde"

```
1. Abre NimHealth
2. Ve Dashboard: Docker status=running pero health=degraded
3. Click en Docker → Vista detalle
4. Ve: Docker engine running pero 0 containers respondiendo
5. Ve logs: "Error: no space left on device"
6. Entiende el problema: pool lleno
7. Va a Storage Manager → libera espacio o expande pool
8. Vuelve a NimHealth → reinicia Docker → healthy
```

### "¿Qué está usando mi disco?"

```
1. Abre NimHealth
2. Dashboard muestra servicios por pool
3. Docker: 23 GB en volume2
4. NimTorrent: 156 GB en volume2 (shares)
5. NimBackup: 45 GB en volume2 (snapshots)
6. Total consumo de servicios visible de un vistazo
```

---

## Prioridad de implementación

### Fase 1 — Mínimo funcional (lo que desbloquea el destroy seguro)
- Dashboard con lista de servicios (sin métricas del sistema)
- Status + health + pool de cada servicio
- Botones start/stop/restart
- Modal de destroy pool en Storage Manager
- Endpoint dependencies?pool=X integrado en destroy

### Fase 2 — Diagnóstico
- Métricas del sistema en el dashboard (CPU, RAM, disco, red)
- Stats por servicio (CPU/RAM del proceso)
- Logs por servicio (últimas 50 líneas)

### Fase 3 — Inteligencia
- Alertas automáticas ("disco al 90%", "servicio crasheó")
- Historial de reinicios
- Correlación: "NimTorrent reiniciado 3 veces en 1 hora"

---

## Notas para Sonnet (UI)

- La app sigue el patrón NimOS: sidebar + inner-wrap > inner > titlebar + content + statusbar
- Sidebar tiene filtros: Todos, Running, Errors, por pool
- NO mostrar apps con type='ui' — solo servicios con proceso real
- Las service cards son el elemento principal — compactas pero informativas
- El detalle se abre como panel expandible o como navegación interna, no como modal
- Colores: verde=running/healthy, rojo=error/unreachable, ámbar=starting/degraded/warning, gris=stopped
- El modal de destroy pool es un componente separado que Storage Manager importa
- Responsive: en móvil la sidebar se colapsa y las cards se apilan
