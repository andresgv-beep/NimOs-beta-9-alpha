// storage_executor_real.go — Implementación real de BtrfsExecutor.
//
// Ejecuta comandos `btrfs`, `mkfs.btrfs`, `mount`, `umount`, `wipefs`
// directamente. NO escribe a SQLite (eso es responsabilidad del Service).
// NO toca el filesystem JSON viejo (eso muere en Fase 5).
//
// La lógica BTRFS que tiene Beta 7 en storage_btrfs_pool.go etc. se va
// a REIMPLEMENTAR aquí — con menos dependencias y más limpio — porque
// las funciones viejas mezclan BTRFS con JSON, mount entries, etc.
//
// Para los tests unitarios se usa MockBtrfsExecutor. Para tests de
// integración (Bloque 5) y producción real, se usa esta implementación.

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// RealBtrfsExecutor implementa BtrfsExecutor invocando comandos reales.
type RealBtrfsExecutor struct {
	// Timeout para comandos largos (mkfs, balance). Default 30 min.
	CmdTimeout time.Duration
}

// NewRealBtrfsExecutor crea el executor con valores razonables.
func NewRealBtrfsExecutor() *RealBtrfsExecutor {
	return &RealBtrfsExecutor{
		CmdTimeout: 30 * time.Minute,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helper común: ejecutar comando con timeout y log
// ─────────────────────────────────────────────────────────────────────────────

// runCommand ejecuta un comando y devuelve stdout o error. Logea el
// comando antes de ejecutar y el resultado después.
func (e *RealBtrfsExecutor) runCommand(ctx context.Context, name string, args ...string) (string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, e.CmdTimeout)
	defer cancel()

	logMsg("BtrfsExecutor: %s %s", name, strings.Join(args, " "))

	cmd := exec.CommandContext(cmdCtx, name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%s %s: %v: %s",
			name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

// runCommandNoTimeout es como runCommand pero SIN el CmdTimeout de 30 min.
// Para operaciones intrínsecamente largas (balance/convert) cuyo tiempo
// depende del tamaño del array y puede ser de horas. Sigue respetando el
// ctx del caller (si el daemon se apaga, el ctx se cancela y el comando
// recibe la señal), pero no impone un límite artificial que mataría la
// operación a la mitad. STOR-03.
func (e *RealBtrfsExecutor) runCommandNoTimeout(ctx context.Context, name string, args ...string) (string, error) {
	logMsg("BtrfsExecutor (long-op): %s %s", name, strings.Join(args, " "))

	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%s %s: %v: %s",
			name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// CreateFilesystem
// ─────────────────────────────────────────────────────────────────────────────

func (e *RealBtrfsExecutor) CreateFilesystem(ctx context.Context, req CreateFilesystemRequest) (*FilesystemInfo, error) {
	if len(req.ByIDPaths) == 0 {
		return nil, fmt.Errorf("CreateFilesystem: no devices provided")
	}
	if !req.Profile.IsValid() {
		return nil, fmt.Errorf("CreateFilesystem: invalid profile %q", req.Profile)
	}
	if len(req.ByIDPaths) < req.Profile.MinDisks() {
		return nil, fmt.Errorf("CreateFilesystem: profile %s requires at least %d disks, got %d",
			req.Profile, req.Profile.MinDisks(), len(req.ByIDPaths))
	}

	// Wipe defensivo si se pide
	if req.WipeFirst {
		for _, p := range req.ByIDPaths {
			if err := e.WipeDevice(ctx, p); err != nil {
				return nil, fmt.Errorf("CreateFilesystem: wipe %s: %w", p, err)
			}
		}
	}

	// Construir args de mkfs.btrfs
	// HARD-5 fix: -f (force) solo si WipeFirst=true. Sin -f, mkfs falla
	// limpio si encuentra un filesystem existente, lo cual actúa como
	// last line of defense del kernel contra races entre preflight check
	// y el mkfs real.
	args := []string{"-L", req.Label}
	if req.WipeFirst {
		args = append([]string{"-f"}, args...)
	}
	switch req.Profile {
	case ProfileSingle:
		// single: si hay >1 disco, metadata raid1 igual (BTRFS default sano)
		if len(req.ByIDPaths) > 1 {
			args = append(args, "-d", "single", "-m", "raid1")
		}
	case ProfileRaid1:
		args = append(args, "-d", "raid1", "-m", "raid1")
	case ProfileRaid1c3:
		args = append(args, "-d", "raid1c3", "-m", "raid1c3")
	case ProfileRaid10:
		args = append(args, "-d", "raid10", "-m", "raid10")
	}
	args = append(args, req.ByIDPaths...)

	// btrfs device scan --forget para limpiar referencias previas del kernel
	_, _ = e.runCommand(ctx, "btrfs", "device", "scan", "--forget")

	// mkfs.btrfs con retry simple (kernel a veces ve los devices ocupados)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}
		_, err := e.runCommand(ctx, "mkfs.btrfs", args...)
		if err == nil {
			lastErr = nil
			break
		}
		lastErr = err
		if !strings.Contains(err.Error(), "Device or resource busy") {
			break
		}
	}
	if lastErr != nil {
		return nil, fmt.Errorf("mkfs.btrfs failed: %w", lastErr)
	}

	// Obtener UUID del filesystem creado (sobre el primer device)
	uuidOut, err := e.runCommand(ctx, "blkid", "-s", "UUID", "-o", "value", req.ByIDPaths[0])
	if err != nil {
		return nil, fmt.Errorf("CreateFilesystem: cannot read UUID: %w", err)
	}
	uuid := strings.TrimSpace(uuidOut)
	if uuid == "" {
		return nil, fmt.Errorf("CreateFilesystem: blkid returned empty UUID")
	}

	devices := make([]FilesystemDevice, len(req.ByIDPaths))
	for i, p := range req.ByIDPaths {
		devices[i] = FilesystemDevice{
			ByIDPath: p,
			DeviceID: i + 1,
		}
	}

	return &FilesystemInfo{
		BtrfsUUID: uuid,
		Devices:   devices,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// MountFilesystem / UnmountFilesystem
// ─────────────────────────────────────────────────────────────────────────────

func (e *RealBtrfsExecutor) MountFilesystem(ctx context.Context, byIDPath, mountPoint string) error {
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		return fmt.Errorf("MountFilesystem: cannot create %s: %w", mountPoint, err)
	}

	_, err := e.runCommand(ctx, "mount", "-t", "btrfs", byIDPath, mountPoint)
	if err != nil {
		return fmt.Errorf("MountFilesystem: %w", err)
	}
	return nil
}

func (e *RealBtrfsExecutor) UnmountFilesystem(ctx context.Context, mountPoint string) error {
	// Si no está montado, no es error
	_, err := os.Stat(mountPoint)
	if os.IsNotExist(err) {
		return nil
	}

	out, err := e.runCommand(ctx, "umount", mountPoint)
	if err != nil {
		// Si dice "not mounted" no es error
		if strings.Contains(out, "not mounted") || strings.Contains(err.Error(), "not mounted") {
			return nil
		}
		return fmt.Errorf("UnmountFilesystem %s: %w", mountPoint, err)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// DestroyFilesystem
// ─────────────────────────────────────────────────────────────────────────────

func (e *RealBtrfsExecutor) DestroyFilesystem(ctx context.Context, req DestroyFilesystemRequest) error {
	// 1. Pre-check: ¿hay filesystems montados ENCIMA del pool? (overlays Docker,
	//    binds...). Si los hay, el umount fallaría y dejaría un pool fantasma.
	//    Solo miramos filesystem (findmnt -R), no consultamos otros módulos.
	if req.MountPoint != "" {
		out, _ := e.runCommand(ctx, "findmnt", "-R", "-n", "-o", "TARGET", req.MountPoint)
		// countRealSubmounts: cuenta solo targets hijos reales ("<mp>/..."),
		// NO líneas crudas — findmnt puede partir una entrada en varias líneas
		// y producir un falso positivo (mismo bug que poolHasSubmounts, 13/06).
		if n := countRealSubmounts(out, req.MountPoint); n > 0 {
			return fmt.Errorf("DestroyFilesystem: pool tiene %d filesystems montados encima (servicios activos); deténlos antes de destruir", n)
		}
	}

	// 2. Unmount SIN lazy. El lazy (umount -l) desmonta del namespace pero deja
	//    los inodos vivos → escrituras fantasma → el bug del 28/05. Si el unmount
	//    falla, abortamos ANTES de wipear (wipear un FS montado lo corrompe).
	//    Nota: se eliminó la rama req.Force con umount -l a propósito.
	if req.MountPoint != "" {
		if err := e.UnmountFilesystem(ctx, req.MountPoint); err != nil {
			return fmt.Errorf("DestroyFilesystem: unmount: %w", err)
		}
		// Verificación post-unmount: findmnt debe devolver vacío.
		check, _ := e.runCommand(ctx, "findmnt", "-n", "-o", "TARGET", req.MountPoint)
		if strings.TrimSpace(check) != "" {
			return fmt.Errorf("DestroyFilesystem: el pool sigue montado tras umount; hay procesos con archivos abiertos")
		}
	}

	// 3. A partir de aquí el FS está confirmado desmontado. Seguro wipear.
	for _, p := range req.ByIDPaths {
		if err := e.WipeDevice(ctx, p); err != nil {
			// No abortar — intentar limpiar todos los devices
			logMsg("DestroyFilesystem: wipe %s failed: %v", p, err)
		}
	}

	// 4. Remove mount point if empty and under /nimos/pools
	if strings.HasPrefix(req.MountPoint, "/nimos/pools/") {
		_ = os.Remove(req.MountPoint)
	}

	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// AddDevice / RemoveDevice / ReplaceDevice
// ─────────────────────────────────────────────────────────────────────────────

func (e *RealBtrfsExecutor) AddDevice(ctx context.Context, mountPoint, byIDPath string) error {
	_, err := e.runCommand(ctx, "btrfs", "device", "add", byIDPath, mountPoint)
	if err != nil {
		return fmt.Errorf("AddDevice: %w", err)
	}
	return nil
}

func (e *RealBtrfsExecutor) RemoveDevice(ctx context.Context, mountPoint, byIDPath string) error {
	_, err := e.runCommand(ctx, "btrfs", "device", "remove", byIDPath, mountPoint)
	if err != nil {
		return fmt.Errorf("RemoveDevice: %w", err)
	}
	return nil
}

func (e *RealBtrfsExecutor) ReplaceDevice(ctx context.Context, mountPoint, oldByIDPath, newByIDPath string) error {
	// btrfs replace start <old> <new> <mountpoint>
	// El nuevo device se sincroniza desde los demás miembros del pool.
	//
	// IMPORTANTE para discos MISSING: si el disco viejo ya no existe físicamente
	// (caso típico de reparación), su by-id/path NO resuelve y btrfs falla con
	// "Never started". En ese caso btrfs exige el DEVID (número) del disco que
	// falta. Detectamos esto: si oldByIDPath no es un path existente, intentamos
	// resolver el devid del disco missing desde `btrfs filesystem show`.
	oldRef := oldByIDPath
	if !devicePathExists(oldByIDPath) {
		if devid := missingDevidForPool(mountPoint); devid != "" {
			logMsg("ReplaceDevice: old device no existe (%s); usando devid=%s del disco missing", oldByIDPath, devid)
			oldRef = devid
		}
	}

	// `-B` = foreground: BLOQUEA hasta que la reconstrucción TERMINA. Sin él,
	// `btrfs replace start` corre en segundo plano y retorna al instante, con lo
	// que el service marcaría la operación COMPLETED y cambiaría la membresía en
	// la BD mientras la copia sigue sincronizándose por detrás (minutos en GB,
	// horas en TB) — "listo" cuando aún no hay redundancia. Con `-B` el éxito de
	// este comando significa redundancia REAL reconstruida.
	//
	// runCommandNoTimeout (no el de 30 min): un replace en un array de TB puede
	// durar horas; el CmdTimeout estándar lo mataría a la mitad (STOR-03, igual
	// que ConvertProfile). Sigue respetando el ctx del caller.
	_, err := e.runCommandNoTimeout(ctx, "btrfs", "replace", "start", "-B", "-f", oldRef, newByIDPath, mountPoint)
	if err != nil {
		return fmt.Errorf("ReplaceDevice: replace start: %w", err)
	}

	// Wipefs SEGURO del old SOLO si existe físicamente (un disco missing no se
	// puede ni se debe wipear — ya no está). Como `-B` ya bloqueó hasta terminar,
	// aquí el replace está COMPLETO: wipear el viejo ya no compite con una
	// sincronización en curso.
	if devicePathExists(oldByIDPath) {
		if err := e.WipeDevice(ctx, oldByIDPath); err != nil {
			logMsg("ReplaceDevice: warning, wipe of old device %s failed: %v", oldByIDPath, err)
		}
	}
	return nil
}

// missingDevidForPool devuelve el devid (como string) del disco que falta en un
// pool degradado, leyéndolo de `btrfs filesystem show <mountpoint>`. Devuelve ""
// si no hay disco missing o no se puede determinar.
//
// Salida típica:
//
//	devid    1 size 111.79GiB used 44.03GiB path /dev/sda
//	devid    2 size 0 used 0 path <missing disk> MISSING
func missingDevidForPool(mountPoint string) string {
	out, ok := runSafe("btrfs", "filesystem", "show", mountPoint)
	if !ok {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "MISSING") && !strings.Contains(line, "missing") {
			continue
		}
		// Buscar "devid N"
		fields := strings.Fields(line)
		for i, f := range fields {
			if f == "devid" && i+1 < len(fields) {
				return fields[i+1]
			}
		}
	}
	return ""
}

// ConvertProfile cambia el profile de un pool ejecutando btrfs balance
// con filtros de profile. El comando bloquea hasta que termina (operación
// pesada que puede tardar minutos/horas).
func (e *RealBtrfsExecutor) ConvertProfile(ctx context.Context, mountPoint string, newProfile Profile) error {
	if !newProfile.IsValid() {
		return fmt.Errorf("ConvertProfile: invalid profile %q", newProfile)
	}

	profileStr := string(newProfile)
	// btrfs balance start -dconvert=raid1 -mconvert=raid1 <mountpoint>
	// Convertimos data Y metadata para mantener consistencia.
	args := []string{
		"balance", "start",
		"-dconvert=" + profileStr,
		"-mconvert=" + profileStr,
		"--full-balance",
		mountPoint,
	}

	// STOR-03: un balance de conversión en un array grande supera de sobra los
	// 30 min del CmdTimeout estándar (pensado para mkfs). Si dejáramos ese
	// timeout, el context mataría el balance a media conversión, dejando el
	// pool en estado intermedio. El balance de BTRFS es transaccional/resumible,
	// pero matarlo genera un error innecesario. Usamos runCommandNoTimeout:
	// el balance hace su propio control y termina cuando termina.
	_, err := e.runCommandNoTimeout(ctx, "btrfs", args...)
	if err != nil {
		return fmt.Errorf("ConvertProfile: %w", err)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// WipeDevice — con guards defensivos
// ─────────────────────────────────────────────────────────────────────────────

// WipeDevice borra firmas del device. Implementa los guards documentados
// en storage_invariants.md#4.2:
//  1. Verifica que el device no es el boot disk
//  2. Verifica que no está montado
//
// Solo entonces hace wipefs.
func (e *RealBtrfsExecutor) WipeDevice(ctx context.Context, byIDPath string) error {
	if byIDPath == "" {
		return fmt.Errorf("WipeDevice: empty path")
	}

	// Resolver el real path para los checks
	realPath, err := filepath.EvalSymlinks(byIDPath)
	if err != nil {
		// Si no se puede resolver el symlink, no podemos verificar nada,
		// rechazar por seguridad.
		return fmt.Errorf("WipeDevice: cannot resolve %s: %w", byIDPath, err)
	}

	// Guard 1: ¿es el boot disk?
	if isBootDisk(realPath) {
		return fmt.Errorf("WipeDevice: refusing to wipe boot disk %s (resolved from %s)",
			realPath, byIDPath)
	}

	// Guard 2: ¿está montado?
	if isDeviceMounted(realPath) {
		return fmt.Errorf("WipeDevice: refusing to wipe %s, currently mounted", realPath)
	}

	// STOR-09: cerrar la ventana TOCTOU. Entre el guard de arriba y el wipefs
	// hay una ventana en la que el device podría montarse. La re-verificación
	// inmediatamente antes del comando la reduce a microsegundos. Combinado con
	// storageMu (que serializa las mutaciones de storage) y el hecho de que el
	// daemon es el único actor, el riesgo queda prácticamente eliminado.
	if isDeviceMounted(realPath) {
		return fmt.Errorf("WipeDevice: %s se montó entre el check y el wipe (TOCTOU), abortando", realPath)
	}
	if isBootDisk(realPath) {
		return fmt.Errorf("WipeDevice: %s detectado como boot disk en re-check, abortando", realPath)
	}

	// Solo entonces, wipefs
	_, err = e.runCommand(ctx, "wipefs", "-a", byIDPath)
	if err != nil {
		return fmt.Errorf("WipeDevice: %w", err)
	}
	return nil
}

// isBootDisk devuelve true si el device es el disco de boot del sistema.
// Lo determina viendo qué device contiene la partición montada en "/".
//
// Soporta tanto particiones tradicionales (sda1 → sda) como NVMe
// (nvme0n1p2 → nvme0n1).
func isBootDisk(realPath string) bool {
	// Leer /proc/mounts y encontrar lo que está montado en "/"
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		// Por seguridad, si no podemos leer mounts, asumir que sí
		return true
	}

	rootDevice := ""
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "/" {
			rootDevice = fields[0]
			break
		}
	}
	if rootDevice == "" {
		// No encontramos root mount, asumir peor caso
		return true
	}

	// Resolver symlinks del root device
	rootReal, err := filepath.EvalSymlinks(rootDevice)
	if err != nil {
		return true
	}

	// Si el realPath es la partición del root, es boot.
	if rootReal == realPath {
		return true
	}
	// Si el realPath es el disco padre de la partición root, también es boot.
	parent := parentDeviceOf(rootReal)
	return parent == realPath
}

// parentDeviceOf devuelve el disco padre de una partición.
//
// Convenciones del kernel Linux:
//   - sd*, vd*, hd*: la partición es <disk><N>  (sda1 → sda)
//   - nvme*:         la partición es <disk>p<N> (nvme0n1p2 → nvme0n1)
//   - mmc*:          la partición es <disk>p<N> (mmcblk0p1 → mmcblk0)
//
// Si el path no parece una partición, devuelve el path original.
func parentDeviceOf(devicePath string) string {
	base := filepath.Base(devicePath)
	dir := filepath.Dir(devicePath)

	// NVMe / MMC: <disk>p<N> donde <disk> también contiene dígitos
	// (nvme0n1, mmcblk0). El separador "p" es el indicador.
	if strings.HasPrefix(base, "nvme") || strings.HasPrefix(base, "mmcblk") {
		// Buscar el último "p" seguido SOLO de dígitos al final
		idx := strings.LastIndex(base, "p")
		if idx > 0 {
			suffix := base[idx+1:]
			if suffix != "" && allDigits(suffix) {
				return filepath.Join(dir, base[:idx])
			}
		}
		// No tiene "pN" al final: es el disco entero, no una partición
		return devicePath
	}

	// sd*, vd*, hd*: stripping de dígitos finales
	trimmed := strings.TrimRight(base, "0123456789")
	if trimmed == base {
		// No tenía dígitos al final → ya es el disco, no una partición
		return devicePath
	}
	return filepath.Join(dir, trimmed)
}

// allDigits devuelve true si s no está vacía y solo contiene dígitos.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// isDeviceMounted devuelve true si el device está montado en alguna parte.
func isDeviceMounted(realPath string) bool {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		// Por seguridad, si no podemos saber, asumir que sí
		return true
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 1 {
			mounted, err := filepath.EvalSymlinks(fields[0])
			if err == nil && mounted == realPath {
				return true
			}
		}
	}
	return false
}

// ─────────────────────────────────────────────────────────────────────────────
// GetFilesystemInfo
// ─────────────────────────────────────────────────────────────────────────────

func (e *RealBtrfsExecutor) GetFilesystemInfo(ctx context.Context, mountPoint string) (*FilesystemInfo, error) {
	info := &FilesystemInfo{}

	// ── 1. Total/Used desde `btrfs filesystem usage -b <mp>` ──────────────
	// -b = bytes crudos (sin redondeo humano), parseable de forma fiable.
	usageOut, err := e.runCommand(ctx, "btrfs", "filesystem", "usage", "-b", mountPoint)
	if err == nil {
		for _, line := range strings.Split(usageOut, "\n") {
			line = strings.TrimSpace(line)
			// "Device size:		  1000204886016"
			if strings.HasPrefix(line, "Device size:") {
				info.TotalBytes = parseTrailingInt(line)
			}
			// "Used:			   123456789"
			if strings.HasPrefix(line, "Used:") {
				info.UsedBytes = parseTrailingInt(line)
			}
		}
	} else {
		logMsg("GetFilesystemInfo: usage falló en %s: %v", mountPoint, err)
	}

	// ── 2. UUID + devices desde `btrfs filesystem show <mp>` ──────────────
	showOut, err := e.runCommand(ctx, "btrfs", "filesystem", "show", mountPoint)
	if err == nil {
		for _, line := range strings.Split(showOut, "\n") {
			line = strings.TrimSpace(line)
			// "uuid: a0192857-..."
			if idx := strings.Index(line, "uuid:"); idx >= 0 {
				fields := strings.Fields(line[idx+len("uuid:"):])
				if len(fields) > 0 && info.BtrfsUUID == "" {
					info.BtrfsUUID = fields[0]
				}
			}
			// "devid    1 size 111.79GiB used 5.02GiB path /dev/sda"
			if strings.HasPrefix(line, "devid") {
				dev := parseFsDevidLine(line)
				if dev.DevicePath != "" {
					info.Devices = append(info.Devices, dev)
				}
			}
		}
	} else {
		logMsg("GetFilesystemInfo: show falló en %s: %v", mountPoint, err)
	}

	// ── 3. Errores por device desde `btrfs device stats <mp>` ─────────────
	// Formato: "[/dev/sda].write_io_errs    22"
	statsOut, err := e.runCommand(ctx, "btrfs", "device", "stats", mountPoint)
	if err == nil {
		errsByDev := parseDeviceStats(statsOut)
		for i := range info.Devices {
			if e, ok := errsByDev[info.Devices[i].DevicePath]; ok {
				info.Devices[i].WriteErrors = e.write
				info.Devices[i].ReadErrors = e.read
				info.Devices[i].FlushErrors = e.flush
			}
		}
	} else {
		logMsg("GetFilesystemInfo: device stats falló en %s: %v", mountPoint, err)
	}

	return info, nil
}

// parseTrailingInt extrae el último entero de una línea (p.ej. "Used: 12345").
func parseTrailingInt(line string) int64 {
	fields := strings.Fields(line)
	for i := len(fields) - 1; i >= 0; i-- {
		if n, err := strconv.ParseInt(fields[i], 10, 64); err == nil {
			return n
		}
	}
	return 0
}

// parseFsDevidLine parsea "devid N size X used Y path /dev/sdX" desde
// `btrfs filesystem show`. Distinta de parseDevidLine (storage_btrfs_probe.go),
// que usa formato --raw y devuelve *ObservedDevice.
func parseFsDevidLine(line string) FilesystemDevice {
	var dev FilesystemDevice
	fields := strings.Fields(line)
	for i := 0; i < len(fields)-1; i++ {
		switch fields[i] {
		case "devid":
			if n, err := strconv.Atoi(fields[i+1]); err == nil {
				dev.DeviceID = n
			}
		case "path":
			dev.DevicePath = fields[i+1]
		}
	}
	return dev
}

type devErrs struct{ write, read, flush int }

// parseDeviceStats parsea la salida de `btrfs device stats`, devolviendo
// los contadores de error indexados por device path.
func parseDeviceStats(out string) map[string]devErrs {
	result := map[string]devErrs{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		// "[/dev/sda].write_io_errs    22"
		if !strings.HasPrefix(line, "[") {
			continue
		}
		end := strings.Index(line, "]")
		if end < 0 {
			continue
		}
		devPath := line[1:end]
		rest := strings.TrimSpace(line[end+1:])
		val := parseTrailingInt(rest)
		e := result[devPath]
		switch {
		case strings.HasPrefix(rest, ".write_io_errs"):
			e.write = int(val)
		case strings.HasPrefix(rest, ".read_io_errs"):
			e.read = int(val)
		case strings.HasPrefix(rest, ".flush_io_errs"):
			e.flush = int(val)
		}
		result[devPath] = e
	}
	return result
}

// FilesystemExistsByUUID consulta `btrfs filesystem show` para ver si
// el kernel conoce un filesystem con el UUID dado. No requiere que esté
// montado.
//
// Comando: btrfs filesystem show <uuid>
// - exit 0: existe → devolvemos true
// - exit != 0: no existe (o no se pudo determinar) → false
//
// Si btrfs filesystem show falla por motivos distintos a "no existe"
// (kernel sin btrfs, permisos), devolvemos error explícito en lugar de
// false silencioso. El caller (recovery) debe decidir qué hacer ante
// incertidumbre.
func (e *RealBtrfsExecutor) FilesystemExistsByUUID(ctx context.Context, btrfsUUID string) (bool, error) {
	if btrfsUUID == "" {
		return false, fmt.Errorf("FilesystemExistsByUUID: empty UUID")
	}

	// Antes de consultar, hacemos un device scan para que el kernel
	// conozca los filesystems disponibles aunque no estén montados.
	_, _ = e.runCommand(ctx, "btrfs", "device", "scan")

	cmd := exec.CommandContext(ctx, "btrfs", "filesystem", "show", btrfsUUID)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}

	// Exit code != 0 con mensaje "No filesystem found" → no existe
	outStr := strings.ToLower(string(output))
	if strings.Contains(outStr, "no filesystem found") ||
		strings.Contains(outStr, "not a btrfs filesystem") {
		return false, nil
	}

	// Cualquier otro error: propagamos. El caller decide.
	return false, fmt.Errorf("FilesystemExistsByUUID: %v: %s", err, strings.TrimSpace(string(output)))
}
