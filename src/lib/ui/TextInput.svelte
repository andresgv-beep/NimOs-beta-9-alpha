<script>
  /**
   * TextInput · Input de texto con bevel sm
   * ─────────────────────────────────────────
   * Usar para formularios, búsquedas, campos en general.
   *
   * Props:
   *   - value (bindable)
   *   - placeholder
   *   - type       → 'text' | 'password' | 'email' | 'number' | 'search'
   *   - icon       → string (símbolo Unicode al principio)
   *   - keyHint    → atajo de teclado al final (ej. "/", "⌘K")
   *   - size       → 'sm' | 'md' (default)
   *   - disabled
   *   - onInput    → handler
   */
  import KeyBind from './KeyBind.svelte';

  export let value = '';
  export let placeholder = '';
  export let type = 'text';
  export let icon = '';
  export let keyHint = '';
  export let size = 'md';
  export let disabled = false;
  export let onInput = null;
  export let onKeydown = null;

  function handleInput(e) {
    value = e.target.value;
    if (onInput) onInput(e);
  }
</script>

<div class="wrap" class:sm={size === 'sm'} class:disabled>
  {#if icon}<span class="icon">{icon}</span>{/if}
  <input
    {type}
    {placeholder}
    {disabled}
    {value}
    on:input={handleInput}
    on:keydown={onKeydown}
  />
  {#if keyHint}<KeyBind key={keyHint} />{/if}
</div>

<style>
  .wrap {
    display: flex;
    align-items: center;
    gap: 8px;
    height: 34px;
    padding: 0 11px;
    border: 1px solid var(--line-bright);
    border-radius: 4px;
    background: var(--canvas-soft);
    transition: border-color 0.12s, box-shadow 0.12s;
  }
  .wrap.sm { height: 24px; padding: 0 8px; }
  .wrap:focus-within { border-color: var(--signal); box-shadow: 0 0 0 2px var(--signal-soft); }
  .wrap.disabled { opacity: 0.5; cursor: not-allowed; }

  .icon {
    color: var(--ink-mute);
    font-size: 13px;
    flex-shrink: 0;
  }
  input {
    flex: 1;
    min-width: 0;
    background: transparent;
    border: none;
    outline: none;
    color: var(--ink);
    font-family: var(--font-sans);
    font-size: 13px;
    letter-spacing: 0;
  }
  .wrap.sm input { font-size: 10px; }
  input::placeholder { color: var(--ink-faint); }
</style>
