// apps_game.go — Panel de Juego (openMode "game").
//
// Servidores de juego (Minecraft, etc.) NO son webapps: no se abren en
// navegador ni van por Caddy HTTP. El cliente del juego conecta directo a
// host:puerto (TCP/UDP del juego). Este endpoint compone la información que
// el Modal de Juego del frontend necesita: las direcciones de conexión
// (local y externa) y el puerto del juego.
//
// GET /api/apps/{id}/game-info → GameInfo JSON
//
// Reusa piezas existentes (sin reinventar):
//   · getStackHostIP()          · IP local del NAS (docker_async.go)
//   · networkRepo.GetExposureConfig() · dominio DuckDNS base (si está configurado)
//   · app.parsedPorts()         · puerto(s) del juego (db_apps.go)
package main

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// GameInfo es la vista que consume el Modal de Juego del frontend.
type GameInfo struct {
	AppID          string `json:"app_id"`
	Name           string `json:"name"`
	Port           int    `json:"port"`            // puerto del juego (host)
	Protocol       string `json:"protocol"`        // "tcp" | "udp"
	LocalAddress   string `json:"local_address"`   // ej. "192.168.1.131:25565"
	ExternalAddress string `json:"external_address,omitempty"` // ej. "nimbarraca.duckdns.org:25565" (vacío si no hay dominio)
	FilesPath      string `json:"files_path"`      // ruta absoluta de la carpeta del server
	FilesShare      string `json:"files_share,omitempty"`     // share que CONTIENE esa carpeta (vacío si ninguno)
	FilesRelPath    string `json:"files_rel_path,omitempty"`  // ruta relativa dentro del share (ej. "/minecraft-java")
	RconEnabled    bool   `json:"rcon_enabled"`    // si la app soporta consola RCON
}

// composeGameAddress compone una dirección de conexión "host:puerto".
// Pura y testeable. Devuelve "" si falta el host (para que el frontend
// sepa que esa dirección no está disponible).
func composeGameAddress(host string, port int) string {
	host = strings.TrimSpace(host)
	if host == "" || port <= 0 {
		return ""
	}
	return fmt.Sprintf("%s:%d", host, port)
}

// gamePortFromApp extrae el puerto principal del juego y su protocolo de la
// app. Toma el primer PortBinding (el puerto declarado del juego). Pura.
func gamePortFromApp(app *DBDockerApp) (int, string) {
	if app == nil {
		return 0, "tcp"
	}
	ports := app.parsedPorts()
	if len(ports) == 0 {
		return 0, "tcp"
	}
	p := ports[0]
	proto := p.Protocol
	if proto == "" {
		proto = "tcp"
	}
	// Host port es el que ve el jugador (el publicado en el NAS).
	port := p.Host
	if port <= 0 {
		port = p.Declared
	}
	return port, proto
}

// handleAppsSubRoutes despacha las rutas /api/apps/{id}/... (con id variable).
// Las rutas exactas sin id (ej. /api/apps/launchable) se registran aparte y
// tienen prioridad en el mux. Aquí van las que llevan {id} en medio.
//
// Rutas soportadas:
//   GET  /api/apps/{id}/game-info  · info del Panel de Juego (direcciones, puerto)
//   POST /api/apps/{id}/rcon       · ejecuta un comando RCON en el servidor
func handleAppsSubRoutes(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/game-info"):
		handleGameInfo(w, r)
	case strings.HasSuffix(r.URL.Path, "/rcon"):
		handleGameRcon(w, r)
	default:
		jsonError(w, http.StatusNotFound, "unknown apps route")
	}
}

