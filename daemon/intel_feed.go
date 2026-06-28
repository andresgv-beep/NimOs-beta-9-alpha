// intel_feed.go — NimShield Intelligence · descarga, caché y estado del feed.
//
// FASE B: NimOS descarga el feed de raw.githubusercontent (como probamos a
// mano), lo verifica (FASE A), lo cachea en SQLite y mantiene el trie activo.
// Incluye anti-replay (no aplicar un feed_version menor que el vigente) y
// rollback (guardamos la versión previa para volver si la nueva sale mala).
package main

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

// intelFeedBaseURL es de dónde NimOS baja el feed. Apunta al repo público de
// la fábrica. Configurable a futuro; por ahora constante (la fuente oficial).
const intelFeedBaseURL = "https://raw.githubusercontent.com/andresgv-beep/nimshield-intelligence/main/latest"

// intelMaxFileBytes acota cada descarga (anti fichero gigante).
const intelMaxFileBytes = 25 * 1024 * 1024 // 25 MB

// IntelState es el estado vivo del feed en memoria.
type IntelState struct {
	trie        *IntelTrie
	feedVersion int
	generatedAt string
	loadedAt    time.Time
	source      string // "embedded" | "cache" | "network"
	observeOnly bool   // true mientras el feed esté en modo observación
}

// intelActive es el trie/estado en uso por el hot path. Arranca vacío.
var intelActive = &IntelState{trie: newIntelTrie(), source: "none"}

// ─── Caché en SQLite ───
//
// Guardamos los ficheros crudos del feed (manifest, sig, blocklists) por
// feed_version, para: (a) arrancar sin red con el último bueno, (b) rollback.

func dbIntelInit() {
	if db == nil {
		return
	}
	db.Exec(`
		CREATE TABLE IF NOT EXISTS intel_feed (
			feed_version INTEGER NOT NULL,
			filename     TEXT NOT NULL,
			content      BLOB NOT NULL,
			fetched_at   TEXT NOT NULL,
			PRIMARY KEY (feed_version, filename)
		);
	`)
	db.Exec(`
		CREATE TABLE IF NOT EXISTS intel_meta (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
	`)
}

func dbIntelCurrentVersion() int {
	if db == nil {
		return 0
	}
	var v int
	db.QueryRow(`SELECT value FROM intel_meta WHERE key = 'current_version'`).Scan(&v)
	return v
}

func dbIntelSetCurrentVersion(v int) {
	if db == nil {
		return
	}
	db.Exec(`INSERT OR REPLACE INTO intel_meta (key, value) VALUES ('current_version', ?)`, v)
}

