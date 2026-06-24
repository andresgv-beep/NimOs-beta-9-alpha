package main

import "testing"

// ─── fstabContentIsValid — P5: detección de fstab corrupto ────────────────────

func TestFstabValid_NormalEntry(t *testing.T) {
	content := `# /etc/fstab
UUID=abc-123 / ext4 defaults 0 1
UUID=def-456 /nimos/pools/data8 btrfs defaults,nofail 0 2
`
	if !fstabContentIsValid(content) {
		t.Error("fstab normal debería ser válido")
	}
}

func TestFstabValid_OnlyCommentsAndBlanks(t *testing.T) {
	content := "# comentario\n\n   \n# otro\n"
	if !fstabContentIsValid(content) {
		t.Error("solo comentarios/blancos: válido (no es trabajo de esta función exigir entradas)")
	}
}

func TestFstabValid_EmptyIsValid(t *testing.T) {
	if !fstabContentIsValid("") {
		t.Error("vacío: no restaurar sin causa → válido")
	}
}

func TestFstabInvalid_TruncatedLine(t *testing.T) {
	// Línea truncada a media escritura: menos de 4 campos.
	content := `UUID=abc-123 / ext4 defaults 0 1
UUID=def-456 /nimos/pools/da`
	if fstabContentIsValid(content) {
		t.Error("línea truncada debe detectarse como corrupta")
	}
}

func TestFstabInvalid_MissingFields(t *testing.T) {
	content := "UUID=abc / ext4\n" // solo 3 campos
	if fstabContentIsValid(content) {
		t.Error("entrada con 3 campos debe ser inválida (faltan opts)")
	}
}

func TestFstabValid_FourFieldsMinimum(t *testing.T) {
	// device + mountpoint + fstype + opts = 4 campos, dump/pass opcionales.
	content := "UUID=abc /mnt ext4 defaults\n"
	if !fstabContentIsValid(content) {
		t.Error("4 campos (dump/pass omitidos) debería ser válido")
	}
}

func TestFstabInvalid_GarbageMidFile(t *testing.T) {
	content := `UUID=abc / ext4 defaults 0 1
\x00\x00garbage
UUID=def /data btrfs defaults 0 2
`
	if fstabContentIsValid(content) {
		t.Error("basura en medio del archivo debe detectarse como corrupta")
	}
}
