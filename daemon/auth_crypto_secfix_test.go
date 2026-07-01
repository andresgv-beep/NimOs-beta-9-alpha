package main

import (
	"strings"
	"testing"
)

// #3: el dummy de equalización de timing debe ser scrypt VÁLIDO (salt:hash), no
// bcrypt. Si no tiene ":", verifyPassword hace fast-return y vuelve el oráculo.
func TestDummyPasswordHashIsScryptFormat(t *testing.T) {
	if !strings.Contains(dummyPasswordHash, ":") {
		t.Fatal("dummyPasswordHash sin ':' → verifyPassword haría fast-return (oráculo de timing)")
	}
	if parts := strings.SplitN(dummyPasswordHash, ":", 2); len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		t.Fatalf("formato salt:hash inválido: %q", dummyPasswordHash)
	}
	// El dummy NUNCA debe validar un password (es un hash de una cadena fija).
	if verifyPassword("cualquiera", dummyPasswordHash) {
		t.Error("el dummy no debe validar passwords")
	}
}
