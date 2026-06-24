<script>
  /**
   * CPMaintenance · Panel de Control · sección Limpieza y Mantenimiento
   * ───────────────────────────────────────────────────────────────────
   * Lista las tareas de mantenimiento registradas en el daemon. Por cada una:
   *   · toggle on/off
   *   · frecuencia configurable: cada X (interval) · diario a hh:mm · semanal
   *     (día + hh:mm) · al arranque
   *   · "ejecutar ahora"
   *   · última / próxima ejecución
   * Debajo, historial de ejecuciones recientes.
   *
   * Consume /api/maintenance/* (la API ya devuelve lastRun/nextRun por tarea).
   * Respuestas del daemon: JSON plano.
   */
  import { onMount, onDestroy } from 'svelte';
  import { hdrs } from '$lib/stores/auth.js';

  let tasks = [];
  let history = [];
  let loading = true;
  let err = '';
  let pollTimer = null;
  // estado del pool de red de Docker (Fix B): si hace falta reiniciar Docker.
  let poolStatus = { poolPresent: false, restartPending: false };
  let restarting = false;
  // edición local de schedule por tarea (id -> draft)
  let drafts = {};
  // estado transitorio por tarea, keyed por id (sobrevive a recargas de `tasks`,
  // a diferencia de mutar el objeto-tarea, que load() reemplaza).
  let running = {};    // id -> bool (ejecución en curso)
  let lastResult = {}; // id -> string (resultado de la última ejecución manual)

  const weekdays = [
    { v: 0, l: 'Dom' }, { v: 1, l: 'Lun' }, { v: 2, l: 'Mar' }, { v: 3, l: 'Mié' },
    { v: 4, l: 'Jue' }, { v: 5, l: 'Vie' }, { v: 6, l: 'Sáb' },
  ];

  // Orden de categorías (subcategorías) en la UI. Las no listadas van al final,
  // alfabéticas. La categoría llega del backend en t.category.
  const categoryOrder = ['Docker', 'Almacenamiento', 'General'];

  function buildGroups(list) {
    const byCat = {};
    for (const t of list) {
      const c = t.category || 'General';
      if (!byCat[c]) byCat[c] = [];
      byCat[c].push(t);
    }
    const cats = Object.keys(byCat).sort((a, b) => {
      const ia = categoryOrder.indexOf(a), ib = categoryOrder.indexOf(b);
      if (ia === -1 && ib === -1) return a.localeCompare(b);
      if (ia === -1) return 1;
      if (ib === -1) return -1;
      return ia - ib;
    });
    return cats.map((c) => ({
      category: c,
      items: byCat[c].slice().sort((a, b) => a.name.localeCompare(b.name)),
    }));
  }

  $: groups = buildGroups(tasks);

  async function load() {
    try {
      const r = await fetch('/api/maintenance/tasks', { headers: hdrs() });
      if (r.ok) {
        const d = await r.json();
        tasks = d.tasks || [];
        // sembrar drafts desde la config actual (sin pisar ediciones en curso)
        for (const t of tasks) {
          if (!drafts[t.id]) drafts[t.id] = { ...t.config.schedule };
        }
        drafts = drafts;
      }
      const rh = await fetch('/api/maintenance/history?limit=30', { headers: hdrs() });
      if (rh.ok) { const d = await rh.json(); history = d.history || []; }
      const rp = await fetch('/api/docker/network-pool', { headers: hdrs() });
      if (rp.ok) { poolStatus = await rp.json(); }
      err = '';
    } catch (e) {
      err = 'No se pudo cargar el estado de mantenimiento';
    } finally {
      loading = false;
    }
  }

  async function restartDocker() {
    if (restarting) return;
    restarting = true;
    try {
      const r = await fetch('/api/docker/network-pool/restart', {
        method: 'POST', headers: hdrs(),
      });
      if (!r.ok) throw new Error();
      await load();
    } catch {
      err = 'No se pudo reiniciar Docker';
    } finally {
      restarting = false;
    }
  }

  async function saveTask(t) {
    const draft = drafts[t.id];
    try {
      const r = await fetch('/api/maintenance/tasks/' + t.id, {
        method: 'PUT',
        headers: { ...hdrs(), 'Content-Type': 'application/json' },
        body: JSON.stringify({ enabled: t.config.enabled, schedule: draft }),
      });
      if (!r.ok) throw new Error();
      await load();
    } catch {
      err = 'No se pudo guardar la configuración de ' + t.name;
    }
  }

  async function toggleTask(t) {
    t.config.enabled = !t.config.enabled;
    tasks = tasks;
    await saveTask(t);
  }

  async function runNow(t) {
    running[t.id] = true;
    running = running;
    try {
      const r = await fetch('/api/maintenance/tasks/' + t.id + '/run', {
        method: 'POST', headers: hdrs(),
      });
      if (r.ok) {
        const d = await r.json();
        lastResult[t.id] = d.skipped
          ? ('Saltada: ' + (d.skipReason || ''))
          : ('Liberados ' + fmtBytes(d.bytesFreed) + ' · ' + (d.itemsRemoved || 0) + ' elementos');
        lastResult = lastResult;
      }
      await load();
    } catch {
      err = 'No se pudo ejecutar ' + t.name;
    } finally {
      running[t.id] = false;
      running = running;
    }
  }

  function fmtBytes(n) {
    if (!n) return '0 B';
    const u = ['B', 'KB', 'MB', 'GB']; let i = 0; let x = n;
    while (x >= 1024 && i < u.length - 1) { x /= 1024; i++; }
    return x.toFixed(i ? 1 : 0) + ' ' + u[i];
  }

  onMount(() => {
    load();
    pollTimer = setInterval(load, 15000);
  });
  onDestroy(() => clearInterval(pollTimer));
