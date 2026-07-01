// shield_windows_cleanup_test.go — Verifica la poda de las ventanas
// deslizantes del motor de reglas.
//
// Contexto: countAndAdd expira los timestamps DENTRO de una key, pero las
// keys en sí solo las borra cleanup(), que hasta este fix no se llamaba desde
// ningún sitio → los mapas crecían sin límite con cardinalidad controlada por
// el atacante (apiEnumWindow usa ip:endpoint, authUserWindow ip:usuario).
// Ahora startShieldCleanup los poda cada 5 minutos.

package main

import (
	"testing"
	"time"
)

func TestSlidingWindow_CleanupRemovesStaleKeys(t *testing.T) {
	sw := newSlidingWindow()

	// Key fresca: un evento de ahora mismo.
	sw.countAndAdd("ip:203.0.113.1:user:alice", time.Minute)

	// Key rancia: todos sus timestamps son de hace una hora.
	sw.mu.Lock()
	sw.entries["ip:203.0.113.2:user:bob"] = &windowEntry{
		timestamps: []time.Time{time.Now().Add(-time.Hour)},
	}
	sw.mu.Unlock()

	sw.cleanup(10 * time.Minute)

	sw.mu.Lock()
	defer sw.mu.Unlock()
	if _, ok := sw.entries["ip:203.0.113.2:user:bob"]; ok {
		t.Fatal("cleanup debería borrar las keys cuyos timestamps expiraron todos")
	}
	if _, ok := sw.entries["ip:203.0.113.1:user:alice"]; !ok {
		t.Fatal("cleanup no debe tocar keys con timestamps frescos")
	}
}

func TestSlidingWindow_CleanupKeepsFreshTimestampsOfMixedKey(t *testing.T) {
	sw := newSlidingWindow()

	// Key mixta: un timestamp viejo y uno fresco → sobrevive solo el fresco.
	sw.mu.Lock()
	sw.entries["ip:203.0.113.3"] = &windowEntry{
		timestamps: []time.Time{time.Now().Add(-time.Hour), time.Now()},
	}
	sw.mu.Unlock()

	sw.cleanup(10 * time.Minute)

	sw.mu.Lock()
	defer sw.mu.Unlock()
	entry, ok := sw.entries["ip:203.0.113.3"]
	if !ok {
		t.Fatal("la key con un timestamp fresco debe sobrevivir a la poda")
	}
	if len(entry.timestamps) != 1 {
		t.Fatalf("deben quedar solo los timestamps frescos: quedan %d, esperaba 1", len(entry.timestamps))
	}
}
