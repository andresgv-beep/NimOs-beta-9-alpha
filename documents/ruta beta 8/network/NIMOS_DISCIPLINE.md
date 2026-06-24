# NIMOS DISCIPLINE — Cuándo NO aplicar un patrón

**Tipo**: Documento de principios arquitectónicos
**Audiencia**: Andrés + Claude (co-developer)
**Estado**: VIVO · actualizar cuando aparezcan nuevos casos
**Versión**: v2 (21/05/2026) — refinamientos tras discusión Network v3→v4

**Cambios v1 → v2**:
- Reconciler tiers: **separar TIER de INTERVAL** (estaban acoplados, era el bug del v3)
- CircuitBreaker: explicitar política de persistencia mínima (lazy + state-only)
- SystemCapabilities: refresh **on-demand**, no polling periódico
- Nueva sección 8: HealthStatus pertenece al servicio, no a la máquina de estado interna

---

## CONTEXTO

Durante la sesión del 19/05/2026, NimOS pasó de "código que funciona"
a "código con arquitectura coherente". Se introdujeron 9 patrones
transversales:

```
1. HealthStatus enum unificado
2. Triple Generation (Desired/Observed/Applied)
3. SystemCapabilities detection
4. CircuitBreaker para providers externos
5. Snapshots persistidos en SQLite
6. Reconciler con priority tiers
7. Triggered_by + request_id en operations
8. Events con category + event indexados
9. Secrets table con AES-GCM
```

**Estos patrones son buenos. Pero pueden volverse malos si se aplican
sin disciplina.**

Este documento es el ANTÍDOTO al over-engineering.

---

## REGLA FUNDAMENTAL

```
═══════════════════════════════════════════════════════════════
  Un patrón se aplica SOLO si responde a un problema CONCRETO
  en el módulo donde se aplica.
  
  NO se aplica "porque es buena práctica".
  NO se aplica "porque otros módulos lo tienen".
  NO se aplica "por consistencia formal".
═══════════════════════════════════════════════════════════════
```

**El test**: si quitas el patrón, ¿qué problema real aparece?
- Si la respuesta es concreta → aplica el patrón
- Si la respuesta es vaga ("podría haber problemas algún día") → NO aplicar

---

## REGLAS ESPECÍFICAS POR PATRÓN

### 1. Triple Generation (Desired/Observed/Applied)

**APLICAR cuando**:
- Hay convergencia real entre lo declarado y lo observado
- El sistema puede divergir (drift detection es útil)
- Existe un reconciler que aplica el desired al observed

**NO APLICAR cuando**:
- La entidad es read-only (logs, eventos, métricas)
- Es config one-shot (sin reconcile posterior)
- Es estado derivado de otras entidades

**Ejemplos**:
```
✅ Pool BTRFS         (declarado: name+devices, observed: real state)
✅ DDNS               (declarado: IP=X, observed: IP=X? drift posible)
✅ Cert               (declarado: válido hasta Y, observed: válido/expirado)
✅ Puerto daemon      (declarado: 5009, observed: listener:5009?)

❌ Log entry          (append-only, no converge)
❌ Event              (append-only)
❌ Capability         (read-only del sistema)
❌ Health snapshot    (cálculo en runtime, no se "aplica")
❌ Firewall rule estática (existe o no, no converge)
```

### 2. Snapshot Persistido en SQLite

**APLICAR cuando**:
- Necesitas debugging histórico ("¿qué pasaba a las 03:42?")
- Recovery tras crash es importante
- Métricas a largo plazo del estado del módulo

**NO APLICAR cuando**:
- El estado cambia constantemente (sería write storm)
- La info es trivialmente re-computable
- Solo necesitas el último valor

