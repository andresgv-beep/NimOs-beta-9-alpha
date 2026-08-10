<script>
  import { onMount, onDestroy } from 'svelte';
  import AppShell from '$lib/components/AppShell.svelte';
  import TorrentList from './nimtorrent/TorrentList.svelte';
  import TorrentDetail from './nimtorrent/TorrentDetail.svelte';
  import AddTorrentWizard from './nimtorrent/AddTorrentWizard.svelte';
  import { getToken, jsonHdrs as headers } from '$lib/stores/auth.js';
  import { formatRate } from './nimtorrent/formatters.js';

  let active = 'all';
  let selectedHash = null;
  let torrents = [];
  let stats = { total: 0, active: 0, seeding: 0, paused: 0, download_rate: 0, upload_rate: 0 };
  let loading = true;
  let error = '';
  let pollInterval;
  let busy = new Set();

  let shares = [];
  let selectedShare = '';
  let storage = { ready: false, message: 'Comprobando almacenamiento…' };

  let addOpen = false;
  let addError = '';
  let uploading = false;

  const stateMatches = {
    all: () => true,
    active: torrent => !torrent.paused && ['downloading', 'seeding', 'metadata', 'checking'].includes(torrent.state),
    downloading: torrent => !torrent.paused && ['downloading', 'metadata'].includes(torrent.state),
    seeding: torrent => !torrent.paused && torrent.state === 'seeding',
    paused: torrent => torrent.paused || torrent.state === 'paused',
    error: torrent => torrent.state === 'error',
  };

  $: filtered = torrents.filter(stateMatches[active] || stateMatches.all);
  $: selected = torrents.find(torrent => torrent.hash === selectedHash) || null;
  $: canAdd = storage.ready && shares.length > 0;
  $: counts = Object.fromEntries(Object.entries(stateMatches).map(([key, match]) => [key, torrents.filter(match).length]));
  const dot = kind => `<span class="torrent-nav-dot ${kind}"></span>`;
  $: sections = [{
    label: 'Descargas',
    items: [
      { id: 'all', label: 'Todas', icon: dot('neutral'), badge: counts.all },
      { id: 'active', label: 'Activas', icon: dot('active'), badge: counts.active },
      { id: 'downloading', label: 'Descargando', icon: dot('active'), badge: counts.downloading },
      { id: 'seeding', label: 'Compartiendo', icon: dot('seeding'), badge: counts.seeding },
      { id: 'paused', label: 'En pausa', icon: dot('neutral'), badge: counts.paused },
      { id: 'error', label: 'Con error', icon: dot('error'), badge: counts.error },
    ],
  }];

  async function requestJSON(path) {
    const response = await fetch(path, { headers: headers() });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    return response.json();
  }

  async function loadTorrents() {
    try {
      const data = await requestJSON('/api/torrent/torrents');
      torrents = Array.isArray(data) ? data : [];
      error = '';
      if (selectedHash && !torrents.some(torrent => torrent.hash === selectedHash)) selectedHash = null;
      if (!selectedHash && torrents.length) selectedHash = torrents[0].hash;
    } catch {
      error = 'El servicio de descargas no está disponible';
      torrents = [];
    } finally {
      loading = false;
    }
  }

  async function loadStats() {
    try { stats = await requestJSON('/api/torrent/stats'); } catch { /* La lista muestra el error principal. */ }
  }

  async function loadStorage() {
    try { storage = await requestJSON('/api/torrent/storage'); }
    catch { storage = { ready: false, message: 'No se pudo comprobar el almacenamiento' }; }
  }

  async function loadShares() {
    try {
      const data = await requestJSON('/api/files');
      shares = (data.shares || []).filter(share => share.permission === 'rw' && !share.remote && !share.system);
      if (!shares.some(share => share.name === selectedShare)) selectedShare = shares[0]?.name || '';
    } catch {
      shares = [];
      selectedShare = '';
    }
  }

  async function refresh() {
    await Promise.all([loadTorrents(), loadStats(), loadShares(), loadStorage()]);
  }

  async function post(action, body) {
    const response = await fetch(`/api/torrent/${action}`, {
      method: 'POST',
      headers: headers(),
      body: JSON.stringify(body),
    });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
  }

  async function withTorrentBusy(torrent, action) {
    if (busy.has(torrent.hash)) return;
    busy = new Set(busy).add(torrent.hash);
    try { await action(); }
    finally {
      busy.delete(torrent.hash);
      busy = new Set(busy);
      await refresh();
    }
  }

  function togglePause(torrent) {
    return withTorrentBusy(torrent, () => post(torrent.paused ? 'resume' : 'pause', { hash: torrent.hash }));
  }

  function removeTorrent(torrent) {
    return withTorrentBusy(torrent, async () => {
      await post('remove', { hash: torrent.hash, delete_files: false });
      if (selectedHash === torrent.hash) selectedHash = null;
    });
  }

  async function pauseAll() {
    await Promise.all(torrents.filter(torrent => !torrent.paused).map(torrent => post('pause', { hash: torrent.hash })));
    await refresh();
  }

  function openAdd() {
    if (!canAdd) return;
    addError = '';
    addOpen = true;
  }

  async function addTorrent(event) {
    const { file, share } = event.detail;
    uploading = true;
    addError = '';
    try {
      const form = new FormData();
      form.append('torrent', file);
      form.append('share', share);
      const response = await fetch('/api/torrent/upload', {
        method: 'POST',
        headers: getToken() ? { Authorization: `Bearer ${getToken()}` } : {},
        body: form,
      });
      if (!response.ok) {
        let message = 'No se pudo añadir la descarga';
        try { message = (await response.json()).error || message; } catch { /* respuesta sin JSON */ }
        throw new Error(message);
      }
      addOpen = false;
      await refresh();
    } catch (cause) {
      addError = cause.message || 'Error de red al añadir la descarga';
    } finally {
      uploading = false;
    }
  }

  onMount(async () => {
    let attempts = 0;
    while (!getToken() && attempts < 10) { await new Promise(resolve => setTimeout(resolve, 200)); attempts += 1; }
    await refresh();
    pollInterval = setInterval(refresh, 2500);
  });
  onDestroy(() => clearInterval(pollInterval));
