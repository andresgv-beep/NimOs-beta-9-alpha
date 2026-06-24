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
    height: 28px;
    padding: 0 10px;
    border: 1px solid var(--border);
    background: var(--bg-1);
    clip-path: polygon(
      0 0,
      calc(100% - var(--bev-sm)) 0,
      100% var(--bev-sm),
      100% 100%,
      var(--bev-sm) 100%,
      0 calc(100% - var(--bev-sm))
    );
    transition: border-color 0.12s;
  }
  .wrap.sm { height: 24px; padding: 0 8px; }
  .wrap:focus-within { border-color: var(--accent); }
  .wrap.disabled { opacity: 0.5; cursor: not-allowed; }

  .icon {
    color: var(--fg-mute);
    font-size: 11px;
    flex-shrink: 0;
  }
  input {
    flex: 1;
    min-width: 0;
    background: transparent;
    border: none;
    outline: none;
    color: var(--ink);
    font-family: var(--font-mono);
    font-size: 11px;
    letter-spacing: 0.5px;
  }
  .wrap.sm input { font-size: 10px; }
  input::placeholder { color: var(--fg-mute); }
</style>
