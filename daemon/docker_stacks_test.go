// docker_stacks_test.go — Tests del flujo de stacks.
//
// Cubre fillUnresolvedPathVars (APP-067 · fix bug navidrome MUSIC_PATH):
// variables ${VAR} sin definir reciben un default seguro bajo CONFIG_PATH,
// pero las que ya están definidas o tienen default inline NO se tocan.

package main

import (
	"testing"
)

const testContainerPath = "/nimos/pools/data7/docker/containers/navidrome"

// TestFillUnresolvedPathVars_NavidromeMusicPath · el caso real del bug.
// El compose usa ${MUSIC_PATH} sin default y NimOS no la conoce → debe
// recibir un default seguro {containerPath}/music.
func TestFillUnresolvedPathVars_NavidromeMusicPath(t *testing.T) {
	compose := `services:
  navidrome:
    image: deluan/navidrome:latest
    volumes:
      - ${CONFIG_PATH}/data:/data
      - ${MUSIC_PATH}:/music:ro
`
	autoEnv := map[string]interface{}{
		"CONFIG_PATH": testContainerPath,
		"HOST_IP":     "192.168.1.131",
		"TZ":          "Europe/Madrid",
	}

	result := fillUnresolvedPathVars(compose, autoEnv, testContainerPath)

	// MUSIC_PATH debe haberse rellenado
	music, ok := result["MUSIC_PATH"]
	if !ok {
		t.Fatal("MUSIC_PATH no se rellenó · el deploy seguiría fallando")
	}
	want := testContainerPath + "/music"
	if music != want {
		t.Errorf("MUSIC_PATH = %v, want %q", music, want)
	}

	// CONFIG_PATH NO debe haberse tocado (ya estaba)
	if result["CONFIG_PATH"] != testContainerPath {
		t.Errorf("CONFIG_PATH se modificó: %v", result["CONFIG_PATH"])
	}
}

// TestFillUnresolvedPathVars_AlreadyDefined · variable ya en autoEnv no se toca.
func TestFillUnresolvedPathVars_AlreadyDefined(t *testing.T) {
	compose := `services:
  app:
    volumes:
      - ${MUSIC_PATH}:/music
`
	autoEnv := map[string]interface{}{
		"CONFIG_PATH": testContainerPath,
		"MUSIC_PATH":  "/mnt/biblioteca/musica", // usuario ya la definió
	}

	result := fillUnresolvedPathVars(compose, autoEnv, testContainerPath)

	// Debe conservar el valor del usuario, NO sobrescribir con default
	if result["MUSIC_PATH"] != "/mnt/biblioteca/musica" {
		t.Errorf("MUSIC_PATH = %v, want '/mnt/biblioteca/musica' (no debe pisarse)", result["MUSIC_PATH"])
	}
}

// TestFillUnresolvedPathVars_InlineDefault · variable con default inline
// ${VAR:-x} no se toca · docker-compose la resuelve sola.
func TestFillUnresolvedPathVars_InlineDefault(t *testing.T) {
	compose := `services:
  app:
    ports:
      - "${HOST_PORT:-8080}:80"
    volumes:
      - ${DATA_DIR:-/var/data}:/data
`
	autoEnv := map[string]interface{}{
		"CONFIG_PATH": testContainerPath,
	}

	result := fillUnresolvedPathVars(compose, autoEnv, testContainerPath)

	// Ninguna con default inline debe añadirse · compose las resuelve
	if _, ok := result["HOST_PORT"]; ok {
		t.Error("HOST_PORT con default inline NO debería añadirse")
	}
	if _, ok := result["DATA_DIR"]; ok {
		t.Error("DATA_DIR con default inline NO debería añadirse")
	}
}

