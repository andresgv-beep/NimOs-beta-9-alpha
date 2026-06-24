package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

func sanitizeFileName(name string) string {
	// Extract only the base filename — strip any directory path components
	name = filepath.Base(name)
	// Reject . and .. explicitly
	if name == "." || name == ".." || name == "" {
		return ""
	}
	// Remove dangerous characters
	re := regexp.MustCompile(`[\/\\:*?"<>|]`)
	name = re.ReplaceAllString(name, "_")
	name = strings.ReplaceAll(name, "..", "")
	// Remove null bytes
	name = strings.ReplaceAll(name, "\x00", "")
	// Trim leading dots (hidden files on Linux)
	// This is optional — uncomment if you want to prevent hidden file creation
	// name = strings.TrimLeft(name, ".")
	if name == "" {
		return ""
	}
	return name
}

// getAvailableBytes returns available bytes for writing to the given path.
// For BTRFS subvolumes with quota, uses btrfs subvolume show (quota limit - usage).
// For ZFS datasets with quota, uses zfs get.
// Falls back to df for other filesystems.
// Returns -1 if space cannot be determined (caller should allow the operation).
func getAvailableBytes(path string) int64 {
	// Try BTRFS quota first
	if out, ok := runSafe("btrfs", "subvolume", "show", path); ok && out != "" {
		var limitBytes, usedBytes int64
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "Limit referenced:") {
				val := strings.TrimSpace(strings.TrimPrefix(line, "Limit referenced:"))
				if val != "-" && val != "none" {
					limitBytes = parseHumanBytesFiles(val)
				}
			}
			if strings.HasPrefix(line, "Usage referenced:") {
				val := strings.TrimSpace(strings.TrimPrefix(line, "Usage referenced:"))
				usedBytes = parseHumanBytesFiles(val)
			}
		}
		if limitBytes > 0 {
			avail := limitBytes - usedBytes
			if avail < 0 {
				avail = 0
			}
			return avail
		}
		// BTRFS subvolume without quota — fall through to df
	}

	// Beta 8.1: rama ZFS eliminada. La función ahora intenta:
	//   1. BTRFS qgroup quota (arriba)
	//   2. df como fallback (abajo) — funciona para cualquier FS montado
	//
	// La rama ZFS antigua ejecutaba `zfs get available <dataset>` para
	// resolver quotas a nivel de subvolume. Ya no aplica.

	// Fallback to df
	out, ok := runSafe("df", "-B1", "--output=avail", path)
	if !ok || strings.TrimSpace(out) == "" {
		out, ok = runSafe("sudo", "df", "-B1", "--output=avail", path)
	}
	if ok {
		// Parse the last non-empty line (skip header)
		lines := strings.Split(strings.TrimSpace(out), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			s := strings.TrimSpace(lines[i])
			if s != "" && s != "Avail" {
				var n int64
				fmt.Sscanf(s, "%d", &n)
				if n > 0 {
					return n
				}
				break
			}
		}
	}

	// Cannot determine available space — return -1 to signal "unknown"
	return -1
}

// parseHumanBytesFiles parses strings like "4.66GiB", "7.20GiB", "500.00MiB" into bytes.
func parseHumanBytesFiles(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" || s == "none" {
		return 0
	}

	multiplier := int64(1)
	if strings.HasSuffix(s, "TiB") {
		multiplier = 1024 * 1024 * 1024 * 1024
		s = strings.TrimSuffix(s, "TiB")
	} else if strings.HasSuffix(s, "GiB") {
		multiplier = 1024 * 1024 * 1024
		s = strings.TrimSuffix(s, "GiB")
	} else if strings.HasSuffix(s, "MiB") {
		multiplier = 1024 * 1024
		s = strings.TrimSuffix(s, "MiB")
	} else if strings.HasSuffix(s, "KiB") {
		multiplier = 1024
		s = strings.TrimSuffix(s, "KiB")
	} else if strings.HasSuffix(s, "B") {
		s = strings.TrimSuffix(s, "B")
	}

	var val float64
	fmt.Sscanf(strings.TrimSpace(s), "%f", &val)
	return int64(val * float64(multiplier))
}

func fmtSizeFiles(b int64) string {
	if b >= 1e9 {
		return fmt.Sprintf("%.1f GB", float64(b)/1e9)
	}
	if b >= 1e6 {
		return fmt.Sprintf("%.0f MB", float64(b)/1e6)
	}
	return fmt.Sprintf("%.0f KB", float64(b)/1e3)
}
