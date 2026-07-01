package main

// ═══════════════════════════════════════════════════════════════════════════════
// NimOS Storage — Wipe Module (Plan v2)
// Based on TrueNAS middleware disk wipe architecture.
// Reviewed by GPT (structure) and Gemini (rollback, serial check, mutex).
//
// Principles:
//   1. Verify after every operation — never trust exit codes alone
//   2. Unmount clean first, fuser as fallback
//   3. Zero start + end of disk (GPT backup table)
//   4. Exclusive lock — one storage operation at a time
//   5. Journal — know where we are if daemon crashes
// ═══════════════════════════════════════════════════════════════════════════════

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ─── Command Executor ────────────────────────────────────────────────────────

type CmdOptions struct {
	Timeout   time.Duration
	Retries   int
	RetryWait time.Duration
}

type CmdResult struct {
	Stdout string
	Stderr string
	Code   int
	OK     bool
}

func runCmd(cmd string, args []string, opts CmdOptions) (CmdResult, error) {
	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}

	var lastErr error

	for attempt := 0; attempt <= opts.Retries; attempt++ {
		if attempt > 0 {
			time.Sleep(opts.RetryWait)
		}

		ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
		c := exec.CommandContext(ctx, cmd, args...)

		var out, errb bytes.Buffer
		c.Stdout = &out
		c.Stderr = &errb

		err := c.Run()
		cancel()

		res := CmdResult{
			Stdout: out.String(),
			Stderr: errb.String(),
		}

		if err == nil {
			res.OK = true
			return res, nil
		}

		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.Code = exitErr.ExitCode()
			lastErr = fmt.Errorf("%s failed (code %d): %s", cmd, res.Code, res.Stderr)
		} else {
			lastErr = err
		}
	}

	return CmdResult{}, lastErr
}

// ─── Storage Errors ──────────────────────────────────────────────────────────

type StorageError struct {
	Code string
	Msg  string
}

func (e StorageError) Error() string {
	return e.Msg
}

var (
	ErrNotEligible = StorageError{"NOT_ELIGIBLE", "disk not eligible for this operation"}
	ErrIsBoot      = StorageError{"IS_BOOT", "cannot operate on boot disk"}
	ErrBusy        = StorageError{"BUSY", "device or resource busy"}
	ErrWipeFail    = StorageError{"WIPE_FAIL", "wipe verification failed — partitions still present"}
)

// ErrDiskHasFilesystem es un error tipado y enriquecido que se devuelve
// cuando preFlightCheck detecta que un disco tiene un filesystem (BTRFS o
// cualquier otro) y la operación podría destruir datos.
//
// La UI captura este error y muestra al usuario información rica para
// decidir: importar el pool existente, destruir explícitamente, o cancelar.
//
// Modelo Managed/Observed (docs/storage_observer_design.md):
//
//	· Observed: lo que detectamos físicamente en el disco
//	· Managed:  si NimOS ya gestiona el pool (IsManaged=true)
//
// El JSON output sigue el patrón de StorageError pero con campos extra:
//
//	{
//	  "code": "DISK_HAS_FILESYSTEM",
//	  "msg": "Disk /dev/sdb has an existing BTRFS filesystem",
//	  "disk": "/dev/sdb",
//	  "fs_type": "btrfs",
//	  "fs_uuid": "884ec939-...",
//	  "fs_label": "DATOS4",
//	  "fs_profile": "raid1",
//	  "is_managed": true,
//	  "pool_name": "DATOS4",
//	  "observation_health": "healthy",
//	  "size_bytes": 119000000000,
//	  "used_bytes": 552000,
//	  "last_seen": "2026-05-17T18:00:00Z"
//	}
type ErrDiskHasFilesystem struct {
	Disk    string `json:"disk"`
	FSType  string `json:"fs_type"`
	FSUUID  string `json:"fs_uuid,omitempty"`
	FSLabel string `json:"fs_label,omitempty"`

	// Profile real (solo si es BTRFS detectado por el observer)
	FSProfile string `json:"fs_profile,omitempty"`

	// Si NimOS ya gestiona este filesystem
	IsManaged bool   `json:"is_managed"`
	PoolID    string `json:"pool_id,omitempty"`
	PoolName  string `json:"pool_name,omitempty"`

	// Estado computado por el observer (healthy/incomplete/degraded/partial/unknown)
	ObservationHealth HealthStatus `json:"observation_health,omitempty"`

	// Capacidad y uso reales (si están disponibles)
	SizeBytes int64 `json:"size_bytes,omitempty"`
	UsedBytes int64 `json:"used_bytes,omitempty"`

	// Cuándo fue observado por última vez
	LastSeen string `json:"last_seen,omitempty"`
}

