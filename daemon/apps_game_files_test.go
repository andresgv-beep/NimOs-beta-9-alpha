package main

import "testing"

func TestPickShareForPath(t *testing.T) {
	shares := []sharePathEntry{
		{Name: "docker-apps", Path: "/nimos/pools/data9/docker/containers"},
		{Name: "media", Path: "/nimos/pools/data9/media"},
		// Trampa: comparte prefijo textual pero NO es la carpeta padre.
		{Name: "containers-backup", Path: "/nimos/pools/data9/docker/containers-backup"},
		{Name: "root-ish", Path: "/nimos/pools/data9/docker"},
	}

	cases := []struct {
		name      string
		absPath   string
		shares    []sharePathEntry
		wantShare string
		wantRel   string
	}{
		{
			name:      "subcarpeta del juego dentro de docker-apps",
			absPath:   "/nimos/pools/data9/docker/containers/minecraft-java",
			shares:    shares,
			wantShare: "docker-apps",
			wantRel:   "/minecraft-java",
		},
		{
			name:      "match exacto con la base del share",
			absPath:   "/nimos/pools/data9/docker/containers",
			shares:    shares,
			wantShare: "docker-apps",
			wantRel:   "/",
		},
		{
			name:      "no confunde containers con containers-backup",
			absPath:   "/nimos/pools/data9/docker/containers-backup/foo",
			shares:    shares,
			wantShare: "containers-backup",
			wantRel:   "/foo",
		},
		{
			name:      "gana el share mas especifico (base mas larga)",
			absPath:   "/nimos/pools/data9/docker/containers/n8n",
			shares:    shares,
			wantShare: "docker-apps", // no "root-ish" aunque tambien contiene la ruta
			wantRel:   "/n8n",
		},
		{
			name:      "ningun share contiene la ruta",
			absPath:   "/var/lib/docker/containers/minecraft-java",
			shares:    shares,
			wantShare: "",
			wantRel:   "",
		},
		{
			name:      "ruta vacia",
			absPath:   "",
			shares:    shares,
			wantShare: "",
			wantRel:   "",
		},
		{
			name:      "share con barra final se normaliza",
			absPath:   "/data/games/minecraft-java",
			shares:    []sharePathEntry{{Name: "games", Path: "/data/games/"}},
			wantShare: "games",
			wantRel:   "/minecraft-java",
		},
		{
			name:      "share con path vacio se ignora",
			absPath:   "/data/games/minecraft-java",
			shares:    []sharePathEntry{{Name: "bad", Path: ""}, {Name: "games", Path: "/data/games"}},
			wantShare: "games",
			wantRel:   "/minecraft-java",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotShare, gotRel := pickShareForPath(tc.absPath, tc.shares)
			if gotShare != tc.wantShare || gotRel != tc.wantRel {
				t.Fatalf("pickShareForPath(%q) = (%q,%q), quería (%q,%q)",
					tc.absPath, gotShare, gotRel, tc.wantShare, tc.wantRel)
			}
		})
	}
}
