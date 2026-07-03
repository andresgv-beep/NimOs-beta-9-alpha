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
