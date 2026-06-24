package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// scanLANForNimOS scans the given subnet for NimOS devices (port 5000).
// If subnet is empty, autodetects from the first non-loopback interface.
func scanLANForNimOS(subnet string) []DiscoveredDevice {
	if subnet == "" {
		subnet = detectSubnet()
	}
	if subnet == "" {
		return nil
	}

	// Parse subnet (we support /24 only for speed)
	base := subnet
	if idx := strings.LastIndex(base, "."); idx > 0 {
		base = base[:idx]
	}
	base = strings.TrimSuffix(base, "/24")

	var (
		mu      sync.Mutex
		results []DiscoveredDevice
		wg      sync.WaitGroup
	)

	for i := 1; i <= 254; i++ {
		addr := fmt.Sprintf("%s.%d", base, i)
		wg.Add(1)
		go func(addr string) {
			defer wg.Done()
			dev := probeNimOS(addr)
			if dev != nil {
				mu.Lock()
				results = append(results, *dev)
				mu.Unlock()
			}
		}(addr)
	}
	wg.Wait()

	// Sort by IP for consistent ordering
	sort.Slice(results, func(i, j int) bool {
		return results[i].Addr < results[j].Addr
	})

	return results
}

func probeNimOS(addr string) *DiscoveredDevice {
	// TCP connect with 400ms timeout
	conn, err := net.DialTimeout("tcp", addr+":5000", 400*time.Millisecond)
	if err != nil {
		return nil
	}
	conn.Close()

	// Verify it's NimOS by hitting /api/auth/status
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://%s:5000/api/auth/status", addr))
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil
	}

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil
	}

	// NimOS /api/auth/status returns either:
	//   { "initialized": true/false, ... }   (newer versions)
	//   { "setup": true/false, ... }          (current/older versions)
	// Accept both — if either field exists, it's a NimOS device.
	_, hasInitialized := data["initialized"]
	_, hasSetup := data["setup"]
	_, hasHostname := data["hostname"]
	if !hasInitialized && !hasSetup && !hasHostname {
		return nil
	}

	name := "NimOS"
	if n, ok := data["hostname"].(string); ok && n != "" {
		name = n
	}
	version := "unknown"
	if v, ok := data["version"].(string); ok && v != "" {
		version = v
	}

	return &DiscoveredDevice{
		Addr:    addr,
		Name:    name,
		Version: version,
	}
}

// startAutoDiscovery runs a background goroutine that scans the LAN every 60s
// for NimOS devices and keeps the list in memory.
func startAutoDiscovery() {
	ctx, cancel := context.WithCancel(context.Background())
	discoveryCancel = cancel

	// Run an initial scan immediately
	go func() {
		runDiscoveryScan()

		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				logMsg("discovery: auto-discovery stopped")
				return
			case <-ticker.C:
				runDiscoveryScan()
			}
		}
	}()

	logMsg("discovery: auto-discovery started (60s interval)")
}

func stopAutoDiscovery() {
	if discoveryCancel != nil {
		discoveryCancel()
	}
}

// refreshPairedDeviceStatus pings all paired devices and caches their status.
func refreshPairedDeviceStatus() {
	devices, err := dbBackupDeviceList()
	if err != nil || len(devices) == 0 {
		return
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	newCache := map[string]map[string]interface{}{}

	for _, dev := range devices {
		dev := dev // capture
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, _ := dev["id"].(string)
			status := checkDeviceStatus(dev)
			mu.Lock()
			newCache[id] = status
			mu.Unlock()
		}()
	}
	wg.Wait()

	deviceStatusCacheMu.Lock()
	deviceStatusCache = newCache
	deviceStatusCacheMu.Unlock()
}

// getDeviceStatusCached returns cached status for a device, or a default offline status.
func getDeviceStatusCached(id string) map[string]interface{} {
	deviceStatusCacheMu.RLock()
	defer deviceStatusCacheMu.RUnlock()
	if s, ok := deviceStatusCache[id]; ok {
		return s
	}
	return map[string]interface{}{"online": false, "ping": "—"}
}

// enrichDevicesWithStatus adds online/ping/freeSpace/version to each device from cache.
func enrichDevicesWithStatus(devices []map[string]interface{}) {
	for _, dev := range devices {
		id, _ := dev["id"].(string)
		status := getDeviceStatusCached(id)
		for k, v := range status {
			dev[k] = v
		}
	}
}

func runDiscoveryScan() {
	// Get our own local addresses to exclude ourselves
	localAddrs := getLocalAddrs()

	devices := scanLANForNimOS("")

	// Filter out ourselves
	var filtered []DiscoveredDevice
	for _, d := range devices {
		if !localAddrs[d.Addr] {
			filtered = append(filtered, d)
		}
	}
	if filtered == nil {
		filtered = []DiscoveredDevice{}
	}

	discoveredDevicesMu.Lock()
	discoveredDevices = filtered
	discoveredDevicesMu.Unlock()

	if len(filtered) > 0 {
		names := make([]string, len(filtered))
		for i, d := range filtered {
			names[i] = fmt.Sprintf("%s(%s)", d.Name, d.Addr)
		}
		logMsg("discovery: found %d NimOS device(s): %s", len(filtered), strings.Join(names, ", "))
	}

	// Refresh paired device status in a separate goroutine so it doesn't
	// block the discovery cycle or hold DB connections during network timeouts
	go refreshPairedDeviceStatus()
}

