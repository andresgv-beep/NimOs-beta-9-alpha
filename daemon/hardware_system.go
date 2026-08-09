package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func getContainers() []interface{} {
	if !hasDocker {
		return []interface{}{}
	}
	containerCacheMu.Lock()
	defer containerCacheMu.Unlock()

	now := time.Now().UnixMilli()
	if containerCache != nil && (now-containerCacheTime) < 5000 {
		return containerCache
	}

	raw, ok := runSafe("docker", "ps", "-a", "--format", "{{.ID}}|{{.Names}}|{{.Image}}|{{.Status}}|{{.Ports}}|{{.State}}|{{.CreatedAt}}")
	if !ok || raw == "" {
		return []interface{}{}
	}

	var containers []interface{}
	for _, line := range strings.Split(raw, "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 7)
		if len(parts) < 6 {
			continue
		}
		ports := "—"
		if len(parts) > 4 && parts[4] != "" {
			ports = parts[4]
		}
		c := map[string]interface{}{
			"id": parts[0], "name": parts[1], "image": parts[2],
			"status": parts[3], "ports": ports, "state": parts[5],
		}
		if len(parts) > 6 {
			c["created"] = parts[6]
		}
		containers = append(containers, c)
	}

	// docker stats
	if stats, ok := runSafe("docker", "stats", "--no-stream", "--format", "{{.Name}}|{{.CPUPerc}}|{{.MemUsage}}|{{.MemPerc}}"); ok && stats != "" {
		statMap := map[string][3]string{}
		for _, line := range strings.Split(stats, "\n") {
			p := strings.SplitN(line, "|", 4)
			if len(p) >= 4 {
				statMap[p[0]] = [3]string{p[1], p[2], p[3]}
			}
		}
		for _, c := range containers {
			cm := c.(map[string]interface{})
			if s, ok := statMap[cm["name"].(string)]; ok {
				cm["cpu"] = s[0]
				cm["mem"] = s[1]
				cm["memPct"] = s[2]
			} else {
				cm["cpu"] = "—"
				cm["mem"] = "—"
				cm["memPct"] = "—"
			}
		}
	}

	if containers == nil {
		containers = []interface{}{}
	}
	containerCache = containers
	containerCacheTime = now
	return containers
}

func containerAction(id, action string) map[string]interface{} {
	allowed := map[string]bool{"start": true, "stop": true, "restart": true, "pause": true, "unpause": true}
	if !allowed[action] {
		return map[string]interface{}{"error": "Invalid action"}
	}
	// Sanitize
	re := regexp.MustCompile(`[^a-zA-Z0-9_.\-/:]+`)
	safeId := re.ReplaceAllString(id, "")
	if safeId == "" || len(safeId) > 256 || strings.Contains(safeId, "..") {
		return map[string]interface{}{"error": "Invalid container ID"}
	}
	out, _ := runSafe("docker", action, safeId)
	return map[string]interface{}{"ok": true, "action": action, "id": safeId, "output": out}
}

func getSystemSummary() map[string]interface{} {
	systemCacheMu.Lock()
	defer systemCacheMu.Unlock()

	now := time.Now().UnixMilli()
	if systemCache != nil && (now-systemCacheTime) < 1500 {
		return systemCache
	}

	cpu := getCpuUsage()
	mem := getMemory()
	gpus := getGpu()
	temps := getTemps(gpus)
	network := getNetwork()
	diskInfo := getDisks()
	uptime := getUptime()

	hostname, _ := os.Hostname()

	// Main temp
	mainTemp := pickMainTemp(temps)

	// Primary network interface
	var primaryNet interface{}
	for _, n := range network {
		ip, _ := n["ip"].(string)
		status, _ := n["status"].(string)
		if ip != "—" && status == "up" {
			primaryNet = n
			break
		}
	}
	if primaryNet == nil && len(network) > 0 {
		primaryNet = network[0]
	}

	uname, _ := runSafe("uname", "-sr")

	systemCache = map[string]interface{}{
		"cpu":        cpu,
		"memory":     mem,
		"gpus":       gpus,
		"temps":      temps,
		"mainTemp":   mainTemp,
		"network":    network,
		"primaryNet": primaryNet,
		"disks":      diskInfo,
		"uptime":     uptime,
		"hostname":   hostname,
		"platform":   uname,
		"arch":       runtimeArch(), // "amd64"|"arm64"|... · para filtrar el catálogo por compatibilidad
	}
	systemCacheTime = now
	return systemCache
}

