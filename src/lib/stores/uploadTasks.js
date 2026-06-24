import { writable, derived, get } from 'svelte/store';

// ═══════════════════════════════════════════════════════════════
// NimOS Upload Task Manager
// ═══════════════════════════════════════════════════════════════
//
// States: queued → uploading → done | error
//                 ↕ paused
//
// Features:
//   - Concurrent upload limit (MAX_CONCURRENT)
//   - Pause / resume per task
//   - Cancel with server-side chunk cleanup
//   - AbortController per task for fetch cancellation
//   - Resume from last completed chunk
// ═══════════════════════════════════════════════════════════════

export const uploadTasks = writable([]);

export const activeTasks = derived(uploadTasks, $t => $t.filter(t => t.status === 'uploading'));
export const hasActiveTasks = derived(activeTasks, $t => $t.length > 0);
export const queuedTasks = derived(uploadTasks, $t => $t.filter(t => t.status === 'queued'));

const MAX_CONCURRENT = 2;

let nextId = 0;
const controllers = new Map();  // taskId → AbortController
const taskMeta = new Map();     // taskId → { file, share, path, totalChunks, chunkSize, lastChunk }

// ── Auth helper ──
function getAuthToken() {
  try { return localStorage.getItem('nimos_token') || ''; } catch { return ''; }
}

// ── Add task to queue ──
export function addTask(name, size, file, share, path, totalChunks, chunkSize) {
  const id = ++nextId;
  taskMeta.set(id, { file, share, path, totalChunks, chunkSize, lastChunk: -1 });
  uploadTasks.update(t => [...t, {
    id, name, size, progress: 0,
    status: 'queued',
    error: '',
    speed: 0,
    startedAt: null,
    showBubble: true,
  }]);
  processQueue();
  return id;
}

// ── Hide bubble (visual only — does NOT stop the upload) ──
export function hideBubbleTask(id) {
  uploadTasks.update(t => t.map(x => x.id === id ? { ...x, showBubble: false } : x));
}

// ── Get signal for fetch abort ──
export function getSignal(id) {
  return controllers.get(id)?.signal;
}

// ── Progress ──
export function updateProgress(id, progress, speed = 0) {
  uploadTasks.update(t => t.map(x => x.id === id ? { ...x, progress, speed } : x));
}

// ── Complete ──
export function completeTask(id) {
  controllers.delete(id);
  taskMeta.delete(id);
  uploadTasks.update(t => t.map(x => x.id === id ? { ...x, progress: 100, status: 'done', speed: 0 } : x));
  processQueue();
}

// ── Fail ──
export function failTask(id, error = '') {
  controllers.delete(id);
  uploadTasks.update(t => t.map(x => x.id === id ? { ...x, status: 'error', error, speed: 0 } : x));
  processQueue();
}

// ── Pause ──
export function pauseTask(id) {
  const ctrl = controllers.get(id);
  if (ctrl) ctrl.abort();
  controllers.delete(id);
  // Save last completed chunk in meta
  uploadTasks.update(t => t.map(x => x.id === id && x.status === 'uploading'
    ? { ...x, status: 'paused', speed: 0 }
    : x
  ));
  processQueue();
}

// ── Resume ──
export function resumeTask(id) {
  uploadTasks.update(t => t.map(x => x.id === id && x.status === 'paused'
    ? { ...x, status: 'queued' }
    : x
  ));
  processQueue();
}

// ── Cancel ──
export async function cancelTask(id) {
  const ctrl = controllers.get(id);
  if (ctrl) ctrl.abort();
  controllers.delete(id);

  // Clean up server-side chunks
  const meta = taskMeta.get(id);
  if (meta) {
    try {
      await fetch('/api/files/upload-cancel', {
        method: 'POST',
        headers: {
          'Authorization': `Bearer ${getAuthToken()}`,
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ share: meta.share, path: meta.path, filename: meta.file.name }),
      });
    } catch {}
    taskMeta.delete(id);
  }

  uploadTasks.update(t => t.filter(x => x.id !== id));
  processQueue();
}

// ── Remove (completed/error only) ──
export function removeTask(id) {
  controllers.delete(id);
  taskMeta.delete(id);
  uploadTasks.update(t => t.filter(x => x.id !== id));
}

// ── Clear done ──
export function clearDone() {
  uploadTasks.update(t => {
    t.filter(x => x.status === 'done' || x.status === 'error').forEach(x => {
      controllers.delete(x.id);
      taskMeta.delete(x.id);
    });
    return t.filter(x => x.status !== 'done' && x.status !== 'error');
  });
}

// ── Queue processor ──
function processQueue() {
  const tasks = get(uploadTasks);
  const uploading = tasks.filter(t => t.status === 'uploading').length;
  const queued = tasks.filter(t => t.status === 'queued');
  const slots = MAX_CONCURRENT - uploading;

  for (let i = 0; i < Math.min(slots, queued.length); i++) {
    startUpload(queued[i].id);
  }
}

// ── Start uploading a task ──
async function startUpload(id) {
  const meta = taskMeta.get(id);
  if (!meta) return;

  const controller = new AbortController();
  controllers.set(id, controller);

  uploadTasks.update(t => t.map(x => x.id === id
    ? { ...x, status: 'uploading', startedAt: x.startedAt || Date.now() }
    : x
  ));

  const { file, share, path, totalChunks, chunkSize, lastChunk } = meta;
  const startFrom = lastChunk + 1;
  let lastTime = Date.now();
  let lastBytes = 0;

  try {
    for (let i = startFrom; i < totalChunks; i++) {
      // Check if aborted (paused/cancelled)
      if (controller.signal.aborted) return;

      const start = i * chunkSize;
      const end = Math.min(start + chunkSize, file.size);
      const chunk = file.slice(start, end);

      const resp = await fetch('/api/files/upload-chunk', {
        method: 'POST',
        signal: controller.signal,
        headers: {
          'Authorization': `Bearer ${getAuthToken()}`,
          'X-Share': share,
          'X-Path': path,
          'X-Filename': file.name,
          'X-Chunk-Index': String(i),
          'X-Total-Chunks': String(totalChunks),
          'X-Total-Size': String(file.size),
        },
        body: chunk,
      });

      const d = await resp.json();
      if (d.error) {
        const msg = (d.error.toLowerCase().includes('quota') || d.error.toLowerCase().includes('space') || d.error.toLowerCase().includes('full'))
          ? `Sin espacio: ${file.name}`
          : d.error;
        failTask(id, msg);
        return;
      }

      // Update progress and speed
      meta.lastChunk = i;
      const pct = Math.round(((i + 1) / totalChunks) * 100);
      const now = Date.now();
      const elapsed = (now - lastTime) / 1000;
      const bytesSent = end - (startFrom * chunkSize);
      let speed = 0;
      if (elapsed > 0.5) {
        speed = (bytesSent - lastBytes) / elapsed;
        lastTime = now;
        lastBytes = bytesSent;
      }
      updateProgress(id, pct, speed);
    }

    completeTask(id);
  } catch (err) {
    if (err.name === 'AbortError') return; // paused or cancelled
    failTask(id, 'Error de conexión');
  }
}

// ── Get task metadata (for TransferManager display) ──
export function getTaskMeta(id) {
  return taskMeta.get(id);
}
