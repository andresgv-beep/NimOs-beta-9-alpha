// formatters.js — Helpers de presentación puros para Storage.
//
// Extraídos de StorageApp.svelte en la Fase 1 del refactor modular.
// Todas las funciones son puras: no leen estado del componente, no
// hacen fetch, no tocan DOM. Recibir input → devolver string/variant.
//
// Esto las hace trivialmente testables y reutilizables en cualquier
// componente del módulo storage (vistas, wizards, modales).

/**
 * fmtBytes — Formatea bytes a la unidad más legible.
 *
 * CONVENCIÓN DE PRODUCTO (decisión 2026-07-04, a petición del usuario):
 * división BINARIA (1024) con etiquetas TB/GB — la convención de facto del
 * ecosistema NAS (Synology, QNAP, Windows, lsblk). Un disco de 4 TB de
 * caja se muestra "3.6 TB", igual que en Synology. Lo INNEGOCIABLE (bug
 * F7 de la auditoría) es que TODAS las superficies usen la misma base:
 * app, widget y móvil salen de este helper.
 *
 * null/undefined = dato AUSENTE → '—'; 0 real → '0 B'.
 */
const KIB = 1024, MIB = 1024 ** 2, GIB = 1024 ** 3, TIB = 1024 ** 4;

export function fmtBytes(b) {
  if (b == null) return '—';
  if (!b || b === 0) return '0 B';
  if (b >= TIB) return (b / TIB).toFixed(1) + ' TB';
  if (b >= GIB) return (b / GIB).toFixed(1) + ' GB';
  if (b >= MIB) return (b / MIB).toFixed(0) + ' MB';
  if (b >= KIB) return (b / KIB).toFixed(0) + ' KB';
  return b + ' B';
}

/**
 * splitBytes — Como fmtBytes pero devuelve { n, u } por separado, para
 * UIs que renderizan número y unidad con estilos distintos (widgets).
 * Misma convención binaria estilo NAS que fmtBytes (ver arriba).
 */
export function splitBytes(b) {
  if (b == null) return { n: '—', u: '' };
  if (b >= TIB) return { n: (b / TIB).toFixed(1), u: 'TB' };
  if (b >= GIB) return { n: (b / GIB).toFixed(0), u: 'GB' };
  if (b >= MIB) return { n: (b / MIB).toFixed(0), u: 'MB' };
  if (b >= KIB) return { n: (b / KIB).toFixed(0), u: 'KB' };
  return { n: String(b), u: 'B' };
}

/**
 * fmtDate — Formatea fecha ISO a "dd/mm/yyyy hh:mm" en es-ES.
 * Si ISO es falsy → '—'. Si falla el parse → devuelve el input tal cual.
 */
export function fmtDate(iso) {
  if (!iso) return '—';
  try {
    const d = new Date(iso);
    return d.toLocaleDateString('es-ES') + ' ' + d.toLocaleTimeString('es-ES', { hour: '2-digit', minute: '2-digit' });
  } catch { return iso; }
}

/**
 * inferDiskRole — Rol visual de un disco: 'mirror' en espejos, 'data' en
 * el resto. BTRFS no tiene discos de paridad dedicados.
 */
export function inferDiskRole(disks, idx, vdevType) {
  // AUDIT (menor): se eliminó la heurística que etiquetaba discos como
  // "parity" por su POSICIÓN en perfiles raidzN — nomenclatura ZFS que
  // BTRFS no soporta y que no reflejaba ningún layout real: era un badge
  // inventado. mirror → 'mirror'; todo lo demás → 'data'.
  const v = (vdevType || '').toLowerCase();
  if (v === 'mirror') return 'mirror';
  return 'data';
}

/**
 * healthLabel — Traduce el estado de salud del observer a español.
 * Vocabulario del observer (storage_observe_types.go):
 *   healthy | incomplete | degraded | partial | unknown
 */
export function healthLabel(h) {
  switch (h) {
    case 'healthy':     return 'sano';
    case 'incomplete':  return 'incompleto';
    case 'degraded':    return 'degradado';
    case 'partial':     return 'parcial';
    case 'unknown':     return 'desconocido';
    default:            return h || '—';
  }
}

/**
 * healthVariant — Mapea el estado del observer a variant de Badge.
 * Vocabulario: mismos estados que healthLabel.
 */
export function healthVariant(h) {
  switch (h) {
    case 'healthy':     return 'success';
    case 'incomplete':  return 'warn';
    case 'degraded':    return 'warn';
    case 'partial':     return 'critical';
    default:            return 'default';
  }
}

/**
 * usageVariant — Mapea porcentaje de uso (0-100) a variant de barra.
 * Umbrales: 90+ crítico, 75+ warn, resto ok.
 */
export function usageVariant(pct) {
  if (pct >= 90) return 'crit';
  if (pct >= 75) return 'warn';
  return 'ok';
}

/**
 * ledVariantForHealth — Mapea PoolHealth.Status del backend v2 a variant de LED.
 * Vocabulario de PoolHealth (distinto al del observer):
 *   healthy | at_risk | unstable | degraded | critical | missing
 */
export function ledVariantForHealth(health) {
  const h = (health || '').toLowerCase();
  if (h === 'healthy')                              return 'ok';
  if (h === 'at_risk' || h === 'unstable')          return 'warn';
  if (h === 'degraded')                             return 'warn';
  if (h === 'critical')                             return 'crit';
  if (h === 'missing')                              return 'off'; // gris: ausente
  return 'off';
}

/**
 * healthStatusLabel — Etiqueta legible (es) para PoolHealth.Status.
 */
export function healthStatusLabel(health) {
  const h = (health || '').toLowerCase();
  switch (h) {
    case 'healthy':  return 'correcto';
    case 'at_risk':  return 'en riesgo';
    case 'unstable': return 'inestable';
    case 'degraded': return 'degradado';
    case 'critical': return 'crítico';
    case 'missing':  return 'no detectado';
    default:         return health || '—';
  }
}

/**
 * smartVariant — Mapea SMART status (ok/warning/critical/missing) a variant.
 * 'missing' se trata como 'crit' (sin datos SMART = problema).
 */
export function smartVariant(smartStatus) {
  if (smartStatus === 'ok')       return 'ok';
  if (smartStatus === 'warning')  return 'warn';
  if (smartStatus === 'critical') return 'crit';
  if (smartStatus === 'missing')  return 'crit';
  return 'off';
}
