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
 * Usa SI (1e3) no IEC (1024) para coincidir con la UI de discos.
 * 0 o falsy → '0 B'.
 */
export function fmtBytes(b) {
  if (!b || b === 0) return '0 B';
  if (b >= 1e12) return (b / 1e12).toFixed(1) + ' TB';
  if (b >= 1e9)  return (b / 1e9).toFixed(1)  + ' GB';
  if (b >= 1e6)  return (b / 1e6).toFixed(0)  + ' MB';
  if (b >= 1e3)  return (b / 1e3).toFixed(0)  + ' KB';
  return b + ' B';
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
 * inferDiskRole — Devuelve el rol visual de un disco dentro de un vdev.
 *
 * BTRFS no expone parity explícita por disco, pero para UI es útil
 * mostrar "data" vs "parity" en perfiles con redundancia paritaria.
 * Esto es heurística visual, no refleja el layout real on-disk.
 *
 * - raidz / raidz1   → último disco = parity, resto data
 * - raidz2           → últimos 2 = parity
 * - raidz3           → últimos 3 = parity
 * - mirror           → todos 'mirror'
 * - cualquier otro   → 'data'
 *
 * NOTA: los perfiles 'raidzN' son nomenclatura ZFS legada en la UI;
 * BTRFS no los soporta nativamente. Se mantienen por compatibilidad
 * mientras la UI no migre la nomenclatura.
 */
export function inferDiskRole(disks, idx, vdevType) {
  const v = (vdevType || '').toLowerCase();
  const n = disks.length;
  if (v === 'raidz' || v === 'raidz1') return idx === n - 1 ? 'parity' : 'data';
  if (v === 'raidz2') return idx >= n - 2 ? 'parity' : 'data';
  if (v === 'raidz3') return idx >= n - 3 ? 'parity' : 'data';
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
