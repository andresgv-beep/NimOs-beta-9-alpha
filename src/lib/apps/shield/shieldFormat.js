// shieldFormat.js — Formateadores y constantes compartidos por las vistas de
// NimShield. Las funciones de tiempo reciben `now` (el tick de 1s del store)
// para que los countdowns se actualicen en vivo sin cerrar sobre estado.

export const sevClass = { critical: 'crit', high: 'high', medium: 'med', low: 'med' };

export const catShort = {
  auth: 'AUTH', traversal: 'TRAV', injection: 'INJ',
  scan: 'SCAN', honeypot: 'HONEY', system: 'SYS',
};

export const repLevelMeta = {
  habitual: { label: 'habitual', cls: 'lvl-trusted' },
  known: { label: 'conocida', cls: 'lvl-known' },
  unknown: { label: 'desconocida', cls: 'lvl-unknown' },
  distrust: { label: 'desconfianza', cls: 'lvl-distrust' },
};

export function fmtTime(ts) {
  const d = new Date(ts);
  if (isNaN(d)) return '—';
  return d.toLocaleTimeString('es-ES', { hour12: false });
}

// countdown "23m 14s" / "23h 41m" / "3d 2h"
export function fmtExpires(expiresAt, now) {
  const ms = Date.parse(expiresAt) - now;
  if (isNaN(ms) || ms <= 0) return 'expirado';
  const s = Math.floor(ms / 1000);
  if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60}s`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
  return `${Math.floor(s / 86400)}d ${Math.floor((s % 86400) / 3600)}h`;
}

export function expiresSoon(expiresAt, now) {
  const ms = Date.parse(expiresAt) - now;
  return !isNaN(ms) && ms > 0 && ms < 30 * 60 * 1000;
}

// "hace 12m" / "1h 22m" / "hace 12 días"
export function fmtAgo(createdAt, now, long = false) {
  const ms = now - Date.parse(createdAt);
  if (isNaN(ms) || ms < 0) return '—';
  const s = Math.floor(ms / 1000);
  if (s < 60) return long ? 'hace un momento' : `${s}s`;
  if (s < 3600) return (long ? 'hace ' : '') + `${Math.floor(s / 60)}m`;
  if (s < 86400) return (long ? 'hace ' : '') + `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
  const d = Math.floor(s / 86400);
  return long ? `hace ${d} día${d === 1 ? '' : 's'}` : `${d}d`;
}
