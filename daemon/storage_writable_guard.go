// storage_writable_guard.go — La puerta ÚNICA de escritura segura.
//
// PROBLEMA QUE RESUELVE:
// Varios servicios (shares, docker, torrent) escriben en /nimos/pools/<X>. Cada
// uno verificaba el montaje por su cuenta con isPathOnMountedPool. Eso cubría
// "el pool no está montado" pero NO otros estados inseguros (read-only,
// degradado al borde del colapso). Y al ser un bool, el servicio no sabía POR
// QUÉ no podía escribir.
//
// assertPoolWritable centraliza TODAS las comprobaciones de seguridad antes de
// una escritura, y devuelve un error explicativo. Es la única puerta: si todos
// los servicios la llaman, ninguna escritura insegura pasa.
//
// FILOSOFÍA (Regla 16 llevada a fiabilidad): ante la duda, NO se escribe. Mejor
// un error honesto que un dato perdido o caído en el disco de sistema.

package main

import (
	"fmt"
	"strings"
)

// PoolWritableError explica por qué un pool NO es seguro para escribir.
type PoolWritableError struct {
	Path   string
	Reason string
	Code   string // mount_missing | path_invalid | read_only | not_a_pool
}

func (e *PoolWritableError) Error() string {
	return fmt.Sprintf("escritura no segura en %s: %s", e.Path, e.Reason)
}

// poolWritableChecks agrupa las comprobaciones inyectables (para tests).
type poolWritableChecks struct {
	mountedPool func(path string) bool       // ¿el path está sobre un pool montado?
	readOnly    func(mountPoint string) bool // ¿el pool está montado read-only?
}

// defaultPoolWritableChecks usa los helpers reales del sistema.
var defaultPoolWritableChecks = poolWritableChecks{
	mountedPool: isPathOnMountedPool,
	readOnly:    poolMountIsReadOnly,
}

// assertPoolWritable es LA puerta de seguridad. Devuelve nil si es seguro
// escribir en `path`, o un *PoolWritableError explicando por qué no.
//
// Comprobaciones, en orden (de más básica a más sutil):
//  1. path no vacío y bajo /nimos/pools/.
//  2. el pool está REALMENTE montado (no cae al disco de sistema).
//  3. el pool NO está en read-only (btrfs se pone ro al detectar daño grave;
//     escribir ahí falla o empeora las cosas).
func assertPoolWritable(path string) error {
	return assertPoolWritableWith(path, defaultPoolWritableChecks)
}

func assertPoolWritableWith(path string, c poolWritableChecks) error {
	if path == "" {
		return &PoolWritableError{Path: path, Code: "path_invalid",
			Reason: "ruta vacía"}
	}
	if !strings.HasPrefix(path, nimosPoolsDir+"/") {
		return &PoolWritableError{Path: path, Code: "not_a_pool",
			Reason: fmt.Sprintf("la ruta no está bajo %s", nimosPoolsDir)}
	}

	// 2. Montaje real: es la protección clave contra escribir en el disco de
	//    sistema cuando el pool no está montado (el bug que más daño hizo).
	if !c.mountedPool(path) {
		return &PoolWritableError{Path: path, Code: "mount_missing",
			Reason: "el pool no está montado; escribir aquí caería en el disco de sistema"}
	}

	// 3. Read-only: btrfs remonta ro al detectar daño grave (errores de árbol,
	//    etc.). Escribir en un pool ro falla; peor, intentarlo puede enmascarar
	//    el problema. Si está ro, NO es escribible.
	poolMount := poolMountFromPath(path)
	if poolMount != "" && c.readOnly(poolMount) {
		return &PoolWritableError{Path: path, Code: "read_only",
			Reason: "el pool está montado en solo-lectura (btrfs detectó un problema); revisa la salud del pool"}
	}

	return nil
}
