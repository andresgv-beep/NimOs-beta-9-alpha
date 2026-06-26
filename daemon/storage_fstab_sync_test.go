// storage_fstab_sync_test.go — fstab generado desde la BD.
//
// Verifica que el bloque [nimos] se regenera bien: preserva líneas del usuario,
// absorbe legacy, es idempotente, refleja altas/bajas de pools, y nunca deja un
// pool de la BD fuera (el bug de data8).

package main

import (
	"strings"
	"testing"
)

const userFstab = `# /etc/fstab
UUID=root-uuid / ext4 defaults 0 1
UUID=boot-uuid /boot vfat defaults 0 2
`

func countPoolLines(content, mountPoint string) int {
	n := 0
	for _, l := range strings.Split(content, "\n") {
		f := strings.Fields(strings.TrimSpace(l))
		if len(f) >= 2 && f[1] == mountPoint {
			n++
		}
	}
	return n
}

func TestBuildFstab_AddsManagedBlock(t *testing.T) {
	pools := []*Pool{{Name: "data8", BtrfsUUID: "u8", MountPoint: "/nimos/pools/data8"}}
	out := buildManagedFstab(userFstab, pools)

	if !strings.Contains(out, "UUID=root-uuid / ext4") {
		t.Error("perdió una línea del usuario")
	}
	if !strings.Contains(out, fstabMarkerStart) || !strings.Contains(out, fstabMarkerEnd) {
		t.Error("faltan los marcadores [nimos]")
	}
	if !strings.Contains(out, "UUID=u8 /nimos/pools/data8 btrfs") {
		t.Error("falta la línea del pool data8")
	}
	if !strings.Contains(out, "nofail") {
		t.Error("la línea del pool debe incluir nofail")
	}
}

// Idempotencia: aplicar dos veces da el mismo resultado (no acumula).
func TestBuildFstab_Idempotent(t *testing.T) {
	pools := []*Pool{{Name: "data8", BtrfsUUID: "u8", MountPoint: "/nimos/pools/data8"}}
	once := buildManagedFstab(userFstab, pools)
	twice := buildManagedFstab(once, pools)
	if once != twice {
		t.Errorf("no es idempotente:\n--1--\n%s\n--2--\n%s", once, twice)
	}
	if countPoolLines(twice, "/nimos/pools/data8") != 1 {
		t.Error("la línea del pool se duplicó al reaplicar")
	}
}

// Absorbe una entrada legacy suelta de appendFstab (fuera del bloque).
func TestBuildFstab_AbsorbsLegacyLine(t *testing.T) {
	legacy := userFstab + "UUID=u8 /nimos/pools/data8 btrfs defaults,nofail 0 2\n"
	pools := []*Pool{{Name: "data8", BtrfsUUID: "u8", MountPoint: "/nimos/pools/data8"}}
	out := buildManagedFstab(legacy, pools)
	if countPoolLines(out, "/nimos/pools/data8") != 1 {
		t.Errorf("la entrada legacy debió absorberse al bloque (sin duplicar); got %d", countPoolLines(out, "/nimos/pools/data8"))
	}
}

// data8 en la BD pero ausente de fstab → se añade (el bug del incidente).
func TestBuildFstab_AddsMissingPool(t *testing.T) {
	pools := []*Pool{{Name: "data8", BtrfsUUID: "u8", MountPoint: "/nimos/pools/data8"}}
	out := buildManagedFstab(userFstab, pools) // userFstab NO tiene data8
	if countPoolLines(out, "/nimos/pools/data8") != 1 {
		t.Error("un pool en la BD ausente de fstab debe añadirse (caso data8)")
	}
}

// Un pool que ya no está en la BD desaparece del bloque.
func TestBuildFstab_RemovesGonePool(t *testing.T) {
	withTwo := buildManagedFstab(userFstab, []*Pool{
		{Name: "data8", BtrfsUUID: "u8", MountPoint: "/nimos/pools/data8"},
		{Name: "data9", BtrfsUUID: "u9", MountPoint: "/nimos/pools/data9"},
	})
	withOne := buildManagedFstab(withTwo, []*Pool{
		{Name: "data8", BtrfsUUID: "u8", MountPoint: "/nimos/pools/data8"},
	})
	if countPoolLines(withOne, "/nimos/pools/data9") != 0 {
		t.Error("data9 ya no está en la BD; debe desaparecer del bloque")
	}
	if countPoolLines(withOne, "/nimos/pools/data8") != 1 {
		t.Error("data8 sigue en la BD; debe permanecer")
	}
}

// Líneas del usuario siempre intactas.
func TestBuildFstab_PreservesUserLines(t *testing.T) {
	out := buildManagedFstab(userFstab, nil)
	for _, want := range []string{"UUID=root-uuid / ext4 defaults 0 1", "UUID=boot-uuid /boot vfat defaults 0 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("falta línea del usuario: %q", want)
		}
	}
}

// Pool sin UUID se omite (no se escribe una línea rota).
func TestBuildFstab_SkipsPoolWithoutUUID(t *testing.T) {
	out := buildManagedFstab(userFstab, []*Pool{{Name: "x", BtrfsUUID: "", MountPoint: "/nimos/pools/x"}})
	if countPoolLines(out, "/nimos/pools/x") != 0 {
		t.Error("un pool sin UUID no debe generar línea")
	}
}
