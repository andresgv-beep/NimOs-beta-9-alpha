package main

// ═══════════════════════════════════════════════════════════════════════
// FILE TYPE DISTRIBUTION · share · NimOS Beta 8.1
// ═══════════════════════════════════════════════════════════════════════
//
// Wrapper delgado para la barra "distribución por tipo" de la card. NO
// reimplementa la clasificación ni el escaneo: reutiliza getFileStatsByCategory
// y classifyExt de shares_types.go (las mismas que usa la Files app), para que
// haya UNA sola fuente de verdad de cómo se clasifican los ficheros.
// ═══════════════════════════════════════════════════════════════════════

// FileTypeDistribution es el resultado para el front: bytes por categoría
// más el total agregado.
type FileTypeDistribution struct {
	Categories map[string]int64 `json:"categories"` // categoría -> bytes
	TotalBytes int64            `json:"totalBytes"`
}

// scanShareFileTypes resuelve el path del share y agrega bytes por categoría
// reutilizando el escáner existente (find + classifyExt, timeout interno 3s).
func scanShareFileTypes(shareName string) (*FileTypeDistribution, error) {
	sharePath, err := getManagedSharePath(shareName)
	if err != nil {
		return nil, err
	}
	stats := getFileStatsByCategory(sharePath)

	var total int64
	for _, b := range stats {
		total += b
	}
	return &FileTypeDistribution{Categories: stats, TotalBytes: total}, nil
}
