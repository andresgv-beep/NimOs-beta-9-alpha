package main

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// ═══════════════════════════════════
// Startup detection
// ═══════════════════════════════════

var (
	hasSmartctl bool
	hasSensors  bool
	hasDocker   bool
	hasNvidia   bool
	hasAmdDrm   bool
	systemArch  string
	systemRamGB int
)

// Pre-compiled regexes for hot paths (avoid recompiling in loops)
var (
	reSdDisk   = regexp.MustCompile(`^sd[a-z]+$`)
	reNvmeDisk = regexp.MustCompile(`^nvme\d+n\d+$`)
	reVdDisk   = regexp.MustCompile(`^vd[a-z]+$`)
)

func detectHardwareTools() {
	_, hasSmartctl = runSafe("which", "smartctl")
	_, hasSensors = runSafe("which", "sensors")
	_, hasDocker = runSafe("which", "docker")
	_, hasNvidia = runSafe("which", "nvidia-smi")
	hasAmdDrm = detectAmdDrm()

	// Beta 8: ZFS no longer supported. Only BTRFS is detected.
	detectBtrfs()

	// System info
	archOut, _ := runSafe("uname", "-m")
	systemArch = strings.TrimSpace(archOut)

	// RAM: leer /proc/meminfo directamente sin shell.
	// Antes usábamos runShellStatic con un pipe awk, pero el shield lo
	// rechaza por interpolar comandos. Leer el archivo nativo es más
	// seguro, más rápido, y no dispara warnings de seguridad.
	if meminfoBytes, err := os.ReadFile("/proc/meminfo"); err == nil {
		re := regexp.MustCompile(`MemTotal:\s+(\d+)\s+kB`)
		if m := re.FindStringSubmatch(string(meminfoBytes)); m != nil {
			kb := parseInt64(m[1])
			systemRamGB = int(kb / 1024 / 1024) // kB → GB
		}
	}

	if hasBtrfs {
		logMsg("Btrfs available (arch=%s, ram=%dGB)", systemArch, systemRamGB)
	} else {
		logMsg("WARNING: No supported storage backend (arch=%s, ram=%dGB) — install btrfs-progs", systemArch, systemRamGB)
	}
}

// ═══════════════════════════════════
// Helpers
// ═══════════════════════════════════

func readFileStr(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func formatBytes(bytes int64) string {
	if bytes == 0 {
		return "0 B"
	}
	sizes := []string{"B", "KB", "MB", "GB", "TB"}
	i := int(math.Floor(math.Log(math.Abs(float64(bytes))) / math.Log(1024)))
	if i >= len(sizes) {
		i = len(sizes) - 1
	}
	return fmt.Sprintf("%.1f %s", float64(bytes)/math.Pow(1024, float64(i)), sizes[i])
}

func parseInt64(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}

func parseIntDefault(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}

// ═══════════════════════════════════
// CPU
// ═══════════════════════════════════

var prevCpuIdle, prevCpuTotal int64

// ── Disk I/O tracking (for /api/hardware/stats) ──
var (
	prevDiskRead  int64
	prevDiskWrite int64
	prevDiskTime  int64
)

// getNetworkAggregate returns total rx/tx bytes per second across all physical interfaces.
// Uses its own tracking vars to avoid interfering with getNetwork() per-interface stats.
var (
	prevNetAgg   = map[string]netStat{}
	prevNetAggMu sync.Mutex
)

// ═══════════════════════════════════
// Memory
// ═══════════════════════════════════

// ═══════════════════════════════════
// GPU
// ═══════════════════════════════════

// ═══════════════════════════════════
// GPU Driver Info
// ═══════════════════════════════════

// ═══════════════════════════════════
// Temperatures
// ═══════════════════════════════════

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

// ═══════════════════════════════════
// Network
// ═══════════════════════════════════

var (
	prevNetStats   = map[string]netStat{}
	prevNetStatsMu sync.Mutex
)

type netStat struct {
	rx, tx int64
	time   int64
}

// ═══════════════════════════════════
// Disks
// ═══════════════════════════════════

var (
	diskCache     map[string]interface{}
	diskCacheTime int64
	diskCacheMu   sync.Mutex
)

// ═══════════════════════════════════
// Uptime
// ═══════════════════════════════════

// ═══════════════════════════════════
// Containers
// ═══════════════════════════════════

var (
	containerCache     []interface{}
	containerCacheTime int64
	containerCacheMu   sync.Mutex
)

// ═══════════════════════════════════
// System Summary
// ═══════════════════════════════════

var (
	systemCache     map[string]interface{}
	systemCacheTime int64
	systemCacheMu   sync.Mutex
)

// ═══════════════════════════════════
// Hardware HTTP routes
// ═══════════════════════════════════

// ═══════════════════════════════════════════════════════════════════════════════
// SMART — Disk health data via smartctl
// GET /api/disks/smart?disk=sda
// ═══════════════════════════════════════════════════════════════════════════════

// ═══════════════════════════════════════════════════════════════════════════════
// SMART Monitor — Background disk health monitoring
// Runs every 30 minutes, checks all disks, creates notifications on status change
// ═══════════════════════════════════════════════════════════════════════════════

var smartHistory = map[string]string{}            // disk name -> last known status ("ok"/"warning"/"critical")
var smartDetailsCache = map[string]SmartDetails{} // disk name -> cached SMART detail metrics
var smartMu sync.Mutex

// FIX3 — debounce de temperatura. tempHistory guarda el último estado de temp
// notificado por disco ("normal"/"high"), para notificar SOLO en transiciones y
// no en cada ciclo SMART. Histéresis: se entra en "high" al cruzar tempHighC
// hacia arriba y solo se vuelve a "normal" al bajar de tempRecoverC, evitando
// parpadeo si la temperatura oscila alrededor del umbral.
var tempHistory = map[string]string{} // disk name -> "normal" | "high"

const (
	tempHighC    = 55 // umbral de alerta (°C)
	tempRecoverC = 50 // histéresis: recuperación solo por debajo de esto
)
