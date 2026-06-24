# Esquema del catálogo de apps · contrato del sistema de config/aprovisionamiento

> Estado: DISEÑO v2 · Paso 1 (el contrato) · 19/06/2026
> Revisado por Andrés (arquitectura a largo plazo). Define qué lee el modal del
> catálogo y cómo lo pasa al backend. Se apoya en campos que YA existen.

> ┌─────────────────────────────────────────────────────────────────────┐
> │ MAPA DE DOCUMENTOS:                                                  │
> │  · ESTE doc (APP-CATALOG-SCHEMA) = el CONTRATO EXACTO · LA REFERENCIA│
> │    al implementar. Es la versión DEFINITIVA del esquema. Si algo     │
> │    difiere de APP-PROVISIONING-DESIGN, MANDA ESTE.                   │
> │  · APP-PROVISIONING-DESIGN.md = el PORQUÉ y la visión (problema,     │
> │    capas 1/2, flujo). Leerlo para ENTENDER; este para IMPLEMENTAR.  │
> │  · ENV-ESCAPE-HALLAZGO.md = el tema del $ en el .env (tests reales): │
> │    solo el $ rompe, se escapa $→$$ SOLO en valores de usuario        │
> │    (no en las ${VAR} internas de NimOS).                            │
> │  · PERMISOS-DESIGN.md = refactor de permisos (independiente, ya hecho)│
> └─────────────────────────────────────────────────────────────────────┘

## Principio rector (lo correcto del diseño)

```
Catálogo = describe QUÉ necesita la app  (Necesito SERVER_NAME, ADMIN_USER...)
NimOS    = decide CÓMO obtenerlo         (DuckDNS, Caddy, generar, IP local...)
```
La app NO sabe de DuckDNS/Cloudflare/Caddy/Tailscale. Solo declara sus necesidades.
NimOS resuelve el resto. La EXPOSICIÓN a internet se decide en Network, no aquí.

## ⭐ DISTINCIÓN CLAVE · datos ANTES vs DESPUÉS de arrancar

La pregunta que decide dónde va cada dato:
```
¿La app NECESITA este dato para ARRANCAR?

  SÍ → configFields (Tipo A) · va al .env ANTES del compose up
       Ej: VS Code PASSWORD (code-server lo lee al iniciar)
           Matrix SERVER_NAME (Synapse lo necesita en su config)

  NO, se aplica con la app ya viva → postInstall (Tipo B) · tras healthy
       Ej: Matrix admin user/pass (se crea con register_new_matrix_user
           DESPUÉS, cuando Synapse ya corre · arranca vacío sin él)
```

Ejemplos que ilustran la diferencia:
```
VS CODE   · PASSWORD en la config para arrancar → configFields (Tipo A)
            · Hoy está HARDCODED ("nimos") · agujero · el usuario debe elegirlo
APP TIPO IMMICH · pide user/pass en SU PROPIA UI al abrirla por primera vez
            · NimOS NO hace nada · ni configFields ni postInstall · se autoconfigura
MATRIX    · SERVER_NAME → configFields (Tipo A, antes de arrancar)
            · admin user/pass → postInstall (Tipo B, después, vía comando)
```

El MISMO modal recoge ambos tipos; NimOS los aplica en momentos distintos
(Tipo A antes de arrancar, Tipo B después de healthy). El usuario solo rellena.

NOTA sobre random_password: NO se usa para passwords de USUARIO (VS Code, admin
Matrix) · el usuario los elige (los va a usar para entrar, como VS Code). Queda
reservado SOLO para secretos INTERNOS puros que el usuario nunca ve (ej. password
app↔BD interna) · caso raro, se tratará si aparece. Adiós a los hardcoded actuales
("nimos"/"admin"/"nimbus"): los reemplaza el password que elige el usuario.

## Campos que YA existen en el catálogo (verificados en código)
```
name, description, icon, category, port, image, official  · metadatos
compose   · el docker-compose (texto)
env       · dict de variables · ej. { "SERVER_NAME": "nimos.local" }
isStack   · bool · true para stacks docker-compose
openMode  · "internal"|"external" = cómo NimOS ABRE la app (iframe/pestaña)
            · NO es exposición · y VA A DESAPARECER · no apoyarse en él
```

## ─────────────────────────────────────────────────────────────
## CONTRATO NUEVO (con los ajustes de la revisión de Andrés)
## ─────────────────────────────────────────────────────────────

