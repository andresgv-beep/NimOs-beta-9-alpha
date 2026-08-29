package main

import (
	"fmt"
	"net/http"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	smbManagedGlobalBegin = "# BEGIN NIMOS MANAGED SMB GLOBAL"
	smbManagedGlobalEnd   = "# END NIMOS MANAGED SMB GLOBAL"
	smbManagedSharesBegin = "# BEGIN NIMOS MANAGED SMB SHARES"
	smbManagedSharesEnd   = "# END NIMOS MANAGED SMB SHARES"
)

var (
	smbSystemConfigFile = "/etc/samba/smb.conf"
	smbCommand          = runSafe
	smbShareRoute       = regexp.MustCompile(`^/api/smb/share/([a-zA-Z0-9_-]+)$`)
	smbWorkgroupPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,14}$`)
)

type SMBConfig struct {
	Workgroup  string `json:"workgroup"`
	ServerName string `json:"serverString"`
}

func defaultSMBConfig() SMBConfig {
	return SMBConfig{Workgroup: "WORKGROUP", ServerName: "NimOS NAS"}
}

func readSMBConfig() SMBConfig {
	defaults := defaultSMBConfig()
	raw := readJSONConfig(smbConfigFile, map[string]interface{}{})
	if value, ok := raw["workgroup"].(string); ok && strings.TrimSpace(value) != "" {
		defaults.Workgroup = strings.TrimSpace(value)
	}
	if value, ok := raw["serverString"].(string); ok && strings.TrimSpace(value) != "" {
		defaults.ServerName = strings.TrimSpace(value)
	}
	return defaults
}

func validateSMBConfig(config SMBConfig) error {
	if !smbWorkgroupPattern.MatchString(config.Workgroup) {
		return fmt.Errorf("workgroup inválido: usa 1-15 letras, números, guion o guion bajo")
	}
	if config.ServerName == "" || utf8.RuneCountInString(config.ServerName) > 96 {
		return fmt.Errorf("nombre visible inválido: debe tener entre 1 y 96 caracteres")
	}
	for _, r := range config.ServerName {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("nombre visible inválido: contiene caracteres de control")
		}
	}
	return nil
}

func saveSMBConfig(config SMBConfig) error {
	config.Workgroup = strings.TrimSpace(config.Workgroup)
	config.ServerName = strings.TrimSpace(config.ServerName)
	if err := validateSMBConfig(config); err != nil {
		return err
	}
	return writeJSONConfig(smbConfigFile, map[string]interface{}{
		"workgroup":    config.Workgroup,
		"serverString": config.ServerName,
	})
}

func stripSMBManagedBlock(input, begin, end string) (string, error) {
	for {
		start := strings.Index(input, begin)
		if start < 0 {
			return input, nil
		}
		finishRel := strings.Index(input[start+len(begin):], end)
		if finishRel < 0 {
			return "", fmt.Errorf("bloque SMB administrado incompleto: falta %q", end)
		}
		finish := start + len(begin) + finishRel + len(end)
		for finish < len(input) && (input[finish] == '\r' || input[finish] == '\n') {
			finish++
		}
		input = input[:start] + input[finish:]
	}
}

func smbSectionNames(config string) map[string]bool {
	sections := map[string]bool{}
	for _, line := range strings.Split(config, "\n") {
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if len(trimmed) >= 3 && strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			name := strings.ToLower(strings.TrimSpace(trimmed[1 : len(trimmed)-1]))
			if name != "" && name != "global" {
				sections[name] = true
			}
		}
	}
	return sections
}

func insertSMBGlobalBlock(base, block string) string {
	lines := strings.Split(base, "\n")
	global := -1
	insertAt := len(lines)
	for i, line := range lines {
		trimmed := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(line, "\r")))
		if trimmed == "[global]" {
			global = i
			continue
		}
		if global >= 0 && i > global && strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			insertAt = i
			break
		}
	}
	if global < 0 {
		return "[global]\n" + block + "\n\n" + strings.TrimLeft(base, "\r\n")
	}
	before := append([]string{}, lines[:insertAt]...)
	after := append([]string{}, lines[insertAt:]...)
	for len(before) > 0 && strings.TrimSpace(before[len(before)-1]) == "" {
		before = before[:len(before)-1]
	}
	out := strings.Join(before, "\n") + "\n\n" + block + "\n"
	if len(after) > 0 {
		out += "\n" + strings.Join(after, "\n")
	}
	return out
}

func validateSMBShare(share DBShare) error {
	if err := checkShareName(share.Name); err != nil {
		return err
	}
	if strings.ContainsAny(share.Path, "\r\n") {
		return fmt.Errorf("share %s: ruta inválida", share.Name)
	}
	clean := pathpkg.Clean(share.Path)
	if !strings.HasPrefix(clean, "/nimos/pools/") {
		return fmt.Errorf("share %s: la ruta debe estar dentro de /nimos/pools", share.Name)
	}
	return nil
}

func renderSMBConfig(existing string, config SMBConfig, shares []DBShare) (string, error) {
	if err := validateSMBConfig(config); err != nil {
		return "", err
	}
	base, err := stripSMBManagedBlock(existing, smbManagedGlobalBegin, smbManagedGlobalEnd)
	if err != nil {
		return "", err
	}
	base, err = stripSMBManagedBlock(base, smbManagedSharesBegin, smbManagedSharesEnd)
	if err != nil {
		return "", err
	}

	unmanagedSections := smbSectionNames(base)
	enabled := make([]DBShare, 0, len(shares))
	for _, share := range shares {
		if !share.SMBEnabled {
			continue
		}
		if err := validateSMBShare(share); err != nil {
			return "", err
		}
		if unmanagedSections[strings.ToLower(share.Name)] {
			return "", fmt.Errorf("ya existe una sección SMB externa llamada [%s]", share.Name)
		}
		enabled = append(enabled, share)
	}
	sort.Slice(enabled, func(i, j int) bool { return enabled[i].Name < enabled[j].Name })

	globalBlock := strings.Join([]string{
		smbManagedGlobalBegin,
		"   workgroup = " + config.Workgroup,
		"   server string = " + config.ServerName,
		smbManagedGlobalEnd,
	}, "\n")
	result := insertSMBGlobalBlock(strings.TrimSpace(base), globalBlock)

	var managed strings.Builder
	managed.WriteString(smbManagedSharesBegin)
	for _, share := range enabled {
		users := make([]string, 0, len(share.Permissions))
		writers := make([]string, 0, len(share.Permissions))
		for username, permission := range share.Permissions {
			if !validUsername.MatchString(username) {
				continue
			}
			if permission == "rw" || permission == "ro" {
				users = append(users, username)
			}
			if permission == "rw" {
				writers = append(writers, username)
			}
		}
		sort.Strings(users)
		sort.Strings(writers)
		group := groupName(share.Name)
		validUsers := append([]string{"@" + group}, users...)
		writeList := append([]string{"@" + group}, writers...)

		managed.WriteString("\n\n[")
		managed.WriteString(share.Name)
		managed.WriteString("]\n")
		managed.WriteString("   path = " + pathpkg.Clean(share.Path) + "\n")
		managed.WriteString("   browseable = yes\n")
		managed.WriteString("   guest ok = no\n")
		managed.WriteString("   read only = yes\n")
		managed.WriteString("   valid users = " + strings.Join(validUsers, " ") + "\n")
		managed.WriteString("   write list = " + strings.Join(writeList, " ") + "\n")
		managed.WriteString("   create mask = 0660\n")
		managed.WriteString("   directory mask = 0770\n")
		managed.WriteString("   inherit acls = yes\n")
		managed.WriteString("   hide unreadable = yes\n")
	}
	managed.WriteString("\n")
	managed.WriteString(smbManagedSharesEnd)
	return strings.TrimSpace(result) + "\n\n" + managed.String() + "\n", nil
}

func applySMBConfiguration() error {
	shares, err := dbSharesListRaw()
	if err != nil {
		return fmt.Errorf("leer carpetas: %w", err)
	}
	for _, share := range shares {
		if !share.SMBEnabled {
			continue
		}
		info, statErr := os.Stat(share.Path)
		if statErr != nil {
			return fmt.Errorf("share %s no disponible: %w", share.Name, statErr)
		}
		if !info.IsDir() {
			return fmt.Errorf("share %s no es un directorio", share.Name)
		}
	}

	existing, err := os.ReadFile(smbSystemConfigFile)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("leer smb.conf: %w", err)
	}
	if os.IsNotExist(err) {
		existing = []byte("[global]\n   server role = standalone server\n   map to guest = bad user\n   min protocol = SMB2\n")
	}
	rendered, err := renderSMBConfig(string(existing), readSMBConfig(), shares)
	if err != nil {
		return err
	}

	dir := filepath.Dir(smbSystemConfigFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("crear directorio Samba: %w", err)
	}
	candidate, err := os.CreateTemp(dir, ".smb.conf.nimos-candidate-*")
	if err != nil {
		return fmt.Errorf("crear candidato smb.conf: %w", err)
	}
	candidatePath := candidate.Name()
	defer os.Remove(candidatePath)
	if err := candidate.Chmod(0644); err != nil {
		candidate.Close()
		return err
	}
	if _, err := candidate.WriteString(rendered); err != nil {
		candidate.Close()
		return err
	}
	if err := candidate.Close(); err != nil {
		return err
	}
	if output, ok := smbCommand("testparm", "-s", candidatePath); !ok {
		return fmt.Errorf("Samba rechazó la configuración: %s", output)
	}

	backupPath := smbSystemConfigFile + ".nimos.bak"
	if len(existing) > 0 {
		if err := writeFileAtomic(backupPath, existing, 0644); err != nil {
			return fmt.Errorf("guardar backup de smb.conf: %w", err)
		}
	}
	if err := writeFileAtomic(smbSystemConfigFile, []byte(rendered), 0644); err != nil {
		return fmt.Errorf("instalar smb.conf: %w", err)
	}

	if _, running := smbCommand("systemctl", "is-active", "smbd"); running {
		if output, ok := smbCommand("smbcontrol", "all", "reload-config"); !ok {
			if rollbackErr := writeFileAtomic(smbSystemConfigFile, existing, 0644); rollbackErr != nil {
				return fmt.Errorf("recarga falló (%s) y rollback falló: %v", output, rollbackErr)
			}
			_, _ = smbCommand("smbcontrol", "all", "reload-config")
			return fmt.Errorf("recarga Samba falló; configuración restaurada: %s", output)
		}
	}
	return nil
}

func startSMBService() error {
	if err := applySMBConfiguration(); err != nil {
		return err
	}
	if output, ok := smbCommand("systemctl", "enable", "smbd"); !ok {
		return fmt.Errorf("no se pudo habilitar smbd: %s", output)
	}
	if output, ok := smbCommand("systemctl", "start", "smbd"); !ok {
		return fmt.Errorf("no se pudo arrancar smbd: %s", output)
	}
	// nmbd only provides legacy NetBIOS discovery. SMB over TCP/445 remains
	// functional if the distro does not ship the unit.
	_, _ = smbCommand("systemctl", "enable", "nmbd")
	_, _ = smbCommand("systemctl", "start", "nmbd")
	openServicePorts("smb")
	return nil
}

func stopSMBService() error {
	if output, ok := smbCommand("systemctl", "stop", "smbd"); !ok {
		return fmt.Errorf("no se pudo detener smbd: %s", output)
	}
	_, _ = smbCommand("systemctl", "stop", "nmbd")
	_, _ = smbCommand("systemctl", "disable", "smbd", "nmbd")
	closeServicePorts("smb")
	return nil
}

func restartSMBService() error {
	if err := applySMBConfiguration(); err != nil {
		return err
	}
	if output, ok := smbCommand("systemctl", "restart", "smbd"); !ok {
		return fmt.Errorf("no se pudo reiniciar smbd: %s", output)
	}
	_, _ = smbCommand("systemctl", "restart", "nmbd")
	return nil
}

func handleSmbRoutes(w http.ResponseWriter, r *http.Request) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	urlPath := r.URL.Path
	method := r.Method

	if urlPath == "/api/smb/status" && method == http.MethodGet {
		_, installed := smbCommand("smbd", "--version")
		runningOut, _ := smbCommand("systemctl", "is-active", "smbd")
		version, _ := smbCommand("smbd", "--version")
		jsonOk(w, map[string]interface{}{
			"installed": installed,
			"running":   strings.TrimSpace(runningOut) == "active",
			"version":   version,
			"config":    readSMBConfig(),
			"port":      445,
		})
		return
	}

	if session.Role != "admin" {
		jsonError(w, http.StatusForbidden, "Admin required")
		return
	}

	switch {
	case urlPath == "/api/smb/config" && method == http.MethodPost:
		body, err := readBody(r)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "JSON inválido")
			return
		}
		config := SMBConfig{Workgroup: bodyStr(body, "workgroup"), ServerName: bodyStr(body, "serverString")}
		if err := saveSMBConfig(config); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := applySMBConfiguration(); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonOk(w, map[string]interface{}{"ok": true, "config": config})

	case urlPath == "/api/smb/start" && method == http.MethodPost:
		if err := startSMBService(); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonOk(w, map[string]interface{}{"ok": true})

	case urlPath == "/api/smb/stop" && method == http.MethodPost:
		if err := stopSMBService(); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonOk(w, map[string]interface{}{"ok": true})

	case urlPath == "/api/smb/restart" && method == http.MethodPost:
		if err := restartSMBService(); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonOk(w, map[string]interface{}{"ok": true})

	case urlPath == "/api/smb/apply" && method == http.MethodPost:
		if err := applySMBConfiguration(); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonOk(w, map[string]interface{}{"ok": true})

	case urlPath == "/api/smb/set-password" && method == http.MethodPost:
		body, err := readBody(r)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "JSON inválido")
			return
		}
		username := bodyStr(body, "username")
		password := bodyStr(body, "password")
		if username == "" || password == "" {
			jsonError(w, http.StatusBadRequest, "Username and password required")
			return
		}
		response := handleOp(Request{Op: "user.set_smb_password", Username: username, Password: password})
		if !response.Ok {
			jsonError(w, http.StatusInternalServerError, response.Error)
			return
		}
		jsonOk(w, map[string]interface{}{"ok": true})

	case smbShareRoute.MatchString(urlPath) && method == http.MethodPut:
		name := smbShareRoute.FindStringSubmatch(urlPath)[1]
		body, err := readBody(r)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "JSON inválido")
			return
		}
		enabled, ok := body["smb"].(bool)
		if !ok {
			jsonError(w, http.StatusBadRequest, "smb debe ser booleano")
			return
		}
		share, err := dbSharesGetRaw(name)
		if err != nil {
			jsonError(w, http.StatusNotFound, err.Error())
			return
		}
		if err := dbShareSetSMBEnabled(name, enabled); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := applySMBConfiguration(); err != nil {
			if rollbackErr := dbShareSetSMBEnabled(name, share.SMBEnabled); rollbackErr != nil {
				logMsg("SMB: rollback DB falló para %s: %v", name, rollbackErr)
			}
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonOk(w, map[string]interface{}{"ok": true, "name": name, "smb": enabled})

	default:
		jsonError(w, http.StatusNotFound, "Not found")
	}
}
