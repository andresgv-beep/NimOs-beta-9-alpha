package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// generatePairToken creates a 32-byte hex token for device pairing.
func generatePairToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}

// verifyPairToken checks if the X-Pair-Token header matches any paired device.
// Returns the matched device map or nil if no match.
func verifyPairToken(r *http.Request) map[string]interface{} {
	token := r.Header.Get("X-Pair-Token")
	if token == "" {
		return nil
	}
	tokenHash := sha256Hex(token)
	devices, _ := dbBackupDeviceList()
	for _, d := range devices {
		// SECURITY: comparación en tiempo constante para no filtrar información
		// por timing. Aunque comparamos hashes SHA-256 (no el token crudo), usar
		// subtle.ConstantTimeCompare es la práctica correcta y barata.
		if h, _ := d["pairTokenHash"].(string); h != "" &&
			subtle.ConstantTimeCompare([]byte(h), []byte(tokenHash)) == 1 {
			return d
		}
	}
	return nil
}

// verifyPairedDevice checks if request comes from a paired device.
// First checks X-Pair-Token header (preferred), then falls back to IP match.
func verifyPairedDevice(r *http.Request) map[string]interface{} {
	if dev := verifyPairToken(r); dev != nil {
		return dev
	}
	// SECURITY (A2): IP-based fallback is opt-in per device. By default a device
	// authenticates ONLY via its pair token. The fallback (matching the source
	// IP against a known device addr) is trivially spoofable on a LAN, so it is
	// applied only to devices that explicitly set allow_ip_auth = 1.
	remoteIP := r.RemoteAddr
	if idx := strings.LastIndex(remoteIP, ":"); idx > 0 {
		remoteIP = remoteIP[:idx]
	}
	remoteIP = strings.Trim(remoteIP, "[]")
	devices, _ := dbBackupDeviceList()
	for _, d := range devices {
		allow, _ := d["allowIpAuth"].(bool)
		if !allow {
			continue
		}
		if addr, _ := d["addr"].(string); addr == remoteIP {
			logMsg("backup: device %v authenticated via IP fallback (allow_ip_auth on)", d["id"])
			return d
		}
	}
	return nil
}

// getOutboundPairToken retrieves the raw pair token to send when calling a remote device.
// This is the token the remote gave us during pairing — we send it as X-Pair-Token.
func getOutboundPairToken(deviceID string) string {
	var token string
	db.QueryRow(`SELECT pair_token_outbound FROM backup_devices WHERE id = ?`, deviceID).Scan(&token)
	return token
}

// fetchSSHHostKey retrieves the SSH host key from a remote host using ssh-keyscan.
func fetchSSHHostKey(addr string) (string, error) {
	out, ok := runSafe("ssh-keyscan", "-t", "ed25519,rsa", "-T", "5", addr)
	if !ok || strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("ssh-keyscan failed for %s", addr)
	}
	return strings.TrimSpace(out), nil
}

// writeKnownHostsFile writes a per-device known_hosts file for SSH.
// Returns the path to the file, or "" if no host key is stored.
func writeKnownHostsFile(deviceID string) string {
	devices, _ := dbBackupDeviceList()
	for _, d := range devices {
		if id, _ := d["id"].(string); id == deviceID {
			hostKey, _ := d["sshHostKey"].(string)
			if hostKey == "" {
				return ""
			}
			khDir := "/var/lib/nimos/ssh"
			os.MkdirAll(khDir, 0700)
			khPath := fmt.Sprintf("%s/known_hosts_%s", khDir, deviceID)
			os.WriteFile(khPath, []byte(hostKey+"\n"), 0600)
			return khPath
		}
	}
	return ""
}

// sshOptsForDevice returns SSH options string for a backup device.
// If host key is stored, uses StrictHostKeyChecking=yes with per-device known_hosts.
// Otherwise falls back to StrictHostKeyChecking=no (legacy/first-time).
func sshOptsForDevice(deviceID string) string {
	khPath := writeKnownHostsFile(deviceID)
	if khPath != "" {
		return fmt.Sprintf("-o StrictHostKeyChecking=yes -o UserKnownHostsFile=%s -o ConnectTimeout=30", khPath)
	}
	// Fallback for devices without stored host key (paired before LOGIC-021)
	return "-o StrictHostKeyChecking=no -o ConnectTimeout=30"
}

