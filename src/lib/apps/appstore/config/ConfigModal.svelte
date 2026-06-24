<script>
  /**
   * ConfigModal.svelte — Modal de configuración previo a la instalación.
   *
   * Se construye DINÁMICAMENTE desde el catálogo de la app (genérico · sirve
   * para Matrix, VSCode, etc.). Lee configFields + postInstall.fields, los
   * pinta con ConfigField, valida, y al confirmar emite los valores separados
   * por destino (env / postInstall) para que InstallFlow los mande al backend.
   *
   * Toda la lógica vive en configSchema.js / autoProviders.js (puro, testeado).
   * Este componente solo orquesta UI · modular y fino.
   *
   * Props:
   *   catalog · entrada de catálogo de la app (con configFields/postInstall)
   *   appName · nombre de la app (para el título y el auto:domain)
   *   appIcon · icono (opcional)
   *   ctx     · contexto para resolver auto (baseDomain, httpsPort, localIp...)
   *
   * Eventos:
   *   confirm · { env: {...}, postInstall: {...} } · el usuario pulsó Instalar
   *   cancel  · el usuario canceló
   */
  import { createEventDispatcher } from 'svelte';
  import ConfigField from './ConfigField.svelte';
  import {
    collectFields,
    initialValues,
    validateAll,
    splitValuesByDestination,
  } from './configSchema.js';

  export let catalog = {};
  export let appName = '';
  export let appIcon = '';
  export let ctx = {};

  const dispatch = createEventDispatcher();

  // Campos a mostrar (configFields + postInstall.fields), con su origen marcado.
  $: fields = collectFields(catalog);

  // Valores: se inicializan una vez con defaults + auto resueltos.
  let values = {};
  let initialized = false;
  $: if (!initialized && fields.length > 0) {
    values = initialValues(fields, { ...ctx, appName });
    initialized = true;
  }

  let errors = {};

  // ¿Hay campos de cada tipo? (para separar visualmente Config / Administrador)
  $: envFields = fields.filter((f) => f._kind === 'env');
  $: postFields = fields.filter((f) => f._kind === 'postInstall');

  function onInstall() {
    const res = validateAll(fields, values);
    errors = res.errors;
    if (!res.ok) return;
    const split = splitValuesByDestination(fields, values);
    dispatch('confirm', split);
  }

  function onCancel() {
    dispatch('cancel');
  }

  // immutable: editable al instalar (aquí siempre se puede · es la instalación).
  // El readonly aplicaría en una futura UI de "editar config" (Beta 9+).
  function isReadonly() {
    return false;
  }
</script>

<div class="cfg-overlay" on:click|self={onCancel} role="presentation">
  <div class="cfg-modal" role="dialog" aria-modal="true">
    <div class="band"></div>

    <div class="cfg-head">
      {#if appIcon}
        <img class="cfg-app-icon" src={appIcon} alt={appName} />
      {:else}
        <div class="cfg-app-icon placeholder">{(appName || '?')[0]}</div>
      {/if}
      <div class="cfg-head-txt">
        <div class="cfg-title">Instalar {appName}</div>
        <div class="cfg-sub">Esta app necesita unos datos antes de instalar</div>
      </div>
    </div>

    <div class="cfg-body">
      {#if envFields.length > 0}
        <div class="cfg-section">Configuración</div>
        {#each envFields as field (field.key)}
          <ConfigField
            {field}
            bind:value={values[field.key]}
            error={errors[field.key] || ''}
            readonly={isReadonly(field)}
          />
        {/each}
      {/if}

      {#if postFields.length > 0}
        <div class="cfg-section sep">Administrador</div>
        {#each postFields as field (field.key)}
          <ConfigField
            {field}
            bind:value={values[field.key]}
            error={errors[field.key] || ''}
            readonly={isReadonly(field)}
          />
        {/each}
        <div class="cfg-note">
          Se creará automáticamente. Los demás usuarios los añadirás luego como admin.
        </div>
      {/if}
    </div>

    <div class="cfg-foot">
      <button type="button" class="btn ghost" on:click={onCancel}>Cancelar</button>
      <button type="button" class="btn primary" on:click={onInstall}>Instalar</button>
    </div>
  </div>
</div>

<style>
  .cfg-overlay {
    position: fixed; inset: 0; z-index: 1000;
    background: rgba(0, 0, 0, 0.55);
    display: flex; align-items: center; justify-content: center;
    padding: 20px;
  }
  .cfg-modal {
    width: 100%; max-width: 440px;
    background: var(--bg-window); border-radius: 14px; overflow: hidden;
    box-shadow: 0 20px 60px rgba(0, 0, 0, 0.5), 0 0 0 1px rgba(255, 255, 255, 0.05);
    max-height: 90vh; display: flex; flex-direction: column;
  }
  .band { height: 3px; background: var(--nim-green); opacity: 0.85; flex-shrink: 0; }

  .cfg-head { padding: 18px 20px 6px; display: flex; align-items: center; gap: 11px; }
  .cfg-app-icon {
    width: 34px; height: 34px; border-radius: 8px; flex-shrink: 0;
    object-fit: cover;
  }
  .cfg-app-icon.placeholder {
    background: linear-gradient(135deg, #0dbd8b, var(--nim-green));
    display: flex; align-items: center; justify-content: center;
    font-weight: 700; color: #06281c; font-size: 17px; text-transform: uppercase;
  }
  .cfg-head-txt { display: flex; flex-direction: column; gap: 2px; }
  .cfg-title { font-size: 16px; font-weight: 600; color: var(--fg); }
  .cfg-sub { font-size: 12px; color: var(--fg-4); }

  .cfg-body {
    padding: 14px 20px 6px; display: flex; flex-direction: column; gap: 16px;
    overflow-y: auto;
  }
  .cfg-section {
    font-size: 10px; color: var(--fg-5); letter-spacing: 0.6px;
    text-transform: uppercase; font-weight: 600;
  }
  .cfg-section.sep { border-top: 1px solid var(--bd); padding-top: 14px; }
  .cfg-note { font-size: 10.5px; color: var(--fg-4); line-height: 1.45; }

  .cfg-foot {
    padding: 16px 20px; display: flex; justify-content: flex-end; gap: 8px;
    border-top: 1px solid var(--bd); margin-top: 8px; flex-shrink: 0;
  }
  .btn {
    padding: 8px 18px; border-radius: 7px; font-size: 13px; font-weight: 500;
    cursor: pointer; border: 1px solid transparent; font-family: var(--font-sans);
    transition: all 0.12s;
  }
  .btn.ghost { background: transparent; border-color: var(--bd-3); color: var(--fg-3); }
  .btn.ghost:hover { color: var(--fg); border-color: var(--fg-4); }
  .btn.primary { background: var(--nim-green); color: #06281c; font-weight: 600; }
  .btn.primary:hover { filter: brightness(1.1); }
</style>
