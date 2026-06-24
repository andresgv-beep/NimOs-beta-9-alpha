package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func detectAmdDrm() bool {
	entries, err := os.ReadDir("/sys/class/drm")
	if err != nil {
		return false
	}
	for _, e := range entries {
		if matched, _ := regexp.MatchString(`^card\d$`, e.Name()); matched {
			if data := readFileStr(fmt.Sprintf("/sys/class/drm/%s/device/gpu_busy_percent", e.Name())); data != "" {
				return true
			}
		}
	}
	return false
}

func getGpu() []map[string]interface{} {
	var gpus []map[string]interface{}

	if hasNvidia {
		out, ok := runSafe("nvidia-smi", "--query-gpu=index,name,utilization.gpu,temperature.gpu,memory.used,memory.total", "--format=csv,noheader,nounits")
		if ok && out != "" {
			for _, line := range strings.Split(out, "\n") {
				parts := strings.Split(line, ",")
				if len(parts) >= 6 {
					memUsed := parseIntDefault(strings.TrimSpace(parts[4]), 0)
					memTotal := parseIntDefault(strings.TrimSpace(parts[5]), 0)
					memPct := 0
					if memTotal > 0 {
						memPct = int(math.Round(float64(memUsed) / float64(memTotal) * 100))
					}
					gpus = append(gpus, map[string]interface{}{
						"index":       parseIntDefault(strings.TrimSpace(parts[0]), 0),
						"name":        strings.TrimSpace(parts[1]),
						"utilization": parseIntDefault(strings.TrimSpace(parts[2]), 0),
						"temperature": parseIntDefault(strings.TrimSpace(parts[3]), 0),
						"memUsed":     memUsed,
						"memTotal":    memTotal,
						"memPercent":  memPct,
						"driver":      "nvidia",
					})
				}
			}
		}
	}

	if hasAmdDrm {
		entries, _ := os.ReadDir("/sys/class/drm")
		for _, e := range entries {
			if matched, _ := regexp.MatchString(`^card\d$`, e.Name()); !matched {
				continue
			}
			busy := readFileStr(fmt.Sprintf("/sys/class/drm/%s/device/gpu_busy_percent", e.Name()))
			if busy == "" {
				continue
			}
			// Find temperature
			temp := 0
			hwmonDirs, _ := filepath.Glob(fmt.Sprintf("/sys/class/drm/%s/device/hwmon/hwmon*", e.Name()))
			for _, dir := range hwmonDirs {
				t := readFileStr(filepath.Join(dir, "temp1_input"))
				if t != "" {
					temp = parseIntDefault(t, 0) / 1000
					break
				}
			}
			gpus = append(gpus, map[string]interface{}{
				"index":       len(gpus),
				"name":        fmt.Sprintf("AMD GPU (%s)", e.Name()),
				"utilization": parseIntDefault(busy, 0),
				"temperature": temp,
				"memUsed":     0,
				"memTotal":    0,
				"memPercent":  0,
				"driver":      "amd",
			})
		}
	}

	if gpus == nil {
		gpus = []map[string]interface{}{}
	}
	return gpus
}

