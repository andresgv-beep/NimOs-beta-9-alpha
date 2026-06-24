# Addendum a PERMISOS-DESIGN.md · Contrato `runtimeIdentity` (cierre del punto 5)

> Estado: DISEÑO · 20/06/2026 · cierra las decisiones abiertas para que una app
> corra con el MISMO UID único al que NimOS chowna su volumen.
> NO es un doc nuevo: es la continuación de `PERMISOS-DESIGN.md` (punto 5).

> ┌─────────────────────────────────────────────────────────────────────┐
> │ MAPA · de dónde sale cada pieza (esto NO se inventa, se termina):    │
> │  · PERMISOS-DESIGN.md · punto 5 ya decidió "pasar el UID asignado a   │
> │    la app como PUID/PGID". Aquí se GENERALIZA (no solo PUID/PGID) y   │
> │    se especifica la implementación que faltaba.                      │
> │  · APP-CATALOG-SCHEMA.md · ya lista el provider `auto:{provider:uid}` │
> │    como futuro ("→ UID asignado · refactor permisos"). Es la mitad    │
> │    de RESOLUCIÓN del contrato. Aquí se añade `gid` y se conecta.      │
> │  · ENV-ESCAPE-HALLAZGO.md · un UID es entero → inyectarlo en el .env  │
> │    es seguro (el único carácter problemático era el `$`).            │
> └─────────────────────────────────────────────────────────────────────┘

## El hallazgo que motiva el addendum

8 apps del catálogo (Gitea, las linuxserver, Syncthing) **hardcodean `UID 1000`**
en su compose (`USER_UID=1000` / `PUID=1000`). Pero NimOS chowna el volumen al
**UID único asignado** (rango 100000+) con `0750`. La app acaba corriendo como
`1000`, el volumen es de `100xxx` → la app **no puede leer/escribir su propio
volumen** → fallo (Gitea: `InitCfgProvider() [F] Unable to init conf` → crash
loop; Synapse: la misma familia, ya descrita en el punto 1).

Causa raíz ÚNICA: NimOS asigna el UID pero **nunca se lo dice a la app**. El
punto 5 ya preveía decírselo (como PUID/PGID), pero (a) no se implementó y
(b) solo contemplaba `PUID/PGID`, dejando fuera a Gitea (`USER_UID`) y Synapse
(`UID`).

## Los tres tipos de app (solo uno se rompe)

```
1. Corre como root y se queda root (pihole, adguard, portainer)
   → root escribe cualquier volumen → NO se rompe. No tocar.

2. Imagen con Config.User FIJO (grafana=472, prometheus=65534)
   → imageUID() lo lee y decideVolumePlan ya chowna a ese UID → YA funciona.

3. Se baja a un UID interno fijo ≠ el asignado (las 8 + Synapse)
   → ES LA QUE SE ROMPE. Y todas son adaptables por env var.
```

Conclusión del inventario (1ª pasada · confirmar `Config.User` en el Pi):
**no hay ningún bloqueante.** Todas las afectadas tienen una perilla de env;
solo cambia el NOMBRE de la variable.

| App | Imagen | Perilla (env) |
|---|---|---|
| gitea | gitea/gitea | `USER_UID` / `USER_GID` |
| transmission | linuxserver | `PUID` / `PGID` |
| qbittorrent | linuxserver | `PUID` / `PGID` |
| sonarr | linuxserver | `PUID` / `PGID` |
| radarr | linuxserver | `PUID` / `PGID` |
| prowlarr | linuxserver | `PUID` / `PGID` |
| code-server | linuxserver | `PUID` / `PGID` |
| syncthing | syncthing/syncthing | `PUID` / `PGID` |
| synapse | element-hq/synapse | `UID` / `GID` (o `user:`) |

## Validación en hierro real (Pi · ARM64 · 20/06/2026)

Confirmado contra el sistema real (ya NO es hipótesis):

