// network_router_upnp.go — Implementación de RouterProvider vía
// miniupnpc (`upnpc` CLI).
//
// Reutilizamos `upnpc` (binario CLI de miniupnpc) en lugar de
// implementar SSDP/UPnP en Go puro. Razones:
//   1. UPnP es 500+ LOC + multicast UDP + parsing XML de SCPD si lo
//      implementas bien. Para un módulo "best-effort" es overkill.
//   2. `miniupnpc` está empaquetado para Debian/Ubuntu/Raspbian.
//   3. Es el mismo enfoque que el código legacy de NimOS — mantenemos
//      consistencia operativa.
//
// El binario `upnpc` se invoca con timeout porque puede colgarse si el
// router responde a SSDP pero no a las RPCs subsiguientes (visto en
// Movistar/Vodafone). El timeout es duro: si no responde en N segundos,
// asumimos router inaccesible.
//
// Output parsing es frágil (texto sin formato estable entre versiones)
// pero el legacy lo hace ya. Mantenemos el parsing legacy probado.

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// UPnPRouterProvider
// ─────────────────────────────────────────────────────────────────────────────

// UPnPRouterProvider implementa RouterProvider invocando `upnpc`.
type UPnPRouterProvider struct {
	breaker *CircuitBreaker

	// upnpcBin es el path al binario. Default "upnpc"; inyectable para tests.
	upnpcBin string

	// cmdTimeout es el timeout duro para cada invocación de upnpc.
	// Default 10s — suficiente para SSDP discovery + 1 RPC.
	cmdTimeout time.Duration

	// runCmd es inyectable para tests. Default: ejecuta `upnpc` real.
	runCmd cmdRunner
}

// cmdRunner es la interfaz mínima para ejecutar comandos. En producción
// usa exec.CommandContext; en tests se puede mockear para devolver
// outputs canned y verificar args.
type cmdRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// UPnPRouterProviderConfig agrupa parámetros del constructor.
type UPnPRouterProviderConfig struct {
	Breaker    *CircuitBreaker
	UpnpcBin   string        // default "upnpc"
	CmdTimeout time.Duration // default 10s
	RunCmd     cmdRunner     // default exec.CommandContext
}

// NewUPnPRouterProvider construye el provider.
func NewUPnPRouterProvider(cfg UPnPRouterProviderConfig) (*UPnPRouterProvider, error) {
	if cfg.Breaker == nil {
		return nil, errors.New("NewUPnPRouterProvider: Breaker is required")
	}
	if cfg.UpnpcBin == "" {
		cfg.UpnpcBin = "upnpc"
	}
	if cfg.CmdTimeout == 0 {
		cfg.CmdTimeout = 10 * time.Second
	}
	if cfg.RunCmd == nil {
		cfg.RunCmd = defaultRunCmd
	}
	return &UPnPRouterProvider{
		breaker:    cfg.Breaker,
		upnpcBin:   cfg.UpnpcBin,
		cmdTimeout: cfg.CmdTimeout,
		runCmd:     cfg.RunCmd,
	}, nil
}

// defaultRunCmd ejecuta el comando real con timeout. Captura stdout y
// stderr juntos (upnpc escribe a ambos sin distinción consistente).
func defaultRunCmd(ctx context.Context, name string, args ...string) ([]byte, error) {
	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.Bytes(), err
}

// Name implementa RouterProvider.
func (p *UPnPRouterProvider) Name() string { return "upnp" }

// ─────────────────────────────────────────────────────────────────────────────
// Detect
// ─────────────────────────────────────────────────────────────────────────────

// Detect implementa RouterProvider.
//
// Llama `upnpc -s` (status). Si el binario no existe, devuelve
// Available=false. Si existe pero el router no responde, devuelve
// Available=true, Detected=false. Si todo OK, parsea local/external IP.
func (p *UPnPRouterProvider) Detect(ctx context.Context) (*RouterStatus, error) {
	// Comprobar que el binario existe.
	if _, err := exec.LookPath(p.upnpcBin); err != nil {
		return &RouterStatus{Available: false}, nil
	}

	cmdCtx, cancel := context.WithTimeout(ctx, p.cmdTimeout)
	defer cancel()

	out, err := p.runCmd(cmdCtx, p.upnpcBin, "-s")
	raw := string(out)

	if err != nil {
		// Comando falló: no hay router UPnP, o timeout, o error.
		// NO es un error fatal — Detected=false y volvemos.
		return &RouterStatus{
			Available: true,
			Detected:  false,
			Raw:       truncateRaw(raw, 4096),
		}, nil
	}

	status := &RouterStatus{
		Available: true,
		Detected:  true,
		Raw:       truncateRaw(raw, 4096),
	}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Local LAN ip address"):
			if parts := strings.SplitN(line, ":", 2); len(parts) == 2 {
				status.LocalIP = strings.TrimSpace(parts[1])
			}
		case strings.HasPrefix(line, "ExternalIPAddress"):
			if parts := strings.SplitN(line, "=", 2); len(parts) == 2 {
				status.ExternalIP = strings.TrimSpace(parts[1])
			}
		case strings.Contains(line, "desc:"):
			status.Desc = strings.TrimSpace(line)
		}
	}
	return status, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// ListMappings
