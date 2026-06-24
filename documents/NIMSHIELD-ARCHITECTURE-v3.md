# NimShield — Architecture & Design Document
## NimOS Integrated Security Module
### Version 3.0 — April 2026

---

## Changelog v2 → v3

- **Nuevo**: Correlation Engine (Pieza 2 de la evolución) — eventos correlacionados como campañas, no incidentes aislados
- **Nuevo**: Deception Layer (Honeypots) — endpoints falsos que detectan atacantes con 100% fiabilidad
- **Nuevo**: Adaptive Response Engine — respuestas distintas según velocity, persistence, confidence
- **Nuevo**: Sección CSP Compensation — NimShield compensa las limitaciones de CSP con `unsafe-inline` en SvelteKit
- **Nuevo**: Stateful Scoring — velocity + persistence + confidence, no suma plana
- **Nuevo**: Security Headers ya implementados en Beta 7 (CSP, X-Content-Type-Options, X-Frame-Options, Referrer-Policy)
- **Nuevo**: Server-side prefs injection (implementado en Beta 7 — elimina flash de defaults)
- **Actualizado**: Fases renumeradas para Beta 7+ (antes era Beta 5.x)
- **Actualizado**: Métricas de éxito refinadas
- **Actualizado**: Modelo de amenazas con XSS/CSP bypass y ataques distribuidos
- **Eliminado**: ML/AI references — sustituido por estadística simple (media, desviación estándar, contadores)
- **Eliminado**: eBPF como obligatorio en Lockdown — ahora es opcional en todas las fases

---

## 1. Visión

NimShield es el módulo de seguridad activa de NimOS. No es un firewall estático ni un antivirus — es un sistema de defensa en profundidad que opera en múltiples capas: kernel, red, aplicación y contenedores. Detecta, clasifica, reacciona y aprende.

Filosofía: un NAS expuesto a internet debe comportarse como una fortaleza con múltiples anillos de defensa. Si un anillo falla, el siguiente lo contiene. NimShield no confía en ninguna capa individual.

### 1.1 Principios de Diseño

- **Defense in Depth**: Mínimo 3 capas entre un atacante e internet y los datos del usuario
- **Zero-dependency core**: El motor de reglas y bloqueo funciona sin software externo. Las capas avanzadas (seccomp, AppArmor) se activan si el kernel lo soporta
- **Opt-in granular**: Cada función se activa/desactiva independientemente
- **Cero falsos positivos destructivos**: NimShield nunca puede bloquear al admin legítimo sin mecanismo de recuperación
- **Observable**: Todo lo que NimShield hace es visible, explicable y reversible
- **Adaptativo**: No solo reglas fijas — aprende el baseline de tráfico normal y alerta anomalías
- **Fail-open safe**: Si NimShield crashea, el daemon sigue funcionando. Seguridad degradada, no servicio muerto
- **No-ML**: Estadística simple (medias, desviaciones, contadores). Sin modelos de ML ni dependencias pesadas. Un Raspberry Pi con 4GB debe poder correr NimShield sin impacto

### 1.2 Modelo de Amenazas (actualizado)

| Escenario | Probabilidad | Impacto | Capa de defensa |
|-----------|-------------|---------|-----------------|
| Brute force SSH/HTTP | Alta | Medio | L3: Rate limit + auto-block |
| Vulnerability scanner | Alta | Bajo | L3: UA detect + throttle + honeypots |
| Path traversal | Media | Alto | L3: Input validation + block |
| SQL/Command injection | Media | Crítico | L3: Pattern detect + block |
| XSS via inline script | Media | Alto | L3: Request inspection (compensa CSP unsafe-inline) |
| Port scanning | Media | Bajo | L2: nftables rate limit |
| Container escape | Baja | Crítico | L1: seccomp + AppArmor + L2: network isolation |
| Credential stuffing | Media | Alto | L3: Multi-user detect + GeoIP + correlation |
| Slow-rate attack | Baja | Alto | L3: Correlation Engine + SlowRateTracker |
| Stolen token replay | Baja | Alto | L3: Session binding + anomaly |
| Distributed attack (multi-IP) | Baja | Alto | L3: Correlation Engine pattern matching |
| Honeypot probe | Media | Bajo | L3: Deception Layer → instant block |
| Insider / compromised admin | Muy baja | Crítico | L3: Audit trail + change detection |

### 1.3 CSP Compensation (nuevo)

NimOS usa SvelteKit como SPA, lo cual obliga a `script-src 'self' 'unsafe-inline'` en CSP. Trusted Types son incompatibles con SvelteKit (usa `innerHTML` internamente). NimShield compensa estas limitaciones:

