package main

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
	"time"
)

// getDiskIO reads /proc/diskstats and returns aggregate read/write bytes per second.
// Only counts whole-disk devices (sda, nvme0n1, vda), not partitions.
func getDiskIO() map[string]interface{} {
	data := readFileStr("/proc/diskstats")
	if data == "" {
		return map[string]interface{}{"readSpeed": 0, "writeSpeed": 0}
	}

	var totalRead, totalWrite int64
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 14 {
			continue
		}
		dev := fields[2]
		// Only whole disks, not partitions
		// sd[a-z], nvme[0-9]n[0-9], vd[a-z], xvd[a-z]
		isWholeDisk := false
		if reSdDisk.MatchString(dev) ||
			reNvmeDisk.MatchString(dev) ||
			reVdDisk.MatchString(dev) {
			isWholeDisk = true
		}
		if !isWholeDisk {
			continue
		}
		// fields[5] = sectors read, fields[9] = sectors written
		// Sector size = 512 bytes
		totalRead += parseInt64(fields[5]) * 512
		totalWrite += parseInt64(fields[9]) * 512
	}

	now := time.Now().UnixMilli()
	var readSpeed, writeSpeed int64
	if prevDiskTime > 0 {
		dt := float64(now-prevDiskTime) / 1000
		if dt > 0 {
			readSpeed = int64(math.Round(float64(totalRead-prevDiskRead) / dt))
			writeSpeed = int64(math.Round(float64(totalWrite-prevDiskWrite) / dt))
			if readSpeed < 0 {
				readSpeed = 0
			}
			if writeSpeed < 0 {
				writeSpeed = 0
			}
		}
	}
	prevDiskRead = totalRead
	prevDiskWrite = totalWrite
	prevDiskTime = now

	return map[string]interface{}{
		"readSpeed":  readSpeed,
		"writeSpeed": writeSpeed,
	}
}

func getNetworkAggregate() map[string]interface{} {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return map[string]interface{}{"rxSpeed": 0, "txSpeed": 0}
	}

	var totalRx, totalTx int64
	now := time.Now().UnixMilli()

	prevNetAggMu.Lock()
	defer prevNetAggMu.Unlock()

	for _, e := range entries {
		dev := e.Name()
		if !isPhysicalInterface(dev) {
			continue
		}
		operstate := readFileStr(fmt.Sprintf("/sys/class/net/%s/operstate", dev))
		if operstate != "up" {
			continue
		}
		rxBytes := parseInt64(readFileStr(fmt.Sprintf("/sys/class/net/%s/statistics/rx_bytes", dev)))
		txBytes := parseInt64(readFileStr(fmt.Sprintf("/sys/class/net/%s/statistics/tx_bytes", dev)))

		if prev, ok := prevNetAgg[dev]; ok {
			dt := float64(now-prev.time) / 1000
			if dt > 0 {
				totalRx += int64(math.Round(float64(rxBytes-prev.rx) / dt))
				totalTx += int64(math.Round(float64(txBytes-prev.tx) / dt))
			}
		}
		prevNetAgg[dev] = netStat{rx: rxBytes, tx: txBytes, time: now}
	}

	if totalRx < 0 {
		totalRx = 0
	}
	if totalTx < 0 {
		totalTx = 0
	}

	return map[string]interface{}{
		"rxSpeed": totalRx,
		"txSpeed": totalTx,
	}
}

func getCpuUsage() map[string]interface{} {
	stat := readFileStr("/proc/stat")
	cpuCount := 0
	cpuModel := "Unknown"
	cpuInfo := readFileStr("/proc/cpuinfo")
	if cpuInfo != "" {
		for _, line := range strings.Split(cpuInfo, "\n") {
			if strings.HasPrefix(line, "processor") {
				cpuCount++
			}
			if strings.HasPrefix(line, "model name") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					cpuModel = strings.TrimSpace(parts[1])
				}
			}
		}
		// ARM: no "model name" — try /proc/device-tree/model or Hardware line
		if cpuModel == "Unknown" {
			if dtModel := readFileStr("/proc/device-tree/model"); dtModel != "" {
				cpuModel = strings.TrimRight(dtModel, "\x00\n")
			} else {
				for _, line := range strings.Split(cpuInfo, "\n") {
					if strings.HasPrefix(line, "Hardware") {
						parts := strings.SplitN(line, ":", 2)
						if len(parts) == 2 {
							cpuModel = strings.TrimSpace(parts[1])
						}
						break
					}
				}
			}
		}
	}
	if cpuCount == 0 {
		cpuCount = 1
	}

	percent := 0
	if stat != "" {
		line := strings.Split(stat, "\n")[0]
		fields := strings.Fields(line)
		if len(fields) >= 8 {
			var values []int64
			for _, f := range fields[1:] {
				values = append(values, parseInt64(f))
			}
			idle := values[3]
			if len(values) > 4 {
				idle += values[4] // iowait
			}
			total := int64(0)
			for _, v := range values {
				total += v
			}

			if prevCpuTotal > 0 {
				diffIdle := idle - prevCpuIdle
				diffTotal := total - prevCpuTotal
				if diffTotal > 0 {
					percent = int(math.Round(float64(diffTotal-diffIdle) / float64(diffTotal) * 100))
				}
			}
			prevCpuIdle = idle
			prevCpuTotal = total
		}
	}

	return map[string]interface{}{
		"percent": percent,
		"cores":   cpuCount,
		"model":   cpuModel,
	}
}

func getMemory() map[string]interface{} {
	info := readFileStr("/proc/meminfo")
	if info == "" {
		return map[string]interface{}{"total": 0, "used": 0, "percent": 0}
	}

	parse := func(key string) int64 {
		re := regexp.MustCompile(key + `:\s+(\d+)`)
		m := re.FindStringSubmatch(info)
		if m == nil {
			return 0
		}
		return parseInt64(m[1]) * 1024 // kB to bytes
	}

	total := parse("MemTotal")
	available := parse("MemAvailable")
	used := total - available

	return map[string]interface{}{
		"total":   total,
		"used":    used,
		"totalGB": fmt.Sprintf("%.1f", float64(total)/1073741824),
		"usedGB":  fmt.Sprintf("%.1f", float64(used)/1073741824),
		"percent": func() int {
			if total > 0 {
				return int(math.Round(float64(used) / float64(total) * 100))
			}
			return 0
		}(),
	}
}

func getUptime() string {
	raw := readFileStr("/proc/uptime")
	if raw == "" {
		return "—"
	}
	secs := parseFloat(strings.Fields(raw)[0])
	days := int(secs) / 86400
	hours := (int(secs) % 86400) / 3600
	mins := (int(secs) % 3600) / 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}
