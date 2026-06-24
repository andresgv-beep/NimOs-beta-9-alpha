package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// ═══════════════════════════════════════════════════════════════════════════════
// NimOS Backup — WireGuard Tunnel Management
// ═══════════════════════════════════════════════════════════════════════════════

const (
	wgConfigDir  = "/etc/wireguard"
	wgInterface  = "wg-nimbackup"
	wgListenPort = 51820
	wgSubnet     = "10.10.0"
)

// ─── Key Generation ─────────────────────────────────────────────────────────

func generateWGKeyPair() (privateKey, publicKey string, err error) {
	privOut, ok := runSafe("wg", "genkey")
	if !ok || privOut == "" {
		return "", "", fmt.Errorf("wg genkey failed (is wireguard-tools installed?)")
	}
	privateKey = strings.TrimSpace(privOut)

	pubOut, ok := runSafeInput(privateKey+"\n", "wg", "pubkey")
	if !ok || pubOut == "" {
		return "", "", fmt.Errorf("wg pubkey failed")
	}
	publicKey = strings.TrimSpace(pubOut)
	return privateKey, publicKey, nil
}

// ─── Config Management ─────────────────────────────────────────────────────

type WGPeerConfig struct {
	PublicKey  string `json:"publicKey"`
	Endpoint   string `json:"endpoint"`
	AllowedIPs string `json:"allowedIPs"`
	DeviceID   string `json:"deviceId"`
}

type wgState struct {
	PrivateKey string         `json:"privateKey"`
	PublicKey  string         `json:"publicKey"`
	ListenPort int            `json:"listenPort"`
	LocalIP    string         `json:"localIP"`
	Peers      []WGPeerConfig `json:"peers"`
	NextPeerIP int            `json:"nextPeerIP"`
}

const wgStatePath = "/var/lib/nimos/config/wireguard-state.json"

