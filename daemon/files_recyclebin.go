package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ═══════════════════════════════════════════════════════════════════════
// PAPELERA DE RECICLAJE · por share · NimOS Beta 8.1
// ═══════════════════════════════════════════════════════════════════════
//
// Una papelera por carpeta compartida (share), en la raíz: ".papelera/".
// Compartida por todas las subcarpetas del share (no se replica por subdir).
//
// Cuando un share tiene recycleBin activado, borrar un fichero lo MUEVE a
// .papelera/ (rename TOCTOU-safe vía os.Root) en vez de eliminarlo. Cada ítem
// guarda su metadata (ruta original, fecha, tamaño) en .papelera/.index.json
// para poder restaurarlo a su sitio y evitar colisiones de nombre.
//
// La .papelera cuenta para la cuota del share (es lo honesto: un fichero
// recuperable sigue ocupando). El vaciado es manual (sin purga automática v1).
// ═══════════════════════════════════════════════════════════════════════

const (
	recycleBinDir   = ".papelera"
	recycleIndexRel = ".papelera/.index.json"
)

// RecycleItem describe un fichero en la papelera.
type RecycleItem struct {
	ID        string `json:"id"`        // nombre único dentro de .papelera/
	Original  string `json:"original"`  // ruta relativa original dentro del share
	Name      string `json:"name"`      // nombre base original (para mostrar)
	DeletedAt string `json:"deletedAt"` // RFC3339
	SizeBytes int64  `json:"sizeBytes"`
	IsDir     bool   `json:"isDir"`
}

// recycleIndex es el contenido de .papelera/.index.json
type recycleIndex struct {
	Items []RecycleItem `json:"items"`
}

// isRecycleBinEnabled consulta el flag recycleBin del share en la DB.
func isRecycleBinEnabled(shareName string) bool {
	s, err := dbSharesGetRaw(shareName)
	if err != nil || s == nil {
		return false
	}
	return s.RecycleBin
}

// isInsideRecycleBin indica si una ruta relativa está dentro de la papelera
// (para no meter la papelera dentro de sí misma ni listarla como fichero).
func isInsideRecycleBin(rel string) bool {
	return rel == recycleBinDir || strings.HasPrefix(rel, recycleBinDir+"/")
}

