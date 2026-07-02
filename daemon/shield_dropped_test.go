// shield_dropped_test.go — Verifica el contador de eventos descartados por
// canal lleno. Antes se tiraban en silencio: bajo saturación el shield se
// quedaba ciego sin que nadie lo supiera.

package main

import (
	"testing"
)

func TestShieldEmit_CountsDroppedWhenChannelFull(t *testing.T) {
	// Canal minúsculo para el test; se restaura al salir.
	prevCh := shieldEvents
	shieldEvents = make(chan ShieldEvent, 1)
	defer func() { shieldEvents = prevCh }()

	before := shieldEventsDropped.Load()

	// 1º entra (buffer 1); 2º y 3º se descartan y deben contarse.
	shieldEmit(ShieldEvent{Category: "scan", SourceIP: "203.0.113.90"})
	shieldEmit(ShieldEvent{Category: "scan", SourceIP: "203.0.113.90"})
	shieldEmit(ShieldEvent{Category: "scan", SourceIP: "203.0.113.90"})

	if got := shieldEventsDropped.Load() - before; got != 2 {
		t.Fatalf("descartados = %d, esperaba 2", got)
	}

	// El que entró sigue en el canal (no se pierde el que cabía).
	select {
	case ev := <-shieldEvents:
		if ev.SourceIP != "203.0.113.90" {
			t.Errorf("evento inesperado en el canal: %+v", ev)
		}
	default:
		t.Fatal("el primer evento debería estar en el canal")
	}
}
