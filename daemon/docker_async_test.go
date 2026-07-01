// docker_async_test.go — Tests de los helpers async (Beta 8.1.x · APP-014 + APP-053).
//
// Cobertura:
//   - isAsyncRequested · query string parsing
//   - asHTTPError + writeWorkerError · mapeo a status HTTP
//   - runWorkerAsync · ciclo completo Pending → Running → Succeeded/Failed
//   - updateOpProgressSafe · no-op cuando opID vacío
//
// NO testea los workers reales (runDockerInstallWork, runDockerPullWork)
// porque dependen de comandos externos (docker, systemctl). Esos quedan
// para fase 7 con infraestructura de mock de runSafe.

package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// ═══════════════════════════════════════════════════════════════════════
// isAsyncRequested
// ═══════════════════════════════════════════════════════════════════════

func TestIsAsyncRequested(t *testing.T) {
	cases := []struct {
		query string
		want  bool
	}{
		{"async=true", true},
		{"async=1", true},
		{"async=yes", true},
		{"async=false", false},
		{"async=0", false},
		{"async=", false},
		{"", false},
		{"other=true", false},
		{"async=TRUE", false}, // case-sensitive · documentado en el helper
	}
	for _, c := range cases {
		t.Run(c.query, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/docker/install?"+c.query, nil)
			got := isAsyncRequested(r)
			if got != c.want {
				t.Errorf("isAsyncRequested(%q) = %v, want %v", c.query, got, c.want)
			}
		})
	}
}

// ═══════════════════════════════════════════════════════════════════════
// asHTTPError + writeWorkerError
// ═══════════════════════════════════════════════════════════════════════

func TestAsHTTPError_FormatsMessage(t *testing.T) {
	err := asHTTPError(409, "conflict with %s at line %d", "resource", 42)
	hse, ok := err.(*httpStatusError)
	if !ok {
		t.Fatalf("expected *httpStatusError, got %T", err)
	}
	if hse.Code != 409 {
		t.Errorf("Code = %d, want 409", hse.Code)
	}
	if hse.Msg != "conflict with resource at line 42" {
		t.Errorf("Msg = %q, want formatted", hse.Msg)
	}
}

