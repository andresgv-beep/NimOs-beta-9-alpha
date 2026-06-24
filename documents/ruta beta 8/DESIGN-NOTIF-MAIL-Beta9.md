# DESIGN — Notificaciones por Email (NOTIF-MAIL)

**Proyecto:** NimOS · Módulo Notifications (transversal)
**Target:** Beta 9
**Estado:** DISEÑO — pendiente de aprobación
**Gobernanza:** DISCIPLINE v2.1
**Fecha:** 2026-06-12
**Dependencias:** STOR-SPACE Fase B (la bocina interna) · nimos_secrets.go
**Fuentes cubiertas:** NimHealth (apps/Docker), Storage (Space Guard + diagnostics),
SMART (hardware.go), Limpieza programada (maintenance), NimShield (seguridad)

---

## 0. Resumen ejecutivo

NimOS ya tiene un embudo de notificaciones in-app (`notifications.go`:
`addNotification` + SQLite + rutas HTTP) por el que pasan TODOS los módulos. Lo que
falta es que esas notificaciones lleguen al usuario **sin abrir la UI** — el último
metro que OMV sí tiene (cron + mail) y que la auditoría vs OMV identificó como el
único punto donde nos ganan.

NOTIF-MAIL añade un **canal de email** que se engancha al embudo existente en un
único punto. Ningún módulo aprende a enviar correos: emiten al embudo como hoy, y un
dispatcher decide qué sale por email según una matriz de enrutado configurable. Nuevo
apartado "Notificaciones" en el Panel de Control.

**Decisión arquitectónica central**: el email es un *canal* del sistema de
notificaciones, no una feature de cada módulo. Un punto de integración, cero
acoplamiento, y extensible a futuros canales (Telegram, webhook, ntfy) sin tocar los
módulos emisores.

---

## 1. Arquitectura

```
 NimHealth ─┐
 Storage   ─┤
 SMART     ─┼──► addNotification() ──► tabla notifications (in-app, como hoy)
 Mainten.  ─┤            │
 NimShield ─┘            ▼
                  NotificationDispatcher          ◄── matriz de enrutado (SQLite)
                  · ¿esta categoría+severidad      ◄── cooldowns / dedupe
                    sale por email?
                  · ¿inmediato o digest?
                         │
                         ▼
                  email_outbox (SQLite) ──► MailSender (goroutine, retry+backoff)
                                                  │
                                                  ▼ SMTP (STARTTLS/TLS)
                                            buzón del usuario
```

Principios:

- **P1 — Un solo chokepoint**: `addNotification` es el único punto donde el email se
  decide. Si un evento no pasa por el embudo, no existe (y eso es un bug del módulo
  emisor, no de este sistema).
- **P2 — Outbox pattern**: ningún envío SMTP inline en el flujo del emisor. Se
  encola en SQLite y un sender en background lo despacha con reintentos. Un SMTP
  caído jamás bloquea ni ralentiza al daemon, y los emails sobreviven a un restart.
- **P3 — El email es para lo importante**: defaults conservadores. Un NAS que manda
  20 correos al día entrena al usuario a ignorarlos — y entonces el correo del disco
  muriéndose muere en la carpeta de "luego lo miro".
- **P4 — Credenciales en secrets**: la password SMTP vive en nimos_secrets, jamás en
  la tabla de configuración ni en logs.

---

## 2. Fuentes y qué emite cada una

El sistema NO inventa eventos: enruta lo que ya entra (o entrará) al embudo. Estado
por fuente y trabajo pendiente para que emitan correctamente:

