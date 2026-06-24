// storage_btrfs_pool_test.go — Tests de removePoolFromDB.
//
// removePoolFromDB es el "core transaccional" extraído de destroyPoolBtrfs y
// exportPoolBtrfs (refactor 20/05/2026). Encapsula el fix de CRIT-2 (errores
// DB ya no se ignoran dentro de la transacción → rollback atómico).
//
// Los tests cubren:
//   - Borrado de pool no primary: primary_pool no se toca
//   - Borrado de primary con otro pool managed: se transfiere el primary
//   - Borrado de primary con pools no-managed: se limpia metadata
//   - Borrado de primary único: se limpia metadata (primary_pool + configured_at)
//   - Borrado sin primary_pool en metadata: no rompe (sql.ErrNoRows manejado)
//   - Borrado de pool inexistente: error + rollback (metadata intacta)
//
// NO testeamos el rollback explícito en mitad de tx (requiere inyección de DB
// mock — trabajo futuro). El test de PoolNotFound cubre el rollback al fallar
// la primera query (DeletePool), que es el path principal del fix CRIT-2.

package main

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers locales
// ─────────────────────────────────────────────────────────────────────────────

// insertTestPool inserta un pool en la DB con campos mínimos. Devuelve el ID.
func insertTestPool(t *testing.T, svc *StorageService, name string, state ControlState) string {
	t.Helper()
	ctx := context.Background()
	id := "pool-id-" + name
	pool := &Pool{
		ID:           id,
		Name:         name,
		BtrfsUUID:    "uuid-" + name,
		Profile:      ProfileSingle,
		MountPoint:   "/nimos/pools/" + name,
		ControlState: state,
	}
	tx, err := svc.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("insertTestPool BeginTx: %v", err)
	}
	if err := svc.repo.CreatePool(ctx, tx, pool); err != nil {
		tx.Rollback()
		t.Fatalf("insertTestPool CreatePool: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("insertTestPool Commit: %v", err)
	}
	return id
}

// setPrimaryPool establece primary_pool + configured_at en metadata.
func setPrimaryPool(t *testing.T, svc *StorageService, poolID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := svc.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO storage_metadata (key, value) VALUES ('primary_pool', ?)`, poolID); err != nil {
		t.Fatalf("setPrimaryPool primary_pool: %v", err)
	}
	if _, err := svc.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO storage_metadata (key, value) VALUES ('configured_at', ?)`,
		"2026-05-20T18:00:00Z"); err != nil {
		t.Fatalf("setPrimaryPool configured_at: %v", err)
	}
}

// getPrimaryPoolID lee primary_pool. Devuelve "" si no existe.
func getPrimaryPoolID(t *testing.T, svc *StorageService) string {
	t.Helper()
	var id string
	err := svc.db.QueryRow(`SELECT value FROM storage_metadata WHERE key = 'primary_pool'`).Scan(&id)
	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		t.Fatalf("getPrimaryPoolID: %v", err)
	}
	return id
}