func TestWriteWorkerError_PreservesHTTPStatusCode(t *testing.T) {
	rec := httptest.NewRecorder()
	writeWorkerError(rec, asHTTPError(409, "specific error"))
	if rec.Code != 409 {
		t.Errorf("status = %d, want 409", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "specific error") {
		t.Errorf("body should contain message, got %q", body)
	}
}

func TestWriteWorkerError_DefaultsTo500ForGenericError(t *testing.T) {
	rec := httptest.NewRecorder()
	writeWorkerError(rec, errors.New("plain error"))
	if rec.Code != 500 {
		t.Errorf("status = %d, want 500 for generic", rec.Code)
	}
}

// ═══════════════════════════════════════════════════════════════════════
// runWorkerAsync · requiere operationsRepo
// ═══════════════════════════════════════════════════════════════════════

// withTestOperationsRepo monta una BD temporal con el schema y la asigna
// como operationsRepo global durante la ejecución de fn. Restaura al final.
//
// NOTA: tests con esta helper NO pueden correr en paralelo (mutan global).
func withTestOperationsRepo(t *testing.T, fn func(*OperationsRepo)) {
	t.Helper()
	_, repo, cleanup := setupTestOperationsDB(t)
	defer cleanup()

	saved := operationsRepo
	operationsRepo = repo
	defer func() { operationsRepo = saved }()

	fn(repo)
}

// waitForStatus polls repo.Get hasta que la op alcanza un status terminal
// o expira el timeout. Usado para esperar a que la goroutine async termine.
func waitForStatus(t *testing.T, repo *OperationsRepo, opID string, timeout time.Duration) *DBOperation {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		op, err := repo.Get(context.Background(), opID)
		if err != nil {
			t.Fatalf("Get during wait: %v", err)
		}
		if op != nil && IsTerminalOpsStatus(op.Status) {
			return op
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("operation %s did not reach terminal status within %v", opID, timeout)
	return nil
}

// TestRunWorkerAsync_Success · happy path · worker devuelve resultado,
// la op pasa por Pending → Running → Succeeded con resultJSON correcto.
func TestRunWorkerAsync_Success(t *testing.T) {
	withTestOperationsRepo(t, func(repo *OperationsRepo) {
		ctx := context.Background()
		op, _ := repo.Create(ctx, "test.task", "andres")

		runWorkerAsync(op.ID, func(ctx context.Context) (map[string]interface{}, error) {
			return map[string]interface{}{"ok": true, "value": 42}, nil
		})

		final := waitForStatus(t, repo, op.ID, 2*time.Second)
		if final.Status != OpsStatusSucceeded {
			t.Errorf("status = %q, want succeeded", final.Status)
		}
		if final.StartedAt == "" {
			t.Error("StartedAt should be set after Running")
		}
		if final.FinishedAt == "" {
			t.Error("FinishedAt should be set on terminal")
		}
		if !strings.Contains(final.ResultJSON, `"ok":true`) {
			t.Errorf("ResultJSON should contain ok:true, got %q", final.ResultJSON)
		}
	})
}

// TestRunWorkerAsync_Failure · worker devuelve error genérico · op pasa
// a Failed con Error poblado.
func TestRunWorkerAsync_Failure(t *testing.T) {
	withTestOperationsRepo(t, func(repo *OperationsRepo) {
		ctx := context.Background()
		op, _ := repo.Create(ctx, "test.task", "andres")

		runWorkerAsync(op.ID, func(ctx context.Context) (map[string]interface{}, error) {
			return nil, errors.New("simulated failure")
		})

		final := waitForStatus(t, repo, op.ID, 2*time.Second)
		if final.Status != OpsStatusFailed {
			t.Errorf("status = %q, want failed", final.Status)
		}
		if final.Error != "simulated failure" {
			t.Errorf("Error = %q, want 'simulated failure'", final.Error)
		}
	})
}

// TestRunWorkerAsync_HTTPStatusError · worker devuelve httpStatusError ·
// el Msg debe quedar en Error de la op (el Code se ignora en async).
func TestRunWorkerAsync_HTTPStatusError(t *testing.T) {
	withTestOperationsRepo(t, func(repo *OperationsRepo) {
		ctx := context.Background()
		op, _ := repo.Create(ctx, "test.task", "andres")

		runWorkerAsync(op.ID, func(ctx context.Context) (map[string]interface{}, error) {
			return nil, asHTTPError(409, "conflict: /var/lib/docker has data")
		})

		final := waitForStatus(t, repo, op.ID, 2*time.Second)
		if final.Status != OpsStatusFailed {
			t.Errorf("status = %q, want failed", final.Status)
		}
		if !strings.Contains(final.Error, "conflict") {
			t.Errorf("Error should contain 'conflict', got %q", final.Error)
		}
	})
}

// TestRunWorkerAsync_NilResult · worker exitoso sin result · debe marcar
// succeeded con resultJSON vacío (sin panic).
func TestRunWorkerAsync_NilResult(t *testing.T) {
	withTestOperationsRepo(t, func(repo *OperationsRepo) {
		ctx := context.Background()
		op, _ := repo.Create(ctx, "test.task", "andres")

		runWorkerAsync(op.ID, func(ctx context.Context) (map[string]interface{}, error) {
			return nil, nil
		})

		final := waitForStatus(t, repo, op.ID, 2*time.Second)
		if final.Status != OpsStatusSucceeded {
			t.Errorf("status = %q, want succeeded", final.Status)
		}
		if final.ResultJSON != "" {
			t.Errorf("ResultJSON should be empty for nil result, got %q", final.ResultJSON)
		}
	})
}

// TestRunWorkerAsync_ProgressUpdates · worker que llama updateOpProgressSafe
// debe verlo reflejado tras MarkSucceeded.
func TestRunWorkerAsync_ProgressUpdates(t *testing.T) {
	withTestOperationsRepo(t, func(repo *OperationsRepo) {
		ctx := context.Background()
		op, _ := repo.Create(ctx, "test.task", "andres")
		opID := op.ID

		runWorkerAsync(opID, func(ctx context.Context) (map[string]interface{}, error) {
			updateOpProgressSafe(ctx, opID, 25, "Quarter way")
			updateOpProgressSafe(ctx, opID, 50, "Halfway")
			// Pequeña pausa para asegurar que la BD ha visto los updates
			time.Sleep(20 * time.Millisecond)
			return map[string]interface{}{"ok": true}, nil
		})

		final := waitForStatus(t, repo, op.ID, 2*time.Second)
		if final.Status != OpsStatusSucceeded {
			t.Fatalf("status = %q, want succeeded", final.Status)
		}
		// Tras succeeded, progress queda al último valor reportado (50)
		// porque MarkSucceeded no toca progress. Esto está OK · clientes
		// que ven succeeded saben que el trabajo terminó.
		if final.Progress != 50 {
			t.Errorf("Progress = %d, want 50 (last updated value)", final.Progress)
		}
		if final.Message != "Halfway" {
			t.Errorf("Message = %q, want 'Halfway'", final.Message)
		}
	})
}

// ═══════════════════════════════════════════════════════════════════════
// updateOpProgressSafe
// ═══════════════════════════════════════════════════════════════════════

// TestUpdateOpProgressSafe_NoOpEmptyID · opID vacío es no-op · no panic
// aunque operationsRepo no esté configurado.
func TestUpdateOpProgressSafe_NoOpEmptyID(t *testing.T) {
	// Asegurar que el global está nil para este test
	saved := operationsRepo
	operationsRepo = nil
	defer func() { operationsRepo = saved }()

	updateOpProgressSafe(context.Background(), "", 50, "irrelevant")
	// Si llegamos aquí sin panic, OK
}

// TestUpdateOpProgressSafe_NoOpNilRepo · opID válido pero repo nil · no panic.
func TestUpdateOpProgressSafe_NoOpNilRepo(t *testing.T) {
	saved := operationsRepo
	operationsRepo = nil
	defer func() { operationsRepo = saved }()

	updateOpProgressSafe(context.Background(), "op_1_deadbeef", 50, "irrelevant")
}

// ═══════════════════════════════════════════════════════════════════════
// writeAsyncAccepted
// ═══════════════════════════════════════════════════════════════════════

func TestWriteAsyncAccepted_ResponseShape(t *testing.T) {
	rec := httptest.NewRecorder()
	op := &DBOperation{
		ID:     "op_123_aabbccdd",
		Type:   "docker.install",
		Status: OpsStatusPending,
	}
	writeAsyncAccepted(rec, op)

	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", rec.Code)
	}
	body := rec.Body.String()
	for _, expected := range []string{
		`"operationId":"op_123_aabbccdd"`,
		`"pollUrl":"/api/operations/op_123_aabbccdd"`,
		`"type":"docker.install"`,
		`"status":"pending"`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("body should contain %q, got %q", expected, body)
		}
	}
}

