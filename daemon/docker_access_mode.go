// docker_access_mode.go — SHIELD-P2 · Candado de puerto directo por app.
//
// PROBLEMA: los contenedores publican sus puertos en 0.0.0.0, así que cada
// app instalada es accesible por IP:puerto desde toda la LAN aunque ya esté
// expuesta vía Caddy con TLS. Peor: Docker mete sus propias reglas DNAT y
// SE SALTA el firewall del host (ufw policy drop no protege puertos
// publicados por Docker). La única verdad a nivel kernel es NO publicar el
// puerto fuera del host.
//
// MECANISMO: las apps tipo 'stack' tienen su docker-compose.yml (generado
// por NimOS, fuente de verdad — política APP-001). El candado reescribe las
// entradas de `ports:` para fijar el bind a 127.0.0.1 ("8080:80" →
// "127.0.0.1:8080:80") y aplica con `compose up -d` (recrea solo lo que
// cambió, preservando volúmenes/env/labels). Caddy, que vive en el host,
// sigue llegando por loopback: ÚNICA puerta.
//
// La edición usa yaml.v3 a nivel de Node para tocar SOLO los escalares de
// ports y preservar comentarios, orden y el resto del documento intacto.
//
// FRONTERAS DE MÓDULO: Exposición decide QUÉ se expone; APPS (este código)
// ejecuta el binding porque es quien posee los contenedores; NimShield
// audita la superficie resultante. Cero acoplamiento cruzado.
//
// Apps tipo 'container' (sueltas): 501, misma política honesta que el
// rebuild (APP-001-B) — reconstruir flags desde inspect llegará después.

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ─────────────────────────────────────────────────────────────────────────────
// Reescritura de ports en el compose (función pura, testeable)
// ─────────────────────────────────────────────────────────────────────────────

// rewriteComposePorts transforma las publicaciones de puertos del YAML:
//
//	loopback=true  → todo bind queda en 127.0.0.1
//	                  "8080:80"           → "127.0.0.1:8080:80"
//	                  "0.0.0.0:8080:80"   → "127.0.0.1:8080:80"
//	                  "8080:80/udp"       → "127.0.0.1:8080:80/udp"
//	                  8096 (escalar)      → "127.0.0.1:8096:8096"
//	                  long syntax         → host_ip: "127.0.0.1"
//	loopback=false → deshace el candado (quita el prefijo 127.0.0.1/0.0.0.0;
//	                  otros IPs explícitos se respetan: son intención del user)
//
// Devuelve el YAML resultante y cuántas entradas cambió. Preserva
// comentarios y estructura (edición a nivel de yaml.Node).
func rewriteComposePorts(src []byte, loopback bool) ([]byte, int, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, 0, fmt.Errorf("parse compose: %w", err)
	}
	if len(doc.Content) == 0 {
		return src, 0, nil
	}
	root := doc.Content[0]
	services := yamlMapValue(root, "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return src, 0, nil // sin services: nada que hacer
	}

	changed := 0
	for i := 1; i < len(services.Content); i += 2 {
		svc := services.Content[i]
		ports := yamlMapValue(svc, "ports")
		if ports == nil || ports.Kind != yaml.SequenceNode {
			continue
		}
		for _, entry := range ports.Content {
			switch entry.Kind {
			case yaml.ScalarNode:
				out, ok := rewritePortSpec(entry.Value, loopback)
				if ok && out != entry.Value {
					entry.Value = out
					entry.Tag = "!!str" // "127.0.0.1:8096:8096" debe ir como string
					entry.Style = yaml.DoubleQuotedStyle
					changed++
				}
			case yaml.MappingNode: // long syntax: {target, published, host_ip…}
				hostIP := yamlMapValue(entry, "host_ip")
				if loopback {
					if hostIP == nil {
						entry.Content = append(entry.Content,
							&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "host_ip"},
							&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "127.0.0.1", Style: yaml.DoubleQuotedStyle},
						)
						changed++
					} else if hostIP.Value != "127.0.0.1" {
						hostIP.Value = "127.0.0.1"
						hostIP.Style = yaml.DoubleQuotedStyle
						changed++
					}
				} else if hostIP != nil && (hostIP.Value == "127.0.0.1" || hostIP.Value == "0.0.0.0") {
					removeYamlKey(entry, "host_ip")
					changed++
				}
			}
		}
	}

	if changed == 0 {
		return src, 0, nil
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal compose: %w", err)
	}
	return out, changed, nil
}

