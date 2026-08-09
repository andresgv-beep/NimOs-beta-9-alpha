<script>
  import { createEventDispatcher } from 'svelte';
  import WizardFrame from '$lib/components/WizardFrame.svelte';

  export let open = false;
  export let shares = [];
  export let selectedShare = '';
  export let uploading = false;
  export let error = '';

  const dispatch = createEventDispatcher();
  let step = 1;
  let file = null;
  let dragOver = false;
  let input;
  let wasOpen = false;

  $: if (open && !wasOpen) { step = 1; file = null; dragOver = false; }
  $: wasOpen = open;
  $: canAdvance = step === 1 ? Boolean(file) : Boolean(selectedShare) && !uploading;

  function setFile(candidate) {
    if (!candidate?.name?.toLowerCase().endsWith('.torrent')) return;
    file = candidate;
  }
  function next() {
    if (step === 1) step = 2;
    else dispatch('submit', { file, share: selectedShare });
  }
</script>

<WizardFrame
  {open}
  title="Añadir descarga"
  currentStep={step}
  totalSteps={2}
  canGoBack={!uploading}
  {canAdvance}
  nextLabel={step === 1 ? 'Elegir destino' : uploading ? 'Añadiendo…' : 'Añadir torrent'}
  width={520}
  on:next={next}
  on:back={() => step = 1}
  on:cancel={() => !uploading && dispatch('cancel')}
>
  {#if step === 1}
    <div class="intro"><strong>Selecciona el archivo</strong><span>NimTorrent admite archivos .torrent. El contenido nunca se guardará en el disco del sistema.</span></div>
    <button
      type="button"
      class:over={dragOver}
      class:chosen={file}
      class="drop"
      on:click={() => input?.click()}
      on:dragover|preventDefault={() => dragOver = true}
      on:dragleave={() => dragOver = false}
      on:drop|preventDefault={(event) => { dragOver = false; setFile(event.dataTransfer?.files?.[0]); }}
    >
      <span class="file-icon">{file ? '✓' : '↓'}</span>
      <strong>{file?.name || 'Arrastra un archivo .torrent'}</strong>
      <span>{file ? 'Pulsa para cambiarlo' : 'o pulsa para buscarlo'}</span>
    </button>
    <input bind:this={input} hidden type="file" accept=".torrent,application/x-bittorrent" on:change={(event) => setFile(event.target?.files?.[0])} />
  {:else}
    <div class="intro"><strong>Elige dónde se guardará</strong><span>Solo aparecen carpetas con escritura dentro de pools locales activos.</span></div>
    <div class="destinations">
      {#each shares as share (share.name)}
        <button type="button" class:selected={selectedShare === share.name} on:click={() => dispatch('selectShare', share.name)}>
          <span class="folder">▰</span>
          <span><strong>{share.displayName || share.name}</strong><small>Pool local · escritura permitida</small></span>
          <span class="check">{selectedShare === share.name ? '✓' : ''}</span>
        </button>
      {/each}
    </div>
    <div class="summary"><span>Archivo</span><strong>{file?.name}</strong></div>
    {#if error}<div class="error">{error}</div>{/if}
  {/if}
</WizardFrame>

<style>
  .intro { display: grid; gap: 5px; }
  .intro strong { color: var(--ink); font-size: 14px; }
  .intro span { color: var(--ink-mute); font-size: 12px; line-height: 1.5; }
  .drop { min-height: 180px; border: 1px dashed var(--line); border-radius: 8px; background: var(--bg-card); color: var(--ink-dim); display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 8px; font: inherit; cursor: pointer; }
  .drop:hover, .drop.over { border-color: var(--signal); background: var(--ui-select-bg); }
  .drop.chosen { border-style: solid; }
  .drop strong { max-width: 390px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--ink); font-size: 13px; }
  .drop span { color: var(--ink-mute); font-size: 11px; }
  .file-icon { width: 34px; height: 34px; border-radius: 7px; display: grid; place-items: center; background: var(--ui-select-bg); color: var(--signal) !important; font-size: 17px !important; }
  .destinations { display: grid; gap: 7px; max-height: 245px; overflow: auto; }
  .destinations button { display: grid; grid-template-columns: 30px 1fr 20px; align-items: center; gap: 10px; width: 100%; padding: 11px 12px; border: 1px solid var(--line); border-radius: 7px; background: var(--bg-card); color: var(--ink-dim); text-align: left; font: inherit; cursor: pointer; }
  .destinations button:hover { background: var(--side-hover); }
  .destinations button.selected { border-color: var(--signal); background: var(--ui-select-bg); }
  .folder, .check { color: var(--signal); }
  .destinations strong, .destinations small { display: block; }
  .destinations strong { color: var(--ink); font-size: 12px; }
  .destinations small { margin-top: 3px; color: var(--ink-mute); font-size: 10px; }
  .summary { display: grid; grid-template-columns: 70px 1fr; gap: 10px; padding: 10px 12px; border: 1px solid var(--line); border-radius: 7px; font-size: 11px; }
  .summary span { color: var(--ink-mute); }
  .summary strong { color: var(--ink-dim); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .error { padding: 10px 12px; border-left: 3px solid var(--crit); background: color-mix(in srgb, var(--crit) 8%, transparent); color: var(--crit); font-size: 11px; }
</style>
