# Sistema de Configuración y Aprovisionamiento de Apps · NimOS

> Estado: DISEÑO (idea cerrada, sin implementar) · 18/06/2026
> Autor de la idea: Andrés · documentado con Claude
> Relación: independiente del refactor de permisos (PERMISOS-DESIGN.md)

## El problema

Algunas apps Docker necesitan **configuración del usuario** y/o **aprovisionamiento
inicial** que NimOS hoy no soporta. NimOS instala las apps con los valores por
defecto del catálogo, sin pedir nada (InstallFlow.svelte pasa `env:
view.catalog.env` tal cual). Consecuencias reales detectadas con Matrix/Synapse:

1. **Variables sin personalizar**: el catálogo trae `env: { SERVER_NAME:
   "nimos.local" }` como placeholder. Synapse se instaló con ese valor por
   defecto en vez del dominio real del usuario → `server_name` incorrecto.
   El catálogo NO puede hardcodear el dominio (es compartido entre todos los
   usuarios; cada uno tiene el suyo: nimosbarraca1.duckdns.org, etc.).

2. **Primer usuario admin (huevo y gallina)**: Synapse arranca vacío. El primer
   admin debe crearse con `register_new_matrix_user` por terminal. Ketesa (admin
   UI) no puede crear el PRIMER admin (necesita un admin existente para entrar).
   El usuario queda obligado a usar terminal tras cada reinstalación.

## La idea (de Andrés)

Un sistema donde el catálogo marca qué apps necesitan configuración avanzada.
Al instalar una de esas apps, NimOS **fuerza un modal de configuración** antes
de proceder. El modal recoge variables Y datos de aprovisionamiento (como el
usuario admin), y tras la instalación NimOS ejecuta las acciones necesarias.

Apps simples (sin configuración marcada) siguen instalándose directo, sin modal.
Esto NO rompe el flujo actual: solo añade un paso para las apps que lo declaran.

## Dos naturalezas distintas (importante)

Los datos que pide el modal son de DOS tipos, que se tratan diferente:

### Tipo A · Variables de entorno (→ .env del compose)
- Ej: `SERVER_NAME` → se escribe en el .env, el container la lee al arrancar.
- Simple: NimOS mergea el valor en el env y hace `compose up`.
- El backend YA casi lo soporta (installApp recibe `env` y lo mergea; solo falta
  que el frontend deje EDITAR ese env antes de enviarlo, en vez de pasar el
  default del catálogo tal cual).

### Tipo B · Acciones post-instalación (→ comando tras arrancar)
- Ej: crear admin → NO es una variable, es un COMANDO que se ejecuta DESPUÉS del
  `compose up`, cuando el servicio ya está vivo y healthy.
  (`register_new_matrix_user -c /data/homeserver.yaml -a -u USER -p PASS URL`)
- Más complejo: requiere esperar healthcheck, ejecutar dentro del container,
  manejar errores, e idempotencia (no re-crear si ya existe en reinstalación).

## Esquema de catálogo propuesto

```json
"matrix-synapse": {
  "name": "Matrix Server (Synapse)",
  ...
  "env": { "SERVER_NAME": "nimos.local" },

  "configFields": [                       // Tipo A · variables → .env
    {
      "key": "SERVER_NAME",
      "label": "Tu dominio Matrix",
      "placeholder": "ej. midominio.duckdns.org",
      "help": "Identidad del servidor. Tus usuarios serán @usuario:DOMINIO. No se puede cambiar después.",
      "type": "text",
      "required": true
    }
  ],

  "postInstall": [                        // Tipo B · acciones tras arrancar
    {
      "type": "exec",
      "waitFor": "healthy",               // espera healthcheck antes de ejecutar
      "container": "matrix_synapse",
      "command": "register_new_matrix_user -c /data/homeserver.yaml -a -u {{ADMIN_USER}} -p {{ADMIN_PASS}} http://localhost:8008",
      "idempotent": true,                 // no falla si el user ya existe (reinstalación)
      "fields": [                         // campos extra que pide el modal para esta acción
        { "key": "ADMIN_USER", "label": "Usuario admin", "type": "text", "required": true },
        { "key": "ADMIN_PASS", "label": "Contraseña admin", "type": "password", "required": true }
      ]
    }
  ]
}
```

## Tipos de campo del modal (propuesta)

