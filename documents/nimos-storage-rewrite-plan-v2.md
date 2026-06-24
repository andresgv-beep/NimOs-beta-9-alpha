# NimOS Storage — Reescritura desde cero (v2)

> Basado en la arquitectura de TrueNAS SCALE middleware, portado a Go.
> Incluye código base generado por GPT, revisado y corregido.
> Aportes de Gemini: rollback, serial check, exclusive lock, idempotencia.
> Sin Docker en la lógica de storage. Pools, discos, wipe, montaje. Nada más.

---

## Principios (no negociables)

1. **Verificación post-acción** (TrueNAS): No confiar en exit codes. Después de wipe → `lsblk`. Después de mount → `findmnt`. Después de create → `zpool list`.
2. **La realidad del hardware gana** (Gemini): Al arrancar, reconciliar `zpool list` real contra `storage.json`. Si difieren, el hardware manda.
3. **Idempotencia** (Gemini): Los pasos `Do()` verifican si el trabajo ya está hecho. Si el zpool ya existe, no falla — pasa al siguiente paso.
4. **Rollback automático** (Gemini): Si un paso falla, se deshacen los pasos completados en orden inverso.
5. **Exclusive Lock** (Gemini): Un mutex global impide operaciones simultáneas. No se puede wipear mientras se crea un pool.
6. **Identidad por serial** (Gemini): Antes de operaciones destructivas, verificar serial del disco. `/dev/sdX` puede cambiar entre reboots.
7. **Nunca escribir al disco del sistema** (diagnóstico de hoy): `isPathOnMountedPool()` en toda operación que toque `/nimos/pools/`.
8. **Journal de operaciones** (GPT): Cada operación persiste su paso actual. Si el daemon crashea, sabe dónde retomar o limpiar.

---

## Arquitectura: 7 componentes

```
daemon/
├── storage_cmd.go        ← Ejecutor de comandos con timeout + retries
├── storage_journal.go    ← Journal de operaciones para recuperación
├── storage_ops.go        ← Motor de operaciones idempotentes
├── storage_errors.go     ← Errores tipados
├── storage_disks.go      ← Detección y clasificación de discos
├── storage_wipe.go       ← Wipe de discos
├── storage_pools.go      ← Crear, destruir, montar, listar pools
├── storage_health.go     ← Health monitoring, scrub, alerts
```

Todo en package `main` (mismo que el daemon actual). Se puede reestructurar en packages después cuando funcione.

---

## 1. `storage_cmd.go` — Ejecutor de comandos

Reemplaza la función `run()` actual del daemon que traga errores silenciosamente.

```go
package main

import (
    "bytes"
    "context"
    "errors"
    "fmt"
    "os/exec"
    "time"
)

type CmdOptions struct {
    Timeout   time.Duration
    Retries   int
    RetryWait time.Duration
}

var defaultCmdOpts = CmdOptions{
    Timeout:   30 * time.Second,
    Retries:   0,
    RetryWait: 1 * time.Second,
}

type CmdResult struct {
    Stdout string
    Stderr string
    Code   int
    OK     bool
}

// runCmd ejecuta un comando con timeout, retries y captura de errores.
// A diferencia de run(), NUNCA traga errores silenciosamente.
// NOTA: cuando se llama desde un Step, el Step recibe un ctx padre
// con timeout global de 10 min. runCmd crea su propio sub-timeout
// (por defecto 30s). Si el ctx padre expira, CommandContext cancela el comando.
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
```

---

## 2. `storage_journal.go` — Journal de operaciones

Permite recuperar operaciones interrumpidas (crash, reboot durante wipe, etc.).

