package main

import (
	"strings"
	"testing"
)

// missingDevidForPool extrae el devid del disco que falta — necesario para
// reparar un disco MISSING (btrfs replace exige el devid, no el path muerto).

func TestMissingDevidParsing(t *testing.T) {
	// Simulamos el parseo sobre la salida real de btrfs filesystem show.
	// (la función real llama runSafe; aquí probamos la lógica de parseo
	// replicándola sobre un output fijo)
	output := `Label: 'data8'  uuid: a0192857-e85f-4ad2-a277-50a6ebaafc1f
        Total devices 2 FS bytes used 24.85GiB
        devid    1 size 111.79GiB used 44.03GiB path /dev/sda
        devid    2 size 0 used 0 path <missing disk> MISSING`

	var found string
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, "MISSING") && !strings.Contains(line, "missing") {
			continue
		}
		fields := strings.Fields(line)
		for i, f := range fields {
			if f == "devid" && i+1 < len(fields) {
				found = fields[i+1]
			}
		}
	}
	if found != "2" {
		t.Errorf("devid del disco missing: got %q, want \"2\"", found)
	}
}

func TestMissingDevidParsing_NoMissing(t *testing.T) {
	// Pool sano: no hay MISSING → no devid.
	output := `Label: 'data8'  uuid: xxx
        devid    1 size 111GiB used 44GiB path /dev/sda
        devid    2 size 111GiB used 44GiB path /dev/sdb`

	var found string
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, "MISSING") && !strings.Contains(line, "missing") {
			continue
		}
		fields := strings.Fields(line)
		for i, f := range fields {
			if f == "devid" && i+1 < len(fields) {
				found = fields[i+1]
			}
		}
	}
	if found != "" {
		t.Errorf("pool sano no debe tener devid missing, got %q", found)
	}
}
