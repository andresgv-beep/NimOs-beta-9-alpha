package main

// NimOS Storage — Constants, globals, JSON helpers
//
// Beta 8.1 Bloque C (Sesión 3): este archivo ya no contiene wrappers
// del antiguo adapter de Beta 7 (eliminados getStorageConfigFull,
// saveStorageConfigFull, getStoragePoolsGo, hasPoolGo). Toda la
// lectura/escritura de pools va directa al storageService
// (storage_service.go).

import (
	"encoding/json"
	"sync"
)

// ─── Constants ───────────────────────────────────────────────────────────────

const nimosPoolsDir = "/nimos/pools"

// storageConfigFile · ruta del JSON storage.json de Beta 7.
//
// Usada solo por el migrador one-shot al boot (migrateFromJSON en db.go,
// invocado desde boot.go). Tras el primer arranque, el path está renombrado a
// storage.json.migrated-<timestamp> y deja de existir como fuente de verdad.
//
// Variable (no const) para que los tests puedan apuntarla a un tempdir.
var storageConfigFile = "/var/lib/nimos/config/storage.json"

// ─── Global vars ─────────────────────────────────────────────────────────────

var hasBtrfs bool

// storageAlertsGo: lista actual de alertas de storage (caché en memoria).
// Recalculada periódicamente por checkStorageHealthGo.
var storageAlertsGo []map[string]interface{}

// storageAlertsMu protege storageAlertsGo (lecturas concurrentes).
var storageAlertsMu sync.RWMutex

// ─── JSON helpers (used across storage) ──────────────────────────────────────

func jsonToInt64(v interface{}) int64 {
	switch val := v.(type) {
	case float64:
		return int64(val)
	case string:
		return parseInt64(val)
	case json.Number:
		n, _ := val.Int64()
		return n
	}
	return 0
}

func jsonToBool(v interface{}) bool {
	switch val := v.(type) {
	case bool:
		return val
	case string:
		return val == "1" || val == "true"
	case float64:
		return val == 1
	}
	return false
}
