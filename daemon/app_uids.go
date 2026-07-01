// app_uids.go — Asignación de UIDs únicos por app Docker (Refactor permisos, Fase 1).
//
// MODELO (ver PERMISOS-DESIGN.md):
//   · Cada app Docker tiene un UID/GID ÚNICO y dedicado, asignado por NimOS.
//   · Rango dedicado [appUIDBase, appUIDMax] — fuera de usuarios humanos (1000+)
//     y de sistema (1-999). Idéntico en ARM64 y amd64 (UIDs son de Linux).
//   · NO se reusan UIDs entre apps distintas: el contador (uid_allocator) solo
//     sube. Razón de seguridad: desinstalar-normal CONSERVA datos en disco, así
//     que reusar un UID heredaría datos de otra app (ataque de reciclaje de UID
//     + corrupción de datos del usuario).
//   · Reinstalar la MISMA app (mismo app_id) reusa SU propio UID → recupera sus
//     datos conservados.
//   · Desinstalar (normal o total) marca released_at pero NO libera el UID.
//
// Este módulo NO aplica permisos a archivos (eso es Fase 2). Solo:
//   · Gestiona las tablas app_uids + uid_allocator.
//   · Asigna/reusa el UID de una app.
//   · Crea el usuario de sistema correspondiente (useradd).
//
// Multi-arch: useradd con UIDs altos funciona igual en ARM64 y amd64.

package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// appUIDBase · primer UID del rango dedicado a apps Docker.
	// 100000 está muy por encima de usuarios humanos (1000+) y de sistema
	// (1-999), evitando colisiones. Docker userns-remap usa rangos similares.
	appUIDBase = 100000
	// appUIDMax · último UID del rango. 100000..165535 = 65536 UIDs, más que
	// suficiente (jamás se agota en un NAS, aun sin reusar).
	appUIDMax = 165535
	// appUserPrefix · prefijo del usuario de sistema creado por app.
	// El usuario se llama nimos-app-<appID-sanitizado>.
	appUserPrefix = "nimos-app-"
)

// AppUID representa la asignación de UID/GID de una app (fila de app_uids).
type AppUID struct {
	AppID      string
	UID        int
	GID        int
	AssignedAt string
	ReleasedAt string // "" = activa; ISO timestamp = desinstalada (normal o total)
}

// sanitizeAppUserName convierte un app_id en un nombre de usuario de sistema
// válido: minúsculas, [a-z0-9-], sin guiones/underscores iniciales problemáticos.
// useradd acepta nombres con guiones; normalizamos underscores a guiones.
func sanitizeAppUserName(appID string) string {
	s := strings.ToLower(strings.TrimSpace(appID))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteByte('-')
		default:
			// cualquier otro carácter → guion (evita nombres inválidos)
			b.WriteByte('-')
		}
	}
	name := appUserPrefix + b.String()
	// useradd limita el nombre a 32 chars en muchos sistemas. Truncamos seguro.
	if len(name) > 32 {
		name = name[:32]
	}
	// No puede terminar en guion (cosmético, pero evita rarezas).
	return strings.TrimRight(name, "-")
}

// initAppUIDsSchema crea las tablas app_uids + uid_allocator si no existen e
// inicializa el contador. Idempotente. Debe llamarse tras initAppsSchema.
func initAppUIDsSchema(db *sql.DB) error {
	const ddl = `
	CREATE TABLE IF NOT EXISTS uid_allocator (
		id       INTEGER PRIMARY KEY CHECK (id = 1),  -- fila única
		next_uid INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS app_uids (
		app_id      TEXT PRIMARY KEY,
		uid         INTEGER NOT NULL UNIQUE,
		gid         INTEGER NOT NULL,
		assigned_at TEXT NOT NULL,
		released_at TEXT  -- NULL = activa
	);
	CREATE INDEX IF NOT EXISTS idx_app_uids_uid ON app_uids(uid);`
	if _, err := db.Exec(ddl); err != nil {
		return fmt.Errorf("app_uids schema: %w", err)
	}
	// Inicializar el contador a appUIDBase si la fila no existe. El INSERT OR
	// IGNORE no toca el valor si ya está (preserva el contador entre arranques).
	if _, err := db.Exec(
		`INSERT OR IGNORE INTO uid_allocator (id, next_uid) VALUES (1, ?)`,
		appUIDBase,
	); err != nil {
		return fmt.Errorf("app_uids allocator init: %w", err)
	}
	return nil
}

