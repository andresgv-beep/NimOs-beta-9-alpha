# Escape de valores en el .env · hallazgo y opciones

> Estado: INVESTIGACIÓN · 19/06/2026
> Problema detectado al diseñar la Capa 1 del sistema de config (modal).
> Andrés va a investigar opciones. Este doc recoge lo verificado.

## El problema

El sistema de config (modal) deja al usuario meter valores libres (ej. un
PASSWORD para VSCode, un SERVER_NAME para Matrix). Esos valores se escriben al
`.env` del stack. El escritor actual NO escapa nada:

```go
// docker_env.go · writeEnvFile (extraído de docker_stacks.go)
fmt.Sprintf("%s=%v", k, env[k])    // sin escape
```

Para valores simples (dominios, nombres, passwords alfanuméricos) funciona.
Pero un password con caracteres especiales puede romper o corromper el .env:

```
my$ecret   → docker-compose interpreta $ecret como variable → "my" (ROTO)
Casa #2024 → # puede cortar como comentario según contexto
pa ss      → espacio puede truncar
```

## Cómo NimOS consume el .env (VERIFICADO en código)

```
Deploy: docker compose -f composePath up -d   (cmd.Dir = stackPath)
   → docker-compose lee el .env del directorio AUTOMÁTICAMENTE
   → lo usa para INTERPOLAR ${VAR} en el compose
```

## El conflicto de fondo (VERIFICADO en los compose del catálogo)

Hay DOS formas de que un valor del .env llegue al container, y cada una trata
el `$` de forma OPUESTA:

```
1. INTERPOLACIÓN · compose con environment: - X=${VAR}
   (Matrix: SYNAPSE_SERVER_NAME=${SERVER_NAME})
   → docker-compose lee el .env e INTERPRETA el $
   → para un $ LITERAL en el valor hay que duplicarlo: $ → $$
   → .env: PASSWORD=my$$ecret  → container ve: my$ecret  ✅

2. ENV_FILE · compose con env_file: .env
   (Immich usa env_file)
   → docker pasa cada línea del .env TAL CUAL al container (sin interpolar)
   → el valor va literal · NO se duplica el $
   → si duplicáramos: container vería my$$ecret (doble $)  ❌
```

CONFLICTO: la MISMA variable con un `$` no puede servir bien a interpolación y
a env_file a la vez. El escape correcto DEPENDE de cómo se consuma la variable.

## Estado por app (VERIFICADO en catalog.json)

```
matrix-synapse · environment con ${VAR}  · INTERPOLACIÓN  → querría $$
immich         · env_file: true          · ENV_FILE       → querría $ literal
vscode         · env vacío, sin ${VAR}   · (habría que añadirle PASSWORD)
jellyfin       · environment con ${VAR}  · INTERPOLACIÓN
```

## Observación clave (a verificar por Andrés)

Las variables que vienen del MODAL (configFields · las que mete el usuario)
parecen ir a apps que usan INTERPOLACIÓN (Matrix ${SERVER_NAME}, VSCode
${PASSWORD} cuando se añada). Las de ENV_FILE (Immich) son credenciales
INTERNAS ({RANDOM}) que NimOS ya gestiona aparte (resolveRandomPlaceholders),
NO vienen del modal.

SI esto se confirma (ninguna var del modal cae en una app con env_file):
   → escapar $ → $$ en writeEnvFile es SEGURO para el caso del modal
SI alguna var del modal va a un env_file:
   → hace falta un escape POR-VARIABLE según cómo la consuma su compose

## Opciones sobre la mesa (para que Andrés decida)

### Opción 1 · Escapar $ → $$ globalmente en writeEnvFile
```
+ Simple, un solo punto
+ Correcto para interpolación (Matrix, VSCode, la mayoría)
- Rompería apps con env_file que tengan $ en un valor (Immich y futuras)
- Asume que ninguna var de env_file lleva $ (hoy {RANDOM} genera sin $, pero frágil)
```

### Opción 2 · Escape por-variable según el consumo del compose
```
+ Correcto siempre (detecta si la var va a ${} o a env_file)
- Complejo · hay que parsear el compose y saber qué variable va a dónde
- Más código, más superficie de error
```

