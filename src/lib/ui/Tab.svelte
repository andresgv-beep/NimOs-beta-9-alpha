<script>
  /**
   * Tab · Pestaña individual
   * ─────────────────────────
   * Uso agrupado dentro de un .tabs container:
   *   <div class="tabs">
   *     <Tab active onClick={...}>Live <Badge>142</Badge> <KeyBind key="1" /></Tab>
   *     <Tab onClick={...}>Históricos</Tab>
   *   </div>
   *
   * El slot es flexible; en la mayoría de casos ponemos texto, badge opcional
   * y keybind opcional.
   */
  export let active = false;
  export let onClick = null;
  export let hasError = false;
  export let hasWarn = false;
  export let disabled = false;

  function handleClick(e) {
    if (disabled) return;
    if (onClick) onClick(e);
  }
</script>

<button
  class="tab"
  class:active
  class:has-error={hasError}
  class:has-warn={hasWarn}
  {disabled}
  on:click={handleClick}
>
  <slot />
</button>

<style>
  .tab {
    font-family: var(--font-mono);
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 1.2px;
    text-transform: uppercase;
    padding: 11px 18px 9px;
    background: transparent;
    border: none;
    color: var(--fg-dim);
    cursor: pointer;
    transition: all 0.12s;
    display: flex;
    align-items: center;
    gap: 8px;
    border-bottom: 2px solid transparent;
  }
  .tab:hover { color: var(--ink); background: var(--bg-1); }
  .tab.active {
    color: var(--accent);
    background: var(--bg-1);
    border-bottom-color: var(--accent);
    padding-bottom: 7px;
  }
  .tab.has-error:not(.active) { color: var(--crit); }
  .tab.has-warn:not(.active)  { color: var(--warn); }
  .tab[disabled] { opacity: 0.4; cursor: not-allowed; }
</style>
