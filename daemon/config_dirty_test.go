// config_dirty_test.go — Backup de config event-driven.
//
// Verifica lo crítico: una ráfaga de cambios coalesce en UN backup (debounce),
// cambios espaciados generan varios, y el backstop dispara aunque no haya señales.

package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// counter de backups, seguro para concurrencia.
type backupCounter struct {
	n    int32
	last chan struct{}
}

func newBackupCounter() *backupCounter {
	return &backupCounter{last: make(chan struct{}, 100)}
}
func (c *backupCounter) fn() {
	atomic.AddInt32(&c.n, 1)
	select {
	case c.last <- struct{}{}:
	default:
	}
}
func (c *backupCounter) count() int { return int(atomic.LoadInt32(&c.n)) }

// markConfigDirty coalesce: muchas señales seguidas = como mucho 1 pendiente.
func TestMarkConfigDirty_Coalesces(t *testing.T) {
	// Vaciar cualquier señal previa del canal global.
	select {
	case <-configDirty:
	default:
	}
	for i := 0; i < 50; i++ {
		markConfigDirty()
	}
	// El canal tiene capacidad 1 → solo una señal pendiente.
	if len(configDirty) != 1 {
		t.Errorf("se esperaba 1 señal coalescida, hay %d", len(configDirty))
	}
	<-configDirty // limpiar
}

// Una ráfaga de señales dentro del debounce → UN solo backup.
func TestRunLoop_BurstCoalescesToOne(t *testing.T) {
	dirty := make(chan struct{}, 1)
	stop := make(chan struct{})
	c := newBackupCounter()
	debounce := 40 * time.Millisecond

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); runConfigBackupLoop(dirty, debounce, time.Hour, c.fn, stop) }()

	// Ráfaga: 10 señales muy seguidas.
	for i := 0; i < 10; i++ {
		dirty <- struct{}{}
		time.Sleep(2 * time.Millisecond)
	}
	// Esperar a que cierre la ventana de debounce + margen.
	select {
	case <-c.last:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("el backup no llegó a dispararse")
	}
	time.Sleep(60 * time.Millisecond) // dar tiempo a un (indeseado) segundo backup

	if got := c.count(); got != 1 {
		t.Errorf("una ráfaga debe coalescer en 1 backup; hubo %d", got)
	}
	close(stop)
	wg.Wait()
}

// Señales espaciadas más que el debounce → varios backups.
func TestRunLoop_SpacedSignalsBackupEach(t *testing.T) {
	dirty := make(chan struct{}, 1)
	stop := make(chan struct{})
	c := newBackupCounter()
	debounce := 20 * time.Millisecond

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); runConfigBackupLoop(dirty, debounce, time.Hour, c.fn, stop) }()

	for i := 0; i < 3; i++ {
		dirty <- struct{}{}
		<-c.last // esperar el backup de esta señal antes de la siguiente
	}
	if got := c.count(); got != 3 {
		t.Errorf("3 señales espaciadas → 3 backups; hubo %d", got)
	}
	close(stop)
	wg.Wait()
}

// El backstop dispara aunque no haya ninguna señal.
func TestRunLoop_BackstopFires(t *testing.T) {
	dirty := make(chan struct{}, 1)
	stop := make(chan struct{})
	c := newBackupCounter()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		runConfigBackupLoop(dirty, time.Hour, 30*time.Millisecond, c.fn, stop)
	}()

	select {
	case <-c.last:
		// bien: el backstop disparó sin señales
	case <-time.After(500 * time.Millisecond):
		t.Fatal("el backstop no disparó")
	}
	close(stop)
	wg.Wait()
}

// dirtyIfOK: marca en éxito, no marca en error.
func TestDirtyIfOK(t *testing.T) {
	select {
	case <-configDirty:
	default:
	}
	if err := dirtyIfOK(nil); err != nil {
		t.Errorf("no debía devolver error; got %v", err)
	}
	if len(configDirty) != 1 {
		t.Error("éxito debe marcar dirty")
	}
	<-configDirty
	dirtyIfOK(errTest)
	if len(configDirty) != 0 {
		t.Error("error NO debe marcar dirty")
	}
}

var errTest = &mockErr{"boom"}