// rewritePortSpec transforma una publicación short-syntax. ok=false si la
// entrada no es transformable (se deja tal cual).
func rewritePortSpec(spec string, loopback bool) (string, bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return spec, false
	}
	// Separar sufijo de protocolo ("/tcp", "/udp")
	proto := ""
	if i := strings.IndexByte(spec, '/'); i >= 0 {
		proto = spec[i:]
		spec = spec[:i]
	}
	parts := strings.Split(spec, ":")

	if loopback {
		switch len(parts) {
		case 1: // "8096" → host=container
			return `127.0.0.1:` + parts[0] + `:` + parts[0] + proto, true
		case 2: // "8080:80"
			return `127.0.0.1:` + spec + proto, true
		case 3: // "IP:8080:80" → forzar loopback
			return `127.0.0.1:` + parts[1] + `:` + parts[2] + proto, true
		default:
			return spec + proto, false // IPv6 u otras formas exóticas: no tocar
		}
	}

	// lan: quitar SOLO nuestros binds (127.0.0.1) y el redundante 0.0.0.0.
	// Otros IPs explícitos (p.ej. una IP de VLAN) son intención del usuario.
	if len(parts) == 3 && (parts[0] == "127.0.0.1" || parts[0] == "0.0.0.0") {
		return parts[1] + ":" + parts[2] + proto, true
	}
	return spec + proto, false
}