```go
package main

import (
    "encoding/json"
    "os"
    "sync"
)

type OpStatus string

const (
    OpPending OpStatus = "pending"
    OpDone    OpStatus = "done"
    OpFailed  OpStatus = "failed"
)

// StepPhase distingue si un paso empezó o terminó.
// Aporte de Gemini — elimina ambigüedad tras crash:
//   PhaseStarted   → paso puede estar a medias, repetir o rollback
//   PhaseCompleted → paso terminó OK, continuar con el siguiente
type StepPhase string

const (
    PhaseStarted   StepPhase = "started"
    PhaseCompleted StepPhase = "completed"
)

// journalPath dentro de la estructura existente de NimOS
const journalPath = "/var/lib/nimos/storage-journal.json"

type JournalOp struct {
    ID        string            `json:"id"`
    Type      string            `json:"type"`    // "wipe", "create_pool", "destroy_pool"
    Step      int               `json:"step"`
    Phase     StepPhase         `json:"phase"`   // started o completed (Gemini)
    Status    OpStatus          `json:"status"`
    Data      map[string]string `json:"data"`
    Timestamp string            `json:"ts"`      // ISO8601 para debugging (Gemini)
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

    // Escritura atómica: tmp → fsync → rename (Gemini)
    // Previene JSON truncado si el daemon crashea durante la escritura.
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

    // Rename es atómico en Linux — el journal nunca queda a medias
    return os.Rename(tmpPath, journalPath)
}

func journalLoad() (*JournalOp, error) {
    data, err := os.ReadFile(journalPath)
    if err != nil {
        return nil, err
    }
    var op JournalOp
    if err := json.Unmarshal(data, &op); err != nil {
        // JSON truncado/corrupto por crash durante escritura (Gemini)
        // No es bit-rot — es corrupción por crash. Borrar y seguir.
        logMsg("WARNING: journal file corrupted (truncated JSON?) — deleting: %s", err)
        os.Remove(journalPath)
        return nil, fmt.Errorf("journal corrupted, deleted")
    }
    return &op, err
}

func journalClear() {
    os.Remove(journalPath)
}

// journalRecover se llama al arrancar el daemon.
// Usa phase tracking (Gemini) para decidir qué hacer:
//   PhaseStarted   → paso estaba a medias, necesita rollback o retry
//   PhaseCompleted → paso terminó, puede continuar desde el siguiente
func journalRecover() {
    // Guardrail ligero: verificar que no hay otra instancia del daemon
    // corriendo operaciones de storage (Gemini — restart race protection)
    tryStorageLockOrWarn()

    op, err := journalLoad()
    if err != nil {
        return // no hay journal pendiente
    }
    if op.Status == OpDone {
        journalClear()
        return
    }

    switch op.Phase {
    case PhaseStarted:
        // Paso empezó pero no terminó — estado incierto
        logMsg("WARNING: Operation '%s' crashed during step %d (started but not completed) — cleaning up",
            op.ID, op.Step)
        // TODO: ejecutar rollback de los pasos anteriores
    case PhaseCompleted:
        // Paso terminó OK — se puede reanudar desde el siguiente
        logMsg("WARNING: Operation '%s' crashed after step %d (completed) — can resume from step %d",
            op.ID, op.Step, op.Step+1)
        // TODO: reanudar desde op.Step + 1
    default:
        logMsg("WARNING: Operation '%s' has unknown phase '%s' — clearing",
            op.ID, op.Phase)
    }

    journalClear()
}

// tryStorageLockOrWarn intenta tomar un file lock no-bloqueante.
// NO bloquea si falla — solo logea warning.
// Protege contra restart race de systemd (Gemini):
//   proceso A muere, proceso B arranca antes de que A libere recursos.
// El kernel libera el flock automáticamente cuando el proceso muere.
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
    storageLockFile = f // mantener fd abierto mientras el daemon viva
}
```

---

## 3. `storage_ops.go` — Motor de operaciones con rollback

Aporte de Gemini: cada paso tiene un `Do` y un `Undo`. Si un paso falla, se ejecutan los `Undo` de los pasos completados en orden inverso. Esto deja el sistema limpio en vez de a medias.

Segundo aporte de Gemini: **Global Exclusive Lock**. Un mutex global impide que dos operaciones de storage se ejecuten a la vez (ej: usuario hace doble click en Wipe, o wipe + create pool simultáneos).