**REGLAS DE FRECUENCIA**:
```
NUNCA snapshot cada scan del observer (write storm en SD).

Patrón correcto:
   · Snapshot completo cada 15 minutos
   · Eventos puntuales entre medias (cambio detectado, error)
   · Retention agresiva:
     - 100 últimos snapshots completos
     - Uno por hora durante último día
     - Uno por día durante último mes
     - Borrar el resto
   
SQLite con WAL mode + sync NORMAL para reducir IO.
```

**Ejemplos**:
```
✅ Network observed   (cambios significativos, debugging útil)
✅ Storage observed   (mismo)

❌ CPU usage          (cambia cada segundo, usa series temporales aparte)
❌ Memory snapshots   (igual)
❌ Lista de procesos  (read on demand)
```

### 3. Circuit Breaker

**APLICAR cuando**:
- El servicio es EXTERNO (no controlas su disponibilidad)
- Falla puede ser persistente (no transitorio)
- Hammer durante fallo causa daño (rate limit, CPU, logs)

**NO APLICAR cuando**:
- El servicio es interno (otro módulo de NimOS)
- Las operaciones son one-shot del usuario
- Ya hay otro mecanismo de protección

**EXCEPCIÓN IMPORTANTE**:
```
El CircuitBreaker NO es específico de Network.
Vive en NIMOS CORE como módulo reutilizable.

Casos esperados de uso:
   · DuckDNS API           (network)
   · Let's Encrypt API     (network)
   · ifconfig.me           (network - public IP detection)
   · AWS S3 / B2           (backup, futuro)
   · Docker Hub            (app store)
   · Pushover/Telegram     (notifications)
   · OpenAI / Anthropic    (AI features, futuro)
   · Metrics endpoints     (telemetry)

Ubicación: /daemon/breaker.go (no /daemon/network_breaker.go)
Tabla:     nimos_breakers   (no network_breakers)
```

**POLÍTICA DE PERSISTENCIA**:
```
Persistir state cross-restart EVITA:
   · Crash loop del daemon → spam de calls al provider externo → ban
   · Pérdida del cooldown tras `systemctl restart`

Pero hay que ser disciplinado para no convertir el breaker en una base
de datos de métricas:

✅ PERSISTIR (mínimo viable):
   · name            (PK)
   · state           (closed/open/half_open)
   · next_retry_at   (para respetar cooldown across restart)

❌ NO PERSISTIR en la tabla breakers:
   · total_calls, total_failures, histograms → eso es MÉTRICAS,
     va en otro sitio (Prometheus, NimMetrics tabla aparte, no aquí)
   · last_failure_at  → derivable de next_retry_at - cooldown
   · failure_reason   → ya está en events

ESCRITURA LAZY:
   · Write a SQLite SOLO al cambiar de state (closed↔open↔half_open).
   · NO write en cada Call() — eso sería write storm.
   · En operación normal, <10 writes/día por breaker.

RECUPERACIÓN AL BOOT:
   · Si state=open y next_retry_at > now → respetar cooldown.
   · Si state=open y next_retry_at < now → arrancar en half_open.
   · Si state=half_open → arrancar en closed (asumimos que durante el
     downtime el problema pudo resolverse).
```

### 4. Events Persistidos

**APLICAR cuando**:
- Necesitas timeline de operaciones
- Auditoría es requisito
- Debugging a posteriori

**NO APLICAR cuando**:
- El evento es trivial (heartbeat, scan exitoso rutinario)
- Ya está en logs del sistema (no duplicar)

**REGLAS ANTI-EXPLOSION**:

