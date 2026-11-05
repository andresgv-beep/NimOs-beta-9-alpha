// storage_integration_failure_test.go — Caminos de FALLO contra BTRFS real.
//
// Los tests de integración originales cubrían solo el camino feliz (crear,
// destruir, añadir/quitar disco). Lo que decide si se pierden datos —un disco
// que muere, el montaje degradado al arrancar, el replace, la corrupción que
// el scrub tiene que encontrar— solo se probaba contra MockBtrfsExecutor, que
// hace lo que CREEMOS que hace btrfs. Aquí se ejecuta el código de NimOS
// contra el kernel de verdad con discos loopback.
//
// REQUIERE root y un espacio de montajes PRIVADO (el test del disco perdido
// monta un /etc/fstab temporal encima del real; fuera del namespace no se toca
// nada y los montajes desaparecen solos al salir):
//
//	sudo unshare -m --propagation private go test -tags integration -run TestStorageIntegrationFailure -v
//
//go:build integration

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// failureRig: discos loopback con un enlace estable tipo by-id, como en un NAS
// real. "Matar" un disco = soltar el loop + borrar el enlace: desde NimOS el
// disco desaparece exactamente igual que uno arrancado de la bahía.
type failureRig struct {
	t       *testing.T
	disks   []*loopbackDisk
	links   []string // /tmp/.../byid/disk-N → /dev/loopX
	ids     []string // IDs en storage_devices
	service *StorageService
}

func newFailureRig(t *testing.T, n int) *failureRig {
	t.Helper()
	requireBtrfs(t)

	disks, cleanupDisks := createLoopbackDisks(t, n, 256)
	linkDir := t.TempDir()

	conn, _, dbCleanup := setupTestDB(t)
	repo := NewStorageRepo(conn)
	executor := NewRealBtrfsExecutor()
	executor.CmdTimeout = 120 * time.Second
	service := NewStorageService(conn, repo, NewPolicyChecker(), executor, NewMockDeviceScanner(nil))
	service.deviceChecker = noopDeviceChecker

	// Varios caminos (reconcile, scrub, docker) leen el service global.
	origSvc := storageService
	storageService = service

	// CreatePool/Destroy/rename regeneran el bloque [nimos] de /etc/fstab
	// desde la BD. Con el service de test como global, eso escribía el fstab
	// REAL del host (los namespaces de montaje no aíslan el contenido de los
	// ficheros). Los tests que necesitan fstab montan uno propio
	// (privateFstab) con buildManagedFstab.
	origSync := syncFstabFromDBFn
	syncFstabFromDBFn = func(context.Context) error { return nil }

	rig := &failureRig{t: t, disks: disks, service: service}
	ctx := context.Background()
	tx, _ := conn.BeginTx(ctx, nil)
	for i, d := range disks {
		link := filepath.Join(linkDir, fmt.Sprintf("disk-%d", i))
		if err := os.Symlink(d.DevicePath, link); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		id := fmt.Sprintf("fail-dev-%d", i)
		if _, err := repo.UpsertDevice(ctx, tx, &Device{
			ID:          id,
			Serial:      fmt.Sprintf("LOOPFAIL-%d", i),
			ByIDPath:    link,
			CurrentPath: link,
			Model:       "loopback",
			SizeBytes:   256 * 1024 * 1024,
		}); err != nil {
			tx.Rollback()
			t.Fatalf("UpsertDevice: %v", err)
		}
		rig.links = append(rig.links, link)
		rig.ids = append(rig.ids, id)
	}
	tx.Commit()

	t.Cleanup(func() {
		storageService = origSvc
		syncFstabFromDBFn = origSync
		dbCleanup()
		cleanupDisks()
		_ = exec.Command("btrfs", "device", "scan", "--forget").Run()
	})
	return rig
}

