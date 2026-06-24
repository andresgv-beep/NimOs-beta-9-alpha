<script>
  /**
   * AppShell · Envoltorio estándar de apps NimOS Beta 8.1 · v3.1
   * ─────────────────────────────────────────────────────────────
   * Provee el chrome común: titlebar con cubo 45° + path + LEDs,
   * sidebar con secciones, main area, footer interno opcional.
   *
   * CAMBIOS v3.1 (aditivos, sin romper apps existentes):
   *   · Nueva prop `sidebarWidth` (default 220px, antes 240px del CSS var).
   *     Storage/NimHealth/Settings/etc pasan a 220 sin modificar nada.
   *   · Nueva prop `showSidebar` (default true). Modo false declarado,
   *     diseño visual PENDIENTE de mockup — ver TODO abajo.
   *   · Nuevo slot `sidebar-content` que sustituye al render automático
   *     de `sections` cuando se pasa. Permite TreeNode (Files), o
   *     cualquier sidebar dinámico que no cabe en items planos.
   *   · Nuevo slot `sidebar-header` que sustituye al `.sb-header`
   *     interno (icono+título). Permite SVG inline en apps que lo
   *     necesiten sin perder consistencia.
   *
   * APP SHELL · ÚNICA FUENTE de:
   *   - dimensiones del sidebar (--sidebar-width)
   *   - estética del titlebar (cubo + path + LEDs)
   *   - patrón de items del sidebar (sb-item, sb-section)
   *
   * `documents/sidebar-tokens (1).css` queda DEPRECADO en este v3.1.
   * Eliminar en el paso 2 del refactor (limpieza tokens).
   *
   * Uso típico (sin cambios respecto a v3):
   *   <AppShell
   *     appId="nimhealth"
   *     title="NimHealth"
   *     headerIcon="♥"
   *     sections={[
   *       { label: 'Monitor', items: [
   *         { id: 'task', label: 'Task Manager', keyHint: 'T' },
   *       ]},
   *     ]}
   *     bind:active
   *     pathSegments={['health', 'task-manager']}
   *   >
   *     [contenido principal de la app]
   *   </AppShell>
   *
   * Uso avanzado v3.1 (Files):
   *   <AppShell
   *     appId="files"
   *     title="Files"
   *     pathSegments={['files', ...]}
   *   >
   *     <svelte:fragment slot="sidebar-content">
   *       <!-- TreeNode + grupos Local/Remoto -->
   *     </svelte:fragment>
   *     [contenido]
   *   </AppShell>
   *
   * TODO (Beta 8.2 o cuando llegue mockup):
   *   - Implementar estética modo `showSidebar={false}`. La prop ya
   *     existe y oculta el aside, pero el main hereda padding/spacing
   *     pensados para la versión con sidebar. Diseñar cuando haya
   *     mockup canónico de ventana sin sidebar.
   *
   * Estética Beta 8.1 (sin cambios):
   *   - Cubo 45° blanco con drop-shadow lechoso del boot (firma NimOS)
   *   - Path mono `nimos://app/seccion` · app luminoso, contexto en gris
   *   - LEDs C2: min/max/close cuadrados 10×10 con glow dramático
   *   - LEDs orden: min · max · close (close al final, protege accidentes)
   *   - Sin glass, sin border-radius
   */
  import { getContext } from 'svelte';
  import { user } from '$lib/stores/auth.js';
  import LED from '$lib/ui/LED.svelte';
  import KeyBind from '$lib/ui/KeyBind.svelte';
  import Badge from '$lib/ui/Badge.svelte';

  /** appId · prop legacy (la titlebar que la usaba se eliminó en 8.2).
      Se mantiene aceptada para no romper las llamadas existentes. */
  export const appId = '';
  export let title = '';
  export let headerIcon = '◆';
  export let sections = [];
  export let active = '';
  /** pathSegments · prop legacy del path nimos:// (titlebar eliminada en 8.2).
      Se mantiene aceptada para no romper llamadas existentes. */
  export const pathSegments = [];
  /** Footer interno que muestra daemon status + versión */
  export let showDaemonStatus = true;

  /* ─── v3.1 props (aditivas) ─────────────────────────────────── */
  /** Si false, oculta el aside completo. TODO: diseño pendiente. */
  export let showSidebar = true;

  // Padding estándar del cuerpo. Por defecto el AppShell aplica el margen
  // uniforme (alineado con el page-header) para que todas las apps respiren
  // igual. Las apps con layout propio a sangre completa (scroll interno,
  // tablas full-bleed, terminal, splits) lo desactivan con bodyPadding={false}
  // y gestionan su propio espaciado.
  export let bodyPadding = true;
  /** Ancho del sidebar. Default 220px (canónico Beta 8.1). */
  export let sidebarWidth = '220px';

  const wc = getContext('windowControls');

  $: userName = $user?.username || 'user';

  function handleItem(itemId) {
    active = itemId;
  }
