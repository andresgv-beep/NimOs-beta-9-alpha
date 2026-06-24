<script>
  /**
   * ConfigField.svelte — Renderiza UN campo de configuración según su `type`.
   *
   * Ladrillo reutilizable del modal de config (ConfigModal). Soporta los tipos
   * del contrato (APP-CATALOG-SCHEMA.md): text, password, number, toggle, select.
   *
   * Toda la lógica (validación, auto, etc.) vive en configSchema.js · este
   * componente SOLO pinta el campo y emite el valor. Modular y fino.
   *
   * Props:
   *   field  · el descriptor del campo (key, label, type, required, hint,
   *            mono, placeholder, options, secret, immutable...)
   *   value  · valor actual (bind)
   *   error  · mensaje de error a mostrar ('' si no hay)
   *   readonly · si el campo es solo-lectura (immutable tras instalar)
   */
  export let field = {};
  export let value = '';
  export let error = '';
  export let readonly = false;

  // type por defecto: text
  $: type = field.type || 'text';
  $: isMono = !!field.mono;
</script>

<div class="cfg-field">
  <label class="cfg-label" for={`cfg-${field.key}`}>
    {field.label || field.key}
    {#if field.required}<span class="req">*</span>{/if}
    {#if !field.required}<span class="opt">· opcional</span>{/if}
  </label>

  {#if type === 'toggle'}
    <button
      type="button"
      class="cfg-toggle"
      class:on={value === true || value === 'true'}
      disabled={readonly}
      on:click={() => (value = !(value === true || value === 'true'))}
      id={`cfg-${field.key}`}
    >
      <span class="knob"></span>
      <span class="toggle-txt">{(value === true || value === 'true') ? 'Sí' : 'No'}</span>
    </button>

  {:else if type === 'select'}
    <select
      class="cfg-input"
      bind:value
      disabled={readonly}
      id={`cfg-${field.key}`}
    >
      {#each (field.options || []) as opt}
        <option value={opt}>{opt}</option>
      {/each}
    </select>

  {:else if type === 'number'}
    <input
      class="cfg-input"
      class:mono={isMono}
      type="number"
      bind:value
      readonly={readonly}
      placeholder={field.placeholder || ''}
      id={`cfg-${field.key}`}
    />

  {:else if type === 'password'}
    <input
      class="cfg-input"
      class:mono={isMono}
      type="password"
      bind:value
      readonly={readonly}
      placeholder={field.placeholder || '••••••••'}
      autocomplete="new-password"
      id={`cfg-${field.key}`}
    />

  {:else}
    <!-- text (default) -->
    <input
      class="cfg-input"
      class:mono={isMono}
      type="text"
      bind:value
      readonly={readonly}
      placeholder={field.placeholder || ''}
      id={`cfg-${field.key}`}
    />
  {/if}

  {#if error}
    <div class="cfg-msg err">{error}</div>
  {:else if field.hint}
    <div class="cfg-msg hint">{field.hint}</div>
  {/if}
</div>

<style>
  .cfg-field { display: flex; flex-direction: column; gap: 6px; }
  .cfg-label {
    font-size: 11px; color: var(--fg-3); font-weight: 600;
    letter-spacing: 0.3px; display: flex; align-items: center; gap: 6px;
  }
  .cfg-label .req { color: var(--nim-green); }
  .cfg-label .opt {
    color: var(--fg-5); font-weight: 400; font-size: 10px;
    text-transform: none; letter-spacing: 0;
  }
  .cfg-input {
    width: 100%; padding: 9px 12px; border-radius: 7px;
    background: var(--bg-inner); border: 1px solid var(--bd-3);
    color: var(--fg); font-size: 13px; font-family: var(--font-sans);
    transition: border-color 0.12s;
  }
  .cfg-input.mono { font-family: var(--font-mono); }
  .cfg-input::placeholder { color: var(--fg-5); }
  .cfg-input:focus { border-color: var(--nim-green); outline: none; }
  .cfg-input:read-only { opacity: 0.6; cursor: not-allowed; }
  .cfg-input:disabled { opacity: 0.6; cursor: not-allowed; }

  .cfg-toggle {
    display: flex; align-items: center; gap: 8px;
    background: var(--bg-inner); border: 1px solid var(--bd-3);
    border-radius: 7px; padding: 7px 12px; cursor: pointer;
    font-family: var(--font-sans); color: var(--fg-3); font-size: 13px;
    width: fit-content;
  }
  .cfg-toggle .knob {
    width: 16px; height: 16px; border-radius: 50%;
    background: var(--fg-5); transition: background 0.12s;
  }
  .cfg-toggle.on { color: var(--fg); border-color: var(--nim-green); }
  .cfg-toggle.on .knob { background: var(--nim-green); }
  .cfg-toggle:disabled { opacity: 0.6; cursor: not-allowed; }

  .cfg-msg { font-size: 10.5px; line-height: 1.45; }
  .cfg-msg.hint { color: var(--fg-4); }
  .cfg-msg.err { color: var(--st-crit); }
</style>
