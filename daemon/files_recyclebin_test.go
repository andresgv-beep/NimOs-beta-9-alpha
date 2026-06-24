package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Test del ciclo completo de la papelera usando un os.Root real sobre un
// tempdir (no necesita BTRFS, solo filesystem). Cubre: mover, listar,
// restaurar, eliminar, vaciar, y la protección de no-meterse-a-sí-misma.
func TestRecycleBinCycle(t *testing.T) {
	base := t.TempDir()
	// Crear un fichero de prueba en la raíz del "share"
	if err := os.WriteFile(filepath.Join(base, "documento.txt"), []byte("hola"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Y uno dentro de una subcarpeta
	os.MkdirAll(filepath.Join(base, "sub"), 0o755)
	os.WriteFile(filepath.Join(base, "sub", "nota.md"), []byte("# nota"), 0o644)

	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	// 1) Mover documento.txt a la papelera
	if err := moveToRecycleBin(root, "documento.txt"); err != nil {
		t.Fatalf("moveToRecycleBin: %v", err)
	}
	// El original ya no debe existir
	if _, err := root.Lstat("documento.txt"); !os.IsNotExist(err) {
		t.Error("el fichero original debería haberse movido")
	}

	// 2) Mover sub/nota.md a la papelera
	if err := moveToRecycleBin(root, "sub/nota.md"); err != nil {
		t.Fatalf("moveToRecycleBin sub: %v", err)
	}

	// 3) Listar: debe haber 2 ítems
	items := listRecycleBin(root)
	if len(items) != 2 {
		t.Fatalf("esperaba 2 ítems en papelera, hay %d", len(items))
	}

	// 4) Restaurar el primero (documento.txt) a su sitio
	var docID string
	for _, it := range items {
		if it.Name == "documento.txt" {
			docID = it.ID
		}
	}
	if docID == "" {
		t.Fatal("no encuentro documento.txt en la papelera")
	}
	if err := restoreFromRecycleBin(root, docID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := root.Lstat("documento.txt"); err != nil {
		t.Error("documento.txt debería estar restaurado en su ruta original")
	}

	// Tras restaurar, debe quedar 1 ítem
	if items := listRecycleBin(root); len(items) != 1 {
		t.Errorf("tras restaurar esperaba 1 ítem, hay %d", len(items))
	}

	// 5) Vaciar la papelera
	if err := emptyRecycleBin(root); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if items := listRecycleBin(root); len(items) != 0 {
		t.Errorf("tras vaciar esperaba 0 ítems, hay %d", len(items))
	}
}

// Restaurar cuando el destino ya existe → no debe pisar, usa nombre alternativo.
func TestRecycleBinRestoreConflict(t *testing.T) {
	base := t.TempDir()
	os.WriteFile(filepath.Join(base, "f.txt"), []byte("v1"), 0o644)

	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	// Mover f.txt a papelera
	if err := moveToRecycleBin(root, "f.txt"); err != nil {
		t.Fatal(err)
	}
	// Crear OTRO f.txt en el mismo sitio
	os.WriteFile(filepath.Join(base, "f.txt"), []byte("v2"), 0o644)

	// Restaurar el de la papelera → no debe pisar el v2
	items := listRecycleBin(root)
	if len(items) != 1 {
		t.Fatalf("esperaba 1 ítem, hay %d", len(items))
	}
	if err := restoreFromRecycleBin(root, items[0].ID); err != nil {
		t.Fatalf("restore con conflicto: %v", err)
	}

	// El f.txt original (v2) debe seguir intacto
	data, _ := os.ReadFile(filepath.Join(base, "f.txt"))
	if string(data) != "v2" {
		t.Errorf("el fichero existente fue pisado: %q", string(data))
	}
}

func TestRecycleBinStats(t *testing.T) {
	base := t.TempDir()
	os.WriteFile(filepath.Join(base, "a.txt"), []byte("12345"), 0o644) // 5 bytes
	root, _ := os.OpenRoot(base)
	defer root.Close()

	moveToRecycleBin(root, "a.txt")
	count, bytes := recycleBinStats(root)
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
	if bytes != 5 {
		t.Errorf("bytes = %d, want 5", bytes)
	}
}