</script>

<div class="app-shell" style="--sidebar-width: {sidebarWidth};">
  <!-- ═══════════ APP BODY · sidebar + main ═══════════ -->
  <!-- Beta 8.2: la titlebar con path nimos:// se elimina. Los controles
       de ventana (min/max/close) pasan a flotar en la esquina superior
       derecha del main, según mockup nimos-window-shell-v2. Las acciones
       opcionales (slot titlebar-actions) flotan a su izquierda. -->
  <div class="app-body" class:no-sidebar={!showSidebar}>

    {#if showSidebar}
      <!-- Sidebar -->
      <aside class="sidebar">
        {#if $$slots['sidebar-header']}
          <slot name="sidebar-header" />
        {:else}
          <div class="sb-header">
            <div class="sb-header-icon">{headerIcon}</div>
            <div class="sb-title">{title}</div>
          </div>
        {/if}

        <div class="sb-scroll">
          {#if $$slots['sidebar-content']}
            <slot name="sidebar-content" />
          {:else}
            {#each sections as section}
              <div class="sb-section">
                <span>{section.label}</span>
              </div>
              {#each section.items as item}
                <div
                  class="sb-item"
                  class:active={active === item.id}
                  on:click={() => handleItem(item.id)}
                  on:keydown={(e) => e.key === 'Enter' && handleItem(item.id)}
                  role="button"
                  tabindex="0"
                >
                  <span class="sb-prefix">{active === item.id ? '▸' : '\u00A0'}</span>
                  {#if item.icon}
                    <span class="sb-icon">{@html item.icon}</span>
                  {/if}
                  <span class="sb-label">{item.label}</span>
                  {#if item.badge !== undefined && item.badge !== null && item.badge !== 0}
                    <Badge size="sm" variant={item.badgeVariant || 'default'}>{item.badge}</Badge>
                  {/if}
                  {#if item.iconAfter}
                    <span class="sb-icon-after">{@html item.iconAfter}</span>
                  {/if}
                  {#if item.keyHint}
                    <KeyBind key={item.keyHint} active={active === item.id} />
                  {/if}
                </div>
              {/each}
            {/each}
          {/if}
        </div>

        {#if $$slots['sidebar-foot']}
          <div class="sb-foot-slot">
            <slot name="sidebar-foot" />
          </div>
        {/if}

        {#if showDaemonStatus}
          <div class="sb-footer">
            <div class="sb-footer-row">
              <LED size={7} />
              <span class="k">daemon</span>
              <span class="v">running</span>
            </div>
            <div class="sb-footer-row">
              <span class="k">user</span>
              <span class="v">{userName}</span>
            </div>
          </div>
        {/if}
      </aside>
    {/if}

    <!-- Main -->
    <div class="main">
      <!-- Los controles min/max/close ahora viven en WindowFrame (anclados
           a la ventana real, no se pierden al encoger). Aquí solo quedan
           las acciones propias de cada app (slot titlebar-actions), que
           flotan a la IZQUIERDA de los controles de ventana. -->
      {#if $$slots['titlebar-actions']}
        <div class="win-ctl-bar">
          <div class="tb-actions">
            <slot name="titlebar-actions" />
          </div>
        </div>
      {/if}

      {#if $$slots['page-header']}
        <div class="page-header">
          <slot name="page-header" />
        </div>
      {/if}
      <slot name="toolbar" />
      <div
        class="content"
        class:no-header={!$$slots['page-header'] && !$$slots['toolbar']}
        class:padded={bodyPadding}
      >
        <slot />
      </div>
      <slot name="footer-raw" />
      {#if $$slots.footer}
        <div class="inner-footer">
          <div class="left">
            <slot name="footer" />
          </div>
          <div class="right">
            <slot name="footer-right" />
          </div>
        </div>
      {/if}
    </div>

  </div>
</div>

<style>
  .app-shell {
    width: 100%;
    height: 100%;
    background: transparent;
    font-family: var(--font-sans);
    color: var(--ink, #f2f2f5);
    display: flex;
    flex-direction: column;
    min-width: 780px;
    overflow: hidden;
  }

  /* ═══════════════════════════════════════════════════════════
     CONTROLES DE VENTANA FLOTANTES · estética mockup v2
     ───────────────────────────────────────────────────────────
     Beta 8.2: sin titlebar/path. Los controles flotan en la
     esquina superior derecha del main. Cuadraditos planos con
     esquinas suaves (border-radius 3px), sin glow agresivo.
     Las acciones del slot titlebar-actions flotan a su izquierda.
     ═══════════════════════════════════════════════════════════ */
  .win-ctl-bar {
    position: absolute;
    top: 12px;
    /* dejamos hueco a la derecha para los 3 controles de ventana
       (viven en WindowFrame: ~3×12px + gaps + right:14px ≈ 70px) */
    right: 74px;
    z-index: 20;
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .tb-actions {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  /* ═══════════════════════════════════════════════════════════
     APP BODY · sidebar + main
     ───────────────────────────────────────────────────────────
     v3.1: --sidebar-width viene del style attr del .app-shell
     (sobrescribible vía prop sidebarWidth). Default fijado en
     220px en el script. Si app.css declara --sidebar-width
     a otro valor, gana el style attr local.
     ═══════════════════════════════════════════════════════════ */
  .app-body {
    flex: 1;
    display: grid;
    grid-template-columns: var(--sidebar-width) 1fr;
    overflow: hidden;
    min-height: 0;
  }
  /* v3.1: modo sin sidebar — TODO diseño pendiente */
  .app-body.no-sidebar {
    grid-template-columns: 1fr;
  }

  /* ─── Sidebar ─── */
  .sidebar {
    background: var(--side-bg, #131316);
    border-right: 1px solid var(--side-border, rgba(255, 255, 255, 0.04));
    display: flex;
    flex-direction: column;
    font-family: var(--font-sans);
    font-size: 13px;
    overflow: hidden;
  }
  .sb-header {
    padding: 14px 12px 16px;
    display: flex;
    align-items: center;
    gap: 10px;
    flex-shrink: 0;
  }
  .sb-header-icon {
    width: 22px;
    height: 22px;
    border-radius: 5px;
    background: var(--signal, #00ff9f);
    color: var(--bg-window, #16161a);
    display: flex;
    align-items: center;
    justify-content: center;
    font-weight: 700;
    font-size: 11px;
    flex-shrink: 0;
  }
  .sb-title {
    color: var(--ink, #f2f2f5);
    font-weight: 600;
    letter-spacing: 0.6px;
    text-transform: uppercase;
    font-size: 12px;
  }

  .sb-scroll {
    flex: 1;
    overflow-y: auto;
    padding: 2px 10px 10px;
  }

  .sb-section {
    padding: 14px 8px 6px;
    font-size: 10px;
    color: var(--ink-trace, #5a5a62);
    text-transform: uppercase;
    letter-spacing: 0.8px;
    font-weight: 500;
  }

  .sb-item {
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 7px 8px;
    margin: 1px 0;
    border-radius: 6px;
    color: var(--ink-dim, #9c9ca4);
    cursor: pointer;
    transition: background 0.12s, color 0.12s;
    font-size: 12px;
    font-weight: 400;
  }
  .sb-item:hover {
    background: var(--side-hover, rgba(255, 255, 255, 0.025));
    color: var(--ink, #d0d0d4);
  }
  .sb-item.active {
    background: var(--side-active-bg, rgba(122, 158, 177, 0.10));
    color: var(--side-active-fg, #7a9eb1);
  }
  .sb-item {
    position: relative;
  }
  .sb-prefix {
    display: none;
  }
  .sb-icon {
    width: 14px;
    height: 14px;
    flex-shrink: 0;
    color: currentColor;
    opacity: 0.85;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .sb-icon :global(svg) {
    width: 100%;
    height: 100%;
  }
  /* sprint Updates · icono renderizado DESPUÉS del badge (estado/indicador) */
  .sb-icon-after {
    width: 18px;
    height: 18px;
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    margin-left: 6px;
    /* Glow sutil · llama la atención sin ser agresivo */
    filter: drop-shadow(0 0 4px var(--info-glow, rgba(77, 184, 255, 0.5)));
  }
  .sb-icon-after :global(svg) {
    width: 100%;
    height: 100%;
  }
  .sb-label {
    flex: 1;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  /* Sidebar footer · daemon status */
  .sb-foot-slot {
    margin-top: auto;
    padding: 10px 8px 0;
  }
  .sb-foot-slot + .sb-footer {
    margin-top: 0;
  }

  .sb-footer {
    padding: 12px 16px;
    border-top: 1px solid var(--side-border, rgba(255, 255, 255, 0.04));
    display: flex;
    flex-direction: column;
    gap: 4px;
    font-size: 11px;
    color: var(--ink-mute, #9a9aa3);
    flex-shrink: 0;
    background: transparent;
    font-family: var(--font-mono, monospace);
  }
  .sb-footer-row {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .sb-footer .k {
    color: var(--ink-trace, #44444a);
    letter-spacing: 1px;
    text-transform: uppercase;
    font-size: 9px;
  }
  .sb-footer .v {
    color: var(--ink-dim, #c8c8cf);
    margin-left: auto;
    font-weight: 500;
  }

  /* ═══════════════════════════════════════════════════════════
     MAIN · área de contenido
     ═══════════════════════════════════════════════════════════ */
  .main {
    position: relative;
    display: flex;
    flex-direction: column;
    overflow: hidden;
    background: var(--main-bg, #1a1a1f);
    min-width: 0;
  }
  .content {
    flex: 1;
    overflow: auto;
    min-height: 0;
  }
  /* Margen estándar del cuerpo (bodyPadding por defecto). Lateral alineado
     con el page-header (22px) para que el contenido cuadre con el título. */
  .content.padded {
    padding: 14px 22px 20px;
  }
  /* Sin page-header ni toolbar, el contenido empieza pegado arriba y
     quedaría bajo los controles de ventana flotantes (top:12 right:14).
     Reservamos una franja superior para que no se solapen. */
  .content.no-header {
    padding-top: 40px;
  }
  .content.padded.no-header {
    padding: 40px 22px 20px;
  }

  /* Page header opcional · título y descripción (sin titlebar encima) */
  .page-header {
    padding: 14px 78px 14px 22px;
    background: transparent;
    font-family: var(--font-sans);
    font-size: 14px;
    color: var(--ink, #f2f2f5);
    letter-spacing: -0.1px;
    flex-shrink: 0;
    display: flex;
    align-items: center;
    gap: 10px;
    min-height: 44px;
    border-bottom: 1px solid var(--line, rgba(255, 255, 255, 0.04));
  }
  /* ───────────────────────────────────────────────────────────
     El page-header vive bajo la .drag-zone de WindowFrame (z-index 5),
     que captura el mousedown para arrastrar la ventana. Para que los
     controles del header (flecha atrás, breadcrumb, botones) reciban
     clicks, los "perforamos" por encima de la drag-zone: position
     relative + z-index 6. El fondo vacío del header sigue debajo, así
     que arrastrar desde ahí sigue funcionando.
     ─────────────────────────────────────────────────────────── */
  .page-header :global(button),
  .page-header :global(a),
  .page-header :global([role="button"]),
  .page-header :global(input),
  .page-header :global(select) {
    position: relative;
    z-index: 6;
  }
  /* Los SVG/iconos dentro de los controles del header no deben capturar
     el click: si lo hacen, el target es el <svg> (no elevado) y la
     drag-zone de WindowFrame se lo come. Forzamos que el click atraviese
     al control padre (que sí está a z-index 6).
     OJO: se limita a SVG a propósito. No usar `button *` porque eso
     desactivaría los clicks de menús/dropdowns anidados dentro de un
     control con role="button". */
  .page-header :global(button svg),
  .page-header :global(button svg *),
  .page-header :global(a svg),
  .page-header :global([role="button"] svg),
  .page-header :global([role="button"] svg *) {
    pointer-events: none;
  }
  .page-header :global(b),
  .page-header :global(strong) {
    color: var(--ink, #f2f2f5);
    font-weight: 600;
  }
  .page-header :global(.ph-desc),
  .page-header :global(.ph-path) {
    color: var(--ink-mute, #9a9aa3);
    font-size: 12px;
    font-weight: 400;
    letter-spacing: 0;
  }
  .page-header :global(.ph-right) {
    margin-left: auto;
    display: flex;
    align-items: center;
    gap: 8px;
  }

  /* Inner footer (status bar interno de la app) */
  .inner-footer {
    height: 30px;
    background: var(--canvas-soft, #111111);
    border-top: 1px solid var(--line, rgba(255, 255, 255, 0.08));
    display: flex;
    align-items: center;
    padding: 0 18px;
    font-family: var(--font-mono, monospace);
    font-size: 10px;
    color: var(--ink-mute, #9a9aa3);
    letter-spacing: 0.5px;
    flex-shrink: 0;
  }
  .inner-footer .left, .inner-footer .right {
    display: flex;
    align-items: center;
    gap: 14px;
  }
  .inner-footer .left  { flex: 1; }
  .inner-footer .right { margin-left: auto; }
</style>
