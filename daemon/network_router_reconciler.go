// network_router_reconciler.go — Reconciler que sincroniza network_ports
// con mappings UPnP del router.
//
// Filosofía:
//   - **Best-effort**: si el router no soporta UPnP, NimOS sigue
//     funcionando. El reconciler emite warn una vez por pasada y se
//     calla hasta la siguiente.
//
//   - **No destructivo**: solo añade mappings con descripciones que
//     empiezan por `RouterMappingDescPrefix` ("NimOS-"). Mappings de
//     otros dispositivos NO se tocan.
//
//   - **Sin nuevas tablas**: el estado deseado son los `network_ports`
//     con `enabled=true`. El estado real lo obtenemos del router en
//     cada pasada — no se persiste.
//
// Tier=Low, interval=3600s (1 hora). Razón: UPnP no es crítico (el cert
// puede emitirse con DNS-01 sin necesitar puerto abierto), y consultar
// el router cada minuto es ruido en la LAN. Una vez por hora es plenty
// para mantener mappings vivos (los routers típicamente tienen lease
// infinito cuando se piden con duration=0).

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Constants
// ─────────────────────────────────────────────────────────────────────────────

// RouterMappingDescPrefix es el prefijo que NimOS pone en las
// descripciones de sus mappings. Permite distinguir mappings nuestros
// de los de otros dispositivos.
const RouterMappingDescPrefix = "NimOS-"

// ─────────────────────────────────────────────────────────────────────────────
// Config
// ─────────────────────────────────────────────────────────────────────────────

// RouterReconcilerConfig agrupa parámetros del constructor.
type RouterReconcilerConfig struct {
	Interval time.Duration

	// LocalIP es la IP interna que NimOS expone al router cuando crea
	// mappings. Si vacía, el reconciler intenta detectarla en cada
	// pasada (interfaces no-loopback de la máquina).
	LocalIP string
}