```
PROBLEMA: Reconciler cada 15min × 5 reconcilers × 4 eventos/run × 365 días
        = ~700,000 eventos/año
        → SD card sufre, queries lentas, ruido inútil

ANTÍDOTOS OBLIGATORIOS:

A) DEDUPE en runtime:
   · Mismo evento (mismo category+event+target) en últimos N minutos
     → incrementar counter en lugar de crear nuevo
   · Ventana típica: 5 minutos
   
   Ejemplo: ddns_update_succeeded ocurre 96 veces/día
            → 1 evento + counter incrementándose
            → Solo se crea otro al cambiar de día

B) RATE LIMITING por category:
   · Max 10 events/min por category
   · Si excede → drop con counter "events_dropped" en métricas
   
C) AGGREGATION nocturna:
   · Cada noche a las 03:00: comprimir eventos del día anterior
   · Mantener: errors, warns, eventos únicos
   · Resumen: "Reconciler ddns: 96 runs OK, 0 fallidos"
   · Borrar los individuales

D) RETENTION agresiva:
   · level=error: 90 días
   · level=warn:  30 días  
   · level=info:  7 días
   · level=debug: 24h
   
E) NIVELES correctos:
   · "Reconciler started"      → debug (no info!)
   · "DDNS update succeeded"   → debug (rutina)
   · "DDNS IP changed"          → info (cambio real)
   · "DDNS update failed"      → warn
   · "Cert expiring < 7 days"  → warn
   · "Cert expired"             → error
```

### 5. Reconciler Tiers

**APLICAR cuando**:
- Hay varios reconcilers con criticidad distinta
- Recursos limitados (Pi a 700MHz, no puedes correr todos a la vez)
- Hay un scheduler central

**NO APLICAR cuando**:
- Tienes 1-2 reconcilers solo
- Todos tienen misma criticidad

**REGLA DEL NÚMERO MÁGICO**:
```
Máximo 3 tiers (Critical / Medium / Low).
NO 5 ni 7 tiers granulares.

Aprende de systemd: tiene "default.target / multi-user.target /
network-online.target" — pocos niveles, bien distinguidos.

Si necesitas más granularidad → es señal de que algo está mal modelado.
```

**SEPARACIÓN DE TIER vs INTERVAL** (refinamiento 21/05):
```
ERROR del modelo v3: acoplar tier=intervalo.
   v3 decía: Critical=1min, High=5min, Medium=15min, Low=1h.
   Resultado: cada vez que un reconciler quería un intervalo nuevo,
   había que inventar un tier nuevo. Anti-patrón.

MODELO CORRECTO: tier e interval son ortogonales.

TIER (prioridad de scheduling) — 3 valores:
   · Critical: si hay drift detectado por observer, se ejecuta
               INMEDIATO sin esperar al próximo interval.
               Bypassa la cola si está saturada.
               
   · Medium:   default. Se ejecuta en su interval, FIFO con otros
               Medium. Se serializa con el lock global del módulo.
               
   · Low:      best-effort. Se posterga si hay contention.
               Eventos generados → level=debug, no info.

INTERVAL (cadencia) — campo libre del reconciler, en segundos:
   · cert_renewer:      Critical, interval=60s
   · port_listener:     Critical, interval=60s
   · ddns_updater:      Medium,   interval=900s   (15min)
   · upnp_refresh:      Low,      interval=3600s  (1h)
   · capability_detect: Low,      interval=86400s (24h) o on-demand
   
   Storage (futuro):
   · pool_health:       Medium,   interval=300s   (5min)
   · scrub_scheduler:   Low,      interval=21600s (6h)
```

**EJEMPLO**: un reconciler Medium con interval=300s (5min) es
perfectamente válido. No necesita un tier "High" inventado. Su tier dice
"no es crítico, se serializa normal", su interval dice "cada 5 min".

**TIPS DE IMPLEMENTACIÓN**:
```
type NamedReconciler struct {
    Name        string
    Tier        ReconcilerTier  // Critical / Medium / Low
    Interval    time.Duration   // cadencia normal
    Reconciler  Reconciler
    
    // Si tier es Critical y observer detecta drift,
    // ejecutar AHORA sin esperar interval.
    ForceOnDrift bool
}
```

### 6. HealthStatus Unificado

**APLICAR siempre** — este es de los pocos patrones que SÍ se aplica
universalmente.

