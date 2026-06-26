// storage_layout_guard.go — FIX-1 · Gate de montaje para ops de layout.
//
// PROBLEMA (incidente data8, junio 2026):
// Las ops de layout —AddDevice, RemoveDevice, ReplaceDevice, ConvertProfile—
// ejecutan `btrfs device add/remove/replace` o `btrfs balance ... <mountpoint>`,
// y TODAS requieren el pool montado. Si el pool no está montado (drift BD↔kernel,
// reinicio a media operación), btrfs devuelve el críptico
// "ERROR: not a btrfs filesystem: <mountpoint>" — justo el mensaje que confundió
// durante la reparación, sin pista de que el problema era simplemente "no montado".
//
// SOLUCIÓN:
// Reutilizar la puerta assertPoolWritable (que ya verifica montaje real +
// read-only) ANTES de tocar btrfs, devolviendo un error claro y accionable
// (mount_missing / read_only) en vez del de btrfs.
//
// REGLA 16: ante la duda sobre el montaje, no se opera. Mejor un error honesto
// que una operación de disco sobre coordenadas equivocadas.

package main

// assertLayoutOpAllowed verifica que el pool está montado y escribible antes de
// una operación de layout sobre btrfs. Devuelve un *PoolWritableError explicativo
// si no lo está. Es el gate único de las 4 ops de layout.
func assertLayoutOpAllowed(pool *Pool) error {
	return assertLayoutOpAllowedWith(pool, defaultPoolWritableChecks)
}

// assertLayoutOpAllowedWith permite inyectar las comprobaciones (para tests).
func assertLayoutOpAllowedWith(pool *Pool, c poolWritableChecks) error {
	if pool == nil {
		return &PoolWritableError{Code: "path_invalid", Reason: "pool nulo"}
	}
	if pool.MountPoint == "" {
		return &PoolWritableError{Code: "mount_missing",
			Reason: "el pool no tiene punto de montaje registrado; móntalo antes de operar"}
	}
	return assertPoolWritableWith(pool.MountPoint, c)
}
