// storage_balance_status.go — Estado/progreso de un balance BTRFS en curso.
//
// Da soporte al modelo async de ConvertProfile: el frontend hace polling de
// GET /pools/{id}/balance-status para mostrar el progreso real del balance
// (la conversión single→raid1, etc.) mientras la Operation está in_progress.
//
// Consulta de solo lectura con timeout y estado explícito si falla.

package main

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// BalanceStatus describe el estado de un balance en un mountpoint.
type BalanceStatus struct {
	Known       bool    `json:"known"`        // false means the query failed or its output was not recognized
	Active      bool    `json:"active"`       // hay un balance en curso (running o paused)
	Paused      bool    `json:"paused"`       // está pausado
	PercentDone float64 `json:"percent_done"` // progreso 0-100 (estimado de btrfs)
	Detail      string  `json:"detail"`       // línea informativa de btrfs (chunks)
}

// A status query is bounded; it must never hang recovery on a stuck command.
func readBalanceStatus(mountPoint string) BalanceStatus {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, code := runSafeStatusContext(ctx, "btrfs", "balance", "status", mountPoint)
	return balanceStatusFromCommand(out, code)
}

func balanceStatusFromCommand(out string, code int) BalanceStatus {
	st := parseBalanceStatus(out)
	// Some versions return 1 for an active balance. Never accept an error as
	// evidence that the filesystem is idle, even if it contains a known phrase.
	if code != 0 && !(code == 1 && st.Known && st.Active) {
		return BalanceStatus{}
	}
	return st
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

	if strings.HasPrefix(strings.TrimSpace(low), "no balance found on '") && !strings.Contains(low, "error:") {
		st.Known = true
		return st
	}
	if !strings.HasPrefix(strings.TrimSpace(low), "balance on '") || strings.Contains(low, "error:") {
		return st
	}
	if strings.Contains(low, "is running") {
		st.Active = true
	}
	if strings.Contains(low, "is paused") {
		st.Active = true
		st.Paused = true
	}
	if !st.Active {
		// Unknown output is not evidence of an idle filesystem.
		return st
	}

	st.Known = true

	// Progreso desde "P% left" (lo que btrfs estima que queda).
	if m := chunksRe.FindStringSubmatch(out); len(m) == 4 {
		if left, err := strconv.ParseFloat(m[3], 64); err == nil {
			st.PercentDone = 100 - left
		}
		st.Detail = strings.TrimSpace(m[0])
	}
	return st
}