**REGLA**:
```
6 estados es el máximo:
   healthy, degraded, failed, partial, unknown, stale

NUNCA añadir más estados sin discusión arquitectónica formal.

El test: ¿puede un usuario distinguir "degraded" de "partial"
de "stale"? Si la respuesta es ambigua, eliminar uno.
```

### 7. SystemCapabilities

**APLICAR para cosas opcionales**:
- Tools externas (certbot, openssl, btrfs, etc.)
- Features del kernel (overlayfs, namespaces, etc.)
- Hardware específico (UPS, GPU, etc.)

**NO APLICAR para**:
- Cosas obligatorias (NimOS NO funciona sin ellas → fail al boot)
- Detección runtime que cambia frecuente

**REGLA**:
```
Capability detection es para "feature está disponible?"
NO para "feature está activado?"

Ejemplo:
   ✅ CertbotInstalled (estática, sí o no)
   ❌ CertbotIsRunningRightNow (dinámica, usar observer)
```

**REFRESH ON-DEMAND, NO POLLING** (refinamiento 21/05):
```
ERROR común: meter un goroutine que refresca capabilities cada
N minutos "por si acaso". Eso es polling activo para un evento
que ocurre 0-1 veces al día.

`apt install certbot` no pasa cada 15 minutos.

POLÍTICA CORRECTA:
   1. Detect al boot → guardar en `nimos_capabilities` tabla.
   2. Refresh LAZY cuando el frontend lo pide:
      · GET /api/network/capabilities
      · Si last_detected_at > 1h → refrescar antes de responder.
      · Si < 1h → devolver cache.
   3. Refresh EXPLÍCITO en endpoints relacionados:
      · Antes de iniciar emisión de cert → refrescar (por si user
        acaba de instalar certbot y quiere usarlo ya).
   4. Si quieres una "garantía" de freshness con goroutine:
      · capability_detect → tier Low, interval=86400s (24h).
      · No menos.

Esto es coherente con el principio de NimOS:
   · Hardware modesto (Pi 4/5).
   · CPU es un recurso finito.
   · Polling activo solo cuando hay evidencia de que es necesario.
```

### 8. HealthStatus es del SERVICIO, no de la máquina de estado interna

**REGLA**:
```
Una máquina de estado interna (CircuitBreaker, ReconcilerScheduler,
PortListener, etc.) tiene STATE, no HEALTH.

El SERVICIO que la máquina sirve tiene HEALTH.
HealthStatus es la traducción del state interno al lenguaje de UI/usuario.
```

**EJEMPLO**:
```
CircuitBreaker.State = closed | open | half_open  (su MOTOR interno)

Servicio DuckDNS que usa el breaker:
   breaker.closed     → DuckDNS health: healthy
   breaker.half_open  → DuckDNS health: degraded ("recuperándose")
   breaker.open       → DuckDNS health: failed   ("provider caído")

La traducción la hace el OBSERVER del servicio.
NO la expone el breaker directamente como "Health".
```

**POR QUÉ IMPORTA**:
```
Si el breaker expusiera HealthStatus directamente, mezclamos
abstracciones:
   · Un breaker es un mecanismo de protección (interna).
   · Un Health es información para el usuario (externa).
   · Si los acoplas, no puedes reutilizar el breaker en contextos
     donde su state no se traduce 1:1 a health.

Ejemplo donde la traducción NO es 1:1:
   · Breaker de Pushover (notificaciones).
   · Si está open → la notificación no se entrega, PERO el sistema
     en general está sano. El "health del servicio NimOS" no se ve
     afectado. Solo el subsistema notifications es degraded.
```

**APLICACIÓN PRÁCTICA**:
```
✅ breaker.go         → expone GetState() CircuitState
✅ network_observer   → traduce a HealthStatus por cada servicio
✅ UI                 → muestra HealthStatus, NO CircuitState

❌ breaker.go         → expone GetHealth() HealthStatus
   (acopla protección con presentación)
```