| Fuente | Estado del embudo | Trabajo pendiente |
|---|---|---|
| **SMART** | ✅ Ya emite por transición (hardware.go:1722+) | Añadir debounce a la alerta de temperatura (notifica cada ciclo de 30 min — fragilidad #2 de la auditoría). Cooldown de 24h por disco. |
| **Storage** | ⚠️ Lo cubre STOR-SPACE Fase B | Nada extra aquí — la Fase B conecta diagnostics (disk_missing, pool_degraded, metadata_pressure_*, scrub con errores) al embudo con dedupe por transición. NOTIF-MAIL las hereda gratis. |
| **NimHealth (apps)** | ⚠️ Detecta pero hay que auditar emisión | Auditar nimhealth_detectors.go / nimhealth_observer.go: cada detector con severidad warning+ debe llamar a addNotification en transición (app caída, contenedor en crash-loop, healthcheck fallando N veces). Patrón SMART. |
| **Limpieza programada** | ⚠️ A verificar en maintenance | El resultado de cada tarea programada (limpieza de archivos, retención) emite: éxito → info (solo in-app), fallo → error (candidato a email). |
| **NimShield** | ✅ notifSecurity existe | Definir qué eventos merecen email (ver matriz §3). Un escaneo de puertos bloqueado NO es un email; 50 IPs bloqueadas en una hora SÍ. NimShield agrega y emite el evento agregado. |

Regla derivada (propuesta DISCIPLINE NOTIF-R1): **los módulos emiten transiciones y
agregados, nunca eventos crudos en bucle**. El dispatcher protege con cooldowns, pero
la primera línea de defensa anti-spam es del emisor.

---

## 3. Matriz de enrutado (el corazón configurable)

Tabla `notification_routing`, editable desde el Panel de Control:

| Categoría | Severidad | Default email | Modo |
|---|---|---|---|
| storage | critical/error | ✅ | inmediato |
| storage | warning | ✅ | digest diario |
| smart | critical/error | ✅ | inmediato |
| smart | warning | ✅ | digest diario |
| apps (nimhealth) | error | ✅ | inmediato |
| apps (nimhealth) | warning | ❌ | — |
| security (nimshield) | security | ✅ | inmediato |
| maintenance | error | ✅ | digest diario |
| maintenance | info/success | ❌ | — |
| system (genérico) | * | ❌ | — |

```sql
CREATE TABLE IF NOT EXISTS notification_routing (
    category   TEXT NOT NULL,   -- storage|smart|apps|security|maintenance|system
    severity   TEXT NOT NULL,   -- info|success|warning|error|security
    email      INTEGER NOT NULL DEFAULT 0,
    mode       TEXT NOT NULL DEFAULT 'immediate',  -- immediate|digest
    PRIMARY KEY (category, severity)
);
```

Nota: hoy `addNotification(ntype, category, ...)` usa category con valores
`notification|system`. Hay que **enriquecer la taxonomía** para que category refleje
el módulo origen (storage, smart, apps, security, maintenance). Cambio pequeño:
los helpers `notifError(...)` ganan variantes con categoría o los emisores pasan a
usar `addNotification` directo con su categoría. Migración: lo existente mapea a
`system`.

---

## 4. Anti-spam: dedupe, cooldown y digest

Tres mecanismos en el dispatcher, por orden:

1. **Cooldown por clave**: clave = `category + hash(title)`. Máximo 1 email por
   clave por hora (configurable). La 2ª ocurrencia dentro de la ventana se cuenta
   pero no envía; el siguiente email de esa clave incluye "(ocurrido N veces desde
   el último aviso)".
2. **Agrupación de ráfaga**: ventana de coalescencia de 2 min. Si en ese margen
   entran 4 notificaciones elegibles para email inmediato, sale **un** correo con
   las 4 — no cuatro correos. (Caso real: un disco que muere dispara SMART critical
   + pool_degraded + apps con IO errors casi simultáneos.)
3. **Digest**: las rutas en modo digest se acumulan y salen en un único correo
   diario a hora configurable (default 08:00), solo si hay contenido. Asunto:
   `[NimOS nimbarraca] Resumen diario: 3 avisos`.

---

## 5. Configuración SMTP y seguridad

### 5.1 Tabla de configuración

```sql
CREATE TABLE IF NOT EXISTS mail_config (
    id              INTEGER PRIMARY KEY CHECK (id = 1),  -- singleton
    enabled         INTEGER NOT NULL DEFAULT 0,
    smtp_host       TEXT NOT NULL DEFAULT '',
    smtp_port       INTEGER NOT NULL DEFAULT 587,
    encryption      TEXT NOT NULL DEFAULT 'starttls',  -- starttls|tls|none
    username        TEXT NOT NULL DEFAULT '',
    -- password: NUNCA aquí. Vive en nimos_secrets con clave 'smtp_password'.
    from_address    TEXT NOT NULL DEFAULT '',
    from_name       TEXT NOT NULL DEFAULT 'NimOS',
    recipients      TEXT NOT NULL DEFAULT '[]',  -- JSON array
    digest_hour     INTEGER NOT NULL DEFAULT 8,
    cooldown_min    INTEGER NOT NULL DEFAULT 60
);
```