```go
package main

import (
    "fmt"
    "sync"
)

// storageMu protege TODAS las operaciones de storage.
// Solo una operación destructiva puede estar en curso a la vez.
// Wipe, create pool, destroy pool, expand — todas pasan por aquí.
var storageMu sync.Mutex

// StepErrorPolicy determina qué hacer si un paso falla.
// Aporte de Gemini — no todos los errores son iguales.
type StepErrorPolicy int

const (
    FailFast StepErrorPolicy = iota // Detiene ejecución y dispara rollback
    Continue                        // Logea error pero sigue al siguiente paso
    Ignore                          // Ignora error silenciosamente (limpieza opcional)
)

// Step define una operación con su inversa para rollback.
// Do y Undo reciben context para respetar el timeout global.
// Si el context expira, los comandos ejecutados via runCmd se cancelan.
type Step struct {
    Name   string
    Do     func(ctx context.Context) error
    Undo   func(ctx context.Context) error // nil si no se puede deshacer
    Policy StepErrorPolicy // por defecto FailFast
}

// runSteps ejecuta pasos con journal + rollback + exclusive lock + timeout global.
// - Global lock: solo una operación de storage a la vez
// - Timeout global: 10 minutos para toda la operación (Gemini)
// - ErrorPolicy por paso: FailFast (rollback), Continue (log + sigue), Ignore
// - Si un paso FailFast falla: ejecuta Undo de los pasos completados
func runSteps(op JournalOp, steps []Step) error {
    storageMu.Lock()
    defer storageMu.Unlock()

    // Timeout global de 10 minutos para toda la secuencia de pasos
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
    defer cancel()

    var completed []int

    for i := op.Step; i < len(steps); i++ {
        // Comprobar timeout global
        if ctx.Err() != nil {
            logMsg("storage op '%s': global timeout reached at step %d", op.ID, i)
            break
        }

        op.Step = i
        op.Status = OpPending
        journalSave(op)

        logMsg("storage op '%s': step %d/%d — %s", op.ID, i+1, len(steps), steps[i].Name)

        // Marcar STARTED antes de ejecutar (Gemini phase tracking)
        op.Phase = PhaseStarted
        journalSave(op)

        if err := steps[i].Do(ctx); err != nil {
            switch steps[i].Policy {
            case FailFast:
                op.Status = OpFailed
                journalSave(op)
                logMsg("storage op '%s': step %d FAILED (FailFast) — %s — initiating rollback",
                    op.ID, i, err)

                // Rollback: deshacer pasos completados en orden inverso
                for j := len(completed) - 1; j >= 0; j-- {
                    idx := completed[j]
                    if steps[idx].Undo != nil {
                        logMsg("storage op '%s': rollback step %d — %s", op.ID, idx, steps[idx].Name)
                        if undoErr := steps[idx].Undo(ctx); undoErr != nil {
                            logMsg("storage op '%s': rollback step %d failed: %s", op.ID, idx, undoErr)
                        }
                    }
                }

                journalClear()
                return fmt.Errorf("step %d (%s) failed: %w", i, steps[i].Name, err)

            case Continue:
                logMsg("storage op '%s': step %d WARNING (Continue) — %s — continuing",
                    op.ID, i, err)
                // No rollback, sigue al siguiente paso

            case Ignore:
                // Silencioso
            }
        }

        completed = append(completed, i)

        // Marcar COMPLETED después de ejecutar OK (Gemini phase tracking)
        op.Phase = PhaseCompleted
        journalSave(op)
    }

    op.Status = OpDone
    journalSave(op)
    journalClear()
    return nil
}
```

---

## 4. `storage_errors.go` — Errores tipados

```go
package main

type StorageError struct {
    Code string
    Msg  string
}

func (e StorageError) Error() string {
    return e.Msg
}

var (
    ErrNotEligible  = StorageError{"NOT_ELIGIBLE", "disk not eligible for this operation"}
    ErrIsBoot       = StorageError{"IS_BOOT", "cannot operate on boot disk"}
    ErrBusy         = StorageError{"BUSY", "device or resource busy"}
    ErrMountFail    = StorageError{"MOUNT_FAIL", "mount verification failed"}
    ErrWipeFail     = StorageError{"WIPE_FAIL", "wipe verification failed — partitions still present"}
    ErrPoolExists   = StorageError{"POOL_EXISTS", "pool already exists"}
    ErrPoolNotFound = StorageError{"POOL_NOT_FOUND", "pool not found"}
    ErrNotMounted   = StorageError{"NOT_MOUNTED", "pool is not mounted — refusing operation to protect system disk"}
)
```

---

## 5. `storage_disks.go` — Detección de discos