// Error implementa la interfaz error. El mensaje es legible para humanos
// pero los datos estructurados están en los campos públicos.
func (e *ErrDiskHasFilesystem) Error() string {
	if e.IsManaged && e.PoolName != "" {
		return fmt.Sprintf("disk %s is part of managed pool %q (%s)",
			e.Disk, e.PoolName, e.FSType)
	}
	if e.FSLabel != "" {
		return fmt.Sprintf("disk %s has %s filesystem (label=%q, uuid=%s)",
			e.Disk, e.FSType, e.FSLabel, e.FSUUID)
	}
	return fmt.Sprintf("disk %s has %s filesystem (uuid=%s)",
		e.Disk, e.FSType, e.FSUUID)
}

// Code devuelve el código semántico para que la UI lo distinga.
func (e *ErrDiskHasFilesystem) Code() string {
	return "DISK_HAS_FILESYSTEM"
}

// ─── Journal ─────────────────────────────────────────────────────────────────

type OpStatus string

const (
	OpPending OpStatus = "pending"
	OpDone    OpStatus = "done"
	OpFailed  OpStatus = "failed"
)

type StepPhase string

const (
	PhaseStarted   StepPhase = "started"
	PhaseCompleted StepPhase = "completed"
)

// journalPath es la ruta del journal de wipe. Es var (no const) para que los
// tests puedan redirigirlo a un tmpdir; en producción usa el valor por defecto.
var journalPath = "/var/lib/nimos/storage-journal.json"

type JournalOp struct {
	ID        string            `json:"id"`
	Type      string            `json:"type"`
	Step      int               `json:"step"`
	Phase     StepPhase         `json:"phase"`
	Status    OpStatus          `json:"status"`
	Data      map[string]string `json:"data"`
	Timestamp string            `json:"ts"`
}

var journalMu sync.Mutex

func journalSave(op JournalOp) error {
	journalMu.Lock()
	defer journalMu.Unlock()

	op.Timestamp = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(op, "", "  ")
	if err != nil {
		return err
	}

	// Atomic write: tmp → fsync → rename
	tmpPath := journalPath + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	f.Close()
	return os.Rename(tmpPath, journalPath)
}

func journalClear() {
	os.Remove(journalPath)
}

// journalRecoverOnBoot consume el journal de wipe al arrancar (STOR-06).
// Si un wipe se interrumpió por un crash, el journal quedó con el último paso
// alcanzado. Esta función lo lee, lo reporta con claridad (qué device, qué
// paso) y lo limpia.
//
// Por qué solo limpiar y no "reanudar": un wipe es re-ejecutable desde cero
// sin daño (borrar firmas dos veces es inocuo). Reanudar a media secuencia
// sería más frágil que volver a empezar. Así que el consumidor deja el sistema
// en estado conocido (sin journal pendiente) y, si el disco quedó a medio
// wipear, el usuario simplemente relanza el wipe — que ahora parte limpio.
//
// Devuelve true si había un journal pendiente (hubo wipe interrumpido).
func journalRecoverOnBoot() bool {
	journalMu.Lock()
	defer journalMu.Unlock()

	data, err := os.ReadFile(journalPath)
	if err != nil {
		// No hay journal → no había wipe en curso. Caso normal.
		return false
	}

	var op JournalOp
	if e := json.Unmarshal(data, &op); e != nil {
		// Journal corrupto: lo limpiamos igualmente para no arrastrarlo.
		logMsg("StorageJournal: journal de wipe corrupto al arranque, limpiando: %v", e)
		os.Remove(journalPath)
		return true
	}

	devInfo := ""
	if d, ok := op.Data["device"]; ok {
		devInfo = fmt.Sprintf(" device=%s", d)
	}
	logMsg("StorageJournal: wipe interrumpido detectado al arranque "+
		"(op=%s tipo=%s paso=%d fase=%s status=%s%s). "+
		"El wipe es re-ejecutable; limpiando journal. Si el disco quedó a medias, relanza el wipe.",
		op.ID, op.Type, op.Step, op.Phase, op.Status, devInfo)

	os.Remove(journalPath)
	return true
}

var storageLockFile *os.File

