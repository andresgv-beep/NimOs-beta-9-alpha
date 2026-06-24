// docker_env_test.go — Tests de writeEnvFile.
//
// Incluye:
//   1. Test de CARACTERIZACIÓN · captura el comportamiento ACTUAL exacto
//      (el que tenía el código inline en dockerStackDeploy antes de extraerlo).
//      Si este test pasa, la extracción NO cambió el comportamiento observable.
//   2. (Tras añadir el escape) tests de casos especiales.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// parseEnvForTest lee un .env a un map, SIN depender del orden de las líneas.
// Comparar maps (no strings literales) es robusto al orden no determinista.
func parseEnvForTest(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no se pudo leer %s: %v", path, err)
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		// Split en el PRIMER '=' · el value puede contener '=' después.
		i := strings.IndexByte(line, '=')
		if i < 0 {
			t.Fatalf("línea sin '=': %q", line)
		}
		out[line[:i]] = line[i+1:]
	}
	return out
}

// TestWriteEnvFile_Characterization · captura el comportamiento ACTUAL.
// El código original hacía: para cada k,v → "k=v", unidas por "\n", con "\n"
// final, permisos 0644. Este test verifica que writeEnvFile produce
// exactamente ese contenido (comparando por map, robusto al orden).
func TestWriteEnvFile_Characterization(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")

	env := map[string]interface{}{
		"CONFIG_PATH": "/nimos/pools/data8/docker/containers/x",
		"HOST_IP":     "192.168.1.131",
		"TZ":          "Europe/Madrid",
		"SERVER_NAME": "matrix.midominio.duckdns.org",
	}

	if err := writeEnvFile(path, env); err != nil {
		t.Fatalf("writeEnvFile error: %v", err)
	}

	// 1. El contenido (por map) debe coincidir EXACTAMENTE con el input.
	got := parseEnvForTest(t, path)
	if len(got) != len(env) {
		t.Errorf("nº de claves: got %d, want %d", len(got), len(env))
	}
	for k, v := range env {
		want := v.(string)
		if got[k] != want {
			t.Errorf("clave %s: got %q, want %q", k, got[k], want)
		}
	}

	// 2. Debe terminar en "\n" (como el original).
	data, _ := os.ReadFile(path)
	if !strings.HasSuffix(string(data), "\n") {
		t.Error("el .env debe terminar en \\n (comportamiento original)")
	}

	// 3. Permisos 0600 (secretos · solo root puede leer el .env).
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Errorf("permisos: got %o, want 0600 (secretos · solo root)", info.Mode().Perm())
	}
}

// TestWriteEnvFile_Deterministic · el nuevo comportamiento ordena las claves,
// así que dos escrituras del mismo env dan EXACTAMENTE el mismo archivo
// (antes, el range sobre map daba orden aleatorio).
func TestWriteEnvFile_Deterministic(t *testing.T) {
	dir := t.TempDir()
	env := map[string]interface{}{
		"Z_VAR": "1", "A_VAR": "2", "M_VAR": "3", "B_VAR": "4",
	}

	p1 := filepath.Join(dir, "a.env")
	p2 := filepath.Join(dir, "b.env")
	writeEnvFile(p1, env)
	writeEnvFile(p2, env)

	d1, _ := os.ReadFile(p1)
	d2, _ := os.ReadFile(p2)
	if string(d1) != string(d2) {
		t.Errorf("dos escrituras del mismo env deben ser idénticas:\n%s\n---\n%s", d1, d2)
	}

	// Y deben estar ordenadas alfabéticamente.
	want := "A_VAR=2\nB_VAR=4\nM_VAR=3\nZ_VAR=1\n"
	if string(d1) != want {
		t.Errorf("orden alfabético esperado:\ngot:\n%s\nwant:\n%s", d1, want)
	}
}

// TestWriteEnvFile_SecurePerms · GARANTÍA DE SEGURIDAD · el .env debe ser 0600
// (solo root). Puede contener secretos (passwords del modal). Si esto cambia a
// un modo más permisivo, es un AGUJERO · este test lo detecta.
func TestWriteEnvFile_SecurePerms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	env := map[string]interface{}{
		"ADMIN_PASS": "un-secreto-que-no-debe-leer-cualquiera",
	}
	if err := writeEnvFile(path, env); err != nil {
		t.Fatalf("writeEnvFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("el .env DEBE ser 0600 (contiene secretos), got %o", perm)
	}
	// Verificación extra: no debe ser legible por grupo ni otros.
	if perm&0077 != 0 {
		t.Errorf("el .env NO debe dar permisos a grupo/otros, got %o", perm)
	}
}

// TestWriteEnvFile_FixesExistingPerms · CRÍTICO · si ya existe un .env con
// permisos laxos (0644, instalación vieja), reescribirlo debe CORREGIRLO a
// 0600. os.WriteFile por sí solo NO cambia perms de ficheros existentes · por
// eso writeEnvFile hace Chmod explícito. Sin esto, las apps ya instaladas
// mantendrían el agujero al reinstalar.
func TestWriteEnvFile_FixesExistingPerms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")

	// Simular un .env viejo con permisos laxos.
	if err := os.WriteFile(path, []byte("OLD=1\n"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0644 {
		t.Fatalf("setup: el .env de prueba debería ser 0644")
	}

	// Reescribir con writeEnvFile → debe corregir a 0600.
	if err := writeEnvFile(path, map[string]interface{}{"NEW": "1"}); err != nil {
		t.Fatalf("writeEnvFile: %v", err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Errorf("CRÍTICO: un .env preexistente 0644 debe corregirse a 0600, got %o", info.Mode().Perm())
	}
}

// TestReprotectEnvFile_From775 · CRÍTICO · simula lo que pasa en deploy: el
// .env queda a 775 tras el chmod -R 775 del stackPath. reprotectEnvFile debe
// devolverlo a 0600. Este es el caso REAL que se vio en hardware (todos los
// .env aparecían a 775 porque el chmod -R los pisaba).
func TestReprotectEnvFile_From775(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("ADMIN_PASS=secreto\n"), 0600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// Simular el chmod -R 775 que pisa el .env.
	if err := os.Chmod(path, 0775); err != nil {
		t.Fatalf("setup chmod 775: %v", err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0775 {
		t.Fatalf("setup: el .env debería estar a 775 antes de reproteger")
	}

	// Reproteger → debe volver a 0600.
	reprotectEnvFile(path)

	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Errorf("CRÍTICO: tras reprotect, el .env debe ser 0600, got %o", info.Mode().Perm())
	}
	if info.Mode().Perm()&0077 != 0 {
		t.Errorf("el .env NO debe dar permisos a grupo/otros, got %o", info.Mode().Perm())
	}
}

// TestReprotectEnvFile_NoFile · si el .env no existe, no debe petar (no todos
// los stacks tienen .env · la función es silenciosa).
func TestReprotectEnvFile_NoFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "no-existe.env")
	reprotectEnvFile(path) // no debe panic ni error
}

// TestWriteEnvFile_EmptyMap · un env vacío produce solo "\n" (un archivo
// prácticamente vacío). Caso límite, no debe petar.
func TestWriteEnvFile_EmptyMap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := writeEnvFile(path, map[string]interface{}{}); err != nil {
		t.Fatalf("writeEnvFile con map vacío: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "\n" {
		t.Errorf("env vacío: got %q, want %q", string(data), "\n")
	}
}
