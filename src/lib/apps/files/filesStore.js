/**
 * filesStore.js · helpers puros de la app Files
 * ─────────────────────────────────────────────────────────────
 * Funciones sin estado reactivo extraídas de FileManager.svelte
 * durante la modularización (paso 1, sin cambios de comportamiento).
 *
 * Aquí va SOLO lógica pura/reutilizable: formato de tamaños y fechas,
 * iconos, detección de tipo. El estado reactivo (currentShare, files,
 * selected, clipboard…) y la orquestación siguen en FileManager.svelte.
 */

// ── SVG de carpetas (local amarilla / remoto azul · dos tonos con pestaña) ──
const FOLDER_PATH_TAB = 'M853.333333 256H469.333333l-85.333333-85.333333H170.666667c-46.933333 0-85.333333 38.4-85.333334 85.333333v170.666667h853.333334v-85.333334c0-46.933333-38.4-85.333333-85.333334-85.333333z';
const FOLDER_PATH_BODY = 'M853.333333 256H170.666667c-46.933333 0-85.333333 38.4-85.333334 85.333333v426.666667c0 46.933333 38.4 85.333333 85.333334 85.333333h682.666666c46.933333 0 85.333333-38.4 85.333334-85.333333V341.333333c0-46.933333-38.4-85.333333-85.333334-85.333333z';

function folderSvg(size, tab, body) {
  // viewBox ajustado al contenido real (la carpeta vive entre y≈170 y y≈853)
  // para que ocupe como un emoji y no se vea pequeña al lado de los archivos.
  return `<svg width="${size}" height="${size}" viewBox="40 130 944 760" xmlns="http://www.w3.org/2000/svg"><path d="${FOLDER_PATH_TAB}" fill="${tab}"/><path d="${FOLDER_PATH_BODY}" fill="${body}"/></svg>`;
}

export const SVG_FOLDER_LOCAL     = folderSvg(40, '#FFA000', '#FFCA28');
export const SVG_FOLDER_REMOTE    = folderSvg(40, '#1E88E5', '#42A5F5');
export const SVG_FOLDER_SM_LOCAL  = folderSvg(22, '#FFA000', '#FFCA28');
export const SVG_FOLDER_SM_REMOTE = folderSvg(22, '#1E88E5', '#42A5F5');

// ── Tamaño legible ──
export function fmtSize(b) {
  if (!b) return '—';
  if (b >= 1e9) return (b / 1e9).toFixed(2) + ' GB';
  if (b >= 1e6) return (b / 1e6).toFixed(2) + ' MB';
  if (b >= 1e3) return (b / 1e3).toFixed(0) + ' KB';
  return b + ' B';
}

// ── Fecha legible dd/mm/aaaa hh:mm ──
export function fDate(iso) {
  if (!iso) return '—';
  const d = new Date(iso);
  return `${String(d.getDate()).padStart(2, '0')}/${String(d.getMonth() + 1).padStart(2, '0')}/${d.getFullYear()} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
}

// ── Extensión en mayúsculas (para columna Tipo) ──
export function fExt(name) {
  const p = name.lastIndexOf('.');
  return p >= 0 ? name.slice(p + 1).toUpperCase() : '—';
}

// ── ¿Es un .zip? ──
export function isZipFile(file) {
  return !file.isDirectory && file.name.toLowerCase().endsWith('.zip');
}

// ── Icono emoji por extensión ──
export function fIcon(file) {
  if (file.isDirectory) return '📁';
  const e = file.name.split('.').pop().toLowerCase();
  return { mp4: '🎬', mkv: '🎬', avi: '🎬', mov: '🎬', mp3: '🎵', wav: '🎵', flac: '🎵', jpg: '🖼️', jpeg: '🖼️', png: '🖼️', gif: '🖼️', svg: '🎨', pdf: '📕', doc: '📝', zip: '📦', rar: '📦', js: '💻', py: '💻', go: '💻', txt: '📄', md: '📄', json: '📄', html: '📄', css: '🅰', iso: '💿', sh: '🔧' }[e] || '📄';
}

// ── Icono HTML (SVG carpeta o emoji) ──
export function fIconHtml(file, small = false) {
  if (file.isDirectory) return small ? SVG_FOLDER_SM_LOCAL : SVG_FOLDER_LOCAL;
  return fIcon(file);
}

// ── Tipo legible para la columna "Tipo" (estilo Synology) ──
export function fType(file) {
  if (file.isDirectory) return 'Carpeta';
  const e = file.name.split('.').pop().toLowerCase();
  if (e === file.name.toLowerCase()) return 'Archivo';
  return e.toUpperCase();
}
