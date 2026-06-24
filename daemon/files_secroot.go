package main

import (
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ═══════════════════════════════════════════════════════════════════════
// SECURE FILE ACCESS · os.Root (TOCTOU-safe) · NimOS Beta 8.1
// ═══════════════════════════════════════════════════════════════════════
//
// Modelo: cada operación de filesystem sobre un share se ejecuta a través
// de un *os.Root anclado al sharePath. El kernel (openat2 / O_NOFOLLOW por
// debajo) garantiza que NINGÚN componente de la ruta — ni siquiera uno
// sustituido por un symlink en una condición de carrera — pueda escapar
// del directorio raíz del share. Esto cierra de raíz el TOCTOU que tenía
// el patrón anterior (validar ruta, luego operar sobre ella).
//
// os.Root en Go 1.24 expone: Open, OpenFile, Create, Mkdir, Remove, Stat,
// Lstat. NO expone RemoveAll, MkdirAll ni Rename. Por eso las operaciones
// recursivas (borrado de árbol, copia de árbol, mkdir anidado) se
// implementan a mano sobre el root.
// ═══════════════════════════════════════════════════════════════════════

// errEscape se devuelve cuando una ruta intenta salir del share.
var errEscape = errors.New("invalid path: access denied")

// relWithinShare normaliza una ruta de usuario a una ruta RELATIVA y limpia,
// apta para pasar a los métodos de *os.Root.
//
// Reglas:
//   - "" o "/" → "." (la raíz del share)
//   - se limpia con path.Clean usando '/' como separador (las rutas de la
//     API siempre vienen con '/')
//   - cualquier ruta que tras limpiar empiece por ".." → error (escape)
//   - se eliminan separadores iniciales para que sea relativa
//
// NOTA: esto es defensa de primer nivel + ergonomía. La garantía dura la
// da os.Root. Pero rechazar ".." aquí da errores claros (400) en vez de
// errores opacos del kernel.
func relWithinShare(subPath string) (string, error) {
	p := strings.ReplaceAll(subPath, "\\", "/")

	// Tratar SIEMPRE como relativa: quitar barras iniciales ANTES de limpiar.
	// Si no, path.Clean("/../x") = "/x" (POSIX descarta ".." en la raíz) y
	// perderíamos la señal de escape. Como relativa, Clean("../x") = "../x".
	p = strings.TrimLeft(p, "/")

	// Limpiar como ruta RELATIVA. Si se sale por arriba, Clean deja ".."
	// o "../..." y lo detectamos abajo (400 claro en vez de error opaco).
	p = path.Clean(p)

	switch {
	case p == "." || p == "":
		return ".", nil
	case p == ".." || strings.HasPrefix(p, "../"):
		return "", errEscape
	}
	return p, nil
}

// openRootAt abre un *os.Root anclado a sharePath. El caller DEBE cerrarlo.
func openRootAt(sharePath string) (*os.Root, error) {
	return os.OpenRoot(sharePath)
}

// ─── Recursión propia sobre os.Root ─────────────────────────────────────

// mkdirAllIn crea rel y todos sus padres dentro del root (equivalente a
// os.MkdirAll pero TOCTOU-safe). Idempotente.
func mkdirAllIn(root *os.Root, rel string, perm os.FileMode) error {
	if rel == "." || rel == "" {
		return nil
	}
	// Construye incrementalmente: a, a/b, a/b/c
	parts := strings.Split(rel, "/")
	cur := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		if cur == "" {
			cur = part
		} else {
			cur = cur + "/" + part
		}
		err := root.Mkdir(cur, perm)
		if err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	return nil
}

// removeAllIn borra rel recursivamente dentro del root (equivalente a
// os.RemoveAll pero TOCTOU-safe: cada nivel se resuelve vía root).
func removeAllIn(root *os.Root, rel string) error {
	info, err := root.Lstat(rel)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	// Si es un symlink, lo borramos sin seguirlo (Lstat no lo siguió).
	if info.Mode()&os.ModeSymlink != 0 {
		return root.Remove(rel)
	}

	if info.IsDir() {
		// Leer hijos vía un fd del propio directorio (resuelto por el root).
		d, err := root.Open(rel)
		if err != nil {
			return err
		}
		names, readErr := d.Readdirnames(-1)
		d.Close()
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		for _, name := range names {
			child := rel + "/" + name
			if rel == "." {
				child = name
			}
			if err := removeAllIn(root, child); err != nil {
				return err
			}
		}
	}
	return root.Remove(rel)
}

// copyFileIn copia un fichero regular srcRel→dstRel, ambos dentro del root.
func copyFileIn(root *os.Root, srcRel, dstRel string, perm os.FileMode) error {
	in, err := root.Open(srcRel)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := root.OpenFile(dstRel, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		root.Remove(dstRel)
		return copyErr
	}
	return closeErr
}

// copyTreeIn copia srcRel→dstRel recursivamente, todo dentro del root.
// No sigue symlinks (los recrea como symlink sólo si apuntan dentro; por
// simplicidad y seguridad, aquí los OMITIMOS — un share no debería depender
// de symlinks internos y copiarlos es vector de fuga).
func copyTreeIn(root *os.Root, srcRel, dstRel string) error {
	info, err := root.Lstat(srcRel)
	if err != nil {
		return err
	}

	if info.Mode()&os.ModeSymlink != 0 {
		// Política: no copiamos symlinks (seguridad). Se ignoran.
		return nil
	}

	if info.IsDir() {
		if err := root.Mkdir(dstRel, info.Mode().Perm()); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		d, err := root.Open(srcRel)
		if err != nil {
			return err
		}
		names, readErr := d.Readdirnames(-1)
		d.Close()
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		for _, name := range names {
			if err := copyTreeIn(root, srcRel+"/"+name, dstRel+"/"+name); err != nil {
				return err
			}
		}
		return nil
	}

	return copyFileIn(root, srcRel, dstRel, info.Mode().Perm())
}

// crossRootCopyTree copia un árbol de srcRoot→dstRoot (shares distintos).
// Usado por paste entre shares diferentes.
func crossRootCopyTree(srcRoot *os.Root, srcRel string, dstRoot *os.Root, dstRel string) error {
	info, err := srcRoot.Lstat(srcRel)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil // no copiamos symlinks
	}
	if info.IsDir() {
		if err := dstRoot.Mkdir(dstRel, info.Mode().Perm()); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		d, err := srcRoot.Open(srcRel)
		if err != nil {
			return err
		}
		names, readErr := d.Readdirnames(-1)
		d.Close()
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		for _, name := range names {
			if err := crossRootCopyTree(srcRoot, srcRel+"/"+name, dstRoot, dstRel+"/"+name); err != nil {
				return err
			}
		}
		return nil
	}
	// fichero
	in, err := srcRoot.Open(srcRel)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := dstRoot.OpenFile(dstRel, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		dstRoot.Remove(dstRel)
		return copyErr
	}
	return closeErr
}

