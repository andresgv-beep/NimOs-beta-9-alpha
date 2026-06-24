package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// withTempJournal redirige journalPath a un archivo temporal durante el test.
func withTempJournal(t *testing.T) {
	t.Helper()
	old := journalPath
	journalPath = filepath.Join(t.TempDir(), "storage-journal.json")
	t.Cleanup(func() { journalPath = old })
}

// STOR-06 · journalRecoverOnBoot debe detectar y limpiar un journal de wipe
// interrumpido, y no hacer nada si no hay journal.

func TestJournalRecoverOnBoot_NoJournal(t *testing.T) {
	withTempJournal(t)
	// (no creamos journal)
	if journalRecoverOnBoot() {
		t.Error("sin journal, journalRecoverOnBoot debería devolver false")
	}
}

func TestJournalRecoverOnBoot_PendingJournalCleared(t *testing.T) {
	withTempJournal(t)
	// Escribir un journal de wipe "interrumpido"
	op := JournalOp{
		ID:     "test-wipe-op",
		Type:   "wipe",
		Step:   2,
		Phase:  PhaseStarted,
		Status: OpPending,
		Data:   map[string]string{"device": "/dev/loopX"},
	}
	if err := journalSave(op); err != nil {
		t.Fatalf("journalSave: %v", err)
	}

	// Confirmar que el archivo existe antes
	if _, err := os.Stat(journalPath); err != nil {
		t.Fatalf("el journal debería existir antes del recovery: %v", err)
	}

	// Recovery debe detectarlo (true) y limpiarlo
	if !journalRecoverOnBoot() {
		t.Error("con journal pendiente, debería devolver true")
	}

	// El journal debe haberse limpiado
	if _, err := os.Stat(journalPath); !os.IsNotExist(err) {
		t.Error("el journal debería estar limpiado tras el recovery")
	}
}

func TestJournalRecoverOnBoot_CorruptJournalCleared(t *testing.T) {
	withTempJournal(t)
	// Escribir basura en el journal (corrupto)
	if err := os.WriteFile(journalPath, []byte("{ esto no es json valido"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Recovery debe limpiarlo igualmente (no arrastrar un journal corrupto)
	if !journalRecoverOnBoot() {
		t.Error("con journal corrupto, debería devolver true (lo limpia)")
	}
	if _, err := os.Stat(journalPath); !os.IsNotExist(err) {
		t.Error("el journal corrupto debería haberse limpiado")
	}
}

// Sanity: el journal que escribimos es JSON válido y releíble.
func TestJournalSaveRoundtrip(t *testing.T) {
	withTempJournal(t)
	op := JournalOp{ID: "rt", Type: "wipe", Step: 1, Phase: PhaseCompleted, Status: OpDone}
	if err := journalSave(op); err != nil {
		t.Fatalf("journalSave: %v", err)
	}
	data, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got JournalOp
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("el journal no es JSON válido: %v", err)
	}
	if got.ID != "rt" || got.Step != 1 {
		t.Errorf("roundtrip falló: got %+v", got)
	}
}
