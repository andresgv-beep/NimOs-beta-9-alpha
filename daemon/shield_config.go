// shield_config.go — NimShield · Política configurable (singleton en DB).
//
// Expone al admin la política de FUERZA BRUTA de login: cuántos fallos
// tolera cada nivel de reputación y cuánto dura el bloqueo, con ESCALADO
// por reincidencia (1ª vez 5min, 2ª 15min, 3ª 1h, 4ª+ 24h).
//
// NOTA: el on/off del motor NO vive aquí — se persiste aparte en la tabla
// shield_settings (dbShieldSetEnabled / loadShieldEnabled). Este módulo solo
// gobierna umbrales y duraciones.
//
// FUERA DE ALCANCE A PROPÓSITO: las reglas duras (inyección, honeypots,
// path traversal, scanner-UA) NO son configurables. Son defensa innegociable.
//
// Defaults = EXACTAMENTE el comportamiento hardcoded previo → actualizar no
// cambia nada hasta que el admin decida tocar algo.

package main

import (
	"sync"
	"time"
)

// ShieldConfig es la política mutable. Duraciones en minutos. El bloqueo
// permanente queda deliberadamente fuera (techo 24h).
type ShieldConfig struct {
	FailUnknown  int `json:"fail_unknown"`  // IP sin historial
	FailKnown    int `json:"fail_known"`    // con algún login exitoso
	FailHabitual int `json:"fail_habitual"` // habitual (margen amplio)

	DistrustStreak int `json:"distrust_streak"` // racha que dispara desconfianza

	BlockMin1 int `json:"block_min_1"` // 1er bloqueo de la IP
	BlockMin2 int `json:"block_min_2"` // 2º
	BlockMin3 int `json:"block_min_3"` // 3º
	BlockMin4 int `json:"block_min_4"` // 4º en adelante (techo)
}

func defaultShieldConfig() ShieldConfig {
	return ShieldConfig{
		FailUnknown:    5,
		FailKnown:      7,
		FailHabitual:   10,
		DistrustStreak: 3,
		BlockMin1:      5,
		BlockMin2:      15,
		BlockMin3:      60,
		BlockMin4:      1440, // 24h, techo (sin permanente)
	}
}

var (
	shieldCfgMu     sync.RWMutex
	shieldCfgCache  ShieldConfig
	shieldCfgLoaded bool
)

// dbShieldConfigInit crea la tabla y carga la config en caché. Se llama
// desde dbShieldInit.
func dbShieldConfigInit() {
	if db == nil {
		return
	}
	db.Exec(`
		CREATE TABLE IF NOT EXISTS shield_config (
			id            INTEGER PRIMARY KEY CHECK (id = 1),
			fail_unknown  INTEGER NOT NULL DEFAULT 5,
			fail_known    INTEGER NOT NULL DEFAULT 7,
			fail_habitual INTEGER NOT NULL DEFAULT 10,
			distrust_streak INTEGER NOT NULL DEFAULT 3,
			block_min_1   INTEGER NOT NULL DEFAULT 5,
			block_min_2   INTEGER NOT NULL DEFAULT 15,
			block_min_3   INTEGER NOT NULL DEFAULT 60,
			block_min_4   INTEGER NOT NULL DEFAULT 1440
		);
	`)
	cfg := loadShieldConfigFromDB()
	shieldCfgMu.Lock()
	shieldCfgCache = cfg
	shieldCfgLoaded = true
	shieldCfgMu.Unlock()
}

func loadShieldConfigFromDB() ShieldConfig {
	cfg := defaultShieldConfig()
	if db == nil {
		return cfg
	}
	err := db.QueryRow(`
		SELECT fail_unknown, fail_known, fail_habitual, distrust_streak,
		       block_min_1, block_min_2, block_min_3, block_min_4
		FROM shield_config WHERE id = 1
	`).Scan(&cfg.FailUnknown, &cfg.FailKnown, &cfg.FailHabitual, &cfg.DistrustStreak,
		&cfg.BlockMin1, &cfg.BlockMin2, &cfg.BlockMin3, &cfg.BlockMin4)
	if err != nil {
		saveShieldConfigToDB(cfg) // siembra defaults
		return cfg
	}
	return cfg
}

