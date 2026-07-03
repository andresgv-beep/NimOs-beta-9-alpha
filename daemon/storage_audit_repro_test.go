// storage_audit_repro_test.go — Tests-trampa de la auditoría de storage
// (2026-07-03). Cada test documenta una mentira detectada en la auditoría:
// un test en rojo aquí = bug CONFIRMADO pendiente de fix. Cuando se aplique
// el fix correspondiente, el test pasa a verde y se queda como regresión.
//
// Referencia completa: ~/storage-audit-2026-07-03.md
package main

import (
	"context"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────
// AUDIT-1 · Parser de `btrfs filesystem show` vs discos MISSING
//
// Con el FS montado y degradado, btrfs-progs imprime el disco ausente como:
//   "devid 2 size 0 used 0 path /dev/sdd MISSING"
// parseDevidLine debe reconocerlo como AUSENTE (nil o marcado missing), no
// devolver un device presente con Path="/dev/sdd MISSING". Si lo cuenta,
// DevicesOnline++ → DevicesMissing=0 → un RAID1 degradado sale "healthy".
// ─────────────────────────────────────────────────────────────────────────

func TestParseDevidLine_MissingSuffixNotCountedAsOnline(t *testing.T) {
	line := "devid 2 size 0 used 0 path /dev/sdd MISSING"
	dev := parseDevidLine(line)
	if dev != nil {
		t.Errorf("un devid con sufijo MISSING no debe contarse como device online; got Path=%q SizeBytes=%d",
			dev.Path, dev.SizeBytes)
	}
}

// Variante con --raw y by-id (formato que sí debe parsear bien — regresión).
func TestParseDevidLine_RawFormatStillParses(t *testing.T) {
	line := "devid 1 size 120033041920 used 2155872256 path /dev/sda"
	dev := parseDevidLine(line)
	if dev == nil {
		t.Fatal("línea devid válida no parseada")
	}
	if dev.Path != "/dev/sda" {
		t.Errorf("Path: got %q, want /dev/sda", dev.Path)
	}
	if dev.SizeBytes != 120033041920 {
		t.Errorf("SizeBytes: got %d, want 120033041920", dev.SizeBytes)
	}
}

// ─────────────────────────────────────────────────────────────────────────
// AUDIT-2 · computeUsableCapacity sobrestima en raid1c3 asimétrico
//
// raid1c3 = 3 copias, cada una en un disco DISTINTO. Con exactamente 3
// discos, cada disco guarda una copia de todo → usable = disco MENOR.
// Con [1000, 100, 100]: usable real = 100. La fórmula actual
// (min(suma/copias, suma−mayor)) devuelve 200 → TotalBytes 2× el real.
// ─────────────────────────────────────────────────────────────────────────

func TestComputeUsableCapacity_Raid1c3ThreeDisksAsymmetric(t *testing.T) {
	const gb = int64(1_000_000_000)
	sizes := []int64{1000 * gb, 100 * gb, 100 * gb}
	got := computeUsableCapacity(ProfileRaid1c3, sizes)
	want := 100 * gb
	if got != want {
		t.Errorf("raid1c3 [1000,100,100] GB: got %d GB usables, want %d GB (3 copias en 3 discos ⇒ limita el menor)",
			got/gb, want/gb)
	}
}

// Casos que la fórmula actual SÍ clava (regresión, deben seguir verdes).
func TestComputeUsableCapacity_KnownGoodCases(t *testing.T) {
	const gb = int64(1_000_000_000)
	cases := []struct {
		name    string
		profile Profile
		sizes   []int64
		want    int64
	}{
		{"raid1 2 discos asimétricos (120+320)", ProfileRaid1, []int64{120 * gb, 320 * gb}, 120 * gb},
		{"raid1 3 discos (500+500+1000)", ProfileRaid1, []int64{500 * gb, 500 * gb, 1000 * gb}, 1000 * gb},
		{"raid1c3 3 discos balanceados", ProfileRaid1c3, []int64{500 * gb, 500 * gb, 500 * gb}, 500 * gb},
		{"raid10 4 discos asimétricos (1000+100+100+100)", ProfileRaid10, []int64{1000 * gb, 100 * gb, 100 * gb, 100 * gb}, 300 * gb},
		{"single suma todo", ProfileSingle, []int64{100 * gb, 200 * gb}, 300 * gb},
	}
	for _, c := range cases {
		if got := computeUsableCapacity(c.profile, c.sizes); got != c.want {
			t.Errorf("%s: got %d GB, want %d GB", c.name, got/gb, c.want/gb)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────
// AUDIT-3 · computePoolUsage mezcla bytes RAW con bytes usables
//
// `btrfs filesystem usage -b` en la sección Overall:
//   Used:               ← bytes RAW (cuenta las N copias; ratio 2.0 en RAID1)
//   Free (statfs, df):  ← bytes USABLES (1 copia)
// Verificado en vivo (pool data1): Used=25.306.505.216 raw con solo
// ~12,4 GB de datos reales. La UI muestra el doble de uso.
//
// computePoolUsage llama a runSafe directamente (no inyectable), así que el
// fix debe extraer el parseo a una función pura — este test define su
// contrato y NO COMPILARÁ hasta que exista (está desactivado con build tag
// mental: descomentar al hacer el fix F1):
//
//	func parseBtrfsUsageOutput(out string) (usedRaw, freeStatfs, dataRatio ...)
//
// Mientras tanto, dejamos aquí la salida real capturada del sistema como
// fixture para el fix:
// ─────────────────────────────────────────────────────────────────────────

const btrfsUsageFixtureData1 = `Overall:
    Device size:		     8001574060032
    Device allocated:		       32279363584
    Device unallocated:		     7969294696448
    Device missing:		                 0
    Device slack:		                 0
    Used:			       25306505216
    Free (estimated):		     3986206183424	(min: 3986206183424)
    Free (statfs, df):		     3986205110272
    Data ratio:			              2.00
    Metadata ratio:		              2.00
    Global reserve:		          29261824	(used: 0)
    Multiple profiles:		                no`

// TestParseBtrfsUsageOverall_RealFixture verifica el contrato del fix F1
// contra la salida REAL capturada del pool data1: el uso servido debe ser
// ~12,65 GB (usable = raw/ratio), NO los 25,3 GB raw que doblaban la cifra.
func TestParseBtrfsUsageOverall_RealFixture(t *testing.T) {
	ov := parseBtrfsUsageOverall(btrfsUsageFixtureData1)

	if ov.UsedRaw != 25306505216 {
		t.Errorf("UsedRaw: got %d, want 25306505216", ov.UsedRaw)
	}
	if ov.FreeStatfs != 3986205110272 {
		t.Errorf("FreeStatfs: got %d, want 3986205110272", ov.FreeStatfs)
	}
	if ov.DataRatio != 2.0 {
		t.Errorf("DataRatio: got %v, want 2.0", ov.DataRatio)
	}
	if got, want := ov.usableUsedBytes(), int64(12653252608); got != want {
		t.Errorf("usableUsedBytes: got %d (%.1f GB), want %d (la mitad del raw en RAID1)",
			got, float64(got)/1e9, want)
	}
}

// Sin línea "Free (statfs, df)" (btrfs-progs < 6.3) el parser debe dejar
// FreeStatfs=0 para que computePoolUsage caiga al fallback de df.
func TestParseBtrfsUsageOverall_OldProgsWithoutStatfsLine(t *testing.T) {
	out := `Overall:
    Device size:		     8001574060032
    Used:			       25306505216
    Free (estimated):		     3986206183424	(min: 3986206183424)
    Data ratio:			              2.00`
	ov := parseBtrfsUsageOverall(out)
	if ov.FreeStatfs != 0 {
		t.Errorf("FreeStatfs sin línea statfs: got %d, want 0 (activa fallback df)", ov.FreeStatfs)
	}
	if ov.usableUsedBytes() != 12653252608 {
		t.Errorf("usableUsedBytes: got %d, want 12653252608", ov.usableUsedBytes())
	}
}

// Sin "Data ratio" legible, mejor servir el raw (sobrestimar) que inventar.
func TestParseBtrfsUsageOverall_NoRatioServesRaw(t *testing.T) {
	ov := parseBtrfsUsageOverall("Used:  1000\n")
	if ov.usableUsedBytes() != 1000 {
		t.Errorf("sin ratio: got %d, want 1000 (raw tal cual)", ov.usableUsedBytes())
	}
}

// ─────────────────────────────────────────────────────────────────────────
// AUDIT F4 · Compresión honesta (Fase 2)
//
// Tres piezas descoordinadas mentían: fstab hardcodeaba compress=zstd para
// todos los pools, SetPoolCompression solo escribía la BD, y SOT-05 leía
// `btrfs property get` (ciego a la opción de montaje). Verificado en vivo:
// pool data1 montado compress=zstd:3, property vacía, BD/UI "none".
// ─────────────────────────────────────────────────────────────────────────

func TestFstabOptsForPool_DerivesFromCompression(t *testing.T) {
	cases := []struct {
		name string
		comp string
		want string
	}{
		{"none no añade compress", "none", "defaults,nofail,noatime"},
		{"vacío no añade compress", "", "defaults,nofail,noatime"},
		{"zstd:3 se refleja", "zstd:3", "defaults,nofail,noatime,compress=zstd:3"},
		{"lzo se refleja", "lzo", "defaults,nofail,noatime,compress=lzo"},
		// Anti-inyección: un valor no whitelisteado JAMÁS llega a fstab.
		{"basura se omite", "zstd,exec=/bin/sh", "defaults,nofail,noatime"},
	}
	for _, c := range cases {
		p := &Pool{Name: "t", Compression: c.comp}
		if got := fstabOptsForPool(p); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestValidCompressionAlgo(t *testing.T) {
	valid := []string{"none", "lzo", "zlib", "zstd", "zstd:1", "zstd:15", "zlib:9"}
	for _, a := range valid {
		if !validCompressionAlgo(a) {
			t.Errorf("%q debería ser válido", a)
		}
	}
	invalid := []string{"", "ZSTD", "zstd:0", "zstd:16", "zlib:10", "gzip",
		"zstd,exec=/bin/sh", "zstd:3,ro", "zstd 3", "none;rm -rf /"}
	for _, a := range invalid {
		if validCompressionAlgo(a) {
			t.Errorf("%q debería ser inválido", a)
		}
	}
}

func TestCompressionFromMountOpts(t *testing.T) {
	cases := map[string]string{
		// Las opciones reales del pool data1 el día de la auditoría:
		"rw,noatime,compress=zstd:3,space_cache=v2,subvolid=5,subvol=/": "zstd:3",
		"rw,noatime,space_cache=v2":                                    "none",
		"rw,compress=no,space_cache=v2":                                "none",
		"rw,compress-force=lzo":                                        "lzo",
		"rw,compress=zstd":                                             "zstd",
	}
	for opts, want := range cases {
		if got := compressionFromMountOpts(opts); got != want {
			t.Errorf("opts %q: got %q, want %q", opts, got, want)
		}
	}
}

func TestMountOptForCompression(t *testing.T) {
	// El kernel necesita compress=no explícito para desactivar en remount.
	if got := mountOptForCompression("none"); got != "compress=no" {
		t.Errorf("none: got %q, want compress=no", got)
	}
	if got := mountOptForCompression("zstd:5"); got != "compress=zstd:5" {
		t.Errorf("zstd:5: got %q, want compress=zstd:5", got)
	}
}

// El setter debe rechazar algoritmos fuera de la whitelist ANTES de tocar
// nada (antes aceptaba cualquier string y lo persistía).
func TestSetPoolCompression_RejectsInvalidAlgorithm(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.CreatePool(ctx, tx, &Pool{
		ID: "p1", Name: "data", BtrfsUUID: "u1",
		Profile: ProfileSingle, MountPoint: "/m",
	})
	service.repo.SetPoolCapabilities(ctx, tx, "p1", []string{"compression"})
	tx.Commit()

	if _, err := service.SetPoolCompression(ctx, "p1", "zstd,exec=/bin/sh"); err == nil {
		t.Fatal("algoritmo con inyección de opciones aceptado — debe rechazarse")
	}
	pool, _ := service.GetPool(ctx, "p1")
	if pool.Compression != "" && pool.Compression != "none" {
		t.Errorf("la compresión no debe haber cambiado; got %q", pool.Compression)
	}
}

// ─────────────────────────────────────────────────────────────────────────
// AUDIT F8 · Scrub status con progreso en vivo
//
// La UI mostraba "Último scrub: —" hardcodeado y ningún progreso: el
// parser ignoraba "Bytes scrubbed: X (Y%)", "Time left:" y "ETA:".
// ─────────────────────────────────────────────────────────────────────────

func TestParseScrubStatus_Running(t *testing.T) {
	out := `UUID:             cb0163d6-1d87-4074-9021-90b5c05e42f9
Scrub started:    Fri Jul  4 10:00:00 2026
Status:           running
Duration:         0:05:12
Time left:        0:47:21
ETA:              Fri Jul  4 10:52:33 2026
Total to scrub:   11.78GiB
Bytes scrubbed:   1.50GiB  (12.73%)
Rate:             98.34MiB/s
Error summary:    no errors found`

	r := parseScrubStatusOutput(out)
	if r["status"] != "scrubbing" {
		t.Errorf("status: got %v, want scrubbing", r["status"])
	}
	if pct, ok := r["progress"].(float64); !ok || pct != 12.73 {
		t.Errorf("progress: got %v, want 12.73", r["progress"])
	}
	if r["bytesScrubbed"] != "1.50GiB" {
		t.Errorf("bytesScrubbed: got %v", r["bytesScrubbed"])
	}
	if r["timeLeft"] != "0:47:21" {
		t.Errorf("timeLeft: got %v", r["timeLeft"])
	}
	if r["lastScrub"] == nil {
		t.Error("lastScrub debe traer la fecha de inicio del scrub en curso")
	}
}

func TestParseScrubStatus_FinishedWithErrors(t *testing.T) {
	out := `UUID:             cb0163d6-1d87-4074-9021-90b5c05e42f9
Scrub started:    Thu Jul  3 22:00:00 2026
Status:           finished
Duration:         0:32:11
Total to scrub:   11.78GiB
Rate:             99.99MiB/s
Error summary:    csum=3 verify=1`

	r := parseScrubStatusOutput(out)
	if r["status"] != "done" {
		t.Errorf("status: got %v, want done", r["status"])
	}
	if r["errors"] != 4 {
		t.Errorf("errors: got %v, want 4 (csum=3 + verify=1)", r["errors"])
	}
	if r["lastScrub"] == nil {
		t.Error("lastScrub nil en scrub terminado")
	}
	if r["lastDuration"] != "0:32:11" {
		t.Errorf("lastDuration: got %v", r["lastDuration"])
	}
}

func TestParseScrubStatus_NeverRun(t *testing.T) {
	r := parseScrubStatusOutput("scrub status for /nimos/pools/data1: no stats available")
	if r["status"] != "never" {
		t.Errorf("status: got %v, want never", r["status"])
	}
}

// Cazado EN VIVO (2026-07-04) al verificar F8: en los primeros segundos de
// un scrub, btrfs imprime "no stats available" JUNTO a las líneas de
// progreso (la cabecera de stats aún no existe). El parser devolvía
// "never" con el scrub corriendo. Fixture literal del sistema.
func TestParseScrubStatus_JustStartedTransient(t *testing.T) {
	out := `UUID:             cb0163d6-1d87-4074-9021-90b5c05e42f9
	no stats available
Time left:        0:00:00
ETA:              Sat Jul  4 01:22:34 2026
Total to scrub:   23.57GiB
Bytes scrubbed:   0.00B  (0.00%)
Rate:             0.00B/s
Error summary:    no errors found`

	r := parseScrubStatusOutput(out)
	if r["status"] != "scrubbing" {
		t.Errorf("status: got %v, want scrubbing (scrub recién arrancado, no 'never')", r["status"])
	}
}

// ─────────────────────────────────────────────────────────────────────────
// AUDIT F10 · Wipe con identidad + preflight FS en add/replace
// ─────────────────────────────────────────────────────────────────────────

// El wipe era la única op destructiva direccionada solo por /dev/sdX: una
// renumeración entre listar y confirmar podía borrar el disco equivocado.
func TestVerifyWipeTargetIdentity(t *testing.T) {
	orig := readDeviceSerial
	defer func() { readDeviceSerial = orig }()

	readDeviceSerial = func(string) string { return "SERIAL-REAL" }
	if err := verifyWipeTargetIdentity("/dev/sdb", "SERIAL-REAL"); err != nil {
		t.Errorf("serial coincidente debe pasar: %v", err)
	}
	if err := verifyWipeTargetIdentity("/dev/sdb", "OTRO-SERIAL"); err == nil {
		t.Error("serial distinto DEBE bloquear el wipe (disco renumerado)")
	}
	readDeviceSerial = func(string) string { return "" }
	if err := verifyWipeTargetIdentity("/dev/sdb", "SERIAL-REAL"); err == nil {
		t.Error("serial ilegible con identidad esperada DEBE bloquear (no se puede confirmar)")
	}
	// Cliente antiguo sin serial: se permite (compat), solo loguea.
	if err := verifyWipeTargetIdentity("/dev/sdb", ""); err != nil {
		t.Errorf("sin serial esperado no debe bloquear: %v", err)
	}
}

// AddDevice sin wipe_first debe avisar si el disco trae un filesystem.
func TestAddDevice_PreflightBlocksDiskWithFilesystem(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	poolID, _ := createTestPool(t, service, ctx, "data", ProfileRaid1, 2)
	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.UpsertDevice(ctx, tx, &Device{
		ID: "extra", Serial: "E1",
		ByIDPath: "/dev/disk/by-id/e1", CurrentPath: "/dev/sde",
		SizeBytes: 1e12,
	})
	tx.Commit()

	service.deviceChecker = func(devices []*Device) error {
		return &ErrDiskHasFilesystem{Disk: "/dev/sde", FSType: "ext4"}
	}

	_, err := service.AddDevice(ctx, AddDeviceRequest{PoolID: poolID, DeviceID: "extra"})
	if err == nil {
		t.Fatal("disco con filesystem sin wipe_first debe rechazarse")
	}
	if _, ok := err.(*ErrDiskHasFilesystem); !ok {
		t.Errorf("debe fluir *ErrDiskHasFilesystem (para la UI); got %T: %v", err, err)
	}

	// Con wipe_first el usuario ya consintió: el preflight se salta.
	service.deviceChecker = func([]*Device) error {
		t.Error("con wipe_first NO debe ejecutarse el preflight")
		return nil
	}
	op, err := service.AddDevice(ctx, AddDeviceRequest{PoolID: poolID, DeviceID: "extra", WipeFirst: true})
	if err != nil {
		t.Fatalf("AddDevice con wipe_first: %v", err)
	}
	waitForOperation(t, service, ctx, op.ID, 3*time.Second)
}

// ReplaceDevice debe avisar si el disco NUEVO trae un filesystem (el
// executor usa `replace start -f`, que lo machacaría sin preguntar).
func TestReplaceDevice_PreflightBlocksNewDiskWithFilesystem(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	poolID, deviceIDs := createTestPool(t, service, ctx, "data", ProfileRaid1, 2)
	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.UpsertDevice(ctx, tx, &Device{
		ID: "new-1", Serial: "NEW-1",
		ByIDPath: "/dev/disk/by-id/new-1", CurrentPath: "/dev/sdn",
		SizeBytes: 2e12,
	})
	tx.Commit()

	service.deviceChecker = func(devices []*Device) error {
		if len(devices) != 1 || devices[0].ID != "new-1" {
			t.Errorf("el preflight debe evaluar SOLO el disco nuevo; got %+v", devices)
		}
		return &ErrDiskHasFilesystem{Disk: "/dev/sdn", FSType: "ntfs"}
	}

	_, err := service.ReplaceDevice(ctx, ReplaceDeviceRequest{
		PoolID: poolID, OldDeviceID: deviceIDs[0], NewDeviceID: "new-1",
	})
	if err == nil {
		t.Fatal("disco nuevo con filesystem sin force debe rechazarse")
	}
	if _, ok := err.(*ErrDiskHasFilesystem); !ok {
		t.Errorf("debe fluir *ErrDiskHasFilesystem; got %T: %v", err, err)
	}

	// Con force (usuario avisado por la UI) procede.
	origScrub := startScrubOnPool
	defer func() { startScrubOnPool = origScrub }()
	startScrubOnPool = func(string, string) error { return nil }
	service.deviceChecker = noopDeviceChecker
	op, err := service.ReplaceDevice(ctx, ReplaceDeviceRequest{
		PoolID: poolID, OldDeviceID: deviceIDs[0], NewDeviceID: "new-1", Force: true,
	})
	if err != nil {
		t.Fatalf("ReplaceDevice con force: %v", err)
	}
	final := waitForOperation(t, service, ctx, op.ID, 3*time.Second)
	if final.Status != OpStatusCompleted {
		t.Errorf("op.Status: got %q", final.Status)
	}
}

// ─────────────────────────────────────────────────────────────────────────
// AUDIT F11 · fstab derivado de la BD en create y destroy
//
// Antes: CreatePool usaba appendFstab (skip silencioso si el mountpoint ya
// figuraba — heredando el UUID de un pool destruido homónimo — y con
// compress=zstd hardcodeado) y DestroyPool NO tocaba fstab (entrada
// huérfana hasta el siguiente boot).
// ─────────────────────────────────────────────────────────────────────────

func TestCreateAndDestroyPool_RegenerateFstabFromDB(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	syncCalls := 0
	orig := syncFstabFromDBFn
	syncFstabFromDBFn = func(context.Context) error { syncCalls++; return nil }
	defer func() { syncFstabFromDBFn = orig }()

	poolID, _ := createTestPool(t, service, ctx, "data", ProfileRaid1, 2)
	if syncCalls != 1 {
		t.Errorf("CreatePool debe regenerar fstab desde la BD: got %d syncs, want 1", syncCalls)
	}

	op, err := service.DestroyPool(ctx, poolID)
	if err != nil {
		t.Fatalf("DestroyPool: %v", err)
	}
	if op.Status != OpStatusCompleted {
		t.Fatalf("destroy op: %q", op.Status)
	}
	if syncCalls != 2 {
		t.Errorf("DestroyPool debe limpiar su entrada de fstab: got %d syncs, want 2", syncCalls)
	}
}
