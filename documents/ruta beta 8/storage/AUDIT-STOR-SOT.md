# Auditoría STOR-SOT · Fuente de Verdad del módulo Storage

**Fecha:** 2026-06-08
**Disparador:** Conversión single→raid1 por terminal. NimOS siguió mostrando
`single` tras reiniciar, con health en `WARN/unstable`, porque la BD no reflejó
el cambio real del filesystem.

**Veredicto:** Fallo de diseño real y sistémico, no un caso aislado. NimOS trata
la BD como fuente de verdad para hechos que BTRFS conoce mejor. La clase entera
de bugs se llama **drift BD↔realidad**.

---

## 1. El problema de raíz

NimOS mantiene **dos fuentes de verdad** sobre el mismo hecho físico:

- **La realidad** — lo que BTRFS reporta del disco (profile, devices, tamaño…)
- **La BD (SQLite)** — lo que NimOS cacheó la última vez que pasó por su flujo

Se sincronizan **solo por el camino feliz** (las mutaciones vía API/modal).
Cualquier cambio del filesystem por fuera de NimOS —terminal, script, recovery
manual, reconexión de discos— provoca divergencia silenciosa, y NimOS sirve su
copia obsoleta sin contrastarla.

Un caché de estado del mundo externo **siempre deriva**, porque el mundo cambia
por vías que el caché no intercepta. Por eso esto no es "un bug", es una clase.

---

## 2. Clasificación de cada campo cacheado

Criterio: **¿lo sabe BTRFS? → leer en vivo. ¿Es metadato propio de NimOS? →
cachear en BD es correcto.**

### tabla `storage_pools`

| Campo | ¿Quién es la verdad? | Estado actual | Veredicto |
|-------|----------------------|---------------|-----------|
| `id` (UUID interno) | NimOS | BD | ✅ correcto cachear |
| `name` | NimOS (lo asigna el user) | BD | ✅ correcto |
| `btrfs_uuid` | BTRFS (inmutable) | BD | ✅ correcto (no cambia nunca) |
| **`profile`** | **BTRFS** | **BD, sin contrastar** | ❌ **BUG — causa de hoy** |
| `mount_point` | NimOS (lo decide al crear) | BD | ⚠️ aceptable (NimOS lo fija) |
| `role` | NimOS (roadmap) | BD | ✅ correcto |
| `compression` | BTRFS (property) | BD, sin contrastar | ⚠️ drift potencial |
| `control_state` | NimOS | BD | ✅ correcto |
| `generation` | NimOS | BD | ✅ correcto |

### tabla `storage_devices`

| Campo | ¿Quién es la verdad? | Estado actual | Veredicto |
|-------|----------------------|---------------|-----------|
| `serial` | hardware (firmware) | BD | ✅ identidad estable |
| `by_id_path` | kernel/udev | BD, sin contrastar | ❌ **puede quedar obsoleto** (ya nos mordió hoy en AddDevice) |
| `current_path` | kernel (cambia en reboot) | BD | ⚠️ caché runtime, debe refrescarse en scan |
| `model`/`size_bytes` | hardware | BD | ✅ estable |

### Composición del pool (qué devices lo forman)

| Hecho | ¿Quién es la verdad? | Estado actual | Veredicto |
|-------|----------------------|---------------|-----------|
| Devices que forman el pool | **BTRFS** (`fi show`) | BD (`storage_pool_devices`) | ❌ **mismo bug que profile** — si añades/quitas un disco por CLI, NimOS no se entera |

---

## 3. Hallazgos priorizados

### SOT-01 (CRÍTICO) · profile cacheado sin contrastar — *causa del incidente*
`GetPool` → `s.repo.GetPool` lee `profile` de la BD y lo sirve tal cual.
`enrichPool` añade Usage/Health/Mounted en vivo, pero **nunca toca el profile
ni la composición de devices**. Resultado: BD dice `single`, disco dice `raid1`,
NimOS muestra `single` para siempre (hasta un reinicio con STOR-01 desplegado).

### SOT-02 (CRÍTICO) · composición de devices cacheada sin contrastar
Idéntico a SOT-01 pero para qué discos forman el pool. Añadir/quitar un device
por `btrfs device add/remove` deja `storage_pool_devices` divergente.

