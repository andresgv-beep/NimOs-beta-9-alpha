package main

import "strings"

import "testing"

// transformFstabMountPoint es la parte delicada del rename: reescribir /etc/fstab
// sin corromperlo. Estos tests clavan que preserva todo lo demás y solo cambia
// el mount point correcto. Es el caso exacto del bug data6→data9.

func TestTransformFstab_ReplacesCorrectEntry(t *testing.T) {
	fstab := `# /etc/fstab
UUID=aaa /nimos/pools/data1 btrfs defaults,nofail,noatime,compress=zstd 0 2
UUID=bbb /nimos/pools/data9 btrfs defaults,nofail,noatime,compress=zstd 0 2`

	out, n := transformFstabMountPoint(fstab, "/nimos/pools/data9", "/nimos/pools/data6")

	if n != 1 {
		t.Fatalf("reemplazos: got %d, want 1", n)
	}
	if !strings.Contains(out, "UUID=bbb /nimos/pools/data6 btrfs") {
		t.Errorf("la entrada data9 no se reescribió a data6:\n%s", out)
	}
	// La OTRA entrada (data1) debe quedar intacta — clave: no tocar lo que no es.
	if !strings.Contains(out, "UUID=aaa /nimos/pools/data1 btrfs") {
		t.Errorf("data1 fue alterada (no debía):\n%s", out)
	}
	// Las opciones (UUID, fstype, flags) se conservan.
	if !strings.Contains(out, "defaults,nofail,noatime,compress=zstd 0 2") {
		t.Errorf("se perdieron opciones de montaje:\n%s", out)
	}
}

func TestTransformFstab_PreservesCommentsAndBlanks(t *testing.T) {
	fstab := "# comentario importante\n\nUUID=x /nimos/pools/old btrfs defaults 0 2\n# otro\n"
	out, n := transformFstabMountPoint(fstab, "/nimos/pools/old", "/nimos/pools/new")
	if n != 1 {
		t.Fatalf("reemplazos: got %d, want 1", n)
	}
	if !strings.Contains(out, "# comentario importante") || !strings.Contains(out, "# otro") {
		t.Errorf("se perdieron comentarios:\n%s", out)
	}
	if !strings.Contains(out, "/nimos/pools/new") {
		t.Errorf("no se aplicó el cambio:\n%s", out)
	}
}

func TestTransformFstab_NoMatchLeavesUnchanged(t *testing.T) {
	fstab := "UUID=x /nimos/pools/data1 btrfs defaults 0 2\n"
	out, n := transformFstabMountPoint(fstab, "/nimos/pools/inexistente", "/nimos/pools/nuevo")
	if n != 0 {
		t.Errorf("no debía reemplazar nada: got %d", n)
	}
	if !strings.Contains(out, "/nimos/pools/data1") {
		t.Errorf("el contenido cambió sin match:\n%s", out)
	}
}

func TestTransformFstab_NoPartialMatch(t *testing.T) {
	// /nimos/pools/data NO debe coincidir con /nimos/pools/data9 (match exacto
	// de campo, no prefijo). Evita corromper la entrada equivocada.
	fstab := "UUID=x /nimos/pools/data9 btrfs defaults 0 2\n"
	out, n := transformFstabMountPoint(fstab, "/nimos/pools/data", "/nimos/pools/renamed")
	if n != 0 {
		t.Errorf("match parcial indebido: got %d reemplazos, want 0", n)
	}
	if !strings.Contains(out, "/nimos/pools/data9") {
		t.Errorf("data9 fue alterada por un match parcial de 'data':\n%s", out)
	}
}
