# NimOS Notification System — Fase 1: Sistema Base

## Objetivo

Crear el sistema de notificaciones toast de NimOS. En esta fase SOLO se crean los archivos nuevos y se monta el componente. NO se integra con ninguna app todavía — eso se hará después por separado.

## Archivos a crear (NUEVOS)

### 1. `src/lib/stores/notifications.js`

Store de Svelte para gestionar notificaciones.

```javascript
import { writable } from 'svelte/store';

export const notifications = writable([]);

let nextId = 0;

export function notify(message, type = 'info', duration = 5000) {
  const id = ++nextId;
  notifications.update(n => [...n, { id, type, message, duration, timestamp: Date.now() }]);
  if (duration > 0) {
    setTimeout(() => dismiss(id), duration);
  }
  return id;
}

export function notifySuccess(message) { return notify(message, 'success', 4000); }
export function notifyError(message) { return notify(message, 'error', 8000); }
export function notifyWarning(message) { return notify(message, 'warning', 6000); }
export function notifyInfo(message) { return notify(message, 'info', 5000); }

export function dismiss(id) {
  notifications.update(n => n.filter(x => x.id !== id));
}
```

### 2. `src/lib/components/ToastContainer.svelte`

Componente visual que renderiza las notificaciones.

#### Requisitos de diseño:
- **Posición**: fixed, bottom-right, encima del taskbar: `bottom: calc(var(--taskbar-height, 48px) + 16px); right: 16px`
- **z-index**: 9999 (encima de todo, incluidas ventanas)
- **Stack**: vertical, gap 8px, más recientes abajo
- **Máximo**: 5 toasts visibles (si hay más, el más antiguo desaparece)
- **Cada toast**:
  - Fondo: `var(--bg-inner)` con border `var(--border)`
  - Border-radius: 10px
  - Padding: 12px 16px
  - Shadow: `0 8px 32px rgba(0,0,0,0.3)`
  - Ancho: 320px max
  - Layout: icono izquierda + mensaje + botón X derecha
  - Font-size mensaje: 12px, color `var(--text-1)`
  - Borde izquierdo de 3px con color según tipo

#### Colores por tipo:
- `success`: borde izquierdo `var(--green)`, icono check verde
- `error`: borde izquierdo `var(--red)`, icono X rojo
- `warning`: borde izquierdo `var(--amber)`, icono triangulo ámbar
- `info`: borde izquierdo `var(--accent)`, icono i accent

#### Animaciones:
- Entrada: slide desde la derecha (`translateX(120%) → translateX(0)`) en 0.3s
- Salida: fade out + slide derecha en 0.2s
- Usar CSS transitions o Svelte `transition:fly`

#### Botón cerrar (X):
- Pequeño, esquina derecha del toast
- Al hacer clic llama `dismiss(id)`

### 3. Montaje en Desktop

Añadir `<ToastContainer />` en el componente que renderiza el escritorio. Buscar en:
- `src/lib/components/Desktop.svelte`
- o `src/routes/+page.svelte`

Añadir al FINAL del template, después de todo lo demás:
```svelte
<script>
  import ToastContainer from '$lib/components/ToastContainer.svelte';
</script>

<!-- al final del template -->
<ToastContainer />
```

## REGLAS CRÍTICAS

1. **SOLO crear archivos nuevos** — `notifications.js` y `ToastContainer.svelte` son archivos NUEVOS
2. **El ÚNICO archivo existente que se toca** es Desktop.svelte o +page.svelte — SOLO para añadir el import y el componente
3. **NO tocar FileManager.svelte**
4. **NO tocar NimBackup.svelte**
5. **NO tocar ningún otro componente existente**
6. **NO tocar archivos Go del daemon**
7. **Usar SOLO CSS variables de NimOS** definidas en `src/app.css`
8. **NO inventar colores nuevos**
9. **Font family**: heredado del sistema

## Resultado esperado

- No cambia nada visible en NimOS (no hay notificaciones activas todavía)
- El sistema está listo para que otros módulos importen y usen `notifyError()`, `notifySuccess()`, etc.
- Los toasts aparecerán en la esquina inferior derecha, encima del taskbar
