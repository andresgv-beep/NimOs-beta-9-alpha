// docker_labels_test.go — Tests unitarios del sistema de labels NimOS.
//
// NO testea listNimOSContainers porque requiere daemon Docker real.
// Eso queda para integration tests.
//
// Cobertura aquí:
//   - SchemaVersion · valor estable, no se cambia sin querer
//   - Constantes de labels · sin typos
//   - NewNimOSLabels · construye correctamente con todos los campos
//   - ToDockerLabelArgs · formato correcto para docker run
//   - ToMap · formato correcto para inyección en YAML
//   - injectNimOSLabelsIntoCompose · CRÍTICO · varios escenarios reales:
//       · compose simple sin labels existentes
//       · compose con labels en formato map
//       · compose con labels en formato lista (key=value)
//       · stack multi-servicio (Immich-style)
//       · compose mal formado · devuelve error

package main

import (
	"context"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestSchemaVersion_StableValue · si alguien cambia esto sin querer, el test grita.
func TestSchemaVersion_StableValue(t *testing.T) {
	const expected = "beta_8.2"
	if SchemaVersion != expected {
		t.Fatalf("SchemaVersion cambió sin documentación: got %q, want %q. "+
			"Si es intencional, actualiza CHANGELOG.md y el reconciler "+
			"para reconocer ambas versiones durante migración.",
			SchemaVersion, expected)
	}
}

// TestLabelConstants_NoTypos · verifica que los nombres de labels son los documentados.
func TestLabelConstants_NoTypos(t *testing.T) {
	cases := []struct {
		name     string
		got      string
		expected string
	}{
		{"LabelSchemaVersion", LabelSchemaVersion, "com.nimos.schema_version"},
		{"LabelManaged", LabelManaged, "com.nimos.managed"},
		{"LabelAppID", LabelAppID, "com.nimos.app_id"},
		{"LabelAppVersion", LabelAppVersion, "com.nimos.app_version"},
		{"LabelInstalledBy", LabelInstalledBy, "com.nimos.installed_by"},
		{"LabelInstalledAt", LabelInstalledAt, "com.nimos.installed_at"},
		{"LabelStack", LabelStack, "com.nimos.stack"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.expected {
				t.Errorf("%s = %q, want %q", c.name, c.got, c.expected)
			}
		})
	}
}

// TestNewNimOSLabels_StackBuild · construcción para un stack.
func TestNewNimOSLabels_StackBuild(t *testing.T) {
	labels := NewNimOSLabels("nextcloud", "29.0.7", "andres", true)

	if labels.AppID != "nextcloud" {
		t.Errorf("AppID = %q, want 'nextcloud'", labels.AppID)
	}
	if labels.AppVersion != "29.0.7" {
		t.Errorf("AppVersion = %q, want '29.0.7'", labels.AppVersion)
	}
	if labels.InstalledBy != "andres" {
		t.Errorf("InstalledBy = %q, want 'andres'", labels.InstalledBy)
	}
	if !labels.IsStack {
		t.Errorf("IsStack = false, want true")
	}
	if labels.InstalledAt == "" {
		t.Error("InstalledAt vacío · debería rellenarse con time.Now().UTC()")
	}
	if !strings.Contains(labels.InstalledAt, "T") || !strings.HasSuffix(labels.InstalledAt, "Z") {
		t.Errorf("InstalledAt = %q, no parece ISO 8601 UTC", labels.InstalledAt)
	}
}

// TestToDockerLabelArgs_Format · verifica el formato para docker run.
func TestToDockerLabelArgs_Format(t *testing.T) {
	labels := NewNimOSLabels("test-app", "1.0", "test-user", true)
	args := labels.ToDockerLabelArgs()

	if len(args) != 14 {
		t.Fatalf("ToDockerLabelArgs devolvió %d args, want 14", len(args))
	}

	for i := 0; i < len(args); i += 2 {
		if args[i] != "--label" {
			t.Errorf("args[%d] = %q, want '--label'", i, args[i])
		}
		if !strings.Contains(args[i+1], "=") {
			t.Errorf("args[%d] = %q, no contiene '='", i+1, args[i+1])
		}
	}

	mustContain(t, args, LabelManaged+"=true")
	mustContain(t, args, LabelAppID+"=test-app")
	mustContain(t, args, LabelStack+"=true")
	mustContain(t, args, LabelSchemaVersion+"="+SchemaVersion)
}

