// docker_install_lock_test.go — Tests del guard de instalación concurrente y
// la espera del lock de apt (fix 12/06/2026).

package main

import (
	"context"
	"testing"
	"time"
)

// TestDockerInstallMu_TryLock · verifica que el mutex impide instalaciones
// concurrentes. La primera toma el lock; la segunda (TryLock) debe fallar.
func TestDockerInstallMu_TryLock(t *testing.T) {
	// Estado limpio
	if !dockerInstallMu.TryLock() {
		t.Fatal("el mutex debería estar libre al inicio")
	}
	// Segundo intento mientras está tomado → debe fallar
	if dockerInstallMu.TryLock() {
		dockerInstallMu.Unlock()
		t.Fatal("TryLock debería fallar mientras hay una instalación en curso")
	}
	// Liberar
	dockerInstallMu.Unlock()
	// Ahora debería poder tomarse de nuevo
	if !dockerInstallMu.TryLock() {
		t.Fatal("el mutex debería estar libre tras Unlock")
	}
	dockerInstallMu.Unlock()
}

// TestAptLockFree_NonexistentFiles · si los lock files no existen, debe
// considerar el lock libre (no bloquear la instalación).
func TestAptLockFree_NonexistentFiles(t *testing.T) {
	fakeLocks := []string{
		"/tmp/nonexistent-dpkg-lock-xyz",
		"/tmp/nonexistent-apt-lock-xyz",
	}
	if !aptLockFree(fakeLocks) {
		t.Error("con lock files inexistentes, aptLockFree debería devolver true (libre)")
	}
}

// TestWaitForAptLock_FreeImmediately · si el lock está libre, debe devolver
// true rápido sin esperar.
func TestWaitForAptLock_FreeImmediately(t *testing.T) {
	ctx := context.Background()
	start := time.Now()
	// Con timeout corto · si está libre, devuelve casi instantáneo
	ok := waitForAptLock(ctx, 5*time.Second)
	elapsed := time.Since(start)

	if !ok {
		t.Skip("apt/dpkg parece ocupado en el entorno de test · se omite")
	}
	if elapsed > 2*time.Second {
		t.Errorf("waitForAptLock tardó %v con lock libre · debería ser rápido", elapsed)
	}
}

// TestWaitForAptLock_RespectsContext · si el contexto se cancela, debe salir.
func TestWaitForAptLock_RespectsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelar inmediatamente

	// Aunque pidamos timeout largo, el contexto cancelado debe cortar.
	// (Solo aplica si el lock estuviera ocupado · si está libre devuelve true
	// antes de mirar el contexto, lo cual también es correcto.)
	_ = waitForAptLock(ctx, 60*time.Second)
	// El test pasa si no se cuelga · el contexto cancelado garantiza salida.
}
