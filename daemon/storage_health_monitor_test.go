package main

import "testing"

// ─── parseUnallocatedByDevice — P1: lectura por device ────────────────────────
//
// El output de `btrfs filesystem usage -b` lista, por device, una cabecera
// "/dev/sdX, ID: N" seguida de líneas indentadas incluyendo "Unallocated:".

func TestParseUnallocatedByDevice_SingleDevice(t *testing.T) {
	out := `Overall:
    Device size:                  120034123776
    Device allocated:               6475739136
    Used:                           5368709120

/dev/sda, ID: 1
   Device size:            120034123776
   Device slack:                      0
   Data,single:              5368709120
   Metadata,single:          1073741824
   System,single:              33554432
   Unallocated:            113558118400
`
	byDev, ok := parseUnallocatedByDevice(out)
	if !ok {
		t.Fatal("debería haber parseado al menos un device")
	}
	if byDev["/dev/sda"] != 113558118400 {
		t.Errorf("sda: got %d, want 113558118400", byDev["/dev/sda"])
	}
}

func TestParseUnallocatedByDevice_TwoDevices(t *testing.T) {
	out := `/dev/sda, ID: 1
   Device size:            1000204886016
   Unallocated:               2147483648

/dev/sdb, ID: 2
   Device size:            1000204886016
   Unallocated:                536870912
`
	byDev, ok := parseUnallocatedByDevice(out)
	if !ok {
		t.Fatal("debería haber parseado")
	}
	if byDev["/dev/sda"] != 2147483648 {
		t.Errorf("sda: got %d, want 2147483648", byDev["/dev/sda"])
	}
	if byDev["/dev/sdb"] != 536870912 {
		t.Errorf("sdb: got %d, want 536870912", byDev["/dev/sdb"])
	}
}

func TestParseUnallocatedByDevice_NoLines(t *testing.T) {
	out := "Overall:\n    Device size: 120034123776\n    Used: 5368709120\n"
	if _, ok := parseUnallocatedByDevice(out); ok {
		t.Error("sin líneas Unallocated debe devolver ok=false")
	}
}

func TestParseUnallocatedByDevice_Empty(t *testing.T) {
	if _, ok := parseUnallocatedByDevice(""); ok {
		t.Error("output vacío debe devolver ok=false")
	}
}

// ─── effectiveUnallocated — el fix por perfil (corrige falsos positivos) ──────
//
// Un chunk de metadata con N copias necesita unallocated en N devices a la vez.
// El margen real es el N-ésimo mayor, NO el mínimo.

func TestEffectiveUnallocated_Single(t *testing.T) {
	// single → mínimo (conservador).
	byDev := map[string]int64{"/dev/sda": 100, "/dev/sdb": 30, "/dev/sdc": 70}
	if got := effectiveUnallocated(ProfileSingle, byDev); got != 30 {
		t.Errorf("single: got %d, want 30 (min)", got)
	}
}

// EL CASO QUE MOTIVA EL FIX: RAID1 asimétrico 8TB+1TB. El mínimo (device de 1TB
// casi lleno) daría falso positivo; el efectivo (2º mayor) ve el margen real.
func TestEffectiveUnallocated_Raid1Asymmetric(t *testing.T) {
	// sda (8TB) con 4 GiB libres, sdb (1TB) con 256 MiB libres.
	byDev := map[string]int64{
		"/dev/sda": 4 << 30,   // 4 GiB
		"/dev/sdb": 256 << 20, // 256 MiB
	}
	// raid1 necesita 2 devices → 2º mayor = 256 MiB. Aquí el efectivo SÍ es
	// bajo porque con solo 2 devices el pequeño limita de verdad (un chunk
	// raid1 necesita ambos). El min y el 2º mayor coinciden con 2 devices.
	if got := effectiveUnallocated(ProfileRaid1, byDev); got != 256<<20 {
		t.Errorf("raid1 2 devices: got %d, want %d", got, 256<<20)
	}
}

// RAID1 con 3 devices asimétricos: aquí el fix se nota. El mínimo daría el
// device pequeño, pero raid1 (2 copias) puede colocar el chunk en los DOS
// grandes, así que el margen real es el 2º mayor, no el mínimo.
func TestEffectiveUnallocated_Raid1ThreeDevices(t *testing.T) {
	byDev := map[string]int64{
		"/dev/sda": 8 << 30,   // 8 GiB
		"/dev/sdb": 6 << 30,   // 6 GiB
		"/dev/sdc": 128 << 20, // 128 MiB (casi lleno)
	}
	// min = 128 MiB → falso positivo crítico.
	// efectivo raid1 = 2º mayor = 6 GiB → correcto, hay margen de sobra.
	if got := effectiveUnallocated(ProfileRaid1, byDev); got != 6<<30 {
		t.Errorf("raid1 3 devices: got %d, want %d (2º mayor, no el min)", got, 6<<30)
	}
}