// ═══════════════════════════════════════════════════════════════════════
// getStackHostIP · helper para inyección de HOST_IP en .env de stacks
// ═══════════════════════════════════════════════════════════════════════

// TestGetStackHostIP_ReturnsNonEmpty · siempre debe devolver algo (al menos
// el fallback "127.0.0.1") · nunca cadena vacía. Esto es importante porque
// si HOST_IP queda vacío, JELLYFIN_PublishedServerUrl = "" y la app crashea.
func TestGetStackHostIP_ReturnsNonEmpty(t *testing.T) {
	ip := getStackHostIP()
	if ip == "" {
		t.Error("getStackHostIP() returned empty string · debe haber al menos fallback")
	}
}

// TestGetStackHostIP_ValidIPv4 · el resultado debe parsearse como IPv4 válida.
// No verifica que sea la "correcta" IP del LAN (depende del entorno) · solo
// que el formato es válido.
func TestGetStackHostIP_ValidIPv4(t *testing.T) {
	ip := getStackHostIP()
	parsed := net.ParseIP(ip)
	if parsed == nil {
		t.Errorf("getStackHostIP() = %q · no es IP válida", ip)
	}
	if parsed != nil && parsed.To4() == nil {
		t.Errorf("getStackHostIP() = %q · debe ser IPv4 (To4() != nil)", ip)
	}
}

// ═══════════════════════════════════════════════════════════════════════
// getStackTimezone
// ═══════════════════════════════════════════════════════════════════════

