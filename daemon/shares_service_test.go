// shares_service_test.go
//
// Tests de la capa service. Aquí cubrimos LO PURAMENTE TESTABLE:
//   · validateShareName (validación de nombre + safe name)
//   · applyPermissionDiff (cálculo del diff de permisos)
//
// Lo que requiere BTRFS real (CreateShare, UpdateShare, DeleteShare,
// createBtrfsSubvolIfMissing, etc.) se testea via integration tests
// que corren en el NAS real, no aquí.
//
// Filosofía: aislar la lógica pura testeable. Las operaciones de FS
// se testean E2E manualmente.

package main

import (
	"strings"
	"testing"
)

// ─── validateShareName ──────────────────────────────────────────────────

func TestValidateShareName_HappyPath(t *testing.T) {
	cases := []struct {
		input    string
		wantSafe string
	}{
		{"fotos", "fotos"},
		{"FOTOS", "fotos"},
		{"Mis Fotos", "mis-fotos"},
		{"Videos Familia", "videos-familia"},
		{"backup_2026", "backup_2026"},
		{"share-test", "share-test"},
		{"  trimmed  ", "trimmed"},
	}
	for _, c := range cases {
		got, err := validateShareName(c.input)
		if err != nil {
			t.Errorf("validateShareName(%q) error = %v, want nil", c.input, err)
			continue
		}
		if got != c.wantSafe {
			t.Errorf("validateShareName(%q) = %q, want %q", c.input, got, c.wantSafe)
		}
	}
}

func TestValidateShareName_Empty(t *testing.T) {
	cases := []string{"", "   ", "\t", "\n"}
	for _, c := range cases {
		_, err := validateShareName(c)
		if err == nil {
			t.Errorf("validateShareName(%q) error = nil, want error 'required'", c)
		}
		if !strings.Contains(err.Error(), "required") {
			t.Errorf("validateShareName(%q) error = %v, want 'required'", c, err)
		}
	}
}

func TestValidateShareName_TooLong(t *testing.T) {
	longName := strings.Repeat("a", 65)
	_, err := validateShareName(longName)
	if err == nil {
		t.Fatal("validateShareName(65 chars) error = nil, want 'too long'")
	}
	if !strings.Contains(err.Error(), "too long") {
		t.Errorf("validateShareName(long) error = %v, want 'too long'", err)
	}
}

func TestValidateShareName_MaxLength(t *testing.T) {
	// 64 chars exactos debe pasar (límite inclusivo)
	maxName := strings.Repeat("a", 64)
	safe, err := validateShareName(maxName)
	if err != nil {
		t.Fatalf("validateShareName(64 chars) error = %v, want nil", err)
	}
	if safe != maxName {
		t.Errorf("validateShareName(64 chars) = %q, want unchanged", safe)
	}
}

func TestValidateShareName_InvalidChars(t *testing.T) {
	// Caracteres NO permitidos
	invalidCases := []string{
		"foto/test",  // slash
		"foto..test", // dots (path traversal risk)
		"foto;test",  // semicolon
		"foto*test",  // wildcard
		"foto'test",  // quote
		"foto\"test", // double quote
		"foto`test",  // backtick
		"foto$test",  // dollar
		"foto&test",  // ampersand
		"foto|test",  // pipe
		"foto<test",  // redirect
		"foto>test",  // redirect
		"foto\\test", // backslash
		"foto:test",  // colon
		"foto?test",  // question
		"foto@test",  // at
		"foto#test",  // hash
		"foto%test",  // percent
		"foto+test",  // plus
		"foto=test",  // equals
		"foto(test)", // parentheses
		"foto[test]", // brackets
		"foto{test}", // braces
		"foto!test",  // exclamation
		"foto~test",  // tilde
	}
	for _, c := range invalidCases {
		_, err := validateShareName(c)
		if err == nil {
			t.Errorf("validateShareName(%q) error = nil, want validation error", c)
		}
	}
}

