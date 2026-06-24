# Sistema de Asignación de Puertos (Port Allocator) · NimOS

> Estado: IMPLEMENTADO Y VALIDADO EN HIERRO · 21/06/2026
> Autor de la idea: Andrés · documentado con Claude
> Relación: gemelo del UID allocator (PERMISOS-DESIGN.md). Independiente, mismo patrón.
> Arquitectura: Raspberry Pi ARM64 (producción) + amd64 Z370 (Ubuntu). Agnóstico de arch.

> ┌─────────────────────────────────────────────────────────────────────┐
> │ MAPA DE DOCUMENTOS                                                    │
> │                                                                      │
> │  1. ESTE doc = el subsistema de puertos COMPLETO (porqué + cómo).    │
> │     · El problema, el principio, el modelo de reservados, las fases  │
> │       del allocator, el preflight, el badge, validación y roadmap.   │
> │                                                                      │
> │  2. APP-CATALOG-SCHEMA.md = contrato del catálogo (configFields,     │
> │     purpose:"network_port", etc.). Donde difieran, MANDA CATALOG.    │
> │                                                                      │
> │  3. PERMISOS-DESIGN.md = el UID allocator (gemelo conceptual: misma  │
> │     idea de "el catálogo declara, NimOS asigna", aplicada a UIDs).   │
> └─────────────────────────────────────────────────────────────────────┘

---

## El problema

Las apps del catálogo declaran puertos host literales en su compose (`'9091:9091'`,
`'8081:8081'`...). Eso **no escala**:

1. **Colisiones reales.** La web de Transmission (9091) choca con el daemon nativo
   NimTorrent (`nimos-torrentd`, reserva 9091 web + 6881 peer). Pi-hole y AdGuard
   chocan en el `:53`. qBittorrent reserva 6881 (peer), que también es de NimTorrent.
2. **Parametrizar app por app es tedioso Y no resuelve colisiones.** Aunque
   pongas cada puerto como configurable en el catálogo, dos apps pueden seguir
   pidiendo el mismo puerto y el usuario tiene que resolverlo a mano.

La parametrización manual del catálogo (ver PORT-PARAMETRIZACION-TRIAGE histórico)
queda **mayormente obsoleta** para colisiones: el allocator las resuelve solo.

---

## El principio rector

**"El catálogo declara, NimOS asigna."**

El catálogo describe lo que la app necesita (puertos en su compose). NimOS, en el
deploy, **auto-asigna puertos host libres de conflicto** para todas las apps con
puertos literales, sin trabajo por-app. Es el gemelo exacto del UID allocator:
el catálogo no decide el puerto final, lo decide NimOS.

---

## Modelo de reservados (port_reserved.go)

Dos clases de puerto que el allocator NO entrega al pool:

### Reservados DUROS — propiedad de NimOS, jamás disponibles
| Puerto | Dueño | Constante |
|--------|-------|-----------|
| 5000 | Daemon NimOS (http.go:20) | `portDaemonHTTP` |
| 9091 | NimTorrent web (apps.go) | `portNimTorrent` |
| 6881 | NimTorrent peer BitTorrent | `portNimTorrentPeer` |
| 2019 | Caddy admin | `portCaddyAdmin` |
| http/https | Caddy (dinámico) | `networkRepo.GetExposureConfig` (default 80/443, prod 444) |

`reservedHard(caddyHTTP, caddyHTTPS)` los devuelve. Los de Caddy se leen en
runtime (nil-safe), no se hardcodean.

### Reservados BLANDOS — "fijos por naturaleza", no flotan
`reservedSoft()` → **22, 53, 67, 68, 123** (SSH, DNS, DHCP server/client, NTP).

Son puertos donde **el consumidor espera EXACTAMENTE ese número** (las máquinas
los hardcodean: un cliente DNS siempre pregunta al :53). Moverlos a un puerto del
pool dejaría el servicio **inalcanzable**. Por eso el allocator NO los reasigna —
los deja intactos. Si dos apps necesitan el mismo puerto blando → conflicto
inevitable que captura el **preflight** (ver abajo), no el allocator.

