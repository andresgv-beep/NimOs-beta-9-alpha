<script>
  // MobileFiles — navegador de archivos. Sin share seleccionado lista los
  // shares; dentro de uno navega carpetas. Descarga vía token como el desktop.
  // Subir queda fuera del alcance inicial (se rellena después).
  import { onMount } from 'svelte';
  import { get } from 'svelte/store';
  import { hdrs, getToken } from '$lib/stores/auth.js';
  import { pendingFileShare } from '../mobileNav.js';

  let shares = [];
  let entries = [];
  let currentShare = null;
  let currentPath = '';
  let loading = true;

  function fmtBytes(n) {
    if (!n || n <= 0) return '';
    const u = ['B', 'KB', 'MB', 'GB', 'TB'];
    let i = 0, v = n;
    while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
    return `${v.toFixed(1)} ${u[i]}`;
  }

  async function loadShares() {
    loading = true;
    try {
      const r = await fetch('/api/files', { headers: hdrs() });
      if (r.ok) {
        const d = await r.json();
        shares = d.shares || d || [];
      }
    } catch (e) {} finally { loading = false; }
  }

  async function loadDir(share, path) {
    loading = true;
    try {
      const r = await fetch(`/api/files?share=${encodeURIComponent(share)}&path=${encodeURIComponent(path)}`, { headers: hdrs() });
      if (r.ok) {
        const d = await r.json();
        entries = d.files || d.entries || [];
        currentShare = share;
        currentPath = path;
      }
    } catch (e) {} finally { loading = false; }
  }

  function openShare(s) {
    loadDir(s.name || s, '');
  }

  function openEntry(e) {
    if (e.is_dir || e.isDir || e.type === 'dir') {
      const next = currentPath ? `${currentPath}/${e.name}` : e.name;
      loadDir(currentShare, next);
    } else {
      download(e);
    }
  }

  function goUp() {
    if (!currentPath) {
      // Volver a la lista de shares
      currentShare = null;
      entries = [];
      return;
    }
    const parts = currentPath.split('/');
    parts.pop();
    loadDir(currentShare, parts.join('/'));
  }

  async function download(e) {
    const fp = currentPath ? `${currentPath}/${e.name}` : e.name;
    try {
      const res = await fetch('/api/files/download-token', {
        method: 'POST', headers: hdrs(),
        body: JSON.stringify({ share: currentShare, path: fp }),
      });
      if (res.ok) {
        const data = await res.json();
        window.open(`/api/files/download?share=${currentShare}&path=${encodeURIComponent(fp)}&dl=${data.token}`, '_blank');
      } else {
        window.open(`/api/files/download?share=${currentShare}&path=${encodeURIComponent(fp)}&token=${getToken()}`, '_blank');
      }
    } catch (e) {
      window.open(`/api/files/download?share=${currentShare}&path=${encodeURIComponent(fp)}&token=${getToken()}`, '_blank');
    }
  }

  function isDir(e) {
    return e.is_dir || e.isDir || e.type === 'dir';
  }

  onMount(() => {
    // Si venimos de Inicio con un share concreto, abrirlo directamente.
    const pending = get(pendingFileShare);
    if (pending) {
      pendingFileShare.set(null);
      loadDir(pending, '');
    } else {
      loadShares();
    }
  });
</script>

