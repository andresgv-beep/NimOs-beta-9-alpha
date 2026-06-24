package main

// ═══════════════════════════════════════════════════════════════════════════════
// NimOS Backup — dispatcher HTTP (handleBackupRoutes) + helpers.
// El módulo está repartido (Beta 8.2): persistencia en backup_db.go, ejecución
// y scheduler en backup_executor.go, snapshots/retención en backup_snapshots.go,
// pairing/SSH en backup_pairing.go, descubrimiento LAN en backup_discovery.go,
// montajes remotos en backup_mounts.go. BTRFS send/receive (BTRFS-only).
// ═══════════════════════════════════════════════════════════════════════════════

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/user"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ─── Database Tables ────────────────────────────────────────────────────────

// ─── ID Generation ──────────────────────────────────────────────────────────

func backupID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano()/1e6)
}

// ─── Pair Token Helpers ─────────────────────────────────────────────────────

// ─── SSH Host Key Helpers (LOGIC-021) ───────────────────────────────────────

// ─── Device DB Operations ───────────────────────────────────────────────────

// ─── Job DB Operations ──────────────────────────────────────────────────────

// ─── History DB Operations ──────────────────────────────────────────────────

// ─── Schedule Parsing ───────────────────────────────────────────────────────

// parseInt parses a string to int, returning fallback on failure.
func parseInt(s string, fallback int) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	if n == 0 {
		return fallback
	}
	return n
}

// ─── Retention Parsing ──────────────────────────────────────────────────────

// ─── Backup Execution ───────────────────────────────────────────────────────

// backupRunningJobs tracks currently running jobs to prevent double execution
var (
	backupRunningJobs   = map[string]bool{}
	backupRunningJobsMu sync.Mutex
)

// ─── Retention ──────────────────────────────────────────────────────────────

// ─── LAN Scanner ────────────────────────────────────────────────────────────

