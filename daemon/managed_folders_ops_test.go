package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckFolderRelPath(t *testing.T) {
	valid := []string{"proyectos", "fotos", "backup_2026", "mi-carpeta"}
	for _, v := range valid {
		if err := checkFolderRelPath(v); err != nil {
			t.Errorf("checkFolderRelPath(%q) debería ser válido, got %v", v, err)
		}
	}

	invalid := []string{
		"",                  // vacía
		"a/b",               // anidada (no primer nivel)
		"../escape",         // traversal
		"..",                // padre
		".",                 // actual
		".oculta",           // empieza por punto
		"sub/../otra",       // traversal disfrazado
		"con\\barra",        // backslash
		"/absoluta",         // absoluta
	}
	for _, iv := range invalid {
		if err := checkFolderRelPath(iv); err == nil {
			t.Errorf("checkFolderRelPath(%q) debería ser inválido, pero pasó", iv)
		}
	}
}

func TestPoolMountFromSharePath(t *testing.T) {
	cases := map[string]string{
		"/nimos/pools/data8/shares/media": "/nimos/pools/data8",
		"/nimos/pools/tester/shares/docs": "/nimos/pools/tester",
		"/algo/raro/sinshares/x":          "", // no encaja patrón
	}
	for in, want := range cases {
		if got := poolMountFromSharePath(in); got != want {
			t.Errorf("poolMountFromSharePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDirIsEmpty(t *testing.T) {
	base := t.TempDir()

	empty := filepath.Join(base, "vacia")
	os.MkdirAll(empty, 0755)
	if ok, err := dirIsEmpty(empty); err != nil || !ok {
		t.Errorf("dir vacío: ok=%v err=%v, want true,nil", ok, err)
	}

	full := filepath.Join(base, "llena")
	os.MkdirAll(full, 0755)
	os.WriteFile(filepath.Join(full, "f.txt"), []byte("x"), 0644)
	if ok, err := dirIsEmpty(full); err != nil || ok {
		t.Errorf("dir con contenido: ok=%v err=%v, want false,nil", ok, err)
	}

	// inexistente → error
	if _, err := dirIsEmpty(filepath.Join(base, "nope")); err == nil {
		t.Error("dir inexistente debería dar error")
	}
}
