// shieldStore.js — Estado y lógica de datos de NimShield (separado de la
// presentación, igual que filesStore.js). Centraliza las llamadas a la API,
// el refresco con polling, el tick de 1s para countdowns y las acciones
// (toggle, unblock, whitelist, olvidar reputación, guardar config).

import { writable, get as getStore } from 'svelte/store';
import { jsonHdrs as hdrs } from '$lib/stores/auth.js';

export const status = writable({ enabled: false, blockedIPs: 0, honeypots: 0, rules: 0 });
export const events = writable([]);
export const blocks = writable([]);
export const whitelist = writable([]);
export const reputation = writable([]);
export const config = writable(null);
export const configDefaults = writable(null);
export const loading = writable(true);
export const adminRequired = writable(false);
export const now = writable(Date.now());
export const busy = writable(new Set());

// ─── API ───
async function api(path, opts) {
  const r = await fetch('/api/shield/' + path, opts);
  return r;
}
async function getJSON(path) {
  const r = await api(path, { headers: hdrs() });
  if (r.status === 403) { adminRequired.set(true); return null; }
  if (!r.ok) return null;
  return r.json();
}
async function send(path, method, body) {
  const r = await api(path, {
    method,
    headers: hdrs(),
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!r.ok) {
    let msg = 'Error';
    try { const e = await r.json(); if (e.error) msg = e.error; } catch {}
    throw new Error(msg);
  }
  return r.json();
}
export const post = (path, body) => send(path, 'POST', body);
export const put = (path, body) => send(path, 'PUT', body);

export async function refresh() {
  const [s, e, b, wl, rep, cfg] = await Promise.all([
    getJSON('status'), getJSON('events?limit=200'), getJSON('blocks'),
    getJSON('whitelist'), getJSON('reputation'), getJSON('config'),
  ]);
  if (s) status.set(s);
  if (e) events.set(e.events || []);
  if (b) blocks.set((b.blocks || []).sort((x, y) => (x.expiresAt < y.expiresAt ? -1 : 1)));
  if (wl) whitelist.set(wl.whitelist || []);
  if (rep) reputation.set(rep.reputation || []);
  if (cfg) { config.set(cfg.config); configDefaults.set(cfg.defaults); }
  loading.set(false);
}

// ─── Lifecycle (polling + tick) ───
let pollInterval = null;
let tickInterval = null;
export function startShield() {
  refresh();
  pollInterval = setInterval(refresh, 5000);
  tickInterval = setInterval(() => now.set(Date.now()), 1000);
}
export function stopShield() {
  if (pollInterval) clearInterval(pollInterval);
  if (tickInterval) clearInterval(tickInterval);
  pollInterval = tickInterval = null;
}

// ─── Helpers de busy ───
function markBusy(ip) { busy.update(s => new Set(s).add(ip)); }
function clearBusy(ip) { busy.update(s => { const n = new Set(s); n.delete(ip); return n; }); }
export function isBusy(ip) { return getStore(busy).has(ip); }

// ─── Acciones ───
export async function toggleEngine() {
  try {
    const r = await post('toggle');
    status.update(s => ({ ...s, enabled: r.enabled }));
  } catch { /* sin permiso o error */ }
}

export async function unblock(ip) {
  if (isBusy(ip)) return;
  markBusy(ip);
  try { await post('unblock', { ip }); } catch {}
  clearBusy(ip);
  await refresh();
}

export async function whitelistFromBlock(ip) {
  if (isBusy(ip)) return;
  markBusy(ip);
  try { await post('whitelist', { ip, note: 'añadida desde bloqueos' }); } catch {}
  clearBusy(ip);
  await refresh();
}

export async function addWhitelist(ip, note) {
  await post('whitelist', { ip, note }); // deja propagar el error al llamador
  await refresh();
}

export async function removeWhitelist(ip) {
  if (isBusy(ip)) return;
  markBusy(ip);
  try { await post('whitelist/remove', { ip }); } catch {}
  clearBusy(ip);
  await refresh();
}

export async function forgetReputation(ip) {
  if (isBusy(ip)) return;
  markBusy(ip);
  try { await post('reputation/forget', { ip }); await refresh(); } catch {}
  finally { clearBusy(ip); }
}

export async function saveConfig(draft) {
  const r = await put('config', draft); // el error se propaga al componente
  config.set(r.config);
  return r.config;
}