**Mecanismo EXACTO del fallo (Gitea).** El volumen `/data` (raíz) es
`drwxr-x--- 100021 100021` (`0750`, dueño = UID asignado por NimOS). Gitea corre
como `USER_UID=1000`. Para llegar a `/data/gitea/conf/app.ini`, el proceso 1000
debe ATRAVESAR `/data` (`0750`), donde 1000 cae en "otros" = `---` → no puede ni
entrar:
```
stat /data/gitea/conf/app.ini: permission denied
```
No es "no puede leer el fichero": es que **no puede cruzar la raíz de su propio
volumen**. (Los subdirs `git/` y `gitea/` son de 1000 — los creó el init de Gitea
como root antes de bajarse — pero son INALCANZABLES tras la raíz `0750/100021`.)
→ Con `runtimeIdentity` inyectando `USER_UID=100021`, Gitea corre como 100021 =
dueño de `/data` → atraviesa y arranca. El fix resuelve esto al milímetro.

**Modelo per-UID activo y correcto.** Las 29 apps tienen UID único (100000–100028),
allocator en 100029. BD real del daemon: **`/var/lib/nimos/config/nimos.db`**
(tablas `app_uids`, `uid_allocator`). (Ojo: hay un `/var/lib/nimos/nimos.db`
vacío/huérfano que confunde · el daemon NO lo usa · revisar por qué existe.)

**Lista DEFINITIVA de apps que rompen** (corren como UID fijo ≠ su asignado):

| App | UID asignado | corre como | perilla |
|---|---|---|---|
| gitea | 100021 | 1000 | `USER_UID` ← **roto confirmado** |
| codeserver | 100006 | 1000 | `PUID` |
| prowlarr | 100013 | 1000 | `PUID` |
| sonarr | 100014 | 1000 | `PUID` |
| radarr | 100015 | 1000 | `PUID` |
| qbittorrent | 100016 | 1000 | `PUID` |
| transmission | 100017 | 1000 | `PUID` |
| syncthing | 100024 | 1000 | `PUID` |
| matrix-synapse | 100001 | 991 | `UID` / `GID` |

**FUERA de este bug (verificado en hierro):**
- **Root** (pihole, adguard, portainer, minecraft, vaultwarden, nginx-proxy,
  homeassistant, jellyfin): root atraviesa cualquier `0750` → OK.
- **Config.User fijo** (grafana=472, prometheus=nobody, n8n=node, ketesa=sws,
  element=nginx): `decideVolumePlan` chowna al UID de la imagen → OK (confirmar
  que `imageUID` resuelve nombre→número en `sws`/`nginx`/`nobody`).
- **authelia** (Restarting 1): `Config.User` vacío; `exit 1` apunta a error de
  CONFIG, no de permisos → diagnosticar aparte por logs, NO meter en este saco.
- **immich** (multi-servicio: server + postgres 999 + ml + redis): el límite ya
  documentado · `runtimeIdentity` app-level no basta · separar / por-servicio.
- **filebrowser** (100008): UID asignado pero SIN container → registro huérfano
  en `app_uids` (backlog item 3 · GC de huérfanos).

## El contrato · dos casos, ambos confinados por volumen

```
runtimeIdentity (caso "env")  · app flexible que se baja a un UID interno
                                configurable por env var. NimOS asigna un UID
                                único, lo INYECTA en esa env var, y chowna el
                                volumen a ese mismo UID. (← este addendum)

fixedUid (caso "fijo")        · app que EXIGE un UID concreto (postgres 999).
                                NimOS usa ese UID, chowna el volumen a él, lo
                                registra y confina por volumen. (← ya en
                                PERMISOS-DESIGN, punto 4 · no cambia)
```

Son **mutuamente excluyentes**: una app es "fija" O "flexible-por-env".
Regla de coexistencia: **si `fixedUid` está presente, manda** (no se inyecta
`runtimeIdentity`). Las apps tipo 1 y 2 (arriba) no declaran ninguno de los dos.

**Alcance · stacks multi-servicio (LÍMITE consciente, no olvido).**
`runtimeIdentity` es a nivel de app y aplica al **servicio principal**. Hay stacks
con varios servicios y necesidades de UID DISTINTAS por servicio (ej. Immich =
servidor que querría `PUID` + su postgres que querría `fixedUid: 999`). Un solo
`uidEnv`/`gidEnv` no sabe expresar eso. Por ahora NO se resuelve aquí: se sortea
**separando apps** (como ya se hace · ver backlog "multi-servicio") o con un
contrato POR-SERVICIO en el futuro. Se documenta el límite A PROPÓSITO para que
dentro de 3 meses no parezca un descuido del contrato.

