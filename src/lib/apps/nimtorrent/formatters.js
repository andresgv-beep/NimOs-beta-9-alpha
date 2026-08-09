export function formatBytes(bytes) {
  if (bytes === null || bytes === undefined || bytes < 0) return '—';
  if (bytes === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1000)), units.length - 1);
  const value = bytes / Math.pow(1000, index);
  return `${index === 0 ? Math.round(value) : value.toFixed(1)} ${units[index]}`;
}

export function formatRate(bytes) {
  return !bytes || bytes < 1 ? '—' : `${formatBytes(bytes)}/s`;
}

export function formatEta(torrent) {
  if (torrent.state === 'seeding') return 'Completado';
  if (torrent.paused || torrent.state === 'paused') return 'En pausa';
  if (torrent.state === 'error') return 'Error';
  const remaining = (torrent.total_wanted || 0) - (torrent.total_done || 0);
  if (remaining <= 0) return '—';
  if (!torrent.download_rate || torrent.download_rate < 1) return 'Calculando…';
  const seconds = Math.round(remaining / torrent.download_rate);
  if (seconds >= 86400) return `${Math.floor(seconds / 86400)} d ${Math.floor((seconds % 86400) / 3600)} h`;
  if (seconds >= 3600) return `${Math.floor(seconds / 3600)} h ${Math.floor((seconds % 3600) / 60)} min`;
  if (seconds >= 60) return `${Math.floor(seconds / 60)} min`;
  return `${seconds} s`;
}

export function percent(progress) {
  return Math.max(0, Math.min(100, Math.round((progress || 0) * 100)));
}

export function visualState(torrent) {
  if (torrent.paused || torrent.state === 'paused') return 'paused';
  if (torrent.state === 'error') return 'error';
  if (torrent.state === 'seeding') return 'seeding';
  if (torrent.state === 'checking') return 'checking';
  return 'downloading';
}

export const stateLabels = {
  downloading: 'Descargando',
  metadata: 'Obteniendo metadatos',
  seeding: 'Compartiendo',
  paused: 'Pausado',
  error: 'Error',
  checking: 'Verificando',
  queued: 'En cola',
  finished: 'Completado',
};
