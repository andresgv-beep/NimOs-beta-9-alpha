// network_legacy_services.go — Servicios de compartición/acceso legacy.
//
// ⚠️ LEGACY — pendiente migración a v4 (un sprint por servicio).
//
// Estos handlers vienen del antiguo network.go (Beta 7). Gestionan
// servicios del sistema vía systemctl + config files, sin pasar por la
// arquitectura v4 (repo/observer/reconciler). Se conservan tal cual hasta
// que cada servicio tenga su sprint de migración:
//
//   · SSH    → network_legacy_ssh    (futuro: network_ssh_*.go v4)
//   · FTP    → network_legacy_ftp    (futuro: network_ftp_*.go v4)
//   · NFS    → network_legacy_nfs    (futuro: network_nfs_*.go v4)
//   · WebDAV → network_legacy_webdav (futuro: network_webdav_*.go v4)
//   · SMB    → network_legacy_smb    (futuro: network_smb_*.go v4)
//
// NO añadir features aquí. Si necesitas tocar un servicio, migra primero.

package main

import (
	"net/http"
	"regexp"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
// SSH
// ─────────────────────────────────────────────────────────────────────────────

func handleSshRoutes(w http.ResponseWriter, r *http.Request) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	switch {
	case r.URL.Path == "/api/ssh/status" && r.Method == "GET":
		running, _ := runShellStatic("systemctl is-active sshd 2>/dev/null || systemctl is-active ssh 2>/dev/null")
		version, _ := runShellStatic("ssh -V 2>&1 | head -1")
		jsonOk(w, map[string]interface{}{"running": strings.TrimSpace(running) == "active", "version": version})
	case r.URL.Path == "/api/ssh/start" && r.Method == "POST":
		runShellStatic("sudo systemctl enable ssh sshd 2>/dev/null; sudo systemctl start sshd 2>/dev/null || sudo systemctl start ssh 2>/dev/null")
		jsonOk(w, map[string]interface{}{"ok": true})
	case r.URL.Path == "/api/ssh/stop" && r.Method == "POST":
		runShellStatic("sudo systemctl stop sshd ssh 2>/dev/null; sudo systemctl disable ssh sshd 2>/dev/null")
		jsonOk(w, map[string]interface{}{"ok": true})
	default:
		jsonError(w, 404, "Not found")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// FTP
// ─────────────────────────────────────────────────────────────────────────────

func handleFtpRoutes(w http.ResponseWriter, r *http.Request) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	switch {
	case r.URL.Path == "/api/ftp/status" && r.Method == "GET":
		_, installed := runShellStatic("which vsftpd 2>/dev/null || test -x /usr/sbin/vsftpd && echo yes")
		running1, _ := runSafe("systemctl", "is-active", "vsftpd")
		running := strings.TrimSpace(running1) == "active"
		jsonOk(w, map[string]interface{}{"installed": installed, "running": running})
	case r.URL.Path == "/api/ftp/start" && r.Method == "POST":
		runShellStatic("sudo systemctl enable vsftpd 2>/dev/null; sudo systemctl start vsftpd 2>/dev/null")
		openServicePorts("ftp")
		jsonOk(w, map[string]interface{}{"ok": true})
	case r.URL.Path == "/api/ftp/stop" && r.Method == "POST":
		runShellStatic("sudo systemctl stop vsftpd 2>/dev/null; sudo systemctl disable vsftpd 2>/dev/null")
		closeServicePorts("ftp")
		jsonOk(w, map[string]interface{}{"ok": true})
	default:
		jsonError(w, 404, "Not found")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// NFS
// ─────────────────────────────────────────────────────────────────────────────

func handleNfsRoutes(w http.ResponseWriter, r *http.Request) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	switch {
	case r.URL.Path == "/api/nfs/status" && r.Method == "GET":
		_, installed := runShellStatic("dpkg -l nfs-kernel-server 2>/dev/null | grep -q '^ii' && echo yes")
		running1, _ := runSafe("systemctl", "is-active", "nfs-server")
		running := strings.TrimSpace(running1) == "active"
		exports := readFileStr("/etc/exports")
		jsonOk(w, map[string]interface{}{"installed": installed, "running": running, "exports": exports})
	case r.URL.Path == "/api/nfs/start" && r.Method == "POST":
		runShellStatic("sudo systemctl enable nfs-server 2>/dev/null; sudo systemctl start nfs-server 2>/dev/null")
		openServicePorts("nfs")
		jsonOk(w, map[string]interface{}{"ok": true})
	case r.URL.Path == "/api/nfs/stop" && r.Method == "POST":
		runShellStatic("sudo systemctl stop nfs-server 2>/dev/null; sudo systemctl disable nfs-server 2>/dev/null")
		closeServicePorts("nfs")
		jsonOk(w, map[string]interface{}{"ok": true})
	default:
		jsonError(w, 404, "Not found")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// SMB / Samba
// ─────────────────────────────────────────────────────────────────────────────

func handleSmbRoutes(w http.ResponseWriter, r *http.Request) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	urlPath := r.URL.Path
	method := r.Method

	if urlPath == "/api/smb/status" && method == "GET" {
		_, installed := runShellStatic("which smbd 2>/dev/null || test -x /usr/sbin/smbd && echo yes")
		running1, _ := runSafe("systemctl", "is-active", "smbd")
		running := strings.TrimSpace(running1) == "active"
		version, _ := runSafe("smbd", "--version")
		config := readJSONConfig(smbConfigFile, map[string]interface{}{"workgroup": "WORKGROUP", "serverString": "NimOS NAS"})
		jsonOk(w, map[string]interface{}{"installed": installed, "running": running, "version": version, "config": config, "port": 445})
		return
	}

	if urlPath == "/api/smb/config" && method == "POST" {
		if session.Role != "admin" {
			jsonError(w, 403, "Admin required")
			return
		}
		body, _ := readBody(r)
		current := readJSONConfig(smbConfigFile, map[string]interface{}{})
		for k, v := range body {
			current[k] = v
		}
		writeJSONConfig(smbConfigFile, current)
		jsonOk(w, map[string]interface{}{"ok": true, "config": current})
		return
	}

	if urlPath == "/api/smb/start" && method == "POST" {
		if session.Role != "admin" {
			jsonError(w, 403, "Admin required")
			return
		}
		runShellStatic("sudo systemctl enable smbd nmbd 2>/dev/null; sudo systemctl start smbd nmbd 2>/dev/null")
		openServicePorts("smb")
		jsonOk(w, map[string]interface{}{"ok": true})
		return
	}

	if urlPath == "/api/smb/stop" && method == "POST" {
		if session.Role != "admin" {
			jsonError(w, 403, "Admin required")
			return
		}
		runShellStatic("sudo systemctl stop smbd nmbd 2>/dev/null; sudo systemctl disable smbd nmbd 2>/dev/null")
		closeServicePorts("smb")
		jsonOk(w, map[string]interface{}{"ok": true})
		return
	}

	if urlPath == "/api/smb/restart" && method == "POST" {
		if session.Role != "admin" {
			jsonError(w, 403, "Admin required")
			return
		}
		runSafe("sudo", "systemctl", "restart", "smbd", "nmbd")
		jsonOk(w, map[string]interface{}{"ok": true})
		return
	}

	if urlPath == "/api/smb/apply" && method == "POST" {
		if session.Role != "admin" {
			jsonError(w, 403, "Admin required")
			return
		}
		runSafe("sudo", "smbcontrol", "all", "reload-config")
		jsonOk(w, map[string]interface{}{"ok": true})
		return
	}

	if urlPath == "/api/smb/set-password" && method == "POST" {
		if session.Role != "admin" {
			jsonError(w, 403, "Admin required")
			return
		}
		body, _ := readBody(r)
		username := bodyStr(body, "username")
		password := bodyStr(body, "password")
		if username == "" || password == "" {
			jsonError(w, 400, "Username and password required")
			return
		}
		handleOp(Request{Op: "user.set_smb_password", Username: username, Password: password})
		jsonOk(w, map[string]interface{}{"ok": true})
		return
	}

	// PUT /api/smb/share/:name
	reSmbShare := regexp.MustCompile(`^/api/smb/share/([a-zA-Z0-9_-]+)$`)
	if m := reSmbShare.FindStringSubmatch(urlPath); m != nil && method == "PUT" {
		if session.Role != "admin" {
			jsonError(w, 403, "Admin required")
			return
		}
		jsonOk(w, map[string]interface{}{"ok": true, "name": m[1]})
		return
	}

	jsonError(w, 404, "Not found")
}

// ─────────────────────────────────────────────────────────────────────────────
// Registro de rutas legacy supervivientes
// ─────────────────────────────────────────────────────────────────────────────

// registerLegacyServiceRoutes registra los endpoints de los servicios
// legacy que aún no tienen equivalente v4. Se invoca desde el setup HTTP.
func registerLegacyServiceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/ssh/", handleSshRoutes)
	mux.HandleFunc("/api/ftp/", handleFtpRoutes)
	mux.HandleFunc("/api/nfs/", handleNfsRoutes)
	mux.HandleFunc("/api/smb/", handleSmbRoutes)
}
