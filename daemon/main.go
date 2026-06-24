// NimOS — Permissions Daemon (nimos-daemon)
//
// Runs as root. Listens on Unix socket only (+ HTTP API en loopback).
// Accepts a closed catalog of operations — nothing else.
// Enforces permissions at the filesystem level (groups + ACLs).
//
// Socket: /run/nimos-daemon.sock
// Build:  go build -o nimos-daemon .
//
// ── Estructura (refactor 11/06/2026 · antes era un main.go de 991 líneas) ──
//
//	main.go       → entry point, config global, logging
//	boot.go       → secuencia de arranque en fases (bootCore/HTTP/Storage/Services)
//	exec.go       → capa de ejecución de comandos (runSafe/runShellStatic) · CRÍTICA
//	validate.go   → validación de input (regexes allowlist, check*/isValid*)
//	socket_api.go → API privilegiada del socket Unix (handleOp, reconcile, server)
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// ═══════════════════════════════════
// Configuration
// ═══════════════════════════════════

var (
	sharesFile  = getEnv("NIMOS_SHARES_FILE", "/var/lib/nimos/config/shares.json")
	usersFile   = getEnv("NIMOS_USERS_FILE", "/var/lib/nimos/config/users.json")
	serviceUser = getEnv("NIMOS_USER", "nimos")
	poolBase    = "/nimos/pools/"
)

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ═══════════════════════════════════
// Logging
// ═══════════════════════════════════

func logMsg(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	log.Printf("[nimos-daemon] %s %s", time.Now().UTC().Format(time.RFC3339Nano)[:23]+"Z", msg)
}

// ═══════════════════════════════════
// Entry point
// ═══════════════════════════════════

func main() {
	logMsg("NimOS Permissions Daemon starting...")
	logMsg("Socket: %s", socketPath)
	logMsg("Shares config: %s", sharesFile)
	logMsg("Database: %s", dbPath)

	// Initialize SQLite database. El defer db.Close() vive aquí (no en
	// bootCore) porque debe sobrevivir hasta que main() retorne.
	if err := openDB(); err != nil {
		logMsg("Fatal: %v", err)
		os.Exit(1)
	}
	defer db.Close()
	logMsg("Database ready")

	// Fases de arranque · orden = contrato. Detalle en boot.go.
	bootCore()     // DB schemas + módulos storage/network (aborta si falla)
	bootHTTP()     // HTTP API (loopback) + mantenimiento
	bootStorage()  // montaje pools + recovery + monitoring (síncrono)
	bootServices() // schedulers background (backup, salud, shield…)

	// Servidor del socket Unix privilegiado · bloquea hasta shutdown.
	runSocketServer()
}

// installShutdownHandler instala el manejador de SIGTERM/SIGINT: para los
// schedulers con stop explícito, cierra el listener y elimina el socket.
// (Los demás schedulers usan goroutines que mueren con el proceso.)
func installShutdownHandler(listener interface{ Close() error }) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		logMsg("Shutting down (signal: %v)...", sig)
		stopBackupScheduler()
		stopAutoDiscovery()
		listener.Close()
		os.Remove(socketPath)
		os.Exit(0)
	}()
}
