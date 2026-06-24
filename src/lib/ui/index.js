/**
 * NimOS Beta 8.1 · Design System v3 Primitives
 * ────────────────────────────────────────────
 * Punto de entrada de las primitivas UI.
 *
 * Uso típico en una app:
 *   import { BevelButton, KPICard, LED, SectionHead, DenseTable } from '$lib/ui';
 *
 * Catálogo:
 *   - LED              · indicador pulsante
 *   - KeyBind          · badge de atajo [K]
 *   - Badge            · pill de estado/contador
 *   - BevelButton      · botón sistema D
 *   - IconButton       · botón cuadrado icon-only
 *   - TextInput        · input con bevel (search, text, etc.)
 *   - Sparkline        · gráfica inline SVG
 *   - KPICard          · KPI con corner brackets (legacy)
 *   - StatCard         · tarjeta de estadística v3 (borde de color + valor mono)
 *   - DataTable        · tabla densa v3 (cabecera mono, filas con separador)
 *   - SectionHead      · título de sección asimétrico
 *   - DenseTable       · tabla densa con columnas configurables
 *   - StripeProgressBar· barra con stripes animadas
 *   - CmdOutputLog     · viewer de logs estilo journalctl
 *   - EmptyState       · estado vacío genérico
 *   - Spinner          · spinner ASCII retro
 *   - Tab              · pestaña individual
 *   - Footer           · footer denso de ventana
 *   - AppIcon          · icono de app con marco HUD (xs/sm/md/lg)
 *   - ConfirmDialog    · modal de confirmación (simple o con input-to-confirm)
 *   - WizardFrame      · frame de wizard multipaso (Design System Beta 8.1)
 */

export { default as LED }               from './LED.svelte';
export { default as KeyBind }           from './KeyBind.svelte';
export { default as Badge }             from './Badge.svelte';
export { default as BevelButton }       from './BevelButton.svelte';
export { default as IconButton }        from './IconButton.svelte';
export { default as TextInput }         from './TextInput.svelte';
export { default as Sparkline }         from './Sparkline.svelte';
export { default as KPICard }           from './KPICard.svelte';
export { default as StatCard }          from './StatCard.svelte';
export { default as DataTable }         from './DataTable.svelte';
export { default as FilterBar }         from './FilterBar.svelte';
export { default as SectionHead }       from './SectionHead.svelte';
export { default as DenseTable }        from './DenseTable.svelte';
export { default as StripeProgressBar } from './StripeProgressBar.svelte';
export { default as CmdOutputLog }      from './CmdOutputLog.svelte';
export { default as EmptyState }        from './EmptyState.svelte';
export { default as Spinner }           from './Spinner.svelte';
export { default as Tab }               from './Tab.svelte';
export { default as Footer }            from './Footer.svelte';
export { default as AppIcon }           from './AppIcon.svelte';
export { default as ConfirmDialog }     from './ConfirmDialog.svelte';
export { default as WizardFrame }       from './WizardFrame.svelte';