func loadWGState() (*wgState, error) {
	data, err := os.ReadFile(wgStatePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var state wgState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func saveWGState(state *wgState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(wgStatePath, data, 0600)
}

// initWGState initializes WireGuard state. The localIP is passed in:
// - Initiator calls with "" → gets .1
// - Responder calls with the IP assigned by the initiator
func initWGState() (*wgState, error) {
	state, err := loadWGState()
	if err != nil {
		return nil, fmt.Errorf("load wg state: %v", err)
	}
	if state != nil {
		return state, nil
	}

	privKey, pubKey, err := generateWGKeyPair()
	if err != nil {
		return nil, fmt.Errorf("generate keys: %v", err)
	}

	// Default to .1 — will be overridden by responder
	state = &wgState{
		PrivateKey: privKey,
		PublicKey:  pubKey,
		ListenPort: wgListenPort,
		LocalIP:    fmt.Sprintf("%s.1/24", wgSubnet),
		Peers:      []WGPeerConfig{},
		NextPeerIP: 2,
	}

	if err := saveWGState(state); err != nil {
		return nil, fmt.Errorf("save wg state: %v", err)
	}

	logMsg("wireguard: initialized — pubkey=%s, local=%s", pubKey[:8]+"...", state.LocalIP)
	return state, nil
}

// ─── Config File Generation ─────────────────────────────────────────────────

func writeWGConfig(state *wgState) error {
	os.MkdirAll(wgConfigDir, 0700)

	var sb strings.Builder
	sb.WriteString("[Interface]\n")
	sb.WriteString(fmt.Sprintf("PrivateKey = %s\n", state.PrivateKey))
	sb.WriteString(fmt.Sprintf("ListenPort = %d\n", state.ListenPort))
	sb.WriteString(fmt.Sprintf("Address = %s\n", state.LocalIP))
	sb.WriteString("\n")

	for _, peer := range state.Peers {
		sb.WriteString("[Peer]\n")
		sb.WriteString(fmt.Sprintf("PublicKey = %s\n", peer.PublicKey))
		if peer.Endpoint != "" {
			sb.WriteString(fmt.Sprintf("Endpoint = %s\n", peer.Endpoint))
		}
		sb.WriteString(fmt.Sprintf("AllowedIPs = %s\n", peer.AllowedIPs))
		sb.WriteString("PersistentKeepalive = 25\n")
		sb.WriteString("\n")
	}

	confPath := fmt.Sprintf("%s/%s.conf", wgConfigDir, wgInterface)
	if err := os.WriteFile(confPath, []byte(sb.String()), 0600); err != nil {
		return fmt.Errorf("write wg config: %v", err)
	}

	logMsg("wireguard: config written to %s (%d peers)", confPath, len(state.Peers))
	return nil
}

// ─── Interface Control ──────────────────────────────────────────────────────

func wgUp() error {
	if out, ok := runSafe("ip", "link", "show", wgInterface); ok && out != "" {
		// Already up — reload config
		confPath := fmt.Sprintf("%s/%s.conf", wgConfigDir, wgInterface)
		runSafe("wg", "setconf", wgInterface, confPath)
		logMsg("wireguard: config reloaded")
		return nil
	}

	out, ok := runSafe("wg-quick", "up", wgInterface)
	if !ok {
		return fmt.Errorf("wg-quick up failed: %s", out)
	}
	logMsg("wireguard: interface %s is up", wgInterface)
	return nil
}

func wgDown() error {
	runSafe("wg-quick", "down", wgInterface)
	logMsg("wireguard: interface %s is down", wgInterface)
	return nil
}

func wgIsUp() bool {
	out, ok := runSafe("ip", "link", "show", wgInterface)
	return ok && out != "" && strings.Contains(out, "UP")
}

// ─── Auto-start on daemon boot ──────────────────────────────────────────────

// startWGTunnel checks if WireGuard was configured and brings it up on daemon start.
// Called from main.go startup sequence.
func startWGTunnel() {
	state, err := loadWGState()
	if err != nil || state == nil {
		return // No WG configured
	}
	if len(state.Peers) == 0 {
		return // No peers to connect to
	}

	// Write config and bring up
	if err := writeWGConfig(state); err != nil {
		logMsg("wireguard: auto-start failed (write config): %v", err)
		return
	}
	if err := wgUp(); err != nil {
		logMsg("wireguard: auto-start failed (wg up): %v", err)
		return
	}
	logMsg("wireguard: auto-started with %d peers", len(state.Peers))
}

// ─── Peer Management ────────────────────────────────────────────────────────

func addWGPeer(deviceID, remotePublicKey, remoteEndpoint string) (assignedIP string, err error) {
	state, err := initWGState()
	if err != nil {
		return "", err
	}

	// Check if peer already exists (by public key to avoid duplicates)
	for _, p := range state.Peers {
		if p.PublicKey == remotePublicKey {
			// Update endpoint if changed
			if remoteEndpoint != "" && p.Endpoint != remoteEndpoint {
				p.Endpoint = remoteEndpoint
				saveWGState(state)
				writeWGConfig(state)
				wgUp()
			}
			return strings.TrimSuffix(p.AllowedIPs, "/32"), nil
		}
	}

	// Assign next IP
	peerIP := fmt.Sprintf("%s.%d", wgSubnet, state.NextPeerIP)
	state.NextPeerIP++

	peer := WGPeerConfig{
		PublicKey:  remotePublicKey,
		Endpoint:   remoteEndpoint,
		AllowedIPs: peerIP + "/32",
		DeviceID:   deviceID,
	}
	state.Peers = append(state.Peers, peer)

	if err := saveWGState(state); err != nil {
		return "", fmt.Errorf("save state: %v", err)
	}
	if err := writeWGConfig(state); err != nil {
		return "", fmt.Errorf("write config: %v", err)
	}
	if err := wgUp(); err != nil {
		return "", fmt.Errorf("bring up interface: %v", err)
	}

	logMsg("wireguard: peer added — device=%s, ip=%s, endpoint=%s", deviceID, peerIP, remoteEndpoint)
	return peerIP, nil
}

func removeWGPeer(deviceID string) error {
	state, err := loadWGState()
	if err != nil || state == nil {
		return fmt.Errorf("no wireguard state")
	}

	found := false
	var filtered []WGPeerConfig
	for _, p := range state.Peers {
		if p.DeviceID == deviceID {
			found = true
			continue
		}
		filtered = append(filtered, p)
	}

	if !found {
		return fmt.Errorf("peer not found for device %s", deviceID)
	}

	state.Peers = filtered
	if err := saveWGState(state); err != nil {
		return err
	}
	if err := writeWGConfig(state); err != nil {
		return err
	}

	if len(state.Peers) == 0 {
		wgDown()
		logMsg("wireguard: last peer removed, interface down")
	} else {
		wgUp()
		logMsg("wireguard: peer removed for device %s, %d peers remain", deviceID, len(state.Peers))
	}

	return nil
}

// ─── Key Exchange (Pairing Flow) ────────────────────────────────────────────

// initiateWGPairing is called by the INITIATOR (the NAS that starts the pairing).
// It sends our public key + our public IP to the remote, receives the remote's
// public key + assigned tunnel IP, and configures the tunnel.
func initiateWGPairing(deviceID, remoteAddr, remoteToken string) (map[string]interface{}, error) {
	state, err := initWGState()
	if err != nil {
		return nil, fmt.Errorf("init local wg: %v", err)
	}

	// Get our own public IP to send as endpoint
	ourPublicIP, _ := runSafe("curl", "-fsSL", "--connect-timeout", "5", "https://api.ipify.org")
	ourPublicIP = strings.TrimSpace(ourPublicIP)
	ourEndpoint := ""
	if ourPublicIP != "" {
		ourEndpoint = fmt.Sprintf("%s:%d", ourPublicIP, wgListenPort)
	}

	client := &http.Client{
		Timeout: 15 * time.Second,
	}

	payload := map[string]interface{}{
		"publicKey":  state.PublicKey,
		"listenPort": state.ListenPort,
		"endpoint":   ourEndpoint, // Our public endpoint so the remote can reach us
	}
	payloadJSON, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST",
		fmt.Sprintf("https://%s:5009/api/backup/pair/wg-exchange", remoteAddr),
		strings.NewReader(string(payloadJSON)))
	req.Header.Set("Authorization", "Bearer "+remoteToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wg exchange request failed: %v", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("wg exchange response parse error: %v", err)
	}

	if errMsg, ok := result["error"].(string); ok && errMsg != "" {
		return nil, fmt.Errorf("remote wg exchange error: %s", errMsg)
	}

	remotePublicKey, _ := result["publicKey"].(string)
	if remotePublicKey == "" {
		return nil, fmt.Errorf("remote did not provide a public key")
	}

	// The remote assigned us a tunnel IP — update our local IP
	assignedToUs, _ := result["yourIP"].(string)
	if assignedToUs != "" {
		state.LocalIP = assignedToUs + "/24"
		saveWGState(state)
		writeWGConfig(state)
		logMsg("wireguard: remote assigned us tunnel IP %s", assignedToUs)
	}

	// Remote's tunnel IP (what we'll use to reach them)
	remoteTunnelIP, _ := result["myIP"].(string)

	// Add the remote as a peer with their DDNS/public endpoint
	remoteEndpoint := fmt.Sprintf("%s:%d", remoteAddr, wgListenPort)
	_, err = addWGPeer(deviceID, remotePublicKey, remoteEndpoint)
	if err != nil {
		return nil, fmt.Errorf("add peer: %v", err)
	}

	// Verify tunnel connectivity
	tunnelOk := false
	if remoteTunnelIP != "" {
		for attempt := 0; attempt < 8; attempt++ {
			time.Sleep(time.Duration(500+attempt*500) * time.Millisecond)
			if out, ok := runSafe("ping", "-c", "1", "-W", "2", remoteTunnelIP); ok && strings.Contains(out, "1 received") {
				tunnelOk = true
				break
			}
		}
	}

	return map[string]interface{}{
		"ok":              true,
		"tunnelVerified":  tunnelOk,
		"localPublicKey":  state.PublicKey,
		"remotePublicKey": remotePublicKey,
		"localIP":         strings.TrimSuffix(state.LocalIP, "/24"),
		"remoteIP":        remoteTunnelIP,
	}, nil
}

// handleWGExchange is called on the RESPONDER (the NAS that receives the pairing request).
// It receives the initiator's public key + endpoint, assigns tunnel IPs, and returns
// its own public key + the assigned IPs.
func handleWGExchange(body map[string]interface{}, r *http.Request) map[string]interface{} {
	remotePubKey := bodyStr(body, "publicKey")
	if remotePubKey == "" {
		return map[string]interface{}{"error": "publicKey required"}
	}

	state, err := initWGState()
	if err != nil {
		return map[string]interface{}{"error": "init wg failed: " + err.Error()}
	}

	// Get remote endpoint — from the body (their public IP) or from the request source
	remoteEndpoint := bodyStr(body, "endpoint")
	if remoteEndpoint == "" {
		// Try to get from request IP
		remoteIP := r.RemoteAddr
		if idx := strings.LastIndex(remoteIP, ":"); idx > 0 {
			remoteIP = remoteIP[:idx]
		}
		remoteIP = strings.Trim(remoteIP, "[]")
		if remoteIP != "" && remoteIP != "127.0.0.1" {
			remoteEndpoint = fmt.Sprintf("%s:%d", remoteIP, wgListenPort)
		}
	}

	// We (responder) keep .1, assign the initiator the next available IP
	initiatorIP := fmt.Sprintf("%s.%d", wgSubnet, state.NextPeerIP)
	state.NextPeerIP++

	// Check if this peer already exists (re-pairing)
	exists := false
	for i, p := range state.Peers {
		if p.PublicKey == remotePubKey {
			// Update endpoint
			state.Peers[i].Endpoint = remoteEndpoint
			state.Peers[i].AllowedIPs = initiatorIP + "/32"
			exists = true
			break
		}
	}

	if !exists {
		peer := WGPeerConfig{
			PublicKey:  remotePubKey,
			Endpoint:   remoteEndpoint,
			AllowedIPs: initiatorIP + "/32",
			DeviceID:   "pending-" + remotePubKey[:8],
		}
		state.Peers = append(state.Peers, peer)
	}

	if err := saveWGState(state); err != nil {
		return map[string]interface{}{"error": "save state: " + err.Error()}
	}
	if err := writeWGConfig(state); err != nil {
		return map[string]interface{}{"error": "write config: " + err.Error()}
	}
	if err := wgUp(); err != nil {
		return map[string]interface{}{"error": "bring up interface: " + err.Error()}
	}

	// Our tunnel IP (without /24)
	myIP := strings.TrimSuffix(state.LocalIP, "/24")

	return map[string]interface{}{
		"ok":         true,
		"publicKey":  state.PublicKey,
		"listenPort": state.ListenPort,
		"myIP":       myIP,        // Responder's tunnel IP (what initiator uses to reach us)
		"yourIP":     initiatorIP, // Initiator's assigned tunnel IP
	}
}

// ─── Status ─────────────────────────────────────────────────────────────────

func getWGStatus() map[string]interface{} {
	state, err := loadWGState()
	if err != nil || state == nil {
		return map[string]interface{}{
			"configured": false,
			"active":     false,
			"peers":      0,
		}
	}

	active := wgIsUp()

	peers := []map[string]interface{}{}
	if active {
		out, ok := runSafe("wg", "show", wgInterface, "dump")
		if ok && out != "" {
			peerStats := parseWGDump(out)
			for _, p := range state.Peers {
				peerInfo := map[string]interface{}{
					"publicKey":  p.PublicKey[:8] + "...",
					"allowedIPs": p.AllowedIPs,
					"deviceId":   p.DeviceID,
					"endpoint":   p.Endpoint,
				}
				if stats, ok := peerStats[p.PublicKey]; ok {
					peerInfo["lastHandshake"] = stats["lastHandshake"]
					peerInfo["rxBytes"] = stats["rxBytes"]
					peerInfo["txBytes"] = stats["txBytes"]
					peerInfo["connected"] = stats["connected"]
				}
				peers = append(peers, peerInfo)
			}
		}
	} else {
		for _, p := range state.Peers {
			peers = append(peers, map[string]interface{}{
				"publicKey":  p.PublicKey[:8] + "...",
				"allowedIPs": p.AllowedIPs,
				"deviceId":   p.DeviceID,
				"connected":  false,
			})
		}
	}

	return map[string]interface{}{
		"configured": true,
		"active":     active,
		"publicKey":  state.PublicKey[:8] + "...",
		"localIP":    state.LocalIP,
		"listenPort": state.ListenPort,
		"peerCount":  len(state.Peers),
		"peers":      peers,
	}
}

func parseWGDump(dump string) map[string]map[string]interface{} {
	result := map[string]map[string]interface{}{}
	lines := strings.Split(strings.TrimSpace(dump), "\n")

	for _, line := range lines[1:] {
		fields := strings.Split(line, "\t")
		if len(fields) < 7 {
			continue
		}

		pubKey := fields[0]
		handshakeTS := parseInt(fields[4], 0)
		rxBytes := parseInt(fields[5], 0)
		txBytes := parseInt(fields[6], 0)

		connected := false
		lastHandshake := "never"
		if handshakeTS > 0 {
			t := time.Unix(int64(handshakeTS), 0)
			age := time.Since(t)
			if age < 3*time.Minute {
				connected = true
			}
			lastHandshake = fmt.Sprintf("%.0fs ago", age.Seconds())
		}

		result[pubKey] = map[string]interface{}{
			"lastHandshake": lastHandshake,
			"rxBytes":       rxBytes,
			"txBytes":       txBytes,
			"connected":     connected,
		}
	}

	return result
}

// ─── HTTP Route Extensions ──────────────────────────────────────────────────

func handleWGRoutes(w http.ResponseWriter, r *http.Request, urlPath string, body map[string]interface{}) {
	switch {
	case urlPath == "/api/backup/wg/status" && r.Method == "GET":
		jsonOk(w, getWGStatus())

	case urlPath == "/api/backup/wg/setup" && r.Method == "POST":
		state, err := initWGState()
		if err != nil {
			jsonError(w, 500, err.Error())
			return
		}
		if err := writeWGConfig(state); err != nil {
			jsonError(w, 500, err.Error())
			return
		}
		jsonOk(w, map[string]interface{}{
			"ok":        true,
			"publicKey": state.PublicKey,
			"localIP":   state.LocalIP,
		})

	case urlPath == "/api/backup/wg/up" && r.Method == "POST":
		state, err := loadWGState()
		if err != nil || state == nil {
			jsonError(w, 400, "WireGuard not configured. Run setup first.")
			return
		}
		if err := writeWGConfig(state); err != nil {
			jsonError(w, 500, err.Error())
			return
		}
		if err := wgUp(); err != nil {
			jsonError(w, 500, err.Error())
			return
		}
		jsonOk(w, map[string]interface{}{"ok": true, "active": true})

	case urlPath == "/api/backup/wg/down" && r.Method == "POST":
		wgDown()
		jsonOk(w, map[string]interface{}{"ok": true, "active": false})

	case urlPath == "/api/backup/pair/wg-exchange" && r.Method == "POST":
		result := handleWGExchange(body, r)
		if errMsg, ok := result["error"].(string); ok && errMsg != "" {
			jsonError(w, 500, errMsg)
			return
		}
		jsonOk(w, result)

	case strings.HasPrefix(urlPath, "/api/backup/wg/peer/") && r.Method == "DELETE":
		deviceID := strings.TrimPrefix(urlPath, "/api/backup/wg/peer/")
		deviceID = strings.TrimSuffix(deviceID, "/")
		if err := removeWGPeer(deviceID); err != nil {
			jsonError(w, 404, err.Error())
			return
		}
		jsonOk(w, map[string]interface{}{"ok": true})

	default:
		jsonError(w, 404, "Not found")
	}
}