// getAppUID devuelve la asignación existente de una app, o nil si no tiene.
func getAppUID(db *sql.DB, appID string) (*AppUID, error) {
	var a AppUID
	var released sql.NullString
	err := db.QueryRow(
		`SELECT app_id, uid, gid, assigned_at, released_at FROM app_uids WHERE app_id = ?`,
		appID,
	).Scan(&a.AppID, &a.UID, &a.GID, &a.AssignedAt, &released)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getAppUID(%s): %w", appID, err)
	}
	if released.Valid {
		a.ReleasedAt = released.String
	}
	return &a, nil
}

// assignAppUID asigna (o reusa) el UID de una app, dentro de una transacción.
//
//   - Si la app YA tiene UID (activa o released) → reusa SU uid (reinstalación:
//     sus datos conservados siguen siendo suyos). Limpia released_at (re-activa).
//   - Si es nueva → toma next_uid del allocator, lo incrementa (NUNCA reusa el
//     de otra app), registra la fila.
//
// NO crea el usuario de sistema (eso es ensureAppSystemUser, separado para
// testear la lógica de allocator sin tocar el sistema). Devuelve el AppUID.
//
// nowISO permite inyectar el timestamp en tests (determinismo).
func assignAppUID(db *sql.DB, appID string, nowISO string) (*AppUID, error) {
	if strings.TrimSpace(appID) == "" {
		return nil, fmt.Errorf("assignAppUID: appID vacío")
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("assignAppUID begin: %w", err)
	}
	defer tx.Rollback()

	// ¿Ya tiene UID? (reinstalación · reusa el suyo)
	var uid, gid int
	var released sql.NullString
	err = tx.QueryRow(
		`SELECT uid, gid, released_at FROM app_uids WHERE app_id = ?`, appID,
	).Scan(&uid, &gid, &released)

	switch {
	case err == nil:
		// Existe · re-activar (limpiar released_at) y reusar su uid/gid.
		if _, err := tx.Exec(
			`UPDATE app_uids SET released_at = NULL WHERE app_id = ?`, appID,
		); err != nil {
			return nil, fmt.Errorf("assignAppUID reactivate: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("assignAppUID commit(reuse): %w", err)
		}
		return &AppUID{AppID: appID, UID: uid, GID: gid, AssignedAt: "", ReleasedAt: ""}, nil

	case err == sql.ErrNoRows:
		// Nueva · tomar next_uid y avanzar el contador.
		var next int
		if err := tx.QueryRow(
			`SELECT next_uid FROM uid_allocator WHERE id = 1`,
		).Scan(&next); err != nil {
			return nil, fmt.Errorf("assignAppUID read allocator: %w", err)
		}
		if next > appUIDMax {
			return nil, fmt.Errorf("assignAppUID: rango de UIDs agotado (next=%d > max=%d)", next, appUIDMax)
		}
		// GID = UID (cada app su propio grupo dedicado, mismo número).
		newUID, newGID := next, next
		if _, err := tx.Exec(
			`INSERT INTO app_uids (app_id, uid, gid, assigned_at, released_at) VALUES (?, ?, ?, ?, NULL)`,
			appID, newUID, newGID, nowISO,
		); err != nil {
			return nil, fmt.Errorf("assignAppUID insert: %w", err)
		}
		if _, err := tx.Exec(
			`UPDATE uid_allocator SET next_uid = ? WHERE id = 1`, next+1,
		); err != nil {
			return nil, fmt.Errorf("assignAppUID bump allocator: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("assignAppUID commit(new): %w", err)
		}
		return &AppUID{AppID: appID, UID: newUID, GID: newGID, AssignedAt: nowISO, ReleasedAt: ""}, nil

	default:
		return nil, fmt.Errorf("assignAppUID query: %w", err)
	}
}

// releaseAppUID marca el UID de una app como liberado (desinstalación). NO borra
// la fila ni libera el número (no se reusa). Idempotente.
func releaseAppUID(db *sql.DB, appID string, nowISO string) error {
	_, err := db.Exec(
		`UPDATE app_uids SET released_at = ? WHERE app_id = ? AND released_at IS NULL`,
		nowISO, appID,
	)
	if err != nil {
		return fmt.Errorf("releaseAppUID(%s): %w", appID, err)
	}
	return nil
}

// ensureAppSystemUser crea el usuario de sistema para el UID/GID asignado, si no
// existe ya. Idempotente. Crea primero el grupo (groupadd) y luego el usuario
// (useradd) sin login, sin home, de sistema.
//
// Separado de assignAppUID para poder testear la lógica de allocator sin tocar
// el sistema. Usa runSafe (no falla la instalación si algo va mal · loguea).
func ensureAppSystemUser(appID string, uid, gid int) {
	userName := sanitizeAppUserName(appID)
	groupName := userName // mismo nombre para el grupo dedicado

	// ¿El UID ya existe en el sistema? (idempotencia · no recrear)
	if _, ok := runSafe("getent", "passwd", strconv.Itoa(uid)); ok {
		return // ya existe un usuario con ese UID · no tocar
	}

	// Grupo dedicado con el GID exacto.
	if _, ok := runSafe("getent", "group", strconv.Itoa(gid)); !ok {
		runSafe("groupadd", "-g", strconv.Itoa(gid), groupName)
	}
	// Usuario de sistema con UID fijo y alto. NO usamos -r (espera UIDs 1-999,
	// emite warning SYS_UID_MAX). Como damos el UID explícito con -u, -M (sin
	// home), nologin y -N (no crear grupo propio · ya tenemos el nuestro), -r
	// es redundante.
	//
	// -K UID_MAX=appUIDMax+1: amplía el rango VÁLIDO solo para esta llamada,
	// sin tocar /etc/login.defs (config global). Sin esto, useradd avisa
	// "uid X outside of UID_MIN/UID_MAX range" porque nuestro rango (100000+)
	// está por encima del UID_MAX por defecto (60000). El usuario se crea
	// igual, pero el -K elimina el warning · más limpio en los logs.
	// Verificado en Pi ARM64 (18/06/2026): exit 0, uid/gid correctos, sin warning.
	runSafe("useradd",
		"-u", strconv.Itoa(uid),
		"-g", strconv.Itoa(gid),
		"-M",                                       // sin crear home
		"-N",                                       // no crear grupo propio (usamos el nuestro)
		"-K", "UID_MAX="+strconv.Itoa(appUIDMax+1), // rango válido para esta op
		"-s", "/usr/sbin/nologin", // sin shell
		userName,
	)
	logMsg("app_uids: usuario de sistema %s creado (uid=%d gid=%d) para app %s",
		userName, uid, gid, appID)
}

// ─────────────────────────────────────────────────────────────────────────
// Fase 2 · Aplicación de permisos por volumen usando el UID único de la app.
// ─────────────────────────────────────────────────────────────────────────
//
// Modelo (Opción A · ver PERMISOS-DESIGN.md):
//   · App con UID único asignado por NimOS → su volumen es suyo (chown UID,
//     chmod 0750), confinado, excluido del modelo de shares.
//   · App con UID FIJO de imagen (postgres 999, synapse 991) → la imagen ignora
//     un UID forzado; respetamos su UID real. Confinada por VOLUMEN exclusivo
//     (0700 BD / 0750 resto). El aislamiento viene del volumen + no exposición.
//
// El FileManager corre como root (daemon) → navega cualquier volumen sin grupo
// compartido. Por eso NO añadimos grupo nimos-share-docker-apps aquí.

// volPermPlan describe qué permisos aplicar a un volumen concreto.
type volPermPlan struct {
	HostPath string // ruta-host del bind mount (ya resuelta y validada bajo el pool)
	UID      int    // dueño a aplicar
	GID      int    // grupo a aplicar
	Mode     string // "0700" (BD) | "0750" (normal)
	IsDB     bool   // true → volumen de base de datos (0700, sin ACL)
}

// decideVolumePlan decide los permisos de UN volumen dado el UID asignado a la
// app y el UID que la imagen declara. Lógica PURA (testeable sin tocar disco).
//
//	appUID/appGID · el UID/GID único asignado por NimOS a la app (Fase 1).
//	imageUIDStr   · lo que devuelve imageUID(image): "", "0", "root", "999"...
//	containerPath · lado-container del volumen (para detectar si es BD).
//
// Reglas:
//  1. Volumen de BD → 0700, dueño = UID fijo de imagen (o 999 por defecto).
//     Las BBDD hardcodean su UID; NO se les puede forzar el asignado.
//  2. Imagen con UID fijo NO-root (synapse 991, etc.) → 0750, dueño = ese UID.
//     La imagen ignoraría un UID forzado; respetamos el suyo, confinado.
//  3. Imagen sin UID o root → 0750, dueño = UID ASIGNADO por NimOS. La app es
//     flexible (o linuxserver vía PUID); le imponemos su UID único.
func decideVolumePlan(appUID, appGID int, imageUIDStr, containerPath, hostPath string) volPermPlan {
	isDB := isDBContainerPath(containerPath)

	// Normalizar el UID de imagen a entero, 0 si vacío/root.
	imgUID := 0
	if imageUIDStr != "" && imageUIDStr != "root" {
		if n, err := strconv.Atoi(imageUIDStr); err == nil {
			imgUID = n
		}
	}

	switch {
	case isDB:
		// BD · UID fijo de imagen o 999 por defecto. 0700 exclusivo.
		dbUID := imgUID
		if dbUID == 0 {
			dbUID = 999 // postgres/mariadb/mongo usan 999 casi universalmente
		}
		return volPermPlan{HostPath: hostPath, UID: dbUID, GID: dbUID, Mode: "0700", IsDB: true}

	case imgUID != 0:
		// UID fijo de imagen (no-root, no-BD): synapse 991, grafana 472...
		// La imagen ignora un UID forzado; respetamos el suyo. 0750 confinado.
		return volPermPlan{HostPath: hostPath, UID: imgUID, GID: imgUID, Mode: "0750", IsDB: false}

	default:
		// Flexible (sin UID en imagen, o root, o linuxserver PUID): le imponemos
		// el UID ÚNICO asignado por NimOS. 0750 confinado.
		return volPermPlan{HostPath: hostPath, UID: appUID, GID: appGID, Mode: "0750", IsDB: false}
	}
}

// applyVolPermPlan ejecuta el plan sobre el disco (chown/chmod). No falla la
// instalación · loguea. Separado de decideVolumePlan para testear la decisión.
func applyVolPermPlan(p volPermPlan) {
	mode := "0750"
	if p.Mode != "" {
		mode = p.Mode
	}
	os.MkdirAll(p.HostPath, 0750)
	// Quitar ACLs heredadas (las BD las rechazan; en general, limpio).
	runSafe("setfacl", "-R", "-b", p.HostPath)
	runSafe("chown", "-R", fmt.Sprintf("%d:%d", p.UID, p.GID), p.HostPath)
	runSafe("chmod", "-R", mode, p.HostPath)
	logMsg("app_uids: volumen %s → %s dueño %d:%d (db=%v · excluido del modelo de shares)",
		p.HostPath, mode, p.UID, p.GID, p.IsDB)
}

// applyAppPermissions es el punto de entrada de la Fase 2, llamado desde el
// flujo de instalación ANTES del `compose up`. Reemplaza al viejo
// applyUIDPermissions.
//
//	appID    · id de la app (para asignar/buscar su UID único).
//	compose  · texto del compose.
//	envVars  · el .env ya resuelto (para expandir rutas de volúmenes).
//	nowISO   · timestamp para el registro del UID.
//
// Hace:
//  1. Asigna (o reusa) el UID único de la app + crea el usuario de sistema.
//  2. Para cada volumen bajo el pool, decide y aplica los permisos.
//  3. Devuelve las rutas-host de TODOS los volúmenes tratados, para que el
//     modelo de shares posterior los EXCLUYA (no los pise).
//
// No falla la instalación si algo va mal · loguea y sigue (devuelve lo tratado).
func applyAppPermissions(appID, compose string, envVars map[string]interface{}, nowISO string) []string {
	var excludeVolumes []string

	// 1. UID único de la app (Fase 1).
	au, err := assignAppUID(db, appID, nowISO)
	if err != nil {
		logMsg("app_uids: applyAppPermissions · no se pudo asignar UID a %s: %v (se omite)", appID, err)
		return excludeVolumes
	}
	ensureAppSystemUser(appID, au.UID, au.GID)

	// 2. Parsear el compose y aplicar permisos por volumen.
	var parsed composeForPerms
	if err := yaml.Unmarshal([]byte(compose), &parsed); err != nil {
		logMsg("app_uids: applyAppPermissions · no se pudo parsear compose de %s: %v (se omite)", appID, err)
		return excludeVolumes
	}

	for svcName, svc := range parsed.Services {
		if svc.Image == "" {
			continue
		}
		imgUID := imageUID(svc.Image)
		for _, vol := range svc.Volumes {
			hostPath := resolveVolumeHostPath(vol, envVars)
			if hostPath == "" {
				continue
			}
			if !strings.HasPrefix(hostPath, nimosPoolsRoot()) {
				continue // fuera del pool (ej. /etc/localtime) · no tocar
			}
			containerPath := volumeContainerPath(vol)
			plan := decideVolumePlan(au.UID, au.GID, imgUID, containerPath, hostPath)
			applyVolPermPlan(plan)
			excludeVolumes = append(excludeVolumes, hostPath)
			logMsg("app_uids: %s · servicio %s · volumen %s tratado (uid=%d db=%v)",
				appID, svcName, hostPath, plan.UID, plan.IsDB)
		}
	}
	return excludeVolumes
}

// ─────────────────────────────────────────────────────────────────────────
// Fase 3 · Finalización de permisos del container, SIN grupo compartido.
// ─────────────────────────────────────────────────────────────────────────
//
// Reemplaza a applySharePermsExcluding (que aplicaba el grupo compartido
// nimos-share-docker-apps + chmod 2775). Modelo nuevo: las apps son cajas
// confinadas, gestionadas SOLO por la UI de NimOS (el FileManager corre como
// root y navega/edita todo sin grupo). NO se exponen por SMB.
//
// Tras compose up, Docker puede haber creado subcarpetas/volúmenes nuevos bajo
// containerPath que la Fase 2 (que corre ANTES del up) no trató. Esta función:
//
//	· Pone la raíz containerPath con el UID de la app y 0750 (sin grupo).
//	· Recorre las entradas de primer nivel: las que NO fueron tratadas como
//	  volumen (no están en treatedVolumes) reciben el UID de la app + 0750.
//	· Las ya tratadas (volúmenes de la Fase 2, incl. BD con 0700) se respetan.
//
// No falla la instalación · loguea.
func finalizeAppContainerPerms(appID, containerPath string, treatedVolumes []string) {
	au, err := getAppUID(db, appID)
	if err != nil || au == nil {
		logMsg("app_uids: finalizeAppContainerPerms · sin UID para %s (err=%v) · se omite", appID, err)
		return
	}

	treated := func(path string) bool {
		clean := strings.TrimRight(path, "/")
		for _, v := range treatedVolumes {
			if strings.TrimRight(v, "/") == clean {
				return true
			}
		}
		return false
	}

	// La raíz del container: UID de la app, 0750, sin grupo. El FileManager
	// (root) navega igual; la app es dueña.
	runSafe("chown", fmt.Sprintf("%d:%d", au.UID, au.GID), containerPath)
	runSafe("chmod", "0750", containerPath)

	entries, err := os.ReadDir(containerPath)
	if err != nil {
		// No podemos listar · al menos la raíz quedó bien arriba.
		logMsg("app_uids: finalizeAppContainerPerms · no se pudo listar %s: %v", containerPath, err)
		return
	}
	for _, e := range entries {
		full := filepath.Join(containerPath, e.Name())
		if treated(full) {
			// Volumen ya tratado por la Fase 2 (incl. BD 0700) · respetar.
			continue
		}
		// Carpeta/archivo no declarado como volumen (p.ej. creado por Docker en
		// el up): darle el UID de la app + 0750, sin grupo compartido.
		runSafe("chown", "-R", fmt.Sprintf("%d:%d", au.UID, au.GID), full)
		runSafe("chmod", "-R", "0750", full)
		logMsg("app_uids: finalizeAppContainerPerms · %s → uid %d 0750 (sin grupo)", full, au.UID)
	}
}

// ─────────────────────────────────────────────────────────────────────────
// Fase 4 · Reconciler de higiene de UIDs.
// ─────────────────────────────────────────────────────────────────────────
//
// Limpia usuarios de sistema fantasma SIN reusar UIDs jamás. Para cada app
// marcada como released (desinstalada):
//   · Si NO quedan archivos suyos en disco (desinstalada-total, datos borrados)
//     → userdel del usuario de sistema (libera el NOMBRE, no el número).
//   · Si SÍ quedan archivos (desinstalada-normal, datos conservados)
//     → NO tocar. El usuario debe seguir existiendo para que sus datos tengan
//       dueño válido y una reinstalación los recupere.
//
// NUNCA reasigna ni libera un UID para reuso (el contador uid_allocator solo
// sube). Esto evita el ataque de reciclaje de UID.
//
// Devuelve un informe (cuántos limpiados, cuántos conservados) para logging.

type uidHygieneReport struct {
	Cleaned   []string // app_ids cuyo usuario de sistema se eliminó (sin datos)
	Preserved []string // app_ids released pero con datos en disco (no se tocan)
	Errors    []string // problemas no fatales
}

// appHasFilesOnDisk indica si quedan archivos propiedad de uid bajo el pool.
// Usa `find -uid N -print -quit` (se detiene en el primer hallazgo · barato).
// Si el find falla, asume true (conservador: NO limpiar ante la duda).
func appHasFilesOnDisk(uid int, searchRoot string) bool {
	out, ok := runSafe("find", searchRoot, "-uid", strconv.Itoa(uid), "-print", "-quit")
	if !ok {
		// Ante la duda, conservador: asumir que hay archivos (NO limpiar).
		return true
	}
	return strings.TrimSpace(out) != ""
}

// reconcileDecision decide, para una app released, qué hacer: limpiar el
// usuario de sistema ("clean") o conservarlo ("preserve"). Lógica PURA,
// testeable. Recibe si la app está activa y si tiene archivos en disco.
//
//	· Activa (reinstalada) → preserve (no tocar)
//	· Tiene archivos (desinstalada-normal, datos conservados) → preserve
//	· Sin archivos (desinstalada-total) → clean (userdel)
func reconcileDecision(isActive, hasFiles bool) string {
	if isActive || hasFiles {
		return "preserve"
	}
	return "clean"
}

// reconcileAppUIDs recorre las apps released y limpia los usuarios de sistema
// que ya no tienen datos. searchRoot acota el find (típicamente el área de
// containers del pool). activeAppIDs son las apps ACTUALMENTE instaladas (para
// no tocar sus usuarios aunque por error tuvieran released_at).
//
// hasFilesFn permite inyectar la comprobación de archivos en tests. Si es nil,
// usa appHasFilesOnDisk (el find real).
func reconcileAppUIDs(database *sql.DB, searchRoot string, activeAppIDs map[string]bool, hasFilesFn func(uid int) bool) uidHygieneReport {
	var rep uidHygieneReport

	if hasFilesFn == nil {
		hasFilesFn = func(uid int) bool { return appHasFilesOnDisk(uid, searchRoot) }
	}

	rows, err := database.Query(
		`SELECT app_id, uid, gid FROM app_uids WHERE released_at IS NOT NULL`,
	)
	if err != nil {
		rep.Errors = append(rep.Errors, fmt.Sprintf("query released: %v", err))
		return rep
	}
	defer rows.Close()

	type cand struct {
		appID    string
		uid, gid int
	}
	var candidates []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.appID, &c.uid, &c.gid); err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("scan: %v", err))
			continue
		}
		candidates = append(candidates, c)
	}

	for _, c := range candidates {
		decision := reconcileDecision(activeAppIDs[c.appID], hasFilesFn(c.uid))
		if decision == "preserve" {
			rep.Preserved = append(rep.Preserved, c.appID)
			logMsg("app_uids: reconcile · %s (uid=%d) conservado (activa o con datos) · usuario NO eliminado", c.appID, c.uid)
			continue
		}
		// "clean" · sin datos · limpiar el usuario de sistema (NO el número).
		userName := sanitizeAppUserName(c.appID)
		runSafe("userdel", userName)
		runSafe("groupdel", userName)
		rep.Cleaned = append(rep.Cleaned, c.appID)
		logMsg("app_uids: reconcile · %s (uid=%d) sin datos · usuario %s eliminado (UID NO reusable)",
			c.appID, c.uid, userName)
	}

	logMsg("app_uids: reconcile completado · %d limpiados, %d conservados, %d errores",
		len(rep.Cleaned), len(rep.Preserved), len(rep.Errors))
	return rep
}
