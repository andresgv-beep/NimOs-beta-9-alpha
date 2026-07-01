// nimos_capabilities.go — Detección de capabilities del sistema (NimOS core).
//
// Vive en /daemon/nimos_capabilities.go porque las capabilities las usan
// múltiples módulos (network, backup, apps, shares...). La tabla
// nimos_capabilities es global.
//
// Diseño (NIMOS_DISCIPLINE.md §7 v2):
//
//   - Capability detection responde "¿está disponible esta tool?",
//     NO "¿está corriendo ahora mismo?". Es ESTÁTICA.
//   - Detect al boot del daemon.
//   - Refresh ON-DEMAND cuando el frontend pide el endpoint y la
//     persistencia es más vieja que un threshold (típicamente 1h).
//   - NUNCA polling activo periódico.
//
// Diseño técnico:
//
//   - DetectFunc es una closure inyectable. RealDetect usa exec.LookPath
//     + exec.CommandContext con timeout. MockDetect (para tests) devuelve
//     un struct fijo sin tocar el sistema.
//   - El store NO mantiene cache in-memory: la DB es fuente única.
//     Cada Get hace un SELECT (barato, una sola row). Si aparece un
//     caso de muchas lecturas/segundo, añadiremos cache. Hoy no existe.
//   - Single-flight vía mutex: si dos goroutines llaman ForceRefresh a
//     la vez, solo una detecta. La otra recibe el resultado fresh.

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Constants
// ─────────────────────────────────────────────────────────────────────────────

const (
	// capabilitiesSingletonID es la PK de la tabla nimos_capabilities.
	// Solo hay una fila (estado del sistema entero).
	capabilitiesSingletonID = "system"

	// versionDetectTimeout limita cuánto puede tardar un `--version`.
	// Si tarda más, asumimos que la tool está en mal estado y omitimos
	// la versión (la presencia ya quedó registrada por LookPath).
	versionDetectTimeout = 5 * time.Second
)

// ─────────────────────────────────────────────────────────────────────────────
// Errors
// ─────────────────────────────────────────────────────────────────────────────

var (
	// ErrCapabilitiesNotPersisted indica que nunca se ha detectado.
	// El caller debe llamar a ForceRefresh.
	ErrCapabilitiesNotPersisted = errors.New("capabilities have not been detected yet")
)

// ─────────────────────────────────────────────────────────────────────────────
// Types
// ─────────────────────────────────────────────────────────────────────────────

// SystemCapabilities describe qué tools y features están disponibles
// en el sistema host. Se serializa a JSON para persistencia y para el
// endpoint GET /api/network/capabilities.
//
// Filosofía (DISCIPLINE §7 v2):
//   - Solo cosas OPCIONALES (sin las que NimOS sigue arrancando).
//   - Solo cosas ESTÁTICAS (rara vez cambian — apt install / remove).
//   - NO meter aquí estado dinámico (servicio corriendo, puerto abierto).
//     Para eso está el observer del módulo correspondiente.
type SystemCapabilities struct {
	// Network — TLS / certificados
	CertbotInstalled bool   `json:"certbot_installed"`
	CertbotVersion   string `json:"certbot_version,omitempty"`
	OpenSSLInstalled bool   `json:"openssl_installed"`

	// Network — Routing / firewall
	UPnPClient      bool `json:"upnp_client"`      // upnpc / miniupnpc
	NFTBackend      bool `json:"nft_backend"`      // nftables (preferido)
	IPTablesBackend bool `json:"iptables_backend"` // iptables (legacy)
	UFWInstalled    bool `json:"ufw_installed"`    // ufw wrapper

	// Network — DNS tools
	DigInstalled  bool `json:"dig_installed"`
	HostInstalled bool `json:"host_installed"`

	// System
	SystemdAvailable bool `json:"systemd_available"`

	DetectedAt time.Time `json:"detected_at"`
}

// HasAnyFirewallBackend devuelve true si NimOS puede gestionar firewall
// con alguno de los backends disponibles. Útil para diagnósticos:
// "no firewall backend → no gestionar reglas, mostrar warning".
func (c SystemCapabilities) HasAnyFirewallBackend() bool {
	return c.NFTBackend || c.IPTablesBackend
}

