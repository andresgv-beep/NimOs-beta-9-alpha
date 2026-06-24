package main

import "testing"

// TestLegacyServicePorts verifica que el mapeo de puertos por servicio es el
// esperado (B1). Si alguien cambia un puerto, este test lo caza.
func TestLegacyServicePorts(t *testing.T) {
	cases := map[string][]string{
		"ftp": {"21/tcp", "55000:55999/tcp"},
		"nfs": {"2049/tcp"},
		"smb": {"445/tcp"},
	}
	for service, want := range cases {
		spec, ok := legacyServicePorts[service]
		if !ok {
			t.Errorf("servicio %q no está mapeado", service)
			continue
		}
		if len(spec.specs) != len(want) {
			t.Errorf("%s: esperados %d specs, got %d (%v)", service, len(want), len(spec.specs), spec.specs)
			continue
		}
		for i := range want {
			if spec.specs[i] != want[i] {
				t.Errorf("%s spec[%d]: got %q want %q", service, i, spec.specs[i], want[i])
			}
		}
	}
}

// TestOpenCloseUnknownService confirma que un servicio sin mapeo es no-op
// (no panic, no error). SSH (22) deliberadamente NO está mapeado: nunca se
// cierra su puerto desde aquí.
func TestOpenCloseUnknownService(t *testing.T) {
	if _, ok := legacyServicePorts["ssh"]; ok {
		t.Error("SSH no debe estar en legacyServicePorts: su puerto nunca se gestiona aquí")
	}
	// No deben entrar en pánico con un servicio desconocido.
	openServicePorts("desconocido")
	closeServicePorts("desconocido")
}