<section class="m-section">
  {#if currentShare === null}
    <div class="section-t">Archivos · shares</div>
  {:else}
    <button class="breadcrumb" on:click={goUp}>
      <svg viewBox="0 0 24 24"><polyline points="15 18 9 12 15 6"/></svg>
      <span class="bc-share">{currentShare}</span>{#if currentPath}<span class="bc-path">/{currentPath}</span>{/if}
    </button>
  {/if}

  {#if loading}
    {#each Array(5) as _}<div class="row-skeleton"></div>{/each}
  {:else if currentShare === null}
    {#if shares.length === 0}
      <div class="empty"><div class="empty-t">Sin shares</div></div>
    {:else}
      <div class="files-card">
        {#each shares as s}
          <button class="file-row" on:click={() => openShare(s)}>
            <div class="file-ico share"><svg viewBox="0 0 24 24"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg></div>
            <div class="file-info"><div class="file-name">{s.name || s}</div><div class="file-meta">{s.path || 'share'}</div></div>
            <svg class="chev" viewBox="0 0 24 24"><polyline points="9 18 15 12 9 6"/></svg>
          </button>
        {/each}
      </div>
    {/if}
  {:else}
    {#if entries.length === 0}
      <div class="empty"><div class="empty-t">Carpeta vacía</div></div>
    {:else}
      <div class="files-card">
        {#each entries as e}
          <button class="file-row" on:click={() => openEntry(e)}>
            <div class="file-ico" class:share={isDir(e)} class:doc={!isDir(e)}>
              {#if isDir(e)}
                <svg viewBox="0 0 24 24"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg>
              {:else}
                <svg viewBox="0 0 24 24"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>
              {/if}
            </div>
            <div class="file-info">
              <div class="file-name">{e.name}</div>
              <div class="file-meta">{isDir(e) ? 'carpeta' : fmtBytes(e.size)}</div>
            </div>
            {#if isDir(e)}
              <svg class="chev" viewBox="0 0 24 24"><polyline points="9 18 15 12 9 6"/></svg>
            {:else}
              <svg class="chev dl" viewBox="0 0 24 24"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>
            {/if}
          </button>
        {/each}
      </div>
    {/if}
  {/if}
</section>

<style>
  .m-section { padding-bottom: 8px; }
  .section-t { font-size: 11px; color: var(--ink-mute); font-family: var(--font-mono); text-transform: uppercase; letter-spacing: 0.8px; font-weight: 600; margin: 18px 2px 12px; }
  .breadcrumb { display: flex; align-items: center; gap: 8px; background: none; border: none; cursor: pointer; padding: 14px 2px; width: 100%; color: var(--ink); }
  .breadcrumb svg { width: 18px; height: 18px; stroke: var(--signal); stroke-width: 2; fill: none; flex-shrink: 0; }
  .bc-share { font-size: 14px; font-weight: 700; }
  .bc-path { font-size: 13px; color: var(--ink-mute); font-family: var(--font-mono); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .row-skeleton { height: 62px; background: var(--bg-card); border: 1px solid var(--line); border-radius: 11px; margin-bottom: 8px; opacity: 0.4; }
  .empty { text-align: center; padding: 40px 20px; }
  .empty-t { font-size: 14px; color: var(--ink-dim); }

  .files-card { background: var(--bg-card); border: 1px solid var(--line); border-radius: 12px; overflow: hidden; }
  .file-row { display: flex; align-items: center; gap: 13px; padding: 13px 16px; width: 100%; background: none; border: none; cursor: pointer; text-align: left; }
  .file-row + .file-row { border-top: 1px solid var(--line); }
  .file-row:active { background: var(--main-hover); }
  .file-ico { width: 36px; height: 36px; border-radius: 9px; display: flex; align-items: center; justify-content: center; flex-shrink: 0; }
  .file-ico.share { background: rgba(255,156,90,0.14); color: var(--nim-folder, #ff9c5a); }
  .file-ico.doc { background: var(--bg-inner); color: var(--ink-mute); }
  .file-ico svg { width: 19px; height: 19px; stroke: currentColor; stroke-width: 1.8; fill: none; stroke-linecap: round; stroke-linejoin: round; }
  .file-info { flex: 1; min-width: 0; }
  .file-name { font-size: 14px; font-weight: 600; color: var(--ink); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .file-meta { font-size: 11px; color: var(--ink-faint); font-family: var(--font-mono); margin-top: 2px; }
  .chev { width: 16px; height: 16px; stroke: var(--ink-faint); stroke-width: 2; fill: none; flex-shrink: 0; }
  .chev.dl { stroke: var(--signal); }
</style>
