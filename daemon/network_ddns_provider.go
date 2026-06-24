// network_ddns_provider.go — Interfaz DDNSProvider y tipos comunes.
//
// Cada proveedor de DDNS (DuckDNS, No-IP, Dynu, ...) implementa esta
// interfaz. El reconciler las consume sin saber cuál es cuál.
//
// F-004 entrega:
//   - La interfaz DDNSProvider.
//   - Un solo implementador concreto: DuckDNSProvider (network_ddns_duckdns.go).
//
// Cuando aparezcan más proveedores se añaden archivos paralelos sin
// modificar esta interfaz (Open/Closed Principle aplicado donde tiene
// sentido — no por anticipación, sino porque el contrato real existe ya).
//
// El reconciler mantiene un map[providerName]DDNSProvider y selecciona
// según el campo `provider` de cada fila network_ddns.

package main

import (
	"context"
	"errors"
)

// ─────────────────────────────────────────────────────────────────────────────
// Errors comunes
// ─────────────────────────────────────────────────────────────────────────────

// ErrDDNSProviderUnknown se devuelve si el reconciler encuentra una fila
// network_ddns con provider="X" y no hay implementador registrado para X.
var ErrDDNSProviderUnknown = errors.New("ddns provider not registered")

// ErrDDNSAuthFailed indica que el proveedor rechazó las credenciales
// (token inválido/expirado, dominio no propio, etc.). El caller debería
// emitir un evento warn y, opcionalmente, marcar el DDNS como
// enabled=false hasta que el usuario rote el token.
var ErrDDNSAuthFailed = errors.New("ddns provider rejected credentials")

// ErrDDNSTransient marca fallos recuperables (timeout, 5xx temporal,
// red caída). El breaker contará estas y abrirá si exceden el umbral.
// El reconciler debe reintentar en su próximo tick.
var ErrDDNSTransient = errors.New("ddns provider transient failure")

// ─────────────────────────────────────────────────────────────────────────────
// DDNSUpdateResult
// ─────────────────────────────────────────────────────────────────────────────

// DDNSUpdateResult describe el resultado de un Update exitoso.
//
//   - NewIP: la IP que el proveedor confirma para el dominio. Puede ser
//     "" si el proveedor no la devuelve (p.ej. respuestas tipo "OK").
//   - Changed: true si el proveedor indica que la IP ha cambiado en este
//     update (DuckDNS responde "OK" tanto si cambia como si no, así que
//     el provider concreto decide cómo poblarlo — para DuckDNS será
//     siempre `false` y dependerá del caller comparar con last_ip).
//   - NoChange: true si el proveedor explícitamente indica que la IP no
//     ha cambiado. Útil para emitir eventos en nivel debug en vez de info.
//   - RawResponse: el cuerpo crudo que devolvió el provider, para audit
//     trail. Se persiste en network_operations.data.
type DDNSUpdateResult struct {
	NewIP       string
	Changed     bool
	NoChange    bool
	RawResponse string
}

// ─────────────────────────────────────────────────────────────────────────────
// DDNSProvider
// ─────────────────────────────────────────────────────────────────────────────

// DDNSProvider es el contrato mínimo que debe cumplir un proveedor DDNS.
//
// IMPORTANTE — el provider NO conoce la DB ni los secretos directamente:
//   - El reconciler lee el secret de `nimos_secrets` y se lo pasa como
//     argumento explícito.
//   - El provider no decide si actualizar — el reconciler ya decidió
//     llamarlo.
//   - El provider devuelve sentinel errors (ErrDDNSAuthFailed,
//     ErrDDNSTransient) para que el reconciler sepa cómo clasificar el
//     resultado.
//
// El provider DEBE respetar el contexto: si ctx se cancela durante la
// llamada HTTP, abortar limpiamente.
type DDNSProvider interface {
	// Name devuelve el identificador estable del proveedor.
	// Debe matchear el valor en network_ddns.provider (e.g. "duckdns").
	Name() string

	// Update notifica al proveedor que la IP del dominio debe
	// actualizarse. La mayoría de proveedores DDNS detectan la IP
	// del cliente automáticamente al recibir la petición — el reconciler
	// no necesita conocerla.
	//
	// secret es la credencial cruda (token plaintext) que el reconciler
	// obtuvo descifrando nimos_secrets. El provider NO debe guardarla,
	// NO debe loguearla, y NO debe retornarla en errores.
	//
	// Devuelve:
	//   - DDNSUpdateResult, nil: éxito.
	//   - nil, ErrDDNSAuthFailed: credencial inválida (no reintentable).
	//   - nil, ErrDDNSTransient: fallo recuperable (red, 5xx).
	//   - nil, otro error: fallo inesperado (4xx no-auth, parse error,
	//     etc.). El reconciler lo trata como permanente para no spamear
	//     pero no abre el breaker.
	Update(ctx context.Context, domain, secret string) (*DDNSUpdateResult, error)
}