// ─────────────────────────────────────────────────────────────────────────────

// ListMappings implementa RouterProvider. Ejecuta `upnpc -l` y parsea
// el output. Formato típico de una línea con mapping:
//
//	"  0 TCP  8080->192.168.1.50:8080  'NimOS HTTPS' '' 0"
//
//	índice protocol  external->internal_ip:internal_port  'desc' '' lease
func (p *UPnPRouterProvider) ListMappings(ctx context.Context) ([]RouterPortMapping, error) {
	out, err := p.runUnderBreaker(ctx, "-l")
	if err != nil {
		return nil, err
	}
	return parseUPnPList(string(out)), nil
}

// parseUPnPList extrae mappings del output de `upnpc -l`. Función pura,
// testeada por separado.
func parseUPnPList(out string) []RouterPortMapping {
	var mappings []RouterPortMapping
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "->") {
			continue
		}
		// Campos típicos:
		//   "0 TCP 8080->192.168.1.50:8080 'desc' '' 0"
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		// fields[1] = protocolo. fields[2] = "8080->192.168.1.50:8080"
		proto := strings.ToUpper(fields[1])
		if proto != "TCP" && proto != "UDP" {
			// Algunas versiones de upnpc imprimen líneas de cabecera
			// con "->" pero no son mappings. Skip.
			continue
		}
		mapping := strings.SplitN(fields[2], "->", 2)
		if len(mapping) != 2 {
			continue
		}
		extPort, err := strconv.Atoi(mapping[0])
		if err != nil {
			continue
		}
		// "192.168.1.50:8080"
		hostParts := strings.SplitN(mapping[1], ":", 2)
		if len(hostParts) != 2 {
			continue
		}
		intPort, err := strconv.Atoi(hostParts[1])
		if err != nil {
			continue
		}

		m := RouterPortMapping{
			Protocol:     proto,
			ExternalPort: extPort,
			InternalIP:   hostParts[0],
			InternalPort: intPort,
		}

		// Descripción opcional: si hay un campo 4 entre comillas simples,
		// extraerlo. El parsing es best-effort: strings.Fields rompe
		// descripciones con espacios. Las descripciones que NimOS escribe
		// son tokens únicos (e.g. "NimOS-HTTPS" en lugar de "NimOS HTTPS")
		// para evitarlo.
		if len(fields) >= 4 {
			desc := strings.Trim(fields[3], "'")
			if desc != "''" && desc != "" {
				m.Description = desc
			}
		}

		mappings = append(mappings, m)
	}
	return mappings
}

// ─────────────────────────────────────────────────────────────────────────────
// AddMapping
// ─────────────────────────────────────────────────────────────────────────────

