# NimShield Intelligence — Red de inteligencia firmada para NimOS

**Documento de concepto · v2 · Beta 8.1 · borrador para discusión**

> El objetivo: que NimShield tenga **defensas siempre actualizadas** sin fricción para el usuario. Una red de inteligencia que tú mantienes y firmas, que cada NimOS del mundo se baja solo cada 2 días y verifica criptográficamente. Hoy distribuye IPs maliciosas; mañana, sin tocar el protocolo, distribuye dominios, ASN, IOCs, hashes, reglas o CVEs críticas. Repo público para leer, imposible de falsificar.

> **v2** — Incorpora cinco refuerzos sobre el concepto inicial: (1) feed genérico, no solo blocklist; (2) manifest firmado como índice, no firma por fichero; (3) doble versionado (contenido + esquema); (4) rollback con varias versiones; (5) modo observación como fase de primera clase.

---

## 1. El problema que resuelve

NimShield hoy es **reactivo**: detecta al atacante cuando ya está tocando la puerta (honeypots, fuerza bruta, escaneo). El siguiente nivel es **proactivo**: conocer las amenazas que ya golpean a otros y neutralizarlas *antes* de que lleguen.

Para eso NimShield necesita inteligencia actualizada. Pero hay una trampa de experiencia de usuario que NO queremos:

```
❌ Pedir a cada usuario un token de un servicio externo → barrera horrible.
   Nadie quiere registrarse en un tercero para que su NAS funcione.
```

La solución correcta (modelo pi-hole / antivirus): **inteligencia mantenida que se distribuye firmada**. El usuario no configura nada; consume tu feed. Tú tienes UN token (el tuyo, en la fábrica) para generarlo; los usuarios cero.

### Principios de diseño

1. **Cero fricción** — el usuario no se registra en nada ni configura tokens.
2. **Genérico** — el protocolo distribuye *ficheros*, no "una lista". El contenido puede crecer sin cambiar el transporte.
3. **Pública para leer, imposible de falsificar** — repo público + firma ed25519.
4. **Funciona offline** — feed base embebido; el refresco es una mejora, no un requisito.
5. **Cero dependencias nuevas** — `net/http` + `crypto/ed25519` + SQLite, todo stdlib de Go.
6. **Respeta Regla 16** — la inteligencia la poseen sus fuentes; NimOS la consume. Si la fuente no responde, NimShield sigue con la última versión buena.
7. **Separación de responsabilidades** — quien firma (clave privada) vive apartado del NimOS de producción (en primera línea, expuesto).
8. **Seguro contra el propio error** — la firma protege del atacante; el rollback y el modo observación protegen de un fallo humano en la generación.

---

## 2. Arquitectura en dos lados

```
┌─────────────────────────────┐         ┌──────────────────────────────┐
│   LA FÁBRICA (Pi Zero 2 W)  │         │   CADA NimOS (consumidor)    │
│   máquina aislada, dedicada │         │                              │
│                             │         │   · feed base EMBEBIDO       │
│   1. descarga fuentes       │  push   │   · cada 2 días: baja el     │
│   2. agrega + dedup + limpia│ ──────► │     manifest + ficheros      │
│   3. genera el MANIFEST     │  GitHub │   · VERIFICA firma del        │
│      (índice + hashes)      │ público │     manifest (ed25519)        │
│   4. FIRMA el manifest      │ ◄────── │   · valida hash de c/fichero  │
│   5. publica en GitHub      │  pull   │   · MODO OBSERVACIÓN → o →    │
│                             │         │     bloqueo duro              │
│   clave PRIVADA vive AQUÍ   │         │   · conserva N versiones      │
└─────────────────────────────┘         │     (rollback)               │
         clave PRIVADA                   └──────────────────────────────┘
                                              clave PÚBLICA embebida
```

La clave: **público no significa vulnerable**. GitHub da HTTPS (nadie altera en tránsito) y la firma garantiza que aunque comprometan el repo o suplanten la URL, ningún NimOS acepta un feed sin tu firma.

---

## 3. El feed es genérico — distribuye ficheros, no "una lista"

El cambio conceptual clave: NimShield Intelligence no distribuye *una blocklist*, distribuye un **conjunto de ficheros indexados por un manifest**. El protocolo (descargar → verificar → aplicar) es idéntico sea cual sea el contenido.

```
nimshield-intelligence/
├── manifest.json              # el índice firmado (ver §4)
├── manifest.sig               # firma ed25519 del manifest
├── blocklist_ipv4.txt         # IPs/CIDR maliciosas IPv4
├── blocklist_ipv6.txt         # IPs/CIDR maliciosas IPv6
├── compromised_domains.txt    # dominios comprometidos (futuro)
├── malicious_asn.txt          # ASN maliciosos (futuro)
├── geo_exceptions.json        # excepciones geográficas (futuro)
└── signatures.json            # firmas / IOCs / reglas (futuro)
```

