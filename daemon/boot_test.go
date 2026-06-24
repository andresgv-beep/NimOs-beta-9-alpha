// boot_test.go — Tests del helper waitForCondition (fix D-003).
//
// waitForCondition sustituye los sleeps fijos del boot storm. Como ahora hay
// LÓGICA donde antes había un número mágico, esa lógica se testea: condición
// inmediata, condición tardía, timeout, y cancelación por contexto.
//
// Ejecutar:
//
//	cd daemon/
//	go test -run TestWaitForCondition -v
package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// Condición que ya es cierta → retorno inmediato, sin esperar ni un tick.
func TestWaitForCondition_ImmediateTrue(t *testing.T) {
	start := time.Now()
	ok := waitForCondition(context.Background(), "test-immediate", 5*time.Second, func() bool { return true })
	if !ok {
		t.Fatal("condición cierta desde el inicio debe devolver true")
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("condición inmediata tardó %v — no debe esperar ningún intervalo", elapsed)
	}
}

// Condición que se vuelve cierta tras unos polls → true antes del timeout.
func TestWaitForCondition_EventuallyTrue(t *testing.T) {
	var calls int32
	check := func() bool {
		return atomic.AddInt32(&calls, 1) >= 3 // cierta al tercer poll
	}
	ok := waitForCondition(context.Background(), "test-eventual", 10*time.Second, check)
	if !ok {
		t.Fatal("condición que se cumple al 3er poll debe devolver true")
	}
	if n := atomic.LoadInt32(&calls); n < 3 {
		t.Errorf("esperaba ≥3 polls, hubo %d", n)
	}
}

// Condición que nunca se cumple → false al agotar el timeout, sin colgarse.
func TestWaitForCondition_Timeout(t *testing.T) {
	start := time.Now()
	ok := waitForCondition(context.Background(), "test-timeout", 1200*time.Millisecond, func() bool { return false })
	if ok {
		t.Fatal("condición nunca cierta debe devolver false")
	}
	elapsed := time.Since(start)
	// Debe respetar el timeout: ni retornar al instante ni pasarse de largo.
	// Margen superior generoso (timeout + último intervalo de backoff).
	if elapsed < 1200*time.Millisecond {
		t.Errorf("retornó en %v, antes del timeout de 1.2s", elapsed)
	}
	if elapsed > 4*time.Second {
		t.Errorf("tardó %v en rendirse — el backoff no debe alargar el timeout indefinidamente", elapsed)
	}
}

// Contexto cancelado durante la espera → false sin agotar el timeout.
func TestWaitForCondition_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	ok := waitForCondition(ctx, "test-cancel", 30*time.Second, func() bool { return false })
	if ok {
		t.Fatal("contexto cancelado debe devolver false")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("cancelación tardó %v en surtir efecto — debe abortar el poll en curso", elapsed)
	}
}
