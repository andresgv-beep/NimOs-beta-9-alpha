# Modelo de permisos seguro por app · NimOS Beta 8.2

## Problema

Las apps Docker en NimOS comparten identidad y el modelo de shares pisa sus
permisos. Dos consecuencias:

1. **Funcional**: apps que gestionan su propio `/data` (Synapse UID 991) se
   rompen porque el `chmod -R 2775` del modelo de shares las pisa →
   `PermissionError`. Workaround actual: `chown` manual cada reinstalación.

2. **Seguridad** (lo importante para un NAS con apps expuestas): no hay UID
   único por app. Si Matrix (expuesto a internet) y Immich comparten UID o
   acceden al pool, un Matrix comprometido puede leer/escribir los datos de
   Immich. No hay confinamiento.

## Modelo de amenaza

- Solo el **admin** instala apps (`requireAdmin` ya lo garantiza) → riesgo de
  "app maliciosa instalada" es BAJO.
- Algunas apps están **expuestas a internet** (Matrix) → riesgo de "app
  legítima comprometida desde fuera" es REAL.
- **Objetivo de seguridad**: una app comprometida debe quedar CONFINADA a su
  propio volumen. No puede tocar datos de otras apps ni del sistema.

## Principios de diseño

1. **UID único por app** · cada app tiene su propio UID dedicado, asignado por
   NimOS, sin colisiones. Aislamiento entre apps.
2. **Mínimo privilegio** · cada app es dueña SOLO de su volumen, nada más.
3. **El catálogo no decide el UID** · NimOS lo asigna y lo registra. (El
   catálogo PUEDE sugerir un UID fijo para apps que lo exigen, ver más abajo.)
4. **Confinamiento** · el volumen de una app NO recibe el modelo de shares si
   la app gestiona sus propios permisos. Su UID es el dueño.
5. **Auditable** · NimOS registra qué UID tiene cada app (tabla).

## Asignación y ciclo de vida del UID · DECISIÓN (validada)

NimOS tiene DOS desinstalaciones:
  · Desinstalar (normal) → quita la app pero CONSERVA los datos en disco
  · Desinstalación TOTAL → quita la app Y borra los datos

Esto OBLIGA al modelo "no reusar UIDs entre apps distintas":
  · Tras desinstalar normal, los archivos del UID siguen en disco A PROPÓSITO
    (el usuario quiere reinstalar luego conservando sus datos).
  · Verificar "no hay containers con este UID" NO basta para liberarlo: los
    containers son efímeros, los DATOS persisten. Reusar el UID haría que una
    app nueva heredara los datos conservados de la app anterior → ataque de
    reciclaje de UID + corrupción de datos del usuario.

Modelo definitivo:
  · Instalar app NUEVA (app_id nunca visto) → next_uid del contador (++),
    NUNCA se reusa un UID de otra app.
  · Reinstalar MISMA app (mismo app_id) → reusa SU propio UID → recupera sus
    datos conservados. (Solo reusa el suyo, no el de otra app.)
  · Desinstalar normal → marca released_at, datos CONSERVADOS, UID NO liberado.
  · Desinstalación TOTAL → borra datos + userdel, UID queda quemado en el
    historial (tampoco se reusa).
  · Rango 100000-165535 = 65535 UIDs · prácticamente infinito para un NAS.

### Reconciler de higiene (NO libera UIDs)
  · Limpia usuarios de sistema nimos-app-* huérfanos (app desinstalada-total
    Y sin archivos del UID) → userdel.
  · Reporta archivos huérfanos (UID sin app ni activa ni conservada).
  · NUNCA reasigna ni libera un UID para reuso (podría haber datos conservados).
  · Distingue desinstalada-normal (datos vivos, no tocar) de total (sin datos).

### Tablas
```sql
CREATE TABLE uid_allocator ( next_uid INTEGER NOT NULL DEFAULT 100000 );
CREATE TABLE app_uids (
    app_id      TEXT PRIMARY KEY,
    uid         INTEGER NOT NULL UNIQUE,
    gid         INTEGER NOT NULL,
    assigned_at TEXT NOT NULL,
    released_at TEXT   -- NULL=activa, fecha=desinstalada (normal o total)
);
```