| Limitación CSP | Compensación NimShield |
|---|---|
| `unsafe-inline` permite inline scripts maliciosos | INJ-003 detecta XSS payloads en requests ANTES de que lleguen al SPA |
| Sin Trusted Types para bloquear sinks DOM | Behavioral baseline detecta exfiltración post-XSS (fetches anómalos) |
| CSP no detecta ataques lentos | Correlation Engine + SlowRateTracker |
| CSP es estático (declarativo) | NimShield es dinámico (reactivo, adaptativo) |

Headers de seguridad ya implementados en Beta 7 (static.go):
```
Content-Security-Policy: default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; img-src 'self' data: blob:; connect-src 'self'; font-src 'self' https://fonts.gstatic.com; frame-src 'self'; frame-ancestors 'self'; object-src 'none'; base-uri 'self'
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
Referrer-Policy: no-referrer
Cache-Control: no-store, no-cache, must-revalidate (para HTML con prefs inyectadas)
```

Futuro (cuando SvelteKit soporte nonces): migrar a `script-src 'self' 'nonce-{random}'` y eliminar `unsafe-inline`.

---

## 2. Arquitectura — Defense in Depth

NimShield opera en 3 capas concurrentes. Un ataque debe superar TODAS las capas para tener éxito.

```
═══════════════════════════════════════════════════════════
                    INTERNET / LAN
═══════════════════════════════════════════════════════════
                         │
                         ▼
┌─────────────────────────────────────────────────────────┐
│              LAYER 1 — KERNEL HARDENING                 │
│                                                         │
│  ┌─────────┐  ┌──────────┐  ┌────────┐                │
│  │ sysctl  │  │ seccomp  │  │AppArmor│                │
│  │ params  │  │ profiles │  │profiles│                │
│  │         │  │ per-svc  │  │per-svc │                │
│  └─────────┘  └──────────┘  └────────┘                │
│  • SYN flood protection    • Container sandboxing      │
│  • ICMP hardening          • Syscall whitelist          │
│  • Shared memory protect   • File access control        │
│  • ASLR enforced           • Network namespace          │
└───────────────────────────┬─────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────┐
│              LAYER 2 — NETWORK FIREWALL                 │
│                                                         │
│  ┌─────────────────────────────────────────────────┐   │
│  │              nftables (primary)                   │   │
│  │  ┌──────────┐  ┌──────────┐  ┌───────────────┐  │   │
│  │  │ nimshield│  │ nimshield│  │  nimshield    │  │   │
│  │  │ _input   │  │ _forward │  │  _ratelimit   │  │   │
│  │  │ (allow/  │  │ (docker  │  │  (per-IP      │  │   │
│  │  │  deny)   │  │  egress) │  │   throttle)   │  │   │
│  │  └──────────┘  └──────────┘  └───────────────┘  │   │
│  └─────────────────────────────────────────────────┘   │
│  • Per-IP connection limits    • SYN proxy              │
│  • Container egress control    • GeoIP pre-filter       │
│  • Port knocking (optional)    • Rate limit per-subnet  │
│  UFW compatibility: nftables backend, ufw as alias      │
└───────────────────────────┬─────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────┐
│              LAYER 3 — APPLICATION SHIELD                │
│                                                         │
│  ┌──────────────────────────────────────────────────┐  │
│  │                 nimos-daemon                       │  │
│  │                                                    │  │
│  │  ┌────────────────────────────────────────────┐   │  │
│  │  │           NimShield Engine v3                │   │  │
│  │  │                                              │   │  │
│  │  │  Collector → Correlation → Scoring → Reactor │   │  │
│  │  │       │          │            │          │   │   │  │
│  │  │    events    campaigns    stateful    adaptive│   │  │
│  │  │    + honeypots           scoring     response │   │  │
│  │  │                                              │   │  │
│  │  │  ┌────────────────────────────────────────┐  │   │  │
│  │  │  │         Deception Layer                 │  │   │  │
│  │  │  │  /.env  /wp-login  /phpmyadmin  /debug  │  │   │  │
│  │  │  └────────────────────────────────────────┘  │   │  │
│  │  └────────────────────────────────────────────┘   │  │
│  │                                                    │  │
│  │  ┌──────┐ ┌──────┐ ┌───────┐ ┌────────┐ ┌─────┐ │  │
│  │  │ Auth │ │Files │ │Docker │ │Network │ │ ... │ │  │
│  │  └──────┘ └──────┘ └───────┘ └────────┘ └─────┘ │  │
│  └──────────────────────────────────────────────────┘  │
│                                                         │
│  ┌──────────────────────────────────────────────────┐  │
│  │              SQLite (nimos.db)                     │  │
│  │  shield_events │ shield_blocks │ shield_baseline  │  │
│  │  shield_campaigns │ shield_honeypots              │  │
│  └──────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────┘
```