### 5.2 Decisiones de seguridad

- Password en `nimos_secrets` (módulo existente), clave `smtp_password`. La API de
  config la acepta en escritura y **jamás** la devuelve en lectura (campo
  `password_set: true/false`).
- `encryption: none` permitido solo con warning explícito en la UI (relays locales);
  default STARTTLS puerto 587.
- El cuerpo de los emails no incluye secretos, tokens ni rutas internas sensibles —
  título, detalle humano, hostname y deep-link a la UI (`https://nimbarraca.duckdns.org/...`).
- Validación de destinatarios (formato) y límite de 10.

### 5.3 Implementación del envío

**Decisión a validar**: stdlib `net/smtp` cubre STARTTLS pero su API está congelada y
es incómoda para TLS implícito (465). Recomendación: `github.com/wneessen/go-mail`
(mantenida, cero dependencias transitivas, soporta starttls/tls/none y context).
Alternativa si se prefiere cero deps: implementación propia sobre `crypto/tls` +
`net/smtp` (~80 líneas, más superficie que mantener). Inclinación: go-mail — es el
tipo de rueda que no aporta nada reinventar.

---

## 6. Outbox y sender

```sql
CREATE TABLE IF NOT EXISTS email_outbox (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at    TEXT NOT NULL,
    subject       TEXT NOT NULL,
    body          TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'pending',  -- pending|sent|failed|dead
    attempts      INTEGER NOT NULL DEFAULT 0,
    last_error    TEXT,
    next_retry_at TEXT,
    sent_at       TEXT
);
```

MailSender: goroutine con ticker de 60s (patrón del scrub scheduler), procesa
`pending` con `next_retry_at <= now`. Backoff exponencial 1m → 5m → 15m → 1h → 6h;
tras 8 intentos → `dead` + notificación in-app "No se pudo enviar el email de
alerta" (la ironía está contemplada: si el email no sale, la UI lo cuenta).
Retención: sent/dead > 30 días se purgan.

Envío síncrono solo en un caso: el **botón "Enviar email de prueba"** del panel
(respuesta inmediata al usuario con el error SMTP literal si falla — es la
herramienta de diagnóstico de la configuración).

---

## 7. Plantillas

v1: **texto plano**, en el idioma de la UI. Estructura fija:

```
Asunto: [NimOS nimbarraca] CRÍTICO · Disco sdb en riesgo de fallo

NimOS ha detectado un problema que requiere tu atención:

  Disco sdb (WD Red 4TB) — SMART indica errores críticos
  (uncorrectable=12). Reemplaza el disco lo antes posible.

  Pool afectado: tank (RAID1) — los datos siguen protegidos
  mientras el segundo disco esté sano.

  Detectado: 2026-06-12 14:32 CEST
  Ver detalles: https://nimbarraca.duckdns.org/health

— NimOS · nimbarraca
```

HTML queda para Beta 10 (cero valor de seguridad, algo de valor estético; el texto
plano pasa todos los filtros de spam mejor). El hostname en el asunto permite a
usuarios con varios NAS filtrar.

---

## 8. Panel de Control — apartado "Notificaciones"

Nueva sección en NimSettings (Panel de Control), siguiendo la modularización de
Beta 8.1:

1. **Canal Email**: toggle global, host/puerto/cifrado/usuario/password
   (write-only), remitente, destinatarios, botón **"Enviar email de prueba"**.
2. **Qué se notifica**: la matriz §3 renderizada como tabla de toggles por
   categoría×severidad con selector inmediato/digest. Botón "Restaurar
   recomendados".
3. **Resumen diario**: hora del digest.
4. **Historial de envíos**: lectura de email_outbox (estado, asunto, fecha,
   error si lo hay). Es el log de auditoría y la herramienta de soporte.

Estética: cubitos 1.5–3px para los toggles de la matriz, card 8–10px por bloque,
JetBrains Mono para el historial. Sin glass.

---

## 9. API HTTP

