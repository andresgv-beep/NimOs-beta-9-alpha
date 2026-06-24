package main

import "testing"

// Fix de seguridad: un disco con filesystem a disco completo (sin particiones)
// DEBE reportar hasExistingData=true. Antes solo miraba particiones, así que un
// miembro BTRFS de pool o un disco ext4 de otro sistema aparecía como "vacío"
// → invitación al wipe en el CreatePoolWizard.

func TestDiskHasExistingData(t *testing.T) {
	cases := []struct {
		name       string
		partitions int
		fstype     string
		want       bool
	}{
		// El caso peligroso que arreglamos:
		{"FS a disco completo (btrfs miembro de pool)", 0, "btrfs", true},
		{"FS a disco completo (ext4 de otro sistema)", 0, "ext4", true},
		{"FS a disco completo (xfs)", 0, "xfs", true},
		// Casos con particiones (ya funcionaban):
		{"disco con particiones, sin FS whole-disk", 2, "", true},
		{"disco con particiones Y fstype", 1, "vfat", true},
		// Disco realmente vacío (sin particiones, sin FS):
		{"disco virgen sin nada", 0, "", false},
		// lsblk a veces da el literal "<nil>" cuando el campo es null:
		{"fstype nulo literal", 0, "<nil>", false},
		{"fstype null string", 0, "null", false},
		{"fstype con espacios", 0, "  ", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := diskHasExistingData(c.partitions, c.fstype)
			if got != c.want {
				t.Errorf("diskHasExistingData(%d, %q) = %v, want %v",
					c.partitions, c.fstype, got, c.want)
			}
		})
	}
}

func TestNormalizeFstype(t *testing.T) {
	cases := map[string]string{
		"btrfs":   "btrfs",
		"<nil>":   "",
		"null":    "",
		"  ext4 ": "ext4",
		"":        "",
		"   ":     "",
	}
	for in, want := range cases {
		if got := normalizeFstype(in); got != want {
			t.Errorf("normalizeFstype(%q) = %q, want %q", in, got, want)
		}
	}
}