// yamlMapValue devuelve el nodo valor de la clave `key` en un MappingNode.
func yamlMapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// removeYamlKey elimina una clave (y su valor) de un MappingNode.
func removeYamlKey(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Worker: aplicar el modo de acceso
// ─────────────────────────────────────────────────────────────────────────────

// setAppAccessModeWorker cambia el modo de acceso de una app stack:
// reescribe los ports del compose, aplica con `compose up -d` (recrea solo
// los servicios cuyo binding cambió) y persiste el modo en DB.
func setAppAccessModeWorker(ctx context.Context, appID, mode string) (map[string]interface{}, error) {
	app, err := appsRepo.GetDockerApp(ctx, appID)
	if err != nil {
		return nil, err
	}
	if app == nil || app.Deleting {
		return nil, asHTTPError(404, "App %q not found", appID)
	}
	if app.Type != "stack" {
		// Misma política honesta que el rebuild (APP-001): containers
		// sueltos requieren reconstruir flags desde inspect (APP-001-B).
		return nil, asHTTPError(http.StatusNotImplemented,
			"El candado de puerto directo solo está disponible para apps tipo stack (APP-001-B)")
	}

	conf := getDockerConfigGo()
	dockerPath, _ := conf["path"].(string)
	if dockerPath == "" {
		dp, derr := getDockerPath()
		if derr != nil {
			return nil, asHTTPError(400, "Docker not configured")
		}
		dockerPath = dp
	}
	stackPath := filepath.Join(dockerPath, "stacks", sanitizeDockerNameGo(appID))
	composePath := filepath.Join(stackPath, "docker-compose.yml")

	src, err := os.ReadFile(composePath)
	if err != nil {
		return nil, asHTTPError(500, "Cannot read compose file: %v", err)
	}
	out, changed, err := rewriteComposePorts(src, mode == "caddy_only")
	if err != nil {
		return nil, asHTTPError(500, "Cannot rewrite compose ports: %v", err)
	}

	if changed > 0 {
		// Backup del compose antes de tocarlo — si el up falla, se restaura.
		backup := composePath + ".bak"
		if err := os.WriteFile(backup, src, 0644); err != nil {
			return nil, asHTTPError(500, "Cannot write compose backup: %v", err)
		}
		if err := os.WriteFile(composePath, out, 0644); err != nil {
			return nil, asHTTPError(500, "Cannot write compose file: %v", err)
		}

		upCtx, cancel := context.WithTimeout(commitContext(), 5*time.Minute)
		defer cancel()
		upCmd := exec.CommandContext(upCtx, "docker", "compose", "-f", composePath, "up", "-d")
		upCmd.Dir = stackPath
		if cmdOut, err := upCmd.CombinedOutput(); err != nil {
			// Rollback: compose original + up para volver al estado previo.
			_ = os.WriteFile(composePath, src, 0644)
			rbCtx, rbCancel := context.WithTimeout(commitContext(), 5*time.Minute)
			defer rbCancel()
			rb := exec.CommandContext(rbCtx, "docker", "compose", "-f", composePath, "up", "-d")
			rb.Dir = stackPath
			_, _ = rb.CombinedOutput()
			return nil, asHTTPError(500, "compose up failed (rolled back): %s", string(cmdOut))
		}
		_ = os.Remove(backup)
	}

	if err := appsRepo.SetDockerAppAccessMode(ctx, appID, mode); err != nil {
		return nil, err
	}
	logMsg("shield-p2: app %s access_mode=%s (ports reescritos: %d)", appID, mode, changed)
	return map[string]interface{}{
		"ok": true, "appId": appID, "accessMode": mode, "portsRewritten": changed,
	}, nil
}

// handleAppAccessMode — POST /api/installed-apps/{id}/access-mode
// Body: {"mode":"lan"|"caddy_only"}
func handleAppAccessMode(w http.ResponseWriter, r *http.Request, appID string) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	body, err := readBody(r)
	if err != nil {
		jsonError(w, 400, "Invalid body")
		return
	}
	mode, _ := body["mode"].(string)
	if mode != "lan" && mode != "caddy_only" {
		jsonError(w, 400, "mode must be 'lan' or 'caddy_only'")
		return
	}
	result, werr := setAppAccessModeWorker(r.Context(), appID, mode)
	if werr != nil {
		writeWorkerError(w, werr)
		return
	}
	jsonOk(w, result)
}

// ─────────────────────────────────────────────────────────────────────────────
// URL externa (Caddy) de una app — para launcher/iframe cuando el puerto
// directo está cerrado
// ─────────────────────────────────────────────────────────────────────────────

// externalURLForApp devuelve la URL Caddy (https://sub.dominio[:puerto] o
// https://dominio[:puerto]/ruta) de la app si está expuesta y la exposición
// activa. "" si no hay exposición utilizable. Lectura simple del módulo
// network vía su repo (una dirección, sin acoplamiento inverso).
func externalURLForApp(ctx context.Context, appID string) string {
	if networkRepo == nil {
		return ""
	}
	cfg, err := networkRepo.GetExposureConfig(ctx)
	if err != nil || !cfg.Enabled || cfg.BaseDomain == "" {
		return ""
	}
	apps, err := networkRepo.ListExposedApps(ctx)
	if err != nil {
		return ""
	}
	for _, a := range apps {
		if a.AppID != appID || !a.Enabled {
			continue
		}
		portPart := ""
		if cfg.HTTPSPort != 0 && cfg.HTTPSPort != 443 {
			portPart = fmt.Sprintf(":%d", cfg.HTTPSPort)
		}
		if a.Subdomain != "" {
			return "https://" + a.Subdomain + "." + cfg.BaseDomain + portPart
		}
		if a.Path != "" {
			return "https://" + cfg.BaseDomain + portPart + a.Path
		}
	}
	return ""
}