// DiscoveredDevice represents a NimOS device found on the LAN.
type DiscoveredDevice struct {
	Addr    string `json:"addr"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ─── Auto-Discovery Service ─────────────────────────────────────────────────

// discoveredDevices holds the latest LAN scan results, updated periodically.
var (
	discoveredDevices   []DiscoveredDevice
	discoveredDevicesMu sync.RWMutex
	discoveryCancel     context.CancelFunc
)

// ─── Device Status Cache ────────────────────────────────────────────────────

// deviceStatusCache holds the latest status for each paired device, keyed by device ID.
var (
	deviceStatusCache   = map[string]map[string]interface{}{}
	deviceStatusCacheMu sync.RWMutex
)

// ─── Device Status (Ping) ───────────────────────────────────────────────────

// formatBytes is defined in hardware.go — reused here

// ─── Scheduler ──────────────────────────────────────────────────────────────

var backupSchedulerCancel context.CancelFunc

// ─── HTTP Route Handler ─────────────────────────────────────────────────────

func handleBackupRoutes(w http.ResponseWriter, r *http.Request) {
	urlPath := r.URL.Path
	method := r.Method

	// Public endpoint: paired devices can list shares without full auth
	// (verified by checking if requester IP is a paired device)
	if urlPath == "/api/backup/public-shares" && method == "GET" {
		result := getPublicShares(r)
		if errMsg, ok := result["error"].(string); ok && errMsg != "" {
			jsonError(w, 403, errMsg)
			return
		}
		jsonOk(w, result)
		return
	}

	// Public endpoint: paired devices request NFS export of a share path
	if urlPath == "/api/backup/nfs-export" && method == "POST" {
		handleNFSExport(w, r)
		return
	}

	// All other backup routes require admin
	session := requireAdmin(w, r)
	if session == nil {
		return
	}

	// ── GET routes ──
	if method == "GET" {
		switch {
		// GET /api/backup/devices
		case urlPath == "/api/backup/devices":
			devices, err := dbBackupDeviceList()
			if err != nil {
				jsonError(w, 500, err.Error())
				return
			}
			enrichDevicesWithStatus(devices)
			// SECURITY: Don't leak sensitive fields to the frontend
			for _, d := range devices {
				delete(d, "pairTokenHash")
				delete(d, "pairTokenOutbound")
				delete(d, "sshHostKey")
			}
			jsonOk(w, map[string]interface{}{"devices": devices})

		// GET /api/backup/devices/:id/status
		case strings.HasSuffix(urlPath, "/status") && strings.HasPrefix(urlPath, "/api/backup/devices/"):
			id := extractPathSegment(urlPath, "/api/backup/devices/", "/status")
			dev, err := dbBackupDeviceGet(id)
			if err != nil {
				jsonError(w, 404, err.Error())
				return
			}
			status := checkDeviceStatus(dev)
			jsonOk(w, status)

		// GET /api/backup/jobs
		case urlPath == "/api/backup/jobs":
			jobs, err := dbBackupJobList()
			if err != nil {
				jsonError(w, 500, err.Error())
				return
			}
			jsonOk(w, map[string]interface{}{"jobs": jobs})

		// GET /api/backup/jobs/:id/status
		case strings.HasSuffix(urlPath, "/status") && strings.HasPrefix(urlPath, "/api/backup/jobs/"):
			id := extractPathSegment(urlPath, "/api/backup/jobs/", "/status")
			job, err := dbBackupJobGet(id)
			if err != nil {
				jsonError(w, 404, err.Error())
				return
			}
			// Return current status + running state
			backupRunningJobsMu.Lock()
			running := backupRunningJobs[id]
			backupRunningJobsMu.Unlock()
			result := map[string]interface{}{
				"status":  job["status"],
				"lastRun": job["lastRun"],
				"nextRun": job["nextRun"],
				"running": running,
			}
			jsonOk(w, result)

		// GET /api/backup/history
		case urlPath == "/api/backup/history":
			deviceID := r.URL.Query().Get("deviceId")
			limitStr := r.URL.Query().Get("limit")
			limit := 50
			if limitStr != "" {
				limit = parseInt(limitStr, 50)
			}
			history, err := dbBackupHistoryList(deviceID, limit)
			if err != nil {
				jsonError(w, 500, err.Error())
				return
			}
			jsonOk(w, map[string]interface{}{"history": history})

		// GET /api/backup/snapshots
		case urlPath == "/api/backup/snapshots":
			pool := r.URL.Query().Get("pool")
			jsonOk(w, listBackupSnapshots(pool))

		// GET /api/backup/discovered — auto-discovered NimOS devices on LAN
		case urlPath == "/api/backup/discovered":
			devices := getDiscoveredDevices()
			jsonOk(w, map[string]interface{}{"devices": devices})

		// GET /api/backup/devices/:id/remote-shares — list shares available on remote device
		case strings.HasSuffix(urlPath, "/remote-shares") && strings.HasPrefix(urlPath, "/api/backup/devices/"):
			id := extractPathSegment(urlPath, "/api/backup/devices/", "/remote-shares")
			dev, err := dbBackupDeviceGet(id)
			if err != nil {
				jsonError(w, 404, err.Error())
				return
			}
			shares, err := fetchRemoteShares(dev)
			if err != nil {
				jsonError(w, 500, err.Error())
				return
			}
			// Enrich with mount status
			mounted := listMountedRemoteShares(id)
			mountedMap := map[string]map[string]interface{}{}
			for _, m := range mounted {
				if sn, ok := m["shareName"].(string); ok {
					mountedMap[sn] = m
				}
			}
			for _, s := range shares {
				name, _ := s["name"].(string)
				if m, ok := mountedMap[name]; ok {
					s["mounted"] = m["mounted"]
					s["mountPoint"] = m["mountPoint"]
				} else {
					s["mounted"] = false
				}
			}
			jsonOk(w, map[string]interface{}{"shares": shares})

		// GET /api/backup/devices/:id/mounts — list currently mounted remote shares
		case strings.HasSuffix(urlPath, "/mounts") && strings.HasPrefix(urlPath, "/api/backup/devices/"):
			id := extractPathSegment(urlPath, "/api/backup/devices/", "/mounts")
			mounts := listMountedRemoteShares(id)
			jsonOk(w, map[string]interface{}{"mounts": mounts})

		// GET /api/backup/wg/* — WireGuard routes
		case strings.HasPrefix(urlPath, "/api/backup/wg/"):
			handleWGRoutes(w, r, urlPath, nil)

		default:
			jsonError(w, 404, "Not found")
		}
		return
	}

	// ── POST / PUT / DELETE routes ──
	if method == "POST" || method == "PUT" || method == "DELETE" {
		body, _ := readBody(r)

		switch {
		// POST /api/backup/devices — add paired device
		case urlPath == "/api/backup/devices" && method == "POST":
			name := bodyStr(body, "name")
			addr := bodyStr(body, "addr")
			devType := bodyStr(body, "type")

			// Clean addr: strip protocol, port, trailing slashes
			addr = strings.TrimSpace(addr)
			addr = strings.TrimPrefix(addr, "https://")
			addr = strings.TrimPrefix(addr, "http://")
			addr = strings.TrimRight(addr, "/")
			// Strip port if present (e.g., "nimosbarraca.duckdns.org:5009" → "nimosbarraca.duckdns.org")
			if idx := strings.LastIndex(addr, ":"); idx > 0 {
				portPart := addr[idx+1:]
				if _, err := strconv.Atoi(portPart); err == nil {
					addr = addr[:idx]
				}
			}

			if name == "" || addr == "" {
				jsonError(w, 400, "Name and addr are required")
				return
			}
			id := backupID("dev")
			dev := map[string]interface{}{
				"id":   id,
				"name": name,
				"addr": addr,
				"type": devType,
			}
			if purposes, ok := body["purposes"]; ok {
				dev["purposes"] = purposes
			}
			if err := dbBackupDeviceCreate(dev); err != nil {
				jsonError(w, 500, err.Error())
				return
			}

			// LOGIC-023: Generate pair token for secure inter-device auth
			pairToken, err := generatePairToken()
			if err != nil {
				jsonError(w, 500, "Failed to generate pair token")
				return
			}
			dbBackupDeviceSetPairToken(id, sha256Hex(pairToken))

			// If the remote sent us their pair token (mutual pairing), store it
			// so we can send it as X-Pair-Token when calling them
			if incomingToken := bodyStr(body, "pairToken"); incomingToken != "" {
				db.Exec(`UPDATE backup_devices SET pair_token_outbound = ? WHERE id = ?`, incomingToken, id)
			}

			// LOGIC-021: Fetch SSH host key for MITM protection during backup
			go func() {
				if hostKey, err := fetchSSHHostKey(addr); err == nil {
					dbBackupDeviceSetSSHHostKey(id, hostKey)
					logMsg("backup: stored SSH host key for %s (%s)", name, addr)
				} else {
					logMsg("backup: could not fetch SSH host key for %s: %v", addr, err)
				}
			}()

			// Return our token to caller — they store it and send it as X-Pair-Token
			jsonOk(w, map[string]interface{}{"ok": true, "id": id, "pairToken": pairToken})

		// DELETE /api/backup/devices/:id
		case strings.HasPrefix(urlPath, "/api/backup/devices/") && method == "DELETE":
			id := strings.TrimPrefix(urlPath, "/api/backup/devices/")
			id = strings.TrimSuffix(id, "/")
			if err := dbBackupDeviceDelete(id); err != nil {
				jsonError(w, 404, err.Error())
				return
			}
			jsonOk(w, map[string]interface{}{"ok": true})

		// POST /api/backup/devices/:id/purposes
		case strings.HasSuffix(urlPath, "/purposes") && strings.HasPrefix(urlPath, "/api/backup/devices/") && method == "POST":
			id := extractPathSegment(urlPath, "/api/backup/devices/", "/purposes")
			purposesRaw, ok := body["purposes"]
			if !ok {
				jsonError(w, 400, "Purposes array required")
				return
			}
			// Convert to []string
			var purposes []string
			if arr, ok := purposesRaw.([]interface{}); ok {
				for _, v := range arr {
					if s, ok := v.(string); ok {
						purposes = append(purposes, s)
					}
				}
			}
			if err := dbBackupDeviceUpdatePurposes(id, purposes); err != nil {
				jsonError(w, 404, err.Error())
				return
			}
			jsonOk(w, map[string]interface{}{"ok": true})

		// POST /api/backup/devices/:id/sync-pairs
		case strings.HasSuffix(urlPath, "/sync-pairs") && strings.HasPrefix(urlPath, "/api/backup/devices/") && method == "POST":
			id := extractPathSegment(urlPath, "/api/backup/devices/", "/sync-pairs")
			pairs, ok := body["syncPairs"]
			if !ok {
				jsonError(w, 400, "syncPairs required")
				return
			}
			if err := dbBackupDeviceUpdateSyncPairs(id, pairs); err != nil {
				jsonError(w, 500, err.Error())
				return
			}
			jsonOk(w, map[string]interface{}{"ok": true})

		// POST /api/backup/jobs — create job
		case urlPath == "/api/backup/jobs" && method == "POST":
			name := bodyStr(body, "name")
			deviceID := bodyStr(body, "deviceId")
			fsType := bodyStr(body, "fsType")
			source := bodyStr(body, "source")
			dest := bodyStr(body, "dest")

			if name == "" || deviceID == "" || fsType == "" || source == "" || dest == "" {
				jsonError(w, 400, "name, deviceId, fsType, source, and dest are required")
				return
			}

			// Validate device exists
			if _, err := dbBackupDeviceGet(deviceID); err != nil {
				jsonError(w, 404, "Device not found")
				return
			}

			// Validate fsType — Beta 8.1: solo BTRFS
			if fsType != "btrfs" {
				jsonError(w, 400, "fsType must be 'btrfs' (ZFS no longer supported)")
				return
			}

			// SECURITY (C1): reject malicious source/dest at creation time.
			if err := validateBackupPath("source", source); err != nil {
				jsonError(w, 400, err.Error())
				return
			}
			if err := validateBackupPath("dest", dest); err != nil {
				jsonError(w, 400, err.Error())
				return
			}

			id := backupID("job")
			job := map[string]interface{}{
				"id":        id,
				"name":      name,
				"deviceId":  deviceID,
				"fsType":    fsType,
				"source":    source,
				"dest":      dest,
				"schedule":  bodyStr(body, "schedule"),
				"retention": bodyStr(body, "retention"),
			}
			if err := dbBackupJobCreate(job); err != nil {
				jsonError(w, 500, err.Error())
				return
			}
			jsonOk(w, map[string]interface{}{"ok": true, "id": id})

		// PUT /api/backup/jobs/:id — edit job
		case strings.HasPrefix(urlPath, "/api/backup/jobs/") && method == "PUT":
			id := strings.TrimPrefix(urlPath, "/api/backup/jobs/")
			id = strings.TrimSuffix(id, "/")

			// SECURITY (C1): if the edit touches source/dest, re-validate them.
			if _, ok := body["source"]; ok {
				if err := validateBackupPath("source", bodyStr(body, "source")); err != nil {
					jsonError(w, 400, err.Error())
					return
				}
			}
			if _, ok := body["dest"]; ok {
				if err := validateBackupPath("dest", bodyStr(body, "dest")); err != nil {
					jsonError(w, 400, err.Error())
					return
				}
			}

			if err := dbBackupJobUpdate(id, body); err != nil {
				jsonError(w, 404, err.Error())
				return
			}
			// Recalculate next run if schedule changed
			if _, ok := body["schedule"]; ok {
				schedule := bodyStr(body, "schedule")
				if schedule != "" {
					dbBackupJobUpdate(id, map[string]interface{}{"nextRun": computeNextRun(schedule)})
				}
			}
			jsonOk(w, map[string]interface{}{"ok": true})

		// DELETE /api/backup/jobs/:id
		case strings.HasPrefix(urlPath, "/api/backup/jobs/") && method == "DELETE":
			id := strings.TrimPrefix(urlPath, "/api/backup/jobs/")
			id = strings.TrimSuffix(id, "/")
			if err := dbBackupJobDelete(id); err != nil {
				jsonError(w, 404, err.Error())
				return
			}
			jsonOk(w, map[string]interface{}{"ok": true})

		// POST /api/backup/run/:id — execute job manually
		case strings.HasPrefix(urlPath, "/api/backup/run/") && method == "POST":
			id := strings.TrimPrefix(urlPath, "/api/backup/run/")
			id = strings.TrimSuffix(id, "/")
			job, err := dbBackupJobGet(id)
			if err != nil {
				jsonError(w, 404, err.Error())
				return
			}
			// Run in background, return immediately
			go executeBackupJob(job)
			jsonOk(w, map[string]interface{}{"ok": true, "message": "Backup started"})

		// POST /api/backup/pair/scan — scan LAN for NimOS devices (also refreshes auto-discovery)
		case urlPath == "/api/backup/pair/scan" && method == "POST":
			subnet := bodyStr(body, "subnet")
			localAddrs := getLocalAddrs()
			devices := scanLANForNimOS(subnet)
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
			// Update discovery cache
			discoveredDevicesMu.Lock()
			discoveredDevices = filtered
			discoveredDevicesMu.Unlock()
			jsonOk(w, map[string]interface{}{"devices": filtered})

		// POST /api/backup/pair/connect — initiate pairing with remote device
		case urlPath == "/api/backup/pair/connect" && method == "POST":
			addr := bodyStr(body, "addr")
			username := bodyStr(body, "username")
			password := bodyStr(body, "password")
			totpCode := bodyStr(body, "totpCode")

			// Clean addr: strip protocol, port, trailing slashes
			addr = strings.TrimSpace(addr)
			addr = strings.TrimPrefix(addr, "https://")
			addr = strings.TrimPrefix(addr, "http://")
			addr = strings.TrimRight(addr, "/")
			if idx := strings.LastIndex(addr, ":"); idx > 0 {
				portPart := addr[idx+1:]
				if _, err := strconv.Atoi(portPart); err == nil {
					addr = addr[:idx]
				}
			}

			if addr == "" || username == "" || password == "" {
				jsonError(w, 400, "addr, username, and password are required")
				return
			}
			result := pairWithRemote(addr, username, password, totpCode)
			if errMsg, ok := result["error"].(string); ok && errMsg != "" {
				jsonError(w, 400, errMsg)
				return
			}
			jsonOk(w, result)

		// POST /api/backup/pair/update-addr — remote tells us to use tunnel IP
		case urlPath == "/api/backup/pair/update-addr" && method == "POST":
			tunnelAddr := bodyStr(body, "tunnelAddr")
			if tunnelAddr == "" {
				jsonError(w, 400, "tunnelAddr required")
				return
			}
			// Find the device by the request's source IP and update its addr
			remoteIP := r.RemoteAddr
			if idx := strings.LastIndex(remoteIP, ":"); idx > 0 {
				remoteIP = remoteIP[:idx]
			}
			remoteIP = strings.Trim(remoteIP, "[]")

			devices, _ := dbBackupDeviceList()
			updated := false
			for _, d := range devices {
				dAddr, _ := d["addr"].(string)
				dID, _ := d["id"].(string)
				// Match by current addr (could be DDNS or IP)
				if dAddr == remoteIP || dAddr == tunnelAddr {
					dbBackupDeviceUpdate(dID, "addr", tunnelAddr)
					logMsg("wireguard: updated device %s addr to tunnel IP %s (requested by remote)", dID, tunnelAddr)
					updated = true
					break
				}
			}
			if !updated {
				// Try matching by any addr that isn't a local 192.168/10./172. addr
				for _, d := range devices {
					dAddr, _ := d["addr"].(string)
					dID, _ := d["id"].(string)
					if !isLocalAddr(dAddr) {
						dbBackupDeviceUpdate(dID, "addr", tunnelAddr)
						logMsg("wireguard: updated WAN device %s addr to tunnel IP %s", dID, tunnelAddr)
						updated = true
						break
					}
				}
			}
			jsonOk(w, map[string]interface{}{"ok": true, "updated": updated})

		// POST /api/backup/snapshots — create manual backup snapshot
		case urlPath == "/api/backup/snapshots" && method == "POST":
			source := bodyStr(body, "source")
			fsType := bodyStr(body, "fsType")
			if source == "" || fsType == "" {
				jsonError(w, 400, "source and fsType required")
				return
			}
			result := createBackupSnapshot(source, fsType)
			jsonOk(w, result)

		// DELETE /api/backup/snapshots/:name
		case strings.HasPrefix(urlPath, "/api/backup/snapshots/") && method == "DELETE":
			name := strings.TrimPrefix(urlPath, "/api/backup/snapshots/")
			name = strings.TrimSuffix(name, "/")
			fsType := bodyStr(body, "fsType")
			source := bodyStr(body, "source")
			result := deleteBackupSnapshot(name, fsType, source)
			jsonOk(w, result)

		// POST /api/backup/devices/:id/mount — mount a remote share
		case strings.HasSuffix(urlPath, "/mount") && strings.HasPrefix(urlPath, "/api/backup/devices/") && method == "POST":
			id := extractPathSegment(urlPath, "/api/backup/devices/", "/mount")
			dev, err := dbBackupDeviceGet(id)
			if err != nil {
				jsonError(w, 404, err.Error())
				return
			}
			shareName := bodyStr(body, "shareName")
			remotePath := bodyStr(body, "remotePath")
			if shareName == "" || remotePath == "" {
				jsonError(w, 400, "shareName and remotePath required")
				return
			}
			devName, _ := dev["name"].(string)
			devAddr, _ := dev["addr"].(string)
			result := mountRemoteShare(id, devName, devAddr, shareName, remotePath)
			if errMsg, ok := result["error"].(string); ok && errMsg != "" {
				jsonError(w, 500, errMsg)
				return
			}
			jsonOk(w, result)

		// POST /api/backup/devices/:id/unmount — unmount a remote share
		case strings.HasSuffix(urlPath, "/unmount") && strings.HasPrefix(urlPath, "/api/backup/devices/") && method == "POST":
			id := extractPathSegment(urlPath, "/api/backup/devices/", "/unmount")
			shareName := bodyStr(body, "shareName")
			if shareName == "" {
				jsonError(w, 400, "shareName required")
				return
			}
			result := unmountRemoteShare(id, shareName)
			if errMsg, ok := result["error"].(string); ok && errMsg != "" {
				jsonError(w, 500, errMsg)
				return
			}
			jsonOk(w, result)

		// POST/DELETE /api/backup/wg/* and /api/backup/pair/wg-* — WireGuard routes
		case strings.HasPrefix(urlPath, "/api/backup/wg/") || strings.HasPrefix(urlPath, "/api/backup/pair/wg-"):
			handleWGRoutes(w, r, urlPath, body)

		default:
			jsonError(w, 404, "Not found")
		}
		return
	}

	jsonError(w, 405, "Method not allowed")
}

// ─── URL Helpers ────────────────────────────────────────────────────────────

// extractPathSegment extracts the segment between prefix and suffix from a URL path.
// E.g., extractPathSegment("/api/backup/devices/dev_123/status", "/api/backup/devices/", "/status") → "dev_123"
func extractPathSegment(path, prefix, suffix string) string {
	s := strings.TrimPrefix(path, prefix)
	s = strings.TrimSuffix(s, suffix)
	s = strings.TrimSuffix(s, "/")
	return s
}

// ─── Pairing ────────────────────────────────────────────────────────────────

// ─── Backup Snapshots ───────────────────────────────────────────────────────

// snapshotRetentionMax es el número máximo de snapshots nimbackup-* que se
// conservan por pool. Al crear uno nuevo, los que excedan este número (los más
// viejos) se borran. Acotado a propósito (P4): retention básica, no un
// scheduler de políticas completo (eso sería su propio frente).
const snapshotRetentionMax = 10

// ─── Remote Shares (NFS mount) ──────────────────────────────────────────────

const remoteMountBase = "/nimos/remote"

// ─── Mount Records (SQLite) ─────────────────────────────────────────────────

func removeMountRecord(deviceID, shareName string) {
	db.Exec(`DELETE FROM remote_mounts WHERE device_id = ? AND share_name = ?`, deviceID, shareName)
}

// remountAllOnStartup re-mounts all saved remote shares on daemon start.
func remountAllOnStartup() {
	rows, err := db.Query(`SELECT device_id, share_name, remote_path, mount_point, device_addr FROM remote_mounts`)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var deviceID, shareName, remotePath, mountPoint, addr string
		if rows.Scan(&deviceID, &shareName, &remotePath, &mountPoint, &addr) != nil {
			continue
		}
		// Only remount if not already mounted
		if _, ok := runSafe("mountpoint", "-q", mountPoint); ok {
			continue
		}
		os.MkdirAll(mountPoint, 0755)
		if _, ok := runSafe("mount", "-t", "nfs", "-o", "soft,timeo=50,retrans=3,nolock", addr+":"+remotePath, mountPoint); ok {
			logMsg("remote-share: remounted %s:%s → %s", addr, remotePath, mountPoint)
		}
	}
}

// ─── NFS Export Management ──────────────────────────────────────────────────
// These functions manage /etc/exports so that paired devices can mount our shares.

const exportsFile = "/etc/exports"
const nimosExportMarker = "# NimOS-managed"

// lookupNimosIDs returns the UID/GID of the nimos user, falling back to
// safe defaults (1000/1000) if the user doesn't exist. Used for NFS
// anonymous user mapping so remote devices never get root on our shares.
func lookupNimosIDs() (int, int) {
	uid, gid := 1000, 1000
	if u, err := user.Lookup("nimos"); err == nil {
		if parsed, err := strconv.Atoi(u.Uid); err == nil {
			uid = parsed
		}
		if parsed, err := strconv.Atoi(u.Gid); err == nil {
			gid = parsed
		}
	}
	return uid, gid
}

// addNFSExport adds a path to /etc/exports for a specific client IP.
// Only adds if not already exported. Runs exportfs -ra to apply.
func addNFSExport(path, clientIP string) error {
	// SECURITY (A1): clientIP is written verbatim into /etc/exports. A crafted
	// value like "* (rw,no_root_squash) #" would rewrite the export options and
	// defeat the squash. Accept only a bare IP or a CIDR range; reject anything
	// else before touching the file.
	if !isValidNFSClient(clientIP) {
		return fmt.Errorf("invalid client address: %q", clientIP)
	}

	// Ensure NFS server is installed
	runShellStatic("which exportfs >/dev/null 2>&1 || apt-get install -y -qq nfs-kernel-server 2>/dev/null")

	// Read current exports
	data, err := os.ReadFile(exportsFile)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read exports: %v", err)
	}
	content := string(data)

	// SECURITY (LOGIC-022): use root_squash + all_squash with anonymous mapping
	// to the nimos user. Previous versions used no_root_squash which gave the
	// remote device root privileges over our exported files — if the paired
	// device was compromised it would have full root on our shares. all_squash
	// maps every remote user (including root) to the unprivileged nimos user,
	// which is the same user that owns the share directories.
	nimosUID, nimosGID := lookupNimosIDs()
	exportLine := fmt.Sprintf("%s %s(rw,sync,no_subtree_check,root_squash,all_squash,anonuid=%d,anongid=%d) %s",
		path, clientIP, nimosUID, nimosGID, nimosExportMarker)
	if strings.Contains(content, fmt.Sprintf("%s %s(", path, clientIP)) {
		// Already exported
		runSafe("exportfs", "-ra")
		return nil
	}

	// Append the export
	if !strings.HasSuffix(content, "\n") && content != "" {
		content += "\n"
	}
	content += exportLine + "\n"

	if err := os.WriteFile(exportsFile, []byte(content), 0644); err != nil {
		return fmt.Errorf("write exports: %v", err)
	}

	// Apply exports
	out, ok := runSafe("exportfs", "-ra")
	if !ok {
		logMsg("nfs: exportfs -ra failed: %s", out)
		// Try starting the NFS server
		runShellStatic("systemctl start nfs-kernel-server 2>/dev/null || service nfs-kernel-server start 2>/dev/null")
		runSafe("exportfs", "-ra")
	}

	logMsg("nfs: exported %s for %s", path, clientIP)
	return nil
}

// removeNFSExport removes a path from /etc/exports for a specific client IP.
func removeNFSExport(path, clientIP string) {
	data, err := os.ReadFile(exportsFile)
	if err != nil {
		return
	}

	lines := strings.Split(string(data), "\n")
	var kept []string
	for _, line := range lines {
		// Remove lines matching this path + clientIP
		if strings.HasPrefix(strings.TrimSpace(line), path+" ") && strings.Contains(line, clientIP) {
			continue
		}
		kept = append(kept, line)
	}

	os.WriteFile(exportsFile, []byte(strings.Join(kept, "\n")), 0644)
	runSafe("exportfs", "-ra")
	logMsg("nfs: unexported %s for %s", path, clientIP)
}

// ensureNFSServer makes sure NFS server is running.
func ensureNFSServer() {
	// Check if running
	if out, _ := runSafe("systemctl", "is-active", "nfs-kernel-server"); strings.TrimSpace(out) == "active" {
		return
	}
	// Start it
	runSafe("systemctl", "enable", "nfs-kernel-server")
	runSafe("systemctl", "start", "nfs-kernel-server")
	logMsg("nfs: started nfs-kernel-server")
}

// handleNFSExport handles the /api/backup/nfs-export endpoint.
// Called by a paired device requesting us to export a path for their IP.
// Auth: verifies pair token (preferred) or falls back to IP check for legacy devices.
func handleNFSExport(w http.ResponseWriter, r *http.Request) {
	if dev := verifyPairedDevice(r); dev == nil {
		jsonError(w, 403, "not a paired device")
		return
	}

	body, err := readBody(r)
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}

	path := bodyStr(body, "path")
	clientIP := bodyStr(body, "clientIP")
	if path == "" || clientIP == "" {
		jsonError(w, 400, "path and clientIP required")
		return
	}

	// Security: verify the path is actually a share we own
	shares, _ := dbSharesListRaw()
	validPath := false
	for _, s := range shares {
		if s.Path == path {
			validPath = true
			break
		}
	}
	if !validPath {
		jsonError(w, 403, "path is not a shared folder")
		return
	}

	ensureNFSServer()

	if err := addNFSExport(path, clientIP); err != nil {
		jsonError(w, 500, err.Error())
		return
	}

	jsonOk(w, map[string]interface{}{"ok": true, "exported": path, "client": clientIP})
}

// ─── Disk Usage Helpers ─────────────────────────────────────────────────────

// getPathDiskUsage returns used and total bytes for the filesystem containing the given path.
// For ZFS datasets it uses zfs get, for others it uses df.
func getPathDiskUsage(path string) (int64, int64) {
	// Try df first
	if out, ok := runSafe("df", "-B1", "--output=used,size", path); ok && out != "" {
		lines := strings.Split(out, "\n")
		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				used := parseByteSize(fields[0])
				total := parseByteSize(fields[1])
				if total > 0 {
					return used, total
				}
			}
		}
	}
	// Fallback: try du for used + df for total
	var used, total int64
	if out, ok := runSafe("du", "-sb", path); ok {
		fields := strings.Fields(out)
		if len(fields) >= 1 {
			used = parseByteSize(fields[0])
		}
	}
	if out, ok := runSafe("df", "-B1", "--output=size", path); ok {
		lines := strings.Split(out, "\n")
		for _, line := range lines {
			v := strings.TrimSpace(line)
			if v != "" && v != "1B-blocks" {
				total = parseByteSize(v)
				break
			}
		}
	}
	return used, total
}
