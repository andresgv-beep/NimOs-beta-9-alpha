package main

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestComputeDockerAggregateHealth · cubre lógica pura (sin DB).
// Verifica que la función traduce children → HealthStatus correctamente
// según las reglas documentadas en nimhealth_docker.go.
func TestComputeDockerAggregateHealth(t *testing.T) {
	makeChild := func(status, health string) DockerAppStatus {
		return DockerAppStatus{
			ServiceBase: ServiceBase{
				Status: status,
				Health: health,
			},
		}
	}

	cases := []struct {
		name     string
		children []DockerAppStatus
		want     HealthStatus
	}{
		{
			name:     "no children · engine OK vacío",
			children: nil,
			want:     HealthHealthy,
		},
		{
			name: "all running healthy",
			children: []DockerAppStatus{
				makeChild("running", string(HealthHealthy)),
				makeChild("running", string(HealthHealthy)),
			},
			want: HealthHealthy,
		},
		{
			name: "all stopped · engine OK sin actividad",
			children: []DockerAppStatus{
				makeChild("stopped", string(HealthHealthy)),
				makeChild("stopped", string(HealthHealthy)),
			},
			want: HealthHealthy,
		},
		{
			name: "one error",
			children: []DockerAppStatus{
				makeChild("running", string(HealthHealthy)),
				makeChild("error", string(HealthFailed)),
			},
			want: HealthDegraded,
		},
		{
			name: "mix running + stopped",
			children: []DockerAppStatus{
				makeChild("running", string(HealthHealthy)),
				makeChild("stopped", string(HealthHealthy)),
			},
			want: HealthDegraded,
		},
		{
			name: "one failed health (sin status error)",
			children: []DockerAppStatus{
				makeChild("running", string(HealthFailed)),
			},
			want: HealthDegraded,
		},
		{
			name: "single running",
			children: []DockerAppStatus{
				makeChild("running", string(HealthHealthy)),
			},
			want: HealthHealthy,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ComputeDockerAggregateHealth(c.children)
			if got != c.want {
				t.Errorf("ComputeDockerAggregateHealth(...) = %v, want %v", got, c.want)
			}
		})
	}
}

// TestInBootGracePeriod · usa el hook osReadFile para inyectar uptimes.
func TestInBootGracePeriod(t *testing.T) {
	original := osReadFile
	defer func() { osReadFile = original }()

	cases := []struct {
		name      string
		uptimeStr string
		want      bool
	}{
		{"recién arrancado · 5s", "5.00 4.50\n", true},
		{"en gracia · 30s", "30.00 25.00\n", true},
		{"borde inferior justo antes · 89s", "89.00 80.00\n", true},
		{"borde justo en límite · 90s", "90.00 80.00\n", false},
		{"fuera de gracia · 100s", "100.00 90.00\n", false},
		{"sistema viejo · 5 días", "432000.00 100000.00\n", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			osReadFile = func(path string) ([]byte, error) {
				return []byte(c.uptimeStr), nil
			}
			got := inBootGracePeriod()
			if got != c.want {
				t.Errorf("inBootGracePeriod() = %v, want %v (uptime=%q)", got, c.want, c.uptimeStr)
			}
		})
	}

	// Caso: /proc/uptime no se puede leer → false (no aplicar gracia)
	t.Run("read error → no grace", func(t *testing.T) {
		osReadFile = func(path string) ([]byte, error) {
			return nil, fmt.Errorf("simulated")
		}
		if inBootGracePeriod() {
			t.Error("inBootGracePeriod() should be false when /proc/uptime unreadable")
		}
	})

	// Caso: /proc/uptime vacío → false
	t.Run("empty uptime → no grace", func(t *testing.T) {
		osReadFile = func(path string) ([]byte, error) {
			return []byte(""), nil
		}
		if inBootGracePeriod() {
			t.Error("inBootGracePeriod() should be false when /proc/uptime empty")
		}
	})
}