// dbIntelStore guarda los ficheros de una versión y poda versiones viejas
// (conservamos las últimas 3 para rollback: current/previous/previous-2).
func dbIntelStore(version int, files map[string][]byte) error {
	if db == nil {
		return fmt.Errorf("sin DB")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	for name, content := range files {
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO intel_feed (feed_version, filename, content, fetched_at) VALUES (?,?,?,?)`,
			version, name, content, now,
		); err != nil {
			tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// poda: deja solo las 3 versiones más recientes
	db.Exec(`DELETE FROM intel_feed WHERE feed_version NOT IN (
		SELECT DISTINCT feed_version FROM intel_feed ORDER BY feed_version DESC LIMIT 3
	)`)
	return nil
}

// dbIntelLoadVersion recupera los ficheros de una versión cacheada.
func dbIntelLoadVersion(version int) (map[string][]byte, error) {
	if db == nil {
		return nil, fmt.Errorf("sin DB")
	}
	rows, err := db.Query(`SELECT filename, content FROM intel_feed WHERE feed_version = ?`, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := map[string][]byte{}
	for rows.Next() {
		var name string
		var content []byte
		if err := rows.Scan(&name, &content); err != nil {
			return nil, err
		}
		files[name] = content
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("versión %d no está en caché", version)
	}
	return files, nil
}

// ─── Descarga de red ───

func intelFetch(client *http.Client, name string) ([]byte, error) {
	url := intelFeedBaseURL + "/" + name
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d para %s", resp.StatusCode, name)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, intelMaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > intelMaxFileBytes {
		return nil, fmt.Errorf("%s excede el tope de tamaño", name)
	}
	return data, nil
}

// ─── Aplicar un feed (desde bytes ya en mano: red o caché) ───
//
// applyFeed verifica firma + hashes, construye el trie y lo activa. NO toca el
// hot path hasta que TODO está validado (swap atómico al final). Devuelve la
// versión aplicada o error (en cuyo caso el trie vigente se queda intacto).
func applyFeed(manifestBytes, sigBytes []byte, fileLoader func(name string) ([]byte, error), source string, enforceNewer bool) (int, error) {
	// 1. firma
	if err := verifyManifestSignature(manifestBytes, string(sigBytes)); err != nil {
		return 0, err
	}
	// 2. manifest
	m, err := parseManifest(manifestBytes)
	if err != nil {
		return 0, err
	}
	// 3. anti-replay: no aplicar un feed más viejo que el vigente
	if enforceNewer {
		cur := dbIntelCurrentVersion()
		if m.FeedVersion < cur {
			return 0, fmt.Errorf("feed v%d es más viejo que el vigente v%d — ignorado (anti-replay)", m.FeedVersion, cur)
		}
	}
	// 4. cargar ficheros y validar hashes → construir trie
	files := map[string][]byte{"manifest.json": manifestBytes, "manifest.json.sig": sigBytes}
	contentByName := map[string][]byte{}
	for _, f := range m.Files {
		b, err := fileLoader(f.Name)
		if err != nil {
			return 0, fmt.Errorf("cargando %s: %w", f.Name, err)
		}
		files[f.Name] = b
		contentByName[f.Name] = b
	}
	trie, summary, err := buildTrieFromManifest(m, contentByName)
	if err != nil {
		return 0, err
	}
	// 5. ¿el feed está en modo observación? (si TODOS sus ficheros lo están)
	observeOnly := true
	for _, f := range m.Files {
		if f.Action == "block" {
			observeOnly = false
		}
	}
	// 6. SWAP atómico: a partir de aquí el hot path usa el trie nuevo
	intelActive.trie.swapFrom(trie)
	intelActive.feedVersion = m.FeedVersion
	intelActive.generatedAt = m.GeneratedAt
	intelActive.loadedAt = time.Now()
	intelActive.source = source
	intelActive.observeOnly = observeOnly

	for _, s := range summary {
		logMsg("intel: %s", s)
	}
	logMsg("intel: feed v%d activo (%d prefijos, fuente=%s, observe=%v)",
		m.FeedVersion, trie.size(), source, observeOnly)
	return m.FeedVersion, nil
}

// ─── Orquestación: refrescar desde la red, con caché y rollback ───

// intelRefresh baja el feed de la red, lo aplica y lo cachea. Si la red falla
// o el feed nuevo no valida, NO toca el feed vigente. Devuelve la versión
// activa tras el intento.
func intelRefresh() (int, error) {
	client := &http.Client{Timeout: 60 * time.Second}

	manifestBytes, err := intelFetch(client, "manifest.json")
	if err != nil {
		return intelActive.feedVersion, fmt.Errorf("descargando manifest: %w", err)
	}
	sigBytes, err := intelFetch(client, "manifest.json.sig")
	if err != nil {
		return intelActive.feedVersion, fmt.Errorf("descargando firma: %w", err)
	}

	// cache de ficheros descargados para guardarlos si todo va bien
	downloaded := map[string][]byte{}
	loader := func(name string) ([]byte, error) {
		b, err := intelFetch(client, name)
		if err != nil {
			return nil, err
		}
		downloaded[name] = b
		return b, nil
	}

	version, err := applyFeed(manifestBytes, sigBytes, loader, "network", true)
	if err != nil {
		return intelActive.feedVersion, err
	}

	// persistir en caché (incluye manifest + sig + blocklists)
	downloaded["manifest.json"] = manifestBytes
	downloaded["manifest.json.sig"] = sigBytes
	if err := dbIntelStore(version, downloaded); err != nil {
		logMsg("intel: aviso, no pude cachear v%d: %v", version, err)
	} else {
		dbIntelSetCurrentVersion(version)
	}
	return version, nil
}

// intelLoadFromCache carga el último feed bueno de la caché (arranque sin red).
func intelLoadFromCache() (int, error) {
	version := dbIntelCurrentVersion()
	if version == 0 {
		return 0, fmt.Errorf("no hay feed en caché")
	}
	files, err := dbIntelLoadVersion(version)
	if err != nil {
		return 0, err
	}
	manifestBytes, ok := files["manifest.json"]
	if !ok {
		return 0, fmt.Errorf("caché v%d sin manifest", version)
	}
	sigBytes := files["manifest.json.sig"]
	loader := func(name string) ([]byte, error) {
		b, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("%s no está en la caché", name)
		}
		return b, nil
	}
	// enforceNewer=false: la caché es nuestra fuente de arranque, no replay
	return applyFeed(manifestBytes, sigBytes, loader, "cache", false)
}

// intelRollback vuelve a la versión previa cacheada (si la actual salió mala).
func intelRollback() (int, error) {
	cur := dbIntelCurrentVersion()
	// buscar la mayor versión < cur que tengamos en caché
	var prev int
	if db != nil {
		db.QueryRow(`SELECT MAX(feed_version) FROM intel_feed WHERE feed_version < ?`, cur).Scan(&prev)
	}
	if prev == 0 {
		return cur, fmt.Errorf("no hay versión previa para rollback")
	}
	files, err := dbIntelLoadVersion(prev)
	if err != nil {
		return cur, err
	}
	loader := func(name string) ([]byte, error) {
		b, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("%s no está en caché", name)
		}
		return b, nil
	}
	v, err := applyFeed(files["manifest.json"], files["manifest.json.sig"], loader, "cache", false)
	if err != nil {
		return cur, err
	}
	dbIntelSetCurrentVersion(v)
	logMsg("intel: ROLLBACK a v%d", v)
	return v, nil
}