// handleGameInfo · GET /api/apps/{id}/game-info
func handleGameInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// Extraer {id} de la ruta /api/apps/{id}/game-info
	path := strings.TrimPrefix(r.URL.Path, "/api/apps/")
	id := strings.TrimSuffix(path, "/game-info")
	id = strings.Trim(id, "/")
	if id == "" {
		jsonError(w, http.StatusBadRequest, "missing app id")
		return
	}

	ctx := r.Context()
	if appsRepo == nil {
		jsonError(w, http.StatusInternalServerError, "apps repo not available")
		return
	}
	app, err := appsRepo.GetDockerApp(ctx, id)
	if err != nil || app == nil {
		jsonError(w, http.StatusNotFound, "app not found")
		return
	}

	port, proto := gamePortFromApp(app)

	// IP local del NAS (siempre disponible).
	localIP := getStackHostIP()
	localAddr := composeGameAddress(localIP, port)

	// Dominio externo (DuckDNS) · solo si está configurado en Network.
	externalAddr := ""
	if networkRepo != nil {
		if cfg, err := networkRepo.GetExposureConfig(ctx); err == nil {
			if cfg.BaseDomain != "" {
				externalAddr = composeGameAddress(cfg.BaseDomain, port)
			}
		}
	}

	// Ruta de ficheros del server (la carpeta del container en el pool).
	filesPath := gameFilesPath(id)
	// Share que contiene esa carpeta (si existe uno) → para deep-link en Files.
	filesShare, filesRel := resolveShareForPath(filesPath)

	info := GameInfo{
		AppID:           id,
		Name:            app.Name,
		Port:            port,
		Protocol:        proto,
		LocalAddress:    localAddr,
		ExternalAddress: externalAddr,
		FilesPath:       filesPath,
		FilesShare:      filesShare,
		FilesRelPath:    filesRel,
		RconEnabled:     gameHasRcon(app),
	}
	jsonOk(w, info)
}

// gameFilesPath devuelve la ruta de la carpeta de datos del server de juego,
// para abrirla en el FileManager. Es el volumen del container en el pool.
func gameFilesPath(appID string) string {
	dockerPath, err := getDockerPath()
	if err != nil || dockerPath == "" {
		return ""
	}
	return fmt.Sprintf("%s/containers/%s", strings.TrimRight(dockerPath, "/"), appID)
}

// sharePathEntry es un par (nombre, ruta base) de share, para el matcher puro.
type sharePathEntry struct {
	Name string
	Path string
}

// pickShareForPath elige, de una lista de shares, el que CONTIENE absPath y
// devuelve (nombreShare, rutaRelativaDentroDelShare). Si varios lo contienen,
// gana el más específico (la base más larga). Si ninguno lo contiene devuelve
// ("",""), y el frontend deshabilita el botón de Ficheros. Pura y testeable.
//
// El match es en frontera de directorio (igual a la base, o dentro de base/…),
// para no confundir "/pool/containers" con "/pool/containers-backup".
func pickShareForPath(absPath string, shares []sharePathEntry) (string, string) {
	if absPath == "" {
		return "", ""
	}
	clean := filepath.Clean(absPath)
	bestName := ""
	bestBase := ""
	for _, s := range shares {
		if s.Path == "" {
			continue
		}
		base := filepath.Clean(s.Path)
		if clean == base || strings.HasPrefix(clean, base+"/") {
			if len(base) > len(bestBase) { // el más específico gana
				bestName = s.Name
				bestBase = base
			}
		}
	}
	if bestName == "" {
		return "", ""
	}
	rel := strings.TrimPrefix(clean, bestBase)
	if rel == "" {
		rel = "/"
	}
	if !strings.HasPrefix(rel, "/") {
		rel = "/" + rel
	}
	return bestName, rel
}

// resolveShareForPath resuelve, contra los shares de la BD, cuál contiene
// absPath. Devuelve (nombreShare, rutaRelativa) o ("","") si ninguno.
func resolveShareForPath(absPath string) (string, string) {
	raw, err := dbSharesListRaw()
	if err != nil {
		return "", ""
	}
	entries := make([]sharePathEntry, 0, len(raw))
	for _, s := range raw {
		entries = append(entries, sharePathEntry{Name: s.Name, Path: s.Path})
	}
	return pickShareForPath(absPath, entries)
}

