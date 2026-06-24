package main

import "testing"

// ─────────────────────────────────────────────────────────────────────────────
// poolMountFromPath — base de la INVARIANTE FUNDAMENTAL de NimOS:
// nunca escribir al disco de sistema. Una ruta de archivo dentro de un pool
// debe resolver al mountpoint del POOL (/nimos/pools/<nombre>), que es lo que
// luego se verifica contra el kernel con `mountpoint -q`.
//
// Regression 13/06: la versión anterior usaba `findmnt --target`, que resuelve
// hacia arriba hasta `/` (sda2) cuando el pool está desmontado → archivos al
// disco de sistema. Esta función extrae el pool de forma pura y exacta.
// ─────────────────────────────────────────────────────────────────────────────

func TestPoolMountFromPath(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{
			name: "archivo en raíz del pool",
			path: "/nimos/pools/datatest1/foto.jpg",
			want: "/nimos/pools/datatest1",
		},
		{
			name: "ruta profunda resuelve al mountpoint del pool, no a subdir",
			path: "/nimos/pools/datatest1/fotos/2026/junio/img.png",
			want: "/nimos/pools/datatest1",
		},
		{
			name: "el propio mountpoint del pool (con prefijo /nimos/pools/) resuelve a sí mismo",
			path: "/nimos/pools/datatest1",
			want: "/nimos/pools/datatest1",
		},
		{
			name: "fuera de /nimos/pools/ → vacío (no es pool)",
			path: "/home/andres/cosa.txt",
			want: "",
		},
		{
			name: "disco de sistema directo → vacío",
			path: "/var/lib/algo",
			want: "",
		},
		{
			name: "pool con nombre largo",
			path: "/nimos/pools/data_backup_2026/x",
			want: "/nimos/pools/data_backup_2026",
		},
		{
			name: "string vacío",
			path: "",
			want: "",
		},
		{
			name: "prefijo parecido pero no es pools (no debe matchear)",
			path: "/nimos/poolsX/data/x",
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := poolMountFromPath(tc.path)
			if got != tc.want {
				t.Errorf("poolMountFromPath(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// TestIsPathOnMountedPool_RejectsNonPoolPaths verifica que rutas fuera de
// /nimos/pools/ se rechazan SIN siquiera consultar al kernel — primera línea
// de defensa de la invariante.
func TestIsPathOnMountedPool_RejectsNonPoolPaths(t *testing.T) {
	rejected := []string{
		"",
		"/",
		"/home/andres/file.txt",
		"/var/lib/docker/x",
		"/nimos/poolsX/data",
		"/etc/passwd",
	}
	for _, p := range rejected {
		if isPathOnMountedPool(p) {
			t.Errorf("isPathOnMountedPool(%q) = true, want false (no es ruta de pool)", p)
		}
	}
}