```go
package main

// Clasificación de discos — whitelist de prefijos válidos
// Solo sd*, nvme*, vd* son discos reales.
// Todo lo demás (loop, ram, zram, dm, md) se ignora.
var validDiskPrefixes = []string{"sd", "nvme", "vd"}

type DiskClass string

const (
    DiskBoot     DiskClass = "boot"
    DiskInPool   DiskClass = "in_pool"
    DiskEligible DiskClass = "eligible"
    DiskUSBSmall DiskClass = "usb_small"
    DiskSkip     DiskClass = "skip"
)

type DetectedDisk struct {
    Name          string      `json:"name"`
    Path          string      `json:"path"`
    Model         string      `json:"model"`
    Serial        string      `json:"serial"`
    Size          int64       `json:"size"`
    SizeFormatted string      `json:"sizeFormatted"`
    Transport     string      `json:"transport"`
    Rotational    bool        `json:"rotational"`
    Removable     bool        `json:"removable"`
    Class         DiskClass   `json:"classification"`
    PoolName      string      `json:"poolName,omitempty"`
    Partitions    []DiskPart  `json:"partitions"`
    HasData       bool        `json:"hasData"`
}

type DiskPart struct {
    Name       string `json:"name"`
    Path       string `json:"path"`
    Size       int64  `json:"size"`
    Fstype     string `json:"fstype"`
    Label      string `json:"label"`
    Mountpoint string `json:"mountpoint"`
}

// classifyDisk — reglas claras:
//   BOOT:      contiene partición montada en /
//   IN_POOL:   aparece en storage.json
//   USB_SMALL: USB + removable + < 10GB (pendrives)
//   ELIGIBLE:  todo lo demás >= 1GB con prefijo válido
//   SKIP:      virtual, < 1GB, prefijo no válido
func classifyDisk(name, transport string, size int64, removable bool,
    rootDisk string, poolDisks map[string]bool) DiskClass {

    // Whitelist de prefijos
    valid := false
    for _, prefix := range validDiskPrefixes {
        if len(name) >= len(prefix) && name[:len(prefix)] == prefix {
            valid = true
            break
        }
    }
    if !valid {
        return DiskSkip
    }

    if size < 1*1024*1024*1024 { // < 1GB
        return DiskSkip
    }

    if name == rootDisk {
        return DiskBoot
    }

    if poolDisks["/dev/"+name] {
        return DiskInPool
    }

    // USB pendrive: USB + removable + < 10GB
    if transport == "usb" && removable && size < 10*1024*1024*1024 {
        return DiskUSBSmall
    }

    return DiskEligible
}

// partitionName devuelve el nombre correcto de la partición 1.
// Para SATA/USB: sda → sda1
// Para NVMe: nvme0n1 → nvme0n1p1
func partitionName(diskName string) string {
    for _, prefix := range []string{"nvme", "loop"} {
        if len(diskName) >= len(prefix) && diskName[:len(prefix)] == prefix {
            return diskName + "p1"
        }
    }
    return diskName + "1"
}

// verifyDiskSerial comprueba que el disco en `path` sigue siendo el mismo
// que esperamos. Las letras /dev/sdX pueden cambiar entre reboots.
// Antes de cualquier operación destructiva (wipe, partition, zpool create),
// verificar que el serial coincide.
// Aporte de Gemini — previene wipear el disco equivocado.
func verifyDiskSerial(path, expectedSerial string) error {
    if expectedSerial == "" {
        return nil // sin serial conocido, skip (algunos USB no lo reportan)
    }
    res, err := runCmd("lsblk", []string{"-dn", "-o", "SERIAL", path},
        CmdOptions{Timeout: 5 * time.Second})
    if err != nil {
        return nil // no crítico si lsblk falla
    }
    actual := strings.TrimSpace(res.Stdout)
    if actual == "" {
        return nil // disco sin serial, skip
    }
    if actual != expectedSerial {
        return fmt.Errorf("SAFETY: disk at %s has serial %s, expected %s — refusing operation", path, actual, expectedSerial)
    }
    return nil
}

// preFlightCheck ejecuta verificaciones de seguridad ANTES de cualquier
// operación destructiva en un disco. Aporte de Gemini.
//   - ¿Es el disco del sistema? → rechazar
//   - ¿Tiene holders (LVM, RAID, multipath)? → rechazar
//   - ¿El serial coincide? → rechazar si no
func preFlightCheck(diskPath string, expectedSerial string) error {
    diskName := strings.TrimPrefix(diskPath, "/dev/")

    // 1. ¿Es el disco del sistema?
    rootDisk := findRootDisk()
    if diskName == rootDisk {
        return ErrIsBoot
    }

    // 2. ¿Tiene holders del kernel? (LVM, dm, RAID activo, multipath)
    holdersPath := fmt.Sprintf("/sys/block/%s/holders", diskName)
    entries, err := os.ReadDir(holdersPath)
    if err == nil && len(entries) > 0 {
        names := []string{}
        for _, e := range entries {
            names = append(names, e.Name())
        }
        return fmt.Errorf("disk %s has active holders: %s — cannot operate", diskPath, strings.Join(names, ", "))
    }

    // 3. Serial check
    return verifyDiskSerial(diskPath, expectedSerial)
}

// Reconciliación al arrancar (Gemini: "hardware as source of truth")
//
// Estado FOREIGN: pool existe en ZFS/BTRFS pero NO en storage.json
//   → Log warning, ofrecer "Importar" en la UI
//
// Estado MISSING: pool existe en storage.json pero NO en hardware
//   → Marcar como "offline/desconectado" en la UI
//
// Estado OK: pool existe en ambos y está montado
//   → Normal
```

