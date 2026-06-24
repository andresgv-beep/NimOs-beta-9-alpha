package main

// storage_wipe_test.go — Tests del preFlightCheck enriquecido (Bloque C2).
//
// Cubre:
//   · ErrDiskHasFilesystem implementa error y Code()
//   · ErrDiskHasFilesystem mensaje legible varía según contexto
//   · detectFilesystemOnDisk usa el observer si está disponible
//   · detectFilesystemOnDisk distingue managed vs orphan
//   · parseBlkidExport parsea correctamente

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ─── ErrDiskHasFilesystem ──────────────────────────────────────────────────

func TestErrDiskHasFilesystem_Code(t *testing.T) {
	e := &ErrDiskHasFilesystem{Disk: "/dev/sda", FSType: "btrfs"}
	if e.Code() != "DISK_HAS_FILESYSTEM" {
		t.Errorf("Code() = %q, want DISK_HAS_FILESYSTEM", e.Code())
	}
}

func TestErrDiskHasFilesystem_MessageForManagedPool(t *testing.T) {
	e := &ErrDiskHasFilesystem{
		Disk:      "/dev/sda",
		FSType:    "btrfs",
		IsManaged: true,
		PoolName:  "datos1",
	}
	msg := e.Error()
	if !strings.Contains(msg, "/dev/sda") {
		t.Errorf("message should mention disk path: %q", msg)
	}
	if !strings.Contains(msg, "datos1") {
		t.Errorf("message should mention pool name: %q", msg)
	}
	if !strings.Contains(msg, "managed") {
		t.Errorf("message should indicate managed status: %q", msg)
	}
}

func TestErrDiskHasFilesystem_MessageForOrphan(t *testing.T) {
	e := &ErrDiskHasFilesystem{
		Disk:    "/dev/sdb",
		FSType:  "btrfs",
		FSLabel: "OLD-POOL",
		FSUUID:  "1234-uuid",
	}
	msg := e.Error()
	if !strings.Contains(msg, "/dev/sdb") {
		t.Errorf("message should mention disk: %q", msg)
	}
	if !strings.Contains(msg, "OLD-POOL") {
		t.Errorf("message should mention label: %q", msg)
	}
}

func TestErrDiskHasFilesystem_MessageWithoutLabel(t *testing.T) {
	e := &ErrDiskHasFilesystem{
		Disk:   "/dev/sdc",
		FSType: "btrfs",
		FSUUID: "no-label-uuid",
	}
	msg := e.Error()
	if !strings.Contains(msg, "no-label-uuid") {
		t.Errorf("message should fall back to UUID: %q", msg)
	}
}

// ─── detectFilesystemOnDisk con observer ───────────────────────────────────

func TestDetectFilesystemOnDisk_NoObserver(t *testing.T) {
	// Sin observer + sin blkid disponible → nil (disco "limpio" o no determinable).
	savedObs := globalObserver
	defer func() { globalObserver = savedObs }()
	globalObserver = nil

	// /tmp/no-existe-disco no tendrá blkid output → debe devolver nil
	err := detectFilesystemOnDisk("/tmp/no-existe-disco-test")
	if err != nil {
		t.Errorf("expected nil for non-existent device, got: %v", err)
	}
}

func TestDetectFilesystemOnDisk_ObserverDetectsManaged(t *testing.T) {
	savedObs := globalObserver
	defer func() { globalObserver = savedObs }()

	// Construir observer con snapshot que ya tiene el FS
	o := NewStorageObserver(1 * time.Hour)
	o.snapshot.Store(&ObservedSnapshot{
		Generation: 1,
		Timestamp:  time.Now(),
		Filesystems: []ObservedBtrfs{
			{
				UUID:              "test-uuid-managed",
				Label:             "DATOS1",
				Profile:           "raid1",
				IsManaged:         true,
				ManagedPoolID:     "pool-id-abc",
				ManagedPoolName:   "datos1",
				ObservationHealth: HealthHealthy,
				SizeBytes:         100_000_000_000,
				UsedBytes:         500_000,
				LastSeen:          time.Now().UTC(),
				Devices: []ObservedDevice{
					{Path: "/dev/sda"},
					{Path: "/dev/sdb"},
				},
			},
		},
	})
	o.generation.Store(1)
	globalObserver = o

	err := detectFilesystemOnDisk("/dev/sda")
	if err == nil {
		t.Fatal("expected ErrDiskHasFilesystem for managed disk")
	}
	fsErr, ok := err.(*ErrDiskHasFilesystem)
	if !ok {
		t.Fatalf("expected *ErrDiskHasFilesystem, got %T", err)
	}
	if !fsErr.IsManaged {
		t.Error("IsManaged should be true")
	}
	if fsErr.PoolName != "datos1" {
		t.Errorf("PoolName = %q, want datos1", fsErr.PoolName)
	}
	if fsErr.FSUUID != "test-uuid-managed" {
		t.Errorf("FSUUID = %q, want test-uuid-managed", fsErr.FSUUID)
	}
	if fsErr.ObservationHealth != HealthHealthy {
		t.Errorf("ObservationHealth = %q, want healthy", fsErr.ObservationHealth)
	}
}

