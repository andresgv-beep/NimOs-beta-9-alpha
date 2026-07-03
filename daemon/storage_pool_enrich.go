// storage_pool_enrich.go — Enriquecimiento de Pool con datos runtime.
//
// Los campos Usage, Health, IsPrimary y Mounted NO se almacenan en SQLite:
// son derivados del estado físico del sistema en runtime.
//
// Esta capa los calcula y los adjunta al struct Pool para que la API HTTP
// (storage_http_v2.go) devuelva pools "completos" a la UI sin que el frontend
// tenga que pegar varios endpoints.
//
// Diseño:
//   · enrichPool() toma un Pool ya cargado del repo y le añade campos runtime
//   · Reusa helpers existentes (runSafe, buildPoolHealth, btrfsVdevTypeForProfile)
//   · No reinventa la lógica que ya estaba en getBtrfsPoolInfo
//
// Principios aplicados:
//   · Principio 1 — SQLite=entidades, JSON=payload. Estos campos son derivados.
//   · Principio 2 — runSafe con timeout donde aplica
//   · Principio 13 — Health rico, no booleano

package main

import (
	"context"
	"strings"
)

// enrichPool añade los campos derivados (Usage, Health, IsPrimary, Mounted)
// a un Pool ya hidratado con Devices.
//
// primaryPool es el nombre del pool primario actual (leído de
// storage_metadata.primary_pool en SQLite vía getPrimaryPoolName).
func enrichPool(p *Pool, primaryPool string) {
	if p == nil {
		return
	}

	p.IsPrimary = p.Name == primaryPool

	// Detect mount status
	p.Mounted = isPoolMounted(p.MountPoint)
	if p.Mounted {
		// ── Regla 16 · External Systems Own Their Facts ──────────────────
		// Reconciliar PRIMERO: BTRFS es la autoridad del profile/compresión.
		// Si la BD diverge, servimos el valor real (+ self-heal). Hacerlo
		// antes de calcular capacidad asegura que usamos el profile REAL
		// (p.ej. raid1 recién convertido por CLI), no el cacheado.
		reconcilePoolProfileWithReality(p)

		p.Usage = computePoolUsage(p.MountPoint)

		// Capacidad TOTAL estable: la calculamos de la geometría de discos
		// (profile + tamaños), NO del `free` de btrfs, que en RAID1 asimétrico
		// es engañoso y "baila" según el llenado. Así el TOTAL mostrado es la
		// capacidad usable real y no cambia al escribir datos.
		if p.Usage != nil && len(p.Devices) > 0 {
			sizes := make([]int64, 0, len(p.Devices))
			for _, d := range p.Devices {
				if d.SizeBytes > 0 {
					sizes = append(sizes, d.SizeBytes)
				}
			}
			if usable := computeUsableCapacity(p.Profile, sizes); usable > 0 {
				p.Usage.TotalBytes = usable
				// Available coherente con el total estable (nunca negativo).
				avail := usable - p.Usage.UsedBytes
				if avail < 0 {
					avail = 0
				}
				p.Usage.AvailableBytes = avail
				p.Usage.UsagePercent = int(float64(p.Usage.UsedBytes) / float64(usable) * 100)
			}
		}
	}

	// Compute health using the existing diagnostic engine + enrich each
	// device with SmartStatus (runtime, from smartctl cache).
	configDisks := make([]string, 0, len(p.Devices))
	for i, d := range p.Devices {
		// ── Fuente ÚNICA de verdad del estado (resolveDeviceState) ──────────
		// En vez de calcular presencia + SMART aquí (y arriesgar divergir con
		// otras vistas), delegamos en resolveDeviceState: la realidad (path +
		// serial) manda sobre el SMART cacheado. Un disco ausente sale missing,
		// uno cambiado sale swapped — nunca "ok" falso.
		name := d.CurrentPath
		if strings.HasPrefix(name, "/dev/") {
			name = strings.TrimPrefix(name, "/dev/")
		}

		var smartStatus string
		if name != "" {
			smartStatus, _ = getSmartDetailsForDisk(name)
			configDisks = append(configDisks, name)
		}

		state := resolveDeviceState(&p.Devices[i], smartStatus)
		p.Devices[i].SmartStatus = deviceStateToSmartStatus(state, smartStatus)
	}

	vdevType := btrfsVdevTypeForProfile(string(p.Profile))
	health := buildPoolHealth(DiagnosticInput{
		PoolType:    "btrfs",
		VdevType:    vdevType,
		ConfigDisks: configDisks,
		MountPoint:  p.MountPoint,
	})
	p.Health = &health
}

