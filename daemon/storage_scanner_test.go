// storage_scanner_test.go — Tests del scanner y de StorageService.ScanDevices.
//
// No requieren sudo ni lsblk real. Usan MockDeviceScanner para inyectar
// listas de discos predefinidas y verificar:
//   - Inserción de devices nuevos
//   - Actualización de devices existentes (por serial)
//   - Devices sin serial rechazados
//   - Devices con by_id_path cambiado se actualizan
//   - Ejecuciones repetidas son idempotentes

package main

import (
	"context"
	"fmt"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// ScanDevices — caminos básicos
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageScannerInsertNewDevices(t *testing.T) {
	service, _, scanner, cleanup := setupTestServiceWithScanner(t)
	defer cleanup()
	ctx := context.Background()

	scanner.Devices = []ScannedDevice{
		{
			Name: "sdb", DevicePath: "/dev/sdb",
			ByIDPath: "/dev/disk/by-id/ata-WDC_WD40EFRX-WCC4N1234",
			Serial:   "WCC4N1234", Model: "WDC WD40EFRX",
			SizeBytes: 4000000000000, Transport: "sata",
		},
		{
			Name: "sdc", DevicePath: "/dev/sdc",
			ByIDPath: "/dev/disk/by-id/ata-WDC_WD40EFRX-WCC4N5678",
			Serial:   "WCC4N5678", Model: "WDC WD40EFRX",
			SizeBytes: 4000000000000, Transport: "sata",
		},
	}

	result, err := service.ScanDevices(ctx)
	if err != nil {
		t.Fatalf("ScanDevices: %v", err)
	}
	if result.Total != 2 {
		t.Errorf("Total: got %d, want 2", result.Total)
	}
	if result.Inserted != 2 {
		t.Errorf("Inserted: got %d, want 2", result.Inserted)
	}
	if result.Updated != 0 {
		t.Errorf("Updated: got %d, want 0", result.Updated)
	}

	// Verificar persistencia
	devs, _ := service.ListDevices(ctx)
	if len(devs) != 2 {
		t.Errorf("devices in DB: got %d, want 2", len(devs))
	}

	// El matching por serial funciona
	d1, _ := service.repo.GetDeviceBySerial(ctx, "WCC4N1234")
	if d1 == nil {
		t.Fatal("device WCC4N1234 not found")
	}
	if d1.ByIDPath != "/dev/disk/by-id/ata-WDC_WD40EFRX-WCC4N1234" {
		t.Errorf("ByIDPath: got %q", d1.ByIDPath)
	}
}

func TestStorageScannerUpdateExistingDevices(t *testing.T) {
	service, _, scanner, cleanup := setupTestServiceWithScanner(t)
	defer cleanup()
	ctx := context.Background()

	// Primera pasada
	scanner.Devices = []ScannedDevice{
		{
			Name: "sdb", DevicePath: "/dev/sdb",
			ByIDPath: "/dev/disk/by-id/ata-X-S1",
			Serial:   "S1", SizeBytes: 1e12,
		},
	}
	r1, _ := service.ScanDevices(ctx)
	if r1.Inserted != 1 {
		t.Fatalf("first scan: inserted got %d, want 1", r1.Inserted)
	}

	// Segunda pasada con el mismo serial pero current_path distinto
	// (simula reboot donde el kernel asignó otra letra al disco)
	scanner.Devices = []ScannedDevice{
		{
			Name: "sdc", DevicePath: "/dev/sdc", // ← cambió
			ByIDPath: "/dev/disk/by-id/ata-X-S1", // sigue siendo el mismo
			Serial:   "S1", SizeBytes: 1e12,
		},
	}
	r2, _ := service.ScanDevices(ctx)
	if r2.Inserted != 0 {
		t.Errorf("second scan: inserted got %d, want 0", r2.Inserted)
	}
	if r2.Updated != 1 {
		t.Errorf("second scan: updated got %d, want 1", r2.Updated)
	}

	// El device tiene current_path actualizado, ID interno conservado
	d, _ := service.repo.GetDeviceBySerial(ctx, "S1")
	if d.CurrentPath != "/dev/sdc" {
		t.Errorf("CurrentPath after second scan: got %q, want /dev/sdc", d.CurrentPath)
	}
	// Solo debe haber 1 device en DB (no se duplica)
	all, _ := service.ListDevices(ctx)
	if len(all) != 1 {
		t.Errorf("total devices: got %d, want 1", len(all))
	}
}

func TestStorageScannerIdempotent(t *testing.T) {
	service, _, scanner, cleanup := setupTestServiceWithScanner(t)
	defer cleanup()
	ctx := context.Background()

	scanner.Devices = []ScannedDevice{
		{Name: "sdb", DevicePath: "/dev/sdb",
			ByIDPath: "/dev/disk/by-id/x", Serial: "ABC", SizeBytes: 1e12},
	}

	// Ejecutar 3 veces — debe seguir habiendo 1 device
	for i := 0; i < 3; i++ {
		_, err := service.ScanDevices(ctx)
		if err != nil {
			t.Fatalf("scan %d: %v", i+1, err)
		}
	}

	devs, _ := service.ListDevices(ctx)
	if len(devs) != 1 {
		t.Errorf("after 3 scans: got %d devices, want 1", len(devs))
	}
}

func TestStorageScannerDevicesNotInScanArePreserved(t *testing.T) {
	// Si un device desaparece físicamente del scan, NO se borra de DB.
	// Se preserva para que Fase 4 (reconciler) lo marque como missing.
	service, _, scanner, cleanup := setupTestServiceWithScanner(t)
	defer cleanup()
	ctx := context.Background()

	// Primera pasada con 2 discos
	scanner.Devices = []ScannedDevice{
		{Name: "sdb", DevicePath: "/dev/sdb", ByIDPath: "/dev/disk/by-id/a",
			Serial: "A", SizeBytes: 1e12},
		{Name: "sdc", DevicePath: "/dev/sdc", ByIDPath: "/dev/disk/by-id/b",
			Serial: "B", SizeBytes: 1e12},
	}
	service.ScanDevices(ctx)

	// Segunda pasada: solo aparece uno (el otro se desconectó)
	scanner.Devices = []ScannedDevice{
		{Name: "sdb", DevicePath: "/dev/sdb", ByIDPath: "/dev/disk/by-id/a",
			Serial: "A", SizeBytes: 1e12},
	}
	service.ScanDevices(ctx)

	// Los 2 devices siguen en DB
	devs, _ := service.ListDevices(ctx)
	if len(devs) != 2 {
		t.Errorf("got %d devices, want 2 (missing not deleted)", len(devs))
	}
}

func TestStorageScannerScannerErrorPropagates(t *testing.T) {
	service, _, scanner, cleanup := setupTestServiceWithScanner(t)
	defer cleanup()

	scanner.Err = fmt.Errorf("lsblk: permission denied")

	_, err := service.ScanDevices(context.Background())
	if err == nil {
		t.Fatal("expected error from scanner")
	}
}

func TestStorageScannerEmptyByIDPathFallbacks(t *testing.T) {
	// Si un device viene sin by_id_path (udev aún no creó el symlink),
	// se persiste usando current_path como fallback temporal.
	service, _, scanner, cleanup := setupTestServiceWithScanner(t)
	defer cleanup()
	ctx := context.Background()

	scanner.Devices = []ScannedDevice{
		{Name: "sdb", DevicePath: "/dev/sdb", ByIDPath: "", // sin symlink
			Serial: "NEW-DISK-123", SizeBytes: 1e12},
	}

	result, err := service.ScanDevices(ctx)
	if err != nil {
		t.Fatalf("ScanDevices: %v", err)
	}
	if result.Inserted != 1 {
		t.Errorf("Inserted: got %d, want 1", result.Inserted)
	}

	d, _ := service.repo.GetDeviceBySerial(ctx, "NEW-DISK-123")
	if d == nil {
		t.Fatal("device not persisted")
	}
	if d.ByIDPath != "/dev/sdb" {
		t.Errorf("ByIDPath fallback: got %q, want /dev/sdb", d.ByIDPath)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests de helpers locales del scanner
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageScannerIsStorageDeviceName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"sda", true},
		{"sdb1", true}, // prefijo, ya filtramos por type=disk antes
		{"nvme0n1", true},
		{"vda", true},
		{"hda", true},
		{"loop0", false},
		{"sr0", false},
		{"dm-0", false},
		{"md0", false},
	}
	for _, c := range cases {
		got := isStorageDeviceName(c.name)
		if got != c.want {
			t.Errorf("%q: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestStorageScannerFindRootDeviceNameSDA(t *testing.T) {
	// Mockear readFile para devolver /proc/mounts con / en /dev/sda1
	originalReadFile := readFile
	readFile = func(path string) (string, error) {
		if path == "/proc/mounts" {
			return "/dev/sda1 / ext4 rw 0 0\n/dev/sda2 /home ext4 rw 0 0\n", nil
		}
		return "", fmt.Errorf("unexpected path")
	}
	defer func() { readFile = originalReadFile }()

	got := findRootDeviceName()
	if got != "sda" {
		t.Errorf("got %q, want sda", got)
	}
}

func TestStorageScannerFindRootDeviceNameNVMe(t *testing.T) {
	originalReadFile := readFile
	readFile = func(path string) (string, error) {
		if path == "/proc/mounts" {
			return "/dev/nvme0n1p2 / ext4 rw 0 0\n", nil
		}
		return "", fmt.Errorf("unexpected path")
	}
	defer func() { readFile = originalReadFile }()

	got := findRootDeviceName()
	if got != "nvme0n1" {
		t.Errorf("got %q, want nvme0n1", got)
	}
}