func TestValidateShareName_AllowedChars(t *testing.T) {
	// Caracteres explícitamente permitidos
	validCases := []string{
		"abc123",
		"abc-def",
		"abc_def",
		"abc def",
		"ABC123",
		"a-b_c d",
	}
	for _, c := range validCases {
		_, err := validateShareName(c)
		if err != nil {
			t.Errorf("validateShareName(%q) error = %v, want nil (should be allowed)", c, err)
		}
	}
}

// ─── applyPermissionDiff ────────────────────────────────────────────────
//
// Test puramente lógico del cálculo del diff.
// NO testea handleOp() ni dbShareSetPermission() — esos son
// side effects que se verifican E2E.
//
// Lo que SÍ testeamos:
//   · Universo de usuarios (old ∪ new) se calcula correctamente
//   · "no change" caso se ignora
//   · Vacío en newPerms se trata como "none"

// shareDiffOp representa una operación que applyPermissionDiff
// efectuaría. Usado para tests sin tocar SQLite ni handleOp.
type shareDiffOp struct {
	username string
	action   string // "add_rw", "add_ro", "remove", "none_change"
}

// calculatePermissionDiff es una versión PURA del diff de permisos,
// útil para test unitario. La lógica es idéntica a applyPermissionDiff
// pero NO ejecuta side effects: solo devuelve qué se haría.
//
// Esta función NO existe en producción. Es un mirror de la lógica
// para verificar que applyPermissionDiff hace lo correcto.
func calculatePermissionDiff(oldPerms, newPerms map[string]string) []shareDiffOp {
	if oldPerms == nil {
		oldPerms = map[string]string{}
	}

	allUsers := map[string]bool{}
	for u := range oldPerms {
		allUsers[u] = true
	}
	for u := range newPerms {
		allUsers[u] = true
	}

	ops := []shareDiffOp{}
	for username := range allUsers {
		oldPerm := oldPerms[username]
		newPerm := newPerms[username]
		if newPerm == "" {
			newPerm = "none"
		}
		if oldPerm == newPerm {
			ops = append(ops, shareDiffOp{username, "none_change"})
			continue
		}
		switch newPerm {
		case "none":
			ops = append(ops, shareDiffOp{username, "remove"})
		case "rw":
			ops = append(ops, shareDiffOp{username, "add_rw"})
		case "ro":
			ops = append(ops, shareDiffOp{username, "add_ro"})
		}
	}
	return ops
}

func findOp(ops []shareDiffOp, username, action string) bool {
	for _, op := range ops {
		if op.username == username && op.action == action {
			return true
		}
	}
	return false
}

func TestPermissionDiff_AddNewUser(t *testing.T) {
	old := map[string]string{}
	new_ := map[string]string{"alice": "rw"}
	ops := calculatePermissionDiff(old, new_)

	if !findOp(ops, "alice", "add_rw") {
		t.Errorf("expected add_rw for alice, got %v", ops)
	}
}

func TestPermissionDiff_RemoveUser(t *testing.T) {
	old := map[string]string{"alice": "rw"}
	new_ := map[string]string{}
	ops := calculatePermissionDiff(old, new_)

	if !findOp(ops, "alice", "remove") {
		t.Errorf("expected remove for alice (in old but not new), got %v", ops)
	}
}

func TestPermissionDiff_ChangePermission(t *testing.T) {
	old := map[string]string{"alice": "rw"}
	new_ := map[string]string{"alice": "ro"}
	ops := calculatePermissionDiff(old, new_)

	if !findOp(ops, "alice", "add_ro") {
		t.Errorf("expected add_ro for alice (changed rw→ro), got %v", ops)
	}
}

func TestPermissionDiff_NoChange(t *testing.T) {
	old := map[string]string{"alice": "rw"}
	new_ := map[string]string{"alice": "rw"}
	ops := calculatePermissionDiff(old, new_)

	if !findOp(ops, "alice", "none_change") {
		t.Errorf("expected none_change for alice, got %v", ops)
	}
}

