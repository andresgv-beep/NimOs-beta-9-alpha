import { writable, derived } from 'svelte/store';

const API = '/api/auth';
const TOKEN_KEY = 'nimos_token';

// Core state
export const appState = writable('loading'); // 'loading' | 'wizard' | 'login' | 'desktop'
export const user = writable(null);
export const token = writable('');

// Derived
export const isLoggedIn = derived(appState, $s => $s === 'desktop');
export const isAdmin = derived(user, $u => $u?.role === 'admin');

// Leer localStorage de forma segura (evita SSR y race conditions)
function readToken() {
  try { return localStorage.getItem(TOKEN_KEY) || ''; } catch { return ''; }
}

function saveToken(t) {
  token.set(t);
  try {
    if (t) localStorage.setItem(TOKEN_KEY, t);
    else localStorage.removeItem(TOKEN_KEY);
  } catch {}
  // Sync token to cookie for iframe sub-requests (/app/ proxy)
  try {
    if (t) document.cookie = `nimos_token=${t};path=/;SameSite=Strict`;
    else document.cookie = 'nimos_token=;path=/;expires=Thu, 01 Jan 1970 00:00:00 GMT';
  } catch {}
}

// Get current token value synchronously
let currentToken = '';
token.subscribe(t => currentToken = t);
export function getToken() { return currentToken; }

// Centralized auth headers — use these instead of defining hdrs() in each component
export function hdrs() { return { 'Authorization': `Bearer ${currentToken}` }; }
export function jsonHdrs() { return { 'Authorization': `Bearer ${currentToken}`, 'Content-Type': 'application/json' }; }

// Initialize — check status + restore session
export async function init() {
  // Leer el token aquí, dentro de onMount, cuando localStorage ya está disponible
  const storedToken = readToken();
  if (storedToken) {
    token.set(storedToken);
  }

  try {
    const statusRes = await fetch(`${API}/status`);
    const status = await statusRes.json();

    if (!status.setup) {
      appState.set('wizard');
      return;
    }

    if (storedToken) {
      const meRes = await fetch(`${API}/me`, {
        headers: { 'Authorization': `Bearer ${storedToken}` },
      });
      const me = await meRes.json();
      if (me.user) {
        user.set(me.user);
        appState.set('desktop');
        return;
      } else {
        // Token inválido o expirado — limpiar
        saveToken('');
      }
    }

    appState.set('login');
  } catch {
    appState.set('login');
  }
}

// Crea el administrador y AUTENTICA (guarda token), pero NO toca appState:
// el wizard sigue al mando para los pasos de 2FA y resumen. Punto de no retorno
// (el backend rechaza recrear el admin si ya existe usuario).
export async function createAdmin(username, password) {
  const res = await fetch(`${API}/setup`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  });
  const data = await res.json();
  if (data.error) throw new Error(data.error);
  saveToken(data.token);
  user.set(data.user);
  return data;
}

// Cierra el wizard → escritorio. Se llama en "Iniciar NimOS" (paso 5).
export function finishWizard() {
  appState.set('desktop');
}

// Back-compat: crear admin + ir a escritorio en un solo paso.
export async function completeSetup(username, password) {
  const data = await createAdmin(username, password);
  finishWizard();
  return data;
}

// Alta de 2FA. Requiere sesión (createAdmin ya autentica).
// Devuelve { ok, secret, uri (otpauth://), qr? (SVG si el server tiene qrencode) }.
export async function setup2fa() {
  const res = await fetch(`${API}/2fa/setup`, { method: 'POST', headers: hdrs() });
  const data = await res.json();
  if (data.error) throw new Error(data.error);
  return data;
}

// Verifica el código TOTP y activa 2FA.
// Devuelve { ok, message, backupCodes: [8] }.
export async function verify2fa(code) {
  const res = await fetch(`${API}/2fa/verify`, {
    method: 'POST',
    headers: jsonHdrs(),
    body: JSON.stringify({ code }),
  });
  const data = await res.json();
  if (data.error) throw new Error(data.error);
  return data;
}

export async function login(username, password, totpCode) {
  const res = await fetch(`${API}/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password, totpCode }),
  });
  const data = await res.json();
  if (data.requires2FA) return data;
  if (data.error) throw new Error(data.error);
  saveToken(data.token);
  user.set(data.user);
  // Reload page so the daemon serves HTML with user prefs injected server-side.
  // This eliminates the flash of default theme/layout after login.
  window.location.reload();
  return data;
}

export async function logout() {
  if (currentToken) {
    try {
      await fetch(`${API}/logout`, {
        method: 'POST',
        headers: { 'Authorization': `Bearer ${currentToken}` },
      });
    } catch {}
  }
  saveToken('');
  user.set(null);
  appState.set('login');
}

export function lock() {
  appState.set('login');
}
