// storage_balance_status.go — Estado/progreso de un balance BTRFS en curso.
//
// Da soporte al modelo async de ConvertProfile: el frontend hace polling de
// GET /pools/{id}/balance-status para mostrar el progreso real del balance
// (la conversión single→raid1, etc.) mientras la Operation está in_progress.
//
// Es SOLO LECTURA (btrfs balance status), por eso usa runSafe directamente
// (patrón de readRealDataProfile) en vez de pasar por el executor.

package main

import (
	"regexp"
	"strconv"
	"strings"
)

// BalanceStatus describe el estado de un balance en un mountpoint.
type BalanceStatus struct {
	Active      bool    `json:"active"`       // hay un balance en curso (running o paused)
	Paused      bool    `json:"paused"`       // está pausado
	PercentDone float64 `json:"percent_done"` // progreso 0-100 (estimado de btrfs)
	Detail      string  `json:"detail"`       // línea informativa de btrfs (chunks)
}

// readBalanceStatus consulta `btrfs balance status <mp>` y devuelve el estado.
// Nota: btrfs balance status devuelve exit code 1 cuando HAY balance en curso
// (peculiaridad de la herramienta), así que no tratamos el exit != 0 como
// error — parseamos el output directamente.
func readBalanceStatus(mountPoint string) BalanceStatus {
	out, _ := runSafe("btrfs", "balance", "status", mountPoint)
	return parseBalanceStatus(out)
}

// chunksRe captura "N out of about M chunks balanced (K considered), P% left"
var chunksRe = regexp.MustCompile(`(\d+)\s+out of about\s+(\d+)\s+chunks balanced.*?(\d+)%\s+left`)

// parseBalanceStatus interpreta la salida de `btrfs balance status`.
// Función pura, testeable sin discos.
//
// Salidas posibles de btrfs:
//
//	"No balance found on '/mnt'"                          → inactivo
//	"Balance on '/mnt' is running\n3 out of about 10..."  → activo, % calculable
//	"Balance on '/mnt' is paused\n..."                    → activo + pausado
func parseBalanceStatus(out string) BalanceStatus {
	st := BalanceStatus{}
	low := strings.ToLower(out)

	if strings.Contains(low, "no balance found") || strings.TrimSpace(out) == "" {
		return st // Active=false
	}
	if strings.Contains(low, "is running") {
		st.Active = true
	}
	if strings.Contains(low, "is paused") {
		st.Active = true
		st.Paused = true
	}
	if !st.Active {
		// Output no reconocido → mejor reportar inactivo que inventar estado.
		return st
	}

	// Progreso desde "P% left" (lo que btrfs estima que queda).
	if m := chunksRe.FindStringSubmatch(out); len(m) == 4 {
		if left, err := strconv.ParseFloat(m[3], 64); err == nil {
			st.PercentDone = 100 - left
		}
		st.Detail = strings.TrimSpace(m[0])
	}
	return st
}