func pairWithRemote(addr, username, password, totpCode string) map[string]interface{} {
	// Determine protocol + port
	proto := "http"
	port := "5000"
	if !isLocalAddr(addr) {
		proto = "https"
		port = "5009"
	}

	baseURL := fmt.Sprintf("%s://%s:%s", proto, addr, port)
	client := &http.Client{Timeout: 10 * time.Second}

	// Step 1: Authenticate with remote
	loginBody := map[string]string{
		"username": username,
		"password": password,
	}
	if totpCode != "" {
		loginBody["totpCode"] = totpCode
	}

	loginJSON, _ := json.Marshal(loginBody)
	resp, err := client.Post(baseURL+"/api/auth/login", "application/json", strings.NewReader(string(loginJSON)))
	if err != nil {
		return map[string]interface{}{"error": "Cannot reach remote device: " + err.Error()}
	}
	defer resp.Body.Close()

	var loginResp map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&loginResp)

	// Check if 2FA is required
	if requires2FA, _ := loginResp["requires2FA"].(bool); requires2FA {
		return map[string]interface{}{
			"requires2FA": true,
			"message":     "Enter TOTP code to continue",
		}
	}

	// Check for errors
	if errMsg, _ := loginResp["error"].(string); errMsg != "" {
		return map[string]interface{}{"error": "Authentication failed: " + errMsg}
	}

	token, _ := loginResp["token"].(string)
	if token == "" {
		return map[string]interface{}{"error": "No token received from remote"}
	}

	// Step 2: Get remote device info
	req, _ := http.NewRequest("GET", baseURL+"/api/auth/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	infoResp, err := client.Do(req)
	if err != nil {
		return map[string]interface{}{"error": "Cannot get remote info: " + err.Error()}
	}
	defer infoResp.Body.Close()

	var info map[string]interface{}
	json.NewDecoder(infoResp.Body).Decode(&info)

	remoteName := "NimOS"
	if h, ok := info["hostname"].(string); ok && h != "" {
		remoteName = h
	}
	remoteVersion := "unknown"
	if v, ok := info["version"].(string); ok && v != "" {
		remoteVersion = v
	}

	// Step 3: Register device locally
	id := backupID("dev")
	dev := map[string]interface{}{
		"id":   id,
		"name": remoteName,
		"addr": addr,
		"type": "nas",
	}
	if err := dbBackupDeviceCreate(dev); err != nil {
		return map[string]interface{}{"error": "Failed to save device: " + err.Error()}
	}

	// LOGIC-023: Generate a pair token for verifying our outbound requests
	localPairToken, _ := generatePairToken()
	dbBackupDeviceSetPairToken(id, sha256Hex(localPairToken))

	// Step 3b: Register ourselves on the remote NAS (mutual pairing)
	// Send our local pair token so the remote can verify our future requests
	localName := getLocalHostname()
	localAddr := getLocalLANAddr(addr)
	remoteDevPayload, _ := json.Marshal(map[string]interface{}{
		"name":      localName,
		"addr":      localAddr,
		"type":      "nas",
		"pairToken": localPairToken,
	})
	regReq, _ := http.NewRequest("POST", baseURL+"/api/backup/devices", strings.NewReader(string(remoteDevPayload)))
	regReq.Header.Set("Authorization", "Bearer "+token)
	regReq.Header.Set("Content-Type", "application/json")
	regResp, regErr := client.Do(regReq)
	var remotePairToken string
	if regErr != nil {
		logMsg("backup: mutual pairing failed: %v (one-way pairing still valid)", regErr)
	} else {
		// Capture the remote's pair token for our outbound requests to them
		var regData map[string]interface{}
		json.NewDecoder(regResp.Body).Decode(&regData)
		regResp.Body.Close()
		if pt, ok := regData["pairToken"].(string); ok && pt != "" {
			remotePairToken = pt
		}
		logMsg("backup: mutual pairing OK — registered '%s' on remote %s", localName, addr)
	}

	// Store the remote's pair token so we can send it in X-Pair-Token header
	if remotePairToken != "" {
		db.Exec(`UPDATE backup_devices SET pair_token_outbound = ? WHERE id = ?`,
			remotePairToken, id)
	}

	result := map[string]interface{}{
		"ok":      true,
		"id":      id,
		"name":    remoteName,
		"addr":    addr,
		"version": remoteVersion,
	}

	// Step 4: If WAN connection, set up WireGuard tunnel
	if !isLocalAddr(addr) {
		wgResult, err := initiateWGPairing(id, addr, token)
		if err != nil {
			logMsg("wireguard: pairing failed for %s: %v (device saved without WG)", addr, err)
			result["wireguard"] = map[string]interface{}{"error": err.Error()}
		} else {
			result["wireguard"] = wgResult

			// Update local device addr to the remote's tunnel IP
			// (so we use the tunnel to reach them from now on)
			if remoteIP, ok := wgResult["remoteIP"].(string); ok && remoteIP != "" {
				dbBackupDeviceUpdate(id, "addr", remoteIP)
				result["addr"] = remoteIP
				logMsg("wireguard: updated device %s addr to tunnel IP %s", id, remoteIP)
			}

			// Tell the remote to update their record of us to our tunnel IP
			if localIP, ok := wgResult["localIP"].(string); ok && localIP != "" {
				updatePayload, _ := json.Marshal(map[string]interface{}{
					"tunnelAddr": localIP,
				})
				updReq, _ := http.NewRequest("POST", baseURL+"/api/backup/pair/update-addr", strings.NewReader(string(updatePayload)))
				updReq.Header.Set("Authorization", "Bearer "+token)
				updReq.Header.Set("Content-Type", "application/json")
				if updResp, err := client.Do(updReq); err == nil {
					updResp.Body.Close()
					logMsg("wireguard: notified remote to use tunnel IP %s for us", localIP)
				}
			}
		}
	}

	// LOGIC-021: Fetch SSH host key for MITM protection during backup
	go func() {
		if hostKey, err := fetchSSHHostKey(addr); err == nil {
			dbBackupDeviceSetSSHHostKey(id, hostKey)
			logMsg("backup: stored SSH host key for %s (%s)", remoteName, addr)
		} else {
			logMsg("backup: could not fetch SSH host key for %s: %v", addr, err)
		}
	}()

	return result
}