func handleHardwareRoutes(w http.ResponseWriter, r *http.Request) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}

	path := r.URL.Path
	switch path {
	case "/api/hardware/stats":
		// Combined stats for NimHealth dashboard
		cpuData := getCpuUsage()
		memData := getMemory()
		diskData := getDiskIO()
		netData := getNetworkAggregate()
		loadStr := ""
		if loadAvg := readFileStr("/proc/loadavg"); loadAvg != "" {
			parts := strings.Fields(loadAvg)
			if len(parts) > 0 {
				loadStr = parts[0]
			}
		}
		cpuData["load1"] = parseFloat(loadStr)
		// temp/uptime para el widget SysPanel 2×2. getTemps con slice
		// vacío (no nil) salta el lookup de GPU: el panel solo necesita
		// CPU y esto corre cada 3s.
		jsonOk(w, map[string]interface{}{
			"cpu":     cpuData,
			"memory":  memData,
			"disk":    diskData,
			"network": netData,
			"temp":    pickMainTemp(getTemps([]map[string]interface{}{})),
			"uptime":  getUptime(),
		})
	case "/api/system":
		jsonOk(w, getSystemSummary())
	case "/api/cpu":
		jsonOk(w, getCpuUsage())
	case "/api/memory":
		jsonOk(w, getMemory())
	case "/api/gpu":
		jsonOk(w, getGpu())
	case "/api/temps":
		jsonOk(w, getTemps(nil))
	case "/api/network":
		jsonOk(w, getNetwork())
	case "/api/disks":
		jsonOk(w, getDisks())
	case "/api/disks/smart":
		disk := r.URL.Query().Get("disk")
		if disk == "" {
			jsonError(w, 400, "Provide disk name (e.g. ?disk=sda)")
			return
		}
		jsonOk(w, getDiskSmart(disk))
	case "/api/disks/smart/summary":
		jsonOk(w, getSmartSummary())
	case "/api/uptime":
		jsonOk(w, map[string]interface{}{"uptime": getUptime()})
	case "/api/containers":
		jsonOk(w, getContainers())
	case "/api/hostname":
		h, _ := os.Hostname()
		jsonOk(w, map[string]interface{}{"hostname": h})
	case "/api/hardware/gpu-info":
		jsonOk(w, getHardwareGpuInfo())
	case "/api/system/info":
		handleSystemInfo(w)
	case "/api/system/update/check":
		handleUpdateCheck(w)
	case "/api/system/update/status":
		handleUpdateStatus(w)
	case "/api/system/reboot", "/api/system/shutdown", "/api/system/reboot-service", "/api/system/update/apply", "/api/terminal":
		// These are POST-only admin routes — reject GET and non-admin
		if r.Method != "POST" {
			jsonError(w, 405, "Method not allowed")
			return
		}
		handleSystemPost(w, r, session)
	default:
		// POST routes need body
		if r.Method == "POST" {
			handleSystemPost(w, r, session)
			return
		}
		jsonError(w, 404, "Not found")
	}
}

func handleSystemPost(w http.ResponseWriter, r *http.Request, session *DBSession) {
	if session.Role != "admin" {
		jsonError(w, 403, "Unauthorized")
		return
	}

	path := r.URL.Path
	switch path {
	case "/api/system/reboot-service":
		jsonOk(w, map[string]interface{}{"ok": true, "message": "NimOS restarting..."})
		go func() {
			time.Sleep(1 * time.Second)
			runSafe("sudo", "systemctl", "restart", "nimos")
		}()
	case "/api/system/reboot":
		jsonOk(w, map[string]interface{}{"ok": true, "message": "System rebooting..."})
		go func() {
			time.Sleep(1 * time.Second)
			runSafe("sudo", "reboot")
		}()
	case "/api/system/shutdown":
		jsonOk(w, map[string]interface{}{"ok": true, "message": "System shutting down..."})
		go func() {
			time.Sleep(1 * time.Second)
			runSafe("sudo", "shutdown", "-h", "now")
		}()
	case "/api/system/update/apply":
		handleUpdateApply(w)
	case "/api/terminal":
		handleTerminal(w, r, session)
	default:
		jsonError(w, 404, "Not found")
	}
}

