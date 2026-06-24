import { writable } from 'svelte/store';
import { getToken } from './auth.js';

/**
 * widgetData · Polling centralizado para widgets de escritorio
 * ─────────────────────────────────────────────────────────────
 * Un solo punto de polling por topic, compartido entre widgets:
 *   - Si dos widgets consumen 'system', hay UN intervalo, no dos.
 *   - Si ningún widget consume un topic, NO se hace polling.
 *   - Pestaña oculta (visibilitychange) → todos los intervalos se
 *     pausan. Al volver, fetch inmediato + reanudación.
 *
 * Uso desde un widget:
 *   import { topicStore, acquire } from '$lib/stores/widgetData.js';
 *   const data = topicStore('system');     // store legible ($data)
 *   onMount(() => acquire('system'));      // devuelve release()
 *
 * acquire() devuelve la función de liberación → usable directamente
 * como cleanup de onMount.
 */

const TOPICS = {
  system:   { url: '/api/hardware/stats',       interval: 3000  },
  network:  { url: '/api/network',              interval: 3000  },
  storage:  { url: '/api/storage/v2/pools',     interval: 15000 },
  smart:    { url: '/api/disks/smart/summary',  interval: 60000 },
  services: { url: '/api/services',             interval: 10000 },
  torrent:  { url: '/api/torrent/torrents',     interval: 5000  }, // proxy Go → torrentd
};

// Estado interno por topic: { store, refs, timer }
const state = {};

function ensure(topic) {
  if (!state[topic]) {
    state[topic] = { store: writable(null), refs: 0, timer: null };
  }
  return state[topic];
}

async function fetchTopic(topic) {
  const def = TOPICS[topic];
  if (!def) return;
  try {
    const r = await fetch(def.url, {
      headers: { 'Authorization': `Bearer ${getToken() || ''}` },
    });
    if (!r.ok) return; // mantener último dato válido, no machacar con null
    const data = await r.json();
    ensure(topic).store.set(data);
  } catch {
    // red caída o daemon reiniciando: conservar último dato
  }
}

function startTimer(topic) {
  const s = ensure(topic);
  if (s.timer || document.hidden) return;
  fetchTopic(topic); // dato inmediato, sin esperar al primer tick
  s.timer = setInterval(() => fetchTopic(topic), TOPICS[topic].interval);
}

function stopTimer(topic) {
  const s = state[topic];
  if (s?.timer) {
    clearInterval(s.timer);
    s.timer = null;
  }
}

/**
 * Store legible del topic. Existe aunque no haya polling activo
 * (valor null hasta el primer fetch).
 */
export function topicStore(topic) {
  return ensure(topic).store;
}

/**
 * Registra un consumidor del topic. Arranca el polling si es el
 * primero. Devuelve release(): al liberarse el último consumidor,
 * el polling se detiene.
 */
export function acquire(topic) {
  if (!TOPICS[topic]) {
    console.warn(`[widgetData] topic desconocido: ${topic}`);
    return () => {};
  }
  const s = ensure(topic);
  s.refs += 1;
  if (s.refs === 1) startTimer(topic);

  let released = false;
  return () => {
    if (released) return; // release idempotente
    released = true;
    s.refs = Math.max(0, s.refs - 1);
    if (s.refs === 0) stopTimer(topic);
  };
}

// ─── Pausa global cuando la pestaña no es visible ───
if (typeof document !== 'undefined') {
  document.addEventListener('visibilitychange', () => {
    for (const topic of Object.keys(state)) {
      if (document.hidden) {
        stopTimer(topic);
      } else if (state[topic].refs > 0) {
        startTimer(topic); // hace fetch inmediato al volver
      }
    }
  });
}
