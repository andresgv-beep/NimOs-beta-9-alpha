<script>
  /**
   * NimSettings · Preferencias de NimOS Beta 8.1
   * ──────────────────────────────────────────────────
   * Personalización del sistema. La administración (usuarios, compartidas,
   * servicios, permisos, 2FA, actualizaciones) vive en el Panel de Control.
   *
   * Estética NimOS Beta 8.1:
   *  · Bisel firma · Cubo 45° · Path nimos:// · LEDs C2
   *  · Theme cards con preview REAL del sistema
   *  · Accent picker con 6 predefinidos + hex custom
   *  · Wallpapers sistema + uploads del user
   *  · 2 secciones: Appearance / About
   *
   * Endpoints:
   *  GET/PUT /api/user/preferences
   *  GET     /api/wallpapers · POST /api/wallpapers · DELETE
   *  GET     /api/system/info
   */
  import { onMount, onDestroy } from 'svelte';
  import { prefs, setPref, ACCENT_COLORS } from '$lib/stores/theme.js';
  import { user, getToken, hdrs } from '$lib/stores/auth.js';
  import AppShell from '$lib/components/AppShell.svelte';

  // ───── Navegación ─────
  let activeView = 'tema';

  // Sidebar sections (agrupadas como espera AppShell)
  const sections = [
    {
      label: 'Preferencias',
      items: [
        { id: 'tema',     label: 'Tema' },
        { id: 'fondo',    label: 'Fondo' },
        { id: 'taskbar',  label: 'Taskbar' },
        { id: 'escala',   label: 'Escala' },
      ],
    },
    {
      label: 'Sistema',
      items: [
        { id: 'about',    label: 'Acerca de' },
      ],
    },
  ];

  // ───── Theme state ─────
  $: currentTheme = $prefs.theme || 'dark';
  $: currentAccent = $prefs.accentColor || 'green';
  $: customAccent = $prefs.customAccentColor || '';
  $: currentWallpaper = $prefs.wallpaper || '';

  let customHexInput = '';
  $: customHexInput = customAccent;

  function selectTheme(t) {
    setPref('theme', t);
  }

  function selectAccent(name) {
    setPref('accentColor', name);
  }

  function applyCustomHex() {
    const v = (customHexInput || '').trim();
    if (!/^#?[0-9a-fA-F]{6}$/.test(v.replace('#',''))) {
      customHexErr = 'Formato hex inválido. Ej: #00ff9f';
      return;
    }
    const hex = v.startsWith('#') ? v : '#' + v;
    setPref('customAccentColor', hex);
    setPref('accentColor', 'custom');
    customHexErr = '';
  }
  let customHexErr = '';

  function selectWallpaper(wp) {
    setPref('wallpaper', wp);
  }

  // ───── Wallpaper system + user ─────
  // Sistema: definidos en /usr/share/nimos/wallpapers/ (servidos por el daemon)
  // User: subidos por el user, en /var/lib/nimos/wallpapers/<user>/
  let systemWallpapers = [];
  let userWallpapers = [];
  let wallpapersLoading = false;

  async function loadWallpapers() {
    wallpapersLoading = true;
    try {
      const r = await fetch('/api/wallpapers', { headers: hdrs() });
      if (r.ok) {
        const d = await r.json();
        systemWallpapers = d.system || [];
        userWallpapers   = d.user || [];
      }
    } catch { /* silent */ }
    wallpapersLoading = false;
  }

  let wallUploadInput;
  async function uploadWallpaper(e) {
    const f = e.target.files?.[0];
    if (!f) return;
    if (!f.type.startsWith('image/')) {
      wallUploadMsg = 'El archivo debe ser una imagen';
      wallUploadErr = true;
      return;
    }
    if (f.size > 25 * 1024 * 1024) {
      wallUploadMsg = 'Máximo 25MB';
      wallUploadErr = true;
      return;
    }
    wallUploadMsg = 'Subiendo...';
    wallUploadErr = false;

    // Enviar el archivo binario directamente vía multipart/form-data.
    // (Antes se mandaba base64 en JSON, lo que truncaba imágenes >~7.5MB
    //  por el límite de readBody del daemon. El binario no tiene ese problema.)
    try {
      const fd = new FormData();
      fd.append('file', f, f.name);
      // OJO: no fijar Content-Type manualmente; el navegador añade el boundary.
      const r = await fetch('/api/user/wallpaper', {
        method: 'POST',
        headers: { ...hdrs() },
        body: fd,
      });
      if (r.ok) {
        const d = await r.json().catch(() => ({}));
        wallUploadMsg = '✓ Subido correctamente';
        wallUploadErr = false;
        if (d.url) {
          setPref('wallpaper', d.url);
        }
        await loadWallpapers();
        setTimeout(() => { wallUploadMsg = ''; }, 3000);
      } else {
        const err = await r.json().catch(() => ({}));
        wallUploadMsg = err.error || 'Error al subir';
        wallUploadErr = true;
      }
    } catch (e) {
      wallUploadMsg = 'Error de red: ' + e.message;
      wallUploadErr = true;
    }
    if (wallUploadInput) wallUploadInput.value = '';
  }
  let wallUploadMsg = '';
  let wallUploadErr = false;

  async function deleteUserWallpaper(wp) {
    if (!confirm('¿Eliminar este fondo?')) return;
    try {
      const r = await fetch('/api/wallpapers/' + encodeURIComponent(wp.id), {
        method: 'DELETE',
        headers: hdrs(),
      });
      if (r.ok) {
        if (currentWallpaper === wp.url) setPref('wallpaper', '');
        await loadWallpapers();
      }
    } catch {}
  }

  // ───── Users ─────


  // ───── Shares ─────
  // ───── Updates ─────
  // ───── About / System info ─────
  let sysInfo = {};
  async function loadSysInfo() {
    try {
      const r = await fetch('/api/system/info', { headers: hdrs() });
      if (r.ok) sysInfo = await r.json();
    } catch {}
  }

  // ───── Lazy loading por sección ─────



  $: if (activeView === 'about' && !sysInfo.kernel) loadSysInfo();
  $: if (activeView === 'fondo' && systemWallpapers.length === 0 && !wallpapersLoading) loadWallpapers();

  onMount(() => {
    loadWallpapers();
  });

  // Date format helper
  function fmtUptime(s) {
    if (!s) return '—';
    const days = Math.floor(s / 86400);
    const hrs = Math.floor((s % 86400) / 3600);
    return `${days}d ${hrs}h`;
  }

  // Path segments dinámicos
  $: pathSegments = ['nimsettings', activeView];

  // Encontrar el label del item activo (buscar en todos los grupos)
  $: activeLabel = sections
    .flatMap(g => g.items)
    .find(it => it.id === activeView)?.label || 'Settings';

  // Subtítulo por vista (estilo mockup: "Tema · apariencia del sistema")
  const VIEW_SUBTITLES = {
    tema:    '· apariencia del sistema',
    fondo:   '· escritorio',
    taskbar: '· barra de tareas',
    escala:  '· tamaño de la interfaz',
    about:   '· información del sistema',
  };
  $: activeSubtitle = VIEW_SUBTITLES[activeView] || '';

  // Accent colors disponibles (los 6 predefinidos + custom)
  const ACCENT_PRESETS = [
    { id: 'green',   hex: '#00ff9f', label: 'Verde fósforo' },
    { id: 'amber',   hex: '#ffb800', label: 'Ámbar' },
    { id: 'cyan',    hex: '#4db8ff', label: 'Cian' },
    { id: 'magenta', hex: '#e873ff', label: 'Magenta' },
    { id: 'orange',  hex: '#ff8c3f', label: 'Naranja' },
    { id: 'red',     hex: '#ff5a5a', label: 'Rojo' },
  ];