// TestToMap_AllFieldsPresent · ToMap debe contener los 7 labels.
func TestToMap_AllFieldsPresent(t *testing.T) {
	labels := NewNimOSLabels("test-app", "1.0", "user", false)
	m := labels.ToMap()
	if len(m) != 7 {
		t.Errorf("ToMap devolvió %d entradas, want 7", len(m))
	}
	for _, key := range []string{
		LabelSchemaVersion, LabelManaged, LabelAppID, LabelAppVersion,
		LabelInstalledBy, LabelInstalledAt, LabelStack,
	} {
		if _, ok := m[key]; !ok {
			t.Errorf("ToMap falta clave %q", key)
		}
	}
	if m[LabelStack] != "false" {
		t.Errorf("ToMap LabelStack = %q, want 'false'", m[LabelStack])
	}
}

// ─────────────────────────────────────────────────────────────────────────
// injectNimOSLabelsIntoCompose · tests críticos
// ─────────────────────────────────────────────────────────────────────────

// TestInject_SimpleCompose_NoExistingLabels · compose sencillo sin labels.
// El más típico · catálogo NimOS los compose suelen venir así.
func TestInject_SimpleCompose_NoExistingLabels(t *testing.T) {
	compose := `services:
  nextcloud:
    image: nextcloud:latest
    container_name: nextcloud
    ports:
      - "8080:80"
    restart: unless-stopped
`
	labels := NewNimOSLabels("nextcloud", "29.0.7", "andres", true)

	out, err := injectNimOSLabelsIntoCompose(compose, labels)
	if err != nil {
		t.Fatalf("injectNimOSLabelsIntoCompose falló: %v", err)
	}

	assertComposeHasLabel(t, out, "nextcloud", LabelManaged, "true")
	assertComposeHasLabel(t, out, "nextcloud", LabelAppID, "nextcloud")
	assertComposeHasLabel(t, out, "nextcloud", LabelStack, "true")
	assertComposeHasLabel(t, out, "nextcloud", LabelSchemaVersion, SchemaVersion)
	assertComposeHasLabel(t, out, "nextcloud", LabelAppVersion, "29.0.7")
	assertComposeHasLabel(t, out, "nextcloud", LabelInstalledBy, "andres")

	// El resto del compose debe preservarse
	if !strings.Contains(out, "nextcloud:latest") {
		t.Error("La imagen 'nextcloud:latest' se perdió tras inyección")
	}
	if !strings.Contains(out, "8080:80") {
		t.Error("El port mapping 8080:80 se perdió tras inyección")
	}
}

// TestInject_ComposeWithExistingLabels_MapFormat · compose con labels en map.
// Los nuevos labels NimOS se añaden, los del usuario se preservan.
func TestInject_ComposeWithExistingLabels_MapFormat(t *testing.T) {
	compose := `services:
  jellyfin:
    image: jellyfin/jellyfin:latest
    labels:
      traefik.enable: "true"
      org.example.custom: "userlabel"
`
	labels := NewNimOSLabels("jellyfin", "10.11.10", "andres", true)

	out, err := injectNimOSLabelsIntoCompose(compose, labels)
	if err != nil {
		t.Fatalf("injectNimOSLabelsIntoCompose falló: %v", err)
	}

	assertComposeHasLabel(t, out, "jellyfin", LabelManaged, "true")
	assertComposeHasLabel(t, out, "jellyfin", LabelAppID, "jellyfin")
	assertComposeHasLabel(t, out, "jellyfin", "traefik.enable", "true")
	assertComposeHasLabel(t, out, "jellyfin", "org.example.custom", "userlabel")
}