```
HOY:     blocklist_ipv4 + blocklist_ipv6
MAÑANA:  + dominios + ASN + IOCs + hashes + CVEs críticas + reglas

EL PROTOCOLO NO CAMBIA. SOLO CAMBIA EL CONTENIDO DEL MANIFEST.
```

NimShield aplica cada tipo de fichero según su categoría declarada en el manifest; los tipos que un NimOS dado no entiende, los ignora con gracia (gracias al versionado de esquema, §6).

---

## 4. El manifest firmado — un índice, no N firmas

En vez de firmar cada fichero por separado (N firmas que gestionar), se firma **un solo manifest** que contiene el hash SHA-256 de cada fichero. Cadena de confianza limpia:

```
firma válida del manifest → los hashes son auténticos → cada fichero íntegro
```

Es el patrón de los repos de Debian (`Release` + `Release.gpg` + hashes de `Packages`) y **el mismo del formato `.nimpkg`**. Convergen en un solo modelo.

```jsonc
{
  "schema_version": 1,                    // versión del FORMATO del manifest (§6)
  "feed_version": 42,                      // versión del CONTENIDO (§6)
  "generated_at": "2026-06-26T03:00:00Z",
  "expires_at": "2026-07-03T03:00:00Z",    // caducidad blanda (anti-replay viejo)
  "files": [
    {
      "name": "blocklist_ipv4.txt",
      "type": "blocklist_ip",              // categoría → cómo lo aplica NimShield
      "sha256": "a3f5…",
      "entries": 14203,
      "action": "block"                    // block | observe | enrich
    },
    {
      "name": "blocklist_ipv6.txt",
      "type": "blocklist_ip",
      "sha256": "9b21…",
      "entries": 880,
      "action": "block"
    }
    // mañana: compromised_domains.txt, malicious_asn.txt, signatures.json…
  ]
}
```

Solo `manifest.sig` firma el `manifest.json`. Verificado el manifest, cada fichero se valida por su `sha256`. Una firma, todos los ficheros cubiertos.

---

## 5. La firma — por qué y cómo (ed25519)

Analogía: un sello de lacre. Solo tú tienes el anillo (clave privada). Cualquiera ve la carta y el sello, pero nadie puede falsificarlo. Quien la recibe sabe que la hiciste tú (autenticidad) y que nadie la tocó (integridad).

```
Par de claves ed25519 (Go lo trae en crypto/ed25519, cero dependencias):
  · CLAVE PRIVADA → secreta, vive solo en la fábrica (Pi Zero). Jamás sale.
  · CLAVE PÚBLICA → 32 bytes, embebida como constante en NimOS. Pública.

FIRMAR (la fábrica):    firma = sign(manifest.json, clave_privada)
VERIFICAR (cada NimOS): verify(manifest.json, firma, clave_publica) → SÍ/NO
```

La clave pública **verifica** firmas pero **no puede crear**las. Aunque todo el mundo tenga la pública, solo tú firmas manifests válidos.

> ⚠️ **La clave privada es la joya de la corona.** Vive en la fábrica aislada, nunca en el repo ni en el código. Su rotación se contempla en §6.

---

## 6. Doble versionado — contenido y esquema separados

Dos versiones distintas que conviene NO mezclar:

```
feed_version (versión del CONTENIDO): 42 → 43 → 44…
  · Es "qué publicación es esta". Monótona creciente.
  · NimShield rechaza aplicar un feed con feed_version MENOR que el que ya
    tiene (anti-rollback malicioso: nadie te re-sirve una lista vieja).

schema_version (versión del FORMATO del manifest): 1 → 2 → 3…
  · Es "cómo está estructurado el manifest".
  · Un NimOS sabe leer hasta el schema que conoce. Si llega uno mayor,
    aplica lo que entiende e ignora con gracia lo nuevo (o avisa de que
    conviene actualizar NimOS). Nunca se rompe.
```

Esto permite evolucionar el formato (meter campos, tipos de fichero nuevos) **sin romper instalaciones antiguas**. El NimOS de hace seis meses sigue consumiendo IPs aunque el feed nuevo traiga ASN que él no sabe usar.

### Rotación de clave (caso futuro)

Para el día que haya que cambiar la clave de firma: NimOS puede embeber **varias claves públicas válidas** (la actual + la siguiente) durante una ventana de transición. Se firma con la nueva, los NimOS que ya tienen ambas la aceptan, y los que no, se actualizan. Transición suave sin cortar a nadie.

---

## 7. Rollback — varias versiones conservadas

La firma protege contra el atacante. Pero un feed **válidamente firmado pero roto por error humano** (un `0.0.0.0/0` colado que bloquearía el mundo) pasa la verificación criptográfica y sería veneno. Defensa: conservar varias versiones.

