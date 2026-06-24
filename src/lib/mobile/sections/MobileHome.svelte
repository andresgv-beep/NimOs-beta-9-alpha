<script>
  // MobileHome — vista de inicio. Resumen de estado (sistema + volúmenes),
  // acceso rápido a apps y archivos recientes. Consume endpoints reales y
  // es defensiva con los campos (el shape exacto puede variar por versión).
  import { onMount } from 'svelte';
  import { hdrs } from '$lib/stores/auth.js';
  import { goToSection, openShareInFiles } from '../mobileNav.js';
  import MobileStatCard from '../components/MobileStatCard.svelte';

  let sys = null;
  let shares = [];
  let nativeApps = [];
  let pools = [];
  let loading = true;

  // Apps nativas de NimOS (no Docker). Mismo criterio que la sección Apps.
  const NIMOS_APPS = ['nimtorrent', 'nimbackup', 'nimshield', 'nimsync', 'immich'];
  function isNimApp(s) {
    const t = (s.type || '').toLowerCase();
    if (t === 'docker' || t === 'docker-app') return false;
    const id = (s.id || s.name || '').toLowerCase();
    return NIMOS_APPS.some((n) => id.includes(n));
  }
  function isRunning(s) {
    const st = (s.status || s.state || '').toLowerCase();
    return st === 'running' || st === 'active' || st === 'up' || s.running === true;
  }
  const COLORS = ['green', 'blue', 'violet', 'orange', 'pink', 'teal'];
  function colorFor(id) {
    let h = 0;
    for (let i = 0; i < (id || '').length; i++) h = (h * 31 + id.charCodeAt(i)) >>> 0;
    return COLORS[h % COLORS.length];
  }
  function flatten(raw) {
    const flat = [];
    for (const svc of raw) {
      flat.push(svc);
      if (svc.children && svc.children.length) for (const c of svc.children) flat.push(c);
    }
    return flat;
  }

  function fmtBytes(n) {
    if (!n || n <= 0) return '0';
    const u = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
    let i = 0, v = n;
    while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
    return `${v.toFixed(1)} ${u[i]}`;
  }

  $: sysMetrics = sys ? buildSysMetrics(sys) : [];

  function buildSysMetrics(s) {
    const m = [];

    // Disco del sistema: la raíz "/" (no los pools, que van en Volúmenes).
    const allMounts = (s.disks && s.disks.mounts) || [];
    const root = allMounts.find((mt) => mt.mount === '/');
    if (root && root.total > 0) {
      m.push({
        label: 'Disco',
        value: `${fmtBytes(root.used)} / ${fmtBytes(root.total)}`,
        pct: root.percent || (root.used / root.total) * 100,
        variant: 'green',
      });
    }

    // RAM: sys.memory con percent y usedGB/totalGB.
    if (s.memory && s.memory.total) {
      m.push({
        label: 'RAM',
        value: `${fmtBytes(s.memory.used)} / ${fmtBytes(s.memory.total)}`,
        pct: s.memory.percent || (s.memory.used / s.memory.total) * 100,
        variant: 'info',
      });
    }

    // CPU: sys.cpu.percent.
    if (s.cpu && s.cpu.percent != null) {
      m.push({ label: 'CPU', value: `${s.cpu.percent}%`, pct: s.cpu.percent, variant: 'orange' });
    }

    return m;
  }

  // mainTemp puede venir como número o como objeto { value, ... }.
  function readTemp(t) {
    if (t == null) return null;
    if (typeof t === 'number') return t;
    if (typeof t === 'object' && t.value != null) return t.value;
    const n = parseFloat(t);
    return isNaN(n) ? null : n;
  }

  $: tempC = sys ? readTemp(sys.mainTemp) : null;
  $: tempBadge = tempC != null
    ? { text: `${Math.round(tempC)}°`, variant: tempC > 70 ? 'crit' : 'warn' }
    : null;

  // Volúmenes = pools reales de NimOS. Usamos pool.usage (de
  // `btrfs filesystem usage`), que da la capacidad USABLE correcta —
  // a diferencia de `df`, que en BTRFS RAID1 reporta cifras engañosas.
  $: volumes = (pools || [])
    .filter((p) => p && p.usage && p.usage.total_bytes > 0)
    .map((p) => ({
      name: p.name || poolName(p.mount_point),
      used: p.usage.used_bytes,
      total: p.usage.total_bytes,
      percent: p.usage.usage_percent,
      profile: p.profile,
    }));

  function poolName(mount) {
    const parts = String(mount || '').split('/');
    return parts[parts.length - 1] || mount;
  }

  async function load() {
    loading = true;
    try {
      const [rSys, rShares, rApps, rPools] = await Promise.all([
        fetch('/api/system', { headers: hdrs() }).catch(() => null),
        fetch('/api/files', { headers: hdrs() }).catch(() => null),
        fetch('/api/services', { headers: hdrs() }).catch(() => null),
        fetch('/api/storage/v2/pools', { headers: hdrs() }).catch(() => null),
      ]);
      if (rSys && rSys.ok) sys = await rSys.json();
      if (rShares && rShares.ok) {
        const d = await rShares.json();
        shares = d.shares || d || [];
      }
      if (rApps && rApps.ok) {
        const d = await rApps.json();
        nativeApps = flatten(d.services || d || []).filter(isNimApp);
      }
      if (rPools && rPools.ok) {
        const d = await rPools.json();
        // El endpoint v2 envuelve en { data: [...] } o devuelve el array directo.
        pools = d.data || d.pools || d || [];
      }
    } catch (e) {
      // vista degradada: mostramos lo que haya
    } finally {
      loading = false;
    }
  }

  onMount(load);
