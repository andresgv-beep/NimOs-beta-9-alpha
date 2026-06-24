// docker_postinstall_test.go — Tests del motor postInstall.
//
// FASE 1: substituteTokens, findUnresolvedTokens, ofuscateSecretsInCommand
// (funciones puras · sin docker · testeables a fondo).

package main

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSubstituteTokens(t *testing.T) {
	cmd := "register_new_matrix_user -c /data/homeserver.yaml -a -u {{ADMIN_USER}} -p {{ADMIN_PASS}} http://localhost:8008"
	values := map[string]string{"ADMIN_USER": "andres", "ADMIN_PASS": "secreto123"}
	got := substituteTokens(cmd, values)
	want := "register_new_matrix_user -c /data/homeserver.yaml -a -u andres -p secreto123 http://localhost:8008"
	if got != want {
		t.Errorf("substituteTokens:\n got:  %q\n want: %q", got, want)
	}
}

func TestSubstituteTokens_MissingValueStaysLiteral(t *testing.T) {
	// Si falta un valor, el token se deja literal (señal de que falta · NO
	// sustituir por vacío y ejecutar algo a medias).
	cmd := "cmd -u {{ADMIN_USER}} -p {{ADMIN_PASS}}"
	values := map[string]string{"ADMIN_USER": "andres"} // falta ADMIN_PASS
	got := substituteTokens(cmd, values)
	want := "cmd -u andres -p {{ADMIN_PASS}}"
	if got != want {
		t.Errorf("missing value:\n got:  %q\n want: %q", got, want)
	}
}

func TestSubstituteTokens_NoTokens(t *testing.T) {
	cmd := "echo hola sin tokens"
	got := substituteTokens(cmd, map[string]string{"X": "y"})
	if got != cmd {
		t.Errorf("sin tokens debe quedar igual: got %q", got)
	}
}

func TestSubstituteTokens_Empty(t *testing.T) {
	if got := substituteTokens("", map[string]string{"X": "y"}); got != "" {
		t.Errorf("comando vacío: got %q", got)
	}
}

func TestSubstituteTokens_RepeatedToken(t *testing.T) {
	cmd := "{{USER}} y otra vez {{USER}}"
	got := substituteTokens(cmd, map[string]string{"USER": "andres"})
	want := "andres y otra vez andres"
	if got != want {
		t.Errorf("token repetido:\n got:  %q\n want: %q", got, want)
	}
}

func TestFindUnresolvedTokens(t *testing.T) {
	// Comando con tokens sin resolver.
	cmd := "cmd -u andres -p {{ADMIN_PASS}} --extra {{OTRO}}"
	got := findUnresolvedTokens(cmd)
	want := []string{"ADMIN_PASS", "OTRO"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("findUnresolvedTokens:\n got:  %v\n want: %v", got, want)
	}
}

func TestFindUnresolvedTokens_AllResolved(t *testing.T) {
	cmd := "cmd -u andres -p secreto" // ya sustituido, sin tokens
	if got := findUnresolvedTokens(cmd); got != nil {
		t.Errorf("sin tokens debe ser nil: got %v", got)
	}
}

func TestFindUnresolvedTokens_Dedup(t *testing.T) {
	// El mismo token repetido se cuenta una vez.
	cmd := "{{X}} {{X}} {{Y}}"
	got := findUnresolvedTokens(cmd)
	want := []string{"X", "Y"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dedup:\n got:  %v\n want: %v", got, want)
	}
}

func TestOfuscateSecretsInCommand(t *testing.T) {
	// El comando ya sustituido lleva el secreto en claro · al loguear, ocultarlo.
	cmd := "register ... -u andres -p secreto123 http://localhost:8008"
	got := ofuscateSecretsInCommand(cmd, []string{"secreto123"})
	want := "register ... -u andres -p *** http://localhost:8008"
	if got != want {
		t.Errorf("ofuscate:\n got:  %q\n want: %q", got, want)
	}
}