// AddMapping implementa RouterProvider. Ejecuta:
//
//	upnpc -a LOCAL_IP EXT_PORT INT_PORT PROTOCOL [DURATION] [DESCRIPTION]
//
// Usamos DURATION=0 (persistente) y DESCRIPTION del mapping.
//
// Idempotencia: `upnpc -a` reemplaza un mapping existente si los params
// son los mismos. Si hay conflicto (otra IP interna mapeada), upnpc
// devuelve un mensaje específico que detectamos como ErrRouterConflict.
func (p *UPnPRouterProvider) AddMapping(ctx context.Context, m RouterPortMapping) error {
	if err := validateMapping(m); err != nil {
		return err
	}

	args := []string{
		"-a",
		m.InternalIP,
		strconv.Itoa(m.InternalPort),
		strconv.Itoa(m.ExternalPort),
		strings.ToUpper(m.Protocol),
		"0", // duration: persistente
	}
	if m.Description != "" {
		args = append(args, m.Description)
	}

	out, err := p.runUnderBreaker(ctx, args...)
	outStr := string(out)
	if err != nil {
		if strings.Contains(outStr, "ConflictInMappingEntry") ||
			strings.Contains(outStr, "Conflict") {
			return ErrRouterConflict
		}
		return err
	}
	// Algunos casos upnpc retorna 0 pero el body indica error:
	if strings.Contains(outStr, "failed") || strings.Contains(outStr, "Failure") {
		if strings.Contains(outStr, "Conflict") {
			return ErrRouterConflict
		}
		return fmt.Errorf("upnpc AddMapping: %s", strings.TrimSpace(truncateRaw(outStr, 500)))
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// RemoveMapping
// ─────────────────────────────────────────────────────────────────────────────

// RemoveMapping implementa RouterProvider. Ejecuta:
//
//	upnpc -d EXT_PORT PROTOCOL
//
// Idempotente: si el mapping no existe, upnpc puede devolver error.
// Lo tratamos como no-error porque el resultado deseado (mapping no
// existe) está conseguido.
func (p *UPnPRouterProvider) RemoveMapping(ctx context.Context, protocol string, externalPort int) error {
	protocol = strings.ToUpper(protocol)
	if protocol != "TCP" && protocol != "UDP" {
		return fmt.Errorf("invalid protocol %q (expected TCP|UDP)", protocol)
	}
	if externalPort <= 0 || externalPort > 65535 {
		return fmt.Errorf("invalid external port %d", externalPort)
	}

	out, err := p.runUnderBreaker(ctx, "-d", strconv.Itoa(externalPort), protocol)
	if err != nil {
		outStr := string(out)
		// "NoSuchEntryInArray" o similar: el mapping ya no existe. OK.
		if strings.Contains(outStr, "NoSuchEntryInArray") ||
			strings.Contains(outStr, "not found") {
			return nil
		}
		return err
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Internals
// ─────────────────────────────────────────────────────────────────────────────

// runUnderBreaker ejecuta `upnpc` envuelto en el breaker. Errores de
// comando se clasifican: timeout → transient, no-such-binary → unavailable.
func (p *UPnPRouterProvider) runUnderBreaker(ctx context.Context, args ...string) ([]byte, error) {
	if _, err := exec.LookPath(p.upnpcBin); err != nil {
		return nil, ErrRouterUnavailable
	}

	var output []byte
	var providerErr error

	breakerErr := p.breaker.Call(func() error {
		cmdCtx, cancel := context.WithTimeout(ctx, p.cmdTimeout)
		defer cancel()

		out, err := p.runCmd(cmdCtx, p.upnpcBin, args...)
		output = out
		if err != nil {
			// Timeout → transient.
			if cmdCtx.Err() == context.DeadlineExceeded {
				providerErr = ErrRouterTransient
				return ErrRouterTransient
			}
			// Otros errores: pueden ser router-unavailable (no SSDP
			// response) o conflict (capturado por el caller).
			// Por simplicidad, no abrimos el breaker por estos —
			// el caller decide qué hacer con el output.
			providerErr = err
			return nil
		}
		return nil
	})

	if errors.Is(breakerErr, ErrCircuitOpen) {
		return nil, ErrRouterTransient
	}
	if providerErr != nil {
		return output, providerErr
	}
	return output, nil
}

// validateMapping comprueba los campos de RouterPortMapping antes de
// invocar upnpc para evitar pasar basura a un proceso externo.
func validateMapping(m RouterPortMapping) error {
	proto := strings.ToUpper(m.Protocol)
	if proto != "TCP" && proto != "UDP" {
		return fmt.Errorf("invalid protocol %q (expected TCP|UDP)", m.Protocol)
	}
	if m.ExternalPort <= 0 || m.ExternalPort > 65535 {
		return fmt.Errorf("invalid external port %d", m.ExternalPort)
	}
	if m.InternalPort <= 0 || m.InternalPort > 65535 {
		return fmt.Errorf("invalid internal port %d", m.InternalPort)
	}
	if m.InternalIP == "" {
		return errors.New("internal IP is required")
	}
	// Validación simple de IPv4 (sin parsear): rechaza espacios y chars
	// inválidos. El binario upnpc se quejará si es realmente inválida.
	if strings.ContainsAny(m.InternalIP, " \t\n;|&$`") {
		return fmt.Errorf("internal IP contains invalid characters: %q", m.InternalIP)
	}
	return nil
}

// truncateRaw recorta el output crudo a maxBytes para evitar logs
// gigantes si upnpc imprime mucha información.
func truncateRaw(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes] + "...[truncated]"
}