const updatePackageURL = "https://raw.githubusercontent.com/andresgv-beep/NimOs-beta-9-alpha/main/package.json"

var semverPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$`)

type parsedSemver struct {
	major, minor, patch int
	pre                 string
}

func parseSemver(value string) (parsedSemver, bool) {
	match := semverPattern.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return parsedSemver{}, false
	}
	major, errMajor := strconv.Atoi(match[1])
	minor, errMinor := strconv.Atoi(match[2])
	patch, errPatch := strconv.Atoi(match[3])
	if errMajor != nil || errMinor != nil || errPatch != nil {
		return parsedSemver{}, false
	}
	return parsedSemver{major: major, minor: minor, patch: patch, pre: match[4]}, true
}

func comparePrerelease(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return 1
	}
	if b == "" {
		return -1
	}
	aParts, bParts := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(aParts) && i < len(bParts); i++ {
		if aParts[i] == bParts[i] {
			continue
		}
		aNum, aErr := strconv.Atoi(aParts[i])
		bNum, bErr := strconv.Atoi(bParts[i])
		switch {
		case aErr == nil && bErr == nil:
			if aNum < bNum {
				return -1
			}
			return 1
		case aErr == nil:
			return -1
		case bErr == nil:
			return 1
		case aParts[i] < bParts[i]:
			return -1
		default:
			return 1
		}
	}
	if len(aParts) < len(bParts) {
		return -1
	}
	return 1
}

// compareSemver devuelve -1 si a < b, 0 si son equivalentes y 1 si a > b.
func compareSemver(a, b string) (int, bool) {
	va, okA := parseSemver(a)
	vb, okB := parseSemver(b)
	if !okA || !okB {
		return 0, false
	}
	for _, pair := range [][2]int{{va.major, vb.major}, {va.minor, vb.minor}, {va.patch, vb.patch}} {
		if pair[0] < pair[1] {
			return -1, true
		}
		if pair[0] > pair[1] {
			return 1, true
		}
	}
	return comparePrerelease(va.pre, vb.pre), true
}

func packageVersion(data []byte) (string, error) {
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "", err
	}
	if _, ok := parseSemver(pkg.Version); !ok {
		return "", fmt.Errorf("invalid version %q", pkg.Version)
	}
	return pkg.Version, nil
}

func handleUpdateCheck(w http.ResponseWriter) {
	data, err := os.ReadFile("/opt/nimos/package.json")
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "No se pudo leer la versión instalada")
		return
	}
	currentVersion, err := packageVersion(data)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "La versión instalada no es válida")
		return
	}

	out, ok := runSafe("curl", "--connect-timeout", "5", "--max-time", "15", "-fsSL", updatePackageURL)
	if !ok || strings.TrimSpace(out) == "" {
		jsonError(w, http.StatusBadGateway, "No se pudo consultar el servidor de actualizaciones")
		return
	}
	latestVersion, err := packageVersion([]byte(out))
	if err != nil {
		jsonError(w, http.StatusBadGateway, "El servidor devolvió una versión no válida")
		return
	}
	comparison, ok := compareSemver(currentVersion, latestVersion)
	if !ok {
		jsonError(w, http.StatusInternalServerError, "No se pudieron comparar las versiones")
		return
	}
	// Si falta esta marca, una actualización anterior copió el código pero no
	// llegó a generar el frontend. Permitimos repararlo aun con igual versión.
	_, markerErr := os.Stat("/opt/nimos/dist/.nimos-build")
	repairRequired := markerErr != nil
	jsonOk(w, map[string]interface{}{
		"currentVersion":  currentVersion,
		"latestVersion":   latestVersion,
		"updateAvailable": comparison < 0 || repairRequired,
		"repairRequired":  repairRequired,
		"installDir":      "/opt/nimos",
	})
}

func handleUpdateApply(w http.ResponseWriter) {
	script := "/opt/nimos/scripts/update.sh"
	os.MkdirAll("/opt/nimos/scripts", 0755)
	os.MkdirAll("/var/log/nimos", 0755)

	// Si no existe el script, descargarlo de GitHub
	if _, err := os.Stat(script); err != nil {
		logMsg("update.sh not found, downloading from GitHub...")
		// SECURITY: Download update script safely (no shell interpolation)
		_, ok := runSafe("curl", "-fsSL",
			"https://raw.githubusercontent.com/andresgv-beep/NimOs-beta-9-alpha/main/scripts/update.sh",
			"-o", "/opt/nimos/scripts/update.sh")
		if !ok {
			// curl returns "" on success sometimes
		}
		// Download checksum file for verification
		checksumOut, csOk := runSafe("curl", "-fsSL",
			"https://raw.githubusercontent.com/andresgv-beep/NimOs-beta-9-alpha/main/scripts/update.sh.sha256")
		if csOk && checksumOut != "" {
			// Verify checksum: expected format "sha256hash  filename" or just hash
			expectedHash := strings.Fields(checksumOut)[0]
			actualHash, hashOk := runSafe("sha256sum", script)
			if hashOk {
				actualHashStr := strings.Fields(actualHash)[0]
				if actualHashStr != expectedHash {
					os.Remove(script)
					logMsg("SECURITY: update.sh checksum mismatch! Expected %s, got %s", expectedHash, actualHashStr)
					jsonError(w, 500, "Update script checksum verification failed")
					return
				}
				logMsg("update.sh checksum verified OK")
			}
		} else {
			logMsg("WARNING: No checksum file available for update.sh — proceeding without verification")
		}
		if err2 := os.Chmod(script, 0755); err2 != nil {
			jsonError(w, 500, "Failed to download update script")
			return
		}
	}

	// Verificar que ahora existe
	if _, err := os.Stat(script); err != nil {
		jsonError(w, 400, "Update script not found and could not be downloaded")
		return
	}

	// Ejecutar una copia estable: el propio update reemplaza /opt/nimos/scripts
	// al instalar el archivo descargado. Si Bash mantuviera abierto ese mismo
	// inode, podría leer un script truncado o mezclado a mitad de ejecución.
	scriptData, err := os.ReadFile(script)
	if err != nil {
		jsonError(w, 500, "Failed to read update script")
		return
	}
	runCopy, err := os.CreateTemp("/var/tmp", "nimos-update-run-*.sh")
	if err != nil {
		jsonError(w, 500, "Failed to prepare update script")
		return
	}
	runPath := runCopy.Name()
	if _, err = runCopy.Write(scriptData); err != nil {
		runCopy.Close()
		os.Remove(runPath)
		jsonError(w, 500, "Failed to prepare update script")
		return
	}
	if err = runCopy.Close(); err != nil || os.Chmod(runPath, 0700) != nil {
		os.Remove(runPath)
		jsonError(w, 500, "Failed to prepare update script")
		return
	}

	os.Remove("/var/log/nimos/update-result.json")
	lockPath := "/run/nimos-update.lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		os.Remove(runPath)
		if os.IsExist(err) {
			jsonError(w, http.StatusConflict, "Ya hay una actualización en curso")
			return
		}
		jsonError(w, 500, "Failed to lock update process")
		return
	}
	lockFile.Close()

	// setsid no separa un proceso del cgroup de systemd. El updater debe vivir en
	// una unidad transitoria propia; de lo contrario, al parar nimos-daemon,
	// systemd mata también la actualización y deja el equipo a medias.
	unitName := fmt.Sprintf("nimos-update-%d", time.Now().UnixNano())
	cmd := exec.Command("systemd-run",
		"--unit="+unitName,
		"--collect",
		"--no-block",
		"--working-directory=/opt/nimos",
		"/bin/bash", "-c",
		`bash "$1"; status=$?; rm -f -- "$1" "$2"; exit "$status"`,
		"nimos-update-runner", runPath, lockPath)
	logFile, err := os.OpenFile("/var/log/nimos/update.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	// --no-block hace que systemd-run vuelva en cuanto la unidad queda creada;
	// esperamos ese resultado breve para no afirmar que arrancó si fue rechazada.
	if err := cmd.Run(); err != nil {
		if logFile != nil {
			logFile.Close()
		}
		os.Remove(runPath)
		os.Remove(lockPath)
		jsonError(w, 500, fmt.Sprintf("Failed to start update: %v", err))
		return
	}
	if logFile != nil {
		logFile.Close()
	}
	jsonOk(w, map[string]interface{}{"ok": true, "message": "Update started.", "unit": unitName})
}

func handleUpdateStatus(w http.ResponseWriter) {
	rf := "/var/log/nimos/update-result.json"
	if data, err := os.ReadFile(rf); err == nil {
		var result map[string]interface{}
		if json.Unmarshal(data, &result) == nil {
			result["done"] = true
			jsonOk(w, result)
			return
		}
	}
	jsonOk(w, map[string]interface{}{"done": false})
}

// WARNING: Admin-level RCE endpoint — intentional privileged shell access.
// El terminal ES una shell root para el admin, sin filtros: un blocklist de
// "comandos peligrosos" aquí sería teatro (trivial de saltar con espacios,
// variables, busybox…) y solo daría falsa sensación de seguridad. Las defensas
// reales son: requireAdmin (doble check en handleSystemPost), opt-in explícito
// vía security.json (disabled-by-default, ver isTerminalEnabled) y audit log
// de cada comando.
func handleTerminal(w http.ResponseWriter, r *http.Request, session *DBSession) {
	// SECURITY: Terminal must be explicitly enabled via config
	if !isTerminalEnabled() {
		jsonError(w, 403, "Terminal is disabled in system configuration")
		return
	}

	body, _ := readBody(r)
	cmd := bodyStr(body, "cmd")
	cwd := bodyStr(body, "cwd")
	if cmd == "" || len(cmd) > 4096 {
		jsonError(w, 400, "Invalid cmd (max 4096 chars)")
		return
	}

	if cwd == "" {
		cwd = "/root"
	}

	// SECURITY: Validate cwd is a real absolute path (no injection via quotes)
	cleanCwd := filepath.Clean(cwd)
	if !filepath.IsAbs(cleanCwd) {
		cleanCwd = "/root"
	}

	// SECURITY: Audit log EVERY terminal command with session info
	username := session.Username
	ip := r.RemoteAddr
	logMsg("TERMINAL [user=%s ip=%s cwd=%s]: %s", username, ip, cleanCwd, cmd)

	// Execute: use sh -c for the command but set WorkDir safely
	c := exec.Command("sh", "-c", cmd)
	c.Dir = cleanCwd
	out, err := c.CombinedOutput()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		}
	}
	jsonOk(w, map[string]interface{}{"stdout": strings.TrimSpace(string(out)), "stderr": "", "code": code, "cwd": cleanCwd})
}

// isTerminalEnabled checks if terminal access is enabled in config.
// SECURITY: fail-closed. El terminal da shell root vía HTTP; en un NAS
// expuesto no puede venir activado de serie ni activarse solo porque el
// config falte o esté corrupto. Opt-in explícito:
//
//	echo '{"terminalEnabled": true}' > /var/lib/nimos/config/security.json
func isTerminalEnabled() bool {
	data, err := os.ReadFile("/var/lib/nimos/config/security.json")
	if err != nil {
		return false // default: disabled (fail-closed)
	}
	var conf map[string]interface{}
	if json.Unmarshal(data, &conf) != nil {
		return false
	}
	if enabled, ok := conf["terminalEnabled"].(bool); ok {
		return enabled
	}
	return false
}

func handleSystemInfo(w http.ResponseWriter) {
	interfaces := getNetwork()
	hostname, _ := os.Hostname()
	gateway, _ := runShellStatic("ip route | grep default | awk '{print $3}' | head -1")
	if gateway == "" {
		gateway = "—"
	}
	dnsOut, _ := runShellStatic("cat /etc/resolv.conf 2>/dev/null | grep nameserver | awk '{print $2}'")
	var dnsServers []string
	for _, s := range strings.Split(dnsOut, "\n") {
		if s != "" {
			dnsServers = append(dnsServers, s)
		}
	}
	if dnsServers == nil {
		dnsServers = []string{}
	}

	// Find primary interface name
	primaryName := "eth0"
	for _, n := range interfaces {
		if ip, _ := n["ip"].(string); ip != "—" {
			primaryName, _ = n["name"].(string)
			break
		}
	}
	subnetOut, _ := runSafe("ip", "-4", "-o", "addr", "show", primaryName)
	subnet := ""
	for _, line := range strings.Split(subnetOut, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 {
			subnet = fields[3]
			break
		}
	}
	if subnet == "" {
		subnet = "—"
	}

	jsonOk(w, map[string]interface{}{
		"network": map[string]interface{}{
			"hostname":   hostname,
			"gateway":    gateway,
			"subnet":     subnet,
			"dns":        dnsServers,
			"interfaces": interfaces,
		},
	})
}