// TestFillUnresolvedPathVars_MultipleUnresolved · varias variables sin definir.
func TestFillUnresolvedPathVars_MultipleUnresolved(t *testing.T) {
	compose := `services:
  app:
    volumes:
      - ${MUSIC_PATH}:/music
      - ${PHOTOS_DIR}:/photos
      - ${MEDIA}:/media
`
	autoEnv := map[string]interface{}{
		"CONFIG_PATH": testContainerPath,
	}

	result := fillUnresolvedPathVars(compose, autoEnv, testContainerPath)

	cases := map[string]string{
		"MUSIC_PATH": testContainerPath + "/music",
		"PHOTOS_DIR": testContainerPath + "/photos",
		"MEDIA":      testContainerPath + "/media",
	}
	for varName, want := range cases {
		got, ok := result[varName]
		if !ok {
			t.Errorf("%s no se rellenó", varName)
			continue
		}
		if got != want {
			t.Errorf("%s = %v, want %q", varName, got, want)
		}
	}
}

// TestFillUnresolvedPathVars_NoVars · compose sin variables · no añade nada.
func TestFillUnresolvedPathVars_NoVars(t *testing.T) {
	compose := `services:
  app:
    image: nginx:latest
    ports:
      - "80:80"
`
	autoEnv := map[string]interface{}{
		"CONFIG_PATH": testContainerPath,
	}

	result := fillUnresolvedPathVars(compose, autoEnv, testContainerPath)

	if len(result) != 1 {
		t.Errorf("se añadieron vars de más · result = %v", result)
	}
}

// TestFillUnresolvedPathVars_IgnoresEnvAndCommand · EL FIX CRÍTICO (01/06).
// Variables en environment: y command: NO deben tocarse · solo las de
// volúmenes. Antes, el fix convertía POSTGRES_USER y $$user de postgres en
// rutas y rompía Immich.
func TestFillUnresolvedPathVars_IgnoresEnvAndCommand(t *testing.T) {
	// Compose tipo Immich postgres · variables en environment y command
	compose := `services:
  database:
    image: tensorchord/pgvecto-rs:pg14
    environment:
      POSTGRES_PASSWORD: ${DB_PASSWORD}
      POSTGRES_USER: ${DB_USERNAME}
      POSTGRES_DB: ${DB_DATABASE_NAME}
    volumes:
      - ${DB_DATA_LOCATION}:/var/lib/postgresql/data
    command: postgres -c 'search_path="$$user", public, vectors'
`
	autoEnv := map[string]interface{}{
		"CONFIG_PATH":      testContainerPath,
		"DB_PASSWORD":      "secret",
		"DB_USERNAME":      "postgres",
		"DB_DATABASE_NAME": "immich",
		"DB_DATA_LOCATION": testContainerPath + "/postgres",
	}

	result := fillUnresolvedPathVars(compose, autoEnv, testContainerPath)

	// NINGUNA de estas debe haberse añadido/modificado como ruta:
	forbidden := []string{"POSTGRES_USER", "POSTGRES_DB", "POSTGRES_PASSWORD", "user"}
	for _, name := range forbidden {
		if v, ok := result[name]; ok {
			t.Errorf("variable %q NO debería tocarse (está en environment/command), pero se asignó %v", name, v)
		}
	}

	// Las ya definidas se conservan con su valor original
	if result["DB_USERNAME"] != "postgres" {
		t.Errorf("DB_USERNAME se modificó: %v", result["DB_USERNAME"])
	}
	if result["DB_DATA_LOCATION"] != testContainerPath+"/postgres" {
		t.Errorf("DB_DATA_LOCATION se modificó: %v", result["DB_DATA_LOCATION"])
	}
}

