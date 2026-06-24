// docker_network_pool.go — Fix B: migración del pool de direcciones de Docker
// para instalaciones YA existentes + detección de "pendiente de reiniciar".
//
// Contexto (ver NETWORK-POOL-DESIGN.md):
//   El pool por defecto de Docker solo da ~31 redes. Cada app NimOS es un stack
//   de compose = una red = una subred → con ~31 apps el pool se agota y no se
//   pueden instalar más ("all predefined address pools have been fully
//   subnetted"). Fix A escribe un pool amplio en daemon.json al INSTALAR Docker,
//   pero las instalaciones que ya existían (como la Pi de prod) tienen un
//   daemon.json sin el pool. Fix B lo añade en caliente, de forma:
//     · NO destructiva  → preserva data-root y cualquier otra clave.
//     · idempotente     → si ya hay default-address-pools, no toca nada.
//     · segura          → si el JSON no parsea, NO se sobrescribe.
//     · SIN auto-reinicio de Docker → reiniciar Docker tumba todas las apps;
//       eso lo decide el usuario (botón en Mantenimiento). Solo avisamos.

package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const dockerDaemonJSONPath = "/etc/docker/daemon.json"

// dockerAddressPools es la ÚNICA fuente de verdad del pool de direcciones que
// NimOS provisiona. La usan tanto el install (Fix A) como la migración (Fix B).
//
// 172.17.0.0/12 en bloques /24 = 4096 redes posibles (vs ~31 por defecto).
// Excluimos 192.168.0.0/16 a propósito: su /20 por defecto solapa con LANs
// domésticas (p.ej. la propia .131 del NAS) y con macvlan del Nivel-2.
func dockerAddressPools() []map[string]interface{} {
	return []map[string]interface{}{
		{"base": "172.17.0.0/12", "size": 24},
	}
}

// mergeDockerAddressPoolJSON añade default-address-pools al contenido de un
// daemon.json si falta, preservando el resto de claves. PURA (testeable).
//
//	changed=false  → ya existía la clave (no se toca) o JSON vacío ya cubierto.
//	err!=nil       → el JSON es inválido; el llamador NO debe escribir nada.
func mergeDockerAddressPoolJSON(existing []byte) (out []byte, changed bool, err error) {
	conf := map[string]interface{}{}
	if strings.TrimSpace(string(existing)) != "" {
		if e := json.Unmarshal(existing, &conf); e != nil {
			return nil, false, e
		}
	}
	if _, ok := conf["default-address-pools"]; ok {
		// Ya configurado (por nosotros o por el usuario) → respetar, no pisar.
		return existing, false, nil
	}
	conf["default-address-pools"] = dockerAddressPools()
	out, err = json.MarshalIndent(conf, "", "  ")
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

// ensureDockerAddressPool migra el daemon.json existente. Llamado en el arranque
// del daemon. Best-effort: cualquier fallo se loguea pero no aborta NimOS.
func ensureDockerAddressPool() {
	data, err := os.ReadFile(dockerDaemonJSONPath)
	if err != nil {
		// Sin daemon.json no migramos: o Docker no está gestionado por NimOS, o
		// aún no se instaló (Fix A lo cubrirá al instalar). No fabricamos config
		// para un Docker desconocido.
		if !os.IsNotExist(err) {
			logMsg("docker-pool: no se pudo leer %s: %v", dockerDaemonJSONPath, err)
		}
		return
	}
	out, changed, err := mergeDockerAddressPoolJSON(data)
	if err != nil {
		// LÍNEA ROJA: nunca sobrescribir un daemon.json que no podemos parsear.
		logMsg("docker-pool: daemon.json no parseable, no se toca: %v", err)
		return
	}
	if !changed {
		return // ya tiene pool → idempotente, nada que hacer
	}
	if err := os.WriteFile(dockerDaemonJSONPath, out, 0644); err != nil {
		logMsg("docker-pool: no se pudo escribir daemon.json: %v", err)
		return
	}
	logMsg("docker-pool: default-address-pools añadido a daemon.json (pendiente de reiniciar Docker para aplicar)")
	notifWarning("Red de Docker ampliada",
		"Se amplió el pool de redes de Docker para permitir instalar más apps. Reinícialo desde Panel de Control → Mantenimiento para aplicarlo (corte breve de las apps).")
}

// dockerDaemonStartedAt devuelve cuándo arrancó el proceso de Docker, leyendo el
// mtime de /proc/<MainPID> (= hora de inicio del proceso, sin parsear timestamps
// de systemd, que traen abreviaturas de zona poco fiables). ok=false si no se
// puede determinar.
func dockerDaemonStartedAt() (time.Time, bool) {
	out, ok := runSafeNoLog("systemctl", "show", "docker", "--property=MainPID")
	if !ok {
		return time.Time{}, false
	}
	pidStr := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out), "MainPID="))
	pid, err := strconv.Atoi(pidStr)
	if err != nil || pid <= 0 {
		return time.Time{}, false
	}
	fi, err := os.Stat("/proc/" + pidStr)
	if err != nil {
		return time.Time{}, false
	}
	return fi.ModTime(), true
}

