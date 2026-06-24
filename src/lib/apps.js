/**
 * NimOS Beta 8.1 · App Manifest
 * ────────────────────────────────
 *
 * Este archivo declara las apps disponibles en el launcher y el taskbar.
 *
 * SCOPE BETA 8.1 v1 (core — incluidas):
 *   - Files, NimSettings, Storage, Network, NimTorrent
 *   - AppStore, NimBackup, NimHealth, NimShield
 *   - Terminal, Notes
 *
 * POSPUESTAS PARA BETA 9+ (no incluidas):
 *   - MediaPlayer  → en un NAS normalmente Jellyfin vía Docker
 *   - Virtual Machines → mover a AppStore como instalación opcional
 *   - NimLink → reevaluar si sigue siendo relevante
 *
 * WIDGETS DEL SYSTRAY (no son apps con ventana, viven en el taskbar):
 *   - Transferencias (popover) → se abre como panel lateral, no ventana
 *   - Notificaciones (panel)   → igual
 *
 * ICONOS:
 *   - Identificador lógico (ej: 'storage') que AppIcon resuelve a
 *     /icons/<dark|light>/<id>.svg según el tema activo
 *   - Si el icono falla, se usa el emoji de fallback en `fallback`
 *   - Para iconos legacy PNG aún no rediseñados, usar la ruta directa
 *     "/icons/X.png" (AppIcon los pasa tal cual)
 *
 * CATEGORÍAS (para el launcher agrupado):
 *   - 'system'     → apps core de NimOS
 *   - 'utilities'  → herramientas (Terminal, Notes)
 *   - 'docker'     → apps externas instaladas desde AppStore (se cargan dinámicamente)
 */

export const APP_META = {
  files: {
    name:     'Files',
    icon:     'files',
    fallback: '📁',
    width:    1000,
    height:   640,
    category: 'system',
  },

  nimsettings: {
    name:     'NimSettings',
    icon:     'nimsettings',
    fallback: '⚙️',
    width:    920,
    height:   600,
    category: 'system',
  },

  controlpanel: {
    name:     'Panel de Control',
    icon:     'panelcontrol',
    fallback: '🛠️',
    width:    980,
    height:   640,
    category: 'system',
  },

  storage: {
    name:     'Storage',
    icon:     'storage',
    fallback: '🗄️',
    width:    1040,
    height:   660,
    category: 'system',
  },

  network: {
    name:     'Network',
    icon:     'network',
    fallback: '🌐',
    width:    920,
    height:   600,
    category: 'system',
  },

  nimtorrent: {
    name:     'NimTorrent',
    icon:     'nimtorrent',
    fallback: '⬇️',
    width:    900,
    height:   580,
    category: 'app',
  },

  appstore: {
    name:     'App Store',
    icon:     'appstore',
    fallback: '🛍️',
    width:    980,
    height:   640,
    category: 'system',
  },

  nimbackup: {
    name:     'NimBackup',
    icon:     'nimbackup',
    fallback: '📦',
    width:    980,
    height:   640,
    category: 'system',
  },

  nimhealth: {
    name:     'NimHealth',
    icon:     'nimhealth',
    fallback: '💓',
    width:    1080,
    height:   680,
    category: 'system',
  },

  nimshield: {
    name:     'NimShield',
    icon:     'nimshield',
    fallback: '🛡️',
    width:    1080,
    height:   680,
    category: 'system',
  },

  terminal: {
    name:     'Terminal',
    icon:     'terminal',
    fallback: '💻',
    width:    820,
    height:   520,
    category: 'utilities',
  },

  notes: {
    name:     'Notes',
    icon:     'notes',
    fallback: '📝',
    width:    900,
    height:   620,
    category: 'app',
  },
};

/**
 * Helpers
 */
export function getAppMeta(appId) {
  return APP_META[appId] || null;
}

export function listAppsByCategory(category) {
  return Object.entries(APP_META)
    .filter(([, meta]) => meta.category === category)
    .map(([id, meta]) => ({ id, ...meta }));
}

export function listAllApps() {
  return Object.entries(APP_META).map(([id, meta]) => ({ id, ...meta }));
}
