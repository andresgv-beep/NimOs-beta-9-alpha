<script>
  /**
   * ShareCard · Panel de Control · Carpetas compartidas
   * ────────────────────────────────────────────────────
   * Card desplegable de una carpeta compartida (share). Header siempre visible;
   * al pulsar despliega el panel con uso, metadatos, usuarios, carpetas
   * gestionadas (Fase 3) y acciones (incl. Eliminar).
   *
   * Cableado REAL: uso (used/quota/available), papelera (recycleBin), usuarios
   * (permissions), carpetas gestionadas (/api/shares/{share}/folders), borrado
   * (DELETE /api/shares/{name}).
   *
   * MAQUETA INERTE (sin backend aún): distribución por tipo, protocolos
   * (el share no persiste smb/nfs/ftp), snapshot.
   */
  import { createEventDispatcher } from 'svelte';
  import { hdrs } from '$lib/stores/auth.js';

  export let share;            // objeto share del GET /api/shares
  export let open = false;

  const dispatch = createEventDispatcher();

  // ─── Distribución por tipo ───
  let dist = null;          // { categories: {cat: bytes}, totalBytes }
  let distLoading = false;
  let distLoaded = false;

  // Categorías → color (coincide con tokens --cat-* de la maqueta) y etiqueta.
  const CAT_META = {
    video:    { color: '#4db8ff', label: 'Vídeo' },
    image:    { color: '#00ff9f', label: 'Imagen' },
    music:    { color: '#ffc857', label: 'Audio' },
    document: { color: '#b07aff', label: 'Documento' },
    code:     { color: '#7a9eb1', label: 'Código' },
    archive:  { color: '#ff9c5a', label: 'Archivo' },
    other:    { color: '#5a5a62', label: 'Otros' },
  };

  $: name = share?.name || '';
  $: displayName = share?.displayName || name;
  $: pool = share?.pool || '—';
  $: mountPoint = share?.mountPoint || share?.path || '';
  $: permissions = share?.permissions || {};
  $: userCount = Object.keys(permissions).length;
  $: recycleBin = !!share?.recycleBin;

  // Uso real (bytes). El backend expone used/quota/available.
  $: usedBytes = num(share?.used);
  $: quotaBytes = num(share?.quota);
  $: availBytes = num(share?.available);
  $: hasQuota = quotaBytes > 0;
  $: usedPct = hasQuota && quotaBytes > 0
    ? Math.min(100, Math.round((usedBytes / quotaBytes) * 100))
    : 0;

  // Anillo SVG
  const R = 48;
  const CIRC = 2 * Math.PI * R; // 301.59
  // Mínimo visual: si hay algo de uso (>0), mostrar al menos un 3% de arco para
  // que el segmento verde se perciba aunque el uso real sea ínfimo.
  $: ringPctVisual = usedPct > 0 ? Math.max(usedPct, 3) : 0;
  $: ringOffset = CIRC - (ringPctVisual / 100) * CIRC;
  $: ringColor = usedPct >= 90 ? 'var(--st-crit, #ff5a5a)'
    : usedPct >= 70 ? 'var(--st-warn, #ffc857)'
    : 'var(--nim-green, #00ff9f)';

  function num(v) {
    const n = Number(v);
    return Number.isFinite(n) ? n : 0;
  }
  function fmtBytes(b) {
    b = num(b);
    if (!b) return '0 B';
    const u = ['B', 'KB', 'MB', 'GB', 'TB'];
    let i = 0, n = b;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return `${n.toFixed(n < 10 && i > 0 ? 1 : 0)} ${u[i]}`;
  }
  function initial(s) { return (s || '?').charAt(0).toUpperCase(); }

  function toggle() {
    open = !open;
    if (open && !distLoaded) loadDist();
  }

  // ─── Cargar distribución por tipo ───
  async function loadDist() {
    distLoading = true;
    try {
      const r = await fetch(`/api/shares/${encodeURIComponent(name)}/filetypes`, { headers: hdrs() });
      if (r.ok) dist = await r.json();
    } catch { /* sin distribución, se oculta */ }
    distLoading = false;
    distLoaded = true;
  }

  // Segmentos ordenados de mayor a menor, solo categorías con bytes.
  $: distSegments = dist && dist.totalBytes > 0
    ? Object.entries(dist.categories || {})
        .filter(([, b]) => b > 0)
        .sort((a, b) => b[1] - a[1])
        .map(([cat, bytes]) => ({
          cat,
          bytes,
          pct: (bytes / dist.totalBytes) * 100,
          color: (CAT_META[cat] || CAT_META.other).color,
          label: (CAT_META[cat] || CAT_META.other).label,
        }))
    : [];

  // ─── Eliminar el share (lo gestiona el padre vía evento) ───
  function requestDelete() {
    dispatch('delete', { name });
  }

  function requestEdit() {
    dispatch('edit', { name });
  }

  function permClass(p) {
    return p === 'rw' ? 'rw' : p === 'ro' ? 'ro' : 'owner';
  }
