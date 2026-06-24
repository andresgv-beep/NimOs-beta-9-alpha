<script>
  /**
   * TreeNode · Nodo de árbol de carpetas para Files · v3.2
   * ───────────────────────────────────────────────────────
   * Renderiza recursivamente la jerarquía de directorios de
   * una share. Se monta en el sidebar de FileManager dentro
   * del slot `sidebar-content` del AppShell v3.1.
   *
   * CAMBIOS v3.2:
   *   · Eliminado el SVG folder genérico (Beta 6/7).
   *   · Eliminado también el dot de origen (era redundante).
   *   · Sustituido por un único cubo 10×10 con border-radius 2px
   *     — firma NimOS · pareja micro del `.ink-cube` blanco del
   *     titlebar.
   *   · Color del cubo: naranja `--nim-folder` para shares
   *     locales, azul `--nim-remote` para remotas. El cubo
   *     marca origen por sí mismo en cualquier depth.
   *   · Microinteracción: cuando el activeShare/activePath
   *     cae dentro de este subárbol (`isActive || shouldBeOpen`),
   *     el cubo rota 45° con glow del color de origen.
   *     Cuadrado quieto = no estás aquí. Rombo = estás dentro.
   *
   * CAMBIOS v3.1 (preservados):
   *   · Estética alineada al patrón `.sb-item` del AppShell.
   *   · Indent uniforme: padding-left = 10 + depth × 14.
   *
   * MECÁNICA (sin cambios):
   *   · Recursión interna con <TreeNode> auto-importado.
   *   · loadChildren() lazy al primer expand.
   *   · shouldBeOpen: auto-expande si activeShare === share
   *     y el path actual es descendiente del nodo.
   *   · Click en chevron alterna expand; click en row navega.
   *
   * API:
   *   share        · nombre de la share raíz
   *   path         · ruta dentro de la share ("/", "/sub", …)
   *   name         · display name del nodo
   *   depth        · nivel (0 = root de la share)
   *   activePath   · path actualmente seleccionado en FileManager
   *   activeShare  · share actualmente seleccionada
   *   onNavigate   · callback (share, path) al hacer click
   *   remote       · true si la share raíz es remota
   */
  import { getToken } from '$lib/stores/auth.js';
  import TreeNode from '$lib/components/TreeNode.svelte';

  export let share;
  export let path;
  export let name;
  export let depth = 0;
  export let activePath;
  export let activeShare;
  export let onNavigate;
  export let remote = false;

  const hdrs = () => ({ 'Authorization': `Bearer ${getToken()}` });

  let expanded = false;
  let children = null;

  $: shouldBeOpen = activeShare === share && isAncestor(path, activePath);
  $: if (shouldBeOpen && !expanded) { expanded = true; if (children === null) loadChildren(); }

  function isAncestor(nodePath, targetPath) {
    if (!targetPath || !nodePath) return false;
    if (nodePath === '/') return targetPath !== '/';
    return targetPath.startsWith(nodePath + '/');
  }

  async function loadChildren() {
    try {
      const r = await fetch('/api/files?share=' + share + '&path=' + encodeURIComponent(path), { headers: hdrs() });
      const d = await r.json();
      children = (d.files || []).filter(f => f.isDirectory);
    } catch { children = []; }
  }

  function handleClick() { onNavigate(share, path); }

  /* El cubo es ahora el control de expand/colapso (ya no hay chevron).
     Click en el cubo → alterna expand sin navegar. Click en el resto
     de la fila → navega. */
  async function onCubeClick(e) {
    e.stopPropagation();
    expanded = !expanded;
    if (expanded && children === null) await loadChildren();
  }

  $: isActive = activeShare === share && activePath === path;
  /* v3.2: el cubo gira (rombo) SOLO cuando el nodo está realmente
     expandido Y tiene subcarpetas que se están mostrando. Estar en el
     trail de navegación auto-expande (abajo), pero el giro refleja el
     estado real del árbol, no el foco — así no gira en vacío. */
  $: inTrail = isActive || shouldBeOpen;
  $: isOpenLike = expanded && children !== null && children.length > 0;
</script>

<!-- svelte-ignore a11y_click_events_have_key_events -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div
  class="tree-item"
  class:active={isActive}
  class:in-trail={inTrail}
  class:root={depth === 0}
  class:remote
  style="padding-left:{10 + depth * 14}px"
  on:click={handleClick}
  on:keydown
  role="button"
  tabindex="0"
