<script>
  /**
   * WidgetPicker · Ventana de añadir widgets · NimOS Beta 8.1
   * ─────────────────────────────────────────────────────────
   * Panel deslizante (patrón overlay+panel del sistema). Una sección
   * por widget del catálogo que no esté ya activo; dentro, una opción
   * por talla soportada con PREVIEW REAL: el propio componente del
   * widget montado a escala reducida (transform: scale), no un dibujo
   * aparte. Así el preview nunca diverge del widget real.
   *
   * Solo AÑADE. Las acciones sobre un widget ya puesto (config, quitar,
   * talla) viven en el hover de la caja (WidgetLayer). Elegir una talla
   * emite `add` con { id, size } y cierra.
   *
   * Props:
   *   open      — visible
   *   catalog   — WIDGET_CATALOG
   *   activeIds — Set de ids ya colocados (no se ofrecen de nuevo)
   * Eventos:
   *   add   { id, size: [w,h] }
   *   close
   */
  import { createEventDispatcher, onMount } from 'svelte';
  import WidgetIcon from '$lib/widgets/parts/WidgetIcon.svelte';
  import { GROUP_ORDER, GRID } from '$lib/widgets/index.js';
  import { portal } from '$lib/actions/portal.js';
  import { topicStore, acquire } from '$lib/stores/widgetData.js';

  export let open = false;
  export let catalog = [];
  export let activeIds = new Set();

  const dispatch = createEventDispatcher();

  // Pools en vivo, para expandir widgets multi-instancia (Storage).
  // Se suscribe solo mientras el picker está montado.
  const storageData = topicStore('storage');
  onMount(() => acquire('storage'));
  $: pools = Array.isArray($storageData?.data) ? $storageData.data : [];

  // Talla canónica de preview (del GRID central). El picker NO es
  // fluido: los previews son una representación de referencia, no la
  // rejilla viva. Por eso usa baseCell, no la celda fluida del layer.
  const CELL = GRID.baseCell, GAP = GRID.gap;
  // Ancho disponible para un preview en el panel (px). El preview se
  // escala para caber aquí manteniendo proporción de la talla real.
  const PREVIEW_MAX_W = 150;

  // Filtra los ya colocados, EXCEPTO los configurables (ej. Storage):
  // esos siguen ofreciéndose porque su contenido (pools) se ajusta
  // desde la config, no añadiendo más instancias.
  // Lista de entradas a ofrecer. Los widgets normales se filtran por
  // "ya colocado". Los multi-instancia (instancePer) se EXPANDEN: una
  // entrada virtual por cada pool que aún no tenga su caja, con id
  // derivado "storage:<pool>" y su config.pool.
  $: available = catalog.flatMap(w => {
    if (w.instancePer === 'pools') {
      return pools
        .map(p => p.name)
        .filter(name => !activeIds.has(`${w.id}:${name}`))
        .map(name => ({
          ...w,
          _instanceId: `${w.id}:${name}`,
          _config: { pool: name },
          name: name,                 // el nombre del pool es el título
          desc: `Pool · ${w.desc}`,
        }));
    }
    // normal: oculto si ya está (los configurables siguen visibles)
    return (w.configurable || !activeIds.has(w.id)) ? [w] : [];
  });

  // Construye los "tiles" (opciones de talla) de una entrada. Para
  // entradas virtuales (multi-instancia) usa el id derivado y arrastra
  // su config, de modo que al elegir se añade la instancia correcta.
  function tilesOf(w) {
    return (w.sizes || [[w.w, w.h]]).map(([cw, ch]) => ({
      id: w._instanceId || w.id,
      cw, ch,
      component: w.component,
      props: w.props || {},
      config: w._config || {},
    }));
  }

  // Agrupa por familia (group) y, dentro, fusiona las entradas que
  // comparten `mergeKey` en un solo apartado (ej. sysmon 2×1 + syspanel
  // 2×2 → un único "Sistema" con dos tallas). El apartado fusionado
  // toma nombre/icono/desc de la entrada de menor `order`, y junta los
  // tiles de todas. Entradas sin mergeKey = apartado propio.
  $: grouped = (() => {
    const byGroup = {};
    for (const w of available) {
      const g = w.group || 'Otros';
      (byGroup[g] ||= []).push(w);
    }
    const order = [...GROUP_ORDER, 'Otros'];

    return order
      .filter(g => byGroup[g]?.length)
      .map(g => {
        // fusión por mergeKey dentro de la familia
        const merged = {};   // mergeKey -> item
        const items = [];
        for (const w of byGroup[g].sort((a, b) => (a.order ?? 0) - (b.order ?? 0))) {
          if (w.mergeKey) {
            if (!merged[w.mergeKey]) {
              merged[w.mergeKey] = {
                key: w.mergeKey,
                // Nombre base sin la talla (ej. "Sistema", no "Sistema 2×2")
                name: w.name.replace(/\s*\d+×\d+\s*$/, '').trim(),
                icon: w.icon, desc: w.desc,
                order: w.order ?? 0,
                tiles: [...tilesOf(w)],
              };
              items.push(merged[w.mergeKey]);
            } else {
              merged[w.mergeKey].tiles.push(...tilesOf(w));
            }
          } else {
            items.push({
              key: w._instanceId || w.id,
              name: w.name, icon: w.icon, desc: w.desc,
              order: w.order ?? 0,
              tiles: tilesOf(w),
            });
          }
        }
        // dedupe de tallas iguales dentro de un apartado fusionado
        for (const it of items) {
          const seen = new Set();
          it.tiles = it.tiles.filter(t => {
            const k = t.cw + 'x' + t.ch;
            if (seen.has(k)) return false;
            seen.add(k); return true;
          });
        }
        return { name: g, items };
      });
  })();

  function cellPx(cw, ch) {
    return {
      w: cw * CELL + (cw - 1) * GAP,
      h: ch * CELL + (ch - 1) * GAP,
    };
  }
  // Escala para que el preview de talla cw×ch quepa en PREVIEW_MAX_W.
  function scaleFor(cw) {
    const realW = cw * CELL + (cw - 1) * GAP;
    return Math.min(1, PREVIEW_MAX_W / realW);
  }

  function choose(id, size, config) {
    // Si ya está colocado (caso de los configurables), abre su config.
    if (activeIds.has(id)) {
      dispatch('configure', { id });
    } else {
      dispatch('add', { id, size, config });
    }
    dispatch('close');
  }
  function close() { dispatch('close'); }