// createPool crea un pool real con el service de NimOS sobre los discos dados.
func (r *failureRig) createPool(name string, profile Profile, diskIdx ...int) *Pool {
	r.t.Helper()
	mp := filepath.Join(nimosPoolsDir, name)
	r.t.Cleanup(func() {
		_ = exec.Command("umount", "-l", mp).Run()
		_ = os.Remove(mp)
	})
	var ids []string
	for _, i := range diskIdx {
		ids = append(ids, r.ids[i])
	}
	op, err := r.service.CreatePool(context.Background(), CreatePoolRequest{
		Name: name, Profile: profile, DeviceIDs: ids, Compression: "none", WipeFirst: true,
	})
	if err != nil {
		r.t.Fatalf("CreatePool: %v", err)
	}
	if op.Status != OpStatusCompleted {
		r.t.Fatalf("CreatePool: status=%s error=%s", op.Status, opErr(op))
	}
	pool, err := r.service.GetPool(context.Background(), *op.PoolID)
	if err != nil || pool == nil {
		r.t.Fatalf("GetPool: %v", err)
	}
	return pool
}

// kill simula un disco arrancado de la bahía.
func (r *failureRig) kill(i int) {
	r.t.Helper()
	if out, err := exec.Command("losetup", "-d", r.disks[i].DevicePath).CombinedOutput(); err != nil {
		r.t.Fatalf("losetup -d %s: %v: %s", r.disks[i].DevicePath, err, out)
	}
	r.disks[i].DevicePath = "" // el cleanup no debe volver a soltarlo
	_ = os.Remove(r.links[i])
}

func opErr(op *Operation) string {
	if op != nil && op.Error != nil {
		return *op.Error
	}
	return ""
}

// waitOp espera a que una Operation async salga de in_progress.
func (r *failureRig) waitOp(id string, timeout time.Duration) *Operation {
	r.t.Helper()
	return waitIntegrationOp(r.t, r.service, id, timeout)
}