### Declaración en el catálogo

```json
"runtimeIdentity": {
  "uidEnv": "USER_UID",
  "gidEnv": "USER_GID"
}
```

**⚠ Qué declara `runtimeIdentity` — y qué NO (leer antes de tocarlo):**

`runtimeIdentity` **NO define el UID.** Define **los NOMBRES de las variables de
entorno** que NimOS rellena con el UID/GID que él mismo asigna. El valor es
siempre un nombre de variable (string), JAMÁS un número.

```
❌  "runtimeIdentity": { "uid": 1000 }           · MAL · esto NO es el UID a usar
✅  "runtimeIdentity": { "uidEnv": "USER_UID" }   · el NOMBRE de la env var que
                                                    NimOS rellena con el UID asignado
```

El sufijo `Env` + este ejemplo existen para que dentro de 6 meses nadie meta un
número literal ahí y rompa el contrato. Meter `"uid": 1000` violaría además el
**principio nº3 de PERMISOS-DESIGN**: *"El catálogo no decide el UID · NimOS lo
asigna y lo registra."* El catálogo solo dice *por qué variable* recibirlo.

Decisiones cerradas:

- **Bloque DEDICADO, no un configField.** Los `configFields` son *campos que el
  usuario rellena en el modal*; un UID es fontanería de NimOS, no config de
  usuario. Un "configField oculto" sería una contradicción y ensuciaría el modal.
- **El catálogo NOMBRA la variable** (`uidEnv`/`gidEnv`), no se adivina. Así
  cubre `USER_UID` (Gitea), `PUID` (linuxserver), `UID` (Synapse) con un solo
  contrato. (Generaliza el punto 5, que solo decía PUID/PGID.)
- **El sufijo `Env`** deja claro que el valor es *el nombre de la variable*, no
  el número.
- **El compose DEBE usar interpolación**: `environment: - USER_UID=${USER_UID}`
  (NUNCA `=1000`). Es lo que permite que el valor del `.env` llegue al container.

### Resolución · reusa la LÓGICA de los providers `uid`/`gid` (no el `auto` de un configField)

⚠ Matiz de implementación: en el schema, `auto:{provider:uid}` es un *atributo de
un configField*. `runtimeIdentity` **NO es un configField** y NO pasa por esa
maquinaria. Lo que se reusa es la **lógica de resolución** de los providers
`uid`/`gid` (el código que calcula el UID/GID asignado), invocada DIRECTAMENTE
desde `runtimeIdentity`. El schema ya prevé el provider `uid`; este addendum:

- Añade el provider hermano **`gid`**.
- Invoca `uid`/`gid` desde `runtimeIdentity.uidEnv`/`gidEnv` (no desde un configField).

Flujo (encaja en el "nuevo orden" de instalación de PERMISOS-DESIGN):

```
1. assignAppUID(appID)  · idempotente · devuelve (uid, gid) únicos
   (por diseño gid == uid · cada app su propio grupo, mismo número · así
    PGID/USER_GID reciben el mismo valor que PUID/USER_UID · NO es un error)
2. Si la app declara runtimeIdentity:
     autoEnv[uidEnv] = uid     (entero · sin escape · ver ENV-ESCAPE)
     autoEnv[gidEnv] = gid
   (se inyectan como vars INTERNAS de NimOS, igual que HOST_IP/TZ · NO van al
    modal, NO al frontend, NO a logs)
3. writeEnvFile  · el .env ya lleva USER_UID=100xxx
4. applyAppPermissions · assignAppUID de nuevo (idempotente, mismo UID) →
   decideVolumePlan chowna el volumen al UID asignado (rama flexible)
5. compose up · docker interpola ${USER_UID} → el proceso corre como 100xxx
   → proceso y volumen COINCIDEN → la app accede a su volumen. Aislamiento intacto.
```

Por qué es seguro inyectar (ENV-ESCAPE, probado en hardware real): el único
carácter problemático en el `.env` es el `$`; un UID es un entero → no necesita
escape, y es un valor INTERNO de NimOS (no del modal), así que ni siquiera entra
en la lógica de escape de valores de usuario.

## A verificar antes de codear (Regla 16)