## Diseño técnico

### Tabla de asignación de UIDs (nueva)
```sql
CREATE TABLE app_uids (
    app_id   TEXT PRIMARY KEY,
    uid      INTEGER NOT NULL UNIQUE,
    gid      INTEGER NOT NULL,
    assigned_at TEXT NOT NULL
);
```
- Al instalar una app, NimOS le asigna el siguiente UID libre del rango
  (o el UID fijo de la imagen si lo exige), lo registra, y crea el usuario
  de sistema correspondiente (`useradd -u <uid> -r nimos-app-<appid>`).
- Idempotente: si la app ya tiene UID, lo reusa.

### Permisos del volumen
```
Volumen de la app:
  chown -R <uid>:<gid>  · la app es dueña
  chmod -R 0750         · dueño rwx, grupo rx, otros nada (confinado)
  NO setgid de grupo compartido · cada app su gid
  EXCLUIR del modelo de shares (no chmod 2775)
```

### El FileManager · RESUELTO (verificado en código)
- El FileManager son las funciones filesBrowse/filesDelete/etc. en files.go,
  que corren DENTRO del daemon (proceso root, /opt/nimos/daemon/nimos-daemon).
- Lee/escribe con os.Open/os.ReadDir DIRECTAMENTE como root.
- El control de acceso es a nivel de APLICACIÓN: mira session.Role y los
  permisos del share en la BD (s.Permissions[username]), NO los permisos POSIX.
- CONSECUENCIA CLAVE: el daemon root navega CUALQUIER volumen sin importar su
  dueño/permisos POSIX. NO necesita el grupo compartido nimos-share-docker-apps.
- Por tanto, el grupo compartido + chmod 2775 era un PARCHE INNECESARIO que
  además rompía las apps de UID propio. Se ELIMINA del modelo de apps Docker.
  (Ojo: el grupo SÍ puede seguir siendo necesario para SHARES de usuarios
  humanos vía SMB/NFS, que NO pasan por el daemon. Eso es otro flujo · revisar
  que este cambio solo afecta a volúmenes de apps Docker, no a shares SMB/NFS.)

### Flujo de instalación (nuevo orden)
```
1. Asignar UID único a la app (tabla app_uids) + crear usuario sistema
2. Crear volúmenes, chown al UID de la app, chmod 0750
3. compose up (la app arranca; si hace su propio chown, coincide con el UID)
4. NO aplicar modelo de shares sobre los volúmenes de la app
```

### Apps que exigen UID fijo (postgres 999, etc.)
- El catálogo puede declarar `"fixedUid": 999` para esas apps.
- NimOS usa ese UID en vez del asignado, pero igual lo registra en app_uids
  y aplica el mismo confinamiento (volumen exclusivo 0700/0750).
- Si dos apps exigen el MISMO fixedUid (dos postgres) → cada una en su volumen
  exclusivo; comparten UID pero no volumen, así que siguen aisladas a nivel de
  datos (aunque comparten identidad de proceso · documentar el matiz).

## Migración (apps ya instaladas)
- Las apps actuales no tienen UID asignado. Al arrancar el daemon, una
  migración les asigna UID del rango y ajusta permisos de sus volúmenes.
- CUIDADO: cambiar el dueño de los datos existentes de una app puede romperla
  si esperaba otro UID. Hay que mapear el UID actual de los datos → asignar
  ese, o re-chown con cuidado.

## userns-remap · DESCARTADO (investigado 17/06/2026)