### SOT-03 (ALTO) · STOR-01 solo corre al boot, no en runtime
La detección de drift (`detectLayoutDrift`) existe y es correcta, pero **solo se
invoca en `main.go` al arrancar**. Un drift con el daemon en marcha (como el de
hoy) no se detecta hasta el siguiente reinicio. Y aun reiniciando, requiere que
el binario tenga el código STOR-01 desplegado (hoy no lo tenía).

### SOT-04 (ALTO) · `by_id_path` obsoleto rompe operaciones
Ya nos mordió hoy: el `by-id` guardado no existía en `/dev/disk/by-id/` y el
`btrfs device add` falló. Parcheado con fallback verificado a `current_path`,
pero la raíz es la misma: se confió en un path cacheado sin verificar que vive.

### SOT-05 (MEDIO) · `compression` cacheada sin contrastar
Si alguien cambia la compresión por `btrfs property set` por CLI, la BD miente.
Mismo patrón, menor impacto (la compresión no es crítica para integridad).

### SOT-06 (MEDIO) · health "pegajoso" tras limpiar errores
El health usa `corruption_errs` del device. Tras un scrub limpio, el contador
sigue marcado hasta un `btrfs device stats -z` manual. NimOS no resetea ni
distingue "errores históricos ya resueltos" de "errores activos", dejando el
pool en WARN sin causa viva.

---

## 4. La solución de raíz

**Principio rector:** *La BD guarda lo que BTRFS no sabe. Todo lo que BTRFS sabe,
se lee de BTRFS en cada `GetPool`.*

### Fase A — `profile` y composición como lectura en vivo (cierra SOT-01, SOT-02)
En `enrichPool` (o un paso nuevo `reconcilePoolWithReality`), tras cargar el pool
de BD y si está montado:
1. Leer profile real (`readRealDataProfile`, ya existe — STOR-01 lo usa).
2. Leer devices reales (`btrfs filesystem show`).
3. Si difieren de la BD: servir **el valor real** + lanzar persistencia en
   background (self-heal) en vez de esperar a un reinicio.

Resultado: el profile mostrado nunca puede divergir de la realidad, porque se
relee cada vez. La BD pasa a ser una caché que se auto-corrige.

### Fase B — drift detection en runtime, no solo boot (cierra SOT-03)
Convertir `detectLayoutDrift` de "una vez al boot" a parte del ciclo de refresco
(o dispararlo en cada `GetPool`/`ListPools` con un throttle). El código ya está;
es cambiar dónde se invoca.

### Fase C — verificación de paths antes de usarlos (cierra SOT-04)
Generalizar el fallback que metimos hoy en AddDevice: cualquier operación que use
`by_id_path`/`current_path` verifica `os.Stat` antes y refresca el path desde el
scan si está muerto. Idealmente, re-resolver el path desde `serial` (la identidad
absoluta) en vez de confiar en el cacheado.

### Fase D — compression en vivo + health no pegajoso (cierra SOT-05, SOT-06)
- Leer compression real de `btrfs property get` en enrichPool.
- Health: distinguir errores activos de históricos; ofrecer reset de contadores
  desde la UI tras un scrub limpio, o auto-reset si el scrub más reciente salió
  limpio.

---

## 5. Lo que NO hay que tocar

- `id`, `name`, `role`, `control_state`, `btrfs_uuid`, `serial`, `model`:
  metadatos propios de NimOS o inmutables. Cachear en BD es correcto.
- La estructura de operaciones/recovery: funciona bien, es ortogonal.

---

## 6. Orden de ataque recomendado

1. **Fase A** primero (cierra el incidente de hoy de raíz; es el corazón).
2. **Fase C** (barata, ya tenemos el patrón de hoy; evita roturas de operaciones).
3. **Fase B** (apoya a A; el código ya existe, es recablear).
4. **Fase D** (pulido; menor criticidad).

Cada fase con sus tests estrictos (validación + no-falsos-positivos), igual que
hicimos en la auditoría STOR original. Disciplina DISCIPLINE v2.1: refactor solo
con causa (aquí la causa es real y demostrada), sin deudas, módulo por módulo.

---

## 7. Nota honesta

Parte de esto ya lo intuimos al construir STOR-01 (drift de layout). El diseño no
estaba ciego al problema — pero se quedó a medias: detecta al boot, no en vivo, y
no generalizó el principio "leer de la realidad" al resto de campos. Esta
auditoría lo lleva hasta el final: convertir el principio en regla del módulo, no
en un parche para un caso.
