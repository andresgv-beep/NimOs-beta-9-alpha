// api.js — Cliente HTTP del módulo Storage.
//
// Extraído de StorageApp.svelte en la Fase 2 del refactor modular.
// Centraliza las 8+ llamadas a /api/storage/v2/* en funciones nombradas
// con tipos de retorno predecibles y errores normalizados.
//
// Beneficio operativo: cuando llegue CRIT-1 (If-Match header en mutations),
// la modificación vive en UN solo sitio (unwrap + addIfMatch helper) en
// lugar de tener que tocar 8 fetches dispersos.
//
// Beneficio conceptual: el componente deja de mezclar "qué pedir" con
// "cómo pedirlo".

import { hdrs, jsonHdrs } from '$lib/stores/auth.js';

const BASE = '/api/storage/v2';

// ────────────────────────────────────────────────────────────────────────
// unwrap — normaliza respuesta v2.
//
// Soporta dos formatos:
//   · v2:     {data: payload} ó {error: {code, message, details}}
//   · legacy: payload directo (array u objeto)
//
// Si OK con {data}, devuelve data. Si OK sin {data}, devuelve body tal cual.
// Si error: lanza Error con .code y .details.
// ────────────────────────────────────────────────────────────────────────
async function unwrap(res, label = 'api call') {
  let body;
  try {
    body = await res.json();
  } catch {
    throw new Error(`${label}: invalid JSON response (status ${res.status})`);
  }
  if (!res.ok) {
    let code = `http_${res.status}`;
    let msg = res.statusText || 'request failed';
    let details;
    if (body?.error) {
      if (typeof body.error === 'string') {
        msg = body.error;
        code = body.error;
      } else if (typeof body.error === 'object') {
        code = body.error.code || code;
        msg = body.error.message || msg;
        details = body.error.details;
      }
    }
    const e = new Error(`${label}: ${msg}`);
    e.code = code;
    e.details = details;
    throw e;
  }
  if (body && typeof body === 'object' && 'data' in body && !Array.isArray(body)) {
    return body.data;
  }
  return body;
}

// ────────────────────────────────────────────────────────────────────────
// GETs — consultas sin efectos
// ────────────────────────────────────────────────────────────────────────

export async function getPools() {
  const res = await fetch(`${BASE}/pools`, { headers: hdrs() });
  return unwrap(res, 'pools');
}

export async function getStatus() {
  const res = await fetch(`${BASE}/status`, { headers: hdrs() });
  return unwrap(res, 'status');
}

export async function getDisks() {
  const res = await fetch(`${BASE}/disks`, { headers: hdrs() });
  return unwrap(res, 'disks');
}

export async function getAlerts() {
  const res = await fetch(`${BASE}/alerts`, { headers: hdrs() });
  return unwrap(res, 'alerts');
}

export async function getCapabilities() {
  const res = await fetch(`${BASE}/capabilities`, { headers: hdrs() });
  return unwrap(res, 'capabilities');
}

/**
 * getObserved — devuelve el snapshot del observer.
 * Si refresh=true, fuerza un re-scan antes de leer (más caro).
 */
export async function getObserved({ refresh = false } = {}) {
  const url = refresh ? `${BASE}/observed?refresh=true` : `${BASE}/observed`;
  const res = await fetch(url, { headers: hdrs() });
  return unwrap(res, 'observed');
}

export async function getSnapshots(poolName) {
  const res = await fetch(
    `${BASE}/snapshots?pool=${encodeURIComponent(poolName)}`,
    { headers: hdrs() }
  );
  return unwrap(res, 'snapshots');
}

// ────────────────────────────────────────────────────────────────────────
// Acciones (POST) — mutaciones
// ────────────────────────────────────────────────────────────────────────

/**
 * importPool — adopta un filesystem BTRFS huérfano como pool managed.
 * Equivalente backend: POST /api/storage/v2/pools/import
 */
export async function importPool({ uuid, name }) {
  const res = await fetch(`${BASE}/pools/import`, {
    method: 'POST',
    headers: jsonHdrs(),
    body: JSON.stringify({ uuid, name }),
  });
  return unwrap(res, 'pool import');
}

/**
 * wipeDisk — borra el contenido de un disco.
 * `force=true` permite wipe sobre disco con BTRFS huérfano. Sin force,
 * el preflight aborta si detecta filesystem.
 */
export async function wipeDisk(path, { force = false } = {}) {
  const res = await fetch(`${BASE}/wipe`, {
    method: 'POST',
    headers: jsonHdrs(),
    body: JSON.stringify({ disk: path, force }),
  });
  return unwrap(res, 'wipe disk');
}

/**
 * scanDisks — re-escanea los discos del sistema (refresca clasificación
 * eligible/nvme/usb/provisioned/orphan_filesystem).
 */
export async function scanDisks() {
  const res = await fetch(`${BASE}/scan`, {
    method: 'POST',
    headers: hdrs(),
  });
  return unwrap(res, 'scan disks');
}

/**
 * startScrub — dispara un scrub del pool. Asíncrono: devuelve OK al
 * arrancar el scrub, no al terminarlo.
 */
export async function startScrub(poolName) {
  const res = await fetch(`${BASE}/scrub`, {
    method: 'POST',
    headers: jsonHdrs(),
    body: JSON.stringify({ pool: poolName }),
  });
  return unwrap(res, 'scrub start');
}

// ────────────────────────────────────────────────────────────────────────
// Upgrade de profile (single → raid1): añadir disco + convertir + progreso.
// El convert es ASYNC en el backend: devuelve la Operation in_progress y el
// balance corre en background. El progreso se consulta con getBalanceStatus.
// ────────────────────────────────────────────────────────────────────────

export async function addDeviceToPool(poolId, devicePath) {
  const res = await fetch(`${BASE}/pools/${poolId}/devices`, {
    method: 'POST',
    headers: jsonHdrs(),
    body: JSON.stringify({ device_path: devicePath }),
  });
  return unwrap(res, 'add device');
}

export async function convertPoolProfile(poolId, newProfile) {
  const res = await fetch(`${BASE}/pools/${poolId}/convert-profile`, {
    method: 'POST',
    headers: jsonHdrs(),
    body: JSON.stringify({ new_profile: newProfile }),
  });
  return unwrap(res, 'convert profile');
}

export async function getBalanceStatus(poolId) {
  const res = await fetch(`${BASE}/pools/${poolId}/balance-status`, { headers: hdrs() });
  return unwrap(res, 'balance status');
}

export async function getOperations({ poolId, status, limit } = {}) {
  const params = new URLSearchParams();
  if (poolId) params.set('pool_id', poolId);
  if (status) params.set('status', status);
  if (limit) params.set('limit', String(limit));
  const res = await fetch(`${BASE}/operations?${params}`, { headers: hdrs() });
  return unwrap(res, 'operations');
}
