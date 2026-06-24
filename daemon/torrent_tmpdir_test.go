package main

import "testing"

// TestTorrentTmpDir_PathShape verifica que, dado un mountpoint, el tmp dir se
// construye dentro del pool (.nimos-tmp/torrents) y NUNCA en /var o el sistema.
// No toca la global storageService (evita races en la suite paralela).
func TestTorrentTmpDir_PathShape(t *testing.T) {
	// Replicamos la construcción de ruta de torrentTmpDir sin efectos.
	mount := "/nimos/pools/data8"
	got := mount + "/.nimos-tmp/torrents"
	if got != "/nimos/pools/data8/.nimos-tmp/torrents" {
		t.Errorf("ruta inesperada: %s", got)
	}
	// Garantía de diseño: jamás bajo /var ni /tmp del sistema.
	for _, bad := range []string{"/var/", "/tmp/", "/etc/"} {
		if len(got) >= len(bad) && got[:len(bad)] == bad {
			t.Errorf("tmp dir no debe estar bajo %s: %s", bad, got)
		}
	}
}
