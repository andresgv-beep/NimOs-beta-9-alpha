<script>
  /**
   * Showcase · Biblioteca visual de primitivas v3
   * ──────────────────────────────────────────────
   * Página autónoma accesible en /showcase
   * Renderiza todas las primitivas con ejemplos de uso reales.
   * Útil durante el desarrollo para ver lo que hay disponible.
   */
  import {
    LED, KeyBind, Badge, BevelButton, IconButton, TextInput,
    Sparkline, KPICard, SectionHead, DenseTable, StripeProgressBar,
    CmdOutputLog, EmptyState, Spinner, Tab, Footer
  } from '$lib/ui';

  let search = '';
  let progress1 = 68;
  let progress2 = 34;
  let progress3 = 92;
  let progress4 = 15;

  const cpuHistory = [14,12,15,10,13,8,11,7,9,5,8,6,10,4,7,3,6,5];
  const ramHistory = [9,8,9,7,8,6,7,5,6,4,5,3,4,2,3,2,1,2];
  const logLines = [
    { ts: '2026-04-19 14:22:03', level: 'ok',   msg: 'Daemon started successfully' },
    { ts: '2026-04-19 14:22:04', level: 'info', msg: 'Loading pools: done pools=3' },
    { ts: '2026-04-19 14:22:05', level: 'info', msg: 'Docker daemon commit=daa9cb7 storage-driver=overlay2' },
    { ts: '2026-04-19 14:22:06', level: 'warn', msg: 'CDI directory does not exist, skipping dir=/etc/cdi' },
    { ts: '2026-04-19 14:22:08', level: 'err',  msg: 'Connection refused host=192.168.1.200 retries=3' },
    { ts: '2026-04-19 14:22:12', level: 'ok',   msg: 'API listen on /run/docker.sock' },
    { ts: '2026-04-19 14:22:15', level: 'info', msg: 'NimShield rules loaded count=218' },
  ];

  let activeTab = 'live';
</script>