func TestDetectFilesystemOnDisk_ObserverDetectsOrphan(t *testing.T) {
	savedObs := globalObserver
	defer func() { globalObserver = savedObs }()

	o := NewStorageObserver(1 * time.Hour)
	o.snapshot.Store(&ObservedSnapshot{
		Generation: 1,
		Timestamp:  time.Now(),
		Filesystems: []ObservedBtrfs{
			{
				UUID:              "orphan-uuid",
				Label:             "abandoned",
				Profile:           "single",
				IsManaged:         false, // ← orphan
				ObservationHealth: HealthHealthy,
				Devices: []ObservedDevice{
					{Path: "/dev/sdz"},
				},
			},
		},
	})
	o.generation.Store(1)
	globalObserver = o

	err := detectFilesystemOnDisk("/dev/sdz")
	if err == nil {
		t.Fatal("expected ErrDiskHasFilesystem for orphan FS")
	}
	fsErr := err.(*ErrDiskHasFilesystem)
	if fsErr.IsManaged {
		t.Error("IsManaged should be false for orphan")
	}
	if fsErr.PoolName != "" {
		t.Errorf("PoolName should be empty for orphan, got %q", fsErr.PoolName)
	}
	if fsErr.FSLabel != "abandoned" {
		t.Errorf("FSLabel = %q, want abandoned", fsErr.FSLabel)
	}
}

func TestDetectFilesystemOnDisk_ObserverCleanDisk(t *testing.T) {
	savedObs := globalObserver
	defer func() { globalObserver = savedObs }()

	// Observer con un FS, pero el disco que preguntamos NO está en él
	o := NewStorageObserver(1 * time.Hour)
	o.snapshot.Store(&ObservedSnapshot{
		Generation: 1,
		Timestamp:  time.Now(),
		Filesystems: []ObservedBtrfs{
			{
				UUID:    "some-fs",
				Devices: []ObservedDevice{{Path: "/dev/sda"}},
			},
		},
	})
	o.generation.Store(1)
	globalObserver = o

	err := detectFilesystemOnDisk("/dev/sdx") // disco no en ningún FS
	if err != nil {
		t.Errorf("expected nil for clean disk, got: %v", err)
	}
}

// ─── parseBlkidExport ──────────────────────────────────────────────────────

func TestParseBlkidExport(t *testing.T) {
	out := `DEVNAME=/dev/sdb
UUID=12345-67890
TYPE=ext4
LABEL=mydata`

	parsed := parseBlkidExport(out)
	if parsed["TYPE"] != "ext4" {
		t.Errorf("TYPE = %q, want ext4", parsed["TYPE"])
	}
	if parsed["UUID"] != "12345-67890" {
		t.Errorf("UUID = %q, want 12345-67890", parsed["UUID"])
	}
	if parsed["LABEL"] != "mydata" {
		t.Errorf("LABEL = %q, want mydata", parsed["LABEL"])
	}
}

func TestParseBlkidExport_Empty(t *testing.T) {
	parsed := parseBlkidExport("")
	if len(parsed) != 0 {
		t.Errorf("expected empty map, got %d entries", len(parsed))
	}
}

// ─── notifyStorageChanged hook ─────────────────────────────────────────────

func TestNotifyStorageChanged_NoObserver(t *testing.T) {
	// Sin observer global, no debe panic
	savedObs := globalObserver
	defer func() { globalObserver = savedObs }()
	globalObserver = nil

	notifyStorageChanged() // no-op, no panic
}

func TestNotifyStorageChanged_TriggersScan(t *testing.T) {
	savedObs := globalObserver
	defer func() { globalObserver = savedObs }()

	o := NewStorageObserver(1 * time.Hour)

	var scanCount atomic.Int64
	o.probeFn = func() ([]ObservedBtrfs, []ObservedDevice, bool) {
		scanCount.Add(1)
		return nil, nil, true
	}
	// Fingerprint que cambia siempre (counter) — para evitar skip
	var fpCounter atomic.Int64
	o.fingerprintFn = func() [32]byte {
		var fp [32]byte
		n := fpCounter.Add(1)
		fp[0] = byte(n)
		return fp
	}

	o.Start()
	defer o.Stop()

	globalObserver = o

	// Esperar scan inicial
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && scanCount.Load() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	initial := scanCount.Load()
	if initial == 0 {
		t.Fatal("initial scan never happened")
	}

	// notifyStorageChanged debe disparar otro scan
	notifyStorageChanged()

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && scanCount.Load() <= initial {
		time.Sleep(10 * time.Millisecond)
	}
	if scanCount.Load() <= initial {
		t.Errorf("notifyStorageChanged did not trigger scan (count stayed at %d)", initial)
	}
}
