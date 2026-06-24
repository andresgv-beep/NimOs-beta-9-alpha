// apps.go — HTTP handlers de apps (Beta 8.1)
//
// Esta capa gestiona dos tipos de aplicaciones del usuario:
//
//   docker_apps   → containers Docker (Jellyfin, Plex, Sonarr...)
//   native_apps   → packages Linux (Samba, KVM, Transmission...)
//
// El estado vive en SQLite (tablas docker_apps + native_apps).
// El acceso a SQL va SIEMPRE a través de appsRepo · NUNCA SQL crudo aquí.
//
// El catálogo `knownNativeApps` permanece como CONFIGURACIÓN estática
// (security boundary: install/uninstall commands hardcoded), pero el
// ESTADO de instalación se persiste en la DB.
//
// Bootstrap: al arrancar el daemon, `bootstrapNativeApps` escanea el sistema
// y registra como `auto_detected=1` las apps que pasan el CheckCommand.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ═══════════════════════════════════════════════════════════════════════
// Constants
// ═══════════════════════════════════════════════════════════════════════

const (
	configDir = "/var/lib/nimos/config"
	nimosRoot = "/var/lib/nimos"
)

// ═══════════════════════════════════════════════════════════════════════
// Known native apps · catálogo de configuración (security boundary)
// ═══════════════════════════════════════════════════════════════════════
//
// El catálogo es HARDCODED por diseño: los install/uninstall commands
// se ejecutan como shell strings y NO pueden venir de input externo.
// La DB persiste sólo metadata (qué está instalado), nunca comandos.

type nativeAppDef struct {
	Name             string
	Description      string
	Category         string
	CheckCommand     string
	InstallCommand   string
	UninstallCommand string
	Port             int
	Icon             string
	Color            string
	IsNativeApp      bool
	IsDesktop        bool
	NimosApp         string
}

var knownNativeApps = map[string]nativeAppDef{
	"virtualization": {
		Name:             "Virtual Machines (KVM)",
		Description:      "Full virtualization with QEMU/KVM. Create and manage virtual machines.",
		Category:         "system",
		CheckCommand:     "which virsh 2>/dev/null && which qemu-system-x86_64 2>/dev/null",
		InstallCommand:   "sudo apt install -y qemu-kvm libvirt-daemon-system libvirt-clients bridge-utils virt-install virtinst && sudo systemctl enable libvirtd && sudo systemctl start libvirtd && sudo mkdir -p /var/lib/nimos/vms /var/lib/nimos/isos",
		UninstallCommand: "sudo apt remove -y qemu-kvm libvirt-daemon-system libvirt-clients virt-install virtinst",
		Port:             0,
		Icon:             "/app-icons/virtualization.svg",
		Color:            "#7C4DFF",
		IsNativeApp:      true,
		NimosApp:         "vms",
	},
	"transmission": {
		Name:             "Transmission",
		Description:      "Lightweight BitTorrent client with web interface and RPC API.",
		Category:         "downloads",
		CheckCommand:     "which transmission-daemon 2>/dev/null",
		InstallCommand:   "sudo apt install -y transmission-daemon && sudo systemctl stop transmission-daemon && sudo mkdir -p /etc/transmission-daemon /nimos/downloads && sudo usermod -a -G debian-transmission nimos 2>/dev/null; sudo systemctl enable transmission-daemon",
		UninstallCommand: "sudo systemctl stop transmission-daemon; sudo systemctl disable transmission-daemon; sudo apt remove -y transmission-daemon",
		Port:             9091,
		Icon:             "/app-icons/transmission.svg",
		Color:            "#B50D0D",
		IsNativeApp:      true,
		NimosApp:         "downloads",
	},
	"amule": {
		Name:             "aMule",
		Description:      "eMule-compatible P2P client for ed2k and Kademlia networks.",
		Category:         "downloads",
		CheckCommand:     "systemctl is-active amuled || which amuled 2>/dev/null",
		InstallCommand:   "sudo apt install -y amule-daemon amule-utils && sudo systemctl enable amuled 2>/dev/null",
		UninstallCommand: "sudo systemctl stop amuled; sudo apt remove -y amule-daemon amule-utils",
		Port:             4711,
		Icon:             "/app-icons/amule.svg",
		Color:            "#0078D4",
		IsNativeApp:      true,
	},
	"onlyoffice": {
		Name:         "OnlyOffice",
		CheckCommand: "which onlyoffice-desktopeditors || snap list onlyoffice-desktopeditors 2>/dev/null || ls /snap/bin/onlyoffice* 2>/dev/null || flatpak list 2>/dev/null | grep -i onlyoffice",
		Icon:         "/app-icons/onlyoffice.svg",
		Color:        "#FF6F3D",
		IsDesktop:    true,
	},
	"samba": {
		Name:           "Samba (SMB)",
		CheckCommand:   "systemctl is-active smbd",
		InstallCommand: "sudo apt install -y samba",
		Port:           445,
		Icon:           "📁",
		Color:          "#4A90A4",
	},
	"libreoffice": {
		Name:         "LibreOffice",
		CheckCommand: "which libreoffice || snap list libreoffice 2>/dev/null",
		Icon:         "/app-icons/libreoffice.svg",
		Color:        "#18A303",
		IsDesktop:    true,
	},
}

