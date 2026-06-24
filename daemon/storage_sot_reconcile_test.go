package main

import "testing"

// ─────────────────────────────────────────────────────────────────────────────
// SOT-01 · reconcilePoolProfileWithReality
// Regla 16: BTRFS es la autoridad del profile. La función sirve el valor real
// cuando la BD diverge, y respeta la BD cuando no hay verdad fiable.
// ─────────────────────────────────────────────────────────────────────────────

// withStubRealState inyecta un readRealPoolStateFn fijo durante el test.
func withStubRealState(t *testing.T, st RealPoolState) {
	t.Helper()
	orig := readRealPoolStateFn
	readRealPoolStateFn = func(string) RealPoolState { return st }
	t.Cleanup(func() { readRealPoolStateFn = orig })
}

func TestReconcileProfile_RealWins_WhenDiverges(t *testing.T) {
	// BD dice single, BTRFS dice raid1 → debe servir raid1 (el caso real de hoy)
	withStubRealState(t, RealPoolState{Profile: "raid1", DevicePaths: []string{"/dev/sda", "/dev/sdb"}, OK: true})

	p := &Pool{Name: "data8", MountPoint: "/nimos/pools/data8", Profile: ProfileSingle,
		Devices: []Device{{}, {}}}
	reconcilePoolProfileWithReality(p)

	if p.Profile != ProfileRaid1 {
		t.Errorf("profile servido: got %q, want raid1 (la realidad manda)", p.Profile)
	}
}

func TestReconcileProfile_NoChange_WhenMatches(t *testing.T) {
	// BD y realidad coinciden → no toca nada
	withStubRealState(t, RealPoolState{Profile: "raid1", DevicePaths: []string{"/dev/sda", "/dev/sdb"}, OK: true})

	p := &Pool{Name: "data8", MountPoint: "/nimos/pools/data8", Profile: ProfileRaid1,
		Devices: []Device{{}, {}}}
	reconcilePoolProfileWithReality(p)

	if p.Profile != ProfileRaid1 {
		t.Errorf("profile: got %q, want raid1 (sin cambio)", p.Profile)
	}
}

func TestReconcileProfile_RespectsDsB_WhenRealUnreadable(t *testing.T) {
	// CRÍTICO: si BTRFS no responde (OK=false), NO inventar — respetar la BD.
	withStubRealState(t, RealPoolState{OK: false})

	p := &Pool{Name: "data8", MountPoint: "/nimos/pools/data8", Profile: ProfileSingle,
		Devices: []Device{{}}}
	reconcilePoolProfileWithReality(p)

	if p.Profile != ProfileSingle {
		t.Errorf("profile: got %q, want single (sin lectura fiable, respetar BD)", p.Profile)
	}
}

func TestReconcileProfile_RespectsDB_WhenRealProfileEmpty(t *testing.T) {
	// OK=true pero profile vacío (parse falló) → no reconciliar.
	withStubRealState(t, RealPoolState{Profile: "", OK: true})

	p := &Pool{Name: "data8", MountPoint: "/nimos/pools/data8", Profile: ProfileRaid10,
		Devices: []Device{{}, {}, {}, {}}}
	reconcilePoolProfileWithReality(p)

	if p.Profile != ProfileRaid10 {
		t.Errorf("profile: got %q, want raid10 (profile real vacío, respetar BD)", p.Profile)
	}
}

func TestReconcileProfile_NoMountPoint_NoOp(t *testing.T) {
	// Pool sin mountpoint → no hay realidad que leer, no crashea.
	p := &Pool{Name: "x", MountPoint: "", Profile: ProfileSingle}
	reconcilePoolProfileWithReality(p) // no debe panic
	if p.Profile != ProfileSingle {
		t.Errorf("profile: got %q, want single", p.Profile)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// SOT-05 · compression en vivo
// ─────────────────────────────────────────────────────────────────────────────

func TestReconcileCompression_RealWins_WhenDiverges(t *testing.T) {
	// BD dice none, BTRFS dice zstd:3 (alguien lo cambió por CLI) → servir real
	withStubRealState(t, RealPoolState{Profile: "raid1", Compression: "zstd:3", OK: true})

	p := &Pool{Name: "data8", MountPoint: "/nimos/pools/data8", Profile: ProfileRaid1,
		Compression: "none", Devices: []Device{{}, {}}}
	reconcilePoolProfileWithReality(p)

	if p.Compression != "zstd:3" {
		t.Errorf("compression: got %q, want zstd:3 (la realidad manda)", p.Compression)
	}
}

func TestReconcileCompression_EvaluatedEvenIfProfileMatches(t *testing.T) {
	// Profile coincide (no hay early-return que bloquee la compresión).
	withStubRealState(t, RealPoolState{Profile: "raid1", Compression: "lzo", OK: true})

	p := &Pool{Name: "data8", MountPoint: "/nimos/pools/data8", Profile: ProfileRaid1,
		Compression: "none", Devices: []Device{{}, {}}}
	reconcilePoolProfileWithReality(p)

	if p.Compression != "lzo" {
		t.Errorf("compression: got %q, want lzo (debe evaluarse aunque el profile coincida)", p.Compression)
	}
}

func TestReconcileCompression_RespectsDB_WhenRealEmpty(t *testing.T) {
	// Compresión real vacía (no leíble) → respetar la BD, no poner "".
	withStubRealState(t, RealPoolState{Profile: "raid1", Compression: "", OK: true})

	p := &Pool{Name: "data8", MountPoint: "/nimos/pools/data8", Profile: ProfileRaid1,
		Compression: "zstd:3", Devices: []Device{{}, {}}}
	reconcilePoolProfileWithReality(p)

	if p.Compression != "zstd:3" {
		t.Errorf("compression: got %q, want zstd:3 (real vacío, respetar BD)", p.Compression)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// SOT-06 · parseIntSafe (helper del health no pegajoso)
// ─────────────────────────────────────────────────────────────────────────────

func TestParseIntSafe(t *testing.T) {
	cases := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"0", 0, false},
		{"47", 47, false},
		{"138", 138, false},
		{"", 0, true},
		{"12x", 0, true},
		{"-5", 0, true}, // el signo no es dígito → error (los contadores btrfs no son negativos)
	}
	for _, c := range cases {
		got, err := parseIntSafe(c.in)
		if c.wantErr && err == nil {
			t.Errorf("parseIntSafe(%q): esperaba error", c.in)
		}
		if !c.wantErr && (err != nil || got != c.want) {
			t.Errorf("parseIntSafe(%q): got %d,%v want %d,nil", c.in, got, err, c.want)
		}
	}
}
