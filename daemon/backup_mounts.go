package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// mountRemoteShare mounts a remote NFS share locally.
// First requests the remote NAS to export the path via NFS for our IP,
// then mounts it locally.
func mountRemoteShare(deviceID, deviceName, deviceAddr, shareName, remotePath string) map[string]interface{} {
	// SECURITY: validar remotePath antes de pasarlo a mount. Aunque runSafe es
	// argv-separado (sin shell), un remotePath con metacaracteres o no-absoluto
	// no tiene sentido y podría confundir al cliente NFS. Defensa en profundidad.
	if remotePath == "" || !strings.HasPrefix(remotePath, "/") ||
		strings.ContainsAny(remotePath, ";|&`$'\"\\<>(){}*?\n\r") ||
		strings.Contains(remotePath, "..") {
		return map[string]interface{}{"error": "invalid remote path"}
	}

	// Sanitize names for filesystem
	safeDev := regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(deviceName, "_")
	safeShare := regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(shareName, "_")

	mountPoint := fmt.Sprintf("%s/%s/%s", remoteMountBase, safeDev, safeShare)

	// Create mount point
	os.MkdirAll(mountPoint, 0755)

	// Check if already mounted
	if _, ok := runSafe("mountpoint", "-q", mountPoint); ok {
		return map[string]interface{}{"ok": true, "mountPoint": mountPoint, "message": "Already mounted"}
	}

	// Step 1: Ask the remote NAS to export this path for our IP
	ourIP := getLocalLANAddr(deviceAddr)
	proto := "http"
	port := "5000"
	if !isLocalAddr(deviceAddr) {
		proto = "https"
		port = "5009"
	}

	client := &http.Client{Timeout: 10 * time.Second}
	exportPayload, _ := json.Marshal(map[string]string{
		"path":     remotePath,
		"clientIP": ourIP,
	})
	// LOGIC-023: Send pair token for authentication
	exportReq, _ := http.NewRequest("POST",
		fmt.Sprintf("%s://%s:%s/api/backup/nfs-export", proto, deviceAddr, port),
		strings.NewReader(string(exportPayload)))
	exportReq.Header.Set("Content-Type", "application/json")
	if outToken := getOutboundPairToken(deviceID); outToken != "" {
		exportReq.Header.Set("X-Pair-Token", outToken)
	}
	exportResp, exportErr := client.Do(exportReq)
	if exportErr != nil {
		logMsg("remote-share: failed to request NFS export from %s: %v", deviceAddr, exportErr)
	} else {
		exportResp.Body.Close()
	}

	// Brief wait for NFS export to take effect
	time.Sleep(500 * time.Millisecond)

	// Step 2: Ensure NFS client is available locally
	runShellStatic("which mount.nfs >/dev/null 2>&1 || apt-get install -y -qq nfs-common 2>/dev/null")

	// Step 3: Mount via NFS
	out, ok := runSafe("mount", "-t", "nfs", "-o", "soft,timeo=50,retrans=3,nolock",
		deviceAddr+":"+remotePath, mountPoint)
	if !ok {
		// Fallback: try CIFS/SMB mount
		out2, ok2 := runSafe("mount", "-t", "cifs",
			"//"+deviceAddr+"/"+shareName, mountPoint, "-o", "guest,vers=3.0,soft")
		if !ok2 {
			return map[string]interface{}{
				"error": fmt.Sprintf("NFS failed: %s | SMB failed: %s", out, out2),
			}
		}
	}

	logMsg("remote-share: mounted %s:%s → %s", deviceAddr, remotePath, mountPoint)

	// Save mount info for persistence
	saveMountRecord(deviceID, shareName, remotePath, mountPoint, deviceAddr)

	return map[string]interface{}{
		"ok":         true,
		"mountPoint": mountPoint,
	}
}

// unmountRemoteShare unmounts a remote share.
func unmountRemoteShare(deviceID, shareName string) map[string]interface{} {
	record := getMountRecord(deviceID, shareName)
	if record == nil {
		return map[string]interface{}{"error": "mount not found"}
	}

	mountPoint, _ := record["mountPoint"].(string)
	if mountPoint == "" {
		return map[string]interface{}{"error": "no mount point"}
	}

	out, ok := runSafe("umount", mountPoint)
	if !ok {
		// Force unmount
		runSafe("umount", "-f", mountPoint)
	}
	_ = out

	// Clean up empty directory
	os.Remove(mountPoint) // rmdir equivalent for empty dirs

	removeMountRecord(deviceID, shareName)

	logMsg("remote-share: unmounted %s/%s", deviceID, shareName)
	return map[string]interface{}{"ok": true}
}

// listMountedRemoteShares returns all currently mounted remote shares for a device.
func listMountedRemoteShares(deviceID string) []map[string]interface{} {
	deviceStatusCacheMu.RLock()
	defer deviceStatusCacheMu.RUnlock()
	// Read from mount records in DB
	rows, err := db.Query(`SELECT share_name, remote_path, mount_point, device_addr
		FROM remote_mounts WHERE device_id = ?`, deviceID)
	if err != nil {
		return []map[string]interface{}{}
	}
	defer rows.Close()

	var mounts []map[string]interface{}
	for rows.Next() {
		var shareName, remotePath, mountPoint, addr string
		if rows.Scan(&shareName, &remotePath, &mountPoint, &addr) != nil {
			continue
		}
		// Check if still mounted
		mounted := false
		if _, ok := runSafe("mountpoint", "-q", mountPoint); ok {
			mounted = true
		}
		mounts = append(mounts, map[string]interface{}{
			"shareName":  shareName,
			"remotePath": remotePath,
			"mountPoint": mountPoint,
			"mounted":    mounted,
		})
	}
	if mounts == nil {
		mounts = []map[string]interface{}{}
	}
	return mounts
}