<div class="showcase">

  <!-- Header -->
  <header class="sc-header">
    <div class="logo"></div>
    <div class="title-block">
      <h1>NimOS · Design System v3</h1>
      <p>Biblioteca de primitivas · Terminal Retro</p>
    </div>
    <span class="version">v0.8.1-alpha</span>
  </header>

  <!-- LEDs -->
  <section>
    <SectionHead>LED · Indicadores pulsantes</SectionHead>
    <div class="demo-row">
      <span class="demo-item"><LED /> <code>default</code></span>
      <span class="demo-item"><LED variant="warn" /> <code>warn</code></span>
      <span class="demo-item"><LED variant="crit" /> <code>crit</code></span>
      <span class="demo-item"><LED variant="off" /> <code>off</code></span>
      <span class="demo-item"><LED pulse={false} /> <code>static</code></span>
      <span class="demo-item"><LED size={14} /> <code>size=14</code></span>
    </div>
  </section>

  <!-- KeyBind + Badge -->
  <section>
    <SectionHead>KeyBind · Badge</SectionHead>
    <div class="demo-row">
      <span class="demo-item"><KeyBind key="E" /> <code>default</code></span>
      <span class="demo-item"><KeyBind key="^C" active /> <code>active</code></span>
      <span class="demo-item"><KeyBind key="ESC" /></span>
    </div>
    <div class="demo-row" style="margin-top:10px">
      <span class="demo-item"><Badge>12</Badge> <code>default</code></span>
      <span class="demo-item"><Badge variant="accent">NEW</Badge></span>
      <span class="demo-item"><Badge variant="warn">!</Badge></span>
      <span class="demo-item"><Badge variant="crit">ERR</Badge></span>
      <span class="demo-item"><Badge variant="info">4</Badge></span>
      <span class="demo-item"><Badge variant="magenta">BETA</Badge></span>
      <span class="demo-item"><Badge variant="orange">WIP</Badge></span>
    </div>
  </section>

  <!-- Buttons -->
  <section>
    <SectionHead>BevelButton · Sistema D</SectionHead>
    <div class="demo-row">
      <BevelButton>Default</BevelButton>
      <BevelButton variant="primary" iconPrefix="▸">Primary</BevelButton>
      <BevelButton variant="danger">Danger</BevelButton>
      <BevelButton variant="info">Info</BevelButton>
      <BevelButton variant="warn">Warn</BevelButton>
      <BevelButton disabled>Disabled</BevelButton>
    </div>
    <div class="demo-row" style="margin-top:10px">
      <BevelButton size="sm">Small</BevelButton>
      <BevelButton>Medium</BevelButton>
      <BevelButton size="lg">Large</BevelButton>
      <BevelButton keyHint="U" iconPrefix="↑">Upload</BevelButton>
      <BevelButton variant="primary" keyHint="N" iconPrefix="＋">New</BevelButton>
    </div>
    <div class="demo-row" style="margin-top:10px">
      <IconButton title="Add">+</IconButton>
      <IconButton title="Remove">−</IconButton>
      <IconButton title="Refresh">↻</IconButton>
      <IconButton title="Up">↑</IconButton>
      <IconButton title="Down">↓</IconButton>
      <IconButton title="More">⋮</IconButton>
      <IconButton title="Settings">⚙</IconButton>
      <IconButton variant="danger" title="Delete">×</IconButton>
      <IconButton size="sm">+</IconButton>
      <IconButton size="sm">−</IconButton>
    </div>
  </section>

  <!-- Inputs -->
  <section>
    <SectionHead>TextInput</SectionHead>
    <div class="demo-row">
      <div style="width:220px">
        <TextInput bind:value={search} placeholder="Buscar..." icon="⌕" keyHint="/" />
      </div>
      <div style="width:180px">
        <TextInput placeholder="Comando" icon="&gt;" />
      </div>
      <div style="width:160px">
        <TextInput type="password" placeholder="••••••••" size="sm" />
      </div>
    </div>
  </section>

  <!-- Sparklines -->
  <section>
    <SectionHead>Sparkline · Gráficas inline SVG</SectionHead>
    <div class="grid-2">
      <div class="panel">
        <div class="p-label">CPU · accent · filled</div>
        <Sparkline data={cpuHistory} filled variant="accent" />
      </div>
      <div class="panel">
        <div class="p-label">RAM · warn · line</div>
        <Sparkline data={ramHistory} variant="warn" />
      </div>
    </div>
  </section>

  <!-- KPI Cards -->
  <section>
    <SectionHead>KPICard · Con corner brackets HUD</SectionHead>
    <div class="kpi-grid">
      <KPICard
        label="CPU"
        value="4.2"
        unit="% · load 0.34"
        state="12 cores"
        stateVariant="ok"
        delta="▼ 0.8"
        deltaVariant="down"
        valueVariant="accent"
        sparkData={cpuHistory}
        sparkVariant="accent"
        sparkFilled
      />
      <KPICard
        label="Memoria"
        value="10.8"
        unit="/ 16.7 GB · 64%"
        state="high"
        stateVariant="warn"
        delta="▲ 2%"
        deltaVariant="up"
        valueVariant="warn"
        sparkData={ramHistory}
        sparkVariant="warn"
        bracketVariant="warn"
      />
      <KPICard
        label="Disco I/O"
        value="619.8"
        unit="MB/s"
        state="active"
        stateVariant="ok"
        delta="peak"
        valueVariant="info"
        sparkData={[18,17,16,14,15,12,13,11,10,8,6,4,3,2,3,4,5,5]}
        sparkVariant="info"
        sparkFilled
        bracketVariant="info"
      />
      <KPICard
        label="Red"
        value="18.0"
        unit="MB/s · ↓ 5.8 ↑ 12.2"
        state="stable"
        stateVariant="ok"
        delta="▼ 3%"
        deltaVariant="down"
        sparkData={[10,9,11,8,10,7,9,6,8,5,7,4,6,5]}
        sparkVariant="dim"
      />
    </div>
  </section>

  <!-- Tabs -->
  <section>
    <SectionHead>Tab · Pestañas con keybind + badge</SectionHead>
    <div class="tabs-bar">
      <Tab active={activeTab === 'live'} onClick={() => activeTab = 'live'}>Live <Badge size="sm" variant="accent">142</Badge></Tab>
      <Tab active={activeTab === 'hist'} onClick={() => activeTab = 'hist'}>Históricos <Badge size="sm">2k</Badge></Tab>
      <Tab active={activeTab === 'stats'} onClick={() => activeTab = 'stats'}>Stats</Tab>
      <Tab hasError active={activeTab === 'err'} onClick={() => activeTab = 'err'}>Errores <Badge size="sm" variant="crit">3</Badge></Tab>
    </div>
  </section>

  <!-- Progress bars -->
  <section>
    <SectionHead>StripeProgressBar · Con stripes animadas</SectionHead>
    <div class="grid-2">
      <div class="panel">
        <div class="p-label">accent · activa</div>
        <StripeProgressBar percent={progress1} variant="accent" />
      </div>
      <div class="panel">
        <div class="p-label">info · upload</div>
        <StripeProgressBar percent={progress2} variant="info" />
      </div>
      <div class="panel">
        <div class="p-label">warn · pausada (sin movimiento)</div>
        <StripeProgressBar percent={progress4} variant="warn" animated={false} />
      </div>
      <div class="panel">
        <div class="p-label">crit · error</div>
        <StripeProgressBar percent={progress3} variant="crit" animated={false} />
      </div>
    </div>
  </section>

  <!-- Dense table -->
  <section>
    <SectionHead count="· 5 items">DenseTable · Tabla densa sysadmin</SectionHead>
    <DenseTable
      columns="40px 1fr 100px 90px 100px"
      headers={[
        { label: '#' },
        { label: 'Nombre' },
        { label: 'Estado' },
        { label: 'CPU', align: 'right', sortable: true, active: true, direction: 'desc' },
        { label: 'Uptime' },
      ]}
    >
      <div class="tr-row">
        <div class="tr-ln">01</div>
        <div>jellyfin <Badge size="sm" variant="info">docker</Badge></div>
        <div><LED size={6} /> running</div>
        <div style="text-align:right">1.8%</div>
        <div>6d 18:22</div>
      </div>
      <div class="tr-row">
        <div class="tr-ln">02</div>
        <div>nimtorrent</div>
        <div><LED size={6} /> running</div>
        <div style="text-align:right">0.8%</div>
        <div>17d 04:22</div>
      </div>
      <div class="tr-row warn-row">
        <div class="tr-ln">03</div>
        <div>containers <Badge size="sm" variant="warn">system</Badge></div>
        <div style="color:var(--warn)"><LED size={6} variant="warn" /> degraded</div>
        <div style="text-align:right">0.6%</div>
        <div>12d 03:14</div>
      </div>
      <div class="tr-row muted">
        <div class="tr-ln">04</div>
        <div>vscode-server</div>
        <div><LED size={6} variant="off" /> stopped</div>
        <div style="text-align:right; color:var(--fg-faint)">—</div>
        <div style="color:var(--fg-faint)">—</div>
      </div>
      <div class="tr-row crit-row">
        <div class="tr-ln">05</div>
        <div>nextcloud</div>
        <div style="color:var(--crit)"><LED size={6} variant="crit" /> error</div>
        <div style="text-align:right; color:var(--fg-faint)">—</div>
        <div style="color:var(--crit)">exit 137</div>
      </div>
    </DenseTable>
  </section>

  <!-- Command log -->
  <section>
    <SectionHead count="· journalctl style">CmdOutputLog · Logs estilo journalctl</SectionHead>
    <CmdOutputLog lines={logLines} follow={false} height={180} />
  </section>

  <!-- Empty + Spinner -->
  <section>
    <SectionHead>EmptyState · Spinner</SectionHead>
    <div class="grid-2">
      <div class="panel" style="padding:0">
        <EmptyState icon="⇅" title="Sin transferencias" hint="Las descargas y subidas aparecerán aquí" />
      </div>
      <div class="panel" style="display:flex; flex-direction:column; gap:20px; align-items:center; padding:40px">
        <div style="display:flex; gap:24px; align-items:center">
          <Spinner variant="classic" />
          <Spinner variant="dots" />
          <Spinner variant="braille" />
          <Spinner variant="arrows" />
          <Spinner variant="ring" />
        </div>
        <div style="font-family:var(--font-mono); font-size:10px; color:var(--fg-mute); letter-spacing:1px">
          classic · dots · braille · arrows · ring
        </div>
      </div>
    </div>
  </section>

  <!-- Footer demo -->
  <section>
    <SectionHead>Footer · Metadatos sysadmin</SectionHead>
    <div class="footer-demo">
      <Footer>
        <span><span class="k">services:</span> <span class="v">13</span></span>
        <span class="sep">·</span>
        <span><span class="k">running:</span> <span class="v">9</span></span>
        <span class="sep">·</span>
        <span><span class="k">stopped:</span> <span class="v">2</span></span>
        <svelte:fragment slot="right">
          <span><span class="k">mem:</span> <span class="v">10.8 / 16.7 GB</span></span>
          <span><span class="k">cpu:</span> <span class="v">4.2%</span></span>
        </svelte:fragment>
      </Footer>
    </div>
  </section>

  <div class="sc-footer">
    <span>──</span>
    <span>NimOS Beta 8.1 · Design System v3 showcase</span>
    <span>──</span>
  </div>

