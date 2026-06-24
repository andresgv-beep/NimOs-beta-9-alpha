/**
 * Catálogo de widgets · NimOS Beta 8.1
 * ─────────────────────────────────────
 * Registro central de los widgets de escritorio. WidgetLayer consume
 * este catálogo para colocar y arrastrar cajas — NO sabe qué pinta
 * cada widget. Cada widget es un componente Svelte autocontenido en
 * src/lib/widgets/ que recibe `widget` como prop y consume datos de
 * widgetData.js.
 *
 * Reglas (decisión de diseño, junio 2026):
 *   - Tamaños FIJOS por widget (w×h en celdas). Sin redimensión.
 *   - Catálogo cerrado: estos 5 widgets existen, el usuario activa
 *     o desactiva los que quiera desde el menú contextual.
 *   - `component: null` → WidgetLayer renderiza placeholder (fase
 *     contenedor). Al implementar cada widget, se importa aquí y
 *     se asigna. WidgetLayer no cambia.
 *
 * topic → clave de polling en widgetData.js que el widget necesita.
 */

import Clock from './Clock.svelte';
import SysMon from './SysMon.svelte';
import SysPanel from './SysPanel.svelte';
import Storage from './Storage.svelte';
import Network from './Network.svelte';
import RingSolo from './RingSolo.svelte';
import Services from './Services.svelte';
import Torrent from './Torrent.svelte';

/**
 * Geometría del grid de escritorio · config CENTRAL (jun 2026).
 * ─────────────────────────────────────────────────────────────
 * `cols` es FIJO a propósito: define el significado de col/row en
 * prefs.widgetLayout. Al no depender del viewport, un widget en
 * col=8 cae SIEMPRE en el mismo sitio relativo (1080p, 125%, 1440p,
 * 4K) → adiós a la recolocación al cambiar de escala/monitor.
 *
 * `cell` NO está aquí: es FLUIDA y la calcula WidgetLayer.measure()
 * como (ancho - 2·pad - (cols-1)·gap) / cols. La celda escala con la
 * pantalla; la posición lógica no se mueve (comportamiento DSM).
 *
 * `baseCell` = talla de referencia (px). La usa WidgetPicker para los
 * previews del catálogo, que son canónicos (un palette no es fluido).
 *
 * Para experimentar (12/14/16 cols en la ROG): cambiar SOLO `cols`
 * aquí. WidgetLayer y WidgetPicker lo consumen, no hay otro sitio.
 */
export const GRID = {
  cols:     12,   // FIJO · columnas del escritorio (invariante al viewport)
  gap:      14,   // px CSS · separación entre celdas
  pad:      20,   // px CSS · margen interior de la capa
  baseCell: 144,  // px CSS · talla de referencia para previews del picker
};

export const WIDGET_CATALOG = [
  {
    id: 'clock',
    group: 'General',
    order: 0,
    name: 'Reloj',
    icon: 'clock',
    desc: 'Hora y fecha',
    w: 1,
    h: 1,
    topic: null,          // no necesita datos del backend
    component: Clock,
    defaultOn: true,
    sizes: [[1, 1], [2, 1]],
  },
  {
    id: 'sysmon',
    group: 'Sistema',
    order: 2,
    mergeKey: 'sistema',  // se funde con syspanel en un solo apartado
    name: 'Sistema',
    icon: 'cpu',
    desc: 'CPU · RAM',
    w: 2,
    h: 1,
    topic: 'system',      // /api/hardware/stats · CPU + RAM rings
    component: SysMon,
    defaultOn: true,
    sizes: [[2, 1]],
  },
  {
    id: 'syspanel',
    group: 'Sistema',
    order: 3,
    mergeKey: 'sistema',  // se funde con sysmon en un solo apartado
    name: 'Sistema 2×2',
    icon: 'cpu',
    desc: 'CPU · RAM · temp · uptime',
    w: 2,
    h: 2,
    topic: 'system',      // mismo topic que sysmon · polling compartido
    component: SysPanel,
    defaultOn: false,     // existe en catálogo, apagado por defecto
    sizes: [[2, 2]],      // talla fija · es el rediseño 2×2 (junio 2026)
  },
  {
    id: 'cpu',
    group: 'Sistema',
    order: 0,
    name: 'CPU',
    icon: 'cpu',
    desc: 'Uso de CPU',
    w: 1,
    h: 1,
    topic: 'system',      // mismo topic que sysmon · un solo polling compartido
    component: RingSolo,
    props: { metric: 'cpu' },
    defaultOn: false,
    sizes: [[1, 1]],
  },
  {
    id: 'ram',
    group: 'Sistema',
    order: 1,
    name: 'RAM',
    icon: 'ram',
    desc: 'Uso de memoria',
    w: 1,
    h: 1,
    topic: 'system',      // mismo topic que sysmon
    component: RingSolo,
    props: { metric: 'ram' },
    defaultOn: false,
    sizes: [[1, 1]],
  },
  {
    id: 'storage',
    group: 'Almacenamiento',
    order: 0,
    name: 'Almacenamiento',
    icon: 'hdd',
    desc: 'Pools BTRFS',
    w: 2,
    h: 1,
    topic: 'storage',     // /api/storage/v2/pools (+smart en el widget)
    component: Storage,
    defaultOn: true,
    sizes: [[2, 1], [2, 2]],
    // Multi-instancia: una caja por pool. El picker lee el topic
    // 'storage' y ofrece añadir un widget por cada pool que aún no
    // tenga el suyo. El id de instancia es "storage:<pool>"; el
    // componente recibe config.pool con el nombre. Ver splitInstanceId.
    instancePer: 'pools',
  },
  {
    id: 'network',
    group: 'Red',
    order: 0,
    name: 'Red',
    icon: 'activity',
    desc: 'Tráfico de interfaces',
    w: 2,
    h: 1,
    topic: 'network',     // /api/network · sparklines DL/UL
    component: Network,
    defaultOn: true,
    sizes: [[2, 1], [2, 2]],
  },
  {
    id: 'services',
    group: 'General',
    order: 1,
    name: 'Servicios',
    icon: 'services',
    desc: 'Estado de servicios',
    w: 2,
    h: 1,
    topic: 'services',    // /api/services · NimHealth
    component: Services,
    defaultOn: true,
    sizes: [[1, 1], [2, 1], [2, 2]],
    // Orden en el widget: failed/error primero, luego degraded/stopped,
    // running al final. Lo que falla sube arriba solo.
  },
  {
    id: 'folders',
    group: 'Carpetas',
    order: 0,
    name: 'Carpetas compartidas',
    icon: 'folder',
    desc: 'Carpetas gestionadas',
    w: 2,
    h: 1,
    topic: 'folders',     // pendiente: endpoint de carpetas
    component: null,      // placeholder · fase contenedor
    defaultOn: false,
    sizes: [[2, 1], [2, 2]],
    configurable: 'folders', // mismo patrón que pools (futuro)
  },
  {
    id: 'nimshield',
    group: 'Seguridad',
    order: 0,
    name: 'NimShield',
    icon: 'shield',
    desc: 'Estado de seguridad',
    w: 2,
    h: 1,
    topic: 'nimshield',   // pendiente: endpoint NimShield
    component: null,      // placeholder · fase contenedor
    defaultOn: false,
    sizes: [[1, 1], [2, 1], [2, 2]],
  },
  {
    id: 'nimtorrent',
    group: 'Descargas',
    order: 0,
    name: 'NimTorrent',
    icon: 'download',
    desc: 'Descargas torrent',
    w: 2,
    h: 1,
    topic: 'torrent',     // /api/torrent/torrents · proxy Go → torrentd
    component: Torrent,
    defaultOn: false,     // existe en catálogo, apagado por defecto
    sizes: [[2, 1], [2, 2]],
  },
];

