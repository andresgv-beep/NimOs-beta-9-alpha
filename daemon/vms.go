package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// ═══════════════════════════════════
// VMs (QEMU/KVM) HTTP handlers
// ═══════════════════════════════════
//
// NOTA: VMs no es parte del módulo network. Su registro vivía en el
// antiguo network.go; al retirar ese archivo, el registro se trae aquí.
// Pendiente: mover a un módulo vms/ propio cuando se aborde su sprint.

const (
	vmDir  = "/var/lib/nimos/vms"
	isoDir = "/var/lib/nimos/isos"
)

// registerVMsRoutes registra los endpoints de gestión de máquinas virtuales.
func registerVMsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/vms/", handleVMsRoutes)
}

func handleVMsRoutes(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}

	urlPath := r.URL.Path
	method := r.Method

	switch {
	case urlPath == "/api/vms/status" && method == "GET":
		vmsStatus(w)
	case urlPath == "/api/vms/list" && method == "GET":
		vmsList(w)
	case urlPath == "/api/vms/overview" && method == "GET":
		vmsOverview(w)
	case urlPath == "/api/vms/create" && method == "POST":
		vmsCreate(w, r, session)
	case urlPath == "/api/vms/action" && method == "POST":
		vmsAction(w, r, session)
	case urlPath == "/api/vms/isos" && method == "GET":
		vmsIsos(w)
	case urlPath == "/api/vms/networks" && method == "GET":
		vmsNetworks(w)
	case strings.HasPrefix(urlPath, "/api/vms/vnc/") && method == "GET":
		vmsVnc(w, r)
	case urlPath == "/api/vms/logs" && method == "GET":
		vmsLogs(w)
	case urlPath == "/api/vms/snapshot" && method == "POST":
		vmsSnapshot(w, r, session)
	default:
		jsonError(w, 404, "Not found")
	}
}

func vmsStatus(w http.ResponseWriter) {
	_, virshOk := runSafe("which", "virsh")
	_, qemuOk := runSafe("which", "qemu-system-x86_64")
	kvmCount, _ := runShellStatic(`grep -Ec "(vmx|svm)" /proc/cpuinfo 2>/dev/null`)
	_, kvmLoaded := runShellStatic("lsmod 2>/dev/null | grep kvm")
	libvirtStatus, _ := runSafe("systemctl", "is-active", "libvirtd")
	version, _ := runSafe("virsh", "version", "--daemon")

	os.MkdirAll(vmDir, 0755)
	os.MkdirAll(isoDir, 0755)

	jsonOk(w, map[string]interface{}{
		"installed":       virshOk && qemuOk,
		"kvmSupport":      parseIntDefault(strings.TrimSpace(kvmCount), 0) > 0,
		"kvmLoaded":       kvmLoaded,
		"libvirtdRunning": strings.TrimSpace(libvirtStatus) == "active",
		"version":         version,
	})
}

func vmsList(w http.ResponseWriter) {
	raw, _ := runSafe("virsh", "list", "--all")
	var vms []map[string]interface{}

	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "Id") || strings.HasPrefix(line, "---") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}

		var id interface{}
		name := parts[1]
		status := strings.Join(parts[2:], " ")
		if parts[0] == "-" {
			id = nil
		} else {
			id = parts[0]
		}

		cpu, ram, disk := "—", "—", "—"
		var ip interface{}
		ip = "—"

		if info, ok := runSafe("virsh", "dominfo", name); ok {
			reCPU := regexp.MustCompile(`CPU\(s\):\s+(\d+)`)
			reRAM := regexp.MustCompile(`Max memory:\s+(\d+)`)
			if m := reCPU.FindStringSubmatch(info); m != nil {
				cpu = m[1]
			}
			if m := reRAM.FindStringSubmatch(info); m != nil {
				mb := parseIntDefault(m[1], 0) / 1024
				ram = fmt.Sprintf("%d MB", mb)
				if mb >= 1024 {
					ram = fmt.Sprintf("%d GB", mb/1024)
				}
			}
		}

		if status == "running" {
			if ips, ok := runSafe("virsh", "domifaddr", name); ok {
				reIP := regexp.MustCompile(`(\d+\.\d+\.\d+\.\d+)`)
				if m := reIP.FindStringSubmatch(ips); m != nil {
					ip = m[1]
				}
			}
		}

		if blk, ok := runSafe("virsh", "domblklist", name, "--details"); ok {
			for _, bl := range strings.Split(blk, "\n") {
				if strings.Contains(bl, "disk") {
					diskPath := strings.TrimSpace(strings.Fields(bl)[len(strings.Fields(bl))-1])
					if _, err := os.Stat(diskPath); err == nil {
						if sz, ok := runSafe("qemu-img", "info", diskPath); ok {
							reSz := regexp.MustCompile(`virtual size:\s+(.+?)(?:\s+\(|$)`)
							if m := reSz.FindStringSubmatch(sz); m != nil {
								disk = m[1]
							}
						}
					}
					break
				}
			}
		}

		vms = append(vms, map[string]interface{}{
			"id": id, "name": name, "status": status,
			"cpu": cpu, "ram": ram, "disk": disk, "ip": ip,
		})
	}

	if vms == nil {
		vms = []map[string]interface{}{}
	}
	jsonOk(w, map[string]interface{}{"vms": vms})
}