### configVersion (int) · OBLIGATORIO · versión del contrato
```json
"configVersion": 1
```
Permite evolucionar el contrato sin romper catálogos viejos. El frontend lee la
versión y sabe interpretar la estructura. Sin esto → hacks futuros. Si algún día
el contrato cambia → configVersion: 2, y el frontend maneja ambas.

### minVersion (string) · OPCIONAL · versión mínima de NimOS requerida
```json
"minVersion": "8.2"
```
Compatibilidad de CAPACIDADES. Si una app usa features que no existen en NimOS
viejas (postInstall, managed folders, uid allocator, permisos v2...), declara la
versión mínima. NimOS compara con la suya:
   · NimOS < minVersion → BLOQUEA la instalación con mensaje claro
     ("Esta app necesita NimOS 8.2 o superior")
   · NimOS >= minVersion → instala normal
Evita instalar apps que fallarían de formas raras por features ausentes.
Como minSdkVersion en Android.

### configApp · ELIMINADO (se infiere)
```
NO existe flag configApp. Se deriva:
   if (configFields?.length > 0 || postInstall?.length > 0) → abrir modal
   si no → instalar directo (apps simples, comportamiento actual)
Un flag menos que mantener / desincronizar.
```

### configFields (array) · campos que pide el modal · Tipo A (variables → env)
```json
"configFields": [
  {
    "key": "SERVER_NAME",            // nombre de la variable de entorno
    "label": "Nombre del servidor",
    "type": "text",                  // text | password | number | toggle | select
    "required": false,
    "default": "",
    "immutable": true,               // editable al INSTALAR, solo-lectura DESPUÉS
    "reconfigure": false,            // true → editable DESPUÉS de instalar (Beta 9+)
    "secret": false,                 // true → no loguear, no devolver por API, cifrar
    "mono": true,                    // (opcional) fuente monospace (dominios, tokens)
    "placeholder": "matrix.midominio.duckdns.org:444",
    "hint": "No se puede cambiar después de instalar.",
    "options": [],                   // solo type:select → ["a","b","c"]

    "auto": {                        // (opcional) NimOS rellena el campo solo
      "provider": "domain"           // ver providers abajo
    },

    "validation": {                  // (opcional) reglas que el catálogo expresa
      "type": "hostname"             // hostname | email | url | port | none
      // o bien:  "regex": "^[a-z0-9.-]+$"
    }
  }
]
```

#### type (presentación del campo)
```
text     · input de texto          number   · numérico
password · oculto con •••           toggle   · sí/no
select   · lista (usa options[])
```

#### secret (comportamiento, INDEPENDIENTE de type)
```
secret: true → el backend:
   · NO lo escribe en logs
   · NO lo devuelve por API (write-only)
   · lo guarda cifrado (o no lo persiste · ver postInstall)
   · la UI lo oculta
NOTA: type:password es presentación; secret:true es comportamiento. Un campo
puede ser type:text + secret:true (token visible al teclear, no persistible).
NimShield se beneficia de esta marca.
```

#### immutable (ciclo de vida)
```
immutable: true → editable SOLO en la instalación; después solo-lectura.
Para variables que NO se pueden cambiar tras crear el servicio
(ej. Matrix SERVER_NAME · cambiarlo rompe Synapse). El contrato se protege.
```

#### reconfigure (ciclo de vida · pensado para Beta 9+)
```
reconfigure: true → este campo se puede EDITAR después de instalar, sin reinstalar.
Para datos que cambian con el tiempo:
   · SMTP de una app        · API key (rotarla)
   · URL externa            · ajustes que evolucionan
NO hace falta implementarlo ahora (Beta 9), pero el contrato ya lo describe ·
así el catálogo declara la intención sin esperar a la implementación.

REGLA de coexistencia con immutable (no ambiguo):
   · immutable GANA SIEMPRE. Si immutable:true → reconfigure se ignora.
   · immutable:true  + reconfigure:true → contradicción → vale immutable (solo-lectura)
   · Lo ideal: validar el catálogo y rechazar esa combinación como error.
   · Sin ninguno de los dos → se pone al instalar, no se edita normalmente.
```

