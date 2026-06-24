package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// port_bindings.go — Fase 4/6 (núcleo puro) del Port Allocator.
//
// parseComposeBindings extrae los puertos publicados del compose, y
// resolveStackHostPorts decide la reescritura de TODOS los puertos host
// flotantes. Ambas PURAS (sin DB ni red): el wiring que las alimenta vive en
// dockerStackDeploy.

// parseComposeBindings extrae los bindings de puerto del compose como
// []PortBinding (Declared=contenedor, Host=host, Protocol). Solo hosts NUMÉRICOS:
// las publicaciones con ${VAR} en el host se omiten (las resuelve el env feature).
// Recorre todos los servicios.
func parseComposeBindings(compose string) ([]PortBinding, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(compose), &doc); err != nil {
		return nil, fmt.Errorf("parse compose: %w", err)
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	root := doc.Content[0]
	services := yamlMapValue(root, "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return nil, nil
	}

	var out []PortBinding
	for i := 1; i < len(services.Content); i += 2 {
		svc := services.Content[i]
		ports := yamlMapValue(svc, "ports")
		if ports == nil || ports.Kind != yaml.SequenceNode {
			continue
		}
		for _, entry := range ports.Content {
			switch entry.Kind {
			case yaml.ScalarNode:
				if b, ok := parseShortBinding(entry.Value); ok {
					out = append(out, b)
				}
			case yaml.MappingNode:
				if b, ok := parseLongBinding(entry); ok {
					out = append(out, b)
				}
			}
		}
	}
	return out, nil
}

// parseShortBinding parsea una publicación short-syntax ("host:cont",
// "ip:host:cont", "cont", con sufijo "/tcp"|"/udp"). ok=false si el host o el
// contenedor no son numéricos (p.ej. "${VAR}") o la forma es exótica.
func parseShortBinding(spec string) (PortBinding, bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return PortBinding{}, false
	}
	proto := "tcp"
	if i := strings.IndexByte(spec, '/'); i >= 0 {
		if strings.ToLower(spec[i+1:]) == "udp" {
			proto = "udp"
		}
		spec = spec[:i]
	}
	parts := strings.Split(spec, ":")
	var hostStr, contStr string
	switch len(parts) {
	case 1: // "8096" → host == container
		hostStr, contStr = parts[0], parts[0]
	case 2: // "host:container"
		hostStr, contStr = parts[0], parts[1]
	case 3: // "ip:host:container"
		hostStr, contStr = parts[1], parts[2]
	default:
		return PortBinding{}, false
	}
	host, err1 := strconv.Atoi(strings.TrimSpace(hostStr))
	cont, err2 := strconv.Atoi(strings.TrimSpace(contStr))
	if err1 != nil || err2 != nil {
		return PortBinding{}, false
	}
	return PortBinding{Declared: cont, Host: host, Protocol: proto}, true
}

// parseLongBinding parsea una publicación long-syntax {target, published,
// protocol}. ok=false si target/published no son numéricos.
func parseLongBinding(entry *yaml.Node) (PortBinding, bool) {
	pub := yamlMapValue(entry, "published")
	tgt := yamlMapValue(entry, "target")
	if pub == nil || tgt == nil {
		return PortBinding{}, false
	}
	host, err1 := strconv.Atoi(strings.TrimSpace(pub.Value))
	cont, err2 := strconv.Atoi(strings.TrimSpace(tgt.Value))
	if err1 != nil || err2 != nil {
		return PortBinding{}, false
	}
	proto := "tcp"
	if p := yamlMapValue(entry, "protocol"); p != nil && strings.ToLower(p.Value) == "udp" {
		proto = "udp"
	}
	return PortBinding{Declared: cont, Host: host, Protocol: proto}, true
}

