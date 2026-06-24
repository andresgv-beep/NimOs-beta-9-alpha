package main

import (
	"fmt"
	"testing"
)

// Reproduce el formato EXACTO de error que produce runCmd cuando el comando
// sale con código != 0: "<cmd> failed (code N): <stderr>", con CmdResult vacío.
func TestInterpretQuotaShow(t *testing.T) {
	cases := []struct {
		name        string
		res         CmdResult
		err         error
		wantEnabled bool
		wantErr     bool
	}{
		{
			name:        "quota habilitada (code 0, lista tabla)",
			res:         CmdResult{Code: 0, OK: true, Stdout: "Qgroupid Referenced Exclusive Path\n0/5 16.00KiB 16.00KiB <toplevel>\n"},
			err:         nil,
			wantEnabled: true,
			wantErr:     false,
		},
		{
			// ESTE es el caso que fallaba esta mañana: runCmd devuelve
			// CmdResult{} vacío + el stderr embebido en el error.
			name:        "quota NO habilitada (caso real de la x86)",
			res:         CmdResult{},
			err:         fmt.Errorf("btrfs failed (code 1): ERROR: can't list qgroups: quotas not enabled"),
			wantEnabled: false,
			wantErr:     false, // ← NO es error, es "no habilitada"
		},
		{
			name:        "error real: no es un filesystem btrfs",
			res:         CmdResult{},
			err:         fmt.Errorf("btrfs failed (code 1): ERROR: not a btrfs filesystem: /nimos/pools/x"),
			wantEnabled: false,
			wantErr:     true, // ← esto SÍ es error real
		},
		{
			name:        "error real: path inexistente",
			res:         CmdResult{},
			err:         fmt.Errorf("btrfs failed (code 1): ERROR: cannot access '/nope': No such file or directory"),
			wantEnabled: false,
			wantErr:     true,
		},
		{
			// Variante defensiva: si algún día runCmd SÍ rellenara Stderr.
			name:        "not enabled en res.Stderr en vez de err",
			res:         CmdResult{Code: 1, Stderr: "ERROR: can't list qgroups: quotas not enabled"},
			err:         nil,
			wantEnabled: false,
			wantErr:     false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			enabled, err := interpretQuotaShow(c.res, c.err)
			if enabled != c.wantEnabled {
				t.Errorf("enabled = %v, want %v", enabled, c.wantEnabled)
			}
			if (err != nil) != c.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, c.wantErr)
			}
		})
	}
}