// loadRecycleIndex lee el índice de la papelera (vacío si no existe).
func loadRecycleIndex(root *os.Root) (*recycleIndex, error) {
	idx := &recycleIndex{Items: []RecycleItem{}}
	f, err := root.Open(recycleIndexRel)
	if err != nil {
		if os.IsNotExist(err) {
			return idx, nil
		}
		return idx, nil // índice ilegible → tratar como vacío, no romper
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	if err := dec.Decode(idx); err != nil {
		return &recycleIndex{Items: []RecycleItem{}}, nil
	}
	if idx.Items == nil {
		idx.Items = []RecycleItem{}
	}
	return idx, nil
}

// saveRecycleIndex escribe el índice (crea .papelera/ si hace falta).
func saveRecycleIndex(root *os.Root, idx *recycleIndex) error {
	if err := mkdirAllIn(root, recycleBinDir, 0o770); err != nil {
		return err
	}
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	f, err := root.OpenFile(recycleIndexRel, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o660)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	return cerr
}

// uniqueRecycleID genera un nombre único dentro de .papelera/ basado en el
// nombre original + timestamp, evitando colisiones con lo ya presente.
func uniqueRecycleID(root *os.Root, baseName string) string {
	ts := time.Now().UnixNano()
	candidate := fmt.Sprintf("%d_%s", ts, baseName)
	// Comprobar colisión (muy improbable por el ns, pero por robustez).
	for i := 0; ; i++ {
		rel := recycleBinDir + "/" + candidate
		if _, err := root.Lstat(rel); os.IsNotExist(err) {
			return candidate
		}
		candidate = fmt.Sprintf("%d_%d_%s", ts, i, baseName)
		if i > 1000 {
			return candidate // salida de seguridad
		}
	}
}

// moveToRecycleBin mueve rel a la papelera del share y registra metadata.
// Se llama desde filesDelete cuando el share tiene recycleBin activado.
func moveToRecycleBin(root *os.Root, rel string) error {
	// Nunca meter la papelera dentro de sí misma.
	if isInsideRecycleBin(rel) {
		// Si borran algo que YA está en la papelera, es borrado definitivo.
		return removeAllIn(root, rel)
	}

	info, err := root.Lstat(rel)
	if err != nil {
		return err
	}

	// Asegurar .papelera/
	if err := mkdirAllIn(root, recycleBinDir, 0o770); err != nil {
		return err
	}

	baseName := relBase(rel)
	id := uniqueRecycleID(root, baseName)
	dstRel := recycleBinDir + "/" + id

	// Tamaño (para el índice y la UI).
	var size int64
	if info.IsDir() {
		size, _ = dirSizeIn(root, rel)
	} else {
		size = info.Size()
	}

	// Mover (rename TOCTOU-safe dentro del root).
	if err := renameIn(root, rel, dstRel); err != nil {
		return err
	}

	// Registrar en el índice.
	idx, _ := loadRecycleIndex(root)
	idx.Items = append(idx.Items, RecycleItem{
		ID:        id,
		Original:  rel,
		Name:      baseName,
		DeletedAt: time.Now().UTC().Format(time.RFC3339),
		SizeBytes: size,
		IsDir:     info.IsDir(),
	})
	if err := saveRecycleIndex(root, idx); err != nil {
		// El fichero ya se movió; si el índice falla, al menos no se perdió.
		logMsg("moveToRecycleBin: WARNING no se pudo guardar índice: %v", err)
	}
	return nil
}

// listRecycleBin devuelve los ítems de la papelera (más recientes primero).
func listRecycleBin(root *os.Root) []RecycleItem {
	idx, _ := loadRecycleIndex(root)
	items := idx.Items
	sort.Slice(items, func(i, j int) bool {
		return items[i].DeletedAt > items[j].DeletedAt
	})
	return items
}

// restoreFromRecycleBin restaura un ítem a su ruta original. Si la ruta
// original ya existe, restaura con sufijo " (restaurado)" para no pisar.
func restoreFromRecycleBin(root *os.Root, id string) error {
	idx, _ := loadRecycleIndex(root)
	var item *RecycleItem
	pos := -1
	for i := range idx.Items {
		if idx.Items[i].ID == id {
			item = &idx.Items[i]
			pos = i
			break
		}
	}
	if item == nil {
		return fmt.Errorf("item not found in recycle bin")
	}

	srcRel := recycleBinDir + "/" + item.ID
	dstRel := item.Original

	// Asegurar que el directorio padre del destino existe.
	parent := relDir(dstRel)
	if parent != "." {
		if err := mkdirAllIn(root, parent, 0o770); err != nil {
			return err
		}
	}

	// Si el destino ya existe, evitar pisar: añadir sufijo.
	if _, err := root.Lstat(dstRel); err == nil {
		dstRel = restoreConflictName(root, dstRel)
	}

	if err := renameIn(root, srcRel, dstRel); err != nil {
		return err
	}

	// Quitar del índice.
	idx.Items = append(idx.Items[:pos], idx.Items[pos+1:]...)
	saveRecycleIndex(root, idx)
	return nil
}

// restoreConflictName genera un nombre alternativo si el destino ya existe.
func restoreConflictName(root *os.Root, rel string) string {
	dir := relDir(rel)
	base := relBase(rel)
	// Separar nombre y extensión.
	ext := ""
	name := base
	if dot := strings.LastIndex(base, "."); dot > 0 {
		ext = base[dot:]
		name = base[:dot]
	}
	for i := 1; i < 1000; i++ {
		candidate := name + " (restaurado " + strconv.Itoa(i) + ")" + ext
		candRel := candidate
		if dir != "." {
			candRel = dir + "/" + candidate
		}
		if _, err := root.Lstat(candRel); os.IsNotExist(err) {
			return candRel
		}
	}
	return rel // fallback (muy improbable)
}

// deleteFromRecycleBin elimina DEFINITIVAMENTE un ítem de la papelera.
func deleteFromRecycleBin(root *os.Root, id string) error {
	idx, _ := loadRecycleIndex(root)
	pos := -1
	for i := range idx.Items {
		if idx.Items[i].ID == id {
			pos = i
			break
		}
	}
	if pos < 0 {
		return fmt.Errorf("item not found in recycle bin")
	}
	srcRel := recycleBinDir + "/" + idx.Items[pos].ID
	if err := removeAllIn(root, srcRel); err != nil {
		return err
	}
	idx.Items = append(idx.Items[:pos], idx.Items[pos+1:]...)
	saveRecycleIndex(root, idx)
	return nil
}

// emptyRecycleBin vacía la papelera por completo (borrado definitivo).
func emptyRecycleBin(root *os.Root) error {
	idx, _ := loadRecycleIndex(root)
	for _, it := range idx.Items {
		removeAllIn(root, recycleBinDir+"/"+it.ID)
	}
	// Reescribir índice vacío.
	return saveRecycleIndex(root, &recycleIndex{Items: []RecycleItem{}})
}

// recycleBinStats devuelve nº de ítems y bytes totales (para el aviso al
// desactivar la papelera con contenido).
func recycleBinStats(root *os.Root) (count int, bytes int64) {
	idx, _ := loadRecycleIndex(root)
	for _, it := range idx.Items {
		count++
		bytes += it.SizeBytes
	}
	return
}