// ═══════════════════════════════════════════════════════════════════════
// detectNativeApp · runtime check (filesystem scan)
// ═══════════════════════════════════════════════════════════════════════

// detectNativeApp ejecuta el CheckCommand del catálogo para saber si una
// app native está instalada y/o corriendo. Es la fuente de verdad para
// "está instalado AHORA MISMO?" (la DB es el cache).
func detectNativeApp(appId string) (installed bool, running bool) {
	def, ok := knownNativeApps[appId]
	if !ok {
		return false, false
	}
	// SECURITY BOUNDARY: CheckCommand from hardcoded knownNativeApps only
	out, ok := runShellStatic(def.CheckCommand)
	if !ok {
		return false, false
	}
	isActive := out == "active" || len(out) > 0
	return true, isActive
}

// ═══════════════════════════════════════════════════════════════════════
// bootstrapNativeApps · escaneo al arranque
// ═══════════════════════════════════════════════════════════════════════
//
// Al arrancar el daemon escaneamos `knownNativeApps` y:
//   · Para cada CheckCommand que pasa → INSERT/UPDATE en native_apps
//     con auto_detected=1 y last_seen_at=now()
//   · Las apps que NO pasan el check pero estaban auto_detectadas antes
//     se eliminan con DeleteStaleAutoDetected (cutoff razonable: 5min)
//
// Esto refleja en la DB cualquier instalación/desinstalación que el user
// haya hecho manualmente vía apt/snap sin pasar por la UI.

func bootstrapNativeApps(ctx context.Context) {
	if appsRepo == nil {
		logMsg("apps: bootstrap skipped (appsRepo not initialized)")
		return
	}

	detected := 0
	scanStart := time.Now()

	for id, def := range knownNativeApps {
		installed, _ := detectNativeApp(id)
		if !installed {
			continue
		}

		app := &DBNativeApp{
			ID:           id,
			Name:         def.Name,
			Description:  def.Description,
			Category:     def.Category,
			Icon:         def.Icon,
			Color:        def.Color,
			Port:         def.Port,
			IsDesktop:    def.IsDesktop,
			IsNative:     def.IsNativeApp,
			NimosApp:     def.NimosApp,
			AutoDetected: true,
		}
		if err := appsRepo.CreateOrUpdateNativeApp(ctx, app); err != nil {
			logMsg("apps: bootstrap upsert %s failed: %v", id, err)
			continue
		}
		detected++
	}

	// Limpieza: apps autodetectadas que llevan más de 5 minutos sin verse
	// (probablemente el user las desinstaló manualmente vía apt remove).
	// Cutoff conservador: si la última detección fue antes del scanStart,
	// claramente está stale.
	staleAge := time.Since(scanStart) + 1*time.Minute
	removed, err := appsRepo.DeleteStaleAutoDetected(ctx, staleAge)
	if err != nil {
		logMsg("apps: bootstrap stale cleanup failed: %v", err)
	}

	logMsg("apps: bootstrap done · %d native apps detected · %d stale removed", detected, removed)
}

// ═══════════════════════════════════════════════════════════════════════
// Native Apps HTTP handlers
// ═══════════════════════════════════════════════════════════════════════