/** Lookup rápido por id. */
/**
 * splitInstanceId · separa un id de instancia en { base, variant }.
 * Los widgets multi-instancia usan ids derivados "base:variant"
 * (ej. "storage:data1"). El motor sigue tratando el id completo como
 * instancia única; solo el lookup de definición y la config saben
 * descomponerlo.
 */
export function splitInstanceId(id) {
  const i = (id || '').indexOf(':');
  if (i === -1) return { base: id, variant: null };
  return { base: id.slice(0, i), variant: id.slice(i + 1) };
}

const _byId = Object.fromEntries(WIDGET_CATALOG.map(w => [w.id, w]));

// Proxy: un id derivado "storage:data1" resuelve a la def base
// "storage" sin que el resto del motor tenga que cambiar.
export const WIDGET_BY_ID = new Proxy(_byId, {
  get(target, key) {
    if (typeof key === 'string' && !(key in target)) {
      const { base } = splitInstanceId(key);
      if (base in target) return target[base];
    }
    return target[key];
  },
  has(target, key) {
    if (typeof key === 'string' && !(key in target)) {
      return splitInstanceId(key).base in target;
    }
    return key in target;
  },
});

/**
 * Talla efectiva de una instancia de widget.
 * ──────────────────────────────────────────
 * `sizes` en el catálogo = tallas soportadas [[w,h],...]; w/h del
 * catálogo = talla de serie. El layout puede llevar `size: [w,h]`
 * por instancia (elegida en el menú contextual). Una talla guardada
 * que el catálogo ya no soporte cae a la de serie — nunca rompe.
 */
export function widgetSize(item, def) {
  if (Array.isArray(item?.size) && item.size.length === 2) {
    const [w, h] = item.size;
    if ((def.sizes || []).some(([sw, sh]) => sw === w && sh === h)) {
      return { w, h };
    }
  }
  return { w: def.w, h: def.h };
}

/**
 * Layout por defecto · columna anclada al borde derecho.
 * col/row negativos = intención "desde el borde derecho/inferior":
 *   col -1 → última columna · col -2 → penúltima (origen de un 2×1)
 * La resolución a celdas absolutas y el clamping ocurren SOLO en
 * render (WidgetLayer), nunca aquí ni al guardar.
 */
export const DEFAULT_LAYOUT = [
  { id: 'clock', col:-1, row: 0 },
  { id: 'sysmon', col:-2, row: 1 },
  { id: 'network', col:-2, row: 3 },
  { id: 'services', col:-2, row: 4 },
  // Storage ya no va por defecto: es multi-instancia (una caja por
  // pool, id "storage:<pool>"). El usuario añade las que quiera desde
  // el picker, que lee los pools en runtime.
];

/** Orden de familias en la ventana de añadir widgets. */
export const GROUP_ORDER = ['Sistema', 'Almacenamiento', 'Carpetas', 'General', 'Seguridad', 'Descargas', 'Red'];