---

## 3. Layer 1 — Kernel Hardening

### 3.1 Sysctl Hardening

Fichero: `/etc/sysctl.d/99-nimshield.conf`

NimShield genera y aplica este fichero al activarse:

```ini
# ── Network stack hardening ──
net.ipv4.tcp_syncookies = 1
net.ipv4.tcp_max_syn_backlog = 4096
net.ipv4.tcp_synack_retries = 2
net.ipv4.conf.all.rp_filter = 1
net.ipv4.conf.default.rp_filter = 1
net.ipv4.conf.all.accept_redirects = 0
net.ipv4.conf.default.accept_redirects = 0
net.ipv4.conf.all.send_redirects = 0
net.ipv6.conf.all.accept_redirects = 0
net.ipv4.conf.all.accept_source_route = 0
net.ipv6.conf.all.accept_source_route = 0
net.ipv4.conf.all.log_martians = 1
net.ipv4.icmp_echo_ignore_broadcasts = 1

# ── Memory protection ──
fs.suid_dumpable = 0
kernel.randomize_va_space = 2
kernel.kptr_restrict = 2
kernel.dmesg_restrict = 1
kernel.yama.ptrace_scope = 2

# ── File system ──
fs.protected_hardlinks = 1
fs.protected_symlinks = 1
fs.protected_fifos = 2
fs.protected_regular = 2
```

```go
func applyKernelHardening() error {
    params := map[string]string{
        "net.ipv4.tcp_syncookies": "1",
        // ... all params above
    }
    for key, val := range params {
        path := "/proc/sys/" + strings.ReplaceAll(key, ".", "/")
        if err := os.WriteFile(path, []byte(val), 0644); err != nil {
            logMsg("shield: sysctl %s failed: %v", key, err)
        }
    }
    return nil
}
```

### 3.2 Seccomp Profiles

Per-service seccomp profiles that whitelist only needed syscalls:

```json
{
  "defaultAction": "SCMP_ACT_ERRNO",
  "architectures": ["SCMP_ARCH_X86_64", "SCMP_ARCH_AARCH64"],
  "syscalls": [
    {"names": ["read","write","open","close","stat","fstat","mmap","mprotect","munmap","brk","ioctl","access","pipe","select","sched_yield","mremap","msync","clone","fork","vfork","execve","exit","wait4","kill","getpid","sendto","recvfrom","socket","connect","accept","bind","listen","getsockname","getpeername","socketpair","setsockopt","getsockopt","shutdown","futex","epoll_create","epoll_ctl","epoll_wait","openat","newfstatat","getrandom"],
     "action": "SCMP_ACT_ALLOW"}
  ]
}
```

### 3.3 AppArmor Profiles (si disponible)

Generados per-service. Restringen acceso a ficheros y capacidades:

```
profile nimos-daemon /opt/nimos/daemon/nimos-daemon {
  /opt/nimos/** r,
  /var/lib/nimos/config/** rw,
  /nimos/pools/** rw,
  /proc/sys/** r,
  deny /etc/shadow r,
  deny /root/** rw,
}
```

---

## 4. Layer 2 — Network Firewall (nftables)

### 4.1 Por qué nftables

UFW no tiene rate limiting per-IP nativo, no puede filtrar por GeoIP, no tiene contadores atómicos. NimShield usa nftables directamente. UFW sigue funcionando en paralelo.

### 4.2 Estructura nftables

```nft
table inet nimshield {
    set blocked_ips {
        type ipv4_addr
        flags timeout
    }

    set whitelisted_ips {
        type ipv4_addr
        elements = { 127.0.0.1 }
    }

    set ratelimit_ips {
        type ipv4_addr
        flags dynamic,timeout
    }

    set geo_blocked {
        type ipv4_addr
        flags interval
    }

    chain input {
        type filter hook input priority -10; policy accept;
        ip saddr @whitelisted_ips accept
        ip saddr @blocked_ips drop
        ip saddr @geo_blocked drop
        tcp dport 5000 ct state new meter nimshield_ratelimit \
            { ip saddr limit rate over 30/minute burst 10 packets } \
            add @ratelimit_ips { ip saddr timeout 300s } drop
        tcp flags syn limit rate 100/second burst 50 accept
        tcp flags syn drop
        ct state invalid drop
    }

    chain forward {
        type filter hook forward priority 0; policy accept;
        iifname "docker*" ip daddr 127.0.0.1 drop
        iifname "docker*" ip daddr { 192.168.0.0/16, 10.0.0.0/8, 172.16.0.0/12 } \
            tcp dport { 22, 5000 } drop
    }

    chain log_and_drop {
        log prefix "nimshield_drop: " group 1
        drop
    }
}
```

### 4.3 Sincronización daemon ↔ nftables

