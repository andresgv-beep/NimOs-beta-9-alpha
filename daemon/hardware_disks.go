package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
	"time"
)

func isPhysicalInterface(dev string) bool {
	skip := []string{"lo", "docker", "br-", "veth", "virbr", "tun", "tap"}
	for _, s := range skip {
		if dev == s || strings.HasPrefix(dev, s) {
			return false
		}
	}
	// Check physical device
	if _, err := os.Stat(fmt.Sprintf("/sys/class/net/%s/device", dev)); err == nil {
		return true
	}
	// Allow common naming patterns
	for _, prefix := range []string{"eth", "enp", "eno", "ens", "wl"} {
		if strings.HasPrefix(dev, prefix) {
			return true
		}
	}
	return false
}

func getNetwork() []map[string]interface{} {
	var interfaces []map[string]interface{}

	// Get all IPs
	allIps := map[string]string{}
	if ipOut, ok := runSafe("ip", "-4", "-o", "addr", "show"); ok {
		for _, line := range strings.Split(ipOut, "\n") {
			re := regexp.MustCompile(`^\d+:\s+(\S+)\s+inet\s+([\d.]+)`)
			if m := re.FindStringSubmatch(line); m != nil {
				allIps[m[1]] = m[2]
			}
		}
	}

	entries, _ := os.ReadDir("/sys/class/net")
	prevNetStatsMu.Lock()
	defer prevNetStatsMu.Unlock()

	now := time.Now().UnixMilli()

	for _, e := range entries {
		dev := e.Name()
		if !isPhysicalInterface(dev) {
			continue
		}

		operstate := readFileStr(fmt.Sprintf("/sys/class/net/%s/operstate", dev))
		if operstate != "up" {
			continue
		}

		speed := readFileStr(fmt.Sprintf("/sys/class/net/%s/speed", dev))
		rxBytes := parseInt64(readFileStr(fmt.Sprintf("/sys/class/net/%s/statistics/rx_bytes", dev)))
		txBytes := parseInt64(readFileStr(fmt.Sprintf("/sys/class/net/%s/statistics/tx_bytes", dev)))
		mac := readFileStr(fmt.Sprintf("/sys/class/net/%s/address", dev))
		isWifi := strings.HasPrefix(dev, "wl")

		var ssid, signal interface{}
		ssid = nil
		signal = nil
		if isWifi {
			if s, ok := runSafe("iwgetid", "-r", dev); ok && s != "" {
				ssid = strings.TrimSpace(s)
			}
			if sig, ok := runSafe("iwconfig", dev); ok {
				re := regexp.MustCompile(`Signal level[=:]?\s*(-?\d+)`)
				if m := re.FindStringSubmatch(sig); m != nil {
					signal = parseIntDefault(m[1], 0)
				}
			}
		}

		// Calculate rates
		var rxRate, txRate int64
		if prev, ok := prevNetStats[dev]; ok {
			dt := float64(now-prev.time) / 1000
			if dt > 0 {
				rxRate = int64(math.Round(float64(rxBytes-prev.rx) / dt))
				txRate = int64(math.Round(float64(txBytes-prev.tx) / dt))
			}
		}
		prevNetStats[dev] = netStat{rx: rxBytes, tx: txBytes, time: now}

		speedStr := "—"
		if speed != "" {
			n := parseIntDefault(speed, 0)
			if n > 0 {
				speedStr = fmt.Sprintf("%s Mbps", speed)
			} else if isWifi && ssid != nil {
				speedStr = "WiFi"
			}
		}

		iface := map[string]interface{}{
			"name":            dev,
			"type":            "ethernet",
			"status":          operstate,
			"speed":           speedStr,
			"ip":              allIps[dev],
			"mac":             mac,
			"ssid":            ssid,
			"signal":          signal,
			"rxBytes":         rxBytes,
			"txBytes":         txBytes,
			"rxRate":          rxRate,
			"txRate":          txRate,
			"rxRateFormatted": formatBytes(rxRate) + "/s",
			"txRateFormatted": formatBytes(txRate) + "/s",
		}
		if isWifi {
			iface["type"] = "wifi"
		}
		if _, ok := allIps[dev]; !ok {
			iface["ip"] = "—"
		}
		interfaces = append(interfaces, iface)
	}

	if interfaces == nil {
		interfaces = []map[string]interface{}{}
	}
	return interfaces
}

