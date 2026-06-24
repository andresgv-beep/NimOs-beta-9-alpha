import { writable, derived } from 'svelte/store';

let nextZ = 100;
let counter = 0;
const posMap = {}; // { [id]: { x, y, width, height } } — hot state, no reactivity needed

export const windows = writable({});

export const windowList = derived(windows, $w => Object.values($w));

// ─── Geometría de viewport · ÚNICO sitio que lee zoom/taskbar ───
// maximizeWindow y refitWindow comparten esta verdad (no más copias
// divergentes de innerWidth/zoom/taskbar). openWindow conserva su
// propia mate de centrado inicial a propósito (cascada al abrir).
// Coordenadas LÓGICAS (post-zoom): innerWidth/zoom.
function viewport() {
  const tbPos = document.documentElement.getAttribute('data-taskbar-pos') || 'bottom';
  const tbH = parseInt(getComputedStyle(document.documentElement).getPropertyValue('--taskbar-height')) || 48;
  const zoom = parseFloat(document.documentElement.style.zoom) || 1;
  const vpW = window.innerWidth / zoom;
  const vpH = window.innerHeight / zoom;
  const offsetLeft = tbPos === 'left' ? tbH : 0;
  const offsetTop = tbPos === 'top' ? tbH : 0;
  return { tbPos, tbH, zoom, vpW, vpH, offsetLeft, offsetTop };
}

// Rect de una ventana maximizada para el viewport actual (misma mate
// que usaba maximizeWindow inline; ahora vive aquí y la comparten
// maximize + reflow).
function maximizedRect() {
  const { tbPos, tbH, vpW, vpH } = viewport();
  let x = 0, y = 0, width = vpW, height = vpH;
  if (tbPos === 'bottom') height -= tbH;
  else if (tbPos === 'top') { y = tbH; height -= tbH; }
  else if (tbPos === 'left') { x = tbH; width -= tbH; }
  return { x, y, width, height };
}

// Re-encaja un rect en el viewport actual: mantiene el tamaño si cabe,
// lo recorta si ya no, y reposiciona para que la ventana no se salga
// por abajo/derecha. Mismos mínimos que el resize manual (400×300).
function fitRect(rect) {
  const { tbPos, tbH, vpW, vpH, offsetLeft, offsetTop } = viewport();
  const usableW = vpW - offsetLeft;
  const usableH = vpH - offsetTop - (tbPos === 'bottom' ? tbH : 0);
  const width = Math.max(400, Math.min(rect.width, usableW));
  const height = Math.max(300, Math.min(rect.height, usableH));
  const x = Math.min(Math.max(rect.x, offsetLeft), offsetLeft + usableW - width);
  const y = Math.min(Math.max(rect.y, offsetTop), offsetTop + usableH - height);
  return { x, y, width, height };
}

// Re-encaja la ventana `id` al viewport actual y sincroniza posMap.
// Devuelve el rect nuevo (o null si la ventana ya no existe) para que
// WindowFrame vuelque a sus x/y/w/h locales. Llamado en window.resize.
export function refitWindow(id, maximized) {
  const pos = posMap[id];
  if (!pos) return null;
  const rect = maximized ? maximizedRect() : fitRect(pos);
  Object.assign(posMap[id], rect);
  return rect;
}

export function openWindow(appId, options = {}, webAppData = null) {
  const id = `w${++counter}`;
  const { width: reqW = 800, height: reqH = 520 } = options;

  const tbPos = document.documentElement.getAttribute('data-taskbar-pos') || 'bottom';
  const tbH = parseInt(getComputedStyle(document.documentElement).getPropertyValue('--taskbar-height')) || 48;
  const zoom = parseFloat(document.documentElement.style.zoom) || 1;
  const offsetLeft = tbPos === 'left' ? tbH : 0;
  const offsetTop = tbPos === 'top' ? tbH : 0;

  // Use zoomed viewport dimensions (CSS zoom adjusts innerWidth/Height automatically)
  const availW = (window.innerWidth / zoom) - offsetLeft;
  const availH = ((window.innerHeight / zoom) - (tbPos !== 'left' ? tbH : 0));
  const width = Math.min(reqW, availW - 40);
  const height = Math.min(reqH, availH - 40);

  const offset = (counter % 6) * 30;
  const vpW = window.innerWidth / zoom;
  const vpH = window.innerHeight / zoom;
  const x = Math.max(offsetLeft + 20, Math.min((vpW - width) / 2 + offset, vpW - width - 10));
  const y = Math.max(offsetTop + 20, Math.min((vpH - height) / 2 - 40 + offset, vpH - height - tbH - 10));
  const zIndex = nextZ++;

  posMap[id] = { x, y, width, height };

  windows.update(w => ({
    ...w,
    [id]: {
      id, appId, zIndex, minimized: false, maximized: false, prevRect: null,
      isWebApp: webAppData?.isWebApp || false,
      webAppPort: webAppData?.port || null,
      webAppName: webAppData?.appName || null,
      webAppLandingPath: webAppData?.landingPath || '',
      gameData: webAppData?.gameData || null,
      filesTarget: webAppData?.filesTarget || null,
    },
  }));

  return id;
}

export function closeWindow(id) {
  delete posMap[id];
  windows.update(w => {
    const next = { ...w };
    delete next[id];
    return next;
  });
}

export function focusWindow(id) {
  windows.update(w => ({
    ...w,
    [id]: { ...w[id], zIndex: nextZ++, minimized: false },
  }));
}

export function minimizeWindow(id) {
  windows.update(w => ({
    ...w,
    [id]: { ...w[id], minimized: true },
  }));
}

export function maximizeWindow(id) {
  windows.update(w => {
    const win = w[id];
    const pos = posMap[id];
    if (!win || !pos) return w;

    // Restaurar: re-encaja el rect previo por si el viewport cambió
    // mientras estaba maximizada (escala de SO / cambio de monitor).
    if (win.maximized && win.prevRect) {
      Object.assign(posMap[id], fitRect(win.prevRect));
      return { ...w, [id]: { ...win, maximized: false, prevRect: null } };
    }

    // Restaurar sin prevRect (caso raro): centrar un rect por defecto.
    if (win.maximized) {
      posMap[id] = fitRect({ x: 0, y: 0, width: 800, height: 520 });
      return { ...w, [id]: { ...win, maximized: false, prevRect: null } };
    }

    // Maximizar: rect a pantalla completa del viewport actual.
    const prevRect = { ...pos };
    posMap[id] = maximizedRect();
    return { ...w, [id]: { ...win, maximized: true, prevRect } };
  });
}

export function restoreWindow(id) {
  windows.update(w => ({
    ...w,
    [id]: { ...w[id], minimized: false, zIndex: nextZ++ },
  }));
}

// Hot updates — no reactivity, direct DOM manipulation during drag
export function updateWindowPos(id, updates) {
  if (posMap[id]) Object.assign(posMap[id], updates);
}

export function getWindowPos(id) {
  return posMap[id] || { x: 0, y: 0, width: 800, height: 520 };
}