</script>

{#if open}
  <div class="portal-root" use:portal>
    <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
    <div class="overlay" on:click={close}></div>
    <div class="panel" role="dialog" aria-label="Añadir widget">
    <div class="panel-header">
      <span class="panel-title">Añadir widget</span>
      <button class="x" on:click={close} aria-label="Cerrar">✕</button>
    </div>

    <div class="panel-body">
      {#if available.length === 0}
        <div class="empty">Todos los widgets están en el escritorio.</div>
      {/if}

      {#each grouped as fam (fam.name)}
        <div class="group">
          <div class="group-head">{fam.name}</div>
          {#each fam.items as item (item.key)}
            <section class="sec">
              <div class="sec-head">
                <span class="sec-ic"><WidgetIcon name={item.icon} size={16} /></span>
                <span class="sec-name">{item.name}</span>
                <span class="sec-desc">{item.desc || ''}</span>
              </div>

              <div class="opts">
                {#each item.tiles as t (t.cw + 'x' + t.ch)}
                  {@const px = cellPx(t.cw, t.ch)}
                  {@const sc = scaleFor(t.cw)}
                  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
                  <div class="opt" on:click={() => choose(t.id, [t.cw, t.ch], t.config)}>
                    <div
                      class="frame"
                      style="width:{px.w * sc}px; height:{px.h * sc}px;"
                    >
                      {#if t.component}
                        <div
                          class="scaler"
                          style="
                            width:{px.w}px; height:{px.h}px;
                            transform: scale({sc});
                          "
                        >
                          <svelte:component
                            this={t.component}
                            w={t.cw} h={t.ch}
                            {...t.props}
                            config={t.config || {}}
                          />
                        </div>
                      {:else}
                        <div class="ph">{t.cw}×{t.ch}</div>
                      {/if}
                    </div>
                    <span class="opt-label">{t.cw}×{t.ch}</span>
                  </div>
                {/each}
              </div>
            </section>
          {/each}
        </div>
      {/each}
    </div>
    </div>
  </div>
{/if}

<style>
  .overlay {
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    /* Respeta la taskbar: el velo para a su altura, la barra queda
       a plena luz junto con el panel apoyado encima. */
    bottom: var(--taskbar-height, 52px);
    z-index: 9700;
    background: rgba(0, 0, 0, 0.45);
    backdrop-filter: blur(2px);
    pointer-events: auto;
  }
  .panel {
    position: fixed;
    top: 0;
    right: 0;
    /* Se apoya encima de la taskbar en vez de taparla: queda más
       limpio y la barra sigue visible/usable. */
    bottom: var(--taskbar-height, 52px);
    width: 360px;
    z-index: 9710;
    background: var(--side-bg);
    border-left: 1px solid var(--line);
    border-top-left-radius: var(--bev-lg);
    box-shadow: -20px 0 50px rgba(0, 0, 0, 0.5);
    display: flex;
    flex-direction: column;
    pointer-events: auto;
    animation: slidein 0.18s cubic-bezier(0.2, 0, 0.2, 1);
  }
  @keyframes slidein {
    from { transform: translateX(100%); }
    to   { transform: translateX(0); }
  }

  .panel-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 16px 18px;
    border-bottom: 1px solid var(--line);
  }
  .panel-title {
    font-family: var(--font-mono);
    font-size: 12px;
    letter-spacing: 0.16em;
    text-transform: uppercase;
    color: var(--ink);
  }
  .x {
    border: none;
    background: transparent;
    color: var(--ink-faint);
    font-size: 14px;
    cursor: pointer;
    padding: 4px 6px;
    border-radius: var(--radius-sm);
  }
  .x:hover { color: var(--ink); background: var(--side-hover); }

  .panel-body {
    flex: 1;
    overflow-y: auto;
    padding: 6px 16px 24px;
  }
  .panel-body::-webkit-scrollbar { width: 6px; }
  .panel-body::-webkit-scrollbar-thumb {
    background: var(--line-bright);
    border-radius: 3px;
  }

  .empty {
    padding: 40px 12px;
    text-align: center;
    font-family: var(--font-sans);
    font-size: 12px;
    color: var(--ink-faint);
  }

  .group { margin-bottom: 4px; }
  .group-head {
    font-family: var(--font-mono);
    font-size: 9.5px;
    letter-spacing: 0.18em;
    text-transform: uppercase;
    color: var(--signal);
    padding: 14px 2px 2px;
  }

  .sec {
    padding: 12px 0;
    border-top: 1px solid var(--line);
  }
  .group .sec:first-of-type { border-top: none; }
  .sec-head {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 12px;
  }
  .sec-ic { color: var(--signal); display: flex; }
  .sec-name {
    font-family: var(--font-sans);
    font-size: 13px;
    font-weight: 600;
    color: var(--ink);
  }
  .sec-desc {
    font-family: var(--font-mono);
    font-size: 9.5px;
    color: var(--ink-faint);
    margin-left: auto;
  }

  .opts {
    display: flex;
    flex-wrap: wrap;
    gap: 12px;
    align-items: flex-end;
  }
  .opt {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 6px;
    cursor: pointer;
  }
  .frame {
    border: 1px solid var(--line);
    border-radius: var(--bev-md);
    overflow: hidden;
    background: rgba(20, 20, 26, 0.5);
    position: relative;
    transition: border-color 0.15s ease, box-shadow 0.15s ease;
  }
  .opt:hover .frame {
    border-color: var(--signal);
    box-shadow: 0 0 0 1px var(--signal-dim), 0 6px 18px rgba(0, 0, 0, 0.4);
  }
  .scaler {
    transform-origin: top left;
    pointer-events: none; /* el preview no es interactivo */
  }
  .ph {
    width: 100%;
    height: 100%;
    display: flex;
    align-items: center;
    justify-content: center;
    font-family: var(--font-mono);
    font-size: 12px;
    color: var(--ink-faint);
  }
  .opt-label {
    font-family: var(--font-mono);
    font-size: 9.5px;
    color: var(--ink-mute);
  }
  .opt:hover .opt-label { color: var(--signal); }
</style>