> Eje clave: para un puerto web, lo que se mueve es el PUERTO (un humano teclea
> `:30000`). Para un puerto fijo (DNS), lo que se movería es la IP, no el puerto
> (ver Nivel 2 / IP-Allocator en el roadmap). El allocator solo toca puertos.

---

## Decisión clave: PortsJSON es la única fuente de verdad

**NO existe una tabla `port_allocations`** (a diferencia del UID allocator, que sí
tiene `app_uids` + `uid_allocator`). Razón: **los puertos son REUSABLES**. Cuando
una app se desinstala, su puerto vuelve a estar libre — no hay riesgo de "reusar"
nada peligroso, al revés que con los UIDs (donde reusar un UID puede dar acceso a
ficheros de un dueño anterior). Por eso la razón-de-ser de la tabla del UID
allocator no aplica aquí.

La ocupación se calcula en vivo desde `docker_apps.PortsJSON` (APP-033, canónico).
`PortBinding{Declared, Host, Protocol}` (models.go) es el modelo; `parsedPorts()`
lo lee.

---

## El allocator, por fases (todas IMPLEMENTADAS + tests)

### Fase 1 · Reservados + ocupación · `port_reserved.go` (+ test)
- `reservedHard(caddyHTTP, caddyHTTPS)`, `reservedSoft()`.
- `occupiedHostPorts(apps) → map[int]bool` (puertos host ya usados, leídos del PortsJSON).
- `isPortFree(port, occupied, hard)`, `inFloatPool(port)`.
- Constantes de reservados + `floatPoolMin/Max`.

### Fase 2 · El allocator · `port_allocator.go` (+ test)
- `allocatePort(preferred, fixed, sticky, occupied, hard, soft)`:
  - **sticky** (puerto previo si la app ya existía) → **preferido-si-libre** → **pool**.
  - Puertos **fijos** dan error si están tomados (pueden reclamar un blando).
  - Puertos **flotantes** nunca caen sobre un blando.
- **Pool: 30000–59999.**

### Fase 3 · Reescritor de host · `port_rewrite.go` (+ test, heavy)
- `rewriteComposeHostPorts(src, remap map[int]int)` + `rewriteHostPortSpec`.
- Remapea el NÚMERO del puerto host en todas las formas: `host:cont`,
  `ip:host:cont`, `/udp`, escalar, long-syntax `published`.
- **`${VAR}` en host se deja INTACTO** a propósito (lo resuelve el env feature → roadmap).

### Fase 4 · Wiring en install · `docker_stacks.go`
- En `dockerStackDeploy`, tras inyección de labels y ANTES de `os.WriteFile(composePath)`:
  recoge ocupación (otras apps), reservados (hard con Caddy dinámico + soft), sticky
  (bindings previos), llama a `resolveStackHostPorts`, reescribe el compose y registra
  `Port` + `PortsJSON`.
- **100% NO-BLOQUEANTE**: cualquier error / sin-puerto / `${VAR}` → fallback al
  comportamiento previo (usa el puerto declarado). Nunca rompe un deploy.

### Fase 6 · Multi-binding · `port_bindings.go` (+ test)
- `parseComposeBindings`, `parseShortBinding`, `parseLongBinding`.
- `resolveStackHostPorts(compose, declaredMain, prevBindings, occupied, hard, soft)`
  → `(composeOut, mainHost, portsJSON, err)`.
- Reasigna **TODOS** los bindings flotantes (no solo el principal).
- **tcp+udp del mismo host lógico → mismo puerto host nuevo.**
- Los **fijos por naturaleza** (container o host es un blando, p.ej. DNS 53) **NO se reasignan**.
- Sticky por-binding desde `prev.parsedPorts()`.

---

## El Preflight de conflictos de puerto fijo · `port_preflight.go` (+ test)

