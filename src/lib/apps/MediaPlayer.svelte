<script>
  /**
   * MediaPlayer · reproductor nativo de NimOS
   * ──────────────────────────────────────────
   * Reproduce audio y vídeo DIRECTO desde los shares (streaming local con
   * Range vía /api/files/download, auth por cookie de sesión — sin token en
   * la URL). Sin transcoding: lo que el navegador soporte nativo.
   *
   *   · Modo VÍDEO: escenario 16:9 + subtítulos hermanos (.vtt/.srt) + PiP
   *     + pantalla completa.
   *   · Modo AUDIO: visualizador de ondas REAL (Web Audio API · AnalyserNode
   *     FFT sobre el elemento) + aleatorio/repetir.
   *   · Cola: los media hermanos de la carpeta, click para saltar,
   *     auto-avance al terminar.
   *
   * Modular (para no llorar): la lógica de tipo/URL/subs vive en
   * mediaplayer/mediaUtils.js; ondas, transporte y cola son componentes
   * stateless en mediaplayer/. Este shell solo orquesta el <video> y el
   * estado. Estado 100% local a la instancia → varias ventanas conviven.
   */
  import { onMount, onDestroy } from 'svelte';
  import { hdrs } from '$lib/stores/auth.js';
  import {
    isMediaFile, isAudioFile, streamUrl, findSubtitleFor, srtToVtt, extOf, baseName,
    probeMedia, pickPlayback, remuxUrl, subsTrackUrl,
  } from './mediaplayer/mediaUtils.js';
  import MediaWaves from './mediaplayer/MediaWaves.svelte';
  import MediaTransport from './mediaplayer/MediaTransport.svelte';
  import MediaQueue from './mediaplayer/MediaQueue.svelte';

  export let initialShare = null;
  export let initialPath = null; // ruta COMPLETA del fichero dentro del share

  // ── Estado de la instancia ──
  let share = initialShare;
  let dir = '/';
  let queue = [];
  let currentIndex = -1;
  let siblings = []; // listado completo de la carpeta (para buscar subtítulos)

  let videoEl;
  let stageEl;
  let playing = false;
  let currentTime = 0;
  let duration = 0;
  let buffered = 0;
  let volume = +(localStorage.getItem('nimos-mp-volume') ?? 1) || 1;
  let muted = false;
  let shuffle = false;
  let repeat = false;
  let showQueue = true;
  let unsupported = false;

  // Subtítulos
  let subUrl = null;   // blob URL (hermano) o URL /api/media/subs (interno)
  let subsOn = true;
  $: hasSubs = !!subUrl;

  // Web Audio (ondas): se crea UNA vez, en el primer play (política autoplay).
  let audioCtx = null;
  let analyser = null;

  // Remux (audio Dolby/DTS que el navegador no descodifica → peli muda):
  // el probe del NAS decide; en streaming el seek va por t= (recarga del src)
  // y el reloj visible es timeOffset + tiempo del elemento.
  let playback = { streaming: false, tooHeavy: false, audioTracks: [], subTracks: [], duration: 0, ffmpeg: false };
  let timeOffset = 0;
  let selectedAudio = 0;

  $: current = currentIndex >= 0 ? queue[currentIndex] : null;
  $: mode = current && isAudioFile(current.name) ? 'audio' : 'video';
  $: src = !current || playback.tooHeavy
    ? ''
    : playback.streaming
      ? remuxUrl(share, joinPath(dir, current.name), timeOffset, selectedAudio)
      : streamUrl(share, joinPath(dir, current.name));
  $: dirLabel = dir === '/' ? share : dir.split('/').filter(Boolean).pop();
  $: shownDuration = playback.streaming ? playback.duration : duration;
  $: shownCurrent = playback.streaming ? timeOffset + currentTime : currentTime;
  $: shownBuffered = playback.streaming
    ? (playback.duration > 0 ? Math.min(1, (timeOffset + buffered * (duration || 0)) / playback.duration) : 0)
    : buffered;

  function joinPath(d, name) {
    return d === '/' ? '/' + name : d + '/' + name;
  }

  // ── Carga de la carpeta y construcción de la cola ──
  async function loadFolder(fileName) {
    try {
      const r = await fetch(`/api/files?share=${share}&path=${encodeURIComponent(dir)}`, { headers: hdrs() });
      const d = await r.json();
      siblings = d.files || [];
    } catch {
      siblings = [];
    }
    queue = siblings
      .filter((f) => !f.isDirectory && isMediaFile(f.name))
      .sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }));
    let idx = queue.findIndex((f) => f.name === fileName);
    if (idx < 0 && fileName) {
      // El fichero pedido no salió en el listado (raro): reprodúcelo suelto.
      queue = [{ name: fileName }, ...queue];
      idx = 0;
    }
    currentIndex = queue.length ? Math.max(0, idx) : -1;
  }

  function dropSub() {
    if (subUrl && subUrl.startsWith('blob:')) URL.revokeObjectURL(subUrl);
    subUrl = null;
  }

  // ── Subtítulos: fichero hermano (.vtt/.srt) o, si no hay, la primera
  // pista de texto DENTRO del contenedor (vía ffmpeg → WebVTT). ──
  async function loadSubtitles(mediaName) {
    dropSub();
    const sub = findSubtitleFor(mediaName, siblings);
    if (sub) {
      try {
        const r = await fetch(streamUrl(share, joinPath(dir, sub.name)), { headers: hdrs() });
        if (!r.ok) return;
        let text = await r.text();
        if (extOf(sub.name) === 'srt') text = srtToVtt(text);
        subUrl = URL.createObjectURL(new Blob([text], { type: 'text/vtt' }));
      } catch { /* sin subs */ }
      return;
    }
    if (playback.ffmpeg && playback.subTracks.length) {
      subUrl = subsTrackUrl(share, joinPath(dir, mediaName), playback.subTracks[0].index);
    }
  }

  // ── Reproducción ──
  async function playIndex(i) {
    if (i < 0 || i >= queue.length) return;
    currentIndex = i;
    unsupported = false;
    currentTime = 0;
    duration = 0;
    buffered = 0;
    timeOffset = 0;
    selectedAudio = 0;

    // Probe del NAS: pistas y códecs. Decide directo vs remux ANTES de tocar
    // el elemento — una peli AC3 reproduce en silencio sin dar error, así que
    // la detección por error no vale.
    const name = queue[i].name;
    const probe = await probeMedia(share, joinPath(dir, name), hdrs());
    if (currentIndex !== i) return; // el usuario saltó a otra mientras tanto
    playback = pickPlayback(probe);

    // Vídeo demasiado pesado para el navegador (4K/HEVC): no lo intentamos —
    // NimOS es visor, no media server. El aviso invita a Jellyfin/Descargar.
    if (playback.tooHeavy) { playing = false; return; }

    if (mode === 'video') loadSubtitles(name);
    else dropSub();
    queueMicrotask(async () => {
      if (!videoEl) return;
      videoEl.load();
      try { await videoEl.play(); } catch { /* autoplay bloqueado: espera clic */ }
    });
  }

  // reloadAt · en streaming, recargar el src en un punto (seek / cambio de
  // pista de audio) manteniendo la reproducción.
  function reloadAt(seconds) {
    timeOffset = Math.max(0, Math.min(seconds, playback.duration || seconds));
    queueMicrotask(async () => {
      if (!videoEl) return;
      videoEl.load();
      try { await videoEl.play(); } catch { /* espera clic */ }
    });
  }

  function ensureAnalyser() {
    if (audioCtx || !videoEl) return;
    try {
      const Ctx = window.AudioContext || window.webkitAudioContext;
      audioCtx = new Ctx();
      const srcNode = audioCtx.createMediaElementSource(videoEl);
      analyser = audioCtx.createAnalyser();
      analyser.fftSize = 256;
      analyser.smoothingTimeConstant = 0.82;
      srcNode.connect(analyser);
      analyser.connect(audioCtx.destination);
    } catch { analyser = null; }
  }

  async function togglePlay() {
    if (!videoEl) return;
    if (videoEl.paused) {
      ensureAnalyser();
      if (audioCtx?.state === 'suspended') audioCtx.resume();
      try { await videoEl.play(); } catch { /* formato no soportado */ }
    } else {
      videoEl.pause();
    }
  }

  function nextIndex() {
    if (!queue.length) return -1;
    if (shuffle && queue.length > 1) {
      let i;
      do { i = Math.floor(Math.random() * queue.length); } while (i === currentIndex);
      return i;
    }
    return currentIndex + 1 < queue.length ? currentIndex + 1 : -1;
  }

  function onEnded() {
    if (repeat && videoEl) { videoEl.currentTime = 0; videoEl.play(); return; }
    const n = nextIndex();
    if (n >= 0) playIndex(n);
  }

  function onTimeUpdate() {
    if (!videoEl) return;
    currentTime = videoEl.currentTime;
    duration = videoEl.duration || 0;
    try {
      const b = videoEl.buffered;
      buffered = b.length && duration > 0 ? b.end(b.length - 1) / duration : 0;
    } catch { buffered = 0; }
  }

  function seekTo(frac) {
    if (playback.streaming) {
      if (playback.duration > 0) reloadAt(frac * playback.duration);
      return;
    }
    if (videoEl && duration > 0) videoEl.currentTime = frac * duration;
  }

  function setAudioTrack(idx) {
    if (idx === selectedAudio) return;
    selectedAudio = idx;
    if (playback.streaming) reloadAt(shownCurrent);
  }
  function setVolume(v) {
    volume = v;
    muted = false;
    if (videoEl) { videoEl.volume = v; videoEl.muted = false; }
    localStorage.setItem('nimos-mp-volume', String(v));
  }
  function toggleMute() {
    muted = !muted;
    if (videoEl) videoEl.muted = muted;
  }
  function toggleSubs() {
    subsOn = !subsOn;
    applySubsMode();
  }
  function applySubsMode() {
    if (!videoEl) return;
    for (const t of videoEl.textTracks) t.mode = subsOn ? 'showing' : 'hidden';
  }
  async function togglePip() {
    try {
      if (document.pictureInPictureElement) await document.exitPictureInPicture();
      else if (videoEl) await videoEl.requestPictureInPicture();
    } catch { /* no soportado */ }
  }
  async function toggleFullscreen() {
    try {
      if (document.fullscreenElement) await document.exitFullscreen();
      else if (stageEl) await stageEl.requestFullscreen();
    } catch { /* no soportado */ }
  }

  function onKey(e) {
    if (e.target.tagName === 'INPUT' || e.target.tagName === 'TEXTAREA' || e.target.tagName === 'SELECT') return;
    if (e.code === 'Space') { e.preventDefault(); togglePlay(); }
    else if (e.code === 'ArrowRight') {
      if (playback.streaming) reloadAt(shownCurrent + 15);
      else if (videoEl) videoEl.currentTime = Math.min(duration, currentTime + 5);
    } else if (e.code === 'ArrowLeft') {
      if (playback.streaming) reloadAt(Math.max(0, shownCurrent - 15));
      else if (videoEl) videoEl.currentTime = Math.max(0, currentTime - 5);
    }
  }

  onMount(async () => {
    if (!share || !initialPath) return;
    const parts = initialPath.split('/').filter(Boolean);
    const fileName = parts.pop() || '';
    dir = parts.length ? '/' + parts.join('/') : '/';
    await loadFolder(fileName);
    if (currentIndex >= 0) playIndex(currentIndex);
  });

  // onMediaError · el elemento falló. Si íbamos en directo y hay ffmpeg,
  // reintenta por remux de audio (cubre el mkv con contenedor raro pero vídeo
  // amigable). Si aun así falla, no es reproducible en el navegador.
  function onMediaError() {
    if (!src) return;
    playing = false;
    if (!playback.streaming && playback.ffmpeg && mode === 'video') {
      playback = { ...playback, streaming: true };
      reloadAt(0);
      return;
    }
    unsupported = true;
  }

  onDestroy(() => {
    dropSub();
    if (audioCtx) audioCtx.close().catch(() => {});
  });