// TestGetStackTimezone_ReturnsNonEmpty · siempre debe devolver algo (al menos
// "UTC" como fallback final).
func TestGetStackTimezone_ReturnsNonEmpty(t *testing.T) {
	tz := getStackTimezone()
	if tz == "" {
		t.Error("getStackTimezone() returned empty string · debe haber fallback")
	}
}

// ═══════════════════════════════════════════════════════════════════════
// expandStackEnvRefs · resuelve referencias ${KEY} entre values del .env
// ═══════════════════════════════════════════════════════════════════════

func TestExpandStackEnvRefs_BasicSubstitution(t *testing.T) {
	env := map[string]interface{}{
		"CONFIG_PATH":   "/data/docker/containers/codeserver",
		"PROJECTS_PATH": "${CONFIG_PATH}/projects",
	}
	result := expandStackEnvRefs(env, 4)
	if got := result["PROJECTS_PATH"]; got != "/data/docker/containers/codeserver/projects" {
		t.Errorf("PROJECTS_PATH = %v, want expanded", got)
	}
}

func TestExpandStackEnvRefs_LeavesUnchangedWhenNoRefs(t *testing.T) {
	env := map[string]interface{}{
		"PASSWORD":    "nimbus",
		"CONFIG_PATH": "/foo",
	}
	result := expandStackEnvRefs(env, 4)
	if result["PASSWORD"] != "nimbus" {
		t.Error("PASSWORD should not change without refs")
	}
}

func TestExpandStackEnvRefs_UnknownRefStaysLiteral(t *testing.T) {
	env := map[string]interface{}{
		"MIXED": "${UNKNOWN_VAR}/suffix",
	}
	result := expandStackEnvRefs(env, 4)
	// Referencia no resuelta · queda literal para que el error sea visible.
	if got := result["MIXED"]; got != "${UNKNOWN_VAR}/suffix" {
		t.Errorf("MIXED = %v, want literal", got)
	}
}

func TestExpandStackEnvRefs_MultiLevelChain(t *testing.T) {
	// A → B → C debe resolverse en pocas pasadas.
	env := map[string]interface{}{
		"A": "valueA",
		"B": "${A}_with_B",
		"C": "${B}_with_C",
	}
	result := expandStackEnvRefs(env, 4)
	if got := result["C"]; got != "valueA_with_B_with_C" {
		t.Errorf("C = %v, want fully resolved chain", got)
	}
}

func TestExpandStackEnvRefs_CircularStopsAfterMaxPasses(t *testing.T) {
	// Referencia circular · no debe colgar, ni panic. Después de maxPasses
	// queda en un estado consistente (no resuelto del todo).
	env := map[string]interface{}{
		"A": "${B}",
		"B": "${A}",
	}
	result := expandStackEnvRefs(env, 4)
	// Solo nos importa que no se cuelgue · el valor final es indefinido.
	if result == nil {
		t.Error("expandStackEnvRefs returned nil on circular refs")
	}
}

func TestExpandStackEnvRefs_NonStringValuesUntouched(t *testing.T) {
	env := map[string]interface{}{
		"PORT":          8080,
		"PROJECTS_PATH": "${PORT}/projects", // ref a un value numérico
	}
	result := expandStackEnvRefs(env, 4)
	if result["PORT"] != 8080 {
		t.Errorf("PORT (int) should stay int, got %T %v", result["PORT"], result["PORT"])
	}
	// Cuando un value no-string se referencia, se serializa con %v
	if got := result["PROJECTS_PATH"]; got != "8080/projects" {
		t.Errorf("PROJECTS_PATH = %v, want 8080/projects", got)
	}
}

// ═══════════════════════════════════════════════════════════════════════
// resolveRandomPlaceholders · {RANDOM} con persistencia idempotente
// ═══════════════════════════════════════════════════════════════════════

