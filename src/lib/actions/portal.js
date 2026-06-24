/**
 * portal · Svelte action · NimOS Beta 8.1
 * ────────────────────────────────────────
 * Mueve el nodo al final de <body> (o a un target dado) tras montar.
 * Necesario para overlays que viven dentro de un contenedor con
 * stacking context propio (ej. WidgetLayer en z-index:2): por mucho
 * z-index que tenga el overlay, un hijo no puede superar a un hermano
 * del padre. Sacarlo al body lo libera de esa cárcel de apilamiento.
 *
 * Uso:  <div use:portal> … </div>
 *       <div use:portal={'#algun-target'}> … </div>
 *
 * Limpia tras sí mismo al destruir el componente.
 */
export function portal(node, target = 'body') {
  let dest;
  function mount() {
    dest = typeof target === 'string' ? document.querySelector(target) : target;
    if (dest) dest.appendChild(node);
  }
  function unmount() {
    if (node.parentNode) node.parentNode.removeChild(node);
  }
  mount();
  return {
    update(newTarget) { target = newTarget; unmount(); mount(); },
    destroy() { unmount(); },
  };
}