func getHardwareGpuInfo() map[string]interface{} {
	result := map[string]interface{}{
		"gpus":             []interface{}{},
		"currentDriver":    nil,
		"driverVersion":    nil,
		"availableDrivers": []interface{}{},
		"kernelModules":    []interface{}{},
	}

	// Detect GPUs via lspci
	var gpuList []interface{}
	lspci, ok := runShellStatic(`lspci -nn 2>/dev/null | grep -iE "VGA|3D|Display"`)
	if ok && lspci != "" {
		for _, line := range strings.Split(lspci, "\n") {
			if line == "" {
				continue
			}
			lower := strings.ToLower(line)
			vendor := "unknown"
			if strings.Contains(lower, "nvidia") {
				vendor = "nvidia"
			} else if strings.Contains(lower, "amd") || strings.Contains(lower, "ati") {
				vendor = "amd"
			} else if strings.Contains(lower, "intel") {
				vendor = "intel"
			}
			pciId := ""
			if m := regexp.MustCompile(`\[([0-9a-f]{4}:[0-9a-f]{4})\]`).FindStringSubmatch(line); m != nil {
				pciId = m[1]
			}
			desc := line
			if idx := strings.Index(line, " "); idx > 0 {
				desc = strings.TrimSpace(line[idx:])
			}
			gpuList = append(gpuList, map[string]interface{}{
				"description": desc,
				"vendor":      vendor,
				"pciId":       pciId,
			})
		}
	}

	// ARM fallback
	if len(gpuList) == 0 {
		if vcgencmd, ok := runSafe("vcgencmd", "get_mem", "gpu"); ok && vcgencmd != "" {
			model := readFileStr("/proc/device-tree/model")
			if model == "" {
				model = "Raspberry Pi"
			}
			gpuMem := strings.Replace(strings.Replace(vcgencmd, "gpu=", "", 1), "M", " MB", 1)
			gpuList = append(gpuList, map[string]interface{}{
				"description": fmt.Sprintf("%s — VideoCore (%s)", strings.TrimSpace(model), strings.TrimSpace(gpuMem)),
				"vendor":      "broadcom",
				"pciId":       nil,
			})
			result["currentDriver"] = "v3d"
		}
	}
	if gpuList == nil {
		gpuList = []interface{}{}
	}
	result["gpus"] = gpuList

	// NVIDIA driver
	if hasNvidia {
		if ver, ok := runSafe("nvidia-smi", "--query-gpu=driver_version", "--format=csv,noheader,nounits"); ok && ver != "" {
			result["currentDriver"] = "nvidia"
			result["driverVersion"] = strings.TrimSpace(strings.Split(ver, "\n")[0])
		}
	}

	// AMD driver
	if out, ok := runShellStatic("lsmod 2>/dev/null | grep amdgpu"); ok && out != "" {
		if result["currentDriver"] == nil {
			result["currentDriver"] = "amdgpu"
		}
	}

	// Intel driver
	if out, ok := runShellStatic("lsmod 2>/dev/null | grep i915"); ok && out != "" {
		if result["currentDriver"] == nil {
			result["currentDriver"] = "i915"
		}
	}

	// Kernel modules
	var modules []interface{}
	if mods, ok := runShellStatic(`lsmod 2>/dev/null | grep -iE "nvidia|amdgpu|radeon|i915|nouveau"`); ok && mods != "" {
		for _, line := range strings.Split(mods, "\n") {
			if line == "" {
				continue
			}
			parts := strings.Fields(line)
			entry := map[string]interface{}{"name": parts[0]}
			if len(parts) > 1 {
				entry["size"] = parts[1]
			}
			if len(parts) > 3 {
				entry["usedBy"] = parts[3]
			}
			modules = append(modules, entry)
		}
	}
	if modules == nil {
		modules = []interface{}{}
	}
	result["kernelModules"] = modules

	return result
}

func getTemps(gpusCache []map[string]interface{}) map[string]interface{} {
	temps := map[string]interface{}{}

	// CPU via /sys/class/thermal
	entries, _ := os.ReadDir("/sys/class/thermal")
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "thermal_zone") {
			continue
		}
		typeName := readFileStr(fmt.Sprintf("/sys/class/thermal/%s/type", e.Name()))
		tempStr := readFileStr(fmt.Sprintf("/sys/class/thermal/%s/temp", e.Name()))
		if typeName != "" && tempStr != "" {
			temps[typeName] = parseIntDefault(tempStr, 0) / 1000
		}
	}

	// lm-sensors fallback
	if len(temps) == 0 && hasSensors {
		if out, ok := runSafe("sensors", "-u"); ok {
			re := regexp.MustCompile(`temp1_input:\s+([\d.]+)`)
			if m := re.FindStringSubmatch(out); m != nil {
				temps["cpu"] = int(math.Round(parseFloat(m[1])))
			}
		}
	}

	// GPU temps
	gpus := gpusCache
	if gpus == nil {
		gpus = getGpu()
	}
	for i, g := range gpus {
		if t, ok := g["temperature"].(int); ok && t > 0 {
			temps[fmt.Sprintf("gpu%d", i)] = t
		}
	}

	return temps
}

// pickMainTemp elige la temperatura "principal" de CPU de forma
// determinista. Orden por nombre de zona térmica: x86 (pkg/coretemp),
// Raspberry Pi (cpu-thermal) y genérico (cpu). Fallback: primera
// entrada del mapa (no determinista, último recurso).
func pickMainTemp(temps map[string]interface{}) interface{} {
	for _, key := range []string{"x86_pkg_temp", "cpu-thermal", "cpu", "coretemp"} {
		if v, ok := temps[key]; ok {
			return v
		}
	}
	for _, v := range temps {
		return v
	}
	return nil
}