```
NimShield guarda, en su caché SQLite:
  current      → el feed activo
  previous     → el anterior
  previous-2   → el de antes

Si tras aplicar `current` se detecta algo raro (pico de bloqueos anómalo,
una whitelist propia bloqueada, etc.) → rollback INMEDIATO a `previous`.
```

Salvaguardas automáticas que disparan rollback o cuarentena:
- El feed bloquearía una IP que está en la **whitelist** del usuario.
- El feed contiene rangos absurdamente amplios (p.ej. un `/8` público, `0.0.0.0/0`).
- El nº de entradas cae o crece de forma desproporcionada respecto a `previous` (heurística anti-corrupción).

La firma te protege de los demás; el rollback te protege **de ti mismo**.

---

## 8. Modo observación — la salvaguarda anti-falso-positivo

La feature más importante para no repetir el dolor de los autobloqueos. Antes de bloquear en duro, NimShield **observa**:

```
FASE OBSERVACIÓN (configurable, p.ej. los primeros N días de un feed nuevo):
   IP entrante coincide con el ThreatFeed
        ↓
   NO bloquear
        ↓
   registrar el match (evento "observed", sin acción)
        ↓
   acumular estadísticas: cuántos matches, contra qué IPs, ¿alguna era
   tráfico legítimo del usuario?

CUANDO LAS ESTADÍSTICAS DAN CONFIANZA (manual o automático por umbral):
        ↓
   activar bloqueo en duro
```

Beneficios:
- Mides el impacto real del feed en *tu* tráfico antes de que muerda.
- Detectas si una fuente mete falsos positivos sin que afecte a nadie.
- El `action` por fichero en el manifest (`observe` vs `block`) permite que NimShield reciba un fichero ya marcado como "solo observar" desde la fábrica.

Esto, combinado con whitelist + reputación con prioridad, hace casi imposible que el feed bloquee al usuario legítimo.

---

## 9. La fábrica — Pi Zero 2 W dedicada

### Por qué una máquina dedicada y aislada

```
✅ Propósito único → superficie de ataque minúscula. Solo genera+firma+publica.
✅ La clave privada vive en la máquina MENOS expuesta — NO en el NimOS de
   producción (en primera línea con dominio público). Si comprometen el NAS
   expuesto, NO se llevan la llave de firma de TODO el ecosistema.
✅ Si la fábrica peta, los NimOS siguen con la última versión buena.
```

> **Esta es la decisión de diseño más valiosa del sistema.** Pensar en el peor caso —"¿y si comprometen un NAS?"— y que la respuesta sea: no puede publicar feeds falsos, no puede firmar `.nimpkg`, no compromete a otros usuarios. El radio de daño está contenido por diseño. Es el modelo de las infraestructuras maduras.

### Por qué software dedicado y no n8n

```
La Pi Zero 2 W es modesta (512MB RAM). n8n es pesado → iría justo, y para un
pipeline lineal de 5 pasos programado es matar moscas a cañonazos.

DECISIÓN: software dedicado, ligero, monitoreable localmente. Un daemon de
propósito único con panel local (LAN, sin exponer): última ejecución, nº de
entradas por fichero, fuentes activas, errores, log de publicaciones, y la
feed_version actual.
```

### El pipeline de la fábrica

```
[Scheduler · cada 2 días]
   ↓
[Descarga] fuentes públicas (blocklist.de, FireHOL, AbuseIPDB con TU token…)
   ↓
[Agregación] unir + deduplicar + limpiar + validar + sanity-checks
   ↓
[Genera ficheros] blocklist_ipv4.txt, blocklist_ipv6.txt, …
   ↓
[Genera MANIFEST] índice + sha256 de cada fichero + feed_version++
   ↓
[FIRMA] manifest.sig = sign(manifest.json, clave_privada)
   ↓
[Publica] git commit + push al repo público
   ↓
[Panel local] registra la ejecución
```

El token de fuentes (AbuseIPDB, etc.) es **uno solo, el tuyo**, vive en la fábrica. Los usuarios nunca lo ven.

---

## 10. El consumidor — NimShield en cada NimOS

```
1. ARRANQUE: carga el feed base EMBEBIDO → protege desde el segundo cero,
   sin internet.

2. REFRESCO (cada 2 días, configurable):
   · GET manifest.json + manifest.sig (HTTPS), con límite de tamaño
   · VERIFICA firma del manifest con la clave pública embebida
   · ¿feed_version > la actual? (si no, descarta: es más viejo)
   · ¿schema_version conocida? (si mayor, aplica lo que entiende)
   · descarga los ficheros del manifest, valida cada sha256
   · rota versiones: current → previous → previous-2
   · aplica según `action` (block / observe / enrich) y categoría
   · ¿repo no responde / firma falla / sanity falla? → conserva la actual

3. EN CALIENTE (hot path):
   · ¿la IP/dominio/ASN entrante está en el feed? → acción según modo
     (observar o bloquear), SIEMPRE respetando whitelist y reputación.
```