```go
func nftBlockIP(ip string, duration time.Duration) error {
    secs := int(duration.Seconds())
    _, ok := runSafe("nft", "add", "element", "inet", "nimshield", "blocked_ips",
        fmt.Sprintf("{ %s timeout %ds }", ip, secs))
    if !ok {
        return ufwBlockIP(ip)
    }
    return nil
}

func nftUnblockIP(ip string) error {
    _, ok := runSafe("nft", "delete", "element", "inet", "nimshield", "blocked_ips",
        fmt.Sprintf("{ %s }", ip))
    if !ok {
        return ufwUnblockIP(ip)
    }
    return nil
}
```

### 4.4 Fallback chain

Si nftables no disponible → iptables → solo bloqueo a nivel aplicación (L3).

### 4.5 Container Network Policies

| Política | Descripción | Containers |
|----------|-------------|------------|
| **full** | Sin restricciones de red | Explícitamente seleccionado por admin |
| **standard** | HTTP/HTTPS saliente, no LAN | Default |
| **isolated** | Solo otros containers del mismo stack | Databases, Redis |
| **none** | Sin red | Herramientas offline |

---

## 5. Layer 3 — Application Shield

### 5.1 Collector

```go
var shieldEvents = make(chan ShieldEvent, 2000)

type ShieldEvent struct {
    Timestamp time.Time
    Category  string // auth, traversal, injection, scan, docker, system, honeypot
    Severity  string // low, medium, high, critical
    SourceIP  string
    UserAgent string
    Endpoint  string
    Username  string
    Method    string
    Status    int
    Details   map[string]interface{}
}
```

### 5.2 Deception Layer (NUEVO)

Endpoints honeypot que no existen en NimOS. Cualquier request a estos paths es 100% malicioso — zero falsos positivos.

```go
var honeypotPaths = map[string]string{
    "/.env":                    "HONEY-001: dotenv probe",
    "/.git/config":             "HONEY-002: git config probe",
    "/wp-login.php":            "HONEY-003: WordPress probe",
    "/wp-admin":                "HONEY-004: WordPress admin probe",
    "/phpmyadmin":              "HONEY-005: phpMyAdmin probe",
    "/admin":                   "HONEY-006: admin panel probe",
    "/api/admin/debug":         "HONEY-007: debug endpoint probe",
    "/config.json":             "HONEY-008: config probe",
    "/api/v1/exec":             "HONEY-009: exec endpoint probe",
    "/shell":                   "HONEY-010: shell probe",
    "/console":                 "HONEY-011: console probe",
    "/actuator":                "HONEY-012: Spring actuator probe",
    "/server-status":           "HONEY-013: Apache status probe",
    "/.well-known/security.txt": "HONEY-014: security.txt probe",
    "/xmlrpc.php":              "HONEY-015: XML-RPC probe",
}

func checkHoneypot(r *http.Request) bool {
    path := strings.ToLower(r.URL.Path)
    if reason, isHoneypot := honeypotPaths[path]; isHoneypot {
        shieldEvents <- ShieldEvent{
            Timestamp: time.Now(),
            Category:  "honeypot",
            Severity:  "critical",
            SourceIP:  clientIP(r),
            UserAgent: r.UserAgent(),
            Endpoint:  r.URL.Path,
            Details:   map[string]interface{}{"rule": reason},
        }
        // Instant block — no scoring needed, 100% malicious
        blockIP(clientIP(r), 24*time.Hour, reason)
        return true
    }
    return false
}
```

El middleware ejecuta `checkHoneypot()` ANTES de cualquier routing.

### 5.3 Correlation Engine (NUEVO)

No analiza eventos individuales — correlaciona eventos de la misma IP en el tiempo para detectar campañas.

