package main

// ═══════════════════════════════════════════════════════════════════════
// SHARES TYPES + HELPERS · NimOS Beta 8.1
// ═══════════════════════════════════════════════════════════════════════
//
// Esta capa contiene:
//   · ShareView      (modelo enriquecido para frontend)
//   · ShareUpdate    (input parcial para actualizar shares)
//   · CategoryStats  (estadísticas de archivos por categoría)
//   · parseHumanBytes (helper para parsear "1.5GiB" → bytes)
//   · getFileStatsByCategory (escanea dir y agrupa bytes por tipo)
//
// Sin dependencias de SQLite ni HTTP. Funciones puras (excepto
// getFileStatsByCategory que ejecuta `find` para escanear).
//
// La capa de tipos NO conoce a service ni a http. Es la base.
// ═══════════════════════════════════════════════════════════════════════

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// NOTA: ShareView vive en models.go (con DBShare embebido y ToMap).
// Este archivo solo contiene helpers/enums que no eran parte del modelo
// pero sí del módulo Shares.

// ═══════════════════════════════════════════════════════════════════════
// CATEGORIZACIÓN DE ARCHIVOS · listas de extensiones por tipo
// ═══════════════════════════════════════════════════════════════════════
//
// Estas listas son la fuente de verdad de qué extensión cae en qué
// categoría. Centralizadas aquí para reutilización (Files app las usa).

var videoExts = map[string]bool{
	".mp4": true, ".mkv": true, ".avi": true, ".mov": true,
	".webm": true, ".flv": true, ".wmv": true, ".m4v": true,
	".mpg": true, ".mpeg": true,
}

var imageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".webp": true, ".bmp": true, ".svg": true, ".tiff": true,
	".heic": true,
}

var musicExts = map[string]bool{
	".mp3": true, ".flac": true, ".wav": true, ".ogg": true,
	".m4a": true, ".aac": true, ".wma": true, ".opus": true,
}

var documentExts = map[string]bool{
	".pdf": true, ".doc": true, ".docx": true, ".odt": true,
	".txt": true, ".rtf": true, ".xls": true, ".xlsx": true,
	".ppt": true, ".pptx": true, ".csv": true, ".md": true,
}

var archiveExts = map[string]bool{
	".zip": true, ".rar": true, ".7z": true, ".tar": true,
	".gz": true, ".bz2": true, ".xz": true, ".iso": true,
}

var codeExts = map[string]bool{
	".go": true, ".py": true, ".js": true, ".ts": true,
	".rs": true, ".c": true, ".cpp": true, ".h": true,
	".html": true, ".css": true, ".json": true, ".xml": true,
	".sh": true, ".rb": true, ".java": true, ".svelte": true,
}

// classifyExt devuelve la categoría de una extensión.
// "" se trata como "other".
func classifyExt(ext string) string {
	ext = strings.ToLower(ext)
	switch {
	case videoExts[ext]:
		return "video"
	case imageExts[ext]:
		return "image"
	case musicExts[ext]:
		return "music"
	case documentExts[ext]:
		return "document"
	case archiveExts[ext]:
		return "archive"
	case codeExts[ext]:
		return "code"
	default:
		return "other"
	}
}

// emptyCategoryStats devuelve un map vacío con todas las categorías.
// Útil para mantener el shape estable aunque el dir esté vacío.
func emptyCategoryStats() map[string]int64 {
	return map[string]int64{
		"video":    0,
		"image":    0,
		"document": 0,
		"music":    0,
		"archive":  0,
		"code":     0,
		"other":    0,
	}
}

// ═══════════════════════════════════════════════════════════════════════
// PARSE HELPERS · convertir output de btrfs/df a bytes
// ═══════════════════════════════════════════════════════════════════════

// parseHumanBytes convierte una string tipo "1.5GiB", "500MB", "2.3 TiB"
// a int64 bytes. Tolera comas como separador decimal (locale-aware).
//
// Casos especiales:
//
//	""        → 0
//	"-"       → 0
//	"none"    → 0
//	"1024"    → 1024 (sin unidad = bytes raw)
//
// Es el helper unificado del módulo shares (y files lo importa).
// Sustituye al duplicado parseHumanBytesFiles (en files_helpers.go tras la
// modularización de Beta 8.2; Beta 8.1 cleanup).
func parseHumanBytes(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" || s == "none" {
		return 0
	}

	var numStr strings.Builder
	var unitStr strings.Builder
	parsingNum := true
	for _, c := range s {
		if parsingNum && (c >= '0' && c <= '9' || c == '.' || c == ',') {
			numStr.WriteRune(c)
		} else {
			parsingNum = false
			unitStr.WriteRune(c)
		}
	}

	num := 0.0
	fmt.Sscanf(strings.ReplaceAll(numStr.String(), ",", "."), "%f", &num)

	multiplier := int64(1)
	unit := strings.ToUpper(strings.TrimSpace(unitStr.String()))
	switch unit {
	case "K", "KB", "KIB":
		multiplier = 1024
	case "M", "MB", "MIB":
		multiplier = 1024 * 1024
	case "G", "GB", "GIB":
		multiplier = 1024 * 1024 * 1024
	case "T", "TB", "TIB":
		multiplier = 1024 * 1024 * 1024 * 1024
	}

	return int64(num * float64(multiplier))
}

// ═══════════════════════════════════════════════════════════════════════
// FILE STATS · escanea un dir y agrupa bytes por categoría
// ═══════════════════════════════════════════════════════════════════════

// getFileStatsByCategory escanea recursivamente un directorio y devuelve
// el total de bytes agrupados por categoría (video, image, document,
// music, archive, code, other).
//
// Implementación:
//
//	· Ejecuta `find <path> -type f -printf "%s %p\n"`
//	· Para cada línea: parsea size + ruta
//	· Clasifica por extensión vía classifyExt
//
// Timeout interno: 3 segundos. Si el dir tiene millones de archivos,
// devuelve lo que pudo escanear en ese tiempo (no error).
func getFileStatsByCategory(dirPath string) map[string]int64 {
	stats := emptyCategoryStats()

	opts := CmdOptions{Timeout: 3 * time.Second}
	res, err := runCmd("find", []string{dirPath, "-type", "f", "-printf", "%s %p\n"}, opts)
	if err != nil || res.Stdout == "" {
		return stats
	}

	for _, line := range strings.Split(res.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		var size int64
		fmt.Sscanf(parts[0], "%d", &size)
		ext := filepath.Ext(parts[1])
		stats[classifyExt(ext)] += size
	}

	return stats
}