</div>

<style>
  .showcase {
    width: 100%;
    height: 100vh;
    overflow-y: auto;
    background: var(--bg);
    padding: 40px 60px 80px;
    font-family: var(--font-sans);
    color: var(--fg);
  }

  .sc-header {
    display: flex;
    align-items: center;
    gap: 16px;
    padding-bottom: 30px;
    margin-bottom: 30px;
    border-bottom: 1px solid var(--border);
  }
  .logo {
    width: 32px; height: 32px;
    background: var(--accent);
    clip-path: polygon(50% 0, 100% 50%, 50% 100%, 0 50%);
    box-shadow: 0 0 12px var(--accent-glow);
    flex-shrink: 0;
  }
  .title-block h1 {
    font-family: var(--font-mono);
    font-size: 16px;
    font-weight: 700;
    color: var(--fg);
    letter-spacing: 2px;
    text-transform: uppercase;
  }
  .title-block p {
    font-size: 10px;
    color: var(--fg-mute);
    letter-spacing: 1.5px;
    text-transform: uppercase;
    margin-top: 4px;
  }
  .version {
    margin-left: auto;
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--accent);
    letter-spacing: 1px;
    padding: 4px 10px;
    border: 1px solid var(--accent);
    clip-path: polygon(0 0, calc(100% - 4px) 0, 100% 4px, 100% 100%, 4px 100%, 0 calc(100% - 4px));
  }

  section {
    margin-bottom: 42px;
  }

  .demo-row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 16px;
    padding: 16px;
    background: var(--bg-1);
    border: 1px solid var(--border);
    margin-top: 10px;
  }
  .demo-item {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-dim);
  }
  .demo-item code {
    color: var(--fg-mute);
    font-size: 10px;
    letter-spacing: 0.5px;
  }

  .grid-2 {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
    margin-top: 10px;
  }

  .panel {
    padding: 14px 18px;
    background: var(--bg-1);
    border: 1px solid var(--border);
  }
  .p-label {
    font-family: var(--font-mono);
    font-size: 9px;
    color: var(--fg-mute);
    letter-spacing: 1.5px;
    text-transform: uppercase;
    margin-bottom: 10px;
  }

  .kpi-grid {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    border: 1px solid var(--border);
    margin-top: 10px;
    background: var(--bg-1);
  }
  .kpi-grid :global(.kpi) {
    border-right: 1px solid var(--border);
  }
  .kpi-grid :global(.kpi:last-child) {
    border-right: none;
  }

  .tabs-bar {
    display: flex;
    background: var(--bg-1);
    border: 1px solid var(--border);
    padding: 0 6px;
    margin-top: 10px;
  }

  .footer-demo {
    border: 1px solid var(--border);
    background: var(--bg-1);
    margin-top: 10px;
  }

  .sc-footer {
    display: flex;
    gap: 12px;
    justify-content: center;
    margin-top: 60px;
    padding-top: 20px;
    border-top: 1px solid var(--border);
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--fg-mute);
    letter-spacing: 1.5px;
    text-transform: uppercase;
  }
</style>