```go
type ThreatContext struct {
    IP            string
    Events        []ShieldEvent
    FirstSeen     time.Time
    LastSeen      time.Time
    Categories    map[string]int  // category → count
    TotalEvents   int
    AttackPattern string          // "scan", "brute_force", "injection_campaign", "mixed"
    Confidence    float64         // 0.0 - 1.0
}

// Sliding window: 2 hours
var threatContexts = map[string]*ThreatContext{} // IP → context
var threatMu sync.Mutex

func correlateEvent(event ShieldEvent) *ThreatContext {
    threatMu.Lock()
    defer threatMu.Unlock()

    ctx, exists := threatContexts[event.SourceIP]
    if !exists {
        ctx = &ThreatContext{
            IP:         event.SourceIP,
            FirstSeen:  event.Timestamp,
            Categories: map[string]int{},
        }
        threatContexts[event.SourceIP] = ctx
    }

    // Expire old events (sliding window 2h)
    cutoff := time.Now().Add(-2 * time.Hour)
    fresh := []ShieldEvent{}
    for _, e := range ctx.Events {
        if e.Timestamp.After(cutoff) {
            fresh = append(fresh, e)
        }
    }
    ctx.Events = append(fresh, event)
    ctx.LastSeen = event.Timestamp
    ctx.TotalEvents = len(ctx.Events)
    ctx.Categories[event.Category]++

    // Classify attack pattern
    ctx.AttackPattern = classifyPattern(ctx)
    ctx.Confidence = calculateConfidence(ctx)

    return ctx
}

func classifyPattern(ctx *ThreatContext) string {
    if ctx.Categories["auth"] > 3 && len(ctx.Categories) == 1 {
        return "brute_force"
    }
    if ctx.Categories["scan"] > 5 {
        return "reconnaissance"
    }
    if ctx.Categories["injection"] > 0 && ctx.Categories["traversal"] > 0 {
        return "exploitation_campaign"
    }
    if ctx.Categories["honeypot"] > 0 {
        return "automated_scanner"
    }
    if len(ctx.Categories) >= 3 {
        return "multi_vector"
    }
    return "unknown"
}

func calculateConfidence(ctx *ThreatContext) float64 {
    signals := 0.0
    if ctx.TotalEvents > 5 { signals += 0.3 }
    if len(ctx.Categories) > 1 { signals += 0.2 }
    if ctx.Categories["honeypot"] > 0 { signals += 0.4 }  // honeypot = certain
    if ctx.Categories["injection"] > 0 { signals += 0.2 }
    duration := ctx.LastSeen.Sub(ctx.FirstSeen)
    if duration > 10*time.Minute { signals += 0.1 }  // sustained = intentional
    if signals > 1.0 { signals = 1.0 }
    return signals
}
```

### 5.4 Rule Engine (determinístico)

22 reglas (18 originales + 4 nuevas):

| ID | Nombre | Trigger | Acción |
|----|--------|---------|--------|
| `AUTH-001` | Brute Force Login | 5+ login fail / 5min / IP | Block 30min |
| `AUTH-002` | Credential Stuffing | 3+ users fail / 2min / IP | Block 1h |
| `AUTH-003` | Token Spray | 10+ tokens inválidos / 1min / IP | Block 1h |
| `AUTH-004` | 2FA Brute Force | 5+ 2FA fail / 5min / user | Lock user 30min |
| `TRAV-001` | Path Traversal Scan | 3+ traversal / 1min / IP | Block 2h |
| `TRAV-002` | Config File Probe | Intento de leer config files | Block 4h + notify |
| `INJ-001` | SQL Injection | 3+ SQLi / 5min / IP | Block 2h |
| `INJ-002` | Command Injection | Cualquier cmd injection | Block 24h + notify |
| `INJ-003` | XSS Attack | 5+ XSS / 5min / IP | Block 1h |
| `SCAN-001` | Port Scan | 10+ 404s / 1min / IP | Block 30min |
| `SCAN-002` | API Enumeration | 20+ endpoints / 2min / IP | Throttle |
| `SCAN-003` | Vuln Scanner UA | nikto, sqlmap, nmap UA | Block 24h |
| `NET-001` | Geo-Anomaly | Login desde país nuevo | Notify |
| `NET-002` | Tor Exit Node | IP en lista Tor | Configurable |
| `DOCK-001` | Container Escape Attempt | Syscall violation (seccomp) | Kill container + notify |
| `DOCK-002` | Malicious Compose | Host mounts peligrosos | Reject + notify |
| `SYS-001` | Rapid Config Change | 5+ changes / 5min | Notify |
| `SYS-002` | Admin Lockout Risk | Último admin desactivándose | Prevent |
| `HONEY-001` | Honeypot Trigger | Request a endpoint falso | Block 24h instant |
| `CORR-001` | Multi-vector Campaign | 3+ categorías en 1h / IP | Block 4h + notify |
| `CORR-002` | Sustained Reconnaissance | 10+ scan events en 2h / IP | Block 2h |
| `CSP-001` | XSS Payload in Request | `<script>`, `javascript:`, `onerror=` en params/body | Block 1h (compensa CSP) |

### 5.5 Behavioral Baseline (estadística simple, no ML)

```go
type Baseline struct {
    AvgRequestsPerHour    float64
    StdDevRequestsPerHour float64
    AvgUniqueIPsPerDay    int
    StdDevUniqueIPsPerDay float64
    NormalEndpoints       map[string]float64  // endpoint → avg hits/hour
    NormalUserAgents      map[string]bool
    NormalGeoCountries    map[string]bool
    NormalLoginHours      [24]float64
    NormalIPSubnets       map[string]bool     // /24 subnets
    LastUpdated           time.Time
}
```