// fetchRemoteShares queries the shares list from a remote NimOS device.
// Uses the pairing credentials to authenticate.
func fetchRemoteShares(device map[string]interface{}) ([]map[string]interface{}, error) {
	addr, _ := device["addr"].(string)
	if addr == "" {
		return nil, fmt.Errorf("device has no address")
	}

	proto := "http"
	port := "5000"
	if !isLocalAddr(addr) {
		proto = "https"
		port = "5009"
	}

	client := &http.Client{Timeout: 5 * time.Second}

	// LOGIC-023: Send pair token for authentication
	url := fmt.Sprintf("%s://%s:%s/api/backup/public-shares", proto, addr, port)
	req, _ := http.NewRequest("GET", url, nil)
	if outToken, _ := device["pairTokenOutbound"].(string); outToken != "" {
		req.Header.Set("X-Pair-Token", outToken)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach remote: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("remote returned status %d", resp.StatusCode)
	}

	var data interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("invalid response: %v", err)
	}

	// Response can be an array or { "shares": [...] }
	var shares []map[string]interface{}
	switch v := data.(type) {
	case []interface{}:
		for _, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				shares = append(shares, map[string]interface{}{
					"name":        m["name"],
					"displayName": m["displayName"],
					"description": m["description"],
					"path":        m["path"],
					"pool":        m["pool"],
					"used":        m["used"],
					"total":       m["total"],
				})
			}
		}
	case map[string]interface{}:
		if arr, ok := v["shares"].([]interface{}); ok {
			for _, item := range arr {
				if m, ok := item.(map[string]interface{}); ok {
					shares = append(shares, map[string]interface{}{
						"name":        m["name"],
						"displayName": m["displayName"],
						"description": m["description"],
						"path":        m["path"],
						"pool":        m["pool"],
						"used":        m["used"],
						"total":       m["total"],
					})
				}
			}
		}
	}

	if shares == nil {
		shares = []map[string]interface{}{}
	}
	return shares, nil
}

// getPublicShares returns this NAS's shares in a simplified format for paired devices.
// Auth: verifies pair token (preferred) or falls back to IP check for legacy devices.
func getPublicShares(r *http.Request) map[string]interface{} {
	if dev := verifyPairedDevice(r); dev == nil {
		return map[string]interface{}{"error": "not a paired device"}
	}

	dbShares, err := dbSharesListRaw()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}

	// Build enriched views with quota data (BTRFS · Beta 8.1)
	views := buildShareViews(r.Context(), dbShares)

	// Return simplified share info with disk usage
	var result []map[string]interface{}
	for _, v := range views {
		entry := map[string]interface{}{
			"name":        v.Name,
			"displayName": v.DisplayName,
			"description": v.Description,
			"path":        v.Path,
			"pool":        v.Pool,
		}
		// Quota = total capacity for this share, used = actual usage
		if v.Quota > 0 {
			entry["total"] = v.Quota
		} else if v.Available > 0 {
			entry["total"] = v.Used + v.Available
		}
		if v.Used > 0 {
			entry["used"] = v.Used
		}
		result = append(result, entry)
	}
	if result == nil {
		result = []map[string]interface{}{}
	}

	return map[string]interface{}{"shares": result}
}