// metadataHasKey verifica si una key existe en storage_metadata.
func metadataHasKey(t *testing.T, svc *StorageService, key string) bool {
	t.Helper()
	var v string
	err := svc.db.QueryRow(`SELECT value FROM storage_metadata WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		t.Fatalf("metadataHasKey: %v", err)
	}
	return true
}

// poolExistsInDB verifica si un pool sigue existiendo.
func poolExistsInDB(t *testing.T, svc *StorageService, poolID string) bool {
	t.Helper()
	p, err := svc.repo.GetPool(context.Background(), poolID)
	if err != nil {
		t.Fatalf("poolExistsInDB: %v", err)
	}
	return p != nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests — removePoolFromDB
// ─────────────────────────────────────────────────────────────────────────────

// Caso 1: pool no es primary → se borra, primary_pool sigue apuntando al otro.
func TestRemovePoolFromDB_NonPrimary(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	primaryID := insertTestPool(t, service, "primary", ControlStateManaged)
	victimID := insertTestPool(t, service, "victim", ControlStateManaged)
	setPrimaryPool(t, service, primaryID)

	if err := removePoolFromDB(ctx, service, victimID); err != nil {
		t.Fatalf("removePoolFromDB: %v", err)
	}

	if poolExistsInDB(t, service, victimID) {
		t.Error("victim pool should be deleted")
	}
	if !poolExistsInDB(t, service, primaryID) {
		t.Error("primary pool should still exist")
	}
	if got := getPrimaryPoolID(t, service); got != primaryID {
		t.Errorf("primary_pool: got %q, want %q", got, primaryID)
	}
	if !metadataHasKey(t, service, "configured_at") {
		t.Error("configured_at should still exist")
	}
}

// Caso 2: pool primary con otro managed disponible → se transfiere el primary.
func TestRemovePoolFromDB_PrimaryWithManagedBackup(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	primaryID := insertTestPool(t, service, "primary", ControlStateManaged)
	backupID := insertTestPool(t, service, "backup", ControlStateManaged)
	setPrimaryPool(t, service, primaryID)

	if err := removePoolFromDB(ctx, service, primaryID); err != nil {
		t.Fatalf("removePoolFromDB: %v", err)
	}

	if poolExistsInDB(t, service, primaryID) {
		t.Error("primary pool should be deleted")
	}
	if got := getPrimaryPoolID(t, service); got != backupID {
		t.Errorf("primary_pool should have been transferred: got %q, want %q", got, backupID)
	}
	if !metadataHasKey(t, service, "configured_at") {
		t.Error("configured_at should still exist (transferred, not cleared)")
	}
}

// Caso 3: pool primary, único managed, otros observed → NO transferir a observed.
// El primary debe limpiarse junto con configured_at (no hay pool managed válido).
func TestRemovePoolFromDB_PrimaryWithOnlyObservedBackup(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	primaryID := insertTestPool(t, service, "primary", ControlStateManaged)
	_ = insertTestPool(t, service, "observed", ControlStateObserved)
	setPrimaryPool(t, service, primaryID)

	if err := removePoolFromDB(ctx, service, primaryID); err != nil {
		t.Fatalf("removePoolFromDB: %v", err)
	}

	if poolExistsInDB(t, service, primaryID) {
		t.Error("primary pool should be deleted")
	}
	if got := getPrimaryPoolID(t, service); got != "" {
		t.Errorf("primary_pool should be cleared (observed is not eligible): got %q", got)
	}
	if metadataHasKey(t, service, "configured_at") {
		t.Error("configured_at should be cleared along with primary_pool")
	}
}

// Caso 4: pool primary único en la DB → se limpia metadata completa.
func TestRemovePoolFromDB_PrimarySoleSurvivor(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	primaryID := insertTestPool(t, service, "solo", ControlStateManaged)
	setPrimaryPool(t, service, primaryID)

	if err := removePoolFromDB(ctx, service, primaryID); err != nil {
		t.Fatalf("removePoolFromDB: %v", err)
	}

	if poolExistsInDB(t, service, primaryID) {
		t.Error("pool should be deleted")
	}
	if got := getPrimaryPoolID(t, service); got != "" {
		t.Errorf("primary_pool should be cleared: got %q", got)
	}
	if metadataHasKey(t, service, "configured_at") {
		t.Error("configured_at should be cleared")
	}
}

// Caso 5: no hay primary_pool en metadata → borrado limpio sin tocar metadata.
// Verifica que sql.ErrNoRows se maneja correctamente (no degenera en error).
func TestRemovePoolFromDB_NoPrimaryMetadata(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	poolID := insertTestPool(t, service, "orphan", ControlStateManaged)
	// NO llamamos a setPrimaryPool — la metadata queda vacía

	if err := removePoolFromDB(ctx, service, poolID); err != nil {
		t.Fatalf("removePoolFromDB con metadata vacía debe funcionar: %v", err)
	}

	if poolExistsInDB(t, service, poolID) {
		t.Error("pool should be deleted")
	}
	if metadataHasKey(t, service, "primary_pool") {
		t.Error("primary_pool no debe aparecer si no existía antes")
	}
}

// Caso 6 (CRIT-2 rollback): pool inexistente → DeletePool falla →
// la tx hace rollback y la metadata NO se toca.
//
// Este es el test clave del fix CRIT-2: si DeletePool retorna error y la
// transacción no hiciera rollback correctamente, los pasos siguientes podrían
// modificar primary_pool dejando la DB inconsistente.
func TestRemovePoolFromDB_PoolNotFoundRollback(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Setup: pool real que ES primary
	realID := insertTestPool(t, service, "real", ControlStateManaged)
	setPrimaryPool(t, service, realID)
	configuredAtBefore := metadataHasKey(t, service, "configured_at")

	// Intento de borrar un pool que no existe
	err := removePoolFromDB(ctx, service, "pool-id-fantasma")
	if err == nil {
		t.Fatal("removePoolFromDB con ID inexistente debe devolver error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error debe indicar 'not found': %v", err)
	}

	// Verificación crítica: rollback dejó todo intacto
	if !poolExistsInDB(t, service, realID) {
		t.Error("ROLLBACK FAIL: el pool real fue afectado por una operación que falló")
	}
	if got := getPrimaryPoolID(t, service); got != realID {
		t.Errorf("ROLLBACK FAIL: primary_pool cambió tras error: got %q, want %q", got, realID)
	}
	if metadataHasKey(t, service, "configured_at") != configuredAtBefore {
		t.Error("ROLLBACK FAIL: configured_at fue modificado tras error")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// NOTA SOBRE TESTS DE INTEGRACIÓN E2E
//
// destroyPoolBtrfs y exportPoolBtrfs NO se testean directamente porque tocan:
//   - El global `db` (a través de canDestroyPool → checkPoolDependencies)
//   - Shell calls vía runCmd (umount, wipefs, findmnt, partprobe, btrfs)
//   - Otros globals: notifyStorageChanged, updateTorrentConfig,
//     dbServiceDeleteByPool, cleanOrphanPoolDirs, rescanSCSIBuses
//
// Testarlas de forma unitaria requiere inyección del shell exec (mitigación
// HARD-2 de la auditoría, scope Sprint 3) o tests de integración con discos
// loop reales.
//
// Lo que SÍ tenemos blindado en este archivo es la lógica transaccional pura
// de removePoolFromDB, que es donde vive el fix de CRIT-2 — la parte crítica
// del comportamiento correcto frente a fallos parciales de DB.
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
// countRealSubmounts — fix del falso pool_busy_submounts (13/06/2026)
//
// Bug original: poolHasSubmounts contaba líneas crudas del output de findmnt.
// findmnt puede partir una entrada en varias líneas (opciones largas, formato),
// con lo que un pool SIN submounts contaba 2 líneas → falso busy → bloqueaba
// el desmontaje. Encontrado en hardware real (pool data2, disco degradado).
//
// Fix: contar solo targets que cuelgan del pool ("<mountPoint>/...").
// ─────────────────────────────────────────────────────────────────────────────

func TestCountRealSubmounts(t *testing.T) {
	const mp = "/nimos/pools/data2"

	cases := []struct {
		name   string
		output string
		want   int
	}{
		{
			name:   "solo el pool, una línea limpia",
			output: "/nimos/pools/data2\n",
			want:   0,
		},
		{
			// El caso REAL del bug: findmnt parte la entrada en 2 líneas
			// por opciones largas. El código viejo contaba 2 → falso busy.
			name:   "una entrada partida en dos líneas (caso real del bug)",
			output: "/nimos/pools/data2\n                 rw,noatime,compress=zstd:3,space_cache=v2,subvolid=5\n",
			want:   0,
		},
		{
			name:   "línea en blanco final extra",
			output: "/nimos/pools/data2\n\n\n",
			want:   0,
		},
		{
			name:   "un submount real (bind de Docker)",
			output: "/nimos/pools/data2\n/nimos/pools/data2/apps/jellyfin/config\n",
			want:   1,
		},
		{
			name:   "varios submounts reales",
			output: "/nimos/pools/data2\n/nimos/pools/data2/apps/jellyfin\n/nimos/pools/data2/torrent/downloads\n",
			want:   2,
		},
		{
			// Pool hermano con prefijo de nombre similar: data2backup NO es
			// hijo de data2. El código viejo ni contemplaba este caso.
			name:   "nombre similar no es hijo (data2backup)",
			output: "/nimos/pools/data2\n/nimos/pools/data2backup\n",
			want:   0,
		},
		{
			name:   "mountPoint con barra final se normaliza",
			output: "/nimos/pools/data2\n/nimos/pools/data2/x\n",
			want:   1,
		},
		{
			name:   "output vacío (no montado)",
			output: "",
			want:   0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mount := mp
			if tc.name == "mountPoint con barra final se normaliza" {
				mount = mp + "/"
			}
			got := countRealSubmounts(tc.output, mount)
			if got != tc.want {
				t.Errorf("countRealSubmounts(%q) = %d, want %d", tc.output, got, tc.want)
			}
		})
	}
}