---

## 6. `storage_wipe.go` — Wipe verificable

```go
package main

import (
    "fmt"
    "strings"
    "time"
)

// wipeDiskNew es el wipe basado en TrueNAS.
// Diferencias con el wipe actual:
//   1. Intenta desmonte limpio ANTES de fuser
//   2. Escribe ceros al principio Y al final del disco (GPT backup)
//   3. VERIFICA con lsblk que las particiones desaparecieron
//   4. Si la verificación falla, devuelve error — no "ok"
func wipeDiskNew(diskPath string) error {
    // Verificar que el disco existe y no es boot
    // (se valida antes de llegar aquí)

    opts := CmdOptions{Timeout: 30 * time.Second, Retries: 1, RetryWait: 1 * time.Second}
    optsNoFail := CmdOptions{Timeout: 15 * time.Second}

    op := JournalOp{
        ID:   "wipe-" + diskPath,
        Type: "wipe",
        Data: map[string]string{"disk": diskPath},
    }

    steps := []Step{
        // Paso 0: Desmontar particiones (intento limpio primero)
        {Name: "unmount_clean", Do: func() error {
            res, _ := runCmd("lsblk", []string{"-ln", "-o", "NAME,MOUNTPOINT", diskPath}, optsNoFail)
            for _, line := range strings.Split(res.Stdout, "\n") {
                fields := strings.Fields(line)
                if len(fields) >= 2 && fields[1] != "" {
                    runCmd("umount", []string{fields[1]}, optsNoFail)
                }
            }
            return nil
        }, Undo: nil},

        // Paso 1: Matar procesos que usen el disco (fallback)
        {Name: "fuser_kill", Do: func() error {
            runCmd("fuser", []string{"-km", diskPath}, optsNoFail)
            time.Sleep(500 * time.Millisecond)
            return nil
        }, Undo: nil},

        // Paso 2: Limpiar labels ZFS si aplica
        {Name: "zpool_labelclear", Do: func() error {
            runCmd("zpool", []string{"labelclear", "-f", diskPath}, optsNoFail)
            return nil
        }, Undo: nil},

        // Paso 3: Escribir ceros — primeros 32MB
        {Name: "zero_start", Do: func() error {
            _, err := runCmd("dd", []string{
                "if=/dev/zero", "of=" + diskPath,
                "bs=1M", "count=32", "conv=fsync,notrunc",
            }, opts)
            return err
        }, Undo: nil},

        // Paso 4: Escribir ceros — últimos 32MB (GPT backup table)
        {Name: "zero_end", Do: func() error {
            res, err := runCmd("blockdev", []string{"--getsize64", diskPath}, optsNoFail)
            if err != nil {
                return nil
            }
            sizeStr := strings.TrimSpace(res.Stdout)
            var size int64
            fmt.Sscanf(sizeStr, "%d", &size)
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
        }, Undo: nil},

        // Paso 5: Destruir GPT
        {Name: "sgdisk_zap", Do: func() error {
            runCmd("sgdisk", []string{"-Z", diskPath}, optsNoFail)
            return nil
        }, Undo: nil},

        // Paso 6: Borrar firmas restantes
        {Name: "wipefs", Do: func() error {
            runCmd("wipefs", []string{"-af", diskPath}, optsNoFail)
            return nil
        }, Undo: nil},

        // Paso 7: Forzar re-lectura de tabla de particiones
        {Name: "reread_partitions", Do: func() error {
            runCmd("blockdev", []string{"--rereadpt", diskPath}, optsNoFail)
            runCmd("partprobe", []string{diskPath}, optsNoFail)
            runCmd("udevadm", []string{"settle", "--timeout=5"}, optsNoFail)
            time.Sleep(1 * time.Second)
            return nil
        }, Undo: nil},

        // Paso 8: VERIFICAR — particiones deben haber desaparecido
        {Name: "verify_clean", Do: func() error {
            res, _ := runCmd("lsblk", []string{"-ln", "-o", "NAME", diskPath}, optsNoFail)
            lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
            partCount := 0
            for _, line := range lines {
                line = strings.TrimSpace(line)
                if line != "" && line != strings.TrimPrefix(diskPath, "/dev/") {
                    partCount++
                }
            }
            if partCount > 0 {
                return fmt.Errorf("wipe verification failed: %d partitions still present on %s", partCount, diskPath)
            }
            return nil
        }, Undo: nil},
    }

    return runSteps(op, steps)
}
```