</script>

<AppShell appId="nimtorrent" title="NimTorrent" headerIcon="↓" {sections} bind:active bodyPadding={false}>
  <svelte:fragment slot="page-header">
    <b>Descargas</b><span class="page-description">{torrents.length} en total</span>
  </svelte:fragment>

  <div class="toolbar" slot="toolbar">
    <button class="secondary" on:click={pauseAll} disabled={!torrents.some(torrent => !torrent.paused)}>Pausar todas</button>
    <button class="primary" on:click={openAdd} disabled={!canAdd}>Añadir torrent</button>
  </div>

  <main class:with-warning={!canAdd} class:with-detail={Boolean(selected)}>
    {#if !canAdd}
      <div class="storage-warning">
        <div><strong>Descargas bloqueadas por seguridad</strong><span>{storage.ready ? 'Crea una carpeta compartida con permiso de escritura para elegirla como destino.' : storage.message}</span></div>
        <span class="safe-label">El disco del sistema no se utilizará</span>
      </div>
    {/if}
    <TorrentList
      torrents={filtered}
      {selectedHash}
      {loading}
      {error}
      on:select={(event) => selectedHash = event.detail}
    />
    {#if selected}
      <TorrentDetail
        torrent={selected}
        busy={busy.has(selected.hash)}
        on:toggle={(event) => togglePause(event.detail)}
        on:remove={(event) => removeTorrent(event.detail)}
      />
    {/if}
  </main>

  <AddTorrentWizard
    open={addOpen}
    {shares}
    {selectedShare}
    {uploading}
    error={addError}
    on:selectShare={(event) => selectedShare = event.detail}
    on:submit={addTorrent}
    on:cancel={() => addOpen = false}
  />

  <svelte:fragment slot="footer">
    <span>Descarga <strong>{formatRate(stats.download_rate)}</strong></span><span class="footer-separator">·</span><span>Subida <strong>{formatRate(stats.upload_rate)}</strong></span>
  </svelte:fragment>
  <svelte:fragment slot="footer-right"><span>{stats.active} activas de {stats.total}</span></svelte:fragment>
</AppShell>

<style>
  :global(.torrent-nav-dot) { width: 7px; height: 7px; border-radius: 2px; display: inline-block; background: var(--ink-mute); }
  :global(.torrent-nav-dot.active) { background: var(--signal); }
  :global(.torrent-nav-dot.seeding) { background: var(--info, #55b7f3); }
  :global(.torrent-nav-dot.error) { background: var(--crit); }
  .page-description { margin-left: 10px; color: var(--ink-mute); font-size: 12px; font-weight: 400; }
  .toolbar { display: flex; justify-content: flex-end; align-items: center; gap: 8px; padding: 10px 20px; border-bottom: 1px solid var(--line); }
  button { font-family: var(--font-sans); }
  .secondary, .primary { padding: 8px 13px; border-radius: 6px; font-size: 11px; font-weight: 600; cursor: pointer; }
  .secondary { border: 1px solid var(--line); background: var(--bg-card); color: var(--ink-dim); }
  .primary { border: 0; background: var(--signal); color: var(--bg-window); }
  .secondary:hover:not(:disabled) { color: var(--ink); background: var(--side-hover); }
  .primary:hover:not(:disabled) { filter: brightness(1.08); }
  .secondary:disabled, .primary:disabled { opacity: .4; cursor: default; }
  main { height: 100%; min-height: 0; overflow: hidden; display: grid; grid-template-rows: minmax(230px, 1fr); }
  main.with-detail { grid-template-rows: minmax(230px, 1fr) minmax(210px, .72fr); }
  main.with-warning { grid-template-rows: auto minmax(230px, 1fr); }
  main.with-warning.with-detail { grid-template-rows: auto minmax(230px, 1fr) minmax(210px, .72fr); }
  .storage-warning { display: flex; align-items: center; justify-content: space-between; gap: 20px; margin: 14px 18px 0; padding: 12px 14px; border: 1px solid color-mix(in srgb, var(--warn) 35%, var(--line)); border-left: 3px solid var(--warn); border-radius: 7px; background: color-mix(in srgb, var(--warn) 6%, var(--bg-card)); }
  .storage-warning div { display: grid; gap: 4px; }
  .storage-warning strong { color: var(--ink); font-size: 12px; }
  .storage-warning span { color: var(--ink-mute); font-size: 11px; line-height: 1.4; }
  .safe-label { flex: 0 0 auto; color: var(--warn) !important; }
  :global(.app-footer) span { font-size: 10px; color: var(--ink-mute); }
  :global(.app-footer) strong { margin-left: 4px; color: var(--ink-dim); font-weight: 500; font-variant-numeric: tabular-nums; }
  .footer-separator { margin: 0 8px; }
  @media (max-width: 850px) { .safe-label { display: none; } }
</style>
