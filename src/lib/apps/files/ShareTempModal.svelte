<script>
  /**
   * ShareTempModal · Files · Compartir temporal
   * ─────────────────────────────────────────────
   * Dos estados:
   *   A) Configurar: alcance (LAN/público) · tiempo de exposición ·
   *      acceso (libre/contraseña) · descargas simultáneas → Generar.
   *   B) Enlace listo: URL + copiar + QR + revocar.
   *
   * API: POST /api/tempshares · DELETE /api/tempshares/{token}
   * El enlace público usa publicBase (dominio de exposición) si existe;
   * el LAN usa el origin actual del navegador.
   */
  import { createEventDispatcher } from 'svelte';
  import { jsonHdrs as hdrs } from '$lib/stores/auth.js';
  import qrcode from 'qrcode-generator';

  const dispatch = createEventDispatcher();

  /** { file, share, path } | null */
  export let target = null;

  // ─── Estado del formulario ───
  let scope = 'public';
  let ttl = 24;
  let accessMode = 'free'; // 'free' | 'password'
  let password = '';
  let maxConcurrent = 2;
  let creating = false;
  let error = '';

  // ─── Estado post-creación ───
  let created = null;   // item devuelto por la API
  let publicBase = '';
  let copied = false;

  const TTL_OPTIONS = [
    { label: '1h', hours: 1 },
    { label: '3h', hours: 3 },
    { label: '12h', hours: 12 },
    { label: '24h', hours: 24 },
    { label: '3d', hours: 72 },
    { label: '7d', hours: 168 },
  ];

  // Reset al abrir con un archivo nuevo
  $: if (target) resetForm();
  let lastTarget = null;
  function resetForm() {
    if (target === lastTarget) return;
    lastTarget = target;
    scope = 'public'; ttl = 24; accessMode = 'free'; password = '';
    maxConcurrent = 2; creating = false; error = ''; created = null; copied = false;
  }

  $: shareUrl = created
    ? (created.scope === 'public' && publicBase
        ? `${publicBase}/s/${created.token}`
        : `${location.origin}/s/${created.token}`)
    : '';

  $: qrSvg = shareUrl ? makeQr(shareUrl) : '';
  function makeQr(url) {
    try {
      const qr = qrcode(0, 'M');
      qr.addData(url);
      qr.make();
      return qr.createSvgTag({ cellSize: 3, margin: 2, scalable: true });
    } catch { return ''; }
  }

  function fmtBytes(b) {
    if (b == null) return '';
    if (b < 1024) return `${b} B`;
    const units = ['KB', 'MB', 'GB', 'TB'];
    let i = -1;
    do { b /= 1024; i++; } while (b >= 1024 && i < units.length - 1);
    return `${b.toFixed(1)} ${units[i]}`;
  }

  async function generate() {
    if (creating) return;
    error = '';
    if (accessMode === 'password' && !password.trim()) {
      error = 'Escribe una contraseña o elige descarga libre';
      return;
    }
    creating = true;
    try {
      const r = await fetch('/api/tempshares', {
        method: 'POST',
        headers: hdrs(),
        body: JSON.stringify({
          share: target.share,
          path: target.path,
          scope,
          ttlHours: ttl,
          password: accessMode === 'password' ? password : '',
          maxConcurrent,
        }),
      });
      const data = await r.json();
      if (!r.ok) { error = data.error || 'No se pudo crear el enlace'; }
      else { created = data.item; publicBase = data.publicBase || ''; }
    } catch { error = 'Error de red'; }
    creating = false;
  }

  async function copyLink() {
    try {
      await navigator.clipboard.writeText(shareUrl);
      copied = true;
      setTimeout(() => (copied = false), 1600);
    } catch {}
  }

  async function revoke() {
    if (!created) return;
    try {
      await fetch(`/api/tempshares/${created.token}`, { method: 'DELETE', headers: hdrs() });
    } catch {}
    close();
  }

  function close() { dispatch('close'); }

  function handleKeydown(e) {
    if (target && e.key === 'Escape') close();
  }

  $: ttlLabel = TTL_OPTIONS.find(o => o.hours === ttl)?.label || `${ttl}h`;
</script>

<svelte:window on:keydown={handleKeydown} />