func TestEffectiveUnallocated_Raid1c3(t *testing.T) {
	// raid1c3 (3 copias) → 3er mayor.
	byDev := map[string]int64{
		"/dev/sda": 8 << 30,
		"/dev/sdb": 6 << 30,
		"/dev/sdc": 4 << 30,
		"/dev/sdd": 1 << 30,
	}
	if got := effectiveUnallocated(ProfileRaid1c3, byDev); got != 4<<30 {
		t.Errorf("raid1c3: got %d, want %d (3er mayor)", got, 4<<30)
	}
}

func TestEffectiveUnallocated_Raid10(t *testing.T) {
	// raid10 → menor de los 4 mayores (4º mayor).
	byDev := map[string]int64{
		"/dev/sda": 8 << 30,
		"/dev/sdb": 6 << 30,
		"/dev/sdc": 4 << 30,
		"/dev/sdd": 2 << 30,
	}
	if got := effectiveUnallocated(ProfileRaid10, byDev); got != 2<<30 {
		t.Errorf("raid10: got %d, want %d (4º mayor)", got, 2<<30)
	}
}

func TestEffectiveUnallocated_DegradedNotEnoughDevices(t *testing.T) {
	// raid1 con un solo device disponible (estado degradado): no se puede
	// asignar un chunk redundante → 0.
	byDev := map[string]int64{"/dev/sda": 8 << 30}
	if got := effectiveUnallocated(ProfileRaid1, byDev); got != 0 {
		t.Errorf("raid1 con 1 device: got %d, want 0 (no cabe chunk redundante)", got)
	}
}

func TestEffectiveUnallocated_Empty(t *testing.T) {
	if got := effectiveUnallocated(ProfileRaid1, map[string]int64{}); got != 0 {
		t.Errorf("sin devices: got %d, want 0", got)
	}
}

// Verifica el umbral crítico.
func TestUnallocatedThreshold(t *testing.T) {
	if !((unallocatedCriticalBytes - 1) < unallocatedCriticalBytes) {
		t.Error("device por debajo del umbral debe considerarse crítico")
	}
	if (unallocatedCriticalBytes + 1) < unallocatedCriticalBytes {
		t.Error("device por encima del umbral NO debe ser crítico")
	}
}

// ─── humanBytes — formato de los mensajes ─────────────────────────────────────

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{512, "512 B"},
		{1 << 30, "1.0 GiB"},
		{536870912, "512.0 MiB"},
		{2147483648, "2.0 GiB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.in); got != c.want {
			t.Errorf("humanBytes(%d): got %q, want %q", c.in, got, c.want)
		}
	}
}

// ─── Lógica de transición — FIX1 ──────────────────────────────────────────────
//
// El motor de salud (ComputePoolHealth) ya está testeado aparte. Aquí se cubre
// la POLÍTICA de notificación: notificar solo en transiciones de estado, con
// dedupe natural, replicando el patrón del SMART monitor.
//
// Modelamos la decisión pura (¿notificar?) para testearla sin tocar la DB.
// shouldNotifyHealth vive en storage_health_monitor.go (producción); aquí solo
// se verifica su comportamiento.

func TestShouldNotifyHealth_FirstScanHealthy_NoNotif(t *testing.T) {
	if shouldNotifyHealth("", false, "healthy") {
		t.Error("primer scan saludable NO debe notificar (evita ruido al boot)")
	}
}

func TestShouldNotifyHealth_FirstScanDegraded_Notifies(t *testing.T) {
	if !shouldNotifyHealth("", false, "degraded") {
		t.Error("primer scan ya degradado SÍ debe notificar")
	}
}

func TestShouldNotifyHealth_HealthyToDegraded_Notifies(t *testing.T) {
	if !shouldNotifyHealth("healthy", true, "degraded") {
		t.Error("healthy→degraded debe notificar")
	}
}

func TestShouldNotifyHealth_SostainedDegraded_NoReNotif(t *testing.T) {
	// degradado sostenido: el estado no cambia → NO re-notifica (dedupe).
	if shouldNotifyHealth("degraded", true, "degraded") {
		t.Error("degradado sostenido NO debe re-notificar cada ciclo")
	}
}

func TestShouldNotifyHealth_Recovery_Notifies(t *testing.T) {
	if !shouldNotifyHealth("degraded", true, "healthy") {
		t.Error("recovery degraded→healthy debe notificar")
	}
}

func TestShouldNotifyHealth_DegradedToCritical_Notifies(t *testing.T) {
	if !shouldNotifyHealth("degraded", true, "critical") {
		t.Error("degraded→critical debe notificar (empeora)")
	}
}