</script>

<!-- svelte-ignore a11y_no_noninteractive_tabindex a11y_no_static_element_interactions -->
<div class="mp" tabindex="0" on:keydown={onKey}>
  {#if !share || !initialPath}
    <div class="mp-empty">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><rect x="2" y="4" width="20" height="16" rx="2"/><polygon points="10 9 15 12 10 15 10 9" fill="currentColor" stroke="none"/></svg>
      <div>Abre un vídeo o una canción desde Files</div>
      <div class="mp-empty-hint">clic derecho → Reproducir</div>
    </div>
  {:else}
    <div class="mp-crumb">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg>
      <span>{share}</span>
      {#if dir !== '/'}<span class="sep">/</span><span>{dirLabel}</span>{/if}
      <span class="sep">/</span>
      <span class="cur">{current?.name || '—'}</span>
    </div>

    <div class="mp-body" class:with-queue={showQueue}>
      <div class="mp-main">
        <div class="stage" bind:this={stageEl}>
          <!-- Un ÚNICO elemento <video> para ambos modos: en audio queda oculto
               tras las ondas (el elemento reproduce audio igual). Así la Web
               Audio se engancha una sola vez. -->
          <!-- svelte-ignore a11y_media_has_caption -->
          <video
            bind:this={videoEl}
            {src}
            class:hidden={mode === 'audio'}
            crossorigin="use-credentials"
            preload="metadata"
            on:play={() => { playing = true; applySubsMode(); }}
            on:pause={() => (playing = false)}
            on:ended={onEnded}
            on:timeupdate={onTimeUpdate}
            on:durationchange={onTimeUpdate}
            on:progress={onTimeUpdate}
            on:loadedmetadata={() => { if (videoEl) { videoEl.volume = volume; videoEl.muted = muted; } }}
            on:error={onMediaError}
            on:click={togglePlay}
          >
            {#if subUrl && mode === 'video'}
              <track kind="subtitles" src={subUrl} srclang="es" label="Subtítulos" default />
            {/if}
          </video>

          {#if mode === 'audio'}
            <MediaWaves {analyser} {playing} />
            <div class="audio-meta">
              <span class="disc">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="2.4" fill="currentColor" stroke="none"/></svg>
              </span>
              <div>
                <div class="t1">{baseName(current?.name || '')}</div>
                <div class="t2">{dirLabel}</div>
              </div>
            </div>
          {/if}

          {#if playback.tooHeavy}
            <div class="oops">
              <svg class="oops-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"><rect x="2" y="4" width="20" height="16" rx="2"/><path d="m10 9 5 3-5 3z" fill="currentColor" stroke="none"/></svg>
              <div class="oops-t">Vídeo 4K / HEVC — demasiado para el navegador</div>
              <div class="oops-s">NimOS es un visor rápido, no un centro multimedia. Para verlo fluido, ábrelo en Jellyfin (App Store) o descárgalo y reprodúcelo en tu equipo.</div>
            </div>
          {:else if unsupported}
            <div class="oops">
              <div class="oops-t">El navegador no puede reproducir este formato</div>
              <div class="oops-s">{current?.name} · usa Descargar en Files para verlo en tu equipo</div>
            </div>
          {/if}

          {#if !playing && !unsupported && !playback.tooHeavy && current}
            <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
            <div class="bigplay" on:click={togglePlay}>
              <svg viewBox="0 0 24 24" fill="currentColor"><polygon points="8 5 19 12 8 19 8 5"/></svg>
            </div>
          {/if}
        </div>

        <MediaTransport
          {mode} {playing}
          currentTime={shownCurrent} duration={shownDuration} buffered={shownBuffered}
          {volume} {muted} {shuffle} {repeat} {hasSubs} {subsOn}
          audioTracks={playback.streaming ? playback.audioTracks : []}
          {selectedAudio}
          canPrev={currentIndex > 0}
          canNext={currentIndex >= 0 && currentIndex < queue.length - 1}
          on:playpause={togglePlay}
          on:seek={(e) => seekTo(e.detail)}
          on:audiotrack={(e) => setAudioTrack(e.detail)}
          on:prev={() => playIndex(currentIndex - 1)}
          on:next={() => playIndex(currentIndex + 1)}
          on:volume={(e) => setVolume(e.detail)}
          on:togglemute={toggleMute}
          on:toggleshuffle={() => (shuffle = !shuffle)}
          on:togglerepeat={() => (repeat = !repeat)}
          on:togglesubs={toggleSubs}
          on:pip={togglePip}
          on:fullscreen={toggleFullscreen}
          on:togglequeue={() => (showQueue = !showQueue)}
        />
      </div>

      {#if showQueue}
        <div class="mp-side">
          <MediaQueue {queue} {currentIndex} on:select={(e) => playIndex(e.detail)} />
        </div>
      {/if}
    </div>
  {/if}
</div>

<style>
  .mp {
    display: flex; flex-direction: column; height: 100%;
    background: var(--bg-main, #26262f); color: var(--fg, #f2f2f5);
    outline: none;
  }
  .mp-crumb {
    display: flex; align-items: center; gap: 6px; flex-shrink: 0;
    padding: 8px 14px; font-family: var(--font-mono); font-size: 11px;
    color: var(--fg-4, #7a7a82); border-bottom: 1px solid var(--bd-2, #20202a);
    white-space: nowrap; overflow: hidden;
  }
  .mp-crumb svg { width: 13px; height: 13px; flex-shrink: 0; }
  .mp-crumb .sep { color: var(--fg-5, #4a4a52); }
  .mp-crumb .cur { color: var(--fg-2, #c8c8cf); overflow: hidden; text-overflow: ellipsis; }

  .mp-body { display: grid; grid-template-columns: 1fr; flex: 1; min-height: 0; }
  .mp-body.with-queue { grid-template-columns: 1fr 200px; }
  .mp-main { display: flex; flex-direction: column; min-width: 0; min-height: 0; }
  .mp-side {
    border-left: 1px solid var(--bd-2, #20202a);
    background: var(--bg-side, #1c1c22); min-height: 0;
  }

  .stage {
    position: relative; flex: 1; min-height: 0; margin: 14px 14px 0;
    background: #0a0a0d; border: 1px solid var(--bd-2, #20202a);
    border-radius: 10px; overflow: hidden;
  }
  .stage video {
    position: absolute; inset: 0; width: 100%; height: 100%;
    object-fit: contain; background: #0a0a0d;
  }
  .stage video.hidden { visibility: hidden; }

  .audio-meta {
    position: absolute; bottom: 12px; left: 14px;
    display: flex; align-items: center; gap: 10px; pointer-events: none;
  }
  .audio-meta .disc {
    width: 40px; height: 40px; border-radius: 7px;
    background: rgba(127, 119, 221, 0.15); color: var(--accent-lav, #7f77dd);
    display: flex; align-items: center; justify-content: center;
  }
  .audio-meta .disc svg { width: 20px; height: 20px; }
  .audio-meta .t1 { font-size: 13px; color: var(--fg, #f2f2f5); }
  .audio-meta .t2 { font-size: 11px; color: var(--fg-3, #9a9aa3); font-family: var(--font-mono); }

  .bigplay {
    position: absolute; inset: 0; display: flex; align-items: center;
    justify-content: center; cursor: pointer;
  }
  .bigplay svg {
    width: 26px; height: 26px; color: var(--fg, #f2f2f5);
    background: rgba(0, 0, 0, 0.45); border: 1px solid rgba(255, 255, 255, 0.15);
    border-radius: 50%; padding: 15px;
  }
  .oops {
    position: absolute; inset: 0; display: flex; flex-direction: column;
    align-items: center; justify-content: center; gap: 6px; padding: 20px;
    text-align: center;
  }
  .oops-t { font-size: 13px; color: var(--fg, #f2f2f5); }
  .oops-s { font-size: 11px; color: var(--fg-4, #7a7a82); font-family: var(--font-mono); line-height: 1.6; max-width: 380px; }
  .oops-ico { width: 34px; height: 34px; color: var(--accent-lav, #7f77dd); opacity: 0.8; margin-bottom: 2px; }

  .mp-empty {
    flex: 1; display: flex; flex-direction: column; align-items: center;
    justify-content: center; gap: 10px; color: var(--fg-4, #7a7a82);
    font-size: 13px;
  }
  .mp-empty svg { width: 42px; height: 42px; opacity: 0.5; }
  .mp-empty-hint { font-family: var(--font-mono); font-size: 11px; color: var(--fg-5, #5a5a62); }
</style>