</script>

<AppShell
  appId="nimsettings"
  title="NimSettings"
  headerIcon="⚙"
  {sections}
  bind:active={activeView}
  pathSegments={pathSegments}
  bodyPadding={false}
>
  <svelte:fragment slot="page-header">
    <b>{activeLabel}</b>{#if activeSubtitle}<span class="page-subtitle">{activeSubtitle}</span>{/if}
  </svelte:fragment>

  <div class="settings-content">

    {#if activeView === 'tema'}
      <!-- ────── TEMA DEL SISTEMA ────── -->
      <div class="section-label">Tema del sistema</div>
      <div class="theme-row">
        {#each ['dark', 'cream'] as t}
          <div class="theme-card" class:active={currentTheme === t} on:click={() => selectTheme(t)} on:keydown={(e) => e.key === 'Enter' && selectTheme(t)} role="button" tabindex="0">
            <div class="tp-frame {t}">
              <div class="tp-window">
                <div class="tp-tb">
                  <span class="tp-cube"></span>
                  <span class="tp-path">nimos://<b>storage</b></span>
                  <span class="tp-leds">
                    <span class="l min"></span><span class="l max"></span><span class="l close"></span>
                  </span>
                </div>
                <div class="tp-body">
                  <div class="tp-card">
                    <span class="tp-tab-pz">POOLS</span>
                    <div class="tp-card-body">2<div class="sub">▸ ONLINE</div></div>
                  </div>
                  <div class="tp-card">
                    <span class="tp-tab-pz">USO</span>
                    <div class="tp-card-body">58%<div class="sub">▸ 5.2 TB</div></div>
                  </div>
                </div>
              </div>
              <div class="tp-clock-led">
                <span class="d"></span><span class="d"></span>
                <span class="d" style="width:2px"></span>
                <span class="d"></span><span class="d"></span>
              </div>
              <div class="tp-taskbar">
                <svg class="logo" viewBox="-15 0 200 185" fill="none">
                  <rect x="5" y="45" width="80" height="80" rx="16" transform="rotate(-30 45 85)" fill={t === 'cream' ? '#1a1a1a' : '#fff'}/>
                  <rect x="108" y="12" width="60" height="60" rx="10" fill={t === 'cream' ? '#1a1a1a' : '#fff'}/>
                  <rect x="108" y="98" width="60" height="60" rx="10" fill={t === 'cream' ? '#1a1a1a' : '#fff'}/>
                </svg>
                <span class="clock">13:06</span>
              </div>
            </div>
            <div class="theme-card-label">
              <span>{t === 'dark' ? 'Dark' : 'Cream'}</span>
              <span class="check"></span>
            </div>
          </div>
        {/each}
      </div>

      <!-- ────── COLOR DE ACENTO ────── -->
      <div class="section-label" style="margin-top: 32px">Color de acento</div>
      <div class="accent-row">
        {#each ACCENT_PRESETS as preset}
          <div
            class="accent-dot"
            class:active={currentAccent === preset.id}
            style="background: {preset.hex}; color: {preset.hex}"
            title={preset.label}
            on:click={() => selectAccent(preset.id)}
            on:keydown={(e) => e.key === 'Enter' && selectAccent(preset.id)}
            role="button"
            tabindex="0"
          ></div>
        {/each}
      </div>

      <!-- Custom hex input -->
      <div class="custom-hex">
        <span class="custom-hex-label">Hex personalizado</span>
        <div class="custom-hex-row">
          <div class="hex-preview" style="background: {customAccent || '#00ff9f'}"></div>
          <input
            type="text"
            class="form-input hex-input"
            placeholder="#00ff9f"
            bind:value={customHexInput}
            maxlength="7"
          />
          <button class="btn-secondary" on:click={applyCustomHex}>Aplicar</button>
        </div>
        {#if customHexErr}<div class="form-msg error" style="margin-top: 8px">{customHexErr}</div>{/if}
      </div>

    {:else if activeView === 'fondo'}
      <!-- ────── FONDO DE ESCRITORIO ────── -->
      <div class="wall-header">
        <div class="section-label" style="margin-bottom: 0">Fondo de escritorio</div>
        <label class="wall-add-btn">
          <svg viewBox="0 0 24 24"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
          Añadir imagen
          <input type="file" accept="image/*" on:change={uploadWallpaper} bind:this={wallUploadInput} style="display: none"/>
        </label>
      </div>
      {#if wallUploadMsg}<div class="form-msg" class:error={wallUploadErr} style="margin-bottom: 12px">{wallUploadMsg}</div>{/if}

      <!-- Mis fondos: sin fondo + subidos por el user -->
      <div class="section-label" style="margin-top: 4px">Mis fondos</div>
      <div class="wall-grid">
        <!-- Default · sin wallpaper (usa --wallpaper NimOS del CSS) -->
        <div class="wall-item" class:active={!currentWallpaper} on:click={() => selectWallpaper('')} on:keydown={(e) => e.key === 'Enter' && selectWallpaper('')} role="button" tabindex="0">
          <div class="wall-none">NimOS</div>
          {#if !currentWallpaper}<div class="wall-check">✓</div>{/if}
        </div>

        <!-- Wallpapers subidos por el user -->
        {#each userWallpapers as wp}
          <div class="wall-item user-wp" class:active={currentWallpaper === wp.url} on:click={() => selectWallpaper(wp.url)} on:keydown={(e) => e.key === 'Enter' && selectWallpaper(wp.url)} role="button" tabindex="0">
            <img src={wp.url} alt={wp.name} class="wall-thumb" loading="lazy" />
            <span class="wall-tag-user">MI</span>
            {#if currentWallpaper === wp.url}<div class="wall-check">✓</div>{/if}
            <button class="wall-delete" on:click|stopPropagation={() => deleteUserWallpaper(wp)} title="Eliminar">×</button>
          </div>
        {/each}
      </div>

      <!-- Predeterminados: wallpapers del sistema -->
      {#if systemWallpapers.length > 0}
        <div class="section-label" style="margin-top: 28px">Predeterminados</div>
        <div class="wall-grid">
          {#each systemWallpapers as wp}
            <div class="wall-item" class:active={currentWallpaper === wp.url} on:click={() => selectWallpaper(wp.url)} on:keydown={(e) => e.key === 'Enter' && selectWallpaper(wp.url)} role="button" tabindex="0">
              <img src={wp.url} alt={wp.name} class="wall-thumb" loading="lazy" />
              <span class="wall-tag-sys">SISTEMA</span>
              {#if currentWallpaper === wp.url}<div class="wall-check">✓</div>{/if}
            </div>
          {/each}
        </div>
      {/if}

    {:else if activeView === 'taskbar'}
        <div class="section-label">Estilo del taskbar</div>
        <div class="setting-row">
          <span class="setting-label">Modo</span>
          <div class="setting-options">
            <button class="opt-btn" class:active={$prefs.taskbarMode === 'classic'} on:click={() => setPref('taskbarMode', 'classic')}>Clásico</button>
            <button class="opt-btn" class:active={$prefs.taskbarMode === 'dock'} on:click={() => setPref('taskbarMode', 'dock')}>Dock</button>
          </div>
        </div>
        <div class="setting-row">
          <span class="setting-label">Posición</span>
          <div class="setting-options">
            {#each [{v:'bottom', l:'Abajo'}, {v:'top', l:'Arriba'}, {v:'left', l:'Izquierda'}] as opt}
              <button class="opt-btn"
                class:active={$prefs.taskbarPosition === opt.v}
                disabled={$prefs.taskbarMode === 'dock' && opt.v === 'left'}
                on:click={() => setPref('taskbarPosition', opt.v)}
              >{opt.l}</button>
            {/each}
          </div>
        </div>
        <div class="setting-row">
          <span class="setting-label">Tamaño</span>
          <div class="setting-options">
            {#each [{v:'small', l:'Pequeño'}, {v:'medium', l:'Medio'}, {v:'large', l:'Grande'}] as opt}
              <button class="opt-btn"
                class:active={$prefs.taskbarSize === opt.v}
                on:click={() => setPref('taskbarSize', opt.v)}
              >{opt.l}</button>
            {/each}
          </div>
        </div>

    {:else if activeView === 'escala'}
      <div class="section-label">Escala de interfaz</div>
        <div class="setting-row">
          <span class="setting-label">Escala UI</span>
          <div class="setting-options">
            {#each [{v:'auto', l:'Auto'}, {v:85, l:'85%'}, {v:100, l:'100%'}, {v:115, l:'115%'}, {v:125, l:'125%'}, {v:150, l:'150%'}] as opt}
              <button class="opt-btn"
                class:active={$prefs.uiScale === opt.v}
                on:click={() => setPref('uiScale', opt.v)}
              >{opt.l}</button>
            {/each}
          </div>
        </div>
        <div class="info-strip">
          ▸ Pantalla: {typeof window !== 'undefined' ? `${window.screen.width}×${window.screen.height}` : '—'}
          · DPR: {typeof window !== 'undefined' ? window.devicePixelRatio?.toFixed(2) : '—'}
          · CSS: {typeof window !== 'undefined' ? `${window.innerWidth}×${window.innerHeight}` : '—'}
        </div>

    {:else if activeView === 'about'}
      <div class="section-label">Información del sistema</div>
      <div class="about-hero">
        <svg class="about-logo" viewBox="-15 0 200 185" fill="none">
          <rect x="5" y="45" width="80" height="80" rx="16" transform="rotate(-30 45 85)" fill="currentColor"/>
          <rect x="108" y="12" width="60" height="60" rx="10" fill="currentColor"/>
          <rect x="108" y="98" width="60" height="60" rx="10" fill="currentColor"/>
        </svg>
        <div class="about-info">
          <div class="about-name">NimOS</div>
          <div class="about-version">Beta 8.1.0 · {sysInfo.buildDate || 'dev'}</div>
        </div>
      </div>
      <div class="field-group">
        <div class="field-row"><span class="field-label">Kernel</span><span class="field-value">{sysInfo.kernel || '—'}</span></div>
        <div class="field-row"><span class="field-label">Arquitectura</span><span class="field-value">{sysInfo.arch || '—'}</span></div>
        <div class="field-row"><span class="field-label">Hostname</span><span class="field-value">{sysInfo.hostname || '—'}</span></div>
        <div class="field-row"><span class="field-label">Uptime</span><span class="field-value">{fmtUptime(sysInfo.uptime)}</span></div>
      </div>
    {/if}

  </div>
</AppShell>

<style>
  /* ═══════════════════════════════════════════════════════════
     NIMSETTINGS · estilos Beta 8.1 (border-radius escalado, sin clip-path)
     Usa los tokens reales del sistema: --signal, --ink, --panel, --line…
     ═══════════════════════════════════════════════════════════ */
  .settings-content {
    padding: 24px 28px;
    max-width: 860px;
  }

  /* Subtítulo del header */
  .page-subtitle {
    font-size: 12px;
    color: var(--ink-mute);
    font-weight: 400;
    margin-left: 8px;
    font-family: var(--font-mono);
  }

  .section-label {
    font-size: 11px;
    color: var(--ink-mute);
    letter-spacing: 0.8px;
    text-transform: uppercase;
    font-weight: 600;
    margin-bottom: 14px;
  }

  /* ═══ THEME CARDS · preview del sistema ═══ */
  .theme-row {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
    max-width: 720px;
  }
  .theme-card {
    border: 1px solid var(--line);
    border-radius: 10px;
    cursor: pointer;
    transition: border-color 0.15s, box-shadow 0.15s;
    overflow: hidden;
    background: var(--panel);
  }
  .theme-card:hover { border-color: var(--line-bright); }
  .theme-card.active {
    border-color: var(--signal);
    box-shadow: 0 0 0 1px var(--signal);
  }
  .tp-frame {
    height: 168px;
    position: relative;
    overflow: hidden;
  }
  .tp-frame.dark {
    --tp-canvas: #0a0a0c;
    --tp-panel: #161616;
    --tp-panel-elev: #1c1c1c;
    --tp-ink: #f2f2f5;
    --tp-ink-mute: #9a9aa3;
    --tp-line: rgba(255,255,255,0.08);
    --tp-line-bright: rgba(255,255,255,0.14);
    background:
      linear-gradient(rgba(0, 255, 159, 0.04) 1px, transparent 1px) 0 0 / 26px 26px,
      linear-gradient(90deg, rgba(0, 255, 159, 0.04) 1px, transparent 1px) 0 0 / 26px 26px,
      radial-gradient(ellipse 55% 50% at 20% 25%, rgba(0, 255, 159, 0.07) 0%, transparent 60%),
      var(--tp-canvas);
  }
  .tp-frame.cream {
    --tp-canvas: #ebebe4;
    --tp-panel: #fdfdf7;
    --tp-panel-elev: #ffffff;
    --tp-ink: #1a1a1a;
    --tp-ink-mute: #6a6a72;
    --tp-line: rgba(0,0,0,0.10);
    --tp-line-bright: rgba(0,0,0,0.18);
    background:
      linear-gradient(rgba(0, 0, 0, 0.03) 1px, transparent 1px) 0 0 / 26px 26px,
      linear-gradient(90deg, rgba(0, 0, 0, 0.03) 1px, transparent 1px) 0 0 / 26px 26px,
      radial-gradient(ellipse 55% 50% at 20% 25%, rgba(0, 200, 130, 0.06) 0%, transparent 60%),
      var(--tp-canvas);
  }

  /* Mini-ventana dentro del preview */
  .tp-window {
    position: absolute;
    top: 18px;
    left: 18px;
    right: 18px;
    background: var(--tp-panel);
    border: 1px solid var(--tp-line);
    border-radius: 7px;
    overflow: hidden;
    box-shadow: 0 6px 18px rgba(0,0,0,0.25);
  }
  .tp-tb {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 6px 8px;
    border-bottom: 1px solid var(--tp-line);
    background: var(--tp-panel-elev);
  }
  .tp-cube {
    width: 7px; height: 7px;
    background: var(--tp-ink);
    transform: rotate(45deg);
    flex-shrink: 0;
  }
  .tp-path {
    font-family: var(--font-mono);
    font-size: 8px;
    color: var(--tp-ink-mute);
    flex: 1;
  }
  .tp-path b { color: var(--tp-ink); }
  .tp-leds { display: flex; gap: 3px; }
  .tp-leds .l { width: 6px; height: 6px; border-radius: 2px; }
  .tp-leds .l.min { background: #ffc857; }
  .tp-leds .l.max { background: #00ff9f; }
  .tp-leds .l.close { background: #ff5a5a; }
  .tp-body {
    display: flex;
    gap: 7px;
    padding: 9px;
  }
  .tp-card {
    flex: 1;
    background: var(--tp-panel-elev);
    border: 1px solid var(--tp-line);
    border-radius: 5px;
    padding: 7px 8px;
  }
  .tp-tab-pz {
    font-family: var(--font-mono);
    font-size: 7px;
    color: var(--tp-ink-mute);
    letter-spacing: 0.5px;
    font-weight: 700;
  }
  .tp-card-body {
    font-family: var(--font-mono);
    font-size: 16px;
    font-weight: 700;
    color: var(--tp-ink);
    margin-top: 2px;
    line-height: 1;
  }
  .tp-card-body .sub {
    font-size: 7px;
    color: #00ff9f;
    font-weight: 600;
    margin-top: 3px;
  }
  /* Cubitos LED del reloj (firma) */
  .tp-clock-led {
    position: absolute;
    bottom: 30px;
    right: 22px;
    display: flex;
    gap: 2px;
    align-items: flex-end;
  }
  .tp-clock-led .d {
    width: 4px; height: 7px;
    background: var(--tp-ink-mute);
    border-radius: 1px;
    opacity: 0.5;
  }
  /* Taskbar del preview */
  .tp-taskbar {
    position: absolute;
    bottom: 0;
    left: 0; right: 0;
    height: 22px;
    background: var(--tp-panel);
    border-top: 1px solid var(--tp-line);
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0 10px;
  }
  .tp-taskbar .logo { width: 14px; height: 13px; }
  .tp-taskbar .clock {
    font-family: var(--font-mono);
    font-size: 9px;
    color: var(--tp-ink);
    font-weight: 600;
  }

  .theme-card-label {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 10px 13px;
    font-size: 12px;
    font-weight: 600;
    color: var(--ink);
  }
  .theme-card-label .check {
    width: 16px; height: 16px;
    border-radius: 4px;
    border: 1px solid var(--line-bright);
  }
  .theme-card.active .theme-card-label .check {
    background: var(--signal);
    border-color: var(--signal);
    position: relative;
  }
  .theme-card.active .theme-card-label .check::after {
    content: '✓';
    position: absolute;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    color: #0a0a0c;
    font-size: 11px;
    font-weight: 700;
  }

  /* ═══ COLOR DE ACENTO ═══ */
  .accent-row {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
  }
  .accent-dot {
    width: 38px; height: 38px;
    border-radius: 9px;
    cursor: pointer;
    border: 2px solid transparent;
    transition: transform 0.12s;
    position: relative;
  }
  .accent-dot:hover { transform: scale(1.08); }
  .accent-dot.active { border-color: var(--ink); }
  .accent-dot.active::after {
    content: '✓';
    position: absolute;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    color: #0a0a0c;
    font-size: 15px;
    font-weight: 700;
  }

  /* Hex personalizado */
  .custom-hex {
    margin-top: 20px;
    max-width: 480px;
  }
  .custom-hex-label {
    font-size: 11px;
    color: var(--ink-mute);
    text-transform: uppercase;
    letter-spacing: 0.8px;
    font-weight: 600;
    display: block;
    margin-bottom: 8px;
  }
  .custom-hex-row {
    display: flex;
    gap: 8px;
    align-items: center;
  }
  .hex-preview {
    width: 38px; height: 38px;
    border-radius: 8px;
    border: 1px solid var(--line);
    flex-shrink: 0;
  }
  .form-input {
    background: var(--canvas-soft);
    border: 1px solid var(--line);
    border-radius: 7px;
    padding: 10px 12px;
    color: var(--ink);
    font-family: var(--font-mono);
    font-size: 13px;
    outline: none;
    transition: border-color 0.12s;
  }
  .form-input:focus { border-color: var(--signal); }
  .hex-input { flex: 1; }
  .btn-secondary {
    padding: 10px 18px;
    background: transparent;
    border: 1px solid var(--line-bright);
    border-radius: 7px;
    color: var(--ink-dim);
    font-family: var(--font-mono);
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    cursor: pointer;
    transition: color 0.12s, border-color 0.12s;
  }
  .btn-secondary:hover { color: var(--ink); border-color: var(--signal); }

  /* ═══ FONDO · wallpapers galería ═══ */
  .wall-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 16px;
  }
  .wall-add-btn {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    padding: 8px 14px;
    border: 1px solid var(--line-bright);
    border-radius: 7px;
    color: var(--ink-dim);
    font-family: var(--font-mono);
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    cursor: pointer;
    transition: color 0.12s, border-color 0.12s;
  }
  .wall-add-btn:hover { color: var(--signal); border-color: var(--signal); }
  .wall-add-btn svg {
    width: 13px; height: 13px;
    stroke: currentColor;
    stroke-width: 2.5;
    fill: none;
    stroke-linecap: round;
  }

  .wall-grid {
    display: grid;
    grid-template-columns: repeat(3, 1fr);
    gap: 10px;
    max-width: 640px;
  }
  .wall-item {
    aspect-ratio: 16/10;
    border-radius: 9px;
    overflow: hidden;
    cursor: pointer;
    position: relative;
    border: 2px solid transparent;
    transition: border-color 0.12s;
    background: var(--canvas-soft);
  }
  .wall-item:hover { border-color: var(--line-bright); }
  .wall-item.active { border-color: var(--signal); }
  .wall-thumb {
    width: 100%;
    height: 100%;
    object-fit: cover;
    display: block;
  }
  .wall-none {
    width: 100%;
    height: 100%;
    display: flex;
    align-items: center;
    justify-content: center;
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--ink-mute);
    letter-spacing: 1.5px;
    background:
      linear-gradient(rgba(0, 255, 159, 0.04) 1px, transparent 1px) 0 0 / 16px 16px,
      linear-gradient(90deg, rgba(0, 255, 159, 0.04) 1px, transparent 1px) 0 0 / 16px 16px,
      var(--canvas-soft);
  }
  .wall-check {
    position: absolute;
    top: 6px; right: 6px;
    width: 20px; height: 20px;
    border-radius: 5px;
    background: var(--signal);
    color: #0a0a0c;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 12px;
    font-weight: 700;
  }
  .wall-tag-sys, .wall-tag-user {
    position: absolute;
    bottom: 6px; left: 6px;
    font-family: var(--font-mono);
    font-size: 8px;
    padding: 2px 6px;
    border-radius: 3px;
    letter-spacing: 0.5px;
    text-transform: uppercase;
    backdrop-filter: blur(4px);
  }
  .wall-tag-sys { background: rgba(0,0,0,0.5); color: rgba(255,255,255,0.85); }
  .wall-tag-user { background: rgba(0,255,159,0.2); color: #00ff9f; }
  .wall-delete {
    position: absolute;
    top: 6px; left: 6px;
    width: 20px; height: 20px;
    border-radius: 5px;
    background: rgba(0,0,0,0.6);
    border: none;
    color: #ff5a5a;
    cursor: pointer;
    display: none;
    align-items: center;
    justify-content: center;
    font-size: 14px;
    line-height: 1;
    backdrop-filter: blur(4px);
  }
  .wall-item.user-wp:hover .wall-delete { display: flex; }

  /* ═══ TASKBAR / ESCALA · setting rows ═══ */
  .setting-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 14px 16px;
    background: var(--panel);
    border: 1px solid var(--line);
    border-radius: 9px;
    margin-bottom: 8px;
    max-width: 640px;
  }
  .setting-label {
    font-size: 13px;
    color: var(--ink);
    font-weight: 500;
  }
  .setting-options {
    display: flex;
    gap: 4px;
    background: var(--canvas-soft);
    border: 1px solid var(--line);
    border-radius: 7px;
    padding: 3px;
  }
  .opt-btn {
    padding: 6px 13px;
    background: transparent;
    border: none;
    border-radius: 5px;
    color: var(--ink-mute);
    font-family: var(--font-mono);
    font-size: 11px;
    font-weight: 600;
    cursor: pointer;
    transition: color 0.12s, background 0.12s;
  }
  .opt-btn:hover:not(:disabled) { color: var(--ink); }
  .opt-btn.active { background: var(--signal); color: #0a0a0c; }
  .opt-btn:disabled { opacity: 0.35; cursor: not-allowed; }

  .info-strip {
    margin-top: 14px;
    padding: 10px 14px;
    background: var(--canvas-soft);
    border: 1px solid var(--line);
    border-radius: 7px;
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--ink-mute);
    letter-spacing: 0.3px;
    max-width: 640px;
  }

  /* ═══ ACERCA DE ═══ */
  .about-hero {
    display: flex;
    align-items: center;
    gap: 16px;
    margin-bottom: 22px;
  }
  .about-logo { width: 52px; height: 48px; color: var(--signal); }
  .about-name { font-size: 20px; font-weight: 700; color: var(--ink); letter-spacing: -0.3px; }
  .about-version {
    font-size: 12px;
    color: var(--ink-mute);
    margin-top: 2px;
    font-family: var(--font-mono);
  }
  .field-group {
    background: var(--panel);
    border: 1px solid var(--line);
    border-radius: 10px;
    overflow: hidden;
    max-width: 480px;
  }
  .field-row {
    display: flex;
    justify-content: space-between;
    padding: 12px 16px;
    font-size: 12px;
  }
  .field-row + .field-row { border-top: 1px solid var(--line); }
  .field-label { color: var(--ink-mute); }
  .field-value { color: var(--ink); font-family: var(--font-mono); }

  /* ═══ MENSAJES ═══ */
  .form-msg {
    font-size: 12px;
    color: var(--ink-dim);
    font-family: var(--font-mono);
  }
  .form-msg.error { color: #ff5a5a; }
</style>
