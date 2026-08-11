<script>
  /**
   * TreeNode · Nodo de árbol de carpetas para Files · v3.2
   * ───────────────────────────────────────────────────────
   * Renderiza recursivamente la jerarquía de directorios de
   * una share. Se monta en el sidebar de FileManager dentro
   * del slot `sidebar-content` del AppShell v3.1.
   *
   * CAMBIOS v3.3:
   *   · Eliminados los cubos de color: parecían indicadores de estado.
   *   · El despliegue usa un chevron neutro que gira al abrir la rama
   *     y desaparece cuando el nodo no contiene subcarpetas.
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

  // El chevron despliega la rama sin navegar; el resto de la fila navega.
  async function onToggleClick(e) {
    e.stopPropagation();
    expanded = !expanded;
    if (expanded && children === null) await loadChildren();
  }

  $: isActive = activeShare === share && activePath === path;
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
  <!-- Chevron neutro: indica jerarquía sin parecer un estado. -->
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <span
    class="tn-toggle"
    class:open={isOpenLike}
    class:leaf={children !== null && children.length === 0}
    on:click={onToggleClick}
    role="button"
    tabindex="-1"
    aria-label={expanded ? 'Contraer carpeta' : 'Expandir carpeta'}
  >
    <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
      <polyline points="7 4 13 10 7 16"/>
    </svg>
  </span>

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

  /* Toggle neutro del árbol: sin color semántico ni glow. */
  .tn-toggle {
    width: 14px;
    height: 14px;
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
    color: var(--ink-faint, #73737c);
    transition: color 0.12s, opacity 0.12s;
  }
  .tn-toggle svg {
    width: 11px;
    height: 11px;
    transition: transform 0.16s ease;
  }
  .tn-toggle.open svg {
    transform: rotate(90deg);
  }
  .tn-toggle:not(.leaf):hover {
    color: var(--ink, #f2f2f5);
  }
  .tn-toggle.leaf {
    cursor: default;
    opacity: 0;
    pointer-events: none;
  }

  /* ─── Nombre ─── */
  .tn-name {
    flex: 1;
    min-width: 0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  :global(.window.inactive) .tn-toggle { opacity: 0.55; }
  :global(.window.inactive) .tn-toggle.leaf { opacity: 0; }
</style>