// TestResolveRandomPlaceholders_FreshInstall · primera instalación, .env no
// existe · debe generar una cadena random de 24 chars.
func TestResolveRandomPlaceholders_FreshInstall(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := tmpDir + "/.env"
	// .env no existe · primera instalación

	env := map[string]interface{}{
		"DB_PASSWORD": "{RANDOM}",
		"OTHER_VAR":   "literal_value",
		"CONFIG_PATH": "/data/x",
	}

	result := resolveRandomPlaceholders(env, envPath)

	password, ok := result["DB_PASSWORD"].(string)
	if !ok {
		t.Fatalf("DB_PASSWORD no es string: %T %v", result["DB_PASSWORD"], result["DB_PASSWORD"])
	}
	if password == "{RANDOM}" {
		t.Error("DB_PASSWORD sigue siendo literal {RANDOM} · no se resolvió")
	}
	if len(password) != 24 {
		t.Errorf("DB_PASSWORD length = %d · expected 24", len(password))
	}
	if result["OTHER_VAR"] != "literal_value" {
		t.Error("OTHER_VAR debería quedar intacto (no era {RANDOM})")
	}
}

// TestResolveRandomPlaceholders_ReinstallWithLiteral · reinstalación cuando
// el .env previo tiene "{RANDOM}" literal (instalación pre-fix) · MANTIENE
// el literal para no romper el container existente.
//
// Justificación: Postgres tiene en su data dir el hash de "{RANDOM}" como
// password. Si cambiásemos a una random nueva, no podría autenticarse.
func TestResolveRandomPlaceholders_ReinstallWithLiteral(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := tmpDir + "/.env"
	// .env previo con literal "{RANDOM}"
	err := os.WriteFile(envPath, []byte("DB_PASSWORD={RANDOM}\nDB_USERNAME=postgres\n"), 0644)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	env := map[string]interface{}{
		"DB_PASSWORD": "{RANDOM}",
		"DB_USERNAME": "postgres",
	}

	result := resolveRandomPlaceholders(env, envPath)

	if result["DB_PASSWORD"] != "{RANDOM}" {
		t.Errorf("DB_PASSWORD = %v · expected literal '{RANDOM}' para preservar Postgres existente", result["DB_PASSWORD"])
	}
}

// TestResolveRandomPlaceholders_ReinstallWithGenerated · reinstalación cuando
// el .env previo ya tenía una pass random generada · MANTIENE esa pass.
//
// Este es el caso "feliz": Immich se instaló fresh con el fix activo,
// generó pass random, y se reinstala/actualiza después · debe seguir
// funcionando sin que se regenere otra pass distinta.
func TestResolveRandomPlaceholders_ReinstallWithGenerated(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := tmpDir + "/.env"
	const previousPass = "Kj8mNq2pVx7wRyL4aBcD5eFg"
	err := os.WriteFile(envPath, []byte("DB_PASSWORD="+previousPass+"\n"), 0644)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	env := map[string]interface{}{
		"DB_PASSWORD": "{RANDOM}", // el catálogo siempre manda {RANDOM} literal
	}

	result := resolveRandomPlaceholders(env, envPath)

	if result["DB_PASSWORD"] != previousPass {
		t.Errorf("DB_PASSWORD = %v · expected %q para mantener Postgres existente", result["DB_PASSWORD"], previousPass)
	}
}

// TestResolveRandomPlaceholders_NoRandomKeysUntouched · si ningún valor es
// "{RANDOM}", el map sale igual sin tocar nada.
func TestResolveRandomPlaceholders_NoRandomKeysUntouched(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := tmpDir + "/.env"

	env := map[string]interface{}{
		"CONFIG_PATH": "/data/x",
		"HOST_IP":     "192.168.1.131",
		"PASSWORD":    "nimbus", // literal del catálogo, NO {RANDOM}
	}

	result := resolveRandomPlaceholders(env, envPath)

	if result["PASSWORD"] != "nimbus" {
		t.Errorf("PASSWORD debería quedar 'nimbus', got %v", result["PASSWORD"])
	}
}

// TestGenerateRandomString_LengthAndCharset · 24 chars, todos del alphabet
// alfanumérico, distintos entre llamadas (probabilidad de colisión
// astronómicamente baja).
func TestGenerateRandomString_LengthAndCharset(t *testing.T) {
	a := generateRandomString(24)
	b := generateRandomString(24)

	if len(a) != 24 {
		t.Errorf("len(a) = %d · expected 24", len(a))
	}
	if a == b {
		t.Error("dos llamadas consecutivas devolvieron el mismo string · entropía rota")
	}
	for _, c := range a {
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			t.Errorf("char %q fuera del alphabet alfanumérico", c)
		}
	}
}