// dirSizeIn calcula el tamaño total de rel dentro del root (para checks de
// quota antes de copiar). Equivalente seguro a `du -sb`.
func dirSizeIn(root *os.Root, rel string) (int64, error) {
	info, err := root.Lstat(rel)
	if err != nil {
		return 0, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return 0, nil
	}
	if !info.IsDir() {
		return info.Size(), nil
	}
	var total int64
	d, err := root.Open(rel)
	if err != nil {
		return 0, err
	}
	names, readErr := d.Readdirnames(-1)
	d.Close()
	if readErr != nil && readErr != io.EOF {
		return 0, readErr
	}
	for _, name := range names {
		sz, err := dirSizeIn(root, rel+"/"+name)
		if err != nil {
			return 0, err
		}
		total += sz
	}
	return total, nil
}

// joinRel une un directorio rel + un nombre, devolviendo rel limpio.
func joinRel(dirRel, name string) string {
	if dirRel == "." || dirRel == "" {
		return name
	}
	return dirRel + "/" + name
}

// baseName / dirName helpers operando en el espacio relativo del share.
func relBase(rel string) string { return filepath.Base(rel) }
func relDir(rel string) string {
	d := filepath.Dir(rel)
	if d == "/" {
		return "."
	}
	return d
}

// renameIn renombra/mueve oldRel→newRel DENTRO del mismo root, atómico y
// TOCTOU-safe. os.Root no expone su fd, así que abrimos los directorios
// PADRE de origen y destino vía root (resolución segura) y hacemos
// renameat() usando sus fds como base con los nombres hoja. El renameat
// no sigue symlinks en los componentes base y es atómico (mismo inode si
// es el mismo directorio de pool).
func renameIn(root *os.Root, oldRel, newRel string) error {
	oldDir, oldName := splitParent(oldRel)
	newDir, newName := splitParent(newRel)

	oldDirFile, err := openDirForRename(root, oldDir)
	if err != nil {
		return err
	}
	defer oldDirFile.Close()

	newDirFile, err := openDirForRename(root, newDir)
	if err != nil {
		return err
	}
	defer newDirFile.Close()

	return unixRenameat(int(oldDirFile.Fd()), oldName, int(newDirFile.Fd()), newName)
}

// openDirForRename abre un directorio (dirRel; "." = raíz del share) vía root.
func openDirForRename(root *os.Root, dirRel string) (*os.File, error) {
	if dirRel == "" {
		dirRel = "."
	}
	return root.Open(dirRel)
}

// splitParent separa rel en (dirPadre, nombreHoja). Para "a/b/c" → ("a/b","c").
// Para "c" → (".","c").
func splitParent(rel string) (string, string) {
	i := strings.LastIndex(rel, "/")
	if i < 0 {
		return ".", rel
	}
	return rel[:i], rel[i+1:]
}

// walkEntry es una entrada visitada por walkIn.
type walkEntry struct {
	Rel   string      // ruta relativa al root
	Info  os.FileInfo // de Lstat (no sigue symlinks)
	IsDir bool
}

// walkIn recorre rel recursivamente dentro del root, en orden estable
// (lexicográfico), devolviendo entradas. NO sigue symlinks (los reporta
// como entrada pero el caller decide; para zip los omitimos). Seguro:
// cada nivel se resuelve vía root.
func walkIn(root *os.Root, rel string) ([]walkEntry, error) {
	var out []walkEntry
	info, err := root.Lstat(rel)
	if err != nil {
		return nil, err
	}
	out = append(out, walkEntry{Rel: rel, Info: info, IsDir: info.IsDir()})

	if info.Mode()&os.ModeSymlink != 0 {
		return out, nil // no descender en symlinks
	}
	if !info.IsDir() {
		return out, nil
	}

	d, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	names, readErr := d.Readdirnames(-1)
	d.Close()
	if readErr != nil && readErr != io.EOF {
		return nil, readErr
	}
	sortStrings(names)
	for _, name := range names {
		child := joinRel(rel, name)
		sub, err := walkIn(root, child)
		if err != nil {
			return nil, err
		}
		out = append(out, sub...)
	}
	return out, nil
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
