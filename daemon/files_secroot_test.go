package main

import (
	"os"
	"path/filepath"
	"testing"
)

// ═══════════════════════════════════════════════════════════════════════
// TESTS · files_secroot.go (acceso a ficheros TOCTOU-safe vía os.Root)
// ═══════════════════════════════════════════════════════════════════════
//
// Estos tests ejercitan las primitivas que TODAS las operaciones del
// módulo Files usan por debajo. Cubren:
//   · normalización de rutas y rechazo de escapes (relWithinShare)
//   · bloqueo de escape por symlink (os.Root)
//   · recursión propia segura (removeAllIn, copyTreeIn, crossRootCopyTree)
//   · rename atómico (renameIn) y su resistencia a symlink en el padre
//   · cálculo de tamaño (dirSizeIn) y recorrido (walkIn)
//
// La garantía dura la da os.Root (openat2/O_NOFOLLOW); estos tests son la
// red que detecta cualquier regresión que reintroduzca un escape.

func secTestShare(t *testing.T) (share string, base string) {
	t.Helper()
	base = t.TempDir()
	share = filepath.Join(base, "share")
	if err := os.MkdirAll(filepath.Join(share, "docs", "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(share, "docs", "a.txt"), []byte("aaa"), 0644)
	os.WriteFile(filepath.Join(share, "docs", "sub", "b.txt"), []byte("bbbbb"), 0644)
	os.WriteFile(filepath.Join(base, "secret.txt"), []byte("TOPSECRET"), 0644)
	return share, base
}

func TestSecRelWithinShare(t *testing.T) {
	cases := []struct {
		in   string
		want string
		err  bool
	}{
		{"", ".", false},
		{"/", ".", false},
		{"/docs/a.txt", "docs/a.txt", false},
		{"docs/a.txt", "docs/a.txt", false},
		{"docs/../a.txt", "a.txt", false},
		{"../secret.txt", "", true},
		{"/../secret.txt", "", true},
		{"docs/../../secret.txt", "", true},
		{"a/b/../../../x", "", true},
		{"./docs/./a.txt", "docs/a.txt", false},
		{"docs\\sub\\b.txt", "docs/sub/b.txt", false},
	}
	for _, c := range cases {
		got, err := relWithinShare(c.in)
		if c.err {
			if err == nil {
				t.Errorf("relWithinShare(%q): esperaba error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("relWithinShare(%q): error inesperado %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("relWithinShare(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSecOpenRootBlocksSymlinkEscape(t *testing.T) {
	share, base := secTestShare(t)
	root, err := openRootAt(share)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	if f, err := root.Open("docs/a.txt"); err != nil {
		t.Errorf("no pude abrir fichero legítimo: %v", err)
	} else {
		f.Close()
	}

	os.Symlink(filepath.Join(base, "secret.txt"), filepath.Join(share, "docs", "escape"))
	if _, err := root.Open("docs/escape"); err == nil {
		t.Error("FALLO CRÍTICO: symlink de escape permitió abrir secreto externo")
	}
}

func TestSecRemoveAllIn(t *testing.T) {
	share, base := secTestShare(t)
	root, _ := openRootAt(share)
	defer root.Close()

	if err := removeAllIn(root, "docs"); err != nil {
		t.Fatalf("removeAllIn falló: %v", err)
	}
	if _, err := os.Stat(filepath.Join(share, "docs")); !os.IsNotExist(err) {
		t.Error("docs debería haber sido borrado")
	}
	if _, err := os.Stat(filepath.Join(base, "secret.txt")); err != nil {
		t.Error("removeAllIn tocó algo fuera del share")
	}
	if err := removeAllIn(root, "noexiste"); err != nil {
		t.Errorf("removeAllIn sobre inexistente debería ser nil, got %v", err)
	}
}

func TestSecRemoveAllInDoesNotFollowSymlink(t *testing.T) {
	share, base := secTestShare(t)
	os.MkdirAll(filepath.Join(base, "external"), 0755)
	os.WriteFile(filepath.Join(base, "external", "keep.txt"), []byte("keep"), 0644)
	os.Symlink(filepath.Join(base, "external"), filepath.Join(share, "docs", "extlink"))

	root, _ := openRootAt(share)
	defer root.Close()

	if err := removeAllIn(root, "docs"); err != nil {
		t.Fatalf("removeAllIn falló: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "external", "keep.txt")); err != nil {
		t.Error("FALLO: removeAllIn siguió un symlink y borró contenido externo")
	}
}

func TestSecMkdirAllIn(t *testing.T) {
	share, _ := secTestShare(t)
	root, _ := openRootAt(share)
	defer root.Close()

	if err := mkdirAllIn(root, "x/y/z", 0755); err != nil {
		t.Fatalf("mkdirAllIn falló: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(share, "x", "y", "z")); err != nil || !fi.IsDir() {
		t.Error("x/y/z no se creó")
	}
	if err := mkdirAllIn(root, "x/y/z", 0755); err != nil {
		t.Errorf("mkdirAllIn repetido debería ser nil, got %v", err)
	}
}

func TestSecCopyTreeIn(t *testing.T) {
	share, _ := secTestShare(t)
	root, _ := openRootAt(share)
	defer root.Close()

	if err := copyTreeIn(root, "docs", "docs_copy"); err != nil {
		t.Fatalf("copyTreeIn falló: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(share, "docs_copy", "sub", "b.txt"))
	if err != nil || string(b) != "bbbbb" {
		t.Errorf("copia recursiva incorrecta: %q err=%v", b, err)
	}
}

func TestSecCopyTreeInSkipsSymlinks(t *testing.T) {
	share, base := secTestShare(t)
	os.Symlink(filepath.Join(base, "secret.txt"), filepath.Join(share, "docs", "leak"))

	root, _ := openRootAt(share)
	defer root.Close()

	if err := copyTreeIn(root, "docs", "docs_copy"); err != nil {
		t.Fatalf("copyTreeIn falló: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(share, "docs_copy", "leak")); !os.IsNotExist(err) {
		t.Error("FALLO: copyTreeIn copió un symlink (vector de fuga)")
	}
}

func TestSecCrossRootCopyTree(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src")
	dst := filepath.Join(base, "dst")
	os.MkdirAll(filepath.Join(src, "d"), 0755)
	os.MkdirAll(dst, 0755)
	os.WriteFile(filepath.Join(src, "d", "f.txt"), []byte("hello"), 0644)

	srcRoot, _ := openRootAt(src)
	defer srcRoot.Close()
	dstRoot, _ := openRootAt(dst)
	defer dstRoot.Close()

	if err := crossRootCopyTree(srcRoot, "d", dstRoot, "d"); err != nil {
		t.Fatalf("crossRootCopyTree falló: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dst, "d", "f.txt"))
	if err != nil || string(b) != "hello" {
		t.Errorf("copia cross-root incorrecta: %q err=%v", b, err)
	}
}

func TestSecDirSizeIn(t *testing.T) {
	share, _ := secTestShare(t)
	root, _ := openRootAt(share)
	defer root.Close()

	sz, err := dirSizeIn(root, "docs")
	if err != nil {
		t.Fatal(err)
	}
	if sz != 8 { // a.txt=3 + sub/b.txt=5
		t.Errorf("dirSizeIn = %d, want 8", sz)
	}
}

func TestSecRenameIn(t *testing.T) {
	share, _ := secTestShare(t)
	root, _ := openRootAt(share)
	defer root.Close()

	if err := renameIn(root, "docs/a.txt", "docs/renamed.txt"); err != nil {
		t.Fatalf("renameIn falló: %v", err)
	}
	if _, err := os.Stat(filepath.Join(share, "docs", "renamed.txt")); err != nil {
		t.Error("renamed.txt no existe tras rename")
	}
	if _, err := os.Stat(filepath.Join(share, "docs", "a.txt")); !os.IsNotExist(err) {
		t.Error("a.txt original debería haber desaparecido")
	}
}

func TestSecRenameInBlocksParentSymlinkEscape(t *testing.T) {
	share, base := secTestShare(t)
	os.MkdirAll(filepath.Join(base, "outside"), 0755)
	os.Symlink(filepath.Join(base, "outside"), filepath.Join(share, "docs", "evil"))

	root, _ := openRootAt(share)
	defer root.Close()

	_ = renameIn(root, "docs/a.txt", "docs/evil/stolen.txt")
	if _, statErr := os.Stat(filepath.Join(base, "outside", "stolen.txt")); statErr == nil {
		t.Error("FALLO CRÍTICO: rename escapó por symlink en directorio padre")
	}
}

func TestSecWalkIn(t *testing.T) {
	share, _ := secTestShare(t)
	root, _ := openRootAt(share)
	defer root.Close()

	entries, err := walkIn(root, "docs")
	if err != nil {
		t.Fatal(err)
	}
	// docs, docs/a.txt, docs/sub, docs/sub/b.txt
	if len(entries) != 4 {
		t.Errorf("walkIn devolvió %d entradas, want 4", len(entries))
	}
	if entries[0].Rel != "docs" || !entries[0].IsDir {
		t.Errorf("primera entrada debería ser el dir raíz docs, got %+v", entries[0])
	}
}