func TestOfuscateSecretsInCommand_MultipleSecrets(t *testing.T) {
	cmd := "cmd --pass secreto1 --token secreto2"
	got := ofuscateSecretsInCommand(cmd, []string{"secreto1", "secreto2"})
	want := "cmd --pass *** --token ***"
	if got != want {
		t.Errorf("multiple:\n got:  %q\n want: %q", got, want)
	}
}

func TestOfuscateSecretsInCommand_EmptySecretIgnored(t *testing.T) {
	// Un secreto vacío no debe reemplazar todo el comando con ***.
	cmd := "cmd --user andres"
	got := ofuscateSecretsInCommand(cmd, []string{""})
	if got != cmd {
		t.Errorf("secreto vacío no debe tocar: got %q", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────
// FASE 2 · parseHealthOutput (puro) + waitForHealthyWith (stub inyectado)
// ─────────────────────────────────────────────────────────────────────────

func TestParseHealthOutput(t *testing.T) {
	cases := []struct {
		out  string
		ok   bool
		want string
	}{
		{"healthy", true, healthHealthy},
		{"unhealthy", true, healthUnhealthy},
		{"starting", true, healthStarting},
		{"none", true, healthNone},
		{"healthy\n", true, healthHealthy},   // con salto de línea (TrimSpace)
		{"  healthy  ", true, healthHealthy}, // con espacios
		{"", true, healthUnknown},            // vacío
		{"loquesea", true, healthUnknown},    // inesperado
		{"healthy", false, healthUnknown},    // comando falló (ok=false)
	}
	for _, c := range cases {
		got := parseHealthOutput(c.out, c.ok)
		if got != c.want {
			t.Errorf("parseHealthOutput(%q, %v): got %q, want %q", c.out, c.ok, got, c.want)
		}
	}
}

func TestWaitForHealthyWith_NoHealthcheck(t *testing.T) {
	// Si el container no declara healthcheck (none) → noHealthcheck=true,
	// para que el llamador dé error claro (decisión D4 · estricto).
	healthFn := func(string) string { return healthNone }
	ok, noHC := waitForHealthyWith(context.Background(), "x", time.Second, healthFn)
	if ok {
		t.Error("sin healthcheck no debe devolver ok=true")
	}
	if !noHC {
		t.Error("sin healthcheck debe devolver noHealthcheck=true")
	}
}

func TestWaitForHealthyWith_BecomesHealthy(t *testing.T) {
	// Simula un container que tarda unos chequeos en estar healthy.
	calls := 0
	healthFn := func(string) string {
		calls++
		if calls >= 3 {
			return healthHealthy // al 3er chequeo, sano
		}
		return healthStarting
	}
	ok, noHC := waitForHealthyWith(context.Background(), "x", 10*time.Second, healthFn)
	if !ok {
		t.Error("debería llegar a healthy")
	}
	if noHC {
		t.Error("tiene healthcheck, noHealthcheck debe ser false")
	}
}

func TestWaitForHealthyWith_Timeout(t *testing.T) {
	// Nunca se pone healthy → timeout → ok=false (pero tiene healthcheck).
	healthFn := func(string) string { return healthStarting }
	ok, noHC := waitForHealthyWith(context.Background(), "x", 1*time.Second, healthFn)
	if ok {
		t.Error("nunca healthy → ok debe ser false (timeout)")
	}
	if noHC {
		t.Error("tiene healthcheck (starting), noHealthcheck debe ser false")
	}
}

// ─────────────────────────────────────────────────────────────────────────
// FASE 3 · idempotentAlreadyExists (puro) + runPostInstallAction/runPostInstall
// (con execFn inyectado · sin docker real)
// ─────────────────────────────────────────────────────────────────────────

func TestIdempotentAlreadyExists(t *testing.T) {
	yes := []string{
		"User ID already exists",
		"ERROR: user already registered",
		"duplicate key value",
		"el usuario ya existe",
	}
	for _, s := range yes {
		if !idempotentAlreadyExists(s) {
			t.Errorf("debería detectar 'ya existe' en: %q", s)
		}
	}
	no := []string{
		"Success",
		"connection refused",
		"some other error",
		"",
	}
	for _, s := range no {
		if idempotentAlreadyExists(s) {
			t.Errorf("NO debería detectar 'ya existe' en: %q", s)
		}
	}
}

// execFn stub que captura lo que se le pasó y devuelve lo configurado.
func stubExec(output string, ok bool, captured *string) func(string, string) (string, bool) {
	return func(container, command string) (string, bool) {
		if captured != nil {
			*captured = command
		}
		return output, ok
	}
}

func TestRunPostInstallAction_Success(t *testing.T) {
	var capturedCmd string
	action := PostInstallAction{
		ID:        "create_admin",
		Type:      "exec",
		Container: "matrix_synapse",
		Command:   "register -u {{ADMIN_USER}} -p {{ADMIN_PASS}}",
		// sin WaitFor para no depender de healthy en este test
	}
	values := map[string]string{"ADMIN_USER": "andres", "ADMIN_PASS": "secreto"}
	res := runPostInstallAction(context.Background(), action, values, []string{"secreto"},
		stubExec("Success!", true, &capturedCmd))

	if !res.OK {
		t.Errorf("debería ser OK, err: %s", res.Err)
	}
	if capturedCmd != "register -u andres -p secreto" {
		t.Errorf("comando mal sustituido: %q", capturedCmd)
	}
}

func TestRunPostInstallAction_MissingToken(t *testing.T) {
	action := PostInstallAction{
		ID:      "x", Type: "exec", Container: "c",
		Command: "cmd -u {{ADMIN_USER}} -p {{ADMIN_PASS}}",
	}
	values := map[string]string{"ADMIN_USER": "andres"} // falta ADMIN_PASS
	res := runPostInstallAction(context.Background(), action, values, nil,
		stubExec("", true, nil))

	if res.OK {
		t.Error("no debería ejecutar con tokens sin resolver")
	}
	if res.Err == "" || !strings.Contains(res.Err, "ADMIN_PASS") {
		t.Errorf("el error debería mencionar ADMIN_PASS: %q", res.Err)
	}
}

func TestRunPostInstallAction_IdempotentAlreadyExists(t *testing.T) {
	action := PostInstallAction{
		ID: "create_admin", Type: "exec", Container: "c",
		Command:    "register -u {{ADMIN_USER}}",
		Idempotent: true,
	}
	values := map[string]string{"ADMIN_USER": "andres"}
	// El comando "falla" pero la salida dice que el user ya existe.
	res := runPostInstallAction(context.Background(), action, values, nil,
		stubExec("ERROR: User ID already exists", false, nil))

	if !res.OK {
		t.Error("idempotente + 'ya existe' debería ser OK")
	}
	if !res.Skipped {
		t.Error("debería marcar Skipped (ya estaba hecho)")
	}
}

func TestRunPostInstallAction_RealError_OfuscatesSecret(t *testing.T) {
	action := PostInstallAction{
		ID: "create_admin", Type: "exec", Container: "c",
		Command: "register -p {{ADMIN_PASS}}",
	}
	values := map[string]string{"ADMIN_PASS": "secreto123"}
	// Falla de verdad, y la salida contiene el secreto (no debe filtrarse).
	res := runPostInstallAction(context.Background(), action, values, []string{"secreto123"},
		stubExec("error connecting with password secreto123", false, nil))

	if res.OK {
		t.Error("error real no debería ser OK")
	}
	if strings.Contains(res.Err, "secreto123") {
		t.Errorf("el secreto NO debe aparecer en el error: %q", res.Err)
	}
	if !strings.Contains(res.Err, "***") {
		t.Errorf("el error debería tener el secreto ofuscado (***): %q", res.Err)
	}
}

func TestRunPostInstallAction_UnsupportedType(t *testing.T) {
	action := PostInstallAction{ID: "x", Type: "http", Container: "c", Command: "x"}
	res := runPostInstallAction(context.Background(), action, nil, nil,
		stubExec("", true, nil))
	if res.OK {
		t.Error("tipo no soportado no debería ser OK")
	}
	if !strings.Contains(res.Err, "http") {
		t.Errorf("el error debería mencionar el tipo: %q", res.Err)
	}
}

func TestRunPostInstall_AllOK(t *testing.T) {
	actions := []PostInstallAction{
		{ID: "a1", Type: "exec", Container: "c", Command: "echo {{X}}"},
		{ID: "a2", Type: "exec", Container: "c", Command: "echo hola"},
	}
	values := map[string]string{"X": "1"}
	results, err := runPostInstall(context.Background(), actions, values, nil,
		stubExec("ok", true, nil))
	if err != nil {
		t.Errorf("no debería haber error: %v", err)
	}
	if len(results) != 2 || !results[0].OK || !results[1].OK {
		t.Errorf("ambas acciones deberían ser OK: %+v", results)
	}
}

func TestRunPostInstall_StopsOnError(t *testing.T) {
	actions := []PostInstallAction{
		{ID: "a1", Type: "exec", Container: "c", Command: "cmd {{FALTA}}"}, // falla
		{ID: "a2", Type: "exec", Container: "c", Command: "echo hola"},     // no se ejecuta
	}
	results, err := runPostInstall(context.Background(), actions, map[string]string{}, nil,
		stubExec("ok", true, nil))
	if err == nil {
		t.Error("debería devolver error")
	}
	if len(results) != 1 {
		t.Errorf("debería parar tras la 1ª (fallida), got %d resultados", len(results))
	}
}

// ─────────────────────────────────────────────────────────────────────────
// FASE 4 · parseo del body (frontend → structs)
// ─────────────────────────────────────────────────────────────────────────

func TestParsePostInstallActions(t *testing.T) {
	// Simula body["postInstall"] como llega del JSON (interface{}).
	raw := []interface{}{
		map[string]interface{}{
			"id":         "create_admin",
			"type":       "exec",
			"waitFor":    "healthy",
			"container":  "matrix_synapse",
			"command":    "register -u {{ADMIN_USER}}",
			"idempotent": true,
		},
	}
	actions := parsePostInstallActions(raw)
	if len(actions) != 1 {
		t.Fatalf("esperaba 1 acción, got %d", len(actions))
	}
	a := actions[0]
	if a.ID != "create_admin" || a.Type != "exec" || a.WaitFor != "healthy" {
		t.Errorf("acción mal parseada: %+v", a)
	}
	if a.Container != "matrix_synapse" || !a.Idempotent {
		t.Errorf("container/idempotent mal: %+v", a)
	}
}

func TestParsePostInstallActions_Empty(t *testing.T) {
	if got := parsePostInstallActions(nil); got != nil {
		t.Errorf("nil debería dar nil, got %v", got)
	}
	if got := parsePostInstallActions("no soy un array"); got != nil {
		t.Errorf("tipo inválido debería dar nil, got %v", got)
	}
}

func TestParsePostInstallValues(t *testing.T) {
	raw := map[string]interface{}{
		"ADMIN_USER": "andres",
		"ADMIN_PASS": "secreto",
		"PORT":       float64(8008), // los números llegan como float64 del JSON
	}
	vals := parsePostInstallValues(raw)
	if vals["ADMIN_USER"] != "andres" || vals["ADMIN_PASS"] != "secreto" {
		t.Errorf("valores mal: %v", vals)
	}
	if vals["PORT"] != "8008" {
		t.Errorf("número debería convertirse a string: %q", vals["PORT"])
	}
}

func TestParseSecretKeys(t *testing.T) {
	raw := []interface{}{"ADMIN_PASS", "TOKEN"}
	keys := parseSecretKeys(raw)
	if len(keys) != 2 || keys[0] != "ADMIN_PASS" || keys[1] != "TOKEN" {
		t.Errorf("secret keys mal: %v", keys)
	}
	if got := parseSecretKeys(nil); got != nil {
		t.Errorf("nil debería dar nil, got %v", got)
	}
}

func TestParsePostInstallActions_MissingFields(t *testing.T) {
	// Defensivo: campos ausentes no deben romper.
	raw := []interface{}{
		map[string]interface{}{"id": "x"}, // solo id
	}
	actions := parsePostInstallActions(raw)
	if len(actions) != 1 || actions[0].ID != "x" {
		t.Errorf("debería parsear con campos ausentes: %+v", actions)
	}
	if actions[0].Idempotent {
		t.Error("idempotent ausente debería ser false")
	}
}