</script>

<section class="m-section">
  <div class="section-t">Estado</div>

  {#if loading}
    <div class="m-skeleton"></div>
  {:else}
    <div class="status-row">
      <MobileStatCard title="Sistema" icon="server" badge={tempBadge} metrics={sysMetrics}>
        {#if sysMetrics.length === 0}
          <div class="empty-note">Sin datos</div>
        {/if}
      </MobileStatCard>

      <MobileStatCard title="Volúmenes" icon="disk">
        {#if volumes.length > 0}
          {#each volumes as v}
            <div class="pool-line">
              <span class="pool-cap">{fmtBytes(v.used)} / {fmtBytes(v.total)}</span>
              <span class="pool-prf"><span class="dot"></span>{v.name}{#if v.profile} · {v.profile}{/if}</span>
            </div>
            <div class="bar"><div class="bar-fill" style="width:{v.percent || 0}%"></div></div>
          {/each}
        {:else}
          <div class="empty-note">Sin pools</div>
        {/if}
      </MobileStatCard>
    </div>
  {/if}

  <div class="section-t">Apps NimOS</div>
  {#if loading}
    <div class="apps-grid">
      {#each Array(4) as _}<div class="app-skeleton"></div>{/each}
    </div>
  {:else if nativeApps.length === 0}
    <div class="empty-note pad">Sin apps de NimOS</div>
  {:else}
    <div class="apps-grid">
      {#each nativeApps as app (app.id)}
        <button class="app-tile" on:click={() => goToSection('apps')}>
          <div class="app-ico {colorFor(app.id)}">
            <svg viewBox="0 0 24 24"><path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/></svg>
            <span class="app-dot {isRunning(app) ? '' : 'off'}"></span>
          </div>
          <span class="app-label">{app.name || app.id}</span>
        </button>
      {/each}
    </div>
  {/if}

  <div class="section-t">Carpetas compartidas</div>
  {#if loading}
    <div class="files-card">
      {#each Array(3) as _}<div class="file-skeleton"></div>{/each}
    </div>
  {:else if shares.length === 0}
    <div class="empty-note pad">Sin carpetas compartidas</div>
  {:else}
    <div class="files-card">
      {#each shares as s}
        <button class="file-row" on:click={() => openShareInFiles(s.name || s)}>
          <div class="file-ico"><svg viewBox="0 0 24 24"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg></div>
          <div class="file-info">
            <div class="file-name">{s.name || s}</div>
            <div class="file-meta">{s.path || 'compartida'}</div>
          </div>
          <svg class="file-chev" viewBox="0 0 24 24"><polyline points="9 18 15 12 9 6"/></svg>
        </button>
      {/each}
    </div>
  {/if}
</section>

<style>
  .m-section { padding-bottom: 8px; }
  .status-row {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 10px;
    align-items: stretch;
  }
  /* En pantallas muy estrechas (<360px) se apilan para no comprimir los datos */
  @media (max-width: 360px) {
    .status-row { grid-template-columns: 1fr; }
  }
  .section-t {
    font-size: 11px; color: var(--ink-mute); font-family: var(--font-mono);
    text-transform: uppercase; letter-spacing: 0.8px; font-weight: 600;
    margin: 18px 2px 12px;
  }
  .m-skeleton { height: 150px; background: var(--bg-card); border: 1px solid var(--line); border-radius: 12px; opacity: 0.4; }
  .pool-line { display: flex; flex-direction: column; gap: 3px; margin-bottom: 5px; }
  .pool-cap { font-size: 12px; color: var(--info); font-family: var(--font-mono); }
  .pool-prf { display: flex; align-items: center; gap: 5px; font-size: 10px; color: var(--signal); font-family: var(--font-mono); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .pool-prf .dot { width: 7px; height: 7px; border-radius: 2px; background: var(--signal); flex-shrink: 0; }
  .bar { height: 5px; background: var(--bg-inner); border-radius: 2px; overflow: hidden; margin-bottom: 12px; }
  .bar:last-child { margin-bottom: 0; }
  .bar-fill { height: 100%; background: var(--info); border-radius: 2px; }
  .empty-note { font-size: 12px; color: var(--ink-faint); font-family: var(--font-mono); }
  .empty-note.pad { padding: 8px 2px; }

  /* Grid de apps NimOS */
  .apps-grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 12px; }
  .app-tile { display: flex; flex-direction: column; align-items: center; gap: 7px; background: none; border: none; cursor: pointer; padding: 0; }
  .app-ico { width: 56px; height: 56px; border-radius: 15px; display: flex; align-items: center; justify-content: center; position: relative; }
  .app-ico svg { width: 26px; height: 26px; stroke: #fff; stroke-width: 1.7; fill: none; stroke-linecap: round; stroke-linejoin: round; }
  .app-ico.green { background: linear-gradient(135deg, #00d98a, #00a866); }
  .app-ico.blue { background: linear-gradient(135deg, #4db8ff, #2a90e8); }
  .app-ico.violet { background: linear-gradient(135deg, #9a7aff, #6d4ae8); }
  .app-ico.orange { background: linear-gradient(135deg, #ffb04d, #f7913f); }
  .app-ico.pink { background: linear-gradient(135deg, #ff7a9a, #e8487a); }
  .app-ico.teal { background: linear-gradient(135deg, #2ad9c8, #1ba898); }
  .app-dot { position: absolute; top: -2px; right: -2px; width: 13px; height: 13px; border-radius: 4px; background: var(--signal); border: 2px solid var(--canvas); }
  .app-dot.off { background: var(--ink-trace); }
  .app-label { font-size: 11px; color: var(--ink-dim); text-align: center; max-width: 70px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .app-skeleton { height: 56px; border-radius: 15px; background: var(--bg-card); opacity: 0.4; }

  /* Carpetas compartidas */
  .files-card { background: var(--bg-card); border: 1px solid var(--line); border-radius: 12px; overflow: hidden; }
  .file-row { display: flex; align-items: center; gap: 13px; padding: 13px 16px; width: 100%; background: none; border: none; cursor: pointer; text-align: left; }
  .file-row + .file-row { border-top: 1px solid var(--line); }
  .file-row:active { background: var(--main-hover); }
  .file-ico { width: 36px; height: 36px; border-radius: 9px; background: rgba(255,156,90,0.14); color: var(--nim-folder, #ff9c5a); display: flex; align-items: center; justify-content: center; flex-shrink: 0; }
  .file-ico svg { width: 19px; height: 19px; stroke: currentColor; stroke-width: 1.8; fill: none; stroke-linecap: round; stroke-linejoin: round; }
  .file-info { flex: 1; min-width: 0; }
  .file-name { font-size: 14px; font-weight: 600; color: var(--ink); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .file-meta { font-size: 11px; color: var(--ink-faint); font-family: var(--font-mono); margin-top: 2px; }
  .file-chev { width: 16px; height: 16px; stroke: var(--ink-faint); stroke-width: 2; fill: none; flex-shrink: 0; }
  .file-skeleton { height: 62px; opacity: 0.4; }
  .file-skeleton + .file-skeleton { border-top: 1px solid var(--line); }
</style>