>
  <!-- Cubo · firma NimOS · ES el control de expand/colapso (ya no hay
       flecha). Rota a 45° cuando está abierto o en el trail de navegación.
       Color refleja origen: naranja local / azul remote. -->
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <span
    class="tn-cube"
    class:open={isOpenLike}
    class:leaf={children !== null && children.length === 0}
    on:click={onCubeClick}
    aria-hidden="true"
  ><span class="tn-square"></span></span>

  <span class="tn-name">{name}</span>
</div>

{#if expanded && children}
  {#each children as child}
    <TreeNode
      share={share}
      path={path === '/' ? '/' + child.name : path + '/' + child.name}
      name={child.name}
      depth={depth + 1}
      activePath={activePath}
      activeShare={activeShare}
      onNavigate={onNavigate}
      remote={remote}
    />
  {/each}
{/if}

<style>
  /* ─── Tree row · alineado a sb-item del AppShell ─── */
  .tree-item {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 7px 10px;
    margin: 1px 0;
    border-radius: 6px;
    cursor: pointer;
    user-select: none;
    color: var(--ink-dim, #c8c8cf);
    font-family: var(--font-sans);
    font-size: 13px;
    font-weight: 400;
    transition: background 0.12s, color 0.12s;
    /* padding-left se inyecta por style attr según depth */
  }
  .tree-item:hover {
    background: var(--side-hover, rgba(255, 255, 255, 0.04));
    color: var(--ink, #f2f2f5);
  }
  .tree-item.active {
    background: var(--side-active-bg, rgba(122, 158, 177, 0.10));
    color: var(--side-active-fg, #7a9eb1);
  }

  /* ─── Cubo · firma NimOS · ES el toggle de expand/colapso ───
     Contenedor .tn-cube: caja flex de 14×14 que centra el cuadrado
     visible (.tn-square) y aporta el área de click. El cuadrado interno
     es 10×10 con border-radius 2px. Color por origen:
       · Local  → --nim-folder (naranja)  · Remote → --nim-remote (azul)
     Cuadrado quieto = carpeta cerrada · rombo con glow = abierta.
     Sin position:absolute ni margin negativo → alinea con el texto.
  */
  .tn-cube {
    width: 14px;
    height: 14px;
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
  }
  .tn-square {
    width: 10px;
    height: 10px;
    border-radius: 2px;
    background: var(--nim-folder, #ff9c5a);
    transition:
      transform 0.25s cubic-bezier(0.4, 0, 0.2, 1),
      box-shadow 0.2s ease,
      opacity 0.15s;
  }
  .tree-item.remote .tn-square {
    background: var(--nim-remote, #4db8ff);
  }
  /* Expandido con hijos → rombo con glow */
  .tn-cube.open .tn-square {
    transform: rotate(45deg);
    box-shadow: 0 0 5px rgba(255, 156, 90, 0.45);
  }
  .tree-item.remote .tn-cube.open .tn-square {
    box-shadow: 0 0 5px rgba(77, 184, 255, 0.45);
  }
  /* Carpeta sin hijos → cubo apagado, no actúa como toggle */
  .tn-cube.leaf {
    cursor: default;
  }
  .tn-cube.leaf .tn-square {
    opacity: 0.5;
  }
  .tn-cube:not(.leaf):hover .tn-square {
    box-shadow: 0 0 6px rgba(255, 156, 90, 0.5);
  }
  .tree-item.remote .tn-cube:not(.leaf):hover .tn-square {
    box-shadow: 0 0 6px rgba(77, 184, 255, 0.5);
  }

  /* ─── Nombre ─── */
  .tn-name {
    flex: 1;
    min-width: 0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  /* ─── Estado inactivo de la ventana ───
     Cuando la ventana no tiene foco, atenuar el cubo y su glow
     para que case con el resto del chrome inactivo (ink-cube, LEDs).
  */
  :global(.window.inactive) .tn-square {
    opacity: 0.55;
  }
  :global(.window.inactive) .tn-cube.open .tn-square {
    box-shadow: 0 0 2px rgba(255, 156, 90, 0.2);
  }
  :global(.window.inactive) .tree-item.remote .tn-cube.open .tn-square {
    box-shadow: 0 0 2px rgba(77, 184, 255, 0.2);
  }
</style>