func handleNativeAppsRoutes(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method
	ctx := r.Context()

	// GET /api/native-apps · lista apps native instaladas
	if path == "/api/native-apps" && method == "GET" {
		session := requireAuth(w, r)
		if session == nil {
			return
		}

		installed, err := appsRepo.ListNativeApps(ctx)
		if err != nil {
			jsonError(w, 500, err.Error())
			return
		}

		// Para cada app instalada, comprobar si está corriendo (live check)
		result := make([]map[string]interface{}, 0, len(installed))
		for _, a := range installed {
			m := a.ToMap()
			_, running := detectNativeApp(a.ID)
			m["running"] = running
			result = append(result, m)
		}
		jsonOk(w, map[string]interface{}{"apps": result})
		return
	}

	// GET /api/native-apps/available · catálogo + estado instalado/running
	if path == "/api/native-apps/available" && method == "GET" {
		session := requireAuth(w, r)
		if session == nil {
			return
		}
		var available []map[string]interface{}
		for id, def := range knownNativeApps {
			installed, running := detectNativeApp(id)
			entry := map[string]interface{}{
				"id":          id,
				"name":        def.Name,
				"description": def.Description,
				"category":    def.Category,
				"icon":        def.Icon,
				"color":       def.Color,
				"installed":   installed,
				"running":     running,
				"isDesktop":   def.IsDesktop,
				"isNativeApp": def.IsNativeApp,
			}
			if def.Port > 0 {
				entry["port"] = def.Port
			}
			if def.InstallCommand != "" {
				entry["installCommand"] = def.InstallCommand
			}
			if def.UninstallCommand != "" {
				entry["uninstallCommand"] = def.UninstallCommand
			}
			if def.NimosApp != "" {
				entry["nimosApp"] = def.NimosApp
			}
			if def.Category == "" {
				entry["category"] = "system"
			}
			available = append(available, entry)
		}
		jsonOk(w, map[string]interface{}{"apps": available})
		return
	}

	// Regex routes: /api/native-apps/:id/action
	appActionRegex := regexp.MustCompile(`^/api/native-apps/([a-zA-Z0-9_-]+)/(start|stop|install|install-status|uninstall|status)$`)
	matches := appActionRegex.FindStringSubmatch(path)
	if matches == nil {
		jsonError(w, 404, "Not found")
		return
	}

	appId := matches[1]
	action := matches[2]

	switch action {
	case "start":
		nativeAppStart(w, r, appId)
	case "stop":
		nativeAppStop(w, r, appId)
	case "install":
		nativeAppInstall(w, r, appId)
	case "install-status":
		nativeAppInstallStatus(w, r, appId)
	case "uninstall":
		nativeAppUninstall(w, r, appId)
	case "status":
		nativeAppStatus(w, r, appId)
	}
}

func nativeAppStart(w http.ResponseWriter, r *http.Request, appId string) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	if _, ok := knownNativeApps[appId]; !ok {
		jsonError(w, 404, "Unknown app")
		return
	}
	// Probar múltiples patrones de service name — no shell needed
	patterns := []string{appId + "-daemon", appId + "d", appId}
	started := false
	for _, svc := range patterns {
		if _, ok := runSafe("sudo", "systemctl", "start", svc); ok {
			started = true
			break
		}
	}
	if !started {
		jsonError(w, 500, "Failed to start service")
		return
	}
	jsonOk(w, map[string]interface{}{"ok": true, "appId": appId})
}

func nativeAppStop(w http.ResponseWriter, r *http.Request, appId string) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	if _, ok := knownNativeApps[appId]; !ok {
		jsonError(w, 404, "Unknown app")
		return
	}
	for _, svc := range []string{appId + "-daemon", appId + "d", appId} {
		runSafe("sudo", "systemctl", "stop", svc)
	}
	jsonOk(w, map[string]interface{}{"ok": true, "appId": appId})
}