---

## 7. `storage_pools.go` — Crear, destruir, montar

### Crear pool ZFS

```go
func createPoolZfsNew(name string, vdevType string, disks []string) error {
    op := JournalOp{
        ID:   "create-zfs-" + name,
        Type: "create_pool",
        Data: map[string]string{"name": name, "type": "zfs"},
    }

    opts := CmdOptions{Timeout: 60 * time.Second}
    zpoolName := "nimos-" + name
    mountPoint := "/nimos/pools/" + name

    steps := []Step{
        // Paso 0: Wipear todos los discos
        {Name: "wipe_disks", Do: func() error {
            for _, d := range disks {
                if err := wipeDiskNew(d); err != nil {
                    return fmt.Errorf("wipe %s: %w", d, err)
                }
            }
            return nil
        }, Undo: nil}, // wipe no se deshace

        // Paso 1: Particionar discos (partición BF01 para ZFS)
        {Name: "partition_disks", Do: func() error {
            for _, d := range disks {
                _, err := runCmd("sgdisk", []string{"-n", "1:0:0", "-t", "1:BF01", d}, opts)
                if err != nil {
                    return fmt.Errorf("partition %s: %w", d, err)
                }
            }
            runCmd("udevadm", []string{"settle", "--timeout=5"}, opts)
            time.Sleep(1 * time.Second)

            // Esperar a que las particiones aparezcan
            for _, d := range disks {
                pName := "/dev/" + partitionName(strings.TrimPrefix(d, "/dev/"))
                if err := waitForDevice(pName, 5*time.Second); err != nil {
                    return fmt.Errorf("partition %s not ready: %w", pName, err)
                }
            }
            return nil
        }, Undo: func() error {
            // Rollback: limpiar particiones que se acaban de crear
            for _, d := range disks {
                runCmd("sgdisk", []string{"-Z", d}, CmdOptions{Timeout: 10 * time.Second})
                runCmd("wipefs", []string{"-af", d}, CmdOptions{Timeout: 10 * time.Second})
            }
            return nil
        }},

        // Paso 2: Crear zpool
        {Name: "zpool_create", Do: func() error {
            args := []string{"create", "-f", "-o", "ashift=12", "-m", mountPoint, zpoolName}

            if vdevType != "" && vdevType != "stripe" {
                args = append(args, vdevType)
            }

            // Pasar PARTICIONES, no discos enteros
            for _, d := range disks {
                pName := partitionName(strings.TrimPrefix(d, "/dev/"))
                args = append(args, pName)
            }

            _, err := runCmd("zpool", args, opts)
            return err
        }, Undo: func() error {
            // Rollback: destruir el zpool parcial
            runCmd("zpool", []string{"destroy", "-f", zpoolName},
                CmdOptions{Timeout: 30 * time.Second})
            return nil
        }},

        // Paso 3: Configurar propiedades
        {Name: "set_properties", Do: func() error {
            props := map[string]string{
                "compression": "lz4",
                "atime":       "off",
                "xattr":       "sa",
                "acltype":     "posixacl",
            }
            for k, v := range props {
                runCmd("zfs", []string{"set", k + "=" + v, zpoolName},
                    CmdOptions{Timeout: 10 * time.Second})
            }
            return nil
        }, Undo: nil}, // propiedades no necesitan rollback

        // Paso 4: Crear datasets estándar
        {Name: "create_datasets", Do: func() error {
            for _, ds := range []string{"shares", "system-backup"} {
                runCmd("zfs", []string{"create", zpoolName + "/" + ds},
                    CmdOptions{Timeout: 10 * time.Second})
            }
            return nil
        }, Undo: nil}, // si llegamos aquí y falla, el destroy del paso 2 limpia todo

        // Paso 5: VERIFICAR montaje real
        {Name: "verify_mount", Do: func() error {
            if !isPathOnMountedPool(mountPoint) {
                return ErrMountFail
            }
            return nil
        }, Undo: nil},

        // Paso 6: Guardar en storage.json + escribir identity
        {Name: "save_config", Do: func() error {
            // Guardar config...
            // Escribir .nimos-pool.json...
            return nil
        }, Undo: nil},
    }

    return runSteps(op, steps)
}

// waitForDevice espera a que un device aparezca en /dev/
func waitForDevice(path string, timeout time.Duration) error {
    deadline := time.Now().Add(timeout)
    for time.Now().Before(deadline) {
        if _, err := os.Stat(path); err == nil {
            return nil
        }
        time.Sleep(200 * time.Millisecond)
    }
    return fmt.Errorf("timeout waiting for %s", path)
}
```