func vmsOverview(w http.ResponseWriter) {
	hostname, _ := runSafe("hostname")
	cpuUsage, _ := runShellStatic("top -bn1 | grep '%Cpu' | awk '{print $2}' 2>/dev/null")
	memUsage, _ := runShellStatic(`free -m | awk '/Mem:/{printf "%.0f", $3/$2*100}' 2>/dev/null`)
	nodeInfo, _ := runSafe("virsh", "nodeinfo")

	totalCPUs := "?"
	totalRAM := "?"
	reCPU := regexp.MustCompile(`CPU\(s\):\s+(\d+)`)
	reRAM := regexp.MustCompile(`Memory size:\s+(\d+)`)
	if m := reCPU.FindStringSubmatch(nodeInfo); m != nil {
		totalCPUs = m[1]
	}
	if m := reRAM.FindStringSubmatch(nodeInfo); m != nil {
		totalRAM = m[1]
	}

	raw, _ := runSafe("virsh", "list", "--all")
	running := 0
	total := 0
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "Id") || strings.HasPrefix(line, "---") {
			continue
		}
		total++
		if strings.Contains(line, "running") {
			running++
		}
	}

	jsonOk(w, map[string]interface{}{
		"hostname": strings.TrimSpace(hostname), "cpuUsage": strings.TrimSpace(cpuUsage),
		"memUsage": strings.TrimSpace(memUsage), "totalCPUs": totalCPUs, "totalRAM": totalRAM,
		"running": running, "total": total,
	})
}

func vmsCreate(w http.ResponseWriter, r *http.Request, session *DBSession) {
	if session.Role != "admin" {
		jsonError(w, 403, "Admin required")
		return
	}

	body, _ := readBody(r)
	name := bodyStr(body, "name")
	if name == "" {
		jsonError(w, 400, "Name required")
		return
	}

	safeName := regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(name, "")
	cpus := bodyStr(body, "cpus")
	if cpus == "" {
		cpus = "2"
	}
	ramStr := bodyStr(body, "ram")
	ramUnit := bodyStr(body, "ramUnit")
	diskStr := bodyStr(body, "disk")
	diskUnit := bodyStr(body, "diskUnit")
	networkType := bodyStr(body, "networkType")
	iso := bodyStr(body, "iso")
	firmware := bodyStr(body, "firmware")

	ramMB := parseIntDefault(ramStr, 2048)
	if ramUnit == "GB" {
		ramMB *= 1024
	}

	diskSuffix := "G"
	if diskUnit == "TB" {
		diskSuffix = "T"
	}

	diskPath := filepath.Join(vmDir, safeName+".qcow2")
	diskSize := fmt.Sprintf("%s%s", diskStr, diskSuffix)

	// Create disk
	cmd := exec.Command("qemu-img", "create", "-f", "qcow2", diskPath, diskSize)
	if out, err := cmd.CombinedOutput(); err != nil {
		jsonError(w, 500, fmt.Sprintf("Failed to create disk: %s", string(out)))
		return
	}

	// Build virt-install command
	args := []string{
		"--name", safeName,
		"--vcpus", cpus,
		"--memory", fmt.Sprintf("%d", ramMB),
		"--disk", fmt.Sprintf("path=%s,format=qcow2", diskPath),
		"--os-variant", "generic",
		// SHIELD · consola VNC solo en loopback. En 0.0.0.0 quedaba expuesta
		// a la LAN/WAN sin auth de NimOS ni NimShield — control total de la
		// VM para cualquiera. Consola remota vía túnel SSH
		// (ssh -L 5900:127.0.0.1:5900) o proxy noVNC tras Caddy.
		"--graphics", "vnc,listen=127.0.0.1",
	}

	switch networkType {
	case "bridge":
		args = append(args, "--network", "bridge=br0,model=virtio")
	case "nat":
		args = append(args, "--network", "network=default,model=virtio")
	default:
		args = append(args, "--network", "none")
	}

	if firmware == "UEFI" {
		args = append(args, "--boot", "uefi")
	}

	if iso != "" {
		args = append(args, "--cdrom", filepath.Join(isoDir, iso))
	} else {
		args = append(args, "--import", "--noautoconsole")
	}
	if iso != "" {
		args = append(args, "--noautoconsole")
	}

	installCmd := exec.Command("virt-install", args...)
	out, err := installCmd.CombinedOutput()
	if err != nil {
		jsonError(w, 500, fmt.Sprintf("Failed to create VM: %s", string(out)))
		return
	}

	if bodyStr(body, "autoStart") == "true" || body["autoStart"] == true {
		runSafe("virsh", "autostart", safeName)
	}

	jsonOk(w, map[string]interface{}{"ok": true, "name": safeName, "log": string(out)})
}