**El problema que cierra:** el allocator deja los puertos fijos (`:53`) en su sitio
(correcto). Pero si DOS apps necesitan el mismo puerto fijo (Pi-hole y AdGuard en
:53), chocan de forma **inevitable**. Antes, NimOS intentaba el deploy igual, docker
fallaba a medias (`Bind for :::53 failed: port is already allocated · status 500`)
y dejaba basura: red `*_default` creada, container muerto, a veces registro.

**La solución:** verificar ANTES de crear nada y cancelar limpio.

- `occupiedHostPortsBy(apps) → map[int]string` — gemela de `occupiedHostPorts` pero
  guardando QUÉ app retiene cada puerto (para nombrarla en el mensaje).
- `detectFixedConflicts(finalBindings, occupiedBy) → []PortConflict` — **genérico**:
  tras el allocator, devuelve los bindings que SIGUEN cayendo sobre un puerto de otra
  app. Como el allocator ya movió todo lo movible, lo que queda ahí es un puerto que
  no se pudo mover (fijo, o pool agotado) → conflicto real. Dedup por puerto host
  (53/tcp + 53/udp → un solo conflicto).
- `portConflictMessage(conflicts)` — mensaje legible.

**Punto de corte (docker_stacks.go):** entre `resolveStackHostPorts` y el
`os.WriteFile(composePath)`. Si hay conflicto → responde **409** estructurado y
`return`. **Cero efectos secundarios**: no se escribe compose, no hay `compose up`,
no se crea red ni container ni registro, ni se sobrescribe un compose previo.

**Respuesta 409** (forma de objeto que el wrapper `unwrap` del frontend ya entiende):
```json
{ "error": { "code": "port_in_use",
             "message": "El puerto 53 ya está en uso por la app «pihole». Solo una app puede usar ese puerto a la vez.",
             "details": { "conflicts": [ { "port": 53, "held_by": "pihole" } ] } } }
```

**Frontend (InstallFlow.svelte):** al recibir `code === "port_in_use"`, muestra el
mensaje claro (sin el ruido técnico de label/code/status). Explica el motivo, sin
orden imperativa de "desinstala el otro".

**Es genérico:** no busca el 53 en concreto. El día que se añada correo, un segundo
DLNA, etc., cualquier choque de puerto fijo salta solo, sin tocar este código.

---

## El badge de puerto efectivo · `AppStoreDetail.svelte`

El detalle del AppStore mostraba el puerto del catálogo en dos sitios. Se separó:
- **Badge superior** (junto a Docker · Activa) → `badgePort`: si la app está
  instalada, el puerto **efectivo/registrado** (`launchInfo.localPort` =
  `docker_apps.port`, el que decidió el allocator). Si no, el del catálogo.
  Ej: Transmission muestra `:30000`.
- **"Puerto" de info técnica** → sigue mostrando `view.catalog.port` (el default del
  contenedor, ej. `:9091`). Intencionado: es la referencia del puerto nativo de la imagen.

---

## Invariantes y decisiones

- **Regla 16 (DISCIPLINE):** la realidad externa manda. La ocupación se lee del estado
  real (PortsJSON), no de hipótesis.
- **El `:53` (y los blandos) no se mueven.** Cambiar el puerto de un DNS lo hace
  inalcanzable (las máquinas hardcodean el 53). Es un límite del protocolo, no del motor.
- **Sticky-pero-reusable:** una app conserva su puerto entre reinstalaciones, pero al
  desinstalarse el puerto vuelve al pool.
- **No-bloqueante por construcción:** ante la duda, el allocator se omite y se usa el
  puerto declarado. El preflight es la única parada dura, y solo ante conflicto real.

---

## Validación en hierro (Raspberry Pi ARM64)

| App | Resultado |
|-----|-----------|
| Transmission | web 9091 (NimTorrent-reservado) → **host 30000**, peers 51413 intactos. `Up`. |
| qBittorrent | web 8081 conservado (libre), peer 6881 (NimTorrent) → pool. `Up`. |
| nginx | antes fallaba por puerto → ahora instala solo. `Up`. |
| AdGuard | setup 3000 → **30002**, web 80 → **8083**, `:53` intacto (correcto). `Up`. |
| Preflight DNS | con Pi-hole en :53, instalar AdGuard → **mensaje limpio, cero basura**. ✅ |

