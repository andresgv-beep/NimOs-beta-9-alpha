package main

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func pasteTestFile(t *testing.T, rootPath, rel, contents string) (*os.Root, os.FileInfo) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(rootPath, filepath.Dir(rel)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, rel), []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
	root, err := openRootAt(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	info, err := root.Lstat(rel)
	if err != nil {
		root.Close()
		t.Fatal(err)
	}
	return root, info
}

func TestMovePasteEntryEXDEVFallsBackWithoutDataLoss(t *testing.T) {
	base := t.TempDir()
	root, info := pasteTestFile(t, base, "source/file.txt", "contenido importante")
	defer root.Close()
	if err := os.Mkdir(filepath.Join(base, "dest"), 0755); err != nil {
		t.Fatal(err)
	}

	copied, err := movePasteEntry(
		root, "source/file.txt", info, root, "dest/file.txt", base, true,
		func(*os.Root, string, string) error { return syscall.EXDEV },
		removeAllIn,
	)
	if err != nil {
		t.Fatalf("fallback EXDEV falló: %v", err)
	}
	if !copied {
		t.Fatal("se esperaba que EXDEV utilizase copia+borrado")
	}
	got, err := os.ReadFile(filepath.Join(base, "dest", "file.txt"))
	if err != nil || string(got) != "contenido importante" {
		t.Fatalf("destino incompleto: %q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(base, "source", "file.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("el origen debería haberse retirado tras verificar la copia: %v", err)
	}
}

func TestMovePasteEntryDeleteFailurePreservesSourceAndDestination(t *testing.T) {
	base := t.TempDir()
	root, info := pasteTestFile(t, base, "source/file.txt", "dato que no se puede perder")
	defer root.Close()
	if err := os.Mkdir(filepath.Join(base, "dest"), 0755); err != nil {
		t.Fatal(err)
	}

	copied, err := movePasteEntry(
		root, "source/file.txt", info, root, "dest/file.txt", base, true,
		func(*os.Root, string, string) error { return syscall.EXDEV },
		func(*os.Root, string) error { return errors.New("borrado simulado") },
	)
	if err == nil || !copied {
		t.Fatalf("se esperaba resultado parcial seguro; copied=%v err=%v", copied, err)
	}

	for _, rel := range []string{"source/file.txt", "dest/file.txt"} {
		got, readErr := os.ReadFile(filepath.Join(base, rel))
		if readErr != nil || string(got) != "dato que no se puede perder" {
			t.Fatalf("%s no quedó íntegro: %q err=%v", rel, got, readErr)
		}
	}
}

func TestMovePasteEntryRenameFailureDoesNotCreateDestination(t *testing.T) {
	base := t.TempDir()
	root, info := pasteTestFile(t, base, "source/file.txt", "original")
	defer root.Close()

	copied, err := movePasteEntry(
		root, "source/file.txt", info, root, "dest/file.txt", base, true,
		func(*os.Root, string, string) error { return syscall.EPERM },
		removeAllIn,
	)
	if err == nil || copied {
		t.Fatalf("un error distinto de EXDEV no debe copiar; copied=%v err=%v", copied, err)
	}
	got, readErr := os.ReadFile(filepath.Join(base, "source", "file.txt"))
	if readErr != nil || string(got) != "original" {
		t.Fatalf("el origen cambió tras fallar rename: %q err=%v", got, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(base, "dest", "file.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("no debía crearse destino: %v", statErr)
	}
}

func TestMovePasteEntryCrossRootMovesCompleteTree(t *testing.T) {
	srcPath := t.TempDir()
	dstPath := t.TempDir()
	srcRoot, info := pasteTestFile(t, srcPath, "folder/a.txt", "a")
	defer srcRoot.Close()
	if err := os.WriteFile(filepath.Join(srcPath, "folder", "b.txt"), []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := srcRoot.Lstat("folder")
	if err != nil {
		t.Fatal(err)
	}
	dstRoot, err := openRootAt(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	defer dstRoot.Close()

	copied, err := movePasteEntry(
		srcRoot, "folder", info, dstRoot, "folder", dstPath, false,
		renameIn, removeAllIn,
	)
	if err != nil || !copied {
		t.Fatalf("movimiento cross-root falló; copied=%v err=%v", copied, err)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(dstPath, "folder", name)); err != nil {
			t.Fatalf("falta %s en destino: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(srcPath, "folder")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("el árbol origen debería haberse retirado: %v", err)
	}
}

func TestMovePasteEntryRejectsSymlinkWithoutTouchingSource(t *testing.T) {
	base := t.TempDir()
	root, info := pasteTestFile(t, base, "source/file.txt", "original")
	defer root.Close()
	if err := os.Symlink("file.txt", filepath.Join(base, "source", "link.txt")); err != nil {
		t.Fatal(err)
	}
	info, err := root.Lstat("source")
	if err != nil {
		t.Fatal(err)
	}

	copied, err := movePasteEntry(
		root, "source", info, root, "destination", base, true,
		func(*os.Root, string, string) error { return syscall.EXDEV },
		removeAllIn,
	)
	if err == nil || copied {
		t.Fatalf("un árbol con symlink debe rechazarse antes de copiar; copied=%v err=%v", copied, err)
	}
	if got, readErr := os.ReadFile(filepath.Join(base, "source", "file.txt")); readErr != nil || string(got) != "original" {
		t.Fatalf("el archivo original cambió: %q err=%v", got, readErr)
	}
	if target, readErr := os.Readlink(filepath.Join(base, "source", "link.txt")); readErr != nil || target != "file.txt" {
		t.Fatalf("el symlink original cambió: %q err=%v", target, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(base, "destination")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("no debía crearse el destino: %v", statErr)
	}
}