Se recalcula cada hora con datos de los últimos 7 días rolling.

Detecta anomalías:
- Traffic spike (>3 std dev sobre media)
- Subnet nueva
- User-Agent nunca visto
- Login fuera de horario habitual
- Endpoint con spike de tráfico
- País nuevo

Respuesta para anomalías: no block inmediato. Incrementar sensibilidad de reglas para esa IP (thresholds a la mitad) + notificar al admin.

### 5.6 Stateful Scoring (NUEVO)

No es una suma plana — el score tiene dimensiones:

```go
type ScoreState struct {
    CurrentScore  float64  // 0-100
    Velocity      float64  // qué rápido sube (events/min)
    Persistence   float64  // duración del ataque (minutos)
    Confidence    float64  // certeza de que es ataque (0-1)
}

// Mismo score, respuesta distinta:
// - Alta velocity → ataque agresivo → block inmediato
// - Alta persistence → ataque lento → monitoring + notify
// - Baja confidence → shadow mode (log only, no block)
```

Escalation thresholds:
```
Score 0-20   → Log only
Score 21-40  → Throttle + increased sensitivity
Score 41-70  → Block temporal (L2 nftables + L3 app)
Score 71-90  → Block 24h + session kill
Score 91-100 → Ban permanente + nftables permanent + notify

Decay: -1 punto por hora, mínimo 0
```

### 5.7 Adaptive Response Engine (NUEVO)

```go
func decideAction(ctx *ThreatContext, score ScoreState) Action {
    // High velocity → aggressive attack → instant block
    if score.Velocity > 10 && score.CurrentScore > 40 {
        return Action{Type: "block", Duration: 2 * time.Hour}
    }

    // High persistence, low velocity → slow attack → monitor + notify
    if score.Persistence > 60 && score.Velocity < 2 {
        return Action{Type: "monitor", Notify: true, IncreaseSensitivity: true}
    }

    // Honeypot hit → 100% certain → instant long block
    if ctx.Categories["honeypot"] > 0 {
        return Action{Type: "block", Duration: 24 * time.Hour}
    }

    // Low confidence → shadow mode
    if score.Confidence < 0.3 {
        return Action{Type: "shadow", LogOnly: true}
    }

    // Default escalation based on score
    return defaultEscalation(score.CurrentScore)
}
```

### 5.8 Slow-Rate Attack Detection

```go
type SlowRateTracker struct {
    mu      sync.Mutex
    history map[string]*SlowRateProfile  // IP → profile
}

type SlowRateProfile struct {
    FailedLogins    int
    FirstSeen       time.Time
    LastSeen        time.Time
    TotalDuration   time.Duration
    // 10+ login fails en 24h → block aunque nunca supere 5/5min threshold
}
```

### 5.9 Session Binding

```go
type BoundSession struct {
    Token       string
    Username    string
    BoundIP     string
    BoundUA     string
    Fingerprint string    // Hash(IP + UA + Accept-Language)
}

func validateSessionBinding(session BoundSession, r *http.Request) bool {
    currentFP := hashFingerprint(clientIP(r), r.UserAgent(), r.Header.Get("Accept-Language"))
    if session.Fingerprint != currentFP {
        shieldEvents <- ShieldEvent{
            Category: "auth", Severity: "high",
            SourceIP: clientIP(r), Endpoint: r.URL.Path,
            Details: map[string]interface{}{
                "type": "session_anomaly",
                "original_ip": session.BoundIP,
                "current_ip":  clientIP(r),
            },
        }
        if getShieldMode() >= ModeStrict {
            return false // invalidate session
        }
    }
    return true
}
```

### 5.10 Reactor

```go
func blockIP(ip string, duration time.Duration, reason string) {
    // L3: Application level block (inmediato)
    addToBlocklist(ip, duration, reason)

    // L2: Firewall level block (más profundo)
    firewall.BlockIP(ip, duration)

    // Kill active sessions from this IP
    if getShieldConfig("kill_sessions_on_block") {
        dbSessionsDeleteByIP(ip)
    }

    // Log to DB
    dbShieldBlockInsert(ip, duration, reason)

    // Notify with deduplication
    notifyBlock(ip, duration, reason)
}
```

### 5.11 Notifier

Desktop push (WebSocket), Email (SMTP), Webhook (HTTP POST), Log.

Notification deduplication: misma regla × misma IP × 1 minuto → una notificación con count, no N notificaciones.

---

## 6. Docker Security

### 6.1 Container Launch Hardening

