package main

import "testing"

// ─────────────────────────────────────────────────────────────────────────────
// Snapshots · validación de nombre (seguridad: evita inyección de path)
// ─────────────────────────────────────────────────────────────────────────────

func TestIsValidSnapshotName_Valid(t *testing.T) {
	valid := []string{
		"backup",
		"daily-2026-06-02",
		"snap_01",
		"PreUpdate",
		"a",
		"mix-of_ALL-123",
	}
	for _, n := range valid {
		if !isValidSnapshotName(n) {
			t.Errorf("isValidSnapshotName(%q) = false, want true", n)
		}
	}
}

func TestIsValidSnapshotName_Invalid(t *testing.T) {
	// CRÍTICO: estos nombres NO deben aceptarse — varios son intentos de
	// inyección de path que escaparían del dir .snapshots.
	invalid := []struct {
		name string
		why  string
	}{
		{"", "vacío"},
		{"../etc/passwd", "path traversal con .."},
		{"foo/bar", "separador de path"},
		{"foo\\bar", "backslash"},
		{".hidden", "empieza por punto (.. parcial)"},
		{"with space", "espacio"},
		{"semi;colon", "metacarácter shell"},
		{"dollar$ign", "metacarácter shell"},
		{"quote'd", "comilla"},
		{"new\nline", "salto de línea"},
		{"tab\there", "tab"},
		{"slash/../escape", "traversal embebido"},
	}
	for _, c := range invalid {
		if isValidSnapshotName(c.name) {
			t.Errorf("isValidSnapshotName(%q) = true, want false (%s) — RIESGO DE SEGURIDAD", c.name, c.why)
		}
	}
}

func TestIsValidSnapshotName_TooLong(t *testing.T) {
	long := ""
	for i := 0; i < 65; i++ {
		long += "a"
	}
	if isValidSnapshotName(long) {
		t.Errorf("nombre de 65 chars debería rechazarse (límite 64)")
	}
	// 64 exacto sí vale
	ok64 := long[:64]
	if !isValidSnapshotName(ok64) {
		t.Errorf("nombre de 64 chars debería aceptarse")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// createSnapshot / rollbackSnapshot · validación de entrada (sin tocar disco)
// ─────────────────────────────────────────────────────────────────────────────

func TestCreateSnapshot_RejectsBadInput(t *testing.T) {
	cases := []map[string]interface{}{
		{},                                          // sin pool ni name
		{"pool": "data1"},                           // sin name
		{"name": "snap"},                            // sin pool
		{"pool": "data1", "name": "../escape"},      // nombre inválido
		{"pool": "data1", "name": "with space"},     // nombre inválido
	}
	for i, body := range cases {
		res := createSnapshot(body)
		if ok, _ := res["ok"].(bool); ok {
			t.Errorf("caso %d: createSnapshot(%v) aceptó entrada inválida", i, body)
		}
		if _, hasErr := res["error"]; !hasErr {
			t.Errorf("caso %d: createSnapshot debería devolver error", i)
		}
	}
}

func TestRollbackSnapshot_RejectsBadInput(t *testing.T) {
	cases := []map[string]interface{}{
		{},
		{"pool": "data1"},
		{"name": "snap"},
		{"pool": "data1", "name": "../../etc"},
	}
	for i, body := range cases {
		res := rollbackSnapshot(body)
		if ok, _ := res["ok"].(bool); ok {
			t.Errorf("caso %d: rollbackSnapshot(%v) aceptó entrada inválida", i, body)
		}
	}
}

// listSnapshots sobre un pool inexistente devuelve lista vacía, no error/panic.
func TestListSnapshots_NonexistentPoolEmpty(t *testing.T) {
	res := listSnapshots("pool-que-no-existe-xyz")
	snaps, ok := res["snapshots"].([]interface{})
	if !ok {
		t.Fatalf("listSnapshots no devolvió 'snapshots' como slice")
	}
	if len(snaps) != 0 {
		t.Errorf("pool inexistente debería dar 0 snapshots, got %d", len(snaps))
	}
}
