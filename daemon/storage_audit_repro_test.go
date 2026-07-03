// storage_audit_repro_test.go — Tests-trampa de la auditoría de storage
// (2026-07-03). Cada test documenta una mentira detectada en la auditoría:
// un test en rojo aquí = bug CONFIRMADO pendiente de fix. Cuando se aplique
// el fix correspondiente, el test pasa a verde y se queda como regresión.
//
// Referencia completa: ~/storage-audit-2026-07-03.md
package main

import "testing"

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