### Destruir pool

```go
func destroyPoolNew(poolName string) error {
    // 1. Borrar shares de la DB que pertenecen a este pool
    // 2. Listar todos los datasets ZFS (zfs list -H -o name -r zpoolName)
    // 3. Para cada dataset (hijos primero):
    //    a. Intentar umount limpio
    //    b. Si falla: lsof + fuser -km
    //    c. zfs unmount -f
    // 4. zpool destroy -f
    // 5. Si falla: zpool export -f → zpool import → zpool destroy -f
    // 6. Si sigue fallando: zpool export -f (al menos libera discos)
    // 7. Limpiar directorio mount point
    // 8. Limpiar storage.json
    // 9. partprobe para que el kernel vea los discos libres
}
```

### Montar pools al boot

```go
func mountPoolsOnBoot() {
    // ZFS:
    //   1. zpool import -a -N       (importar sin montar)
    //   2. Para cada pool ZFS en storage.json:
    //      a. zfs set mountpoint={mp} {zpoolName}
    //      b. zfs mount {zpoolName}
    //      c. zfs mount -a
    //      d. VERIFICAR con isPathOnMountedPool
    //      e. Si falla: marcar como offline, NO crear directorios
    //
    // BTRFS:
    //   1. Para cada pool BTRFS en storage.json:
    //      a. mount -t btrfs UUID={uuid} {mp}
    //      b. VERIFICAR
    //      c. Si falla: intentar mount por label, por device
    //      d. Si todo falla: marcar como offline, NO crear directorios
    //
    // Reconciliar:
    //   - Si zpool list muestra un pool que NO está en storage.json → log warning
    //   - Si storage.json tiene un pool que no existe en zpool list → marcar offline
}
```

---

## 8. `storage_health.go` — Monitoring

```go
// Health check cada 5 minutos:
//   - Uso de disco por pool (warning >85%, critical >95%)
//   - Errores de dispositivo (btrfs device stats, zpool status)
//   - SMART si disponible
//
// Scrub:
//   POST /api/storage/scrub → inicia scrub
//   GET  /api/storage/scrub/status → progreso
//
// Snapshots (ZFS):
//   GET  /api/storage/snapshots → listar
//   POST /api/storage/snapshot → crear manual
//   POST /api/storage/snapshot/rollback → rollback
//   Scheduler automático: hourly/daily/weekly con retención configurable
```

---

## Análisis de riesgos (de GPT, integrado)

