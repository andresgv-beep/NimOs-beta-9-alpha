// docker_dataroot_guard.go — Norma 1 de Docker: su data-root manda.
//
// PROBLEMA QUE RESUELVE (fallo detectado en hardware, Pi):
// Docker tiene su data-root en daemon.json apuntando a /nimos/pools/<X>/docker.
// Si ese pool se cae (disco fallido/degradado/desmontado), Docker arranca igual
// y escribe en el DISCO DE SISTEMA (la SD en la Pi → la llena → tumba el SO).
// NimOS no se enteraba porque Docker tiene config propia y no le pregunta.
//
// NORMA 1 (Andrés): si Docker tiene una config estipulada (data-root en un
// pool), NO puede saltársela escribiendo en otro sitio. Si el pool no está,
// Docker se DETIENE hasta que el usuario corrija el pool. NimHealth, al
// reiniciar/comprobar, vuelve a verificar y rearranca Docker si el pool ya está
// disponible.
//
// Este guard es la implementación de esa norma.

package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// DockerDataRootStatus describe si es seguro tener Docker corriendo.
type DockerDataRootStatus struct {
	Safe      bool   // ¿es seguro arrancar/mantener Docker?
	DataRoot  string // el data-root configurado en daemon.json
	PoolMount string // el mountpoint del pool derivado del data-root
	Reason    string // si no es seguro, por qué
	Code      string // no_config | not_in_pool | pool_not_mounted | ok
}

// checkDockerDataRoot lee la config de Docker y comprueba si su data-root está
// sobre un pool REALMENTE montado. Es la verificación de la Norma 1.
//
// Devuelve Safe=false si:
//   - el data-root apunta a /nimos/pools/<X> pero ese pool NO está montado
//     (escribir ahí caería en el disco de sistema).
//
// Devuelve Safe=true si:
//   - el data-root está sobre un pool montado, o
//   - no hay data-root en un pool (Docker usa su default; no es asunto nuestro).
var checkDockerDataRoot = func() DockerDataRootStatus {
	conf := getDockerConfigGo()
	dataRoot, _ := conf["data-root"].(string)
	if dataRoot == "" {
		// Sin data-root configurado por NimOS → Docker usa /var/lib/docker
		// (disco de sistema, pero es SU default consciente, no un pool caído).
		return DockerDataRootStatus{Safe: true, Code: "no_config",
			Reason: "Docker no tiene data-root en un pool de NimOS"}
	}

	if !strings.HasPrefix(dataRoot, nimosPoolsDir+"/") {
		// data-root fuera de /nimos/pools → no es un pool nuestro, no opinamos.
		return DockerDataRootStatus{Safe: true, Code: "not_in_pool",
			DataRoot: dataRoot,
			Reason:   "el data-root de Docker no está en un pool de NimOS"}
	}

	// data-root = /nimos/pools/<X>/docker/data → derivar el mountpoint del pool.
	poolMount := dockerDataRootToPoolMount(dataRoot)
	if poolMount == "" || !isPoolMounted(poolMount) {
		return DockerDataRootStatus{Safe: false, Code: "pool_not_mounted",
			DataRoot: dataRoot, PoolMount: poolMount,
			Reason: fmt.Sprintf("el almacenamiento de Docker (%s) no está montado; arrancar Docker escribiría en el disco de sistema", poolMount)}
	}

	return DockerDataRootStatus{Safe: true, Code: "ok",
		DataRoot: dataRoot, PoolMount: poolMount}
}

// dockerDataRootToPoolMount deriva el mountpoint del pool desde el data-root.
//   /nimos/pools/data8/docker/data → /nimos/pools/data8
func dockerDataRootToPoolMount(dataRoot string) string {
	if !strings.HasPrefix(dataRoot, nimosPoolsDir+"/") {
		return ""
	}
	rel := strings.TrimPrefix(dataRoot, nimosPoolsDir+"/")
	parts := strings.SplitN(rel, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	return filepath.Join(nimosPoolsDir, parts[0])
}

// ensureDockerSafeOrStop aplica la Norma 1: si el data-root de Docker no es
// seguro (pool no montado), DETIENE Docker y notifica. Si es seguro, no hace
// nada (deja que Docker siga su curso normal).
//
// Se llama:
//   - al arrancar NimOS (boot), antes de dejar que Docker opere.
//   - desde NimHealth periódicamente / al reiniciar, para reaccionar a cambios.
//
// Devuelve true si Docker está en estado seguro (corriendo o parado-a-propósito),
// false si tuvo que detenerlo por seguridad.
var ensureDockerSafeOrStop = func() bool {
	status := checkDockerDataRoot()
	if status.Safe {
		return true
	}

	// NO es seguro: el pool de Docker no está montado. Norma 1 → detener Docker
	// para que NO escriba en el disco de sistema, y avisar al usuario.
	logMsg("docker_guard: DETENIENDO Docker — %s", status.Reason)
	runSafe("systemctl", "stop", "docker", "docker.socket", "containerd")

	notifyDockerStopped(status)
	return false
}

// notifyDockerStopped registra/notifica que Docker se detuvo por seguridad.
// Separado para poder testear ensureDockerSafeOrStop sin efectos.
var notifyDockerStopped = func(status DockerDataRootStatus) {
	logMsg("docker_guard: Docker detenido por seguridad. El usuario debe corregir el pool %s. Cuando el pool vuelva a estar montado, NimHealth rearrancará Docker.", status.PoolMount)
	// Notificación push al usuario (si el subsistema está disponible).
	notifWarning(
		"Docker detenido para proteger el sistema",
		fmt.Sprintf("El almacenamiento de Docker (%s) no está disponible. Docker se detuvo para no escribir en el disco de sistema. Revisa el estado del pool; Docker volverá a arrancar cuando esté montado.", status.PoolMount),
	)
}

// ensureDockerStartedIfSafe es la otra mitad de la Norma 1: si el pool de Docker
// está disponible Y Docker NO está corriendo (lo paramos antes, o un reinicio),
// lo arranca. Esto es lo que hace NimHealth "volver a encenderlo" cuando el
// usuario corrige el pool.
//
// Devuelve true si Docker quedó corriendo (o ya lo estaba).
var ensureDockerStartedIfSafe = func() bool {
	status := checkDockerDataRoot()
	if !status.Safe {
		return false // sigue sin ser seguro; no arrancar
	}
	if dockerDaemonReady() {
		return true // ya está corriendo y responde
	}
	// Pool disponible y Docker parado → arrancar.
	logMsg("docker_guard: pool de Docker (%s) disponible y Docker parado → arrancando", status.PoolMount)
	runSafe("systemctl", "start", "containerd", "docker")
	return dockerDaemonReady()
}
