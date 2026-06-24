package main

import "testing"

// ─── smartOutputIsUsable — fix del fallback SAT ───────────────────────────────
//
// El monitor probaba smartctl sin -d sat; en discos detrás de capa SAT eso
// devuelve solo el aviso "Probable ATA device behind a SAT layer". Esta función
// distingue una lectura útil (tabla SMART real) de ese aviso, para decidir si
// hay que reintentar con otro device-type.

func TestSmartUsable_RealAttributeTable(t *testing.T) {
	out := `smartctl 7.5
ID# ATTRIBUTE_NAME          FLAG     VALUE WORST THRESH TYPE
197 Current_Pending_Sector  0x0012   100   100   000   Old_age
`
	if !smartOutputIsUsable(out) {
		t.Error("tabla de atributos real debe ser usable")
	}
}

func TestSmartUsable_HealthVerdict(t *testing.T) {
	out := "SMART overall-health self-assessment test result: PASSED\n"
	if !smartOutputIsUsable(out) {
		t.Error("veredicto de salud debe ser usable")
	}
}

func TestSmartUsable_SatHintOnly(t *testing.T) {
	// El caso real que disparó el bug: solo el aviso, sin datos.
	out := `smartctl 7.5 2025-04-30
Copyright (C) 2002-25
Probable ATA device behind a SAT layer
Try an additional '-d ata' or '-d sat' argument.
`
	if smartOutputIsUsable(out) {
		t.Error("solo el aviso SAT (sin tabla) NO debe ser usable → hay que reintentar")
	}
}

func TestSmartUsable_Empty(t *testing.T) {
	if smartOutputIsUsable("") {
		t.Error("vacío no es usable")
	}
}

func TestSmartUsable_SatHintButWithData(t *testing.T) {
	// Si el aviso aparece pero TAMBIÉN hay tabla real, es usable.
	out := `Probable ATA device behind a SAT layer
ID# ATTRIBUTE_NAME          FLAG
197 Current_Pending_Sector  0x0012   100   100   000
`
	if !smartOutputIsUsable(out) {
		t.Error("aviso + tabla real debe ser usable")
	}
}
