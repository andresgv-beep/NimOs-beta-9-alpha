<script>
  /**
   * CPTempLinks · Panel de Control · sección Enlaces compartidos
   * ─────────────────────────────────────────────────────────────
   * Gestión de los enlaces temporales creados desde Files →
   * "Compartir temporal". Distinto de CPShares (carpetas SMB/NFS
   * persistentes): esto son capability-URLs /s/{token} con caducidad.
   *
   * API:
   *   GET    /api/tempshares            → { items, publicBase, now }
   *   PATCH  /api/tempshares/{token}    → reconfigurar
   *   DELETE /api/tempshares/{token}    → revocar
   *   DELETE /api/tempshares/expired    → limpiar expirados
   */
  import { onMount, onDestroy } from 'svelte';
  import { jsonHdrs as hdrs } from '$lib/stores/auth.js';
  import qrcode from 'qrcode-generator';

  let items = [];
  let publicBase = '';
  let loading = true;
  let menuFor = null;      // token con el menú ⋯ abierto
  let qrFor = null;        // item con el modal QR abierto
  let editFor = null;      // item en reconfiguración
  let copiedTok = null;
  let nowMs = Date.now();

  // ─── Reconfig form ───
  let edScope = 'public';
  let edTtl = 24;
  let edAccessMode = 'keep'; // 'keep' | 'free' | 'password'
  let edPassword = '';
  let edMax = 0;
  let edSaving = false;
  let edError = '';

  const TTL_OPTIONS = [
    { label: '1h', hours: 1 }, { label: '3h', hours: 3 }, { label: '12h', hours: 12 },
    { label: '24h', hours: 24 }, { label: '3d', hours: 72 }, { label: '7d', hours: 168 },
  ];

  let tick;
  onMount(() => {
    load();
    tick = setInterval(() => (nowMs = Date.now()), 30000);
  });
  onDestroy(() => clearInterval(tick));

  async function load() {
    try {
      const r = await fetch('/api/tempshares', { headers: hdrs() });
      const data = await r.json();
      if (r.ok) {
        items = data.items || [];
        publicBase = data.publicBase || '';
      }
    } catch {}
    loading = false;
  }

  $: activeCount = items.filter(i => i.expiresAt > nowMs).length;
  $: expiredCount = items.length - activeCount;

  function linkFor(item) {
    if (item.scope === 'public' && publicBase) return `${publicBase}/s/${item.token}`;
    return `${location.origin}/s/${item.token}`;
  }

  function remaining(item) {
    const ms = item.expiresAt - nowMs;
    if (ms <= 0) return 'expirado';
    const h = Math.floor(ms / 3600000);
    const m = Math.floor((ms % 3600000) / 60000);
    if (h > 48) return `${Math.floor(h / 24)}d ${h % 24}h`;
    return `${h}h ${m}m`;
  }

  function fmtBytes(b) {
    if (b == null) return '';
    if (b < 1024) return `${b} B`;
    const units = ['KB', 'MB', 'GB', 'TB'];
    let i = -1;
    do { b /= 1024; i++; } while (b >= 1024 && i < units.length - 1);
    return `${b.toFixed(1)} ${units[i]}`;
  }

  async function copyLink(item) {
    try {
      await navigator.clipboard.writeText(linkFor(item));
      copiedTok = item.token;
      setTimeout(() => (copiedTok = null), 1500);
    } catch {}
    menuFor = null;
  }

  function makeQr(url) {
    try {
      const qr = qrcode(0, 'M');
      qr.addData(url);
      qr.make();
      return qr.createSvgTag({ cellSize: 3, margin: 2, scalable: true });
    } catch { return ''; }
  }

  async function revoke(item) {
    menuFor = null;
    try {
      await fetch(`/api/tempshares/${item.token}`, { method: 'DELETE', headers: hdrs() });
    } catch {}
    load();
  }

  async function cleanExpired() {
    try {
      await fetch('/api/tempshares/expired', { method: 'DELETE', headers: hdrs() });
    } catch {}
    load();
  }

  function openEdit(item) {
    menuFor = null;
    editFor = item;
    edScope = item.scope;
    edTtl = 24;
    edAccessMode = 'keep';
    edPassword = '';
    edMax = item.maxConcurrent;
    edError = '';
  }

  async function saveEdit() {
    if (edSaving || !editFor) return;
    edError = '';
    if (edAccessMode === 'password' && !edPassword.trim()) {
      edError = 'Escribe la nueva contraseña o elige otra opción';
      return;
    }
    edSaving = true;
    const body = { scope: edScope, ttlHours: edTtl, maxConcurrent: edMax };
    if (edAccessMode === 'free') body.clearPassword = true;
    if (edAccessMode === 'password') body.password = edPassword;
    try {
      const r = await fetch(`/api/tempshares/${editFor.token}`, {
        method: 'PATCH', headers: hdrs(), body: JSON.stringify(body),
      });
      const data = await r.json();
      if (!r.ok) edError = data.error || 'No se pudo guardar';
      else { editFor = null; load(); }
    } catch { edError = 'Error de red'; }
    edSaving = false;
  }

  // Cerrar menú ⋯ al clickar fuera
  function onWindowClick(e) {
    if (!e.target.closest('.tl-menu') && !e.target.closest('.tl-dots')) menuFor = null;
  }