func getDiscoveredDevices() []DiscoveredDevice {
	discoveredDevicesMu.RLock()
	defer discoveredDevicesMu.RUnlock()
	result := make([]DiscoveredDevice, len(discoveredDevices))
	copy(result, discoveredDevices)
	return result
}

func getLocalAddrs() map[string]bool {
	result := map[string]bool{"127.0.0.1": true, "localhost": true}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return result
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
			result[ipnet.IP.String()] = true
		}
	}
	return result
}

func detectSubnet() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			ip := ipnet.IP.To4()
			return fmt.Sprintf("%d.%d.%d.0/24", ip[0], ip[1], ip[2])
		}
	}
	return ""
}

func checkDeviceStatus(device map[string]interface{}) map[string]interface{} {
	addr, _ := device["addr"].(string)
	if addr == "" {
		return map[string]interface{}{"online": false, "ping": "—"}
	}

	// Check WireGuard address first
	if wg, ok := device["wireguard"].(map[string]interface{}); ok {
		if active, _ := wg["active"].(bool); active {
			if wgIP, _ := wg["localIP"].(string); wgIP != "" {
				if idx := strings.Index(wgIP, "/"); idx > 0 {
					wgIP = wgIP[:idx]
				}
				addr = wgIP
			}
		}
	}

	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr+":5000", 3*time.Second)
	if err != nil {
		return map[string]interface{}{"online": false, "ping": "—"}
	}
	conn.Close()
	ping := time.Since(start)

	// Also get free space and version from remote
	client := &http.Client{Timeout: 3 * time.Second}
	result := map[string]interface{}{
		"online": true,
		"ping":   fmt.Sprintf("%.0fms", float64(ping.Microseconds())/1000.0),
	}

	resp, err := client.Get(fmt.Sprintf("http://%s:5000/api/auth/status", addr))
	if err == nil {
		defer resp.Body.Close()
		var data map[string]interface{}
		if json.NewDecoder(resp.Body).Decode(&data) == nil {
			if v, ok := data["version"].(string); ok {
				result["version"] = v
			}
			if h, ok := data["hostname"].(string); ok {
				result["hostname"] = h
			}
		}
	}

	// Get free space from remote storage endpoint (if available)
	resp2, err2 := client.Get(fmt.Sprintf("http://%s:5000/api/storage/status", addr))
	if err2 == nil {
		defer resp2.Body.Close()
		var sdata map[string]interface{}
		if json.NewDecoder(resp2.Body).Decode(&sdata) == nil {
			if pools, ok := sdata["pools"].([]interface{}); ok {
				var totalFree int64
				for _, p := range pools {
					if pm, ok := p.(map[string]interface{}); ok {
						if free, ok := pm["free"].(float64); ok {
							totalFree += int64(free)
						}
					}
				}
				if totalFree > 0 {
					result["freeSpace"] = formatBytes(totalFree)
				}
			}
		}
	}

	return result
}

// isLocalAddr reports whether addr is in an RFC 1918 private range or localhost.
// Previous versions accepted all 172.* which is incorrect — only 172.16-31.*
// is actually private (172.16.0.0/12).
func isLocalAddr(addr string) bool {
	if addr == "localhost" || addr == "127.0.0.1" {
		return true
	}
	if strings.HasPrefix(addr, "192.168.") || strings.HasPrefix(addr, "10.") {
		return true
	}
	if strings.HasPrefix(addr, "172.") {
		rest := strings.TrimPrefix(addr, "172.")
		dotIdx := strings.IndexByte(rest, '.')
		if dotIdx <= 0 {
			return false
		}
		second, err := strconv.Atoi(rest[:dotIdx])
		if err != nil {
			return false
		}
		return second >= 16 && second <= 31
	}
	return false
}

// getLocalHostname returns this machine's hostname.
func getLocalHostname() string {
	if out, ok := runSafe("hostname"); ok && out != "" {
		return strings.TrimSpace(out)
	}
	return "NimOS"
}

// getLocalLANAddr returns our IP address that's on the same subnet as the remote addr.
// E.g., if remote is 192.168.1.131, returns our 192.168.1.x address.
func getLocalLANAddr(remoteAddr string) string {
	// Extract remote subnet prefix (first 3 octets)
	parts := strings.Split(remoteAddr, ".")
	if len(parts) < 3 {
		return detectOwnIP()
	}
	remotePrefix := parts[0] + "." + parts[1] + "." + parts[2] + "."

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return detectOwnIP()
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
			ip := ipnet.IP.String()
			if strings.HasPrefix(ip, remotePrefix) {
				return ip
			}
		}
	}
	return detectOwnIP()
}

// detectOwnIP returns the first non-loopback IPv4 address.
func detectOwnIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			return ipnet.IP.String()
		}
	}
	return "127.0.0.1"
}