Se investigó Docker userns-remap como posible capa de aislamiento. CONCLUSIÓN:
NO sirve para el objetivo (aislar apps ENTRE SÍ) y tiene pegas serias para
NimOS. Se descarta. Razones:

  · Es GLOBAL, no por-app: aplica el MISMO remapeo a TODOS los containers.
    No permite "UID único por app". Da aislamiento container↔HOST (un escape
    de container no llega a root del host), NO app↔app. No es lo que queremos.
  · Hay que activarlo en Docker LIMPIO: invalida imágenes/volúmenes existentes
    (Docker los mueve a /var/lib/docker/<uid>.<gid>/). Deshabilitarlo pierde
    acceso a lo creado con él.
  · Rompe network_mode:host (--network=host incompatible) → apps como Pi-hole
    o Home Assistant que usan host-networking dejarían de funcionar.
  · Incompatible con --privileged (sin --userns=host), drivers de volumen
    externos, compartir PID/NET con host.
  · Overhead de traducción UID/GID en operaciones de FS, peor en ARM (Pi).

userns-remap protege escape→host-root, que es OTRA capa distinta del
aislamiento app↔app. Si algún día se quiere ese hardening extra, está aquí
documentado con sus pegas. Por ahora: FUERA.

## Modelo elegido · Capa 1 (aislamiento app↔app)

  · UID único por app (asignado por NimOS, rango 100000+, no reusable).
  · Cada app dueña SOLO de su volumen (chown UID + chmod 0750).
  · Apps internas (postgres de un stack) no expuestas, en la red de su stack.
  · Sin grupo compartido nimos-share-docker-apps en volúmenes de apps (el
    daemon root navega igual; ese grupo era un parche innecesario que rompía
    las apps de UID propio). OJO: no tocar el modelo de shares SMB/NFS humanos.

## A verificar antes de codear la Capa 1
1. ✅ FileManager corre como root (daemon) → no necesita grupo compartido.
   (verificado en files.go · usa os.Open directo, control de acceso por app)
2. ✅ Grupo compartido solo en docker_stacks.go, no en shares SMB/NFS humanos.
   (verificado · tocar apps Docker no afecta shares humanos)
3. ⏳ Rango 100000+ en ARM64 → probar: useradd -u 100001 -r -M nimos-test
4. ⏳ Apps que EXIGEN UID fijo (postgres 999) → respetar el UID de imagen pero
   confinar por volumen exclusivo (0700/0750). Comparten identidad pero no
   datos. Aislamiento adicional: no exponer (viven en la red de su stack).
5. ⏳ linuxserver.io (PUID/PGID) → pasarles el UID asignado como PUID/PGID
   (encajan perfecto, son flexibles por diseño).
6. ⏳ Migración apps existentes → NO aplica: Andrés desinstalará todas las apps
   antes del refactor. Empezamos con contador en 100000 limpio.

## Fases de implementación
  Fase 1 · Tabla app_uids + uid_allocator + asignación al instalar (+ useradd)
  Fase 2 · Aplicar chown UID + chmod 0750 + excluir del modelo de shares
  Fase 3 · Quitar el grupo compartido del modelo de apps Docker
  Fase 4 · Reconciler de higiene (limpia usuarios fantasma, NO reusa UIDs)
  (Sin fase de migración · se parte de cero tras desinstalar todo.)

## Multi-arquitectura · ARM64 (Pi) Y amd64 (Z370) · IMPORTANTE
NimOS corre en ARM64 (Raspberry Pi) Y amd64 (sobremesa Z370, Ubuntu resolute
26.04). El modelo de permisos (Capa 1) es AGNÓSTICO de arquitectura porque se
basa en UIDs de Linux, permisos POSIX y chown/chmod (idénticos en ambas). Pero:
  · Verificar useradd -u 100001 en AMBAS máquinas (Pi ARM64 + Z370 amd64).
    Debería ir bien en las dos (es estándar Linux), pero confirmar en ambas.
  · imageUID (Config.User): las imágenes son multi-arch; el Config.User es
    metadata, debería ser igual en ARM y amd64 (Synapse vacío en ambas), pero
    verificar al reinstalar en cada máquina.
  · userns-remap descartado también beneficia a amd64 (era global y rompía
    host-net en ambas; overhead peor en ARM pero molesto en las dos).
  · Las fases de implementación y tests deben pasar/probarse en las DOS arquis.
