<script>
  /**
   * MediaTransport · barra de transporte del MediaPlayer
   * ─────────────────────────────────────────────────────
   * Stateless: recibe el estado de reproducción por props y emite eventos;
   * el padre (MediaPlayer) los aplica sobre el elemento <video>. Mismo patrón
   * que FilesContextMenu.
   *
   * En modo vídeo muestra CC/PiP/pantalla completa; en audio, aleatorio y
   * repetir. El scrubber emite `seek` con la fracción [0,1] clicada.
   */
  import { createEventDispatcher } from 'svelte';
  import { fmtTime, audioTrackLabel } from './mediaUtils.js';

  const dispatch = createEventDispatcher();

  export let mode = 'video'; // 'video' | 'audio'
  export let playing = false;
  export let currentTime = 0;
  export let duration = 0;
  export let buffered = 0; // fracción [0,1]
  export let volume = 1;
  export let muted = false;
  export let shuffle = false;
  export let repeat = false;
  export let hasSubs = false;
  export let subsOn = false;
  export let canPrev = false;
  export let canNext = false;
  // Pistas de audio del contenedor (solo en modo remux; el navegador no
  // permite cambiar de pista en reproducción directa).
  export let audioTracks = [];
  export let selectedAudio = 0;

  $: playedFrac = duration > 0 ? Math.min(1, currentTime / duration) : 0;

  function seekAt(e) {
    const r = e.currentTarget.getBoundingClientRect();
    const f = Math.max(0, Math.min(1, (e.clientX - r.left) / r.width));
    dispatch('seek', f);
  }
  function volAt(e) {
    const r = e.currentTarget.getBoundingClientRect();
    const v = Math.max(0, Math.min(1, (e.clientX - r.left) / r.width));
    dispatch('volume', v);
  }
</script>