// DefaultRouterReconcilerConfig.
func DefaultRouterReconcilerConfig() RouterReconcilerConfig {
	return RouterReconcilerConfig{
		Interval: 3600 * time.Second,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Reconciler
// ─────────────────────────────────────────────────────────────────────────────

// RouterReconciler implementa Reconciler.
type RouterReconciler struct {
	repo     *NetworkRepo
	emitter  *EventEmitter
	clock    Clock
	provider RouterProvider
	config   RouterReconcilerConfig

	// lastUnavailableEmit recuerda si en la pasada previa ya emitimos
	// "router_unavailable" — evita spam si el router está caído largo
	// tiempo. Si el router vuelve, lo notamos y emitimos "router_recovered".
	mu                  sync.Mutex
	lastUnavailableEmit bool
}

// NewRouterReconciler construye el reconciler.
func NewRouterReconciler(
	repo *NetworkRepo,
	emitter *EventEmitter,
	clock Clock,
	provider RouterProvider,
	config RouterReconcilerConfig,
) (*RouterReconciler, error) {
	if repo == nil {
		return nil, errors.New("NewRouterReconciler: repo is nil")
	}
	if emitter == nil {
		return nil, errors.New("NewRouterReconciler: emitter is nil")
	}
	if provider == nil {
		return nil, errors.New("NewRouterReconciler: provider is nil")
	}
	if clock == nil {
		clock = NewRealClock()
	}
	if config.Interval == 0 {
		config.Interval = DefaultRouterReconcilerConfig().Interval
	}
	return &RouterReconciler{
		repo:     repo,
		emitter:  emitter,
		clock:    clock,
		provider: provider,
		config:   config,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Reconciler interface impl
// ─────────────────────────────────────────────────────────────────────────────

func (r *RouterReconciler) Name() string            { return "router_upnp" }
func (r *RouterReconciler) Tier() ReconcilerTier    { return TierLow }
func (r *RouterReconciler) Interval() time.Duration { return r.config.Interval }

// Reconcile ejecuta una pasada.
func (r *RouterReconciler) Reconcile(ctx context.Context) error {
	// 1. Detect router. Si no disponible, emit warn y volver.
	status, err := r.provider.Detect(ctx)
	if err != nil {
		// Error inesperado al detectar. NO es ErrRouterUnavailable —
		// es algo más raro. Log y volver.
		logMsg("router reconciler: Detect: %v", err)
		return nil
	}

	if !status.Available || !status.Detected {
		r.handleRouterDown(ctx, status)
		return nil
	}

	// El router responde. Si en pasada previa estaba caído, emitir recovery.
	r.mu.Lock()
	wasDown := r.lastUnavailableEmit
	r.lastUnavailableEmit = false
	r.mu.Unlock()
	if wasDown {
		r.emit(ctx, EventLevelInfo, "router_recovered",
			fmt.Sprintf("Router UPnP recovered (external IP %s)", status.ExternalIP))
	}

	// 2. Determinar localIP a usar.
	localIP := r.config.LocalIP
	if localIP == "" {
		localIP = status.LocalIP // el router suele saberlo
	}
	if localIP == "" {
		localIP = detectLocalIP()
	}
	if localIP == "" {
		r.emit(ctx, EventLevelWarn, "local_ip_unknown",
			"Cannot determine local IP for router mappings; skipping")
		return nil
	}

	// 3. Listar mappings actuales del router.
	current, err := r.provider.ListMappings(ctx)
	if err != nil {
		// Si el router no responde después de Detect OK, lo tratamos como
		// transient. No spamear: solo log.
		logMsg("router reconciler: ListMappings: %v", err)
		return nil
	}

	// 4. Listar puertos deseados (network_ports enabled=true).
	desired, err := r.repo.ListPorts(ctx)
	if err != nil {
		return fmt.Errorf("list ports: %w", err)
	}

	// 5. Por cada puerto deseado + enabled, asegurar que existe mapping.
	for _, p := range desired {
		if !p.Enabled {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r.ensureMapping(ctx, p, localIP, current)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Internals
// ─────────────────────────────────────────────────────────────────────────────

// handleRouterDown maneja el caso de router no disponible / no responde.
// Emite warn una vez (la primera detección) y luego se calla hasta
// recuperación.
func (r *RouterReconciler) handleRouterDown(ctx context.Context, status *RouterStatus) {
	r.mu.Lock()
	alreadyEmitted := r.lastUnavailableEmit
	r.lastUnavailableEmit = true
	r.mu.Unlock()

	if alreadyEmitted {
		return
	}
	var msg string
	if !status.Available {
		msg = "UPnP client (upnpc) not installed. Run: apt install miniupnpc"
	} else {
		msg = "Router does not respond to UPnP. Check router admin: UPnP may be disabled (common on Movistar/Vodafone routers)"
	}
	r.emit(ctx, EventLevelWarn, "router_unavailable", msg)
}

// ensureMapping garantiza que existe un mapping UPnP para el puerto
// deseado. Idempotente — si ya existe con los mismos params, no-op.
//
// NetworkPort solo expone http/https que son TCP por definición. Si en
// el futuro hay puertos UDP, NetworkPort deberá ganar campo Protocol.
func (r *RouterReconciler) ensureMapping(ctx context.Context, p *NetworkPort, localIP string, current []RouterPortMapping) {
	const protocol = "TCP" // http/https son TCP
	desc := RouterMappingDescPrefix + p.ID

	// Buscar en current si ya hay mapping nuestro para este (proto, ext_port).
	for _, m := range current {
		if strings.EqualFold(m.Protocol, protocol) && m.ExternalPort == p.Port {
			// Hay mapping. ¿Coincide internal_ip e internal_port?
			if m.InternalIP == localIP && m.InternalPort == p.Port {
				// Todo bien, no-op.
				return
			}
			// Mapping existe pero apunta a otro lado. Si la descripción
			// indica que es nuestro, lo "refrescamos" (add lo reemplaza).
			// Si no es nuestro, NO tocamos — es de otro dispositivo.
			if !strings.HasPrefix(m.Description, RouterMappingDescPrefix) {
				r.emit(ctx, EventLevelWarn, "router_mapping_conflict",
					fmt.Sprintf("Port %d/%s is mapped on router to %s:%d by another device (%s); NimOS will not override",
						p.Port, protocol, m.InternalIP, m.InternalPort, m.Description))
				return
			}
			// Es nuestro pero apunta mal — re-mapear.
			break
		}
	}

	// No hay mapping (o es nuestro y desactualizado). Crear/refrescar.
	mapping := RouterPortMapping{
		Protocol:     protocol,
		ExternalPort: p.Port,
		InternalIP:   localIP,
		InternalPort: p.Port,
		Description:  desc,
	}
	err := r.provider.AddMapping(ctx, mapping)
	switch {
	case err == nil:
		r.emit(ctx, EventLevelInfo, "mapping_created",
			fmt.Sprintf("Port mapping %d/%s → %s created on router", p.Port, protocol, localIP))
	case errors.Is(err, ErrRouterConflict):
		r.emit(ctx, EventLevelWarn, "router_mapping_conflict",
			fmt.Sprintf("Router refused mapping for port %d/%s: conflict with another device",
				p.Port, protocol))
	default:
		logMsg("router reconciler: AddMapping %d/%s: %v", p.Port, protocol, err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// LocalIP detection
// ─────────────────────────────────────────────────────────────────────────────

// detectLocalIP devuelve la primera IPv4 no-loopback de las interfaces
// del sistema. Heurística simple — el router suele saber mejor cuál es
// la IP del cliente, así que esto es solo fallback.
func detectLocalIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil {
				continue
			}
			// Solo IPv4 no-loopback.
			if ip.To4() == nil {
				continue
			}
			if ip.IsLoopback() {
				continue
			}
			return ip.String()
		}
	}
	return ""
}

// ─────────────────────────────────────────────────────────────────────────────
// Events helper
// ─────────────────────────────────────────────────────────────────────────────

func (r *RouterReconciler) emit(ctx context.Context, level EventLevel, event, message string) {
	if _, err := r.emitter.Emit(ctx, EventInput{
		Category: CategoryObserver,
		Event:    event,
		Level:    level,
		Message:  message,
	}); err != nil {
		logMsg("router reconciler: emit %s: %v", event, err)
	}
}
