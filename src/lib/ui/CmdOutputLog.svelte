<script>
  /**
   * CmdOutputLog · Viewer de logs estilo journalctl
   * ──────────────────────────────────────────────────
   * Uso:
   *   <CmdOutputLog
   *     lines={[
   *       { ts: '2026-04-19 14:22:03', level: 'info', msg: 'Daemon started' },
   *       { ts: '2026-04-19 14:22:04', level: 'warn', msg: 'Config missing key foo' },
   *       { ts: '2026-04-19 14:22:05', level: 'err',  msg: 'Connection refused' },
   *     ]}
   *     follow={true}
   *   />
   *
   * Props:
   *   - lines:  [{ ts, level, msg }] — level: 'info' | 'warn' | 'err' | 'ok'
   *   - follow: boolean — auto-scroll al final cuando llegan líneas nuevas
   *   - height: altura en px (default: flex auto)
   */
  import { afterUpdate } from 'svelte';

  export let lines = [];
  export let follow = true;
  export let height = null;

  let scrollEl;
  let lastLen = 0;

  afterUpdate(() => {
    if (follow && scrollEl && lines.length !== lastLen) {
      scrollEl.scrollTop = scrollEl.scrollHeight;
      lastLen = lines.length;
    }
  });

  /**
   * Formatea mensajes estilo key=value resaltando keys en gris mute.
   * 'Starting up error=nil' → 'Starting up <span class="k">error=</span>nil'
   */
  function formatMsg(msg) {
    if (!msg) return '';
    return String(msg)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/([a-z_]+)=/g, '<span class="k">$1=</span>');
  }
</script>

<div class="cmd" style={height ? `height:${height}px` : ''}>
  <div class="body" bind:this={scrollEl}>
    {#each lines as line}
      <div class="line">
        <span class="ts">{line.ts}</span>
        <span class="lvl {line.level || 'info'}">{line.level || 'info'}</span>
        <span class="msg">{@html formatMsg(line.msg)}</span>
      </div>
    {/each}
    {#if lines.length === 0}
      <div class="empty">─── log vacío ───</div>
    {/if}
    {#if follow}<span class="cursor"></span>{/if}
  </div>
</div>

<style>
  .cmd {
    display: flex;
    flex-direction: column;
    background: var(--bg);
    border: 1px solid var(--border);
    font-family: var(--font-mono);
  }
  .body {
    flex: 1;
    overflow-y: auto;
    padding: 8px 12px;
    font-size: 10px;
    color: var(--fg-dim);
    line-height: 1.6;
  }
  .body::-webkit-scrollbar { width: 6px; }
  .body::-webkit-scrollbar-thumb { background: var(--border-bright); }

  .line {
    display: grid;
    grid-template-columns: 150px 50px 1fr;
    gap: 10px;
    padding: 1px 0;
    white-space: nowrap;
  }
  .line:hover { background: var(--bg-1); }

  .ts {
    color: var(--fg-faint);
    font-size: 9.5px;
  }
  .lvl {
    font-size: 9px;
    letter-spacing: 1px;
    text-transform: uppercase;
    text-align: center;
    font-weight: 600;
  }
  .lvl.info { color: var(--info); }
  .lvl.warn { color: var(--warn); }
  .lvl.err  { color: var(--crit); }
  .lvl.ok   { color: var(--accent); }

  .msg {
    color: var(--fg-dim);
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .msg :global(.k) { color: var(--fg-mute); }

  .empty {
    text-align: center;
    color: var(--fg-faint);
    font-size: 10px;
    padding: 20px;
    letter-spacing: 2px;
  }

  .cursor {
    display: inline-block;
    width: 7px; height: 11px;
    background: var(--accent);
    animation: cursor-blink 1s steps(2) infinite;
    margin-left: 2px;
    vertical-align: text-bottom;
  }
</style>