1. ⏳ `Config.User` REAL de cada imagen instalada en el Pi (no de memoria):
   ```
   docker images --format '{{.Repository}}' | sort -u | while read i; do \
     printf '%-45s ' "$i"; docker inspect "$i" --format '{{.Config.User}}' 2>/dev/null; echo; done
   ```
   Confirma cuáles van por la rama "fijo" (Config.User no vacío) y cuáles son
   flexibles (vacío → necesitan `runtimeIdentity`).
2. ⏳ Que el compose de las 8 apps use `${USER_UID}`/`${PUID}` y no `=1000`
   (cambio de catálogo · Fase B).
3. ✅ `useradd -u 100001` en ARM64 y amd64 (ya en PERMISOS-DESIGN, punto 3).
4. ⏳ Reinstalación limpia: Andrés desinstala las apps afectadas antes del cambio
   (mismo enfoque "empezar de cero" del refactor de permisos). Al reinstalar,
   `assignAppUID` REUSA el mismo UID (idempotente) y `applyAppPermissions`
   re-chowna los datos a ese UID → las apps que se "auto-curaron" a 1000
   transicionan sin perder datos (cambia el dueño, no el contenido).
5. ⏳ UID alto sin entrada en `/etc/passwd`: la app correrá como un UID del rango
   100000+ que NO existe en el `/etc/passwd` del container. Gitea y linuxserver
   crean el usuario y van bien, pero algunas imágenes llaman a `getpwuid()` y
   fallan ("cannot find name for user ID 100005"). Confirmar por imagen que
   tolera un UID alto sin passwd (se ve al reinstalar, Fase C).

## Fases de implementación (encajan en las de PERMISOS-DESIGN)

```
Fase A · Daemon
   · Leer runtimeIdentity del catálogo (llega como el resto de metadata).
   · Helper PURO: (runtimeIdentity, uid, gid) → overrides de env. Con test de mesa.
   · En dockerStackDeploy: asignar UID ANTES de writeEnvFile (idempotente) e
     inyectar los overrides en autoEnv. Providers uid/gid en la resolución de auto.
   · Nada más toca el motor.

Fase B · Catálogo
   · Las 8 apps: `=1000` → `${USER_UID}`/`${PUID}` + declarar runtimeIdentity.
   · Synapse: añadir runtimeIdentity {uidEnv:"UID", gidEnv:"GID"} (cierra item 1).

   ⚠ Fase A y Fase B van JUNTAS · ninguna sola arregla end-to-end:
       daemon sin catálogo → el compose sigue con =1000 (ignora el .env)
       catálogo sin daemon → ${USER_UID} queda vacío → la app falla

Fase C · Verificación en Pi (ARM64 Y amd64)
   · Reinstalar Gitea limpio → confirmar: volumen dueño = UID asignado, proceso
     corre como UID asignado (`docker exec gitea id`), Gitea arranca sin crash.
   · Luego el resto de las 8 + Synapse.
```

## Multi-arquitectura

Agnóstico de arquitectura (UIDs de Linux + POSIX, idénticos en ARM64 y amd64).
Igual que el resto del refactor de permisos: las fases y tests se prueban en las
DOS máquinas (Pi ARM64 + Z370 amd64).

## NOTA para el yo-futuro

- Esto **cierra el punto 5 generalizándolo** (no solo PUID/PGID). No se reinventa
  nada: la resolución (`auto:uid`) ya estaba prevista en el schema; aquí se le
  añade `gid` y se le pone la declaración (`runtimeIdentity`) y la inyección.
- El UID **nunca** va al frontend ni a logs (no es secreto, pero es ruido y es
  identidad interna). Por eso bloque dedicado, no configField.
- `runtimeIdentity` es el BUCKET que escala: mañana puede llevar `umask`,
  `supplementaryGroups`, etc. (identidad del proceso). Las `capabilities` NO son
  identidad (son perfil de seguridad Linux) → probablemente quieran su propio
  hermano `runtimeSecurity` el día que hagan falta. NO se cierra hoy.
- Posible unificación futura `fixedUid` + `runtimeIdentity` bajo un `mode`
  (`env`/`fixed`): NO ahora · `fixedUid` ya existe y funciona; unificar sería un
  cambio cosmético sin urgencia.