{#if target}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
  <div class="stm-overlay" on:click={close}>
    <div class="stm-modal" on:click|stopPropagation role="dialog" aria-label="Compartir temporal">
      <div class="stm-band"></div>
      <div class="stm-body">

        <div class="stm-head">
          <div class="stm-head-ico">
            {#if created}
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/><polyline points="22 4 12 14.01 9 11.01"/></svg>
            {:else}
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/></svg>
            {/if}
          </div>
          <div>
            <div class="stm-title">{created ? 'Enlace generado' : 'Compartir temporal'}</div>
            <div class="stm-sub">{created ? target.file.name : 'enlace público con caducidad'}</div>
          </div>
          <button class="stm-x" on:click={close} title="Cerrar" aria-label="Cerrar">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
          </button>
        </div>

        {#if !created}
          <!-- ═══ Estado A · configurar ═══ -->
          <div class="stm-file">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>
            <span class="stm-file-name">{target.file.name}</span>
            <span class="stm-file-size">{fmtBytes(target.file.size)}</span>
          </div>

          <div class="stm-label">Alcance</div>
          <div class="stm-seg">
            <button class:sel={scope === 'lan'} on:click={() => (scope = 'lan')}>
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M3 9l9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg>
              Solo LAN
            </button>
            <button class:sel={scope === 'public'} on:click={() => (scope = 'public')}>
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg>
              Público
            </button>
          </div>

          <div class="stm-label">Tiempo de exposición</div>
          <div class="stm-chips">
            {#each TTL_OPTIONS as opt}
              <button class:sel={ttl === opt.hours} on:click={() => (ttl = opt.hours)}>{opt.label}</button>
            {/each}
          </div>

          <div class="stm-label">Acceso</div>
          <div class="stm-seg">
            <button class:sel={accessMode === 'free'} on:click={() => (accessMode = 'free')}>Descarga libre</button>
            <button class:sel={accessMode === 'password'} on:click={() => (accessMode = 'password')}>
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>
              Con contraseña
            </button>
          </div>
          {#if accessMode === 'password'}
            <input
              class="stm-pw"
              type="password"
              bind:value={password}
              placeholder="Contraseña del enlace"
              autocomplete="new-password"
            />
          {/if}

          <div class="stm-row">
            <span class="stm-row-lbl">Descargas simultáneas</span>
            <div class="stm-stepper">
              <button on:click={() => (maxConcurrent = Math.max(0, maxConcurrent - 1))} aria-label="Menos">−</button>
              <span class="stm-stepper-v">{maxConcurrent === 0 ? '∞' : maxConcurrent}</span>
              <button on:click={() => (maxConcurrent = Math.min(64, maxConcurrent + 1))} aria-label="Más">+</button>
            </div>
          </div>

          {#if error}<div class="stm-error">{error}</div>{/if}

          <button class="stm-primary" on:click={generate} disabled={creating}>
            {#if creating}Generando…{:else}
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/></svg>
              Generar enlace
            {/if}
          </button>

        {:else}
          <!-- ═══ Estado B · enlace listo ═══ -->
          <div class="stm-tags">
            <span class="stm-tag green">
              {#if created.scope === 'public'}Público{:else}Solo LAN{/if}
            </span>
            <span class="stm-tag">caduca en {ttlLabel}</span>
            <span class="stm-tag">{created.hasPassword ? 'contraseña' : 'libre'}</span>
            <span class="stm-tag">{created.maxConcurrent === 0 ? '∞' : `máx ${created.maxConcurrent}`}</span>
          </div>

          <div class="stm-label">Enlace para compartir</div>
          <div class="stm-link">
            <span class="stm-link-url">{shareUrl}</span>
            <button class="stm-link-copy" on:click={copyLink} title="Copiar" aria-label="Copiar enlace">
              {#if copied}
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><polyline points="20 6 9 17 4 12"/></svg>
              {:else}
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
              {/if}
            </button>
          </div>

          {#if created.scope === 'public' && !publicBase}
            <div class="stm-warn">Sin dominio de exposición configurado: el enlace usa la dirección local. Configúralo en Network → Exposición.</div>
          {/if}

          {#if qrSvg}
            <div class="stm-qr">{@html qrSvg}</div>
          {/if}

          <div class="stm-actions">
            <button class="stm-primary flex1" on:click={copyLink}>
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
              {copied ? '¡Copiado!' : 'Copiar enlace'}
            </button>
            <button class="stm-danger" on:click={revoke}>
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="10"/><line x1="4.93" y1="4.93" x2="19.07" y2="19.07"/></svg>
              Revocar
            </button>
          </div>
        {/if}

      </div>
    </div>
  </div>
{/if}

<style>
  .stm-overlay {
    position: fixed; inset: 0; z-index: 1000;
    background: rgba(0, 0, 0, 0.55);
    display: flex; align-items: center; justify-content: center;
    padding: 20px;
  }
  .stm-modal {
    width: 100%; max-width: 380px;
    background: var(--bg-window, #16161a);
    border-radius: 6px; overflow: hidden;
    box-shadow: 0 20px 60px rgba(0,0,0,0.5), 0 0 0 1px rgba(255,255,255,0.05);
    max-height: 90vh; display: flex; flex-direction: column;
    font-family: var(--font-sans, ui-sans-serif, system-ui, sans-serif);
  }
  .stm-band { height: 2px; background: var(--signal, #5b8ff9); flex-shrink: 0; }
  .stm-body { padding: 18px 20px; overflow-y: auto; }

  .stm-head { display: flex; align-items: center; gap: 10px; margin-bottom: 16px; }
  .stm-head-ico {
    width: 34px; height: 34px; border-radius: 4px; flex-shrink: 0;
    background: rgba(91, 143, 249, 0.12); color: var(--signal, #5b8ff9);
    display: flex; align-items: center; justify-content: center;
  }
  .stm-head-ico svg { width: 17px; height: 17px; }
  .stm-title { font-size: 14px; color: var(--ink, #f2f2f5); font-weight: 500; }
  .stm-sub { font-size: 11px; color: var(--ink-mute, #9a9aa3); }
  .stm-x {
    margin-left: auto; width: 28px; height: 28px; flex-shrink: 0;
    display: flex; align-items: center; justify-content: center;
    background: transparent; border: none; border-radius: 5px;
    color: var(--ink-mute, #9a9aa3); cursor: pointer; transition: all 0.12s;
  }
  .stm-x svg { width: 15px; height: 15px; }
  .stm-x:hover { color: var(--crit, #ff5a5a); background: rgba(255,90,90,0.08); }

  .stm-file {
    display: flex; align-items: center; gap: 10px;
    padding: 9px 11px; background: var(--bg-inner, #101015);
    border-radius: 6px; margin-bottom: 16px;
  }
  .stm-file svg { width: 18px; height: 18px; color: #7a9eb1; flex-shrink: 0; }
  .stm-file-name {
    flex: 1; font-size: 12px; color: var(--ink, #e8e8ea);
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .stm-file-size { font-size: 11px; color: var(--ink-trace, #6a6a72); font-family: var(--font-mono, monospace); }

  .stm-label {
    font-size: 11px;
    color: var(--ink-trace, #6a6a72); margin-bottom: 7px;
  }

  .stm-seg { display: flex; gap: 6px; margin-bottom: 15px; }
  .stm-seg button {
    flex: 1; display: flex; align-items: center; justify-content: center; gap: 5px;
    padding: 7px; border-radius: 6px; font-size: 11px; cursor: pointer;
    color: var(--ink-mute, #9a9aa3); background: transparent;
    border: 1px solid var(--line, rgba(255,255,255,0.1));
    font-family: inherit; transition: all 0.12s;
  }
  .stm-seg button svg { width: 13px; height: 13px; }
  .stm-seg button:hover { border-color: var(--line-bright, rgba(255,255,255,0.2)); }
  .stm-seg button.sel {
    color: white; background: var(--signal, #5b8ff9);
    border-color: var(--signal, #5b8ff9); font-weight: 600;
  }

  .stm-chips { display: flex; gap: 5px; margin-bottom: 15px; flex-wrap: wrap; }
  .stm-chips button {
    padding: 6px 10px; border-radius: 6px; font-size: 11px; cursor: pointer;
    color: var(--ink-mute, #9a9aa3); background: transparent;
    border: 1px solid var(--line, rgba(255,255,255,0.1));
    font-family: inherit; transition: all 0.12s;
  }
  .stm-chips button:hover { border-color: var(--line-bright, rgba(255,255,255,0.2)); }
  .stm-chips button.sel {
    color: white; background: var(--signal, #5b8ff9);
    border-color: var(--signal, #5b8ff9); font-weight: 600;
  }

  .stm-pw {
    width: 100%; padding: 9px 12px; border-radius: 6px; margin: -7px 0 15px;
    background: var(--bg-inner, #101015); color: var(--ink, #e8e8ea);
    border: 1px solid var(--line, rgba(255,255,255,0.12));
    font-size: 12px; font-family: inherit; outline: none;
  }
  .stm-pw:focus { border-color: rgba(91, 143, 249, 0.55); }

  .stm-row { display: flex; align-items: center; justify-content: space-between; margin-bottom: 18px; }
  .stm-row-lbl { font-size: 11px; color: var(--ink-dim, #c8c8cf); }
  .stm-stepper {
    display: flex; align-items: center;
    border: 1px solid var(--line, rgba(255,255,255,0.12));
    border-radius: 6px; overflow: hidden;
  }
  .stm-stepper button {
    width: 28px; height: 26px; display: flex; align-items: center; justify-content: center;
    background: transparent; border: none; color: var(--ink-mute, #9a9aa3);
    font-size: 14px; cursor: pointer; font-family: inherit;
  }
  .stm-stepper button:hover { color: var(--ink, #f2f2f5); background: rgba(255,255,255,0.04); }
  .stm-stepper-v {
    width: 32px; height: 26px; display: flex; align-items: center; justify-content: center;
    color: var(--ink, #f2f2f5); font-size: 12px; font-family: var(--font-mono, monospace);
    border-left: 1px solid var(--line, rgba(255,255,255,0.1));
    border-right: 1px solid var(--line, rgba(255,255,255,0.1));
  }

  .stm-error {
    font-size: 11px; color: var(--st-crit, #ff5a5a);
    background: rgba(255,90,90,0.08); border: 1px solid rgba(255,90,90,0.25);
    border-radius: 6px; padding: 8px 10px; margin-bottom: 12px;
  }
  .stm-warn {
    font-size: 11px; color: #f0b429; line-height: 1.45;
    background: rgba(240,180,41,0.08); border: 1px solid rgba(240,180,41,0.25);
    border-radius: 6px; padding: 8px 10px; margin-bottom: 14px;
  }

  .stm-primary {
    width: 100%; display: flex; align-items: center; justify-content: center; gap: 7px;
    padding: 10px; border-radius: 6px; font-size: 12px; font-weight: 500;
    color: white; background: var(--signal, #5b8ff9);
    border: none; cursor: pointer; font-family: inherit; transition: filter 0.12s;
  }
  .stm-primary svg { width: 14px; height: 14px; }
  .stm-primary:hover { filter: brightness(1.08); }
  .stm-primary:disabled { opacity: 0.6; cursor: default; }
  .stm-primary.flex1 { flex: 1; width: auto; }

  .stm-tags { display: flex; gap: 6px; flex-wrap: wrap; margin-bottom: 16px; }
  .stm-tag {
    font-size: 10px; color: var(--ink-dim, #c8c8cf);
    background: rgba(255,255,255,0.06); padding: 4px 9px; border-radius: 4px;
  }
  .stm-tag.green { color: #9ab9ff; background: rgba(91,143,249,0.12); }

  .stm-link {
    display: flex; align-items: center; gap: 8px;
    padding: 10px 11px; background: var(--bg-inner, #101015);
    border: 1px solid rgba(91, 143, 249, 0.3); border-radius: 4px; margin-bottom: 14px;
  }
  .stm-link-url {
    flex: 1; font-size: 11px; color: var(--signal, #5b8ff9);
    font-family: var(--font-mono, monospace);
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .stm-link-copy {
    background: transparent; border: none; cursor: pointer;
    color: var(--ink-mute, #9a9aa3); display: flex; padding: 2px;
  }
  .stm-link-copy svg { width: 15px; height: 15px; }
  .stm-link-copy:hover { color: var(--ink, #f2f2f5); }

  .stm-qr {
    display: flex; align-items: center; justify-content: center;
    padding: 14px; background: var(--bg-inner, #101015);
    border-radius: 6px; margin-bottom: 14px;
  }
  .stm-qr :global(svg) { width: 120px; height: 120px; border-radius: 4px; }

  .stm-actions { display: flex; gap: 8px; }
  .stm-danger {
    display: flex; align-items: center; gap: 5px;
    padding: 9px 14px; border-radius: 6px; font-size: 12px;
    color: var(--crit, #f87171); background: transparent;
    border: 1px solid rgba(248,113,113,0.3); cursor: pointer;
    font-family: inherit; transition: all 0.12s;
  }
  .stm-danger svg { width: 14px; height: 14px; }
  .stm-danger:hover { background: rgba(248,113,113,0.08); }
</style>