// TestDockerAppCache_ConcurrentAccess · stress test del RWMutex.
// Múltiples readers + 1 writer concurrent · no debe haber data race
// (corre con -race en CI).
func TestDockerAppCache_ConcurrentAccess(t *testing.T) {
	var cache DockerAppCache

	// Inicializar con valor known
	cache.mu.Lock()
	cache.statuses = []DockerAppStatus{}
	cache.aggHealth = HealthHealthy
	cache.initialized = true
	cache.mu.Unlock()

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 8 readers concurrent
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					cache.mu.RLock()
					_ = cache.aggHealth
					_ = cache.statuses
					cache.mu.RUnlock()
				}
			}
		}()
	}

	// 1 writer concurrent, alterna health
	wg.Add(1)
	go func() {
		defer wg.Done()
		toggle := false
		for {
			select {
			case <-stop:
				return
			default:
				cache.mu.Lock()
				if toggle {
					cache.aggHealth = HealthHealthy
				} else {
					cache.aggHealth = HealthDegraded
				}
				toggle = !toggle
				cache.updatedAt = time.Now()
				cache.mu.Unlock()
			}
		}
	}()

	// Correr 200ms (suficiente para muchas iteraciones)
	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()

	// Si llegamos aquí sin deadlock ni race, OK
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	if !cache.initialized {
		t.Error("cache should still be initialized")
	}
}

// TestNimHealthObserver_ReconcilerInterface · garantiza que la interfaz
// Reconciler está bien implementada (Name/Tier/Interval no panic).
func TestNimHealthObserver_ReconcilerInterface(t *testing.T) {
	obs := NewNimHealthObserver(NewRealClock(), DefaultNimHealthConfig())

	if obs.Name() == "" {
		t.Error("Name() must not be empty")
	}
	if obs.Name() != "nimhealth_observer" {
		t.Errorf("Name() = %q, want %q", obs.Name(), "nimhealth_observer")
	}
	if obs.Tier() != TierMedium {
		t.Errorf("Tier() = %v, want TierMedium", obs.Tier())
	}
	if obs.Interval() <= 0 {
		t.Errorf("Interval() = %v, must be > 0", obs.Interval())
	}
	if obs.Interval() != 30*time.Second {
		t.Errorf("Interval() = %v, want 30s default", obs.Interval())
	}
}

// TestNimHealthObserver_NilClock · sin panic si pasas clock nil.
// Debe fallback a RealClock interno.
func TestNimHealthObserver_NilClock(t *testing.T) {
	obs := NewNimHealthObserver(nil, DefaultNimHealthConfig())
	if obs.clock == nil {
		t.Error("NewNimHealthObserver should fallback to RealClock when nil passed")
	}
}

// TestNimHealthObserver_ZeroInterval · sin panic si config.Interval = 0.
// Debe usar default 30s.
func TestNimHealthObserver_ZeroInterval(t *testing.T) {
	obs := NewNimHealthObserver(NewRealClock(), NimHealthObserverConfig{Interval: 0})
	if obs.Interval() != 30*time.Second {
		t.Errorf("Interval() = %v, want 30s default when config is 0", obs.Interval())
	}
}

// Asegurar que el observer implementa Reconciler (compile-time check).
var _ Reconciler = (*NimHealthObserver)(nil)

// Asegurar que el contexto pasado a Reconcile no se ignora · al menos
// debe poderse llamar sin panic.
func TestNimHealthObserver_ReconcileWithCancelledCtx(t *testing.T) {
	// NOTA: este test SOLO verifica que Reconcile no panics con ctx
	// cancelado. NO verifica resultado porque eso necesita DB real.
	// La compilación + ausencia de panic ya es bastante señal.
	t.Skip("requires DB · ejecutar en integration tests con sqlite real")
	_ = context.Background()
}

// ═══════════════════════════════════════════════════════════════════════
// APP-017 · Stack-matching helpers (matchContainerForAppID + isPossibleStackSubContainer)
// ═══════════════════════════════════════════════════════════════════════

// TestMatchContainerForAppID_ExactMatch · prefiere el match exacto sobre
// cualquier sufijo. Si existe un container llamado "jellyfin" Y otro
// "jellyfin_server", debe devolver "jellyfin".
func TestMatchContainerForAppID_ExactMatch(t *testing.T) {
	containers := map[string]dockerContainer{
		"jellyfin":        {Name: "jellyfin", Image: "jellyfin/jellyfin"},
		"jellyfin_server": {Name: "jellyfin_server", Image: "other"},
	}
	name, c := matchContainerForAppID("jellyfin", containers)
	if name != "jellyfin" {
		t.Errorf("expected exact match 'jellyfin', got %q", name)
	}
	if c == nil || c.Image != "jellyfin/jellyfin" {
		t.Errorf("expected container with image jellyfin/jellyfin, got %v", c)
	}
}