func tryStorageLockOrWarn() {
	lockPath := journalPath + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return
	}
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		f.Close()
		logMsg("WARNING: storage lock already held — another daemon instance may be running")
		return
	}
	storageLockFile = f
}

// ─── Operations Engine ───────────────────────────────────────────────────────

var storageMu sync.Mutex

type StepErrorPolicy int

const (
	FailFast StepErrorPolicy = iota
	Continue
	Ignore
)

type Step struct {
	Name   string
	Do     func() error
	Undo   func() error
	Policy StepErrorPolicy
}

func runSteps(op JournalOp, steps []Step) error {
	var completed []int

	for i := op.Step; i < len(steps); i++ {
		op.Step = i
		op.Phase = PhaseStarted
		op.Status = OpPending
		journalSave(op)

		logMsg("storage op '%s': step %d/%d — %s", op.ID, i+1, len(steps), steps[i].Name)

		if err := steps[i].Do(); err != nil {
			switch steps[i].Policy {
			case FailFast:
				op.Status = OpFailed
				journalSave(op)
				logMsg("storage op '%s': step %d FAILED — %s — rolling back", op.ID, i, err)

				for j := len(completed) - 1; j >= 0; j-- {
					idx := completed[j]
					if steps[idx].Undo != nil {
						logMsg("storage op '%s': rollback step %d — %s", op.ID, idx, steps[idx].Name)
						steps[idx].Undo()
					}
				}
				journalClear()
				return fmt.Errorf("step %d (%s) failed: %w", i, steps[i].Name, err)

			case Continue:
				logMsg("storage op '%s': step %d warning — %s — continuing", op.ID, i, err)

			case Ignore:
				// silent
			}
		}

		completed = append(completed, i)
		op.Phase = PhaseCompleted
		journalSave(op)
	}

	op.Status = OpDone
	journalSave(op)
	journalClear()
	return nil
}

// ─── Pre-flight Check ────────────────────────────────────────────────────────

// preFlightCheck verifica que un disco es seguro de wipear/usar.
//
// Beta 8.1 Bloque C2: añadido check contra el observer para detectar
// filesystems no managed que se perderían silenciosamente. Si hay
// filesystem detectado, devuelve *ErrDiskHasFilesystem con contexto rico
// que la UI puede usar para mostrar al usuario y dejarle decidir.
//
// La operación se aborta solo si hay filesystem detectado. Discos
// totalmente limpios (loose devices) pasan el check sin problema.
//
// Si el observer no está disponible (boot temprano, NIMOS_NO_STORAGE_OBSERVER=1),
// se hace fallback a un check simple via `blkid` para no perder la salvaguarda.
func preFlightCheck(diskPath string) error {
	return preFlightCheckWithOptions(diskPath, false)
}

// preFlightCheckWithOptions ejecuta el preflight con la opción de permitir
// wipe sobre filesystems huérfanos (no managed). Boot disk, kernel holders
// y filesystems MANAGED siguen bloqueando aunque allowOrphanWipe=true.
func preFlightCheckWithOptions(diskPath string, allowOrphanWipe bool) error {
	diskName := strings.TrimPrefix(diskPath, "/dev/")

	// Boot disk?
	lsblkRaw, _ := runSafe("lsblk", "-J", "-b", "-o", "NAME,MOUNTPOINT,TYPE")
	rootDisk := findRootDiskGo(lsblkRaw)
	if diskName == rootDisk {
		return ErrIsBoot
	}

	// Kernel holders? (LVM, dm, RAID)
	holdersPath := fmt.Sprintf("/sys/block/%s/holders", diskName)
	entries, err := os.ReadDir(holdersPath)
	if err == nil && len(entries) > 0 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return fmt.Errorf("disk %s has active holders: %s", diskPath, strings.Join(names, ", "))
	}

	// Disk exists?
	if _, err := os.Stat(diskPath); err != nil {
		return fmt.Errorf("disk %s not found", diskPath)
	}

	// Filesystem detection — consulta el observer si está disponible.
	// Esto es lo que evita pérdida silenciosa de datos.
	if fsErr := detectFilesystemOnDisk(diskPath); fsErr != nil {
		// allowOrphanWipe=true permite saltarse esta protección SOLO si
		// el FS detectado es huérfano (no managed por NimOS).
		// Los pools managed siempre están protegidos.
		if allowOrphanWipe {
			if e, ok := fsErr.(*ErrDiskHasFilesystem); ok && !e.IsManaged {
				return nil
			}
		}
		return fsErr
	}

	return nil
}

