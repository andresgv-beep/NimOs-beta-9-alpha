// network_legacy_common.go — Helpers compartidos por los servicios legacy.
//
// ⚠️ LEGACY — pendiente migración a v4.
//
// Estos helpers daban soporte a los handlers HTTP del antiguo network.go
// (Beta 7). Tras retirar F-005 (certs/ACME → Caddy) y los handlers muertos
// (ddns/remote-access/dns/proxy/portal/firewall/router → v4 o Caddy), solo
// sobreviven los servicios de compartición de archivos y acceso:
//
//   · network_legacy_ssh.go
//   · network_legacy_ftp.go
//   · network_legacy_nfs.go
//   · network_legacy_webdav.go
//   · network_legacy_smb.go
//
// Cada uno se migrará a la arquitectura v4 (repo/observer/reconciler) en su
// propio sprint. NO añadir features nuevas aquí: si tocas un servicio,
// migra primero. Cuando todos estén migrados, este archivo desaparece.

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Rutas de configuración de los servicios legacy supervivientes.
const (
	smbConfigFile = "/var/lib/nimos/config/smb.json"
)

// readJSONConfig lee un JSON de config; si no existe o es inválido,
// devuelve los defaults proporcionados.
func readJSONConfig(path string, defaults map[string]interface{}) map[string]interface{} {
	data, err := os.ReadFile(path)
	if err != nil {
		return defaults
	}
	var conf map[string]interface{}
	if json.Unmarshal(data, &conf) != nil {
		return defaults
	}
	return conf
}

// writeJSONConfig persiste un objeto de config como JSON indentado,
// creando el directorio padre si hace falta.
func writeJSONConfig(path string, conf interface{}) {
	data, _ := json.MarshalIndent(conf, "", "  ")
	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, data, 0644)
}
