// apps_install_boundary_test.go — Blindaje de la frontera de seguridad de
// comandos de apps nativas.
//
// CONTEXTO (auditoría seguridad · Beta 8.1)
//
//	nativeAppInstall ejecuta `exec.Command("bash", "-c", def.InstallCommand)`.
//	Esto es RCE por diseño: el comando se ejecuta como root vía sudo. La ÚNICA
//	cosa que lo hace seguro es la invariante:
//
//	    «InstallCommand / UninstallCommand / CheckCommand SOLO pueden provenir
//	     del catálogo estático `knownNativeApps`, NUNCA de input externo
//	     (request body, DB, UI, fichero de usuario).»
//
//	Esa invariante hoy vive en un COMENTARIO. Un comentario no falla en CI. El
//	día que alguien cablee un comando desde la DB o la UI, la barrera de
//	seguridad desaparece en silencio. Este fichero convierte el comentario en
//	un test que SÍ falla.
//
//	No probamos que el shell sea "seguro" (no se puede: es un shell). Probamos
//	que la FUENTE del comando no pueda derivar a externa sin romper un test.
//
// Ejecutar:
//
//	cd daemon/
//	go test -run TestInstallCommandBoundary -v
package main

import (
	"reflect"
	"strings"
	"testing"
)

// Diente 1 — El struct nativeAppDef que transporta comandos NO debe ganar
// jamás una etiqueta de (de)serialización. Si alguien añade un `json:"..."` o
// `db:"..."` a un campo *Command, está abriendo la puerta a poblarlo desde
// fuera (Unmarshal de un body, Scan de una fila). Lo detectamos por reflexión.
func TestInstallCommandBoundary_NoSerializationTags(t *testing.T) {
	commandFields := map[string]bool{
		"InstallCommand":   true,
		"UninstallCommand": true,
		"CheckCommand":     true,
	}

	typ := reflect.TypeOf(nativeAppDef{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if !commandFields[f.Name] {
			continue
		}
		// Cualquier tag estructural sobre un campo de comando es sospechoso:
		// habilita Unmarshal/Scan automático desde una fuente externa.
		for _, tagKey := range []string{"json", "db", "yaml", "sql", "form"} {
			if v, ok := f.Tag.Lookup(tagKey); ok {
				t.Errorf("CAMPO DE COMANDO %q lleva tag %q=%q — un campo *Command "+
					"NUNCA debe ser (de)serializable; eso permite poblarlo desde "+
					"input externo y convierte el bash -c en RCE arbitrario. "+
					"Si necesitas serializar metadata, hazlo en un struct aparte "+
					"SIN los comandos.", f.Name, tagKey, v)
			}
		}
	}
}

// Diente 2 — La fila DBNativeApp (lo que se persiste y se lee de SQLite) NO
// debe contener NUNCA un campo de comando. La DB guarda SOLO metadata («qué
// está instalado»), jamás «qué shell ejecutar». Si este test rompe, alguien
// añadió una columna de comando a la persistencia: la fuente del comando ya no
// es exclusivamente el catálogo estático.
func TestInstallCommandBoundary_DBRowHasNoCommands(t *testing.T) {
	// Tokens que delatan un campo portador de comando. Se comparan contra el
	// nombre del campo partido en palabras (camelCase → ["install","command"])
	// para no chocar con metadata legítima como "Description" (que contiene la
	// subcadena "script" pero no es un comando).
	commandTokens := map[string]bool{
		"command": true, "cmd": true, "script": true,
		"exec": true, "shell": true, "run": true,
	}

	typ := reflect.TypeOf(DBNativeApp{})
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		for _, word := range splitCamel(name) {
			if commandTokens[strings.ToLower(word)] {
				t.Errorf("DBNativeApp tiene campo %q (palabra %q) — la persistencia "+
					"NO debe transportar comandos. La DB guarda metadata; los comandos "+
					"viven solo en knownNativeApps. Mover comandos a la DB rompe la "+
					"frontera de seguridad de nativeAppInstall.", name, word)
			}
		}
	}
}

// Diente 3 — Los comandos del catálogo no deben contener verbos de formato
// (%s, %d, %v) ni marcadores de interpolación. Si los tuvieran, sería señal de
// que en algún punto se rellenan con datos dinámicos antes de llegar al shell
// — exactamente el patrón que runShellStatic ya rechaza en runtime, pero que
// aquí cazamos en CI antes de desplegar.
func TestInstallCommandBoundary_CatalogIsStatic(t *testing.T) {
	forbidden := []string{"%s", "%d", "%v", "${"}

	for id, def := range knownNativeApps {
		for _, field := range []struct {
			label string
			value string
		}{
			{"InstallCommand", def.InstallCommand},
			{"UninstallCommand", def.UninstallCommand},
			{"CheckCommand", def.CheckCommand},
		} {
			if field.value == "" {
				continue
			}
			for _, marker := range forbidden {
				if strings.Contains(field.value, marker) {
					t.Errorf("app %q · %s contiene marcador de interpolación %q: %q — "+
						"los comandos del catálogo deben ser literales 100%% estáticos. "+
						"Un verbo de formato implica que se rellena con datos en algún "+
						"punto, lo que reabre la inyección.",
						id, field.label, marker, field.value)
				}
			}
		}
	}
}

// Diente 4 — Ancla de cobertura: garantiza que el catálogo no está vacío (un
// refactor que vacíe knownNativeApps dejaría los dientes 1–3 pasando en falso)
// y que cada entrada con InstallCommand también declara cómo verificarse
// (CheckCommand) — sin Check no hay forma de saber si la instalación funcionó,
// y un install a ciegas como root es justo lo que no queremos.
func TestInstallCommandBoundary_CatalogIntegrity(t *testing.T) {
	if len(knownNativeApps) == 0 {
		t.Fatal("knownNativeApps está vacío — los tests de frontera pasarían en "+
			"falso. Si esto es intencional, elimina este fichero a conciencia.")
	}

	for id, def := range knownNativeApps {
		if def.InstallCommand != "" && def.CheckCommand == "" {
			t.Errorf("app %q define InstallCommand pero no CheckCommand — instalar "+
				"como root sin forma de verificar el resultado es un install a "+
				"ciegas. Añade un CheckCommand idempotente.", id)
		}
	}
}

// splitCamel parte un identificador CamelCase/PascalCase en sus palabras
// componentes: "InstallCommand" → ["Install","Command"], "ID" → ["ID"].
// Permite comparar tokens completos en vez de subcadenas, evitando falsos
// positivos como "Description" ⊇ "script".
func splitCamel(s string) []string {
	var words []string
	start := 0
	for i := 1; i < len(s); i++ {
		// frontera: minúscula seguida de mayúscula (instalCommand → instal|Command)
		if s[i] >= 'A' && s[i] <= 'Z' && s[i-1] >= 'a' && s[i-1] <= 'z' {
			words = append(words, s[start:i])
			start = i
		}
	}
	words = append(words, s[start:])
	return words
}