// detectFilesystemOnDisk consulta el StorageObserver para ver si el disco
// pertenece a algún filesystem BTRFS conocido. Si lo hace, devuelve
// *ErrDiskHasFilesystem con contexto rico.
//
// Si el observer no está disponible, hace un blkid rápido como fallback
// (no es tan rico, pero al menos detecta cualquier FS — ext4, ntfs, etc.).
//
// Devuelve nil si el disco está limpio o no se puede determinar (en cuyo
// caso preFlightCheck continúa).
func detectFilesystemOnDisk(diskPath string) error {
	// Caso 1: observer activo → consulta el cache
	if globalObserver != nil {
		snap := globalObserver.Snapshot()
		if snap != nil {
			for i := range snap.Filesystems {
				fs := &snap.Filesystems[i]
				for _, dev := range fs.Devices {
					if dev.Path == diskPath {
						return &ErrDiskHasFilesystem{
							Disk:              diskPath,
							FSType:            "btrfs",
							FSUUID:            fs.UUID,
							FSLabel:           fs.Label,
							FSProfile:         fs.Profile,
							IsManaged:         fs.IsManaged,
							PoolID:            fs.ManagedPoolID,
							PoolName:          fs.ManagedPoolName,
							ObservationHealth: fs.ObservationHealth,
							SizeBytes:         fs.SizeBytes,
							UsedBytes:         fs.UsedBytes,
							LastSeen:          fs.LastSeen.Format("2006-01-02T15:04:05Z07:00"),
						}
					}
				}
			}
			// Snapshot consultado, disco no aparece en ningún FS → limpio
			return nil
		}
	}

	// Caso 2: fallback blkid (cualquier FS, no solo BTRFS)
	// Si blkid devuelve output no vacío, hay FS. Pero sin contexto rico —
	// solo type y UUID. Es mejor que nada para no perder la salvaguarda.
	out, ok := runSafe("blkid", "-o", "export", diskPath)
	if !ok || strings.TrimSpace(out) == "" {
		return nil // disco sin FS detectado
	}
	parsed := parseBlkidExport(out)
	if parsed["TYPE"] == "" {
		return nil
	}
	return &ErrDiskHasFilesystem{
		Disk:    diskPath,
		FSType:  parsed["TYPE"],
		FSUUID:  parsed["UUID"],
		FSLabel: parsed["LABEL"],
	}
}

// parseBlkidExport parsea el output `blkid -o export` que viene como
// KEY=value por línea. Útil para fallback cuando el observer no responde.
func parseBlkidExport(out string) map[string]string {
	result := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if idx := strings.Index(line, "="); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			val := strings.TrimSpace(line[idx+1:])
			result[key] = val
		}
	}
	return result
}

// ─── WIPE ────────────────────────────────────────────────────────────────────

// wipeDiskGo is the real wipe implementation based on TrueNAS.
// Called from storage_http_v2.go (handleWipe) and from create-pool flow
// when WipeFirst=true.
//
// Order of operations:
//  0. Pre-flight safety check (boot disk, holders)
//  1. Unmount partitions cleanly
//  2. Kill processes using the disk (fuser fallback)
//  3. Clear ZFS labels
//  4. Zero first 32MB (MBR, GPT, superblocks)
//  5. Zero last 32MB (GPT backup, ZFS tail labels)
//  6. Destroy GPT with sgdisk
//  7. Clear remaining signatures with wipefs
//  8. Force kernel to re-read partition table
//  9. VERIFY: lsblk must show zero partitions
func wipeDiskGo(diskPath string) map[string]interface{} {
	return wipeDiskWithOptions(diskPath, false)
}

// wipeDiskForce permite wipear discos con filesystem detectado SIEMPRE
// QUE NO estén managed por NimOS. Casos legítimos:
//
//	· Destroy intencional de un orphan_filesystem desde la UI
//	· Limpieza de un disco con BTRFS abandonado de una instalación previa
//
// SIEMPRE bloquea si:
//
//	· Es el disco de boot
//	· Tiene holders del kernel activos (LVM, dm, RAID)
//	· Pertenece a un pool managed (protección dura contra borrado accidental)
func wipeDiskForce(diskPath string) map[string]interface{} {
	return wipeDiskWithOptions(diskPath, true)
}

