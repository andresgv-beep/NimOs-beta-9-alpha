<script>
  /**
   * MediaQueue · cola de reproducción (los media hermanos de la carpeta)
   * ─────────────────────────────────────────────────────────────────────
   * Stateless: lista + índice actual por props, emite `select` con el índice.
   */
  import { createEventDispatcher } from 'svelte';
  import { isAudioFile } from './mediaUtils.js';

  const dispatch = createEventDispatcher();

  export let queue = [];
  export let currentIndex = 0;
</script>

<div class="q">
  <div class="q-head">EN COLA · CARPETA</div>
  {#each queue as item, i (item.name)}
    <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
    <div class="q-row" class:active={i === currentIndex} on:click={() => dispatch('select', i)}>
      <span class="q-ico" class:audio={isAudioFile(item.name)}>
        {#if i === currentIndex}
          <svg viewBox="0 0 24 24" fill="currentColor"><polygon points="6 3 20 12 6 21 6 3"/></svg>
        {:else if isAudioFile(item.name)}
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M9 18V5l12-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/></svg>
        {:else}
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><rect x="2" y="4" width="20" height="16" rx="2"/><polygon points="10 9 15 12 10 15 10 9" fill="currentColor" stroke="none"/></svg>
        {/if}
      </span>
      <div class="q-name">{item.name}</div>
    </div>
  {/each}
  {#if !queue.length}
    <div class="q-empty">Sin más media en esta carpeta</div>
  {/if}
</div>

<style>
  .q { padding: 12px 10px; overflow-y: auto; height: 100%; }
  .q-head {
    font-family: var(--font-mono); font-size: 10px; letter-spacing: 1px;
    color: var(--fg-4, #7a7a82); padding: 0 6px 8px;
  }
  .q-row {
    display: flex; align-items: center; gap: 10px;
    padding: 8px 10px; border-radius: 8px; cursor: pointer;
  }
  .q-row:hover { background: var(--bg-inner, #20202a); }
  .q-row.active {
    background: rgba(0, 255, 159, 0.07);
    border-left: 2px solid var(--nim-green, #00ff9f);
    border-radius: 0 8px 8px 0;
  }
  .q-ico {
    width: 30px; height: 30px; border-radius: 6px; flex-shrink: 0;
    background: var(--bg-inner, #20202a);
    display: flex; align-items: center; justify-content: center;
    color: var(--fg-3, #9a9aa3);
  }
  .q-ico.audio { color: var(--accent-lav, #7f77dd); }
  .q-row.active .q-ico { color: var(--nim-green, #00ff9f); }
  .q-ico svg { width: 14px; height: 14px; }
  .q-name {
    min-width: 0; font-size: 11px; color: var(--fg-2, #c8c8cf);
    white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  }
  .q-row.active .q-name { color: var(--fg, #f2f2f5); }
  .q-empty { padding: 12px 8px; font-size: 11px; color: var(--fg-4, #7a7a82); font-family: var(--font-mono); }
</style>
