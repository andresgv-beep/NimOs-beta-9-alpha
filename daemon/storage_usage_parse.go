// storage_usage_parse.go — Parser puro de la sección "Overall:" de
// `btrfs filesystem usage -b`.
//
// Semántica de las líneas (fuente del bug AUDIT-3, auditoría 2026-07-03):
//
//	Used:               bytes RAW — cuenta las N copias del profile.
//	                    En RAID1 es ~2× los datos reales del usuario.
//	Free (statfs, df):  bytes USABLES (1 copia) — lo que reporta statfs.
//	                    NO existe en btrfs-progs < 6.3.
//	Data ratio:         copias efectivas del profile de data (2.00 en raid1).
//
// Mezclar Used (raw) con Free (usable) hacía que la UI mostrara el doble de
// uso en RAID1. Este parser expone cada campo con su semántica explícita y
// usableUsedBytes() hace la conversión raw→usable en un único sitio.
package main

import (
	"strconv"
	"strings"
)

type btrfsUsageOverall struct {
	UsedRaw    int64   // "Used:" en bytes raw (todas las copias)
	FreeStatfs int64   // "Free (statfs, df):" en bytes usables; 0 si la línea no existe
	DataRatio  float64 // "Data ratio:"; 0 si no legible
}

func parseBtrfsUsageOverall(out string) btrfsUsageOverall {
	var ov btrfsUsageOverall
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Used:"):
			ov.UsedRaw = parseInt64(strings.TrimSpace(strings.TrimPrefix(line, "Used:")))
		case strings.HasPrefix(line, "Free (statfs, df):"):
			val := strings.TrimSpace(strings.TrimPrefix(line, "Free (statfs, df):"))
			if idx := strings.Index(val, "("); idx > 0 {
				val = strings.TrimSpace(val[:idx])
			}
			ov.FreeStatfs = parseInt64(val)
		case strings.HasPrefix(line, "Data ratio:"):
			val := strings.TrimSpace(strings.TrimPrefix(line, "Data ratio:"))
			if f, err := strconv.ParseFloat(val, 64); err == nil {
				ov.DataRatio = f
			}
		}
	}
	return ov
}

// usableUsedBytes convierte el Used RAW a bytes usables dividiendo por el
// data ratio. Sin ratio legible (o ratio < 1, imposible), devuelve el raw
// tal cual: mejor sobrestimar uso que inventar un ratio.
func (ov btrfsUsageOverall) usableUsedBytes() int64 {
	if ov.DataRatio < 1 {
		return ov.UsedRaw
	}
	return int64(float64(ov.UsedRaw) / ov.DataRatio)
}
