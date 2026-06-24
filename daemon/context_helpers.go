// context_helpers.go — Helpers de context.Context con semántica explícita.
//
// Origen: bug Nextcloud (26/05/2026). Al instalar Nextcloud desde el AppStore,
// el `docker compose up -d` tardó 6 minutos descargando ~1GB. El navegador
// dio timeout HTTP a los 60-120s y canceló el `r.Context()` del request.
//
// El handler seguía ejecutándose (el subprocess Docker corre independiente)
// pero al llegar a `appsRepo.CreateOrUpdateDockerApp(r.Context(), app)` el
// INSERT a `docker_apps` falló silenciosamente porque su context estaba
// cancelado. Resultado: container vivo en Docker, sin row en BD, invisible
// para todo el ecosistema NimOS.
//
// Este archivo establece la convención canónica para todo el daemon.
//
// ─────────────────────────────────────────────────────────────────────────
// REGLA
// ─────────────────────────────────────────────────────────────────────────
//
//   r.Context()         · operaciones cancelables si el cliente se va
//                         (SELECTs, lecturas, validaciones, launches que
//                         devuelven inmediato)
//
//   commitContext()     · operaciones que DEBEN persistir aunque el
//                         cliente se haya ido (INSERTs/UPDATEs/DELETEs
//                         post-acción, cleanups tras subprocess, refreshes
//                         de cache que afectan a otros consumidores)
//
// EL TEST: "si el cliente se desconecta a mitad de esta operación, ¿el
//          sistema queda consistente?"
//   · SÍ → r.Context() está bien
//   · NO → commitContext() obligatorio
//
// ─────────────────────────────────────────────────────────────────────────
// EJEMPLOS
// ─────────────────────────────────────────────────────────────────────────
//
//   // OK · solo lectura · si cliente se va, no pasa nada
//   apps, err := appsRepo.ListDockerApps(r.Context())
//
//   // OK · launch async · devuelve operationId inmediato
//   op, err := operationsRepo.Create(r.Context(), "docker.install", user)
//
//   // BUG (Nextcloud) · INSERT post-subprocess · cliente puede haberse ido
//   appsRepo.CreateOrUpdateDockerApp(r.Context(), app)  // ❌
//   appsRepo.CreateOrUpdateDockerApp(commitContext(), app)  // ✅
//
//   // BUG · cleanup tras delete · cliente puede haberse ido
//   ForceDockerCacheRefresh(r.Context())  // ❌
//   ForceDockerCacheRefresh(commitContext())  // ✅
//
// ─────────────────────────────────────────────────────────────────────────
// PATRÓN PROHIBIDO (rechazar en review)
// ─────────────────────────────────────────────────────────────────────────
//
//   - appsRepo.<Create|Update|Delete>(r.Context(), ...) tras subprocess
//   - operationsRepo.<Mark*>(r.Context(), ...) en goroutine async
//   - networkRepo.<Create|Update|Delete>(r.Context(), ...) tras acción real
//   - ForceDockerCacheRefresh(r.Context())
//   - cualquier refresh de cache que otros consumidores leerán

package main

import "context"

// commitContext devuelve un context.Background() con nombre semántico.
//
// Úsalo en operaciones de persistencia post-acción que deben sobrevivir
// al cierre de la conexión HTTP del cliente. Ver doc del archivo para
// la regla completa y ejemplos.
//
// Equivale a context.Background() pero el nombre comunica intención:
// el reviewer entiende inmediatamente que el dev tomó la decisión
// consciente de desacoplar esta operación del request HTTP.
func commitContext() context.Context {
	return context.Background()
}