### Opción 3 · Validar en el modal (no escapar en backend)
```
+ Simple · el modal rechaza caracteres problemáticos ($ # espacios) en passwords
- Limita al usuario (no puede usar $ en su password)
- Mala experiencia · passwords fuertes suelen llevar símbolos
- No resuelve el problema de raíz, solo lo evita
```

### Opción 4 · postInstall en vez de .env para secretos del usuario
```
Idea: los passwords del usuario NO van al .env · van por postInstall (comando
que los recibe como argumento, no por interpolación).
Ej: VSCode podría arrancar sin password y setearlo por postInstall.
+ Evita el problema del .env del todo para secretos
- No todas las apps permiten setear el secreto por comando
- VSCode (code-server) lee el PASSWORD del entorno · no encaja fácil
```

### Opción 5 · Generar el .env con env_file SIEMPRE y no interpolar
```
Idea: cambiar el modelo · pasar el .env como env_file en TODAS las apps,
evitando la interpolación (y su problema con $).
+ Un solo comportamiento (env_file · $ literal)
- Cambio grande · los compose del catálogo usan ${VAR} (interpolación)
- Habría que reescribir cómo funcionan los compose · alto riesgo
```

## Recomendación provisional (sin cerrar · Andrés investiga)

La Opción 1 ($ → $$) es la más simple y cubre el caso del modal SI se confirma
que las vars del modal van por interpolación. Pero la Opción 2 (por-variable) es
la "correcta de verdad". La 3 (validar) es el plan B rápido si urge.

Lo que NO se debe hacer: meter un escape a ciegas sin saber cómo se consume cada
variable · rompería apps de forma sutil.

## Lo ya hecho (no se pierde)

```
✅ writeEnvFile extraída a docker_env.go (modular, testeable)
✅ Test de caracterización · captura el comportamiento actual (sin escape)
✅ .env ahora determinista (orden alfabético) · mejora equivalente
✅ Suite completa verde · la extracción no rompió nada
```
El escape se añade DENTRO de writeEnvFile cuando se decida la opción · la base
modular y testeada ya está lista para recibirlo.

## ═══════════════════════════════════════════════════════════
## RESULTADOS DE LOS TESTS REALES (Pi · ARM64) · 19/06/2026
## ═══════════════════════════════════════════════════════════

Los tests de integración (test-env-escape.sh y test-env-escape2.sh) en hardware
real DESMONTARON casi todas las suposiciones teóricas. Datos definitivos:

