<script>
  /**
   * Sparkline · Gráfica inline SVG
   * ───────────────────────────────
   * Uso:
   *   <Sparkline data={[3,5,2,8,4,6,7]} />
   *   <Sparkline data={cpuHistory} variant="accent" filled />
   *
   * Props:
   *   - data:      number[] — valores (se normalizan entre min/max del array)
   *   - variant:   'accent' | 'warn' | 'crit' | 'info' | 'dim' (default accent)
   *   - filled:    boolean — rellena el área bajo la línea
   *   - width:     número (default 100)
   *   - height:    número (default 20)
   *   - min:       opcional — fija el mínimo (default: min(data))
   *   - max:       opcional — fija el máximo (default: max(data))
   */
  export let data = [];
  export let variant = 'accent';
  export let filled = false;
  export let width = 100;
  export let height = 20;
  export let min = null;
  export let max = null;

  $: points = (() => {
    if (!data || data.length === 0) return '';
    const lo = min !== null ? min : Math.min(...data);
    const hi = max !== null ? max : Math.max(...data);
    const range = hi - lo || 1;
    const step = data.length > 1 ? width / (data.length - 1) : 0;
    return data.map((v, i) => {
      const x = i * step;
      const y = height - ((v - lo) / range) * height;
      return `${x},${y.toFixed(1)}`;
    }).join(' ');
  })();

  $: areaPoints = filled && points
    ? `${points} ${width},${height} 0,${height}`
    : '';

  $: strokeColor =
    variant === 'warn' ? 'var(--warn)' :
    variant === 'crit' ? 'var(--crit)' :
    variant === 'info' ? 'var(--info)' :
    variant === 'dim'  ? 'var(--fg-dim)' :
    'var(--accent)';

  $: fillColor =
    variant === 'warn' ? 'rgba(255, 184, 0, 0.08)' :
    variant === 'crit' ? 'rgba(255, 90, 90, 0.08)' :
    variant === 'info' ? 'rgba(77, 184, 255, 0.08)' :
    variant === 'dim'  ? 'rgba(136, 136, 136, 0.08)' :
    'var(--accent-dim)';
</script>

<svg
  class="sparkline"
  viewBox="0 0 {width} {height}"
  preserveAspectRatio="none"
  width="100%"
  {height}
>
  {#if filled && areaPoints}
    <polyline points={areaPoints} fill={fillColor} stroke="none" />
  {/if}
  {#if points}
    <polyline points={points} fill="none" stroke={strokeColor} stroke-width="1" />
  {/if}
</svg>

<style>
  .sparkline {
    display: block;
  }
</style>