```go
func buildSecureDockerArgs(appId string, policy ContainerPolicy) []string {
    args := []string{
        "--security-opt", "no-new-privileges:true",
        "--cap-drop=ALL",
        "--pids-limit", "256",
        "--memory", policy.MemoryLimit,
        "--cpus", policy.CPULimit,
    }
    if policy.SeccompProfile != "" {
        args = append(args, "--security-opt", "seccomp="+policy.SeccompProfile)
    }
    if policy.ReadOnly {
        args = append(args, "--read-only", "--tmpfs", "/tmp:size=100M")
    }
    for _, cap := range policy.AllowedCaps {
        args = append(args, "--cap-add="+cap)
    }
    if policy.Network == "isolated" {
        args = append(args, "--network=nimshield_isolated")
    }
    if hasAppArmor() {
        args = append(args, "--security-opt", "apparmor=docker-app-"+appId)
    }
    return args
}
```

### 6.2 Compose Sanitization

Pre-deploy analysis que rechaza compose files peligrosos:
- Host volume mounts a /, /etc, /root, /var/lib/nimos, /proc, /sys
- Privileged mode
- Host network mode
- Docker socket mount
- SYS_ADMIN / ALL capabilities

Admin puede forzar override con `force_unsafe: true` (logueado como SYS-003).

### 6.3 Container Runtime Monitoring

Cada 30 segundos:
- CPU > 95% → alert
- Outbound connections a IPs internas en puertos 22, 5000 → alert
- Procesos sospechosos (nc, ncat, nmap, reverse shells) → alert + kill

---

## 7. Modos de Protección

### 7.1 Off
NimShield desactivado. Solo logging básico. L1 y L2 no se tocan.

### 7.2 Normal (default)
- L1: Sysctl hardening
- L2: nftables basic (rate limit, SYN protection)
- L3: Rule engine ON + Honeypots + Correlation. Behavioral baseline learning
- Docker: no-new-privileges, cap-drop
- Notifications: high+ only

### 7.3 Strict
- L1: Sysctl + seccomp profiles + AppArmor (si disponible)
- L2: nftables full (rate limit + GeoIP + container egress control)
- L3: All rules + behavioral anomaly detection + session binding + adaptive response
- Docker: seccomp, read-only rootfs, pids-limit, memory-limit
- Notifications: medium+

### 7.4 Lockdown
- L1: Everything
- L2: nftables deny-all except whitelist
- L3: All sessions killed except current admin. Only whitelisted IPs
- Docker: All containers paused. Admin confirmation to resume
- All remote access disabled except SSH from whitelist
- Recovery: physical access, local console, or `.shield-disable` file in config dir

---

## 8. Auto-Pentest Integrado

```go
func runAutopentest() PentestReport {
    tests := []PentestTest{
        // Network
        {"open_ports", checkOpenPorts},
        {"tls_config", checkTLSConfiguration},
        {"hsts_header", checkHSTSHeader},
        {"csp_header", checkCSPHeader},
        {"security_headers", checkSecurityHeaders},

        // Auth
        {"default_credentials", checkDefaultCredentials},
        {"session_expiry", checkSessionExpiry},
        {"2fa_available", check2FAAvailable},
        {"password_policy", checkPasswordPolicy},
        {"session_binding", checkSessionBinding},

        // Firewall
        {"firewall_active", checkFirewallActive},
        {"nimshield_active", checkNimShieldActive},
        {"ssh_hardened", checkSSHHardened},
        {"honeypots_active", checkHoneypots},

        // Docker
        {"docker_socket_protected", checkDockerSocket},
        {"containers_hardened", checkContainerHardening},
        {"no_privileged_containers", checkNoPrivileged},

        // Kernel
        {"sysctl_hardened", checkSysctlParams},
        {"aslr_enabled", checkASLR},
        {"seccomp_available", checkSeccomp},

        // Data
        {"db_permissions", checkDBPermissions},
        {"config_permissions", checkConfigPermissions},
        {"prefs_injection_safe", checkPrefsInjection},

        // Updates
        {"system_updated", checkSystemUpToDate},
        {"docker_images_updated", checkDockerImagesAge},
    }

    for _, t := range tests {
        result := t.Fn()
        report.Results = append(report.Results, result)
    }
    report.Score = calculateSecurityScore(report.Results)
    return report
}
```

Security Score: 0-100. Instalación default: >70. Hardening completo: 95+.

---

## 9. Implementación por Fases

### Fase 1 — Foundation (Beta 7.1)
Files: `shield.go`, `shield_rules.go`, `shield_http.go`

- Collector (chan ShieldEvent)
- DB tables: shield_events, shield_blocks
- Honeypots (15 endpoints) — instant detection
- Rule Engine: AUTH-001/002, TRAV-001, INJ-001/002/003, SCAN-001/003, HONEY-001, CSP-001
- Middleware: checkHoneypot() + checkBlocked() + instrumentEvent()
- blockIP() con L3 (app-level blocklist)
- API: GET /api/shield/status, GET /api/shield/events, GET /api/shield/blocks, POST /api/shield/unblock
- UI: NimShield dashboard — events feed, blocked IPs, status indicator
- **Entregable**: El NAS detecta honeypots, brute force, scanners y XSS payloads. Bloquea a nivel app.

