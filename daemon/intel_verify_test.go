package main

import (
	"net/netip"
	"testing"
)

// ─── Tests del radix trie (matching IP/CIDR) ───

func TestIntelTrie_ExactAndCIDR(t *testing.T) {
	trie := newIntelTrie()
	mustInsert(t, trie, "1.2.3.4/32", "block")
	mustInsert(t, trie, "10.20.0.0/16", "observe") // (no reservado en este test directo)
	mustInsert(t, trie, "2001:db8::/32", "block")

	cases := []struct {
		ip      string
		wantHit bool
		action  string
	}{
		{"1.2.3.4", true, "block"},        // exacta
		{"1.2.3.5", false, ""},            // vecina, no está
		{"10.20.30.40", true, "observe"},  // dentro del /16
		{"10.21.0.1", false, ""},          // fuera del /16
		{"2001:db8::dead", true, "block"}, // dentro del /32 v6
		{"2001:dead::1", false, ""},       // fuera
	}
	for _, c := range cases {
		addr := netip.MustParseAddr(c.ip)
		m := trie.lookup(addr)
		if m.Hit != c.wantHit {
			t.Errorf("%s: Hit=%v, want %v", c.ip, m.Hit, c.wantHit)
		}
		if c.wantHit && m.Action != c.action {
			t.Errorf("%s: action=%q, want %q", c.ip, m.Action, c.action)
		}
	}
}

func TestIntelTrie_LongestPrefix(t *testing.T) {
	trie := newIntelTrie()
	mustInsert(t, trie, "8.8.0.0/16", "observe")
	mustInsert(t, trie, "8.8.8.0/24", "block") // más específico
	// 8.8.8.8 cae en ambos → debe ganar el /24 (block)
	m := trie.lookup(netip.MustParseAddr("8.8.8.8"))
	if !m.Hit || m.Action != "block" {
		t.Errorf("longest-prefix falló: %+v (esperado block)", m)
	}
	// 8.8.9.9 solo en el /16 → observe
	m2 := trie.lookup(netip.MustParseAddr("8.8.9.9"))
	if !m2.Hit || m2.Action != "observe" {
		t.Errorf("8.8.9.9 debería ser observe: %+v", m2)
	}
}

// ─── Test del filtro de rangos reservados ───

func TestIntel_ReservedFiltered(t *testing.T) {
	reserved := []string{
		"0.0.0.0/8",      // el que apareció en FireHOL
		"127.0.0.1/32",   // loopback
		"192.168.1.0/24", // privado
		"10.0.0.0/8",     // privado
		"169.254.0.0/16", // link-local
		"224.0.0.0/4",    // multicast
		"5.0.0.0/4",      // prefijo absurdamente amplio (<8) → feed roto
	}
	for _, s := range reserved {
		p := netip.MustParsePrefix(s)
		if !isReservedPrefix(p) {
			t.Errorf("%s debería filtrarse como reservado/absurdo y NO se filtró", s)
		}
	}
	// Una IP pública normal NO debe filtrarse
	pub := netip.MustParsePrefix("185.220.101.5/32")
	if isReservedPrefix(pub) {
		t.Error("una IP pública normal se filtró por error")
	}
}

// loadBlocklistInto debe saltarse las reservadas
func TestIntel_LoadSkipsReserved(t *testing.T) {
	trie := newIntelTrie()
	content := []byte("# comentario\n0.0.0.0/8\n127.0.0.1\n185.220.101.5\n1.2.3.4\nbasura-no-ip\n")
	added, skipped := loadBlocklistInto(trie, content, "observe")
	if added != 2 { // solo 185.220.101.5 y 1.2.3.4
		t.Errorf("added=%d, want 2", added)
	}
	if skipped < 3 { // 0.0.0.0/8, 127.0.0.1, basura
		t.Errorf("skipped=%d, want >=3", skipped)
	}
	// la pública sí está
	if !trie.lookup(netip.MustParseAddr("185.220.101.5")).Hit {
		t.Error("la IP pública debería estar en el trie")
	}
	// la reservada no
	if trie.lookup(netip.MustParseAddr("127.0.0.1")).Hit {
		t.Error("loopback NO debería estar en el trie")
	}
}

func mustInsert(t *testing.T, trie *IntelTrie, cidr, action string) {
	t.Helper()
	p, ok := parsePrefix(cidr)
	if !ok {
		t.Fatalf("no pude parsear %s", cidr)
	}
	trie.insert(p, action)
}