---

## ANTI-PATTERNS A EVITAR

### 1. "Abstracción anticipada"

```
❌ MAL:
   Crear interface ReconcilerProvider con 10 métodos
   "porque algún día tendremos múltiples implementaciones"
   
✅ BIEN:
   Función concreta hasta que aparezca el SEGUNDO caso
   Entonces extraer interface CON los 2 casos reales en mente
```

### 2. "Patrón global por consistencia"

```
❌ MAL:
   "Storage tiene observer, NimBackup también debería tenerlo
    aunque no haya divergencia real"
   
✅ BIEN:
   "NimBackup tiene divergencia (declared schedule vs observed runs)
    → SÍ aplica observer"
```

### 3. "Snapshot por si acaso"

```
❌ MAL:
   "Vamos a snapshot cada minuto por si necesitamos debuggear"
   
✅ BIEN:
   "Snapshot cuando hay cambio real, retention agresiva"
```

### 4. "Event para cada cosa"

```
❌ MAL:
   "Cada función importante debería emitir un evento"
   
✅ BIEN:
   "Eventos son para acciones AUDITABLE. Debug usa logs."
```

### 5. "Tres niveles por tener tres niveles"

```
❌ MAL:
   "Critical, High, Medium, Low, Background, Idle..."
   
✅ BIEN:
   "Critical (no puede esperar), Medium (default), Low (best effort)"
```

---

## CHECKLIST AL AÑADIR UN PATRÓN NUEVO

Antes de meter un patrón en NimOS, contestar:

```
[ ] ¿Resuelve un problema REAL y CONCRETO?
[ ] ¿Puedo dar 3 ejemplos donde se aplica?
[ ] ¿Puedo dar 3 ejemplos donde NO se aplica?
[ ] ¿Sé cuál es el coste (mental + código)?
[ ] ¿Es reversible si me equivoco?
[ ] ¿Es comprensible para un dev nuevo en 30 minutos?
[ ] ¿Tiene tests que documenten su uso correcto?
[ ] ¿Está documentado en NIMOS_DISCIPLINE.md?

Si alguna respuesta es NO → NO añadir todavía.
```

---

## REVISIÓN PERIÓDICA

Cada 3 meses (o al cerrar una Beta importante):

```
1. Listar TODOS los usos de cada patrón
2. Para cada uso, validar contra "APLICAR cuando" del patrón
3. Si hay usos dudosos, decidir:
   · Mantener si tiene justificación
   · Refactor a algo más simple si no
4. Documentar la decisión

NimOS no es Kubernetes. Es un NAS doméstico bien hecho.
La diferencia es CRÍTICA.
```

---

## CONTRA-EJEMPLOS DE LA INDUSTRIA

Aprende de fallos públicos:

```
KUBERNETES (1.0 → 1.28):
   · 50+ CRDs core
   · Operators dentro de operators
   · Complejidad legendaria
   · → Necesitas un equipo dedicado solo para operarlo
   
PROMETHEUS:
   · Excelente para servers
   · Para sistemas pequeños = overkill brutal
   · Bytes por métrica innecesarios
   
SYSTEMD (los memes):
   · Inicialmente "init system simple"
   · Hoy: 50+ binarios, 100k+ líneas
   · Funciona, pero entender qué hace requiere un mes
```

**NimOS NO debe ser ninguno de estos.**

```
OBJETIVO de NimOS:
   · NAS doméstico que un dev puede entender en 1 día
   · Self-hosted sin dependencias cloud
   · Mantenible por 1-2 personas
   · Extensible sin reescribir
   · Robusto en hardware modesto (Pi 4/5)
```

Si NimOS empieza a parecerse a Kubernetes mini → STOP. Algo se nos fue
de las manos. Volver a este documento.

---

## INSPIRACIONES CORRECTAS