</script>

<svelte:window on:click={onWindowClick} />

<div class="cp-templinks">

  <div class="tl-head">
    <span class="tl-count">
      {activeCount} activo{activeCount === 1 ? '' : 's'} · {expiredCount} expirado{expiredCount === 1 ? '' : 's'}
    </span>
  </div>

  {#if loading}
    <div class="tl-empty"><span class="tl-empty-msg">Cargando…</span></div>
  {:else if items.length === 0}
    <div class="tl-empty">
      <div class="tl-empty-ic">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/></svg>
      </div>
      <span class="tl-empty-title">Sin enlaces compartidos</span>
      <span class="tl-empty-msg">Crea uno desde Files: clic derecho en un archivo → Compartir temporal.</span>
    </div>
  {:else}
    <div class="tl-cols">
      <span>Archivo</span><span>Alcance</span><span>Caduca</span><span>Descargas</span><span></span>
    </div>

    <div class="tl-rows">
      {#each items as item (item.token)}
        {@const expired = item.expiresAt <= nowMs}
        <div class="tl-row" class:expired>
          <div class="tl-file">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>
            <div class="tl-file-txt">
              <span class="tl-file-name" title={`${item.share}/${item.path}`}>{item.fileName}</span>
              <span class="tl-file-tok">/s/{item.token} · {fmtBytes(item.sizeBytes)}</span>
            </div>
          </div>
          <span class="tl-scope" class:pub={item.scope === 'public'}>
            {item.scope === 'public' ? 'Público' : 'LAN'}
          </span>
          <span class="tl-expiry" class:red={expired}>{remaining(item)}</span>
          <span class="tl-dl">
            {#if item.hasPassword}
              <svg class="tl-lock" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>
            {/if}
            {item.downloads} / {item.maxConcurrent === 0 ? '∞' : item.maxConcurrent}
          </span>
          <button
            class="tl-dots"
            class:open={menuFor === item.token}
            on:click={() => (menuFor = menuFor === item.token ? null : item.token)}
            title="Acciones" aria-label="Acciones"
          >
            <svg viewBox="0 0 24 24" fill="currentColor"><circle cx="12" cy="5" r="1.6"/><circle cx="12" cy="12" r="1.6"/><circle cx="12" cy="19" r="1.6"/></svg>
          </button>

          {#if menuFor === item.token}
            <div class="tl-menu">
              <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
              <div class="tl-menu-item" on:click={() => copyLink(item)}>
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
                {copiedTok === item.token ? '¡Copiado!' : 'Copiar enlace'}
              </div>
              <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
              <div class="tl-menu-item" on:click={() => { qrFor = item; menuFor = null; }}>
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><path d="M14 14h3v3h-3zM20 14h1M14 20h1M20 20h1"/></svg>
                Ver QR
              </div>
              {#if !expired}
                <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
                <div class="tl-menu-item" on:click={() => openEdit(item)}>
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>
                  Reconfigurar
                </div>
              {/if}
              <div class="tl-menu-sep"></div>
              <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
              <div class="tl-menu-item danger" on:click={() => revoke(item)}>
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="10"/><line x1="4.93" y1="4.93" x2="19.07" y2="19.07"/></svg>
                Revocar
              </div>
            </div>
          {/if}
        </div>
      {/each}
    </div>

    <div class="tl-foot">
      <span class="tl-foot-note">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/></svg>
        Los enlaces expirados se limpian solos a las 24h
      </span>
      {#if expiredCount > 0}
        <button class="tl-clean" on:click={cleanExpired}>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14H6L5 6"/><path d="M10 11v6M14 11v6"/><path d="M9 6V4h6v2"/></svg>
          Limpiar expirados
        </button>
      {/if}
    </div>
  {/if}

</div>

<!-- ═══ Modal QR ═══ -->
{#if qrFor}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
  <div class="tl-overlay" on:click={() => (qrFor = null)}>
    <div class="tl-modal" on:click|stopPropagation role="dialog" aria-label="Código QR">
      <div class="tl-modal-band"></div>
      <div class="tl-modal-body center">
        <div class="tl-modal-title">{qrFor.fileName}</div>
        <div class="tl-qr">{@html makeQr(linkFor(qrFor))}</div>
        <div class="tl-qr-url">{linkFor(qrFor)}</div>
        <button class="tl-btn" on:click={() => (qrFor = null)}>Cerrar</button>
      </div>
    </div>
  </div>
{/if}

<!-- ═══ Modal Reconfigurar ═══ -->
{#if editFor}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
  <div class="tl-overlay" on:click={() => (editFor = null)}>
    <div class="tl-modal" on:click|stopPropagation role="dialog" aria-label="Reconfigurar enlace">
      <div class="tl-modal-band"></div>
      <div class="tl-modal-body">
        <div class="tl-modal-title">Reconfigurar · {editFor.fileName}</div>
        <div class="tl-modal-sub">La caducidad se re-extiende desde ahora.</div>

        <div class="tl-label">Alcance</div>
        <div class="tl-seg">
          <button class:sel={edScope === 'lan'} on:click={() => (edScope = 'lan')}>Solo LAN</button>
          <button class:sel={edScope === 'public'} on:click={() => (edScope = 'public')}>Público</button>
        </div>

        <div class="tl-label">Nueva caducidad</div>
        <div class="tl-chips">
          {#each TTL_OPTIONS as opt}
            <button class:sel={edTtl === opt.hours} on:click={() => (edTtl = opt.hours)}>{opt.label}</button>
          {/each}
        </div>

        <div class="tl-label">Acceso</div>
        <div class="tl-seg">
          <button class:sel={edAccessMode === 'keep'} on:click={() => (edAccessMode = 'keep')}>Mantener</button>
          <button class:sel={edAccessMode === 'free'} on:click={() => (edAccessMode = 'free')}>Libre</button>
          <button class:sel={edAccessMode === 'password'} on:click={() => (edAccessMode = 'password')}>Contraseña</button>
        </div>
        {#if edAccessMode === 'password'}
          <input class="tl-pw" type="password" bind:value={edPassword} placeholder="Nueva contraseña" autocomplete="new-password" />
        {/if}

        <div class="tl-row-cfg">
          <span>Descargas simultáneas</span>
          <div class="tl-stepper">
            <button on:click={() => (edMax = Math.max(0, edMax - 1))} aria-label="Menos">−</button>
            <span class="tl-stepper-v">{edMax === 0 ? '∞' : edMax}</span>
            <button on:click={() => (edMax = Math.min(64, edMax + 1))} aria-label="Más">+</button>
          </div>
        </div>

        {#if edError}<div class="tl-error">{edError}</div>{/if}

        <div class="tl-modal-actions">
          <button class="tl-btn primary" on:click={saveEdit} disabled={edSaving}>
            {edSaving ? 'Guardando…' : 'Guardar cambios'}
          </button>
          <button class="tl-btn" on:click={() => (editFor = null)}>Cancelar</button>
        </div>
      </div>
    </div>
  </div>
{/if}

<style>
  .cp-templinks { display: flex; flex-direction: column; gap: 12px; }

  .tl-head { display: flex; align-items: center; justify-content: flex-end; }
  .tl-count { font-size: 11px; color: var(--fg-5, #5a5a62); font-family: var(--font-mono, monospace); }

  /* ─── Cabecera de columnas + filas ─── */
  .tl-cols, .tl-row {
    display: grid;
    grid-template-columns: 1fr 90px 110px 100px 40px;
    gap: 12px; align-items: center;
  }
  .tl-cols {
    padding: 0 12px;
    font-size: 10px; letter-spacing: 0.6px; text-transform: uppercase;
    color: var(--fg-5, #5a5a62); font-family: var(--font-mono, monospace);
  }
  .tl-rows { display: flex; flex-direction: column; }
  .tl-row {
    position: relative;
    padding: 11px 12px;
    background: var(--bg-card, #16161a);
    border: 1px solid var(--line, rgba(255,255,255,0.06));
    border-bottom: none;
  }
  .tl-row:first-child { border-radius: 8px 8px 0 0; }
  .tl-row:last-child { border-radius: 0 0 8px 8px; border-bottom: 1px solid var(--line, rgba(255,255,255,0.06)); }
  .tl-row:first-child:last-child { border-radius: 8px; }
  .tl-row.expired { opacity: 0.5; }

  .tl-file { display: flex; align-items: center; gap: 10px; min-width: 0; }
  .tl-file > svg { width: 18px; height: 18px; color: #7a9eb1; flex-shrink: 0; }
  .tl-file-txt { display: flex; flex-direction: column; gap: 1px; min-width: 0; }
  .tl-file-name {
    font-size: 12.5px; color: var(--ink, #e8e8ea);
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .tl-file-tok { font-size: 10px; color: var(--fg-5, #6a6a72); font-family: var(--font-mono, monospace); }

  .tl-scope {
    justify-self: start;
    font-size: 10px; color: var(--ink-dim, #c8c8cf);
    background: rgba(255,255,255,0.06); padding: 3px 8px; border-radius: 4px;
  }
  .tl-scope.pub { color: var(--signal, #00ff9f); background: rgba(0,255,159,0.1); }

  .tl-expiry { font-size: 11px; color: var(--ink-dim, #c8c8cf); font-family: var(--font-mono, monospace); }
  .tl-expiry.red { color: var(--crit, #f87171); }

  .tl-dl {
    display: flex; align-items: center; gap: 5px;
    font-size: 11px; color: var(--ink-dim, #c8c8cf); font-family: var(--font-mono, monospace);
  }
  .tl-lock { width: 12px; height: 12px; color: #f0b429; }

  .tl-dots {
    justify-self: center; width: 26px; height: 26px;
    display: flex; align-items: center; justify-content: center;
    background: transparent; border: none; border-radius: 5px;
    color: var(--ink-mute, #9a9aa3); cursor: pointer; transition: all 0.12s;
  }
  .tl-dots svg { width: 15px; height: 15px; }
  .tl-dots:hover, .tl-dots.open { color: var(--signal, #00ff9f); background: rgba(255,255,255,0.05); }

  /* ─── Menú ⋯ ─── */
  .tl-menu {
    position: absolute; right: 8px; top: calc(100% - 6px); z-index: 50;
    width: 180px; background: var(--bg-inner, #101015);
    border: 1px solid var(--line-bright, rgba(255,255,255,0.1));
    border-radius: 8px; padding: 4px;
    box-shadow: 0 8px 32px rgba(0,0,0,0.5);
  }
  .tl-menu-item {
    display: flex; align-items: center; gap: 9px;
    padding: 7px 10px; border-radius: 5px;
    font-size: 12px; color: var(--ink-dim, #c8c8cf);
    cursor: pointer; transition: background 0.1s, color 0.1s;
  }
  .tl-menu-item svg { width: 13px; height: 13px; flex-shrink: 0; opacity: 0.7; }
  .tl-menu-item:hover { background: var(--side-active-bg, rgba(122,158,177,0.10)); color: var(--ink, #f2f2f5); }
  .tl-menu-item.danger { color: var(--crit, #f87171); }
  .tl-menu-item.danger:hover { background: rgba(248,113,113,0.10); color: var(--crit, #f87171); }
  .tl-menu-sep { height: 1px; background: var(--line, rgba(255,255,255,0.08)); margin: 3px 4px; }

  /* ─── Pie ─── */
  .tl-foot {
    display: flex; align-items: center; justify-content: space-between;
    padding: 11px 14px; background: var(--bg-inner, #131318);
    border: 1px solid var(--line, rgba(255,255,255,0.06)); border-radius: 8px;
  }
  .tl-foot-note {
    display: flex; align-items: center; gap: 6px;
    font-size: 11px; color: var(--fg-5, #6a6a72);
  }
  .tl-foot-note svg { width: 13px; height: 13px; }
  .tl-clean {
    display: flex; align-items: center; gap: 5px;
    font-size: 11px; color: var(--crit, #f87171);
    background: transparent; border: none; cursor: pointer; font-family: inherit;
    padding: 4px 6px; border-radius: 5px; transition: background 0.12s;
  }
  .tl-clean svg { width: 13px; height: 13px; }
  .tl-clean:hover { background: rgba(248,113,113,0.08); }

  /* ─── Empty ─── */
  .tl-empty {
    display: flex; flex-direction: column; align-items: center; justify-content: center;
    gap: 10px; padding: 56px 24px; text-align: center;
  }
  .tl-empty-ic {
    width: 44px; height: 44px; border-radius: 11px;
    background: var(--bg-card, #15151a); border: 1px solid var(--bd-3, #2a2a32);
    display: flex; align-items: center; justify-content: center;
    color: var(--fg-5, #5a5a62); margin-bottom: 4px;
  }
  .tl-empty-ic svg { width: 20px; height: 20px; }
  .tl-empty-title { font-size: 13px; color: var(--fg-2, #d0d0d4); }
  .tl-empty-msg { font-size: 11px; color: var(--fg-5, #5a5a62); max-width: 340px; line-height: 1.5; }

  /* ─── Modales (QR + reconfig) ─── */
  .tl-overlay {
    position: fixed; inset: 0; z-index: 1000;
    background: rgba(0,0,0,0.55);
    display: flex; align-items: center; justify-content: center; padding: 20px;
  }
  .tl-modal {
    width: 100%; max-width: 360px;
    background: var(--bg-window, #16161a);
    border-radius: 6px; overflow: hidden;
    box-shadow: 0 20px 60px rgba(0,0,0,0.5), 0 0 0 1px rgba(255,255,255,0.05);
    max-height: 90vh; display: flex; flex-direction: column;
  }
  .tl-modal-band { height: 3px; background: var(--signal, #00ff9f); opacity: 0.85; flex-shrink: 0; }
  .tl-modal-body { padding: 18px 20px; overflow-y: auto; }
  .tl-modal-body.center { text-align: center; }
  .tl-modal-title { font-size: 13px; color: var(--ink, #f2f2f5); font-weight: 500; margin-bottom: 4px; }
  .tl-modal-sub { font-size: 11px; color: var(--ink-mute, #9a9aa3); margin-bottom: 16px; }

  .tl-qr {
    display: flex; align-items: center; justify-content: center;
    padding: 14px; background: var(--bg-inner, #101015);
    border-radius: 6px; margin: 14px 0;
  }
  .tl-qr :global(svg) { width: 150px; height: 150px; border-radius: 4px; }
  .tl-qr-url {
    font-size: 10px; color: var(--signal, #00ff9f); font-family: var(--font-mono, monospace);
    word-break: break-all; margin-bottom: 16px;
  }

  .tl-label {
    font-size: 10px; letter-spacing: 0.6px; text-transform: uppercase;
    color: var(--fg-5, #6a6a72); margin-bottom: 7px;
  }
  .tl-seg { display: flex; gap: 6px; margin-bottom: 15px; }
  .tl-seg button {
    flex: 1; padding: 7px; border-radius: 6px; font-size: 11px; cursor: pointer;
    color: var(--ink-mute, #9a9aa3); background: transparent;
    border: 1px solid var(--line, rgba(255,255,255,0.1)); font-family: inherit;
    transition: all 0.12s;
  }
  .tl-seg button.sel {
    color: var(--bg-window, #0f0f14); background: var(--signal, #00ff9f);
    border-color: var(--signal, #00ff9f); font-weight: 500;
  }
  .tl-chips { display: flex; gap: 5px; margin-bottom: 15px; flex-wrap: wrap; }
  .tl-chips button {
    padding: 6px 10px; border-radius: 6px; font-size: 11px; cursor: pointer;
    color: var(--ink-mute, #9a9aa3); background: transparent;
    border: 1px solid var(--line, rgba(255,255,255,0.1)); font-family: inherit;
    transition: all 0.12s;
  }
  .tl-chips button.sel {
    color: var(--bg-window, #0f0f14); background: var(--signal, #00ff9f);
    border-color: var(--signal, #00ff9f); font-weight: 500;
  }
  .tl-pw {
    width: 100%; padding: 9px 12px; border-radius: 6px; margin: -7px 0 15px;
    background: var(--bg-inner, #101015); color: var(--ink, #e8e8ea);
    border: 1px solid var(--line, rgba(255,255,255,0.12));
    font-size: 12px; font-family: inherit; outline: none;
  }
  .tl-pw:focus { border-color: rgba(0, 255, 159, 0.4); }
  .tl-row-cfg {
    display: flex; align-items: center; justify-content: space-between;
    margin-bottom: 16px; font-size: 11px; color: var(--ink-dim, #c8c8cf);
  }
  .tl-stepper {
    display: flex; align-items: center;
    border: 1px solid var(--line, rgba(255,255,255,0.12));
    border-radius: 6px; overflow: hidden;
  }
  .tl-stepper button {
    width: 28px; height: 26px; display: flex; align-items: center; justify-content: center;
    background: transparent; border: none; color: var(--ink-mute, #9a9aa3);
    font-size: 14px; cursor: pointer; font-family: inherit;
  }
  .tl-stepper button:hover { color: var(--ink, #f2f2f5); background: rgba(255,255,255,0.04); }
  .tl-stepper-v {
    width: 32px; height: 26px; display: flex; align-items: center; justify-content: center;
    color: var(--ink, #f2f2f5); font-size: 12px; font-family: var(--font-mono, monospace);
    border-left: 1px solid var(--line, rgba(255,255,255,0.1));
    border-right: 1px solid var(--line, rgba(255,255,255,0.1));
  }
  .tl-error {
    font-size: 11px; color: var(--st-crit, #ff5a5a);
    background: rgba(255,90,90,0.08); border: 1px solid rgba(255,90,90,0.25);
    border-radius: 6px; padding: 8px 10px; margin-bottom: 12px;
  }
  .tl-modal-actions { display: flex; gap: 8px; }
  .tl-btn {
    padding: 9px 16px; border-radius: 6px; font-size: 12px; cursor: pointer;
    color: var(--ink-mute, #9a9aa3); background: transparent;
    border: 1px solid var(--line, rgba(255,255,255,0.12)); font-family: inherit;
    transition: all 0.12s;
  }
  .tl-btn:hover { color: var(--ink, #f2f2f5); border-color: var(--line-bright, rgba(255,255,255,0.2)); }
  .tl-btn.primary {
    flex: 1; color: var(--bg-window, #0f0f14); background: var(--signal, #00ff9f);
    border-color: var(--signal, #00ff9f); font-weight: 500;
  }
  .tl-btn.primary:hover { filter: brightness(1.08); }
  .tl-btn.primary:disabled { opacity: 0.6; cursor: default; }
</style>