---

## Roadmap / pendientes (NO son deuda de estas fases)

### A · Alineación puerto↔env (Flavor A) — DISEÑADO, sin implementar
Cuando el host de un binding es `${VAR}` (p.ej. Minecraft `'${MC_PORT}:25565'`), el
allocator hoy lo SALTA — y el preflight también → **agujero**: si `MC_PORT` choca,
vuelve el error feo de docker.

**Diseño (auto-descriptivo, sin tocar catálogo):** el allocator detecta `${VAR}` en
la posición host, extrae el nombre de la var, resuelve su valor del `.env`, lo pasa
por el allocator (preferido→pool si choca) y **escribe el resultado de vuelta en el
`.env`**. El `${VAR}` del compose ES la declaración. Reusa la máquina de `.env`
(patrón idéntico a `runtimeIdentity`, que inyecta el UID en una env var declarada).

**Apps afectadas (catálogo real):** Minecraft (`MC_PORT`); futuros game servers
(Valheim, Project Zomboid, Rust) seguirán el mismo patrón → valor estratégico.
También qBittorrent `WEBUI_PORT` (igualar si el 8081 se reasigna; dispara poco).

**Corrección honesta:** el puerto de *peers* de qBittorrent en la imagen linuxserver
**no tiene env** (se configura en su `.conf`), así que el seeding óptimo **no es
resoluble por env**. Solo aplica a host-ports `${VAR}` y a `WEBUI_PORT`.

### B · Nivel 2 / IP-Allocator (macvlan) — INVESTIGADO, sin implementar
Para correr DOS DNS a la vez (Pi-hole **y** AdGuard). El eje es la IP, no el puerto:
cada DNS en su propia IP, los dos en :53. La forma universal (Synology, OMV, Unraid)
es **macvlan** (cada container su IP). Pega: limitación de kernel (el host no llega
al container macvlan) → necesita un "shim" + servicio de arranque (efímero tras
reboot). Ningún sistema lo hace con cero-config: el admin define la IP/interfaz
(la LAN no es de NimOS). Feature de red seria, futura.

### C · Flag `fixed` por-app en el catálogo — refinamiento menor
Que la app declare su puerto fijo (`purpose:"dns"` / `fixed:true`) en vez de la lista
global `reservedSoft()`. Más robusto (cubre fijos app-específicos), encaja con
`configFields`. Para cuando haga falta.

### D · Fase 5 cosmética — opcional, valor bajo
Aviso PRE-clic en el modal ("este puerto se moverá al 30000") informativo. La mitad
importante (conflicto DNS) ya la cubre el preflight. Solo queda lo informativo.

### E · Credenciales de qBittorrent — cruza con backlog "primer admin"
qbit imprime una password temporal en logs (`docker logs qbittorrent | grep -i
"temporary password"`). Surface postInstall (A) o modal + hash PBKDF2 (B).

---

## Ficheros del subsistema

| Fichero | Rol |
|---------|-----|
| `port_reserved.go` (+ test) | Reservados (hard/soft), ocupación, helpers |
| `port_allocator.go` (+ test) | `allocatePort` (sticky→preferido→pool, fijo/flotante) |
| `port_rewrite.go` (+ test) | Reescritor del puerto host en el compose |
| `port_bindings.go` (+ test) | Parseo + `resolveStackHostPorts` (multi-binding) |
| `port_preflight.go` (+ test) | `occupiedHostPortsBy`, `detectFixedConflicts`, mensaje |
| `docker_stacks.go` | Wiring del allocator + corte preflight (409) |
| `AppStoreDetail.svelte` | Badge de puerto efectivo |
| `InstallFlow.svelte` | Mensaje claro del 409 (port_in_use) |