```
SQLite:
   · Una sola idea (BD local), bien ejecutada
   · API mínima, comportamiento predecible
   · Sin features que no necesitas
   
Nginx (clásico):
   · Patrón maestro/workers simple
   · Config legible
   · Sin XML, sin DSL nuevo
   
Caddy:
   · "Just works" para 95% de casos
   · Complejidad opcional, no obligatoria
   
Redis (1.0):
   · 5 estructuras de datos, no 50
   · Comandos simples, composables
```

**NimOS aspira a esto.**

---

## CRÉDITOS

```
Este documento nace de una crítica final de Andrés (19/05/2026 noche):

"Ojo con esto. Porque reconciler, convergence, breakers, observed
 snapshots, generations, scheduler tiers, todo eso escala MUY bien...
 pero también multiplica complejidad mental.
 
 Mi consejo: mantén SIEMPRE pocos estados, pocos tiers, pocos
 abstractions. No conviertas NimOS en un framework."

Esta disciplina es probablemente más valiosa que los 9 patrones
juntos. Sin esto, NimOS se convierte en un framework imposible
de evolucionar.

Con esto, NimOS sigue siendo un producto comprensible y mantenible.
```

**Refinamientos v2 (21/05/2026)**:

```
Tras discusión Network v3 → v4, se identificaron 4 puntos de
disciplina que el v1 no cubría con suficiente claridad:

1. TIER ≠ INTERVAL.
   El v3 acoplaba ambos (Critical=1min, High=5min, ...).
   Cada nuevo intervalo deseado forzaba un nuevo tier.
   v2 separa: TIER es prioridad (3 valores), INTERVAL es libre.

2. PERSISTENCIA DEL BREAKER.
   v1 mencionaba "ubicación /daemon/breaker.go" pero no qué
   persistir. v2 explicita: solo state + next_retry_at, lazy write.

3. CAPABILITIES REFRESH.
   v1 decía "estática". v2 explicita: detect al boot + refresh
   on-demand (lazy), NUNCA polling periódico activo.

4. HEALTHSTATUS vs STATE.
   Nueva sección 8: HealthStatus pertenece al SERVICIO, no a la
   máquina de estado interna. El breaker tiene CircuitState,
   el servicio que envuelve tiene HealthStatus. La traducción
   la hace el observer del servicio.

Estos refinamientos son producto de discutir antes de codear.
Es la disciplina aplicándose a sí misma.
```

---

## REVISIÓN DEL PLAN NETWORK v4 BAJO ESTA DISCIPLINA

Aplicando este documento al plan v4 (tras refinamientos 21/05):

```
✅ Triple Generation: SOLO en DDNS, Certs, Ports (3 entities).
   NO en eventos, logs, capabilities. Correcto.

✅ Snapshot persistido: 15min snapshot + eventos puntuales.
   Retention: 100 últimos / 1h último día / 1d último mes.
   SQLite WAL + sync NORMAL.

✅ CircuitBreaker: en /daemon/breaker.go (global, core).
   Tabla nimos_breakers (no network_breakers).
   Persistencia mínima: name + state + next_retry_at.
   Lazy write: solo al cambiar de state.

✅ Events: dedupe (5min) + rate limit (10/min/category) +
   aggregation nocturna (03:00) + retention por nivel.

✅ HealthStatus: 6 estados. Pertenece al servicio,
   NO a la máquina de estado interna del breaker.

✅ Capabilities: detect al boot + refresh on-demand (lazy).
   NO polling periódico activo.

✅ Reconciler: 3 tiers (Critical/Medium/Low).
   TIER ≠ INTERVAL — son ortogonales, interval libre por reconciler.

✅ Triggered_by + request_id: aplicar solo donde correlation importa
   (operations table).

✅ networkMu: lock global del módulo para serializar reconcilers
   en operaciones write (como storageMu).
```

→ Network Plan v4 incorpora TODAS estas correcciones de disciplina.
