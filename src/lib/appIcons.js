/**
 * NimOS · Mapeo de appId → identificador de icono
 * ────────────────────────────────────────────────
 * Centraliza la asociación entre appId y el identificador lógico
 * del icono. AppIcon resuelve a /icons/<theme>/<id>.svg según el tema.
 *
 * Uso:
 *   import { getAppIcon } from '$lib/appIcons.js';
 *   <AppIcon src={getAppIcon('storage')} alt="Storage" />
 *
 * Para retrocompat, algunos appIds pueden mapear a iconos PNG legacy
 * (ruta absoluta empezando por '/'). AppIcon los detecta y los sirve
 * tal cual sin pasar por el resolver de tema.
 */

const APP_ICONS = {
  // Apps del sistema (SVG adaptativo dark/light)
  nimhealth:   'nimhealth',
  nimshield:   'nimshield',
  network:     'network',
  storage:     'storage',
  files:       'files',
  filemanager: 'files',
  settings:    'nimsettings',
  nimsettings: 'nimsettings',
  controlpanel: 'panelcontrol', // icono propio (mezclador). light = placeholder dark hasta rediseño
  nimtorrent:  'nimtorrent',
  nimbackup:   'nimbackup',
  notes:       'notes',
  terminal:    'terminal',
  appstore:    'appstore',
  media:       'mediaplayer',
  mediaplayer: 'mediaplayer',
  vms:         'vms',

  // Iconos legacy PNG aún no rediseñados.
  // Se sirven como ruta directa (AppIcon detecta el '/' inicial).
  containers:  '/icons/containers.png',
  users:       '/icons/users.png',
  lock:        '/icons/lock.png',
};

/**
 * Devuelve el identificador del icono para un appId, o null si no existe.
 */
export function getAppIcon(appId) {
  if (!appId) return null;
  return APP_ICONS[appId.toLowerCase()] || null;
}

/**
 * Devuelve el objeto completo (si quieres iterar).
 */
export const appIcons = APP_ICONS;
