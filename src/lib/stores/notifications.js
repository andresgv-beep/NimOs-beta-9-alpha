import { writable, derived, get } from 'svelte/store';

export const notifications = writable([]);
export const unreadCount = derived(notifications, $n => $n.filter(x => !x.read).length);

// ── Auth token helper ──
function getAuthToken() {
  try { return localStorage.getItem('nimos_token') || ''; } catch { return ''; }
}
function hdrs() {
  return { 'Authorization': `Bearer ${getAuthToken()}`, 'Content-Type': 'application/json' };
}

// ── Load from backend on init ──
// Preserves local-only notifications (like login SMART alert) and sorts critical first
export async function loadNotifications() {
  try {
    const r = await fetch('/api/notifications?limit=100', { headers: hdrs() });
    if (!r.ok) return;
    const d = await r.json();
    const backend = d.notifications || [];

    // Preserve local-only entries (no numeric id — they have string ids like 'smart-login-xxx')
    const current = get(notifications);
    const localOnly = current.filter(n => typeof n.id === 'string' && !backend.some(b => b.id === n.id));

    // Merge: local-only + backend, then sort by severity
    const merged = [...localOnly, ...backend];
    merged.sort(sortBySeverity);

    notifications.set(merged);
  } catch {}
}

// Sort: critical/error first, then warning, then by date
const SEVERITY_ORDER = { error: 0, security: 0, critical: 0, warning: 1, info: 2, success: 3 };
function sortBySeverity(a, b) {
  const sa = SEVERITY_ORDER[a.type] ?? 2;
  const sb = SEVERITY_ORDER[b.type] ?? 2;
  if (sa !== sb) return sa - sb;
  // Same severity — newest first
  return new Date(b.timestamp || 0) - new Date(a.timestamp || 0);
}

// ── Create notification (persisted to backend) ──
export async function notify(message, options = {}) {
  const {
    type     = 'info',
    category = 'notification',
    title    = '',
    bubble   = true,
  } = options;

  try {
    const r = await fetch('/api/notifications', {
      method: 'POST', headers: hdrs(),
      body: JSON.stringify({ type, category, title, message }),
    });
    const d = await r.json();
    if (d.ok && d.notification) {
      const entry = { ...d.notification, showBubble: bubble };
      notifications.update(n => [entry, ...n]);
      return entry.id;
    }
  } catch {}

  // Fallback: local only if backend unavailable
  const id = Date.now();
  notifications.update(n => [{
    id, type, category, title, message,
    timestamp: new Date().toISOString(),
    read: false, showBubble: bubble,
  }, ...n]);
  return id;
}

// ── Helpers ──
export function notifySuccess(message, title = '')  { return notify(message, { type: 'success', title }); }
export function notifyError(message,   title = '')  { return notify(message, { type: 'error',   title }); }
export function notifyWarning(message, title = '')  { return notify(message, { type: 'warning',  title }); }
export function notifyInfo(message,    title = '')  { return notify(message, { type: 'info',     title }); }
export function notifySecurity(message, title = 'Seguridad') {
  return notify(message, { type: 'security', category: 'system', title });
}
export function notifySystem(message, title = 'Sistema') {
  return notify(message, { type: 'info', category: 'system', title });
}

// ── Mark read ──
export async function markRead(id) {
  notifications.update(n => n.map(x => x.id === id ? { ...x, read: true } : x));
  try { await fetch(`/api/notifications/${id}/read`, { method: 'PUT', headers: hdrs() }); } catch {}
}

export async function markAllRead() {
  notifications.update(n => n.map(x => ({ ...x, read: true })));
  try { await fetch('/api/notifications/read-all', { method: 'PUT', headers: hdrs() }); } catch {}
}

// ── Dismiss ──
export async function dismissNotification(id) {
  notifications.update(n => n.filter(x => x.id !== id));
  try { await fetch(`/api/notifications/${id}`, { method: 'DELETE', headers: hdrs() }); } catch {}
}

// ── Clear by category ──
export async function clearCategory(category) {
  notifications.update(n => n.filter(x => x.category !== category));
  try { await fetch(`/api/notifications?category=${category}`, { method: 'DELETE', headers: hdrs() }); } catch {}
}

export async function clearAll() {
  notifications.set([]);
  try { await fetch('/api/notifications', { method: 'DELETE', headers: hdrs() }); } catch {}
}

// ── Hide bubble (local only — no backend needed) ──
export function hideBubble(id) {
  notifications.update(n => n.map(x => x.id === id ? { ...x, showBubble: false } : x));
}