</script>

<div class="cp-maint">
  {#if loading}
    <div class="m-hint">Cargando tareas de mantenimiento…</div>
  {:else}
    {#if err}<div class="m-err">{err}</div>{/if}

    {#if poolStatus.restartPending}
      <div class="m-banner">
        <div class="m-banner-txt">
          <strong>Docker necesita reiniciarse.</strong>
          Se amplió el pool de redes para poder instalar más apps; el cambio se
          aplica al reiniciar Docker (corte breve de todas las apps).
        </div>
        <button class="m-banner-btn" disabled={restarting} on:click={restartDocker}>
          {restarting ? 'Reiniciando…' : 'Reiniciar Docker'}
        </button>
      </div>
    {/if}

    <!-- LÍNEA ROJA recordatorio -->
    <div class="m-note">
      El mantenimiento solo limpia temporales, cachés, logs y registros internos.
      Nunca toca tus datos, carpetas ni descargas.
    </div>

    {#each groups as g (g.category)}
      <div class="m-cat">{g.category}</div>
      {#each g.items as t (t.id)}
      <div class="m-task">
        <div class="m-head">
          <span class="m-led" class:on={t.config.enabled}></span>
          <div class="m-titlewrap">
            <div class="m-title">{t.name}</div>
            <div class="m-desc">{t.description}</div>
          </div>
          <button class="m-toggle" class:on={t.config.enabled} on:click={() => toggleTask(t)}>
            {t.config.enabled ? 'Activa' : 'Inactiva'}
          </button>
        </div>

        {#if drafts[t.id]}
          <div class="m-cfg">
            <label class="m-field">
              <span>Frecuencia</span>
              <select bind:value={drafts[t.id].kind}>
                <option value="interval">Cada X tiempo</option>
                <option value="daily">Diario</option>
                <option value="weekly">Semanal</option>
                <option value="at_boot">Al arrancar</option>
              </select>
            </label>

            {#if drafts[t.id].kind === 'interval'}
              <label class="m-field">
                <span>Cada (minutos)</span>
                <input type="number" min="5" bind:value={drafts[t.id].intervalMinutes} />
              </label>
            {/if}

            {#if drafts[t.id].kind === 'daily' || drafts[t.id].kind === 'weekly'}
              {#if drafts[t.id].kind === 'weekly'}
                <label class="m-field">
                  <span>Día</span>
                  <select bind:value={drafts[t.id].atWeekday}>
                    {#each weekdays as d}<option value={d.v}>{d.l}</option>{/each}
                  </select>
                </label>
              {/if}
              <label class="m-field">
                <span>Hora</span>
                <input type="number" min="0" max="23" bind:value={drafts[t.id].atHour} />
              </label>
              <label class="m-field">
                <span>Min</span>
                <input type="number" min="0" max="59" bind:value={drafts[t.id].atMinute} />
              </label>
            {/if}

            <button class="m-save" on:click={() => saveTask(t)}>Guardar</button>
            <button class="m-run" disabled={running[t.id]} on:click={() => runNow(t)}>
              {running[t.id] ? 'Ejecutando…' : 'Ejecutar ahora'}
            </button>
          </div>

          <div class="m-meta">
            {#if t.lastRun}<span>Última: {t.lastRun}</span>{/if}
            {#if t.nextRun}<span>Próxima: {t.nextRun}</span>{/if}
            {#if lastResult[t.id]}<span class="m-result">{lastResult[t.id]}</span>{/if}
          </div>
        {/if}
      </div>
      {/each}
    {/each}

    <!-- Historial -->
    {#if history.length}
      <div class="m-hist">
        <div class="m-hist-title">Historial reciente</div>
        <div class="evt-table">
          <div class="evt-head">
            <span></span>
            <span>Cuándo</span>
            <span>Tarea</span>
            <span>Resultado</span>
            <span>Dur.</span>
          </div>
          <div class="evt-scroll">
            {#each history as h}
              <div class="evt-row">
                <span class="evt-led" class:err={h.error} class:skip={h.skipped} class:ok={!h.error && !h.skipped}></span>
                <span class="evt-time">{h.ranAt}</span>
                <span class="evt-task">{h.taskId}</span>
                <span class="evt-result">
                  {#if h.error}<span class="r-err">error</span>
                  {:else if h.skipped}<span class="r-skip">saltada · {h.skipReason || ''}</span>
                  {:else}{h.itemsRemoved || 0} elem · {fmtBytes(h.bytesFreed)}{/if}
                </span>
                <span class="evt-dur">{h.durationMs} ms</span>
              </div>
            {/each}
          </div>
        </div>
      </div>
    {/if}
  {/if}
</div>

<style>
  .cp-maint { display: flex; flex-direction: column; gap: 12px; font-family: var(--font-sans); }
  .m-hint, .m-err, .m-note { font-size: 13px; }
  .m-err { color: var(--st-crit, #ff5470); }
  .m-banner {
    display: flex; align-items: center; gap: 12px;
    background: var(--warn-dim, rgba(251,191,36,0.15));
    border: 1px solid var(--warn, #fbbf24); border-radius: 8px;
    padding: 10px 12px; margin-bottom: 12px;
  }
  .m-banner-txt { flex: 1; color: var(--fg-2, #d0d0d4); font-size: 13px; line-height: 1.4; }
  .m-banner-txt strong { color: var(--warn, #fbbf24); }
  .m-banner-btn {
    flex-shrink: 0; cursor: pointer; white-space: nowrap;
    background: var(--warn, #fbbf24); color: #1a1a1f; font-weight: 600;
    border: none; border-radius: 6px; padding: 8px 14px; font-size: 13px;
  }
  .m-banner-btn:disabled { opacity: 0.6; cursor: default; }

  .m-note {
    color: var(--fg-4, #7a7a82); background: var(--bg-inner, #1a1a1f);
    border: 1px solid var(--bd-2, #2a2a33); border-radius: 6px; padding: 8px 10px;
  }

  .m-cat {
    color: var(--nim-green, #00ff9f); font-size: 11px; font-weight: 600;
    text-transform: uppercase; letter-spacing: 0.06em;
    margin: 16px 0 6px; padding-bottom: 4px;
    border-bottom: 1px solid var(--bd-2, #2a2a33);
  }
  .m-cat:first-of-type { margin-top: 4px; }

  .m-task {
    background: var(--bg-card, #16161a); border: 1px solid var(--bd-2, #2a2a33);
    border-radius: 8px; padding: 12px;
  }
  .m-head { display: flex; align-items: center; gap: 10px; }
  .m-led {
    width: 11px; height: 11px; border-radius: 3px;
    background: var(--st-crit, #ff5470); flex: 0 0 auto;
  }
  .m-led.on { background: var(--nim-green, #00ff9f); }
  .m-titlewrap { flex: 1 1 auto; }
  .m-title { color: var(--fg, #e4e4e7); font-size: 14px; }
  .m-desc { color: var(--fg-4, #7a7a82); font-size: 12px; margin-top: 2px; }

  .m-toggle {
    background: transparent; border: 1px solid var(--bd-3, #3a3a44);
    color: var(--fg-3, #9a9aa2); border-radius: 5px; padding: 4px 10px;
    font-family: var(--font-sans); font-size: 12px; cursor: pointer;
  }
  .m-toggle.on { border-color: var(--nim-green, #00ff9f); color: var(--nim-green, #00ff9f); }

  .m-cfg {
    display: flex; flex-wrap: wrap; align-items: flex-end; gap: 10px;
    margin-top: 12px; padding-top: 12px; border-top: 1px solid var(--bd-2, #2a2a33);
  }
  .m-field { display: flex; flex-direction: column; gap: 3px; }
  .m-field span { color: var(--fg-4, #7a7a82); font-size: 11px; }
  .m-field select, .m-field input {
    background: var(--bg-inner, #1a1a1f); border: 1px solid var(--bd-3, #3a3a44);
    color: var(--fg, #e4e4e7); border-radius: 5px; padding: 5px 8px;
    font-family: var(--font-sans); font-size: 13px; min-width: 80px;
  }
  .m-save, .m-run {
    border-radius: 5px; padding: 6px 12px; font-family: var(--font-sans);
    font-size: 12px; cursor: pointer; border: 1px solid var(--bd-3, #3a3a44);
    background: transparent; color: var(--fg-3, #9a9aa2);
  }
  .m-run { border-color: var(--nim-green, #00ff9f); color: var(--nim-green, #00ff9f); }
  .m-run:disabled { opacity: 0.5; cursor: default; }

  .m-meta {
    display: flex; flex-wrap: wrap; gap: 14px; margin-top: 10px;
    color: var(--fg-4, #7a7a82); font-size: 12px;
  }
  .m-result { color: var(--nim-green, #00ff9f); }

  .m-hist { margin-top: 8px; }
  .m-hist-title { color: var(--fg-3, #9a9aa2); font-size: 13px; margin-bottom: 8px; }

  /* Tabla de eventos estilo NimShield · contenida con scroll */
  .evt-table {
    background: var(--bg-card, #15151a);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 8px;
    overflow: hidden;
  }
  .evt-head {
    display: grid;
    grid-template-columns: 14px 90px 1fr 1.4fr 70px;
    gap: 12px;
    padding: 9px 14px;
    background: var(--bg-inner, #101015);
    border-bottom: 1px solid var(--bd-2, #20202a);
    font-family: var(--font-mono, ui-monospace, monospace);
    font-size: 9px;
    color: var(--fg-5, #5a5a62);
    letter-spacing: 0.8px;
    text-transform: uppercase;
    font-weight: 600;
  }
  /* Scroll contenido: ~5-6 filas visibles, resto con scroll interno */
  .evt-scroll {
    max-height: 222px;   /* ≈ 6 filas de 37px */
    overflow-y: auto;
  }
  .evt-row {
    display: grid;
    grid-template-columns: 14px 90px 1fr 1.4fr 70px;
    gap: 12px;
    padding: 9px 14px;
    align-items: center;
    font-size: 11px;
  }
  .evt-row + .evt-row { border-top: 1px solid #1a1a20; }
  .evt-row:hover { background: rgba(255,255,255,0.015); }

  .evt-led {
    width: 9px; height: 9px;
    border-radius: 2px;
    background: var(--fg-5, #5a5a62);
  }
  .evt-led.ok   { background: var(--st-info, #4db8ff); }
  .evt-led.err  { background: var(--st-crit, #ff5470); }
  .evt-led.skip { background: var(--st-warn, #ffc857); }

  .evt-time {
    font-family: var(--font-mono, ui-monospace, monospace);
    font-size: 10px;
    color: var(--fg-4, #7a7a82);
    font-variant-numeric: tabular-nums;
  }
  .evt-task {
    font-family: var(--font-mono, ui-monospace, monospace);
    font-size: 11px;
    color: var(--fg-2, #d0d0d4);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .evt-result { font-size: 11px; color: var(--fg-3, #9a9aa2); }
  .evt-dur {
    font-family: var(--font-mono, ui-monospace, monospace);
    font-size: 10px;
    color: var(--fg-4, #7a7a82);
    font-variant-numeric: tabular-nums;
    text-align: right;
  }
  .r-err { color: var(--st-crit, #ff5470); }
  .r-skip { color: var(--fg-4, #7a7a82); }
</style>