// btrfsVdevTypeForProfile mapea el Profile BTRFS al vdevType que entiende
// buildPoolHealth (alineado con la convención de storage_health.go).
func btrfsVdevTypeForProfile(profile string) string {
	switch profile {
	case "raid1", "raid1c3":
		return "mirror"
	case "raid10":
		return "raid10"
	case "single":
		return "single"
	}
	return "single"
}

// isPoolMounted comprueba si mountPoint tiene un filesystem montado
// distinto del root (cubre el caso edge de mountPoint=/ que sería un bug).
var isPoolMounted = func(mountPoint string) bool {
	if mountPoint == "" || mountPoint == "/" {
		return false
	}
	mountSrc, _ := runSafe("findmnt", "-n", "-o", "SOURCE", mountPoint)
	if strings.TrimSpace(mountSrc) == "" {
		return false
	}
	rootSrc, _ := runSafe("findmnt", "-n", "-o", "SOURCE", "/")
	return strings.TrimSpace(mountSrc) != strings.TrimSpace(rootSrc)
}

// computePoolUsage calcula la capacidad usable real de un pool BTRFS
// usando `btrfs filesystem usage -b` (correcto para RAID asimétrico) con
// fallback a `df -B1` si btrfs no responde.
//
// IMPORTANTE: el cálculo en getBtrfsPoolInfo usa el mismo método.
// Aquí mantenemos la fórmula correcta: total = used + available (capacidad
// usable real, NO el tamaño bruto de los discos).
//
// Bug fix histórico (2026-05): el cálculo ingenuo "Free (estimated)" sobrestima
// en RAID1 con discos asimétricos. "Free (statfs, df)" da el valor real.
// computeUsableCapacity calcula la capacidad USABLE real de un pool según su
// profile y los tamaños de sus discos. Es estable: no depende del estado de
// llenado (a diferencia del `free` de btrfs, que en RAID asimétrico "baila").
//
// BTRFS no replica como el RAID clásico (que se limita al disco menor y
// desperdicia el resto del mayor). Reparte copias mientras pueda colocarlas en
// discos distintos. La capacidad usable resultante por profile:
//
//	single  : suma de todos (sin redundancia)
//	raid1   : 2 copias, cada una en un disco distinto
//	raid1c3 : 3 copias, cada una en un disco distinto
//	raid10  : stripe sobre mirrors → 2 copias (requiere ≥4 discos)
//
// Para los perfiles con copias se usa la condición general (water-filling):
// U bytes lógicos caben ⟺ sum(min(s_i, U)) ≥ copias·U. Ejemplos:
//   - raid1 120+320   → U=120 (el mayor no se empareja consigo mismo)
//   - raid1c3 1000+100+100 → U=100 (3 copias en 3 discos ⇒ limita el menor)
func computeUsableCapacity(profile Profile, sizes []int64) int64 {
	if len(sizes) == 0 {
		return 0
	}
	var sum int64
	for _, s := range sizes {
		sum += s
	}

	switch profile {
	case ProfileSingle:
		return sum

	case ProfileRaid1, ProfileRaid1c3, ProfileRaid10:
		// AUDIT-2: la fórmula anterior min(suma/copias, suma−mayor) restaba
		// el disco mayor UNA sola vez — correcta para raid1 de 2-4 discos,
		// pero sobrestimaba en raid1c3 asimétrico (con [1000,100,100]
		// devolvía 200 GB cuando lo usable real es 100: cada byte necesita
		// 3 copias en 3 discos DISTINTOS, así que limita el menor).
		//
		// Fórmula general (water-filling): U bytes lógicos son colocables
		// ⟺ sum(min(s_i, U)) ≥ copias·U — cada disco aporta como máximo
		// min(s_i, U) porque no puede guardar dos copias del mismo byte.
		// f(U) = sum(min(s_i,U)) − copias·U es cóncava con f(0)=0, así que
		// la región factible es [0, U*]: búsqueda binaria del borde.
		copies := int64(profileCopies(profile))
		if int64(len(sizes)) < copies {
			return 0 // imposible colocar copias en discos distintos
		}
		lo, hi := int64(0), sum/copies
		for lo < hi {
			mid := lo + (hi-lo+1)/2
			var capBytes int64
			for _, s := range sizes {
				if s < mid {
					capBytes += s
				} else {
					capBytes += mid
				}
			}
			if capBytes >= copies*mid {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		return lo
	}
	return sum
}

// profileCopies devuelve el nº de copias que mantiene cada profile.
func profileCopies(p Profile) int {
	switch p {
	case ProfileRaid1, ProfileRaid10:
		return 2
	case ProfileRaid1c3:
		return 3
	}
	return 1
}

func computePoolUsage(mountPoint string) *PoolUsage {
	if mountPoint == "" {
		return nil
	}

	var used, available int64

	if bfsOut, ok := runSafe("btrfs", "filesystem", "usage", "-b", mountPoint); ok {
		ov := parseBtrfsUsageOverall(bfsOut)
		// AUDIT-3: "Used:" son bytes RAW (todas las copias del profile);
		// servirlo tal cual doblaba el uso mostrado en RAID1.
		used = ov.usableUsedBytes()
		available = ov.FreeStatfs
	}

	// Fallback a df si btrfs no respondió o dejó campos incompletos.
	// AUDIT: btrfs-progs < 6.3 no imprime "Free (statfs, df)" — antes eso
	// dejaba available=0 sin activar el fallback → UI al 100% en falso.
	if used == 0 || available == 0 {
		if dfOut, ok := runSafe("df", "-B1", "--output=size,used,avail", mountPoint); ok {
			lines := strings.Split(strings.TrimSpace(dfOut), "\n")
			if len(lines) >= 2 {
				parts := strings.Fields(lines[1])
				if len(parts) >= 3 {
					if used == 0 {
						used = parseInt64(parts[1])
					}
					if available == 0 {
						available = parseInt64(parts[2])
					}
				}
			}
		}
	}

	if used == 0 && available == 0 {
		return nil
	}

	total := used + available
	usagePct := 0
	if total > 0 {
		usagePct = int(float64(used) / float64(total) * 100)
	}

	return &PoolUsage{
		TotalBytes:     total,
		UsedBytes:      used,
		AvailableBytes: available,
		UsagePercent:   usagePct,
	}
}

// getPrimaryPoolName devuelve el nombre del pool primario configurado.
// Beta 8.1: lee storage_metadata directamente (sin pasar por el adapter
// de Beta 7, que fue eliminado). El valor se almacena como UUID y se
// resuelve a nombre vía repo.GetPool.
func getPrimaryPoolName() string {
	if storageService == nil {
		return ""
	}
	ctx := context.Background()
	var primaryID string
	err := storageService.repo.db.QueryRowContext(ctx,
		`SELECT value FROM storage_metadata WHERE key = 'primary_pool'`).Scan(&primaryID)
	if err != nil || primaryID == "" {
		return ""
	}
	pool, err := storageService.repo.GetPool(ctx, primaryID)
	if err != nil || pool == nil {
		return ""
	}
	return pool.Name
}

// deviceIsPresent comprueba si un disco registrado en la BD sigue presente
// físicamente. Inyectable para tests. Regla 16: el kernel manda sobre la BD.
//
//  1. by-id estable existe → presente (lo más fiable).
//  2. CurrentPath existe Y serial coincide → presente.
//  3. en otro caso → ausente (missing).
//
// El serial es la identidad absoluta: /dev/sdb puede existir pero ser OTRO
// disco (uno nuevo), así que se confirma el serial, no solo el path.
var deviceIsPresent = func(d *Device) bool {
	if d.ByIDPath != "" && devicePathExists(d.ByIDPath) {
		return true
	}
	if d.CurrentPath != "" && devicePathExists(d.CurrentPath) {
		if d.Serial == "" {
			return true
		}
		actualSerial := readDeviceSerial(d.CurrentPath)
		if actualSerial == "" || actualSerial == d.Serial {
			return true
		}
		return false // path existe pero es otro disco (serial distinto)
	}
	return false
}

// readDeviceSerial lee el serial del disco en un path (vía lsblk). "" si no se
// puede determinar. Inyectable para tests.
var readDeviceSerial = func(path string) string {
	out, ok := runSafe("lsblk", "-ndo", "SERIAL", path)
	if !ok {
		return ""
	}
	return strings.TrimSpace(out)
}