#### auto · providers (NimOS resuelve el valor)
```
auto es un OBJETO extensible: { "provider": "...", ...params }

Providers iniciales:
   { "provider": "domain" }       → subdominio(app) + dominio DDNS + puerto
                                     ej. matrix.midominio.duckdns.org:444
   { "provider": "domain_root" }  → solo el dominio base (sin subdominio)
   { "provider": "local_ip" }     → IP local del NAS + puerto · 192.168.1.131:8008
   { "provider": "hostname" }     → hostname del NAS

Providers futuros (el objeto lo permite sin romper):
   { "provider": "random_password", "length": 32 }  → genera secreto
   { "provider": "uid" }                             → UID asignado (refactor permisos)

⚠ CUESTIÓN ABIERTA · random_password: si NimOS genera el secreto, hay que
diseñar su ciclo de vida (¿dónde se muestra al usuario? ¿se guarda cifrado?
¿cómo lo recupera?). El contrato lo soporta; la implementación concreta de ese
provider tendrá su diseño de seguridad cuando toque.
```

#### validation (reglas que el catálogo expresa)
```
{ "validation": { "type": "hostname" } }   → tipos predefinidos
{ "validation": { "regex": "^...$" } }      → regex a medida
El modal valida ANTES de instalar · evita instalaciones que fallan por dato malo.
Tipos: hostname | email | url | port | (regex)
```

### postInstall (array) · Tipo B · acciones tras arrancar (Capa 2)
```json
"postInstall": [
  {
    "id": "create_admin",            // OBLIGATORIO · identidad para tracking
    "type": "exec",                  // exec dentro del container
    "waitFor": "healthy",            // espera healthcheck antes de ejecutar
    "container": "matrix_synapse",
    "command": "register_new_matrix_user -c /data/homeserver.yaml -a -u {{ADMIN_USER}} -p {{ADMIN_PASS}} http://localhost:8008",
    "idempotent": true,              // no falla si ya existe (reinstalación)
    "fields": [                      // campos extra que el modal pide para esto
      { "key": "ADMIN_USER", "label": "Usuario admin", "type": "text", "required": true,
        "validation": { "type": "none" } },
      { "key": "ADMIN_PASS", "label": "Contraseña admin", "type": "password",
        "required": true, "secret": true }
    ]
  }
]
```

#### type · exec es SOLO UNO de los tipos posibles (puerta abierta)
```
HOY se implementa: "exec" (comando dentro del container)

El campo type está pensado para CRECER. Tipos previstos a futuro:
   exec            · comando dentro del container (command)        ← HOY
   http            · llamada HTTP a la API de la app (url/method/body)
   sql             · ejecutar SQL · seed inicial de BD (query)
   generate_secret · generar un secreto interno
   create_user     · alto nivel · NimOS sabe crear users en apps conocidas

Cada type tendrá sus PROPIOS campos (exec usa command; http usaría url+method+
body; sql usaría query). El contrato lo permite sin romper · el frontend/backend
hace switch por type. NO asumir que exec es el único · es el primero.
```

#### id (identidad de la acción)
```
Permite trackear cada acción en Operations:
   ✓ create_admin     (hecha)
   ✓ migrate_database (hecha)
   ✗ seed_data        (falló · reintentar)
Sin id no se puede seguir el estado de cada paso.
```

## Ejemplo COMPLETO · matrix-synapse
```json
"matrix-synapse": {
  "name": "Matrix Server (Synapse)",
  "category": "homelab",
  "port": 8008,
  "image": "ghcr.io/element-hq/synapse:latest",
  "official": true,
  "isStack": true,
  "compose": "...SYNAPSE_SERVER_NAME=${SERVER_NAME}...",
  "env": { "SERVER_NAME": "nimos.local" },

  "configVersion": 1,
  "minVersion": "8.2",

  "configFields": [
    {
      "key": "SERVER_NAME",
      "label": "Nombre del servidor",
      "type": "text",
      "required": false,
      "immutable": true,
      "mono": true,
      "auto": { "provider": "domain" },
      "validation": { "type": "hostname" },
      "hint": "Si lo dejas vacío usaremos tu dominio. No se puede cambiar después."
    }
  ],

  "postInstall": [
    {
      "id": "create_admin",
      "type": "exec",
      "waitFor": "healthy",
      "container": "matrix_synapse",
      "command": "register_new_matrix_user -c /data/homeserver.yaml -a -u {{ADMIN_USER}} -p {{ADMIN_PASS}} http://localhost:8008",
      "idempotent": true,
      "fields": [
        { "key": "ADMIN_USER", "label": "Usuario admin", "type": "text", "required": true },
        { "key": "ADMIN_PASS", "label": "Contraseña admin", "type": "password", "required": true, "secret": true }
      ]
    }
  ]
}
```

