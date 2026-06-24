package main

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// port_rewrite.go — Fase 3 del Port Allocator (PORT-ALLOCATOR-DESIGN v1.1).
//
// Reescribe el NÚMERO de puerto host de las publicaciones del compose, paralelo a
// rewriteComposePorts (docker_access_mode.go) que reescribe la IP. Reutiliza el
// mismo enfoque yaml.Node (preserva comentarios y estructura) y el helper
// yamlMapValue.
//
// COMPONENTE DE MAYOR RIESGO del subsistema: máxima cobertura de tests sobre todas
// las formas de `ports:`.

// rewriteComposeHostPorts remapea los puertos host del compose según `remap`
// (oldHostPort → newHostPort). Devuelve el YAML resultante y cuántas entradas
// cambió. No toca entradas cuyo host no esté en `remap`, ni hosts no numéricos
// (p.ej. "${VAR}": esos los resuelve el wiring por env en Fase 4).
//
// Formas soportadas (short y long syntax):
//
//	"8080:80"            → "<new>:80"
//	"127.0.0.1:8080:80"  → "127.0.0.1:<new>:80"   (IP preservada)
//	"8080:80/udp"        → "<new>:80/udp"          (protocolo preservado)
//	8096 (escalar)       → "<new>:8096"            (container = el original)
//	{published: 8080, …} → {published: <new>, …}   (long syntax)
func rewriteComposeHostPorts(src []byte, remap map[int]int) ([]byte, int, error) {
	if len(remap) == 0 {
		return src, 0, nil
	}
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
				out, ok := rewriteHostPortSpec(entry.Value, remap)
				if ok && out != entry.Value {
					entry.Value = out
					entry.Tag = "!!str"
					entry.Style = yaml.DoubleQuotedStyle
					changed++
				}
			case yaml.MappingNode: // long syntax: {target, published, protocol, host_ip…}
				pub := yamlMapValue(entry, "published")
				if pub == nil {
					continue
				}
				hp, err := strconv.Atoi(strings.TrimSpace(pub.Value))
				if err != nil {
					continue // "${VAR}" u otro no numérico
				}
				if np, ok := remap[hp]; ok && np != hp {
					pub.Value = strconv.Itoa(np)
					pub.Tag = "!!int"
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

// rewriteHostPortSpec remapea el puerto host de una publicación short-syntax.
// ok=false si la entrada no es transformable (host no numérico, forma exótica, o
// el host no está en remap): se deja tal cual.
func rewriteHostPortSpec(spec string, remap map[int]int) (string, bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return spec, false
	}
	// Separar sufijo de protocolo ("/tcp", "/udp").
	proto := ""
	if i := strings.IndexByte(spec, '/'); i >= 0 {
		proto = spec[i:]
		spec = spec[:i]
	}
	parts := strings.Split(spec, ":")

	// Localizar el índice del puerto host según la forma.
	var hostIdx int
	switch len(parts) {
	case 1: // "8096" → host == container
		hostIdx = 0
	case 2: // "host:container"
		hostIdx = 0
	case 3: // "ip:host:container"
		hostIdx = 1
	default: // IPv6 u otras formas exóticas: no tocar
		return spec + proto, false
	}

	host, err := strconv.Atoi(strings.TrimSpace(parts[hostIdx]))
	if err != nil {
		return spec + proto, false // "${VAR}" u otro no numérico → no tocar
	}
	np, ok := remap[host]
	if !ok || np == host {
		return spec + proto, false
	}
	newHost := strconv.Itoa(np)

	switch len(parts) {
	case 1: // "8096" → "<new>:8096" (el container es el puerto original)
		return newHost + ":" + parts[0] + proto, true
	case 2: // "host:container" → "<new>:container"
		return newHost + ":" + parts[1] + proto, true
	case 3: // "ip:host:container" → "ip:<new>:container"
		return parts[0] + ":" + newHost + ":" + parts[2] + proto, true
	}
	return spec + proto, false
}