// resolveStackHostPorts decide la reescritura de los puertos host de un stack en
// install. Función PURA.
//
//	compose      : YAML del stack (ya con labels).
//	declaredMain : puerto host que el frontend marca como principal (body["port"]); 0 si ninguno.
//	prevBindings : bindings de la instalación anterior (prev.parsedPorts()) para el
//	               sticky por puerto; nil si es nueva.
//	occupied/hard/soft : ocupación de otras apps y reservados (port_reserved.go).
//
// Devuelve (composeOut, mainHost, portsJSON, err):
//   - composeOut : el compose, reescrito en los puertos host que cambiaron.
//   - mainHost   : el puerto host principal efectivo (== declaredMain si no cambió).
//   - portsJSON  : JSON de []PortBinding a persistir.
//   - err        : solo en parse/marshal/alloc irrecuperable; el caller debe caer
//     entonces al comportamiento previo (usar declaredMain, sin reescritura).
//
// Fase 6: reasigna TODOS los puertos flotantes (principal + secundarios). Reglas:
//   - tcp+udp del mismo puerto lógico (mismo host) → MISMO host nuevo.
//   - los FIJOS por naturaleza (puerto contenedor o host es un reservado blando
//     well-known: DNS 53, NTP 123…) NO se reasignan: el cliente espera ese puerto
//     exacto. Si chocan, fallará en docker (caso "elige una", lo refina Fase 5).
func resolveStackHostPorts(compose string, declaredMain int, prevBindings []PortBinding, occupied, hard, soft map[int]bool) (string, int, string, error) {
	if declaredMain <= 0 {
		return compose, declaredMain, "", nil
	}
	bindings, err := parseComposeBindings(compose)
	if err != nil {
		return compose, declaredMain, "", err
	}
	if len(bindings) == 0 {
		return compose, declaredMain, "", nil
	}

	// Copia de la ocupación para acumular las asignaciones de ESTA app sin mutar
	// el mapa del caller.
	occ := make(map[int]bool, len(occupied))
	for k := range occupied {
		occ[k] = true
	}

	// Sticky por binding: puerto host previo (match Declared+Protocol en la
	// instalación anterior), indexado por el host ACTUAL del binding.
	stickyByHost := map[int]int{}
	for _, b := range bindings {
		for _, pb := range prevBindings {
			if pb.Declared == b.Declared && pb.Protocol == b.Protocol {
				stickyByHost[b.Host] = pb.Host
				break
			}
		}
	}

	// Asignar cada puerto host único (un puerto lógico; tcp+udp comparten host).
	remap := map[int]int{}
	seen := map[int]bool{}
	for _, b := range bindings {
		if seen[b.Host] {
			continue
		}
		seen[b.Host] = true
		// Fijo por protocolo (DNS, NTP…) · no se reasigna: el cliente espera ese
		// puerto exacto. Se detecta porque el contenedor o el host es well-known.
		if soft[b.Declared] || soft[b.Host] {
			continue
		}
		newHost, aerr := allocatePort(b.Host, false, stickyByHost[b.Host], occ, hard, soft)
		if aerr != nil {
			return compose, declaredMain, "", aerr
		}
		if newHost != b.Host {
			remap[b.Host] = newHost
		}
		occ[newHost] = true // que el siguiente puerto lógico no lo repita
	}

	out := compose
	if len(remap) > 0 {
		rewritten, n, rerr := rewriteComposeHostPorts([]byte(compose), remap)
		if rerr != nil {
			return compose, declaredMain, "", rerr
		}
		if n > 0 {
			out = string(rewritten)
		}
	}

	// PortsJSON con los hosts finales + localizar el principal.
	mainHost := declaredMain
	outBindings := make([]PortBinding, len(bindings))
	for i, b := range bindings {
		nb := b
		if nh, ok := remap[b.Host]; ok {
			nb.Host = nh
		}
		outBindings[i] = nb
		if b.Host == declaredMain {
			mainHost = nb.Host
		}
	}

	js, jerr := json.Marshal(outBindings)
	if jerr != nil {
		return out, mainHost, "", jerr
	}
	return out, mainHost, string(js), nil
}
