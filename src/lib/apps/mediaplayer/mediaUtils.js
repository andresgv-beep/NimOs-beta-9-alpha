// mediaUtils.js — Utilidades puras del MediaPlayer (detección de tipo,
// formato de tiempo, URL de streaming, subtítulos). Sin estado.

// Extensiones que el <video>/<audio> del navegador puede reproducir de forma
// razonable. mkv/mov son "quizás" (depende de códecs y navegador): se intentan
// y, si el elemento falla, el player muestra el aviso de formato.
const VIDEO_EXTS = new Set(['mp4', 'webm', 'ogv', 'mkv', 'mov', 'm4v']);
const AUDIO_EXTS = new Set(['mp3', 'wav', 'flac', 'aac', 'm4a', 'opus', 'ogg', 'oga']);
const SUB_EXTS = new Set(['vtt', 'srt']);

export function extOf(name) {
  const i = (name || '').lastIndexOf('.');
  return i < 0 ? '' : name.slice(i + 1).toLowerCase();
}

export const isVideoFile = (name) => VIDEO_EXTS.has(extOf(name));
export const isAudioFile = (name) => AUDIO_EXTS.has(extOf(name));
export const isMediaFile = (name) => isVideoFile(name) || isAudioFile(name);
export const isSubtitleFile = (name) => SUB_EXTS.has(extOf(name));

// streamUrl · URL del fichero para el elemento <video>/<audio>. La auth viaja
// en la cookie HttpOnly de sesión (same-origin) — NUNCA token en la URL. El
// backend soporta Range (seek) y sirve inline sin ?download=1.
export function streamUrl(share, path) {
  return `/api/files/download?share=${encodeURIComponent(share)}&path=${encodeURIComponent(path)}`;
}

// fmtTime · segundos → "m:ss" o "h:mm:ss". NaN/Infinity → "0:00".
export function fmtTime(s) {
  if (!isFinite(s) || s < 0) s = 0;
  s = Math.floor(s);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const x = s % 60;
  const mm = h > 0 ? String(m).padStart(2, '0') : String(m);
  return (h > 0 ? h + ':' : '') + mm + ':' + String(x).padStart(2, '0');
}

// baseName · nombre sin extensión ("Peli.2026.mkv" → "Peli.2026").
export function baseName(name) {
  const i = (name || '').lastIndexOf('.');
  return i < 0 ? name : name.slice(0, i);
}

// findSubtitleFor · busca en el listado un .vtt/.srt "hermano" del media:
// mismo basename exacto, o basename que empiece igual (cubre "Peli.esp.srt").
// Prefiere .vtt (nativo) sobre .srt (requiere conversión).
export function findSubtitleFor(mediaName, files) {
  const base = baseName(mediaName).toLowerCase();
  const subs = (files || []).filter((f) => !f.isDirectory && isSubtitleFile(f.name));
  const scored = subs
    .map((f) => {
      const sb = baseName(f.name).toLowerCase();
      let score = -1;
      if (sb === base) score = 2;
      else if (sb.startsWith(base)) score = 1;
      if (score >= 0 && extOf(f.name) === 'vtt') score += 0.5;
      return { f, score };
    })
    .filter((s) => s.score >= 0)
    .sort((a, b) => b.score - a.score);
  return scored.length ? scored[0].f : null;
}

// srtToVtt · conversión mínima SRT → WebVTT: cabecera + comas de los
// timestamps a puntos. Suficiente para subtítulos normales; los tags raros
// del SRT que el navegador no entienda simplemente se muestran tal cual.
export function srtToVtt(srt) {
  const body = (srt || '')
    .replace(/\r+/g, '')
    .replace(/(\d{2}:\d{2}:\d{2}),(\d{3})/g, '$1.$2');
  return 'WEBVTT\n\n' + body;
}

// ── Remux (ffmpeg en el NAS) ────────────────────────────────────────────────
// Los navegadores no descodifican audio Dolby/DTS (AC3/EAC3/DTS): una peli
// HEVC+EAC3 se ve pero NO SE OYE. El backend la remuxea al vuelo (vídeo
// copiado + audio→AAC). El probe nos dice qué pistas hay y decidimos aquí.