| Endpoint | Método | Descripción |
|---|---|---|
| `/api/v2/notifications/mail/config` | GET/PUT | config (password write-only, devuelve password_set) |
| `/api/v2/notifications/mail/test` | POST | envío síncrono de prueba, error SMTP literal |
| `/api/v2/notifications/routing` | GET/PUT | matriz completa |
| `/api/v2/notifications/mail/outbox` | GET | historial paginado |

Mutaciones con If-Match/generation (CRIT-1), como todo el stack v2.

---

## 10. Plan de fases

**Fase A — Transporte** *(aislada, testeable sin tocar módulos)*
mail_config + secrets + go-mail (o decisión alternativa) + outbox + MailSender +
endpoint config/test. ✓ Verificación: email de prueba real desde la Pi vía un SMTP
real (Gmail app-password y un relay genérico); SMTP caído → reintentos con backoff
observables en outbox; restart a media cola → la cola sobrevive.

**Fase B — Dispatcher + matriz**
Hook en addNotification + taxonomía de categorías enriquecida + routing + cooldown +
coalescencia. ✓ Verificación: simular ráfaga de 5 notificaciones en 30s → 1 email;
misma clave 3 veces en 1h → 1 email con contador.

**Fase C — Emisores al día**
Auditoría NimHealth (detectores → embudo en transición) + debounce de temperatura
SMART + agregación NimShield + resultados de limpieza programada. ✓ Verificación:
matar un contenedor a mano → email en <2 min; calentar un disco (o mockear) → 1
email/24h, no 48.

**Fase D — Digest + UI completa**
Digest diario + apartado completo en Panel de Control + historial. ✓ Verificación:
soak de 7 días en nimbarraca con la matriz default; criterio de éxito: ≤1 email/día
en operación sana, 0 falsos críticos.

Dependencia cruzada: la Fase C de aquí y la Fase B de STOR-SPACE comparten el
mecanismo de transición/dedupe — implementar STOR-SPACE-B primero y reutilizar.

---

## 11. Reglas para DISCIPLINE (propuestas)

- **NOTIF-R1**: los módulos emiten transiciones y agregados al embudo, nunca eventos
  crudos en bucle. El dispatcher tiene cooldowns, pero son la red, no el trapecio.
- **NOTIF-R2**: ningún módulo envía email directamente. Todo pasa por
  addNotification → dispatcher → outbox.
- **NOTIF-R3**: la password SMTP solo existe en nimos_secrets. Cualquier endpoint
  que la devuelva en lectura es un bug de seguridad P1.
- **NOTIF-R4**: ningún envío SMTP síncrono en flujos del daemon, excepción única:
  el endpoint de prueba.

---

## 12. Touch points

| Archivo | Cambio |
|---|---|
| `notifications.go` | hook de dispatcher en addNotification + taxonomía de categoría |
| `mail_config.go` *(nuevo)* | config + secrets + validación |
| `mail_sender.go` *(nuevo)* | outbox, sender, backoff, plantillas |
| `mail_dispatcher.go` *(nuevo)* | routing, cooldown, coalescencia, digest |
| `nimos_secrets.go` | clave smtp_password (sin cambios de API, solo uso) |
| `nimhealth_detectors.go` / `_observer.go` | emisión en transición (auditoría Fase C) |
| `hardware.go` | debounce temperatura (24h por disco) |
| `shield_rules.go` | evento agregado para email |
| `maintenance_scheduler.go` | emisión de resultado de tareas |
| `boot.go` | arranque del MailSender |
| frontend: `PanelControl/Notificaciones*` *(nuevo)* | sección §8 |

Estimación: A ~1 sesión, B ~1 sesión, C ~1-2 sesiones (la auditoría de NimHealth es
lo elástico), D ~1 sesión + soak.

---

## 13. Alcance negativo

- Sin HTML en v1 (Beta 10).
- Sin canales adicionales (Telegram/webhook/ntfy) en v1 — pero el dispatcher se
  estructura con interfaz de canal para que añadirlos sea implementar la interfaz,
  no reescribir.
- Sin notificaciones push del navegador (es otra bestia: service workers + VAPID).
- Sin servidor de correo propio ni sendmail local: SMTP de terceros configurado por
  el usuario, punto.