// waitScrub espera a que el scrub del pool termine y devuelve el status que
// sirve NimOS (parseScrubStatusOutput sobre la salida REAL de btrfs-progs).
func waitScrub(t *testing.T, mp, name string, timeout time.Duration) map[string]interface{} {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		st := getBtrfsScrubStatus(mp, name)
		if st["status"] == "done" {
			return st
		}
		if time.Now().After(deadline) {
			out, _ := exec.Command("btrfs", "scrub", "status", mp).CombinedOutput()
			t.Fatalf("scrub no terminó en %s (status=%v):\n%s", timeout, st["status"], out)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func run(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

// writeRandomFile escribe size bytes aleatorios (incompresibles) y devuelve su sha256.
func writeRandomFile(t *testing.T, path string, size int) string {
	t.Helper()
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	run(t, "sync")
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func fileSHA(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// chunkProfiles devuelve los perfiles reales del pool ("Data, RAID1", ...)
// según `btrfs filesystem df`. GlobalReserve es siempre single y se ignora.
func chunkProfiles(t *testing.T, mp string) []string {
	t.Helper()
	var profiles []string
	for _, line := range strings.Split(run(t, "btrfs", "filesystem", "df", mp), "\n") {
		head, _, ok := strings.Cut(strings.TrimSpace(line), ":")
		// Ni GlobalReserve ni los avisos de btrfs ("WARNING: Multiple block
		// group profiles detected") son perfiles de chunks.
		if !ok || strings.HasPrefix(head, "GlobalReserve") || head == "WARNING" {
			continue
		}
		profiles = append(profiles, head)
	}
	return profiles
}

func mountOptions(t *testing.T, mp string) string {
	t.Helper()
	return strings.TrimSpace(run(t, "findmnt", "-n", "-o", "OPTIONS", mp))
}

func dropCaches(t *testing.T, loops ...string) {
	t.Helper()
	for _, l := range loops {
		_ = exec.Command("blockdev", "--flushbufs", l).Run()
	}
	_ = os.WriteFile("/proc/sys/vm/drop_caches", []byte("3"), 0)
}

// corruptCopy pisa con ceros 4 KiB del contenido de needle dentro del fichero
// que respalda un disco: rompe UNA copia física sin pasar por btrfs.
func corruptCopy(t *testing.T, backing string, needle []byte) {
	t.Helper()
	raw, err := os.ReadFile(backing)
	if err != nil {
		t.Fatal(err)
	}
	off := bytes.Index(raw, needle)
	if off < 0 {
		t.Fatalf("no encuentro el bloque del fichero en %s (¿compresión activa?)", backing)
	}
	f, err := os.OpenFile(backing, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteAt(make([]byte, 4096), int64(off)); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
}

// requirePrivateMountNS: el test va a montar un fstab temporal sobre
// /etc/fstab. Solo es seguro dentro de un namespace de montajes propio.
func requirePrivateMountNS(t *testing.T) {
	t.Helper()
	self, err1 := os.Readlink("/proc/self/ns/mnt")
	pid1, err2 := os.Readlink("/proc/1/ns/mnt")
	if err1 != nil || err2 != nil || self == pid1 {
		t.Skip("monta un fstab temporal: ejecutar bajo `sudo unshare -m --propagation private go test ...`")
	}
}

// privateFstab monta un fichero temporal con el contenido dado sobre
// /etc/fstab (solo visible dentro de este namespace).
func privateFstab(t *testing.T, content string) {
	t.Helper()
	requirePrivateMountNS(t)
	tmp := filepath.Join(t.TempDir(), "fstab")
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	run(t, "mount", "--bind", tmp, "/etc/fstab")
	t.Cleanup(func() { _ = exec.Command("umount", "/etc/fstab").Run() })
}

// ─────────────────────────────────────────────────────────────────────────────
// 1. Convertir a single con varios discos: la metadata sigue redundante
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageIntegrationFailureConvertToSingleKeepsMetadataRedundant(t *testing.T) {
	rig := newFailureRig(t, 2)
	pool := rig.createPool("integ-convert", ProfileRaid1, 0, 1)
	sha := writeRandomFile(t, filepath.Join(pool.MountPoint, "datos.bin"), 16<<20)

	// Lo que hacía el código antes: -mconvert=single. El kernel lo rechaza sin
	// --force (reduce la redundancia de la metadata), así que la conversión a
	// single simplemente FALLABA. Lo dejamos fijado para que se sepa por qué.
	oldOut, oldErr := exec.Command("btrfs", "balance", "start",
		"-dconvert=single", "-mconvert=single", "--full-balance", pool.MountPoint).CombinedOutput()
	if oldErr == nil {
		t.Fatalf("el comando antiguo debía fallar sin --force y funcionó:\n%s", oldOut)
	}
	t.Logf("comando antiguo rechazado por btrfs (esperado): %s", strings.TrimSpace(string(oldOut)))

	op, err := rig.service.ConvertProfile(context.Background(), ConvertProfileRequest{
		PoolID: pool.ID, NewProfile: ProfileSingle,
	})
	if err != nil {
		t.Fatalf("ConvertProfile: %v", err)
	}
	if done := rig.waitOp(op.ID, 3*time.Minute); done.Status != OpStatusCompleted {
		t.Fatalf("ConvertProfile: status=%s error=%s", done.Status, opErr(done))
	}

	profiles := chunkProfiles(t, pool.MountPoint)
	t.Logf("perfiles tras convertir: %v", profiles)
	for _, p := range profiles {
		if strings.HasPrefix(p, "Metadata") && p != "Metadata, RAID1" {
			t.Errorf("metadata sin redundancia tras convertir a single: %q", p)
		}
		if strings.HasPrefix(p, "Data") && p != "Data, single" {
			t.Errorf("datos no convertidos: %q", p)
		}
	}
	if got, _ := rig.service.GetPool(context.Background(), pool.ID); got.Profile != ProfileSingle {
		t.Errorf("profile en BD: %q, want single", got.Profile)
	}
	if got, err := fileSHA(filepath.Join(pool.MountPoint, "datos.bin")); err != nil || got != sha {
		t.Errorf("datos alterados por la conversión: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 2. Disco que muere → arranque degradado en solo lectura → replace
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageIntegrationFailureDiskLossDegradedBootAndReplace(t *testing.T) {
	requirePrivateMountNS(t)
	rig := newFailureRig(t, 3) // 0 y 1 en el pool, 2 de repuesto
	ctx := context.Background()
	pool := rig.createPool("integ-degraded", ProfileRaid1, 0, 1)
	file := filepath.Join(pool.MountPoint, "fotos.bin")
	sha := writeRandomFile(t, file, 32<<20)

	// fstab exactamente como lo genera NimOS (nofail, sin degraded).
	privateFstab(t, buildManagedFstab("", []*Pool{pool}))

	// ─── El disco 0 muere con la máquina apagada ───────────────────────
	run(t, "umount", pool.MountPoint)
	rig.kill(0)
	run(t, "btrfs", "device", "scan", "--forget")

	// Un mount normal NO debe entrar: sin `degraded` fijo en fstab, un
	// disco que solo tarda no acaba montando el pool sin redundancia.
	if out, err := exec.Command("mount", pool.MountPoint).CombinedOutput(); err == nil {
		t.Fatalf("el mount normal debía fallar con un disco ausente:\n%s", out)
	}

	// ─── Arranque del daemon: reconcileMountState ──────────────────────
	res, err := reconcileMountState(ctx)
	if err != nil {
		t.Fatalf("reconcileMountState: %v", err)
	}
	if res.Mounted != 1 || res.ReadOnly != 1 {
		t.Fatalf("reconcile: %+v, want Mounted=1 ReadOnly=1", res)
	}
	opts := mountOptions(t, pool.MountPoint)
	if !strings.Contains(opts, "ro") || !strings.Contains(opts, "degraded") {
		t.Fatalf("montaje tras perder un disco: %q, want degraded,ro", opts)
	}
	if got, err := fileSHA(file); err != nil || got != sha {
		t.Fatalf("los datos deben seguir legibles desde el espejo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pool.MountPoint, "nuevo.txt"), []byte("x"), 0644); err == nil {
		t.Fatal("un pool degradado no debe aceptar escrituras")
	}
	if devid := missingDevidForPool(pool.MountPoint); devid == "" {
		t.Fatalf("missingDevidForPool no detecta el disco ausente:\n%s",
			run(t, "btrfs", "filesystem", "show", pool.MountPoint))
	}

	// ─── Reparación: replace del disco muerto por el de repuesto ───────
	op, err := rig.service.ReplaceDevice(ctx, ReplaceDeviceRequest{
		PoolID: pool.ID, OldDeviceID: rig.ids[0], NewDeviceID: rig.ids[2],
	})
	if err != nil {
		t.Fatalf("ReplaceDevice: %v", err)
	}
	if done := rig.waitOp(op.ID, 5*time.Minute); done.Status != OpStatusCompleted {
		t.Fatalf("ReplaceDevice: status=%s error=%s\n%s", done.Status, opErr(done),
			run(t, "btrfs", "filesystem", "show", pool.MountPoint))
	}
	// El replace lanza un scrub de verificación: debe salir limpio.
	if st := waitScrub(t, pool.MountPoint, pool.Name, 3*time.Minute); st["errors"] != 0 {
		t.Errorf("scrub post-replace con errores: %v", st["dataErrors"])
	}

	show := run(t, "btrfs", "filesystem", "show", pool.MountPoint)
	if strings.Contains(strings.ToLower(show), "missing") {
		t.Errorf("sigue faltando un disco tras el replace:\n%s", show)
	}
	if strings.Contains(mountOptions(t, pool.MountPoint), "ro,") {
		t.Errorf("el pool debe quedar escribible tras reparar: %s", mountOptions(t, pool.MountPoint))
	}
	// Redundancia REAL: ningún chunk single (se crean si se escribe en
	// degraded,rw y nadie los reconvierte después).
	for _, p := range chunkProfiles(t, pool.MountPoint) {
		if !strings.HasSuffix(p, "RAID1") {
			t.Errorf("chunks sin redundancia tras el replace: %q (todos: %v)", p, chunkProfiles(t, pool.MountPoint))
		}
	}
	got, _ := rig.service.GetPool(ctx, pool.ID)
	var members []string
	for _, d := range got.Devices {
		members = append(members, d.ID)
	}
	if strings.Contains(strings.Join(members, ","), rig.ids[0]) || !strings.Contains(strings.Join(members, ","), rig.ids[2]) {
		t.Errorf("miembros en BD tras el replace: %v", members)
	}
	if sum, err := fileSHA(file); err != nil || sum != sha {
		t.Errorf("datos alterados tras el replace: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 3. Corrupción en UNA copia → el scrub la encuentra y la repara
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageIntegrationFailureScrubRepairsOneBadCopy(t *testing.T) {
	rig := newFailureRig(t, 2)
	pool := rig.createPool("integ-bitrot", ProfileRaid1, 0, 1)
	file := filepath.Join(pool.MountPoint, "documentos.bin")
	sha := writeRandomFile(t, file, 8<<20)
	raw, _ := os.ReadFile(file)
	needle := raw[4<<20 : 4<<20+64<<10]

	run(t, "umount", pool.MountPoint)
	corruptCopy(t, rig.disks[0].BackingFile, needle)
	dropCaches(t, rig.disks[0].DevicePath, rig.disks[1].DevicePath)
	run(t, "mount", "-t", "btrfs", rig.links[1], pool.MountPoint)

	if err := startScrubOnPool(pool.MountPoint, pool.Name); err != nil {
		t.Fatalf("startScrubOnPool: %v", err)
	}
	st := waitScrub(t, pool.MountPoint, pool.Name, 2*time.Minute)
	t.Logf("NimOS ve el scrub así: errors=%v dataErrors=%v", st["errors"], st["dataErrors"])
	if n, _ := st["errors"].(int); n == 0 {
		t.Fatalf("el scrub de NimOS no reporta la corrupción:\n%s", run(t, "btrfs", "scrub", "status", pool.MountPoint))
	}

	stats, err := parseBtrfsDeviceStats(pool.MountPoint)
	if err != nil {
		t.Fatalf("parseBtrfsDeviceStats: %v", err)
	}
	csum := 0
	for _, s := range stats {
		csum += s.ChecksumErrors
	}
	if csum == 0 {
		t.Errorf("device stats no registran errores de checksum: %+v\n%s", stats, run(t, "btrfs", "device", "stats", pool.MountPoint))
	}
	// Reparado: no hay corrupción ACTIVA aunque el contador histórico siga > 0.
	if !lastScrubWasClean(pool.MountPoint) {
		t.Errorf("con todo corregido, lastScrubWasClean debía ser true:\n%s", run(t, "btrfs", "scrub", "status", pool.MountPoint))
	}
	if got, err := fileSHA(file); err != nil || got != sha {
		t.Errorf("el fichero no se recuperó intacto: %v", err)
	}

	// Un segundo scrub ya no encuentra nada: la copia mala se reescribió.
	if err := startScrubOnPool(pool.MountPoint, pool.Name); err != nil {
		t.Fatal(err)
	}
	if st := waitScrub(t, pool.MountPoint, pool.Name, 2*time.Minute); st["errors"] != 0 {
		t.Errorf("segundo scrub con errores: %v", st["dataErrors"])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 4. Corrupción en LAS DOS copias → datos perdidos, y NimOS tiene que decirlo
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageIntegrationFailureScrubReportsUnrecoverableLoss(t *testing.T) {
	rig := newFailureRig(t, 2)
	pool := rig.createPool("integ-loss", ProfileRaid1, 0, 1)
	file := filepath.Join(pool.MountPoint, "perdido.bin")
	writeRandomFile(t, file, 8<<20)
	raw, _ := os.ReadFile(file)
	needle := raw[4<<20 : 4<<20+64<<10]

	run(t, "umount", pool.MountPoint)
	corruptCopy(t, rig.disks[0].BackingFile, needle)
	corruptCopy(t, rig.disks[1].BackingFile, needle)
	dropCaches(t, rig.disks[0].DevicePath, rig.disks[1].DevicePath)
	run(t, "mount", "-t", "btrfs", rig.links[0], pool.MountPoint)

	if err := startScrubOnPool(pool.MountPoint, pool.Name); err != nil {
		t.Fatalf("startScrubOnPool: %v", err)
	}
	st := waitScrub(t, pool.MountPoint, pool.Name, 2*time.Minute)
	if n, _ := st["errors"].(int); n == 0 {
		t.Errorf("NimOS no reporta errores con las dos copias rotas: %v", st["dataErrors"])
	}
	if lastScrubWasClean(pool.MountPoint) {
		t.Errorf("con datos irreparables, lastScrubWasClean NO puede ser true:\n%s", run(t, "btrfs", "scrub", "status", pool.MountPoint))
	}
	// btrfs no devuelve datos corruptos en silencio: la lectura falla.
	if _, err := os.ReadFile(file); err == nil {
		t.Error("leer un bloque sin ninguna copia buena debía dar error de E/S")
	}
}
