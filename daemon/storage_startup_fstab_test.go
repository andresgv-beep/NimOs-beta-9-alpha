package main

import "testing"

// ─────────────────────────────────────────────────────────────────────────────
// fstabHasMountpoint — fix del falso skip de appendFstab (saga 12-13/06/2026)
//
// Bug: appendFstab usaba strings.Contains(fstab, mountPoint). Eso daba falso
// positivo cuando el mountpoint era substring de otra entrada (o de un residuo),
// provocando un skip silencioso → la entrada nunca se escribía → el pool se
// montaba en caliente pero NO sobrevivía al reinicio → caía a directorio hueco
// sobre el disco de sistema. Encontrado en hardware: datatest1/datatest2 sin
// entrada en fstab pese a que appendFstab "corrió".
// ─────────────────────────────────────────────────────────────────────────────

func TestFstabHasMountpoint(t *testing.T) {
	cases := []struct {
		name       string
		fstab      string
		mountPoint string
		want       bool
	}{
		{
			name:       "vacío → no está",
			fstab:      "",
			mountPoint: "/nimos/pools/datatest1",
			want:       false,
		},
		{
			name:       "entrada exacta presente",
			fstab:      "UUID=abc /nimos/pools/datatest1 btrfs defaults 0 2\n",
			mountPoint: "/nimos/pools/datatest1",
			want:       true,
		},
		{
			// EL BUG: datatest1 contiene como substring "datatest". Con Contains
			// daba true (falso positivo) y se saltaba la escritura.
			name:       "substring de otra entrada NO cuenta (el bug real)",
			fstab:      "UUID=xyz /nimos/pools/datatest btrfs defaults 0 2\n",
			mountPoint: "/nimos/pools/datatest1",
			want:       false,
		},
		{
			name:       "prefijo: datatest1 no matchea datatest10",
			fstab:      "UUID=xyz /nimos/pools/datatest10 btrfs defaults 0 2\n",
			mountPoint: "/nimos/pools/datatest1",
			want:       false,
		},
		{
			name:       "el UUID contiene el texto pero el mountpoint no → no cuenta",
			fstab:      "UUID=datatest1-fake /dev/other ext4 defaults 0 2\n",
			mountPoint: "/nimos/pools/datatest1",
			want:       false,
		},
		{
			name:       "línea comentada no cuenta",
			fstab:      "# UUID=abc /nimos/pools/datatest1 btrfs defaults 0 2\n",
			mountPoint: "/nimos/pools/datatest1",
			want:       false,
		},
		{
			name:       "presente entre otras entradas",
			fstab:      "UUID=root / ext4 defaults 0 1\nUUID=abc /nimos/pools/datatest1 btrfs defaults 0 2\nUUID=d /nimos/pools/data6 btrfs defaults 0 2\n",
			mountPoint: "/nimos/pools/datatest1",
			want:       true,
		},
		{
			name:       "trailing slash se normaliza",
			fstab:      "UUID=abc /nimos/pools/datatest1 btrfs defaults 0 2\n",
			mountPoint: "/nimos/pools/datatest1/",
			want:       true,
		},
		{
			name:       "línea malformada (1 campo) se ignora",
			fstab:      "/nimos/pools/datatest1\n",
			mountPoint: "/nimos/pools/datatest1",
			want:       false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fstabHasMountpoint(tc.fstab, tc.mountPoint)
			if got != tc.want {
				t.Errorf("fstabHasMountpoint(%q, %q) = %v, want %v",
					tc.fstab, tc.mountPoint, got, tc.want)
			}
		})
	}
}
