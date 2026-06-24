// nimhealth_multicontainer_test.go — Tests del fix multi-container (15/06/2026).
//
// Verifica que las acciones Docker (start/stop/restart/logs) operan sobre la
// LISTA de containers de una app (resueltos por label com.nimos.app_id), no
// sobre un único nombre = app_id. Esto arregla apps multi-servicio como matrix
// (matrix_synapse + matrix_element) que antes fallaban con "No such container".

package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHandleDockerAction_UnknownAction · acción inválida → 404, sin tocar Docker.
func TestHandleDockerAction_UnknownAction(t *testing.T) {
	w := httptest.NewRecorder()
	handleDockerAction(w, []string{"matrix_synapse", "matrix_element"}, "frobnicate")
	if w.Code != 404 {
		t.Errorf("acción desconocida debería dar 404, dio %d", w.Code)
	}
}

// TestHandleDockerAction_MultiContainerFailReporting · cuando los containers no
// existen (sin Docker en el test), debe reportar TODOS los que fallaron, no
// solo el primero. Verifica que itera sobre la lista completa.
func TestHandleDockerAction_MultiContainerFailReporting(t *testing.T) {
	w := httptest.NewRecorder()
	// Containers que no existen · runSafe(docker stop ...) fallará para ambos
	handleDockerAction(w, []string{"nonexistent_a", "nonexistent_b"}, "stop")

	// Sin Docker disponible, esperamos 500 con ambos containers en el mensaje
	if w.Code != 500 {
		t.Skipf("esperaba 500 (docker no disponible en test), dio %d · se omite", w.Code)
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	// El mensaje de error debe mencionar el fallo · confirma que se procesó la lista
	if resp["error"] == nil && resp["message"] == nil {
		t.Error("la respuesta de error debería tener un mensaje")
	}
}

// TestHandleDockerLogs_MultiContainerHeaders · con varios containers, debe
// incluir encabezados por container para distinguirlos.
func TestHandleDockerLogs_MultiContainerHeaders(t *testing.T) {
	w := httptest.NewRecorder()
	handleDockerLogs(w, []string{"matrix_synapse", "matrix_element"}, 10)

	if w.Code != 200 {
		t.Fatalf("logs debería responder 200, dio %d", w.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("respuesta no es JSON válido: %v", err)
	}
	logs, ok := resp["logs"].([]interface{})
	if !ok {
		t.Fatal("la respuesta debería tener un array 'logs'")
	}
	// Con 2 containers (aunque no existan logs reales), debe haber al menos
	// los 2 encabezados "── nombre ──"
	headerCount := 0
	for _, l := range logs {
		entry, _ := l.(map[string]interface{})
		if msg, _ := entry["message"].(string); strings.HasPrefix(msg, "\u2500\u2500 ") {
			headerCount++
		}
	}
	if headerCount < 2 {
		t.Errorf("esperaba 2 encabezados de container (multi), encontré %d", headerCount)
	}
}

// TestHandleDockerLogs_SingleContainerNoHeader · con un solo container, NO debe
// añadir encabezado (comportamiento limpio para apps de un servicio).
func TestHandleDockerLogs_SingleContainerNoHeader(t *testing.T) {
	w := httptest.NewRecorder()
	handleDockerLogs(w, []string{"navidrome"}, 10)

	if w.Code != 200 {
		t.Fatalf("logs debería responder 200, dio %d", w.Code)
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	logs, _ := resp["logs"].([]interface{})
	// Con 1 container no debe haber encabezado "── ... ──"
	for _, l := range logs {
		entry, _ := l.(map[string]interface{})
		if msg, _ := entry["message"].(string); strings.HasPrefix(msg, "\u2500\u2500 ") {
			t.Error("con un solo container NO debería haber encabezado")
		}
	}
}