Empezar SIMPLE y ampliar:
- `text` · texto libre (dominio, nombre)
- `password` · oculto, no se loguea
- (futuro) `number`, `toggle` (sí/no), `select` (lista de opciones)

## Flujo completo

```
1. Usuario pulsa "Instalar" en una app
2. NimOS mira el catálogo: ¿tiene configFields o postInstall? 
     · NO  → instala directo (flujo actual, sin cambios)
     · SÍ  → abre el MODAL de configuración
3. Modal muestra los campos (configFields + postInstall.fields), validación required
4. Usuario rellena (dominio, admin user/pass...) y pulsa "Instalar"
5. NimOS:
     a. Mergea configFields en el env → escribe .env
     b. compose up
     c. Por cada postInstall: espera waitFor (healthy) → ejecuta command
        (sustituyendo {{ADMIN_USER}} etc. con los valores del modal)
     d. Maneja errores/idempotencia
6. App instalada Y aprovisionada · el usuario entra directo, sin terminal
```

## Piezas a tocar

```
1. CATÁLOGO  · añadir configFields / postInstall a las apps que lo necesiten
2. FRONTEND  · InstallFlow.svelte detecta configFields/postInstall →
               nuevo componente ConfigModal (campos + validación)
3. BACKEND   · 
   · Tipo A: dejar que el frontend edite el env (casi listo, installApp ya recibe env)
   · Tipo B: motor de postInstall · esperar healthcheck + docker exec + 
             sustitución de tokens + idempotencia + ocultar secrets en logs
```

## Implementación por CAPAS (recomendado · no big-bang)

```
CAPA 1 · configFields simples (Tipo A · variables → .env)
   · Modal con campos texto/password + validación required
   · Mergea en el env antes de instalar
   · Resuelve SERVER_NAME (y cualquier variable de usuario)
   · Base sólida, acotada, sin la complejidad del timing

CAPA 2 · postInstall actions (Tipo B · acciones tras arrancar)
   · Se monta ENCIMA de la Capa 1 (reusa el modal, añade ejecución)
   · Esperar healthcheck + docker exec + sustitución de tokens
   · Idempotencia (no re-crear admin en reinstalación)
   · Resuelve el huevo y la gallina del primer admin (Matrix y futuras)
   · Más complejo: timing, errores, secrets
```

Resuelve DOS problemas de un golpe:
  · SERVER_NAME personalizado (Capa 1)
  · Primer admin sin terminal (Capa 2)
Y es un patrón reutilizable para cualquier app futura (Nextcloud, Grafana,
Vaultwarden, etc.) que necesite config de usuario o un admin inicial.

## Cuestiones a resolver antes de implementar

1. SEGURIDAD de secrets: las contraseñas del modal (ADMIN_PASS) NO deben
   loguearse, ni quedar en texto plano en el .env si se pueden evitar. ¿Pasarlas
   solo al comando post-install y no persistirlas? ¿Cómo maneja docker exec el
   secret sin que aparezca en `docker inspect` / logs / ps?
2. IDEMPOTENCIA: en reinstalación (misma app, datos conservados), el admin ya
   existe. El postInstall debe detectarlo y NO fallar (register_new_matrix_user
   con -a sobre user existente da error · capturarlo o comprobar antes).
3. TIMING / waitFor: ¿cómo detecta NimOS que el servicio está "healthy"? 
   ¿healthcheck del compose? ¿polling de un endpoint? ¿timeout máximo?
4. FALLOS: si el postInstall falla (comando error), ¿la app queda instalada
   pero sin admin? ¿se reintenta? ¿se avisa al usuario en la UI?
5. server_name en Matrix · matiz: suele ser el dominio RAÍZ
   (nimosbarraca1.duckdns.org), no el subdominio (matrix.nimosbarraca1...).
   El public_baseurl sí apunta al subdominio. Delegación vía .well-known.
   El modal debería explicar esto al usuario (help text claro).
6. ¿configFields editables solo en INSTALACIÓN, o también re-configurables
   después? (Matrix server_name NO se puede cambiar tras crear el servidor.)

## Relación con otros frentes

- Independiente del refactor de permisos (PERMISOS-DESIGN.md). Pueden ir en
  paralelo o secuencial, no se pisan.
- La Capa 2 RESUELVE la tarea pendiente #4 (primer admin Matrix sin terminal).
- El bug de UI del título largo (que rompía el cajón de apps) es OTRO tema
  aparte (NimOS debería truncar títulos largos, no dejar que empujen iconos).