// dockerAddressPoolRestartPending indica si el pool está escrito en daemon.json
// pero Docker NO se ha reiniciado desde entonces (la config aún no está activa).
// Stateless y auto-correctivo: en cuanto Docker reinicia, deja de ser pending.
func dockerAddressPoolRestartPending() bool {
	fi, err := os.Stat(dockerDaemonJSONPath)
	if err != nil {
		return false
	}
	data, err := os.ReadFile(dockerDaemonJSONPath)
	if err != nil {
		return false
	}
	conf := map[string]interface{}{}
	if json.Unmarshal(data, &conf) != nil {
		return false
	}
	if _, ok := conf["default-address-pools"]; !ok {
		return false // no hay pool configurado → nada pendiente
	}
	startedAt, ok := dockerDaemonStartedAt()
	if !ok {
		return false // no sabemos cuándo arrancó Docker → no damos por pendiente
	}
	// Si el fichero es más nuevo que el arranque de Docker, el cambio aún no se
	// aplicó (Docker arrancó antes de que escribiéramos el pool).
	return fi.ModTime().After(startedAt)
}

// ── HTTP ─────────────────────────────────────────────────────────────────────

// dockerNetworkPoolStatus · GET /api/docker/network-pool
// Devuelve si el pool está configurado y si hace falta reiniciar Docker.
func dockerNetworkPoolStatus(w http.ResponseWriter, r *http.Request) {
	if requireAuth(w, r) == nil {
		return
	}
	poolPresent := false
	if data, err := os.ReadFile(dockerDaemonJSONPath); err == nil {
		conf := map[string]interface{}{}
		if json.Unmarshal(data, &conf) == nil {
			_, poolPresent = conf["default-address-pools"]
		}
	}
	jsonOk(w, map[string]interface{}{
		"poolPresent":    poolPresent,
		"restartPending": dockerAddressPoolRestartPending(),
	})
}

// dockerNetworkPoolRestart · POST /api/docker/network-pool/restart
// Reinicia Docker para aplicar el pool. Acción explícita del usuario (admin).
// Reiniciar Docker NO afecta al daemon de NimOS (procesos separados), pero sí
// reinicia todos los contenedores (corte breve).
func dockerNetworkPoolRestart(w http.ResponseWriter, r *http.Request) {
	if requireAdmin(w, r) == nil {
		return
	}
	if _, ok := runSafe("systemctl", "restart", "docker"); !ok {
		jsonError(w, 500, "No se pudo reiniciar Docker")
		return
	}
	logMsg("docker-pool: Docker reiniciado por petición del usuario · pool de red aplicado")
	notifSuccess("Docker reiniciado", "El nuevo pool de red de Docker ya está activo.")
	jsonOk(w, map[string]interface{}{"ok": true})
}