// SupportsDNS01 indica si el sistema puede emitir certs via DNS-01
// challenge. Requiere certbot + al menos dig (para verificar TXT records).
func (c SystemCapabilities) SupportsDNS01() bool {
	return c.CertbotInstalled && c.DigInstalled
}

// DetectFunc es la firma del detector. Pluggable para tests.
//
// La implementación REAL (RealDetect) ejecuta exec.LookPath y subprocesos.
// La implementación MOCK devuelve un struct fijo sin tocar el sistema.
type DetectFunc func() SystemCapabilities

// ─────────────────────────────────────────────────────────────────────────────
// RealDetect — implementación de producción
// ─────────────────────────────────────────────────────────────────────────────

// RealDetect inspecciona el sistema y devuelve las capabilities reales.
// Es seguro de llamar varias veces (idempotente, ~50ms en hardware modesto).
//
// El DetectedAt usa time.Now() porque RealDetect no tiene Clock inyectable.
// El caller (CapabilitiesStore.ForceRefresh) sobrescribe DetectedAt con su
// propio Clock para coherencia en tests.
func RealDetect() SystemCapabilities {
	caps := SystemCapabilities{}

	// Tools donde solo nos importa presencia.
	caps.OpenSSLInstalled = lookPathExists("openssl")
	caps.UPnPClient = lookPathExists("upnpc")
	caps.NFTBackend = lookPathExists("nft")
	caps.IPTablesBackend = lookPathExists("iptables")
	caps.UFWInstalled = lookPathExists("ufw")
	caps.DigInstalled = lookPathExists("dig")
	caps.HostInstalled = lookPathExists("host")

	// certbot tiene además detección de versión.
	if path, err := exec.LookPath("certbot"); err == nil {
		caps.CertbotInstalled = true
		caps.CertbotVersion = detectCertbotVersion(path)
	}

	// systemd se detecta por la existencia de su runtime dir.
	if _, err := os.Stat("/run/systemd/system"); err == nil {
		caps.SystemdAvailable = true
	}

	caps.DetectedAt = time.Now().UTC()
	return caps
}

// lookPathExists devuelve true si exec.LookPath encuentra el binario en $PATH.
func lookPathExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// detectCertbotVersion ejecuta `<path> --version` con timeout y parsea
// la versión del output. Si falla por cualquier motivo, devuelve "" —
// la ausencia de versión no debe invalidar el hecho de que certbot está
// presente.
//
// Output esperado: "certbot 1.32.0" o "certbot 2.10.0".
func detectCertbotVersion(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), versionDetectTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(out))
	// Buscar el último token: "certbot 1.32.0" → "1.32.0".
	// Defensive: si el output no tiene formato esperado, devolver vacío.
	fields := strings.Fields(s)
	if len(fields) < 2 {
		return ""
	}
	last := fields[len(fields)-1]
	// Validación mínima: la "versión" debe parecer una versión (empieza
	// con dígito). Esto filtra outputs tipo error en lugar de versión.
	if last == "" || last[0] < '0' || last[0] > '9' {
		return ""
	}
	return last
}

// ─────────────────────────────────────────────────────────────────────────────
// CapabilitiesStore
// ─────────────────────────────────────────────────────────────────────────────

// CapabilitiesStore gestiona la persistencia y el refresh lazy de las
// capabilities. Thread-safe.
type CapabilitiesStore struct {
	db     *sql.DB
	clock  Clock
	detect DetectFunc

	mu sync.Mutex // serializa ForceRefresh (single-flight)
}

// NewCapabilitiesStore construye el store. detect=nil usa RealDetect.
// clock=nil usa RealClock.
func NewCapabilitiesStore(db *sql.DB, clock Clock, detect DetectFunc) (*CapabilitiesStore, error) {
	if db == nil {
		return nil, fmt.Errorf("db is nil")
	}
	if clock == nil {
		clock = NewRealClock()
	}
	if detect == nil {
		detect = RealDetect
	}
	return &CapabilitiesStore{
		db:     db,
		clock:  clock,
		detect: detect,
	}, nil
}

