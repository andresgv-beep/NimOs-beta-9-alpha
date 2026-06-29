// intel_verify.go — NimShield Intelligence · verificación y parseo del feed.
//
// El consumidor: verifica la firma ed25519 del manifest con la clave PÚBLICA
// embebida, valida el SHA-256 de cada fichero, parsea las IPs/CIDR y las carga
// en el radix trie — filtrando rangos reservados/especiales para no bloquear
// tráfico legítimo.
//
// FASE A: solo verificación + parseo (no descarga ni aplica todavía).
package main

import (
	"bufio"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
)

// NimShieldIntelPubKeyB64 es la clave PÚBLICA de producción de la fábrica
// (github.com/andresgv-beep/nimshield-intelligence). Verifica el feed; NO
// puede firmar. Es pública por diseño. La privada vive solo en la fábrica.
const NimShieldIntelPubKeyB64 = "NPE6bR2BvAefcDo+v6dtiZZcj8IOoK4M8fwnMOUDJ+0="

// IntelManifest refleja el manifest.json del feed (formato schema v1).
type IntelManifest struct {
	SchemaVersion int                 `json:"schema_version"`
	FeedVersion   int                 `json:"feed_version"`
	GeneratedAt   string              `json:"generated_at"`
	Files         []IntelManifestFile `json:"files"`
}

type IntelManifestFile struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	SHA256  string `json:"sha256"`
	Entries int    `json:"entries"`
	Action  string `json:"action"`
}

// intelSupportedSchema es el schema máximo que este NimOS entiende. Un feed con
// schema mayor se acepta parcialmente (lo conocido) — forward-compatible.
const intelSupportedSchema = 1

func intelPublicKey() (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(NimShieldIntelPubKeyB64)
	if err != nil {
		return nil, fmt.Errorf("clave pública embebida inválida: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("clave pública con tamaño inesperado: %d", len(raw))
	}
	return ed25519.PublicKey(raw), nil
}

// verifyManifestSignature comprueba la firma ed25519 del manifest. `sigB64` es
// el contenido de manifest.json.sig (base64). Verifica sobre los BYTES EXACTOS
// del manifest (por eso la fábrica usa .gitattributes -text: que GitHub no
// cambie los LF y rompa la firma).
func verifyManifestSignature(manifestBytes []byte, sigB64 string) error {
	pub, err := intelPublicKey()
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sigB64))
	if err != nil {
		return fmt.Errorf("firma no es base64 válido: %w", err)
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("firma con tamaño inesperado: %d", len(sig))
	}
	if !ed25519.Verify(pub, manifestBytes, sig) {
		return fmt.Errorf("FIRMA INVÁLIDA — el manifest no está firmado por la clave de producción (¿manipulado?)")
	}
	return nil
}

// parseManifest deserializa y valida el manifest (tras verificar la firma).
func parseManifest(manifestBytes []byte) (IntelManifest, error) {
	var m IntelManifest
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		return m, fmt.Errorf("manifest ilegible: %w", err)
	}
	if m.SchemaVersion < 1 {
		return m, fmt.Errorf("schema_version inválida: %d", m.SchemaVersion)
	}
	// Forward-compat (#2): un feed con schema mayor del que entendemos se acepta
	// parcialmente — cargamos los tipos conocidos (blocklist_ip) e ignoramos los
	// nuevos. Avisamos para que el admin sepa que su NimOS va por detrás del feed.
	if m.SchemaVersion > intelSupportedSchema {
		logMsg("intel: AVISO feed schema_version=%d > soportado=%d — se cargará lo conocido; conviene actualizar NimOS",
			m.SchemaVersion, intelSupportedSchema)
	}
	if len(m.Files) == 0 {
		return m, fmt.Errorf("manifest sin ficheros")
	}
	return m, nil
}

// verifyFileHash comprueba que el contenido de un fichero casa con el sha256
// declarado en el manifest (que ya está firmado → cadena de confianza).
func verifyFileHash(content []byte, expectedHex string) error {
	sum := sha256.Sum256(content)
	got := hex.EncodeToString(sum[:])
	if got != strings.ToLower(strings.TrimSpace(expectedHex)) {
		return fmt.Errorf("hash no coincide: esperado %s, obtenido %s", expectedHex, got)
	}
	return nil
}

// loadBlocklistInto parsea un fichero de blocklist (una IP/CIDR por línea) y
// mete las entradas válidas en el trie con la acción dada. Filtra rangos
// reservados/especiales (loopback, privados, multicast, 0.0.0.0/8…) para no
// bloquear tráfico legítimo aunque la fuente los incluya. Devuelve cuántas
// metió y cuántas descartó.
func loadBlocklistInto(trie *IntelTrie, content []byte, action string) (added, skipped int) {
	sc := bufio.NewScanner(strings.NewReader(string(content)))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p, ok := parsePrefix(line)
		if !ok {
			skipped++
			continue
		}
		if isReservedPrefix(p) {
			skipped++ // nunca bloqueamos rangos especiales
			continue
		}
		trie.insert(p, action)
		added++
	}
	return added, skipped
}

// parsePrefix acepta "1.2.3.4" (→/32), "1.2.3.0/24", "2001:db8::1" (→/128),
// "2001:db8::/32" y devuelve un netip.Prefix canónico.
func parsePrefix(s string) (netip.Prefix, bool) {
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, false
		}
		return p.Masked(), true
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, false
	}
	bits := 32
	if addr.Is6() {
		bits = 128
	}
	return netip.PrefixFrom(addr, bits), true
}

// isReservedPrefix descarta rangos que NUNCA deben bloquearse: loopback,
// privados (RFC1918), link-local, multicast, no-especificado (0.0.0.0/8), y
// prefijos demasiado amplios (un /0../7 indicaría un feed roto).
func isReservedPrefix(p netip.Prefix) bool {
	addr := p.Addr()
	// Prefijos absurdamente amplios = feed roto / footgun. No los aplicamos.
	if addr.Is4() && p.Bits() < 8 {
		return true
	}
	if addr.Is6() && p.Bits() < 16 {
		return true
	}
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() {
		return true
	}
	// 0.0.0.0/8 (red "este host") — IsUnspecified solo cubre 0.0.0.0/32.
	if addr.Is4() {
		b := addr.As4()
		if b[0] == 0 {
			return true
		}
	}
	return false
}

// buildTrieFromManifest construye un trie nuevo a partir del manifest verificado
// y los contenidos de los ficheros (ya validados por hash). Devuelve el trie y
// un resumen por fichero.
func buildTrieFromManifest(m IntelManifest, files map[string][]byte) (*IntelTrie, []string, error) {
	trie := newIntelTrie()
	var summary []string
	for _, f := range m.Files {
		if f.Type != "blocklist_ip" {
			continue // tipos futuros (dominios, asn…) se ignoran de momento
		}
		content, ok := files[f.Name]
		if !ok {
			return nil, nil, fmt.Errorf("falta el fichero %s referido por el manifest", f.Name)
		}
		if err := verifyFileHash(content, f.SHA256); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		action := f.Action
		if action == "" {
			action = "observe"
		}
		added, skipped := loadBlocklistInto(trie, content, action)
		summary = append(summary, fmt.Sprintf("%s: +%d (-%d reservadas/inválidas) [%s]", f.Name, added, skipped, action))
	}
	return trie, summary, nil
}
