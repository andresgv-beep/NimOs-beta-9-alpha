# PORT-ALLOCATOR-DESIGN

**Proyecto:** NimOS · subsistema de apps Docker
**Versión:** 1.1 (decisiones cerradas · lista para implementar)
**Fecha:** 21/06/2026
**Estado:** spec aprobada · implementación por fases (DISCIPLINE v2.1)
**Diseño:** Claude (socio) · revisión arquitectónica + decisiones: Andrés

> Cambios v1.0 → v1.1: `PortsJSON` única fuente de verdad (se elimina `port_allocations`);
> pool flotante 30000–59999; reservados en dos niveles (duro/blando); Transmission convive
> con NimTorrent vía allocator. Detalle en §10.

---

## 1. El problema (por qué esto no se parchea a mano)

Las apps del catálogo publican puertos host que colisionan con:

1. **Servicios nativos de NimOS** — caso real: **Transmission** publica por defecto en `9091`, el mismo puerto que reserva **NimTorrent** (`nimos-torrentd`, `127.0.0.1:9091`). → `failed to bind host port 0.0.0.0:9091/tcp: address already in use`.
2. **Otras apps instaladas** — caso real: Pi-hole y AdGuard compiten por el `:53` (DNS).
3. **Cualquier cosa del host.**

Hoy explota en `docker compose up` con un 500 críptico. **Parchear el `default` de cada app a mano no escala** (con ~30 apps ya colisionan dos; con 100+ es inviable y seguirías eligiendo defaults a ciegas). Problema sistémico → solución sistémica.

### Dos objetivos que NO son el mismo

- **(A) Puerto editable por el usuario** (estilo Synology) → `configField` por app.
- **(B) Cero colisiones, automáticamente, para toda app presente y futura** → **motor de asignación**.

**Este documento resuelve (B).** (A) se monta encima: un `configField` que, si el user no lo toca, deja que el motor asigne.

---

## 2. Activos que YA existen (no reinventar)

| Pieza | Dónde | Qué aporta |
|-------|-------|------------|
| `PortBinding{Declared, Host, Protocol}` | `models.go:537` | Separa puerto **declarado** (compose) del **host** (real). Substrato exacto del asignador. |
| `docker_apps.PortsJSON` | `db_apps.go:45` (APP-033) | **Almacén canónico multi-puerto por app** (array de `PortBinding`). **Única fuente de verdad** (ver §4). |
| `parsedPorts()` | `db_apps.go:93` | Deserializa `PortsJSON`, fallback a `Port` legacy como binding único. |
| `rewriteComposePorts(src, loopback)` | `docker_access_mode.go:43` | **Reescritor de `ports:` puro y testeado** (`yaml.Node`): corta, larga, `/udp`, IPs, preserva comentarios. Lo **extendemos** para remapear el número de puerto host. |
| `parsePorts(rawPorts, config)` | `nimhealth_docker.go:382` | Parser de bindings desde `docker ps`. |
| `uid_allocator` / `reconcileAppUIDs` | subsistema permisos | Patrón mental gemelo (con **una diferencia clave**, §4). |

El reescritor (la parte frágil) y el modelo de datos **ya existen**. El trabajo es conectar las piezas con la política de asignación.

---

## 3. Puertos reservados — DOS NIVELES

Distinción crítica (si se ignora, se bloquea a Pi-hole/AdGuard).

### 3.1 Reservado DURO — NimOS los posee, nunca disponibles (ni fijo ni flotante)

| Puerto | Servicio | Fuente |
|--------|----------|--------|
| `5000` | Daemon HTTP NimOS | `http.go:20` |
| `9091` | NimTorrent (`nimos-torrentd`) | `apps.go:86`, `static.go:191` |
| Caddy `http_port` (def. 80) | Caddy | `network_exposure_config` (DB) · **dinámico** |
| Caddy `https_port` (def. 443; **444** en prod) | Caddy | `network_exposure_config.HTTPSPort` · **dinámico** |

### 3.2 Reservado BLANDO — well-known del sistema

| Puerto | Servicio |
|--------|----------|
| `22` | SSH |
| `53` | DNS |
| `67` / `68` | DHCP |
| `123` | NTP |

**Regla blanda:** NO se auto-asignan a puertos **flotantes**, PERO una app que los pida **explícitamente como fijo** (Pi-hole pidiendo `53`) **sí puede reclamarlos** si el host los tiene libres. Entre dos que pidan el mismo fijo (Pi-hole vs AdGuard en `53`) → flujo "elige una".

