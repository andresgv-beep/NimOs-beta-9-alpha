// intel_trie.go — Radix trie (árbol de prefijos binario) para matching
// eficiente de IPs contra la blocklist del feed.
//
// El problema: el feed trae ~26.000 entradas (IPs sueltas y rangos CIDR).
// Comprobar cada petición entrante con un bucle lineal sería O(n) por petición
// — inviable en el hot path del shield. Un radix trie indexa por los bits de
// la dirección: la búsqueda es O(longitud de la IP) = máx 32 pasos (IPv4) o
// 128 (IPv6), independientemente de cuántas entradas haya.
//
// Soporta "longest-prefix match": si una IP cae en un /24 bloqueado, se
// detecta aunque la entrada sea el rango, no la IP exacta.
package main

import (
	"net/netip"
	"sync"
)

// intelNode es un nodo del trie binario. child[0]/child[1] siguen el bit 0/1.
type intelNode struct {
	child [2]*intelNode
	// terminal: aquí termina un prefijo de la blocklist. Guarda metadatos
	// para que el match sepa de qué se trata (acción, fuente futura).
	terminal bool
	action   string // "block" | "observe" — heredado del manifest
}

// IntelTrie es el índice de la blocklist. Dos árboles: v4 y v6 separados.
// Seguro para uso concurrente (lecturas en el hot path, recarga al refrescar).
type IntelTrie struct {
	mu    sync.RWMutex
	root4 *intelNode
	root6 *intelNode
	count int
}

func newIntelTrie() *IntelTrie {
	return &IntelTrie{root4: &intelNode{}, root6: &intelNode{}}
}

// insert añade un prefijo (CIDR) al trie con su acción.
func (t *IntelTrie) insert(p netip.Prefix, action string) {
	addr := p.Addr()
	root := t.root4
	if addr.Is6() {
		root = t.root6
	}
	bits := addr.AsSlice()
	node := root
	for i := 0; i < p.Bits(); i++ {
		bit := (bits[i/8] >> (7 - uint(i%8))) & 1
		if node.child[bit] == nil {
			node.child[bit] = &intelNode{}
		}
		node = node.child[bit]
	}
	if !node.terminal {
		t.count++
	}
	node.terminal = true
	node.action = action
}

// IntelMatch es el resultado de una consulta.
type IntelMatch struct {
	Hit    bool
	Action string // "block" | "observe"
}

// lookup busca una IP. Devuelve el match del prefijo más específico que la
// contenga (longest-prefix match): recorre los bits de la IP y se queda con
// el último nodo terminal encontrado por el camino.
func (t *IntelTrie) lookup(addr netip.Addr) IntelMatch {
	t.mu.RLock()
	defer t.mu.RUnlock()

	addr = addr.Unmap() // normaliza ::ffff:1.2.3.4 → 1.2.3.4
	root := t.root4
	if addr.Is6() {
		root = t.root6
	}
	if root == nil {
		return IntelMatch{}
	}
	bits := addr.AsSlice()
	node := root
	var best *intelNode
	if node.terminal {
		best = node
	}
	maxBits := len(bits) * 8
	for i := 0; i < maxBits; i++ {
		bit := (bits[i/8] >> (7 - uint(i%8))) & 1
		node = node.child[bit]
		if node == nil {
			break
		}
		if node.terminal {
			best = node // prefijo más específico hasta ahora
		}
	}
	if best != nil {
		return IntelMatch{Hit: true, Action: best.action}
	}
	return IntelMatch{}
}

// size devuelve cuántos prefijos hay indexados.
func (t *IntelTrie) size() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.count
}

// Nota: el antiguo swapFrom (intercambiar raíces dentro del mismo trie) se
// eliminó: el refresco del feed ahora publica un IntelState completo nuevo
// vía atomic.Pointer (intelActive.Store), así que el trie nunca muta una vez
// publicado — los lectores solo necesitan el RLock frente a la construcción.
