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
// SMB ya vive en network_smb.go, pero reutiliza aquí el helper JSON hasta que
// el último servicio legacy haya migrado.
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
func writeJSONConfig(path string, conf interface{}) error {
	data, err := json.MarshalIndent(conf, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return writeFileAtomic(path, append(data, '\n'), 0644)
}

// writeFileAtomic writes a complete replacement beside the destination and
// renames it into place. Readers therefore see either the old file or the new
// one, never a partially-written configuration.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".nimos-config-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
