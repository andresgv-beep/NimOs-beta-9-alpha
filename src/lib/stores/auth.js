import { writable, derived } from 'svelte/store';

const API = '/api/auth';

// Core state
export const appState = writable('loading'); // 'loading' | 'wizard' | 'login' | 'desktop'
export const user = writable(null);
export const token = writable('');

// Derived
export const isLoggedIn = derived(appState, $s => $s === 'desktop');
export const isAdmin = derived(user, $u => $u?.role === 'admin');

// SEGURIDAD (#1 crítico): el token de sesión NO se persiste en JS. El servidor
// pone una cookie HttpOnly (invisible a JS) y la auth va por ella. Antes se
// guardaba el token en localStorage y en un document.cookie legible por JS, lo
// que convertía cualquier XSS en robo de sesión. Aquí solo lo mantenemos en
// memoria de forma transitoria (login → reload); tras el reload es cookie-only.
function saveToken(t) {
  token.set(t); // solo en memoria (transitorio); NO localStorage, NO document.cookie
}

// Get current token value synchronously
let currentToken = '';
token.subscribe(t => currentToken = t);
export function getToken() { return currentToken; }

// Centralized auth headers — use these instead of defining hdrs() in each component
export function hdrs() { return { 'Authorization': `Bearer ${currentToken}` }; }
export function jsonHdrs() { return { 'Authorization': `Bearer ${currentToken}`, 'Content-Type': 'application/json' }; }

// Initialize — check status + restore session (vía la cookie HttpOnly).
export async function init() {
  // Limpieza única: borrar el token que versiones anteriores dejaban en
  // localStorage (vector de robo por XSS). NO tocamos la cookie aquí para no
  // cerrar sesiones existentes; la auth va por la cookie HttpOnly.
  try { localStorage.removeItem('nimos_token'); } catch {}

  try {
    const statusRes = await fetch(`${API}/status`);
    const status = await statusRes.json();

    if (!status.setup) {
      appState.set('wizard');
      return;
    }

    // Restaurar sesión con la cookie HttpOnly: se envía sola en same-origin.
    // Sin token en JS; /me autentica por la cookie (getBearerToken cae a ella).
    const meRes = await fetch(`${API}/me`);
    const me = await meRes.json();
    if (me.user) {
      user.set(me.user);
      appState.set('desktop');
      return;
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
  // Siempre pedimos el logout al server: la cookie HttpOnly autentica la
  // petición y el server INVALIDA la sesión y BORRA la cookie. (Antes se
  // saltaba si no había token en JS → con cookie-only no cerraría sesión.)
  try {
    await fetch(`${API}/logout`, { method: 'POST' });
  } catch {}
  saveToken('');
  user.set(null);
  appState.set('login');
}

export function lock() {
  appState.set('login');
}