func saveShieldConfigToDB(cfg ShieldConfig) error {
	if db == nil {
		return nil
	}
	_, err := db.Exec(`
		INSERT INTO shield_config
			(id, fail_unknown, fail_known, fail_habitual, distrust_streak,
			 block_min_1, block_min_2, block_min_3, block_min_4)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			fail_unknown = excluded.fail_unknown,
			fail_known = excluded.fail_known,
			fail_habitual = excluded.fail_habitual,
			distrust_streak = excluded.distrust_streak,
			block_min_1 = excluded.block_min_1,
			block_min_2 = excluded.block_min_2,
			block_min_3 = excluded.block_min_3,
			block_min_4 = excluded.block_min_4
	`, cfg.FailUnknown, cfg.FailKnown, cfg.FailHabitual, cfg.DistrustStreak,
		cfg.BlockMin1, cfg.BlockMin2, cfg.BlockMin3, cfg.BlockMin4)
	return err
}

func getShieldConfig() ShieldConfig {
	shieldCfgMu.RLock()
	defer shieldCfgMu.RUnlock()
	if !shieldCfgLoaded {
		return defaultShieldConfig()
	}
	return shieldCfgCache
}

func setShieldConfig(cfg ShieldConfig) error {
	cfg = sanitizeShieldConfig(cfg)
	if err := saveShieldConfigToDB(cfg); err != nil {
		return err
	}
	shieldCfgMu.Lock()
	shieldCfgCache = cfg
	shieldCfgLoaded = true
	shieldCfgMu.Unlock()
	return nil
}

// sanitizeShieldConfig encierra los valores en rangos sanos para que un
// ajuste manual no deje NimShield inservible (ej. 0 fallos = autobloqueo).
func sanitizeShieldConfig(c ShieldConfig) ShieldConfig {
	clamp := func(v, lo, hi, def int) int {
		if v == 0 {
			return def
		}
		if v < lo {
			return lo
		}
		if v > hi {
			return hi
		}
		return v
	}
	d := defaultShieldConfig()
	c.FailUnknown = clamp(c.FailUnknown, 1, 100, d.FailUnknown)
	c.FailKnown = clamp(c.FailKnown, 1, 100, d.FailKnown)
	c.FailHabitual = clamp(c.FailHabitual, 1, 100, d.FailHabitual)
	c.DistrustStreak = clamp(c.DistrustStreak, 2, 20, d.DistrustStreak)
	c.BlockMin1 = clamp(c.BlockMin1, 1, 1440, d.BlockMin1)
	c.BlockMin2 = clamp(c.BlockMin2, 1, 1440, d.BlockMin2)
	c.BlockMin3 = clamp(c.BlockMin3, 1, 1440, d.BlockMin3)
	c.BlockMin4 = clamp(c.BlockMin4, 1, 1440, d.BlockMin4)
	return c
}

// escalatedBlockDuration elige la duración según cuántas veces ha sido
// bloqueada ya esta IP. offenseCount = bloqueos PREVIOS (0 = primera vez).
func escalatedBlockDuration(cfg ShieldConfig, offenseCount int) time.Duration {
	switch {
	case offenseCount <= 0:
		return time.Duration(cfg.BlockMin1) * time.Minute
	case offenseCount == 1:
		return time.Duration(cfg.BlockMin2) * time.Minute
	case offenseCount == 2:
		return time.Duration(cfg.BlockMin3) * time.Minute
	default:
		return time.Duration(cfg.BlockMin4) * time.Minute
	}
}

// shieldAuthDecision decide, ante un fallo de login, si toca bloquear y si es
// por desconfianza. Lee la política de config.
//
//	· DESCONFIANZA: una IP conocida cuya racha de fallos seguidos alcanza
//	  DistrustStreak → bloqueo INMEDIATO (caso "dispositivo robado").
//	· Si no, bloqueo cuando los fallos en la ventana de 5min alcanzan el
//	  umbral del nivel de reputación.
func shieldAuthDecision(cfg ShieldConfig, successCount, failStreak, windowCount int) (block, distrust bool) {
	distrust = successCount > 0 && failStreak >= cfg.DistrustStreak
	threshold := cfg.FailUnknown
	switch {
	case successCount >= repHabitualThreshold:
		threshold = cfg.FailHabitual
	case successCount >= repKnownThreshold:
		threshold = cfg.FailKnown
	}
	block = distrust || windowCount >= threshold
	return block, distrust
}