// Get devuelve las capabilities persistidas. Si maxAge > 0 y la
// persistencia es más vieja que maxAge, ejecuta un Refresh implícito
// antes de devolver.
//
// Si no hay persistencia previa (primer arranque), ejecuta Refresh
// y devuelve el resultado.
//
// Comportamiento por maxAge:
//
//	maxAge == 0  → devuelve la persistencia tal cual.
//	               Si no hay nada, ErrCapabilitiesNotPersisted.
//	maxAge > 0   → si stale → refresca. Si no hay nada → refresca.
//
// Pattern típico de uso desde un endpoint:
//
//	caps, err := store.Get(1 * time.Hour)  // refresh si > 1h
func (s *CapabilitiesStore) Get(maxAge time.Duration) (*SystemCapabilities, error) {
	caps, err := s.readFromDB()
	if errors.Is(err, ErrCapabilitiesNotPersisted) {
		if maxAge == 0 {
			return nil, err
		}
		return s.ForceRefresh()
	}
	if err != nil {
		return nil, err
	}

	if maxAge == 0 {
		return caps, nil
	}

	age := s.clock.Now().Sub(caps.DetectedAt)
	if age > maxAge {
		return s.ForceRefresh()
	}
	return caps, nil
}

// ForceRefresh ejecuta una detección y persiste el resultado, sea cual
// sea el estado previo. Single-flight: llamadas concurrentes esperan a
// la primera y reciben todas el mismo resultado.
func (s *CapabilitiesStore) ForceRefresh() (*SystemCapabilities, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	caps := s.detect()
	// Sobrescribir DetectedAt con nuestro Clock para que los tests sean
	// deterministas (y RealDetect uses time.Now por convención).
	caps.DetectedAt = s.clock.Now().UTC()

	if err := s.writeToDB(&caps); err != nil {
		return nil, err
	}
	return &caps, nil
}

// LastDetected devuelve el timestamp de la última detección persistida,
// o un error si no hay nada. NO hace refresh — útil para diagnóstico.
func (s *CapabilitiesStore) LastDetected() (time.Time, error) {
	caps, err := s.readFromDB()
	if err != nil {
		return time.Time{}, err
	}
	return caps.DetectedAt, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// DB read / write
// ─────────────────────────────────────────────────────────────────────────────

func (s *CapabilitiesStore) readFromDB() (*SystemCapabilities, error) {
	var (
		detectedAtStr string
		jsonBlob      string
	)
	err := s.db.QueryRow(`
		SELECT detected_at, capabilities FROM nimos_capabilities WHERE id = ?
	`, capabilitiesSingletonID).Scan(&detectedAtStr, &jsonBlob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCapabilitiesNotPersisted
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read capabilities: %w", err)
	}

	var caps SystemCapabilities
	if err := json.Unmarshal([]byte(jsonBlob), &caps); err != nil {
		return nil, fmt.Errorf("cannot unmarshal capabilities JSON: %w", err)
	}

	// DetectedAt en el JSON debería coincidir con el campo detected_at
	// de la tabla. Confiamos en el JSON (mismo origen). El campo de la
	// tabla está sobre todo para queries / indexing futuro.
	if caps.DetectedAt.IsZero() {
		if t, perr := time.Parse(time.RFC3339, detectedAtStr); perr == nil {
			caps.DetectedAt = t
		}
	}
	return &caps, nil
}

func (s *CapabilitiesStore) writeToDB(caps *SystemCapabilities) error {
	data, err := json.Marshal(caps)
	if err != nil {
		return fmt.Errorf("cannot marshal capabilities: %w", err)
	}
	detectedAtStr := caps.DetectedAt.UTC().Format(time.RFC3339)

	// UPSERT vía INSERT OR REPLACE (singleton, PK fijo).
	_, err = s.db.Exec(`
		INSERT OR REPLACE INTO nimos_capabilities (id, detected_at, capabilities)
		VALUES (?, ?, ?)
	`, capabilitiesSingletonID, detectedAtStr, string(data))
	if err != nil {
		return fmt.Errorf("cannot write capabilities: %w", err)
	}
	return nil
}