func nativeAppInstall(w http.ResponseWriter, r *http.Request, appId string) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	def, ok := knownNativeApps[appId]
	if !ok {
		jsonError(w, 404, "Unknown app")
		return
	}
	if def.InstallCommand == "" {
		jsonError(w, 400, "No install command defined")
		return
	}

	logDir := "/var/log/nimos"
	os.MkdirAll(logDir, 0755)
	statusFile := filepath.Join(logDir, fmt.Sprintf("install-%s.json", appId))

	// Mark as installing
	statusData, _ := json.Marshal(map[string]interface{}{
		"status":    "installing",
		"appId":     appId,
		"startedAt": time.Now().UTC().Format(time.RFC3339),
	})
	os.WriteFile(statusFile, statusData, 0644)

	// Capturar el ID para la goroutine asíncrona
	appIdCapture := appId
	defCapture := def

	go func() {
		ctx := context.Background()
		logFile := filepath.Join(logDir, fmt.Sprintf("install-%s.log", appIdCapture))

		// SECURITY BOUNDARY: InstallCommand from hardcoded knownNativeApps only.
		// Shell is used because install commands chain apt + systemctl + mkdir.
		out, err := exec.Command("bash", "-c", defCapture.InstallCommand).CombinedOutput()
		os.WriteFile(logFile, out, 0644)

		if err == nil {
			// Registrar en native_apps · marca explícita como NO auto-detected
			// (el user la instaló desde la UI, no se autodetectó)
			app := &DBNativeApp{
				ID:           appIdCapture,
				Name:         defCapture.Name,
				Description:  defCapture.Description,
				Category:     defCapture.Category,
				Icon:         defCapture.Icon,
				Color:        defCapture.Color,
				Port:         defCapture.Port,
				IsDesktop:    defCapture.IsDesktop,
				IsNative:     defCapture.IsNativeApp,
				NimosApp:     defCapture.NimosApp,
				AutoDetected: false,
			}
			if dbErr := appsRepo.CreateOrUpdateNativeApp(ctx, app); dbErr != nil {
				logMsg("apps: install registration failed for %s: %v", appIdCapture, dbErr)
			}

			statusData, _ := json.Marshal(map[string]interface{}{
				"status": "done",
				"appId":  appIdCapture,
				"code":   0,
			})
			os.WriteFile(statusFile, statusData, 0644)
		} else {
			statusData, _ := json.Marshal(map[string]interface{}{
				"status": "error",
				"appId":  appIdCapture,
				"code":   1,
			})
			os.WriteFile(statusFile, statusData, 0644)
		}
	}()

	jsonOk(w, map[string]interface{}{
		"ok":      true,
		"appId":   appId,
		"async":   true,
		"message": "Installation started",
	})
}

func nativeAppInstallStatus(w http.ResponseWriter, r *http.Request, appId string) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}
	statusFile := filepath.Join("/var/log/nimos", fmt.Sprintf("install-%s.json", appId))
	data, err := os.ReadFile(statusFile)
	if err != nil {
		jsonOk(w, map[string]interface{}{"status": "unknown"})
		return
	}
	var status map[string]interface{}
	json.Unmarshal(data, &status)
	jsonOk(w, status)
}

func nativeAppUninstall(w http.ResponseWriter, r *http.Request, appId string) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	def, ok := knownNativeApps[appId]
	if !ok {
		jsonError(w, 404, "Unknown app")
		return
	}
	if def.UninstallCommand != "" {
		// SECURITY BOUNDARY: UninstallCommand from hardcoded knownNativeApps only
		if _, ok := runShellStatic(def.UninstallCommand); !ok {
			jsonError(w, 500, "Uninstall failed")
			return
		}
	}

	// Eliminar de native_apps en la DB
	// commitContext() · la app ya está desinstalada del sistema, la BD debe
	// reflejarlo aunque el cliente se haya ido.
	if err := appsRepo.DeleteNativeApp(commitContext(), appId); err != nil {
		logMsg("apps: uninstall DB cleanup failed for %s: %v", appId, err)
		// No abortamos · la app ya está desinstalada del sistema
	}

	jsonOk(w, map[string]interface{}{"ok": true, "appId": appId})
}

func nativeAppStatus(w http.ResponseWriter, r *http.Request, appId string) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}
	def, ok := knownNativeApps[appId]
	if !ok {
		jsonError(w, 404, "Unknown app")
		return
	}
	installed, running := detectNativeApp(appId)
	result := map[string]interface{}{
		"id":        appId,
		"name":      def.Name,
		"installed": installed,
		"running":   running,
	}
	if def.Port > 0 {
		result["port"] = def.Port
	}
	jsonOk(w, result)
}

// ═══════════════════════════════════════════════════════════════════════
// Installed Apps (Docker) HTTP handlers
// ═══════════════════════════════════════════════════════════════════════