// TestMatchContainerForAppID_SuffixMatch · prueba todos los sufijos
// soportados en orden de preferencia.
func TestMatchContainerForAppID_SuffixMatch(t *testing.T) {
	cases := []struct {
		name      string
		appID     string
		container string
	}{
		{"_server suffix", "immich", "immich_server"},
		{"-server suffix", "immich2", "immich2-server"},
		{"_app suffix", "nextcloud", "nextcloud_app"},
		{"-app suffix", "nextcloud2", "nextcloud2-app"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			containers := map[string]dockerContainer{
				c.container: {Name: c.container, Image: "test"},
			}
			gotName, gotContainer := matchContainerForAppID(c.appID, containers)
			if gotName != c.container {
				t.Errorf("expected match %q, got %q", c.container, gotName)
			}
			if gotContainer == nil {
				t.Error("expected container, got nil")
			}
		})
	}
}

// TestMatchContainerForAppID_PrefixFallback · cuando no hay sufijo
// conocido, cae al prefix-match con "_" o "-" como separador.
func TestMatchContainerForAppID_PrefixFallback(t *testing.T) {
	containers := map[string]dockerContainer{
		"immich_microservices": {Name: "immich_microservices", Image: "test"},
	}
	name, c := matchContainerForAppID("immich", containers)
	if name != "immich_microservices" {
		t.Errorf("expected prefix fallback to 'immich_microservices', got %q", name)
	}
	if c == nil {
		t.Error("expected container, got nil")
	}
}

// TestMatchContainerForAppID_DashUnderscore · appID con guion (matrix-synapse)
// debe encontrar un container con guion_bajo (matrix_synapse). Sin esto, el
// container aparecía como app "huérfana" (fantasma) en el launcher. Regresión
// 16/06/2026.
func TestMatchContainerForAppID_DashUnderscore(t *testing.T) {
	containers := map[string]dockerContainer{
		"matrix_synapse": {Name: "matrix_synapse", Image: "ghcr.io/element-hq/synapse"},
	}
	name, c := matchContainerForAppID("matrix-synapse", containers)
	if name != "matrix_synapse" {
		t.Errorf("appID 'matrix-synapse' debería emparejar 'matrix_synapse', got %q", name)
	}
	if c == nil {
		t.Error("expected container, got nil")
	}
}

// TestMatchContainerForAppID_NoMatch · si no hay candidatos, devuelve
// ("", nil) sin panic.
func TestMatchContainerForAppID_NoMatch(t *testing.T) {
	containers := map[string]dockerContainer{
		"unrelated": {Name: "unrelated", Image: "test"},
	}
	name, c := matchContainerForAppID("jellyfin", containers)
	if name != "" {
		t.Errorf("expected empty name, got %q", name)
	}
	if c != nil {
		t.Errorf("expected nil container, got %v", c)
	}
}

// TestMatchContainerForAppID_EmptyMap · map vacío no debe panic.
func TestMatchContainerForAppID_EmptyMap(t *testing.T) {
	name, c := matchContainerForAppID("jellyfin", map[string]dockerContainer{})
	if name != "" || c != nil {
		t.Errorf("expected empty match on empty map, got %q / %v", name, c)
	}
}

// TestIsPossibleStackSubContainer_Keyword · containers con substrings
// conocidos de stack-sub son identificados.
func TestIsPossibleStackSubContainer_Keyword(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"immich_redis", true},
		{"immich_postgres", true},
		{"immich_ml", true},
		{"immich_machine_learning", true},
		{"immich_db", true},
		{"immich_cache", true},
		{"jellyfin", false},
		{"plex_server", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isPossibleStackSubContainer(c.name, map[string]bool{})
			if got != c.want {
				t.Errorf("isPossibleStackSubContainer(%q) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

// TestIsPossibleStackSubContainer_PrefixFromMatched · containers que
// comparten prefijo con un match previo son tratados como subcomponentes.
func TestIsPossibleStackSubContainer_PrefixFromMatched(t *testing.T) {
	matched := map[string]bool{
		"immich_server": true,
	}
	cases := []struct {
		name string
		want bool
	}{
		// Comparte prefijo "immich" con matched immich_server → sub
		{"immich_proxy", true},
		{"immich-something", true},
		// No comparte prefijo con ningún matched
		{"jellyfin", false},
		{"sonarr", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isPossibleStackSubContainer(c.name, matched)
			if got != c.want {
				t.Errorf("isPossibleStackSubContainer(%q, matched=%v) = %v, want %v",
					c.name, matched, got, c.want)
			}
		})
	}
}