func wipeDiskWithOptions(diskPath string, force bool) map[string]interface{} {
	storageMu.Lock()
	defer storageMu.Unlock()

	result := wipeDiskInternalWithOptions(diskPath, force)

	// Bloque C2: notificar al observer si el wipe fue exitoso. El disco
	// pasa a ser loose device en el próximo snapshot.
	if errVal, hasErr := result["error"]; !hasErr || errVal == "" {
		notifyStorageChanged()
	}

	return result
}

// wipeDiskInternal does the actual wipe — called with lock already held
func wipeDiskInternal(diskPath string) map[string]interface{} {
	return wipeDiskInternalWithOptions(diskPath, false)
}

func wipeDiskInternalWithOptions(diskPath string, force bool) map[string]interface{} {

	// Pre-flight (con o sin permiso de orphan-bypass)
	if err := preFlightCheckWithOptions(diskPath, force); err != nil {
		return map[string]interface{}{"error": err.Error()}
	}

	opts := CmdOptions{Timeout: 30 * time.Second, Retries: 1, RetryWait: 1 * time.Second}
	optsNoFail := CmdOptions{Timeout: 15 * time.Second}
	diskBase := filepath.Base(diskPath)

	op := JournalOp{
		ID:   "wipe-" + diskBase,
		Type: "wipe",
		Data: map[string]string{"disk": diskPath},
	}

	steps := []Step{
		// 0. Unmount partitions cleanly first
		{Name: "unmount_clean", Policy: Continue, Do: func() error {
			res, _ := runCmd("lsblk", []string{"-ln", "-o", "NAME,MOUNTPOINT", diskPath}, optsNoFail)
			for _, line := range strings.Split(res.Stdout, "\n") {
				fields := strings.Fields(line)
				if len(fields) >= 2 && fields[1] != "" {
					runCmd("umount", []string{"-f", fields[1]}, optsNoFail)
				}
			}
			runCmd("umount", []string{"-f", diskPath}, optsNoFail)
			return nil
		}},

		// 1. Kill processes using the disk (fallback)
		{Name: "fuser_kill", Policy: Continue, Do: func() error {
			runCmd("fuser", []string{"-km", diskPath}, optsNoFail)
			partsOut, _ := runCmd("lsblk", []string{"-ln", "-o", "NAME", diskPath}, optsNoFail)
			for _, line := range strings.Split(partsOut.Stdout, "\n") {
				p := strings.TrimSpace(line)
				if p != "" && p != diskBase {
					runCmd("fuser", []string{"-km", "/dev/" + p}, optsNoFail)
				}
			}
			time.Sleep(500 * time.Millisecond)
			return nil
		}},

		// 2. Clear BTRFS multi-device locks.
		// Beta 8: ZFS label clearing removed (ZFS no longer supported).
		{Name: "clear_fs_labels", Policy: Continue, Do: func() error {
			// Release BTRFS multi-device lock — without this, mkfs.btrfs
			// fails with "Device or resource busy" on multi-device pools
			if hasBtrfs {
				runCmd("btrfs", []string{"device", "scan", "--forget"}, optsNoFail)
			}
			return nil
		}},

		// 3. Wipe signatures on EACH PARTITION first, then remove partitions from kernel
		// This must happen BEFORE zeroing the disk or destroying GPT
		{Name: "wipe_partitions", Policy: Continue, Do: func() error {
			partsOut, _ := runCmd("lsblk", []string{"-ln", "-o", "NAME", diskPath}, optsNoFail)
			for _, line := range strings.Split(partsOut.Stdout, "\n") {
				p := strings.TrimSpace(line)
				if p != "" && p != diskBase {
					partDev := "/dev/" + p
					// Wipe filesystem signatures on the partition
					runCmd("wipefs", []string{"-af", partDev}, optsNoFail)
					// Zero first 1MB of partition (superblock area)
					runCmd("dd", []string{"if=/dev/zero", "of=" + partDev, "bs=1M", "count=1", "conv=fsync,notrunc"}, optsNoFail)
				}
			}
			// Remove partitions from kernel cache
			runCmd("partx", []string{"-d", diskPath}, optsNoFail)
			time.Sleep(500 * time.Millisecond)
			return nil
		}},

		// 4. Zero first 32MB (MBR, GPT primary, superblocks)
		{Name: "zero_start", Policy: FailFast, Do: func() error {
			_, err := runCmd("dd", []string{
				"if=/dev/zero", "of=" + diskPath,
				"bs=1M", "count=32", "conv=fsync,notrunc",
			}, opts)
			return err
		}},

		// 5. Zero last 32MB (GPT backup table, ZFS tail labels)
		{Name: "zero_end", Policy: Continue, Do: func() error {
			res, err := runCmd("blockdev", []string{"--getsize64", diskPath}, optsNoFail)
			if err != nil {
				return nil
			}
			var size int64
			fmt.Sscanf(strings.TrimSpace(res.Stdout), "%d", &size)
			if size > 64*1024*1024 {
				seekMB := (size / (1024 * 1024)) - 32
				runCmd("dd", []string{
					"if=/dev/zero", "of=" + diskPath,
					"bs=1M", "count=32",
					fmt.Sprintf("seek=%d", seekMB),
					"conv=fsync,notrunc",
				}, opts)
			}
			return nil
		}},

		// 6. Destroy GPT/MBR structures
		{Name: "sgdisk_zap", Policy: Continue, Do: func() error {
			runCmd("sgdisk", []string{"-Z", diskPath}, optsNoFail)
			return nil
		}},

		// 7. Final wipefs on disk itself
		{Name: "wipefs_disk", Policy: Continue, Do: func() error {
			runCmd("wipefs", []string{"-af", diskPath}, optsNoFail)
			return nil
		}},

		// 8. Force kernel to re-read partition table
		{Name: "reread_partitions", Policy: Continue, Do: func() error {
			runCmd("partx", []string{"-d", diskPath}, optsNoFail)
			runCmd("blockdev", []string{"--rereadpt", diskPath}, optsNoFail)
			runCmd("partprobe", []string{diskPath}, optsNoFail)
			runCmd("udevadm", []string{"settle", "--timeout=10"}, optsNoFail)
			time.Sleep(2 * time.Second)
			return nil
		}},

		// 9. VERIFY — disk must be clean.
		// Uses wipefs (reads disk directly) instead of lsblk (kernel cache)
		// to avoid false failures when the kernel hasn't updated yet.
		{Name: "verify_clean", Policy: FailFast, Do: func() error {
			for attempt := 0; attempt < 3; attempt++ {
				// Primary check: wipefs reads signatures directly from disk
				wipeRes, _ := runCmd("wipefs", []string{"--list", "--noheadings", diskPath}, optsNoFail)
				sigCount := 0
				for _, line := range strings.Split(strings.TrimSpace(wipeRes.Stdout), "\n") {
					if strings.TrimSpace(line) != "" {
						sigCount++
					}
				}

				// Secondary check: lsblk for partitions (kernel view)
				lsRes, _ := runCmd("lsblk", []string{"-ln", "-o", "NAME", diskPath}, optsNoFail)
				partCount := 0
				for _, line := range strings.Split(strings.TrimSpace(lsRes.Stdout), "\n") {
					line = strings.TrimSpace(line)
					if line != "" && line != diskBase {
						partCount++
					}
				}

				// Clean if no signatures on disk — partitions in lsblk may be stale kernel cache
				if sigCount == 0 {
					if partCount > 0 {
						logMsg("Wipe verified: %s clean (0 signatures, %d stale partitions in kernel cache — will clear on next reread)", diskPath, partCount)
						// One more attempt to clear kernel cache
						runCmd("blockdev", []string{"--rereadpt", diskPath}, optsNoFail)
					} else {
						logMsg("Wipe verified: %s is clean (0 signatures, 0 partitions)", diskPath)
					}
					return nil
				}

				if attempt < 2 {
					logMsg("Wipe verify attempt %d: %d signatures remain on %s — retrying", attempt+1, sigCount, diskPath)
					runCmd("wipefs", []string{"-af", diskPath}, optsNoFail)
					runCmd("dd", []string{"if=/dev/zero", "of=" + diskPath, "bs=1M", "count=1", "conv=fsync,notrunc"}, optsNoFail)
					runCmd("sgdisk", []string{"-Z", diskPath}, optsNoFail)
					runCmd("blockdev", []string{"--rereadpt", diskPath}, optsNoFail)
					runCmd("partprobe", []string{diskPath}, optsNoFail)
					runCmd("udevadm", []string{"settle", "--timeout=10"}, optsNoFail)
					time.Sleep(3 * time.Second)
				}
			}
			return fmt.Errorf("wipe verification failed: signatures still on %s after 3 attempts", diskPath)
		}},
	}

	if err := runSteps(op, steps); err != nil {
		return map[string]interface{}{"error": err.Error()}
	}

	return map[string]interface{}{"ok": true, "disk": diskPath}
}