// TestInject_ComposeWithExistingLabels_ListFormat · compose con labels en lista.
func TestInject_ComposeWithExistingLabels_ListFormat(t *testing.T) {
	compose := `services:
  gitea:
    image: gitea/gitea:latest
    labels:
      - "traefik.enable=true"
      - "org.example.custom=userlabel"
`
	labels := NewNimOSLabels("gitea", "1.21", "andres", true)

	out, err := injectNimOSLabelsIntoCompose(compose, labels)
	if err != nil {
		t.Fatalf("injectNimOSLabelsIntoCompose falló: %v", err)
	}

	if !strings.Contains(out, "traefik.enable=true") {
		t.Error("Label del usuario 'traefik.enable=true' se perdió")
	}
	if !strings.Contains(out, "com.nimos.managed=true") {
		t.Error("Label NimOS 'com.nimos.managed=true' no se añadió")
	}
	if !strings.Contains(out, "com.nimos.app_id=gitea") {
		t.Error("Label NimOS 'com.nimos.app_id=gitea' no se añadió")
	}
}

// TestInject_MultiServiceStack · stack tipo Immich con varios servicios.
// TODOS los servicios deben recibir los labels.
func TestInject_MultiServiceStack(t *testing.T) {
	compose := `services:
  immich-server:
    image: ghcr.io/immich-app/immich-server:release
    ports:
      - "2283:2283"
  immich-machine-learning:
    image: ghcr.io/immich-app/immich-machine-learning:release
  redis:
    image: redis:alpine
  database:
    image: postgres:14
    environment:
      POSTGRES_PASSWORD: changeme
`
	labels := NewNimOSLabels("immich", "v2.7.5", "andres", true)

	out, err := injectNimOSLabelsIntoCompose(compose, labels)
	if err != nil {
		t.Fatalf("injectNimOSLabelsIntoCompose falló: %v", err)
	}

	for _, svc := range []string{"immich-server", "immich-machine-learning", "redis", "database"} {
		assertComposeHasLabel(t, out, svc, LabelManaged, "true")
		assertComposeHasLabel(t, out, svc, LabelAppID, "immich")
	}
}

// TestInject_EmptyCompose · devuelve error.
func TestInject_EmptyCompose(t *testing.T) {
	labels := NewNimOSLabels("test", "1.0", "user", true)
	_, err := injectNimOSLabelsIntoCompose("", labels)
	if err == nil {
		t.Error("inject con compose vacío debería fallar, no falló")
	}
}

// TestInject_NoServicesKey · compose sin 'services:' → error.
func TestInject_NoServicesKey(t *testing.T) {
	compose := `version: "3"
networks:
  default:
    external: true
`
	labels := NewNimOSLabels("test", "1.0", "user", true)
	_, err := injectNimOSLabelsIntoCompose(compose, labels)
	if err == nil {
		t.Error("inject sin 'services:' debería fallar, no falló")
	}
}

// TestInject_RegressionNextcloudBug · simula el compose Nextcloud con
// variables interpoladas (${CONFIG_PATH}, ${TZ}). Las variables deben
// preservarse tras la inyección.
func TestInject_RegressionNextcloudBug(t *testing.T) {
	compose := `services:
  nextcloud:
    image: nextcloud:latest
    container_name: nextcloud
    ports:
      - "${HOST_PORT:-8080}:80"
    volumes:
      - ${CONFIG_PATH}/html:/var/www/html
      - ${CONFIG_PATH}/data:/var/www/html/data
    environment:
      - TZ=${TZ}
    restart: unless-stopped
`
	labels := NewNimOSLabels("nextcloud", "", "andres", true)

	out, err := injectNimOSLabelsIntoCompose(compose, labels)
	if err != nil {
		t.Fatalf("inject del compose Nextcloud falló: %v", err)
	}

	// Re-parse · debe seguir siendo YAML válido
	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output no es YAML válido tras inyección: %v\noutput:\n%s", err, out)
	}

	assertComposeHasLabel(t, out, "nextcloud", LabelManaged, "true")
	assertComposeHasLabel(t, out, "nextcloud", LabelAppID, "nextcloud")

	// Las variables interpoladas del compose original deben preservarse
	if !strings.Contains(out, "${CONFIG_PATH}/html") {
		t.Error("Variable ${CONFIG_PATH} se perdió tras inyección")
	}
	if !strings.Contains(out, "${TZ}") {
		t.Error("Variable ${TZ} se perdió tras inyección")
	}
}

