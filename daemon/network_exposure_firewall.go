// network_exposure_firewall.go — Sincronización del firewall del host con
// los puertos de exposición.
//
// PROBLEMA QUE RESUELVE: NimOS permite configurar los puertos HTTP/HTTPS de
// Caddy desde la UI, pero si el host tiene un firewall con política de
// bloqueo (ej. ufw con "deny incoming"), mover Caddy a un puerto nuevo lo
// deja inaccesible aunque todo lo demás esté perfecto: el firewall tira los
// paquetes antes de que lleguen. Si NimOS gestiona los puertos, NimOS debe
// gestionar también su paso por el firewall.
//
// DISEÑO v1 (semilla de NimShield Phase 2):
//   · Soporta ufw (el gestor más común en instalaciones caseras). Si ufw
//     no existe o está inactivo → no-op silencioso (no hay muro que abrir).
//   · NimOS solo toca reglas QUE ÉL GESTIONA: la lista de puertos abiertos
//     por NimOS se persiste (fw_managed_ports) y solo esos se retiran
//     cuando el usuario cambia de puerto. Las reglas del usuario no se tocan.
//   · El puerto 22 (SSH) JAMÁS se elimina, esté donde esté: cortarse el
//     acceso remoto por un bug sería catastrófico.
//   · Ejecución segura: exec con argumentos separados y puertos validados
//     como enteros — nunca sh -c, cero superficie de inyección.
//   · Best-effort desde el reconciler: si falla, se emite evento y el
//     reverse proxy sigue funcionando.
//
// Cuando NimShield Phase 2 exista (gobierno completo de nftables), esta
// lógica se mudará allí con su dueño natural.

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// commandRunner abstrae la ejecución de comandos para poder testear sin
// un ufw real.
type commandRunner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

// realCommandRunner ejecuta comandos de verdad.
type realCommandRunner struct{}

func (realCommandRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	// Locale fijado a C: parseamos la salida de comandos (p.ej. `ufw status`)
	// por sus literales en inglés ("Status: active"). En un sistema con locale
	// es_ES, ufw responde "Estado: activo" y la comprobación fallaba en
	// silencio → NimOS creía ufw inactivo y nunca abría los puertos de
	// exposición (80/443/444…). Pin obligatorio para cualquier parseo de CLI.
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// firewallEnsurer es lo que el reconciler necesita del firewall. Interfaz
// para mockear en tests.
type firewallEnsurer interface {
	// EnsurePorts garantiza que wantPorts están permitidos y retira los
	// puertos de prevManaged que ya no se quieren. Devuelve la nueva lista
	// de puertos gestionados y si cambió respecto a prevManaged.
	EnsurePorts(ctx context.Context, wantPorts, prevManaged []int) (managed []int, changed bool, err error)
}

// UFWFirewall implementa firewallEnsurer sobre ufw.
type UFWFirewall struct {
	run commandRunner
}

// NewUFWFirewall construye el syncer. runner nil → ejecución real.
func NewUFWFirewall(runner commandRunner) *UFWFirewall {
	if runner == nil {
		runner = realCommandRunner{}
	}
	return &UFWFirewall{run: runner}
}

// EnsurePorts implementa firewallEnsurer.
//
// Flujo:
//  1. `ufw status` — si ufw no existe o está inactivo, no se gestiona nada
//     (managed vacío). Sin firewall activo no hay nada que abrir.
//  2. Permitir (allow) los wantPorts que no estén ya en las reglas.
//  3. Retirar (delete allow) los prevManaged que ya no se quieren — solo
//     los nuestros, nunca el 22, y nunca uno que siga en wantPorts.
func (f *UFWFirewall) EnsurePorts(ctx context.Context, wantPorts, prevManaged []int) ([]int, bool, error) {
	out, err := f.run.Run(ctx, "ufw", "status")
	if err != nil {
		// ufw no instalado o no ejecutable: no hay firewall que gestionar.
		// No es un error de NimOS — degradación silenciosa.
		return nil, len(prevManaged) > 0, nil
	}
	if !strings.Contains(out, "Status: active") {
		// Firewall apagado: nada que abrir, y dejamos de "gestionar" lo
		// previo (si el usuario lo reactiva, el siguiente ciclo re-abre).
		return nil, len(prevManaged) > 0, nil
	}

	existing := parseUFWAllowedPorts(out)

	want := dedupeValidPorts(wantPorts)
	for _, p := range want {
		if existing[p] {
			continue
		}
		spec := fmt.Sprintf("%d/tcp", p)
		if out, err := f.run.Run(ctx, "ufw", "allow", spec); err != nil {
			return prevManaged, false, fmt.Errorf("ufw allow %s: %v: %s", spec, err, strings.TrimSpace(out))
		}
	}

	wantSet := map[int]bool{}
	for _, p := range want {
		wantSet[p] = true
	}
	for _, p := range dedupeValidPorts(prevManaged) {
		if wantSet[p] {
			continue
		}
		if p == 22 {
			// GUARDARRAÍL: jamás retirar SSH, pase lo que pase con el estado.
			continue
		}
		if !existing[p] {
			continue // ya no está; nada que retirar
		}
		spec := fmt.Sprintf("%d/tcp", p)
		if out, err := f.run.Run(ctx, "ufw", "delete", "allow", spec); err != nil {
			return prevManaged, false, fmt.Errorf("ufw delete allow %s: %v: %s", spec, err, strings.TrimSpace(out))
		}
	}

	changed := !equalIntSets(want, prevManaged)
	return want, changed, nil
}

// parseUFWAllowedPorts extrae de la salida de `ufw status` los puertos TCP
// con regla ALLOW. Reconoce "444/tcp", "444" (sin protocolo = ambos) y sus
// variantes (v6).
func parseUFWAllowedPorts(out string) map[int]bool {
	ports := map[int]bool{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		action := strings.ToUpper(fields[1])
		if action != "ALLOW" {
			continue
		}
		spec := fields[0] // "444/tcp" | "444" | "444/udp"
		if strings.HasSuffix(spec, "/udp") {
			continue
		}
		spec = strings.TrimSuffix(spec, "/tcp")
		var p int
		if _, err := fmt.Sscanf(spec, "%d", &p); err == nil && p >= 1 && p <= 65535 {
			ports[p] = true
		}
	}
	return ports
}

// dedupeValidPorts filtra a puertos válidos (1-65535), deduplica y ordena
// (orden estable para comparaciones y persistencia determinista).
func dedupeValidPorts(in []int) []int {
	seen := map[int]bool{}
	out := []int{}
	for _, p := range in {
		if p < 1 || p > 65535 || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}

// equalIntSets compara dos listas como conjuntos.
func equalIntSets(a, b []int) bool {
	a, b = dedupeValidPorts(a), dedupeValidPorts(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