<div class="tp">
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
  <div class="scrub" on:click={seekAt} title="Ir a">
    <div class="buf" style="width:{Math.round(buffered * 100)}%"></div>
    <div class="played" style="width:{playedFrac * 100}%"></div>
    <div class="knob" style="left:{playedFrac * 100}%"></div>
  </div>
  <div class="times">
    <span class="cur">{fmtTime(currentTime)}</span>
    <span>-{fmtTime(Math.max(0, duration - currentTime))}</span>
  </div>

  <div class="row">
    <div class="grp">
      {#if mode === 'audio'}
        <button class="tb" class:accent={shuffle} title="Aleatorio" on:click={() => dispatch('toggleshuffle')}>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><polyline points="16 3 21 3 21 8"/><line x1="4" y1="20" x2="21" y2="3"/><polyline points="21 16 21 21 16 21"/><line x1="15" y1="15" x2="21" y2="21"/><line x1="4" y1="4" x2="9" y2="9"/></svg>
        </button>
      {/if}
      <button class="tb" title="Anterior" disabled={!canPrev} on:click={() => dispatch('prev')}>
        <svg viewBox="0 0 24 24" fill="currentColor"><polygon points="19 20 9 12 19 4 19 20"/><line x1="5" y1="19" x2="5" y2="5" stroke="currentColor" stroke-width="2"/></svg>
      </button>
      <button class="tb play" title={playing ? 'Pausar' : 'Reproducir'} on:click={() => dispatch('playpause')}>
        {#if playing}
          <svg viewBox="0 0 24 24" fill="currentColor"><rect x="6" y="4" width="4" height="16"/><rect x="14" y="4" width="4" height="16"/></svg>
        {:else}
          <svg viewBox="0 0 24 24" fill="currentColor"><polygon points="6 3 20 12 6 21 6 3"/></svg>
        {/if}
      </button>
      <button class="tb" title="Siguiente" disabled={!canNext} on:click={() => dispatch('next')}>
        <svg viewBox="0 0 24 24" fill="currentColor"><polygon points="5 4 15 12 5 20 5 4"/><line x1="19" y1="5" x2="19" y2="19" stroke="currentColor" stroke-width="2"/></svg>
      </button>
      {#if mode === 'audio'}
        <button class="tb" class:accent={repeat} title="Repetir" on:click={() => dispatch('togglerepeat')}>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><polyline points="17 1 21 5 17 9"/><path d="M3 11V9a4 4 0 0 1 4-4h14"/><polyline points="7 23 3 19 7 15"/><path d="M21 13v2a4 4 0 0 1-4 4H3"/></svg>
        </button>
      {/if}
      <div class="vol">
        <button class="tb slim" title={muted ? 'Activar sonido' : 'Silenciar'} on:click={() => dispatch('togglemute')}>
          {#if muted || volume === 0}
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5" fill="currentColor" stroke="none"/><line x1="23" y1="9" x2="17" y2="15"/><line x1="17" y1="9" x2="23" y2="15"/></svg>
          {:else}
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5" fill="currentColor" stroke="none"/><path d="M15.5 8.5a5 5 0 0 1 0 7"/><path d="M18.5 5.5a9 9 0 0 1 0 13"/></svg>
          {/if}
        </button>
        <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
        <div class="volbar" on:click={volAt} title="Volumen">
          <div class="volfill" style="width:{muted ? 0 : Math.round(volume * 100)}%"></div>
        </div>
      </div>
    </div>

    <div class="grp">
      {#if mode === 'video' && audioTracks.length > 1}
        <select
          class="atrack"
          title="Pista de audio"
          value={selectedAudio}
          on:change={(e) => dispatch('audiotrack', +e.currentTarget.value)}
        >
          {#each audioTracks as tr (tr.index)}
            <option value={tr.index}>{audioTrackLabel(tr)}</option>
          {/each}
        </select>
      {/if}
      {#if mode === 'video'}
        {#if hasSubs}
          <button class="tb wide" class:accent={subsOn} title="Subtítulos" on:click={() => dispatch('togglesubs')}>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><rect x="2" y="5" width="20" height="14" rx="2"/><path d="M6 13h4M6 16h2M14 13h4M12 16h6"/></svg>
            CC
          </button>
        {/if}
        <button class="tb" title="Imagen sobre imagen" on:click={() => dispatch('pip')}>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><rect x="2" y="4" width="20" height="16" rx="2"/><rect x="12" y="12" width="7" height="5" rx="1" fill="currentColor" stroke="none"/></svg>
        </button>
        <button class="tb" title="Pantalla completa" on:click={() => dispatch('fullscreen')}>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M8 3H5a2 2 0 0 0-2 2v3M16 3h3a2 2 0 0 1 2 2v3M8 21H5a2 2 0 0 1-2-2v-3M16 21h3a2 2 0 0 0 2-2v-3"/></svg>
        </button>
      {/if}
      <button class="tb" class:accent={false} title="Cola" on:click={() => dispatch('togglequeue')}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><line x1="8" y1="6" x2="21" y2="6"/><line x1="8" y1="12" x2="21" y2="12"/><line x1="8" y1="18" x2="21" y2="18"/><line x1="3" y1="6" x2="3.01" y2="6"/><line x1="3" y1="12" x2="3.01" y2="12"/><line x1="3" y1="18" x2="3.01" y2="18"/></svg>
      </button>
    </div>
  </div>
</div>

<style>
  .tp { padding: 12px 14px 10px; }
  .scrub { position: relative; height: 6px; border-radius: 3px; background: var(--bd-2, #2a2a32); cursor: pointer; }
  .buf { position: absolute; left: 0; top: 0; height: 100%; border-radius: 3px; background: var(--bd-3, #3a3a44); }
  .played { position: absolute; left: 0; top: 0; height: 100%; border-radius: 3px; background: var(--nim-green, #00ff9f); }
  .knob { position: absolute; top: 50%; width: 13px; height: 13px; border-radius: 50%; background: var(--nim-green, #00ff9f); transform: translate(-50%, -50%); }
  .times { display: flex; justify-content: space-between; margin-top: 6px; font-family: var(--font-mono); font-size: 11px; color: var(--fg-4, #7a7a82); }
  .times .cur { color: var(--fg-2, #c8c8cf); }
  .row { display: flex; align-items: center; justify-content: space-between; margin-top: 8px; gap: 8px; flex-wrap: wrap; }
  .grp { display: flex; align-items: center; gap: 8px; }
  .tb {
    width: 34px; height: 34px; border-radius: 8px;
    border: 1px solid var(--bd-2, #2a2a32); background: transparent;
    color: var(--fg-2, #c8c8cf); cursor: pointer;
    display: flex; align-items: center; justify-content: center;
  }
  .tb svg { width: 16px; height: 16px; }
  .tb:hover:not(:disabled) { border-color: var(--bd-3, #3a3a44); color: var(--fg, #f2f2f5); }
  .tb:disabled { opacity: 0.35; cursor: default; }
  .tb.play {
    width: 44px; height: 44px; border-radius: 50%;
    background: rgba(0, 255, 159, 0.12);
    border-color: var(--nim-green, #00ff9f); color: var(--nim-green, #00ff9f);
  }
  .tb.play svg { width: 19px; height: 19px; }
  .tb.wide { width: auto; padding: 0 10px; gap: 5px; font-family: var(--font-mono); font-size: 11px; }
  .tb.slim { border: none; width: 28px; }
  .tb.accent { color: var(--nim-green, #00ff9f); border-color: rgba(0, 255, 159, 0.35); }
  .vol { display: flex; align-items: center; gap: 4px; margin-left: 4px; }
  .volbar { width: 60px; height: 5px; border-radius: 3px; background: var(--bd-2, #2a2a32); position: relative; cursor: pointer; }
  .volfill { position: absolute; left: 0; top: 0; height: 100%; border-radius: 3px; background: var(--fg-2, #c8c8cf); }
  .atrack {
    height: 34px; border-radius: 8px; padding: 0 8px;
    border: 1px solid var(--bd-2, #2a2a32); background: transparent;
    color: var(--fg-2, #c8c8cf); font-family: var(--font-mono); font-size: 11px;
    cursor: pointer; outline: none;
  }
  .atrack:hover { border-color: var(--bd-3, #3a3a44); }
  .atrack option { background: var(--bg-inner, #16161c); color: var(--fg-2, #c8c8cf); }
</style>