> Implementación: `reservedHard() []int` (constantes + Caddy de config) y `reservedSoft() []int` (constantes). `ss` solo como **verificación** final, nunca como fuente de decisión.

---

## 4. Modelo de datos — `PortsJSON` ÚNICA fuente de verdad

**Decisión cerrada: NO se crea `port_allocations`.**

Razón (no es por miedo al desync — eso es recuperable con wipe+reinstall en esta fase): **la analogía con el UID allocator se rompe aquí.** `uid_allocator` existe porque los UIDs **nunca se reutilizan** (hay que recordar los retirados). Los **puertos SÍ se reutilizan** → no hay nada que recordar más allá de las apps vivas, y las apps vivas ya están en `PortsJSON`. Sin esa necesidad, una tabla aparte solo añade una segunda fuente de verdad y un lookup O(1) innecesario.

- **`PortBinding`**: `Declared` = puerto contenedor (del compose, inmutable); `Host` = puerto host **asignado por el motor**; `Protocol` = tcp/udp.
- **Mapa de ocupación**: se construye en cada install escaneando el `PortsJSON` de las apps instaladas + reservados (duro/blando). Con 30/100/1000 apps son microsegundos. No hay necesidad de O(1).
- **Reconciler**: también deriva de `PortsJSON`. Sin tabla.

> Si en el futuro aparece una necesidad concreta (no especular, §1), se evalúa una tabla entonces. La red de "wipe + reinstalar" hace esa migración trivial.

### Política de asignación

- **Pool flotante: `30000–59999`** (cerrado). Margen amplio, identificable a ojo como "asignado por NimOS".
- **Preferido-si-libre**: si el puerto del compose está libre y no es reservado-duro → **se respeta** (no se reasigna por gusto; mantiene URLs/bookmarks).
- **Pegajoso pero reutilizable**:
  - La app **conserva su `Host` entre reinstalaciones** (persistido en `PortsJSON`) → no rompe marcadores.
  - Al **desinstalar**, el puerto **vuelve al pool**. (UIDs nunca se reutilizan; puertos sí.)

---

## 5. Clasificación fijo vs flotante (contrato `purpose`)

| Tipo | Marca | Comportamiento |
|------|-------|----------------|
| **Flotante** | `purpose:"network_port"` (web TCP) | Reasignable. Acceso por Caddy/dominio (puerto invisible) o por `host:puerto` que NimOS **muestra**. Choca → siguiente libre, sin molestar. |
| **Flotante silencioso** | sin `purpose` (peer ports: 51413, 6881) | Reasignable. No va al launcher, pero se le da host libre. |
| **Fijo** | `purpose` fijo + `fixed:true` (DNS `:53`, puerto de juego que el cliente espera) | **NO** se reasigna. Puede reclamar reservado-blando si está libre. Choca → "elige una". |

**Regla tcp+udp del mismo puerto lógico** (51413/tcp + 51413/udp, `:53` tcp+udp): un puerto lógico con dos protocolos → **mismo `Host`** para ambas líneas.

---

## 6. Flujo en install (corazón)

En `dockerStackDeploy`, **antes** de `compose up`:

1. **Parsear** bindings declarados del compose (`Declared`, `Protocol`) — walker `yaml.Node` reusado de `rewriteComposePorts`.
2. **Mapa de ocupación**: `reservedHard()` + `reservedSoft()` + `PortsJSON` de apps instaladas (+ `ss` verificación opcional).
3. **Resolver cada binding**:
   - ¿App ya tenía `Host` (reinstall)? → **reusar** (pegajoso).
   - Flotante → preferido-si-libre, si no → siguiente libre del pool (30000–59999).
   - Fijo → preferido (puede ser reservado-blando si está libre); si ocupado → **error pre-flight** (no llega a docker), "elige una".
   - tcp+udp del mismo puerto → mismo `Host`.
4. **Reescribir compose**: lado host → `Host` asignado (extender `rewriteComposePorts` para remapear el número, coordinado con el modo loopback — una sola pasada que aplique IP + número).
5. **Persistir** `PortsJSON` (todos los bindings con su `Host`) + `Port` legacy = `Host` del `network_port` (compat launcher).
6. **`compose up`**.

Resultado: el catálogo declara puertos naturales y NimOS garantiza que no colisionen. Cero trabajo por app.

---

## 7. Integración con lo existente