func handleInstalledAppsRoutes(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method
	ctx := r.Context()

	// GET /api/installed-apps · lista apps Docker registradas
	//
	// APP-010 · DEPRECATED desde Beta 8.1.x.
	// El frontend nuevo debe leer /api/services y filtrar por type="docker-app",
	// que devuelve el cruce completo registry + docker ps con estados precisos.
	// Mantenido para compat con clientes pre-Beta-8.
	if path == "/api/installed-apps" && method == "GET" {
		session := requireAuth(w, r)
		if session == nil {
			return
		}
		// APP-010 · headers RFC 8594 de deprecación
		w.Header().Set("Deprecation", "true")
		w.Header().Set("Link", `</api/services>; rel="successor-version"`)

		apps, err := appsRepo.ListDockerApps(ctx)
		if err != nil {
			jsonError(w, 500, err.Error())
			return
		}
		// Serializar como []map para mantener contrato con frontend
		result := make([]map[string]interface{}, 0, len(apps))
		for _, a := range apps {
			result = append(result, a.ToMap())
		}
		jsonOk(w, result)
		return
	}

	// POST /api/installed-apps · registra una app Docker
	if path == "/api/installed-apps" && method == "POST" {
		session := requireAdmin(w, r)
		if session == nil {
			return
		}
		body, _ := readBody(r)
		appId := bodyStr(body, "id")
		if appId == "" {
			jsonError(w, 400, "App ID required")
			return
		}

		iconPath := bodyStr(body, "icon")
		if iconPath == "" {
			iconPath = "📦"
		}
		// Descargar icon si viene como URL externa
		if strings.HasPrefix(iconPath, "http") {
			localPath := downloadAppIcon(appId, iconPath)
			if localPath != "" {
				iconPath = localPath
			}
		}

		// Resolver openMode (preferir explícito, fallback a external bool)
		openMode := "internal"
		if om, ok := body["openMode"].(string); ok && om != "" {
			openMode = om
		} else if ext, ok := body["external"].(bool); ok && ext {
			openMode = "external"
		}

		// Resolver port
		port := 0
		if p, ok := body["port"].(float64); ok {
			port = int(p)
		}

		app := &DBDockerApp{
			ID:          appId,
			Name:        bodyStr(body, "name"),
			Icon:        iconPath,
			Image:       bodyStr(body, "image"),
			Color:       bodyStr(body, "color"),
			Type:        bodyStr(body, "type"),
			OpenMode:    openMode,
			Port:        port,
			InstalledBy: session.Username,
		}

		if err := appsRepo.CreateOrUpdateDockerApp(ctx, app); err != nil {
			jsonError(w, 500, err.Error())
			return
		}
		jsonOk(w, map[string]interface{}{"ok": true})
		return
	}

	// POST /api/installed-apps/:id/access-mode · SHIELD-P2 candado de puerto
	accessModeRegex := regexp.MustCompile(`^/api/installed-apps/([a-zA-Z0-9_.-]+)/access-mode$`)
	if matches := accessModeRegex.FindStringSubmatch(path); matches != nil && method == "POST" {
		handleAppAccessMode(w, r, matches[1])
		return
	}

	// DELETE /api/installed-apps/:id
	appDelRegex := regexp.MustCompile(`^/api/installed-apps/([a-zA-Z0-9_.-]+)$`)
	if matches := appDelRegex.FindStringSubmatch(path); matches != nil && method == "DELETE" {
		session := requireAdmin(w, r)
		if session == nil {
			return
		}
		appId := matches[1]
		if err := appsRepo.DeleteDockerApp(ctx, appId); err != nil {
			jsonError(w, 500, err.Error())
			return
		}
		jsonOk(w, map[string]interface{}{"ok": true})
		return
	}

	jsonError(w, 404, "Not found")
}

// ═══════════════════════════════════════════════════════════════════════
// downloadAppIcon · descarga icon desde URL externa
// ═══════════════════════════════════════════════════════════════════════

func downloadAppIcon(appId, iconUrl string) string {
	// Validar URL para prevenir command injection
	if !strings.HasPrefix(iconUrl, "http://") && !strings.HasPrefix(iconUrl, "https://") {
		return ""
	}
	// Rechazar URLs con caracteres shell-peligrosos
	if strings.ContainsAny(iconUrl, "\"'`$;|&<>(){}\\") {
		return ""
	}

	for _, dir := range []string{
		"/opt/nimos/public/app-icons",
		filepath.Join(nimosRoot, "app-icons"),
	} {
		os.MkdirAll(dir, 0755)
		ext := ".png"
		if strings.Contains(iconUrl, ".svg") {
			ext = ".svg"
		} else if strings.Contains(iconUrl, ".jpg") || strings.Contains(iconUrl, ".jpeg") {
			ext = ".jpg"
		} else if strings.Contains(iconUrl, ".webp") {
			ext = ".webp"
		}
		localPath := filepath.Join(dir, appId+ext)
		// exec.Command sin shell interpolation para evitar inyección
		cmd := exec.Command("curl", "-sL", "-o", localPath, "--max-time", "15", iconUrl)
		if err := cmd.Run(); err == nil {
			return "/app-icons/" + appId + ext
		}
	}
	return ""
}