func vmsAction(w http.ResponseWriter, r *http.Request, session *DBSession) {
	if session.Role != "admin" {
		jsonError(w, 403, "Admin required")
		return
	}

	body, _ := readBody(r)
	name := bodyStr(body, "name")
	action := bodyStr(body, "action")
	if name == "" || action == "" {
		jsonError(w, 400, "Name and action required")
		return
	}

	safeName := regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(name, "")
	var result string

	switch action {
	case "start":
		result, _ = runSafe("virsh", "start", safeName)
	case "stop":
		result, _ = runSafe("virsh", "shutdown", safeName)
	case "force-stop":
		result, _ = runSafe("virsh", "destroy", safeName)
	case "pause":
		result, _ = runSafe("virsh", "suspend", safeName)
	case "resume":
		result, _ = runSafe("virsh", "resume", safeName)
	case "restart":
		result, _ = runSafe("virsh", "reboot", safeName)
	case "delete":
		runSafe("virsh", "destroy", safeName)
		runSafe("virsh", "undefine", safeName, "--remove-all-storage")
		result = "VM deleted"
	case "autostart-on":
		result, _ = runSafe("virsh", "autostart", safeName)
	case "autostart-off":
		result, _ = runSafe("virsh", "autostart", "--disable", safeName)
	default:
		jsonError(w, 400, "Unknown action")
		return
	}

	jsonOk(w, map[string]interface{}{"ok": true, "result": result})
}

func vmsIsos(w http.ResponseWriter) {
	os.MkdirAll(isoDir, 0755)
	var isos []map[string]interface{}
	entries, err := os.ReadDir(isoDir)
	if err == nil {
		for _, e := range entries {
			if strings.HasSuffix(strings.ToLower(e.Name()), ".iso") {
				info, _ := e.Info()
				size := "0"
				if info != nil {
					size = fmtSizeVms(info.Size())
				}
				isos = append(isos, map[string]interface{}{"name": e.Name(), "size": size})
			}
		}
	}
	if isos == nil {
		isos = []map[string]interface{}{}
	}
	jsonOk(w, map[string]interface{}{"isos": isos, "path": isoDir})
}

func fmtSizeVms(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

func vmsNetworks(w http.ResponseWriter) {
	raw, _ := runSafe("virsh", "net-list", "--all")
	var networks []map[string]interface{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "Name") || strings.HasPrefix(line, "---") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			entry := map[string]interface{}{"name": parts[0], "state": parts[1]}
			if len(parts) >= 3 {
				entry["autostart"] = parts[2]
			}
			if len(parts) >= 4 {
				entry["persistent"] = parts[3]
			}
			networks = append(networks, entry)
		}
	}
	if networks == nil {
		networks = []map[string]interface{}{}
	}
	bridges, _ := runSafe("brctl", "show")
	jsonOk(w, map[string]interface{}{"networks": networks, "bridges": bridges})
}

func vmsVnc(w http.ResponseWriter, r *http.Request) {
	vmName := filepath.Base(r.URL.Path)
	display, _ := runSafe("virsh", "vncdisplay", vmName)
	display = strings.TrimSpace(display)
	var port interface{}
	if display != "" {
		num := parseIntDefault(strings.Replace(display, ":", "", 1), 0)
		port = 5900 + num
	}
	jsonOk(w, map[string]interface{}{"port": port, "display": display})
}

func vmsLogs(w http.ResponseWriter) {
	logs, _ := runSafe("journalctl", "-u", "libvirtd", "--no-pager", "-n", "50", "--output=short")
	jsonOk(w, map[string]interface{}{"logs": logs})
}

func vmsSnapshot(w http.ResponseWriter, r *http.Request, session *DBSession) {
	if session.Role != "admin" {
		jsonError(w, 403, "Admin required")
		return
	}

	body, _ := readBody(r)
	name := bodyStr(body, "name")
	snapAction := bodyStr(body, "action")
	snapshotName := bodyStr(body, "snapshotName")
	if name == "" {
		jsonError(w, 400, "VM name required")
		return
	}

	safeName := regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(name, "")

	switch snapAction {
	case "create":
		snapN := snapshotName
		if snapN == "" {
			snapN = fmt.Sprintf("snap-%d", os.Getpid())
		}
		safeSnap := regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(snapN, "")
		result, _ := runSafe("virsh", "snapshot-create-as", safeName, safeSnap)
		jsonOk(w, map[string]interface{}{"ok": true, "result": result})
	case "list":
		result, _ := runSafe("virsh", "snapshot-list", safeName)
		jsonOk(w, map[string]interface{}{"snapshots": result})
	case "revert":
		if snapshotName == "" {
			jsonError(w, 400, "Snapshot name required")
			return
		}
		safeSnap := regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(snapshotName, "")
		result, _ := runSafe("virsh", "snapshot-revert", safeName, safeSnap)
		jsonOk(w, map[string]interface{}{"ok": true, "result": result})
	case "delete":
		if snapshotName == "" {
			jsonError(w, 400, "Snapshot name required")
			return
		}
		safeSnap := regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(snapshotName, "")
		result, _ := runSafe("virsh", "snapshot-delete", safeName, safeSnap)
		jsonOk(w, map[string]interface{}{"ok": true, "result": result})
	default:
		jsonError(w, 400, "Unknown snapshot action")
	}
}