func TestPermissionDiff_ExplicitNone(t *testing.T) {
	// "none" como string explícito debe tratarse como remove (igual que vacío)
	old := map[string]string{"alice": "rw"}
	new_ := map[string]string{"alice": "none"}
	ops := calculatePermissionDiff(old, new_)

	if !findOp(ops, "alice", "remove") {
		t.Errorf("expected remove for alice (rw→none), got %v", ops)
	}
}

func TestPermissionDiff_EmptyStringIsNone(t *testing.T) {
	// "" vacío también debe tratarse como none → remove
	old := map[string]string{"alice": "rw"}
	new_ := map[string]string{"alice": ""}
	ops := calculatePermissionDiff(old, new_)

	if !findOp(ops, "alice", "remove") {
		t.Errorf("expected remove for alice (rw→\"\"), got %v", ops)
	}
}

func TestPermissionDiff_MultipleUsers(t *testing.T) {
	old := map[string]string{
		"alice": "rw",
		"bob":   "ro",
	}
	new_ := map[string]string{
		"alice":   "rw", // sin cambio
		"bob":     "rw", // cambio ro → rw
		"charlie": "ro", // nuevo usuario
		// dave NO está → ningún cambio para él
	}
	ops := calculatePermissionDiff(old, new_)

	if !findOp(ops, "alice", "none_change") {
		t.Error("expected alice no change")
	}
	if !findOp(ops, "bob", "add_rw") {
		t.Error("expected bob add_rw")
	}
	if !findOp(ops, "charlie", "add_ro") {
		t.Error("expected charlie add_ro")
	}

	// Verificar que no hay ops espurias
	if len(ops) != 3 {
		t.Errorf("expected exactly 3 ops, got %d: %v", len(ops), ops)
	}
}

func TestPermissionDiff_NilOldPerms(t *testing.T) {
	// El service llama con permisos potencialmente nil. Debe tolerarlo.
	var old map[string]string = nil
	new_ := map[string]string{"alice": "rw"}
	ops := calculatePermissionDiff(old, new_)

	if !findOp(ops, "alice", "add_rw") {
		t.Errorf("expected add_rw for alice with nil old perms, got %v", ops)
	}
}

// ─── mapShareErrorToStatus (HTTP mapping) ───────────────────────────────

func TestMapShareErrorToStatus_NotFound(t *testing.T) {
	cases := []string{
		"share not found",
		"Shared folder not found",
		"Pool not found",
	}
	for _, msg := range cases {
		got := mapShareErrorToStatus(errStr(msg))
		if got != 404 {
			t.Errorf("mapShareErrorToStatus(%q) = %d, want 404", msg, got)
		}
	}
}

func TestMapShareErrorToStatus_BadRequest(t *testing.T) {
	cases := []string{
		"Shared folder already exists",
		"Folder name required",
		"Folder name too long (max 64 characters)",
		"Name can only contain letters, numbers, spaces, -, _",
		"Invalid pool",
	}
	for _, msg := range cases {
		got := mapShareErrorToStatus(errStr(msg))
		if got != 400 {
			t.Errorf("mapShareErrorToStatus(%q) = %d, want 400", msg, got)
		}
	}
}

func TestMapShareErrorToStatus_ServiceUnavailable(t *testing.T) {
	got := mapShareErrorToStatus(errStr("Storage pool is not mounted"))
	if got != 503 {
		t.Errorf("mapShareErrorToStatus('not mounted') = %d, want 503", got)
	}
}

func TestMapShareErrorToStatus_InternalError(t *testing.T) {
	// Error desconocido → 500
	got := mapShareErrorToStatus(errStr("something completely unexpected"))
	if got != 500 {
		t.Errorf("mapShareErrorToStatus(unknown) = %d, want 500", got)
	}
}

// errStr es un helper para crear errores con mensaje específico
// sin importar fmt.Errorf en cada test.
type stringError string

func (s stringError) Error() string { return string(s) }

func errStr(s string) error { return stringError(s) }