// Códecs de audio que el navegador reproduce nativo (sin remux).
const BROWSER_AUDIO_OK = new Set(['aac', 'mp3', 'opus', 'vorbis', 'flac', 'pcm_s16le', 'mp2']);
// Códecs de vídeo que el navegador descodifica con soltura. HEVC/H.265 queda
// FUERA: los navegadores no lo traen (licencias). NimOS es un VISOR, no un
// media server → esos ficheros se derivan a Jellyfin/Descargar, no se
// transcodifican (decisión de producto 2026-07-05).
const BROWSER_VIDEO_OK = new Set(['h264', 'avc', 'vp8', 'vp9', 'av1']);

// remuxUrl · vídeo intacto, solo audio→AAC (el caso peli-muda Dolby/DTS).
export function remuxUrl(share, path, t = 0, audioIdx = 0) {
  return `/api/media/stream?share=${encodeURIComponent(share)}&path=${encodeURIComponent(path)}` +
    `&t=${Math.max(0, Math.floor(t * 1000) / 1000)}&audio=${audioIdx}`;
}

export function subsTrackUrl(share, path, track = 0) {
  return `/api/media/subs?share=${encodeURIComponent(share)}&path=${encodeURIComponent(path)}&track=${track}`;
}

// probeMedia · pregunta al NAS qué lleva dentro el fichero. null si falla
// (el player sigue en modo directo, como antes de existir el probe).
export async function probeMedia(share, path, headers) {
  try {
    const r = await fetch(
      `/api/media/probe?share=${encodeURIComponent(share)}&path=${encodeURIComponent(path)}`,
      { headers },
    );
    if (!r.ok) return null;
    return await r.json();
  } catch {
    return null;
  }
}

// pickPlayback · decide el modo con los datos del probe. NimOS es VISOR:
//   · directo (streaming=false): el navegador con todo — vídeo y audio
//     amigables (≤1080p H.264 + AAC, o música). Cero CPU del NAS.
//   · remux de audio (streaming=true): vídeo amigable pero audio Dolby/DTS →
//     se copia el vídeo y se recodifica solo el audio. Coste ~0.
//   · tooHeavy=true: vídeo HEVC y/o >1080p que el navegador no maneja. NO se
//     transcodifica (eso es territorio Jellyfin) → el player muestra un aviso
//     limpio para verlo en Jellyfin o descargarlo.
export function pickPlayback(probe) {
  const none = { streaming: false, tooHeavy: false, audioTracks: [], subTracks: [], duration: 0, ffmpeg: false };
  if (!probe || !probe.ffmpeg || !Array.isArray(probe.streams)) return none;
  const audioTracks = probe.streams.filter((s) => s.type === 'audio');
  const video = probe.streams.find((s) => s.type === 'video');
  const subTracks = probe.streams.filter((s) => s.type === 'subtitle');
  const audioOK = audioTracks.length === 0 || BROWSER_AUDIO_OK.has(audioTracks[0].codec);
  // Vídeo que el navegador no puede: códec no amigable (HEVC…) o resolución
  // mayor que 1080p (4K se atasca aunque sea H.264).
  const tooHeavy = !!video && (!BROWSER_VIDEO_OK.has(video.codec) || (video.height || 0) > 1080 || (video.width || 0) > 1920);
  const streaming = !tooHeavy && !!video && !audioOK;
  return { streaming, tooHeavy, audioTracks, subTracks, duration: probe.duration || 0, ffmpeg: true };
}

// audioTrackLabel · etiqueta corta para el selector de pista ("SPA 5.1").
export function audioTrackLabel(tr) {
  const lang = (tr.lang || tr.title || `pista ${tr.index + 1}`).toUpperCase().slice(0, 12);
  const ch = tr.channels >= 6 ? '5.1' : tr.channels === 2 ? '2.0' : tr.channels ? String(tr.channels) : '';
  return ch ? `${lang} ${ch}` : lang;
}