</script>

<div class="share" class:open>
  <!-- Header -->
  <div class="share-head" on:click={toggle} on:keydown={(e) => e.key === 'Enter' && toggle()} role="button" tabindex="0">
    <div class="share-icon">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
        <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>
      </svg>
    </div>
    <div class="share-id">
      <div class="share-name">{displayName}</div>
      <div class="share-pool">{pool} · {userCount} {userCount === 1 ? 'usuario' : 'usuarios'}</div>
    </div>
    <span class="share-chevron">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"/></svg>
    </span>
  </div>

  <!-- Panel desplegable -->
  {#if open}
    <div class="share-panel">
      <!-- Anillo + metadatos -->
      <div class="panel-top">
        <div class="usage-ring">
          <div class="ring-wrap">
            <svg width="104" height="104" viewBox="0 0 110 110">
              <circle cx="55" cy="55" r={R} fill="none" stroke="#3a3a44" stroke-width="8"/>
              {#if hasQuota}
                <circle cx="55" cy="55" r={R} fill="none" stroke={ringColor} stroke-width="8"
                  stroke-linecap="round" stroke-dasharray={CIRC} stroke-dashoffset={ringOffset}
                  transform="rotate(-90 55 55)"/>
              {/if}
            </svg>
            <div class="ring-center">
              <span class="ring-pct">{hasQuota ? usedPct + '%' : '—'}</span>
              <span class="ring-lbl">usado</span>
            </div>
          </div>
          <div class="usage-detail">
            <div class="usage-used">{fmtBytes(usedBytes)} usados</div>
            <div class="usage-free">
              {#if hasQuota}{fmtBytes(quotaBytes)} cuota{:else}sin límite{/if}
              {#if availBytes} · {fmtBytes(availBytes)} libres{/if}
            </div>
          </div>
        </div>

        <div class="panel-meta">
          <div class="meta-item">
            <div class="meta-k">Pool</div>
            <div class="meta-v">{pool}</div>
          </div>
          <div class="meta-item">
            <div class="meta-k">Cuota</div>
            <div class="meta-v mono">{hasQuota ? fmtBytes(quotaBytes) : 'sin límite'}</div>
          </div>
          <div class="meta-item">
            <div class="meta-k">Papelera</div>
            <div class="meta-v mono" class:on={recycleBin}>{recycleBin ? 'activada' : 'desactivada'}</div>
          </div>
          <div class="meta-item">
            <div class="meta-k">Usuarios</div>
            <div class="meta-v mono">{userCount}</div>
          </div>
          <div class="meta-item full">
            <div class="meta-k">Montaje</div>
            <div class="meta-v path">{mountPoint || '—'}</div>
          </div>
        </div>
      </div>

      <!-- Distribución por tipo -->
      {#if distLoading}
        <div class="dist">
          <div class="dist-lbl">Distribución por tipo</div>
          <div class="dist-calc">Calculando…</div>
        </div>
      {:else if distSegments.length > 0}
        <div class="dist">
          <div class="dist-lbl">Distribución por tipo</div>
          <div class="dist-bar">
            {#each distSegments as seg (seg.cat)}
              <div class="dist-seg" style="width: {seg.pct}%; background: {seg.color}" title="{seg.label}"></div>
            {/each}
          </div>
          <div class="dist-legend">
            {#each distSegments as seg (seg.cat)}
              <div class="dist-item">
                <span class="dist-dot" style="background: {seg.color}"></span>
                {seg.label} <span class="v">· {fmtBytes(seg.bytes)}</span>
              </div>
            {/each}
          </div>
        </div>
      {/if}

      <!-- Usuarios con permisos -->
      {#if userCount > 0}
        <div class="panel-users">
          <div class="pu-lbl">Acceso de usuarios</div>
          <div class="pu-list">
            {#each Object.entries(permissions) as [user, perm] (user)}
              <div class="pu-row">
                <span class="pu-avatar">{initial(user)}</span>
                <span class="pu-name">{user}</span>
                <span class="pu-perm {permClass(perm)}">{perm}</span>
              </div>
            {/each}
          </div>
        </div>
      {/if}

      <!-- Acciones -->
      <div class="panel-actions">
        <div class="pa-spacer"></div>
        <button class="pa-btn" on:click|stopPropagation={requestEdit}>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.12 2.12 0 0 1 3 3L12 15l-4 1 1-4z"/></svg>
          Editar
        </button>
        <button class="pa-btn danger" on:click|stopPropagation={requestDelete}>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/></svg>
          Eliminar carpeta
        </button>
      </div>
    </div>
  {/if}
</div>

<style>
  .share {
    background: var(--bg-card, #15151a);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 8px;
    overflow: hidden;
    transition: border-color 0.15s;
  }
  .share.open { border-color: var(--ui-select-border, rgba(122,158,177,0.35)); }

  .share-head {
    display: flex;
    align-items: center;
    gap: 14px;
    padding: 14px 16px;
    cursor: pointer;
    transition: background 0.12s;
  }
  .share-head:hover { background: rgba(255,255,255,0.012); }

  .share-icon {
    width: 34px; height: 34px;
    border-radius: 7px;
    background: rgba(0, 255, 159, 0.08);
    border: 1px solid rgba(0, 255, 159, 0.2);
    display: flex; align-items: center; justify-content: center;
    color: var(--nim-green, #00ff9f);
    flex-shrink: 0;
  }
  .share-icon svg { width: 17px; height: 17px; }
  .share-id { flex: 1; min-width: 0; }
  .share-name { font-size: 14px; font-weight: 600; color: var(--fg, #f0f0f0); }
  .share-pool {
    font-family: var(--font-mono, ui-monospace, monospace);
    font-size: 11px;
    color: var(--fg-4, #7a7a82);
    margin-top: 2px;
  }
  .share-chevron { color: var(--fg-5, #5a5a62); transition: transform 0.2s; flex-shrink: 0; }
  .share-chevron svg { width: 14px; height: 14px; display: block; }
  .share.open .share-chevron { transform: rotate(180deg); }

  .share-panel { border-top: 1px solid var(--bd-sep, #2e2e38); background: var(--bg-window, #16161a); }

  .panel-top { display: grid; grid-template-columns: 170px 1fr; }
  .usage-ring {
    padding: 18px;
    display: flex; flex-direction: column; align-items: center; justify-content: center;
    border-right: 1px solid var(--bd-sep, #2e2e38);
    gap: 4px;
  }
  .ring-wrap { position: relative; width: 104px; height: 104px; }
  .ring-center {
    position: absolute; inset: 0;
    display: flex; flex-direction: column; align-items: center; justify-content: center;
  }
  .ring-pct {
    font-family: var(--font-mono, ui-monospace, monospace);
    font-size: 22px; font-weight: 600; color: var(--fg, #f0f0f0);
  }
  .ring-lbl { font-size: 10px; color: var(--fg-5, #5a5a62); text-transform: uppercase; letter-spacing: 0.6px; }
  .usage-detail { text-align: center; margin-top: 6px; }
  .usage-used { font-size: 13px; font-weight: 600; color: var(--fg, #f0f0f0); }
  .usage-free { font-size: 10px; color: var(--fg-4, #7a7a82); margin-top: 2px; font-family: var(--font-mono, ui-monospace, monospace); }

  .panel-meta {
    padding: 18px 20px;
    display: grid; grid-template-columns: 1fr 1fr; gap: 14px 24px;
    align-content: start;
  }
  .meta-item.full { grid-column: 1 / -1; }
  .meta-k { font-size: 9px; color: var(--fg-5, #5a5a62); text-transform: uppercase; letter-spacing: 1px; font-weight: 600; margin-bottom: 4px; }
  .meta-v { font-size: 13px; color: var(--fg, #f0f0f0); font-weight: 500; }
  .meta-v.mono { font-family: var(--font-mono, ui-monospace, monospace); font-size: 12px; }
  .meta-v.mono.on { color: var(--nim-green, #00ff9f); }
  .meta-v.path { font-family: var(--font-mono, ui-monospace, monospace); font-size: 11px; color: var(--fg-2, #d0d0d4); word-break: break-all; }

  /* Distribución por tipo */
  .dist { padding: 0 20px 18px; }
  .dist-lbl { font-size: 9px; color: var(--fg-5, #5a5a62); text-transform: uppercase; letter-spacing: 1px; font-weight: 600; margin-bottom: 8px; }
  .dist-calc { font-size: 11px; color: var(--fg-4, #7a7a82); }
  .dist-bar {
    height: 8px; border-radius: 3px; overflow: hidden;
    display: flex; gap: 1px;
    background: var(--bg-inner, #101015);
    margin-bottom: 10px;
  }
  .dist-seg { height: 100%; }
  .dist-legend { display: flex; flex-wrap: wrap; gap: 12px; }
  .dist-item { display: flex; align-items: center; gap: 6px; font-size: 10px; color: var(--fg-3, #9c9ca4); }
  .dist-dot { width: 8px; height: 8px; border-radius: 2px; flex-shrink: 0; }
  .dist-item .v { color: var(--fg-4, #7a7a82); font-family: var(--font-mono, ui-monospace, monospace); }

  /* Usuarios */
  .panel-users { padding: 16px 20px; border-top: 1px solid var(--bd-sep, #2e2e38); }
  .pu-lbl { font-size: 9px; color: var(--fg-5, #5a5a62); text-transform: uppercase; letter-spacing: 1px; font-weight: 600; margin-bottom: 10px; }
  .pu-list { display: flex; flex-direction: column; gap: 4px; }
  .pu-row { display: flex; align-items: center; gap: 10px; padding: 7px 10px; background: var(--bg-inner, #101015); border-radius: 6px; }
  .pu-avatar {
    width: 24px; height: 24px;
    border-radius: 5px;
    background: var(--bg-card, #15151a);
    border: 1px solid var(--bd-2, #20202a);
    display: flex; align-items: center; justify-content: center;
    font-family: var(--font-mono, ui-monospace, monospace);
    font-size: 10px; color: var(--ui-select, #7a9eb1); font-weight: 600;
  }
  .pu-name { flex: 1; font-size: 12px; color: var(--fg-2, #d0d0d4); }
  .pu-perm {
    font-family: var(--font-mono, ui-monospace, monospace);
    font-size: 9px; font-weight: 600;
    padding: 2px 8px; border-radius: 3px;
    text-transform: uppercase; letter-spacing: 0.5px;
  }
  .pu-perm.rw { color: var(--nim-green, #00ff9f); background: rgba(0,255,159,0.10); }
  .pu-perm.ro { color: var(--st-info, #4db8ff); background: rgba(77,184,255,0.10); }
  .pu-perm.owner { color: var(--nim-green, #00ff9f); background: rgba(0,255,159,0.10); border: 1px solid rgba(0,255,159,0.25); }

  /* Acciones */
  .panel-actions { padding: 14px 20px; border-top: 1px solid var(--bd-sep, #2e2e38); display: flex; gap: 6px; }
  .pa-spacer { flex: 1; }
  .pa-btn {
    padding: 7px 13px;
    border-radius: 6px;
    border: 1px solid var(--bd-2, #20202a);
    background: transparent;
    color: var(--fg-3, #9c9ca4);
    font-size: 11px; font-weight: 500;
    cursor: pointer;
    display: flex; align-items: center; gap: 6px;
    font-family: inherit;
    transition: color 0.12s, border-color 0.12s;
  }
  .pa-btn svg { width: 12px; height: 12px; }
  .pa-btn.danger:hover { color: var(--st-crit, #ff5a5a); border-color: rgba(255,90,90,0.3); }
</style>