### Fase 2 — Correlation + Firewall (Beta 7.2)
Files: `shield_correlation.go`, `shield_firewall.go`

- Correlation Engine: ThreatContext, campañas, classifyPattern()
- Stateful Scoring: velocity + persistence + confidence
- Adaptive Response Engine
- nftables integration con fallback (iptables → app-only)
- All 22 rules active
- L2 block sync (nftBlockIP/nftUnblockIP)
- SlowRateTracker (24h accumulator)
- API: GET /api/shield/threats (campañas activas)
- UI: Threat map, campaign visualization
- **Entregable**: Detección de campañas multi-vector y ataques lentos. Defense in depth L2+L3.

### Fase 3 — Intelligence + Docker (Beta 7.3)
Files: `shield_baseline.go`, `shield_docker.go`, `shield_geo.go`

- Behavioral baseline (estadística simple, 7 días rolling)
- Anomaly detection (traffic spike, new subnet, unusual hour)
- Session binding (IP+UA fingerprint)
- GeoIP offline + nftables integration
- Docker: seccomp profiles, compose sanitizer, cap-drop
- Container network policies (standard/isolated/none)
- Container runtime monitoring
- Notifier con deduplication (Email, Webhook, Desktop push)
- Live monitor SSE stream
- **Entregable**: El NAS aprende patrones normales y detecta anomalías. Containers sandboxed.

### Fase 4 — Hardening Total (Beta 7.4)
Files: `shield_pentest.go`, `shield_kernel.go`

- L1: Sysctl hardening automated
- AppArmor profile generation (si disponible)
- Auto-pentest integrado (24 tests)
- Security Score dashboard (0-100)
- Lockdown mode con whitelist + recovery
- Export/import NimShield config
- **Entregable**: Hardened como un bunker. Security Score visible.

---

## 10. Métricas de Éxito

NimShield se considera exitoso cuando:

1. Brute force se bloquea en <10 segundos
2. Port scan se detecta en <30 segundos
3. Honeypot probe se bloquea en <1 segundo
4. Atacante lento (1 req/10min) se detecta en <4 horas
5. Multi-vector campaign se detecta en <30 minutos
6. Container escape attempt se mata en <1 segundo (seccomp)
7. Overhead <2% CPU y <30MB RAM en Raspberry Pi 4
8. Admin bloqueado puede recuperar acceso local en <2 minutos
9. Security Score default >70/100
10. Zero false-positive blocks en uso normal durante 30 días
11. XSS payload en request se bloquea aunque CSP tenga unsafe-inline

---

## 11. Diferenciadores

| Feature | Synology DSM | TrueNAS | NimOS + NimShield |
|---------|-------------|---------|-------------------|
| Rate limit login | ✅ Básico | ❌ | ✅ Per-IP + per-user |
| Auto-block IPs | ✅ fail2ban | ❌ | ✅ nftables + app |
| Honeypots | ❌ | ❌ | ✅ 15 endpoints |
| Correlation engine | ❌ | ❌ | ✅ Campaign detection |
| Adaptive response | ❌ | ❌ | ✅ Velocity/persistence |
| Kernel hardening | Parcial | ❌ | ✅ sysctl + seccomp + AppArmor |
| Network firewall | UFW/iptables | ❌ | ✅ nftables dedicado |
| Container sandboxing | ❌ | N/A | ✅ seccomp + caps + read-only |
| Compose sanitization | ❌ | N/A | ✅ Pre-deploy analysis |
| Anomaly detection | ❌ | ❌ | ✅ Behavioral baseline |
| Slow-rate attack detect | ❌ | ❌ | ✅ 24h accumulator |
| Session binding | ❌ | ❌ | ✅ IP+UA fingerprint |
| Threat scoring | ❌ | ❌ | ✅ Stateful multi-signal |
| GeoIP blocking | ✅ Addon | ❌ | ✅ nftables native |
| Real-time monitor | ❌ | ❌ | ✅ SSE stream |
| Auto-pentest | ❌ | ❌ | ✅ Integrado |
| Security Score | ❌ | ❌ | ✅ 0-100 |
| CSP compensation | N/A | N/A | ✅ Request-level XSS detect |
| Defense layers | 1 (app) | 0-1 | 3 (kernel+net+app) |
| Container net policy | ❌ | N/A | ✅ Per-container |

---

*NimShield v3 — Three walls between the attacker and your data.*
*Now with eyes that see patterns, traps that catch scanners, and responses that adapt.*