### Test 1 · qué caracteres rompen (sin escape)
```
Carácter   INTERPOLACIÓN    ENV_FILE      ¿problema?
─────────────────────────────────────────────────────
$ (dólar)  my$ecret→"my"    "my"          ⚠️ ROMPE en AMBOS
# (hash)   OK               OK            ✅ no toca
espacio    OK               OK            ✅ no toca
= (igual)  OK               OK            ✅ no toca
" (comilla) OK              OK            ✅ no toca
```
CONCLUSIÓN 1: el ÚNICO carácter problemático es el $. Los demás (# espacio = ")
funcionan perfecto SIN escape. (Mis suposiciones teóricas eran FALSAS.)
CONCLUSIÓN 2: el $ rompe IGUAL en interpolación y env_file → NO hay conflicto
entre los dos modos (la gran preocupación inicial era infundada).

### Test 2 · cómo recuperar un $ literal
```
Escape       INTERPOLACIÓN    ENV_FILE     ¿da my$ecret?
──────────────────────────────────────────────────────────
my$$ecret →  my$ecret        my$ecret     ✅ SÍ (en AMBOS modos)
my\$ecret →  my\             my\          ❌ NO (rompe peor)
```
CONCLUSIÓN 3: duplicar el $ ($→$$) es la solución universal · funciona igual
en interpolación y env_file. El backslash NO sirve.

### MATIZ CRÍTICO descubierto al implementar (otro error evitado)
Un replace ciego $→$$ en TODOS los valores ROMPERÍA las referencias ${VAR}
internas de NimOS que quedan sin resolver (ej. PROJECTS_PATH=${CONFIG_PATH}/x
si CONFIG_PATH no se resolvió). El $ de esas referencias es SINTAXIS, no literal.

Por tanto el escape debe ser POR ORIGEN del valor:
```
$ en valores del USUARIO (passwords del modal)  → SÍ escapar ($→$$)
$ en referencias internas de NimOS (${VAR})      → NO tocar (sintaxis)
```
Implementación correcta: escapar SOLO los valores que vienen del MODAL
(configFields), al mergear body.env, ANTES de las expansiones de NimOS.
NO va en writeEnvFile (que ve todos los valores mezclados).

### Plan definitivo (con datos reales)
```
1. secret:true + reglas (0600, no-log, no-API) · YA · no depende del modal
2. Escape $→$$ · cuando exista el modal/configFields · solo valores de usuario
   · El backend escapa el $ de los configFields al mergearlos
   · Las vars de NimOS (${VAR}) quedan intactas
3. NO escapar # espacio = " · funcionan solos (probado)
```

## ═══════════════════════════════════════════════════════════
## REVISIÓN DE ANDRÉS (19/06/2026) · reordena prioridades
## ═══════════════════════════════════════════════════════════

CORRECCIÓN DE ENFOQUE: se estaba diseñando la solución definitiva (5 opciones)
ANTES de verificar el comportamiento real de docker-compose. El orden correcto:
PRIMERO probar de verdad, LUEGO elegir solución.

### El problema REAL no es el escape · es el SECRETO
Aunque el escape fuera perfecto, el password sigue en TEXTO PLANO en el .env.
El problema de fondo: ¿quién lo lee? ¿aparece en logs/APIs/backups/exports?
   → secret:true + permisos 0600 + no-log + no-API resuelve MÁS que el escape.

### Distinguir secretos del resto
   · Dominios/nombres (SERVER_NAME, DOMAIN, BASE_URL) → raramente tienen $ # raros
   · Secretos (PASSWORD, TOKEN, API_KEY) → pueden tener CUALQUIER cosa
   → el problema del escape afecta SOBRE TODO a los secretos.

### Recomendación para Beta 8.2 (mantener simple)
```
{ "key": "ADMIN_PASS", "secret": true }
Reglas para secret:true:
   · guardar en .env con permisos 0600, propietario root
   · NUNCA mostrar el valor en APIs
   · NUNCA escribir el valor en logs
   · NUNCA devolver el valor al frontend tras guardar
NO introducir todavía un sistema completo de secretos.
```

### Prioridades (revisión Andrés)
```
ALTA:
   · Verificar comportamiento real con tests Docker Compose (Paso 1 · test-env-escape.sh)
   · Añadir secret:true + reglas (0600, no-log, no-API)
MEDIA:
   · Resolver correctamente el caso del $ (según lo que diga el test real)
BAJA:
   · Parsear compose para detectar modo de consumo (Opción 2)
   · Sistema avanzado de secretos
   · Rediseñar el modelo de .env (Opción 5)
```

### Valoración de las 5 opciones (Andrés)
```
Opción 1 ($→$$ global)   · razonable SI se confirma que el modal solo interpola
Opción 2 (por-variable)  · la solución final, pero no para 8.2
Opción 3 (validar/prohibir) · NO recomendable (mala UX, no resuelve raíz)
Opción 4 (postInstall)   · útil para casos concretos, no estrategia global
Opción 5 (todo env_file) · excesivo para el problema actual
```

### Conclusión de la revisión
El problema existe pero NO está entre los principales riesgos arquitectónicos
actuales. Antes de construir algo complejo, verificar:
   1. Cómo interpreta docker-compose los valores problemáticos (test real)
   2. Si alguna var del modal acaba en una app con env_file
   3. Si afecta a casos reales del catálogo o solo hipotéticos futuros
SI las vars del modal siempre interpolan → escape simple $→$$ basta para 8.2,
dejando la solución sofisticada para después.

### TEST REAL pendiente · test-env-escape.sh
Ejecutar en Pi (ARM64) Y Z370 (amd64). Crea un .env con valores problemáticos
(my$ecret, Casa#2024, pa ss...), los pasa por interpolación Y por env_file, y
captura con `env` qué recibe el container realmente. Elimina toda suposición.

## ───────────────────────────────────────────────────────────
## (Análisis técnico previo · se mantiene como referencia)
## ───────────────────────────────────────────────────────────
