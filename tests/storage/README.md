# Fiabilidad Btrfs: pruebas y alcance

Revisión del 11 de noviembre de 2026.

## Cambios verificados

- Las consultas de balance distinguen estado conocido de fallo/salida desconocida,
  conservan el código de salida y tienen un límite de 10 segundos.
- Tras reiniciar el daemon, la desaparición de un balance no demuestra éxito:
  también puede significar cancelación o ENOSPC. Se registra resultado inconcluso
  y se pone el pool en `recovery`, sin actualizar el perfil esperado a ciegas.
- El estado de recuperación y el fallo de la operación se guardan en una sola
  transacción. Si falla la persistencia, permanece el bloqueo `in_progress`.
- Agotar el tiempo de supervisión tampoco permite otra modificación del layout.
- Una conversión que devuelve error, o cuyo resultado no puede persistirse,
  pone el pool en recuperación: puede haber aplicado parte del cambio.
- Aceptar el estado de recuperación exige poder observar un balance inactivo.
- El progreso de la interfaz sigue el ID de la conversión, no la última operación
  del pool. No muestra éxito por ausencia de balance o por un error de consulta.

Una conversión recuperada puede exigir revisión incluso si realmente terminó
bien: no se conserva su código de salida original. Es una decisión conservadora.
La recuperación asistida existente sigue siendo necesaria para aceptar el estado
real. Esta revisión no certifica todos los caminos de reemplazo, montaje o borrado.

## Ejecutar las regresiones sin discos

Desde la raíz del repositorio:

```bash
(cd daemon && GOCACHE=/tmp/nimos-go-cache go test -race ./... -run 'Test(BalanceStatus|ParseBalance|RecoveredBalance|RecoveryUnknown|RecoveryQuarantine|Storage|WatchReadopted)')
node --test tests/storage/conversion-progress.test.mjs
(cd daemon && GOCACHE=/tmp/nimos-go-cache go build -o /tmp/nimos-daemon-btrfs-hardened .)
npm run build
```

Se usan bases SQLite temporales y fallos simulados para comprobar consultas
fallidas, cancelación, timeout, fallo de persistencia, bloqueo de nuevas
operaciones y conversión parcialmente aplicada. Las pruebas de interfaz
comprueban operaciones ausentes, pendientes, equivocadas, completadas y fallidas.

## Prueba con discos virtuales

El script utiliza exclusivamente imágenes temporales propias y loop devices
recién asignados. No acepta dispositivos físicos. Se ejecuta en un espacio de
montajes privado y limpia sus propios recursos al salir. Requiere Linux con
Btrfs, `btrfs-progs`, `util-linux` y permisos de administrador.

```bash
(cd daemon && GOCACHE=/tmp/nimos-go-cache go test -c -o /tmp/nimos-storage.test)
sudo unshare --mount --propagation private env LC_ALL=C \
  NIMOS_STORAGE_TEST_BINARY=/tmp/nimos-storage.test \
  bash tests/storage/btrfs_virtual.sh
```

Cubre creación, conversión single a RAID1, snapshot, retirada de un dispositivo
virtual, montaje degradado, reemplazo, scrub, remontaje y ENOSPC. Comprueba SHA-256
del contenido después de las operaciones. No ejecuta el daemon completo: comprueba
el filesystem real y su lector de estado; la orquestación se prueba por separado.
No simula cortes eléctricos, corrupción de sectores ni reinicios del kernel.

## Resultado en este PC

- Regresiones de almacenamiento con `-race`: superadas.
- Regresiones de estado de la interfaz: superadas.
- Compilación del daemon y de la interfaz: superadas.
- Batería completa `go test ./...`: falla únicamente
  `TestEncryptDecryptSecret_Roundtrip`, que intenta acceder a
  `/var/lib/nimos/config/.server_key` sin permisos. Fallaba también antes de estos
  cambios. No se ha ejecutado toda la batería como root ni alterado esa clave.
- Prueba con Btrfs montado: **no ejecutada**. `sudo -n` informa que requiere
  autenticación interactiva. El script solo tiene validación de sintaxis; su
  ejecución real y la validación de sus resultados quedan pendientes.

No considerar esto una certificación para datos críticos. Faltan ejecutar la
prueba virtual, validar recuperación tras cortes/reinicios y probar restauración
desde copias externas en el hardware y kernel de destino.

## Segunda revisión: identidad y discos ausentes

Se sustituyeron las coincidencias por subcadenas por comparación exacta de las
rutas de las filas `devid`. Una ruta en la etiqueta del pool no acredita un disco.
Cuando existe `by-id`, debe poder resolverse y prevalece sobre `CurrentPath` para
no aceptar un nombre de dispositivo reutilizado.

La consulta de miembros ahora devuelve error si falla el comando, falta el total,
hay filas incompletas/duplicadas o la cantidad de miembros no cuadra. La ausencia
de discos solo se considera comprobada con una lectura completa. Si faltan varios
discos, no se elige automáticamente el primero como origen del reemplazo.
Los formatos de salida no reconocidos requieren revisión: se rechazan en lugar
de asumir que el pool está completo.

Durante la recuperación de un reemplazo, una consulta fallida o un disco nuevo no
confirmado conserva la asignación anterior y pone el pool en `recovery`. No se
restaura redundancia ni se confirma el intercambio sobre esa evidencia dudosa.

Pruebas específicas (sin discos reales):

```bash
GOCACHE=/tmp/nimos-go-cache go -C daemon test -race ./... \
  -run 'Test(StorageMembership|StorageReplaceUnknown|StorageRecoveredReplace|WatchReadoptedReplace)'
```

Incluyen `/dev/loop1` frente a `/dev/loop10`, etiquetas que contienen rutas,
`by-id` válido/roto, rutas antiguas, errores de comando, filas truncadas, varios
discos ausentes y conservación de la asignación en SQLite tras un falso
`finished`. Las antiguas pruebas de discos ausentes duplicaban el algoritmo:
ahora ejercitan el parser y el lector que se usan en producción.