- **Launcher** (`apps_launchable.go:83`): usa `a.Port`. Mantenemos `a.Port = Host` del `network_port`. (Migrar a `PortsJSON` = opcional/posterior.)
- **Panel de Juego**: ya compone la dirección con el host port registrado → mismo patrón, consistente.
- **Access mode loopback** (`rewriteComposePorts`): ya reescribe IP; el motor reescribe número. **Coordinar en una sola reescritura** para no pisarse.
- **Apps por Caddy**: host port invisible al usuario → flota libre sin fricción.
- **Check de conflicto en el modal**: endpoint de ocupación → aviso antes de instalar; para fijos en conflicto, "elige una". Cara visible del motor.

---

## 8. Rangos (Valheim 2456–2458)

Asignar **hueco contiguo libre** del tamaño pedido en el pool. Más complejo (no es puerto suelto) → **fase posterior**. `PortBinding` actual es por puerto único; un rango = N bindings contiguos o extensión del struct. Decisión para esa fase.

---

## 9. Fases verificables (estilo PERMISOS-DESIGN)

Cada fase compila, pasa tests, no rompe lo anterior. Funciones puras primero.

1. **Fase 1 · Reservados + ocupación.** `reservedHard()` (constantes + Caddy de config), `reservedSoft()`, `isPortFree(port, occupancy)`, construcción del mapa desde `PortsJSON` de apps instaladas. Tests. **No toca install. SIN tabla nueva.**
2. **Fase 2 · Allocator puro.** `allocatePort(declared, protocol, occupancy, sticky)` — preferido/siguiente-libre, pegajoso, respeta duro/blando. Tests de tabla (colisión, reservado-duro, reservado-blando reclamable, reinstall pegajoso, pool agotado). **No toca install.**
3. **Fase 3 · Reescritor de host port.** Extender `rewriteComposePorts`/`rewritePortSpec` para remapear el número host (+ misma var tcp/udp). **Máxima inversión de tests aquí** — es el componente de mayor riesgo. Cubrir: `"8080:80"`, `"127.0.0.1:8080:80"`, `"8080:80/udp"`, escalar `8096`, sintaxis larga `{target, published}`.
4. **Fase 4 · Wiring en install.** Conectar en `dockerStackDeploy`: resolver → reescribir → persistir `PortsJSON` + `Port` legacy. **Solo flotantes TCP** primero (95%). Verificar en hierro (Pi ARM64 + amd64): instalar dos apps que pidan el mismo puerto → la segunda flota sola.
5. **Fase 5 · Fijo vs flotante + check de conflicto.** Contrato `purpose`/`fixed`, pre-flight para fijos, reclamación de reservado-blando, endpoint de ocupación + aviso en modal. Resuelve Pi-hole/AdGuard `:53` con UX clara.
6. **Fase 6 · udp/multi-binding + rangos.** Mismo host tcp+udp, rangos contiguos (Valheim). Cierra el wiring udp del backlog.

---

## 10. Decisiones cerradas (v1.1)

| Tema | Decisión |
|------|----------|
| Fuente de verdad | **`docker_apps.PortsJSON`** única. **No** se crea `port_allocations`. |
| Pool flotante | **30000–59999** |
| Reservados duros | `5000`, `9091`, Caddy http/https (dinámico) |
| Reservados blandos | `22`, `53`, `67`, `68`, `123` (no auto-flotante; reclamables como fijo) |
| `ss` | Solo verificación, nunca fuente de decisión |
| Persistencia | Pegajosa entre reinstalaciones, reutilizable al desinstalar |
| Transmission | **Se mantiene** en catálogo; convive con NimTorrent vía allocator (puerto flota a 30000+) |
| Riesgo principal | `rewriteComposePorts` extendido → batería fuerte de tests (Fase 3) |

---

## 11. Alcance — qué NO hace

- No resuelve la **exposición multi-servicio por Caddy** (backlog item 2 · `getAppPort` asume 1 app = 1 puerto). Asigna y registra multi-puerto, pero la exposición HTTP de varios servicios por Caddy es otro trabajo.
- No implementa el **tracking/exposición udp** más allá de bindear el puerto (binding funciona; seguimiento udp = Fase 6 + backlog).
- No toca **UIDs/permisos** (ortogonal).

---

## 12. Resumen en una frase

NimOS ya tiene el modelo (`PortBinding`/`PortsJSON`), el reescritor de compose y el patrón mental del allocator. Falta el motor que, en install, respete reservados (duro/blando) y los puertos de otras apps, reasigne flotantes en conflicto y reescriba el compose — convirtiendo "parchear cada app a mano" en "el catálogo declara, NimOS asigna", con `PortsJSON` como única fuente de verdad.