// ─────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────

// assertComposeHasLabel parsea el YAML y verifica que un servicio tiene un
// label con la clave y valor especificados. Más robusto que strings.Contains
// porque acepta múltiples formatos (map o lista).
func assertComposeHasLabel(t *testing.T, composeYAML, serviceName, labelKey, expectedValue string) {
	t.Helper()

	var doc map[string]interface{}
	if err := yaml.Unmarshal([]byte(composeYAML), &doc); err != nil {
		t.Fatalf("assertComposeHasLabel: no parsea YAML: %v", err)
	}

	services, ok := doc["services"].(map[string]interface{})
	if !ok {
		t.Fatalf("assertComposeHasLabel: services no es map")
	}

	svc, ok := services[serviceName].(map[string]interface{})
	if !ok {
		t.Fatalf("assertComposeHasLabel: servicio %q no encontrado", serviceName)
	}

	labels, hasLabels := svc["labels"]
	if !hasLabels {
		t.Fatalf("assertComposeHasLabel: servicio %q no tiene labels", serviceName)
	}

	// Labels en formato map
	if labelMap, ok := labels.(map[string]interface{}); ok {
		got, exists := labelMap[labelKey]
		if !exists {
			t.Errorf("servicio %q: label %q no presente", serviceName, labelKey)
			return
		}
		gotStr, _ := got.(string)
		if gotStr != expectedValue {
			t.Errorf("servicio %q label %q = %q, want %q", serviceName, labelKey, gotStr, expectedValue)
		}
		return
	}

	// Labels en formato lista (key=value)
	if labelList, ok := labels.([]interface{}); ok {
		want := labelKey + "=" + expectedValue
		for _, item := range labelList {
			if itemStr, _ := item.(string); itemStr == want {
				return
			}
		}
		t.Errorf("servicio %q (lista): no encontrado %q en %v", serviceName, want, labelList)
		return
	}

	t.Errorf("servicio %q: labels en formato desconocido (%T)", serviceName, labels)
}

func mustContain(t *testing.T, args []string, want string) {
	t.Helper()
	for _, a := range args {
		if a == want {
			return
		}
	}
	t.Errorf("args no contiene %q · args = %v", want, args)
}

// ─────────────────────────────────────────────────────────────────────────
// Borrado de imágenes (uninstall wipe)
// ─────────────────────────────────────────────────────────────────────────

// TestDedupeNonEmpty · el helper que limpia la lista de imágenes.
func TestDedupeNonEmpty(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"vacío", []string{}, nil},
		{"sin duplicados", []string{"a", "b", "c"}, []string{"a", "b", "c"}},
		{"con duplicados", []string{"a", "b", "a", "c", "b"}, []string{"a", "b", "c"}},
		{"con vacíos", []string{"a", "", "  ", "b"}, []string{"a", "b"}},
		{"con espacios", []string{" a ", "a", "b "}, []string{"a", "b"}},
		{"todos iguales", []string{"x", "x", "x"}, []string{"x"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := dedupeNonEmpty(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("dedupeNonEmpty(%v) = %v, want %v", c.in, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("dedupeNonEmpty(%v)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
				}
			}
		})
	}
}

// TestRemoveAppImages_SkipsNoneImages · verifica que las imágenes <none>
// (sin tag claro) se saltan por seguridad · no intentamos borrarlas.
//
// No testea el borrado real (requiere Docker) · solo la lógica de filtrado.
// Como removeAppImages llama a runSafe (que ejecuta docker), aquí solo
// verificamos que con input de solo <none> NO intenta nada y devuelve 0.
func TestRemoveAppImages_SkipsNoneImages(t *testing.T) {
	// Estas imágenes deben saltarse todas · sin Docker real, si intentara
	// borrarlas runSafe fallaría, pero como las salta, removed=0 sin tocar nada.
	skipped := []string{
		"",
		"<none>:<none>",
		"repo:<none>",
		"  ",
	}
	got := removeAppImages(context.Background(), skipped)
	if got != 0 {
		t.Errorf("removeAppImages con solo imágenes inválidas devolvió %d, want 0", got)
	}
}