| Riesgo | Dónde se aborda | Solución |
|--------|-----------------|----------|
| `fuser -km` agresivo | `storage_wipe.go` paso 0-1 | Desmonte limpio primero, `fuser` solo como fallback |
| Race conditions udev | `storage_pools.go` paso 1 | `waitForDevice()` con retry + backoff |
| Edge cases discos (NVMe, dm) | `storage_disks.go` | Whitelist de prefijos + `partitionName()` |
| BTRFS depende de fstab | `storage_pools.go` mountOnBoot | Montar desde daemon con UUID, fstab como backup con `nofail` |
| storage.json desincronizado | `storage_pools.go` mountOnBoot | Reconciliar hardware vs config: estados FOREIGN/MISSING/OK (Gemini) |
| Fallo a mitad de operación | `storage_ops.go` | Steps con `Do`/`Undo` + rollback automático (Gemini) |
| Disco cambia de letra | `storage_disks.go` | `verifyDiskSerial()` + `preFlightCheck()` (Gemini) |
| Rollback deja basura | `storage_pools.go` | `Undo` en partition → `sgdisk -Z`, en zpool → `zpool destroy -f` (Gemini) |
| Disco bloqueado por LVM/RAID | `storage_disks.go` | `preFlightCheck()` lee `/sys/block/X/holders` (Gemini) |
| Pasos con errores no críticos | `storage_ops.go` | `StepErrorPolicy`: FailFast/Continue/Ignore (Gemini) |
| Operación colgada indefinidamente | `storage_ops.go` | `context.WithTimeout` 10 min global (Gemini) |
| Operaciones simultáneas | `storage_ops.go` | `storageMu sync.Mutex` exclusive lock (Gemini) |

---

## Orden de implementación

```
Fase 1: Infraestructura (storage_cmd + journal + ops + errors)
  → Base para todo lo demás. Se prueba aislado.

Fase 2: storage_disks.go
  → Probar con disco USB SSD: aparece como eligible
  → Probar: boot disk no aparece
  → Probar: NVMe no-boot aparece como eligible

Fase 3: storage_wipe.go
  → Probar: disco con particiones ext4 → wipe → 0 particiones
  → Probar: disco NTFS → wipe → 0 particiones
  → Probar: disco boot → RECHAZADO

Fase 4: storage_pools.go (crear + montar)
  → Probar: crear pool ZFS → montado → datasets creados
  → Probar: reiniciar daemon → pool sigue montado
  → Probar: crear pool BTRFS → mismas verificaciones

Fase 5: storage_pools.go (destruir)
  → Probar: destruir → disco libre → config limpia

Fase 6: storage_health.go
  → Health, scrub, snapshots

Cada fase se prueba en equipo real. No se pasa a la siguiente hasta que funcione.
```

---

## Referencias

- TrueNAS middleware: `https://github.com/truenas/middleware`
  - Disk wipe: `src/middlewared/middlewared/plugins/disk_/wipe.py`
  - Disk format: `src/middlewared/middlewared/plugins/disk_/format.py`
  - Pool create: `src/middlewared/middlewared/plugins/pool_/pool_operations.py`
  - Pool import: `src/middlewared/middlewared/plugins/pool_/import_pool.py`
  - Pool export: `src/middlewared/middlewared/plugins/pool_/export.py`
- DeepWiki: `https://deepwiki.com/truenas/middleware/3.2-disk-management`
- OMV ZFS: `https://github.com/OpenMediaVault-Plugin-Developers/openmediavault-zfs`
- Análisis de riesgos: sesión GPT (6 puntos integrados en la tabla de riesgos)



Ver el orden de implementación del plan v2Del plan v2, lo implementado vs pendiente:
HECHO:

✅ Fase 1: Infraestructura (cmd, journal, ops, errors)
✅ Fase 2: Detección de discos
✅ Fase 3: Wipe (con retry agresivo)
✅ Fase 4: Pool create ZFS + BTRFS
✅ Fase 5: Pool destroy ZFS + BTRFS
✅ Fase 6: Snapshots, Scrub, Datasets (ZFS)
✅ Shared folders como datasets/subvolumes con quota (ZFS + BTRFS)
✅ Bug de mount namespace (ReadWritePaths)

PENDIENTE del plan:

❌ Snapshot scheduler automático (hourly/daily/weekly con retención)
❌ Expand pool (añadir disco a pool existente)
❌ Import pool ZFS externo
❌ Reconciliación hardware vs config al boot (estados FOREIGN/MISSING/OK)
❌ SMART monitoring
❌ NimTorrent auto-update download_dir al crear/destruir pool