### Decisiones de diseño (cerradas)

```
· Acción por defecto de una entrada "block" → bloqueo duro, pero whitelist
  y reputación tienen PRIORIDAD sobre el feed.
· ¿Permanente? → NO. Techo de duración, coherente con NimShield.
· El feed añade una capa; las reglas duras (inyección, honeypots, traversal)
  siguen siendo innegociables e independientes.
· Feed nuevo → entra en MODO OBSERVACIÓN antes de bloquear en duro.
```

---

## 11. Seguridad — guardarraíles

| Riesgo | Mitigación |
|---|---|
| Feed falso / repo comprometido | Firma ed25519 del manifest verificada antes de aplicar |
| Fichero corrupto en tránsito | sha256 por fichero, dentro del manifest firmado |
| Re-servir un feed viejo | `feed_version` monótona; se rechaza si es menor |
| Cambio de formato rompe NimOS viejos | `schema_version`; lo desconocido se ignora con gracia |
| Feed firmado pero roto (error humano) | Rollback a `previous`; sanity-checks; cuarentena |
| Falsos positivos | Modo observación + whitelist/reputación con prioridad |
| Fuente caída / repo no responde | Fallback a la versión actual; feed base embebido |
| Clave privada comprometida | Vive en la fábrica aislada; rotación con claves múltiples |
| Token de fuente expuesto | Vive solo en la fábrica; usuarios cero |
| NAS comprometido afecta a otros | Imposible: no tiene la clave privada. Radio de daño contenido |

---

## 12. Cero dependencias nuevas

```
Fábrica:     net/http + crypto/ed25519 + crypto/sha256 + git + scheduler.
Consumidor:  net/http + crypto/ed25519 + crypto/sha256 + SQLite.
```

Todo stdlib de Go (salvo git en la fábrica) y todo lo que NimOS ya usa. Mismo patrón que el provider de DuckDNS. Nada de terceros que auditar, nada distinto en ARM64.

---

## 13. Plan por fases (propuesto)

```
FASE 0 · Ladrillo de firma (ed25519)  ← COMPARTIDO con .nimpkg
   · gen-keys, firmar manifest, verificar. Base de TODO.

FASE 1 · La fábrica (sin UI)
   · Descarga + agregación + ficheros + manifest + firma + push.

FASE 2 · El consumidor en NimShield (MODO OBSERVACIÓN)  ← valor real
   · Feed base embebido + descarga + verificación + rollback + observación.
   · Arranca SOLO observando: mide impacto sin bloquear.

FASE 3 · Activación del bloqueo + panel de la fábrica
   · Pasar de observar a bloquear con confianza + cara bonita de monitoreo.

FASE 4 · Tipos nuevos (dominios, ASN, IOCs…)
   · El protocolo ya los soporta; solo se añaden ficheros al manifest.
```

> Orden deliberado: el consumidor llega **en modo observación** (Fase 2), y el bloqueo en duro se activa aparte (Fase 3) cuando los datos dan confianza. Nunca se muerde a ciegas.

---

## 14. Relación con el formato .nimpkg

La **Fase 0 (firma ed25519) es el ladrillo común**. Y con v2 convergen aún más: ambos usan **manifest firmado + checksums SHA-256**. Es literalmente el mismo modelo de confianza aplicado a dos cosas:

```
NimShield Intelligence → "este feed lo hizo NimOS y nadie lo tocó"
.nimpkg                → "este paquete lo hizo NimOS y nadie lo tocó"
```

Construir el módulo de firma + verificación de manifest una vez sirve para los dos proyectos.

---

## 15. Preguntas abiertas (para decidir)

1. **Fuentes + umbral**: ¿qué listas y con qué confianza? Demasiado agresivo → falsos positivos; demasiado blando → poco valor. (El modo observación mitiga el riesgo de calibrarlo.)
2. **Repo**: ¿dedicado `nimos/nimshield-intelligence` o carpeta en el repo de NimOS?
3. **Frecuencia**: 2 días fijo o configurable por el usuario.
4. **Tope de tamaño**: máximo de entradas para no inflar la RAM del NAS (relevante en Pi de usuario).
5. **Ventana de observación**: ¿cuántos días por defecto antes de proponer el paso a bloqueo? ¿Automática por umbral o siempre manual?
6. **Identidad del servicio**: nombre cerrado **NimShield Intelligence** (evita "Network" para no chocar con el módulo Network de NimOS).

La #1 sigue siendo la que más afecta a la calidad real — y ahora el **modo observación** es justo la herramienta para calibrarla sin riesgo.
```