// gameHasRcon indica si la app tiene RCON habilitado, según lo declarado en
// el catálogo (bloque game.rcon, persistido en config). Si el catálogo no lo
// declara pero es openMode "game", asumimos RCON (caso Minecraft típico).
func gameHasRcon(app *DBDockerApp) bool {
	if app == nil {
		return false
	}
	_, _, enabled := gameConfigFromConfig(app.Config)
	if enabled {
		return true
	}
	// Fallback: openMode game sin bloque rcon declarado → asumimos sí.
	return app.OpenMode == "game"
}

// ── Consola RCON ──

// rconExecuteFunc es indirección para poder testear el handler sin un server
// real. En producción apunta a rconExecute (el cliente real).
var rconExecuteFunc = rconExecute

// handleGameRcon · POST /api/apps/{id}/rcon · body {command}
//
// Ejecuta un comando RCON en el servidor de juego y devuelve la respuesta.
// Lee el RCON_PASSWORD del .env de la app (guardado al instalar, 0600).
// El puerto RCON se conecta en localhost (el container lo publica en
// 127.0.0.1, nunca expuesto fuera · seguridad).
func handleGameRcon(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/apps/")
	id := strings.TrimSuffix(path, "/rcon")
	id = strings.Trim(id, "/")
	if id == "" {
		jsonError(w, http.StatusBadRequest, "missing app id")
		return
	}

	body, err := readBody(r)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid body")
		return
	}
	command := strings.TrimSpace(bodyStr(body, "command"))
	if command == "" {
		jsonError(w, http.StatusBadRequest, "missing command")
		return
	}

	// Config RCON del catálogo (persistida en docker_apps.config). El núcleo
	// NO asume la variable ni el puerto · los lee de lo que declaró el catálogo.
	// Si la app no declaró game.rcon, usa defaults de Minecraft.
	app, err := appsRepo.GetDockerApp(r.Context(), id)
	if err != nil || app == nil {
		jsonError(w, http.StatusNotFound, "app not found")
		return
	}
	passwordEnv, rconPort, _ := gameConfigFromConfig(app.Config)

	// Leer la password del .env del stack (la variable que declaró el catálogo).
	dockerPath, err := getDockerPath()
	if err != nil || dockerPath == "" {
		jsonError(w, http.StatusInternalServerError, "docker path unavailable")
		return
	}
	envPath := filepath.Join(dockerPath, "stacks", id, ".env")
	env := readEnvFile(envPath)
	password := env[passwordEnv]
	if password == "" {
		jsonError(w, http.StatusBadRequest, "esta app no tiene RCON configurado (sin "+passwordEnv+")")
		return
	}

	// El puerto RCON puede venir también por .env (override del usuario).
	if p := env["RCON_PORT"]; p != "" {
		if n, ok := atoiSafe(p); ok && n > 0 {
			rconPort = n
		}
	}

	// El RCON se publica en 127.0.0.1 (localhost) por seguridad · nunca
	// expuesto a internet. NimOS corre en el host, así que conecta por
	// localhost directamente.
	host := "127.0.0.1"

	resp, err := rconExecuteFunc(host, rconPort, password, command, 5*time.Second)
	if err != nil {
		jsonOk(w, map[string]interface{}{
			"command":  command,
			"response": "",
			"error":    rconUserError(err),
		})
		return
	}
	jsonOk(w, map[string]interface{}{
		"command":  command,
		"response": resp,
		"error":    "",
	})
}

// rconUserError traduce errores técnicos a mensajes claros para el usuario.
func rconUserError(err error) string {
	switch {
	case err == errRconAuthFailed:
		return "Autenticación RCON fallida · revisa la contraseña RCON."
	case strings.Contains(err.Error(), "connection refused"):
		return "El servidor no responde al RCON · ¿está arrancado? Puede tardar un poco al iniciar."
	case strings.Contains(err.Error(), "timeout"), strings.Contains(err.Error(), "i/o timeout"):
		return "El servidor RCON no respondió a tiempo."
	default:
		return "No se pudo ejecutar el comando: " + err.Error()
	}
}

// atoiSafe convierte string a int sin paniquear.
func atoiSafe(s string) (int, bool) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, len(s) > 0
}