func getDisks() map[string]interface{} {
	diskCacheMu.Lock()
	defer diskCacheMu.Unlock()

	now := time.Now().UnixMilli()

	// Cache hardware info for 60s
	if diskCache == nil || (now-diskCacheTime) > 60000 {
		var disks []interface{}
		if lsblk, ok := runSafe("lsblk", "-Jbdo", "NAME,SIZE,MODEL,TYPE,TRAN"); ok && lsblk != "" {
			var data struct {
				BlockDevices []struct {
					Name  string `json:"name"`
					Size  string `json:"size"`
					Model string `json:"model"`
					Type  string `json:"type"`
					Tran  string `json:"tran"`
				} `json:"blockdevices"`
			}
			if json.Unmarshal([]byte(lsblk), &data) == nil {
				for _, dev := range data.BlockDevices {
					if dev.Type != "disk" {
						continue
					}
					if strings.HasPrefix(dev.Name, "loop") || strings.HasPrefix(dev.Name, "ram") || strings.HasPrefix(dev.Name, "zram") {
						continue
					}
					size := parseInt64(dev.Size)
					if size <= 0 {
						continue
					}

					var temp interface{}
					if hasSmartctl && isValidDev(dev.Name) {
						if smart, ok := runSafe("smartctl", "-A", "/dev/"+dev.Name); ok && smart != "" {
							// Filter for temperature line
							for _, line := range strings.Split(smart, "\n") {
								if strings.Contains(strings.ToLower(line), "temperature") {
									re := regexp.MustCompile(`(\d+)\s*$`)
									if m := re.FindStringSubmatch(line); m != nil {
										temp = parseIntDefault(m[1], 0)
									}
									break
								}
							}
						}
					}

					tran := dev.Tran
					if tran == "" {
						tran = "—"
					}
					disks = append(disks, map[string]interface{}{
						"name":          fmt.Sprintf("/dev/%s", dev.Name),
						"model":         strings.TrimSpace(dev.Model),
						"size":          size,
						"sizeFormatted": formatBytes(size),
						"temperature":   temp,
						"transport":     tran,
						"type":          "disk",
					})
				}
			}
		}
		if disks == nil {
			disks = []interface{}{}
		}

		// RAID
		var raids []interface{}
		mdstat := readFileStr("/proc/mdstat")
		if mdstat != "" {
			re := regexp.MustCompile(`(?m)^(md\d+)\s*:\s*active\s+(\w+)\s+(.+)`)
			for _, m := range re.FindAllStringSubmatch(mdstat, -1) {
				raids = append(raids, map[string]interface{}{
					"name": m[1], "type": m[2], "devices": strings.TrimSpace(m[3]),
				})
			}
		}
		if raids == nil {
			raids = []interface{}{}
		}

		diskCache = map[string]interface{}{"disks": disks, "raids": raids}
		diskCacheTime = now
	}

	// df always fresh
	var mounts []interface{}
	if df, ok := runSafe("df", "-B1", "--output=source,size,used,avail,target"); ok {
		for _, line := range strings.Split(df, "\n")[1:] {
			parts := strings.Fields(line)
			if len(parts) < 5 || !strings.HasPrefix(parts[0], "/dev/") || strings.Contains(parts[0], "loop") {
				continue
			}
			total := parseInt64(parts[1])
			used := parseInt64(parts[2])
			pct := 0
			if total > 0 {
				pct = int(math.Round(float64(used) / float64(total) * 100))
			}
			mounts = append(mounts, map[string]interface{}{
				"device":         parts[0],
				"total":          total,
				"used":           used,
				"available":      parseInt64(parts[3]),
				"mount":          parts[4],
				"totalFormatted": formatBytes(total),
				"usedFormatted":  formatBytes(used),
				"percent":        pct,
			})
		}
	}
	if mounts == nil {
		mounts = []interface{}{}
	}

	result := map[string]interface{}{}
	for k, v := range diskCache {
		result[k] = v
	}
	result["mounts"] = mounts
	return result
}
