// firewall.go — Reglas de firewall del NAS (Beta 8.1)
//
// Endpoints:
//   POST /api/firewall/add-rule     · añade regla iptables
//   POST /api/firewall/remove-rule  · elimina regla
//   POST /api/firewall/toggle       · habilita/deshabilita una regla
//
// Aviso arquitectónico (mayo 2026): este archivo vivía dentro de docker.go
// por casualidad histórica · firewall NO es Docker. Movido a archivo propio
// durante el sprint post-cierre de AppStore (APP-016).

package main

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

func firewallAddRule(w http.ResponseWriter, r *http.Request) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	body, _ := readBody(r)
	port := fmt.Sprintf("%v", body["port"])
	protocol := bodyStr(body, "protocol")
	action := bodyStr(body, "action")
	source := bodyStr(body, "source")

	if port == "" || protocol == "" || action == "" {
		jsonError(w, 400, "port, protocol, and action required")
		return
	}

	// Strict validation — prevent command injection
	// Port: digits only, or digits:digits for ranges
	if matched, _ := regexp.MatchString(`^\d{1,5}(:\d{1,5})?$`, port); !matched {
		jsonError(w, 400, "Invalid port format (use number or range like 8000:8100)")
		return
	}
	// Protocol: whitelist only
	if protocol != "tcp" && protocol != "udp" && protocol != "both" {
		jsonError(w, 400, "Invalid protocol (must be tcp, udp, or both)")
		return
	}
	// Action: whitelist only
	if action != "allow" && action != "deny" && action != "reject" && action != "limit" {
		jsonError(w, 400, "Invalid action (must be allow, deny, reject, or limit)")
		return
	}
	// Source: must be a valid IP or CIDR, or empty/any
	if source != "" && source != "any" && source != "Any" {
		if matched, _ := regexp.MatchString(`^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(/\d{1,2})?$`, source); !matched {
			jsonError(w, 400, "Invalid source (must be IP address or CIDR like 192.168.1.0/24)")
			return
		}
	}

	_, hasUfw := runSafe("which", "ufw")
	if hasUfw {
		// Build ufw args safely — no shell interpolation
		portProto := port
		if protocol != "both" {
			portProto = port + "/" + protocol
		}
		args := []string{action, portProto}
		if source != "" && source != "any" && source != "Any" {
			args = append(args, "from", source)
		}
		result, _ := runSafe("ufw", args...)
		jsonOk(w, map[string]interface{}{"ok": true, "command": "ufw " + strings.Join(args, " "), "result": result})
	} else {
		jsonError(w, 400, "ufw not installed")
	}
}

func firewallRemoveRule(w http.ResponseWriter, r *http.Request) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	body, _ := readBody(r)
	ruleNum := fmt.Sprintf("%v", body["ruleNum"])
	if ruleNum == "" {
		jsonError(w, 400, "ruleNum required")
		return
	}
	// Strict validation — ruleNum must be a positive integer
	if matched, _ := regexp.MatchString(`^\d{1,5}$`, ruleNum); !matched {
		jsonError(w, 400, "Invalid rule number (must be a positive integer)")
		return
	}
	result, _ := runSafeInput("y\n", "ufw", "delete", ruleNum)
	jsonOk(w, map[string]interface{}{"ok": true, "result": result})
}

func firewallToggle(w http.ResponseWriter, r *http.Request) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	body, _ := readBody(r)
	enable, _ := body["enable"].(bool)
	if enable {
		result, _ := runSafeInput("y\n", "ufw", "enable")
		jsonOk(w, map[string]interface{}{"ok": true, "result": result})
	} else {
		result, _ := runSafe("ufw", "disable")
		jsonOk(w, map[string]interface{}{"ok": true, "result": result})
	}
}

// ═══════════════════════════════════
// Hardware driver install
// ═══════════════════════════════════