// TestFillUnresolvedPathVars_OnlyVolumeVars · una var sin definir en un
// volumen SÍ se rellena, pero la misma forma en environment NO.
func TestFillUnresolvedPathVars_OnlyVolumeVars(t *testing.T) {
	compose := `services:
  app:
    image: someapp:latest
    environment:
      SOME_CONFIG: ${SOME_CONFIG}
    volumes:
      - ${MEDIA_PATH}:/media
`
	autoEnv := map[string]interface{}{
		"CONFIG_PATH": testContainerPath,
	}

	result := fillUnresolvedPathVars(compose, autoEnv, testContainerPath)

	// MEDIA_PATH (en volumen) SÍ debe rellenarse
	if _, ok := result["MEDIA_PATH"]; !ok {
		t.Error("MEDIA_PATH (en volumen) debería rellenarse")
	}
	// SOME_CONFIG (en environment) NO debe tocarse
	if _, ok := result["SOME_CONFIG"]; ok {
		t.Error("SOME_CONFIG (en environment) NO debería tocarse")
	}
}

// TestDefaultDirNameForVar · derivación de nombre de directorio.
func TestDefaultDirNameForVar(t *testing.T) {
	cases := map[string]string{
		"MUSIC_PATH":       "music",
		"PHOTOS_DIR":       "photos",
		"MEDIA_LOCATION":   "media",
		"DOWNLOADS_FOLDER": "downloads",
		"MEDIA":            "media",
		"DATA":             "data",
	}
	for varName, want := range cases {
		got := defaultDirNameForVar(varName)
		if got != want {
			t.Errorf("defaultDirNameForVar(%q) = %q, want %q", varName, got, want)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────
// APP-068 · Permisos por UID de imagen
// ─────────────────────────────────────────────────────────────────────────

func TestResolveVolumeHostPath(t *testing.T) {
	env := map[string]interface{}{
		"CONFIG_PATH":      "/nimos/pools/data8/docker/containers/grafana",
		"DB_DATA_LOCATION": "/nimos/pools/data8/docker/containers/immich/postgres",
		"UPLOAD_LOCATION":  "/nimos/pools/data8/docker/containers/immich/upload",
	}
	cases := []struct {
		name string
		vol  string
		want string
	}{
		{"grafana data", "${CONFIG_PATH}/data:/var/lib/grafana", "/nimos/pools/data8/docker/containers/grafana/data"},
		{"postgres", "${DB_DATA_LOCATION}:/var/lib/postgresql/data", "/nimos/pools/data8/docker/containers/immich/postgres"},
		{"upload con opts", "${UPLOAD_LOCATION}:/usr/src/app/upload:rw", "/nimos/pools/data8/docker/containers/immich/upload"},
		{"volumen con nombre", "model-cache:/cache", ""},                                   // no bind mount
		{"localtime fuera del pool", "/etc/localtime:/etc/localtime:ro", "/etc/localtime"}, // resuelve pero fuera del pool (se filtra luego)
		{"var no resuelta", "${UNKNOWN_VAR}:/data", ""},                                    // queda con $ → no resoluble
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveVolumeHostPath(c.vol, env)
			if got != c.want {
				t.Errorf("resolveVolumeHostPath(%q) = %q, want %q", c.vol, got, c.want)
			}
		})
	}
}

func TestExpandComposeVars(t *testing.T) {
	env := map[string]interface{}{
		"CONFIG_PATH": "/nimos/pools/data8/x",
		"PORT":        3001,
	}
	cases := map[string]string{
		"${CONFIG_PATH}/data": "/nimos/pools/data8/x/data",
		"$CONFIG_PATH":        "/nimos/pools/data8/x",
		"port ${PORT}":        "port 3001",
		"${MISSING}/y":        "${MISSING}/y", // no resuelta · se deja
		"sin vars":            "sin vars",
	}
	for in, want := range cases {
		got := expandComposeVars(in, env)
		if got != want {
			t.Errorf("expandComposeVars(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestApplyUIDPermissions_ParsesMultiService · verifica que el parser entiende
// un compose multi-servicio (Immich) y detecta el volumen de BD.
// TestDecideVolumePlan_DBvsNormal · verifica la decisión de permisos por
// volumen del nuevo modelo (Fase 2): BD → 0700 UID fijo; UID fijo de imagen →
// 0750 con ese UID; flexible → 0750 con el UID asignado.
func TestDecideVolumePlan_DBvsNormal(t *testing.T) {
	const appUID, appGID = 100005, 100005

	// 1. Volumen de BD (postgres) · imagen sin UID declarado → 999, 0700
	db := decideVolumePlan(appUID, appGID, "", "/var/lib/postgresql/data", "/nimos/pools/data8/x/pg")
	if !db.IsDB || db.Mode != "0700" || db.UID != 999 {
		t.Errorf("BD sin UID: esperaba {IsDB:true Mode:0700 UID:999}, got %+v", db)
	}

	// 2. Volumen de BD con UID de imagen explícito (1001) → respeta 1001, 0700
	db2 := decideVolumePlan(appUID, appGID, "1001", "/var/lib/mysql", "/nimos/pools/data8/x/my")
	if !db2.IsDB || db2.Mode != "0700" || db2.UID != 1001 {
		t.Errorf("BD con UID 1001: esperaba {IsDB:true Mode:0700 UID:1001}, got %+v", db2)
	}

	// 3. UID fijo de imagen no-BD (synapse 991) → respeta 991, 0750
	syn := decideVolumePlan(appUID, appGID, "991", "/data", "/nimos/pools/data8/x/syn")
	if syn.IsDB || syn.Mode != "0750" || syn.UID != 991 {
		t.Errorf("UID fijo 991: esperaba {IsDB:false Mode:0750 UID:991}, got %+v", syn)
	}

	// 4. App flexible (imagen sin UID / root) → UID ASIGNADO, 0750
	flex := decideVolumePlan(appUID, appGID, "", "/usr/src/app/upload", "/nimos/pools/data8/x/up")
	if flex.IsDB || flex.Mode != "0750" || flex.UID != appUID {
		t.Errorf("flexible: esperaba {IsDB:false Mode:0750 UID:%d}, got %+v", appUID, flex)
	}

	// 5. Imagen con user "root" explícito → tratado como flexible (UID asignado)
	rootImg := decideVolumePlan(appUID, appGID, "root", "/config", "/nimos/pools/data8/x/cfg")
	if rootImg.UID != appUID {
		t.Errorf("imagen root: debería usar el UID asignado %d, got %d", appUID, rootImg.UID)
	}
}

// TestIsDBContainerPath · verifica la detección de rutas de BD.
func TestIsDBContainerPath(t *testing.T) {
	dbPaths := map[string]bool{
		"/var/lib/postgresql/data":  true,
		"/var/lib/postgresql/data/": true, // con barra final
		"/var/lib/mysql":            true,
		"/var/lib/mariadb":          true,
		"/data/db":                  true,
		"/usr/src/app/upload":       false, // immich upload · no es BD
		"/var/lib/grafana":          false, // grafana · no es BD
		"/config":                   false, // linuxserver · no es BD
		"/media":                    false,
	}
	for path, want := range dbPaths {
		if got := isDBContainerPath(path); got != want {
			t.Errorf("isDBContainerPath(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestVolumeContainerPath · extracción del lado-container del volumen.
func TestVolumeContainerPath(t *testing.T) {
	cases := map[string]string{
		"${DB_DATA}:/var/lib/postgresql/data": "/var/lib/postgresql/data",
		"${CONFIG}/data:/var/lib/grafana":     "/var/lib/grafana",
		"${UPLOAD}:/usr/src/app/upload:rw":    "/usr/src/app/upload",
		"model-cache:/cache":                  "/cache",
		"/etc/localtime:/etc/localtime:ro":    "/etc/localtime",
		"singlepart":                          "",
	}
	for vol, want := range cases {
		if got := volumeContainerPath(vol); got != want {
			t.Errorf("volumeContainerPath(%q) = %q, want %q", vol, got, want)
		}
	}
}
