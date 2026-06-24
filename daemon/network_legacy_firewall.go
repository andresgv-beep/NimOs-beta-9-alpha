// network_legacy_firewall.go — Apertura/cierre de puertos UFW para servicios legacy.
//
// B1: el instalador ya NO abre de fábrica los puertos de FTP/NFS/SMB/WebDAV.
// El firewall arranca con lo mínimo (SSH + Web UI + mDNS). Cuando el usuario
// activa un servicio desde la UI, NimOS abre su puerto; al desactivarlo, lo
// cierra. Así la superficie de ataque sigue al uso real, no a la instalación.
//
// Diseño: helper aislado (no acopla a los handlers ni a la arquitectura v4 de
// network_exposure). Usa el binario `ufw` con argumentos separados (sin shell).
// Degradación silenciosa: si ufw no está activo/instalado, no es un error —
// simplemente no hay firewall que gestionar.

package main

import (
	"strings"
)

// servicePortSpec describe los puertos UFW de un servicio legacy.
// Cada spec es un argumento válido para `ufw allow <spec>` (p.ej. "445/tcp"
// o "55000:55999/tcp").
type servicePortSpec struct {
	name  string
	specs []string
}

var legacyServicePorts = map[string]servicePortSpec{
	"ftp": {name: "FTP", specs: []string{"21/tcp", "55000:55999/tcp"}},
	"nfs": {name: "NFS", specs: []string{"2049/tcp"}},
	"smb": {name: "SMB", specs: []string{"445/tcp"}},
}

// ufwIsActive informa si ufw está instalado y activo. Si no, no hay nada que
// gestionar y los helpers de open/close se vuelven no-ops.
func ufwIsActive() bool {
	out, ok := runSafe("ufw", "status")
	if !ok {
		return false
	}
	return strings.Contains(out, "Status: active")
}

// openServicePorts abre en ufw los puertos del servicio indicado (ftp/nfs/smb).
// No-op si el servicio no tiene puertos mapeados o si ufw no está activo.
func openServicePorts(service string) {
	spec, known := legacyServicePorts[service]
	if !known {
		return
	}
	if !ufwIsActive() {
		logMsg("firewall: ufw inactivo, no se abren puertos de %s", spec.name)
		return
	}
	for _, s := range spec.specs {
		if _, ok := runSafe("ufw", "allow", s, "comment", "NimOS "+spec.name); !ok {
			logMsg("firewall: fallo abriendo %s (%s)", s, spec.name)
		}
	}
	logMsg("firewall: abierto %s (%v)", spec.name, spec.specs)
}

// closeServicePorts retira de ufw los puertos del servicio indicado.
// No-op si el servicio no tiene puertos mapeados o si ufw no está activo.
func closeServicePorts(service string) {
	spec, known := legacyServicePorts[service]
	if !known {
		return
	}
	if !ufwIsActive() {
		return
	}
	for _, s := range spec.specs {
		// `ufw delete allow <spec>` es idempotente: si la regla no existe, ufw
		// lo reporta sin fallar el flujo. Lo registramos pero no es error.
		if _, ok := runSafe("ufw", "delete", "allow", s); !ok {
			logMsg("firewall: nada que cerrar para %s (%s) o fallo al retirar", s, spec.name)
		}
	}
	logMsg("firewall: cerrado %s (%v)", spec.name, spec.specs)
}