## Ejemplo · VS Code (code-server) · password ANTES de arrancar (Tipo A puro)
```json
"vscode": {
  "name": "VS Code (code-server)",
  "category": "dev",
  "port": 8443,
  "configVersion": 1,
  "configFields": [
    {
      "key": "PASSWORD",
      "label": "Contraseña de acceso",
      "type": "password",
      "required": true,
      "secret": true,
      "hint": "La contraseña para entrar a VS Code. Elígela tú."
    }
  ]
  // SIN postInstall · VS Code no necesita nada después de arrancar
  // Esto REEMPLAZA el password hardcoded actual ("nimos") · cada instalación
  // con el password que elige el usuario.
}
```

## Ejemplo · app simple (Jellyfin · sin cambios)
```json
"jellyfin": {
  "name": "Jellyfin", "category": "media", "port": 8096, ...
  // SIN configFields, SIN postInstall → instala directo (como ahora)
  // (no necesita ni configVersion · solo lo llevan las apps con config)
}
```

## Flujo que define este contrato
```
1. Pulsar Instalar
2. ¿configFields?.length > 0 || postInstall?.length > 0?   (inferido, sin flag)
   · NO → instalar directo (env del catálogo tal cual · como ahora)
   · SÍ → abrir MODAL (leer configVersion para interpretar)
3. MODAL dinámico:
   · Por cada configFields → campo según type
   · auto:{provider} → NimOS pre-rellena (domain/local_ip/...)
   · immutable → editable ahora (solo-lectura en futuras reconfiguraciones)
   · validation → valida en cliente antes de permitir instalar
   · Por cada postInstall.fields → añade esos campos
   (La exposición NO se toca aquí · se decide en Network después)
4. Usuario rellena (validado), pulsa Instalar
5. NimOS:
   a. Mergea configFields en env (secret:true → trato especial)
   b. compose up
   c. (Capa 2) por cada postInstall (por id): espera waitFor → ejecuta command
      sustituyendo {{TOKENS}} · idempotente · secrets no logueados
   d. registra app + asigna UID (refactor permisos)
```

## Los ajustes que blindan el contrato a futuro (revisión Andrés)
```
configVersion · evolucionar sin romper catálogos viejos      · OBLIGATORIO
minVersion    · bloquear apps que necesitan NimOS más nueva   · compat capacidades
immutable     · variables que no cambian tras instalar (server_name) · clave
reconfigure   · editable después sin reinstalar (Beta 9+)     · puerta abierta
secret        · comportamiento de secretos (no log/api/cifrar) · seguridad
validation    · el catálogo expresa reglas · evita instalaciones rotas
+ id en postInstall   · tracking de acciones en Operations
+ type extensible     · exec es UNO de varios (http/sql/seed... a futuro)
+ auto como objeto    · extensible (random_password, uid...) sin romper
- configApp           · ELIMINADO · se infiere de configFields/postInstall
```

## Pares de ciclo de vida de un campo (resumen)
```
immutable:true   → nunca cambia (server_name)          · GANA sobre reconfigure
reconfigure:true → editable después (SMTP, API key)    · Beta 9+
(ninguno)        → se pone al instalar, no se toca normalmente
```

## Compatibilidad · NO rompe nada
```
· Apps sin configFields/postInstall → flujo actual intacto (instala directo)
· Todos los campos nuevos son OPCIONALES por campo
· El dominio es un configField con auto · NO se ata a openMode (que desaparece)
· La exposición a internet se decide en Network, no aquí
· El backend ya recibe env · solo hay que dejar que el frontend lo edite
```

## Cuestiones para las siguientes capas (no ahora)
```
· random_password: ciclo de vida del secreto generado (mostrar/guardar/recuperar)
· secret + persistencia: ¿cifrado en BD? ¿no persistir y solo pasar a postInstall?
· waitFor "healthy": ¿healthcheck del compose? ¿polling? ¿timeout máximo?
· Fallos postInstall: ¿app instalada sin admin? ¿reintento por id? ¿aviso UI?
· immutable en reconfiguración: la UI de "editar config" respeta solo-lectura
```
